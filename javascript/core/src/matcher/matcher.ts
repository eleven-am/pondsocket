import { EventParams, parsePondRoute, PondPath } from '@eleven-am/pondsocket-common';

export function parseAddress <Path extends string> (route: PondPath<Path>, address: string) {
    if (route instanceof RegExp) {
        const match = address.match(route);

        if (!match) {
            return null;
        }

        return {
            params: {},
            query: {},
        } as EventParams<Path>;
    }

    return parsePondRoute(route, address);
}
