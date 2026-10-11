import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { performance } from 'node:perf_hooks';
import { join } from 'node:path';
import { assertVisibleRowsMatchOracle, readNDJSONResourceIdentityOracle } from '../helpers/ndjson-resource-oracle.mjs';
import { readBasicCodedGroupRecordedCodeOracle, visibleRecordedCodeGroupRows } from '../helpers/basic-coded-group-recorded-code-oracle.mjs';
import { previewHeaderIndex } from '../helpers/playwright-preview-rows.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { browserURL } from './builder-url.mjs';

const ACTION_BUDGET_MS = 5_000;
const ROOT_TABLE_TITLE = 'Basic Observation recorded codes';
const CODING_PATH = 'code.coding[]';

const remainingMs = (startedAt, label) => {
  const remaining = ACTION_BUDGET_MS - (performance.now() - startedAt);
  assert(remaining > 0, `${label} exhausted its ${ACTION_BUDGET_MS} ms action-to-render budget`);
  return remaining;
};

const waitForDOM = (page, predicate, argument, timeoutMs) => page.waitForFunction(
  predicate,
  argument,
  { timeout: Math.max(1, Math.min(ACTION_BUDGET_MS, timeoutMs)) },
);

export const readVisibleTable = (page, proposal = false) => page.evaluate(({ expectProposal }) => {
  if (expectProposal) {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    const table = preview?.querySelector('table');
    if (!preview || !table) return undefined;
    const rows = [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
      .map((row, index) => ({
        ordinal: index + 1,
        cells: [...row.querySelectorAll('td')].map(cell => cell.innerText.replace(/\s+/g, ' ').trim()),
      }));
    const headers = [...table.querySelectorAll('thead th')]
      .map(cell => (cell.firstElementChild?.innerText ?? '').replace(/\s+/g, ' ').trim());
    return {
      outputId: preview.dataset.previewOutputId,
      status: preview.dataset.previewStatus,
      rowCount: rows.length,
      columnCount: String(headers.length),
      headers,
      rows,
    };
  }

  const preview = document.querySelector('[data-testid="construction-preview"]');
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!preview || !table) return undefined;
  const rows = [...table.querySelectorAll('[role="row"]')].slice(1).map(row => ({
    ordinal: Number(row.firstElementChild?.innerText.trim()),
    cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.replace(/\s+/g, ' ').trim()),
  })).filter(row => row.cells.length > 0);
  return {
    outputId: preview.dataset.previewOutputId,
    status: preview.dataset.previewStatus,
    ariaRowCount: table.getAttribute('aria-rowcount'),
    ariaColumnCount: table.getAttribute('aria-colcount'),
    headers: [...table.querySelectorAll('[role="columnheader"]')]
      .map(cell => cell.innerText.replace(/\s+/g, ' ').trim()),
    rows,
  };
}, { expectProposal: proposal });

const assertActiveOutput = async (page, outputId) => {
  const selected = page.locator('button[data-testid^="construction-table-"][aria-current="page"]');
  assert.equal(await selected.count(), 1, 'Builder must expose exactly one selected output table');
  assert.equal(await selected.getAttribute('data-testid'), `construction-table-${outputId}`,
    'The selected Builder table must be the exact Observation root output');
};

const assertOperationEditorOutput = async (page, outputId) => {
  const editor = page.locator('[data-testid="construction-operation-editor"]');
  assert.equal(await editor.count(), 1, 'The native reshape editor must be the active Builder context');
  assert.equal(await editor.isVisible(), true, 'The exact reshape output binding must be visibly active');
  assert.equal(await editor.getAttribute('data-output-id'), outputId,
    'The native reshape editor must belong to the exact Observation output');
};

