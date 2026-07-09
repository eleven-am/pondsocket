import {
    AnyPondSchema,
    EventPayload,
    EventsOf,
    PondPath,
    RouteParamsArguments,
} from '@eleven-am/pondsocket-common';

import { Channel } from './channel';
import { EventHandler, LeaveCallback, OutgoingEventHandler } from '../abstracts/types';
import { LobbyEngine } from '../engines/lobbyEngine';


export class PondChannel<Schema extends AnyPondSchema = AnyPondSchema> {
    readonly #lobby: LobbyEngine;

    constructor (lobby: LobbyEngine) {
        this.#lobby = lobby;
    }

    public onEvent<Event extends Extract<keyof EventsOf<Schema>, string>> (event: PondPath<Event>, handler: EventHandler<Event, Schema, Event>): PondChannel<Schema> {
        this.#lobby.onEvent<Schema, Event>(event, handler);

        return this;
    }

    public onLeave (callback: LeaveCallback): PondChannel<Schema> {
        this.#lobby.onLeave(callback);

        return this;
    }

    public handleOutgoingEvent<Event extends Extract<keyof EventsOf<Schema>, string>> (event: PondPath<Event>, handler: OutgoingEventHandler<Event, Schema, Event>) {
        this.#lobby.handleOutgoingEvent<Schema, Event>(event, handler);

        return this;
    }

    public getChannel (channelName: string): Channel<Schema> | null {
        try {
            return this.#getChannel(channelName);
        } catch {
            return null;
        }
    }

    public broadcast<Event extends Extract<keyof EventsOf<Schema>, string>> (channelName: string, event: Event, payload: EventPayload<EventsOf<Schema>, Event>, ...args: RouteParamsArguments<Event>): PondChannel<Schema> {
        this.#getChannel(channelName).broadcast(event, payload, ...args);

        return this;
    }

    public broadcastFrom<Event extends Extract<keyof EventsOf<Schema>, string>> (channelName: string, userId: string, event: Event, payload: EventPayload<EventsOf<Schema>, Event>, ...args: RouteParamsArguments<Event>): PondChannel<Schema> {
        this.#getChannel(channelName).broadcastFrom(userId, event, payload, ...args);

        return this;
    }

    public broadcastTo<Event extends Extract<keyof EventsOf<Schema>, string>> (channelName: string, userIds: string | string[], event: Event, payload: EventPayload<EventsOf<Schema>, Event>, ...args: RouteParamsArguments<Event>): PondChannel<Schema> {
        this.#getChannel(channelName).broadcastTo(userIds, event, payload, ...args);

        return this;
    }

    #getChannel (channelName: string) {
        const channel = this.#lobby.getChannel(channelName);

        return new Channel<Schema>(channel);
    }
}
