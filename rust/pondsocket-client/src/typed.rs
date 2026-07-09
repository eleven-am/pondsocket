use std::marker::PhantomData;
use std::time::Duration;

use pondsocket_common::{
    ChannelEvent, PondEvent, PondSchema, PresenceMessage, ServerMessage, from_pond_map,
    from_pond_value, to_pond_map,
};
use tokio::sync::broadcast;

use crate::{Channel, ClientError, PondClient};

#[derive(Clone)]
pub struct TypedChannel<S> {
    raw: Channel,
    _schema: PhantomData<S>,
}

impl<S> TypedChannel<S>
where
    S: PondSchema,
{
    pub fn new(raw: Channel) -> Self {
        Self {
            raw,
            _schema: PhantomData,
        }
    }

    pub fn raw(&self) -> &Channel {
        &self.raw
    }

    pub fn name(&self) -> &str {
        self.raw.name()
    }

    pub fn state(&self) -> pondsocket_common::ChannelState {
        self.raw.state()
    }

    pub fn subscribe_events(&self) -> broadcast::Receiver<ChannelEvent> {
        self.raw.subscribe_events()
    }

    pub fn subscribe_state(&self) -> tokio::sync::watch::Receiver<pondsocket_common::ChannelState> {
        self.raw.subscribe_state()
    }

    pub async fn presence(&self) -> Result<Vec<S::Presence>, ClientError> {
        self.raw
            .presence()
            .await
            .into_iter()
            .map(from_pond_map)
            .collect::<serde_json::Result<Vec<_>>>()
            .map_err(ClientError::Serialization)
    }

    pub async fn join(&self) {
        self.raw.join().await;
    }

    pub async fn leave(&self) {
        self.raw.leave().await;
    }

    pub async fn send<E>(&self, payload: &E::Payload) -> Result<(), ClientError>
    where
        E: PondEvent,
    {
        self.raw
            .send_message(E::NAME, Some(to_pond_map(payload)?))
            .await;
        Ok(())
    }

    pub async fn request<E>(
        &self,
        payload: &E::Payload,
        timeout: Option<Duration>,
    ) -> Result<E::Response, ClientError>
    where
        E: PondEvent,
    {
        let response = self
            .raw
            .send_for_response(E::NAME, Some(to_pond_map(payload)?), timeout)
            .await?;
        from_pond_map(response).map_err(ClientError::Serialization)
    }

    pub fn decode_message<E>(
        &self,
        message: &ServerMessage,
    ) -> Result<Option<E::Payload>, ClientError>
    where
        E: PondEvent,
    {
        if message.event != E::NAME {
            return Ok(None);
        }
        from_pond_map(message.payload.clone())
            .map(Some)
            .map_err(ClientError::Serialization)
    }

    pub fn decode_presence(
        &self,
        message: &PresenceMessage,
    ) -> Result<(S::Presence, Vec<S::Presence>), ClientError> {
        let changed =
            from_pond_map(message.payload.changed.clone()).map_err(ClientError::Serialization)?;
        let presence = message
            .payload
            .presence
            .iter()
            .cloned()
            .map(from_pond_map)
            .collect::<serde_json::Result<Vec<_>>>()
            .map_err(ClientError::Serialization)?;
        Ok((changed, presence))
    }

    pub fn decode_event<E>(&self, event: ChannelEvent) -> Result<Option<E::Payload>, ClientError>
    where
        E: PondEvent,
    {
        match event {
            ChannelEvent::Message(message) => self.decode_message::<E>(&message),
            ChannelEvent::Presence(_) => Ok(None),
        }
    }
}

impl PondClient {
    pub async fn create_typed_channel<S>(
        &self,
        name: impl Into<String>,
        params: Option<&S::JoinParams>,
    ) -> Result<TypedChannel<S>, ClientError>
    where
        S: PondSchema,
    {
        let params = params.map(to_pond_map).transpose()?;
        let channel = self.create_channel(name, params).await;
        Ok(TypedChannel::new(channel))
    }
}

pub fn decode_payload<E>(message: ServerMessage) -> Result<E::Payload, ClientError>
where
    E: PondEvent,
{
    from_pond_map(message.payload).map_err(ClientError::Serialization)
}

pub fn decode_presence_value<S>(value: serde_json::Value) -> Result<S::Presence, ClientError>
where
    S: PondSchema,
{
    from_pond_value(value).map_err(ClientError::Serialization)
}

#[cfg(test)]
mod tests {
    use super::*;
    use pondsocket_common::{ChannelState, ServerAction};
    use serde::{Deserialize, Serialize};
    use serde_json::json;

    use crate::PondClient;

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct ChatPayload {
        text: String,
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct AckPayload {
        ok: bool,
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct Member {
        user_id: String,
    }

    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct Role {
        role: String,
    }

    struct Chat;
    struct RoomSchema;

    impl PondEvent for Chat {
        type Payload = ChatPayload;
        type Response = AckPayload;

        const NAME: &'static str = "chat";
    }

    impl PondSchema for RoomSchema {
        type Presence = Member;
        type Assigns = Role;
        type JoinParams = Role;
    }

    fn server_message(event: &str, payload: serde_json::Value) -> ServerMessage {
        ServerMessage {
            action: ServerAction::Broadcast,
            event: event.to_owned(),
            channel_name: "room".to_owned(),
            request_id: "r1".to_owned(),
            payload: serde_json::from_value(payload).unwrap(),
        }
    }

    #[test]
    fn decode_payload_reads_typed_event() {
        let message = server_message("chat", json!({ "text": "hi" }));
        let payload = decode_payload::<Chat>(message).unwrap();
        assert_eq!(
            payload,
            ChatPayload {
                text: "hi".to_owned()
            }
        );
    }

    #[test]
    fn decode_presence_value_reads_single_member() {
        let member = decode_presence_value::<RoomSchema>(json!({ "user_id": "u1" })).unwrap();
        assert_eq!(
            member,
            Member {
                user_id: "u1".to_owned()
            }
        );
    }

    #[tokio::test]
    async fn decode_event_decodes_matching_message_and_ignores_others() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client
            .create_typed_channel::<RoomSchema>("room", None::<&Role>)
            .await
            .unwrap();

        let matching = ChannelEvent::Message(server_message("chat", json!({ "text": "hey" })));
        assert_eq!(
            channel.decode_event::<Chat>(matching).unwrap(),
            Some(ChatPayload {
                text: "hey".to_owned()
            })
        );

        let other = ChannelEvent::Message(server_message("other", json!({ "text": "hey" })));
        assert_eq!(channel.decode_event::<Chat>(other).unwrap(), None);
    }

    #[tokio::test]
    async fn subscribe_state_reports_current_channel_state() {
        let client = PondClient::new("ws://example.com/socket", None).unwrap();
        let channel = client
            .create_typed_channel::<RoomSchema>("room", None::<&Role>)
            .await
            .unwrap();

        let state = channel.subscribe_state();
        assert_eq!(*state.borrow(), ChannelState::Idle);
    }
}
