import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';

export async function rowSourcesWorkflow({ page, cda }) {
const project = cda.project;
const lineageMode = process.env.LOOM_LINEAGE_MODE ?? 'GROUP';
assert(['GROUP', 'CODED_GROUP', 'RELATED_EXPAND'].includes(lineageMode));
const resourceType = lineageMode === 'CODED_GROUP' ? 'Observation' : 'Specimen';
const withFilter = process.env.LOOM_LINEAGE_FILTER === '1';
assert(!withFilter || lineageMode === 'GROUP', 'Composed filtering wave starts with ordinary Group');
const explorer = `row-sources-browser-${Date.now()}`;
const evidence = cda.evidenceDirectory;
const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = Object.assign(cda.report, { explorer, lineageMode, withFilter, cases: [], errors: [], requests: [], nativeRequests: cda.nativeRequests, started: new Date().toISOString() });

const browserEval = (_page, callback, args = []) => cda.inspect(callback, args);
const click = (_page, selector, identity, timeout) => cda.click(selector, identity, timeout);
const fill = (_page, selector, value, identity, timeout) => cda.fill(selector, value, identity, timeout);
const navigate = (_page, url) => cda.navigate(url);
const selectOption = (_page, selector, value, options) => cda.selectOption(selector, value, options);
const waitForBrowser = (_page, predicate, argsOrTimeout = [], timeout) => Array.isArray(argsOrTimeout)
  ? cda.wait(predicate, argsOrTimeout, timeout ?? 5000)
  : cda.wait(predicate, [], argsOrTimeout);
const waitForCapturedResponse = (_page, tracker, predicate, timeout) => cda.waitForCapturedResponse(tracker, predicate, timeout);
const captureRequests = (_page, _report, ownedPathPrefix, options = {}) => cda.captureRequests(ownedPathPrefix, options);
const includeBrowserDiagnostics = () => cda.includeBrowserDiagnostics();
let builder, outputId, browserEvents, fatal;
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `row-sources-browser-${randomUUID()}` },
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
  const matchesRequest = request => !expectedFilter || request.body?.candidateConstruction?.steps?.some(step =>
    step.operation?.kind === 'FILTER' && step.operation.filter.operator === expectedFilter.operator &&
    JSON.stringify(step.operation.filter.values) === JSON.stringify(expectedFilter.values));
  const predicate = request => request.path === base + '/construction-proposals' &&
    request.startedAt >= start && request.response && matchesRequest(request);
  const response = report.nativeRequests.findLast(predicate) ??
    await waitForCapturedResponse(page, browserEvents, predicate, Math.max(1, start + 5000 - Date.now()));
  if(response.status===200 && response.response.proposalId) {
    await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId===__arg0), [response.response.proposalId]);
  }
  await waitForBrowser(page, () => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)), []);
  const result = await browserEval(page, () => { const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))}; });
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
  await click(page, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
  const previewPredicate = request => request.path === base + '/preview' && request.startedAt >= start && request.status === 200;
  report.nativeRequests.findLast(previewPredicate) ??
    await waitForCapturedResponse(page, browserEvents, previewPredicate, Math.max(1, start + 5000 - Date.now()));
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async expectedRows => {
  const start = Date.now();
  await navigate(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector('[data-testid="construction-table-'+String(__arg0)+'"]')), [outputId]);
  await click(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false), []);
  await rendered(expectedRows);
  recordRender('load-to-render', start);
};
const rendered = async expectedRows => {
  await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === __arg0 && !document.body.innerText.includes('Loading your table…')), [String(expectedRows.length + 1)]);
  const rows = await browserEval(page, () => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length); });
  if (expectedRows.length > 5) {
    assert(rows.length > 0 && rows.length <= expectedRows.length);
    for (const row of rows) assert(expectedRows.some(expected => JSON.stringify(expected) === JSON.stringify(row)), 'Mounted source row must match the independent CDA page');
  } else assert.deepEqual(rows, expectedRows, 'Saved rendered rows must match the CDA oracle');
};
try {
  if (lineageMode === 'CODED_GROUP' || withFilter) {
    const testName = withFilter
      ? 'TestConstructionGroupFilterRowLineagePagesScopedContributorsAgainstArango'
      : 'TestConstructionCodedGroupRowLineagePagesScopedContributorsAgainstArango';
    const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
    const testArgs = ['proxy', 'docker', 'exec',
      '-e', `LOOM_TEST_ARANGO_URL=http://${arangoContainer}:8529`, '-e', 'LOOM_TEST_ARANGO_DATABASE=loom_dev',
      '-w', '/workspace', process.env.LOOM_CDA_API_CONTAINER,
      'go', 'test', './internal/dataframe/compiler', '-run', `^${testName}$`, '-count=1', '-v'];
    const check = spawnSync('rtk', testArgs, { encoding: 'utf8', timeout: 60000 });
    report.compilerChecks = [{ testName, args: testArgs, status: check.status, stdout: check.stdout, stderr: check.stderr }];
    assert.equal(check.status, 0, check.stderr + check.stdout);
    assert(check.stdout.includes(`--- PASS: ${testName}`) && !check.stdout.includes('--- SKIP:'), 'The scoped Arango regression must execute');
  }
  const rawQuery = query => {
    const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
    assert.equal(raw.status, 0, raw.stderr);
    return JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  };
  const sourceLimit = lineageMode === 'RELATED_EXPAND' ? 1 : 105;
  const codingFilter = lineageMode === 'CODED_GROUP'
    ? 'LET codes=FLATTEN(s.payload.component[*].code.coding) FILTER POSITION(codes[*].code,"specimen_type")'
    : '';
  const query = `FOR s IN ${resourceType} FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" ${codingFilter} SORT s._key LIMIT ${sourceLimit} RETURN {id:s.id,_id:s._id,generation:s.dataset_generation,component:s.payload.component}`;
  const sources = rawQuery(query);
  assert.equal(sources.length, sourceLimit);
  assert.equal(new Set(sources.map(source => source.id)).size, sourceLimit);
  assert(sources.every(source => source.generation === 'cda-fhir-v1'));
  let groupedRows = [[resourceType, String(sourceLimit)]];
  let expectedContributors = sources.map(source => resourceType + '/' + source.id);
  const codingGroups = new Map();
  let relatedPatients;
  let compositeRowNumber = 1;
  if (lineageMode === 'CODED_GROUP') {
    for (const source of sources) {
      const tuples = new Set(source.component.flatMap(component => (component.code?.coding ?? []).map(coding => JSON.stringify([coding.system ?? null, coding.version ?? null, coding.code ?? null]))));
      for (const tuple of tuples) {
        const ids = codingGroups.get(tuple) ?? new Set();
        ids.add('Observation/' + source.id);
        codingGroups.set(tuple, ids);
      }
    }
    const tuples = [...codingGroups.keys()].sort();
    assert(tuples.length > 0 && tuples.length <= 25, 'Use bounded complete CDA coding groups');
    groupedRows = tuples.map(tuple => [...JSON.parse(tuple).map(value => value ?? '—'), String(codingGroups.get(tuple).size)]);
    const inspectedTuple = tuples.find(tuple => JSON.parse(tuple)[2] === 'specimen_type' && codingGroups.get(tuple).size === 105);
    assert(inspectedTuple, 'The selected coding tuple must have a multi-page independent witness');
    compositeRowNumber = tuples.indexOf(inspectedTuple) + 1;
    expectedContributors = [...codingGroups.get(inspectedTuple)];
  } else if (lineageMode === 'RELATED_EXPAND') {
    const relatedQuery = `FOR e IN fhir_edge FILTER e._from==${JSON.stringify(sources[0]._id)} AND e.label=="subject_Patient" AND e.project=="${project}" AND e.dataset_generation=="cda-fhir-v1" FILTER STARTS_WITH(e._to,"Patient/") LET d=DOCUMENT(e._to) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" SORT d._key RETURN DISTINCT {id:d.id,_id:d._id}`;
    relatedPatients = rawQuery(relatedQuery);
    assert.equal(relatedPatients.length, 1, 'Use a bounded one-hop CDA relationship witness');
    groupedRows = [[sources[0].id, 'Specimen', relatedPatients[0].id]];
    expectedContributors = ['Specimen/' + sources[0].id, 'Patient/' + relatedPatients[0].id];
  }
  report.oracle = { query, sources, groupedRows, expectedContributors, relatedPatients };
  await api(root, { name: explorer, title: 'Source records lifecycle QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, 'cda-fhir-v1');
  const node = builder.catalog.nodes.find(candidate => candidate.resourceType === resourceType);
  assert(node);
  await command([{ type: 'CREATE_TABLE', title: `${lineageMode} source records QA`, rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  for (const [fieldPath, title] of [['id', 'FHIR resource ID'], ['resourceType', 'Record type']]) {
    const field = builder.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === fieldPath);
    assert(field);
    await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title }]);
  }
  const selection = await api(base.replace('/authoring/v2', '/selections'), {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: sources.map(source => ({ project, generation: source.generation, resourceType, id: source.id })) } },
  });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(choice => choice.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  const baseline = builder;
  const sourceRows = sources.slice(0, 25).map(source => [source.id, resourceType]);
  browserEvents = cda.captureRequests(base);
  const inspect = async (name, composite) => {
    const started = Date.now();
    const rowNumber = composite ? compositeRowNumber : 1;
    await click(page, `button[aria-label="Inspect row ${rowNumber} identity"]`);
    const selector = `[role="dialog"][aria-label="Row ${rowNumber} identity"]`;
    await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(__arg0)), [selector]);
    let details = await browserEval(page, ([__arg0]) => { return document.querySelector(__arg0).innerText; }, [selector]);
    assert(!/cannot be listed|unavailable|could not be fully listed|Could not load/i.test(details), details);
    if (composite) {
      await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(__arg0)), [selector + ' ul li'], 5000);
      recordRender(name + '-first-page', started);
      let pages = 1;
      while (await browserEval(page, ([__arg0]) => { return [...document.querySelectorAll(__arg0)].some(button => button.innerText === 'Show more source records'); }, [selector + ' button'])) {
        const previousCount = await browserEval(page, ([__arg0]) => { return document.querySelectorAll(__arg0).length; }, [selector + ' ul li']);
        const pageStarted = Date.now();
        await click(page, `${selector} button`, { name: 'Show more source records' });
        await waitForBrowser(page, ([__arg0, __arg1, __arg2]) => Boolean(document.querySelectorAll(__arg0).length > __arg1 && ![...document.querySelectorAll(__arg2)].some(button=>button.innerText==='Loading…')), [selector + ' ul li', previousCount, selector + ' button'], 5000);
        recordRender(name + '-page-' + ++pages, pageStarted);
        assert(pages <= 5, 'Contributor pagination must converge');
      }
      const contributors = await browserEval(page, ([__arg0]) => { return [...document.querySelectorAll(__arg0)].map(item=>item.innerText.trim()); }, [selector + ' ul li']);
      assert.deepEqual([...contributors].sort(), [...expectedContributors].sort());
      assert.equal(new Set(contributors).size, expectedContributors.length);
      report.lineagePages = pages;
      const bounds = await browserEval(page, ([__arg0]) => { const rect=document.querySelector(__arg0).getBoundingClientRect();return {top:rect.top,bottom:rect.bottom,height:rect.height,viewportHeight:innerHeight}; }, [selector]);
      assert(bounds.top >= 0 && bounds.bottom <= bounds.viewportHeight, 'Inspector must fit the viewport: ' + JSON.stringify(bounds));
      report.inspectorBounds = bounds;

    } else {
      assert(details.includes('Starting FHIR record'), details);
      assert(details.includes(resourceType + '/' + sources[0].id), details);
      recordRender(name, started);
    }
    const identity = await browserEval(page, ([__arg0]) => { return document.querySelector(__arg0)?.textContent; }, [selector + ' p.font-mono']);
    assert(identity);
    await click(page, `${selector} button`, { name: 'Close' });
    await waitForBrowser(page, ([__arg0]) => Boolean(!document.querySelector(__arg0)), [selector]);
    return identity;
  };
  await open(sourceRows);
  await inspect('source-row-inspection', false);
  const configureGroup = async name => {
    const started = Date.now();
    await click(page, '[data-testid="construction-rows-settings-trigger"]');
    if (lineageMode === 'RELATED_EXPAND') {
      await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]:not(:disabled)')), [], 5000);
      await click(page, '[data-testid="construction-action-related-rows"]');
      await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]:not(:disabled)')), []);
      await selectOption(page, '[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]', 'Patient');
      await waitForBrowser(page, () => Boolean(document.querySelector('input[aria-label="Specimen -[subject]-> Patient"]:not(:disabled)')), []);
      await click(page, 'input[aria-label="Specimen -[subject]-> Patient"]');
    } else if (lineageMode === 'CODED_GROUP') {
      await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-action-coded-group-rows"]:not(:disabled)')), [], 5000);
      await click(page, '[data-testid="construction-action-coded-group-rows"]');
      await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-coded-group-path"]:not(:disabled)')), []);
      await selectOption(page, '[data-testid="construction-coded-group-path"]', 'component[].code.coding[]');
    } else {
      await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-action-group-rows"]:not(:disabled)')), [], 5000);
      await click(page, '[data-testid="construction-action-group-rows"]');
      await waitForBrowser(page, () => Boolean(document.querySelector('input[aria-label="Group by Record type"]:not(:disabled)')), []);
      await click(page, 'input[aria-label="Group by Record type"]');
    }
    await proposal(name, started, groupedRows);
  };
  await configureGroup('source-records-group-preview');
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
  assert.deepEqual((await api(base + '/builder')).workspace, baseline.workspace);
  await configureGroup('confirmed-source-records-group-preview');
  await apply(groupedRows);
  await open(groupedRows);
  let grouped = builder;
  const identity = await inspect('grouped-row-source-records', true);
  await open(groupedRows);
  assert.equal(await inspect('reloaded-grouped-row-source-records', true), identity);
  assert.deepEqual((await api(base + '/builder')).workspace, grouped.workspace, 'Inspection must not change saved data');
  if (withFilter) {
    const groupStep = doc(builder).construction.steps.find(step => step.operation.kind === 'GROUP');
    const countColumn = groupStep.outputs.find(column => column.type === 'integer');
    assert(countColumn, 'The independently counted Group must expose its integer count');
    const configureFilter = async (name, threshold, editingStepId) => {
      const started = Date.now();
      if (editingStepId) {
        await click(page, `[data-testid="construction-history-step-${editingStepId}"]`);
        await click(page, `[data-testid="construction-edit-step-${editingStepId}"]`);
      } else {
        await click(page, '[data-testid="construction-action-keep-rows"]');
      }
      await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]:not(:disabled)')), []);
      await selectOption(page, '[data-testid="construction-filter-editor"] select[aria-label="Column"]', countColumn.id);
      await selectOption(page, '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'GT');
      const valueSelector = '[data-testid="construction-filter-editor"] input[aria-label="Value"]';
      await waitForBrowser(page, ([__arg0]) => Boolean(document.querySelector(__arg0)), [valueSelector + ':not(:disabled)']);
      await fill(page, valueSelector, String(threshold));
      await proposal(name, started, threshold < 105 ? groupedRows : [], { operator: 'GT', values: [{ kind: 'INTEGER', integer: threshold }] });
    };
    await configureFilter('group-count-filter-preview', 104);
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
    assert.deepEqual((await api(base + '/builder')).workspace, grouped.workspace);
    await configureFilter('confirmed-group-count-filter-preview', 104);
    await apply(groupedRows);
    await open(groupedRows);
    let filtered = builder;
    const filterStep = doc(builder).construction.steps.find(step => step.operation.kind === 'FILTER');
    assert.deepEqual(filterStep.operation.filter.values, [{ kind: 'INTEGER', integer: 104 }]);
    assert.equal(await inspect('filtered-group-source-records', true), identity);
    assert.deepEqual((await api(base + '/builder')).workspace, filtered.workspace);
    await configureFilter('second-group-count-filter-preview', 103);
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
    assert.deepEqual((await api(base + '/builder')).workspace, filtered.workspace);
    await configureFilter('confirmed-second-group-count-filter-preview', 103);
    await apply(groupedRows);
    await open(groupedRows);
    const secondFilter = doc(builder).construction.steps.filter(step => step.operation.kind === 'FILTER').at(-1);
    assert(secondFilter.id !== filterStep.id);
    assert.deepEqual(secondFilter.operation.filter.values, [{ kind: 'INTEGER', integer: 103 }]);
    assert.equal(await inspect('repeated-filter-group-source-records', true), identity);
    filtered = builder;
    await configureFilter('excluded-group-filter-edit-preview', 105, filterStep.id);
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
    assert.deepEqual((await api(base + '/builder')).workspace, filtered.workspace);
    await configureFilter('confirmed-excluded-group-filter-edit-preview', 105, filterStep.id);
    await apply([]);
    await open([]);
    const preview = report.nativeRequests.findLast(request => request.path === base + '/preview' && request.status === 200 && request.response)?.response;
    assert(preview?.receiptId);
    const notFoundResponse = await fetch(apiOrigin + base + '/row-lineage', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ receiptId: preview.receiptId, outputId, rowId: identity, offset: 0, limit: 25 }), signal: AbortSignal.timeout(5000),
    });
    const notFound = await notFoundResponse.json();
    report.filteredOutIdentityCheck = { status: notFoundResponse.status, response: notFound };
    assert.equal(notFoundResponse.status, 404, JSON.stringify(notFound));
    assert.equal(notFound.error?.code, 'PREVIEW_ROW_NOT_FOUND');
    assert(!sources.some(source => JSON.stringify(notFound).includes(source.id)), 'An excluded group must not disclose contributors');
    const removeFilterStarted = Date.now();
    await click(page, `[data-testid="construction-history-step-${filterStep.id}"]`);
    await click(page, `[data-testid="construction-remove-step-${filterStep.id}"]`);
    await proposal('remove-count-filter-preview', removeFilterStarted, groupedRows);
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
    assert.deepEqual((await api(base + '/builder')).workspace, builder.workspace);
    const confirmedRemoveFilterStarted = Date.now();
    await click(page, `[data-testid="construction-history-step-${filterStep.id}"]`);
    await click(page, `[data-testid="construction-remove-step-${filterStep.id}"]`);
    await proposal('confirmed-remove-count-filter-preview', confirmedRemoveFilterStarted, groupedRows);
    await apply(groupedRows);
    await open(groupedRows);
    assert.equal(await inspect('group-after-intermediate-filter-removal-source-records', true), identity);
    const remainingFilters = doc(builder).construction.steps.filter(step => step.operation.kind === 'FILTER');
    assert.equal(remainingFilters.length, 1, 'Removing an identity-preserving filter must retain the later independent condition');
    assert.equal(remainingFilters[0].id, secondFilter.id);
    assert.deepEqual(remainingFilters[0].operation.filter, secondFilter.operation.filter);
    const beforeFinalFilterRemoval = builder;
    const lastFilterRemoveStarted = Date.now();
    await click(page, `[data-testid="construction-history-step-${secondFilter.id}"]`);
    await click(page, `[data-testid="construction-remove-step-${secondFilter.id}"]`);
    await proposal('remove-final-count-filter-preview', lastFilterRemoveStarted, groupedRows);
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
    assert.deepEqual((await api(base + '/builder')).workspace, beforeFinalFilterRemoval.workspace);
    const confirmedLastFilterRemoveStarted = Date.now();
    await click(page, `[data-testid="construction-history-step-${secondFilter.id}"]`);
    await click(page, `[data-testid="construction-remove-step-${secondFilter.id}"]`);
    await proposal('confirmed-remove-final-count-filter-preview', confirmedLastFilterRemoveStarted, groupedRows);
    await apply(groupedRows);
    await open(groupedRows);
    assert.equal(await inspect('restored-unfiltered-group-source-records', true), identity);
    assert.deepEqual(doc(builder).construction, doc(grouped).construction, 'Removing both filters must preserve the exact upstream Group');
    grouped = builder;
  }
  if (lineageMode !== 'GROUP') {
    const savedStep = doc(builder).construction.steps.find(step => step.operation.kind === lineageMode);
    const configureEdit = async name => {
      const started = Date.now();
      await click(page, `[data-testid="construction-history-step-${savedStep.id}"]`);
      await click(page, `[data-testid="construction-edit-step-${savedStep.id}"]`);
      if (lineageMode === 'CODED_GROUP') {
        await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-coded-group-path"]')?.value === 'component[].code.coding[]'), []);
        await click(page, '[data-testid="construction-reshape-coded-group"] summary');
        await selectOption(page, '[data-testid="construction-coded-group-missing"]', 'EXCLUDE');
      } else {
        await waitForBrowser(page, () => Boolean(document.querySelector('select[aria-label="Related record type"]')?.value === 'Patient'), []);
        await selectOption(page, 'select[aria-label="If a current row has no matches"]', 'EXCLUDE');
      }
      await proposal(name, started, groupedRows);
    };
    await configureEdit('source-records-edit-preview');
    await click(page, '[data-testid="construction-cancel-proposal"]');
    await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
    assert.deepEqual((await api(base + '/builder')).workspace, grouped.workspace, 'Cancel must preserve the original source bindings and row operation');
    await configureEdit('confirmed-source-records-edit-preview');
    await apply(groupedRows);
    await open(groupedRows);
    const editedOperation = doc(builder).construction.steps.find(step => step.id === savedStep.id).operation;
    assert.equal(lineageMode === 'CODED_GROUP' ? editedOperation.codedGroup.missingKeyPolicy : editedOperation.relatedExpand.emptyPolicy, 'EXCLUDE');
    assert.equal(await inspect('edited-row-source-records', true), identity, 'Changing unmatched/missing policy must preserve identities of the same matched rows');
    grouped = builder;
  }
  const group = doc(builder).construction.steps.find(step => step.operation.kind === lineageMode);
  await click(page, `[data-testid="construction-history-step-${group.id}"]`);
  const removeStarted = Date.now();
  await click(page, `[data-testid="construction-remove-step-${group.id}"]`);
  await proposal('remove-inspected-group-preview', removeStarted, sourceRows);
  await click(page, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), []);
  assert.deepEqual((await api(base + '/builder')).workspace, grouped.workspace);
  await click(page, `[data-testid="construction-history-step-${group.id}"]`);
  const confirmedRemoveStarted = Date.now();
  await click(page, `[data-testid="construction-remove-step-${group.id}"]`);
  await proposal('confirmed-remove-inspected-group-preview', confirmedRemoveStarted, sourceRows);
  await apply(sourceRows);
  await open(sourceRows);
  await inspect('restored-source-row-inspection', false);
  assert.equal(doc(builder).construction?.steps?.length ?? 0, 0);
  assert.deepEqual(doc(builder).population, doc(baseline).population);
  const baselineColumns = doc(baseline).columns;
  const restoredColumns = doc(builder).columns.map((column, index) => {
    if (baselineColumns[index]?.columnId) return column;
    const { columnId, ...authored } = column;
    assert(columnId, 'Construction must preserve its generated source column identity');
    return authored;
  });
  assert.deepEqual(restoredColumns, baselineColumns, 'Removing Group must restore authored source columns and bindings');
  includeBrowserDiagnostics();
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
} catch (error) {
  fatal = error;
  report.status = 'failed'; report.error = String(error.stack ?? error);
  report.failureUI = await browserEval(page, () => document.body.innerText).catch(String);
} finally {
  await browserEvents?.flush();
  report.finished = new Date().toISOString();
  await cda.attachReport('row-sources-domain-report.json', report);
}

if (fatal || report.status === 'failed') throw fatal ?? new Error(report.error ?? 'Row-source lifecycle failed');
return report;
}
