#!/usr/bin/env node
import { assertLoadedDataset } from './helpers/dataset-preflight.mjs';

const args = process.argv.slice(2);
const value = (name) => {
  const index = args.indexOf(`--${name}`);
  return index >= 0 ? args[index + 1] : undefined;
};
const apiOrigin = value('api') ?? process.env.LOOM_CDA_API_ORIGIN ?? process.env.LOOM_FAST_API_URL;
const project = value('project') ?? process.env.LOOM_CDA_PROJECT ?? process.env.LOOM_FAST_PROJECT;
const generation = value('generation') ?? process.env.LOOM_CDA_GENERATION ?? process.env.LOOM_FAST_GENERATION;
const explorerId = value('explorer') ?? process.env.LOOM_CDA_EXPLORER_ID ?? process.env.LOOM_FAST_EXPLORER;

try {
  const result = await assertLoadedDataset({ apiOrigin, project, generation, explorerId });
  console.log('DATASET_READY ' + JSON.stringify(result));
} catch (error) {
  console.error('DATASET_NOT_READY ' + (error instanceof Error ? error.message : String(error)));
  process.exit(1);
}
