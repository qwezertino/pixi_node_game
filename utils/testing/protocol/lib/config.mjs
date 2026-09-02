// The probes need the same movement constants the client and server share. Reading the
// shared config keeps a tuning change from silently invalidating every assertion.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const config = JSON.parse(readFileSync(join(here, '../../../../src/shared/gameConfig.json'), 'utf8'));

export const { movement: MOVEMENT, network: NETWORK, world: WORLD } = config;
