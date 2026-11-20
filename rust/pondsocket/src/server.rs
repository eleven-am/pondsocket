use pondsocket_common::uuid;
use std::sync::Arc;
use tokio::sync::{Mutex, RwLock};

use crate::contexts::IncomingConnection;
use crate::endpoint::{ConnectionHandler, Endpoint};
use crate::errors::Result;
use crate::liveness::NodeLiveness;
use crate::parser::parse;
use crate::pubsub::PubSub;
use crate::transport::Transport;
use crate::types::{Options, Route};

pub struct EndpointMatch {
    pub endpoint: Arc<Endpoint>,
    pub route: Route,
}

pub struct PondSocket {
    options: Options,
    pubsub: Option<Arc<dyn PubSub>>,
    endpoints: RwLock<Vec<Arc<Endpoint>>>,
    liveness: Arc<NodeLiveness>,
    lifecycle: Mutex<PondLifecycle>,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum PondLifecycle {
    Open,
    Closing,
    Closed,
}

impl PondSocket {
    pub fn new(options: Options, pubsub: Option<Arc<dyn PubSub>>) -> Arc<Self> {
        let mut options = options.normalized();
        if pubsub.is_some() && options.node_id.is_empty() {
            options.node_id = uuid();
        }
        let liveness = NodeLiveness::new(&options, pubsub.clone());
        Arc::new(Self {
            options,
            pubsub,
            endpoints: RwLock::new(Vec::new()),
            liveness,
            lifecycle: Mutex::new(PondLifecycle::Open),
        })
    }

    pub fn with_defaults() -> Arc<Self> {
        Self::new(Options::default(), None)
    }

    pub async fn create_endpoint<H>(
        self: &Arc<Self>,
        path: impl Into<String>,
        handler: H,
    ) -> Result<Arc<Endpoint>>
    where
        H: ConnectionHandler + 'static,
    {
        let lifecycle = self.lifecycle.lock().await;
        if *lifecycle != PondLifecycle::Open {
            return Err(crate::errors::internal(
                "pondsocket",
                "server is closing or closed",
            ));
        }
        let endpoint = Endpoint::new_with_liveness(
            path,
            handler,
            self.options.clone(),
            self.pubsub.clone(),
            self.liveness.clone(),
        );
        self.endpoints.write().await.push(endpoint.clone());
        drop(lifecycle);
        Ok(endpoint)
    }

    pub async fn match_endpoint(&self, path: &str) -> Option<EndpointMatch> {
        for endpoint in self.endpoints.read().await.iter() {
            if let Ok(route) = parse(endpoint.path(), path) {
                return Some(EndpointMatch {
                    endpoint: endpoint.clone(),
                    route,
                });
            }
        }
        None
    }

    pub async fn request_connection(
        &self,
        path: &str,
        incoming: IncomingConnection,
        user_id: Option<String>,
    ) -> Option<(Arc<Endpoint>, crate::contexts::ConnectionContext)> {
        let matched = self.match_endpoint(path).await?;
        let ctx = matched
            .endpoint
            .request_connection(incoming, matched.route, user_id)
            .await;
        Some((matched.endpoint, ctx))
    }

    pub async fn find_transport(&self, user_id: &str) -> Option<Arc<dyn Transport>> {
        let endpoints = self.endpoints.read().await.clone();
        for endpoint in endpoints {
            if let Some(transport) = endpoint.get_transport(user_id).await {
                return Some(transport);
            }
        }
        None
    }

