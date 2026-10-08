import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import {
  buildArangoShellInvocation,
  buildBoundedArangoQueryScript,
  summarizeArangoShellResult,
} from '../helpers/owned-arangosh-command.mjs';

export const rootRebaseOracleBounds = Object.freeze({
  observationSampleLimit: 10_000,
  candidatePatientLimit: 25,
  observationSentinelLimit: 26,
  maxRuntimeSeconds: 8,
  memoryLimitBytes: 256 * 1024 * 1024,
  hostTimeoutMs: 30_000,
  visiblePreviewLimit: 25,
});

export function selectRootRebaseWitness(candidates, rereadCandidate, expectedScope) {
  if (!Array.isArray(candidates)) throw new TypeError('Root-rebase candidates must be an array.');
  if (typeof rereadCandidate !== 'function') throw new TypeError('Root-rebase candidate rereader must be a function.');
  if (!expectedScope || typeof expectedScope.project !== 'string' || typeof expectedScope.generation !== 'string') {
    throw new TypeError('Root-rebase candidate validation requires an expected project and generation.');
  }

  const { candidatePatientLimit } = rootRebaseOracleBounds;
  const boundedCandidates = candidates.slice(0, candidatePatientLimit);
  const attempts = [];
  let witness = null;

  for (const candidate of boundedCandidates) {
    const patientId = candidate?.patientId;
    const sampledObservationCount = candidate?.sampledObservationCount;
    if (typeof patientId !== 'string' || !patientId) {
      attempts.push({ patientId: null, sampledObservationCount: sampledObservationCount ?? null, outcome: 'invalid-sample-candidate' });
      continue;
    }
    if (!Number.isInteger(sampledObservationCount) || sampledObservationCount < 2 || sampledObservationCount > 25) {
      attempts.push({ patientId, sampledObservationCount: sampledObservationCount ?? null, outcome: 'sample-count-out-of-range' });
      continue;
    }

    const exactRows = rereadCandidate(patientId);
    const exact = Array.isArray(exactRows) && exactRows.length === 1 ? exactRows[0] : null;
    const observations = Array.isArray(exact?.observations) ? exact.observations : [];
    const observationIDs = observations.map(observation => observation?.id);
    const duplicateObservationIDs = new Set(observationIDs).size !== observationIDs.length;
    let outcome = 'accepted';

    if (!Array.isArray(exactRows) || exactRows.length !== 1) outcome = 'patient-row-count';
    else if (exact.patientId !== patientId) outcome = 'patient-identity-mismatch';
    else if (exact.project !== expectedScope.project || exact.generation !== expectedScope.generation) outcome = 'patient-scope-mismatch';
    else if (!Array.isArray(exact.observations)) outcome = 'observation-rows-missing';
    else if (observations.some(observation => observation?.project !== expectedScope.project
      || observation?.generation !== expectedScope.generation)) outcome = 'observation-scope-mismatch';
    else if (observationIDs.length === rootRebaseOracleBounds.observationSentinelLimit
      || observationIDs.length > rootRebaseOracleBounds.observationSentinelLimit) outcome = 'observation-sentinel-exceeded';
    else if (observationIDs.length < 2) outcome = 'too-few-observations';
    else if (observationIDs.some(id => typeof id !== 'string' || !id) || duplicateObservationIDs) outcome = 'invalid-observation-identities';

    const attempt = {
      patientId,
      sampledObservationCount: candidate.sampledObservationCount ?? null,
      exactPatientRows: Array.isArray(exactRows) ? exactRows.length : null,
      exactObservationCount: observationIDs.length,
      outcome,
    };
    attempts.push(attempt);

    if (outcome === 'accepted' && typeof exact.patientKey === 'string'
      && exact.patientKey.startsWith('Patient/')) {
      witness = {
        patientId: exact.patientId,
        patientKey: exact.patientKey,
        project: exact.project,
        generation: exact.generation,
        observationIDs,
      };
      break;
    }
    if (outcome === 'accepted') attempt.outcome = 'invalid-patient-key';
  }

  return {
    witness,
    attempts,
    sampledCandidateCount: candidates.length,
    candidatePatientLimit,
    candidateLimitReached: !witness && boundedCandidates.length === candidatePatientLimit,
  };
}

export function buildRootRebaseOracleMetadata(witness, selection) {
  const {
    observationSampleLimit,
    candidatePatientLimit,
    observationSentinelLimit,
    maxRuntimeSeconds,
    memoryLimitBytes,
    hostTimeoutMs,
    visiblePreviewLimit,
  } = rootRebaseOracleBounds;
  return {
    patientId: witness.patientId,
    patientKey: witness.patientKey,
    observationIDs: witness.observationIDs,
    observationCount: witness.observationIDs.length,
    observationSampleLimit,
    candidatePatientLimit,
    candidateSelectionAttempts: selection.attempts,
    candidateLimitReached: selection.candidateLimitReached,
    sampleSelection: 'First 10,000 scoped Observation rows sorted by id; grouped Patient.subject counts only propose candidates.',
    candidateSort: 'Patient subject reference',
    perPatientObservationSentinelLimit: observationSentinelLimit,
    sentinelMeaning: 'A 26-row exact result is at least 26 matches and is excluded; only exact scoped rereads with 2–25 rows are accepted.',
    queryCaps: { maxRuntimeSeconds, memoryLimitBytes, hostTimeoutMs },
    exactSelectedPatientReread: true,
    expectedProject: witness.project,
    expectedGeneration: witness.generation,
    expectedPatientRows: [witness.patientId],
    expectedObservationRows: witness.observationIDs,
    expectedRestoredPatientRows: [witness.patientId],
    visiblePreviewLimit,
  };
}

