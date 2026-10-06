import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { performAction, requireUnique } from '../helpers/playwright-actions.mjs';

export async function composedRowLineageWorkflow({ page, cda, lineageMode = process.env.LOOM_COMPOSED_LINEAGE_MODE ?? 'COMPOSED_RELATED' }) {
const project = cda.project;
const generation = cda.generation;
assert.equal(generation, 'cda-fhir-v1', 'Composed row lineage requires the cda-fhir-v1 fixture generation.');
assert(['COMPOSED_RELATED', 'DIRECT_PIVOT', 'DIRECT_PIVOT_SHARED_CONTRIBUTOR', 'DIRECT_GROUP_COUNT_PIVOT'].includes(lineageMode), `Unsupported lineage mode: ${lineageMode}`);
const directPivot = lineageMode.startsWith('DIRECT_PIVOT') || lineageMode === 'DIRECT_GROUP_COUNT_PIVOT';
const sharedContributorPivot = lineageMode === 'DIRECT_PIVOT_SHARED_CONTRIBUTOR';
const groupCountPivot = lineageMode === 'DIRECT_GROUP_COUNT_PIVOT';
const pivotName = groupCountPivot ? 'group-count-pivot-lineage' : sharedContributorPivot ? 'shared-contributor-pivot-lineage' : 'direct-pivot-lineage';
const explorer = `${directPivot ? pivotName : 'composed-row-lineage'}-${Date.now()}-${randomUUID().slice(0, 8)}`;
const rootDiscoveryLimit = 2000;
const witnessPathsLimit = 2;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
assert(apiOrigin && uiOrigin, 'The CDA fixture must provide an isolated API and UI origin.');
const arangoContainer = cda.target.arangoContainer;
const localAPI = new URL(apiOrigin);
const localUI = new URL(uiOrigin);
const localOrigins = new Set([localAPI.origin, localUI.origin]);
const base = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
const explorersPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const report = cda.report;
Object.assign(report, {
  explorer, project, generation, lineageMode,
  scope: { apiOrigin: localAPI.origin, uiOrigin: localUI.origin,
    authorization: 'local no-auth unrestricted API', oracleScope: 'project + dataset generation + explicit member root IDs' },
  routeContract: groupCountPivot
    ? ['two explicitly selected Observation roots with one shared status/unit pair', 'ordinary Group by status+unit with COUNT_ROWS', 'ordinary Pivot grouped by unit, categorized by status, values from Group COUNT_ROWS']
    : directPivot ? sharedContributorPivot
      ? ['two explicitly selected Observation roots with one shared status group key', 'ordinary Pivot categorized by valueQuantity.unit and summed by valueQuantity.value']
      : ['Observation direct membership roots', 'ordinary Pivot grouped by Observation FHIR resource ID, categorized by Observation.status']
      : ['Specimen -[subject]-> Patient', 'Patient <-[subject]- Observation -[specimen]-> Specimen', 'Specimen -[subject]-> Patient'],
  cases: [], requests: [], started: new Date().toISOString(),
});
report.target = cda.target;
const requestCapture = cda.captureRequests(base);
let builder, outputId;
const oracleQueries = [];
const waitUI = (condition, timeout = 5000) => page.waitForFunction(condition, undefined, { timeout: Math.min(timeout, 5000) });
const navigateUI = url => page.goto(url, { waitUntil: 'domcontentloaded', timeout: 5000 });
const actionTarget = (selector, identity = {}) => {
  let locator = page.locator(selector);
  if (identity.name !== undefined) locator = locator.and(page.getByRole('button', { name: identity.name, exact: true }));
  if (identity.includes !== undefined) {
    const escaped = identity.includes.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    locator = locator.and(page.getByRole('button', { name: new RegExp(escaped, 'i') }));
  }
  return locator;
};
const clickUI = (selector, identity = {}) => {
  const locator = actionTarget(selector, identity);
  return performAction(report, identity.name ?? identity.includes ?? selector, locator, target => target.click({ timeout: 5000 }));
};
const fillUI = (selector, value, label = selector) => {
  const locator = page.locator(selector);
  return performAction(report, label, locator, (target, { timeout }) => target.fill(value, { timeout }), { editable: true });
};
const selectUI = async (selector, value, { settledWhen, dismissSelector } = {}) => {
  const locator = page.locator(selector);
  await performAction(report, `Select ${value}`, locator, (target, { timeout }) => target.selectOption(value, { timeout }));
  if (settledWhen) await waitUI(settledWhen, 5000);
  else await waitUI(`document.querySelector(${JSON.stringify(selector)})?.value === ${JSON.stringify(value)}`, 5000);
  if (dismissSelector) await clickUI(dismissSelector);
};
const evidenceSafe = (value, key = '') => {
  if (/authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key/i.test(key)) return '[REDACTED]';
  if (Array.isArray(value)) return value.map(item => evidenceSafe(item));
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([childKey, childValue]) => [childKey, evidenceSafe(childValue, childKey)]));
  return value;
};
const requestBody = entry => requestCapture.rawRequestBody(entry) ?? entry.body;
const responseBody = entry => requestCapture.rawResponseBody(entry) ?? entry.response;
const inspectPreviewRows = async () => page.locator('[data-testid="preview-table-scroll"] [role="row"]')
  .evaluateAll(rows => rows.slice(1).map(row => {
    const label = row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label') ?? '';
    const rowNumber = Number(label.match(/^Inspect row (\d+) identity$/)?.[1]);
    return { rowNumber, cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()) };
  }).filter(row => Number.isInteger(row.rowNumber)));
const collectPreviewRows = async () => {
  const scroll = page.getByTestId('preview-table-scroll');
  await requireUnique(scroll, 'Preview scroll area');
  await scroll.waitFor({ state: 'visible', timeout: 5000 });
  for (let pageIndex = 0; pageIndex < 100; pageIndex++) {
    const top = await scroll.evaluate(element => element.scrollTop);
    if (top <= 0) break;
    await performAction(report, 'Return preview to first row', scroll, async target => {
      await target.hover({ timeout: 5000 });
      await page.mouse.wheel(0, -Math.max(1, await target.evaluate(element => element.clientHeight)));
    });
    await page.waitForFunction(({ previousTop, selector }) => {
      const element = document.querySelector(selector);
      return element && element.scrollTop < previousTop;
    }, { previousTop: top, selector: '[data-testid="preview-table-scroll"]' }, { timeout: 5000 });
  }
  const rows = new Map();
  for (let pageIndex = 0; pageIndex < 100; pageIndex++) {
    for (const row of await inspectPreviewRows()) rows.set(row.rowNumber, row.cells);
    const position = await scroll.evaluate(element => ({ top: element.scrollTop, height: element.clientHeight, total: element.scrollHeight }));
    if (position.top + position.height >= position.total) break;
    await performAction(report, 'Scroll preview rows', scroll, async target => {
      await target.hover({ timeout: 5000 });
      await page.mouse.wheel(0, Math.max(1, Math.floor(position.height / 2)));
    });
    await page.waitForFunction(({ previousTop, selector }) => {
      const element = document.querySelector(selector);
      return element && element.scrollTop > previousTop;
    }, { previousTop: position.top, selector: '[data-testid="preview-table-scroll"]' }, { timeout: 5000 });
  }
  return [...rows.entries()].sort(([left], [right]) => left - right).map(([, cells]) => cells);
};

