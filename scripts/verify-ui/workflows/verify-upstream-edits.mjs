import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';

export async function verifyUpstreamEdits({ page, cda }) {
const values = {
  origin: cda.apiOrigin,
  project: cda.project,
  'related-seed': process.env.LOOM_CDA_UPSTREAM_RELATED_SEED ?? 'cda-builder-full-qa-1790440983382',
  'group-seed': process.env.LOOM_CDA_UPSTREAM_GROUP_SEED,
  evidence: cda.evidence,
  'ui-origin': cda.uiOrigin,
  'api-container': cda.target.apiContainer,
  'arango-container': cda.target.arangoContainer ?? process.env.LOOM_ARANGO_CONTAINER,
};
const started = new Date().toISOString();
const report = { started, cases: [], failures: [], requests: [], environment: {
  node: process.version, apiOrigin: values.origin, uiOrigin: values['ui-origin'],
  commit: spawnSync('rtk', ['proxy', 'git', 'rev-parse', 'HEAD'], { encoding: 'utf8' }).stdout?.trim(),
  workingTree: spawnSync('rtk', ['proxy', 'git', 'status', '--short'], { encoding: 'utf8' }).stdout?.trim(),
} };
const root = `/api/v1/projects/${encodeURIComponent(values.project)}/explorers`;
const stateTracker = { activeAction: undefined, actions: [] };
const waitForDOM = (predicate, args = {}, timeout = 5000) => cda.wait(predicate, args, Math.min(timeout, 5000));
const inspectDOM = (fn, args = {}) => cda.inspect(fn, args);
const navigatePage = (url) => cda.navigate(url);
const clickControl = async (selector, identity = {}) => {
  const label = `Click ${identity.name ?? identity.includes ?? selector}`;
  stateTracker.activeAction = { label, startedAt: Date.now() };
  try { return await cda.action(label, page.locator(selector), locator => locator.click(), { timeout: 5000 }); }
  finally { stateTracker.activeAction = undefined; }
};
const selectControl = (selector, value) => cda.selectOption(selector, value);
const browserRequests = cda.captureRequests(root, { responsePaths: /construction-proposals|builder|preview/ });
const api = async (path, body) => {
  const requestId = `upstream-edit-${randomUUID()}`;
  const record = { path, requestId, request: body };
  report.requests.push(record);
  const start = Date.now();
  const response = await fetch(values.origin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(30000),
  });
  record.status = response.status;
  record.response = await response.json();
  record.durationMs = Date.now() - start;
  assert(response.ok, `${response.status} ${JSON.stringify(record.response)}`);
  return record.response;
};
const identity = builder => ({ snapshotToken: builder.catalog.snapshotToken,
  expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest });
const apply = async (base, builder, outputId, proposal) => api(base + '/commands', {
  ...identity(builder), commandId: randomUUID(), semanticsVersion: builder.workspace.semanticsVersion,
  commands: [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: proposal.proposalId }],
});
const clone = async (seed, name) => {
  const explorer = `upstream-${name}-${Date.now()}`;
  await api(root, { name: explorer, title: `Upstream edit QA: ${name}`, ...(seed ? { sourceExplorerId: seed } : {}) });
  const base = `${root}/${encodeURIComponent(explorer)}/authoring/v2`;
  return { explorer, base, builder: await api(base + '/builder') };
};
const propose = (base, builder, outputId, construction, changedStepId) => api(base + '/construction-proposals', {
  ...identity(builder), outputId, changedStepId, candidateConstruction: construction, limit: 25,
});
const saveAppend = async (context, step) => {
  const document = context.builder.workspace.documents[0];
  const construction = document.construction ?? { version: 1, steps: [] };
  const proposal = await propose(context.base, context.builder, document.output.id,
    { ...construction, steps: [...construction.steps, step] }, step.id);
  assert.equal(proposal.previewStatus, 'READY');
  context.initialPreview = proposal.preview;
  await apply(context.base, context.builder, document.output.id, proposal);
  context.builder = await api(context.base + '/builder');
};
const filter = (id, columnId, outputs) => ({ id, inputs: [{ kind: 'SOURCE_PROJECTION' }],
  operation: { kind: 'FILTER', filter: { columnId, operator: 'EXISTS' } }, outputs });
