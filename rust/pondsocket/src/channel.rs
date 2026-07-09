use async_trait::async_trait;
use pondsocket_common::{PondAssigns, PresenceEventType, ServerAction, uuid};
use serde_json::{Value, json};
use std::collections::{HashMap, HashSet};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Weak};
use std::time::Instant;
use tokio::sync::{RwLock, Semaphore, mpsc};
use tokio::task::JoinHandle;
use tokio::time::{sleep, timeout};

use crate::contexts::{EventContext, LeaveContext, OutgoingContext};
use crate::errors::{Result, conflict, internal, not_found};
use crate::lobby::Lobby;
use crate::parser::parse;
use crate::pubsub::{PubSub, SubscriptionId, format_heartbeat_topic, format_topic};
use crate::transport::Transport;
use crate::types::{Event, Options, User};

#[async_trait]
pub trait EventHandler: Send + Sync {
    async fn call(&self, ctx: &mut EventContext) -> Result<()>;
}

#[async_trait]
impl<F, Fut> EventHandler for F
where
    F: Fn(&mut EventContext) -> Fut + Send + Sync,
    Fut: std::future::Future<Output = Result<()>> + Send,
{
    async fn call(&self, ctx: &mut EventContext) -> Result<()> {
        self(ctx).await
    }
}

#[async_trait]
pub trait OutgoingHandler: Send + Sync {
    async fn call(&self, ctx: &mut OutgoingContext) -> Result<()>;
}

#[async_trait]
impl<F, Fut> OutgoingHandler for F
where
    F: Fn(&mut OutgoingContext) -> Fut + Send + Sync,
    Fut: std::future::Future<Output = Result<()>> + Send,
{
    async fn call(&self, ctx: &mut OutgoingContext) -> Result<()> {
        self(ctx).await
    }
}

#[async_trait]
pub trait LeaveHandler: Send + Sync {
    async fn call(&self, ctx: &mut LeaveContext) -> Result<()>;
}

#[async_trait]
impl<F, Fut> LeaveHandler for F
where
    F: Fn(&mut LeaveContext) -> Fut + Send + Sync,
    Fut: std::future::Future<Output = Result<()>> + Send,
{
    async fn call(&self, ctx: &mut LeaveContext) -> Result<()> {
        self(ctx).await
    }
}

#[derive(Clone)]
pub struct EventRegistration {
    pub pattern: String,
    pub handler: Arc<dyn EventHandler>,
}

#[derive(Clone)]
pub struct OutgoingRegistration {
    pub pattern: String,
    pub handler: Arc<dyn OutgoingHandler>,
}

struct DispatchItem {
    event: Event,
    recipients: Vec<String>,
}

pub struct Channel {
    pub name: String,
    endpoint_path: String,
    node_id: String,
    options: Options,
    pubsub: Option<Arc<dyn PubSub>>,
    connections: RwLock<HashMap<String, Arc<dyn Transport>>>,
    assigns: RwLock<HashMap<String, pondsocket_common::PondAssigns>>,
    presence: RwLock<HashMap<String, pondsocket_common::PondPresence>>,
    event_handlers: Vec<EventRegistration>,
    outgoing_handlers: Vec<OutgoingRegistration>,
    leave_handler: Option<Arc<dyn LeaveHandler>>,
    dispatch_tx: mpsc::Sender<DispatchItem>,
    dispatch_rx: RwLock<Option<mpsc::Receiver<DispatchItem>>>,
    dispatch_task: RwLock<Option<JoinHandle<()>>>,
    dispatch_semaphore: Arc<Semaphore>,
    active: RwLock<bool>,
    node_last_seen: RwLock<HashMap<String, Instant>>,
    node_users: RwLock<HashMap<String, HashSet<String>>>,
    heartbeat_task: RwLock<Option<JoinHandle<()>>>,
    stale_task: RwLock<Option<JoinHandle<()>>>,
    lobby: Weak<Lobby>,
    in_flight_joins: AtomicUsize,
    pubsub_sub_id: RwLock<Option<SubscriptionId>>,
    heartbeat_sub_id: RwLock<Option<SubscriptionId>>,
}

pub struct ChannelConfig {
    pub name: String,
    pub endpoint_path: String,
    pub options: Options,
    pub pubsub: Option<Arc<dyn PubSub>>,
    pub event_handlers: Vec<EventRegistration>,
    pub outgoing_handlers: Vec<OutgoingRegistration>,
    pub leave_handler: Option<Arc<dyn LeaveHandler>>,
    pub lobby: Weak<Lobby>,
}

impl Channel {
    pub fn new(config: ChannelConfig) -> Arc<Self> {
        let (dispatch_tx, dispatch_rx) = mpsc::channel(config.options.internal_queue_buffer);
        Arc::new(Self {
            name: config.name,
            endpoint_path: config.endpoint_path,
            node_id: config.options.node_id.clone(),
            dispatch_semaphore: Arc::new(Semaphore::new(
                config.options.dispatch_concurrency.max(1),
            )),
            options: config.options,
            pubsub: config.pubsub,
            connections: RwLock::new(HashMap::new()),
            assigns: RwLock::new(HashMap::new()),
            presence: RwLock::new(HashMap::new()),
            event_handlers: config.event_handlers,
            outgoing_handlers: config.outgoing_handlers,
            leave_handler: config.leave_handler,
            dispatch_tx,
            dispatch_rx: RwLock::new(Some(dispatch_rx)),
            dispatch_task: RwLock::new(None),
            active: RwLock::new(false),
            node_last_seen: RwLock::new(HashMap::new()),
            node_users: RwLock::new(HashMap::new()),
            heartbeat_task: RwLock::new(None),
            stale_task: RwLock::new(None),
            lobby: config.lobby,
            in_flight_joins: AtomicUsize::new(0),
            pubsub_sub_id: RwLock::new(None),
            heartbeat_sub_id: RwLock::new(None),
        })
    }

    pub fn begin_join(&self) {
        self.in_flight_joins.fetch_add(1, Ordering::SeqCst);
    }

    pub fn end_join(&self) {
        self.in_flight_joins.fetch_sub(1, Ordering::SeqCst);
    }

    pub async fn is_idle(&self) -> bool {
        self.user_count().await == 0
            && self.in_flight_joins.load(Ordering::SeqCst) == 0
            && self.node_users.read().await.values().all(HashSet::is_empty)
    }

    pub async fn start(self: &Arc<Self>) -> Result<()> {
        *self.active.write().await = true;
        self.subscribe_to_pubsub().await?;
        self.start_liveness().await?;
        if let Some(mut rx) = self.dispatch_rx.write().await.take() {
            let channel = self.clone();
            let task = tokio::spawn(async move {
                while let Some(item) = rx.recv().await {
                    if !*channel.active.read().await {
                        break;
                    }
                    channel.dispatch_item(item).await;
                }
            });
            *self.dispatch_task.write().await = Some(task);
        }
        if let Some(hooks) = &self.options.hooks
            && let Some(metrics) = &hooks.metrics
        {
            metrics.channel_created(&self.name);
        }
        Ok(())
    }

    pub async fn close(&self) -> Result<()> {
        *self.active.write().await = false;
        self.unsubscribe_from_pubsub().await?;
        if let Some(task) = self.dispatch_task.write().await.take() {
            task.abort();
        }
        if let Some(task) = self.heartbeat_task.write().await.take() {
            task.abort();
        }
        if let Some(task) = self.stale_task.write().await.take() {
            task.abort();
        }
        self.connections.write().await.clear();
        self.assigns.write().await.clear();
        self.presence.write().await.clear();
        self.node_last_seen.write().await.clear();
        self.node_users.write().await.clear();
        if let Some(hooks) = &self.options.hooks
            && let Some(metrics) = &hooks.metrics
        {
            metrics.channel_destroyed(&self.name);
        }
        Ok(())
    }

    pub async fn has_user(&self, user_id: &str) -> bool {
        self.connections.read().await.contains_key(user_id)
    }

    pub async fn user_count(&self) -> usize {
        self.connections.read().await.len()
    }