export const waitForPreview = async (page, { outputId, dataRows, proposal = false, timeoutMs }) => {
  await waitForDOM(page, ({ expectedOutputId, expectedRows, expectProposal }) => {
    const preview = expectProposal
      ? document.querySelector('[data-testid="construction-proposal-preview"]')
      : document.querySelector('[data-testid="construction-preview"]');
    const table = expectProposal
      ? preview?.querySelector('table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    const renderedRows = expectProposal
      ? table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length
      : Number(table?.getAttribute('aria-rowcount')) - 1;
    return preview?.dataset.previewStatus === 'ready'
      && preview.dataset.previewOutputId === expectedOutputId
      && renderedRows === expectedRows
      && (!expectProposal || panel?.dataset.proposalStatus === 'ready')
      && (expectProposal || (!document.body.innerText.includes('Loading your table…')
        && !document.body.innerText.includes('Preview failed:')));
  }, { expectedOutputId: outputId, expectedRows: dataRows, expectProposal: proposal }, timeoutMs);
};

export const assertRootRows = async (page, outputId, oracle, label, timeoutMs, proposal = false) => {
  await waitForPreview(page, { outputId, dataRows: oracle.count, proposal, timeoutMs });
  if (!proposal) await assertActiveOutput(page, outputId);
  const table = await readVisibleTable(page, proposal);
  assert.equal(table.outputId, outputId, `${label} preview must belong to its exact root output`);
  if (proposal) {
    assert.equal(table.columnCount, '1', `${label} proposal must retain only the direct root ID column`);
    assert.equal(table.rows.length, oracle.count, `${label} proposal must render all six source rows`);
    assert.deepEqual(table.rows.map(row => row.cells), oracle.ids.map(id => [id]),
      `${label} proposal rows must exactly match independent Basic Observation identities`);
    assert.deepEqual(table.rows.map(row => row.ordinal), oracle.ids.map((_, index) => index + 1),
      `${label} proposal source rows must be contiguous and complete`);
  } else {
    assert.equal(table.ariaColumnCount, '1', `${label} must retain only the direct root ID column`);
    assert.equal(table.rows.length, oracle.count, `${label} must render all six source rows in the Preview viewport`);
    assertVisibleRowsMatchOracle({ rows: table.rows, sourceIds: oracle.ids, ariaRowCount: table.ariaRowCount });
  }
  return table;
};

export const assertCodedGroupRows = async (page, outputId, oracle, label, timeoutMs, proposal = false, operationEditor = proposal) => {
  await waitForPreview(page, { outputId, dataRows: oracle.groups.length, proposal, timeoutMs });
  if (operationEditor) await assertOperationEditorOutput(page, outputId);
  else await assertActiveOutput(page, outputId);
  const table = await readVisibleTable(page, proposal);
  assert.equal(table.outputId, outputId, `${label} preview must belong to its exact Observation output`);
  assert.equal(proposal ? table.columnCount : table.ariaColumnCount, '4',
    `${label} must render the four declared CODED_GROUP outputs`);
  const expectedHeaders = ['Code system', 'Code version', 'Code', 'Source records'];
  assert.equal(table.headers.length, expectedHeaders.length,
    `${label} must render exactly the four standalone coded-group output columns`);
  assert.deepEqual(expectedHeaders.map(header => previewHeaderIndex(table.headers, header)), [0, 1, 2, 3],
    `${label} headers must map uniquely to the exact standalone coded-group output order`);
  assert.equal(table.rows.length, oracle.groups.length, `${label} must render every independently derived code group`);
  assert.deepEqual(table.rows.map(row => row.cells), visibleRecordedCodeGroupRows(oracle.groups),
    `${label} visible tuples and source counts must match the exact direct-code oracle`);
  assert.deepEqual(table.rows.map(row => row.ordinal), oracle.groups.map((_, index) => index + 1),
    `${label} code-group rows must be contiguous and complete`);
  if (!proposal) {
    assert.equal(Number(table.ariaRowCount), oracle.groups.length + 1,
      `${label} must report every coded-group row plus its header`);
  }
  return table;
};

const readBuilder = async (page, target, explorerId, timeoutMs) => {
  const path = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`;
  const response = await page.context().request.get(`${target.apiUrl}${path}`, { timeout: timeoutMs });
  let value;
  try { value = await response.json(); }
  catch (error) { throw new Error(`Read-only Builder request returned HTTP ${response.status()} without JSON: ${error.message}`); }
  assert(response.ok(), `Read-only Builder request returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 800)}`);
  return value;
};

const assertRootBinding = (builder, target, expected) => {
  assert.equal(builder.catalog?.generation, target.fixtureGeneration,
    'Builder must remain bound to the exact Basic fixture generation');
  assert.equal(builder.workspace?.documents?.length, 1, 'The fresh Explorer must contain one Observation output');
  assert.equal(builder.workspace?.tabs?.length, 1, 'The fresh Explorer must contain one Observation table tab');
  const document = builder.workspace.documents.find(candidate => candidate.output?.id === expected.outputId);
  assert(document, 'Builder must retain the exact native Observation output identity');
  const tab = builder.workspace.tabs.find(candidate => candidate.outputId === expected.outputId);
  assert(tab, 'Builder must retain the exact native Observation table tab');
  assert.equal(document.output.title, ROOT_TABLE_TITLE, 'The saved root table must retain its authored title');
  assert.equal(tab.title, ROOT_TABLE_TITLE, 'The table tab must retain the authored root title');
  assert.equal(document.rootResourceType, 'Observation', 'The saved output must retain Observation as its root');
  const directRootID = (document.columns ?? []).filter(column => column.occurrenceId === 'base'
    && column.source?.kind === 'field'
    && column.source.field?.path === 'id'
    && column.source.field?.projectionMode === 'VALUE');
  assert.equal(directRootID.length, 1, 'The saved table must retain one exact direct Observation.id binding');
  return document;
};

const assertSavedCodedGroup = (builder, target, expected, oracle, policy) => {
  const document = assertRootBinding(builder, target, expected);
  const steps = document.construction?.steps ?? [];
  assert.equal(steps.length, 1, 'The saved Observation output must contain exactly one standalone coded-group step');
  const [step] = steps;
  assert(step.id, 'The saved CODED_GROUP must have a stable construction identity');
  assert.equal(step.operation?.kind, 'CODED_GROUP', 'The native By recorded code control must save CODED_GROUP');
  const codedGroup = step.operation.codedGroup;
  assert.equal(codedGroup.constructionId, step.id, 'CODED_GROUP must retain its exact step identity');
  assert.equal(codedGroup.source.occurrenceId, 'base', 'CODED_GROUP must bind directly to the root occurrence');
  assert.equal(codedGroup.source.resourceType, 'Observation', 'CODED_GROUP must retain Observation as its source');
  assert.equal(codedGroup.source.codingPath, CODING_PATH, 'CODED_GROUP must bind to direct Observation.code.coding[]');
  assert.equal(codedGroup.source.fhirType, 'Coding');
  assert.equal(codedGroup.source.cardinality, 'MANY');
  assert.equal(codedGroup.source.shape, 'ARRAY');
  assert.deepEqual(codedGroup.source.route, [], 'CODED_GROUP must stay on the root without a relationship route');
  assert.equal(codedGroup.missingKeyPolicy, policy, 'Saved coded-group edit must preserve its selected missing-code policy');
  assert.equal(new Set([
    codedGroup.systemOutputColumnId,
    codedGroup.versionOutputColumnId,
    codedGroup.codeOutputColumnId,
    codedGroup.distinctSourceCountOutputColumnId,
  ]).size, 4, 'CODED_GROUP must retain four distinct output identities');
  assert.deepEqual(step.outputs.map(({ name, label }) => ({ name, label })), [
    { name: 'code_system', label: 'Code system' },
    { name: 'code_version', label: 'Code version' },
    { name: 'code', label: 'Code' },
    { name: 'source_records', label: 'Source records' },
  ], 'Saved CODED_GROUP must retain its exact declared output columns');
  assert.equal(oracle.codingPath, CODING_PATH, 'The raw fixture oracle must use the same direct Coding path');
  return { document, step };
};

const assertNoConstruction = (builder, target, expected) => {
  const document = assertRootBinding(builder, target, expected);
  assert.deepEqual(document.construction?.steps ?? [], [], 'The restored Observation table must have no coded-group construction');
  return document;
};

export const basicCodedGroupRecordedCodeWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  const target = context.target;
  configureNativePage(page);
  assert.equal(context.custom, false, 'This lifecycle requires a fresh owned Basic fixture');
  assert.equal(context.seed?.fresh, true, 'This lifecycle must use a fresh disposable fixture project');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'The Basic fixture project must be isolated');
  assert(target.fixtureGeneration && target.fixtureDir, 'The Basic fixture project, generation, and source directory must be explicit');
  assert.equal(existsSync(join(target.fixtureDir, 'j01-concepts.fixture.json')), false,
    'The literal six-row Basic coded-group fixture must not select generated J01 Observations');

  const sourcePath = join(target.fixtureDir, 'Observation.ndjson');
  const [identityOracle, codedOracle] = await Promise.all([
    readNDJSONResourceIdentityOracle({
      path: sourcePath, project: target.fixtureProject, generation: target.fixtureGeneration, resourceType: 'Observation',
    }),
    readBasicCodedGroupRecordedCodeOracle({
      path: sourcePath, project: target.fixtureProject, generation: target.fixtureGeneration,
    }),
  ]);
  assert.equal(identityOracle.count, 6, 'Basic Observation identity oracle must contain exactly six records');
  assert.equal(codedOracle.sourceCount, 6, 'Basic coded-group source oracle must contain exactly six records');
  assert.deepEqual([...identityOracle.ids].sort(), codedOracle.sourceIDs,
    'Identity and literal code oracles must bind to the same six Observation source records');
  report.target.fixtureOracle = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sourcePath: codedOracle.path,
    sourceSHA256: codedOracle.sha256,
    resourceType: codedOracle.resourceType,
    codingPath: codedOracle.codingPath,
    sourceCount: codedOracle.sourceCount,
    sourceIDs: codedOracle.sourceIDs,
    groups: codedOracle.groups,
    rowIdentityOrdering: identityOracle.ordering,
  };
  report.target.qaIsolation = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    bootstrapExplorerId: target.bootstrapExplorerId,
    explorer: null,
    outputId: null,
    sourceSHA256: codedOracle.sha256,
    workflowMutations: [],
    cleanupMutations: [],
  };
  check('correctness', 'literal Basic Observation fixture proves exact direct code groups and distinct source counts',
    codedOracle.groups.length === 3 && codedOracle.groups.map(group => group.count).join(',') === '2,1,3',
    report.target.fixtureOracle);

  const nativeAction = (label, locator, perform, settle, requiredCheck) => {
    const startedAt = performance.now();
    return action(label, locator, perform, {
      timeout: ACTION_BUDGET_MS,
      budget: ACTION_BUDGET_MS,
      ...(settle ? { after: async () => {
        await settle(() => remainingMs(startedAt, label));
        remainingMs(startedAt, label);
      } } : {}),
      ...(requiredCheck ? { requiredCheck } : {}),
    });
  };
  const click = (label, locator, settle, requiredCheck) => nativeAction(
    label, locator, () => locator.click({ timeout: ACTION_BUDGET_MS }), settle, requiredCheck,
  );
  const fill = (label, locator, value, requiredCheck) => {
    const startedAt = performance.now();
    return action(label, locator, () => locator.fill(value, { timeout: ACTION_BUDGET_MS }), {
      editable: true,
      timeout: ACTION_BUDGET_MS,
      budget: ACTION_BUDGET_MS,
      after: async () => {
        await locator.inputValue({ timeout: remainingMs(startedAt, label) }).then(actual =>
          assert.equal(actual, value, `${label} must retain the exact entered title`));
        remainingMs(startedAt, label);
      },
      ...(requiredCheck ? { requiredCheck } : {}),
    });
  };
  const select = (label, locator, value, settle, requiredCheck) => {
    const startedAt = performance.now();
    return action(label, locator, () => locator.selectOption(value, { timeout: ACTION_BUDGET_MS }), {
      timeout: ACTION_BUDGET_MS,
      budget: ACTION_BUDGET_MS,
      after: async () => {
        assert.equal(await locator.inputValue({ timeout: remainingMs(startedAt, label) }), value,
          `${label} must select the exact recorded Coding path`);
        if (settle) await settle(() => remainingMs(startedAt, label));
        remainingMs(startedAt, label);
      },
      ...(requiredCheck ? { requiredCheck } : {}),
    });
  };

  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS,
  });
  await waitForDOM(page, expectedExplorer => document.querySelector('select[aria-label="Explorer"]')?.value === expectedExplorer,
    target.bootstrapExplorerId, ACTION_BUDGET_MS);
  const newExplorer = page.getByText('New explorer', { exact: true });
  await click('open native Explorer creation for recorded-code grouping', newExplorer,
    timeoutMs => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: timeoutMs() }));
  const explorerTitle = `Verify ${context.runID.slice(-10)} Basic recorded-code grouping`;
  await fill('name the disposable Basic recorded-code Explorer', page.locator('#new-explorer-name'), explorerTitle);
  await click('create and select a blank native recorded-code Explorer', page.getByRole('button', { name: 'Create blank', exact: true }),
    timeoutMs => waitForDOM(page, title => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === title
      && document.body.innerText.includes('Build your first table'), explorerTitle, timeoutMs()),
    'native blank Explorer is selected in the exact fresh Basic project within five seconds');
  const explorerId = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorerId && explorerId !== target.bootstrapExplorerId, 'The native coded-group lifecycle must use its distinct Explorer');
  report.target.explorer = explorerId;
  report.target.qaIsolation.explorer = explorerId;
  report.target.qaIsolation.workflowMutations.push('native UI created a blank Explorer in the fresh Basic fixture project');

  await fill('name the Observation root table', page.locator('#first-table-name'), ROOT_TABLE_TITLE);
  const chooseObservation = page.getByRole('button', { name: 'Choose Observation rows', exact: true });
  let outputId;
  let baselineBuilder;
  await click('choose native Observation root rows', chooseObservation, async timeoutMs => {
    await waitForDOM(page, expectedRows => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return preview?.dataset.previewStatus === 'ready'
        && Boolean(preview.dataset.previewOutputId)
        && table?.getAttribute('aria-rowcount') === String(expectedRows + 1);
    }, identityOracle.count, timeoutMs());
    outputId = (await readVisibleTable(page))?.outputId;
    assert(outputId, 'Native Observation root creation must expose its exact output identity');
    await assertRootRows(page, outputId, identityOracle, 'Initial Observation source preview', timeoutMs());
    baselineBuilder = await readBuilder(page, target, explorerId, timeoutMs());
    const document = assertRootBinding(baselineBuilder, target, { outputId });
    assert.deepEqual(document.construction?.steps ?? [], [], 'Native root-table setup must begin without saved row operations');
  }, 'native Observation root selection renders all exact Basic source rows within five seconds');

  const baselineDocument = assertRootBinding(baselineBuilder, target, { outputId });
  assert.deepEqual(baselineDocument.construction?.steps ?? [], [], 'Native root-table setup must begin without saved row operations');
  report.target.outputId = outputId;
  report.target.qaIsolation.outputId = outputId;
  report.target.qaIsolation.workflowMutations.push('native UI created the named Observation root table');
  check('correctness', 'native Explorer saves the exact titled Observation root and direct ID binding', true, {
    explorerId, outputId, title: ROOT_TABLE_TITLE, rootResourceType: baselineDocument.rootResourceType,
    fixtureGeneration: baselineBuilder.catalog.generation,
  });
  const baselineDraftDigest = baselineBuilder.draftDigest;

  const waitForRootAndReadSaved = async timeoutMs => {
    const table = await assertRootRows(page, outputId, identityOracle, 'Restored Observation source preview', timeoutMs());
    assert.equal(await page.locator('[data-testid="construction-proposal-panel"]').count(), 0,
      'A settled direct-source preview must have no pending construction proposal');
    const builder = await readBuilder(page, target, explorerId, timeoutMs());
    const document = assertNoConstruction(builder, target, { outputId });
    return { table, builder, document };
  };
  const waitForCodedGroupEditor = async timeoutMs => {
    await page.locator('[data-testid="construction-reshape-coded-group"]').waitFor({ state: 'visible', timeout: timeoutMs() });
    const pathControl = page.getByTestId('construction-coded-group-path');
    await pathControl.waitFor({ state: 'visible', timeout: timeoutMs() });
    const pathOptions = await pathControl.locator('option').evaluateAll(options => options.map(option => option.value));
    assert(pathOptions.includes(CODING_PATH), 'Native coded-group path choices must include populated direct code.coding[]');
  };
  const waitForProposalGroups = async timeoutMs => {
    await waitForCodedGroupEditor(timeoutMs);
    const selectedPath = await page.getByTestId('construction-coded-group-path').inputValue();
    assert.equal(selectedPath, CODING_PATH, 'The native coded-group editor must target direct Observation.code.coding[]');
    return assertCodedGroupRows(page, outputId, codedOracle, 'Native standalone CODED_GROUP Preview', timeoutMs(), true);
  };

  const rowSettings = page.getByTestId('construction-rows-settings-trigger');
  await click('open native row-definition choices', rowSettings, async timeoutMs => {
    await page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible', timeout: timeoutMs() });
    const recordedCodeChoice = page.getByTestId('construction-action-coded-group-rows');
    await recordedCodeChoice.waitFor({ state: 'visible', timeout: timeoutMs() });
    await waitForDOM(page, () => {
      const button = document.querySelector('[data-testid="construction-action-coded-group-rows"]');
      return Boolean(button && !button.disabled);
    }, undefined, timeoutMs());
    assert.equal(await recordedCodeChoice.isEnabled(), true,
      'Basic populated Observation coding must enable the native By recorded code choice');
  });
  const codedGroupChoice = page.getByTestId('construction-action-coded-group-rows');
  await click('select native By recorded code row grouping', codedGroupChoice, async timeoutMs => {
    const pathControl = page.getByTestId('construction-coded-group-path');
    await pathControl.waitFor({ state: 'visible', timeout: timeoutMs() });
    const pathOptions = await pathControl.locator('option').evaluateAll(options => options.map(option => option.value));
    assert(pathOptions.includes(CODING_PATH), 'Native coded-group path choices must include populated direct code.coding[]');
    await waitForCodedGroupEditor(timeoutMs);
  }, 'native By recorded code opens the standalone coded-group editor within five seconds');

  const pathControl = page.getByTestId('construction-coded-group-path');
  await select('select exact Observation.code.coding[] in the native coded-group editor', pathControl, CODING_PATH,
    timeoutMs => waitForProposalGroups(timeoutMs),
    'native coded-group editor selects direct Observation.code.coding[] and settles exact Preview within five seconds');
  const initialGroupPreview = await assertCodedGroupRows(page, outputId, codedOracle,
    'Initial literal direct-code group Preview', ACTION_BUDGET_MS, true);
  check('correctness', 'native By recorded code Preview renders exact height, weight, and paired source counts', true, {
    outputId, headers: initialGroupPreview.headers, rows: initialGroupPreview.rows,
    expectedGroups: visibleRecordedCodeGroupRows(codedOracle.groups),
  });

  await click('Cancel the first native coded-group Preview', page.getByTestId('construction-cancel-proposal'), async timeoutMs => {
    const restored = await waitForRootAndReadSaved(timeoutMs);
    assert.equal(restored.builder.draftDigest, baselineDraftDigest,
      'Cancel must preserve the exact saved Builder draft digest');
    assert.deepEqual(restored.document.construction?.steps ?? [], [], 'Cancel must leave no CODED_GROUP step saved');
  }, 'Cancel restores exact Observation source rows and leaves the saved draft unchanged within five seconds');
  check('correctness', 'Cancel leaves the exact direct Observation rows and saved construction unchanged', true, {
    outputId, draftDigest: baselineDraftDigest, sourceIDs: identityOracle.ids,
  });

  await click('reopen native row-definition choices after Cancel', rowSettings, async timeoutMs => {
    const choice = page.getByTestId('construction-action-coded-group-rows');
    await choice.waitFor({ state: 'visible', timeout: timeoutMs() });
    await waitForDOM(page, () => {
      const button = document.querySelector('[data-testid="construction-action-coded-group-rows"]');
      return Boolean(button && !button.disabled);
    }, undefined, timeoutMs());
    assert.equal(await choice.isEnabled(), true, 'By recorded code must remain available after native Cancel');
  });
  await click('select By recorded code again for Apply', codedGroupChoice,
    timeoutMs => waitForCodedGroupEditor(timeoutMs));
  await select('reselect exact direct Observation Coding field before Apply', pathControl, CODING_PATH,
    timeoutMs => waitForProposalGroups(timeoutMs));
  await click('Apply native standalone CODED_GROUP', page.getByTestId('construction-apply-proposal'), async timeoutMs => {
    await assertCodedGroupRows(page, outputId, codedOracle, 'Applied standalone CODED_GROUP result', timeoutMs());
    const builder = await readBuilder(page, target, explorerId, timeoutMs());
    const saved = assertSavedCodedGroup(builder, target, { outputId }, codedOracle, 'GROUP');
    assert.equal(saved.step.id.length > 0, true);
  }, 'Apply saves standalone CODED_GROUP and exact source counts within five seconds');
  const savedAfterApply = await readBuilder(page, target, explorerId, ACTION_BUDGET_MS);
  const applied = assertSavedCodedGroup(savedAfterApply, target, { outputId }, codedOracle, 'GROUP');
  const stepId = applied.step.id;
  report.target.codedGroup = {
    outputId, stepId, codingPath: CODING_PATH, missingKeyPolicy: 'GROUP',
    groups: codedOracle.groups,
  };
  report.target.qaIsolation.workflowMutations.push('native UI applied direct-root CODED_GROUP after exact Preview and Cancel');
  check('persistence', 'Apply saves exact standalone CODED_GROUP path, root binding, outputs, and three source counts', true, {
    outputId, stepId, codingPath: CODING_PATH, missingKeyPolicy: 'GROUP', groups: codedOracle.groups,
  });

  const rootTab = page.getByTestId(`construction-table-${outputId}`);
  await nativeAction('reload Builder with applied coded-group result', rootTab,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS }), async timeoutMs => {
    await assertCodedGroupRows(page, outputId, codedOracle, 'Reloaded applied CODED_GROUP result', timeoutMs());
    const builder = await readBuilder(page, target, explorerId, timeoutMs());
    const saved = assertSavedCodedGroup(builder, target, { outputId }, codedOracle, 'GROUP');
    assert.equal(saved.step.id, stepId, 'Reload must retain the same coded-group construction identity');
  }, 'reload restores the saved direct-code group rows, root, title, and step identity within five seconds');
  check('persistence', 'reload retains the exact saved CODED_GROUP root, title, coding path, and grouped rows', true, {
    outputId, stepId, codingPath: CODING_PATH, groups: codedOracle.groups,
  });

  await click('open saved row operations for native coded-group edit', rowSettings,
    timeoutMs => page.getByTestId('construction-row-operation-history').waitFor({ state: 'visible', timeout: timeoutMs() }));
  const editStep = page.getByTestId(`construction-row-edit-${stepId}`);
  await click('edit the saved native CODED_GROUP step', editStep, async timeoutMs => {
    const editor = page.getByTestId('construction-reshape-coded-group');
    await editor.waitFor({ state: 'visible', timeout: timeoutMs() });
    assert.equal(await page.getByTestId('construction-coded-group-path').inputValue(), CODING_PATH,
      'Native edit must reopen the exact direct Observation Coding path');
    assert.equal(await page.getByTestId('construction-coded-group-missing').inputValue(), 'GROUP',
      'Native edit must reopen the saved GROUP missing-code policy');
    await assertCodedGroupRows(page, outputId, codedOracle, 'Unchanged coded-group saved-step preview', timeoutMs(), false, true);
  }, 'native edit reopens the saved CODED_GROUP and exact rows within five seconds');

  const advancedSummary = page.locator('[data-testid="construction-reshape-coded-group"] summary');
  await click('open native missing-code policy options', advancedSummary,
    timeoutMs => waitForDOM(page, () => document.querySelector('[data-testid="construction-reshape-coded-group"] details')?.open === true,
      undefined, timeoutMs()));
  const missingPolicy = page.getByTestId('construction-coded-group-missing');
  await select('edit missing-code policy to EXCLUDE using the native control', missingPolicy, 'EXCLUDE',
    timeoutMs => assertCodedGroupRows(page, outputId, codedOracle, 'Edited EXCLUDE coded-group proposal', timeoutMs(), true),
    'native CODED_GROUP edit previews exact existing tuples after changing missing-code policy within five seconds');
  await click('Apply native coded-group edit', page.getByTestId('construction-apply-proposal'), async timeoutMs => {
    await assertCodedGroupRows(page, outputId, codedOracle, 'Applied edited CODED_GROUP result', timeoutMs());
    const builder = await readBuilder(page, target, explorerId, timeoutMs());
    const saved = assertSavedCodedGroup(builder, target, { outputId }, codedOracle, 'EXCLUDE');
    assert.equal(saved.step.id, stepId, 'Editing CODED_GROUP must retain its saved construction identity');
  }, 'native CODED_GROUP edit saves EXCLUDE and exact source counts within five seconds');
  const editedBuilder = await readBuilder(page, target, explorerId, ACTION_BUDGET_MS);
  assertSavedCodedGroup(editedBuilder, target, { outputId }, codedOracle, 'EXCLUDE');
  report.target.codedGroup.missingKeyPolicy = 'EXCLUDE';
  report.target.qaIsolation.workflowMutations.push('native UI edited CODED_GROUP missing-code policy from GROUP to EXCLUDE');
  check('persistence', 'native CODED_GROUP edit saves EXCLUDE without changing exact populated-code groups', true, {
    outputId, stepId, missingKeyPolicy: 'EXCLUDE', groups: codedOracle.groups,
  });

  await nativeAction('reload Builder after native coded-group edit', rootTab,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS }), async timeoutMs => {
    await assertCodedGroupRows(page, outputId, codedOracle, 'Reloaded edited CODED_GROUP result', timeoutMs());
    const builder = await readBuilder(page, target, explorerId, timeoutMs());
    const saved = assertSavedCodedGroup(builder, target, { outputId }, codedOracle, 'EXCLUDE');
    assert.equal(saved.step.id, stepId, 'Reload after edit must retain the exact CODED_GROUP identity');
  }, 'reload retains native CODED_GROUP edit and exact grouped source rows within five seconds');
  check('persistence', 'reload retains the edited missing-code policy and unchanged literal code groups', true, {
    outputId, stepId, missingKeyPolicy: 'EXCLUDE', groups: codedOracle.groups,
  });

  await click('open saved row operations for native coded-group removal', rowSettings,
    timeoutMs => page.getByTestId('construction-row-operation-history').waitFor({ state: 'visible', timeout: timeoutMs() }));
  const removeStep = page.getByTestId(`construction-row-remove-${stepId}`);
  await click('propose native CODED_GROUP removal', removeStep, async timeoutMs => {
    await assertRootRows(page, outputId, identityOracle, 'CODED_GROUP removal Preview', timeoutMs(), true);
    const summary = page.getByTestId('construction-removal-summary');
    await summary.waitFor({ state: 'visible', timeout: timeoutMs() });
    const builder = await readBuilder(page, target, explorerId, timeoutMs());
    assertSavedCodedGroup(builder, target, { outputId }, codedOracle, 'EXCLUDE');
  }, 'removal Preview restores all exact source rows while keeping the saved CODED_GROUP until Apply within five seconds');
  check('correctness', 'CODED_GROUP removal Preview restores all six source IDs and remains unsaved before Apply', true, {
    outputId, sourceIDs: identityOracle.ids, stepId,
  });

  await click('Apply native CODED_GROUP removal and restore Observation rows', page.getByTestId('construction-apply-proposal'), async timeoutMs => {
    const restored = await waitForRootAndReadSaved(timeoutMs);
    assert.deepEqual(restored.document.construction?.steps ?? [], [], 'Applying removal must restore the unshaped root document');
  }, 'Apply removal restores exact Observation rows and clears the saved coded-group construction within five seconds');
  check('persistence', 'Apply removal restores the exact direct Observation source table and clears CODED_GROUP', true, {
    outputId, sourceIDs: identityOracle.ids,
  });

  await nativeAction('reload Builder after native coded-group removal', rootTab,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS }), async timeoutMs => {
    const restored = await waitForRootAndReadSaved(timeoutMs);
    assert.equal(restored.document.output.title, ROOT_TABLE_TITLE);
    assert.equal(restored.document.rootResourceType, 'Observation');
  }, 'reload retains the restored Observation title, root binding, and exact source rows within five seconds');
  check('persistence', 'final reload retains the exact Basic Observation source IDs with no saved CODED_GROUP', true, {
    outputId, sourceIDs: identityOracle.ids, sourceSHA256: codedOracle.sha256,
  });

  const finalOracle = await readBasicCodedGroupRecordedCodeOracle({
    path: sourcePath, project: target.fixtureProject, generation: target.fixtureGeneration,
  });
  assert.equal(finalOracle.sha256, codedOracle.sha256, 'Basic Observation source bytes must stay unchanged throughout the lifecycle');
  check('correctness', 'Basic Observation raw fixture bytes remain unchanged through configure, edit, removal, and reload',
    finalOracle.sha256 === codedOracle.sha256,
    { path: sourcePath, sha256Before: codedOracle.sha256, sha256After: finalOracle.sha256 });
};
