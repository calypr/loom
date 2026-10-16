import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { browserURL } from './builder-url.mjs';
import { collectPreviewRows, previewHeaderIndex } from '../helpers/playwright-preview-rows.mjs';
import {
  loadBasicPivotUnpivotFixtureOracle,
  readBasicPivotUnpivotSeedManifest,
} from '../helpers/basic-pivot-unpivot-oracle.mjs';
import { pivotSourceSelectionReady } from './root-quantity-pivot-workflow.mjs';
import { fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const PROPOSAL_SELECTOR = '[data-testid="construction-proposal-panel"]';
const PREVIEW_SELECTOR = '[data-testid="construction-preview"]';

export const readOptionalProposalID = locator => locator.evaluateAll(panels =>
  panels[0]?.getAttribute('data-proposal-id') ?? null);

const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const sourcePath = column => column.source?.field?.path ?? column.source?.path ?? '';
const stableRows = rows => rows.map(row => JSON.stringify(row)).sort();
const categoryIdentity = key => key?.kind === 'MISSING' ? 'MISSING'
  : key?.kind === 'NULL' ? 'NULL'
    : key?.kind === 'STRING' ? key.string : undefined;

const waitFor = (page, predicate, value, timeout = ACTION_TIMEOUT_MS) =>
  page.waitForFunction(({ predicateSource, argument }) =>
    Boolean(Function('argument', `return (${predicateSource})(argument);`)(argument)),
  { predicateSource: predicate.toString(), argument: value }, { timeout: Math.min(timeout, ACTION_TIMEOUT_MS) });

const rowsForColumns = (preview, expectedRows, label) => {
  assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows),
    `${label}: native proposal must include typed preview columns and rows`);
  assert.equal(preview.rowCount, expectedRows.length, `${label}: typed row count differs from the literal fixture oracle`);
  assert.equal(preview.rows.length, expectedRows.length, `${label}: native preview omitted rows`);
  const columns = preview.columns.map(column => column.column);
  assert(columns.every(column => typeof column === 'string' && column.length > 0), `${label}: every preview column needs a stable name`);
  const actualRows = preview.rows.map(row => columns.map(column => row[column] ?? null));
  const expectedMatrices = expectedRows.map(row => columns.map(column => row[column] ?? null));
  assert.deepEqual(stableRows(actualRows), stableRows(expectedMatrices),
    `${label}: typed row values, identity tuples, or multiplicity differ from the literal fixture oracle`);
  return { columns, actualRows };
};

const outputForID = (step, id, label) => {
  const output = step.outputs.find(candidate => candidate.id === id);
  assert(output, `${label}: exact output ${id} is missing from the candidate step`);
  return output;
};

const nativePreview = async (page, outputId) => {
  await waitFor(page, argument => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === argument.outputId &&
      Boolean(preview.dataset.previewReceiptId);
  }, { outputId });
  const previewNode = page.getByTestId('construction-preview');
  const receiptId = await previewNode.getAttribute('data-preview-receipt-id');
  assert(receiptId, 'Saved preview must expose its exact receipt ID');
  return { receiptId, outputId };
};

