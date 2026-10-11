import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { buildBasicNestedCodingExpandOracle } from '../helpers/basic-nested-coding-expand-oracle.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const sourceIDPath = 'id';
const ownerPath = 'component[].valueString';
const nestedCodesPath = 'component[].code.coding[].code';
const expandedItemName = 'expanded_code';
const expandedItemLabel = 'Expanded code';
const positionName = 'expanded_code_ordinal';
const positionLabel = 'Code ordinal';
const editedItemLabel = 'Reviewed expanded code';
const previewSelector = '[data-testid="preview-table-scroll"] [role="table"]';

const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const expectedVisible = value => {
  if (value === null || value === undefined) return '—';
  if (Array.isArray(value)) {
    const items = value.map(expectedVisible).filter(item => item !== '—');
    return items.length ? items.join('; ') : '—';
  }
  return normalize(value);
};
const rowKey = row => JSON.stringify([row.id, row.ordinal === null ? -1 : row.ordinal]);
const observeResponse = responsePromise => responsePromise.then(
  entry => ({ entry }), error => ({ error }),
);

const readFixture = fixtureDir => {
  const path = join(fixtureDir, 'Observation.ndjson');
  const stat = statSync(path);
  assert(stat.size > 0 && stat.size <= 8_192, 'Basic nested-coding fixture must remain a small, bounded file.');
  const bytes = readFileSync(path);
  const resources = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  return {
    path,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    oracle: buildBasicNestedCodingExpandOracle(resources),
  };
};

const typedPreview = payload => payload?.preview ?? payload;
const assertTypedExpandedRows = (payload, { sourceIDColumn, ownerColumn, itemColumn, ordinalColumn, oracle, phase }) => {
  const preview = typedPreview(payload);
  assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows),
    `${phase}: native preview response omitted typed columns or rows.`);
  assert.equal(preview.previewStatus ?? payload.previewStatus, 'READY', `${phase}: native proposal preview is not ready.`);
  assert.equal(preview.rowCount, oracle.expectedRows.length, `${phase}: typed preview row count differs from the literal source oracle.`);
  const columnFor = label => preview.columns.find(column => column.label === label)?.column;
  const sourceIDKey = columnFor(sourceIDColumn.label);
  const ownerKey = columnFor(ownerColumn.label);
  const itemKey = columnFor(itemColumn.label);
  const ordinalKey = ordinalColumn ? columnFor(ordinalColumn.label) : undefined;
  assert(sourceIDKey && ownerKey && itemKey, `${phase}: typed response omitted a required native output column.`);
  if (ordinalColumn) assert(ordinalKey, `${phase}: typed response omitted the authored ordinal output.`);
  assert.equal(preview.rows.length, oracle.expectedRows.length,
    `${phase}: row multiplicity differs from the literal source oracle.`);

  const actual = preview.rows.map(row => {
    const id = row[sourceIDKey];
    const owner = row[ownerKey];
    const item = row[itemKey];
    const ordinal = ordinalKey ? row[ordinalKey] : null;
    const scalar = value => Array.isArray(value)
      ? (value.length === 0 ? null : (assert.equal(value.length, 1), value[0]))
      : value;
    return { id, ownerLabel: scalar(owner), ordinal: scalar(ordinal), code: scalar(item) };
  }).sort((left, right) => rowKey(left).localeCompare(rowKey(right)));
  const expected = oracle.expectedRows.map(({ id, ownerLabel, ordinal, code }) => ({ id, ownerLabel, ordinal, code }))
    .sort((left, right) => rowKey(left).localeCompare(rowKey(right)));
  assert.deepEqual(actual, expected, `${phase}: exact nested code, owner, ordinal, or preserved-parent value differs.`);
  const actualPopulated = actual.filter(row => row.code !== null);
  assert.equal(actualPopulated.length, 3, `${phase}: nested coding multiplicity must remain 2 + 1.`);
  assert.equal(new Set(actual.map(rowKey)).size, actual.length, `${phase}: expanded row identities must be unique by source and ordinal.`);
  return { rowCount: actual.length, rows: actual };
};

const readVisibleGrid = async (page, proposal) => page.evaluate(({ previewSelector, proposal }) => {
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const proposalPreview = document.querySelector('[data-testid="construction-proposal-preview"]');
  const table = proposal && proposalPreview
    ? proposalPreview.querySelector('table')
    : document.querySelector(previewSelector);
  if (!table) return { headers: [], rows: [], rowCount: null };
  if (proposal && proposalPreview) {
    const headers = [...table.querySelectorAll('thead th')]
      .map(cell => normalize(cell.querySelector('span')?.innerText ?? cell.innerText));
    const rows = [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => normalize(cell.innerText)));
    return { headers, rows, rowCount: rows.length };
  }
  const rows = [...table.querySelectorAll('[role="row"]')];
  return {
    headers: [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
      .map(cell => normalize(cell.innerText)),
    rows: rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')]
      .map(cell => normalize(cell.innerText))),
    rowCount: rows.length - 1,
  };
}, { previewSelector, proposal });

const indexFor = (headers, label, phase) => {
  const index = headers.findIndex(header => normalize(header).toLowerCase() === normalize(label).toLowerCase());
  assert(index >= 0, `${phase}: rendered table omitted ${label}; headers=${JSON.stringify(headers)}`);
  return index;
};

