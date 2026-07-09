//! Axum adapter for PondSocket.

use async_trait::async_trait;
use axum::extract::ws::{Message, WebSocket};
use futures_util::{SinkExt, StreamExt};
use pondsocket::contexts::IncomingConnection;
use pondsocket::errors::{Result, internal};
use pondsocket::transport::Transport;
use pondsocket::types::{Event, TransportType};
use pondsocket::wire::{event_to_json, parse_inbound_text};
use pondsocket::{Endpoint, PondSocket};
use pondsocket_common::{PondAssigns, uuid};
use serde_json::{Value, json};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::{RwLock, mpsc};

pub struct AxumWebSocketTransport {
    id: String,
    assigns: RwLock<PondAssigns>,
    active: RwLock<bool>,
    tx: mpsc::Sender<Message>,
}

impl AxumWebSocketTransport {
    pub fn new(id: impl Into<String>, assigns: PondAssigns, tx: mpsc::Sender<Message>) -> Self {
        Self {
            id: id.into(),
            assigns: RwLock::new(assigns),
            active: RwLock::new(true),
            tx,
        }
    }
}

#[async_trait]
impl Transport for AxumWebSocketTransport {
    fn id(&self) -> &str {
        &self.id
    }

    async fn send_event(&self, event: Event) -> Result<()> {
        let text = event_to_json(&event).map_err(|e| internal("", e.to_string()))?;
        self.tx
            .send(Message::Text(text.into()))
            .await
            .map_err(|_| internal("", "websocket writer closed"))
    }

    async fn close(&self) -> Result<()> {
        *self.active.write().await = false;
        let _ = self.tx.send(Message::Close(None)).await;
        Ok(())
    }

    fn transport_type(&self) -> TransportType {
        TransportType::WebSocket
    }

