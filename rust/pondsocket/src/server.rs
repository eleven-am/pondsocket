use pondsocket_common::uuid;
use std::sync::Arc;
use tokio::sync::RwLock;

use crate::contexts::IncomingConnection;
use crate::endpoint::{ConnectionHandler, Endpoint};
use crate::errors::Result;
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
}

impl PondSocket {
    pub fn new(options: Options, pubsub: Option<Arc<dyn PubSub>>) -> Arc<Self> {
        let mut options = options;
        if pubsub.is_some() && options.node_id.is_empty() {
            options.node_id = uuid();
        }
        Arc::new(Self {
            options,
            pubsub,
            endpoints: RwLock::new(Vec::new()),
        })
    }

    pub fn with_defaults() -> Arc<Self> {
        Self::new(Options::default(), None)
    }

    pub async fn create_endpoint<H>(
        self: &Arc<Self>,
        path: impl Into<String>,
        handler: H,
    ) -> Arc<Endpoint>
    where
        H: ConnectionHandler + 'static,
    {
        let endpoint = Endpoint::new(path, handler, self.options.clone(), self.pubsub.clone());
        self.endpoints.write().await.push(endpoint.clone());
        endpoint
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
        for endpoint in self.endpoints.read().await.iter() {
            if let Some(transport) = endpoint.get_transport(user_id).await {
                return Some(transport);
            }
        }
        None
    }

    pub async fn close(&self) -> Result<()> {
        for endpoint in self.endpoints.read().await.iter() {
            endpoint.close().await?;
        }
        self.endpoints.write().await.clear();
        if let Some(pubsub) = &self.pubsub {
            let _ = pubsub.close().await;
        }
        Ok(())
    }
}
