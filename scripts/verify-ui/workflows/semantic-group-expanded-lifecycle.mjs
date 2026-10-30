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
  stableDigest,
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
  codingSourceKind,
  scalarSourceKind,
  codingChoiceId,
  scalarChoiceId,
  subjectCandidate,
} = {}) {
  assert.equal(step?.operation?.kind, 'GROUP', 'Expanded Coding identity must use ordinary GROUP.');
  const keys = step.operation.group?.keys ?? [];
  assert.equal(keys.some(key => key.semanticCategory !== undefined), false,
    'The expanded current Coding item is a field source, not a root-wide semantic-inventory category.');
  const resolvedCodingSourceKind = codingSourceKind ?? sourceKind ??
    (keys.some(key => key.fieldSource?.kind === 'BINDING' && key.fieldSource.valueKind === 'CODING_IDENTITY')
      ? 'BINDING' : 'CHOICE');
  const codingKey = codingGroupKey(step, { sourceKind: resolvedCodingSourceKind, choiceId: codingChoiceId });
  assert(codingKey, `GROUP ${resolvedCodingSourceKind} must bind the exact current Coding source choice.`);
  const source = codingKey.fieldSource;
  if (resolvedCodingSourceKind === 'CHOICE') {
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
  const resolvedScalarSourceKind = scalarSourceKind ?? sourceKind ??
    (keys.some(key => key.fieldSource?.kind === 'BINDING' && key.fieldSource.valueKind === 'SCALAR' &&
      key.fieldSource.canonicalPath === (subjectCandidate?.fieldPath ?? 'subject.reference'))
      ? 'BINDING' : 'CHOICE');
  const scalarKey = scalarGroupKey(step, { sourceKind: resolvedScalarSourceKind, choiceId: scalarChoiceId,
    canonicalPath: subjectCandidate?.fieldPath ?? 'subject.reference' });
  assert.equal(keys.length, scalar ? 2 : 1,
    `Expected ${scalar ? 'the exact scalar plus Coding identity' : 'Coding identity alone'} in the Group grain.`);
  if (scalar) {
    assert(scalarKey, `GROUP ${resolvedScalarSourceKind} must include the exact scalar field-source choice.`);
    const scalarSource = scalarKey.fieldSource;
    if (resolvedScalarSourceKind === 'CHOICE') {
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
  const { scalar, sourceKind, codingSourceKind, scalarSourceKind, codingChoiceId, scalarChoiceId, subjectCandidate } = context;
  const { codingKey, scalarKey, aggregate, keyOutput, aggregateOutput } = assertCodingGroupKey(step, undefined, {
    scalar, sourceKind, codingSourceKind, scalarSourceKind, codingChoiceId, scalarChoiceId, subjectCandidate,
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
    return keys.length === (scalar ? 2 : 1) && keys.every(key => {
      if (key.fieldSource?.kind === 'CHOICE') return typeof key.fieldSource.choiceId === 'string' && key.fieldSource.choiceId.trim();
      if (key.fieldSource?.kind === 'BINDING') return Boolean(key.fieldSource.valueKind && key.fieldSource.canonicalPath);
      return false;
    });
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
    const { codingKey, scalarKey } = assertCodingGroupKey(step, rowChoice, {
      scalar, codingChoiceId, scalarChoiceId, subjectCandidate,
    });
    assert.equal(keys.every(key => ['CHOICE', 'BINDING'].includes(key.fieldSource?.kind)), true);
    return { step, codingKey, scalarKey };
  };
  const gridFor = (step, records, { scalar, sourceKind, codingSourceKind, scalarSourceKind }) => expectedGroupGrid(step, records, {
    codingSourceKind, scalarSourceKind,
    scalar, sourceKind, codingChoiceId, scalarChoiceId, subjectCandidate,
  });
  const assertProposalGrid = async (step, records, { scalar, phase }) => {
    const expected = gridFor(step, records, { scalar });
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

  let ownerRowValueChoice;
  const openGlobalGroupContributorColumns = async () => {
    const section = page.getByRole('region', {
      name: 'Add columns from Group contributors', exact: true,
    });
    if (await section.isVisible().catch(() => false)) {
      assert(ownerRowValueChoice,
        'The active Group contributor choices must come from the selected terminal Group capabilities.');
      return { section, choice: ownerRowValueChoice };
    }

    const editor = page.getByTestId('construction-operation-editor');
    const editorAlreadyOpen = await editor.isVisible().catch(() => false) &&
      await editor.getAttribute('data-operation-family') === 'ADD_COLUMNS';
    if (editorAlreadyOpen && ownerRowValueChoice) {
      await section.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      return { section, choice: ownerRowValueChoice };
    }

    if (!editorAlreadyOpen) {
      const addColumns = page.getByTestId('construction-action-add-columns');
      const capabilitiesResponsePromise = page.waitForResponse(response => {
        try {
          const body = response.request().postDataJSON();
          return response.request().method() === 'POST' &&
            new URL(response.url()).pathname.endsWith('/construction-capabilities') &&
            body.outputId === outputId && body.groupStepId === savedGroup.id &&
            body.stageId === 'source_projection';
        } catch { return false; }
      }, { timeout: ACTION_BUDGET_MS });
      await action('open global toolbar Add columns for active Group contributors', addColumns,
        () => addColumns.click({ timeout: ACTION_BUDGET_MS }), async () => {
        await editor.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
        assert.equal(await editor.getAttribute('data-operation-family'), 'ADD_COLUMNS',
          'The global toolbar must open Add columns while keeping the saved Group active.');
        await section.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
        await capabilitiesResponsePromise;
      });

      const capabilitiesResponse = await capabilitiesResponsePromise;
      assert(capabilitiesResponse.ok(),
        `Active Group contributor capabilities returned HTTP ${capabilitiesResponse.status()}.`);
      const capabilities = await capabilitiesResponse.json();
      assert.equal(capabilities.outputId, outputId);
      assert.equal(capabilities.stageId, 'source_projection');
      assert.equal(capabilities.selectedStage?.id, 'source_projection');
      const capabilityRequest = capabilitiesResponse.request().postDataJSON();
      assert.equal(capabilityRequest.stageId, 'source_projection',
        'Contributor choices must resolve against the active Group input stage.');
      assert.equal(capabilityRequest.groupStepId, savedGroup.id,
        'The contributor-only list must be scoped to the exact terminal Group step.');
      assert.equal(getDocument(await readBuilder()).columns.length, 0,
        'Observation.id must still be unprojected when opening Add columns for the active Group.');

      const matches = (capabilities.groupRowValueChoices ?? []).filter(choice =>
        choice.kind === 'FIELD_SOURCE' && choice.valueKind === 'SCALAR' &&
        choice.candidateId === idCandidate.candidateId && choice.canonicalPath === 'id' &&
        ['ROOT', 'OWNER'].includes(choice.sourceRole) &&
        choice.resourceType === 'Observation' && choice.ownerResourceType === 'Observation');
      assert.equal(matches.length, 1,
        'The terminal Group must expose exactly the unprojected Observation.id ROOT/OWNER contributor choice.');
      const choice = matches[0];
      assert.equal(choice.fieldSource?.kind, 'CHOICE');
      assert.equal(typeof choice.fieldSource.choiceId, 'string');
      assert(choice.fieldSource.choiceId.trim());
      assert.equal(choice.sourceStageId, 'source_projection');
      assert.equal(typeof choice.sourceShapeId, 'string');
      assert(choice.sourceShapeId.trim());
      assert.equal(choice.occurrenceId, 'base');
      assert.equal(choice.ownerOccurrenceId, 'base');
      assert.equal(choice.ownerResourceType, 'Observation');
      assert.equal(choice.candidateId, idCandidate.candidateId);
      assert.equal(choice.canonicalPath, idCandidate.fieldPath);
      assert.equal(choice.inputColumnId, undefined,
        'A previously unprojected Group RowValue choice must not carry an inputColumnId.');
      assert.equal(choice.boundarySelector, undefined,
        'Direct Observation owner values must not claim a repeated component boundary.');

      const contributorSection = section.getByTestId('construction-group-add-columns');
      await contributorSection.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      const choiceControl = contributorSection.getByTestId(
        `construction-group-add-column-choice-${choice.fieldSource.choiceId}`,
      );
      await choiceControl.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      assert.equal(await choiceControl.getAttribute('aria-label'), `Add column ${choice.label}`);
      assert.equal(await page.getByRole('tab', { name: 'Coded values', exact: true }).isVisible(), true,
        'The ordinary coded catalog remains available beside active Group contributors.');
      assert.equal(await page.getByRole('tab', { name: 'Other fields', exact: true }).isVisible(), true,
        'The ordinary field catalog remains available beside active Group contributors.');
      ownerRowValueChoice = choice;
      return { section, choice };
    }

    await section.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    assert(ownerRowValueChoice,
      'Active Group contributor choices must be capability-issued before their UI selection.');
    return { section, choice: ownerRowValueChoice };
  };
  const proposeOwnerIDRowValue = async () => {
    const { section, choice } = await openGlobalGroupContributorColumns();
    const checkbox = section.getByTestId(
      `construction-group-add-column-choice-${choice.fieldSource.choiceId}`,
    );
    await checkbox.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    assert.equal(await checkbox.isEnabled(), true);
    assert.equal(await checkbox.isChecked(), false,
      'A cancelled Group contributor preview must not retain the uncommitted Observation.id selection.');
    const requestPromise = page.waitForRequest(request => {
      try {
        if (request.method() !== 'POST' || !new URL(request.url()).pathname.endsWith('/construction-proposals')) return false;
        const body = request.postDataJSON();
        const step = body.candidateConstruction?.steps?.find(candidate => candidate.id === savedGroup.id);
        return body.outputId === outputId && step?.operation?.kind === 'GROUP' &&
          (step.rowValues ?? []).some(value => value.fieldSource?.kind === 'CHOICE' &&
            value.fieldSource.choiceId === choice.fieldSource.choiceId);
      } catch { return false; }
    }, { timeout: ACTION_BUDGET_MS });
    const priorReceipt = await proposalReceipt();
    await action('select unprojected Observation.id from global Add columns Group contributors', checkbox,
      () => checkbox.check({ timeout: ACTION_BUDGET_MS }), async () => waitNextProposal(priorReceipt));
    const request = await requestPromise;
    const { step } = assertCandidate(request, { scalar: true });
    assert.equal(step.id, savedGroup.id,
      'Global Add columns must update the same saved terminal Group step.');
    assert.equal(step.rowValues?.length, 1,
      'Global Add columns must add one compiler-issued source RowValue to the active Group.');
    const rowValue = step.rowValues[0];
    assert.deepEqual(rowValue.fieldSource, { kind: 'CHOICE', choiceId: choice.fieldSource.choiceId });
    assert.equal(Object.hasOwn(rowValue, 'inputColumnId'), false,
      'The Group RowValue request must use fieldSource CHOICE without inputColumnId.');
    assert.equal(rowValue.policy, 'ALL',
      'The owner contributor must retain every distinct Observation.id across matching source occurrences.');
    assert(step.outputs?.some(output => output.id === rowValue.outputColumnId),
      'The source RowValue must have a corresponding Group output.');
    const preview = await assertProposalGrid(step, oracle.expectedSubjectGroups, {
      scalar: true, phase: 'Observation.id Group RowValue Preview',
    });
    return { choice, request, step, rowValue, preview };
  };

  const beforeRowValueCancel = await readBuilder();
  await proposeOwnerIDRowValue();
  await action('Cancel unprojected Observation.id Group RowValue Preview', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click({ timeout: ACTION_BUDGET_MS }),
    () => page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS }));
  const afterRowValueCancel = await readBuilder();
  assert.deepEqual(afterRowValueCancel.workspace, beforeRowValueCancel.workspace);
  assert.equal(afterRowValueCancel.draftVersion, beforeRowValueCancel.draftVersion);
  assert.equal(afterRowValueCancel.draftDigest, beforeRowValueCancel.draftDigest);
  assert.equal(getGroupStep(afterRowValueCancel).id, firstStepID,
    'Cancel must preserve the exact Group step while discarding the uncommitted contributor choice.');
  assert.equal(getGroupStep(afterRowValueCancel).rowValues?.length ?? 0, 0,
    'Cancel must leave the applied Group without a contributor RowValue.');
  await assertSavedGrid(getGroupStep(afterRowValueCancel), oracle.expectedSubjectGroups, {
    scalar: true, phase: 'Group after cancelling Observation.id RowValue',
  });
  cda.check('persistence',
    'Cancel leaves the applied Group and exact owner-route preimage unchanged before RowValue Apply',
    true, { draftVersion: afterRowValueCancel.draftVersion, draftDigest: afterRowValueCancel.draftDigest });

  const ownerRowValueApply = await proposeOwnerIDRowValue();
  const applyOwnerRowValue = page.getByTestId('construction-apply-proposal');
  await action('Apply the exact unprojected Observation.id Group RowValue before public columns', applyOwnerRowValue,
    () => applyOwnerRowValue.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitGridMatch(page, 'saved', ownerRowValueApply.preview.expected.headers, ownerRowValueApply.preview.expected.rows);
    });
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assert.equal(savedGroup.id, firstStepID,
    'Applying global Add columns must preserve the exact active Group step identity.');
  assertGroupKey(savedGroup, { scalar: true });
  assert.deepEqual(codingGroupKey(savedGroup, { sourceKind: 'BINDING' }).fieldSource, codingSourceBinding,
    'The contributor update must retain the exact current Coding identity source binding.');
  assert.deepEqual(scalarGroupKey(savedGroup, {
    sourceKind: 'BINDING', canonicalPath: subjectCandidate.fieldPath,
  }).fieldSource, scalarSourceBinding,
  'The contributor update must retain the exact scalar Group key binding.');
  assert.equal(getDocument(builder).columns.length, 0,
    'The Observation.id contributor RowValue must be added before selecting any public column.');
  assert.equal(savedGroup.rowValues?.length, 1);
  const savedOwnerRowValue = savedGroup.rowValues[0];
  assert.equal(Object.hasOwn(savedOwnerRowValue, 'inputColumnId'), false);
  assert.equal(savedOwnerRowValue.policy, 'ALL');
  assert.equal(savedOwnerRowValue.outputColumnId, ownerRowValueApply.rowValue.outputColumnId);
  const ownerRowValueBinding = savedOwnerRowValue.fieldSource;
  const expectedOwnerRowValueBinding = {
    kind: 'BINDING',
    valueKind: 'SCALAR',
    sourceStageId: ownerRowValueApply.choice.sourceStageId,
    sourceShapeId: ownerRowValueApply.choice.sourceShapeId,
    sourceRole: ownerRowValueApply.choice.sourceRole,
    occurrenceId: ownerRowValueApply.choice.occurrenceId,
    resourceType: ownerRowValueApply.choice.resourceType,
    ownerResourceType: ownerRowValueApply.choice.ownerResourceType,
    candidateId: ownerRowValueApply.choice.candidateId,
    canonicalPath: ownerRowValueApply.choice.canonicalPath,
  };
  if (Object.hasOwn(ownerRowValueBinding, 'ownerOccurrenceId')) {
    expectedOwnerRowValueBinding.ownerOccurrenceId = ownerRowValueApply.choice.ownerOccurrenceId;
  }
  assert.deepEqual(Object.keys(ownerRowValueBinding).sort(), Object.keys(expectedOwnerRowValueBinding).sort(),
    'The direct owner RowValue BINDING must contain only the exact resolved capability fields.');
  assert.deepEqual(ownerRowValueBinding, expectedOwnerRowValueBinding,
    'Apply must resolve the Group RowValue CHOICE to the exact Observation owner BINDING.');
  const ownerRowValueGrid = await assertSavedGrid(savedGroup, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'applied Observation.id Group RowValue',
  });
  assertExactRows(ownerRowValueGrid.actual.rows, ownerRowValueApply.preview.actual.rows,
    'Applying the source RowValue must preserve exact owner IDs and duplicate-sensitive Group counts.');
  cda.check('persistence',
    'Global Add columns resolves the unprojected Observation.id contributor CHOICE to its exact ROOT/OWNER Group BINDING',
    true, {
      rowValuePolicy: savedOwnerRowValue.policy,
      sourceRole: ownerRowValueBinding.sourceRole,
      resourceType: ownerRowValueBinding.resourceType,
      ownerResourceType: ownerRowValueBinding.ownerResourceType,
      canonicalPath: ownerRowValueBinding.canonicalPath,
      contributorOwnerValueCount: oracle.expectedSubjectGroups.reduce((sum, group) => sum + group.ownerIDs.length, 0),
      groupRowCount: ownerRowValueGrid.actual.rows.length,
      oracleDigest: oracleSummary.digest,
    });

  await reloadToTable(ownerRowValueGrid.expected);
  builder = await readBuilder();
  savedGroup = getGroupStep(builder);
  assertGroupKey(savedGroup, { scalar: true });
  assert.deepEqual(savedGroup.rowValues?.[0], savedOwnerRowValue,
    'Reload must retain the exact capability-bound Observation.id RowValue and ALL policy.');
  const reloadedOwnerRowValueGrid = await assertSavedGrid(savedGroup, oracle.expectedSubjectGroups, {
    scalar: true, phase: 'reloaded Observation.id Group RowValue',
  });
  assert.equal(getDocument(builder).columns.length, 0,
    'Reload must keep the RowValue field-source separate from public source columns.');
  cda.check('persistence',
    'Reload retains the exact Observation owner BINDING and all contributor values',
    true, {
      rowValuePolicy: savedGroup.rowValues[0].policy,
      contributorOwnerValueCount: oracle.expectedSubjectGroups.reduce((sum, group) => sum + group.ownerIDs.length, 0),
      groupRowCount: reloadedOwnerRowValueGrid.actual.rows.length,
      oracleDigest: oracleSummary.digest,
    });

  assert.equal(savedGroup?.id, firstStepID);
  const reloadedBeforeEdit = reloadedOwnerRowValueGrid;
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
    await action('open Add columns after Group removal to restore the source-row value', add,
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
    assert.equal(await field.isEnabled(), true, 'Observation.id must be selectable as the exact restored source scalar.');
    await action('select exact Observation.id source column', field,
      () => field.check({ timeout: ACTION_BUDGET_MS }), async () => {
        await page.getByRole('button', { name: /Add 1 selected feature/ }).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      });
    const addSelected = page.getByRole('button', { name: /Add 1 selected feature/ });
    const responsePromise = page.waitForResponse(response => {
      try {
        return response.request().method() === 'POST' &&
          new URL(response.url()).pathname.endsWith('/construction-choice-proposals');
      } catch { return false; }
    }, { timeout: ACTION_BUDGET_MS });
    await action('preview Observation.id for the restored expanded source rows', addSelected,
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
    const preview = responseBody.preview;
    assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows),
      'Add columns response must include exact expanded source-row values.');
    const previewRows = await readPreviewGrid(page, 'proposal');
    return { responseBody, requestBody, preview, previewRows };
  };

  const removalHistory = page.getByTestId('construction-row-operation-history');
  if (!await removalHistory.isVisible().catch(() => false)) {
    await clickRowSettings(() => removalHistory.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }));
  }
  const removeButton = page.getByTestId(`construction-row-remove-${savedGroup.id}`);
  const removalRequestPromise = page.waitForRequest(request => {
    try {
      if (request.method() !== 'POST' || !new URL(request.url()).pathname.endsWith('/construction-proposals')) return false;
      const body = request.postDataJSON();
      return body.outputId === outputId &&
        !(body.candidateConstruction?.steps ?? []).some(step => step.operation?.kind === 'GROUP');
    } catch { return false; }
  }, { timeout: ACTION_BUDGET_MS });
  await action('preview removal of the expanded Coding Group', removeButton,
    () => removeButton.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      await waitProposalPreview();
    });
  const removalRequest = await removalRequestPromise;
  assert.deepEqual(removalRequest.postDataJSON().candidateConstruction.steps, [],
    'Group removal must restore the original expanded source construction stage.');
  const removalPreview = await readPreviewGrid(page, 'proposal');
  assert(removalPreview.ready);
  cda.check('correctness',
    'Group removal Preview removes the exact Group and RowValue while preserving the expanded source definition',
    true, {
      restoredExpandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      uniqueOwnerCount: oracle.sourceIDs.length,
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
  assertGroupKey(getGroupStep(afterRemovalCancel), { scalar: true });
  assert.deepEqual(getGroupStep(afterRemovalCancel).rowValues?.[0], savedOwnerRowValue,
    'Cancel must preserve the exact Observation.id RowValue BINDING.');
  await assertSavedGrid(getGroupStep(afterRemovalCancel), oracle.expectedSubjectGroups, {
    scalar: true, phase: 'Group after cancelling removal',
  });
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
      await page.locator('[data-testid="preview-table-scroll"] [role="table"]')
        .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    });
  builder = await readBuilder();
  const restoredDocument = getDocument(builder);
  assert.deepEqual(restoredDocument.construction?.steps ?? [], []);
  assert.equal(restoredDocument.rows.kind, 'EXPANDED');
  assert.equal(restoredDocument.rows.expanded.scopePath, VALUE_CODEABLE_CONCEPT_CODING_PATH);
  assert.equal(restoredDocument.columns.length, 0,
    'Removing Group must restore the exact Coding row shape before any public source column is added.');
  cda.check('persistence',
    'removing Group restores the exact expanded Coding source shape before public columns',
    true, {
      rowShape: restoredDocument.rows,
      sourceColumnCount: restoredDocument.columns.length,
      uniqueOwnerCount: oracle.sourceIDs.length,
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      oracleDigest: oracleSummary.digest,
    });

  const restoredSource = await chooseObservationID();
  assert.equal(restoredSource.requestBody.outputId, outputId);
  assert.equal(restoredSource.preview.columns.length, 1,
    'The post-Group Add columns proposal must restore exactly one Observation.id source column.');
  const restoredIDPreviewColumn = restoredSource.preview.columns[0];
  const restoredIDValues = restoredSource.preview.rows.map(row => [row[restoredIDPreviewColumn.column]]);
  assertExactRows(restoredIDValues, oracle.expectedRawExpandedRows,
    'Post-Group Add columns must restore Observation.id once for every exact Coding source occurrence.');
  assertExactRows(restoredSource.previewRows.rows, oracle.expectedRawExpandedRows.map(row => row.map(renderCell)),
    'The visible Add columns proposal must display exact restored Observation owner values and multiplicity.');
  const applyRestoredSource = page.getByTestId('construction-choice-proposal-panel')
    .getByRole('button', { name: 'Apply columns', exact: true });
  await action('Apply Observation.id after restoring the expanded source rows', applyRestoredSource,
    () => applyRestoredSource.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    });
  builder = await readBuilder();
  const restoredIDColumn = getDocument(builder).columns.find(column => column.source?.field?.path === 'id');
  assert(restoredIDColumn);
  assert.equal(restoredIDColumn.occurrenceId, 'base');
  assert.equal(restoredIDColumn.source.field.projectionMode, 'VALUE');
  assert.deepEqual(getDocument(builder).construction?.steps ?? [], []);
  const restoredGrid = await readPreviewGrid(page, 'saved');
  assert.deepEqual(restoredGrid.headers, [restoredIDColumn.label]);
  assertExactRows(restoredGrid.rows, oracle.expectedRawExpandedRows,
    'Post-Group source projection must preserve the exact owner value for every Coding occurrence.');
  cda.check('correctness',
    'Add columns after Group removal restores exact Observation.id values and duplicate-sensitive occurrence counts',
    true, {
      sourcePath: restoredIDColumn.source?.field?.path,
      occurrenceId: restoredIDColumn.occurrenceId,
      restoredRowCount: restoredGrid.rows.length,
      expandedOccurrenceCount: oracleSummary.expandedOccurrenceCount,
      oracleDigest: oracleSummary.digest,
    });

  await reloadToTable({ headers: [restoredIDColumn.label], rows: oracle.expectedRawExpandedRows });
  builder = await readBuilder();
  const finalDocument = getDocument(builder);
  assert.deepEqual(finalDocument.construction?.steps ?? [], []);
  assert.equal(finalDocument.rows.expanded.scopePath, VALUE_CODEABLE_CONCEPT_CODING_PATH);
  assert.equal(finalDocument.columns.length, 1);
  assert.equal(finalDocument.columns[0].source?.field?.path, 'id');
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
