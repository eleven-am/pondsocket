use pondsocket_common::{ValidationError, parse_client_message};
use serde_json::{Map, Value};

use crate::types::Event;

pub fn event_to_json(event: &Event) -> serde_json::Result<String> {
    serde_json::to_string(event)
}

pub fn parse_inbound_text(text: &str) -> Result<Event, ValidationError> {
    let msg = parse_client_message(text)?;
    Ok(Event {
        action: serde_json::to_string(&msg.action)
            .unwrap_or_default()
            .trim_matches('"')
            .to_owned(),
        channel_name: msg.channel_name,
        request_id: msg.request_id,
        event: msg.event,
        payload: Value::Object(msg.payload),
        node_id: String::new(),
        recipients: Vec::new(),
    })
}

pub fn event_to_pubsub_bytes(event: &Event) -> serde_json::Result<Vec<u8>> {
    serde_json::to_vec(event)
}

pub fn pubsub_bytes_to_event(data: &[u8]) -> Option<Event> {
    serde_json::from_slice(data).ok()
}

pub fn object(entries: impl IntoIterator<Item = (impl Into<String>, Value)>) -> Value {
    Value::Object(
        entries
            .into_iter()
            .map(|(k, v)| (k.into(), v))
            .collect::<Map<String, Value>>(),
    )
}
