import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');

export const FAST_STATE_PATH = resolve(REPO_ROOT, '.artifacts/playwright-fast/fixture.json');

export const readFastState = () => {
  if (!existsSync(FAST_STATE_PATH)) return undefined;
  return JSON.parse(readFileSync(FAST_STATE_PATH, 'utf8'));
};

export const writeFastState = (state) => {
  mkdirSync(dirname(FAST_STATE_PATH), { recursive: true });
  writeFileSync(FAST_STATE_PATH, JSON.stringify(state, null, 2) + '\n');
};