    pub async fn add_user(&self, transport: Arc<dyn Transport>) -> Result<()> {
        if !*self.active.read().await {
            return Err(internal(&self.name, "channel is not active"));
        }
        let user_id = transport.id().to_owned();
        let mut connections = self.connections.write().await;
        if connections.contains_key(&user_id) {
            return Err(conflict(&self.name, "user already exists in channel"));
        }
        let assigns = transport.clone_assigns().await;
        connections.insert(user_id.clone(), transport);
        drop(connections);
        self.assigns
            .write()
            .await
            .insert(user_id.clone(), assigns.clone());
        if let Some(hooks) = &self.options.hooks
            && let Some(metrics) = &hooks.metrics
        {
            metrics.channel_joined(&user_id, &self.name);
        }
        let _ = self.publish_user_joined(&user_id, &assigns).await;
        Ok(())
    }

    pub async fn remove_user(self: &Arc<Self>, user_id: &str, reason: &str) -> Result<()> {
        if self.connections.write().await.remove(user_id).is_none() {
            return Ok(());
        }
        let assigns = self
            .assigns
            .write()
            .await
            .remove(user_id)
            .unwrap_or_default();
        let presence = self.presence.write().await.remove(user_id);
        if let Some(old) = presence.clone() {
            self.publish_presence_change(user_id, PresenceEventType::Leave, old)
                .await?;
        }
        let _ = self.publish_user_left(user_id).await;
        let user = User {
            id: user_id.to_owned(),
            assigns,
            presence,
        };
        if let Some(handler) = &self.leave_handler {
            let mut ctx = LeaveContext {
                channel: self.clone(),
                user,
                reason: reason.to_owned(),
            };
            let _ = handler.call(&mut ctx).await;
        }
        if let Some(hooks) = &self.options.hooks
            && let Some(metrics) = &hooks.metrics
        {
            metrics.channel_left(user_id, &self.name);
        }
        if let Some(lobby) = self.lobby.upgrade() {
            lobby.remove_channel_if_idle(&self.name).await;
        }
        Ok(())
    }

    pub async fn get_user(&self, user_id: &str) -> Result<User> {
        if !self.connections.read().await.contains_key(user_id) {
            return Err(not_found(&self.name, "user not in channel"));
        }
        Ok(User {
            id: user_id.to_owned(),
            assigns: self
                .assigns
                .read()
                .await
                .get(user_id)
                .cloned()
                .unwrap_or_default(),
            presence: self.presence.read().await.get(user_id).cloned(),
        })
    }

    pub async fn get_assigns(&self) -> HashMap<String, pondsocket_common::PondAssigns> {
        self.assigns.read().await.clone()
    }

    pub async fn get_presence(&self) -> HashMap<String, pondsocket_common::PondPresence> {
        self.presence.read().await.clone()
    }

    pub async fn update_assign(&self, user_id: &str, key: &str, value: Value) -> Result<()> {
        if !self.connections.read().await.contains_key(user_id) {
            let mut remote_assigns = self
                .assigns
                .read()
                .await
                .get(user_id)
                .cloned()
                .unwrap_or_default();
            remote_assigns.insert(key.to_owned(), value.clone());
            self.publish_assign_update(user_id, key, value, remote_assigns)
                .await?;
            return Err(not_found(&self.name, "user not in channel"));
        }
        let mut assigns = self.assigns.write().await;
        let current = assigns.entry(user_id.to_owned()).or_default();
        current.insert(key.to_owned(), value.clone());
        let updated_assigns = current.clone();
        drop(assigns);
        self.publish_assign_update(user_id, key, value, updated_assigns)
            .await?;
        Ok(())
    }

    pub async fn track_presence(&self, user_id: &str, presence: Value) -> Result<()> {
        let Some(presence) = presence.as_object().cloned() else {
            return Err(internal(&self.name, "presence must be an object"));
        };
        let mut store = self.presence.write().await;
        if store.contains_key(user_id) {
            return Err(conflict(&self.name, "already tracking presence"));
        }
        store.insert(user_id.to_owned(), presence.clone());
        drop(store);
        self.publish_presence_change(user_id, PresenceEventType::Join, presence)
            .await
    }

    pub async fn update_presence(&self, user_id: &str, presence: Value) -> Result<()> {
        let Some(presence) = presence.as_object().cloned() else {
            return Err(internal(&self.name, "presence must be an object"));
        };
        let mut store = self.presence.write().await;
        if !store.contains_key(user_id) {
            return Err(not_found(&self.name, "not tracking presence"));
        }
        store.insert(user_id.to_owned(), presence.clone());
        drop(store);
        self.publish_presence_change(user_id, PresenceEventType::Update, presence)
            .await
    }

    pub async fn untrack_presence(&self, user_id: &str) -> Result<()> {
        let old = self.presence.write().await.remove(user_id);
        if let Some(old) = old {
            self.publish_presence_change(user_id, PresenceEventType::Leave, old)
                .await?;
        }
        Ok(())
    }

    pub async fn broadcast(&self, event: &str, payload: Value) -> Result<()> {
        let recipients = self
            .connections
            .read()
            .await
            .keys()
            .cloned()
            .collect::<Vec<_>>();
        self.send_event(
            ServerAction::Broadcast,
            event,
            payload,
            recipients,
            None,
            None,
            None,
            None,
        )
        .await
    }

    pub async fn broadcast_to(
        &self,
        event: &str,
        payload: Value,
        user_ids: &[String],
    ) -> Result<()> {
        self.send_event(
            ServerAction::Broadcast,
            event,
            payload,
            user_ids.to_vec(),
            None,
            Some(user_ids.to_vec()),
            None,
            None,
        )
        .await
    }

    pub async fn broadcast_from(&self, event: &str, payload: Value, sender_id: &str) -> Result<()> {
        let recipients = self
            .connections
            .read()
            .await
            .keys()
            .filter(|id| id.as_str() != sender_id)
            .cloned()
            .collect::<Vec<_>>();
        self.send_event(
            ServerAction::Broadcast,
            event,
            payload,
            recipients,
            None,
            None,
            Some("ALL_EXCEPT_SENDER".to_owned()),
            Some(sender_id.to_owned()),
        )
        .await
    }

    pub async fn send_system(
        &self,
        event: &str,
        payload: Value,
        request_id: &str,
        user_ids: &[String],
    ) -> Result<()> {
        let ev = Event::new("SYSTEM", &self.name, request_id, event, payload);
        self.send_direct(user_ids, ev).await
    }

    pub async fn send_acknowledge(&self, request_id: &str, user_id: &str) -> Result<()> {
        self.send_system("ACKNOWLEDGE", json!({}), request_id, &[user_id.to_owned()])
            .await
    }

    pub async fn send_unauthorized(
        &self,
        request_id: &str,
        user_id: &str,
        code: u16,
        message: &str,
    ) -> Result<()> {
        self.send_system(
            "UNAUTHORIZED",
            json!({ "code": code, "message": message }),
            request_id,
            &[user_id.to_owned()],
        )
        .await
    }

    pub async fn evict_user(self: &Arc<Self>, user_id: &str, reason: &str) -> Result<()> {
        if !self.has_user(user_id).await {
            return self
                .publish_user_command("user:evict", user_id, reason)
                .await;
        }
        let ev = Event::new(
            "SYSTEM",
            &self.name,
            uuid(),
            "EVICTED",
            json!({ "reason": reason }),
        );
        self.send_direct(&[user_id.to_owned()], ev).await?;
        self.remove_user(user_id, "evicted").await
    }

    pub async fn handle_incoming_event(
        self: &Arc<Self>,
        event: Event,
        sender_id: &str,
    ) -> Result<()> {
        let user = self.get_user(sender_id).await?;
        for reg in &self.event_handlers {
            let Ok(route) = parse(&reg.pattern, &event.event) else {
                continue;
            };
            let mut ctx = EventContext::new(self.clone(), event.clone(), user.clone(), route);
            return reg.handler.call(&mut ctx).await;
        }
        self.send_system(
            "NOT_FOUND",
            json!({ "channel": self.name, "event": event.event }),
            &event.request_id,
            &[sender_id.to_owned()],
        )
        .await
    }

