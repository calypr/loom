import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchCdaBrowser, navigate, selectOption, waitForBrowser, fill } from './lib/playwright-cda-actions.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const resourceType = 'Specimen';
const patientType = 'Patient';
const observationType = 'Observation';
const relatedForm = process.env.LOOM_CDA_NAMED_COHORT_FORM ?? 'COUNT';
assert(['COUNT', 'ALL'].includes(relatedForm), 'LOOM_CDA_NAMED_COHORT_FORM must be COUNT or ALL');
const relatedFormName = relatedForm.toLowerCase();
const explorer = `named-cohort-related-${relatedFormName}-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/${explorer}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const witnessPatientLimit = 2000;
const observationDocumentCap = 11;
const maxExactObservations = observationDocumentCap - 1;
const groupLabel = 'Two sibling Specimens';
const includeEmptyGroup = process.env.LOOM_CDA_NAMED_COHORT_EMPTY_GROUP === '1';
const emptyGroupId = 'qa-empty-declared-group';
const emptyGroupLabel = 'Empty declared group';
const routeLabel = 'Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const selections = base.replace('/authoring/v2', '/selections');
const report = {
  project, generation, resourceType, explorer, protectedExplorer, protectedExplorerUntouched: true, groupLabel, routeLabel,
  mode: includeEmptyGroup ? 'empty-declared-group' : 'nonempty-only', relatedForm,
  witnessBounds: { scopedSpecimens: witnessPatientLimit, distinctObservationDocuments: observationDocumentCap, maxSelectedObservationDocuments: maxExactObservations },
  cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString(),
};
await mkdir(evidence, { recursive: true });

let browser;
let nativeCapture;
let builder;
let outputId;
let frozenSource;
let frozenApiBuild;

const pause = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const pathOf = entry => entry.path.split('?')[0];
const doc = state => state.workspace.documents.find(document => document.output.id === outputId);
const sorted = values => [...values].sort();
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
assert.equal(project, 'loom_dev_cda_fhir', 'This verifier is bound to the CDA-FHIR project');
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot, arangoContainer });
const apiBuildTarget = 'local-cda-api';
const readApiBuildStamp = () => checkContainerApiBuildStamp(apiContainer);

class UnsupportedCapabilityError extends Error {
  constructor(message, evidenceValue) {
    super(message);
    this.name = 'UnsupportedCapabilityError';
    this.evidence = evidenceValue;
  }
}

class BoundedAbsenceError extends Error {
  constructor(message, evidenceValue) {
    super(message);
    this.name = 'BoundedAbsenceError';
    this.evidence = evidenceValue;
  }
}

const record = (name, startedAt, evidenceValue) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, ...(evidenceValue ? { evidence: evidenceValue } : {}) });
};

const api = async (path, body) => {
  const startedAt = Date.now();
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `named-cohort-related-${relatedFormName}-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({
    path, method: body ? 'POST' : 'GET', body, startedAt, completedAt: Date.now(), status: response.status,
    response: path.endsWith('/builder') ? {
      draftVersion: value.draftVersion,
      draftDigest: value.draftDigest,
      catalog: { generation: value.catalog?.generation, authorizationScopeDigest: value.catalog?.authorizationScopeDigest },
      workspace: value.workspace,
    } : value,
  });
  assert(response.ok, JSON.stringify(value));
  return value;
};

const command = async commands => {
  const before = builder;
  await api(base + '/commands', {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, before.catalog.generation);
};

const rawQuery = query => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr);
  const opening = result.stdout.indexOf('[');
  assert(opening >= 0, `Arango did not return a JSON array: ${result.stdout.slice(0, 500)}`);
  return JSON.parse(result.stdout.slice(opening));
};

const sourceWitnessQuery = `
LET scopedSpecimens = (
  FOR specimen IN Specimen
    FILTER specimen.project == ${JSON.stringify(project)}
      AND specimen.dataset_generation == ${JSON.stringify(generation)}
      AND specimen.resourceType == ${JSON.stringify(resourceType)}
    SORT specimen._key
    LIMIT ${witnessPatientLimit}
    RETURN specimen
)
FOR seed IN scopedSpecimens
  FOR patientEdge IN fhir_edge
    FILTER patientEdge._from == seed._id
      AND patientEdge.label == "subject_Patient"
      AND patientEdge.project == ${JSON.stringify(project)}
      AND patientEdge.dataset_generation == ${JSON.stringify(generation)}
      AND STARTS_WITH(patientEdge._to, "Patient/")
    LET patient = DOCUMENT(patientEdge._to)
    FILTER patient != null
      AND patient.project == ${JSON.stringify(project)}
      AND patient.dataset_generation == ${JSON.stringify(generation)}
      AND patient.resourceType == ${JSON.stringify(patientType)}
    LET members = (
      FOR siblingEdge IN fhir_edge
        FILTER siblingEdge._to == patient._id
          AND siblingEdge.label == "subject_Patient"
          AND siblingEdge.project == ${JSON.stringify(project)}
          AND siblingEdge.dataset_generation == ${JSON.stringify(generation)}
          AND STARTS_WITH(siblingEdge._from, "Specimen/")
        LET member = DOCUMENT(siblingEdge._from)
        FILTER member != null
          AND member.project == ${JSON.stringify(project)}
          AND member.dataset_generation == ${JSON.stringify(generation)}
          AND member.resourceType == ${JSON.stringify(resourceType)}
        COLLECT memberKey = member._key INTO memberDocs = member
        SORT memberKey
        LIMIT 2
        LET selected = FIRST(memberDocs)
        RETURN { id: selected.id, _id: selected._id, resourceType: selected.resourceType, project: selected.project, generation: selected.dataset_generation }
    )
    FILTER LENGTH(members) == 2
    LET observations = (
      FOR observationEdge IN fhir_edge
        FILTER observationEdge._to == patient._id
          AND observationEdge.label == "subject_Patient"
          AND observationEdge.project == ${JSON.stringify(project)}
          AND observationEdge.dataset_generation == ${JSON.stringify(generation)}
          AND STARTS_WITH(observationEdge._from, "Observation/")
        LET observation = DOCUMENT(observationEdge._from)
        FILTER observation != null
          AND observation.project == ${JSON.stringify(project)}
          AND observation.dataset_generation == ${JSON.stringify(generation)}
          AND observation.resourceType == ${JSON.stringify(observationType)}
        COLLECT observationKey = observation._key INTO observationDocs = observation
        SORT observationKey
        LIMIT ${observationDocumentCap}
        LET selected = FIRST(observationDocs)
        RETURN { id: selected.id, _id: selected._id, resourceType: selected.resourceType, project: selected.project, generation: selected.dataset_generation }
    )
    FILTER LENGTH(observations) >= 1 AND LENGTH(observations) <= ${maxExactObservations}
    SORT patient._key
    LIMIT 1
    RETURN {
      patient: { id: patient.id, _id: patient._id, resourceType: patient.resourceType, project: patient.project, generation: patient.dataset_generation },
      members,
      observations,
    }
`;

