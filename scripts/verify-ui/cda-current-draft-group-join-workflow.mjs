import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import { validatedArangoContainer } from './native-cda-workflow-tools.mjs';
import {
  builderDraftStateEvidence,
  canceledDraftEvidence,
  constructionCandidateWireEquivalent,
  currentDraftSourceEvidence,
  groupCounts,
  joinGroupedCounts,
  workspaceOutputOption,
} from './builder-combine-draft-helpers.mjs';
import { nativeCombineTargetBindingEvidence, rootedEmptyTargetRestorationEvidence } from './builder-combine-helpers.mjs';
import { proposalPreviewReadinessExpression } from './proposal-preview-readiness.mjs';

const ACTION_CHECK = 'all native lifecycle actions complete within five seconds';
const TABLE_SELECTOR = '[data-testid="preview-table-scroll"] [role="table"]';
const PROPOSAL_SELECTOR = '[data-testid="construction-proposal-preview"][data-preview-status="ready"]';
const OPERATION_EDITOR = '[data-testid="construction-operation-editor"]';
const MAX_RAW_SCAN = 2_000;
const MAX_MEMBERS = 8;
const GROUP_KEY_FIELD_PATH = 'subject.reference';
const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();

const encoded = value => encodeURIComponent(value);
const apiRoot = (project, explorer) => `/api/v1/projects/${encoded(project)}/explorers/${encoded(explorer)}`;
const authoringRoot = (project, explorer) => `${apiRoot(project, explorer)}/authoring/v2`;
const sourceRef = ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`;
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const querySelector = value => JSON.stringify(value);

function rawObservationScan({ arangoContainer, project, generation }) {
  const query = `FOR r IN Observation FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)} AND r.payload.resourceType == "Observation" AND IS_STRING(r.payload.subject.reference) AND LENGTH(TRIM(r.payload.subject.reference)) > 0 SORT r._id LIMIT ${MAX_RAW_SCAN} RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,groupKey:r.payload.subject.reference}`;
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(result.status, 0, `Owned Arango Observation scan failed: ${tidy(result.stderr || result.stdout).slice(0, 1800)}`);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Owned Arango Observation scan returned no JSON array: ${tidy(result.stdout).slice(0, 600)}`);
  const rows = JSON.parse(result.stdout.slice(jsonStart));
  assert(Array.isArray(rows) && rows.length <= MAX_RAW_SCAN, 'Arango raw witness exceeded its returned-row bound');
  assert(rows.every(row => row.project === project && row.generation === generation && row.resourceType === 'Observation' &&
    typeof row._id === 'string' && typeof row.id === 'string' && typeof row.groupKey === 'string' && row.groupKey.trim()),
  `Bounded Arango result contains an out-of-scope or non-string Observation ${GROUP_KEY_FIELD_PATH}`);
  assert.equal(new Set(rows.map(row => row._id)).size, rows.length, 'Bounded Arango scan repeated a document key');
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length, 'Bounded Arango scan repeated a FHIR Observation ID');
  return { query, rows };
}

export function chooseOverlappingGroupKeyMemberships(rows) {
  const byGroupKey = new Map();
  for (const row of rows) {
    const members = byGroupKey.get(row.groupKey) ?? [];
    members.push(row);
    byGroupKey.set(row.groupKey, members);
  }
  const sharedGroupKey = [...byGroupKey.keys()].sort().find(groupKey => (byGroupKey.get(groupKey)?.length ?? 0) >= 3);
  if (!sharedGroupKey) throw new Error(`The first ${MAX_RAW_SCAN} scoped Observations contain no nonempty string ${GROUP_KEY_FIELD_PATH} with three distinct records`);
  const leftOnlyGroupKey = [...byGroupKey.keys()].sort().find(groupKey => groupKey !== sharedGroupKey);
  if (!leftOnlyGroupKey) throw new Error(`The bounded CDA witness has no second ${GROUP_KEY_FIELD_PATH} value for a left-only Group key`);
  const left = [byGroupKey.get(sharedGroupKey)[0], byGroupKey.get(sharedGroupKey)[1], byGroupKey.get(leftOnlyGroupKey)[0]];
  const right = [byGroupKey.get(sharedGroupKey)[2]];
  assert(left.length <= MAX_MEMBERS && right.length <= MAX_MEMBERS);
  assert.equal(left.filter(row => row.groupKey === sharedGroupKey).length, 2);
  assert.equal(right.filter(row => row.groupKey === sharedGroupKey).length, 1);
  assert(left.some(row => row.groupKey === leftOnlyGroupKey) && right.every(row => row.groupKey !== leftOnlyGroupKey));
  assert.notDeepEqual(left.map(row => row.id).sort(), right.map(row => row.id).sort());
  assert.notEqual(left[0].id, right[0].id, `Join witness must use distinct Observation records with shared ${GROUP_KEY_FIELD_PATH}`);
  return { left, right, sharedGroupKey, leftOnlyGroupKey, groupKeyFieldPath: GROUP_KEY_FIELD_PATH };
}

export function prepareCdaGroupJoinOracle(rows) {
  const memberships = chooseOverlappingGroupKeyMemberships(rows);
  const expectedLeftInitial = groupCounts(memberships.left, 'groupKey');
  const expectedRight = groupCounts(memberships.right, 'groupKey');
  const expectedLeftDistinct = expectedLeftInitial.map(([groupKey]) => [groupKey, 1]);
  return {
    memberships,
    groupKeyFieldPath: GROUP_KEY_FIELD_PATH,
    expectedLeftInitial,
    expectedRight,
    expectedLeftDistinct,
    expectedLeft: joinGroupedCounts(expectedLeftInitial, expectedRight, 'LEFT'),
    expectedInner: joinGroupedCounts(expectedLeftInitial, expectedRight, 'INNER'),
    expectedDistinctInner: joinGroupedCounts(expectedLeftDistinct, expectedRight, 'INNER'),
  };
}