    async fn is_active(&self) -> bool {
        *self.active.read().await
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

#[derive(Debug, Clone, Default)]
pub struct RequestParts {
    pub path: String,
    pub headers: HashMap<String, String>,
    pub cookies: HashMap<String, String>,
    pub query: HashMap<String, String>,
    pub address: String,
}

pub async fn handle_socket(pond: Arc<PondSocket>, socket: WebSocket, request: RequestParts) {
    let Some(matched) = pond.match_endpoint(&request.path).await else {
        let mut socket = socket;
        let _ = socket.send(Message::Close(None)).await;
        return;
    };
    let endpoint = matched.endpoint;
    let incoming = IncomingConnection {
        id: String::new(),
        headers: request.headers,
        cookies: request.cookies,
        query: request.query,
        params: matched.route.params.clone(),
        address: request.address,
    };
    let ctx = endpoint
        .request_connection(incoming, matched.route, None)
        .await;
    if ctx.is_declined() {
        let mut socket = socket;
        let _ = socket.send(Message::Close(None)).await;
        return;
    }

    let (mut sender, mut receiver) = socket.split();
    let (tx, mut rx) = mpsc::channel::<Message>(1024);
    let transport = Arc::new(AxumWebSocketTransport::new(
        ctx.user_id.clone(),
        ctx.assigns(),
        tx,
    ));
    if endpoint
        .register_transport(transport.clone())
        .await
        .is_err()
    {
        let _ = sender.send(Message::Close(None)).await;
        return;
    }
    send_pending_reply(&ctx, transport.clone()).await;

    let writer = tokio::spawn(async move {
        while let Some(message) = rx.recv().await {
            if sender.send(message).await.is_err() {
                break;
            }
        }
    });

    read_loop(endpoint.clone(), transport.clone(), &mut receiver).await;
    let _ = endpoint.unregister_transport(transport.id()).await;
    let _ = transport.close().await;
    writer.abort();
}

enum Inbound {
    Dispatch(Event),
    Feedback { event: Event, close: bool },
}

fn gateway_error_event(message: impl Into<String>) -> Event {
    Event::new(
        "SYSTEM",
        "GATEWAY",
        uuid(),
        "INTERNAL_ERROR",
        json!({ "message": message.into() }),
    )
}

fn classify_text(text: &str, max_size: usize) -> Inbound {
    if text.len() > max_size {
        return Inbound::Feedback {
            event: gateway_error_event("message exceeds maximum size"),
            close: true,
        };
    }
    match parse_inbound_text(text) {
        Ok(event) => Inbound::Dispatch(event),
        Err(err) => Inbound::Feedback {
            event: gateway_error_event(err.to_string()),
            close: false,
        },
    }
}

async fn read_loop(
    endpoint: Arc<Endpoint>,
    transport: Arc<AxumWebSocketTransport>,
    receiver: &mut futures_util::stream::SplitStream<WebSocket>,
) {
    while let Some(next) = receiver.next().await {
        let Ok(message) = next else {
            break;
        };
        let outcome = match message {
            Message::Text(text) => classify_text(&text, endpoint.max_message_size()),
            Message::Binary(bytes) => {
                if bytes.len() > endpoint.max_message_size() {
                    Inbound::Feedback {
                        event: gateway_error_event("message exceeds maximum size"),
                        close: true,
                    }
                } else {
                    match String::from_utf8(bytes.to_vec()) {
                        Ok(text) => classify_text(&text, endpoint.max_message_size()),
                        Err(_) => Inbound::Feedback {
                            event: gateway_error_event("message is not valid UTF-8 text"),
                            close: false,
                        },
                    }
                }
            }
            Message::Close(_) => break,
            _ => continue,
        };
        match outcome {
            Inbound::Dispatch(event) => {
                let _ = endpoint.handle_message(event, transport.clone()).await;
            }
            Inbound::Feedback { event, close } => {
                let _ = transport.send_event(event).await;
                if close {
                    let _ = transport.close().await;
                    break;
                }
            }
        }
    }
}

async fn send_pending_reply(
    ctx: &pondsocket::ConnectionContext,
    transport: Arc<AxumWebSocketTransport>,
) {
    if let Some((event, payload)) = ctx.pending_reply() {
        let ev = Event::new("SYSTEM", "GATEWAY", uuid(), event, payload);
        let _ = transport.send_event(ev).await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn assert_gateway_error(event: &Event) {
        assert_eq!(event.action, "SYSTEM");
        assert_eq!(event.channel_name, "GATEWAY");
        assert_eq!(event.event, "INTERNAL_ERROR");
        assert!(!event.request_id.is_empty());
        assert!(
            event
                .payload
                .get("message")
                .and_then(Value::as_str)
                .is_some()
        );
    }

    #[test]
    fn valid_frame_is_dispatched() {
        let text = r#"{"action":"BROADCAST","event":"message","channelName":"/chat/1","requestId":"r1","payload":{}}"#;
        match classify_text(text, 1024) {
            Inbound::Dispatch(event) => {
                assert_eq!(event.action, "BROADCAST");
                assert_eq!(event.channel_name, "/chat/1");
            }
            _ => panic!("expected dispatch"),
        }
    }

    #[test]
    fn malformed_frame_yields_feedback_without_close() {
        match classify_text("not json", 1024) {
            Inbound::Feedback { event, close } => {
                assert!(!close);
                assert_gateway_error(&event);
            }
            _ => panic!("expected feedback"),
        }
    }

    #[test]
    fn oversize_frame_yields_feedback_with_close() {
        let text = r#"{"action":"BROADCAST","event":"message","channelName":"/chat/1","requestId":"r1","payload":{}}"#;
        match classify_text(text, 8) {
            Inbound::Feedback { event, close } => {
                assert!(close);
                assert_gateway_error(&event);
                assert_eq!(
                    event.payload.get("message").and_then(Value::as_str),
                    Some("message exceeds maximum size")
                );
            }
            _ => panic!("expected feedback"),
        }
    }

    #[test]
    fn adv_exact_max_size_is_not_oversize() {
        let text = r#"{"action":"BROADCAST","event":"message","channelName":"/chat/1","requestId":"r1","payload":{}}"#;
        match classify_text(text, text.len()) {
            Inbound::Dispatch(event) => assert_eq!(event.action, "BROADCAST"),
            _ => panic!("exact-max-size valid frame must dispatch, not be treated as oversize"),
        }
        match classify_text(text, text.len() - 1) {
            Inbound::Feedback { close, .. } => assert!(close),
            _ => panic!("one byte over max must be oversize+close"),
        }
    }

    #[test]
    fn adv_empty_string_is_feedback_without_close() {
        match classify_text("", 1024) {
            Inbound::Feedback { event, close } => {
                assert!(!close);
                assert_gateway_error(&event);
            }
            _ => panic!("empty string must be feedback, not dispatch"),
        }
    }

    #[test]
    fn adv_valid_json_non_object_is_feedback() {
        for text in ["123", "[]", "\"hello\"", "null", "true"] {
            match classify_text(text, 1024) {
                Inbound::Feedback { event, close } => {
                    assert!(!close, "non-object {text} should not close");
                    assert_gateway_error(&event);
                }
                Inbound::Dispatch(_) => panic!("valid-JSON non-object {text} must not dispatch"),
            }
        }
    }

    #[test]
    fn adv_huge_but_valid_message_dispatches_under_generous_limit() {
        let big = "x".repeat(500_000);
        let text = format!(
            r#"{{"action":"BROADCAST","event":"message","channelName":"/chat/1","requestId":"r1","payload":{{"blob":"{big}"}}}}"#
        );
        match classify_text(&text, 1024 * 1024) {
            Inbound::Dispatch(event) => assert_eq!(event.action, "BROADCAST"),
            _ => panic!("huge but valid and under-limit message must dispatch"),
        }
    }

    #[test]
    fn adv_gateway_error_serializes_to_exact_wire_frame() {
        let event = gateway_error_event("boom");
        let json = event_to_json(&event).unwrap();
        let value: Value = serde_json::from_str(&json).unwrap();
        let obj = value.as_object().unwrap();

        assert_eq!(obj.get("action").and_then(Value::as_str), Some("SYSTEM"));
        assert_eq!(
            obj.get("event").and_then(Value::as_str),
            Some("INTERNAL_ERROR")
        );
        assert_eq!(
            obj.get("channelName").and_then(Value::as_str),
            Some("GATEWAY")
        );
        assert!(
            obj.get("requestId")
                .and_then(Value::as_str)
                .is_some_and(|s| !s.is_empty())
        );
        assert_eq!(
            obj.get("payload")
                .and_then(|p| p.get("message"))
                .and_then(Value::as_str),
            Some("boom")
        );
        assert!(!obj.contains_key("nodeId"));
        assert!(!obj.contains_key("fromUserId"));
        assert!(!obj.contains_key("recipients"));
        assert!(!obj.contains_key("recipientDescriptor"));
    }
}
