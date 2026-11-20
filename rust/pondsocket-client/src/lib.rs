use std::collections::{HashMap, VecDeque};
use std::sync::atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex as StdMutex, Weak};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use futures_util::{Sink, SinkExt, StreamExt};
use pondsocket_common::{
    ChannelEvent, ChannelState, ClientAction, ClientMessage, EventName, JoinParams, PondMessage,
    PondPresence, PresenceEventType, PresenceMessage, ServerAction, ServerMessage, uuid,
};
use serde_json::{Map, Value};
use thiserror::Error;
use tokio::sync::{Mutex, Notify, broadcast, mpsc, oneshot, watch};
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tokio_tungstenite::connect_async_with_config;
use tokio_tungstenite::tungstenite::protocol::WebSocketConfig;
use tokio_tungstenite::tungstenite::{Bytes, Message};
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
    pub join_timeout: Duration,
    pub max_queue_size: usize,
    pub reconnect_initial_delay: Duration,
    pub max_reconnect_delay: Duration,
    pub ping_interval: Option<Duration>,
    pub pong_timeout: Duration,
    pub max_message_size: usize,
    pub max_frame_size: usize,
}

impl Default for ClientOptions {
    fn default() -> Self {
        Self {
            connection_timeout: Duration::from_secs(10),
            response_timeout: Duration::from_secs(5),
            join_timeout: Duration::from_secs(10),
            max_queue_size: 100,
            reconnect_initial_delay: Duration::from_millis(250),
            max_reconnect_delay: Duration::from_secs(30),
            ping_interval: Some(Duration::from_secs(30)),
            pong_timeout: Duration::from_secs(60),
            max_message_size: 1024 * 1024,
            max_frame_size: 1024 * 1024,
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
    #[error("channel join timed out")]
    JoinTimeout,
    #[error("connection lost: {0}")]
    ConnectionLost(String),
    #[error("protocol error: {0}")]
    Protocol(String),
    #[error("max_queue_size must be greater than zero")]
    InvalidQueueCapacity,
    #[error("invalid client option: {0}")]
    InvalidOption(String),
}

type Result<T> = std::result::Result<T, ClientError>;

pub struct PondClient {
    inner: Arc<ClientInner>,
}

struct ClientInner {
    url: String,
    options: ClientOptions,
    state: watch::Sender<ConnectionState>,
    errors: broadcast::Sender<Arc<ClientError>>,
    channels: Mutex<HashMap<String, Channel>>,
    outbound: Mutex<Option<mpsc::Sender<ClientMessage>>>,
    read_task: StdMutex<Option<JoinHandle<()>>>,
    write_task: StdMutex<Option<JoinHandle<()>>>,
    reconnect_task: StdMutex<Option<JoinHandle<()>>>,
    lifecycle_lock: Mutex<()>,
    desired_connected: AtomicBool,
    reconnecting: AtomicBool,
    generation: AtomicU64,
    owners: AtomicUsize,
    liveness: StdMutex<LivenessState>,
    liveness_changed: Notify,
    cancel: Notify,
    #[cfg(test)]
    connect_install_hook: StdMutex<Option<(Arc<Notify>, Arc<Notify>)>>,
}

#[derive(Debug)]
struct LivenessState {
    last_inbound: Instant,
    outstanding_ping: Option<Instant>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum HeartbeatAction {
    Wait,
    SendPing,
    TimedOut,
}

impl LivenessState {
    fn new(now: Instant) -> Self {
        Self {
            last_inbound: now,
            outstanding_ping: None,
        }
    }

    fn deadline(&self, ping_interval: Duration, pong_timeout: Duration) -> Instant {
        self.outstanding_ping
            .map_or(self.last_inbound + ping_interval, |ping_sent_at| {
                ping_sent_at + pong_timeout
            })
    }

    fn action(
        &self,
        now: Instant,
        ping_interval: Duration,
        pong_timeout: Duration,
    ) -> HeartbeatAction {
        if let Some(ping_sent_at) = self.outstanding_ping {
            if now >= ping_sent_at + pong_timeout {
                HeartbeatAction::TimedOut
            } else {
                HeartbeatAction::Wait
            }
        } else if now >= self.last_inbound + ping_interval {
            HeartbeatAction::SendPing
        } else {
            HeartbeatAction::Wait
        }
    }

    fn ping_sent(&mut self, now: Instant) {
        self.outstanding_ping = Some(now);
    }

    fn inbound_received(&mut self, now: Instant) {
        self.last_inbound = now;
        // Any inbound frame proves the peer is alive, not only an explicit
        // Pong. Active application traffic therefore clears an outstanding
        // probe as well.
        self.outstanding_ping = None;
    }
}

struct ConnectAttemptGuard {
    inner: Arc<ClientInner>,
    armed: bool,
}

impl ConnectAttemptGuard {
    fn new(inner: &Arc<ClientInner>) -> Self {
        Self {
            inner: Arc::clone(inner),
            armed: true,
        }
    }

    fn finish(&mut self) {
        self.armed = false;
    }
}

impl Drop for ConnectAttemptGuard {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }
        self.inner.generation.fetch_add(1, Ordering::AcqRel);
        self.inner.abort_io_tasks();
        if let Ok(mut outbound) = self.inner.outbound.try_lock() {
            *outbound = None;
        }
        self.inner
            .set_connection_state(ConnectionState::Disconnected);
        self.inner.schedule_reconnect();
    }
}

struct PendingRequestGuard {
    inner: Arc<ChannelInner>,
    request_id: String,
}

impl PendingRequestGuard {
    fn new(inner: &Arc<ChannelInner>, request_id: String) -> Self {
        Self {
            inner: Arc::clone(inner),
            request_id,
        }
    }
}

impl Drop for PendingRequestGuard {
    fn drop(&mut self) {
        self.inner
            .pending
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .remove(&self.request_id);
    }
}

struct AckFlushGuard {
    inner: Arc<ChannelInner>,
    armed: bool,
}

struct ReconnectCancellationGuard {
    inner: Arc<ClientInner>,
    armed: bool,
}

impl ReconnectCancellationGuard {
    fn new(inner: &Arc<ClientInner>) -> Self {
        Self {
            inner: Arc::clone(inner),
            armed: true,
        }
    }

    fn finish(&mut self) {
        self.armed = false;
    }
}

impl Drop for ReconnectCancellationGuard {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }
        self.inner.reconnecting.store(false, Ordering::Release);
        if self.inner.desired_connected.load(Ordering::Acquire)
            && self.inner.state() == ConnectionState::Disconnected
        {
            self.inner.schedule_reconnect();
        }
    }
}

impl AckFlushGuard {
    fn new(inner: &Arc<ChannelInner>) -> Self {
        Self {
            inner: Arc::clone(inner),
            armed: true,
        }
    }

    fn finish(&mut self) {
        self.armed = false;
    }
}

impl Drop for AckFlushGuard {
    fn drop(&mut self) {
        if self.armed && *self.inner.state.borrow() == ChannelState::Joined {
            self.inner.state.send_replace(ChannelState::Stalled);
            self.inner.queue_space.notify_waiters();
        }
    }
}

