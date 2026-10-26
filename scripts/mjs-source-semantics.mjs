import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

export const MJS_SOURCE_SEMANTICS_RELATIVE_PATH = 'verification/builder-interactions/mjs-source-semantics.json';
export const MJS_SOURCE_SEMANTICS_SHARD_DIRECTORY = 'mjs-source-semantics';

const MANIFEST_VERSION = 2;
const CATALOG_VERSION = 1;
const MAX_SHARD_BYTES = 2_000_000;
const HASH_PREFIX_START_LENGTH = 1;

const isRecord = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const compare = (left, right) => (left < right ? -1 : left > right ? 1 : 0);

function fail(message) {
  throw new Error(`mjs source semantics: ${message}`);
}

function assertExactKeys(value, expectedKeys, label) {
  if (!isRecord(value)) fail(`${label} must be an object`);
  const actual = Object.keys(value).sort();
  const expected = [...expectedKeys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    fail(`${label} must contain exactly ${expectedKeys.join(', ')}`);
  }
}

function validateJsonValue(value, label, active = new Set()) {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return;
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) fail(`${label} contains a non-finite number`);
    if (Object.is(value, -0)) fail(`${label} contains negative zero, which JSON cannot round-trip`);
    return;
  }
  if (typeof value !== 'object') fail(`${label} contains a value JSON cannot preserve`);
  if (active.has(value)) fail(`${label} contains a cycle`);
  if (!Array.isArray(value)) {
    const prototype = Object.getPrototypeOf(value);
    if (prototype !== Object.prototype && prototype !== null) fail(`${label} contains a non-JSON object`);
  }
  active.add(value);
  if (Array.isArray(value)) {
    for (let index = 0; index < value.length; index++) {
      const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
      if (!descriptor?.enumerable || !Object.hasOwn(descriptor, 'value')) fail(`${label} contains an array hole or accessor`);
      validateJsonValue(descriptor.value, `${label}[${index}]`, active);
    }
    const keys = Reflect.ownKeys(value).filter((key) => key !== 'length');
    if (keys.length !== value.length || keys.some((key) => typeof key !== 'string')) {
      fail(`${label} contains non-index array properties`);
    }
  } else {
    for (const key of Reflect.ownKeys(value)) {
      if (typeof key !== 'string') fail(`${label} contains a symbol property`);
      const descriptor = Object.getOwnPropertyDescriptor(value, key);
      if (!descriptor?.enumerable || !Object.hasOwn(descriptor, 'value')) {
        fail(`${label}.${key} is not a plain enumerable value`);
      }
      validateJsonValue(descriptor.value, `${label}.${key}`, active);
    }
  }
  active.delete(value);
}

function compactJson(value, label) {
  validateJsonValue(value, label);
  const text = JSON.stringify(value);
  if (typeof text !== 'string') fail(`${label} could not be serialized`);
  return text;
}

function checkedCatalogPath(value, label) {
  if (typeof value !== 'string' || value.length === 0 || value.includes('\\')
    || path.posix.isAbsolute(value) || value.split('/').some((part) => part === '..' || part === '.')) {
    fail(`${label} must be a repository-relative path without traversal`);
  }
  if (path.posix.normalize(value) !== value) fail(`${label} must be normalized`);
  return value;
}

function checkedDirectory(value, label) {
  if (typeof value !== 'string' || value.length === 0 || value.includes('\\')
    || path.posix.isAbsolute(value) || value.split('/').some((part) => part === '..')) {
    fail(`${label} must be a relative source directory without traversal`);
  }
  if (value !== '.' && (value.split('/').some((part) => !part) || path.posix.normalize(value) !== value)) {
    fail(`${label} must be normalized`);
  }
  return value;
}

function checkedOrderReferences(order, label, groupField, directoryMode) {
  if (!Array.isArray(order)) fail(`${label} must be an array`);
  return order.map((reference, index) => {
    const where = `${label}[${index}]`;
    if (typeof reference !== 'string') fail(`${where} must be a group/bucket reference`);
    const separator = reference.lastIndexOf('#');
    if (separator <= 0) fail(`${where} must include an encoded group and bucket prefix`);
    const encodedGroup = reference.slice(0, separator);
    let groupKey;
    try {
      groupKey = decodeURIComponent(encodedGroup);
    } catch (error) {
      fail(`${where} has an invalid encoded group: ${error.message}`);
    }
    if (!groupKey || encodeURIComponent(groupKey) !== encodedGroup) fail(`${where} has a noncanonical group encoding`);
    if (directoryMode) groupKey = checkedDirectory(groupKey, `${where} directory`);
    const prefixToken = reference.slice(separator + 1);
    const bucketPrefix = prefixToken === 'all' ? '' : prefixToken;
    if (prefixToken !== 'all' && !/^[a-f0-9]{1,64}$/.test(prefixToken)) {
      fail(`${where} must use an all marker or lowercase SHA-256 prefix`);
    }
    return { [groupField]: groupKey, bucketPrefix };
  });
}

