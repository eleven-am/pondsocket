# pondsocket-client

Python client for the [PondSocket](../pondsocket/) server. Connects to PondSocket servers (Python, Go, or TypeScript implementation) over WebSocket.

Modeled after the JavaScript client architecture (`@eleven-am/pondsocket-client`) rather than the Go one — the JS client fixes several bugs the Go client has (see [Parity notes](#parity-notes) below).

## Install

```
pip install pondsocket-client
```

## Quick start

```python
import asyncio
from pondsocket_client import PondClient


async def main() -> None:
    client = PondClient("ws://localhost:8000/ws", {"token": "secret"})
    await client.connect()

    channel = client.create_channel("/chat/42", {"username": "alice"})
    channel.on_message_event("message", lambda m: print(m.payload))
    channel.on_join(lambda user: print("joined:", user))
    channel.join()

    channel.send_message("message", {"text": "hello"})

    response = await channel.send_for_response("ping", {"text": "abc"})
    print("got pong:", response)

    await asyncio.sleep(10)
    await client.disconnect()


asyncio.run(main())
```

## Architecture

```
BaseClient (abstract)
  ├── channel registry
  ├── inbound event routing (ACK / UNAUTHORIZED / CONNECTION / per-channel)
  ├── connection state machine: DISCONNECTED → CONNECTING → CONNECTED
  └── exponential reconnect

WebSocketClient(BaseClient)  (abstract)
  ├── opens via `websockets` library
  └── receive loop pumps inbound to BaseClient

PondClient(WebSocketClient)  (concrete)
  ├── URL handling (http → ws, https → wss)
  └── appends query-string params
```

The `Channel` class holds the per-channel state machine (IDLE → JOINING → JOINED → STALLED → CLOSED/DECLINED), a bounded outbound queue (drops oldest on overflow), and a `send_for_response` that correlates inbound replies by `requestId`.

## Connection states

Subscribe via `client.on_connection_change(handler)`:

| State | When |
|---|---|
| `DISCONNECTED` | initial; after disconnect or socket close |
| `CONNECTING` | `connect()` called; socket handshake in flight |
| `CONNECTED` | socket open AND server sent the CONNECTION event |

## Channel states

Subscribe via `channel.on_channel_state_change(handler)`:

| State | Meaning |
|---|---|
| `IDLE` | created but `join()` not called |
| `JOINING` | `join()` called; awaiting ACKNOWLEDGE |
| `JOINED` | server acknowledged; messages flow |
| `STALLED` | socket dropped while JOINED; will rejoin on reconnect |
| `CLOSED` | `leave()` called |
| `DECLINED` | server sent UNAUTHORIZED; channel is terminal — create a new one |

## Parity notes

Compared to the Go client (`go/pondsocket-client/`):

| Behavior | Go | Python |
|---|---|---|
| `send_for_response` correlation | event name (buggy) | `requestId` (correct) |
| Reconnect backoff | linear despite comment | true exponential, capped |
| `UNAUTHORIZED` handling | not implemented | → `DECLINED` state |
| Connection state | bool (no CONNECTING) | three-state enum |
| Outbound queue | unbounded | bounded, drops oldest |
| Join while disconnected | dropped silently | deferred until CONNECTED |

The Python client follows the JS client design where the two diverge.

## Public API

```python
from pondsocket_client import (
    PondClient,
    Channel,
    ConnectionState,
    ClientOptions,
    ResponseTimeoutError,
)
```

Also exposed for adapter authors: `BaseClient`, `WebSocketClient`, `Publisher` (type alias).

## License

GPL-3.0-or-later.
