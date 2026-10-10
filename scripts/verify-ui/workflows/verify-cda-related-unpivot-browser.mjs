import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from '../helpers/source-freeze.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from '../helpers/api-build-freeze.mjs';
import { sourceFingerprint } from '../helpers/source-fingerprint.mjs';
import { sanitizePayload } from '../helpers/playwright-browser.mjs';
import { assertVisibleRowsMatchOracle } from '../helpers/cda-row-oracle.mjs';
import { assertOwnedCdaTarget } from '../helpers/owned-cda-target.mjs';
import { finalOutputSchema, sourceColumnSchema } from '../helpers/unpivot-schema.mjs';


export async function flushRelatedUnpivotNativeRequests(capture, report, timeoutMs = 5_000) {
  assert.equal(typeof capture?.flush, 'function', 'Related Unpivot native request capture must provide a flush operation');
  assert(Array.isArray(report?.nativeRequests), 'Related Unpivot native request report must expose captured requests');
  await capture.flush({ timeoutMs, waitForNativeRequestTerminals: true });
  assert(report.nativeRequests.length > 0,
    'Related Unpivot lifecycle captured no owned native requests; terminal drain cannot be established');

  const isExactTerminal = entry => (entry.nativeEventChronology ?? []).some(event =>
    (event.event === 'requestfinished' || event.event === 'requestfailed') &&
    event.objectMatch === true && event.browserRequestId === entry.browserRequestId);
  const pending = report.nativeRequests.filter(entry => !isExactTerminal(entry));
  const drainEvidence = report.nativeRequestDrainEvidence ?? [];
  assert.equal(pending.length, 0,
    `Related Unpivot requests did not reach an exact native terminal event: ${JSON.stringify(pending.map(entry => ({
      requestId: entry.requestId ?? null, browserRequestId: entry.browserRequestId ?? null,
      method: entry.method ?? null, path: entry.path ?? null,
    })))}`);
  assert.equal(drainEvidence.length, 0,
    `Related Unpivot native request terminal drain timed out: ${JSON.stringify(drainEvidence)}`);

  const finished = report.nativeRequests.filter(entry =>
    (entry.nativeEventChronology ?? []).some(event => event.event === 'requestfinished' &&
      event.objectMatch === true && event.browserRequestId === entry.browserRequestId)).length;
  const failed = report.nativeRequests.filter(entry =>
    (entry.nativeEventChronology ?? []).some(event => event.event === 'requestfailed' &&
      event.objectMatch === true && event.browserRequestId === entry.browserRequestId)).length;
  return { timeoutMs, total: report.nativeRequests.length, finished, failed, pending: pending.length };
}

export async function runRelatedUnpivotBrowserWorkflow({ page, cda }, originalArgs = {}) {
  const environment = cda.env ?? process.env;
const includeFixtureDiagnostics = domainReport => {
    const diagnostics = cda.diagnostics;
    domainReport.errors ??= [];
    const add = (entry, same) => { if (!domainReport.errors.some(same)) domainReport.errors.push(entry); };
    for (const failure of diagnostics.pageErrors ?? []) add({ kind: 'runtime', message: failure.message }, item => item.kind === 'runtime' && item.message === failure.message);
    for (const failure of diagnostics.console ?? []) add({ kind: 'console', message: failure.text, location: failure.location }, item => item.kind === 'console' && item.message === failure.text);
    for (const failure of diagnostics.networkFailures ?? []) add({ kind: 'network', path: failure.url, failure: failure.failure }, item => item.kind === 'network' && item.path === failure.url);
    for (const failure of diagnostics.httpFailures ?? []) add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, item => item.kind === 'http' && item.url === failure.url && item.status === failure.status);
    domainReport.incidentalErrors ??= [];
    for (const failure of diagnostics.assetFailures ?? []) if (!domainReport.incidentalErrors.some(item => item.url === failure.url && item.status === failure.status)) domainReport.incidentalErrors.push(failure);
  };
  const captureFailure = async (error, details = {}) => cda.attachReport('failure-evidence', { error: String(error), details, diagnostics: cda.diagnostics });