const exactObservationOracle = memberIDs => {
  const query = `
LET selectedSpecimens = (
  FOR specimen IN Specimen
    FILTER specimen.id IN ${JSON.stringify(memberIDs)}
      AND specimen.project == ${JSON.stringify(project)}
      AND specimen.dataset_generation == ${JSON.stringify(generation)}
      AND specimen.resourceType == ${JSON.stringify(resourceType)}
    RETURN specimen
)
FOR specimen IN selectedSpecimens
  FOR patientEdge IN fhir_edge
    FILTER patientEdge._from == specimen._id
      AND patientEdge.label == "subject_Patient"
      AND patientEdge.project == specimen.project
      AND patientEdge.dataset_generation == specimen.dataset_generation
      AND STARTS_WITH(patientEdge._to, "Patient/")
    LET patient = DOCUMENT(patientEdge._to)
    FILTER patient != null
      AND patient.project == specimen.project
      AND patient.dataset_generation == specimen.dataset_generation
      AND patient.resourceType == ${JSON.stringify(patientType)}
    FOR observationEdge IN fhir_edge
      FILTER observationEdge._to == patient._id
        AND observationEdge.label == "subject_Patient"
        AND observationEdge.project == patient.project
        AND observationEdge.dataset_generation == patient.dataset_generation
        AND STARTS_WITH(observationEdge._from, "Observation/")
      LET observation = DOCUMENT(observationEdge._from)
      FILTER observation != null
        AND observation.project == patient.project
        AND observation.dataset_generation == patient.dataset_generation
        AND observation.resourceType == ${JSON.stringify(observationType)}
      COLLECT observationKey = observation._key INTO observationDocs = observation
      SORT observationKey
      LIMIT ${observationDocumentCap}
      LET selected = FIRST(observationDocs)
      RETURN { id: selected.id, _id: selected._id, resourceType: selected.resourceType, project: selected.project, generation: selected.dataset_generation }
`;
  return { query, observations: rawQuery(query) };
};

const exactMemberObservationOracle = memberID => {
  const query = `
FOR specimen IN Specimen
  FILTER specimen.id == ${JSON.stringify(memberID)}
    AND specimen.project == ${JSON.stringify(project)}
    AND specimen.dataset_generation == ${JSON.stringify(generation)}
    AND specimen.resourceType == ${JSON.stringify(resourceType)}
  FOR patientEdge IN fhir_edge
    FILTER patientEdge._from == specimen._id
      AND patientEdge.label == "subject_Patient"
      AND patientEdge.project == specimen.project
      AND patientEdge.dataset_generation == specimen.dataset_generation
      AND STARTS_WITH(patientEdge._to, "Patient/")
    LET patient = DOCUMENT(patientEdge._to)
    FILTER patient != null
      AND patient.project == specimen.project
      AND patient.dataset_generation == specimen.dataset_generation
      AND patient.resourceType == ${JSON.stringify(patientType)}
    FOR observationEdge IN fhir_edge
      FILTER observationEdge._to == patient._id
        AND observationEdge.label == "subject_Patient"
        AND observationEdge.project == patient.project
        AND observationEdge.dataset_generation == patient.dataset_generation
        AND STARTS_WITH(observationEdge._from, "Observation/")
      LET observation = DOCUMENT(observationEdge._from)
      FILTER observation != null
        AND observation.project == patient.project
        AND observation.dataset_generation == patient.dataset_generation
        AND observation.resourceType == ${JSON.stringify(observationType)}
      COLLECT observationKey = observation._key INTO observationDocs = observation
      SORT observationKey
      LIMIT ${observationDocumentCap}
      LET selected = FIRST(observationDocs)
      RETURN { id: selected.id, _id: selected._id, project: selected.project, generation: selected.dataset_generation }
`;
  return { query, observations: rawQuery(query) };
};

const rawExplicitGroupRosterOracle = revisionId => {
  const query = `
LET definitions = (
  FOR group IN loom_explorer_explicit_group_definitions
    FILTER group.revisionId == ${JSON.stringify(revisionId)}
      AND group.project == ${JSON.stringify(project)}
    SORT group.ordinal, group.groupId
    RETURN { id: group.groupId, label: group.label, ordinal: group.ordinal }
)
LET memberships = (
  FOR member IN loom_explorer_explicit_group_memberships
    FILTER member.revisionId == ${JSON.stringify(revisionId)}
      AND member.project == ${JSON.stringify(project)}
      AND member.generation == ${JSON.stringify(generation)}
      AND member.resourceType == ${JSON.stringify(resourceType)}
    SORT member.groupId, member.id
    RETURN { groupId: member.groupId, id: member.id, project: member.project, generation: member.generation, resourceType: member.resourceType }
)
RETURN { definitions, memberships }
`;
  const [roster] = rawQuery(query);
  assert(roster, 'Raw explicit-group storage did not return a group roster');
  return { query, ...roster };
};

const expectedRelatedValue = group => relatedForm === 'COUNT'
  ? group.expectedObservationCount
  : group.observations.slice().sort((left, right) => left._id.localeCompare(right._id)).map(observation => observation.id);

const assertNamedGroupRows = (view, expectedGroupCounts, relatedLabel, form = relatedForm) => {
  const headerIndex = label => view.headers.findIndex(header => header.trim().toLowerCase() === label);
  const labelIndex = headerIndex('group label');
  const ordinalIndex = headerIndex('group ordinal');
  const membersIndex = headerIndex('members');
  const relatedIndex = relatedLabel === undefined
    ? -1
    : view.headers.findIndex(header => header.trim().toLowerCase() === relatedLabel.toLowerCase());
  assert(labelIndex >= 0, `Group label is missing from headers: ${JSON.stringify(view.headers)}`);
  assert(ordinalIndex >= 0, `Group ordinal is missing from headers: ${JSON.stringify(view.headers)}`);
  assert(membersIndex >= 0, `Group Members is missing from headers: ${JSON.stringify(view.headers)}`);
  if (relatedLabel !== undefined) assert(relatedIndex >= 0, `Related output header ${relatedLabel} is missing: ${JSON.stringify(view.headers)}`);
  assert.equal(view.rows.length, expectedGroupCounts.length, 'Rendered output must include each declared named group exactly once');

  const actual = new Map();
  for (const row of view.rows) {
    const cells = row.map(cell => typeof cell === 'string' ? cell : cell?.text ?? '');
    const identity = `${cells[labelIndex]}\u0000${cells[ordinalIndex]}`;
    assert(!actual.has(identity), `Rendered named group is duplicated: ${identity}`);
    actual.set(identity, cells);
  }
  for (const group of expectedGroupCounts) {
    const identity = `${group.label}\u0000${group.ordinal}`;
    const row = actual.get(identity);
    assert(row, `Declared group ${group.label} (ordinal ${group.ordinal}) is missing from the rendered output`);
    const memberText = row[membersIndex];
    if (group.memberIDs.length === 0) {
      for (const id of report.oracle.memberIDs) assert(!memberText.includes(id), `Empty group ${group.label} unexpectedly contains source member ${id}`);
    } else {
      for (const id of group.memberIDs) assert(memberText.includes(id), `Group ${group.label} lost exact raw member ${id}`);
    }
    if (relatedIndex >= 0) {
      if (form === 'COUNT') {
        assert.equal(row[relatedIndex], String(group.expectedObservationCount), `Related COUNT must match the independent raw oracle for ${group.label}`);
      } else {
        const expectedIDs = expectedRelatedValue(group);
        const expectedText = expectedIDs.length === 0 ? '—' : expectedIDs.join('; ');
        assert.equal(row[relatedIndex], expectedText,
          `Formatted related ALL cell must contain exactly the independently ordered IDs for ${group.label}`);
      }
    }
  }
  return [...actual.values()];
};

