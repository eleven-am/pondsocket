use std::collections::HashMap;
use std::sync::Arc;

use tokio::sync::RwLock;

use crate::channel::{
    Channel, ChannelConfig, EventHandler, EventRegistration, LeaveHandler, OutgoingHandler,
    OutgoingRegistration,
};
use crate::errors::Result;
use crate::pubsub::PubSub;
use crate::types::Options;

pub struct Lobby {
    path: String,
    endpoint_path: String,
    options: Options,
    pubsub: Option<Arc<dyn PubSub>>,
    channels: RwLock<HashMap<String, Arc<Channel>>>,
    event_handlers: RwLock<Vec<EventRegistration>>,
    outgoing_handlers: RwLock<Vec<OutgoingRegistration>>,
    leave_handler: RwLock<Option<Arc<dyn LeaveHandler>>>,
}

impl Lobby {
    pub fn new(
        path: impl Into<String>,
        endpoint_path: impl Into<String>,
        options: Options,
        pubsub: Option<Arc<dyn PubSub>>,
    ) -> Arc<Self> {
        Arc::new(Self {
            path: path.into(),
            endpoint_path: endpoint_path.into(),
            options,
            pubsub,
            channels: RwLock::new(HashMap::new()),
            event_handlers: RwLock::new(Vec::new()),
            outgoing_handlers: RwLock::new(Vec::new()),
            leave_handler: RwLock::new(None),
        })
    }

    pub fn path(&self) -> &str {
        &self.path
    }

    pub async fn on_message<H>(&self, pattern: impl Into<String>, handler: H)
    where
        H: EventHandler + 'static,
    {
        self.event_handlers.write().await.push(EventRegistration {
            pattern: pattern.into(),
            handler: Arc::new(handler),
        });
    }

    pub async fn on_outgoing<H>(&self, pattern: impl Into<String>, handler: H)
    where
        H: OutgoingHandler + 'static,
    {
        self.outgoing_handlers
            .write()
            .await
            .push(OutgoingRegistration {
                pattern: pattern.into(),
                handler: Arc::new(handler),
            });
    }

    pub async fn on_leave<H>(&self, handler: H)
    where
        H: LeaveHandler + 'static,
    {
        *self.leave_handler.write().await = Some(Arc::new(handler));
    }

    pub async fn get_channel(&self, name: &str) -> Option<Arc<Channel>> {
        self.channels.read().await.get(name).cloned()
    }

    pub async fn get_or_create_channel(self: &Arc<Self>, name: &str) -> Result<Arc<Channel>> {
        let mut channels = self.channels.write().await;
        self.get_or_create_locked(&mut channels, name).await
    }

    pub async fn acquire_for_join(self: &Arc<Self>, name: &str) -> Result<Arc<Channel>> {
        let mut channels = self.channels.write().await;
        let channel = self.get_or_create_locked(&mut channels, name).await?;
        channel.begin_join();
        Ok(channel)
    }

    pub async fn finish_join(self: &Arc<Self>, channel: Arc<Channel>) {
        channel.end_join();
        self.remove_channel_if_idle(&channel.name).await;
    }

    pub async fn remove_channel_if_idle(self: &Arc<Self>, name: &str) {
        let mut channels = self.channels.write().await;
        let Some(channel) = channels.get(name).cloned() else {
            return;
        };
        if !channel.is_idle().await {
            return;
        }
        channels.remove(name);
        drop(channels);
        let _ = channel.close().await;
    }

    async fn get_or_create_locked(
        self: &Arc<Self>,
        channels: &mut HashMap<String, Arc<Channel>>,
        name: &str,
    ) -> Result<Arc<Channel>> {
        if let Some(channel) = channels.get(name) {
            return Ok(channel.clone());
        }
        let channel = Channel::new(ChannelConfig {
            name: name.to_owned(),
            endpoint_path: self.endpoint_path.clone(),
            options: self.options.clone(),
            pubsub: self.pubsub.clone(),
            event_handlers: self.event_handlers.read().await.clone(),
            outgoing_handlers: self.outgoing_handlers.read().await.clone(),
            leave_handler: self.leave_handler.read().await.clone(),
            lobby: Arc::downgrade(self),
        });
        channel.start().await?;
        channels.insert(name.to_owned(), channel.clone());
        Ok(channel)
    }

