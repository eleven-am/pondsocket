use async_trait::async_trait;
use futures_util::FutureExt;
use redis::AsyncCommands;
use redis::aio::{MultiplexedConnection, PubSubSink};
use std::collections::{HashMap, HashSet, VecDeque};
use std::panic::AssertUnwindSafe;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, Instant};
use tokio::sync::{Mutex, RwLock, mpsc, watch};
use tokio::task::JoinHandle;
use tokio_stream::StreamExt;

use crate::errors::{Result, not_found, unavailable};
use crate::pubsub::{
    FailureSignal, PubSub, PubSubFailureHandler, PubSubHandler, SubscriptionId, match_topic,
};

const DEFAULT_SUBSCRIPTION_BUFFER: usize = 256;
const CONNECT_TIMEOUT: Duration = Duration::from_secs(10);
const COMMAND_TIMEOUT: Duration = Duration::from_secs(5);
const HANDLER_TIMEOUT: Duration = Duration::from_secs(30);
const SUPERVISED_HANDLER_TIMEOUT: Duration = Duration::from_secs(120);
const SUBSCRIPTION_DRAIN_TIMEOUT: Duration = Duration::from_secs(1);
const CLOSE_TIMEOUT: Duration = Duration::from_secs(1);
const INITIAL_RECONNECT_DELAY: Duration = Duration::from_millis(50);
const MAX_RECONNECT_DELAY: Duration = Duration::from_secs(5);
const STABLE_SESSION: Duration = Duration::from_secs(1);
const DEDUPE_WINDOW: usize = 4_096;

struct RedisSubscription {
    id: SubscriptionId,
    tx: Option<mpsc::Sender<(String, Vec<u8>)>>,
    task: Option<JoinHandle<()>>,
    supervised: Option<SupervisedHandler>,
}

#[derive(Clone)]
struct RedisDeliveryTarget {
    key: String,
    id: SubscriptionId,
    is_pattern: bool,
    tx: Option<mpsc::Sender<(String, Vec<u8>)>>,
    supervised: Option<SupervisedHandler>,
}

#[derive(Clone)]
struct SupervisedHandler {
    handler: PubSubHandler,
    failure: Arc<FailureSignal>,
    cancelled: watch::Sender<bool>,
}

struct SupervisedDelivery {
    topic: String,
    payload: Vec<u8>,
    target: SupervisedHandler,
}

#[derive(Default)]
struct RedisState {
    exact: HashMap<String, Vec<RedisSubscription>>,
    patterns: HashMap<String, Vec<RedisSubscription>>,
    closed: bool,
}

#[derive(Debug, Default, PartialEq, Eq)]
struct SubscriptionPlan {
    exact: HashSet<String>,
    patterns: HashSet<String>,
}

#[derive(Default)]
struct SeenMessages {
    ids: HashSet<(String, String)>,
    order: VecDeque<(String, String)>,
}

impl SeenMessages {
    fn should_deliver(&mut self, topic: &str, payload: &[u8]) -> bool {
        let Some(id) = distributed_message_id(payload) else {
            return true;
        };
        let key = (topic.to_owned(), id);
        if !self.ids.insert(key.clone()) {
            return false;
        }
        self.order.push_back(key);
        if self.order.len() > DEDUPE_WINDOW
            && let Some(expired) = self.order.pop_front()
        {
            self.ids.remove(&expired);
        }
        true
    }
}

pub struct RedisPubSub {
    client: redis::Client,
    publisher: Mutex<Option<MultiplexedConnection>>,
    state: Arc<RwLock<RedisState>>,
    changes: watch::Sender<u64>,
    applied: watch::Sender<u64>,
    listener: Mutex<Option<JoinHandle<()>>>,
    _supervised_tx: mpsc::Sender<SupervisedDelivery>,
    supervised_worker: Mutex<Option<JoinHandle<()>>>,
    shutdown: watch::Sender<bool>,
    close_lock: Mutex<()>,
    next_id: AtomicU64,
    buffer_size: usize,
}

impl RedisPubSub {
    pub async fn new(client: redis::Client) -> Result<Self> {
        Self::with_buffer_size(client, DEFAULT_SUBSCRIPTION_BUFFER).await
    }

    pub async fn with_buffer_size(client: redis::Client, buffer_size: usize) -> Result<Self> {
        ensure_tls_crypto_provider(&client);
        let buffer_size = buffer_size.max(1);
        let mut publisher = connect_publisher(&client).await?;
        let _: String = tokio::time::timeout(
            COMMAND_TIMEOUT,
            redis::cmd("PING").query_async(&mut publisher),
        )
        .await
        .map_err(|_| unavailable("redis", "PING timed out"))?
        .map_err(|error| unavailable("redis", error.to_string()))?;

        let state = Arc::new(RwLock::new(RedisState::default()));
        let (changes, change_rx) = watch::channel(0);
        let (applied, _) = watch::channel(0);
        let (shutdown, _) = watch::channel(false);
        let (supervised_tx, supervised_rx) = mpsc::channel(buffer_size);
        let supervised_worker = tokio::spawn(run_supervised_worker(supervised_rx));
        let task = tokio::spawn(run_listener(
            client.clone(),
            Arc::clone(&state),
            changes.clone(),
            change_rx,
            applied.clone(),
            supervised_tx.clone(),
        ));

        Ok(Self {
            client,
            publisher: Mutex::new(Some(publisher)),
            state,
            changes,
            applied,
            listener: Mutex::new(Some(task)),
            _supervised_tx: supervised_tx,
            supervised_worker: Mutex::new(Some(supervised_worker)),
            shutdown,
            close_lock: Mutex::new(()),
            next_id: AtomicU64::new(0),
            buffer_size,
        })
    }

    fn notify_changed(&self) -> u64 {
        let mut next = 0;
        self.changes.send_modify(|version| {
            *version = version.wrapping_add(1);
            next = *version;
        });
        next
    }