    #[allow(clippy::too_many_arguments)]
    async fn send_event(
        &self,
        action: ServerAction,
        event: &str,
        payload: Value,
        recipients: Vec<String>,
        request_id: Option<String>,
        pubsub_recipients: Option<Vec<String>>,
        recipient_descriptor: Option<String>,
        sender: Option<String>,
    ) -> Result<()> {
        if recipients.is_empty() {
            return Ok(());
        }
        let mut ev = Event::new(
            action_to_str(action),
            &self.name,
            request_id.unwrap_or_else(uuid),
            event,
            payload,
        );
        if let Some(pubsub_recipients) = pubsub_recipients {
            ev.recipients = pubsub_recipients;
        }
        if let Some(recipient_descriptor) = recipient_descriptor {
            ev.recipient_descriptor = recipient_descriptor;
        }
        if let Some(sender) = sender {
            ev.from_user_id = sender;
        }
        ev.node_id = self.node_id.clone();
        self.enqueue_dispatch(DispatchItem {
            event: ev.clone(),
            recipients,
        })
        .await?;
        if action == ServerAction::Broadcast {
            self.publish_event_to_pubsub(ev, "message").await?;
        }
        Ok(())
    }

    async fn send_direct(&self, user_ids: &[String], event: Event) -> Result<()> {
        let transports = {
            let connections = self.connections.read().await;
            user_ids
                .iter()
                .filter_map(|user_id| connections.get(user_id).cloned())
                .collect::<Vec<_>>()
        };
        for transport in transports {
            if transport.is_active().await {
                let _ = transport.send_event(event.clone()).await;
            }
        }
        Ok(())
    }

    async fn enqueue_dispatch(&self, item: DispatchItem) -> Result<()> {
        timeout(
            self.options.internal_queue_timeout,
            self.dispatch_tx.send(item),
        )
        .await
        .map_err(|_| internal(&self.name, "channel dispatch queue timed out"))?
        .map_err(|_| internal(&self.name, "channel dispatch queue closed"))
    }

    async fn enqueue_dispatch_best_effort(&self, item: DispatchItem) {
        let _ = self.enqueue_dispatch(item).await;
    }

    async fn local_presence_snapshot(&self) -> (Vec<pondsocket_common::PondPresence>, Vec<String>) {
        let presence = self.presence.read().await;
        (
            presence.values().cloned().collect::<Vec<_>>(),
            presence.keys().cloned().collect::<Vec<_>>(),
        )
    }

    async fn handle_remote_presence(&self, event: &mut Event) -> Result<Vec<String>> {
        let changed = event
            .payload
            .get("changed")
            .and_then(Value::as_object)
            .cloned()
            .unwrap_or_default();
        let user_id = changed
            .get("userId")
            .or_else(|| changed.get("id"))
            .and_then(Value::as_str)
            .map(ToOwned::to_owned);
        if let Some(user_id) = user_id {
            let mut presence = self.presence.write().await;
            if event.event == "LEAVE" {
                presence.remove(&user_id);
            } else {
                presence.insert(user_id, changed);
            }
        }
        let (snapshot, recipients) = self.local_presence_snapshot().await;
        event.payload["presence"] = json!(snapshot);
        Ok(recipients)
    }

    async fn dispatch_local_or_timeout(&self, event: Event, recipients: Vec<String>) -> Result<()> {
        if recipients.is_empty() {
            return Ok(());
        }
        self.enqueue_dispatch(DispatchItem { event, recipients })
            .await
    }

    async fn dispatch_local_best_effort(&self, event: Event, recipients: Vec<String>) {
        if recipients.is_empty() {
            return;
        }
        self.enqueue_dispatch_best_effort(DispatchItem { event, recipients })
            .await;
    }

    async fn dispatch_pubsub_event(self: &Arc<Self>, mut event: Event) -> Result<()> {
        let recipients = if event.action == "PRESENCE" {
            self.handle_remote_presence(&mut event).await?
        } else if event.action == "ASSIGNS" {
            self.handle_remote_assigns(event).await?;
            return Ok(());
        } else if event.action == "USER_COMMAND" {
            self.handle_remote_user_command(event).await?;
            return Ok(());
        } else if event.action == "STATE_REQUEST" {
            self.handle_state_request().await?;
            return Ok(());
        } else if event.action == "STATE_RESPONSE" {
            self.handle_state_response(event).await;
            return Ok(());
        } else if event.action == "USER_JOINED" {
            self.handle_remote_user_joined(event).await;
            return Ok(());
        } else if event.action == "USER_LEFT" {
            self.handle_remote_user_left(event).await;
            return Ok(());
        } else if !event.recipients.is_empty() {
            let local = self.connections.read().await;
            event
                .recipients
                .iter()
                .filter(|id| local.contains_key(id.as_str()))
                .cloned()
                .collect::<Vec<_>>()
        } else if event.recipient_descriptor == "ALL_EXCEPT_SENDER" {
            self.connections
                .read()
                .await
                .keys()
                .filter(|id| *id != &event.from_user_id)
                .cloned()
                .collect::<Vec<_>>()
        } else {
            self.connections
                .read()
                .await
                .keys()
                .cloned()
                .collect::<Vec<_>>()
        };
        self.dispatch_local_or_timeout(event, recipients).await
    }

    async fn publish_presence_change(
        &self,
        user_id: &str,
        event_type: PresenceEventType,
        mut changed: pondsocket_common::PondPresence,
    ) -> Result<()> {
        let (snapshot, recipients) = self.local_presence_snapshot().await;
        changed
            .entry("userId".to_owned())
            .or_insert_with(|| json!(user_id));
        let mut ev = Event::new(
            "PRESENCE",
            &self.name,
            uuid(),
            presence_event_to_str(event_type),
            json!({ "presence": snapshot, "changed": changed }),
        );
        ev.node_id = self.node_id.clone();
        self.dispatch_local_best_effort(ev.clone(), recipients)
            .await;
        self.publish_event_to_pubsub(
            ev,
            &format!(
                "presence:{}",
                presence_event_to_str(event_type).to_lowercase()
            ),
        )
        .await
    }

    async fn dispatch_item(self: &Arc<Self>, item: DispatchItem) {
        for recipient_id in item.recipients {
            let channel = self.clone();
            let event = item.event.clone();
            let permit = self.dispatch_semaphore.clone().acquire_owned().await;
            tokio::spawn(async move {
                let _permit = permit;
                let _ = channel.deliver_one(event, &recipient_id).await;
            });
        }
    }

    async fn deliver_one(self: &Arc<Self>, event: Event, recipient_id: &str) -> Result<()> {
        let Some(transport) = self.connections.read().await.get(recipient_id).cloned() else {
            return Ok(());
        };
        if !transport.is_active().await {
            return Ok(());
        }
        let user = self.get_user(recipient_id).await?;
        let mut ctx = OutgoingContext::new(self.clone(), event, user, transport.clone());
        for reg in &self.outgoing_handlers {
            if let Ok(route) = parse(&reg.pattern, &ctx.event.event) {
                ctx.route = route;
                reg.handler.call(&mut ctx).await?;
                break;
            }
        }
        if !ctx.is_blocked() {
            transport.send_event(ctx.event).await?;
        }
        Ok(())
    }

    fn clean_endpoint(&self) -> String {
        self.endpoint_path.trim_start_matches('/').to_owned()
    }

    fn namespace(&self) -> &str {
        &self.options.namespace
    }

    fn pubsub_pattern(&self) -> String {
        format_topic(self.namespace(), &self.clean_endpoint(), &self.name)
    }

    async fn subscribe_to_pubsub(self: &Arc<Self>) -> Result<()> {
        let Some(pubsub) = &self.pubsub else {
            return Ok(());
        };
        if self.endpoint_path.is_empty() {
            return Ok(());
        }
        let channel = self.clone();
        let id = pubsub
            .subscribe(
                &self.pubsub_pattern(),
                Arc::new(move |_topic, data| {
                    let channel = channel.clone();
                    tokio::spawn(async move {
                        let _ = channel.handle_pubsub_message(data).await;
                    });
                }),
            )
            .await?;
        *self.pubsub_sub_id.write().await = Some(id);
        Ok(())
    }

