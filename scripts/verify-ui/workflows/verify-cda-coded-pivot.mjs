import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertOwnedCdaTarget } from '../helpers/owned-cda-target.mjs';
import { startVerificationIdentity } from '../helpers/cda-verification-identity.mjs';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { DEFAULT_ACTION_TO_RENDER_BUDGET_MS, recordPivotActionToRender } from '../helpers/quantity-pivot-budget.mjs';
import {
  codedPivotFirstFailureEvidenceFor,
  codedPivotEditorDisposalCancellationEvidenceFor,
  codedPivotPolicyReplacementCancellationEvidenceFor,
  codedPivotPersistedSourceBindingsEqual,
  codedPivotPersistedSourceBindingsFor,
  codedPivotPersistedSourceMatchesOption,
  codedPivotProposalRequestMatches,
  codedPivotRemovalRequestMatches,
  codedPivotSourceOptionsDiagnosticFor,
  summarizeCodedPivotNativeRequests,
} from '../helpers/coded-pivot-native-evidence.mjs';
import { createdExplorerScope } from '../helpers/created-explorer-scope.mjs';
import { scenarioCaseFor } from '../registry.mjs';
import {
  CODED_PIVOT_OBSERVATION_ID,
  codedPivotExpectedHeaderValuesFor,
  codedPivotBackToTableControl,
  codedPivotFailureDomSnapshot,
  codedPivotFirstTableReady,
  codedPivotFixtureFor,
  codedPivotRemovalProposalReady,
  codedPivotRenderedValuesFor,
  codedPivotRestoredSourceRowVisible,
  codedPivotSourceControls,
  codedPivotSourceRadioFor,
  codedPivotValuesFor,
} from '../helpers/coded-pivot-fixture.mjs';


export async function runCodedPivotWorkflow({ page, cda }, originalArgs = {}) {
  const environment = cda.env ?? process.env;
const includeFixtureDiagnostics = domainReport => {
    const diagnostics = cda.diagnostics;
    domainReport.errors ??= [];
    const add = (entry, same) => { if (!domainReport.errors.some(same)) domainReport.errors.push(entry); };
    for (const failure of diagnostics.pageErrors ?? []) add({ kind: 'runtime', message: failure.message }, item => item.kind === 'runtime' && item.message === failure.message);
    for (const failure of diagnostics.console ?? []) add({ kind: 'console', message: failure.text, location: failure.location }, item => item.kind === 'console' && item.message === failure.text);
    for (const failure of diagnostics.networkFailures ?? []) add({ kind: 'network', path: failure.url, failure: failure.failure }, item => item.kind === 'network' && item.path === failure.url);
    for (const failure of diagnostics.httpFailures ?? []) add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, item => item.kind === 'http' && item.url === failure.url && item.status === failure.status);
    domainReport.incidentalErrors ??= [];
    for (const failure of diagnostics.assetFailures ?? []) if (!domainReport.incidentalErrors.some(item => item.url === failure.url && item.status === failure.status)) domainReport.incidentalErrors.push(failure);
  };
  const captureFailure = async (error, details = {}) => cda.attachReport('failure-evidence', { error: String(error), details, diagnostics: cda.diagnostics });
const waitNative = (callback, args = {}, timeout = 5000) => cda.wait(callback, args ?? {}, Math.min(timeout, 5000));
const project = cda.project;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const apiContainer = (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER);
const arangoContainer = (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER);
const composeProject = (cda.target.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT);
const sourceRoot = fileURLToPath(new URL('../../..', import.meta.url));
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, arangoContainer, composeProject, sourceRoot });
const verificationIdentity = await startVerificationIdentity(sourceRoot, apiContainer);
const observationId = CODED_PIVOT_OBSERVATION_ID;
const mode = originalArgs.mode ?? originalArgs[0];
const expected = codedPivotFixtureFor(mode);
const requiredChecks = scenarioCaseFor('standalone-reshape-coded-pivot', `coded-pivot-${mode}`).requiredChecks;
const requestedExplorerName = cda.explorer;
let explorerId;
const tableName = `Coded pivot ${mode} QA ${Date.now()}`;
const evidence = cda.evidence;
const artifact = join(evidence, `bounded-coded-pivot-${mode}.json`);
const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const report = { requestedExplorerName, explorerId: null, tableName, observationId, project, mode, expected, clicks: 0, timingsMs: {}, timingCheckpoints: [], requests: [], nativeRequests: [], errors: [], failures: [] };

let requestCapture;
let sourceOptionsRequest;
let sourceOptionsRequestBody;
let sourceOptionsResponseBody;
let sourceOptionsEndpointPath;
let semanticInventoryEndpointPath;
let created = false;
let selectedURL;
let editorDisposalWindow;
const tracker = { activeAction: undefined, actions: [] };
const acceptedExpectedCancellationRequestIds = [];
const acceptedEditorDisposalRequestIds = [];
const acceptedEditorDisposalBrowserRequestIds = [];
const policyReplacementCancellationsFor = actionLabel => (cda.report.expectedCancellations ?? []).filter(entry =>
  entry?.proof?.contract === 'coded-pivot-policy-replacement' && entry.proof.actionLabel === actionLabel);
const trackedAction = async (label, locator, perform, options = {}) => {
  tracker.activeAction = { label, startedAt: Date.now() };
  const elapsedMs = await cda.action(label, locator, perform, { timeout: DEFAULT_ACTION_TO_RENDER_BUDGET_MS, budget: DEFAULT_ACTION_TO_RENDER_BUDGET_MS, ...options });
  tracker.actions.push({ label, elapsedMs });
  tracker.activeAction = undefined;
  return elapsedMs;
};
const action = async (label, locator) => {
  report.clicks++;
  return trackedAction(label, locator, target => target.click({ timeout: 5000 }));
};
const fill = async (label, locator, value) => trackedAction(label, locator,
  target => target.fill(value, { timeout: 5000 }), { editable: true });
const select = async (label, locator, value) => trackedAction(label, locator,
  target => target.selectOption(value, { timeout: 5000 }));
