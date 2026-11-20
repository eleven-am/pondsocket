use async_trait::async_trait;
use futures_util::FutureExt;
use pondsocket_common::uuid;
use serde_json::json;
use std::collections::HashMap;
use std::panic::AssertUnwindSafe;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Weak};
use std::time::{Duration, Instant};
use tokio::sync::{Mutex, RwLock};
use tokio::time::timeout;

use crate::channel::{Channel, DeliveryHub};
use crate::contexts::{ConnectionContext, ConnectionDecision, IncomingConnection, JoinContext};
use crate::errors::{PondError, Result, conflict, forbidden, internal, not_found};
use crate::liveness::NodeLiveness;
use crate::lobby::Lobby;
use crate::parser::parse;
use crate::pubsub::PubSub;
use crate::transport::Transport;
use crate::types::{Event, Options, Route, User};

#[async_trait]
pub trait ConnectionHandler: Send + Sync {
    async fn call(&self, ctx: &mut ConnectionContext) -> Result<()>;
}

#[async_trait]
impl<F, Fut> ConnectionHandler for F
where
    F: Fn(&mut ConnectionContext) -> Fut + Send + Sync,
    Fut: std::future::Future<Output = Result<()>> + Send,
{
    async fn call(&self, ctx: &mut ConnectionContext) -> Result<()> {
        self(ctx).await
    }
}

#[async_trait]
pub trait JoinHandler: Send + Sync {
    async fn call(&self, ctx: &mut JoinContext) -> Result<()>;
}

#[async_trait]
impl<F, Fut> JoinHandler for F
where
    F: Fn(&mut JoinContext) -> Fut + Send + Sync,
    Fut: std::future::Future<Output = Result<()>> + Send,
{
    async fn call(&self, ctx: &mut JoinContext) -> Result<()> {
        self(ctx).await
    }
}

struct ChannelRegistration {
    pattern: String,
    handler: Arc<dyn JoinHandler>,
    lobby: Arc<Lobby>,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum EndpointLifecycle {
    Open,
    Closing,
    Closed,
}

/// Reservation acquired before a transport performs expensive connection
/// authorization or a WebSocket upgrade.
pub struct ConnectionPermit {
    endpoint: Weak<Endpoint>,
    active: bool,
}

impl ConnectionPermit {
    fn commit(&mut self) {
        self.active = false;
    }
}

impl Drop for ConnectionPermit {
    fn drop(&mut self) {
        if self.active
            && let Some(endpoint) = self.endpoint.upgrade()
        {
            endpoint.connection_slots.fetch_sub(1, Ordering::SeqCst);
        }
    }
}

pub struct Endpoint {
    path: String,
    connection_handler: Arc<dyn ConnectionHandler>,
    options: Options,
    pubsub: Option<Arc<dyn PubSub>>,
    connections: RwLock<HashMap<String, Arc<dyn Transport>>>,
    memberships: RwLock<HashMap<String, Vec<Weak<Channel>>>>,
    channel_registrations: RwLock<Vec<ChannelRegistration>>,
    connect_times: RwLock<HashMap<String, Instant>>,
    delivery_hub: Arc<DeliveryHub>,
    liveness: Arc<NodeLiveness>,
    connection_slots: AtomicUsize,
    lifecycle: Mutex<EndpointLifecycle>,
    accepting_connections: AtomicBool,
}

impl Endpoint {
    pub fn new<H>(
        path: impl Into<String>,
        connection_handler: H,
        options: Options,
        pubsub: Option<Arc<dyn PubSub>>,
    ) -> Arc<Self>
    where
        H: ConnectionHandler + 'static,
    {
        let mut options = options.normalized();
        if pubsub.is_some() && options.node_id.is_empty() {
            options.node_id = uuid();
        }
        let liveness = NodeLiveness::new(&options, pubsub.clone());
        Self::new_with_liveness(path, connection_handler, options, pubsub, liveness)
    }

    pub(crate) fn new_with_liveness<H>(
        path: impl Into<String>,
        connection_handler: H,
        options: Options,
        pubsub: Option<Arc<dyn PubSub>>,
        liveness: Arc<NodeLiveness>,
    ) -> Arc<Self>
    where
        H: ConnectionHandler + 'static,
    {
        let delivery_hub = DeliveryHub::new(options.internal_queue_buffer);
        let endpoint = Arc::new(Self {
            path: path.into(),
            connection_handler: Arc::new(connection_handler),
            options,
            pubsub,
            connections: RwLock::new(HashMap::new()),
            memberships: RwLock::new(HashMap::new()),
            channel_registrations: RwLock::new(Vec::new()),
            connect_times: RwLock::new(HashMap::new()),
            delivery_hub,
            liveness,
            connection_slots: AtomicUsize::new(0),
            lifecycle: Mutex::new(EndpointLifecycle::Open),
            accepting_connections: AtomicBool::new(true),
        });
        endpoint
            .delivery_hub
            .bind_endpoint(Arc::downgrade(&endpoint));
        endpoint
    }

    pub fn path(&self) -> &str {
        &self.path
    }

    pub async fn create_channel<H>(
        self: &Arc<Self>,
        pattern: impl Into<String>,
        handler: H,
    ) -> Result<Arc<Lobby>>
    where
        H: JoinHandler + 'static,
    {
        let lifecycle = self.lifecycle.lock().await;
        if *lifecycle != EndpointLifecycle::Open {
            return Err(internal(&self.path, "endpoint is closing or closed"));
        }
        let pattern = pattern.into();
        let lobby = Lobby::new_with_delivery_hub(
            pattern.clone(),
            self.path.clone(),
            self.options.clone(),
            self.pubsub.clone(),
            self.delivery_hub.clone(),
            self.liveness.clone(),
        );
        self.channel_registrations
            .write()
            .await
            .push(ChannelRegistration {
                pattern,
                handler: Arc::new(handler),
                lobby: lobby.clone(),
            });
        drop(lifecycle);
        Ok(lobby)
    }