export async function basicPivotUnpivotWorkflow({ page, report, action, check, networkObserver, target, seed }) {
  assert.equal(report.scenario, 'basic-pivot-unpivot');
  assert.equal(seed?.fresh, true, 'Pivot→Unpivot requires a fresh isolated Basic fixture project');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected the runner-owned Basic fixture project');
  assert(target.fixtureGeneration && target.fixtureDir, 'The fixture project and generation must be explicit');

  const manifest = readBasicPivotUnpivotSeedManifest(target.fixtureDir);
  const oracle = loadBasicPivotUnpivotFixtureOracle(target.fixtureDir);
  assert.deepEqual(manifest.map(({ name, recordCount }) => ({ name, recordCount })), [
    { name: 'Observation.ndjson', recordCount: 5 },
    { name: 'Patient.ndjson', recordCount: 2 },
  ]);
  check('correctness', 'Basic Pivot→Unpivot fixture preserves five exact Observation rows and two Patient group identities',
    oracle.sourceRows.length === 5 && oracle.patients.length === 2 &&
      oracle.categories.map(({ identity }) => identity).join('|') === 'MISSING|NULL|d',
    { manifest, sourceRows: oracle.sourceRows, categories: oracle.categories });

  const project = target.fixtureProject;
  const generation = target.fixtureGeneration;
  const apiOrigin = target.apiUrl.replace(/\/$/, '');
  const explorer = `basic-pivot-unpivot-${randomUUID()}`;
  const explorerCollection = fixtureProjectExplorerCollectionPath(project);
  report.target.explorer = explorer;
  report.target.outputId = null;
  const apiReads = [];
  const api = async (path, body) => {
    const response = await fetch(`${apiOrigin}${path}`, {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(30_000),
    });
    const value = await response.json();
    apiReads.push({ method: body === undefined ? 'GET' : 'POST', path, status: response.status });
    assert(response.ok, `Owned fixture Builder request ${path} returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 800)}`);
    return value;
  };
  const base = `${explorerCollection}/${encodeURIComponent(explorer)}/authoring/v2`;
  const readBuilder = () => api(`${base}/builder`);
  let builder;
  const command = async commands => {
    await api(`${base}/commands`, {
      commandId: randomUUID(),
      semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    });
    builder = await readBuilder();
  };

  await api(explorerCollection, { name: explorer, title: 'Basic Pivot to Unpivot lifecycle' });
  builder = await readBuilder();
  assert.equal(builder.catalog?.generation, generation, 'Fresh Builder catalog must use the exact fixture generation');
  const root = builder.catalog.nodes.find(node => node.resourceType === 'Observation');
  assert(root, 'Fresh fixture catalog must expose Observation as a root');
  const fieldPaths = ['id', 'subject.reference', 'valueQuantity.code', 'valueQuantity.value'];
  const fields = fieldPaths.map(fieldPath => {
    const candidate = builder.catalog.candidates.find(item => item.nodeId === root.nodeId && item.fieldPath === fieldPath);
    assert(candidate, `Fresh fixture catalog must expose exact Observation.${fieldPath}`);
    return { fieldPath, candidate };
  });
  await command([{ type: 'CREATE_TABLE', title: 'Pivot to Unpivot input', rootNodeId: root.nodeId }]);
  let document = builder.workspace.documents[0];
  assert(document?.output?.id, 'The exact Observation root table must have a saved output identity');
  const outputId = document.output.id;
  report.target.outputId = outputId;
  for (const { fieldPath, candidate } of fields) {
    await command([{
      type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: candidate.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: fieldPath === 'id' ? 'Observation ID' : `Observation ${fieldPath}`,
    }]);
  }
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  const prePivotWorkspace = structuredClone(builder.workspace);
  const prePivotColumns = structuredClone(document.columns);
  assert.deepEqual(prePivotColumns.map(sourcePath).sort(), [...fieldPaths].sort(),
    'The input table must contain exactly ID, subject reference, quantity code, and quantity value source bindings');

  const click = (label, locator, after) => action(label, locator, () => locator.click({ timeout: ACTION_TIMEOUT_MS }), {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    ...(after ? { after } : {}),
  });
  const select = (label, locator, value, after) => action(label, locator, () => locator.selectOption(value, { timeout: ACTION_TIMEOUT_MS }), {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    ...(after ? { after } : {}),
  });
  const waitVisible = locator => locator.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const waitProposalPanel = async () => waitFor(page, () => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]') ??
      document.querySelector('[data-testid="construction-choice-proposal-panel"]');
    return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  }, undefined);
  const proposal = async (fromIndex, label, paths = ['/construction-proposals', '/construction-choice-proposals']) => {
    await waitProposalPanel();
    const panel = page.locator('[data-testid="construction-proposal-panel"], [data-testid="construction-choice-proposal-panel"]');
    const proposalId = await panel.getAttribute('data-proposal-id');
    const entry = await networkObserver.waitForResponse(candidate => candidate.index >= fromIndex &&
      paths.some(path => candidate.path.endsWith(path)) && candidate.status !== undefined && candidate.response &&
      (!proposalId || candidate.response.proposalId === proposalId),
    { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    assert.equal(entry.status, 200, `${label}: native proposal must return HTTP 200`);
    assert.equal(entry.response?.outputId, outputId, `${label}: proposal must remain bound to the exact Observation output`);
    if (proposalId) assert.equal(entry.response?.proposalId, proposalId, `${label}: rendered panel must show the exact native proposal receipt`);
    return entry.response;
  };
  const startProposal = () => networkObserver.requests.length;
  const applyProposal = async (label, expectedRows) => {
    const panel = page.locator('[data-testid="construction-proposal-panel"], [data-testid="construction-choice-proposal-panel"]');
    const testId = await panel.getAttribute('data-testid');
    const apply = testId === 'construction-choice-proposal-panel'
      ? panel.getByRole('button', { name: 'Apply columns', exact: true })
      : page.getByTestId('construction-apply-proposal');
    await click(`${label}: Apply`, apply, async () => {
      await waitFor(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]') &&
        !document.querySelector('[data-testid="construction-choice-proposal-panel"]'), undefined);
      await waitFor(page, expected => {
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expected.outputId &&
          Boolean(preview.dataset.previewReceiptId);
      }, { outputId });
      builder = await readBuilder();
      assert(builder.draftDigest && builder.draftVersion !== undefined,
        `${label}: the applied draft must expose its exact saved identity`);
      document = builder.workspace.documents.find(item => item.output.id === outputId);
      assert(document, `${label}: the exact saved output must remain in the Builder draft`);
      await waitFor(page, expected => {
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return preview?.dataset.currentDraftDigest === expected.digest &&
          preview.dataset.currentDraftVersion === expected.version;
      }, { digest: builder.draftDigest, version: String(builder.draftVersion) });
      if (expectedRows) await readReceiptRows(label, expectedRows);
    });
    return document;
  };
  const assertVisible = async (expectedRows, label, columns) => {
    const visible = await collectPreviewRows(page, { timeout: ACTION_TIMEOUT_MS });
    assert.equal(visible.rowCount, expectedRows.length, `${label}: rendered preview row count differs from the literal oracle`);
    if (columns) {
      const expectedLabels = columns.map(column => column.label.toLowerCase());
      assert.deepEqual(visible.headers.map(header => normalize(header).toLowerCase()), expectedLabels,
        `${label}: rendered headers differ from the exact saved construction outputs`);
      const expectedCells = expectedRows.map(row => columns.map(column => row[column.name] == null ? '—' : String(row[column.name])));
      assert.deepEqual(stableRows(visible.rows.map(row => row.values)), stableRows(expectedCells),
        `${label}: rendered cells differ from the literal independent fixture oracle`);
    }
    return visible;
  };
  const assertVisibleProposal = async (proposalId, expectedRows, label, columns) => {
    await waitFor(page, expected => {
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      return preview?.dataset.previewStatus === 'ready' &&
        preview.dataset.previewReceiptId === expected.proposalId &&
        preview.dataset.previewOutputId === expected.outputId;
    }, { proposalId, outputId });
    const tables = await page.getByTestId('construction-proposal-preview').locator('table').evaluateAll(elements =>
      elements.map(table => ({
        headers: [...table.querySelectorAll('thead th')].map(cell => cell.innerText.trim().split('\n')[0]),
        rows: [...table.querySelectorAll('tbody tr')].map(row =>
          [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
      })));
    assert.equal(tables.length, 1, `${label}: proposal preview must render exactly one native table`);
    const [visible] = tables;
    assert.deepEqual(visible.headers.map(header => normalize(header).toLowerCase()),
      columns.map(column => normalize(column.label).toLowerCase()),
      `${label}: proposal preview headers differ from the exact output columns`);
    const expectedCells = expectedRows.map(row => columns.map(column =>
      row[column.name] == null ? '—' : String(row[column.name])));
    assert.deepEqual(stableRows(visible.rows), stableRows(expectedCells),
      `${label}: rendered proposal cells differ from the literal independent fixture oracle`);
  };
  const readReceiptRows = async (label, expectedRows) => {
    const receipt = await nativePreview(page, outputId);
    const preview = await api(`${base}/preview`, { receiptId: receipt.receiptId, outputId, limit: 100 });
    assert.equal(preview.receiptId, receipt.receiptId, `${label}: read-only preview must retain the exact saved receipt`);
    assert.equal(preview.outputId, outputId, `${label}: saved preview must retain the exact output identity`);
    rowsForColumns(preview, expectedRows, label);
    await assertVisible(expectedRows, label, preview.columns.map(column => ({ name: column.column, label: column.label })));
    return preview;
  };
  const reloadAndRead = async (label, expectedRows) => {
    const before = { digest: builder.draftDigest, version: String(builder.draftVersion) };
    assert(before.digest && before.version, `${label}: saved draft identity is required before reload`);
    const table = page.getByTestId(`construction-table-${outputId}`);
    let preview;
    await action(`reload Builder for ${label}`, table, async () => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
      await table.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      if (await table.getAttribute('aria-current') !== 'page') await table.click({ timeout: ACTION_TIMEOUT_MS });
    }, {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      after: async () => {
        await waitFor(page, expected => {
          const selected = document.querySelector(`[data-testid="construction-table-${expected.outputId}"][aria-current="page"]`);
          const preview = document.querySelector('[data-testid="construction-preview"]');
          return Boolean(selected && preview?.dataset.previewStatus === 'ready' &&
            preview.dataset.previewOutputId === expected.outputId &&
            preview.dataset.currentDraftDigest === expected.digest &&
            preview.dataset.currentDraftVersion === expected.version && preview.dataset.previewReceiptId);
        }, { outputId, ...before });
        builder = await readBuilder();
        assert.equal(builder.draftDigest, before.digest, `${label}: reloaded Builder must retain the exact saved draft digest`);
        assert.equal(String(builder.draftVersion), before.version, `${label}: reloaded Builder must retain the exact saved draft version`);
        document = builder.workspace.documents.find(item => item.output.id === outputId);
        assert(document, `${label}: the exact saved output must remain in the reloaded Builder draft`);
        preview = await readReceiptRows(label, expectedRows);
      },
    });
    return { builder, document, preview };
  };
  const awaitExactPanel = async label => {
    await waitProposalPanel();
    const status = await page.locator(PROPOSAL_SELECTOR).getAttribute('data-proposal-status');
    assert.equal(status, 'ready', `${label}: native proposal must be ready`);
  };
  const cancelProposal = async (label, savedWorkspace) => {
    await click(`${label}: Cancel`, page.getByTestId('construction-cancel-proposal'), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS });
      builder = await readBuilder();
      assert.deepEqual(builder.workspace, savedWorkspace, `${label}: Cancel must leave the exact saved workspace unchanged`);
    });
  };

  await page.goto(browserURL(target, project, explorer, 'builder'), { waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  const outputTab = page.getByTestId(`construction-table-${outputId}`);
  await waitVisible(outputTab);
  await click('select the exact Observation root output', outputTab, async () => {
    await waitFor(page, argument => document.querySelector(`[data-testid="construction-table-${argument.outputId}"][aria-current="page"]`), { outputId });
    await waitFor(page, expected => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      return preview?.dataset.currentDraftDigest === expected.digest &&
        preview.dataset.currentDraftVersion === expected.version;
    }, { digest: builder.draftDigest, version: String(builder.draftVersion) });
    const initialReceipt = await nativePreview(page, outputId);
    const initialPreview = await api(`${base}/preview`, { receiptId: initialReceipt.receiptId, outputId, limit: 100 });
    const initialColumns = new Map(initialPreview.columns.map(column => [column.label, column.column]));
    for (const label of ['Observation ID', 'Observation subject.reference', 'Observation valueQuantity.code', 'Observation valueQuantity.value']) {
      assert(initialColumns.has(label), `Initial native preview must expose exact source field ${label}`);
    }
    const sourceExpected = oracle.sourceRows.map(row => ({
      [initialColumns.get('Observation ID')]: row.id,
      [initialColumns.get('Observation subject.reference')]: row.subjectReference,
      [initialColumns.get('Observation valueQuantity.code')]: row.category === 'd' ? 'd' : null,
      [initialColumns.get('Observation valueQuantity.value')]: row.value,
    }));
    rowsForColumns(initialPreview, sourceExpected, 'exact five-row Observation starting state');
    const initialVisible = await assertVisible(sourceExpected, 'exact five-row Observation starting state',
      initialPreview.columns.map(column => ({ name: column.column, label: column.label })));
    const observationIDHeaderIndex = previewHeaderIndex(initialVisible.headers, 'Observation ID');
    assert.deepEqual(initialVisible.rows.map(row => row.values[observationIDHeaderIndex]).sort(),
      oracle.sourceRows.map(row => row.id).sort(), 'The visible starting table must retain every exact raw Observation ID');
    check('correctness', 'Native Observation starting table matches all five literal fixture rows and IDs', true,
      { rowCount: initialPreview.rowCount, sourceIDs: oracle.sourceRows.map(row => row.id), rows: sourceExpected });
  });
  await waitFor(page, () => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false, undefined);

  const openPivot = async () => {
    await click('open row changes for Pivot', page.getByTestId('construction-rows-settings-trigger'), () =>
      waitFor(page, () => document.querySelector('[data-testid="construction-action-table-pivot-rows"]')?.disabled === false, undefined));
    await click('choose native Pivot', page.getByTestId('construction-action-table-pivot-rows'), () =>
      waitVisible(page.locator('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]')));
  };
  const choosePivotSource = async (label, path, afterSelection) => {
    const selector = `select[aria-label=${JSON.stringify(label)}]`;
    const options = await page.locator(selector).locator('option').evaluateAll(items => items.map(item => ({ label: item.textContent.trim(), value: item.value, title: item.title })));
    const [resourceType, ...fieldPathSegments] = path.split('.');
    const fieldPath = fieldPathSegments.length > 0 ? fieldPathSegments.join('.') : path;
    const match = options.filter(option => {
      const optionFieldPath = String(option.title ?? '').replace(/^root\./, '');
      return option.value.startsWith('source:')
        && (optionFieldPath === path || optionFieldPath === fieldPath)
        && (!option.label.includes(' · ') || option.label.startsWith(`${resourceType} ·`));
    });
    assert.equal(match.length, 1, `${label}: expected one native source choice for ${path}: ${JSON.stringify(options)}`);
    const role = label === 'Add pivot group field' ? 'group' : label === 'Pivot category field' ? 'category' : 'value';
    await select(`select ${label} ${path}`, page.locator(selector), match[0].value,
      async () => {
        await waitFor(page, pivotSourceSelectionReady, {
          selector, sourceValue: match[0].value, role, expectedLabel: match[0].label,
        });
        if (afterSelection) await afterSelection();
      });
    return match[0].value;
  };
  const pivotProposal = async (fromIndex, label) => {
    const response = await proposal(fromIndex, label, ['/construction-proposals']);
    const step = response.candidateConstruction?.steps?.findLast(candidate => candidate.operation?.kind === 'PIVOT');
    assert(step, `${label}: candidate construction must contain a PIVOT step`);
    const operation = step.operation.pivot;
    assert.equal(operation.duplicatePolicy, 'ERROR', `${label}: unique fixture cells must use the default ERROR duplicate policy`);
    assert.equal(operation.groupKeyIds.length, 1, `${label}: Pivot must retain exactly one row identity key`);
    const groupOutput = outputForID(step, operation.groupKeyIds[0], label);
    const categories = operation.categories.map(category => ({
      identity: categoryIdentity(category.key),
      output: outputForID(step, category.outputColumnId, label),
      key: category.key,
    }));
    assert.deepEqual(categories.map(category => category.identity).sort(), ['MISSING', 'NULL', 'd']);
    const byIdentity = new Map(categories.map(category => [category.identity, category.output]));
    const expectedObjects = oracle.pivotRows.map(row => ({
      [groupOutput.name]: row.subjectReference,
      [byIdentity.get('MISSING').name]: row.MISSING,
      [byIdentity.get('NULL').name]: row.NULL,
      [byIdentity.get('d').name]: row.d,
    }));
    const preview = response.preview;
    assert(preview, `${label}: Pivot proposal must embed its typed preview`);
    assert.equal(preview.receiptId, response.proposalId, `${label}: Pivot preview must be bound to the exact proposal receipt`);
    const checked = rowsForColumns(preview, expectedObjects, label);
    assert.equal(new Set(preview.rows.map(row => row.__loom_row_id)).size, 2,
      `${label}: the two exact subject groups must have two distinct native row identities`);
    const categoryColumns = categories.map(category => ({
      identity: category.identity,
      id: category.output.id,
      name: category.output.name,
      label: category.output.label,
    }));
    return { response, step, operation, preview, groupOutput, categories, categoryColumns, expectedObjects, columns: checked.columns };
  };
  const choosePivot = async () => {
    await openPivot();
    await choosePivotSource('Add pivot group field', 'Observation.subject.reference');
    await choosePivotSource('Pivot category field', 'Observation.valueQuantity.code');
    const fromIndex = startProposal();
    let result;
    await choosePivotSource('Pivot values field', 'Observation.valueQuantity.value', async () => {
      result = await pivotProposal(fromIndex, 'native Pivot of Basic Observation quantities');
    });
    assert(result, 'The final native Pivot value selection must produce its exact proposal inside the action gate');
    return result;
  };
  const pivotPreview = await choosePivot();
  check('correctness', 'Native Pivot selection produces the exact two subject rows and MISSING, NULL, and d columns',
    pivotPreview.preview.rowCount === 2 && pivotPreview.categories.length === 3,
    { categories: pivotPreview.categories.map(({ identity, output }) => ({ identity, name: output.name, label: output.label })), rows: pivotPreview.expectedObjects });
  const cancelPivotCount = networkObserver.requests.filter(entry => entry.method === 'POST' && entry.path.endsWith('/commands')).length;
  await cancelProposal('native Pivot preview', prePivotWorkspace);
  assert.equal(networkObserver.requests.filter(entry => entry.method === 'POST' && entry.path.endsWith('/commands')).length, cancelPivotCount,
    'Canceling Pivot must not issue a draft command');
  check('correctness', 'Canceling the native Pivot preview preserves the exact source workspace without a command', true,
    { commandCount: cancelPivotCount, workspaceUnchanged: true });

  const confirmedPivot = await choosePivot();
  await awaitExactPanel('confirmed Pivot proposal');
  await applyProposal('native Pivot', confirmedPivot.expectedObjects);
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  const savedPivot = document.construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(savedPivot, 'Applied native Pivot must persist its exact construction step');
  assert.equal(savedPivot.operation.pivot.duplicatePolicy, 'ERROR');
  assert.deepEqual(savedPivot.operation.pivot.categories.map(category => categoryIdentity(category.key)).sort(), ['MISSING', 'NULL', 'd']);
  assert.deepEqual(savedPivot.operation.pivot.groupKeyIds, confirmedPivot.operation.groupKeyIds);
  const pivotReload = await reloadAndRead('reloaded Basic Pivot rows', confirmedPivot.expectedObjects);
  document = pivotReload.document;
  assert.deepEqual(document.construction.steps.map(step => step.operation.kind), ['PIVOT']);
  check('persistence', 'Applied Pivot retains its exact two row identities, three typed category columns, and fixture generation after reload',
    pivotReload.preview.rowCount === 2 && document.construction.steps.length === 1 && pivotReload.builder.catalog.generation === generation,
    { generation, outputId, rows: pivotReload.preview.rows.map(row => row.__loom_row_id), construction: document.construction.steps });

  const categoryLabels = new Map(confirmedPivot.categoryColumns.map(category => [category.identity, category.label]));
  assert([...categoryLabels.values()].every(label => typeof label === 'string' && label.length > 0),
    'Every typed Pivot category must have a visible column label for native Unpivot');
  const preservingExpected = oracle.preservingUnpivotRows.map(row => ({
    [confirmedPivot.groupOutput.name]: row.subjectReference,
    unpivotKey: categoryLabels.get(row.category),
    unpivotValue: row.value,
  }));
  const droppingExpected = oracle.droppingUnpivotRows.map(row => ({
    [confirmedPivot.groupOutput.name]: row.subjectReference,
    unpivotKey: categoryLabels.get(row.category),
    unpivotValue: row.value,
  }));
  const openUnpivot = async (fromIndex, nullPolicy, label, expected) => {
    await click('open row changes for Unpivot', page.getByTestId('construction-rows-settings-trigger'), () =>
      waitFor(page, () => document.querySelector('[data-testid="construction-action-unpivot-rows"]')?.disabled === false, undefined));
    await click('choose native Unpivot', page.getByTestId('construction-action-unpivot-rows'), () =>
      waitVisible(page.locator('[data-testid="construction-reshape-unpivot"]')));
    let result;
    for (const category of confirmedPivot.categoryColumns) {
      const selector = `input[aria-label=${JSON.stringify(`Unpivot ${category.label}`)}]`;
      const checkbox = page.locator(selector);
      await waitVisible(checkbox);
      assert.equal(await checkbox.count(), 1, `Unpivot must expose one exact Pivot output ${category.label}`);
      if (!await checkbox.isChecked()) {
        const previousProposalId = await readOptionalProposalID(page.locator(PROPOSAL_SELECTOR));
        const options = {
          timeout: ACTION_TIMEOUT_MS,
          budget: ACTION_TIMEOUT_MS,
        };
        if (category === confirmedPivot.categoryColumns.at(-1)) {
          options.after = async () => {
            await waitFor(page, expected => {
              const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
              return panel?.dataset.proposalStatus === 'ready' && panel.dataset.proposalId !== expected.previousProposalId;
            }, { previousProposalId: previousProposalId ?? '' });
            result = await unpivotProposal(fromIndex, nullPolicy, label, expected);
          };
        }
        await action(`select native Unpivot input ${category.identity}`, checkbox,
          locator => locator.check({ timeout: ACTION_TIMEOUT_MS }), options);
      }
    }
    assert(result, `${label}: the final native Unpivot checkbox action must produce its exact proposal inside the action gate`);
    return result;
  };
  const unpivotProposal = async (fromIndex, nullPolicy, label, expected) => {
    await waitFor(page, () => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready', undefined);
    const response = await proposal(fromIndex, label, ['/construction-proposals']);
    const step = response.candidateConstruction?.steps?.findLast(candidate => candidate.operation?.kind === 'UNPIVOT');
    assert(step, `${label}: candidate construction must contain an UNPIVOT step`);
    const operation = step.operation.unpivot;
    assert.equal(operation.nullRowPolicy, nullPolicy, `${label}: Unpivot must retain the exact selected null row policy`);
    assert.deepEqual(operation.inputs.map(input => input.columnId).sort(), confirmedPivot.categoryColumns.map(category => category.id).sort(),
      `${label}: Unpivot must consume exactly the three columns created by the native Pivot`);
    const groupOutput = step.outputs.find(output => output.id === confirmedPivot.groupOutput.id);
    assert(groupOutput, `${label}: Unpivot must preserve the exact Pivot row identity output`);
    const keyOutput = outputForID(step, operation.keyOutputColumnId, label);
    const valueOutput = outputForID(step, operation.valueOutputColumnId, label);
    const actualExpected = expected.map(row => ({
      [groupOutput.name]: row[confirmedPivot.groupOutput.name],
      [keyOutput.name]: row.unpivotKey,
      [valueOutput.name]: row.unpivotValue,
    }));
    const preview = response.preview;
    assert(preview, `${label}: native Unpivot proposal must embed a typed preview`);
    assert.equal(preview.receiptId, response.proposalId, `${label}: Unpivot preview must bind to the exact proposal receipt`);
    rowsForColumns(preview, actualExpected, label);
    const identityTuples = preview.rows.map(row => ({
      rowID: row.__loom_row_id,
      group: row[groupOutput.name] ?? null,
      key: row[keyOutput.name] ?? null,
      value: row[valueOutput.name] ?? null,
    }));
    assert(identityTuples.every(item => typeof item.rowID === 'string' && item.rowID.length > 0),
      `${label}: every unpivoted tuple needs a native row identity`);
    assert.equal(new Set(identityTuples.map(item => item.rowID)).size, expected.length,
      `${label}: each group/category tuple must have a distinct row identity`);
    return { response, step, operation, preview, groupOutput, keyOutput, valueOutput, expectedRows: actualExpected, identityTuples };
  };
  const chooseUnpivot = async () => {
    const fromIndex = startProposal();
    return openUnpivot(fromIndex, 'PRESERVE', 'preserving native Unpivot of Pivot columns', preservingExpected);
  };

  const preUnpivotWorkspace = structuredClone(builder.workspace);
  const previewUnpivot = await chooseUnpivot();
  assert.equal(previewUnpivot.preview.rowCount, 6, 'PRESERVE must retain all two-by-three Pivot cells, including the absent category cell');
  check('correctness', 'Native Unpivot preview preserves all six group/category pairs and the generated null value', true,
    { rowCount: previewUnpivot.preview.rowCount, rows: previewUnpivot.expectedRows, identityTuples: previewUnpivot.identityTuples });
  await cancelProposal('native Unpivot PRESERVE preview', preUnpivotWorkspace);
  check('correctness', 'Canceling Unpivot preserves the exact saved Pivot workspace and rows', true,
    { workspaceUnchanged: true, pivotRows: pivotReload.preview.rows });

  const confirmedUnpivot = await chooseUnpivot();
  await applyProposal('native Unpivot PRESERVE', confirmedUnpivot.expectedRows);
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  let savedUnpivot = document.construction.steps.find(step => step.operation.kind === 'UNPIVOT');
  assert(savedUnpivot, 'Applied Unpivot must persist its native operation step');
  assert.deepEqual(savedUnpivot.operation.unpivot.inputs.map(input => input.columnId).sort(), confirmedPivot.categoryColumns.map(category => category.id).sort());
  assert.equal(savedUnpivot.operation.unpivot.nullRowPolicy, 'PRESERVE');
  const preserveReload = await reloadAndRead('reloaded six-row preserving Unpivot', confirmedUnpivot.expectedRows);
  document = preserveReload.document;
  savedUnpivot = document.construction.steps.find(step => step.operation.kind === 'UNPIVOT');
  assert.equal(savedUnpivot?.operation.unpivot?.nullRowPolicy, 'PRESERVE');
  const reloadedPreserveTuples = preserveReload.preview.rows.map(row => ({
    rowID: row.__loom_row_id,
    group: row[confirmedUnpivot.groupOutput.name] ?? null,
    key: row[confirmedUnpivot.keyOutput.name] ?? null,
    value: row[confirmedUnpivot.valueOutput.name] ?? null,
  }));
  assert.deepEqual(stableRows(reloadedPreserveTuples), stableRows(confirmedUnpivot.identityTuples),
    'Preserving Unpivot must retain the exact row identity-to-group/category/value mapping across reload');
  check('persistence', 'Applied Unpivot reload preserves all six exact group/category rows and stable row identities',
    preserveReload.preview.rowCount === 6 && savedUnpivot.operation.unpivot.nullRowPolicy === 'PRESERVE',
    { rowCount: preserveReload.preview.rowCount, identityTuples: reloadedPreserveTuples, construction: document.construction.steps });

  const history = async kind => {
    const steps = builder.workspace.documents.find(item => item.output.id === outputId).construction.steps;
    const step = steps.findLast(candidate => candidate.operation.kind === kind);
    assert(step, `Saved ${kind} step must remain available in the history`);
    const historyButton = page.getByTestId(`construction-history-step-${step.id}`);
    await click(`select saved ${kind} step`, historyButton);
    return step;
  };
  const openStepEditor = async step => {
    const editorButton = page.getByTestId(`construction-edit-step-${step.id}`);
    await click('open saved Unpivot editor', editorButton, () =>
      waitVisible(page.locator('[data-testid="construction-reshape-unpivot"]')));
  };
  let currentUnpivot = await history('UNPIVOT');
  await openStepEditor(currentUnpivot);
  const advanced = page.getByTestId('construction-unpivot-advanced');
  if (!(await advanced.evaluate(element => element.open))) {
    await click('open saved Unpivot advanced options', advanced.locator(':scope > summary'), () =>
      waitFor(page, () => document.querySelector('[data-testid="construction-unpivot-advanced"]')?.open === true, undefined));
  }
  const editOffset = startProposal();
  const previousEditProposalId = await readOptionalProposalID(page.locator(PROPOSAL_SELECTOR));
  let editedUnpivot;
  await select('edit saved Unpivot policy to DROP', page.getByLabel('Unpivot null row policy', { exact: true }), 'DROP',
    async () => {
      await waitFor(page, expected => {
        const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
        return panel?.dataset.proposalStatus === 'ready' && panel.dataset.proposalId !== expected.previousProposalId;
      }, { previousProposalId: previousEditProposalId });
      editedUnpivot = await unpivotProposal(editOffset, 'DROP', 'edited DROP native Unpivot of Pivot columns', droppingExpected);
    });
  assert(editedUnpivot, 'The Unpivot DROP policy selection must produce its exact proposal inside the action gate');
  assert.equal(editedUnpivot.preview.rowCount, 5, 'DROP must remove only the one absent group/category cell');
  await applyProposal('edited Unpivot DROP', editedUnpivot.expectedRows);
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  currentUnpivot = document.construction.steps.find(step => step.operation.kind === 'UNPIVOT');
  assert.equal(currentUnpivot.operation.unpivot.nullRowPolicy, 'DROP');
  const dropReload = await reloadAndRead('reloaded five-row DROP Unpivot', editedUnpivot.expectedRows);
  document = dropReload.document;
  currentUnpivot = document.construction.steps.find(step => step.operation.kind === 'UNPIVOT');
  assert.equal(currentUnpivot.operation.unpivot.nullRowPolicy, 'DROP');
  assert.equal(dropReload.preview.rowCount, 5);
  const reloadedDropTuples = dropReload.preview.rows.map(row => ({
    rowID: row.__loom_row_id,
    group: row[editedUnpivot.groupOutput.name] ?? null,
    key: row[editedUnpivot.keyOutput.name] ?? null,
    value: row[editedUnpivot.valueOutput.name] ?? null,
  }));
  assert.deepEqual(stableRows(reloadedDropTuples), stableRows(editedUnpivot.identityTuples),
    'Edited DROP Unpivot must retain the exact surviving row identity mapping across reload');
  check('persistence', 'Editing Unpivot to DROP removes only the generated null cell and survives reload', true,
    { rowCount: dropReload.preview.rowCount, expectedRows: editedUnpivot.expectedRows,
      identityTuples: reloadedDropTuples,
      droppedTuple: { subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'NULL', value: null } });

  const beforeUnpivotRemoval = structuredClone(builder.workspace);
  const beforeUnpivotRemovalRows = editedUnpivot.expectedRows;
  const removeUnpivot = async () => {
    const step = await history('UNPIVOT');
    const fromIndex = startProposal();
    let result;
    await click('propose removal of saved Unpivot', page.getByTestId(`construction-remove-step-${step.id}`),
      async () => {
        await waitFor(page, () => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready', undefined);
        const response = await proposal(fromIndex, 'remove saved Unpivot', ['/construction-proposals']);
        const expected = confirmedPivot.expectedObjects;
        rowsForColumns(response.preview, expected, 'remove saved Unpivot restores exact Pivot rows');
        await assertVisibleProposal(response.proposalId, expected,
          'remove saved Unpivot restores exact Pivot rows',
          response.preview.columns.map(column => ({ name: column.column, label: column.label })));
        result = { response, expected };
      });
    return result;
  };
  const removalPreview = await removeUnpivot();
  await cancelProposal('Unpivot removal preview', beforeUnpivotRemoval);
  assert.deepEqual(beforeUnpivotRemovalRows, editedUnpivot.expectedRows);
  check('correctness', 'Canceling Unpivot removal preserves the edited five-row saved result', true,
    { rowsPreserved: beforeUnpivotRemovalRows.length, workspaceUnchanged: true });
  const confirmedRemoval = await removeUnpivot();
  await applyProposal('remove Unpivot and restore Pivot', confirmedRemoval.expected);
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  assert.deepEqual(document.construction.steps.map(step => step.operation.kind), ['PIVOT'],
    'Removing Unpivot must restore the exact saved Pivot as the remaining construction');
  const restoredPivot = await reloadAndRead('reloaded Pivot after Unpivot removal', confirmedPivot.expectedObjects);
  document = restoredPivot.document;
  assert.deepEqual(document.construction.steps.map(step => step.operation.kind), ['PIVOT']);
  check('persistence', 'Removing Unpivot restores the exact two-row Pivot and its three output columns after reload',
    restoredPivot.preview.rowCount === 2 && document.construction.steps.length === 1,
    { rowCount: restoredPivot.preview.rowCount, rows: restoredPivot.preview.rows, construction: document.construction.steps });

  const removePivot = async () => {
    const step = await history('PIVOT');
    const fromIndex = startProposal();
    let result;
    await click('propose removal of saved Pivot', page.getByTestId(`construction-remove-step-${step.id}`),
      async () => {
        await waitFor(page, () => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready', undefined);
        const response = await proposal(fromIndex, 'remove saved Pivot', ['/construction-proposals']);
        const sourceColumns = response.preview.columns;
        const idColumn = sourceColumns.find(column => column.label === 'Observation ID');
        const subjectColumn = sourceColumns.find(column => column.label === 'Observation subject.reference');
        const codeColumn = sourceColumns.find(column => column.label === 'Observation valueQuantity.code');
        const valueColumn = sourceColumns.find(column => column.label === 'Observation valueQuantity.value');
        assert(idColumn && subjectColumn && codeColumn && valueColumn,
          `Removing Pivot must restore the exact four source columns: ${JSON.stringify(sourceColumns)}`);
        const expected = oracle.sourceRows.map(row => ({
          [idColumn.column]: row.id,
          [subjectColumn.column]: row.subjectReference,
          [codeColumn.column]: row.category === 'MISSING' ? null : row.category === 'NULL' ? null : row.category,
          [valueColumn.column]: row.value,
        }));
        rowsForColumns(response.preview, expected, 'remove Pivot restores exact five raw source identities and values');
        await assertVisibleProposal(response.proposalId, expected,
          'remove Pivot restores exact five raw source identities and values',
          response.preview.columns.map(column => ({ name: column.column, label: column.label })));
        result = { response, expected };
      });
    return result;
  };
  const pivotRemoval = await removePivot();
  const beforePivotRemoval = structuredClone(builder.workspace);
  await cancelProposal('Pivot removal preview', beforePivotRemoval);
  check('correctness', 'Canceling Pivot removal preserves the exact saved Pivot workspace', true,
    { workspaceUnchanged: true, pivotRows: restoredPivot.preview.rows });
  const confirmedPivotRemoval = await removePivot();
  await applyProposal('remove Pivot and restore source rows', confirmedPivotRemoval.expected);
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  assert.deepEqual(document.construction.steps, [], 'Removing Pivot must restore the source table without construction steps');
  const restoredSource = await reloadAndRead('reloaded exact Observation source rows', confirmedPivotRemoval.expected);
  document = restoredSource.document;
  assert.deepEqual(document.construction.steps, []);
  assert.deepEqual(document.columns.map(sourcePath).sort(), prePivotColumns.map(sourcePath).sort(),
    'Removing Pivot must restore every original source field binding');
  const sourceIDs = restoredSource.preview.rows.map(row => row[restoredSource.preview.columns.find(column => column.label === 'Observation ID').column]).sort();
  assert.deepEqual(sourceIDs, oracle.sourceRows.map(row => row.id).sort(),
    'Removing Pivot and reloading must restore all five exact source Observation IDs');
  check('persistence', 'Removing Pivot restores all five original Observation rows, source bindings, and IDs after reload',
    sourceIDs.length === 5 && document.construction.steps.length === 0 && document.columns.length === prePivotColumns.length,
    { sourceIDs, sourcePaths: document.columns.map(sourcePath), generation: restoredSource.builder.catalog.generation });

  const measured = report.actions.filter(item => Number.isFinite(item.elapsedMs));
  assert(measured.every(item => item.elapsedMs <= ACTION_TIMEOUT_MS),
    `Every Pivot→Unpivot native control action must remain within ${ACTION_TIMEOUT_MS}ms`);
  check('performance', 'Every Pivot→Unpivot lifecycle action stays within the five-second native gate', true,
    { actionCount: measured.length, maxActionMs: Math.max(0, ...measured.map(item => item.elapsedMs)), budgetMs: ACTION_TIMEOUT_MS });
  report.target.verificationReads = apiReads;
  report.target.fixtureOracle = {
    kind: 'Basic native fixture; two Observation.subject.reference groups and MISSING/NULL/d quantity-code categories',
    sourceRows: oracle.sourceRows,
    categories: oracle.categories,
    pivotRows: oracle.pivotRows,
    preservingUnpivotRows: oracle.preservingUnpivotRows,
    droppingUnpivotRows: oracle.droppingUnpivotRows,
    seedManifest: manifest,
  };
  report.lifecycle = ['select Pivot group/category/value', 'preview Pivot', 'Cancel Pivot', 'Apply Pivot', 'reload',
    'select Pivot columns for Unpivot', 'preview preserving Unpivot', 'Cancel Unpivot', 'Apply Unpivot', 'reload',
    'edit Unpivot PRESERVE to DROP', 'Apply', 'reload', 'Cancel Unpivot removal', 'remove Unpivot and restore Pivot', 'reload',
    'Cancel Pivot removal', 'remove Pivot and restore source', 'reload'];
  assert.equal(removalPreview.response.preview.rowCount, 2);
  assert.equal(pivotRemoval.response.preview.rowCount, oracle.sourceRows.length);
}