const assertTypedNamedGroupRows = (rows, expectedGroupCounts, revisionId, relatedColumnName, form = relatedForm) => {
  assert(Array.isArray(rows), 'Native Preview must expose typed rows');
  assert.equal(rows.length, expectedGroupCounts.length, 'Native Preview must return every declared named group exactly once');
  const actual = new Map();
  for (const row of rows) {
    assert(row && typeof row === 'object', 'Every native preview row must be an object');
    const identity = `${row.group_label}\u0000${row.group_ordinal}`;
    assert(!actual.has(identity), `Native Preview duplicated named group ${identity}`);
    actual.set(identity, row);
  }
  const evidenceRows = [];
  for (const group of expectedGroupCounts) {
    const identity = `${group.label}\u0000${group.ordinal}`;
    const row = actual.get(identity);
    assert(row, `Native Preview omitted declared group ${group.label} (ordinal ${group.ordinal})`);
    assert.equal(row.group_id, group.id, `Native Preview group ID changed for ${group.label}`);
    assert.equal(row.group_label, group.label);
    assert.equal(row.group_ordinal, group.ordinal);
    assert.deepEqual(row.__loom_row_id, { group_id: group.id, group_revision_id: revisionId },
      `Native Preview row identity must bind ${group.label} to the exact group revision`);
    assert(Array.isArray(row.members), `Native Preview must return typed Members for ${group.label}`);
    const identities = row.members.map(member => {
      const sourceIdentity = member?.source_identity;
      assert(sourceIdentity && typeof sourceIdentity === 'object', `Every ${group.label} member must carry source_identity`);
      assert.deepEqual(Object.keys(sourceIdentity).sort(), ['generation', 'id', 'project', 'resource_type'],
        `Member source_identity must remain a scoped CDA identity for ${group.label}`);
      assert.equal(sourceIdentity.project, project);
      assert.equal(sourceIdentity.generation, generation);
      assert.equal(sourceIdentity.resource_type, resourceType);
      return {
        generation: sourceIdentity.generation,
        id: sourceIdentity.id,
        project: sourceIdentity.project,
        resource_type: sourceIdentity.resource_type,
      };
    }).sort((left, right) => left.id.localeCompare(right.id));
    const expectedIdentities = group.memberIDs.map(id => ({
      generation, id, project, resource_type: resourceType,
    })).sort((left, right) => left.id.localeCompare(right.id));
    assert.deepEqual(identities, expectedIdentities,
      `Native Preview Members must match the exact raw scoped roster for ${group.label}`);
    if (relatedColumnName !== undefined) {
      if (form === 'COUNT') {
        assert.equal(row[relatedColumnName], group.expectedObservationCount,
          `Native Preview related COUNT must match the independent raw oracle for ${group.label}`);
      } else {
        const expectedIDs = expectedRelatedValue(group);
        assert(Array.isArray(row[relatedColumnName]), `Native Preview related ALL must return a typed array for ${group.label}`);
        assert.deepEqual(row[relatedColumnName], expectedIDs,
          `Native Preview related ALL must equal the exact distinct raw IDs for ${group.label}`);
        assert.equal(new Set(row[relatedColumnName]).size, row[relatedColumnName].length,
          `Native Preview related ALL must deduplicate Observation IDs for ${group.label}`);
      }
    }
    evidenceRows.push({
      groupID: group.id, groupLabel: group.label, groupOrdinal: group.ordinal,
      memberIDs: identities.map(sourceIdentity => sourceIdentity.id),
      ...(relatedColumnName === undefined ? {} : { [relatedColumnName]: row[relatedColumnName] }),
    });
  }
  return evidenceRows;
};

const waitNative = async (suffix, startedAt, predicate = () => true) => {
  const deadline = startedAt + 5000;
  while (Date.now() < deadline) {
    const match = report.nativeRequests.findLast(entry => pathOf(entry).endsWith(suffix) &&
      entry.startedAt >= startedAt && entry.completedAt && entry.response !== undefined && predicate(entry));
    if (match) return match;
    await pause(40);
  }
  assert.fail(`Native ${suffix} request did not complete with a readable response within five seconds`);
};

const captureTable = async () => browserEval(browser.page, `return {
  rowCount: Number(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') ?? 0) - 1,
  headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(header => header.innerText.trim()),
  rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
};`);

const waitTable = async expectedRows => {
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(expectedRows + 1))} && !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:')`);
  return captureTable();
};

