import { readFileSync } from 'node:fs';
import { defineConfig } from 'vitest/config';

const mutationTarget = process.env.LOOM_MUTATION_TARGET;
const mutationSource = process.env.LOOM_MUTATION_SOURCE;
const mutationOverrides: Record<string, string> = process.env.LOOM_MUTATION_OVERRIDES
  ? JSON.parse(readFileSync(process.env.LOOM_MUTATION_OVERRIDES, 'utf8')) : {};

export default defineConfig({
  cacheDir: process.env.LOOM_VITEST_CACHE_DIR,
  plugins: [{
    name: 'loom-mutation-overlay',
    enforce: 'pre',
    load(id: string) {
      const override = mutationOverrides[id.split('?')[0]];
      if (override) return readFileSync(override, 'utf8');
      if (!mutationTarget || !mutationSource) return null;
      if (id.split('?')[0] === mutationTarget) return readFileSync(mutationSource, 'utf8');
      return null;
    },
  }],
  test: {
    environment: 'node',
    globals: true,
    setupFiles: ['./vitest.setup.ts'],
    testTimeout: 30_000,
    hookTimeout: 30_000,
  },
});
