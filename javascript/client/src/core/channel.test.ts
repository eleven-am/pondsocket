import {
    BehaviorSubject,
    ChannelEvent,
    ChannelState,
    ClientActions,
    ErrorTypes,
    JoinParams,
    PresenceEventTypes,
    PondSchema,
    ServerActions,
    Subject,
} from '@eleven-am/pondsocket-common';

import { Channel } from './channel';
import { ClientMessage, ConnectionState } from '../types';

const createChannel = (params: JoinParams = {}) => {
    const publisher = jest.fn();
    const state = new BehaviorSubject<ConnectionState>(ConnectionState.CONNECTED);
    const receiver = new Subject<ChannelEvent>();

    const channel = new Channel(
        publisher,
        state,
        'test',
        params,
    );

    return {
        channel,
        publisher,
        state,
        receiver,
    };
};

describe('Channel', () => {
    it('should correctly perform the join process', () => {
        const { channel, publisher, state, receiver } = createChannel();

        // the channel should be in the idle state when it is created
        expect(channel.channelState).toBe(ChannelState.IDLE);

        // if the socket is not connected the channel should not post a join message
        state.publish(ConnectionState.DISCONNECTED);
        channel.join();

        expect(publisher).not.toHaveBeenCalled();
        expect(channel.channelState).toBe(ChannelState.JOINING);

        // once the socket is connected, the channel should attempt to join
        state.publish(ConnectionState.CONNECTED);
        expect(channel.channelState).toBe(ChannelState.JOINING);
        expect(publisher).toHaveBeenCalledWith(
            expect.objectContaining({
                action: ClientActions.JOIN_CHANNEL,
                channelName: 'test',
                event: ClientActions.JOIN_CHANNEL,
                payload: {},
            }),
        );

        // once the server responds with an ack, the channel state should be joined
        channel.acknowledge(receiver);
        expect(channel.channelState).toBe(ChannelState.JOINED);

        // if the socket is disconnected, the channel should be stalled
        state.publish(ConnectionState.DISCONNECTED);
        expect(channel.channelState).toBe(ChannelState.STALLED);

        // once the socket is reconnected, a join message should be sent
        publisher.mockClear();
        state.publish(ConnectionState.CONNECTED);
        expect(channel.channelState).toBe(ChannelState.STALLED);
        expect(publisher).toHaveBeenCalledWith(
            expect.objectContaining({
                action: ClientActions.JOIN_CHANNEL,
                channelName: 'test',
                event: ClientActions.JOIN_CHANNEL,
                payload: {},
            }),
        );

        // once the server responds with an ack, the channel state should be joined
        channel.acknowledge(receiver);
        expect(channel.channelState).toBe(ChannelState.JOINED);

        // if the channel is closed, the channel should be closed
        channel.leave();
        expect(channel.channelState).toBe(ChannelState.CLOSED);
        publisher.mockClear();

        // if the socket closes, while the channel is closed, the channel should not attempt to join when the socket reconnects
        state.publish(ConnectionState.DISCONNECTED);
        state.publish(ConnectionState.CONNECTED);
        expect(publisher).not.toHaveBeenCalled();

        // if the channel is closed, during a join process, the channel should not attempt to join when the socket reconnects
        const { channel: channel2, publisher: publisher2, state: state2 } = createChannel();

        state2.publish(ConnectionState.DISCONNECTED);
        channel2.join();

        expect(publisher2).not.toHaveBeenCalled();
        expect(channel2.channelState).toBe(ChannelState.JOINING);

        channel2.leave();
        state2.publish(ConnectionState.CONNECTED);
        expect(publisher2).not.toHaveBeenCalled();

        // a late acknowledgement must not reopen an explicitly closed channel
        channel2.acknowledge(receiver);
        expect(channel2.channelState).toBe(ChannelState.CLOSED);
    });

    it('should notify subscribers when the channel state changes', () => {
        const { channel, receiver, state } = createChannel();

        const stateListener = jest.fn();

        channel.onChannelStateChange(stateListener);

        expect(stateListener).toHaveBeenCalledWith(ChannelState.IDLE);

        channel.join();

        expect(stateListener).toHaveBeenCalledWith(ChannelState.JOINING);

        channel.acknowledge(receiver);
        expect(stateListener).toHaveBeenCalledWith(ChannelState.JOINED);

        state.publish(ConnectionState.DISCONNECTED);
        expect(stateListener).toHaveBeenCalledWith(ChannelState.STALLED);

        channel.leave();

        expect(stateListener).toHaveBeenCalledWith(ChannelState.CLOSED);
    });

    it('retries a join when the transport disconnects before acknowledgement', () => {
        const { channel, publisher, receiver, state } = createChannel();

        channel.join();
        expect(publisher).toHaveBeenCalledTimes(1);
        expect(channel.channelState).toBe(ChannelState.JOINING);

        state.publish(ConnectionState.DISCONNECTED);
        expect(channel.channelState).toBe(ChannelState.STALLED);
        state.publish(ConnectionState.CONNECTED);
        expect(publisher).toHaveBeenCalledTimes(2);

        channel.acknowledge(receiver);
        expect(channel.channelState).toBe(ChannelState.JOINED);
    });

    it('should correctly send a message', () => {
        const { channel, publisher, receiver, state } = createChannel();

        // the channel should not send a message if it is not joined, the message should be queued
        channel.sendMessage('test', {
            test: 'test',
        });

        expect(publisher).not.toHaveBeenCalled();

        // however, if the channel is joined, the queued message should be sent
        channel.join();
        publisher.mockClear();

        // acknowledge the join
        channel.acknowledge(receiver);

        expect(publisher).toHaveBeenCalledWith(
            expect.objectContaining({
                action: ClientActions.BROADCAST,
                channelName: 'test',
                event: 'test',
                payload: {
                    test: 'test',
                },
            }),
        );

        publisher.mockClear();

        // if the channel stalls and rejoins the previous message should not be sent again
        state.publish(ConnectionState.DISCONNECTED);
        state.publish(ConnectionState.CONNECTED);

        // acknowledge the join
        channel.acknowledge(receiver);

        expect(publisher).toHaveBeenCalledTimes(1); // The join message
        expect(publisher).not.toHaveBeenCalledWith(
            expect.objectContaining({
                action: ClientActions.BROADCAST,
                channelName: 'test',
                event: 'test',
                payload: {
                    test: 'test',
                },
            }),
        );

        publisher.mockClear();
        // if a message is sent while the channel is stalled, it should be queued
        state.publish(ConnectionState.DISCONNECTED);

        channel.sendMessage('test', {
            test: 'test stalling',
        });

        expect(publisher).not.toHaveBeenCalled();
        state.publish(ConnectionState.CONNECTED);
        publisher.mockClear(); // The join message

        // acknowledge the join
        channel.acknowledge(receiver);
        expect(publisher).toHaveBeenCalledWith(
            expect.objectContaining({
                action: ClientActions.BROADCAST,
                channelName: 'test',
                event: 'test',
                payload: {
                    test: 'test stalling',
                },
            }),
        );

        expect(publisher).toHaveBeenCalledTimes(1);
    });

    it('flushes queued messages in FIFO order', () => {
        const { channel, publisher, receiver } = createChannel();

        channel.sendMessage('first', { order: 1 });
        channel.sendMessage('second', { order: 2 });
        channel.sendMessage('third', { order: 3 });
        channel.join();
        publisher.mockClear();
        channel.acknowledge(receiver);

        expect(publisher.mock.calls.map(([message]) => message.event)).toEqual(['first', 'second', 'third']);
    });

    it('reports queue backpressure without evicting older fire-and-forget messages', () => {
        const publisher = jest.fn();
        const state = new BehaviorSubject<ConnectionState>(ConnectionState.CONNECTED);
        const receiver = new Subject<ChannelEvent>();
        const channel = new Channel(publisher, state, 'test', {}, 1);
        const errorListener = jest.fn();

        channel.onError(errorListener);
        channel.sendMessage('first', { order: 1 });
        expect(() => channel.sendMessage('second', { order: 2 })).toThrow(/message queue capacity was exceeded/);

        expect(errorListener).toHaveBeenCalledWith(expect.objectContaining({
            status: 429,
        }));
        channel.join();
        publisher.mockClear();
        channel.acknowledge(receiver);
        expect(publisher.mock.calls.map(([message]) => message.event)).toEqual(['first']);
    });

    it('retains a failed ACK flush and retries the whole FIFO after rejoin', () => {
        const { channel, publisher, receiver, state } = createChannel();
        let failFirstBroadcast = true;

        publisher.mockImplementation((message: ClientMessage) => {
            if (message.action === ClientActions.BROADCAST && failFirstBroadcast) {
                failFirstBroadcast = false;
                throw new Error('transport write failed');
            }
        });
        channel.sendMessage('first', { order: 1 });
        channel.sendMessage('second', { order: 2 });
        channel.join();
        publisher.mockClear();

        channel.acknowledge(receiver);
        expect(channel.channelState).toBe(ChannelState.STALLED);
        expect(publisher.mock.calls.map(([message]) => message.event)).toEqual(['first']);

        state.publish(ConnectionState.DISCONNECTED);
        state.publish(ConnectionState.CONNECTED);
        channel.acknowledge(receiver);

        expect(channel.channelState).toBe(ChannelState.JOINED);
        expect(publisher.mock.calls
            .filter(([message]) => message.action === ClientActions.BROADCAST)
            .map(([message]) => message.event)).toEqual(['first', 'first', 'second']);
    });

    it('returns a live send failure while retaining the message for reconnect', () => {
        const { channel, publisher, receiver, state } = createChannel();

        channel.join();
        channel.acknowledge(receiver);
        publisher.mockImplementationOnce(() => {
            throw new Error('socket closed');
        });

        expect(() => channel.sendMessage('retained', { order: 1 })).toThrow('socket closed');
        expect(channel.channelState).toBe(ChannelState.STALLED);

        publisher.mockClear();
        state.publish(ConnectionState.DISCONNECTED);
        state.publish(ConnectionState.CONNECTED);
        channel.acknowledge(receiver);

        expect(publisher.mock.calls
            .filter(([message]) => message.action === ClientActions.BROADCAST)
            .map(([message]) => message.event)).toEqual(['retained']);
    });

    it('cleans a failed join attempt and retries only after a disconnect transition', () => {
        const { channel, publisher, receiver, state } = createChannel();
        const errors = jest.fn();

        channel.onError(errors);
        publisher.mockImplementationOnce(() => {
            throw new Error('join write failed');
        });
        channel.join();

        expect(channel.channelState).toBe(ChannelState.STALLED);
        expect(errors).toHaveBeenCalledWith(expect.objectContaining({ message: 'join write failed' }));
        expect(publisher).toHaveBeenCalledTimes(1);

        state.publish(ConnectionState.CONNECTED);
        expect(publisher).toHaveBeenCalledTimes(1);
        state.publish(ConnectionState.DISCONNECTED);
        state.publish(ConnectionState.CONNECTED);
        expect(publisher).toHaveBeenCalledTimes(2);

        channel.acknowledge(receiver);
        expect(channel.channelState).toBe(ChannelState.JOINED);
    });

    // The presence system tests
    it('should notify subscribers when a user joins the channel', () => {
        const { channel, receiver } = createChannel();

        const presenceListener = jest.fn();

        channel.onJoin(presenceListener);

        expect(presenceListener).not.toHaveBeenCalled();

        // acknowledge the join
        channel.join();
        channel.acknowledge(receiver);

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.JOIN,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).toHaveBeenCalledWith({
            id: 'test',
            status: 'online',
        });

        presenceListener.mockClear();

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();

        // also, if a presence event is received for a different channel, it should not be sent to the listener
        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.JOIN,
            channelName: 'test2',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();
    });

    it('should notify subscribers when a user leaves the channel', () => {
        const { channel, receiver } = createChannel();

        const presenceListener = jest.fn();

        channel.onLeave(presenceListener);

        expect(presenceListener).not.toHaveBeenCalled();

        // acknowledge the join
        channel.join();
        channel.acknowledge(receiver);

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.LEAVE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).toHaveBeenCalledWith({
            id: 'test',
            status: 'online',
        });

        presenceListener.mockClear();

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();

        // also, if a presence event is received for a different channel, it should not be sent to the listener
        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.LEAVE,
            channelName: 'test2',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();
    });

    it('should notify subscribers when a user updates their presence', () => {
        const { channel, receiver } = createChannel();

        const presenceListener = jest.fn();

        channel.onUsersChange(presenceListener);

        expect(presenceListener).not.toHaveBeenCalled();

        // acknowledge the join
        channel.join();
        channel.acknowledge(receiver);

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).toHaveBeenCalledWith([
            {
                id: 'test',
                status: 'online',
            },
            {
                id: 'test2',
                status: 'online',
            },
        ]);

        expect(channel.presence).toEqual(presenceListener.mock.calls[0][0]);
        expect(channel.presence).not.toBe(presenceListener.mock.calls[0][0]);

        presenceListener.mockClear();

        // if we receive a leave or join event, it should be sent to the listener
        // this is because we are listening for all presence events
        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.JOIN,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).toHaveBeenCalledWith([
            {
                id: 'test',
                status: 'online',
            },
        ]);

        expect(channel.presence).toEqual(presenceListener.mock.calls[0][0]);
        expect(channel.presence).not.toBe(presenceListener.mock.calls[0][0]);

        presenceListener.mockClear();

        // also, if a presence event is received for a different channel, it should not be sent to the listener
        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test2',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();

        // we once again send a presence event for the same channel, but this time it should be sent to the listener
        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'away',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'away',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).toHaveBeenCalledWith([
            {
                id: 'test',
                status: 'away',
            },
            {
                id: 'test2',
                status: 'online',
            },
        ]);

        expect(channel.presence).toEqual(presenceListener.mock.calls[0][0]);
        expect(channel.presence).not.toBe(presenceListener.mock.calls[0][0]);
    });

    it('does not expose mutable presence state', () => {
        const { channel, receiver } = createChannel();
        const presenceListener = jest.fn((presence: Array<Record<string, any>>) => {
            presence[0].profile.name = 'changed by listener';
        });

        channel.onUsersChange(presenceListener);
        channel.join();
        channel.acknowledge(receiver);
        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                },
                presence: [{
                    id: 'test',
                    profile: {
                        name: 'original',
                    },
                }],
            },
        });

        const snapshot = channel.presence as Array<Record<string, any>>;

        expect(snapshot[0].profile.name).toBe('original');
        snapshot[0].profile.name = 'changed by getter';
        expect((channel.presence as Array<Record<string, any>>)[0].profile.name).toBe('original');
    });

    it('should notify subscribers when any presence event occurs', () => {
        const { channel, receiver, state } = createChannel();

        const presenceListener = jest.fn();

        channel.onPresenceChange(presenceListener);

        expect(presenceListener).not.toHaveBeenCalled();

        // acknowledge the join
        channel.join();
        channel.acknowledge(receiver);

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).toHaveBeenCalledWith({
            changed: {
                id: 'test',
                status: 'online',
            },
            presence: [
                {
                    id: 'test',
                    status: 'online',
                },
                {
                    id: 'test2',
                    status: 'online',
                },
            ],
        });

        // messages for other channels should not be sent to the listener
        // since the other presence events listeners rely on this function we don't need to test this on those tests

        presenceListener.mockClear();

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test2',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();
        // if the channel is stalled, the presence event should not be sent to the listener
        state.publish(ConnectionState.DISCONNECTED);
        expect(channel.channelState).toBe(ChannelState.STALLED);

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();
        state.publish(ConnectionState.CONNECTED);
        expect(channel.channelState).toBe(ChannelState.STALLED);

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();

        // if the channel is closed, the presence event should not be sent to the listener
        channel.acknowledge(receiver);

        channel.leave();

        receiver.publish({
            requestId: 'test',
            action: ServerActions.PRESENCE,
            event: PresenceEventTypes.UPDATE,
            channelName: 'test',
            payload: {
                changed: {
                    id: 'test',
                    status: 'online',
                },
                presence: [
                    {
                        id: 'test',
                        status: 'online',
                    },
                    {
                        id: 'test2',
                        status: 'online',
                    },
                ],
            },
        });

        expect(presenceListener).not.toHaveBeenCalled();
    });

    // message events

    it('should notify subscribers when a message is received', () => {
        const { channel, receiver } = createChannel();

        const messageListener = jest.fn();

        channel.join();
        channel.onMessage(messageListener);

        expect(messageListener).not.toHaveBeenCalled();

        receiver.publish({
            requestId: 'test',
            action: ServerActions.BROADCAST,
            event: 'message',
            channelName: 'test',
            payload: {
                id: 'test',
                message: 'test',
            },
        });

        expect(messageListener).not.toHaveBeenCalled(); // the server should acknowledge the join first

        // acknowledge the join
        channel.acknowledge(receiver);

        receiver.publish({
            requestId: 'test',
            action: ServerActions.BROADCAST,
            event: 'message',
            channelName: 'test',
            payload: {
                id: 'test',
                message: 'test',
            },
        });

        expect(messageListener).toHaveBeenCalledWith('message', {
            id: 'test',
            message: 'test',
        });

        // if a message event is received for a different channel, it should not be sent to the listener
        receiver.publish({
            requestId: 'test',
            action: ServerActions.BROADCAST,
            event: 'message',
            channelName: 'test2',
            payload: {
                id: 'test',
                message: 'test',
            },
        });

        expect(messageListener).toHaveBeenCalledTimes(1);

        // we can also subscribe to a specific message event
        const specificMessageListener = jest.fn();

        channel.onMessageEvent('test', specificMessageListener);

        expect(specificMessageListener).not.toHaveBeenCalled();

        receiver.publish({
            requestId: 'test',
            action: ServerActions.BROADCAST,
            event: 'test',
            channelName: 'test',
            payload: {
                id: 'test',
                message: 'test',
            },
        });

        expect(specificMessageListener).toHaveBeenCalledWith({
            id: 'test',
            message: 'test',
        }, {
            params: {},
            query: {},
        });

        specificMessageListener.mockClear();
        // if a message event is received for the same channel, but a different event, it should not be sent to the listener
        receiver.publish({
            requestId: 'test',
            action: ServerActions.BROADCAST,
            event: 'test2',
            channelName: 'test',
            payload: {
                id: 'test',
                message: 'test',
            },
        });

        expect(specificMessageListener).not.toHaveBeenCalled();

        // if a message event is received for a different channel, it should not be sent to the listener
        receiver.publish({
            requestId: 'test',
            action: ServerActions.BROADCAST,
            event: 'test',
            channelName: 'test2',
            payload: {
                id: 'test',
                message: 'test',
            },
        });

        expect(specificMessageListener).not.toHaveBeenCalled();
    });

    it('should handle decline by setting state to DECLINED and clearing queue', () => {
        const { channel } = createChannel();

        channel.join();
        channel.sendMessage('queued', { data: 'test' });

        channel.decline({
            code: ErrorTypes.UNAUTHORIZED_JOIN_REQUEST,
            message: 'Not authorized',
            status: 403,
            statusCode: 403,
        });

        expect(channel.channelState).toBe(ChannelState.DECLINED);
        expect(channel.joinError).toEqual(expect.objectContaining({
            message: 'Not authorized',
            status: 403,
        }));
        expect(() => channel.sendMessage('queued', { data: 'test' })).toThrow(/Cannot send messages/);
    });

    it('should ignore a decline for a stale join request', () => {
        const { channel, publisher } = createChannel();

        channel.join();
        const requestId = publisher.mock.calls[0][0].requestId;
        const error = {
            code: ErrorTypes.CHANNEL_NOT_FOUND,
            message: 'Missing',
            status: 404,
        };

        channel.decline(error, 'stale-request');
        expect(channel.channelState).toBe(ChannelState.JOINING);

        channel.decline(error, requestId);
        expect(channel.channelState).toBe(ChannelState.DECLINED);
    });

    it('should time out a join that receives no terminal response', () => {
        jest.useFakeTimers();
        const publisher = jest.fn();
        const state = new BehaviorSubject<ConnectionState>(ConnectionState.CONNECTED);
        const channel = new Channel(publisher, state, 'missing', {}, 100, 50);

        channel.join();
        jest.advanceTimersByTime(50);

        expect(channel.channelState).toBe(ChannelState.DECLINED);
        expect(channel.joinError).toEqual(expect.objectContaining({
            status: 408,
        }));
        jest.useRealTimers();
    });

    it('should not re-join when already in JOINED state', () => {
        const { channel, publisher, receiver } = createChannel();

        channel.join();
        channel.acknowledge(receiver);

        expect(channel.channelState).toBe(ChannelState.JOINED);

        publisher.mockClear();
        channel.join();

        expect(publisher).not.toHaveBeenCalled();
    });

    it('sendForResponse should reject with timeout error', async () => {
        const publisher = jest.fn();
        const state = new BehaviorSubject<ConnectionState>(ConnectionState.CONNECTED);
        const receiver = new Subject<ChannelEvent>();

        const channel = new Channel(
            publisher,
            state,
            'test',
            {},
        );

        channel.join();
        channel.acknowledge(receiver);

        // @ts-expect-error - testing timeout behavior
        const promise = channel.sendForResponse('timeout-event', { data: 'test' }, 100);

        await expect(promise).rejects.toThrow(/sendForResponse timed out after 100ms/);
    });

    it('removes a queued sendForResponse request when it times out', async () => {
        jest.useFakeTimers();
        const publisher = jest.fn();
        const state = new BehaviorSubject<ConnectionState>(ConnectionState.DISCONNECTED);
        const receiver = new Subject<ChannelEvent>();
        const channel = new Channel(publisher, state, 'test', {});

        channel.join();
        // @ts-expect-error - testing timeout behavior
        const promise = channel.sendForResponse('timeout-event', { data: 'test' }, 100);

        jest.advanceTimersByTime(100);
        await expect(promise).rejects.toThrow(/sendForResponse timed out after 100ms/);
        state.publish(ConnectionState.CONNECTED);
        publisher.mockClear();
        channel.acknowledge(receiver);
        expect(publisher).not.toHaveBeenCalledWith(expect.objectContaining({
            event: 'timeout-event',
        }));
        jest.useRealTimers();
    });

    it('rejects pending response requests when the channel closes', async () => {
        const { channel, receiver } = createChannel();

        channel.join();
        channel.acknowledge(receiver);
        // @ts-expect-error - testing lifecycle cleanup
        const promise = channel.sendForResponse('WAITING', { data: 'test' });

        channel.leave();

        await expect(promise).rejects.toThrow(/Channel "test" was closed/);
    });

    it('rejects new response requests when the bounded queue is full without reordering queued work', async () => {
        const publisher = jest.fn();
        const state = new BehaviorSubject<ConnectionState>(ConnectionState.CONNECTED);
        const channel = new Channel(publisher, state, 'test', {}, 1);

        // @ts-expect-error - testing bounded queue behavior
        const first = channel.sendForResponse('FIRST', { data: 'first' });
        // @ts-expect-error - testing bounded queue behavior
        const second = channel.sendForResponse('SECOND', { data: 'second' });

        await expect(second).rejects.toThrow(/message queue capacity was exceeded/);
        channel.leave();
        await expect(first).rejects.toThrow(/Channel "test" was closed/);
        expect(publisher).not.toHaveBeenCalled();
    });

    it('should be able to wait for a message', async () => {
        const state = new BehaviorSubject<ConnectionState>(ConnectionState.CONNECTED);
        const receiver = new Subject<ChannelEvent>();
        const params = {};

        const publisher = (data: ClientMessage) => {
            if (data.event === 'WAITING') {
                // if the server response with another request id, it should not be sent to the listener
                receiver.publish({
                    requestId: 'test',
                    action: ServerActions.BROADCAST,
                    event: 'test',
                    channelName: 'test',
                    payload: {
                        id: 'test',
                        response: 'test-response',
                    },
                });

                // if the server response with the same request id, it should be sent to the listener
                receiver.publish({
                    requestId: data.requestId,
                    action: ServerActions.BROADCAST,
                    event: 'test',
                    channelName: 'test',
                    payload: {
                        id: 'test',
                        response: 'second-test-response',
                    },
                });
            }
        };

        const channel = new Channel(
            publisher,
            state,
            'test',
            params,
        );

        channel.join();

        channel.acknowledge(receiver);

        // @ts-expect-error - this is a test
        const response: any = await channel.sendForResponse('WAITING', {
            id: 'test',
            message: 'test',
        });

        expect(response).toEqual({
            id: 'test',
            response: 'second-test-response',
        });
    });

    it('should build and match event route parameters', () => {
        type RouteSchema = PondSchema<{
            'message/:messageId': { text: string };
        }>;
        const publisher = jest.fn();
        const state = new BehaviorSubject<ConnectionState>(ConnectionState.CONNECTED);
        const receiver = new Subject<ChannelEvent>();
        const channel = new Channel<RouteSchema>(publisher, state, 'test', {});
        const listener = jest.fn();

        channel.join();
        channel.acknowledge(receiver);
        publisher.mockClear();
        channel.onMessageEvent('message/:messageId', listener);

        channel.sendMessage('message/:messageId', { text: 'hello' }, { messageId: 'message 1' });

        expect(publisher).toHaveBeenCalledWith(expect.objectContaining({
            event: 'message/message%201',
            payload: { text: 'hello' },
        }));

        receiver.publish({
            requestId: 'request-1',
            action: ServerActions.BROADCAST,
            event: 'message/message%202?source=history',
            channelName: 'test',
            payload: { text: 'saved' },
        });

        expect(listener).toHaveBeenCalledWith({ text: 'saved' }, {
            params: { messageId: 'message 2' },
            query: { source: 'history' },
        });
    });
});
