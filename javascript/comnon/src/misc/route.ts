import { EventParams, Params } from './types';

export class PondRouteError extends Error {
    constructor (message: string, public readonly route: string) {
        super(`Invalid PondSocket route "${route}": ${message}`);
        this.name = 'PondRouteError';
    }
}

interface CompiledRoute {
    keys: string[];
    regexp: RegExp;
    route: string;
}

function escapeRegExp (value: string) {
    return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function parseSegments (route: string) {
    if (route.includes('?')) {
        throw new PondRouteError('query strings are not allowed in route declarations', route);
    }

    const keys: string[] = [];
    const segments = route.split('/');
    const regexpParts = segments.map((segment) => {
        if (segment === '*') {
            if (keys.includes('*')) {
                throw new PondRouteError('a wildcard can only appear once', route);
            }

            keys.push('*');

            return '(.*)';
        }

        if (segment.includes('*')) {
            throw new PondRouteError('wildcards must occupy an entire path segment', route);
        }

        if (segment.startsWith(':')) {
            const key = segment.slice(1);

            if (!(/^[A-Za-z_][A-Za-z0-9_]*$/).test(key)) {
                throw new PondRouteError('parameter names must be non-empty JavaScript identifiers', route);
            }

            if (keys.includes(key)) {
                throw new PondRouteError(`duplicate parameter "${key}"`, route);
            }

            keys.push(key);

            return '([^/]+)';
        }

        if (segment.includes(':')) {
            throw new PondRouteError('parameters must occupy an entire path segment', route);
        }

        return escapeRegExp(segment);
    });

    return {
        keys,
        regexp: new RegExp(`^${regexpParts.join('/')}$`),
    };
}

export function compilePondRoute (route: string): CompiledRoute {
    const { keys, regexp } = parseSegments(route);

    return {
        keys,
        regexp,
        route,
    };
}

export function parsePondRoute<Path extends string> (route: Path, address: string): EventParams<Path> | null {
    const compiled = compilePondRoute(route);
    const [path, queryString = ''] = address.split('?');
    const match = compiled.regexp.exec(path);

    if (!match) {
        return null;
    }

    const params = compiled.keys.reduce((result, key, index) => {
        const rawValue = match[index + 1] ?? '';
        const value = key === '*' ? rawValue : decodeURIComponent(rawValue);

        result[key] = key === '*' ? `/${value}`.replace(/\/+/g, '/') : value;

        return result;
    }, {} as Record<string, string>);
    const query: Record<string, string> = {};

    new URLSearchParams(queryString).forEach((value, key) => {
        query[key] = value;
    });

    return {
        params: params as Params<Path>,
        query,
    };
}

export function buildPondRoute<Path extends string> (route: Path, params: Params<Path>): string {
    compilePondRoute(route);

    return route.split('/')
        .map((segment) => {
            const key = segment === '*' ? '*' : segment.startsWith(':') ? segment.slice(1) : null;

            if (!key) {
                return segment;
            }

            const value = (params as Record<string, string>)[key];

            if (typeof value !== 'string' || value.length === 0) {
                throw new PondRouteError(`missing value for parameter "${key}"`, route);
            }

            if (key === '*') {
                return value.replace(/^\/+/, '');
            }

            return encodeURIComponent(value);
        })
        .join('/');
}