function groupRecords(records, keyFor) {
  const groups = new Map();
  for (const record of records) {
    const key = keyFor(record);
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(record);
  }
  return groups;
}

function recordLineBytes(record, label) {
  return Buffer.byteLength(`${compactJson(record, label)}\n`, 'utf8');
}

function partitionRecords(records, nameFor) {
  const hashes = new Map(records.map((record) => [record, sha256(nameFor(record))]));
  const buckets = [];
  const addOrSplit = (prefix, members) => {
    const bytes = members.reduce((size, record) => size + recordLineBytes(record, 'record'), 0);
    if (bytes < MAX_SHARD_BYTES) {
      buckets.push({ prefix, records: members });
      return;
    }
    if (members.some((record) => recordLineBytes(record, 'record') >= MAX_SHARD_BYTES)) {
      fail('a single complete record is at least 2 MB');
    }
    if (prefix.length >= 64) fail('hash-prefix bucket cannot be split below 2 MB');
    const nextLength = Math.max(HASH_PREFIX_START_LENGTH, prefix.length + 1);
    const children = new Map();
    for (const record of members) {
      const childPrefix = hashes.get(record).slice(0, nextLength);
      if (!children.has(childPrefix)) children.set(childPrefix, []);
      children.get(childPrefix).push(record);
    }
    if (children.size < 2) fail('hash-prefix bucket did not split');
    for (const [childPrefix, childRecords] of [...children].sort(([left], [right]) => compare(left, right))) {
      addOrSplit(childPrefix, childRecords);
    }
  };

  const totalBytes = records.reduce((size, record) => size + recordLineBytes(record, 'record'), 0);
  if (totalBytes < MAX_SHARD_BYTES) return [{ prefix: '', records }];
  const firstLevel = new Map();
  for (const record of records) {
    const prefix = hashes.get(record).slice(0, HASH_PREFIX_START_LENGTH);
    if (!firstLevel.has(prefix)) firstLevel.set(prefix, []);
    firstLevel.get(prefix).push(record);
  }
  for (const [prefix, members] of [...firstLevel].sort(([left], [right]) => compare(left, right))) {
    addOrSplit(prefix, members);
  }
  return buckets;
}

function safeName(value) {
  const readable = value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48);
  return `${readable || 'record'}-${sha256(value).slice(0, 10)}`;
}

function shardPath(collection, groupKey, bucketPrefix) {
  return `${MJS_SOURCE_SEMANTICS_SHARD_DIRECTORY}/${collection}-${safeName(groupKey)}-${bucketPrefix || 'all'}.jsonl`;
}

function bucketIdentity(collection, groupKey, bucketPrefix) {
  return JSON.stringify([collection, groupKey, bucketPrefix]);
}

function orderReference(groupKey, bucketPrefix) {
  return `${encodeURIComponent(groupKey)}#${bucketPrefix || 'all'}`;
}

function bucketPrefixFor(bucketPrefixes, name, label) {
  if (!bucketPrefixes.has(name)) fail(`${label} has no bucket assignment`);
  return bucketPrefixes.get(name);
}

function serializeGroup({ collection, groupKey, records, nameFor, shards }) {
  const buckets = partitionRecords(records, nameFor);
  const bucketPrefixByName = new Map();
  const shardReferences = buckets.map(({ prefix, records: bucketRecords }) => {
    const lines = bucketRecords.map((record) => compactJson(record, 'record'));
    const text = `${lines.join('\n')}\n`;
    const bytes = Buffer.byteLength(text, 'utf8');
    if (bytes >= MAX_SHARD_BYTES) fail(`generated shard for ${groupKey} is not under 2 MB`);
    const relativePath = shardPath(collection, groupKey, prefix);
    if (shards.has(relativePath)) fail(`generated duplicate shard path ${relativePath}`);
    shards.set(relativePath, text);
    for (const record of bucketRecords) bucketPrefixByName.set(nameFor(record), prefix);
    return {
      path: relativePath,
      count: lines.length,
      sha256: sha256(Buffer.from(text, 'utf8')),
      bucketPrefix: prefix,
    };
  });
  return { shardReferences, bucketPrefixByName };
}