    async fn unsubscribe_from_pubsub(&self) -> Result<()> {
        if let Some(pubsub) = &self.pubsub {
            if !self.endpoint_path.is_empty()
                && let Some(id) = self.pubsub_sub_id.write().await.take()
            {
                let _ = pubsub.unsubscribe(&self.pubsub_pattern(), id).await;
            }
            if let Some(id) = self.heartbeat_sub_id.write().await.take() {
                let _ = pubsub
                    .unsubscribe(&format_heartbeat_topic(self.namespace()), id)
                    .await;
            }
        }
        Ok(())
    }

    async fn publish_event_to_pubsub(&self, event: Event, subtype: &str) -> Result<()> {
        let Some(pubsub) = &self.pubsub else {
            return Ok(());
        };
        if self.endpoint_path.is_empty() {
            return Ok(());
        }
        let topic = format_topic(self.namespace(), &self.clean_endpoint(), &self.name);
        let bytes = distributed_event_to_bytes(&event, subtype, &self.clean_endpoint())
            .map_err(|e| internal(&self.name, e.to_string()))?;
        pubsub.publish(&topic, bytes).await
    }

    async fn handle_pubsub_message(self: &Arc<Self>, data: Vec<u8>) -> Result<()> {
        let Some(event) = distributed_bytes_to_event(&data) else {
            return Ok(());
        };
        if !event.node_id.is_empty() && event.node_id == self.node_id {
            return Ok(());
        }
        if event.channel_name != self.name {
            return Ok(());
        }
        self.dispatch_pubsub_event(event).await
    }

    async fn publish_assign_update(
        &self,
        user_id: &str,
        key: &str,
        value: Value,
        assigns: PondAssigns,
    ) -> Result<()> {
        let mut ev = Event::new(
            "ASSIGNS",
            &self.name,
            uuid(),
            "assigns:update",
            json!({ "userId": user_id, "key": key, "value": value, "assigns": assigns }),
        );
        ev.node_id = self.node_id.clone();
        self.publish_event_to_pubsub(ev, "assigns:update").await
    }

    async fn publish_user_command(&self, command: &str, user_id: &str, reason: &str) -> Result<()> {
        let mut ev = Event::new(
            "USER_COMMAND",
            &self.name,
            uuid(),
            command,
            json!({ "userId": user_id, "reason": reason }),
        );
        ev.node_id = self.node_id.clone();
        self.publish_event_to_pubsub(ev, command).await
    }

    async fn handle_remote_assigns(&self, event: Event) -> Result<()> {
        if event.event != "assigns:update" {
            return Ok(());
        }
        let Some(user_id) = event.payload.get("userId").and_then(Value::as_str) else {
            return Ok(());
        };
        if let Some(assigns) = self.assigns.write().await.get_mut(user_id) {
            if let Some(key) = event.payload.get("key").and_then(Value::as_str) {
                let value = event.payload.get("value").cloned().unwrap_or(Value::Null);
                assigns.insert(key.to_owned(), value);
            } else if let Some(remote_assigns) =
                event.payload.get("assigns").and_then(Value::as_object)
            {
                assigns.extend(remote_assigns.clone());
            }
        }
        Ok(())
    }

    async fn handle_remote_user_command(self: &Arc<Self>, event: Event) -> Result<()> {
        let Some(user_id) = event.payload.get("userId").and_then(Value::as_str) else {
            return Ok(());
        };
        let reason = event
            .payload
            .get("reason")
            .and_then(Value::as_str)
            .unwrap_or("remote command");
        match event.event.as_str() {
            "user:evict" => {
                if self.has_user(user_id).await {
                    let ev = Event::new(
                        "SYSTEM",
                        &self.name,
                        uuid(),
                        "EVICTED",
                        json!({ "reason": reason }),
                    );
                    self.send_direct(&[user_id.to_owned()], ev).await?;
                    self.remove_user(user_id, "evicted").await?;
                }
            }
            "user:remove" => {
                if self.has_user(user_id).await {
                    self.remove_user(user_id, reason).await?;
                }
            }
            "user:get_request" => {
                if self.has_user(user_id).await {
                    let assigns = self
                        .assigns
                        .read()
                        .await
                        .get(user_id)
                        .cloned()
                        .unwrap_or_default();
                    let presence = self
                        .presence
                        .read()
                        .await
                        .get(user_id)
                        .cloned()
                        .unwrap_or_default();
                    let request_id = event
                        .payload
                        .get("requestId")
                        .and_then(Value::as_str)
                        .unwrap_or(&event.request_id)
                        .to_owned();
                    self.publish_user_get_response(user_id, &request_id, assigns, presence)
                        .await?;
                }
            }
            "user:get_response" => {
                if !self.has_user(user_id).await {
                    if let Some(assigns) = event.payload.get("assigns").and_then(Value::as_object) {
                        self.assigns
                            .write()
                            .await
                            .insert(user_id.to_owned(), assigns.clone());
                    }
                    if let Some(presence) = event.payload.get("presence").and_then(Value::as_object)
                        && !presence.is_empty()
                    {
                        self.presence
                            .write()
                            .await
                            .insert(user_id.to_owned(), presence.clone());
                    }
                    self.track_node_user(&event.node_id, user_id).await;
                }
            }
            _ => {}
        }
        Ok(())
    }

    async fn start_liveness(self: &Arc<Self>) -> Result<()> {
        let Some(pubsub) = self.pubsub.clone() else {
            return Ok(());
        };
        let heartbeat_topic = format_heartbeat_topic(self.namespace());

        let handler_channel = self.clone();
        let heartbeat_id = pubsub
            .subscribe(
                &heartbeat_topic,
                Arc::new(move |_topic, data| {
                    let channel = handler_channel.clone();
                    tokio::spawn(async move {
                        channel.handle_heartbeat(data).await;
                    });
                }),
            )
            .await?;
        *self.heartbeat_sub_id.write().await = Some(heartbeat_id);

        let interval = self.options.heartbeat_interval;
        let hb_channel = self.clone();
        let hb_pubsub = pubsub.clone();
        let hb_topic = heartbeat_topic.clone();
        let node_id = self.node_id.clone();
        let heartbeat_task = tokio::spawn(async move {
            loop {
                if !*hb_channel.active.read().await {
                    break;
                }
                let bytes = heartbeat_to_bytes(&node_id);
                let _ = hb_pubsub.publish(&hb_topic, bytes).await;
                sleep(interval).await;
            }
        });
        *self.heartbeat_task.write().await = Some(heartbeat_task);

        let timeout_duration = self.options.heartbeat_timeout;
        let stale_channel = self.clone();
        let stale_task = tokio::spawn(async move {
            loop {
                sleep(timeout_duration).await;
                if !*stale_channel.active.read().await {
                    break;
                }
                stale_channel.cleanup_stale_nodes().await;
            }
        });
        *self.stale_task.write().await = Some(stale_task);

        self.request_channel_state().await;
        Ok(())
    }

    async fn handle_heartbeat(&self, data: Vec<u8>) {
        if let Some(node) = parse_heartbeat_node(&data)
            && node != self.node_id
        {
            self.node_last_seen
                .write()
                .await
                .insert(node, Instant::now());
        }
    }

    async fn cleanup_stale_nodes(&self) {
        let timeout_duration = self.options.heartbeat_timeout;
        let now = Instant::now();
        let stale = self
            .node_last_seen
            .read()
            .await
            .iter()
            .filter(|(_, seen)| now.duration_since(**seen) >= timeout_duration)
            .map(|(node, _)| node.clone())
            .collect::<Vec<_>>();
        if stale.is_empty() {
            return;
        }
        for node in stale {
            let users = self.node_users.write().await.remove(&node);
            if let Some(users) = users {
                let mut assigns = self.assigns.write().await;
                let mut presence = self.presence.write().await;
                for user in users {
                    assigns.remove(&user);
                    presence.remove(&user);
                }
            }
            self.node_last_seen.write().await.remove(&node);
        }
        if let Some(lobby) = self.lobby.upgrade() {
            lobby.remove_channel_if_idle(&self.name).await;
        }
    }