    async fn wait_until_applied(&self, target: u64) -> Result<()> {
        let mut applied = self.applied.subscribe();
        let mut changes = self.changes.subscribe();
        tokio::time::timeout(CONNECT_TIMEOUT, async {
            while *applied.borrow() < target {
                if self.state.read().await.closed {
                    return Err(unavailable("redis", "pubsub closed"));
                }
                tokio::select! {
                    changed = applied.changed() => {
                        changed.map_err(|_| unavailable("redis", "listener stopped"))?;
                    }
                    changed = changes.changed() => {
                        changed.map_err(|_| unavailable("redis", "listener stopped"))?;
                        if self.state.read().await.closed {
                            return Err(unavailable("redis", "pubsub closed"));
                        }
                    }
                }
            }
            Ok(())
        })
        .await
        .map_err(|_| unavailable("redis", "subscription update timed out"))?
    }

    async fn add_subscription(
        &self,
        topic: &str,
        handler: PubSubHandler,
        is_pattern: bool,
        failure: Option<Arc<FailureSignal>>,
    ) -> Result<SubscriptionId> {
        let id = self.next_id.fetch_add(1, Ordering::SeqCst);
        let (tx, task, supervised) = if let Some(failure) = failure {
            (
                None,
                None,
                Some(SupervisedHandler {
                    handler,
                    failure,
                    cancelled: watch::channel(false).0,
                }),
            )
        } else {
            let (tx, task) =
                spawn_subscription_worker(handler, self.buffer_size, HANDLER_TIMEOUT, None);
            (Some(tx), Some(task), None)
        };
        {
            let mut state = self.state.write().await;
            if state.closed {
                drop(state);
                if let Some(task) = task {
                    task.abort();
                    let _ = task.await;
                }
                return Err(unavailable("redis", "pubsub closed"));
            }
            let subscriptions = if is_pattern {
                &mut state.patterns
            } else {
                &mut state.exact
            };
            subscriptions
                .entry(topic.to_owned())
                .or_default()
                .push(RedisSubscription {
                    id,
                    tx,
                    task,
                    supervised,
                });
        }
        let version = self.notify_changed();
        if let Err(error) = self.wait_until_applied(version).await {
            let subscription = {
                let mut state = self.state.write().await;
                remove_redis_subscription(&mut state.exact, topic, id)
                    .or_else(|| remove_redis_subscription(&mut state.patterns, topic, id))
            };
            if let Some(subscription) = subscription {
                stop_redis_subscription(subscription, false).await;
            }
            self.notify_changed();
            return Err(error);
        }
        Ok(id)
    }
}

fn ensure_tls_crypto_provider(client: &redis::Client) {
    if matches!(
        client.get_connection_info().addr(),
        redis::ConnectionAddr::TcpTls { .. }
    ) && rustls::crypto::CryptoProvider::get_default().is_none()
    {
        // Respect a provider selected by the application. If none exists,
        // install Ring so `rediss://` works without a process-global panic.
        // A concurrent installer may win this race, which is also acceptable.
        let _ = rustls::crypto::ring::default_provider().install_default();
    }
}

fn spawn_subscription_worker(
    handler: PubSubHandler,
    buffer_size: usize,
    handler_timeout: Duration,
    failure: Option<Arc<FailureSignal>>,
) -> (mpsc::Sender<(String, Vec<u8>)>, JoinHandle<()>) {
    let (tx, mut rx) = mpsc::channel(buffer_size.max(1));
    let task = tokio::spawn(async move {
        while let Some((topic, payload)) = rx.recv().await {
            let outcome = tokio::time::timeout(
                handler_timeout,
                AssertUnwindSafe(handler(topic, payload)).catch_unwind(),
            )
            .await;
            if !matches!(outcome, Ok(Ok(()))) {
                if let Some(failure) = &failure {
                    failure.fire();
                }
                break;
            }
        }
    });
    (tx, task)
}

async fn run_supervised_worker(mut rx: mpsc::Receiver<SupervisedDelivery>) {
    while let Some(delivery) = rx.recv().await {
        if delivery.target.failure.is_fired() || *delivery.target.cancelled.borrow() {
            continue;
        }
        let mut cancelled = delivery.target.cancelled.subscribe();
        let outcome = tokio::select! {
            biased;
            _ = wait_for_shutdown(&mut cancelled) => continue,
            outcome = tokio::time::timeout(
                SUPERVISED_HANDLER_TIMEOUT,
                AssertUnwindSafe((delivery.target.handler)(delivery.topic, delivery.payload))
                    .catch_unwind(),
            ) => outcome,
        };
        if !matches!(outcome, Ok(Ok(()))) {
            delivery.target.failure.fire();
        }
    }
}

async fn stop_redis_subscription(mut subscription: RedisSubscription, drain: bool) {
    if let Some(supervised) = &subscription.supervised {
        supervised.cancelled.send_replace(true);
    }
    subscription.tx.take();
    if let Some(task) = subscription.task.take() {
        let mut task = task;
        if !drain
            || tokio::time::timeout(SUBSCRIPTION_DRAIN_TIMEOUT, &mut task)
                .await
                .is_err()
        {
            task.abort();
            let _ = task.await;
        }
    }
}

#[async_trait]
impl PubSub for RedisPubSub {
    async fn subscribe(&self, pattern: &str, handler: PubSubHandler) -> Result<SubscriptionId> {
        self.add_subscription(pattern, handler, false, None).await
    }

    async fn subscribe_pattern(
        &self,
        pattern: &str,
        handler: PubSubHandler,
    ) -> Result<SubscriptionId> {
        self.add_subscription(pattern, handler, true, None).await
    }

    async fn subscribe_supervised(
        &self,
        topic: &str,
        handler: PubSubHandler,
        on_failure: PubSubFailureHandler,
    ) -> Result<SubscriptionId> {
        self.add_subscription(topic, handler, false, Some(FailureSignal::new(on_failure)))
            .await
    }

