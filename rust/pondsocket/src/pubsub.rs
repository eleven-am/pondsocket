use async_trait::async_trait;
use futures_util::FutureExt;
use futures_util::future::BoxFuture;
use futures_util::stream::{FuturesUnordered, StreamExt};
use std::collections::HashMap;
use std::future::Future;
use std::panic::AssertUnwindSafe;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use tokio::sync::{Mutex, RwLock, mpsc, watch};
use tokio::task::JoinHandle;
use tokio::time::{Duration, timeout};

use crate::errors::{Result, not_found, unavailable};

const HANDLER_TIMEOUT: Duration = Duration::from_secs(30);
const SUPERVISED_HANDLER_TIMEOUT: Duration = Duration::from_secs(120);
const DELIVERY_TIMEOUT: Duration = Duration::from_millis(100);
const SUBSCRIPTION_DRAIN_TIMEOUT: Duration = Duration::from_secs(1);

pub type SubscriptionId = u64;
pub type PubSubFailureHandler = Arc<dyn Fn() + Send + Sync>;

#[async_trait]
pub trait PubSub: Send + Sync {
    /// Subscribes to one exact topic. Wildcard-looking topic names remain exact.
    async fn subscribe(&self, topic: &str, handler: PubSubHandler) -> Result<SubscriptionId>;
    /// Explicit opt-in wildcard subscription (`.*` suffix syntax).
    async fn subscribe_pattern(
        &self,
        pattern: &str,
        handler: PubSubHandler,
    ) -> Result<SubscriptionId> {
        self.subscribe(pattern, handler).await
    }
    /// Subscribes to an ordered internal stream whose failure must never be
    /// silent. Built-in implementations invoke `on_failure` if the callback
    /// panics, times out, or its bounded delivery queue overflows.
    async fn subscribe_supervised(
        &self,
        _topic: &str,
        _handler: PubSubHandler,
        _on_failure: PubSubFailureHandler,
    ) -> Result<SubscriptionId> {
        Err(unavailable(
            "pubsub",
            "implementation does not support supervised subscriptions",
        ))
    }
    async fn unsubscribe(&self, pattern: &str, id: SubscriptionId) -> Result<()>;
    async fn publish(&self, topic: &str, data: Vec<u8>) -> Result<()>;
    async fn close(&self) -> Result<()>;
}

/// Future returned by an asynchronous subscription callback.
pub type PubSubFuture = BoxFuture<'static, ()>;

/// Async callbacks let implementations preserve FIFO ordering while applying
/// bounded backpressure instead of spawning one task per message.
///
/// This intentionally replaces the former synchronous callback API.
pub type PubSubHandler = Arc<dyn Fn(String, Vec<u8>) -> PubSubFuture + Send + Sync>;

pub fn pubsub_handler<F, Fut>(handler: F) -> PubSubHandler
where
    F: Fn(String, Vec<u8>) -> Fut + Send + Sync + 'static,
    Fut: Future<Output = ()> + Send + 'static,
{
    Arc::new(move |topic, data| Box::pin(handler(topic, data)))
}

struct Subscription {
    id: SubscriptionId,
    tx: Option<mpsc::Sender<(String, Vec<u8>)>>,
    task: Option<JoinHandle<()>>,
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
    data: Vec<u8>,
    target: SupervisedHandler,
}

struct SupervisedDispatcher {
    tx: mpsc::Sender<SupervisedDelivery>,
    task: JoinHandle<()>,
}

pub(crate) struct FailureSignal {
    fired: std::sync::atomic::AtomicBool,
    handler: PubSubFailureHandler,
}

impl FailureSignal {
    pub(crate) fn new(handler: PubSubFailureHandler) -> Arc<Self> {
        Arc::new(Self {
            fired: std::sync::atomic::AtomicBool::new(false),
            handler,
        })
    }

