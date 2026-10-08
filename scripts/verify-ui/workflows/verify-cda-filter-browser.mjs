import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';

export function buildFilterBrowserOracleQuery({ project, generation, numeric = false, booleanCase = false }) {
  assert.equal(typeof project, 'string', 'The raw filter oracle requires a project identity');
  assert(project.length > 0, 'The raw filter oracle requires a project identity');
  assert.equal(typeof generation, 'string', 'The raw filter oracle requires the pinned source generation');
  assert(generation.length > 0, 'The raw filter oracle requires the pinned source generation');
  assert(!(numeric && booleanCase), 'The raw filter oracle cannot select two typed resource modes');

  const resourceType = numeric ? 'Observation' : booleanCase ? 'Substance' : 'Specimen';
  const valuePath = numeric ? 's.payload.valueQuantity.value' : booleanCase ? 's.payload.instance' : 's.id';
  const typePredicate = numeric ? ` FILTER IS_NUMBER(${valuePath})`
    : booleanCase ? ` FILTER IS_BOOL(${valuePath})` : '';
  return `FOR s IN ${resourceType} FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}${typePredicate} LIMIT 1 RETURN {id:s.id,generation:s.dataset_generation,value:${valuePath}}`;
}

export function assertFilterBrowserDefaultMode(env = process.env) {
  assert(!env.LOOM_SAVED_FILTER_OPERATOR, 'The registered filter-lifecycle case must use its default filter operator');
  assert(!env.LOOM_FILTER_VALUE_TYPE, 'The registered filter-lifecycle case must use the default Specimen ID type');
  assert.notEqual(env.LOOM_GROUP_FILTER_UPSTREAM_EDIT, '1', 'The registered filter-lifecycle case must not enable upstream Group edit');
}