    pub async fn request_connection(
        &self,
        mut incoming: IncomingConnection,
        route: Route,
        user_id: Option<String>,
    ) -> ConnectionContext {
        let uid = user_id.unwrap_or_else(uuid);
        incoming.id = uid.clone();
        let mut ctx = ConnectionContext::new(uid, incoming, route);
        let lifecycle = self.lifecycle.lock().await;
        if *lifecycle != EndpointLifecycle::Open {
            let _ = ctx.decline(503, "Endpoint is closing or closed");
            return ctx;
        }
        drop(lifecycle);
        let decision = timeout(
            self.options.handler_timeout.max(Duration::from_millis(1)),
            AssertUnwindSafe(self.connection_handler.call(&mut ctx)).catch_unwind(),
        )
        .await;
        match decision {
            Ok(Ok(Err(err))) if ctx.decision() == ConnectionDecision::Pending => {
                let _ = ctx.decline(err.code, err.message);
            }
            Ok(Err(_)) if ctx.decision() == ConnectionDecision::Pending => {
                let _ = ctx.decline(500, "Connection handler panicked");
            }
            Err(_) if ctx.decision() == ConnectionDecision::Pending => {
                let _ = ctx.decline(504, "Connection handler timed out");
            }
            _ => {}
        }
        if ctx.decision() == ConnectionDecision::Pending {
            let _ = ctx.decline(401, "Connection handler did not accept or decline");
        }
        ctx
    }

    pub fn try_reserve_connection(self: &Arc<Self>) -> Result<ConnectionPermit> {
        loop {
            if !self.accepting_connections.load(Ordering::SeqCst) {
                return Err(internal(&self.path, "endpoint is closing or closed"));
            }
            let current = self.connection_slots.load(Ordering::SeqCst);
            if self.options.max_connections > 0 && current >= self.options.max_connections {
                return Err(forbidden("GATEWAY", "Maximum connections reached"));
            }
            if self
                .connection_slots
                .compare_exchange(current, current + 1, Ordering::SeqCst, Ordering::SeqCst)
                .is_ok()
            {
                if !self.accepting_connections.load(Ordering::SeqCst) {
                    self.connection_slots.fetch_sub(1, Ordering::SeqCst);
                    return Err(internal(&self.path, "endpoint is closing or closed"));
                }
                return Ok(ConnectionPermit {
                    endpoint: Arc::downgrade(self),
                    active: true,
                });
            }
        }
    }

    pub async fn register_transport(self: &Arc<Self>, transport: Arc<dyn Transport>) -> Result<()> {
        let permit = self.try_reserve_connection()?;
        self.register_transport_with_permit(transport, permit).await
    }

    pub async fn register_transport_with_permit(
        self: &Arc<Self>,
        transport: Arc<dyn Transport>,
        mut permit: ConnectionPermit,
    ) -> Result<()> {
        let lifecycle = self.lifecycle.lock().await;
        if *lifecycle != EndpointLifecycle::Open {
            return Err(internal(&self.path, "endpoint is closing or closed"));
        }
        if !permit
            .endpoint
            .upgrade()
            .is_some_and(|endpoint| Arc::ptr_eq(&endpoint, self))
        {
            return Err(forbidden(
                "GATEWAY",
                "Connection permit belongs to another endpoint",
            ));
        }
        let user_id = transport.id().to_owned();
        {
            let mut connections = self.connections.write().await;
            if connections.contains_key(&user_id) {
                return Err(conflict(&self.path, "connection id already registered"));
            }
            connections.insert(user_id.clone(), transport.clone());
        }
        self.delivery_hub.register_owner(transport.clone()).await;
        self.connect_times
            .write()
            .await
            .insert(user_id.clone(), Instant::now());
        if let Err(error) = self.send_connect_event(transport.clone()).await {
            self.connections.write().await.remove(&user_id);
            self.connect_times.write().await.remove(&user_id);
            self.delivery_hub.unregister(&user_id).await;
            return Err(error);
        }
        if let Some(hooks) = &self.options.hooks {
            if let Some(metrics) = &hooks.metrics {
                let _ = std::panic::catch_unwind(AssertUnwindSafe(|| {
                    metrics.connection_opened(&user_id, &self.path);
                }));
            }
            if let Some(hook) = &hooks.on_connect {
                let hook_result = timeout(
                    self.options.handler_timeout.max(Duration::from_millis(1)),
                    AssertUnwindSafe(hook.call(&user_id)).catch_unwind(),
                )
                .await;
                if !matches!(hook_result, Ok(Ok(()))) {
                    self.connections.write().await.remove(&user_id);
                    self.connect_times.write().await.remove(&user_id);
                    self.delivery_hub.unregister(&user_id).await;
                    let message = if hook_result.is_err() {
                        "on_connect hook timed out"
                    } else {
                        "on_connect hook panicked"
                    };
                    return Err(internal(&self.path, message));
                }
            }
        }
        permit.commit();
        drop(lifecycle);
        Ok(())
    }

    pub async fn unregister_transport(&self, user_id: &str) -> Result<()> {
        self.unregister_transport_inner(user_id).await.1
    }

