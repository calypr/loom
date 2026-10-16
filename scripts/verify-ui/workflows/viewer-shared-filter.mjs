import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readViewerSharedFilterOracle } from '../helpers/viewer-shared-filter-oracle.mjs';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { browserURL } from './builder-url.mjs';

const graphPath = '/graphql/graph';
const outputIds = { all: 'patients_all', second: 'patient_002_only' };
const outputTitles = { all: 'All Patients', second: 'Patient 002 only' };
const sharedName = 'Patient cohort';
const sharedColumn = 'patient_id';
export const repositoryConfigExplorerId = 'default';
const runtimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
const repositoryConfigPath = (project, generation) =>
  `/api/v1/projects/${encodeURIComponent(project)}/generations/${encodeURIComponent(generation)}/explorer-config`;

export const repositoryConfigResponseMatches = ({ ok, body, project, generation }) =>
  ok === true && body?.project === project && body?.generation === generation &&
  body?.explorerId === repositoryConfigExplorerId && body?.activated === true &&
  ['receiptId', 'revisionId', 'executionId'].every(key => typeof body?.[key] === 'string' && body[key].trim().length > 0);

export const runtimeBindingsMatch = (actual, expected) => Array.isArray(actual) &&
  actual.length === expected.length &&
  actual.every((binding, index) => binding?.outputId === expected[index]?.outputId &&
    binding?.column === expected[index]?.column);

const patientDocument = (id, title, fixedFilters = []) => ({
  kind: 'ExplorerBuilderDocument',
  output: { id, title, rowLabel: title },
  rootResourceType: 'Patient',
  route: { occurrenceId: 'base', resourceType: 'Patient' },
  rows: { kind: 'RECORDS', records: {} },
  columns: [{
    columnId: `${id}-patient-id`,
    column: sharedColumn,
    label: 'Patient ID',
    logicalType: 'string',
    occurrenceId: 'base',
    source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
    table: { visible: true, order: 0 },
    filter: { label: 'Patient ID', order: 0 },
  }],
  ...(fixedFilters.length ? { fixedFilters } : {}),
});

export const workspaceForSharedFilter = () => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderWorkspace',
  semanticsVersion: 10,
  explorer: { title: 'Shared Patient cohort' },
  documents: [
    patientDocument(outputIds.all, outputTitles.all),
    patientDocument(outputIds.second, outputTitles.second, [{ column: sharedColumn, values: ['dev-patient-002'] }]),
  ],
  tabs: [
    { id: 'all-patients-tab', title: outputTitles.all, outputId: outputIds.all, order: 0, visible: true },
    { id: 'patient-002-tab', title: outputTitles.second, outputId: outputIds.second, order: 1, visible: true },
  ],
  sharedFilters: {
    [sharedName]: [
      { outputId: outputIds.all, column: sharedColumn },
      { outputId: outputIds.second, column: sharedColumn },
    ],
  },
});

const inputFor = entry => entry?.request?.variables?.input;
const normalizedFilters = input => Array.isArray(input?.filters) ? input.filters : [];
const sameSelector = (actual, expected) => actual?.recipe === expected?.recipe &&
  actual?.translationVersion === expected?.translationVersion && actual?.output === expected?.output;

const graphEntryFor = (observer, { fromIndex, project, outputId, filters, facetName }) =>
  observer.waitForResponse(entry => {
    const input = inputFor(entry);
    if (entry.method !== 'POST' || entry.path !== graphPath || input?.projectId !== project ||
      input?.selector?.output !== outputId || !Array.isArray(input.columns) || !input.columns.includes(sharedColumn)) return false;
    // Exact recipe and translation version are checked against the runtime after it loads.
    if (input.selector?.output !== outputId) return false;
    if (filters && JSON.stringify(normalizedFilters(input)) !== JSON.stringify(filters)) return false;
    if (facetName) {
      const facetInput = entry.request?.variables?.facetInput;
      return facetInput?.projectId === project && facetInput?.selector?.output === outputId &&
        facetInput.specs?.some(spec => spec.name === facetName && spec.column === sharedColumn && spec.kind === 'TERMS');
    }
    return true;
  }, { fromIndex, timeoutMs: 5_000 });

