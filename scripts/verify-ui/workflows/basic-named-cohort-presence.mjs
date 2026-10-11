import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { performance } from 'node:perf_hooks';
import { browserURL } from './builder-url.mjs';
import { fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { canonicalProjectID } from '../../loom-dev.mjs';
import {
  assertBasicNamedCohortPresenceRows,
  buildBasicNamedCohortPresenceOracle,
} from '../helpers/basic-named-cohort-presence-oracle.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const ROUTE_LABEL = 'Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation';
const editedPresenceLabel = 'Reviewed related Observation presence';
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();

const readFixture = fixtureDir => {
  const paths = ['Patient.ndjson', 'Specimen.ndjson', 'Observation.ndjson'].map(name => join(fixtureDir, name));
  const contents = paths.map(path => {
    const stat = statSync(path);
    assert(stat.size > 0 && stat.size <= 8_192, `Basic PRESENCE fixture must remain small: ${path}`);
    const bytes = readFileSync(path);
    return { path, bytes, rows: bytes.toString('utf8').trim().split(/\r?\n/).map(line => JSON.parse(line)) };
  });
  const [patients, specimens, observations] = contents.map(entry => entry.rows);
  return {
    files: contents.map(({ path, bytes, rows }) => ({
      path, sha256: createHash('sha256').update(bytes).digest('hex'), recordCount: rows.length,
    })),
    oracle: buildBasicNamedCohortPresenceOracle({ patients, specimens, observations }),
  };
};

const visibleGrid = async (page, proposal = false) => {
  const selector = proposal ? '[data-testid="construction-proposal-preview"] table' : '[data-testid="preview-table-scroll"] [role="table"]';
  const table = page.locator(selector);
  await table.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  return table.evaluate((node, proposal) => {
    const headers = [...node.querySelectorAll(proposal ? 'thead th' : '[role="columnheader"]')]
      .map(cell => String(cell.querySelector('span')?.innerText ?? cell.innerText ?? '').replace(/\s+/g, ' ').trim());
    const rows = proposal
      ? [...node.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => String(cell.innerText ?? '').replace(/\s+/g, ' ').trim()))
      : [...node.querySelectorAll('[role="row"]')].slice(1)
        .filter(row => row.querySelector('[role="cell"]'))
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => String(cell.innerText ?? '').replace(/\s+/g, ' ').trim()));
    return { headers, rows };
  }, proposal);
};

