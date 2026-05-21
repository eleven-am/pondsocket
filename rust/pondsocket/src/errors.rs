use serde::{Deserialize, Serialize};
use serde_json::Value;
use thiserror::Error;

pub type Result<T> = std::result::Result<T, PondError>;

#[derive(Debug, Clone, Error, Serialize, Deserialize)]
#[error("{}: {message}", channel_name.as_deref().unwrap_or("pondsocket"))]
pub struct PondError {
    #[serde(rename = "channelName", skip_serializing_if = "Option::is_none")]
    pub channel_name: Option<String>,
    pub message: String,
    pub code: u16,
    pub temporary: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub details: Option<Value>,
}

impl PondError {
    pub fn new(channel: impl Into<String>, code: u16, message: impl Into<String>) -> Self {
        Self {
            channel_name: Some(channel.into()),
            message: message.into(),
            code,
            temporary: false,
            details: None,
        }
    }

    pub fn temporary(mut self, temporary: bool) -> Self {
        self.temporary = temporary;
        self
    }

    pub fn details(mut self, details: Value) -> Self {
        self.details = Some(details);
        self
    }
}

pub fn bad_request(channel: impl Into<String>, message: impl Into<String>) -> PondError {
    PondError::new(channel, 400, message)
}

pub fn unauthorized(channel: impl Into<String>, message: impl Into<String>) -> PondError {
    PondError::new(channel, 401, message)
}

pub fn forbidden(channel: impl Into<String>, message: impl Into<String>) -> PondError {
    PondError::new(channel, 403, message)
}

pub fn not_found(channel: impl Into<String>, message: impl Into<String>) -> PondError {
    PondError::new(channel, 404, message)
}

pub fn conflict(channel: impl Into<String>, message: impl Into<String>) -> PondError {
    PondError::new(channel, 409, message)
}

pub fn internal(channel: impl Into<String>, message: impl Into<String>) -> PondError {
    PondError::new(channel, 500, message)
}

pub fn unavailable(channel: impl Into<String>, message: impl Into<String>) -> PondError {
    PondError::new(channel, 503, message).temporary(true)
}

pub fn timeout(channel: impl Into<String>, message: impl Into<String>) -> PondError {
    PondError::new(channel, 504, message).temporary(true)
}
