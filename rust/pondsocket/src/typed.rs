use std::future::Future;
use std::marker::PhantomData;
use std::pin::Pin;
use std::sync::Arc;

use async_trait::async_trait;
use pondsocket_common::{
    PondEvent, PondSchema, from_pond_map, from_pond_value, to_pond_map, to_pond_value,
};

use crate::channel::{Channel, EventHandler};
use crate::contexts::{EventContext, JoinContext};
use crate::endpoint::{Endpoint, JoinHandler};
use crate::errors::{Result, internal};
use crate::lobby::Lobby;
use crate::types::User;

pub type BoxHandlerFuture<'a> = Pin<Box<dyn Future<Output = Result<()>> + Send + 'a>>;

struct TypedJoinHandlerAdapter<S, H> {
    handler: H,
    _schema: PhantomData<S>,
}

struct TypedEventHandlerAdapter<S, E, H> {
    handler: H,
    _schema: PhantomData<S>,
    _event: PhantomData<E>,
}

#[async_trait]
impl<S, H> JoinHandler for TypedJoinHandlerAdapter<S, H>
where
    S: PondSchema + Send + Sync + 'static,
    H: for<'a> Fn(TypedJoinContext<'a, S>) -> BoxHandlerFuture<'a> + Send + Sync,
{
    async fn call(&self, ctx: &mut JoinContext) -> Result<()> {
        (self.handler)(TypedJoinContext::<S>::new(ctx)).await
    }
}

#[async_trait]
impl<S, E, H> EventHandler for TypedEventHandlerAdapter<S, E, H>
where
    S: PondSchema + Send + Sync + 'static,
    E: PondEvent + Send + Sync + 'static,
    H: for<'a> Fn(TypedEventContext<'a, S, E>) -> BoxHandlerFuture<'a> + Send + Sync,
{
    async fn call(&self, ctx: &mut EventContext) -> Result<()> {
        (self.handler)(TypedEventContext::<S, E>::new(ctx)).await
    }
}

#[derive(Clone)]
pub struct TypedChannel<S> {
    raw: Arc<Channel>,
    _schema: PhantomData<S>,
}

impl<S> TypedChannel<S>
where
    S: PondSchema,
{
    pub fn new(raw: Arc<Channel>) -> Self {
        Self {
            raw,
            _schema: PhantomData,
        }
    }

    pub fn raw(&self) -> &Arc<Channel> {
        &self.raw
    }

    pub fn name(&self) -> &str {
        &self.raw.name
    }

    pub async fn broadcast<E>(&self, payload: &E::Payload) -> Result<()>
    where
        E: PondEvent,
    {
        let value = to_pond_value(payload).map_err(|e| internal(&self.raw.name, e.to_string()))?;
        self.raw.broadcast(E::NAME, value).await
    }

    pub async fn broadcast_to<E>(&self, payload: &E::Payload, user_ids: &[String]) -> Result<()>
    where
        E: PondEvent,
    {
        let value = to_pond_value(payload).map_err(|e| internal(&self.raw.name, e.to_string()))?;
        self.raw.broadcast_to(E::NAME, value, user_ids).await
    }

    pub async fn broadcast_from<E>(&self, payload: &E::Payload, sender_id: &str) -> Result<()>
    where
        E: PondEvent,
    {
        let value = to_pond_value(payload).map_err(|e| internal(&self.raw.name, e.to_string()))?;
        self.raw.broadcast_from(E::NAME, value, sender_id).await
    }

    pub async fn track_presence(&self, user_id: &str, presence: &S::Presence) -> Result<()> {
        let value = to_pond_value(presence).map_err(|e| internal(&self.raw.name, e.to_string()))?;
        self.raw.track_presence(user_id, value).await
    }

    pub async fn update_presence(&self, user_id: &str, presence: &S::Presence) -> Result<()> {
        let value = to_pond_value(presence).map_err(|e| internal(&self.raw.name, e.to_string()))?;
        self.raw.update_presence(user_id, value).await
    }

    pub async fn get_presence(&self) -> Result<std::collections::HashMap<String, S::Presence>> {
        self.raw
            .get_presence()
            .await
            .into_iter()
            .map(|(id, presence)| {
                from_pond_map(presence)
                    .map(|presence| (id, presence))
                    .map_err(|e| internal(&self.raw.name, e.to_string()))
            })
            .collect()
    }

    pub async fn get_assigns(&self) -> Result<std::collections::HashMap<String, S::Assigns>> {
        self.raw
            .get_assigns()
            .await
            .into_iter()
            .map(|(id, assigns)| {
                from_pond_map(assigns)
                    .map(|assigns| (id, assigns))
                    .map_err(|e| internal(&self.raw.name, e.to_string()))
            })
            .collect()
    }
}

pub struct TypedJoinContext<'a, S>
where
    S: PondSchema,
{
    raw: &'a mut JoinContext,
    _schema: PhantomData<S>,
}

