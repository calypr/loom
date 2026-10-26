import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  buildBasicNestedCodingExpandOracle,
  classifyNestedCodingFixture,
} from '../basic-nested-coding-expand-oracle.mjs';

const repositoryRoot = join(dirname(fileURLToPath(import.meta.url)), '../../../..');
const fixturePath = join(repositoryRoot, 'testdata/basic-nested-coding-expand/Observation.ndjson');
const fixtureRows = () => readFileSync(fixturePath, 'utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));

test('Basic nested-coding fixture preserves exact owner, coding multiplicity, and distinct missing/null/empty shapes', () => {
  const resources = fixtureRows();
  assert.deepEqual(resources.map(({ code }) => code), Array.from({ length: 8 }, () => ({ text: 'Basic fixture Observation' })));
  const oracle = buildBasicNestedCodingExpandOracle(resources);
  assert.deepEqual(oracle.shapeCounts, { populated: 2, missing: 1, null: 1, empty: 1, 'not-applicable': 3 });
  assert.deepEqual(oracle.sourceRows.map(({ id, componentShape, ownerLabel, codingShape, codes }) =>
    [id, componentShape, ownerLabel, codingShape, codes]), [
    ['basic-nested-001', 'one-owner', 'owner-alpha', 'populated', ['ALPHA-1', 'ALPHA-2']],
    ['basic-nested-002', 'one-owner', 'owner-beta', 'populated', ['BETA-1']],
    ['basic-nested-003', 'one-owner', 'owner-missing-coding', 'missing', []],
    ['basic-nested-004', 'one-owner', 'owner-null-coding', 'null', []],
    ['basic-nested-005', 'one-owner', 'owner-empty-coding', 'empty', []],
    ['basic-nested-006', 'empty', null, 'not-applicable', []],
    ['basic-nested-007', 'null', null, 'not-applicable', []],
    ['basic-nested-008', 'missing', null, 'not-applicable', []],
  ]);
  assert.deepEqual(oracle.expectedRows.map(({ id, ownerLabel, ordinal, code }) => [id, ownerLabel, ordinal, code]), [
    ['basic-nested-001', 'owner-alpha', 0, 'ALPHA-1'],
    ['basic-nested-001', 'owner-alpha', 1, 'ALPHA-2'],
    ['basic-nested-002', 'owner-beta', 0, 'BETA-1'],
    ['basic-nested-003', 'owner-missing-coding', null, null],
    ['basic-nested-004', 'owner-null-coding', null, null],
    ['basic-nested-005', 'owner-empty-coding', null, null],
    ['basic-nested-006', null, null, null],
    ['basic-nested-007', null, null, null],
    ['basic-nested-008', null, null, null],
  ]);
});

test('fixture oracle rejects multiple components so nested item ownership stays independently assertable', () => {
  assert.throws(() => classifyNestedCodingFixture({
    resourceType: 'Observation',
    id: 'ambiguous-owner',
    code: { text: 'Basic fixture Observation' },
    component: [
      { valueString: 'owner-one', code: { coding: [{ code: 'A' }] } },
      { valueString: 'owner-two', code: { coding: [{ code: 'B' }] } },
    ],
  }), /one component owner per Observation/);
});