const timing = (name, started) => {
  const checkpoint = recordPivotActionToRender({
    cases: report.timingCheckpoints,
    name: `${name}-to-render`,
    startedAt: started,
    budgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS,
  });
  report.timingsMs[checkpoint.name] = checkpoint.durationMs;
  return checkpoint;
};
const recordCheck = (index, dimension, passed, evidence = {}) => cda.check(dimension, requiredChecks[index], passed, evidence);
const documentFor = state => state.workspace.documents.find(document => document.output.title === tableName);
const codedPivotBindingsFor = step => ({
  stepId: step.id,
  inputs: step.inputs,
  source: codedPivotPersistedSourceBindingsFor(step),
  categories: step.operation.codedPivot.categories.map(({ system, code, outputColumnId }) => ({ system, code, outputColumnId }))
    .sort((left, right) => `${left.system}|${left.code}`.localeCompare(`${right.system}|${right.code}`)),
  outputs: step.outputs.map(({ id, name, label, type }) => ({ id, name, label, type })).sort((left, right) => left.id.localeCompare(right.id)),
});
const waitForRenderedTable = async codedStep => {
  const expectedColumns = codedPivotExpectedHeaderValuesFor(codedStep, expected);
  await waitNative(({ expectedColumns: expected }) => {
    const preview = document.querySelector('[data-testid="preview-table-scroll"]');
    const table = preview?.querySelector('[role="table"]');
    if (table?.getAttribute('aria-rowcount') !== '2') return false;
    const headers = [...preview.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim().split(/\r?\n/, 1)[0].replace(/\s+/g, ' ').trim().toLowerCase());
    const rows = [...preview.querySelectorAll('[role="row"]')].slice(1)
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
    return rows.length === 1 && expected.every(({ label, value }) => {
      const matches = headers.flatMap((header, index) => header === String(label).replace(/\s+/g, ' ').trim().toLowerCase() ? [index] : []);
      return matches.length === 1 && rows[0][matches[0]] === String(value);
    });
  }, { expectedColumns });
  return cda.inspect(() => ({
    headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText),
    rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText)),
  }));
};
const api = async (path, body) => {
  const requestId = `coded-pivot-${Date.now()}-${report.requests.length + 1}`;
  const startedAt = Date.now();
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, method: body === undefined ? 'GET' : 'POST', requestId, status: response.status, elapsedMs: Date.now() - startedAt, request: body, response: value });
  assert(response.ok, `${path}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};
const rawObservation = () => {
  const query = `FOR d IN Observation FILTER d.id == ${JSON.stringify(observationId)} AND d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(report.generation)} LIMIT 2 RETURN {id:d.id,project:d.project,generation:d.dataset_generation,resourceType:d.payload.resourceType,status:d.payload.status,component:d.payload.component,code:d.payload.code,valueQuantity:d.payload.valueQuantity}`;
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('docker', ['exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', script], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Arango returned no JSON array: ${result.stdout.slice(-1000)}`);
  const matches = JSON.parse(result.stdout.slice(start));
  assert.equal(matches.length, 1, 'The project/generation/ID source oracle must resolve exactly one Observation');
  const source = matches[0];
  assert.equal(source.id, observationId);
  assert.equal(source.project, project);
  assert.equal(source.generation, report.generation);
  assert.equal(source.resourceType, 'Observation');
  const values = codedPivotValuesFor(source, { mode, project, generation: report.generation });
  assert.deepEqual(values.map(({ code, value }) => ({ code, value })), expected.map(({ code, value }) => ({ code, value })), 'Independent raw FHIR Coding/value oracle differs from expected fixture');
  report.oracle = { source: 'Arango Observation payload scoped by exact project, generation and ID; paired by Coding.system and Coding.code', id: source.id, project: source.project, generation: source.generation, resourceType: source.resourceType, values };
};

