import { EventParams, PondPath } from '@eleven-am/pondsocket-common';

function pathToRegexp (path: string) {
    const parts = path.split('/');

    const regexpParts = parts.map((part) => {
        if (part.startsWith(':')) {
            return '([^/]+)';
        } else if (part === '*') {
            return '(.*)';
        }

        return part;
    });

    return new RegExp(`^${regexpParts.join('/')}$`);
}

function getParams (path: string, route: string) {
    const regexp = pathToRegexp(route);
    const match = path.match(regexp);

    if (!match) {
        return null;
    }

    const cleanRoute = route.replace(/:/g, '');

    const cleanMatch = cleanRoute.match(regexp);

    if (!cleanMatch) {
        return null;
    }

    const keys = cleanMatch.slice(1).filter((s) => s !== '');
    const values = match.slice(1).filter((s) => s !== '');

    if (keys.length !== values.length) {
        return {};
    }

    return keys.reduce((params, key, index) => {
        if (key === '*') {
            params[key] = `/${values.slice(index).join('/')}`.replace(/\/+/g, '/');

            return params;
        }

        params[key] = values[index];

        return params;
    }, {} as Record<string, string>);
}

function getQuery (query: string) {
    if (!query) {
        return {};
    }

    const parts = query.split('&');

    return parts.reduce((params, part) => {
        const [key, value] = part.split('=');

        params[key] = value;

        return params;
    }, {} as Record<string, string>);
}

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

    const [path, queryParams] = address.split('?');
    const params = getParams(path, route);

    if (!params) {
        return null;
    }

    const query = getQuery(queryParams);

    return {
        params,
        query,
    } as EventParams<Path>;
}
