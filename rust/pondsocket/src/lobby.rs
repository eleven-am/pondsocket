use std::collections::HashMap;
use std::sync::Arc;

use pondsocket_common::uuid;
use tokio::sync::{Mutex, RwLock};

use crate::channel::{
    Channel, ChannelConfig, DeliveryHub, EventHandler, EventRegistration, LeaveHandler,
    OutgoingHandler, OutgoingRegistration,
};
use crate::errors::{Result, internal};
use crate::liveness::NodeLiveness;
use crate::pubsub::PubSub;
use crate::types::Options;

pub struct Lobby {
    path: String,
    endpoint_path: String,
    options: Options,
    pubsub: Option<Arc<dyn PubSub>>,
    delivery_hub: Arc<DeliveryHub>,
    owns_delivery_hub: bool,
    liveness: Arc<NodeLiveness>,
    channels: RwLock<HashMap<String, Arc<Channel>>>,
    event_handlers: RwLock<Vec<EventRegistration>>,
    outgoing_handlers: RwLock<Vec<OutgoingRegistration>>,
    leave_handler: RwLock<Option<Arc<dyn LeaveHandler>>>,
    lifecycle: Mutex<LobbyLifecycle>,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum LobbyLifecycle {
    Open,
    Closing,
    Closed,
}

pub(crate) struct JoinPermit {
    channel: Arc<Channel>,
    lobby: std::sync::Weak<Lobby>,
    active: bool,
}

impl JoinPermit {
    pub(crate) fn channel(&self) -> Arc<Channel> {
        self.channel.clone()
    }

    pub(crate) async fn finish(mut self) {
        self.release();
        if let Some(lobby) = self.lobby.upgrade() {
            lobby.remove_channel_if_idle(&self.channel.name).await;
        }
    }

    fn release(&mut self) {
        if self.active {
            self.channel.end_join();
            self.active = false;
        }
    }
}

impl Drop for JoinPermit {
    fn drop(&mut self) {
        self.release();
        let Some(lobby) = self.lobby.upgrade() else {
            return;
        };
        let channel_name = self.channel.name.clone();
        if let Ok(handle) = tokio::runtime::Handle::try_current() {
            handle.spawn(async move {
                lobby.remove_channel_if_idle(&channel_name).await;
            });
        }
    }
}

impl Lobby {
    pub fn new(
        path: impl Into<String>,
        endpoint_path: impl Into<String>,
        options: Options,
        pubsub: Option<Arc<dyn PubSub>>,
    ) -> Arc<Self> {
        let mut options = options.normalized();
        if pubsub.is_some() && options.node_id.is_empty() {
            options.node_id = uuid();
        }
        let delivery_hub = DeliveryHub::new(options.internal_queue_buffer);
        let liveness = NodeLiveness::new(&options, pubsub.clone());
        Self::new_inner(
            path,
            endpoint_path,
            options,
            pubsub,
            delivery_hub,
            liveness,
            true,
        )
    }

    pub(crate) fn new_with_delivery_hub(
        path: impl Into<String>,
        endpoint_path: impl Into<String>,
        options: Options,
        pubsub: Option<Arc<dyn PubSub>>,
        delivery_hub: Arc<DeliveryHub>,
        liveness: Arc<NodeLiveness>,
    ) -> Arc<Self> {
        Self::new_inner(
            path,
            endpoint_path,
            options,
            pubsub,
            delivery_hub,
            liveness,
            false,
        )
    }

