from __future__ import annotations

from pondsocket_common import (
    ChannelReceiver,
    IncomingConnection,
    PresencePayload,
    PubSubEvents,
    PubSubGetPresenceCommand,
    PubSubMessageEvent,
    PubSubPresenceEvent,
    UserData,
)


def test_user_data_defaults_are_empty_dicts() -> None:
    u = UserData(id="abc")
    assert u.id == "abc"
    assert u.assigns == {}
    assert u.presence == {}


def test_user_data_with_explicit_state() -> None:
    u = UserData(id="abc", assigns={"role": "admin"}, presence={"status": "online"})
    assert u.assigns == {"role": "admin"}
    assert u.presence == {"status": "online"}


def test_incoming_connection_defaults() -> None:
    c = IncomingConnection(id="x", address="1.2.3.4")
    assert len(c.headers) == 0
    assert c.cookies == {}
    assert c.query == {}
    assert c.params == {}


def test_presence_payload_shape() -> None:
    payload = PresencePayload(changed={"id": "1"}, presence=[{"id": "1"}, {"id": "2"}])
    assert payload.changed == {"id": "1"}
    assert payload.presence == [{"id": "1"}, {"id": "2"}]


def test_pubsub_event_variants_carry_default_tags() -> None:
    get = PubSubGetPresenceCommand(endpoint="/e", channel="c", pub_sub_id="p")
    assert get.event is PubSubEvents.GET_PRESENCE

    pres = PubSubPresenceEvent(
        endpoint="/e", channel="c", pub_sub_id="p", presence={}
    )
    assert pres.event is PubSubEvents.PRESENCE

    msg = PubSubMessageEvent(
        endpoint="/e",
        channel="c",
        pub_sub_id="p",
        message={},
        recipient=ChannelReceiver.ALL_USERS,
    )
    assert msg.event is PubSubEvents.MESSAGE
    assert msg.recipient is ChannelReceiver.ALL_USERS