export async function filterBrowserWorkflow({ page, cda }) {
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
  const project = cda.project;
  const explorer = `filter-browser-${Date.now()}`;
  const evidence = cda.evidence;
  const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
  const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
  const root = `/api/v1/projects/${project}/explorers`;
  const base = `${root}/${explorer}/authoring/v2`;
  const report = cda.report;
  Object.assign(report, { savedOperator, booleanCase, integerGroup, upstreamGroupEdit, explorer, cases: [], errors: report.errors ?? [], requests: [], started: new Date().toISOString() });
  const lifecycle = report.lifecycle = { choice: null, proposal: null, cancel: null, apply: null, savedRows: null, reload: null, edit: null, restoration: null };
  let builder, outputId, savedValues, sourceValue, sourceCell, sourceRow, filterColumnId, browserEvents, fatal, lastRenderedRows;
  let lastProposedState;
  const valueSelector = label => `[data-testid="construction-filter-editor"] ${booleanCase ? 'select' : 'input'}[aria-label="${label}"]`;
  const recordFilterChoice = async () => {
    const editor = await cda.inspect(() => {
      const form = document.querySelector('[data-testid="construction-filter-editor"]');
      if (!form) return null;
      const column = form.querySelector('select[aria-label="Column"]');
      const condition = form.querySelector('select[aria-label="Condition"]');
      return {
        columnId: column?.value ?? null,
        columnDisabled: column?.disabled ?? true,
        condition: condition?.value ?? null,
        conditionDisabled: condition?.disabled ?? true,
        enabledConditions: [...(condition?.options ?? [])].filter(option => !option.disabled).map(option => option.value),
      };
    });
    assert(editor, 'Filter rows editor must remain visible while configuring the filter');
    assert(editor.columnId && !editor.columnDisabled, 'Filter rows must expose an enabled selected source column');
    assert(editor.condition && !editor.conditionDisabled, 'Filter rows must expose an enabled typed condition');
    assert(editor.enabledConditions.includes('EQUALS'), 'Filter rows must keep the equality condition available');
    lifecycle.choice ??= { status: 'passed', ...editor };
  };
  const setFilterValue = async (label, value) => {
    const selector = valueSelector(label);
    await cda.wait(([__arg0]) => Boolean(document.querySelector(__arg0)), [selector + ':not(:disabled)']);
    if (booleanCase) {
      await cda.selectOption(selector, String(value));
    } else {
      await cda.fill(selector, String(value));
    }
    await recordFilterChoice();
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
  const restoredSourceDocument = document => ({
    ...document,
    construction: document.construction ?? { version: 1, steps: [] },
  });
  const proposal = async (name, start, expectedRows, expectedFilter) => {
    const matchesFilter = request => !expectedFilter || request.body?.candidateConstruction?.steps?.some(step =>
      step.operation?.kind === 'FILTER' && step.operation.filter.operator === expectedFilter.operator &&
      JSON.stringify(step.operation.filter.values) === JSON.stringify(expectedFilter.values));
    const predicate = request => request.path === base + '/construction-proposals' &&
      request.startedAt >= start && request.response && matchesFilter(request);
    const response = report.nativeRequests.findLast(predicate) ??
      await cda.waitForCapturedResponse(browserEvents, predicate, Math.max(1, start + 5000 - Date.now()));
    if(response.status===200 && response.response.proposalId) {
      await cda.wait(([__arg0]) => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId===__arg0), [response.response.proposalId]);
    }
    await cda.wait(() => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)), []);
    const result = await cda.inspect(() => { const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))}; });
    assert.equal(result.status, 'ready', result.text);
    assert.deepEqual(result.rows, expectedRows, 'Preview must match the independently selected CDA record');
    lastProposedState = {
      construction: response.response?.candidateConstruction,
      digest: response.response?.candidateWorkspaceDigest,
    };
    assert(lastProposedState.construction, `${name} proposal must return its candidate construction`);
    assert(lastProposedState.digest, `${name} proposal must return its candidate workspace digest`);
    const durationMs = Date.now() - start;
    assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
    if (lifecycle.choice && !lifecycle.proposal) {
      lifecycle.proposal = { status: 'passed', name, durationMs, rows: result.rows, source: report.oracle.source };
    }
    report.cases.push({ name, durationMs, result });
  };
  const recordRender = (name, start) => {
    const durationMs = Date.now() - start;
    assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
    report.cases.push({ name, durationMs });
    return durationMs;
  };
  const apply = async expectedRows => {
    const start = Date.now();
    await cda.click('[data-testid="construction-apply-proposal"]');
    await cda.wait(() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
    const previewPredicate = request => request.path === base + '/preview' && request.startedAt >= start && request.status === 200;
    report.nativeRequests.findLast(previewPredicate) ??
      await cda.waitForCapturedResponse(browserEvents, previewPredicate, Math.max(1, start + 5000 - Date.now()));
    const actualRows = await rendered(expectedRows);
    lastRenderedRows = actualRows;
    assert(lastProposedState, 'Apply must follow a checked construction proposal');
    builder = await api(base + '/builder');
    assert.equal(builder.draftDigest, lastProposedState.digest, 'Applied Builder digest must equal the accepted proposal digest');
    assert.deepEqual(doc(builder)?.construction, lastProposedState.construction, 'Applied construction must exactly equal the accepted proposal');
    const appliedFilter = doc(builder)?.construction?.steps?.find(step => step.operation.kind === 'FILTER');
    const durationMs = recordRender('apply-to-persisted-state', start);
    if (appliedFilter) {
      lifecycle.apply = { status: 'passed', stepId: appliedFilter.id, draftVersion: builder.draftVersion,
        draftDigest: builder.draftDigest, construction: doc(builder).construction, expectedRows, actualRows, durationMs };
      lifecycle.savedRows = { status: 'passed', expectedRows, actualRows, source: report.oracle.source };
    }
  };
  const open = async (expectedRows, verifyReloaded = () => {}) => {
    const start = Date.now();
    const expectedDigest = builder?.draftDigest;
    const expectedConstruction = doc(builder)?.construction;
    assert(expectedDigest, 'Reload must start from a persisted Builder digest');
    await cda.navigate( `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
    await cda.wait(([__arg0]) => Boolean(document.querySelector('[data-testid="construction-table-'+String(__arg0)+'"]')), [outputId]);
    await cda.click(`[data-testid="construction-table-${outputId}"]`);
    await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false), []);
    const actualRows = await rendered(expectedRows);
    lastRenderedRows = actualRows;
    const reloaded = await api(base + '/builder');
    assert.equal(reloaded.draftDigest, expectedDigest, 'Reload must restore the exact saved Builder digest');
    assert.deepEqual(doc(reloaded)?.construction, expectedConstruction, 'Reload must restore the exact saved construction');
    const reloadEvidence = verifyReloaded(reloaded);
    const durationMs = recordRender(reloadEvidence?.name ?? 'reload-to-persisted-state', start);
    if (reloadEvidence?.restoration) lifecycle.restoration = { ...reloadEvidence.restoration, durationMs };
    const reloadedFilter = doc(reloaded)?.construction?.steps?.find(step => step.operation.kind === 'FILTER');
    if (reloadedFilter) {
      lifecycle.reload = { status: 'passed', stepId: reloadedFilter.id, draftVersion: reloaded.draftVersion,
        draftDigest: reloaded.draftDigest, construction: doc(reloaded).construction, expectedRows, actualRows, durationMs };
      builder = reloaded;
    }
  };
  const rendered = async expectedRows => {
    await cda.wait(([__arg0]) => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === __arg0 && !document.body.innerText.includes('Loading your table…')), [String(expectedRows.length + 1)]);
    const rows = await cda.inspect(() => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length); });
    assert.deepEqual(rows, expectedRows, 'Saved rendered rows must match the CDA oracle');
    return rows;
  };
  try {
    report.target = cda.target;
    const query = buildFilterBrowserOracleQuery({ project, generation: cda.generation, numeric, booleanCase });
    const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', cda.target.arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
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
    browserEvents = cda.captureRequests(base);
    await open([sourceRow]);
    let start;
    if (integerGroup) {
      const sourceBaseline = builder;
      const groupedRows = [[source.id, '1']];
      const configureGroup = async name => {
        const started = Date.now();
        await cda.click('[data-testid="construction-rows-settings-trigger"]');
        await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-action-group-rows"]:not(:disabled)')), [], 5000);
        await cda.click('[data-testid="construction-action-group-rows"]');
        await cda.wait(() => Boolean(document.querySelector('input[aria-label="Group by FHIR resource ID"]:not(:disabled)')), []);
        await cda.click('input[aria-label="Group by FHIR resource ID"]');
        await proposal(name, started, groupedRows);
      };
      await configureGroup('integer-count-group-preview');
      await cda.click('[data-testid="construction-cancel-proposal"]');
      await cda.wait(() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
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
    const expectedRestoredDocument = restoredSourceDocument(doc(baseline));
    let expectedSteps = expectedRestoredDocument.construction.steps;
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
    await cda.click('[data-testid="construction-action-keep-rows"]');
    await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')), []);
    await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'MISSING');
    await recordFilterChoice();
    lifecycle.choice.durationMs = recordRender('open-filter-editor', start);
    await proposal('missing-ID-preview', start, []);
    assert.equal((await api(base + '/builder')).draftDigest, baseline.draftDigest);
    const cancelStarted = Date.now();
    await cda.click('[data-testid="construction-cancel-proposal"]');
    await cda.wait(() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
    assert.deepEqual((await api(base + '/builder')).workspace, baseline.workspace, 'Cancel must leave the source table unchanged');
    const cancelledRows = await rendered([sourceRow]);
    const durationMs = recordRender('cancel-to-source-rows', cancelStarted);
    lifecycle.cancel = { status: 'passed', preservedDraftDigest: baseline.draftDigest, expectedRows: [sourceRow], actualRows: cancelledRows, durationMs };
    await cda.click('[data-testid="construction-action-keep-rows"]');
    await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')), []);
    // Toggle away and back so a canceled proposal is recreated even if the editor retained its form.
    await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
    start = Date.now();
    await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'MISSING');
    await proposal('confirmed-missing-ID-preview', start, []);
    await apply([]);
    }
    const filtered = builder;
    const filter = doc(builder).construction.steps.find(s => s.operation.kind === 'FILTER');
    assert(filter);
    await open([]);
    await rendered([]);
    assert.equal((await api(base + '/builder')).draftDigest, filtered.draftDigest);
    const editStarted = Date.now();
    await cda.click(`[data-testid="construction-history-step-${filter.id}"]`);
    const editControl = await cda.inspect(([stepId]) => {
      const button = document.querySelector(`[data-testid="construction-edit-step-${stepId}"]`);
      return { present: Boolean(button), disabled: button?.disabled, text: button?.innerText,
        history: document.querySelector(`[data-testid="construction-history-step-${stepId}"]`)?.innerText };
    }, [filter.id]);
    report.savedFilterEditControl = editControl;
    assert(editControl.present && !editControl.disabled, 'A backend-supported saved filter must remain editable: '+JSON.stringify(editControl));
    await cda.click(`[data-testid="construction-edit-step-${filter.id}"]`);
    await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')));
    const reopenedFilter = await cda.inspect(() => ({
      columnId: document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]')?.value,
      condition: document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]')?.value,
    }));
    assert.equal(reopenedFilter.columnId, filter.operation.filter.columnId, 'The edit form must preserve the saved source column');
    assert.equal(reopenedFilter.condition, filter.operation.filter.operator, 'The edit form must preserve the saved condition');
    if(savedOperator) {
      assert.equal(reopenedFilter.condition,savedOperator,'The edit form must preserve the saved operator');
      for (const [index, expected] of savedValues.entries()) {
        const label = savedOperator === 'IN' ? `Value ${index + 1}` : 'Value';
        const actual = await cda.inspect(([selector]) => document.querySelector(selector)?.value, [valueSelector(label)]);
        assert.equal(actual, String(expected), 'Reopening must preserve each saved typed value');
      }
    } else {
      assert.equal(reopenedFilter.condition, 'MISSING', 'The default saved filter must reopen with its exact missing-value condition');
    }
    const editControlsDurationMs = recordRender('edit-filter-to-controls', editStarted);
    start = Date.now();
    await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
    await setFilterValue('Value', sourceValue);
    await proposal('edit-equality-preview', start, [sourceRow]);
    if(savedOperator) {
      const cancelStarted = Date.now();
      await cda.click('[data-testid="construction-cancel-proposal"]');
      await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
      const cancelledRows = await rendered([]);
      assert.deepEqual((await api(base+'/builder')).workspace,filtered.workspace,`Cancel must retain the saved ${savedOperator} condition`);
      const durationMs = recordRender('cancel-to-saved-filter-rows', cancelStarted);
      lifecycle.cancel = { status: 'passed', preservedDraftDigest: filtered.draftDigest, expectedRows: [], actualRows: cancelledRows, durationMs };
      start=Date.now();
      await cda.click(`[data-testid="construction-history-step-${filter.id}"]`);
      await cda.click(`[data-testid="construction-edit-step-${filter.id}"]`);
      await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')));
      const canceledOperator = await cda.inspect(() => document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]').value);
      assert.equal(canceledOperator,savedOperator);
      await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]','EQUALS');
      await setFilterValue('Value', sourceValue);
      await proposal('confirmed-saved-operator-edit-equality',start,[sourceRow]);
    }
    await apply([sourceRow]);
    const editedApply = lifecycle.apply;
    await open([sourceRow]);
    await rendered([sourceRow]);
    const savedFilter = doc(await api(base + '/builder')).construction.steps.find(s => s.id === filter.id);
    assert.equal(savedFilter.operation.filter.operator, 'EQUALS');
    const editedReload = lifecycle.reload;
    assert(editedApply?.status === 'passed' && editedReload?.status === 'passed', 'Edited filter must Apply and reload before its lifecycle evidence passes');
    assert.deepEqual(editedApply.construction, lastProposedState.construction, 'Edited Apply must persist the exact accepted replacement proposal');
    assert.equal(editedApply.draftDigest, lastProposedState.digest, 'Edited Apply digest must equal its accepted replacement proposal');
    assert.deepEqual(editedApply.actualRows, [sourceRow], 'Edited Apply must render the exact oracle row');
    assert.deepEqual(editedReload.construction, editedApply.construction, 'Edited filter construction must survive a fresh Builder reload');
    assert.equal(editedReload.draftDigest, editedApply.draftDigest, 'Edited filter digest must survive a fresh Builder reload');
    assert.deepEqual(editedReload.actualRows, [sourceRow], 'Edited reload must render the exact oracle row');
    const editedReloadedFilter = doc(builder).construction.steps.find(step => step.id === filter.id);
    assert(editedReloadedFilter, 'The edited filter must remain after reload');
    assert.equal(editedReloadedFilter.operation.filter.operator, 'EQUALS');
    lifecycle.edit = {
      status: 'passed', stepId: filter.id, ...reopenedFilter, controlsDurationMs: editControlsDurationMs,
      proposal: { construction: lastProposedState.construction, candidateWorkspaceDigest: lastProposedState.digest },
      applied: { draftDigest: editedApply.draftDigest, durationMs: editedApply.durationMs,
        construction: editedApply.construction, rows: editedApply.actualRows },
      reloaded: { draftDigest: editedReload.draftDigest, durationMs: editedReload.durationMs,
        construction: editedReload.construction, rows: editedReload.actualRows },
    };
    await cda.click(`[data-testid="construction-history-step-${filter.id}"]`);
    start = Date.now();
    await cda.click(`[data-testid="construction-remove-step-${filter.id}"]`);
    await proposal('remove-filter-preview', start, [sourceRow]);
    await apply([sourceRow]);
    await open([sourceRow], restored => {
      assert.equal(restored.draftDigest, lastProposedState.digest, 'Removal reload must restore the accepted removal proposal digest');
      assert.deepEqual(doc(restored), expectedRestoredDocument,
        'Removal must restore the complete source document, allowing only nil construction to normalize to version 1 with zero steps');
      return { name: 'filter-removal-to-restored-state', restoration: {
        status: 'passed', baselineDraftDigest: baseline.draftDigest,
        removalCandidateDigest: lastProposedState.digest, restoredDraftDigest: restored.draftDigest,
        normalizedBaselineDocument: expectedRestoredDocument, restoredDocument: doc(restored),
        constructionSteps: doc(restored).construction.steps, population: doc(restored).population,
        columns: doc(restored).columns, rows: doc(restored).rows,
        expectedRows: [sourceRow], actualRows: lastRenderedRows,
      } };
    });
    if (savedOperator) {
      const restored = await api(base + '/builder');
      const nativeValues = numeric ? [sourceValue - 1] : booleanCase ? [!sourceValue] : savedOperator === 'IN' ? [savedValues[0], source.id]
        : savedOperator === 'CONTAINS_TEXT' ? [source.id.slice(0, Math.max(1, Math.floor(source.id.length / 2)))] : savedValues;
      const nativeRows = savedOperator === 'NOT_EQUALS' && !booleanCase ? [] : [sourceRow];
      const configureSavedOperator = async name => {
        const started = Date.now();
        await cda.click('[data-testid="construction-action-keep-rows"]');
        await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')));
        if (integerGroup) await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Column"]', filterColumnId);
        await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'MISSING');
        await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', savedOperator);
        for (const [index, value] of nativeValues.entries()) {
          const label = savedOperator === 'IN' ? `Value ${index + 1}` : 'Value';
          const selector = valueSelector(label);
          if (index > 0 && !await cda.inspect(([targetSelector]) => Boolean(document.querySelector(targetSelector)), [selector]))
            await cda.click('[data-testid="construction-filter-editor"] button[aria-label="Add another value"]');
          await setFilterValue(label, value);
        }
        await proposal(name, started, nativeRows, { operator: savedOperator, values: nativeValues.map(typedValue) });
      };
      await configureSavedOperator(`native-${savedOperator}-preview`);
      await cda.click('[data-testid="construction-cancel-proposal"]');
      await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
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
            await cda.click(`[data-testid="construction-history-step-${nativeStep.id}"]`);
            await cda.click(`[data-testid="construction-edit-step-${nativeStep.id}"]`);
            await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]:not(:disabled)')));
            await cda.selectOption('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', operator);
            await cda.click('[data-testid="construction-filter-editor"] input[aria-label="Value"]');
            await cda.fill('[data-testid="construction-filter-editor"] input[aria-label="Value"]', sourceCell);
            await cda.fill('[data-testid="construction-filter-editor"] input[aria-label="Value"]', sourceCell);
            await proposal(name, started, expectedRows, { operator, values: [typedValue(sourceValue)] });
          };
          await configureBoundary(`numeric-${operator}-boundary-preview`);
          await cda.click('[data-testid="construction-cancel-proposal"]');
          await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
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
          await cda.click(`[data-testid="construction-history-step-${group.id}"]`);
          await cda.click(`[data-testid="construction-edit-step-${group.id}"]`);
            await cda.wait(() => Boolean(document.querySelector('input[aria-label="Group by FHIR resource ID"]:not(:disabled)')));
          const checked = await cda.inspect(() => document.querySelector('input[aria-label="Group by FHIR resource ID"]').checked);
          assert.equal(checked, !includeId, 'Saved grouping key must reopen correctly');
          await cda.click('input[aria-label="Group by FHIR resource ID"]');
          await proposal(name, started, expectedRows, preservedFilter);
        };
        await changeGroupingKey('upstream-group-global-count-preview', false, [['1']]);
        await cda.click('[data-testid="construction-cancel-proposal"]');
        await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
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
          await cda.click(`[data-testid="construction-history-step-${nativeStep.id}"]`);
          await cda.click(`[data-testid="construction-edit-step-${nativeStep.id}"]`);
          await cda.wait(() => Boolean(document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value 2"]:not(:disabled)')));
          assert.equal(await cda.inspect(() => document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value 2"]').value), nativeValues[1]);
          const started = Date.now();
          await cda.click('[data-testid="construction-filter-editor"] button[aria-label="Remove value 2"]');
          await proposal(name, started, [], { operator: 'IN', values: [{ kind: 'STRING', string: savedValues[0] }] });
        };
        await removeSecondValue('remove-membership-value-preview');
        await cda.click('[data-testid="construction-cancel-proposal"]');
        await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
        assert.deepEqual((await api(base + '/builder')).workspace, listSaved.workspace);
        await rendered(nativeRows);
        await removeSecondValue('confirmed-remove-membership-value-preview');
        await apply([]);
        await open([]);
        const editedList = doc(await api(base + '/builder')).construction.steps.find(step => step.id === nativeStep.id);
        assert.deepEqual(editedList.operation.filter.values, [{ kind: 'STRING', string: savedValues[0] }]);
      }
      await cda.click(`[data-testid="construction-history-step-${nativeStep.id}"]`);
      start = Date.now();
      await cda.click(`[data-testid="construction-remove-step-${nativeStep.id}"]`);
      await proposal(`remove-native-${savedOperator}-preview`, start, [sourceRow]);
      await apply([sourceRow]);
      await open([sourceRow]);
      const final = doc(await api(base + '/builder'));
      assert.deepEqual(final.construction?.steps ?? [], expectedSteps);
      assert.deepEqual(final.population, doc(baseline).population);
      assert.deepEqual(final.columns, doc(baseline).columns, 'Native filter removal must preserve every source column field, including its stable ID');
    }
    const requiredChecks = [
      ['choice', 'usability', 'native Filter rows controls expose an enabled source column and typed condition'],
      ['proposal', 'correctness', 'filter proposals and rendered result values match an independent scoped CDA source oracle within five seconds'],
      ['cancel', 'persistence', 'Cancel preserves the exact pre-proposal construction and rendered rows within five seconds'],
      ['apply', 'persistence', 'Apply persists the filter construction and exact result rows within five seconds'],
      ['savedRows', 'correctness', 'saved filter rows match the independent scoped CDA oracle'],
      ['reload', 'persistence', 'reload restores the saved filter and exact rendered rows'],
      ['edit', 'persistence', 'edit reopens the exact saved column and condition before applying a replacement within five seconds'],
      ['restoration', 'persistence', 'filter removal restores the exact source columns, population, and rows after reload'],
    ];
    for (const [phase, dimension, name] of requiredChecks) {
      assert(lifecycle[phase]?.status === 'passed', `Filter lifecycle phase ${phase} did not complete`);
      cda.check(dimension, name, true, lifecycle[phase]);
    }
    cda.includeBrowserDiagnostics();
    assert.deepEqual(report.errors, []);
    report.status = 'passed';
  } catch (error) {
    report.failureEvidence = cda.evidence;
    report.status = 'failed'; report.error = String(error.stack ?? error); fatal = error;
    report.failureUI = await cda.inspect(() => document.body.innerText).catch(String);
  } finally {
    await browserEvents?.flush();
    report.finished = new Date().toISOString();
    cda.includeBrowserDiagnostics();
    await cda.attachReport('filter-browser', report);
  }
  if (fatal) throw fatal;
}
