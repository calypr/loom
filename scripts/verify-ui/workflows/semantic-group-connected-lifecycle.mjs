import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import { captureBuilderScreenshot } from './verify-cda-builder-related-source-chooser.mjs';

const ACTION_BUDGET_MS = 5_000;
const ROOT_DISCOVERY_LIMIT = 300;
const TARGET_DISCOVERY_LIMIT = 16;
const ROOT_SAMPLE_SIZE = 2;
const TARGET_RESULT_LIMIT = 1_000;
const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const digest = value => createHash('sha256').update(JSON.stringify(value)).digest('hex');
const encode = value => encodeURIComponent(value);
const displayCell = value => Array.isArray(value) ? value.map(clean).join('; ') : clean(value);

function runReadOnlyAQL(container, database, query, marker) {
  const javascript = `var rows = db._query(${JSON.stringify(query)}).toArray(); print(${JSON.stringify(marker)} + JSON.stringify(rows));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', container, 'arangosh', '--server.database', database,
    '--server.password', '', '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0,
    `Bounded read-only connected Group oracle failed (exit=${result.status}, outputSha256=${digest(clean(result.stderr || result.stdout))}).`);
  const markerOffset = result.stdout.indexOf(marker);
  assert(markerOffset >= 0, 'The bounded connected Group oracle returned no marked JSON result.');
  const rows = JSON.parse(result.stdout.slice(markerOffset + marker.length).trim());
  assert(Array.isArray(rows));
  return rows;
}

function routeSubquery(project, generation, targetLimit) {
  return `LET specimens = (FOR edge IN fhir_edge
    FILTER edge._to == patient._id AND edge.from_type == "Specimen" AND edge.to_type == "Patient"
      AND edge.label == "subject_Patient" AND edge.project == ${JSON.stringify(project)}
      AND edge.dataset_generation == ${JSON.stringify(generation)}
    LET specimen = DOCUMENT(edge._from)
    FILTER specimen != null AND specimen.project == ${JSON.stringify(project)}
      AND specimen.dataset_generation == ${JSON.stringify(generation)}
      AND specimen.resourceType == "Specimen" AND IS_STRING(specimen.id)
    COLLECT specimenKey = specimen._key INTO routeEdges = edge
    LET target = DOCUMENT(CONCAT("Specimen/", specimenKey))
    LET codings = IS_ARRAY(target.payload.type.coding) ? target.payload.type.coding : []
    SORT target.id
    LIMIT ${targetLimit}
    RETURN {key:target._key,id:target.id,subjectReference:target.payload.subject.reference,
      typeText:target.payload.type.text,duplicateRouteEdges:LENGTH(routeEdges),typeCodings:codings})`;
}

function readPatientDiscovery(container, database, project, generation) {
  const query = `FOR patient IN Patient
    FILTER patient.project == ${JSON.stringify(project)} AND patient.dataset_generation == ${JSON.stringify(generation)}
      AND patient.resourceType == "Patient" AND IS_STRING(patient.id)
    SORT patient._key
    LIMIT ${ROOT_DISCOVERY_LIMIT}
    ${routeSubquery(project, generation, TARGET_DISCOVERY_LIMIT)}
    FILTER LENGTH(specimens) > 1
    FILTER LENGTH(FOR specimen IN specimens FILTER LENGTH(specimen.typeCodings) > 0 RETURN 1) > 0
    SORT LENGTH(specimens) DESC, patient._key
    LIMIT 10
    RETURN {patientKey:patient._key,patientID:patient.id,specimenCount:LENGTH(specimens),specimens}`;
  return runReadOnlyAQL(container, database, query, '__CONNECTED_GROUP_DISCOVERY__');
}

function readExactPatientTargets(container, database, project, generation, patientKeys) {
  const query = `FOR patient IN Patient
    FILTER patient._key IN ${JSON.stringify(patientKeys)} AND patient.project == ${JSON.stringify(project)}
      AND patient.dataset_generation == ${JSON.stringify(generation)} AND patient.resourceType == "Patient"
    ${routeSubquery(project, generation, TARGET_RESULT_LIMIT + 1)}
    SORT patient._key
    RETURN {patientKey:patient._key,patientID:patient.id,specimenCount:LENGTH(specimens),specimens}`;
  return runReadOnlyAQL(container, database, query, '__CONNECTED_GROUP_SELECTED_ROOTS__');
}

function normalizeCodingTuple(coding, where) {
  for (const field of ['system', 'version', 'code', 'display']) {
    assert(coding[field] === undefined || coding[field] === null || typeof coding[field] === 'string',
      `${where}.${field} is a present non-string value.`);
  }
  const system = typeof coding.system === 'string' ? coding.system : null;
  const version = typeof coding.version === 'string' && coding.version.trim() ? coding.version : null;
  const code = typeof coding.code === 'string' && coding.code.trim() ? coding.code : null;
  const display = typeof coding.display === 'string' && coding.display.trim() ? coding.display : null;
  return { tuple: [system, version, code], display };
}

function buildConnectedGroupOracle(selectedRoots) {
  const grouped = new Map();
  const sourceRows = [];
  for (const patient of selectedRoots) {
    for (const specimen of patient.specimens) {
      const codings = specimen.typeCodings;
      assert(Array.isArray(codings), `Specimen ${specimen.id} type.coding must be an array or absent.`);
      const occurrences = codings.length > 0 ? codings : [null];
      occurrences.forEach((coding, ordinal) => {
        const normalized = coding
          ? normalizeCodingTuple(coding, `Specimen ${specimen.id}.type.coding[${ordinal}]`)
          : { tuple: [null, null, null], display: null };
        const key = JSON.stringify([patient.patientID, normalized.tuple]);
        let group = grouped.get(key);
        if (!group) {
          group = {
            patientID: patient.patientID,
            tuple: normalized.tuple,
            count: 0,
            displays: [],
            ownerIDs: new Set(),
            occurrences: [],
          };
          grouped.set(key, group);
        }
        group.count += 1;
        if (normalized.display !== null) group.displays.push(normalized.display);
        group.ownerIDs.add(specimen.id);
        group.occurrences.push({ specimenKey: specimen.key, specimenID: specimen.id, ordinal: coding ? ordinal : null });
        sourceRows.push({ patientID: patient.patientID, specimenID: specimen.id, ordinal: coding ? ordinal : null, tuple: normalized.tuple });
      });
    }
  }
  const groups = [...grouped.values()].map(group => {
    group.displays.sort((left, right) => left < right ? -1 : left > right ? 1 : 0);
    group.display = group.displays[0] ?? group.tuple[2] ?? null;
    group.ownerIDs = [...group.ownerIDs].sort();
    group.occurrences.sort((left, right) => left.specimenID.localeCompare(right.specimenID) || (left.ordinal ?? -1) - (right.ordinal ?? -1));
    return group;
  }).sort((left, right) => left.patientID.localeCompare(right.patientID) || JSON.stringify(left.tuple).localeCompare(JSON.stringify(right.tuple)));
  return { groups, sourceRows };
}

function relatedFacts(choice) {
  return choice?.relatedOccurrence && typeof choice.relatedOccurrence === 'object' ? choice.relatedOccurrence : null;
}

function exactRouteStep(route) {
  assert(Array.isArray(route) && route.length === 1,
    `The native witness must use one exact Patient-to-Specimen route: ${JSON.stringify(route)}`);
  const [step] = route;
  assert.equal(step.fromResourceType, 'Patient');
  assert.equal(step.toResourceType, 'Specimen');
  assert.equal(step.relationship, 'subject_Patient');
  assert.equal(step.storageDirection, 'INBOUND');
  return step;
}

function assertRelatedBinding(binding, { valueKind, candidateID, expectedRoute, expectedShapeID }) {
  assert.equal(binding?.kind, 'BINDING');
  assert.equal(binding?.valueKind, valueKind);
  assert.equal(binding?.sourceStageId, 'source_projection');
  assert.equal(binding?.sourceShapeId, expectedShapeID,
    'The persisted related binding must retain the exact compiler-issued source shape.');
  assert.equal(typeof binding?.sourceShapeId, 'string');
  assert(binding.sourceShapeId.trim());
  assert.equal(binding?.input?.kind, 'RELATED_OCCURRENCE');
  const related = binding.input.relatedOccurrence;
  assert(related && typeof related === 'object');
  assert.equal(related.anchorResourceType, 'Patient');
  assert.equal(related.targetResourceType, 'Specimen');
  assert.equal(related.multiplicity, 'DISTINCT_TARGET_PER_INPUT');
  assert.equal(related.emptyPolicy, 'PRESERVE_PARENT');
  assert.equal(related.candidateId, candidateID);
  exactRouteStep(related.route);
  const routeFields = [
    'stepIndex', 'fromOccurrenceId', 'fromNodeId', 'fromResourceType', 'edgeId',
    'relationship', 'storageDirection', 'matchMode', 'toOccurrenceId', 'toNodeId', 'toResourceType',
  ];
  assert.deepEqual(related.route.map(step => Object.fromEntries(routeFields.map(field => [field, step[field]]))),
    expectedRoute.map(step => Object.fromEntries(routeFields.map(field => [field, step[field]]))),
    'The persisted Group field binding must retain the exact current authorized route.');
  assert.equal(related.anchorOccurrenceId, expectedRoute[0].fromOccurrenceId);
  assert.equal(related.anchorNodeId, expectedRoute[0].fromNodeId);
  assert.equal(related.anchorResourceType, 'Patient');
  assert.equal(related.targetOccurrenceId, expectedRoute.at(-1).toOccurrenceId);
  assert.equal(related.targetNodeId, expectedRoute.at(-1).toNodeId);
  assert.equal(related.targetResourceType, 'Specimen');
  if (valueKind === 'CODING_IDENTITY') {
    assert.equal(related.resourceType, 'Coding');
    assert.equal(related.codingSelector, 'type.coding[]');
    assert.equal(related.codingEmptyPolicy, 'GROUP');
    assert.equal(related.candidateId, undefined);
    assert.equal(related.ordinalSemantics, 'ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY');
  } else {
    assert.equal(related.resourceType, 'string',
      'The persisted related resourceType must be the selected FHIR datatype, while targetResourceType remains Specimen.');
    assert.equal(related.canonicalPath, 'id');
    assert.equal(related.codingSelector, undefined);
  }
  return related;
}

function relatedRowValueChoiceMatchesBinding(choice, binding) {
  const related = binding?.input?.relatedOccurrence;
  return choice?.kind === 'FIELD_SOURCE' && choice.valueKind === 'SCALAR' &&
    choice.fieldSource?.kind === 'CHOICE' && binding?.kind === 'BINDING' && binding.valueKind === 'SCALAR' &&
    choice.sourceStageId === binding.sourceStageId && choice.sourceShapeId === binding.sourceShapeId &&
    choice.sourceRole === 'RELATED_OCCURRENCE' && choice.occurrenceId === related?.targetOccurrenceId &&
    choice.resourceType === related?.resourceType && choice.ownerOccurrenceId === related?.targetOccurrenceId &&
    choice.ownerResourceType === related?.targetResourceType && choice.candidateId === related?.candidateId &&
    choice.canonicalPath === related?.canonicalPath && !choice.boundarySelector;
}

function assertRootBinding(binding, candidateID, expectedShapeID) {
  assert.equal(binding?.kind, 'BINDING');
  assert.equal(binding?.valueKind, 'SCALAR');
  assert.equal(binding?.sourceStageId, 'source_projection');
  assert.equal(binding?.sourceShapeId, expectedShapeID,
    'The persisted Patient.id binding must retain the exact compiler-issued source shape.');
  assert.equal(typeof binding?.sourceShapeId, 'string');
  assert(binding.sourceShapeId.trim());
  assert.equal(binding?.input?.kind, 'CURRENT_STAGE');
  const current = binding.input.currentStage;
  assert.equal(current?.sourceRole, 'ROOT');
  assert.equal(current?.occurrenceId, 'base');
  assert.equal(current?.ownerResourceType, 'Patient');
  assert.equal(current?.resourceType, 'Patient');
  assert.equal(current?.candidateId, candidateID);
  assert.equal(current?.canonicalPath, 'id');
}

function relatedRouteBindingIdentity(binding) {
  const related = binding?.input?.relatedOccurrence;
  assert(related && typeof related === 'object');
  return {
    anchorOccurrenceId: related.anchorOccurrenceId,
    anchorNodeId: related.anchorNodeId,
    anchorResourceType: related.anchorResourceType,
    route: related.route,
    targetOccurrenceId: related.targetOccurrenceId,
    targetNodeId: related.targetNodeId,
    targetResourceType: related.targetResourceType,
    emptyPolicy: related.emptyPolicy,
    multiplicity: related.multiplicity,
  };
}

function documentFor(builder, outputId) {
  const matches = (builder.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, `Expected exactly one document for output ${outputId}.`);
  return matches[0];
}

function groupStepFor(builder, outputId) {
  return documentFor(builder, outputId).construction?.steps?.find(step => step.operation?.kind === 'GROUP');
}

function expectedGrid(step, oracle, choices, rowValueChoice, { includeArrayIndicators = false } = {}) {
  const rootKey = step.operation.group.keys.find(key =>
    key.fieldSource?.kind === 'CHOICE' ? key.fieldSource.choiceId === choices.patientID.fieldSource.choiceId
      : key.fieldSource?.valueKind === 'SCALAR' && key.fieldSource?.input?.currentStage?.canonicalPath === 'id');
  const codingKey = step.operation.group.keys.find(key =>
    key.fieldSource?.kind === 'CHOICE' ? key.fieldSource.choiceId === choices.coding.fieldSource.choiceId
      : key.fieldSource?.valueKind === 'CODING_IDENTITY');
  const count = step.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
  assert(rootKey && codingKey && count, 'The candidate Group must keep Patient.id, related Coding identity, and COUNT_ROWS.');
  const rowValue = (step.rowValues ?? []).find(item => item.fieldSource?.kind === 'CHOICE'
    ? item.fieldSource.choiceId === rowValueChoice?.fieldSource?.choiceId
    : item.outputColumnId && item.fieldSource?.valueKind === 'SCALAR' && item.fieldSource?.input?.kind === 'RELATED_OCCURRENCE');
  const headers = step.outputs.map(output => output.label);
  const rows = oracle.groups.map(group => step.outputs.map(output => {
    if (output.id === rootKey.outputColumnId) return group.patientID;
    if (output.id === codingKey.outputColumnId) return group.display ?? '';
    if (output.id === count.outputColumnId) return String(group.count);
    if (rowValue && output.id === rowValue.outputColumnId) {
      if (!includeArrayIndicators) return group.ownerIDs.join('; ');
      if (group.ownerIDs.length === 0) return 'Empty list';
      const countLabel = group.ownerIDs.length === 1 ? 'value' : 'values';
      return `${group.ownerIDs.join('; ')} ${group.ownerIDs.length} ${countLabel}`;
    }
    assert.fail(`Group output ${output.id} is not an exact key, COUNT_ROWS aggregate, or related RowValue.`);
  }));
  return { headers, rows: rows.map(row => row.map(displayCell)) };
}

function previewGridSnapshot({ kind, expected }) {
  const proposal = kind === 'proposal'
    ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]') : null;
  if (kind === 'proposal' && !proposal) return expected ? false : { ready: false, headers: [], rows: [] };
  const table = kind === 'proposal' ? proposal.querySelector('table')
    : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return expected ? false : { ready: false, headers: [], rows: [] };
  const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const headers = proposal
    ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
    : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
  const rows = proposal
    ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
    : [...table.querySelectorAll('[role="row"]')].slice(1)
      .filter(row => row.querySelector('[role="cell"]'))
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
  if (!expected) return { ready: true, headers, rows };
  const stable = values => values.map(value => JSON.stringify(value)).sort();
  return JSON.stringify(headers) === JSON.stringify(expected.headers) &&
    JSON.stringify(stable(rows)) === JSON.stringify(stable(expected.rows));
}

function readPreviewGrid(page, kind) {
  return page.evaluate(previewGridSnapshot, { kind });
}

function stableRows(rows) {
  return rows.map(row => JSON.stringify(row)).sort();
}

function gridDifference(expected, actual) {
  const expectedRows = stableRows(expected.rows);
  const actualRows = stableRows(actual.rows);
  let rowIndex = 0;
  while (rowIndex < expectedRows.length && expectedRows[rowIndex] === actualRows[rowIndex]) rowIndex += 1;
  const expectedRow = rowIndex < expectedRows.length ? JSON.parse(expectedRows[rowIndex]) : null;
  const actualRow = rowIndex < actualRows.length ? JSON.parse(actualRows[rowIndex]) : null;
  let cellIndex = 0;
  while (expectedRow && actualRow && cellIndex < expectedRow.length && expectedRow[cellIndex] === actualRow[cellIndex]) cellIndex += 1;
  const expectedCell = expectedRow && cellIndex < expectedRow.length ? expectedRow[cellIndex] : null;
  const actualCell = actualRow && cellIndex < actualRow.length ? actualRow[cellIndex] : null;
  return {
    headersMatch: JSON.stringify(expected.headers) === JSON.stringify(actual.headers),
    firstDifferentRow: rowIndex < Math.max(expectedRows.length, actualRows.length) ? rowIndex + 1 : null,
    firstDifferentCell: cellIndex < Math.max(expectedRow?.length ?? 0, actualRow?.length ?? 0) ? cellIndex + 1 : null,
    expectedCell,
    actualCell,
    expectedCellCodePoints: expectedCell === null ? [] : Array.from(expectedCell, character => character.codePointAt(0)),
    actualCellCodePoints: actualCell === null ? [] : Array.from(actualCell, character => character.codePointAt(0)),
  };
}

async function assertGrid(page, kind, expected, phase, onMismatch) {
  try {
    await page.waitForFunction(previewGridSnapshot, { kind, expected }, { timeout: ACTION_BUDGET_MS });
  } catch (error) {
    const actual = await readPreviewGrid(page, kind);
    const diagnostic = { phase, expected, actual, firstDifference: gridDifference(expected, actual) };
    onMismatch?.(diagnostic);
    if (error instanceof Error) error.message = `${error.message}\nGrid diagnostic: ${JSON.stringify(diagnostic)}`;
    throw error;
  }
  const actual = await readPreviewGrid(page, kind);
  assert.deepEqual(actual.headers, expected.headers, `${phase} changed the Group output labels.`);
  assert.deepEqual(stableRows(actual.rows), stableRows(expected.rows), `${phase} differs from the complete selected-root oracle.`);
  return actual;
}

async function waitProposalReady(page) {
  await page.waitForFunction(() =>
    document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready' &&
    document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready',
  [], { timeout: ACTION_BUDGET_MS });
}

async function waitNewProposalReceipt(page, previousReceipt) {
  await page.waitForFunction(previous => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    return preview?.dataset.previewStatus === 'ready' && Boolean(preview.dataset.previewReceiptId) &&
      preview.dataset.previewReceiptId !== previous;
  }, previousReceipt, { timeout: ACTION_BUDGET_MS });
}

export async function semanticGroupConnectedLifecycle({ page, cda }) {
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  assert.equal(project, cda.target.fixtureProject);
  assert.equal(generation, cda.target.fixtureGeneration);
  assert.equal(generation, 'cda-fhir-v1');
  assert(apiOrigin && uiOrigin);
  const arango = validatedArangoContainer(cda.target);
  const database = cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE;
  assert(database, 'The owned CDA target must name its existing Arango database.');

  const report = cda.report;
  report.phase = 'native connected Patient-to-Specimen Coding Group';
  report.scope = {
    project, generation, rootResourceType: 'Patient', relatedResourceType: 'Specimen',
    relationship: 'subject_Patient', storageDirection: 'INBOUND', codingSelector: 'type.coding[]',
    rootDiscoveryLimit: ROOT_DISCOVERY_LIMIT, targetDiscoveryLimit: TARGET_DISCOVERY_LIMIT,
    exactTargetLimitPerSelectedRoot: TARGET_RESULT_LIMIT,
  };
  const discovery = readPatientDiscovery(arango, database, project, generation);
  const discoveryCandidates = discovery.filter(patient => patient.specimenCount > 1 &&
    patient.specimens.some(specimen => Array.isArray(specimen.typeCodings) && specimen.typeCodings.length > 0));
  assert(discoveryCandidates.length >= ROOT_SAMPLE_SIZE,
    `The bounded current CDA witness needs ${ROOT_SAMPLE_SIZE} Patient roots with multiple connected Specimens and populated type.coding[]; saw ${discoveryCandidates.length}.`);
  const chosen = discoveryCandidates.slice(0, ROOT_SAMPLE_SIZE);
  const selectedRootKeys = chosen.map(patient => patient.patientKey);
  const selectedRootIDs = chosen.map(patient => patient.patientID);
  const exactRoots = readExactPatientTargets(arango, database, project, generation, selectedRootKeys);
  assert.equal(exactRoots.length, ROOT_SAMPLE_SIZE, 'The exact selected Patient roots changed during the read-only witness.');
  for (const patient of exactRoots) {
    assert(patient.patientID && patient.patientID.trim());
    assert(patient.specimenCount > 1);
    assert(patient.specimenCount <= TARGET_RESULT_LIMIT,
      `Selected Patient ${patient.patientID} exceeds the exact ${TARGET_RESULT_LIMIT}-target oracle cap.`);
    assert.equal(patient.specimenCount, patient.specimens.length,
      `Selected Patient ${patient.patientID} target results overflowed the read cap.`);
    for (const specimen of patient.specimens) {
      assert(specimen.id && specimen.id.trim());
      assert.equal(specimen.subjectReference, `Patient/${patient.patientID}`,
        `Specimen ${specimen.id} does not carry the exact Patient subject reference.`);
      assert(Array.isArray(specimen.typeCodings), `Specimen ${specimen.id} lacks the raw type.coding array.`);
      assert(specimen.duplicateRouteEdges >= 1);
    }
  }
  const oracle = buildConnectedGroupOracle(exactRoots);
  assert(oracle.sourceRows.length > 0);
  const oracleEvidence = {
    selectedPatientCount: exactRoots.length,
    selectedPatientIDs: selectedRootIDs,
    uniqueConnectedSpecimenCount: exactRoots.reduce((sum, patient) => sum + patient.specimens.length, 0),
    physicalCodingOccurrenceCount: oracle.sourceRows.filter(row => row.ordinal !== null).length,
    groupInputRowsIncludingPreservedEmptyCoding: oracle.sourceRows.length,
    completeTargetMatches: true,
    targetOverflow: false,
    targetLimitPerSelectedRoot: TARGET_RESULT_LIMIT,
    groupedTupleCount: oracle.groups.length,
    codingSelector: 'Specimen.type.coding[]',
    route: 'Patient --subject_Patient / INBOUND--> Specimen',
    digest: digest(oracle.groups.map(group => ({
      patientID: group.patientID, tuple: group.tuple, count: group.count,
      display: group.display, ownerIDs: group.ownerIDs, occurrences: group.occurrences,
    }))),
  };
  report.oracle = oracleEvidence;
  cda.check('correctness',
    'bounded raw current-CDA witness confirms populated Patient.id and all inbound subject_Patient Specimen.type.coding[] rows for two exact Patient roots',
    true, oracleEvidence);

  const api = async (path, body) => {
    const headers = { 'Content-Type': 'application/json', 'X-Request-ID': `semantic-group-connected-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()} (bodySha256=${digest(clean(text))}).`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()} (bodySha256=${digest(value)}).`);
    return value;
  };
  const explorerID = `semantic-group-connected-${randomUUID()}`;
  const explorersPath = `/api/v1/projects/${encode(project)}/explorers`;
  const authoringPath = `${explorersPath}/${encode(explorerID)}/authoring/v2`;
  const pageURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorerID)}&mode=builder`;
  await api(explorersPath, { name: explorerID, title: 'Connected Specimen Coding Group verification' });
  let builder = await api(`${authoringPath}/builder`);
  assert.equal(builder.catalog?.generation, generation);
  const patientNode = builder.catalog?.nodes?.find(node => node.resourceType === 'Patient' && node.rowRootEligible);
  const specimenNode = builder.catalog?.nodes?.find(node => node.resourceType === 'Specimen');
  assert(patientNode?.nodeId && specimenNode?.nodeId,
    'The owned CDA catalog must expose Patient and Specimen source nodes.');
  assert.equal(patientNode.rowRootEligible, true,
    'The current capability snapshot must authorize Patient as this table’s root.');
  const patientToSpecimenEdges = (builder.catalog?.edges ?? []).filter(edge =>
    edge.fromNodeId === patientNode.nodeId && edge.toNodeId === specimenNode.nodeId &&
    edge.label === 'subject_Patient' && edge.storageDirection === 'INBOUND');
  assert.equal(patientToSpecimenEdges.length, 1,
    `The current catalog must expose the exact Patient-to-Specimen inbound subject_Patient edge: ${JSON.stringify((builder.catalog?.edges ?? []).map(edge => ({
      edgeId: edge.edgeId, fromNodeId: edge.fromNodeId, toNodeId: edge.toNodeId, label: edge.label, storageDirection: edge.storageDirection,
    })))}.`);
  const patientToSpecimenEdge = patientToSpecimenEdges[0];
  assert.equal(patientToSpecimenEdge.populated, true,
    'The exact inbound subject_Patient edge must be observed as populated in this capability snapshot.');
  const command = async commands => {
    const response = await api(`${authoringPath}/commands`, {
      commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, commands,
    });
    builder = await api(`${authoringPath}/builder`);
    return response;
  };
  const create = await command([{ type: 'CREATE_TABLE', title: 'Connected Specimen Coding groups', rootNodeId: patientNode.nodeId }]);
  const created = create.results?.find(result => result.type === 'TABLE_CREATED');
  assert(created?.outputId, 'CREATE_TABLE must return the authoritative Patient output ID.');
  const outputId = created.outputId;
  let document = documentFor(builder, outputId);
  assert.equal(document.rootResourceType, 'Patient');
  assert.equal(document.columns.length, 0,
    'The connected Group must be authored before public columns so its sources remain correlated.');
  const selection = await api(`${explorersPath}/${encode(explorerID)}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `semantic-group-connected-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: selectedRootIDs.map(id => ({ project, generation, resourceType: 'Patient', id })) } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Patient');
  assert.equal(selection.memberCount, ROOT_SAMPLE_SIZE);
  const populationRoutes = await api(`${authoringPath}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
  });
  const directRoute = populationRoutes.choices.find(choice => choice.route.length === 0);
  assert(directRoute?.routeChoiceId, 'The exact selected Patient roots must support their direct population route.');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  document = documentFor(builder, outputId);
  assert.equal(document.columns.length, 0);
  const sourceDocumentBeforeGroup = structuredClone(document);
  const patientIDCandidate = builder.catalog.candidates.find(candidate =>
    candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'id' && candidate.populated === true);
  const specimenCodingCodeCandidate = builder.catalog.candidates.find(candidate =>
    candidate.nodeId === specimenNode.nodeId && candidate.fieldPath === 'type.coding[].code' && candidate.populated === true);
  assert(patientIDCandidate?.candidateId, 'The current populated Patient.id candidate must be issued by the CDA catalog.');
  assert(specimenCodingCodeCandidate?.candidateId,
    'The current populated Specimen.type.coding[].code candidate must be issued by the CDA catalog.');

  await cda.navigate(pageURL);
  const tableControl = page.getByTestId(`construction-table-${outputId}`);
  await tableControl.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await tableControl.click({ timeout: ACTION_BUDGET_MS });
  await page.getByText('Add a column to see your table.', { exact: true })
    .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const settings = page.getByTestId('construction-rows-settings-trigger');
  await settings.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  assert.equal(await settings.isEnabled(), true,
    'The zero-column Patient table must expose its ready row-operation control.');
  await settings.click({ timeout: ACTION_BUDGET_MS });
  const groupAction = page.getByTestId('construction-action-group-rows');
  await groupAction.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const groupCapabilitiesPromise = page.waitForResponse(response => {
    try {
      const body = response.request().postDataJSON();
      return response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/construction-capabilities') &&
        body.outputId === outputId && body.stageId === 'source_projection' && !body.groupStepId;
    } catch { return false; }
  }, { timeout: ACTION_BUDGET_MS });
  const groupStarted = Date.now();
  await groupAction.click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId('construction-reshape-group').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const groupCapabilitiesResponse = await groupCapabilitiesPromise;
  assert(groupCapabilitiesResponse.ok(), `Group capabilities returned HTTP ${groupCapabilitiesResponse.status()}.`);
  const groupCapabilities = await groupCapabilitiesResponse.json();
  assert.equal(groupCapabilities.outputId, outputId);
  assert.equal(groupCapabilities.stageId, 'source_projection');
  assert.equal(groupCapabilities.selectedStage?.id, 'source_projection');
  const groupChoices = (groupCapabilities.groupFieldChoices ?? []).filter(choice => choice.kind === 'FIELD_SOURCE' && choice.fieldSource?.kind === 'CHOICE');
  const patientIDChoices = groupChoices.filter(choice => choice.valueKind === 'SCALAR' && choice.sourceRole === 'ROOT' &&
    choice.ownerResourceType === 'Patient' && choice.resourceType === 'Patient' && choice.canonicalPath === 'id' &&
    choice.candidateId === patientIDCandidate.candidateId);
  const specimenCodingChoices = groupChoices.filter(choice => choice.valueKind === 'CODING_IDENTITY' &&
    choice.ownerResourceType === 'Specimen' && choice.resourceType === 'Coding' &&
    choice.canonicalPath === 'type.coding[]');
  assert.equal(patientIDChoices.length, 1,
    `Group field capabilities must offer the current populated Patient.id root scalar: ${JSON.stringify(groupChoices)}.`);
  assert.equal(specimenCodingChoices.length, 1,
    `Group field capabilities must offer exactly the routed Specimen.type.coding[] identity: ${JSON.stringify(groupChoices)}.`);
  const patientIDChoice = patientIDChoices[0];
  const specimenCodingChoice = specimenCodingChoices[0];
  const groupBox = page.getByTestId('construction-reshape-group');
  await groupBox.getByTestId(`construction-group-field-choice-${patientIDChoice.fieldSource.choiceId}`)
    .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  assert(Date.now() - groupStarted <= ACTION_BUDGET_MS,
    'Opening Group and loading its exact source capabilities exceeded five seconds.');
  const codingRoute = relatedFacts(specimenCodingChoice);
  assert(codingRoute?.routeId, 'The Coding Group choice must carry its exact relatedOccurrence.routeId.');
  for (const choice of [patientIDChoice, specimenCodingChoice]) {
    assert.equal(typeof choice.fieldSource.choiceId, 'string');
    assert(choice.fieldSource.choiceId.trim());
    assert.equal(choice.sourceStageId, 'source_projection');
    assert.equal(typeof choice.sourceShapeId, 'string');
    assert(choice.sourceShapeId.trim());
  }
  cda.check('usability',
    'native Group choices are issued for current Patient.id and the exact inbound subject_Patient Specimen.type.coding[] occurrence',
    true, {
      patientIDChoice: { label: patientIDChoice.label, candidateId: patientIDChoice.candidateId, sourceRole: patientIDChoice.sourceRole },
      codingChoice: { label: specimenCodingChoice.label, valueKind: specimenCodingChoice.valueKind,
        ownerResourceType: specimenCodingChoice.ownerResourceType, resourceType: specimenCodingChoice.resourceType,
        canonicalPath: specimenCodingChoice.canonicalPath, routeId: codingRoute.routeId },
      catalogRoute: { edgeId: patientToSpecimenEdge.edgeId, fromNodeId: patientToSpecimenEdge.fromNodeId,
        toNodeId: patientToSpecimenEdge.toNodeId, relationship: patientToSpecimenEdge.label,
        storageDirection: patientToSpecimenEdge.storageDirection, populated: patientToSpecimenEdge.populated },
      oracleDigest: oracleEvidence.digest,
    });

  const receipt = () => page.evaluate(() =>
    document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id') ?? null,
  );
  const groupRequest = async choice => {
    const requestCaptureState = { status: 'pending' };
    const previous = await receipt();
    const requestCapturePromise = page.waitForRequest(request => {
      try {
        if (request.method() !== 'POST' || !new URL(request.url()).pathname.endsWith('/construction-proposals')) return false;
        const body = request.postDataJSON();
        const step = body.candidateConstruction?.steps?.find(candidate => candidate.operation?.kind === 'GROUP');
        return body.outputId === outputId && step?.operation?.group?.keys?.some(key =>
          key.fieldSource?.kind === 'CHOICE' && key.fieldSource.choiceId === choice.fieldSource.choiceId);
      } catch { return false; }
    }, { timeout: ACTION_BUDGET_MS }).then(request => {
      requestCaptureState.status = 'captured';
      return { status: 'captured', request };
    }, error => {
      requestCaptureState.status = 'failed';
      requestCaptureState.error = clean(error instanceof Error ? error.message : error).slice(0, 300);
      return { status: 'failed' };
    });
    const started = Date.now();
    const choiceControl = groupBox.getByTestId(`construction-group-field-choice-${choice.fieldSource.choiceId}`);
    assert.equal(await choiceControl.isChecked(), false,
      `Connected Group choice ${choice.fieldSource.choiceId} must start unselected.`);
    await choiceControl.click({ timeout: ACTION_BUDGET_MS });
    await waitNewProposalReceipt(page, previous);
    const requestCapture = await requestCapturePromise;
    assert.equal(requestCapture.status, 'captured', 'Selecting a connected Group field must issue its exact Group proposal request.');
    assert(Date.now() - started <= ACTION_BUDGET_MS, 'Selecting a connected Group field exceeded five seconds.');
    return requestCapture.request;
  };
  await groupRequest(patientIDChoice);
  const finalGroupRequestPromise = groupRequest(specimenCodingChoice);
  const finalGroupRequest = await finalGroupRequestPromise;
  const candidateBody = finalGroupRequest.postDataJSON();
  const candidateStep = candidateBody.candidateConstruction.steps.find(step => step.operation?.kind === 'GROUP');
  assert(candidateStep, 'The native Group proposal must include one Group step.');
  const candidateKeys = candidateStep.operation.group.keys;
  assert.equal(candidateKeys.length, 2);
  assert(candidateKeys.some(key => key.fieldSource?.choiceId === patientIDChoice.fieldSource.choiceId));
  assert(candidateKeys.some(key => key.fieldSource?.choiceId === specimenCodingChoice.fieldSource.choiceId));
  assert.equal(candidateStep.operation.group.missingKeyPolicy ?? 'GROUP', 'GROUP');
  assert((candidateStep.operation.group.aggregates ?? []).some(aggregate => aggregate.operation === 'COUNT_ROWS'),
    'The native Group must use COUNT_ROWS over every connected Coding ordinal.');
  assert.equal(documentFor(builder, outputId).columns.length, 0,
    'Connected Group keys must be selected before public columns.');
  const initialExpected = expectedGrid(candidateStep, oracle, { patientID: patientIDChoice, coding: specimenCodingChoice });
  await assertGrid(page, 'proposal', initialExpected, 'connected Group Preview');
  await captureBuilderScreenshot({ page, cda, report, name: 'connected-group-picker.png' });
  const groupApplyStarted = Date.now();
  await page.getByTestId('construction-apply-proposal').click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
  assert(Date.now() - groupApplyStarted <= ACTION_BUDGET_MS, 'Applying the connected Group exceeded five seconds.');
  builder = await api(`${authoringPath}/builder`);
  let savedGroup = groupStepFor(builder, outputId);
  assert(savedGroup, 'Applying the connected Group must persist one Group step.');
  const savedPatientKey = savedGroup.operation.group.keys.find(key => key.fieldSource?.valueKind === 'SCALAR' &&
    key.fieldSource?.input?.currentStage?.canonicalPath === 'id');
  const savedCodingKey = savedGroup.operation.group.keys.find(key => key.fieldSource?.valueKind === 'CODING_IDENTITY');
  assertRootBinding(savedPatientKey?.fieldSource, patientIDCandidate.candidateId, patientIDChoice.sourceShapeId);
  const savedCodingRelated = savedCodingKey?.fieldSource?.input?.relatedOccurrence;
  const authorizedRoute = savedCodingRelated?.route;
  exactRouteStep(authorizedRoute);
  assert.equal(savedCodingRelated.anchorResourceType, 'Patient');
  assert.equal(savedCodingRelated.targetResourceType, 'Specimen');
  assert.equal(savedCodingRelated.resourceType, 'Coding');
  assert.equal(savedCodingRelated.codingSelector, 'type.coding[]');
  const routeStep = authorizedRoute[0];
  assert.equal(routeStep.edgeId, patientToSpecimenEdge.edgeId);
  assert.equal(routeStep.fromNodeId, patientNode.nodeId);
  assert.equal(routeStep.toNodeId, specimenNode.nodeId);
  assert.equal(routeStep.fromResourceType, 'Patient');
  assert.equal(routeStep.toResourceType, 'Specimen');
  assert.equal(routeStep.relationship, patientToSpecimenEdge.label);
  assert.equal(routeStep.storageDirection, patientToSpecimenEdge.storageDirection);
  assert.equal(routeStep.matchMode, 'OPTIONAL');
  assertRelatedBinding(savedCodingKey?.fieldSource, {
    valueKind: 'CODING_IDENTITY', expectedRoute: authorizedRoute,
    expectedShapeID: specimenCodingChoice.sourceShapeId,
  });
  assert.equal(documentFor(builder, outputId).columns.length, 0);
  const savedExpected = expectedGrid(savedGroup, oracle, { patientID: patientIDChoice, coding: specimenCodingChoice });
  const savedActual = await assertGrid(page, 'saved', savedExpected, 'saved connected Group');
  cda.check('correctness',
    'connected Group Preview and Apply match every Coding tuple, physical ordinal count, display label, and selected Patient',
    true, {
      sourceRows: oracle.sourceRows.length, groups: savedActual.rows.length,
      totalCountedOccurrences: oracle.groups.reduce((sum, group) => sum + group.count, 0),
      fieldSources: savedGroup.operation.group.keys.map(key => key.fieldSource),
      oracleDigest: oracleEvidence.digest,
    });

  const returnToTableStarted = Date.now();
  const rowSettingsDialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
  await rowSettingsDialog.getByRole('button', { name: 'Back to table', exact: true })
    .click({ timeout: ACTION_BUDGET_MS });
  await rowSettingsDialog.waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
  assert(Date.now() - returnToTableStarted <= ACTION_BUDGET_MS,
    'Returning to the table after applying the connected Group exceeded five seconds.');

  const capabilitiesPromise = page.waitForResponse(response => {
    try {
      const body = response.request().postDataJSON();
      return response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/construction-capabilities') &&
        body.outputId === outputId && body.stageId === 'source_projection' && body.groupStepId === savedGroup.id;
    } catch { return false; }
  }, { timeout: ACTION_BUDGET_MS });
  const addColumnsStarted = Date.now();
  await page.getByTestId('construction-action-add-columns').click({ timeout: ACTION_BUDGET_MS });
  await page.getByRole('region', { name: 'Add columns from Group contributors', exact: true })
    .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const capabilitiesResponse = await capabilitiesPromise;
  assert(Date.now() - addColumnsStarted <= ACTION_BUDGET_MS,
    'Opening active Group Add columns and loading exact contributors exceeded five seconds.');
  assert(capabilitiesResponse.ok(), `Connected Group RowValue capabilities returned HTTP ${capabilitiesResponse.status()}.`);
  const rowCapabilities = await capabilitiesResponse.json();
  assert.equal(rowCapabilities.outputId, outputId);
  assert.equal(rowCapabilities.stageId, 'source_projection');
  assert.equal(capabilitiesResponse.request().postDataJSON().groupStepId, savedGroup.id,
    'The Group contributor capability request must target the exact saved Group input preimage.');
  const specimenIDCandidate = builder.catalog.candidates.find(candidate =>
    candidate.nodeId === specimenNode.nodeId && candidate.fieldPath === 'id' && candidate.populated === true);
  assert(specimenIDCandidate?.candidateId,
    'The connected Specimen owner ID must be issued by the current populated catalog.');
  const specimenOwnerChoices = (rowCapabilities.groupRowValueChoices ?? []).filter(choice =>
    choice.kind === 'FIELD_SOURCE' && choice.valueKind === 'SCALAR' &&
    choice.candidateId === specimenIDCandidate.candidateId && choice.canonicalPath === 'id' &&
    choice.resourceType === 'string' && choice.ownerResourceType === 'Specimen' && choice.fieldSource?.kind === 'CHOICE');
  assert.equal(specimenOwnerChoices.length, 1,
    `The exact active Group preimage must issue one Specimen.id related RowValue choice: ${JSON.stringify(rowCapabilities.groupRowValueChoices)}.`);
  let specimenIDChoice = specimenOwnerChoices[0];
  const rowValueRoute = relatedFacts(specimenIDChoice);
  assert(rowValueRoute?.routeId);
  assert.equal(rowValueRoute.routeId, codingRoute.routeId,
    'The post-Group Specimen.id RowValue must use the same exact related route as the Coding key.');
  assert.equal(specimenIDChoice.inputColumnId, undefined,
    'The not-yet-projected related owner ID must be a capability-bound source, not a preexisting input column.');
  const groupBeforeRowValueProposals = structuredClone(builder);
  const contributorSection = page.getByRole('region', { name: 'Add columns from Group contributors', exact: true });
  let rowValueControl = contributorSection.getByTestId(`construction-group-add-column-choice-${specimenIDChoice.fieldSource.choiceId}`);
  assert.equal(await rowValueControl.getAttribute('aria-label'), `Add column ${specimenIDChoice.label}`);
  const proposeSpecimenIDRowValue = async () => {
    const previousReceipt = await receipt();
    const rowValueRequestPromise = page.waitForRequest(request => {
      try {
        if (request.method() !== 'POST' || !new URL(request.url()).pathname.endsWith('/construction-proposals')) return false;
        const body = request.postDataJSON();
        const step = body.candidateConstruction?.steps?.find(candidate => candidate.id === savedGroup.id);
        return body.outputId === outputId && step?.operation?.kind === 'GROUP' &&
          step.rowValues?.some(value => value.fieldSource?.kind === 'CHOICE' &&
            value.fieldSource.choiceId === specimenIDChoice.fieldSource.choiceId);
      } catch { return false; }
    }, { timeout: ACTION_BUDGET_MS });
    const started = Date.now();
    assert.equal(await rowValueControl.isChecked(), false,
      'The connected Specimen.id RowValue choice must start unselected.');
    await rowValueControl.click({ timeout: ACTION_BUDGET_MS });
    await waitNewProposalReceipt(page, previousReceipt);
    const rowValueRequest = await rowValueRequestPromise;
    const body = rowValueRequest.postDataJSON();
    const step = body.candidateConstruction.steps.find(candidate => candidate.id === savedGroup.id);
    assert.equal(step.rowValues.length, 1);
    const candidate = step.rowValues[0];
    assert.equal(candidate.policy, 'ALL');
    assert.deepEqual(candidate.fieldSource, { kind: 'CHOICE', choiceId: specimenIDChoice.fieldSource.choiceId });
    const expected = expectedGrid(step, oracle, { patientID: patientIDChoice, coding: specimenCodingChoice }, specimenIDChoice);
    await assertGrid(page, 'proposal', expected, 'connected Specimen.id RowValue Preview');
    assert(Date.now() - started <= ACTION_BUDGET_MS,
      'Selecting the exact connected RowValue and rendering its proposal exceeded five seconds.');
    return { body, step, expected };
  };
  const rowValueProposal = await proposeSpecimenIDRowValue();
  const cancelRowValueStarted = Date.now();
  await page.getByTestId('construction-cancel-proposal').click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
  assert(Date.now() - cancelRowValueStarted <= ACTION_BUDGET_MS,
    'Canceling the connected RowValue Preview exceeded five seconds.');
  builder = await api(`${authoringPath}/builder`);
  assert.deepEqual(builder.workspace, groupBeforeRowValueProposals.workspace,
    'Canceling connected Specimen.id Preview must preserve the exact saved Group workspace.');
  assert.equal(builder.draftVersion, groupBeforeRowValueProposals.draftVersion,
    'Canceling connected Specimen.id Preview must preserve the exact draft version.');
  assert.equal(builder.draftDigest, groupBeforeRowValueProposals.draftDigest,
    'Canceling connected Specimen.id Preview must preserve the exact draft digest.');
  savedGroup = groupStepFor(builder, outputId);
  assert.equal(savedGroup.rowValues?.length ?? 0, 0,
    'Canceling connected Specimen.id Preview must not persist a contributor column.');
  await assertGrid(page, 'saved', savedExpected, 'connected Group after Specimen.id Preview Cancel');
  cda.check('persistence',
    'Canceling connected Specimen.id Preview preserves the exact saved Group workspace and draft identity',
    true, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, groupStepID: savedGroup.id });

  const reopenedCapabilitiesPromise = page.waitForResponse(response => {
    try {
      const body = response.request().postDataJSON();
      return response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/construction-capabilities') &&
        body.outputId === outputId && body.stageId === 'source_projection' && body.groupStepId === savedGroup.id;
    } catch { return false; }
  }, { timeout: ACTION_BUDGET_MS });
  const reopenedAddColumnsStarted = Date.now();
  await page.getByTestId('construction-action-add-columns').click({ timeout: ACTION_BUDGET_MS });
  await page.getByRole('region', { name: 'Add columns from Group contributors', exact: true })
    .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const reopenedCapabilitiesResponse = await reopenedCapabilitiesPromise;
  assert(Date.now() - reopenedAddColumnsStarted <= ACTION_BUDGET_MS,
    'Reopening Group Add columns after Cancel exceeded five seconds.');
  assert(reopenedCapabilitiesResponse.ok(),
    `Reopened connected Group RowValue capabilities returned HTTP ${reopenedCapabilitiesResponse.status()}.`);
  const reopenedRowCapabilities = await reopenedCapabilitiesResponse.json();
  assert.equal(reopenedCapabilitiesResponse.request().postDataJSON().groupStepId, savedGroup.id);
  const reopenedSpecimenOwnerChoices = (reopenedRowCapabilities.groupRowValueChoices ?? []).filter(choice =>
    choice.kind === 'FIELD_SOURCE' && choice.valueKind === 'SCALAR' &&
    choice.candidateId === specimenIDCandidate.candidateId && choice.canonicalPath === 'id' &&
    choice.resourceType === 'string' && choice.ownerResourceType === 'Specimen' && choice.fieldSource?.kind === 'CHOICE');
  assert.equal(reopenedSpecimenOwnerChoices.length, 1,
    'Cancel must leave exactly one compiler-issued Specimen.id RowValue choice for the same Group.');
  specimenIDChoice = reopenedSpecimenOwnerChoices[0];
  const reopenedRowValueRoute = relatedFacts(specimenIDChoice);
  assert.equal(reopenedRowValueRoute?.routeId, codingRoute.routeId,
    'Reopened Specimen.id RowValue must retain the exact Coding key route.');
  rowValueControl = page.getByRole('region', { name: 'Add columns from Group contributors', exact: true })
    .getByTestId(`construction-group-add-column-choice-${specimenIDChoice.fieldSource.choiceId}`);
  assert.equal(await rowValueControl.getAttribute('aria-label'), `Add column ${specimenIDChoice.label}`);
  const confirmedRowValueProposal = await proposeSpecimenIDRowValue();
  assert.deepEqual(confirmedRowValueProposal.expected, rowValueProposal.expected,
    'Reopening the RowValue after Cancel must reproduce the same complete owner oracle.');
  const rowValueApplyStarted = Date.now();
  await page.getByTestId('construction-apply-proposal').click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
  assert(Date.now() - rowValueApplyStarted <= ACTION_BUDGET_MS, 'Applying the related Group RowValue exceeded five seconds.');

  builder = await api(`${authoringPath}/builder`);
  savedGroup = groupStepFor(builder, outputId);
  assert.equal(savedGroup.rowValues?.length, 1);
  const savedOwnerValue = savedGroup.rowValues[0];
  assert.equal(savedOwnerValue.policy, 'ALL');
  assertRelatedBinding(savedOwnerValue.fieldSource, {
    valueKind: 'SCALAR', candidateID: specimenIDCandidate.candidateId, expectedRoute: authorizedRoute,
    expectedShapeID: specimenIDChoice.sourceShapeId,
  });
  const savedCodingKeyAfterRowValue = savedGroup.operation.group.keys.find(key => key.fieldSource?.valueKind === 'CODING_IDENTITY');
  assert.deepEqual(relatedRouteBindingIdentity(savedOwnerValue.fieldSource), relatedRouteBindingIdentity(savedCodingKeyAfterRowValue.fieldSource),
    'The related owner RowValue and Coding key must retain the same exact saved route and private occurrence continuity.');
  const finalExpected = expectedGrid(savedGroup, oracle, { patientID: patientIDChoice, coding: specimenCodingChoice }, specimenIDChoice);
  const finalSavedExpected = expectedGrid(savedGroup, oracle,
    { patientID: patientIDChoice, coding: specimenCodingChoice }, specimenIDChoice, { includeArrayIndicators: true });
  let finalActual;
  try {
    finalActual = await assertGrid(page, 'saved', finalSavedExpected,
      'saved connected Group with exact related-owner RowValue', diagnostic => { report.savedGridDiagnostic = diagnostic; });
  } catch (error) {
    try {
      await captureBuilderScreenshot({ page, cda, report, name: 'connected-group-applied-diagnostic.png' });
    } catch (captureError) {
      report.savedGridDiagnosticScreenshotError = clean(captureError instanceof Error ? captureError.message : captureError);
    }
    throw error;
  }
  cda.check('persistence',
    'Add columns rehydrates only the exact connected Specimen.id Group contributors and retains the route-bound source across reload',
    true, {
      rowValueSource: savedOwnerValue.fieldSource,
      routeId: rowValueRoute.routeId,
      sourceCodingRows: oracle.sourceRows.length,
      groupedRows: finalActual.rows.length,
      oracleDigest: oracleEvidence.digest,
    });
  await captureBuilderScreenshot({ page, cda, report, name: 'connected-group-applied.png' });

  const beforeGroupReload = structuredClone(builder);
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible().catch(() => false)) {
    await page.getByTestId(`construction-table-${outputId}`).click({ timeout: ACTION_BUDGET_MS });
  }
  const reloadedActual = await assertGrid(page, 'saved', finalSavedExpected, 'reloaded connected Group RowValue');
  builder = await api(`${authoringPath}/builder`);
  assert.equal(builder.draftDigest, beforeGroupReload.draftDigest,
    'Reload must preserve the exact connected Group RowValue draft digest.');
  assert.deepEqual(builder.workspace, beforeGroupReload.workspace,
    'Reload must preserve the exact connected Group RowValue workspace.');
  savedGroup = groupStepFor(builder, outputId);
  assert(savedGroup?.rowValues?.length === 1,
    'Reload must retain the exact related-owner Group RowValue.');
  cda.check('persistence',
    'reloaded native connected Group preserves the exact root key, related Coding tuple counts, and related-owner values',
    true, { groupedRows: reloadedActual.rows.length, sourceCodingRows: oracle.sourceRows.length, oracleDigest: oracleEvidence.digest });

  const beforeGroupEdit = structuredClone(builder);
  const reloadedSavedGroup = groupStepFor(builder, outputId);
  assert(reloadedSavedGroup, 'The reloaded connected Group must remain editable.');
  const editCapabilitiesPromise = page.waitForResponse(response => {
    try {
      const body = response.request().postDataJSON();
      return response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/construction-capabilities') &&
        body.outputId === outputId && body.stageId === 'source_projection' && body.groupStepId === reloadedSavedGroup.id;
    } catch { return false; }
  }, { timeout: ACTION_BUDGET_MS });
  const editStarted = Date.now();
  await page.getByTestId(`construction-history-step-${reloadedSavedGroup.id}`).click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId(`construction-edit-step-${reloadedSavedGroup.id}`).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await page.getByTestId(`construction-edit-step-${reloadedSavedGroup.id}`).click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId('construction-reshape-group').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const editCapabilitiesResponse = await editCapabilitiesPromise;
  assert(Date.now() - editStarted <= ACTION_BUDGET_MS,
    'Opening the saved connected Group editor and resolving its selected choices exceeded five seconds.');
  assert(editCapabilitiesResponse.ok(),
    `Saved connected Group capabilities returned HTTP ${editCapabilitiesResponse.status()}.`);
  const editCapabilities = await editCapabilitiesResponse.json();
  assert.equal(editCapabilities.outputId, outputId);
  assert.equal(editCapabilities.stageId, 'source_projection');
  assert.equal(editCapabilitiesResponse.request().postDataJSON().groupStepId, reloadedSavedGroup.id);
  const savedKeys = reloadedSavedGroup.operation.group.keys;
  assert.equal(savedKeys.length, 2);
  const matchedChoices = savedKeys.map(key => {
    const matches = (editCapabilities.groupFieldChoices ?? []).filter(choice =>
      choice.kind === 'FIELD_SOURCE' && choice.matchedOutputKeyId === key.outputColumnId &&
      choice.valueKind === key.fieldSource?.valueKind);
    assert.equal(matches.length, 1,
      `Saved Group output ${key.outputColumnId} must resolve to exactly one compiler capability through matchedOutputKeyId.`);
    const [choice] = matches;
    assert.equal(choice.fieldSource?.kind, 'CHOICE',
      `The matched capability for saved Group output ${key.outputColumnId} must remain a selectable CHOICE identity.`);
    if (key.fieldSource?.valueKind === 'CODING_IDENTITY') {
      assert.equal(relatedFacts(choice)?.routeId, codingRoute.routeId,
        'The saved Coding binding must resolve to the same exact related route during edit.');
    }
    return { key, choice };
  });
  assert.equal(new Set(matchedChoices.map(item => item.choice.fieldSource.choiceId)).size, savedKeys.length,
    'Every saved Group key must rehydrate from a distinct matched output capability.');
  const editGroupBox = page.getByTestId('construction-reshape-group');
  const checkedFieldSourceOptions = editGroupBox.locator('[data-testid="construction-group-field-source-option"] input[type="checkbox"]:checked');
  assert.equal(await checkedFieldSourceOptions.count(), savedKeys.length,
    'Editing the saved Group must render one checked binding per exact saved key.');
  const selectedBindingLabels = await checkedFieldSourceOptions.evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label')));
  for (const { key, choice } of matchedChoices) {
    const duplicateChoice = page.getByTestId(`construction-group-field-choice-${choice.fieldSource.choiceId}`);
    assert.equal(await duplicateChoice.count(), 0,
      `Saved Group output ${key.outputColumnId} must not also appear as a duplicate selectable CHOICE.`);
    assert(selectedBindingLabels.some(label => label?.startsWith(`Group by ${choice.label}`)),
      `The selected saved binding for ${choice.label} must remain checked in the editor.`);
  }
  const savedRowValues = reloadedSavedGroup.rowValues ?? [];
  assert.equal(savedRowValues.length, 1, 'The saved Group must retain its exact related-owner RowValue before editing.');
  const savedRowValue = savedRowValues[0];
  const rowValueChoices = (editCapabilities.groupRowValueChoices ?? []).filter(choice =>
    relatedRowValueChoiceMatchesBinding(choice, savedRowValue.fieldSource));
  assert.equal(rowValueChoices.length, 1,
    'The saved related-owner RowValue must rehydrate from exactly one compiler-issued capability.');
  const [editRowValueChoice] = rowValueChoices;
  assert.equal(relatedFacts(editRowValueChoice)?.routeId, codingRoute.routeId,
    'The saved RowValue capability must retain the exact Group Coding route.');
  const rowValueOutput = reloadedSavedGroup.outputs.find(output => output.id === savedRowValue.outputColumnId);
  assert(rowValueOutput, 'The saved Group must expose the exact RowValue output column.');
  const editRowValueRows = editGroupBox.getByTestId('construction-group-row-value');
  assert.equal(await editRowValueRows.count(), savedRowValues.length,
    'Editing the saved Group must render exactly its saved RowValue contributor.');
  const editRowValueRow = editRowValueRows.first();
  const rowValuePresentation = await editRowValueRow.locator(':scope > div').first().innerText();
  assert.equal(clean(rowValuePresentation), clean(`${editRowValueChoice.label} ${editRowValueChoice.scopeLabel}`),
    'The saved RowValue editor row must display the exact capability-issued field and related route.');
  assert.equal(await editRowValueRow.getByRole('combobox', { name: 'Keep values 1', exact: true }).inputValue(), savedRowValue.policy,
    'Editing the saved Group must preserve the RowValue ALL policy.');
  assert.equal(await editRowValueRow.getByRole('textbox', { name: 'Added column name 1', exact: true }).inputValue(), rowValueOutput.name,
    'Editing the saved Group must preserve the RowValue output name.');
  assert.equal(await editGroupBox.getByTestId(`construction-group-add-column-choice-${editRowValueChoice.fieldSource.choiceId}`).count(), 0,
    'The selected RowValue must not also appear as a duplicate selectable capability.');

  const savedCountAggregate = reloadedSavedGroup.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
  assert(savedCountAggregate, 'The saved connected Group must retain its COUNT_ROWS summary.');
  const savedCountOutput = reloadedSavedGroup.outputs.find(output => output.id === savedCountAggregate.outputColumnId);
  assert(savedCountOutput, 'The saved connected Group must expose its exact COUNT_ROWS output.');
  const editedCountName = `${savedCountOutput.name}_edit`;
  assert(!reloadedSavedGroup.outputs.some(output => output.name === editedCountName),
    'The visible summary-name edit must remain unique within the saved Group.');
  const previousEditReceipt = await page.getByTestId('construction-preview').getAttribute('data-preview-receipt-id');
  assert(previousEditReceipt, 'The saved Group edit must begin from its current applied preview receipt.');
  const editProposalRequestPromise = page.waitForRequest(request => {
    try {
      if (request.method() !== 'POST' || !new URL(request.url()).pathname.endsWith('/construction-proposals')) return false;
      const body = request.postDataJSON();
      const step = body.candidateConstruction?.steps?.find(candidate => candidate.id === reloadedSavedGroup.id);
      return body.outputId === outputId && step?.operation?.kind === 'GROUP';
    } catch { return false; }
  }, { timeout: ACTION_BUDGET_MS }).then(request => ({ request }), error => ({ error }));
  const editPreviewStarted = Date.now();
  await editGroupBox.getByRole('textbox', { name: 'Summary output name 1', exact: true })
    .fill(editedCountName, { timeout: ACTION_BUDGET_MS });
  const [editProposalResult] = await Promise.all([
    editProposalRequestPromise,
    waitNewProposalReceipt(page, previousEditReceipt),
    waitProposalReady(page),
  ]);
  assert(!editProposalResult.error, `Editing the saved connected Group did not issue a proposal: ${editProposalResult.error ?? ''}`);
  assert(Date.now() - editPreviewStarted <= ACTION_BUDGET_MS,
    'Changing the saved connected Group summary name and resolving its proposal exceeded five seconds.');
  const editProposalRequest = editProposalResult.request;
  assert(editProposalRequest, 'The visible saved Group summary-name change must issue one proposal request.');
  const editCandidate = editProposalRequest.postDataJSON().candidateConstruction.steps.find(step => step.id === reloadedSavedGroup.id);
  assert(editCandidate, 'The saved Group summary-name proposal must contain the exact Group step.');
  assert.deepEqual(editCandidate.operation.group.keys, savedKeys,
    'The saved Group edit proposal must retain the exact selected BINDING keys and output IDs.');
  assert.deepEqual(editCandidate.operation.group.aggregates, reloadedSavedGroup.operation.group.aggregates,
    'The saved Group summary-name edit must preserve its exact aggregation semantics.');
  assert.deepEqual(editCandidate.rowValues, savedRowValues,
    'The saved Group edit proposal must retain the exact related-owner RowValue binding and policy.');
  const expectedCandidateOutputs = reloadedSavedGroup.outputs.map(output => output.id === savedCountOutput.id
    ? { ...output, name: editedCountName }
    : output);
  assert.deepEqual(editCandidate.outputs, expectedCandidateOutputs,
    'The saved Group proposal may change only the visible COUNT_ROWS output name.');
  const editExpected = expectedGrid(editCandidate, oracle,
    { patientID: patientIDChoice, coding: specimenCodingChoice }, specimenIDChoice);
  assert.deepEqual(editExpected.rows, finalExpected.rows,
    'Changing the summary output name must not change the complete Group and related-owner data oracle.');
  assert.deepEqual(editExpected.headers, finalExpected.headers,
    'The proposal headers must be derived from the returned candidate output labels.');
  await assertGrid(page, 'proposal', editExpected, 'saved connected Group edit rehydration and summary-name preview');
  cda.check('correctness',
    'saved connected Group edit rehydrates its exact keys and RowValue, previews a summary-name edit, and keeps the full oracle',
    true, {
      matched: matchedChoices.map(({ key, choice }) => ({ outputColumnId: key.outputColumnId, choiceId: choice.fieldSource.choiceId,
        matchedOutputKeyId: choice.matchedOutputKeyId, valueKind: choice.valueKind })),
      selectedBindingLabels,
      rowValueChoiceId: editRowValueChoice.fieldSource.choiceId,
      rowValueOutputColumnId: savedRowValue.outputColumnId,
      editedCountName,
      previewHeaders: editExpected.headers,
    });

  const cancelEditStarted = Date.now();
  await page.getByTestId('construction-cancel-proposal').click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
  assert(Date.now() - cancelEditStarted <= ACTION_BUDGET_MS,
    'Canceling the saved connected Group edit exceeded five seconds.');
  builder = await api(`${authoringPath}/builder`);
  assert.deepEqual(builder.workspace, beforeGroupEdit.workspace,
    'Canceling the saved connected Group edit must preserve the exact workspace.');
  assert.equal(builder.draftVersion, beforeGroupEdit.draftVersion,
    'Canceling the saved connected Group edit must preserve the draft version.');
  assert.equal(builder.draftDigest, beforeGroupEdit.draftDigest,
    'Canceling the saved connected Group edit must preserve the draft digest.');
  const groupAfterEditCancel = groupStepFor(builder, outputId);
  assert.deepEqual(groupAfterEditCancel, reloadedSavedGroup,
    'Canceling the saved Group edit must leave the exact Group keys and RowValue binding unchanged.');
  await assertGrid(page, 'saved', finalSavedExpected, 'connected Group after edit Cancel');
  cda.check('persistence',
    'Canceling connected Group edit preserves the saved exact route, Coding keys, contributor values, and draft identity',
    true, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, groupStepID: groupAfterEditCancel.id });

  const removalRequestPromise = page.waitForRequest(request => {
    try {
      if (request.method() !== 'POST' || !new URL(request.url()).pathname.endsWith('/construction-proposals')) return false;
      const body = request.postDataJSON();
      return body.outputId === outputId && Array.isArray(body.candidateConstruction?.steps) &&
        !body.candidateConstruction.steps.some(step => step.id === groupAfterEditCancel.id);
    } catch { return false; }
  }, { timeout: ACTION_BUDGET_MS });
  const previousRemovalReceipt = await receipt();
  const removeGroupStarted = Date.now();
  await page.getByTestId(`construction-history-step-${groupAfterEditCancel.id}`).click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId(`construction-remove-step-${groupAfterEditCancel.id}`).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await page.getByTestId(`construction-remove-step-${groupAfterEditCancel.id}`).click({ timeout: ACTION_BUDGET_MS });
  await waitNewProposalReceipt(page, previousRemovalReceipt);
  const removalRequest = await removalRequestPromise;
  await waitProposalReady(page);
  assert(Date.now() - removeGroupStarted <= ACTION_BUDGET_MS,
    'Previewing connected Group removal and restoring the source rows exceeded five seconds.');
  const removalCandidate = removalRequest.postDataJSON().candidateConstruction;
  assert.equal(removalCandidate.steps.length, 0,
    'The Group removal proposal must remove the Group and every private preparation step.');
  const removalReceipt = await receipt();
  assert(removalReceipt, 'The Group removal proposal must expose its exact source-preview receipt.');
  const removalPreview = await api(`${authoringPath}/preview`, { receiptId: removalReceipt, outputId, limit: ROOT_SAMPLE_SIZE + 1 });
  assert.equal(removalPreview.receiptId, removalReceipt);
  assert.equal(removalPreview.outputId, outputId);
  assert.equal(removalPreview.rowCount, ROOT_SAMPLE_SIZE,
    'Removing Group must restore one source row per selected Patient root.');
  assert.equal(removalPreview.sampled, false, 'The restored source preview must be complete for the two selected roots.');
  assert.equal(removalPreview.columns.length, sourceDocumentBeforeGroup.columns.length,
    'Group removal Preview must restore the exact pre-Group public column count.');
  assert.equal(removalPreview.rowSources.length, ROOT_SAMPLE_SIZE);
  const restoredPreviewRoots = removalPreview.rowSources.map(source => {
    assert.equal(source.kind, 'SINGLE');
    assert.equal(source.resourceType, 'Patient');
    assert(source.id);
    return source.id;
  }).sort();
  assert.deepEqual(restoredPreviewRoots, [...selectedRootIDs].sort(),
    'Group removal Preview must restore the exact selected Patient row grain.');
  const removeApplyStarted = Date.now();
  await page.getByTestId('construction-apply-proposal').click({ timeout: ACTION_BUDGET_MS });
  await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
  assert(Date.now() - removeApplyStarted <= ACTION_BUDGET_MS,
    'Applying connected Group removal exceeded five seconds.');
  builder = await api(`${authoringPath}/builder`);
  const restoredDocument = documentFor(builder, outputId);
  assert.equal(restoredDocument.rootResourceType, sourceDocumentBeforeGroup.rootResourceType);
  assert.deepEqual(restoredDocument.columns, sourceDocumentBeforeGroup.columns,
    'Group removal must restore the exact pre-Group public column schema.');
  assert.deepEqual(restoredDocument.population, sourceDocumentBeforeGroup.population,
    'Group removal must restore the exact selected-root population and route.');
  assert.equal(restoredDocument.construction?.steps?.length ?? 0, 0,
    'Group removal must leave no Group or private preparation step.');
  cda.check('correctness',
    'removing connected Group restores exact Patient source grain, prior columns, population, and no private preparation step',
    true, { restoredRootResourceType: restoredDocument.rootResourceType, rowCount: removalPreview.rowCount,
      rowSources: restoredPreviewRoots, columns: restoredDocument.columns, constructionSteps: restoredDocument.construction?.steps?.length ?? 0 });

  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible().catch(() => false)) {
    await page.getByTestId(`construction-table-${outputId}`).click({ timeout: ACTION_BUDGET_MS });
  }
  builder = await api(`${authoringPath}/builder`);
  const restoredAfterReload = documentFor(builder, outputId);
  assert.equal(restoredAfterReload.rootResourceType, 'Patient');
  assert.deepEqual(restoredAfterReload.columns, sourceDocumentBeforeGroup.columns);
  assert.deepEqual(restoredAfterReload.population, sourceDocumentBeforeGroup.population);
  assert.equal(restoredAfterReload.population.selectionRevisionId, selection.id,
    'Reload must preserve the exact selected Patient root set after Group removal.');
  assert.deepEqual(restoredAfterReload.population.route, [],
    'Reload must preserve the direct Patient root route after Group removal.');
  assert.equal(restoredAfterReload.construction?.steps?.length ?? 0, 0);
  await page.getByText('Add a column to see your table.', { exact: true })
    .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  assert.equal(await page.locator('[data-testid="preview-table-scroll"] [role="table"]').count(), 0,
    'Reloaded source-only table must remain in its empty-column state without a preview grid.');
  const restoredAddColumns = page.getByTestId('construction-action-add-columns');
  assert.equal(await restoredAddColumns.isVisible(), true,
    'Reloaded Patient source table must keep its row settings available.');
  assert.equal(await restoredAddColumns.isEnabled(), true,
    'Reloaded Patient source table must keep Add columns enabled.');
  cda.check('persistence',
    'reloaded Group removal preserves the exact Patient root selection, public columns, empty construction, and empty table state',
    true, { rootResourceType: restoredAfterReload.rootResourceType,
      selectionRevisionId: restoredAfterReload.population.selectionRevisionId,
      sourceRootIDs: restoredPreviewRoots, columns: restoredAfterReload.columns,
      constructionSteps: restoredAfterReload.construction?.steps?.length ?? 0,
      emptyTablePrompt: 'Add a column to see your table.' });
}