#[derive(Clone)]
pub struct Channel {
    inner: Arc<ChannelInner>,
}

struct ChannelInner {
    name: String,
    params: JoinParams,
    client: Weak<ClientInner>,
    state: watch::Sender<ChannelState>,
    events: broadcast::Sender<ChannelEvent>,
    presence: Mutex<Vec<PondPresence>>,
    queue: Mutex<VecDeque<ClientMessage>>,
    queue_space: Notify,
    send_lock: Mutex<()>,
    pending: StdMutex<HashMap<String, oneshot::Sender<Result<PondMessage>>>>,
    pending_join: Mutex<Option<String>>,
    join_changed: Notify,
    decline_reason: Mutex<Option<PondMessage>>,
    join_task: StdMutex<Option<JoinHandle<()>>>,
    closed: Mutex<bool>,
}

impl Clone for PondClient {
    fn clone(&self) -> Self {
        self.inner.owners.fetch_add(1, Ordering::Relaxed);
        Self {
            inner: Arc::clone(&self.inner),
        }
    }
}

impl Drop for PondClient {
    fn drop(&mut self) {
        if self.inner.owners.fetch_sub(1, Ordering::AcqRel) != 1 {
            return;
        }
        self.inner.desired_connected.store(false, Ordering::Release);
        self.inner.generation.fetch_add(1, Ordering::AcqRel);
        self.inner.cancel.notify_waiters();
        self.inner.abort_background_tasks();
        if let Ok(mut outbound) = self.inner.outbound.try_lock() {
            *outbound = None;
        }
        self.inner
            .set_connection_state(ConnectionState::Disconnected);
    }
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
        if options.connection_timeout.is_zero()
            || options.response_timeout.is_zero()
            || options.join_timeout.is_zero()
            || options.reconnect_initial_delay.is_zero()
            || options.max_reconnect_delay < options.reconnect_initial_delay
            || options
                .ping_interval
                .is_some_and(|duration| duration.is_zero())
            || options.pong_timeout.is_zero()
            || options.max_message_size == 0
            || options.max_frame_size == 0
        {
            return Err(ClientError::InvalidOption(
                "timeouts and message limits must be non-zero, and max reconnect delay must not be smaller than the initial delay"
                    .to_owned(),
            ));
        }
        let url = resolve_url(endpoint.as_ref(), params.as_ref())?;
        let (state, _) = watch::channel(ConnectionState::Disconnected);
        let (errors, _) = broadcast::channel(64);

        Ok(Self {
            inner: Arc::new(ClientInner {
                url,
                options,
                state,
                errors,
                channels: Mutex::new(HashMap::new()),
                outbound: Mutex::new(None),
                read_task: StdMutex::new(None),
                write_task: StdMutex::new(None),
                reconnect_task: StdMutex::new(None),
                lifecycle_lock: Mutex::new(()),
                desired_connected: AtomicBool::new(false),
                reconnecting: AtomicBool::new(false),
                generation: AtomicU64::new(0),
                owners: AtomicUsize::new(1),
                liveness: StdMutex::new(LivenessState::new(Instant::now())),
                liveness_changed: Notify::new(),
                cancel: Notify::new(),
                #[cfg(test)]
                connect_install_hook: StdMutex::new(None),
            }),
        })
    }

    pub fn state(&self) -> ConnectionState {
        *self.inner.state.borrow()
    }

    pub fn subscribe_state(&self) -> watch::Receiver<ConnectionState> {
        self.inner.state.subscribe()
    }

    pub fn subscribe_errors(&self) -> broadcast::Receiver<Arc<ClientError>> {
        self.inner.errors.subscribe()
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
                client: Arc::downgrade(&self.inner),
                state,
                events,
                presence: Mutex::new(Vec::new()),
                queue: Mutex::new(VecDeque::new()),
                queue_space: Notify::new(),
                send_lock: Mutex::new(()),
                pending: StdMutex::new(HashMap::new()),
                pending_join: Mutex::new(None),
                join_changed: Notify::new(),
                decline_reason: Mutex::new(None),
                join_task: StdMutex::new(None),
                closed: Mutex::new(false),
            }),
        };

        channels.insert(name, channel.clone());
        channel
    }

    pub async fn connect(&self) -> Result<()> {
        self.inner.desired_connected.store(true, Ordering::Release);
        self.inner.cancel_reconnect_task().await;
        let result = self.inner.connect_once().await;
        if let Err(error) = &result {
            self.inner.emit_error(Arc::new(clone_error(error)));
            self.inner.schedule_reconnect();
        }
        result
    }

    pub async fn disconnect(&self) {
        self.inner.desired_connected.store(false, Ordering::Release);
        self.inner.generation.fetch_add(1, Ordering::AcqRel);
        self.inner.cancel.notify_waiters();
        self.inner.abort_background_tasks();
        let _lifecycle = self.inner.lifecycle_lock.lock().await;
        // A connector that was already past its final desired-state check can
        // install new IO tasks while disconnect waits for this lock. Invalidate
        // and abort once more inside the serialized lifecycle section.
        self.inner.generation.fetch_add(1, Ordering::AcqRel);
        self.inner.abort_background_tasks();
        *self.inner.outbound.lock().await = None;
        self.inner
            .set_connection_state(ConnectionState::Disconnected);
        self.inner
            .notify_channels(ConnectionState::Disconnected)
            .await;
        let channels: Vec<Channel> = self.inner.channels.lock().await.values().cloned().collect();
        for channel in channels {
            channel.force_close().await;
        }
        self.inner.channels.lock().await.clear();
    }
}

