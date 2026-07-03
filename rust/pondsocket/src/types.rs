use pondsocket_common::{PondAssigns, PondPresence};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use crate::hooks::Hooks;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TransportType {
    WebSocket,
    Sse,
    InMemory,
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Route {
    pub params: HashMap<String, String>,
    pub query: HashMap<String, Vec<String>>,
    pub wildcard: Option<String>,
}

impl Route {
    pub fn param(&self, key: &str) -> Option<&str> {
        self.params.get(key).map(String::as_str)
    }

    pub fn query_param(&self, key: &str) -> &[String] {
        self.query.get(key).map(Vec::as_slice).unwrap_or(&[])
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Event {
    pub action: String,
    #[serde(rename = "channelName")]
    pub channel_name: String,
    #[serde(rename = "requestId")]
    pub request_id: String,
    #[serde(default)]
    pub payload: Value,
    pub event: String,
    #[serde(rename = "nodeId", skip_serializing_if = "String::is_empty", default)]
    pub node_id: String,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub recipients: Vec<String>,
    #[serde(
        rename = "fromUserId",
        skip_serializing_if = "String::is_empty",
        default
    )]
    pub from_user_id: String,
    #[serde(
        rename = "recipientDescriptor",
        skip_serializing_if = "String::is_empty",
        default
    )]
    pub recipient_descriptor: String,
}

impl Event {
    pub fn new(
        action: impl Into<String>,
        channel_name: impl Into<String>,
        request_id: impl Into<String>,
        event: impl Into<String>,
        payload: Value,
    ) -> Self {
        Self {
            action: action.into(),
            channel_name: channel_name.into(),
            request_id: request_id.into(),
            event: event.into(),
            payload,
            node_id: String::new(),
            recipients: Vec::new(),
            from_user_id: String::new(),
            recipient_descriptor: String::new(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct User {
    pub id: String,
    pub assigns: PondAssigns,
    pub presence: Option<PondPresence>,
}

#[derive(Clone)]
pub struct Options {
    pub max_message_size: usize,
    pub internal_queue_timeout: Duration,
    pub internal_queue_buffer: usize,
    pub dispatch_concurrency: usize,
    pub max_connections: usize,
    pub node_id: String,
    pub namespace: String,
    pub heartbeat_interval: Duration,
    pub heartbeat_timeout: Duration,
    pub hooks: Option<Arc<Hooks>>,
}

impl Default for Options {
    fn default() -> Self {
        Self {
            max_message_size: 1024 * 1024,
            internal_queue_timeout: Duration::from_secs(30),
            internal_queue_buffer: 128,
            dispatch_concurrency: 32,
            max_connections: 0,
            node_id: String::new(),
            namespace: "default".to_owned(),
            heartbeat_interval: Duration::from_secs(30),
            heartbeat_timeout: Duration::from_secs(90),
            hooks: None,
        }
    }
}