    async fn track_node_user(&self, node_id: &str, user_id: &str) {
        if node_id.is_empty() {
            return;
        }
        self.node_users
            .write()
            .await
            .entry(node_id.to_owned())
            .or_default()
            .insert(user_id.to_owned());
        self.node_last_seen
            .write()
            .await
            .entry(node_id.to_owned())
            .or_insert_with(Instant::now);
    }

    async fn untrack_node_user(&self, node_id: &str, user_id: &str) {
        if node_id.is_empty() {
            return;
        }
        let mut nodes = self.node_users.write().await;
        if let Some(users) = nodes.get_mut(node_id) {
            users.remove(user_id);
            if users.is_empty() {
                nodes.remove(node_id);
            }
        }
    }

    async fn request_channel_state(&self) {
        let mut ev = Event::new(
            "STATE_REQUEST",
            &self.name,
            uuid(),
            "state:request",
            json!({ "fromNode": self.node_id }),
        );
        ev.node_id = self.node_id.clone();
        let _ = self.publish_event_to_pubsub(ev, "state:request").await;
    }

    async fn handle_state_request(&self) -> Result<()> {
        let ids = self
            .connections
            .read()
            .await
            .keys()
            .cloned()
            .collect::<Vec<_>>();
        if ids.is_empty() {
            return Ok(());
        }
        let assigns = self.assigns.read().await;
        let presence = self.presence.read().await;
        let users = ids
            .iter()
            .map(|id| {
                json!({
                    "id": id,
                    "assigns": assigns.get(id).cloned().unwrap_or_default(),
                    "presence": presence.get(id).cloned().unwrap_or_default(),
                })
            })
            .collect::<Vec<_>>();
        drop(assigns);
        drop(presence);
        let mut ev = Event::new(
            "STATE_RESPONSE",
            &self.name,
            uuid(),
            "state:response",
            json!({ "users": users }),
        );
        ev.node_id = self.node_id.clone();
        self.publish_event_to_pubsub(ev, "state:response").await
    }

    async fn handle_state_response(&self, event: Event) {
        let Some(users) = event.payload.get("users").and_then(Value::as_array) else {
            return;
        };
        for user in users {
            let Some(id) = user.get("id").and_then(Value::as_str) else {
                continue;
            };
            if self.connections.read().await.contains_key(id) {
                continue;
            }
            let assigns = user
                .get("assigns")
                .and_then(Value::as_object)
                .cloned()
                .unwrap_or_default();
            self.assigns.write().await.insert(id.to_owned(), assigns);
            if let Some(presence) = user.get("presence").and_then(Value::as_object)
                && !presence.is_empty()
            {
                self.presence
                    .write()
                    .await
                    .insert(id.to_owned(), presence.clone());
            }
            self.track_node_user(&event.node_id, id).await;
        }
    }

    async fn handle_remote_user_joined(&self, event: Event) {
        let Some(id) = event.payload.get("userId").and_then(Value::as_str) else {
            return;
        };
        if self.connections.read().await.contains_key(id) {
            return;
        }
        let assigns = event
            .payload
            .get("assigns")
            .and_then(Value::as_object)
            .cloned()
            .unwrap_or_default();
        self.assigns.write().await.insert(id.to_owned(), assigns);
        if let Some(presence) = event.payload.get("presence").and_then(Value::as_object)
            && !presence.is_empty()
        {
            self.presence
                .write()
                .await
                .insert(id.to_owned(), presence.clone());
        }
        self.track_node_user(&event.node_id, id).await;
    }

    async fn handle_remote_user_left(&self, event: Event) {
        let Some(id) = event.payload.get("userId").and_then(Value::as_str) else {
            return;
        };
        if self.connections.read().await.contains_key(id) {
            return;
        }
        self.assigns.write().await.remove(id);
        self.presence.write().await.remove(id);
        self.untrack_node_user(&event.node_id, id).await;
        if let Some(lobby) = self.lobby.upgrade() {
            lobby.remove_channel_if_idle(&self.name).await;
        }
    }

    async fn publish_user_joined(&self, user_id: &str, assigns: &PondAssigns) -> Result<()> {
        let mut ev = Event::new(
            "USER_JOINED",
            &self.name,
            uuid(),
            "user:joined",
            json!({ "userId": user_id, "presence": {}, "assigns": assigns }),
        );
        ev.node_id = self.node_id.clone();
        self.publish_event_to_pubsub(ev, "user:joined").await
    }

    async fn publish_user_left(&self, user_id: &str) -> Result<()> {
        let mut ev = Event::new(
            "USER_LEFT",
            &self.name,
            uuid(),
            "user:left",
            json!({ "userId": user_id }),
        );
        ev.node_id = self.node_id.clone();
        self.publish_event_to_pubsub(ev, "user:left").await
    }

    pub async fn request_user(&self, user_id: &str) -> Result<()> {
        let request_id = uuid();
        let mut ev = Event::new(
            "USER_COMMAND",
            &self.name,
            request_id.clone(),
            "user:get_request",
            json!({ "userId": user_id, "requestId": request_id, "fromNode": self.node_id }),
        );
        ev.node_id = self.node_id.clone();
        self.publish_event_to_pubsub(ev, "user:get_request").await
    }

    async fn publish_user_get_response(
        &self,
        user_id: &str,
        request_id: &str,
        assigns: PondAssigns,
        presence: pondsocket_common::PondPresence,
    ) -> Result<()> {
        let mut ev = Event::new(
            "USER_COMMAND",
            &self.name,
            request_id.to_owned(),
            "user:get_response",
            json!({
                "userId": user_id,
                "requestId": request_id,
                "assigns": assigns,
                "presence": presence,
            }),
        );
        ev.node_id = self.node_id.clone();
        self.publish_event_to_pubsub(ev, "user:get_response").await
    }
}

fn action_to_str(action: ServerAction) -> &'static str {
    match action {
        ServerAction::Presence => "PRESENCE",
        ServerAction::System => "SYSTEM",
        ServerAction::Broadcast => "BROADCAST",
        ServerAction::Error => "ERROR",
        ServerAction::Connect => "CONNECT",
    }
}

fn presence_event_to_str(event: PresenceEventType) -> &'static str {
    match event {
        PresenceEventType::Join => "JOIN",
        PresenceEventType::Leave => "LEAVE",
        PresenceEventType::Update => "UPDATE",
    }
}