impl ClientInner {
    async fn connect_once(self: &Arc<Self>) -> Result<()> {
        let _lifecycle = self.lifecycle_lock.lock().await;
        // Register before checking the desired state so a concurrent
        // disconnect cannot notify between the check and waiter creation.
        let cancelled = self.cancel.notified();
        tokio::pin!(cancelled);
        cancelled.as_mut().enable();
        if !self.desired_connected.load(Ordering::Acquire) {
            return Err(ClientError::NotConnected);
        }
        if self.state() != ConnectionState::Disconnected {
            return Ok(());
        }
        self.set_connection_state(ConnectionState::Connecting);
        let mut attempt = ConnectAttemptGuard::new(self);
        let config = WebSocketConfig::default()
            .max_message_size(Some(self.options.max_message_size))
            .max_frame_size(Some(self.options.max_frame_size));
        let connect = connect_async_with_config(&self.url, Some(config), false);
        let connection = tokio::select! {
            connection = tokio::time::timeout(self.options.connection_timeout, connect) => connection,
            () = &mut cancelled => {
                self.set_connection_state(ConnectionState::Disconnected);
                self.notify_channels(ConnectionState::Disconnected).await;
                return Err(ClientError::NotConnected);
            }
        };
        let (socket, _) = match connection {
            Ok(Ok(connection)) => connection,
            Ok(Err(error)) => {
                self.set_connection_state(ConnectionState::Disconnected);
                self.notify_channels(ConnectionState::Disconnected).await;
                return Err(ClientError::WebSocket(error));
            }
            Err(_) => {
                self.set_connection_state(ConnectionState::Disconnected);
                self.notify_channels(ConnectionState::Disconnected).await;
                return Err(ClientError::ConnectionTimeout);
            }
        };
        if !self.desired_connected.load(Ordering::Acquire) {
            return Err(ClientError::NotConnected);
        }

        #[cfg(test)]
        let connect_install_hook = self
            .connect_install_hook
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .take();
        #[cfg(test)]
        if let Some((reached, resume)) = connect_install_hook {
            reached.notify_one();
            resume.notified().await;
        }

        self.abort_io_tasks();
        let generation = self.generation.fetch_add(1, Ordering::AcqRel) + 1;
        let (mut writer, mut reader) = socket.split();
        let (tx, mut rx) = mpsc::channel::<ClientMessage>(self.options.max_queue_size);
        *self.outbound.lock().await = Some(tx);
        *self
            .liveness
            .lock()
            .unwrap_or_else(|error| error.into_inner()) = LivenessState::new(Instant::now());
        self.set_connection_state(ConnectionState::Connected);

        let write_inner = Arc::clone(self);
        let write_task = tokio::spawn(async move {
            let mut terminal_error = None;
            loop {
                let liveness_changed = write_inner.liveness_changed.notified();
                tokio::pin!(liveness_changed);
                liveness_changed.as_mut().enable();
                let heartbeat_deadline = write_inner.options.ping_interval.map(|ping_interval| {
                    write_inner
                        .liveness
                        .lock()
                        .unwrap_or_else(|error| error.into_inner())
                        .deadline(ping_interval, write_inner.options.pong_timeout)
                });
                tokio::select! {
                    next = rx.recv() => {
                        let Some(message) = next else { break; };
                        let text = match serde_json::to_string(&message) {
                            Ok(text) => text,
                            Err(error) => {
                                write_inner.emit_error(Arc::new(ClientError::Serialization(error)));
                                continue;
                            }
                        };
                        let send_result = if message.action == ClientAction::JoinChannel {
                            let channel = write_inner
                                .channels
                                .lock()
                                .await
                                .get(&message.channel_name)
                                .cloned();
                            let Some(channel) = channel else {
                                continue;
                            };
                            let join_changed = channel.inner.join_changed.notified();
                            tokio::pin!(join_changed);
                            join_changed.as_mut().enable();
                            if !channel.is_current_join(&message.request_id).await {
                                continue;
                            }
                            tokio::select! {
                                biased;
                                () = &mut join_changed => continue,
                                result = send_websocket_frame(
                                    &mut writer,
                                    Message::Text(text.into()),
                                    write_inner.options.connection_timeout,
                                ) => result,
                            }
                        } else {
                            send_websocket_frame(
                                &mut writer,
                                Message::Text(text.into()),
                                write_inner.options.connection_timeout,
                            ).await
                        };
                        if let Err(error) = send_result {
                            terminal_error = Some(error);
                            break;
                        }
                    }
                    () = &mut liveness_changed => {}
                    _ = async {
                        match heartbeat_deadline {
                            Some(deadline) => tokio::time::sleep_until(deadline).await,
                            None => std::future::pending().await,
                        }
                    } => {
                        let action = write_inner
                            .liveness
                            .lock()
                            .unwrap_or_else(|error| error.into_inner())
                            .action(
                                Instant::now(),
                                write_inner.options.ping_interval.expect("deadline requires interval"),
                                write_inner.options.pong_timeout,
                            );
                        match action {
                            HeartbeatAction::Wait => {}
                            HeartbeatAction::TimedOut => {
                                terminal_error = Some(ClientError::ConnectionLost(
                                    "server did not respond before the pong timeout".to_owned(),
                                ));
                                break;
                            }
                            HeartbeatAction::SendPing => {
                                // Mark the probe before writing so an extremely
                                // fast Pong cannot clear it and then be
                                // overwritten after send() returns.
                                write_inner
                                    .liveness
                                    .lock()
                                    .unwrap_or_else(|error| error.into_inner())
                                    .ping_sent(Instant::now());
                                if let Err(error) = send_websocket_frame(
                                    &mut writer,
                                    Message::Ping(Bytes::new()),
                                    write_inner.options.connection_timeout,
                                ).await {
                                    terminal_error = Some(error);
                                    break;
                                }
                                let mut liveness = write_inner
                                    .liveness
                                    .lock()
                                    .unwrap_or_else(|error| error.into_inner());
                                if liveness.outstanding_ping.is_some() {
                                    liveness.ping_sent(Instant::now());
                                }
                            }
                        }
                    }
                }
            }
            if let Err(error) =
                close_websocket_writer(&mut writer, write_inner.options.connection_timeout).await
                && terminal_error.is_none()
            {
                terminal_error = Some(error);
            }
            if let Some(error) = terminal_error {
                write_inner.connection_lost(generation, Some(error)).await;
            }
        });

        let read_inner = Arc::clone(self);
        let read_task = tokio::spawn(async move {
            let mut terminal_error = None;
            while let Some(frame) = reader.next().await {
                if read_inner.generation.load(Ordering::Acquire) != generation {
                    return;
                }
                let message = match frame {
                    Ok(message) => {
                        read_inner
                            .liveness
                            .lock()
                            .unwrap_or_else(|error| error.into_inner())
                            .inbound_received(Instant::now());
                        read_inner.liveness_changed.notify_one();
                        message
                    }
                    Err(error) => {
                        terminal_error = Some(ClientError::WebSocket(error));
                        break;
                    }
                };
                let text = match message {
                    Message::Text(text) => Some(text.to_string()),
                    Message::Binary(bytes) => match String::from_utf8(bytes.to_vec()) {
                        Ok(text) => Some(text),
                        Err(error) => {
                            read_inner.emit_error(Arc::new(ClientError::Protocol(format!(
                                "binary message is not UTF-8: {error}"
                            ))));
                            None
                        }
                    },
                    Message::Close(frame) => {
                        terminal_error = Some(ClientError::ConnectionLost(format!(
                            "peer closed the websocket{}",
                            frame.map_or_else(String::new, |frame| format!(": {}", frame.reason))
                        )));
                        break;
                    }
                    _ => None,
                };
                let Some(text) = text else {
                    continue;
                };
                for (line_number, line) in text
                    .lines()
                    .map(str::trim)
                    .filter(|line| !line.is_empty())
                    .enumerate()
                {
                    match pondsocket_common::parse_channel_event(line) {
                        Ok(event) => read_inner.route_event(event).await,
                        Err(error) => {
                            read_inner.emit_error(Arc::new(ClientError::Protocol(format!(
                                "invalid server message at line {}: {error}",
                                line_number + 1
                            ))))
                        }
                    }
                }
            }
            read_inner.connection_lost(generation, terminal_error).await;
        });

        *self.write_task.lock().unwrap_or_else(|e| e.into_inner()) = Some(write_task);
        *self.read_task.lock().unwrap_or_else(|e| e.into_inner()) = Some(read_task);
        attempt.finish();
        if self.generation.load(Ordering::Acquire) == generation
            && self.state() == ConnectionState::Connected
        {
            self.notify_channels(ConnectionState::Connected).await;
        }
        Ok(())
    }

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