    async fn unsubscribe(&self, pattern: &str, id: SubscriptionId) -> Result<()> {
        let subscription = {
            let mut state = self.state.write().await;
            if state.closed {
                return Err(unavailable("redis", "pubsub closed"));
            }
            remove_redis_subscription(&mut state.exact, pattern, id)
                .or_else(|| remove_redis_subscription(&mut state.patterns, pattern, id))
                .ok_or_else(|| not_found("redis", "subscription not found"))?
        };
        stop_redis_subscription(subscription, true).await;
        let version = self.notify_changed();
        self.wait_until_applied(version).await?;
        Ok(())
    }

    async fn publish(&self, topic: &str, data: Vec<u8>) -> Result<()> {
        if self.state.read().await.closed {
            return Err(unavailable("redis", "pubsub closed"));
        }
        let mut shutdown = self.shutdown.subscribe();

        // One ordered publishing connection avoids per-message connection churn
        // and preserves the order in which callers acquire this mutex.
        let mut publisher = tokio::select! {
            biased;
            _ = wait_for_shutdown(&mut shutdown) => {
                return Err(unavailable("redis", "pubsub closed"));
            }
            publisher = tokio::time::timeout(COMMAND_TIMEOUT, self.publisher.lock()) => {
                publisher.map_err(|_| unavailable("redis", "publisher lock timed out"))?
            }
        };
        if self.state.read().await.closed {
            return Err(unavailable("redis", "pubsub closed"));
        }
        if publisher.is_none() {
            *publisher = Some(connect_publisher_until_shutdown(&self.client, &mut shutdown).await?);
        }
        let first = tokio::select! {
            biased;
            _ = wait_for_shutdown(&mut shutdown) => {
                return Err(unavailable("redis", "pubsub closed"));
            }
            result = tokio::time::timeout(
                COMMAND_TIMEOUT,
                publisher
                    .as_mut()
                    .expect("publisher initialized above")
                    .publish::<_, _, usize>(topic, data.clone()),
            ) => result,
        };
        if matches!(first, Ok(Ok(_))) {
            return Ok(());
        }

        // Reconnect and retry once. Redis PUBLISH is not acknowledged with a
        // message id, so more retries could create duplicate delivery.
        *publisher = None;
        if *shutdown.borrow() || self.state.read().await.closed {
            return Err(unavailable("redis", "pubsub closed"));
        }
        let mut connection = connect_publisher_until_shutdown(&self.client, &mut shutdown).await?;
        tokio::select! {
            biased;
            _ = wait_for_shutdown(&mut shutdown) => {
                return Err(unavailable("redis", "pubsub closed"));
            }
            result = tokio::time::timeout(
                COMMAND_TIMEOUT,
                connection.publish::<_, _, usize>(topic, data),
            ) => {
                result
                    .map_err(|_| unavailable("redis", "PUBLISH timed out"))?
                    .map_err(|error| unavailable("redis", error.to_string()))?;
            }
        }
        if *shutdown.borrow() || self.state.read().await.closed {
            return Err(unavailable("redis", "pubsub closed"));
        }
        *publisher = Some(connection);
        Ok(())
    }

    async fn close(&self) -> Result<()> {
        let _close = tokio::time::timeout(COMMAND_TIMEOUT, self.close_lock.lock())
            .await
            .map_err(|_| unavailable("redis", "concurrent close timed out"))?;
        let subscriptions = {
            let mut state = self.state.write().await;
            state.closed = true;
            let mut subscriptions = state
                .exact
                .drain()
                .flat_map(|(_, subscriptions)| subscriptions)
                .collect::<Vec<_>>();
            subscriptions.extend(
                state
                    .patterns
                    .drain()
                    .flat_map(|(_, subscriptions)| subscriptions),
            );
            subscriptions
        };
        self.shutdown.send_replace(true);
        self.notify_changed();
        for subscription in subscriptions {
            stop_redis_subscription(subscription, false).await;
        }

        let mut first_error = None;
        match tokio::time::timeout(CLOSE_TIMEOUT, self.publisher.lock()).await {
            Ok(mut publisher) => {
                publisher.take();
            }
            Err(_) => first_error = Some(unavailable("redis", "publisher close timed out")),
        }
        let task = match tokio::time::timeout(CLOSE_TIMEOUT, self.listener.lock()).await {
            Ok(mut listener) => listener.take(),
            Err(_) => {
                if first_error.is_none() {
                    first_error = Some(unavailable("redis", "listener close timed out"));
                }
                None
            }
        };
        if let Some(task) = task {
            task.abort();
            let _ = task.await;
        }
        let supervised_worker =
            match tokio::time::timeout(CLOSE_TIMEOUT, self.supervised_worker.lock()).await {
                Ok(mut worker) => worker.take(),
                Err(_) => {
                    if first_error.is_none() {
                        first_error =
                            Some(unavailable("redis", "supervised worker close timed out"));
                    }
                    None
                }
            };
        if let Some(task) = supervised_worker {
            task.abort();
            let _ = task.await;
        }
        first_error.map_or(Ok(()), Err)
    }
}

fn remove_redis_subscription(
    subscriptions: &mut HashMap<String, Vec<RedisSubscription>>,
    topic: &str,
    id: SubscriptionId,
) -> Option<RedisSubscription> {
    let list = subscriptions.get_mut(topic)?;
    let index = list.iter().position(|subscription| subscription.id == id)?;
    let subscription = list.remove(index);
    if list.is_empty() {
        subscriptions.remove(topic);
    }
    Some(subscription)
}

async fn connect_publisher(client: &redis::Client) -> Result<MultiplexedConnection> {
    tokio::time::timeout(CONNECT_TIMEOUT, client.get_multiplexed_async_connection())
        .await
        .map_err(|_| unavailable("redis", "connection timed out"))?
        .map_err(|error| unavailable("redis", error.to_string()))
}

