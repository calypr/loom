import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';

const ACTION_BUDGET_MS = 5_000;
const QUERY_LIMIT = 5_000;
const MAX_SELECTED_ROWS = 12;
const encode = value => encodeURIComponent(value);
const basePath = (project, explorer) => `/api/v1/projects/${encode(project)}/explorers/${encode(explorer)}`;
const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();

function readSpecimenRows(container, project, generation) {
  const query = `FOR r IN Specimen
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Specimen"
      AND IS_STRING(r.id) AND LENGTH(TRIM(r.id)) > 0
      AND IS_STRING(r.payload.subject.reference) AND LENGTH(TRIM(r.payload.subject.reference)) > 0
      AND IS_ARRAY(r.payload.type.coding) AND LENGTH(r.payload.type.coding) > 0
    SORT r._key LIMIT ${QUERY_LIMIT}
    RETURN {_key:r._key,id:r.id,project:r.project,generation:r.dataset_generation,
      resourceType:r.payload.resourceType,subjectReference:r.payload.subject.reference,
      codings:r.payload.type.coding[* RETURN {system:CURRENT.system,
        version:(IS_STRING(CURRENT.version) ? CURRENT.version : null),code:CURRENT.code,display:CURRENT.display}]}`;
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', container, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0, `Bounded Specimen source oracle failed: ${clean(result.stderr || result.stdout).slice(0, 1_200)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `Specimen source oracle returned no JSON array: ${clean(result.stdout).slice(-800)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  assert(rows.length <= QUERY_LIMIT, 'The Specimen source oracle exceeded its returned-row cap.');
  return rows;
}

const familyKey = coding => JSON.stringify([coding.system, coding.version ?? null]);
const tupleKey = coding => JSON.stringify([coding.system, coding.version ?? null, coding.code]);

function normalizedCodingRows(rawRows, project, generation) {
  const byId = new Map();
  for (const row of rawRows) {
    assert.equal(row.project, project);
    assert.equal(row.generation, generation);
    assert.equal(row.resourceType, 'Specimen');
    assert.equal(typeof row._key, 'string');
    assert.equal(typeof row.id, 'string');
    assert.equal(typeof row.subjectReference, 'string');
    assert(row.subjectReference.trim());
    assert(Array.isArray(row.codings));
    const codings = row.codings.filter(coding =>
      typeof coding?.system === 'string' && coding.system.trim() &&
      typeof coding?.code === 'string' && coding.code.trim(),
    ).map(coding => ({
      system: coding.system,
      version: typeof coding.version === 'string' && coding.version.trim().length > 0 ? coding.version : null,
      code: coding.code,
      display: typeof coding.display === 'string' && coding.display.trim() ? coding.display : null,
    }));
    if (!codings.length) continue;
    assert(!byId.has(row.id), `Raw Specimen id ${row.id} is not unique enough for an immutable resource selection.`);
    byId.set(row.id, { _key: row._key, id: row.id, subjectReference: row.subjectReference, codings });
  }
  return [...byId.values()].sort((left, right) => left._key.localeCompare(right._key));
}

function chooseWitness(rawRows, project, generation) {
  const rows = normalizedCodingRows(rawRows, project, generation);
  const byFamily = new Map();
  for (const row of rows) {
    for (const coding of row.codings) {
      const key = familyKey(coding);
      const family = byFamily.get(key) ?? { system: coding.system, version: coding.version, rows: [] };
      family.rows.push({ row, coding });
      byFamily.set(key, family);
    }
  }
  const families = [...byFamily.values()].map(family => {
    const codes = new Set(family.rows.map(({ coding }) => coding.code));
    const resourceIDs = new Set(family.rows.map(({ row }) => row.id));
    return { ...family, distinctCodes: codes.size, resourceCount: resourceIDs.size };
  }).sort((left, right) => right.distinctCodes - left.distinctCodes || right.resourceCount - left.resourceCount ||
    left.system.localeCompare(right.system) || String(left.version ?? '').localeCompare(String(right.version ?? '')));
  const family = families[0];
  assert(family, 'The bounded CDA source read contains no usable Specimen.type Coding family.');
  const selectedById = new Map();
  for (const { row, coding } of family.rows) {
    if (!selectedById.has(row.id)) selectedById.set(row.id, row);
    if (selectedById.size >= MAX_SELECTED_ROWS) break;
  }
  const selectedRows = [...selectedById.values()].sort((left, right) => left._key.localeCompare(right._key));
  assert(selectedRows.length >= 2, 'The bounded Specimen type family must have at least two physical source rows.');

  const grouped = new Map();
  for (const row of selectedRows) {
    const rowTuples = new Set();
    for (const coding of row.codings) {
      if (familyKey(coding) !== familyKey(family.rows[0].coding)) continue;
      const identity = tupleKey(coding);
      const group = grouped.get(identity) ?? {
        system: coding.system, version: coding.version, code: coding.code,
        subjectReferenceCounts: new Map(), displays: new Set(),
      };
      if (coding.display) group.displays.add(coding.display);
      grouped.set(identity, group);
      rowTuples.add(identity);
    }
    for (const identity of rowTuples) {
      const group = grouped.get(identity);
      group.subjectReferenceCounts.set(row.subjectReference,
        (group.subjectReferenceCounts.get(row.subjectReference) ?? 0) + 1);
    }
  }
  assert(grouped.size > 0, 'The selected source rows contain no tuple from the chosen semantic family.');

  const displayFor = group => clean([...group.displays].sort()[0] ?? group.code);
  const withSubject = [];
  const semanticOnly = [];
  const withSubjectRecords = [];
  const semanticOnlyRecords = [];
  for (const group of grouped.values()) {
    const label = displayFor(group);
    let total = 0;
    for (const [subjectReference, count] of group.subjectReferenceCounts) {
      withSubject.push([subjectReference, label, String(count)]);
      withSubjectRecords.push({ subjectReference, label, count: String(count) });
      total += count;
    }
    semanticOnly.push([label, String(total)]);
    semanticOnlyRecords.push({ label, count: String(total) });
  }
  const sortGroupRows = values => values.sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  return {
    rows: selectedRows,
    family: { system: family.system, version: family.version },
    uniqueTupleCount: grouped.size,
    expectedWithSubject: sortGroupRows(withSubject),
    expectedSemanticOnly: sortGroupRows(semanticOnly),
    expectedWithSubjectRecords: withSubjectRecords,
    expectedSemanticOnlyRecords: semanticOnlyRecords,
    expectedRaw: sortGroupRows(selectedRows.map(row => [row.id, row.subjectReference])),
    sourceIDs: selectedRows.map(row => row.id),
    sourceKeyValues: selectedRows.map(row => row._key),
    rawCodingTuples: selectedRows.map(row => ({ id: row.id, subjectReference: row.subjectReference,
      codings: row.codings.filter(coding => familyKey(coding) === familyKey(family.rows[0].coding)) })),
  };
}

