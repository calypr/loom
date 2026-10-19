import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { readNDJSONResourceIdentityOracle } from './ndjson-resource-oracle.mjs';

const BASIC_PATIENT_IDS = ['dev-patient-001', 'dev-patient-002'];
const REQUIRED_IDENTITY_FIELDS = [
  'project', 'datasetGeneration', 'receiptId', 'executionId', 'outputId',
  'revisionId', 'schemaDigest', 'outputContractDigest',
];
const sha256 = value => createHash('sha256').update(value).digest('hex');
const expectedMembers = format => [
  format === 'CSV' ? 'data.csv' : 'data.jsonl',
  'schema.json',
  'provenance.json',
  'quality.json',
  'README.md',
  'manifest.json',
].sort();
const requiredText = (value, name) => {
  assert.equal(typeof value, 'string', `${name} must be a string`);
  assert.notEqual(value.trim(), '', `${name} must be non-empty`);
};

const crc32 = bytes => {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
  }
  return (crc ^ 0xffffffff) >>> 0;
};

// Read the stored members from the actual archive bytes. The layout follows
// the central directory, so entries written with ZIP data descriptors remain
// readable even when their local size fields are zero.
const readStoredZip = archivePath => {
  const archive = readFileSync(archivePath);
  let endOffset = -1;
  for (let offset = archive.length - 22; offset >= Math.max(0, archive.length - 65_557); offset -= 1) {
    if (archive.readUInt32LE(offset) === 0x06054b50) { endOffset = offset; break; }
  }
  assert(endOffset >= 0, 'dataset ZIP must have an end-of-central-directory record');
  const entryCount = archive.readUInt16LE(endOffset + 10);
  const centralSize = archive.readUInt32LE(endOffset + 12);
  const centralOffset = archive.readUInt32LE(endOffset + 16);
  assert(centralOffset + centralSize <= endOffset, 'dataset ZIP central directory must fit before its end record');
  let cursor = centralOffset;
  const members = new Map();
  for (let index = 0; index < entryCount; index += 1) {
    assert.equal(archive.readUInt32LE(cursor), 0x02014b50, 'dataset ZIP central directory entry must be valid');
    const flags = archive.readUInt16LE(cursor + 8);
    const method = archive.readUInt16LE(cursor + 10);
    const checksum = archive.readUInt32LE(cursor + 16);
    const compressedBytes = archive.readUInt32LE(cursor + 20);
    const uncompressedBytes = archive.readUInt32LE(cursor + 24);
    const nameBytes = archive.readUInt16LE(cursor + 28);
    const extraBytes = archive.readUInt16LE(cursor + 30);
    const commentBytes = archive.readUInt16LE(cursor + 32);
    const localOffset = archive.readUInt32LE(cursor + 42);
    const name = archive.subarray(cursor + 46, cursor + 46 + nameBytes).toString('utf8');
    assert.equal(flags & 1, 0, `dataset ZIP member ${name} must not be encrypted`);
    assert.equal(method, 0, `dataset ZIP member ${name} must use stored compression`);
    assert.equal(compressedBytes, uncompressedBytes, `dataset ZIP member ${name} stored lengths must match`);
    assert(!members.has(name), `dataset ZIP member ${name} must not be duplicated`);
    assert.equal(archive.readUInt32LE(localOffset), 0x04034b50, `dataset ZIP member ${name} local header must be valid`);
    const localNameBytes = archive.readUInt16LE(localOffset + 26);
    const localExtraBytes = archive.readUInt16LE(localOffset + 28);
    const localName = archive.subarray(localOffset + 30, localOffset + 30 + localNameBytes).toString('utf8');
    assert.equal(localName, name, `dataset ZIP member ${name} local and central names must match`);
    const dataOffset = localOffset + 30 + localNameBytes + localExtraBytes;
    const data = archive.subarray(dataOffset, dataOffset + compressedBytes);
    assert.equal(data.length, uncompressedBytes, `dataset ZIP member ${name} bytes must be complete`);
    assert.equal(crc32(data), checksum, `dataset ZIP member ${name} CRC must match its central directory`);
    members.set(name, data);
    cursor += 46 + nameBytes + extraBytes + commentBytes;
  }
  assert.equal(cursor, centralOffset + centralSize, 'dataset ZIP central directory must have no trailing entries');
  return members;
};

