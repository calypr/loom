import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

export async function verifyBaseSettings({ page, cda }) {
const values = {
  origin: cda.apiOrigin,
  'ui-origin': cda.uiOrigin,
  project: cda.project,
  'related-seed': process.env.LOOM_CDA_BASE_SETTINGS_RELATED_SEED ?? 'upstream-related-empty-policy-1790867613516',
  evidence: cda.evidence,
  browser: true,
  'browser-cases': process.env.LOOM_CDA_BASE_SETTINGS_BROWSER_CASES,
  'arango-container': cda.target.arangoContainer ?? process.env.LOOM_ARANGO_CONTAINER,
  'api-container': cda.target.apiContainer,
  'compose-project': cda.target.composeProject,
};
const report = { started: new Date().toISOString(), cases: [], failures: [], requests: [], coverage: [], environment: {
  node: process.version, apiOrigin: values.origin, uiOrigin: values['ui-origin'], project: values.project,
} };
const browserCases = [];
const root = `/api/v1/projects/${encodeURIComponent(values.project)}/explorers`;
const sourceID = '485e2567-b566-56f3-b5bd-5f025f37cd95';
await mkdir(values.evidence, { recursive: true });
const requestTracker = cda.captureRequests(root, { responsePaths: /row-definition-proposals|builder|preview/, browserRequestOrigin: values['ui-origin'] });
const api = async (path, body, allowFailure = false) => {
  const entry = { path, request: body, requestId: `base-settings-${randomUUID()}` };
  report.requests.push(entry);
  const start = Date.now();
  const response = await fetch(values.origin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': entry.requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(30000),
  });
  entry.status = response.status;
  entry.body = await response.json();
  entry.durationMs = Date.now() - start;
  if (!allowFailure) assert(response.ok, `${entry.status}: ${JSON.stringify(entry.body)}`);
  return entry;
};
const identity = b => ({ snapshotToken: b.catalog.snapshotToken, expectedDraftVersion: b.draftVersion, expectedDraftDigest: b.draftDigest });
const create = async (name, seed) => {
  const explorer = `base-settings-${name}-${Date.now()}`;
  await api(root, { name: explorer, title: `Base settings QA: ${name}`, ...(seed ? { sourceExplorerId: seed } : {}) });
  const base = `${root}/${explorer}/authoring/v2`;
  return { name, explorer, base, builder: (await api(base + '/builder')).body };
};
const command = async (ctx, commands) => {
  await api(ctx.base + '/commands', { ...identity(ctx.builder), commandId: randomUUID(), semanticsVersion: ctx.builder.workspace?.semanticsVersion ?? 10, commands });
  ctx.builder = (await api(ctx.base + '/builder')).body;
};
const choices = async ctx => (await api(ctx.base + '/row-definition-choices?' + new URLSearchParams({ snapshotToken: ctx.builder.catalog.snapshotToken, outputId: ctx.builder.workspace.documents[0].output.id }))).body;
const fixture = async () => {
  const ctx = await create('source');
  const node = ctx.builder.catalog.nodes.find(n => n.resourceType === 'Observation');
  assert(node, 'CDA Observation must be loaded');
  await command(ctx, [{ type: 'CREATE_TABLE', title: 'Observation', rootNodeId: node.nodeId }]);
  const outputId = ctx.builder.workspace.documents[0].output.id;
  const field = ctx.builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  assert(field);
  await command(ctx, [{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' }]);
  const selectionPath = ctx.base.replace('/authoring/v2', '/selections');
  const selection = (await api(selectionPath, { snapshotToken: ctx.builder.catalog.snapshotToken, idempotencyKey: ctx.explorer, source: { kind: 'resources', resources: { refs: [{ project: values.project, generation: ctx.builder.catalog.generation, resourceType: 'Observation', id: sourceID }] } } })).body;
  const routes = (await api(ctx.base + '/population-routes', { snapshotToken: ctx.builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 })).body;
  const route = routes.choices.find(c => c.route.length === 0);
  assert(route);
  await command(ctx, [{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: route.routeChoiceId }]);
  const page = (await api(selectionPath + '/' + selection.id + '?limit=100')).body;
  assert.equal(page.members.length, 1);
  ctx.group = (await api(selectionPath + '/' + selection.id + '/explicit-groups', { snapshotToken: ctx.builder.catalog.snapshotToken, idempotencyKey: randomUUID(), groups: [{ id: 'qa-cohort', label: 'QA cohort', ordinal: 0, memberIds: [page.members[0].memberKey] }] })).body;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', values['arango-container'], 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db.Observation.byExample({id:${JSON.stringify(sourceID)},project:${JSON.stringify(values.project)},dataset_generation:${JSON.stringify(ctx.builder.catalog.generation)}}).limit(1).toArray()[0].payload))`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  ctx.raw = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('{')));
  assert.equal(ctx.raw.id, sourceID);
  return ctx;
};
const append = async (ctx, step) => {
  const doc = ctx.builder.workspace.documents[0];
  const proposal = (await api(ctx.base + '/construction-proposals', { ...identity(ctx.builder), outputId: doc.output.id, changedStepId: step.id, candidateConstruction: { ...(doc.construction ?? { version: 1 }), steps: [...(doc.construction?.steps ?? []), step] }, limit: 25 })).body;
  assert.equal(proposal.previewStatus, 'READY');
  await command(ctx, [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId: doc.output.id, proposalId: proposal.proposalId }]);
};
const partialCohort = async source => {
  const ctx = await create('partial-cohort', source.explorer);
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', values['arango-container'], 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db.Observation.byExample({project:${JSON.stringify(values.project)},dataset_generation:${JSON.stringify(ctx.builder.catalog.generation)}}).limit(2).toArray().map(r=>r.id)))`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const ids = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  const other = ids.find(id => id !== sourceID);
  assert(other, 'Partial cohort coverage requires a second real Observation');
  const selectionPath = ctx.base.replace('/authoring/v2', '/selections');
  const selection = (await api(selectionPath, { snapshotToken: ctx.builder.catalog.snapshotToken, idempotencyKey: ctx.explorer, source: { kind: 'resources', resources: { refs: [sourceID, other].map(id => ({ project: values.project, generation: ctx.builder.catalog.generation, resourceType: 'Observation', id })) } } })).body;
  const outputId = ctx.builder.workspace.documents[0].output.id;
  const routes = (await api(ctx.base + '/population-routes', { snapshotToken: ctx.builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 })).body;
  const route = routes.choices.find(c => c.route.length === 0);
  assert(route);
  await command(ctx, [{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: route.routeChoiceId }]);
  const page = (await api(selectionPath + '/' + selection.id + '?limit=100')).body;
  assert.equal(page.members.length, 2);
  ctx.group = (await api(selectionPath + '/' + selection.id + '/explicit-groups', { snapshotToken: ctx.builder.catalog.snapshotToken, idempotencyKey: randomUUID(), groups: [{ id: 'qa-partial', label: 'Partially assigned cohort', ordinal: 0, memberIds: [page.members[0].memberKey] }] })).body;
  return ctx;
};
const expandValues = (payload, path) => path.split('.').reduce((items, part) => items.flatMap(item => {
  const value = item?.[part.replace('[]', '')];
  return part.endsWith('[]') ? (Array.isArray(value) ? value : []) : [value];
}), [payload]).filter(v => v !== undefined && v !== null);
const verifySaved = (ctx, item, beforeSteps) => {
  const doc = ctx.builder.workspace.documents[0];
  assert.deepEqual(doc.construction?.steps ?? [], beforeSteps, 'A base row change must preserve authored steps');
  if (item.selection.kind === 'RECORDS') assert.equal(doc.rows.kind, 'RECORDS');
  else if (item.selection.kind === 'EXPANDED') {
    assert.equal(doc.rows.kind, 'EXPANDED');
    assert.equal(doc.rows.expanded.scopePath, item.path);
    assert.equal(doc.rows.expanded.emptyCollectionPolicy, item.selection.expanded.emptyCollectionPolicy);
  } else {
    assert.equal(doc.rows.kind, 'GROUPS');
    assert.equal(doc.rows.groups.source.kind, 'EXPLICIT');
    assert.deepEqual(doc.rows.groups.source.explicit, item.selection.explicitGroup);
  }
};
const selections = (discovery, group) => [
  { name: 'records', shape: 'records', selection: { kind: 'RECORDS' }, expectedRows: 1 },
  ...discovery.choices.filter(c => c.kind === 'EXPANDED').flatMap(c => c.policies.flatMap(p => {
    assert.equal(p.name, 'emptyCollectionPolicy');
    return p.options.map(policy => ({ name: `${c.fieldPath}-${policy}`, shape: `expanded:${c.choiceId}`, policy: `expanded:${c.choiceId}:${policy}`, path: c.fieldPath, selection: { kind: 'EXPANDED', expanded: { rowChoiceId: c.choiceId, emptyCollectionPolicy: policy } } }));
  })),
  ...discovery.explicitGroups.filter(g => g.revisionId === group?.revisionId).flatMap(g => g.unassignedMemberPolicies.map(policy => ({ name: `cohort-${policy}`, shape: `explicit:${g.revisionId}`, policy: `explicit:${g.revisionId}:${policy}`, selection: { kind: 'EXPLICIT_GROUP', explicitGroup: { revisionId: g.revisionId, unassignedMemberPolicy: policy } }, expectedRows: 1 }))),
];
const verifyBrowser = async (ctx, item, record) => {
  const directory = join(values.evidence, record.id);
  await mkdir(directory, { recursive: true });
  const state = { exceptions: [], http: [], incidental: [], responses: [], previews: [], errors: [], nativeRequests: cda.report.nativeRequests };
  const requests = requestTracker;
  const caseErrors = { console: [], pageErrors: [], networkFailures: [], httpFailures: [] };
  const onConsole = message => { if (message.type() === 'error') caseErrors.console.push(message.text()); };
  const onPageError = error => caseErrors.pageErrors.push(error.message);
  const onRequestFailed = request => caseErrors.networkFailures.push({ url: request.url(), error: request.failure()?.errorText });
  const onResponse = response => {
    const path = new URL(response.url()).pathname;
    if (response.status() >= 400 && !(record.expectedValidation && response.status() === 422 && path.endsWith('/row-definition-proposals'))) {
      caseErrors.httpFailures.push({ status: response.status(), url: response.url() });
    }
  };
  page.on('console', onConsole); page.on('pageerror', onPageError); page.on('requestfailed', onRequestFailed); page.on('response', onResponse);
  const inspect = (fn, args) => cda.inspect(fn, args);
  const waitFor = (fn, args = {}, timeout = 5000) => cda.wait(fn, args, timeout);
  const click = (selector, identity = {}) => cda.click(selector, identity);
  const select = (selector, value) => cda.selectOption(selector, value);
  const assertBrowserClean = () => {
    assert.deepEqual(caseErrors.console, [], 'Unexpected browser console errors');
    assert.deepEqual(caseErrors.pageErrors, [], 'Unexpected page errors');
    assert.deepEqual(caseErrors.networkFailures, [], 'Unexpected app network failures');
    const unexpectedHTTP = caseErrors.httpFailures.filter(failure => {
      const expectedValidation = record.expectedValidation && failure.status === 422 && failure.url.endsWith('/row-definition-proposals');
      return !expectedValidation;
    });
    assert.deepEqual(unexpectedHTTP, [], 'Unexpected app HTTP failures');
    assert.deepEqual(state.errors, [], 'Unexpected API request failures');
  };
  const table = `[data-testid="construction-table-${ctx.builder.workspace.documents[0].output.id}"]`;
  const url = `${values['ui-origin']}/?project=${encodeURIComponent(values.project)}&explorer=${ctx.explorer}&mode=builder`;
  const open = async () => {
    await cda.navigate(url);
    await waitFor(({ selector }) => Boolean(document.querySelector(selector)), { selector: table }, 5000);
    await click(table);
    await waitFor(() => !document.body.innerText.includes('Loading your table') && document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false, {}, 5000);
    await click('[data-testid="construction-rows-settings-trigger"]');
    await waitFor( () => document.querySelector('select[aria-label="What should each row represent?"]')?.disabled === false, {}, 5000);
  };
  try {
    state.browserVersion = page.context().browser()?.version();
    await open();
    const shapeSelect = 'select[aria-label="What should each row represent?"]';
    state.options = await inspect(({ selector }) => [...document.querySelector(selector).options].map(option => ({ value: option.value, label: option.text, disabled: option.disabled })), { selector: shapeSelect });
    if (ctx.builder.workspace.documents[0].construction?.steps.length) {
      assert(!state.options.some(option => option.value.startsWith('explicit:')), 'Saved cohorts must not be offered with authored steps');
      assert.equal(await inspect(() => [...document.querySelectorAll('button')].some(button => button.innerText === 'Create groups from this selection')), false);
    }
    const configure = async (target = item) => {
      const requestStart = state.nativeRequests.length;
      const started = Date.now();
      await select(shapeSelect, target.shape);
      if (target.policy) {
        await waitFor( () => document.querySelector('select[aria-label="Unmatched record policy"]')?.disabled === false, {}, 5000);
        await select('select[aria-label="Unmatched record policy"]', target.policy);
      }
      const [request] = await Promise.all([
        requests.waitFor(entry => entry.path.endsWith('/row-definition-proposals') && entry.method === 'POST' && JSON.stringify(entry.body?.selection) === JSON.stringify(target.selection), { fromIndex: requestStart, timeoutMs: 5000 }),
        waitFor(() => !document.body.innerText.includes('Compiling and comparing row membership') && (document.querySelector('[aria-label="Row definition preview"]') || document.querySelector('[role="alert"]')), {}, 5000),
      ]);
      await requests.flush();
      state.durationMs = Date.now() - started;
      state.previews.push({ selection: target.selection, durationMs: state.durationMs });
      assert(state.durationMs <= 5000, `The selected policy did not render a proposal within five seconds: ${state.durationMs} ms`);
      assert.equal(request.status, target.selection.expanded?.emptyCollectionPolicy === 'ERROR' || target.selection.explicitGroup?.unassignedMemberPolicy === 'ERROR' ? 422 : 200);
      assert.equal(state.errors.length, 0, JSON.stringify(state.errors));
      state.response = { selection: request.body.selection, proposal: request.response, status: request.status };
      state.responses.push(state.response);
      state.proposal = state.response.proposal;
    };
    await configure();
    if (record.expectedValidation) {
      assert.equal(state.response.status, 422);
      assert.equal(state.proposal.error?.code, record.expectedErrorCode);
      assert.equal(state.proposal.proposalId, undefined);
      assert.equal(await inspect(() => [...document.querySelectorAll('button')].some(button => button.innerText === 'Apply row definition')), false);
      assert.equal((await api(ctx.base + '/builder')).body.draftDigest, ctx.builder.draftDigest);
      assert.equal(await inspect(() => document.querySelector('select[aria-label="Unmatched record policy"]').disabled), false);
      assert.match(await inspect(() => document.querySelector('[role="alert"]').innerText), /Some records/);
      const repair = { ...item, policy: item.policy.replace(/ERROR$/, 'EXCLUDE'), selection: item.selection.kind === 'EXPANDED'
        ? { kind: 'EXPANDED', expanded: { ...item.selection.expanded, emptyCollectionPolicy: 'EXCLUDE' } }
        : { kind: 'EXPLICIT_GROUP', explicitGroup: { ...item.selection.explicitGroup, unassignedMemberPolicy: 'EXCLUDE' } } };
      await configure(repair);
      assert.equal(state.proposal.comparison.status, 'AVAILABLE');
      assert.equal(state.proposal.comparison.candidate.rowCount, record.repairRows);
      const beforeSteps = ctx.builder.workspace.documents[0].construction?.steps ?? [];
      await click('[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
      await waitFor( () => !document.querySelector('[aria-label="Row definition settings"]'), {}, 5000);
      ctx.builder = (await api(ctx.base + '/builder')).body;
      verifySaved(ctx, repair, beforeSteps);
      await open();
      assert.equal(await inspect(() => document.querySelector('select[aria-label="Unmatched record policy"]').value), repair.policy);
      state.rendered = await inspect(() => document.body.innerText);
      assert.equal(Number(state.rendered.match(/Total rows\n([^\n]+)/)?.[1]), record.repairRows);
      state.repair = 'EXCLUDE applied and persisted';
      assertBrowserClean();
      record.browser = { status: 'validation-and-repair-passed', durationMs: state.durationMs };
      return;
    }
    assert(state.proposal?.proposalId, 'An offered row choice must produce an applicable proposal');
    assert.equal(state.proposal.comparison.candidate.rowCount, record.expectedRows);
    assert.equal((await api(ctx.base + '/builder')).body.draftDigest, ctx.builder.draftDigest);
    if (item.name === 'records' || item.name === 'cohort-ERROR') {
      await click('[aria-label="Row definition settings"] button', { name: 'Cancel' });
      await waitFor( () => !document.querySelector('[aria-label="Row definition settings"]'), {}, 5000);
      assert.equal((await api(ctx.base + '/builder')).body.draftDigest, ctx.builder.draftDigest);
      state.cancel = 'unchanged draft';
      await open();
      await configure();
    }
    await click('[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
    await waitFor( () => !document.querySelector('[aria-label="Row definition settings"]'), {}, 5000);
    ctx.builder = (await api(ctx.base + '/builder')).body;
    const digest = ctx.builder.draftDigest;
    await open();
    assert.equal(await inspect(({ selector }) => document.querySelector(selector).value, { selector: shapeSelect }), item.shape);
    if (item.policy) assert.equal(await inspect(() => document.querySelector('select[aria-label="Unmatched record policy"]')?.value), item.policy);
    assert.equal((await api(ctx.base + '/builder')).body.draftDigest, digest);
    state.rendered = await inspect(() => document.body.innerText);
    assert.equal(Number(state.rendered.match(/Total rows\n([^\n]+)/)?.[1]), record.expectedRows, 'The reloaded table must render the raw-source row count');
    assertBrowserClean();
    record.browser = { status: 'passed', durationMs: state.durationMs };
  } catch (error) {
    throw error;
  } finally {
    await requests.flush();
    state.body = await page.locator('body').innerText().catch(() => undefined);
    state.exceptions = [...caseErrors.console, ...caseErrors.pageErrors];
    state.http = [...caseErrors.httpFailures, ...caseErrors.networkFailures, ...state.errors];
    page.off('console', onConsole); page.off('pageerror', onPageError); page.off('requestfailed', onRequestFailed); page.off('response', onResponse);
    await writeFile(join(directory, 'browser.json'), JSON.stringify(state, null, 2));
  }
};

try {
  const source = await fixture();
  const filtered = await create('filter', source.explorer);
  const capability = (await api(filtered.base + '/construction-capabilities', { ...identity(filtered.builder), outputId: filtered.builder.workspace.documents[0].output.id, stageId: 'source_projection' })).body;
  const columns = capability.selectedStage.columns.filter(c => !c.internal);
  await append(filtered, { id: 'qa-filter', inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'FILTER', filter: { columnId: columns[0].id, operator: 'EXISTS' } }, outputs: columns.map(({ id, name, label, type }) => ({ id, name, label, type })) });
  const grouped = await create('group', source.explorer);
  await append(grouped, { id: 'qa-group', inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'GROUP', group: {
    constructionId: 'qa-group', keys: [{ inputColumnId: columns[0].id, outputColumnId: 'qa-id' }],
    aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'qa-count' }], missingKeyPolicy: 'GROUP',
  } }, outputs: [{ id: 'qa-id', name: 'record_id', label: 'Observation ID', type: 'string' }, { id: 'qa-count', name: 'row_count', label: 'Row count', type: 'integer' }] });
  const related = await create('related', values['related-seed']);
  const partial = await partialCohort(source);
  assert(related.builder.workspace.documents[0].construction?.steps.some(s => s.operation.kind === 'RELATED_EXPAND'), 'Related seed must retain the reported expansion-chain shape');
  for (const variant of [source, filtered, grouped, related, partial]) {
    const discovery = await choices(variant);
    assert(discovery.choices.every(choice => choice.kind === 'EXPANDED'), 'Lifecycle discovery must not advertise an unsupported row operation');
    const hasSteps = Boolean(variant.builder.workspace.documents[0].construction?.steps.length);
    report.coverage.push({ state: variant.name, explorer: variant.explorer, authoredSteps: hasSteps, expandedPaths: discovery.choices.filter(c => c.kind === 'EXPANDED').map(c => c.fieldPath), explicitGroups: discovery.explicitGroups.length });
    if (hasSteps) assert.equal(discovery.explicitGroups.length, 0, 'Discovery advertised a saved cohort that cannot compose with authored steps');
    else assert(discovery.explicitGroups.some(g => g.revisionId === variant.group.revisionId), 'Source-only cohorts must remain usable');
    // The reported multi-resource chain is a discovery regression. Data oracles
    // below use a single selected Observation, so never guess its chain counts.
    if (variant === related) continue;
    for (const item of selections(discovery, hasSteps ? undefined : variant.group).filter(item => variant !== partial || item.selection.kind === 'EXPLICIT_GROUP')) {
      const record = { id: `${variant.name}-${report.cases.length}`, name: item.name, selection: item.selection, status: 'running' };
      report.cases.push(record);
      try {
        const ctx = await create(record.id, variant.explorer);
        if (item.selection.kind === 'RECORDS') {
          const initial = selections(discovery).find(s => s.path && expandValues(source.raw, s.path).length && s.selection.expanded.emptyCollectionPolicy === 'PRESERVE_PARENT');
          assert(initial, 'Records restoration needs a populated expansion fixture');
          const proposal = (await api(ctx.base + '/row-definition-proposals', { ...identity(ctx.builder), outputId: ctx.builder.workspace.documents[0].output.id, selection: initial.selection, limit: 25 })).body;
          await command(ctx, [{ type: 'APPLY_ROW_DEFINITION_PROPOSAL', outputId: ctx.builder.workspace.documents[0].output.id, proposalId: proposal.proposalId }]);
        }
        const count = item.path ? expandValues(source.raw, item.path).length : item.expectedRows;
        record.expectedRows = count || (item.selection.expanded?.emptyCollectionPolicy === 'EXCLUDE' ? 0 : 1);
        if (variant === grouped && record.expectedRows > 0) record.expectedRows = 1;
        if (variant === partial && item.selection.explicitGroup.unassignedMemberPolicy === 'GROUP_AS_UNASSIGNED') record.expectedRows = 2;
        record.expectedValidation = (!count && item.selection.expanded?.emptyCollectionPolicy === 'ERROR') || (variant === partial && item.selection.explicitGroup.unassignedMemberPolicy === 'ERROR');
        record.expectedErrorCode = variant === partial ? 'EXPLICIT_GROUP_UNASSIGNED_MEMBER' : 'EMPTY_COLLECTION_ERROR';
        record.repairRows = variant === partial ? 1 : 0;
        const outputId = ctx.builder.workspace.documents[0].output.id;
        const beforeSteps = ctx.builder.workspace.documents[0].construction?.steps ?? [];
        const proposal = await api(ctx.base + '/row-definition-proposals', { ...identity(ctx.builder), outputId, selection: item.selection, limit: 25 }, record.expectedValidation);
        record.proposal = proposal.body; record.durationMs = proposal.durationMs;
        if (record.expectedValidation) {
          assert.equal(proposal.status, 422);
          assert.equal(record.proposal.error?.code, record.expectedErrorCode);
          assert.equal(record.proposal.proposalId, undefined, 'Invalid data must not receive an applicable proposal');
        } else {
          assert(record.proposal.proposalId, 'Offered choice must compile an applicable proposal');
          assert.notEqual(record.proposal.comparison.status, 'UNAVAILABLE');
          assert.equal(record.proposal.comparison.candidate.rowCount, record.expectedRows);
        }
        assert(proposal.durationMs <= 5000);
        assert.equal((await api(ctx.base + '/builder')).body.draftDigest, ctx.builder.draftDigest);
        if (values.browser) browserCases.push({ ctx, item, record, beforeSteps });
        else if (!record.expectedValidation) {
          await command(ctx, [{ type: 'APPLY_ROW_DEFINITION_PROPOSAL', outputId, proposalId: record.proposal.proposalId }]);
          assert.equal((await api(ctx.base + '/builder')).body.draftDigest, ctx.builder.draftDigest);
        }
        if (!values.browser && !record.expectedValidation) verifySaved(ctx, item, beforeSteps);
        record.status = values.browser ? 'api-passed' : 'passed';
      } catch (error) {
        record.status = 'failed'; record.error = String(error.stack ?? error); report.failures.push({ id: record.id, error: record.error });
      }
      await writeFile(join(values.evidence, record.id + '.json'), JSON.stringify(record, null, 2));
      console.log(JSON.stringify({ case: record.id, choice: record.name, status: record.status }));
    }
  }
  if (values.browser && report.failures.length === 0) {
    const requested = values['browser-cases']?.split(',');
    if (requested) for (const name of requested) assert(browserCases.some(c => c.item.name === name), `Unknown browser case: ${name}`);
    report.browserSelection = requested ?? 'all';
    for (const { ctx, item, record, beforeSteps } of browserCases.filter(c => !requested || requested.includes(c.item.name))) {
      try {
        await verifyBrowser(ctx, item, record);
        if (!record.expectedValidation) verifySaved(ctx, item, beforeSteps);
        record.status = 'passed';
      } catch (error) {
        record.status = 'failed'; record.error = String(error.stack ?? error);
        report.failures.push({ id: record.id, error: record.error });
      }
      await writeFile(join(values.evidence, record.id + '.json'), JSON.stringify(record, null, 2));
      console.log(JSON.stringify({ browserCase: record.id, choice: record.name, status: record.status }));
    }
  }
} catch (error) { report.failures.push({ error: String(error.stack ?? error) }); }
report.finished = new Date().toISOString();
report.browserSelection = values['browser-cases']?.split(',') ?? 'all';
await writeFile(join(values.evidence, 'report.json'), JSON.stringify(report, null, 2));
await cda.attachReport('base-settings', report);
assert.deepEqual(report.failures, [], 'Base-settings browser lifecycle must complete every selected case');
cda.check('correctness', 'base settings lifecycle preserves authored steps and persisted row shape', true,
  { browserCases: report.cases.map(item => item.id), coverage: report.coverage });
return report;
}