function readGrid(page, kind = 'proposal') {
  return page.evaluate(({ kind }) => {
    const proposal = kind === 'proposal'
      ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]')
      : null;
    if (kind === 'proposal' && !proposal) return { ready: false, headers: [], rows: [] };
    const table = kind === 'proposal' ? proposal.querySelector('table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { ready: false, headers: [], rows: [] };
    const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = proposal
      ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
    const rows = proposal
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1)
        .filter(row => row.querySelector('[role="cell"]'))
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
    return { ready: true, headers, rows };
  }, { kind });
}

function expectedGroupGrid(step, oracle, subjectColumnID) {
  const keys = step?.operation?.group?.keys ?? [];
  const aggregates = step?.operation?.group?.aggregates ?? [];
  assert.equal(aggregates.length, 1);
  assert.equal(aggregates[0].operation, 'COUNT_ROWS');
  const outputs = step.outputs ?? [];
  const headers = outputs.map(output => output.label);
  const expectedRecords = keys.some(key => key.inputColumnId === subjectColumnID)
    ? oracle.expectedWithSubjectRecords
    : oracle.expectedSemanticOnlyRecords;
  const rows = expectedRecords.map(record => outputs.map(output => {
    if (output.id === aggregates[0].outputColumnId) return record.count;
    const key = keys.find(candidate => candidate.outputColumnId === output.id);
    if (key?.semanticCategory !== undefined) return record.label;
    if (key?.inputColumnId === subjectColumnID) return record.subjectReference;
    assert.fail(`Group output ${output.id} has no independently mapped source key or COUNT_ROWS aggregate.`);
  }));
  return { headers, rows };
}

function assertGrid(grid, { expectedHeaders, expectedRows, phase }) {
  assert(grid.ready, `${phase} did not render a ready native Builder table.`);
  assert.deepEqual(grid.headers, expectedHeaders, `${phase} headers differ from the independent source oracle.`);
  assert.deepEqual(sortRows(grid.rows), sortRows(expectedRows), `${phase} values or row multiplicity differ from the independent source oracle.`);
}