    async fn unregister_transport_inner(
        &self,
        user_id: &str,
    ) -> (Option<Arc<dyn Transport>>, Result<()>) {
        let transport = self.connections.write().await.remove(user_id);
        let was_registered = transport.is_some();
        if was_registered {
            self.connection_slots.fetch_sub(1, Ordering::SeqCst);
        }
        let memberships = self
            .memberships
            .write()
            .await
            .remove(user_id)
            .unwrap_or_default();
        let mut first_error = None;
        for channel in memberships
            .into_iter()
            .filter_map(|channel| channel.upgrade())
        {
            if channel.has_user(user_id).await
                && let Err(error) = channel.remove_user(user_id, "connection_closed").await
                && first_error.is_none()
            {
                first_error = Some(error);
            }
        }
        let duration = self
            .connect_times
            .write()
            .await
            .remove(user_id)
            .map(|t| t.elapsed());
        self.delivery_hub.unregister(user_id).await;
        if was_registered && let Some(hooks) = &self.options.hooks {
            if let (Some(metrics), Some(duration)) = (&hooks.metrics, duration) {
                let _ = std::panic::catch_unwind(AssertUnwindSafe(|| {
                    metrics.connection_closed(user_id, duration);
                }));
            }
            if let Some(hook) = &hooks.on_disconnect {
                let hook_result = timeout(
                    self.options.handler_timeout.max(Duration::from_millis(1)),
                    AssertUnwindSafe(hook.call(user_id)).catch_unwind(),
                )
                .await;
                if !matches!(hook_result, Ok(Ok(()))) && first_error.is_none() {
                    let message = if hook_result.is_err() {
                        "on_disconnect hook timed out"
                    } else {
                        "on_disconnect hook panicked"
                    };
                    first_error = Some(internal(&self.path, message));
                }
            }
            if let Some(rate_limiter) = &hooks.rate_limiter {
                let reset_result = timeout(
                    self.options.handler_timeout.max(Duration::from_millis(1)),
                    AssertUnwindSafe(rate_limiter.reset(user_id)).catch_unwind(),
                )
                .await;
                if !matches!(reset_result, Ok(Ok(()))) {
                    let message = if reset_result.is_err() {
                        "rate limiter reset timed out"
                    } else {
                        "rate limiter reset panicked"
                    };
                    let error = internal(&self.path, message);
                    if let Some(metrics) = &hooks.metrics {
                        let _ = std::panic::catch_unwind(AssertUnwindSafe(|| {
                            metrics.error("endpoint.unregister_transport", &error);
                        }));
                    }
                    if first_error.is_none() {
                        first_error = Some(error);
                    }
                }
            }
        }
        let result = match first_error {
            Some(error) => Err(error),
            None => Ok(()),
        };
        (transport, result)
    }

    pub(crate) async fn handle_delivery_failure(&self, user_id: &str) {
        let (transport, _) = self.unregister_transport_inner(user_id).await;
        if let Some(transport) = transport {
            let _ = transport.close().await;
        }
    }

    pub async fn handle_message(
        self: &Arc<Self>,
        event: Event,
        transport: Arc<dyn Transport>,
    ) -> Result<()> {
        let registered = self.get_transport(transport.id()).await;
        let is_current = registered
            .as_ref()
            .is_some_and(|current| Arc::ptr_eq(current, &transport));
        let is_active = if is_current {
            timeout(
                self.options.handler_timeout.max(Duration::from_millis(1)),
                transport.is_active(),
            )
            .await
            .unwrap_or(false)
        } else {
            false
        };
        if !is_current || !is_active {
            if is_current {
                let (removed, _) = self.unregister_transport_inner(transport.id()).await;
                if let Some(removed) = removed {
                    let _ = removed.close().await;
                }
            } else {
                let _ = transport.close().await;
            }
            return Err(forbidden(
                "GATEWAY",
                "Transport is not the active registered connection",
            ));
        }
        if let Some(hooks) = &self.options.hooks {
            if let Some(rate_limiter) = &hooks.rate_limiter {
                let allowed = timeout(
                    self.options.handler_timeout.max(Duration::from_millis(1)),
                    AssertUnwindSafe(rate_limiter.allow(transport.id())).catch_unwind(),
                )
                .await;
                if !matches!(allowed, Ok(Ok(true))) {
                    let error = PondError::new(&event.channel_name, 429, "Rate limit exceeded")
                        .temporary(true);
                    let _ = transport
                        .send_event(Event::new(
                            "ERROR",
                            &event.channel_name,
                            &event.request_id,
                            "RATE_LIMITED",
                            json!({ "code": 429, "message": error.message.clone() }),
                        ))
                        .await;
                    self.report_message_result(&event, Some(&error)).await;
                    return Err(error);
                }
            }
            if let Some(before) = &hooks.before_message {
                let before_result = timeout(
                    self.options.handler_timeout.max(Duration::from_millis(1)),
                    AssertUnwindSafe(before.call(&event, None)).catch_unwind(),
                )
                .await;
                if !matches!(before_result, Ok(Ok(()))) {
                    let message = if before_result.is_err() {
                        "before_message hook timed out"
                    } else {
                        "before_message hook panicked"
                    };
                    let error = internal(&event.channel_name, message);
                    self.report_message_result(&event, Some(&error)).await;
                    return Err(error);
                }
            }
        }

        let observed_event = event.clone();
        let result = match event.action.as_str() {
            "JOIN_CHANNEL" => self.handle_join(event, transport).await,
            "LEAVE_CHANNEL" => self.handle_leave(event, transport).await,
            "BROADCAST" => self.handle_broadcast(event, transport).await,
            _ => Ok(()),
        };
        self.report_message_result(&observed_event, result.as_ref().err())
            .await;
        result
    }

