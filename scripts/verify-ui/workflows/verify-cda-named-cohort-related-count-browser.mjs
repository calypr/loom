import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { sanitizeReportPayload } from '../helpers/playwright-browser.mjs';

export async function namedCohortRelatedCountWorkflow({
  page,
  cda,
  relatedForm = process.env.LOOM_CDA_NAMED_COHORT_FORM ?? 'COUNT',
  includeEmptyGroup = process.env.LOOM_CDA_NAMED_COHORT_EMPTY_GROUP === '1',
}) {
const project = cda.project;
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const resourceType = 'Specimen';
const patientType = 'Patient';
const observationType = 'Observation';
assert(['COUNT', 'ALL'].includes(relatedForm), 'LOOM_CDA_NAMED_COHORT_FORM must be COUNT or ALL');
const relatedFormName = relatedForm.toLowerCase();
const explorer = `named-cohort-related-${relatedFormName}-${Date.now()}-${randomUUID().slice(0, 8)}`;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const arangoContainer = cda.target.arangoContainer;
const witnessPatientLimit = 2000;
const observationDocumentCap = 11;
const maxExactObservations = observationDocumentCap - 1;
const groupLabel = 'Two sibling Specimens';
const emptyGroupId = 'qa-empty-declared-group';
const emptyGroupLabel = 'Empty declared group';
const routeLabel = 'Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const selections = base.replace('/authoring/v2', '/selections');
const report = Object.assign(cda.report, {
  project, generation, resourceType, explorer, protectedExplorer, protectedExplorerUntouched: true, groupLabel, routeLabel,
  mode: includeEmptyGroup ? 'empty-declared-group' : 'nonempty-only', relatedForm,
  witnessBounds: { scopedSpecimens: witnessPatientLimit, distinctObservationDocuments: observationDocumentCap, maxSelectedObservationDocuments: maxExactObservations },
  cases: [], errors: [], requests: [], nativeRequests: cda.nativeRequests, started: new Date().toISOString(),
});

let nativeCapture;
let fatal;
let builder;
let outputId;

const pause = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const browserEval = (_page, callback, args = []) => cda.inspect(callback, args);
const click = (_page, selector, options = {}) => cda.click(selector, options);
const navigate = (_page, url) => cda.navigate(url);
const selectOption = (_page, selector, value, timeout = 5000) => cda.selectOption(selector, value, { timeout });
const fill = (_page, selector, value, options = {}) => cda.fill(selector, value, options);
const waitForBrowser = (_page, predicate, argsOrTimeout = [], argsOrTimeout2) => Array.isArray(argsOrTimeout)
  ? cda.wait(predicate, argsOrTimeout, argsOrTimeout2 ?? 5000)
  : cda.wait(predicate, [], argsOrTimeout);
const pathOf = entry => entry.path.split('?')[0];
const doc = state => state.workspace.documents.find(document => document.output.id === outputId);
const sorted = values => [...values].sort();
assert.equal(project, 'loom_dev_cda_fhir', 'This verifier is bound to the CDA-FHIR project');

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
    path, method: body ? 'POST' : 'GET', body: sanitizeReportPayload(body), startedAt, completedAt: Date.now(), status: response.status,
    response: sanitizeReportPayload(path.endsWith('/builder') ? {
      draftVersion: value.draftVersion,
      draftDigest: value.draftDigest,
      catalog: { generation: value.catalog?.generation, authorizationScopeDigest: value.catalog?.authorizationScopeDigest },
      workspace: value.workspace,
    } : value),
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

const protocolResponse = entry => nativeCapture.rawResponseBody(entry);
const waitNative = async (suffix, startedAt, predicate = () => true) => {
  const deadline = startedAt + 5000;
  while (Date.now() < deadline) {
    const match = report.nativeRequests.findLast(entry => {
      const response = protocolResponse(entry);
      return pathOf(entry).endsWith(suffix) && entry.startedAt >= startedAt && entry.completedAt &&
        response !== undefined && predicate(entry, response);
    });
    if (match) return match;
    await pause(40);
  }
  assert.fail(`Native ${suffix} request did not complete with a readable response within five seconds`);
};

const captureTable = async () => browserEval(page, (args) => { return {
  rowCount: Number(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') ?? 0) - 1,
  headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(header => header.innerText.trim()),
  rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
}; });

const waitTable = async expectedRows => {
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === args[0] && !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:'))); }, [String(expectedRows + 1)]);
  return captureTable();
};

const openTable = async (expectedGroupCounts, relatedColumnName) => {
  const startedAt = Date.now();
  await navigate(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-table-' + args[0] + '"]'))); }, [outputId]);
  const tablePreviewStartedAt = Date.now();
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false)); });
  const table = await waitTable(expectedGroupCounts.length);
  const nativePreview = await waitNative('/preview', tablePreviewStartedAt, (entry, response) =>
    entry.status === 200 && response?.rows?.length === expectedGroupCounts.length);
  const protocolRows = assertTypedNamedGroupRows(protocolResponse(nativePreview).rows, expectedGroupCounts, report.cohort.revisionId, relatedColumnName);
  record('reload-to-preview', startedAt, { table, protocolRows });
  return table;
};

