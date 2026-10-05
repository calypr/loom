import { resolve } from 'node:path';

export const environmentForFixtureDir = (env = process.env, fixtureDir) => {
  if (fixtureDir === undefined || fixtureDir === null || String(fixtureDir).trim() === '') return env;
  const sourceRoot = String(env.LOOM_DEV_SOURCE_ROOT ?? '').trim();
  if (!sourceRoot) throw new Error('LOOM_DEV_SOURCE_ROOT is required when fixtureDir is selected');
  return { ...env, LOOM_DEV_FIXTURE_DIR: resolve(sourceRoot, String(fixtureDir)) };
};
