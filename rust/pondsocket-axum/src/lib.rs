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
use pondsocket_common::PondAssigns;
use serde_json::Value;
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

async fn read_loop(
    endpoint: Arc<Endpoint>,
    transport: Arc<AxumWebSocketTransport>,
    receiver: &mut futures_util::stream::SplitStream<WebSocket>,
) {
    while let Some(next) = receiver.next().await {
        let Ok(message) = next else {
            break;
        };
        match message {
            Message::Text(text) => {
                if text.len() > endpoint.max_message_size() {
                    break;
                }
                let Ok(event) = parse_inbound_text(&text) else {
                    continue;
                };
                let _ = endpoint.handle_message(event, transport.clone()).await;
            }
            Message::Binary(bytes) => {
                if bytes.len() > endpoint.max_message_size() {
                    break;
                }
                let Ok(text) = String::from_utf8(bytes.to_vec()) else {
                    continue;
                };
                let Ok(event) = parse_inbound_text(&text) else {
                    continue;
                };
                let _ = endpoint.handle_message(event, transport.clone()).await;
            }
            Message::Close(_) => break,
            _ => {}
        }
    }
}

async fn send_pending_reply(
    ctx: &pondsocket::ConnectionContext,
    transport: Arc<AxumWebSocketTransport>,
) {
    if let Some((event, payload)) = ctx.pending_reply() {
        let ev = Event::new(
            "SYSTEM",
            "GATEWAY",
            pondsocket_common::uuid(),
            event,
            payload,
        );
        let _ = transport.send_event(ev).await;
    }
}