const proposalPreview = async (startedAt, expectedGroupCounts) => {
  const request = await waitNative('/construction-proposals', startedAt, entry => entry.status === 200);
  const response = protocolResponse(request);
  assert.equal(request.status, 200, JSON.stringify(request.response));
  assert.equal(response.previewStatus, 'READY', JSON.stringify(request.response));
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === args[0] && document.querySelector('[data-testid="construction-proposal-ready"]'))); }, [response.proposalId]);
  const view = await browserEval(page, (args) => { const preview=document.querySelector('[data-testid="construction-proposal-preview"]');return {
    headers:[...preview.querySelectorAll('th')].map(header=>header.firstElementChild?.textContent?.trim()??header.innerText.trim()),
    rows:[...preview.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))),
  }; });
  const step = response.candidateConstruction.steps.at(-1);
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
  const protocolRows = assertTypedNamedGroupRows(response.preview?.rows, expectedGroupCounts, report.cohort.revisionId, output.name);
  assertNamedGroupRows(view, expectedGroupCounts, output.label);
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `related ${relatedForm} proposal preview took ${durationMs}ms`);
  report.cases.push({ name: `related-${relatedFormName}-proposal-preview`, durationMs, headers: view.headers, row: view.rows.map(row => row.map(cell => cell.text)), protocolRows, candidateConstruction: sanitizeReportPayload(response.candidateConstruction) });
  return { request, view, step, output };
};

