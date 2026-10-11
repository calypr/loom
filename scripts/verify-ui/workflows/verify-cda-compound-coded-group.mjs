import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createdExplorerScope } from '../helpers/created-explorer-scope.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import {
  CDA_COMPOUND_CODED_GROUP_CATEGORY,
  CDA_COMPOUND_CODED_GROUP_SCOPE,
  assertCdaCompoundCodedGroupRows,
  buildCdaCompoundCodedGroupExactReadQuery,
  prepareCdaCompoundCodedGroupOracle,
  validateCdaCompoundCodedGroupCandidate,
  validateCdaCompoundCodedGroupInventoryItem,
  validateCdaCompoundCodedGroupSourceOption,
} from '../helpers/cda-compound-coded-group-oracle.mjs';

const encode = value => encodeURIComponent(value);
const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const rootPath = project => `/api/v1/projects/${encode(project)}/explorers`;
const readTableDOM = () => ({
  headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
    .map(cell => (cell.textContent ?? '').trim().replace(/\s+/g, ' ')),
  rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
    .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
});

function readExactArangoRows(arangoContainer, query) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `Exact-ID Arango source reread failed: ${tidy(result.stderr || result.stdout).slice(0, 1600)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Arango source reread returned no JSON array: ${tidy(result.stdout).slice(-800)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  return rows;
}

export async function verifyCdaCompoundCodedGroup({ page, cda }) {
  const { project, generation } = cda;
  assert.equal(project, CDA_COMPOUND_CODED_GROUP_SCOPE.project);
  assert.equal(generation, CDA_COMPOUND_CODED_GROUP_SCOPE.generation);
  assert.equal(project, cda.target.fixtureProject);
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(cda.target);
  const report = cda.report;
  report.phase = 'real-CDA compound coded GROUP: direct Observation.code paired with valueCodeableConcept display';
  report.nativeRequests ??= [];
  report.workflowCheckpoints = [];
  report.actionToRender = [];
  report.oracle = { kind: 'exact project/generation/ID-scoped raw Arango Observation payload reread' };
  let capture;
  let activeAction;
  let explorerScope;
  let outputId;
  let sourceDocument;
  let oracle;
  let snapshotToken;
  let selectedFrameOption;
  let selectedInventoryItem;
  let categoryChoiceId;
  let checkboxName;
  let categoryControl;
  let initialProposal;
  let appliedCandidate;
  let editedCandidate;
  let baselineWorkspace;
  let editedWorkspace;

  const check = (dimension, name, passed, evidence = {}) => {
    cda.check(dimension, name, Boolean(passed), evidence);
    assert(passed, name);
  };
  const checkpoint = (name, startedAt, evidence = {}) => {
    const durationMs = Date.now() - startedAt;
    report.workflowCheckpoints.push({ name, durationMs });
    report.actionToRender.push({ name, durationMs });
    assert(durationMs <= 5_000, `${name} took ${durationMs}ms; native action-to-render budget is 5,000 ms.`);
    return { name, durationMs, ...evidence };
  };
  const deadlineFrom = startedAt => startedAt + 5_000;
  const remaining = deadline => Math.max(1, deadline - Date.now());
  const api = async (path, body, deadline = Date.now() + 30_000) => {
    const requestId = `cda-coded-group-${randomUUID()}`;
    const timeout = remaining(deadline);
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers: { 'X-Request-ID': requestId }, timeout })
      : await cda.request.post(`${apiOrigin}${path}`, { headers: { 'X-Request-ID': requestId }, data: body, timeout });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${tidy(text).slice(0, 1200)}`); }
    report.apiRequests ??= [];
    report.apiRequests.push({ path, method: body === undefined ? 'GET' : 'POST', requestId, status: response.status() });
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1600)}`);
    return value;
  };
  const act = async (label, locator, perform) => {
    activeAction = label;
    try {
      return await cda.action(label, locator, target => perform ? perform(target) : target.click({ timeout: 5_000 }), {
        timeout: 5_000, budget: 5_000,
      });
    } finally {
      activeAction = undefined;
    }
  };
  const wait = (predicate, args, deadline) => cda.wait(predicate, args ?? {}, remaining(deadline));
  const documentFor = builder => {
    const documents = builder?.workspace?.documents ?? [];
    assert.equal(documents.length, 1, 'The isolated Builder must contain one owned output document.');
    return documents[0];
  };
  const builderRead = (deadline = Date.now() + 30_000) => api(`${explorerScope.authoringBase}/builder`, undefined, deadline);
  const visibleRows = async (deadline, expectedRowCount) => {
    await wait(({ count }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return Boolean(table && table.getAttribute('aria-rowcount') === String(count + 1));
    }, { count: expectedRowCount }, deadline);
    const snapshot = await cda.inspect(readTableDOM);
    assert.equal(snapshot.rows.length, expectedRowCount);
    return snapshot;
  };
  const rowsBySourceID = (snapshot, document, ids) => {
    const idColumn = document.columns.find(column => column.source?.kind === 'field' && column.source.field?.path === 'id');
    assert(idColumn, 'The source table must preserve direct Observation.id.');
    const index = snapshot.headers.findIndex(header => header.toLowerCase() === idColumn.label.toLowerCase());
    assert(index >= 0, `The restored table must show ${idColumn.label}.`);
    assert.deepEqual(snapshot.rows.map(row => row[index]).sort(), [...ids].sort());
    return { idColumn, index };
  };
  const protocolGroupRows = response => {
    const candidate = response?.candidateConstruction;
    const group = candidate?.steps?.find(step => step.operation?.kind === 'GROUP');
    assert(group, 'Proposal response must contain the normalized GROUP step.');
    const key = group.operation.group.keys?.[0];
    const aggregate = group.operation.group.aggregates?.find(item => item.operation === 'COUNT_ROWS');
    assert(key && aggregate);
    const keyOutput = group.outputs.find(output => output.id === key.outputColumnId);
    const countOutput = group.outputs.find(output => output.id === aggregate.outputColumnId);
    assert(keyOutput && countOutput);
    const columns = response.preview?.columns ?? [];
    const keyColumn = columns.find(column => column.column === keyOutput.name);
    const countColumn = columns.find(column => column.column === countOutput.name);
    assert(keyColumn && countColumn, 'Proposal Preview must expose the exact GROUP key and COUNT_ROWS outputs.');
    const rows = (response.preview.rows ?? []).map(row => [row[keyColumn.column], row[countColumn.column]]);
    assertCdaCompoundCodedGroupRows(rows);
    return { rows, group, keyOutput, countOutput, keyColumn, countColumn };
  };
  const assertVisibleGroupRows = (snapshot, response, document) => {
    const group = document.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
    assert(group, 'Rendered Group rows require the saved native GROUP step.');
    const key = group.operation.group.keys[0];
    const aggregate = group.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
    const keyOutput = group.outputs.find(item => item.id === key.outputColumnId);
    const countOutput = group.outputs.find(item => item.id === aggregate.outputColumnId);
    assert(keyOutput && countOutput);
    const columns = response.preview?.columns ?? response.columns ?? [];
    const keyColumn = columns.find(column => column.column === keyOutput.name);
    const countColumn = columns.find(column => column.column === countOutput.name);
    assert(keyColumn && countColumn);
    const keyIndex = snapshot.headers.findIndex(header => header.trim().toLowerCase() === keyColumn.label.trim().toLowerCase());
    const countIndex = snapshot.headers.findIndex(header => header.trim().toLowerCase() === countColumn.label.trim().toLowerCase());
    assert(keyIndex >= 0 && countIndex >= 0, 'The visible preview must render the exact Group key and COUNT_ROWS columns.');
    const actual = snapshot.rows.map(row => [row[keyIndex] === '—' ? null : row[keyIndex], Number(row[countIndex])]);
    assertCdaCompoundCodedGroupRows(actual);
    return actual;
  };
  const sourceIDRowsFromResponse = (response, document, ids) => {
    const idSource = document.columns.find(column => column.source?.kind === 'field' && column.source.field?.path === 'id');
    assert(idSource);
    const previewColumn = (response.preview?.columns ?? response.columns ?? []).find(column => column.column === idSource.column);
    assert(previewColumn);
    const actual = (response.preview?.rows ?? response.rows ?? []).map(row => row[previewColumn.column]);
    assert.deepEqual([...actual].sort(), [...ids].sort());
    return actual;
  };
  const assertVisibleSourceRows = (snapshot, response, document, ids) => {
    sourceIDRowsFromResponse(response, document, ids);
    const idSource = document.columns.find(column => column.source?.kind === 'field' && column.source.field?.path === 'id');
    const previewColumn = (response.preview?.columns ?? response.columns ?? []).find(column => column.column === idSource.column);
    const headerIndex = snapshot.headers.findIndex(header => header.trim().toLowerCase() === previewColumn.label.trim().toLowerCase());
    assert(headerIndex >= 0, 'Rendered source restoration must expose the exact Observation.id output.');
    assert.deepEqual(snapshot.rows.map(row => row[headerIndex]).sort(), [...ids].sort());
  };
  const savedPreviewRows = (response, document) => {
    assert.equal(response?.outputId, outputId, 'Saved Preview must be bound to the exact output.');
    assert(response.receiptId, 'Saved Preview must expose its exact receipt identity.');
    const group = document.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
    if (!group) {
      sourceIDRowsFromResponse(response, document, oracle.ids);
      return { kind: 'source', rows: (response.rows ?? []).map(row => Object.values(row)) };
    }
    const key = group.operation.group.keys[0];
    const aggregate = group.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
    const keyOutput = group.outputs.find(item => item.id === key.outputColumnId);
    const countOutput = group.outputs.find(item => item.id === aggregate.outputColumnId);
    const columns = response.columns ?? [];
    const keyColumn = columns.find(column => column.column === keyOutput.name);
    const countColumn = columns.find(column => column.column === countOutput.name);
    assert(keyColumn && countColumn, 'Saved Preview must expose the exact Group key and COUNT_ROWS outputs.');
    const rows = (response.rows ?? []).map(row => [row[keyColumn.column], row[countColumn.column]]);
    assertCdaCompoundCodedGroupRows(rows);
    return { kind: 'group', rows };
  };
  const findRequest = async (fromIndex, predicate, deadline) => capture.waitForResponse(predicate, { fromIndex, timeoutMs: remaining(deadline) });
  const proposalFor = async ({ name, startedAt, fromIndex, verifyRequest, verifyResponse = true }) => {
    const deadline = deadlineFrom(startedAt);
    const entry = await findRequest(fromIndex, request => request.path === `${explorerScope.explorerRoot}/authoring/v2/construction-proposals`
      && request.method === 'POST' && request.status === 200 && request.completedAt, deadline);
    const request = capture.requestBody(entry);
    const response = capture.responseBody(entry);
    assert(request && response, `${name} requires exact native proposal request and response bodies.`);
    assert.equal(request.outputId, outputId);
    assert.equal(request.snapshotToken, snapshotToken);
    assert.equal(response.outputId, outputId);
    assert.equal(response.previewStatus, 'READY');
    assert(response.proposalId);
    await wait(({ proposalId }) => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return panel?.dataset.proposalId === proposalId && panel.dataset.proposalStatus === 'ready';
    }, { proposalId: response.proposalId }, deadline);
    const dom = await cda.inspect(() => ({
      status: document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus,
      proposalId: document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId,
      headers: [...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')]
        .map(cell => {
          const label = cell.querySelector(':scope > span');
          return (label?.textContent ?? cell.textContent ?? '').trim().replace(/\s+/g, ' ');
        }),
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
    }));
    assert.equal(dom.status, 'ready');
    assert.equal(dom.proposalId, response.proposalId);
    if (verifyResponse) {
      protocolGroupRows(response);
      const groupedDocument = { construction: response.candidateConstruction };
      assertVisibleGroupRows(dom, response, groupedDocument);
    }
    verifyRequest?.(request, response);
    checkpoint(name, startedAt, { proposalId: response.proposalId, protocolRows: response.preview?.rows?.length, renderedRows: dom.rows });
    return { entry, request, response, dom };
  };
  const openGroupEditor = async () => {
    await act('Open Observation row settings', page.locator('[data-testid="construction-rows-settings-trigger"]'));
    const openAt = Date.now();
    const deadline = deadlineFrom(openAt);
    await wait(() => document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled === false, {}, deadline);
    const fromIndex = capture.requests.length;
    await act('Open native Group editor', page.locator('[data-testid="construction-action-group-rows"]'),
      target => target.click({ timeout: remaining(deadline) }));
    await page.getByRole('region', { name: 'Coded value choices by direct source field', exact: true })
      .waitFor({ state: 'visible', timeout: remaining(deadline) });
    const elapsed = Date.now() - openAt;
    assert(elapsed <= 5_000, `Opening the native Group editor took ${elapsed}ms.`);
    return { startedAt: openAt, deadline, fromIndex };
  };
  const discoverDirectCodedCategory = async ({ editorWindow, name }) => {
    const { startedAt, deadline, fromIndex } = editorWindow;
    const region = page.getByRole('region', { name: 'Coded value choices by direct source field', exact: true });
    assert.equal(await region.count(), 1, `${name} must expose the current direct-source coded choice region exactly once.`);
    await region.waitFor({ state: 'visible', timeout: remaining(deadline) });
    assert.equal(await region.locator('input[aria-label="Search coded values"]').count(), 0,
      `${name} must use automatically rendered coded choices without a search field.`);
    assert.equal(await region.getByRole('button', { name: 'More coded values', exact: true }).count(), 0,
      `${name} must use the current complete legal inventory without a More coded values control.`);

    const frameSourceRequest = await capture.waitForResponse(entry => entry.path.endsWith('/frame-source-options')
      && entry.method === 'POST' && entry.status === 200 && entry.completedAt
      && capture.requestBody(entry)?.snapshotToken === snapshotToken
      && capture.requestBody(entry)?.outputId === outputId
      && capture.requestBody(entry)?.resourceType === 'Observation'
      && (capture.responseBody(entry)?.sources ?? []).some(source => source.sourcePath === 'code'
        && source.resourceType === 'Observation' && source.valuePath === 'valueCodeableConcept.text'
        && source.route?.length === 0),
    { fromIndex, timeoutMs: remaining(deadline) });
    const frameRequestBody = capture.requestBody(frameSourceRequest);
    const frameSourceResponse = capture.responseBody(frameSourceRequest);
    assert.equal(frameRequestBody.snapshotToken, snapshotToken);
    assert.equal(frameRequestBody.outputId, outputId);
    assert.equal(frameRequestBody.resourceType, 'Observation');
    assert.equal(frameSourceResponse.snapshotToken, snapshotToken);
    assert.equal(frameSourceResponse.outputId, outputId);
    const frameSources = frameSourceResponse.sources;
    const matchingFrameSources = frameSources.filter(source => source.sourcePath === 'code'
      && source.resourceType === 'Observation' && source.valuePath === 'valueCodeableConcept.text'
      && source.route?.length === 0);
    assert.equal(matchingFrameSources.length, 1,
      `${name} must expose exactly one direct Observation.code/valueCodeableConcept display frame.`);
    const frameOption = matchingFrameSources[0];
    validateCdaCompoundCodedGroupSourceOption(frameOption, { outputId, snapshotToken });

    const isScopedInventory = entry => {
      if (!entry.path.endsWith('/semantic-inventory') || entry.method !== 'POST' || entry.status !== 200 || !entry.completedAt) return false;
      const body = capture.requestBody(entry);
      return body?.snapshotToken === snapshotToken && body?.outputId === outputId && body?.rowRoot === 'Observation'
        && body?.sourceChoiceId === frameOption.choiceId && !Object.hasOwn(body, 'query');
    };
    const isExactCategory = item => item?.system === CDA_COMPOUND_CODED_GROUP_CATEGORY.system
      && item?.code === CDA_COMPOUND_CODED_GROUP_CATEGORY.code && item?.bindingId === frameOption.bindingId
      && item?.resourceType === 'Observation' && item?.sourcePath === 'code'
      && item?.valueSelector === 'valueCodeableConcept.text';
    const targetRequest = await capture.waitForResponse(entry => isScopedInventory(entry)
      && (capture.responseBody(entry)?.entries ?? []).some(isExactCategory),
    { fromIndex, timeoutMs: remaining(deadline) });
    const scopedInventoryRequests = capture.requests.slice(fromIndex).filter(isScopedInventory);
    assert(scopedInventoryRequests.length > 0, `${name} must capture scoped no-query semantic inventory for the current source choice.`);
    const exactItems = scopedInventoryRequests.flatMap(entry => capture.responseBody(entry)?.entries ?? []).filter(isExactCategory);
    assert(exactItems.length >= 1, `${name} must find the exact SNOMED category in the returned legal inventory.`);
    const validatedCategoryChoiceIDs = exactItems.map(item => validateCdaCompoundCodedGroupInventoryItem(item, frameOption));
    const categoryChoiceIDs = [...new Set(validatedCategoryChoiceIDs)];
    assert.equal(categoryChoiceIDs.length, 1, `${name} must resolve one stable server-issued category choice across inventory pages.`);
    const item = exactItems[0];
    const categoryChoiceId = validateCdaCompoundCodedGroupInventoryItem(item, frameOption);
    assert.equal(categoryChoiceId, categoryChoiceIDs[0]);
    const semanticResponse = capture.responseBody(targetRequest);
    assert.equal(semanticResponse.state, 'complete', `${name} requires a complete semantic inventory build.`);
    assert(semanticResponse.contextToken && semanticResponse.buildId,
      `${name} must retain the current semantic context and build identities.`);
    const label = String(item.display ?? '').trim() || item.code;
    const checkboxName = /^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$/.test(label)
      ? label.replaceAll('_', ' ').replace(/^./, first => first.toUpperCase())
      : label;
    const accessibleName = `Group by coded value: ${checkboxName}`;
    const categoryControl = region.getByRole('checkbox', { name: accessibleName, exact: true });
    assert.equal(await categoryControl.count(), 1, `${name} must render the exact legal category checkbox once.`);
    await categoryControl.waitFor({ state: 'visible', timeout: remaining(deadline) });
    assert(await categoryControl.isEnabled(), `${name} must render the exact category checkbox enabled.`);
    checkpoint(name, startedAt, { sourceChoiceId: frameOption.choiceId, categoryChoiceId,
      scopedInventoryRequests: scopedInventoryRequests.length, targetRequestId: targetRequest.browserRequestId });
    return { frameOption, item, categoryChoiceId, checkboxName, categoryControl, targetRequest,
      scopedInventoryRequests, semanticEvidence: { contextToken: semanticResponse.contextToken,
        buildId: semanticResponse.buildId, state: semanticResponse.state, sourceAvailability: semanticResponse.sourceAvailability },
      startedAt, deadline };
  };
  const applyProposal = async ({ name, proposal, expectedRows, verifySaved }) => {
    const startedAt = Date.now();
    const deadline = deadlineFrom(startedAt);
    const previewFromIndex = capture.requests.length;
    const applyProposalId = proposal?.response?.proposalId;
    assert(applyProposalId, `${name} must apply the exact native proposal receipt.`);
    await act(name, page.locator('[data-testid="construction-apply-proposal"]'),
      target => target.click({ timeout: remaining(deadline) }));
    const commandEntry = await findRequest(previewFromIndex, entry => entry.path === `${explorerScope.authoringBase}/commands`
      && entry.method === 'POST' && entry.status === 200 && entry.completedAt, deadline);
    const commandRequest = capture.requestBody(commandEntry);
    assert.deepEqual(commandRequest?.commands, [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: applyProposalId }],
      'Apply must submit only the exact construction proposal command.');
    const expectedStepCount = proposal.response.candidateConstruction?.steps
      ?.filter(step => !step.ownerStepId).length ?? 0;
    await wait(({ count }) => !document.querySelector('[data-testid="construction-proposal-panel"]')
      && document.querySelectorAll('[data-testid^="construction-history-step-"]').length === count, { count: expectedStepCount }, deadline);
    const builder = await builderRead(deadline);
    const doc = documentFor(builder);
    const rows = await visibleRows(deadline, expectedRows.length);
    const previewEntry = await findRequest(previewFromIndex, entry => entry.path === `${explorerScope.authoringBase}/preview`
      && entry.method === 'POST' && entry.status === 200 && entry.completedAt
      && capture.requestBody(entry)?.outputId === outputId, deadline);
    const saved = capture.responseBody(previewEntry);
    const request = capture.requestBody(previewEntry);
    assert(request?.receiptId, 'The native saved Preview request must carry its exact receipt identity.');
    const verified = savedPreviewRows(saved, doc);
    if (verified.kind === 'group') assertVisibleGroupRows(rows, saved, doc);
    else rowsBySourceID(rows, doc, oracle.ids);
    const panel = await cda.inspect(() => {
      const current = document.querySelector('[data-testid="construction-preview"]');
      return { status: current?.dataset.previewStatus, receiptId: current?.dataset.previewReceiptId,
        outputId: current?.dataset.previewOutputId, draftVersion: Number(current?.dataset.currentDraftVersion),
        draftDigest: current?.dataset.currentDraftDigest };
    });
    assert.equal(panel.status, 'ready');
    assert.equal(panel.receiptId, request.receiptId);
    assert.equal(panel.outputId, outputId);
    assert.equal(panel.draftVersion, builder.draftVersion);
    assert.equal(panel.draftDigest, builder.draftDigest);
    verifySaved?.(builder, doc, rows);
    checkpoint(name, startedAt, { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
      previewRequestId: previewEntry.browserRequestId, receiptId: request.receiptId, verifiedRows: verified.rows,
      visibleRows: rows.rows });
    return { builder, doc, rows, saved, previewEntry, proposal };
  };
  const reloadAndRead = async ({ name, expectedGrouped = false, expectedIDs = [] }) => {
    const startedAt = Date.now();
    const deadline = deadlineFrom(startedAt);
    const previewFromIndex = capture.requests.length;
    await page.reload({ waitUntil: 'domcontentloaded', timeout: remaining(deadline) });
    if (expectedGrouped) {
      await wait(() => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1
        && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '3', {}, deadline);
    } else {
      await wait(({ count }) => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0
        && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(count + 1),
      { count: expectedIDs.length }, deadline);
    }
    const builder = await builderRead(deadline);
    const doc = documentFor(builder);
    const rows = await visibleRows(deadline, expectedGrouped ? 2 : expectedIDs.length);
    const previewEntry = await findRequest(previewFromIndex, entry => entry.path === `${explorerScope.authoringBase}/preview`
      && entry.method === 'POST' && entry.status === 200 && entry.completedAt
      && capture.requestBody(entry)?.outputId === outputId, deadline);
    const protocol = capture.responseBody(previewEntry);
    const previewRequest = capture.requestBody(previewEntry);
    assert(previewRequest?.receiptId);
    const verified = savedPreviewRows(protocol, doc);
    if (verified.kind === 'group') assertVisibleGroupRows(rows, protocol, doc);
    const panel = await cda.inspect(() => {
      const current = document.querySelector('[data-testid="construction-preview"]');
      return { status: current?.dataset.previewStatus, receiptId: current?.dataset.previewReceiptId,
        outputId: current?.dataset.previewOutputId, draftVersion: Number(current?.dataset.currentDraftVersion),
        draftDigest: current?.dataset.currentDraftDigest };
    });
    assert.equal(panel.status, 'ready');
    assert.equal(panel.receiptId, previewRequest.receiptId);
    assert.equal(panel.outputId, outputId);
    assert.equal(panel.draftVersion, builder.draftVersion);
    assert.equal(panel.draftDigest, builder.draftDigest);
    if (expectedGrouped) {
      const step = doc.construction.steps.find(candidate => candidate.operation.kind === 'GROUP');
      assert(step);
      const id = step.id;
      const panel = page.locator(`[data-testid="construction-history-step-${id}"]`);
      assert.equal(await panel.count(), 1);
      const coded = doc.construction.steps.find(candidate => candidate.operation.kind === 'CODED_PIVOT');
      assert(coded && step);
      return { builder, doc, rows, protocol, previewEntry,
        duration: checkpoint(name, startedAt, { stepIDs: doc.construction.steps.map(step => step.id), rows: verified.rows,
          previewRequestId: previewEntry.browserRequestId, receiptId: previewRequest.receiptId }) };
    }
    rowsBySourceID(rows, doc, expectedIDs);
    assert.deepEqual([...sourceIDRowsFromResponse(protocol, doc, expectedIDs)].sort(), [...expectedIDs].sort());
    return { builder, doc, rows, protocol, previewEntry,
      duration: checkpoint(name, startedAt, { ids: expectedIDs, rows: rows.rows,
        previewRequestId: previewEntry.browserRequestId, receiptId: previewRequest.receiptId }) };
  };
  const cancelProposal = async ({ name, beforeBuilder, expectedRows, expectedCount, expectedPreview }) => {
    const startedAt = Date.now();
    const deadline = deadlineFrom(startedAt);
    const cancelFromIndex = capture.requests.length;
    await act(name, page.locator('[data-testid="construction-cancel-proposal"]'),
      target => target.click({ timeout: remaining(deadline) }));
    await wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), {}, deadline);
    assert(!capture.requests.slice(cancelFromIndex).some(entry => entry.path === `${explorerScope.authoringBase}/commands`),
      'Cancel must not issue a draft mutation command.');
    const after = await builderRead(deadline);
    assert.equal(after.draftDigest, beforeBuilder.draftDigest);
    assert.deepEqual(after.workspace, beforeBuilder.workspace);
    const rows = await visibleRows(deadline, expectedCount);
    if (expectedRows === 'source') rowsBySourceID(rows, documentFor(after), oracle.ids);
    else {
      assertCdaCompoundCodedGroupRows(expectedRows);
      assertVisibleGroupRows(rows, expectedPreview, documentFor(after));
    }
    checkpoint(name, startedAt, { draftDigest: after.draftDigest, rowCount: rows.rows.length });
    return after;
  };

  try {
    const query = buildCdaCompoundCodedGroupExactReadQuery({ project, generation });
    const sourceRows = readExactArangoRows(arangoContainer, query);
    oracle = prepareCdaCompoundCodedGroupOracle(sourceRows, { project, generation });
    report.oracle = { query, scope: { project, generation }, exactIDs: oracle.ids, category: oracle.category,
      source: oracle.source, valuesByID: oracle.valuesByID, expectedRows: oracle.expectedRows,
      reread: sourceRows.map(source => ({ id: source.id, project: source.project, generation: source.generation,
        resourceType: source.payload.resourceType, code: source.payload.code, valueCodeableConcept: source.payload.valueCodeableConcept ?? null })) };
    check('correctness', 'raw exact CDA source oracle proves the three Observation IDs and coded value-or-null pairing', true, report.oracle);

    const requestedName = `cda-coded-group-${randomUUID().slice(0, 8)}`;
    const created = await api(rootPath(project), { name: requestedName, title: 'Real CDA compound coded GROUP verification' });
    explorerScope = createdExplorerScope(project, created);
    const base = explorerScope.authoringBase;
    report.explorer = explorerScope.explorerId;
    report.target.explorer = explorerScope.explorerId;
    capture = cda.captureRequests(explorerScope.explorerRoot, { browserRequestOrigin: uiOrigin,
      responsePaths: /frame-source-options|semantic-inventory|construction-proposals|commands|builder|preview|selections/ });

    const initialReadStarted = Date.now();
    const initialReadDeadline = deadlineFrom(initialReadStarted);
    const initial = await builderRead(initialReadDeadline);
    assert.equal(initial.catalog.generation, generation);
    snapshotToken = initial.catalog.snapshotToken;
    checkpoint('read exact Explorer catalog and snapshot', initialReadStarted,
      { snapshotToken, generation: initial.catalog.generation });
    const selection = await api(`${explorerScope.explorerRoot}/selections`, {
      snapshotToken: initial.catalog.snapshotToken,
      idempotencyKey: `cda-coded-group-${randomUUID()}`,
      source: { kind: 'resources', resources: { refs: oracle.ids.map(id => ({ project, generation, resourceType: 'Observation', id })) } },
    });
    assert.equal(selection.project, project);
    assert.equal(selection.generation, generation);
    assert.equal(selection.resourceType, 'Observation');
    assert.equal(selection.memberCount, 3);
    const url = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorerScope.explorerId)}&mode=builder&selection=${encode(selection.id)}`;
    await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 5_000 });
    await wait(() => Boolean(document.querySelector('button[aria-label="Choose Observation rows"]:not(:disabled)')),
      {}, deadlineFrom(Date.now()));
    await act('Choose exact Observation rows', page.getByRole('button', { name: 'Choose Observation rows', exact: true }));
    const sourceSetupStarted = Date.now();
    const sourceSetupDeadline = deadlineFrom(sourceSetupStarted);
    await wait(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false, {}, sourceSetupDeadline);
    await act('Open starting collection settings', page.locator('[data-testid="construction-rows-settings-trigger"]'));
    await wait(() => [...document.querySelectorAll('[aria-label="Starting collection"] button')]
      .some(button => button.innerText.trim() === 'Use selected resources' && !button.disabled), {}, sourceSetupDeadline);
    await act('Attach the exact selected Observations', page.getByRole('button', { name: 'Use selected resources', exact: true }));
    await wait(() => document.querySelector('[aria-label="Starting collection settings"]')?.innerText.includes('3 Observation resources attached'), {}, sourceSetupDeadline);
    await act('Return to the source table', page.getByRole('button', { name: 'Back to table', exact: true }));
    const sourceRenderStarted = Date.now();
    const sourceRenderDeadline = deadlineFrom(sourceRenderStarted);
    const sourceBuilder = await builderRead(sourceRenderDeadline);
    sourceDocument = documentFor(sourceBuilder);
    outputId = sourceDocument.output.id;
    const initialRows = await visibleRows(sourceRenderDeadline, 3);
    rowsBySourceID(initialRows, sourceDocument, oracle.ids);
    assert.deepEqual(sourceDocument.construction?.steps ?? [], []);
    checkpoint('initial exact source rows render within five seconds', sourceRenderStarted, { selectionId: selection.id, outputId, rows: initialRows.rows });

    let editorWindow = await openGroupEditor();
    let directCategory = await discoverDirectCodedCategory({ editorWindow, name: 'discover exact SNOMED category for Observation.code' });
    selectedFrameOption = directCategory.frameOption;
    selectedInventoryItem = directCategory.item;
    categoryChoiceId = directCategory.categoryChoiceId;
    checkboxName = directCategory.checkboxName;
    categoryControl = directCategory.categoryControl;
    report.nativeBinding = { outputId, snapshotToken: initial.catalog.snapshotToken,
      sourceChoiceId: selectedFrameOption.choiceId, sourceOption: selectedFrameOption,
      categoryChoiceId, semanticItem: selectedInventoryItem,
      semanticEvidence: directCategory.semanticEvidence,
      initialInventoryRequests: directCategory.scopedInventoryRequests.map(entry => ({ requestId: entry.browserRequestId,
        body: capture.requestBody(entry) })) };
    const proposalStarted = Date.now();
    const proposalDeadline = deadlineFrom(proposalStarted);
    const proposalStart = capture.requests.length;
    await act('Select exact Observation coded category', categoryControl,
      target => target.click({ timeout: remaining(proposalDeadline) }));
    initialProposal = await proposalFor({ name: 'initial coded GROUP Preview returns exact Stage I/null counts',
      startedAt: proposalStarted, fromIndex: proposalStart,
      verifyRequest: request => {
        const coded = request.candidateConstruction.steps.find(step => step.operation?.kind === 'CODED_PIVOT');
        assert(coded);
        assert.equal(coded.operation.codedPivot.sourceChoiceId, selectedFrameOption.choiceId);
        assert.deepEqual(coded.operation.codedPivot.categories.map(category => ({ choiceId: category.choiceId })), [{ choiceId: categoryChoiceId }]);
      },
    });
    const candidateBinding = validateCdaCompoundCodedGroupCandidate(initialProposal.response.candidateConstruction,
      selectedFrameOption, selectedInventoryItem);
    assert.deepEqual(initialProposal.response.preview.rows.length, 2);
    assertCdaCompoundCodedGroupRows(protocolGroupRows(initialProposal.response).rows);
    const normalizedCoded = initialProposal.response.candidateConstruction.steps.find(step => step.operation.kind === 'CODED_PIVOT');
    assert.equal(normalizedCoded.operation.codedPivot.categories[0].choiceId, undefined);
    assert.deepEqual(normalizedCoded.operation.codedPivot.categories.map(({ system, code }) => ({ system, code })), [CDA_COMPOUND_CODED_GROUP_CATEGORY]);
    report.initialProposal = { request: initialProposal.request, response: initialProposal.response,
      candidateBinding, actualRows: protocolGroupRows(initialProposal.response).rows };
    check('correctness', 'native coded GROUP proposal preserves exact category identities and returns Stage I/null counts', true, report.initialProposal);

    const beforeCancelStarted = Date.now();
    const beforeCancelDeadline = deadlineFrom(beforeCancelStarted);
    const beforeCancel = await builderRead(beforeCancelDeadline);
    assert.equal(beforeCancel.draftDigest, sourceBuilder.draftDigest,
      'Opening the coded GROUP proposal must not change the saved source draft before Cancel.');
    assert.deepEqual(beforeCancel.workspace, sourceBuilder.workspace,
      'The saved source workspace must remain unchanged while the proposal is open.');
    checkpoint('read exact source state before Cancel', beforeCancelStarted, { draftDigest: beforeCancel.draftDigest });
    const afterCancel = await cancelProposal({ name: 'Cancel initial coded GROUP Preview preserves exact source draft',
      beforeBuilder: beforeCancel, expectedRows: 'source', expectedCount: 3 });
    assert.deepEqual(documentFor(afterCancel).construction?.steps ?? [], []);
    check('persistence', 'Cancel preserves the exact three-row source draft and empty construction', true,
      { draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest, selectionId: selection.id, rows: oracle.ids });

    editorWindow = await openGroupEditor();
    directCategory = await discoverDirectCodedCategory({ editorWindow,
      name: 'repeat exact semantic category discovery after Cancel' });
    assert.equal(directCategory.frameOption.choiceId, selectedFrameOption.choiceId,
      'Cancel retry must preserve the authorized direct source choice for this output and snapshot.');
    assert.equal(directCategory.categoryChoiceId, categoryChoiceId,
      'Cancel retry must reauthorize the same category identity for the current snapshot.');
    assert.deepEqual({ system: directCategory.item.system, code: directCategory.item.code,
      bindingId: directCategory.item.bindingId, sourcePath: directCategory.item.sourcePath,
      valueSelector: directCategory.item.valueSelector },
    { system: selectedInventoryItem.system, code: selectedInventoryItem.code,
      bindingId: selectedInventoryItem.bindingId, sourcePath: selectedInventoryItem.sourcePath,
      valueSelector: selectedInventoryItem.valueSelector });
    categoryControl = directCategory.categoryControl;
    report.nativeBinding.afterCancel = { sourceChoiceId: directCategory.frameOption.choiceId,
      categoryChoiceId: directCategory.categoryChoiceId,
      semanticEvidence: directCategory.semanticEvidence,
      inventoryRequests: directCategory.scopedInventoryRequests.map(entry => ({ requestId: entry.browserRequestId,
        body: capture.requestBody(entry) })) };
    const confirmedStarted = Date.now();
    const confirmedDeadline = deadlineFrom(confirmedStarted);
    const confirmedStart = capture.requests.length;
    await act('Select coded category for Apply', categoryControl,
      target => target.click({ timeout: remaining(confirmedDeadline) }));
    const confirmedProposal = await proposalFor({ name: 'confirmed coded GROUP Preview preserves Stage I/null counts',
      startedAt: confirmedStarted, fromIndex: confirmedStart,
      verifyRequest: request => {
        const coded = request.candidateConstruction.steps.find(step => step.operation?.kind === 'CODED_PIVOT');
        assert(coded);
        assert.equal(coded.operation.codedPivot.sourceChoiceId, selectedFrameOption.choiceId);
        assert.equal(coded.operation.codedPivot.categories[0].choiceId, categoryChoiceId);
      },
    });
    validateCdaCompoundCodedGroupCandidate(confirmedProposal.response.candidateConstruction, selectedFrameOption, selectedInventoryItem);
    const applied = await applyProposal({ name: 'Apply owned CODED_PIVOT and GROUP', proposal: confirmedProposal,
      expectedRows: oracle.expectedRows,
      verifySaved: (builder, doc) => {
        const coded = doc.construction.steps.find(step => step.operation.kind === 'CODED_PIVOT');
        const group = doc.construction.steps.find(step => step.operation.kind === 'GROUP');
        assert(coded && group);
        assert.equal(coded.ownerStepId, group.id);
        assert.equal(coded.operation.codedPivot.missingCellPolicy, 'NULL');
        assert.equal(group.operation.group.missingKeyPolicy, 'GROUP');
        assert.deepEqual(coded.operation.codedPivot.categories.map(({ system, code }) => ({ system, code })), [CDA_COMPOUND_CODED_GROUP_CATEGORY]);
        validateCdaCompoundCodedGroupCandidate(doc.construction, selectedFrameOption, selectedInventoryItem);
      },
    });
    appliedCandidate = applied.doc.construction;
    check('persistence', 'Apply persists owned CODED_PIVOT and GROUP with NULL/GROUP policies', true,
      { construction: appliedCandidate, sourceOption: selectedFrameOption, category: selectedInventoryItem, outputId,
        draftVersion: applied.builder.draftVersion, draftDigest: applied.builder.draftDigest });

    const appliedReload = await reloadAndRead({ name: 'actual page reload preserves exact coded GROUP output', expectedGrouped: true });
    assert.deepEqual(appliedReload.doc.construction, appliedCandidate);
    check('persistence', 'applied GROUP and exact rows survive actual page reload', true,
      { construction: appliedReload.doc.construction, rows: appliedReload.protocol.rows, draftDigest: appliedReload.builder.draftDigest });

    const groupStep = appliedReload.doc.construction.steps.find(step => step.operation.kind === 'GROUP');
    const editStarted = Date.now();
    const editDeadline = deadlineFrom(editStarted);
    await act('Select coded GROUP history', page.locator(`[data-testid="construction-history-step-${groupStep.id}"]`),
      target => target.click({ timeout: remaining(editDeadline) }));
    await wait(({ stepId }) => Boolean(document.querySelector(`[data-testid="construction-edit-step-${stepId}"]:not(:disabled)`)),
      { stepId: groupStep.id }, editDeadline);
    await act('Edit saved coded GROUP', page.locator(`[data-testid="construction-edit-step-${groupStep.id}"]`),
      target => target.click({ timeout: remaining(editDeadline) }));
    await wait(({ ariaLabel }) => Boolean(document.querySelector(`input[type="checkbox"][aria-label=${JSON.stringify(ariaLabel)}]:checked`)),
      { ariaLabel: `Group by coded value: ${checkboxName}` }, editDeadline);
    const groupAdvancedOptions = page.locator('[data-testid="construction-reshape-group-advanced"]');
    assert.equal(await groupAdvancedOptions.count(), 1, 'Editing the saved GROUP must expose one active advanced-options section.');
    if (!await groupAdvancedOptions.evaluate(details => details.open)) {
      await act('Open GROUP summary output settings', groupAdvancedOptions.locator(':scope > summary'),
        target => target.click({ timeout: remaining(editDeadline) }));
    }
    await wait(() => Boolean(document.querySelector(
      '[data-testid="construction-reshape-group-advanced"][open] input[aria-label="Summary output label 1"]')),
    {}, editDeadline);
    const editWindow = { startedAt: editStarted, deadline: editDeadline, fromIndex: capture.requests.length };
    const labelInput = groupAdvancedOptions.getByRole('textbox', { name: 'Summary output label 1', exact: true });
    await act('Edit COUNT_ROWS label', labelInput,
      target => target.fill('Observation count', { timeout: remaining(editWindow.deadline) }));
    editedCandidate = await proposalFor({ name: 'edit saved coded GROUP preserves exact paired rows',
      startedAt: editWindow.startedAt, fromIndex: editWindow.fromIndex,
      verifyRequest: request => {
        const coded = request.candidateConstruction.steps.find(step => step.operation?.kind === 'CODED_PIVOT');
        const group = request.candidateConstruction.steps.find(step => step.operation?.kind === 'GROUP');
        assert(coded && group);
        assert.equal(coded.operation.codedPivot.sourceChoiceId, selectedFrameOption.choiceId);
        assert.equal(coded.operation.codedPivot.categories[0].choiceId, categoryChoiceId);
        const count = group.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
        assert.equal(group.outputs.find(output => output.id === count.outputColumnId).label, 'Observation count');
      },
    });
    const editReadStarted = Date.now();
    const editBuilderBeforeApply = await builderRead(deadlineFrom(editReadStarted));
    assert.deepEqual(documentFor(editBuilderBeforeApply).construction, appliedCandidate,
      'Editing the Group must remain a proposal until Apply.');
    checkpoint('read persisted construction before edited Apply', editReadStarted,
      { draftVersion: editBuilderBeforeApply.draftVersion, draftDigest: editBuilderBeforeApply.draftDigest });
    const editedApply = await applyProposal({ name: 'Apply edited coded GROUP label', proposal: editedCandidate,
      expectedRows: oracle.expectedRows,
      verifySaved: (_builder, doc) => {
        const coded = doc.construction.steps.find(step => step.operation.kind === 'CODED_PIVOT');
        const group = doc.construction.steps.find(step => step.operation.kind === 'GROUP');
        assert(coded && group);
        assert.equal(coded.ownerStepId, group.id);
        assert.equal(group.outputs.find(output => output.id === group.operation.group.aggregates[0].outputColumnId).label, 'Observation count');
        validateCdaCompoundCodedGroupCandidate(doc.construction, selectedFrameOption, selectedInventoryItem);
      },
    });
    editedWorkspace = editedApply.builder.workspace;
    const editedReload = await reloadAndRead({ name: 'edited coded GROUP reload retains exact Stage I/null rows', expectedGrouped: true });
    assert.deepEqual(editedReload.builder.workspace, editedWorkspace);
    editedCandidate = editedReload.doc.construction;
    check('persistence', 'editing GROUP preserves exact source/category bindings and output rows', true,
      { construction: editedCandidate, rows: editedReload.protocol.rows, draftDigest: editedReload.builder.draftDigest });

    const editedGroup = editedReload.doc.construction.steps.find(step => step.operation.kind === 'GROUP');
    await act('Select edited coded GROUP history', page.locator(`[data-testid="construction-history-step-${editedGroup.id}"]`));
    const removalStarted = Date.now();
    const removalDeadline = deadlineFrom(removalStarted);
    const removalStart = capture.requests.length;
    await act('Propose coded GROUP removal', page.locator(`[data-testid="construction-remove-step-${editedGroup.id}"]`),
      target => target.click({ timeout: remaining(removalDeadline) }));
    const removalEntry = await findRequest(removalStart, entry => entry.path.endsWith('/construction-proposals') && entry.method === 'POST'
      && entry.status === 200 && entry.completedAt, removalDeadline);
    const removalResponse = capture.responseBody(removalEntry);
    const removalRequest = capture.requestBody(removalEntry);
    assert(removalResponse);
    assert.equal(removalRequest.outputId, outputId);
    assert.equal(removalRequest.snapshotToken, snapshotToken);
    assert.equal(removalResponse.previewStatus, 'READY');
    assert.deepEqual(removalResponse.candidateConstruction.steps, [], 'Removing the Group must restore the empty construction candidate.');
    sourceIDRowsFromResponse(removalResponse, sourceDocument, oracle.ids);
    await wait(({ proposalId }) => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === proposalId
      && document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready',
    { proposalId: removalResponse.proposalId }, removalDeadline);
    const removalDom = await cda.inspect(() => ({
      headers: [...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')]
        .map(cell => {
          const label = cell.querySelector(':scope > span');
          return (label?.textContent ?? cell.textContent ?? '').trim().replace(/\s+/g, ' ');
        }),
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
    }));
    assertVisibleSourceRows(removalDom, removalResponse, sourceDocument, oracle.ids);
    checkpoint('propose Group removal renders exact original Observation IDs', removalStarted,
      { proposalId: removalResponse.proposalId, rows: removalDom.rows });
    const removalReadStarted = Date.now();
    const removalBuilderBeforeCancel = await builderRead(deadlineFrom(removalReadStarted));
    checkpoint('read persisted construction before removal Cancel', removalReadStarted,
      { draftVersion: removalBuilderBeforeCancel.draftVersion, draftDigest: removalBuilderBeforeCancel.draftDigest });
    const afterRemovalCancel = await cancelProposal({ name: 'Cancel coded GROUP removal preserves edited construction',
      beforeBuilder: removalBuilderBeforeCancel, expectedRows: oracle.expectedRows, expectedCount: 2,
      expectedPreview: editedReload.protocol });
    assert.deepEqual(documentFor(afterRemovalCancel).construction, editedCandidate);
    check('persistence', 'Cancel removal preserves the edited grouped draft', true,
      { draftDigest: afterRemovalCancel.draftDigest, construction: documentFor(afterRemovalCancel).construction });

    const finalRemovalStarted = Date.now();
    const finalRemovalDeadline = deadlineFrom(finalRemovalStarted);
    const finalRemovalStart = capture.requests.length;
    await act('Select edited GROUP before applying removal', page.locator(`[data-testid="construction-history-step-${editedGroup.id}"]`),
      target => target.click({ timeout: remaining(finalRemovalDeadline) }));
    await act('Reopen coded GROUP removal proposal', page.locator(`[data-testid="construction-remove-step-${editedGroup.id}"]`),
      target => target.click({ timeout: remaining(finalRemovalDeadline) }));
    const finalRemovalEntry = await findRequest(finalRemovalStart, entry => entry.path.endsWith('/construction-proposals') && entry.method === 'POST'
      && entry.status === 200 && entry.completedAt, finalRemovalDeadline);
    const finalRemovalResponse = capture.responseBody(finalRemovalEntry);
    const finalRemovalRequest = capture.requestBody(finalRemovalEntry);
    assert.equal(finalRemovalRequest.outputId, outputId);
    assert.equal(finalRemovalRequest.snapshotToken, snapshotToken);
    assert.deepEqual(finalRemovalResponse.candidateConstruction.steps, []);
    sourceIDRowsFromResponse(finalRemovalResponse, sourceDocument, oracle.ids);
    await wait(({ proposalId }) => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId === proposalId
      && document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready',
    { proposalId: finalRemovalResponse.proposalId }, finalRemovalDeadline);
    const finalRemovalDom = await cda.inspect(() => ({
      headers: [...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')]
        .map(cell => {
          const label = cell.querySelector(':scope > span');
          return (label?.textContent ?? cell.textContent ?? '').trim().replace(/\s+/g, ' ');
        }),
      rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
    }));
    assertVisibleSourceRows(finalRemovalDom, finalRemovalResponse, sourceDocument, oracle.ids);
    checkpoint('reopened removal Preview still restores exact original Observation IDs', finalRemovalStarted,
      { proposalId: finalRemovalResponse.proposalId, rows: finalRemovalDom.rows });
    const restored = await applyProposal({ name: 'Apply GROUP removal restores exact source rows',
      proposal: { response: finalRemovalResponse, request: capture.requestBody(finalRemovalEntry) },
      expectedRows: oracle.ids.map(id => [id]),
      verifySaved: (builder, doc) => {
        assert.deepEqual(doc.construction?.steps ?? [], []);
        const columns = columnsWithoutGeneratedIdentity(doc.columns);
        assert.deepEqual(columns, columnsWithoutGeneratedIdentity(sourceDocument.columns));
      },
    });
    const restoredReload = await reloadAndRead({ name: 'actual restoration reload returns all original Observation IDs', expectedIDs: oracle.ids });
    assert.deepEqual(restoredReload.doc.construction?.steps ?? [], []);
    assert.deepEqual(columnsWithoutGeneratedIdentity(restoredReload.doc.columns), columnsWithoutGeneratedIdentity(sourceDocument.columns));
    check('persistence', 'applying removal restores all original Observation rows and schema across reload', true,
      { rows: restoredReload.rows.rows, ids: oracle.ids, columns: restoredReload.doc.columns,
        draftDigest: restoredReload.builder.draftDigest, removedProposalId: finalRemovalResponse.proposalId });

    const maxAction = Math.max(0, ...report.actionToRender.map(entry => entry.durationMs));
    check('performance', 'all native action-to-render lifecycle checkpoints complete within five seconds',
      report.actionToRender.every(entry => Number.isFinite(entry.durationMs) && entry.durationMs <= 5_000),
      { checkpoints: report.actionToRender, maxDurationMs: maxAction, budgetMs: 5_000 });
    const unexpectedErrors = (report.errors ?? []).filter(error => error.expected !== true && error.kind !== 'asset');
    assert.deepEqual(unexpectedErrors, [], 'The native lifecycle must not hide browser, network, or HTTP errors.');
    check('correctness', 'No unexpected native requests or browser errors occurred', true,
      { nativeRequestCount: report.nativeRequests.length, apiRequestCount: report.apiRequests?.length ?? 0, unexpectedErrors });
  } catch (error) {
    report.failure = { error: String(error.stack ?? error), activeAction, explorer: explorerScope?.explorerId,
      outputId, sourceOption: selectedFrameOption, semanticItem: selectedInventoryItem,
      proposalId: initialProposal?.response?.proposalId };
    if (error.invalidatesRun) report.status = 'invalidated';
    throw error;
  } finally {
    await cda.attachReport(`cda-compound-coded-group-${explorerScope?.explorerId ?? 'setup'}`, report);
  }
}

function columnsWithoutGeneratedIdentity(columns) {
  return columns.map(({ columnId: _columnId, ...column }) => column);
}
