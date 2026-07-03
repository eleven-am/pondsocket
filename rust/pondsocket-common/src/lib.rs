use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use thiserror::Error;

pub type PondMessage = Map<String, Value>;
pub type PondPresence = Map<String, Value>;
pub type PondAssigns = Map<String, Value>;
pub type JoinParams = Map<String, Value>;

pub trait PondEvent {
    type Payload: Serialize + DeserializeOwned + Send + Sync + 'static;
    type Response: Serialize + DeserializeOwned + Send + Sync + 'static;

    const NAME: &'static str;
}

pub trait PondSchema {
    type Presence: Serialize + DeserializeOwned + Send + Sync + 'static;
    type Assigns: Serialize + DeserializeOwned + Send + Sync + 'static;
    type JoinParams: Serialize + DeserializeOwned + Send + Sync + 'static;
}

pub fn to_pond_value<T>(value: &T) -> serde_json::Result<Value>
where
    T: Serialize + ?Sized,
{
    serde_json::to_value(value)
}

pub fn to_pond_map<T>(value: &T) -> serde_json::Result<Map<String, Value>>
where
    T: Serialize + ?Sized,
{
    match serde_json::to_value(value)? {
        Value::Object(map) => Ok(map),
        Value::Null => Ok(Map::new()),
        value => {
            let mut map = Map::new();
            map.insert("value".to_owned(), value);
            Ok(map)
        }
    }
}

pub fn from_pond_value<T>(value: Value) -> serde_json::Result<T>
where
    T: DeserializeOwned,
{
    serde_json::from_value(value)
}

pub fn from_pond_map<T>(value: Map<String, Value>) -> serde_json::Result<T>
where
    T: DeserializeOwned,
{
    serde_json::from_value(Value::Object(value))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum PresenceEventType {
    Join,
    Leave,
    Update,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ClientAction {
    JoinChannel,
    LeaveChannel,
    Broadcast,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ServerAction {
    Presence,
    System,
    Broadcast,
    Error,
    Connect,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ChannelState {
    Idle,
    Joining,
    Joined,
    Stalled,
    Closed,
    Declined,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ErrorType {
    UnauthorizedConnection,
    UnauthorizedJoinRequest,
    UnauthorizedBroadcast,
    InvalidMessage,
    HandlerNotFound,
    PresenceError,
    ChannelError,
    EndpointError,
    InternalServerError,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum EventName {
    Acknowledge,
    ExitAcknowledge,
    Connection,
    InternalError,
    NotFound,
    Unauthorized,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ClientMessage {
    pub event: String,
    #[serde(rename = "requestId")]
    pub request_id: String,
    #[serde(rename = "channelName")]
    pub channel_name: String,
    #[serde(default)]
    pub payload: PondMessage,
    pub action: ClientAction,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ServerMessage {
    pub event: String,
    #[serde(rename = "requestId")]
    pub request_id: String,
    #[serde(rename = "channelName")]
    pub channel_name: String,
    #[serde(default)]
    pub payload: PondMessage,
    pub action: ServerAction,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct PresencePayload {
    #[serde(default)]
    pub presence: Vec<PondPresence>,
    #[serde(default)]
    pub changed: PondPresence,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct PresenceMessage {
    #[serde(rename = "requestId")]
    pub request_id: String,
    #[serde(rename = "channelName")]
    pub channel_name: String,
    pub event: PresenceEventType,
    pub action: PresenceAction,
    pub payload: PresencePayload,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum PresenceAction {
    Presence,
}

#[derive(Debug, Clone, PartialEq)]
pub enum ChannelEvent {
    Message(ServerMessage),
    Presence(PresenceMessage),
}

#[derive(Debug, Clone, Error, PartialEq, Eq)]
#[error("{path}: {message}")]
pub struct ValidationError {
    pub path: String,
    pub message: String,
}

impl ValidationError {
    pub fn new(path: impl Into<String>, message: impl Into<String>) -> Self {
        Self {
            path: path.into(),
            message: message.into(),
        }
    }
}

pub fn uuid() -> String {
    uuid::Uuid::new_v4().to_string()
}

pub fn parse_client_message(data: &str) -> Result<ClientMessage, ValidationError> {
    let value = parse_object(data, "clientMessage")?;
    serde_json::from_value(value).map_err(|e| ValidationError::new("clientMessage", e.to_string()))
}

pub fn parse_server_message(data: &str) -> Result<ServerMessage, ValidationError> {
    let value = parse_object(data, "serverMessage")?;
    serde_json::from_value(value).map_err(|e| ValidationError::new("serverMessage", e.to_string()))
}

pub fn parse_channel_event(data: &str) -> Result<ChannelEvent, ValidationError> {
    let value = parse_object(data, "channelEvent")?;
    let action = value
        .get("action")
        .and_then(Value::as_str)
        .ok_or_else(|| ValidationError::new("action", "Missing required field"))?;
    if action == "PRESENCE" {
        let msg: PresenceMessage = serde_json::from_value(value)
            .map_err(|e| ValidationError::new("presenceMessage", e.to_string()))?;
        Ok(ChannelEvent::Presence(msg))
    } else {
        let msg: ServerMessage = serde_json::from_value(value)
            .map_err(|e| ValidationError::new("serverMessage", e.to_string()))?;
        Ok(ChannelEvent::Message(msg))
    }
}

fn parse_object(data: &str, root: &str) -> Result<Value, ValidationError> {
    let value: Value =
        serde_json::from_str(data).map_err(|e| ValidationError::new(root, e.to_string()))?;
    if value.is_object() {
        Ok(value)
    } else {
        Err(ValidationError::new(root, "Expected object"))
    }
}

pub fn message_to_json(message: &ServerMessage) -> serde_json::Result<String> {
    serde_json::to_string(message)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_client_message_with_camel_case_fields() {
        let msg = parse_client_message(
            r#"{"action":"BROADCAST","event":"message","channelName":"/chat/1","requestId":"r1","payload":{"text":"hi"}}"#,
        )
        .unwrap();
        assert_eq!(msg.action, ClientAction::Broadcast);
        assert_eq!(msg.channel_name, "/chat/1");
    }

    #[test]
    fn routes_presence_channel_event() {
        let ev = parse_channel_event(
            r#"{"action":"PRESENCE","event":"JOIN","channelName":"/chat/1","requestId":"r1","payload":{"presence":[{"id":"u1"}],"changed":{"id":"u1"}}}"#,
        )
        .unwrap();
        assert!(matches!(ev, ChannelEvent::Presence(_)));
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct Sample {
        id: String,
        count: u32,
    }

    #[test]
    fn pond_value_round_trips_through_from_pond_value() {
        let sample = Sample {
            id: "a".to_owned(),
            count: 3,
        };
        let value = to_pond_value(&sample).unwrap();
        assert_eq!(from_pond_value::<Sample>(value).unwrap(), sample);
    }

    #[test]
    fn pond_map_round_trips_through_from_pond_map() {
        let sample = Sample {
            id: "b".to_owned(),
            count: 7,
        };
        let map = to_pond_map(&sample).unwrap();
        assert_eq!(map["id"], Value::from("b"));
        assert_eq!(from_pond_map::<Sample>(map).unwrap(), sample);
    }

    #[test]
    fn to_pond_map_wraps_scalar_under_value_key() {
        let map = to_pond_map(&42u32).unwrap();
        assert_eq!(map["value"], Value::from(42));
    }

    #[test]
    fn to_pond_map_maps_null_to_empty_map() {
        let map = to_pond_map(&Option::<Sample>::None).unwrap();
        assert!(map.is_empty());
    }
}