    fn state(&self) -> ConnectionState {
        *self.state.borrow()
    }

    fn set_connection_state(&self, state: ConnectionState) {
        self.state.send_replace(state);
    }

    fn emit_error(&self, error: Arc<ClientError>) {
        let _ = self.errors.send(error);
    }

    async fn notify_channels(&self, state: ConnectionState) {
        let channels = self
            .channels
            .lock()
            .await
            .values()
            .cloned()
            .collect::<Vec<_>>();
        for channel in channels {
            channel.on_connection_change(state).await;
        }
    }

    async fn connection_lost(self: &Arc<Self>, generation: u64, error: Option<ClientError>) {
        if self
            .generation
            .compare_exchange(
                generation,
                generation.saturating_add(1),
                Ordering::AcqRel,
                Ordering::Acquire,
            )
            .is_err()
        {
            return;
        }
        *self.outbound.lock().await = None;
        self.set_connection_state(ConnectionState::Disconnected);
        self.notify_channels(ConnectionState::Disconnected).await;
        if let Some(error) = error {
            self.emit_error(Arc::new(error));
        }
        self.schedule_reconnect();
    }

    fn schedule_reconnect(self: &Arc<Self>) {
        if !self.desired_connected.load(Ordering::Acquire)
            || self
                .reconnecting
                .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
                .is_err()
        {
            return;
        }
        let inner = Arc::clone(self);
        let task = tokio::spawn(async move {
            let mut attempt = 0_u32;
            while inner.desired_connected.load(Ordering::Acquire) {
                let delay = reconnect_delay(&inner.options, attempt);
                tokio::time::sleep(delay).await;
                if !inner.desired_connected.load(Ordering::Acquire) {
                    break;
                }
                match inner.connect_once().await {
                    Ok(()) if inner.state() == ConnectionState::Connected => break,
                    Ok(()) => {}
                    Err(error) => inner.emit_error(Arc::new(error)),
                }
                attempt = attempt.saturating_add(1);
            }
            inner.reconnecting.store(false, Ordering::Release);
            // A connection can disappear after the success check above but
            // before reconnecting is cleared. Re-arm here so that loss is not
            // stranded by that small hand-off window.
            if inner.desired_connected.load(Ordering::Acquire)
                && inner.state() == ConnectionState::Disconnected
            {
                inner.schedule_reconnect();
            }
        });
        *self
            .reconnect_task
            .lock()
            .unwrap_or_else(|e| e.into_inner()) = Some(task);
    }

    async fn remove_channel(&self, name: &str, expected: &Arc<ChannelInner>) {
        let mut channels = self.channels.lock().await;
        if channels
            .get(name)
            .is_some_and(|channel| Arc::ptr_eq(&channel.inner, expected))
        {
            channels.remove(name);
        }
    }

    fn abort_io_tasks(&self) {
        abort_task(&self.read_task);
        abort_task(&self.write_task);
    }

    fn abort_reconnect_task(&self) {
        abort_task(&self.reconnect_task);
    }

    async fn cancel_reconnect_task(self: &Arc<Self>) {
        let mut cancellation = ReconnectCancellationGuard::new(self);
        let task = self
            .reconnect_task
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .take();
        if let Some(task) = task {
            task.abort();
            let _ = task.await;
        }
        self.reconnecting.store(false, Ordering::Release);
        cancellation.finish();
    }

    fn abort_background_tasks(&self) {
        self.abort_reconnect_task();
        self.abort_io_tasks();
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
        let request_id = message.request_id.clone();
        *self.inner.pending_join.lock().await = Some(request_id.clone());
        self.inner.state.send_replace(ChannelState::Joining);
        let join_changed = self.inner.join_changed.notified();
        tokio::pin!(join_changed);
        join_changed.as_mut().enable();
        // Arm the timeout before enqueue/publish, both of which can apply
        // backpressure. If the caller cancels join while blocked, the channel
        // still leaves Joining deterministically.
        self.schedule_join_timeout(request_id.clone());
        let enqueue_result = tokio::select! {
            biased;
            () = &mut join_changed => return,
            result = self.enqueue_or_send(message) => result,
        };
        match enqueue_result {
            Ok(()) => {}
            Err(error) => {
                let mut pending_join = self.inner.pending_join.lock().await;
                if pending_join.as_deref() != Some(request_id.as_str()) {
                    return;
                }
                *pending_join = None;
                drop(pending_join);
                self.inner.state.send_replace(ChannelState::Stalled);
                self.abort_join_task();
                if let Ok(client) = self.client() {
                    client.emit_error(Arc::new(error));
                }
            }
        }
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
        if let Ok(client) = self.client() {
            let _ = client.publish(message).await;
        }
        self.force_close().await;
    }

