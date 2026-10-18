import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import {
  buildSemanticGroupExpandedOracle,
  VALUE_CODEABLE_CONCEPT_CODING_PATH,
  SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT,
} from '../helpers/semantic-group-expanded-oracle.mjs';
import {
  assertExactRows,
  protectNativeRequestBodies,
  summarizeOracle,
} from '../helpers/semantic-group-expanded-report.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';

const ACTION_BUDGET_MS = 5_000;
const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const renderCell = value => Array.isArray(value)
  ? value.map(item => clean(item)).join('; ')
  : typeof value === 'string' ? clean(value) : value;
const encode = value => encodeURIComponent(value);

function readRawCodingRows(container, database, project, generation) {
  const query = `FOR r IN Observation
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation"
      AND IS_STRING(r.id) AND LENGTH(TRIM(r.id)) > 0
      AND IS_STRING(r.payload.subject.reference) AND LENGTH(TRIM(r.payload.subject.reference)) > 0
      AND IS_ARRAY(r.payload.valueCodeableConcept.coding) AND LENGTH(r.payload.valueCodeableConcept.coding) > 0
    LET codings = r.payload.valueCodeableConcept.coding
    FILTER LENGTH(FOR c IN codings FILTER !IS_STRING(c.code) OR LENGTH(TRIM(c.code)) == 0 RETURN 1) == 0
    LIMIT ${SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT}
    RETURN {_key:r._key,id:r.id,project:r.project,generation:r.dataset_generation,
      resourceType:r.payload.resourceType,subjectReference:r.payload.subject.reference,
      codings:(FOR coding, ordinal IN codings RETURN {ordinal,
        system:(IS_STRING(coding.system) ? coding.system : null),
        version:(IS_STRING(coding.version) ? coding.version : null),
        code:coding.code,display:(IS_STRING(coding.display) ? coding.display : null)})}`;
  const marker = '__SEMANTIC_GROUP_EXPANDED_RAW__';
  const javascript = `print(${JSON.stringify(marker)} + JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`
    .replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', container, 'arangosh', '--server.database', database,
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024 });
  assert.equal(result.status, 0,
    `Bounded Observation Coding oracle failed (exit=${result.status}, outputSha256=${stableDigest(clean(result.stderr || result.stdout))}).`);
  const markerOffset = result.stdout.indexOf(marker);
  assert(markerOffset >= 0, 'The bounded Observation Coding oracle returned no marked JSON result.');
  const rawRows = JSON.parse(result.stdout.slice(markerOffset + marker.length).trim());
  assert(Array.isArray(rawRows));
  assert(rawRows.length <= SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT,
    'The bounded Observation Coding oracle returned more than its declared row cap.');
  return rawRows;
}

function documentFor(state, outputId) {
  const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, `Expected one exact Observation table for output ${outputId}.`);
  return matches[0];
}

function groupStepFor(state, outputId) {
  return documentFor(state, outputId).construction?.steps?.find(step => step.operation?.kind === 'GROUP');
}

function codingGroupKey(step, { sourceKind, choiceId } = {}) {
  return (step?.operation?.group?.keys ?? []).find(key => sourceKind === 'CHOICE'
    ? key.fieldSource?.kind === 'CHOICE' && key.fieldSource.choiceId === choiceId
    : key.fieldSource?.kind === 'BINDING' && key.fieldSource.valueKind === 'CODING_IDENTITY');
}

function scalarGroupKey(step, { sourceKind, choiceId, canonicalPath = 'subject.reference' } = {}) {
  return (step?.operation?.group?.keys ?? []).find(key => sourceKind === 'CHOICE'
    ? key.fieldSource?.kind === 'CHOICE' && key.fieldSource.choiceId === choiceId
    : key.fieldSource?.kind === 'BINDING' && key.fieldSource.valueKind === 'SCALAR' &&
      key.fieldSource.canonicalPath === canonicalPath);
}

function assertCodingGroupKey(step, rowChoice, {
  scalar,
  sourceKind,
  codingChoiceId,
  scalarChoiceId,
  subjectCandidate,
} = {}) {
  assert.equal(step?.operation?.kind, 'GROUP', 'Expanded Coding identity must use ordinary GROUP.');
  const keys = step.operation.group?.keys ?? [];
  assert.equal(keys.some(key => key.semanticCategory !== undefined), false,
    'The expanded current Coding item is a field source, not a root-wide semantic-inventory category.');
  const codingKey = codingGroupKey(step, { sourceKind, choiceId: codingChoiceId });
  assert(codingKey, `GROUP ${sourceKind} must bind the exact current Coding source choice.`);
  const source = codingKey.fieldSource;
  if (sourceKind === 'CHOICE') {
    assert.equal(source.kind, 'CHOICE');
    assert.equal(source.choiceId, codingChoiceId);
  } else {
    assert.equal(source.kind, 'BINDING');
    assert.equal(source.valueKind, 'CODING_IDENTITY');
    assert.equal(source.sourceStageId, 'source_projection');
    assert.equal(typeof source.sourceShapeId, 'string');
    assert(source.sourceShapeId.trim());
    assert.equal(source.sourceRole, 'CURRENT_ITEM');
    assert.equal(source.occurrenceId, 'base');
    assert.equal(source.resourceType, 'Coding');
    assert.equal(source.ownerOccurrenceId, 'base');
    assert.equal(source.ownerResourceType, 'Observation');
    assert.equal(source.canonicalPath, VALUE_CODEABLE_CONCEPT_CODING_PATH);
    assert.equal(source.boundarySelector, undefined);
    assert.equal(source.candidateId, undefined,
      'The current Coding item BINDING must derive from the selected row occurrence, not a root field candidate.');
    assert(!JSON.stringify(source).includes('Observation.code.coding[]'),
      'GROUP must not substitute the unrelated root Observation.code.coding[] path.');
  }
  assert(codingKey.outputColumnId, 'The current Coding identity must have a stable Group output.');
  const scalarKey = scalarGroupKey(step, { sourceKind, choiceId: scalarChoiceId,
    canonicalPath: subjectCandidate?.fieldPath ?? 'subject.reference' });
  assert.equal(keys.length, scalar ? 2 : 1,
    `Expected ${scalar ? 'the exact scalar plus Coding identity' : 'Coding identity alone'} in the Group grain.`);
  if (scalar) {
    assert(scalarKey, `GROUP ${sourceKind} must include the exact scalar field-source choice.`);
    const scalarSource = scalarKey.fieldSource;
    if (sourceKind === 'CHOICE') {
      assert.equal(scalarSource.kind, 'CHOICE');
      assert.equal(scalarSource.choiceId, scalarChoiceId);
    } else {
      assert.equal(scalarSource.kind, 'BINDING');
      assert.equal(scalarSource.valueKind, 'SCALAR');
      assert.equal(scalarSource.sourceStageId, 'source_projection');
      assert.equal(typeof scalarSource.sourceShapeId, 'string');
      assert(scalarSource.sourceShapeId.trim());
      assert(['OWNER', 'ROOT'].includes(scalarSource.sourceRole),
        'The direct Observation.subject.reference key must resolve against the Observation owner/root.');
      assert.equal(scalarSource.occurrenceId, 'base');
      assert.equal(scalarSource.resourceType, 'Observation');
      assert.equal(scalarSource.ownerOccurrenceId, 'base');
      assert.equal(scalarSource.ownerResourceType, 'Observation');
      assert.equal(typeof scalarSource.candidateId, 'string');
      assert(scalarSource.candidateId.trim());
      assert.equal(scalarSource.canonicalPath, subjectCandidate?.fieldPath ?? 'subject.reference');
      assert.equal(scalarSource.boundarySelector, undefined);
    }
    assert(scalarKey.outputColumnId, 'The scalar Group output must have a stable binding.');
  }
  else assert.equal(scalarKey, undefined);
  const aggregate = (step.operation.group.aggregates ?? []).find(item => item.operation === 'COUNT_ROWS');
  assert(aggregate, 'Expanded Coding Group must count source occurrences with COUNT_ROWS.');
  assert.equal((step.operation.group.aggregates ?? []).filter(item => item.operation === 'COUNT_ROWS').length, 1);
  const keyOutput = step.outputs?.find(output => output.id === codingKey.outputColumnId);
  assert(keyOutput?.name && keyOutput?.label, 'Coding Group output must expose its display label.');
  const aggregateOutput = step.outputs?.find(output => output.id === aggregate.outputColumnId);
  assert(aggregateOutput?.name && aggregateOutput?.label, 'COUNT_ROWS must expose its output label.');
  if (rowChoice?.choiceId) assert(rowChoice.choiceId.trim());
  return { codingKey, scalarKey, aggregate, keyOutput, aggregateOutput };
}

