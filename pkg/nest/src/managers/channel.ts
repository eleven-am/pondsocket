import { manageClass } from './class';
import { channelKey } from '../constants';

export function manageChannel (target: unknown) {
    return manageClass<string>(channelKey, target);
}
