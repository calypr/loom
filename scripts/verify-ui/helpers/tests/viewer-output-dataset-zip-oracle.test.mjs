import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import {
  inspectBasicPatientDatasetZip,
  inspectDatasetArtifactZip,
  readBasicPatientDatasetZipOracle,
} from '../viewer-output-dataset-zip-oracle.mjs';

const fixtureDir = fileURLToPath(new URL('../../../../testdata/devloop-fixture/', import.meta.url));
const expectedIdentity = {
  project: 'basic-fixture-project',
  datasetGeneration: 'basic-fixture-generation',
  outputId: 'patients',
  revisionId: 'revision-basic-patients',
  schemaDigest: 'a'.repeat(64),
};
const sha256 = value => createHash('sha256').update(value).digest('hex');
const expectedMembers = format => [
  format === 'CSV' ? 'data.csv' : 'data.jsonl',
  'schema.json',
  'provenance.json',
  'quality.json',
  'README.md',
  'manifest.json',
].sort();

const crc32 = bytes => {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) {
      crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
};

const storedZip = members => {
  const localParts = [];
  const centralParts = [];
  let localOffset = 0;
  for (const [name, rawValue] of members) {
    const filename = Buffer.from(name);
    const data = Buffer.isBuffer(rawValue) ? rawValue : Buffer.from(rawValue);
    const checksum = crc32(data);
    const local = Buffer.alloc(30 + filename.length);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0, 6);
    local.writeUInt16LE(0, 8);
    local.writeUInt32LE(checksum, 14);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(filename.length, 26);
    local.writeUInt16LE(0, 28);
    filename.copy(local, 30);
    localParts.push(local, data);

    const central = Buffer.alloc(46 + filename.length);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0, 8);
    central.writeUInt16LE(0, 10);
    central.writeUInt32LE(checksum, 16);
    central.writeUInt32LE(data.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(filename.length, 28);
    central.writeUInt16LE(0, 30);
    central.writeUInt16LE(0, 32);
    central.writeUInt16LE(0, 34);
    central.writeUInt16LE(0, 36);
    central.writeUInt32LE(0, 38);
    central.writeUInt32LE(localOffset, 42);
    filename.copy(central, 46);
    centralParts.push(central);
    localOffset += local.length + data.length;
  }

  const centralDirectory = Buffer.concat(centralParts);
  const localDirectory = Buffer.concat(localParts);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(members.length, 8);
  end.writeUInt16LE(members.length, 10);
  end.writeUInt32LE(centralDirectory.length, 12);
  end.writeUInt32LE(localDirectory.length, 16);
  end.writeUInt16LE(0, 20);
  return Buffer.concat([localDirectory, centralDirectory, end]);
};

const artifactPackage = (format, rows, overrides = {}) => {
  const identity = {
    project: expectedIdentity.project,
    datasetGeneration: expectedIdentity.datasetGeneration,
    receiptId: 'receipt-basic-patients',
    executionId: 'execution-basic-patients',
    outputId: expectedIdentity.outputId,
    revisionId: expectedIdentity.revisionId,
    schemaDigest: expectedIdentity.schemaDigest,
    outputContractDigest: 'b'.repeat(64),
  };
  const column = {
    name: 'patient_id',
    outputKey: 'patient_id',
    label: 'Patient ID',
    logicalType: 'string',
    shape: 'scalar',
    nullable: true,
    repeated: false,
    emissionId: 'patient_id',
    candidateId: 'candidate-patient-id',
    occurrenceId: 'base',
    construction: 'VALUE',
    reductionPolicy: 'VALUE',
    sourceResourceType: 'Patient',
    sourcePath: 'id',
    ...overrides.column,
  };
  const descriptor = {
    version: 1,
    outputKey: 'patients',
    receiptFormatVersion: 2,
    compilerContractVersion: 'compiler-test-v1',
    recipeSchemaVersion: 1,
    translationVersion: 'translation-test-v1',
    sourceGeneration: expectedIdentity.datasetGeneration,
    publishedSchemaDigest: expectedIdentity.schemaDigest,
    resolvedSchemaDigest: 'c'.repeat(64),
    outputContractDigest: identity.outputContractDigest,
    rowGrain: 'patient',
    rowMultiplication: 'none',
    rowIdentity: {
      key: '__loom_row_id',
      sourceResourceType: 'Patient',
      sourceIdColumn: 'patient_id',
    },
    columns: [column],
  };
  const nullEncoding = String.raw`\N`;
  const arrayEncoding = format === 'JSONL' ? 'native' : 'json';
  const schema = { format, columns: [column], nullEncoding, arrayEncoding };
  const provenance = {
    project: identity.project,
    explorerId: 'default',
    revisionId: identity.revisionId,
    receiptId: identity.receiptId,
    executionId: identity.executionId,
    outputId: identity.outputId,
    datasetGeneration: identity.datasetGeneration,
    schemaDigest: identity.schemaDigest,
    authorizationScopeDigest: 'd'.repeat(64),
    ...overrides.provenance,
  };
  const names = {
    CSV: 'data.csv',
    JSONL: 'data.jsonl',
  };
  const dataName = names[format];
  const data = format === 'CSV'
    ? `patient_id\n${rows.map(row => row.patient_id).join('\n')}\n`
    : `${rows.map((row, index) => JSON.stringify({ rowId: `patient-row-${index + 1}`, values: { patient_id: row.patient_id } })).join('\n')}\n`;
  const memberValues = new Map([
    [dataName, Buffer.from(overrides.data ?? data)],
    ['schema.json', Buffer.from(JSON.stringify(schema))],
    ['provenance.json', Buffer.from(JSON.stringify(provenance))],
    ['quality.json', Buffer.from('[]')],
    ['README.md', Buffer.from('# Test artifact\n')],
  ]);
  const manifest = {
    version: 2,
    identity,
    descriptor,
    format,
    selection: { recipe: {}, datasetGeneration: expectedIdentity.datasetGeneration },
    interpretations: [],
    rows: rows.length,
    features: 1,
    nullEncoding,
    arrayEncoding,
    members: [...memberValues].map(([name, bytes]) => ({ name, sha256: sha256(bytes), bytes: bytes.length })),
  };
  if (overrides.manifest) Object.assign(manifest, overrides.manifest);
  const archiveMembers = [...memberValues];
  if (overrides.rehashMembers !== false) {
    const sourceMembers = new Map(archiveMembers);
    manifest.members = [...sourceMembers].map(([name, bytes]) => ({ name, sha256: sha256(bytes), bytes: bytes.length }));
    if (overrides.manifest) Object.assign(manifest, overrides.manifest);
  }
  archiveMembers.push(['manifest.json', Buffer.from(JSON.stringify(manifest))]);
  return { archive: storedZip(archiveMembers), manifest, archiveMembers };
};

