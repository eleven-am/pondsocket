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
pub mod typed;
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
pub use typed::{BoxHandlerFuture, TypedChannel, TypedEventContext, TypedJoinContext, TypedLobby};
pub use types::{Event, Options, Route, TransportType, User};

#[cfg(test)]
mod integration_tests {
    use super::*;
    use async_trait::async_trait;
    use pondsocket_common::{PondEvent, PondSchema};
    use serde::{Deserialize, Serialize};
    use serde_json::json;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};
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

    #[tokio::test]
    async fn duplicate_connection_ids_are_rejected() {
        let pond = PondSocket::with_defaults();
        let endpoint = pond.create_endpoint("/", AcceptConn).await;
        let first = Arc::new(MemoryTransport::new(
            "same-id",
            pondsocket_common::PondAssigns::new(),
        ));
        let second = Arc::new(MemoryTransport::new(
            "same-id",
            pondsocket_common::PondAssigns::new(),
        ));

        endpoint.register_transport(first.clone()).await.unwrap();
        let err = endpoint.register_transport(second).await.unwrap_err();

        assert_eq!(err.code, 409);
        assert!(Arc::ptr_eq(
            &endpoint.get_transport("same-id").await.unwrap(),
            &(first as Arc<dyn Transport>)
        ));
    }

    #[tokio::test]
    async fn dispatch_concurrency_zero_is_clamped() {
        let opts = Options {
            dispatch_concurrency: 0,
            ..Options::default()
        };
        let pond = PondSocket::new(opts, None);
        let endpoint = pond.create_endpoint("/", AcceptConn).await;
        let lobby = endpoint.create_channel("/chat/:room", AcceptJoin).await;
        lobby.on_message("message", Echo).await;
        let transport = Arc::new(MemoryTransport::new(
            "u1",
            pondsocket_common::PondAssigns::new(),
        ));
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();
        let _ = transport.recv().await;

        endpoint
            .handle_message(
                Event::new("JOIN_CHANNEL", "/chat/1", "join", "JOIN_CHANNEL", json!({})),
                transport.clone(),
            )
            .await
            .unwrap();
        let _ = transport.recv().await;
        let _ = transport.recv().await;
        endpoint
            .handle_message(
                Event::new(
                    "BROADCAST",
                    "/chat/1",
                    "msg",
                    "message",
                    json!({"ok": true}),
                ),
                transport.clone(),
            )
            .await
            .unwrap();

        let broadcast = timeout(Duration::from_secs(1), transport.recv())
            .await
            .unwrap()
            .unwrap();
        assert_eq!(broadcast.payload["ok"], true);
    }

    #[tokio::test]
    async fn channel_dispatch_task_does_not_retain_after_close() {
        let channel = crate::channel::Channel::new(crate::channel::ChannelConfig {
            name: "/chat/1".to_owned(),
            endpoint_path: "/".to_owned(),
            options: Options::default(),
            pubsub: None,
            event_handlers: Vec::new(),
            outgoing_handlers: Vec::new(),
            leave_handler: None,
        });
        let weak = Arc::downgrade(&channel);
        channel.start().await.unwrap();
        channel.close().await.unwrap();
        drop(channel);
        tokio::task::yield_now().await;

        assert!(weak.upgrade().is_none());
    }

    struct CountingPubSub {
        subscribe_count: AtomicUsize,
    }

    #[async_trait]
    impl PubSub for CountingPubSub {
        async fn subscribe(&self, _pattern: &str, _handler: pubsub::PubSubHandler) -> Result<()> {
            self.subscribe_count.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }

        async fn unsubscribe(&self, _pattern: &str) -> Result<()> {
            Ok(())
        }

        async fn publish(&self, _topic: &str, _data: Vec<u8>) -> Result<()> {
            Ok(())
        }

        async fn close(&self) -> Result<()> {
            Ok(())
        }
    }

    #[tokio::test]
    async fn concurrent_lobby_first_join_reuses_single_channel() {
        let pubsub = Arc::new(CountingPubSub {
            subscribe_count: AtomicUsize::new(0),
        });
        let lobby = Lobby::new("/chat/:room", "/", Options::default(), Some(pubsub.clone()));

        let (a, b) = tokio::join!(
            lobby.get_or_create_channel("/chat/1"),
            lobby.get_or_create_channel("/chat/1")
        );
        let a = a.unwrap();
        let b = b.unwrap();

        assert!(Arc::ptr_eq(&a, &b));
        assert_eq!(pubsub.subscribe_count.load(Ordering::SeqCst), 2);
    }

    #[tokio::test]
    async fn distributed_assign_updates_round_trip_key_value() {
        let pubsub = Arc::new(LocalPubSub::new(100));
        let opts_a = Options {
            node_id: "node-a".to_owned(),
            ..Options::default()
        };
        let opts_b = Options {
            node_id: "node-b".to_owned(),
            ..Options::default()
        };
        let pond_a = PondSocket::new(opts_a, Some(pubsub.clone()));
        let pond_b = PondSocket::new(opts_b, Some(pubsub));
        let endpoint_a = pond_a.create_endpoint("/", AcceptConn).await;
        let endpoint_b = pond_b.create_endpoint("/", AcceptConn).await;
        let lobby_a = endpoint_a.create_channel("/chat/:room", AcceptJoin).await;
        let lobby_b = endpoint_b.create_channel("/chat/:room", AcceptJoin).await;

        let tb = Arc::new(MemoryTransport::new(
            "b",
            pondsocket_common::PondAssigns::new(),
        ));
        endpoint_b.register_transport(tb.clone()).await.unwrap();
        let _ = tb.recv().await;
        endpoint_b
            .handle_message(
                Event::new("JOIN_CHANNEL", "/chat/1", "jb", "JOIN_CHANNEL", json!({})),
                tb,
            )
            .await
            .unwrap();
        let _ = lobby_a.get_or_create_channel("/chat/1").await.unwrap();
        let channel_b = lobby_b.get_channel("/chat/1").await.unwrap();
        let _ = channel_b
            .get_assigns()
            .await
            .get("b")
            .expect("user b joined");

        let channel_a = lobby_a.get_channel("/chat/1").await.unwrap();
        let err = channel_a
            .update_assign("b", "role", json!("mod"))
            .await
            .unwrap_err();
        assert_eq!(err.code, 404);

        timeout(Duration::from_secs(1), async {
            loop {
                if channel_b
                    .get_assigns()
                    .await
                    .get("b")
                    .and_then(|assigns| assigns.get("role"))
                    == Some(&json!("mod"))
                {
                    break;
                }
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
    }

    #[tokio::test]
    async fn two_node_state_sync_assigns_and_stale_cleanup() {
        let pubsub = Arc::new(LocalPubSub::new(200));
        let opts_a = Options {
            node_id: "node-a".to_owned(),
            heartbeat_interval: Duration::from_millis(50),
            heartbeat_timeout: Duration::from_millis(300),
            ..Options::default()
        };
        let opts_b = Options {
            node_id: "node-b".to_owned(),
            heartbeat_interval: Duration::from_millis(50),
            heartbeat_timeout: Duration::from_millis(300),
            ..Options::default()
        };
        let pond_a = PondSocket::new(opts_a, Some(pubsub.clone()));
        let pond_b = PondSocket::new(opts_b, Some(pubsub));
        let endpoint_a = pond_a.create_endpoint("/", AcceptConn).await;
        let endpoint_b = pond_b.create_endpoint("/", AcceptConn).await;
        let lobby_a = endpoint_a.create_channel("/chat/:room", AcceptJoin).await;
        let lobby_b = endpoint_b.create_channel("/chat/:room", AcceptJoin).await;

        let tb = Arc::new(MemoryTransport::new(
            "b",
            pondsocket_common::PondAssigns::new(),
        ));
        endpoint_b.register_transport(tb.clone()).await.unwrap();
        let _ = tb.recv().await;
        endpoint_b
            .handle_message(
                Event::new("JOIN_CHANNEL", "/chat/1", "jb", "JOIN_CHANNEL", json!({})),
                tb.clone(),
            )
            .await
            .unwrap();
        let channel_b = lobby_b.get_channel("/chat/1").await.unwrap();

        let channel_a = lobby_a.get_or_create_channel("/chat/1").await.unwrap();

        timeout(Duration::from_secs(3), async {
            loop {
                let assigns = channel_a.get_assigns().await;
                let presence = channel_a.get_presence().await;
                if assigns.contains_key("b") && presence.contains_key("b") {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .expect("node A should learn user b via STATE_REQUEST/STATE_RESPONSE");

        channel_b
            .update_assign("b", "role", json!("admin"))
            .await
            .unwrap();
        timeout(Duration::from_secs(3), async {
            loop {
                if channel_a
                    .get_assigns()
                    .await
                    .get("b")
                    .and_then(|assigns| assigns.get("role"))
                    == Some(&json!("admin"))
                {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .expect("assigns update should propagate to node A");

        endpoint_b.close().await.unwrap();

        timeout(Duration::from_secs(5), async {
            loop {
                let assigns = channel_a.get_assigns().await;
                let presence = channel_a.get_presence().await;
                if !assigns.contains_key("b") && !presence.contains_key("b") {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .expect("stale node B users should be evicted from node A after heartbeat timeout");
    }

    #[tokio::test]
    async fn distributed_presence_propagates_across_nodes() {
        let pubsub = Arc::new(LocalPubSub::new(200));
        let opts_a = Options {
            node_id: "node-a".to_owned(),
            ..Options::default()
        };
        let opts_b = Options {
            node_id: "node-b".to_owned(),
            ..Options::default()
        };
        let pond_a = PondSocket::new(opts_a, Some(pubsub.clone()));
        let pond_b = PondSocket::new(opts_b, Some(pubsub));
        let endpoint_a = pond_a.create_endpoint("/", AcceptConn).await;
        let endpoint_b = pond_b.create_endpoint("/", AcceptConn).await;
        let lobby_a = endpoint_a.create_channel("/chat/:room", AcceptJoin).await;
        let _lobby_b = endpoint_b.create_channel("/chat/:room", AcceptJoin).await;

        let ta = Arc::new(MemoryTransport::new(
            "a",
            pondsocket_common::PondAssigns::new(),
        ));
        endpoint_a.register_transport(ta.clone()).await.unwrap();
        let _ = ta.recv().await;
        endpoint_a
            .handle_message(
                Event::new("JOIN_CHANNEL", "/chat/1", "ja", "JOIN_CHANNEL", json!({})),
                ta.clone(),
            )
            .await
            .unwrap();
        let channel_a = lobby_a.get_channel("/chat/1").await.unwrap();

        let tb = Arc::new(MemoryTransport::new(
            "b",
            pondsocket_common::PondAssigns::new(),
        ));
        endpoint_b.register_transport(tb.clone()).await.unwrap();
        let _ = tb.recv().await;
        endpoint_b
            .handle_message(
                Event::new("JOIN_CHANNEL", "/chat/1", "jb", "JOIN_CHANNEL", json!({})),
                tb.clone(),
            )
            .await
            .unwrap();

        timeout(Duration::from_secs(3), async {
            loop {
                if channel_a.get_presence().await.contains_key("b") {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .expect("node A should receive node B's presence update");
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct ChatPayload {
        text: String,
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct AckPayload {
        ok: bool,
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct Presence {
        user_id: String,
        status: String,
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct Assigns {
        role: String,
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct JoinParams {
        token: String,
    }

    struct Chat;
    struct ChatSchema;

    impl PondEvent for Chat {
        type Payload = ChatPayload;
        type Response = AckPayload;

        const NAME: &'static str = "chat";
    }

    impl PondSchema for ChatSchema {
        type Presence = Presence;
        type Assigns = Assigns;
        type JoinParams = JoinParams;
    }

    #[tokio::test]
    async fn typed_channel_api_wraps_dynamic_runtime() {
        let pond = PondSocket::with_defaults();
        let endpoint = pond.create_endpoint("/", AcceptConn).await;
        let lobby = endpoint
            .create_typed_channel::<ChatSchema, _>("/chat/:room", |mut ctx| {
                Box::pin(async move {
                    let params = ctx.join_params()?;
                    assert_eq!(params.token, "secret");
                    ctx.accept(&Assigns {
                        role: "member".to_owned(),
                    })
                    .await?;
                    ctx.track(&Presence {
                        user_id: ctx.user_id().to_owned(),
                        status: "online".to_owned(),
                    })
                    .await
                })
            })
            .await;
        lobby
            .on::<Chat, _>(|mut ctx| {
                Box::pin(async move {
                    let payload = ctx.payload()?;
                    assert_eq!(payload.text, "hello");
                    ctx.reply::<Chat>(&AckPayload { ok: true }).await
                })
            })
            .await;

        let transport = Arc::new(MemoryTransport::new(
            "typed-user",
            pondsocket_common::PondAssigns::new(),
        ));
        endpoint
            .register_transport(transport.clone())
            .await
            .unwrap();
        let _ = transport.recv().await;
        endpoint
            .handle_message(
                Event::new(
                    "JOIN_CHANNEL",
                    "/chat/typed",
                    "join-typed",
                    "JOIN_CHANNEL",
                    json!({ "token": "secret" }),
                ),
                transport.clone(),
            )
            .await
            .unwrap();
        assert_eq!(transport.recv().await.unwrap().event, "ACKNOWLEDGE");
        assert_eq!(transport.recv().await.unwrap().event, "JOIN");

        endpoint
            .handle_message(
                Event::new(
                    "BROADCAST",
                    "/chat/typed",
                    "msg-typed",
                    "chat",
                    json!({ "text": "hello" }),
                ),
                transport.clone(),
            )
            .await
            .unwrap();
        let reply = transport.recv().await.unwrap();
        assert_eq!(reply.event, "chat");
        assert_eq!(reply.payload["ok"], true);

        let channel = lobby.get_channel("/chat/typed").await.unwrap();
        let presence = channel.get_presence().await.unwrap();
        assert_eq!(presence["typed-user"].status, "online");
    }
}