impl<'a, S> TypedJoinContext<'a, S>
where
    S: PondSchema,
{
    pub fn new(raw: &'a mut JoinContext) -> Self {
        Self {
            raw,
            _schema: PhantomData,
        }
    }

    pub fn raw(&mut self) -> &mut JoinContext {
        self.raw
    }

    pub fn user_id(&self) -> &str {
        self.raw.transport.id()
    }

    pub fn channel(&self) -> TypedChannel<S> {
        TypedChannel::new(self.raw.channel.clone())
    }

    pub fn join_params(&self) -> Result<S::JoinParams> {
        let payload = self
            .raw
            .event
            .payload
            .as_object()
            .cloned()
            .unwrap_or_default();
        from_pond_map(payload).map_err(|e| internal(&self.raw.channel.name, e.to_string()))
    }

    pub async fn accept(&mut self, assigns: &S::Assigns) -> Result<&mut Self> {
        let assigns =
            to_pond_map(assigns).map_err(|e| internal(&self.raw.channel.name, e.to_string()))?;
        self.raw.accept(assigns).await?;
        Ok(self)
    }

    pub async fn decline(&mut self, code: u16, message: impl Into<String>) -> Result<()> {
        self.raw.decline(code, message).await
    }

    pub async fn reply<E>(&self, payload: &E::Response) -> Result<()>
    where
        E: PondEvent,
    {
        let value =
            to_pond_value(payload).map_err(|e| internal(&self.raw.channel.name, e.to_string()))?;
        self.raw.reply(E::NAME, value).await
    }

    pub async fn track(&self, presence: &S::Presence) -> Result<()> {
        let value =
            to_pond_value(presence).map_err(|e| internal(&self.raw.channel.name, e.to_string()))?;
        self.raw.track(value).await
    }

    pub async fn broadcast<E>(&self, payload: &E::Payload) -> Result<()>
    where
        E: PondEvent,
    {
        let value =
            to_pond_value(payload).map_err(|e| internal(&self.raw.channel.name, e.to_string()))?;
        self.raw.broadcast(E::NAME, value).await
    }
}

pub struct TypedEventContext<'a, S, E>
where
    S: PondSchema,
    E: PondEvent,
{
    raw: &'a mut EventContext,
    _schema: PhantomData<S>,
    _event: PhantomData<E>,
}

impl<'a, S, E> TypedEventContext<'a, S, E>
where
    S: PondSchema,
    E: PondEvent,
{
    pub fn new(raw: &'a mut EventContext) -> Self {
        Self {
            raw,
            _schema: PhantomData,
            _event: PhantomData,
        }
    }

    pub fn raw(&mut self) -> &mut EventContext {
        self.raw
    }

    pub fn user(&self) -> &User {
        &self.raw.user
    }

    pub fn channel(&self) -> TypedChannel<S> {
        TypedChannel::new(self.raw.channel.clone())
    }

    pub fn payload(&self) -> Result<E::Payload> {
        from_pond_value(self.raw.event.payload.clone())
            .map_err(|e| internal(&self.raw.channel.name, e.to_string()))
    }

    pub fn assigns(&self) -> Result<S::Assigns> {
        from_pond_map(self.raw.user.assigns.clone())
            .map_err(|e| internal(&self.raw.channel.name, e.to_string()))
    }

    pub fn presence(&self) -> Result<Option<S::Presence>> {
        self.raw
            .user
            .presence
            .clone()
            .map(from_pond_map)
            .transpose()
            .map_err(|e| internal(&self.raw.channel.name, e.to_string()))
    }

    pub async fn reply<R>(&mut self, payload: &R::Response) -> Result<()>
    where
        R: PondEvent,
    {
        let value =
            to_pond_value(payload).map_err(|e| internal(&self.raw.channel.name, e.to_string()))?;
        self.raw.reply(R::NAME, value).await
    }

    pub async fn broadcast<R>(&self, payload: &R::Payload) -> Result<()>
    where
        R: PondEvent,
    {
        let value =
            to_pond_value(payload).map_err(|e| internal(&self.raw.channel.name, e.to_string()))?;
        self.raw.broadcast(R::NAME, value).await
    }

    pub async fn track(&self, presence: &S::Presence) -> Result<()> {
        let value =
            to_pond_value(presence).map_err(|e| internal(&self.raw.channel.name, e.to_string()))?;
        self.raw.track(value).await
    }

    pub async fn update_presence(&self, presence: &S::Presence) -> Result<()> {
        let value =
            to_pond_value(presence).map_err(|e| internal(&self.raw.channel.name, e.to_string()))?;
        self.raw.update_presence(value).await
    }
}

#[derive(Clone)]
pub struct TypedLobby<S> {
    raw: Arc<Lobby>,
    _schema: PhantomData<S>,
}

impl<S> TypedLobby<S>
where
    S: PondSchema + Send + Sync + 'static,
{
    pub fn new(raw: Arc<Lobby>) -> Self {
        Self {
            raw,
            _schema: PhantomData,
        }
    }

    pub fn raw(&self) -> &Arc<Lobby> {
        &self.raw
    }

    pub async fn get_channel(&self, name: &str) -> Option<TypedChannel<S>> {
        self.raw.get_channel(name).await.map(TypedChannel::new)
    }

    pub async fn on<E, H>(&self, handler: H)
    where
        E: PondEvent + Send + Sync + 'static,
        H: for<'a> Fn(TypedEventContext<'a, S, E>) -> BoxHandlerFuture<'a> + Send + Sync + 'static,
    {
        self.raw
            .on_message(
                E::NAME,
                TypedEventHandlerAdapter::<S, E, H> {
                    handler,
                    _schema: PhantomData,
                    _event: PhantomData,
                },
            )
            .await;
    }
}

impl Endpoint {
    pub async fn create_typed_channel<S, H>(
        self: &Arc<Self>,
        pattern: impl Into<String>,
        handler: H,
    ) -> TypedLobby<S>
    where
        S: PondSchema + Send + Sync + 'static,
        H: for<'a> Fn(TypedJoinContext<'a, S>) -> BoxHandlerFuture<'a> + Send + Sync + 'static,
    {
        let lobby = self
            .create_channel(
                pattern,
                TypedJoinHandlerAdapter::<S, H> {
                    handler,
                    _schema: PhantomData,
                },
            )
            .await;
        TypedLobby::new(lobby)
    }
}
