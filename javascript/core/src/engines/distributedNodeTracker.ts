import { Unsubscribe } from '@eleven-am/pondsocket-common';

import { IDistributedBackend } from '../types';

type StaleNodeHandler = (nodeId: string) => void;

interface TrackedNode {
    lastSeen: number;
    references: number;
}

/**
 * Shares one heartbeat subscription and one stale-node sweep across every
 * concrete channel owned by a lobby. Channels retain only their own user map
 * and a lightweight stale-node callback.
 */
export class DistributedNodeTracker {
    readonly #backend: IDistributedBackend;

    readonly #handlers: Set<StaleNodeHandler> = new Set();

    readonly #nodes: Map<string, TrackedNode> = new Map();

    readonly #heartbeatSubscription: Unsubscribe;

    readonly #staleNodeTimer: ReturnType<typeof setInterval>;

    #closed: boolean = false;

    constructor (backend: IDistributedBackend) {
        this.#backend = backend;
        this.#heartbeatSubscription = backend.subscribeToHeartbeats((nodeId) => {
            const node = this.#nodes.get(nodeId);

            if (node) {
                node.lastSeen = Date.now();
            }
        });
        this.#staleNodeTimer = setInterval(() => this.#cleanupStaleNodes(), backend.heartbeatTimeoutMs);
        this.#staleNodeTimer.unref?.();
    }

    subscribe (handler: StaleNodeHandler): Unsubscribe {
        if (this.#closed) {
            throw new Error('Distributed node tracker is closed');
        }

        this.#handlers.add(handler);
        let active = true;

        return () => {
            if (!active) {
                return;
            }
            active = false;
            this.#handlers.delete(handler);
        };
    }

    track (nodeId: string): void {
        if (this.#closed) {
            return;
        }

        const node = this.#nodes.get(nodeId);

        if (node) {
            node.references += 1;
            node.lastSeen = Date.now();

            return;
        }

        this.#nodes.set(nodeId, {
            lastSeen: Date.now(),
            references: 1,
        });
    }

    untrack (nodeId: string): void {
        const node = this.#nodes.get(nodeId);

        if (!node) {
            return;
        }

        node.references -= 1;
        if (node.references <= 0) {
            this.#nodes.delete(nodeId);
        }
    }

    close (): void {
        if (this.#closed) {
            return;
        }

        this.#closed = true;
        clearInterval(this.#staleNodeTimer);
        this.#nodes.clear();
        this.#handlers.clear();

        try {
            this.#heartbeatSubscription();
        } catch (_) {
            void 0;
        }
    }

    #cleanupStaleNodes (): void {
        const now = Date.now();

        for (const [nodeId, node] of this.#nodes) {
            if (now - node.lastSeen < this.#backend.heartbeatTimeoutMs) {
                continue;
            }

            this.#nodes.delete(nodeId);
            for (const handler of [...this.#handlers]) {
                try {
                    handler(nodeId);
                } catch (_) {
                    void 0;
                }
            }
        }
    }
}
