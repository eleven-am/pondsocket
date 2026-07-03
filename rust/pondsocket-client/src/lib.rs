use std::collections::{HashMap, VecDeque};
use std::sync::Arc;
use std::time::Duration;

use futures_util::{SinkExt, StreamExt};
use pondsocket_common::{
    ChannelEvent, ChannelState, ClientAction, ClientMessage, EventName, JoinParams, PondMessage,
    PondPresence, PresenceEventType, PresenceMessage, ServerAction, ServerMessage, uuid,
};
use serde_json::{Map, Value};
use thiserror::Error;
use tokio::sync::{Mutex, broadcast, mpsc, oneshot, watch};
use tokio::task::JoinHandle;
use tokio_tungstenite::connect_async;
use tokio_tungstenite::tungstenite::Message;
use url::Url;

pub mod typed;
pub use typed::TypedChannel;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConnectionState {
    Connecting,
    Connected,
    Disconnected,
}

#[derive(Debug, Clone)]
pub struct ClientOptions {
    pub connection_timeout: Duration,
    pub response_timeout: Duration,
    pub max_queue_size: usize,
}

impl Default for ClientOptions {
    fn default() -> Self {
        Self {
            connection_timeout: Duration::from_secs(10),
            response_timeout: Duration::from_secs(5),
            max_queue_size: 100,
        }
    }
}