const openTable = async (expectedGroupCounts, relatedColumnName) => {
  const startedAt = Date.now();
  await navigate(browser.page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  const tablePreviewStartedAt = Date.now();
  await click(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  const table = await waitTable(expectedGroupCounts.length);
  const nativePreview = await waitNative('/preview', tablePreviewStartedAt, entry =>
    entry.status === 200 && entry.response?.rows?.length === expectedGroupCounts.length);
  const protocolRows = assertTypedNamedGroupRows(nativePreview.response.rows, expectedGroupCounts, report.cohort.revisionId, relatedColumnName);
  record('reload-to-preview', startedAt, { table, protocolRows });
  return table;
};

const proposalPreview = async (startedAt, expectedGroupCounts) => {
  const request = await waitNative('/construction-proposals', startedAt, entry => entry.status === 200);
  assert.equal(request.status, 200, JSON.stringify(request.response));
  assert.equal(request.response.previewStatus, 'READY', JSON.stringify(request.response));
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === ${JSON.stringify(request.response.proposalId)} && document.querySelector('[data-testid="construction-proposal-ready"]')`);
  const view = await browserEval(browser.page, `const preview=document.querySelector('[data-testid="construction-proposal-preview"]');return {
    headers:[...preview.querySelectorAll('th')].map(header=>header.firstElementChild?.textContent?.trim()??header.innerText.trim()),
    rows:[...preview.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))),
  };`);
  const step = request.response.candidateConstruction.steps.at(-1);
  assert.equal(step?.operation.kind, 'RELATED_SOURCE', 'The Add columns transition must save an authored RELATED_SOURCE step');
  const source = step.operation.relatedSource;
  assert.equal(source.form, relatedForm, `Native chooser must author RELATED_SOURCE ${relatedForm}`);
  assert.equal(source.source.kind, 'FIELD');
  assert.equal(source.source.resourceType, observationType);
  assert.equal(source.source.path, 'id');
  assert.deepEqual(source.contributorRule, { policy: 'ALL_MATCHES' }, 'RELATED_SOURCE must retain every exact matching Observation');
  assert.deepEqual(source.route.map(edge => ({ from: edge.fromResourceType, to: edge.toResourceType, relationship: edge.relationship, direction: edge.storageDirection })), [
    { from: 'Specimen', to: 'Patient', relationship: 'subject_Patient', direction: 'OUTBOUND' },
    { from: 'Patient', to: 'Observation', relationship: 'subject_Patient', direction: 'INBOUND' },
  ]);
  const output = step.outputs.find(column => column.id === source.outputColumnId);
  assert(output, `The candidate related ${relatedForm} must have a terminal authored output column`);
  const protocolRows = assertTypedNamedGroupRows(request.response.preview?.rows, expectedGroupCounts, report.cohort.revisionId, output.name);
  assertNamedGroupRows(view, expectedGroupCounts, output.label);
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `related ${relatedForm} proposal preview took ${durationMs}ms`);
  report.cases.push({ name: `related-${relatedFormName}-proposal-preview`, durationMs, headers: view.headers, row: view.rows.map(row => row.map(cell => cell.text)), protocolRows, candidateConstruction: request.response.candidateConstruction });
  return { request, view, step, output };
};

const addRelatedObservation = async expectedGroupCounts => {
  await click(browser.page, '[data-testid="construction-action-add-columns"]');
  await click(browser.page, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-add-columns-source"]')`);
  if (!await browserEval(browser.page, `return document.querySelector('[aria-label="Related resources"] summary')?.parentElement.open;`)) {
    await click(browser.page, '[aria-label="Related resources"] summary');
  }
  await click(browser.page, '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  if (!await browserEval(browser.page, `return document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')?.parentElement.open;`)) {
    await click(browser.page, '[data-testid="feature-catalog-raw-fields"] summary');
  }
  await waitForBrowser(browser.page, `document.querySelector('input[aria-label="Select Observation.id"]:not(:disabled)')`);
  await click(browser.page, 'input[aria-label="Select Observation.id"]');
  await click(browser.page, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForBrowser(browser.page, `document.querySelector('[role="dialog"]')`);
  if (!await browserEval(browser.page, `return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Other relationship paths'))?.parentElement.open;`)) {
    await click(browser.page, '[role="dialog"] summary', { includes: 'Other relationship paths' });
  }
  const routeSelector = `[role="dialog"] input[aria-label=${JSON.stringify(routeLabel)}]`;
  await waitForBrowser(browser.page, `document.querySelector(${JSON.stringify(routeSelector)})`);
  const routeOptions = await browserEval(browser.page, `return [...document.querySelectorAll(${JSON.stringify(routeSelector)})].map(input=>({disabled:input.disabled,label:input.getAttribute('aria-label')}));`);
  if (!routeOptions.some(option => !option.disabled)) {
    throw new UnsupportedCapabilityError('The native related-source chooser cannot select Observation.id on the Specimen → Patient ← Observation route after the named cohort.', { routeOptions });
  }
  await click(browser.page, routeSelector);
  const formLabel = relatedForm === 'COUNT' ? 'Observation ID: Count matching records' : 'Observation ID: Keep all matching values';
  const formSelector = `[role="dialog"] input[aria-label=${JSON.stringify(formLabel)}]`;
  await waitForBrowser(browser.page, `document.querySelector(${JSON.stringify(formSelector)})`);
  const formOptions = await browserEval(browser.page, `return [...document.querySelectorAll(${JSON.stringify(formSelector)})].map(input=>({disabled:input.disabled,label:input.getAttribute('aria-label')}));`);
  if (!formOptions.some(option => !option.disabled)) {
    throw new UnsupportedCapabilityError(`The native related-source chooser cannot express the ${relatedForm} form after the named cohort.`, { formOptions });
  }
  const startedAt = Date.now();
  await click(browser.page, formSelector);
  const selectedChoice = await browserEval(browser.page, `const dialog=document.querySelector('[role="dialog"]');return {route:dialog?.querySelector('input[aria-label=${JSON.stringify(routeLabel)}]')?.checked,form:dialog?.querySelector('input[aria-label=${JSON.stringify(formLabel)}]')?.checked};`);
  assert.deepEqual(selectedChoice, { route: true, form: true }, `Native chooser must retain the exact route and ${relatedForm} source form`);
  await click(browser.page, '[role="dialog"] button', { name: 'Add 1 column' });
  return proposalPreview(startedAt, expectedGroupCounts);
};

