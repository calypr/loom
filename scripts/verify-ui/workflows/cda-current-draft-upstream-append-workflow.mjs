import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import {
  CDA_UPSTREAM_APPEND_RESOURCES,
  CDA_UPSTREAM_APPEND_SOURCES,
  CdaUpstreamAppendWitnessUnavailable,
  assertCdaUpstreamAppendReread,
  cdaUpstreamAppendRereadQuery,
  cdaUpstreamAppendScanQuery,
  prepareCdaUpstreamAppendOracle,
  proveObservedSupersededEmptyGroupProposals,
} from '../helpers/cda-current-draft-upstream-append-oracle.mjs';
import {
  builderDraftStateEvidence,
  currentDraftSourceEvidence,
  workspaceOutputOption,
} from '../helpers/builder-combine-draft-helpers.mjs';
import { nativeCombineTargetBindingEvidence } from '../helpers/builder-combine-helpers.mjs';
import { proposalPreviewReadinessExpression, proposalPreviewStateInPage } from '../helpers/proposal-preview-readiness.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';

const MAX_ACTION_MS = 5_000;
const ACTION_CHECK = 'all native CDA Group→DERIVE→APPEND lifecycle actions complete within five seconds';
const TABLE_SELECTOR = '[data-testid="preview-table-scroll"] [role="table"]';
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const encoded = value => encodeURIComponent(value);
const apiRoot = (project, explorer) => `/api/v1/projects/${encoded(project)}/explorers/${encoded(explorer)}`;
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const sortedSelectionRefs = members => members.map(member => {
  const ref = member.ref ?? {};
  return `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`;
}).sort();
const selectionRef = row => `${row.project}/${row.generation}/${row.resourceType}/${row.id}`;
const proposalReady = (outputId, rowCount) => proposalPreviewReadinessExpression(outputId, rowCount);
export const readSelectOptionsInPage = selectElement => [...selectElement.options].map(option => ({
  value: option.value, text: option.textContent, disabled: option.disabled,
}));

