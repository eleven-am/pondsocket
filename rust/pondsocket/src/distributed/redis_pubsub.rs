use async_trait::async_trait;
use redis::AsyncCommands;
use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use tokio::sync::RwLock;
use tokio::task::JoinHandle;
use tokio_stream::StreamExt;

use crate::errors::{Result, unavailable};
use crate::pubsub::{PubSub, PubSubHandler, SubscriptionId, match_topic};

#[derive(Clone)]
struct RedisSubscription {
    id: SubscriptionId,
    handler: PubSubHandler,
}

pub struct RedisPubSub {
    client: redis::Client,
    subscriptions: Arc<RwLock<HashMap<String, Vec<RedisSubscription>>>>,
    listeners: Arc<RwLock<HashMap<String, JoinHandle<()>>>>,
    next_id: AtomicU64,
}

impl RedisPubSub {
    pub async fn new(client: redis::Client) -> Result<Self> {
        let mut conn = client
            .get_multiplexed_async_connection()
            .await
            .map_err(|e| unavailable("redis", e.to_string()))?;
        let _: String = redis::cmd("PING")
            .query_async(&mut conn)
            .await
            .map_err(|e| unavailable("redis", e.to_string()))?;
        Ok(Self {
            client,
            subscriptions: Arc::new(RwLock::new(HashMap::new())),
            listeners: Arc::new(RwLock::new(HashMap::new())),
            next_id: AtomicU64::new(0),
        })
    }

    async fn ensure_listener(&self, pattern: String) -> Result<()> {
        if self.listeners.read().await.contains_key(&pattern) {
            return Ok(());
        }
        let client = self.client.clone();
        let subscriptions = self.subscriptions.clone();
        let listener_pattern = pattern.clone();
        let handle = tokio::spawn(async move {
            let Ok(mut pubsub) = client.get_async_pubsub().await else {
                return;
            };
            if pubsub
                .psubscribe(to_redis_pattern(&listener_pattern))
                .await
                .is_err()
            {
                return;
            }
            let mut stream = pubsub.on_message();
            while let Some(msg) = stream.next().await {
                let topic = msg.get_channel_name().to_owned();
                let Ok(payload) = msg.get_payload::<Vec<u8>>() else {
                    continue;
                };
                let snapshot = subscriptions.read().await.clone();
                for (local_pattern, handlers) in snapshot {
                    if match_topic(&local_pattern, &topic) {
                        for subscription in handlers {
                            (subscription.handler)(topic.clone(), payload.clone());
                        }
                    }
                }
            }
        });
        let mut listeners = self.listeners.write().await;
        if let Some(existing) = listeners.insert(pattern, handle) {
            existing.abort();
        }
        Ok(())
    }
}

#[async_trait]
impl PubSub for RedisPubSub {
    async fn subscribe(&self, pattern: &str, handler: PubSubHandler) -> Result<SubscriptionId> {
        let id = self.next_id.fetch_add(1, Ordering::SeqCst);
        let mut subs = self.subscriptions.write().await;
        let first = !subs.contains_key(pattern);
        subs.entry(pattern.to_owned())
            .or_default()
            .push(RedisSubscription { id, handler });
        drop(subs);
        if first {
            self.ensure_listener(pattern.to_owned()).await?;
        }
        Ok(id)
    }

    async fn unsubscribe(&self, pattern: &str, id: SubscriptionId) -> Result<()> {
        let mut subs = self.subscriptions.write().await;
        let now_empty = match subs.get_mut(pattern) {
            Some(list) => {
                list.retain(|subscription| subscription.id != id);
                list.is_empty()
            }
            None => false,
        };
        if now_empty {
            subs.remove(pattern);
            drop(subs);
            if let Some(handle) = self.listeners.write().await.remove(pattern) {
                handle.abort();
            }
        }
        Ok(())
    }

    async fn publish(&self, topic: &str, data: Vec<u8>) -> Result<()> {
        let mut conn = self
            .client
            .get_multiplexed_async_connection()
            .await
            .map_err(|e| unavailable("redis", e.to_string()))?;
        let _: usize = conn
            .publish(topic, data)
            .await
            .map_err(|e| unavailable("redis", e.to_string()))?;
        Ok(())
    }

    async fn close(&self) -> Result<()> {
        self.subscriptions.write().await.clear();
        for (_, handle) in self.listeners.write().await.drain() {
            handle.abort();
        }
        Ok(())
    }
}

fn to_redis_pattern(pattern: &str) -> String {
    pattern
        .strip_suffix(".*")
        .map_or_else(|| pattern.to_owned(), |prefix| format!("{prefix}*"))
}
