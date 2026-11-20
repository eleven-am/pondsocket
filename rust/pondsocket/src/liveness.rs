use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Weak};
use std::time::Instant;

use tokio::sync::{Mutex, RwLock};
use tokio::task::JoinHandle;
use tokio::time::sleep;

use crate::channel::{Channel, heartbeat_to_bytes, parse_heartbeat_node};
use crate::errors::Result;
use crate::pubsub::{
    PubSub, PubSubFailureHandler, SubscriptionId, format_heartbeat_topic, pubsub_handler,
};
use crate::types::Options;

#[derive(Default)]
struct LivenessState {
    channels: Vec<Weak<Channel>>,
    subscription_id: Option<SubscriptionId>,
    heartbeat_task: Option<JoinHandle<()>>,
    stale_task: Option<JoinHandle<()>>,
}

/// Node liveness is shared by every channel in one server namespace. Keeping
/// this at server/lobby scope avoids one heartbeat publisher and subscription
/// per channel while still letting each channel own its remote membership.
pub(crate) struct NodeLiveness {
    pubsub: Option<Arc<dyn PubSub>>,
    namespace: String,
    node_id: String,
    heartbeat_interval: std::time::Duration,
    heartbeat_timeout: std::time::Duration,
    state: Mutex<LivenessState>,
    last_seen: RwLock<HashMap<String, Instant>>,
    generation: AtomicU64,
}

impl NodeLiveness {
    pub(crate) fn new(options: &Options, pubsub: Option<Arc<dyn PubSub>>) -> Arc<Self> {
        Arc::new(Self {
            pubsub,
            namespace: options.namespace.clone(),
            node_id: options.node_id.clone(),
            heartbeat_interval: options.heartbeat_interval,
            heartbeat_timeout: options.heartbeat_timeout,
            state: Mutex::new(LivenessState::default()),
            last_seen: RwLock::new(HashMap::new()),
            generation: AtomicU64::new(0),
        })
    }

    pub(crate) async fn register(self: &Arc<Self>, channel: &Arc<Channel>) -> Result<()> {
        let mut state = self.state.lock().await;
        state.channels.retain(|channel| channel.strong_count() > 0);
        let channel_ref = Arc::downgrade(channel);
        if !state.channels.iter().any(|item| item.ptr_eq(&channel_ref)) {
            state.channels.push(channel_ref.clone());
        }
        if self.pubsub.is_none() || state.subscription_id.is_some() {
            return Ok(());
        }

        let pubsub = self.pubsub.as_ref().expect("checked above").clone();
        let heartbeat_topic = format_heartbeat_topic(&self.namespace);
        let weak = Arc::downgrade(self);
        let failed = Arc::downgrade(self);
        let on_failure: PubSubFailureHandler = Arc::new(move || {
            let Some(liveness) = failed.upgrade() else {
                return;
            };
            tokio::spawn(async move {
                liveness.fail_subscription().await;
            });
        });
        let subscription_id = match pubsub
            .subscribe_supervised(
                &heartbeat_topic,
                pubsub_handler(move |_topic, data| {
                    let weak = weak.clone();
                    async move {
                        if let Some(liveness) = weak.upgrade() {
                            liveness.handle_heartbeat(&data).await;
                        }
                    }
                }),
                on_failure,
            )
            .await
        {
            Ok(id) => id,
            Err(error) => {
                state.channels.retain(|item| !item.ptr_eq(&channel_ref));
                return Err(error);
            }
        };

        let generation = self.generation.fetch_add(1, Ordering::SeqCst) + 1;
        let heartbeat_weak = Arc::downgrade(self);
        let heartbeat_pubsub = pubsub.clone();
        let publish_topic = heartbeat_topic.clone();
        let heartbeat_task = tokio::spawn(async move {
            loop {
                let Some(liveness) = heartbeat_weak.upgrade() else {
                    break;
                };
                if liveness.generation.load(Ordering::SeqCst) != generation {
                    break;
                }
                let node_id = liveness.node_id.clone();
                let heartbeat_interval = liveness.heartbeat_interval;
                drop(liveness);
                let _ = heartbeat_pubsub
                    .publish(&publish_topic, heartbeat_to_bytes(&node_id))
                    .await;
                sleep(heartbeat_interval).await;
            }
        });

        let stale_weak = Arc::downgrade(self);
        let heartbeat_timeout = self.heartbeat_timeout;
        let stale_task = tokio::spawn(async move {
            loop {
                sleep(heartbeat_timeout).await;
                let Some(liveness) = stale_weak.upgrade() else {
                    break;
                };
                if liveness.generation.load(Ordering::SeqCst) != generation {
                    break;
                }
                liveness.cleanup_stale_nodes().await;
            }
        });

        state.subscription_id = Some(subscription_id);
        state.heartbeat_task = Some(heartbeat_task);
        state.stale_task = Some(stale_task);
        Ok(())
    }