async fn wait_for_shutdown(shutdown: &mut watch::Receiver<bool>) {
    while !*shutdown.borrow_and_update() {
        if shutdown.changed().await.is_err() {
            return;
        }
    }
}

async fn connect_publisher_until_shutdown(
    client: &redis::Client,
    shutdown: &mut watch::Receiver<bool>,
) -> Result<MultiplexedConnection> {
    tokio::select! {
        biased;
        _ = wait_for_shutdown(shutdown) => Err(unavailable("redis", "pubsub closed")),
        result = connect_publisher(client) => result,
    }
}

async fn run_listener(
    client: redis::Client,
    state: Arc<RwLock<RedisState>>,
    changes_tx: watch::Sender<u64>,
    mut changes: watch::Receiver<u64>,
    applied: watch::Sender<u64>,
    supervised_tx: mpsc::Sender<SupervisedDelivery>,
) {
    let mut reconnect_delay = INITIAL_RECONNECT_DELAY;
    let mut seen = SeenMessages::default();
    loop {
        if state.read().await.closed {
            return;
        }

        let connection = tokio::time::timeout(CONNECT_TIMEOUT, client.get_async_pubsub()).await;
        let pubsub = match connection {
            Ok(Ok(pubsub)) => pubsub,
            _ => {
                tokio::time::sleep(reconnect_delay).await;
                reconnect_delay = next_reconnect_delay(reconnect_delay);
                continue;
            }
        };
        let session_started = Instant::now();

        let (mut sink, mut stream) = pubsub.split();
        let mut active = SubscriptionPlan::default();
        let version = *changes.borrow_and_update();
        let plan = {
            let state = state.read().await;
            plan_for(&state)
        };
        if !matches!(
            tokio::time::timeout(COMMAND_TIMEOUT, apply_plan(&mut sink, &active, &plan)).await,
            Ok(Ok(()))
        ) {
            tokio::time::sleep(reconnect_delay).await;
            reconnect_delay = next_reconnect_delay(reconnect_delay);
            continue;
        }
        active = plan;
        applied.send_replace(version);

        loop {
            tokio::select! {
                message = stream.next() => {
                    let Some(message) = message else {
                        break;
                    };
                    let topic = message.get_channel_name().to_owned();
                    let Ok(payload) = message.get_payload::<Vec<u8>>() else {
                        continue;
                    };
                    if !seen.should_deliver(&topic, &payload) {
                        continue;
                    }
                    let subscriptions = matching_subscriptions(&state, &topic).await;
                    let mut failed = Vec::new();
                    for subscription in subscriptions {
                        let delivered = if let Some(supervised) = &subscription.supervised {
                            supervised_tx.try_send(SupervisedDelivery {
                                topic: topic.clone(),
                                payload: payload.clone(),
                                target: supervised.clone(),
                            }).is_ok()
                        } else if let Some(tx) = &subscription.tx {
                            tx.try_send((topic.clone(), payload.clone())).is_ok()
                        } else {
                            false
                        };
                        if !delivered {
                            if let Some(supervised) = &subscription.supervised {
                                supervised.failure.fire();
                            }
                            failed.push(subscription);
                        }
                    }
                    if !failed.is_empty() {
                        evict_subscriptions(&state, failed).await;
                        changes_tx.send_modify(|version| *version = version.wrapping_add(1));
                    }
                }
                changed = changes.changed() => {
                    if changed.is_err() || state.read().await.closed {
                        return;
                    }
                    let version = *changes.borrow_and_update();
                    let plan = {
                        let state = state.read().await;
                        plan_for(&state)
                    };
                    if !matches!(
                        tokio::time::timeout(COMMAND_TIMEOUT, apply_plan(&mut sink, &active, &plan)).await,
                        Ok(Ok(()))
                    ) {
                        break;
                    }
                    active = plan;
                    applied.send_replace(version);
                }
            }
        }

        if session_started.elapsed() >= STABLE_SESSION {
            reconnect_delay = INITIAL_RECONNECT_DELAY;
        } else {
            let current_delay = reconnect_delay;
            reconnect_delay = next_reconnect_delay(reconnect_delay);
            tokio::time::sleep(current_delay).await;
            continue;
        }
        tokio::time::sleep(reconnect_delay).await;
    }
}

fn next_reconnect_delay(current: Duration) -> Duration {
    (current * 2).min(MAX_RECONNECT_DELAY)
}

fn distributed_message_id(payload: &[u8]) -> Option<String> {
    let value: serde_json::Value = serde_json::from_slice(payload).ok()?;
    (value.get("protocol")?.as_str()? == "pondsocket.distributed"
        && value.get("version")?.as_u64()? == 1
        && value.get("type")?.as_str().is_some())
    .then(|| value.get("messageId")?.as_str().map(ToOwned::to_owned))?
}

async fn matching_subscriptions(
    state: &RwLock<RedisState>,
    topic: &str,
) -> Vec<RedisDeliveryTarget> {
    let state = state.read().await;
    let mut matched = state
        .exact
        .get(topic)
        .into_iter()
        .flatten()
        .map(|subscription| RedisDeliveryTarget {
            key: topic.to_owned(),
            id: subscription.id,
            is_pattern: false,
            tx: subscription.tx.clone(),
            supervised: subscription.supervised.clone(),
        })
        .collect::<Vec<_>>();
    matched.extend(
        state
            .patterns
            .iter()
            .filter(|(pattern, _)| match_topic(pattern, topic))
            .flat_map(|(pattern, subscriptions)| {
                subscriptions
                    .iter()
                    .map(move |subscription| RedisDeliveryTarget {
                        key: pattern.clone(),
                        id: subscription.id,
                        is_pattern: true,
                        tx: subscription.tx.clone(),
                        supervised: subscription.supervised.clone(),
                    })
            }),
    );
    matched
}