function validateCatalogForSerialization(catalog) {
  if (!isRecord(catalog) || catalog.schemaVersion !== CATALOG_VERSION) {
    fail(`catalog schemaVersion must be ${CATALOG_VERSION}`);
  }
  validateJsonValue(catalog, 'catalog');
  assertExactKeys(catalog, ['schemaVersion', 'sourceCommit', 'inventorySha256', 'claims', 'files'], 'catalog');
  if (!/^[a-f0-9]{40}$/.test(catalog.sourceCommit ?? '')) fail('catalog sourceCommit must be a full commit hash');
  if (!/^[a-f0-9]{64}$/.test(catalog.inventorySha256 ?? '')) fail('catalog inventorySha256 must be a SHA-256 digest');
  if (!Array.isArray(catalog.claims) || !Array.isArray(catalog.files)) fail('catalog claims and files must be arrays');

  const claimNames = new Set();
  for (const [index, claim] of catalog.claims.entries()) {
    if (!isRecord(claim) || typeof claim.id !== 'string' || !claim.id
      || typeof claim.category !== 'string' || !claim.category) {
      fail(`claims[${index}] must be a named claim with a category`);
    }
    if (claimNames.has(claim.id)) fail(`claims contains duplicate id ${claim.id}`);
    claimNames.add(claim.id);
    compactJson(claim, `claims[${index}]`);
  }

  const fileNames = new Set();
  for (const [index, file] of catalog.files.entries()) {
    if (!isRecord(file) || typeof file.path !== 'string' || !file.path) {
      fail(`files[${index}] must be a named file record`);
    }
    checkedCatalogPath(file.path, `files[${index}].path`);
    if (fileNames.has(file.path)) fail(`files contains duplicate path ${file.path}`);
    fileNames.add(file.path);
    compactJson(file, `files[${index}]`);
  }
}

export function serializeMjsSourceSemantics(catalog) {
  validateCatalogForSerialization(catalog);
  const shards = new Map();
  const claimsByCategory = groupRecords(catalog.claims, (claim) => claim.category);
  const filesByDirectory = groupRecords(catalog.files, (file) => path.posix.dirname(file.path));
  const claimGroups = [];
  const fileGroups = [];
  const claimBucketPrefixes = new Map();
  const fileBucketPrefixes = new Map();

  for (const [category, records] of claimsByCategory) {
    const categoryEncoding = serializeGroup({
      collection: 'claims',
      groupKey: category,
      records,
      nameFor: (record) => record.id,
      shards,
    });
    claimGroups.push({ category, shards: categoryEncoding.shardReferences });
    for (const [name, prefix] of categoryEncoding.bucketPrefixByName) claimBucketPrefixes.set(name, prefix);
  }

  for (const [directory, records] of filesByDirectory) {
    const checked = checkedDirectory(directory, `file source directory ${directory}`);
    const directoryEncoding = serializeGroup({
      collection: 'files',
      groupKey: checked,
      records,
      nameFor: (record) => record.path,
      shards,
    });
    fileGroups.push({ directory: checked, shards: directoryEncoding.shardReferences });
    for (const [name, prefix] of directoryEncoding.bucketPrefixByName) fileBucketPrefixes.set(name, prefix);
  }

  const manifest = {
    schemaVersion: MANIFEST_VERSION,
    sourceCommit: catalog.sourceCommit,
    inventorySha256: catalog.inventorySha256,
    claims: {
      order: catalog.claims.map((claim) => orderReference(
        claim.category,
        bucketPrefixFor(claimBucketPrefixes, claim.id, `claim ${claim.id}`),
      )),
      groups: claimGroups,
    },
    files: {
      order: catalog.files.map((file) => orderReference(
        path.posix.dirname(file.path),
        bucketPrefixFor(fileBucketPrefixes, file.path, `file ${file.path}`),
      )),
      groups: fileGroups,
    },
  };
  const manifestText = `${JSON.stringify(manifest, null, 2)}\n`;
  return { manifestText, shards };
}