const applyConstructionProposal = async (expectedGroupCounts, relatedColumnName) => {
  const startedAt = Date.now();
  const nativeStartIndex = report.nativeRequests.length;
  await click(browser.page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  const table = await waitTable(expectedGroupCounts.length);
  const savedCommand = await waitNative('/commands', startedAt, entry => entry.status === 200);
  const previewRequest = await waitNative('/preview', startedAt, entry => entry.status === 200);
  assert(savedCommand, 'Apply must persist through the native authoring command endpoint');
  assert(previewRequest.response?.receiptId, 'Apply must render a fresh native Preview receipt');
  const protocolRows = assertTypedNamedGroupRows(previewRequest.response.rows, expectedGroupCounts, report.cohort.revisionId, relatedColumnName);
  assert(report.nativeRequests.slice(nativeStartIndex).some(entry => pathOf(entry).endsWith('/commands') && entry.status === 200));
  record('apply-to-render', startedAt, { table, protocolRows });
  builder = await api(base + '/builder');
  return table;
};

try {
  const apiBuildStartedAt = new Date().toISOString();
  report.apiBuildFreeze = { target: apiBuildTarget, startedAt: apiBuildStartedAt };
  frozenApiBuild = await captureApiBuildFreeze(readApiBuildStamp);
  report.apiBuildFreeze = { ...report.apiBuildFreeze, initial: frozenApiBuild.initial };
  frozenSource = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze = { startedAt: new Date().toISOString(), watchedFileCount: frozenSource.watchedFileCount };
  const [seed] = rawQuery(sourceWitnessQuery);
  if (!seed) {
    report.status = 'bounded-absence';
    report.oracle = {
      status: 'bounded-absence',
      query: sourceWitnessQuery,
      witnessBounds: report.witnessBounds,
      explanation: `No two-member Specimen cohort sharing a Patient with 1-${maxExactObservations} distinct Observations was found among the first ${witnessPatientLimit} scoped Specimens. This does not establish absence elsewhere in the project or generation.`,
    };
    throw new BoundedAbsenceError(report.oracle.explanation, report.oracle);
  }
  const memberIDs = sorted(seed.members.map(member => member.id));
  assert.equal(memberIDs.length, 2);
  assert.equal(new Set(memberIDs).size, 2);
  assert(seed.members.every(member => member.project === project && member.generation === generation && member.resourceType === resourceType));
  assert.equal(seed.patient.project, project);
  assert.equal(seed.patient.generation, generation);
  assert.equal(seed.patient.resourceType, patientType);
  assert(seed.observations.length >= 1 && seed.observations.length <= maxExactObservations);
  const exactSourcesQuery = `FOR specimen IN Specimen FILTER specimen.id IN ${JSON.stringify(memberIDs)} AND specimen.project == ${JSON.stringify(project)} AND specimen.dataset_generation == ${JSON.stringify(generation)} AND specimen.resourceType == ${JSON.stringify(resourceType)} RETURN {id:specimen.id,_id:specimen._id,project:specimen.project,generation:specimen.dataset_generation,resourceType:specimen.resourceType}`;
  const exactSources = rawQuery(exactSourcesQuery);
  assert.deepEqual(sorted(exactSources.map(source => source.id)), memberIDs, 'The independent exact-membership reread must resolve both scoped source IDs');
  const exactOracle = exactObservationOracle(memberIDs);
  assert(exactOracle.observations.length <= maxExactObservations, 'The exact selected witness exceeds the distinct Observation document cap');
  assert.equal(new Set(exactOracle.observations.map(observation => observation._id)).size, exactOracle.observations.length,
    'The exact raw Observation witness must contain distinct FHIR documents only');
  assert.deepEqual(sorted(exactOracle.observations.map(observation => observation.id)), sorted(seed.observations.map(observation => observation.id)), 'Bounded finder and exact-member route oracle disagree');
  const memberObservationSets = memberIDs.map(memberID => ({ memberID, ...exactMemberObservationOracle(memberID) }));
  for (const memberSet of memberObservationSets) {
    assert(memberSet.observations.length >= 1 && memberSet.observations.length <= maxExactObservations,
      `Selected Specimen ${memberSet.memberID} must independently reach a bounded nonempty Observation set`);
    assert.equal(new Set(memberSet.observations.map(observation => observation._id)).size, memberSet.observations.length,
      `Selected Specimen ${memberSet.memberID} raw source set must be distinct by FHIR document`);
    assert(memberSet.observations.every(observation => observation.project === project && observation.generation === generation),
      `Selected Specimen ${memberSet.memberID} source set must remain within the explicit project and generation`);
  }
  const observationOccurrences = new Map();
  for (const memberSet of memberObservationSets) {
    for (const observation of memberSet.observations) {
      const occurrence = observationOccurrences.get(observation._id) ?? { observation, members: [] };
      occurrence.members.push(memberSet.memberID);
      observationOccurrences.set(observation._id, occurrence);
    }
  }
  const perMemberUnion = [...observationOccurrences.values()].map(entry => entry.observation)
    .sort((left, right) => left._id.localeCompare(right._id));
  assert.deepEqual(perMemberUnion.map(observation => observation._id),
    exactOracle.observations.slice().sort((left, right) => left._id.localeCompare(right._id)).map(observation => observation._id),
    'Independent per-member raw route union must equal the independently queried selected-member source set');
  const sharedObservationIDs = [...observationOccurrences.values()]
    .filter(entry => entry.members.length === memberIDs.length)
    .map(entry => entry.observation.id);
  assert(sharedObservationIDs.length > 0,
    'Both independently selected sibling Specimens must contribute the same Observation, exercising related ALL deduplication');
  report.oracle = {
    status: 'selected',
    witnessQuery: sourceWitnessQuery,
    exactSourcesQuery,
    exactObservationQuery: exactOracle.query,
    exactPerMemberObservationQueries: memberObservationSets.map(({ memberID, query }) => ({ memberID, query })),
    patient: seed.patient,
    members: seed.members,
    memberIDs,
    observations: perMemberUnion,
    memberObservationSets: memberObservationSets.map(({ memberID, observations }) => ({ memberID, observations })),
    sharedObservationIDs,
    expectedDistinctObservationCount: perMemberUnion.length,
    expectedDistinctObservationIDs: perMemberUnion.map(observation => observation.id),
    bounds: report.witnessBounds,
  };

  await api(root, { name: explorer, title: `Named cohort authored related ${relatedForm} QA` });
  assert.notEqual(explorer, protectedExplorer, 'Only a fresh owned QA Explorer may be used');
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, generation);
  const scopeDigest = builder.catalog.authorizationScopeDigest;
  assert(scopeDigest, 'The catalog must expose the active authorization scope digest');
  const node = builder.catalog.nodes.find(candidate => candidate.resourceType === resourceType);
  assert(node, `${resourceType} must be present in the active catalog`);
  await command([{ type: 'CREATE_TABLE', title: `Named cohort related ${relatedForm} QA`, rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(idField, 'The direct Specimen FHIR ID field must be available');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Source Specimen ID' }]);
  const selection = await api(selections, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: exactSources.map(source => ({ project, generation, resourceType, id: source.id })) } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, resourceType);
  assert.equal(selection.scopeDigest, scopeDigest);
  assert.equal(selection.memberCount, exactSources.length);
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const directRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(directRoute, 'The exact selected Specimen resources must expose a direct population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  const selectionPage = await api(`${selections}/${selection.id}?limit=100`);
  assert.equal(selectionPage.revision.id, selection.id);
  assert.equal(selectionPage.revision.scopeDigest, scopeDigest);
  assert.equal(selectionPage.revision.project, project);
  assert.equal(selectionPage.revision.generation, generation);
  assert.equal(selectionPage.revision.resourceType, resourceType);
  assert.equal(selectionPage.revision.memberCount, exactSources.length);
  assert.deepEqual(sorted(selectionPage.members.map(member => member.ref.id)), memberIDs, 'Pinned selection membership must equal the independent raw Specimen witness');
  const memberByID = new Map(selectionPage.members.map(member => [member.ref.id, member.memberKey]));
  assert(memberIDs.every(id => memberByID.get(id)), 'Each exact selected FHIR ID must have a cohort member key');
  const groupDefinitions = [
    { id: 'qa-sibling-specimens', label: groupLabel, ordinal: 0, memberIDs },
    ...(includeEmptyGroup ? [{ id: emptyGroupId, label: emptyGroupLabel, ordinal: 1, memberIDs: [] }] : []),
  ];
  const cohort = await api(`${selections}/${selection.id}/explicit-groups`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: randomUUID(),
    groups: groupDefinitions.map(group => ({
      id: group.id, label: group.label, ordinal: group.ordinal,
      memberIds: group.memberIDs.map(id => memberByID.get(id)),
    })),
  });
  assert.equal(cohort.sourceSelectionRevisionId, selection.id);
  assert.equal(cohort.groupCount, groupDefinitions.length);
  assert.equal(cohort.memberCount, memberIDs.length);
  assert.deepEqual(cohort.groups, groupDefinitions.map(group => ({
    id: group.id, label: group.label, ordinal: group.ordinal, memberCount: group.memberIDs.length,
  })), 'The API must preserve exact named-group identities, order, and empty/nonempty membership counts');
  const rawGroupRoster = rawExplicitGroupRosterOracle(cohort.revisionId);
  assert.deepEqual(rawGroupRoster.definitions, groupDefinitions.map(({ id, label, ordinal }) => ({ id, label, ordinal })),
    'Independent Arango group definitions must match the submitted exact names and ordinals');
  const expectedRawMemberships = groupDefinitions.flatMap(group => group.memberIDs.map(id => ({
    groupId: group.id, id, project, generation, resourceType,
  }))).sort((left, right) => left.groupId.localeCompare(right.groupId) || left.id.localeCompare(right.id));
  assert.deepEqual(rawGroupRoster.memberships, expectedRawMemberships,
    'Independent Arango membership rows must contain exactly the nonempty group roster and no empty-group members');
  const expectedGroupCounts = [];
  for (const group of groupDefinitions) {
    const rawMembers = rawGroupRoster.memberships.filter(member => member.groupId === group.id).map(member => member.id);
    assert.deepEqual(sorted(rawMembers), sorted(group.memberIDs), `Raw group roster differs for ${group.label}`);
    const groupObservationOracle = exactObservationOracle(rawMembers);
    const expectedObservationCount = groupObservationOracle.observations.length;
    if (group.id === 'qa-sibling-specimens') {
      assert.deepEqual(sorted(groupObservationOracle.observations.map(item => item.id)), sorted(exactOracle.observations.map(item => item.id)),
        'Nonempty group count oracle must match the original independent route oracle');
    }
    if (group.memberIDs.length === 0) assert.equal(expectedObservationCount, 0, 'The independently empty raw group must have zero related Observations');
    expectedGroupCounts.push({
      id: group.id, label: group.label, ordinal: group.ordinal, memberIDs: rawMembers,
      expectedObservationCount, observationQuery: groupObservationOracle.query,
      observations: groupObservationOracle.observations,
      expectedRelatedValue: relatedForm === 'COUNT'
        ? expectedObservationCount
        : groupObservationOracle.observations.slice().sort((left, right) => left._id.localeCompare(right._id)).map(observation => observation.id),
    });
  }
  report.oracle.groups = expectedGroupCounts;
  report.oracle.groupRoster = rawGroupRoster;
  report.cohort = { ...cohort, scopeDigest, selectionRevisionId: selection.id, rawMemberIDs: memberIDs, rawGroupRoster };

  browser = await launchCdaBrowser(evidence, apiOrigin, uiOrigin);
  nativeCapture = captureCDARequests(browser.page, {
    apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: base, report,
    responsePaths: /builder|commands|preview|construction-proposals|population-routes|selections/,
  });
  browser.page.on('request', request => {
    const url=new URL(request.url());
    if(url.pathname.includes(`/explorers/${protectedExplorer}/`)) report.protectedExplorerUntouched=false;
    const entry=nativeCapture.byRequest.get(request);
    if(entry?.authorizationHeaderPresent) report.errors.push({kind:'unexpected-auth-header',path:entry.path});
  });

  const startedLoad = Date.now();
  await navigate(browser.page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await waitTable(memberIDs.length);
  record('open-exact-selected-source-table', startedLoad);

  const rowSettingsStart = Date.now();
  await click(browser.page, '[data-testid="construction-rows-settings-trigger"]');
  const rowShapeSelector = 'select[aria-label="What should each row represent?"]';
  await waitForBrowser(browser.page, `document.querySelector(${JSON.stringify(rowShapeSelector)})?.disabled === false`);
  const cohortValue = `explicit:${cohort.revisionId}`;
  await selectOption(browser.page, rowShapeSelector, cohortValue);
  const policySelector = 'select[aria-label="Unmatched record policy"]';
  await waitForBrowser(browser.page, `document.querySelector(${JSON.stringify(policySelector)})?.disabled === false`);
  await selectOption(browser.page, policySelector, `${cohortValue}:ERROR`);
  await waitForBrowser(browser.page, `[...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled)`);
  const rowPreview = await browserEval(browser.page, `return document.querySelector('[aria-label="Row definition preview"]')?.innerText;`);
  assert(rowPreview?.includes(`${memberIDs.length} rows → ${groupDefinitions.length} rows`), rowPreview ?? 'Named cohort preview did not materialize every declared group');
  const groupApplyStart = Date.now();
  await click(browser.page, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
  await waitForBrowser(browser.page, `[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(header=>header.innerText.trim().toLowerCase()==='members')`);
  const groupedTable = await waitTable(groupDefinitions.length);
  const groupedNativePreview = await waitNative('/preview', groupApplyStart, entry =>
    entry.status === 200 && entry.response?.rows?.length === groupDefinitions.length);
  const groupedProtocolRows = assertTypedNamedGroupRows(groupedNativePreview.response.rows, expectedGroupCounts, cohort.revisionId);
  record('apply-named-cohort', rowSettingsStart, { table: groupedTable, protocolRows: groupedProtocolRows });
  builder = await api(base + '/builder');
  const groupedBaselineWorkspace = structuredClone(builder.workspace);
  const groupedBaselineDocument = structuredClone(doc(builder));
  assert.equal(groupedBaselineDocument.population.selectionRevisionId, selection.id);
  assert.equal(groupedBaselineDocument.rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(groupedBaselineDocument.rows.groups.source.explicit.unassignedMemberPolicy, 'ERROR');
  assert.equal(groupedBaselineDocument.construction?.steps.length ?? 0, 0);
  assertNamedGroupRows(groupedTable, expectedGroupCounts);
  report.nativeCohortRows = { headers: groupedTable.headers, rows: groupedTable.rows, groups: expectedGroupCounts, revisionId: cohort.revisionId };

  const capabilities = await api(base + '/construction-capabilities', {
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, outputId, stageId: 'group_rows',
  });
  const relatedCapability = capabilities.selectedStage.capabilities.find(capability => capability.kind === 'RELATED_SOURCE');
  report.appendStageCapability = {
    stageId: capabilities.selectedStage.id,
    rowIdentityColumn: capabilities.selectedStage.rowIdentityColumn,
    relatedSource: relatedCapability,
    columnNames: capabilities.selectedStage.columns.map(column => column.name),
  };
  if (!capabilities.selectedStage.rowIdentityColumn || !relatedCapability?.supported) {
    throw new UnsupportedCapabilityError('The backend does not expose a related-source append stage after the named cohort.', report.appendStageCapability);
  }

  const first = await addRelatedObservation(expectedGroupCounts);
  const cancelStart = Date.now();
  await click(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base + '/builder')).workspace, groupedBaselineWorkspace, 'Cancel must keep named-cohort membership and authored schema unchanged');
  record(`cancel-related-${relatedFormName}-proposal`, cancelStart);

  const second = await addRelatedObservation(expectedGroupCounts);
  const relatedIntent = operation => {
    const { outputColumnId, ...source } = operation.relatedSource;
    assert(outputColumnId, 'Each related-column proposal must define an output identity');
    return { kind: operation.kind, relatedSource: source };
  };
  assert.deepEqual(relatedIntent(second.step.operation), relatedIntent(first.step.operation),
    `Reopening after Cancel must retain the same related source binding and ${relatedForm} intent`);
  const applyStart = Date.now();
  await applyConstructionProposal(expectedGroupCounts, second.output.name);
  record(`apply-related-${relatedFormName}-step`, applyStart);
  builder = await api(base + '/builder');
  const savedRelatedDocument = structuredClone(doc(builder));
  assert.equal(savedRelatedDocument.rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(savedRelatedDocument.population.selectionRevisionId, selection.id);
  assert.equal(savedRelatedDocument.columns.length, groupedBaselineDocument.columns.length);
  for (let index = 0; index < groupedBaselineDocument.columns.length; index += 1) {
    const before = groupedBaselineDocument.columns[index];
    const after = savedRelatedDocument.columns[index];
    const { columnId: beforeID, ...beforeBinding } = before;
    const { columnId: afterID, ...afterBinding } = after;
    assert.deepEqual(afterBinding, beforeBinding, 'Related output append must preserve authored source bindings');
    if (beforeID) assert.equal(afterID, beforeID, 'Existing source column identity must remain stable');
    else assert(afterID, 'Construction must materialize the previously implicit source column identity');
  }
  assert.equal(savedRelatedDocument.construction.steps.length, 1);
  const savedStep = savedRelatedDocument.construction.steps[0];
  assert.equal(savedStep.operation.kind, 'RELATED_SOURCE');
  assert.equal(savedStep.operation.relatedSource.form, relatedForm);
  assert.deepEqual(savedStep.operation, second.step.operation);
  assert(savedStep.outputs.some(column => column.id === savedStep.operation.relatedSource.outputColumnId));

  let savedTable = await openTable(expectedGroupCounts, savedStep.outputs.find(column => column.id === savedStep.operation.relatedSource.outputColumnId).name);
  const relatedLabel = savedStep.outputs.find(column => column.id === savedStep.operation.relatedSource.outputColumnId).label;
  assertNamedGroupRows(savedTable, expectedGroupCounts, relatedLabel);

  const beforeEditWorkspace = structuredClone(builder.workspace);
  await click(browser.page, `[data-testid="construction-history-step-${savedStep.id}"]`);
  await click(browser.page, `[data-testid="construction-edit-step-${savedStep.id}"]`);
  const labelSelector = '[data-testid="related-source-step-editor"] input[aria-label="Output column label"]';
  await waitForBrowser(browser.page, `document.querySelector(${JSON.stringify(labelSelector)})?.matches(":not(:disabled)")`);
  const editedLabel = `${relatedLabel} reviewed`;
  const editStart = Date.now();
  await fill(browser.page, labelSelector, editedLabel);
  const editedProposal = await waitNative('/construction-proposals', editStart, entry => entry.status === 200);
  assert.equal(editedProposal.response.previewStatus, 'READY', JSON.stringify(editedProposal.response));
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === ${JSON.stringify(editedProposal.response.proposalId)} && document.querySelector('[data-testid="construction-proposal-ready"]')`);
  const editedStep = editedProposal.response.candidateConstruction.steps.find(step => step.id === savedStep.id);
  assert(editedStep);
  assert.deepEqual(editedStep.operation, savedStep.operation, 'Editing the output label must preserve the exact route, source ID and cohort anchor');
  const editedOutput = editedStep.outputs.find(column => column.id === savedStep.operation.relatedSource.outputColumnId);
  assert.equal(editedOutput.label, editedLabel);
  const editedProtocolRows = assertTypedNamedGroupRows(editedProposal.response.preview?.rows, expectedGroupCounts, cohort.revisionId, editedOutput.name);
  const editedView = await browserEval(browser.page, `const preview=document.querySelector('[data-testid="construction-proposal-preview"]');return {
    headers:[...preview.querySelectorAll('th')].map(header=>header.firstElementChild?.textContent?.trim()??header.innerText.trim()),
    rows:[...preview.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))),
  };`);
  assertNamedGroupRows(editedView, expectedGroupCounts, editedLabel);
  record(`edit-related-${relatedFormName}-preview`, editStart, { editedLabel, rows: editedView.rows.map(row => row.map(cell => cell.text)), protocolRows: editedProtocolRows });
  const cancelEditStart = Date.now();
  await click(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base + '/builder')).workspace, beforeEditWorkspace, `Cancel edit must retain the saved related ${relatedForm} operation`);
  record(`cancel-related-${relatedFormName}-label-edit`, cancelEditStart);

  await click(browser.page, `[data-testid="construction-history-step-${savedStep.id}"]`);
  await click(browser.page, `[data-testid="construction-edit-step-${savedStep.id}"]`);
  await waitForBrowser(browser.page, `document.querySelector(${JSON.stringify(labelSelector)})?.matches(":not(:disabled)")`);
  const confirmedEditStart = Date.now();
  await fill(browser.page, labelSelector, editedLabel);
  const confirmedEditProposal = await waitNative('/construction-proposals', confirmedEditStart, entry => entry.status === 200);
  const confirmedEditStep = confirmedEditProposal.response.candidateConstruction.steps.find(step => step.id === savedStep.id);
  const confirmedEditOutput = confirmedEditStep.outputs.find(column => column.id === savedStep.operation.relatedSource.outputColumnId);
  assertTypedNamedGroupRows(confirmedEditProposal.response.preview?.rows, expectedGroupCounts, cohort.revisionId, confirmedEditOutput.name);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-proposal-ready"]')`);
  await applyConstructionProposal(expectedGroupCounts, confirmedEditOutput.name);
  record(`apply-related-${relatedFormName}-label-edit`, confirmedEditStart);
  builder = await api(base + '/builder');
  savedTable = await openTable(expectedGroupCounts, savedStep.outputs.find(column => column.id === savedStep.operation.relatedSource.outputColumnId).name);
  const reloadedStep = doc(builder).construction.steps.find(step => step.id === savedStep.id);
  assert(reloadedStep);
  assert.deepEqual(reloadedStep.operation, savedStep.operation);
  assert.equal(reloadedStep.outputs.find(column => column.id === savedStep.operation.relatedSource.outputColumnId).label, editedLabel);
  assertNamedGroupRows(savedTable, expectedGroupCounts, editedLabel);

  const editedWorkspace = structuredClone(builder.workspace);
  await click(browser.page, `[data-testid="construction-history-step-${savedStep.id}"]`);
  const removeStart = Date.now();
  await click(browser.page, `[data-testid="construction-remove-step-${savedStep.id}"]`);
  const removeProposal = await waitNative('/construction-proposals', removeStart, entry => entry.status === 200);
  assert.equal(removeProposal.response.previewStatus, 'READY', JSON.stringify(removeProposal.response));
  const removeProtocolRows = assertTypedNamedGroupRows(removeProposal.response.preview?.rows, expectedGroupCounts, cohort.revisionId);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-proposal-ready"]')`);
  const removeView = await browserEval(browser.page, `const preview=document.querySelector('[data-testid="construction-proposal-preview"]');return {headers:[...preview.querySelectorAll('th')].map(header=>header.firstElementChild?.textContent?.trim()??header.innerText.trim()),rows:[...preview.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};`);
  assert.equal(removeProposal.response.candidateConstruction.steps.length, 0, 'Removing the authored Add column must restore the construction-free cohort');
  assertNamedGroupRows(removeView, expectedGroupCounts);
  record(`remove-related-${relatedFormName}-preview`, removeStart, { headers: removeView.headers, rows: removeView.rows, protocolRows: removeProtocolRows });
  const cancelRemoveStart = Date.now();
  await click(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base + '/builder')).workspace, editedWorkspace, `Cancel removal must retain the renamed related ${relatedForm} output`);
  record(`cancel-related-${relatedFormName}-removal`, cancelRemoveStart);

  await click(browser.page, `[data-testid="construction-history-step-${savedStep.id}"]`);
  const confirmedRemoveStart = Date.now();
  await click(browser.page, `[data-testid="construction-remove-step-${savedStep.id}"]`);
  const confirmedRemoveProposal = await waitNative('/construction-proposals', confirmedRemoveStart, entry => entry.status === 200);
  assertTypedNamedGroupRows(confirmedRemoveProposal.response.preview?.rows, expectedGroupCounts, cohort.revisionId);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-proposal-ready"]')`);
  await applyConstructionProposal(expectedGroupCounts);
  record(`apply-related-${relatedFormName}-removal`, confirmedRemoveStart);
  builder = await api(base + '/builder');
  const expectedRestoredDocument = {
    ...groupedBaselineDocument,
    columns: savedRelatedDocument.columns,
    construction: { version: 1, steps: [] },
  };
  assert.deepEqual(doc(builder), expectedRestoredDocument,
    'Removal must restore the cohort and bindings while retaining the verified materialized source IDs');
  const restored = await openTable(expectedGroupCounts);
  assert.deepEqual(restored.headers, groupedTable.headers);
  assert.deepEqual(restored.rows, groupedTable.rows);
  assertNamedGroupRows(restored, expectedGroupCounts);
  builder = await api(base + '/builder');
  assert.equal(doc(builder).rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(doc(builder).population.selectionRevisionId, selection.id);
  assert.deepEqual(doc(builder).construction?.steps ?? [], []);
  assert.deepEqual(report.errors, [], 'The lifecycle must have no unexpected UI/API errors');
  assert.equal(report.protectedExplorerUntouched, true, 'The protected full-QA Explorer must remain untouched');
  report.sourceFreeze = { ...report.sourceFreeze, ...(await frozenSource.assertUnchanged()) };
  report.status = 'passed';
} catch (error) {
  const invalidated = Boolean(error.invalidatesRun);
  report.status = invalidated ? 'invalidated' : error instanceof BoundedAbsenceError ? 'bounded-absence' : error instanceof UnsupportedCapabilityError ? 'unsupported' : 'failed';
  report.error = String(error.stack ?? error);
  if (error instanceof ApiBuildFreezeError) {
    report.apiBuildFreeze = {
      ...report.apiBuildFreeze,
      initial: error.before,
      ...(error.after?.checked ? { after: error.after } : {}),
      unchanged: false,
      invalidatesRun: true,
      productFailure: false,
      reason: error.reason,
    };
  } else if (invalidated) {
    report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true };
  }
  if (error instanceof UnsupportedCapabilityError) report.unsupportedEvidence = error.evidence;
  else if (error instanceof BoundedAbsenceError) report.boundedAbsence = error.evidence;
  if (invalidated || (!(error instanceof UnsupportedCapabilityError) && !(error instanceof BoundedAbsenceError))) process.exitCode = 1;
  if (browser) await browser.captureFailure(error, { action: browser.activeAction ?? browser.lastAction, explorer, phase: 'CDA named cohort related COUNT lifecycle', draftVersion: builder?.draftVersion, draftDigest: builder?.draftDigest });
  if (browser) report.failureUI = await browserEval(browser.page, 'return document.body.innerText;').catch(String);
} finally {
  await nativeCapture?.flush();
  const apiBuildFinishedAt = new Date().toISOString();
  if (frozenApiBuild) {
    try {
      report.apiBuildFreeze = {
        ...report.apiBuildFreeze,
        ...(await frozenApiBuild.assertUnchanged()),
        finishedAt: apiBuildFinishedAt,
      };
    } catch (error) {
      report.priorStatus = report.status;
      if (report.error) report.priorError = report.error;
      report.status = 'invalidated';
      report.error = String(error.stack ?? error);
      report.apiBuildFreeze = {
        ...report.apiBuildFreeze,
        ...(error.before ? { initial: error.before } : {}),
        ...(error.after ? { after: error.after } : {}),
        unchanged: false,
        invalidatesRun: true,
        productFailure: false,
        ...(error.reason ? { reason: error.reason } : {}),
        error: String(error),
        finishedAt: apiBuildFinishedAt,
      };
      process.exitCode = 1;
    }
  } else {
    try {
      const finalOnly = await captureApiBuildFreeze(readApiBuildStamp);
      report.apiBuildFreeze = {
        ...report.apiBuildFreeze,
        after: finalOnly.initial,
        unchanged: false,
        invalidatesRun: true,
        productFailure: false,
        finishedAt: apiBuildFinishedAt,
      };
    } catch (error) {
      report.apiBuildFreeze = {
        ...report.apiBuildFreeze,
        ...(error.before ? { after: error.before } : {}),
        unchanged: false,
        invalidatesRun: true,
        productFailure: false,
        ...(error.reason ? { reason: error.reason } : {}),
        finishedAt: apiBuildFinishedAt,
      };
    }
    report.priorStatus ??= report.status;
    report.status = 'invalidated';
    process.exitCode = 1;
  }
  if (frozenSource && !report.sourceFreeze?.unchanged) {
    try {
      report.sourceFreeze = { ...report.sourceFreeze, ...(await frozenSource.assertUnchanged()) };
    } catch (error) {
      report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: error.invalidatesRun ?? false, error: String(error) };
      if (report.status === 'passed') {
        report.status = 'invalidated';
        report.error = String(error.stack ?? error);
        process.exitCode = 1;
      }
    }
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(item => ({ name: item.name, durationMs: item.durationMs })), error: report.error }));