async fn evict_subscriptions(state: &RwLock<RedisState>, failed: Vec<RedisDeliveryTarget>) {
    let mut removed = Vec::new();
    let mut state = state.write().await;
    for target in failed {
        let subscriptions = if target.is_pattern {
            &mut state.patterns
        } else {
            &mut state.exact
        };
        if let Some(subscription) = remove_redis_subscription(subscriptions, &target.key, target.id)
        {
            removed.push(subscription);
        }
    }
    drop(state);
    for subscription in removed {
        stop_redis_subscription(subscription, false).await;
    }
}

fn plan_for(state: &RedisState) -> SubscriptionPlan {
    let mut wildcard_patterns = state.patterns.keys().cloned().collect::<Vec<_>>();
    wildcard_patterns.sort_by_key(String::len);

    let mut patterns: HashSet<String> = HashSet::new();
    for pattern in wildcard_patterns {
        let sample = pattern.strip_suffix('*').unwrap_or(&pattern);
        if patterns
            .iter()
            .any(|existing| match_topic(existing, sample))
        {
            continue;
        }
        patterns.insert(pattern);
    }

    let exact = state
        .exact
        .keys()
        .filter(|topic| !patterns.iter().any(|pattern| match_topic(pattern, topic)))
        .cloned()
        .collect();
    SubscriptionPlan { exact, patterns }
}

async fn apply_plan(
    sink: &mut PubSubSink,
    active: &SubscriptionPlan,
    desired: &SubscriptionPlan,
) -> redis::RedisResult<()> {
    // Establish all new coverage before removing old coverage. Redis may
    // briefly deliver a valid PondSocket envelope twice during an
    // exact-to-pattern transition; `SeenMessages` removes that duplicate.
    for exact in desired.exact.difference(&active.exact) {
        sink.subscribe(exact).await?;
    }
    for pattern in desired.patterns.difference(&active.patterns) {
        sink.psubscribe(to_redis_pattern(pattern)).await?;
    }
    for exact in active.exact.difference(&desired.exact) {
        sink.unsubscribe(exact).await?;
    }
    for pattern in active.patterns.difference(&desired.patterns) {
        sink.punsubscribe(to_redis_pattern(pattern)).await?;
    }
    Ok(())
}