const assertVisibleExpandedRows = async (page, expectedRows, labels, phase, proposal = false) => {
  const visible = await readVisibleGrid(page, proposal);
  assert.equal(visible.rowCount, expectedRows.length, `${phase}: rendered row count differs from the literal oracle.`);
  const idIndex = indexFor(visible.headers, labels.id, phase);
  const ownerIndex = indexFor(visible.headers, labels.owner, phase);
  const codeIndex = indexFor(visible.headers, labels.code, phase);
  const ordinalIndex = indexFor(visible.headers, labels.ordinal, phase);
  const actual = visible.rows.map(row => ({
    id: row[idIndex], ownerLabel: row[ownerIndex], code: row[codeIndex], ordinal: row[ordinalIndex],
  })).sort((left, right) => {
    const leftOrdinal = left.ordinal === '—' || left.ordinal === '' ? -1 : Number(left.ordinal);
    const rightOrdinal = right.ordinal === '—' || right.ordinal === '' ? -1 : Number(right.ordinal);
    return left.id.localeCompare(right.id) || leftOrdinal - rightOrdinal;
  });
  const expected = expectedRows.map(row => ({
    id: row.id,
    ownerLabel: expectedVisible(row.ownerLabel === null ? [] : [row.ownerLabel]),
    code: expectedVisible(row.code),
    ordinal: expectedVisible(row.ordinal),
  })).sort((left, right) => {
    const leftOrdinal = left.ordinal === '—' ? -1 : Number(left.ordinal);
    const rightOrdinal = right.ordinal === '—' ? -1 : Number(right.ordinal);
    return left.id.localeCompare(right.id) || leftOrdinal - rightOrdinal;
  });
  assert.deepEqual(actual, expected, `${phase}: rendered values differ from the literal source/owner/ordinal oracle.`);
  return visible;
};

const assertVisibleSourceRows = async (page, sourceRows, labels, phase, proposal = false) => {
  const visible = await readVisibleGrid(page, proposal);
  assert.equal(visible.rowCount, sourceRows.length, `${phase}: rendered source row count differs from the fixture.`);
  const idIndex = indexFor(visible.headers, labels.id, phase);
  const ownerIndex = indexFor(visible.headers, labels.owner, phase);
  const codesIndex = indexFor(visible.headers, labels.codes, phase);
  const actual = visible.rows.map(row => [row[idIndex], row[ownerIndex], row[codesIndex]])
    .sort((left, right) => left[0].localeCompare(right[0]));
  const expected = sourceRows.map(row => [
    row.id,
    expectedVisible(row.ownerLabel === null ? [] : [row.ownerLabel]),
    expectedVisible(row.codes),
  ]).sort((left, right) => left[0].localeCompare(right[0]));
  assert.deepEqual(actual, expected, `${phase}: exact source IDs, owner labels, and nested code arrays differ from the fixture.`);
  return visible;
};

