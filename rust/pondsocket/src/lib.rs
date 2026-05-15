pub mod channel;
pub mod contexts;
pub mod distributed;
pub mod endpoint;
pub mod errors;
pub mod hooks;
pub mod lobby;
pub mod parser;
pub mod pubsub;
pub mod server;
pub mod transport;
pub mod types;
pub mod wire;

pub use channel::Channel;
pub use contexts::{
    ConnectionContext, ConnectionDecision, EventContext, JoinContext, LeaveContext, OutgoingContext,
};
pub use endpoint::{ConnectionHandler, Endpoint, JoinHandler};
pub use errors::{PondError, Result};
pub use lobby::Lobby;
pub use pubsub::{LocalPubSub, PubSub, format_topic, match_topic};
pub use server::{EndpointMatch, PondSocket};
pub use transport::{MemoryTransport, Transport};
pub use types::{Event, Options, Route, TransportType, User};

#[cfg(test)]
mod integration_tests {
    use super::*;
    use async_trait::async_trait;
    use serde_json::json;
    use std::sync::Arc;
    use tokio::time::{Duration, timeout};

    struct AcceptConn;

    #[async_trait]
    impl ConnectionHandler for AcceptConn {
        async fn call(&self, ctx: &mut ConnectionContext) -> Result<()> {
            ctx.accept(pondsocket_common::PondAssigns::new())?;
            Ok(())
        }
    }

    struct AcceptJoin;

    #[async_trait]
    impl JoinHandler for AcceptJoin {
        async fn call(&self, ctx: &mut JoinContext) -> Result<()> {
            ctx.accept(pondsocket_common::PondAssigns::new()).await?;
            ctx.track(json!({ "userId": ctx.transport.id(), "status": "online" }))
                .await?;
            Ok(())
        }
    }

    struct Echo;

    #[async_trait]
    impl crate::channel::EventHandler for Echo {
        async fn call(&self, ctx: &mut EventContext) -> Result<()> {
            ctx.broadcast(&ctx.event.event, ctx.event.payload.clone())
                .await
        }
    }

    #[tokio::test]
    async fn joins_tracks_presence_and_broadcasts() {
        let pond = PondSocket::with_defaults();
        let endpoint = pond.create_endpoint("/", AcceptConn).await;
        let lobby = endpoint.create_channel("/chat/:room", AcceptJoin).await;
        lobby.on_message("message", Echo).await;

        let transport = Arc::new(MemoryTransport::new(
            "u1",
            pondsocket_common::PondAssigns::new(),
        ));
        let ctx = endpoint
            .request_connection(Default::default(), Route::default(), Some("u1".to_owned()))
            .await;
        assert!(ctx.is_accepted());
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();

        assert_eq!(transport.recv().await.unwrap().event, "CONNECTION");
        endpoint
            .handle_message(
                Event::new(
                    "JOIN_CHANNEL",
                    "/chat/1",
                    "join-1",
                    "JOIN_CHANNEL",
                    json!({}),
                ),
                transport.clone(),
            )
            .await
            .unwrap();
        assert_eq!(transport.recv().await.unwrap().event, "ACKNOWLEDGE");
        let presence = transport.recv().await.unwrap();
        assert_eq!(presence.action, "PRESENCE");
        assert_eq!(presence.event, "JOIN");

        endpoint
            .handle_message(
                Event::new(
                    "BROADCAST",
                    "/chat/1",
                    "msg-1",
                    "message",
                    json!({ "text": "hi" }),
                ),
                transport.clone(),
            )
            .await
            .unwrap();
        let broadcast = timeout(Duration::from_secs(1), transport.recv())
            .await
            .unwrap()
            .unwrap();
        assert_eq!(broadcast.action, "BROADCAST");
        assert_eq!(broadcast.event, "message");
        assert_eq!(broadcast.payload["text"], "hi");
    }

    #[tokio::test]
    async fn local_pubsub_delivers_broadcasts_across_nodes() {
        let pubsub = Arc::new(LocalPubSub::new(100));
        let mut opts_a = Options::default();
        opts_a.node_id = "node-a".to_owned();
        let mut opts_b = Options::default();
        opts_b.node_id = "node-b".to_owned();
        let pond_a = PondSocket::new(opts_a, Some(pubsub.clone()));
        let pond_b = PondSocket::new(opts_b, Some(pubsub));
        let endpoint_a = pond_a.create_endpoint("/", AcceptConn).await;
        let endpoint_b = pond_b.create_endpoint("/", AcceptConn).await;
        let lobby_a = endpoint_a.create_channel("/chat/:room", AcceptJoin).await;
        let _lobby_b = endpoint_b.create_channel("/chat/:room", AcceptJoin).await;
        lobby_a.on_message("message", Echo).await;

        let ta = Arc::new(MemoryTransport::new(
            "a",
            pondsocket_common::PondAssigns::new(),
        ));
        let tb = Arc::new(MemoryTransport::new(
            "b",
            pondsocket_common::PondAssigns::new(),
        ));
        endpoint_a.register_transport(ta.clone()).await.unwrap();
        endpoint_b.register_transport(tb.clone()).await.unwrap();
        let _ = ta.recv().await;
        let _ = tb.recv().await;

        endpoint_a
            .handle_message(
                Event::new("JOIN_CHANNEL", "/chat/1", "ja", "JOIN_CHANNEL", json!({})),
                ta.clone(),
            )
            .await
            .unwrap();
        endpoint_b
            .handle_message(
                Event::new("JOIN_CHANNEL", "/chat/1", "jb", "JOIN_CHANNEL", json!({})),
                tb.clone(),
            )
            .await
            .unwrap();
        let _ = ta.recv().await;
        let _ = ta.recv().await;
        let _ = tb.recv().await;
        let _ = tb.recv().await;

        endpoint_a
            .handle_message(
                Event::new(
                    "BROADCAST",
                    "/chat/1",
                    "m1",
                    "message",
                    json!({ "text": "distributed" }),
                ),
                ta,
            )
            .await
            .unwrap();
        let remote = timeout(Duration::from_secs(1), tb.recv())
            .await
            .unwrap()
            .unwrap();
        assert_eq!(remote.action, "BROADCAST");
        assert_eq!(remote.payload["text"], "distributed");
    }
}