    pub(crate) fn fire(&self) {
        if !self.fired.swap(true, Ordering::SeqCst) {
            let _ = std::panic::catch_unwind(AssertUnwindSafe(|| (self.handler)()));
        }
    }

    pub(crate) fn is_fired(&self) -> bool {
        self.fired.load(Ordering::SeqCst)
    }
}

#[derive(Default)]
struct LocalState {
    exact: HashMap<String, Vec<Subscription>>,
    patterns: HashMap<String, Vec<Subscription>>,
    closed: bool,
}

pub struct LocalPubSub {
    state: RwLock<LocalState>,
    next_id: AtomicU64,
    buffer_size: usize,
    supervised: Mutex<Option<SupervisedDispatcher>>,
}

impl LocalPubSub {
    pub fn new(buffer_size: usize) -> Self {
        Self {
            state: RwLock::new(LocalState::default()),
            next_id: AtomicU64::new(0),
            buffer_size: buffer_size.max(1),
            supervised: Mutex::new(None),
        }
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
            let mut dispatcher = self.supervised.lock().await;
            if dispatcher.is_none() {
                let (tx, rx) = mpsc::channel(self.buffer_size);
                *dispatcher = Some(SupervisedDispatcher {
                    tx,
                    task: tokio::spawn(run_supervised_worker(rx)),
                });
            }
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
            let (tx, mut rx) = mpsc::channel(self.buffer_size);
            let task = tokio::spawn(async move {
                while let Some((topic, data)) = rx.recv().await {
                    let outcome = timeout(
                        HANDLER_TIMEOUT,
                        AssertUnwindSafe(handler(topic, data)).catch_unwind(),
                    )
                    .await;
                    if !matches!(outcome, Ok(Ok(()))) {
                        break;
                    }
                }
            });
            (Some(tx), Some(task), None)
        };

        let mut state = self.state.write().await;
        if state.closed {
            drop(state);
            if let Some(task) = task {
                task.abort();
                let _ = task.await;
            }
            return Err(unavailable("pubsub", "pubsub closed"));
        }
        let subscriptions = if is_pattern {
            &mut state.patterns
        } else {
            &mut state.exact
        };
        subscriptions
            .entry(topic.to_owned())
            .or_default()
            .push(Subscription {
                id,
                tx,
                task,
                supervised,
            });
        Ok(id)
    }

    async fn remove_subscription(&self, topic: &str, id: SubscriptionId) -> Result<()> {
        let subscription = {
            let mut state = self.state.write().await;
            if state.closed {
                return Err(unavailable("pubsub", "pubsub closed"));
            }
            remove_subscription(&mut state.exact, topic, id)
                .or_else(|| remove_subscription(&mut state.patterns, topic, id))
                .ok_or_else(|| not_found("pubsub", "subscription not found"))?
        };
        stop_subscription(subscription, true).await;
        Ok(())
    }
}

async fn run_supervised_worker(mut rx: mpsc::Receiver<SupervisedDelivery>) {
    while let Some(delivery) = rx.recv().await {
        if delivery.target.failure.is_fired() || *delivery.target.cancelled.borrow() {
            continue;
        }
        let mut cancelled = delivery.target.cancelled.subscribe();
        let outcome = tokio::select! {
            biased;
            _ = wait_for_cancellation(&mut cancelled) => continue,
            outcome = timeout(
                SUPERVISED_HANDLER_TIMEOUT,
                AssertUnwindSafe((delivery.target.handler)(delivery.topic, delivery.data))
                    .catch_unwind(),
            ) => outcome,
        };
        if !matches!(outcome, Ok(Ok(()))) {
            delivery.target.failure.fire();
        }
    }
}

async fn wait_for_cancellation(cancelled: &mut watch::Receiver<bool>) {
    while !*cancelled.borrow_and_update() {
        if cancelled.changed().await.is_err() {
            return;
        }
    }
}