const waitForPatientIDs = async (page, title, expectedIDs) => {
  await page.waitForFunction(({ title: wantedTitle, ids }) => {
    const table = [...document.querySelectorAll('table[aria-label$=" results"]')]
      .find(candidate => candidate.getAttribute('aria-label') === `${wantedTitle} results`);
    if (!table) return false;
    const headerCells = [...table.querySelectorAll('thead th')];
    const patientIDColumn = headerCells.findIndex(header => header.textContent?.trim() === 'Patient ID');
    if (patientIDColumn < 0) return false;
    const outputSection = table.closest('section[aria-label]');
    if (outputSection?.getAttribute('aria-label') !== wantedTitle) return false;
    const updating = [...outputSection.querySelectorAll('[role="status"]')]
      .some(status => status.innerText.includes('Updating results…'));
    const patientIDs = [...table.querySelectorAll('tbody tr')]
      .map(row => row.querySelectorAll('td')[patientIDColumn]?.innerText.trim() ?? '').sort();
    return !updating && JSON.stringify(patientIDs) === JSON.stringify([...ids].sort());
  }, { title, ids: expectedIDs }, { timeout: 5_000 });
  const table = page.locator(`table[aria-label="${title} results"]`);
  const patientIDColumn = await table.locator('thead th').evaluateAll(headers =>
    headers.findIndex(header => header.textContent?.trim() === 'Patient ID'));
  assert.notEqual(patientIDColumn, -1, `${title} must expose its exact Patient ID column`);
  const actualIDs = await table.locator('tbody tr').evaluateAll((rows, index) =>
    rows.map(row => row.querySelectorAll('td')[index]?.innerText.trim() ?? '').sort(), patientIDColumn);
  assert.deepEqual(actualIDs, [...expectedIDs].sort(), `${title} must show the exact independent Patient ID cell values`);
  return actualIDs;
};

const waitForNoPatientRows = async (page, title) => {
  await page.waitForFunction(wantedTitle => {
    const table = [...document.querySelectorAll('table[aria-label$=" results"]')]
      .find(candidate => candidate.getAttribute('aria-label') === `${wantedTitle} results`);
    if (!table) return false;
    const outputSection = table.closest('section[aria-label]');
    if (outputSection?.getAttribute('aria-label') !== wantedTitle) return false;
    const updating = [...outputSection.querySelectorAll('[role="status"]')]
      .some(status => status.innerText.includes('Updating results…'));
    return table.querySelectorAll('tbody tr').length === 0 &&
      !updating && outputSection.innerText.includes('No rows match the current filters.');
  }, title, { timeout: 5_000 });
  const table = page.locator(`table[aria-label="${title} results"]`);
  assert.equal(await table.locator('tbody tr').count(), 0, `${title} must be empty after the shared Patient ID filter`);
};

const assertGraphRequest = (check, entry, { name, project, selector, outputId, filters }) => {
  const input = inputFor(entry);
  const actualFilters = normalizedFilters(input);
  const body = entry?.response;
  const noGraphQLErrors = !Array.isArray(body?.errors) || body.errors.length === 0;
  const passed = entry?.status >= 200 && entry.status < 300 && input?.projectId === project &&
    sameSelector(input?.selector, selector) && selector?.output === outputId &&
    JSON.stringify(actualFilters) === JSON.stringify(filters) && noGraphQLErrors;
  check('correctness', name, passed, {
    status: entry?.status ?? null,
    project: input?.projectId ?? null,
    outputId: input?.selector?.output ?? null,
    filters: actualFilters,
    expectedFilters: filters,
    graphQLErrors: Array.isArray(body?.errors) ? body.errors.map(error => error?.message ?? 'GraphQL error').slice(0, 4) : [],
  });
  assert(passed, `${name} must use the exact successful project, output selector, and filter values`);
};

const performQueryAction = async ({ action, label, locator, perform, observer, report, project, output, filters, afterRows, afterEmpty }) => {
  report.target.viewerOutputId = output.outputId;
  const fromIndex = observer.requests.length;
  const query = graphEntryFor(observer, { fromIndex, project, outputId: output.outputId, filters });
  let entry;
  await action(label, locator, perform, {
    timeout: 5_000,
    budget: 5_000,
    after: async () => {
      entry = await query;
      if (afterRows) await waitForPatientIDs(afterRows.page, output.title, afterRows.ids);
      if (afterEmpty) await waitForNoPatientRows(afterEmpty.page, output.title);
    },
  });
  return entry;
};

