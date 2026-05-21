use std::sync::Arc;

use pondsocket_common::PondAssigns;
use serde_json::Value;

use crate::channel::Channel;
use crate::errors::{Result, bad_request};
use crate::transport::Transport;
use crate::types::{Event, Route};

pub struct JoinContext {
    pub channel: Arc<Channel>,
    pub event: Event,
    pub transport: Arc<dyn Transport>,
    pub route: Route,
    responded: bool,
    accepted: bool,
}

impl JoinContext {
    pub fn new(
        channel: Arc<Channel>,
        event: Event,
        transport: Arc<dyn Transport>,
        route: Route,
    ) -> Self {
        Self {
            channel,
            event,
            transport,
            route,
            responded: false,
            accepted: false,
        }
    }

    pub fn has_responded(&self) -> bool {
        self.responded
    }

    pub fn is_accepted(&self) -> bool {
        self.accepted
    }

    pub async fn accept(&mut self, assigns: PondAssigns) -> Result<&mut Self> {
        self.ensure_not_responded()?;
        for (key, value) in assigns {
            self.transport.set_assign(&key, value).await;
        }
        self.responded = true;
        self.accepted = true;
        self.channel.add_user(self.transport.clone()).await?;
        self.channel
            .send_acknowledge(&self.event.request_id, self.transport.id())
            .await?;
        Ok(self)
    }

    pub async fn decline(&mut self, code: u16, message: impl Into<String>) -> Result<()> {
        self.ensure_not_responded()?;
        self.responded = true;
        self.accepted = false;
        self.channel
            .send_unauthorized(
                &self.event.request_id,
                self.transport.id(),
                code,
                &message.into(),
            )
            .await
    }

    pub async fn reply(&self, event: &str, payload: Value) -> Result<()> {
        self.ensure_accepted()?;
        self.channel
            .send_system(
                event,
                payload,
                &self.event.request_id,
                &[self.transport.id().to_owned()],
            )
            .await
    }

    pub async fn track(&self, presence: Value) -> Result<()> {
        self.ensure_accepted()?;
        self.channel
            .track_presence(self.transport.id(), presence)
            .await
    }

    pub async fn broadcast(&self, event: &str, payload: Value) -> Result<()> {
        self.ensure_accepted()?;
        self.channel.broadcast(event, payload).await
    }

    pub async fn set_assign(&self, key: &str, value: Value) -> Result<()> {
        if self.accepted {
            self.channel
                .update_assign(self.transport.id(), key, value)
                .await
        } else {
            self.transport.set_assign(key, value).await;
            Ok(())
        }
    }

    fn ensure_not_responded(&self) -> Result<()> {
        if self.responded {
            return Err(bad_request(
                &self.channel.name,
                "already responded to join request",
            ));
        }
        Ok(())
    }

    fn ensure_accepted(&self) -> Result<()> {
        if !self.accepted {
            return Err(bad_request(
                &self.channel.name,
                "join request has not been accepted",
            ));
        }
        Ok(())
    }
}