export const basicNestedCodingExpandWorkflow = async ({ page, action, check, networkObserver, report, target, seed }, context = {}) => {
  configureNativePage(page);
  assert.equal(report.scenario, 'basic-nested-coding-expand');
  assert.equal(seed?.fresh, true, 'Nested coding must run in a fresh isolated Basic fixture project.');
  assert.equal(context.custom, false, 'Nested coding must use the registered Basic fixture, not an owned CDA target.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected the disposable Basic fixture project.');
  assert(target.fixtureGeneration && target.fixtureDir, 'The Basic fixture project, generation, and path must be explicit.');

  const { path: fixturePath, sha256, oracle } = readFixture(target.fixtureDir);
  report.target.fixtureOracle = {
    scope: 'Basic synthetic Observation fixture only; this does not provide a loaded CDA source witness.',
    fixturePath,
    sha256,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sourceRows: oracle.sourceRows,
    shapeCounts: oracle.shapeCounts,
    expectedExpandedRows: oracle.expectedRows,
  };
  check('correctness', 'literal Basic fixture proves populated nested coding multiplicity and exact component owners',
    oracle.expectedRows.length === 9 && oracle.expectedRows.filter(row => row.code !== null).length === 3 &&
      oracle.shapeCounts.missing === 1 && oracle.shapeCounts.null === 1 && oracle.shapeCounts.empty === 1,
    report.target.fixtureOracle);

  const doAction = (label, locator, perform, after, options = {}) => action(label, locator, perform, {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    ...(after ? { after } : {}),
    ...options,
  });
  const click = (label, locator, after) => doAction(label, locator,
    () => locator.click({ timeout: ACTION_TIMEOUT_MS }), after);
  const fill = (label, locator, value, after) => doAction(label, locator,
    () => locator.fill(value, { timeout: ACTION_TIMEOUT_MS }), after, { editable: true });
  const select = (label, locator, value, after) => doAction(label, locator,
    () => locator.selectOption(value, { timeout: ACTION_TIMEOUT_MS }), after);
  const remainingActionMs = startedAt => Math.max(1, ACTION_TIMEOUT_MS - (performance.now() - startedAt));
  const waitVisible = (locator, timeout = ACTION_TIMEOUT_MS) => locator.waitFor({ state: 'visible', timeout });
  const waitHidden = (locator, timeout = ACTION_TIMEOUT_MS) => locator.waitFor({ state: 'hidden', timeout });
  const retryVisibleRows = async (assertRows, startedAt, phase) => {
    const deadline = startedAt + ACTION_TIMEOUT_MS;
    let lastError;
    while (performance.now() < deadline) {
      try {
        return await assertRows();
      } catch (error) {
        lastError = error;
      }
      const remaining = deadline - performance.now();
      if (remaining <= 1) break;
      await page.waitForTimeout(Math.min(40, remaining));
    }
    throw new Error(`${phase}: exact literal rows did not render within the five-second action budget: ${lastError?.message ?? lastError}`);
  };
  const waitForPreview = (rows, proposal = false, proposalId, timeout = ACTION_TIMEOUT_MS) => page.waitForFunction(({ rows, proposal, proposalId }) => {
    if (proposal) {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      const count = preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length;
      return panel?.dataset.proposalStatus === 'ready' && count === rows &&
        (proposalId === undefined || panel.dataset.proposalId === proposalId);
    }
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return preview?.dataset.previewStatus === 'ready' &&
      table?.getAttribute('aria-rowcount') === String(rows + 1) &&
      !document.body.innerText.includes('Loading your table…') &&
      !document.body.innerText.includes('Preview failed:');
  }, { rows, proposal, proposalId }, { timeout });

  const project = target.fixtureProject;
  const explorerPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  await page.goto(browserURL(target, project, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  await page.waitForFunction(() => document.body.innerText.includes('Build your first table') ||
    document.body.innerText.includes('Dataset graph'), undefined, { timeout: ACTION_TIMEOUT_MS });
  const newExplorer = page.locator('summary').filter({ hasText: /^New explorer$/ });
  await click('open new Basic Explorer creation', newExplorer,
    () => waitVisible(page.locator('#new-explorer-name')));
  const explorerTitle = `Verify ${project.slice(-10)} nested coding EXPAND`;
  await fill('name the disposable nested-coding Explorer', page.locator('#new-explorer-name'), explorerTitle);
  const createdExplorerResponse = observeResponse(networkObserver.waitForResponse(entry =>
    entry.method === 'POST' && entry.path === explorerPath && entry.request?.title === explorerTitle,
  { fromIndex: networkObserver.requests.length, timeoutMs: ACTION_TIMEOUT_MS }));
  const createExplorerStartedAt = performance.now();
  await click('create the fresh nested-coding Explorer', page.getByRole('button', { name: 'Create blank', exact: true }), async () => {
    const result = await createdExplorerResponse;
    if (result.error) throw result.error;
    const entry = result.entry;
    assert(entry.status >= 200 && entry.status < 300, `Native Explorer creation must succeed, got HTTP ${entry.status}.`);
    await page.waitForFunction(title => document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === title &&
      document.body.innerText.includes('Build your first table'), explorerTitle,
    { timeout: remainingActionMs(createExplorerStartedAt) });
  });
  const explorer = await page.locator('select[aria-label="Explorer"]').inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The case must use its newly created Explorer.');
  report.target.explorer = explorer;

  const firstTableTitle = 'Basic nested coding source';
  await fill('name the Observation source table', page.locator('#first-table-name'), firstTableTitle);
  await click('create the native Observation root table', page.getByRole('button', { name: 'Choose Observation rows', exact: true }), async () => {
    await page.waitForFunction(title => document.body.innerText.includes('DATASET WORKSPACE') &&
      document.body.innerText.includes(title) && !document.body.innerText.includes('Build your first table'),
    firstTableTitle, { timeout: ACTION_TIMEOUT_MS });
    await page.getByTestId('construction-action-add-columns').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  });

  const authoringBase = `${explorerPath}/${encodeURIComponent(explorer)}/authoring/v2`;
  const readBuilder = async (timeout = ACTION_TIMEOUT_MS) => {
    const response = await page.context().request.get(`${target.apiUrl}${authoringBase}/builder`, { timeout });
    const body = await response.json();
    assert(response.ok(), `Native Builder read returned HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 700)}`);
    assert.equal(body.catalog?.generation, target.fixtureGeneration);
    return body;
  };
  const documentFor = builder => builder.workspace.documents.find(document => document.rootResourceType === 'Observation');
  let builder = await readBuilder();
  let document = documentFor(builder);
  assert(document?.output?.id, 'Native Observation root table must be saved in the new Explorer.');
  const outputId = document.output.id;
  report.target.outputId = outputId;
  assert.equal(document.rows?.kind, 'RECORDS', 'The source table must begin with native Observation RECORDS.');
  const originalRows = structuredClone(document.rows);
  check('correctness', 'native Observation table starts with an exact RECORDS source and stable ID column',
    document.columns.some(column => column.source?.field?.path === sourceIDPath && column.source?.field?.projectionMode === 'VALUE'),
    { outputId, rows: originalRows, columns: document.columns });

  const rawFieldsDetails = page.getByTestId('feature-catalog-raw-fields');
  const addColumnsEditor = page.locator('[aria-label="Add columns editor"]');
  await click('open native Add columns for nested source fields', page.getByTestId('construction-action-add-columns'),
    () => waitVisible(addColumnsEditor));
  const fieldsTab = page.locator('[aria-label="Column types"]')
    .getByRole('button', { name: 'Fields and related data', exact: true });
  if (await fieldsTab.getAttribute('aria-pressed') !== 'true') {
    await click('choose native Fields and related data', fieldsTab,
      () => waitVisible(page.getByTestId('construction-add-columns-source')));
  }
  assert.equal(await rawFieldsDetails.count(), 1, 'The native source chooser must expose raw FHIR fields.');
  if (!await rawFieldsDetails.evaluate(element => element.open)) {
    await click('open the raw Observation fields', rawFieldsDetails.locator(':scope > summary'),
      () => page.waitForFunction(() => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true,
        undefined, { timeout: ACTION_TIMEOUT_MS }));
  }
  for (const path of [ownerPath, nestedCodesPath]) {
    const field = page.getByRole('checkbox', { name: `Select Observation.${path}`, exact: true });
    await waitVisible(field);
    await doAction(`select native Observation.${path}`, field,
      () => field.check({ timeout: ACTION_TIMEOUT_MS }),
      () => page.waitForFunction(expected => document.querySelector(`input[aria-label=${JSON.stringify(`Select Observation.${expected}`)}]`)?.checked === true,
        path, { timeout: ACTION_TIMEOUT_MS }));
  }
  const addFields = page.getByRole('button', { name: 'Add 2 selected features', exact: true });
  let addFieldsEntry;
  const addFieldsStartedAt = performance.now();
  const choiceProposalPanel = page.getByTestId('construction-choice-proposal-panel');
  await click('propose the two native nested source fields', addFields,
    async () => {
      const choiceDialog = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
      await waitVisible(choiceDialog, remainingActionMs(addFieldsStartedAt));
      const ownerAll = choiceDialog.getByRole('radio', {
        name: 'Component Value String: Keep all matching values', exact: true,
      });
      const nestedCodesAll = choiceDialog.getByRole('radio', {
        name: 'Component Code Coding Code: Keep all matching values', exact: true,
      });
      assert.equal(await ownerAll.isChecked(), true,
        'The native owner-label choice must preserve every matching component value.');
      assert.equal(await nestedCodesAll.isChecked(), true,
        'The native coding choice must preserve every matching nested code.');

      const addFieldsIndex = networkObserver.requests.length;
      const addFieldsResponse = observeResponse(networkObserver.waitForResponse(entry => {
        if (entry.index < addFieldsIndex || entry.method !== 'POST' ||
          !entry.path.endsWith('/construction-choice-proposals')) return false;
        const request = entry.request;
        const choices = request?.constructionChoices;
        return request?.outputId === outputId &&
          request.expectedDraftVersion === builder.draftVersion &&
          request.expectedDraftDigest === builder.draftDigest &&
          Array.isArray(choices) && choices.length === 2 &&
          choices.every(choice => choice.form === 'ALL' && typeof choice.choiceId === 'string') &&
          new Set(choices.map(choice => choice.choiceId)).size === 2;
      }, { fromIndex: addFieldsIndex, timeoutMs: remainingActionMs(addFieldsStartedAt) }));
      await choiceDialog.getByRole('button', { name: 'Add 2 columns', exact: true })
        .click({ timeout: remainingActionMs(addFieldsStartedAt) });
      const [result] = await Promise.all([
        addFieldsResponse,
        waitVisible(choiceProposalPanel, remainingActionMs(addFieldsStartedAt)),
      ]);
      if (result.error) throw result.error;
      addFieldsEntry = result.entry;
      assert.equal(addFieldsEntry.status, 200, `Native source-field proposal returned HTTP ${addFieldsEntry.status}.`);
      assert.equal(addFieldsEntry.response?.commandId, addFieldsEntry.request?.commandId,
        'The native proposal response must match the exact choice-confirmation command.');
      assert.equal(addFieldsEntry.response?.outputId, outputId,
        'The native source-field proposal must remain bound to the new Observation table.');
      assert.equal(addFieldsEntry.response?.previewStatus, 'READY',
        'The native source-field proposal must return a ready preview.');
      assert.deepEqual(addFieldsEntry.response?.constructionChoices?.map(choice => choice.form).sort(), ['ALL', 'ALL'],
        'The native proposal must preserve all matching owner and nested code values.');
      await page.waitForFunction(() =>
        document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus === 'ready',
      undefined, { timeout: remainingActionMs(addFieldsStartedAt) });
    });
  const applyFields = page.getByTestId('construction-choice-proposal-panel')
    .getByRole('button', { name: 'Apply columns', exact: true });
  let sourceIDColumn;
  let ownerColumn;
  let nestedCodesColumn;
  let sourceColumns;
  let sourceLabels;
  let sourceGrid;
  const applyFieldsStartedAt = performance.now();
  await click('apply native owner-label and nested-code fields', applyFields, async () => {
    await waitHidden(page.getByTestId('construction-choice-proposal-panel'), remainingActionMs(applyFieldsStartedAt));
    await waitForPreview(oracle.sourceRows.length, false, undefined, remainingActionMs(applyFieldsStartedAt));
    builder = await readBuilder(remainingActionMs(applyFieldsStartedAt));
    document = documentFor(builder);
    sourceIDColumn = document.columns.find(column => column.source?.field?.path === sourceIDPath && column.source.field.projectionMode === 'VALUE');
    ownerColumn = document.columns.find(column => column.source?.field?.path === ownerPath && column.source.field.projectionMode === 'ALL');
    nestedCodesColumn = document.columns.find(column => column.source?.field?.path === nestedCodesPath && column.source.field.projectionMode === 'ALL');
    assert(sourceIDColumn && ownerColumn && nestedCodesColumn,
      `Native fields must save the exact Observation ID, owner, and nested coding paths: ${JSON.stringify(document.columns)}`);
    assert.equal(document.rows.kind, 'RECORDS');
    assert.deepEqual(document.rows, originalRows, 'Adding projected fields must preserve source RECORDS rows.');
    sourceColumns = structuredClone(document.columns);
    sourceLabels = { id: sourceIDColumn.label, owner: ownerColumn.label, codes: nestedCodesColumn.label };
    sourceGrid = await retryVisibleRows(() => assertVisibleSourceRows(page, oracle.sourceRows, sourceLabels,
      'Initial nested-code source Preview'), applyFieldsStartedAt, 'Initial nested-code source Preview');
  });
  report.target.columns = {
    sourceID: { id: sourceIDColumn.columnId, physical: sourceIDColumn.column, label: sourceIDColumn.label, path: sourceIDPath },
    owner: { id: ownerColumn.columnId, physical: ownerColumn.column, label: ownerColumn.label, path: ownerPath, projectionMode: 'ALL' },
    nestedCodes: { id: nestedCodesColumn.columnId, physical: nestedCodesColumn.column, label: nestedCodesColumn.label, path: nestedCodesPath, projectionMode: 'ALL' },
  };
  check('correctness', 'native source Preview preserves every literal owner and nested coding list', true,
    { sourceGrid, expected: oracle.sourceRows });

  await click('close native Add columns before configuring row shape',
    page.getByTestId('construction-close-operation-editor'),
    () => waitVisible(page.getByTestId('construction-rows-settings-trigger')));

  const waitForExpandActionState = async (settings, startedAt) => {
    const expand = settings.getByTestId('construction-action-expand-rows');
    try {
      await waitVisible(expand, remainingActionMs(startedAt));
      await page.waitForFunction(() => {
        const button = document.querySelector('[data-testid="construction-action-expand-rows"]');
        return button instanceof HTMLButtonElement && !button.disabled;
      }, undefined, { timeout: remainingActionMs(startedAt) });
    } catch (error) {
      const expectedState = {
        snapshotToken: builder.catalog?.snapshotToken,
        draftVersion: builder.draftVersion,
        draftDigest: builder.draftDigest,
        outputId,
      };
      const currentDraft = entry => entry.request?.snapshotToken === expectedState.snapshotToken &&
        entry.request?.outputId === outputId &&
        (entry.request?.expectedDraftVersion ?? entry.request?.draftVersion) === expectedState.draftVersion &&
        (entry.request?.expectedDraftDigest ?? entry.request?.draftDigest) === expectedState.draftDigest;
      const summarize = entry => ({
        index: entry.index,
        requestId: entry.requestId,
        status: entry.status ?? (entry.failure ? 'failed' : 'pending'),
        failure: entry.failure,
        requestStageId: entry.request?.stageId,
        responseStageId: entry.response?.selectedStage?.id ?? entry.response?.stageId,
        expandCapability: entry.response?.selectedStage?.capabilities?.find(capability => capability.kind === 'EXPAND'),
      });
      const latestFor = suffix => networkObserver.requests
        .filter(entry => entry.method === 'POST' && entry.path.endsWith(suffix) && currentDraft(entry))
        .at(-1);
      const nativeState = await expand.evaluate(button => ({
        disabled: button.disabled,
        reason: button.querySelector(':scope > span:nth-child(3)')?.textContent?.trim() ?? '',
      })).catch(() => ({ disabled: true, reason: 'Expand control is no longer attached.' }));
      const latestReconciliation = latestFor('/reconcile');
      const latestCapabilities = latestFor('/construction-capabilities');
      const diagnostics = {
        expectedState,
        nativeState,
        latestReconciliation: latestReconciliation ? summarize(latestReconciliation) : null,
        latestCapabilities: latestCapabilities ? summarize(latestCapabilities) : null,
      };
      throw new Error(`Native EXPAND did not become enabled within the current five-second UI action budget; readiness records are diagnostic only: ${JSON.stringify(diagnostics)}; ${error?.message ?? error}`, { cause: error });
    }
    return expand;
  };
  const openRowsSettings = async label => {
    const settings = page.getByRole('dialog', { name: 'Row definition settings' });
    if (await settings.count() === 0) {
      const startedAt = performance.now();
      await click(`${label}: open Configure rows`, page.getByTestId('construction-rows-settings-trigger'),
        async () => {
          await waitVisible(settings, remainingActionMs(startedAt));
          await waitForExpandActionState(settings, startedAt);
        });
    }
    return settings;
  };
  const openExpandEditor = async label => {
    const settings = await openRowsSettings(label);
    const expand = settings.getByTestId('construction-action-expand-rows');
    await waitVisible(expand);
    const disabledReason = await expand.evaluate(button =>
      button.querySelector(':scope > span:nth-child(3)')?.textContent?.trim() ?? '');
    assert.equal(await expand.isEnabled(), true,
      `Native source rows must expose the direct EXPAND action.${disabledReason ? ` Capability says: ${disabledReason}` : ''}`);
    await click(`${label}: select Make one row per list value`, expand,
      () => waitVisible(page.locator('[data-testid="construction-reshape-expand"]')));
  };
  const waitForExpandResponse = (fromIndex, expected) => networkObserver.waitForResponse(entry => {
    if (entry.index < fromIndex || entry.method !== 'POST' || !entry.path.endsWith('/construction-proposals')) return false;
    const request = entry.request;
    const step = request?.candidateConstruction?.steps?.findLast(candidate => candidate.operation?.kind === 'EXPAND');
    const expand = step?.operation?.expand;
    const item = step?.outputs?.find(column => column.id === expand?.outputColumnId);
    const ordinal = step?.outputs?.find(column => column.id === expand?.ordinalColumnId);
    return request?.outputId === outputId &&
      (!expected.stepId || step?.id === expected.stepId) &&
      expand?.inputColumnId === expected.inputColumnId &&
      expand?.emptyPolicy === 'PRESERVE_PARENT' &&
      Boolean(expand?.ordinalColumnId) &&
      item?.name === expandedItemName && item.label === expected.itemLabel &&
      ordinal?.name === positionName && ordinal.label === positionLabel &&
      (!expected.outputColumnId || item.id === expected.outputColumnId) &&
      (!expected.ordinalColumnId || ordinal.id === expected.ordinalColumnId);
  }, { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
  const waitForFinalExpandProposal = async (responseOutcome, startedAt) => {
    const result = await responseOutcome;
    if (result.error) throw result.error;
    const entry = result.entry;
    assert.equal(entry.status, 200, `Native EXPAND proposal returned HTTP ${entry.status ?? 'without a response'}.`);
    assert.equal(entry.response?.previewStatus, 'READY',
      `The final native EXPAND proposal must be ready: ${JSON.stringify(entry.response).slice(0, 1200)}`);
    assert(entry.response?.proposalId, 'The final native EXPAND response must identify its proposal receipt.');
    const remaining = remainingActionMs(startedAt);
    await waitForPreview(oracle.expectedRows.length, true, entry.response.proposalId, remaining);
    return entry;
  };
  const setExpandFields = async label => {
    const fieldSelect = page.getByRole('combobox', { name: 'Repeated field', exact: true });
    await waitVisible(fieldSelect);
    const options = await fieldSelect.locator('option').evaluateAll(nodes => nodes.map(option => ({
      value: option.value, label: option.textContent?.trim() ?? '', disabled: option.disabled,
    })));
    const choices = options.filter(option => !option.disabled && normalize(option.label).includes(normalize(nestedCodesColumn.label)));
    assert.equal(choices.length, 1,
      `Native EXPAND must expose exactly the projected nested coding list: ${JSON.stringify(options)}`);
    await select(`bind native EXPAND to ${nestedCodesPath}`, fieldSelect, choices[0].value,
      async () => assert.equal(await fieldSelect.inputValue(), choices[0].value));
    const advanced = page.getByTestId('construction-reshape-expand-advanced');
    if (!await advanced.evaluate(element => element.open)) {
      await click('open native EXPAND output and policy options', advanced.locator(':scope > summary'),
        () => page.waitForFunction(() => document.querySelector('[data-testid="construction-reshape-expand-advanced"]')?.open === true,
          undefined, { timeout: ACTION_TIMEOUT_MS }));
    }
    await fill('name the native EXPAND item output', page.getByRole('textbox', { name: 'Expanded item name', exact: true }), expandedItemName);
    await fill('label the native EXPAND item output', page.getByRole('textbox', { name: 'Expanded item label', exact: true }), label);
    const position = page.getByRole('checkbox', { name: 'Include item position', exact: true });
    if (!await position.isChecked()) {
      await doAction('include the native zero-based code position', position,
        () => position.check({ timeout: ACTION_TIMEOUT_MS }),
        () => page.waitForFunction(() => document.querySelector('input[aria-label="Include item position"]')?.checked === true,
          undefined, { timeout: ACTION_TIMEOUT_MS }));
    }
    await fill('name the native coding ordinal output', page.getByRole('textbox', { name: 'Position column name', exact: true }), positionName);
    await select('preserve source parents with absent nested coding values', page.getByRole('combobox', { name: 'Empty list policy', exact: true }),
      'PRESERVE_PARENT');
    const proposalStartedAt = performance.now();
    const responseOutcome = observeResponse(waitForExpandResponse(networkObserver.requests.length, {
      inputColumnId: choices[0].value,
      itemLabel: label,
    }));
    let response;
    let visible;
    await fill('finish the exact native EXPAND proposal with its ordinal label',
      page.getByRole('textbox', { name: 'Position column label', exact: true }), positionLabel,
      async () => {
        response = await waitForFinalExpandProposal(responseOutcome, proposalStartedAt);
        visible = await retryVisibleRows(() => assertVisibleExpandedRows(page, oracle.expectedRows, {
          id: sourceIDColumn.label, owner: ownerColumn.label, code: label, ordinal: positionLabel,
        }, `${label} native proposal`, true), proposalStartedAt, `${label} native proposal`);
      });
    return { inputValue: choices[0].value, response, visible };
  };
  const assertExpandedPreview = (label, payload, visible) => {
    const typed = assertTypedExpandedRows(payload, {
      sourceIDColumn, ownerColumn, itemColumn: { label }, ordinalColumn: { label: positionLabel }, oracle,
      phase: `${label} native proposal`,
    });
    assert.equal(visible?.rowCount, oracle.expectedRows.length,
      `${label} native proposal did not retain its final visible rows in the bounded action step.`);
    return { typed, visible };
  };

  await openExpandEditor('Cancel first native EXPAND proposal');
  const firstProposal = await setExpandFields(expandedItemLabel);
  const initialProposalBody = firstProposal.response.response;
  assertExpandedPreview(expandedItemLabel, initialProposalBody, firstProposal.visible);
  const savedBeforeCancel = await readBuilder();
  const savedDocumentBeforeCancel = documentFor(savedBeforeCancel);
  const cancelButton = page.getByTestId('construction-cancel-proposal');
  const cancelStartedAt = performance.now();
  await click('cancel the first native nested-code EXPAND proposal', cancelButton, async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'), remainingActionMs(cancelStartedAt));
    await waitForPreview(oracle.sourceRows.length, false, undefined, remainingActionMs(cancelStartedAt));
    await retryVisibleRows(() => assertVisibleSourceRows(page, oracle.sourceRows, sourceLabels,
      'Canceled nested-code EXPAND source Preview'), cancelStartedAt, 'Canceled nested-code EXPAND source Preview');
    const settings = page.getByRole('dialog', { name: 'Row definition settings' });
    if (await settings.isVisible()) await waitForExpandActionState(settings, cancelStartedAt);
  });
  builder = await readBuilder();
  document = documentFor(builder);
  assert.deepEqual(builder.workspace, savedBeforeCancel.workspace, 'EXPAND Cancel must preserve the exact saved Builder workspace.');
  assert.deepEqual(document.rows, originalRows);
  assert.deepEqual(document.columns, savedDocumentBeforeCancel.columns);
  assert.equal(document.construction?.steps?.length ?? 0, 0);
  check('correctness', 'EXPAND Cancel leaves exact source rows, columns, and saved construction unchanged', true,
    { draftDigest: builder.draftDigest, outputId, rows: document.rows, columns: document.columns });

  await openExpandEditor('Apply native nested-code EXPAND');
  const confirmed = await setExpandFields(expandedItemLabel);
  const confirmedPreview = assertExpandedPreview(expandedItemLabel, confirmed.response.response, confirmed.visible);
  const savedBeforeApply = await readBuilder();
  const applyButton = page.getByTestId('construction-apply-proposal');
  const applyStartedAt = performance.now();
  let appliedVisible;
  await click('apply native nested-code EXPAND', applyButton, async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'), remainingActionMs(applyStartedAt));
    await waitForPreview(oracle.expectedRows.length, false, undefined, remainingActionMs(applyStartedAt));
    appliedVisible = await retryVisibleRows(() => assertVisibleExpandedRows(page, oracle.expectedRows, {
      id: sourceIDColumn.label, owner: ownerColumn.label, code: expandedItemLabel, ordinal: positionLabel,
    }, 'Applied native nested-code EXPAND'), applyStartedAt, 'Applied native nested-code EXPAND');
  });
  builder = await readBuilder();
  document = documentFor(builder);
  const appliedStep = document.construction?.steps?.findLast(step => step.operation?.kind === 'EXPAND');
  assert(appliedStep, 'Native Apply must persist the authored EXPAND step.');
  assert.equal(document.rows.kind, 'RECORDS');
  assert.deepEqual(document.rows, originalRows, 'EXPAND must preserve the exact source row definition.');
  assert.deepEqual(document.columns, sourceColumns,
    'EXPAND must keep source projections stable while its new output stays in the construction step.');
  assert.equal(document.construction.steps.length, 1, 'Exactly one native EXPAND step must be saved.');
  assert.equal(appliedStep.operation.expand.inputColumnId, confirmed.inputValue);
  assert.equal(appliedStep.operation.expand.emptyPolicy, 'PRESERVE_PARENT');
  assert(appliedStep.operation.expand.ordinalColumnId, 'EXPAND must save the requested ordinal output.');
  const appliedOutput = appliedStep.outputs.find(column => column.id === appliedStep.operation.expand.outputColumnId);
  const savedOrdinal = appliedStep.outputs.find(column => column.id === appliedStep.operation.expand.ordinalColumnId);
  assert.equal(appliedOutput?.label, expandedItemLabel);
  assert.equal(savedOrdinal?.label, positionLabel);
  assert.equal(builder.draftVersion, Number(savedBeforeApply.draftVersion) + 1,
    'Apply must advance the saved Builder draft exactly once.');
  report.target.appliedExpand = {
    stepId: appliedStep.id,
    inputColumnId: appliedStep.operation.expand.inputColumnId,
    outputColumnId: appliedOutput.id,
    outputLabel: appliedOutput.label,
    ordinalColumnId: savedOrdinal.id,
    emptyPolicy: appliedStep.operation.expand.emptyPolicy,
    proposalRows: confirmedPreview.typed.rows,
    visibleRows: appliedVisible.rows,
    sourceRows: oracle.sourceRows.length,
  };
  check('correctness', 'EXPAND Apply saves exact nested codes, owner relationships, ordinals, and parent-preserved empty shapes', true,
    report.target.appliedExpand);

  const reloadAndAssertExpanded = async label => {
    const tab = page.getByTestId(`construction-table-${outputId}`);
    const reloadStartedAt = performance.now();
    await doAction(`${label}: reload exact expanded output`, tab, async () => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: remainingActionMs(reloadStartedAt) });
      await tab.waitFor({ state: 'visible', timeout: remainingActionMs(reloadStartedAt) });
      if (await tab.getAttribute('aria-current') !== 'page') await tab.click({ timeout: remainingActionMs(reloadStartedAt) });
    }, async () => {
      await waitForPreview(oracle.expectedRows.length, false, undefined, remainingActionMs(reloadStartedAt));
      const visibleCodeLabel = label.includes('edited') ? editedItemLabel : expandedItemLabel;
      await retryVisibleRows(() => assertVisibleExpandedRows(page, oracle.expectedRows, {
        id: sourceIDColumn.label, owner: ownerColumn.label, code: visibleCodeLabel, ordinal: positionLabel,
      }, label), reloadStartedAt, label);
    });
    const reloaded = await readBuilder();
    const reloadedDocument = documentFor(reloaded);
    const reloadedStep = reloadedDocument.construction.steps.findLast(step => step.operation?.kind === 'EXPAND');
    assert.equal(reloadedStep?.id, appliedStep.id, `${label}: saved EXPAND identity must survive reload.`);
    assert.equal(reloadedStep.operation.expand.outputColumnId, appliedStep.operation.expand.outputColumnId);
    assert.deepEqual(reloadedDocument.rows, originalRows);
    return reloadedDocument;
  };
  await reloadAndAssertExpanded('Reload applied nested-code EXPAND');
  check('persistence', 'applied nested-code values and exact owner/ordinal rows survive reload', true,
    { stepId: appliedStep.id, outputId, rowCount: oracle.expectedRows.length });

  const rowsSettings = await openRowsSettings('Edit saved native EXPAND');
  const editButton = rowsSettings.getByTestId(`construction-row-edit-${appliedStep.id}`);
  await click('open the saved native nested-code EXPAND for edit', editButton,
    () => waitVisible(page.locator('[data-testid="construction-reshape-expand"]')));
  const advanced = page.getByTestId('construction-reshape-expand-advanced');
  if (!await advanced.evaluate(element => element.open)) {
    await click('open saved EXPAND output settings', advanced.locator(':scope > summary'),
      () => page.waitForFunction(() => document.querySelector('[data-testid="construction-reshape-expand-advanced"]')?.open === true,
        undefined, { timeout: ACTION_TIMEOUT_MS }));
  }
  const editProposalStartedAt = performance.now();
  const editResponseOutcome = observeResponse(waitForExpandResponse(networkObserver.requests.length, {
    stepId: appliedStep.id,
    inputColumnId: appliedStep.operation.expand.inputColumnId,
    outputColumnId: appliedStep.operation.expand.outputColumnId,
    ordinalColumnId: appliedStep.operation.expand.ordinalColumnId,
    itemLabel: editedItemLabel,
  }));
  let editProposal;
  let editProposalVisible;
  await fill('edit the saved nested-code output label', page.getByRole('textbox', { name: 'Expanded item label', exact: true }), editedItemLabel,
    async () => {
      editProposal = await waitForFinalExpandProposal(editResponseOutcome, editProposalStartedAt);
      editProposalVisible = await retryVisibleRows(() => assertVisibleExpandedRows(page, oracle.expectedRows, {
        id: sourceIDColumn.label, owner: ownerColumn.label, code: editedItemLabel, ordinal: positionLabel,
      }, 'Edited native nested-code EXPAND proposal', true), editProposalStartedAt, 'Edited native nested-code EXPAND proposal');
    });
  assertExpandedPreview(editedItemLabel, editProposal.response, editProposalVisible);
  const applyEdit = page.getByTestId('construction-apply-proposal');
  const applyEditStartedAt = performance.now();
  let editedVisible;
  await click('apply the saved nested-code output label edit', applyEdit, async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'), remainingActionMs(applyEditStartedAt));
    await waitForPreview(oracle.expectedRows.length, false, undefined, remainingActionMs(applyEditStartedAt));
    editedVisible = await retryVisibleRows(() => assertVisibleExpandedRows(page, oracle.expectedRows, {
      id: sourceIDColumn.label, owner: ownerColumn.label, code: editedItemLabel, ordinal: positionLabel,
    }, 'Applied edited native nested-code EXPAND'), applyEditStartedAt, 'Applied edited native nested-code EXPAND');
  });
  builder = await readBuilder();
  document = documentFor(builder);
  const editedStep = document.construction.steps.findLast(step => step.operation?.kind === 'EXPAND');
  assert.equal(editedStep.id, appliedStep.id, 'Editing must retain the saved EXPAND identity.');
  assert.equal(editedStep.operation.expand.inputColumnId, appliedStep.operation.expand.inputColumnId);
  assert.equal(editedStep.operation.expand.outputColumnId, appliedStep.operation.expand.outputColumnId);
  assert.equal(editedStep.outputs.find(column => column.id === editedStep.operation.expand.outputColumnId)?.label, editedItemLabel);
  assert.deepEqual(document.rows, originalRows);
  assert.deepEqual(document.columns, sourceColumns);
  check('correctness', 'saved EXPAND edit keeps input and output identity with exact nested rows', true,
    { stepId: editedStep.id, inputColumnId: editedStep.operation.expand.inputColumnId,
      outputColumnId: editedStep.operation.expand.outputColumnId, label: editedItemLabel });
  await reloadAndAssertExpanded('Reload edited nested-code EXPAND');
  check('persistence', 'edited nested-code label and exact rows survive reload', true,
    { stepId: editedStep.id, label: editedItemLabel, rowCount: oracle.expectedRows.length });

  const removeSettings = await openRowsSettings('Remove saved native EXPAND');
  const removeButton = removeSettings.getByTestId(`construction-row-remove-${editedStep.id}`);
  const removalStartedAt = performance.now();
  let removalVisible;
  await click('propose removing the saved nested-code EXPAND', removeButton, async () => {
    await page.waitForFunction(count => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      return panel?.dataset.proposalStatus === 'ready' &&
        preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length === count &&
        Boolean(document.querySelector('[data-testid="construction-removal-summary"]'));
    }, oracle.sourceRows.length, { timeout: remainingActionMs(removalStartedAt) });
    removalVisible = await retryVisibleRows(() => assertVisibleSourceRows(page, oracle.sourceRows, sourceLabels,
      'Nested-code EXPAND removal proposal', true), removalStartedAt, 'Nested-code EXPAND removal proposal');
  });
  const removalApply = page.getByTestId('construction-apply-proposal');
  const removeApplyStartedAt = performance.now();
  let restoredVisible;
  await click('apply nested-code EXPAND removal and restore source rows', removalApply, async () => {
    await waitHidden(page.getByTestId('construction-proposal-panel'), remainingActionMs(removeApplyStartedAt));
    await waitForPreview(oracle.sourceRows.length, false, undefined, remainingActionMs(removeApplyStartedAt));
    restoredVisible = await retryVisibleRows(() => assertVisibleSourceRows(page, oracle.sourceRows, sourceLabels,
      'Restored source rows after EXPAND removal'), removeApplyStartedAt, 'Restored source rows after EXPAND removal');
  });
  builder = await readBuilder();
  document = documentFor(builder);
  assert.equal(document.construction?.steps?.length ?? 0, 0, 'Removing EXPAND must restore a construction with no row steps.');
  assert.deepEqual(document.rows, originalRows, 'Removing EXPAND must restore the exact original RECORDS definition.');
  assert.deepEqual(document.columns, sourceColumns,
    'Removing EXPAND must restore the exact original source-column bindings.');
  assert.deepEqual(restoredVisible.rows, removalVisible.rows,
    'Applying removal must restore the exact rows shown in the native removal proposal.');
  check('persistence', 'EXPAND removal proposal and Apply restore every exact source row and nested list', true,
    { outputId, restoredRows: oracle.sourceRows.length, removedStepId: editedStep.id, removalRows: removalVisible.rows });

  const finalTab = page.getByTestId(`construction-table-${outputId}`);
  const finalReloadStartedAt = performance.now();
  await doAction('reload the fully restored Basic nested-coding source', finalTab, async () => {
    await page.reload({ waitUntil: 'domcontentloaded', timeout: remainingActionMs(finalReloadStartedAt) });
    await finalTab.waitFor({ state: 'visible', timeout: remainingActionMs(finalReloadStartedAt) });
    if (await finalTab.getAttribute('aria-current') !== 'page') await finalTab.click({ timeout: remainingActionMs(finalReloadStartedAt) });
  }, async () => {
    await waitForPreview(oracle.sourceRows.length, false, undefined, remainingActionMs(finalReloadStartedAt));
    await retryVisibleRows(() => assertVisibleSourceRows(page, oracle.sourceRows, sourceLabels,
      'Final reloaded nested-coding source'), finalReloadStartedAt, 'Final reloaded nested-coding source');
  });
  builder = await readBuilder();
  document = documentFor(builder);
  assert.deepEqual(document.rows, originalRows);
  assert.deepEqual(document.columns, sourceColumns);
  assert.equal(document.construction?.steps?.length ?? 0, 0);
  check('persistence', 'final reload retains exact Basic Observation rows and literal nested source fields', true,
    { outputId, constructionSteps: document.construction?.steps?.length ?? 0, rowCount: oracle.sourceRows.length });
};