function expectedGroupGrid(step, records, context = {}) {
  const { scalar, sourceKind, codingChoiceId, scalarChoiceId, subjectCandidate } = context;
  const { codingKey, scalarKey, aggregate, keyOutput, aggregateOutput } = assertCodingGroupKey(step, undefined, {
    scalar, sourceKind, codingChoiceId, scalarChoiceId, subjectCandidate,
  });
  const outputs = step.outputs ?? [];
  const headers = outputs.map(output => output.label);
  const rows = records.map(record => outputs.map(output => {
    if (output.id === codingKey.outputColumnId) return clean(record.display);
    if (scalar && output.id === scalarKey.outputColumnId) return clean(record.subjectReference);
    if (output.id === aggregate.outputColumnId) return String(record.count);
    const rowValue = (step.rowValues ?? []).find(candidate => candidate.outputColumnId === output.id);
    if (rowValue) return record.ownerIDs.join('; ');
    assert.fail(`Group output ${output.id} does not map to an exact Coding identity, scalar key, count, or contributor value.`);
  }));
  return { headers, rows, codingOutput: keyOutput, aggregateOutput };
}

function readPreviewGrid(page, kind) {
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

async function waitGridMatch(page, kind, expectedHeaders, expectedRows) {
  await page.waitForFunction(({ kind, expectedHeaders, expectedRows }) => {
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
}

async function waitProposalReady(page, panel = 'construction-proposal-panel') {
  await page.waitForFunction(({ panel }) =>
    document.querySelector(`[data-testid="${panel}"]`)?.dataset.proposalStatus === 'ready' &&
    document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready',
  { panel }, { timeout: ACTION_BUDGET_MS });
}

async function readCodingGroupOptions(groupBox) {
  return groupBox.locator('input[type="checkbox"]').evaluateAll(inputs => inputs.map((input, index) => {
    const attributes = Object.fromEntries([...input.attributes]
      .filter(attribute => attribute.name.startsWith('data-'))
      .map(attribute => [attribute.name, attribute.value]));
    const label = input.closest('label');
    const fieldDetails = input.closest('[data-testid^="construction-group-field-details-"]');
    const option = input.closest('[data-testid="construction-group-semantic-option"]') ??
      input.closest('[data-testid="construction-group-source-option"]');
    return {
      index,
      ariaLabel: input.getAttribute('aria-label') ?? '',
      value: input.value,
      checked: input.checked,
      disabled: input.disabled,
      labelText: (label?.innerText ?? '').replace(/\s+/g, ' ').trim(),
      detailsText: (fieldDetails?.innerText ?? '').replace(/\s+/g, ' ').trim(),
      optionText: (option?.innerText ?? '').replace(/\s+/g, ' ').trim(),
      attributes,
    };
  }));
}

function isExactCodingSourceChoice(option) {
  const serialized = JSON.stringify(option);
  return serialized.includes(VALUE_CODEABLE_CONCEPT_CODING_PATH) &&
    !serialized.includes('Observation.code.coding[]') &&
    (serialized.includes('CODING_IDENTITY') ||
      /Value Codeable Concept.{0,80}Coding|Coding.{0,80}Value Codeable Concept/i.test(serialized));
}

function isSubjectScalarChoice(option, subjectCandidate) {
  return option.ariaLabel === `Group by ${subjectCandidate.label}` ||
    JSON.stringify(option).includes(subjectCandidate.fieldPath);
}

function groupProposalStep(request) {
  const body = request.postDataJSON();
  return body?.candidateConstruction?.steps?.at(-1);
}

function requestHasGroupShape(request, { scalar, outputId }) {
  try {
    if (request.method() !== 'POST' || !request.url().endsWith('/construction-proposals')) return false;
    const body = request.postDataJSON();
    if (body?.outputId !== outputId) return false;
    const step = body.candidateConstruction?.steps?.at(-1);
    if (step?.operation?.kind !== 'GROUP') return false;
    const keys = step.operation.group?.keys ?? [];
    return keys.length === (scalar ? 2 : 1) &&
      keys.every(key => key.fieldSource?.kind === 'CHOICE' && typeof key.fieldSource.choiceId === 'string');
  } catch {
    return false;
  }
}

export async function semanticGroupExpandedLifecycle({ page, cda }) {
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arango = validatedArangoContainer(cda.target);
  const database = cda.target.arangoDatabase ?? 'loom_dev';
  assert.equal(project, cda.target.fixtureProject);
  assert.equal(generation, cda.target.fixtureGeneration);
  assert.equal(generation, 'cda-fhir-v1');
  assert(apiOrigin && uiOrigin);

  const report = cda.report;
  protectNativeRequestBodies(report);
  report.phase = 'native expanded Value Codeable Concept Coding Group lifecycle';
  report.scope = {
    project, generation, sourceResourceType: 'Observation',
    sourcePath: VALUE_CODEABLE_CONCEPT_CODING_PATH, occurrenceId: 'base',
    rawRowLimit: SEMANTIC_GROUP_EXPANDED_SCAN_LIMIT, actionBudgetMs: ACTION_BUDGET_MS,
  };
  const oracle = buildSemanticGroupExpandedOracle(
    readRawCodingRows(arango, database, project, generation), { project, generation },
  );
  const oracleSummary = summarizeOracle(oracle);
  report.oracle = oracleSummary;
  cda.check('correctness',
    'bounded raw Observation oracle pins exact valueCodeableConcept Coding tuples, display labels, owners, and ordinals',
    true, oracleSummary);

  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `semantic-group-expanded-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch {
      throw new Error(`${path} returned non-JSON HTTP ${response.status()} ` +
        `(bodySha256=${stableDigest(clean(text))}).`);
    }
    assert(response.ok(), `${path} returned HTTP ${response.status()} (bodySha256=${stableDigest(value)}).`);
    return value;
  };
  const rootPath = `/api/v1/projects/${encode(project)}/explorers`;
  const explorer = `semantic-group-expanded-${randomUUID()}`;
  const authoringPath = `${rootPath}/${encode(explorer)}/authoring/v2`;
  await api(rootPath, { name: explorer, title: 'Expanded Observation Coding Group verification' });
  let builder = await api(`${authoringPath}/builder`);
  assert.equal(builder.catalog?.generation, generation);
  const observationNode = builder.catalog?.nodes?.find(node =>
    node.resourceType === 'Observation' && node.rowRootEligible) ??
    builder.catalog?.nodes?.find(node => node.resourceType === 'Observation');
  assert(observationNode?.nodeId, 'The owned CDA Builder must expose an Observation root.');
  const rootCandidates = builder.catalog.candidates.filter(candidate => candidate.nodeId === observationNode.nodeId);
  const idCandidate = rootCandidates.find(candidate => candidate.fieldPath === 'id');
  const subjectCandidate = rootCandidates.find(candidate =>
    candidate.fieldPath === 'subject.reference' && candidate.populated === true);
  assert(idCandidate?.candidateId, 'The exact Observation owner ID source field must be available.');
  assert(subjectCandidate?.candidateId,
    'The exact Observation.subject.reference scalar Group key must be available.');

  const command = async commands => {
    await api(`${authoringPath}/commands`, {
      commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    });
    builder = await api(`${authoringPath}/builder`);
  };
  await command([{ type: 'CREATE_TABLE', title: 'Expanded Value Codeable Concept Coding', rootNodeId: observationNode.nodeId }]);
  const sourceDocument = builder.workspace.documents.find(document =>
    document.output?.title === 'Expanded Value Codeable Concept Coding');
  assert(sourceDocument?.output?.id, 'Fresh CDA QA table creation must return an exact output identity.');
  const outputId = sourceDocument.output.id;
  assert.equal(sourceDocument.rootResourceType, 'Observation');
  assert.equal(sourceDocument.columns.length, 0,
    'The native case must apply source-row expansion and Group before adding public columns.');

  const selection = await api(`${rootPath}/${encode(explorer)}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `semantic-group-expanded-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: oracle.sourceIDs.map(id => ({
      project, generation, resourceType: 'Observation', id,
    })) } },
  });
  assert.equal(selection.project, project);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Observation');
  assert.equal(selection.memberCount, oracle.sourceIDs.length);
  const routes = await api(`${authoringPath}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken, outputId,
    selectionRevisionId: selection.id, limit: 50,
  });
  const rootRoute = routes.choices.find(choice => choice.route.length === 0);
  assert(rootRoute?.routeChoiceId, 'The selected Observation owners must resolve to a direct root route.');
  await command([{
    type: 'SET_TABLE_POPULATION', outputId,
    selectionRevisionId: selection.id, routeChoiceId: rootRoute.routeChoiceId,
  }]);
  assert.equal(documentFor(builder, outputId).columns.length, 0,
    'Population setup must not add public columns before Group.');

  const rowChoiceResponse = await api(`${authoringPath}/row-definition-choices?${new URLSearchParams({
    snapshotToken: builder.catalog.snapshotToken, outputId,
  })}`);
  const rowChoices = rowChoiceResponse.choices ?? [];
  const matchingChoices = rowChoices.filter(choice => choice.kind === 'EXPANDED' &&
    choice.fieldPath === VALUE_CODEABLE_CONCEPT_CODING_PATH && choice.occurrenceId === 'base');
  assert.equal(matchingChoices.length, 1,
    `The selected Observation table must expose exactly the Value Codeable Concept → Coding source expansion; found ${JSON.stringify(rowChoices.map(choice => ({ fieldPath: choice.fieldPath, occurrenceId: choice.occurrenceId, kind: choice.kind })))}`);
  const rowChoice = matchingChoices[0];
  assert.equal(rowChoice.fhirType, 'Coding');
  assert.equal(rowChoice.cardinality, 'MANY');
  assert.equal(rowChoice.shape, 'ARRAY');
  assert.equal(rowChoice.route?.length ?? 0, 0,
    'The bounded CDA source witness uses the direct Observation owner route.');
  report.rowChoice = {
    kind: rowChoice.kind, fieldPath: rowChoice.fieldPath,
    occurrenceId: rowChoice.occurrenceId, fhirType: rowChoice.fhirType,
    cardinality: rowChoice.cardinality, shape: rowChoice.shape,
    route: rowChoice.route ?? [], choiceId: rowChoice.choiceId,
  };
  cda.check('usability',
    'native source-row chooser exposes the exact Value Codeable Concept → Coding expansion on Observation',
    true, report.rowChoice);

  let codingChoiceId;
  let scalarChoiceId;
  const assertGroupKey = (step, { scalar, sourceKind = 'BINDING' } = {}) => assertCodingGroupKey(step, rowChoice, {
    scalar, sourceKind, codingChoiceId, scalarChoiceId, subjectCandidate,
  });

  const uiURL = `${uiOrigin}/?project=${encode(project)}&explorer=${encode(explorer)}&mode=builder`;
  const proposalPath = `${authoringPath}/construction-proposals`;
  const actionLabels = [];
  const action = async (label, locator, perform, after, editable = false) => {
    const startedAt = Date.now();
    await cda.action(label, locator, perform, {
      timeout: ACTION_BUDGET_MS,
      budget: ACTION_BUDGET_MS,
      editable,
      ...(after ? { after, requiredCheck: 'all native expanded Coding Group actions stay within five seconds' } : {}),
    });
    const durationMs = Date.now() - startedAt;
    actionLabels.push({ label, durationMs });
    assert(durationMs <= ACTION_BUDGET_MS, `${label} took ${durationMs} ms; limit is ${ACTION_BUDGET_MS} ms.`);
  };
  const readBuilder = () => api(`${authoringPath}/builder`);
  const getDocument = state => documentFor(state, outputId);
  const getGroupStep = state => groupStepFor(state, outputId);
  const waitProposalPreview = () => waitProposalReady(page);
  const proposalReceipt = async () => page.locator('[data-testid="construction-proposal-preview"]')
    .getAttribute('data-preview-receipt-id').catch(() => null);
  const waitNextProposal = priorReceipt => page.waitForFunction(previous => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    return preview?.dataset.previewStatus === 'ready' &&
      Boolean(preview.dataset.previewReceiptId) && preview.dataset.previewReceiptId !== previous;
  }, priorReceipt, { timeout: ACTION_BUDGET_MS });
  const proposalRequest = (shape, requestOutputId = outputId) => page.waitForRequest(
    request => requestHasGroupShape(request, { scalar: shape === 'with-scalar', outputId: requestOutputId }),
    { timeout: ACTION_BUDGET_MS },
  );
  const reloadToTable = async expectedGrid => {
    const startedAt = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
    const table = page.getByTestId(`construction-table-${outputId}`);
    await table.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    const preview = page.locator('[data-testid="preview-table-scroll"] [role="table"]');
    if (await preview.count() === 0 || !await preview.isVisible().catch(() => false)) {
      await action('select the exact expanded Observation output after reload', table,
        () => table.click({ timeout: ACTION_BUDGET_MS }), async () => {
          if (expectedGrid) await waitGridMatch(page, 'saved', expectedGrid.headers, expectedGrid.rows);
          else await preview.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
        });
    } else if (expectedGrid) {
      await waitGridMatch(page, 'saved', expectedGrid.headers, expectedGrid.rows);
    }
    const elapsedMs = Date.now() - startedAt;
    actionLabels.push({ label: 'reload and restore exact expanded Observation table', durationMs: elapsedMs });
    assert(elapsedMs <= ACTION_BUDGET_MS,
      `Reload and table restore took ${elapsedMs} ms; limit is ${ACTION_BUDGET_MS} ms.`);
  };
  const openTable = async expectedGrid => {
    const startedAt = Date.now();
    await cda.navigate(uiURL);
    const table = page.getByTestId(`construction-table-${outputId}`);
    await table.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    const preview = page.locator('[data-testid="preview-table-scroll"] [role="table"]');
    if (await preview.count() === 0 || !await preview.isVisible().catch(() => false)) {
      await action('select the exact fresh Observation source table', table,
        () => table.click({ timeout: ACTION_BUDGET_MS }), async () => {
          if (expectedGrid) await waitGridMatch(page, 'saved', expectedGrid.headers, expectedGrid.rows);
          else await preview.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
        });
    } else if (expectedGrid) {
      await waitGridMatch(page, 'saved', expectedGrid.headers, expectedGrid.rows);
    }
    const elapsedMs = Date.now() - startedAt;
    actionLabels.push({ label: 'open exact expanded Observation table', durationMs: elapsedMs });
    assert(elapsedMs <= ACTION_BUDGET_MS,
      `Opening the Observation table took ${elapsedMs} ms; limit is ${ACTION_BUDGET_MS} ms.`);
  };
  const clickRowSettings = async after => {
    const settings = page.getByTestId('construction-rows-settings-trigger');
    await settings.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    await action('open native row operation settings', settings,
      () => settings.click({ timeout: ACTION_BUDGET_MS }), after);
  };
  const rowShapeSelect = () => page.locator(
    'select[aria-label="What should each row represent?"], select[aria-label="Rows per starting record"]',
  );
  const expandRows = async () => {
    await clickRowSettings(async () => rowShapeSelect().first().waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
    const shape = rowShapeSelect();
    await shape.first().waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    assert.equal(await shape.count(), 1, 'The native source row-shape select must be unique.');
    const options = await shape.evaluate(element => [...element.options].map(option => ({
      value: option.value, label: option.label, disabled: option.disabled,
    })));
    const expandedOption = options.find(option => option.value === `expanded:${rowChoice.choiceId}`);
    assert(expandedOption,
      `The native row selector must expose the exact ${VALUE_CODEABLE_CONCEPT_CODING_PATH} choice: ${JSON.stringify(options)}`);
    assert(/Value Codeable Concept.{0,80}Coding|Coding.{0,80}Value Codeable Concept/i.test(expandedOption.label),
      `The visible row choice must identify Value Codeable Concept → Coding: ${expandedOption.label}`);
    const receiptBefore = await proposalReceipt();
    await action('choose Value Codeable Concept → Coding source rows', shape,
      () => shape.selectOption(expandedOption.value, { timeout: ACTION_BUDGET_MS }),
      async () => waitNextProposal(receiptBefore));
    const policy = page.locator('select[aria-label="Unmatched record policy"]');
    if (await policy.count() === 1) {
      const value = `expanded:${rowChoice.choiceId}:PRESERVE_PARENT`;
      const policyReceipt = await proposalReceipt();
      await action('preserve each selected Observation owner during Coding expansion', policy,
        () => policy.selectOption(value, { timeout: ACTION_BUDGET_MS }),
        async () => waitNextProposal(policyReceipt));
    }
    await waitProposalPreview();
    const expandedSummary = await page.getByLabel('Row definition preview', { exact: true }).innerText();
    assert(expandedSummary.includes(`→ ${oracle.expectedExpandedRowCount} rows`),
      `Native Coding expansion preview row count differs from the raw tuple oracle: ${expandedSummary}`);
    return { shape, policy };
  };
  const finishRowExpansion = async ({ apply }) => {
    const dialog = page.getByLabel('Row definition settings', { exact: true });
    const button = dialog.getByRole('button', { name: apply ? 'Apply row definition' : 'Cancel', exact: true });
    await action(apply ? 'Apply exact Value Codeable Concept Coding row expansion' : 'Cancel Coding row expansion preview',
      button, () => button.click({ timeout: ACTION_BUDGET_MS }), async () => {
        await dialog.waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      });
  };

  await openTable();
  const initialDraft = await readBuilder();
  assert.equal(getDocument(initialDraft).rows.kind, 'RECORDS');
  assert.equal(getDocument(initialDraft).columns.length, 0);
  await expandRows();
  const beforeExpansionCancel = await readBuilder();
  await finishRowExpansion({ apply: false });
  const afterExpansionCancel = await readBuilder();
  assert.equal(afterExpansionCancel.draftVersion, beforeExpansionCancel.draftVersion);
  assert.equal(afterExpansionCancel.draftDigest, beforeExpansionCancel.draftDigest);
  assert.equal(getDocument(afterExpansionCancel).rows.kind, 'RECORDS');
  cda.check('persistence',
    'Cancel leaves the fresh Observation source table and draft unchanged before Coding expansion',
    true, { draftVersion: afterExpansionCancel.draftVersion, draftDigest: afterExpansionCancel.draftDigest });

  await expandRows();
  await finishRowExpansion({ apply: true });
  builder = await readBuilder();
  const expandedDocument = getDocument(builder);
  assert.equal(expandedDocument.rows.kind, 'EXPANDED');
  assert.equal(expandedDocument.rows.expanded.scopePath, VALUE_CODEABLE_CONCEPT_CODING_PATH);
  assert.equal(expandedDocument.rows.expanded.occurrenceId ?? 'base', 'base');
  assert.equal(expandedDocument.columns.length, 0,
    'Native Group must be authored before any public source columns are selected.');
  cda.check('correctness',
    'Apply saves the exact base Observation.valueCodeableConcept.coding[] source-row expansion with no public columns',
    true, {
      rows: expandedDocument.rows,
      selectedRootCount: oracleSummary.selectedRootCount,
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      oracleDigest: oracleSummary.digest,
      publicColumnCount: expandedDocument.columns.length,
    });

  const waitGroupEditor = async () => page.getByTestId('construction-reshape-group')
    .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const openGroup = async () => {
    await clickRowSettings(async () => page.getByTestId('construction-action-group-rows')
      .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
    const groupAction = page.getByTestId('construction-action-group-rows');
    const editor = page.getByTestId('construction-reshape-group');
    await action('open native Group for expanded Coding rows', groupAction,
      () => groupAction.click({ timeout: ACTION_BUDGET_MS }), async () => {
        await waitGroupEditor();
        await waitProposalPreview();
      });
    return editor;
  };
  const chooseGroupOptions = async (groupBox, { scalar, expectedShape }) => {
    const options = await readCodingGroupOptions(groupBox);
    const codingMatches = options.filter(isExactCodingSourceChoice);
    assert.equal(codingMatches.length, 1,
      `Group must expose one current Value Codeable Concept Coding identity option, not a root-wide Coding family: ${JSON.stringify(options)}`);
    const codingOption = codingMatches[0];
    const scalarMatches = options.filter(option => isSubjectScalarChoice(option, subjectCandidate));
    assert.equal(scalarMatches.length, 1,
      `Group must expose the exact populated scalar ${subjectCandidate.fieldPath}: ${JSON.stringify(options)}`);
    const scalarOption = scalarMatches[0];
    if (expectedShape === 'with-scalar') {
      for (const option of options) {
        const keep = option.index === codingOption.index || option.index === scalarOption.index;
        if (option.checked && !keep) {
          const checkbox = groupBox.locator('input[type="checkbox"]').nth(option.index);
          const previous = await proposalReceipt();
          await action(`clear unrelated Group key ${option.ariaLabel || option.labelText}`, checkbox,
            () => checkbox.uncheck({ timeout: ACTION_BUDGET_MS }), async () => waitNextProposal(previous));
        }
      }
    }
    const preferred = expectedShape === 'with-scalar'
      ? [codingOption, scalarOption]
      : [codingOption];
    let finalRequest;
    for (const option of preferred) {
      if (option.checked) continue;
      const current = await readCodingGroupOptions(groupBox);
      const nextChecked = new Set(current.filter(item => item.checked).map(item => item.index));
      nextChecked.add(option.index);
      const nextShape = nextChecked.has(scalarOption.index) ? 'with-scalar' : 'coding-only';
      const request = proposalRequest(nextShape);
      const previous = await proposalReceipt();
      const checkbox = groupBox.locator('input[type="checkbox"]').nth(option.index);
      await action(`select ${option === codingOption ? 'exact current Coding identity' : 'Observation.subject.reference'} Group key`,
        checkbox, () => checkbox.check({ timeout: ACTION_BUDGET_MS }), async () => waitNextProposal(previous));
      finalRequest = await request;
    }
    if (!finalRequest) {
      const target = expectedShape === 'with-scalar' ? scalarOption : codingOption;
      const checkbox = groupBox.locator('input[type="checkbox"]').nth(target.index);
      const firstShape = expectedShape === 'with-scalar' ? 'coding-only' : 'coding-only';
      const firstRequest = proposalRequest(firstShape);
      const firstReceipt = await proposalReceipt();
      await action(`refresh exact ${target === codingOption ? 'Coding identity' : 'scalar'} Group selection`, checkbox,
        () => checkbox.uncheck({ timeout: ACTION_BUDGET_MS }), async () => waitNextProposal(firstReceipt));
      await firstRequest;
      const finalShapeRequest = proposalRequest(expectedShape);
      const secondReceipt = await proposalReceipt();
      await action(`restore exact ${target === codingOption ? 'Coding identity' : 'scalar'} Group selection`, checkbox,
        () => checkbox.check({ timeout: ACTION_BUDGET_MS }), async () => waitNextProposal(secondReceipt));
      finalRequest = await finalShapeRequest;
    }
    const checked = await readCodingGroupOptions(groupBox);
    assert.equal(checked.find(option => option.index === codingOption.index)?.checked, true);
    assert.equal(checked.find(option => option.index === scalarOption.index)?.checked, expectedShape === 'with-scalar');
    const current = checked.filter(option => option.checked);
    assert.equal(current.length, expectedShape === 'with-scalar' ? 2 : 1,
      `Group selection must contain only the exact current Coding source${expectedShape === 'with-scalar' ? ' and scalar key' : ''}.`);
    return { codingOption, scalarOption, finalRequest };
  };
  const assertCandidate = (request, { scalar }) => {
    const step = groupProposalStep(request);
    const keys = step?.operation?.group?.keys ?? [];
    const { codingKey, scalarKey } = assertGroupKey(step, { scalar, sourceKind: 'CHOICE' });
    assert.equal(keys.filter(key => key.fieldSource?.kind === 'CHOICE').length, keys.length);
    return { step, codingKey, scalarKey };
  };
  const gridFor = (step, records, { scalar, sourceKind }) => expectedGroupGrid(step, records, {
    scalar, sourceKind, codingChoiceId, scalarChoiceId, subjectCandidate,
  });
  const assertProposalGrid = async (step, records, { scalar, phase }) => {
    const expected = gridFor(step, records, { scalar, sourceKind: 'CHOICE' });
    await waitGridMatch(page, 'proposal', expected.headers, expected.rows);
    const actual = await readPreviewGrid(page, 'proposal');
    assert.deepEqual(actual.headers, expected.headers, `${phase} changed the exact Group output labels.`);
    assertExactRows(actual.rows, expected.rows,
      `${phase} differs from the exact (system|null, version|null, code) occurrence oracle`);
    return { expected, actual };
  };
  const assertSavedGrid = async (step, records, { scalar, phase } = {}) => {
    const expected = gridFor(step, records, { scalar, sourceKind: 'BINDING' });
    await waitGridMatch(page, 'saved', expected.headers, expected.rows);
    const actual = await readPreviewGrid(page, 'saved');
    assert.deepEqual(actual.headers, expected.headers, `${phase} changed the exact saved Group output labels.`);
    assertExactRows(actual.rows, expected.rows,
      `${phase} differs from the exact grouped Coding identity counts`);
    return { expected, actual };
  };

  const initialGroupBox = await openGroup();
  const initialOptions = await readCodingGroupOptions(initialGroupBox);
  const sourceOption = initialOptions.filter(isExactCodingSourceChoice);
  assert.equal(sourceOption.length, 1,
    `Group must offer the expanded current Coding identity ${VALUE_CODEABLE_CONCEPT_CODING_PATH}: ${JSON.stringify(initialOptions)}`);
  const scalarOption = initialOptions.filter(option => isSubjectScalarChoice(option, subjectCandidate));
  assert.equal(scalarOption.length, 1, 'The Group editor must expose the exact source scalar key.');
  codingChoiceId = sourceOption[0].value;
  scalarChoiceId = scalarOption[0].value;
  assert(codingChoiceId.trim(), 'The Coding source must expose its signed Group choice ID.');
  assert(scalarChoiceId.trim(), 'The scalar source must expose its signed Group choice ID.');
  const selected = await chooseGroupOptions(initialGroupBox, { scalar: true, expectedShape: 'with-scalar' });
  assert.equal(selected.codingOption.value, codingChoiceId);
  assert.equal(selected.scalarOption.value, scalarChoiceId);
  const firstRequest = selected.finalRequest;
  const initialCandidate = assertCandidate(firstRequest, { scalar: true });
  const firstPreview = await assertProposalGrid(initialCandidate.step, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'expanded Coding plus scalar Group Preview',
  });
  cda.check('usability',
    'native Group selects CODING_IDENTITY for valueCodeableConcept.coding[] and the exact scalar key before public columns',
    true, {
      codingSourceChoice: selected.codingOption,
      scalarSourceChoice: selected.scalarOption,
      fieldSource: initialCandidate.codingKey.fieldSource,
      keys: initialCandidate.step.operation.group.keys,
      publicColumnCountBeforeGroup: expandedDocument.columns.length,
    });
  cda.check('correctness',
    'initial Group Preview matches exact Coding system, version, code, display label, subject, and occurrence counts',
    true, {
      headers: firstPreview.actual.headers,
      groupRowCount: firstPreview.actual.rows.length,
      oracleDigest: oracleSummary.digest,
      codingIdentityCount: oracleSummary.codingIdentityCount,
      subjectCodingGroupCount: oracleSummary.subjectCodingGroupCount,
    });

  const beforeGroupCancel = await readBuilder();
  await action('Cancel expanded Coding Group Preview', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click({ timeout: ACTION_BUDGET_MS }),
    () => page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS }));
  const afterGroupCancel = await readBuilder();
  assert.deepEqual(afterGroupCancel.workspace, beforeGroupCancel.workspace);
  assert.equal(afterGroupCancel.draftVersion, beforeGroupCancel.draftVersion);
  assert.equal(afterGroupCancel.draftDigest, beforeGroupCancel.draftDigest);
  assert.deepEqual(getDocument(afterGroupCancel).construction?.steps ?? [], []);
  assert.equal(getDocument(afterGroupCancel).rows.expanded.scopePath, VALUE_CODEABLE_CONCEPT_CODING_PATH);
  cda.check('persistence',
    'Cancel preserves the exact expanded Coding source table and draft before Group Apply',
    true, { draftVersion: afterGroupCancel.draftVersion, draftDigest: afterGroupCancel.draftDigest });

  await openGroup();
  const applyOptions = await chooseGroupOptions(page.getByTestId('construction-reshape-group'), {
    scalar: true, expectedShape: 'with-scalar',
  });
  const applyRequest = applyOptions.finalRequest;
  const applyCandidate = assertCandidate(applyRequest, { scalar: true });
  const applyPreview = await assertProposalGrid(applyCandidate.step, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'applied expanded Coding Group Preview',
  });
  const firstStepID = applyCandidate.step.id;
  const firstAppliedGrid = applyPreview.expected;
  await action('Apply expanded Coding Group before adding public columns', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitGridMatch(page, 'saved', firstAppliedGrid.headers, firstAppliedGrid.rows);
    });
  builder = await readBuilder();
  let savedGroup = getGroupStep(builder);
  assert(savedGroup?.id);
  assert.equal(savedGroup.id, firstStepID);
  assertGroupKey(savedGroup, { scalar: true });
  const codingSourceBinding = codingGroupKey(savedGroup, { sourceKind: 'BINDING' }).fieldSource;
  const scalarSourceBinding = scalarGroupKey(savedGroup, {
    sourceKind: 'BINDING', canonicalPath: subjectCandidate.fieldPath,
  }).fieldSource;
  assert.equal(getDocument(builder).columns.length, 0,
    'The Group operation must apply before a public source column is selected.');
  const appliedBeforeReload = await assertSavedGrid(savedGroup, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'applied expanded Coding Group',
  });
  cda.check('persistence',
    'Apply saves current Coding identity GROUP and the scalar key with exact tuple counts before public columns',
    true, {
      groupStepId: savedGroup.id,
      outputCount: savedGroup.outputs.length,
      groupRowCount: appliedBeforeReload.actual.rows.length,
      codingBinding: {
        kind: codingSourceBinding.kind,
        valueKind: codingSourceBinding.valueKind,
        sourceRole: codingSourceBinding.sourceRole,
        occurrenceId: codingSourceBinding.occurrenceId,
        ownerResourceType: codingSourceBinding.ownerResourceType,
        canonicalPath: codingSourceBinding.canonicalPath,
      },
      scalarBinding: {
        kind: scalarSourceBinding.kind,
        valueKind: scalarSourceBinding.valueKind,
        sourceRole: scalarSourceBinding.sourceRole,
        occurrenceId: scalarSourceBinding.occurrenceId,
        ownerResourceType: scalarSourceBinding.ownerResourceType,
        canonicalPath: scalarSourceBinding.canonicalPath,
      },
      oracleDigest: oracleSummary.digest,
    });

  await reloadToTable(appliedBeforeReload.expected);
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assert.equal(savedGroup?.id, firstStepID);
  assertGroupKey(savedGroup, { scalar: true });
  const reloadedBeforeEdit = await assertSavedGrid(savedGroup, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'reloaded expanded Coding Group',
  });
  cda.check('persistence',
    'reloaded Coding Group preserves exact source row expansion, tuple identities, scalar key, and counts',
    true, {
      groupRowCount: reloadedBeforeEdit.actual.rows.length,
      rowShape: getDocument(builder).rows,
      oracleDigest: oracleSummary.digest,
    });

  const history = page.getByTestId('construction-row-operation-history');
  if (!await history.isVisible().catch(() => false)) {
    await clickRowSettings(() => history.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
  }
  const editButton = page.getByTestId(`construction-row-edit-${savedGroup.id}`);
  await action('open saved expanded Coding Group editor', editButton,
    () => editButton.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await waitGroupEditor();
      await waitProposalPreview();
    });
  const editOptions = await readCodingGroupOptions(page.getByTestId('construction-reshape-group'));
  const editCoding = editOptions.filter(isExactCodingSourceChoice);
  const editScalar = editOptions.filter(option => isSubjectScalarChoice(option, subjectCandidate));
  assert.equal(editCoding.length, 1,
    `Saved Group edit must rehydrate only the exact Coding identity source: ${JSON.stringify(editOptions)}`);
  assert.equal(editScalar.length, 1, 'Saved Group edit must rehydrate the exact subject scalar key.');
  assert.equal(editCoding[0].checked, true);
  assert.equal(editScalar[0].checked, true);
  codingChoiceId = editCoding[0].value;
  scalarChoiceId = editScalar[0].value;
  assert(codingChoiceId.trim() && scalarChoiceId.trim(), 'Saved Group edit choices must retain exact capability-issued IDs.');
  const editRequest = proposalRequest('coding-only');
  const editReceipt = await proposalReceipt();
  const scalarCheckbox = page.getByTestId('construction-reshape-group')
    .locator('input[type="checkbox"]').nth(editScalar[0].index);
  await action('edit Group to the exact Coding identity key only', scalarCheckbox,
    () => scalarCheckbox.uncheck({ timeout: ACTION_BUDGET_MS }), async () => waitNextProposal(editReceipt));
  const editedRequest = await editRequest;
  const editedCandidate = assertCandidate(editedRequest, { scalar: false });
  const editPreview = await assertProposalGrid(editedCandidate.step, oracle.expectedIdentityGroups, {
    scalar: false, phase: 'Coding-only saved Group edit Preview',
  });
  cda.check('usability',
    'saved Group edit rehydrates the exact current Coding identity and scalar key before the edit',
    true, { codingOption: editCoding[0], scalarOption: editScalar[0] });
  cda.check('correctness',
    'Coding-only edit Preview matches exact tuple identity, observed display labels, and occurrence multiplicity',
    true, {
      headers: editPreview.actual.headers,
      groupRowCount: editPreview.actual.rows.length,
      codingIdentityCount: oracleSummary.codingIdentityCount,
      oracleDigest: oracleSummary.digest,
    });

  const beforeEditCancel = await readBuilder();
  await action('Cancel Coding-only Group edit', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click({ timeout: ACTION_BUDGET_MS }),
    () => page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS }));
  const afterEditCancel = await readBuilder();
  assert.deepEqual(afterEditCancel.workspace, beforeEditCancel.workspace);
  assert.equal(afterEditCancel.draftVersion, beforeEditCancel.draftVersion);
  assert.equal(afterEditCancel.draftDigest, beforeEditCancel.draftDigest);
  assertGroupKey(getGroupStep(afterEditCancel), { scalar: true });
  cda.check('persistence', 'Cancel preserves saved Coding Group scalar key, tuple counts, and draft identity', true, {
    draftVersion: afterEditCancel.draftVersion, draftDigest: afterEditCancel.draftDigest,
  });

  const currentGroup = getGroupStep(afterEditCancel);
  const currentHistory = page.getByTestId('construction-row-operation-history');
  const reopenEdit = page.getByTestId(`construction-row-edit-${currentGroup.id}`);
  await action('reopen saved Coding Group edit to apply its exact current-item grain', reopenEdit,
    () => reopenEdit.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await waitGroupEditor();
      await waitProposalPreview();
    });
  const reapplyOptions = await readCodingGroupOptions(page.getByTestId('construction-reshape-group'));
  const reapplyCoding = reapplyOptions.filter(isExactCodingSourceChoice);
  const reapplyScalar = reapplyOptions.filter(option => isSubjectScalarChoice(option, subjectCandidate));
  assert.equal(reapplyCoding.length, 1);
  assert.equal(reapplyScalar.length, 1);
  assert.equal(reapplyCoding[0].checked, true);
  assert.equal(reapplyScalar[0].checked, true);
  codingChoiceId = reapplyCoding[0].value;
  scalarChoiceId = reapplyScalar[0].value;
  assert(codingChoiceId.trim() && scalarChoiceId.trim());
  const reapplyRequest = proposalRequest('coding-only');
  const reapplyReceipt = await proposalReceipt();
  const reapplyCheckbox = page.getByTestId('construction-reshape-group')
    .locator('input[type="checkbox"]').nth(reapplyScalar[0].index);
  await action('remove scalar key from saved Coding Group', reapplyCheckbox,
    () => reapplyCheckbox.uncheck({ timeout: ACTION_BUDGET_MS }), async () => waitNextProposal(reapplyReceipt));
  const reapplyCandidate = assertCandidate(await reapplyRequest, { scalar: false });
  const reapplyPreview = await assertProposalGrid(reapplyCandidate.step, oracle.expectedIdentityGroups, {
    scalar: false, phase: 'applied Coding-only Group edit Preview',
  });
  const editedStepID = reapplyCandidate.step.id;
  await action('Apply Coding-only Group edit', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitGridMatch(page, 'saved', reapplyPreview.expected.headers, reapplyPreview.expected.rows);
    });
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assert.equal(savedGroup?.id, editedStepID);
  assertGroupKey(savedGroup, { scalar: false });
  const editedSavedGrid = await assertSavedGrid(savedGroup, oracle.expectedIdentityGroups, {
    scalar: false, phase: 'saved Coding-only Group edit',
  });
  cda.check('persistence',
    'Apply persists the edited Coding identity grain and exact tuple counts',
    true, { groupStepId: savedGroup.id, groupRowCount: editedSavedGrid.actual.rows.length, oracleDigest: oracleSummary.digest });
  await reloadToTable(editedSavedGrid.expected);
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assertGroupKey(savedGroup, { scalar: false });
  const beforeColumnsGrid = await assertSavedGrid(savedGroup, oracle.expectedIdentityGroups, {
    scalar: false, phase: 'reloaded Coding-only Group',
  });
  cda.check('persistence',
    'edited Coding-only Group and exact identity counts survive Builder reload', true, {
      rowShape: getDocument(builder).rows,
      groupRowCount: beforeColumnsGrid.actual.rows.length,
      codingIdentityCount: oracleSummary.codingIdentityCount,
      oracleDigest: oracleSummary.digest,
    });

  const groupBeforeContributorEdit = getGroupStep(builder);
  const addScalarEdit = page.getByTestId(`construction-row-edit-${groupBeforeContributorEdit.id}`);
  await action('reopen saved Coding-only Group to restore its scalar owner key', addScalarEdit,
    () => addScalarEdit.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await waitGroupEditor();
      await waitProposalPreview();
    });
  const addScalarOptions = await readCodingGroupOptions(page.getByTestId('construction-reshape-group'));
  const addScalarCoding = addScalarOptions.filter(isExactCodingSourceChoice);
  const addScalarSubject = addScalarOptions.filter(option => isSubjectScalarChoice(option, subjectCandidate));
  assert.equal(addScalarCoding.length, 1);
  assert.equal(addScalarSubject.length, 1);
  assert.equal(addScalarCoding[0].checked, true);
  assert.equal(addScalarSubject[0].checked, false,
    'The applied Coding-only edit must leave its scalar owner key unchecked before it is restored.');
  codingChoiceId = addScalarCoding[0].value;
  scalarChoiceId = addScalarSubject[0].value;
  assert(codingChoiceId.trim() && scalarChoiceId.trim());
  const restoreScalarRequest = proposalRequest('with-scalar');
  const restoreScalarReceipt = await proposalReceipt();
  const restoreScalarCheckbox = page.getByTestId('construction-reshape-group')
    .locator('input[type="checkbox"]').nth(addScalarSubject[0].index);
  await action('restore Observation.subject.reference as the exact scalar Group key', restoreScalarCheckbox,
    () => restoreScalarCheckbox.check({ timeout: ACTION_BUDGET_MS }), async () => waitNextProposal(restoreScalarReceipt));
  const restoredScalarCandidate = assertCandidate(await restoreScalarRequest, { scalar: true });
  const restoredScalarPreview = await assertProposalGrid(restoredScalarCandidate.step, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'restored scalar Coding Group edit Preview',
  });
  await action('Apply restored scalar Coding Group edit', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitGridMatch(page, 'saved', restoredScalarPreview.expected.headers, restoredScalarPreview.expected.rows);
    });
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assertGroupKey(savedGroup, { scalar: true });
  const restoredScalarGrid = await assertSavedGrid(savedGroup, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'saved Coding Group with restored scalar owner key',
  });
  cda.check('persistence',
    'Group edit restores the exact scalar owner key while retaining Coding tuple counts',
    true, {
      groupStepId: savedGroup.id,
      groupRowCount: restoredScalarGrid.actual.rows.length,
      subjectCodingGroupCount: oracleSummary.subjectCodingGroupCount,
      oracleDigest: oracleSummary.digest,
    });
  await reloadToTable(restoredScalarGrid.expected);
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assertGroupKey(savedGroup, { scalar: true });
  await assertSavedGrid(savedGroup, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'reloaded Coding Group with restored scalar owner key',
  });

  const openAddColumns = async () => {
    const add = page.getByTestId('construction-action-add-columns');
    await add.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    await action('open Add columns after Coding Group Apply and reload', add,
      () => add.click({ timeout: ACTION_BUDGET_MS }), async () => {
        await page.getByTestId('construction-add-columns-source').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      });
    const fieldsTab = page.getByRole('tab', { name: 'Other fields', exact: true });
    if (await fieldsTab.count() === 1 && await fieldsTab.getAttribute('aria-selected') !== 'true') {
      await action('open the exact source fields in Add columns', fieldsTab,
        () => fieldsTab.click({ timeout: ACTION_BUDGET_MS }), () => fieldsTab.getAttribute('aria-selected').then(value => assert.equal(value, 'true')));
    }
    const rootSource = page.locator('[data-testid="construction-add-columns-source-option"][data-source-kind="ROOT"]');
    await rootSource.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    if (await rootSource.getAttribute('aria-pressed') !== 'true') {
      await action('select the exact Observation owner for Add columns', rootSource,
        () => rootSource.click({ timeout: ACTION_BUDGET_MS }), () => rootSource.getAttribute('aria-pressed').then(value => assert.equal(value, 'true')));
    }
  };
  const chooseObservationID = async () => {
    await openAddColumns();
    const rawFields = page.locator('[data-testid="feature-catalog-raw-fields"] > summary');
    if (await rawFields.count() === 1 && await rawFields.evaluate(element => !element.parentElement.open)) {
      await action('open raw Observation fields in Add columns', rawFields,
        () => rawFields.click({ timeout: ACTION_BUDGET_MS }), () => page.locator('input[type="checkbox"][aria-label^="Select "]')
          .first().waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
    }
    const fieldOptions = await page.locator('input[type="checkbox"][aria-label^="Select "]').evaluateAll(inputs => inputs.map((input, index) => ({
      index,
      ariaLabel: input.getAttribute('aria-label') ?? '',
      disabled: input.disabled,
      text: (input.closest('label')?.innerText ?? '').replace(/\s+/g, ' ').trim(),
      data: Object.fromEntries([...input.attributes].filter(attribute => attribute.name.startsWith('data-')
        ).map(attribute => [attribute.name, attribute.value])),
    })));
    const fieldMatches = fieldOptions.filter(option =>
      option.ariaLabel === 'Select Observation.id' ||
      option.ariaLabel === `Select ${idCandidate.label}` ||
      option.text.includes('Observation.id') ||
      JSON.stringify(option).includes(idCandidate.candidateId));
    assert.equal(fieldMatches.length, 1,
      `Add columns must expose exactly the Observation.id owner field: ${JSON.stringify(fieldOptions)}`);
    const field = page.locator('input[type="checkbox"][aria-label^="Select "]').nth(fieldMatches[0].index);
    await field.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    assert.equal(await field.isEnabled(), true, 'Observation.id must be selectable as the exact contributing owner scalar.');
    await action('select exact Observation.id contributor source', field,
      () => field.check({ timeout: ACTION_BUDGET_MS }), async () => {
        await page.getByRole('button', { name: /Add 1 selected feature/ }).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      });
    const rowValuePolicy = page.getByLabel('Values per grouped row', { exact: true });
    if (await rowValuePolicy.count() === 1) {
      assert.equal(await rowValuePolicy.inputValue(), 'ALL',
        'Contributor values must keep all distinct source IDs for the exact expanded Coding Group.');
    }
    const addSelected = page.getByRole('button', { name: /Add 1 selected feature/ });
    const responsePromise = page.waitForResponse(response => {
      try {
        return response.request().method() === 'POST' &&
          new URL(response.url()).pathname.endsWith('/construction-choice-proposals');
      } catch { return false; }
    }, { timeout: ACTION_BUDGET_MS });
    await action('preview Observation.id from exact Group contributing owners', addSelected,
      () => addSelected.click({ timeout: ACTION_BUDGET_MS }), async () => {
        await page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
        await page.waitForFunction(() =>
          document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus === 'ready',
        undefined, { timeout: ACTION_BUDGET_MS });
      });
    const response = await responsePromise;
    const responseBody = await response.json();
    assert(response.ok(), `Add columns proposal returned HTTP ${response.status()}.`);
    const requestBody = response.request().postDataJSON();
    assert.equal(requestBody.outputId, outputId);
    assert(JSON.stringify(requestBody).includes(idCandidate.candidateId),
      'The Add columns proposal must bind the exact Observation.id catalog candidate.');
    assert(JSON.stringify(requestBody).includes('"rowValuePolicy":"ALL"'),
      'The native Add columns request must retain all distinct contributor values.');
    const preview = responseBody.preview;
    assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows),
      'Add columns response must include exact preimage contributor values.');
    const previewRows = await readPreviewGrid(page, 'proposal');
    const savedSourceDocument = getDocument(await readBuilder());
    const group = savedSourceDocument.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
    assert(group, 'Add columns preview must preserve the saved expanded Coding Group.');
    return { responseBody, requestBody, preview, previewRows, group };
  };

  const preimageBuilder = await readBuilder();
  const preimageWorkspace = structuredClone(preimageBuilder.workspace);
  const firstAdd = await chooseObservationID();
  const firstGroup = firstAdd.group;
  const { scalarKey: firstScalarKey,
    keyOutput: firstCodingOutput, aggregateOutput: firstCountOutput } =
    assertGroupKey(firstGroup, { scalar: true });
  const codingColumn = firstAdd.preview.columns.find(column =>
    column.column === firstCodingOutput.name || column.label === firstCodingOutput.label);
  const countColumn = firstAdd.preview.columns.find(column =>
    column.column === firstCountOutput.name || column.label === firstCountOutput.label);
  assert(codingColumn && countColumn, 'Add columns Preview must retain the exact Group key and COUNT_ROWS output columns.');
  const scalarOutput = firstGroup.outputs.find(output => output.id === firstScalarKey.outputColumnId);
  assert(scalarOutput?.name && scalarOutput.label, 'The Group must retain its exact scalar key output before Add columns.');
  const scalarColumn = firstAdd.preview.columns.find(column =>
    column.column === scalarOutput.name || column.label === scalarOutput.label);
  assert(scalarColumn, 'Add columns Preview must retain the exact scalar Group key.');
  const ownerColumns = firstAdd.preview.columns.filter(column =>
    column !== codingColumn && column !== countColumn && column !== scalarColumn);
  assert.equal(ownerColumns.length, 1, 'Add columns Preview must append exactly one Observation owner scalar.');
  const ownerColumn = ownerColumns[0];
  const expectedAddRows = oracle.expectedSubjectGroups.map(group => firstAdd.preview.columns.map(column => {
    if (column === codingColumn) return group.display;
    if (column === countColumn) return String(group.count);
    if (column === scalarColumn) return group.subjectReference;
    if (column === ownerColumn) return group.ownerIDs;
    assert.fail(`Add columns preview contains an unmapped output ${column.label}/${column.column}.`);
  }));
  assertExactRows(firstAdd.preview.rows.map(row => firstAdd.preview.columns.map(column => row[column.column])), expectedAddRows,
    'Add columns contributor rehydration must match exact unique owner IDs and COUNT_ROWS multiplicity for each subject and Coding identity');
  assertExactRows(firstAdd.previewRows.rows, expectedAddRows.map(row => row.map(renderCell)),
  'The visible Add columns proposal must display exact owner IDs and multiplicity-matched counts');
  cda.check('correctness',
    'Add columns rehydrates exact Observation owner IDs from pre-Group Coding occurrences with matching counts',
    true, {
      sourceColumn: idCandidate.fieldPath,
      rowValuePolicy: 'ALL',
      selectedRootCount: oracleSummary.selectedRootCount,
      subjectCodingGroupCount: oracleSummary.subjectCodingGroupCount,
      contributorOwnerValueCount: oracle.expectedSubjectGroups.reduce((sum, group) => sum + group.ownerIDs.length, 0),
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      oracleDigest: oracleSummary.digest,
    });
  await action('Cancel Observation.id contributor proposal', page.getByTestId('construction-choice-proposal-panel')
    .getByRole('button', { name: 'Cancel', exact: true }),
  () => page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Cancel', exact: true })
    .click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitGridMatch(page, 'saved', restoredScalarGrid.expected.headers, restoredScalarGrid.expected.rows);
    });
  const afterAddCancel = await readBuilder();
  assert.deepEqual(afterAddCancel.workspace, preimageWorkspace);
  assert.equal(afterAddCancel.draftVersion, preimageBuilder.draftVersion);
  assert.equal(afterAddCancel.draftDigest, preimageBuilder.draftDigest);
  cda.check('persistence',
    'Canceling Add columns preserves the edited Coding Group and exact draft preimage', true, {
      draftVersion: afterAddCancel.draftVersion, draftDigest: afterAddCancel.draftDigest,
    });

  const appliedAdd = await chooseObservationID();
  assertExactRows(appliedAdd.preview.rows.map(row =>
    appliedAdd.preview.columns.map(column => row[column.column])), expectedAddRows,
  'The fresh Add columns Apply proposal must reproduce exact subject-plus-Coding contributor values and occurrence counts');
  assertExactRows(appliedAdd.previewRows.rows, expectedAddRows.map(row => row.map(renderCell)),
  'The fresh Add columns Apply proposal must display the exact contributor values and counts');
  const applyColumns = page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Apply columns', exact: true });
  await action('Apply exact Observation.id contributor column', applyColumns,
    () => applyColumns.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    });
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assertGroupKey(savedGroup, { scalar: true });
  const savedIDColumn = getDocument(builder).columns.find(column => column.source?.field?.path === 'id');
  assert(savedIDColumn, 'Apply must save the exact Observation.id source column.');
  assert.equal(savedIDColumn.occurrenceId, 'base');
  assert.equal(savedIDColumn.source.field.projectionMode, 'VALUE');
  const savedRowValue = savedGroup.rowValues?.find(value => value.inputColumnId === savedIDColumn.columnId);
  assert(savedRowValue, 'The Group must bind Observation.id as a contributor row-value.');
  assert.equal(savedRowValue.policy, 'ALL');
  const savedRowValueOutput = savedGroup.outputs.find(output => output.id === savedRowValue.outputColumnId);
  assert(savedRowValueOutput?.name && savedRowValueOutput?.label);
  const appliedContributorHeaders = savedGroup.outputs.map(output => output.label);
  const appliedContributorRows = oracle.expectedSubjectGroups.map(group => savedGroup.outputs.map(output => {
    if (output.id === codingGroupKey(savedGroup, { sourceKind: 'BINDING' }).outputColumnId) return clean(group.display);
    if (output.id === scalarGroupKey(savedGroup, { sourceKind: 'BINDING', canonicalPath: subjectCandidate.fieldPath }).outputColumnId) return clean(group.subjectReference);
    if (output.id === savedGroup.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS')?.outputColumnId) return String(group.count);
    if (output.id === savedRowValue.outputColumnId) return group.ownerIDs.join('; ');
    assert.fail(`Applied Group output ${output.id} is not mapped to the exact raw Coding oracle.`);
  }));
  await waitGridMatch(page, 'saved', appliedContributorHeaders, appliedContributorRows);
  const savedContributorGrid = await readPreviewGrid(page, 'saved');
  assertExactRows(savedContributorGrid.rows, appliedContributorRows,
    'Applied contributor values must match each subject-plus-Coding group');
  cda.check('persistence',
    'Apply persists the owner-bound Observation.id Group row-value with ALL policy and exact grouped output',
    true, {
      sourcePath: savedIDColumn.source?.field?.path,
      occurrenceId: savedIDColumn.occurrenceId,
      rowValuePolicy: savedRowValue.policy,
      outputCount: savedGroup.outputs.length,
      groupRowCount: savedContributorGrid.rows.length,
      contributorOwnerValueCount: oracle.expectedSubjectGroups.reduce((sum, group) => sum + group.ownerIDs.length, 0),
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      oracleDigest: oracleSummary.digest,
    });

  await reloadToTable({ headers: appliedContributorHeaders, rows: appliedContributorRows });
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assertGroupKey(savedGroup, { scalar: true });
  const reloadedIDColumn = getDocument(builder).columns.find(column => column.columnId === savedIDColumn.columnId);
  assert(reloadedIDColumn);
  assert.equal(reloadedIDColumn.occurrenceId, savedIDColumn.occurrenceId);
  assert.deepEqual(reloadedIDColumn.source, savedIDColumn.source);
  assert.deepEqual(savedGroup.rowValues?.find(value => value.inputColumnId === savedIDColumn.columnId), savedRowValue);
  await assertSavedGrid(savedGroup, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'reloaded Coding Group with owner contributor values',
  });
  const contributorCells = await readPreviewGrid(page, 'saved');
  assertExactRows(contributorCells.rows, appliedContributorRows,
    'Reload must retain exact Add columns owner values and their count multiplicity');
  cda.check('persistence',
    'reload preserves exact Observation.id contributor values and Group occurrence counts', true, {
      sourcePath: reloadedIDColumn.source?.field?.path,
      occurrenceId: reloadedIDColumn.occurrenceId,
      rowValuePolicy: savedGroup.rowValues?.find(value => value.inputColumnId === savedIDColumn.columnId)?.policy,
      headers: contributorCells.headers,
      groupRowCount: contributorCells.rows.length,
      contributorOwnerValueCount: oracle.expectedSubjectGroups.reduce((sum, group) => sum + group.ownerIDs.length, 0),
      oracleDigest: oracleSummary.digest,
    });

  const removalHistory = page.getByTestId('construction-row-operation-history');
  if (!await removalHistory.isVisible().catch(() => false)) {
    await clickRowSettings(() => removalHistory.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
  }
  const removeButton = page.getByTestId(`construction-row-remove-${savedGroup.id}`);
  await action('preview removal of the expanded Coding Group', removeButton,
    () => removeButton.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      await waitProposalPreview();
    });
  const removalPreview = await readPreviewGrid(page, 'proposal');
  assert(removalPreview.ready);
  assert.equal(removalPreview.rows.length, oracle.expectedExpandedRowCount,
    'Group removal must restore one output row per exact Coding occurrence.');
  const rawIDHeaderIndex = removalPreview.headers.findIndex(header => header === savedIDColumn.label);
  assert(rawIDHeaderIndex >= 0, 'Group removal Preview must expose the exact Observation.id source scalar.');
  const expectedRawIDs = oracle.expectedRawExpandedRows;
  assertExactRows(removalPreview.rows.map(row => [row[rawIDHeaderIndex]]), expectedRawIDs,
    'Group removal Preview must restore exact Observation owners for every expanded Coding occurrence, including multiplicity');
  cda.check('correctness',
    'Group removal Preview restores exact valueCodeableConcept Coding owner rows and duplicate-sensitive cardinality',
    true, {
      headers: removalPreview.headers,
      restoredRowCount: removalPreview.rows.length,
      uniqueOwnerCount: oracle.sourceIDs.length,
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      oracleDigest: oracleSummary.digest,
    });

  const beforeRemovalCancel = await readBuilder();
  const cancelRemoval = page.getByTestId('construction-cancel-proposal');
  await action('Cancel expanded Coding Group removal', cancelRemoval,
    () => cancelRemoval.click({ timeout: ACTION_BUDGET_MS }),
    () => page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS }));
  const afterRemovalCancel = await readBuilder();
  assert.deepEqual(afterRemovalCancel.workspace, beforeRemovalCancel.workspace);
  assert.equal(afterRemovalCancel.draftVersion, beforeRemovalCancel.draftVersion);
  assert.equal(afterRemovalCancel.draftDigest, beforeRemovalCancel.draftDigest);
  assertGroupKey(getGroupStep(afterRemovalCancel), { scalar: false });
  cda.check('persistence', 'Cancel preserves the expanded Coding Group and exact contributor values', true, {
    draftVersion: afterRemovalCancel.draftVersion, draftDigest: afterRemovalCancel.draftDigest,
  });

  const reopenHistory = page.getByTestId('construction-row-operation-history');
  if (!await reopenHistory.isVisible().catch(() => false)) {
    await clickRowSettings(() => reopenHistory.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
  }
  const reopenedRemove = page.getByTestId(`construction-row-remove-${savedGroup.id}`);
  await action('reopen exact expanded Coding Group removal', reopenedRemove,
    () => reopenedRemove.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      await waitProposalPreview();
    });
  await action('Apply removal and restore the exact expanded Coding source rows', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      const sourceHeaders = [savedIDColumn.label];
      await waitGridMatch(page, 'saved', sourceHeaders,
        oracle.expectedRawExpandedRows.map(row => [row[0]]));
    });
  builder = await readBuilder();
  const restoredDocument = getDocument(builder);
  assert.deepEqual(restoredDocument.construction?.steps ?? [], []);
  assert.equal(restoredDocument.rows.kind, 'EXPANDED');
  assert.equal(restoredDocument.rows.expanded.scopePath, VALUE_CODEABLE_CONCEPT_CODING_PATH);
  assert.deepEqual(restoredDocument.columns.map(column => ({
    path: column.source?.field?.path,
    occurrenceId: column.occurrenceId,
    label: column.label,
  })), [{ path: 'id', occurrenceId: 'base', label: savedIDColumn.label }]);
  const restoredGrid = await readPreviewGrid(page, 'saved');
  assert.deepEqual(restoredGrid.headers, [savedIDColumn.label]);
  assertExactRows(restoredGrid.rows, oracle.expectedRawExpandedRows,
    'Group removal must restore the exact owner value for every Coding occurrence');
  cda.check('persistence',
    'removing Group restores exact expanded Coding owner rows and source scalar values after Apply',
    true, {
      rowShape: restoredDocument.rows,
      sourceColumnCount: restoredDocument.columns.length,
      restoredRowCount: restoredGrid.rows.length,
      uniqueOwnerCount: oracle.sourceIDs.length,
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      oracleDigest: oracleSummary.digest,
    });

  await reloadToTable({ headers: [savedIDColumn.label], rows: oracle.expectedRawExpandedRows });
  builder = await readBuilder();
  const finalDocument = getDocument(builder);
  assert.deepEqual(finalDocument.construction?.steps ?? [], []);
  assert.equal(finalDocument.rows.expanded.scopePath, VALUE_CODEABLE_CONCEPT_CODING_PATH);
  const finalGrid = await readPreviewGrid(page, 'saved');
  assertExactRows(finalGrid.rows, oracle.expectedRawExpandedRows,
    'Final reload must retain the exact owner value for every Coding occurrence');
  cda.check('persistence',
    'final reload retains exact Observation.id values for every Value Codeable Concept Coding row', true, {
      restoredRowCount: finalGrid.rows.length,
      uniqueOwnerCount: oracle.sourceIDs.length,
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      oracleDigest: oracleSummary.digest,
    });
  assert(actionLabels.length > 0);
  assert(actionLabels.every(item => item.durationMs <= ACTION_BUDGET_MS));
  cda.check('performance', 'all native expanded Coding Group actions and reloads stay within five seconds', true, {
    actionBudgetMs: ACTION_BUDGET_MS,
    actions: actionLabels,
    maximumActionMs: Math.max(...actionLabels.map(item => item.durationMs)),
  });
  cda.check('correctness',
    'native expanded Coding lifecycle preserves exact owner route and never selects Observation.code.coding[]',
    true, {
      sourcePath: VALUE_CODEABLE_CONCEPT_CODING_PATH,
      occurrenceId: 'base',
      directOwnerResourceType: 'Observation',
      selectedRootCount: oracleSummary.selectedRootCount,
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      codingIdentityCount: oracleSummary.codingIdentityCount,
      subjectCodingGroupCount: oracleSummary.subjectCodingGroupCount,
      oracleDigest: oracleSummary.digest,
      unrelatedDirectCodePathSelected: false,
    });
}