function checkedManifestPath(manifestPath) {
  const value = manifestPath ?? path.resolve(REPOSITORY_ROOT, MJS_SOURCE_SEMANTICS_RELATIVE_PATH);
  if (typeof value !== 'string' || !path.isAbsolute(value)) fail('manifestPath must be an absolute path');
  return path.resolve(value);
}

function readManifest(manifestPath) {
  let parsed;
  try {
    parsed = JSON.parse(readFileSync(manifestPath, 'utf8'));
  } catch (error) {
    fail(`cannot read v2 manifest ${manifestPath}: ${error.message}`);
  }
  assertExactKeys(parsed, ['schemaVersion', 'sourceCommit', 'inventorySha256', 'claims', 'files'], 'manifest');
  if (parsed.schemaVersion !== MANIFEST_VERSION) fail(`manifest schemaVersion must be ${MANIFEST_VERSION}`);
  if (!/^[a-f0-9]{40}$/.test(parsed.sourceCommit ?? '')) fail('manifest sourceCommit must be a full commit hash');
  if (!/^[a-f0-9]{64}$/.test(parsed.inventorySha256 ?? '')) fail('manifest inventorySha256 must be a SHA-256 digest');
  return parsed;
}

function validateManifestCollection(collection, definition, recordName, directoryMode) {
  assertExactKeys(definition, ['order', 'groups'], `${collection} manifest`);
  const groupField = directoryMode ? 'directory' : 'category';
  const order = checkedOrderReferences(definition.order, `${collection}.order`, groupField, directoryMode);
  if (!Array.isArray(definition.groups)) fail(`${collection}.groups must be an array`);
  const references = [];
  const groupKeys = new Set();
  for (const [index, group] of definition.groups.entries()) {
    const label = `${collection}.groups[${index}]`;
    assertExactKeys(group, [groupField, 'shards'], label);
    const key = directoryMode
      ? checkedDirectory(group[groupField], `${label}.${groupField}`)
      : group[groupField];
    if (typeof key !== 'string' || key.length === 0) fail(`${label}.${groupField} must be a non-empty string`);
    if (groupKeys.has(key)) fail(`${collection}.groups contains duplicate group ${key}`);
    groupKeys.add(key);
    if (!Array.isArray(group.shards) || group.shards.length === 0) fail(`${label}.shards must be non-empty`);
    let priorPrefix = null;
    for (const [shardIndex, shard] of group.shards.entries()) {
      const shardLabel = `${label}.shards[${shardIndex}]`;
      assertExactKeys(shard, ['path', 'count', 'sha256', 'bucketPrefix'], shardLabel);
      const shardPathValue = shard.path;
      if (typeof shardPathValue !== 'string' || shardPathValue.includes('\\')
        || path.posix.isAbsolute(shardPathValue) || path.posix.normalize(shardPathValue) !== shardPathValue
        || shardPathValue.split('/').some((part) => part === '..' || part === '.')) {
        fail(`${shardLabel}.path must be normalized and repository-relative`);
      }
      const expectedPrefix = `${MJS_SOURCE_SEMANTICS_SHARD_DIRECTORY}/${collection}-`;
      if (!shardPathValue.startsWith(expectedPrefix) || !shardPathValue.endsWith('.jsonl')) {
        fail(`${shardLabel}.path must reference an adjacent ${collection} JSONL shard`);
      }
      if (typeof shard.bucketPrefix !== 'string' || !/^[a-f0-9]{0,64}$/.test(shard.bucketPrefix)) {
        fail(`${shardLabel}.bucketPrefix must be a lowercase SHA-256 prefix`);
      }
      if (shardPathValue !== shardPath(collection, key, shard.bucketPrefix)) {
        fail(`${shardLabel}.path does not match its group and hash prefix`);
      }
      if (!Number.isSafeInteger(shard.count) || shard.count <= 0) fail(`${shardLabel}.count must be a positive integer`);
      if (!/^[a-f0-9]{64}$/.test(shard.sha256 ?? '')) fail(`${shardLabel}.sha256 must be a SHA-256 digest`);
      if (priorPrefix !== null && compare(priorPrefix, shard.bucketPrefix) >= 0) {
        fail(`${label}.shards hash prefixes must be sorted`);
      }
      if (group.shards.some((other, otherIndex) => otherIndex !== shardIndex
        && typeof other.bucketPrefix === 'string'
        && (other.bucketPrefix.startsWith(shard.bucketPrefix) || shard.bucketPrefix.startsWith(other.bucketPrefix)))) {
        fail(`${label}.shards hash prefixes must be unique and non-overlapping`);
      }
      priorPrefix = shard.bucketPrefix;
      references.push({
        collection,
        groupKey: key,
        bucketPrefix: shard.bucketPrefix,
        path: shardPathValue,
        shard,
        recordName,
      });
    }
    if (group.shards.length === 1 && group.shards[0].bucketPrefix !== '') {
      fail(`${label} must use an empty prefix when it fits in one shard`);
    }
    if (group.shards.length > 1 && group.shards.some((shard) => shard.bucketPrefix === '')) {
      fail(`${label} cannot mix an unbucketed shard with hash-prefix buckets`);
    }
  }
  const availableBuckets = new Set(references.map((reference) =>
    bucketIdentity(collection, reference.groupKey, reference.bucketPrefix)));
  for (const [index, reference] of order.entries()) {
    const identity = bucketIdentity(collection, reference[groupField], reference.bucketPrefix);
    if (!availableBuckets.has(identity)) fail(`${collection}.order[${index}] references an unknown group/bucket`);
  }
  const expectedCount = references.reduce((count, reference) => count + reference.shard.count, 0);
  if (order.length !== expectedCount) fail(`${collection}.order count differs from referenced shard counts`);
  return { order, references };
}

