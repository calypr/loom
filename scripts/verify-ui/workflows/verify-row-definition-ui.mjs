import assert from 'node:assert/strict';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

export async function verifyRowDefinitionUI({ page, cda, source: sourceInput, selectionEvidencePath }) {
const source = sourceInput ?? JSON.parse(readFileSync(selectionEvidencePath, 'utf8'));
const target = { ...(source.target ?? cda.target), explorerId: source.explorerId ?? cda.explorer };
assert.ok(target.apiUrl && target.uiUrl && target.project && target.explorerId, 'evidence has no browser target');
assert.equal(target.project, cda.project, 'selection evidence must belong to the owned CDA project');
assert.equal(target.explorerId, cda.explorer, 'selection evidence must belong to the owned CDA explorer');
const apiURL = new URL(target.apiUrl);
const uiURL = new URL(target.uiUrl);
assert.equal(apiURL.hostname, '127.0.0.1', 'row-definition verification only operates on the isolated local API');
assert.equal(uiURL.hostname, '127.0.0.1', 'row-definition verification only operates on the isolated local UI');
assert.match(target.project, /^loom_dev_verify_[a-z0-9]+-[a-f0-9]+$/, 'row-definition verification requires a disposable verification project');
assert.match(target.explorerId, /^loom-dev-verification-selection-[a-f0-9-]+$/, 'row-definition verification requires its disposable selection explorer');

const authoring = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}/authoring/v2`;
const json = async (url, body) => {
  const response = await fetch(url, {
    ...(body === undefined ? {} : {
      method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body),
    }),
    signal: AbortSignal.timeout(30_000),
  });
  const value = await response.json();
  assert.ok(response.ok, `${url}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};

