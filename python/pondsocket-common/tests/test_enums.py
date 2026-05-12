from __future__ import annotations

from pondsocket_common import (
    ChannelReceiver,
    ChannelState,
    ClientActions,
    ErrorTypes,
    Events,
    PresenceEventTypes,
    PubSubEvents,
    ServerActions,
    SystemSender,
)


def test_server_actions_have_exact_wire_values() -> None:
    assert ServerActions.PRESENCE == "PRESENCE"
    assert ServerActions.SYSTEM == "SYSTEM"
    assert ServerActions.BROADCAST == "BROADCAST"
    assert ServerActions.ERROR == "ERROR"
    assert ServerActions.CONNECT == "CONNECT"


def test_client_actions_have_exact_wire_values() -> None:
    assert ClientActions.JOIN_CHANNEL == "JOIN_CHANNEL"
    assert ClientActions.LEAVE_CHANNEL == "LEAVE_CHANNEL"
    assert ClientActions.BROADCAST == "BROADCAST"


def test_channel_state_values() -> None:
    states = {s.value for s in ChannelState}
    assert states == {"IDLE", "JOINING", "JOINED", "STALLED", "CLOSED", "DECLINED"}


def test_presence_event_types() -> None:
    assert PresenceEventTypes.JOIN == "JOIN"
    assert PresenceEventTypes.LEAVE == "LEAVE"
    assert PresenceEventTypes.UPDATE == "UPDATE"


def test_error_types_match_js() -> None:
    members = {e.value for e in ErrorTypes}
    assert members == {
        "UNAUTHORIZED_CONNECTION",
        "UNAUTHORIZED_JOIN_REQUEST",
        "UNAUTHORIZED_BROADCAST",
        "INVALID_MESSAGE",
        "HANDLER_NOT_FOUND",
        "PRESENCE_ERROR",
        "CHANNEL_ERROR",
        "ENDPOINT_ERROR",
        "INTERNAL_SERVER_ERROR",
    }


def test_system_sender_and_channel_receiver_and_events_and_pubsub() -> None:
    assert SystemSender.ENDPOINT == "ENDPOINT"
    assert SystemSender.CHANNEL == "CHANNEL"
    assert ChannelReceiver.ALL_USERS == "ALL_USERS"
    assert ChannelReceiver.ALL_EXCEPT_SENDER == "ALL_EXCEPT_SENDER"
    assert Events.ACKNOWLEDGE == "ACKNOWLEDGE"
    assert Events.CONNECTION == "CONNECTION"
    assert Events.UNAUTHORIZED == "UNAUTHORIZED"
    assert PubSubEvents.MESSAGE == "MESSAGE"
    assert PubSubEvents.PRESENCE == "PRESENCE"
    assert PubSubEvents.GET_PRESENCE == "GET_PRESENCE"