await mkdir(evidence, { recursive: true });
try {
  const createdExplorer = await api(root, { name: requestedExplorerName, title: `CDA coded pivot ${mode} verification` });
  const creationRequest = report.requests.at(-1);
  assert.equal(creationRequest?.path, root);
  assert.equal(creationRequest?.status, 201);
  assert.equal(creationRequest?.request?.name, requestedExplorerName);
  const createdScope = createdExplorerScope(project, createdExplorer);
  sourceOptionsEndpointPath = `${createdScope.explorerRoot}/authoring/v2/frame-source-options`;
  semanticInventoryEndpointPath = `${createdScope.explorerRoot}/authoring/v2/semantic-inventory`;
  const proposalEndpointPath = `${createdScope.explorerRoot}/authoring/v2/construction-proposals`;
  explorerId = createdScope.explorerId;
  report.explorerId = explorerId;
  report.explorer = explorerId;
  report.target = { ...(cda.report?.target ?? {}), explorer: explorerId };
  if (cda.report?.target) cda.report.target.explorer = explorerId;
  report.explorerProvisioning = {
    requestedName: requestedExplorerName,
    createRequestId: creationRequest.requestId,
    createStatus: creationRequest.status,
    returnedProject: createdExplorer.project,
    returnedExplorerId: explorerId,
    explorerRoot: createdScope.explorerRoot,
  };
  cda.captureRequests(createdScope.explorerRoot, { apiOrigin: uiOrigin });
  const builder = await api(`${createdScope.authoringBase}/builder`);
  report.generation = builder.catalog.generation;
  report.snapshotToken = builder.catalog.snapshotToken;
  assert(report.generation, 'The isolated project must have a loaded dataset generation');
  rawObservation();
  recordCheck(0, 'correctness', report.oracle.project === project && report.oracle.generation === report.generation && report.oracle.id === observationId &&
    report.oracle.values.length === expected.length && report.oracle.values.every((entry, index) => entry.code === expected[index].code && entry.value === expected[index].value),
  { project, generation: report.generation, observationId, values: report.oracle.values, expected });
  const selection = await api(`${createdScope.explorerRoot}/selections`, {
    snapshotToken: report.snapshotToken,
    idempotencyKey: explorerId,
    source: { kind: 'resources', resources: { refs: [{ project, generation: report.generation, resourceType: 'Observation', id: observationId }] } },
  });
  assert(selection.id);
  report.selectionId = selection.id;
  const baseURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
  selectedURL = `${baseURL}&selection=${encodeURIComponent(selection.id)}`;

  requestCapture = captureCDARequests(page, { apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: createdScope.explorerRoot, report, responsePaths: /frame-source-options|semantic-inventory|construction-proposals|commands|builder|selections|preview/ });
  await cda.navigate( baseURL);
  await waitNative(codedPivotFirstTableReady, {}, 5000);
  assert.equal(await cda.inspect( () => [...document.querySelectorAll('button')].some(button => button.innerText.trim() === 'Preview')), false, 'Manual Preview button should not exist');
  await cda.navigate( selectedURL);
  await waitNative(codedPivotFirstTableReady, {}, 5000);
  await fill('Name the table', page.locator('#first-table-name'), tableName);
  await action('Choose Observation rows', page.getByRole('button', { name: 'Choose Observation rows', exact: true }));
  created = true;
  await waitNative( ({ tableName: name }) => document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(name), { tableName }, 5000);
  await waitNative( () => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false, {}, 5000);
  await action('Open row settings', page.locator('[data-testid="construction-rows-settings-trigger"]'));
  await waitNative( () => Boolean(document.querySelector('[role="dialog"][aria-label="Row definition settings"]')), {}, 5000);
  await waitNative( () => [...document.querySelectorAll('[aria-label="Starting collection"] button')].some(button => button.innerText === 'Use selected resources' && !button.disabled), {}, 5000);
  const useSelected = page.getByRole('button', { name: 'Use selected resources', exact: true });
  await action('Use selected resources', useSelected);
  await waitNative( () => document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes('1 Observation resources attached'), {}, 5000);
  await action('Close row settings', page.getByRole('dialog', { name: 'Row definition settings' }).getByRole('button', { name: 'Back to table', exact: true }));
  await waitNative( () => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false, {}, 5000);
  await action('Reopen row settings', page.locator('[data-testid="construction-rows-settings-trigger"]'));
  await waitNative( () => Boolean(document.querySelector('[data-testid="construction-action-pivot-rows"]:not(:disabled)')), {}, 5000);
  const sourceBuilder = await api(`${createdScope.authoringBase}/builder`);
  const sourceDocument = documentFor(sourceBuilder);
  assert(sourceDocument, `The saved source table ${tableName} must exist before choosing a coded Pivot source`);
  report.tableOutputId = sourceDocument.output.id;
  report.sourceTableIdentity = { outputId: sourceDocument.output.id, draftVersion: sourceBuilder.draftVersion, draftDigest: sourceBuilder.draftDigest };
  report.choice = await cda.inspect( () => { const button = document.querySelector('[data-testid="construction-action-pivot-rows"]'); return { disabled: button?.disabled, text: button?.innerText }; });
  assert.equal(report.choice.disabled, false, report.choice.text);
  const sourceOptionsRequestStart = report.nativeRequests.length;
  await action('Choose coded values as columns', page.locator('[data-testid="construction-action-pivot-rows"]'));
  await waitNative( () => Boolean(document.querySelector('section[aria-label="Coded values as columns"]')), {}, 5000);
  await waitNative( () => !document.querySelector('section[aria-label="Coded values as columns"] [role="status"]'), {}, 5000);
  sourceOptionsRequest = await requestCapture.waitFor(entry => entry.path === sourceOptionsEndpointPath && entry.method === 'POST' &&
    Array.isArray(requestCapture.rawResponseBody(entry)?.sources), { fromIndex: sourceOptionsRequestStart, timeoutMs: 5000 });
  sourceOptionsRequestBody = requestCapture.rawRequestBody(sourceOptionsRequest);
  sourceOptionsResponseBody = requestCapture.rawResponseBody(sourceOptionsRequest);
  const sourceOptions = sourceOptionsResponseBody.sources;
  report.sources = await cda.inspect(codedPivotSourceControls);
  const matchingSource = report.sources.find(source => source.text?.includes('component') && source.text.toLowerCase().includes(mode));
  assert(matchingSource, `Direct component ${mode} source is missing`);
  const normalizeLabel = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const matchingOptions = sourceOptions.filter(option => option.route?.length === 0 && option.resourceType === 'Observation' &&
    normalizeLabel(`${option.title} ${option.description}`) === normalizeLabel(matchingSource.text));
  assert.equal(matchingOptions.length, 1, `The native ${mode} label must identify exactly one direct Observation source option`);
  const selectedSourceOption = matchingOptions[0];
  const expectedValuePath = mode === 'integer' ? /valueInteger$/i : /valueString$/i;
  assert(expectedValuePath.test(selectedSourceOption.valuePath), `The ${mode} source must bind its expected scalar value path: ${selectedSourceOption.valuePath}`);
  report.selectedSourceOption = selectedSourceOption;
  const semanticInventoryRequestStart = report.nativeRequests.length;
  const matchingSourceRadio = codedPivotSourceRadioFor(page, matchingSource.text);
  await action(`Select ${mode} coded source`, matchingSourceRadio);
  await waitNative( ({ label }) => [...document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]')].some(input => input.closest('label')?.innerText.toLowerCase().includes(label)), { label: expected[0].label.toLowerCase() }, 5000);
  const semanticInventoryRequest = await requestCapture.waitFor(entry => {
    const request = requestCapture.rawRequestBody(entry);
    const response = requestCapture.rawResponseBody(entry);
    return entry.path.endsWith('/semantic-inventory') && entry.method === 'POST' &&
      request?.sourceChoiceId === selectedSourceOption.choiceId && Array.isArray(response?.entries);
  }, { fromIndex: semanticInventoryRequestStart, timeoutMs: 5000 });
  const semanticInventory = requestCapture.rawResponseBody(semanticInventoryRequest);
  const exactInventoryCategories = semanticInventory.entries.filter(item => expected.some(pair => pair.system === item.system && pair.code === item.code));
  assert.deepEqual(exactInventoryCategories.map(({ system, code }) => ({ system, code })).sort((left, right) => left.code.localeCompare(right.code)),
    expected.map(({ system, code }) => ({ system, code })).sort((left, right) => left.code.localeCompare(right.code)));
  for (const category of exactInventoryCategories) {
    assert(category.constructionChoice?.choiceId, `Selected ${category.system}|${category.code} category must have a current construction choice`);
  }
  const proposalCategories = exactInventoryCategories.map(category => {
    const fixtureCategory = expected.find(pair => pair.system === category.system && pair.code === category.code);
    assert(fixtureCategory, `The selected ${category.system}|${category.code} choice must match the independent coded-value fixture`);
    return { system: category.system, code: category.code, label: fixtureCategory.label, choiceId: category.constructionChoice.choiceId };
  });
  report.categories = await cda.inspect( () => [...document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]')].map(input => ({ text: input.closest('label')?.innerText, disabled: input.disabled })));
  const proposalStarted = Date.now();
  const proposalRequestStart = report.nativeRequests.length;
  for (const pair of expected) {
    const match = report.categories.filter(category => category.text?.toLowerCase().includes(pair.label.toLowerCase()));
    assert.equal(match.length, 1, `Expected one ${pair.label} category`);
    assert.equal(match[0].disabled, false, `${pair.label} category must be enabled`);
    await action(`Select ${pair.label}`, page.locator('section[aria-label="Coded values as columns"] label').filter({ hasText: pair.label }));
  }
  report.selectedChooser = await cda.inspect( () => ({
    sources: [...document.querySelectorAll('input[name="coded-pivot-source"]:checked')].map(input => input.closest('label')?.innerText),
    categories: [...document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]:checked')].map(input => input.closest('label')?.innerText),
  }));
  assert.deepEqual(report.selectedChooser.sources, [matchingSource.text]);
  assert.deepEqual(report.selectedChooser.categories.map(text => expected.find(pair => text.toLowerCase().includes(pair.label.toLowerCase()))?.label).sort(), expected.map(pair => pair.label).sort());
  recordCheck(1, 'usability', true, { mode, selectedSource: matchingSource, selectedSourceOption, selectedChooser: report.selectedChooser,
    exactInventoryCategories, expectedCategories: expected.map(({ system, code, label }) => ({ system, code, label })) });
  const proposal = await requestCapture.waitFor(entry => entry.path === proposalEndpointPath && entry.method === 'POST' &&
    codedPivotProposalRequestMatches(requestCapture.rawRequestBody(entry), {
      outputId: report.tableOutputId, snapshotToken: report.snapshotToken, sourceChoiceId: selectedSourceOption.choiceId,
      missingCellPolicy: 'NULL', categories: proposalCategories,
    }),
  { fromIndex: proposalRequestStart, timeoutMs: Math.max(1, 5000 - (Date.now() - proposalStarted)) });
  await waitNative( () => ['ready', 'error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')), {}, Math.max(1, 5000 - (Date.now() - proposalStarted)));
  assert.equal(proposal.status, 200);
  assert(proposal.response?.proposalId, 'Browser proposal must return an applicable receipt');
  report.proposal = await cda.inspect(() => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    return {
      status: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),
      text: document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,
      applyDisabled: document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled,
      preview: {
        headers: [...(preview?.querySelectorAll('thead th') ?? [])].map(cell => cell.innerText),
        rows: [...(preview?.querySelectorAll('[data-testid="construction-proposal-preview-row"]') ?? [])]
          .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
      },
    };
  });
  assert.equal(report.proposal.status, 'ready', report.proposal.text);
  assert.equal(report.proposal.applyDisabled, false);
  const proposalBody = requestCapture.rawResponseBody(proposal);
  const proposedCodedStep = proposalBody?.candidateConstruction?.steps?.find(step => step.operation?.kind === 'CODED_PIVOT');
  assert(proposedCodedStep, 'The exact coded Pivot proposal must return its normalized construction step');
  const proposedSourceBindings = codedPivotPersistedSourceBindingsFor(proposedCodedStep);
  report.proposedSourceBindings = proposedSourceBindings;
  assert(codedPivotPersistedSourceMatchesOption(proposedCodedStep, selectedSourceOption),
    'The normalized coded Pivot proposal must preserve the selected native frame family and route');
  report.proposalAssociation = codedPivotRenderedValuesFor(report.proposal.preview, proposedCodedStep, expected);
  timing('category-selection-to-proposal-render', proposalStarted);
  recordCheck(2, 'correctness', report.proposal.status === 'ready' && report.proposalAssociation.length === expected.length,
    { mode, proposalId: proposal.response.proposalId, proposal: report.proposal, categoryHeaderValues: report.proposalAssociation });
  const prePivotBuilder = await api(`${createdScope.authoringBase}/builder`);
  const prePivotDocument = documentFor(prePivotBuilder);
  assert(prePivotDocument, `The saved source table ${tableName} must exist before coded Pivot Apply`);
  assert.equal(prePivotDocument.output.id, report.tableOutputId, 'Coded Pivot must preserve the initial source table output binding.');
  const prePivotConstruction = prePivotDocument.construction ?? { version: 1, steps: [] };
  assert.deepEqual(prePivotConstruction, { version: 1, steps: [] }, 'The fresh source table must have the empty base construction before coded Pivot Apply.');
  report.prePivotSource = { document: prePivotDocument, construction: prePivotConstruction, draftVersion: prePivotBuilder.draftVersion, draftDigest: prePivotBuilder.draftDigest };
  const applyStarted = Date.now();
  await action('Apply coded pivot', page.locator('[data-testid="construction-apply-proposal"]'));
  await waitNative( () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 5000);
  const appliedRows = await waitForRenderedTable(proposedCodedStep);
  assert.equal(appliedRows.rows.length, 1, JSON.stringify(appliedRows));
  report.appliedOutputAssociation = codedPivotRenderedValuesFor(appliedRows, proposedCodedStep, expected);
  assert.deepEqual(report.appliedOutputAssociation, report.proposalAssociation);
  timing('apply-to-render', applyStarted);
  const reloadStarted = Date.now();
  await cda.navigate( selectedURL);
  await waitNative( ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
  const previewStarted = Date.now();
  await action('Open coded pivot table', page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
  report.saved = await waitForRenderedTable(proposedCodedStep);
  assert.equal(report.saved.rows.length, 1, JSON.stringify(report.saved));
  report.savedOutputAssociation = codedPivotRenderedValuesFor(report.saved, proposedCodedStep, expected);
  assert.deepEqual(report.savedOutputAssociation, report.proposalAssociation);
  timing('open-table-to-render', previewStarted);
  timing('reload-to-render', reloadStarted);
  const appliedBuilder = await api(`${createdScope.authoringBase}/builder`);
  const appliedDocument = documentFor(appliedBuilder);
  assert(appliedDocument, 'Reloaded Builder must preserve the coded Pivot table');
  const codedStep = appliedDocument.construction.steps.find(step => step.operation.kind === 'CODED_PIVOT');
  assert(codedStep, 'Apply must persist one CODED_PIVOT construction step');
  const durableCategories = codedStep.operation.codedPivot.categories.map(({ system, code }) => ({ system, code })).sort((left, right) => left.code.localeCompare(right.code));
  const expectedCategories = expected.map(({ system, code }) => ({ system, code })).sort((left, right) => left.code.localeCompare(right.code));
  assert.deepEqual(durableCategories, expectedCategories, 'Saved coded Pivot must retain exact Coding.system/code categories');
  report.outputAssociation = codedPivotRenderedValuesFor(report.saved, codedStep, expected);
  report.initialStepBindings = codedPivotBindingsFor(codedStep);
  assert(codedPivotPersistedSourceBindingsEqual(proposedCodedStep, codedStep),
    'Saved coded Pivot must retain the exact canonical source binding returned by its accepted proposal');
  assert(codedPivotPersistedSourceMatchesOption(codedStep, selectedSourceOption),
    'Saved coded Pivot must retain the selected native frame family and route');
  assert.equal(codedStep.operation.codedPivot.missingCellPolicy, 'NULL', 'Initial coded Pivot must retain the default NULL missing-cell policy');
  report.applied = { draftVersion: appliedBuilder.draftVersion, draftDigest: appliedBuilder.draftDigest, stepId: codedStep.id,
    source: report.initialStepBindings.source, selectedSource: matchingSource.text, categories: durableCategories, saved: report.saved };
  recordCheck(4, 'persistence', true, { tableOutputId: report.tableOutputId, draftVersion: appliedBuilder.draftVersion, draftDigest: appliedBuilder.draftDigest,
    stepId: codedStep.id, operation: codedStep.operation, categories: durableCategories, expectedCategories, visibleRows: report.saved.rows, headers: report.saved.headers });
  const persistedProposalCategories = report.initialStepBindings.categories.map(category => ({
    ...category,
    label: expected.find(pair => pair.system === category.system && pair.code === category.code)?.label,
  }));
  await action('Select coded pivot history', page.locator('[data-testid^="construction-history-step-"]'));
  await waitNative( () => Boolean(document.querySelector('[data-testid^="construction-edit-step-"]:not(:disabled)')), {}, 5000);
  await action('Edit coded pivot', page.locator('[data-testid^="construction-edit-step-"]:not(:disabled)'));
  await waitNative( () => Boolean(document.querySelector('section[aria-label="Coded values as columns"] select')), {}, 5000);
  await waitNative( ({ count }) => document.querySelectorAll('section[aria-label="Coded values as columns"] input[type="checkbox"]:checked').length === count, { count: expected.length }, 5000);
  report.reopened = await cda.inspect( () => { const section = document.querySelector('section[aria-label="Coded values as columns"]'); return { selected: [...section.querySelectorAll('input[type="checkbox"]:checked')].map(input => input.closest('label')?.innerText), source: [...section.querySelectorAll('input[name="coded-pivot-source"]:checked')].map(input => input.closest('label')?.innerText), policies: [...section.querySelectorAll('select')].map(input => input.value) }; });
  assert.equal(report.reopened.selected.length, expected.length, JSON.stringify(report.reopened));
  assert.equal(report.reopened.source.length, 1, JSON.stringify(report.reopened));
  const section = page.locator('section[aria-label="Coded values as columns"]');
  await action('Expand coded pivot options', section.locator('details summary'));
  const beforeEdit = await api(`${createdScope.authoringBase}/builder`);
  const beforeEditDocument = documentFor(beforeEdit);
  assert(beforeEditDocument);
  assert.equal(beforeEditDocument.construction.steps.find(step => step.operation.kind === 'CODED_PIVOT')?.operation.codedPivot.missingCellPolicy, 'NULL');
  const editStarted = Date.now();
  const editRequestStart = report.nativeRequests.length;
  const policyAction = { label: 'Set missing value policy', startedAt: Date.now() };
  const policyReplacementProof = {
    contract: 'coded-pivot-policy-replacement',
    actionLabel: policyAction.label,
    mode,
    project,
    generation: report.generation,
    explorerId,
    outputId: report.tableOutputId,
    snapshotToken: report.snapshotToken,
    draftVersion: beforeEdit.draftVersion,
    draftDigest: beforeEdit.draftDigest,
    stepId: codedStep.id,
    sourceChoiceId: selectedSourceOption.choiceId,
    categories: persistedProposalCategories,
    fromPolicy: 'NULL',
    toPolicy: 'ERROR',
  };
  const policyReplacementReason = 'Selecting ERROR superseded the exact in-flight NULL candidate for this coded Pivot edit.';
  let editProposal;
  await cda.withExpectedCancellations({
    origin: uiOrigin,
    method: 'POST',
    paths: [proposalEndpointPath],
    requestIdPrefixes: ['construction-proposal-'],
    reason: policyReplacementReason,
    proof: policyReplacementProof,
    actionLabel: policyAction.label,
  }, async () => {
    await select(policyAction.label, section.locator('select').nth(1), 'ERROR');
    policyAction.completedAt = Date.now();
    editProposal = await requestCapture.waitFor(entry => entry.path === proposalEndpointPath && entry.method === 'POST' &&
      codedPivotProposalRequestMatches(requestCapture.rawRequestBody(entry), {
      outputId: report.tableOutputId, snapshotToken: report.snapshotToken, sourceChoiceId: selectedSourceOption.choiceId,
      missingCellPolicy: 'ERROR', categories: persistedProposalCategories, stepId: codedStep.id,
      }),
    { fromIndex: editRequestStart, timeoutMs: 5000 });
  });
  const policyReplacementEvidence = codedPivotPolicyReplacementCancellationEvidenceFor({
    requests: report.nativeRequests.slice(editRequestStart),
    expectedCancellations: policyReplacementCancellationsFor(policyAction.label),
    replacementRequest: editProposal,
    policyAction,
    expected: {
      origin: uiOrigin,
      path: proposalEndpointPath,
      ...policyReplacementProof,
      reason: policyReplacementReason,
    },
  });
  assert.notEqual(policyReplacementEvidence.status, 'invalid', JSON.stringify(policyReplacementEvidence));
  report.policyReplacementEvidence = policyReplacementEvidence;
  if (policyReplacementEvidence.status === 'matched-cancellation') {
    const canceledRequest = report.nativeRequests.filter(entry => entry.requestId === policyReplacementEvidence.canceledRequestId);
    assert.equal(canceledRequest.length, 1, 'The policy replacement must identify one local native request capture.');
    const localCancellation = {
      contract: 'coded-pivot-policy-replacement',
      requestId: policyReplacementEvidence.canceledRequestId,
      browserRequestId: canceledRequest[0].browserRequestId,
      reason: policyReplacementReason,
      proof: policyReplacementProof,
      fixtureCancellation: policyReplacementEvidence.cancellation,
    };
    canceledRequest[0].expected = true;
    canceledRequest[0].expectedCancellation = localCancellation;
    acceptedExpectedCancellationRequestIds.push(canceledRequest[0].requestId);
    for (const error of report.errors) {
      if (error.requestId !== canceledRequest[0].requestId) continue;
      error.expected = true;
      error.expectedCancellation = localCancellation;
    }
  }
  await waitNative( () => ['ready', 'error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')), {}, 5000);
  report.editedProposal = await cda.inspect(() => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    return {
      status: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),
      text: document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,
      preview: {
        headers: [...(preview?.querySelectorAll('thead th') ?? [])].map(cell => cell.innerText),
        rows: [...(preview?.querySelectorAll('[data-testid="construction-proposal-preview-row"]') ?? [])]
          .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
      },
    };
  });
  assert.equal(report.editedProposal.status, 'ready', report.editedProposal.text);
  const editProposalBody = requestCapture.rawResponseBody(editProposal);
  const editCodedStep = editProposalBody?.candidateConstruction?.steps?.find(step => step.operation?.kind === 'CODED_PIVOT');
  assert(editCodedStep, 'The edited coded Pivot proposal must return its normalized construction step');
  assert.deepEqual(codedPivotBindingsFor(editCodedStep), report.initialStepBindings,
    'Changing missing-cell policy must retain the original source choice, step, category outputs, and output schema');
  report.editedProposalAssociation = codedPivotRenderedValuesFor(report.editedProposal.preview, editCodedStep, expected);
  assert.deepEqual(report.editedProposalAssociation, report.outputAssociation,
    'Editing missing-cell policy must retain each exact coded header/value association');
  timing('edit-proposal-to-render', editStarted);
  await waitNative( () => document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled === false, {}, 5000);
  const commandCountBeforeCancel = report.nativeRequests.filter(request => request.path.endsWith('/commands')).length;
  const cancelStarted = Date.now();
  await action('Cancel coded pivot edit proposal', page.locator('[data-testid="construction-cancel-proposal"]'));
  await waitNative( () => !document.querySelector('[data-testid="construction-proposal-panel"]'), {}, 5000);
  const canceledRows = await waitForRenderedTable(codedStep);
  assert.deepEqual(canceledRows.rows, report.saved.rows, 'Cancel must return to the exact saved coded Pivot output');
  report.canceledOutputAssociation = codedPivotRenderedValuesFor(canceledRows, codedStep, expected);
  assert.deepEqual(report.canceledOutputAssociation, report.outputAssociation);
  timing('cancel-edit-to-closed-proposal', cancelStarted);
  const afterCancel = await api(`${createdScope.authoringBase}/builder`);
  const afterCancelDocument = documentFor(afterCancel);
  assert.equal(afterCancel.draftVersion, beforeEdit.draftVersion, 'Cancel must preserve the saved coded Pivot draft version');
  assert.equal(afterCancel.draftDigest, beforeEdit.draftDigest, 'Cancel must preserve the saved coded Pivot draft digest');
  assert.deepEqual(afterCancelDocument, beforeEditDocument, 'Cancel must preserve the exact saved coded Pivot construction');
  assert.deepEqual(afterCancelDocument.construction.steps.find(step => step.operation.kind === 'CODED_PIVOT')?.operation, beforeEditDocument.construction.steps.find(step => step.operation.kind === 'CODED_PIVOT')?.operation);
  assert.equal(report.nativeRequests.filter(request => request.path.endsWith('/commands')).length, commandCountBeforeCancel,
    'Cancel must not send an authoring command');
  const cancelReloadStarted = Date.now();
  await cda.navigate( selectedURL);
  await waitNative( ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
  await action('Reopen coded pivot table after Cancel', page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
  report.cancelReloadRows = await waitForRenderedTable(codedStep);
  report.cancelReloadAssociation = codedPivotRenderedValuesFor(report.cancelReloadRows, codedStep, expected);
  assert.deepEqual(report.cancelReloadAssociation, report.outputAssociation, 'Cancel reload must retain each exact coded header/value pair');
  await action('Select coded pivot history after Cancel', page.locator('[data-testid^="construction-history-step-"]'));
  await waitNative( () => Boolean(document.querySelector('[data-testid^="construction-edit-step-"]:not(:disabled)')), {}, 5000);
  await action('Reopen coded pivot editor after Cancel', page.locator('[data-testid^="construction-edit-step-"]:not(:disabled)'));
  await waitNative( () => document.querySelectorAll('section[aria-label="Coded values as columns"] select')[1]?.value === 'NULL', {}, 5000);
  timing('cancel-reload-restores-null-policy', cancelReloadStarted);
  const cancelReloadBuilder = await api(`${createdScope.authoringBase}/builder`);
  const cancelReloadDocument = documentFor(cancelReloadBuilder);
  assert.deepEqual(cancelReloadDocument, beforeEditDocument, 'Reload after Cancel must retain the exact saved coded Pivot construction');
  assert.equal(cancelReloadBuilder.draftVersion, beforeEdit.draftVersion);
  assert.equal(cancelReloadBuilder.draftDigest, beforeEdit.draftDigest);
  recordCheck(3, 'persistence', true, { draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest,
    construction: afterCancelDocument.construction, reloadedDraftVersion: cancelReloadBuilder.draftVersion,
    reloadedDraftDigest: cancelReloadBuilder.draftDigest, reloadedConstruction: cancelReloadDocument.construction,
    renderedRows: report.cancelReloadRows, categoryHeaderValues: report.cancelReloadAssociation,
    commandCountBeforeCancel, commandCountAfterCancel: report.nativeRequests.filter(request => request.path.endsWith('/commands')).length });
  await action('Expand coded pivot options after Cancel', page.locator('section[aria-label="Coded values as columns"] details summary'));
  const reopenedSection = page.locator('section[aria-label="Coded values as columns"]');
  const reapplyStarted = Date.now();
  const reapplyRequestStart = report.nativeRequests.length;
  const reapplyPolicyAction = { label: 'Set missing value policy after Cancel', startedAt: Date.now() };
  const reapplyPolicyReplacementProof = {
    ...policyReplacementProof,
    actionLabel: reapplyPolicyAction.label,
  };
  const reapplyPolicyReplacementReason = 'Selecting ERROR after Cancel superseded the exact in-flight NULL candidate for this coded Pivot edit.';
  let reapplyProposal;
  await cda.withExpectedCancellations({
    origin: uiOrigin,
    method: 'POST',
    paths: [proposalEndpointPath],
    requestIdPrefixes: ['construction-proposal-'],
    reason: reapplyPolicyReplacementReason,
    proof: reapplyPolicyReplacementProof,
    actionLabel: reapplyPolicyAction.label,
  }, async () => {
    await select(reapplyPolicyAction.label, reopenedSection.locator('select').nth(1), 'ERROR');
    reapplyPolicyAction.completedAt = Date.now();
    reapplyProposal = await requestCapture.waitFor(entry => entry.path === proposalEndpointPath && entry.method === 'POST' &&
      codedPivotProposalRequestMatches(requestCapture.rawRequestBody(entry), {
        outputId: report.tableOutputId, snapshotToken: report.snapshotToken, sourceChoiceId: selectedSourceOption.choiceId,
        missingCellPolicy: 'ERROR', categories: persistedProposalCategories, stepId: codedStep.id,
      }),
    { fromIndex: reapplyRequestStart, timeoutMs: 5000 });
  });
  const reapplyPolicyReplacementEvidence = codedPivotPolicyReplacementCancellationEvidenceFor({
    requests: report.nativeRequests.slice(reapplyRequestStart),
    expectedCancellations: policyReplacementCancellationsFor(reapplyPolicyAction.label),
    replacementRequest: reapplyProposal,
    policyAction: reapplyPolicyAction,
    expected: {
      origin: uiOrigin,
      path: proposalEndpointPath,
      ...reapplyPolicyReplacementProof,
      reason: reapplyPolicyReplacementReason,
    },
  });
  assert.notEqual(reapplyPolicyReplacementEvidence.status, 'invalid', JSON.stringify(reapplyPolicyReplacementEvidence));
  report.reapplyPolicyReplacementEvidence = reapplyPolicyReplacementEvidence;
  if (reapplyPolicyReplacementEvidence.status === 'matched-cancellation') {
    const canceledRequest = report.nativeRequests.filter(entry => entry.requestId === reapplyPolicyReplacementEvidence.canceledRequestId);
    assert.equal(canceledRequest.length, 1, 'The post-Cancel policy replacement must identify one local native request capture.');
    const localCancellation = {
      contract: 'coded-pivot-policy-replacement',
      requestId: reapplyPolicyReplacementEvidence.canceledRequestId,
      browserRequestId: canceledRequest[0].browserRequestId,
      reason: reapplyPolicyReplacementReason,
      proof: reapplyPolicyReplacementProof,
      fixtureCancellation: reapplyPolicyReplacementEvidence.cancellation,
    };
    canceledRequest[0].expected = true;
    canceledRequest[0].expectedCancellation = localCancellation;
    acceptedExpectedCancellationRequestIds.push(canceledRequest[0].requestId);
    for (const error of report.errors) {
      if (error.requestId !== canceledRequest[0].requestId) continue;
      error.expected = true;
      error.expectedCancellation = localCancellation;
    }
  }
  const policyReplacementActions = new Set([policyAction.label, reapplyPolicyAction.label]);
  assert((cda.report.expectedCancellations ?? []).every(entry => entry?.proof?.contract !== 'coded-pivot-policy-replacement' ||
    policyReplacementActions.has(entry.proof.actionLabel)), 'Every coded Pivot policy cancellation must match one of the two validated policy actions.');
  await waitNative( () => ['ready', 'error'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')), {}, 5000);
  report.confirmedEditedProposal = await cda.inspect( () => ({ status: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'), text: document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText }));
  assert.equal(report.confirmedEditedProposal.status, 'ready', report.confirmedEditedProposal.text);
  const reapplyProposalBody = requestCapture.rawResponseBody(reapplyProposal);
  const reapplyCodedStep = reapplyProposalBody?.candidateConstruction?.steps?.find(step => step.operation?.kind === 'CODED_PIVOT');
  assert(reapplyCodedStep, 'The reapplied coded Pivot proposal must return its normalized construction step');
  assert.deepEqual(codedPivotBindingsFor(reapplyCodedStep), report.initialStepBindings,
    'Reapplying ERROR must retain the original source choice, step, category outputs, and output schema');
  timing('reopened-edit-proposal-to-render', reapplyStarted);
  const editApplyStarted = Date.now();
  await action('Apply edited coded pivot', page.locator('[data-testid="construction-apply-proposal"]'));
  await waitNative( () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1 && !document.querySelector('section[aria-label="Coded values as columns"]'), {}, 5000);
  const editedAppliedRows = await waitForRenderedTable(reapplyCodedStep);
  assert.deepEqual(editedAppliedRows.rows, report.saved.rows);
  report.editedAppliedAssociation = codedPivotRenderedValuesFor(editedAppliedRows, reapplyCodedStep, expected);
  assert.deepEqual(report.editedAppliedAssociation, report.outputAssociation);
  timing('edit-apply-to-render', editApplyStarted);
  const editReloadStarted = Date.now();
  await cda.navigate( selectedURL);
  await waitNative( ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
  await action('Reopen coded pivot table', page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
  await action('Select coded pivot history after reload', page.locator('[data-testid^="construction-history-step-"]'));
  await waitNative( () => Boolean(document.querySelector('[data-testid^="construction-edit-step-"]:not(:disabled)')), {}, 5000);
  const editorDisposalRequestStart = report.nativeRequests.length;
  await action('Reopen coded pivot editor', page.locator('[data-testid^="construction-edit-step-"]:not(:disabled)'));
  await waitNative( () => document.querySelectorAll('section[aria-label="Coded values as columns"] select')[1]?.value === 'ERROR', {}, 5000);
  timing('edit-reload-to-error-policy', editReloadStarted);
  report.editedReload = await cda.inspect( () => ({ policies: [...document.querySelectorAll('section[aria-label="Coded values as columns"] select')].map(input => input.value) }));
  const editedPreviewStarted = Date.now();
  const editorDisposalAction = { label: 'Back to table', startedAt: Date.now() };
  const editorDisposalReason = 'The Back to table action retired this exact Coded Pivot semantic-inventory response-body read.';
  const editorDisposalProof = {
    contract: 'coded-pivot-editor-disposal',
    actionLabel: editorDisposalAction.label,
    mode,
    project,
    generation: report.generation,
    explorerId,
    selectionId: report.selectionId,
    outputId: report.tableOutputId,
    snapshotToken: report.snapshotToken,
    sourceChoiceId: selectedSourceOption.choiceId,
    rowRoot: 'Observation',
    limit: 50,
  };
  const editorDisposalExpected = {
    origin: uiOrigin,
    path: semanticInventoryEndpointPath,
    ...editorDisposalProof,
    reason: editorDisposalReason,
  };
  editorDisposalWindow = { fromIndex: editorDisposalRequestStart, action: editorDisposalAction, expected: editorDisposalExpected };
  await cda.withExpectedCancellations({
    origin: uiOrigin,
    method: 'POST',
    paths: [semanticInventoryEndpointPath],
    requestIdPrefixes: ['cda-request-'],
    reason: editorDisposalReason,
    proof: editorDisposalProof,
    actionLabel: editorDisposalAction.label,
  }, async () => {
    await action(editorDisposalAction.label, codedPivotBackToTableControl(page));
    await waitNative(() => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 5000);
    editorDisposalAction.completedAt = Date.now();
  });
  const editorDisposalRequests = report.nativeRequests.slice(editorDisposalWindow.fromIndex);
  const editorDisposalCandidate = editorDisposalRequests.find(entry => entry.path === semanticInventoryEndpointPath && entry.failure === 'net::ERR_ABORTED');
  const editorDisposalObservation = codedPivotEditorDisposalCancellationEvidenceFor({
    requests: editorDisposalRequests,
    expectedCancellations: cda.report.expectedCancellations ?? [],
    fixtureNetworkFailures: cda.diagnostics.networkFailures,
    request: editorDisposalCandidate,
    action: editorDisposalAction,
    expected: editorDisposalExpected,
  });
  report.editorDisposalObservation = editorDisposalObservation;
  assert.notEqual(editorDisposalObservation.status, 'invalid', JSON.stringify(editorDisposalObservation));
  if (editorDisposalObservation.status === 'verified-retirement-candidate') {
    const localCancellation = {
      contract: 'coded-pivot-editor-disposal',
      requestId: editorDisposalObservation.requestId,
      browserRequestId: editorDisposalObservation.browserRequestId,
      method: 'POST',
      url: `${uiOrigin}${semanticInventoryEndpointPath}`,
      reason: editorDisposalReason,
      proof: editorDisposalObservation.cancellation.proof,
      fixtureCancellation: editorDisposalObservation.cancellation,
    };
    editorDisposalCandidate.expected = true;
    editorDisposalCandidate.canceled = true;
    editorDisposalCandidate.cancellationReason = editorDisposalReason;
    editorDisposalCandidate.expectedCancellation = localCancellation;
    for (const error of report.errors) {
      if (error.browserRequestId !== editorDisposalCandidate.browserRequestId) continue;
      error.expected = true;
      error.expectedCancellation = localCancellation;
    }
  }
  report.editedReload.historyCount = 1;
  report.editedSaved = await waitForRenderedTable(reapplyCodedStep);
  assert.deepEqual(report.editedSaved, report.saved, 'Changing missing-value handling changed populated CDA values');
  timing('back-to-edited-render', editedPreviewStarted);
  const editedBuilder = await api(`${createdScope.authoringBase}/builder`);
  const editedDocument = documentFor(editedBuilder);
  const editedCodedStep = editedDocument.construction.steps.find(step => step.operation.kind === 'CODED_PIVOT');
  assert.equal(editedCodedStep?.operation.codedPivot.missingCellPolicy, 'ERROR', 'The edited missing-cell policy must persist as ERROR');
  assert.deepEqual(editedCodedStep.operation.codedPivot.categories.map(({ system, code }) => ({ system, code })).sort((left, right) => left.code.localeCompare(right.code)), expectedCategories);
  assert.deepEqual(codedPivotBindingsFor(editedCodedStep), report.initialStepBindings,
    'Edit Apply/reload must retain the original source choice, step, category outputs, and output schema');
  report.editedOutputAssociation = codedPivotRenderedValuesFor(report.editedSaved, editedCodedStep, expected);
  assert.deepEqual(report.editedOutputAssociation, report.outputAssociation, 'Edited/reloaded output must retain each Coding-to-header-to-value association');
  recordCheck(5, 'persistence', true, { draftVersion: editedBuilder.draftVersion, draftDigest: editedBuilder.draftDigest, missingCellPolicy: editedCodedStep.operation.codedPivot.missingCellPolicy,
    valuesUnchanged: JSON.stringify(report.editedSaved) === JSON.stringify(report.saved), saved: report.editedSaved, categories: editedCodedStep.operation.codedPivot.categories });
  const remove = page.locator('[data-testid^="construction-remove-step-"]');
  if (!(await remove.count())) await action('Select coded group before remove', page.locator('[data-testid^="construction-history-step-"]'));
  await waitNative( () => Boolean(document.querySelector('[data-testid^="construction-remove-step-"]')), {}, 5000);
  const removalStart = report.nativeRequests.length;
  const removalProposalStarted = Date.now();
  await action('Remove coded pivot group', page.locator('[data-testid^="construction-remove-step-"]'));
  const removalProposal = await requestCapture.waitFor(entry => entry.path === proposalEndpointPath && entry.method === 'POST' &&
    codedPivotRemovalRequestMatches(requestCapture.rawRequestBody(entry), {
      outputId: report.tableOutputId,
      snapshotToken: report.snapshotToken,
      removedStepId: editedCodedStep.id,
      candidateConstruction: prePivotConstruction,
    }), { fromIndex: removalStart, timeoutMs: 5000 });
  assert.equal(removalProposal.status, 200, 'Removing coded Pivot must return a successful exact-source restoration proposal.');
  await waitNative(codedPivotRemovalProposalReady, { id: observationId }, 5000);
  timing('remove-action-to-restoration-proposal', removalProposalStarted);
  const removalApplyStarted = Date.now();
  await action('Apply coded pivot removal', page.locator('[data-testid="construction-apply-proposal"]'));
  await waitNative( () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0, {}, 5000);
  const removedRows = await cda.inspect(() => ({
    headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText),
    rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText)),
  }));
  assert.deepEqual(removedRows.headers, ['OBSERVATION ID']);
  assert.deepEqual(removedRows.rows, [[observationId]]);
  timing('remove-apply-to-source-render', removalApplyStarted);
  const restorationReloadStarted = Date.now();
  await cda.navigate( selectedURL);
  await waitNative( ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
  await action('Open restored table', page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
  report.restored = await cda.inspect( () => ({ historyCount: document.querySelectorAll('[data-testid^="construction-history-step-"]').length, body: document.body.innerText.slice(0, 900) }));
  assert.equal(report.restored.historyCount, 0);
  await waitNative(codedPivotRestoredSourceRowVisible, { id: observationId }, 5000);
  report.restored.headers = await cda.inspect( () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText));
  assert.deepEqual(report.restored.headers, ['OBSERVATION ID']);
  timing('restoration-reload-to-source-render', restorationReloadStarted);
  const restoredBuilder = await api(`${createdScope.authoringBase}/builder`);
  const restoredDocument = documentFor(restoredBuilder);
  assert.deepEqual(restoredDocument, prePivotDocument, 'Removing the coded Pivot must restore the exact pre-Pivot source construction');
  assert.deepEqual(report.restored.headers, ['OBSERVATION ID']);
  recordCheck(6, 'persistence', true, { draftVersion: restoredBuilder.draftVersion, draftDigest: restoredBuilder.draftDigest,
    restoredDocument, prePivotDocument, headers: report.restored.headers, observationId });
  const actions = tracker.actions.map(({ label, elapsedMs }) => ({ name: label, durationMs: elapsedMs,
    budgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS, withinBudget: elapsedMs <= DEFAULT_ACTION_TO_RENDER_BUDGET_MS }));
  const allWithinBudget = actions.length > 0 && report.timingCheckpoints.length > 0 &&
    actions.every(action => action.withinBudget) && report.timingCheckpoints.every(checkpoint => checkpoint.durationMs <= checkpoint.budgetMs);
  recordCheck(7, 'performance', allWithinBudget, { actions, timingCheckpoints: report.timingCheckpoints,
    checkpointBudgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS });
  report.outcome = 'passed';
} catch (error) {
  report.outcome = 'failed';
  report.failure = String(error.stack ?? error);
  report.failures.push(report.failure);
  const firstFailureEvidence = await codedPivotFirstFailureEvidenceFor({
    mode,
    action: tracker.activeAction,
    captureDom: page ? () => cda.inspect(codedPivotFailureDomSnapshot, { mode }) : undefined,
    captureSourceOptions: requestCapture && sourceOptionsRequest && sourceOptionsRequestBody && sourceOptionsResponseBody && sourceOptionsEndpointPath
      ? domSnapshot => codedPivotSourceOptionsDiagnosticFor(sourceOptionsRequest, sourceOptionsRequestBody,
        sourceOptionsResponseBody, {
          origin: uiOrigin, project, generation: report.generation, explorerId, outputId: report.tableOutputId,
          snapshotToken: report.snapshotToken, mode, domSnapshot,
        })
      : undefined,
  });
  report.firstFailureEvidence = firstFailureEvidence;
  if (page) await captureFailure(error, {
    phase: 'coded-pivot-lifecycle', action: tracker.activeAction,
    elapsedMs: tracker.activeAction ? Date.now() - tracker.activeAction.startedAt : undefined,
    firstFailureEvidence,
    state: { tableName, mode, timingsMs: report.timingsMs, requests: report.nativeRequests },
  });
  report.__nativeFailure = true;
} finally {
  if (created) {
    try {
      await cda.navigate( selectedURL);
      await waitNative( ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
      await action('Open table for cleanup', page.locator('[data-testid^="construction-table-"]').filter({ hasText: tableName }));
      await waitNative( ({ tableName: name }) => document.querySelector('[data-testid="construction-workspace"] header')?.innerText.includes(name), { tableName }, 5000);
      await waitNative( () => document.querySelector('[data-testid="construction-delete-table"]')?.disabled === false, {}, 5000);
      page.once('dialog', dialog => dialog.accept());
      await action('Delete verifier table', page.locator('[data-testid="construction-delete-table"]'));
      await waitNative( ({ name }) => ![...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
      report.cleanup = 'deleted';
    } catch (error) {
      report.cleanup = 'failed';
      report.cleanupFailure = String(error);
      report.__nativeFailure = true;
    }
  }
  if (requestCapture) {
    try {
      await requestCapture.flush();
    } catch (error) {
      report.nativeRequestFlushFailure = String(error.stack ?? error);
      report.failures.push(report.nativeRequestFlushFailure);
      report.__nativeFailure = true;
    }
  }
  if (editorDisposalWindow) {
    try {
      const editorDisposalRequests = report.nativeRequests.slice(editorDisposalWindow.fromIndex);
      const editorDisposalCandidate = editorDisposalRequests.find(entry =>
        entry.path === editorDisposalWindow.expected.path && entry.failure === 'net::ERR_ABORTED');
      const finalizedEditorDisposal = codedPivotEditorDisposalCancellationEvidenceFor({
        requests: editorDisposalRequests,
        expectedCancellations: cda.report.expectedCancellations ?? [],
        fixtureNetworkFailures: cda.diagnostics.networkFailures,
        request: editorDisposalCandidate,
        action: editorDisposalWindow.action,
        expected: editorDisposalWindow.expected,
        finalized: true,
      });
      report.editorDisposalEvidence = finalizedEditorDisposal;
      if (finalizedEditorDisposal.status === 'matched-cancellation') {
        acceptedEditorDisposalRequestIds.push(finalizedEditorDisposal.requestId);
        acceptedEditorDisposalBrowserRequestIds.push(finalizedEditorDisposal.browserRequestId);
      } else if (finalizedEditorDisposal.status !== 'no-cancellation') {
        throw new Error(`Coded Pivot editor-disposal proof did not survive finalization: ${JSON.stringify(finalizedEditorDisposal)}`);
      }
    } catch (error) {
      report.editorDisposalFailure = String(error.stack ?? error);
      report.failures.push(report.editorDisposalFailure);
      report.__nativeFailure = true;
    }
  }
  report.nativeRequestEvidence = summarizeCodedPivotNativeRequests(report.nativeRequests, {
    acceptedExpectedCancellationRequestIds,
    acceptedEditorDisposalRequestIds,
  });
  const unexpectedWorkflowErrors = report.errors.filter(error => !error.expectedCancellation);
  const unexpectedFixtureNetworkFailures = cda.diagnostics.networkFailures.filter(error => {
    const cancellation = error.expectedCancellation;
    const acceptedPolicyReplacement = error.expected === true && cancellation?.contract === 'coded-pivot-policy-replacement' &&
      acceptedExpectedCancellationRequestIds.includes(error.requestId);
    const acceptedEditorDisposal = error.expected === true && cancellation?.contract === 'coded-pivot-editor-disposal' &&
      acceptedEditorDisposalBrowserRequestIds.includes(error.browserRequestId);
    return !acceptedPolicyReplacement && !acceptedEditorDisposal;
  });
  const cleanNativeDiagnostics = unexpectedWorkflowErrors.length === 0 && report.nativeRequestEvidence.passed &&
    cda.diagnostics.console.length === 0 && cda.diagnostics.pageErrors.length === 0 &&
    unexpectedFixtureNetworkFailures.length === 0 && cda.diagnostics.httpFailures.length === 0;
  try {
    recordCheck(8, 'correctness', cleanNativeDiagnostics, {
      nativeRequestEvidence: report.nativeRequestEvidence,
      errors: report.errors,
      unexpectedWorkflowErrors,
      unexpectedFixtureNetworkFailures,
      diagnostics: cda.diagnostics,
    });
  } catch (error) {
    report.outcome = 'failed';
    report.failure ??= String(error.stack ?? error);
    report.failures.push(report.failure);
    report.__nativeFailure = true;
  }
if (page) {
    report.diagnostics = cda.diagnostics;
  }
  try { report.verificationIdentity = await verificationIdentity.finish(); }
  catch (error) { report.outcome = 'invalidated'; report.identityFailure = String(error); report.__nativeFailure = true; }
  if (report.__nativeFailure && report.outcome === 'passed') report.outcome = 'failed';
  await writeFile(artifact, JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.failure ?? report.cleanupFailure ?? report.identityFailure ?? report.nativeRequestFlushFailure ?? 'verify-cda-coded-pivot.mjs workflow failed');
  await cda.attachReport('verify-cda-coded-pivot.mjs', report);
  return report;
}
