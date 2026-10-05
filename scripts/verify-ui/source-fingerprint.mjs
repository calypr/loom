import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const watchedPaths = [
  'cmd', 'internal', 'generated', 'schemas', 'openapi',
  'ui/packages/loom-ui/src', 'ui/apps/demo/src',
  'scripts', 'testdata/devloop-fixture', 'testdata/verify-combine', 'testdata/verify-repeated-empty', 'testdata/root-quantity-pivot-fixture', '.codex/skills/verify',
  'go.mod', 'go.sum', 'compose.dev.yaml', '.air.dev.toml',
  'ui/apps/demo/index.html', 'ui/apps/demo/vite.config.ts',
];

export const sourceFingerprintWithManifest = (root) => {
  const hash = createHash('sha256');
  const manifest = {};
  let files = 0;
  const visit = (relative) => {
    const absolute = join(root, relative);
    const stat = lstatSync(absolute, { throwIfNoEntry: false });
    if (!stat) return;
    if (stat.isDirectory()) {
      for (const name of readdirSync(absolute).sort()) {
        if (name === 'node_modules' || name === '.artifacts') continue;
        visit(join(relative, name));
      }
      return;
    }
    if (!stat.isFile()) return;
    const content = readFileSync(absolute);
    hash.update(relative);
    hash.update('\0');
    hash.update(content);
    manifest[relative] = createHash('sha256').update(content).digest('hex');
    files += 1;
  };
  for (const path of watchedPaths) visit(path);
  if (files === 0) throw new Error(`no watched source files found at ${root}`);
  return { fingerprint: { sha256: hash.digest('hex'), files }, manifest };
};

export const sourceFingerprintChangedPaths = (before, after) => {
  const changedPaths = [];
  const paths = [...new Set([...Object.keys(before), ...Object.keys(after)])].sort();
  for (const path of paths) {
    if (!Object.hasOwn(before, path)) changedPaths.push({ path, change: 'added' });
    else if (!Object.hasOwn(after, path)) changedPaths.push({ path, change: 'removed' });
    else if (before[path] !== after[path]) changedPaths.push({ path, change: 'modified' });
  }
  return changedPaths;
};

export const sourceFingerprint = (root) => sourceFingerprintWithManifest(root).fingerprint;
