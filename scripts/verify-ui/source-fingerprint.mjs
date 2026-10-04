import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const watchedPaths = [
  'cmd', 'internal', 'generated', 'schemas', 'openapi',
  'ui/packages/loom-ui/src', 'ui/apps/demo/src',
  'go.mod', 'go.sum', 'compose.dev.yaml', '.air.dev.toml',
  'ui/apps/demo/index.html', 'ui/apps/demo/vite.config.ts',
];

export const sourceFingerprint = (root) => {
  const hash = createHash('sha256');
  let files = 0;
  const visit = (relative) => {
    const absolute = join(root, relative);
    const stat = lstatSync(absolute, { throwIfNoEntry: false });
    if (!stat) return;
    if (stat.isDirectory()) {
      for (const name of readdirSync(absolute).sort()) visit(join(relative, name));
      return;
    }
    if (!stat.isFile()) return;
    hash.update(relative);
    hash.update('\0');
    hash.update(readFileSync(absolute));
    files += 1;
  };
  for (const path of watchedPaths) visit(path);
  if (files === 0) throw new Error(`no watched source files found at ${root}`);
  return { sha256: hash.digest('hex'), files };
};
