use async_trait::async_trait;
use pondsocket_common::uuid;
use serde_json::json;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Instant;
use tokio::sync::RwLock;

use crate::contexts::{ConnectionContext, ConnectionDecision, IncomingConnection, JoinContext};
use crate::errors::{Result, conflict, forbidden, not_found};
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

pub struct Endpoint {
    path: String,
    connection_handler: Arc<dyn ConnectionHandler>,
    options: Options,
    pubsub: Option<Arc<dyn PubSub>>,
    connections: RwLock<HashMap<String, Arc<dyn Transport>>>,
    channel_registrations: RwLock<Vec<ChannelRegistration>>,
    connect_times: RwLock<HashMap<String, Instant>>,
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
        Arc::new(Self {
            path: path.into(),
            connection_handler: Arc::new(connection_handler),
            options,
            pubsub,
            connections: RwLock::new(HashMap::new()),
            channel_registrations: RwLock::new(Vec::new()),
            connect_times: RwLock::new(HashMap::new()),
        })
    }

    pub fn path(&self) -> &str {
        &self.path
    }

    pub async fn create_channel<H>(
        self: &Arc<Self>,
        pattern: impl Into<String>,
        handler: H,
    ) -> Arc<Lobby>
    where
        H: JoinHandler + 'static,
    {
        let pattern = pattern.into();
        let lobby = Lobby::new(
            pattern.clone(),
            self.path.clone(),
            self.options.clone(),
            self.pubsub.clone(),
        );
        self.channel_registrations
            .write()
            .await
            .push(ChannelRegistration {
                pattern,
                handler: Arc::new(handler),
                lobby: lobby.clone(),
            });
        lobby
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
        if let Err(err) = self.connection_handler.call(&mut ctx).await
            && ctx.decision() == ConnectionDecision::Pending
        {
            let _ = ctx.decline(err.code, err.message);
        }
        if ctx.decision() == ConnectionDecision::Pending {
            let _ = ctx.decline(401, "Connection handler did not accept or decline");
        }
        ctx
    }

    pub async fn register_transport(&self, transport: Arc<dyn Transport>) -> Result<()> {
        let user_id = transport.id().to_owned();
        {
            let mut connections = self.connections.write().await;
            if connections.contains_key(&user_id) {
                return Err(conflict(&self.path, "connection id already registered"));
            }
            if self.options.max_connections > 0 && connections.len() >= self.options.max_connections
            {
                return Err(forbidden("GATEWAY", "Maximum connections reached"));
            }
            connections.insert(user_id.clone(), transport.clone());
        }
        self.connect_times
            .write()
            .await
            .insert(user_id.clone(), Instant::now());
        self.send_connect_event(transport.clone()).await?;
        if let Some(hooks) = &self.options.hooks {
            if let Some(metrics) = &hooks.metrics {
                metrics.connection_opened(&user_id, &self.path);
            }
            if let Some(hook) = &hooks.on_connect {
                hook.call(&user_id).await;
            }
        }
        Ok(())
    }

    pub async fn unregister_transport(&self, user_id: &str) -> Result<()> {
        let lobbies = self
            .channel_registrations
            .read()
            .await
            .iter()
            .map(|reg| reg.lobby.clone())
            .collect::<Vec<_>>();
        for lobby in lobbies {
            for channel in lobby.list_channels().await {
                if channel.has_user(user_id).await {
                    channel.remove_user(user_id, "connection_closed").await?;
                }
            }
        }
        self.connections.write().await.remove(user_id);
        let duration = self
            .connect_times
            .write()
            .await
            .remove(user_id)
            .map(|t| t.elapsed());
        if let Some(hooks) = &self.options.hooks {
            if let (Some(metrics), Some(duration)) = (&hooks.metrics, duration) {
                metrics.connection_closed(user_id, duration);
            }
            if let Some(hook) = &hooks.on_disconnect {
                hook.call(user_id).await;
            }
        }
        Ok(())
    }

    pub async fn handle_message(
        self: &Arc<Self>,
        event: Event,
        transport: Arc<dyn Transport>,
    ) -> Result<()> {
        match event.action.as_str() {
            "JOIN_CHANNEL" => self.handle_join(event, transport).await,
            "LEAVE_CHANNEL" => self.handle_leave(event, transport).await,
            "BROADCAST" => self.handle_broadcast(event, transport).await,
            _ => Ok(()),
        }
    }

    async fn handle_join(
        self: &Arc<Self>,
        event: Event,
        transport: Arc<dyn Transport>,
    ) -> Result<()> {
        for reg in self.channel_registrations.read().await.iter() {
            let Ok(route) = parse(&reg.pattern, &event.channel_name) else {
                continue;
            };
            let channel = reg.lobby.acquire_for_join(&event.channel_name).await?;
            if let Some(hooks) = &self.options.hooks
                && let Some(before) = &hooks.before_join
            {
                let user = User {
                    id: transport.id().to_owned(),
                    assigns: transport.clone_assigns().await,
                    presence: None,
                };
                before.call(&user, &event.channel_name).await;
            }
            let mut ctx =
                JoinContext::new(channel.clone(), event.clone(), transport.clone(), route);
            if let Err(err) = reg.handler.call(&mut ctx).await
                && !ctx.has_responded()
            {
                let _ = ctx.decline(err.code, err.message).await;
            }
            let mut outcome = Ok(());
            if !ctx.has_responded() {
                outcome = ctx.decline(401, "Join handler did not respond").await;
            }
            if ctx.is_accepted()
                && let Some(hooks) = &self.options.hooks
                && let Some(after) = &hooks.after_join
            {
                let user = User {
                    id: transport.id().to_owned(),
                    assigns: transport.clone_assigns().await,
                    presence: None,
                };
                after.call(&user, &event.channel_name).await;
            }
            reg.lobby.finish_join(channel).await;
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
            channel
                .remove_user(transport.id(), "explicit_leave")
                .await?;
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
        let Some(transport) = self.get_transport(user_id).await else {
            return Err(not_found(&self.path, "connection not found"));
        };
        transport.close().await
    }

    pub async fn close(&self) -> Result<()> {
        let lobbies = self
            .channel_registrations
            .read()
            .await
            .iter()
            .map(|reg| reg.lobby.clone())
            .collect::<Vec<_>>();
        for lobby in lobbies {
            lobby.close().await?;
        }
        let transports = self
            .connections
            .read()
            .await
            .values()
            .cloned()
            .collect::<Vec<_>>();
        for transport in transports {
            let _ = transport.close().await;
        }
        self.connections.write().await.clear();
        self.connect_times.write().await.clear();
        Ok(())
    }

    pub fn max_message_size(&self) -> usize {
        self.options.max_message_size
    }
}
