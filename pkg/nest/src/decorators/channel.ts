import { manageChannel } from '../managers/channel';

export function Channel (path = '*'): ClassDecorator {
    return (target) => {
        const { set } = manageChannel(target);

        set(path);
    };
}
