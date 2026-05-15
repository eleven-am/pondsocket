use async_trait::async_trait;
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::{RwLock, mpsc};

use crate::errors::{Result, not_found, unavailable};

#[async_trait]
pub trait PubSub: Send + Sync {
    async fn subscribe(&self, pattern: &str, handler: PubSubHandler) -> Result<()>;
    async fn unsubscribe(&self, pattern: &str) -> Result<()>;
    async fn publish(&self, topic: &str, data: Vec<u8>) -> Result<()>;
    async fn close(&self) -> Result<()>;
}

pub type PubSubHandler = Arc<dyn Fn(String, Vec<u8>) + Send + Sync>;

#[derive(Clone)]
struct Subscription {
    tx: mpsc::Sender<(String, Vec<u8>)>,
}

pub struct LocalPubSub {
    subscriptions: RwLock<HashMap<String, Vec<Subscription>>>,
    buffer_size: usize,
    closed: RwLock<bool>,
}

impl LocalPubSub {
    pub fn new(buffer_size: usize) -> Self {
        Self {
            subscriptions: RwLock::new(HashMap::new()),
            buffer_size: buffer_size.max(1),
            closed: RwLock::new(false),
        }
    }
}

#[async_trait]
impl PubSub for LocalPubSub {
    async fn subscribe(&self, pattern: &str, handler: PubSubHandler) -> Result<()> {
        if *self.closed.read().await {
            return Err(unavailable("pubsub", "pubsub closed"));
        }
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
            .push(Subscription { tx });
        Ok(())
    }

    async fn unsubscribe(&self, pattern: &str) -> Result<()> {
        if *self.closed.read().await {
            return Err(unavailable("pubsub", "pubsub closed"));
        }
        let removed = self.subscriptions.write().await.remove(pattern);
        if removed.is_none() {
            return Err(not_found("pubsub", "pattern not found"));
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

pub fn format_topic(endpoint: &str, channel: &str, _event: &str) -> String {
    format!("pondsocket:v1:default:{endpoint}:{channel}")
}

pub fn format_heartbeat_topic() -> String {
    "pondsocket:v1:default:__heartbeat__".to_owned()
}