const savedReady = (outputId, rowCount) => `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector(${JSON.stringify(TABLE_SELECTOR)});return Boolean(p?.dataset.previewStatus==='ready'&&p.dataset.previewOutputId===${JSON.stringify(outputId)}&&t&&t.getAttribute('aria-rowcount')===${JSON.stringify(String(rowCount + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
export const selectedSavedReady = (outputId, rowCount) => `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"][aria-current="page"]`)})&&${savedReady(outputId, rowCount)})`;
export const emptyTargetReady = outputId => `(()=>{const selected=document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"][aria-current="page"]`)});const preview=document.querySelector('[data-testid="construction-preview"]');return Boolean(selected&&!document.querySelector('[data-testid="construction-proposal-panel"]')&&!document.querySelector('[data-testid="construction-operation-editor"]')&&!document.querySelector('[data-testid="construction-history"]')&&preview?.dataset.previewStatus==='empty'&&preview.dataset.previewOutputId===${JSON.stringify(outputId)}&&!preview.querySelector('[role="table"]'))})()`;
export const reloadSelectedTarget = async ({ select, waitReady, reload }) => {
  await select();
  await waitReady('before reload');
  await reload();
  await waitReady('after reload');
};
export const exactSelectionReadMatches = (method, requestURL, expectedOrigin, expectedPath) => {
  if (method !== 'GET') return false;
  try {
    const url = new URL(requestURL);
    return url.origin === expectedOrigin && url.pathname === expectedPath;
  } catch { return false; }
};
const waitWithinDeadline = (promise, remainingMs, label) => {
  let timer;
  const timeout = new Promise((resolve, reject) => {
    timer = setTimeout(() => reject(new Error(`${label} exceeded the remaining action deadline`)), Math.max(1, remainingMs()));
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
};
export const readReloadedSelectionResponse = async (browserRequest, remainingMs, selectionID) => {
  const response = await waitWithinDeadline(browserRequest.response(), remainingMs, `Reloaded selection ${selectionID} response`);
  assert(response, `Reloaded selection ${selectionID} request completed without a response`);
  assert.equal(response.status(), 200, `Reloaded selection ${selectionID} returned HTTP ${response.status()}`);
  const failure = await waitWithinDeadline(response.finished(), remainingMs, `Reloaded selection ${selectionID} response body`);
  assert.equal(failure, null, `Reloaded selection ${selectionID} response did not finish cleanly: ${failure}`);
  return response;
};

function runAQL(arangoContainer, query, label) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const invocation = buildArangoShellInvocation({ container: arangoContainer, database: 'loom_dev', script: javascript });
  const result = spawnSync(invocation.command, invocation.args,
    { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `${label} failed: ${normalize(result.stderr || result.stdout).slice(0, 1_800)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `${label} returned no JSON array: ${normalize(result.stdout).slice(0, 800)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows), `${label} did not return an array`);
  return rows;
}

export async function cdaCurrentDraftUpstreamAppendWorkflow({ page, cda }) {
  const { target, request, report } = cda;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target);
  assert(project && generation && apiOrigin && uiOrigin, 'Owned CDA fixture must provide project, generation, API, and UI origins');
  assert.equal(project, target.fixtureProject, 'Bounded CDA witnesses must use the exact owned project');
  assert.equal(generation, 'cda-fhir-v1', 'Group→DERIVE→APPEND requires the pinned FHIR generation');

  const rawScans = Object.fromEntries(CDA_UPSTREAM_APPEND_RESOURCES.map(resource => [
    resource.resourceType,
    runAQL(arangoContainer, cdaUpstreamAppendScanQuery({ ...resource, project, generation }),
      `Bounded exact-scope ${resource.resourceType}.${resource.fieldPath} scan`),
  ]));
  let oracle;
  try {
    oracle = prepareCdaUpstreamAppendOracle(rawScans, { project, generation });
  } catch (error) {
    if (!(error instanceof CdaUpstreamAppendWitnessUnavailable)) throw error;
    report.boundedWitnessUnavailable = error.evidence;
    report.gaps ??= [];
    report.gaps.push({
      assertion: 'bounded CDA Patient.id and two disjoint Observation.status final-row pairs support duplicate-category Group→DERIVE→APPEND',
      status: 'untested',
      reason: error.message,
      evidence: error.evidence,
    });
    throw error;
  }

  const api = async (path, body) => {
    const url = `${apiOrigin}${path}`;
    const headers = { 'X-Request-ID': `cda-upstream-append-${randomUUID()}` };
    const response = body === undefined
      ? await request.get(url, { headers, timeout: 30_000 })
      : await request.post(url, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${normalize(text).slice(0, 1_200)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1_800)}`);
    return value;
  };
  let explorer;
  let explorerBase;
  let requestCapture;
  let builder;
  let initialScope;
  let publishRequests = 0;
  const ownedOrigins = new Set([apiOrigin, uiOrigin].map(value => new URL(value).origin));
  const onRequest = browserRequest => {
    try {
      const url = new URL(browserRequest.url());
      if (ownedOrigins.has(url.origin) && url.pathname.endsWith('/authoring/v2/publish')) publishRequests += 1;
    } catch { /* A non-URL request cannot be a Publish call. */ }
  };
  page.on('request', onRequest);

  const requireCheck = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, passed, evidence);
  const waitFunction = (predicate, timeout = MAX_ACTION_MS) => page.waitForFunction(predicate, undefined, { timeout: Math.min(MAX_ACTION_MS, timeout) });
  const waitSelector = (selector, timeout = MAX_ACTION_MS) => page.locator(selector).waitFor({ state: 'visible', timeout: Math.min(MAX_ACTION_MS, timeout) });
  const timedAction = (label, locator, perform, after, editable = false) => cda.action(label, locator, perform, {
    timeout: MAX_ACTION_MS,
    budget: MAX_ACTION_MS,
    ...(after ? { after, requiredCheck: ACTION_CHECK } : {}),
    ...(editable ? { editable: true } : {}),
  });
  const click = (label, locator, after) => timedAction(label, locator, item => item.click({ timeout: MAX_ACTION_MS }), after);
  const fill = (label, locator, value, after) => timedAction(label, locator, item => item.fill(value, { timeout: MAX_ACTION_MS }), after, true);
  const select = (label, locator, value, after) => timedAction(label, locator, item => item.selectOption(value, { timeout: MAX_ACTION_MS }), after);
  const readBuilder = () => api(`${explorerBase}/authoring/v2/builder`);
  const documentByOutput = (state, outputId) => {
    const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
    assert.equal(matches.length, 1, `Expected one current-draft document for ${outputId}`);
    return matches[0];
  };
  const command = async commands => {
    assert(builder?.catalog?.snapshotToken, 'Current-draft commands require the exact Builder snapshot');
    const body = {
      commandId: randomUUID(),
      semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    };
    await api(`${explorerBase}/authoring/v2/commands`, body);
    builder = await readBuilder();
    checkBuilderScope(builder);
    return builder;
  };
  const checkBuilderScope = state => {
    assert.equal(state.catalog?.generation, generation, 'Builder left the pinned CDA generation');
    assert.equal(state.catalog?.snapshotToken, initialScope.snapshotToken, 'Builder snapshot changed during the current-draft lifecycle');
    assert.equal(state.catalog?.authorizationScopeDigest, initialScope.authorizationScopeDigest,
      'Builder scope digest changed during the current-draft lifecycle');
  };
  const readGrid = async kind => page.evaluate(({ kind }) => {
    const proposal = kind === 'proposal'
      ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]') : null;
    if (kind === 'proposal' && !proposal) return { ready: false, headers: [], rows: [], ariaRowCount: null };
    const table = kind === 'proposal' ? proposal.querySelector('table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { ready: false, headers: [], rows: [], ariaRowCount: null };
    const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = kind === 'proposal'
      ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
    const rows = kind === 'proposal'
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
    return { ready: true, headers, rows, ariaRowCount: table.getAttribute('aria-rowcount') };
  }, { kind });
  const assertRows = (name, grid, headers, expectedRows) => {
    const actual = grid.rows.map(row => row.map(normalize));
    const expected = expectedRows.map(row => row.map(normalize));
    const ok = grid.ready && isDeepStrictEqual(grid.headers, headers) && isDeepStrictEqual(sortRows(actual), sortRows(expected));
    requireCheck('correctness', name, ok, { headers: grid.headers, expectedHeaders: headers, rows: actual, expectedRows: expected,
      comparisonMode: 'unordered-row-multiset', rowOrderAsserted: false, multiplicityPreserved: true });
    assert(ok, `${name}: exact rows differ from the bounded raw CDA oracle`);
  };
  const previewIdentity = () => page.evaluate(() => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return {
      status: preview?.dataset.previewStatus ?? null,
      receipt: preview?.dataset.previewReceiptId ?? null,
      outputId: preview?.dataset.previewOutputId ?? null,
      draftVersion: preview?.dataset.currentDraftVersion ?? null,
      draftDigest: preview?.dataset.currentDraftDigest ?? null,
      stale: Boolean(document.querySelector('[data-testid="construction-preview-stale-notice"]')),
    };
  });
  const waitProposal = (outputId, rowCount) => waitFunction(proposalReady(outputId, rowCount));
  const waitSaved = (outputId, rowCount) => waitFunction(savedReady(outputId, rowCount));
  const waitEmptyTarget = outputId => waitFunction(emptyTargetReady(outputId));
  const tableLocator = outputId => page.getByTestId(`construction-table-${outputId}`);
  const selectTable = async (outputId, rowCount, label) => {
    const locator = tableLocator(outputId);
    if (await locator.getAttribute('aria-current') === 'page') {
      if (rowCount !== undefined) await waitSaved(outputId, rowCount);
      return;
    }
    await click(label, locator, async () => {
      if (rowCount === undefined) await waitFunction(`Boolean(document.querySelector('[data-testid="construction-table-${outputId}"][aria-current="page"]'))`);
      else await waitSaved(outputId, rowCount);
    });
  };
  const waitForReloadedSelection = (selectionID, deadline) => {
    const expectedOrigin = new URL(uiOrigin).origin;
    const expectedPath = `${explorerBase}/selections/${encoded(selectionID)}`;
    const matches = browserRequest => exactSelectionReadMatches(browserRequest.method(), browserRequest.url(), expectedOrigin, expectedPath);
    return page.waitForRequest(matches, { timeout: Math.max(1, deadline - performance.now()) }).then(async browserRequest => {
      return readReloadedSelectionResponse(browserRequest, () => deadline - performance.now(), selectionID);
    });
  };
  const reloadSelectTable = async (outputId, rowCount, label, selectionID) => {
    const locator = tableLocator(outputId);
    let deadline;
    const readiness = rowCount === undefined ? emptyTargetReady(outputId) : selectedSavedReady(outputId, rowCount);
    const waitTargetReady = () => waitFunction(readiness, Math.max(1, deadline - performance.now()));
    await timedAction(label, locator, async item => {
      deadline = performance.now() + MAX_ACTION_MS;
      await reloadSelectedTarget({
        select: async () => {
          if (await item.getAttribute('aria-current') !== 'page') await item.click({ timeout: Math.max(1, deadline - performance.now()) });
        },
        waitReady: waitTargetReady,
        reload: async () => {
          const selectionWait = selectionID ? waitForReloadedSelection(selectionID, deadline) : undefined;
          const reloadPage = page.reload({ waitUntil: 'domcontentloaded', timeout: Math.max(1, deadline - performance.now()) });
          if (selectionWait) await Promise.all([reloadPage, selectionWait]);
          else await reloadPage;
        },
      });
    }, waitTargetReady);
  };
  const reloadBuilder = async outputId => {
    await cda.navigate(`${uiOrigin}/?project=${encoded(project)}&explorer=${encoded(explorer)}&mode=builder`);
    await waitSelector(`[data-testid="construction-table-${outputId}"]`);
  };
  const assertPreviewMatchesBuilder = async (outputId, state, expectedReceipt) => {
    const preview = await previewIdentity();
    const ok = preview.status === 'ready' && !preview.stale && preview.outputId === outputId && Boolean(preview.receipt) &&
      Number(preview.draftVersion) === state.draftVersion && preview.draftDigest === state.draftDigest &&
      (!expectedReceipt || preview.receipt !== expectedReceipt);
    requireCheck('persistence', 'current-draft preview receipt, version, and digest match the exact saved workspace', ok, {
      preview, draftVersion: state.draftVersion, draftDigest: state.draftDigest,
      previousReceipt: expectedReceipt ?? null,
    });
    assert(ok, 'Current preview does not bind the exact current-draft receipt and CAS');
    return preview;
  };

  try {
    const boundedEvidence = {
      project, generation, scanLimitPerResource: oracle.scanLimitPerResource,
      scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
      resources: oracle.resources,
      selections: Object.fromEntries(Object.entries(oracle.sources).map(([sourceKey, rows]) => [sourceKey,
        rows.map(row => ({ resourceType: row.resourceType, arangoDocumentKey: row._id, fhirID: row.id,
          value: row.fieldValue, present: row.fieldPresent }))])),
      categoryWitness: oracle.categories,
      duplicateAppendCategoryCount: oracle.append.duplicateStatusCount,
    };
    requireCheck('correctness', 'bounded raw CDA witnesses select two Patient FHIR IDs and two disjoint Observation.status final pairs with duplicate APPEND categories', true, boundedEvidence);
    assert(Object.keys(oracle.sources).length === 3 && Object.values(oracle.sources).every(rows => rows.length === 2));
    assert(Object.values(oracle.sources).every(rows => rows.every(row => row.id !== row._id)),
      'Selection witnesses must keep FHIR ids distinct from Arango _id values');
    report.rawOracle = boundedEvidence;

    const rawExplorer = `cda-upstream-append-${randomUUID()}`;
    const title = `CDA current-draft Group Derive Append ${randomUUID().slice(0, 8)}`;
    const created = await api(`/api/v1/projects/${encoded(project)}/explorers`, { name: rawExplorer, title });
    explorer = created.explorerId ?? created.id ?? created.explorer?.id ?? rawExplorer;
    assert.equal(explorer, rawExplorer, 'Fresh owned Explorer must retain its unique requested identity');
    explorerBase = apiRoot(project, explorer);
    requestCapture = cda.captureRequests(`${explorerBase}/authoring/v2`, { responsePaths: /commands|construction-proposals|preview/ });
    report.target.explorer = explorer;
    const list = await api(`/api/v1/projects/${encoded(project)}/explorers`);
    const summaries = Array.isArray(list) ? list : list.explorers ?? list.value ?? [];
    const matching = summaries.filter(item => (item.explorerId ?? item.id ?? item.name) === explorer);
    assert.equal(matching.length, 1, 'Fresh CDA Explorer must appear exactly once in its owned project');
    assert.equal(matching[0].project, project);
    assert.equal(matching[0].title, title);
    assert.equal(matching[0].management, 'INTERACTIVE');
    builder = await readBuilder();
    const empty = builderDraftStateEvidence(builder, 'empty');
    assert.equal(builder.catalog?.generation, generation);
    assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest);
    initialScope = {
      generation: builder.catalog.generation,
      snapshotToken: builder.catalog.snapshotToken,
      authorizationScopeDigest: builder.catalog.authorizationScopeDigest,
    };
    const fresh = { project: matching[0].project === project, generation: initialScope.generation === generation,
      snapshot: Boolean(initialScope.snapshotToken), scopeDigest: Boolean(initialScope.authorizationScopeDigest), emptyDraft: empty.ok };
    requireCheck('correctness', 'fresh CDA Explorer preserves exact project, generation, snapshot, baseline scope digest, and empty draft',
      Object.values(fresh).every(Boolean), { project, explorer, title, initialScope, currentScopeDigest: builder.catalog.authorizationScopeDigest,
        scopeDigestMatchesBaseline: builder.catalog.authorizationScopeDigest === initialScope.authorizationScopeDigest, empty });
    assert(Object.values(fresh).every(Boolean));

    const sourceByKey = new Map();
    const sourceDefinitions = [];
    for (const definition of CDA_UPSTREAM_APPEND_SOURCES) {
      const rawRows = oracle.sources[definition.sourceKey];
      assert.equal(rawRows.length, 2, `${definition.sourceKey} must have exactly two raw members`);
      const rootNodes = builder.catalog.nodes.filter(node => node.resourceType === definition.resourceType && node.rowRootEligible);
      assert.equal(rootNodes.length, 1, `Scoped catalog must expose one ${definition.resourceType} root`);
      const root = rootNodes[0];
      const candidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === root.nodeId);
      const idCandidates = candidates.filter(candidate => candidate.fieldPath === 'id');
      const keyCandidates = candidates.filter(candidate => candidate.fieldPath === definition.fieldPath);
      assert.equal(idCandidates.length, 1, `Scoped ${definition.resourceType} catalog needs one FHIR id candidate`);
      assert.equal(keyCandidates.length, 1, `Scoped ${definition.resourceType} catalog needs one ${definition.fieldPath} candidate`);
      assert.equal(keyCandidates[0].logicalType, 'string', `${definition.resourceType}.${definition.fieldPath} must be scalar text`);
      assert.equal(keyCandidates[0].repeated, false, `${definition.resourceType}.${definition.fieldPath} must not be repeated`);
      const beforeIDs = new Set((builder.workspace?.documents ?? []).map(document => document.output?.id));
      await command([{ type: 'CREATE_TABLE', title: definition.title, rootNodeId: root.nodeId }]);
      const createdDocs = builder.workspace.documents.filter(document => !beforeIDs.has(document.output.id));
      assert.equal(createdDocs.length, 1);
      const outputId = createdDocs[0].output.id;
      const idColumnTitle = definition.resourceType === 'Patient' ? 'Patient FHIR ID' : `${definition.title} FHIR ID`;
      const keyColumnTitle = definition.fieldPath === 'id' ? idColumnTitle : definition.title;
      await command([{
        type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidates[0].candidateId,
        projectionMode: 'VALUE', initialPresentation: 'TABLE', title: idColumnTitle,
      }]);
      if (definition.fieldPath !== 'id') {
        await command([{
          type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: keyCandidates[0].candidateId,
          projectionMode: 'VALUE', initialPresentation: 'TABLE', title: keyColumnTitle,
        }]);
      }
      const refs = rawRows.map(row => ({ project, generation, resourceType: definition.resourceType, id: row.id }));
      assert(refs.every(ref => !Object.hasOwn(ref, '_id')));
      const selection = await api(`${explorerBase}/selections`, {
        snapshotToken: builder.catalog.snapshotToken,
        idempotencyKey: `cda-upstream-append-${randomUUID()}`,
        source: { kind: 'resources', resources: { refs } },
      });
      assert.equal(selection.project, project);
      assert.equal(selection.generation, generation);
      assert.equal(selection.resourceType, definition.resourceType);
      assert.equal(selection.scopeDigest, initialScope.authorizationScopeDigest);
      assert.equal(selection.memberCount, rawRows.length);
      const selectionRead = await api(`${explorerBase}/selections/${encoded(selection.id)}?limit=100`);
      const members = selectionRead.members ?? [];
      assert.equal(selectionRead.revision?.id, selection.id);
      assert.equal(selectionRead.revision?.project, project);
      assert.equal(selectionRead.revision?.generation, generation);
      assert.equal(selectionRead.revision?.resourceType, definition.resourceType);
      assert.equal(selectionRead.revision?.scopeDigest, initialScope.authorizationScopeDigest);
      assert.equal(selectionRead.revision?.memberCount, rawRows.length);
      assert.equal(members.length, rawRows.length);
      const actualRefs = sortedSelectionRefs(members);
      const expectedRefs = refs.map(selectionRef).sort();
      assert.deepEqual(actualRefs, expectedRefs, `Immutable ${definition.sourceKey} selection must exactly contain its two FHIR IDs`);
      assert(members.every(member => typeof member.memberKey === 'string' && member.memberKey));
      assert.equal(new Set(members.map(member => member.memberKey)).size, rawRows.length);
      const rereadRows = runAQL(arangoContainer, cdaUpstreamAppendRereadQuery({
        project, generation, resourceType: definition.resourceType, fieldPath: definition.fieldPath,
        documentIDs: rawRows.map(row => row._id),
      }), `Exact selected ${definition.sourceKey} raw reread`);
      const reread = assertCdaUpstreamAppendReread(rereadRows, oracle.exactExpected[definition.sourceKey], {
        project, generation, resourceType: definition.resourceType,
      });
      const routes = await api(`${explorerBase}/authoring/v2/population-routes`, {
        snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
      });
      const direct = routes.choices.find(choice => choice.route.length === 0);
      assert(direct, `Exact ${definition.sourceKey} selection must expose its direct root route`);
      await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
      const sourceDocument = documentByOutput(builder, outputId);
      const sourceIDColumns = sourceDocument.columns.filter(column => column.source?.field?.path === 'id' &&
        column.source?.field?.projectionMode === 'VALUE');
      const sourceKeyColumns = sourceDocument.columns.filter(column => column.source?.field?.path === definition.fieldPath &&
        column.source?.field?.projectionMode === 'VALUE');
      const sourceIsExact = sourceDocument.rootResourceType === definition.resourceType &&
        sourceDocument.population?.selectionRevisionId === selection.id && (sourceDocument.population?.route?.length ?? 0) === 0 &&
        sourceIDColumns.length === 1 && sourceKeyColumns.length === 1;
      requireCheck('correctness', `${definition.title} immutable current-draft population binds exactly its scoped raw FHIR witnesses`, sourceIsExact,
        { sourceKey: definition.sourceKey, outputId, selectionId: selection.id, selectionCount: members.length, actualRefs, expectedRefs,
          generation, scopeDigest: selection.scopeDigest, reread, idColumnCount: sourceIDColumns.length, keyColumnCount: sourceKeyColumns.length });
      assert(sourceIsExact);
      const source = { definition, sourceKey: definition.sourceKey, outputId, selection, rawRows,
        keyColumnTitle, idColumnTitle, rootNodeId: root.nodeId };
      sourceByKey.set(definition.sourceKey, source);
      sourceDefinitions.push(source);
      builder = await readBuilder();
      checkBuilderScope(builder);
    }
    const leftObservationIDs = sourceByKey.get('observation-left').rawRows.map(row => row._id);
    const rightObservationIDs = sourceByKey.get('observation-right').rawRows.map(row => row._id);
    const patientFHIRIDs = sourceByKey.get('patient-id').rawRows.map(row => row.id);
    const populationsDistinct = new Set(sourceDefinitions.map(source => source.selection.id)).size === 3 &&
      sourceDefinitions.every(source => source.rawRows.length === 2 && source.selection.memberCount === 2) &&
      new Set([...leftObservationIDs, ...rightObservationIDs]).size === 4 &&
      new Set(patientFHIRIDs).size === 2;
    requireCheck('correctness', 'three immutable populations preserve disjoint Observation pairs and exact Patient FHIR IDs', populationsDistinct,
      { sources: sourceDefinitions.map(source => ({ sourceKey: source.sourceKey, resourceType: source.definition.resourceType,
        outputId: source.outputId, selectionId: source.selection.id, memberCount: source.selection.memberCount })),
        leftObservationIDs, rightObservationIDs, patientFHIRIDs });
    assert(populationsDistinct);

    await reloadBuilder(sourceByKey.get('observation-left').outputId);
    const checkVisibleRawSource = async source => {
      const rows = source.rawRows.length;
      await selectTable(source.outputId, rows, `Open exact ${source.definition.title} population`);
      const grid = await readGrid('saved');
      const sourceDocument = documentByOutput(await readBuilder(), source.outputId);
      const idColumn = sourceDocument.columns.find(column => column.source?.field?.path === 'id');
      const keyColumn = sourceDocument.columns.find(column => column.source?.field?.path === source.definition.fieldPath);
      assert(idColumn && keyColumn);
      const idIndex = grid.headers.indexOf(idColumn.label);
      const keyIndex = grid.headers.indexOf(keyColumn.label);
      const hasSeparateKey = source.definition.fieldPath !== 'id';
      const actual = grid.rows.map(row => hasSeparateKey ? [row[idIndex], row[keyIndex]] : [row[idIndex]])
        .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
      const expected = source.rawRows.map(row => hasSeparateKey ? [row.id, String(row.key)] : [row.id])
        .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
      const exact = grid.ready && idIndex >= 0 && (!hasSeparateKey || keyIndex >= 0) && isDeepStrictEqual(actual, expected);
      requireCheck('correctness', `${source.definition.title} root preview equals its exact immutable raw population`, exact,
        { sourceKey: source.sourceKey, outputId: source.outputId, selectionId: source.selection.id, headers: grid.headers,
          actual, expected, oneFHIRIDColumn: sourceDocument.columns.filter(column => column.source?.field?.path === 'id').length === 1 });
      assert(exact);
      return { idColumn, keyColumn };
    };
    const groupSource = async source => {
      source.groupRows = oracle.grouped[source.sourceKey].map(([key, count]) => [String(key), String(count)]);
      const { idColumn, keyColumn } = await checkVisibleRawSource(source);
      await click(`Open ${source.definition.title} row settings`, page.getByTestId('construction-rows-settings-trigger'),
        async () => waitFunction(`document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled===false`));
      const proposalPath = `${explorerBase}/authoring/v2/construction-proposals`;
      const proposalOrigin = new URL(uiOrigin).origin;
      const groupProposalFromIndex = report.nativeRequests.length;
      await click(`Open native ${source.definition.title} GROUP`, page.getByTestId('construction-action-group-rows'),
        async () => waitSelector('select[aria-label="Summary 1"]'));
      const summary = page.locator('select[aria-label="Summary 1"]');
      assert.equal(await summary.inputValue(), 'COUNT_ROWS', 'New native GROUP must start with COUNT_ROWS');
      const groupKey = page.locator(`input[type="checkbox"][aria-label=${JSON.stringify(`Group by ${keyColumn.label}`)}]`);
      assert.equal(await groupKey.count(), 1, 'GROUP editor must expose the exact selected raw FHIR key column');
      const groupBase = await readBuilder();
      const replacementFromIndex = report.nativeRequests.length;
      let checkboxClickedAt;
      await timedAction(`Select ${source.definition.title} native GROUP key and render COUNT_ROWS`, groupKey, async item => {
        checkboxClickedAt = Date.now();
        await item.click({ timeout: MAX_ACTION_MS });
      },
        async () => waitProposal(source.outputId, source.groupRows.length));
      const grid = await readGrid('proposal');
      assertRows(`${source.definition.title} native GROUP preview matches exact selected raw counts`, grid,
        [keyColumn.label, 'Row count'], source.groupRows);
      const groupEvent = await requestCapture.waitFor(entry => entry.path === proposalPath &&
        entry.origin === proposalOrigin && entry.method === 'POST' && entry.status === 200 &&
        Number.isFinite(entry.completedAt) && entry.failure === undefined && entry.responseReadError === undefined &&
        requestCapture.rawRequestBody(entry)?.outputId === source.outputId &&
        requestCapture.rawRequestBody(entry)?.snapshotToken === groupBase.catalog.snapshotToken &&
        requestCapture.rawRequestBody(entry)?.expectedDraftVersion === groupBase.draftVersion &&
        requestCapture.rawRequestBody(entry)?.expectedDraftDigest === groupBase.draftDigest &&
        requestCapture.rawRequestBody(entry)?.candidateConstruction?.steps?.length === 1 &&
        requestCapture.rawRequestBody(entry)?.candidateConstruction?.steps?.[0]?.operation?.kind === 'GROUP' &&
        requestCapture.rawRequestBody(entry)?.candidateConstruction?.steps?.[0]?.operation?.group?.keys?.length === 1 &&
        requestCapture.rawRequestBody(entry)?.candidateConstruction?.steps?.[0]?.operation?.group?.keys?.[0]?.inputColumnId === keyColumn.columnId,
      { fromIndex: replacementFromIndex, timeoutMs: MAX_ACTION_MS });
      const groupRequest = requestCapture.rawRequestBody(groupEvent);
      const groupResponse = requestCapture.rawResponseBody(groupEvent);
      const proposalDOM = await page.evaluate(proposalPreviewStateInPage, source.outputId);
      assert.equal(proposalDOM.proposalPanelCount, 1, 'The replacement proposal must have exactly one native proposal panel');
      assert.equal(proposalDOM.proposalStatus, 'ready', 'The replacement proposal panel must be READY');
      assert.equal(proposalDOM.proposalId, groupResponse.proposalId, 'The visible proposal must bind the captured replacement response');
      assert.equal(proposalDOM.resultSectionCount, 1, 'The current output must expose exactly one preview section');
      assert.equal(proposalDOM.resultOutputId, source.outputId, 'The visible replacement preview must target the current output');
      assert.equal(proposalDOM.resultReceiptId, groupResponse.proposalId, 'The visible current preview receipt must match the response');
      assert.equal(proposalDOM.proposalPreviewCount, 1, 'The current output must expose exactly one proposal preview');
      assert.equal(proposalDOM.previewStatus, 'ready', 'The visible replacement preview must be READY');
      assert.equal(proposalDOM.previewOutputId, source.outputId);
      assert.equal(proposalDOM.previewReceiptId, groupResponse.proposalId);
      const groupStep = groupRequest?.candidateConstruction?.steps?.at(-1);
      assert(groupStep?.operation?.kind === 'GROUP');
      const aggregate = groupStep.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
      assert(aggregate, 'Native GROUP must use COUNT_ROWS');
      const outputs = groupStep.outputs ?? [];
      const keyOutput = outputs.find(output => groupStep.operation.group.keys.some(key => key.outputColumnId === output.id));
      const countOutput = outputs.find(output => output.id === aggregate.outputColumnId);
      assert(keyOutput && countOutput);
      source.groupCandidate = { groupStep, keyOutput, countOutput, idColumn, keyColumn };
      const proposalCAS = groupRequest.expectedDraftVersion === groupBase.draftVersion &&
        groupRequest.expectedDraftDigest === groupBase.draftDigest && groupRequest.snapshotToken === groupBase.catalog.snapshotToken &&
        groupResponse.previewStatus === 'READY' && groupResponse.preview?.outputId === source.outputId &&
        Boolean(groupResponse.preview?.receiptId) && groupResponse.preview.receiptId === groupResponse.proposalId &&
        proposalDOM.proposalId === groupResponse.proposalId && proposalDOM.resultReceiptId === groupResponse.proposalId &&
        proposalDOM.previewReceiptId === groupResponse.proposalId;
      const proposalEntries = report.nativeRequests.slice(groupProposalFromIndex).filter(entry =>
        entry.origin === proposalOrigin && entry.path === proposalPath && entry.method === 'POST' &&
        Number.isFinite(entry.completedAt));
      const supersessionProofs = proveObservedSupersededEmptyGroupProposals({
        proposalEntries: proposalEntries.map(entry => ({ entry, body: requestCapture.rawRequestBody(entry) })),
        replacementEntry: groupEvent,
        replacementBody: groupRequest,
        replacementResponse: groupResponse,
        uiOrigin: proposalOrigin,
        proposalPath,
        outputId: source.outputId,
        snapshotToken: groupBase.catalog.snapshotToken,
        draftVersion: groupBase.draftVersion,
        draftDigest: groupBase.draftDigest,
        inputColumnId: keyColumn.columnId,
        checkboxClickedAt,
        previewRowCount: source.groupRows.length,
        previewRowsMatched: true,
      });
      const proposalAborts = proposalEntries.filter(entry => entry.failure === 'net::ERR_ABORTED');
      for (let index = 0; index < proposalAborts.length; index += 1) {
        cda.expectCapturedCancellation(proposalAborts[index],
          'Initial empty-key GROUP proposal was superseded by the user-selected key and exact READY preview',
          supersessionProofs[index]);
      }
      requireCheck('correctness', `${source.definition.title} native GROUP binds its exact raw field and COUNT_ROWS`,
        groupStep.operation.group.keys.some(key => key.inputColumnId === keyColumn.columnId) && aggregate.operation === 'COUNT_ROWS' && proposalCAS,
        { outputId: source.outputId, inputColumnId: keyColumn.columnId, groupStepId: groupStep.id,
          keyOutput, countOutput, groupOperation: groupStep.operation.group, proposalCAS, proposalDOM,
          supersededIntermediateProposals: supersessionProofs,
          draftVersion: groupRequest.expectedDraftVersion, draftDigest: groupRequest.expectedDraftDigest });
      await click(`Apply ${source.definition.title} native GROUP`, page.getByTestId('construction-apply-proposal'),
        async () => waitSaved(source.outputId, source.groupRows.length));
      const beforeReload = await readBuilder();
      builder = beforeReload;
      await reloadSelectTable(source.outputId, source.groupRows.length, `Reload exact ${source.definition.title} GROUP values`, source.selection.id);
      const afterReload = await readBuilder();
      builder = afterReload;
      checkBuilderScope(afterReload);
      const saved = documentByOutput(afterReload, source.outputId);
      const savedGroup = saved.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
      const savedCount = savedGroup?.outputs?.find(output => savedGroup.operation.group.aggregates.some(item => item.outputColumnId === output.id));
      const savedKey = savedGroup?.outputs?.find(output => savedGroup.operation.group.keys.some(item => item.outputColumnId === output.id));
      const persistent = savedGroup?.id === groupStep.id && savedGroup.operation.group.aggregates.some(item => item.operation === 'COUNT_ROWS') &&
        savedGroup.operation.group.keys.some(item => item.inputColumnId === keyColumn.columnId);
      requireCheck('persistence', `${source.definition.title} GROUP key and COUNT_ROWS survive reload`, persistent,
        { outputId: source.outputId, draftVersionBeforeReload: beforeReload.draftVersion,
          draftVersionAfterReload: afterReload.draftVersion, groupStepId: savedGroup?.id, savedKey, savedCount });
      assert(persistent);
      assertRows(`${source.definition.title} grouped source values survive reload`, await readGrid('saved'),
        [savedKey.label, savedCount.label], source.groupRows);
      source.group = { ...source.groupCandidate, groupStep: savedGroup, keyOutput: savedKey, countOutput: savedCount };
      source.keyLabel = savedKey.label;
      source.countLabel = savedCount.label;
      return source;
    };

    const observationLeft = await groupSource(sourceByKey.get('observation-left'));
    const observationRight = await groupSource(sourceByKey.get('observation-right'));
    const patient = await groupSource(sourceByKey.get('patient-id'));
    const makePatientDerive = async (offset, { apply, cancel = false } = {}) => {
      await selectTable(patient.outputId, patient.groupRows.length, 'Select Patient FHIR ID GROUP before native DERIVE');
      const sourceBuilder = await readBuilder();
      checkBuilderScope(sourceBuilder);
      const patientDocument = documentByOutput(sourceBuilder, patient.outputId);
      const savedGroup = patientDocument.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
      const groupCount = savedGroup?.outputs?.find(output => savedGroup.operation.group.aggregates.some(item => item.outputColumnId === output.id));
      const groupKey = savedGroup?.outputs?.find(output => savedGroup.operation.group.keys.some(item => item.outputColumnId === output.id));
      assert(groupCount?.name === 'row_count' && groupKey, 'Patient GROUP must expose row_count and FHIR ID key outputs');
      const beganAt = Date.now();
      await click(`Open Patient native DERIVE for +${offset}`, page.getByTestId('construction-action-calculate'),
        async () => waitSelector('[data-testid="construction-calculate-editor"]'));
      await click('Open native formula editor', page.getByRole('button', { name: 'Formula editor', exact: true }));
      await fill(`Set native Patient DERIVE formula to row_count + ${offset}`,
        page.locator('textarea[aria-label="Formula"]'), `${groupCount.name} + ${offset}`);
      await fill('Set Patient DERIVE output name', page.locator('input[aria-label="Output column name"]'), 'row_count_offset');
      const expectedPatientRows = offset === 1 ? oracle.patientDerived.plusOne : oracle.patientDerived.plusTwo;
      await fill(`Set Patient DERIVE label and render exact +${offset} rows`,
        page.locator('input[aria-label="Output column label"]'), `Count plus ${offset}`,
        async () => waitProposal(patient.outputId, expectedPatientRows.length));
      const grid = await readGrid('proposal');
      assertRows(`Patient GROUP→DERIVE +${offset} preview matches exact selected raw counts`, grid,
        [groupKey.label, groupCount.label, `Count plus ${offset}`], expectedPatientRows);
      const elapsedMs = Date.now() - beganAt;
      requireCheck('performance', `Patient native Group→DERIVE +${offset} discovery, configuration, and preview complete within five seconds`,
        elapsedMs <= MAX_ACTION_MS, { elapsedMs, outputId: patient.outputId, groupStepId: savedGroup.id, offset });
      assert(elapsedMs <= MAX_ACTION_MS, `Patient Group→DERIVE +${offset} exceeded five seconds`);
      if (cancel) {
        return { grid, expectedPatientRows, groupKey, groupCount };
      }
      if (apply) {
        await click(`Apply Patient Group→DERIVE +${offset}`, page.getByTestId('construction-apply-proposal'),
          async () => waitSaved(patient.outputId, expectedPatientRows.length));
        const after = await readBuilder();
        checkBuilderScope(after);
        const saved = documentByOutput(after, patient.outputId);
        const derive = saved.construction?.steps?.find(step => step.operation?.kind === 'DERIVE');
        const derivedOutput = derive?.outputs?.find(output => output.name === 'row_count_offset');
        const expression = derive?.operation?.derive;
        const exactDerive = Boolean(derive && derivedOutput && derive.inputs?.some(input => input.kind === 'STEP_OUTPUT' && input.stepId === savedGroup.id) &&
          expression.operation === 'ADD' && expression.left?.kind === 'COLUMN' && expression.left.columnId === groupCount.id &&
          expression.right?.kind === 'LITERAL' && expression.right.literal?.kind === 'INTEGER' && expression.right.literal.integer === offset &&
          expression.outputColumnId === derivedOutput.id && saved.construction.steps.map(step => step.operation.kind).join(',') === 'GROUP,DERIVE');
        requireCheck('persistence', `Patient native Group→DERIVE +${offset} saves exact Group lineage and integer arithmetic`, exactDerive,
          { outputId: patient.outputId, groupStepId: savedGroup.id, deriveStepId: derive?.id, expression, derivedOutput });
        assert(exactDerive);
        patient.derive = { step: derive, output: derivedOutput, keyOutput: groupKey, countOutput: groupCount };
        await reloadSelectTable(patient.outputId, expectedPatientRows.length, `Reload Patient Group→DERIVE +${offset}`, patient.selection.id);
        assertRows(`Patient Group→DERIVE +${offset} rows survive reload`, await readGrid('saved'),
          [groupKey.label, groupCount.label, derivedOutput.label], expectedPatientRows);
        return { grid, expectedPatientRows, groupKey, groupCount, after };
      }
      return { grid, expectedPatientRows, groupKey, groupCount };
    };
    await makePatientDerive(1, { apply: true });
    builder = await readBuilder();
    checkBuilderScope(builder);
    const sources = [observationLeft, observationRight, patient];
    const checkSourceShapes = state => sources.map(source => {
      const document = documentByOutput(state, source.outputId);
      return { outputId: source.outputId, selectionId: source.selection.id, root: document.rootResourceType,
        population: document.population, steps: document.construction?.steps?.map(step => step.operation.kind) ?? [],
        columnPaths: document.columns.map(column => column.source?.field?.path ?? null) };
    });
    const sourceShapeBeforeAppend = checkSourceShapes(builder);
    requireCheck('persistence', 'three source tables retain only their exact immutable populations and GROUP, Patient DERIVE lineage',
      sourceShapeBeforeAppend.every(item => item.population?.selectionRevisionId === item.selectionId) &&
      sourceShapeBeforeAppend.map(item => item.steps.join(',')).join('|') === 'GROUP|GROUP|GROUP,DERIVE',
      { sourceShapeBeforeAppend });

    const createAppendTarget = async () => {
      await selectTable(observationLeft.outputId, observationLeft.groupRows.length, 'Select grouped Observation A source before APPEND');
      builder = await readBuilder();
      checkBuilderScope(builder);
      const priorOutputs = builder.workspace.documents.map(document => document.output.id);
      const observationRoots = builder.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible).map(node => node.nodeId);
      const fromIndex = report.nativeRequests.length;
      const combine = page.getByTestId('construction-action-combine');
      await click('Open native Combine and create an empty current-draft target', combine,
        async () => waitSelector('[data-testid="construction-combine-editor"]'));
      const event = await requestCapture.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/commands` &&
        entry.method === 'POST' && entry.status === 200 && entry.completedAt &&
        requestCapture.rawRequestBody(entry)?.commands?.some(item => item.type === 'CREATE_TABLE'),
      { fromIndex, timeoutMs: MAX_ACTION_MS });
      const body = requestCapture.rawRequestBody(event);
      const response = requestCapture.rawResponseBody(event);
      const mountedOutputId = await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]')
        .getAttribute('data-output-id');
      const evidence = nativeCombineTargetBindingEvidence({ requestBody: body, responseStatus: event.status, response,
        expectedRootNodeIds: observationRoots, expectedRootResourceType: 'Observation',
        previousOutputIds: priorOutputs, mountedOutputId });
      requireCheck('correctness', 'native Combine creates a separate rooted empty APPEND target with exact command and response identity', evidence.ok, evidence);
      assert(evidence.ok, `Native Combine target did not match its exact command: ${JSON.stringify(evidence)}`);
      builder = await readBuilder();
      checkBuilderScope(builder);
      const targetDocument = documentByOutput(builder, evidence.outputId);
      assert.equal(targetDocument.columns?.length, 0);
      assert.equal(targetDocument.construction?.steps?.length ?? 0, 0);
      const result = { outputId: evidence.outputId, rootResourceType: 'Observation', baselineDocument: structuredClone(targetDocument),
        commandEvent: event, commandBody: body, commandResponse: response, createdAfterIndex: fromIndex };
      requireCheck('correctness', 'native APPEND target starts empty and rooted in the exact current draft', true,
        { outputId: result.outputId, rootResourceType: targetDocument.rootResourceType, draftVersion: builder.draftVersion });
      return result;
    };
    const chooseOption = async (selector, label, actionLabel, { deferSelection = false } = {}) => {
      const options = await page.locator(selector).evaluate(readSelectOptionsInPage);
      const wanted = normalize(label).toLowerCase();
      const matches = options.filter(option => {
        if (option.disabled) return false;
        const text = normalize(option.text).toLowerCase().replace(/ · current draft$/, '');
        return text === wanted || text.startsWith(wanted + ' (') || text.startsWith(wanted + ' · ');
      });
      assert.equal(matches.length, 1, `Expected one enabled ${JSON.stringify(label)} option in ${selector}: ${JSON.stringify(options)}`);
      if (!deferSelection) await select(actionLabel, page.locator(selector), matches[0].value);
      return matches[0].value;
    };
    const configureAppend = async target => {
      await click('Choose native APPEND', page.getByTestId('construction-combine-choice-append'),
        async () => waitSelector('select[aria-label="Input table 1"]'));
      for (let index = 0; index < sources.length; index += 1) {
        if (index > 1) {
          await click('Add third native APPEND input', page.getByRole('button', { name: 'Add another table', exact: true }),
            async () => waitSelector('select[aria-label="Input table 3"]'));
        }
        const selector = `select[aria-label="Input table ${index + 1}"]`;
        const desired = workspaceOutputOption(sources[index].outputId);
        const options = await page.locator(selector).evaluate(element => [...element.options].map(option => ({
          value: option.value, group: option.parentElement?.label, disabled: option.disabled,
        })));
        const exact = options.filter(option => option.value === desired && option.group === 'Current draft tables' && !option.disabled);
        assert.equal(exact.length, 1, `APPEND must offer the exact current-draft ${sources[index].definition.resourceType} source`);
        await select(`Bind native APPEND input ${index + 1} to exact current-draft source`, page.locator(selector), desired,
          async () => waitFunction(`document.querySelector(${JSON.stringify(selector)})?.value===${JSON.stringify(desired)}`));
      }
      const addOutput = async (index, name, label, sourceLabels, deferFinalMapping = false) => {
        await click(`Add APPEND output field ${index}`, page.getByRole('button', { name: 'Add output field', exact: true }),
          async () => waitSelector(`input[aria-label="Output field ${index} name"]`));
        await fill(`Name APPEND output ${index}`, page.locator(`input[aria-label="Output field ${index} name"]`), name);
        await fill(`Label APPEND output ${index}`, page.locator(`input[aria-label="Output field ${index} label"]`), label);
        let finalMapping;
        for (let sourceIndex = 0; sourceIndex < sourceLabels.length; sourceIndex += 1) {
          const sourceLabel = sourceLabels[sourceIndex];
          const selector = `select[aria-label="Output field ${index} matching field in input ${sourceIndex + 1}"]`;
          const isFinalMapping = index === 2 && sourceIndex === sourceLabels.length - 1;
          const mappingValue = await chooseOption(selector, sourceLabel,
            `Map APPEND ${label} from input ${sourceIndex + 1}`, { deferSelection: deferFinalMapping && isFinalMapping });
          if (index === 2 && sourceIndex === sourceLabels.length - 1) finalMapping = { selector, value: mappingValue };
        }
        return finalMapping;
      };
      await addOutput(1, 'category', 'Category', sources.map(source => source.keyLabel));
      const countLabels = [observationLeft.countLabel, observationRight.countLabel, patient.derive.output.label];
      const finalMapping = await addOutput(2, 'row_count', 'Row count', countLabels, true);
      assert(finalMapping?.selector && finalMapping?.value, 'APPEND mapping must leave its final Patient derived count as preview trigger');
      const fromIndex = report.nativeRequests.length;
      await select('Complete native three-source APPEND mapping and render the raw concatenation row multiset',
        page.locator(finalMapping.selector), finalMapping.value,
        async () => waitProposal(target.outputId, oracle.append.plusOne.length));
      const grid = await readGrid('proposal');
      assertRows('three-source APPEND proposal row multiset matches raw concatenation with duplicate category multiplicity', grid,
        ['Category', 'Row count'], oracle.append.plusOne);
      const event = await requestCapture.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/construction-proposals` &&
        entry.method === 'POST' && entry.status === 200 && entry.completedAt &&
        requestCapture.rawRequestBody(entry)?.outputId === target.outputId,
      { fromIndex: fromIndex, timeoutMs: MAX_ACTION_MS });
      const requestBody = requestCapture.rawRequestBody(event);
      const responseBody = requestCapture.rawResponseBody(event);
      const step = requestBody.candidateConstruction?.steps?.at(-1);
      const inputIDs = (step?.inputs ?? []).map(input => input.outputId);
      const currentDraft = currentDraftSourceEvidence({ inputs: step?.inputs,
        expectedOutputIDs: sources.map(source => source.outputId), sourceDocuments: builder.workspace.documents });
      const responsePreview = responseBody?.preview;
      const currentCAS = requestBody.expectedDraftVersion === builder.draftVersion && requestBody.expectedDraftDigest === builder.draftDigest &&
        requestBody.snapshotToken === builder.catalog.snapshotToken;
      const exactAppend = step?.operation?.kind === 'COMBINE' && step.operation.combine?.kind === 'APPEND' &&
        isDeepStrictEqual(inputIDs, sources.map(source => source.outputId)) && currentDraft.ok && currentCAS &&
        (step.inputs ?? []).every(input => input.kind === 'WORKSPACE_OUTPUT' && !input.tableId && !input.revisionId) &&
        responseBody?.previewStatus === 'READY' && responsePreview?.outputId === target.outputId && Boolean(responsePreview?.receiptId);
      requireCheck('correctness', 'APPEND candidate binds ordered Observation A, Observation B, and Patient FHIR ID Group→DERIVE inputs with its current receipt proposal', exactAppend,
        { event: { path: event.path, origin: event.origin, status: event.status, requestId: event.requestId }, inputIDs,
          expectedInputIDs: sources.map(source => source.outputId),
          inputOrder: ['Observation final-status GROUP A', 'Observation final-status GROUP B', 'Patient FHIR ID GROUP→DERIVE'],
          currentCAS, currentDraft, stepId: step?.id,
          proposalId: responseBody?.proposalId, preview: responsePreview });
      assert(exactAppend, 'APPEND candidate did not bind the exact current draft and ready preview receipt');
      return { grid, event, requestBody, responseBody, step };
    };
    const applyAppend = async (target, preview) => {
      const base = await readBuilder();
      checkBuilderScope(base);
      await click('Apply current-draft APPEND', page.getByTestId('construction-apply-proposal'),
        async () => waitSaved(target.outputId, oracle.append.plusOne.length));
      const savedState = await readBuilder();
      checkBuilderScope(savedState);
      const saved = documentByOutput(savedState, target.outputId);
      const savedStep = saved.construction?.steps?.at(-1);
      const inputEvidence = currentDraftSourceEvidence({ inputs: savedStep?.inputs,
        expectedOutputIDs: sources.map(source => source.outputId), sourceDocuments: savedState.workspace.documents });
      const exactSaved = savedStep?.id === preview.step.id && savedStep.operation?.kind === 'COMBINE' &&
        savedStep.operation.combine?.kind === 'APPEND' && inputEvidence.ok;
      requireCheck('persistence', 'applied APPEND persists Observation A, Observation B, Patient FHIR ID DERIVE inputs in that order with stable step identity', exactSaved,
        { outputId: target.outputId, stepId: savedStep?.id, candidateStepId: preview.step.id,
          inputOrder: ['Observation final-status GROUP A', 'Observation final-status GROUP B', 'Patient FHIR ID GROUP→DERIVE'], inputEvidence });
      assert(exactSaved);
      await reloadSelectTable(target.outputId, oracle.append.plusOne.length, 'Reload applied APPEND and duplicate row multiset');
      assertRows('applied APPEND row multiset survives reload with source multiplicity', await readGrid('saved'),
        ['Category', 'Row count'], oracle.append.plusOne);
      const receipt = await assertPreviewMatchesBuilder(target.outputId, savedState);
      return { target, step: savedStep, state: savedState, receipt, base };
    };

    const canceledTarget = await createAppendTarget();
    const cancelBase = await readBuilder();
    const canceledPreview = await configureAppend(canceledTarget);
    await click('Cancel APPEND proposal without saving', page.getByTestId('construction-cancel-proposal'),
      async () => waitEmptyTarget(canceledTarget.outputId));
    await reloadSelectTable(canceledTarget.outputId, undefined, 'Reload canceled APPEND and unchanged empty target');
    const afterCancel = await readBuilder();
    checkBuilderScope(afterCancel);
    const canceledEmptyTarget = documentByOutput(afterCancel, canceledTarget.outputId);
    const cancelUnchanged = afterCancel.draftVersion === cancelBase.draftVersion && afterCancel.draftDigest === cancelBase.draftDigest &&
      isDeepStrictEqual(afterCancel.workspace.documents, cancelBase.workspace.documents) &&
      canceledEmptyTarget.columns.length === 0 && (canceledEmptyTarget.construction?.steps?.length ?? 0) === 0;
    requireCheck('persistence', 'Cancel leaves exact APPEND candidate, source workspace, empty target, and draft CAS unchanged after reload', cancelUnchanged,
      { before: { draftVersion: cancelBase.draftVersion, draftDigest: cancelBase.draftDigest },
        after: { draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest },
        candidateStepId: canceledPreview.step.id, targetOutputId: canceledTarget.outputId });
    assert(cancelUnchanged);

    const target = await createAppendTarget();
    const applyPreview = await configureAppend(target);
    const appliedAppend = await applyAppend(target, applyPreview);
    const originalPatientState = await readBuilder();
    const originalPatientDocument = documentByOutput(originalPatientState, patient.outputId);
    const originalDerive = originalPatientDocument.construction?.steps?.find(step => step.operation?.kind === 'DERIVE');
    assert(originalDerive && patient.derive?.step.id === originalDerive.id);
    const originalAppendPreview = appliedAppend.receipt;
    const beforeEditRows = oracle.append.plusOne;
    const afterEditRows = oracle.append.plusTwo;
    const editStep = async (outputId, stepId, family, editorSelector) => {
      await selectTable(outputId, outputId === patient.outputId ? oracle.patientDerived.plusOne.length : oracle.append.plusOne.length,
        `Select saved ${family} source for edit`);
      await click(`Select saved ${family} step`, page.getByTestId(`construction-history-step-${stepId}`));
      const editButton = page.locator(`[data-testid="construction-edit-step-${stepId}"]:not(:disabled)`);
      await waitSelector(`[data-testid="construction-edit-step-${stepId}"]:not(:disabled)`);
      await click(`Open saved ${family} editor`, editButton,
        async () => waitSelector(editorSelector));
    };
    const fillDeriveEdit = async () => {
      await click('Open saved Patient formula editor', page.getByRole('button', { name: 'Formula editor', exact: true }));
      await fill('Set saved Patient DERIVE formula to row_count + 2', page.locator('textarea[aria-label="Formula"]'), 'row_count + 2');
      await fill('Keep Patient DERIVE output name', page.locator('input[aria-label="Output column name"]'), 'row_count_offset');
      await fill('Set saved Patient DERIVE label to Count plus two', page.locator('input[aria-label="Output column label"]'), 'Count plus two',
        async () => waitProposal(patient.outputId, oracle.patientDerived.plusTwo.length));
      assertRows('saved Patient edit proposal computes exact selected GROUP count plus two', await readGrid('proposal'),
        [patient.derive.keyOutput.label, patient.derive.countOutput.label, 'Count plus two'], oracle.patientDerived.plusTwo);
    };
    const preCancelBuilder = await readBuilder();
    const preCancelAppendReceipt = await previewIdentity();
    await editStep(patient.outputId, originalDerive.id, 'Patient DERIVE', '[data-testid="construction-calculate-editor"]');
    await fillDeriveEdit();
    await click('Cancel saved Patient DERIVE +2 edit', page.getByTestId('construction-cancel-proposal'),
      async () => waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&Boolean(document.querySelector('[data-testid="construction-history"]'))`));
    const afterDeriveCancel = await readBuilder();
    const canceledDeriveEdit = afterDeriveCancel.draftVersion === preCancelBuilder.draftVersion &&
      afterDeriveCancel.draftDigest === preCancelBuilder.draftDigest &&
      isDeepStrictEqual(afterDeriveCancel.workspace.documents, preCancelBuilder.workspace.documents);
    requireCheck('persistence', 'Canceling saved DERIVE +2 preserves the complete source and APPEND workspace with the same draft CAS', canceledDeriveEdit,
      { beforeDraftVersion: preCancelBuilder.draftVersion, afterDraftVersion: afterDeriveCancel.draftVersion,
        beforeDraftDigest: preCancelBuilder.draftDigest, afterDraftDigest: afterDeriveCancel.draftDigest,
        priorAppendReceipt: preCancelAppendReceipt });
    assert(canceledDeriveEdit);
    await reloadSelectTable(patient.outputId, oracle.patientDerived.plusOne.length, 'Reload canceled Patient DERIVE edit and preserve +1 output', patient.selection.id);
    assertRows('canceled Patient edit leaves saved Group→DERIVE +1 values after reload', await readGrid('saved'),
      [patient.derive.keyOutput.label, patient.derive.countOutput.label, patient.derive.output.label], oracle.patientDerived.plusOne);

    await editStep(patient.outputId, originalDerive.id, 'Patient DERIVE', '[data-testid="construction-calculate-editor"]');
    const deriveProposalFromIndex = report.nativeRequests.length;
    await fillDeriveEdit();
    const editEvent = await requestCapture.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/construction-proposals` &&
      entry.method === 'POST' && entry.status === 200 && entry.completedAt &&
      requestCapture.rawRequestBody(entry)?.outputId === patient.outputId &&
      requestCapture.rawRequestBody(entry)?.candidateConstruction?.steps?.some(step => step.id === originalDerive.id &&
        step.operation?.kind === 'DERIVE' && step.operation.derive.right?.literal?.integer === 2),
      { fromIndex: deriveProposalFromIndex, timeoutMs: MAX_ACTION_MS });
    const deriveCandidate = requestCapture.rawRequestBody(editEvent)?.candidateConstruction?.steps?.find(step => step.id === originalDerive.id);
    const deriveCandidateOutput = deriveCandidate?.outputs?.find(output => output.id === patient.derive.output.id);
    const deriveCASExact = editEvent.status === 200 && deriveCandidateOutput?.name === 'row_count_offset' &&
      deriveCandidate?.operation?.derive?.left?.columnId === patient.derive.countOutput.id &&
      deriveCandidate?.operation?.derive?.right?.literal?.integer === 2;
    requireCheck('correctness', 'saved Patient DERIVE +2 proposal retains exact stable step, input, output, and integer expression', deriveCASExact,
      { outputId: patient.outputId, event: { path: editEvent.path, status: editEvent.status, requestId: editEvent.requestId },
        candidate: deriveCandidate, derivedOutput: deriveCandidateOutput, captureIndex: deriveProposalFromIndex });
    assert(deriveCASExact);
    await click('Apply saved Patient DERIVE +2 edit', page.getByTestId('construction-apply-proposal'),
      async () => waitSaved(patient.outputId, oracle.patientDerived.plusTwo.length));
    const afterPatientEdit = await readBuilder();
    checkBuilderScope(afterPatientEdit);
    const editedPatientDocument = documentByOutput(afterPatientEdit, patient.outputId);
    const editedDerive = editedPatientDocument.construction?.steps?.find(step => step.operation?.kind === 'DERIVE');
    assert.equal(editedDerive?.id, originalDerive.id);
    assert.equal(editedDerive?.outputs?.find(output => output.name === 'row_count_offset')?.id, patient.derive.output.id);
    assert.equal(editedDerive.operation.derive.right.literal.integer, 2);
    await reloadSelectTable(patient.outputId, oracle.patientDerived.plusTwo.length, 'Reload applied Patient DERIVE +2 source', patient.selection.id);
    assertRows('applied Patient DERIVE +2 source values survive reload', await readGrid('saved'),
      [patient.derive.keyOutput.label, patient.derive.countOutput.label, 'Count plus two'], oracle.patientDerived.plusTwo);
    const editedPatientPreview = await assertPreviewMatchesBuilder(patient.outputId, afterPatientEdit);

    await reloadSelectTable(target.outputId, oracle.append.plusTwo.length,
      'Reload dependent APPEND after current-draft Patient DERIVE edit');
    const recomputedAppendGrid = await readGrid('saved');
    assertRows('dependent APPEND recomputes its row multiset from Patient DERIVE +2 and preserves duplicate status rows', recomputedAppendGrid,
      ['Category', 'Row count'], afterEditRows);
    const afterRecompute = await readBuilder();
    checkBuilderScope(afterRecompute);
    const appendDocument = documentByOutput(afterRecompute, target.outputId);
    const appendStep = appendDocument.construction?.steps?.at(-1);
    const appendInputs = currentDraftSourceEvidence({ inputs: appendStep?.inputs,
      expectedOutputIDs: sources.map(source => source.outputId), sourceDocuments: afterRecompute.workspace.documents });
    const recomputedAppendPreview = await assertPreviewMatchesBuilder(target.outputId, afterRecompute, originalAppendPreview.receipt);
    const recomputed = afterRecompute.draftVersion > appliedAppend.state.draftVersion &&
      afterRecompute.draftDigest !== appliedAppend.state.draftDigest && appendStep?.id === appliedAppend.step.id &&
      appendInputs.ok && recomputedAppendPreview.receipt !== originalAppendPreview.receipt &&
      editedPatientPreview.draftVersion === String(afterPatientEdit.draftVersion);
    requireCheck('persistence', 'dependent APPEND keeps ordered current-draft inputs while recomputing a fresh receipt, version, and digest', recomputed,
      { before: { draftVersion: appliedAppend.state.draftVersion, draftDigest: appliedAppend.state.draftDigest,
        receipt: originalAppendPreview.receipt }, after: { draftVersion: afterRecompute.draftVersion,
        draftDigest: afterRecompute.draftDigest, receipt: recomputedAppendPreview.receipt },
        appendStepId: appendStep?.id, expectedAppendStepId: appliedAppend.step.id,
        inputOrder: ['Observation final-status GROUP A', 'Observation final-status GROUP B', 'Patient FHIR ID GROUP→DERIVE'], appendInputs,
        editedPatientDraftVersion: afterPatientEdit.draftVersion });
    assert(recomputed, 'Dependent APPEND did not rebind to the edited current-draft state');

    const exactRereads = {};
    for (const definition of CDA_UPSTREAM_APPEND_SOURCES) {
      const selected = oracle.sources[definition.sourceKey];
      const rows = runAQL(arangoContainer, cdaUpstreamAppendRereadQuery({
        project, generation, resourceType: definition.resourceType, fieldPath: definition.fieldPath,
        documentIDs: selected.map(row => row._id),
      }), `Final exact selected ${definition.sourceKey} raw reread`);
      exactRereads[definition.sourceKey] = assertCdaUpstreamAppendReread(rows,
        oracle.exactExpected[definition.sourceKey], { project, generation, resourceType: definition.resourceType });
    }
    requireCheck('correctness', 'exact selected raw witnesses reread unchanged after Group, DERIVE edit, and APPEND recompute',
      Object.keys(exactRereads).length === 3, { exactRereads });

    const sourceSnapshots = sources.map(source => structuredClone(documentByOutput(afterRecompute, source.outputId)));
    const fullStepInputs = afterRecompute.workspace.documents.flatMap(document =>
      document.construction?.steps?.flatMap(step => step.inputs ?? []) ?? []);
    const noPinnedTables = fullStepInputs.every(input => input.kind !== 'TABLE_REVISION');
    requireCheck('correctness', 'CDA Group→DERIVE→APPEND uses no pinned table revisions or Publish',
      noPinnedTables && publishRequests === 0, { publishRequests, inputKinds: fullStepInputs.map(input => input.kind) });

    const removeSavedAppend = async ({ remove }) => {
      await selectTable(target.outputId, oracle.append.plusTwo.length, 'Select recomputed APPEND for removal');
      const before = await readBuilder();
      const saved = documentByOutput(before, target.outputId);
      const savedStep = saved.construction.steps.at(-1);
      await click('Select saved APPEND step before removal', page.getByTestId(`construction-history-step-${savedStep.id}`));
      const removeButton = page.locator(`[data-testid="construction-remove-step-${savedStep.id}"]:not(:disabled)`);
      await waitSelector(`[data-testid="construction-remove-step-${savedStep.id}"]:not(:disabled)`);
      await click(remove ? 'Propose APPEND removal' : 'Propose APPEND removal for Cancel', removeButton,
        async () => waitProposal(target.outputId, 0));
      const removalPreview = await page.locator('[data-testid="construction-proposal-preview"][data-preview-status="ready"]')
        .evaluate(element => ({ outputId: element.dataset.previewOutputId, status: element.dataset.previewStatus }));
      const exactEmptyProposal = removalPreview.outputId === target.outputId && removalPreview.status === 'ready';
      requireCheck('correctness', 'APPEND removal proposal previews the exact rooted target before Cancel or Apply', exactEmptyProposal,
        { outputId: target.outputId, removalPreview });
      assert(exactEmptyProposal);
      if (!remove) {
        await click('Cancel APPEND removal', page.getByTestId('construction-cancel-proposal'),
          async () => waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&Boolean(document.querySelector('[data-testid="construction-history"]'))`));
        await reloadSelectTable(target.outputId, oracle.append.plusTwo.length, 'Reload recomputed APPEND after Cancel removal');
        const after = await readBuilder();
        const exact = after.draftVersion === before.draftVersion && after.draftDigest === before.draftDigest &&
          isDeepStrictEqual(after.workspace.documents, before.workspace.documents);
        requireCheck('persistence', 'Cancel APPEND removal preserves complete recomputed output and current-draft source workspace after reload', exact,
          { beforeDraftVersion: before.draftVersion, afterDraftVersion: after.draftVersion,
            beforeDraftDigest: before.draftDigest, afterDraftDigest: after.draftDigest, appendStepId: savedStep.id });
        assert(exact);
        assertRows('canceled APPEND removal preserves recomputed row multiset with source multiplicity', await readGrid('saved'),
          ['Category', 'Row count'], oracle.append.plusTwo);
        return;
      }
      await click('Apply APPEND removal', page.getByTestId('construction-apply-proposal'),
        async () => waitEmptyTarget(target.outputId));
      await reloadSelectTable(target.outputId, undefined, 'Reload restored empty APPEND target');
      const after = await readBuilder();
      checkBuilderScope(after);
      const restored = documentByOutput(after, target.outputId);
      const restoredSources = sources.map(source => documentByOutput(after, source.outputId));
      const sourcesPreserved = isDeepStrictEqual(restoredSources, sourceSnapshots);
      const emptyTarget = restored.rootResourceType === 'Observation' && restored.columns.length === 0 &&
        (restored.construction?.steps?.length ?? 0) === 0;
      requireCheck('persistence', 'applying APPEND removal restores its exact empty target and preserves every full current-draft source',
        emptyTarget && sourcesPreserved,
        { targetOutputId: target.outputId, emptyTarget, restoredTarget: restored,
          sourceOutputIDs: sources.map(source => source.outputId), sourcesPreserved });
      assert(emptyTarget && sourcesPreserved);
      await reloadSelectTable(patient.outputId, oracle.patientDerived.plusTwo.length, 'Reload preserved Patient FHIR ID Group→DERIVE after APPEND removal', patient.selection.id);
      assertRows('Patient Group→DERIVE +2 remains usable after APPEND removal', await readGrid('saved'),
        [patient.derive.keyOutput.label, patient.derive.countOutput.label, 'Count plus two'], oracle.patientDerived.plusTwo);
    };
    await removeSavedAppend({ remove: false });
    await removeSavedAppend({ remove: true });

    const finalBuilder = await readBuilder();
    checkBuilderScope(finalBuilder);
    const allInputs = finalBuilder.workspace.documents.flatMap(document =>
      document.construction?.steps?.flatMap(step => step.inputs ?? []) ?? []);
    const finalIntegrity = publishRequests === 0 && allInputs.every(input => input.kind !== 'TABLE_REVISION') &&
      sources.every(source => finalBuilder.workspace.documents.some(document => document.output.id === source.outputId &&
        document.population?.selectionRevisionId === source.selection.id));
    requireCheck('correctness', 'full lifecycle never publishes and retains the exact immutable source populations', finalIntegrity,
      { publishRequests, tableRevisionInputs: allInputs.filter(input => input.kind === 'TABLE_REVISION'),
        sourceOutputIDs: sources.map(source => source.outputId), draftVersion: finalBuilder.draftVersion });
    assert(finalIntegrity);
    const boundedReread = Object.values(exactRereads).every(item => item.exact);
    const allActionsWithinBudget = report.actions.length > 0 && report.actions.every(item =>
      item.status === 'passed' && item.elapsedMs <= MAX_ACTION_MS);
    requireCheck('performance', ACTION_CHECK, allActionsWithinBudget, {
      actions: report.actions.length,
      maxActionMs: report.actions.reduce((maximum, item) => Math.max(maximum, item.elapsedMs ?? 0), 0),
      exceeded: report.actions.filter(item => item.elapsedMs > MAX_ACTION_MS),
    });
    assert(allActionsWithinBudget, 'A native CDA lifecycle action did not complete within five seconds');
    report.upstreamAppend = {
      project, generation, explorer, initialScope,
      sources: sources.map(source => ({ resourceType: source.definition.resourceType,
        outputId: source.outputId, selectionId: source.selection.id, memberCount: source.selection.memberCount,
        groupStepId: source.group.groupStep.id })),
      patient: { deriveStepId: patient.derive.step.id, outputColumnId: patient.derive.output.id,
        plusOne: oracle.patientDerived.plusOne, plusTwo: oracle.patientDerived.plusTwo },
      append: { outputId: target.outputId, stepId: appliedAppend.step.id,
        plusOne: beforeEditRows, plusTwo: afterEditRows,
        receiptBefore: originalAppendPreview.receipt, receiptAfter: recomputedAppendPreview.receipt,
        draftVersionBefore: appliedAppend.state.draftVersion, draftVersionAfter: afterRecompute.draftVersion,
        draftDigestBefore: appliedAppend.state.draftDigest, draftDigestAfter: afterRecompute.draftDigest },
      exactRereads, noPublish: publishRequests === 0, noTableRevision: noPinnedTables,
      allActionsWithinBudget, boundedReread,
    };
  } finally {
    page.removeListener('request', onRequest);
    await requestCapture?.flush();
  }
}
