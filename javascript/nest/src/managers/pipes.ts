import type { PipeTransform } from '@nestjs/common';

import { pondValidatorsKey } from '../constants';
import { createMetadataManager } from './createMetadataManager';

const { manage, getLocalItems } = createMetadataManager<PipeTransform>(pondValidatorsKey);

export const managePipes = manage;

export const getLocalPipes = getLocalItems;
