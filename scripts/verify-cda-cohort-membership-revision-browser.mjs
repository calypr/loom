import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp, localCDAApiContainer } from './lib/api-build-freeze.mjs';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const resourceType = 'Specimen';
const groupLabel = 'Cohort';
const memberFieldLabel = 'Specimen ID';
const changeSourceCollection = process.env.LOOM_COHORT_SOURCE_COLLECTION_CHANGE === '1';
const explorer = `cohort-membership-revision-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/${explorer}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const selections = base.replace('/authoring/v2', '/selections');
const report = {
  project, generation, resourceType, explorer, groupLabel, memberFieldLabel, changeSourceCollection,
  cases: [], errors: [], requests: [], nativeRequests: [], revisions: [], started: new Date().toISOString(),
};
await mkdir(evidence, { recursive: true });
const sourceFreeze = await captureSourceFreeze(fileURLToPath(new URL('..', import.meta.url)));
const apiBuildTarget = 'local-cda-api';
const readApiBuildStamp = () => checkContainerApiBuildStamp(localCDAApiContainer());
let frozenApiBuild;

let browser;
let builder;
let outputId;
let requestCapture;
const page = () => browser.page;
const waitUI = (condition, timeout = 30000) => page().waitForFunction(condition, undefined, { timeout });
const navigateUI = url => page().goto(url, { waitUntil: 'domcontentloaded', timeout: 30000 });
const actionTarget = (selector, identity = {}) => {
  let locator = page().locator(selector);
  if (identity.name !== undefined) locator = locator.and(page().getByRole('button', { name: identity.name, exact: true }));
  if (identity.includes !== undefined) {
    const escaped = identity.includes.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    locator = locator.and(page().getByRole('button', { name: new RegExp(escaped, 'i') }));
  }
  return locator;
};
const clickUI = (selector, identity = {}) => {
  const locator = actionTarget(selector, identity);
  const label = identity.name ?? identity.includes ?? selector;
  return performAction(report, label, locator, target => target.click({ timeout: 5000 }));
};
const fillUI = (selector, value, label = selector) => {
  const locator = page().locator(selector);
  return performAction(report, label, locator, (target, { timeout }) => target.fill(value, { timeout }), { editable: true });
};
const selectUI = (selector, value) => {
  const locator = page().locator(selector);
  return performAction(report, `Select ${value}`, locator, (target, { timeout }) => target.selectOption(value, { timeout }));
};
const pathOf = entry => entry.path.split('?')[0];
const doc = state => state.workspace.documents.find(document => document.output.id === outputId);
const sorted = values => [...values].sort();
const recordRender = (name, startedAt) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const api = async (path, body) => {
  const startedAt = Date.now();
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `cohort-membership-revision-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, method: body ? 'POST' : 'GET', body, startedAt, completedAt: Date.now(), status: response.status, response: path.endsWith('/builder') ? {
    draftVersion: value.draftVersion,
    draftDigest: value.draftDigest,
    catalog: { generation: value.catalog?.generation, authorizationScopeDigest: value.catalog?.authorizationScopeDigest },
    workspace: value.workspace,
  } : value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(base + '/builder');
};
const waitNative = async (suffix, startedAt, predicate = () => true) => {
  try {
    return await requestCapture.waitFor(entry => pathOf(entry).endsWith(suffix) &&
      entry.startedAt >= startedAt && predicate(entry), { timeout: Math.max(1, startedAt + 5000 - Date.now()) });
  } catch {
    assert.fail(`Native ${suffix} request did not complete with a readable response within five seconds`);
  }
};
const waitRowProposal = async (startedAt, expectedRevisionID, expectedPolicy) => {
  const request = await waitNative('/row-definition-proposals', startedAt, entry => {
    const choice = entry.body?.selection?.explicitGroup;
    return choice?.revisionId === expectedRevisionID && choice?.unassignedMemberPolicy === expectedPolicy;
  });
  assert.equal(request.status, 200, JSON.stringify(request));
  assert(request.response?.proposalId, JSON.stringify(request.response));
  await waitUI(`document.querySelector('[aria-label="Row definition settings"] button')?.innerText === 'Back to table' || [...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled)`);
  await waitUI(`[...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled)`);
  return request;
};
const waitTable = async expectedRows => {
  await waitUI(`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')===${JSON.stringify(String(expectedRows + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:')`);
  const table = page().getByTestId('preview-table-scroll').getByRole('table');
  return table.evaluate(element => ({
    headers: [...element.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
    rows: [...element.querySelectorAll('[role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
  }));
};
const waitSavedPreview = async startedAt => {
  const deadline = startedAt + 5000;
  let response;
  try {
    response = await requestCapture.waitFor(entry => entry.startedAt >= startedAt && entry.status === 200 &&
      (pathOf(entry).endsWith('/preview') || pathOf(entry).endsWith('/commands')),
    { timeout: Math.max(1, deadline - Date.now()) });
  } catch {
    assert.fail('Saved preview or accepted proposal did not render within five seconds');
  }
  await waitUI(`!document.body.innerText.includes('Loading your table…')`, Math.max(1, deadline - Date.now()));
  report.previewDelivery ??= [];
  report.previewDelivery.push({ path: pathOf(response), adoptedProposal: pathOf(response).endsWith('/commands') });
  return response;
};
const assertMemberField = (row, headers, expectedIDs, label = memberFieldLabel) => {
  const columnIndex = headers.findIndex(header => header.toLowerCase().startsWith(label.toLowerCase()));
  assert(columnIndex >= 0, `Expected a ${label} output column: ${JSON.stringify(headers)}`);
  const values = String(row[columnIndex] ?? '').split(';').map(value => value.trim()).filter(Boolean);
  assert.deepEqual(sorted(values), sorted(expectedIDs), `Member field must contain exactly the expected FHIR IDs: ${JSON.stringify(row)}`);
};
const assertGroupRow = (table, expectedIDs, requireMemberField = true) => {
  assert.equal(table.rows.length, 1, `Expected one selected cohort row, got ${JSON.stringify(table.rows)}`);
  const labelIndex = table.headers.findIndex(header => header.toLowerCase() === 'group label');
  const membersIndex = table.headers.findIndex(header => header.toLowerCase() === 'members');
  assert(labelIndex >= 0 && membersIndex >= 0, `Cohort columns are missing: ${JSON.stringify(table.headers)}`);
  const row = table.rows[0];
  assert.equal(row[labelIndex], groupLabel);
  for (const id of expectedIDs) assert(row[membersIndex].includes(id), `Members cell omitted ${id}: ${row[membersIndex]}`);
  if (expectedIDs.length === 1) {
    const excluded = report.oracle.sourceIDs.find(id => id !== expectedIDs[0]);
    assert(!row[membersIndex].includes(excluded), `Excluded FHIR ID ${excluded} leaked into the cohort row`);
  }
  if (requireMemberField) assertMemberField(row, table.headers, expectedIDs);
};
const openRows = async () => {
  await clickUI('[data-testid="construction-rows-settings-trigger"]');
  const selector = 'select[aria-label="What should each row represent?"]';
  await waitUI(`document.querySelector(${JSON.stringify(selector)})?.disabled===false`);
  return selector;
};
const replaceInput = async (selector, text) => {
  await fillUI(selector, text, `Fill ${selector}`);
};
const waitChoiceProposal = async startedAt => {
  const proposal = await waitNative('/construction-choice-proposals', startedAt);
  assert.equal(proposal.status, 200, JSON.stringify(proposal.response));
  await waitUI(`['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`);
  const result = await page().getByTestId('construction-choice-proposal-panel').evaluate(panel => ({
    status: panel.dataset.proposalStatus,
    text: panel.innerText,
    rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
  }));
  assert.equal(result.status, 'ready', result.text);
  return { proposal, result };
};
const createCohortNatively = async (memberIDs, memberKeyByID, revisionName) => {
  await openRows();
  const createButton = '[aria-label="Named cohorts"] button';
  await waitUI(`document.querySelector(${JSON.stringify(createButton)})?.disabled===false`);
  await clickUI(createButton, { name: 'Create groups from this selection' });
  const editor = '[aria-label="Create explicit groups"]';
  await waitUI(`document.querySelector(${JSON.stringify(editor + ' input[aria-label="Group 1 name"]')})?.disabled===false`);
  await replaceInput(editor + ' input[aria-label="Group 1 name"]', groupLabel);
  await clickUI(`${editor} button[aria-label="Remove Group B"]`);
  const assignments = [];
  for (const member of report.oracle.members) {
    const label = `Assign ${member.uiLabel} to ${groupLabel}`;
    const selector = `${editor} input[aria-label=${JSON.stringify(label)}]`;
    const exists = await page().locator(selector).count() === 1;
    assert(exists, `Native member assignment control is missing: ${label}`);
    if (memberIDs.includes(member.ref.id)) {
      await clickUI(selector);
      assignments.push({ id: member.ref.id, memberKey: member.memberKey });
    }
  }
  assert.deepEqual(sorted(assignments.map(member => member.id)), sorted(memberIDs));
  const exactMembershipText = await page().locator('[aria-label="Exact group memberships"]').innerText();
  assert(exactMembershipText?.includes(groupLabel), exactMembershipText);
  for (const id of memberIDs) assert(exactMembershipText.includes(id), `Native exact-membership summary omitted ${id}: ${exactMembershipText}`);
  const beforeCreate = Date.now();
  await clickUI(`${editor} button`, { name: 'Create group revision' });
  await waitUI(`!document.querySelector(${JSON.stringify(editor)})`);
  const createRequest = await waitNative('/explicit-groups', beforeCreate);
  assert.equal(createRequest.status, 201, JSON.stringify(createRequest.response));
  const revision = createRequest.response;
  assert.equal(revision.sourceSelectionRevisionId, report.oracle.selection.id);
  assert.equal(revision.groupCount, 1);
  assert.equal(revision.memberCount, memberIDs.length);
  assert.equal(revision.groups.length, 1);
  assert.equal(revision.groups[0].label, groupLabel);
  assert.equal(revision.groups[0].memberCount, memberIDs.length);
  const submitted = createRequest.body.groups;
  assert.equal(submitted.length, 1);
  assert.equal(submitted[0].label, groupLabel);
  assert.deepEqual(sorted(submitted[0].memberIds), sorted(memberIDs.map(id => memberKeyByID.get(id))));
  assert.deepEqual(sorted(assignments.map(member => member.memberKey)), sorted(submitted[0].memberIds), 'The native checkbox assignments must be the exact posted membership set');
  await waitUI(`document.querySelector('select[aria-label="Unmatched record policy"]')?.disabled===false`);
  recordRender(`${revisionName}-create-to-native-policy`, beforeCreate);
  report.revisions.push({ name: revisionName, revision, submittedMembers: submitted[0].memberIds, submittedIDs: memberIDs });
  return { revision };
};
const selectPolicyAndWait = async (revisionID, policy, name) => {
  const selector = 'select[aria-label="Unmatched record policy"]';
  await waitUI(`document.querySelector(${JSON.stringify(selector)})?.disabled===false`);
  const options = await page().locator(selector).locator('option').evaluateAll(items => items.map(option => ({ value: option.value, disabled: option.disabled, text: option.text })));
  const value = `explicit:${revisionID}:${policy}`;
  assert(options.some(option => option.value === value && !option.disabled), `Unassigned policy ${value} is unavailable: ${JSON.stringify(options)}`);
  const startedAt = Date.now();
  await selectUI(selector, value);
  await waitRowProposal(startedAt, revisionID, policy);
  recordRender(name, startedAt);
};
const applyRowDefinition = async (expectedRows, expectedIDs, name, requireMemberField = true) => {
  const startedAt = Date.now();
  await clickUI('[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
  await waitUI(`!document.querySelector('[aria-label="Row definition settings"]')`);
  await waitSavedPreview(startedAt);
  const table = await waitTable(expectedRows);
  if (expectedIDs.length > 0) assertGroupRow(table, expectedIDs, requireMemberField);
  recordRender(name, startedAt);
  builder = await api(base + '/builder');
  return table;
};
const waitConstructionProposal = async (startedAt, predicate = () => true) => {
  const request = await waitNative('/construction-proposals', startedAt, predicate);
  assert.equal(request.status, 200, JSON.stringify(request.response));
  await waitUI(`['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
  const result = await page().getByTestId('construction-proposal-panel').evaluate(panel => ({
    status: panel.dataset.proposalStatus,
    text: panel.innerText,
    rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
  }));
  assert.equal(result.status, 'ready', result.text);
  return { request, result };
};
const waitLineage = async startedAt => {
  const request = await waitNative('/row-lineage', startedAt);
  assert.equal(request.status, 200, JSON.stringify(request.response));
  return request;
};
const inspectFirstRow = async (name, expectedIDs) => {
  const startedAt = Date.now();
  await clickUI('button[aria-label="Inspect row 1 identity"]');
  const dialog = '[role="dialog"][aria-label="Row 1 identity"]';
  await waitUI(`document.querySelector(${JSON.stringify(dialog)})`);
  await waitUI(`document.querySelectorAll(${JSON.stringify(dialog + ' ul li')}).length===${JSON.stringify(expectedIDs.length)} || /unavailable|cannot be listed|could not load|could not be fully listed/i.test(document.querySelector(${JSON.stringify(dialog)})?.innerText ?? '')`, 5000);
  const inspector = page().locator(dialog);
  const inspectorText = await inspector.innerText();
  assert(!/unavailable|cannot be listed|could not load|could not be fully listed/i.test(inspectorText), `${name}: ${inspectorText}`);
  const native = await waitLineage(startedAt);
  const body = native.response;
  assert.equal(body.status, 'COMPLETE', JSON.stringify(body));
  assert.equal(body.outputId, outputId);
  assert.equal(body.rowId, native.body.rowId);
  const expectedRefs = sorted(expectedIDs.map(id => `${resourceType}/${id}`));
  const contributors = sorted(body.contributors.map(item => `${item.resourceType}/${item.resourceId}`));
  assert.deepEqual(contributors, expectedRefs, `${name} lineage must match the independently scoped raw FHIR IDs`);
  assert.equal(new Set(contributors).size, expectedIDs.length);
  assert.equal(body.hasMore ?? false, false);
  assert(body.contributors.every(item => item.resourceType === resourceType && item.resourceId),
    'The lineage contributor contract is resourceType/resourceId; project and generation are proven by the independent scoped raw-source and selection oracle');
  const listed = await inspector.locator('ul li').allInnerTexts().then(items => items.map(item => item.trim()).sort());
  assert.deepEqual(listed, expectedRefs);
  const identity = await inspector.locator('p.font-mono').textContent();
  assert(identity);
  report.lineage ??= [];
  report.lineage.push({ name, identity, rowId: body.rowId, receiptId: body.receiptId, contributors, status: body.status });
  await clickUI(`${dialog} button`, { name: 'Close' });
  await waitUI(`!document.querySelector(${JSON.stringify(dialog)})`);
  recordRender(name, startedAt);
  return identity;
};

try {
  const apiBuildStartedAt = new Date().toISOString();
  report.apiBuildFreeze = { target: apiBuildTarget, startedAt: apiBuildStartedAt };
  frozenApiBuild = await captureApiBuildFreeze(readApiBuildStamp);
  report.apiBuildFreeze = { ...report.apiBuildFreeze, initial: frozenApiBuild.initial };
  const query = `FOR s IN Specimen FILTER s.project=="${project}" AND s.dataset_generation=="${generation}" SORT s._key LIMIT 2 RETURN {id:s.id,resourceType:s.resourceType,generation:s.dataset_generation,project:s.project}`;
  const raw = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const sources = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert.equal(sources.length, 2, 'The raw CDA oracle must select exactly two Specimen resources');
  assert.equal(new Set(sources.map(source => source.id)).size, 2, 'The raw CDA witness must contain distinct FHIR IDs');
  assert(sources.every(source => source.project === project && source.generation === generation && source.resourceType === resourceType));
  report.oracle = { query, sources, sourceIDs: sources.map(source => source.id) };

  await api(root, { name: explorer, title: 'Cohort membership revision lifecycle' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, generation);
  const scopeDigest = builder.catalog.authorizationScopeDigest;
  assert(scopeDigest, 'The table catalog must bind the active authorization scope');
  const node = builder.catalog.nodes.find(candidate => candidate.resourceType === resourceType);
  assert(node, `${resourceType} must be in the active catalog`);
  await command([{ type: 'CREATE_TABLE', title: 'Cohort membership revision QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(idField, 'The Specimen FHIR ID field must be available');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Source Specimen ID' }]);
  const selection = await api(selections, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `cohort-membership-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: sources.map(source => ({ project, generation, resourceType, id: source.id })) } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, resourceType);
  assert.equal(selection.scopeDigest, scopeDigest);
  assert.equal(selection.memberCount, sources.length);
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const directRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(directRoute, 'The exact CDA selection must have a direct root route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  const selectionPage = await api(`${selections}/${selection.id}?limit=100`);
  assert.equal(selectionPage.revision.id, selection.id);
  assert.equal(selectionPage.revision.scopeDigest, scopeDigest);
  assert.equal(selectionPage.revision.membershipDigest, selection.membershipDigest);
  assert.equal(selectionPage.revision.memberCount, sources.length);
  const selectedRefs = selectionPage.members.map(member => member.ref).map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  const oracleRefs = sources.map(source => `${project}/${generation}/${resourceType}/${source.id}`).sort();
  assert.deepEqual(selectedRefs, oracleRefs, 'The attached selection must contain exactly the independent raw CDA witnesses');
  const memberKeyByID = new Map(selectionPage.members.map(member => [member.ref.id, member.memberKey]));
  assert.equal(memberKeyByID.size, 2);
  assert([...memberKeyByID.values()].every(Boolean));
  let finalSelectionRevisionID = selection.id;
  let finalSourceIDs = report.oracle.sourceIDs;
  report.oracle.selection = selection;
  report.oracle.members = selectionPage.members.map((member, index) => ({
    memberKey: member.memberKey, ref: member.ref, uiLabel: `Record ${index + 1} · ${member.ref.id}`,
  }));

  browser = await launchBrowser({ evidence, appOrigins: [apiOrigin, uiOrigin], noAuth: process.env.LOOM_CDA_NO_AUTH === '1' });
  requestCapture = captureCDARequests(browser.page, {
    apiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: `${root}/${explorer}`,
    report,
    shouldReportHttpError: path => !path.endsWith('/favicon.ico'),
  });

  let start = Date.now();
  await navigateUI(`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitUI(`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await clickUI(`[data-testid="construction-table-${outputId}"]`);
  await waitUI(`document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  let table = await waitTable(sources.length);
  assert.deepEqual(sorted(table.rows.map(row => row[0])), sorted(report.oracle.sourceIDs));
  recordRender('open-exact-source-selection', start);

  const first = await createCohortNatively(report.oracle.sourceIDs, memberKeyByID, 'all-members');
  await selectPolicyAndWait(first.revision.revisionId, 'EXCLUDE', 'initial-cohort-exclude-preview');
  table = await applyRowDefinition(1, report.oracle.sourceIDs, 'apply-initial-two-member-cohort', false);
  assert.equal(doc(builder).rows.groups.source.explicit.revisionId, first.revision.revisionId);
  assert.equal(doc(builder).rows.groups.source.explicit.unassignedMemberPolicy, 'EXCLUDE');
  assert.equal(doc(builder).population.selectionRevisionId, selection.id);
  assert(table.rows[0].some(cell => cell.includes(report.oracle.sourceIDs[0])));
  assert(table.rows[0].some(cell => cell.includes(report.oracle.sourceIDs[1])));
  const initialIdentity = await inspectFirstRow('inspect-initial-two-member-cohort', report.oracle.sourceIDs);

  start = Date.now();
  await clickUI('[data-testid="construction-action-add-columns"]');
  await clickUI('[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitUI(`document.querySelector('[data-testid="construction-add-columns-source"]')`);
  await clickUI('[data-testid="feature-catalog-raw-fields"] summary');
  await waitUI(`document.querySelector('input[aria-label="Select Specimen.id"]:not(:disabled)')`);
  await clickUI('input[aria-label="Select Specimen.id"]');
  await clickUI('[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  const fieldProposal = await waitChoiceProposal(start);
  assert.equal(fieldProposal.result.rows.length, 1, 'The retained member field proposal must contain one cohort row');
  assertMemberField(fieldProposal.result.rows[0], ['Group label', 'Group ordinal', 'Members', memberFieldLabel], report.oracle.sourceIDs);
  start = Date.now();
  await clickUI('[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  await waitUI(`!document.querySelector('[data-testid="construction-choice-proposal-panel"]')&&!document.body.innerText.includes('Loading your table…')`);
  await waitSavedPreview(start);
  table = await waitTable(1);
  assertGroupRow(table, report.oracle.sourceIDs);
  recordRender('apply-member-field', start);
  builder = await api(base + '/builder');
  assert.equal(doc(builder).rows.groups.source.explicit.revisionId, first.revision.revisionId);
  const memberColumn = doc(builder).columns.find(column => column.label === memberFieldLabel);
  assert(memberColumn, `Applying the member field must add the ${memberFieldLabel} column`);
  const memberRowValue = doc(builder).rows.groups.rowValues.find(value => value.columnId === memberColumn.columnId);
  assert(memberRowValue, 'The named cohort must retain the selected member-field row value');

  await clickUI('[data-testid="construction-close-operation-editor"]');
  start = Date.now();
  await clickUI('[data-testid="construction-action-keep-rows"]');
  const filterEditor = '[data-testid="construction-filter-editor"]';
  await waitUI(`document.querySelector(${JSON.stringify(filterEditor + ' select[aria-label="Column"]:not(:disabled)')})`);
  const groupLabelOption = await page().locator(`${filterEditor} select[aria-label="Column"] option`).evaluateAll(options =>
    options.find(option => option.textContent.trim().startsWith('Group label ('))?.value);
  assert(groupLabelOption, 'The downstream filter must be able to select the cohort group label');
  await selectUI(`${filterEditor} select[aria-label="Column"]`, groupLabelOption);
  await selectUI(`${filterEditor} select[aria-label="Condition"]`, 'EQUALS');
  await waitUI(`document.querySelector(${JSON.stringify(filterEditor + ' input[aria-label="Value"]:not(:disabled)')})`);
  await replaceInput(`${filterEditor} input[aria-label="Value"]`, groupLabel);
  const tableBeforeFilter = await waitTable(1);
  const filteredProposal = await waitConstructionProposal(start, entry => entry.body?.candidateConstruction?.steps?.some(step =>
    step.operation?.kind === 'FILTER' && step.operation.filter.operator === 'EQUALS' &&
    step.operation.filter.values?.some(value => value.kind === 'STRING' && value.string === groupLabel)));
  assert.deepEqual(filteredProposal.result.rows, tableBeforeFilter.rows, 'The downstream group-label filter must retain the exact cohort/member-field output row');
  const nativeFilterStep = filteredProposal.request.body.candidateConstruction.steps.find(step => step.operation?.kind === 'FILTER');
  assert(nativeFilterStep, 'The native downstream filter request must include its authored step');
  recordRender('preview-downstream-group-filter', start);
  start = Date.now();
  await clickUI('[data-testid="construction-apply-proposal"]');
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await waitSavedPreview(start);
  table = await waitTable(1);
  assertGroupRow(table, report.oracle.sourceIDs);
  recordRender('apply-downstream-group-filter', start);
  builder = await api(base + '/builder');
  const beforeReplacement = structuredClone(doc(builder));
  const savedFilter = beforeReplacement.construction.steps.find(step => step.id === nativeFilterStep.id);
  assert(savedFilter && savedFilter.operation.kind === 'FILTER');
  assert.deepEqual(savedFilter.operation.filter, nativeFilterStep.operation.filter);
  assert.equal(beforeReplacement.population.selectionRevisionId, selection.id);
  assert.equal(beforeReplacement.rows.groups.source.explicit.revisionId, first.revision.revisionId);
  const initialFinalIdentity = await inspectFirstRow('inspect-two-members-beneath-filter-and-field', report.oracle.sourceIDs);
  assert.equal(initialFinalIdentity, initialIdentity, 'Adding downstream filter/member field must preserve cohort row identity');

  const second = await createCohortNatively([report.oracle.sourceIDs[0]], memberKeyByID, 'one-member-replacement');
  assert.notEqual(second.revision.revisionId, first.revision.revisionId, 'Membership replacement must pin a distinct immutable cohort revision');
  await selectPolicyAndWait(second.revision.revisionId, 'EXCLUDE', 'subset-revision-preview');
  const beforeCancel = await api(base + '/builder');
  const beforeCancelTable = await waitTable(1);
  assertGroupRow(beforeCancelTable, report.oracle.sourceIDs);
  start = Date.now();
  await clickUI('[aria-label="Row definition settings"] button', { name: 'Cancel' });
  await waitUI(`!document.querySelector('[aria-label="Row definition settings"]')`);
  table = await waitTable(1);
  assertGroupRow(table, report.oracle.sourceIDs);
  builder = await api(base + '/builder');
  assert.deepEqual(doc(builder), doc(beforeCancel), 'Cancel must preserve the original pinned revision and every downstream operation');
  assert.deepEqual(table.rows, beforeCancelTable.rows, 'Cancel must leave the two-member output values untouched');
  recordRender('cancel-subset-revision-preview', start);

  const rowSelector = await openRows();
  const revisionOption = `explicit:${second.revision.revisionId}`;
  const choices = await page().locator(rowSelector).locator('option').evaluateAll(options => options.map(option => ({ value: option.value, disabled: option.disabled, text: option.text })));
  assert(choices.some(option => option.value === revisionOption && !option.disabled), 'The canceled immutable revision must remain available for native reattachment');
  await selectUI(rowSelector, revisionOption);
  await waitUI(`document.querySelector('select[aria-label="Unmatched record policy"]')?.disabled===false`);
  await selectPolicyAndWait(second.revision.revisionId, 'EXCLUDE', 'rebind-subset-revision-preview');
  const tableAfterReplacement = await applyRowDefinition(1, [report.oracle.sourceIDs[0]], 'apply-one-member-revision-under-filter-and-field');
  builder = await api(base + '/builder');
  const afterReplacement = doc(builder);
  assert.equal(afterReplacement.rows.groups.source.explicit.revisionId, second.revision.revisionId);
  assert.equal(afterReplacement.rows.groups.source.explicit.unassignedMemberPolicy, 'EXCLUDE');
  assert.equal(afterReplacement.population.selectionRevisionId, selection.id);
  assert.deepEqual(afterReplacement.construction, beforeReplacement.construction, 'Rebinding must retain the downstream authored filter exactly');
  assert.deepEqual(afterReplacement.columns, beforeReplacement.columns, 'Rebinding must retain source and member-field column definitions');
  assert.deepEqual(afterReplacement.rows.groups.rowValues, beforeReplacement.rows.groups.rowValues, 'Rebinding must retain the cohort member-field policy');
  assertGroupRow(tableAfterReplacement, [report.oracle.sourceIDs[0]]);
  const replacementIdentity = await inspectFirstRow('inspect-one-member-revision-beneath-filter-and-field', [report.oracle.sourceIDs[0]]);
  assert.notEqual(replacementIdentity, initialFinalIdentity, 'A new immutable revision must produce its own cohort row identity');
  report.replacement = {
    sourceSelectionRevisionId: second.revision.sourceSelectionRevisionId,
    previousRevisionId: first.revision.revisionId,
    revisionId: second.revision.revisionId,
    previousIDs: report.oracle.sourceIDs,
    currentIDs: [report.oracle.sourceIDs[0]],
    preservedFilterStepId: savedFilter.id,
    preservedFilter: savedFilter.operation.filter,
    preservedMemberColumn: memberFieldLabel,
    initialIdentity: initialFinalIdentity,
    replacementIdentity,
  };

  start = Date.now();
  await navigateUI(`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitUI(`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await clickUI(`[data-testid="construction-table-${outputId}"]`);
  table = await waitTable(1);
  assertGroupRow(table, [report.oracle.sourceIDs[0]]);
  builder = await api(base + '/builder');
  assert.equal(doc(builder).rows.groups.source.explicit.revisionId, second.revision.revisionId);
  assert.deepEqual(doc(builder).construction, beforeReplacement.construction);
  recordRender('reload-replacement-revision-with-dependent-operations', start);
  const reloadedIdentity = await inspectFirstRow('inspect-reloaded-one-member-revision', [report.oracle.sourceIDs[0]]);
  assert.equal(reloadedIdentity, replacementIdentity, 'Reload must preserve the replacement revision row identity');

  if (changeSourceCollection) {
    const excludedSourceID = report.oracle.sourceIDs[1];
    const coverageStart = Date.now();
    const rowSelectorForCoverage = await openRows();
    assert(rowSelectorForCoverage);
    await clickUI('[aria-label="Starting collection"] button', { name: 'Check selected-resource coverage' });
    const coverageRequest = await waitNative('/population-mapping', coverageStart);
    report.populationCoverageRequest = {
      requestId: coverageRequest.requestId,
      status: coverageRequest.status,
      body: coverageRequest.body,
      response: coverageRequest.response,
    };
    assert.equal(coverageRequest.status, 200,
      `Native population coverage request failed: ${JSON.stringify(report.populationCoverageRequest)}`);
    await waitUI(`document.querySelector('[data-testid="population-coverage-report"]')?.innerText.includes('2 selected · 1 produce rows · 1 needs attention')`, 5000);
  const coverage = await page().getByTestId('population-coverage-report').evaluate(element => ({
      summary: element.querySelector('p')?.innerText,
      unmapped: [...element.querySelectorAll('li span')].map(item => item.innerText.trim()),
    }));
    assert.equal(coverage.summary, '2 selected · 1 produce rows · 1 needs attention');
    assert.deepEqual(coverage.unmapped, [`${resourceType}/${excludedSourceID}`],
      'Independent exact one-member cohort rows must identify only the other raw selected Specimen as unaccounted for');
    recordRender('identify-source-collection-member-to-remove', coverageStart);

    const collectionChangeStart = Date.now();
    await clickUI('[aria-label="Starting collection"] button', { name: 'Remove from collection' });
    const populationCommand = await waitNative('/commands', collectionChangeStart, entry =>
      entry.status === 200 && entry.body?.commands?.some(command => command.type === 'SET_TABLE_POPULATION'));
    const populationChange = populationCommand.body.commands.find(command => command.type === 'SET_TABLE_POPULATION');
    const nativeSelectionCreate = report.nativeRequests.findLast(entry => pathOf(entry).endsWith('/selections') &&
      entry.startedAt >= collectionChangeStart && entry.method === 'POST');
    assert(nativeSelectionCreate, 'Removing a native uncovered record must create a derived source collection revision');
    assert.equal(nativeSelectionCreate.body?.source?.kind, 'selectionRevision');
    assert.equal(nativeSelectionCreate.body?.source?.selectionRevision?.selectionRevisionId, selection.id,
      'The source collection revision must derive from the exact two-member pinned selection');
    assert.deepEqual(nativeSelectionCreate.body?.exclusions?.map(ref => ref.id), [excludedSourceID],
      'Native collection removal must exclude exactly the independently identified unaccounted-for Specimen');
    const variantID = populationChange.selectionRevisionId;
    assert(variantID && variantID !== selection.id, 'The authored table must attach a new immutable source collection revision');
    assert.equal(populationChange.outputId, outputId);
    await waitSavedPreview(collectionChangeStart);
    const settingsOpen = await page().locator('[aria-label="Row definition settings"]').count() === 1;
    if (settingsOpen) {
      await clickUI('[aria-label="Row definition settings"] button', { name: 'Back to table' });
      await waitUI(`!document.querySelector('[aria-label="Row definition settings"]')`);
    }
    const oneMemberTable = await waitTable(1);
    assertGroupRow(oneMemberTable, [report.oracle.sourceIDs[0]]);
    recordRender('apply-source-collection-two-to-one-under-filter-and-field', collectionChangeStart);

    builder = await api(base + '/builder');
    const afterCollectionChange = structuredClone(doc(builder));
    assert.equal(afterCollectionChange.population.selectionRevisionId, variantID);
    assert.equal(afterCollectionChange.rows.groups.source.explicit.revisionId, second.revision.revisionId,
      'Changing the starting collection must leave the separately pinned one-member cohort revision intact');
    assert.equal(afterCollectionChange.rows.groups.source.explicit.unassignedMemberPolicy, 'EXCLUDE');
    assert.deepEqual(afterCollectionChange.construction, beforeReplacement.construction,
      'Changing the starting collection must preserve the authored downstream GroupLabel filter');
    assert.deepEqual(afterCollectionChange.columns, beforeReplacement.columns,
      'Changing the starting collection must preserve source and member-field column bindings');
    assert.deepEqual(afterCollectionChange.rows.groups.rowValues, beforeReplacement.rows.groups.rowValues,
      'Changing the starting collection must preserve the authored cohort member-field policy');

    const derivedSelectionPage = await api(`${selections}/${variantID}?limit=100`);
    assert.equal(derivedSelectionPage.revision.id, variantID);
    assert.equal(derivedSelectionPage.revision.source.kind, 'SELECTION_REVISION');
    assert.equal(derivedSelectionPage.revision.source.revisionId, selection.id);
    assert.equal(derivedSelectionPage.revision.source.membershipDigest, selection.membershipDigest);
    assert.equal(derivedSelectionPage.revision.scopeDigest, scopeDigest);
    assert.equal(derivedSelectionPage.revision.generation, generation);
    assert.equal(derivedSelectionPage.revision.resourceType, resourceType);
    assert.equal(derivedSelectionPage.revision.memberCount, 1);
    assert.notEqual(derivedSelectionPage.revision.membershipDigest, selection.membershipDigest);
    assert.deepEqual(derivedSelectionPage.revision.exclusions, [
      { project, generation, resourceType, id: excludedSourceID },
    ], 'The immutable source revision must preserve the exact excluded-resource scope');
    assert.deepEqual(derivedSelectionPage.members.map(member => member.ref), [
      { project, generation, resourceType, id: report.oracle.sourceIDs[0] },
    ], 'The native collection revision must retain exactly the surviving raw scoped Specimen');
    const rawMembershipQuery = `FOR member IN loom_explorer_selection_members FILTER member.selectionId == ${JSON.stringify(variantID)} AND member.project == ${JSON.stringify(project)} AND member.generation == ${JSON.stringify(generation)} AND member.resourceType == ${JSON.stringify(resourceType)} SORT member.id RETURN {memberKey:member.memberKey,id:member.id,project:member.project,generation:member.generation,resourceType:member.resourceType}`;
    const rawMembership = spawnSync('rtk', [
      'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
      '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(rawMembershipQuery)}).toArray()));`,
    ], { encoding: 'utf8', timeout: 30000 });
    assert.equal(rawMembership.status, 0, rawMembership.stderr);
    const opening = rawMembership.stdout.indexOf('[');
    assert(opening >= 0, `Raw selection membership did not return JSON: ${rawMembership.stdout.slice(0, 500)}`);
    const rawVariantMembers = JSON.parse(rawMembership.stdout.slice(opening));
    assert.deepEqual(rawVariantMembers, [{
      memberKey: derivedSelectionPage.members[0].memberKey,
      id: report.oracle.sourceIDs[0], project, generation, resourceType,
    }], 'Independent Arango membership rows must prove the exact one-member source collection revision');
    assertGroupRow(oneMemberTable, [report.oracle.sourceIDs[0]]);
    const collectionIdentity = await inspectFirstRow('inspect-one-source-under-filter-and-field', [report.oracle.sourceIDs[0]]);
    assert.equal(collectionIdentity, replacementIdentity,
      'The source collection revision must preserve the existing named-cohort output row identity');
    report.sourceCollectionChange = {
      previousSelectionRevisionId: selection.id,
      previousMembershipDigest: selection.membershipDigest,
      selectionRevisionId: variantID,
      membershipDigest: derivedSelectionPage.revision.membershipDigest,
      exactIDs: [report.oracle.sourceIDs[0]],
      excludedID: excludedSourceID,
      rawMembershipQuery,
      rawMembers: rawVariantMembers,
      retainedGroupRevisionId: second.revision.revisionId,
      retainedFilterStepId: savedFilter.id,
      retainedMemberField: memberFieldLabel,
      rowIdentity: collectionIdentity,
    };
    finalSelectionRevisionID = variantID;
    finalSourceIDs = [report.oracle.sourceIDs[0]];

    const collectionReloadStart = Date.now();
    await navigateUI(`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await waitUI(`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
    await clickUI(`[data-testid="construction-table-${outputId}"]`);
    table = await waitTable(1);
    assertGroupRow(table, finalSourceIDs);
    builder = await api(base + '/builder');
    assert.equal(doc(builder).population.selectionRevisionId, finalSelectionRevisionID);
    assert.equal(doc(builder).rows.groups.source.explicit.revisionId, second.revision.revisionId);
    assert.deepEqual(doc(builder).construction, beforeReplacement.construction);
    recordRender('reload-one-member-source-collection-with-downstream-operations', collectionReloadStart);
    const reloadedCollectionIdentity = await inspectFirstRow('inspect-reloaded-source-collection-revision', finalSourceIDs);
    assert.equal(reloadedCollectionIdentity, collectionIdentity,
      'Reload must preserve both the new source collection and named-cohort row identity');
  }

  await clickUI(`[data-testid="construction-history-step-${savedFilter.id}"]`);
  start = Date.now();
  await clickUI(`[data-testid="construction-remove-step-${savedFilter.id}"]`);
  const removeFilter = await waitConstructionProposal(start);
  assert(JSON.stringify(removeFilter.request.body).includes(savedFilter.id), 'Filter-removal proposal must name the saved downstream step');
  assert.deepEqual(removeFilter.result.rows, table.rows, 'Removing the filter must restore the exact saved one-member row');
  const removePreview = removeFilter.result.rows[0];
  assert(removePreview.includes(groupLabel));
  assert(removePreview.some(cell => cell.includes(report.oracle.sourceIDs[0])));
  recordRender('preview-downstream-filter-removal', start);
  start = Date.now();
  await clickUI('[data-testid="construction-apply-proposal"]');
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await waitSavedPreview(start);
  table = await waitTable(1);
  assertGroupRow(table, [report.oracle.sourceIDs[0]]);
  recordRender('remove-downstream-filter', start);
  builder = await api(base + '/builder');
  assert(!doc(builder).construction.steps.some(step => step.id === savedFilter.id));
  assert.equal(doc(builder).rows.groups.source.explicit.revisionId, second.revision.revisionId);

  await clickUI('button', { name: 'Columns' });
  start = Date.now();
  await clickUI(`button[aria-label="Remove ${memberFieldLabel} column"]`);
  await waitUI(`!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount')===${JSON.stringify(String(table.headers.length - 1))}`);
  recordRender('remove-member-field', start);
  builder = await api(base + '/builder');
  assert.equal(doc(builder).rows.groups.source.explicit.revisionId, second.revision.revisionId);
  assert(!(doc(builder).rows.groups.rowValues ?? []).some(value => value.columnId === memberColumn.columnId));
  table = await waitTable(1);
  const groupLabelIndex = table.headers.findIndex(header => header.toLowerCase() === 'group label');
  const membersIndex = table.headers.findIndex(header => header.toLowerCase() === 'members');
  assert.equal(table.rows[0][groupLabelIndex], groupLabel);
  assert(table.rows[0][membersIndex].includes(report.oracle.sourceIDs[0]));

  const recordsSelector = await openRows();
  const recordsStart = Date.now();
  await selectUI(recordsSelector, 'records');
  const recordsProposal = await waitNative('/row-definition-proposals', recordsStart, entry => entry.body?.selection?.kind === 'RECORDS');
  assert.equal(recordsProposal.status, 200, JSON.stringify(recordsProposal.response));
  await waitUI(`[...document.querySelectorAll('[aria-label="Row definition settings"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled)`);
  recordRender('preview-remove-cohort-row-definition', recordsStart);
  const restoredTable = await applyRowDefinition(finalSourceIDs.length, [], 'remove-cohort-row-definition');
  assert.deepEqual(sorted(restoredTable.rows.map(row => row[0])), sorted(finalSourceIDs));
  builder = await api(base + '/builder');
  assert.equal(doc(builder).rows.kind, 'RECORDS');
  assert.equal(doc(builder).population.selectionRevisionId, finalSelectionRevisionID);
  assert(!doc(builder).construction.steps.some(step => step.id === savedFilter.id));
  const reloadedBaselineStart = Date.now();
  await navigateUI(`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitUI(`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await clickUI(`[data-testid="construction-table-${outputId}"]`);
  const finalTable = await waitTable(finalSourceIDs.length);
  assert.deepEqual(sorted(finalTable.rows.map(row => row[0])), sorted(finalSourceIDs));
  builder = await api(base + '/builder');
  assert.equal(doc(builder).rows.kind, 'RECORDS');
  assert.equal(doc(builder).population.selectionRevisionId, finalSelectionRevisionID);
  recordRender(changeSourceCollection ? 'reload-restored-one-member-source-scope' : 'reload-restored-exact-source-scope', reloadedBaselineStart);

  await requestCapture?.flush();
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
} catch (error) {
  const apiBuildInvalidated = error instanceof ApiBuildFreezeError;
  report.status = apiBuildInvalidated ? 'invalidated' : 'failed';
  report.error = String(error.stack ?? error);
  if (apiBuildInvalidated) {
    report.apiBuildFreeze = {
      ...report.apiBuildFreeze,
      initial: error.before,
      ...(error.after?.checked ? { after: error.after } : {}),
      unchanged: false,
      invalidatesRun: true,
      productFailure: false,
      reason: error.reason,
    };
    report.priorStatus = 'not-started';
  }
  process.exitCode = 1;
  report.savedBuilderAtFailure = await api(base + '/builder').catch(fetchError => ({ readError: String(fetchError) }));
  if (browser) {
    const failedAction = report.activeAction;
    const failedActionElapsedMs = failedAction?.startedAt ? Date.now() - failedAction.startedAt : undefined;
    report.failureEvidence = await browser.captureFailure(error, { phase: 'cohort-membership-revision', action: failedAction, elapsedMs: failedActionElapsedMs });
    if (failedAction) report.failedAction = { label: failedAction.label, locator: failedAction.locator, startedAt: failedAction.startedAt, elapsedMs: failedActionElapsedMs };
    delete report.activeAction;
    report.failureUI = await browser.page.locator('body').innerText().catch(String);
  }
} finally {
  try { report.sourceFreeze = await sourceFreeze.assertUnchanged(); } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = { unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false, error: String(error) };
    process.exitCode = 1;
  }
  await requestCapture?.flush();
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
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({
  status: report.status, evidence,
  cases: report.cases.map(item => ({ name: item.name, durationMs: item.durationMs })),
  error: report.error,
}));
