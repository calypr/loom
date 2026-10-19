import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  assertCdaPatientDatasetZipReread,
  bindCdaPatientDatasetZipPublication,
  cdaPatientDatasetZipRereadQuery,
  cdaPatientDatasetZipScanQuery,
  CdaPatientDatasetZipWitnessUnavailable,
  inspectCdaPatientDatasetZip,
  MAX_CDA_PATIENT_DATASET_ZIP_SCAN,
  prepareCdaPatientDatasetZipOracle,
} from '../cda-viewer-output-dataset-zip-oracle.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const selectedRows = [
  { _id: 'Patient/source-a', id: 'cda-patient-a', project, generation, resourceType: 'Patient', fieldPresent: true, fieldValue: 'cda-patient-a' },
  { _id: 'Patient/source-b', id: 'cda-patient-b', project, generation, resourceType: 'Patient', fieldPresent: true, fieldValue: 'cda-patient-b' },
];
const sha256 = value => createHash('sha256').update(value).digest('hex');
const expectedIdentity = {
  project,
  datasetGeneration: generation,
  outputId: 'patient-output-1',
  revisionId: 'revision-cda-patient-1',
  schemaDigest: 'a'.repeat(64),
};
const candidate = {
  fieldPath: 'id',
  logicalType: 'string',
  repeated: false,
  cardinality: 'optional_one',
};
const compiledPreviewColumn = {
  column: 'cda_patient_id',
  label: 'Patient ID',
  logicalType: 'string',
  shape: 'scalar',
  nullable: true,
};

const crc32 = bytes => {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
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
  return Buffer.concat([localDirectory, centralDirectory, end]);
};

const packageBytes = (format, patientIDs, columnName = 'cda_patient_id', nullable = true) => {
  const identity = {
    ...expectedIdentity,
    receiptId: 'receipt-cda-patient-1',
    executionId: 'execution-cda-patient-1',
    outputContractDigest: 'b'.repeat(64),
  };
  const column = {
    name: columnName,
    outputKey: columnName,
    label: 'Patient ID',
    logicalType: 'string',
    shape: 'scalar',
    ...(nullable ? { nullable: true } : {}),
    emissionId: 'patient-id-emission-1',
    candidateId: 'candidate-patient-id',
    occurrenceId: 'base',
    construction: 'VALUE',
    reductionPolicy: 'VALUE',
    sourceResourceType: 'Patient',
    sourcePath: 'id',
  };
  const descriptor = {
    version: 1,
    outputKey: expectedIdentity.outputId,
    receiptFormatVersion: 2,
    compilerContractVersion: 'compiler-test-v1',
    recipeSchemaVersion: 1,
    translationVersion: 'translation-test-v1',
    sourceGeneration: generation,
    publishedSchemaDigest: expectedIdentity.schemaDigest,
    resolvedSchemaDigest: 'c'.repeat(64),
    outputContractDigest: identity.outputContractDigest,
    rowGrain: 'patient',
    rowMultiplication: 'none',
    rowIdentity: { key: '__loom_row_id', sourceResourceType: 'Patient', sourceIdColumn: columnName },
    columns: [column],
  };
  const schema = { format, columns: [column], nullEncoding: String.raw`\N`, arrayEncoding: format === 'JSONL' ? 'native' : 'json' };
  const provenance = {
    project,
    explorerId: 'cda-patient-zip-test-explorer',
    revisionId: identity.revisionId,
    receiptId: identity.receiptId,
    executionId: identity.executionId,
    outputId: identity.outputId,
    datasetGeneration: generation,
    schemaDigest: identity.schemaDigest,
    authorizationScopeDigest: 'd'.repeat(64),
  };
  const data = format === 'CSV'
    ? `${columnName}\n${patientIDs.join('\n')}\n`
    : `${patientIDs.map((id, index) => JSON.stringify({ rowId: `patient-row-${index + 1}`, values: { [columnName]: id } })).join('\n')}\n`;
  const payload = new Map([
    [format === 'CSV' ? 'data.csv' : 'data.jsonl', Buffer.from(data)],
    ['schema.json', Buffer.from(JSON.stringify(schema))],
    ['provenance.json', Buffer.from(JSON.stringify(provenance))],
    ['quality.json', Buffer.from('[]')],
    ['README.md', Buffer.from('# CDA test artifact\n')],
  ]);
  const manifest = {
    version: 2,
    identity,
    descriptor,
    format,
    selection: { recipe: {}, datasetGeneration: generation },
    interpretations: [],
    rows: patientIDs.length,
    features: 1,
    nullEncoding: String.raw`\N`,
    arrayEncoding: format === 'JSONL' ? 'native' : 'json',
    members: [...payload].map(([name, bytes]) => ({ name, sha256: sha256(bytes), bytes: bytes.length })),
  };
  payload.set('manifest.json', Buffer.from(JSON.stringify(manifest)));
  return storedZip([...payload]);
};