    pub async fn list_channels(&self) -> Vec<Arc<Channel>> {
        self.channels.read().await.values().cloned().collect()
    }

    pub async fn close(&self) -> Result<()> {
        let channels = self.list_channels().await;
        for channel in channels {
            let _ = channel.close().await;
        }
        self.channels.write().await.clear();
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::transport::MemoryTransport;
    use pondsocket_common::PondAssigns;

    #[tokio::test]
    async fn adv_channel_is_removed_after_last_user_leaves() {
        let lobby = Lobby::new("/room", "/", Options::default(), None);
        let channel = lobby.get_or_create_channel("/room/1").await.unwrap();
        let transport = Arc::new(MemoryTransport::new("u1", PondAssigns::new()));
        channel.add_user(transport).await.unwrap();
        assert_eq!(channel.user_count().await, 1);

        channel.remove_user("u1", "explicit_leave").await.unwrap();
        assert_eq!(channel.user_count().await, 0);

        assert!(lobby.list_channels().await.is_empty());
        assert!(lobby.get_channel("/room/1").await.is_none());
    }

    #[tokio::test]
    async fn adv_declined_join_leaves_no_running_channel() {
        let lobby = Lobby::new("/room", "/", Options::default(), None);
        let channel = lobby.acquire_for_join("/room/1").await.unwrap();
        lobby.finish_join(channel).await;

        assert!(lobby.get_channel("/room/1").await.is_none());
    }

    #[tokio::test]
    async fn adv_concurrent_join_never_rejected_during_teardown() {
        let lobby = Lobby::new("/room", "/", Options::default(), None);
        let mut handles = Vec::new();
        for i in 0..256 {
            let lobby = lobby.clone();
            handles.push(tokio::spawn(async move {
                let user_id = format!("u{i}");
                let channel = lobby.acquire_for_join("/room/1").await.unwrap();
                let transport = Arc::new(MemoryTransport::new(user_id.clone(), PondAssigns::new()));
                let added = channel.add_user(transport).await;
                channel
                    .remove_user(&user_id, "explicit_leave")
                    .await
                    .unwrap();
                lobby.finish_join(channel).await;
                added
            }));
        }
        for handle in handles {
            handle
                .await
                .unwrap()
                .expect("a legitimate join was rejected while a channel was torn down");
        }
        assert!(lobby.get_channel("/room/1").await.is_none());
    }

    #[derive(Default)]
    struct CountingPubSub {
        counts: std::sync::Mutex<HashMap<String, usize>>,
        next_id: std::sync::atomic::AtomicU64,
    }

    impl CountingPubSub {
        fn count(&self, pattern: &str) -> usize {
            *self.counts.lock().unwrap().get(pattern).unwrap_or(&0)
        }
    }

    #[async_trait::async_trait]
    impl crate::pubsub::PubSub for CountingPubSub {
        async fn subscribe(
            &self,
            pattern: &str,
            _handler: crate::pubsub::PubSubHandler,
        ) -> Result<crate::pubsub::SubscriptionId> {
            *self
                .counts
                .lock()
                .unwrap()
                .entry(pattern.to_owned())
                .or_insert(0) += 1;
            Ok(self
                .next_id
                .fetch_add(1, std::sync::atomic::Ordering::SeqCst))
        }

        async fn unsubscribe(
            &self,
            pattern: &str,
            _id: crate::pubsub::SubscriptionId,
        ) -> Result<()> {
            let mut counts = self.counts.lock().unwrap();
            if let Some(count) = counts.get_mut(pattern) {
                *count -= 1;
                if *count == 0 {
                    counts.remove(pattern);
                }
            }
            Ok(())
        }

        async fn publish(&self, _topic: &str, _data: Vec<u8>) -> Result<()> {
            Ok(())
        }

        async fn close(&self) -> Result<()> {
            Ok(())
        }
    }

    #[tokio::test]
    async fn adv_channel_teardown_keeps_other_heartbeat_subscription() {
        let pubsub = Arc::new(CountingPubSub::default());
        let dyn_pubsub: Arc<dyn PubSub> = pubsub.clone();
        let lobby = Lobby::new("/room", "/api", Options::default(), Some(dyn_pubsub));
        let _a = lobby.get_or_create_channel("/room/a").await.unwrap();
        let _b = lobby.get_or_create_channel("/room/b").await.unwrap();

        let heartbeat = crate::pubsub::format_heartbeat_topic("default");
        assert_eq!(pubsub.count(&heartbeat), 2);

        lobby.remove_channel_if_idle("/room/a").await;

        assert!(lobby.get_channel("/room/a").await.is_none());
        assert!(lobby.get_channel("/room/b").await.is_some());
        assert_eq!(pubsub.count(&heartbeat), 1);
    }

    #[tokio::test]
    async fn adv_sequential_recreate_after_teardown_resubscribes() {
        let pubsub = Arc::new(CountingPubSub::default());
        let dyn_pubsub: Arc<dyn PubSub> = pubsub.clone();
        let lobby = Lobby::new("/room", "/api", Options::default(), Some(dyn_pubsub));

        let _first = lobby.get_or_create_channel("/room/x").await.unwrap();
        let heartbeat = crate::pubsub::format_heartbeat_topic("default");
        assert_eq!(pubsub.count(&heartbeat), 1);

        lobby.remove_channel_if_idle("/room/x").await;
        assert_eq!(pubsub.count(&heartbeat), 0);

        let _second = lobby.get_or_create_channel("/room/x").await.unwrap();
        assert_eq!(pubsub.count(&heartbeat), 1);
    }

    #[tokio::test]
    async fn adv_eviction_of_last_user_delivers_evicted_then_tears_down() {
        let lobby = Lobby::new("/room", "/", Options::default(), None);
        let channel = lobby.get_or_create_channel("/room/1").await.unwrap();
        let transport = Arc::new(MemoryTransport::new("u1", PondAssigns::new()));
        channel.add_user(transport.clone()).await.unwrap();

        channel.evict_user("u1", "kicked").await.unwrap();

        let event = transport
            .recv()
            .await
            .expect("no frame delivered on eviction");
        assert_eq!(event.event, "EVICTED");
        assert!(lobby.get_channel("/room/1").await.is_none());
    }

    #[tokio::test]
    async fn adv_teardown_stress_never_deadlocks() {
        let lobby = Lobby::new("/room", "/", Options::default(), None);
        let run = async {
            let mut handles = Vec::new();
            for i in 0..128 {
                let lobby = lobby.clone();
                handles.push(tokio::spawn(async move {
                    let user_id = format!("u{i}");
                    let channel = lobby.acquire_for_join("/room/1").await.unwrap();
                    let transport =
                        Arc::new(MemoryTransport::new(user_id.clone(), PondAssigns::new()));
                    let _ = channel.add_user(transport).await;
                    let _ = channel.broadcast("ping", serde_json::json!({})).await;
                    let _ = lobby.get_channel("/room/1").await;
                    let _ = lobby.list_channels().await;
                    channel
                        .remove_user(&user_id, "explicit_leave")
                        .await
                        .unwrap();
                    lobby.finish_join(channel).await;
                }));
            }
            for handle in handles {
                handle.await.unwrap();
            }
        };
        tokio::time::timeout(std::time::Duration::from_secs(20), run)
            .await
            .expect("teardown stress deadlocked or hung");
        assert!(lobby.get_channel("/room/1").await.is_none());
    }
}
