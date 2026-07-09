use async_trait::async_trait;
use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use tokio::sync::{RwLock, mpsc};

use crate::errors::{Result, not_found, unavailable};

pub type SubscriptionId = u64;

#[async_trait]
pub trait PubSub: Send + Sync {
    async fn subscribe(&self, pattern: &str, handler: PubSubHandler) -> Result<SubscriptionId>;
    async fn unsubscribe(&self, pattern: &str, id: SubscriptionId) -> Result<()>;
    async fn publish(&self, topic: &str, data: Vec<u8>) -> Result<()>;
    async fn close(&self) -> Result<()>;
}

pub type PubSubHandler = Arc<dyn Fn(String, Vec<u8>) + Send + Sync>;

#[derive(Clone)]
struct Subscription {
    id: SubscriptionId,
    tx: mpsc::Sender<(String, Vec<u8>)>,
}

pub struct LocalPubSub {
    subscriptions: RwLock<HashMap<String, Vec<Subscription>>>,
    next_id: AtomicU64,
    buffer_size: usize,
    closed: RwLock<bool>,
}

impl LocalPubSub {
    pub fn new(buffer_size: usize) -> Self {
        Self {
            subscriptions: RwLock::new(HashMap::new()),
            next_id: AtomicU64::new(0),
            buffer_size: buffer_size.max(1),
            closed: RwLock::new(false),
        }
    }
}

#[async_trait]
impl PubSub for LocalPubSub {
    async fn subscribe(&self, pattern: &str, handler: PubSubHandler) -> Result<SubscriptionId> {
        if *self.closed.read().await {
            return Err(unavailable("pubsub", "pubsub closed"));
        }
        let id = self.next_id.fetch_add(1, Ordering::SeqCst);
        let (tx, mut rx) = mpsc::channel(self.buffer_size);
        let handler = handler.clone();
        tokio::spawn(async move {
            while let Some((topic, data)) = rx.recv().await {
                let handler = handler.clone();
                tokio::spawn(async move { handler(topic, data) });
            }
        });
        self.subscriptions
            .write()
            .await
            .entry(pattern.to_owned())
            .or_default()
            .push(Subscription { id, tx });
        Ok(id)
    }

    async fn unsubscribe(&self, pattern: &str, id: SubscriptionId) -> Result<()> {
        if *self.closed.read().await {
            return Err(unavailable("pubsub", "pubsub closed"));
        }
        let mut subscriptions = self.subscriptions.write().await;
        let Some(list) = subscriptions.get_mut(pattern) else {
            return Err(not_found("pubsub", "pattern not found"));
        };
        list.retain(|subscription| subscription.id != id);
        if list.is_empty() {
            subscriptions.remove(pattern);
        }
        Ok(())
    }

    async fn publish(&self, topic: &str, data: Vec<u8>) -> Result<()> {
        if *self.closed.read().await {
            return Err(unavailable("pubsub", "pubsub closed"));
        }
        let subscriptions = self.subscriptions.read().await.clone();
        for (pattern, subs) in subscriptions {
            if match_topic(&pattern, topic) {
                for sub in subs {
                    let _ = sub.tx.try_send((topic.to_owned(), data.clone()));
                }
            }
        }
        Ok(())
    }

    async fn close(&self) -> Result<()> {
        *self.closed.write().await = true;
        self.subscriptions.write().await.clear();
        Ok(())
    }
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
    async fn adv_unsubscribe_removes_only_the_targeted_subscriber() {
        let pubsub = LocalPubSub::new(16);
        let first_hits = Arc::new(AtomicUsize::new(0));
        let second_hits = Arc::new(AtomicUsize::new(0));

        let first = first_hits.clone();
        let first_id = pubsub
            .subscribe(
                "topic.x",
                Arc::new(move |_topic, _data| {
                    first.fetch_add(1, Ordering::SeqCst);
                }),
            )
            .await
            .unwrap();
        let second = second_hits.clone();
        pubsub
            .subscribe(
                "topic.x",
                Arc::new(move |_topic, _data| {
                    second.fetch_add(1, Ordering::SeqCst);
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
}