    pub async fn send_message(&self, event: impl Into<String>, payload: Option<PondMessage>) {
        if let Err(error) = self.send_message_checked(event, payload).await
            && let Ok(client) = self.client()
        {
            client.emit_error(Arc::new(error));
        }
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
            .unwrap_or_else(|error| error.into_inner())
            .insert(request_id.clone(), tx);
        let _pending_guard = PendingRequestGuard::new(&self.inner, request_id.clone());
        let message = ClientMessage {
            action: ClientAction::Broadcast,
            event: event.into(),
            payload: payload.unwrap_or_default(),
            channel_name: self.inner.name.clone(),
            request_id: request_id.clone(),
        };
        let timeout = timeout.unwrap_or(self.client()?.options.response_timeout);
        let started = tokio::time::Instant::now();
        let queued = tokio::time::timeout(timeout, self.enqueue_or_send(message)).await;
        match queued {
            Ok(Ok(())) => {}
            Ok(Err(error)) => return Err(error),
            Err(_) => return Err(ClientError::ResponseTimeout),
        }
        let remaining = timeout.saturating_sub(started.elapsed());
        let result = tokio::time::timeout(remaining, rx).await;
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
            let client = self.client()?;
            let connected = client.state() == ConnectionState::Connected;
            let joined = self.state() == ChannelState::Joined;
            let is_join = message.action == ClientAction::JoinChannel;
            if connected && (joined || is_join) {
                match client.publish(message.clone()).await {
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
            if queue.len() < client.options.max_queue_size {
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
                self.abort_join_task();
                *self.inner.pending_join.lock().await = None;
                self.inner.join_changed.notify_waiters();
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
                self.inner.join_changed.notify_waiters();
                self.abort_join_task();
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
        if let Some(tx) = self
            .inner
            .pending
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .remove(&message.request_id)
        {
            let _ = tx.send(Ok(message.payload));
            return;
        }
        if self.state() == ChannelState::Joined {
            let _ = self.inner.events.send(ChannelEvent::Message(message));
        }
    }

    async fn acknowledge(&self) {
        let _send_guard = self.inner.send_lock.lock().await;
        let Ok(client) = self.client() else {
            return;
        };
        let mut queue = self.inner.queue.lock().await;
        queue.retain(|message| message.action != ClientAction::JoinChannel);
        self.inner.state.send_replace(ChannelState::Joined);
        let mut flush = AckFlushGuard::new(&self.inner);
        while let Some(message) = queue.front().cloned() {
            if client.publish(message.clone()).await.is_err() {
                break;
            }
            queue.pop_front();
            self.inner.queue_space.notify_one();
        }
        if queue.is_empty() {
            flush.finish();
        }
        drop(queue);
        self.inner.queue_space.notify_waiters();
    }

    async fn decline(&self, reason: PondMessage) {
        self.abort_join_task();
        self.inner.join_changed.notify_waiters();
        *self.inner.decline_reason.lock().await = Some(reason);
        self.inner.state.send_replace(ChannelState::Declined);
        self.inner.queue.lock().await.clear();
        self.inner.queue_space.notify_waiters();
        let pending = std::mem::take(
            &mut *self
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner()),
        );
        for (_, response) in pending {
            let _ = response.send(Err(ClientError::ChannelDeclined));
        }
        if let Some(client) = self.inner.client.upgrade() {
            client.remove_channel(&self.inner.name, &self.inner).await;
        }
    }

    async fn force_close(&self) {
        self.abort_join_task();
        *self.inner.closed.lock().await = true;
        self.inner.state.send_replace(ChannelState::Closed);
        self.inner.queue.lock().await.clear();
        self.inner.queue_space.notify_waiters();
        let pending = std::mem::take(
            &mut *self
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner()),
        );
        for (_, response) in pending {
            let _ = response.send(Err(ClientError::ChannelClosed));
        }
        *self.inner.pending_join.lock().await = None;
        self.inner.join_changed.notify_waiters();
        if let Some(client) = self.inner.client.upgrade() {
            client.remove_channel(&self.inner.name, &self.inner).await;
        }
    }

    fn client(&self) -> Result<Arc<ClientInner>> {
        self.inner
            .client
            .upgrade()
            .ok_or(ClientError::ChannelClosed)
    }

    fn schedule_join_timeout(&self, request_id: String) {
        self.abort_join_task();
        let weak = Arc::downgrade(&self.inner);
        let timeout = self
            .inner
            .client
            .upgrade()
            .map_or(Duration::from_secs(10), |client| {
                client.options.join_timeout
            });
        let task = tokio::spawn(async move {
            tokio::time::sleep(timeout).await;
            let Some(inner) = weak.upgrade() else {
                return;
            };
            let channel = Channel { inner };
            channel.join_timed_out(&request_id).await;
        });
        *self
            .inner
            .join_task
            .lock()
            .unwrap_or_else(|e| e.into_inner()) = Some(task);
    }

    async fn join_timed_out(&self, request_id: &str) {
        let mut pending_join = self.inner.pending_join.lock().await;
        if pending_join.as_deref() != Some(request_id) {
            return;
        }
        *pending_join = None;
        drop(pending_join);
        self.inner.join_changed.notify_waiters();
        self.inner.state.send_replace(ChannelState::Declined);
        let mut reason = Map::new();
        reason.insert(
            "message".to_owned(),
            Value::String("channel join timed out".to_owned()),
        );
        reason.insert("status".to_owned(), Value::from(504));
        *self.inner.decline_reason.lock().await = Some(reason);
        self.inner.queue.lock().await.clear();
        self.inner.queue_space.notify_waiters();
        let pending = std::mem::take(
            &mut *self
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner()),
        );
        for (_, response) in pending {
            let _ = response.send(Err(ClientError::JoinTimeout));
        }
        if let Some(client) = self.inner.client.upgrade() {
            client.emit_error(Arc::new(ClientError::JoinTimeout));
            client.remove_channel(&self.inner.name, &self.inner).await;
        }
    }

    fn abort_join_task(&self) {
        abort_task(&self.inner.join_task);
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
        self.inner.join_changed.notify_waiters();
        true
    }

    async fn is_current_join(&self, request_id: &str) -> bool {
        self.inner.pending_join.lock().await.as_deref() == Some(request_id)
    }
}

fn abort_task(slot: &StdMutex<Option<JoinHandle<()>>>) {
    if let Some(task) = slot.lock().unwrap_or_else(|e| e.into_inner()).take() {
        task.abort();
    }
}

async fn send_websocket_frame<S>(writer: &mut S, message: Message, timeout: Duration) -> Result<()>
where
    S: Sink<Message, Error = tokio_tungstenite::tungstenite::Error> + Unpin,
{
    match tokio::time::timeout(timeout, writer.send(message)).await {
        Ok(Ok(())) => Ok(()),
        Ok(Err(error)) => Err(ClientError::WebSocket(error)),
        Err(_) => Err(ClientError::ConnectionLost(
            "websocket write timed out".to_owned(),
        )),
    }
}

async fn close_websocket_writer<S>(writer: &mut S, timeout: Duration) -> Result<()>
where
    S: Sink<Message, Error = tokio_tungstenite::tungstenite::Error> + Unpin,
{
    match tokio::time::timeout(timeout, writer.close()).await {
        Ok(Ok(())) => Ok(()),
        Ok(Err(error)) => Err(ClientError::WebSocket(error)),
        Err(_) => Err(ClientError::ConnectionLost(
            "websocket close timed out".to_owned(),
        )),
    }
}

fn reconnect_delay(options: &ClientOptions, attempt: u32) -> Duration {
    let multiplier = 2_f64.powi(attempt.min(20) as i32);
    let ceiling = options
        .reconnect_initial_delay
        .mul_f64(multiplier)
        .min(options.max_reconnect_delay);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.subsec_nanos());
    let jitter = 0.5 + (f64::from(nanos) / f64::from(u32::MAX)) * 0.5;
    ceiling.mul_f64(jitter)
}

