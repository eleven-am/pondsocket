import { z } from 'zod';

import { ClientActions, PresenceEventTypes, ServerActions } from './enums';

export const clientMessageSchema = z.object({
    event: z.string(),
    requestId: z.string(),
    channelName: z.string(),
    payload: z.record(z.any()),
    action: z.nativeEnum(ClientActions),
});

export type ClientMessage = z.infer<typeof clientMessageSchema>;

export const presenceMessageSchema = z.object({
    requestId: z.string(),
    channelName: z.string(),
    event: z.nativeEnum(PresenceEventTypes),
    action: z.literal(ServerActions.PRESENCE),
    payload: z.object({
        presence: z.array(z.record(z.any())),
        changed: z.record(z.any()),
    }),
});

export const serverMessageSchema = z.object({
    event: z.string(),
    requestId: z.string(),
    channelName: z.string(),
    payload: z.record(z.any()),
    action: z.enum([ServerActions.BROADCAST, ServerActions.CONNECT, ServerActions.ERROR, ServerActions.SYSTEM]),
});

export const channelEventSchema = z.discriminatedUnion('action', [serverMessageSchema, presenceMessageSchema]);