fn distributed_event_to_bytes(
    event: &Event,
    subtype: &str,
    endpoint: &str,
) -> serde_json::Result<Vec<u8>> {
    let message_type = match event.action.as_str() {
        "BROADCAST" => "USER_MESSAGE",
        "PRESENCE" if subtype.ends_with("remove") || event.event == "LEAVE" => "PRESENCE_REMOVED",
        "PRESENCE" => "PRESENCE_UPDATE",
        "ASSIGNS" => "ASSIGNS_UPDATE",
        "USER_COMMAND" if event.event == "user:remove" => "USER_REMOVE",
        "USER_COMMAND" if event.event == "user:get_request" => "USER_GET_REQUEST",
        "USER_COMMAND" if event.event == "user:get_response" => "USER_GET_RESPONSE",
        "USER_COMMAND" => "EVICT_USER",
        "STATE_REQUEST" => "STATE_REQUEST",
        "STATE_RESPONSE" => "STATE_RESPONSE",
        "USER_JOINED" => "USER_JOINED",
        "USER_LEFT" => "USER_LEFT",
        "HEARTBEAT" => "NODE_HEARTBEAT",
        _ => "USER_MESSAGE",
    };
    let mut out = serde_json::Map::new();
    out.insert("protocol".to_owned(), json!("pondsocket.distributed"));
    out.insert("version".to_owned(), json!(1));
    out.insert("type".to_owned(), json!(message_type));
    out.insert("messageId".to_owned(), json!(uuid()));
    out.insert("sourceNodeId".to_owned(), json!(event.node_id));
    out.insert("endpointName".to_owned(), json!(endpoint));
    out.insert("channelName".to_owned(), json!(event.channel_name));
    out.insert("timestamp".to_owned(), json!(now_ms()));
    match message_type {
        "USER_MESSAGE" => {
            let from = if event.from_user_id.is_empty() {
                "CHANNEL"
            } else {
                event.from_user_id.as_str()
            };
            out.insert("fromUserId".to_owned(), json!(from));
            out.insert("event".to_owned(), json!(event.event));
            out.insert("payload".to_owned(), event.payload.clone());
            out.insert("requestId".to_owned(), json!(event.request_id));
            let descriptor = if event.recipient_descriptor == "ALL_EXCEPT_SENDER" {
                json!("ALL_EXCEPT_SENDER")
            } else if !event.recipients.is_empty() {
                json!(event.recipients)
            } else {
                json!("ALL_USERS")
            };
            out.insert("recipientDescriptor".to_owned(), descriptor);
        }
        "PRESENCE_UPDATE" => {
            let changed = event.payload.get("changed").cloned().unwrap_or(json!({}));
            let user_id = changed
                .get("userId")
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_owned();
            out.insert("userId".to_owned(), json!(user_id));
            out.insert("presence".to_owned(), changed);
        }
        "PRESENCE_REMOVED" => {
            out.insert(
                "userId".to_owned(),
                json!(
                    event
                        .payload
                        .get("changed")
                        .and_then(|v| v.get("userId"))
                        .and_then(Value::as_str)
                        .unwrap_or("")
                ),
            );
        }
        "ASSIGNS_UPDATE" => {
            out.insert(
                "userId".to_owned(),
                event.payload.get("userId").cloned().unwrap_or(json!("")),
            );
            out.insert(
                "assigns".to_owned(),
                event.payload.get("assigns").cloned().unwrap_or(json!({})),
            );
        }
        "EVICT_USER" => {
            out.insert(
                "userId".to_owned(),
                event.payload.get("userId").cloned().unwrap_or(json!("")),
            );
            out.insert(
                "reason".to_owned(),
                event.payload.get("reason").cloned().unwrap_or(json!("")),
            );
        }
        "USER_REMOVE" => {
            out.insert(
                "userId".to_owned(),
                event.payload.get("userId").cloned().unwrap_or(json!("")),
            );
        }
        "USER_GET_REQUEST" => {
            out.insert(
                "userId".to_owned(),
                event.payload.get("userId").cloned().unwrap_or(json!("")),
            );
            out.insert("requestId".to_owned(), json!(event.request_id));
            out.insert(
                "fromNode".to_owned(),
                event
                    .payload
                    .get("fromNode")
                    .cloned()
                    .unwrap_or(json!(event.node_id)),
            );
        }
        "USER_GET_RESPONSE" => {
            out.insert(
                "userId".to_owned(),
                event.payload.get("userId").cloned().unwrap_or(json!("")),
            );
            out.insert("requestId".to_owned(), json!(event.request_id));
            out.insert(
                "assigns".to_owned(),
                event.payload.get("assigns").cloned().unwrap_or(json!({})),
            );
            out.insert(
                "presence".to_owned(),
                event.payload.get("presence").cloned().unwrap_or(json!({})),
            );
        }
        "USER_JOINED" => {
            out.insert(
                "userId".to_owned(),
                event.payload.get("userId").cloned().unwrap_or(json!("")),
            );
            out.insert(
                "presence".to_owned(),
                event.payload.get("presence").cloned().unwrap_or(json!({})),
            );
            out.insert(
                "assigns".to_owned(),
                event.payload.get("assigns").cloned().unwrap_or(json!({})),
            );
        }
        "USER_LEFT" => {
            out.insert(
                "userId".to_owned(),
                event.payload.get("userId").cloned().unwrap_or(json!("")),
            );
        }
        "STATE_REQUEST" => {
            out.insert(
                "fromNode".to_owned(),
                event
                    .payload
                    .get("fromNode")
                    .cloned()
                    .unwrap_or(json!(event.node_id)),
            );
        }
        "STATE_RESPONSE" => {
            out.insert(
                "users".to_owned(),
                event.payload.get("users").cloned().unwrap_or(json!([])),
            );
        }
        "NODE_HEARTBEAT" => {
            out.insert("nodeId".to_owned(), json!(event.node_id));
        }
        _ => {
            out.insert("payload".to_owned(), event.payload.clone());
        }
    }
    serde_json::to_vec(&Value::Object(out))
}

