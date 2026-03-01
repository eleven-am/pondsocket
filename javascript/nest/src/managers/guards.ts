import { pondGuardsKey } from '../constants';
import { CanActivate } from '../types';
import { createMetadataManager } from './createMetadataManager';

const { manage, getLocalItems } = createMetadataManager<CanActivate>(pondGuardsKey);

export const manageGuards = manage;

export const getLocalGuards = getLocalItems;