    async fn report_message_result(&self, event: &Event, error: Option<&PondError>) {
        let Some(hooks) = &self.options.hooks else {
            return;
        };
        if let Some(after) = &hooks.after_message {
            let _ = timeout(
                self.options.handler_timeout.max(Duration::from_millis(1)),
                AssertUnwindSafe(after.call(event, error)).catch_unwind(),
            )
            .await;
        }
        if let Some(error) = error
            && let Some(metrics) = &hooks.metrics
        {
            let _ = std::panic::catch_unwind(AssertUnwindSafe(|| {
                metrics.error("endpoint.handle_message", error);
            }));
        }
    }

    async fn handle_join(
        self: &Arc<Self>,
        event: Event,
        transport: Arc<dyn Transport>,
    ) -> Result<()> {
        let registration = {
            let registrations = self.channel_registrations.read().await;
            registrations.iter().find_map(|reg| {
                parse(&reg.pattern, &event.channel_name)
                    .ok()
                    .map(|route| (reg.handler.clone(), reg.lobby.clone(), route))
            })
        };
        if let Some((handler, lobby, route)) = registration {
            let permit = lobby.acquire_join_permit(&event.channel_name).await?;
            let channel = permit.channel();
            let mut ctx =
                JoinContext::new(channel.clone(), event.clone(), transport.clone(), route);
            if let Some(hooks) = &self.options.hooks
                && let Some(before) = &hooks.before_join
            {
                let user = User {
                    id: transport.id().to_owned(),
                    assigns: transport.clone_assigns().await,
                    presence: None,
                };
                let before_result = timeout(
                    self.options.handler_timeout.max(Duration::from_millis(1)),
                    AssertUnwindSafe(before.call(&user, &event.channel_name)).catch_unwind(),
                )
                .await;
                if !matches!(before_result, Ok(Ok(()))) {
                    let message = if before_result.is_err() {
                        "before_join hook timed out"
                    } else {
                        "before_join hook panicked"
                    };
                    let outcome = ctx.decline(500, message).await;
                    permit.finish().await;
                    return outcome;
                }
            }
            match timeout(
                self.options.handler_timeout.max(Duration::from_millis(1)),
                AssertUnwindSafe(handler.call(&mut ctx)).catch_unwind(),
            )
            .await
            {
                Ok(Ok(Err(err))) if !ctx.has_responded() => {
                    let _ = ctx.decline(err.code, err.message).await;
                }
                Ok(Err(_)) if !ctx.has_responded() => {
                    let _ = ctx.decline(500, "Join handler panicked").await;
                }
                Err(_) if !ctx.has_responded() => {
                    let _ = ctx.decline(504, "Join handler timed out").await;
                }
                _ => {}
            }
            let mut outcome = Ok(());
            if !ctx.has_responded() {
                outcome = ctx.decline(401, "Join handler did not respond").await;
            }
            let committed = if ctx.is_accepted() {
                let connections = self.connections.read().await;
                let current = connections.get(transport.id());
                if current.is_some_and(|current| Arc::ptr_eq(current, &transport)) {
                    self.add_membership(transport.id(), &channel).await;
                    true
                } else {
                    if current.is_none() {
                        self.delivery_hub.unregister(transport.id()).await;
                    }
                    let _ = channel
                        .remove_user(transport.id(), "stale_connection")
                        .await;
                    outcome = Err(forbidden(
                        "GATEWAY",
                        "Connection closed while joining channel",
                    ));
                    false
                }
            } else {
                false
            };
            if committed
                && let Some(hooks) = &self.options.hooks
                && let Some(after) = &hooks.after_join
            {
                let user = User {
                    id: transport.id().to_owned(),
                    assigns: transport.clone_assigns().await,
                    presence: None,
                };
                let _ = timeout(
                    self.options.handler_timeout.max(Duration::from_millis(1)),
                    AssertUnwindSafe(after.call(&user, &event.channel_name)).catch_unwind(),
                )
                .await;
            }
            permit.finish().await;
            return outcome;
        }
        let channel_name = event.channel_name.clone();
        transport
            .send_event(Event::new(
                "SYSTEM",
                channel_name.clone(),
                event.request_id,
                "NOT_FOUND",
                json!({ "channel": channel_name }),
            ))
            .await
    }

    async fn handle_leave(&self, event: Event, transport: Arc<dyn Transport>) -> Result<()> {
        if let Some(channel) = self.find_channel(&event.channel_name).await {
            let removal = channel.remove_user(transport.id(), "explicit_leave").await;
            self.remove_membership(transport.id(), &channel).await;
            removal?;
            transport
                .send_event(Event::new(
                    "SYSTEM",
                    event.channel_name,
                    event.request_id,
                    "EXIT_ACKNOWLEDGE",
                    json!({}),
                ))
                .await?;
        }
        Ok(())
    }

    async fn handle_broadcast(
        self: &Arc<Self>,
        event: Event,
        transport: Arc<dyn Transport>,
    ) -> Result<()> {
        let Some(channel) = self.find_channel(&event.channel_name).await else {
            let channel_name = event.channel_name.clone();
            return transport
                .send_event(Event::new(
                    "SYSTEM",
                    channel_name.clone(),
                    event.request_id,
                    "NOT_FOUND",
                    json!({ "channel": channel_name }),
                ))
                .await;
        };
        channel.handle_incoming_event(event, transport.id()).await
    }

    async fn find_channel(&self, name: &str) -> Option<Arc<crate::channel::Channel>> {
        for reg in self.channel_registrations.read().await.iter() {
            if let Some(channel) = reg.lobby.get_channel(name).await {
                return Some(channel);
            }
        }
        None
    }

    async fn send_connect_event(&self, transport: Arc<dyn Transport>) -> Result<()> {
        let id = transport.id().to_owned();
        transport
            .send_event(Event::new(
                "CONNECT",
                "GATEWAY",
                uuid(),
                "CONNECTION",
                json!({ "id": id, "connectionId": id }),
            ))
            .await
    }

