import {
    ChannelReceiver,
    ChannelReceivers,
    EventParams,
    PondEvent,
    PondMessage,
    PondObject,
    UserAssigns,
    UserPresences,
} from '@eleven-am/pondsocket-common';

import { ChannelEngine } from '../engines/channelEngine';
import { Channel } from '../wrappers/channel';

export abstract class BaseContext<Path extends string> {
    readonly #engine: ChannelEngine;

    #params: EventParams<Path>;

    readonly #event: string;

    readonly #sender: string;

    readonly #payload: PondMessage;

    constructor (
        engine: ChannelEngine,
        params: EventParams<Path>,
        event: string,
        payload: PondMessage,
        sender: string,
    ) {
        this.#engine = engine;
        this.#params = params;
        this.#event = event;
        this.#payload = payload;
        this.#sender = sender;
    }

    get event (): PondEvent<Path> {
        return {
            event: this.#event,
            query: this.#params.query,
            params: this.#params.params,
            payload: this.#payload,
        };
    }

    get channelName (): string {
        return this.#engine.name;
    }

    get channel (): Channel {
        return new Channel(this.#engine);
    }

    get presences (): UserPresences {
        return this.#engine.getPresence();
    }

    get assigns (): UserAssigns {
        return this.#engine.getAssigns();
    }

    get user () {
        return this.channel.getUserData(this.#sender);
    }

    broadcast (event: string, payload: PondMessage): this {
        this._sendMessageToRecipients(ChannelReceiver.ALL_USERS, event, payload);

        return this;
    }

    broadcastFrom (event: string, payload: PondMessage): this {
        this._sendMessageToRecipients(ChannelReceiver.ALL_EXCEPT_SENDER, event, payload);

        return this;
    }

    broadcastTo (event: string, payload: PondMessage, userIds: string | string[]): this {
        const ids = Array.isArray(userIds) ? userIds : [userIds];

        this._sendMessageToRecipients(ids, event, payload);

        return this;
    }

    protected abstract _sendMessageToRecipients (recipient: ChannelReceivers, event: string, payload: PondObject): void;

    protected get engine (): ChannelEngine {
        return this.#engine;
    }

    protected get sender (): string {
        return this.#sender;
    }

    protected updateParams (params: EventParams<Path>): void {
        this.#params = params;
    }
}
