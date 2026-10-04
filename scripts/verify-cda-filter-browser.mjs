import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { assertOwnedTarget, browserEval, click, fill, launchBrowser, navigate, selectOption, waitForBrowser, captureRequests, waitForCapturedResponse, includeBrowserDiagnostics } from './lib/cda-playwright.mjs';

const savedOperator = process.env.LOOM_SAVED_FILTER_OPERATOR;
assert(!savedOperator || ['NOT_EQUALS', 'IN', 'CONTAINS_TEXT', 'GT'].includes(savedOperator), 'Saved operator regression covers scalar inequality, list membership, text matching and numeric comparison');
const numeric = savedOperator === 'GT';
const booleanCase = process.env.LOOM_FILTER_VALUE_TYPE === 'BOOLEAN';
const integerGroup = process.env.LOOM_FILTER_VALUE_TYPE === 'INTEGER_GROUP';
const upstreamGroupEdit = process.env.LOOM_GROUP_FILTER_UPSTREAM_EDIT === '1';
assert(!upstreamGroupEdit || integerGroup, 'Upstream Group edit requires INTEGER_GROUP');
assert(!process.env.LOOM_FILTER_VALUE_TYPE || booleanCase || integerGroup, 'Typed fixture mode supports BOOLEAN or INTEGER_GROUP');
assert(!integerGroup || numeric, 'Integer grouping lifecycle starts with GT');
assert(!booleanCase || savedOperator === 'NOT_EQUALS', 'Boolean lifecycle starts with NOT_EQUALS');
const resourceType = numeric ? 'Observation' : booleanCase ? 'Substance' : 'Specimen';
const fieldPath = numeric ? 'valueQuantity.value' : booleanCase ? 'instance' : 'id';
const typedValue = value => integerGroup ? { kind: 'INTEGER', integer: value } : numeric ? { kind: 'DECIMAL', decimal: value } : booleanCase ? { kind: 'BOOLEAN', boolean: value } : { kind: 'STRING', string: value };
const project = process.env.LOOM_CDA_PROJECT;
const explorer = `filter-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-filter-browser-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN?.replace(/\/$/, '');
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN?.replace(/\/$/, '');
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { savedOperator, booleanCase, integerGroup, upstreamGroupEdit, explorer, cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let browser, builder, outputId, savedValues, sourceValue, sourceCell, sourceRow, filterColumnId, browserEvents;
const valueSelector = label => `[data-testid="construction-filter-editor"] ${booleanCase ? 'select' : 'input'}[aria-label="${label}"]`;
const setFilterValue = async (label, value) => {
  const selector = valueSelector(label);
  await waitForBrowser(browser.page, `document.querySelector(${JSON.stringify(selector + ':not(:disabled)')})`);
  if (booleanCase) {
    await selectOption(browser.page, selector, String(value));
  } else {
    await fill(browser.page, selector, String(value));
  }
};
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `filter-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const proposal = async (name, start, expectedRows, expectedFilter) => {
  const matchesFilter = request => !expectedFilter || request.body?.candidateConstruction?.steps?.some(step =>
    step.operation?.kind === 'FILTER' && step.operation.filter.operator === expectedFilter.operator &&
    JSON.stringify(step.operation.filter.values) === JSON.stringify(expectedFilter.values));
  const predicate = request => request.path === base + '/construction-proposals' &&
    request.startedAt >= start && request.response && matchesFilter(request);
  const response = report.nativeRequests.findLast(predicate) ??
    await waitForCapturedResponse(browser.page, browserEvents, predicate, Math.max(1, start + 5000 - Date.now()));
  if(response.status===200 && response.response.proposalId) {
    await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId===${JSON.stringify(response.response.proposalId)}`);
  }
  await waitForBrowser(browser.page, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
  const result = await browserEval(browser.page, `const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};`);
  assert.equal(result.status, 'ready', result.text);
  assert.deepEqual(result.rows, expectedRows, 'Preview must match the independently selected CDA record');
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
};
const apply = async expectedRows => {
  const start = Date.now();
  await click(browser.page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  const previewPredicate = request => request.path === base + '/preview' && request.startedAt >= start && request.status === 200;
  report.nativeRequests.findLast(previewPredicate) ??
    await waitForCapturedResponse(browser.page, browserEvents, previewPredicate, Math.max(1, start + 5000 - Date.now()));
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async expectedRows => {
  const start = Date.now();
  await navigate(browser.page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await rendered(expectedRows);
  recordRender('load-to-render', start);
};
const rendered = async expectedRows => {
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(expectedRows.length + 1))} && !document.body.innerText.includes('Loading your table…')`);
  const rows = await browserEval(browser.page, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);`);
  assert.deepEqual(rows, expectedRows, 'Saved rendered rows must match the CDA oracle');
};
try {
  report.target = await assertOwnedTarget({ project, apiOrigin, uiOrigin });
  const query = `FOR s IN ${resourceType} FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" ${numeric ? 'FILTER IS_NUMBER(s.payload.valueQuantity.value)' : booleanCase ? 'FILTER IS_BOOL(s.payload.instance)' : ''} LIMIT 1 RETURN {id:s.id,generation:s.dataset_generation,value:${numeric ? 's.payload.valueQuantity.value' : booleanCase ? 's.payload.instance' : 's.id'}}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.id);
  sourceValue = source.value;
  if (numeric) assert(Number.isFinite(sourceValue) && sourceValue - 1 < sourceValue);
  if (booleanCase) assert.equal(typeof sourceValue, 'boolean');
  sourceCell = String(sourceValue);
  sourceRow = numeric || booleanCase ? [sourceCell, source.id] : [sourceCell];
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Filter browser lifecycle QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === resourceType);
  await command([{ type: 'CREATE_TABLE', title: 'Filter lifecycle QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === fieldPath);
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: numeric ? 'Quantity value' : booleanCase ? 'Is an instance' : 'Specimen ID' }]);
  if (numeric || booleanCase) {
    const identity = builder.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
    assert(identity);
    await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: identity.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'FHIR resource ID' }]);
  }
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: source.generation, resourceType, id: source.id }] } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  let baseline = builder;
  browser = await launchBrowser(evidence);
  browserEvents = captureRequests(browser, report, base, { apiOrigin, uiOrigin });
  await open([sourceRow]);
  let start;
  if (integerGroup) {
    const sourceBaseline = builder;
    const groupedRows = [[source.id, '1']];
    const configureGroup = async name => {
      const started = Date.now();
      await click(browser.page, '[data-testid="construction-rows-settings-trigger"]');
      await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-action-group-rows"]:not(:disabled)')`, 5000);
      await click(browser.page, '[data-testid="construction-action-group-rows"]');
      await waitForBrowser(browser.page, `document.querySelector('input[aria-label="Group by FHIR resource ID"]:not(:disabled)')`);
      await click(browser.page, 'input[aria-label="Group by FHIR resource ID"]');
      await proposal(name, started, groupedRows);
    };
    await configureGroup('integer-count-group-preview');
    await click(browser.page, '[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    assert.deepEqual((await api(base + '/builder')).workspace, sourceBaseline.workspace);
    await rendered([sourceRow]);
    await configureGroup('confirmed-integer-count-group-preview');
    await apply(groupedRows);
    await open(groupedRows);
    baseline = builder;
    sourceValue = 1;
    sourceCell = '1';
    sourceRow = groupedRows[0];
    report.groupOracle = { memberIds: [source.id], groupedRows, expectedCount: 1 };
  }
  let expectedSteps = doc(baseline).construction?.steps ?? [];
  if (savedOperator) {
    const capabilities = await api(base + '/construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId, stageId: integerGroup ? doc(builder).construction.steps.at(-1).id : 'source_projection',
    });
    const sourceColumn = integerGroup ? capabilities.selectedStage.columns.find(column => column.type === 'integer')
      : capabilities.selectedStage.columns.find(column => column.name === doc(builder).columns[0].name) ?? capabilities.selectedStage.columns[0];
    assert(sourceColumn);
    filterColumnId = sourceColumn.id;
    if (integerGroup) assert.equal(sourceColumn.type, 'integer');
    savedValues = savedOperator === 'NOT_EQUALS' || numeric ? [sourceValue] : savedOperator === 'IN'
      ? [`__loom_absent_${randomUUID()}`, `__loom_absent_${randomUUID()}`] : [`__loom_absent_${randomUUID()}`];
    assert(numeric || savedOperator === 'NOT_EQUALS' || savedValues.every(value => !source.id.includes(value)));
    const step = { id: 'saved-value-filter', inputs: integerGroup ? [{ kind: 'STEP_OUTPUT', stepId: capabilities.selectedStage.id }] : [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: { columnId: sourceColumn.id, operator: savedOperator,
        values: savedValues.map(typedValue) } }, outputs: capabilities.selectedStage.columns.map(column =>
          ({ id: column.id, name: column.name, label: column.label, type: column.type, nullable: column.nullable })) };
    const seeded = await api(base + '/construction-proposals', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId, changedStepId: step.id,
      candidateConstruction: { version: 1, steps: [...(doc(builder).construction?.steps ?? []), step] }, limit: 25,
    });
    assert.deepEqual(seeded.preview.rows, [], `${savedOperator} seed must exclude the sole independently selected ID`);
    await command([{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: seeded.proposalId }]);
  } else {
  await rendered([sourceRow]);
  start = Date.now();
  await click(browser.page, '[data-testid="construction-action-keep-rows"]');
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')`);
  await selectOption(browser.page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'MISSING');
  await proposal('missing-ID-preview', start, []);
  assert.equal((await api(base + '/builder')).draftDigest, baseline.draftDigest);
  await click(browser.page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  assert.deepEqual((await api(base + '/builder')).workspace, baseline.workspace, 'Cancel must leave the source table unchanged');
  await click(browser.page, '[data-testid="construction-action-keep-rows"]');
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')`);
  // Toggle away and back so a canceled proposal is recreated even if the editor retained its form.
  await selectOption(browser.page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
  start = Date.now();
  await selectOption(browser.page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'MISSING');
  await proposal('confirmed-missing-ID-preview', start, []);
  await apply([]);
  }
  const filtered = builder;
  const filter = doc(builder).construction.steps.find(s => s.operation.kind === 'FILTER');
  assert(filter);
  await open([]);
  await rendered([]);
  assert.equal((await api(base + '/builder')).draftDigest, filtered.draftDigest);
  await click(browser.page, `[data-testid="construction-history-step-${filter.id}"]`);
  const editControl = await browserEval(browser.page, `const button=document.querySelector('[data-testid="construction-edit-step-${filter.id}"]');return {present:Boolean(button),disabled:button?.disabled,text:button?.innerText,history:document.querySelector('[data-testid="construction-history-step-${filter.id}"]')?.innerText};`);
  report.savedFilterEditControl = editControl;
  assert(editControl.present && !editControl.disabled, 'A backend-supported saved filter must remain editable: '+JSON.stringify(editControl));
  await click(browser.page, `[data-testid="construction-edit-step-${filter.id}"]`);
  await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')`);
  if(savedOperator) {
    const restoredOperator=await browserEval(browser.page, `return document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]').value;`);
    assert.equal(restoredOperator,savedOperator,'The edit form must preserve the saved operator');
    for (const [index, expected] of savedValues.entries()) {
      const label = savedOperator === 'IN' ? `Value ${index + 1}` : 'Value';
      const actual = await browserEval(browser.page, `return document.querySelector(${JSON.stringify(valueSelector(label))})?.value;`);
      assert.equal(actual, String(expected), 'Reopening must preserve each saved typed value');
    }
  }
  start = Date.now();
  await selectOption(browser.page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
  await setFilterValue('Value', sourceValue);
  await proposal('edit-equality-preview', start, [sourceRow]);
  if(savedOperator) {
    await click(browser.page,'[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(browser.page,`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    await rendered([]);
    assert.deepEqual((await api(base+'/builder')).workspace,filtered.workspace,`Cancel must retain the saved ${savedOperator} condition`);
    start=Date.now();
    await click(browser.page,`[data-testid="construction-history-step-${filter.id}"]`);
    await click(browser.page,`[data-testid="construction-edit-step-${filter.id}"]`);
    await waitForBrowser(browser.page,`document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')`);
    const canceledOperator=await browserEval(browser.page,`return document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]').value;`);
    assert.equal(canceledOperator,savedOperator);
    await selectOption(browser.page,'[data-testid="construction-filter-editor"] select[aria-label="Condition"]','EQUALS');
    await setFilterValue('Value', sourceValue);
    await proposal('confirmed-saved-operator-edit-equality',start,[sourceRow]);
  }
  await apply([sourceRow]);
  await open([sourceRow]);
  await rendered([sourceRow]);
  const savedFilter = doc(await api(base + '/builder')).construction.steps.find(s => s.id === filter.id);
  assert.equal(savedFilter.operation.filter.operator, 'EQUALS');
  await click(browser.page, `[data-testid="construction-history-step-${filter.id}"]`);
  start = Date.now();
  await click(browser.page, `[data-testid="construction-remove-step-${filter.id}"]`);
  await proposal('remove-filter-preview', start, [sourceRow]);
  await apply([sourceRow]);
  await open([sourceRow]);
  await rendered([sourceRow]);
  builder = await api(base + '/builder');
  assert.deepEqual(doc(builder).construction?.steps ?? [], expectedSteps);
  assert.deepEqual(doc(builder).population, doc(baseline).population);
  // Construction assigns a source-stage ID; preserve the authored binding and physical column name.
  const sourceColumns = columns => columns.map(({ columnId: stageId, ...column }) => column);
  assert.deepEqual(sourceColumns(doc(builder).columns), sourceColumns(doc(baseline).columns));
  if (savedOperator) {
    const restored = await api(base + '/builder');
    const nativeValues = numeric ? [sourceValue - 1] : booleanCase ? [!sourceValue] : savedOperator === 'IN' ? [savedValues[0], source.id]
      : savedOperator === 'CONTAINS_TEXT' ? [source.id.slice(0, Math.max(1, Math.floor(source.id.length / 2)))] : savedValues;
    const nativeRows = savedOperator === 'NOT_EQUALS' && !booleanCase ? [] : [sourceRow];
    const configureSavedOperator = async name => {
      const started = Date.now();
      await click(browser.page, '[data-testid="construction-action-keep-rows"]');
      await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')`);
      if (integerGroup) await selectOption(browser.page, '[data-testid="construction-filter-editor"] select[aria-label="Column"]', filterColumnId);
      await selectOption(browser.page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'MISSING');
      await selectOption(browser.page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', savedOperator);
      for (const [index, value] of nativeValues.entries()) {
        const label = savedOperator === 'IN' ? `Value ${index + 1}` : 'Value';
        const selector = valueSelector(label);
        if (index > 0 && !await browserEval(browser.page, `return Boolean(document.querySelector(${JSON.stringify(selector)}));`))
          await click(browser.page, '[data-testid="construction-filter-editor"] button[aria-label="Add another value"]');
        await setFilterValue(label, value);
      }
      await proposal(name, started, nativeRows, { operator: savedOperator, values: nativeValues.map(typedValue) });
    };
    await configureSavedOperator(`native-${savedOperator}-preview`);
    await click(browser.page, '[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
    assert.deepEqual((await api(base + '/builder')).workspace, restored.workspace);
    await configureSavedOperator(`confirmed-native-${savedOperator}-preview`);
    await apply(nativeRows);
    await open(nativeRows);
    const nativeStep = doc(await api(base + '/builder')).construction.steps.find(step => step.operation.kind === 'FILTER');
    assert(nativeStep);
    assert.equal(nativeStep.operation.filter.operator, savedOperator);
    assert.deepEqual(nativeStep.operation.filter.values, nativeValues.map(typedValue));
    if (numeric) {
      for (const [operator, expectedRows] of [['GTE', [sourceRow]], ['LT', []], ['LTE', [sourceRow]]]) {
        const beforeEdit = await api(base + '/builder');
        const configureBoundary = async name => {
          const started = Date.now();
          await click(browser.page, `[data-testid="construction-history-step-${nativeStep.id}"]`);
          await click(browser.page, `[data-testid="construction-edit-step-${nativeStep.id}"]`);
          await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')`);
          await selectOption(browser.page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', operator);
          await click(browser.page, '[data-testid="construction-filter-editor"] input[aria-label="Value"]');
          await fill(browser.page, '[data-testid="construction-filter-editor"] input[aria-label="Value"]', sourceCell);
          await fill(browser.page, '[data-testid="construction-filter-editor"] input[aria-label="Value"]', sourceCell);
          await proposal(name, started, expectedRows, { operator, values: [typedValue(sourceValue)] });
        };
        await configureBoundary(`numeric-${operator}-boundary-preview`);
        await click(browser.page, '[data-testid="construction-cancel-proposal"]');
        await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
        assert.deepEqual((await api(base + '/builder')).workspace, beforeEdit.workspace);
        await configureBoundary(`confirmed-numeric-${operator}-boundary-preview`);
        await apply(expectedRows);
        await open(expectedRows);
        const boundaryStep = doc(await api(base + '/builder')).construction.steps.find(step => step.id === nativeStep.id);
        assert.equal(boundaryStep.operation.filter.operator, operator);
        assert.deepEqual(boundaryStep.operation.filter.values, [typedValue(sourceValue)]);
      }
    }
    if (upstreamGroupEdit) {
      const beforeGroupEdit = await api(base + '/builder');
      const group = doc(beforeGroupEdit).construction.steps.find(step => step.operation.kind === 'GROUP');
      const preservedFilter = doc(beforeGroupEdit).construction.steps.find(step => step.id === nativeStep.id).operation.filter;
      const changeGroupingKey = async (name, includeId, expectedRows) => {
        const started = Date.now();
        await click(browser.page, `[data-testid="construction-history-step-${group.id}"]`);
        await click(browser.page, `[data-testid="construction-edit-step-${group.id}"]`);
        await waitForBrowser(browser.page, `document.querySelector('input[aria-label="Group by FHIR resource ID"]:not(:disabled)')`);
        const checked = await browserEval(browser.page, `return document.querySelector('input[aria-label="Group by FHIR resource ID"]').checked;`);
        assert.equal(checked, !includeId, 'Saved grouping key must reopen correctly');
        await click(browser.page, 'input[aria-label="Group by FHIR resource ID"]');
        await proposal(name, started, expectedRows, preservedFilter);
      };
      await changeGroupingKey('upstream-group-global-count-preview', false, [['1']]);
      await click(browser.page, '[data-testid="construction-cancel-proposal"]');
      await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
      assert.deepEqual((await api(base + '/builder')).workspace, beforeGroupEdit.workspace);
      await rendered([sourceRow]);
      await changeGroupingKey('confirmed-upstream-group-global-count-preview', false, [['1']]);
      await apply([['1']]);
      await open([['1']]);
      let edited = doc(await api(base + '/builder')).construction;
      assert.equal((edited.steps.find(step => step.id === group.id).operation.group.keys ?? []).length, 0);
      assert.deepEqual(edited.steps.find(step => step.id === nativeStep.id).operation.filter, preservedFilter);
      await changeGroupingKey('restore-upstream-group-identity-preview', true, [sourceRow]);
      await apply([sourceRow]);
      await open([sourceRow]);
      edited = doc(await api(base + '/builder')).construction;
      const restoredGroup = edited.steps.find(step => step.id === group.id);
      assert.deepEqual(restoredGroup.operation.group.keys.map(key => key.inputColumnId), group.operation.group.keys.map(key => key.inputColumnId));
      assert.deepEqual(restoredGroup.outputs.find(column => column.id === preservedFilter.columnId), group.outputs.find(column => column.id === preservedFilter.columnId), 'The surviving count column must retain its identity and schema');
      assert.deepEqual(edited.steps.find(step => step.id === nativeStep.id).operation.filter, preservedFilter);
      expectedSteps = edited.steps.filter(step => step.id !== nativeStep.id);
    }
    if (savedOperator === 'IN') {
      const listSaved = await api(base + '/builder');
      const removeSecondValue = async name => {
        await click(browser.page, `[data-testid="construction-history-step-${nativeStep.id}"]`);
        await click(browser.page, `[data-testid="construction-edit-step-${nativeStep.id}"]`);
        await waitForBrowser(browser.page, `document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value 2"]:not(:disabled)')`);
        assert.equal(await browserEval(browser.page, `return document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value 2"]').value;`), nativeValues[1]);
        const started = Date.now();
        await click(browser.page, '[data-testid="construction-filter-editor"] button[aria-label="Remove value 2"]');
        await proposal(name, started, [], { operator: 'IN', values: [{ kind: 'STRING', string: savedValues[0] }] });
      };
      await removeSecondValue('remove-membership-value-preview');
      await click(browser.page, '[data-testid="construction-cancel-proposal"]');
      await waitForBrowser(browser.page, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
      assert.deepEqual((await api(base + '/builder')).workspace, listSaved.workspace);
      await rendered(nativeRows);
      await removeSecondValue('confirmed-remove-membership-value-preview');
      await apply([]);
      await open([]);
      const editedList = doc(await api(base + '/builder')).construction.steps.find(step => step.id === nativeStep.id);
      assert.deepEqual(editedList.operation.filter.values, [{ kind: 'STRING', string: savedValues[0] }]);
    }
    await click(browser.page, `[data-testid="construction-history-step-${nativeStep.id}"]`);
    start = Date.now();
    await click(browser.page, `[data-testid="construction-remove-step-${nativeStep.id}"]`);
    await proposal(`remove-native-${savedOperator}-preview`, start, [sourceRow]);
    await apply([sourceRow]);
    await open([sourceRow]);
    const final = doc(await api(base + '/builder'));
    assert.deepEqual(final.construction?.steps ?? [], expectedSteps);
    assert.deepEqual(final.population, doc(baseline).population);
    assert.deepEqual(sourceColumns(final.columns), sourceColumns(doc(baseline).columns));
  }
  includeBrowserDiagnostics(browser, report);
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
} catch (error) {
  await browser?.captureFailure(error, { phase: 'filter', action: browser?.activeAction });
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  report.failureUI = browser ? await browserEval(browser.page, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  await browserEvents?.flush();
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(c => ({ name: c.name, durationMs: c.durationMs })), error: report.error }));