fn to_redis_pattern(pattern: &str) -> String {
    let prefix = pattern.strip_suffix(".*").unwrap_or(pattern);
    let mut escaped = String::with_capacity(prefix.len() + 1);
    for character in prefix.chars() {
        if matches!(character, '*' | '?' | '[' | ']' | '\\') {
            escaped.push('\\');
        }
        escaped.push(character);
    }
    escaped.push('*');
    escaped
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex as StdMutex;

    fn live_redis_test_lock() -> &'static tokio::sync::Mutex<()> {
        static LOCK: std::sync::OnceLock<tokio::sync::Mutex<()>> = std::sync::OnceLock::new();
        LOCK.get_or_init(|| tokio::sync::Mutex::new(()))
    }

    fn subscription_state(exact: &[&str], patterns: &[&str]) -> RedisState {
        RedisState {
            exact: exact
                .iter()
                .map(|topic| (topic.to_string(), Vec::new()))
                .collect(),
            patterns: patterns
                .iter()
                .map(|pattern| (pattern.to_string(), Vec::new()))
                .collect(),
            closed: false,
        }
    }

    #[test]
    fn plan_eliminates_overlapping_redis_subscriptions() {
        let plan = plan_for(&subscription_state(
            &["pond.room.one", "other.room"],
            &["pond.room.*", "pond.room.nested.*"],
        ));
        assert_eq!(plan.patterns, HashSet::from(["pond.room.*".to_owned()]));
        assert_eq!(plan.exact, HashSet::from(["other.room".to_owned()]));
    }

    #[test]
    fn exact_topics_do_not_use_redis_patterns() {
        let plan = plan_for(&subscription_state(
            &["room[*]", "room?", "room*literal"],
            &[],
        ));
        assert!(plan.patterns.is_empty());
        assert_eq!(plan.exact.len(), 3);
    }

    #[test]
    fn redis_pattern_escapes_user_controlled_glob_characters() {
        assert_eq!(to_redis_pattern("pond.[x]?.*"), r"pond.\[x\]\?*");
        assert_eq!(to_redis_pattern("pond.\\.*"), r"pond.\\*");
    }

    #[test]
    fn tls_client_has_a_crypto_provider_before_connecting() {
        let client = redis::Client::open("rediss://localhost:6379").unwrap();
        ensure_tls_crypto_provider(&client);
        assert!(rustls::crypto::CryptoProvider::get_default().is_some());
    }

    #[test]
    fn reconnect_backoff_grows_and_is_capped() {
        let mut delay = INITIAL_RECONNECT_DELAY;
        for _ in 0..32 {
            let next = next_reconnect_delay(delay);
            assert!(next >= delay);
            assert!(next <= MAX_RECONNECT_DELAY);
            delay = next;
        }
        assert_eq!(delay, MAX_RECONNECT_DELAY);
    }

    #[test]
    fn distributed_message_ids_are_deduplicated_with_a_bounded_window() {
        let mut seen = SeenMessages::default();
        let payload = br#"{"protocol":"pondsocket.distributed","version":1,"type":"USER_MESSAGE","messageId":"m1"}"#;
        assert!(seen.should_deliver("a", payload));
        assert!(!seen.should_deliver("a", payload));
        assert!(seen.should_deliver("b", payload));
        let unrelated = br#"{"messageId":"m1","unrelated":true}"#;
        assert!(seen.should_deliver("a", unrelated));
        assert!(seen.should_deliver("a", unrelated));

        for index in 0..=DEDUPE_WINDOW {
            let payload = format!(
                r#"{{"protocol":"pondsocket.distributed","version":1,"type":"USER_MESSAGE","messageId":"window-{index}"}}"#
            );
            assert!(seen.should_deliver("a", payload.as_bytes()));
        }
        assert!(seen.should_deliver("a", payload));
    }

    #[tokio::test]
    async fn matching_uses_exact_topic_and_separate_pattern_indexes() {
        let state = RwLock::new(RedisState::default());
        let mut tasks = Vec::new();
        {
            let mut state = state.write().await;
            for (id, topic) in [(1, "wanted"), (2, "unrelated"), (3, "another")] {
                let (tx, mut rx) = mpsc::channel(1);
                let task = tokio::spawn(async move { while rx.recv().await.is_some() {} });
                state
                    .exact
                    .entry(topic.to_owned())
                    .or_default()
                    .push(RedisSubscription {
                        id,
                        tx: Some(tx),
                        task: Some(task),
                        supervised: None,
                    });
            }
            let (tx, mut rx) = mpsc::channel(1);
            let task = tokio::spawn(async move { while rx.recv().await.is_some() {} });
            state
                .patterns
                .entry("branch.*".to_owned())
                .or_default()
                .push(RedisSubscription {
                    id: 4,
                    tx: Some(tx),
                    task: Some(task),
                    supervised: None,
                });
        }

        let exact = matching_subscriptions(&state, "wanted").await;
        assert_eq!(
            exact.iter().map(|target| target.id).collect::<Vec<_>>(),
            [1]
        );
        let pattern = matching_subscriptions(&state, "branch.child").await;
        assert_eq!(
            pattern.iter().map(|target| target.id).collect::<Vec<_>>(),
            [4]
        );

        let mut state = state.write().await;
        tasks.extend(state.exact.drain().flat_map(|(_, subscriptions)| {
            subscriptions
                .into_iter()
                .filter_map(|subscription| subscription.task)
        }));
        tasks.extend(state.patterns.drain().flat_map(|(_, subscriptions)| {
            subscriptions
                .into_iter()
                .filter_map(|subscription| subscription.task)
        }));
        drop(state);
        for task in tasks {
            task.abort();
            let _ = task.await;
        }
    }

    #[tokio::test]
    async fn overflowing_delivery_is_evicted_without_waiting_for_queue_space() {
        let state = RwLock::new(RedisState::default());
        let (tx, _rx) = mpsc::channel(1);
        tx.send(("topic".to_owned(), vec![1])).await.unwrap();
        let task = tokio::spawn(std::future::pending::<()>());
        state
            .write()
            .await
            .exact
            .entry("topic".to_owned())
            .or_default()
            .push(RedisSubscription {
                id: 1,
                tx: Some(tx),
                task: Some(task),
                supervised: None,
            });

        let targets = matching_subscriptions(&state, "topic").await;
        assert!(
            targets[0]
                .tx
                .as_ref()
                .unwrap()
                .try_send(("topic".to_owned(), vec![2]))
                .is_err()
        );
        evict_subscriptions(&state, targets).await;
        assert!(state.read().await.exact.is_empty());
    }

    #[tokio::test]
    async fn panicking_redis_handler_stops_only_its_worker() {
        let (tx, task) = spawn_subscription_worker(
            crate::pubsub::pubsub_handler(|_, _| async move {
                panic!("intentional Redis handler panic");
            }),
            1,
            Duration::from_secs(1),
            None,
        );
        tx.send(("topic".to_owned(), vec![])).await.unwrap();
        tokio::time::timeout(Duration::from_secs(1), task)
            .await
            .expect("worker did not stop")
            .expect("panic escaped worker isolation");
        assert!(tx.is_closed());
    }

    #[tokio::test]
    async fn hung_redis_handler_is_cancelled_after_its_deadline() {
        let dropped = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        struct DropSignal(Arc<std::sync::atomic::AtomicUsize>);
        impl Drop for DropSignal {
            fn drop(&mut self) {
                self.0.fetch_add(1, Ordering::SeqCst);
            }
        }
        let handler_dropped = dropped.clone();
        let (tx, task) = spawn_subscription_worker(
            crate::pubsub::pubsub_handler(move |_, _| {
                let guard = DropSignal(handler_dropped.clone());
                async move {
                    let _guard = guard;
                    std::future::pending::<()>().await;
                }
            }),
            1,
            Duration::from_millis(10),
            None,
        );
        tx.send(("topic".to_owned(), vec![])).await.unwrap();
        tokio::time::timeout(Duration::from_secs(1), task)
            .await
            .expect("worker did not stop")
            .expect("worker task failed");
        assert_eq!(dropped.load(Ordering::SeqCst), 1);
        assert!(tx.is_closed());
    }

    #[tokio::test]
    async fn graceful_unsubscribe_drains_messages_already_accepted_by_worker() {
        let received = Arc::new(tokio::sync::Mutex::new(Vec::new()));
        let output = received.clone();
        let (tx, task) = spawn_subscription_worker(
            crate::pubsub::pubsub_handler(move |_, payload| {
                let output = output.clone();
                async move {
                    output.lock().await.push(payload[0]);
                }
            }),
            16,
            HANDLER_TIMEOUT,
            None,
        );
        for value in 0..16_u8 {
            tx.send(("topic".to_owned(), vec![value])).await.unwrap();
        }
        stop_redis_subscription(
            RedisSubscription {
                id: 1,
                tx: Some(tx),
                task: Some(task),
                supervised: None,
            },
            true,
        )
        .await;
        assert_eq!(*received.lock().await, (0..16_u8).collect::<Vec<_>>());
    }

    #[tokio::test]
    async fn close_wakes_concurrent_subscribe_while_redis_is_unreachable() {
        let client = redis::Client::open("redis://127.0.0.1:1/").unwrap();
        let state = Arc::new(RwLock::new(RedisState::default()));
        let (changes, change_rx) = watch::channel(0);
        let (applied, _) = watch::channel(0);
        let (shutdown, _) = watch::channel(false);
        let (supervised_tx, supervised_rx) = mpsc::channel(1);
        let supervised_worker = tokio::spawn(run_supervised_worker(supervised_rx));
        let listener = tokio::spawn(run_listener(
            client.clone(),
            state.clone(),
            changes.clone(),
            change_rx,
            applied.clone(),
            supervised_tx.clone(),
        ));
        let pubsub = Arc::new(RedisPubSub {
            client,
            publisher: Mutex::new(None),
            state: state.clone(),
            changes,
            applied,
            listener: Mutex::new(Some(listener)),
            _supervised_tx: supervised_tx,
            supervised_worker: Mutex::new(Some(supervised_worker)),
            shutdown,
            close_lock: Mutex::new(()),
            next_id: AtomicU64::new(0),
            buffer_size: 1,
        });

        let subscribing = pubsub.clone();
        let subscribe = tokio::spawn(async move {
            subscribing
                .subscribe("topic", crate::pubsub::pubsub_handler(|_, _| async move {}))
                .await
        });
        let deadline = tokio::time::Instant::now() + Duration::from_secs(1);
        while state.read().await.exact.is_empty() {
            assert!(
                tokio::time::Instant::now() < deadline,
                "subscribe never installed its local worker"
            );
            tokio::task::yield_now().await;
        }

        tokio::time::timeout(Duration::from_millis(250), pubsub.close())
            .await
            .expect("close waited for the unreachable Redis listener")
            .unwrap();
        let result = tokio::time::timeout(Duration::from_millis(250), subscribe)
            .await
            .expect("pending subscribe was not woken by close")
            .unwrap();
        assert!(result.is_err());
    }

    #[tokio::test]
    async fn close_cancels_inflight_publish_before_retry_or_connection_restore() {
        let server = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = server.local_addr().unwrap();
        let (accepted_tx, accepted_rx) = tokio::sync::oneshot::channel();
        let blackhole = tokio::spawn(async move {
            let (_stream, _) = server.accept().await.unwrap();
            let _ = accepted_tx.send(());
            std::future::pending::<()>().await;
        });
        let client = redis::Client::open(format!("redis://{address}/")).unwrap();
        let state = Arc::new(RwLock::new(RedisState::default()));
        let (changes, _) = watch::channel(0);
        let (applied, _) = watch::channel(0);
        let (shutdown, _) = watch::channel(false);
        let (supervised_tx, supervised_rx) = mpsc::channel(1);
        let supervised_worker = tokio::spawn(run_supervised_worker(supervised_rx));
        let pubsub = Arc::new(RedisPubSub {
            client,
            publisher: Mutex::new(None),
            state,
            changes,
            applied,
            listener: Mutex::new(Some(tokio::spawn(std::future::pending::<()>()))),
            _supervised_tx: supervised_tx,
            supervised_worker: Mutex::new(Some(supervised_worker)),
            shutdown,
            close_lock: Mutex::new(()),
            next_id: AtomicU64::new(0),
            buffer_size: 1,
        });

        let publishing = pubsub.clone();
        let publish =
            tokio::spawn(async move { publishing.publish("topic", b"payload".to_vec()).await });
        tokio::time::timeout(Duration::from_secs(1), accepted_rx)
            .await
            .expect("publisher did not connect to blackhole")
            .unwrap();
        tokio::time::timeout(Duration::from_millis(250), pubsub.close())
            .await
            .expect("close did not cancel in-flight publish")
            .unwrap();
        assert!(publish.await.unwrap().is_err());
        assert!(pubsub.publisher.lock().await.is_none());
        blackhole.abort();
        let _ = blackhole.await;
    }

    #[tokio::test]
    async fn supervised_dispatcher_preserves_listener_global_topic_order() {
        let (tx, rx) = mpsc::channel(4);
        let worker = tokio::spawn(run_supervised_worker(rx));
        let received = Arc::new(tokio::sync::Mutex::new(Vec::new()));
        let first_output = received.clone();
        let first = SupervisedHandler {
            handler: crate::pubsub::pubsub_handler(move |_, _| {
                let first_output = first_output.clone();
                async move {
                    tokio::time::sleep(Duration::from_millis(25)).await;
                    first_output.lock().await.push("a");
                }
            }),
            failure: FailureSignal::new(Arc::new(|| {})),
            cancelled: watch::channel(false).0,
        };
        let second_output = received.clone();
        let second = SupervisedHandler {
            handler: crate::pubsub::pubsub_handler(move |_, _| {
                let second_output = second_output.clone();
                async move {
                    second_output.lock().await.push("b");
                }
            }),
            failure: FailureSignal::new(Arc::new(|| {})),
            cancelled: watch::channel(false).0,
        };
        tx.send(SupervisedDelivery {
            topic: "topic.a".to_owned(),
            payload: vec![],
            target: first,
        })
        .await
        .unwrap();
        tx.send(SupervisedDelivery {
            topic: "topic.b".to_owned(),
            payload: vec![],
            target: second,
        })
        .await
        .unwrap();
        tokio::time::timeout(Duration::from_secs(1), async {
            while received.lock().await.len() < 2 {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
        assert_eq!(*received.lock().await, ["a", "b"]);
        worker.abort();
        let _ = worker.await;
    }

    #[tokio::test]
    async fn live_redis_overlapping_subscriptions_deliver_once_and_in_order() {
        let Ok(url) = std::env::var("PONDSOCKET_TEST_REDIS_URL") else {
            return;
        };
        let _live_redis_guard = live_redis_test_lock().lock().await;
        let client = redis::Client::open(url).unwrap();
        let control_client = client.clone();
        let pubsub = RedisPubSub::with_buffer_size(client, 256).await.unwrap();
        let wildcard = Arc::new(StdMutex::new(Vec::new()));
        let exact = Arc::new(StdMutex::new(Vec::new()));

        let wildcard_output = wildcard.clone();
        pubsub
            .subscribe_pattern(
                "pond.test.*",
                crate::pubsub::pubsub_handler(move |_topic, payload| {
                    let wildcard_output = wildcard_output.clone();
                    async move {
                        let value: serde_json::Value = serde_json::from_slice(&payload).unwrap();
                        wildcard_output
                            .lock()
                            .unwrap()
                            .push(value["sequence"].as_u64().unwrap());
                    }
                }),
            )
            .await
            .unwrap();
        let exact_output = exact.clone();
        pubsub
            .subscribe(
                "pond.test.one",
                crate::pubsub::pubsub_handler(move |_topic, payload| {
                    let exact_output = exact_output.clone();
                    async move {
                        let value: serde_json::Value = serde_json::from_slice(&payload).unwrap();
                        exact_output
                            .lock()
                            .unwrap()
                            .push(value["sequence"].as_u64().unwrap());
                    }
                }),
            )
            .await
            .unwrap();

        for sequence in 0..64_u64 {
            let payload = serde_json::to_vec(&serde_json::json!({
                "protocol": "pondsocket.distributed",
                "version": 1,
                "type": "USER_MESSAGE",
                "messageId": format!("live-{sequence}"),
                "sequence": sequence,
            }))
            .unwrap();
            pubsub.publish("pond.test.one", payload).await.unwrap();
        }

        let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
        while wildcard.lock().unwrap().len() < 64 || exact.lock().unwrap().len() < 64 {
            assert!(
                tokio::time::Instant::now() < deadline,
                "Redis delivery stalled"
            );
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
        let expected = (0..64).collect::<Vec<_>>();
        assert_eq!(*wildcard.lock().unwrap(), expected);
        assert_eq!(*exact.lock().unwrap(), expected);

        let mut control = control_client
            .get_multiplexed_async_connection()
            .await
            .unwrap();
        let _: usize = redis::cmd("CLIENT")
            .arg("KILL")
            .arg("TYPE")
            .arg("PUBSUB")
            .query_async(&mut control)
            .await
            .unwrap();
        let reconnect_payload = serde_json::to_vec(&serde_json::json!({
            "protocol": "pondsocket.distributed",
            "version": 1,
            "type": "USER_MESSAGE",
            "messageId": "live-after-reconnect",
            "sequence": 64,
        }))
        .unwrap();
        let reconnect_deadline = tokio::time::Instant::now() + Duration::from_secs(5);
        while wildcard.lock().unwrap().len() < 65 || exact.lock().unwrap().len() < 65 {
            assert!(
                tokio::time::Instant::now() < reconnect_deadline,
                "Redis listener did not reconnect"
            );
            pubsub
                .publish("pond.test.one", reconnect_payload.clone())
                .await
                .unwrap();
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
        let expected = (0..=64).collect::<Vec<_>>();
        assert_eq!(*wildcard.lock().unwrap(), expected);
        assert_eq!(*exact.lock().unwrap(), expected);

        pubsub.close().await.unwrap();
        assert!(pubsub.publish("pond.test.one", vec![]).await.is_err());
        assert!(
            pubsub
                .subscribe(
                    "pond.test.one",
                    crate::pubsub::pubsub_handler(|_, _| async {}),
                )
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn live_redis_exact_to_pattern_transition_has_no_coverage_gap() {
        let Ok(url) = std::env::var("PONDSOCKET_TEST_REDIS_URL") else {
            return;
        };
        let _live_redis_guard = live_redis_test_lock().lock().await;
        let root = format!(
            "pond.transition.{}.{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        );
        let topic = format!("{root}.one");
        let pattern = format!("{root}.*");
        let pubsub = Arc::new(
            RedisPubSub::with_buffer_size(redis::Client::open(url).unwrap(), 256)
                .await
                .unwrap(),
        );
        let received = Arc::new(StdMutex::new(HashSet::new()));
        let exact_output = received.clone();
        let exact_id = pubsub
            .subscribe(
                &topic,
                crate::pubsub::pubsub_handler(move |_, payload| {
                    let exact_output = exact_output.clone();
                    async move {
                        let value: serde_json::Value = serde_json::from_slice(&payload).unwrap();
                        exact_output
                            .lock()
                            .unwrap()
                            .insert(value["sequence"].as_u64().unwrap());
                    }
                }),
            )
            .await
            .unwrap();

        let publisher = pubsub.clone();
        let publish_topic = topic.clone();
        let published = Arc::new(StdMutex::new(Vec::new()));
        let published_output = published.clone();
        let publishing = tokio::spawn(async move {
            for sequence in 0..200_u64 {
                let payload = serde_json::to_vec(&serde_json::json!({
                    "protocol": "pondsocket.distributed",
                    "version": 1,
                    "type": "USER_MESSAGE",
                    "messageId": format!("transition-{sequence}"),
                    "sequence": sequence,
                }))
                .unwrap();
                publisher.publish(&publish_topic, payload).await.unwrap();
                published_output.lock().unwrap().push(sequence);
                tokio::time::sleep(Duration::from_millis(2)).await;
            }
        });

        tokio::time::sleep(Duration::from_millis(30)).await;
        let pattern_output = received.clone();
        pubsub
            .subscribe_pattern(
                &pattern,
                crate::pubsub::pubsub_handler(move |_, payload| {
                    let pattern_output = pattern_output.clone();
                    async move {
                        let value: serde_json::Value = serde_json::from_slice(&payload).unwrap();
                        pattern_output
                            .lock()
                            .unwrap()
                            .insert(value["sequence"].as_u64().unwrap());
                    }
                }),
            )
            .await
            .unwrap();
        pubsub.unsubscribe(&topic, exact_id).await.unwrap();
        publishing.await.unwrap();

        let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
        loop {
            let published_len = published.lock().unwrap().len();
            if received.lock().unwrap().len() == published_len {
                break;
            }
            assert!(
                tokio::time::Instant::now() < deadline,
                "subscription transition lost published messages"
            );
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
        assert_eq!(received.lock().unwrap().len(), 200);
        pubsub.close().await.unwrap();
    }
}