const withArchive = async (archiveBytes, run) => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-dataset-zip-oracle-'));
  try {
    const archivePath = join(directory, 'dataset.zip');
    await writeFile(archivePath, archiveBytes);
    return await run(archivePath);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
};

test('Basic Patient ZIP oracle preserves the literal two rows and project/generation binding', async () => {
  const project = 'basic-fixture-project';
  const generation = 'basic-fixture-generation';
  const oracle = await readBasicPatientDatasetZipOracle(fixtureDir, { project, generation });
  const fixtureBytes = await readFile(join(fixtureDir, 'Patient.ndjson'));
  assert.equal(oracle.patientPath, resolve(fixtureDir, 'Patient.ndjson'));
  assert.equal(oracle.patientSHA256, sha256(fixtureBytes));
  assert.equal(oracle.project, project);
  assert.equal(oracle.generation, generation);
  assert.equal(oracle.outputId, 'patients');
  assert.equal(oracle.rowGrain, 'patient');
  assert.deepEqual([...oracle.patientIDs].sort(), ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual([...oracle.rows].sort((left, right) => left.patient_id.localeCompare(right.patient_id)), [
    { patient_id: 'dev-patient-001' },
    { patient_id: 'dev-patient-002' },
  ]);
});

for (const format of ['CSV', 'JSONL']) {
  test(`${format} archive oracle reads exact Basic Patient rows and validates manifest bindings`, async () => {
    const oracle = await readBasicPatientDatasetZipOracle(fixtureDir, {
      project: expectedIdentity.project,
      generation: expectedIdentity.datasetGeneration,
    });
    const { archive, manifest } = artifactPackage(format, oracle.rows);
    await withArchive(archive, archivePath => {
      const result = inspectBasicPatientDatasetZip(archivePath, {
        expectedIdentity,
        expectedRows: oracle.rows,
        expectedArchiveSHA256: sha256(archive),
      });
      assert.deepEqual(result.memberNames, expectedMembers(format));
      assert.equal(result.archiveSHA256, sha256(archive));
      assert.equal(result.archiveBytes, archive.length);
      assert.deepEqual(result.manifestBytes, Buffer.from(JSON.stringify(manifest)));
      assert.equal(result.manifestSHA256, sha256(result.manifestBytes));
      assert.equal(result.artifact.manifest.identity.revisionId, expectedIdentity.revisionId);
      assert.equal(result.artifact.provenance.project, expectedIdentity.project);
      assert.equal(result.artifact.provenance.datasetGeneration, expectedIdentity.datasetGeneration);
      assert.deepEqual(result.artifact.rows.map(row => row.values).sort((left, right) => left.patient_id.localeCompare(right.patient_id)), [
        { patient_id: 'dev-patient-001' },
        { patient_id: 'dev-patient-002' },
      ]);
      if (format === 'CSV') {
        assert.equal(result.members.get('data.csv').toString('utf8'),
          `patient_id\n${oracle.rows.map(row => row.patient_id).join('\n')}\n`);
      } else {
        const encodedRows = result.members.get('data.jsonl').toString('utf8').trimEnd().split('\n').map(line => JSON.parse(line));
        assert.deepEqual(encodedRows.map(row => row.values), oracle.rows);
        assert(encodedRows.every(row => typeof row.rowId === 'string' && row.rowId.length > 0));
        assert.equal(new Set(encodedRows.map(row => row.rowId)).size, oracle.rows.length);
      }
    });
  });
}

test('generic dataset ZIP inspector binds archive hash, publication identity, provenance, schema, and rows', async () => {
  const oracle = await readBasicPatientDatasetZipOracle(fixtureDir, {
    project: expectedIdentity.project,
    generation: expectedIdentity.datasetGeneration,
  });
  const { archive } = artifactPackage('JSONL', oracle.rows);
  await withArchive(archive, archivePath => {
    const result = inspectDatasetArtifactZip(archivePath, {
      expectedIdentity,
      expectedExplorerId: 'default',
      expectedRows: oracle.rows,
      expectedArchiveSHA256: sha256(archive),
    });
    assert.deepEqual(result.memberNames, expectedMembers('JSONL'));
    assert.equal(result.archiveSHA256, sha256(archive));
    assert.equal(result.artifact.manifest.identity.revisionId, expectedIdentity.revisionId);
    assert.equal(result.artifact.provenance.explorerId, 'default');
    assert.deepEqual(result.artifact.rows.map(row => row.values), oracle.rows);
  });
});

test('archive oracle rejects a descriptor hash that does not bind the downloaded ZIP bytes', async () => {
  const oracle = await readBasicPatientDatasetZipOracle(fixtureDir, {
    project: expectedIdentity.project,
    generation: expectedIdentity.datasetGeneration,
  });
  const { archive } = artifactPackage('CSV', oracle.rows);
  await withArchive(archive, archivePath => assert.throws(() => inspectBasicPatientDatasetZip(archivePath, {
    expectedIdentity,
    expectedRows: oracle.rows,
    expectedArchiveSHA256: '0'.repeat(64),
  }), /COMPLETE preparation descriptor archiveSha256/));
});

test('archive oracle accepts the exact sparse scalar schema with omitted repeated and rejects repeated true', async () => {
  const oracle = await readBasicPatientDatasetZipOracle(fixtureDir, {
    project: expectedIdentity.project,
    generation: expectedIdentity.datasetGeneration,
  });
  const sparsePackage = artifactPackage('CSV', oracle.rows, { column: { repeated: undefined } });
  await withArchive(sparsePackage.archive, archivePath => {
    const result = inspectBasicPatientDatasetZip(archivePath, {
      expectedIdentity,
      expectedRows: oracle.rows,
      expectedArchiveSHA256: sha256(sparsePackage.archive),
    });
    const schemaColumn = JSON.parse(result.members.get('schema.json').toString('utf8')).columns[0];
    assert.deepEqual(schemaColumn, {
      name: 'patient_id', outputKey: 'patient_id', label: 'Patient ID', logicalType: 'string',
      shape: 'scalar', nullable: true, emissionId: 'patient_id', candidateId: 'candidate-patient-id',
      occurrenceId: 'base', construction: 'VALUE', reductionPolicy: 'VALUE',
      sourceResourceType: 'Patient', sourcePath: 'id',
    });
    assert.equal(Object.hasOwn(schemaColumn, 'repeated'), false,
      'the sparse scalar schema must omit repeated when the published value is false');
  });

  const repeatedPackage = artifactPackage('CSV', oracle.rows, { column: { repeated: true } });
  await withArchive(repeatedPackage.archive, archivePath => assert.throws(() => inspectBasicPatientDatasetZip(archivePath, {
    expectedIdentity,
    expectedRows: oracle.rows,
    expectedArchiveSHA256: sha256(repeatedPackage.archive),
  }), /schema column repeated must bind the authored Patient ID field/));
});

test('archive oracle rejects a correctly checksummed package with the wrong declared Patient ID label', async () => {
  const oracle = await readBasicPatientDatasetZipOracle(fixtureDir, {
    project: expectedIdentity.project,
    generation: expectedIdentity.datasetGeneration,
  });
  const { archive } = artifactPackage('CSV', oracle.rows, { column: { label: 'Wrong label' } });
  await withArchive(archive, archivePath => assert.throws(() => inspectBasicPatientDatasetZip(archivePath, {
    expectedIdentity,
    expectedRows: oracle.rows,
    expectedArchiveSHA256: sha256(archive),
  }), /schema column label must bind the authored Patient ID field/));
});

test('archive oracle rejects rows that do not match the literal Basic Patient IDs', async () => {
  const oracle = await readBasicPatientDatasetZipOracle(fixtureDir, {
    project: expectedIdentity.project,
    generation: expectedIdentity.datasetGeneration,
  });
  const wrongRows = [{ patient_id: 'dev-patient-001' }, { patient_id: 'dev-patient-999' }];
  const { archive } = artifactPackage('JSONL', wrongRows);
  await withArchive(archive, archivePath => assert.throws(() => inspectBasicPatientDatasetZip(archivePath, {
    expectedIdentity,
    expectedRows: oracle.rows,
    expectedArchiveSHA256: sha256(archive),
  }), /dataset ZIP rows must match the independent source oracle/));
});