export async function semanticGroupLifecycle({ page, cda, testInfo }) {
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arango = validatedArangoContainer(cda.target);
  assert.equal(project, cda.target.fixtureProject);
  assert.equal(generation, 'cda-fhir-v1');
  assert.equal(generation, cda.target.fixtureGeneration);
  assert(apiOrigin && uiOrigin);

  const report = cda.report;
  report.phase = 'native semantic Specimen.type Group lifecycle';
  report.scope = { project, generation, sourceResourceType: 'Specimen', codingPath: 'type.coding[]',
    rawRowLimit: QUERY_LIMIT, selectedRowLimit: MAX_SELECTED_ROWS, actionBudgetMs: ACTION_BUDGET_MS };
  const oracle = chooseWitness(readSpecimenRows(arango, project, generation), project, generation);
  report.oracle = { project, generation, ...oracle };
  cda.check('correctness', 'bounded raw Specimen oracle pins exact source rows and system-version-code group identities', true, {
    project, generation, family: oracle.family, sourceIDs: oracle.sourceIDs,
    sourceKeyValues: oracle.sourceKeyValues, uniqueTupleCount: oracle.uniqueTupleCount,
    rawCodingTuples: oracle.rawCodingTuples, expectedWithSubject: oracle.expectedWithSubject,
  });

  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `semantic-group-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${clean(text).slice(0, 1_000)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1_200)}`);
    return value;
  };
  const explorer = `semantic-group-${randomUUID()}`;
  const collectionPath = `/api/v1/projects/${encode(project)}/explorers`;
  const authoringPath = `${collectionPath}/${encode(explorer)}/authoring/v2`;
  await api(collectionPath, { name: explorer, title: 'Semantic Specimen Group verification' });
  let builder = await api(`${authoringPath}/builder`);
  const specimenNode = builder.catalog?.nodes?.find(node => node.resourceType === 'Specimen' && node.rowRootEligible) ??
    builder.catalog?.nodes?.find(node => node.resourceType === 'Specimen');
  assert(specimenNode?.nodeId, 'The exact CDA Builder must expose a Specimen root node.');
  const rootCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === specimenNode.nodeId);
  const idCandidate = rootCandidates.find(candidate => candidate.fieldPath === 'id');
  const subjectCandidate = rootCandidates.find(candidate => candidate.fieldPath === 'subject.reference' && candidate.populated === true);
  assert(idCandidate?.candidateId, 'The Specimen root must expose the physical id candidate.');
  assert(subjectCandidate?.candidateId, 'The selected Specimen rows must expose populated subject.reference as a scalar Group key.');
  const initialSnapshot = builder.catalog.snapshotToken;
  const initialScope = builder.catalog.authorizationScopeDigest;
  const command = async commands => {
    const result = await api(`${authoringPath}/commands`, {
      commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, commands,
    });
    builder = await api(`${authoringPath}/builder`);
    assert.equal(builder.catalog.snapshotToken, initialSnapshot, 'Builder snapshot must remain stable throughout this native Group lifecycle.');
    assert.equal(builder.catalog.authorizationScopeDigest, initialScope, 'Builder authorization scope must remain stable throughout this lifecycle.');
    return result;
  };
  const createResponse = await command([{ type: 'CREATE_TABLE', title: 'Semantic Specimen source rows', rootNodeId: specimenNode.nodeId }]);
  const createdTable = createResponse.results?.find(result => result.type === 'TABLE_CREATED');
  const outputId = createdTable?.outputId;
  assert(outputId, 'Native table creation must return its authoritative outputId.');
  await command([
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' },
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: subjectCandidate.candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Subject reference' },
  ]);
  const selection = await api(`${collectionPath}/${encode(explorer)}/selections`, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: `semantic-group-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: oracle.sourceIDs.map(id => ({ project, generation, resourceType: 'Specimen', id })) } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Specimen');
  assert.equal(selection.memberCount, oracle.sourceIDs.length);
  assert.equal(selection.scopeDigest, initialScope);
  const routes = await api(`${authoringPath}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
  });
  const directRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(directRoute?.routeChoiceId, 'The selected Specimens must resolve to their direct root population route.');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: directRoute.routeChoiceId }]);
  const sourceDocument = builder.workspace.documents.find(document => document.output?.id === outputId);
  assert(sourceDocument, 'The exact Specimen source table must persist in the Builder workspace.');
  assert.equal(sourceDocument.rootResourceType, 'Specimen');
  assert.equal(sourceDocument.population.selectionRevisionId, selection.id);
  assert.equal(sourceDocument.population.route?.length ?? 0, 0);
  const idColumn = sourceDocument.columns.find(column => column.source?.field?.path === 'id');
  const subjectColumn = sourceDocument.columns.find(column => column.source?.field?.path === 'subject.reference');
  const columnID = column => column?.columnId ?? column?.id;
  assert(idColumn && subjectColumn);
  const idColumnID = columnID(idColumn);
  const subjectColumnID = columnID(subjectColumn);
  const scalarLabel = subjectColumn.label;
  assert.equal(typeof scalarLabel, 'string');
  assert(scalarLabel.trim());
  cda.check('correctness', 'native Specimen selection binds the exact FHIR IDs and direct scalar subject field', true, {
    outputId, selectionId: selection.id, sourceIDs: oracle.sourceIDs,
    sourceColumnIDs: [idColumnID, subjectColumnID], scalarLabel,
    project, generation, scopeDigest: selection.scopeDigest,
  });
  const sourceTableDraftVersion = builder.draftVersion;
  const sourceTableDraftDigest = builder.draftDigest;

  const uiURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=builder`;
  const proposalPath = `${authoringPath}/construction-proposals`;
  const proposalOrigins = new Set([apiOrigin, uiOrigin].map(value => new URL(value).origin));
  const proposalStep = request => {
    const body = request.postDataJSON();
    if (body?.outputId !== outputId) return undefined;
    return body.candidateConstruction?.steps?.at(-1);
  };
  const waitProposalRequest = predicate => page.waitForRequest(request => {
    try {
      const url = new URL(request.url());
      return request.method() === 'POST' && proposalOrigins.has(url.origin) &&
        url.pathname === proposalPath && predicate(proposalStep(request));
    } catch { return false; }
  }, { timeout: ACTION_BUDGET_MS });
  const action = (label, locator, perform, after) => cda.action(label, locator, perform, {
    timeout: ACTION_BUDGET_MS, budget: ACTION_BUDGET_MS,
    ...(after ? { after, requiredCheck: 'native semantic Group actions settle within five seconds' } : {}),
  });
  const waitProposalReady = () => page.waitForFunction(() =>
    document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready' &&
    document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready',
  undefined, { timeout: ACTION_BUDGET_MS });
  const proposalReceipt = async () => {
    const preview = page.locator('[data-testid="construction-proposal-preview"]');
    if (await preview.count() === 0) return null;
    return preview.getAttribute('data-preview-receipt-id').catch(() => null);
  };
  const waitNextProposalPreview = previousReceipt => page.waitForFunction(previous => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    const receipt = preview?.getAttribute('data-preview-receipt-id') ?? '';
    return preview?.dataset.previewStatus === 'ready' && receipt !== '' && receipt !== previous;
  }, previousReceipt, { timeout: ACTION_BUDGET_MS });
  const waitGridMatch = (kind, expectedHeaders, expectedRows) => page.waitForFunction(({ kind, expectedHeaders, expectedRows }) => {
    const proposal = kind === 'proposal'
      ? document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]')
      : null;
    if (kind === 'proposal' && !proposal) return false;
    const table = kind === 'proposal' ? proposal.querySelector('table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return false;
    const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = proposal
      ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
    const rows = proposal
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1)
        .filter(row => row.querySelector('[role="cell"]'))
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
    const stable = values => values.map(value => JSON.stringify(value)).sort();
    return JSON.stringify(headers) === JSON.stringify(expectedHeaders) &&
      JSON.stringify(stable(rows)) === JSON.stringify(stable(expectedRows));
  }, { kind, expectedHeaders, expectedRows }, { timeout: ACTION_BUDGET_MS });
  const readBuilder = () => api(`${authoringPath}/builder`);
  const getDocument = state => {
    const matches = state.workspace?.documents?.filter(document => document.output?.id === outputId) ?? [];
    assert.equal(matches.length, 1, `Expected one exact Specimen output ${outputId}.`);
    return matches[0];
  };
  const groupStep = state => getDocument(state).construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  const semanticGroupKey = step => (step?.operation?.group?.keys ?? []).find(key => key.semanticCategory !== undefined);
  const scalarGroupKey = step => (step?.operation?.group?.keys ?? []).find(key => key.inputColumnId === subjectColumnID);
  const assertSavedBinding = (state, expectedKeys) => {
    const step = groupStep(state);
    assert(step?.id, 'The applied semantic Group must persist an ordinary GROUP step.');
    assert.equal(step.operation.group.keys.length, expectedKeys);
    const key = semanticGroupKey(step);
    assert.equal(key?.semanticCategory?.kind, 'BINDING', 'Saved Group must use its authorized durable semantic binding.');
    assert.equal(key.semanticCategory.system, oracle.family.system);
    assert.equal(key.semanticCategory.version ?? null, oracle.family.version);
    assert.equal(key.semanticCategory.resourceType, 'Specimen');
    assert.equal(key.semanticCategory.codingPath, 'type.coding[]');
    assert(typeof key.semanticCategory.bindingId === 'string' && key.semanticCategory.bindingId.trim());
    assert(typeof key.semanticCategory.familyId === 'string' && key.semanticCategory.familyId.trim());
    assert(typeof key.semanticCategory.ruleVersion === 'string' && key.semanticCategory.ruleVersion.trim());
    assert.equal(step.outputs.find(column => column.id === key.outputColumnId)?.label, semanticOutputLabel,
      'The persisted semantic key must retain its readable output label.');
    if (expectedKeys === 2) assert.equal(scalarGroupKey(step)?.inputColumnId, subjectColumnID);
    else assert.equal(scalarGroupKey(step), undefined, 'Semantic-only Group must omit the scalar subject key.');
    assert.deepEqual(step.operation.group.aggregates.map(aggregate => aggregate.operation), ['COUNT_ROWS']);
    return { step, key };
  };
  const assertProposal = (request, { scalar, expectedKind = 'CHOICE', expectedBinding } = {}) => {
    const step = proposalStep(request);
    assert.equal(step?.operation?.kind, 'GROUP', 'Semantic category selection must stay inside ordinary GROUP.');
    const keys = step.operation.group.keys ?? [];
    const semanticKey = keys.find(key => key.semanticCategory !== undefined);
    assert.equal(semanticKey?.semanticCategory?.kind, expectedKind);
    if (expectedKind === 'CHOICE') {
      assert.equal(semanticKey.semanticCategory.choiceId, choiceId,
        'The ordinary Group candidate must bind the exact visibly selected family choice.');
    } else {
      assert.equal(expectedKind, 'BINDING');
      const versionlessIdentity = binding => ({ ...binding, version: binding.version ?? '' });
      assert.deepEqual(versionlessIdentity(semanticKey.semanticCategory), versionlessIdentity(expectedBinding),
        'Saved Group edit candidates must retain the exact durable semantic family binding.');
    }
    assert.equal(semanticKey.inputColumnId, undefined,
      'A typed semantic key must not be encoded as a scalar inputColumnId.');
    assert.equal(semanticKey.outputColumnId === undefined, false);
    assert.equal(keys.length, scalar ? 2 : 1);
    if (scalar) assert(keys.some(key => key.inputColumnId === subjectColumnID));
    else assert(!keys.some(key => key.inputColumnId === subjectColumnID));
    assert.deepEqual(step.operation.group.aggregates.map(aggregate => aggregate.operation), ['COUNT_ROWS']);
    return { step, semanticKey };
  };
  const assertRendered = async ({ step, phase }) => {
    const expected = expectedGroupGrid(step, oracle, subjectColumnID);
    const expectedRows = expected.rows;
    const expectedHeaders = expected.headers;
    await waitGridMatch('proposal', expectedHeaders, expectedRows);
    const grid = await readGrid(page, 'proposal');
    assertGrid(grid, { expectedHeaders, expectedRows, phase });
    return grid;
  };
  const waitSavedExpected = async ({ step, phase }) => {
    const expected = expectedGroupGrid(step, oracle, subjectColumnID);
    const expectedRows = expected.rows;
    const expectedHeaders = expected.headers;
    await waitGridMatch('saved', expectedHeaders, expectedRows);
    return readGrid(page, 'saved');
  };
  const openTable = async () => {
    await cda.navigate(uiURL);
    const table = page.getByTestId(`construction-table-${outputId}`);
    await table.waitFor({ state: 'visible', timeout: 10_000 });
    await action('select exact raw Specimen source table', table, () => table.click(), async () => {
      await waitGridMatch('saved', [idColumn.label, scalarLabel], oracle.expectedRaw);
    });
    if (!rawSourceVisibleCheck) {
      const grid = await readGrid(page, 'saved');
      assert.deepEqual(grid.headers, [idColumn.label, scalarLabel]);
      assert.deepEqual(sortRows(grid.rows), sortRows(oracle.expectedRaw));
      cda.check('correctness', 'native Specimen table renders exact selected IDs and subject references', true, {
        headers: grid.headers, rows: grid.rows, expectedRows: oracle.expectedRaw,
      });
      rawSourceVisibleCheck = true;
    }
  };
  const openGroup = async () => {
    await openTable();
    const settings = page.getByTestId('construction-rows-settings-trigger');
    await settings.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    await action('open native row operation choices', settings, () => settings.click(),
      () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
    const group = page.getByTestId('construction-action-group-rows');
    const initialRequest = waitProposalRequest(step => step?.operation?.kind === 'GROUP');
    await action('open standard Group editor', group, () => group.click(), async () => {
      await page.getByTestId('construction-reshape-group').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      await initialRequest;
      await waitProposalReady();
    });
    const groupBox = page.getByTestId('construction-reshape-group');
    const semanticChoices = groupBox.locator('input[type="checkbox"][data-semantic-choice-id]');
    await semanticChoices.first().waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    assert((await semanticChoices.count()) > 0,
      'Semantic family checkboxes must live inside the existing Group-by box.');
    return groupBox;
  };
  const semanticScope = binding => `${binding.sourceKind} · ${binding.resourceType}.${binding.codingPath} · ${binding.system} · ${binding.version ? `version ${binding.version}` : 'versionless'}`;
  const selectFamily = async groupBox => {
    const previousReceipt = await proposalReceipt();
    const candidates = await groupBox.locator('input[type="checkbox"][data-semantic-choice-id]').evaluateAll(inputs => inputs.map((input, index) => {
      const option = input.closest('[data-testid="construction-group-semantic-option"]');
      return {
        index,
        choiceId: input.getAttribute('data-semantic-choice-id'),
        accessibleName: input.getAttribute('aria-label') ?? '',
        scopeLabel: option?.querySelector('[data-testid^="construction-group-field-details-"] p')?.textContent?.replace(/\s+/g, ' ').trim() ?? '',
        disabled: input.disabled,
      };
    }));
    const versionLabel = oracle.family.version ? `version ${oracle.family.version}` : 'versionless';
    const matches = candidates.filter(candidate => !candidate.disabled &&
      candidate.accessibleName === 'Group by Specimen type' &&
      candidate.scopeLabel.includes('Specimen.type.coding[]') &&
      candidate.scopeLabel.includes(oracle.family.system) &&
      candidate.scopeLabel.endsWith(versionLabel));
    assert.equal(matches.length, 1,
      `The Group editor must offer exactly the readable Specimen type family ${oracle.family.system} / ${versionLabel} in Details; choices were ${JSON.stringify(candidates)}`);
    const match = matches[0];
    assert(typeof match.choiceId === 'string' && match.choiceId.trim(),
      'A new semantic family checkbox must expose its exact ID in data-semantic-choice-id.');
    choiceId = match.choiceId;
    semanticOutputLabel = match.accessibleName.slice('Group by '.length);
    assert.equal(semanticOutputLabel, 'Specimen type',
      'The semantic option label must be the readable Specimen type name without its system or version.');
    assert(match.scopeLabel.includes(oracle.family.system));
    assert(match.scopeLabel.endsWith(versionLabel), 'The semantic option Details must show the exact family version.');
    const semanticCheckbox = groupBox.locator('input[type="checkbox"][data-semantic-choice-id]').nth(match.index);
    assert.equal(await semanticCheckbox.getAttribute('data-semantic-choice-id'), choiceId,
      'The checked semantic family must be the exact choice identified by data-semantic-choice-id.');
    await action('select visible Specimen type semantic family', semanticCheckbox,
      () => semanticCheckbox.check({ timeout: ACTION_BUDGET_MS }), async () => {
        await waitNextProposalPreview(previousReceipt);
      });
    const selectedCheckbox = page.getByTestId(`construction-group-semantic-choice-${choiceId}`);
    assert.equal(await selectedCheckbox.getAttribute('data-semantic-choice-id'), choiceId);
    assert.equal(await selectedCheckbox.isChecked(), true, 'The exact Specimen type semantic checkbox must be checked.');
    return { choiceId, label: semanticOutputLabel, scopeLabel: match.scopeLabel };
  };

  let choiceId;
  let semanticOutputLabel;
  let rawSourceVisibleCheck = false;
  const initialGroupBox = await openGroup();
  const sourceKey = page.getByRole('checkbox', { name: `Group by ${scalarLabel}`, exact: true });
  await sourceKey.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const scalarRequest = waitProposalRequest(step => step?.operation?.kind === 'GROUP' &&
    step.operation.group.keys?.some(key => key.inputColumnId === subjectColumnID));
  const scalarReceipt = await proposalReceipt();
  await action('select scalar Specimen subject Group key', sourceKey, () => sourceKey.check(), async () => {
    await scalarRequest;
    await waitNextProposalPreview(scalarReceipt);
  });
  const typedRequest = waitProposalRequest(step => step?.operation?.kind === 'GROUP' &&
    step.operation.group.keys?.some(key => key.semanticCategory?.kind === 'CHOICE'));
  const selectedFamily = await selectFamily(initialGroupBox);
  const grainSummary = clean(await page.getByTestId('construction-group-grain-summary').textContent());
  assert.match(grainSummary, /One row per unique combination of/i);
  assert(grainSummary.includes(scalarLabel), 'The Group screen must show the selected scalar grain.');
  assert(grainSummary.includes(semanticOutputLabel), 'The Group screen must show the selected Specimen type grain.');
  assert.equal(typeof testInfo?.attach, 'function', 'The native semantic Group test must provide Playwright testInfo for its screenshot.');
  const selectedScopeDetails = page.getByTestId(`construction-group-field-details-${selectedFamily.choiceId}`);
  assert.equal(await selectedScopeDetails.getAttribute('open'), null,
    'The primary picker screenshot must show the selected field with its technical Details collapsed.');
  await testInfo.attach('semantic-group-initial-picker.png', {
    body: await page.screenshot({ fullPage: true }),
    contentType: 'image/png',
  });
  const selectedScopeText = selectedScopeDetails.locator('p');
  await action('show selected Specimen type family scope Details', selectedScopeDetails.locator('summary'),
    () => selectedScopeDetails.locator('summary').click(),
    () => selectedScopeText.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
  assert.equal(clean(await selectedScopeText.textContent()), selectedFamily.scopeLabel,
    'The selected semantic family Details must display its exact source scope.');
  const typedCandidateRequest = await typedRequest;
  const proposed = assertProposal(typedCandidateRequest, { scalar: true });
  const keyOutput = proposed.step.outputs?.find(output => output.id === proposed.semanticKey.outputColumnId);
  assert(keyOutput?.label && /specimen\s*type/i.test(keyOutput.label),
    'The proposed semantic Group output must use the readable family label.');
  semanticOutputLabel = keyOutput.label;
  const firstPreview = await assertRendered({ step: proposed.step, phase: 'semantic plus scalar Group Preview' });
  cda.check('usability', 'visible Specimen type family selection sits in the existing Group-by box', true, {
    control: 'Semantic family checkbox', choiceId, optionLabel: selectedFamily.label, scopeDetails: selectedFamily.scopeLabel,
    family: oracle.family, scalarGroupKey: scalarLabel,
  });
  cda.check('correctness', 'native semantic Group proposal is ordinary GROUP with a CHOICE and scalar key', true, {
    outputId, stepId: proposed.step.id, keys: proposed.step.operation.group.keys,
    aggregates: proposed.step.operation.group.aggregates, outputLabel: semanticOutputLabel,
  });
  cda.check('correctness', 'semantic plus scalar Group Preview matches independent raw Specimen tuples', true, {
    headers: firstPreview.headers, rows: firstPreview.rows, expectedRows: oracle.expectedWithSubject,
    family: oracle.family, rawCodingTuples: oracle.rawCodingTuples,
  });

  const beforeCancel = await readBuilder();
  await action('Cancel first semantic Group Preview', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(),
    () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
      { timeout: ACTION_BUDGET_MS }));
  const afterCancel = await readBuilder();
  assert.deepEqual(afterCancel.workspace, beforeCancel.workspace);
  assert.equal(afterCancel.draftVersion, beforeCancel.draftVersion);
  assert.equal(afterCancel.draftDigest, beforeCancel.draftDigest);
  assert.equal(afterCancel.draftVersion, sourceTableDraftVersion);
  assert.equal(afterCancel.draftDigest, sourceTableDraftDigest);
  cda.check('persistence', 'Cancel preserves the exact source table and draft without applying Group', true, {
    draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest,
    sourceTableDraftVersion, sourceTableDraftDigest,
  });

  await openGroup();
  const applyScalarKey = page.getByRole('checkbox', { name: `Group by ${scalarLabel}`, exact: true });
  const applyScalarRequest = waitProposalRequest(step => step?.operation?.kind === 'GROUP' &&
    step.operation.group.keys?.some(key => key.inputColumnId === subjectColumnID));
  const applyScalarReceipt = await proposalReceipt();
  await action('select scalar key for semantic Group Apply', applyScalarKey, () => applyScalarKey.check(), async () => {
    await applyScalarRequest;
    await waitNextProposalPreview(applyScalarReceipt);
  });
  const applyTypedRequest = waitProposalRequest(step => step?.operation?.kind === 'GROUP' &&
    step.operation.group.keys?.some(key => key.semanticCategory?.kind === 'CHOICE'));
  await selectFamily(page.getByTestId('construction-reshape-group'));
  const applyCandidate = await applyTypedRequest;
  const applyProposal = assertProposal(applyCandidate, { scalar: true });
  semanticOutputLabel = applyProposal.step.outputs.find(output => output.id === applyProposal.semanticKey.outputColumnId)?.label;
  await assertRendered({ step: applyProposal.step, phase: 'semantic Group Apply preview' });
  await action('Apply semantic plus scalar Group', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(),
    async () => {
      await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
        { timeout: ACTION_BUDGET_MS });
      await waitSavedExpected({ step: applyProposal.step, phase: 'applied semantic plus scalar Group' });
    });
  let saved = await readBuilder();
  const firstSaved = assertSavedBinding(saved, 2);
  const appliedGrid = await waitSavedExpected({ step: firstSaved.step, phase: 'applied semantic plus scalar Group' });
  cda.check('correctness', 'Apply persists semantic BINDING and scalar key with exact raw Group rows', true, {
    stepId: firstSaved.step.id, binding: firstSaved.key.semanticCategory,
    keys: firstSaved.step.operation.group.keys, headers: appliedGrid.headers, rows: appliedGrid.rows,
  });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: 10_000 });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click({ timeout: ACTION_BUDGET_MS });
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: 10_000 });
  let reloaded = await readBuilder();
  let reloadedBinding = assertSavedBinding(reloaded, 2);
  assert.deepEqual(reloadedBinding.key, firstSaved.key, 'Builder reload must preserve the exact bound semantic family identity.');
  const reloadedGrid = await waitSavedExpected({ step: reloadedBinding.step, phase: 'reloaded semantic plus scalar Group' });
  cda.check('persistence', 'reloaded semantic Group retains exact BINDING identity and rows', true, {
    stepId: reloadedBinding.step.id, binding: reloadedBinding.key.semanticCategory,
    headers: reloadedGrid.headers, rows: reloadedGrid.rows,
  });

  const openSavedGroup = async () => {
    const history = page.getByTestId('construction-row-operation-history');
    if (!await history.isVisible().catch(() => false)) {
      await action('open saved Group history', page.getByTestId('construction-rows-settings-trigger'),
        () => page.getByTestId('construction-rows-settings-trigger').click(),
        () => history.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
    }
    const edit = page.getByTestId(`construction-row-edit-${reloadedBinding.step.id}`);
    const savedBindingValue = `binding:${reloadedBinding.key.semanticCategory.bindingId}:${reloadedBinding.key.semanticCategory.ruleVersion}`;
    const expectedScope = semanticScope(reloadedBinding.key.semanticCategory);
    const savedSemanticCheckbox = page.getByRole('checkbox', { name: `Group by ${semanticOutputLabel}`, exact: true });
    await action('open saved semantic Group editor', edit, () => edit.click(), async () => {
      await savedSemanticCheckbox.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      await page.waitForFunction(({ bindingValue, scalarLabel, semanticLabel, expectedScope }) => {
        const semanticCheckbox = [...document.querySelectorAll('input[type="checkbox"]')]
          .find(input => input.value === bindingValue && input.checked);
        const scalarKey = [...document.querySelectorAll('input[type="checkbox"]')]
          .find(input => input.getAttribute('aria-label') === `Group by ${scalarLabel}`);
        const scope = semanticCheckbox?.closest('[data-testid="construction-group-semantic-option"]')
          ?.querySelector('[data-testid="construction-group-saved-field-details"] p')?.textContent
          ?.replace(/\s+/g, ' ').trim();
        return semanticCheckbox?.getAttribute('aria-label') === `Group by ${semanticLabel}` &&
          scope === expectedScope && scalarKey?.checked === true;
      }, { bindingValue: savedBindingValue, scalarLabel, semanticLabel: semanticOutputLabel, expectedScope }, { timeout: ACTION_BUDGET_MS });
    });
    assert.equal(await savedSemanticCheckbox.getAttribute('value'), savedBindingValue,
      'The saved semantic key must rehydrate as a synthetic checkbox carrying its durable binding and rule version.');
    assert.equal(await savedSemanticCheckbox.isChecked(), true,
      'The saved semantic binding checkbox must be checked in the Group editor.');
    const savedScope = clean(await page.getByTestId('construction-group-saved-field-details').locator('p').textContent());
    assert.equal(savedScope, expectedScope, 'The saved BINDING Details must retain the exact persisted scope.');
    return { checkbox: savedSemanticCheckbox, bindingValue: savedBindingValue, scope: savedScope };
  };
  const savedBeforeEdit = await readBuilder();
  const savedSemanticBinding = await openSavedGroup();
  const editSelectedLabel = clean(await savedSemanticBinding.checkbox.getAttribute('aria-label'));
  assert.equal(editSelectedLabel, `Group by ${semanticOutputLabel}`);
  assert.equal(savedSemanticBinding.bindingValue,
    `binding:${firstSaved.key.semanticCategory.bindingId}:${firstSaved.key.semanticCategory.ruleVersion}`);
  assert.equal(savedSemanticBinding.scope, semanticScope(firstSaved.key.semanticCategory));
  const editScalarKey = page.getByRole('checkbox', { name: `Group by ${scalarLabel}`, exact: true });
  assert.equal(await editScalarKey.isChecked(), true, 'Saved Group edit must rehydrate its scalar key.');
  const semanticOnlyRequest = waitProposalRequest(step => step?.operation?.kind === 'GROUP' &&
    step.operation.group.keys?.length === 1 && step.operation.group.keys[0]?.semanticCategory?.kind === 'BINDING');
  const editReceipt = await proposalReceipt();
  await action('edit Group grain to semantic family only', editScalarKey, () => editScalarKey.uncheck(), async () => {
    await semanticOnlyRequest;
    await waitNextProposalPreview(editReceipt);
  });
  const semanticOnlyCandidate = await semanticOnlyRequest;
  const editedProposal = assertProposal(semanticOnlyCandidate, {
    scalar: false, expectedKind: 'BINDING', expectedBinding: firstSaved.key.semanticCategory,
  });
  semanticOutputLabel = editedProposal.step.outputs.find(output => output.id === editedProposal.semanticKey.outputColumnId)?.label;
  const editedPreview = await assertRendered({ step: editedProposal.step, phase: 'semantic-only Group edit Preview' });
  cda.check('usability', 'saved Group edit rehydrates the exact readable semantic family and current scalar key', true, {
    selectedOption: editSelectedLabel, scalarKeyChecked: true,
    binding: firstSaved.key.semanticCategory,
  });
  cda.check('correctness', 'semantic-only Group edit Preview matches independent source tuple counts', true, {
    headers: editedPreview.headers, rows: editedPreview.rows, expectedRows: oracle.expectedSemanticOnly,
  });

  await action('Cancel semantic-only Group edit', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(),
    () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
      { timeout: ACTION_BUDGET_MS }));
  const afterEditCancel = await readBuilder();
  assert.deepEqual(afterEditCancel.workspace, savedBeforeEdit.workspace);
  assert.equal(afterEditCancel.draftVersion, savedBeforeEdit.draftVersion);
  assert.equal(afterEditCancel.draftDigest, savedBeforeEdit.draftDigest);
  assert.deepEqual(assertSavedBinding(afterEditCancel, 2).key, firstSaved.key);
  cda.check('persistence', 'Cancel preserves saved semantic Group keys, rows, and draft identity', true, {
    draftVersion: afterEditCancel.draftVersion, draftDigest: afterEditCancel.draftDigest,
    keys: groupStep(afterEditCancel).operation.group.keys,
  });

  const rehydratedSemanticBinding = await openSavedGroup();
  assert.equal(rehydratedSemanticBinding.bindingValue,
    `binding:${firstSaved.key.semanticCategory.bindingId}:${firstSaved.key.semanticCategory.ruleVersion}`);
  assert.equal(rehydratedSemanticBinding.scope, semanticScope(firstSaved.key.semanticCategory));
  const semanticOnlyApplyRequest = waitProposalRequest(step => step?.operation?.kind === 'GROUP' &&
    step.operation.group.keys?.length === 1 && step.operation.group.keys[0]?.semanticCategory?.kind === 'BINDING');
  const semanticOnlyReceipt = await proposalReceipt();
  await action('remove scalar key for semantic-only Group edit', page.getByRole('checkbox', { name: `Group by ${scalarLabel}`, exact: true }),
    () => page.getByRole('checkbox', { name: `Group by ${scalarLabel}`, exact: true }).uncheck(), async () => {
      await semanticOnlyApplyRequest;
      await waitNextProposalPreview(semanticOnlyReceipt);
    });
  const semanticOnlyApplyCandidate = await semanticOnlyApplyRequest;
  const semanticOnlyApplyProposal = assertProposal(semanticOnlyApplyCandidate, {
    scalar: false, expectedKind: 'BINDING', expectedBinding: firstSaved.key.semanticCategory,
  });
  await assertRendered({ step: semanticOnlyApplyProposal.step, phase: 'applied semantic-only Group Preview' });
  await action('Apply semantic-only Group edit', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(),
    async () => {
      await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
        { timeout: ACTION_BUDGET_MS });
      await waitSavedExpected({ step: semanticOnlyApplyProposal.step, phase: 'applied semantic-only Group' });
    });
  saved = await readBuilder();
  const semanticOnlySaved = assertSavedBinding(saved, 1);
  assert.deepEqual(semanticOnlySaved.key, firstSaved.key,
    'Editing the grouping grain must retain the exact bound semantic family identity.');
  const semanticOnlyGrid = await waitSavedExpected({ step: semanticOnlySaved.step, phase: 'applied semantic-only Group' });
  cda.check('correctness', 'saved semantic-only edit persists the same BINDING and exact grouped labels/counts', true, {
    binding: semanticOnlySaved.key.semanticCategory, headers: semanticOnlyGrid.headers, rows: semanticOnlyGrid.rows,
  });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: 10_000 });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click({ timeout: ACTION_BUDGET_MS });
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: 10_000 });
  reloaded = await readBuilder();
  reloadedBinding = assertSavedBinding(reloaded, 1);
  assert.deepEqual(reloadedBinding.key, firstSaved.key);
  const semanticOnlyReload = await waitSavedExpected({ step: reloadedBinding.step, phase: 'reloaded semantic-only Group' });
  cda.check('persistence', 'edited semantic-only Group and exact rows survive Builder reload', true, {
    binding: reloadedBinding.key.semanticCategory, headers: semanticOnlyReload.headers, rows: semanticOnlyReload.rows,
  });

  const removalSelector = `[data-testid="construction-row-remove-${reloadedBinding.step.id}"]`;
  const history = page.getByTestId('construction-row-operation-history');
  if (!await history.isVisible().catch(() => false)) {
    await action('open semantic-only Group removal history', page.getByTestId('construction-rows-settings-trigger'),
      () => page.getByTestId('construction-rows-settings-trigger').click(),
      () => history.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
  }
  await action('preview semantic Group removal', page.locator(removalSelector),
    () => page.locator(removalSelector).click(), async () => {
      await page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready' &&
        document.querySelector('[data-testid="construction-removal-summary"]') !== null,
      undefined, { timeout: ACTION_BUDGET_MS });
    });
  await waitGridMatch('proposal', [idColumn.label, scalarLabel], oracle.expectedRaw);
  const confirmedRestoredPreview = await readGrid(page, 'proposal');
  assert(confirmedRestoredPreview.ready);
  assert.deepEqual(confirmedRestoredPreview.headers, [idColumn.label, scalarLabel]);
  assert.deepEqual(sortRows(confirmedRestoredPreview.rows), sortRows(oracle.expectedRaw),
    'Removing Group must preview exact original Specimen IDs and subject references.');
  cda.check('correctness', 'Group removal Preview restores exact raw Specimen IDs and subject references', true, {
    headers: confirmedRestoredPreview.headers, rows: confirmedRestoredPreview.rows, expectedRows: oracle.expectedRaw,
  });
  const savedBeforeRemovalCancel = await readBuilder();
  await action('Cancel semantic Group removal', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(),
    () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
      { timeout: ACTION_BUDGET_MS }));
  const afterRemovalCancel = await readBuilder();
  assert.deepEqual(afterRemovalCancel.workspace, savedBeforeRemovalCancel.workspace);
  assert.equal(afterRemovalCancel.draftVersion, savedBeforeRemovalCancel.draftVersion);
  assert.equal(afterRemovalCancel.draftDigest, savedBeforeRemovalCancel.draftDigest);
  assert.deepEqual(assertSavedBinding(afterRemovalCancel, 1).key, firstSaved.key);
  cda.check('persistence', 'Cancel preserves semantic-only Group and its exact saved rows', true, {
    draftVersion: afterRemovalCancel.draftVersion, draftDigest: afterRemovalCancel.draftDigest,
  });

  if (!await history.isVisible().catch(() => false)) {
    await page.getByTestId('construction-rows-settings-trigger').click({ timeout: ACTION_BUDGET_MS });
  }
  await page.locator(removalSelector).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await action('reopen semantic Group removal', page.locator(removalSelector),
    () => page.locator(removalSelector).click(),
    () => page.waitForFunction(() => document.querySelector('[data-testid="construction-removal-summary"]') !== null,
      undefined, { timeout: ACTION_BUDGET_MS }));
  await action('Apply semantic Group removal', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(),
    async () => {
      await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
        { timeout: ACTION_BUDGET_MS });
      await waitGridMatch('saved', [idColumn.label, scalarLabel], oracle.expectedRaw);
    });
  const removedBuilder = await readBuilder();
  assert.deepEqual(getDocument(removedBuilder).construction?.steps ?? [], [], 'Removing Group must restore the ungrouped source document.');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: 10_000 });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click({ timeout: ACTION_BUDGET_MS });
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: 10_000 });
  await waitGridMatch('saved', [idColumn.label, scalarLabel], oracle.expectedRaw);
  const finalRawGrid = await readGrid(page, 'saved');
  assert.deepEqual(finalRawGrid.headers, [idColumn.label, scalarLabel]);
  assert.deepEqual(sortRows(finalRawGrid.rows), sortRows(oracle.expectedRaw));
  cda.check('persistence', 'removing semantic Group restores exact raw Specimen rows after reload', true, {
    draftVersion: removedBuilder.draftVersion, draftDigest: removedBuilder.draftDigest,
    headers: finalRawGrid.headers, rows: finalRawGrid.rows, expectedRows: oracle.expectedRaw,
  });
  cda.check('performance', 'all native semantic Group lifecycle actions stay within five seconds', true, {
    actionBudgetMs: ACTION_BUDGET_MS,
  });
}