async fn stop_subscription(mut subscription: Subscription, drain: bool) {
    if let Some(supervised) = &subscription.supervised {
        supervised.cancelled.send_replace(true);
    }
    subscription.tx.take();
    if let Some(task) = subscription.task.take() {
        let mut task = task;
        if !drain
            || timeout(SUBSCRIPTION_DRAIN_TIMEOUT, &mut task)
                .await
                .is_err()
        {
            task.abort();
            let _ = task.await;
        }
    }
}

#[async_trait]
impl PubSub for LocalPubSub {
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
        self.remove_subscription(pattern, id).await
    }

    async fn publish(&self, topic: &str, data: Vec<u8>) -> Result<()> {
        let subscriptions = {
            let state = self.state.read().await;
            if state.closed {
                return Err(unavailable("pubsub", "pubsub closed"));
            }
            let mut matched = state
                .exact
                .get(topic)
                .into_iter()
                .flatten()
                .map(|subscription| {
                    (
                        topic.to_owned(),
                        subscription.id,
                        subscription.tx.clone(),
                        subscription.supervised.clone(),
                    )
                })
                .collect::<Vec<_>>();
            matched.extend(
                state
                    .patterns
                    .iter()
                    .filter(|(pattern, _)| match_topic(pattern, topic))
                    .flat_map(|(pattern, subscriptions)| {
                        subscriptions.iter().map(move |subscription| {
                            (
                                pattern.clone(),
                                subscription.id,
                                subscription.tx.clone(),
                                subscription.supervised.clone(),
                            )
                        })
                    }),
            );
            matched
        };
        let supervised_tx = self
            .supervised
            .lock()
            .await
            .as_ref()
            .map(|dispatcher| dispatcher.tx.clone());
        let mut deliveries = FuturesUnordered::new();
        let mut failed = Vec::new();
        for (subscription_topic, id, sender, supervised) in subscriptions {
            if let Some(supervised) = supervised {
                let delivered = supervised_tx.as_ref().is_some_and(|tx| {
                    tx.try_send(SupervisedDelivery {
                        topic: topic.to_owned(),
                        data: data.clone(),
                        target: supervised.clone(),
                    })
                    .is_ok()
                });
                if !delivered {
                    supervised.failure.fire();
                    failed.push((subscription_topic, id));
                }
                continue;
            }
            let Some(sender) = sender else {
                failed.push((subscription_topic, id));
                continue;
            };
            let topic = topic.to_owned();
            let data = data.clone();
            deliveries.push(async move {
                let result = timeout(DELIVERY_TIMEOUT, sender.send((topic, data))).await;
                (subscription_topic, id, result)
            });
        }

        while let Some((subscription_topic, id, result)) = deliveries.next().await {
            if !matches!(result, Ok(Ok(()))) {
                failed.push((subscription_topic, id));
            }
        }
        for (subscription_topic, id) in failed {
            let subscription = {
                let mut state = self.state.write().await;
                remove_subscription(&mut state.exact, &subscription_topic, id)
                    .or_else(|| remove_subscription(&mut state.patterns, &subscription_topic, id))
            };
            if let Some(subscription) = subscription {
                stop_subscription(subscription, false).await;
            }
        }
        Ok(())
    }

    async fn close(&self) -> Result<()> {
        let subscriptions = {
            let mut state = self.state.write().await;
            if state.closed {
                return Ok(());
            }
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
        for subscription in subscriptions {
            stop_subscription(subscription, false).await;
        }
        if let Some(dispatcher) = self.supervised.lock().await.take() {
            dispatcher.task.abort();
            let _ = dispatcher.task.await;
        }
        Ok(())
    }
}

fn remove_subscription(
    subscriptions: &mut HashMap<String, Vec<Subscription>>,
    topic: &str,
    id: SubscriptionId,
) -> Option<Subscription> {
    let list = subscriptions.get_mut(topic)?;
    let index = list.iter().position(|subscription| subscription.id == id)?;
    let subscription = list.remove(index);
    if list.is_empty() {
        subscriptions.remove(topic);
    }
    Some(subscription)
}