    pub async fn close(&self) -> Result<()> {
        let mut lifecycle = self.lifecycle.lock().await;
        if *lifecycle == PondLifecycle::Closed {
            return Ok(());
        }
        *lifecycle = PondLifecycle::Closing;
        let endpoints = self.endpoints.read().await.clone();
        let mut first_error = None;
        for endpoint in endpoints {
            if let Err(error) = endpoint.close().await
                && first_error.is_none()
            {
                first_error = Some(error);
            }
        }
        self.endpoints.write().await.clear();
        if let Err(error) = self.liveness.close().await
            && first_error.is_none()
        {
            first_error = Some(error);
        }
        if let Some(pubsub) = &self.pubsub
            && let Err(error) = pubsub.close().await
            && first_error.is_none()
        {
            first_error = Some(error);
        }
        *lifecycle = PondLifecycle::Closed;
        match first_error {
            Some(error) => Err(error),
            None => Ok(()),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::contexts::{ConnectionContext, JoinContext};
    use crate::pubsub::{PubSubHandler, SubscriptionId, format_heartbeat_topic};
    use async_trait::async_trait;
    use std::collections::HashMap;
    use std::sync::Mutex;
    use std::sync::atomic::{AtomicU64, Ordering};

    #[derive(Default)]
    struct CountingPubSub {
        counts: Mutex<HashMap<String, usize>>,
        next_id: AtomicU64,
    }

    impl CountingPubSub {
        fn count(&self, topic: &str) -> usize {
            *self.counts.lock().unwrap().get(topic).unwrap_or(&0)
        }
    }

    #[async_trait]
    impl PubSub for CountingPubSub {
        async fn subscribe(&self, topic: &str, _handler: PubSubHandler) -> Result<SubscriptionId> {
            *self
                .counts
                .lock()
                .unwrap()
                .entry(topic.to_owned())
                .or_default() += 1;
            Ok(self.next_id.fetch_add(1, Ordering::SeqCst))
        }

        async fn subscribe_supervised(
            &self,
            topic: &str,
            handler: PubSubHandler,
            _on_failure: crate::pubsub::PubSubFailureHandler,
        ) -> Result<SubscriptionId> {
            self.subscribe(topic, handler).await
        }

        async fn unsubscribe(&self, topic: &str, _id: SubscriptionId) -> Result<()> {
            let mut counts = self.counts.lock().unwrap();
            if let Some(count) = counts.get_mut(topic) {
                *count -= 1;
                if *count == 0 {
                    counts.remove(topic);
                }
            }
            Ok(())
        }

        async fn publish(&self, _topic: &str, _data: Vec<u8>) -> Result<()> {
            Ok(())
        }

        async fn close(&self) -> Result<()> {
            Ok(())
        }
    }

    struct AcceptConnection;

    #[async_trait]
    impl ConnectionHandler for AcceptConnection {
        async fn call(&self, ctx: &mut ConnectionContext) -> Result<()> {
            ctx.accept(Default::default())?;
            Ok(())
        }
    }

    struct AcceptJoin;

    #[async_trait]
    impl crate::endpoint::JoinHandler for AcceptJoin {
        async fn call(&self, ctx: &mut JoinContext) -> Result<()> {
            ctx.accept(Default::default()).await?;
            Ok(())
        }
    }

    #[tokio::test]
    async fn many_endpoints_and_channels_share_one_node_liveness_runtime() {
        let pubsub = Arc::new(CountingPubSub::default());
        let pond = PondSocket::new(Options::default(), Some(pubsub.clone()));

        for endpoint_index in 0..4 {
            let endpoint = pond
                .create_endpoint(format!("/socket/{endpoint_index}"), AcceptConnection)
                .await
                .unwrap();
            let lobby = endpoint
                .create_channel("/room/:id", AcceptJoin)
                .await
                .unwrap();
            for channel_index in 0..32 {
                lobby
                    .get_or_create_channel(&format!("/room/{channel_index}"))
                    .await
                    .unwrap();
            }
        }

        let heartbeat_topic = format_heartbeat_topic("default");
        assert_eq!(pubsub.count(&heartbeat_topic), 1);
        assert_eq!(pond.liveness.task_count().await, 2);

        pond.close().await.unwrap();
        assert_eq!(pubsub.count(&heartbeat_topic), 0);
        assert_eq!(pond.liveness.task_count().await, 0);
    }

    #[tokio::test]
    async fn close_serializes_with_endpoint_creation_and_rejects_post_close_creation() {
        let pond = PondSocket::with_defaults();
        let endpoints_guard = pond.endpoints.write().await;
        let create_pond = pond.clone();
        let create = tokio::spawn(async move {
            create_pond
                .create_endpoint("/racing", AcceptConnection)
                .await
        });
        tokio::time::timeout(std::time::Duration::from_secs(1), async {
            loop {
                if pond.lifecycle.try_lock().is_err() {
                    break;
                }
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("endpoint creation never acquired the server lifecycle lock");

        let close_pond = pond.clone();
        let close = tokio::spawn(async move { close_pond.close().await });
        tokio::task::yield_now().await;
        assert!(!close.is_finished());
        drop(endpoints_guard);

        let endpoint = create.await.unwrap().unwrap();
        close.await.unwrap().unwrap();
        assert!(pond.endpoints.read().await.is_empty());
        assert!(
            endpoint
                .create_channel("/late/:id", AcceptJoin)
                .await
                .is_err()
        );
        assert!(
            pond.create_endpoint("/late", AcceptConnection)
                .await
                .is_err()
        );
        pond.close().await.unwrap();
    }
}
