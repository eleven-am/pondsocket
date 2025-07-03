import { Event, PondPath } from '@eleven-am/pondsocket-common';

import { ChannelEngine } from './channelEngine';
import { EndpointEngine } from './endpointEngine';
import { Middleware } from '../abstracts/middleware';
import { BroadcastEvent, EventHandler, LeaveCallback, OutgoingEventHandler } from '../abstracts/types';
import { EventContext } from '../contexts/eventContext';
import { OutgoingContext } from '../contexts/outgoingContext';
import { HttpError } from '../errors/httpError';
import { parseAddress } from '../matcher/matcher';
import { IDistributedBackend } from '../types';
import { Channel } from '../wrappers/channel';


export class LobbyEngine {
    leaveCallback: LeaveCallback | undefined;

    readonly middleware: Middleware<BroadcastEvent, ChannelEngine>;

    readonly outgoing: Middleware<OutgoingContext<string>, Event>;

    readonly #backend: IDistributedBackend | null;

    readonly #channels: Map<string, ChannelEngine>;

    constructor (public parent: EndpointEngine, backend: IDistributedBackend | null) {
        this.middleware = new Middleware();
        this.outgoing = new Middleware();
        this.#channels = new Map();
        this.#backend = backend;
    }

    /**
     * Sets the callback for when users leave channels
     */
    onLeave (callback: LeaveCallback) {
        this.leaveCallback = callback;
    }

    /**
     * Attaches a handler for a specific event pattern
     */
    onEvent<Event extends string> (event: PondPath<Event>, handler: EventHandler<Event>) {
        this.middleware.use((requestEvent, channel, next) => {
            const params = parseAddress(event, requestEvent.event);

            if (!params) {
                return next();
            }

            const context = new EventContext(requestEvent, params, channel);

            return handler(context, next);
        });
    }

    /**
     * Attaches a handler for outgoing events
     */
    handleOutgoingEvent<Event extends string> (event: PondPath<Event>, handler: OutgoingEventHandler<Event>) {
        this.outgoing.use(async (context, chEvent, next) => {
            const params = parseAddress(event, chEvent.event);

            if (!params || context.isBlocked()) {
                return next();
            }

            context.updateParams(params);
            const payload = await handler(context, next);

            if (payload === undefined || payload === null) {
                return;
            }

            context.transform({
                ...chEvent,
                payload,
            });

            return next();
        });
    }

    /**
     * Processes an outgoing event, applying middleware and returning a new event
     * @param event - The channel event to process
     * @param engine - The channel engine handling the event
     * @param userId - The ID of the user sending the event
     * @returns A new ChannelEvent or undefined if blocked
     */
    async processOutgoingEvents (event: Event, engine: ChannelEngine, userId: string): Promise<Event | undefined> {
        const params = parseAddress('*', event.event);
        const context = new OutgoingContext(event, params!, engine, userId);

        await this.outgoing.runAsync(context, event, () => {
            // no-op
        });

        if (context.isBlocked()) {
            return;
        }

        return {
            ...event,
            payload: context.payload,
            event: context.event.event,
        };
    }

    /**
     * Gets or creates a channel by name
     */
    getOrCreateChannel (channelName: string) {
        const oldChannel = this.#getChannel(channelName);

        if (oldChannel) {
            return oldChannel;
        }

        return this.#createChannel(channelName);
    }

    /**
     * Gets a channel by name
     * @throws HttpError if a channel not found
     */
    getChannel (channelName: string): ChannelEngine {
        const channel = this.#channels.get(channelName);

        if (!channel) {
            throw new HttpError(404, `Channel ${channelName} not found`);
        }

        return channel;
    }

    /**
     * Creates a Channel wrapper from a ChannelEngine
     * Used by the channel engine for the leave callback
     */
    wrapChannel (engine: ChannelEngine): Channel {
        return new Channel(engine);
    }

    /**
     * Broadcasts an event to all users in a channel
     */
    deleteChannel (channelName: string) {
        const channel = this.#channels.get(channelName);

        if (channel) {
            this.#channels.delete(channelName);
        }
    }

    /**
     * Gets a channel by name or null if not found
     */
    #getChannel (channelName: string): ChannelEngine | null {
        return this.#channels.get(channelName) || null;
    }

    /**
     * Creates a new channel
     */
    #createChannel (channelName: string) {
        const channel = new ChannelEngine(this, channelName, this.#backend);

        this.#channels.set(channelName, channel);

        return channel;
    }
}