export async function rootRebaseWorkflow({ page, cda }) {
  const project = cda.project;
  const explorerId = `cda-root-rebase-${Date.now()}`;
  const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
  const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
  const pageURL = `${uiOrigin}/?project=${project}&explorer=${explorerId}&mode=builder`;
  const authoringURL = `${apiOrigin}/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2`;
  const tableName = `CDA root rebase QA ${Date.now()}`;
  const state = cda.report;
  Object.assign(state, { project, explorer: explorerId, pageURL, tableName, actions: [], timingsMs: {}, rawOracleQueries: [], errors: state.errors ?? [], started: new Date().toISOString() });
  state.target = cda.target;
  const requiredChecks = cda.report.requiredChecks;
  assert.equal(requiredChecks.length, 8, 'The root-rebase report must match its eight registered lifecycle checks');
  const recordRequiredCheck = (index, dimension, passed, evidence) => {
    const name = requiredChecks[index];
    assert(name, `Root-rebase case is missing registered required check ${index}`);
    cda.check(dimension, name, passed, evidence);
  };
  const browserEvents = cda.captureRequests(`/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2`, { responsePaths: /commands|row-change|preview|construction-proposals|construction-choice-proposals/ });
  let outputId;

  const builder = async () => {
    const response = await fetch(`${authoringURL}/builder`, { signal: AbortSignal.timeout(30_000) });
    assert.equal(response.status, 200);
    return response.json();
  };
  const waitForRoot = async (targetId, root) => {
    const value = await builder();
    assert.equal(value.workspace.documents.find(document => document.output.id === targetId)?.rootResourceType, root);
    return value;
  };
  const waitForRowAssessment = async (after) => {
    const response = state.nativeRequests.slice(after).find(candidate => candidate.path.endsWith('/row-change') && candidate.response);
    const entry = response ?? await cda.waitForCapturedResponse(browserEvents,
      candidate => candidate.path.endsWith('/row-change'), 5000);
    return entry.response;
  };
  const actClick = async (selector, name, identity = { name }) => {
    await cda.click( selector, identity);
    state.actions.push(name);
  };
  const selectTable = async () => {
    const locator = page.locator('button[data-testid^="construction-table-"]');
    await cda.wait( ([title]) => [...document.querySelectorAll('button[data-testid^="construction-table-"]')]
      .some(button => button.innerText.trim().endsWith(title)), [tableName]);
    await actClick('button[data-testid^="construction-table-"]', 'Select temporary table', { includes: tableName });
  };
  const openRowControl = async () => {
    const details = page.locator('[data-testid="construction-source-setup"]');
    if (!await details.evaluate(element => element.open)) await actClick('[data-testid="construction-source-setup"] summary', 'Open source and column setup');
    await cda.wait( () => Boolean(document.querySelector('select[aria-label="One row per"]')));
    return cda.inspect( () => {
      const select = document.querySelector('select[aria-label="One row per"]');
      return { disabled: select.disabled, options: [...select.options].map(option => ({ value: option.value, label: option.textContent, disabled: option.disabled })) };
    });
  };
  const preview = async (stage, expectation) => {
    const started = Date.now();
    await actClick('button', `Preview ${stage}`, { name: 'Preview' });
    await waitForVisiblePreviewRows(expectation);
    const value = await cda.inspect( () => ({
      headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell => cell.innerText.trim()),
      rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
    }));
    state.timingsMs[stage] = Date.now() - started;
    assert(state.timingsMs[stage] < 5000, `${stage} preview took ${state.timingsMs[stage]} ms`);
    state[stage] = value;
    return value;
  };
  const waitForVisiblePreviewRows = async expectation => {
    await cda.wait(({ rowCount, exactRows, cells = [], headerIncludes = [], headerColumnValues = [], panelClosed = false }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"]');
      if (!table) return false;
      const headers = [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim().toUpperCase());
      const rows = [...table.querySelectorAll('[role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
      return rows.length === rowCount
        && (exactRows === undefined || JSON.stringify(rows) === JSON.stringify(exactRows))
        && cells.every(({ row, column, value }) => rows[row]?.[column] === value)
        && headerIncludes.every(value => headers.some(header => header.includes(value.toUpperCase())))
        && headerColumnValues.every(({ headerIncludes: label, values, sort = false }) => {
          const column = headers.findIndex(header => header.includes(label.toUpperCase()));
          if (column < 0) return false;
          const actual = rows.map(row => row[column]);
          return JSON.stringify(sort ? [...actual].sort() : actual)
            === JSON.stringify(sort ? [...values].sort() : values);
        })
        && (!panelClosed || !document.querySelector('[data-testid="row-change-preview-panel"]'));
    }, expectation, 5000);
  };
  const waitForRowChangePreviewRows = async ({ rowCount, patientId }) => {
    await cda.wait(({ rowCount: expectedCount, patientId: expectedPatientId }) => {
      const panel = document.querySelector('[data-testid="row-change-preview-panel"]');
      if (!panel) return false;
      const rows = [...panel.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
      const apply = [...panel.querySelectorAll('button')]
        .find(button => button.textContent?.includes('Apply row change') && !button.disabled);
      return Boolean(apply) && rows.length === expectedCount && rows.every(row => row.includes(expectedPatientId));
    }, { rowCount, patientId }, 5000);
    return cda.inspect(() => ({
      text: document.querySelector('[data-testid="row-change-preview-panel"]')?.innerText,
      rows: [...(document.querySelector('[data-testid="row-change-preview-panel"]')?.querySelectorAll('table tr') ?? [])]
        .map(row => [...row.querySelectorAll('th,td')].map(cell => cell.innerText.trim())),
    }));
  };
  const rawQuery = (query, phase) => {
    const container = cda.target.arangoContainer;
    assert(container, 'The owned CDA target must include its Arango source database container.');
    const identity = {
      phase,
      sha256: createHash('sha256').update(query).digest('hex'),
      maxRuntimeSeconds: rootRebaseOracleBounds.maxRuntimeSeconds,
      memoryLimitBytes: rootRebaseOracleBounds.memoryLimitBytes,
      hostTimeoutMs: rootRebaseOracleBounds.hostTimeoutMs,
      startedAt: Date.now(),
    };
    state.rawOracleQueries.push(identity);
    try {
      const script = buildBoundedArangoQueryScript({
        query,
        maxRuntimeSeconds: identity.maxRuntimeSeconds,
        memoryLimitBytes: identity.memoryLimitBytes,
      });
      const invocation = buildArangoShellInvocation({
        container,
        script,
        database: process.env.LOOM_CDA_DATABASE ?? 'loom_dev',
      });
      const result = spawnSync(invocation.command, invocation.args, {
        encoding: 'utf8',
        timeout: identity.hostTimeoutMs,
        maxBuffer: 200000,
      });
      const processEvidence = summarizeArangoShellResult(result, { project, generation: 'cda-fhir-v1' });
      Object.assign(identity, processEvidence);
      if (!processEvidence.processSucceeded || !processEvidence.stdoutJsonComplete) {
        identity.status = 'failed';
        identity.error = processEvidence.spawnError
          || processEvidence.stderrExcerpt
          || processEvidence.stdoutTailExcerpt
          || 'Arangosh did not return a complete JSON row array.';
        throw new Error(`Scoped Arango query failed (${processEvidence.exitStatus ?? 'no exit status'}${processEvidence.signal ? `, ${processEvidence.signal}` : ''}): ${identity.error}`);
      }
      const output = String(result.stdout ?? '');
      const resultStart = output.indexOf('[');
      assert(resultStart >= 0, `Arangosh returned no JSON rows for ${phase}: ${processEvidence.stdoutTailExcerpt}`);
      const rows = JSON.parse(output.slice(resultStart));
      assert(Array.isArray(rows), `Arango ${phase} must return an array`);
      identity.status = 'passed';
      identity.resultRows = rows.length;
      return rows;
    } catch (error) {
      identity.status = 'failed';
      identity.error ??= error instanceof Error ? error.message : String(error);
      throw error;
    } finally {
      identity.durationMs = Date.now() - identity.startedAt;
    }
  };
  const rawPatients = ids => rawQuery(`FOR d IN Patient FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN d.id`, 'exact-patient-row-reread');
  const rawObservationSubjects = ids => rawQuery(`FOR d IN Observation FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == "cda-fhir-v1" AND d.id IN ${JSON.stringify(ids)} RETURN {id:d.id,subject:d.payload.subject.reference}`, 'exact-observation-row-reread');
  const {
    observationSampleLimit,
    candidatePatientLimit,
    observationSentinelLimit,
  } = rootRebaseOracleBounds;
  const rawPatientObservationWitnessCandidates = () => rawQuery(`FOR observation IN Observation FILTER observation.project == ${JSON.stringify(project)} AND observation.dataset_generation == "cda-fhir-v1" AND observation.resourceType == "Observation" AND observation.payload.resourceType == "Observation" AND IS_STRING(observation.payload.subject.reference) AND STARTS_WITH(observation.payload.subject.reference, "Patient/") SORT observation.id LIMIT ${observationSampleLimit} COLLECT patientReference = observation.payload.subject.reference WITH COUNT INTO sampledObservationCount FILTER sampledObservationCount >= 2 AND sampledObservationCount <= 25 SORT patientReference LIMIT ${candidatePatientLimit} RETURN {patientId:SUBSTRING(patientReference, 8),sampledObservationCount}`, 'bounded-observation-subject-candidate-sample');
  const rawPatientObservationExactReread = patientId => rawQuery(`FOR patient IN Patient FILTER patient.project == ${JSON.stringify(project)} AND patient.dataset_generation == "cda-fhir-v1" AND patient.resourceType == "Patient" AND patient.payload.resourceType == "Patient" AND patient.id == ${JSON.stringify(patientId)} LIMIT 2 LET observations = (FOR observation IN Observation FILTER observation.project == ${JSON.stringify(project)} AND observation.dataset_generation == "cda-fhir-v1" AND observation.resourceType == "Observation" AND observation.payload.resourceType == "Observation" AND observation.payload.subject.reference == CONCAT("Patient/", patient.id) SORT observation.id LIMIT ${observationSentinelLimit} RETURN {id:observation.id,project:observation.project,generation:observation.dataset_generation}) RETURN {patientId:patient.id,patientKey:patient._id,project:patient.project,generation:patient.dataset_generation,observations}`, 'exact-candidate-patient-observation-reread');
  const command = async commands => {
    const before = await builder();
    const response = await fetch(`${authoringURL}/commands`, {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ commandId: crypto.randomUUID(), semanticsVersion: before.workspace?.semanticsVersion ?? 9,
        snapshotToken: before.catalog.snapshotToken, expectedDraftVersion: before.draftVersion, expectedDraftDigest: before.draftDigest, commands }),
      signal: AbortSignal.timeout(30_000),
    });
    const body = await response.json();
    assert.equal(response.status, 200, JSON.stringify(body));
    return body;
  };
  const applyReviewedRowChange = async (stage, after) => {
    await cda.wait( () => Boolean([...document.querySelectorAll('[data-testid="row-change-preview-panel"] button')]
      .find(button => button.textContent?.includes('Apply row change') && !button.disabled)));
    state[stage] = await cda.inspect( () => {
      const panel = document.querySelector('[data-testid="row-change-preview-panel"]');
      return { text: panel?.innerText, rows: [...panel.querySelectorAll('table tr')].map(row => [...row.querySelectorAll('th,td')].map(cell => cell.innerText.trim())) };
    });
    assert(state[stage].rows.length > 1, `${stage} did not render proposed rows`);
    const requests = state.nativeRequests.slice(after).map(({ path, status }) => ({ path, status }));
    assert(requests.some(response => response.path.endsWith('/preview') && response.status === 200), `${stage} did not request a successful candidate preview`);
    assert(!requests.some(response => response.path.endsWith('/commands')), `${stage} changed the saved table before Apply`);
    const started = Date.now();
    await actClick('[data-testid="row-change-preview-panel"] button', `Apply ${stage}`, { name: 'Apply row change' });
    return started;
  };
  const seedPatientFilter = async witness => {
    const before = await builder();
    const beforeDocument = before.workspace.documents.find(document => document.output.id === outputId);
    assert(beforeDocument, 'The Patient table must exist before authoring its filter');
    assert.equal(beforeDocument.rootResourceType, 'Patient');
    assert.equal(beforeDocument.construction?.steps?.length ?? 0, 0,
      'The root-rebase case must author one native construction step from an empty starting state');

    let started = Date.now();
    await actClick('[data-testid="construction-action-keep-rows"]', 'Open Patient row filter', { name: 'Filter rows' });
    const columnSelector = '[data-testid="construction-filter-editor"] select[aria-label="Column"]';
    const conditionSelector = '[data-testid="construction-filter-editor"] select[aria-label="Condition"]';
    await cda.wait(([column, condition]) => Boolean(document.querySelector(`${column}:not(:disabled)`) && document.querySelector(`${condition}:not(:disabled)`)), [columnSelector, conditionSelector], 5000);
    state.timingsMs.patientFilterEditor = Date.now() - started;
    assert(state.timingsMs.patientFilterEditor < 5000,
      `Opening the native Patient filter editor took ${state.timingsMs.patientFilterEditor} ms`);
    const sourceColumn = await cda.inspect(() => {
      const select = document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');
      return { id: select?.value, label: select?.selectedOptions?.[0]?.textContent?.trim() };
    });
    assert(sourceColumn.id && sourceColumn.label?.startsWith('Patient ID'),
      `The native filter must target the authored Patient ID column: ${JSON.stringify(sourceColumn)}`);
    const savedColumn = beforeDocument.columns.find(column => column.columnId === sourceColumn.id);
    assert(savedColumn, 'The selected Patient ID control must resolve to the exact saved output column');
    await cda.selectOption(conditionSelector, 'EQUALS');
    const valueSelector = '[data-testid="construction-filter-editor"] input[aria-label="Value"]';
    await cda.wait(([selector]) => Boolean(document.querySelector(`${selector}:not(:disabled)`)), [valueSelector], 5000);
    started = Date.now();
    await cda.fill(valueSelector, witness.patientId);
    state.actions.push('Set Patient ID equality filter');
    await cda.wait(() => ['ready', 'error', 'needs-repair'].includes(
      document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus), [], 5000);
    const renderedProposal = await cda.inspect(() => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return { status: panel?.dataset.proposalStatus, rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())) };
    });
    assert.equal(renderedProposal.status, 'ready');
    assert.deepEqual(renderedProposal.rows, [[witness.patientId]],
      'The visible native proposal must show the exact Patient selected by the raw oracle');
    state.timingsMs.patientFilterProposal = Date.now() - started;
    assert(state.timingsMs.patientFilterProposal < 5000,
      `Patient filter choice-to-preview took ${state.timingsMs.patientFilterProposal} ms`);

    started = Date.now();
    await actClick('[data-testid="construction-apply-proposal"]', 'Apply Patient ID equality filter', { name: 'Apply change' });
    await cda.wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), [], 5000);
    await waitForVisiblePreviewRows({ rowCount: 1, exactRows: [[witness.patientId]] });
    state.timingsMs.patientFilterApply = Date.now() - started;
    assert(state.timingsMs.patientFilterApply < 5000,
      `Applying the Patient filter took ${state.timingsMs.patientFilterApply} ms to render`);
    const saved = await builder();
    const savedDocument = saved.workspace.documents.find(document => document.output.id === outputId);
    assert.deepEqual(savedDocument.columns, beforeDocument.columns,
      'Authoring the construction filter must retain the original output columns');
    assert.deepEqual(savedDocument.population, beforeDocument.population,
      'Authoring the construction filter must retain the starting Patient population and route');
    assert.equal(savedDocument.construction.steps.length, 1);
    const filterStep = savedDocument.construction.steps[0];
    assert.equal(filterStep.operation.kind, 'FILTER');
    assert.deepEqual(filterStep.operation.filter, {
      columnId: sourceColumn.id,
      operator: 'EQUALS',
      values: [{ kind: 'STRING', string: witness.patientId }],
    });
    await cda.navigate(pageURL);
    await selectTable();
    const reloaded = await builder();
    const reloadedDocument = reloaded.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(reloadedDocument.rootResourceType, 'Patient');
    assert.deepEqual(reloadedDocument.construction, savedDocument.construction,
      'Reload must preserve the authored Patient ID filter before root rebase');
    assert.deepEqual(reloadedDocument.columns, beforeDocument.columns);
    assert.deepEqual(reloadedDocument.population, beforeDocument.population);
    const reloadedPreview = await preview('patientPreview', {
      rowCount: 1, exactRows: [[witness.patientId]], headerIncludes: ['PATIENT ID'],
    });
    assert.deepEqual(reloadedPreview.headers, ['PATIENT ID']);
    assert.deepEqual(reloadedPreview.rows, [[witness.patientId]],
      'Reload must render the exact Patient ID selected by the authored filter');
    assert.deepEqual(new Set(reloadedPreview.rows.map(row => row[0])),
      new Set(rawPatients(reloadedPreview.rows.map(row => row[0]))));
    state.patientFilter = {
      status: 'passed', step: filterStep, draftDigest: reloaded.draftDigest,
      patientId: witness.patientId, observationIDs: witness.observationIDs,
      preservedColumns: true, preservedPopulation: true,
    };
    recordRequiredCheck(1, 'persistence', true, {
      patientId: witness.patientId, step: filterStep, columns: reloadedDocument.columns,
      population: reloadedDocument.population, previewRows: reloadedPreview.rows,
      draftDigest: reloaded.draftDigest,
    });
    return { reloaded, document: reloadedDocument, preview: reloadedPreview };
  };
  const proposeObservationRoot = async (stage, witness) => {
    const controls = await openRowControl();
    assert.equal(controls.disabled, false, 'Row-root control must remain enabled with the authored filter');
    const observationChoice = controls.options.find(option => option.label.includes('Observation'));
    assert(observationChoice && !observationChoice.disabled, 'Related Observation cannot be chosen as rows');
    const assessmentStart = state.nativeRequests.length;
    const started = Date.now();
    await cda.selectOption('select[aria-label="One row per"]', observationChoice.value);
    state.actions.push(`Choose Observation rows ${stage}`);
    const assessment = await waitForRowAssessment(assessmentStart);
    state[`${stage}Assessment`] = assessment;
    state.timingsMs[`${stage}Assessment`] = Date.now() - started;
    assert(state.timingsMs[`${stage}Assessment`] < 5000,
      `${stage} root choice-to-assessment took ${state.timingsMs[`${stage}Assessment`]} ms`);
    if (assessment.status === 'BLOCKED') {
      const repairPanelStarted = Date.now();
      await cda.wait(() => document.body.innerText.includes('Choose how to preserve this table'));
      state.timingsMs[`${stage}RepairChoice`] = Date.now() - repairPanelStarted;
      assert(state.timingsMs[`${stage}RepairChoice`] < 5000,
        `${stage} Subject repair choice took ${state.timingsMs[`${stage}RepairChoice`]} ms to render`);
      state[`${stage}RepairPanel`] = await cda.inspect(() => {
        const panel = document.getElementById('row-change-repair-title')?.closest('section');
        return { text: panel?.innerText, buttons: [...(panel?.querySelectorAll('button') ?? [])]
          .map(button => ({ text: button.innerText, disabled: button.disabled })) };
      });
      const repairButton = state[`${stage}RepairPanel`].buttons.find(button => button.text.includes('Match through Subject') && !button.disabled);
      assert(repairButton, `No Subject relationship repair was offered: ${JSON.stringify(state[`${stage}RepairPanel`])}`);
      const repairStarted = state.nativeRequests.length;
      const repairChoiceStarted = Date.now();
      await actClick('section button', `Preserve Patient ID through Observation Subject ${stage}`, { name: repairButton.text });
      state[`${stage}RepairedAssessment`] = await waitForRowAssessment(repairStarted);
      state[`${stage}Preview`] = await waitForRowChangePreviewRows({
        rowCount: witness.observationIDs.length, patientId: witness.patientId,
      });
      state.timingsMs[`${stage}RepairChoiceToPreview`] = Date.now() - repairChoiceStarted;
      assert(state.timingsMs[`${stage}RepairChoiceToPreview`] < 5000,
        `${stage} Subject choice-to-row-preview took ${state.timingsMs[`${stage}RepairChoiceToPreview`]} ms`);
    } else {
      state[`${stage}Preview`] = await waitForRowChangePreviewRows({
        rowCount: witness.observationIDs.length, patientId: witness.patientId,
      });
      state.timingsMs[stage] = Date.now() - started;
      assert(state.timingsMs[stage] < 5000, `${stage} choice-to-row-preview took ${state.timingsMs[stage]} ms`);
    }
    return assessmentStart;
  };
  const cancelReviewedRowChange = async (stage, after, original, witness, originalPreview) => {
    const panel = await cda.inspect(() => {
      const element = document.querySelector('[data-testid="row-change-preview-panel"]');
      return { text: element?.innerText, rows: [...(element?.querySelectorAll('table tr') ?? [])]
        .map(row => [...row.querySelectorAll('th,td')].map(cell => cell.innerText.trim())) };
    });
    assert(panel.rows.length > 1, `${stage} must show the proposed related-resource rows before Cancel`);
    assert.equal(panel.rows.length - 1, witness.observationIDs.length,
      `${stage} must preview the exact raw Observation multiplicity before Cancel`);
    for (const row of panel.rows.slice(1)) assert(row.includes(witness.patientId),
      `${stage} candidate rows must retain the filtered Patient ID`);
    assert(!state.nativeRequests.slice(after).some(request => request.path.endsWith('/commands')),
      `${stage} proposal must not change the saved workspace before Cancel or Apply`);
    state[stage] = panel;
    const requestsBeforeCancel = state.nativeRequests.length;
    const started = Date.now();
    await actClick('[data-testid="row-change-preview-panel"] button', `Cancel ${stage}`, { name: 'Keep current rows' });
    await cda.wait(() => !document.querySelector('[data-testid="row-change-preview-panel"]'));
    await waitForVisiblePreviewRows({
      rowCount: originalPreview.rows.length, exactRows: originalPreview.rows, panelClosed: true,
    });
    state.timingsMs[`${stage}Cancel`] = Date.now() - started;
    assert(state.timingsMs[`${stage}Cancel`] < 5000,
      `${stage} Cancel-to-original-rows took ${state.timingsMs[`${stage}Cancel`]} ms`);
    const cancelled = await builder();
    const cancelledDocument = cancelled.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(cancelled.draftVersion, original.draftVersion, `${stage} Cancel must leave the draft version unchanged`);
    assert.equal(cancelled.draftDigest, original.draftDigest, `${stage} Cancel must leave the draft digest unchanged`);
    assert.deepEqual(cancelled.workspace, original.workspace, `${stage} Cancel must leave the complete saved workspace unchanged`);
    assert.equal(cancelledDocument.rootResourceType, 'Patient');
    assert.deepEqual(cancelledDocument.construction, original.document.construction,
      `${stage} Cancel must retain the authored Patient filter`);
    assert.deepEqual(cancelledDocument.columns, original.document.columns,
      `${stage} Cancel must retain every authored output column`);
    assert.deepEqual(cancelledDocument.population, original.document.population,
      `${stage} Cancel must retain the selected Patient population and route`);
    assert(!state.nativeRequests.slice(requestsBeforeCancel).some(request => request.path.endsWith('/commands')),
      `${stage} Cancel must not write a Builder command`);
    state[`${stage}Cancel`] = { status: 'passed', draftVersion: cancelled.draftVersion,
      draftDigest: cancelled.draftDigest, preservedConstruction: true, preservedColumns: true, preservedPopulation: true };
    if (stage === 'cancelledObservationRootProposal') {
      recordRequiredCheck(2, 'persistence', true, {
        draftVersion: cancelled.draftVersion, draftDigest: cancelled.draftDigest,
        construction: cancelledDocument.construction, columns: cancelledDocument.columns,
        population: cancelledDocument.population, candidatePreviewRows: panel.rows.length - 1,
      });
    }
  };

  try {
    const witnessCandidates = rawPatientObservationWitnessCandidates();
    const witnessSelection = selectRootRebaseWitness(
      witnessCandidates,
      patientId => rawPatientObservationExactReread(patientId),
      { project, generation: 'cda-fhir-v1' },
    );
    const witness = witnessSelection.witness;
    const witnessValid = Boolean(witness);
    state.oracleCandidateSelection = witnessSelection;
    recordRequiredCheck(0, 'correctness', witnessValid, {
      candidateCount: witnessCandidates.length,
      candidateLimit: candidatePatientLimit,
      observationSampleLimit,
      selectionAttempts: witnessSelection.attempts,
      exactWitness: witness,
      perPatientObservationSentinelLimit: observationSentinelLimit,
      queryCaps: { maxRuntimeSeconds: 8, memoryLimitBytes: 256 * 1024 * 1024, hostTimeoutMs: 30_000 },
      project, generation: 'cda-fhir-v1', visiblePreviewLimit: 25,
    });
    assert(witnessValid,
      `The bounded project/generation raw oracle must find one Patient with 2–25 exact Observations among its first ${candidatePatientLimit} sampled candidates; a 26-row sentinel and any query failure are rejected: ${JSON.stringify(witnessSelection)}`);
    state.oracle = buildRootRebaseOracleMetadata(witness, witnessSelection);
    await cda.navigate( pageURL);
    await cda.wait( () => document.body.innerText.includes('DATASET WORKSPACE'));
    state.tablesBefore = await cda.inspect( () => [...document.querySelectorAll('button[data-testid^="construction-table-"]')].map(button => button.innerText.trim().split(String.fromCharCode(10)).at(-1)));
    await actClick('button', 'New table', { name: 'New table' });
    await cda.wait( () => Boolean(document.querySelector('button[aria-label="Choose Patient rows"]') && !document.querySelector('button[aria-label="Choose Patient rows"]').disabled));
    await cda.fill( '#first-table-name', tableName);
    await actClick('button[aria-label="Choose Patient rows"]', 'Choose Patient rows');
    await cda.wait( () => document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]').length > 1);
    const created = await builder();
    const initial = created.workspace.documents.find(document => document.output.title === tableName);
    assert(initial, 'The temporary Patient table was not created');
    outputId = initial.output.id;
    assert.equal(initial.rootResourceType, 'Patient');
    assert.equal(initial.columns.length, 1);
    const patientNode = created.catalog.nodes.find(node => node.resourceType === 'Patient');
    const observationNode = created.catalog.nodes.find(node => node.resourceType === 'Observation');
    const subjectEdge = created.catalog.edges.find(edge => edge.fromNodeId === patientNode.nodeId && edge.toNodeId === observationNode.nodeId && edge.label === 'subject_Patient');
    assert(subjectEdge, 'CDA catalog has no Patient to Observation Subject relationship');
    state.setupRoute = { edgeId: subjectEdge.edgeId, label: subjectEdge.label };
    await command([{ type: 'ADD_ROUTE', outputId, parentOccurrenceId: 'base', edgeId: subjectEdge.edgeId }]);

    await cda.navigate( pageURL);
    await selectTable();
    const seeded = await seedPatientFilter(witness);
    state.before = { ...seeded.reloaded, document: seeded.document };
    state.beforeControl = await openRowControl();
    assert.equal(state.beforeControl.disabled, false, 'Row-root control is disabled after adding a related Observation');
    const originalPreview = seeded.preview;
    assert.deepEqual(originalPreview.headers, ['PATIENT ID']);
    assert.deepEqual(originalPreview.rows, [[witness.patientId]], 'The authored Patient filter must select the exact raw witness');
    assert.deepEqual(new Set(originalPreview.rows.map(row => row[0])), new Set(rawPatients(originalPreview.rows.map(row => row[0]))));

    const cancelledProposalStart = await proposeObservationRoot('cancelledObservationRootProposal', witness);
    await cancelReviewedRowChange('cancelledObservationRootProposal', cancelledProposalStart, state.before, witness, originalPreview);
    await cda.navigate(pageURL);
    await selectTable();
    const cancelledReload = await builder();
    const cancelledReloadDocument = cancelledReload.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(cancelledReloadDocument.rootResourceType, 'Patient');
    assert.deepEqual(cancelledReload.workspace, state.before.workspace,
      'Reload after root-change Cancel must preserve the exact authored filter, columns, population, and route');
    const cancelledPreview = await preview('cancelledPatientPreview', {
      rowCount: 1, exactRows: [[witness.patientId]], headerIncludes: ['PATIENT ID'],
    });
    assert.deepEqual(cancelledPreview.rows, originalPreview.rows,
      'Reload after root-change Cancel must render the exact filtered Patient row');

    const assessmentStart = await proposeObservationRoot('observationRootProposal', witness);
    const observationApplyStartedAt = await applyReviewedRowChange('observationRootProposal', assessmentStart);
    const observationRows = witness.observationIDs.map(() => [witness.patientId]);
    await waitForVisiblePreviewRows({
      rowCount: observationRows.length, exactRows: observationRows, panelClosed: true,
    });
    state.timingsMs.observationRootApply = Date.now() - observationApplyStartedAt;
    assert(state.timingsMs.observationRootApply < 5000,
      `Applying Observation rows took ${state.timingsMs.observationRootApply} ms to render the exact raw row count and Patient IDs`);
    const changed = await waitForRoot(outputId, 'Observation');
    state.rootChangeRequests = state.nativeRequests.slice(assessmentStart).map(({ path, status }) => ({ path, status }));
    state.changed = { draftVersion: changed.draftVersion, document: changed.workspace.documents.find(document => document.output.id === outputId) };
    assert.equal(state.changed.document?.rootResourceType, 'Observation', 'Apply did not change the root resource');
    assert.deepEqual(state.changed.document.construction, state.before.document.construction,
      'Applying Observation rows must preserve the exact authored Patient ID filter');
    assert.deepEqual(state.changed.document.columns, state.before.document.columns,
      'Root change must preserve the exact authored Patient ID output column');
    assert.deepEqual(state.changed.document.population, state.before.document.population,
      'Root change must preserve the selected Patient population and route');
    recordRequiredCheck(3, 'persistence', true, {
      rootResourceType: state.changed.document.rootResourceType,
      construction: state.changed.document.construction,
      columns: state.changed.document.columns, population: state.changed.document.population,
    });
    await cda.navigate( pageURL);
    await selectTable();
    const observationReload = await builder();
    const observationReloadDocument = observationReload.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(observationReloadDocument.rootResourceType, 'Observation');
    assert.deepEqual(observationReloadDocument.construction, state.before.document.construction,
      'Observation-root reload must preserve the exact authored Patient filter');
    assert.deepEqual(observationReloadDocument.columns, state.before.document.columns);
    assert.deepEqual(observationReloadDocument.population, state.before.document.population);
    const observationPreview = await preview('observationPreview', {
      rowCount: witness.observationIDs.length,
      exactRows: witness.observationIDs.map(() => [witness.patientId]),
      headerIncludes: ['PATIENT ID'],
    });
    assert.equal(observationPreview.rows.length, witness.observationIDs.length,
      'The Observation-root preview must preserve the raw Patient-to-Observation multiplicity');
    assert.deepEqual(observationPreview.rows.map(row => row[0]), witness.observationIDs.map(() => witness.patientId),
      'The authored Patient ID filter must retain the exact parent Patient on every Observation row');
    assert.deepEqual(new Set(observationPreview.rows.map(row => row[0])), new Set(rawPatients(observationPreview.rows.map(row => row[0]))), 'Rebased Patient IDs differ from CDA source');
    await actClick('button[aria-label^="Add columns:"]', 'Open Add columns on Observation rows', { includes: 'Add columns' });
    await cda.wait( () => Boolean(document.querySelector('input[aria-label="Select Observation.id"]') && !document.querySelector('input[aria-label="Select Observation.id"]').disabled));
    await actClick('input[aria-label="Select Observation.id"]', 'Select Observation ID');
    await actClick('[aria-label="Add columns editor"] button', 'Preview Observation ID column', { name: 'Add 1 selected feature' });
    await cda.wait( () => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus === 'ready');
    state.observationColumnProposal = await cda.inspect( () => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.innerText.slice(0, 1000));
    const columnApplyStart = state.nativeRequests.length;
    await actClick('[data-testid="construction-choice-proposal-panel"] button', 'Apply Observation ID column', { name: 'Apply columns' });
    const commandResponse = await cda.waitForCapturedResponse(browserEvents,
      request => request.path.endsWith('/commands') && request.method === 'POST', 30_000);
    assert.equal(commandResponse.status, 200, 'Applying Observation ID failed');
    const withObservationID = await builder();
    state.withObservationID = withObservationID.workspace.documents.find(document => document.output.id === outputId);
    assert.deepEqual(state.withObservationID.construction, state.before.document.construction,
      'Adding Observation ID must preserve the authored Patient filter');
    assert.deepEqual(state.withObservationID.population, state.before.document.population,
      'Adding Observation ID must preserve the selected Patient population and route');
    await cda.navigate( pageURL);
    await selectTable();
    const pairedReload = await builder();
    const pairedReloadDocument = pairedReload.workspace.documents.find(document => document.output.id === outputId);
    assert.equal(pairedReloadDocument.rootResourceType, 'Observation');
    assert.deepEqual(pairedReloadDocument.construction, state.before.document.construction);
    assert.deepEqual(pairedReloadDocument.population, state.before.document.population);
    assert.deepEqual(pairedReloadDocument.columns, state.withObservationID.columns);
    const pairedPreview = await preview('pairedPreview', {
      rowCount: witness.observationIDs.length,
      headerColumnValues: [
        { headerIncludes: 'PATIENT ID', values: witness.observationIDs.map(() => witness.patientId) },
        { headerIncludes: 'OBSERVATION ID', values: witness.observationIDs, sort: true },
      ],
    });
    const patientIndex = pairedPreview.headers.findIndex(header => header === 'PATIENT ID');
    const observationIndex = pairedPreview.headers.findIndex((header, index) => index !== patientIndex && header.endsWith('ID'));
    assert(patientIndex >= 0 && observationIndex >= 0, 'The rebased preview lacks Patient and Observation identifiers');
    assert.equal(pairedPreview.rows.length, witness.observationIDs.length,
      'The paired Observation-root preview must retain exact raw multiplicity');
    const displayedObservationIDs = pairedPreview.rows.map(row => row[observationIndex]).sort();
    assert.deepEqual(displayedObservationIDs, [...witness.observationIDs].sort(),
      'Observation-root IDs must match the exact raw Patient-to-Observation oracle');
    assert.deepEqual(pairedPreview.rows.map(row => row[patientIndex]), witness.observationIDs.map(() => witness.patientId),
      'Every exact Observation row must retain the selected Patient ID');
    const observations = rawObservationSubjects(pairedPreview.rows.map(row => row[observationIndex]));
    const sourceByID = new Map(observations.map(record => [record.id, record.subject]));
    assert.equal(sourceByID.size, pairedPreview.rows.length, 'Displayed Observation IDs do not match CDA records');
    for (const row of pairedPreview.rows) assert.equal(sourceByID.get(row[observationIndex]), `Patient/${row[patientIndex]}`, `Patient relationship differs for Observation ${row[observationIndex]}`);
    state.pairOracle = observations;
    recordRequiredCheck(4, 'correctness', true, {
      patientId: witness.patientId, observationIDs: displayedObservationIDs,
      subjects: observations, rows: pairedPreview.rows,
    });
    state.afterControl = await openRowControl();
    const patientChoice = state.afterControl.options.find(option => option.label.includes('Patient'));
    assert(patientChoice && !patientChoice.disabled, 'Patient rows cannot be restored');
    const restorationAssessmentStart = state.nativeRequests.length;
    const restorationStartedAt = Date.now();
    await cda.selectOption( 'select[aria-label="One row per"]', patientChoice.value);
    state.actions.push('Restore Patient rows');
    state.patientAssessment = await waitForRowAssessment(restorationAssessmentStart);
    state.timingsMs.patientRootAssessment = Date.now() - restorationStartedAt;
    assert(state.timingsMs.patientRootAssessment < 5000,
      `Patient root choice-to-assessment took ${state.timingsMs.patientRootAssessment} ms`);
    if (state.patientAssessment.status === 'BLOCKED') {
      const repairPanelStarted = Date.now();
      await cda.wait( () => document.body.innerText.includes('Choose how to preserve this table'));
      state.timingsMs.patientRootRepairChoice = Date.now() - repairPanelStarted;
      assert(state.timingsMs.patientRootRepairChoice < 5000,
        `Patient root Subject repair choice took ${state.timingsMs.patientRootRepairChoice} ms to render`);
      state.restorationRepairPanel = await cda.inspect( () => {
        const panel = document.getElementById('row-change-repair-title')?.closest('section');
        return { text: panel?.innerText, buttons: [...(panel?.querySelectorAll('button') ?? [])].map(button => ({ text: button.innerText, disabled: button.disabled })) };
      });
      const repairButton = state.restorationRepairPanel.buttons.find(button => button.text.includes('Match through Subject') && !button.disabled);
      assert(repairButton, `No Subject relationship restoration was offered: ${JSON.stringify(state.restorationRepairPanel)}`);
      const repairStarted = state.nativeRequests.length;
      const repairChoiceStarted = Date.now();
      await actClick('section button', 'Restore Subject relationship', { name: repairButton.text });
      state.restorationRepairedAssessment = await waitForRowAssessment(repairStarted);
      await waitForRowChangePreviewRows({ rowCount: 1, patientId: witness.patientId });
      state.timingsMs.patientRootRepairChoiceToPreview = Date.now() - repairChoiceStarted;
      assert(state.timingsMs.patientRootRepairChoiceToPreview < 5000,
        `Patient root Subject choice-to-row-preview took ${state.timingsMs.patientRootRepairChoiceToPreview} ms`);
    } else {
      await waitForRowChangePreviewRows({ rowCount: 1, patientId: witness.patientId });
      state.timingsMs.patientRootProposal = Date.now() - restorationStartedAt;
      assert(state.timingsMs.patientRootProposal < 5000,
        `Restoring Patient rows to a rendered proposal took ${state.timingsMs.patientRootProposal} ms`);
    }
    const patientApplyStartedAt = await applyReviewedRowChange('patientRootProposal', restorationAssessmentStart);
    await waitForVisiblePreviewRows({
      rowCount: 1,
      cells: [{ row: 0, column: patientIndex, value: witness.patientId }],
      panelClosed: true,
    });
    state.timingsMs.patientRootApply = Date.now() - patientApplyStartedAt;
    assert(state.timingsMs.patientRootApply < 5000,
      `Applying Patient rows took ${state.timingsMs.patientRootApply} ms to render the exact restored Patient row`);
    const restoredState = await waitForRoot(outputId, 'Patient');
    const restoredDocument = restoredState.workspace.documents.find(document => document.output.id === outputId);
    assert.deepEqual(restoredDocument.construction, state.before.document.construction,
      'Restoring Patient rows must preserve the authored Patient ID filter');
    assert.deepEqual(restoredDocument.population, state.before.document.population,
      'Restoring Patient rows must preserve the selected population and route');
    assert.deepEqual(restoredDocument.columns, state.withObservationID.columns,
      'Restoring Patient rows must preserve both authored output columns');
    state.restorationRequests = state.nativeRequests.slice(restorationAssessmentStart).map(({ path, status }) => ({ path, status }));
    await cda.navigate( pageURL);
    await selectTable();
    const restored = await builder();
    state.restored = { draftVersion: restored.draftVersion, document: restored.workspace.documents.find(document => document.output.id === outputId) };
    assert.equal(state.restored.document?.rootResourceType, 'Patient');
    assert.deepEqual(state.restored.document.construction, state.before.document.construction,
      'Reload after restoration must preserve the exact authored Patient filter');
    assert.deepEqual(state.restored.document.population, state.before.document.population);
    assert.deepEqual(state.restored.document.columns, state.withObservationID.columns);
    const restoredPreview = await preview('restoredPreview', {
      rowCount: 1,
      headerColumnValues: [{ headerIncludes: 'PATIENT ID', values: [witness.patientId] }],
    });
    const restoredPatientIndex = restoredPreview.headers.indexOf('PATIENT ID');
    assert(restoredPatientIndex >= 0, 'Restored Patient ID column is missing');
    assert.deepEqual(restoredPreview.rows.map(row => row[restoredPatientIndex]), originalPreview.rows.map(row => row[0]));
    assert.deepEqual(restoredPreview.rows.map(row => row[restoredPatientIndex]), [witness.patientId],
      'Restored Patient rows must still satisfy the exact authored Patient ID filter');
    recordRequiredCheck(5, 'persistence', true, {
      rootResourceType: state.restored.document.rootResourceType,
      construction: state.restored.document.construction, columns: state.restored.document.columns,
      population: state.restored.document.population, previewRows: restoredPreview.rows,
    });
    const measuredBudgets = Object.entries(state.timingsMs).filter(([, durationMs]) => Number.isFinite(durationMs));
    const withinBudget = measuredBudgets.length > 0 && measuredBudgets.every(([, durationMs]) => durationMs < 5000);
    recordRequiredCheck(6, 'performance', withinBudget, {
      checkpoints: Object.fromEntries(measuredBudgets), maximumDurationMs: Math.max(...measuredBudgets.map(([, durationMs]) => durationMs)),
      budgetMs: 5000,
    });
    const noUnexpectedErrors = state.nativeRequests.every(response => response.status < 400) && state.errors.length === 0;
    await browserEvents.flush();
    cda.includeBrowserDiagnostics();
    const networkClean = state.nativeRequests.every(response => response.status < 400) && state.errors.length === 0;
    recordRequiredCheck(7, 'correctness', networkClean, {
      errors: state.errors, nativeRequests: state.nativeRequests.map(({ path, status }) => ({ path, status })),
    });
    assert(noUnexpectedErrors && networkClean, `Unexpected browser or owned API errors: ${JSON.stringify(state.errors)}`);
  } catch (error) {
    state.error = error instanceof Error ? error.message : String(error);
    state.failureUI = await cda.inspect( () => ({ text: document.body.innerText.slice(0, 3500), alerts: [...document.querySelectorAll('[role="alert"]')].map(node => node.innerText) })).catch(() => undefined);
    cda.includeBrowserDiagnostics();
    throw error;
  } finally {
    if (outputId) {
      try {
        await command([{ type: 'DELETE_TABLE', outputId }]);
        state.cleanup = 'temporary table deleted';
      } catch (error) {
        state.cleanup = error instanceof Error ? error.message : String(error);
      }
    }
    await browserEvents.flush();
    cda.includeBrowserDiagnostics();
    await cda.attachReport('root-rebase-lifecycle', state);
  }
}