pub fn match_topic(pattern: &str, topic: &str) -> bool {
    if pattern == topic {
        return true;
    }
    if let Some(prefix) = pattern.strip_suffix(".*") {
        return topic.starts_with(prefix);
    }
    false
}

pub fn format_topic(namespace: &str, endpoint: &str, channel: &str) -> String {
    format!("pondsocket:v1:{namespace}:{endpoint}:{channel}")
}

pub fn format_heartbeat_topic(namespace: &str) -> String {
    format!("pondsocket:v1:{namespace}:__heartbeat__")
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;
    use std::sync::atomic::{AtomicUsize, Ordering};

    async fn wait_until(counter: &AtomicUsize, target: usize) {
        let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(2);
        while counter.load(Ordering::SeqCst) < target {
            if tokio::time::Instant::now() > deadline {
                panic!("counter never reached {target}");
            }
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
        }
    }

    #[tokio::test]
    async fn adv_subscription_delivery_is_fifo() {
        let pubsub = LocalPubSub::new(2);
        let received = Arc::new(Mutex::new(Vec::new()));
        let output = received.clone();
        pubsub
            .subscribe(
                "topic.x",
                pubsub_handler(move |_topic, data| {
                    let output = output.clone();
                    async move {
                        output.lock().unwrap().push(data[0]);
                    }
                }),
            )
            .await
            .unwrap();

        for value in 0..64 {
            pubsub.publish("topic.x", vec![value]).await.unwrap();
        }

        let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(2);
        while received.lock().unwrap().len() < 64 {
            assert!(tokio::time::Instant::now() < deadline, "delivery stalled");
            tokio::task::yield_now().await;
        }
        assert_eq!(*received.lock().unwrap(), (0..64).collect::<Vec<_>>());
    }

    #[tokio::test]
    async fn adv_wildcard_looking_topic_is_exact_unless_explicitly_patterned() {
        let pubsub = LocalPubSub::new(8);
        let exact_hits = Arc::new(AtomicUsize::new(0));
        let pattern_hits = Arc::new(AtomicUsize::new(0));
        let exact = exact_hits.clone();
        pubsub
            .subscribe(
                "room.*",
                pubsub_handler(move |_, _| {
                    let exact = exact.clone();
                    async move {
                        exact.fetch_add(1, Ordering::SeqCst);
                    }
                }),
            )
            .await
            .unwrap();
        let pattern = pattern_hits.clone();
        pubsub
            .subscribe_pattern(
                "room.*",
                pubsub_handler(move |_, _| {
                    let pattern = pattern.clone();
                    async move {
                        pattern.fetch_add(1, Ordering::SeqCst);
                    }
                }),
            )
            .await
            .unwrap();

        pubsub.publish("room.secret", vec![]).await.unwrap();
        wait_until(&pattern_hits, 1).await;
        assert_eq!(exact_hits.load(Ordering::SeqCst), 0);
        pubsub.publish("room.*", vec![]).await.unwrap();
        wait_until(&exact_hits, 1).await;
    }

    #[tokio::test]
    async fn adv_close_is_terminal_and_idempotent() {
        let pubsub = LocalPubSub::new(1);
        pubsub.close().await.unwrap();
        pubsub.close().await.unwrap();
        assert!(pubsub.publish("topic", vec![]).await.is_err());
        assert!(
            pubsub
                .subscribe("topic", pubsub_handler(|_, _| async {}))
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn adv_unknown_subscription_is_reported() {
        let pubsub = LocalPubSub::new(1);
        let id = pubsub
            .subscribe("topic", pubsub_handler(|_, _| async {}))
            .await
            .unwrap();
        assert!(pubsub.unsubscribe("topic", id + 1).await.is_err());
        pubsub.unsubscribe("topic", id).await.unwrap();
    }

    #[tokio::test]
    async fn adv_unsubscribe_removes_only_the_targeted_subscriber() {
        let pubsub = LocalPubSub::new(16);
        let first_hits = Arc::new(AtomicUsize::new(0));
        let second_hits = Arc::new(AtomicUsize::new(0));

        let first = first_hits.clone();
        let first_id = pubsub
            .subscribe(
                "topic.x",
                pubsub_handler(move |_topic, _data| {
                    let first = first.clone();
                    async move {
                        first.fetch_add(1, Ordering::SeqCst);
                    }
                }),
            )
            .await
            .unwrap();
        let second = second_hits.clone();
        pubsub
            .subscribe(
                "topic.x",
                pubsub_handler(move |_topic, _data| {
                    let second = second.clone();
                    async move {
                        second.fetch_add(1, Ordering::SeqCst);
                    }
                }),
            )
            .await
            .unwrap();

        pubsub.publish("topic.x", vec![1]).await.unwrap();
        wait_until(&first_hits, 1).await;
        wait_until(&second_hits, 1).await;

        pubsub.unsubscribe("topic.x", first_id).await.unwrap();
        let first_before = first_hits.load(Ordering::SeqCst);
        pubsub.publish("topic.x", vec![2]).await.unwrap();
        wait_until(&second_hits, 2).await;

        assert_eq!(first_hits.load(Ordering::SeqCst), first_before);
        assert_eq!(second_hits.load(Ordering::SeqCst), 2);
    }

    #[tokio::test]
    async fn panicking_subscriber_is_evicted_without_poisoning_healthy_subscribers() {
        let pubsub = LocalPubSub::new(4);
        let panicked = Arc::new(tokio::sync::Notify::new());
        let panic_signal = panicked.clone();
        pubsub
            .subscribe(
                "topic.x",
                pubsub_handler(move |_, _| {
                    let panic_signal = panic_signal.clone();
                    async move {
                        panic_signal.notify_one();
                        panic!("intentional subscriber panic");
                    }
                }),
            )
            .await
            .unwrap();

        let healthy_hits = Arc::new(AtomicUsize::new(0));
        let healthy = healthy_hits.clone();
        pubsub
            .subscribe(
                "topic.x",
                pubsub_handler(move |_, _| {
                    let healthy = healthy.clone();
                    async move {
                        healthy.fetch_add(1, Ordering::SeqCst);
                    }
                }),
            )
            .await
            .unwrap();

        pubsub.publish("topic.x", vec![1]).await.unwrap();
        panicked.notified().await;
        tokio::task::yield_now().await;
        pubsub.publish("topic.x", vec![2]).await.unwrap();
        wait_until(&healthy_hits, 2).await;
        pubsub.publish("topic.x", vec![3]).await.unwrap();
        wait_until(&healthy_hits, 3).await;
    }

    #[tokio::test]
    async fn slow_subscriber_is_bounded_and_does_not_block_healthy_delivery() {
        let pubsub = LocalPubSub::new(1);
        let started = Arc::new(tokio::sync::Notify::new());
        let slow_started = started.clone();
        pubsub
            .subscribe(
                "topic.x",
                pubsub_handler(move |_, _| {
                    let slow_started = slow_started.clone();
                    async move {
                        slow_started.notify_one();
                        std::future::pending::<()>().await;
                    }
                }),
            )
            .await
            .unwrap();

        let healthy_hits = Arc::new(AtomicUsize::new(0));
        let healthy = healthy_hits.clone();
        pubsub
            .subscribe(
                "topic.x",
                pubsub_handler(move |_, _| {
                    let healthy = healthy.clone();
                    async move {
                        healthy.fetch_add(1, Ordering::SeqCst);
                    }
                }),
            )
            .await
            .unwrap();

        pubsub.publish("topic.x", vec![1]).await.unwrap();
        started.notified().await;
        pubsub.publish("topic.x", vec![2]).await.unwrap();
        pubsub.publish("topic.x", vec![3]).await.unwrap();
        wait_until(&healthy_hits, 3).await;
        pubsub.publish("topic.x", vec![4]).await.unwrap();
        wait_until(&healthy_hits, 4).await;
    }

    #[tokio::test]
    async fn unsubscribe_cancels_and_awaits_running_handler() {
        struct DropSignal(Arc<AtomicUsize>);
        impl Drop for DropSignal {
            fn drop(&mut self) {
                self.0.fetch_add(1, Ordering::SeqCst);
            }
        }

        let pubsub = LocalPubSub::new(1);
        let started = Arc::new(tokio::sync::Notify::new());
        let handler_started = started.clone();
        let dropped = Arc::new(AtomicUsize::new(0));
        let handler_dropped = dropped.clone();
        let id = pubsub
            .subscribe(
                "topic.x",
                pubsub_handler(move |_, _| {
                    let handler_started = handler_started.clone();
                    let guard = DropSignal(handler_dropped.clone());
                    async move {
                        let _guard = guard;
                        handler_started.notify_one();
                        std::future::pending::<()>().await;
                    }
                }),
            )
            .await
            .unwrap();

        pubsub.publish("topic.x", vec![]).await.unwrap();
        started.notified().await;
        pubsub.unsubscribe("topic.x", id).await.unwrap();
        assert_eq!(dropped.load(Ordering::SeqCst), 1);
    }

    #[tokio::test]
    async fn supervised_handlers_preserve_global_publish_order_across_topics() {
        let pubsub = LocalPubSub::new(8);
        let received = Arc::new(tokio::sync::Mutex::new(Vec::new()));
        let first = received.clone();
        pubsub
            .subscribe_supervised(
                "topic.a",
                pubsub_handler(move |_, _| {
                    let first = first.clone();
                    async move {
                        tokio::time::sleep(Duration::from_millis(25)).await;
                        first.lock().await.push("a");
                    }
                }),
                Arc::new(|| {}),
            )
            .await
            .unwrap();
        let second = received.clone();
        pubsub
            .subscribe_supervised(
                "topic.b",
                pubsub_handler(move |_, _| {
                    let second = second.clone();
                    async move {
                        second.lock().await.push("b");
                    }
                }),
                Arc::new(|| {}),
            )
            .await
            .unwrap();

        pubsub.publish("topic.a", vec![]).await.unwrap();
        pubsub.publish("topic.b", vec![]).await.unwrap();
        tokio::time::timeout(Duration::from_secs(1), async {
            while received.lock().await.len() < 2 {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
        assert_eq!(*received.lock().await, ["a", "b"]);
    }

    #[tokio::test]
    async fn supervised_overflow_fails_visibly_instead_of_silent_desync() {
        let pubsub = LocalPubSub::new(1);
        let started = Arc::new(tokio::sync::Notify::new());
        let handler_started = started.clone();
        let failures = Arc::new(AtomicUsize::new(0));
        let failure_count = failures.clone();
        pubsub
            .subscribe_supervised(
                "topic",
                pubsub_handler(move |_, _| {
                    let handler_started = handler_started.clone();
                    async move {
                        handler_started.notify_one();
                        std::future::pending::<()>().await;
                    }
                }),
                Arc::new(move || {
                    failure_count.fetch_add(1, Ordering::SeqCst);
                }),
            )
            .await
            .unwrap();

        pubsub.publish("topic", vec![1]).await.unwrap();
        started.notified().await;
        pubsub.publish("topic", vec![2]).await.unwrap();
        pubsub.publish("topic", vec![3]).await.unwrap();
        assert_eq!(failures.load(Ordering::SeqCst), 1);
        assert!(pubsub.state.read().await.exact.is_empty());
    }
}
