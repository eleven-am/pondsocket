use async_trait::async_trait;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::Mutex;

use crate::errors::PondError;
use crate::types::{Event, User};

#[async_trait]
pub trait RateLimiter: Send + Sync {
    async fn allow(&self, key: &str) -> bool;
    async fn reset(&self, key: &str);
}

pub trait MetricsCollector: Send + Sync {
    fn connection_opened(&self, _conn_id: &str, _endpoint: &str) {}
    fn connection_closed(&self, _conn_id: &str, _duration: Duration) {}
    fn channel_joined(&self, _user_id: &str, _channel: &str) {}
    fn channel_left(&self, _user_id: &str, _channel: &str) {}
    fn channel_created(&self, _channel: &str) {}
    fn channel_destroyed(&self, _channel: &str) {}
    fn error(&self, _component: &str, _err: &PondError) {}
}

#[async_trait]
pub trait ConnectHook: Send + Sync {
    async fn call(&self, user_id: &str);
}

#[async_trait]
impl<F, Fut> ConnectHook for F
where
    F: Fn(&str) -> Fut + Send + Sync,
    Fut: std::future::Future<Output = ()> + Send,
{
    async fn call(&self, user_id: &str) {
        self(user_id).await;
    }
}

#[async_trait]
pub trait JoinHook: Send + Sync {
    async fn call(&self, user: &User, channel: &str);
}

#[async_trait]
impl<F, Fut> JoinHook for F
where
    F: Fn(&User, &str) -> Fut + Send + Sync,
    Fut: std::future::Future<Output = ()> + Send,
{
    async fn call(&self, user: &User, channel: &str) {
        self(user, channel).await;
    }
}

#[async_trait]
pub trait MessageHook: Send + Sync {
    async fn call(&self, event: &Event, err: Option<&PondError>);
}

#[async_trait]
impl<F, Fut> MessageHook for F
where
    F: Fn(&Event, Option<&PondError>) -> Fut + Send + Sync,
    Fut: std::future::Future<Output = ()> + Send,
{
    async fn call(&self, event: &Event, err: Option<&PondError>) {
        self(event, err).await;
    }
}

#[derive(Default)]
pub struct Hooks {
    pub rate_limiter: Option<Arc<dyn RateLimiter>>,
    pub metrics: Option<Arc<dyn MetricsCollector>>,
    pub on_connect: Option<Arc<dyn ConnectHook>>,
    pub on_disconnect: Option<Arc<dyn ConnectHook>>,
    pub before_join: Option<Arc<dyn JoinHook>>,
    pub after_join: Option<Arc<dyn JoinHook>>,
    pub before_message: Option<Arc<dyn MessageHook>>,
    pub after_message: Option<Arc<dyn MessageHook>>,
}

pub struct TokenBucketRateLimiter {
    rate: f64,
    capacity: f64,
    buckets: Mutex<std::collections::HashMap<String, Bucket>>,
}

struct Bucket {
    tokens: f64,
    last_refill: Instant,
}

impl TokenBucketRateLimiter {
    pub fn new(rate: f64, capacity: f64) -> Self {
        assert!(rate > 0.0, "rate must be > 0");
        assert!(capacity > 0.0, "capacity must be > 0");
        Self {
            rate,
            capacity,
            buckets: Mutex::new(std::collections::HashMap::new()),
        }
    }
}

#[async_trait]
impl RateLimiter for TokenBucketRateLimiter {
    async fn allow(&self, key: &str) -> bool {
        let mut buckets = self.buckets.lock().await;
        let now = Instant::now();
        let bucket = buckets.entry(key.to_owned()).or_insert(Bucket {
            tokens: self.capacity,
            last_refill: now,
        });
        let elapsed = now.duration_since(bucket.last_refill).as_secs_f64();
        bucket.tokens = self.capacity.min(bucket.tokens + elapsed * self.rate);
        bucket.last_refill = now;
        if bucket.tokens >= 1.0 {
            bucket.tokens -= 1.0;
            true
        } else {
            false
        }
    }

    async fn reset(&self, key: &str) {
        self.buckets.lock().await.remove(key);
    }
}
