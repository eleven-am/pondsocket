import { EventParams, PondEvent, PondMessage, UserAssigns, UserPresences } from '@eleven-am/pondsocket-common';

import { ChannelEngine } from '../engines/channelEngine';
import { Channel } from '../wrappers/channel';

export class BaseContext<Path extends string> {
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

    /**
     * The event information
     */
    get event (): PondEvent<Path> {
        return {
            event: this.#event,
            query: this.#params.query,
            params: this.#params.params,
            payload: this.#payload,
        };
    }

    /**
     * The channel name
     */
    get channelName (): string {
        return this.#engine.name;
    }

    /**
     * The channel instance
     */
    get channel (): Channel {
        return new Channel(this.#engine);
    }

    /**
     * All current presences in the channel
     */
    get presences (): UserPresences {
        return this.#engine.getPresence();
    }

    /**
     * All current assigns in the channel
     */
    get assigns (): UserAssigns {
        return this.#engine.getAssigns();
    }

    /**
     * The user who sent the request
     */
    get user () {
        return this.channel.getUserData(this.#sender);
    }

    /**
     * Gets the channel engine instance
     * @protected
     */
    protected get engine (): ChannelEngine {
        return this.#engine;
    }

    /**
     * Updates the event parameters.
     */
    protected updateParams (params: EventParams<Path>): void {
        this.#params = params;
    }
}
