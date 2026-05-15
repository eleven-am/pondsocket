use std::sync::Arc;

use serde_json::Value;

use crate::channel::Channel;
use crate::errors::Result;
use crate::types::{Event, Route, User};

pub struct EventContext {
    pub channel: Arc<Channel>,
    pub event: Event,
    pub user: User,
    pub route: Route,
    replied: bool,
}

impl EventContext {
    pub fn new(channel: Arc<Channel>, event: Event, user: User, route: Route) -> Self {
        Self {
            channel,
            event,
            user,
            route,
            replied: false,
        }
    }

    pub async fn reply(&mut self, event: &str, payload: Value) -> Result<()> {
        if self.replied {
            return Ok(());
        }
        self.replied = true;
        self.channel
            .send_system(
                event,
                payload,
                &self.event.request_id,
                std::slice::from_ref(&self.user.id),
            )
            .await
    }

    pub async fn broadcast(&self, event: &str, payload: Value) -> Result<()> {
        self.channel.broadcast(event, payload).await
    }

    pub async fn broadcast_to(
        &self,
        event: &str,
        payload: Value,
        user_ids: &[String],
    ) -> Result<()> {
        self.channel.broadcast_to(event, payload, user_ids).await
    }

    pub async fn broadcast_from(&self, event: &str, payload: Value) -> Result<()> {
        self.channel
            .broadcast_from(event, payload, &self.user.id)
            .await
    }

    pub async fn track(&self, presence: Value) -> Result<()> {
        self.channel.track_presence(&self.user.id, presence).await
    }

    pub async fn update_presence(&self, presence: Value) -> Result<()> {
        self.channel.update_presence(&self.user.id, presence).await
    }

    pub async fn untrack(&self) -> Result<()> {
        self.channel.untrack_presence(&self.user.id).await
    }

    pub async fn evict(&self, reason: &str, user_ids: &[String]) -> Result<()> {
        let targets = if user_ids.is_empty() {
            vec![self.user.id.clone()]
        } else {
            user_ids.to_vec()
        };
        for user_id in targets {
            self.channel.evict_user(&user_id, reason).await?;
        }
        Ok(())
    }
}