const addRelatedObservation = async expectedGroupCounts => {
  await click(page, '[data-testid="construction-action-add-columns"]');
  await click(page, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-add-columns-source"]'))); });
  if (!await browserEval(page, (args) => { return document.querySelector('[aria-label="Related resources"] summary')?.parentElement.open; })) {
    await click(page, '[aria-label="Related resources"] summary');
  }
  await click(page, '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  if (!await browserEval(page, (args) => { return document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')?.parentElement.open; })) {
    await click(page, '[data-testid="feature-catalog-raw-fields"] summary');
  }
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('input[aria-label="Select Observation.id"]:not(:disabled)'))); });
  await click(page, 'input[aria-label="Select Observation.id"]');
  await click(page, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[role="dialog"]'))); });
  if (!await browserEval(page, (args) => { return [...document.querySelectorAll('[role="dialog"] summary')].find(summary=>summary.innerText.includes('Other relationship paths'))?.parentElement.open; })) {
    await click(page, '[role="dialog"] summary', { includes: 'Other relationship paths' });
  }
  const routeSelector = `[role="dialog"] input[aria-label=${JSON.stringify(routeLabel)}]`;
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector(args[0]))); }, [routeSelector]);
  const routeOptions = await browserEval(page, (args) => { return [...document.querySelectorAll(args[0])].map(input=>({disabled:input.disabled,label:input.getAttribute('aria-label')})); }, [routeSelector]);
  if (!routeOptions.some(option => !option.disabled)) {
    throw new UnsupportedCapabilityError('The native related-source chooser cannot select Observation.id on the Specimen → Patient ← Observation route after the named cohort.', { routeOptions });
  }
  await click(page, routeSelector);
  const formLabel = relatedForm === 'COUNT' ? 'Observation ID: Count matching records' : 'Observation ID: Keep all matching values';
  const formSelector = `[role="dialog"] input[aria-label=${JSON.stringify(formLabel)}]`;
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector(args[0]))); }, [formSelector]);
  const formOptions = await browserEval(page, (args) => { return [...document.querySelectorAll(args[0])].map(input=>({disabled:input.disabled,label:input.getAttribute('aria-label')})); }, [formSelector]);
  if (!formOptions.some(option => !option.disabled)) {
    throw new UnsupportedCapabilityError(`The native related-source chooser cannot express the ${relatedForm} form after the named cohort.`, { formOptions });
  }
  const startedAt = Date.now();
  await click(page, formSelector);
  const selectedChoice = await browserEval(page, (args) => { const dialog=document.querySelector('[role="dialog"]');return {route:dialog?.querySelector('input[aria-label="' + args[0] + '"]')?.checked,form:dialog?.querySelector('input[aria-label="' + args[1] + '"]')?.checked}; }, [routeLabel, formLabel]);
  assert.deepEqual(selectedChoice, { route: true, form: true }, `Native chooser must retain the exact route and ${relatedForm} source form`);
  await click(page, '[role="dialog"] button', { name: 'Add 1 column' });
  return proposalPreview(startedAt, expectedGroupCounts);
};

const applyConstructionProposal = async (expectedGroupCounts, relatedColumnName) => {
  const startedAt = Date.now();
  const nativeStartIndex = report.nativeRequests.length;
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
  const table = await waitTable(expectedGroupCounts.length);
  const savedCommand = await waitNative('/commands', startedAt, entry => entry.status === 200);
  const previewRequest = await waitNative('/preview', startedAt, entry => entry.status === 200);
  assert(savedCommand, 'Apply must persist through the native authoring command endpoint');
  const previewResponse = protocolResponse(previewRequest);
  assert(previewResponse?.receiptId, 'Apply must render a fresh native Preview receipt');
  const protocolRows = assertTypedNamedGroupRows(previewResponse.rows, expectedGroupCounts, report.cohort.revisionId, relatedColumnName);
  assert(report.nativeRequests.slice(nativeStartIndex).some(entry => pathOf(entry).endsWith('/commands') && entry.status === 200));
  record('apply-to-render', startedAt, { table, protocolRows });
  builder = await api(base + '/builder');
  return table;
};