fn distributed_bytes_to_event(data: &[u8]) -> Option<Event> {
    let value: Value = serde_json::from_slice(data).ok()?;
    if value.get("protocol")?.as_str()? != "pondsocket.distributed" {
        return None;
    }
    if value.get("version")?.as_i64()? != 1 {
        return None;
    }
    let typ = value.get("type")?.as_str()?;
    let channel = value.get("channelName")?.as_str()?.to_owned();
    let request_id = value
        .get("requestId")
        .and_then(Value::as_str)
        .map(ToOwned::to_owned)
        .unwrap_or_else(uuid);
    let node_id = value.get("sourceNodeId")?.as_str()?.to_owned();
    match typ {
        "USER_MESSAGE" => Some(Event {
            action: "BROADCAST".to_owned(),
            channel_name: channel,
            request_id,
            event: value.get("event")?.as_str()?.to_owned(),
            payload: value.get("payload").cloned().unwrap_or(json!({})),
            node_id,
            recipients: value
                .get("recipientDescriptor")
                .and_then(Value::as_array)
                .map(|items| {
                    items
                        .iter()
                        .filter_map(Value::as_str)
                        .map(ToOwned::to_owned)
                        .collect()
                })
                .unwrap_or_default(),
            from_user_id: value
                .get("fromUserId")
                .and_then(Value::as_str)
                .unwrap_or("CHANNEL")
                .to_owned(),
            recipient_descriptor: value
                .get("recipientDescriptor")
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_owned(),
        }),
        "PRESENCE_UPDATE" => {
            let user_id = value.get("userId").cloned().unwrap_or(json!(""));
            let mut changed = value.get("presence").cloned().unwrap_or(json!({}));
            if let Some(obj) = changed.as_object_mut() {
                obj.entry("userId".to_owned()).or_insert(user_id.clone());
            }
            Some(Event {
                action: "PRESENCE".to_owned(),
                channel_name: channel,
                request_id,
                event: "UPDATE".to_owned(),
                payload: json!({"presence": [], "changed": changed}),
                node_id,
                recipients: Vec::new(),
                from_user_id: String::new(),
                recipient_descriptor: String::new(),
            })
        }
        "PRESENCE_REMOVED" => Some(Event {
            action: "PRESENCE".to_owned(),
            channel_name: channel,
            request_id,
            event: "LEAVE".to_owned(),
            payload: json!({"presence": [], "changed": {"userId": value.get("userId").cloned().unwrap_or(json!(""))}}),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "ASSIGNS_UPDATE" => Some(Event {
            action: "ASSIGNS".to_owned(),
            channel_name: channel,
            request_id,
            event: "assigns:update".to_owned(),
            payload: json!({
                "userId": value.get("userId").cloned().unwrap_or(json!("")),
                "key": value.get("key").cloned().unwrap_or(Value::Null),
                "value": value.get("value").cloned().unwrap_or(Value::Null),
                "assigns": value.get("assigns").cloned().unwrap_or(json!({})),
            }),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "EVICT_USER" => Some(Event {
            action: "USER_COMMAND".to_owned(),
            channel_name: channel,
            request_id,
            event: "user:evict".to_owned(),
            payload: json!({"userId": value.get("userId").cloned().unwrap_or(json!("")), "reason": value.get("reason").cloned().unwrap_or(json!(""))}),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "USER_REMOVE" => Some(Event {
            action: "USER_COMMAND".to_owned(),
            channel_name: channel,
            request_id,
            event: "user:remove".to_owned(),
            payload: json!({"userId": value.get("userId").cloned().unwrap_or(json!(""))}),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "USER_GET_REQUEST" => Some(Event {
            action: "USER_COMMAND".to_owned(),
            channel_name: channel,
            request_id: request_id.clone(),
            event: "user:get_request".to_owned(),
            payload: json!({
                "userId": value.get("userId").cloned().unwrap_or(json!("")),
                "requestId": request_id,
                "fromNode": value.get("fromNode").cloned().unwrap_or(json!("")),
            }),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "USER_GET_RESPONSE" => Some(Event {
            action: "USER_COMMAND".to_owned(),
            channel_name: channel,
            request_id: request_id.clone(),
            event: "user:get_response".to_owned(),
            payload: json!({
                "userId": value.get("userId").cloned().unwrap_or(json!("")),
                "requestId": request_id,
                "assigns": value.get("assigns").cloned().unwrap_or(json!({})),
                "presence": value.get("presence").cloned().unwrap_or(json!({})),
            }),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "USER_JOINED" => Some(Event {
            action: "USER_JOINED".to_owned(),
            channel_name: channel,
            request_id,
            event: "user:joined".to_owned(),
            payload: json!({
                "userId": value.get("userId").cloned().unwrap_or(json!("")),
                "presence": value.get("presence").cloned().unwrap_or(json!({})),
                "assigns": value.get("assigns").cloned().unwrap_or(json!({})),
            }),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "USER_LEFT" => Some(Event {
            action: "USER_LEFT".to_owned(),
            channel_name: channel,
            request_id,
            event: "user:left".to_owned(),
            payload: json!({ "userId": value.get("userId").cloned().unwrap_or(json!("")) }),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "STATE_REQUEST" => Some(Event {
            action: "STATE_REQUEST".to_owned(),
            channel_name: channel,
            request_id,
            event: "state:request".to_owned(),
            payload: json!({ "fromNode": value.get("fromNode").cloned().unwrap_or(json!("")) }),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "STATE_RESPONSE" => Some(Event {
            action: "STATE_RESPONSE".to_owned(),
            channel_name: channel,
            request_id,
            event: "state:response".to_owned(),
            payload: json!({ "users": value.get("users").cloned().unwrap_or(json!([])) }),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        "NODE_HEARTBEAT" => Some(Event {
            action: "HEARTBEAT".to_owned(),
            channel_name: channel,
            request_id,
            event: "heartbeat".to_owned(),
            payload: json!({ "nodeId": value.get("nodeId").cloned().unwrap_or(json!("")) }),
            node_id,
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }),
        _ => None,
    }
}

fn heartbeat_to_bytes(node_id: &str) -> Vec<u8> {
    let mut ev = Event::new(
        "HEARTBEAT",
        "__heartbeat__",
        uuid(),
        "heartbeat",
        json!({ "nodeId": node_id }),
    );
    ev.node_id = node_id.to_owned();
    distributed_event_to_bytes(&ev, "heartbeat", "__heartbeat__").unwrap_or_default()
}

fn parse_heartbeat_node(data: &[u8]) -> Option<String> {
    let event = distributed_bytes_to_event(data)?;
    if event.action != "HEARTBEAT" {
        return None;
    }
    Some(event.node_id)
}

fn now_ms() -> u128 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis())
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    #[tokio::test]
    async fn internal_queue_timeout_is_enforced() {
        let options = Options {
            internal_queue_buffer: 1,
            internal_queue_timeout: Duration::from_millis(1),
            ..Options::default()
        };
        let channel = Channel::new(ChannelConfig {
            name: "/chat/1".to_owned(),
            endpoint_path: "/".to_owned(),
            options,
            pubsub: None,
            event_handlers: Vec::new(),
            outgoing_handlers: Vec::new(),
            leave_handler: None,
            lobby: std::sync::Weak::new(),
        });
        channel
            .send_event(
                ServerAction::System,
                "ONE",
                json!({}),
                vec!["u1".to_owned()],
                Some("r1".to_owned()),
                None,
                None,
                None,
            )
            .await
            .unwrap();

        let err = channel
            .send_event(
                ServerAction::System,
                "TWO",
                json!({}),
                vec!["u1".to_owned()],
                Some("r2".to_owned()),
                None,
                None,
                None,
            )
            .await
            .unwrap_err();

        assert_eq!(err.code, 500);
        assert!(err.message.contains("timed out"));
    }

    #[tokio::test]
    async fn adv_empty_channel_is_closed_after_last_user_leaves() {
        let lobby = crate::lobby::Lobby::new("/chat/:room", "/", Options::default(), None);
        let channel = lobby.get_or_create_channel("/chat/leak").await.unwrap();
        let transport = Arc::new(crate::transport::MemoryTransport::new(
            "u1",
            PondAssigns::new(),
        ));
        channel.add_user(transport).await.unwrap();
        assert_eq!(channel.user_count().await, 1);

        channel.remove_user("u1", "explicit_leave").await.unwrap();

        assert_eq!(channel.user_count().await, 0);
        assert!(!*channel.active.read().await);
        assert!(channel.dispatch_task.read().await.is_none());
        assert!(lobby.get_channel("/chat/leak").await.is_none());
    }

    #[tokio::test]
    async fn adv_is_idle_requires_no_remote_users() {
        let channel = Channel::new(ChannelConfig {
            name: "/chat/remote".to_owned(),
            endpoint_path: "/".to_owned(),
            options: Options::default(),
            pubsub: None,
            event_handlers: Vec::new(),
            outgoing_handlers: Vec::new(),
            leave_handler: None,
            lobby: std::sync::Weak::new(),
        });
        channel
            .node_users
            .write()
            .await
            .entry("node-b".to_owned())
            .or_default()
            .insert("remote-user".to_owned());

        assert_eq!(channel.user_count().await, 0);
        assert!(!channel.is_idle().await);

        channel.untrack_node_user("node-b", "remote-user").await;
        assert!(channel.is_idle().await);
    }

    fn encode(event: &Event, subtype: &str) -> Value {
        let bytes = distributed_event_to_bytes(event, subtype, "api").unwrap();
        serde_json::from_slice(&bytes).unwrap()
    }

    fn base(action: &str, event_name: &str, payload: Value) -> Event {
        let mut ev = Event::new(action, "/chat/1", "req-1", event_name, payload);
        ev.node_id = "node-a".to_owned();
        ev
    }

    fn assert_envelope(wire: &Value, expected_type: &str) {
        assert_eq!(wire["protocol"], "pondsocket.distributed");
        assert_eq!(wire["version"], 1);
        assert_eq!(wire["type"], expected_type);
        assert!(wire["messageId"].as_str().is_some());
        assert_eq!(wire["sourceNodeId"], "node-a");
        assert_eq!(wire["endpointName"], "api");
        assert_eq!(wire["channelName"], "/chat/1");
        assert!(wire["timestamp"].as_i64().is_some());
    }

    #[test]
    fn round_trip_user_message_all_users() {
        let ev = base("BROADCAST", "message", json!({ "text": "hi" }));
        let wire = encode(&ev, "message");
        assert_envelope(&wire, "USER_MESSAGE");
        assert_eq!(wire["fromUserId"], "CHANNEL");
        assert_eq!(wire["event"], "message");
        assert_eq!(wire["payload"]["text"], "hi");
        assert_eq!(wire["requestId"], "req-1");
        assert_eq!(wire["recipientDescriptor"], "ALL_USERS");

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "BROADCAST");
        assert_eq!(decoded.event, "message");
        assert_eq!(decoded.payload["text"], "hi");
        assert_eq!(decoded.node_id, "node-a");
        assert_eq!(decoded.recipient_descriptor, "ALL_USERS");
        assert!(decoded.recipients.is_empty());
    }

    #[test]
    fn recipient_descriptor_all_except_sender() {
        let mut ev = base("BROADCAST", "message", json!({ "text": "hi" }));
        ev.recipient_descriptor = "ALL_EXCEPT_SENDER".to_owned();
        ev.from_user_id = "u1".to_owned();
        let wire = encode(&ev, "message");
        assert_eq!(wire["recipientDescriptor"], "ALL_EXCEPT_SENDER");
        assert_eq!(wire["fromUserId"], "u1");

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.recipient_descriptor, "ALL_EXCEPT_SENDER");
        assert_eq!(decoded.from_user_id, "u1");
        assert!(decoded.recipients.is_empty());
    }

    #[test]
    fn recipient_descriptor_explicit_ids() {
        let mut ev = base("BROADCAST", "message", json!({ "text": "hi" }));
        ev.recipients = vec!["u1".to_owned(), "u2".to_owned()];
        let wire = encode(&ev, "message");
        assert_eq!(wire["recipientDescriptor"], json!(["u1", "u2"]));

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.recipients, vec!["u1".to_owned(), "u2".to_owned()]);
    }

    #[test]
    fn round_trip_presence_update() {
        let ev = base(
            "PRESENCE",
            "UPDATE",
            json!({ "presence": [], "changed": { "userId": "u1", "status": "online" } }),
        );
        let wire = encode(&ev, "presence:update");
        assert_envelope(&wire, "PRESENCE_UPDATE");
        assert_eq!(wire["userId"], "u1");
        assert_eq!(wire["presence"]["status"], "online");
        assert!(wire.get("event").is_none());

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "PRESENCE");
        assert_eq!(decoded.event, "UPDATE");
        assert_eq!(decoded.payload["changed"]["userId"], "u1");
        assert_eq!(decoded.payload["changed"]["status"], "online");
    }

    #[test]
    fn round_trip_presence_removed() {
        let ev = base(
            "PRESENCE",
            "LEAVE",
            json!({ "presence": [], "changed": { "userId": "u1" } }),
        );
        let wire = encode(&ev, "presence:leave");
        assert_envelope(&wire, "PRESENCE_REMOVED");
        assert_eq!(wire["userId"], "u1");
        assert!(wire.get("event").is_none());
        assert!(wire.get("presence").is_none());

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "PRESENCE");
        assert_eq!(decoded.event, "LEAVE");
        assert_eq!(decoded.payload["changed"]["userId"], "u1");
    }

    #[test]
    fn round_trip_assigns_update() {
        let ev = base(
            "ASSIGNS",
            "assigns:update",
            json!({ "userId": "u1", "assigns": { "role": "admin" } }),
        );
        let wire = encode(&ev, "assigns:update");
        assert_envelope(&wire, "ASSIGNS_UPDATE");
        assert_eq!(wire["userId"], "u1");
        assert_eq!(wire["assigns"]["role"], "admin");
        assert!(wire.get("key").is_none());
        assert!(wire.get("value").is_none());
        assert!(wire.get("payload").is_none());

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "ASSIGNS");
        assert_eq!(decoded.event, "assigns:update");
        assert_eq!(decoded.payload["userId"], "u1");
        assert_eq!(decoded.payload["assigns"]["role"], "admin");
    }

    #[test]
    fn round_trip_evict_user() {
        let ev = base(
            "USER_COMMAND",
            "user:evict",
            json!({ "userId": "u1", "reason": "spam" }),
        );
        let wire = encode(&ev, "user:evict");
        assert_envelope(&wire, "EVICT_USER");
        assert_eq!(wire["userId"], "u1");
        assert_eq!(wire["reason"], "spam");

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "USER_COMMAND");
        assert_eq!(decoded.event, "user:evict");
        assert_eq!(decoded.payload["userId"], "u1");
        assert_eq!(decoded.payload["reason"], "spam");
    }

    #[test]
    fn round_trip_user_remove() {
        let ev = base(
            "USER_COMMAND",
            "user:remove",
            json!({ "userId": "u1", "reason": "gone" }),
        );
        let wire = encode(&ev, "user:remove");
        assert_envelope(&wire, "USER_REMOVE");
        assert_eq!(wire["userId"], "u1");
        assert!(wire.get("reason").is_none());

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "USER_COMMAND");
        assert_eq!(decoded.event, "user:remove");
        assert_eq!(decoded.payload["userId"], "u1");
    }

    #[test]
    fn round_trip_user_get_request() {
        let mut ev = base(
            "USER_COMMAND",
            "user:get_request",
            json!({ "userId": "u1", "fromNode": "node-a" }),
        );
        ev.request_id = "lookup-1".to_owned();
        let wire = encode(&ev, "user:get_request");
        assert_envelope(&wire, "USER_GET_REQUEST");
        assert_eq!(wire["userId"], "u1");
        assert_eq!(wire["requestId"], "lookup-1");
        assert_eq!(wire["fromNode"], "node-a");

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "USER_COMMAND");
        assert_eq!(decoded.event, "user:get_request");
        assert_eq!(decoded.request_id, "lookup-1");
        assert_eq!(decoded.payload["userId"], "u1");
        assert_eq!(decoded.payload["fromNode"], "node-a");
    }

    #[test]
    fn round_trip_user_get_response() {
        let mut ev = base(
            "USER_COMMAND",
            "user:get_response",
            json!({ "userId": "u1", "assigns": { "role": "admin" }, "presence": { "status": "online" } }),
        );
        ev.request_id = "lookup-1".to_owned();
        let wire = encode(&ev, "user:get_response");
        assert_envelope(&wire, "USER_GET_RESPONSE");
        assert_eq!(wire["userId"], "u1");
        assert_eq!(wire["requestId"], "lookup-1");
        assert_eq!(wire["assigns"]["role"], "admin");
        assert_eq!(wire["presence"]["status"], "online");

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.event, "user:get_response");
        assert_eq!(decoded.request_id, "lookup-1");
        assert_eq!(decoded.payload["assigns"]["role"], "admin");
        assert_eq!(decoded.payload["presence"]["status"], "online");
    }

    #[test]
    fn round_trip_user_joined() {
        let ev = base(
            "USER_JOINED",
            "user:joined",
            json!({ "userId": "u1", "presence": {}, "assigns": { "role": "admin" } }),
        );
        let wire = encode(&ev, "user:joined");
        assert_envelope(&wire, "USER_JOINED");
        assert_eq!(wire["userId"], "u1");
        assert_eq!(wire["assigns"]["role"], "admin");
        assert_eq!(wire["presence"], json!({}));

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "USER_JOINED");
        assert_eq!(decoded.payload["userId"], "u1");
        assert_eq!(decoded.payload["assigns"]["role"], "admin");
    }

    #[test]
    fn round_trip_user_left() {
        let ev = base("USER_LEFT", "user:left", json!({ "userId": "u1" }));
        let wire = encode(&ev, "user:left");
        assert_envelope(&wire, "USER_LEFT");
        assert_eq!(wire["userId"], "u1");

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "USER_LEFT");
        assert_eq!(decoded.payload["userId"], "u1");
    }

    #[test]
    fn round_trip_state_request() {
        let ev = base(
            "STATE_REQUEST",
            "state:request",
            json!({ "fromNode": "node-a" }),
        );
        let wire = encode(&ev, "state:request");
        assert_envelope(&wire, "STATE_REQUEST");
        assert_eq!(wire["fromNode"], "node-a");

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "STATE_REQUEST");
        assert_eq!(decoded.payload["fromNode"], "node-a");
    }

    #[test]
    fn round_trip_state_response() {
        let ev = base(
            "STATE_RESPONSE",
            "state:response",
            json!({ "users": [ { "id": "u1", "assigns": { "role": "admin" }, "presence": { "status": "online" } } ] }),
        );
        let wire = encode(&ev, "state:response");
        assert_envelope(&wire, "STATE_RESPONSE");
        assert_eq!(wire["users"][0]["id"], "u1");
        assert!(wire["users"][0].get("userId").is_none());
        assert_eq!(wire["users"][0]["assigns"]["role"], "admin");
        assert_eq!(wire["users"][0]["presence"]["status"], "online");

        let bytes = serde_json::to_vec(&wire).unwrap();
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "STATE_RESPONSE");
        assert_eq!(decoded.payload["users"][0]["id"], "u1");
    }

    #[test]
    fn round_trip_node_heartbeat() {
        let bytes = heartbeat_to_bytes("node-a");
        let wire: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(wire["type"], "NODE_HEARTBEAT");
        assert_eq!(wire["protocol"], "pondsocket.distributed");
        assert_eq!(wire["version"], 1);
        assert_eq!(wire["sourceNodeId"], "node-a");
        assert_eq!(wire["nodeId"], "node-a");
        assert_eq!(wire["endpointName"], "__heartbeat__");
        assert_eq!(wire["channelName"], "__heartbeat__");

        assert_eq!(parse_heartbeat_node(&bytes).as_deref(), Some("node-a"));
        let decoded = distributed_bytes_to_event(&bytes).unwrap();
        assert_eq!(decoded.action, "HEARTBEAT");
        assert_eq!(decoded.node_id, "node-a");
    }
}
