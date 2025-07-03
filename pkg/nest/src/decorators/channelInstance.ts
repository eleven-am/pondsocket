import { manageChannelInstance } from '../managers/channelInstance';

export function ChannelInstance (channel?: string): PropertyDecorator {
    return (target, propertyKey) => {
        const { build } = manageChannelInstance(target);

        build(propertyKey as string, (value) => {
            if (channel) {
                return value.getChannel(channel);
            }

            return value;
        });
    };
}