    pub async fn get_transport(&self, user_id: &str) -> Option<Arc<dyn Transport>> {
        self.connections.read().await.get(user_id).cloned()
    }

    pub async fn close_connection(&self, user_id: &str) -> Result<()> {
        let (transport, unregister_result) = self.unregister_transport_inner(user_id).await;
        let Some(transport) = transport else {
            return Err(not_found(&self.path, "connection not found"));
        };
        let close_result = transport.close().await;
        close_result.and(unregister_result)
    }

    pub async fn close(&self) -> Result<()> {
        let mut lifecycle = self.lifecycle.lock().await;
        if *lifecycle == EndpointLifecycle::Closed {
            return Ok(());
        }
        *lifecycle = EndpointLifecycle::Closing;
        self.accepting_connections.store(false, Ordering::SeqCst);
        let mut first_error = None;
        let user_ids = self
            .connections
            .read()
            .await
            .keys()
            .cloned()
            .collect::<Vec<_>>();
        for user_id in user_ids {
            if let Err(error) = self.close_connection(&user_id).await
                && first_error.is_none()
            {
                first_error = Some(error);
            }
        }
        let lobbies = self
            .channel_registrations
            .read()
            .await
            .iter()
            .map(|reg| reg.lobby.clone())
            .collect::<Vec<_>>();
        for lobby in lobbies {
            if let Err(error) = lobby.close().await
                && first_error.is_none()
            {
                first_error = Some(error);
            }
        }
        self.channel_registrations.write().await.clear();
        self.delivery_hub.close().await;
        self.connections.write().await.clear();
        self.memberships.write().await.clear();
        self.connect_times.write().await.clear();
        *lifecycle = EndpointLifecycle::Closed;
        match first_error {
            Some(error) => Err(error),
            None => Ok(()),
        }
    }

    pub fn max_message_size(&self) -> usize {
        self.options.max_message_size
    }

    pub fn internal_queue_buffer(&self) -> usize {
        self.options.internal_queue_buffer.max(1)
    }

    pub fn internal_queue_timeout(&self) -> std::time::Duration {
        self.options.internal_queue_timeout
    }

    pub fn socket_ping_interval(&self) -> Duration {
        self.options.socket_ping_interval
    }

    pub fn socket_pong_timeout(&self) -> Duration {
        self.options.socket_pong_timeout
    }

    pub fn socket_idle_timeout(&self) -> Duration {
        self.options.socket_idle_timeout
    }

    async fn add_membership(&self, user_id: &str, channel: &Arc<Channel>) {
        let mut memberships = self.memberships.write().await;
        let channels = memberships.entry(user_id.to_owned()).or_default();
        if !channels
            .iter()
            .any(|existing| existing.ptr_eq(&Arc::downgrade(channel)))
        {
            channels.push(Arc::downgrade(channel));
        }
    }