function readShardRecords(manifestPath, references, allNames) {
  const manifestDirectory = path.dirname(manifestPath);
  const shardDirectory = path.resolve(manifestDirectory, MJS_SOURCE_SEMANTICS_SHARD_DIRECTORY);
  let directoryEntries;
  try {
    directoryEntries = readdirSync(shardDirectory, { withFileTypes: true });
  } catch (error) {
    fail(`cannot read adjacent shard directory ${shardDirectory}: ${error.message}`);
  }
  const expectedNames = new Set(references.map(({ path: relativePath }) => path.posix.basename(relativePath)));
  if (expectedNames.size !== references.length) fail('manifest references a shard path more than once');
  const actualNames = new Set(directoryEntries.map((entry) => entry.name));
  const unexpected = [...actualNames].filter((name) => !expectedNames.has(name));
  const missing = [...expectedNames].filter((name) => !actualNames.has(name));
  if (unexpected.length > 0) fail(`unreferenced shard entries exist: ${unexpected.slice(0, 4).join(', ')}`);
  if (missing.length > 0) fail(`referenced shards are missing: ${missing.slice(0, 4).join(', ')}`);

  const recordsByBucket = new Map();
  for (const reference of references) {
    const absolutePath = path.resolve(manifestDirectory, reference.path);
    const relativeToShardDirectory = path.relative(shardDirectory, absolutePath);
    if (relativeToShardDirectory.startsWith('..') || path.isAbsolute(relativeToShardDirectory)
      || relativeToShardDirectory.includes(path.sep)) {
      fail(`shard path escapes the adjacent directory: ${reference.path}`);
    }
    let stat;
    let bytes;
    try {
      stat = lstatSync(absolutePath);
      if (!stat.isFile() || stat.isSymbolicLink()) fail(`shard is not a regular file: ${reference.path}`);
      bytes = readFileSync(absolutePath);
    } catch (error) {
      if (error.message.startsWith('mjs source semantics:')) throw error;
      fail(`cannot read shard ${reference.path}: ${error.message}`);
    }
    if (bytes.length >= MAX_SHARD_BYTES) fail(`shard is not under 2 MB: ${reference.path}`);
    if (sha256(bytes) !== reference.shard.sha256) fail(`shard hash mismatch: ${reference.path}`);
    if (bytes.length === 0 || bytes.at(-1) !== 0x0a) fail(`shard must end with a newline: ${reference.path}`);
    const text = bytes.toString('utf8');
    if (text.includes('\r')) fail(`shard must use LF line endings: ${reference.path}`);
    const lines = text.slice(0, -1).split('\n');
    if (lines.length !== reference.shard.count) fail(`shard count mismatch: ${reference.path}`);
    const records = [];
    for (const [lineIndex, line] of lines.entries()) {
      if (!line) fail(`empty JSONL record at ${reference.path}:${lineIndex + 1}`);
      let record;
      try {
        record = JSON.parse(line);
      } catch (error) {
        fail(`malformed JSONL record at ${reference.path}:${lineIndex + 1}: ${error.message}`);
      }
      if (!isRecord(record)) fail(`JSONL record at ${reference.path}:${lineIndex + 1} must be an object`);
      if (JSON.stringify(record) !== line) fail(`JSONL record is not canonical at ${reference.path}:${lineIndex + 1}`);
      const name = record[reference.recordName];
      if (typeof name !== 'string' || name.length === 0) {
        fail(`JSONL record at ${reference.path}:${lineIndex + 1} has no ${reference.recordName}`);
      }
      if (allNames.has(name)) fail(`duplicate ${reference.collection} record name ${name}`);
      allNames.add(name);
      if (!sha256(name).startsWith(reference.shard.bucketPrefix)) {
        fail(`record ${name} does not match the hash prefix for ${reference.path}`);
      }
      if (reference.collection === 'claims' && record.category !== reference.groupKey) {
        fail(`claim ${name} is stored under the wrong category shard`);
      }
      if (reference.collection === 'files' && path.posix.dirname(name) !== reference.groupKey) {
        fail(`file ${name} is stored under the wrong source directory shard`);
      }
      records.push(record);
    }
    const identity = bucketIdentity(reference.collection, reference.groupKey, reference.bucketPrefix);
    if (recordsByBucket.has(identity)) fail(`duplicate group/bucket reference in ${reference.collection}`);
    recordsByBucket.set(identity, records);
  }
  return recordsByBucket;
}

