import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp, localCDAApiContainer } from './lib/api-build-freeze.mjs';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

// Standalone verifier for a leaf row after three authored RELATED_EXPAND steps.
// Copy beside scripts/lib/browser.mjs only after the composed-lineage compiler
// work is integrated. This script is restricted to the local no-auth fixture.
const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const lineageMode = process.env.LOOM_COMPOSED_LINEAGE_MODE ?? 'COMPOSED_RELATED';
assert(['COMPOSED_RELATED', 'DIRECT_PIVOT'].includes(lineageMode), `Unsupported LOOM_COMPOSED_LINEAGE_MODE: ${lineageMode}`);
const directPivot = lineageMode === 'DIRECT_PIVOT';
const explorer = `${directPivot ? 'direct-pivot-lineage' : 'composed-row-lineage'}-${Date.now()}-${randomUUID().slice(0, 8)}`;
const evidence = process.argv[2] ?? `/tmp/loom-${directPivot ? 'direct-pivot' : 'composed-row-lineage'}-${Date.now()}`;
const rootDiscoveryLimit = 2000;
const witnessPathsLimit = 2;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const localAPI = new URL(apiOrigin);
const localUI = new URL(uiOrigin);
assert.equal(localAPI.origin, 'http://127.0.0.1:8188', 'Use only the local unrestricted no-auth CDA API');
assert.equal(localUI.origin, 'http://127.0.0.1:30008', 'Use only the local CDA Builder UI');
const localOrigins = new Set([localAPI.origin, localUI.origin]);
assert.equal(project, 'loom_dev_cda_fhir');
assert.equal(generation, 'cda-fhir-v1');
assert(!explorer.startsWith('cda-builder-full-qa-'), 'Never use the protected shared QA explorer');
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1';
const base = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2`;
const explorersPath = `/api/v1/projects/${project}/explorers`;
const report = {
  explorer, project, generation, lineageMode, scope: {
    apiOrigin: localAPI.origin, uiOrigin: localUI.origin,
    authorization: 'local no-auth unrestricted API',
    oracleScope: 'project + dataset generation + explicit member root IDs',
  }, routeContract: directPivot ? ['Observation direct membership roots', 'ordinary Pivot grouped by Observation FHIR resource ID, categorized by Observation.status'] : [
    'Specimen -[subject]-> Patient',
    'Patient <-[subject]- Observation -[specimen]-> Specimen',
    'Specimen -[subject]-> Patient',
  ],
  cases: [], errors: [], requests: [], nativeRequests: [], started: new Date().toISOString(),
};
await mkdir(evidence, { recursive: true });
const sourceFreeze = await captureSourceFreeze(fileURLToPath(new URL('..', import.meta.url)));
let browser, builder, outputId, apiBuildFreeze;
const nativeById = new Map();
const pendingNetworkReads = new Set();
const oracleQueries = [];

const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `composed-lineage-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const command = async commands => {
  await api(base + '/commands', {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(base + '/builder');
};
const doc = state => state.workspace.documents.find(item => item.output.id === outputId);
const scopedDocument = alias => `${alias}.project == ${JSON.stringify(project)} AND ${alias}.dataset_generation == ${JSON.stringify(generation)}`;
const rawQuery = query => {
  assert(query.includes(JSON.stringify(project)), 'Every raw oracle query must bind the CDA project');
  assert(query.includes(JSON.stringify(generation)), 'Every raw oracle query must bind the CDA dataset generation');
  assert(!query.includes('auth_resource_path IN'), 'The local unrestricted fixture must not guess authorization paths');
  oracleQueries.push(query);
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', script,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf('[')));
};
const uniqueBy = (values, key) => [...new Map(values.map(value => [key(value), value])).values()];
const relatedRows = (priorRows, queryFor) => {
  if (!priorRows.length) return [];
  const payload = priorRows.map(row => ({ pathKey: row.pathKey, terminalID: row.terminal._id }));
  const query = queryFor(JSON.stringify(payload));
  const raw = rawQuery(query);
  const priorByPath = new Map(priorRows.map(row => [row.pathKey, row]));
  const distinct = uniqueBy(raw, row => `${row.parentPath}\u0000${row.terminal._id}`);
  return distinct.map(row => {
    const parent = priorByPath.get(row.parentPath);
    assert(parent, `Oracle returned an unbound prior-stage row ${row.parentPath}`);
    return {
      pathKey: `${parent.pathKey}/${row.terminal._id}`,
      root: parent.root,
      stageDocs: [...parent.stageDocs, row.terminal],
      terminal: row.terminal,
      bridgeDocs: [...(parent.bridgeDocs ?? []), ...(row.bridge ? [row.bridge] : [])],
    };
  });
};
const resourceProjection = resource => `{
  _id: ${resource}._id, _key: ${resource}._key, id: ${resource}.id,
  project: ${resource}.project, dataset_generation: ${resource}.dataset_generation,
  auth_resource_path: ${resource}.auth_resource_path
}`;
const repeatedLeafWitnessQuery = `
FOR rootDoc IN (
  FOR candidate IN Specimen
    FILTER ${scopedDocument('candidate')}
    SORT candidate._key
    LIMIT ${rootDiscoveryLimit}
    RETURN candidate
)
LET matchingPaths = (
  FOR stageOneEdge IN fhir_edge
    FILTER stageOneEdge._from == rootDoc._id AND stageOneEdge.label == "subject_Patient" AND ${scopedDocument('stageOneEdge')}
    FILTER STARTS_WITH(stageOneEdge._to, "Patient/")
    LET stageOne = DOCUMENT(stageOneEdge._to)
    FILTER stageOne != null AND ${scopedDocument('stageOne')}
    FOR subjectEdge IN fhir_edge
      FILTER subjectEdge._to == stageOne._id AND subjectEdge.label == "subject_Patient" AND ${scopedDocument('subjectEdge')}
      FILTER STARTS_WITH(subjectEdge._from, "Observation/")
      LET observation = DOCUMENT(subjectEdge._from)
      FILTER observation != null AND ${scopedDocument('observation')}
      FOR specimenEdge IN fhir_edge
        FILTER specimenEdge._from == observation._id AND specimenEdge.label == "specimen_Specimen" AND ${scopedDocument('specimenEdge')}
        FILTER STARTS_WITH(specimenEdge._to, "Specimen/")
        LET stageTwo = DOCUMENT(specimenEdge._to)
        FILTER stageTwo != null AND ${scopedDocument('stageTwo')}
        FOR stageThreeEdge IN fhir_edge
          FILTER stageThreeEdge._from == stageTwo._id AND stageThreeEdge.label == "subject_Patient" AND ${scopedDocument('stageThreeEdge')}
          FILTER STARTS_WITH(stageThreeEdge._to, "Patient/")
          LET stageThree = DOCUMENT(stageThreeEdge._to)
          FILTER stageThree != null AND ${scopedDocument('stageThree')}
          FILTER stageOne._id == stageThree._id
          LIMIT ${witnessPathsLimit}
          RETURN {
            root: ${resourceProjection('rootDoc')},
            stageOne: ${resourceProjection('stageOne')},
            bridge: ${resourceProjection('observation')},
            stageTwo: ${resourceProjection('stageTwo')},
            stageThree: ${resourceProjection('stageThree')}
          }
)
FILTER LENGTH(matchingPaths) > 0
LET witness = FIRST(matchingPaths)
SORT rootDoc._key
LIMIT 2
RETURN {
  root: witness.root, stageOne: witness.stageOne, bridge: witness.bridge,
  stageTwo: witness.stageTwo, stageThree: witness.stageThree,
  repeatedPathOverflow: LENGTH(matchingPaths) == ${witnessPathsLimit}
}`;
const stageOneQuery = payload => `
FOR prior IN ${payload}
  FOR e IN fhir_edge
    FILTER e._from == prior.terminalID AND e.label == "subject_Patient" AND ${scopedDocument('e')}
    FILTER STARTS_WITH(e._to, "Patient/")
    LET p = DOCUMENT(e._to)
    FILTER p != null AND ${scopedDocument('p')}
    RETURN {parentPath: prior.pathKey, terminal: ${resourceProjection('p')}}`;
const stageTwoQuery = payload => `
FOR prior IN ${payload}
  FOR subjectEdge IN fhir_edge
    FILTER subjectEdge._to == prior.terminalID AND subjectEdge.label == "subject_Patient" AND ${scopedDocument('subjectEdge')}
    FILTER STARTS_WITH(subjectEdge._from, "Observation/")
    LET observation = DOCUMENT(subjectEdge._from)
    FILTER observation != null AND ${scopedDocument('observation')}
    FOR specimenEdge IN fhir_edge
      FILTER specimenEdge._from == observation._id AND specimenEdge.label == "specimen_Specimen" AND ${scopedDocument('specimenEdge')}
      FILTER STARTS_WITH(specimenEdge._to, "Specimen/")
      LET specimen = DOCUMENT(specimenEdge._to)
      FILTER specimen != null AND ${scopedDocument('specimen')}
      RETURN {parentPath: prior.pathKey, terminal: ${resourceProjection('specimen')}, bridge: ${resourceProjection('observation')}}`;
const stageThreeQuery = payload => `
FOR prior IN ${payload}
  FOR e IN fhir_edge
    FILTER e._from == prior.terminalID AND e.label == "subject_Patient" AND ${scopedDocument('e')}
    FILTER STARTS_WITH(e._to, "Patient/")
    LET p = DOCUMENT(e._to)
    FILTER p != null AND ${scopedDocument('p')}
    RETURN {parentPath: prior.pathKey, terminal: ${resourceProjection('p')}}`;

const proposal = async (name, started, expectedRows) => {
  const deadline = started + 5000;
  let response;
  while (!(response = report.nativeRequests.findLast(request => request.path === base + '/construction-proposals' &&
    request.startedAt >= started && request.completedAt && request.response))) {
    assert(Date.now() < deadline, `${name} did not complete a fresh proposal within five seconds`);
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  if (response.status === 200 && response.response.proposalId) {
    while (true) {
      const currentID = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId;`);
      response = report.nativeRequests.findLast(request => request.path === base + '/construction-proposals' &&
        request.startedAt >= started && request.completedAt && request.response?.proposalId === currentID);
      if (response) break;
      assert(Date.now() < deadline, `${name} current proposal did not render within five seconds`);
      await new Promise(resolve => setTimeout(resolve, 50));
    }
  }
  await waitForBrowser(browser.cdp, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
  const result = await browserEval(browser.cdp, `const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:p?.dataset.proposalStatus,text:p?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(r=>[...r.querySelectorAll('td')].map(c=>c.innerText))};`);
  assert.equal(result.status, 'ready', result.text);
  assert.equal(result.rows.length, Math.min(25, expectedRows.length));
  const witnesses = new Set(expectedRows.map(row => JSON.stringify(row)));
  for (const row of result.rows) assert(witnesses.has(JSON.stringify(row)), `${name} proposal row lacks a raw source witness: ${JSON.stringify(row)}`);
  assert(Date.now() - started <= 5000, `${name} exceeded the five-second proposal bound`);
  report.cases.push({ name, elapsedMs: Date.now() - started, previewRows: result.rows.length });
  return response.response;
};
const apply = async expectedRows => {
  const started = Date.now();
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  const deadline = started + 5000;
  while (!report.nativeRequests.some(request => request.path === base + '/preview' && request.startedAt >= started && request.completedAt && request.status === 200)) {
    assert(Date.now() < deadline, 'Apply did not complete a fresh saved preview within five seconds');
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  await waitForBrowser(browser.cdp, `!document.body.innerText.includes('Loading your table…')`);
  const mounted = await browserEval(browser.cdp, `return (async () => {
    const root = document.querySelector('[data-testid="preview-table-scroll"]');
    const table = root.querySelector('[role="table"]');
    const total = Number(table.getAttribute('aria-rowcount')) - 1;
    const rows = new Map();
    root.scrollTop = 0;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    for (let page = 0; page < 100 && rows.size < total; page++) {
      for (const row of table.querySelectorAll('[role="row"]')) {
        const ordinal = Number(row.firstElementChild?.textContent?.trim());
        if (!Number.isInteger(ordinal) || ordinal < 1) continue;
        rows.set(ordinal, [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
      }
      const next = Math.min(root.scrollTop + Math.max(1, root.clientHeight / 2), root.scrollHeight - root.clientHeight);
      if (rows.size >= total || next === root.scrollTop) break;
      root.scrollTop = next;
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    }
    root.scrollTop = 0;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    return [...rows.entries()].sort((a,b) => a[0]-b[0]).map(([,row]) => row);
  })();`);
  const witnessRows = new Set(expectedRows.map(row => JSON.stringify(row)));
  for (const row of mounted) assert(witnessRows.has(JSON.stringify(row)), `Mounted row is absent from the scoped raw oracle: ${JSON.stringify(row)}`);
  assert(Date.now() - started <= 5000, 'Apply-to-render exceeded five seconds');
  report.cases.push({ name: 'apply-proposal-to-render', elapsedMs: Date.now() - started });
  builder = await api(base + '/builder');
};
const open = async () => {
  const started = Date.now();
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await waitForBrowser(browser.cdp, `!document.body.innerText.includes('Loading your table…') && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')`);
  assert(Date.now() - started <= 5000, 'Reload-to-render exceeded five seconds');
  report.cases.push({ name: 'reload-saved-table', elapsedMs: Date.now() - started });
};
const assertMountedRows = async (expectedRows, label) => {
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, expectedRows.length) + 1))}`);
  const mounted = await browserEval(browser.cdp, `return (async () => {
    const root = document.querySelector('[data-testid="preview-table-scroll"]');
    const table = root.querySelector('[role="table"]');
    const total = Number(table.getAttribute('aria-rowcount')) - 1;
    const rows = new Map();
    root.scrollTop = 0;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    for (let page = 0; page < 100 && rows.size < total; page++) {
      for (const row of table.querySelectorAll('[role="row"]')) {
        const ordinal = Number(row.firstElementChild?.textContent?.trim());
        if (!Number.isInteger(ordinal) || ordinal < 1) continue;
        rows.set(ordinal, [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
      }
      const next = Math.min(root.scrollTop + Math.max(1, root.clientHeight / 2), root.scrollHeight - root.clientHeight);
      if (rows.size >= total || next === root.scrollTop) break;
      root.scrollTop = next;
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    }
    root.scrollTop = 0;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    return [...rows.entries()].sort((a,b) => a[0]-b[0]).map(([,row]) => row);
  })();`);
  assert.equal(mounted.length, Math.min(25, expectedRows.length), `${label} must render the complete bounded preview`);
  if (expectedRows.length <= 25) assert.deepEqual(mounted.map(row => JSON.stringify(row)).sort(), expectedRows.map(row => JSON.stringify(row)).sort(), `${label} exact rendered rows`);
  const witnesses = new Set(expectedRows.map(row => JSON.stringify(row)));
  for (const row of mounted) assert(witnesses.has(JSON.stringify(row)), `${label} mounted row is absent from the scoped raw oracle: ${JSON.stringify(row)}`);
};
const proposeRemoval = async (stepID, name, expectedRows) => {
  const started = Date.now();
  await click(browser.cdp, `[data-testid="construction-history-step-${stepID}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid=${JSON.stringify(`construction-remove-step-${stepID}`)}]')?.disabled === false`);
  await click(browser.cdp, `[data-testid="construction-remove-step-${stepID}"]`);
  return proposal(name, started, expectedRows);
};
const cancelRemoval = async (baseline, name, expectedRows) => {
  const started = Date.now();
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  const after = await api(base + '/builder');
  assert.deepEqual(after.workspace, baseline.workspace, `${name} cancellation must preserve the authored workspace`);
  assert.equal(after.draftVersion, baseline.draftVersion, `${name} cancellation must preserve the draft version`);
  assert.equal(after.draftDigest, baseline.draftDigest, `${name} cancellation must preserve the draft digest`);
  builder = after;
  await assertMountedRows(expectedRows, name);
  assert(Date.now() - started <= 5000, `${name} Cancel-to-render exceeded five seconds`);
  report.cases.push({ name: `${name} cancel-to-render`, elapsedMs: Date.now() - started });
};
const chooseRoute = async (targetType, label) => {
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false`);
  await click(browser.cdp, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(`${panel} select[aria-label="Related record type"]`)})?.disabled === false`);
  await selectOption(browser.cdp, `${panel} select[aria-label="Related record type"]`, targetType);
  const selector = `input[aria-label=${JSON.stringify(label)}]`;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(selector)}))`);
  const visible = await browserEval(browser.cdp, `const item=document.querySelector(${JSON.stringify(selector)});return Boolean(item&&!item.closest('details:not([open])')&&item.getBoundingClientRect().height>0);`);
  if (!visible) {
    await click(browser.cdp, `${panel} [data-testid="construction-related-expand-other-routes"] summary`);
  }
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(selector)})?.disabled === false`, 5000);
  await click(browser.cdp, selector);
  await selectOption(browser.cdp, `${panel} select[aria-label="If a current row has no matches"]`, 'EXCLUDE');
};
const expand = async ({ name, targetType, routeLabel, expectedRows }) => {
  const started = Date.now();
  await chooseRoute(targetType, routeLabel);
  await proposal(name, started, expectedRows);
  await apply(expectedRows);
};

const runDirectPivot = async () => {
  const sourceQuery = `FOR observation IN Observation FILTER ${scopedDocument('observation')} AND IS_STRING(observation.payload.status) AND observation.payload.status != "" SORT observation._key LIMIT 2000 RETURN {id:observation.id,_id:observation._id,_key:observation._key,status:observation.payload.status,project:observation.project,dataset_generation:observation.dataset_generation}`;
  const candidates = rawQuery(sourceQuery);
  assert(candidates.length >= 2, 'The scoped raw Observation scan must find at least two status-bearing records');
  const candidatesByStatus = new Map();
  for (const candidate of candidates) {
    assert(candidate.project === project && candidate.dataset_generation === generation);
    const matching = candidatesByStatus.get(candidate.status) ?? [];
    if (matching.length < 2) matching.push(candidate);
    candidatesByStatus.set(candidate.status, matching);
  }
  const chosen = [...candidatesByStatus.values()].find(records => records.length === 2);
  assert(chosen, 'The bounded CDA scan must find two distinct Observation IDs with one shared status category');
  const members = [...chosen].sort((left, right) => left.id.localeCompare(right.id));
  assert.notEqual(members[0].id, members[1].id);
  const categoryKey = { kind: 'STRING', string: members[0].status };
  const baselineRows = members.map(member => [member.id, member.status]);
  const expectedRows = members.map(member => [member.id, member.id]);
  const expectedContributorByID = new Map(members.map(member => [member.id, ['Observation', member.id, member._key]]));
  report.oracle = {
    query: sourceQuery,
    scope: 'project + dataset generation + exact two-member Observation selection',
    candidateScanCount: candidates.length,
    members,
    typedCategories: [categoryKey],
    groupedCategoryMemberships: [{ groupKey: members.map(member => member.id), category: categoryKey, sourceIDs: members.map(member => member.id) }],
    expectedRows,
    expectedContributors: [...expectedContributorByID.values()],
    rawQueryLog: oracleQueries,
  };

  await api(explorersPath, { name: explorer, title: 'Direct Pivot source-record inspection QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.workspace?.documents?.length ?? 0, 0, 'Direct Pivot verifier must own a fresh empty Explorer');
  assert.equal(builder.catalog.generation, generation);
  const node = builder.catalog.nodes.find(candidate => candidate.resourceType === 'Observation');
  assert(node, 'Catalog must expose Observation as a direct source root');
  await command([{ type: 'CREATE_TABLE', title: 'Direct Pivot lineage', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  const statusField = builder.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'status');
  assert(idField && statusField, 'Direct Pivot requires the independently witnessed Observation.id and Observation.status fields');
  await command([
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation FHIR resource ID' },
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: statusField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation.status' },
  ]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: members.map(member => ({ project, generation, resourceType: 'Observation', id: member.id })) } },
  });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(choice => choice.route.length === 0);
  assert(direct, 'The explicit member selection must provide a direct Observation route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  const sourceWorkspace = builder.workspace;
  const sourceDraftVersion = builder.draftVersion;
  const sourceDraftDigest = builder.draftDigest;

  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.consoleAPICalled', event => { if (event.type === 'error') report.errors.push({ kind: 'console', args: event.args }); });
  browser.cdp.on('Runtime.exceptionThrown', event => report.errors.push({ kind: 'runtime', details: event.exceptionDetails }));
  browser.cdp.on('Network.loadingFailed', event => { if (event.type === 'Script' && event.errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', error: event.errorText }); });
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, wallTime }) => {
    const url = new URL(request.url);
    if (!url.pathname.startsWith(base + '/')) return;
    const authorizationHeaderPresent = Object.keys(request.headers ?? {}).some(header => header.toLowerCase() === 'authorization');
    const entry = { requestId, path: url.pathname, origin: url.origin, authorizationHeaderPresent, method: request.method, startedAt: Math.round(wallTime * 1000) };
    if (request.postData) entry.body = JSON.parse(request.postData);
    nativeById.set(requestId, entry);
    report.nativeRequests.push(entry);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
    const entry = nativeById.get(requestId);
    if (entry) entry.status = response.status;
    if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) report.errors.push({ kind: 'http', status: response.status, url: response.url });
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = nativeById.get(requestId);
    if (!entry) return;
    entry.completedAt = Date.now();
    const read = browser.cdp.send('Network.getResponseBody', { requestId }).then(result => {
      const body = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      try { entry.response = JSON.parse(body); } catch { entry.response = body.slice(0, 32768); }
    }).catch(error => { entry.responseReadError = String(error); }).finally(() => pendingNetworkReads.delete(read));
    pendingNetworkReads.add(read);
  });

  const chooseField = async (label, prefix) => {
    const selector = `select[aria-label=${JSON.stringify(label)}]`;
    const options = await browserEval(browser.cdp, `return [...document.querySelector(${JSON.stringify(selector)}).options].map(option=>({value:option.value,label:option.textContent.trim()}));`);
    const field = options.find(option => option.label.startsWith(prefix));
    assert(field, `${label} lacks ${prefix}: ${JSON.stringify(options)}`);
    await selectOption(browser.cdp, selector, field.value, field.value.startsWith('source:') ? {
      settledWhen: `document.querySelector(${JSON.stringify(selector)})?.selectedOptions[0]?.textContent.startsWith(${JSON.stringify(prefix)}) || Boolean(document.querySelector('[data-testid="construction-proposal-ready"]'))`,
    } : {});
  };
  const typePivotOutputName = async name => {
    const advanced = '[data-testid="construction-reshape-pivot-advanced"]';
    if (!await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(advanced)})?.open;`)) {
      await click(browser.cdp, `${advanced} summary`);
    }
    await waitForBrowser(browser.cdp, `document.querySelectorAll('input[aria-label^="Pivot output name "]').length === 1`);
    const inputs = await browserEval(browser.cdp, `return [...document.querySelectorAll('input[aria-label^="Pivot output name "]')].map(input=>({label:input.getAttribute('aria-label'),value:input.value,disabled:input.disabled}));`);
    assert.equal(inputs.length, 1, `The direct status Pivot must expose one category output name: ${JSON.stringify(inputs)}`);
    assert.equal(inputs[0].disabled, false, 'Native Pivot output name must be editable');
    const selector = `input[aria-label=${JSON.stringify(inputs[0].label)}]`;
    await click(browser.cdp, selector);
    await browserEval(browser.cdp, `document.activeElement.select();return true;`);
    await browser.cdp.send('Input.insertText', { text: name });
    assert.equal(await browserEval(browser.cdp, `return document.activeElement.value;`), name, 'Native typing must set the exact Pivot physical output name');
  };
  const configurePivot = async () => {
    await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-pivot-rows"]')?.disabled === false`);
    await click(browser.cdp, '[data-testid="construction-action-pivot-rows"]');
    await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Pivot category field"]') || document.body.innerText.includes('Coded values as columns')`);
    const ordinaryPivotOpen = await browserEval(browser.cdp, `return Boolean(document.querySelector('select[aria-label="Pivot category field"]'));`);
    if (!ordinaryPivotOpen) {
      await click(browser.cdp, 'button', { name: 'Change row operation' });
      await click(browser.cdp, '[data-testid="construction-reshape-choice-pivot"]');
    }
    await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')`);
    const groupLabels = await browserEval(browser.cdp, `return [...document.querySelectorAll('input[aria-label^="Pivot group "]')].map(input=>({label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled}));`);
    const expectedGroupLabel = 'Pivot group Observation FHIR resource ID';
    assert(groupLabels.some(group => group.label === expectedGroupLabel), `Pivot editor lacks the Observation ID group key: ${JSON.stringify(groupLabels)}`);
    for (const group of groupLabels) {
      const selector = `input[aria-label=${JSON.stringify(group.label)}]`;
      assert(!group.disabled, `Pivot group key is disabled: ${group.label}`);
      const shouldBeChecked = group.label === expectedGroupLabel;
      if (group.checked !== shouldBeChecked) await click(browser.cdp, selector);
    }
    await chooseField('Pivot category field', 'Observation.status');
    const started = Date.now();
    await chooseField('Pivot values field', 'Observation.id');
    return started;
  };
  const assertPivotPreview = (preview, label) => {
    assert(preview, `${label} must include a protocol preview`);
    assert.equal(preview.outputId, outputId);
    assert.equal(preview.rowCount, members.length, `${label} must retain exactly the two selected source groups`);
    const values = preview.rows.map(row => preview.columns.map(column => row[column.column]));
    const sorted = rows => rows.map(row => JSON.stringify(row)).sort();
    assert.deepEqual(sorted(values), sorted(expectedRows), `${label} cells must exactly equal the raw two-member Pivot oracle`);
    assert(preview.rows.every(row => typeof row.__loom_row_id === 'string' && row.__loom_row_id.length > 0));
  };
  const pivotFromResponse = (response, expectedCategoryOutputName = null) => {
    const pivotStep = response.candidateConstruction?.steps?.find(step => step.operation.kind === 'PIVOT');
    assert(pivotStep, 'Native proposal must contain the direct ordinary Pivot step');
    const pivot = pivotStep.operation.pivot;
    assert.equal(typeof pivot.constructionId, 'string');
    assert.equal(pivot.groupKeyIds.length, 1, 'Direct Pivot must group by exactly the witnessed Observation ID');
    const group = pivotStep.outputs.find(column => column.id === pivot.groupKeyIds[0]);
    assert(group?.label?.includes('Observation FHIR resource ID'), `Pivot group identity must remain Observation.id: ${JSON.stringify(pivotStep.outputs)}`);
    assert.deepEqual(pivot.categories.map(category => category.key), [categoryKey], 'Native category domain must exactly equal the typed raw CDA status domain');
    const categoryOutput = pivotStep.outputs.find(column => column.id === pivot.categories[0].outputColumnId);
    assert(categoryOutput, 'The typed raw status category must resolve to its declared Pivot output');
    if (expectedCategoryOutputName !== null) assert.equal(categoryOutput.name, expectedCategoryOutputName, 'Native proposal must retain the exact requested physical output name');
    assert(response.preview.columns.some(column => column.column === categoryOutput.name), 'Protocol preview must contain the declared physical category output column');
    const expectedIDs = members.map(member => JSON.stringify(['GROUPED_PIVOT', pivot.constructionId, ['STRING', member.id]])).sort();
    const actualIDs = response.preview.rows.map(row => row.__loom_row_id).sort();
    assert.deepEqual(actualIDs, expectedIDs, 'Pivot identities must encode the typed raw Observation IDs');
    assertPivotPreview(response.preview, 'Native Pivot proposal');
    return { stepID: pivotStep.id, constructionId: pivot.constructionId, rowIDs: expectedIDs, categoryOutputName: categoryOutput.name };
  };
  const rowIDFor = (identity, id) => JSON.stringify(['GROUPED_PIVOT', identity.constructionId, ['STRING', id]]);
  const assertPreviewCells = (preview, rows, label) => {
    assert(preview, `${label} must include a protocol preview`);
    assert.equal(preview.outputId, outputId);
    const actual = preview.rows.map(row => preview.columns.map(column => row[column.column]));
    const sorted = values => values.map(row => JSON.stringify(row)).sort();
    assert.deepEqual(sorted(actual), sorted(rows), `${label} must exactly match the independently queried raw source rows`);
  };
  const waitForPreview = async (started, label) => {
    const deadline = started + 5000;
    while (true) {
      await Promise.all([...pendingNetworkReads]);
      const request = report.nativeRequests.findLast(entry => entry.path === base + '/preview' && entry.startedAt >= started && entry.completedAt && entry.status === 200 && entry.response);
      if (request) return request.response;
      assert(Date.now() < deadline, `${label} did not receive a fresh saved Preview within five seconds`);
      await new Promise(resolve => setTimeout(resolve, 50));
    }
  };
  const rowsWithInspectorNumbers = async () => browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>({rowNumber:Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/^Inspect row (\\d+) identity$/)?.[1]),cells:[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())})).filter(row=>Number.isInteger(row.rowNumber));`);
  const inspect = async (id, expectedRowID, savedPreview, label) => {
    const rows = await rowsWithInspectorNumbers();
    const visible = rows.find(row => row.cells[0] === id);
    assert(visible, `${label}: raw Observation ${id} must have a mounted Pivot group row`);
    assert(visible.rowNumber > 0 && visible.rowNumber <= 25);
    const started = Date.now();
    const before = report.nativeRequests.length;
    await click(browser.cdp, `button[aria-label="Inspect row ${visible.rowNumber} identity"]`);
    const dialog = `[role="dialog"][aria-label="Row ${visible.rowNumber} identity"]`;
    await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(dialog)})`);
    await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(dialog + ' ul li')})`);
    const deadline = started + 5000;
    const lineageRequests = () => report.nativeRequests.slice(before).filter(request => request.path === base + '/row-lineage');
    while (!lineageRequests().some(request => request.completedAt && request.response)) {
      assert(Date.now() < deadline, `${label} did not complete native row-lineage within five seconds`);
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    await Promise.all([...pendingNetworkReads]);
    assert(Date.now() - started <= 5000, `${label} inspector action exceeded five seconds`);
    const requests = lineageRequests();
    assert.equal(requests.length, 1, 'One direct Pivot row must use one bounded native lineage page');
    const request = requests[0];
    const selectedRowIdentity = await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(dialog + ' p.font-mono')})?.textContent;`);
    assert.equal(selectedRowIdentity, expectedRowID, `${label} inspector must use the independently computed typed Pivot identity`);
    assert.equal(request.status, 200);
    assert.equal(request.body.outputId, outputId);
    assert.equal(request.body.rowId, expectedRowID);
    assert.equal(request.body.receiptId, savedPreview.receiptId, 'Native source inspection must bind to the current saved Preview receipt');
    assert.equal(request.body.offset ?? 0, 0);
    assert.equal(request.body.limit, 25);
    assert.equal(request.response.receiptId, savedPreview.receiptId);
    assert.equal(request.response.outputId, outputId);
    assert.equal(request.response.rowId, expectedRowID);
    assert.equal(request.response.hasMore, false);
    assert.equal(request.response.nextOffset ?? null, null);
    const contributors = request.response.contributors.map(item => [item.resourceType, item.resourceId, item.occurrenceKey]);
    assert.deepEqual(contributors, [expectedContributorByID.get(id)], `${label} source tuples must exactly match the independent raw CDA member witness`);
    const visibleContributors = await browserEval(browser.cdp, `return [...document.querySelectorAll(${JSON.stringify(dialog + ' ul li')})].map(item=>item.innerText.trim());`);
    assert.equal(visibleContributors.length, 1);
    assert(visibleContributors[0].includes(`Observation/${id}`), visibleContributors[0]);
    report.cases.push({ name: label, elapsedMs: Date.now() - started, rowID: expectedRowID, receiptId: savedPreview.receiptId, contributors });
    await browserEval(browser.cdp, `const button=[...document.querySelectorAll(${JSON.stringify(dialog + ' button')})].find(item=>item.innerText==='Close');button.dataset.qaDirectPivotClose=${JSON.stringify(label)};return true;`);
    await click(browser.cdp, `[data-qa-direct-pivot-close=${JSON.stringify(label)}]`);
    await waitForBrowser(browser.cdp, `!document.querySelector(${JSON.stringify(dialog)})`);
  };

  await open();
  await assertMountedRows(baselineRows, 'direct-pivot-starting-table');
  const configureStarted = await configurePivot();
  await typePivotOutputName('null');
  const proposalResponse = await proposal('direct-pivot-source-preview', configureStarted, expectedRows);
  pivotFromResponse(proposalResponse, 'null');
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  const cancelState = await api(base + '/builder');
  assert.deepEqual(cancelState.workspace, sourceWorkspace, 'Cancel must preserve the exact saved source workspace');
  assert.equal(cancelState.draftVersion, sourceDraftVersion, 'Cancel must preserve the source draft version');
  assert.equal(cancelState.draftDigest, sourceDraftDigest, 'Cancel must preserve the source draft digest');
  await assertMountedRows(baselineRows, 'direct-pivot-cancel-preserves-source');
  const confirmedStarted = await configurePivot();
  await typePivotOutputName('null');
  const confirmedProposal = await proposal('confirmed-direct-pivot-source-preview', confirmedStarted, expectedRows);
  const identity = pivotFromResponse(confirmedProposal, 'null');
  const applyStarted = Date.now();
  await apply(expectedRows);
  builder = await api(base + '/builder');
  const authoredWorkspace = builder.workspace;
  const authoredDraftVersion = builder.draftVersion;
  const authoredDraftDigest = builder.draftDigest;
  const authoredRows = doc(builder).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(authoredRows && authoredRows.id === identity.stepID, 'Apply must persist the exact native Pivot stage inspected in the proposal');
  const authoredCategoryOutput = authoredRows.outputs.find(column => column.id === authoredRows.operation.pivot.categories[0].outputColumnId);
  assert.equal(authoredCategoryOutput?.name, 'null', 'The native Pivot must persist the exact reserved-word physical output name');
  const appliedPreview = await waitForPreview(applyStarted, 'applied direct Pivot');
  const appliedRowIDs = appliedPreview.rows.map(row => row.__loom_row_id).sort();
  assert.deepEqual(appliedRowIDs, identity.rowIDs, 'Apply must preserve exact typed Pivot row identities');
  assertPivotPreview(appliedPreview, 'Applied direct Pivot');
  assert(appliedPreview.columns.some(column => column.column === 'null'), 'Saved Preview must expose the physical Pivot output named null');
  const firstOpenStarted = Date.now();
  await open();
  await assertMountedRows(expectedRows, 'direct-pivot-first-load');
  const firstOpenPreview = await waitForPreview(firstOpenStarted, 'first direct Pivot load');
  assert.deepEqual(firstOpenPreview.rows.map(row => row.__loom_row_id).sort(), identity.rowIDs);
  for (const member of members) {
    const rowID = rowIDFor(identity, member.id);
    await inspect(member.id, rowID, firstOpenPreview, `direct-pivot-inspect-${member.id}`);
  }
  assert.deepEqual((await api(base + '/builder')).workspace, authoredWorkspace, 'Source inspection must not mutate the saved direct Pivot');

  const reloadStarted = Date.now();
  builder = await api(base + '/builder');
  await open();
  await assertMountedRows(expectedRows, 'direct-pivot-reload');
  const reloadedPreview = await waitForPreview(reloadStarted, 'reloaded direct Pivot');
  assert.deepEqual(reloadedPreview.rows.map(row => row.__loom_row_id).sort(), identity.rowIDs, 'Reload must retain exact typed Pivot row identities');
  assertPivotPreview(reloadedPreview, 'Reloaded direct Pivot');
  for (const member of members) {
    const rowID = rowIDFor(identity, member.id);
    await inspect(member.id, rowID, reloadedPreview, `direct-pivot-reload-inspect-${member.id}`);
  }
  assert.deepEqual((await api(base + '/builder')).workspace, authoredWorkspace, 'Reloaded source inspection must leave the saved Pivot unchanged');

  const editCancelStarted = Date.now();
  await click(browser.cdp, `[data-testid="construction-history-step-${identity.stepID}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid=${JSON.stringify(`construction-edit-step-${identity.stepID}`)}]')?.disabled === false`);
  await click(browser.cdp, `[data-testid="construction-edit-step-${identity.stepID}"]`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot output name "]'))`);
  await typePivotOutputName('direct_status');
  const editCancelProposal = await proposal('direct-pivot-output-name-cancel-preview', editCancelStarted, expectedRows);
  const editCancelIdentity = pivotFromResponse(editCancelProposal, 'direct_status');
  assert.equal(editCancelIdentity.constructionId, identity.constructionId, 'Editing the physical column name must preserve the Pivot row identity construction');
  assert.deepEqual(editCancelIdentity.rowIDs, identity.rowIDs, 'Previewed name edits must preserve the independently typed Pivot row IDs');
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  const afterEditCancel = await api(base + '/builder');
  assert.deepEqual(afterEditCancel.workspace, authoredWorkspace, 'Canceling a Pivot name edit must preserve the exact saved null-name workspace');
  assert.equal(afterEditCancel.draftVersion, authoredDraftVersion, 'Canceling a Pivot name edit must preserve draft version');
  assert.equal(afterEditCancel.draftDigest, authoredDraftDigest, 'Canceling a Pivot name edit must preserve draft digest');
  const stillNullStep = doc(afterEditCancel).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert.equal(stillNullStep.outputs.find(column => column.id === stillNullStep.operation.pivot.categories[0].outputColumnId)?.name, 'null');
  builder = afterEditCancel;
  await assertMountedRows(expectedRows, 'direct-pivot-name-edit-cancel-preserves-null-output');

  const editApplyStarted = Date.now();
  await click(browser.cdp, `[data-testid="construction-history-step-${identity.stepID}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid=${JSON.stringify(`construction-edit-step-${identity.stepID}`)}]')?.disabled === false`);
  await click(browser.cdp, `[data-testid="construction-edit-step-${identity.stepID}"]`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot output name "]'))`);
  await typePivotOutputName('direct_status');
  const editApplyProposal = await proposal('direct-pivot-output-name-apply-preview', editApplyStarted, expectedRows);
  const editedIdentity = pivotFromResponse(editApplyProposal, 'direct_status');
  assert.equal(editedIdentity.stepID, identity.stepID, 'Editing the Pivot must retain the authored stage identity');
  assert.equal(editedIdentity.constructionId, identity.constructionId, 'Editing a Pivot output name must retain its typed row identity construction');
  assert.deepEqual(editedIdentity.rowIDs, identity.rowIDs, 'Edited Pivot proposal must retain the exact typed row IDs');
  const editSaveStarted = Date.now();
  await apply(expectedRows);
  builder = await api(base + '/builder');
  const editedWorkspace = builder.workspace;
  const editedDraftVersion = builder.draftVersion;
  const editedDraftDigest = builder.draftDigest;
  const editedStep = doc(builder).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(editedStep && editedStep.id === identity.stepID);
  assert.equal(editedStep.outputs.find(column => column.id === editedStep.operation.pivot.categories[0].outputColumnId)?.name, 'direct_status', 'Apply must persist the natively edited physical output name');
  const editedPreview = await waitForPreview(editSaveStarted, 'applied direct Pivot output-name edit');
  assertPreviewCells(editedPreview, expectedRows, 'Applied output-name edit');
  assert.deepEqual(editedPreview.rows.map(row => row.__loom_row_id).sort(), editedIdentity.rowIDs);
  assert(editedPreview.columns.some(column => column.column === 'direct_status'));

  const editReloadStarted = Date.now();
  await open();
  await assertMountedRows(expectedRows, 'direct-pivot-name-edit-reload');
  const editedReloadPreview = await waitForPreview(editReloadStarted, 'reloaded direct Pivot output-name edit');
  assertPreviewCells(editedReloadPreview, expectedRows, 'Reloaded output-name edit');
  assert.deepEqual(editedReloadPreview.rows.map(row => row.__loom_row_id).sort(), editedIdentity.rowIDs);
  const reloadedEditedStep = doc(await api(base + '/builder')).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert.equal(reloadedEditedStep.outputs.find(column => column.id === reloadedEditedStep.operation.pivot.categories[0].outputColumnId)?.name, 'direct_status', 'Reload must retain the exact edited physical output name');
  for (const member of members) {
    await inspect(member.id, rowIDFor(editedIdentity, member.id), editedReloadPreview, `direct-pivot-edited-inspect-${member.id}`);
  }
  assert.deepEqual((await api(base + '/builder')).workspace, editedWorkspace, 'Edited source inspection must leave the saved Pivot unchanged');

  const removalCancelBaseline = { workspace: editedWorkspace, draftVersion: editedDraftVersion, draftDigest: editedDraftDigest };
  const cancelRemovalProposal = await proposeRemoval(identity.stepID, 'direct-pivot-removal-cancel-preview', baselineRows);
  assert(!cancelRemovalProposal.candidateConstruction.steps.some(step => step.operation.kind === 'PIVOT'), 'Pivot removal proposal must restore the source-only construction');
  assertPreviewCells(cancelRemovalProposal.preview, baselineRows, 'Pivot removal cancel proposal');
  await cancelRemoval(removalCancelBaseline, 'direct-pivot-removal-cancel', expectedRows);
  const afterRemovalCancelStep = doc(builder).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert.equal(afterRemovalCancelStep.outputs.find(column => column.id === afterRemovalCancelStep.operation.pivot.categories[0].outputColumnId)?.name, 'direct_status');

  const removalApplyStarted = Date.now();
  const applyRemovalProposal = await proposeRemoval(identity.stepID, 'direct-pivot-removal-apply-preview', baselineRows);
  assert(!applyRemovalProposal.candidateConstruction.steps.some(step => step.operation.kind === 'PIVOT'), 'Applied removal proposal must omit the Pivot stage');
  assertPreviewCells(applyRemovalProposal.preview, baselineRows, 'Pivot removal apply proposal');
  await apply(baselineRows);
  builder = await api(base + '/builder');
  const restoredWorkspace = builder.workspace;
  assert(!doc(builder).construction.steps.some(step => step.operation.kind === 'PIVOT'), 'Apply must remove the authored Pivot stage');
  const restoredPreview = await waitForPreview(removalApplyStarted, 'applied Pivot removal');
  assertPreviewCells(restoredPreview, baselineRows, 'Source rows after Pivot removal');
  const restoredReloadStarted = Date.now();
  await open();
  await assertMountedRows(baselineRows, 'direct-pivot-removal-reload-source-restoration');
  const restoredReloadPreview = await waitForPreview(restoredReloadStarted, 'reloaded source after Pivot removal');
  assertPreviewCells(restoredReloadPreview, baselineRows, 'Reloaded source after Pivot removal');
  assert.deepEqual((await api(base + '/builder')).workspace, restoredWorkspace, 'Reload must preserve the exact source-only workspace after Pivot removal');

  assert(report.nativeRequests.every(request => localOrigins.has(request.origin) && !request.authorizationHeaderPresent), 'Native direct Pivot requests must remain on the local unrestricted no-auth endpoints');
  assert.deepEqual(report.errors, [], 'Direct Pivot lifecycle and source inspection must produce no browser or HTTP errors');
  report.directPivot = { stepID: identity.stepID, constructionId: identity.constructionId, selectionRevisionId: selection.id, category: categoryKey, nullPhysicalOutputName: 'null', editedPhysicalOutputName: 'direct_status', sourceIDs: members.map(member => member.id), rowIDs: identity.rowIDs, previewRows: expectedRows, restoredSourceRows: baselineRows, finalWorkspaceHasPivot: false };
};

try {
  apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(localCDAApiContainer()));
  report.apiBuildFreeze = { initial: apiBuildFreeze.initial };
  if (directPivot) {
    await runDirectPivot();
    report.status = 'passed';
  } else {
  const repeatedWitnesses = rawQuery(repeatedLeafWitnessQuery);
  assert(repeatedWitnesses.length === 2, 'Fixture needs two distinct scoped roots with a three-stage repeated-Patient path');
  assert(repeatedWitnesses.every(row => typeof row.repeatedPathOverflow === 'boolean'), 'Witness search must report its bounded-path overflow sentinel');
  const rootDoc = repeatedWitnesses[0].root;
  const decoy = repeatedWitnesses[1].root;
  assert.notEqual(rootDoc._id, decoy._id, 'The valid decoy must be a separate nonmember root');
  const roots = [rootDoc];
  const rootParents = [{ pathKey: `member:${rootDoc._id}`, root: rootDoc, stageDocs: [], terminal: rootDoc, bridgeDocs: [] }];
  const decoyParent = [{ pathKey: `decoy:${decoy._id}`, root: decoy, stageDocs: [], terminal: decoy, bridgeDocs: [] }];
  const stage1Rows = relatedRows(rootParents, stageOneQuery);
  const decoyStage1Rows = relatedRows(decoyParent, stageOneQuery);
  const decoyStage2Rows = relatedRows(decoyStage1Rows, stageTwoQuery);
  const decoyStage3Rows = relatedRows(decoyStage2Rows, stageThreeQuery);
  const stage2Rows = relatedRows(stage1Rows, stageTwoQuery);
  const stage3Rows = relatedRows(stage2Rows, stageThreeQuery);
  assert(stage1Rows.length > 0 && stage2Rows.length > 0 && stage3Rows.length > 0, 'CDA fixture must support each admitted authored expansion stage');
  assert(decoyStage1Rows.length > 0 && decoyStage2Rows.length > 0 && decoyStage3Rows.length > 0, 'The excluded decoy must have a valid matching three-stage relationship chain');
  assert(stage2Rows.every(row => row.bridgeDocs.length === 1 && row.bridgeDocs[0]._id.startsWith('Observation/')), 'The multihop stage must retain its Observation bridge witness');
  assert(stage2Rows.every(row => row.bridgeDocs.every(bridge => bridge.project === project && bridge.dataset_generation === generation)), 'Every bridge witness must remain inside the scoped fixture');
  assert(stage2Rows.every(row => !row.stageDocs.some(source => row.bridgeDocs.some(bridge => source._id === bridge._id))), 'Observation is a traversal bridge, not a stage terminal');
  assert(stage3Rows.length <= 1000, 'Keep the admitted composed chain bounded');

  const rowValues = rows => rows.map(row => [row.root.id, 'Specimen', ...row.stageDocs.map(stageDoc => stageDoc.id)]);
  const stage1PreviewRows = rowValues(stage1Rows);
  const stage2PreviewRows = rowValues(stage2Rows);
  const stage3PreviewRows = rowValues(stage3Rows);
  const repeatedLeafRows = stage3Rows.filter(row => row.stageDocs[0]._id === row.stageDocs[2]._id);
  assert(repeatedLeafRows.length > 0, 'The selected member root must have a leaf row repeating its Patient at authored stages 1 and 3');
  const leafByPreviewRow = new Map(repeatedLeafRows.map(row => [JSON.stringify(rowValues([row])[0]), row]));
  const contributorsForStages = row => [
    ['Specimen', row.root.id, row.root._key],
    ...row.stageDocs.map(stageDoc => [
      stageDoc._id.slice(0, stageDoc._id.indexOf('/')),
      stageDoc.id,
      stageDoc._key,
    ]),
  ];
  let targetLeaf;
  let expectedTuples;
  report.oracle = {
    discoveryBounds: { candidateRoots: rootDiscoveryLimit, witnessPathsPerRoot: witnessPathsLimit,
      selectedWitnessOverflow: repeatedWitnesses.map(row => ({ rootID: row.root._id, overflow: row.repeatedPathOverflow })) },
    memberRootIDs: roots.map(row => row._id), decoyRootID: decoy._id,
    admittedRowsByStage: [stage1Rows.length, stage2Rows.length, stage3Rows.length],
    repeatedPatientLeafCandidates: repeatedLeafRows.map(row => ({ rootID: row.root._id, terminalIDs: row.stageDocs.map(item => item._id), bridgeIDs: row.bridgeDocs.map(item => item._id) })),
    queries: oracleQueries,
  };

  await api(explorersPath, { name: explorer, title: 'Composed row lineage inspector regression' });
  builder = await api(base + '/builder');
  assert.equal(builder.workspace?.documents?.length ?? 0, 0, 'Verifier must own a fresh empty explorer');
  assert.equal(builder.catalog.generation, generation);
  const node = builder.catalog.nodes.find(item => item.resourceType === 'Specimen');
  assert(node, 'Catalog must expose the Specimen row root');
  await command([{ type: 'CREATE_TABLE', title: 'Composed row lineage', rootNodeId: node.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  for (const [fieldPath, title] of [['id', 'FHIR resource ID'], ['resourceType', 'Record type']]) {
    const candidate = builder.catalog.candidates.find(item => item.nodeId === node.nodeId && item.fieldPath === fieldPath);
    assert(candidate, `Catalog must expose ${fieldPath}`);
    await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: candidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title }]);
  }
  const selection = await api(base.replace('/authoring/v2', '/selections'), {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: roots.map(row => ({ project, generation, resourceType: 'Specimen', id: row.id })) } },
  });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find(choice => choice.route.length === 0);
  assert(direct, 'Exact explicit member selection must have a direct population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  const sourceWorkspace = builder.workspace;
  const sourceDraftVersion = builder.draftVersion;
  const sourceDraftDigest = builder.draftDigest;

  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.consoleAPICalled', event => { if (event.type === 'error') report.errors.push({ kind: 'console', args: event.args }); });
  browser.cdp.on('Runtime.exceptionThrown', event => report.errors.push({ kind: 'runtime', details: event.exceptionDetails }));
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, wallTime }) => {
    const requestURL = new URL(request.url);
    const path = requestURL.pathname;
    if (!path.startsWith(base + '/')) return;
    const authorizationHeaderPresent = Object.keys(request.headers ?? {}).some(header => header.toLowerCase() === 'authorization');
    const entry = { requestId, path, method: request.method, origin: requestURL.origin, authorizationHeaderPresent, startedAt: Math.round(wallTime * 1000) };
    if (request.postData) entry.body = JSON.parse(request.postData);
    nativeById.set(requestId, entry); report.nativeRequests.push(entry);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
    const entry = nativeById.get(requestId);
    if (entry) entry.status = response.status;
    if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) report.errors.push({ kind: 'http', status: response.status, url: response.url });
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = nativeById.get(requestId);
    if (!entry) return;
    entry.completedAt = Date.now();
    const read = browser.cdp.send('Network.getResponseBody', { requestId }).then(result => {
      const body = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      try { entry.response = JSON.parse(body); } catch { entry.response = body.slice(0, 32768); }
    }).catch(error => { entry.responseReadError = String(error); }).finally(() => pendingNetworkReads.delete(read));
    pendingNetworkReads.add(read);
  });

  await open();
  await expand({ name: 'related-stage-1-specimen-patient', targetType: 'Patient', routeLabel: 'Specimen -[subject]-> Patient', expectedRows: stage1PreviewRows });
  await open();
  await expand({ name: 'related-stage-2-patient-bridge-specimen', targetType: 'Specimen', routeLabel: 'Patient <-[subject]- Observation -[specimen]-> Specimen', expectedRows: stage2PreviewRows });
  await open();
  await expand({ name: 'related-stage-3-specimen-patient', targetType: 'Patient', routeLabel: 'Specimen -[subject]-> Patient', expectedRows: stage3PreviewRows });

  const authoredSteps = doc(builder).construction.steps;
  assert.equal(authoredSteps.length, 3, 'The fixture must contain exactly the three authored RELATED_EXPAND steps');
  assert(authoredSteps.every(step => step.operation.kind === 'RELATED_EXPAND'), 'The fixture must contain RELATED_EXPAND only');
  report.deferredGroupPivot = {
    group: 'Deferred to the next unit; this verifier stops after the three related expansions.',
    pivot: 'Deferred to the next unit; this verifier does not author a pivot.',
  };
  const authoredWorkspace = builder.workspace;

  const inspect = async (label, rowNumber, expectedContributors = expectedTuples) => {
    const started = Date.now();
    const before = report.nativeRequests.length;
    await click(browser.cdp, `button[aria-label="Inspect row ${rowNumber} identity"]`);
    const dialog = `[role="dialog"][aria-label="Row ${rowNumber} identity"]`;
    await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(dialog)})`);
    await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(dialog + ' ul li')})`);
    const deadline = Date.now() + 5000;
    const lineageRequests = () => report.nativeRequests.slice(before).filter(request => request.path === base + '/row-lineage');
    while (!lineageRequests().some(request => request.completedAt && request.response)) {
      assert(Date.now() < deadline, `${label} native row-lineage request did not complete within five seconds`);
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    assert(Date.now() - started <= 5000, `${label} native row-lineage action exceeded five seconds`);
    const initialDialogText = await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(dialog)}).innerText;`);
    assert(!/cannot be listed|unavailable|could not be fully listed|Could not load/i.test(initialDialogText), initialDialogText);
    await Promise.all([...pendingNetworkReads]);
    const requests = lineageRequests();
    assert.equal(requests.length, 1, `${expectedContributors.length} contributors must fit on one native inspector page`);
    assert.equal(await browserEval(browser.cdp, `return [...document.querySelectorAll(${JSON.stringify(dialog + ' button')})].some(button=>button.innerText==='Show more source records');`), false);
    const selectedRowIdentity = await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(dialog + ' p.font-mono')})?.textContent;`);
    assert(selectedRowIdentity, 'Native inspector must show the exact canonical row identity');
    const allTuples = [];
    for (let index = 0; index < requests.length; index += 1) {
      const request = requests[index];
      const response = request.response;
      assert.equal(request.status, 200);
      assert.equal(request.body.outputId, outputId);
      assert.equal(request.body.rowId, selectedRowIdentity);
      assert(request.body.receiptId, 'Native row-lineage call must be receipt bound');
      assert.equal(request.body.limit, 25, 'Native inspector must use the existing bounded page size');
      assert.equal(response.receiptId, request.body.receiptId);
      assert.equal(response.outputId, outputId);
      assert.equal(response.rowId, selectedRowIdentity);
      const offset = request.body.offset ?? 0;
      const page = response.contributors.map(item => [item.resourceType, item.resourceId, item.occurrenceKey]);
      assert.equal(offset, 0, 'The first native inspector page must start at offset zero');
      assert.equal(page.length, expectedContributors.length);
      assert.equal(response.hasMore, false, 'The selected lineage must not require paging');
      assert.equal(response.nextOffset ?? null, null);
      allTuples.push(...page);
    }
    assert.deepEqual(allTuples, expectedContributors, 'Native response must exactly match the independent scoped oracle, including repeated authored occurrences');
    const visible = await browserEval(browser.cdp, `return [...document.querySelectorAll(${JSON.stringify(dialog + ' ul li')})].map(item=>item.innerText.trim());`);
    assert.equal(visible.length, expectedContributors.length);
    const expectedDisplayCounts = new Map();
    for (const [resourceType, resourceId] of expectedContributors) {
      const label = `${resourceType}/${resourceId}`;
      expectedDisplayCounts.set(label, (expectedDisplayCounts.get(label) ?? 0) + 1);
    }
    for (const [record, count] of expectedDisplayCounts) {
      assert.equal(visible.filter(line => line.includes(record)).length, count, `Inspector must display each source occurrence for ${record}`);
    }
    assert(!visible.some(line => line.includes('Observation/')), 'The multihop Observation bridge must stay out of the inspector');
    const captured = requests.map(request => ({ request: request.body, response: request.response }));
    report.cases.push({ name: label, rowNumber, elapsedMs: Date.now() - started, pageCount: requests.length, rowId: selectedRowIdentity, calls: captured });
    await browserEval(browser.cdp, `const button=[...document.querySelectorAll(${JSON.stringify(dialog + ' button')})].find(item=>item.innerText==='Close');button.dataset.qaComposedClose=${JSON.stringify(label)};return true;`);
    await click(browser.cdp, `[data-qa-composed-close=${JSON.stringify(label)}]`);
    await waitForBrowser(browser.cdp, `!document.querySelector(${JSON.stringify(dialog)})`);
    return selectedRowIdentity;
  };

  const rowNumberFor = async (rowMap, label) => {
    const visibleRows = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>({rowNumber:Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/^Inspect row (\\d+) identity$/)?.[1]),cells:[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())}));`);
    const match = visibleRows.find(row => rowMap.has(JSON.stringify(row.cells)));
    assert(match, `${label} raw-oracle row must be visible in the saved preview`);
    assert(Number.isInteger(match.rowNumber) && match.rowNumber > 0 && match.rowNumber <= 25, 'The selected native inspector row must be within the bounded first preview page');
    return { rowNumber: match.rowNumber, row: rowMap.get(JSON.stringify(match.cells)) };
  };
  const targetRowNumber = async () => {
    const match = await rowNumberFor(leafByPreviewRow, 'Repeated-Patient leaf');
    targetLeaf = match.row;
    expectedTuples = contributorsForStages(targetLeaf);
    assert.equal(targetLeaf.stageDocs.length, 3);
    assert.equal(targetLeaf.bridgeDocs.length, 1);
    assert(!targetLeaf.stageDocs.some(source => source._id === targetLeaf.bridgeDocs[0]._id), 'The Observation bridge must not be a leaf contributor');
    assert.equal(expectedTuples.length, 4, 'A leaf row must have its root plus one terminal from each authored RELATED_EXPAND stage');
    assert.equal(expectedTuples[1][1], expectedTuples[3][1], 'Repeated Patient must remain present at stages 1 and 3');
    assert.equal(expectedTuples[1][2], expectedTuples[3][2], 'Repeated Patient occurrences retain the same FHIR record key');
    assert(!expectedTuples.some(([resourceType]) => resourceType === 'Observation'), 'Route-only Observation bridges must not be contributors');
    report.oracle.targetLeaf = { rootID: targetLeaf.root._id, terminalIDs: targetLeaf.stageDocs.map(item => item._id), bridgeIDs: targetLeaf.bridgeDocs.map(item => item._id) };
    report.oracle.expectedContributors = expectedTuples;
    return match.rowNumber;
  };
  await open();
  const firstRowNumber = await targetRowNumber();
  const selectedLeafPath = targetLeaf.pathKey;
  const rowIdentity = await inspect('composed-related-first-load', firstRowNumber);
  assert.deepEqual((await api(base + '/builder')).workspace, authoredWorkspace, 'Inspection must not mutate the saved three-step workspace');
  await open();
  const reloadedRowNumber = await targetRowNumber();
  assert.equal(targetLeaf.pathKey, selectedLeafPath, 'Reload must resolve to the same raw-oracle leaf path');
  assert.equal(reloadedRowNumber, firstRowNumber, 'The selected leaf row must remain at the same preview position after reload');
  assert.equal(await inspect('composed-related-reload', reloadedRowNumber), rowIdentity, 'Row identity must survive reload');
  assert.deepEqual((await api(base + '/builder')).workspace, authoredWorkspace, 'Reloaded inspection must not mutate the saved workspace');

  const authoredConstruction = doc(builder).construction;
  const authoredStepIDs = authoredConstruction.steps.map(step => step.id);
  assert.deepEqual(authoredStepIDs, authoredSteps.map(step => step.id));
  const lastStepID = authoredStepIDs.at(-1);
  const twoStepConstruction = { ...authoredConstruction, steps: authoredConstruction.steps.slice(0, 2) };
  const stage2Leaf = stage2Rows.find(row => targetLeaf.pathKey.startsWith(`${row.pathKey}/`) &&
    row.stageDocs[0]._id === targetLeaf.stageDocs[0]._id && row.stageDocs[1]._id === targetLeaf.stageDocs[1]._id);
  assert(stage2Leaf, 'The selected repeated-Patient leaf must have an exact two-step oracle ancestor');
  const stage2LeafPreview = rowValues([stage2Leaf])[0];
  const stage2Map = new Map([[JSON.stringify(stage2LeafPreview), stage2Leaf]]);
  const stage2Contributors = contributorsForStages(stage2Leaf);
  assert.equal(stage2Contributors.length, 3, 'Removing the final authored step must leave root plus two stage terminals');
  const threeStepBaseline = await api(base + '/builder');

  await open();
  const cancelledLastRemoval = await proposeRemoval(lastStepID, 'remove-stage-3-cancel-preview', stage2PreviewRows);
  assert.deepEqual([...cancelledLastRemoval.dependencyImpact.removedStepIds].sort(), [lastStepID].sort(), 'Removing the final step must not cascade into its two upstream steps');
  assert.deepEqual(cancelledLastRemoval.candidateConstruction, twoStepConstruction, 'The final-step removal proposal must retain the first two authored steps exactly');
  await cancelRemoval(threeStepBaseline, 'Final-step removal', stage3PreviewRows);

  await open();
  const appliedLastRemoval = await proposeRemoval(lastStepID, 'remove-stage-3-apply-preview', stage2PreviewRows);
  assert.deepEqual([...appliedLastRemoval.dependencyImpact.removedStepIds].sort(), [lastStepID].sort());
  assert.deepEqual(appliedLastRemoval.candidateConstruction, twoStepConstruction);
  await apply(stage2PreviewRows);
  assert.deepEqual(doc(builder).construction, twoStepConstruction, 'Applying final-step removal must save the exact two-step candidate');
  await open();
  builder = await api(base + '/builder');
  assert.deepEqual(doc(builder).construction, twoStepConstruction, 'The two-step construction must persist after reload');
  await assertMountedRows(stage2PreviewRows, 'Reloaded two-step construction');
  const stage2Target = await rowNumberFor(stage2Map, 'Two-step ancestor of selected repeated-Patient leaf');
  const stage2Identity = await inspect('composed-related-after-last-step-removal', stage2Target.rowNumber, stage2Contributors);
  assert(stage2Identity, 'The remaining two-step row must expose a stable native row identity');
  assert.deepEqual((await api(base + '/builder')).workspace, builder.workspace, 'Two-step row inspection must not mutate the saved workspace');

  const firstStepID = twoStepConstruction.steps[0].id;
  const twoStepIDs = twoStepConstruction.steps.map(step => step.id);
  const zeroStepConstruction = { ...twoStepConstruction, steps: [] };
  const basePreviewRows = [[rootDoc.id, 'Specimen']];
  const twoStepBaseline = await api(base + '/builder');
  await open();
  const cancelledCascade = await proposeRemoval(firstStepID, 'remove-stage-1-cascade-cancel-preview', basePreviewRows);
  assert.deepEqual([...cancelledCascade.dependencyImpact.removedStepIds].sort(), [...twoStepIDs].sort(), 'Removing the first step must cascade to every remaining dependent RELATED_EXPAND step');
  assert.deepEqual(cancelledCascade.candidateConstruction, zeroStepConstruction, 'The first-step cascade must propose an empty construction');
  await cancelRemoval(twoStepBaseline, 'First-step cascade removal', stage2PreviewRows);

  await open();
  const appliedCascade = await proposeRemoval(firstStepID, 'remove-stage-1-cascade-apply-preview', basePreviewRows);
  assert.deepEqual([...appliedCascade.dependencyImpact.removedStepIds].sort(), [...twoStepIDs].sort());
  assert.deepEqual(appliedCascade.candidateConstruction, zeroStepConstruction);
  await apply(basePreviewRows);
  assert.deepEqual(doc(builder).construction, zeroStepConstruction, 'Applying the cascade must save an explicit zero-step construction');
  await open();
  builder = await api(base + '/builder');
  assert.deepEqual(doc(builder).construction, zeroStepConstruction, 'The zero-step construction must persist after reload');
  await assertMountedRows(basePreviewRows, 'Reloaded zero-step construction');
  const baseMap = new Map([[JSON.stringify(basePreviewRows[0]), rootDoc]]);
  const baseTarget = await rowNumberFor(baseMap, 'Starting-record row');
  const startingInspectorStarted = Date.now();
  const beforeStartingInspector = report.nativeRequests.length;
  await click(browser.cdp, `button[aria-label="Inspect row ${baseTarget.rowNumber} identity"]`);
  const baseDialog = `[role="dialog"][aria-label="Row ${baseTarget.rowNumber} identity"]`;
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(baseDialog)})?.innerText.includes('Starting FHIR record')`);
  const baseDialogText = await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(baseDialog)}).innerText;`);
  assert(baseDialogText.includes(`Specimen/${rootDoc.id}`), 'Zero-step row inspector must identify the exact starting Specimen record');
  const baseRowIdentity = await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(baseDialog + ' p.font-mono')})?.textContent;`);
  assert(baseRowIdentity, 'Starting-record inspector must show the stable row identity');
  assert.equal(report.nativeRequests.slice(beforeStartingInspector).filter(request => request.path === base + '/row-lineage').length, 0, 'A single starting record must not request composed row lineage');
  await browserEval(browser.cdp, `const button=[...document.querySelectorAll(${JSON.stringify(baseDialog + ' button')})].find(item=>item.innerText==='Close');button.dataset.qaComposedClose='starting-record';return true;`);
  await click(browser.cdp, '[data-qa-composed-close="starting-record"]');
  await waitForBrowser(browser.cdp, `!document.querySelector(${JSON.stringify(baseDialog)})`);
  assert(Date.now() - startingInspectorStarted <= 5000, 'Starting-record inspection exceeded five seconds');
  report.cases.push({ name: 'zero-step-starting-record-inspector', elapsedMs: Date.now() - startingInspectorStarted, rowNumber: baseTarget.rowNumber, rowIdentity: baseRowIdentity });
  assert.deepEqual((await api(base + '/builder')).workspace, builder.workspace, 'Starting-record inspection must not mutate the zero-step workspace');

  assert(report.nativeRequests.every(request => localOrigins.has(request.origin) && !request.authorizationHeaderPresent), 'Native Builder requests must remain on the local no-auth endpoints');
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
  }
} catch (error) {
  report.status = error.invalidatesRun ? 'invalidated' : 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  if (apiBuildFreeze) {
    try { report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await apiBuildFreeze.assertUnchanged()) }; }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreeze = { unchanged: false, invalidatesRun: true, productFailure: false, error: String(error) };
      process.exitCode = 1;
    }
  }
  try { report.sourceFreeze = await sourceFreeze.assertUnchanged(); } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = { unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false, error: String(error) };
    process.exitCode = 1;
  }
  await Promise.all([...pendingNetworkReads]);
  report.finished = new Date().toISOString();
  await writeFile(`${evidence}/report.json`, JSON.stringify(report, null, 2));
  if (browser) await browser.close().catch(() => {});
}