const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `composed-lineage-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body: evidenceSafe(body), status: response.status, response: evidenceSafe(value) });
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
  const response = await requestCapture.waitFor(request => request.path === base + '/construction-proposals'
    && request.startedAt >= started && request.status !== undefined, { timeout: Math.max(1, started + 5000 - Date.now()) });
  const rawResponse = requestCapture.rawResponseBody(response);
  await waitUI(`['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
  const panel = page.getByTestId('construction-proposal-panel');
  assert.equal(await panel.getAttribute('data-proposal-id'), rawResponse?.proposalId,
    `${name} rendered proposal must match its exact owned API response`);
  const result = await panel.evaluate(element => ({
    status: element.dataset.proposalStatus,
    text: element.innerText,
    rows: [...element.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText)),
  }));
  assert.equal(result.status, 'ready', result.text);
  assert.equal(result.rows.length, Math.min(25, expectedRows.length));
  const witnesses = new Set(expectedRows.map(row => JSON.stringify(row)));
  for (const row of result.rows) assert(witnesses.has(JSON.stringify(row)), `${name} proposal row lacks a raw source witness: ${JSON.stringify(row)}`);
  assert(Date.now() - started <= 5000, `${name} exceeded the five-second proposal bound`);
  report.cases.push({ name, elapsedMs: Date.now() - started, previewRows: result.rows.length });
  return rawResponse;
};
const apply = async expectedRows => {
  const started = Date.now();
  await clickUI('[data-testid="construction-apply-proposal"]');
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await requestCapture.waitFor(request => request.path === base + '/preview' && request.startedAt >= started && request.status === 200,
    { timeout: Math.max(1, started + 5000 - Date.now()) });
  await waitUI(`!document.body.innerText.includes('Loading your table…')`);
  const mounted = await collectPreviewRows();
  const witnessRows = new Set(expectedRows.map(row => JSON.stringify(row)));
  for (const row of mounted) assert(witnessRows.has(JSON.stringify(row)), `Mounted row is absent from the scoped raw oracle: ${JSON.stringify(row)}`);
  assert(Date.now() - started <= 5000, 'Apply-to-render exceeded five seconds');
  report.cases.push({ name: 'apply-proposal-to-render', elapsedMs: Date.now() - started });
  builder = await api(base + '/builder');
};
const open = async () => {
  const started = Date.now();
  await navigateUI(`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitUI(`document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await clickUI(`[data-testid="construction-table-${outputId}"]`);
  await waitUI(`document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await waitUI(`!document.body.innerText.includes('Loading your table…') && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')`);
  assert(Date.now() - started <= 5000, 'Reload-to-render exceeded five seconds');
  report.cases.push({ name: 'reload-saved-table', elapsedMs: Date.now() - started });
};
const assertMountedRows = async (expectedRows, label) => {
  await waitUI(`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, expectedRows.length) + 1))}`);
  const mounted = await collectPreviewRows();
  assert.equal(mounted.length, Math.min(25, expectedRows.length), `${label} must render the complete bounded preview`);
  if (expectedRows.length <= 25) assert.deepEqual(mounted.map(row => JSON.stringify(row)).sort(), expectedRows.map(row => JSON.stringify(row)).sort(), `${label} exact rendered rows`);
  const witnesses = new Set(expectedRows.map(row => JSON.stringify(row)));
  for (const row of mounted) assert(witnesses.has(JSON.stringify(row)), `${label} mounted row is absent from the scoped raw oracle: ${JSON.stringify(row)}`);
};
const proposeRemoval = async (stepID, name, expectedRows) => {
  const started = Date.now();
  await clickUI(`[data-testid="construction-history-step-${stepID}"]`);
  await waitUI(`document.querySelector('[data-testid=${JSON.stringify(`construction-remove-step-${stepID}`)}]')?.disabled === false`);
  await clickUI(`[data-testid="construction-remove-step-${stepID}"]`);
  return proposal(name, started, expectedRows);
};
const cancelRemoval = async (baseline, name, expectedRows) => {
  const started = Date.now();
  await clickUI('[data-testid="construction-cancel-proposal"]');
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
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
  await clickUI('[data-testid="construction-rows-settings-trigger"]');
  await waitUI(`document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false`);
  await clickUI('[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitUI(`document.querySelector(${JSON.stringify(`${panel} select[aria-label="Related record type"]`)})?.disabled === false`);
  await selectUI(`${panel} select[aria-label="Related record type"]`, targetType);
  const selector = `input[aria-label=${JSON.stringify(label)}]`;
  await waitUI(`Boolean(document.querySelector(${JSON.stringify(selector)}))`);
  const visible = await page.locator(selector).evaluate(item => Boolean(!item.closest('details:not([open])') && item.getBoundingClientRect().height > 0));
  if (!visible) {
    await clickUI(`${panel} [data-testid="construction-related-expand-other-routes"] summary`);
  }
  await waitUI(`document.querySelector(${JSON.stringify(selector)})?.disabled === false`, 5000);
  await clickUI(selector);
  await selectUI(`${panel} select[aria-label="If a current row has no matches"]`, 'EXCLUDE');
};
const expand = async ({ name, targetType, routeLabel, expectedRows }) => {
  const started = Date.now();
  await chooseRoute(targetType, routeLabel);
  await proposal(name, started, expectedRows);
  await apply(expectedRows);
};

const runDirectGroupCountPivot = async () => {
  const sourceQuery = `FOR observation IN Observation
  FILTER ${scopedDocument('observation')}
    AND IS_STRING(observation.payload.status) AND observation.payload.status != ""
    AND IS_STRING(observation.payload.valueQuantity.unit) AND observation.payload.valueQuantity.unit != ""
  COLLECT status = observation.payload.status, unit = observation.payload.valueQuantity.unit
    AGGREGATE memberCount = COUNT()
  FILTER memberCount >= 2
  SORT status, unit
  LIMIT 1
  LET members = (
    FOR candidate IN Observation
      FILTER ${scopedDocument('candidate')}
        AND candidate.payload.status == status
        AND candidate.payload.valueQuantity.unit == unit
      SORT candidate._key
      LIMIT 2
      RETURN {
        id: candidate.id, _id: candidate._id, _key: candidate._key,
        status: candidate.payload.status, unit: candidate.payload.valueQuantity.unit,
        project: candidate.project, dataset_generation: candidate.dataset_generation
      }
  )
  FILTER LENGTH(members) == 2
  RETURN {status, unit, members}`;
  const candidates = rawQuery(sourceQuery);
  assert.equal(candidates.length, 1, 'The scoped raw oracle must choose one status/unit pair with two numeric Observation roots');
  const { status: categoryValue, unit: unitValue, members } = candidates[0];
  assert.equal(members.length, 2);
  assert(members.every(member => member.status === categoryValue && member.unit === unitValue
    && member.project === project && member.dataset_generation === generation), 'Raw members must share the exact category/unit and CDA scope');
  assert.notEqual(members[0]._id, members[1]._id);
  const sourceRows = members.map(member => [member.id, member.status, member.unit]);
  const groupedRows = [[categoryValue, unitValue, '2']];
  const pivotRows = [[unitValue, '2']];
  report.oracle = {
    query: sourceQuery,
    scope: 'project + dataset generation + explicit exact two-member Observation selection',
    memberCount: members.length, members, category: { kind: 'STRING', string: categoryValue },
    unit: { kind: 'STRING', string: unitValue },
    sourceRows, groupedRows, pivotRows,
    expectedContributors: members.map(member => ['Observation', member.id, member._key]),
    rawQueryLog: oracleQueries,
  };

  await api(explorersPath, { name: explorer, title: 'Direct Group COUNT_ROWS to Pivot lineage QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.workspace?.documents?.length ?? 0, 0, 'Verifier must own a fresh empty Explorer');
  assert.equal(builder.catalog.generation, generation);
  const observationNode = builder.catalog.nodes.find(candidate => candidate.resourceType === 'Observation');
  assert(observationNode, 'Catalog must expose Observation as a direct source root');
  await command([{ type: 'CREATE_TABLE', title: 'Direct Group COUNT_ROWS to Pivot', rootNodeId: observationNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const fieldCandidate = path => {
    const candidate = builder.catalog.candidates.find(item => item.nodeId === observationNode.nodeId && item.fieldPath === path);
    assert(candidate, `Direct Group COUNT_ROWS to Pivot requires root field ${path}`);
    return candidate;
  };
  const candidatesByPath = new Map(['id', 'status', 'valueQuantity.unit'].map(path => [path, fieldCandidate(path)]));
  await command([...candidatesByPath].map(([path, candidate]) => ({
    type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: candidate.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: candidate.label,
  })));
  const sourceColumnFor = path => {
    const column = doc(builder).columns.find(item => item.source.kind === 'field' && item.source.field.path === path);
    assert(column, `Saved source column ${path} must preserve its root field binding before composition`);
    return column;
  };
  const withoutLocalSourceIDs = columns => columns.map(({ columnId, ...column }) => column);
  const sourceColumns = new Map([...candidatesByPath.keys()].map(path => [path, sourceColumnFor(path)]));
  const selection = await api(base.replace('/authoring/v2', '/selections'), {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: members.map(member => ({ project, generation, resourceType: 'Observation', id: member.id })) } },
  });
  const routes = await api(base + '/population-routes', { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const directRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(directRoute, 'The exact two-member Observation selection must provide a direct root route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  const rootWorkspace = builder.workspace;
  const rootDraftVersion = builder.draftVersion;
  const rootDraftDigest = builder.draftDigest;



  const currentRenderedPreview = async (started, expectedPreview, label) => {
    assert(builder?.draftVersion && builder?.draftDigest, `${label} requires the saved Builder draft identity`);
    const deadline = started + 5000;
    const ready = `(() => {const p=document.querySelector('[data-testid="construction-preview"]');return p?.dataset.previewStatus==='ready'&&p?.dataset.previewOutputId===${JSON.stringify(outputId)}&&p?.dataset.currentDraftVersion===${JSON.stringify(String(builder.draftVersion))}&&p?.dataset.currentDraftDigest===${JSON.stringify(builder.draftDigest)}&&Boolean(p?.dataset.previewReceiptId);})()`;
    await waitUI(ready, 5000);
    const active = await page.getByTestId('construction-preview').evaluate(p => ({ status: p.dataset.previewStatus, receiptId: p.dataset.previewReceiptId, outputId: p.dataset.previewOutputId, draftVersion: p.dataset.currentDraftVersion, draftDigest: p.dataset.currentDraftDigest }));
    const findActiveRequest = () => report.nativeRequests.findLast(entry => entry.path === base + '/preview'
      && entry.startedAt >= started && (entry.body?.receiptId === active.receiptId || entry.response?.receiptId === active.receiptId));
    let request = findActiveRequest();
    if (request && (!request.completedAt || !request.response)) {
      request = await requestCapture.waitFor(entry => entry.path === base + '/preview' && entry.startedAt >= started
        && (requestBody(entry)?.receiptId === active.receiptId || responseBody(entry)?.receiptId === active.receiptId),
      { timeout: Math.max(1, deadline - Date.now()) });
    }
    await requestCapture.flush();
    let preview;
    let source;
    if (request) {
      assert.equal(request.status, 200, `${label} active Preview request failed`);
      preview = responseBody(request);
      source = 'native-browser-preview';
    } else if (expectedPreview?.receiptId === active.receiptId) {
      preview = expectedPreview;
      source = 'accepted-proposal-preview-reused';
    } else {
      preview = await api(base + '/preview', { receiptId: active.receiptId, outputId, limit: 25 });
      source = 'direct-read-of-rendered-receipt';
    }
    assert.equal(preview.receiptId, active.receiptId, `${label} payload must match the rendered receipt`);
    assert.equal(preview.outputId, outputId);
    assert(Date.now() - started <= 5000, `${label} reload-to-current-preview exceeded five seconds`);
    report.cases.push({ name: `${label}-current-preview`, elapsedMs: Date.now() - started, receiptId: active.receiptId, source });
    return preview;
  };
  const capabilitiesFor = async (stageLabels, label) => {
    const request = await requestCapture.waitFor(entry => entry.path === base + '/construction-capabilities'
      && entry.status === 200 && stageLabels.every(wanted => responseBody(entry)?.selectedStage?.columns?.some(column => column.label === wanted)),
    { timeout: 5000 });
    return responseBody(request);
  };
  const assertPreviewByOutputs = (preview, step, expected, labels, label) => {
    assert(preview, `${label} requires protocol preview`);
    assert.equal(preview.outputId, outputId);
    assert.equal(preview.rowCount, expected.length, `${label} row count differs from its independent raw oracle`);
    const outputColumns = labels.map(outputLabel => {
      const output = step.outputs.find(column => column.label === outputLabel);
      assert(output, `${label} construction lacks output ${outputLabel}`);
      const previewColumn = preview.columns.find(column => column.column === output.name);
      assert(previewColumn, `${label} preview lacks physical output ${output.name}`);
      return previewColumn.column;
    });
    const actualRows = preview.rows.map(row => outputColumns.map(column => String(row[column])));
    assert.deepEqual(actualRows.map(row => JSON.stringify(row)).sort(), expected.map(row => JSON.stringify(row)).sort(), `${label} values differ from raw source witnesses`);
    return actualRows;
  };
  const waitProposalResponse = async (started, expectedRows, label) => proposal(label, started, expectedRows);
  const applyAcceptedProposal = async (proposalResponse, expectedRows, label) => {
    const started = Date.now();
    assert.equal(proposalResponse.outputId, outputId, `${label} proposal belongs to a different output`);
    assert(proposalResponse.proposalId && proposalResponse.preview?.receiptId, `${label} requires an exact accepted proposal preview`);
    assert.equal(proposalResponse.preview.receiptId, proposalResponse.proposalId, `${label} proposal preview receipt must match its proposal`);
    await clickUI('[data-testid="construction-apply-proposal"]');
    await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
    await requestCapture.waitFor(entry => entry.path === base + '/commands' && entry.startedAt >= started && entry.status === 200,
      { timeout: Math.max(1, started + 5000 - Date.now()) });
    builder = await api(base + '/builder');
    assert.equal(builder.draftVersion, proposalResponse.draftVersion + 1, `${label} must advance the saved draft exactly once`);
    assert.equal(builder.draftDigest, proposalResponse.candidateWorkspaceDigest, `${label} saved workspace differs from the accepted candidate digest`);
    const accepted = await api(base + '/reconcile', {
      snapshotToken: proposalResponse.snapshotToken,
      draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest,
    });
    assert.equal(accepted.snapshotToken, proposalResponse.snapshotToken);
    assert.equal(accepted.intentDigest, builder.draftDigest);
    assert(accepted.outputs?.some(output => output.outputId === outputId), `${label} accepted receipt omitted its output`);
    assert(accepted.receiptId, `${label} saved draft must return its accepted receipt`);
    await waitUI(`(() => {const p=document.querySelector('[data-testid="construction-preview"]');return p?.dataset.previewStatus==='ready'&&p?.dataset.previewReceiptId===${JSON.stringify(accepted.receiptId)}&&p?.dataset.previewOutputId===${JSON.stringify(outputId)}&&p?.dataset.currentDraftVersion===${JSON.stringify(String(builder.draftVersion))}&&p?.dataset.currentDraftDigest===${JSON.stringify(builder.draftDigest)};})()`, 5000);
    const active = await page.getByTestId('construction-preview').evaluate(p => ({ status: p.dataset.previewStatus, receiptId: p.dataset.previewReceiptId, outputId: p.dataset.previewOutputId, draftVersion: p.dataset.currentDraftVersion, draftDigest: p.dataset.currentDraftDigest }));
    assert.deepEqual(active, { status: 'ready', receiptId: accepted.receiptId, outputId, draftVersion: String(builder.draftVersion), draftDigest: builder.draftDigest }, `${label} rendered preview must belong to the saved draft`);
    const rowCount = Math.min(25, expectedRows.length) + 1;
    await waitUI(`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(rowCount))} && !document.body.innerText.includes('Loading your table…')`, 5000);
    const networkDeadline = started + 5000;
    const postApplyRequests = report.nativeRequests.filter(entry => entry.path === base + '/preview' && entry.startedAt >= started);
    if (postApplyRequests.some(entry => !entry.completedAt)) {
      await requestCapture.waitFor(entry => entry.path === base + '/preview' && entry.startedAt >= started && entry.completedAt,
        { timeout: Math.max(1, networkDeadline - Date.now()) });
    }
    await requestCapture.flush();
    for (const entry of postApplyRequests) {
      assert.equal(entry.status, 200, `${label} post-Apply Preview request failed`);
      const previewResponse = responseBody(entry);
      assert.equal(previewResponse?.receiptId, accepted.receiptId, `${label} post-Apply Preview used another receipt`);
      assert.equal(previewResponse?.outputId, outputId);
      assert.equal(previewResponse?.rowCount, expectedRows.length, `${label} post-Apply Preview row count differs from the independent oracle`);
    }
    const savedPreview = (postApplyRequests.length ? responseBody(postApplyRequests.at(-1)) : undefined)
      ?? (proposalResponse.preview.receiptId === accepted.receiptId
        ? proposalResponse.preview
        : await api(base + '/preview', { receiptId: accepted.receiptId, outputId, limit: 25 }));
    assert.equal(savedPreview.receiptId, accepted.receiptId, `${label} saved preview must use the accepted receipt`);
    assert.equal(savedPreview.outputId, outputId);
    assert.equal(savedPreview.rowCount, expectedRows.length, `${label} saved preview row count differs from the independent oracle`);
    await assertMountedRows(expectedRows, label);
    assert(Date.now() - started <= 5000, `${label} apply-to-saved-preview exceeded five seconds`);
    report.cases.push({ name: `${label}-apply-saved-preview`, elapsedMs: Date.now() - started, receiptId: accepted.receiptId,
      previewSource: postApplyRequests.length ? 'post-apply-preview-request'
        : proposalResponse.preview.receiptId === accepted.receiptId ? 'accepted-proposal-preview-reused' : 'direct-read-of-accepted-receipt',
      draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
    return { builder, accepted, preview: savedPreview, active };
  };
  const chooseGroupKeys = async (labels, expectedInputIDs) => {
    await clickUI('[data-testid="construction-rows-settings-trigger"]');
    await waitUI(`document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled === false`, 5000);
    await clickUI('[data-testid="construction-action-group-rows"]');
    await waitUI(`Boolean(document.querySelector('[data-testid="construction-reshape-group"]'))`, 5000);
    const selectors = labels.map(label => `input[aria-label=${JSON.stringify(`Group by ${label}`)}]`);
    const enabledExpression = selectors.map(selector => `document.querySelector(${JSON.stringify(selector)})?.disabled === false`).join(' && ');
    await waitUI(enabledExpression, 5000);
    const initial = await page.locator('[data-testid="construction-reshape-group"] input[type="checkbox"][aria-label^="Group by "]').evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled })));
    for (const control of initial.filter(item => item.checked)) await clickUI(`input[aria-label=${JSON.stringify(control.label)}]`);
    for (const label of labels) await clickUI(`input[aria-label=${JSON.stringify(`Group by ${label}`)}]`);
    const checkedExpression = selectors.map(selector => `document.querySelector(${JSON.stringify(selector)})?.checked === true`).join(' && ');
    await waitUI(checkedExpression, 5000);
    const final = await page.locator('[data-testid="construction-reshape-group"] input[type="checkbox"][aria-label^="Group by "]:checked').evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label').replace(/^Group by /, '')));
    assert.deepEqual(final.sort(), [...labels].sort(), 'GROUP must use exactly the two oracle-matched source columns');
    const caps = await capabilitiesFor(labels, 'Direct root Group');
    const actualIDs = labels.map(label => caps.selectedStage.columns.find(column => column.label === label)?.id);
    assert(actualIDs.every(Boolean), `Group capability is missing a bound selected input: ${JSON.stringify(caps.selectedStage.columns)}`);
    assert.deepEqual([...actualIDs].sort(), [...expectedInputIDs].sort(), 'Native Group input IDs must be the current owner-bound source columns');
  };
  const groupFromResponse = response => {
    const step = response.candidateConstruction?.steps?.find(candidate => candidate.operation.kind === 'GROUP');
    assert(step, 'Native Group proposal must contain its authored GROUP step');
    const operation = step.operation.group;
    assert.equal(operation.keys.length, 2, 'Native Group must use exactly the status and unit source fields');
    assert.deepEqual(operation.keys.map(key => key.inputColumnId).sort(), [capsRootColumnId(sourceColumns.get('status').label), capsRootColumnId(sourceColumns.get('valueQuantity.unit').label)].sort());
    const inputLabels = operation.keys.map(key => rootCaps.selectedStage.columns.find(column => column.id === key.inputColumnId)?.label);
    assert.deepEqual(inputLabels, [sourceColumns.get('status').label, sourceColumns.get('valueQuantity.unit').label], 'Native Group must preserve status then unit output order');
    return step;
  };
  let rootCaps;
  const capsRootColumnId = label => rootCaps?.selectedStage.columns.find(column => column.label === label)?.id;

  const rootOpenStarted = Date.now();
  await open();
  rootCaps = await capabilitiesFor([...sourceColumns.values()].filter(column => column.source.field.path !== 'id').map(column => column.label), 'Initial root table');
  assert(capsRootColumnId(sourceColumns.get('status').label) && capsRootColumnId(sourceColumns.get('valueQuantity.unit').label), 'Initial capabilities must bind both authored GROUP source columns');
  await assertMountedRows(sourceRows, 'direct-group-count-source-table');
  const rootTablePreview = await currentRenderedPreview(rootOpenStarted, undefined, 'Initial root table');
  assert.equal(rootTablePreview.rowCount, members.length);
  const rootRowIDs = rootTablePreview.rows.map(row => row.__loom_row_id).sort();
  assert.equal(rootRowIDs.length, members.length);
  assert(rootRowIDs.every(rowID => typeof rowID === 'string' && rowID.length > 0), 'The raw root table must expose stable protocol row identities');
  const statusLabel = sourceColumns.get('status').label;
  const unitLabel = sourceColumns.get('valueQuantity.unit').label;
  const initialGroupWorkspace = structuredClone(rootWorkspace);
  const initialGroupVersion = rootDraftVersion;
  const initialGroupDigest = rootDraftDigest;

  const configureGroup = async () => {
    const started = Date.now();
    await chooseGroupKeys([statusLabel, unitLabel], [capsRootColumnId(statusLabel), capsRootColumnId(unitLabel)]);
    return started;
  };
  const groupCancelStarted = Date.now();
  await configureGroup();
  const groupCancelProposal = await waitProposalResponse(groupCancelStarted, groupedRows, 'direct-group-count-cancel-preview');
  const groupCancelStep = groupFromResponse(groupCancelProposal);
  const groupCancelOperation = groupCancelStep.operation.group;
  assert.equal(groupCancelOperation.aggregates.length, 1);
  const groupCancelCount = groupCancelOperation.aggregates[0];
  assert.equal(groupCancelCount.operation, 'COUNT_ROWS', 'Group must use COUNT_ROWS rather than a source-value aggregate');
  const groupCancelOutputLabels = groupCancelOperation.keys.map(key => groupCancelStep.outputs.find(column => column.id === key.outputColumnId)?.label);
  const groupCancelCountOutput = groupCancelStep.outputs.find(column => column.id === groupCancelCount.outputColumnId);
  assert(groupCancelCountOutput, 'Group proposal must preserve its COUNT_ROWS output binding');
  assertPreviewByOutputs(groupCancelProposal.preview, groupCancelStep, groupedRows, [...groupCancelOutputLabels, groupCancelCountOutput.label], 'Group COUNT_ROWS proposal');
  await clickUI('[data-testid="construction-cancel-proposal"]');
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  builder = await api(base + '/builder');
  assert.deepEqual(builder.workspace, initialGroupWorkspace, 'Canceling Group must retain the exact source-only workspace');
  assert.equal(builder.draftVersion, initialGroupVersion, 'Canceling Group must not advance draft version');
  assert.equal(builder.draftDigest, initialGroupDigest, 'Canceling Group must not change draft digest');
  await assertMountedRows(sourceRows, 'direct-group-count-cancel-restores-source-table');
  assert(Date.now() - groupCancelStarted <= 5000, 'Group preview Cancel must settle within five seconds');
  report.cases.push({ name: 'direct-group-count-cancel-preserves-source', elapsedMs: Date.now() - groupCancelStarted });

  const groupApplyStarted = Date.now();
  await configureGroup();
  const groupApplyProposal = await waitProposalResponse(groupApplyStarted, groupedRows, 'direct-group-count-apply-preview');
  const proposedGroup = groupFromResponse(groupApplyProposal);
  const proposedGroupOperation = proposedGroup.operation.group;
  const groupKeyBindings = proposedGroupOperation.keys.map(key => ({ inputColumnId: key.inputColumnId, outputColumnId: key.outputColumnId }));
  const groupKeyLabels = groupKeyBindings.map(binding => proposedGroup.outputs.find(column => column.id === binding.outputColumnId)?.label);
  const countBinding = proposedGroupOperation.aggregates.find(item => item.operation === 'COUNT_ROWS');
  assert(countBinding, 'Native Group proposal must bind one COUNT_ROWS summary');
  const countOutput = proposedGroup.outputs.find(column => column.id === countBinding.outputColumnId);
  assert(countOutput, 'Native Group proposal must expose its saved row count output');
  assertPreviewByOutputs(groupApplyProposal.preview, proposedGroup, groupedRows, [...groupKeyLabels, countOutput.label], 'Group COUNT_ROWS Apply proposal');
  const groupSaved = await applyAcceptedProposal(groupApplyProposal, groupedRows, 'direct-group-count');
  builder = groupSaved.builder;
  const savedGroup = doc(builder).construction.steps.find(step => step.id === proposedGroup.id && step.operation.kind === 'GROUP');
  assert(savedGroup, 'Applying the first operation must save the exact native Group step');
  assert.deepEqual(doc(builder).construction.steps, [savedGroup], 'Group Apply must not introduce a Pivot or duplicate Group step');
  assert.deepEqual(savedGroup.operation.group.keys, proposedGroupOperation.keys, 'Saved Group must preserve both exact source key bindings');
  assert.deepEqual(savedGroup.operation.group.aggregates, proposedGroupOperation.aggregates, 'Saved Group must preserve COUNT_ROWS');
  assertPreviewByOutputs(groupSaved.preview, savedGroup, groupedRows, [
    ...savedGroup.operation.group.keys.map(key => savedGroup.outputs.find(column => column.id === key.outputColumnId)?.label),
    ...savedGroup.operation.group.aggregates.map(aggregate => savedGroup.outputs.find(column => column.id === aggregate.outputColumnId)?.label),
  ], 'Accepted Group COUNT_ROWS saved preview');
  assert.deepEqual(doc(builder).population, doc({ workspace: rootWorkspace }).population, 'Group Apply must preserve exact explicit source population membership');
  assert.deepEqual(withoutLocalSourceIDs(doc(builder).columns), withoutLocalSourceIDs(doc({ workspace: initialGroupWorkspace }).columns), 'Group must retain all three authored root field bindings');
  const rootColumnsAfterGroupApply = structuredClone(doc(builder).columns);
  const groupWorkspace = builder.workspace;
  const groupDraftVersion = builder.draftVersion;
  const groupDraftDigest = builder.draftDigest;
  const savedGroupKeys = savedGroup.operation.group.keys;
  const savedStatusOutput = savedGroup.outputs.find(column => column.id === savedGroupKeys.find(key => key.inputColumnId === capsRootColumnId(statusLabel))?.outputColumnId);
  const savedUnitOutput = savedGroup.outputs.find(column => column.id === savedGroupKeys.find(key => key.inputColumnId === capsRootColumnId(unitLabel))?.outputColumnId);
  const savedCountOutput = savedGroup.outputs.find(column => column.id === savedGroup.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS')?.outputColumnId);
  assert(savedStatusOutput && savedUnitOutput && savedCountOutput, 'Saved Group must preserve dynamic status, unit, and COUNT_ROWS outputs');
  const groupOutputs = { status: savedStatusOutput, unit: savedUnitOutput, count: savedCountOutput };
  const groupKeyLabelByOutput = { status: savedStatusOutput.label, unit: savedUnitOutput.label, count: savedCountOutput.label };
  const groupReloadStarted = Date.now();
  await open();
  await assertMountedRows(groupedRows, 'direct-group-count-apply-reload');
  const groupReloadPreview = await currentRenderedPreview(groupReloadStarted, groupSaved.preview, 'Group Apply reload');
  assertPreviewByOutputs(groupReloadPreview, savedGroup, groupedRows, [groupOutputs.status.label, groupOutputs.unit.label, groupOutputs.count.label], 'Reloaded Group COUNT_ROWS');
  const groupRowIDs = groupReloadPreview.rows.map(row => row.__loom_row_id).sort();
  assert.equal(groupRowIDs.length, 1);
  assert.deepEqual((await api(base + '/builder')).workspace, groupWorkspace, 'Reloading Group must preserve the exact saved Group workspace');

  const pivotCapabilities = await capabilitiesFor(Object.values(groupKeyLabelByOutput), 'Group output stage');
  const pivotStageColumns = pivotCapabilities.selectedStage.columns;
  for (const output of Object.values(groupOutputs)) {
    assert(pivotStageColumns.some(column => column.id === output.id && column.label === output.label), `Current Pivot stage must expose the exact saved Group output ${output.label}`);
  }
  const choosePivotField = async (ariaLabel, expectedLabel, expectedColumnID) => {
    const selector = `select[aria-label=${JSON.stringify(ariaLabel)}]`;
    const locator = page.locator(selector);
    await requireUnique(locator, `${ariaLabel} select`);
    const options = await locator.evaluate(select => [...select.options].map(option => ({ value: option.value, label: option.textContent.trim() })));
    const matches = options.filter(option => option.value === expectedColumnID && (option.label === expectedLabel || option.label.startsWith(expectedLabel + ' (')));
    assert.equal(matches.length, 1, `${ariaLabel} must contain one exact current Group output ${expectedLabel}: ${JSON.stringify(options)}`);
    await selectUI(selector, matches[0].value, { settledWhen: `document.querySelector(${JSON.stringify(selector)})?.selectedOptions[0]?.textContent.trim() === ${JSON.stringify(matches[0].label)}` });
  };
  const configurePivot = async () => {
    await clickUI('[data-testid="construction-rows-settings-trigger"]');
    await waitUI(`document.querySelector('[data-testid="construction-action-pivot-rows"]')?.disabled === false`, 5000);
    const started = Date.now();
    await clickUI('[data-testid="construction-action-pivot-rows"]');
    await waitUI(`document.querySelector('select[aria-label="Pivot category field"]') || document.body.innerText.includes('Coded values as columns')`, 5000);
    if (!await page.locator('select[aria-label="Pivot category field"]').count()) {
      await clickUI('button', { name: 'Change row operation' });
      await clickUI('[data-testid="construction-reshape-choice-pivot"]');
    }
    const categorySelector = 'select[aria-label="Pivot category field"]';
    await waitUI(`document.querySelector(${JSON.stringify(categorySelector)})?.disabled === false`, 5000);
    const groupControls = await page.locator('input[aria-label^="Pivot group "]').evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled })));
    const targetLabel = `Pivot group ${groupOutputs.unit.label}`;
    assert(groupControls.some(control => control.label === targetLabel && !control.disabled), `Pivot editor must expose the saved Group unit output: ${JSON.stringify(groupControls)}`);
    for (const control of groupControls) {
      const shouldBeChecked = control.label === targetLabel;
      if (control.checked !== shouldBeChecked) await clickUI(`input[aria-label=${JSON.stringify(control.label)}]`);
    }
    await choosePivotField('Pivot category field', groupOutputs.status.label, groupOutputs.status.id);
    await choosePivotField('Pivot values field', groupOutputs.count.label, groupOutputs.count.id);
    await waitUI(`Boolean(document.querySelector('[data-testid="construction-reshape-pivot"]'))`, 5000);
    const currentCapabilities = await capabilitiesFor(Object.values(groupKeyLabelByOutput), 'Current Group outputs for Pivot');
    assert(currentCapabilities.selectedStage.columns.some(column => column.id === groupOutputs.status.id));
    assert(currentCapabilities.selectedStage.columns.some(column => column.id === groupOutputs.unit.id));
    assert(currentCapabilities.selectedStage.columns.some(column => column.id === groupOutputs.count.id));
    return started;
  };
  const pivotFromResponse = response => {
    const candidateConstruction = response.candidateConstruction;
    assert(candidateConstruction, 'Pivot proposal must include its complete candidate construction');
    assert.deepEqual(candidateConstruction.steps[0], savedGroup, 'Pivot candidate must retain the exact already-applied Group step');
    const step = candidateConstruction.steps.find(candidate => candidate.operation.kind === 'PIVOT');
    assert(step, 'Pivot proposal must append the ordinary PIVOT operation after Group');
    const pivot = step.operation.pivot;
    assert.equal(pivot.groupKeyIds.length, 1);
    assert.deepEqual(pivot.groupKeyIds, [groupOutputs.unit.id], 'Pivot must retain the Group unit output as its only group key');
    assert.equal(pivot.categoryColumnId, groupOutputs.status.id, 'Pivot category must bind to the Group status output');
    assert.equal(pivot.valueColumnId, groupOutputs.count.id, 'Pivot value must bind to the Group COUNT_ROWS output');
    assert.deepEqual(pivot.categories.map(category => category.key), [{ kind: 'STRING', string: categoryValue }], 'Pivot category must match the scoped raw Observation status');
    assert.equal(response.preview.rowCount, 1, 'Group → Pivot must produce one unit row');
    const unitOutput = step.outputs.find(column => column.id === groupOutputs.unit.id);
    const pivotCategory = pivot.categories[0];
    const categoryOutput = step.outputs.find(column => column.id === pivotCategory.outputColumnId);
    assert(unitOutput && categoryOutput, 'Pivot must preserve its Group key and typed category outputs');
    const unitColumn = response.preview.columns.find(column => column.column === unitOutput.name);
    const categoryColumn = response.preview.columns.find(column => column.column === categoryOutput.name);
    assert(unitColumn && categoryColumn, 'Pivot protocol preview must include its stable Group key and generated category columns');
    assert.equal(response.preview.rows[0][unitColumn.column], unitValue, 'Pivot key cell must equal the independent raw unit');
    assert.equal(Number(response.preview.rows[0][categoryColumn.column]), members.length, 'Pivot cell must equal the independently witnessed Group COUNT_ROWS value');
    const proposalRowID = JSON.stringify(['GROUPED_PIVOT', pivot.constructionId, ['STRING', unitValue]]);
    assert.equal(response.preview.rows[0].__loom_row_id, proposalRowID, 'Pivot proposal row identity must encode its typed construction and raw unit key');
    assert.equal(response.preview.receiptId, response.proposalId, 'Preview receipt must match the candidate proposal');
    return { stepID: step.id, constructionId: pivot.constructionId, rowID: proposalRowID, unitOutput, categoryOutput, finalRows: pivotRows };
  };

  const pivotCancelStarted = Date.now();
  await configurePivot();
  const pivotCancelResponse = await waitProposalResponse(pivotCancelStarted, pivotRows, 'direct-group-count-pivot-cancel-preview');
  const pivotCancelIdentity = pivotFromResponse(pivotCancelResponse);
  await clickUI('[data-testid="construction-cancel-proposal"]');
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  builder = await api(base + '/builder');
  assert.deepEqual(builder.workspace, groupWorkspace, 'Canceling Pivot must preserve the exact saved Group workspace');
  assert.equal(builder.draftVersion, groupDraftVersion);
  assert.equal(builder.draftDigest, groupDraftDigest);
  await assertMountedRows(groupedRows, 'direct-group-count-pivot-cancel-preserves-group');
  assert(Date.now() - pivotCancelStarted <= 5000, 'Pivot preview Cancel must settle within five seconds');
  report.cases.push({ name: 'direct-group-count-pivot-cancel-preserves-group', elapsedMs: Date.now() - pivotCancelStarted, rowID: pivotCancelIdentity.rowID });

  const pivotApplyStarted = Date.now();
  await configurePivot();
  const pivotApplyResponse = await waitProposalResponse(pivotApplyStarted, pivotRows, 'direct-group-count-pivot-apply-preview');
  const pivotIdentity = pivotFromResponse(pivotApplyResponse);
  const pivotSaved = await applyAcceptedProposal(pivotApplyResponse, pivotRows, 'direct-group-count-pivot');
  builder = pivotSaved.builder;
  const savedConstruction = doc(builder).construction;
  assert.equal(savedConstruction.steps.length, 2, 'Applying Pivot must retain one Group followed by one Pivot step');
  assert.deepEqual(savedConstruction.steps[0], savedGroup, 'Pivot Apply must preserve the exact saved Group contract');
  const savedPivot = savedConstruction.steps[1];
  assert.equal(savedPivot.id, pivotIdentity.stepID);
  assert.equal(savedPivot.operation.kind, 'PIVOT');
  const proposedPivot = pivotApplyResponse.candidateConstruction.steps.find(step => step.id === pivotIdentity.stepID);
  assert(proposedPivot, 'Accepted candidate must retain the proposed Pivot step');
  assert.deepEqual(savedPivot.operation.pivot, proposedPivot.operation.pivot, 'Saved Pivot must preserve the exact typed operation bindings');
  const pivotWorkspace = builder.workspace;
  const pivotDraftVersion = builder.draftVersion;
  const pivotDraftDigest = builder.draftDigest;
  const appliedPreview = pivotSaved.preview;
  assertPreviewByOutputs(appliedPreview, savedPivot, pivotRows, [pivotIdentity.unitOutput.label, pivotIdentity.categoryOutput.label], 'Applied Group → Pivot');
  const savedPivotRowID = JSON.stringify(['GROUPED_PIVOT', savedPivot.operation.pivot.constructionId, ['STRING', unitValue]]);
  assert.equal(appliedPreview.rows[0]?.__loom_row_id, savedPivotRowID, 'Accepted Group → Pivot row identity must encode the saved typed construction and independent raw unit key');
  pivotIdentity.rowID = savedPivotRowID;
  const reloadStarted = Date.now();
  await open();
  await assertMountedRows(pivotRows, 'direct-group-count-pivot-reload');
  const reloadPreview = await currentRenderedPreview(reloadStarted, pivotSaved.preview, 'Group → Pivot reload');
  assertPreviewByOutputs(reloadPreview, savedPivot, pivotRows, [pivotIdentity.unitOutput.label, pivotIdentity.categoryOutput.label], 'Reloaded Group → Pivot');
  assert.equal(reloadPreview.rows[0].__loom_row_id, pivotIdentity.rowID, 'Reload must retain typed Pivot row identity');
  assert.deepEqual((await api(base + '/builder')).workspace, pivotWorkspace, 'Reload must preserve exact Group → Pivot workspace');

  const visibleRows = await inspectPreviewRows();
  const inspectable = visibleRows.find(row => row.cells.includes(unitValue));
  assert(inspectable && inspectable.rowNumber > 0 && inspectable.rowNumber <= 25, 'Reloaded Pivot unit row must expose its native inspector control');
  const inspectStarted = Date.now();
  const requestStart = report.nativeRequests.length;
  await clickUI(`button[aria-label="Inspect row ${inspectable.rowNumber} identity"]`);
  const dialog = `[role="dialog"][aria-label="Row ${inspectable.rowNumber} identity"]`;
  await waitUI(`document.querySelector(${JSON.stringify(dialog)})`, 5000);
  await waitUI(`document.querySelector(${JSON.stringify(dialog + ' ul li')})`, 5000);
  const lineageRequests = () => report.nativeRequests.slice(requestStart).filter(request => request.path === base + '/row-lineage');
  if (!lineageRequests().length) await requestCapture.waitFor(request => request.path === base + '/row-lineage' && request.startedAt >= inspectStarted,
    { timeout: Math.max(1, inspectStarted + 5000 - Date.now()) });
  await requestCapture.flush();
  const request = lineageRequests()[0];
  assert.equal(lineageRequests().length, 1, 'A single grouped Pivot row must use one bounded lineage request');
  assert.equal(request.status, 200);
  const body = requestBody(request);
  const response = responseBody(request);
  assert.equal(body.outputId, outputId);
  assert.equal(body.rowId, pivotIdentity.rowID);
  assert.equal(body.receiptId, reloadPreview.receiptId);
  assert.equal(body.limit, 25);
  assert.equal(response.rowId, pivotIdentity.rowID);
  assert.equal(response.receiptId, reloadPreview.receiptId);
  assert.equal(response.hasMore, false);
  const actualContributors = response.contributors.map(item => [item.resourceType, item.resourceId, item.occurrenceKey]);
  const expectedContributors = members.map(member => ['Observation', member.id, member._key]);
  const contributorKey = tuple => tuple.join('\\u0000');
  assert.deepEqual(actualContributors.map(contributorKey).sort(), expectedContributors.map(contributorKey).sort(), 'Group → Pivot inspector must return exactly the two raw Observation roots contributing to COUNT_ROWS');
  const visibleContributors = await page.locator(`${dialog} ul li`).allInnerTexts();
  assert.equal(visibleContributors.length, 2);
  for (const member of members) assert(visibleContributors.some(line => line.includes(`Observation/${member.id}`)), `Inspector omitted scoped Observation/${member.id}`);
  assert(Date.now() - inspectStarted <= 5000, 'Group → Pivot inspection exceeded its five-second UI bound');
  report.cases.push({ name: 'direct-group-count-pivot-inspect-two-root-contributors', elapsedMs: Date.now() - inspectStarted, rowID: pivotIdentity.rowID, receiptId: reloadPreview.receiptId, contributors: actualContributors });
  await clickUI(`${dialog} button`, { name: 'Close' });
  await waitUI(`!document.querySelector(${JSON.stringify(dialog)})`, 5000);
  assert.deepEqual((await api(base + '/builder')).workspace, pivotWorkspace, 'Inspection must not mutate the saved Group → Pivot');

  const removePivotProposal = await proposeRemoval(savedPivot.id, 'direct-group-count-remove-pivot-preview', groupedRows);
  assert.deepEqual(removePivotProposal.candidateConstruction.steps, [savedGroup], 'Removing Pivot must retain the exact saved Group step');
  assertPreviewByOutputs(removePivotProposal.preview, savedGroup, groupedRows, [groupOutputs.status.label, groupOutputs.unit.label, groupOutputs.count.label], 'Group restoration removal proposal');
  const groupRestored = await applyAcceptedProposal(removePivotProposal, groupedRows, 'direct-group-count-remove-pivot');
  builder = groupRestored.builder;
  assert.deepEqual(doc(builder).construction.steps, [savedGroup], 'Pivot removal Apply must restore the Group-only construction');
  const groupRestoredWorkspace = builder.workspace;
  const groupRestoreStarted = Date.now();
  await open();
  await assertMountedRows(groupedRows, 'direct-group-count-pivot-removal-restores-group');
  const groupRestorePreview = await currentRenderedPreview(groupRestoreStarted, groupRestored.preview, 'Group restoration after Pivot removal');
  assertPreviewByOutputs(groupRestorePreview, savedGroup, groupedRows, [groupOutputs.status.label, groupOutputs.unit.label, groupOutputs.count.label], 'Reloaded Group restoration');
  assert.deepEqual(groupRestorePreview.rows.map(row => row.__loom_row_id).sort(), groupRowIDs, 'Removing Pivot must restore the exact saved Group row identity');
  assert.deepEqual((await api(base + '/builder')).workspace, groupRestoredWorkspace, 'Reload after Pivot removal must persist the exact Group-only workspace');

  const removeGroupProposal = await proposeRemoval(savedGroup.id, 'direct-group-count-remove-group-preview', sourceRows);
  assert.deepEqual(removeGroupProposal.candidateConstruction.steps, [], 'Removing Group must restore the exact root-only construction');
  const rootsRestored = await applyAcceptedProposal(removeGroupProposal, sourceRows, 'direct-group-count-remove-group');
  builder = rootsRestored.builder;
  assert.deepEqual(doc(builder).construction?.steps ?? [], [], 'Group removal Apply must remove all authored construction steps');
  assert.deepEqual(doc(builder).population, doc({ workspace: rootWorkspace }).population, 'Group removal must restore exact explicit source population membership');
  assert.deepEqual(doc(builder).columns, rootColumnsAfterGroupApply, 'Removing Group must restore the exact stable root source bindings');
  const restoredWorkspace = builder.workspace;
  const rootRestoreStarted = Date.now();
  await open();
  await assertMountedRows(sourceRows, 'direct-group-count-remove-group-restores-root-identities');
  const rootRestorePreview = await currentRenderedPreview(rootRestoreStarted, rootsRestored.preview, 'Root restoration after Group removal');
  assert.equal(rootRestorePreview.rowCount, members.length);
  assert.deepEqual(rootRestorePreview.rows.map(row => row.__loom_row_id).sort(), rootRowIDs, 'Removing Group must restore the exact raw root row identities');
  assert.deepEqual((await api(base + '/builder')).workspace, restoredWorkspace, 'Final reload must retain the exact root-only workspace');
  assert(report.nativeRequests.every(entry => localOrigins.has(entry.origin) && !entry.authorizationHeaderPresent), 'Group → Pivot requests must remain local no-auth calls');
  assert.deepEqual(report.errors, [], 'Group → Pivot native lifecycle must have no browser/runtime/HTTP errors');
  report.directGroupCountPivot = {
    selectionRevisionId: selection.id, sourceIDs: members.map(member => member.id),
    sourceBindings: rootColumnsAfterGroupApply.map(column => ({ columnId: column.columnId, column: column.column, label: column.label, sourcePath: column.source.field.path })),
    groupStepID: savedGroup.id, pivotStepID: savedPivot.id, groupKeys: savedGroupKeys,
    countOutput: groupOutputs.count, pivotCategory: { kind: 'STRING', string: categoryValue },
    pivotGroupUnit: unitValue, pivotRowID: pivotIdentity.rowID, expectedContributors,
    lifecycle: ['Group preview/cancel', 'Group apply/reload', 'Pivot preview/cancel', 'Pivot apply/reload/inspect', 'remove Pivot and reload Group', 'remove Group and reload root records'],
    restoredRootRows: sourceRows, finalWorkspaceHasConstruction: false,
  };
};

const runDirectPivot = async () => {
  const sourceQuery = sharedContributorPivot
    ? `FOR observation IN Observation
  FILTER ${scopedDocument('observation')}
    AND IS_STRING(observation.payload.status) AND observation.payload.status != ""
    AND IS_STRING(observation.payload.valueQuantity.unit)
    AND IS_NUMBER(observation.payload.valueQuantity.value)
  COLLECT groupKey = observation.payload.status, unit = observation.payload.valueQuantity.unit
    AGGREGATE memberCount = COUNT()
  FILTER memberCount >= 2
  SORT groupKey, unit
  LIMIT 1
  LET members = (
    FOR candidate IN Observation
      FILTER ${scopedDocument('candidate')}
        AND candidate.payload.status == groupKey
        AND candidate.payload.valueQuantity.unit == unit
        AND IS_NUMBER(candidate.payload.valueQuantity.value)
      SORT candidate._key
      LIMIT 2
      RETURN {
        id: candidate.id, _id: candidate._id, _key: candidate._key,
        status: candidate.payload.status, unit: candidate.payload.valueQuantity.unit,
        value: candidate.payload.valueQuantity.value,
        project: candidate.project, dataset_generation: candidate.dataset_generation
      }
  )
  FILTER LENGTH(members) == 2
  RETURN {groupKey, unit, members}`
    : `FOR observation IN Observation FILTER ${scopedDocument('observation')} AND IS_STRING(observation.payload.status) AND observation.payload.status != "" SORT observation._key LIMIT 2000 RETURN {id:observation.id,_id:observation._id,_key:observation._key,status:observation.payload.status,project:observation.project,dataset_generation:observation.dataset_generation}`;
  const candidates = rawQuery(sourceQuery);
  let members;
  if (sharedContributorPivot) {
    assert.equal(candidates.length, 1, 'The scoped raw grouping must find one status/unit pair with at least two numeric Observations');
    members = candidates[0].members;
    assert.equal(candidates[0].members.length, 2, 'The chosen shared Pivot cell must have exactly two independently returned source rows');
    assert.equal(candidates[0].groupKey, members[0].status);
    assert.equal(candidates[0].unit, members[0].unit);
    assert(members.every(member => member.project === project && member.dataset_generation === generation));
  } else {
    assert(candidates.length >= 2, 'The scoped raw Observation scan must find at least two status-bearing records');
    const candidatesByGroup = new Map();
    for (const candidate of candidates) {
      assert(candidate.project === project && candidate.dataset_generation === generation);
      const matching = candidatesByGroup.get(candidate.status) ?? [];
      if (matching.length < 2) matching.push(candidate);
      candidatesByGroup.set(candidate.status, matching);
    }
    const chosen = [...candidatesByGroup.values()].find(records => records.length === 2);
    assert(chosen, 'The bounded CDA scan must find two distinct Observation IDs with one shared status category');
    members = [...chosen];
  }
  members = [...members].sort((left, right) => left.id.localeCompare(right.id));
  assert.notEqual(members[0].id, members[1].id);
  const groupKey = sharedContributorPivot ? members[0].status : undefined;
  const categoryKey = { kind: 'STRING', string: sharedContributorPivot ? members[0].unit : members[0].status };
  const baselineRows = members.map(member => [member.id, member.status]);
  const expectedRows = sharedContributorPivot
    ? [[groupKey, String(members.reduce((total, member) => total + member.value, 0))]]
    : members.map(member => [member.id, member.id]);
  const expectedContributorByID = new Map(members.map(member => [member.id, ['Observation', member.id, member._key]]));
  report.oracle = {
    query: sourceQuery,
    scope: 'project + dataset generation + exact two-member Observation selection',
    candidateScanCount: sharedContributorPivot ? 2 : candidates.length,
    members,
    ...(sharedContributorPivot ? { groupKey, aggregateValue: members.reduce((total, member) => total + member.value, 0) } : {}),
    typedCategories: [categoryKey],
    groupedCategoryMemberships: [{ groupKey: sharedContributorPivot ? [groupKey] : members.map(member => member.id), category: categoryKey, sourceIDs: members.map(member => member.id) }],
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



  const chooseField = async (label, prefix) => {
    const selector = `select[aria-label=${JSON.stringify(label)}]`;
    const locator = page.locator(selector);
    await requireUnique(locator, `${label} select`);
    const options = await locator.evaluate(select => [...select.options].map(option => ({ value: option.value, label: option.textContent.trim() })));
    const field = options.find(option => option.label.startsWith(prefix));
    assert(field, `${label} lacks ${prefix}: ${JSON.stringify(options)}`);
    await selectUI(selector, field.value, field.value.startsWith('source:') ? {
      settledWhen: `document.querySelector(${JSON.stringify(selector)})?.selectedOptions[0]?.textContent.startsWith(${JSON.stringify(prefix)}) || Boolean(document.querySelector('[data-testid="construction-proposal-ready"]'))`,
    } : {});
  };
  const typePivotOutputName = async name => {
    const advanced = '[data-testid="construction-reshape-pivot-advanced"]';
    if (!await page.locator(advanced).evaluate(element => element.open)) {
      await clickUI(`${advanced} summary`);
    }
    await waitUI(`document.querySelectorAll('input[aria-label^="Pivot output name "]').length === 1`);
    const inputs = await page.locator('input[aria-label^="Pivot output name "]').evaluateAll(elements => elements.map(input => ({ label: input.getAttribute('aria-label'), value: input.value, disabled: input.disabled })));
    assert.equal(inputs.length, 1, `The direct status Pivot must expose one category output name: ${JSON.stringify(inputs)}`);
    assert.equal(inputs[0].disabled, false, 'Native Pivot output name must be editable');
    const selector = `input[aria-label=${JSON.stringify(inputs[0].label)}]`;
    await fillUI(selector, name, 'Set Pivot physical output name');
    assert.equal(await page.locator(selector).inputValue(), name, 'Native typing must set the exact Pivot physical output name');
  };
  const configurePivot = async () => {
    await clickUI('[data-testid="construction-rows-settings-trigger"]');
    await waitUI(`document.querySelector('[data-testid="construction-action-pivot-rows"]')?.disabled === false`);
    await clickUI('[data-testid="construction-action-pivot-rows"]');
    await waitUI(`document.querySelector('select[aria-label="Pivot category field"]') || document.body.innerText.includes('Coded values as columns')`);
    const ordinaryPivotOpen = (await page.locator('select[aria-label="Pivot category field"]').count()) === 1;
    if (!ordinaryPivotOpen) {
      await clickUI('button', { name: 'Change row operation' });
      await clickUI('[data-testid="construction-reshape-choice-pivot"]');
    }
    await waitUI(`document.querySelector('select[aria-label="Pivot category field"]:not(:disabled)')`);
    const groupLabels = await page.locator('input[aria-label^="Pivot group "]').evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled })));
    const expectedGroupLabel = sharedContributorPivot ? 'Pivot group Observation.status' : 'Pivot group Observation FHIR resource ID';
    assert(groupLabels.some(group => group.label === expectedGroupLabel), `Pivot editor lacks the expected Observation group key: ${JSON.stringify(groupLabels)}`);
    for (const group of groupLabels) {
      const selector = `input[aria-label=${JSON.stringify(group.label)}]`;
      assert(!group.disabled, `Pivot group key is disabled: ${group.label}`);
      const shouldBeChecked = group.label === expectedGroupLabel;
      if (group.checked !== shouldBeChecked) await clickUI(selector);
    }
    const started = Date.now();
    await chooseField('Pivot category field', sharedContributorPivot ? 'Observation.valueQuantity.unit' : 'Observation.status');
    await chooseField('Pivot values field', sharedContributorPivot ? 'Observation.valueQuantity.value' : 'Observation.id');
    if (sharedContributorPivot) {
      const advanced = '[data-testid="construction-reshape-pivot-advanced"]';
      if (!await page.locator(advanced).evaluate(element => element.open)) {
        await clickUI(`${advanced} summary`);
      }
      await selectUI('select[aria-label="Pivot duplicate policy"]', 'SUM');
    }
    return started;
  };
  let pivotPreviewOutputNames;
  const assertPivotPreview = (preview, label) => {
    assert(preview, `${label} must include a protocol preview`);
    assert.equal(preview.outputId, outputId);
    assert.equal(preview.rowCount, sharedContributorPivot ? 1 : members.length, `${label} must retain the independently selected Pivot groups`);
    if (sharedContributorPivot) {
      assert(pivotPreviewOutputNames, `${label} requires the declared Pivot output names from its construction step`);
      const groupColumn = preview.columns.find(column => column.column === pivotPreviewOutputNames.group);
      assert(groupColumn, `${label} lacks declared Pivot group output ${pivotPreviewOutputNames.group}`);
      assert.equal(preview.rows[0][groupColumn.column], groupKey);
      const categoryOutput = preview.columns.find(column => column.column === pivotPreviewOutputNames.category);
      assert(categoryOutput, `${label} lacks declared Pivot category output ${pivotPreviewOutputNames.category}`);
      const actualSum = Number(preview.rows[0][categoryOutput.column]);
      const expectedSum = members.reduce((total, member) => total + member.value, 0);
      assert(Number.isFinite(actualSum) && Math.abs(actualSum - expectedSum) < 1e-9,
        `${label} summed cell = ${preview.rows[0][categoryOutput.column]}, want ${expectedSum}`);
      assert(typeof preview.rows[0].__loom_row_id === 'string' && preview.rows[0].__loom_row_id.length > 0);
      return;
    }
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
    assert.equal(pivot.groupKeyIds.length, 1, 'Direct Pivot must group by exactly the witnessed Observation key');
    assert.deepEqual(pivot.categories.map(category => category.key), [categoryKey], 'Native category domain must exactly equal the typed raw CDA category domain');
    const categoryOutput = pivotStep.outputs.find(column => column.id === pivot.categories[0].outputColumnId);
    assert(categoryOutput, 'The typed raw category must resolve to its declared Pivot output');
    if (expectedCategoryOutputName !== null) assert.equal(categoryOutput.name, expectedCategoryOutputName, 'Native proposal must retain the exact requested physical output name');
    assert.equal(response.preview.receiptId, response.proposalId, 'Proposal preview must be bound to its exact candidate receipt');
    const expectedGroupLabel = sharedContributorPivot ? 'Observation.status' : 'Observation FHIR resource ID';
    const group = pivotStep.outputs.find(column => column.id === pivot.groupKeyIds[0]);
    assert(group?.label?.includes(expectedGroupLabel), `Pivot group identity must retain ${expectedGroupLabel}: ${JSON.stringify(pivotStep.outputs)}`);
    assert(typeof group.name === 'string' && group.name.length > 0, 'The selected group key must retain its declared physical output name');
    assert(typeof categoryOutput.name === 'string' && categoryOutput.name.length > 0, 'The selected category must retain its declared physical output name');
    pivotPreviewOutputNames = { group: group.name, category: categoryOutput.name };
    assert.notEqual(pivotPreviewOutputNames.group, pivotPreviewOutputNames.category, 'Pivot group and category outputs must have distinct physical names');
    for (const name of Object.values(pivotPreviewOutputNames)) {
      assert(response.preview.columns.some(column => column.column === name), `Protocol preview must contain declared Pivot output ${name}`);
    }
    if (sharedContributorPivot) {
      assert.equal(pivot.duplicatePolicy, 'SUM', 'A shared Pivot cell must sum both source observations');
    }
    const groupValues = sharedContributorPivot ? [groupKey] : members.map(member => member.id);
    const expectedIDs = groupValues.map(value => JSON.stringify(['GROUPED_PIVOT', pivot.constructionId, ['STRING', value]])).sort();
    const actualIDs = response.preview.rows.map(row => row.__loom_row_id).sort();
    assert.deepEqual(actualIDs, expectedIDs, 'Pivot identities must encode the typed raw group keys');
    assertPivotPreview(response.preview, 'Native Pivot proposal');
    return {
      stepID: pivotStep.id, constructionId: pivot.constructionId, rowIDs: expectedIDs,
      groupOutputName: group.name, categoryOutputName: categoryOutput.name,
    };
  };
  const rowIDFor = (identity, id) => JSON.stringify(['GROUPED_PIVOT', identity.constructionId, ['STRING', id]]);
  const assertPreviewCells = (preview, rows, label) => {
    assert(preview, `${label} must include a protocol preview`);
    assert.equal(preview.outputId, outputId);
    if (sharedContributorPivot && rows.length === 1 && rows[0][0] === groupKey) {
      assert.equal(preview.rowCount, 1, `${label} must retain the one shared status group`);
      assert(pivotPreviewOutputNames, `${label} requires the declared Pivot output names from its construction step`);
      const groupColumn = preview.columns.find(column => column.column === pivotPreviewOutputNames.group);
      assert(groupColumn, `${label} lacks declared Pivot group output ${pivotPreviewOutputNames.group}`);
      assert.equal(preview.rows[0][groupColumn.column], groupKey);
      const categoryOutput = preview.columns.find(column => column.column === pivotPreviewOutputNames.category);
      assert(categoryOutput, `${label} lacks declared Pivot category output ${pivotPreviewOutputNames.category}`);
      const actualSum = Number(preview.rows[0][categoryOutput.column]);
      const expectedSum = members.reduce((total, member) => total + member.value, 0);
      assert(Number.isFinite(actualSum) && Math.abs(actualSum - expectedSum) < 1e-9,
        `${label} summed cell = ${preview.rows[0][categoryOutput.column]}, want ${expectedSum}`);
      return;
    }
    const actual = preview.rows.map(row => preview.columns.map(column => row[column.column]));
    const sorted = values => values.map(row => JSON.stringify(row)).sort();
    assert.deepEqual(sorted(actual), sorted(rows), `${label} must exactly match the independently queried raw source rows`);
  };
  const waitForPreview = async (started, label) => {
    const deadline = started + 5000;
    const request = await requestCapture.waitFor(entry => entry.path === base + '/preview' && entry.startedAt >= started && entry.status === 200,
      { timeout: Math.max(1, deadline - Date.now()) });
    return responseBody(request);
  };
  const rowsWithInspectorNumbers = inspectPreviewRows;
  const inspect = async (id, expectedRowID, savedPreview, label) => {
    const rows = await rowsWithInspectorNumbers();
    const visible = rows.find(row => row.cells[0] === id);
    assert(visible, `${label}: Pivot group ${id} must have a mounted row`);
    assert(visible.rowNumber > 0 && visible.rowNumber <= 25);
    const started = Date.now();
    const before = report.nativeRequests.length;
    await clickUI(`button[aria-label="Inspect row ${visible.rowNumber} identity"]`);
    const dialog = `[role="dialog"][aria-label="Row ${visible.rowNumber} identity"]`;
    await waitUI(`document.querySelector(${JSON.stringify(dialog)})`);
    await waitUI(`document.querySelector(${JSON.stringify(dialog + ' ul li')})`);
    const lineageRequests = () => report.nativeRequests.slice(before).filter(request => request.path === base + '/row-lineage');
    if (!lineageRequests().length) await requestCapture.waitFor(request => request.path === base + '/row-lineage' && request.startedAt >= started,
      { timeout: Math.max(1, started + 5000 - Date.now()) });
    await requestCapture.flush();
    assert(Date.now() - started <= 5000, `${label} inspector action exceeded five seconds`);
    const requests = lineageRequests();
    assert.equal(requests.length, 1, 'One direct Pivot row must use one bounded native lineage page');
    const request = requests[0];
    const selectedRowIdentity = await page.locator(`${dialog} p.font-mono`).textContent();
    assert.equal(selectedRowIdentity, expectedRowID, `${label} inspector must use the independently computed typed Pivot identity`);
    assert.equal(request.status, 200);
    const body = requestBody(request);
    const response = responseBody(request);
    assert.equal(body.outputId, outputId);
    assert.equal(body.rowId, expectedRowID);
    assert.equal(body.receiptId, savedPreview.receiptId, 'Native source inspection must bind to the current saved Preview receipt');
    assert.equal(body.offset ?? 0, 0);
    assert.equal(body.limit, 25);
    assert.equal(response.receiptId, savedPreview.receiptId);
    assert.equal(response.outputId, outputId);
    assert.equal(response.rowId, expectedRowID);
    assert.equal(response.hasMore, false);
    assert.equal(response.nextOffset ?? null, null);
    const contributors = response.contributors.map(item => [item.resourceType, item.resourceId, item.occurrenceKey]);
    const expectedContributors = sharedContributorPivot
      ? members.map(member => ['Observation', member.id, member._key])
      : [expectedContributorByID.get(id)];
    const tupleKey = tuple => tuple.join('\u0000');
    assert.deepEqual(contributors.map(tupleKey).sort(), expectedContributors.map(tupleKey).sort(),
      `${label} source tuples must exactly match the independent raw CDA member witness`);
    const visibleContributors = await page.locator(`${dialog} ul li`).allInnerTexts();
    assert.equal(visibleContributors.length, expectedContributors.length);
    assert.equal(new Set(visibleContributors).size, expectedContributors.length, `${label} must display every contributor once`);
    for (const contributor of expectedContributors) {
      assert(visibleContributors.some(line => line.includes(`${contributor[0]}/${contributor[1]}`)),
        `${label} does not display ${contributor[0]}/${contributor[1]}: ${JSON.stringify(visibleContributors)}`);
    }
    report.cases.push({ name: label, elapsedMs: Date.now() - started, rowID: expectedRowID, receiptId: savedPreview.receiptId, contributors });
    await clickUI(`${dialog} button`, { name: 'Close' });
    await waitUI(`!document.querySelector(${JSON.stringify(dialog)})`);
  };

  await open();
  await assertMountedRows(baselineRows, 'direct-pivot-starting-table');
  const configureStarted = await configurePivot();
  await typePivotOutputName('null');
  const proposalResponse = await proposal('direct-pivot-source-preview', configureStarted, expectedRows);
  pivotFromResponse(proposalResponse, 'null');
  await clickUI('[data-testid="construction-cancel-proposal"]');
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
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
  const inspectorKeys = sharedContributorPivot ? [groupKey] : members.map(member => member.id);
  for (const key of inspectorKeys) {
    const rowID = rowIDFor(identity, key);
    await inspect(key, rowID, firstOpenPreview, `direct-pivot-inspect-${key}`);
  }
  assert.deepEqual((await api(base + '/builder')).workspace, authoredWorkspace, 'Source inspection must not mutate the saved direct Pivot');

  const reloadStarted = Date.now();
  builder = await api(base + '/builder');
  await open();
  await assertMountedRows(expectedRows, 'direct-pivot-reload');
  const reloadedPreview = await waitForPreview(reloadStarted, 'reloaded direct Pivot');
  assert.deepEqual(reloadedPreview.rows.map(row => row.__loom_row_id).sort(), identity.rowIDs, 'Reload must retain exact typed Pivot row identities');
  assertPivotPreview(reloadedPreview, 'Reloaded direct Pivot');
  for (const key of inspectorKeys) {
    const rowID = rowIDFor(identity, key);
    await inspect(key, rowID, reloadedPreview, `direct-pivot-reload-inspect-${key}`);
  }
  assert.deepEqual((await api(base + '/builder')).workspace, authoredWorkspace, 'Reloaded source inspection must leave the saved Pivot unchanged');

  const editCancelStarted = Date.now();
  await clickUI(`[data-testid="construction-history-step-${identity.stepID}"]`);
  await waitUI(`document.querySelector('[data-testid=${JSON.stringify(`construction-edit-step-${identity.stepID}`)}]')?.disabled === false`);
  await clickUI(`[data-testid="construction-edit-step-${identity.stepID}"]`);
  await waitUI(`Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot output name "]'))`);
  await typePivotOutputName('direct_status');
  const editCancelProposal = await proposal('direct-pivot-output-name-cancel-preview', editCancelStarted, expectedRows);
  const editCancelIdentity = pivotFromResponse(editCancelProposal, 'direct_status');
  assert.equal(editCancelIdentity.constructionId, identity.constructionId, 'Editing the physical column name must preserve the Pivot row identity construction');
  assert.deepEqual(editCancelIdentity.rowIDs, identity.rowIDs, 'Previewed name edits must preserve the independently typed Pivot row IDs');
  await clickUI('[data-testid="construction-cancel-proposal"]');
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  const afterEditCancel = await api(base + '/builder');
  assert.deepEqual(afterEditCancel.workspace, authoredWorkspace, 'Canceling a Pivot name edit must preserve the exact saved null-name workspace');
  assert.equal(afterEditCancel.draftVersion, authoredDraftVersion, 'Canceling a Pivot name edit must preserve draft version');
  assert.equal(afterEditCancel.draftDigest, authoredDraftDigest, 'Canceling a Pivot name edit must preserve draft digest');
  const stillNullStep = doc(afterEditCancel).construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert.equal(stillNullStep.outputs.find(column => column.id === stillNullStep.operation.pivot.categories[0].outputColumnId)?.name, 'null');
  builder = afterEditCancel;
  await assertMountedRows(expectedRows, 'direct-pivot-name-edit-cancel-preserves-null-output');

  const editApplyStarted = Date.now();
  await clickUI(`[data-testid="construction-history-step-${identity.stepID}"]`);
  await waitUI(`document.querySelector('[data-testid=${JSON.stringify(`construction-edit-step-${identity.stepID}`)}]')?.disabled === false`);
  await clickUI(`[data-testid="construction-edit-step-${identity.stepID}"]`);
  await waitUI(`Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] input[aria-label^="Pivot output name "]'))`);
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
  for (const key of inspectorKeys) {
    await inspect(key, rowIDFor(editedIdentity, key), editedReloadPreview, `direct-pivot-edited-inspect-${key}`);
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
  const pivotEvidence = { stepID: identity.stepID, constructionId: identity.constructionId, selectionRevisionId: selection.id, category: categoryKey, nullPhysicalOutputName: 'null', editedPhysicalOutputName: 'direct_status', sourceIDs: members.map(member => member.id), rowIDs: identity.rowIDs, previewRows: expectedRows, restoredSourceRows: baselineRows, finalWorkspaceHasPivot: false };
  if (sharedContributorPivot) report.sharedContributorPivot = { ...pivotEvidence, groupKey, aggregateValue: members.reduce((total, member) => total + member.value, 0) };
  else report.directPivot = pivotEvidence;
};

try {
  if (groupCountPivot) {
    await runDirectGroupCountPivot();
    report.status = 'passed';
  } else if (directPivot) {
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
    await clickUI(`button[aria-label="Inspect row ${rowNumber} identity"]`);
    const dialog = `[role="dialog"][aria-label="Row ${rowNumber} identity"]`;
    await waitUI(`document.querySelector(${JSON.stringify(dialog)})`);
    await waitUI(`document.querySelector(${JSON.stringify(dialog + ' ul li')})`);
    const lineageRequests = () => report.nativeRequests.slice(before).filter(request => request.path === base + '/row-lineage');
    if (!lineageRequests().length) await requestCapture.waitFor(request => request.path === base + '/row-lineage' && request.startedAt >= started,
      { timeout: Math.max(1, started + 5000 - Date.now()) });
    assert(Date.now() - started <= 5000, `${label} native row-lineage action exceeded five seconds`);
    const initialDialogText = await page.locator(dialog).innerText();
    assert(!/cannot be listed|unavailable|could not be fully listed|Could not load/i.test(initialDialogText), initialDialogText);
    await requestCapture.flush();
    const requests = lineageRequests();
    assert.equal(requests.length, 1, `${expectedContributors.length} contributors must fit on one native inspector page`);
    assert.equal(await page.locator(`${dialog} button`).filter({ hasText: 'Show more source records' }).count(), 0);
    const selectedRowIdentity = await page.locator(`${dialog} p.font-mono`).textContent();
    assert(selectedRowIdentity, 'Native inspector must show the exact canonical row identity');
    const allTuples = [];
    for (let index = 0; index < requests.length; index += 1) {
      const request = requests[index];
      const body = requestBody(request);
      const response = responseBody(request);
      assert.equal(request.status, 200);
      assert.equal(body.outputId, outputId);
      assert.equal(body.rowId, selectedRowIdentity);
      assert(body.receiptId, 'Native row-lineage call must be receipt bound');
      assert.equal(body.limit, 25, 'Native inspector must use the existing bounded page size');
      assert.equal(response.receiptId, body.receiptId);
      assert.equal(response.outputId, outputId);
      assert.equal(response.rowId, selectedRowIdentity);
      const offset = body.offset ?? 0;
      const page = response.contributors.map(item => [item.resourceType, item.resourceId, item.occurrenceKey]);
      assert.equal(offset, 0, 'The first native inspector page must start at offset zero');
      assert.equal(page.length, expectedContributors.length);
      assert.equal(response.hasMore, false, 'The selected lineage must not require paging');
      assert.equal(response.nextOffset ?? null, null);
      allTuples.push(...page);
    }
    assert.deepEqual(allTuples, expectedContributors, 'Native response must exactly match the independent scoped oracle, including repeated authored occurrences');
    const visible = await page.locator(`${dialog} ul li`).allInnerTexts();
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
    const captured = requests.map(request => ({ request: evidenceSafe(requestBody(request)), response: evidenceSafe(responseBody(request)) }));
    report.cases.push({ name: label, rowNumber, elapsedMs: Date.now() - started, pageCount: requests.length, rowId: selectedRowIdentity, calls: captured });
    await clickUI(`${dialog} button`, { name: 'Close' });
    await waitUI(`!document.querySelector(${JSON.stringify(dialog)})`);
    return selectedRowIdentity;
  };

  const rowNumberFor = async (rowMap, label) => {
    const visibleRows = await inspectPreviewRows();
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
  await clickUI(`button[aria-label="Inspect row ${baseTarget.rowNumber} identity"]`);
  const baseDialog = `[role="dialog"][aria-label="Row ${baseTarget.rowNumber} identity"]`;
  await waitUI(`document.querySelector(${JSON.stringify(baseDialog)})?.innerText.includes('Starting FHIR record')`);
  const baseDialogText = await page.locator(baseDialog).innerText();
  assert(baseDialogText.includes(`Specimen/${rootDoc.id}`), 'Zero-step row inspector must identify the exact starting Specimen record');
  const baseRowIdentity = await page.locator(`${baseDialog} p.font-mono`).textContent();
  assert(baseRowIdentity, 'Starting-record inspector must show the stable row identity');
  assert.equal(report.nativeRequests.slice(beforeStartingInspector).filter(request => request.path === base + '/row-lineage').length, 0, 'A single starting record must not request composed row lineage');
  await clickUI(`${baseDialog} button`, { name: 'Close' });
  await waitUI(`!document.querySelector(${JSON.stringify(baseDialog)})`);
  assert(Date.now() - startingInspectorStarted <= 5000, 'Starting-record inspection exceeded five seconds');
  report.cases.push({ name: 'zero-step-starting-record-inspector', elapsedMs: Date.now() - startingInspectorStarted, rowNumber: baseTarget.rowNumber, rowIdentity: baseRowIdentity });
  assert.deepEqual((await api(base + '/builder')).workspace, builder.workspace, 'Starting-record inspection must not mutate the zero-step workspace');

  assert(report.nativeRequests.every(request => localOrigins.has(request.origin) && !request.authorizationHeaderPresent), 'Native Builder requests must remain on the local no-auth endpoints');
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
  }
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  throw error;
} finally {
  if (report.activeAction) {
    report.failedAction = { label: report.activeAction.label, locator: report.activeAction.locator,
      elapsedMs: Date.now() - report.activeAction.startedAt };
    delete report.activeAction;
  }
  await requestCapture.flush();
  await cda.attachReport(`composed-row-lineage-${lineageMode.toLowerCase()}-evidence.json`, report);
}
}