const withArchive = async (archive, run) => {
  const directory = await mkdtemp(join(tmpdir(), 'loom-cda-dataset-zip-'));
  try {
    const archivePath = join(directory, 'dataset.zip');
    await writeFile(archivePath, archive);
    return await run(archivePath);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
};

const oracleFor = rows => prepareCdaPatientDatasetZipOracle(rows, { project, generation });
const boundOracleFor = (oracle, columnName = 'cda_patient_id', previewColumn = compiledPreviewColumn) => bindCdaPatientDatasetZipPublication(oracle, {
  explorerId: 'cda-patient-zip-test-explorer',
  outputId: expectedIdentity.outputId,
  revisionId: expectedIdentity.revisionId,
  schemaDigest: expectedIdentity.schemaDigest,
  columnName,
  candidate,
  compiledPreviewColumn: { ...previewColumn, column: columnName },
});

test('CDA Patient ZIP source queries stay scoped and exact rereads pin the selected document keys', () => {
  const scan = cdaPatientDatasetZipScanQuery({ project, generation });
  assert(scan.includes(`r.project == ${JSON.stringify(project)}`));
  assert(scan.includes(`r.dataset_generation == ${JSON.stringify(generation)}`));
  assert(scan.includes('r.payload.resourceType == "Patient"'));
  assert(scan.includes(`LIMIT ${MAX_CDA_PATIENT_DATASET_ZIP_SCAN}`));
  const reread = cdaPatientDatasetZipRereadQuery({
    project,
    generation,
    documentIDs: selectedRows.map(row => row._id),
  });
  assert(reread.includes(`r._id IN ${JSON.stringify(selectedRows.map(row => row._id))}`));
  assert(reread.includes('r.payload.id'));
});

test('CDA Patient ZIP oracle independently selects and rereads exact payload IDs', () => {
  const oracle = oracleFor([
    { ...selectedRows[1] },
    { ...selectedRows[0] },
    { _id: 'Patient/no-payload-match', id: 'cda-patient-c', project, generation,
      resourceType: 'Patient', fieldPresent: true, fieldValue: 'different-id' },
  ]);
  assert.deepEqual(oracle.selected.map(row => row._id), ['Patient/source-a', 'Patient/source-b']);
  assert.deepEqual([...oracle.patientIDs].sort(), ['cda-patient-a', 'cda-patient-b']);
  assert.deepEqual(oracle.references, [
    { project, generation, resourceType: 'Patient', id: 'cda-patient-a' },
    { project, generation, resourceType: 'Patient', id: 'cda-patient-b' },
  ]);
  assert.deepEqual(assertCdaPatientDatasetZipReread([...selectedRows].reverse(), oracle), {
    exact: true, count: 2, project, generation,
  });
  assert.throws(() => assertCdaPatientDatasetZipReread([
    { ...selectedRows[0], fieldValue: 'changed-payload-id' }, selectedRows[1],
  ], oracle), /raw reread differs/);
});

test('CDA Patient ZIP oracle reports unavailable when bounded source rows cannot provide two payload IDs', () => {
  assert.throws(() => oracleFor([selectedRows[0], {
    ...selectedRows[1], fieldPresent: false, fieldValue: null,
  }]), CdaPatientDatasetZipWitnessUnavailable);
});

test('CDA Patient ZIP schema nullability comes from the exact compiled Preview column', async () => {
  const oracle = oracleFor(selectedRows);
  const requiredPreviewColumn = { ...compiledPreviewColumn, nullable: false };
  const bound = boundOracleFor(oracle, 'cda_patient_id', requiredPreviewColumn);
  assert.equal(bound.expectedColumn.nullable, false,
    'compiled Preview must control archive schema nullability despite optional catalog cardinality');

  const archive = packageBytes('JSONL', oracle.patientIDs, 'cda_patient_id', false);
  await withArchive(archive, archivePath => {
    const inspected = inspectCdaPatientDatasetZip(archivePath, bound, {
      expectedArchiveSHA256: sha256(archive),
    });
    assert.equal(Object.hasOwn(inspected.artifact.schema.columns[0], 'nullable'), false,
      'archive schema writer may omit its false nullable value');
  });
  assert.throws(() => bindCdaPatientDatasetZipPublication(oracle, {
    explorerId: 'cda-patient-zip-test-explorer',
    outputId: expectedIdentity.outputId,
    revisionId: expectedIdentity.revisionId,
    schemaDigest: expectedIdentity.schemaDigest,
    columnName: 'cda_patient_id',
    candidate,
    compiledPreviewColumn: { ...compiledPreviewColumn, column: 'foreign_output_column' },
  }), /must belong to the authored Patient ID output/);
});

for (const format of ['CSV', 'JSONL']) {
  test(`${format} CDA Patient ZIP inspector binds raw IDs, authored schema, identity, and provenance`, async () => {
    const oracle = oracleFor(selectedRows);
    const bound = boundOracleFor(oracle);
    const archive = packageBytes(format, oracle.patientIDs);
    await withArchive(archive, archivePath => {
      const result = inspectCdaPatientDatasetZip(archivePath, bound, {
        expectedArchiveSHA256: sha256(archive),
      });
      assert.deepEqual([...result.memberNames].sort(), [
        'README.md', 'data.csv', 'data.jsonl', 'manifest.json', 'provenance.json', 'quality.json', 'schema.json',
      ].filter(name => name !== (format === 'CSV' ? 'data.jsonl' : 'data.csv')).sort());
      assert.equal(result.artifact.manifest.identity.outputId, expectedIdentity.outputId);
      assert.equal(result.artifact.provenance.explorerId, 'cda-patient-zip-test-explorer');
      assert.deepEqual(result.artifact.rows.map(row => row.values.cda_patient_id).sort(),
        ['cda-patient-a', 'cda-patient-b']);
      const column = result.artifact.schema.columns[0];
      assert.equal(Object.hasOwn(column, 'repeated'), false,
        'published scalar false may be omitted by the archive schema writer');
    });
  });
}

test('CDA Patient ZIP inspector rejects descriptor hash mismatch and non-oracle rows', async () => {
  const oracle = oracleFor(selectedRows);
  const bound = boundOracleFor(oracle);
  const archive = packageBytes('JSONL', ['cda-patient-a', 'cda-patient-wrong']);
  await withArchive(archive, archivePath => {
    assert.throws(() => inspectCdaPatientDatasetZip(archivePath, bound, {
      expectedArchiveSHA256: '0'.repeat(64),
    }), /COMPLETE preparation descriptor archiveSha256/);
    assert.throws(() => inspectCdaPatientDatasetZip(archivePath, bound, {
      expectedArchiveSHA256: sha256(archive),
    }), /dataset ZIP rows must match the independent source oracle/);
  });
});