export const basicNamedCohortPresenceWorkflow = async ({ page, action, check, networkObserver, report, target, seed }, context = {}) => {
  const lifecycleStartedAt = performance.now();
  configureNativePage(page);
  assert.equal(report.scenario, 'basic-named-cohort-presence');
  assert.equal(seed?.fresh, true, 'Named-cohort PRESENCE must run in a fresh isolated Basic fixture project.');
  assert.equal(context.custom, false, 'Named-cohort PRESENCE must use the registered Basic fixture.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected the disposable Basic fixture project.');
  assert(target.fixtureGeneration && target.fixtureDir, 'The Basic fixture project, generation, and path must be explicit.');
  const project = target.fixtureProject;
  const selectionProject = canonicalProjectID(project);

  const { files, oracle } = readFixture(target.fixtureDir);
  const groups = oracle.groups;
  report.target.fixtureOracle = {
    scope: 'Basic literal Patient/Specimen/Observation fixture only; this does not establish any CDA source witness.',
    project: selectionProject,
    storageProject: project,
    generation: target.fixtureGeneration,
    files,
    sourceCounts: oracle.sourceCounts,
    expectedGroups: groups.map(({ groupId, label, ordinal, memberIDs, patientIDs, observationIDs, presence }) => ({
      groupId, label, ordinal, memberIDs, patientIDs, observationIDs, presence,
    })),
  };
  check('correctness', 'literal Basic fixture proves explicit nonempty zero-match Group false and positive comparator true',
    groups.length === 2 && groups[0].memberIDs.length === 1 && groups[0].observationIDs.length === 0 &&
      groups[0].presence === false && groups[1].observationIDs.length === 1 && groups[1].presence === true,
    report.target.fixtureOracle);

  const click = (label, locator, after) => action(label, locator,
    () => locator.click({ timeout: ACTION_TIMEOUT_MS }), { timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS, after });
  const fill = (label, locator, value, after) => action(label, locator,
    () => locator.fill(value, { timeout: ACTION_TIMEOUT_MS }), { timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS, after, editable: true });
  const select = (label, locator, value, after) => action(label, locator,
    () => locator.selectOption(value, { timeout: ACTION_TIMEOUT_MS }), { timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS, after });
  const waitVisible = (locator, timeout = ACTION_TIMEOUT_MS) => locator.waitFor({ state: 'visible', timeout });
  const waitHidden = (locator, timeout = ACTION_TIMEOUT_MS) => locator.waitFor({ state: 'hidden', timeout });
  const waitForInputValue = (selector, value) => page.waitForFunction(({ selector, value }) =>
    document.querySelector(selector)?.value === value, { selector, value }, { timeout: ACTION_TIMEOUT_MS });
  const waitForHistorySelection = (stepId, controlKind) => page.waitForFunction(({ stepId, controlKind }) => {
    const history = [...document.querySelectorAll('[data-testid^="construction-history-step-"]')]
      .find(node => node.getAttribute('data-testid') === `construction-history-step-${stepId}`);
    const control = [...document.querySelectorAll(`[data-testid^="construction-${controlKind}-step-"]`)]
      .find(node => node.getAttribute('data-testid') === `construction-${controlKind}-step-${stepId}`);
    return history?.getAttribute('aria-pressed') === 'true' && control instanceof HTMLButtonElement && !control.disabled;
  }, { stepId, controlKind }, { timeout: ACTION_TIMEOUT_MS });
  const waitForPreview = async (rowCount, proposal = false) => page.waitForFunction(({ rowCount, proposal }) => {
    if (proposal) {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return panel?.dataset.proposalStatus === 'ready' &&
        document.querySelectorAll('[data-testid="construction-proposal-preview"] tbody tr[data-testid="construction-proposal-preview-row"]').length === rowCount;
    }
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return preview?.dataset.previewStatus === 'ready' && table?.getAttribute('aria-rowcount') === String(rowCount + 1) &&
      !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:');
  }, { rowCount, proposal }, { timeout: ACTION_TIMEOUT_MS });
  const assertRows = async (phase, expectedLabel = 'Related Observation presence', proposal = false) => {
    const grid = await visibleGrid(page, proposal);
    const labelIndex = grid.headers.findIndex(header => header.toLowerCase() === 'group label');
    const membersIndex = grid.headers.findIndex(header => header.toLowerCase() === 'members');
    const presenceIndex = grid.headers.findIndex(header => header.toLowerCase() === expectedLabel.toLowerCase());
    assert(labelIndex >= 0, `${phase}: preview lacks Group label: ${JSON.stringify(grid.headers)}`);
    assert(membersIndex >= 0, `${phase}: preview lacks Group Members: ${JSON.stringify(grid.headers)}`);
    assert(presenceIndex >= 0, `${phase}: preview lacks ${expectedLabel}: ${JSON.stringify(grid.headers)}`);
    const rows = grid.rows.map(row => ({
      'Group label': row[labelIndex],
      Members: row[membersIndex],
      [expectedLabel]: row[presenceIndex] === 'true' ? true : row[presenceIndex] === 'false' ? false : row[presenceIndex],
    }));
    const results = assertBasicNamedCohortPresenceRows(rows, {
      memberColumn: 'Members', presenceColumn: expectedLabel,
      oracle: { ...oracle, labelColumn: 'Group label' }, phase,
    });
    return { ...grid, results };
  };

  const browserOrigin = new URL(target.uiUrl).origin;
  const browserExplorerCollectionPath = fixtureProjectExplorerCollectionPath(project);
  const directAPIExplorerCollectionPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  await page.goto(browserURL(target, project, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  await page.waitForFunction(() => document.body.innerText.includes('Build your first table') || document.body.innerText.includes('Dataset graph'),
    undefined, { timeout: ACTION_TIMEOUT_MS });
  const newExplorer = page.locator('summary').filter({ hasText: /^New explorer$/ });
  await click('open fresh Basic Explorer creation', newExplorer, () => waitVisible(page.locator('#new-explorer-name')));
  const explorerTitle = `Verify ${project.slice(-10)} named-cohort PRESENCE`;
  await fill('name the disposable named-cohort Explorer', page.locator('#new-explorer-name'), explorerTitle,
    () => waitForInputValue('#new-explorer-name', explorerTitle));
  const createdExplorerResponse = networkObserver.waitForResponse(entry =>
    entry.origin === browserOrigin && entry.method === 'POST' &&
      entry.path === browserExplorerCollectionPath && entry.request?.title === explorerTitle,
  { fromIndex: networkObserver.requests.length, timeoutMs: ACTION_TIMEOUT_MS });
  await click('create the disposable named-cohort Explorer', page.getByRole('button', { name: 'Create blank', exact: true }), async () => {
    const entry = await createdExplorerResponse;
    assert(entry.status >= 200 && entry.status < 300, `Native Explorer creation returned HTTP ${entry.status}.`);
    await page.waitForFunction(title => document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === title &&
      document.body.innerText.includes('Build your first table'), explorerTitle, { timeout: ACTION_TIMEOUT_MS });
  });
  const explorer = await page.locator('select[aria-label="Explorer"]').inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The case must use its newly created Explorer.');
  report.target.explorer = explorer;
  const rootPath = `${directAPIExplorerCollectionPath}/${encodeURIComponent(explorer)}/authoring/v2`;
  const selectionsPath = rootPath.replace('/authoring/v2', '/selections');
  const readJSON = async (path, data) => {
    const request = data
      ? page.context().request.post(`${target.apiUrl}${path}`, { data, timeout: ACTION_TIMEOUT_MS })
      : page.context().request.get(`${target.apiUrl}${path}`, { timeout: ACTION_TIMEOUT_MS });
    const response = await request;
    const body = await response.json();
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 800)}`);
    return body;
  };
  const firstTableTitle = 'Basic named cohort PRESENCE source';
  await fill('name the exact Specimen source table', page.locator('#first-table-name'), firstTableTitle,
    () => waitForInputValue('#first-table-name', firstTableTitle));
  await click('create native Specimen root table', page.getByRole('button', { name: 'Choose Specimen rows', exact: true }), async () => {
    await page.waitForFunction(title => document.body.innerText.includes('DATASET WORKSPACE') && document.body.innerText.includes(title) &&
      !document.body.innerText.includes('Build your first table'), firstTableTitle, { timeout: ACTION_TIMEOUT_MS });
    await waitVisible(page.getByTestId('construction-action-add-columns'));
  });
  let builder = await readJSON(`${rootPath}/builder`);
  const documentFor = value => value.workspace.documents.find(document => document.rootResourceType === 'Specimen');
  let document = documentFor(builder);
  assert(document?.output?.id, 'The native Specimen table must be saved.');
  const outputId = document.output.id;
  report.target.outputId = outputId;
  const sourceRefs = groups.flatMap(group => group.memberIDs.map(id => ({
    project: selectionProject, generation: target.fixtureGeneration, resourceType: 'Specimen', id,
  })));
  assert.deepEqual(sourceRefs.map(ref => ref.id).sort(), groups.flatMap(group => group.memberIDs).sort());

  const selection = await readJSON(selectionsPath, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `basic-presence-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: sourceRefs } },
  });
  assert.equal(selection.project, selectionProject);
  assert.equal(selection.generation, target.fixtureGeneration);
  assert.equal(selection.resourceType, 'Specimen');
  assert.equal(selection.memberCount, sourceRefs.length);
  const selectionPage = await readJSON(`${selectionsPath}/${encodeURIComponent(selection.id)}?limit=100`);
  assert.equal(selectionPage.revision?.id, selection.id);
  assert.equal(selectionPage.revision?.project, selectionProject);
  assert.equal(selectionPage.revision?.generation, target.fixtureGeneration);
  assert.equal(selectionPage.revision?.resourceType, 'Specimen');
  assert.equal(selectionPage.revision?.memberCount, sourceRefs.length);
  assert.deepEqual(selectionPage.members.map(member => member.ref.id).sort(), sourceRefs.map(ref => ref.id).sort(),
    'Selection membership must equal every exact literal Specimen fixture identity.');
  assert(selectionPage.members.every(member => member.ref.project === selectionProject && member.ref.generation === target.fixtureGeneration &&
    member.ref.resourceType === 'Specimen'), 'Every pinned selection member must retain the exact project, generation, and resource type.');
  const memberKeys = new Map(selectionPage.members.map(member => [member.ref.id, member.memberKey]));
  assert.equal(memberKeys.size, sourceRefs.length);
  assert([...memberKeys.values()].every(Boolean), 'Every literal Specimen must have one pinned selection member key.');
  const groupPayload = groups.map(group => ({
    id: group.groupId, label: group.label, ordinal: group.ordinal,
    memberIds: group.memberIDs.map(id => memberKeys.get(id)),
  }));
  assert(groupPayload.every(group => group.memberIds.length === 1), 'Both explicit fixture groups must be nonempty.');
  const cohort = await readJSON(`${selectionsPath}/${encodeURIComponent(selection.id)}/explicit-groups`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `basic-presence-groups-${randomUUID()}`,
    groups: groupPayload,
  });
  assert.equal(cohort.sourceSelectionRevisionId, selection.id);
  assert.equal(cohort.groupCount, groups.length);
  assert.equal(cohort.memberCount, sourceRefs.length);
  assert.deepEqual(cohort.groups, groups.map(group => ({
    id: group.groupId, label: group.label, ordinal: group.ordinal, memberCount: group.memberIDs.length,
  })), 'The created explicit-group revision must preserve exact group identity, order, and nonempty membership counts.');
  report.target.selectionRevisionId = selection.id;
  report.target.explicitGroupRevisionId = cohort.revisionId;

  const directRoutes = await readJSON(`${rootPath}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
  });
  const directRoute = directRoutes.choices.find(choice => choice.route.length === 0);
  assert(directRoute, 'The exact Specimen selection must expose its direct root route.');
  const sendCommand = async commands => {
    const current = builder;
    await readJSON(`${rootPath}/commands`, {
      commandId: randomUUID(), semanticsVersion: current.workspace?.semanticsVersion ?? 10,
      snapshotToken: current.catalog.snapshotToken, expectedDraftVersion: current.draftVersion,
      expectedDraftDigest: current.draftDigest, commands,
    });
    builder = await readJSON(`${rootPath}/builder`);
    assert.equal(builder.catalog.generation, target.fixtureGeneration);
  };
  await sendCommand([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  document = documentFor(builder);
  assert.equal(document.population.selectionRevisionId, selection.id);

  const expectedDraftVersion = String(builder.draftVersion);
  const expectedDraftDigest = builder.draftDigest;
  const tableTab = page.getByTestId(`construction-table-${outputId}`);
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await tableTab.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  if (await tableTab.getAttribute('aria-current') !== 'page') await tableTab.click({ timeout: ACTION_TIMEOUT_MS });
  await page.waitForFunction(({ outputId, expectedDraftVersion, expectedDraftDigest, expectedRows }) => {
    const workspace = document.querySelector('[data-testid="construction-workspace"]');
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const selected = [...document.querySelectorAll('[data-testid^="construction-table-"][aria-current="page"]')];
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    const pending = [...document.querySelectorAll('[role="status"]')].some(node =>
      /^(Checking .* fields|Creating .* table|Adding the ID column|Loading the preview|Loom is (?:refreshing the current table draft|finishing the previous table update))/.test(node.innerText?.trim() || ''));
    return selected.length === 1 && selected[0].getAttribute('data-testid') === `construction-table-${outputId}` &&
      workspace?.dataset.draftVersion === expectedDraftVersion && workspace.dataset.draftDigest === expectedDraftDigest &&
      preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === outputId &&
      preview.dataset.currentDraftVersion === expectedDraftVersion && preview.dataset.currentDraftDigest === expectedDraftDigest &&
      table?.getAttribute('aria-rowcount') === String(expectedRows + 1) && !pending &&
      !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:');
  }, { outputId, expectedDraftVersion, expectedDraftDigest, expectedRows: groups.length }, { timeout: ACTION_TIMEOUT_MS });

  const rowSettings = page.getByTestId('construction-rows-settings-trigger');
  await click('open native row definition settings', rowSettings, () => waitVisible(page.getByRole('dialog', { name: 'Row definition settings' })));
  const shapeSelector = page.getByRole('combobox', { name: 'What should each row represent?', exact: true });
  const groupValue = `explicit:${cohort.revisionId}`;
  await select('choose the exact revision-bound explicit Group rows', shapeSelector, groupValue,
    async () => assert.equal(await shapeSelector.inputValue(), groupValue));
  const unmatchedPolicy = page.getByRole('combobox', { name: 'Unmatched record policy', exact: true });
  await select('retain only explicitly assigned source members', unmatchedPolicy, `${groupValue}:ERROR`,
    async () => assert.equal(await unmatchedPolicy.inputValue(), `${groupValue}:ERROR`));
  const applyRows = page.getByRole('button', { name: 'Apply row definition', exact: true });
  await page.waitForFunction(() => [...document.querySelectorAll('[aria-label="Row definition settings"] button')]
    .some(button => button.innerText.trim() === 'Apply row definition' && !button.disabled), undefined,
  { timeout: ACTION_TIMEOUT_MS });
  const groupedBaselineWorkspace = structuredClone(builder.workspace);
  await click('apply exact nonempty named Groups', applyRows, async () => {
    await waitVisible(page.getByTestId('preview-table-scroll'));
    await waitForPreview(groups.length);
  });
  builder = await readJSON(`${rootPath}/builder`);
  document = documentFor(builder);
  assert.equal(document.rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(document.rows.groups.source.explicit.unassignedMemberPolicy, 'ERROR');
  const groupOnlyGrid = await visibleGrid(page);
  const labelIndex = groupOnlyGrid.headers.findIndex(header => header.toLowerCase() === 'group label');
  const membersIndex = groupOnlyGrid.headers.findIndex(header => header.toLowerCase() === 'members');
  const ordinalIndex = groupOnlyGrid.headers.findIndex(header => header.toLowerCase() === 'group ordinal');
  assert(labelIndex >= 0 && ordinalIndex >= 0 && membersIndex >= 0,
    `Native Group preview omitted identity/member columns: ${JSON.stringify(groupOnlyGrid.headers)}`);
  assert.deepEqual(groupOnlyGrid.rows.map(row => row[labelIndex]).sort(), groups.map(group => group.label).sort());
  for (const group of groups) {
    const row = groupOnlyGrid.rows.find(values => values[labelIndex] === group.label);
    assert.equal(row?.[ordinalIndex], String(group.ordinal), `Native explicit Group preview reordered ${group.label}.`);
    assert(row?.[membersIndex]?.includes(group.memberIDs[0]), `Native explicit Group preview omitted exact member ${group.memberIDs[0]}.`);
    assert(!row?.[membersIndex]?.includes(groups.find(other => other.groupId !== group.groupId).memberIDs[0]),
      `Native explicit Group preview mixed memberships for ${group.label}.`);
  }
  const groupedBaselineDocument = structuredClone(document);
  check('correctness', 'native named Groups preserve exact literal Specimen members before the related operation', true,
    { groupRevisionId: cohort.revisionId, selectionRevisionId: selection.id, headers: groupOnlyGrid.headers, rows: groupOnlyGrid.rows });

  const openAddColumns = async () => {
    const editor = page.locator('[data-testid="construction-operation-editor"][data-operation-family="ADD_COLUMNS"]');
    if (!await editor.isVisible()) {
      await click('open native Add columns for related Observation presence', page.getByTestId('construction-action-add-columns'),
        () => waitVisible(editor));
    }
    const family = editor.locator('[aria-label="Column types"]')
      .getByRole('button', { name: 'Fields and related data', exact: true });
    if (await family.getAttribute('aria-pressed') !== 'true') {
      await click('choose Fields and related data', family, () => waitVisible(editor.getByTestId('construction-add-columns-source')));
    }
    const relatedDetails = editor.locator('[aria-label="Related resources"]');
    if (!await relatedDetails.evaluate(element => element.open)) {
      await click('open related resource choices', relatedDetails.locator(':scope > summary'),
        () => page.waitForFunction(() => document.querySelector('[aria-label="Related resources"]')?.open === true,
          undefined, { timeout: ACTION_TIMEOUT_MS }));
    }
    const observationSource = editor.locator('[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
    await click('select Observation as the related source', observationSource, () => page.waitForFunction(selector =>
      document.querySelector(selector)?.getAttribute('aria-pressed') === 'true',
    '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]',
    { timeout: ACTION_TIMEOUT_MS }));
    const rawFields = editor.getByTestId('feature-catalog-raw-fields');
    if (!await rawFields.evaluate(element => element.open)) {
      await click('open raw Observation fields', rawFields.locator(':scope > summary'),
        () => page.waitForFunction(() => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true,
          undefined, { timeout: ACTION_TIMEOUT_MS }));
    }
    const observationID = editor.getByRole('checkbox', { name: 'Select Observation.id', exact: true });
    await click('select Observation.id', observationID, () => page.waitForFunction(() =>
      document.querySelector('input[aria-label="Select Observation.id"]')?.checked === true,
    undefined, { timeout: ACTION_TIMEOUT_MS }));
    const addField = editor.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    await click('propose Observation.id as a related field', addField,
      () => waitVisible(page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true })));
    const dialog = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
    const otherPaths = dialog.locator('summary').filter({ hasText: 'Other relationship paths' });
    if (!await otherPaths.evaluate(element => element.parentElement.open)) {
      await click('open indirect relationship paths', otherPaths,
        () => page.waitForFunction(() => [...document.querySelectorAll('[role="dialog"] summary')]
          .some(summary => summary.innerText.includes('Other relationship paths') && summary.parentElement.open),
        undefined, { timeout: ACTION_TIMEOUT_MS }));
    }
    const routeChoice = dialog.locator(`input[type="radio"][aria-label=${JSON.stringify(ROUTE_LABEL)}]`);
    assert.equal(await routeChoice.isEnabled(), true, 'The native chooser must enable the Specimen → Patient ← Observation route.');
    await click('choose the exact Specimen to Patient to Observation route', routeChoice, () => page.waitForFunction(selector =>
      document.querySelector(selector)?.checked === true, `input[type="radio"][aria-label=${JSON.stringify(ROUTE_LABEL)}]`,
    { timeout: ACTION_TIMEOUT_MS }));
    const presenceChoice = dialog.locator('input[type="radio"][aria-label="Observation ID: Show whether a match exists"]');
    assert.equal(await presenceChoice.isEnabled(), true, 'The native chooser must enable Boolean PRESENCE.');
    await click('choose Boolean PRESENCE for Observation.id', presenceChoice, () => page.waitForFunction(() =>
      document.querySelector('input[type="radio"][aria-label="Observation ID: Show whether a match exists"]')?.checked === true,
    undefined, { timeout: ACTION_TIMEOUT_MS }));
    const addColumn = dialog.getByRole('button', { name: 'Add 1 column', exact: true });
    await click('add the native related PRESENCE column', addColumn, async () => {
      await waitForPreview(groups.length, true);
      await waitVisible(page.getByTestId('construction-proposal-ready'));
    });
    const proposalGrid = await visibleGrid(page, true);
    const outputLabel = proposalGrid.headers.find(header => /observation/i.test(header) && !['group label', 'group ordinal', 'members'].includes(header.toLowerCase()));
    assert(outputLabel, `Native PRESENCE proposal omitted its related output: ${JSON.stringify(proposalGrid.headers)}`);
    await assertRows('native related PRESENCE proposal', outputLabel, true);
    return { editor, proposalGrid, outputLabel };
  };
  const applyProposal = async expectedLabel => {
    await click('Apply native related PRESENCE operation', page.getByTestId('construction-apply-proposal'), async () => {
      await waitHidden(page.getByTestId('construction-proposal-panel'));
      await waitForPreview(groups.length);
    });
    builder = await readJSON(`${rootPath}/builder`);
    document = documentFor(builder);
    const step = document.construction.steps.find(candidate => candidate.operation.kind === 'RELATED_SOURCE');
    assert(step, 'Applied construction must contain the saved RELATED_SOURCE operation.');
    const related = step.operation.relatedSource;
    assert.equal(related.form, 'PRESENCE');
    assert.equal(related.source.kind, 'FIELD');
    assert.equal(related.source.resourceType, 'Observation');
    assert.equal(related.source.path, 'id');
    assert.equal(related.contributorRule.policy, 'ALL_MATCHES');
    assert.deepEqual(related.route.map(edge => ({ from: edge.fromResourceType, to: edge.toResourceType,
      relationship: edge.relationship, direction: edge.storageDirection })), [
      { from: 'Specimen', to: 'Patient', relationship: 'subject_Patient', direction: 'OUTBOUND' },
      { from: 'Patient', to: 'Observation', relationship: 'subject_Patient', direction: 'INBOUND' },
    ]);
    const output = step.outputs.find(column => column.id === related.outputColumnId);
    assert(output && output.type === 'boolean', 'Saved PRESENCE must own a Boolean output column.');
    if (expectedLabel) assert.equal(output.label, expectedLabel);
    const grid = await assertRows(`Applied ${expectedLabel ?? 'PRESENCE'}`, output.label);
    assert.equal(document.rows.groups.source.explicit.revisionId, cohort.revisionId);
    assert.equal(document.population.selectionRevisionId, selection.id);
    return { step, output, grid };
  };

  const firstProposal = await openAddColumns();
  const beforeCancelWorkspace = structuredClone((await readJSON(`${rootPath}/builder`)).workspace);
  await click('Cancel the initial related PRESENCE proposal', page.getByTestId('construction-cancel-proposal'), async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'));
    const after = await readJSON(`${rootPath}/builder`);
    assert.deepEqual(after.workspace, beforeCancelWorkspace, 'Cancel must not mutate the saved Group draft or authored schema.');
    const canceledGrid = await visibleGrid(page);
    const canceledLabelIndex = canceledGrid.headers.findIndex(header => header.toLowerCase() === 'group label');
    const canceledMemberIndex = canceledGrid.headers.findIndex(header => header.toLowerCase() === 'members');
    assert.deepEqual(canceledGrid.rows.map(row => row[canceledLabelIndex]).sort(), groups.map(group => group.label).sort());
    for (const group of groups) {
      const row = canceledGrid.rows.find(values => values[canceledLabelIndex] === group.label);
      assert(row[canceledMemberIndex].includes(group.memberIDs[0]), `Cancel must preserve exact ${group.label} membership.`);
    }
  });
  check('persistence', 'related PRESENCE proposal Cancel preserves exact Group rows and saved draft', true,
    { groupRevisionId: cohort.revisionId, selectionRevisionId: selection.id });

  const secondProposal = await openAddColumns();
  assert.deepEqual(secondProposal.proposalGrid.headers, firstProposal.proposalGrid.headers,
    'Reopening after Cancel must preserve the same native field and chooser preview.');
  const applied = await applyProposal();
  check('correctness', 'native RELATED_SOURCE PRESENCE renders exact false and true values for exact nonempty Groups', true,
    { operation: applied.step.operation, outputs: applied.grid.results });

  const reload = async (label, outputLabel) => {
    const tableTab = page.getByTestId(`construction-table-${outputId}`);
    await action(label, tableTab, async () => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
      await tableTab.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      if (await tableTab.getAttribute('aria-current') !== 'page') await tableTab.click({ timeout: ACTION_TIMEOUT_MS });
    }, { timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS, after: async () => {
      await waitForPreview(groups.length);
      if (outputLabel) await assertRows(label, outputLabel);
      else {
        const restoredGrid = await visibleGrid(page);
        const labelIndex = restoredGrid.headers.findIndex(header => header.toLowerCase() === 'group label');
        const ordinalIndex = restoredGrid.headers.findIndex(header => header.toLowerCase() === 'group ordinal');
        const memberIndex = restoredGrid.headers.findIndex(header => header.toLowerCase() === 'members');
        assert(labelIndex >= 0 && ordinalIndex >= 0 && memberIndex >= 0, `${label}: restored Group-only preview omitted identities or members.`);
        assert.deepEqual(restoredGrid.rows.map(row => row[labelIndex]).sort(), groups.map(group => group.label).sort());
        for (const group of groups) {
          const row = restoredGrid.rows.find(values => values[labelIndex] === group.label);
          assert.equal(row[ordinalIndex], String(group.ordinal), `${label}: restored ${group.label} changed its ordinal.`);
          assert(row[memberIndex].includes(group.memberIDs[0]), `${label}: restored ${group.label} lost ${group.memberIDs[0]}.`);
        }
      }
    } });
    builder = await readJSON(`${rootPath}/builder`);
    document = documentFor(builder);
    return document.construction.steps.find(step => step.operation.kind === 'RELATED_SOURCE');
  };
  let reloadedStep = await reload('reload applied false/true PRESENCE rows', applied.output.label);
  assert.equal(reloadedStep?.id, applied.step.id);
  assert.deepEqual(reloadedStep.operation, applied.step.operation);
  check('persistence', 'saved Boolean PRESENCE operation and exact false/true rows survive reload', true,
    { stepId: reloadedStep.id, outputId: reloadedStep.operation.relatedSource.outputColumnId });

  const savedHistoryStep = page.getByTestId(`construction-history-step-${reloadedStep.id}`);
  await click('select saved PRESENCE operation for edit', savedHistoryStep,
    () => waitForHistorySelection(reloadedStep.id, 'edit'));
  const editSaved = page.getByTestId(`construction-edit-step-${reloadedStep.id}`);
  await click('open saved PRESENCE operation editor', editSaved,
    () => waitVisible(page.locator('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]')));
  const labelInput = page.locator('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]');
  const editBeforeCancel = structuredClone(builder.workspace);
  await fill('propose saved PRESENCE label edit', labelInput, editedPresenceLabel, async () => {
    await waitForPreview(groups.length, true);
    await waitVisible(page.getByTestId('construction-proposal-ready'));
    await assertRows('saved PRESENCE label edit proposal', editedPresenceLabel, true);
  });
  await click('Cancel saved PRESENCE label edit', page.getByTestId('construction-cancel-proposal'), async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'));
    const after = await readJSON(`${rootPath}/builder`);
    assert.deepEqual(after.workspace, editBeforeCancel, 'Canceling a saved label edit must preserve the exact operation.');
    await assertRows('saved false/true values after label-edit Cancel', applied.output.label);
  });
  check('persistence', 'saved PRESENCE edit Cancel preserves the exact Boolean operation', true, { stepId: reloadedStep.id });

  await click('reselect saved PRESENCE operation for label edit', savedHistoryStep,
    () => waitForHistorySelection(reloadedStep.id, 'edit'));
  await click('reopen saved PRESENCE editor after Cancel', editSaved,
    () => waitVisible(page.locator('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]')));
  await fill('set reviewed label on saved PRESENCE output', labelInput, editedPresenceLabel, async () => {
    await waitForPreview(groups.length, true);
    await waitVisible(page.getByTestId('construction-proposal-ready'));
    await assertRows('confirmed PRESENCE label edit proposal', editedPresenceLabel, true);
  });
  await click('Apply saved PRESENCE label edit', page.getByTestId('construction-apply-proposal'), async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'));
    await waitForPreview(groups.length);
  });
  builder = await readJSON(`${rootPath}/builder`);
  document = documentFor(builder);
  const editedStep = document.construction.steps.find(step => step.id === reloadedStep.id);
  assert(editedStep, 'Applying the label edit must preserve the saved operation identity.');
  assert.deepEqual(editedStep.operation, reloadedStep.operation, 'A label edit must preserve exact route, source, form, and Group anchor.');
  assert.equal(editedStep.outputs.find(column => column.id === reloadedStep.operation.relatedSource.outputColumnId)?.label, editedPresenceLabel);
  await assertRows('applied saved PRESENCE label edit', editedPresenceLabel);
  reloadedStep = await reload('reload edited false/true PRESENCE rows', editedPresenceLabel);
  assert.equal(reloadedStep.id, editedStep.id);
  assert.deepEqual(reloadedStep.operation, editedStep.operation);
  check('persistence', 'edited Boolean PRESENCE label and exact false/true values survive reload', true,
    { stepId: reloadedStep.id, label: editedPresenceLabel });

  const beforeRemoval = structuredClone(builder.workspace);
  await click('select edited PRESENCE operation for removal', page.getByTestId(`construction-history-step-${reloadedStep.id}`),
    () => waitForHistorySelection(reloadedStep.id, 'remove'));
  const removeButton = page.getByTestId(`construction-remove-step-${reloadedStep.id}`);
  await click('propose removing saved PRESENCE operation', removeButton, async () => {
    await waitForPreview(groups.length, true);
    await waitVisible(page.getByTestId('construction-proposal-ready'));
    assert.equal((await visibleGrid(page, true)).headers.some(header => header.toLowerCase() === editedPresenceLabel.toLowerCase()), false,
      'Removal preview must restore Group-only columns.');
  });
  const removeGrid = await visibleGrid(page, true);
  assert(removeGrid.headers.some(header => header.toLowerCase() === 'members'));
  await click('Cancel removal of saved PRESENCE operation', page.getByTestId('construction-cancel-proposal'), async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'));
    assert.deepEqual((await readJSON(`${rootPath}/builder`)).workspace, beforeRemoval,
      'Canceling removal must retain the exact saved PRESENCE step.');
    await assertRows('PRESENCE rows after removal Cancel', editedPresenceLabel);
  });
  check('persistence', 'saved PRESENCE removal Cancel preserves exact operation and values', true,
    { stepId: reloadedStep.id });

  await click('reselect saved PRESENCE operation for confirmed removal', page.getByTestId(`construction-history-step-${reloadedStep.id}`),
    () => waitForHistorySelection(reloadedStep.id, 'remove'));
  await click('reopen saved PRESENCE removal proposal', page.getByTestId(`construction-remove-step-${reloadedStep.id}`), async () => {
    await waitForPreview(groups.length, true);
    await waitVisible(page.getByTestId('construction-proposal-ready'));
  });
  const confirmedRemoval = await visibleGrid(page, true);
  assert.deepEqual(confirmedRemoval.rows.map(row => row[confirmedRemoval.headers.findIndex(header => header.toLowerCase() === 'group label')]).sort(),
    groups.map(group => group.label).sort(), 'Removal proposal must restore the exact declared Group rows.');
  await click('Apply saved PRESENCE removal and restore Group-only output', page.getByTestId('construction-apply-proposal'), async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'));
    await waitForPreview(groups.length);
  });
  builder = await readJSON(`${rootPath}/builder`);
  document = documentFor(builder);
  assert.equal(document.construction?.steps?.length ?? 0, 0, 'Applying removal must restore the Group-only construction.');
  assert.deepEqual(document.rows, groupedBaselineDocument.rows, 'Removing PRESENCE must restore the exact explicit Group row binding.');
  assert.equal(document.population.selectionRevisionId, selection.id);
  const restored = await visibleGrid(page);
  const restoredLabelIndex = restored.headers.findIndex(header => header.toLowerCase() === 'group label');
  const restoredOrdinalIndex = restored.headers.findIndex(header => header.toLowerCase() === 'group ordinal');
  assert.deepEqual(restored.rows.map(row => row[restoredLabelIndex]).sort(),
    groups.map(group => group.label).sort());
  for (const group of groups) {
    const row = restored.rows.find(values => values[restoredLabelIndex] === group.label);
    const memberText = row[restored.headers.findIndex(header => header.toLowerCase() === 'members')];
    assert.equal(row[restoredOrdinalIndex], String(group.ordinal));
    assert(memberText.includes(group.memberIDs[0]));
  }
  await reload('reload fully restored named Groups', undefined);
  builder = await readJSON(`${rootPath}/builder`);
  document = documentFor(builder);
  assert.equal(document.construction?.steps?.length ?? 0, 0);
  assert.equal(document.rows.groups.source.explicit.revisionId, cohort.revisionId);
  assert.equal(document.population.selectionRevisionId, selection.id);
  check('persistence', 'removal Apply and final reload restore exact explicit Group identities and members', true,
    { groupRevisionId: cohort.revisionId, selectionRevisionId: selection.id, rows: restored.rows });
  assert.deepEqual(report.errors, [], 'The native lifecycle must have no unexpected UI or API errors.');
  report.target.lifecycle = {
    sourceIdentity: { project: selectionProject, storageProject: project, generation: target.fixtureGeneration, resourceType: 'Specimen' },
    sourceIDs: sourceRefs.map(ref => ref.id).sort(),
    groupRevisionId: cohort.revisionId,
    expectedPresence: groups.map(({ groupId, memberIDs, observationIDs, presence }) => ({ groupId, memberIDs, observationIDs, presence })),
    restoredAfterRemoval: true,
    elapsedMs: Math.round(performance.now() - lifecycleStartedAt),
  };
};
