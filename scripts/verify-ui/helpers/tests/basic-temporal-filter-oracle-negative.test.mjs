import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { test } from 'node:test';
import { readBasicTemporalFilterOracle } from '../basic-temporal-filter-oracle.mjs';

const checkedFixtureDir = resolve('testdata/basic-temporal-filter');

const temporaryFixtureDir = (t) => {
  const fixtureDir = mkdtempSync(join(tmpdir(), 'basic-temporal-filter-oracle-negative-'));
  t.after(() => rmSync(fixtureDir, { recursive: true, force: true }));
  return fixtureDir;
};

const fixtureRecords = (resourceType) => readFileSync(join(checkedFixtureDir, `${resourceType}.ndjson`), 'utf8')
  .split(/\r?\n/)
  .filter(line => line.trim())
  .map(line => JSON.parse(line));

const writeFixtureRecords = (fixtureDir, resourceType, records) => {
  writeFileSync(join(fixtureDir, `${resourceType}.ndjson`), `${records.map(record => JSON.stringify(record)).join('\n')}\n`);
};

const writeChangedFixture = (t, resourceType, change) => {
  const fixtureDir = temporaryFixtureDir(t);
  const records = fixtureRecords(resourceType);
  change(records);
  writeFixtureRecords(fixtureDir, resourceType, records);
  return fixtureDir;
};

test('Basic DATE oracle rejects a wrong Patient birthDate value', (t) => {
  const fixtureDir = writeChangedFixture(t, 'Patient', records => {
    records[0].birthDate = '1980-01-03';
  });

  assert.throws(() => readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date' }), Error);
});

test('Basic DATE oracle rejects wrong or missing Patient identity', (t) => {
  for (const [description, change] of [
    ['wrong', records => { records[0].id = 'dev-patient-999'; }],
    ['missing', records => { delete records[0].id; }],
  ]) {
    const fixtureDir = writeChangedFixture(t, 'Patient', change);

    assert.throws(
      () => readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date' }),
      Error,
      `${description} Patient identity must fail the literal oracle`,
    );
  }
});

test('Basic DATE_TIME oracle rejects an offset spelling for the expected instant', (t) => {
  const fixtureDir = writeChangedFixture(t, 'Observation', records => {
    records[0].effectiveDateTime = '2026-01-01T16:00:00.000-08:00';
  });

  assert.throws(() => readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date-time' }), Error);
});

test('Basic DATE_TIME oracle rejects an effectiveDateTime at the wrong instant', (t) => {
  const fixtureDir = writeChangedFixture(t, 'Observation', records => {
    records[0].effectiveDateTime = '2026-01-02T01:00:00Z';
  });

  assert.throws(() => readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date-time' }), Error);
});

test('Basic temporal oracle rejects a missing required Patient row', (t) => {
  const fixtureDir = writeChangedFixture(t, 'Patient', records => {
    records.splice(1, 1);
  });

  assert.throws(() => readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date' }), Error);
});

test('Basic temporal oracle reports malformed required fixture JSON', (t) => {
  const fixtureDir = temporaryFixtureDir(t);
  writeFileSync(join(fixtureDir, 'Patient.ndjson'), '{"resourceType":"Patient","id":\n');

  assert.throws(
    () => readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date' }),
    Error,
  );
});

test('Basic DATE oracle reports a missing required Patient fixture file', (t) => {
  const fixtureDir = temporaryFixtureDir(t);

  assert.throws(
    () => readBasicTemporalFilterOracle({ fixtureDir, caseName: 'date' }),
    Error,
  );
});

test('Basic temporal oracle does not support the instant case', () => {
  assert.throws(
    () => readBasicTemporalFilterOracle({ fixtureDir: checkedFixtureDir, caseName: 'instant' }),
    Error,
  );
});