let builder = await json(`${authoring}/builder`);
const command = async (commands) => {
  const commandId = crypto.randomUUID();
  const result = await json(`${authoring}/commands`, {
    commandId,
    semanticsVersion: 4,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await json(`${authoring}/builder`);
  return result;
};

for (const document of builder.workspace?.documents ?? []) {
  await command([{ type: 'DELETE_TABLE', outputId: document.output.id }]);
}
const specimenNode = builder.catalog.nodes.find((node) => node.resourceType === 'Specimen' && node.rowRootEligible);
const observationNode = builder.catalog.nodes.find((node) => node.resourceType === 'Observation' && node.rowRootEligible);
assert.ok(specimenNode && observationNode, 'fixture lacks row-eligible Specimen or Observation resources');
const forwardEdge = builder.catalog.edges.find((edge) => edge.fromNodeId === specimenNode.nodeId && edge.toNodeId === observationNode.nodeId && edge.populated !== false);
const reverseEdge = builder.catalog.edges.find((edge) => edge.fromNodeId === observationNode.nodeId && edge.toNodeId === specimenNode.nodeId && edge.populated !== false);
assert.ok(forwardEdge && reverseEdge, 'fixture lacks a bidirectional Specimen/Observation route');

const created = await command([{ type: 'CREATE_TABLE', title: 'Laboratory observations', rootNodeId: specimenNode.nodeId }]);
const outputId = created.results.find((result) => result.type === 'TABLE_CREATED')?.outputId;
assert.ok(outputId, 'CREATE_TABLE returned no output ID');
const route = await command([{ type: 'ADD_ROUTE', outputId, parentOccurrenceId: 'base', edgeId: forwardEdge.edgeId }]);
const observationOccurrenceId = route.results.find((result) => result.type === 'ROUTE_ADDED')?.occurrenceId;
assert.ok(observationOccurrenceId, 'ADD_ROUTE returned no Observation occurrence ID');
await command([
  { type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: 'base', title: 'Specimen ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
  { type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: observationOccurrenceId, title: 'Observation ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
]);
const before = builder;
const original = before.workspace.documents.find((document) => document.output.id === outputId);
assert.ok(original && !original.population, 'row-definition journey must start without a file population');
const featureKeys = original.columns.map((column) => column.column);

const query = new URLSearchParams({ project: target.project, explorer: target.explorerId, mode: 'builder' });
const artifactRoot = join(cda.evidence, `row-definition-ui-${Date.now()}`);
const htmlPath = `${artifactRoot}.html`;
const reportPath = `${artifactRoot}.json`;
mkdirSync(cda.evidence, { recursive: true });
const browserFailures = [];
const authoringResponses = [];
const rowDefinitionSelector = 'select[aria-label="One row per"]';
let evidence;
const selectObservationRows = async () => {
  await cda.wait(() => [...document.querySelectorAll('select[aria-label="One row per"] option')]
    .some(option => option.textContent.includes('Observation')));
  const options = await cda.inspect(() => [...document.querySelectorAll('select[aria-label="One row per"] option')]
    .filter(option => option.textContent.includes('Observation'))
    .map(option => ({ value: option.value, text: option.textContent.trim(), disabled: option.disabled })));
  assert.equal(options.length, 1, `expected one Observation row option, found ${options.length}`);
  assert.equal(options[0].disabled, false, 'Observation row option is disabled');
  const startedAt = Date.now();
  await cda.selectOption(rowDefinitionSelector, options[0].value);
  await waitForObservationRows(Math.max(1, startedAt + 5000 - Date.now()));
  assert(Date.now() <= startedAt + 5000, 'row definition selection exceeded its five-second action-to-render budget');
};
const waitForObservationRows = async (timeout = 5000) => {
  await cda.wait(() =>
    document.querySelector('select[aria-label="One row per"] option:checked')?.textContent.trim() === 'Observation', [], timeout);
};
const previewAndWait = async () => {
  const startedAt = Date.now();
  await cda.action('Preview row definition', page.getByRole('button', { name: 'Preview', exact: true }), target => target.click());
  await cda.wait(() => document.body.innerText.includes('dev-pair-001'), [], Math.max(1, startedAt + 5000 - Date.now()));
  assert(Date.now() <= startedAt + 5000, 'Preview exceeded its five-second action-to-render budget');
};
page.on('pageerror', error => browserFailures.push(error.message));
page.on('console', message => { if (message.type() === 'error') browserFailures.push(message.text()); });
page.on('response', response => {
  const responseURL = new URL(response.url());
  if (response.url().includes('/authoring/v2/')) authoringResponses.push(`${response.status()} ${responseURL.pathname}`);
  if (response.status() >= 400 && responseURL.pathname !== '/favicon.ico') browserFailures.push(`${response.status()} ${response.url()}`);
});

try {
  await cda.navigate(`${target.uiUrl}/?${query}`);
  await selectObservationRows();

  const after = await json(`${authoring}/builder`);
  const rebased = after.workspace.documents.find((document) => document.output.id === outputId);
  assert.ok(rebased, 'row-defined table disappeared');
  assert.equal(rebased.rootResourceType, 'Observation');
  assert.equal(rebased.route.children?.[0]?.resourceType, 'Specimen');
  assert.equal(rebased.population, undefined, 'row change introduced a file population');
  assert.deepEqual(rebased.columns.map((column) => column.column), featureKeys, 'row change replaced stable feature keys');
  assert.equal(after.draftVersion, before.draftVersion + 1, 'row definition must create exactly one draft version');

  await previewAndWait();
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
  await waitForObservationRows();

  builder = await json(`${authoring}/builder`);
  await command([{ type: 'DELETE_TABLE', outputId }]);
  const documentNode = builder.catalog.nodes.find((node) => node.resourceType === 'DocumentReference' && node.rowRootEligible);
  assert.ok(documentNode, 'fixture lacks a row-eligible DocumentReference resource');
  const fileToSpecimen = builder.catalog.edges.find((edge) => edge.fromNodeId === documentNode.nodeId && edge.toNodeId === specimenNode.nodeId && edge.populated !== false);
  const specimenToFile = builder.catalog.edges.find((edge) => edge.fromNodeId === specimenNode.nodeId && edge.toNodeId === documentNode.nodeId && edge.populated !== false);
  assert.ok(fileToSpecimen && specimenToFile, 'fixture lacks a bidirectional DocumentReference/Specimen route');
  const deepCreated = await command([{ type: 'CREATE_TABLE', title: 'File-derived observations', rootNodeId: documentNode.nodeId }]);
  const deepOutputId = deepCreated.results.find((result) => result.type === 'TABLE_CREATED')?.outputId;
  assert.ok(deepOutputId, 'deep CREATE_TABLE returned no output ID');
  await command([{ type: 'SET_TABLE_POPULATION', outputId: deepOutputId, selectionRevisionId: source.selections.explicit.id, edgeIds: [] }]);
  const specimenRoute = await command([{ type: 'ADD_ROUTE', outputId: deepOutputId, parentOccurrenceId: 'base', edgeId: fileToSpecimen.edgeId }]);
  const specimenOccurrenceId = specimenRoute.results.find((result) => result.type === 'ROUTE_ADDED')?.occurrenceId;
  assert.ok(specimenOccurrenceId, 'deep ADD_ROUTE returned no Specimen occurrence ID');
  const observationRoute = await command([{ type: 'ADD_ROUTE', outputId: deepOutputId, parentOccurrenceId: specimenOccurrenceId, edgeId: forwardEdge.edgeId }]);
  const deepObservationOccurrenceId = observationRoute.results.find((result) => result.type === 'ROUTE_ADDED')?.occurrenceId;
  assert.ok(deepObservationOccurrenceId, 'deep ADD_ROUTE returned no Observation occurrence ID');
  await command([
    { type: 'ADD_COLUMN_SOURCE', outputId: deepOutputId, occurrenceId: 'base', title: 'File ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
    { type: 'ADD_COLUMN_SOURCE', outputId: deepOutputId, occurrenceId: specimenOccurrenceId, title: 'Specimen ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
    { type: 'ADD_COLUMN_SOURCE', outputId: deepOutputId, occurrenceId: deepObservationOccurrenceId, title: 'Observation ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
  ]);
  const deepBefore = builder;
  const deepOriginal = deepBefore.workspace.documents.find((document) => document.output.id === deepOutputId);
  assert.ok(deepOriginal, 'deep row-definition table disappeared before rebase');
  assert.equal(deepOriginal.population?.selectionRevisionId, source.selections.explicit.id, 'deep journey did not attach the file selection');
  const deepFeatureKeys = deepOriginal.columns.map((column) => column.column);

  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
  await selectObservationRows();
  const deepAfter = await json(`${authoring}/builder`);
  const deepRebased = deepAfter.workspace.documents.find((document) => document.output.id === deepOutputId);
  assert.ok(deepRebased, 'deep row-defined table disappeared');
  assert.equal(deepRebased.rootResourceType, 'Observation');
  assert.equal(deepRebased.route.children?.[0]?.resourceType, 'Specimen');
  assert.equal(deepRebased.route.children?.[0]?.children?.[0]?.resourceType, 'DocumentReference');
  assert.equal(deepRebased.population?.selectionRevisionId, source.selections.explicit.id, 'deep row change lost the immutable selection');
  assert.deepEqual(deepRebased.population?.route.map((step) => step.resourceType), ['Specimen', 'DocumentReference'], 'deep row change did not extend the population route through both former ancestors');
  assert.deepEqual(deepRebased.columns.map((column) => column.column), deepFeatureKeys, 'deep row change replaced stable feature keys');
  assert.equal(deepAfter.draftVersion, deepBefore.draftVersion + 1, 'deep row definition must create exactly one draft version');
  await previewAndWait();
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
  await waitForObservationRows();
  assert.equal(browserFailures.length, 0, `browser failures: ${browserFailures.join(' | ')}`);
  writeFileSync(htmlPath, await page.content(), { mode: 0o600 });

  evidence = {
    status: 'passed', scenario: 'explicit-shallow-and-deep-row-definition',
    target, outputId, deepOutputId,
    before: { draftVersion: before.draftVersion, rootResourceType: original.rootResourceType, featureKeys },
    after: { draftVersion: after.draftVersion, rootResourceType: rebased.rootResourceType, featureKeys: rebased.columns.map((column) => column.column) },
    deep: {
      before: { draftVersion: deepBefore.draftVersion, rootResourceType: deepOriginal.rootResourceType, featureKeys: deepFeatureKeys },
      after: { draftVersion: deepAfter.draftVersion, rootResourceType: deepRebased.rootResourceType, featureKeys: deepRebased.columns.map((column) => column.column) },
    },
    assertions: [
      'Builder exposes an explicit One row per control outside the graph',
      'Specimen rows change to Observation rows without Patient or DocumentReference input',
      'stable Specimen and Observation feature keys survive the row change',
      'Preview returns fixture Observation rows',
      'the Observation row definition survives a full reload',
      'the same control promotes an Observation two authored relationships below DocumentReference',
      'the deep rebase preserves File, Specimen, and Observation feature keys and reverses both route edges',
      'the immutable file selection survives with a two-step population route to Observation rows',
      'Preview and reload succeed after the deep row rebase',
    ],
    authoringResponses, evidencePaths: [htmlPath],
  };
  evidence.evidencePaths = [htmlPath, reportPath];
  writeFileSync(reportPath, JSON.stringify(evidence, null, 2), { mode: 0o600 });
  cda.check('correctness', 'shallow and deep row definitions preserve feature keys and population', true,
    { outputId, deepOutputId, featureKeys, deepFeatureKeys });
} catch (error) {
  writeFileSync(htmlPath, await page.content(), { mode: 0o600 });
  const reason = error instanceof Error ? error.message : String(error);
  throw new Error(`${reason}; authoring responses: ${authoringResponses.slice(-12).join(' | ') || 'none'}; browser failures: ${browserFailures.slice(-5).join(' | ') || 'none'}`);
} finally {
  if (evidence) await cda.attachReport('row-definition-ui', evidence);
}
return evidence;
}