const switchOutput = async ({ page, action, observer, report, project, output, filters, afterRows, afterEmpty }) => {
  const tab = page.getByRole('tab', { name: output.title });
  return performQueryAction({
    action,
    label: `switch to ${output.title} and settle its exact rows`,
    locator: tab,
    perform: () => tab.click(),
    observer,
    report,
    project,
    output,
    filters,
    afterRows: { page, ids: afterRows ?? [] },
    afterEmpty: afterEmpty ? { page } : undefined,
  });
};

const switchCachedOutput = async ({ page, action, report, output, afterRows }) => {
  report.target.viewerOutputId = output.outputId;
  const tab = page.getByRole('tab', { name: output.title });
  let actualIDs;
  await action(`return to ${output.title} and verify its restored rows`, tab, () => tab.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: async () => { actualIDs = await waitForPatientIDs(page, output.title, afterRows); },
  });
  return actualIDs;
};

const readRuntime = (target, observer, project, explorer, page) => {
  const fromIndex = observer.requests.length;
  const response = observer.waitForResponse(entry => entry.method === 'GET' && entry.path === runtimePath(target.fixtureProject, explorer),
    { fromIndex, timeoutMs: 5_000 });
  return { response, navigate: page.goto(browserURL(target, target.fixtureProject, explorer, 'viewer'), { waitUntil: 'domcontentloaded' }), project };
};