export async function cdaCurrentDraftGroupJoinWorkflow({ page, cda }) {
  const { target, request, report } = cda;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target);
  assert(project && generation && apiOrigin && uiOrigin, 'Owned CDA fixture must provide project, generation, API, and UI origins');
  assert.equal(project, target.fixtureProject, 'Raw CDA selection must use the exact owned fixture project');
  assert.equal(generation, 'cda-fhir-v1', 'Current-draft CDA Group/Join requires the pinned FHIR generation');

  const api = async (path, body) => {
    const url = `${apiOrigin}${path}`;
    const headers = { 'X-Request-ID': `cda-group-join-${randomUUID()}` };
    const response = body === undefined
      ? await request.get(url, { headers, timeout: 30_000 })
      : await request.post(url, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${tidy(text).slice(0, 1200)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1800)}`);
    return value;
  };
  const apiURL = path => `${apiOrigin}${path}`;
  let explorer;
  let explorerBase;
  let requestCapture;
  let builder;
  let initialScope;
  let oracle;
  let sources = [];
  let publishRequests = 0;
  const publishPathSuffix = '/authoring/v2/publish';
  const applicationOrigins = new Set([apiOrigin, uiOrigin].map(value => new URL(value).origin));
  const selectionReadEvents = new Map();
  let activeSelectionTransition;
  const onRequest = browserRequest => {
    try {
      const url = new URL(browserRequest.url());
      if (applicationOrigins.has(url.origin) && url.pathname.endsWith(publishPathSuffix)) publishRequests += 1;
      const selectionPrefix = explorerBase ? `${explorerBase}/selections/` : '';
      if (browserRequest.method() === 'GET' && selectionPrefix && url.origin === new URL(uiOrigin).origin &&
        url.pathname.startsWith(selectionPrefix)) {
        const encodedSelectionId = url.pathname.slice(selectionPrefix.length);
        if (encodedSelectionId && !encodedSelectionId.includes('/')) {
          selectionReadEvents.set(browserRequest, {
            selectionId: decodeURIComponent(encodedSelectionId), path: url.pathname,
            startedAt: Date.now(), responseStatus: null, responseAt: null, completedAt: null,
            failedAt: null, failure: null, transitionAction: activeSelectionTransition?.label ?? null,
          });
        }
      }
    } catch { /* Non-URL browser requests cannot be Publish calls. */ }
  };
  const onSelectionResponse = response => {
    const event = selectionReadEvents.get(response.request());
    if (!event) return;
    event.responseStatus = response.status();
    event.responseAt = Date.now();
  };
  const onSelectionRequestFinished = browserRequest => {
    const event = selectionReadEvents.get(browserRequest);
    if (event) event.completedAt = Date.now();
  };
  const onSelectionRequestFailed = browserRequest => {
    const event = selectionReadEvents.get(browserRequest);
    if (!event) return;
    event.failedAt = Date.now();
    event.failure = browserRequest.failure()?.errorText ?? null;
    event.transitionAction = activeSelectionTransition?.label ?? null;
  };
  page.on('request', onRequest);
  page.on('response', onSelectionResponse);
  page.on('requestfinished', onSelectionRequestFinished);
  page.on('requestfailed', onSelectionRequestFailed);

  const requireCheck = (dimension, name, passed, evidence = {}) => cda.check(dimension, name, passed, evidence);
  const waitFunction = (predicate, timeout = 5_000) => page.waitForFunction(predicate, undefined, { timeout: Math.min(5_000, timeout) });
  const waitSelector = (selector, timeout = 5_000) => page.locator(selector).waitFor({ state: 'visible', timeout: Math.min(5_000, timeout) });
  const action = (label, locator, perform, after) => cda.action(label, locator, perform, {
    timeout: 5_000,
    budget: 5_000,
    ...(after ? { after } : {}),
    ...(after ? { requiredCheck: ACTION_CHECK } : {}),
  });
  const readBuilder = () => api(`${explorerBase}/authoring/v2/builder`);
  const getDocument = (state, outputId) => {
    const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
    assert.equal(matches.length, 1, `Expected exactly one Builder document for ${outputId}`);
    return matches[0];
  };
  const command = async (commands, state = builder) => {
    assert(state?.catalog?.snapshotToken, 'Builder command needs the exact current catalog snapshot');
    const commandId = randomUUID();
    await api(`${explorerBase}/authoring/v2/commands`, {
      commandId,
      semanticsVersion: state.workspace?.semanticsVersion ?? 10,
      snapshotToken: state.catalog.snapshotToken,
      expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest,
      commands,
    });
    builder = await readBuilder();
    return builder;
  };
  const checkBuilderScope = state => {
    assert.equal(state.catalog?.generation, generation, 'Builder reloaded outside the exact CDA generation');
    assert.equal(state.catalog?.authorizationScopeDigest, initialScope.authorizationScopeDigest,
      'Builder authorization scope digest changed during the current-draft lifecycle');
    assert.equal(state.catalog?.snapshotToken, initialScope.snapshotToken, 'Builder snapshot token changed during the current-draft lifecycle');
  };
  const readGrid = async kind => page.evaluate(({ kind }) => {
    const proposal = kind === 'proposal' ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]') : null;
    if (kind === 'proposal' && !proposal) return { ready: false, headers: [], rows: [], ariaRowCount: null };
    const table = kind === 'proposal' ? proposal.querySelector('table') : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { ready: false, headers: [], rows: [], ariaRowCount: null };
    const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = proposal
      ? [...table.querySelectorAll('thead th')].map(cell => normalize(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => normalize(cell.textContent));
    const rows = proposal
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => normalize(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => normalize(cell.innerText)));
    return { ready: true, headers, rows, ariaRowCount: table.getAttribute('aria-rowcount') };
  }, { kind });
  const assertGrid = (name, grid, headers, rows) => {
    const matches = grid.ready && isDeepStrictEqual(grid.headers, headers) && isDeepStrictEqual(sortRows(grid.rows), sortRows(rows));
    requireCheck('correctness', name, matches, { headers: grid.headers, expectedHeaders: headers, rows: grid.rows, expectedRows: rows });
    assert(matches, `${name}: rendered output differs from the independent raw oracle`);
    return grid;
  };
  const savedReady = (outputId, rowCount) => `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector(${querySelector(TABLE_SELECTOR)});return Boolean(p?.dataset.previewStatus==='ready'&&p.dataset.previewOutputId===${querySelector(outputId)}&&t&&t.getAttribute('aria-rowcount')===${querySelector(String(rowCount + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
  const selectedReady = outputId => `Boolean(document.querySelector('[data-testid="construction-table-${outputId}"]')?.getAttribute('aria-current')==='page')`;
  const proposalReady = (outputId, rows) => proposalPreviewReadinessExpression(outputId, rows);
  const waitProposal = (outputId, rows) => waitFunction(proposalReady(outputId, rows));
  const waitSaved = (outputId, rows) => waitFunction(savedReady(outputId, rows));
  const tableLocator = outputId => page.getByTestId(`construction-table-${outputId}`);
  const reloadSelectTable = async (outputId, rows, label) => {
    const locator = tableLocator(outputId);
    await action(label, locator, async targetLocator => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: 5_000 });
      await waitSelector(`[data-testid="construction-table-${outputId}"]`);
      await targetLocator.click({ timeout: 5_000 });
    }, async () => {
      if (rows === undefined) await waitFunction(`${selectedReady(outputId)}&&!document.querySelector('[data-testid="construction-proposal-panel"]')`);
      else await waitSaved(outputId, rows);
    });
  };
  const selectTable = async (outputId, rows, label, afterSelection) => {
    const locator = tableLocator(outputId);
    if (await locator.getAttribute('aria-current') === 'page') {
      if (rows === undefined) return;
      await waitSaved(outputId, rows);
      if (afterSelection) await afterSelection();
      return;
    }
    await action(label, locator, targetLocator => targetLocator.click({ timeout: 5_000 }), async () => {
      if (rows === undefined) await waitFunction(selectedReady(outputId));
      else await waitSaved(outputId, rows);
      if (afterSelection) await afterSelection();
    });
  };
  const navigateBuilder = async outputId => {
    await cda.navigate(`${uiOrigin}/?project=${encoded(project)}&explorer=${encoded(explorer)}&mode=builder`);
    await waitSelector(`[data-testid="construction-table-${outputId}"]`, 5_000);
  };
  const selectOptionByText = async (selector, expectedText) => {
    const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({
      value: option.value, text: String(option.textContent ?? '').replace(/\s+/g, ' ').trim(), disabled: option.disabled,
    })));
    const matches = options.filter(option => !option.disabled && option.text.toLowerCase() === expectedText.toLowerCase());
    assert.equal(matches.length, 1, `Expected one enabled ${JSON.stringify(expectedText)} option in ${selector}: ${JSON.stringify(options)}`);
    const locator = page.locator(selector);
    await action(`Choose ${expectedText}`, locator, targetLocator => targetLocator.selectOption(matches[0].value, { timeout: 5_000 }));
    return matches[0].value;
  };
  const openGroupSource = async ({ title, rawRows, sourceLabel, previousSource }) => {
    builder = await readBuilder();
    checkBuilderScope(builder);
    const nodeMatches = builder.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible);
    assert.equal(nodeMatches.length, 1, 'Scoped CDA catalog must expose exactly one eligible Observation row root');
    const rootNode = nodeMatches[0];
    const idCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
    const keyCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === GROUP_KEY_FIELD_PATH);
    assert.equal(idCandidates.length, 1, 'Scoped Observation catalog must expose the exact id candidate');
    assert.equal(keyCandidates.length, 1, `Scoped Observation catalog must expose exactly one ${GROUP_KEY_FIELD_PATH} candidate`);
    assert.equal(keyCandidates[0].logicalType, 'string', `Observation.${GROUP_KEY_FIELD_PATH} must be a scalar string`);
    assert.equal(keyCandidates[0].repeated, false, `Observation.${GROUP_KEY_FIELD_PATH} candidate must not be repeated`);
    const priorOutputIds = new Set((builder.workspace?.documents ?? []).map(document => document.output.id));
    builder = await command([{ type: 'CREATE_TABLE', title, rootNodeId: rootNode.nodeId }], builder);
    const created = (builder.workspace?.documents ?? []).filter(document => !priorOutputIds.has(document.output.id));
    assert.equal(created.length, 1, 'CREATE_TABLE must create one separate Observation source output');
    const document = created[0];
    const outputId = document.output.id;
    const idCandidate = idCandidates[0];
    builder = await command([{
      type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID',
    }], builder);
    const refs = rawRows.map(row => ({ project, generation, resourceType: 'Observation', id: row.id }));
    const selection = await api(`${apiRoot(project, explorer)}/selections`, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: `cda-group-join-${randomUUID()}`,
      source: { kind: 'resources', resources: { refs } },
    });
    assert.equal(selection.project, project);
    assert.equal(selection.generation, generation);
    assert.equal(selection.resourceType, 'Observation');
    assert.equal(selection.scopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(selection.memberCount, refs.length);
    const selectionPage = await api(`${apiRoot(project, explorer)}/selections/${encoded(selection.id)}?limit=100`);
    const members = selectionPage.members ?? [];
    assert.equal(selectionPage.revision?.id, selection.id);
    assert.equal(selectionPage.revision?.project, project);
    assert.equal(selectionPage.revision?.generation, generation);
    assert.equal(selectionPage.revision?.resourceType, 'Observation');
    assert.equal(selectionPage.revision?.scopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(selectionPage.revision?.memberCount, refs.length);
    assert.equal(members.length, refs.length, 'Selection page must return every bounded immutable source member');
    const actualRefs = members.map(member => sourceRef(member.ref)).sort();
    const expectedRefs = refs.map(sourceRef).sort();
    assert.deepEqual(actualRefs, expectedRefs, 'Immutable selection readback must exactly match the raw CDA witness');
    const memberKeys = members.map(member => member.memberKey);
    assert(memberKeys.every(key => typeof key === 'string' && key) && new Set(memberKeys).size === refs.length,
      'Immutable selection must assign unique opaque member keys');
    const routes = await api(`${explorerBase}/authoring/v2/population-routes`, {
      snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
    });
    const direct = routes.choices.find(choice => choice.route.length === 0);
    assert(direct, 'Exact Observation selection has no direct root population route');
    builder = await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }], builder);
    let saved = getDocument(builder, outputId);
    assert.equal(saved.rootResourceType, 'Observation');
    assert.equal(saved.population?.selectionRevisionId, selection.id);
    assert.equal(saved.population?.route?.length ?? 0, 0);
    assert.equal(saved.columns.length, 1, 'Source setup must retain only the exact baseline Observation identity column');
    assert.equal(saved.columns[0].source?.field?.path, 'id');
    assert.equal(saved.columns[0].source?.field?.projectionMode, 'VALUE');
    requireCheck('correctness', 'immutable Observation selections and root populations match exact raw IDs, project, generation, and scope',
      saved.population?.selectionRevisionId === selection.id && isDeepStrictEqual(actualRefs, expectedRefs), {
        sourceLabel, outputId, selectionRevisionId: selection.id, memberCount: members.length,
        resourceType: selection.resourceType, generation: selection.generation,
        scopeBound: selection.scopeDigest === initialScope.authorizationScopeDigest,
        exactRefs: isDeepStrictEqual(actualRefs, expectedRefs),
      });

    await navigateBuilder(outputId);
    let selectionSwitchEvidence;
    const openTableAction = `Open ${sourceLabel} exact population table`;
    if (previousSource) {
      const rightSelectionPath = `${apiRoot(project, explorer)}/selections/${encoded(selection.id)}`;
      const previousSelectionPath = `${apiRoot(project, explorer)}/selections/${encoded(previousSource.selection.id)}`;
      assert.notEqual(previousSource.selection.id, selection.id, 'Independent Groups must retain distinct source selections');
      const responseOutcome = page.waitForResponse(response => {
        try {
          const url = new URL(response.url());
          return url.origin === new URL(uiOrigin).origin && response.request().method() === 'GET' &&
            url.pathname === rightSelectionPath;
        } catch { return false; }
      }, { timeout: 5_000 }).then(response => ({ response }), error => ({ error }));
      const cancellationStart = report.expectedCancellations?.length ?? 0;
      const selectedOutputBeforeSwitch = await page.evaluate(() => {
        const selected = document.querySelector('[data-testid^="construction-table-"][aria-current="page"]');
        return selected?.getAttribute('data-testid')?.slice('construction-table-'.length) ?? null;
      });
      assert.equal(selectedOutputBeforeSwitch, previousSource.outputId,
        'The exact left source table must be selected before opening the right source table');
      const transitionStartedAt = Date.now();
      const pendingPreviousReads = [...selectionReadEvents.values()].filter(event =>
        event.selectionId === previousSource.selection.id && event.path === previousSelectionPath &&
        event.startedAt <= transitionStartedAt && event.completedAt === null && event.failedAt === null);
      activeSelectionTransition = { label: openTableAction, startedAt: transitionStartedAt };
      let rightSelectionIdentity;
      let rightRead;
      const resolveRightSelection = async () => {
        const responseResult = await responseOutcome;
        if (responseResult.error) throw new Error(`Right attached-selection read did not resolve during ${openTableAction}: ${tidy(responseResult.error.message).slice(0, 800)}`);
        const rightSelectionResponse = responseResult.response;
        assert.equal(rightSelectionResponse.status(), 200, 'Right attached-selection read must succeed');
        const rightSelectionPage = await rightSelectionResponse.json();
        const rightRevision = rightSelectionPage?.revision;
        rightSelectionIdentity = {
          id: rightRevision?.id,
          project: rightRevision?.project,
          generation: rightRevision?.generation,
          resourceType: rightRevision?.resourceType,
          scopeDigest: rightRevision?.scopeDigest,
          membershipDigest: rightRevision?.membershipDigest,
          memberCount: rightRevision?.memberCount,
          complete: rightRevision?.complete,
        };
        assert.deepEqual(rightSelectionIdentity, {
          id: selection.id, project, generation, resourceType: 'Observation',
          scopeDigest: initialScope.authorizationScopeDigest,
          membershipDigest: selection.membershipDigest, memberCount: selection.memberCount, complete: true,
        }, 'The replacement selection response must resolve the exact right project/generation/scope/membership');
        rightRead = selectionReadEvents.get(rightSelectionResponse.request());
        assert(rightRead, 'The successful right selection response must have a matching native request-start event');
        assert.deepEqual([rightRead.selectionId, rightRead.path, rightRead.responseStatus],
          [selection.id, rightSelectionPath, 200]);
        assert(rightRead.startedAt >= transitionStartedAt && rightRead.responseAt >= transitionStartedAt,
          'The exact right selection must be fetched successfully after the native table switch begins');
      };
      let transitionFinishedAt;
      try {
        await cda.withExpectedCancellations({
          origin: uiOrigin,
          method: 'GET',
          paths: [previousSelectionPath],
          requestIdPrefixes: ['cda-request-'],
          reason: 'Selecting the right source output retires the previous left attached-selection query.',
          proof: {
            project, explorer, generation,
            previousOutputId: previousSource.outputId,
            previousSelectionRevisionId: previousSource.selection.id,
            nextOutputId: outputId,
            nextSelectionRevisionId: selection.id,
            retirement: 'BuilderWorkspace replaces the keyed attached-selection query when the selected output changes; its useQuery AbortSignal aborts the stale getSelection request.',
          },
          actionLabel: openTableAction,
        }, async () => selectTable(outputId, rawRows.length, openTableAction, resolveRightSelection));
      } finally {
        transitionFinishedAt = Date.now();
        activeSelectionTransition = undefined;
      }
      const cancellations = (report.expectedCancellations ?? []).slice(cancellationStart);
      assert(cancellations.length <= 1, 'Switching source tables may retire at most one pending left selection read');
      const previousFailures = [...selectionReadEvents.values()].filter(event =>
        event.selectionId === previousSource.selection.id && event.path === previousSelectionPath &&
        event.failure === 'net::ERR_ABORTED' && event.failedAt >= transitionStartedAt && event.failedAt <= transitionFinishedAt);
      let retiredPreviousRead;
      if (cancellations.length === 1) {
        const [cancellation] = cancellations;
        assert.equal(pendingPreviousReads.length, 1, 'A retired left selection GET must already be outstanding before the native switch');
        assert.equal(previousFailures.length, 1, 'Only one prior left selection GET may be aborted during the switch');
        retiredPreviousRead = previousFailures[0];
        assert.strictEqual(retiredPreviousRead, pendingPreviousReads[0], 'The aborted request must be the exact pending left selection GET captured before the switch');
        assert(retiredPreviousRead.startedAt < transitionStartedAt && retiredPreviousRead.failedAt >= transitionStartedAt);
        assert.equal(retiredPreviousRead.transitionAction, openTableAction);
        assert.deepEqual([cancellation.method, new URL(cancellation.url).origin, new URL(cancellation.url).pathname,
          cancellation.reason, cancellation.proof?.scopeAction], [
          'GET', new URL(uiOrigin).origin, previousSelectionPath,
          'Selecting the right source output retires the previous left attached-selection query.', openTableAction,
        ]);
        const diagnostics = report.network.filter(entry => entry.kind === 'network' &&
          entry.playwrightRequestId === cancellation.playwrightRequestId);
        assert.equal(diagnostics.length, 1, 'The exact retired request must remain in the native network ledger');
        assert.deepEqual([diagnostics[0].url, diagnostics[0].errorText, diagnostics[0].triggerAction,
          diagnostics[0].expected, diagnostics[0].cancellationAction], [
          `${new URL(uiOrigin).origin}${previousSelectionPath}`, 'net::ERR_ABORTED', openTableAction, true, openTableAction,
        ]);
        const requestErrors = report.errors.filter(entry => entry.kind === 'network' &&
          entry.playwrightRequestId === cancellation.playwrightRequestId);
        assert(requestErrors.every(entry => entry.expected === true),
          'The exact retired selection GET must have no unclassified native error diagnostic');
      } else {
        assert.equal(previousFailures.length, 0, 'An unclassified left selection abort cannot be hidden by the transition proof');
      }
      selectionSwitchEvidence = {
        action: openTableAction,
        transition: { startedAt: transitionStartedAt, finishedAt: transitionFinishedAt, elapsedMs: transitionFinishedAt - transitionStartedAt },
        previous: { outputId: previousSource.outputId, selectionRevisionId: previousSource.selection.id },
        selectedOutputBeforeSwitch,
        next: { outputId, selectionRevisionId: selection.id },
        pendingPreviousReadsAtTransition: pendingPreviousReads.map(event => ({ startedAt: event.startedAt, path: event.path })),
        retiredPreviousRead: retiredPreviousRead ? {
          path: retiredPreviousRead.path, startedAt: retiredPreviousRead.startedAt,
          failedAt: retiredPreviousRead.failedAt, failure: retiredPreviousRead.failure,
          transitionAction: retiredPreviousRead.transitionAction,
          cancellationRequestId: cancellations[0].requestId,
          playwrightRequestId: cancellations[0].playwrightRequestId,
        } : null,
        rightSelectionRead: {
          path: rightRead.path, startedAt: rightRead.startedAt,
          responseAt: rightRead.responseAt, completedAt: rightRead.completedAt,
          status: rightRead.responseStatus, identity: rightSelectionIdentity,
        },
      };
    } else {
      await selectTable(outputId, rawRows.length, openTableAction);
    }
    let baseGrid = await readGrid('saved');
    const idIndex = baseGrid.headers.findIndex(header => /\bid\b/i.test(header));
    assert(idIndex >= 0, 'Rooted Observation preview omitted its identity column');
    const actualIDs = baseGrid.rows.map(row => row[idIndex]).sort();
    assert.deepEqual(actualIDs, rawRows.map(row => row.id).sort(), 'Rooted population preview must equal the exact selected Observation IDs');
    if (selectionSwitchEvidence) {
      selectionSwitchEvidence.ui = { selectedOutputId: outputId, visibleObservationIDs: actualIDs };
      report.selectionSwitchEvidence = selectionSwitchEvidence;
    }
    const addColumns = page.getByTestId('construction-action-add-columns');
    await action(`Open ${sourceLabel} native Add columns`, addColumns,
      locator => locator.click({ timeout: 5_000 }), async () => waitSelector('[aria-label="Add columns editor"]'));
    await cda.click('button', { name: 'Fields and related data' });
    await cda.click('summary', { name: 'Raw FHIR fields (advanced)' });
    const keySelector = `input[type="checkbox"][aria-label=${querySelector(`Select Observation.${GROUP_KEY_FIELD_PATH}`)}]`;
    const keyControl = page.locator(keySelector);
    await waitSelector(keySelector);
    await action(`Select exact ${sourceLabel} Observation.${GROUP_KEY_FIELD_PATH} field`, keyControl,
      locator => locator.check({ timeout: 5_000 }), async () => {
        await waitFunction(`Boolean(document.querySelector('button[aria-label="Add 1 selected feature"]'))||[...document.querySelectorAll('button')].some(button=>button.textContent.trim()==='Add 1 selected feature'&&!button.disabled)`);
      });
    const addSelected = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    await action(`Add ${sourceLabel} Observation.${GROUP_KEY_FIELD_PATH} column`, addSelected,
      locator => locator.click({ timeout: 5_000 }), async () => waitFunction(`[...document.querySelectorAll('button')].some(button=>button.textContent.trim()==='Apply columns'&&!button.disabled)`));
    const applyColumns = page.getByRole('button', { name: 'Apply columns', exact: true });
    await action(`Apply ${sourceLabel} Observation.${GROUP_KEY_FIELD_PATH} column`, applyColumns,
      locator => locator.click({ timeout: 5_000 }), async () => waitSaved(outputId, rawRows.length));
    await cda.click('button', { name: 'Close operation editor' });
    builder = await readBuilder();
    saved = getDocument(builder, outputId);
    const groupKeySourceColumn = saved.columns.find(column => column.source?.field?.path === GROUP_KEY_FIELD_PATH);
    const idColumn = saved.columns.find(column => column.source?.field?.path === 'id');
    assert(groupKeySourceColumn && idColumn, `Native Add columns must preserve direct Observation id and ${GROUP_KEY_FIELD_PATH} bindings`);
    const sourceColumnLabel = {
      actual: groupKeySourceColumn.label,
      expected: 'Subject Reference',
      catalogCandidateLabel: keyCandidates[0].label,
    };
    assert.equal(sourceColumnLabel.actual, sourceColumnLabel.expected,
      `Added source column must retain its exact user-facing label: ${JSON.stringify(sourceColumnLabel)}`);
    assert.equal(groupKeySourceColumn.source?.field?.projectionMode, 'VALUE');
    assert.equal(groupKeySourceColumn.logicalType, 'string');
    assert.equal(groupKeySourceColumn.occurrenceId, 'base');
    baseGrid = await readGrid('saved');
    const keyIndex = baseGrid.headers.findIndex(header => header === groupKeySourceColumn.label);
    const idValueIndex = baseGrid.headers.findIndex(header => /\bid\b/i.test(header));
    assert(keyIndex >= 0 && idValueIndex >= 0, `Native source table must render the selected Observation ${GROUP_KEY_FIELD_PATH} and ID`);
    const actualPairs = baseGrid.rows.map(row => [row[idValueIndex], row[keyIndex]]).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
    const expectedPairs = rawRows.map(row => [row.id, row.groupKey]).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
    assert.deepEqual(actualPairs, expectedPairs, `Native source table must preserve exact raw Observation IDs and ${GROUP_KEY_FIELD_PATH} values`);

    await cda.click('[data-testid="construction-rows-settings-trigger"]');
    await waitFunction("document.querySelector('[data-testid=construction-action-group-rows]')?.disabled===false");
    if (selectionSwitchEvidence) {
      const startingCollection = page.getByRole('region', { name: 'Starting collection', exact: true });
      await startingCollection.waitFor({ state: 'visible', timeout: 5_000 });
      const attachedSelectionRevisionId = await startingCollection.getAttribute('data-attached-selection-revision-id');
      assert.equal(attachedSelectionRevisionId, selection.id,
        'The right source table UI must resolve its own exact attached selection after the table switch');
      selectionSwitchEvidence.ui.attachedSelectionRevisionId = attachedSelectionRevisionId;
      report.selectionSwitchEvidence = selectionSwitchEvidence;
    }
    const groupAction = page.getByTestId('construction-action-group-rows');
    await action(`Open ${sourceLabel} native GROUP editor`, groupAction,
      locator => locator.click({ timeout: 5_000 }), async () => waitSelector('select[aria-label="Summary 1"]'));
    const groupLabels = await page.locator('input[type="checkbox"][aria-label^="Group by "]').evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label').slice('Group by '.length)));
    const keyLabels = groupLabels.filter(label => label === groupKeySourceColumn.label);
    assert.equal(keyLabels.length, 1, `Native GROUP needs one exact Observation ${GROUP_KEY_FIELD_PATH} key: ${JSON.stringify(groupLabels)}`);
    const groupKeyLabel = keyLabels[0];
    const summary = page.locator('select[aria-label="Summary 1"]');
    assert.equal(await summary.inputValue(), 'COUNT_ROWS', 'New native Group must start with COUNT_ROWS');
    const groupKey = page.locator(`input[type="checkbox"][aria-label=${querySelector(`Group by ${groupKeyLabel}`)}]`);
    const expectedGroups = groupCounts(rawRows, 'groupKey');
    const groupKeyLabelLower = groupKeyLabel.toLowerCase();
    const groupedPreviewReady = `(()=>{const ready=${proposalReady(outputId, expectedGroups.length)};const control=document.querySelector(${querySelector(`input[type="checkbox"][aria-label="Group by ${groupKeyLabel}"]`)});const headers=[...document.querySelectorAll(${querySelector(`${PROPOSAL_SELECTOR} thead th`)})].map(cell=>String(cell.querySelector('span')?.textContent??cell.textContent??'').trim().toLowerCase());return Boolean(ready&&control?.checked&&headers.some(header=>header===${querySelector(groupKeyLabelLower)}))})()`;
    await action(`Group ${sourceLabel} by ${GROUP_KEY_FIELD_PATH} with COUNT_ROWS`, groupKey,
      locator => locator.check({ timeout: 5_000 }), async () => waitFunction(groupedPreviewReady));
    const groupGrid = await readGrid('proposal');
    const groupKeyIndex = groupGrid.headers.findIndex(header => header === groupKeyLabel);
    const countIndex = groupGrid.headers.findIndex(header => /row count/i.test(header));
    assert(groupKeyIndex >= 0 && countIndex >= 0, `COUNT_ROWS GROUP proposal must show ${GROUP_KEY_FIELD_PATH} and row count columns`);
    const groupRows = groupGrid.rows.map(row => [row[groupKeyIndex], Number(row[countIndex])]).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
    const expectedGroupRows = expectedGroups.map(([groupKey, count]) => [groupKey, count]).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
    assert.deepEqual(groupRows, expectedGroupRows, `Native GROUP preview must equal exact selected raw ${GROUP_KEY_FIELD_PATH} counts`);
    const applyGroup = page.getByTestId('construction-apply-proposal');
    const applyGroupAction = `Apply ${sourceLabel} current-draft COUNT_ROWS GROUP`;
    const frameSourcePath = `${explorerBase}/authoring/v2/frame-source-options`;
    const snapshotToken = builder.catalog.snapshotToken;
    const groupEditor = page.getByTestId('construction-reshape-editor');
    assert(await groupEditor.isVisible(), 'The exact Group editor must be visible before Apply');
    assert(await groupKey.isChecked(), 'The Group editor must retain the exact selected key before Apply');
    assert.equal(await page.locator('select[aria-label="Summary 1"]').inputValue(), 'COUNT_ROWS');
    const activeOperationEditor = page.getByTestId('construction-operation-editor');
    assert.equal(await activeOperationEditor.getAttribute('data-operation-family'), 'RESHAPE');
    assert.equal(await activeOperationEditor.getAttribute('data-output-id'), outputId,
      'The active Group editor must belong to the exact selected source output');
    const pendingOptions = report.nativeRequests.map(entry => ({ entry, body: requestCapture.rawRequestBody(entry) }))
      .filter(({ entry, body }) => entry.origin === new URL(uiOrigin).origin && entry.path === frameSourcePath &&
        entry.method === 'POST' && !entry.completedAt && body?.snapshotToken === snapshotToken &&
        body?.outputId === outputId && body?.resourceType === 'Observation' && body?.limit === 50);
    assert(pendingOptions.length <= 1, 'At most one exact Group-options request may be retired by Apply');
    const pendingOption = pendingOptions[0];
    const cancellationsBeforeApply = report.expectedCancellations?.length ?? 0;
    const applyStartedAt = Date.now();
    const applyGroupProposal = () => action(applyGroupAction, applyGroup,
      locator => locator.click({ timeout: 5_000 }), async () => {
        await waitSaved(outputId, expectedGroups.length);
        await waitFunction(`!document.querySelector('[data-testid="construction-reshape-editor"]')`);
        if (pendingOption) {
          const completedOption = await cda.waitForCapturedResponse(requestCapture,
            entry => entry.browserRequestId === pendingOption.entry.browserRequestId, 5_000);
          assert.strictEqual(completedOption, pendingOption.entry,
            'The exact pending Group-options request must finish or be classified as canceled during Apply');
        }
      });
    if (pendingOption) {
      await cda.withExpectedCancellations({
        origin: uiOrigin, method: 'POST', paths: [frameSourcePath], requestIdPrefixes: ['cda-request-'],
        reason: 'Applying this Group closes its editor and retires the exact pending coded-value options query.',
        proof: { outputId, snapshotToken, resourceType: 'Observation', limit: 50, groupKeyFieldPath: GROUP_KEY_FIELD_PATH },
        actionLabel: applyGroupAction,
      }, applyGroupProposal);
    } else {
      await applyGroupProposal();
    }
    const applyFinishedAt = Date.now();
    assert.equal(await groupEditor.count(), 0, 'Applying the Group must unmount its exact editor');
    const groupOptionCancellations = (report.expectedCancellations ?? []).slice(cancellationsBeforeApply)
      .filter(cancellation => cancellation.proof?.scopeAction === applyGroupAction);
    assert(groupOptionCancellations.length <= 1, 'One Group Apply may retire at most one exact options request');
    let frameSourceRetirementEvidence = {
      outputId, snapshotToken, editorUnmounted: true, pendingBeforeApply: Boolean(pendingOption),
      cancellationObserved: groupOptionCancellations.length === 1, exact: true,
    };
    if (groupOptionCancellations.length === 1) {
      assert(pendingOption, 'A Group-options cancellation must match a request proven pending before Apply');
      const [cancellation] = groupOptionCancellations;
      const { entry, body } = pendingOption;
      assert(entry.startedAt < applyStartedAt && entry.completedAt >= applyStartedAt && entry.completedAt <= applyFinishedAt);
      assert.equal(entry.failure, 'net::ERR_ABORTED');
      assert.equal(cancellation.browserRequestId, entry.browserRequestId);
      assert.equal(cancellation.proof?.scopeAction, applyGroupAction);
      assert.deepEqual({ snapshotToken: body.snapshotToken, outputId: body.outputId,
        resourceType: body.resourceType, limit: body.limit },
      { snapshotToken, outputId, resourceType: 'Observation', limit: 50 });
      const diagnostics = report.network.filter(entry => entry.kind === 'network' &&
        entry.browserRequestId === cancellation.browserRequestId);
      assert.equal(diagnostics.length, 1);
      assert.deepEqual([diagnostics[0].url, diagnostics[0].errorText, diagnostics[0].triggerAction,
        diagnostics[0].expected, diagnostics[0].canceled, diagnostics[0].cancellationAction], [
        `${new URL(uiOrigin).origin}${frameSourcePath}`, 'net::ERR_ABORTED', applyGroupAction, true, true, applyGroupAction,
      ]);
      const requestErrors = report.errors.filter(error => error.kind === 'network' &&
        error.browserRequestId === cancellation.browserRequestId);
      assert(requestErrors.every(error => error.expected === true));
      frameSourceRetirementEvidence = {
        ...frameSourceRetirementEvidence,
        browserRequestId: entry.browserRequestId,
        requestStartedAt: entry.startedAt,
        requestFailedAt: entry.completedAt,
        requestBody: { snapshotToken: body.snapshotToken, outputId: body.outputId,
          resourceType: body.resourceType, limit: body.limit },
      };
    } else if (pendingOption) {
      assert.equal(pendingOption.entry.status, 200,
        'A pending Group-options request not retired by editor cleanup must finish successfully during Apply');
      assert.equal(pendingOption.entry.failure, undefined);
      assert(pendingOption.entry.completedAt >= applyStartedAt && pendingOption.entry.completedAt <= applyFinishedAt,
        'A non-retired Group-options request must complete inside the timed Apply action');
    }

    builder = await readBuilder();
    checkBuilderScope(builder);
    saved = getDocument(builder, outputId);
    const groupStep = saved.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
    assert(groupStep, 'Applied source must save a native GROUP step');
    assert.equal(groupStep.operation.group.keys?.length, 1);
    const groupKeyInputColumnId = groupKeySourceColumn.columnId;
    assert.equal(typeof groupKeyInputColumnId, 'string', 'Saved source column must expose its stable authoring columnId');
    assert(groupKeyInputColumnId.trim(), 'Saved source column authoring columnId must be nonempty');
    const groupInputBinding = {
      actual: groupStep.operation.group.keys[0].inputColumnId,
      expected: groupKeyInputColumnId,
      stableColumnId: groupKeySourceColumn.columnId,
      physicalColumnName: groupKeySourceColumn.column,
    };
    assert.equal(groupInputBinding.actual, groupInputBinding.expected,
      `Saved GROUP key must bind the stable source columnId: ${JSON.stringify(groupInputBinding)}`);
    assert.equal(groupStep.operation.group.aggregates?.length, 1);
    const aggregate = groupStep.operation.group.aggregates[0];
    assert.equal(aggregate.operation, 'COUNT_ROWS');
    const keyOutput = groupStep.outputs.find(output => output.id === groupStep.operation.group.keys[0].outputColumnId);
    const countOutput = groupStep.outputs.find(output => output.id === aggregate.outputColumnId);
    assert(keyOutput && countOutput, 'Saved GROUP outputs must retain exact key and count column IDs');
    await reloadSelectTable(outputId, expectedGroups.length, `Reload ${sourceLabel} saved GROUP output`);
    const reloadedGrid = await readGrid('saved');
    assertGrid(`Reloaded ${sourceLabel} GROUP output equals raw selected members`, reloadedGrid,
      [keyOutput.label, countOutput.label], expectedGroupRows.map(row => row.map(String)));
    builder = await readBuilder();
    saved = getDocument(builder, outputId);
    assert.equal(saved.population?.selectionRevisionId, selection.id, 'Reloaded GROUP must keep its exact immutable source selection');
    assert.equal(saved.population?.route?.length ?? 0, 0);
    const reloadedGroupRows = expectedGroupRows.map(row => row.map(String));
    const savedGroup = saved.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
    const savedKey = savedGroup?.operation?.group?.keys?.[0];
    const savedAggregate = savedGroup?.operation?.group?.aggregates?.[0];
    const groupOracleEvidence = {
      exactRenderedRows: reloadedGrid.ready && isDeepStrictEqual(reloadedGrid.headers, [keyOutput.label, countOutput.label]) &&
        isDeepStrictEqual(sortRows(reloadedGrid.rows), sortRows(reloadedGroupRows)),
      exactSavedGroup: saved.construction?.steps?.length === 1 && savedGroup?.id === groupStep.id &&
        savedKey?.inputColumnId === groupKeyInputColumnId && savedKey?.outputColumnId === keyOutput.id &&
        savedAggregate?.operation === 'COUNT_ROWS' && savedAggregate?.outputColumnId === countOutput.id,
      frameSourceRetirement: frameSourceRetirementEvidence,
      exactPopulation: saved.population?.selectionRevisionId === selection.id && (saved.population?.route?.length ?? 0) === 0,
      expectedRows: reloadedGroupRows,
      renderedRows: reloadedGrid.rows,
      headers: reloadedGrid.headers,
      keyColumnId: savedKey?.outputColumnId,
      countColumnId: savedAggregate?.outputColumnId,
      selectionId: saved.population?.selectionRevisionId,
    };
    assert(groupOracleEvidence.exactRenderedRows && groupOracleEvidence.exactSavedGroup && groupOracleEvidence.exactPopulation,
      `Reloaded ${sourceLabel} Group no longer matches the exact raw population oracle: ${JSON.stringify(groupOracleEvidence)}`);
    return {
      outputId, title, selection, selectionRefs: expectedRefs, rawRows, sourceLabel,
      groupKeyInputColumnId,
      groupKeyColumnLabel: groupKeySourceColumn.label,
      groupKeyColumnId: keyOutput.id,
      groupKeyLabel: keyOutput.label,
      countColumnId: countOutput.id,
      countOutputLabel: countOutput.label,
      groupStepId: groupStep.id,
      expectedGroups: expectedGroupRows,
      groupOracleEvidence,
      frameSourceRetirementEvidence,
      selectionSwitchEvidence,
      baselineDocument: structuredClone(getDocument(builder, outputId)),
    };
  };
  const outputOption = async (selector, outputId) => {
    const expectedValue = workspaceOutputOption(outputId);
    const options = await page.locator(selector).evaluateAll(nodes => nodes.map(select => [...select.options].map(option => ({
      value: option.value, group: option.parentElement?.label ?? '', disabled: option.disabled,
      text: String(option.textContent ?? '').replace(/\s+/g, ' ').trim(),
    }))));
    assert.equal(options.length, 1, `Expected exactly one selector ${selector}`);
    const matches = options[0].filter(option => option.value === expectedValue && option.group === 'Current draft tables' && !option.disabled);
    assert.equal(matches.length, 1, `Input must bind the exact current-draft output ${outputId}: ${JSON.stringify(options[0])}`);
    return expectedValue;
  };
  const chooseWorkspaceInputs = async sources => {
    await cda.click('[data-testid="construction-combine-choice-key_join"]');
    await waitSelector('select[aria-label="Input table 1"]');
    await waitSelector('select[aria-label="Input table 2"]');
    for (let index = 0; index < sources.length; index += 1) {
      const selector = `select[aria-label="Input table ${index + 1}"]`;
      const value = await outputOption(selector, sources[index].outputId);
      const locator = page.locator(selector);
      await action(`Bind current-draft Group input ${index + 1}`, locator,
        targetLocator => targetLocator.selectOption(value, { timeout: 5_000 }));
    }
  };
  const optionValueById = async (selector, expectedId) => {
    const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({
      value: option.value, text: option.textContent, disabled: option.disabled,
    })));
    const matches = options.filter(option => !option.disabled && option.value === expectedId);
    assert.equal(matches.length, 1, `Expected one enabled stable column ID ${JSON.stringify(expectedId)} in ${selector}: ${JSON.stringify(options)}`);
    return matches[0].value;
  };
  const selectColumnById = async (selector, columnId) => {
    const value = await optionValueById(selector, columnId);
    await action(`Choose source column ${columnId}`, page.locator(selector), locator => locator.selectOption(value, { timeout: 5_000 }));
    return value;
  };
  const setMapping = async (index, name, label, inputIndex, sourceColumnId, leaveUnselected = false) => {
    await cda.click('button', { name: 'Add output field' });
    const nameSelector = `input[aria-label="Output field ${index} name"]`;
    const labelSelector = `input[aria-label="Output field ${index} label"]`;
    await waitSelector(nameSelector);
    await action(`Name Join output ${index}`, page.locator(nameSelector), locator => locator.fill(name, { timeout: 5_000 }));
    await action(`Label Join output ${index}`, page.locator(labelSelector), locator => locator.fill(label, { timeout: 5_000 }));
    const selector = `select[aria-label="Output field ${index} source field in input ${inputIndex}"]`;
    await waitSelector(selector);
    if (!leaveUnselected) await selectColumnById(selector, sourceColumnId);
    return { name, label, selector, sourceColumnId, inputIndex };
  };
  const configureJoin = async (sources, joinType, targetOutputId, expectedRows) => {
    await chooseWorkspaceInputs(sources);
    await selectColumnById('select[aria-label="Matching pair 1 first field"]', sources[0].groupKeyColumnId);
    await selectColumnById('select[aria-label="Matching pair 1 second field"]', sources[1].groupKeyColumnId);
    const joinTypeSelector = 'select[aria-label="If a row in the first table has no match"]';
    const joinTypeSelect = page.locator(joinTypeSelector);
    await action(`Choose ${joinType} Join policy`, joinTypeSelect,
      locator => locator.selectOption(joinType, { timeout: 5_000 }));
    const outputs = [];
    outputs.push(await setMapping(1, 'left_group_key', 'Left group key', 1, sources[0].groupKeyColumnId));
    outputs.push(await setMapping(2, 'left_rows', 'Left rows', 1, sources[0].countColumnId));
    outputs.push(await setMapping(3, 'right_group_key', 'Right group key', 2, sources[1].groupKeyColumnId));
    const finalMapping = await setMapping(4, 'right_rows', 'Right rows', 2, sources[1].countColumnId, true);
    return { outputs, finalMapping, expectedRows, targetOutputId };
  };
  const captureProposal = async ({ capture, fromIndex, targetOutputId, sources, baseState, joinType, expectedStepId }) => {
    const path = `${explorerBase}/authoring/v2/construction-proposals`;
    const event = await capture.waitFor(entry => entry.path === path && entry.method === 'POST' &&
      entry.status === 200 && entry.completedAt && entry.body?.outputId === targetOutputId, { fromIndex, timeoutMs: 5_000 });
    const requestBody = capture.rawRequestBody(event);
    const responseBody = capture.rawResponseBody(event);
    assert(requestBody && responseBody, 'Native proposal capture must retain the exact request and response bodies');
    const step = requestBody.candidateConstruction?.steps?.at(-1);
    const expectedInputIDs = sources.map(source => source.outputId);
    const inputs = (step?.inputs ?? []).map(input => input?.outputId);
    const combine = step?.operation?.combine;
    const responseCandidate = responseBody.candidateConstruction;
    const responsePreview = responseBody.preview;
    const dom = await page.evaluate(() => ({
      proposalId: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id') ?? null,
      receiptId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id') ?? null,
      outputId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-output-id') ?? null,
    }));
    const outputs = step?.outputs ?? [];
    const named = name => outputs.find(output => output.name === name);
    const expectedProjections = [
      { outputColumnID: named('left_group_key')?.id, inputIndex: 0, inputColumnID: sources[0].groupKeyColumnId },
      { outputColumnID: named('left_rows')?.id, inputIndex: 0, inputColumnID: sources[0].countColumnId },
      { outputColumnID: named('right_group_key')?.id, inputIndex: 1, inputColumnID: sources[1].groupKeyColumnId },
      { outputColumnID: named('right_rows')?.id, inputIndex: 1, inputColumnID: sources[1].countColumnId },
    ];
    const projections = (combine?.projections ?? []).map(projection => ({
      outputColumnID: projection.outputColumnId, inputIndex: projection.inputIndex, inputColumnID: projection.inputColumnId,
    }));
    const checks = {
      requestOrigin: event.origin === new URL(uiOrigin).origin,
      exactPath: event.path === path,
      outputMatches: requestBody.outputId === targetOutputId && responseBody.outputId === targetOutputId && dom.outputId === targetOutputId,
      snapshotMatches: requestBody.snapshotToken === baseState.catalog.snapshotToken && responseBody.snapshotToken === requestBody.snapshotToken,
      versionMatches: requestBody.expectedDraftVersion === baseState.draftVersion && responseBody.draftVersion === baseState.draftVersion,
      digestMatches: requestBody.expectedDraftDigest === baseState.draftDigest && responseBody.draftDigest === baseState.draftDigest,
      joinKind: step?.operation?.kind === 'COMBINE' && combine?.kind === 'KEY_JOIN' && combine.joinType === joinType,
      inputsExact: isDeepStrictEqual(inputs, expectedInputIDs) && (step?.inputs ?? []).every(input => input.kind === 'WORKSPACE_OUTPUT' && !input.tableId && !input.revisionId),
      currentSourceDocuments: currentDraftSourceEvidence({ inputs: step?.inputs, expectedOutputIDs: expectedInputIDs,
        sourceDocuments: baseState.workspace?.documents }).ok,
      keyColumns: combine?.keys?.length === 1 && combine.keys[0].leftColumnId === sources[0].groupKeyColumnId &&
        combine.keys[0].rightColumnId === sources[1].groupKeyColumnId,
      outputMappings: isDeepStrictEqual(projections, expectedProjections) && expectedProjections.every(projection => projection.outputColumnID),
      editStepID: !expectedStepId || step?.id === expectedStepId,
      responseMatchesCandidate: constructionCandidateWireEquivalent(requestBody.candidateConstruction, responseCandidate),
      proposalReady: responseBody.previewStatus === 'READY' && responsePreview?.outputId === targetOutputId &&
        responseBody.proposalId === dom.proposalId && responsePreview?.receiptId === dom.receiptId,
      http200: event.status === 200,
    };
    requireCheck('correctness', 'native LEFT Join preview matches the independent grouped oracle and exact current-draft proposal',
      Object.values(checks).every(Boolean), {
        requestPath: event.path, requestOrigin: event.origin, status: event.status, requestId: event.requestId,
        outputId: targetOutputId, inputOutputIds: inputs, expectedInputIDs,
        joinType, checks,
        keyColumnIDs: combine?.keys ?? [], projections,
        expectedProjections, proposalId: responseBody.proposalId,
      });
    assert(Object.values(checks).every(Boolean), `Current-draft ${joinType} Join request or response lost its exact scope: ${JSON.stringify(checks)}`);
    return { event, requestBody, responseBody, step };
  };
  const addAndPreviewJoin = async (sources, joinType, expectedRows, expectedStepId) => {
    await selectTable(sources[0].outputId, sources[0].expectedGroups.length, `Select left Group source for ${joinType} Join`);
    const beforeCreate = await readBuilder();
    checkBuilderScope(beforeCreate);
    const rootNodeIds = beforeCreate.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible).map(node => node.nodeId);
    const existingIDs = (beforeCreate.workspace?.documents ?? []).map(document => document.output.id);
    const commands = requestCapture;
    const fromIndex = report.nativeRequests.length;
    const combineButton = page.getByTestId('construction-action-combine');
    await action('Create native empty Combine target', combineButton,
      locator => locator.click({ timeout: 5_000 }), async () => waitSelector('[data-testid="construction-combine-editor"]'));
    const event = await commands.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/commands` &&
      entry.method === 'POST' && entry.status === 200 && entry.completedAt && entry.body?.commands?.some(command => command.type === 'CREATE_TABLE'),
      { fromIndex, timeoutMs: 5_000 });
    const requestBody = commands.rawRequestBody(event);
    const responseBody = commands.rawResponseBody(event);
    const outputId = await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]').getAttribute('data-output-id');
    const targetEvidence = nativeCombineTargetBindingEvidence({
      requestBody, responseStatus: event.status, response: responseBody, expectedRootNodeIds: rootNodeIds,
      expectedRootResourceType: 'Observation', previousOutputIds: existingIDs, mountedOutputId: outputId,
    });
    assert(targetEvidence.ok, `Native Combine must create a separate rooted empty target: ${JSON.stringify(targetEvidence)}`);
    const targetOutputId = targetEvidence.outputId;
    builder = await readBuilder();
    checkBuilderScope(builder);
    const targetDocument = getDocument(builder, targetOutputId);
    const target = { outputId: targetOutputId, rootResourceType: 'Observation', baselineDocument: structuredClone(targetDocument) };
    assert.equal(targetDocument.columns?.length, 0);
    assert.equal(targetDocument.construction?.steps?.length ?? 0, 0);
    requireCheck('correctness', 'native Combine target is a fresh rooted empty current-draft document', true,
      { outputId: target.outputId, rootResourceType: targetDocument.rootResourceType, documentCount: builder.workspace.documents.length });
    const baseState = structuredClone(builder);
    const proposalTracker = requestCapture;
    let proposalFromIndex = report.nativeRequests.length;
    await cda.click('[data-testid="construction-combine-choice-key_join"]');
    await waitSelector('select[aria-label="Input table 1"]');
    const configuration = await configureJoin(sources, joinType, target.outputId, expectedRows);
    const finalValue = await optionValueById(configuration.finalMapping.selector, sources[1].countColumnId);
    const finalMapping = page.locator(configuration.finalMapping.selector);
    // Re-submit the final field as the timed auto-preview trigger after all other mappings are stable.
    proposalFromIndex = report.nativeRequests.length;
    await action(`Complete native ${joinType} Group Join mapping and render exact preview`, finalMapping,
      locator => locator.selectOption(finalValue, { timeout: 5_000 }), async () => waitProposal(target.outputId, expectedRows.length));
    const grid = await readGrid('proposal');
    assertGrid(`${joinType} Join proposal rows match exact raw Group outputs`, grid,
      ['Left group key', 'Left rows', 'Right group key', 'Right rows'], expectedRows.map(row => row.map(value => value === null ? '—' : String(value))));
    const candidate = await captureProposal({ capture: proposalTracker, fromIndex: proposalFromIndex, targetOutputId: target.outputId,
      sources, baseState, joinType, expectedStepId });
    return { target, baseState, candidate, proposalTracker, expectedRows };
  };
  const persistedJoin = (state, target, sources, joinType, expectedStepId) => {
    const document = getDocument(state, target.outputId);
    const steps = document.construction?.steps ?? [];
    const step = steps.at(-1);
    const combine = step?.operation?.combine;
    const outputByName = new Map((step?.outputs ?? []).map(output => [output.name, output]));
    const expectedInputs = sources.map(source => ({ kind: 'WORKSPACE_OUTPUT', outputId: source.outputId }));
    const actualInputs = (step?.inputs ?? []).map(input => ({ kind: input.kind, outputId: input.outputId }));
    const projections = (combine?.projections ?? []).map(projection => [projection.outputColumnId, projection.inputIndex, projection.inputColumnId]);
    const expectedProjections = [
      [outputByName.get('left_group_key')?.id, 0, sources[0].groupKeyColumnId],
      [outputByName.get('left_rows')?.id, 0, sources[0].countColumnId],
      [outputByName.get('right_group_key')?.id, 1, sources[1].groupKeyColumnId],
      [outputByName.get('right_rows')?.id, 1, sources[1].countColumnId],
    ];
    const keysExact = combine?.keys?.length === 1 && combine.keys[0].leftColumnId === sources[0].groupKeyColumnId &&
      combine.keys[0].rightColumnId === sources[1].groupKeyColumnId;
    const inputsEvidence = currentDraftSourceEvidence({ inputs: step?.inputs, expectedOutputIDs: sources.map(source => source.outputId),
      sourceDocuments: state.workspace?.documents });
    const exact = steps.length === 1 && step?.operation?.kind === 'COMBINE' && combine?.kind === 'KEY_JOIN' &&
      combine.joinType === joinType && (!expectedStepId || step.id === expectedStepId) &&
      isDeepStrictEqual(actualInputs, expectedInputs) && inputsEvidence.ok && keysExact &&
      isDeepStrictEqual(projections, expectedProjections) && expectedProjections.every(([outputId]) => Boolean(outputId)) &&
      document.rootResourceType === 'Observation';
    return { exact, document, step, combine, inputsEvidence, keysExact, projections, expectedProjections, outputByName };
  };
  const checkJoinPersistence = (state, target, sources, joinType, expectedStepId, name) => {
    const evidence = persistedJoin(state, target, sources, joinType, expectedStepId);
    requireCheck('persistence', name, evidence.exact, {
      outputId: target.outputId, stepId: evidence.step?.id, joinType: evidence.combine?.joinType,
      inputs: evidence.step?.inputs, keys: evidence.combine?.keys,
      projections: evidence.projections, expectedProjections: evidence.expectedProjections,
      currentDraftInputs: evidence.inputsEvidence,
    });
    assert(evidence.exact, `${name}: saved Join bindings differ from expected current-draft IDs`);
    return evidence;
  };
  const openSavedStep = async (outputId, stepId, editorSelector, rows) => {
    await selectTable(outputId, rows, `Select output ${outputId} before editing step`);
    const historyEntry = page.getByTestId(`construction-history-step-${stepId}`);
    await action(`Open saved history step ${stepId}`, historyEntry,
      locator => locator.click({ timeout: 5_000 }), async () => waitFunction(`Boolean(document.querySelector('[data-testid="construction-edit-step-${stepId}"]:not(:disabled)'))`));
    const editButton = page.getByTestId(`construction-edit-step-${stepId}`);
    await action(`Open saved ${editorSelector.includes('combine') ? 'Join' : 'Group'} editor ${stepId}`, editButton,
      locator => locator.click({ timeout: 5_000 }), async () => waitSelector(editorSelector));
  };
  const cancelProposal = async (target, name) => {
    const cancel = page.getByTestId('construction-cancel-proposal');
    await action(name, cancel, locator => locator.click({ timeout: 5_000 }), async () =>
      waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&!document.querySelector('[data-testid="construction-combine-editor"]')&&${selectedReady(target.outputId)}`));
  };
  const applyProposal = async (target, rows, name) => {
    const apply = page.getByTestId('construction-apply-proposal');
    await action(name, apply, locator => locator.click({ timeout: 5_000 }), async () => waitSaved(target.outputId, rows));
  };
  const headers = ['Left group key', 'Left rows', 'Right group key', 'Right rows'];

  try {
    const rawScan = rawObservationScan({ arangoContainer, project, generation });
    const preparedOracle = prepareCdaGroupJoinOracle(rawScan.rows);
    oracle = preparedOracle.memberships;
    const selectedIDs = [...oracle.left, ...oracle.right].map(row => row.id);
    const selectedDocumentIDs = [...oracle.left, ...oracle.right].map(row => row._id);
    assert(selectedIDs.length <= MAX_MEMBERS * 2 && selectedDocumentIDs.length <= MAX_MEMBERS * 2);
    const selectedAQL = `FOR r IN Observation FILTER r._id IN ${JSON.stringify(selectedDocumentIDs)} AND r.id IN ${JSON.stringify(selectedIDs)} AND r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)} AND r.payload.resourceType == "Observation" SORT r._id RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,groupKey:r.payload.subject.reference}`;
    const selectedJS = `print(JSON.stringify(db._query(${JSON.stringify(selectedAQL)}).toArray()));`.replaceAll('@', '\\u0040');
    const selectedResult = spawnSync('rtk', [
      'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
      '--javascript.execute-string', selectedJS,
    ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 4 * 1024 * 1024 });
    assert.equal(selectedResult.status, 0, `Exact selected Observation reread failed: ${tidy(selectedResult.stderr || selectedResult.stdout).slice(0, 1800)}`);
    const exactStart = selectedResult.stdout.indexOf('[');
    assert(exactStart >= 0, 'Exact selected Observation reread returned no JSON array');
    const exactRows = JSON.parse(selectedResult.stdout.slice(exactStart));
    const expectedByKey = new Map([...oracle.left, ...oracle.right].map(row => [row._id, row]));
    assert.equal(exactRows.length, expectedByKey.size, 'Exact raw reread returned a missing or extra Observation');
    assert.equal(new Set(exactRows.map(row => row._id)).size, expectedByKey.size);
    for (const row of exactRows) {
      const expected = expectedByKey.get(row._id);
      assert(expected, 'Exact raw reread returned a nonselected Observation');
      assert.equal(row.project, project);
      assert.equal(row.generation, generation);
      assert.equal(row.resourceType, 'Observation');
      assert.equal(row.id, expected.id);
      assert.equal(row.groupKey, expected.groupKey);
    }
    requireCheck('correctness', `CDA raw oracle finds independent bounded Observation memberships with shared and left-only ${GROUP_KEY_FIELD_PATH} Group keys`,
      oracle.left.length === 3 && oracle.right.length === 1 && oracle.sharedGroupKey !== oracle.leftOnlyGroupKey &&
      new Set(oracle.left.map(row => row.id)).size === oracle.left.length && new Set(oracle.right.map(row => row.id)).size === oracle.right.length &&
      oracle.left.filter(row => row.groupKey === oracle.sharedGroupKey).length === 2 && oracle.right.filter(row => row.groupKey === oracle.sharedGroupKey).length === 1 &&
      oracle.left.some(row => row.groupKey === oracle.leftOnlyGroupKey) && oracle.right.every(row => row.groupKey !== oracle.leftOnlyGroupKey), {
        project, generation, scanLimit: MAX_RAW_SCAN, returned: rawScan.rows.length,
        scanLimitBoundsReturnedRowsNotDatabaseScanCost: true,
        selectedMemberCap: MAX_MEMBERS,
        groupKeyFieldPath: GROUP_KEY_FIELD_PATH, sharedGroupKey: oracle.sharedGroupKey, leftOnlyGroupKey: oracle.leftOnlyGroupKey,
        leftIDs: oracle.left.map(row => row.id), rightIDs: oracle.right.map(row => row.id), leftSharedCount: 2,
        exactRereadCount: exactRows.length,
      source: 'owned Arango Observation payloads filtered by project, dataset_generation, and resourceType',
      });
    const {
      expectedLeftInitial, expectedRight, expectedLeftDistinct, expectedLeft, expectedInner, expectedDistinctInner,
    } = preparedOracle;

    const rawExplorer = `cda-cdj-${randomUUID()}`;
    const title = `CDA current-draft Group Join ${randomUUID().slice(0, 8)}`;
    const explorerResponse = await api(`/api/v1/projects/${encoded(project)}/explorers`, { name: rawExplorer, title });
    explorer = explorerResponse.explorerId ?? explorerResponse.id ?? explorerResponse.explorer?.id ?? rawExplorer;
    assert.equal(explorer, rawExplorer, 'Fresh Explorer creation must preserve the exact owned unique ID');
    explorerBase = apiRoot(project, explorer);
    requestCapture = cda.captureRequests(`${explorerBase}/authoring/v2`, {
      responsePaths: /commands|construction-proposals/,
    });
    const explorerList = await api(`/api/v1/projects/${encoded(project)}/explorers`);
    const summaries = Array.isArray(explorerList) ? explorerList : explorerList.explorers ?? explorerList.value ?? [];
    const matchingSummaries = summaries.filter(item => (item.explorerId ?? item.id ?? item.name) === explorer);
    assert.equal(matchingSummaries.length, 1, 'The exact created CDA Explorer must appear exactly once in its owned project list');
    const [summary] = matchingSummaries;
    assert.equal(summary.project, project);
    assert.equal(summary.title, title);
    assert.equal(summary.management, 'INTERACTIVE');
    builder = await readBuilder();
    const emptyDraft = builderDraftStateEvidence(builder, 'empty');
    assert.equal(builder.catalog?.generation, generation);
    assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest);
    initialScope = {
      generation: builder.catalog.generation,
      snapshotToken: builder.catalog.snapshotToken,
      authorizationScopeDigest: builder.catalog.authorizationScopeDigest,
    };
    const emptyScopeChecks = {
      project: summary.project === project,
      generation: builder.catalog.generation === generation,
      snapshot: Boolean(initialScope.snapshotToken),
      authorizationScope: Boolean(initialScope.authorizationScopeDigest),
      emptyBaseline: emptyDraft.ok,
      freshExplorerId: explorer === rawExplorer,
    };
    requireCheck('correctness', 'fresh CDA Explorer matches project, generation, snapshot, authorization scope, and empty baseline',
      Object.values(emptyScopeChecks).every(Boolean), {
        project, explorer, title, generation, snapshotPresent: Boolean(initialScope.snapshotToken),
        authorizationScopeDigestPresent: Boolean(initialScope.authorizationScopeDigest), emptyDraft,
      });
    assert(Object.values(emptyScopeChecks).every(Boolean), `Fresh Explorer scope/baseline mismatch: ${JSON.stringify(emptyScopeChecks)}`);

    const bootstrapPath = `${explorerBase}/authoring/v2/builder`;
    const builderURL = new URL(bootstrapPath, apiOrigin);
    assert.equal(builderURL.pathname, bootstrapPath, 'Builder request must stay on the exact owned project/Explorer path');
    assert.equal(builder.catalog.generation, generation);
    assert.equal(builder.catalog.authorizationScopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(builder.catalog.snapshotToken, initialScope.snapshotToken);
    const root = builder.catalog.nodes.find(node => node.resourceType === 'Observation' && node.rowRootEligible);
    assert(root, 'Fresh scoped CDA catalog has no Observation root');
    const left = await openGroupSource({ title: `Left Observation ${GROUP_KEY_FIELD_PATH} groups`, rawRows: oracle.left, sourceLabel: 'left' });
    const right = await openGroupSource({ title: `Right Observation ${GROUP_KEY_FIELD_PATH} groups`, rawRows: oracle.right, sourceLabel: 'right', previousSource: left });
    sources = [left, right];
    builder = await readBuilder();
    checkBuilderScope(builder);
    const sourceDocIDs = sources.map(source => source.outputId);
    const sourceDocs = sourceDocIDs.map(id => getDocument(builder, id));
    const groupOutputEvidence = sources.map((source, index) => {
      const document = sourceDocs[index];
      const step = document.construction?.steps?.find(candidate => candidate.operation?.kind === 'GROUP');
      const group = step?.operation?.group;
      const key = group?.keys?.[0];
      const aggregate = group?.aggregates?.[0];
      return {
        outputId: source.outputId,
        selectionId: document.population?.selectionRevisionId,
        expectedSelectionId: source.selection.id,
        groupOracle: source.groupOracleEvidence,
        frameSourceRetirement: source.frameSourceRetirementEvidence,
        exact: source.groupOracleEvidence.exactRenderedRows && source.groupOracleEvidence.exactSavedGroup &&
          source.groupOracleEvidence.exactPopulation && source.groupOracleEvidence.frameSourceRetirement?.exact === true &&
          document.construction?.steps?.length === 1 &&
          step?.id === source.groupStepId && key?.inputColumnId === source.groupKeyInputColumnId &&
          key?.outputColumnId === source.groupKeyColumnId && aggregate?.operation === 'COUNT_ROWS' &&
          aggregate?.outputColumnId === source.countColumnId &&
          document.population?.selectionRevisionId === source.selection.id && (document.population?.route?.length ?? 0) === 0,
      };
    });
    const leftHasLiteralCountTwo = left.expectedGroups.some(([groupKey, count]) => groupKey === oracle.sharedGroupKey && count === 2);
    const exactGroupOutputs = groupOutputEvidence.every(evidence => evidence.exact) && leftHasLiteralCountTwo;
    requireCheck('persistence', 'native Observation GROUP outputs equal exact raw counts including shared-key COUNT_ROWS 2',
      exactGroupOutputs, {
        sourceOutputIDs: sourceDocIDs, selections: sources.map(source => source.selection.id),
        expectedLeftGroups: left.expectedGroups, expectedRightGroups: right.expectedGroups,
        sharedGroupKey: oracle.sharedGroupKey, groupKeyFieldPath: GROUP_KEY_FIELD_PATH, leftHasLiteralCountTwo, groupOutputEvidence,
      });
    assert(exactGroupOutputs, 'Each reloaded native Group must retain its exact raw rows, key IDs, COUNT_ROWS output, and selection population');

    const initialJoin = await addAndPreviewJoin(sources, 'LEFT', expectedLeft, undefined);
    const targetOne = initialJoin.target;
    const cancelBaseline = initialJoin.baseState;
    await cancelProposal(targetOne, 'Cancel first LEFT Join preview without saving');
    await reloadSelectTable(targetOne.outputId, undefined, 'Reload canceled LEFT Join target');
    builder = await readBuilder();
    checkBuilderScope(builder);
    const canceled = canceledDraftEvidence(cancelBaseline, builder);
    const targetOneAfter = getDocument(builder, targetOne.outputId);
    const canceledExact = canceled.ok && isDeepStrictEqual(targetOneAfter, targetOne.baselineDocument) &&
      sources.every((source, index) => getDocument(builder, source.outputId).population.selectionRevisionId === source.selection.id &&
        isDeepStrictEqual(getDocument(builder, source.outputId), sourceDocs[index]));
    requireCheck('persistence', 'canceling the first LEFT Join leaves both Groups, target, and draft CAS unchanged after reload', canceledExact, {
      canceled, targetOutputId: targetOne.outputId, targetRestoredEmpty: isDeepStrictEqual(targetOneAfter, targetOne.baselineDocument),
      sourceOutputIDs: sourceDocIDs,
    });
    assert(canceledExact, 'Cancel and reload must preserve both Group sources, the exact empty target, and draft CAS');

    const applyJoin = await addAndPreviewJoin(sources, 'LEFT', expectedLeft, undefined);
    const target = applyJoin.target;
    const leftApplyBaseline = applyJoin.baseState;
    await applyProposal(target, expectedLeft.length, 'Apply current-draft LEFT Group Join');
    builder = await readBuilder();
    checkBuilderScope(builder);
    const leftPersisted = checkJoinPersistence(builder, target, sources, 'LEFT', applyJoin.candidate.step.id,
      'applied LEFT Join retains exact current-draft inputs, key IDs, mappings, and preview after reload');
    assert(builder.draftVersion > leftApplyBaseline.draftVersion && builder.draftDigest !== leftApplyBaseline.draftDigest,
      'Applying the Join must advance the current draft CAS');
    await reloadSelectTable(target.outputId, expectedLeft.length, 'Reload applied LEFT current-draft Group Join');
    assertGrid('Applied LEFT Join rows survive reload exactly', await readGrid('saved'), headers,
      expectedLeft.map(row => row.map(value => value === null ? '—' : String(value))));
    builder = await readBuilder();
    checkBuilderScope(builder);
    checkJoinPersistence(builder, target, sources, 'LEFT', leftPersisted.step.id,
      'applied LEFT Join retains exact current-draft inputs, key IDs, mappings, and preview after reload');

    let savedJoin = getDocument(builder, target.outputId).construction.steps.at(-1);
    await openSavedStep(target.outputId, savedJoin.id, '[data-testid="construction-combine-editor"]', expectedLeft.length);
    const joinTypeSelector = 'select[aria-label="If a row in the first table has no match"]';
    const joinType = page.locator(joinTypeSelector);
    const editCapture = requestCapture;
    const editFromIndex = report.nativeRequests.length;
    await action('Preview saved LEFT Join changed to INNER before Cancel', joinType,
      locator => locator.selectOption('INNER', { timeout: 5_000 }), async () => waitProposal(target.outputId, expectedInner.length));
    assertGrid('Saved Join INNER edit preview matches the independent raw oracle before Cancel', await readGrid('proposal'), headers,
      expectedInner.map(row => row.map(value => value === null ? '—' : String(value))));
    const beforeCancelEdit = structuredClone(builder);
    const innerCandidate = await captureProposal({ capture: editCapture, fromIndex: editFromIndex,
      targetOutputId: target.outputId, sources, baseState: beforeCancelEdit, joinType: 'INNER', expectedStepId: savedJoin.id });
    await cancelProposal(target, 'Cancel saved LEFT Join edit to INNER');
    await reloadSelectTable(target.outputId, expectedLeft.length, 'Reload saved LEFT Join after canceling INNER edit');
    const afterCancelEdit = await readBuilder();
    const editCancelEvidence = canceledDraftEvidence(beforeCancelEdit, afterCancelEdit);
    const stillLeft = persistedJoin(afterCancelEdit, target, sources, 'LEFT', savedJoin.id);
    const unchangedLeftPreview = assertGrid('Cancel preserves exact saved LEFT Join rows after reload', await readGrid('saved'), headers,
      expectedLeft.map(row => row.map(value => value === null ? '—' : String(value))));
    requireCheck('persistence', 'saved LEFT Join edit Cancel preserves exact construction, rows, and CAS after reload',
      editCancelEvidence.ok && stillLeft.exact && unchangedLeftPreview.rows.length === expectedLeft.length, {
        editCancelEvidence, savedLeft: stillLeft.exact,
        canceledCandidateStep: innerCandidate.step.id, savedStepId: savedJoin.id,
      });
    assert(editCancelEvidence.ok && stillLeft.exact);

    builder = afterCancelEdit;
    await openSavedStep(target.outputId, savedJoin.id, '[data-testid="construction-combine-editor"]', expectedLeft.length);
    const innerApplyCapture = requestCapture;
    const innerApplyFromIndex = report.nativeRequests.length;
    await action('Preview saved LEFT Join changed to INNER for Apply', page.locator(joinTypeSelector),
      locator => locator.selectOption('INNER', { timeout: 5_000 }), async () => waitProposal(target.outputId, expectedInner.length));
    assertGrid('Saved Join INNER Apply preview matches the independent raw oracle', await readGrid('proposal'), headers,
      expectedInner.map(row => row.map(value => value === null ? '—' : String(value))));
    const innerApplyBase = structuredClone(builder);
    const innerApplyCandidate = await captureProposal({ capture: innerApplyCapture, fromIndex: innerApplyFromIndex,
      targetOutputId: target.outputId, sources, baseState: innerApplyBase, joinType: 'INNER', expectedStepId: savedJoin.id });
    await applyProposal(target, expectedInner.length, 'Apply saved LEFT Join edit as INNER');
    builder = await readBuilder();
    checkBuilderScope(builder);
    const innerSaved = checkJoinPersistence(builder, target, sources, 'INNER', savedJoin.id,
      'saved LEFT Join edit to INNER preserves exact current-draft construction and preview after reload');
    assert(builder.draftVersion > innerApplyBase.draftVersion && builder.draftDigest !== innerApplyBase.draftDigest);
    await reloadSelectTable(target.outputId, expectedInner.length, 'Reload saved INNER Group Join');
    assertGrid('Saved INNER Join rows survive reload exactly', await readGrid('saved'), headers,
      expectedInner.map(row => row.map(value => value === null ? '—' : String(value))));
    builder = await readBuilder();
    checkBuilderScope(builder);
    checkJoinPersistence(builder, target, sources, 'INNER', innerSaved.step.id,
      'saved LEFT Join edit to INNER preserves exact current-draft construction and preview after reload');

    const beforeSourceEdit = structuredClone(builder);
    const oldPreviewReceipt = await page.locator('[data-testid="construction-preview"]').getAttribute('data-preview-receipt-id');
    const leftDocument = getDocument(builder, left.outputId);
    const sourceGroup = leftDocument.construction.steps.find(step => step.id === left.groupStepId);
    assert(sourceGroup, 'Left Group step must remain editable by its stable step ID');
    await openSavedStep(left.outputId, sourceGroup.id, '[data-testid="construction-reshape-editor"]', left.expectedGroups.length);
    const summarySelector = 'select[aria-label="Summary 1"]';
    const summaryInput = page.locator(summarySelector);
    await selectOptionByText(summarySelector, 'Count distinct values');
    await waitSelector('select[aria-label="Summary field 1"]');
    await selectColumnById('select[aria-label="Summary field 1"]', left.groupKeyInputColumnId);
    const advanced = await page.locator('[data-testid="construction-reshape-group-advanced"]').evaluate(element => !element.open).catch(() => false);
    if (advanced) await cda.click('[data-testid="construction-reshape-group-advanced"] summary');
    await waitSelector('input[aria-label="Summary output label 1"]');
    const distinctLabel = 'Distinct group-key values';
    const distinctRows = expectedLeftDistinct.map(([groupKey, count]) => [groupKey, String(count)]);
    const distinctInput = page.locator('input[aria-label="Summary output label 1"]');
    const sourceProposalCapture = requestCapture;
    const sourceProposalFromIndex = report.nativeRequests.length;
    await action('Preview upstream COUNT_DISTINCT group-key Group edit', distinctInput,
      locator => locator.fill(distinctLabel, { timeout: 5_000 }), async () => waitFunction(`(()=>{const ready=${proposalReady(left.outputId, distinctRows.length)};const label=document.querySelector('input[aria-label="Summary output label 1"]');const headers=[...document.querySelectorAll(${querySelector(`${PROPOSAL_SELECTOR} thead th`)})].map(cell=>String(cell.querySelector('span')?.textContent??'').trim());return Boolean(ready&&label?.value===${querySelector(distinctLabel)}&&headers.includes(${querySelector(distinctLabel)}))})()`));
    const distinctGrid = await readGrid('proposal');
    assertGrid('COUNT_DISTINCT source Group preview equals the raw group-key cardinality oracle', distinctGrid,
      [left.groupKeyLabel, distinctLabel], distinctRows);
    const sourceEvent = await sourceProposalCapture.waitFor(entry => entry.path === `${explorerBase}/authoring/v2/construction-proposals` &&
      entry.method === 'POST' && entry.status === 200 && entry.completedAt && entry.body?.outputId === left.outputId,
      { fromIndex: sourceProposalFromIndex, timeoutMs: 5_000 });
    const sourceRequest = sourceProposalCapture.rawRequestBody(sourceEvent);
    const sourceResponse = sourceProposalCapture.rawResponseBody(sourceEvent);
    const sourceCandidate = sourceRequest?.candidateConstruction?.steps?.find(step => step.id === sourceGroup.id);
    const sourceAggregate = sourceCandidate?.operation?.group?.aggregates?.[0];
    assert.equal(sourceRequest?.snapshotToken, builder.catalog.snapshotToken);
    assert.equal(sourceRequest?.expectedDraftVersion, builder.draftVersion);
    assert.equal(sourceRequest?.expectedDraftDigest, builder.draftDigest);
    assert.equal(sourceRequest?.outputId, left.outputId);
    assert.equal(sourceEvent.origin, new URL(uiOrigin).origin);
    assert.equal(sourceResponse?.outputId, left.outputId);
    assert.equal(sourceResponse?.snapshotToken, sourceRequest?.snapshotToken);
    assert.equal(sourceResponse?.draftVersion, sourceRequest?.expectedDraftVersion);
    assert.equal(sourceResponse?.draftDigest, sourceRequest?.expectedDraftDigest);
    assert(constructionCandidateWireEquivalent(sourceRequest?.candidateConstruction, sourceResponse?.candidateConstruction));
    assert.equal(sourceCandidate?.operation?.kind, 'GROUP');
    assert.equal(sourceAggregate?.operation, 'COUNT_DISTINCT');
    assert.equal(sourceAggregate?.inputColumnId, left.groupKeyInputColumnId);
    assert.equal(sourceResponse?.previewStatus, 'READY');

    await applyProposal({ outputId: left.outputId }, distinctRows.length, 'Apply upstream COUNT_DISTINCT group-key Group edit');
    const afterSourceEdit = await readBuilder();
    checkBuilderScope(afterSourceEdit);
    const savedLeftGroup = getDocument(afterSourceEdit, left.outputId).construction.steps.find(step => step.id === left.groupStepId);
    const savedAggregate = savedLeftGroup?.operation?.group?.aggregates?.[0];
    assert.equal(savedAggregate?.operation, 'COUNT_DISTINCT');
    assert.equal(savedAggregate?.inputColumnId, left.groupKeyInputColumnId);
    assert.equal(savedLeftGroup?.operation?.group?.keys?.[0]?.inputColumnId, left.groupKeyInputColumnId);
    assert.equal(getDocument(afterSourceEdit, left.outputId).population.selectionRevisionId, left.selection.id);
    const oldJoin = persistedJoin(beforeSourceEdit, target, sources, 'INNER', innerSaved.step.id);
    assert(oldJoin.exact, 'Saved Join must be exact before its upstream Group recomputes');
    const dependentTable = tableLocator(target.outputId);
    await action('Recompile downstream Join after upstream Group edit', dependentTable,
      locator => locator.click({ timeout: 5_000 }), async () => waitFunction(`(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector(${querySelector(TABLE_SELECTOR)});return Boolean(p?.dataset.previewStatus==='ready'&&p.dataset.previewOutputId===${querySelector(target.outputId)}&&p.dataset.previewReceiptId!==${querySelector(oldPreviewReceipt)}&&Number(p.dataset.currentDraftVersion)===${afterSourceEdit.draftVersion}&&t&&t.getAttribute('aria-rowcount')===${querySelector(String(expectedDistinctInner.length + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`));
    const newPreviewReceipt = await page.locator('[data-testid="construction-preview"]').getAttribute('data-preview-receipt-id');
    const targetAfterRecompile = persistedJoin(afterSourceEdit, target, sources, 'INNER', innerSaved.step.id);
    const recompile = {
      sourceAdvancedDraftCAS: afterSourceEdit.draftVersion > beforeSourceEdit.draftVersion && afterSourceEdit.draftDigest !== beforeSourceEdit.draftDigest,
      leftAggregateChanged: savedAggregate?.operation === 'COUNT_DISTINCT' && savedAggregate?.inputColumnId === left.groupKeyInputColumnId,
      joinInputsRetained: targetAfterRecompile.exact,
      receiptChanged: Boolean(oldPreviewReceipt && newPreviewReceipt && oldPreviewReceipt !== newPreviewReceipt),
      previewOutputBound: await page.locator('[data-testid="construction-preview"]').getAttribute('data-preview-output-id') === target.outputId,
    };
    assert(Object.values(recompile).every(Boolean), `Upstream Group edit did not produce a fresh dependent Join: ${JSON.stringify(recompile)}`);
    assertGrid('Dependent INNER Join recomputes the exact raw values after COUNT_DISTINCT edit', await readGrid('saved'), headers,
      expectedDistinctInner.map(row => row.map(value => value === null ? '—' : String(value))));
    await reloadSelectTable(target.outputId, expectedDistinctInner.length, 'Reload recomputed INNER Join after upstream Group edit');
    const reloadedRecomputeGrid = assertGrid('Recomputed INNER Join rows survive reload exactly', await readGrid('saved'), headers,
      expectedDistinctInner.map(row => row.map(value => value === null ? '—' : String(value))));
    builder = await readBuilder();
    checkBuilderScope(builder);
    const afterRecomputeReload = persistedJoin(builder, target, sources, 'INNER', innerSaved.step.id);
    const recomputeSurvivedReload = Object.values(recompile).every(Boolean) && afterRecomputeReload.exact &&
      isDeepStrictEqual(sortRows(reloadedRecomputeGrid.rows), sortRows(expectedDistinctInner.map(row => row.map(value => value === null ? '—' : String(value)))));
    requireCheck('persistence', 'upstream Group COUNT_DISTINCT edit recomputes the downstream Join from raw values after reload',
      recomputeSurvivedReload, {
        recompile, afterRecomputeReloadExact: afterRecomputeReload.exact, reloadedRows: reloadedRecomputeGrid.rows,
        expectedRows: expectedDistinctInner, oldPreviewReceipt, newPreviewReceipt,
        sourceStepId: left.groupStepId, targetOutputId: target.outputId,
      });
    assert(recomputeSurvivedReload, 'Upstream Group recompute must retain exact INNER Join inputs and raw-derived rows after reload');

    const beforeRemoval = structuredClone(builder);
    const currentJoinStep = getDocument(builder, target.outputId).construction.steps.at(-1);
    await selectTable(target.outputId, expectedDistinctInner.length, 'Select recomputed INNER Join for removal lifecycle');
    const history = page.getByTestId(`construction-history-step-${currentJoinStep.id}`);
    await action('Open INNER Join history for removal', history,
      locator => locator.click({ timeout: 5_000 }), async () => waitFunction(`Boolean(document.querySelector('[data-testid="construction-remove-step-${currentJoinStep.id}"]:not(:disabled)'))`));
    const remove = page.getByTestId(`construction-remove-step-${currentJoinStep.id}`);
    await action('Preview INNER Join removal before Cancel', remove,
      locator => locator.click({ timeout: 5_000 }), async () => waitProposal(target.outputId, 0));
    await cancelProposal(target, 'Cancel INNER Join removal');
    await reloadSelectTable(target.outputId, expectedDistinctInner.length, 'Reload recomputed INNER Join after canceling removal');
    builder = await readBuilder();
    const removalCancel = canceledDraftEvidence(beforeRemoval, builder);
    const retainedJoin = persistedJoin(builder, target, sources, 'INNER', currentJoinStep.id);
    assertGrid('Cancel preserves recomputed INNER Join rows and values', await readGrid('saved'), headers,
      expectedDistinctInner.map(row => row.map(value => value === null ? '—' : String(value))));
    requireCheck('persistence', 'Join removal Cancel preserves saved INNER construction and output after reload',
      removalCancel.ok && retainedJoin.exact, { removalCancel, retainedJoin: retainedJoin.exact, targetOutputId: target.outputId });
    assert(removalCancel.ok && retainedJoin.exact);

    await selectTable(target.outputId, expectedDistinctInner.length, 'Select recomputed INNER Join for Apply removal');
    await action('Open INNER Join history for Apply removal', page.getByTestId(`construction-history-step-${currentJoinStep.id}`),
      locator => locator.click({ timeout: 5_000 }), async () => waitFunction(`Boolean(document.querySelector('[data-testid="construction-remove-step-${currentJoinStep.id}"]:not(:disabled)'))`));
    await action('Preview INNER Join removal for Apply', page.getByTestId(`construction-remove-step-${currentJoinStep.id}`),
      locator => locator.click({ timeout: 5_000 }), async () => waitProposal(target.outputId, 0));
    const applyRemoval = page.getByTestId('construction-apply-proposal');
    await action('Apply INNER Join removal and restore empty target', applyRemoval,
      locator => locator.click({ timeout: 5_000 }), async () => waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&${selectedReady(target.outputId)}`));
    await reloadSelectTable(target.outputId, undefined, 'Reload removed Join and select restored empty target');
    builder = await readBuilder();
    checkBuilderScope(builder);
    const restoredTarget = getDocument(builder, target.outputId);
    const targetRestoration = rootedEmptyTargetRestorationEvidence(restoredTarget, target.baselineDocument, target);
    const retainedSourceDocuments = sources.map(source => getDocument(builder, source.outputId));
    const sourceDocsRetained = retainedSourceDocuments.every((document, index) =>
      document.population?.selectionRevisionId === sources[index].selection.id &&
      (document.construction?.steps ?? []).some(step => step.operation?.kind === 'GROUP'));
    requireCheck('persistence', 'applying Join removal restores the exact empty target while preserving both Group populations',
      targetRestoration.ok && sourceDocsRetained, {
        targetRestoration, sourceDocsRetained,
        targetOutputId: target.outputId, sourceOutputIDs: sourceDocIDs,
        selectionIDs: sources.map(source => source.selection.id),
      });
    assert(targetRestoration.ok && sourceDocsRetained, 'Join removal must restore the rooted empty target and preserve the two independent Group sources');

    const finalBuilder = await readBuilder();
    checkBuilderScope(finalBuilder);
    const allInputs = finalBuilder.workspace.documents.flatMap(document => (document.construction?.steps ?? [])
      .flatMap(step => step.inputs ?? []));
    const noPinnedSources = allInputs.every(input => input.kind !== 'TABLE_REVISION' && !input.tableId && !input.revisionId);
    const noPublish = publishRequests === 0;
    requireCheck('correctness', 'CDA current-draft lifecycle never publishes or pins table revisions', noPublish && noPinnedSources, {
      publishRequests, noPinnedSources, inputKinds: allInputs.map(input => input.kind), explorer, project, generation,
    });
    assert(noPublish && noPinnedSources, 'Current-draft Group and Join workflow must never Publish or pin TABLE_REVISION inputs');
  } finally {
    page.off('request', onRequest);
    page.off('response', onSelectionResponse);
    page.off('requestfinished', onSelectionRequestFinished);
    page.off('requestfailed', onSelectionRequestFailed);
  }
}
