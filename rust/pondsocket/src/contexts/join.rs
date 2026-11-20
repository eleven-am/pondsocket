use std::sync::Arc;

use pondsocket_common::PondAssigns;
use serde_json::{Value, json};

use crate::channel::Channel;
use crate::errors::{Result, bad_request};
use crate::transport::Transport;
use crate::types::{Event, Route};

struct JoinRollback {
    channel: Arc<Channel>,
    user_id: String,
    armed: bool,
}

impl JoinRollback {
    fn disarm(&mut self) {
        self.armed = false;
    }
}

impl Drop for JoinRollback {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }
        let channel = self.channel.clone();
        let user_id = self.user_id.clone();
        if let Ok(handle) = tokio::runtime::Handle::try_current() {
            handle.spawn(async move {
                let _ = channel.remove_user(&user_id, "join_cancelled").await;
            });
        }
    }
}

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
        self.channel.add_user(self.transport.clone()).await?;
        let mut rollback = JoinRollback {
            channel: self.channel.clone(),
            user_id: self.transport.id().to_owned(),
            armed: true,
        };
        if let Err(error) = self
            .channel
            .send_acknowledge(&self.event.request_id, self.transport.id())
            .await
        {
            let _ = self
                .channel
                .remove_user(self.transport.id(), "join_ack_failed")
                .await;
            return Err(error);
        }
        rollback.disarm();
        self.responded = true;
        self.accepted = true;
        Ok(self)
    }

    pub async fn decline(&mut self, code: u16, message: impl Into<String>) -> Result<()> {
        self.ensure_not_responded()?;
        self.responded = true;
        self.accepted = false;
        self.transport
            .send_event(Event::new(
                "SYSTEM",
                &self.channel.name,
                &self.event.request_id,
                "UNAUTHORIZED",
                json!({ "code": code, "message": message.into() }),
            ))
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::errors::unavailable;
    use crate::transport::Transport;
    use crate::types::{Event, Options, Route, TransportType};
    use async_trait::async_trait;
    use pondsocket_common::PondAssigns;

    struct FailingTransport {
        id: String,
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
    async fn adv_failed_join_ack_rolls_back_membership() {
        let lobby = crate::lobby::Lobby::new("/room", "/", Options::default(), None);
        let channel = lobby.get_or_create_channel("/room").await.unwrap();
        let transport = Arc::new(FailingTransport {
            id: "u1".to_owned(),
        });
        let event = Event::new(
            "JOIN_CHANNEL",
            "/room",
            "request",
            "JOIN_CHANNEL",
            json!({}),
        );
        let mut context = JoinContext::new(channel.clone(), event, transport, Route::default());

        assert!(context.accept(PondAssigns::new()).await.is_err());
        assert!(!context.has_responded());
        assert!(!context.is_accepted());
        assert_eq!(channel.user_count().await, 0);
    }
}
