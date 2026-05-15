use async_trait::async_trait;
use redis::AsyncCommands;
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::RwLock;
use tokio_stream::StreamExt;

use crate::errors::{Result, unavailable};
use crate::pubsub::{PubSub, PubSubHandler, match_topic};

pub struct RedisPubSub {
    client: redis::Client,
    subscriptions: Arc<RwLock<HashMap<String, Vec<PubSubHandler>>>>,
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
        })
    }

    async fn ensure_listener(&self, pattern: String) -> Result<()> {
        let client = self.client.clone();
        let subscriptions = self.subscriptions.clone();
        tokio::spawn(async move {
            let Ok(mut pubsub) = client.get_async_pubsub().await else {
                return;
            };
            if pubsub.psubscribe(to_redis_pattern(&pattern)).await.is_err() {
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
                        for handler in handlers {
                            handler(topic.clone(), payload.clone());
                        }
                    }
                }
            }
        });
        Ok(())
    }
}

#[async_trait]
impl PubSub for RedisPubSub {
    async fn subscribe(&self, pattern: &str, handler: PubSubHandler) -> Result<()> {
        let mut subs = self.subscriptions.write().await;
        let first = !subs.contains_key(pattern);
        subs.entry(pattern.to_owned()).or_default().push(handler);
        drop(subs);
        if first {
            self.ensure_listener(pattern.to_owned()).await?;
        }
        Ok(())
    }

    async fn unsubscribe(&self, pattern: &str) -> Result<()> {
        self.subscriptions.write().await.remove(pattern);
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
        Ok(())
    }
}

fn to_redis_pattern(pattern: &str) -> String {
    pattern
        .strip_suffix(".*")
        .map_or_else(|| pattern.to_owned(), |prefix| format!("{prefix}*"))
}
