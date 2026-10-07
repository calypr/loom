import { resolve } from 'node:path';

export const environmentForFixtureDir = (env = process.env, fixtureDir, fixtureGeneration) => {
  const selectedGeneration = String(fixtureGeneration ?? '').trim();
  const selectedFixture = fixtureDir !== undefined && fixtureDir !== null && String(fixtureDir).trim() !== '';
  if (!selectedFixture && !selectedGeneration) return env;
  const selectedEnv = selectedGeneration ? { ...env, LOOM_DEV_GENERATION: selectedGeneration } : env;
  if (!selectedFixture) return selectedEnv;
  const sourceRoot = String(env.LOOM_DEV_SOURCE_ROOT ?? '').trim();
  if (!sourceRoot) throw new Error('LOOM_DEV_SOURCE_ROOT is required when fixtureDir is selected');
  return { ...selectedEnv, LOOM_DEV_FIXTURE_DIR: resolve(sourceRoot, String(fixtureDir)) };
};