function reconstructRecords(collection, order, groupField, references, recordsByBucket) {
  const referenceByBucket = new Map(references.map((reference) => [
    bucketIdentity(collection, reference.groupKey, reference.bucketPrefix), reference,
  ]));
  const offsets = new Map();
  const records = order.map((entry, index) => {
    const identity = bucketIdentity(collection, entry[groupField], entry.bucketPrefix);
    const bucketRecords = recordsByBucket.get(identity);
    if (!bucketRecords) fail(`${collection}.order[${index}] references a missing shard`);
    const offset = offsets.get(identity) ?? 0;
    if (offset >= bucketRecords.length) fail(`${collection}.order consumes too many records from one shard`);
    offsets.set(identity, offset + 1);
    return bucketRecords[offset];
  });
  for (const [identity, reference] of referenceByBucket) {
    const used = offsets.get(identity) ?? 0;
    if (used !== reference.shard.count) fail(`${collection}.order does not consume every record from ${reference.path}`);
  }
  return records;
}

export function loadMjsSourceSemantics(manifestPath) {
  const absoluteManifestPath = checkedManifestPath(manifestPath);
  const manifest = readManifest(absoluteManifestPath);
  const claims = validateManifestCollection('claims', manifest.claims, 'id', false);
  const files = validateManifestCollection('files', manifest.files, 'path', true);
  const references = [...claims.references, ...files.references];
  const referencedPaths = new Set(references.map((reference) => reference.path));
  if (referencedPaths.size !== references.length) fail('manifest reuses a shard path');

  const names = new Set();
  const recordsByBucket = readShardRecords(absoluteManifestPath, references, names);
  const catalog = {
    schemaVersion: CATALOG_VERSION,
    sourceCommit: manifest.sourceCommit,
    inventorySha256: manifest.inventorySha256,
    claims: reconstructRecords('claims', claims.order, 'category', claims.references, recordsByBucket),
    files: reconstructRecords('files', files.order, 'directory', files.references, recordsByBucket),
  };
  const canonical = serializeMjsSourceSemantics(catalog);
  const actualManifestText = readFileSync(absoluteManifestPath, 'utf8');
  if (actualManifestText !== canonical.manifestText) fail('manifest is not the deterministic serialization of its records');
  const actualPaths = new Set(references.map((reference) => reference.path));
  if (actualPaths.size !== canonical.shards.size
    || [...canonical.shards.keys()].some((relativePath) => !actualPaths.has(relativePath))) {
    fail('shard references are not the deterministic serialization of their groups');
  }
  const manifestDirectory = path.dirname(absoluteManifestPath);
  for (const [relativePath, expectedText] of canonical.shards) {
    if (readFileSync(path.resolve(manifestDirectory, relativePath), 'utf8') !== expectedText) {
      fail(`shard is not the deterministic serialization of its records: ${relativePath}`);
    }
  }
  return catalog;
}
