import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';

import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { collectPreviewRows } from './lib/playwright-preview-rows.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';


export async function runPivotCategoryCycleBrowserWorkflow({ page, cda }, originalArgs = {}) {
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
const explorer = cda.explorer;
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const relatedApplyOnly = originalArgs.relatedApplyOnly === true || (cda.env?.LOOM_RELATED_APPLY_ONLY ?? process.env.LOOM_RELATED_APPLY_ONLY) === '1';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
assert((cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE), 'Set LOOM_ARANGO_DATABASE for the isolated CDA source database.');
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer: (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER),
  composeProject: (cda.target.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT), sourceRoot, arangoContainer: (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER),
  clickhouseContainer: (cda.target.clickhouseContainer ?? cda.env?.LOOM_CLICKHOUSE_CONTAINER) });
const apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp((cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER)));
const report = {
  explorer,
  ...(relatedApplyOnly ? { mode: 'relatedApplyOnly' } : {}),
  cases: [],
  errors: [],
  requests: [],
  authoringRequests: [],
  nativeRequests: [],
  sourceFingerprint: { before: sourceFingerprint(sourceRoot) }, apiBuildIdentity: apiBuildFreeze.initial,
  started: new Date().toISOString(),
};
const fixtureRequestCapture = cda.captureRequests(base, { apiOrigin: uiOrigin });
const inspectPage = (_page, inspect, argument) => cda.inspect(inspect, argument);
const waitForObservable = (page, predicate, argumentOrTimeout, timeoutArgument = 5000) => {
  const argument = typeof argumentOrTimeout === 'number' ? undefined : argumentOrTimeout;
  const timeout = typeof argumentOrTimeout === 'number' ? argumentOrTimeout : timeoutArgument;
  return cda.wait(predicate, argument ?? {}, Math.min(timeout, 5000));
};
const waitForVisible = (page, selector, timeout = 5000) => page.locator(selector).waitFor({ state: 'visible', timeout });
const waitForHidden = (page, selector, timeout = 5000) => page.locator(selector).waitFor({ state: 'hidden', timeout });
const waitForEnabled = async (page, selector, timeout = 5000) => {
  const locator = page.locator(selector);
  await locator.waitFor({ state: 'visible', timeout });
  await page.waitForFunction(value => { const element = document.querySelector(value); return Boolean(element && !element.disabled); }, selector, { timeout });
};
const gotoPage = (_page, url) => cda.navigate(url);
const clickNative = (_page, selector, identity = {}) => cda.click(selector, identity, 5000);
const selectNative = async (_page, selector, value) => {
  const locator = page.locator(selector);
  await cda.selectOption(selector, value);
  assert.equal(await locator.inputValue(), String(value), `Selected value must be applied to ${selector}`);
};
let browserRequestCapture;
const syncAuthoringRequests = () => {
  const endpoints = new Set(['commands', 'reconcile', 'preview', 'construction-proposals', 'construction-category-discoveries']);
  report.authoringRequests.splice(0, report.authoringRequests.length, ...report.nativeRequests
    .filter(request => endpoints.has(request.path.split('/').at(-1)))
    .map(request => ({ ...request, endpoint: request.path.split('/').at(-1), url: `${request.origin}${request.path}`, requestStartedAtMs: request.startedAt,
      responseFinishedAtMs: request.completedAt, durationMs: request.completedAt - request.startedAt,
      requestDraftVersion: request.body?.expectedDraftVersion ?? request.body?.draftVersion, requestDraftDigest: request.body?.expectedDraftDigest ?? request.body?.draftDigest,
      requestOutputId: request.body?.outputId, requestReceiptId: request.body?.receiptId,
      responseDraftVersion: request.response?.draftVersion, responseDraftDigest: request.response?.draftDigest,
      responseReceiptId: request.response?.receiptId, responseOutputId: request.response?.outputId,
      responseRowCount: request.response?.rowCount ?? request.response?.rows?.length,
      ...(request.failure ? { loadingFailure: { errorText: request.failure, canceled: request.failure === 'net::ERR_ABORTED' } } : {}),
    })));
  correlateAuthoringRequests();
};
await mkdir(evidence, { recursive: true });
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(fileURLToPath(new URL('..', import.meta.url)));
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };
let builder, outputId;
let editedPivotPresentation=false;
const correlateAuthoringRequests = () => {
  for (const reconcile of report.authoringRequests.filter(request => request.endpoint === 'reconcile' && request.responseReceiptId)) {
    const command = [...report.authoringRequests].reverse().find(request =>
      request.endpoint === 'commands' &&
      request.responseDraftVersion === reconcile.requestDraftVersion &&
      request.responseDraftDigest === reconcile.requestDraftDigest &&
      request.requestStartedAtMs <= reconcile.requestStartedAtMs,
    );
    if (command) reconcile.commandRequestId = command.requestId;
    for (const preview of report.authoringRequests.filter(request =>
      request.endpoint === 'preview' && request.requestReceiptId === reconcile.responseReceiptId,
    )) {
      preview.reconcileRequestId = reconcile.requestId;
      preview.reconciledDraftVersion = reconcile.requestDraftVersion;
      preview.reconciledDraftDigest = reconcile.requestDraftDigest;
      if (reconcile.commandRequestId) preview.commandRequestId = reconcile.commandRequestId;
    }
  }
};
const summarizeAuthoringRequest = request => request ? ({
  requestId: request.requestId,
  status: request.status,
  requestDraftVersion: request.requestDraftVersion,
  requestDraftDigest: request.requestDraftDigest,
  responseDraftVersion: request.responseDraftVersion,
  responseDraftDigest: request.responseDraftDigest,
  requestOutputId: request.requestOutputId,
  responseOutputId: request.responseOutputId,
  requestReceiptId: request.requestReceiptId,
  responseReceiptId: request.responseReceiptId,
  responseRowCount: request.responseRowCount,
  durationMs: request.durationMs,
}) : undefined;
const relatedApplyRequestTrace = () => {
  const applies = report.authoringRequests.filter(request =>
    request.endpoint === 'commands' &&
    request.body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL'),
  );
  const secondApply = applies[1];
  const secondReconcile = secondApply
    ? report.authoringRequests.find(request =>
        request.endpoint === 'reconcile' && request.commandRequestId === secondApply.requestId,
      )
    : undefined;
  const secondPreview = secondReconcile
    ? report.authoringRequests.find(request =>
        request.endpoint === 'preview' && request.reconcileRequestId === secondReconcile.requestId,
      )
    : undefined;
  return {
    proposalApplies: applies.map(summarizeAuthoringRequest),
    secondReconcile: summarizeAuthoringRequest(secondReconcile),
    secondPreview: summarizeAuthoringRequest(secondPreview),
  };
};
const drainPendingResponseReads = async () => {
  await browserRequestCapture?.flush();
  syncAuthoringRequests();
};
const captureFailureDOM = () => inspectPage(page, () => {
return (() => {
  const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
  const preview = document.querySelector('[data-testid="preview-table-scroll"]');
  const previewPanel = document.querySelector('[data-testid="construction-preview"]');
  const table = preview?.querySelector('[role="table"]');
  const rows = selector => [...document.querySelectorAll(selector)].slice(1).map(row =>
    [...row.querySelectorAll('[role="cell"], td')].map(cell => cell.innerText.trim()),
  );
  return {
    capturedAt: new Date().toISOString(),
    proposal: proposal ? {
      status: proposal.dataset.proposalStatus,
      proposalId: proposal.dataset.proposalId,
      text: proposal.innerText,
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row =>
        [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()),
      ),
    } : undefined,
    savedPreview: preview ? {
      visible: preview.getClientRects().length > 0,
      status: previewPanel?.getAttribute('data-preview-status'),
      receiptId: previewPanel?.getAttribute('data-preview-receipt-id'),
      outputId: previewPanel?.getAttribute('data-preview-output-id'),
      currentDraftVersion: previewPanel?.getAttribute('data-current-draft-version'),
      currentDraftDigest: previewPanel?.getAttribute('data-current-draft-digest'),
      ariaRowCount: table?.getAttribute('aria-rowcount'),
      headers: [...preview.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
      rows: rows('[data-testid="preview-table-scroll"] [role="row"]'),
      loading: document.body.innerText.includes('Loading your table…'),
    } : undefined,
    bodyText: document.body.innerText,
  };
})();
});
const captureRelatedApplyDOM = async () => {
  const previewPanel = page.locator('[data-testid="construction-preview"]');
  if (await previewPanel.count() !== 1) return undefined;
  const collected = await collectPreviewRows(page);
  const previewState = await inspectPage(page, () => {
const panel=document.querySelector('[data-testid="construction-preview"]');const preview=document.querySelector('[data-testid="preview-table-scroll"]');const table=preview?.querySelector('[role="table"]');return {
    selectedTableTestId:document.querySelector('[data-testid^="construction-table-"][aria-current="page"]')?.getAttribute('data-testid'),
    status:panel?.getAttribute('data-preview-status'),receiptId:panel?.getAttribute('data-preview-receipt-id'),outputId:panel?.getAttribute('data-preview-output-id'),
    currentDraftVersion:panel?.getAttribute('data-current-draft-version'),currentDraftDigest:panel?.getAttribute('data-current-draft-digest'),
    ariaRowCount:table?.getAttribute('aria-rowcount'),loading:document.body.innerText.includes('Loading your table…')
  };
});
  return { selectedTableTestId: previewState.selectedTableTestId, preview: {
    status: previewState.status, receiptId: previewState.receiptId, outputId: previewState.outputId,
    currentDraftVersion: previewState.currentDraftVersion, currentDraftDigest: previewState.currentDraftDigest,
    ariaRowCount: previewState.ariaRowCount, loading: previewState.loading,
    totalRows: collected.rowCount, rows: collected.rows,
  }};
};
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', 'X-Request-ID': `pivot-category-cycle-browser-${randomUUID()}` },
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
const proposal = async (name, start, expectedRows) => {
  await waitForObservable(page, () => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await inspectPage(page, () => {
const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,proposalId:p?.dataset.proposalId,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};
});
  assert.equal(result.status, 'ready', result.text);
  assert.equal(result.rows.length, Math.min(25, expectedRows.length));
  const permitted = new Set(expectedRows.map(row=>JSON.stringify(row)));
  for (const row of result.rows) assert(permitted.has(JSON.stringify(row)), 'Preview row must match an independent CDA relationship witness: '+JSON.stringify(row));
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
  await clickNative(page, '[data-testid="construction-apply-proposal"]');
  await waitForObservable(page, () => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows);
  recordRender('apply-to-render', start);
  builder = await api(base + '/builder');
};
const open = async expectedRows => {
  const start = Date.now();
  await gotoPage(page, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForVisible(page, `[data-testid="construction-table-${outputId}"]`);
  await clickNative(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(page, () => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  await rendered(expectedRows);
  recordRender('load-to-render', start);
};
const rendered = async expectedRows => {
  await waitForObservable(page, ({ rowCount }) => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount + 1)
    && !document.body.innerText.includes('Loading your table…'), { rowCount: Math.min(25, expectedRows.length) }, 10000);
  const rows = await inspectPage(page, () => {
return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(r=>[...r.querySelectorAll('[role="cell"]')].map(c=>c.innerText.trim())).filter(r=>r.length);
});
  assert(rows.length > 0 || expectedRows.length === 0);
  const savedRows = editedPivotPresentation ? expectedRows.map(row=>[row[1],row[0]]) : expectedRows;
  if(editedPivotPresentation){
    const headers=await inspectPage(page,() => {
return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(e=>e.innerText.trim());
});
    assert(headers.some(h=>h.toLowerCase().includes(report.oracle.source.id)),JSON.stringify(headers));
    assert(headers.some(h=>h.includes('OBSERVATION FHIR RESOURCE ID')),JSON.stringify(headers));
  }
  for (const row of rows) assert(savedRows.some(expected=>row.every((cell,i)=>cell===expected[i])), 'Visible saved cells must match a CDA witness: '+JSON.stringify(row));
};
try {
  const query = `FOR s IN Specimen FILTER s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LIMIT 1 RETURN {id:s.id,_id:s._id,generation:s.dataset_generation}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER), 'arangosh', '--server.database', (cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE), '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source?.id);
  report.oracle = { query, source };
  await api(root, { name: explorer, title: 'Related Pivot composition QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, source.generation);
  const node = builder.catalog.nodes.find(n => n.resourceType === 'Specimen');
  await command([{ type: 'CREATE_TABLE', title: 'Related Pivot QA', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const field = builder.catalog.candidates.find(c => c.nodeId === node.nodeId && c.fieldPath === 'id');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), { snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{ project, generation: source.generation, resourceType: 'Specimen', id: source.id }] } } });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(c => c.route.length === 0);
  assert(direct);
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  browserRequestCapture = captureCDARequests(page, { apiOrigin: uiOrigin, appOrigins: [apiOrigin, uiOrigin], ownedPathPrefix: base, report, responsePaths: /\/(?:commands|reconcile|preview|construction-proposals|construction-category-discoveries)$/ });
  page.on('request', request => {
    const captured = browserRequestCapture.byRequest.get(request);
    if (captured) captured.observedAfter = report.cases.at(-1)?.name;
  });
  const rawQuery = query => {
    const r=spawnSync('rtk',['proxy','docker','exec',(cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER),'arangosh','--server.database',(cda.target.arangoDatabase ?? cda.env?.LOOM_ARANGO_DATABASE),'--javascript.execute-string',`print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`],{encoding:'utf8',timeout:30000});
    assert.equal(r.status,0,r.stderr);return JSON.parse(r.stdout.slice(r.stdout.indexOf('[')));
  };
  let witnesses=[{anchor:source._id,values:[source.id]}];
  let expected=witnesses.map(w=>w.values);
  await open(expected);
  const chain=[
    {from:'Specimen',to:'Patient',label:'subject_Patient',field:'subject',direction:'OUTBOUND'},
    {from:'Patient',to:'Observation',label:'subject_Patient',field:'subject',direction:'INBOUND'},
  ];
  for(const hop of chain){
    const next=[];
    for(const witness of witnesses){
      const endpoint=hop.direction==='OUTBOUND'?'_from':'_to';
      const target=hop.direction==='OUTBOUND'?'_to':'_from';
      const query=`FOR e IN fhir_edge FILTER e.${endpoint} == ${JSON.stringify(witness.anchor)} AND e.label == ${JSON.stringify(hop.label)} AND e.project == "${project}" AND e.dataset_generation == "cda-fhir-v1" FILTER STARTS_WITH(e.${target}, ${JSON.stringify(hop.to+'/')}) LET d=DOCUMENT(e.${target}) FILTER d.project=="${project}" AND d.dataset_generation=="cda-fhir-v1" RETURN DISTINCT {id:d.id,_id:d._id}`;
      const matches=witness.anchor?rawQuery(query):[];
      if(matches.length)for(const match of matches)next.push({anchor:match._id,values:[...witness.values,match.id]});
      else next.push({anchor:null,values:[...witness.values,'—']});
    }
    assert(next.length<=1000,'Use a bounded CDA chain fixture');
    witnesses=next;expected=witnesses.map(w=>w.values);
    report.oracle.chain??=[];report.oracle.chain.push({hop,witnesses});
    await clickNative(page,'[data-testid="construction-rows-settings-trigger"]');
    await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false));
    await clickNative(page,'[data-testid="construction-action-related-rows"]');
    const panel='[data-testid="construction-related-expand-editor"]';
    await waitForEnabled(page, `${panel} select[aria-label="Related record type"]`);
    let start=Date.now();
    await selectNative(page,panel+' select[aria-label="Related record type"]',hop.to);
    const label=hop.from+(hop.direction==='INBOUND'?` <-[${hop.field}]- `:` -[${hop.field}]-> `)+hop.to;
    await waitForVisible(page, `${panel} input[aria-label="${label}"]`, 5000);
    await clickNative(page,panel+' input[aria-label="'+label+'"]');
    await proposal('expand-'+hop.from+'-'+hop.to,start,expected);
    await apply(expected);
  }
  if (relatedApplyOnly) {
    await drainPendingResponseReads();
    correlateAuthoringRequests();
    const savedDocument = doc(builder);
    assert(savedDocument, `Saved output ${outputId} must still exist after the second related expansion.`);
    const savedSteps = savedDocument.construction?.steps ?? [];
    assert.deepEqual(
      savedSteps.map(step => step.operation.kind),
      ['RELATED_EXPAND', 'RELATED_EXPAND'],
      'The saved output must contain exactly the two related expansion steps.',
    );
    const requestTrace = relatedApplyRequestTrace();
    assert.equal(requestTrace.proposalApplies.length, 2, 'Both related expansion proposals must be applied exactly once.');
    const [firstApply, secondApply] = requestTrace.proposalApplies;
    assert.equal(secondApply.status, 200, 'The second related expansion command must be accepted.');
    assert.equal(secondApply.requestDraftVersion, firstApply.responseDraftVersion, 'The second Apply must use the first Apply draft version.');
    assert.equal(secondApply.responseDraftVersion, builder.draftVersion, 'The saved builder must reflect the second Apply version.');
    assert.equal(requestTrace.secondReconcile?.status, 200, 'The second Apply draft must reconcile successfully.');
    assert.equal(requestTrace.secondReconcile?.requestDraftVersion, secondApply.responseDraftVersion, 'Reconciliation must target the second Apply draft.');
    assert.equal(requestTrace.secondPreview?.status, 200, 'The second Apply receipt must preview successfully.');
    assert.equal(requestTrace.secondPreview?.requestOutputId, outputId, 'The second preview must target the created output.');
    assert.equal(requestTrace.secondPreview?.requestReceiptId, requestTrace.secondReconcile?.responseReceiptId, 'The second preview must use the second Apply receipt.');
    assert.equal(requestTrace.secondPreview?.responseRowCount, Math.min(25, expected.length), 'The second preview must return the expected bounded row count.');
    const dom = await captureRelatedApplyDOM();
    assert.equal(dom.selectedTableTestId, `construction-table-${outputId}`, 'The created output must remain selected after the second Apply.');
    assert.equal(dom.preview?.status, 'ready', 'The saved preview must be ready after the second Apply.');
    assert.equal(dom.preview?.outputId, outputId, 'The visible preview must belong to the created output.');
    assert.equal(dom.preview?.receiptId, requestTrace.secondReconcile?.responseReceiptId, 'The visible preview must show the reconciled receipt.');
    assert.equal(dom.preview?.loading, false, 'The saved preview must finish loading after the second Apply.');
    assert.equal(Number(dom.preview?.ariaRowCount) - 1, Math.min(25, expected.length), 'The visible table must have the expected bounded row count.');
    assert.equal(dom.preview?.rows.length, Math.min(25, expected.length), 'Scrolling the virtualized table must render its expected bounded rows.');
    assert.deepEqual(
      dom.preview?.rows.map(row => row.rowNumber),
      Array.from({ length: Math.min(25, expected.length) }, (_, index) => index + 1),
      'Every rendered row ordinal must be collected once without collapsing repeated values.',
    );
    assert.deepEqual(
      dom.preview?.rows.map(row => row.rowIdentityLabel),
      Array.from({ length: Math.min(25, expected.length) }, (_, index) => `Inspect row ${index + 1} identity`),
      'Every rendered row must retain its native identity inspector.',
    );
    const expectedRows = new Set(expected.map(row => JSON.stringify(row)));
    for (const row of dom.preview?.rows ?? []) {
      assert(expectedRows.has(JSON.stringify(row.values)), `Saved row must match an independent CDA witness: ${JSON.stringify(row.values)}`);
    }
    report.relatedApplyOnly = {
      outputId,
      savedDraftVersion: builder.draftVersion,
      savedDraftDigest: builder.draftDigest,
      savedSteps: savedSteps.map(step => ({ id: step.id, kind: step.operation.kind })),
      expectedRowCount: expected.length,
      displayedRowLimit: Math.min(25, expected.length),
      requestTrace,
      dom,
    };
    const savedAfterSecondApply = structuredClone(builder);
    await open(expected);
    builder = await api(base + '/builder');
    assert.equal(builder.draftVersion, savedAfterSecondApply.draftVersion, 'Reload must preserve the accepted second Apply draft version.');
    assert.equal(builder.draftDigest, savedAfterSecondApply.draftDigest, 'Reload must preserve the accepted second Apply draft digest.');
    assert.deepEqual(builder.workspace, savedAfterSecondApply.workspace, 'Reload must preserve both expansions and the exact selected source membership.');
    const reloaded = await captureRelatedApplyDOM();
    assert.equal(reloaded.preview?.status, 'ready');
    assert.equal(reloaded.preview?.outputId, outputId);
    assert.equal(reloaded.preview?.loading, false);
    assert.equal(reloaded.preview?.rows.length, Math.min(25, expected.length));
    for (const row of reloaded.preview?.rows ?? []) {
      assert(expectedRows.has(JSON.stringify(row.values)), `Reloaded row must match an independent CDA witness: ${JSON.stringify(row.values)}`);
    }
    report.relatedApplyOnly.reloaded = reloaded;
  } else {
  const expanded=builder;
  assert(expected.length>1,'Require multiple related category records');
  const categoryIds=[...new Set(expected.map(row=>row[2]))];
  assert.equal(categoryIds.length,expected.length,'Each Observation category must occur once');
  const patient=expected[0][1];
  assert(expected.every(row=>row[1]===patient));
  const pivoted=[[source.id,...categoryIds.map(()=>patient)]];
  let start;
  const configurePivot=async()=>{
    await clickNative(page,'[data-testid="construction-rows-settings-trigger"]');
    await clickNative(page,'[data-testid="construction-action-pivot-rows"]');
    await waitForObservable(page,() => Boolean(document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')));
    const chooseField=async(label,prefix)=>{
      const options=await inspectPage(page, ({ label }) => [...document.querySelector(`select[aria-label="${label}"]`).options].map(o=>({value:o.value,label:o.textContent})), { label: label });
      const field=options.find(o=>o.label.startsWith(prefix));assert(field,JSON.stringify(options));
      await selectNative(page,`select[aria-label="${label}"]`,field.value);
    };
    await clickNative(page,'input[aria-label="Pivot group Specimen ID"]');
    await chooseField('Pivot category field','Observation FHIR resource ID');
    start=Date.now();
    await chooseField('Pivot values field','Patient FHIR resource ID');
  };
  await open(expected);
  await configurePivot();
  await proposal('related-pivot-preview',start,pivoted);
  await clickNative(page,'[data-testid="construction-cancel-proposal"]');
  await waitForObservable(page,() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,expanded.workspace);
  await configurePivot();
  await proposal('confirmed-related-pivot-preview',start,pivoted);
  await apply(pivoted);
  await open(pivoted);
  const pivot=doc(builder).construction.steps.find(s=>s.operation.kind==='PIVOT');assert(pivot);
  assert.deepEqual(new Set(pivot.operation.pivot.categories.map(c=>c.key.string)),new Set(categoryIds),'Persisted Pivot categories must match raw CDA Observation IDs');
  await clickNative(page,`[data-testid="construction-history-step-${pivot.id}"]`);
  await clickNative(page,`[data-testid="construction-edit-step-${pivot.id}"]`);
  await waitForObservable(page,() => Boolean(document.querySelector('[data-testid="construction-reshape-pivot-advanced"] summary')));
  await clickNative(page,'[data-testid="construction-reshape-pivot-advanced"] summary');
  start=Date.now();
  await selectNative(page,'select[aria-label="Pivot missing cell policy"]','ERROR');
  await proposal('edit-pivot-missing-policy-preview',start,pivoted);
  await apply(pivoted);
  await open(pivoted);
  assert.equal(doc(builder).construction.steps.find(s=>s.id===pivot.id).operation.pivot.missingCellPolicy,'ERROR');
  const beforeFilter=builder;
  const filterPanel='[data-testid="construction-filter-editor"]';
  const configureMissing=async()=>{
    await clickNative(page,'[data-testid="construction-action-keep-rows"]');
    await waitForObservable(page, ({ selector }) => Boolean(document.querySelector(selector) && !document.querySelector(selector).disabled), { selector: `${filterPanel} select[aria-label="Condition"]` });
    const options=await inspectPage(page, ({ selector }) => [...document.querySelector(selector).options].map(o=>({value:o.value,label:o.textContent})), { selector: `${filterPanel} select[aria-label="Column"]` });
    const category=options.find(o=>o.label.startsWith(categoryIds[0]));
    assert(category,'Pivot-generated category must be filterable: '+JSON.stringify(options));
    await selectNative(page,filterPanel+' select[aria-label="Column"]',category.value);
    await selectNative(page,filterPanel+' select[aria-label="Condition"]','EQUALS');
    start=Date.now();
    await selectNative(page,filterPanel+' select[aria-label="Condition"]','MISSING');
  };
  await configureMissing();
  await proposal('pivot-category-missing-preview',start,[]);
  await clickNative(page,'[data-testid="construction-cancel-proposal"]');
  await waitForObservable(page,() => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')));
  assert.deepEqual((await api(base+'/builder')).workspace,beforeFilter.workspace);
  await configureMissing();
  await proposal('confirmed-pivot-category-missing-preview',start,[]);
  await apply([]);
  await open([]);
  const filter=doc(builder).construction.steps.find(s=>s.operation.kind==='FILTER');assert(filter);
  await clickNative(page,`[data-testid="construction-history-step-${filter.id}"]`);
  await clickNative(page,`[data-testid="construction-edit-step-${filter.id}"]`);
  await waitForObservable(page, ({ selector }) => Boolean(document.querySelector(selector) && !document.querySelector(selector).disabled), { selector: `${filterPanel} select[aria-label="Condition"]` });
  await selectNative(page,filterPanel+' select[aria-label="Condition"]','EQUALS');
  start=Date.now();
  await cda.action('fill category filter value', page.locator(filterPanel+' input[aria-label="Value"]'), locator => locator.fill(patient, { timeout: 5000 }), { timeout: 5000, budget: 5000, editable: true });
  await proposal('pivot-category-equality-preview',start,pivoted);
  await apply(pivoted);
  await open(pivoted);
  const beforeCategoryEdit=builder;
  const editCategories=async()=>{
  await clickNative(page,`[data-testid="construction-history-step-${pivot.id}"]`);
  await clickNative(page,`[data-testid="construction-edit-step-${pivot.id}"]`);
  await waitForObservable(page,() => Boolean(document.querySelector('input[aria-label="Pivot group Specimen ID"]:not(:disabled)')));
  await clickNative(page,'input[aria-label="Pivot group Specimen ID"]');
  const categoryOptions=await inspectPage(page,() => {
return [...document.querySelector('select[aria-label="Pivot category field"]').options].map(o=>({value:o.value,label:o.textContent}));
});
  const specimenCategory=categoryOptions.find(o=>o.label==='Specimen ID');assert(specimenCategory,JSON.stringify(categoryOptions));
  await selectNative(page,'select[aria-label="Pivot category field"]',specimenCategory.value);
  await waitForObservable(page,() => Boolean(document.querySelector('input[aria-label="Pivot group Observation FHIR resource ID"]:not(:disabled)')));
  start=Date.now();
  await clickNative(page,'input[aria-label="Pivot group Observation FHIR resource ID"]');
  };
  await editCategories();
  const changed=expected.map(row=>[row[2],row[1]]);
  await proposal('pivot-category-rediscovery-preview',start,changed);
  report.categoryEditWarning=await inspectPage(page,() => {
return document.querySelector('[data-testid="construction-proposal-panel"]').innerText;
});
  await clickNative(page,'input[aria-label="Pivot group Observation FHIR resource ID"]');
  const originalOptions=await inspectPage(page,() => {
return [...document.querySelector('select[aria-label="Pivot category field"]').options].map(o=>({value:o.value,label:o.textContent}));
});
  const originalCategory=originalOptions.find(o=>o.label==='Observation FHIR resource ID');assert(originalCategory,JSON.stringify(originalOptions));
  await selectNative(page,'select[aria-label="Pivot category field"]',originalCategory.value);
  start=Date.now();
  await clickNative(page,'input[aria-label="Pivot group Specimen ID"]');
  await proposal('return-to-original-pivot-category-preview',start,pivoted);
  const restoredWarning=await inspectPage(page,() => {
return document.querySelector('[data-testid="construction-proposal-panel"]').innerText;
});
  report.restoredWarning=restoredWarning;
  assert(!restoredWarning.includes('dependent step'),'Returning to the original fields must retain the dependent filter: '+restoredWarning);
  await apply(pivoted);
  await open(pivoted);
  assert.deepEqual(doc(builder).construction,doc(beforeCategoryEdit).construction,'A field round trip must preserve authored column IDs and downstream filter');

  }
  await drainPendingResponseReads();
  correlateAuthoringRequests();
  assert.deepEqual(report.errors,[]);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); report.__nativeFailure = true;
  await captureFailure(error, { phase: report.activeAction?.label ?? report.cases.at(-1)?.name,
    elapsedMs: report.activeAction ? Date.now() - report.activeAction.startedAt : undefined,
    action: report.activeAction, requestEvidence: report.nativeRequests.slice(-20), latestCase: report.cases.at(-1) });
  await drainPendingResponseReads();
  report.savedBuilderAtFailure=await api(base + '/builder').catch(error=>({readError:String(error)}));
  report.failureDOM = page ? await captureFailureDOM().catch(error=>({captureError:String(error)})) : undefined;
  report.failureUI = report.failureDOM?.bodyText;
  await drainPendingResponseReads();
  correlateAuthoringRequests();
  report.pendingAuthoringRequests=report.authoringRequests.filter(request=>request.responseFinishedAtMs===undefined&&!request.loadingFailure).map(request=>({endpoint:request.endpoint,requestId:request.requestId,method:request.method,url:request.url,requestStartedAtMs:request.requestStartedAtMs,status:request.status,requestDraftVersion:request.requestDraftVersion,requestDraftDigest:request.requestDraftDigest,requestOutputId:request.requestOutputId,requestReceiptId:request.requestReceiptId}));
  if (relatedApplyOnly) {
    const failedDocument = report.savedBuilderAtFailure?.workspace?.documents?.find(document => document.output.id === outputId);
    report.relatedApplyOnly = {
      outputId,
      savedDraftVersion: report.savedBuilderAtFailure?.draftVersion,
      savedDraftDigest: report.savedBuilderAtFailure?.draftDigest,
      savedSteps: failedDocument?.construction?.steps?.map(step => ({ id: step.id, kind: step.operation.kind })),
      expectedRowCount: report.oracle?.chain?.at(-1)?.witnesses?.length,
      displayedRowLimit: Math.min(25, report.oracle?.chain?.at(-1)?.witnesses?.length ?? 0),
      requestTrace: relatedApplyRequestTrace(),
      dom: report.failureDOM,
    };
  }
} finally {
  await drainPendingResponseReads();
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      ...(await sourceFreeze.assertUnchanged()),
      finishedAt: sourceFreezeFinishedAt,
    };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      error: String(error),
      finishedAt: sourceFreezeFinishedAt,
    };
    report.__nativeFailure = true;
  }
  report.sourceFingerprint.after = sourceFingerprint(fileURLToPath(new URL('..', import.meta.url)));
  report.sourceFingerprint.unchanged = report.sourceFreeze?.unchanged === true;
  if (!report.sourceFingerprint.unchanged) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.__nativeFailure = true;
  }
  try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); }
  catch (error) {
    report.priorStatus = report.status; report.status = 'invalidated';
    report.apiBuildFreeze = { checked: true, unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason };
    report.__nativeFailure = true;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-pivot-category-cycle-browser.mjs workflow failed');
  await cda.attachReport('verify-cda-pivot-category-cycle-browser.mjs', report);
  return report;
}