    pub(crate) async fn unregister(&self, channel: &Channel) -> Result<()> {
        let mut state = self.state.lock().await;
        state.channels.retain(|item| {
            item.strong_count() > 0 && !std::ptr::eq(item.as_ptr(), channel as *const Channel)
        });
        if !state.channels.is_empty() {
            return Ok(());
        }
        self.stop_locked(&mut state).await
    }

    pub(crate) async fn close(&self) -> Result<()> {
        let mut state = self.state.lock().await;
        state.channels.clear();
        self.stop_locked(&mut state).await
    }

    async fn stop_locked(&self, state: &mut LivenessState) -> Result<()> {
        self.generation.fetch_add(1, Ordering::SeqCst);
        let heartbeat_task = state.heartbeat_task.take();
        let stale_task = state.stale_task.take();
        if let Some(task) = heartbeat_task {
            task.abort();
            let _ = task.await;
        }
        if let Some(task) = stale_task {
            task.abort();
            let _ = task.await;
        }
        self.last_seen.write().await.clear();
        let Some(subscription_id) = state.subscription_id.take() else {
            return Ok(());
        };
        let Some(pubsub) = &self.pubsub else {
            return Ok(());
        };
        pubsub
            .unsubscribe(&format_heartbeat_topic(&self.namespace), subscription_id)
            .await
    }

    async fn handle_heartbeat(&self, data: &[u8]) {
        if let Some(node_id) = parse_heartbeat_node(data)
            && !node_id.is_empty()
            && node_id != self.node_id
        {
            self.mark_seen(&node_id).await;
        }
    }

    async fn fail_subscription(self: Arc<Self>) {
        let channels = {
            let state = self.state.lock().await;
            state
                .channels
                .iter()
                .filter_map(Weak::upgrade)
                .collect::<Vec<_>>()
        };
        for channel in channels {
            channel.fail_pubsub_subscription().await;
        }
    }

    pub(crate) async fn mark_seen(&self, node_id: &str) {
        if !node_id.is_empty() && node_id != self.node_id {
            self.last_seen
                .write()
                .await
                .insert(node_id.to_owned(), Instant::now());
        }
    }

    async fn cleanup_stale_nodes(&self) {
        let now = Instant::now();
        let stale = {
            let mut last_seen = self.last_seen.write().await;
            let stale = last_seen
                .iter()
                .filter(|(_, seen)| now.duration_since(**seen) >= self.heartbeat_timeout)
                .map(|(node, _)| node.clone())
                .collect::<Vec<_>>();
            for node in &stale {
                last_seen.remove(node);
            }
            stale
        };
        if stale.is_empty() {
            return;
        }
        let channels = {
            let mut state = self.state.lock().await;
            state.channels.retain(|channel| channel.strong_count() > 0);
            state
                .channels
                .iter()
                .filter_map(Weak::upgrade)
                .collect::<Vec<_>>()
        };
        for node in stale {
            for channel in &channels {
                channel.expire_remote_node(&node).await;
            }
        }
    }

    #[cfg(test)]
    pub(crate) async fn task_count(&self) -> usize {
        let state = self.state.lock().await;
        usize::from(state.heartbeat_task.is_some()) + usize::from(state.stale_task.is_some())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::channel::{Channel, ChannelConfig, DeliveryHub};
    use crate::pubsub::LocalPubSub;
    use std::time::Duration;

    #[tokio::test]
    async fn drop_without_close_releases_liveness_and_background_tasks() {
        let pubsub: Arc<dyn PubSub> = Arc::new(LocalPubSub::new(8));
        let options = Options {
            node_id: "node-a".to_owned(),
            heartbeat_interval: Duration::from_secs(60),
            heartbeat_timeout: Duration::from_secs(120),
            ..Options::default()
        };
        let liveness = NodeLiveness::new(&options, Some(pubsub));
        let channel = Channel::new_with_runtime(
            ChannelConfig {
                name: "/room/1".to_owned(),
                endpoint_path: "/socket".to_owned(),
                options,
                pubsub: None,
                event_handlers: Vec::new(),
                outgoing_handlers: Vec::new(),
                leave_handler: None,
                lobby: Weak::new(),
            },
            DeliveryHub::new(8),
            liveness.clone(),
        );
        liveness.register(&channel).await.unwrap();
        let weak = Arc::downgrade(&liveness);

        drop(channel);
        drop(liveness);

        tokio::time::timeout(Duration::from_secs(1), async {
            while weak.upgrade().is_some() {
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("liveness tasks retained their owner after drop");
    }
}