export const viewerSharedFilterWorkflow = async ({ page, report, action, check, networkObserver }, context) => {
  const target = context.target;
  const fixture = readViewerSharedFilterOracle(target);
  const sourceHashBefore = fixture.sha256;
  const project = canonicalProjectID(target.fixtureProject);
  const graphProject = target.fixtureProject;
  const generation = target.fixtureGeneration;
  // Repository-owned configuration is published to the repository's `default`
  // Explorer; the fixture's interactive bootstrap Explorer is a separate ID.
  const explorer = repositoryConfigExplorerId;
  const fixtureIsExact = fixture.allIDs.join('\u0000') === 'dev-patient-001\u0000dev-patient-002' &&
    fixture.selectedID === 'dev-patient-001' && fixture.secondIDs.join('\u0000') === 'dev-patient-002';
  check('correctness', 'shared filter fixture contains the exact independent Patient identities', fixtureIsExact, {
    path: fixture.path,
    sha256: fixture.sha256,
    patientIDs: fixture.allIDs,
    selectedSharedID: fixture.selectedID,
    secondOutputBaseID: fixture.secondIDs,
  });
  assert(fixtureIsExact, 'shared-filter fixture oracle must remain exact');
  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: target.sourceRoot, encoding: 'utf8' }).trim();
  const configPath = repositoryConfigPath(target.fixtureProject, generation);
  const seedResponse = await fetch(`${target.apiUrl}${configPath}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Loom-Source-Commit': sourceCommit },
    body: JSON.stringify(workspaceForSharedFilter()),
    signal: AbortSignal.timeout(30_000),
  });
  const seedText = await seedResponse.text();
  let seedBody;
  try { seedBody = JSON.parse(seedText); } catch { seedBody = { body: seedText.slice(0, 1_000) }; }
  const seedPassed = repositoryConfigResponseMatches({ ok: seedResponse.ok, body: seedBody, project, generation });
  check('correctness', 'two linked Patient outputs publish to the exact fixture project and generation', seedPassed, {
    httpStatus: seedResponse.status,
    project: seedBody.project ?? null,
    expectedProject: project,
    generation: seedBody.generation ?? null,
    expectedGeneration: generation,
    explorer: seedBody.explorerId ?? null,
    expectedExplorer: explorer,
    activated: seedBody.activated ?? null,
    receiptId: seedBody.receiptId ?? null,
    revisionId: seedBody.revisionId ?? null,
    executionId: seedBody.executionId ?? null,
    sourceCommit,
  });
  assert(seedPassed, 'shared-filter Viewer configuration must publish to its exact fixture scope');
  report.target.project = project;
  report.target.fixtureProjectAlias = target.fixtureProject;
  report.target.generation = generation;
  report.target.explorer = explorer;
  report.target.fixtureOracle = { path: fixture.path, sha256: fixture.sha256, patientIDs: fixture.allIDs, selectedFilterID: fixture.selectedID };
  report.target.repositoryConfigSeed = {
    project, generation, explorer, sourceCommit,
    receiptId: seedBody.receiptId, revisionId: seedBody.revisionId, executionId: seedBody.executionId,
  };

  report.target.viewerOutputId = outputIds.all;
  const initialOutputQuery = graphEntryFor(networkObserver, {
    fromIndex: networkObserver.requests.length,
    project: graphProject,
    outputId: outputIds.all,
    filters: [],
  });
  const initialNavigation = readRuntime(target, networkObserver, project, explorer, page);
  const [, runtimeEntry] = await Promise.all([initialNavigation.navigate, initialNavigation.response]);
  const initialEntry = await initialOutputQuery;
  const runtimeEnvelope = runtimeEntry.response;
  const runtime = runtimeEnvelope?.runtime;
  assert(runtime && Array.isArray(runtime.outputs), 'Viewer runtime must return its published output definitions');
  const runtimeGeneration = runtime.generation ?? runtime.publication?.generation;
  const runtimeScopePassed = runtimeEntry.status >= 200 && runtimeEntry.status < 300 &&
    runtimeEnvelope.project === project && runtimeEnvelope.explorerId === explorer && runtimeGeneration === generation;
  check('correctness', 'Viewer runtime retains the exact fixture project, generation, and Explorer', runtimeScopePassed, {
    status: runtimeEntry.status,
    project: runtimeEnvelope.project ?? null,
    expectedProject: project,
    explorer: runtimeEnvelope.explorerId ?? null,
    expectedExplorer: explorer,
    generation: runtimeGeneration ?? null,
    expectedGeneration: generation,
  });
  assert(runtimeScopePassed, 'Viewer runtime must remain bound to the exact fixture project, generation, and Explorer');
  const runtimePublication = runtime.publication ?? {};
  const runtimePublicationPassed = runtimeEnvelope.active?.status === 'ACTIVE' &&
    runtimeEnvelope.active?.revisionId === seedBody.revisionId && runtimePublication.state === 'ACTIVE' &&
    runtimePublication.revisionId === seedBody.revisionId && runtimePublication.executionId === seedBody.executionId &&
    runtimePublication.generation === generation;
  check('correctness', 'Viewer runtime serves the exact activated repository revision', runtimePublicationPassed, {
    activeStatus: runtimeEnvelope.active?.status ?? null,
    activeRevisionId: runtimeEnvelope.active?.revisionId ?? null,
    expectedRevisionId: seedBody.revisionId,
    publicationState: runtimePublication.state ?? null,
    publicationRevisionId: runtimePublication.revisionId ?? null,
    publicationExecutionId: runtimePublication.executionId ?? null,
    expectedExecutionId: seedBody.executionId,
    publicationGeneration: runtimePublication.generation ?? null,
    expectedGeneration: generation,
  });
  assert(runtimePublicationPassed, 'Viewer runtime must be the exact active repository publication');
  const allOutput = runtime.outputs.find(output => output.outputId === outputIds.all);
  const secondOutput = runtime.outputs.find(output => output.outputId === outputIds.second);
  const expectedBindings = [
    { outputId: outputIds.all, column: sharedColumn },
    { outputId: outputIds.second, column: sharedColumn },
  ];
  const exactBindings = runtimeBindingsMatch(runtime.sharedFilters?.[sharedName], expectedBindings);
  const exactOutputs = runtime.outputs.length === 2 && allOutput?.selector?.output === outputIds.all &&
    secondOutput?.selector?.output === outputIds.second &&
    JSON.stringify(secondOutput?.fixedFilters?.[sharedColumn]) === JSON.stringify(fixture.secondIDs) &&
    allOutput?.filters?.some(binding => binding.column === sharedColumn) &&
    secondOutput?.filters?.some(binding => binding.column === sharedColumn);
  check('correctness', 'runtime links the exact Patient ID facet to both outputs and retains the second output base filter', exactBindings && exactOutputs, {
    sharedBindings: runtime.sharedFilters?.[sharedName] ?? null,
    expectedBindings,
    outputIds: runtime.outputs.map(output => output.outputId),
    secondFixedFilter: secondOutput?.fixedFilters?.[sharedColumn] ?? null,
  });
  assert(exactBindings && exactOutputs, 'runtime must publish the exact two shared bindings and independent fixed output filter');
  report.target.outputIds = runtime.outputs.map(output => output.outputId);
  assertGraphRequest(check, initialEntry, {
    name: 'initial All Patients request is unfiltered and scoped to the published output',
    project: graphProject,
    selector: allOutput.selector,
    outputId: allOutput.outputId,
    filters: [],
  });
  await waitForPatientIDs(page, allOutput.title, fixture.allIDs);

  const secondInitialEntry = await switchOutput({
    page, action, observer: networkObserver, report, project: graphProject, output: secondOutput,
    filters: [{ column: sharedColumn, op: 'EQ', value: fixture.secondIDs[0] }],
    afterRows: fixture.secondIDs,
  });
  assertGraphRequest(check, secondInitialEntry, {
    name: 'initial Patient 002 only request retains its exact fixed base filter',
    project: graphProject,
    selector: secondOutput.selector,
    outputId: secondOutput.outputId,
    filters: [{ column: sharedColumn, op: 'EQ', value: fixture.secondIDs[0] }],
  });
  await switchCachedOutput({ page, action, report, output: allOutput, afterRows: fixture.allIDs });

  const sharedSection = page.locator(`section[aria-labelledby="${allOutput.outputId}:shared:${sharedName}-label"]`);
  const loadValues = sharedSection.getByRole('button', { name: 'Load values', exact: true });
  report.target.viewerOutputId = allOutput.outputId;
  const facetFrom = networkObserver.requests.length;
  const facetPromise = graphEntryFor(networkObserver, {
    fromIndex: facetFrom,
    project: graphProject,
    outputId: allOutput.outputId,
    filters: [],
    facetName: `loom:${allOutput.outputId}:${sharedColumn}`,
  });
  let facetEntry;
  await action('load Patient cohort facet values', loadValues, () => loadValues.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: async () => {
      facetEntry = await facetPromise;
      await sharedSection.getByRole('checkbox', { name: /dev-patient-001/ }).waitFor({ state: 'visible', timeout: 5_000 });
    },
  });
  const facetInput = facetEntry.request?.variables?.facetInput;
  const facetScoped = facetInput?.projectId === graphProject && facetInput?.selector?.output === allOutput.outputId &&
    facetInput.specs?.some(spec => spec.name === `loom:${allOutput.outputId}:${sharedColumn}` && spec.column === sharedColumn);
  check('correctness', 'Patient cohort facet values load for the exact project and All Patients selector', facetEntry.status >= 200 && facetEntry.status < 300 && facetScoped, {
    status: facetEntry.status,
    project: facetInput?.projectId ?? null,
    selector: facetInput?.selector?.output ?? null,
    facetColumns: facetInput?.specs?.map(spec => spec.column) ?? [],
  });
  assert(facetEntry.status >= 200 && facetEntry.status < 300 && facetScoped, 'shared facet query must remain project and output scoped');

  const selectedOption = sharedSection.getByRole('checkbox', { name: /dev-patient-001/ });
  await action('propose Patient 001 shared filter', selectedOption, () => selectedOption.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: () => page.getByRole('dialog', { name: 'Apply shared filter?' }).waitFor({ state: 'visible', timeout: 5_000 }),
  });
  report.target.viewerOutputId = allOutput.outputId;
  const requestsBeforeCancel = networkObserver.requests.filter(entry => entry.method === 'POST' && entry.path === graphPath).length;
  const cancel = page.getByRole('dialog', { name: 'Apply shared filter?' }).getByRole('button', { name: 'Cancel', exact: true });
  await action('cancel shared Patient filter proposal', cancel, () => cancel.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: async () => {
      await page.getByRole('dialog', { name: 'Apply shared filter?' }).waitFor({ state: 'hidden', timeout: 5_000 });
      await waitForPatientIDs(page, allOutput.title, fixture.allIDs);
    },
  });
  const requestsAfterCancel = networkObserver.requests.filter(entry => entry.method === 'POST' && entry.path === graphPath).length;
  const cancelUntouched = requestsAfterCancel === requestsBeforeCancel &&
    await page.getByRole('button', { name: `Remove ${fixture.selectedID}`, exact: true }).count() === 0;
  check('correctness', 'Cancel leaves the selected proposal unapplied and All Patients unchanged', cancelUntouched, {
    graphRequestsBeforeCancel: requestsBeforeCancel,
    graphRequestsAfterCancel: requestsAfterCancel,
    renderedIDs: fixture.allIDs,
  });
  assert(cancelUntouched, 'Cancel must leave shared selection unapplied and avoid a new graph query');
  const secondCancelledIDs = await switchCachedOutput({ page, action, report, output: secondOutput, afterRows: fixture.secondIDs });
  check('correctness', 'Cancel leaves Patient 002 only unchanged on the second output', secondCancelledIDs.join('\u0000') === fixture.secondIDs.join('\u0000'), { ids: secondCancelledIDs, expectedIDs: fixture.secondIDs });
  const firstCancelledIDs = await switchCachedOutput({ page, action, report, output: allOutput, afterRows: fixture.allIDs });
  check('correctness', 'Cancel leaves the complete All Patients base set unchanged', firstCancelledIDs.join('\u0000') === fixture.allIDs.join('\u0000'), { ids: firstCancelledIDs, expectedIDs: fixture.allIDs });

  const choosePatient001 = sharedSection.getByRole('checkbox', { name: /dev-patient-001/ });
  await action('propose Patient 001 shared filter again', choosePatient001, () => choosePatient001.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: () => page.getByRole('dialog', { name: 'Apply shared filter?' }).waitFor({ state: 'visible', timeout: 5_000 }),
  });
  const apply = page.getByRole('dialog', { name: 'Apply shared filter?' }).getByRole('button', { name: 'Apply filter', exact: true });
  const appliedAll = await performQueryAction({
    action,
    label: 'apply Patient 001 to All Patients',
    locator: apply,
    perform: () => apply.click(),
    observer: networkObserver,
    report,
    project: graphProject,
    output: allOutput,
    filters: [{ column: sharedColumn, op: 'EQ', value: fixture.selectedID }],
    afterRows: { page, ids: [fixture.selectedID] },
  });
  assertGraphRequest(check, appliedAll, {
    name: 'Apply sends Patient 001 as the exact All Patients shared filter',
    project: graphProject,
    selector: allOutput.selector,
    outputId: allOutput.outputId,
    filters: [{ column: sharedColumn, op: 'EQ', value: fixture.selectedID }],
  });
  check('correctness', 'All Patients displays only the independently selected Patient 001', true, { ids: [fixture.selectedID] });

  const appliedSecond = await switchOutput({
    page, action, observer: networkObserver, report, project: graphProject, output: secondOutput,
    filters: [
      { column: sharedColumn, op: 'EQ', value: fixture.secondIDs[0] },
      { column: sharedColumn, op: 'EQ', value: fixture.selectedID },
    ],
    afterEmpty: true,
  });
  assertGraphRequest(check, appliedSecond, {
    name: 'Apply sends the same shared Patient 001 filter to Patient 002 only',
    project: graphProject,
    selector: secondOutput.selector,
    outputId: secondOutput.outputId,
    filters: [
      { column: sharedColumn, op: 'EQ', value: fixture.secondIDs[0] },
      { column: sharedColumn, op: 'EQ', value: fixture.selectedID },
    ],
  });
  check('correctness', 'Patient 002 only is empty when the shared Patient 001 filter is applied', true, { ids: [], fixedPatientID: fixture.secondIDs[0], sharedPatientID: fixture.selectedID });

  report.target.viewerOutputId = secondOutput.outputId;
  const removeShared = page.getByRole('button', { name: `Remove ${fixture.selectedID}`, exact: true });
  await action('remove the shared Patient 001 filter', removeShared, () => removeShared.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: () => waitForPatientIDs(page, secondOutput.title, fixture.secondIDs),
  });
  const secondRestoredIDs = await waitForPatientIDs(page, secondOutput.title, fixture.secondIDs);
  check('correctness', 'removing the shared filter restores the second output base rows', secondRestoredIDs.join('\u0000') === fixture.secondIDs.join('\u0000'), { ids: secondRestoredIDs, expectedIDs: fixture.secondIDs });
  const restoredAllIDs = await switchCachedOutput({ page, action, report, output: allOutput, afterRows: fixture.allIDs });
  const sharedChipRemoved = await page.getByRole('button', { name: `Remove ${fixture.selectedID}`, exact: true }).count() === 0;
  check('correctness', 'removing the shared filter restores All Patients and removes its active chip', sharedChipRemoved && restoredAllIDs.join('\u0000') === fixture.allIDs.join('\u0000'), { ids: restoredAllIDs, expectedIDs: fixture.allIDs, sharedChipRemoved });

  const chooseForReload = sharedSection.getByRole('checkbox', { name: /dev-patient-001/ });
  await action('propose Patient 001 before reload reset', chooseForReload, () => chooseForReload.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: () => page.getByRole('dialog', { name: 'Apply shared filter?' }).waitFor({ state: 'visible', timeout: 5_000 }),
  });
  const applyBeforeReload = page.getByRole('dialog', { name: 'Apply shared filter?' }).getByRole('button', { name: 'Apply filter', exact: true });
  await action('apply Patient 001 before reload reset', applyBeforeReload, () => applyBeforeReload.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: () => waitForPatientIDs(page, allOutput.title, [fixture.selectedID]),
  });
  report.target.viewerOutputId = allOutput.outputId;
  const reloadFrom = networkObserver.requests.length;
  const reloadQuery = graphEntryFor(networkObserver, { fromIndex: reloadFrom, project: graphProject, outputId: allOutput.outputId, filters: [] });
  let reloadEntry;
  await action('reload Viewer and reset its transient shared filter', page.locator('body'), () => page.reload({ waitUntil: 'domcontentloaded' }), {
    timeout: 5_000,
    budget: 5_000,
    after: async () => {
      reloadEntry = await reloadQuery;
      await waitForPatientIDs(page, allOutput.title, fixture.allIDs);
    },
  });
  assertGraphRequest(check, reloadEntry, {
    name: 'reload resets transient shared selection and restores the unfiltered first output',
    project: graphProject,
    selector: allOutput.selector,
    outputId: allOutput.outputId,
    filters: [],
  });
  const noSharedChip = await page.getByRole('button', { name: `Remove ${fixture.selectedID}`, exact: true }).count() === 0;
  check('persistence', 'Viewer reload clears transient shared selection but retains the published linked outputs', noSharedChip, {
    sharedFilterSelected: !noSharedChip,
    outputIds: runtime.outputs.map(output => output.outputId),
    reloadExpectation: 'shared filter selection is in-memory and resets; published bindings and fixed filters remain',
  });
  const reloadedSecond = await switchOutput({
    page, action, observer: networkObserver, report, project: graphProject, output: secondOutput,
    filters: [{ column: sharedColumn, op: 'EQ', value: fixture.secondIDs[0] }],
    afterRows: fixture.secondIDs,
  });
  assertGraphRequest(check, reloadedSecond, {
    name: 'reload retains Patient 002 only base filter and removes transient shared selection',
    project: graphProject,
    selector: secondOutput.selector,
    outputId: secondOutput.outputId,
    filters: [{ column: sharedColumn, op: 'EQ', value: fixture.secondIDs[0] }],
  });
  check('correctness', 'reloaded Patient 002 only renders its exact fixed-filter Patient row', true, { ids: fixture.secondIDs });

  const finalOracle = readViewerSharedFilterOracle(target);
  check('persistence', 'shared filter lifecycle leaves the independent Patient fixture unchanged', finalOracle.sha256 === sourceHashBefore, {
    path: finalOracle.path,
    beforeSHA256: sourceHashBefore,
    afterSHA256: finalOracle.sha256,
    patientIDs: finalOracle.allIDs,
  });
  assert.equal(finalOracle.sha256, sourceHashBefore, 'Viewer lifecycle must not mutate the independent Patient fixture');
};