const parseJSONMember = (members, name) => {
  const bytes = members.get(name);
  assert(bytes, `dataset ZIP must contain ${name}`);
  try {
    return JSON.parse(bytes.toString('utf8'));
  } catch (error) {
    throw new Error(`dataset ZIP ${name} is invalid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
};

const parseCSV = text => {
  const rows = [];
  let cells = [];
  let value = '';
  let quoted = false;
  let cellQuoted = false;
  let atCellStart = true;
  const pushCell = () => {
    cells.push({ value, quoted: cellQuoted });
    value = '';
    cellQuoted = false;
    atCellStart = true;
  };
  const pushRow = () => {
    pushCell();
    if (cells.some(cell => cell.value !== '') || rows.length > 0 || cells.length > 1) rows.push(cells);
    cells = [];
  };
  for (let index = 0; index < text.length; index += 1) {
    const character = text[index];
    if (quoted && character === '"' && text[index + 1] === '"') {
      value += '"';
      index += 1;
      continue;
    }
    if (character === '"') {
      assert(!quoted || text[index + 1] !== undefined, 'dataset ZIP CSV quoted field must be closed');
      assert(quoted || atCellStart, 'dataset ZIP CSV quote must begin a field');
      quoted = !quoted;
      cellQuoted = true;
      atCellStart = false;
      continue;
    }
    if (!quoted && (character === ',' || character === '\n')) {
      if (character === '\n') {
        if (value.endsWith('\r')) value = value.slice(0, -1);
        pushRow();
      } else pushCell();
      continue;
    }
    value += character;
    atCellStart = false;
  }
  assert(!quoted, 'dataset ZIP CSV must not have an unterminated quoted field');
  if (value || cells.length || cellQuoted) {
    if (value.endsWith('\r')) value = value.slice(0, -1);
    pushCell();
    rows.push(cells);
  }
  return rows;
};

const inspectJ05ArtifactPackage = members => {
  const manifest = parseJSONMember(members, 'manifest.json');
  const schema = parseJSONMember(members, 'schema.json');
  const provenance = parseJSONMember(members, 'provenance.json');
  assert.equal(manifest.version, 2, 'dataset ZIP manifest version must be 2');
  assert(['CSV', 'JSONL'].includes(manifest.format), `dataset ZIP format ${manifest.format} must be CSV or JSONL`);
  const dataName = manifest.format === 'CSV' ? 'data.csv' : 'data.jsonl';
  assert(!members.has(manifest.format === 'CSV' ? 'data.jsonl' : 'data.csv'),
    'dataset ZIP must not include the alternate data representation');
  assert(Array.isArray(schema.columns) && schema.columns.length > 0,
    'dataset ZIP schema must declare output columns');
  assert.equal(schema.format, manifest.format, 'dataset ZIP manifest and schema formats must match');
  assert.equal(manifest.features, schema.columns.length, 'dataset ZIP feature count must match its schema');
  assert.deepEqual(manifest.descriptor?.columns?.map(column => column.name), schema.columns.map(column => column.name),
    'dataset ZIP descriptor column order must match schema.json');
  const contracts = columns => columns.map(column => ({
    name: column.name,
    outputKey: column.outputKey,
    logicalType: column.logicalType,
    shape: column.shape,
    nullable: Boolean(column.nullable),
    repeated: Boolean(column.repeated),
  }));
  assert.deepEqual(contracts(schema.columns), contracts(manifest.descriptor.columns),
    'dataset ZIP descriptor and schema must agree on column types and shapes');

  assert(Array.isArray(manifest.members), 'dataset ZIP manifest must list member checksums');
  const checksumNames = new Set();
  for (const item of manifest.members) {
    assert(item?.name && item.name !== 'manifest.json' && !checksumNames.has(item.name),
      'dataset ZIP manifest member checksum names must be unique and exclude manifest.json');
    checksumNames.add(item.name);
    const bytes = members.get(item.name);
    assert(bytes && bytes.length === item.bytes && sha256(bytes) === item.sha256,
      `dataset ZIP member checksum must match ${item.name}`);
  }
  const payloadNames = [...members.keys()].filter(name => name !== 'manifest.json').sort();
  assert.deepEqual([...checksumNames].sort(), payloadNames,
    'dataset ZIP manifest checksums must cover every non-manifest member exactly once');
  for (const required of [dataName, 'schema.json', 'provenance.json', 'quality.json']) {
    assert(members.has(required), `dataset ZIP must contain ${required}`);
  }

  let rows;
  if (manifest.format === 'CSV') {
    const parsed = parseCSV(members.get(dataName).toString('utf8'));
    const header = parsed.shift()?.map(cell => cell.value) ?? [];
    assert.deepEqual(header, schema.columns.map(column => column.name), 'dataset ZIP CSV header must match schema order');
    rows = parsed.map((cells, rowIndex) => {
      assert.equal(cells.length, schema.columns.length, `dataset ZIP CSV row ${rowIndex + 1} must match feature count`);
      return {
        rowId: undefined,
        values: Object.fromEntries(schema.columns.map((column, columnIndex) => {
          const cell = cells[columnIndex];
          return [column.name, !cell.quoted && cell.value === manifest.nullEncoding ? null : cell.value];
        })),
      };
    });
  } else {
    rows = members.get(dataName).toString('utf8').split(/\r?\n/).filter(Boolean).map((line, index) => {
      let parsed;
      try { parsed = JSON.parse(line); }
      catch (error) {
        throw new Error(`dataset ZIP JSONL row ${index + 1} is invalid JSON: ${error instanceof Error ? error.message : String(error)}`);
      }
      assert(Object.hasOwn(parsed ?? {}, 'rowId') && parsed.values && typeof parsed.values === 'object' && !Array.isArray(parsed.values),
        `dataset ZIP JSONL row ${index + 1} must include rowId and values`);
      return {
        rowId: parsed.rowId,
        values: Object.fromEntries(schema.columns.filter(column => Object.hasOwn(parsed.values, column.outputKey))
          .map(column => [column.name, parsed.values[column.outputKey]])),
      };
    });
  }
  assert.equal(rows.length, manifest.rows, 'dataset ZIP row count must match its manifest');
  const rowIdentityColumn = manifest.descriptor?.rowIdentity?.sourceIdColumn;
  const rowIdentities = rows.map(row => JSON.stringify(row.rowId ?? row.values[rowIdentityColumn]));
  assert(!rowIdentities.some(identity => identity === undefined || identity === 'null'),
    'dataset ZIP rows must carry stable identities');
  assert.equal(new Set(rowIdentities).size, rowIdentities.length, 'dataset ZIP row identities must be distinct');
  return { manifest, schema, provenance, rows, dataName };
};

export async function readBasicPatientDatasetZipOracle(fixtureDir, {
  project,
  generation,
  outputId = 'patients',
} = {}) {
  requiredText(fixtureDir, 'fixtureDir');
  requiredText(project, 'project');
  requiredText(generation, 'generation');
  requiredText(outputId, 'outputId');
  assert.equal(outputId, 'patients', 'Basic Patient dataset ZIP oracle is scoped to output patients');

  const patientPath = resolve(fixtureDir, 'Patient.ndjson');
  const source = await readNDJSONResourceIdentityOracle({
    path: patientPath,
    project,
    generation,
    resourceType: 'Patient',
  });
  assert.equal(source.count, BASIC_PATIENT_IDS.length,
    'Basic Patient dataset ZIP oracle requires exactly the two literal fixture Patients');
  assert.deepEqual([...source.ids].sort(), [...BASIC_PATIENT_IDS].sort(),
    'Basic Patient dataset ZIP oracle must retain the literal fixture Patient identities');

  return {
    patientPath,
    patientSHA256: source.sha256,
    rows: source.ids.map(patient_id => ({ patient_id })),
    patientIDs: source.ids,
    project,
    generation,
    outputId,
    rowGrain: 'patient',
  };
}

const parseManifest = bytes => {
  assert(Buffer.isBuffer(bytes) && bytes.length > 0, 'dataset ZIP manifest.json must have bytes');
  try {
    return JSON.parse(bytes.toString('utf8'));
  } catch (error) {
    throw new Error(`dataset ZIP manifest.json is invalid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
};

const validateBasicPatientSchema = (artifact, expectedIdentity) => {
  const { manifest, schema } = artifact;
  assert.equal(manifest.rows, 2, 'dataset ZIP manifest must declare the two Basic Patient rows');
  assert.equal(manifest.features, 1, 'dataset ZIP manifest must declare one Patient ID feature');
  assert.equal(manifest.nullEncoding, String.raw`\N`, 'dataset ZIP manifest must retain the declared null encoding');
  assert.equal(manifest.arrayEncoding, manifest.format === 'JSONL' ? 'native' : 'json',
    'dataset ZIP manifest array encoding must match its typed representation');
  assert.equal(manifest.selection?.datasetGeneration, expectedIdentity.datasetGeneration,
    'dataset ZIP selection metadata must bind the fixture generation');

  const descriptor = manifest.descriptor;
  assert.equal(descriptor.version, 1, 'dataset ZIP descriptor version must be 1');
  assert.equal(descriptor.outputKey, 'patients', 'dataset ZIP descriptor must bind output patients');
  assert.equal(descriptor.sourceGeneration, expectedIdentity.datasetGeneration,
    'dataset ZIP descriptor must bind the fixture generation');
  assert.equal(descriptor.publishedSchemaDigest, expectedIdentity.schemaDigest,
    'dataset ZIP descriptor must bind the published schema digest');
  assert.equal(descriptor.outputContractDigest, manifest.identity.outputContractDigest,
    'dataset ZIP descriptor must bind the output contract digest');
  assert.equal(descriptor.rowGrain, 'patient', 'dataset ZIP descriptor must retain Patient row grain');
  assert.equal(descriptor.rowMultiplication, 'none', 'dataset ZIP descriptor must preserve one row per Patient');
  assert.deepEqual(descriptor.rowIdentity, {
    key: '__loom_row_id',
    sourceResourceType: 'Patient',
    sourceIdColumn: 'patient_id',
  }, 'dataset ZIP descriptor must bind row identity to the exported Patient ID');

  assert.equal(schema.format, manifest.format, 'dataset ZIP schema format must match the manifest');
  assert.equal(schema.columns.length, 1, 'dataset ZIP schema must contain exactly the Patient ID column');
  const [column] = schema.columns;
  for (const [field, expected] of Object.entries({
    name: 'patient_id',
    outputKey: 'patient_id',
    label: 'Patient ID',
    logicalType: 'string',
    shape: 'scalar',
    nullable: true,
    repeated: false,
    occurrenceId: 'base',
    construction: 'VALUE',
    reductionPolicy: 'VALUE',
    sourceResourceType: 'Patient',
    sourcePath: 'id',
  })) {
    const actual = field === 'repeated' && !Object.hasOwn(column, field) ? false : column[field];
    assert.equal(actual, expected, `dataset ZIP schema column ${field} must bind the authored Patient ID field`);
  }
  requiredText(column.emissionId, 'dataset ZIP schema emissionId');
  requiredText(column.candidateId, 'dataset ZIP schema candidateId');
  assert.deepEqual(descriptor.columns, schema.columns,
    'dataset ZIP descriptor must retain the complete published column lineage');
};

const validateArtifactProvenance = (artifact, expectedIdentity, expectedExplorerId) => {
  const identity = artifact.manifest.identity;
  const provenance = artifact.provenance;
  for (const field of REQUIRED_IDENTITY_FIELDS) requiredText(identity?.[field], `manifest identity ${field}`);
  for (const field of [
    'project', 'datasetGeneration', 'revisionId', 'receiptId', 'executionId', 'outputId', 'schemaDigest',
  ]) {
    assert.equal(provenance?.[field], identity[field],
      `dataset ZIP provenance ${field} must match its immutable manifest identity`);
  }
  for (const field of ['project', 'datasetGeneration', 'outputId', 'revisionId', 'schemaDigest']) {
    assert.equal(provenance[field], expectedIdentity[field],
      `dataset ZIP provenance ${field} must bind the expected publication`);
  }
  requiredText(provenance.explorerId, 'dataset ZIP provenance explorerId');
  requiredText(provenance.authorizationScopeDigest, 'dataset ZIP provenance authorizationScopeDigest');
  assert.equal(provenance.explorerId, expectedExplorerId,
    'dataset ZIP provenance must bind the expected Explorer');
};

export function inspectDatasetArtifactZip(archivePath, {
  expectedIdentity,
  expectedExplorerId,
  expectedRows,
  expectedArchiveSHA256,
} = {}) {
  requiredText(archivePath, 'archivePath');
  for (const field of ['project', 'datasetGeneration', 'outputId', 'revisionId', 'schemaDigest']) {
    requiredText(expectedIdentity?.[field], `expected identity ${field}`);
  }
  requiredText(expectedExplorerId, 'expectedExplorerId');
  assert(Array.isArray(expectedRows), 'dataset ZIP inspection requires independent expected rows');
  requiredText(expectedArchiveSHA256, 'expectedArchiveSHA256');
  assert.match(expectedArchiveSHA256, /^[a-f0-9]{64}$/i,
    'expectedArchiveSHA256 must be a SHA-256 hex digest from the COMPLETE preparation descriptor');

  const archiveBytes = readFileSync(archivePath);
  const archiveSHA256 = sha256(archiveBytes);
  assert.equal(expectedArchiveSHA256.toLowerCase(), archiveSHA256,
    'dataset ZIP archive SHA-256 must equal the COMPLETE preparation descriptor archiveSha256');

  const members = readStoredZip(archivePath);
  const memberNames = [...members.keys()].sort();
  const manifestBytes = members.get('manifest.json');
  const manifest = parseManifest(manifestBytes);
  const artifact = inspectJ05ArtifactPackage(members);
  assert.deepEqual(memberNames, expectedMembers(manifest.format),
    'dataset ZIP must contain exactly the format-specific members');
  assert.deepEqual(manifest, artifact.manifest, 'manifest bytes must parse to the artifact inspector manifest');
  for (const field of ['project', 'datasetGeneration', 'outputId', 'revisionId', 'schemaDigest']) {
    assert.equal(artifact.manifest.identity?.[field], expectedIdentity[field],
      `dataset ZIP manifest ${field} must match the current publication`);
  }
  for (const [manifestField, expectedField] of [
    ['sourceGeneration', 'datasetGeneration'],
    ['outputKey', 'outputId'],
    ['publishedSchemaDigest', 'schemaDigest'],
  ]) {
    assert.equal(artifact.manifest.descriptor?.[manifestField], expectedIdentity[expectedField],
      `dataset ZIP descriptor ${manifestField} must match the current publication`);
  }
  validateArtifactProvenance(artifact, expectedIdentity, expectedExplorerId);
  const canonicalRows = rows => rows.map(row => ({ values: row.values }))
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  assert.deepEqual(canonicalRows(artifact.rows), canonicalRows(expectedRows.map(values => ({ values }))),
    'dataset ZIP rows must match the independent source oracle');

  return {
    members,
    memberNames,
    archiveSHA256,
    archiveBytes: archiveBytes.length,
    manifestBytes,
    manifestSHA256: sha256(manifestBytes),
    artifact,
  };
}

export function inspectBasicPatientDatasetZip(archivePath, {
  expectedIdentity,
  expectedRows,
  expectedArchiveSHA256,
} = {}) {
  assert.equal(expectedIdentity?.outputId, 'patients', 'Basic Patient ZIP identity must target output patients');
  assert(Array.isArray(expectedRows) && expectedRows.length === BASIC_PATIENT_IDS.length,
    'Basic Patient ZIP inspection requires the two literal source rows');
  const expectedIDs = expectedRows.map(row => row?.patient_id);
  assert.deepEqual([...expectedIDs].sort(), [...BASIC_PATIENT_IDS].sort(),
    'expected ZIP rows must be exactly the two literal Basic Patient IDs');

  const inspected = inspectDatasetArtifactZip(archivePath, {
    expectedIdentity,
    expectedExplorerId: 'default',
    expectedRows,
    expectedArchiveSHA256,
  });
  validateBasicPatientSchema(inspected.artifact, expectedIdentity);
  return inspected;
}
