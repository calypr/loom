import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';

const NEW_NATIVE_SOURCE = 'new-native-source';
const NEW_NATIVE_SOURCE_UNVERIFIED = 'new-native-source-unverified';
const PROVENANCE_KEYS = [
  'kind',
  'introducedCommit',
  'parentCommit',
  'introducedBlobSha256',
];

function hasExactProvenanceShape(value) {
  return value !== null
    && typeof value === 'object'
    && !Array.isArray(value)
    && Object.keys(value).sort().join(',') === [...PROVENANCE_KEYS].sort().join(',')
    && value.kind === NEW_NATIVE_SOURCE
    && /^[0-9a-f]{40}$/.test(value.introducedCommit)
    && /^[0-9a-f]{40}$/.test(value.parentCommit)
    && /^[0-9a-f]{64}$/.test(value.introducedBlobSha256);
}

function gitOutput(repositoryRoot, args, encoding = 'utf8') {
  return execFileSync('git', args, { cwd: repositoryRoot, encoding, stdio: ['ignore', 'pipe', 'pipe'] });
}

function hasGitPath(repositoryRoot, revision, sourcePath) {
  try {
    gitOutput(repositoryRoot, ['cat-file', '-e', `${revision}:${sourcePath}`]);
    return true;
  } catch {
    return false;
  }
}

function newNativeSourceProofIssue(sourcePath, record, repositoryRoot) {
  if (!hasExactProvenanceShape(record)) return 'record has an invalid new-native-source shape';
  if (typeof sourcePath !== 'string' || sourcePath.startsWith('/') || sourcePath.includes('\\')
    || sourcePath.split('/').some((segment) => !segment || segment === '.' || segment === '..' || segment.includes(':'))) {
    return 'source path is not a normalized repository-relative path';
  }

  let commitLine;
  try {
    commitLine = gitOutput(repositoryRoot, ['rev-list', '--parents', '-n', '1', record.introducedCommit]).trim();
  } catch {
    return 'introduced commit is not available in the repository';
  }
  const [commit, ...parents] = commitLine.split(/\s+/);
  if (commit !== record.introducedCommit || parents.length !== 1 || parents[0] !== record.parentCommit) {
    return 'introduced commit is not a single-parent commit with the recorded parent';
  }
  if (hasGitPath(repositoryRoot, record.parentCommit, sourcePath)) {
    return 'path existed at the recorded parent';
  }

  let changes;
  try {
    changes = gitOutput(repositoryRoot, ['diff', '--name-status', record.parentCommit, record.introducedCommit, '--', sourcePath]).trim();
  } catch {
    return 'introduced commit diff could not be read';
  }
  if (!changes.split('\n').includes(`A\t${sourcePath}`) || !hasGitPath(repositoryRoot, record.introducedCommit, sourcePath)) {
    return 'path was not added at the introduced commit';
  }

  let contents;
  try {
    contents = gitOutput(repositoryRoot, ['show', `${record.introducedCommit}:${sourcePath}`], null);
  } catch {
    return 'introduced source blob could not be read';
  }
  const actualHash = createHash('sha256').update(contents).digest('hex');
  if (actualHash !== record.introducedBlobSha256) return 'introduced blob SHA-256 does not match';
  return null;
}

function sourceMatchesProvenance(source, record) {
  const provenance = source?.historicalPreimageProvenance;
  return hasExactProvenanceShape(provenance)
    && PROVENANCE_KEYS.every((key) => provenance[key] === record[key]);
}

export function sourcePreimageLedgerIssue(sourcePath, source, record, repositoryRoot) {
  if (typeof record === 'string') {
    if (!source?.preimageSha256 || record !== source.preimageSha256) {
      return `source preimage is not bound to the persistent historical hash ledger: ${sourcePath}`;
    }
    return null;
  }

  if (record?.kind !== NEW_NATIVE_SOURCE) {
    return `source preimage is not bound to the persistent historical hash ledger: ${sourcePath}`;
  }
  if (source?.preimageSha256 !== null
    || source?.historicalPreimageStatus !== NEW_NATIVE_SOURCE_UNVERIFIED
    || !sourceMatchesProvenance(source, record)) {
    return `new-native-source provenance does not match the persistent record: ${sourcePath}`;
  }

  const proofIssue = newNativeSourceProofIssue(sourcePath, record, repositoryRoot);
  return proofIssue ? `new-native-source provenance is invalid for ${sourcePath}: ${proofIssue}` : null;
}
