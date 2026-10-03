import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { lstat, readFile, readlink, realpath } from 'node:fs/promises';
import { promisify } from 'node:util';
import { join, sep } from 'node:path';

const execFileAsync = promisify(execFile);
const ignoredSegments = new Set(['.git', '.gitnexus', 'node_modules', 'datasets', 'evidence']);

export class SourceFreezeError extends Error {
  constructor(changedPaths) {
    super(`Verifier run invalidated: ${changedPaths.length} watched source path(s) changed during the run.`);
    this.name = 'SourceFreezeError';
    this.changedPaths = changedPaths;
    this.invalidatesRun = true;
    this.productFailure = false;
  }
}

function isWatchedPath(filePath) {
  const normalized = filePath.split(sep).join('/');
  const parts = normalized.split('/');
  if (parts.some((part) => ignoredSegments.has(part))) return false;
  return normalized.startsWith('internal/') ||
    normalized.startsWith('ui/src/') ||
    /^ui\/packages\/[^/]+\/src\//.test(normalized);
}

function digest(content) {
  return createHash('sha256').update(content).digest('hex');
}

async function contentIdentity(absolutePath) {
  let stat;
  try {
    stat = await lstat(absolutePath);
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }

  if (stat.isSymbolicLink()) {
    return { kind: 'symlink', hash: digest(Buffer.from(await readlink(absolutePath))) };
  }
  if (!stat.isFile()) return { kind: 'other', hash: digest(Buffer.from(stat.isDirectory() ? 'directory' : 'non-file')) };
  return { kind: 'file', hash: digest(await readFile(absolutePath)) };
}

async function snapshot(rootPath) {
  const root = await realpath(rootPath);
  const { stdout } = await execFileAsync('git', [
    'ls-files', '--cached', '--others', '--exclude-standard', '-z', '--',
    'ui/src', 'ui/packages', 'internal',
  ], { cwd: root, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });

  const paths = stdout.split('\0').filter(Boolean).filter(isWatchedPath).sort();
  const entries = new Map();
  for (const filePath of paths) {
    entries.set(filePath, await contentIdentity(join(root, filePath)));
  }
  return entries;
}

function compareSnapshots(before, after) {
  const changedPaths = [];
  const paths = [...new Set([...before.keys(), ...after.keys()])].sort();
  for (const path of paths) {
    const prior = before.get(path);
    const current = after.get(path);
    if (prior === undefined) {
      changedPaths.push({ path, change: 'added' });
    } else if (current === undefined || current === null) {
      if (prior !== null) changedPaths.push({ path, change: 'removed' });
    } else if (prior === null) {
      changedPaths.push({ path, change: 'added' });
    } else if (prior.kind !== current.kind || prior.hash !== current.hash) {
      changedPaths.push({ path, change: 'modified' });
    }
  }
  return changedPaths;
}

export async function captureSourceFreeze(rootPath) {
  const before = await snapshot(rootPath);
  return Object.freeze({
    watchedFileCount: before.size,
    async assertUnchanged() {
      const after = await snapshot(rootPath);
      const changedPaths = compareSnapshots(before, after);
      if (changedPaths.length > 0) throw new SourceFreezeError(changedPaths);
      return {
        unchanged: true,
        watchedFileCount: before.size,
        changedPaths: [],
        invalidatesRun: false,
        productFailure: false,
      };
    },
  });
}
