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
use tokio::sync::{Mutex, Notify, broadcast, mpsc, oneshot, watch};
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
    #[error("channel join was declined")]
    ChannelDeclined,
    #[error("response timed out")]
    ResponseTimeout,
    #[error("max_queue_size must be greater than zero")]
    InvalidQueueCapacity,
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
    conn_events: broadcast::Sender<ConnectionState>,
    channels: Mutex<HashMap<String, Channel>>,
    outbound: Mutex<Option<mpsc::Sender<ClientMessage>>>,
    read_task: Mutex<Option<JoinHandle<()>>>,
    write_task: Mutex<Option<JoinHandle<()>>>,
    lifecycle_lock: Mutex<()>,
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
    queue_space: Notify,
    send_lock: Mutex<()>,
    pending: Mutex<HashMap<String, oneshot::Sender<Result<PondMessage>>>>,
    pending_join: Mutex<Option<String>>,
    decline_reason: Mutex<Option<PondMessage>>,
    conn_task: Mutex<Option<JoinHandle<()>>>,
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
        if options.max_queue_size == 0 {
            return Err(ClientError::InvalidQueueCapacity);
        }
        let url = resolve_url(endpoint.as_ref(), params.as_ref())?;
        let (state, _) = watch::channel(ConnectionState::Disconnected);
        let (conn_events, _) = broadcast::channel(16);

        Ok(Self {
            inner: Arc::new(ClientInner {
                url,
                options,
                state,
                conn_events,
                channels: Mutex::new(HashMap::new()),
                outbound: Mutex::new(None),
                read_task: Mutex::new(None),
                write_task: Mutex::new(None),
                lifecycle_lock: Mutex::new(()),
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
        if let Some(channel) = channels.get(&name)
            && channel.state() != ChannelState::Closed
            && channel.state() != ChannelState::Declined
        {
            return channel.clone();
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
                queue_space: Notify::new(),
                send_lock: Mutex::new(()),
                pending: Mutex::new(HashMap::new()),
                pending_join: Mutex::new(None),
                decline_reason: Mutex::new(None),
                conn_task: Mutex::new(None),
                closed: Mutex::new(false),
            }),
        };

        let watcher = channel.clone();
        let mut conn_rx = self.inner.conn_events.subscribe();
        let handle = tokio::spawn(async move {
            loop {
                match conn_rx.recv().await {
                    Ok(state) => watcher.on_connection_change(state).await,
                    Err(broadcast::error::RecvError::Lagged(_)) => {
                        let state = *watcher.inner.client.state.borrow();
                        watcher.on_connection_change(state).await;
                    }
                    Err(broadcast::error::RecvError::Closed) => break,
                }
                if *watcher.inner.closed.lock().await {
                    break;
                }
            }
        });
        *channel.inner.conn_task.lock().await = Some(handle);

        channels.insert(name, channel.clone());
        channel
    }

    pub async fn connect(&self) -> Result<()> {
        let _lifecycle = self.inner.lifecycle_lock.lock().await;
        if self.state() != ConnectionState::Disconnected {
            return Ok(());
        }
        self.inner.set_connection_state(ConnectionState::Connecting);
        let connect = connect_async(&self.inner.url);
        let (socket, _) =
            match tokio::time::timeout(self.inner.options.connection_timeout, connect).await {
                Ok(Ok(connection)) => connection,
                Ok(Err(error)) => {
                    self.inner
                        .set_connection_state(ConnectionState::Disconnected);
                    return Err(ClientError::WebSocket(error));
                }
                Err(_) => {
                    self.inner
                        .set_connection_state(ConnectionState::Disconnected);
                    return Err(ClientError::ConnectionTimeout);
                }
            };
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
        // Publish Connected before the reader can observe an immediate peer
        // close. Otherwise a fast reader exit can set Disconnected and then be
        // overwritten by this connect call after the socket is already gone.
        self.inner.set_connection_state(ConnectionState::Connected);
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
                for line in text.lines().map(str::trim).filter(|line| !line.is_empty()) {
                    let Ok(event) = pondsocket_common::parse_channel_event(line) else {
                        continue;
                    };
                    inner.route_event(event).await;
                }
            }
            *inner.outbound.lock().await = None;
            inner.set_connection_state(ConnectionState::Disconnected);
        });

        *self.inner.read_task.lock().await = Some(read_task);
        *self.inner.write_task.lock().await = Some(write_task);
        Ok(())
    }

    pub async fn disconnect(&self) {
        let _lifecycle = self.inner.lifecycle_lock.lock().await;
        *self.inner.outbound.lock().await = None;
        self.inner
            .set_connection_state(ConnectionState::Disconnected);
        let channels: Vec<Channel> = self.inner.channels.lock().await.values().cloned().collect();
        for channel in channels {
            channel.force_close().await;
        }
        self.inner.channels.lock().await.clear();

        if let Some(mut task) = self.inner.write_task.lock().await.take()
            && tokio::time::timeout(self.inner.options.connection_timeout, &mut task)
                .await
                .is_err()
        {
            task.abort();
            let _ = task.await;
        }
        if let Some(task) = self.inner.read_task.lock().await.take() {
            task.abort();
            let _ = task.await;
        }
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

    fn set_connection_state(&self, state: ConnectionState) {
        let changed = *self.state.borrow() != state;
        self.state.send_replace(state);
        if changed {
            let _ = self.conn_events.send(state);
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

    pub async fn decline_reason(&self) -> Option<PondMessage> {
        self.inner.decline_reason.lock().await.clone()
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
        let message = self.join_message();
        *self.inner.pending_join.lock().await = Some(message.request_id.clone());
        self.inner.state.send_replace(ChannelState::Joining);
        let _ = self.enqueue_or_send(message).await;
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
        let _ = self.send_message_checked(event, payload).await;
    }

    pub async fn send_message_checked(
        &self,
        event: impl Into<String>,
        payload: Option<PondMessage>,
    ) -> Result<()> {
        if *self.inner.closed.lock().await {
            return Err(ClientError::ChannelClosed);
        }
        let message = ClientMessage {
            action: ClientAction::Broadcast,
            event: event.into(),
            payload: payload.unwrap_or_default(),
            channel_name: self.inner.name.clone(),
            request_id: uuid(),
        };
        self.enqueue_or_send(message).await
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
        let timeout = timeout.unwrap_or(self.inner.client.options.response_timeout);
        let started = tokio::time::Instant::now();
        let queued = tokio::time::timeout(timeout, self.enqueue_or_send(message)).await;
        match queued {
            Ok(Ok(())) => {}
            Ok(Err(error)) => {
                self.inner.pending.lock().await.remove(&request_id);
                return Err(error);
            }
            Err(_) => {
                self.inner.pending.lock().await.remove(&request_id);
                return Err(ClientError::ResponseTimeout);
            }
        }
        let remaining = timeout.saturating_sub(started.elapsed());
        let result = tokio::time::timeout(remaining, rx).await;
        self.inner.pending.lock().await.remove(&request_id);
        match result {
            Ok(Ok(result)) => result,
            Ok(Err(_)) => Err(ClientError::ChannelClosed),
            Err(_) => Err(ClientError::ResponseTimeout),
        }
    }

    async fn enqueue_or_send(&self, message: ClientMessage) -> Result<()> {
        loop {
            if *self.inner.closed.lock().await {
                return Err(ClientError::ChannelClosed);
            }

            let send_guard = self.inner.send_lock.lock().await;
            let connected = *self.inner.client.state.borrow() == ConnectionState::Connected;
            let joined = self.state() == ChannelState::Joined;
            let is_join = message.action == ClientAction::JoinChannel;
            if connected && (joined || is_join) {
                match self.inner.client.publish(message.clone()).await {
                    Ok(()) => return Ok(()),
                    Err(ClientError::NotConnected) => {}
                    Err(error) => return Err(error),
                }
            }

            let mut queue = self.inner.queue.lock().await;
            if is_join {
                queue.retain(|queued| queued.action != ClientAction::JoinChannel);
                queue.push_front(message);
                return Ok(());
            }
            if queue.len() < self.inner.client.options.max_queue_size {
                queue.push_back(message);
                return Ok(());
            }
            let notified = self.inner.queue_space.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
            drop(queue);
            drop(send_guard);
            notified.await;
        }
    }

    async fn on_connection_change(&self, state: ConnectionState) {
        if *self.inner.closed.lock().await {
            return;
        }
        match state {
            ConnectionState::Disconnected => {
                *self.inner.pending_join.lock().await = None;
                if matches!(self.state(), ChannelState::Joined | ChannelState::Joining) {
                    self.inner.state.send_replace(ChannelState::Stalled);
                }
            }
            ConnectionState::Connected => {
                if matches!(self.state(), ChannelState::Stalled | ChannelState::Joining) {
                    let _send_guard = self.inner.send_lock.lock().await;
                    self.inner
                        .queue
                        .lock()
                        .await
                        .retain(|message| message.action != ClientAction::JoinChannel);
                    self.inner.queue_space.notify_waiters();
                    drop(_send_guard);
                    self.inner.state.send_replace(ChannelState::Stalled);
                    self.join().await;
                }
            }
            ConnectionState::Connecting => {}
        }
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
        if message.action == ServerAction::System && message.event == "EVICTED" {
            let _ = self.inner.events.send(ChannelEvent::Message(message));
            self.force_close().await;
            return;
        }
        if message.action == ServerAction::System
            && message.event == event_name(EventName::Acknowledge)
        {
            if matches!(self.state(), ChannelState::Joining | ChannelState::Stalled) {
                let mut pending_join = self.inner.pending_join.lock().await;
                if pending_join.as_deref() != Some(message.request_id.as_str()) {
                    return;
                }
                *pending_join = None;
                drop(pending_join);
                self.acknowledge().await;
            }
            return;
        }
        if message.action == ServerAction::System
            && message.event == event_name(EventName::Unauthorized)
            && matches!(self.state(), ChannelState::Joining | ChannelState::Stalled)
        {
            if self.take_matching_join(&message.request_id).await {
                self.decline(message.payload).await;
            }
            return;
        }
        if message.action == ServerAction::System
            && message.event == event_name(EventName::NotFound)
            && matches!(self.state(), ChannelState::Joining | ChannelState::Stalled)
        {
            if self.take_matching_join(&message.request_id).await {
                self.decline(message.payload).await;
            }
            return;
        }
        if let Some(tx) = self.inner.pending.lock().await.remove(&message.request_id) {
            let _ = tx.send(Ok(message.payload));
            return;
        }
        if self.state() == ChannelState::Joined {
            let _ = self.inner.events.send(ChannelEvent::Message(message));
        }
    }

    async fn acknowledge(&self) {
        let _send_guard = self.inner.send_lock.lock().await;
        let mut queue = self.inner.queue.lock().await;
        queue.retain(|message| message.action != ClientAction::JoinChannel);
        let mut pending: VecDeque<ClientMessage> = queue.drain(..).collect();
        self.inner.state.send_replace(ChannelState::Joined);
        while let Some(message) = pending.pop_front() {
            if self.inner.client.publish(message.clone()).await.is_err() {
                queue.push_back(message);
                queue.append(&mut pending);
                self.inner.state.send_replace(ChannelState::Stalled);
                break;
            }
        }
        drop(queue);
        self.inner.queue_space.notify_waiters();
    }

    async fn decline(&self, reason: PondMessage) {
        *self.inner.decline_reason.lock().await = Some(reason);
        self.inner.state.send_replace(ChannelState::Declined);
        self.inner.queue.lock().await.clear();
        self.inner.queue_space.notify_waiters();
        let pending = std::mem::take(&mut *self.inner.pending.lock().await);
        for (_, response) in pending {
            let _ = response.send(Err(ClientError::ChannelDeclined));
        }
    }

    async fn force_close(&self) {
        *self.inner.closed.lock().await = true;
        self.inner.state.send_replace(ChannelState::Closed);
        self.inner.queue.lock().await.clear();
        self.inner.queue_space.notify_waiters();
        let pending = std::mem::take(&mut *self.inner.pending.lock().await);
        for (_, response) in pending {
            let _ = response.send(Err(ClientError::ChannelClosed));
        }
        *self.inner.pending_join.lock().await = None;
        if let Some(handle) = self.inner.conn_task.lock().await.take() {
            handle.abort();
            let _ = handle.await;
        }
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

    async fn take_matching_join(&self, request_id: &str) -> bool {
        let mut pending = self.inner.pending_join.lock().await;
        if pending.as_deref() != Some(request_id) {
            return false;
        }
        *pending = None;
        true
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
    async fn failed_connect_restores_disconnected_state_and_can_retry() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        drop(listener);
        let client = PondClient::with_options(
            format!("ws://{addr}/socket"),
            None,
            ClientOptions {
                connection_timeout: Duration::from_millis(100),
                ..ClientOptions::default()
            },
        )
        .unwrap();

        assert!(client.connect().await.is_err());
        assert_eq!(client.state(), ConnectionState::Disconnected);
        assert!(client.connect().await.is_err());
        assert_eq!(client.state(), ConnectionState::Disconnected);
    }

    #[tokio::test]
    async fn immediate_peer_close_cannot_be_overwritten_by_connected_state() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut socket = tokio_tungstenite::accept_async(stream).await.unwrap();
            socket.close(None).await.unwrap();
        });
        let client = PondClient::new(format!("ws://{addr}/socket"), None).unwrap();
        let mut state = client.subscribe_state();

        client.connect().await.unwrap();
        tokio::time::timeout(Duration::from_secs(1), async {
            loop {
                if *state.borrow() == ConnectionState::Disconnected {
                    break;
                }
                state.changed().await.unwrap();
            }
        })
        .await
        .expect("client stayed connected after the peer closed immediately");
        assert!(client.inner.outbound.lock().await.is_none());
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

    use std::sync::atomic::{AtomicUsize, Ordering};
    use tokio::net::{TcpListener, TcpStream};
    use tokio_tungstenite::accept_async;

    async fn serve_session(
        stream: TcpStream,
        joins: Arc<AtomicUsize>,
        conn_id: u64,
        mut kill: broadcast::Receiver<()>,
    ) {
        let Ok(ws) = accept_async(stream).await else {
            return;
        };
        let (mut writer, mut reader) = ws.split();
        loop {
            tokio::select! {
                _ = kill.recv() => {
                    let _ = writer.close().await;
                    return;
                }
                frame = reader.next() => {
                    let message = match frame {
                        Some(Ok(Message::Text(text))) => text.to_string(),
                        Some(Ok(_)) => continue,
                        _ => return,
                    };
                    let Ok(request) = serde_json::from_str::<ClientMessage>(&message) else {
                        continue;
                    };
                    if request.action != ClientAction::JoinChannel {
                        continue;
                    }
                    joins.fetch_add(1, Ordering::SeqCst);
                    let ack = ServerMessage {
                        action: ServerAction::System,
                        event: event_name(EventName::Acknowledge),
                        channel_name: request.channel_name.clone(),
                        request_id: request.request_id.clone(),
                        payload: Map::new(),
                    };
                    let _ = writer
                        .send(Message::Text(serde_json::to_string(&ack).unwrap().into()))
                        .await;
                    let mut payload = Map::new();
                    payload.insert("conn".to_owned(), Value::from(conn_id));
                    let greeting = ServerMessage {
                        action: ServerAction::Broadcast,
                        event: "greeting".to_owned(),
                        channel_name: request.channel_name.clone(),
                        request_id: uuid(),
                        payload,
                    };
                    let _ = writer
                        .send(Message::Text(serde_json::to_string(&greeting).unwrap().into()))
                        .await;
                }
            }
        }
    }

    async fn wait_for_state(channel: &Channel, target: ChannelState) {
        let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
        while channel.state() != target {
            if tokio::time::Instant::now() > deadline {
                panic!(
                    "timed out waiting for {target:?}, still {:?}",
                    channel.state()
                );
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    }

    async fn recv_greeting(events: &mut broadcast::Receiver<ChannelEvent>) -> u64 {
        loop {
            match tokio::time::timeout(Duration::from_secs(2), events.recv()).await {
                Ok(Ok(ChannelEvent::Message(message))) if message.event == "greeting" => {
                    return message.payload.get("conn").and_then(Value::as_u64).unwrap();
                }
                Ok(Ok(_)) => {}
                other => panic!("expected greeting event, got {other:?}"),
            }
        }
    }

    #[tokio::test]
    async fn rejoins_channel_after_socket_drop_and_keeps_receiving_events() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let joins = Arc::new(AtomicUsize::new(0));
        let (kill, _) = broadcast::channel::<()>(4);

        let server_joins = Arc::clone(&joins);
        let server_kill = kill.clone();
        tokio::spawn(async move {
            let mut conn_id = 0;
            while let Ok((stream, _)) = listener.accept().await {
                conn_id += 1;
                tokio::spawn(serve_session(
                    stream,
                    Arc::clone(&server_joins),
                    conn_id,
                    server_kill.subscribe(),
                ));
            }
        });

        let url = format!("ws://{addr}/socket");
        let client = PondClient::new(&url, None).unwrap();
        client.connect().await.unwrap();
        let channel = client.create_channel("room", None).await;
        let mut events = channel.subscribe_events();
        channel.join().await;

        wait_for_state(&channel, ChannelState::Joined).await;
        assert_eq!(recv_greeting(&mut events).await, 1);
        assert_eq!(joins.load(Ordering::SeqCst), 1);

        kill.send(()).unwrap();
        wait_for_state(&channel, ChannelState::Stalled).await;

        client.connect().await.unwrap();
        wait_for_state(&channel, ChannelState::Joined).await;
        assert_eq!(joins.load(Ordering::SeqCst), 2);

        assert_eq!(recv_greeting(&mut events).await, 2);
    }

    async fn serve_not_found(stream: TcpStream, mut kill: broadcast::Receiver<()>) {
        let Ok(ws) = accept_async(stream).await else {
            return;
        };
        let (mut writer, mut reader) = ws.split();
        loop {
            tokio::select! {
                _ = kill.recv() => {
                    let _ = writer.close().await;
                    return;
                }
                frame = reader.next() => {
                    let message = match frame {
                        Some(Ok(Message::Text(text))) => text.to_string(),
                        Some(Ok(_)) => continue,
                        _ => return,
                    };
                    let Ok(request) = serde_json::from_str::<ClientMessage>(&message) else {
                        continue;
                    };
                    if request.action != ClientAction::JoinChannel {
                        continue;
                    }
                    let mut payload = Map::new();
                    payload.insert("channel".to_owned(), Value::from(request.channel_name.clone()));
                    let not_found = ServerMessage {
                        action: ServerAction::System,
                        event: event_name(EventName::NotFound),
                        channel_name: request.channel_name.clone(),
                        request_id: request.request_id.clone(),
                        payload,
                    };
                    let _ = writer
                        .send(Message::Text(serde_json::to_string(&not_found).unwrap().into()))
                        .await;
                }
            }
        }
    }

    async fn serve_join_without_ack(
        stream: TcpStream,
        joins: Arc<AtomicUsize>,
        mut kill: broadcast::Receiver<()>,
    ) {
        let Ok(ws) = accept_async(stream).await else {
            return;
        };
        let (mut writer, mut reader) = ws.split();
        loop {
            tokio::select! {
                _ = kill.recv() => {
                    let _ = writer.close().await;
                    return;
                }
                frame = reader.next() => {
                    match frame {
                        Some(Ok(Message::Text(text))) => {
                            if let Ok(request) = serde_json::from_str::<ClientMessage>(&text)
                                && request.action == ClientAction::JoinChannel
                            {
                                joins.fetch_add(1, Ordering::SeqCst);
                            }
                        }
                        Some(Ok(_)) => {}
                        _ => return,
                    }
                }
            }
        }
    }

    #[tokio::test]
    async fn transitions_to_declined_on_not_found() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let (kill, _) = broadcast::channel::<()>(4);

        let server_kill = kill.clone();
        tokio::spawn(async move {
            while let Ok((stream, _)) = listener.accept().await {
                tokio::spawn(serve_not_found(stream, server_kill.subscribe()));
            }
        });

        let url = format!("ws://{addr}/socket");
        let client = PondClient::new(&url, None).unwrap();
        client.connect().await.unwrap();
        let channel = client.create_channel("room", None).await;
        channel.join().await;

        wait_for_state(&channel, ChannelState::Declined).await;
        let reason = channel.decline_reason().await.unwrap();
        assert_eq!(reason.get("channel").and_then(Value::as_str), Some("room"));
        assert!(channel.inner.queue.lock().await.is_empty());
        assert!(channel.inner.pending.lock().await.is_empty());
    }

    #[tokio::test]
    async fn rejoins_channel_that_was_mid_join_when_socket_dropped() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let joins = Arc::new(AtomicUsize::new(0));
        let (kill, _) = broadcast::channel::<()>(4);

        let server_joins = Arc::clone(&joins);
        let server_kill = kill.clone();
        tokio::spawn(async move {
            let mut conn_id = 0;
            while let Ok((stream, _)) = listener.accept().await {
                conn_id += 1;
                let joins = Arc::clone(&server_joins);
                if conn_id == 1 {
                    tokio::spawn(serve_join_without_ack(
                        stream,
                        joins,
                        server_kill.subscribe(),
                    ));
                } else {
                    tokio::spawn(serve_session(
                        stream,
                        joins,
                        conn_id,
                        server_kill.subscribe(),
                    ));
                }
            }
        });

        let url = format!("ws://{addr}/socket");
        let client = PondClient::new(&url, None).unwrap();
        client.connect().await.unwrap();
        let channel = client.create_channel("room", None).await;
        let mut events = channel.subscribe_events();
        channel.join().await;

        assert_eq!(channel.state(), ChannelState::Joining);
        let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
        while joins.load(Ordering::SeqCst) < 1 {
            if tokio::time::Instant::now() > deadline {
                panic!("server never observed the initial join");
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }

        kill.send(()).unwrap();
        wait_for_state(&channel, ChannelState::Stalled).await;

        client.connect().await.unwrap();
        wait_for_state(&channel, ChannelState::Joined).await;
        assert_eq!(joins.load(Ordering::SeqCst), 2);
        assert_eq!(recv_greeting(&mut events).await, 2);
    }

    fn system_message(
        event: EventName,
        channel: &str,
        mut payload: Map<String, Value>,
    ) -> ServerMessage {
        payload
            .entry("channel".to_owned())
            .or_insert_with(|| Value::from(channel.to_owned()));
        ServerMessage {
            action: ServerAction::System,
            event: event_name(event),
            channel_name: channel.to_owned(),
            request_id: uuid(),
            payload,
        }
    }

    #[tokio::test]
    async fn adv_not_found_after_joined_leaves_live_channel_intact() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        let mut events = channel.subscribe_events();

        channel
            .handle_message(system_message(EventName::NotFound, "room", Map::new()))
            .await;

        assert_eq!(channel.state(), ChannelState::Joined);
        assert!(channel.decline_reason().await.is_none());
        match events.try_recv() {
            Ok(ChannelEvent::Message(m)) => assert_eq!(m.event, event_name(EventName::NotFound)),
            other => panic!("expected NOT_FOUND surfaced as message event, got {other:?}"),
        }
    }

    #[tokio::test]
    async fn adv_unauthorized_after_joined_leaves_live_channel_intact() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);

        channel
            .handle_message(system_message(EventName::Unauthorized, "room", Map::new()))
            .await;

        assert_eq!(channel.state(), ChannelState::Joined);
        assert!(channel.decline_reason().await.is_none());
    }

    #[tokio::test]
    async fn adv_decline_errors_pending_response_promptly() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;

        let asker = channel.clone();
        let handle = tokio::spawn(async move {
            asker
                .send_for_response("ask", None, Some(Duration::from_secs(30)))
                .await
        });

        let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
        while channel.inner.pending.lock().await.is_empty() {
            if tokio::time::Instant::now() > deadline {
                panic!("pending request never registered");
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }

        channel.decline(Map::new()).await;

        let joined = tokio::time::timeout(Duration::from_secs(2), handle)
            .await
            .expect("send_for_response hung after decline instead of returning")
            .unwrap();
        assert!(matches!(joined, Err(ClientError::ChannelDeclined)));
        assert!(channel.inner.pending.lock().await.is_empty());
        assert!(channel.inner.queue.lock().await.is_empty());
    }

    #[tokio::test]
    async fn adv_duplicate_decline_keeps_latest_reason_without_panic() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;

        let mut first = Map::new();
        first.insert("code".to_owned(), Value::from("first"));
        channel.decline(first).await;

        let mut second = Map::new();
        second.insert("code".to_owned(), Value::from("second"));
        channel.decline(second).await;

        assert_eq!(channel.state(), ChannelState::Declined);
        assert_eq!(
            channel
                .decline_reason()
                .await
                .unwrap()
                .get("code")
                .and_then(Value::as_str),
            Some("second")
        );
    }

    #[tokio::test]
    async fn adv_leave_during_stall_blocks_rejoin_on_reconnect() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.join().await;
        assert_eq!(channel.state(), ChannelState::Joining);

        channel
            .on_connection_change(ConnectionState::Disconnected)
            .await;
        assert_eq!(channel.state(), ChannelState::Stalled);

        channel.leave().await;
        assert_eq!(channel.state(), ChannelState::Closed);

        channel
            .on_connection_change(ConnectionState::Connected)
            .await;
        assert_eq!(channel.state(), ChannelState::Closed);
        assert!(channel.inner.queue.lock().await.is_empty());
    }

    #[tokio::test]
    async fn adv_join_before_connect_completes_on_connect() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let joins = Arc::new(AtomicUsize::new(0));
        let (kill, _) = broadcast::channel::<()>(4);

        let server_joins = Arc::clone(&joins);
        let server_kill = kill.clone();
        tokio::spawn(async move {
            let mut conn_id = 0;
            while let Ok((stream, _)) = listener.accept().await {
                conn_id += 1;
                tokio::spawn(serve_session(
                    stream,
                    Arc::clone(&server_joins),
                    conn_id,
                    server_kill.subscribe(),
                ));
            }
        });

        let url = format!("ws://{addr}/socket");
        let client = PondClient::new(&url, None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.join().await;
        assert_eq!(channel.state(), ChannelState::Joining);
        assert_eq!(channel.inner.queue.lock().await.len(), 1);

        client.connect().await.unwrap();
        wait_for_state(&channel, ChannelState::Joined).await;
        assert_eq!(joins.load(Ordering::SeqCst), 1);
        assert!(channel.inner.queue.lock().await.is_empty());
    }

    async fn wait_for_pending(channel: &Channel) -> String {
        let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
        loop {
            if let Some(id) = channel.inner.pending.lock().await.keys().next().cloned() {
                return id;
            }
            if tokio::time::Instant::now() > deadline {
                panic!("pending request never registered");
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    }

    #[tokio::test]
    async fn adv_event_not_found_on_joined_channel_resolves_pending_fast() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);

        let asker = channel.clone();
        let handle = tokio::spawn(async move {
            let start = tokio::time::Instant::now();
            let result = asker
                .send_for_response("no_handler", None, Some(Duration::from_millis(300)))
                .await;
            (start.elapsed(), result)
        });
        let request_id = wait_for_pending(&channel).await;

        let mut payload = Map::new();
        payload.insert("channel".to_owned(), Value::from("room"));
        payload.insert("event".to_owned(), Value::from("no_handler"));
        let not_found = ServerMessage {
            action: ServerAction::System,
            event: event_name(EventName::NotFound),
            channel_name: "room".to_owned(),
            request_id,
            payload,
        };
        channel.handle_message(not_found).await;

        let (elapsed, result) = tokio::time::timeout(Duration::from_secs(2), handle)
            .await
            .expect("request hung")
            .unwrap();
        let resolved = result.expect("pending request should resolve, not time out");
        assert_eq!(
            resolved.get("event").and_then(Value::as_str),
            Some("no_handler")
        );
        assert!(elapsed < Duration::from_millis(250));
        assert_eq!(channel.state(), ChannelState::Joined);
        assert!(channel.decline_reason().await.is_none());
    }

    #[tokio::test]
    async fn adv_evicted_closes_client_channel() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        let mut events = channel.subscribe_events();

        let mut payload = Map::new();
        payload.insert("reason".to_owned(), Value::from("kicked"));
        let evicted = ServerMessage {
            action: ServerAction::System,
            event: "EVICTED".to_owned(),
            channel_name: "room".to_owned(),
            request_id: uuid(),
            payload,
        };
        channel.handle_message(evicted).await;

        match tokio::time::timeout(Duration::from_secs(1), events.recv()).await {
            Ok(Ok(ChannelEvent::Message(m))) => assert_eq!(m.event, "EVICTED"),
            other => panic!("expected EVICTED forwarded as message event, got {other:?}"),
        }
        assert_eq!(channel.state(), ChannelState::Closed);
    }

    #[tokio::test]
    async fn adv_late_response_after_timeout_surfaces_as_message() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        let mut events = channel.subscribe_events();

        let mut payload = Map::new();
        payload.insert("ok".to_owned(), Value::from(true));
        let late = ServerMessage {
            action: ServerAction::Broadcast,
            event: "reply".to_owned(),
            channel_name: "room".to_owned(),
            request_id: "already-timed-out".to_owned(),
            payload,
        };
        channel.handle_message(late).await;

        match tokio::time::timeout(Duration::from_secs(1), events.recv()).await {
            Ok(Ok(ChannelEvent::Message(m))) => assert_eq!(m.event, "reply"),
            other => panic!("expected late response surfaced as message event, got {other:?}"),
        }
    }

    #[tokio::test]
    async fn adv_duplicate_response_second_surfaces_as_message() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        let mut events = channel.subscribe_events();

        let asker = channel.clone();
        let handle = tokio::spawn(async move {
            asker
                .send_for_response("ask", None, Some(Duration::from_secs(30)))
                .await
        });
        let request_id = wait_for_pending(&channel).await;

        let mut first_payload = Map::new();
        first_payload.insert("n".to_owned(), Value::from(1));
        let first = ServerMessage {
            action: ServerAction::Broadcast,
            event: "reply".to_owned(),
            channel_name: "room".to_owned(),
            request_id: request_id.clone(),
            payload: first_payload,
        };
        channel.handle_message(first).await;

        let resolved = tokio::time::timeout(Duration::from_secs(2), handle)
            .await
            .expect("request hung")
            .unwrap()
            .unwrap();
        assert_eq!(resolved.get("n").and_then(Value::as_i64), Some(1));

        let mut second_payload = Map::new();
        second_payload.insert("n".to_owned(), Value::from(2));
        let second = ServerMessage {
            action: ServerAction::Broadcast,
            event: "reply".to_owned(),
            channel_name: "room".to_owned(),
            request_id,
            payload: second_payload,
        };
        channel.handle_message(second).await;

        match tokio::time::timeout(Duration::from_secs(1), events.recv()).await {
            Ok(Ok(ChannelEvent::Message(m))) => {
                assert_eq!(m.payload.get("n").and_then(Value::as_i64), Some(2))
            }
            other => panic!("expected duplicate response surfaced as message event, got {other:?}"),
        }
    }

    #[tokio::test]
    async fn adv_leave_cancels_inflight_request_with_error() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);

        let asker = channel.clone();
        let handle = tokio::spawn(async move {
            asker
                .send_for_response("ask", None, Some(Duration::from_secs(30)))
                .await
        });
        wait_for_pending(&channel).await;

        channel.leave().await;

        let result = tokio::time::timeout(Duration::from_secs(2), handle)
            .await
            .expect("request hung after leave")
            .unwrap();
        assert!(matches!(result, Err(ClientError::ChannelClosed)));
        assert_eq!(channel.state(), ChannelState::Closed);
    }

    #[tokio::test]
    async fn adv_double_leave_is_safe() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        channel.leave().await;
        channel.leave().await;
        assert_eq!(channel.state(), ChannelState::Closed);
    }

    #[tokio::test]
    async fn adv_not_found_on_joined_with_pending_now_resolves_request() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);

        let asker = channel.clone();
        let handle = tokio::spawn(async move {
            let start = tokio::time::Instant::now();
            let result = asker
                .send_for_response("no_handler", None, Some(Duration::from_secs(30)))
                .await;
            (start.elapsed(), result)
        });
        let request_id = wait_for_pending(&channel).await;

        let mut payload = Map::new();
        payload.insert("channel".to_owned(), Value::from("room"));
        payload.insert("event".to_owned(), Value::from("no_handler"));
        let not_found = ServerMessage {
            action: ServerAction::System,
            event: event_name(EventName::NotFound),
            channel_name: "room".to_owned(),
            request_id,
            payload,
        };
        channel.handle_message(not_found).await;

        let (elapsed, result) = tokio::time::timeout(Duration::from_secs(2), handle)
            .await
            .expect("request hung")
            .unwrap();
        let payload = result.expect("expected NOT_FOUND to resolve the pending request");
        assert_eq!(
            payload.get("event").and_then(Value::as_str),
            Some("no_handler")
        );
        assert!(elapsed < Duration::from_secs(5));
        assert_eq!(channel.state(), ChannelState::Joined);
    }

    #[tokio::test]
    async fn adv_stray_acknowledge_on_joined_is_consumed() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        let mut events = channel.subscribe_events();

        channel
            .handle_message(system_message(EventName::Acknowledge, "room", Map::new()))
            .await;

        assert_eq!(channel.state(), ChannelState::Joined);
        assert!(events.try_recv().is_err());
    }

    #[tokio::test]
    async fn adv_evicted_cancels_inflight_request_and_closes() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);

        let asker = channel.clone();
        let handle = tokio::spawn(async move {
            asker
                .send_for_response("ask", None, Some(Duration::from_secs(30)))
                .await
        });
        wait_for_pending(&channel).await;

        let mut payload = Map::new();
        payload.insert("reason".to_owned(), Value::from("kicked"));
        let evicted = ServerMessage {
            action: ServerAction::System,
            event: "EVICTED".to_owned(),
            channel_name: "room".to_owned(),
            request_id: uuid(),
            payload,
        };
        channel.handle_message(evicted).await;

        let result = tokio::time::timeout(Duration::from_secs(2), handle)
            .await
            .expect("request hung after eviction")
            .unwrap();
        assert!(matches!(result, Err(ClientError::ChannelClosed)));
        assert_eq!(channel.state(), ChannelState::Closed);
    }

    #[tokio::test]
    async fn adv_evicted_for_unknown_channel_is_safe() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let _present = client.create_channel("room", None).await;

        let mut payload = Map::new();
        payload.insert("reason".to_owned(), Value::from("kicked"));
        let evicted = ServerMessage {
            action: ServerAction::System,
            event: "EVICTED".to_owned(),
            channel_name: "ghost".to_owned(),
            request_id: uuid(),
            payload,
        };
        client
            .inner
            .route_event(ChannelEvent::Message(evicted))
            .await;

        assert_eq!(
            client.create_channel("ghost", None).await.state(),
            ChannelState::Idle
        );
    }

    #[test]
    fn rejects_zero_queue_capacity() {
        let options = ClientOptions {
            max_queue_size: 0,
            ..ClientOptions::default()
        };
        assert!(matches!(
            PondClient::with_options("ws://example.com/socket", None, options),
            Err(ClientError::InvalidQueueCapacity)
        ));
    }

    #[tokio::test]
    async fn queue_saturation_backpressures_without_dropping_oldest() {
        let options = ClientOptions {
            max_queue_size: 2,
            ..ClientOptions::default()
        };
        let client = PondClient::with_options("ws://example.com/socket", None, options).unwrap();
        let channel = client.create_channel("room", None).await;

        channel
            .send_message_checked(
                "ordered",
                Some(Map::from_iter([(String::from("n"), Value::from(1))])),
            )
            .await
            .unwrap();
        channel
            .send_message_checked(
                "ordered",
                Some(Map::from_iter([(String::from("n"), Value::from(2))])),
            )
            .await
            .unwrap();

        let blocked = channel.clone();
        let third = tokio::spawn(async move {
            blocked
                .send_message_checked(
                    "ordered",
                    Some(Map::from_iter([(String::from("n"), Value::from(3))])),
                )
                .await
        });
        tokio::time::sleep(Duration::from_millis(20)).await;
        assert!(!third.is_finished());

        let first = channel.inner.queue.lock().await.pop_front().unwrap();
        assert_eq!(first.payload.get("n").and_then(Value::as_i64), Some(1));
        channel.inner.queue_space.notify_waiters();
        third.await.unwrap().unwrap();

        let queue = channel.inner.queue.lock().await;
        let order: Vec<i64> = queue
            .iter()
            .map(|message| message.payload.get("n").and_then(Value::as_i64).unwrap())
            .collect();
        assert_eq!(order, vec![2, 3]);
    }

    #[tokio::test]
    async fn response_timeout_includes_time_waiting_for_queue_capacity() {
        let options = ClientOptions {
            max_queue_size: 1,
            response_timeout: Duration::from_millis(30),
            ..ClientOptions::default()
        };
        let client = PondClient::with_options("ws://example.com/socket", None, options).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.send_message("full", None).await;

        let started = tokio::time::Instant::now();
        let result = channel.send_for_response("ask", None, None).await;
        assert!(matches!(result, Err(ClientError::ResponseTimeout)));
        assert!(started.elapsed() < Duration::from_millis(250));
        assert!(channel.inner.pending.lock().await.is_empty());
        assert_eq!(channel.inner.queue.lock().await.len(), 1);
    }

    #[tokio::test]
    async fn stale_join_ack_and_decline_do_not_complete_new_attempt() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joining);
        *channel.inner.pending_join.lock().await = Some("new-attempt".to_owned());

        let stale_ack = ServerMessage {
            action: ServerAction::System,
            event: event_name(EventName::Acknowledge),
            channel_name: "room".to_owned(),
            request_id: "old-attempt".to_owned(),
            payload: Map::new(),
        };
        channel.handle_message(stale_ack).await;
        assert_eq!(channel.state(), ChannelState::Joining);

        let stale_decline = ServerMessage {
            action: ServerAction::System,
            event: event_name(EventName::Unauthorized),
            channel_name: "room".to_owned(),
            request_id: "old-attempt".to_owned(),
            payload: Map::new(),
        };
        channel.handle_message(stale_decline).await;
        assert_eq!(channel.state(), ChannelState::Joining);
        assert!(channel.decline_reason().await.is_none());

        let current_ack = ServerMessage {
            action: ServerAction::System,
            event: event_name(EventName::Acknowledge),
            channel_name: "room".to_owned(),
            request_id: "new-attempt".to_owned(),
            payload: Map::new(),
        };
        channel.handle_message(current_ack).await;
        assert_eq!(channel.state(), ChannelState::Joined);
    }

    #[tokio::test]
    async fn acknowledgement_flushes_queued_messages_before_new_sends() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let (tx, mut rx) = mpsc::channel(256);
        *client.inner.outbound.lock().await = Some(tx);
        client
            .inner
            .set_connection_state(ConnectionState::Connected);
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joining);
        *channel.inner.pending_join.lock().await = Some("join".to_owned());

        for n in 0..50 {
            channel
                .send_message_checked(
                    "ordered",
                    Some(Map::from_iter([(String::from("n"), Value::from(n))])),
                )
                .await
                .unwrap();
        }

        let guard = channel.inner.send_lock.lock().await;
        let acknowledging = channel.clone();
        let ack = tokio::spawn(async move {
            acknowledging
                .handle_message(ServerMessage {
                    action: ServerAction::System,
                    event: event_name(EventName::Acknowledge),
                    channel_name: "room".to_owned(),
                    request_id: "join".to_owned(),
                    payload: Map::new(),
                })
                .await;
        });
        tokio::task::yield_now().await;
        let sender = channel.clone();
        let after = tokio::spawn(async move {
            for n in 50..100 {
                sender
                    .send_message_checked(
                        "ordered",
                        Some(Map::from_iter([(String::from("n"), Value::from(n))])),
                    )
                    .await
                    .unwrap();
            }
        });
        drop(guard);
        ack.await.unwrap();
        after.await.unwrap();

        for expected in 0..100 {
            let message = tokio::time::timeout(Duration::from_secs(1), rx.recv())
                .await
                .unwrap()
                .unwrap();
            assert_eq!(
                message.payload.get("n").and_then(Value::as_i64),
                Some(expected)
            );
        }
    }

    #[tokio::test]
    async fn newline_frame_preserves_valid_event_order_and_skips_bad_line() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut socket = accept_async(stream).await.unwrap();
            tokio::time::sleep(Duration::from_millis(20)).await;
            let mut lines = Vec::new();
            for n in 0..250 {
                let message = ServerMessage {
                    action: ServerAction::Broadcast,
                    event: "ordered".to_owned(),
                    channel_name: "room".to_owned(),
                    request_id: n.to_string(),
                    payload: Map::from_iter([(String::from("n"), Value::from(n))]),
                };
                lines.push(serde_json::to_string(&message).unwrap());
                if n == 124 {
                    lines.push("{bad-json}".to_owned());
                }
            }
            socket
                .send(Message::Text(lines.join("\n").into()))
                .await
                .unwrap();
            tokio::time::sleep(Duration::from_millis(100)).await;
        });

        let client = PondClient::new(format!("ws://{addr}/socket"), None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        let mut events = channel.subscribe_events();
        client.connect().await.unwrap();

        for expected in 0..250 {
            let event = tokio::time::timeout(Duration::from_secs(2), events.recv())
                .await
                .unwrap()
                .unwrap();
            let ChannelEvent::Message(message) = event else {
                panic!("expected message event")
            };
            assert_eq!(message.request_id, expected.to_string());
        }
        client.disconnect().await;
        server.await.unwrap();
    }

    #[tokio::test]
    async fn broadcast_receiver_reports_lag_instead_of_silently_skipping() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        let mut events = channel.subscribe_events();

        for n in 0..150 {
            channel
                .handle_message(ServerMessage {
                    action: ServerAction::Broadcast,
                    event: "ordered".to_owned(),
                    channel_name: "room".to_owned(),
                    request_id: n.to_string(),
                    payload: Map::new(),
                })
                .await;
        }

        assert!(matches!(
            events.recv().await,
            Err(broadcast::error::RecvError::Lagged(_))
        ));
    }
}