const project = cda.project;
assert(project, 'Set LOOM_CDA_PROJECT to the isolated CDA project');
const explorer = cda.explorer;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const apiContainer = (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER);
const arangoContainer = (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER);
const composeProject = (cda.target.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT);
const sourceRoot = fileURLToPath(new URL('../../..', import.meta.url));
const sourceFreezeStartedAt = new Date().toISOString();
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = { explorer, cases: [], errors: [], requests: [], nativeRequests: [], sourceFreeze: { startedAt: sourceFreezeStartedAt }, started: new Date().toISOString() };
const fixtureRequestCapture = cda.captureRequests(base, { apiOrigin: uiOrigin });
await mkdir(evidence, { recursive: true });
let builder, outputId, source, expected, witnesses, selectedOracle;
let sourceFreeze, frozenApiBuild, controls, ownedTarget;
const click = (...args) => controls.click(...args);
const selectOption = (...args) => controls.selectOption(...args);
const fill = (...args) => controls.fill(...args);
const browserEval = (...args) => controls.inspect(...args);
const waitForBrowser = (callback, args = {}, timeout = 5000) => cda.wait(callback, args ?? {}, Math.min(timeout, 5000));
const navigate = (...args) => cda.navigate(...args);
const sanitizeReportValue = sanitizePayload;
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-unpivot-browser-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, ...(body ? { body: sanitizeReportValue(body) } : {}), status: response.status, response: sanitizeReportValue(value) });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', { commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(d => d.output.id === outputId);
const recordLifecycleCheck = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, passed, evidence);
const maximumOracleRows = 24;
const maximumSourceCandidates = 25;
const relatedChain = [
  { from: 'Specimen', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND' },
  { from: 'Patient', to: 'Condition', label: 'subject_Patient', field: 'subject', direction: 'INBOUND' },
  { from: 'Condition', to: 'Observation', label: 'focus_Condition', field: 'focus', direction: 'INBOUND' },
  { from: 'Observation', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND' },
];
const rawQuery = query => {
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const invocation = buildArangoShellInvocation({ container: arangoContainer, script, database: 'loom_dev' });
  const result = spawnSync(invocation.command, invocation.args, { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr || result.stdout || result.error?.message);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Raw CDA query returned no JSON array: ${result.stdout.slice(-1000)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  return rows;
};
const expandBoundedRelatedChain = selectedSource => {
  let rows = [{ anchor: selectedSource._id, values: [selectedSource.id] }];
  let matchedEdges = 0;
  const hops = [];
  for (const hop of relatedChain) {
    const anchors = [...new Set(rows.map(row => row.anchor).filter(Boolean))];
    let matches = [];
    if (anchors.length) {
      const endpoint = hop.direction === 'OUTBOUND' ? '_from' : '_to';
      const target = hop.direction === 'OUTBOUND' ? '_to' : '_from';
      const query = `FOR anchor IN ${JSON.stringify(anchors)} LET related = (
        FOR e IN fhir_edge FILTER e.${endpoint} == anchor AND e.label == ${JSON.stringify(hop.label)}
          AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(selectedSource.generation)}
          FILTER STARTS_WITH(e.${target}, ${JSON.stringify(`${hop.to}/`)})
          LET d = DOCUMENT(e.${target}) FILTER d != null AND d.project == ${JSON.stringify(project)}
            AND d.dataset_generation == ${JSON.stringify(selectedSource.generation)}
          SORT d._id RETURN DISTINCT { id: d.id, _id: d._id }
      ) FOR relatedRecord IN related SORT anchor, relatedRecord._id LIMIT ${maximumOracleRows + 1}
      RETURN { anchor, id: relatedRecord.id, _id: relatedRecord._id }`;
      matches = rawQuery(query);
    }
    if (matches.length > maximumOracleRows) {
      const error = new Error(`Related hop ${hop.from} to ${hop.to} exceeds the ${maximumOracleRows}-row complete-oracle bound`);
      error.code = 'RELATED_ORACLE_ROW_LIMIT';
      throw error;
    }
    const byAnchor = new Map();
    for (const match of matches) {
      const values = byAnchor.get(match.anchor) ?? [];
      values.push(match);
      byAnchor.set(match.anchor, values);
    }
    const next = [];
    for (const row of rows) {
      const found = row.anchor ? byAnchor.get(row.anchor) ?? [] : [];
      if (found.length) {
        matchedEdges += found.length;
        for (const match of found) next.push({ anchor: match._id, values: [...row.values, match.id] });
      } else {
        next.push({ anchor: null, values: [...row.values, '—'] });
      }
      if (next.length > maximumOracleRows) {
        const error = new Error(`Related hop ${hop.from} to ${hop.to} expands beyond the ${maximumOracleRows}-row complete-oracle bound`);
        error.code = 'RELATED_ORACLE_ROW_LIMIT';
        throw error;
      }
    }
    rows = next;
    hops.push({ hop, rows: structuredClone(rows), matchedEdges: matches.length });
  }
  return { rows, hops, matchedEdges };
};
const proposal = async (name, start, expectedRows) => {
  await waitForBrowser(() => (['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await browserEval(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return { status: panel?.dataset.proposalStatus, proposalId: panel?.dataset.proposalId, text: panel?.innerText,
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText)) };
  });
  assert.equal(result.status, 'ready', result.text);
  assertVisibleRowsMatchOracle(result.rows, expectedRows, { label: `${name} preview`, exactWindow: true });
  const durationMs = Date.now() - start;
if (page) report.lastElapsedMs = durationMs;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, result });
  return result;
};
const recordRender = (name, start) => {
  const durationMs = Date.now() - start;
if (page) report.lastElapsedMs = durationMs;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs });
  return durationMs;
};
const apply = async expectedRows => {
  const start = Date.now();
  await click( '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  const rows = await rendered(expectedRows);
  const durationMs = recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
  return { state: builder, rows, durationMs };
};
const open = async expectedRows => {
  const start = Date.now();
  await navigate( `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: `[data-testid="construction-table-${outputId}"]` });
  await click( `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(() => (document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  const rows = await rendered(expectedRows);
  const durationMs = recordRender('load-to-render', start);
  builder = await api(base + '/builder');
  return { state: builder, rows, durationMs };
};
const rendered = async expectedRows => {
  await waitForBrowser(({ rowCount }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === String(Math.min(25, rowCount) + 1) && !document.body.innerText.includes('Loading your table…');
  }, { rowCount: expectedRows.length });
  const rows = await browserEval(() => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));
  assertVisibleRowsMatchOracle(rows, expectedRows, { label: 'saved table', exactWindow: true });
  return rows;
};
try {
  ownedTarget = await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot, arangoContainer });
  report.target = ownedTarget;
  report.sourceFingerprint = { root: sourceRoot, before: sourceFingerprint(sourceRoot) };
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze.watchedFileCount = sourceFreeze.watchedFileCount;
  frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiContainer));
  report.apiBuildFreeze = { target: 'running isolated CDA API build stamp', container: apiContainer, initial: frozenApiBuild.initial, invalidatesRun: true, productFailure: false };
  const query = `FOR s IN Specimen FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(cda.generation ?? 'cda-fhir-v1')} SORT s.id LIMIT ${maximumSourceCandidates} RETURN {id:s.id,_id:s._id,generation:s.dataset_generation}`;
  const sourceCandidates = rawQuery(query);
  assert(sourceCandidates.length > 0, 'No scoped Specimen candidates were found in the bounded source sample');
  const candidateEvidence = [];
  for (const candidate of sourceCandidates) {
    try {
      const expanded = expandBoundedRelatedChain(candidate);
      candidateEvidence.push({ sourceID: candidate.id, completeRows: expanded.rows.length, matchedEdges: expanded.matchedEdges });
      if (expanded.rows.length >= 2 && expanded.rows.length <= maximumOracleRows && expanded.matchedEdges > 0) {
        source = candidate;
        selectedOracle = expanded;
        break;
      }
    } catch (error) {
      if (error.code !== 'RELATED_ORACLE_ROW_LIMIT') throw error;
      candidateEvidence.push({ sourceID: candidate.id, rejected: 'complete chain exceeds the 24-row bound' });
    }
  }
  assert(source && selectedOracle,
    `The first ${maximumSourceCandidates} scoped Specimen candidates contain no complete two-to-twenty-four-row Related chain; this is a bounded fixture gap, not project-wide absence`);
  witnesses = selectedOracle.rows;
  expected = witnesses.map(witness => witness.values);
  report.oracle = { query, candidateLimit: maximumSourceCandidates, maximumCompleteRows: maximumOracleRows,
    candidateEvidence, source, chain: selectedOracle.hops };
  recordLifecycleCheck('correctness',
    'bounded raw CDA oracle selects a complete two-to-twenty-four-row Specimen Related chain',
    expected.length >= 2 && expected.length <= maximumOracleRows && selectedOracle.matchedEdges > 0,
    { sourceID: source.id, candidateCount: candidateEvidence.length, rowCount: expected.length,
      matchedEdges: selectedOracle.matchedEdges, project, generation: source.generation });
  await api(root, { name: explorer, title: 'Related Unpivot composition QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Unpivot QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: source.generation, resourceType: 'Specimen', id: source.id }] } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  controls = cda;
  const directColumn = doc(builder).columns.find(column => column.label === 'Specimen ID');
  assert(directColumn?.columnId, 'Direct source schema must retain a stable Specimen ID columnId');
  assert.equal(doc(builder).population.selectionRevisionId, selection.id);
  assert.equal(doc(builder).population.route.length, 0, 'The pinned selection must retain its direct root route');
  recordLifecycleCheck('correctness',
    'fresh Explorer and direct Specimen ID column bind the exact project and generation-scoped source',
    builder.catalog.generation === source.generation && doc(builder).population.selectionRevisionId === selection.id &&
      doc(builder).columns.some(column => column.columnId === directColumn.columnId && column.label === 'Specimen ID'),
    { outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId,
      source: { project, generation: source.generation, resourceType: 'Specimen', id: source.id }, column: directColumn });
  await open([[source.id]]);
  const expandedColumnsBefore = structuredClone(doc(builder).columns);
  let relatedSteps;
  for (let hopIndex = 0; hopIndex < relatedChain.length; hopIndex += 1) {
    const hop = relatedChain[hopIndex];
    witnesses = selectedOracle.hops[hopIndex].rows;
    expected = witnesses.map(witness => witness.values);
    let start=Date.now();
    await click('[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(() => (document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false));
    await click('[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForBrowser(({ selector }) => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, { selector: `${panel} select[aria-label="Related record type"]` });
    await selectOption(panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForBrowser(({ selector }) => Boolean(document.querySelector(selector)), { selector: `${panel} input[aria-label="${label}"]` }, 5000);
    await click(panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  const expanded=builder;
  relatedSteps = doc(expanded).construction.steps.filter(step => step.operation.kind === 'RELATED_EXPAND');
  assert.equal(relatedSteps.length, relatedChain.length, 'All four native Related row expansions must persist');
  for (let index = 0; index < relatedChain.length; index += 1) {
    const related = relatedSteps[index].operation.relatedExpand;
    const hop = relatedChain[index];
    const persistedHop = related?.route?.[0];
    assert(related && persistedHop, `Related stage ${index + 1} omitted its saved route`);
    assert.equal(related.targetResourceType, hop.to);
    assert.equal(persistedHop.fromResourceType, hop.from);
    assert.equal(persistedHop.toResourceType, hop.to);
    assert.equal(persistedHop.relationship, hop.label);
    assert.equal(persistedHop.storageDirection, hop.direction);
  }
  const relatedRender = await open(expected);
  const relatedRows = relatedRender.rows;
  recordLifecycleCheck('correctness',
    'native Related choices preserve exact raw rows and stage identities at every chain hop',
    relatedSteps.length === relatedChain.length && expected.length >= 2 && expected.length <= maximumOracleRows,
    { relatedStepIDs: relatedSteps.map(step => step.id), sourceColumnIDs: expandedColumnsBefore.map(column => column.columnId),
      rawRows: expected, savedRows: relatedRows, hopRows: selectedOracle.hops.map(hop => hop.rows) });
  const expandedDocument = structuredClone(doc(expanded));
  const expandedWorkspace = structuredClone(expanded.workspace);
  const expandedVersion = expanded.draftVersion;
  const expandedDigest = expanded.draftDigest;
  let start=Date.now();
  await click('[data-testid="construction-rows-settings-trigger"]');
  const unpivotTile = page.getByTestId('construction-action-unpivot-rows');
  await unpivotTile.waitFor({ state: 'visible', timeout: 5000 });
  assert(await unpivotTile.isEnabled(), 'Turn columns into rows must be enabled');
  await click('[data-testid="construction-action-unpivot-rows"]');
  await waitForBrowser(() => (document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.disabled===false));
  await click('input[aria-label="Unpivot Specimen ID"]');
  const selectedUnpivotInput = await browserEval(() => ({
    checked: document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.checked ?? false,
    disabled: document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.disabled ?? true,
  }));
  assert.deepEqual(selectedUnpivotInput, { checked: true, disabled: false });
  recordLifecycleCheck('usability',
    'saved Related rows expose an enabled Unpivot action and the exact Specimen ID input',
    selectedUnpivotInput.checked && !selectedUnpivotInput.disabled,
    { outputId, selectedColumnID: directColumn.columnId, selectedUnpivotInput });
  const unpivotExpected=expected.map(row=>[...row.slice(1),'Specimen ID',row[0]]);
  const firstUnpivotPreview = await proposal('related-chain-unpivot-preview',start,unpivotExpected);
  recordLifecycleCheck('correctness',
    'Unpivot preview matches the exact transformed raw multiset and retains related columns',
    firstUnpivotPreview.status === 'ready' && JSON.stringify(firstUnpivotPreview.rows) === JSON.stringify(unpivotExpected),
    { inputRows: expected, previewRows: firstUnpivotPreview.rows, expectedRows: unpivotExpected,
      retainedColumnIDs: relatedSteps.at(-1).outputs.filter(output => output.id !== directColumn.columnId).map(output => output.id) });
  const beforeFirstCancel = await api(base+'/builder');
  const cancelStart = Date.now();
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  builder = await api(base+'/builder');
  assert.deepEqual(builder.workspace,expandedWorkspace);
  assert.equal(builder.draftVersion, expandedVersion);
  assert.equal(builder.draftDigest, expandedDigest);
  const cancelRows = await rendered(expected);
  const cancelDuration = recordRender('Cancel Unpivot to exact Related rows', cancelStart);
  recordLifecycleCheck('persistence',
    'Cancel preserves the exact Related workspace, draft CAS, construction, and rows',
    JSON.stringify(builder.workspace) === JSON.stringify(beforeFirstCancel.workspace) && builder.draftVersion === expandedVersion &&
      builder.draftDigest === expandedDigest && JSON.stringify(cancelRows) === JSON.stringify(expected),
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, durationMs: cancelDuration, rows: cancelRows });
  await click('[data-testid="construction-rows-settings-trigger"]');
  start=Date.now();
  await click('[data-testid="construction-action-unpivot-rows"]');
  await waitForBrowser(() => (document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.disabled===false));
  await click('input[aria-label="Unpivot Specimen ID"]');
  await proposal('confirmed-related-chain-unpivot-preview',start,unpivotExpected);
  const appliedUnpivot = await apply(unpivotExpected);
  assert.deepEqual(doc(appliedUnpivot.state).population, expandedDocument.population,
    'Apply must preserve the exact source population binding through Unpivot');
  assert(appliedUnpivot.state.draftVersion > expandedVersion,
    'Applying Unpivot must advance the saved draft version');
  assert.notEqual(appliedUnpivot.state.draftDigest, expandedDigest,
    'Applying Unpivot must advance the saved draft digest');
  const unpivot=doc(builder).construction.steps.find(s=>s.operation.kind==='UNPIVOT');
  assert(unpivot);
  const relatedSourceStep = relatedSteps.at(-1);
  const sourceColumnID = directColumn.columnId;
  const relatedSourceOutputs = relatedSourceStep.outputs;
  const selectedSourceOutput = relatedSourceOutputs.find(output => output.id === sourceColumnID);
  assert(selectedSourceOutput, 'The final Related stage must expose the selected source column by its stable columnId');
  assert.deepEqual(unpivot.inputs, [{ kind: 'STEP_OUTPUT', stepId: relatedSourceStep.id }],
    'Unpivot must consume the exact final Related compiler stage');
  assert.deepEqual(unpivot.operation.unpivot.inputs.map(input => input.columnId), [sourceColumnID],
    'Unpivot must consume the exact direct Specimen ID column');
  const unpivotOutputIDs = new Set(unpivot.outputs.map(output => output.id));
  assert(unpivotOutputIDs.has(unpivot.operation.unpivot.keyOutputColumnId));
  assert(unpivotOutputIDs.has(unpivot.operation.unpivot.valueOutputColumnId));
  const relatedColumnIDs = relatedSourceOutputs.filter(output => output.id !== sourceColumnID).map(output => output.id);
  const expectedUnpivotColumnIDs = [
    ...relatedColumnIDs,
    unpivot.operation.unpivot.keyOutputColumnId,
    unpivot.operation.unpivot.valueOutputColumnId,
  ];
  assert.deepEqual(doc(builder).columns.map(sourceColumnSchema), expandedDocument.columns.map(sourceColumnSchema),
    'The source projection must remain unchanged when Unpivot transforms the final stage');
  const usedOutputNames = new Set(relatedSourceOutputs.map(output => output.name.toLowerCase()));
  const uniqueOutputName = baseName => {
    if (!usedOutputNames.has(baseName)) return baseName;
    let suffix = 2;
    while (usedOutputNames.has(`${baseName}_${suffix}`)) suffix += 1;
    return `${baseName}_${suffix}`;
  };
  const expectedKeyOutputName = uniqueOutputName('variable');
  usedOutputNames.add(expectedKeyOutputName);
  const expectedValueOutputName = uniqueOutputName('value');
  const keyOutput = unpivot.outputs.find(output => output.id === unpivot.operation.unpivot.keyOutputColumnId);
  const valueOutput = unpivot.outputs.find(output => output.id === unpivot.operation.unpivot.valueOutputColumnId);
  const expectedKeyOutput = { id: unpivot.operation.unpivot.keyOutputColumnId, name: expectedKeyOutputName,
    label: 'Variable', type: 'string' };
  const expectedValueOutput = { id: unpivot.operation.unpivot.valueOutputColumnId, name: expectedValueOutputName,
    label: 'Value', ...(selectedSourceOutput.type === undefined ? {} : { type: selectedSourceOutput.type }) };
  const expectedUnpivotSchema = [
    ...relatedSourceOutputs.filter(output => output.id !== sourceColumnID).map(finalOutputSchema),
    finalOutputSchema(expectedKeyOutput),
    finalOutputSchema(expectedValueOutput),
  ];
  assert.deepEqual(
    { keyName: keyOutput?.name, keyLabel: keyOutput?.label, valueName: valueOutput?.name, valueLabel: valueOutput?.label },
    { keyName: expectedKeyOutputName, keyLabel: 'Variable', valueName: expectedValueOutputName, valueLabel: 'Value' },
    'Unpivot outputs must keep the declared default labels and deterministic unique names');
  assert.deepEqual(unpivot.outputs.map(finalOutputSchema), expectedUnpivotSchema,
    'Apply must preserve exact ordered retained and generated final-stage output metadata');
  recordLifecycleCheck('persistence',
    'Apply persists Unpivot with the exact source-column and compiler-stage binding',
    unpivot.inputs[0]?.stepId === relatedSourceStep.id &&
      JSON.stringify(unpivot.operation.unpivot.inputs.map(input => input.columnId)) === JSON.stringify([sourceColumnID]) &&
      unpivotOutputIDs.has(unpivot.operation.unpivot.keyOutputColumnId) &&
      unpivotOutputIDs.has(unpivot.operation.unpivot.valueOutputColumnId) &&
      JSON.stringify(unpivot.outputs.map(output => output.id)) === JSON.stringify(expectedUnpivotColumnIDs) &&
      JSON.stringify(unpivot.outputs.map(finalOutputSchema)) === JSON.stringify(expectedUnpivotSchema) &&
      keyOutput?.name === expectedKeyOutputName && keyOutput?.label === 'Variable' &&
      valueOutput?.name === expectedValueOutputName && valueOutput?.label === 'Value',
    { unpivotStepID: unpivot.id, relatedSourceStepID: relatedSourceStep.id, consumedColumnID: sourceColumnID,
      keyOutputColumnID: unpivot.operation.unpivot.keyOutputColumnId,
      valueOutputColumnID: unpivot.operation.unpivot.valueOutputColumnId,
      expectedOutputColumnIDs: expectedUnpivotColumnIDs, actualOutputColumnIDs: unpivot.outputs.map(output => output.id),
      expectedOutputSchema: expectedUnpivotSchema, actualOutputSchema: unpivot.outputs.map(finalOutputSchema),
      outputMetadata: { key: keyOutput, value: valueOutput },
      relatedColumnIDs, appliedRows: appliedUnpivot.rows, durationMs: appliedUnpivot.durationMs,
      draftVersion: appliedUnpivot.state.draftVersion, draftDigest: appliedUnpivot.state.draftDigest,
      previousDraftVersion: expandedVersion, previousDraftDigest: expandedDigest });
  recordLifecycleCheck('correctness',
    'applied Unpivot rows and output identities match the complete bounded source oracle',
    JSON.stringify(appliedUnpivot.rows) === JSON.stringify(unpivotExpected) &&
      JSON.stringify(unpivot.outputs.map(output => output.id)) === JSON.stringify(expectedUnpivotColumnIDs) &&
      JSON.stringify(unpivot.outputs.map(finalOutputSchema)) === JSON.stringify(expectedUnpivotSchema) &&
      keyOutput?.name === expectedKeyOutputName && keyOutput?.label === 'Variable' &&
      valueOutput?.name === expectedValueOutputName && valueOutput?.label === 'Value',
    { rows: appliedUnpivot.rows, expectedRows: unpivotExpected,
      expectedOutputColumnIDs: expectedUnpivotColumnIDs, outputColumnIDs: unpivot.outputs.map(output => output.id),
      expectedOutputSchema: expectedUnpivotSchema, outputSchema: unpivot.outputs.map(finalOutputSchema),
      outputMetadata: { key: keyOutput, value: valueOutput } });
  const unpivotReload = await open(unpivotExpected);
  const reloadedDocument = unpivotReload.state.workspace.documents.find(d => d.output.id === outputId);
  const reloadedUnpivot = reloadedDocument.construction.steps.find(step => step.id === unpivot.id);
  assert(reloadedUnpivot, 'Builder reload must preserve the exact Unpivot step identity');
  assert.equal(unpivotReload.state.draftVersion, appliedUnpivot.state.draftVersion);
  assert.equal(unpivotReload.state.draftDigest, appliedUnpivot.state.draftDigest);
  assert.deepEqual(reloadedUnpivot.operation.unpivot.inputs, unpivot.operation.unpivot.inputs);
  assert.deepEqual(reloadedDocument.columns.map(sourceColumnSchema), expandedDocument.columns.map(sourceColumnSchema),
    'Builder reload must preserve the exact source projection');
  assert.deepEqual(reloadedUnpivot.outputs.map(output => output.id), expectedUnpivotColumnIDs);
  assert.deepEqual(reloadedUnpivot.outputs.map(finalOutputSchema), expectedUnpivotSchema,
    'Builder reload must preserve exact ordered retained and generated final-stage output metadata');
  assert.deepEqual(reloadedDocument.population, expandedDocument.population,
    'Reload must preserve the exact source population binding through Unpivot');
  assert.deepEqual(
    reloadedUnpivot.outputs.filter(output => [unpivot.operation.unpivot.keyOutputColumnId, unpivot.operation.unpivot.valueOutputColumnId].includes(output.id))
      .map(output => ({ id: output.id, name: output.name, label: output.label })),
    [
      { id: unpivot.operation.unpivot.keyOutputColumnId, name: expectedKeyOutputName, label: 'Variable' },
      { id: unpivot.operation.unpivot.valueOutputColumnId, name: expectedValueOutputName, label: 'Value' },
    ], 'Reload must preserve exact Unpivot output identities and defaults');
  recordLifecycleCheck('persistence',
    'Builder reload preserves exact Unpivot schema, bindings, step identity, and rows',
    reloadedUnpivot.id === unpivot.id && JSON.stringify(unpivotReload.rows) === JSON.stringify(unpivotExpected) &&
      JSON.stringify(reloadedUnpivot.outputs.map(output => output.id)) === JSON.stringify(expectedUnpivotColumnIDs) &&
      JSON.stringify(reloadedUnpivot.outputs.map(finalOutputSchema)) === JSON.stringify(expectedUnpivotSchema),
    { stepID: reloadedUnpivot.id, inputColumnIDs: reloadedUnpivot.operation.unpivot.inputs.map(input => input.columnId),
      expectedOutputColumnIDs: expectedUnpivotColumnIDs, outputColumnIDs: reloadedUnpivot.outputs.map(output => output.id),
      expectedOutputSchema: expectedUnpivotSchema, outputSchema: reloadedUnpivot.outputs.map(finalOutputSchema),
      outputMetadata: reloadedUnpivot.outputs.filter(output => [unpivot.operation.unpivot.keyOutputColumnId, unpivot.operation.unpivot.valueOutputColumnId].includes(output.id)),
      rows: unpivotReload.rows, durationMs: unpivotReload.durationMs });
  const savedUnpivot = structuredClone(unpivot);
  start=Date.now();
  await click(`[data-testid="construction-history-step-${unpivot.id}"]`);
  await click(`[data-testid="construction-edit-step-${unpivot.id}"]`);
  await waitForBrowser(() => (document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.checked));
  const editSelection = await browserEval(() => ({
    checked: document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.checked ?? false,
    disabled: document.querySelector('input[aria-label="Unpivot Specimen ID"]')?.disabled ?? true,
  }));
  assert.deepEqual(editSelection, { checked: true, disabled: false });
  await click('[data-testid="construction-unpivot-advanced"] summary');
  await selectOption('select[aria-label="Unpivot null row policy"]','DROP');
  const editPreview = await proposal('edit-unpivot-policy-preview',start,unpivotExpected);
  recordLifecycleCheck('correctness',
    'saved Unpivot edit reopens its exact source and previews the replacement null policy',
    editSelection.checked && editPreview.status === 'ready' && JSON.stringify(editPreview.rows) === JSON.stringify(unpivotExpected),
    { stepID: unpivot.id, inputColumnIDs: savedUnpivot.operation.unpivot.inputs.map(input => input.columnId),
      policy: 'DROP', rows: editPreview.rows });
  const editedApply = await apply(unpivotExpected);
  const editedReload = await open(unpivotExpected);
  const editedUnpivot = doc(builder).construction.steps.find(s=>s.id===unpivot.id);
  const editedReloadDocument = editedReload.state.workspace.documents.find(d=>d.output.id===outputId);
  const reloadedEditedUnpivot = editedReloadDocument.construction.steps.find(step=>step.id===unpivot.id);
  assert.equal(editedUnpivot.operation.unpivot.nullRowPolicy,'DROP');
  assert.deepEqual(editedUnpivot.operation.unpivot.inputs, savedUnpivot.operation.unpivot.inputs,
    'Editing the null policy must preserve the exact Unpivot source-column binding');
  assert.equal(editedReload.state.draftVersion, editedApply.state.draftVersion);
  assert.equal(editedReload.state.draftDigest, editedApply.state.draftDigest);
  assert.deepEqual(editedUnpivot.outputs.map(finalOutputSchema), expectedUnpivotSchema,
    'Editing the null policy must preserve the exact final-stage output schema');
  assert.deepEqual(reloadedEditedUnpivot.outputs.map(finalOutputSchema), expectedUnpivotSchema,
    'Reload after editing must preserve the exact final-stage output schema');
  assert.deepEqual(editedReloadDocument.columns.map(sourceColumnSchema), expandedDocument.columns.map(sourceColumnSchema),
    'Editing Unpivot must not change the source projection');
  recordLifecycleCheck('persistence',
    'edited Unpivot policy applies and reloads with exact schema and raw rows',
    editedUnpivot.operation.unpivot.nullRowPolicy === 'DROP' && editedUnpivot.id === unpivot.id &&
      JSON.stringify(editedReload.rows) === JSON.stringify(unpivotExpected),
    { stepID: editedUnpivot.id, policy: editedUnpivot.operation.unpivot.nullRowPolicy,
      inputColumnIDs: editedUnpivot.operation.unpivot.inputs.map(input => input.columnId),
      outputColumnIDs: editedUnpivot.outputs.map(output=>output.id), rows: editedReload.rows,
      applyDurationMs: editedApply.durationMs, reloadDurationMs: editedReload.durationMs });
  const beforeFilter=builder;
  const filterPanel='[data-testid="construction-filter-editor"]';
  const configureMissing=async()=>{
    start=Date.now();
    await click('[data-testid="construction-action-keep-rows"]');
    await waitForBrowser(({ selector }) => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, { selector: `${filterPanel} select[aria-label="Condition"]` });
    const options=await browserEval(({ selector }) => [...document.querySelector(`${selector} select[aria-label="Column"]`).options].map(option => ({ value: option.value, label: option.textContent })), { selector: filterPanel });
    report.filterColumns=options;
    const value=options.find(o=>/^Value(?: \(|$)/.test(o.label));
    assert(value,'Unpivot Value must be available to Filter: '+JSON.stringify(options));
    await selectOption(filterPanel+' select[aria-label="Column"]',value.value);
    await selectOption(filterPanel+' select[aria-label="Condition"]','EQUALS');
    await selectOption(filterPanel+' select[aria-label="Condition"]','MISSING');
  };
  await configureMissing();
  await proposal('unpivot-value-missing-preview',start,[]);
  const filterCancelStart = Date.now();
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  builder = await api(base+'/builder');
  assert.deepEqual(builder.workspace,beforeFilter.workspace);
  assert.deepEqual(await rendered(unpivotExpected),unpivotExpected);
  recordRender('Cancel Unpivot Value Filter to exact saved Unpivot rows', filterCancelStart);
  await configureMissing();
  await proposal('confirmed-unpivot-value-missing-preview',start,[]);
  const missingApply = await apply([]);
  assert(missingApply.state.draftVersion > beforeFilter.draftVersion,
    'Applying the MISSING filter must advance the saved draft version');
  assert.notEqual(missingApply.state.draftDigest, beforeFilter.draftDigest,
    'Applying the MISSING filter must advance the saved draft digest');
  const missingReload = await open([]);
  const filter = doc(builder).construction.steps.find(step => step.operation.kind === 'FILTER');
  assert(filter);
  assert.equal(filter.operation.filter.operator, 'MISSING');
  assert.equal(Object.hasOwn(filter.operation.filter, 'values'), false,
    'MISSING filters must not persist comparison values');
  const expectedValueColumnID = report.filterColumns.find(column => /^Value(?: \(|$)/.test(column.label))?.value;
  assert(expectedValueColumnID, 'The Unpivot Value column must be available to Filter');
  assert.equal(filter.operation.filter.columnId, expectedValueColumnID);
  assert.deepEqual(missingApply.rows, []);
  assert.deepEqual(missingReload.rows, []);
  assert.equal(missingReload.state.draftVersion, missingApply.state.draftVersion);
  assert.equal(missingReload.state.draftDigest, missingApply.state.draftDigest);
  const missingFilterEvidence = {
    stepID: filter.id, columnID: filter.operation.filter.columnId, operator: filter.operation.filter.operator,
    hasComparisonValues: Object.hasOwn(filter.operation.filter, 'values'), rowsAfterApply: missingApply.rows,
    rowsAfterReload: missingReload.rows, previousDraftVersion: beforeFilter.draftVersion,
    previousDraftDigest: beforeFilter.draftDigest, draftVersion: missingReload.state.draftVersion,
    draftDigest: missingReload.state.draftDigest,
  };
  start=Date.now();
  await click(`[data-testid="construction-history-step-${filter.id}"]`);
  await click(`[data-testid="construction-edit-step-${filter.id}"]`);
  await waitForBrowser(({ selector }) => { const control = document.querySelector(selector); return Boolean(control) && !control.disabled; }, { selector: `${filterPanel} select[aria-label="Condition"]` });
  await selectOption(filterPanel+' select[aria-label="Condition"]','EQUALS');
  await click(filterPanel+' input[aria-label="Value"]');
  await fill(filterPanel+' input[aria-label="Value"]', source.id);
  await proposal('unpivot-value-equality-preview',start,unpivotExpected);
  const equalityApply = await apply(unpivotExpected);
  const equalityReload = await open(unpivotExpected);
  const editedFilter = doc(builder).construction.steps.find(step => step.operation.kind === 'FILTER');
  assert(editedFilter);
  assert.equal(editedFilter.id, filter.id, 'Editing the saved filter must preserve its step identity');
  assert.equal(editedFilter.operation.filter.columnId, expectedValueColumnID);
  assert.equal(editedFilter.operation.filter.operator, 'EQUALS');
  assert.deepEqual(editedFilter.operation.filter.values, [{ kind: 'STRING', string: source.id }]);
  assert(equalityApply.state.draftVersion > missingApply.state.draftVersion,
    'Applying the EQUALS edit must advance the saved draft version');
  assert.notEqual(equalityApply.state.draftDigest, missingApply.state.draftDigest,
    'Applying the EQUALS edit must advance the saved draft digest');
  assert.equal(equalityReload.state.draftVersion, equalityApply.state.draftVersion);
  assert.equal(equalityReload.state.draftDigest, equalityApply.state.draftDigest);
  recordLifecycleCheck('correctness',
    'Unpivot Value missing/equality Filter lifecycle remains exact through reload and restoration',
    missingFilterEvidence.operator === 'MISSING' && missingFilterEvidence.hasComparisonValues === false &&
      JSON.stringify(missingFilterEvidence.rowsAfterApply) === '[]' &&
      JSON.stringify(missingFilterEvidence.rowsAfterReload) === '[]' &&
      missingFilterEvidence.draftVersion > missingFilterEvidence.previousDraftVersion &&
      missingFilterEvidence.draftDigest !== missingFilterEvidence.previousDraftDigest &&
      editedFilter.operation.filter.operator === 'EQUALS' &&
      JSON.stringify(editedFilter.operation.filter.values) === JSON.stringify([{ kind: 'STRING', string: source.id }]) &&
      JSON.stringify(equalityReload.rows) === JSON.stringify(unpivotExpected) &&
      equalityApply.state.draftVersion > missingFilterEvidence.draftVersion &&
      equalityApply.state.draftDigest !== missingFilterEvidence.draftDigest &&
      equalityReload.state.draftVersion === equalityApply.state.draftVersion &&
      equalityReload.state.draftDigest === equalityApply.state.draftDigest,
    { missingFilter: missingFilterEvidence,
      editedFilter: { stepID: editedFilter.id, columnID: editedFilter.operation.filter.columnId,
        operator: editedFilter.operation.filter.operator, values: editedFilter.operation.filter.values,
        rowsAfterApply: equalityApply.rows, rowsAfterReload: equalityReload.rows,
        draftVersionAfterApply: equalityApply.state.draftVersion, draftDigestAfterApply: equalityApply.state.draftDigest,
        draftVersionAfterReload: equalityReload.state.draftVersion, draftDigestAfterReload: equalityReload.state.draftDigest },
      availableColumns: report.filterColumns });
  const beforeRemoval=builder;
  const removeUnpivot=async()=>{
    start=Date.now();
    await click(`[data-testid="construction-history-step-${unpivot.id}"]`);
    await click(`[data-testid="construction-remove-step-${unpivot.id}"]`);
    await proposal('remove-unpivot-and-dependent-filter-preview',start,expected);
    const removed=await browserEval(() => [...document.querySelectorAll('[data-testid^="construction-removal-step-"]')].map(element => element.dataset.testid));
    assert(removed.includes('construction-removal-step-'+unpivot.id),JSON.stringify(removed));
    assert(removed.includes('construction-removal-step-'+filter.id),'Removal warning must name the dependent filter: '+JSON.stringify(removed));
  };
  const removalCancelStart = Date.now();
  await removeUnpivot();
  await click('[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(() => (!document.querySelector('[data-testid="construction-proposal-panel"]')));
  builder = await api(base+'/builder');
  assert.deepEqual(builder.workspace,beforeRemoval.workspace);
  assert.equal(builder.draftVersion, beforeRemoval.draftVersion);
  assert.equal(builder.draftDigest, beforeRemoval.draftDigest);
  const removalCancelRows = await rendered(unpivotExpected);
  const removalCancelDuration = recordRender('Cancel Unpivot removal to exact saved rows', removalCancelStart);
  recordLifecycleCheck('persistence',
    'Canceling Unpivot removal preserves the saved construction and exact rows',
    JSON.stringify(builder.workspace) === JSON.stringify(beforeRemoval.workspace) &&
      builder.draftVersion === beforeRemoval.draftVersion && builder.draftDigest === beforeRemoval.draftDigest &&
      JSON.stringify(removalCancelRows) === JSON.stringify(unpivotExpected),
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
      rows: removalCancelRows, durationMs: removalCancelDuration });
  await removeUnpivot();
  const restoreApply = await apply(expected);
  const restoreReload = await open(expected);
  assert.deepEqual(doc(builder).construction,doc(expanded).construction,'Removing Unpivot must restore the exact related chain');
  assert.deepEqual(doc(builder).columns, expandedDocument.columns, 'Removing Unpivot must restore the exact related column identities and schema');
  assert.deepEqual(doc(builder).population, expandedDocument.population, 'Removing Unpivot must restore the exact source population binding');
  assert.deepEqual(restoreReload.rows, expected, 'Reload after Unpivot removal must restore exact Related rows');
  assert(builder.draftVersion > beforeRemoval.draftVersion, 'Applying removal must advance the draft version');
  assert.notEqual(builder.draftDigest, beforeRemoval.draftDigest, 'Applying removal must advance the draft digest');
  recordLifecycleCheck('persistence',
    'removing Unpivot and reloading restores exact Related construction, schema, bindings, and rows',
    JSON.stringify(doc(builder).construction) === JSON.stringify(expandedDocument.construction) &&
      JSON.stringify(doc(builder).columns) === JSON.stringify(expandedDocument.columns) &&
      JSON.stringify(doc(builder).population) === JSON.stringify(expandedDocument.population) &&
      JSON.stringify(restoreReload.rows) === JSON.stringify(expected) && builder.draftVersion > beforeRemoval.draftVersion &&
      builder.draftDigest !== beforeRemoval.draftDigest,
    { relatedStepIDs: relatedSteps.map(step => step.id), restoredStepIDs: doc(builder).construction.steps.map(step => step.id),
      restoredColumns: doc(builder).columns, restoredPopulation: doc(builder).population, rows: restoreReload.rows,
      applyDurationMs: restoreApply.durationMs, reloadDurationMs: restoreReload.durationMs });

  report.nativeRequestTerminalDrain = await flushRelatedUnpivotNativeRequests(fixtureRequestCapture, cda.report);
  includeFixtureDiagnostics(report);
  const unexpectedOwnedErrors = (cda.report.errors ?? []).filter(item => item.expected !== true &&
    !item.expectedCancellation && !item.expectedInjectedFault && !item.expectedHttpFailure);
  const unexpectedDiagnostics = [
    ...(cda.diagnostics.pageErrors ?? []).map(item => ({ kind: 'runtime', message: item.message })),
    ...(cda.diagnostics.console ?? []).map(item => ({ kind: 'console', message: item.text })),
    ...(cda.diagnostics.networkFailures ?? []).map(item => ({ kind: 'network', path: item.url, failure: item.failure })),
    ...(cda.diagnostics.httpFailures ?? []).map(item => ({ kind: 'http', url: item.url, status: item.status })),
  ];
  recordLifecycleCheck('performance',
    'All native action and action-to-render checkpoints complete within five seconds',
    report.cases.every(item => item.durationMs <= 5000) && cda.report.actions.every(item => item.status === 'passed' && item.elapsedMs <= 5000),
    { workflowCheckpoints: report.cases.map(({ name, durationMs }) => ({ name, durationMs })),
      maximumNativeActionMs: Math.max(0, ...cda.report.actions.map(item => item.elapsedMs)) });
  recordLifecycleCheck('correctness',
    'No unexpected native network, module, or browser errors occurred',
    unexpectedDiagnostics.length === 0 && unexpectedOwnedErrors.length === 0,
    { unexpectedDiagnostics, unexpectedOwnedErrors });
  assert.deepEqual(report.errors,[]);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); report.__nativeFailure = true;
if (page) {
    const action = report.activeAction ?? controls?.lastAction;
    await captureFailure(error, { scenario: explorer, ...(action ? { action, elapsedMs: report.activeAction ? Date.now() - report.activeAction.startedAt : report.lastElapsedMs ?? action.elapsedMs } : {}), draft: builder ? { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest } : undefined, latestDiagnostic: report.errors.at(-1) });
  }
  report.failureUI = page ? await browserEval(() => document.body.innerText).catch(String) : undefined;
} finally {
  if (frozenApiBuild) {
    try { report.apiBuildFreeze = { ...report.apiBuildFreeze, ...await frozenApiBuild.assertUnchanged() }; }
    catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.apiBuildFreeze = { ...report.apiBuildFreeze, unchanged: false, invalidatesRun: true, productFailure: false, error: String(error), reason: error.reason, before: error.before, after: error.after }; report.__nativeFailure = true; }
  }
  if (sourceFreeze) {
    try { report.sourceFreeze = { ...report.sourceFreeze, ...await sourceFreeze.assertUnchanged(), finishedAt: new Date().toISOString() }; }
    catch (error) { report.priorStatus = report.status; report.status = 'invalidated'; report.sourceFreeze = { ...report.sourceFreeze, unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false, error: String(error), finishedAt: new Date().toISOString() }; report.__nativeFailure = true; }
  }
  if (report.sourceFingerprint) {
    report.sourceFingerprint.after = sourceFingerprint(sourceRoot);
    report.sourceFingerprint.unchanged = report.sourceFingerprint.after.sha256 === report.sourceFingerprint.before.sha256 && report.sourceFingerprint.after.files === report.sourceFingerprint.before.files;
    report.sourceFingerprint.invalidatesRun = !report.sourceFingerprint.unchanged;
    if (!report.sourceFingerprint.unchanged) { report.priorStatus = report.status; report.status = 'invalidated'; report.__nativeFailure = true; }
  }

  if (report.status === 'passed' && report.errors.length) {
    const error = new Error(`Unexpected browser diagnostics: ${JSON.stringify(report.errors)}`);
    report.status = 'failed';
    report.error = String(error.stack);
    report.__nativeFailure = true;
    const action = report.activeAction ?? controls?.lastAction;
if (page) await captureFailure(error, { scenario: explorer, ...(action ? { action, elapsedMs: report.lastElapsedMs ?? action.elapsedMs } : {}), draft: builder ? { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest } : undefined, latestDiagnostic: report.errors.at(-1) });
  }
  report.browserDiagnostics = cda.diagnostics;
  report.incidentalAssetFailures = cda.diagnostics.assetFailures ?? [];
  report.actions = cda.report.actions ?? [];
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-related-unpivot-browser.mjs workflow failed');
  await cda.attachReport('verify-cda-related-unpivot-browser.mjs', report);
  return report;
}
