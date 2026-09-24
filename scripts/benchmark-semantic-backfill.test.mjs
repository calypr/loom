import test from 'node:test';
import assert from 'node:assert/strict';
import { documentKey, parseArgs } from './benchmark-semantic-backfill.mjs';

test('benchmark parser keeps bounded source and opaque identities', () => {
  const options = parseArgs(['--source-project', 'source-project', '--source-generation', 'source-generation', '--rows', '50000']);
  assert.equal(options.rows, 50000);
  assert.equal(options.resourceType, 'Specimen');
  assert.throws(() => parseArgs(['--rows', '50001']), /rows/);
  assert.throws(() => parseArgs(['--source-project', 'bad project']), /opaque identifier/);
});

test('benchmark manifest keys match the dataset lifecycle key domain', () => {
  assert.equal(
    documentKey('manifest', 'benchmark-project', 'benchmark-generation'),
    'manifest_8aaa5e6dabbbfecddad0cbd33d561e811f0d953f2a636221f44424f463715e08',
  );
});