fn clone_error(error: &ClientError) -> ClientError {
    match error {
        ClientError::Url(error) => ClientError::Protocol(error.to_string()),
        ClientError::UnsupportedScheme(scheme) => ClientError::UnsupportedScheme(scheme.clone()),
        ClientError::WebSocket(error) => ClientError::ConnectionLost(error.to_string()),
        ClientError::Serialization(error) => ClientError::Protocol(error.to_string()),
        ClientError::ConnectionTimeout => ClientError::ConnectionTimeout,
        ClientError::NotConnected => ClientError::NotConnected,
        ClientError::ChannelClosed => ClientError::ChannelClosed,
        ClientError::ChannelDeclined => ClientError::ChannelDeclined,
        ClientError::ResponseTimeout => ClientError::ResponseTimeout,
        ClientError::JoinTimeout => ClientError::JoinTimeout,
        ClientError::ConnectionLost(message) => ClientError::ConnectionLost(message.clone()),
        ClientError::Protocol(message) => ClientError::Protocol(message.clone()),
        ClientError::InvalidQueueCapacity => ClientError::InvalidQueueCapacity,
        ClientError::InvalidOption(message) => ClientError::InvalidOption(message.clone()),
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

    #[tokio::test(start_paused = true)]
    async fn pong_or_other_inbound_frame_clears_the_outstanding_probe() {
        let ping_interval = Duration::from_secs(30);
        let pong_timeout = Duration::from_secs(5);
        let mut liveness = LivenessState::new(Instant::now());

        tokio::time::advance(ping_interval).await;
        assert_eq!(
            liveness.action(Instant::now(), ping_interval, pong_timeout),
            HeartbeatAction::SendPing
        );
        liveness.ping_sent(Instant::now());

        tokio::time::advance(Duration::from_secs(2)).await;
        liveness.inbound_received(Instant::now());
        tokio::time::advance(Duration::from_secs(4)).await;

        assert_eq!(
            liveness.action(Instant::now(), ping_interval, pong_timeout),
            HeartbeatAction::Wait,
            "inbound activity must clear the old pong deadline"
        );
    }

    #[tokio::test(start_paused = true)]
    async fn outstanding_ping_times_out_only_after_its_own_deadline() {
        let ping_interval = Duration::from_secs(30);
        let pong_timeout = Duration::from_secs(5);
        let mut liveness = LivenessState::new(Instant::now());

        tokio::time::advance(ping_interval).await;
        assert_eq!(
            liveness.action(Instant::now(), ping_interval, pong_timeout),
            HeartbeatAction::SendPing
        );
        liveness.ping_sent(Instant::now());
        tokio::time::advance(pong_timeout).await;

        assert_eq!(
            liveness.action(Instant::now(), ping_interval, pong_timeout),
            HeartbeatAction::TimedOut
        );
    }

    #[tokio::test(start_paused = true)]
    async fn pong_timeout_shorter_than_ping_interval_never_times_out_before_probe() {
        let ping_interval = Duration::from_secs(30);
        let pong_timeout = Duration::from_secs(5);
        PondClient::with_options(
            "ws://example.com/socket",
            None,
            ClientOptions {
                ping_interval: Some(ping_interval),
                pong_timeout,
                ..ClientOptions::default()
            },
        )
        .expect("a short pong deadline is valid because it starts after the ping is sent");
        let liveness = LivenessState::new(Instant::now());

        tokio::time::advance(pong_timeout).await;
        assert_eq!(
            liveness.action(Instant::now(), ping_interval, pong_timeout),
            HeartbeatAction::Wait
        );
        tokio::time::advance(ping_interval - pong_timeout).await;
        assert_eq!(
            liveness.action(Instant::now(), ping_interval, pong_timeout),
            HeartbeatAction::SendPing
        );
    }

    #[test]
    fn rejects_zero_heartbeat_durations() {
        for options in [
            ClientOptions {
                ping_interval: Some(Duration::ZERO),
                ..ClientOptions::default()
            },
            ClientOptions {
                pong_timeout: Duration::ZERO,
                ..ClientOptions::default()
            },
        ] {
            assert!(matches!(
                PondClient::with_options("ws://example.com/socket", None, options),
                Err(ClientError::InvalidOption(_))
            ));
        }
    }

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
        let client = PondClient::with_options(
            &url,
            None,
            ClientOptions {
                reconnect_initial_delay: Duration::from_millis(10),
                max_reconnect_delay: Duration::from_millis(25),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        client.connect().await.unwrap();
        let channel = client.create_channel("room", None).await;
        let mut events = channel.subscribe_events();
        channel.join().await;

        wait_for_state(&channel, ChannelState::Joined).await;
        assert_eq!(recv_greeting(&mut events).await, 1);
        assert_eq!(joins.load(Ordering::SeqCst), 1);

        kill.send(()).unwrap();
        wait_for_state(&channel, ChannelState::Stalled).await;

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
        let client = PondClient::with_options(
            &url,
            None,
            ClientOptions {
                reconnect_initial_delay: Duration::from_millis(10),
                max_reconnect_delay: Duration::from_millis(25),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        client.connect().await.unwrap();
        let channel = client.create_channel("room", None).await;
        channel.join().await;

        wait_for_state(&channel, ChannelState::Declined).await;
        let reason = channel.decline_reason().await.unwrap();
        assert_eq!(reason.get("channel").and_then(Value::as_str), Some("room"));
        assert!(channel.inner.queue.lock().await.is_empty());
        assert!(
            channel
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .is_empty()
        );
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
        while channel
            .inner
            .pending
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .is_empty()
        {
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
        assert!(
            channel
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .is_empty()
        );
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
            if let Some(id) = channel
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .keys()
                .next()
                .cloned()
            {
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
        assert!(
            channel
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .is_empty()
        );
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
        let mut errors = client.subscribe_errors();
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
        let error = tokio::time::timeout(Duration::from_secs(1), errors.recv())
            .await
            .unwrap()
            .unwrap();
        assert!(matches!(&*error, ClientError::Protocol(message) if message.contains("line 126")));
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

    #[tokio::test]
    async fn dropping_last_client_owner_releases_inner_and_invalidates_channels() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let weak = Arc::downgrade(&client.inner);
        let channel = client.create_channel("room", None).await;

        drop(client);
        tokio::task::yield_now().await;

        assert!(weak.upgrade().is_none());
        assert!(matches!(
            channel.send_message_checked("message", None).await,
            Err(ClientError::ChannelClosed)
        ));
    }

    #[tokio::test]
    async fn join_timeout_declines_and_removes_channel_without_leaking_queue() {
        let client = PondClient::with_options(
            "ws://example.com/socket",
            None,
            ClientOptions {
                join_timeout: Duration::from_millis(20),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        let mut errors = client.subscribe_errors();
        let channel = client.create_channel("room", None).await;
        channel.join().await;

        wait_for_state(&channel, ChannelState::Declined).await;

        assert!(client.inner.channels.lock().await.get("room").is_none());
        assert!(channel.inner.queue.lock().await.is_empty());
        assert!(
            channel
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .is_empty()
        );
        let error = errors.recv().await.unwrap();
        assert!(matches!(&*error, ClientError::JoinTimeout));
    }

    #[tokio::test]
    async fn leave_removes_closed_channel_from_client_registry() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);

        channel.leave().await;

        assert_eq!(channel.state(), ChannelState::Closed);
        assert!(client.inner.channels.lock().await.get("room").is_none());
    }

    #[tokio::test]
    async fn stale_connection_generation_cannot_clear_replacement_outbound() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        client
            .inner
            .desired_connected
            .store(true, Ordering::Release);
        client.inner.generation.store(2, Ordering::Release);
        client
            .inner
            .set_connection_state(ConnectionState::Connected);
        let (tx, _rx) = mpsc::channel(1);
        *client.inner.outbound.lock().await = Some(tx);

        client.inner.connection_lost(1, None).await;

        assert_eq!(client.state(), ConnectionState::Connected);
        assert!(client.inner.outbound.lock().await.is_some());
    }

    #[tokio::test]
    async fn ping_timeout_is_reported_and_disconnects_unresponsive_peer() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let _socket = accept_async(stream).await.unwrap();
            tokio::time::sleep(Duration::from_millis(250)).await;
        });
        let client = PondClient::with_options(
            format!("ws://{addr}/socket"),
            None,
            ClientOptions {
                ping_interval: Some(Duration::from_millis(10)),
                pong_timeout: Duration::from_millis(30),
                reconnect_initial_delay: Duration::from_secs(1),
                max_reconnect_delay: Duration::from_secs(1),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        let mut errors = client.subscribe_errors();
        client.connect().await.unwrap();

        let error = tokio::time::timeout(Duration::from_secs(1), async {
            loop {
                let error = errors.recv().await.unwrap();
                if matches!(&*error, ClientError::ConnectionLost(message) if message.contains("pong timeout"))
                {
                    break error;
                }
            }
        })
        .await
        .expect("missing pong timeout error");

        assert!(matches!(&*error, ClientError::ConnectionLost(_)));
        assert_eq!(client.state(), ConnectionState::Disconnected);
        client.disconnect().await;
        server.await.unwrap();
    }

    #[tokio::test]
    async fn explicit_disconnect_cancels_in_progress_handshake() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let (accepted_tx, accepted_rx) = oneshot::channel();
        let server = tokio::spawn(async move {
            let (_stream, _) = listener.accept().await.unwrap();
            let _ = accepted_tx.send(());
            tokio::time::sleep(Duration::from_secs(1)).await;
        });
        let client = PondClient::with_options(
            format!("ws://{addr}/socket"),
            None,
            ClientOptions {
                connection_timeout: Duration::from_secs(5),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        let connector = client.clone();
        let connect = tokio::spawn(async move { connector.connect().await });
        accepted_rx.await.unwrap();

        tokio::time::timeout(Duration::from_millis(250), client.disconnect())
            .await
            .expect("disconnect waited for the handshake timeout");

        assert!(matches!(
            connect.await.unwrap(),
            Err(ClientError::NotConnected)
        ));
        assert_eq!(client.state(), ConnectionState::Disconnected);
        server.abort();
    }

    #[tokio::test]
    async fn disconnect_aborts_io_installed_after_the_prelock_abort() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let _socket = accept_async(stream).await.unwrap();
            tokio::time::sleep(Duration::from_secs(1)).await;
        });
        let client = PondClient::new(format!("ws://{addr}/socket"), None).unwrap();
        let reached = Arc::new(Notify::new());
        let resume = Arc::new(Notify::new());
        *client
            .inner
            .connect_install_hook
            .lock()
            .unwrap_or_else(|error| error.into_inner()) =
            Some((Arc::clone(&reached), Arc::clone(&resume)));

        let connector = client.clone();
        let connect = tokio::spawn(async move { connector.connect().await });
        tokio::time::timeout(Duration::from_secs(1), reached.notified())
            .await
            .expect("connect did not reach the pre-install hook");

        let disconnector = client.clone();
        let disconnect = tokio::spawn(async move { disconnector.disconnect().await });
        tokio::time::timeout(Duration::from_secs(1), async {
            while client.inner.desired_connected.load(Ordering::Acquire) {
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("disconnect did not perform its pre-lock invalidation");

        resume.notify_one();
        connect.await.unwrap().unwrap();
        disconnect.await.unwrap();

        assert_eq!(client.state(), ConnectionState::Disconnected);
        assert!(client.inner.outbound.lock().await.is_none());
        assert!(
            client
                .inner
                .read_task
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .is_none()
        );
        assert!(
            client
                .inner
                .write_task
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .is_none()
        );
        server.abort();
    }

    #[tokio::test]
    async fn aborting_public_connect_restores_disconnected_and_reconnect_intent() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let _socket = accept_async(stream).await.unwrap();
            std::future::pending::<()>().await;
        });
        let client = PondClient::with_options(
            format!("ws://{addr}/socket"),
            None,
            ClientOptions {
                reconnect_initial_delay: Duration::from_secs(3600),
                max_reconnect_delay: Duration::from_secs(3600),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        let reached = Arc::new(Notify::new());
        let resume = Arc::new(Notify::new());
        *client
            .inner
            .connect_install_hook
            .lock()
            .unwrap_or_else(|error| error.into_inner()) = Some((Arc::clone(&reached), resume));

        let connector = client.clone();
        let connect = tokio::spawn(async move { connector.connect().await });
        reached.notified().await;
        connect.abort();
        assert!(connect.await.unwrap_err().is_cancelled());
        tokio::task::yield_now().await;

        assert_eq!(client.state(), ConnectionState::Disconnected);
        assert!(client.inner.outbound.lock().await.is_none());
        assert!(client.inner.reconnecting.load(Ordering::Acquire));
        client.disconnect().await;
        server.abort();
    }

    #[tokio::test]
    async fn public_connect_safely_replaces_an_aborted_reconnect_attempt() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (first, _) = listener.accept().await.unwrap();
            let first = accept_async(first).await.unwrap();
            let (second, _) = listener.accept().await.unwrap();
            let second = accept_async(second).await.unwrap();
            let _sockets = (first, second);
            std::future::pending::<()>().await;
        });
        let client = PondClient::with_options(
            format!("ws://{addr}/socket"),
            None,
            ClientOptions {
                reconnect_initial_delay: Duration::from_millis(1),
                max_reconnect_delay: Duration::from_millis(1),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        let reached = Arc::new(Notify::new());
        let resume = Arc::new(Notify::new());
        *client
            .inner
            .connect_install_hook
            .lock()
            .unwrap_or_else(|error| error.into_inner()) = Some((Arc::clone(&reached), resume));
        client
            .inner
            .desired_connected
            .store(true, Ordering::Release);
        client.inner.schedule_reconnect();
        reached.notified().await;

        tokio::time::timeout(Duration::from_secs(1), client.connect())
            .await
            .expect("replacement connect remained stuck behind cancelled reconnect")
            .unwrap();

        assert_eq!(client.state(), ConnectionState::Connected);
        assert!(client.inner.outbound.lock().await.is_some());
        client.disconnect().await;
        server.abort();
    }

    #[tokio::test]
    async fn cancelling_response_wait_removes_pending_entry() {
        let client = PondClient::with_options(
            "ws://example.com/socket",
            None,
            ClientOptions {
                max_queue_size: 1,
                ..ClientOptions::default()
            },
        )
        .unwrap();
        let channel = client.create_channel("room", None).await;
        channel.inner.state.send_replace(ChannelState::Joined);
        channel.inner.queue.lock().await.push_back(ClientMessage {
            action: ClientAction::Broadcast,
            event: "existing".to_owned(),
            payload: Map::new(),
            channel_name: "room".to_owned(),
            request_id: "existing".to_owned(),
        });

        let sender = channel.clone();
        let send = tokio::spawn(async move {
            sender
                .send_for_response("blocked", None, Some(Duration::from_secs(60)))
                .await
        });
        tokio::time::timeout(Duration::from_secs(1), async {
            while channel
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .is_empty()
            {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
        send.abort();
        assert!(send.await.unwrap_err().is_cancelled());

        assert!(
            channel
                .inner
                .pending
                .lock()
                .unwrap_or_else(|error| error.into_inner())
                .is_empty()
        );
    }

    #[tokio::test(start_paused = true)]
    async fn cancelling_blocked_join_keeps_armed_timeout() {
        let client = PondClient::with_options(
            "ws://example.com/socket",
            None,
            ClientOptions {
                join_timeout: Duration::from_secs(5),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        client
            .inner
            .set_connection_state(ConnectionState::Connected);
        let (tx, _rx) = mpsc::channel(1);
        tx.try_send(ClientMessage {
            action: ClientAction::Broadcast,
            event: "fill".to_owned(),
            payload: Map::new(),
            channel_name: "room".to_owned(),
            request_id: "fill".to_owned(),
        })
        .unwrap();
        *client.inner.outbound.lock().await = Some(tx);
        let channel = client.create_channel("room", None).await;

        let joining = channel.clone();
        let join = tokio::spawn(async move { joining.join().await });
        while channel.state() != ChannelState::Joining {
            tokio::task::yield_now().await;
        }
        join.abort();
        assert!(join.await.unwrap_err().is_cancelled());
        tokio::time::advance(Duration::from_secs(5)).await;
        tokio::task::yield_now().await;

        assert_eq!(channel.state(), ChannelState::Declined);
        assert!(channel.inner.pending_join.lock().await.is_none());
    }

    #[tokio::test(start_paused = true)]
    async fn timed_out_blocked_join_never_commits_a_stale_join() {
        let client = PondClient::with_options(
            "ws://example.com/socket",
            None,
            ClientOptions {
                join_timeout: Duration::from_secs(5),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        client
            .inner
            .set_connection_state(ConnectionState::Connected);
        let (tx, mut rx) = mpsc::channel(1);
        tx.try_send(ClientMessage {
            action: ClientAction::Broadcast,
            event: "fill".to_owned(),
            payload: Map::new(),
            channel_name: "room".to_owned(),
            request_id: "fill".to_owned(),
        })
        .unwrap();
        *client.inner.outbound.lock().await = Some(tx);
        let channel = client.create_channel("room", None).await;

        let joining = channel.clone();
        let join = tokio::spawn(async move { joining.join().await });
        while channel.state() != ChannelState::Joining {
            tokio::task::yield_now().await;
        }
        tokio::time::advance(Duration::from_secs(5)).await;
        join.await.unwrap();

        assert_eq!(channel.state(), ChannelState::Declined);
        assert_eq!(rx.recv().await.unwrap().request_id, "fill");
        tokio::task::yield_now().await;
        assert!(matches!(
            rx.try_recv(),
            Err(mpsc::error::TryRecvError::Empty)
        ));
    }

    #[tokio::test]
    async fn cancelling_ack_flush_preserves_all_unsent_messages_in_order() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        client
            .inner
            .set_connection_state(ConnectionState::Connected);
        let (tx, _rx) = mpsc::channel(1);
        tx.try_send(ClientMessage {
            action: ClientAction::Broadcast,
            event: "fill".to_owned(),
            payload: Map::new(),
            channel_name: "room".to_owned(),
            request_id: "fill".to_owned(),
        })
        .unwrap();
        *client.inner.outbound.lock().await = Some(tx);
        let channel = client.create_channel("room", None).await;
        for request_id in ["first", "second"] {
            channel.inner.queue.lock().await.push_back(ClientMessage {
                action: ClientAction::Broadcast,
                event: "queued".to_owned(),
                payload: Map::new(),
                channel_name: "room".to_owned(),
                request_id: request_id.to_owned(),
            });
        }

        let flushing = channel.clone();
        let flush = tokio::spawn(async move { flushing.acknowledge().await });
        while channel.state() != ChannelState::Joined {
            tokio::task::yield_now().await;
        }
        flush.abort();
        assert!(flush.await.unwrap_err().is_cancelled());
        assert_eq!(channel.state(), ChannelState::Stalled);

        let request_ids = channel
            .inner
            .queue
            .lock()
            .await
            .iter()
            .map(|message| message.request_id.clone())
            .collect::<Vec<_>>();
        assert_eq!(request_ids, ["first".to_owned(), "second".to_owned()]);
    }

    #[tokio::test]
    async fn websocket_write_and_close_are_timeout_bounded() {
        struct PendingSink;

        impl Sink<Message> for PendingSink {
            type Error = tokio_tungstenite::tungstenite::Error;

            fn poll_ready(
                self: std::pin::Pin<&mut Self>,
                _context: &mut std::task::Context<'_>,
            ) -> std::task::Poll<std::result::Result<(), Self::Error>> {
                std::task::Poll::Pending
            }

            fn start_send(
                self: std::pin::Pin<&mut Self>,
                _item: Message,
            ) -> std::result::Result<(), Self::Error> {
                Ok(())
            }

            fn poll_flush(
                self: std::pin::Pin<&mut Self>,
                _context: &mut std::task::Context<'_>,
            ) -> std::task::Poll<std::result::Result<(), Self::Error>> {
                std::task::Poll::Pending
            }

            fn poll_close(
                self: std::pin::Pin<&mut Self>,
                _context: &mut std::task::Context<'_>,
            ) -> std::task::Poll<std::result::Result<(), Self::Error>> {
                std::task::Poll::Pending
            }
        }

        let mut sink = PendingSink;
        let write = send_websocket_frame(
            &mut sink,
            Message::Text("blocked".into()),
            Duration::from_millis(10),
        )
        .await
        .unwrap_err();
        assert!(matches!(
            write,
            ClientError::ConnectionLost(message) if message.contains("write timed out")
        ));

        let close = close_websocket_writer(&mut sink, Duration::from_millis(10))
            .await
            .unwrap_err();
        assert!(matches!(
            close,
            ClientError::ConnectionLost(message) if message.contains("close timed out")
        ));
    }

    #[tokio::test]
    async fn oversized_inbound_message_is_rejected_and_reported() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let mut socket = accept_async(stream).await.unwrap();
            let oversized = "x".repeat(1024);
            socket.send(Message::Text(oversized.into())).await.unwrap();
            tokio::time::sleep(Duration::from_millis(100)).await;
        });
        let client = PondClient::with_options(
            format!("ws://{addr}/socket"),
            None,
            ClientOptions {
                max_message_size: 128,
                max_frame_size: 128,
                reconnect_initial_delay: Duration::from_secs(1),
                max_reconnect_delay: Duration::from_secs(1),
                ..ClientOptions::default()
            },
        )
        .unwrap();
        let mut errors = client.subscribe_errors();
        client.connect().await.unwrap();

        let error = tokio::time::timeout(Duration::from_secs(1), errors.recv())
            .await
            .unwrap()
            .unwrap();

        assert!(matches!(&*error, ClientError::WebSocket(_)));
        assert_eq!(client.state(), ConnectionState::Disconnected);
        client.disconnect().await;
        server.await.unwrap();
    }
}