    async fn remove_membership(&self, user_id: &str, channel: &Arc<Channel>) {
        let mut memberships = self.memberships.write().await;
        let Some(channels) = memberships.get_mut(user_id) else {
            return;
        };
        let target = Arc::downgrade(channel);
        channels.retain(|existing| !existing.ptr_eq(&target) && existing.strong_count() > 0);
        if channels.is_empty() {
            memberships.remove(user_id);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::errors::unavailable;
    use crate::hooks::{ConnectHook, Hooks, MetricsCollector, RateLimiter};
    use crate::transport::MemoryTransport;
    use crate::types::TransportType;
    use async_trait::async_trait;
    use pondsocket_common::PondAssigns;
    use serde_json::Value;
    use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
    use tokio::sync::{Notify, RwLock};

    struct AcceptConnection;

    struct HangingConnection;

    #[async_trait]
    impl ConnectionHandler for AcceptConnection {
        async fn call(&self, ctx: &mut ConnectionContext) -> Result<()> {
            ctx.accept(PondAssigns::new())?;
            Ok(())
        }
    }

    #[async_trait]
    impl ConnectionHandler for HangingConnection {
        async fn call(&self, _ctx: &mut ConnectionContext) -> Result<()> {
            std::future::pending().await
        }
    }

    struct FailingTransport {
        id: String,
    }

    struct DenyLimiter;

    struct HangingResetLimiter;

    struct AcceptJoin;

    struct PausingJoin {
        entered: Arc<Notify>,
        release: Arc<Notify>,
    }

    #[async_trait]
    impl JoinHandler for AcceptJoin {
        async fn call(&self, ctx: &mut JoinContext) -> Result<()> {
            ctx.accept(PondAssigns::new()).await?;
            Ok(())
        }
    }

    #[async_trait]
    impl JoinHandler for PausingJoin {
        async fn call(&self, ctx: &mut JoinContext) -> Result<()> {
            self.entered.notify_one();
            self.release.notified().await;
            ctx.accept(PondAssigns::new()).await?;
            Ok(())
        }
    }

    #[derive(Default)]
    struct CleanupCounters {
        closed: AtomicUsize,
        disconnected: AtomicUsize,
        reset: AtomicUsize,
    }

    struct CountingMetrics(Arc<CleanupCounters>);

    impl MetricsCollector for CountingMetrics {
        fn connection_closed(&self, _conn_id: &str, _duration: Duration) {
            self.0.closed.fetch_add(1, Ordering::SeqCst);
        }
    }

    struct CountingDisconnect(Arc<CleanupCounters>);

    #[async_trait]
    impl ConnectHook for CountingDisconnect {
        async fn call(&self, _user_id: &str) {
            self.0.disconnected.fetch_add(1, Ordering::SeqCst);
        }
    }

    struct CountingLimiter(Arc<CleanupCounters>);

    #[async_trait]
    impl RateLimiter for CountingLimiter {
        async fn allow(&self, _key: &str) -> bool {
            true
        }

        async fn reset(&self, _key: &str) {
            self.0.reset.fetch_add(1, Ordering::SeqCst);
        }
    }

    struct SlowTransport {
        id: String,
        assigns: RwLock<PondAssigns>,
        block: AtomicBool,
        started: Notify,
        release: Notify,
        close_count: AtomicUsize,
    }

    impl SlowTransport {
        fn new(id: &str) -> Self {
            Self {
                id: id.to_owned(),
                assigns: RwLock::new(PondAssigns::new()),
                block: AtomicBool::new(false),
                started: Notify::new(),
                release: Notify::new(),
                close_count: AtomicUsize::new(0),
            }
        }
    }

    #[async_trait]
    impl Transport for SlowTransport {
        fn id(&self) -> &str {
            &self.id
        }

        async fn send_event(&self, _event: Event) -> Result<()> {
            if self.block.load(Ordering::SeqCst) {
                self.started.notify_one();
                self.release.notified().await;
            }
            Ok(())
        }

        async fn close(&self) -> Result<()> {
            self.close_count.fetch_add(1, Ordering::SeqCst);
            self.release.notify_waiters();
            Ok(())
        }

        fn transport_type(&self) -> TransportType {
            TransportType::InMemory
        }

        async fn is_active(&self) -> bool {
            self.close_count.load(Ordering::SeqCst) == 0
        }

        async fn get_assign(&self, key: &str) -> Option<Value> {
            self.assigns.read().await.get(key).cloned()
        }

        async fn set_assign(&self, key: &str, value: Value) {
            self.assigns.write().await.insert(key.to_owned(), value);
        }

        async fn clone_assigns(&self) -> PondAssigns {
            self.assigns.read().await.clone()
        }
    }

    fn cleanup_options(counters: &Arc<CleanupCounters>) -> Options {
        Options {
            internal_queue_buffer: 1,
            hooks: Some(Arc::new(Hooks {
                rate_limiter: Some(Arc::new(CountingLimiter(counters.clone()))),
                metrics: Some(Arc::new(CountingMetrics(counters.clone()))),
                on_disconnect: Some(Arc::new(CountingDisconnect(counters.clone()))),
                ..Hooks::default()
            })),
            ..Options::default()
        }
    }

    async fn wait_for_cleanup(
        endpoint: &Endpoint,
        channel: &Arc<Channel>,
        transport: &SlowTransport,
        counters: &CleanupCounters,
    ) {
        tokio::time::timeout(Duration::from_secs(2), async {
            loop {
                if endpoint.get_transport("slow").await.is_none()
                    && !channel.has_user("slow").await
                    && transport.close_count.load(Ordering::SeqCst) == 1
                    && counters.closed.load(Ordering::SeqCst) == 1
                    && counters.disconnected.load(Ordering::SeqCst) == 1
                    && counters.reset.load(Ordering::SeqCst) == 1
                {
                    break;
                }
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("delivery failure did not fully unregister the endpoint");
    }

    #[async_trait]
    impl RateLimiter for DenyLimiter {
        async fn allow(&self, _key: &str) -> bool {
            false
        }

        async fn reset(&self, _key: &str) {}
    }

    #[async_trait]
    impl RateLimiter for HangingResetLimiter {
        async fn allow(&self, _key: &str) -> bool {
            true
        }

        async fn reset(&self, _key: &str) {
            std::future::pending().await
        }
    }

    #[async_trait]
    impl Transport for FailingTransport {
        fn id(&self) -> &str {
            &self.id
        }

        async fn send_event(&self, _event: Event) -> Result<()> {
            Err(unavailable("transport", "writer failed"))
        }

        async fn close(&self) -> Result<()> {
            Ok(())
        }

        fn transport_type(&self) -> TransportType {
            TransportType::InMemory
        }

        async fn is_active(&self) -> bool {
            true
        }

        async fn get_assign(&self, _key: &str) -> Option<Value> {
            None
        }

        async fn set_assign(&self, _key: &str, _value: Value) {}

        async fn clone_assigns(&self) -> PondAssigns {
            PondAssigns::new()
        }
    }

    #[tokio::test]
    async fn adv_failed_connect_frame_rolls_back_registration() {
        let endpoint = Endpoint::new("/socket", AcceptConnection, Options::default(), None);
        let failed = Arc::new(FailingTransport {
            id: "same-id".to_owned(),
        });

        assert!(endpoint.register_transport(failed).await.is_err());
        assert!(endpoint.get_transport("same-id").await.is_none());

        let replacement = Arc::new(MemoryTransport::new("same-id", PondAssigns::new()));
        endpoint.register_transport(replacement).await.unwrap();
        assert!(endpoint.get_transport("same-id").await.is_some());
    }

    #[tokio::test]
    async fn adv_connection_handler_timeout_declines_instead_of_hanging() {
        let endpoint = Endpoint::new(
            "/socket",
            HangingConnection,
            Options {
                handler_timeout: Duration::from_millis(10),
                ..Options::default()
            },
            None,
        );
        let ctx = tokio::time::timeout(
            Duration::from_secs(1),
            endpoint.request_connection(Default::default(), Route::default(), None),
        )
        .await
        .expect("connection handler timeout was not enforced");
        assert!(ctx.is_declined());
        assert_eq!(ctx.decline_info().0, 504);
    }

    #[tokio::test]
    async fn adv_configured_rate_limiter_is_enforced() {
        let options = Options {
            hooks: Some(Arc::new(Hooks {
                rate_limiter: Some(Arc::new(DenyLimiter)),
                ..Hooks::default()
            })),
            ..Options::default()
        };
        let endpoint = Endpoint::new("/socket", AcceptConnection, options, None);
        let transport = Arc::new(MemoryTransport::new("u1", PondAssigns::new()));
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();
        let connection = transport.recv().await.unwrap();
        assert_eq!(connection.event, "CONNECTION");

        let error = endpoint
            .handle_message(
                Event::new("BROADCAST", "/room", "r1", "message", json!({})),
                transport.clone(),
            )
            .await
            .unwrap_err();

        assert_eq!(error.code, 429);
        let feedback = transport.recv().await.unwrap();
        assert_eq!(feedback.event, "RATE_LIMITED");
    }

    #[tokio::test]
    async fn adv_delivery_overflow_cleans_connection_and_membership_once() {
        let counters = Arc::new(CleanupCounters::default());
        let endpoint = Endpoint::new(
            "/socket",
            AcceptConnection,
            cleanup_options(&counters),
            None,
        );
        let lobby = endpoint
            .create_channel("/room/:id", AcceptJoin)
            .await
            .unwrap();
        let transport = Arc::new(SlowTransport::new("slow"));
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();
        endpoint
            .handle_message(
                Event::new("JOIN_CHANNEL", "/room/1", "join", "JOIN_CHANNEL", json!({})),
                transport.clone(),
            )
            .await
            .unwrap();
        let channel = lobby.get_channel("/room/1").await.unwrap();
        assert!(channel.has_user("slow").await);

        transport.block.store(true, Ordering::SeqCst);
        channel.broadcast("one", json!({})).await.unwrap();
        tokio::time::timeout(Duration::from_secs(1), transport.started.notified())
            .await
            .unwrap();
        channel.broadcast("two", json!({})).await.unwrap();
        assert!(channel.broadcast("three", json!({})).await.is_err());

        wait_for_cleanup(&endpoint, &channel, &transport, &counters).await;
        endpoint.handle_delivery_failure("slow").await;
        assert_eq!(transport.close_count.load(Ordering::SeqCst), 1);
        assert_eq!(counters.closed.load(Ordering::SeqCst), 1);
        assert_eq!(counters.disconnected.load(Ordering::SeqCst), 1);
        assert_eq!(counters.reset.load(Ordering::SeqCst), 1);
    }

    #[tokio::test]
    async fn adv_missing_delivery_entry_errors_and_leaves_no_ghost_member() {
        let counters = Arc::new(CleanupCounters::default());
        let endpoint = Endpoint::new(
            "/socket",
            AcceptConnection,
            cleanup_options(&counters),
            None,
        );
        let lobby = endpoint
            .create_channel("/room/:id", AcceptJoin)
            .await
            .unwrap();
        let transport = Arc::new(SlowTransport::new("slow"));
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();
        endpoint
            .handle_message(
                Event::new("JOIN_CHANNEL", "/room/1", "join", "JOIN_CHANNEL", json!({})),
                transport.clone(),
            )
            .await
            .unwrap();
        let channel = lobby.get_channel("/room/1").await.unwrap();

        endpoint.delivery_hub.unregister("slow").await;
        assert!(channel.broadcast("missing", json!({})).await.is_err());

        wait_for_cleanup(&endpoint, &channel, &transport, &counters).await;
    }

    #[tokio::test]
    async fn adv_endpoint_close_uses_unregister_cleanup_once() {
        let counters = Arc::new(CleanupCounters::default());
        let endpoint = Endpoint::new(
            "/socket",
            AcceptConnection,
            cleanup_options(&counters),
            None,
        );
        let lobby = endpoint
            .create_channel("/room/:id", AcceptJoin)
            .await
            .unwrap();
        let transport = Arc::new(SlowTransport::new("slow"));
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();
        endpoint
            .handle_message(
                Event::new("JOIN_CHANNEL", "/room/1", "join", "JOIN_CHANNEL", json!({})),
                transport.clone(),
            )
            .await
            .unwrap();
        let channel = lobby.get_channel("/room/1").await.unwrap();

        endpoint.close().await.unwrap();

        assert!(endpoint.get_transport("slow").await.is_none());
        assert!(!channel.has_user("slow").await);
        assert_eq!(transport.close_count.load(Ordering::SeqCst), 1);
        assert_eq!(counters.closed.load(Ordering::SeqCst), 1);
        assert_eq!(counters.disconnected.load(Ordering::SeqCst), 1);
        assert_eq!(counters.reset.load(Ordering::SeqCst), 1);
    }

    #[tokio::test]
    async fn hung_rate_limiter_reset_is_bounded_during_close() {
        let endpoint = Endpoint::new(
            "/socket",
            AcceptConnection,
            Options {
                handler_timeout: Duration::from_millis(10),
                hooks: Some(Arc::new(Hooks {
                    rate_limiter: Some(Arc::new(HangingResetLimiter)),
                    ..Hooks::default()
                })),
                ..Options::default()
            },
            None,
        );
        let transport = Arc::new(MemoryTransport::new("hung", PondAssigns::new()));
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();

        let result = tokio::time::timeout(Duration::from_secs(1), endpoint.close())
            .await
            .expect("endpoint close hung on rate limiter reset");

        assert!(result.is_err());
        assert!(endpoint.get_transport("hung").await.is_none());
        assert!(!transport.is_active().await);
    }

    #[tokio::test]
    async fn connection_permit_reserves_capacity_before_registration() {
        let endpoint = Endpoint::new(
            "/socket",
            AcceptConnection,
            Options {
                max_connections: 1,
                ..Options::default()
            },
            None,
        );
        let permit = endpoint.try_reserve_connection().unwrap();
        assert!(endpoint.try_reserve_connection().is_err());
        drop(permit);

        let permit = endpoint.try_reserve_connection().unwrap();
        let transport = Arc::new(MemoryTransport::new("one", PondAssigns::new()));
        endpoint
            .register_transport_with_permit(transport, permit)
            .await
            .unwrap();
        assert!(endpoint.try_reserve_connection().is_err());

        endpoint.close_connection("one").await.unwrap();
        assert!(endpoint.try_reserve_connection().is_ok());
    }

    #[tokio::test]
    async fn stale_transport_with_reused_id_cannot_dispatch_or_replace_current() {
        let endpoint = Endpoint::new("/socket", AcceptConnection, Options::default(), None);
        let endpoint_lobby = endpoint
            .create_channel("/room/:id", AcceptJoin)
            .await
            .unwrap();
        let current = Arc::new(MemoryTransport::new("same", PondAssigns::new()));
        let stale = Arc::new(MemoryTransport::new("same", PondAssigns::new()));
        endpoint.register_transport(current.clone()).await.unwrap();

        let result = endpoint
            .handle_message(
                Event::new("JOIN_CHANNEL", "/room/1", "join", "JOIN_CHANNEL", json!({})),
                stale.clone(),
            )
            .await;

        assert!(result.is_err());
        assert!(!stale.is_active().await);
        assert!(current.is_active().await);
        let registered = endpoint.get_transport("same").await.unwrap();
        let current_dyn: Arc<dyn Transport> = current;
        assert!(Arc::ptr_eq(&registered, &current_dyn));
        assert!(endpoint_lobby.get_channel("/room/1").await.is_none());
    }

    #[tokio::test]
    async fn overflow_during_join_cannot_resurrect_hub_or_membership() {
        let entered = Arc::new(Notify::new());
        let release = Arc::new(Notify::new());
        let endpoint = Endpoint::new("/socket", AcceptConnection, Options::default(), None);
        let lobby = endpoint
            .create_channel(
                "/room/:id",
                PausingJoin {
                    entered: entered.clone(),
                    release: release.clone(),
                },
            )
            .await
            .unwrap();
        let transport = Arc::new(SlowTransport::new("slow"));
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();

        let join_endpoint = endpoint.clone();
        let join_transport = transport.clone();
        let join = tokio::spawn(async move {
            join_endpoint
                .handle_message(
                    Event::new("JOIN_CHANNEL", "/room/1", "join", "JOIN_CHANNEL", json!({})),
                    join_transport,
                )
                .await
        });
        tokio::time::timeout(Duration::from_secs(1), entered.notified())
            .await
            .unwrap();

        endpoint.handle_delivery_failure("slow").await;
        release.notify_waiters();
        assert!(join.await.unwrap().is_err());

        assert!(endpoint.get_transport("slow").await.is_none());
        if let Some(channel) = lobby.get_channel("/room/1").await {
            assert!(!channel.has_user("slow").await);
        }
        assert_eq!(endpoint.delivery_hub.entry_count().await, 0);
        assert_eq!(transport.close_count.load(Ordering::SeqCst), 1);
    }

    #[tokio::test]
    async fn close_serializes_with_registration_and_rejects_post_close_register() {
        let endpoint = Endpoint::new("/socket", AcceptConnection, Options::default(), None);
        let transport = Arc::new(SlowTransport::new("racing"));
        transport.block.store(true, Ordering::SeqCst);

        let register_endpoint = endpoint.clone();
        let register_transport = transport.clone();
        let register = tokio::spawn(async move {
            register_endpoint
                .register_transport(register_transport)
                .await
        });
        tokio::time::timeout(Duration::from_secs(1), transport.started.notified())
            .await
            .expect("registration never reached the blocked connect send");

        let close_endpoint = endpoint.clone();
        let close = tokio::spawn(async move { close_endpoint.close().await });
        tokio::task::yield_now().await;
        assert!(!close.is_finished());

        transport.release.notify_waiters();
        register.await.unwrap().unwrap();
        close.await.unwrap().unwrap();

        assert!(endpoint.get_transport("racing").await.is_none());
        assert_eq!(endpoint.delivery_hub.entry_count().await, 0);
        let late = Arc::new(MemoryTransport::new("late", PondAssigns::new()));
        assert!(endpoint.register_transport(late).await.is_err());
        endpoint.close().await.unwrap();
    }

    #[tokio::test]
    async fn close_serializes_with_lobby_creation_and_clears_registrations() {
        let endpoint = Endpoint::new("/socket", AcceptConnection, Options::default(), None);
        let registrations_guard = endpoint.channel_registrations.write().await;
        let create_endpoint = endpoint.clone();
        let create = tokio::spawn(async move {
            create_endpoint
                .create_channel("/room/:id", AcceptJoin)
                .await
        });
        tokio::time::timeout(Duration::from_secs(1), async {
            loop {
                if endpoint.lifecycle.try_lock().is_err() {
                    break;
                }
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("lobby creation never acquired endpoint lifecycle lock");

        let close_endpoint = endpoint.clone();
        let close = tokio::spawn(async move { close_endpoint.close().await });
        tokio::task::yield_now().await;
        assert!(!close.is_finished());
        drop(registrations_guard);

        let lobby = create.await.unwrap().unwrap();
        close.await.unwrap().unwrap();
        assert!(endpoint.channel_registrations.read().await.is_empty());
        assert!(lobby.get_or_create_channel("/room/1").await.is_err());
        assert!(
            endpoint
                .create_channel("/late/:id", AcceptJoin)
                .await
                .is_err()
        );
    }
}
