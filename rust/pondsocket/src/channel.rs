use async_trait::async_trait;
use pondsocket_common::{PresenceEventType, ServerAction, uuid};
use serde_json::{Value, json};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::{RwLock, Semaphore, mpsc};
use tokio::task::JoinHandle;
use tokio::time::timeout;

use crate::contexts::{EventContext, LeaveContext, OutgoingContext};
use crate::errors::{Result, conflict, internal, not_found};
use crate::parser::parse;
use crate::pubsub::{PubSub, format_topic};
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
}

pub struct ChannelConfig {
    pub name: String,
    pub endpoint_path: String,
    pub options: Options,
    pub pubsub: Option<Arc<dyn PubSub>>,
    pub event_handlers: Vec<EventRegistration>,
    pub outgoing_handlers: Vec<OutgoingRegistration>,
    pub leave_handler: Option<Arc<dyn LeaveHandler>>,
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
        })
    }

    pub async fn start(self: &Arc<Self>) -> Result<()> {
        *self.active.write().await = true;
        self.subscribe_to_pubsub().await?;
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
        if let Some(hooks) = &self.options.hooks {
            if let Some(metrics) = &hooks.metrics {
                metrics.channel_created(&self.name);
            }
        }
        Ok(())
    }

    pub async fn close(&self) -> Result<()> {
        *self.active.write().await = false;
        self.unsubscribe_from_pubsub().await?;
        if let Some(task) = self.dispatch_task.write().await.take() {
            task.abort();
        }
        self.connections.write().await.clear();
        self.assigns.write().await.clear();
        self.presence.write().await.clear();
        if let Some(hooks) = &self.options.hooks {
            if let Some(metrics) = &hooks.metrics {
                metrics.channel_destroyed(&self.name);
            }
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
        self.assigns.write().await.insert(user_id.clone(), assigns);
        if let Some(hooks) = &self.options.hooks {
            if let Some(metrics) = &hooks.metrics {
                metrics.channel_joined(&user_id, &self.name);
            }
        }
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
            self.publish_presence_change(PresenceEventType::Leave, old)
                .await?;
        }
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
        if let Some(hooks) = &self.options.hooks {
            if let Some(metrics) = &hooks.metrics {
                metrics.channel_left(user_id, &self.name);
            }
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
        let mut assigns = self.assigns.write().await;
        let Some(current) = assigns.get_mut(user_id) else {
            drop(assigns);
            self.publish_assign_update(user_id, key, value).await?;
            return Err(not_found(&self.name, "user not in channel"));
        };
        current.insert(key.to_owned(), value.clone());
        drop(assigns);
        self.publish_assign_update(user_id, key, value).await?;
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
        self.publish_presence_change(PresenceEventType::Join, presence)
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
        self.publish_presence_change(PresenceEventType::Update, presence)
            .await
    }

    pub async fn untrack_presence(&self, user_id: &str) -> Result<()> {
        let old = self.presence.write().await.remove(user_id);
        if let Some(old) = old {
            self.publish_presence_change(PresenceEventType::Leave, old)
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

    async fn send_event(
        &self,
        action: ServerAction,
        event: &str,
        payload: Value,
        recipients: Vec<String>,
        request_id: Option<String>,
        pubsub_recipients: Option<Vec<String>>,
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
        } else if !event.recipients.is_empty() {
            let local = self.connections.read().await;
            event
                .recipients
                .iter()
                .filter(|id| local.contains_key(id.as_str()))
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
        event_type: PresenceEventType,
        changed: pondsocket_common::PondPresence,
    ) -> Result<()> {
        let (snapshot, recipients) = self.local_presence_snapshot().await;
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

    fn pubsub_pattern(&self) -> String {
        format_topic(&self.clean_endpoint(), &self.name, ".*")
    }

    async fn subscribe_to_pubsub(self: &Arc<Self>) -> Result<()> {
        let Some(pubsub) = &self.pubsub else {
            return Ok(());
        };
        if self.endpoint_path.is_empty() {
            return Ok(());
        }
        let channel = self.clone();
        pubsub
            .subscribe(
                &self.pubsub_pattern(),
                Arc::new(move |_topic, data| {
                    let channel = channel.clone();
                    tokio::spawn(async move {
                        let _ = channel.handle_pubsub_message(data).await;
                    });
                }),
            )
            .await
    }

    async fn unsubscribe_from_pubsub(&self) -> Result<()> {
        if let Some(pubsub) = &self.pubsub {
            if !self.endpoint_path.is_empty() {
                let _ = pubsub.unsubscribe(&self.pubsub_pattern()).await;
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
        let topic = format_topic(&self.clean_endpoint(), &self.name, subtype);
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

    async fn publish_assign_update(&self, user_id: &str, key: &str, value: Value) -> Result<()> {
        let mut ev = Event::new(
            "ASSIGNS",
            &self.name,
            uuid(),
            "assigns:update",
            json!({ "userId": user_id, "key": key, "value": value }),
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
            _ => {}
        }
        Ok(())
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
            out.insert("fromUserId".to_owned(), json!("CHANNEL"));
            out.insert("event".to_owned(), json!(event.event));
            out.insert("payload".to_owned(), event.payload.clone());
            out.insert("requestId".to_owned(), json!(event.request_id));
            if event.recipients.is_empty() {
                out.insert("recipientDescriptor".to_owned(), json!("ALL_USERS"));
            } else {
                out.insert("recipientDescriptor".to_owned(), json!(event.recipients));
            }
        }
        "PRESENCE_UPDATE" => {
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
            out.insert(
                "presence".to_owned(),
                event.payload.get("changed").cloned().unwrap_or(json!({})),
            );
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
            if let Some(key) = event.payload.get("key") {
                out.insert("key".to_owned(), key.clone());
            }
            if let Some(value) = event.payload.get("value") {
                out.insert("value".to_owned(), value.clone());
            }
        }
        "EVICT_USER" | "USER_REMOVE" => {
            out.insert(
                "userId".to_owned(),
                event.payload.get("userId").cloned().unwrap_or(json!("")),
            );
            out.insert(
                "reason".to_owned(),
                event.payload.get("reason").cloned().unwrap_or(json!("")),
            );
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
        }),
        "PRESENCE_UPDATE" => Some(Event {
            action: "PRESENCE".to_owned(),
            channel_name: channel,
            request_id,
            event: "UPDATE".to_owned(),
            payload: json!({"presence": [], "changed": value.get("presence").cloned().unwrap_or(json!({}))}),
            node_id,
            recipients: Vec::new(),
        }),
        "PRESENCE_REMOVED" => Some(Event {
            action: "PRESENCE".to_owned(),
            channel_name: channel,
            request_id,
            event: "LEAVE".to_owned(),
            payload: json!({"presence": [], "changed": {"userId": value.get("userId").cloned().unwrap_or(json!(""))}}),
            node_id,
            recipients: Vec::new(),
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
        }),
        "EVICT_USER" => Some(Event {
            action: "USER_COMMAND".to_owned(),
            channel_name: channel,
            request_id,
            event: "user:evict".to_owned(),
            payload: json!({"userId": value.get("userId").cloned().unwrap_or(json!("")), "reason": value.get("reason").cloned().unwrap_or(json!(""))}),
            node_id,
            recipients: Vec::new(),
        }),
        "USER_REMOVE" => Some(Event {
            action: "USER_COMMAND".to_owned(),
            channel_name: channel,
            request_id,
            event: "user:remove".to_owned(),
            payload: json!({"userId": value.get("userId").cloned().unwrap_or(json!("")), "reason": value.get("reason").cloned().unwrap_or(json!(""))}),
            node_id,
            recipients: Vec::new(),
        }),
        _ => None,
    }
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
        });
        channel
            .send_event(
                ServerAction::System,
                "ONE",
                json!({}),
                vec!["u1".to_owned()],
                Some("r1".to_owned()),
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
            )
            .await
            .unwrap_err();

        assert_eq!(err.code, 500);
        assert!(err.message.contains("timed out"));
    }
}
