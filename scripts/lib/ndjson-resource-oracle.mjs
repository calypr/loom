import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { createInterface } from 'node:readline';

const sha256 = value => createHash('sha256').update(value).digest('hex');

export async function readNDJSONResourceIdentityOracle({ path, project, generation, resourceType }) {
  for (const [name, value] of Object.entries({ path, project, generation, resourceType })) {
    if (typeof value !== 'string' || value.length === 0) throw new Error(`${name} is required for the independent resource oracle`);
  }
  const input = createReadStream(path);
  const contentHash = createHash('sha256');
  input.on('data', chunk => contentHash.update(chunk));
  const ids = [];
  for await (const line of createInterface({ input, crlfDelay: Infinity })) {
    if (!line.trim()) continue;
    const record = JSON.parse(line);
    if (record.resourceType !== resourceType || typeof record.id !== 'string' || !record.id) {
      throw new Error(`Raw source must contain ${resourceType} records with non-empty ids`);
    }
    ids.push(record.id);
  }
  if (ids.length === 0) throw new Error(`Independent raw ${resourceType} source must not be empty`);
  if (new Set(ids).size !== ids.length) throw new Error(`Raw ${resourceType} identities must be unique`);
  const storageKey = id => sha256(['vertex', project, generation, resourceType, id, ''].join('\0'));
  ids.sort((left, right) => storageKey(left).localeCompare(storageKey(right)));
  return {
    path,
    sha256: contentHash.digest('hex'),
    count: ids.length,
    ids,
    ordering: 'SHA-256 Arango vertex storage key for the exact project, generation, resource type, and source ID',
  };
}