try {
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

  nativeCapture = captureCDARequests(page, {
    apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: base, report,
    responsePaths: /builder|commands|preview|construction-proposals|population-routes|selections/,
  });
  page.on('request', request => {
    const url=new URL(request.url());
    if(url.pathname.includes(`/explorers/${protectedExplorer}/`)) report.protectedExplorerUntouched=false;
    const entry=nativeCapture.byRequest.get(request);
    if(entry?.authorizationHeaderPresent) report.errors.push({kind:'unexpected-auth-header',path:entry.path});
  });

  const startedLoad = Date.now();
  await navigate(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-table-' + args[0] + '"]'))); }, [outputId]);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false)); });
  await waitTable(memberIDs.length);
  record('open-exact-selected-source-table', startedLoad);

  const rowSettingsStart = Date.now();
  await click(page, '[data-testid="construction-rows-settings-trigger"]');
  const rowShapeSelector = 'select[aria-label="What should each row represent?"]';
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector(args[0])?.disabled === false)); }, [rowShapeSelector]);
  const cohortValue = `explicit:${cohort.revisionId}`;
  await selectOption(page, rowShapeSelector, cohortValue);
  const policySelector = 'select[aria-label="Unmatched record policy"]';
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector(args[0])?.disabled === false)); }, [policySelector]);
  await selectOption(page, policySelector, `${cohortValue}:ERROR`);
  await waitForBrowser(page, (args) => { return Boolean(([...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled))); });
  const rowPreview = await browserEval(page, (args) => { return document.querySelector('[aria-label="Row definition preview"]')?.innerText; });
  assert(rowPreview?.includes(`${memberIDs.length} rows → ${groupDefinitions.length} rows`), rowPreview ?? 'Named cohort preview did not materialize every declared group');
  const groupApplyStart = Date.now();
  await click(page, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
  await waitForBrowser(page, (args) => { return Boolean(([...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(header=>header.innerText.trim().toLowerCase()==='members'))); });
  const groupedTable = await waitTable(groupDefinitions.length);
  const groupedNativePreview = await waitNative('/preview', groupApplyStart, (entry, response) =>
    entry.status === 200 && response?.rows?.length === groupDefinitions.length);
  const groupedProtocolRows = assertTypedNamedGroupRows(protocolResponse(groupedNativePreview).rows, expectedGroupCounts, cohort.revisionId);
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
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
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
  await click(page, `[data-testid="construction-history-step-${savedStep.id}"]`);
  await click(page, `[data-testid="construction-edit-step-${savedStep.id}"]`);
  const labelSelector = '[data-testid="related-source-step-editor"] input[aria-label="Output column label"]';
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector(args[0])?.matches(":not(:disabled)"))); }, [labelSelector]);
  const editedLabel = `${relatedLabel} reviewed`;
  const editStart = Date.now();
  await fill(page, labelSelector, editedLabel);
  const editedProposal = await waitNative('/construction-proposals', editStart, entry => entry.status === 200);
  const editedResponse = protocolResponse(editedProposal);
  assert.equal(editedResponse.previewStatus, 'READY', JSON.stringify(editedProposal.response));
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === args[0] && document.querySelector('[data-testid="construction-proposal-ready"]'))); }, [editedResponse.proposalId]);
  const editedStep = editedResponse.candidateConstruction.steps.find(step => step.id === savedStep.id);
  assert(editedStep);
  assert.deepEqual(editedStep.operation, savedStep.operation, 'Editing the output label must preserve the exact route, source ID and cohort anchor');
  const editedOutput = editedStep.outputs.find(column => column.id === savedStep.operation.relatedSource.outputColumnId);
  assert.equal(editedOutput.label, editedLabel);
  const editedProtocolRows = assertTypedNamedGroupRows(editedResponse.preview?.rows, expectedGroupCounts, cohort.revisionId, editedOutput.name);
  const editedView = await browserEval(page, (args) => { const preview=document.querySelector('[data-testid="construction-proposal-preview"]');return {
    headers:[...preview.querySelectorAll('th')].map(header=>header.firstElementChild?.textContent?.trim()??header.innerText.trim()),
    rows:[...preview.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))),
  }; });
  assertNamedGroupRows(editedView, expectedGroupCounts, editedLabel);
  record(`edit-related-${relatedFormName}-preview`, editStart, { editedLabel, rows: editedView.rows.map(row => row.map(cell => cell.text)), protocolRows: editedProtocolRows });
  const cancelEditStart = Date.now();
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
  assert.deepEqual((await api(base + '/builder')).workspace, beforeEditWorkspace, `Cancel edit must retain the saved related ${relatedForm} operation`);
  record(`cancel-related-${relatedFormName}-label-edit`, cancelEditStart);

  await click(page, `[data-testid="construction-history-step-${savedStep.id}"]`);
  await click(page, `[data-testid="construction-edit-step-${savedStep.id}"]`);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector(args[0])?.matches(":not(:disabled)"))); }, [labelSelector]);
  const confirmedEditStart = Date.now();
  await fill(page, labelSelector, editedLabel);
  const confirmedEditProposal = await waitNative('/construction-proposals', confirmedEditStart, entry => entry.status === 200);
  const confirmedEditResponse = protocolResponse(confirmedEditProposal);
  const confirmedEditStep = confirmedEditResponse.candidateConstruction.steps.find(step => step.id === savedStep.id);
  const confirmedEditOutput = confirmedEditStep.outputs.find(column => column.id === savedStep.operation.relatedSource.outputColumnId);
  assertTypedNamedGroupRows(confirmedEditResponse.preview?.rows, expectedGroupCounts, cohort.revisionId, confirmedEditOutput.name);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-proposal-ready"]'))); });
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
  await click(page, `[data-testid="construction-history-step-${savedStep.id}"]`);
  const removeStart = Date.now();
  await click(page, `[data-testid="construction-remove-step-${savedStep.id}"]`);
  const removeProposal = await waitNative('/construction-proposals', removeStart, entry => entry.status === 200);
  const removeResponse = protocolResponse(removeProposal);
  assert.equal(removeResponse.previewStatus, 'READY', JSON.stringify(removeProposal.response));
  const removeProtocolRows = assertTypedNamedGroupRows(removeResponse.preview?.rows, expectedGroupCounts, cohort.revisionId);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-proposal-ready"]'))); });
  const removeView = await browserEval(page, (args) => { const preview=document.querySelector('[data-testid="construction-proposal-preview"]');return {headers:[...preview.querySelectorAll('th')].map(header=>header.firstElementChild?.textContent?.trim()??header.innerText.trim()),rows:[...preview.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}; });
  assert.equal(removeResponse.candidateConstruction.steps.length, 0, 'Removing the authored Add column must restore the construction-free cohort');
  assertNamedGroupRows(removeView, expectedGroupCounts);
  record(`remove-related-${relatedFormName}-preview`, removeStart, { headers: removeView.headers, rows: removeView.rows, protocolRows: removeProtocolRows });
  const cancelRemoveStart = Date.now();
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(page, (args) => { return Boolean((!document.querySelector('[data-testid="construction-proposal-panel"]'))); });
  assert.deepEqual((await api(base + '/builder')).workspace, editedWorkspace, `Cancel removal must retain the renamed related ${relatedForm} output`);
  record(`cancel-related-${relatedFormName}-removal`, cancelRemoveStart);

  await click(page, `[data-testid="construction-history-step-${savedStep.id}"]`);
  const confirmedRemoveStart = Date.now();
  await click(page, `[data-testid="construction-remove-step-${savedStep.id}"]`);
  const confirmedRemoveProposal = await waitNative('/construction-proposals', confirmedRemoveStart, entry => entry.status === 200);
  assertTypedNamedGroupRows(protocolResponse(confirmedRemoveProposal).preview?.rows, expectedGroupCounts, cohort.revisionId);
  await waitForBrowser(page, (args) => { return Boolean((document.querySelector('[data-testid="construction-proposal-ready"]'))); });
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
  report.status = 'passed';
} catch (error) {
  fatal = error;
  const invalidated = Boolean(error.invalidatesRun);
  report.status = invalidated ? 'invalidated' : error instanceof BoundedAbsenceError ? 'bounded-absence' : error instanceof UnsupportedCapabilityError ? 'unsupported' : 'failed';
  report.error = String(error.stack ?? error);
  if (error instanceof UnsupportedCapabilityError) report.unsupportedEvidence = error.evidence;
  else if (error instanceof BoundedAbsenceError) report.boundedAbsence = error.evidence;
  report.failureUI = await browserEval(page, () => { return document.body.innerText; }).catch(String);
} finally {
  await nativeCapture?.flush();
  report.finished = new Date().toISOString();
  report.nativeRequestSummary = report.nativeRequests.map(({ path, method, status, body, response, failure }) => ({ path, method, status, body, response, failure }));
  await cda.attachReport('named-cohort-related-count-domain-report.json', report);
}

if (fatal || report.status !== 'passed') throw fatal ?? new Error(report.oracle?.explanation ?? report.error ?? 'Named cohort related workflow did not pass');
return report;
}