const rawObservationRecords = (ids, generation) => {
  const result = spawnSync('rtk', ['proxy', 'docker', 'exec', values['arango-container'], 'arangosh',
    '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(${JSON.stringify(ids)}.map(id => db.Observation.byExample({id, project:${JSON.stringify(values.project)}, dataset_generation:${JSON.stringify(generation)}}).limit(1).toArray()[0]).map(d => d ? {id:d.id,specimen:d.payload.specimen} : null)))`],
  { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, `${result.stdout}${result.stderr}`);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf('[')));
};
const verifySourceValues = record => {
  if (record.name.startsWith('group')) {
    const [source] = rawObservationRecords(['485e2567-b566-56f3-b5bd-5f025f37cd95'], record.baseline.catalog.generation);
    assert.equal(source?.id, '485e2567-b566-56f3-b5bd-5f025f37cd95');
    assert.deepEqual(record.preview.rows.map(row => row.row_count), [1]);
    record.oracle = { source, expectedCount: 1 };
  } else if (record.name === 'related-target' && record.targetResourceType === 'Observation') {
    const document = record.saved.workspace.documents[0];
    const expansion = document.construction.steps[0];
    const relatedName = expansion.outputs.find(column => column.id === expansion.operation.relatedExpand.relatedRecordColumnId).name;
    const specimenName = document.columns.find(column => column.label === 'Specimen ID').column;
    const rows = record.preview.rows.filter(row => row[relatedName] !== null);
    assert(rows.length > 0, 'The real CDA preview must contain related Observations');
    const sources = rawObservationRecords(rows.map(row => row[relatedName]), record.baseline.catalog.generation);
    for (const [index, source] of sources.entries()) {
      assert.equal(source?.id, rows[index][relatedName]);
      assert.equal(source.specimen?.reference?.split('/').at(-1), rows[index][specimenName], 'Every previewed Observation must reference its parent Specimen in raw FHIR');
    }
    record.oracle = sources;
  }
};
const setupRelated = async name => {
  const context = await clone(values['related-seed'], name);
  const document = context.builder.workspace.documents[0];
  assert.equal(document.construction?.steps[0].operation.kind, 'RELATED_EXPAND');
  const last = document.construction.steps.at(-1);
  await saveAppend(context, filter('independent-root-filter', last.outputs[0].id, last.outputs));
  return context;
};
const setupGroup = async name => {
  const context = await clone(values['group-seed'], name);
  if (!values['group-seed']) {
    const command = async commands => {
      await api(context.base + '/commands', { ...identity(context.builder), commandId: randomUUID(),
        semanticsVersion: context.builder.workspace?.semanticsVersion ?? 10, commands });
      context.builder = await api(context.base + '/builder');
    };
    const node = context.builder.catalog.nodes.find(node => node.resourceType === 'Observation' && node.rowRootEligible);
    assert(node, 'The real CDA Observation collection must be loaded');
    await command([{ type: 'CREATE_TABLE', title: 'Observation', rootNodeId: node.nodeId }]);
    const outputId = context.builder.workspace.documents[0].output.id;
    const idField = context.builder.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
    assert(idField, 'The catalog must advertise Observation.id');
    await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' }]);
    const selection = await api(context.base.replace('/authoring/v2', '/selections'), {
      snapshotToken: context.builder.catalog.snapshotToken, idempotencyKey: context.explorer,
      source: { kind: 'resources', resources: { refs: [{ project: values.project,
        generation: context.builder.catalog.generation, resourceType: 'Observation', id: '485e2567-b566-56f3-b5bd-5f025f37cd95' }] } },
    });
    const routes = await api(context.base + '/population-routes', { snapshotToken: context.builder.catalog.snapshotToken,
      outputId, selectionRevisionId: selection.id, limit: 50 });
    const route = routes.choices.find(choice => choice.route.length === 0);
    assert(route, 'The selected Observation must attach directly to this table');
    await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: route.routeChoiceId }]);
  }
  const document = context.builder.workspace.documents[0];
  assert.equal(document.construction?.steps.length ?? 0, 0, 'Group seed must be a restored source table');
  const capabilities = await api(context.base + '/construction-capabilities', {
    ...identity(context.builder), outputId: document.output.id, stageId: 'source_projection',
  });
  const source = capabilities.selectedStage.columns[0];
  context.sourceColumnId = source.id;
  const startsWithoutKey = name === 'group-key-addition';
  const columns = [{ id: 'group-id', name: 'record_id', label: source.label, type: 'string' },
    { id: 'group-count', name: 'row_count', label: 'Row count', type: 'integer' }];
  await saveAppend(context, { id: 'group-records', inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: { kind: 'GROUP', group: { constructionId: 'group-records', missingKeyPolicy: 'GROUP',
      keys: startsWithoutKey ? [] : [{ inputColumnId: source.id, outputColumnId: 'group-id' }],
      aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'group-count' }] } }, outputs: startsWithoutKey ? columns.slice(1) : columns });
  if (!startsWithoutKey) await saveAppend(context, filter('dependent-id-filter', 'group-id', columns));
  await saveAppend(context, filter('independent-count-filter', 'group-count', startsWithoutKey ? columns.slice(1) : columns));
  assert.equal(context.initialPreview.rows.length, 1, 'The grouping fixture must stay bounded to one real CDA record');
  assert.equal(context.initialPreview.rows[0].row_count, 1);
  if (!startsWithoutKey) assert.equal(context.initialPreview.rows[0].record_id, '485e2567-b566-56f3-b5bd-5f025f37cd95');
  return context;
};
const verifyBrowserEdit = async (context, record) => {
  const state = { failures: [], incidentalErrors: [], proposals: [], requests: [], nativeRequests: [], errors: [] };
  record.browser = state;
  const tracker = { activeAction: undefined, actions: [] };
  const requests = browserRequests;
  state.nativeRequests = cda.report.nativeRequests;
  const stepId = record.expected.changedStepId;
  const stepSelector = `[data-testid="construction-history-step-${stepId}"]`;
  const url = `${values['ui-origin']}/?project=${encodeURIComponent(values.project)}&explorer=${encodeURIComponent(context.explorer)}&mode=builder`;
  const inspect = (fn, args) => inspectDOM(fn, args);
  const openEditor = async () => {
    await clickControl(stepSelector);
    await clickControl(`[data-testid="construction-edit-step-${stepId}"]`);
    await waitForDOM(({ related }) => related
      ? Boolean(document.querySelector('[data-testid="construction-related-expand-editor"]'))
      : Boolean(document.querySelector('input[aria-label="Group by Observation ID"]:not(:disabled)')),
    { related: record.name.startsWith('related') }, 5000);
  };
  const edit = async () => {
    const start = Date.now();
    const requestStart = state.nativeRequests.length;
    if (record.name === 'related-target') {
      await selectControl('[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]', record.targetResourceType);
      await waitForDOM(() => Boolean(document.querySelector('[data-testid="construction-related-expand-editor"] input[type="radio"]:not(:disabled)')), {}, 5000);
      await clickControl('[data-testid="construction-related-expand-editor"] input[type="radio"]');
    } else if (record.name === 'related-empty-policy') {
      await clickControl('[data-testid="construction-related-expand-advanced"] summary');
      await selectControl('[data-testid="construction-related-expand-advanced"] select:last-of-type', record.emptyPolicy);
    } else {
      await clickControl('input[aria-label="Group by Observation ID"]');
    }
    await waitForDOM(() => ['ready', 'error', 'needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status')), {}, 5000);
    state.durationMs = Date.now() - start;
    state.panel = await inspect(() => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return { status: panel?.getAttribute('data-proposal-status'), text: panel?.innerText,
        rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText)) };
    });
    assert.equal(state.panel.status, 'ready', state.panel.text);
    const proposalEntry = await requests.waitFor(entry => entry.path.endsWith('/construction-proposals') && entry.method === 'POST' && entry.body?.changedStepId === stepId, { fromIndex: requestStart, timeoutMs: 5000 });
    const proposalRequest = await cda.waitForCapturedResponse(requests, entry => entry === proposalEntry, 5000);
    state.proposal = proposalRequest.response;
    state.proposals.push(state.proposal);
    assert(state.proposal?.proposalId, 'The browser must issue a construction proposal');
    assert.deepEqual(state.proposal.dependencyImpact.removedStepIds ?? [], record.expected.removed);
    for (const id of record.expected.removed) assert(await inspect(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-removal-step-${id}"]` }), `The warning must identify ${id}`);
    if (record.expected.removed.length) assert(state.panel.text.includes('This edit also removes'));
    assert(state.durationMs <= 5000, `Browser edit took ${state.durationMs} ms`);
    if (record.name === 'group-key-removal') assert.deepEqual(state.panel.rows, [['1']], 'Whole-table summary must count the one selected real CDA record');
    else if (record.name === 'group-key-addition') assert.deepEqual(state.panel.rows, [['485e2567-b566-56f3-b5bd-5f025f37cd95', '1']]);
    else {
      assert.equal(state.panel.rows.length, state.proposal.preview.rows.length);
      const expansion = state.proposal.candidateConstruction.steps.find(step => step.id === stepId);
      const outputName = expansion.outputs.find(column => column.id === expansion.operation.relatedExpand.relatedRecordColumnId).name;
      for (const [index, row] of state.proposal.preview.rows.entries()) {
        if (row[outputName] !== null) assert(state.panel.rows[index].includes(row[outputName]), 'The rendered related identity must match its preview row');
      }
    }
  };
  try {
    state.browserVersion = page.context().browser()?.version();
    await navigatePage(url);
    const tableSelector = `[data-testid="construction-table-${record.outputId}"]`;
    await waitForDOM(({ selector }) => Boolean(document.querySelector(selector)), { selector: tableSelector }, 5000);
    await clickControl(tableSelector);
    await waitForDOM(({ selector }) => !document.body.innerText.includes('Loading your table') && document.querySelector(selector)?.disabled === false, { selector: stepSelector }, 5000);
    await openEditor();
    await edit();
    const beforeCancel = await api(context.base + '/builder');
    assert.equal(beforeCancel.draftDigest, record.baseline.draftDigest);
    await clickControl('[data-testid="construction-cancel-proposal"]');
    await waitForDOM(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), {}, 5000);
    assert.equal((await api(context.base + '/builder')).draftDigest, record.baseline.draftDigest);
    await openEditor();
    await edit();
    await waitForDOM(() => document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled === false, {}, 5000);
    await clickControl('[data-testid="construction-apply-proposal"]');
    await waitForDOM(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), {}, 5000);
    record.saved = await api(context.base + '/builder');
    assert.deepEqual(record.saved.workspace.documents[0].construction, state.proposal.candidateConstruction);
    await navigatePage(url);
    await waitForDOM(({ selector }) => Boolean(document.querySelector(selector)), { selector: tableSelector }, 5000);
    await clickControl(tableSelector);
    await waitForDOM(({ selector }) => !document.body.innerText.includes('Loading your table') && document.querySelector(selector)?.disabled === false, { selector: stepSelector }, 5000);
    const reloaded = await api(context.base + '/builder');
    assert.equal(reloaded.draftDigest, record.saved.draftDigest);
    for (const id of record.expected.removed) assert.equal(await inspect(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-history-step-${id}"]` }), false);
    assert.equal(await inspect(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-history-step-${record.saved.workspace.documents[0].construction.steps.at(-1).id}"]` }), true, 'The independent later filter must survive reload');
    await requests.flush();
    await cda.includeBrowserDiagnostics();
    state.diagnostics = cda.diagnostics;
    state.failures = [...state.diagnostics.console, ...state.diagnostics.pageErrors, ...state.diagnostics.networkFailures, ...state.diagnostics.httpFailures, ...state.errors];
    assert.deepEqual(state.failures, []);
  } catch (error) {
    state.failureCapture = { phase: 'upstream-edit-lifecycle', action: stateTracker.activeAction?.label, elapsedMs: stateTracker.activeAction ? Date.now() - stateTracker.activeAction.startedAt : undefined, state: { name: record.name, proposals: state.proposals } };
    throw error;
  } finally {
    state.body = await page.locator('body').innerText().catch(String);
    await requests.flush();
    if (!state.diagnostics) {
      await cda.includeBrowserDiagnostics();
      state.diagnostics = cda.diagnostics;
    }
    await cda.attachReport(`${record.name}-upstream-edit`, state);
  }
};

const cases = [
  { name: 'related-empty-policy', setup: setupRelated, edit: async (context, construction) => {
    const step = construction.steps[0];
    step.operation.relatedExpand.emptyPolicy = step.operation.relatedExpand.emptyPolicy === 'EXCLUDE' ? 'PRESERVE_PARENT' : 'EXCLUDE';
    context.emptyPolicy = step.operation.relatedExpand.emptyPolicy;
    return { changedStepId: step.id, removed: [] };
  } },
  { name: 'related-target', setup: setupRelated, edit: async (context, construction) => {
    const step = construction.steps[0];
    const targetResourceType = step.operation.relatedExpand.targetResourceType === 'Observation' ? 'Patient' : 'Observation';
    context.targetResourceType = targetResourceType;
    const choices = await api(context.base + '/related-expand-choices', {
      ...identity(context.builder), outputId: context.builder.workspace.documents[0].output.id,
      stageId: 'source_projection', anchorColumnId: step.operation.relatedExpand.anchorColumnId,
      targetResourceType, limit: 50,
    });
    const choice = choices.choices.sort((a, b) => a.route.length - b.route.length)[0];
    assert(choice, 'The real CDA catalog must have a supported alternate target');
    step.operation.relatedExpand = { ...step.operation.relatedExpand, choiceId: choice.choiceId,
      targetNodeId: choice.targetNodeId, targetResourceType, route: choice.route,
      contributorRule: { policy: 'ALL_MATCHES' } };
    delete step.operation.relatedExpand.contributorSource;
    delete step.operation.relatedExpand.contributorChoiceId;
    const output = step.outputs.find(column => column.id === step.operation.relatedExpand.relatedRecordColumnId);
    output.name = `related_${targetResourceType.toLowerCase()}_id`;
    output.label = `${targetResourceType} FHIR resource ID`;
    return { changedStepId: step.id, removed: construction.steps.slice(1, -1).map(item => item.id) };
  } },
  { name: 'group-key-removal', setup: setupGroup, edit: async (_context, construction) => {
    const step = construction.steps[0];
    step.operation.group.keys = [];
    step.outputs = step.outputs.filter(column => column.id !== 'group-id');
    return { changedStepId: step.id, removed: ['dependent-id-filter'] };
  } },
  { name: 'group-key-addition', setup: setupGroup, edit: async (context, construction) => {
    const step = construction.steps[0];
    step.operation.group.keys = [{ inputColumnId: context.sourceColumnId, outputColumnId: 'group-id' }];
    step.outputs.unshift({ id: 'group-id', name: 'record_id', label: 'Observation ID', type: 'string' });
    return { changedStepId: step.id, removed: [] };
  } },
];
for (const scenario of cases) {
  const record = { name: scenario.name };
  report.cases.push(record);
  try {
    const context = await scenario.setup(scenario.name);
    record.explorer = context.explorer;
    record.baseline = context.builder;
    record.baselinePreview = context.initialPreview;
    const document = context.builder.workspace.documents[0];
    record.outputId = document.output.id;
    const construction = structuredClone(document.construction);
    const expected = await scenario.edit(context, construction);
    record.expected = expected;
    record.targetResourceType = context.targetResourceType;
    record.emptyPolicy = context.emptyPolicy;
    const start = Date.now();
    record.proposal = await propose(context.base, context.builder, document.output.id, construction, expected.changedStepId);
    record.durationMs = Date.now() - start;
    const afterProposal = await api(context.base + '/builder');
    assert.equal(afterProposal.draftDigest, context.builder.draftDigest, 'An edit proposal must not save changes');
    assert.equal(afterProposal.draftVersion, context.builder.draftVersion);
    assert.equal(record.proposal.previewStatus, 'READY', 'Upstream edits must produce a valid preview with explicit dependent removals');
    assert.deepEqual(record.proposal.dependencyImpact.removedStepIds ?? [], expected.removed);
    assert.equal(record.proposal.candidateConstruction.steps.at(-1).id, scenario.name.startsWith('related') ? 'independent-root-filter' : 'independent-count-filter');
    assert(record.durationMs <= 5000, `Edit proposal took ${record.durationMs} ms`);
    await verifyBrowserEdit(context, record);
    const proposal = record.browser?.proposal ?? record.proposal;
    record.preview = await api(context.base + '/preview', { receiptId: proposal.proposalId, outputId: document.output.id, limit: 25 });
    assert.deepEqual(record.preview.rows, proposal.preview.rows, 'Saved query must match the previewed result');
    verifySourceValues(record);
  } catch (error) {
    record.failure = String(error.stack ?? error);
    report.failures.push({ name: scenario.name, error: record.failure });
  }
  await cda.attachReport(`${scenario.name}-upstream-edit`, record);
}
const logs = spawnSync('rtk', ['proxy', 'docker', 'logs', '--since', started, values['api-container']], { encoding: 'utf8', timeout: 30000, maxBuffer: 10000000 });
const ids = report.requests.map(request => request.requestId);
report.serverLogs = `${logs.stdout ?? ''}${logs.stderr ?? ''}`.split('\n').filter(line => ids.some(id => line.includes(id)));
if (logs.status !== 0) report.logCaptureError = logs.error?.message ?? `Docker logs exited ${logs.status}`;
await cda.attachReport('upstream-edits', report);
assert.deepEqual(report.failures, [], 'Upstream edit browser lifecycles must complete');
return report;
}