#[derive(Debug, Error)]
pub enum ClientError {
    #[error("invalid websocket URL: {0}")]
    Url(#[from] url::ParseError),
    #[error("unsupported URL scheme: {0}")]
    UnsupportedScheme(String),
    #[error("websocket error: {0}")]
    WebSocket(#[from] tokio_tungstenite::tungstenite::Error),
    #[error("serialization error: {0}")]
    Serialization(#[from] serde_json::Error),
    #[error("connection timed out")]
    ConnectionTimeout,
    #[error("client is not connected")]
    NotConnected,
    #[error("channel is closed")]
    ChannelClosed,
    #[error("response timed out")]
    ResponseTimeout,
}

type Result<T> = std::result::Result<T, ClientError>;

#[derive(Clone)]
pub struct PondClient {
    inner: Arc<ClientInner>,
}

struct ClientInner {
    url: String,
    options: ClientOptions,
    state: watch::Sender<ConnectionState>,
    channels: Mutex<HashMap<String, Channel>>,
    outbound: Mutex<Option<mpsc::Sender<ClientMessage>>>,
    read_task: Mutex<Option<JoinHandle<()>>>,
    write_task: Mutex<Option<JoinHandle<()>>>,
}

#[derive(Clone)]
pub struct Channel {
    inner: Arc<ChannelInner>,
}

struct ChannelInner {
    name: String,
    params: JoinParams,
    client: Arc<ClientInner>,
    state: watch::Sender<ChannelState>,
    events: broadcast::Sender<ChannelEvent>,
    presence: Mutex<Vec<PondPresence>>,
    queue: Mutex<VecDeque<ClientMessage>>,
    pending: Mutex<HashMap<String, oneshot::Sender<PondMessage>>>,
    closed: Mutex<bool>,
}

impl PondClient {
    pub fn new(endpoint: impl AsRef<str>, params: Option<JoinParams>) -> Result<Self> {
        Self::with_options(endpoint, params, ClientOptions::default())
    }

    pub fn with_options(
        endpoint: impl AsRef<str>,
        params: Option<JoinParams>,
        options: ClientOptions,
    ) -> Result<Self> {
        let url = resolve_url(endpoint.as_ref(), params.as_ref())?;
        let (state, _) = watch::channel(ConnectionState::Disconnected);

        Ok(Self {
            inner: Arc::new(ClientInner {
                url,
                options,
                state,
                channels: Mutex::new(HashMap::new()),
                outbound: Mutex::new(None),
                read_task: Mutex::new(None),
                write_task: Mutex::new(None),
            }),
        })
    }

    pub fn state(&self) -> ConnectionState {
        *self.inner.state.borrow()
    }

    pub fn subscribe_state(&self) -> watch::Receiver<ConnectionState> {
        self.inner.state.subscribe()
    }

    pub async fn create_channel(
        &self,
        name: impl Into<String>,
        params: Option<JoinParams>,
    ) -> Channel {
        let name = name.into();
        let mut channels = self.inner.channels.lock().await;
        if let Some(channel) = channels.get(&name) {
            if channel.state() != ChannelState::Closed && channel.state() != ChannelState::Declined
            {
                return channel.clone();
            }
        }

        let (state, _) = watch::channel(ChannelState::Idle);
        let (events, _) = broadcast::channel(100);
        let channel = Channel {
            inner: Arc::new(ChannelInner {
                name: name.clone(),
                params: params.unwrap_or_default(),
                client: Arc::clone(&self.inner),
                state,
                events,
                presence: Mutex::new(Vec::new()),
                queue: Mutex::new(VecDeque::new()),
                pending: Mutex::new(HashMap::new()),
                closed: Mutex::new(false),
            }),
        };
        channels.insert(name, channel.clone());
        channel
    }

    pub async fn connect(&self) -> Result<()> {
        if self.state() != ConnectionState::Disconnected {
            return Ok(());
        }
        self.inner.state.send_replace(ConnectionState::Connecting);
        let connect = connect_async(&self.inner.url);
        let (socket, _) = tokio::time::timeout(self.inner.options.connection_timeout, connect)
            .await
            .map_err(|_| ClientError::ConnectionTimeout)??;
        let (mut writer, mut reader) = socket.split();
        let (tx, mut rx) = mpsc::channel::<ClientMessage>(self.inner.options.max_queue_size);
        *self.inner.outbound.lock().await = Some(tx);

        let write_task = tokio::spawn(async move {
            while let Some(message) = rx.recv().await {
                let Ok(text) = serde_json::to_string(&message) else {
                    continue;
                };
                if writer.send(Message::Text(text.into())).await.is_err() {
                    break;
                }
            }
            let _ = writer.close().await;
        });

        let inner = Arc::clone(&self.inner);
        let read_task = tokio::spawn(async move {
            while let Some(frame) = reader.next().await {
                let text = match frame {
                    Ok(Message::Text(text)) => text.to_string(),
                    Ok(Message::Binary(bytes)) => match String::from_utf8(bytes.to_vec()) {
                        Ok(text) => text,
                        Err(_) => continue,
                    },
                    Ok(Message::Close(_)) => break,
                    Ok(_) => continue,
                    Err(_) => break,
                };
                let Ok(event) = pondsocket_common::parse_channel_event(&text) else {
                    continue;
                };
                inner.route_event(event).await;
            }
            inner.state.send_replace(ConnectionState::Disconnected);
            *inner.outbound.lock().await = None;
        });

        *self.inner.read_task.lock().await = Some(read_task);
        *self.inner.write_task.lock().await = Some(write_task);
        self.inner.state.send_replace(ConnectionState::Connected);
        self.inner.rejoin_stalled_channels().await;
        Ok(())
    }

    pub async fn disconnect(&self) {
        if let Some(task) = self.inner.read_task.lock().await.take() {
            task.abort();
        }
        if let Some(task) = self.inner.write_task.lock().await.take() {
            task.abort();
        }
        *self.inner.outbound.lock().await = None;
        self.inner.state.send_replace(ConnectionState::Disconnected);
        let channels: Vec<Channel> = self.inner.channels.lock().await.values().cloned().collect();
        for channel in channels {
            channel.force_close().await;
        }
        self.inner.channels.lock().await.clear();
    }
}

impl ClientInner {
    async fn publish(&self, message: ClientMessage) -> Result<()> {
        let tx = self
            .outbound
            .lock()
            .await
            .clone()
            .ok_or(ClientError::NotConnected)?;
        tx.send(message)
            .await
            .map_err(|_| ClientError::NotConnected)
    }

    async fn route_event(&self, event: ChannelEvent) {
        let channel_name = match &event {
            ChannelEvent::Message(message) => &message.channel_name,
            ChannelEvent::Presence(message) => &message.channel_name,
        };
        let channel = self.channels.lock().await.get(channel_name).cloned();
        if let Some(channel) = channel {
            channel.handle_event(event).await;
        }
    }

    async fn rejoin_stalled_channels(&self) {
        let channels: Vec<Channel> = self.channels.lock().await.values().cloned().collect();
        for channel in channels {
            let state = channel.state();
            if state == ChannelState::Joining
                || state == ChannelState::Joined
                || state == ChannelState::Stalled
            {
                channel.join().await;
            }
        }
    }
}

impl Channel {
    pub fn name(&self) -> &str {
        &self.inner.name
    }

    pub fn state(&self) -> ChannelState {
        *self.inner.state.borrow()
    }

    pub fn subscribe_state(&self) -> watch::Receiver<ChannelState> {
        self.inner.state.subscribe()
    }

    pub fn subscribe_events(&self) -> broadcast::Receiver<ChannelEvent> {
        self.inner.events.subscribe()
    }

    pub async fn presence(&self) -> Vec<PondPresence> {
        self.inner.presence.lock().await.clone()
    }

    pub async fn join(&self) {
        if *self.inner.closed.lock().await {
            return;
        }
        if matches!(
            self.state(),
            ChannelState::Joining | ChannelState::Joined | ChannelState::Declined
        ) {
            return;
        }
        self.inner.state.send_replace(ChannelState::Joining);
        self.enqueue_or_send(self.join_message()).await;
    }

    pub async fn leave(&self) {
        if *self.inner.closed.lock().await {
            return;
        }
        let message = ClientMessage {
            action: ClientAction::LeaveChannel,
            event: "LEAVE_CHANNEL".to_owned(),
            payload: Map::new(),
            channel_name: self.inner.name.clone(),
            request_id: uuid(),
        };
        let _ = self.inner.client.publish(message).await;
        self.force_close().await;
    }

    pub async fn send_message(&self, event: impl Into<String>, payload: Option<PondMessage>) {
        if *self.inner.closed.lock().await {
            return;
        }
        let message = ClientMessage {
            action: ClientAction::Broadcast,
            event: event.into(),
            payload: payload.unwrap_or_default(),
            channel_name: self.inner.name.clone(),
            request_id: uuid(),
        };
        self.enqueue_or_send(message).await;
    }

    pub async fn send_for_response(
        &self,
        event: impl Into<String>,
        payload: Option<PondMessage>,
        timeout: Option<Duration>,
    ) -> Result<PondMessage> {
        if *self.inner.closed.lock().await {
            return Err(ClientError::ChannelClosed);
        }
        let request_id = uuid();
        let (tx, rx) = oneshot::channel();
        self.inner
            .pending
            .lock()
            .await
            .insert(request_id.clone(), tx);
        let message = ClientMessage {
            action: ClientAction::Broadcast,
            event: event.into(),
            payload: payload.unwrap_or_default(),
            channel_name: self.inner.name.clone(),
            request_id: request_id.clone(),
        };
        self.enqueue_or_send(message).await;
        let timeout = timeout.unwrap_or(self.inner.client.options.response_timeout);
        let result = tokio::time::timeout(timeout, rx).await;
        self.inner.pending.lock().await.remove(&request_id);
        match result {
            Ok(Ok(payload)) => Ok(payload),
            _ => Err(ClientError::ResponseTimeout),
        }
    }

    async fn enqueue_or_send(&self, message: ClientMessage) {
        let connected = *self.inner.client.state.borrow() == ConnectionState::Connected;
        let joined = self.state() == ChannelState::Joined;
        let is_join = message.action == ClientAction::JoinChannel;
        if connected && (joined || is_join) {
            if self.inner.client.publish(message.clone()).await.is_ok() {
                return;
            }
        }
        let mut queue = self.inner.queue.lock().await;
        if queue.len() == self.inner.client.options.max_queue_size {
            queue.pop_front();
        }
        queue.push_back(message);
    }

    async fn handle_event(&self, event: ChannelEvent) {
        if *self.inner.closed.lock().await {
            return;
        }
        match event {
            ChannelEvent::Presence(message) => self.handle_presence(message).await,
            ChannelEvent::Message(message) => self.handle_message(message).await,
        }
    }

    async fn handle_presence(&self, message: PresenceMessage) {
        *self.inner.presence.lock().await = message.payload.presence.clone();
        let event = ChannelEvent::Presence(message.clone());
        let _ = self.inner.events.send(event);
    }

    async fn handle_message(&self, message: ServerMessage) {
        if message.action == ServerAction::System
            && message.event == event_name(EventName::Acknowledge)
        {
            self.acknowledge().await;
            return;
        }
        if message.action == ServerAction::System
            && message.event == event_name(EventName::Unauthorized)
        {
            self.decline().await;
            return;
        }
        if let Some(tx) = self.inner.pending.lock().await.remove(&message.request_id) {
            let _ = tx.send(message.payload);
            return;
        }
        if self.state() == ChannelState::Joined {
            let _ = self.inner.events.send(ChannelEvent::Message(message));
        }
    }

    async fn acknowledge(&self) {
        if self.state() != ChannelState::Joined {
            self.inner.state.send_replace(ChannelState::Joined);
        }
        let mut queue = self.inner.queue.lock().await;
        let pending: Vec<ClientMessage> = queue.drain(..).collect();
        drop(queue);
        for message in pending {
            let _ = self.inner.client.publish(message).await;
        }
    }

    async fn decline(&self) {
        self.inner.state.send_replace(ChannelState::Declined);
        self.inner.queue.lock().await.clear();
        self.inner.pending.lock().await.clear();
    }

    async fn force_close(&self) {
        *self.inner.closed.lock().await = true;
        self.inner.state.send_replace(ChannelState::Closed);
        self.inner.queue.lock().await.clear();
        self.inner.pending.lock().await.clear();
    }

    fn join_message(&self) -> ClientMessage {
        ClientMessage {
            action: ClientAction::JoinChannel,
            event: "JOIN_CHANNEL".to_owned(),
            payload: self.inner.params.clone(),
            channel_name: self.inner.name.clone(),
            request_id: uuid(),
        }
    }
}

fn resolve_url(endpoint: &str, params: Option<&JoinParams>) -> Result<String> {
    let mut url = Url::parse(endpoint)?;
    match url.scheme() {
        "http" => url
            .set_scheme("ws")
            .map_err(|_| ClientError::UnsupportedScheme("http".to_owned()))?,
        "https" => url
            .set_scheme("wss")
            .map_err(|_| ClientError::UnsupportedScheme("https".to_owned()))?,
        "ws" | "wss" => {}
        scheme => return Err(ClientError::UnsupportedScheme(scheme.to_owned())),
    }
    if let Some(params) = params {
        let mut pairs = url.query_pairs_mut();
        for (key, value) in params {
            let value = match value {
                Value::String(value) => value.clone(),
                other => other.to_string(),
            };
            pairs.append_pair(key, &value);
        }
    }
    Ok(url.to_string())
}

fn event_name(event: EventName) -> String {
    serde_json::to_string(&event)
        .unwrap_or_default()
        .trim_matches('"')
        .to_owned()
}

#[allow(dead_code)]
fn presence_event_name(event: PresenceEventType) -> String {
    serde_json::to_string(&event)
        .unwrap_or_default()
        .trim_matches('"')
        .to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;
    use pondsocket_common::{PondEvent, PondSchema, PresencePayload};
    use serde::{Deserialize, Serialize};

    #[test]
    fn resolves_http_url_to_ws_with_params() {
        let mut params = JoinParams::new();
        params.insert("token".to_owned(), Value::String("abc".to_owned()));
        let url = resolve_url("https://example.com/socket?room=one", Some(&params)).unwrap();
        assert_eq!(url, "wss://example.com/socket?room=one&token=abc");
    }

    #[tokio::test]
    async fn queues_join_message_before_connect() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.join().await;
        assert_eq!(channel.state(), ChannelState::Joining);
        assert_eq!(channel.inner.queue.lock().await.len(), 1);
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
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct Assigns {
        role: String,
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct Join {
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
        type JoinParams = Join;
    }

    #[tokio::test]
    async fn typed_channel_sends_and_decodes_schema_values() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let params = Join {
            token: "secret".to_owned(),
        };
        let channel = client
            .create_typed_channel::<ChatSchema>("room", Some(&params))
            .await
            .unwrap();

        channel.join().await;
        channel
            .send::<Chat>(&ChatPayload {
                text: "hello".to_owned(),
            })
            .await
            .unwrap();

        let queued = channel.raw().inner.queue.lock().await;
        assert_eq!(queued[0].payload["token"], "secret");
        assert_eq!(queued[1].event, "chat");
        assert_eq!(queued[1].payload["text"], "hello");
        drop(queued);

        let message = ServerMessage {
            action: ServerAction::Broadcast,
            event: "chat".to_owned(),
            channel_name: "room".to_owned(),
            request_id: "r1".to_owned(),
            payload: serde_json::from_value(serde_json::json!({ "text": "from server" })).unwrap(),
        };
        assert_eq!(
            channel.decode_message::<Chat>(&message).unwrap(),
            Some(ChatPayload {
                text: "from server".to_owned()
            })
        );

        let presence = PresenceMessage {
            action: pondsocket_common::PresenceAction::Presence,
            event: PresenceEventType::Join,
            channel_name: "room".to_owned(),
            request_id: "p1".to_owned(),
            payload: PresencePayload {
                changed: serde_json::from_value(serde_json::json!({ "user_id": "u1" })).unwrap(),
                presence: vec![
                    serde_json::from_value(serde_json::json!({ "user_id": "u1" })).unwrap(),
                ],
            },
        };
        let (changed, users) = channel.decode_presence(&presence).unwrap();
        assert_eq!(
            changed,
            Presence {
                user_id: "u1".to_owned()
            }
        );
        assert_eq!(users, vec![changed]);
    }
}
