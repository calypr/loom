import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import {
  cdaNullableEmptyRemovalPreviewEvidence,
  cdaNullableCodeJoinDirectSourceEvidence,
  cdaNullableValueQuantityCodeCandidateEvidence,
  cdaNullableValueQuantityCodeJoinOracle,
  frozenCdaNullableValueQuantityCodeWitness,
} from '../helpers/cda-nullable-code-join-oracle.mjs';
import {
  builderDraftStateEvidence,
  canceledDraftEvidence,
  constructionCandidateWireEquivalent,
  workspaceOutputOption,
} from '../helpers/builder-combine-draft-helpers.mjs';
import {
  builderRequestURL,
  nativeCombineTargetBindingEvidence,
  rootedEmptyTargetAppliedExpression,
  rootedEmptyTargetRestorationEvidence,
  sameSourceDocuments,
  snapshotSourceDocument,
} from '../helpers/builder-combine-helpers.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import { proposalPreviewReadinessExpression, readProposalPreviewState } from '../helpers/proposal-preview-readiness.mjs';

const generationExpected = 'cda-fhir-v1';
const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const encode = value => encodeURIComponent(value);
const display = value => value === null || value === undefined ? '—' : String(value);
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();

function runArango(arangoContainer, query) {
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 2 * 1024 * 1024 });
  assert.equal(result.status, 0, `Exact owned Arango read failed: ${tidy(result.stderr || result.stdout).slice(0, 1200)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Arango returned no JSON array: ${tidy(result.stdout).slice(-600)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  return rows;
}

export async function cdaCurrentDraftNullableCodeJoinWorkflow({ page, cda }) {
  const witness = frozenCdaNullableValueQuantityCodeWitness;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(cda.target);
  assert.equal(project, witness.project, 'Frozen nullable Join witness is scoped to the owned CDA project.');
  assert.equal(generation, generationExpected);
  assert.equal(generation, witness.generation);
  assert(apiOrigin && uiOrigin);

  const report = cda.report;
  report.phase = 'CDA current-draft nullable Observation.valueQuantity.code KEY_JOIN lifecycle';
  report.scope = { project, generation, keyPath: 'Observation.valueQuantity.code',
    rawOracleApplicationAuthorizationClaimed: false,
    authoringAuthorizationScope: 'Builder catalog and every selection revision are bound to the exact authorizationScopeDigest.' };
  report.cases ??= [];
  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `cda-nullable-code-join-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${tidy(text).slice(0, 900)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1200)}`);
    return value;
  };
  const basePath = explorer => `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}`;
  const runWitnessQuery = () => {
    const ids = [...witness.left, ...witness.right].map(row => row._id);
    const query = `FOR r IN Observation
      FILTER r._id IN ${JSON.stringify(ids)}
        AND r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
        AND r.payload.resourceType == "Observation"
      LET quantity = r.payload.valueQuantity
      LET codePresent = IS_OBJECT(quantity) ? HAS(quantity, "code") : false
      LET code = codePresent ? quantity.code : null
      SORT r._id
      RETURN {_id:r._id,id:r.id,project:r.project,generation:r.dataset_generation,
        resourceType:r.payload.resourceType,valueQuantityCodePresent:codePresent,valueQuantityCode:code}`;
    const rows = runArango(arangoContainer, query);
    assert.equal(rows.length, 6, 'The exact selected six-row raw read must be complete.');
    const byId = new Map(rows.map(row => [row.id, row]));
    assert.equal(byId.size, 6, 'The exact raw read must contain unique FHIR IDs.');
    const orderedSide = side => side.map(expected => {
      const actual = byId.get(expected.id);
      assert(actual, `Exact raw read omitted witness Observation ${expected.id}.`);
      assert.equal(actual._id, expected._id);
      assert.equal(actual.valueQuantityCodePresent, expected.code !== null);
      assert.equal(actual.valueQuantityCode, expected.code);
      return actual;
    });
    const leftRows = orderedSide(witness.left);
    const rightRows = orderedSide(witness.right);
    const oracle = cdaNullableValueQuantityCodeJoinOracle({ leftRows, rightRows, project, generation });
    return { query, rows, leftRows, rightRows, oracle };
  };
  const raw = runWitnessQuery();
  const rawRowsSha256 = createHash('sha256').update(JSON.stringify(raw.rows)).digest('hex');
  cda.check('correctness', 'Frozen exact-scope raw witness proves disjoint 2x2 code=d keys and one missing code per side',
    raw.oracle.innerRows.length === 4 && raw.oracle.leftRows.length === 5 &&
      raw.oracle.leftMissingIDs.length === 1 && raw.oracle.rightMissingIDs.length === 1,
    { project, generation, keyPath: 'valueQuantity.code', rawRowsSha256, query: raw.query,
      leftIDs: raw.oracle.leftIDs, rightIDs: raw.oracle.rightIDs,
      leftMissingIDs: raw.oracle.leftMissingIDs, rightMissingIDs: raw.oracle.rightMissingIDs,
      innerRows: raw.oracle.innerRows, leftRows: raw.oracle.leftRows,
      witnessArtifact: witness.artifact, catalogArtifact: witness.catalogArtifact,
      limits: ['exact six _id values only', 'project and generation filters applied', 'no broader query or claim about physical scan cost'] });

  const rootPath = `/api/v1/projects/${encode(project)}/explorers`;
  const explorer = `cda-nullable-code-join-${randomUUID()}`;
  const title = 'CDA Nullable Code Join QA';
  const created = await api(rootPath, { name: explorer, title });
  assert.equal(created.explorerId ?? created.id ?? created.explorer?.id, explorer);
  const base = basePath(explorer);
  const authoring = `${base}/authoring/v2`;
  const uiURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=builder`;
  const capture = cda.captureRequests(authoring, { responsePaths: /commands|construction-proposals/ });
  let builder = await api(`${authoring}/builder`);
  const empty = builderDraftStateEvidence(builder, 'empty');
  assert(empty.ok, `Fresh CDA Explorer must start with an empty draft: ${JSON.stringify(empty)}`);
  assert.equal(builder.catalog.generation, generation);
  const initialScope = {
    snapshotToken: builder.catalog.snapshotToken,
    authorizationScopeDigest: builder.catalog.authorizationScopeDigest,
    generation: builder.catalog.generation,
  };
  report.scope.authorizationScopeDigest = initialScope.authorizationScopeDigest;
  assert(initialScope.snapshotToken && initialScope.authorizationScopeDigest);
  const rootNodes = builder.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible === true);
  assert.equal(rootNodes.length, 1);
  const candidateEvidence = cdaNullableValueQuantityCodeCandidateEvidence(builder, generation);
  const builderURL = builderRequestURL(apiOrigin, project, explorer);
  const builderScopeEvidence = {
    exactProjectRoute: builderURL.origin === new URL(apiOrigin).origin &&
      builderURL.pathname === `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}/authoring/v2/builder`,
    explorerTitle: builder.workspace?.explorer?.title ?? null,
    exactExplorerTitle: builder.workspace?.explorer?.title === title,
    project, explorer, builderURL: builderURL.toString(),
    generation: builder.catalog.generation, snapshotToken: initialScope.snapshotToken,
    authorizationScopeDigest: initialScope.authorizationScopeDigest,
  };
  cda.check('correctness', 'valueQuantity.code candidate is scalar optional string in the exact project and catalog scope',
    candidateEvidence.ok && builderScopeEvidence.exactProjectRoute && builderScopeEvidence.exactExplorerTitle,
    { ...candidateEvidence, ...builderScopeEvidence,
      generation, snapshotToken: initialScope.snapshotToken,
      authorizationScopeDigest: initialScope.authorizationScopeDigest,
      nodePopulation: rootNodes[0]?.documentCount ?? null,
      catalogSnapshotBound: Boolean(initialScope.snapshotToken && initialScope.authorizationScopeDigest),
      nullableSourceProof: 'raw exact selected Observations contain absent code members; CatalogCandidate does not expose a nullable boolean' });
  assert(candidateEvidence.ok && builderScopeEvidence.exactProjectRoute && builderScopeEvidence.exactExplorerTitle,
    `Catalog candidate or exact builder scope is invalid: ${JSON.stringify({ candidateEvidence, builderScopeEvidence })}`);
  const codeCandidate = candidateEvidence.candidate;
  const idCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === candidateEvidence.rootNodeId && candidate.fieldPath === 'id');
  assert.equal(idCandidates.length, 1);

  const publishPath = `${authoring}/publish`;
  let publishRequests = 0;
  const onRequest = request => {
    try {
      const url = new URL(request.url());
      if ([apiOrigin, uiOrigin].some(origin => url.origin === new URL(origin).origin) && url.pathname === publishPath) publishRequests += 1;
    } catch {}
  };
  page.on('request', onRequest);
  const getDocument = (state, outputId) => {
    const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
    assert.equal(matches.length, 1, `Expected exactly one workspace document ${outputId}.`);
    return matches[0];
  };
  const readBuilder = () => api(`${authoring}/builder`);
  const checkScope = state => {
    assert.equal(state.catalog.generation, initialScope.generation);
    assert.equal(state.catalog.snapshotToken, initialScope.snapshotToken);
    assert.equal(state.catalog.authorizationScopeDigest, initialScope.authorizationScopeDigest);
  };
  const command = async commands => {
    const state = builder;
    const response = await api(`${authoring}/commands`, {
      commandId: randomUUID(), semanticsVersion: state.workspace?.semanticsVersion ?? 10,
      snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest, commands,
    });
    builder = await readBuilder();
    checkScope(builder);
    return response;
  };
  const action = (name, locator, perform, after) => cda.action(name, locator, perform, {
    timeout: 5_000, budget: 5_000,
    ...(after ? { after, requiredCheck: 'All native nullable Join lifecycle actions complete within five seconds' } : {}),
  });
  const waitSelector = selector => page.locator(selector).waitFor({ state: 'visible', timeout: 5_000 });
  const waitFunction = predicate => page.waitForFunction(predicate, undefined, { timeout: 5_000 });
  const waitApplyColumns = () => waitFunction("[...document.querySelectorAll('button')].some(button=>button.textContent.trim()==='Apply columns'&&!button.disabled)");
  const proposalReady = (outputId, count) => proposalPreviewReadinessExpression(outputId, count);
  const waitProposal = (outputId, count) => waitFunction(proposalReady(outputId, count));
  const savedReady = (outputId, count) => `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');const t=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(p?.getAttribute('data-preview-status')==='ready'&&p.getAttribute('data-preview-output-id')===${JSON.stringify(outputId)}&&t?.getAttribute('aria-rowcount')===${JSON.stringify(String(count + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
  const emptyReady = outputId => rootedEmptyTargetAppliedExpression(outputId);
  const readGrid = async kind => page.evaluate(({ kind }) => {
    const proposal = kind === 'proposal';
    const table = proposal
      ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"] table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { ready: false, headers: [], rows: [], ariaRowCount: null };
    const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = proposal
      ? [...table.querySelectorAll('thead th')].map(cell => clean(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => clean(cell.textContent));
    const rows = proposal
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => clean(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => clean(cell.innerText)));
    return { ready: true, headers, rows, ariaRowCount: table.getAttribute('aria-rowcount') };
  }, { kind });
  const assertGrid = (name, grid, headers, rows, evidence = {}) => {
    const actualRows = sortRows(grid.rows);
    const expectedRows = sortRows(rows);
    const ok = grid.ready && isDeepStrictEqual(grid.headers, headers) && isDeepStrictEqual(actualRows, expectedRows);
    cda.check('correctness', name, ok, { headers: grid.headers, expectedHeaders: headers,
      rows: grid.rows, expectedRows: rows, ariaRowCount: grid.ariaRowCount, ...evidence });
    assert(ok, `${name}: visible rows differ from the literal raw Oracle.`);
    return grid;
  };
  const waitSaved = (outputId, count) => waitFunction(savedReady(outputId, count));
  const selectTable = async (outputId, count, label) => {
    const locator = page.getByTestId(`construction-table-${outputId}`);
    await waitSelector(`[data-testid="construction-table-${outputId}"]`);
    const ready = () => count === undefined ? waitFunction(emptyReady(outputId)) : waitSaved(outputId, count);
    if (await locator.getAttribute('aria-current') === 'page') {
      await ready();
      return;
    }
    await action(label, locator, target => target.click({ timeout: 5_000 }), async () => ready());
  };
  const reloadTable = async (outputId, count, label) => {
    const locator = page.getByTestId(`construction-table-${outputId}`);
    await action(label, locator, async target => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: 5_000 });
      await waitSelector(`[data-testid="construction-table-${outputId}"]`);
      if (await target.getAttribute('aria-current') !== 'page') await target.click({ timeout: 5_000 });
    }, async () => count === undefined ? waitFunction(emptyReady(outputId)) : waitSaved(outputId, count));
  };
  const selectColumnById = async (selector, expectedId) => {
    const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({ value: option.value, disabled: option.disabled })));
    const choices = options.filter(option => option.value === expectedId && !option.disabled);
    assert.equal(choices.length, 1, `Expected exact current-draft column ID ${expectedId}: ${JSON.stringify(options)}`);
    await action(`Choose Join key or projection ${expectedId}`, page.locator(selector), locator => locator.selectOption(expectedId, { timeout: 5_000 }));
  };
  const outputOption = async (selector, outputId) => {
    const expected = workspaceOutputOption(outputId);
    const options = await page.locator(selector).evaluate(select => [...select.options].map(option => ({
      value: option.value, group: option.parentElement?.label ?? '', disabled: option.disabled,
    })));
    const choices = options.filter(option => option.value === expected && option.group === 'Current draft tables' && !option.disabled);
    assert.equal(choices.length, 1, `Expected exactly the selected current-draft output ${outputId}: ${JSON.stringify(options)}`);
    return expected;
  };
  const sourceTables = [];
  const addDirectCodeColumn = async source => {
    await cda.navigate(uiURL);
    await selectTable(source.outputId, 3, `Select raw ${source.side} Observation population`);
    const add = page.getByTestId('construction-action-add-columns');
    await action(`Open raw ${source.side} Observation field picker`, add, locator => locator.click({ timeout: 5_000 }),
      async () => waitSelector('[aria-label="Add columns editor"]'));
    await action(`Open raw field list for ${source.side} source`, page.getByRole('button', { name: 'Fields and related data', exact: true }),
      locator => locator.click({ timeout: 5_000 }));
    await action(`Open advanced raw FHIR fields for ${source.side} source`, page.getByText('Raw FHIR fields (advanced)', { exact: true }),
      locator => locator.click({ timeout: 5_000 }));
    const checkbox = 'input[type="checkbox"][aria-label="Select Observation.valueQuantity.code"]';
    await waitSelector(checkbox);
    await action(`Select Observation.valueQuantity.code for ${source.side} source`, page.locator(checkbox),
      locator => locator.check({ timeout: 5_000 }));
    const addSelected = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    await action(`Add scalar valueQuantity.code to ${source.side} source`, addSelected,
      locator => locator.click({ timeout: 5_000 }), async () => waitApplyColumns());
    await action(`Apply raw code column to ${source.side} source`, page.getByRole('button', { name: 'Apply columns', exact: true }),
      locator => locator.click({ timeout: 5_000 }), async () => waitSaved(source.outputId, 3));
    const close = page.getByRole('button', { name: 'Close operation editor', exact: true });
    if (await close.count()) await action(`Return to raw ${source.side} source table`, close,
      locator => locator.click({ timeout: 5_000 }), async () => waitSaved(source.outputId, 3));
    builder = await readBuilder();
    checkScope(builder);
    const document = getDocument(builder, source.outputId);
    assert.equal(document.rootResourceType, 'Observation');
    assert.equal(document.population?.selectionRevisionId, source.selection.id);
    assert.equal(document.construction?.steps?.length ?? 0, 0,
      'Raw Join inputs must remain direct selected Observation tables without Group/Pivot operations.');
    const idColumn = document.columns.find(column => column.source?.field?.path === 'id');
    const codeColumns = document.columns.filter(column => column.source?.field?.path === 'valueQuantity.code');
    assert(idColumn?.id);
    assert.equal(codeColumns.length, 1);
    const codeColumn = codeColumns[0];
    assert.equal(codeColumn.source.field.projectionMode, 'VALUE');
    assert.equal(codeColumn.source.field.candidateId, codeCandidate.candidateId);
    source.idColumn = idColumn;
    source.codeColumn = codeColumn;
    source.document = structuredClone(document);
    source.snapshot = snapshotSourceDocument(document);
    const grid = await readGrid('saved');
    const expectedRows = source.rows.map(row => [row.id, display(row.valueQuantityCode)]);
    assertGrid(`${source.side} raw selected Observation rows expose exact IDs and code/missing cells`, grid,
      [idColumn.label, codeColumn.label], expectedRows, { outputId: source.outputId,
        selectionId: source.selection.id, candidateId: codeColumn.source.field.candidateId,
        projectionMode: codeColumn.source.field.projectionMode, codeCandidate: candidateEvidence });
    return source;
  };
  const createSource = async (side, rows) => {
    builder = await readBuilder();
    checkScope(builder);
    const existing = new Set(builder.workspace.documents.map(document => document.output.id));
    const create = await command([{ type: 'CREATE_TABLE', title: `CDA nullable ${side} raw source`, rootNodeId: candidateEvidence.rootNodeId }]);
    const createdOutput = (create.results ?? []).filter(result => result.type === 'TABLE_CREATED' && !existing.has(result.outputId));
    assert.equal(createdOutput.length, 1);
    const outputId = createdOutput[0].outputId;
    const idCandidate = idCandidates[0];
    await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: `${side} Observation ID` }]);
    const selection = await api(`${base}/selections`, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: `cda-nullable-code-join-${side}-${randomUUID()}`,
      source: { kind: 'resources', resources: { refs: rows.map(row => ({ project, generation, resourceType: 'Observation', id: row.id })) } },
    });
    assert.equal(selection.project, project);
    assert.equal(selection.generation, generation);
    assert.equal(selection.resourceType, 'Observation');
    assert.equal(selection.scopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(selection.memberCount, 3);
    const reread = await api(`${base}/selections/${encode(selection.id)}?limit=100`);
    assert.equal(reread.revision?.id, selection.id);
    assert.equal(reread.revision?.project, project);
    assert.equal(reread.revision?.generation, generation);
    assert.equal(reread.revision?.resourceType, 'Observation');
    assert.equal(reread.revision?.scopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(reread.revision?.memberCount, 3);
    const refs = (reread.members ?? []).map(member => `${member.ref.project}/${member.ref.generation}/${member.ref.resourceType}/${member.ref.id}`).sort();
    const expectedRefs = rows.map(row => `${project}/${generation}/Observation/${row.id}`).sort();
    assert.deepEqual(refs, expectedRefs);
    const routes = await api(`${authoring}/population-routes`, {
      snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
    });
    const direct = (routes.choices ?? []).filter(choice => Array.isArray(choice.route) && choice.route.length === 0);
    assert.equal(direct.length, 1, 'The exact selected Observation IDs must support one direct root population route.');
    await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct[0].routeChoiceId }]);
    const populated = getDocument(builder, outputId);
    assert.equal(populated.population?.selectionRevisionId, selection.id);
    assert.equal(populated.population?.route?.length ?? 0, 0);
    assert.equal(populated.rootResourceType, 'Observation');
    assert.equal(populated.construction?.steps?.length ?? 0, 0);
    assert.equal(populated.columns.length, 1);
    assert.equal(populated.columns[0].source?.field?.path, 'id');
    const source = { side, outputId, selection, selectionRefs: refs, rows };
    sourceTables.push(source);
    cda.check('correctness', `${side} raw selection binds exact scoped IDs to a direct Observation source table`, true,
      { side, project, generation, outputId, selectionId: selection.id, selectionRefs: refs,
        selectionScopeDigest: selection.scopeDigest, routeChoiceId: direct[0].routeChoiceId,
        constructionSteps: populated.construction?.steps ?? [] });
    return addDirectCodeColumn(source);
  };
  try {
    const leftSource = await createSource('left', raw.leftRows);
    const rightSource = await createSource('right', raw.rightRows);
    const sourceOutputIDs = [leftSource.outputId, rightSource.outputId];
    assert.notEqual(leftSource.outputId, rightSource.outputId);
    assert.deepEqual(sourceTables.map(source => source.rows.map(row => row.id)), [raw.oracle.leftIDs, raw.oracle.rightIDs]);
    const sourceBaseBuilder = structuredClone(await readBuilder());
    checkScope(sourceBaseBuilder);
    const sourceBaseDocuments = sourceOutputIDs.map(outputId => snapshotSourceDocument(getDocument(sourceBaseBuilder, outputId)));
    const codeColumns = [leftSource.codeColumn, rightSource.codeColumn];
    assert(codeColumns.every(column => column.source?.field?.candidateId === codeCandidate.candidateId &&
      column.source?.field?.path === 'valueQuantity.code' && column.source?.field?.projectionMode === 'VALUE'));
    cda.check('persistence', 'Both Join sources are distinct direct raw Observation selections with exact valueQuantity.code columns', true,
      { sourceOutputIDs, selectionIDs: [leftSource.selection.id, rightSource.selection.id],
        selectionRefs: [leftSource.selectionRefs, rightSource.selectionRefs],
        sourceSteps: [getDocument(sourceBaseBuilder, leftSource.outputId).construction?.steps ?? [],
          getDocument(sourceBaseBuilder, rightSource.outputId).construction?.steps ?? []],
        sourceColumns: [getDocument(sourceBaseBuilder, leftSource.outputId).columns,
          getDocument(sourceBaseBuilder, rightSource.outputId).columns] });

    const headers = ['Left Observation ID', 'Left valueQuantity.code', 'Right Observation ID', 'Right valueQuantity.code'];
    const expectedInner = raw.oracle.innerRows.map(row => row.map(display));
    const expectedLeft = raw.oracle.leftRows.map(row => row.map(display));
    const sourceColumnIDs = [leftSource.idColumn.id, leftSource.codeColumn.id, rightSource.idColumn.id, rightSource.codeColumn.id];
    const mappingNames = ['left_observation_id', 'left_code', 'right_observation_id', 'right_code'];
    const mappings = [
      { inputIndex: 1, sourceColumnId: sourceColumnIDs[0] },
      { inputIndex: 1, sourceColumnId: sourceColumnIDs[1] },
      { inputIndex: 2, sourceColumnId: sourceColumnIDs[2] },
      { inputIndex: 2, sourceColumnId: sourceColumnIDs[3] },
    ];
    const sources = [leftSource, rightSource];
    let target;
    let expectedOutputColumnIDs;

    const captureProposal = async ({ fromIndex, outputId, baseState }) => {
      const event = await capture.waitFor(entry => entry.path === `${authoring}/construction-proposals` &&
        entry.method === 'POST' && entry.status === 200 && entry.completedAt && entry.body?.outputId === outputId,
      { fromIndex, timeoutMs: 5_000 });
      const requestBody = capture.rawRequestBody(event);
      const responseBody = capture.rawResponseBody(event);
      assert(requestBody && responseBody, 'Native Join proposal request and response must be retained.');
      assert.equal(event.origin, new URL(uiOrigin).origin, 'Native candidate proposal must use the owned UI proxy.');
      assert.equal(event.path, `${authoring}/construction-proposals`);
      assert.equal(requestBody.outputId, outputId);
      assert.equal(responseBody.outputId, outputId);
      assert.equal(responseBody.previewStatus, 'READY');
      assert.equal(responseBody.snapshotToken, requestBody.snapshotToken);
      assert.equal(responseBody.draftVersion, requestBody.expectedDraftVersion);
      assert.equal(responseBody.draftDigest, requestBody.expectedDraftDigest);
      assert.equal(requestBody.snapshotToken, baseState.catalog.snapshotToken);
      assert.equal(requestBody.expectedDraftVersion, baseState.draftVersion);
      assert.equal(requestBody.expectedDraftDigest, baseState.draftDigest);
      assert(constructionCandidateWireEquivalent(requestBody.candidateConstruction, responseBody.candidateConstruction));
      const dom = await page.evaluate(() => ({
        proposalId: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-id') ?? null,
        receiptId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-receipt-id') ?? null,
        outputId: document.querySelector('[data-testid="construction-proposal-preview"]')?.getAttribute('data-preview-output-id') ?? null,
      }));
      assert.equal(dom.outputId, outputId);
      assert.equal(dom.proposalId, responseBody.proposalId);
      assert.equal(dom.receiptId, responseBody.proposalId);
      assert.equal(responseBody.preview?.receiptId, responseBody.proposalId);
      assert.equal(responseBody.preview?.outputId, outputId);
      cda.check('correctness', 'Join proposal preview response binds the exact UI route, receipt, output, and draft CAS', true,
        { project, explorer, routeOrigin: event.origin, routePath: event.path, method: event.method,
          outputId, requestSnapshotToken: requestBody.snapshotToken,
          requestDraftVersion: requestBody.expectedDraftVersion,
          requestDraftDigest: requestBody.expectedDraftDigest,
          responseSnapshotToken: responseBody.snapshotToken,
          responseDraftVersion: responseBody.draftVersion,
          responseDraftDigest: responseBody.draftDigest,
          proposalId: responseBody.proposalId, dom });
      return { event, requestBody, responseBody, dom };
    };
    const makeTarget = async (recordCheck = true) => {
      builder = await readBuilder();
      checkScope(builder);
      const rootNodeIDs = builder.catalog.nodes.filter(node => node.resourceType === 'Observation' && node.rowRootEligible).map(node => node.nodeId);
      const priorOutputIDs = builder.workspace.documents.map(document => document.output.id);
      const fromIndex = capture.startIndex();
      await cda.navigate(uiURL);
      const combineAction = page.getByTestId('construction-action-combine');
      await action('Open native Combine to create the empty nullable Join target', combineAction,
        locator => locator.click({ timeout: 5_000 }), async () => waitSelector('[data-testid="construction-combine-editor"]'));
      const event = await capture.waitFor(entry => entry.path === `${authoring}/commands` && entry.method === 'POST' &&
        entry.status === 200 && entry.completedAt && entry.body?.commands?.some(command => command.type === 'CREATE_TABLE'),
      { fromIndex, timeoutMs: 5_000 });
      const requestBody = capture.rawRequestBody(event);
      const response = capture.rawResponseBody(event);
      const mountedOutputID = await page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]').getAttribute('data-output-id');
      const targetBinding = nativeCombineTargetBindingEvidence({ requestBody, responseStatus: event.status, response,
        expectedRootNodeIds: rootNodeIDs, expectedRootResourceType: 'Observation', previousOutputIds: priorOutputIDs,
        mountedOutputId: mountedOutputID });
      assert(targetBinding.ok, `Native Combine must create a new rooted empty target: ${JSON.stringify(targetBinding)}`);
      builder = await readBuilder();
      checkScope(builder);
      const baselineDocument = structuredClone(getDocument(builder, targetBinding.outputId));
      const target = { outputId: targetBinding.outputId, baselineDocument, createBase: structuredClone(builder), rootResourceType: 'Observation' };
      if (recordCheck) {
        cda.check('correctness', 'Native Combine creates a scoped empty Observation target for the current-draft Join', true,
          { targetBinding, project, generation, snapshotToken: builder.catalog.snapshotToken,
            authorizationScopeDigest: builder.catalog.authorizationScopeDigest });
      }
      return target;
    };
    const configureJoin = async (joinType, expectedRows) => {
      const fromIndex = capture.startIndex();
      const baseState = structuredClone(await readBuilder());
      checkScope(baseState);
      await action('Choose native KEY_JOIN for raw CDA sources', page.getByTestId('construction-combine-choice-key_join'),
        locator => locator.click({ timeout: 5_000 }), async () => waitSelector('select[aria-label="Input table 1"]'));
      for (let index = 0; index < sources.length; index += 1) {
        const selector = `select[aria-label="Input table ${index + 1}"]`;
        const value = await outputOption(selector, sources[index].outputId);
        await action(`Bind nullable Join input ${index + 1} to direct raw source`, page.locator(selector),
          locator => locator.selectOption(value, { timeout: 5_000 }));
      }
      await selectColumnById('select[aria-label="Matching pair 1 first field"]', sourceColumnIDs[1]);
      await selectColumnById('select[aria-label="Matching pair 1 second field"]', sourceColumnIDs[3]);
      const policy = 'select[aria-label="If a row in the first table has no match"]';
      await action(`Set raw nullable KEY_JOIN policy to ${joinType}`, page.locator(policy),
        locator => locator.selectOption(joinType, { timeout: 5_000 }));
      for (let index = 0; index < mappings.length; index += 1) {
        const field = index + 1;
        const nameSelector = `input[aria-label="Output field ${field} name"]`;
        await action(`Add raw Join output field ${field}`, page.getByRole('button', { name: 'Add output field', exact: true }),
          locator => locator.click({ timeout: 5_000 }), async () => waitSelector(nameSelector));
        await action(`Name raw Join output ${field}`, page.locator(nameSelector), locator => locator.fill(mappingNames[index], { timeout: 5_000 }));
        const labelSelector = `input[aria-label="Output field ${field} label"]`;
        await action(`Label raw Join output ${field}`, page.locator(labelSelector), locator => locator.fill(headers[index], { timeout: 5_000 }));
        const selector = `select[aria-label="Output field ${field} source field in input ${mappings[index].inputIndex}"]`;
        await waitSelector(selector);
        if (index < mappings.length - 1) await selectColumnById(selector, mappings[index].sourceColumnId);
        else mappings[index].finalSelector = selector;
      }
      const final = mappings.at(-1);
      const options = await page.locator(final.finalSelector).evaluate(select => [...select.options].map(option => ({ value: option.value, disabled: option.disabled })));
      const finalChoice = options.filter(option => option.value === final.sourceColumnId && !option.disabled);
      assert.equal(finalChoice.length, 1, 'Final raw Join output must select the exact right-side key column ID.');
      const startedAt = Date.now();
      await action(`Render native ${joinType} nullable code Join preview`, page.locator(final.finalSelector),
        locator => locator.selectOption(final.sourceColumnId, { timeout: 5_000 }), async () => waitProposal(target.outputId, expectedRows.length));
      const proposal = await captureProposal({ fromIndex, outputId: target.outputId, baseState });
      const rows = await readGrid('proposal');
      assertGrid(`${joinType} raw nullable code Join preview matches the literal raw-row Oracle`, rows, headers, expectedRows,
        { joinKey: 'd', expectedInnerCartesianPairs: 4, leftMissingIDs: raw.oracle.leftMissingIDs,
          rightMissingIDs: raw.oracle.rightMissingIDs, explicitNullSemanticsClaimed: false });
      const step = proposal.requestBody.candidateConstruction.steps.at(-1);
      const combine = step?.operation?.combine;
      expectedOutputColumnIDs = step.outputs.map(output => output.id);
      assert(expectedOutputColumnIDs.length === mappings.length && expectedOutputColumnIDs.every(Boolean),
        'Every native nullable Join projection must retain a stable output column ID.');
      assert.equal(step?.operation?.kind, 'COMBINE');
      assert.equal(combine?.kind, 'KEY_JOIN');
      assert.equal(combine?.joinType, joinType);
      assert.deepEqual(step.inputs, sources.map(source => ({ kind: 'WORKSPACE_OUTPUT', outputId: source.outputId })));
      const sourceEvidence = cdaNullableCodeJoinDirectSourceEvidence({ inputs: step.inputs,
        expectedOutputIDs: sourceOutputIDs, expectedSelectionRevisionIDs: sources.map(source => source.selection.id),
        sourceDocuments: baseState.workspace.documents });
      assert(sourceEvidence.ok, `Join candidate must bind the two direct raw Observation sources: ${JSON.stringify(sourceEvidence)}`);
      assert.deepEqual(combine.keys, [{ leftColumnId: sourceColumnIDs[1], rightColumnId: sourceColumnIDs[3] }]);
      const expectedProjection = sourceColumnIDs.map((columnId, index) => ({
        outputColumnId: step.outputs[index]?.id, inputIndex: mappings[index].inputIndex - 1, inputColumnId: columnId,
      }));
      assert.deepEqual(step.outputs.map(output => ({ name: output.name, label: output.label })),
        mappingNames.map((name, index) => ({ name, label: headers[index] })));
      assert.deepEqual(combine.projections.map(item => ({ outputColumnId: item.outputColumnId,
        inputIndex: item.inputIndex, inputColumnId: item.inputColumnId })), expectedProjection);
      assert(step.inputs.every(input => input.kind === 'WORKSPACE_OUTPUT' && !input.tableId && !input.revisionId));
      assert.equal(proposal.responseBody.preview?.rowCount, expectedRows.length);
      cda.check('correctness', `${joinType} Join proposal binds exact raw sources, code keys, outputs, and draft CAS`, true,
        { outputId: target.outputId, joinType, sourceOutputIDs, sourceColumnIDs, inputs: step.inputs,
          keys: combine.keys, outputColumnIDs: expectedOutputColumnIDs, projections: combine.projections, proposalId: proposal.responseBody.proposalId,
          rawSourceBinding: sourceEvidence,
          snapshotToken: baseState.catalog.snapshotToken, draftVersion: baseState.draftVersion,
          draftDigest: baseState.draftDigest, expectedRows, actualRows: rows.rows,
          actionElapsedMs: Date.now() - startedAt });
      return { baseState, stepId: step.id, proposal };
    };
    const configurePolicy = async (joinType, expectedRows, currentTarget) => {
      const baseState = structuredClone(await readBuilder());
      checkScope(baseState);
      const fromIndex = capture.startIndex();
      const selector = 'select[aria-label="If a row in the first table has no match"]';
      await action(`Set saved raw Join policy to ${joinType}`, page.locator(selector),
        locator => locator.selectOption(joinType, { timeout: 5_000 }), async () => waitProposal(currentTarget.outputId, expectedRows.length));
      const proposal = await captureProposal({ fromIndex, outputId: currentTarget.outputId, baseState });
      assertGrid(`${joinType} saved edit preview matches the literal raw-row Oracle`, await readGrid('proposal'), headers, expectedRows,
        joinType === 'LEFT' ? { leftMissingIDs: raw.oracle.leftMissingIDs,
          rightMissingIDs: raw.oracle.rightMissingIDs, ordinaryMissingEqualityMatches: 0,
          explicitNullSemanticsClaimed: false } : {});
      const step = proposal.requestBody.candidateConstruction.steps.at(-1);
      const combine = step?.operation?.combine;
      const previousStep = getDocument(baseState, currentTarget.outputId).construction?.steps?.at(-1);
      const candidateOutputColumnIDs = step?.outputs?.map(output => output.id) ?? [];
      const expectedProjection = sourceColumnIDs.map((columnId, index) => ({
        outputColumnId: step?.outputs?.[index]?.id,
        inputIndex: mappings[index].inputIndex - 1,
        inputColumnId: columnId,
      }));
      const exactBinding = step?.id === previousStep?.id && step?.operation?.kind === 'COMBINE' &&
        combine?.kind === 'KEY_JOIN' && combine.joinType === joinType &&
        isDeepStrictEqual(step.inputs, sources.map(source => ({ kind: 'WORKSPACE_OUTPUT', outputId: source.outputId }))) &&
        isDeepStrictEqual(combine.keys, [{ leftColumnId: sourceColumnIDs[1], rightColumnId: sourceColumnIDs[3] }]) &&
        isDeepStrictEqual(step.outputs.map(output => ({ name: output.name, label: output.label })),
          mappingNames.map((name, index) => ({ name, label: headers[index] }))) &&
        isDeepStrictEqual(candidateOutputColumnIDs, expectedOutputColumnIDs) &&
        isDeepStrictEqual(combine.projections.map(item => ({ outputColumnId: item.outputColumnId,
          inputIndex: item.inputIndex, inputColumnId: item.inputColumnId })), expectedProjection) &&
        cdaNullableCodeJoinDirectSourceEvidence({ inputs: step.inputs, expectedOutputIDs: sourceOutputIDs,
          expectedSelectionRevisionIDs: sources.map(source => source.selection.id),
          sourceDocuments: baseState.workspace.documents }).ok;
      cda.check('correctness', `${joinType} Join proposal binds exact raw sources, code keys, outputs, and draft CAS`, exactBinding,
        { outputId: currentTarget.outputId, joinType, stepID: step?.id, previousStepID: previousStep?.id,
          sourceOutputIDs, sourceColumnIDs, outputColumnIDs: candidateOutputColumnIDs, inputs: step?.inputs, keys: combine?.keys,
          projections: combine?.projections, proposalId: proposal.responseBody.proposalId,
          snapshotToken: baseState.catalog.snapshotToken, draftVersion: baseState.draftVersion,
          draftDigest: baseState.draftDigest, expectedRows });
      assert(exactBinding, `${joinType} edit proposal changed source, key, output, or draft-CAS bindings.`);
      return { baseState, stepId: step?.id, proposal };
    };
    const assertSavedJoin = (state, joinType, stepID) => {
      const document = getDocument(state, target.outputId);
      const steps = document.construction?.steps ?? [];
      const step = steps.at(-1);
      const combine = step?.operation?.combine;
      const outputs = step?.outputs ?? [];
      const exact = document.rootResourceType === 'Observation' && steps.length === 1 &&
        step?.id === stepID && step.operation.kind === 'COMBINE' && combine?.kind === 'KEY_JOIN' && combine.joinType === joinType &&
        isDeepStrictEqual(step.inputs, sources.map(source => ({ kind: 'WORKSPACE_OUTPUT', outputId: source.outputId }))) &&
        cdaNullableCodeJoinDirectSourceEvidence({ inputs: step.inputs, expectedOutputIDs: sourceOutputIDs,
          expectedSelectionRevisionIDs: sources.map(source => source.selection.id),
          sourceDocuments: state.workspace.documents }).ok &&
        isDeepStrictEqual(combine.keys, [{ leftColumnId: sourceColumnIDs[1], rightColumnId: sourceColumnIDs[3] }]) &&
        isDeepStrictEqual(outputs.map(output => output.id), expectedOutputColumnIDs) &&
        isDeepStrictEqual(outputs.map(output => ({ name: output.name, label: output.label })), mappingNames.map((name, index) => ({ name, label: headers[index] }))) &&
        isDeepStrictEqual(combine.projections.map(item => [item.inputIndex, item.inputColumnId]), mappings.map(mapping => [mapping.inputIndex - 1, mapping.sourceColumnId]));
      cda.check('persistence', `Saved ${joinType} Join retains exact current-draft source and code-column bindings`, exact,
        { outputId: target.outputId, rootResourceType: document.rootResourceType, step, combine,
          expectedInputs: sources.map(source => ({ kind: 'WORKSPACE_OUTPUT', outputId: source.outputId })),
          expectedKey: { leftColumnId: sourceColumnIDs[1], rightColumnId: sourceColumnIDs[3] },
          sourceColumnIDs, stepCount: steps.length });
      assert(exact, `Saved ${joinType} Join binding changed.`);
      return step;
    };
    const applyJoin = async (joinType, expectedRows, configured) => {
      await action(`Apply ${joinType} raw nullable Join`, page.getByTestId('construction-apply-proposal'),
        locator => locator.click({ timeout: 5_000 }), async () => waitSaved(target.outputId, expectedRows.length));
      builder = await readBuilder();
      checkScope(builder);
      const casAdvanced = builder.draftVersion > configured.baseState.draftVersion && builder.draftDigest !== configured.baseState.draftDigest;
      assert(casAdvanced, `Applying ${joinType} Join must advance both draft version and digest.`);
      const savedStep = assertSavedJoin(builder, joinType, configured.stepId);
      assertGrid(`Applied ${joinType} Join visible rows match the raw Oracle`, await readGrid('saved'), headers, expectedRows);
      cda.check('persistence', `${joinType} Apply advances draft CAS`, casAdvanced, {
        beforeVersion: configured.baseState.draftVersion, afterVersion: builder.draftVersion,
        beforeDigest: configured.baseState.draftDigest, afterDigest: builder.draftDigest,
      });
      await reloadTable(target.outputId, expectedRows.length, `Reload applied ${joinType} raw nullable Join`);
      builder = await readBuilder();
      checkScope(builder);
      assertSavedJoin(builder, joinType, savedStep.id);
      assertGrid(`Reloaded ${joinType} Join preserves exact raw rows`, await readGrid('saved'), headers, expectedRows);
      return { step: savedStep, builder: structuredClone(builder) };
    };
    const cancelProposal = async (baseState, label, expectedRows, empty = false) => {
      await action(label, page.getByTestId('construction-cancel-proposal'), locator => locator.click({ timeout: 5_000 }),
        async () => waitFunction(`!document.querySelector('[data-testid="construction-proposal-panel"]')&&!document.querySelector('[data-testid="construction-combine-editor"]')`));
      await reloadTable(target.outputId, empty ? undefined : expectedRows.length, `${label} reload target`);
      const after = await readBuilder();
      checkScope(after);
      const evidence = canceledDraftEvidence(baseState, after);
      cda.check('persistence', `${label} preserves full workspace and draft CAS after reload`, evidence.ok, evidence);
      assert(evidence.ok, `${label} changed workspace or CAS: ${JSON.stringify(evidence)}`);
      if (empty) {
        assert.deepEqual(getDocument(after, target.outputId), getDocument(baseState, target.outputId));
      } else {
        assertSavedJoin(after, expectedRows === expectedInner ? 'INNER' : 'LEFT', getDocument(baseState, target.outputId).construction.steps.at(-1).id);
        assertGrid(`${label} restores exact saved rows after reload`, await readGrid('saved'), headers, expectedRows);
      }
      builder = after;
      return after;
    };
    const openSavedEdit = async (step, rowCount, label) => {
      await selectTable(target.outputId, rowCount, `Select saved raw Join before ${label}`);
      const history = page.getByTestId(`construction-history-step-${step.id}`);
      const edit = page.getByTestId(`construction-edit-step-${step.id}`);
      await action(`Open saved raw Join history before ${label}`, history, locator => locator.click({ timeout: 5_000 }),
        async () => { await edit.waitFor({ state: 'visible', timeout: 5_000 }); await assertEnabled(edit); });
      await action(label, edit, locator => locator.click({ timeout: 5_000 }),
        async () => waitSelector('[data-testid="construction-combine-editor"]'));
    };
    const assertEnabled = async locator => {
      await locator.waitFor({ state: 'visible', timeout: 5_000 });
      assert.equal(await locator.isEnabled(), true);
    };
    const removalProposal = async (step, expectedRows) => {
      await selectTable(target.outputId, expectedRows.length, 'Select saved Join before removal');
      const history = page.getByTestId(`construction-history-step-${step.id}`);
      const remove = page.getByTestId(`construction-remove-step-${step.id}`);
      await action('Open saved Join history before removal', history, locator => locator.click({ timeout: 5_000 }),
        async () => assertEnabled(remove));
      const baseState = structuredClone(await readBuilder());
      const fromIndex = capture.startIndex();
      await action('Preview raw nullable Join removal', remove, locator => locator.click({ timeout: 5_000 }),
        async () => waitProposal(target.outputId, 0));
      const proposal = await captureProposal({ fromIndex, outputId: target.outputId, baseState });
      const savedDocument = getDocument(baseState, target.outputId);
      const savedStep = savedDocument.construction?.steps?.at(-1);
      const candidateConstruction = proposal.responseBody.candidateConstruction;
      const candidateDocument = { ...structuredClone(savedDocument), construction: candidateConstruction };
      assert.equal(savedStep?.id, step.id);
      assert.equal(savedStep?.operation?.kind, 'COMBINE');
      assert.equal(savedStep?.operation?.combine?.kind, 'KEY_JOIN');
      assert.equal(savedStep?.operation?.combine?.joinType, 'LEFT');
      assert.equal(candidateConstruction.steps.some(candidate => candidate.operation.kind === 'COMBINE'), false);
      const removalEvidence = rootedEmptyTargetRestorationEvidence(candidateDocument, target.baselineDocument, target);
      assert(removalEvidence.ok, `Removal candidate must restore the exact empty target: ${JSON.stringify(removalEvidence)}`);
      cda.check('persistence', 'Removal proposal binds exact CAS and only removes the saved current-draft KEY_JOIN', true,
        { project, explorer, outputId: target.outputId, savedStepId: step.id,
          savedJoinType: savedStep.operation.combine.joinType, snapshotToken: proposal.requestBody.snapshotToken,
          draftVersion: proposal.requestBody.expectedDraftVersion, draftDigest: proposal.requestBody.expectedDraftDigest,
          candidateSteps: candidateConstruction.steps, removalEvidence });
      const dom = await readProposalPreviewState(page, target.outputId);
      const emptyPreview = cdaNullableEmptyRemovalPreviewEvidence({ proposal, outputId: target.outputId, dom });
      cda.check('correctness', 'Removing nullable Join previews the exact empty target', emptyPreview.ok,
        { outputId: target.outputId, proposalId: proposal.responseBody.proposalId,
          previewEvidence: emptyPreview, candidateEmptyTarget: removalEvidence });
      assert(emptyPreview.ok, `Join removal preview must render its exact empty target: ${JSON.stringify(emptyPreview)}`);
      return { baseState, proposal };
    };

    target = await makeTarget(false);
    const initialTargetDocument = structuredClone(target.baselineDocument);
    const initialPreviewBase = structuredClone(builder);
    const initialPreview = await configureJoin('INNER', expectedInner);
    assert.deepEqual(initialPreview.baseState.workspace.documents, initialPreviewBase.workspace.documents);
    const afterInitialCancel = await cancelProposal(initialPreview.baseState, 'Cancel initial INNER raw nullable Join proposal', undefined, true);
    cda.check('persistence', 'Cancel initial Join restores exact empty target and leaves source selections intact',
      isDeepStrictEqual(getDocument(afterInitialCancel, target.outputId), initialTargetDocument),
      { targetOutputId: target.outputId, before: initialTargetDocument, after: getDocument(afterInitialCancel, target.outputId) });

    target = await makeTarget();
    const preCombineDocument = structuredClone(target.baselineDocument);
    const innerConfig = await configureJoin('INNER', expectedInner);
    const appliedInner = await applyJoin('INNER', expectedInner, innerConfig);
    const savedInner = appliedInner.step;
    const beforeLeftCancel = structuredClone(builder);
    await openSavedEdit(savedInner, expectedInner.length, 'Edit saved INNER Join for LEFT Cancel');
    const leftCancel = await configurePolicy('LEFT', expectedLeft, target);
    const leftCancelStep = leftCancel.proposal.requestBody.candidateConstruction.steps.at(-1);
    assert.equal(leftCancelStep.id, savedInner.id);
    await cancelProposal(leftCancel.baseState, 'Cancel saved INNER to LEFT Join edit', expectedInner);
    builder = await readBuilder();
    assertSavedJoin(builder, 'INNER', savedInner.id);
    assertGrid('Canceled LEFT edit keeps exact INNER rows after reload', await readGrid('saved'), headers, expectedInner);
    cda.check('persistence', 'Cancel LEFT edit preserves original INNER Join after reload',
      isDeepStrictEqual(getDocument(builder, target.outputId), getDocument(beforeLeftCancel, target.outputId)),
      { stepID: savedInner.id, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });

    await openSavedEdit(savedInner, expectedInner.length, 'Edit saved INNER Join for LEFT Apply');
    const leftApply = await configurePolicy('LEFT', expectedLeft, target);
    assert.equal(leftApply.proposal.requestBody.candidateConstruction.steps.at(-1).id, savedInner.id);
    const appliedLeft = await applyJoin('LEFT', expectedLeft, leftApply);
    const savedLeft = appliedLeft.step;
    const removalCancel = await removalProposal(savedLeft, expectedLeft);
    await cancelProposal(removalCancel.baseState, 'Cancel LEFT nullable Join removal', expectedLeft);
    builder = await readBuilder();
    assertSavedJoin(builder, 'LEFT', savedLeft.id);
    assertGrid('Cancel LEFT removal preserves exact five-row output', await readGrid('saved'), headers, expectedLeft);
    const removalApply = await removalProposal(savedLeft, expectedLeft);
    await action('Apply nullable KEY_JOIN removal and restore empty target', page.getByTestId('construction-apply-proposal'),
      locator => locator.click({ timeout: 5_000 }), async () => waitFunction(emptyReady(target.outputId)));
    await reloadTable(target.outputId, undefined, 'Reload removed nullable Join and select empty target');
    builder = await readBuilder();
    checkScope(builder);
    const restored = getDocument(builder, target.outputId);
    const restoredEvidence = rootedEmptyTargetRestorationEvidence(restored, preCombineDocument, target);
    cda.check('persistence', 'Join removal reload restores the exact pre-Combine empty target', restoredEvidence.ok,
      { restoredEvidence, before: preCombineDocument, after: restored });
    assert(restoredEvidence.ok);
    const removalCASAdvanced = builder.draftVersion > removalApply.baseState.draftVersion && builder.draftDigest !== removalApply.baseState.draftDigest;
    cda.check('persistence', 'Applying Join removal advances draft version and digest', removalCASAdvanced,
      { beforeVersion: removalApply.baseState.draftVersion, afterVersion: builder.draftVersion,
        beforeDigest: removalApply.baseState.draftDigest, afterDigest: builder.draftDigest });
    assert(removalCASAdvanced);

    const finalBuilder = await readBuilder();
    checkScope(finalBuilder);
    const finalSources = sourceOutputIDs.map(outputId => snapshotSourceDocument(getDocument(finalBuilder, outputId)));
    const sourcesUnchanged = sameSourceDocuments(finalSources, sourceBaseDocuments);
    cda.check('persistence', 'Both direct raw Observation source tables remain unchanged through Join removal', sourcesUnchanged,
      { sourceOutputIDs, before: sourceBaseDocuments, after: finalSources });
    assert(sourcesUnchanged);
    const allInputs = finalBuilder.workspace.documents.flatMap(document => (document.construction?.steps ?? []).flatMap(step => step.inputs ?? []));
    const noPinnedInputs = allInputs.every(input => input.kind === 'WORKSPACE_OUTPUT' && !input.tableId && !input.revisionId);
    const noPublish = publishRequests === 0;
    cda.check('correctness', 'Nullable Join remains current-draft only and never publishes source tables', noPinnedInputs && noPublish,
      { publishRequests, inputKinds: allInputs.map(input => input.kind), project, generation, explorer });
    assert(noPinnedInputs && noPublish);
    const scopeStable = finalBuilder.catalog?.generation === generation &&
      finalBuilder.catalog?.snapshotToken === initialScope.snapshotToken &&
      finalBuilder.catalog?.authorizationScopeDigest === initialScope.authorizationScopeDigest;
    cda.check('persistence', 'Project, generation, catalog snapshot, and authorization digest stay fixed', scopeStable,
      { project, generation, explorer, snapshotToken: finalBuilder.catalog?.snapshotToken,
        authorizationScopeDigest: finalBuilder.catalog?.authorizationScopeDigest });
    assert(scopeStable);
    const failedActions = (report.actions ?? []).filter(record => record.status !== 'passed' || record.elapsedMs > 5_000);
    cda.check('performance', 'All native nullable Join lifecycle actions complete within five seconds', failedActions.length === 0,
      { actionCount: report.actions?.length ?? 0, failedActions, actionBudgetMs: 5_000 });
    assert.equal(failedActions.length, 0);
    report.status = 'passed';
    report.target.explorer = explorer;
    report.target.nullableCodeJoin = { outputId: target.outputId, sourceOutputIDs, selectionIDs: sources.map(source => source.selection.id) };
  } finally {
    page.off('request', onRequest);
    await capture.flush();
    report.finished = new Date().toISOString();
    await cda.attachReport('nullable-code-join', report);
  }
}
