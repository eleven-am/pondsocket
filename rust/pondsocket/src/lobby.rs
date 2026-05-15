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

    pub async fn get_or_create_channel(&self, name: &str) -> Result<Arc<Channel>> {
        if let Some(channel) = self.get_channel(name).await {
            return Ok(channel);
        }
        let channel = Channel::new(ChannelConfig {
            name: name.to_owned(),
            endpoint_path: self.endpoint_path.clone(),
            options: self.options.clone(),
            pubsub: self.pubsub.clone(),
            event_handlers: self.event_handlers.read().await.clone(),
            outgoing_handlers: self.outgoing_handlers.read().await.clone(),
            leave_handler: self.leave_handler.read().await.clone(),
        });
        channel.start().await?;
        let mut channels = self.channels.write().await;
        Ok(channels.entry(name.to_owned()).or_insert(channel).clone())
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