    fn new_inner(
        path: impl Into<String>,
        endpoint_path: impl Into<String>,
        options: Options,
        pubsub: Option<Arc<dyn PubSub>>,
        delivery_hub: Arc<DeliveryHub>,
        liveness: Arc<NodeLiveness>,
        owns_delivery_hub: bool,
    ) -> Arc<Self> {
        Arc::new(Self {
            path: path.into(),
            endpoint_path: endpoint_path.into(),
            options,
            pubsub,
            delivery_hub,
            owns_delivery_hub,
            liveness,
            channels: RwLock::new(HashMap::new()),
            event_handlers: RwLock::new(Vec::new()),
            outgoing_handlers: RwLock::new(Vec::new()),
            leave_handler: RwLock::new(None),
            lifecycle: Mutex::new(LobbyLifecycle::Open),
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
        let lifecycle = self.lifecycle.lock().await;
        if *lifecycle != LobbyLifecycle::Open {
            return Err(internal(&self.path, "lobby is closing or closed"));
        }
        let mut channels = self.channels.write().await;
        let result = self.get_or_create_locked(&mut channels, name).await;
        drop(lifecycle);
        result
    }

    pub async fn acquire_for_join(self: &Arc<Self>, name: &str) -> Result<Arc<Channel>> {
        let lifecycle = self.lifecycle.lock().await;
        if *lifecycle != LobbyLifecycle::Open {
            return Err(internal(&self.path, "lobby is closing or closed"));
        }
        let mut channels = self.channels.write().await;
        let channel = self.get_or_create_locked(&mut channels, name).await?;
        channel.begin_join();
        drop(lifecycle);
        Ok(channel)
    }

    pub(crate) async fn acquire_join_permit(self: &Arc<Self>, name: &str) -> Result<JoinPermit> {
        let lifecycle = self.lifecycle.lock().await;
        if *lifecycle != LobbyLifecycle::Open {
            return Err(internal(&self.path, "lobby is closing or closed"));
        }
        let mut channels = self.channels.write().await;
        let channel = self.get_or_create_locked(&mut channels, name).await?;
        channel.begin_join();
        drop(lifecycle);
        Ok(JoinPermit {
            channel,
            lobby: Arc::downgrade(self),
            active: true,
        })
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

    pub(crate) async fn remove_failed_channel(self: &Arc<Self>, name: &str, failed: &Arc<Channel>) {
        {
            let mut channels = self.channels.write().await;
            if channels
                .get(name)
                .is_some_and(|current| Arc::ptr_eq(current, failed))
            {
                channels.remove(name);
            }
        }
        let _ = failed.close().await;
    }

    async fn get_or_create_locked(
        self: &Arc<Self>,
        channels: &mut HashMap<String, Arc<Channel>>,
        name: &str,
    ) -> Result<Arc<Channel>> {
        if let Some(channel) = channels.get(name) {
            return Ok(channel.clone());
        }
        let channel = Channel::new_with_runtime(
            ChannelConfig {
                name: name.to_owned(),
                endpoint_path: self.endpoint_path.clone(),
                options: self.options.clone(),
                pubsub: self.pubsub.clone(),
                event_handlers: self.event_handlers.read().await.clone(),
                outgoing_handlers: self.outgoing_handlers.read().await.clone(),
                leave_handler: self.leave_handler.read().await.clone(),
                lobby: Arc::downgrade(self),
            },
            self.delivery_hub.clone(),
            self.liveness.clone(),
        );
        channel.start().await?;
        channels.insert(name.to_owned(), channel.clone());
        Ok(channel)
    }

    pub async fn list_channels(&self) -> Vec<Arc<Channel>> {
        self.channels.read().await.values().cloned().collect()
    }

    pub async fn close(&self) -> Result<()> {
        let mut lifecycle = self.lifecycle.lock().await;
        if *lifecycle == LobbyLifecycle::Closed {
            return Ok(());
        }
        *lifecycle = LobbyLifecycle::Closing;
        let channels = self.list_channels().await;
        for channel in channels {
            let _ = channel.close().await;
        }
        self.channels.write().await.clear();
        if self.owns_delivery_hub {
            self.delivery_hub.close().await;
        }
        *lifecycle = LobbyLifecycle::Closed;
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

        async fn subscribe_supervised(
            &self,
            topic: &str,
            handler: crate::pubsub::PubSubHandler,
            _on_failure: crate::pubsub::PubSubFailureHandler,
        ) -> Result<crate::pubsub::SubscriptionId> {
            self.subscribe(topic, handler).await
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
        assert_eq!(pubsub.count(&heartbeat), 1);
        assert_eq!(lobby.liveness.task_count().await, 2);

        lobby.remove_channel_if_idle("/room/a").await;

        assert!(lobby.get_channel("/room/a").await.is_none());
        assert!(lobby.get_channel("/room/b").await.is_some());
        assert_eq!(pubsub.count(&heartbeat), 1);
        assert_eq!(lobby.liveness.task_count().await, 2);
    }

    #[tokio::test]
    async fn adv_many_channels_share_one_heartbeat_runtime() {
        let pubsub = Arc::new(CountingPubSub::default());
        let dyn_pubsub: Arc<dyn PubSub> = pubsub.clone();
        let lobby = Lobby::new("/room", "/api", Options::default(), Some(dyn_pubsub));

        for index in 0..128 {
            lobby
                .get_or_create_channel(&format!("/room/{index}"))
                .await
                .unwrap();
        }

        let heartbeat = crate::pubsub::format_heartbeat_topic("default");
        assert_eq!(pubsub.count(&heartbeat), 1);
        assert_eq!(lobby.liveness.task_count().await, 2);

        lobby.close().await.unwrap();
        assert_eq!(pubsub.count(&heartbeat), 0);
        assert_eq!(lobby.liveness.task_count().await, 0);
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

    #[tokio::test]
    async fn close_serializes_with_creation_and_rejects_post_close_channels() {
        let lobby = Lobby::new("/room", "/", Options::default(), None);
        let channels_guard = lobby.channels.write().await;
        let create_lobby = lobby.clone();
        let create =
            tokio::spawn(async move { create_lobby.get_or_create_channel("/room/racing").await });
        tokio::time::timeout(std::time::Duration::from_secs(1), async {
            loop {
                if lobby.lifecycle.try_lock().is_err() {
                    break;
                }
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("channel creation never acquired the lobby lifecycle lock");

        let close_lobby = lobby.clone();
        let close = tokio::spawn(async move { close_lobby.close().await });
        tokio::task::yield_now().await;
        assert!(!close.is_finished());
        drop(channels_guard);

        let channel = create.await.unwrap().unwrap();
        close.await.unwrap().unwrap();
        assert!(lobby.list_channels().await.is_empty());
        assert!(channel.start().await.is_err());
        assert!(lobby.get_or_create_channel("/room/late").await.is_err());
        lobby.close().await.unwrap();
    }
}
