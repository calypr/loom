import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { scenarioCaseFor } from '../registry.mjs';

const CASE_ID = 'cda-five-hop-related-expansion';
const CASE_NAME = 'medication-preserve-parent';
const ACTION_BUDGET_MS = 5_000;
const PANEL = '[data-testid="construction-related-expand-editor"]';
const ROUTE_LABEL = 'Specimen <-[focus]- Observation <-[stage_assessment]- Condition -[subject]-> Patient <-[subject]- MedicationAdministration -[medication_reference]-> Medication';
const ROUTE_STEPS = [
  ['Specimen', 'Observation', 'focus_Specimen', 'INBOUND'],
  ['Observation', 'Condition', 'stage_assessment_Observation', 'INBOUND'],
  ['Condition', 'Patient', 'subject_Patient', 'OUTBOUND'],
  ['Patient', 'MedicationAdministration', 'subject_Patient', 'INBOUND'],
  ['MedicationAdministration', 'Medication', 'medication_reference_Medication', 'OUTBOUND'],
];

export async function zeroColumnRelatedMedicationWorkflow({ page, cda }) {
  const project = cda.project;
  const generation = cda.target.fixtureGeneration ?? cda.target.generation;
  const arangoContainer = cda.target.arangoContainer;
  const arangoDatabase = process.env.LOOM_ARANGO_DATABASE ?? 'loom_dev';
  const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
  const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
  const explorer = `related-medication-${randomUUID()}`;
  const explorerRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const explorerPath = `${explorerRoot}/${encodeURIComponent(explorer)}`;
  const authoringPath = `${explorerPath}/authoring/v2`;
  const requiredChecks = scenarioCaseFor(CASE_ID, CASE_NAME).requiredChecks;
  assert.deepEqual(requiredChecks, cda.report.requiredChecks,
    'The fixture report must use the registered zero-column related Medication contract');
  assert(project && generation && arangoContainer, 'The case requires the owned CDA project, generation, and Arango container');
  assert.equal(generation, 'cda-fhir-v1');
  assert.equal(requiredChecks.length, 11, 'The registry must define ten lifecycle checks plus watched-source/API integrity');

  const report = Object.assign(cda.report, {
    explorer,
    routeLabel: ROUTE_LABEL,
    rawOracle: {},
    lifecycle: {},
    routeEnumeration: null,
    apiRequests: [],
    checkpoints: [],
  });
  let builder;
  let outputId;
  let selection;
  let tracker;
  let fatal;

  const check = (index, dimension, evidence) => {
    const name = requiredChecks[index];
    assert(name, `Missing registered check ${index}`);
    cda.check(dimension, name, true, evidence);
  };

  const rawQuery = `
FOR specimen IN Specimen
  FILTER specimen.project == ${JSON.stringify(project)} AND specimen.dataset_generation == ${JSON.stringify(generation)}
  SORT specimen.id LIMIT 1000
  LET final = (
    FOR focus IN fhir_edge FILTER focus._to == specimen._id AND focus.from_type == 'Observation' AND focus.to_type == 'Specimen' AND focus.label == 'focus_Specimen' AND focus.project == ${JSON.stringify(project)} AND focus.dataset_generation == ${JSON.stringify(generation)}
    LET observation = DOCUMENT(focus._from) FILTER observation != null AND observation.resourceType == 'Observation' AND observation.project == ${JSON.stringify(project)} AND observation.dataset_generation == ${JSON.stringify(generation)}
    FOR stage IN fhir_edge FILTER stage._to == observation._id AND stage.from_type == 'Condition' AND stage.to_type == 'Observation' AND stage.label == 'stage_assessment_Observation' AND stage.project == ${JSON.stringify(project)} AND stage.dataset_generation == ${JSON.stringify(generation)}
    LET condition = DOCUMENT(stage._from) FILTER condition != null AND condition.resourceType == 'Condition' AND condition.project == ${JSON.stringify(project)} AND condition.dataset_generation == ${JSON.stringify(generation)}
    FOR conditionSubject IN fhir_edge FILTER conditionSubject._from == condition._id AND conditionSubject.from_type == 'Condition' AND conditionSubject.to_type == 'Patient' AND conditionSubject.label == 'subject_Patient' AND conditionSubject.project == ${JSON.stringify(project)} AND conditionSubject.dataset_generation == ${JSON.stringify(generation)}
    LET patient = DOCUMENT(conditionSubject._to) FILTER patient != null AND patient.resourceType == 'Patient' AND patient.project == ${JSON.stringify(project)} AND patient.dataset_generation == ${JSON.stringify(generation)}
    FOR administrationSubject IN fhir_edge FILTER administrationSubject._to == patient._id AND administrationSubject.from_type == 'MedicationAdministration' AND administrationSubject.to_type == 'Patient' AND administrationSubject.label == 'subject_Patient' AND administrationSubject.project == ${JSON.stringify(project)} AND administrationSubject.dataset_generation == ${JSON.stringify(generation)}
    LET administration = DOCUMENT(administrationSubject._from) FILTER administration != null AND administration.resourceType == 'MedicationAdministration' AND administration.project == ${JSON.stringify(project)} AND administration.dataset_generation == ${JSON.stringify(generation)}
    FOR medicationRef IN fhir_edge FILTER medicationRef._from == administration._id AND medicationRef.from_type == 'MedicationAdministration' AND medicationRef.to_type == 'Medication' AND medicationRef.label == 'medication_reference_Medication' AND medicationRef.project == ${JSON.stringify(project)} AND medicationRef.dataset_generation == ${JSON.stringify(generation)}
    LET medication = DOCUMENT(medicationRef._to) FILTER medication != null AND medication.resourceType == 'Medication' AND medication.project == ${JSON.stringify(project)} AND medication.dataset_generation == ${JSON.stringify(generation)}
    COLLECT medicationKey = medication._id
    LET uniqueMedication = DOCUMENT(medicationKey)
    SORT uniqueMedication.id LIMIT 26
    RETURN { id: uniqueMedication.id, _id: uniqueMedication._id }
  )
  RETURN { specimen: { id: specimen.id, _id: specimen._id }, medications: final }
`;

  const readOracle = () => {
    const js = `print(JSON.stringify(db._query(${JSON.stringify(rawQuery)}).toArray()));`;
    const result = execFileSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', arangoDatabase, '--javascript.execute-string', js], {
      encoding: 'utf8', timeout: 30_000, maxBuffer: 4_000_000,
    });
    const payload = result.trim().split(/\r?\n/).at(-1);
    assert(payload?.startsWith('['), 'Raw CDA oracle returned no JSON array');
    return JSON.parse(payload);
  };

  const api = async (path, body) => {
    const method = body === undefined ? 'GET' : 'POST';
    const startedAt = Date.now();
    const response = await fetch(`${apiOrigin}${path}`, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-medication-${randomUUID()}` },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(30_000),
    });
    const text = await response.text();
    let value;
    try { value = JSON.parse(text); } catch { value = text; }
    report.apiRequests.push({ method, path, status: response.status, durationMs: Date.now() - startedAt });
    assert(response.ok, `${method} ${path} returned ${response.status}: ${JSON.stringify(value).slice(0, 4000)}`);
    return value;
  };
  const command = async commands => {
    const pre = builder;
    await api(`${authoringPath}/commands`, {
      commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    });
    builder = await api(`${authoringPath}/builder`);
    return pre;
  };
  const doc = (state = builder) => state.workspace.documents.find(item => item.output.id === outputId);
  const measured = async (name, startAt) => {
    const durationMs = Date.now() - startAt;
    report.checkpoints.push({ name, durationMs, limitMs: ACTION_BUDGET_MS });
    assert(durationMs <= ACTION_BUDGET_MS, `${name} took ${durationMs} ms`);
    return durationMs;
  };
  const wait = (callback, args = [], timeout = ACTION_BUDGET_MS) => cda.wait(callback, args, timeout);
  const revealRouteChoice = async (routeSelector, route, shortestRouteLength) => {
    const detailsSelector = `${PANEL} [data-testid="construction-related-expand-other-routes"]`;
    if (route.length > shortestRouteLength) {
      await wait(([selector]) => Boolean(document.querySelector(selector)), [detailsSelector]);
      const detailsOpen = await cda.inspect(([selector]) => document.querySelector(selector)?.open === true, [detailsSelector]);
      if (!detailsOpen) {
        await cda.click(`${detailsSelector} summary`);
        await wait(([selector]) => document.querySelector(selector)?.open === true, [detailsSelector]);
      }
    }
    await wait(([selector]) => document.querySelector(selector)?.disabled === false, [routeSelector]);
  };
  const nativeRows = async (expectedValues, { fromIndex, draft } = {}) => {
    const expectedCount = expectedValues.length;
    assert(draft?.catalog?.snapshotToken && draft?.draftVersion !== undefined && draft?.draftDigest,
      'Saved Builder draft identity is required to bind the native preview');
    const reconcileRequest = await tracker.waitFor(entry => {
      const body = rawRequest(entry);
      return entry.method === 'POST' && entry.path === `${authoringPath}/reconcile` && entry.status === 200 &&
        body?.snapshotToken === draft.catalog.snapshotToken && body?.draftVersion === draft.draftVersion &&
        body?.draftDigest === draft.draftDigest;
    }, { fromIndex, timeoutMs: ACTION_BUDGET_MS });
    const receipt = rawResponse(reconcileRequest);
    assert.equal(receipt?.kind, 'ExplorerBuilderReceipt', 'Native reconcile must return the saved Builder receipt');
    assert(receipt?.receiptId, 'Native reconcile receipt must have an ID');
    assert.equal(receipt.snapshotToken, draft.catalog.snapshotToken);
    assert.equal(receipt.generation, draft.catalog.generation);
    assert.equal(receipt.authorizationScopeDigest, draft.catalog.authorizationScopeDigest);
    assert.equal(receipt.outputs?.filter(item => item.outputId === outputId).length, 1,
      'Reconcile receipt must contain the exact saved output once');
    const savedDocument = draft.workspace.documents.find(item => item.output.id === outputId);
    const receiptDocument = receipt.builder?.documents?.find(item => item.output.id === outputId);
    assert(savedDocument && receiptDocument, 'Reconcile receipt and saved draft must contain the exact output document');
    assert.deepEqual(receiptDocument.columns, savedDocument.columns);
    assert.deepEqual(receiptDocument.population, savedDocument.population);
    assert.deepEqual(receiptDocument.construction, savedDocument.construction);
    assert.deepEqual(receiptDocument.route, savedDocument.route);
    const previewRequest = await tracker.waitFor(entry => {
      const body = rawRequest(entry);
      return entry.method === 'POST' && entry.path === `${authoringPath}/preview` && entry.status === 200 &&
        body?.receiptId === receipt.receiptId && body?.outputId === outputId;
    }, { fromIndex: cda.report.nativeRequests.indexOf(reconcileRequest) + 1, timeoutMs: ACTION_BUDGET_MS });
    const protocol = rawResponse(previewRequest);
    assert.equal(rawRequest(previewRequest).receiptId, receipt.receiptId);
    assert.equal(protocol?.receiptId, receipt.receiptId, 'Native preview response must bind to the exact reconcile receipt');
    assert.equal(protocol?.outputId, outputId, 'Native preview response must bind to the exact saved output');
    assert.equal(protocol?.rowCount, expectedCount, 'Current-draft preview protocol row count differs from the raw oracle');
    assert.equal(protocol?.rows?.length, expectedCount, 'Current-draft preview omitted rows from the exact native response');
    const protocolColumn = protocol.columns?.find(column => column.label === 'Medication FHIR resource ID');
    assert(protocolColumn, 'Current-draft preview omitted the Medication identity column');
    const protocolValues = protocol.rows.map(row => row[protocolColumn.column]);
    const expectedSorted = expectedValues.map(value => JSON.stringify(value)).sort();
    assert.deepEqual(protocolValues.map(value => JSON.stringify(value)).sort(), expectedSorted,
      'Current-draft preview protocol values differ from the raw Medication membership oracle');
    const protocolIdentities = protocol.rows.map(row => row.__loom_row_id);
    assert(protocolIdentities.every(identity => typeof identity === 'string' && identity.length > 0), 'Native preview rows must retain row identities');
    assert.equal(new Set(protocolIdentities).size, protocolIdentities.length, 'Native preview row identities must be unique');
    const expectedDraftVersion = String(draft.draftVersion);
    const expectedDraftDigest = draft.draftDigest;
    await wait(([count, receiptId, output, version, digest]) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const preview = document.querySelector('[data-testid="construction-preview"]');
      return Boolean(preview?.dataset.previewStatus === 'ready' && preview.dataset.previewReceiptId === receiptId &&
        preview.dataset.previewOutputId === output && preview.dataset.currentDraftVersion === version &&
        preview.dataset.currentDraftDigest === digest && table && table.getAttribute('aria-rowcount') === String(count + 1) &&
        table.getAttribute('aria-colcount') === '1' &&
        !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview did not complete'));
    }, [expectedCount, receipt.receiptId, outputId, expectedDraftVersion, expectedDraftDigest]);
    const visible = await cda.inspect(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const headers = [...(table?.querySelectorAll('[role="columnheader"]') ?? [])].map(node => node.textContent.trim());
      const rows = [...(table?.querySelectorAll('[role="row"]') ?? [])].slice(1).map(row => {
        const identityLabel = row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label') ?? '';
        const rowNumber = Number(/^Inspect row (\d+) identity$/.exec(identityLabel)?.[1] ?? 0);
        const cell = row.querySelector('[role="cell"]');
        const titledValue = cell?.querySelector('[title]');
        return { rowNumber, identityLabel, text: cell?.textContent.trim() ?? '', title: titledValue?.getAttribute('title') ?? null };
      });
      return { headers, rows, rowCount: Number(table?.getAttribute('aria-rowcount') ?? 0) - 1,
        columnCount: Number(table?.getAttribute('aria-colcount') ?? 0) };
    });
    assert.deepEqual(visible.headers, ['Medication FHIR resource ID']);
    assert.equal(visible.rowCount, expectedCount);
    assert(visible.rows.length <= expectedCount, 'Virtualized DOM cannot expose more rows than the complete preview');
    assert(expectedCount === 0 || visible.rows.length > 0, 'A nonempty preview must render at least one visible row');
    const visibleRowNumbers = visible.rows.map(row => row.rowNumber);
    assert.equal(new Set(visibleRowNumbers).size, visibleRowNumbers.length, 'Visible virtual rows must have unique native row indices');
    for (const row of visible.rows) {
      assert(row.rowNumber >= 1 && row.rowNumber <= expectedCount, 'Visible virtual row index must belong to the complete preview');
      const expectedDisplay = protocolValues[row.rowNumber - 1] === null ? '—' : String(protocolValues[row.rowNumber - 1]);
      assert.equal(row.text, expectedDisplay, 'Visible row text must match its exact index in the raw-proven preview');
      assert.equal(row.title, expectedDisplay, 'Nested cell title must preserve the displayed raw-proven value');
    }
    return { ...visible, protocolValues, protocolRowIDs: protocolIdentities, receiptId: receipt.receiptId,
      reconcileRequest, previewRequestStatus: previewRequest.status, previewRequest, protocol };
  };
  const proposalEvidence = async (expectedStatus = 'ready') => {
    await wait(([status]) => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === status, [expectedStatus]);
    return cda.inspect(() => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return { status: panel?.dataset.proposalStatus,
        headers: [...(document.querySelector('[data-testid="construction-proposal-preview"] thead')?.querySelectorAll('th') ?? [])].map(cell => cell.firstElementChild?.textContent.trim() ?? ''),
        rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row => [...row.querySelectorAll('td')].map(cell => ({ text: cell.textContent.trim(), title: cell.getAttribute('title') }))),
        text: panel?.innerText };
    });
  };
  const rawRequest = entry => tracker.rawRequestBody(entry);
  const rawResponse = entry => tracker.rawResponseBody(entry);
  const findProposal = async (predicate, fromIndex = 0) => tracker.waitFor(entry => entry.method === 'POST' && entry.path === `${authoringPath}/construction-proposals` && predicate(rawRequest(entry), rawResponse(entry)), { fromIndex, timeoutMs: ACTION_BUDGET_MS });

  try {
    const scanned = readOracle();
    const positive = scanned.find(root => root.medications.length > 0 && root.medications.length <= 24);
    const noMatches = scanned.filter(root => root.medications.length === 0).slice(0, positive ? 1 : 2);
    const selected = positive ? [positive, ...noMatches] : noMatches;
    assert.equal(scanned.length, 1000, 'Bounded oracle must scan exactly the first 1,000 project/generation Specimens');
    assert(selected.length >= 1 && selected.length <= 2, 'Oracle must select one positive witness when present plus at most one no-match, or two no-match parents');
    const selectedRefs = selected.map(root => ({ project, generation, resourceType: 'Specimen', id: root.specimen.id })).sort((a, b) => a.id.localeCompare(b.id));
    const expectedValues = selected.flatMap(root => root.medications.length ? root.medications.map(item => item.id) : [null]);
    const excludedRootCount = selected.filter(root => root.medications.length === 0).length;
    report.rawOracle = {
      project, generation, scannedSpecimenCount: scanned.length,
      selectedRoots: selected.map(root => ({ id: root.specimen.id, _id: root.specimen._id, medicationIDs: root.medications.map(item => item.id) })),
      positiveWitnessWithinBound: Boolean(positive),
      unmatchedRootCountWithinBound: scanned.filter(root => root.medications.length === 0).length,
      expectedMedicationIDs: expectedValues.filter(value => value !== null).sort(),
      expectedVisibleMedicationValues: expectedValues,
      expectedPreserveRowCount: expectedValues.length,
      expectedExcludeRowCount: expectedValues.length - excludedRootCount,
      route: ROUTE_STEPS.map(([fromResourceType, toResourceType, relationship, storageDirection]) => ({ fromResourceType, toResourceType, relationship, storageDirection })),
      bound: { rootLimit: 1000, distinctMedicationLimitPerRoot: 26 },
    };
    check(0, 'correctness', { ...report.rawOracle, queryHashEvidence: 'The embedded bounded AQL checks project+generation on all five edges and all six endpoint documents.' });

    await api(explorerRoot, { name: explorer, title: 'Zero-column Medication related expansion QA' });
    builder = await api(`${authoringPath}/builder`);
    assert.equal(builder.catalog.generation, generation);
    assert.equal(builder.lifecycleState, 'NEW', 'A fresh interactive Explorer must begin in the NEW Builder lifecycle');
    assert.equal(builder.workspace, null, 'A fresh interactive Explorer has no persisted workspace before its first command');
    const specimenNode = builder.catalog.nodes.find(node => node.resourceType === 'Specimen');
    assert(specimenNode, 'CDA catalog must expose Specimen as a root');
    await command([{ type: 'CREATE_TABLE', title: 'Zero-column Specimen', rootNodeId: specimenNode.nodeId }]);
    assert.equal(builder.lifecycleState, 'READY', 'CREATE_TABLE must initialize the first persisted Builder workspace');
    assert(builder.workspace, 'CREATE_TABLE must return the initialized Builder workspace');
    assert.equal(builder.workspace.documents.length, 1, 'CREATE_TABLE must produce exactly one isolated Specimen document');
    const initial = builder.workspace.documents[0];
    assert(initial, 'QA table creation failed');
    outputId = initial.output.id;
    assert.deepEqual(initial.columns, [], 'Starting table must have zero public columns');
    assert(initial.population == null, 'Starting table must have no population attachment before scoped membership is seeded');
    assert.deepEqual(initial.construction?.steps ?? [], []);
    selection = await api(`${explorerPath}/selections`, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: explorer,
      source: { kind: 'resources', resources: { refs: selectedRefs } },
    });
    const selectionPage = await api(`${explorerPath}/selections/${encodeURIComponent(selection.id)}?limit=100`);
    const actualRefs = selectionPage.members.map(member => member.ref).sort((a, b) => a.id.localeCompare(b.id));
    assert.equal(selection.project, project);
    assert.equal(selection.generation, generation);
    assert.equal(selection.resourceType, 'Specimen');
    assert.equal(selection.scopeDigest, builder.catalog.authorizationScopeDigest);
    assert.equal(selection.memberCount, selectedRefs.length);
    assert.equal(selectionPage.revision.id, selection.id);
    assert.deepEqual(actualRefs, selectedRefs, 'Immutable selected Specimen membership must exactly equal bounded raw oracle roots');
    const routeResponse = await api(`${authoringPath}/population-routes`, {
      snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
    });
    const direct = routeResponse.choices.find(choice => choice.route.length === 0);
    assert(direct, 'The exact selected Specimens must have a direct population route');
    await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
    assert.deepEqual(doc().columns, [], 'Population attachment must preserve the true zero-column root');
    assert.deepEqual(doc().population?.route ?? [], [], 'Selected Specimen roots must be attached directly, with no inferred edge');
    assert.equal(doc().population.selectionRevisionId, selection.id);
    report.seed = { outputId, selectionRevisionId: selection.id, selectedRefs: actualRefs, columns: doc().columns, population: doc().population };
    const originalPopulation = structuredClone(doc().population);
    const seedWorkspace = structuredClone(builder.workspace);
    const seedDraftVersion = builder.draftVersion;
    const seedDraftDigest = builder.draftDigest;
    check(1, 'persistence', { outputId, selectionRevisionId: selection.id, exactRawOracleRefs: actualRefs, publicColumnCount: 0, directRouteChoiceId: direct.routeChoiceId });

    tracker = cda.captureRequests(explorerPath, { responsePaths: /related-expand-choices|commands|construction-proposals|reconcile|preview/ });
    const openTable = async () => {
      await cda.navigate(`${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`);
      await wait(([id]) => Boolean(document.querySelector(`[data-testid="construction-table-${id}"]`)), [outputId]);
      await cda.click(`[data-testid="construction-table-${outputId}"]`);
      await wait(() => Boolean(document.querySelector('[data-testid="preview-table-scroll"]') &&
        document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes('Add a column to see your table.')));
      return cda.inspect(() => ({ prompt: document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.trim(),
        tableCount: document.querySelectorAll('[data-testid="preview-table-scroll"] [role="table"]').length }));
    };
    const zeroColumnScreen = await openTable(undefined);
    assert.equal(zeroColumnScreen.tableCount, 0);
    assert.match(zeroColumnScreen.prompt, /Add a column to see your table/);
    const editorStart = Date.now();
    await cda.click('[data-testid="construction-rows-settings-trigger"]');
    await wait(() => document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false);
    await cda.click('[data-testid="construction-action-related-rows"]');
    await wait(([panel]) => document.querySelector(`${panel} select[aria-label="Related record type"]`)?.disabled === false, [PANEL]);
    measured('native related-expansion editor discovery', editorStart);
    const targetStart = Date.now();
    const routeSearchFromIndex = cda.report.nativeRequests.length;
    await cda.selectOption(`${PANEL} select[aria-label="Related record type"]`, 'Medication');
    const routeSelector = `${PANEL} input[type="radio"][aria-label=${JSON.stringify(ROUTE_LABEL)}]`;
    const routeComplete = await tracker.waitFor(entry => entry.method === 'POST' && /related-expand-choices/.test(entry.path) &&
      rawRequest(entry)?.targetResourceType === 'Medication' && (rawResponse(entry)?.complete === true ||
        (rawResponse(entry)?.truncated === true && rawResponse(entry)?.nextCursor == null)),
    { fromIndex: routeSearchFromIndex, timeoutMs: ACTION_BUDGET_MS });
    assert.equal(routeComplete.status, 200);
    const routePages = cda.report.nativeRequests.slice(routeSearchFromIndex).filter(entry => entry.method === 'POST' &&
      /related-expand-choices/.test(entry.path) && rawRequest(entry)?.targetResourceType === 'Medication' && entry.status === 200);
    assert(routePages.length > 0 && routePages.at(-1) === routeComplete,
      'The native route scan must finish on a terminal complete or cursorless-truncated page before inspecting choices');
    for (let index = 0; index < routePages.length; index += 1) {
      const request = rawRequest(routePages[index]);
      const response = rawResponse(routePages[index]);
      assert.equal(response?.outputId, outputId);
      assert.equal(request?.targetResourceType, 'Medication');
      assert(response?.choices?.every(candidate => candidate.targetResourceType === 'Medication'),
        'Every native choice page must contain only the requested terminal Medication target');
      if (index === 0) assert.equal(request?.cursor, undefined, 'The first native route page must start without a cursor');
      else assert.equal(request?.cursor, rawResponse(routePages[index - 1])?.nextCursor,
        'Each native route page must follow the prior page cursor');
      if (index < routePages.length - 1) assert(response?.nextCursor, 'Every nonterminal route page must provide its next cursor');
      else {
        const complete = response?.complete === true;
        const terminalTruncated = response?.truncated === true && response?.nextCursor == null;
        assert(complete || terminalTruncated,
          'The terminal route page must be complete or explicitly truncated with no continuation cursor');
        if (complete) assert.equal(response?.nextCursor, undefined, 'A complete route page must not leave choices unobserved');
      }
    }
    const routeChoices = routePages.flatMap(entry => rawResponse(entry)?.choices ?? []);
    const exactRoutes = routeChoices.filter(choice => choice.route.length === 5 && choice.route.every((step, index) => {
      const [fromResourceType, toResourceType, relationship, storageDirection] = ROUTE_STEPS[index];
      return step.fromResourceType === fromResourceType && step.toResourceType === toResourceType &&
        step.relationship === relationship && step.storageDirection === storageDirection;
    }));
    const terminalRouteResponse = rawResponse(routeComplete);
    report.routeEnumeration = {
      pageCount: routePages.length,
      terminal: {
        complete: terminalRouteResponse.complete === true,
        truncated: terminalRouteResponse.truncated === true,
        hasNextCursor: terminalRouteResponse.nextCursor != null,
      },
      returnedChoiceCount: routeChoices.length,
      exactFiveHopMatchCount: exactRoutes.length,
      exactRouteObserved: exactRoutes.length > 0,
      exactRouteChoices: exactRoutes.map(candidate => ({
        targetResourceType: candidate.targetResourceType,
        route: candidate.route.map(step => ({
          fromResourceType: step.fromResourceType,
          toResourceType: step.toResourceType,
          relationship: step.relationship,
          storageDirection: step.storageDirection,
        })),
      })),
      choices: routeChoices.map(candidate => ({
        targetResourceType: candidate.targetResourceType,
        route: candidate.route.map(step => ({
          fromResourceType: step.fromResourceType,
          toResourceType: step.toResourceType,
          relationship: step.relationship,
          storageDirection: step.storageDirection,
        })),
      })),
    };
    assert(exactRoutes.length > 0,
      'The terminal native response did not include the exact five-hop Medication route in its returned choices');
    const choice = exactRoutes[0];
    const routeEntry = routePages.find(entry => rawResponse(entry).choices.some(candidate => candidate.choiceId === choice.choiceId));
    const routeRequest = rawRequest(routeEntry);
    assert.equal(routeRequest.outputId, outputId);
    assert.equal(routeRequest.targetResourceType, 'Medication');
    assert.equal(routeRequest.snapshotToken, builder.catalog.snapshotToken);
    assert.equal(routeRequest.expectedDraftVersion, builder.draftVersion);
    assert.equal(routeRequest.expectedDraftDigest, builder.draftDigest);
    for (const entry of routePages) {
      const response = rawResponse(entry);
      assert.equal(response.snapshotToken, builder.catalog.snapshotToken);
      assert.equal(response.draftVersion, builder.draftVersion);
      assert.equal(response.draftDigest, builder.draftDigest);
    }
    const shortestRouteLength = Math.min(...routeChoices.map(candidate => candidate.route.length));
    await revealRouteChoice(routeSelector, choice.route, shortestRouteLength);
    measured('native Medication route-choice discovery', targetStart);
    const proposalStartIndex = cda.report.nativeRequests.length;
    await cda.click(routeSelector);
    assert.equal(await cda.inspect(([panel]) => document.querySelector(`${panel} select[aria-label="If a current row has no matches"]`)?.value, [PANEL]), 'PRESERVE_PARENT');
    const proposal = await proposalEvidence('ready');
    measured('native Medication target selection through exact five-hop ready proposal', targetStart);
    let proposalRequest = await findProposal((body, response) => body?.outputId === outputId && body?.candidateConstruction?.steps?.some(step =>
      step.operation?.kind === 'RELATED_EXPAND' && step.operation.relatedExpand?.targetResourceType === 'Medication' &&
      step.operation.relatedExpand?.choiceId === choice.choiceId) && response?.proposalId, proposalStartIndex);
    assert.equal(proposalRequest.status, 200);
    let proposalBody = rawRequest(proposalRequest);
    let operationStep = proposalBody.candidateConstruction.steps.find(step => step.operation?.kind === 'RELATED_EXPAND');
    assert.equal(operationStep.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.equal(operationStep.operation.relatedExpand.anchorColumnId, '_key', 'Five-hop expansion must start at root identity when no public columns exist');
    assert.deepEqual(operationStep.operation.relatedExpand.route, choice.route);
    let output = operationStep.outputs.find(column => column.id === operationStep.operation.relatedExpand.relatedRecordColumnId);
    assert(output, 'RELATED_EXPAND must define the visible Medication identity output');
    let proposalPreview = rawResponse(proposalRequest).preview;
    assert.equal(proposalPreview?.rowCount, report.rawOracle.expectedPreserveRowCount);
    assert.deepEqual(proposalPreview?.rows?.map(row => row[output.name]).sort((a, b) => String(a).localeCompare(String(b))),
      [...report.rawOracle.expectedVisibleMedicationValues].sort((a, b) => String(a).localeCompare(String(b))));
    assert.equal(proposal.headers[0], 'Medication FHIR resource ID');
    const expectedCellTexts = report.rawOracle.expectedVisibleMedicationValues.map(value => value === null ? '—' : value).sort();
    assert.deepEqual(proposal.rows.map(row => row[0]?.text).sort(), expectedCellTexts,
      'PRESERVE_PARENT proposal must show exact distinct raw Medication IDs and one null ID for each unmatched parent');
    assert.deepEqual(proposal.rows.map(row => row[0]?.title).sort(), expectedCellTexts,
      'PRESERVE_PARENT proposal titles must show each exact Medication ID or the null marker');
    assert.equal(proposal.rows.filter(row => row[0]?.title === '—').length, excludedRootCount,
      'PRESERVE_PARENT proposal must retain every unmatched parent as one null target row');
    check(2, 'correctness', { routeChoiceId: choice.choiceId, route: choice.route, anchorColumnId: operationStep.operation.relatedExpand.anchorColumnId, targetResourceType: 'Medication' });
    check(3, 'correctness', { policy: operationStep.operation.relatedExpand.emptyPolicy, proposalRows: proposal.rows, matchedMedicationIDs: report.rawOracle.expectedMedicationIDs });

    const cancelStart = Date.now();
    await cda.click('[data-testid="construction-cancel-proposal"]');
    await wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
    await wait(() => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes('Add a column to see your table.'));
    measured('native Cancel to restored zero-column source state', cancelStart);
    builder = await api(`${authoringPath}/builder`);
    assert.deepEqual(builder.workspace, seedWorkspace, 'Cancel must leave the selected Specimen table and direct population unchanged');
    assert.equal(builder.draftVersion, seedDraftVersion, 'Cancel must preserve the saved draft version');
    assert.equal(builder.draftDigest, seedDraftDigest, 'Cancel must preserve the saved draft digest');
    assert.deepEqual(doc().construction?.steps ?? [], []);
    assert.deepEqual(doc().columns, []);
    check(4, 'persistence', { draftVersion: seedDraftVersion, draftDigest: seedDraftDigest, workspaceUnchanged: true, columns: doc().columns, population: doc().population });

    const reopenStart = Date.now();
    await cda.click('[data-testid="construction-rows-settings-trigger"]');
    await wait(() => document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false);
    await cda.click('[data-testid="construction-action-related-rows"]');
    await wait(([panel]) => document.querySelector(`${panel} select[aria-label="Related record type"]`)?.disabled === false, [PANEL]);
    measured('native RelatedExpand editor reopen after Cancel', reopenStart);
    const reopenedTargetStart = Date.now();
    await cda.selectOption(`${PANEL} select[aria-label="Related record type"]`, 'Medication');
    await revealRouteChoice(routeSelector, choice.route, shortestRouteLength);
    measured('reopened Medication route-choice discovery', reopenedTargetStart);
    const reopenedProposalFromIndex = cda.report.nativeRequests.length;
    await cda.click(routeSelector);
    const reopenedProposal = await proposalEvidence('ready');
    measured('reopened Medication target selection through exact five-hop ready proposal', reopenedTargetStart);
    const reopenedProposalRequest = await findProposal((body, response) => body?.outputId === outputId && body?.candidateConstruction?.steps?.some(step =>
      step.operation?.kind === 'RELATED_EXPAND' && step.operation.relatedExpand?.choiceId === choice.choiceId &&
      step.operation.relatedExpand?.emptyPolicy === 'PRESERVE_PARENT') && response?.proposalId, reopenedProposalFromIndex);
    assert.equal(reopenedProposalRequest.status, 200);
    proposalRequest = reopenedProposalRequest;
    proposalBody = rawRequest(proposalRequest);
    operationStep = proposalBody.candidateConstruction.steps.find(step => step.operation?.kind === 'RELATED_EXPAND');
    output = operationStep.outputs.find(column => column.id === operationStep.operation.relatedExpand.relatedRecordColumnId);
    proposalPreview = rawResponse(proposalRequest).preview;
    assert.deepEqual(operationStep.operation.relatedExpand.route, choice.route);
    assert.equal(operationStep.operation.relatedExpand.anchorColumnId, '_key');
    assert.equal(operationStep.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.deepEqual(reopenedProposal.rows, proposal.rows, 'Reopening the same route after Cancel must produce the same exact proposal rows');

    const applyRequestFromIndex = cda.report.nativeRequests.length;
    const applyStart = Date.now();
    await cda.click('[data-testid="construction-apply-proposal"]');
    await wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
    const appliedProposalId = rawResponse(proposalRequest)?.proposalId;
    assert(appliedProposalId, 'Native proposal response must provide the exact ID submitted by Apply');
    const applyCommand = await tracker.waitFor(entry => entry.method === 'POST' && entry.path === `${authoringPath}/commands` &&
      rawRequest(entry)?.commands?.some(item => item.type === 'APPLY_CONSTRUCTION_PROPOSAL' &&
        item.outputId === outputId && item.proposalId === appliedProposalId),
    { fromIndex: applyRequestFromIndex, timeoutMs: ACTION_BUDGET_MS });
    assert.equal(applyCommand.status, 200);
    builder = await api(`${authoringPath}/builder`);
    const savedStep = doc().construction.steps.find(step => step.operation.kind === 'RELATED_EXPAND');
    assert(savedStep);
    assert.deepEqual(savedStep.operation.relatedExpand.route, choice.route,
      'The saved RelatedExpand route must preserve the exact catalog route objects chosen in the native editor');
    assert.equal(savedStep.operation.relatedExpand.anchorColumnId, '_key');
    assert.equal(savedStep.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.deepEqual(doc().columns, [], 'RELATED_EXPAND must not synthesize public source columns');
    assert.equal(doc().population.selectionRevisionId, selection.id);
    const savedOutput = savedStep.outputs.find(column => column.id === savedStep.operation.relatedExpand.relatedRecordColumnId);
    assert(savedOutput);
    const appliedRows = await nativeRows(report.rawOracle.expectedVisibleMedicationValues, {
      fromIndex: applyRequestFromIndex, draft: builder,
    });
    measured('native Apply to actual Medication table preview', applyStart);
    assert.equal(appliedRows.headers[0], savedOutput.label);
    assert.equal(appliedRows.columnCount, 1);
    assert.equal(appliedRows.rowCount, report.rawOracle.expectedPreserveRowCount);
    assert.deepEqual(appliedRows.protocolValues.map(value => value === null ? '—' : String(value)).sort(), expectedCellTexts);
    const appliedPreviewRequest = appliedRows.previewRequest;
    assert.equal(rawResponse(appliedPreviewRequest).rowCount, report.rawOracle.expectedPreserveRowCount);
    check(5, 'persistence', { savedRoute: savedStep.operation.relatedExpand.route, policy: savedStep.operation.relatedExpand.emptyPolicy,
      previewRows: appliedRows.rows, previewRowCount: appliedRows.rowCount, requestStatus: appliedPreviewRequest.status,
      receiptId: appliedRows.receiptId, reconcileRequestId: appliedRows.reconcileRequest.requestId,
      previewRequestId: appliedPreviewRequest.requestId, savedDraftVersion: builder.draftVersion, savedDraftDigest: builder.draftDigest });

    const reloadStart = Date.now();
    const reloadFromIndex = cda.report.nativeRequests.length;
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
    await wait(() => Boolean(document.querySelector('[data-testid="construction-workspace"]')));
    await cda.click(`[data-testid="construction-table-${outputId}"]`);
    builder = await api(`${authoringPath}/builder`);
    const reloadedRows = await nativeRows(report.rawOracle.expectedVisibleMedicationValues, {
      fromIndex: reloadFromIndex, draft: builder,
    });
    measured('reload to restored Medication output rows', reloadStart);
    const reloadStep = doc().construction.steps.find(step => step.id === savedStep.id);
    assert(reloadStep);
    assert.deepEqual(reloadStep.operation.relatedExpand.route, savedStep.operation.relatedExpand.route);
    assert.equal(reloadStep.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.deepEqual(reloadedRows.protocolValues, appliedRows.protocolValues);
    assert.deepEqual([...reloadedRows.protocolRowIDs].sort(), [...appliedRows.protocolRowIDs].sort(),
      'Reload must restore the exact unique native row identities for each Medication/null result');
    check(6, 'persistence', { route: reloadStep.operation.relatedExpand.route, policy: reloadStep.operation.relatedExpand.emptyPolicy,
      previewRows: reloadedRows.rows, previewValues: reloadedRows.protocolValues, previewRowCount: reloadedRows.rowCount,
      receiptId: reloadedRows.receiptId, reconcileRequestId: reloadedRows.reconcileRequest.requestId,
      previewRequestId: reloadedRows.previewRequest.requestId, savedDraftVersion: builder.draftVersion, savedDraftDigest: builder.draftDigest });

    const editProposalFromIndex = cda.report.nativeRequests.length;
    const editOpenStart = Date.now();
    await cda.click(`[data-testid="construction-history-step-${savedStep.id}"]`);
    await wait(([id]) => document.querySelector(`[data-testid="construction-edit-step-${id}"]`)?.disabled === false, [savedStep.id]);
    await cda.click(`[data-testid="construction-edit-step-${savedStep.id}"]`);
    await wait(([panel]) => document.querySelector(`${panel} select[aria-label="If a current row has no matches"]`)?.disabled === false, [PANEL]);
    measured('open saved RelatedExpand editor', editOpenStart);
    assert.equal(await cda.inspect(([panel]) => document.querySelector(`${panel} select[aria-label="If a current row has no matches"]`)?.value, [PANEL]), 'PRESERVE_PARENT');
    const policySelector = `${PANEL} select[aria-label="If a current row has no matches"]`;
    const policyChoiceStart = Date.now();
    await cda.selectOption(policySelector, 'EXCLUDE');
    const excludedProposal = await proposalEvidence('ready');
    const excludeProposalRequest = await findProposal((body, response) => body?.outputId === outputId && body?.candidateConstruction?.steps?.some(step =>
      step.id === savedStep.id && step.operation?.relatedExpand?.emptyPolicy === 'EXCLUDE') && response?.proposalId, editProposalFromIndex);
    assert.equal(rawResponse(excludeProposalRequest).preview?.rowCount, report.rawOracle.expectedExcludeRowCount);
    assert.equal(excludedProposal.rows.length, report.rawOracle.expectedExcludeRowCount);
    measured('EXCLUDE policy selection to exact automatic proposal', policyChoiceStart);
    const excludePreview = rawResponse(excludeProposalRequest).preview;
    const excludedExpectedValues = report.rawOracle.expectedVisibleMedicationValues.filter(value => value !== null);
    assert.equal(excludePreview.rowCount, excludedExpectedValues.length);
    assert.deepEqual(excludePreview.rows.map(row => row[savedOutput.name]).sort(), [...excludedExpectedValues].sort());
    const excludeApplyFromIndex = cda.report.nativeRequests.length;
    const excludeApplyStart = Date.now();
    await cda.click('[data-testid="construction-apply-proposal"]');
    await wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
    builder = await api(`${authoringPath}/builder`);
    const excludedRows = await nativeRows(excludedExpectedValues, {
      fromIndex: excludeApplyFromIndex, draft: builder,
    });
    measured('EXCLUDE Apply to actual table preview', excludeApplyStart);
    assert.equal(excludedRows.rowCount, report.rawOracle.expectedExcludeRowCount);
    assert.equal(excludedRows.columnCount, 1);
    const excludedStep = doc().construction.steps.find(step => step.id === savedStep.id);
    assert.equal(excludedStep.operation.relatedExpand.emptyPolicy, 'EXCLUDE');

    const excludeReloadStart = Date.now();
    const excludeReloadFromIndex = cda.report.nativeRequests.length;
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
    await wait(() => Boolean(document.querySelector('[data-testid="construction-workspace"]')));
    await cda.click(`[data-testid="construction-table-${outputId}"]`);
    builder = await api(`${authoringPath}/builder`);
    const excludedReload = await nativeRows(excludedExpectedValues, {
      fromIndex: excludeReloadFromIndex, draft: builder,
    });
    measured('reload after EXCLUDE edit', excludeReloadStart);
    assert.equal(doc().construction.steps.find(step => step.id === savedStep.id)?.operation.relatedExpand.emptyPolicy, 'EXCLUDE');
    assert.deepEqual(excludedReload.protocolValues, excludedRows.protocolValues);
    assert.deepEqual(excludedReload.protocolRowIDs, excludedRows.protocolRowIDs);
    check(7, 'correctness', { editedPolicy: excludedStep.operation.relatedExpand.emptyPolicy, proposalRowCount: report.rawOracle.expectedExcludeRowCount, actualTableRowCount: excludedRows.rowCount, reloadTableRowCount: excludedReload.rowCount });

    const removeFromIndex = cda.report.nativeRequests.length;
    const removeEditorStart = Date.now();
    await cda.click(`[data-testid="construction-history-step-${savedStep.id}"]`);
    await wait(([id]) => document.querySelector(`[data-testid="construction-remove-step-${id}"]`)?.disabled === false, [savedStep.id]);
    measured('open saved RelatedExpand removal controls', removeEditorStart);
    const removeProposalStart = Date.now();
    await cda.click(`[data-testid="construction-remove-step-${savedStep.id}"]`);
    await proposalEvidence('ready');
    const removeProposal = await findProposal((body, response) => body?.outputId === outputId && body?.removeStepIds?.includes(savedStep.id) && response?.proposalId, removeFromIndex);
    assert(!rawRequest(removeProposal).candidateConstruction.steps.some(step => step.id === savedStep.id));
    measured('native Remove to zero-column restoration proposal', removeProposalStart);
    const removeApplyStart = Date.now();
    await cda.click('[data-testid="construction-apply-proposal"]');
    await wait(() => !document.querySelector('[data-testid="construction-proposal-panel"]'));
    await wait(() => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes('Add a column to see your table.'));
    measured('native Remove Apply to restored zero-column UI', removeApplyStart);
    builder = await api(`${authoringPath}/builder`);
    const restored = doc();
    assert.deepEqual(restored.columns, [], 'Removing RELATED_EXPAND must restore the zero-column public shape');
    assert.deepEqual(restored.construction?.steps ?? [], [], 'Removing RELATED_EXPAND must restore the original empty construction');
    assert.deepEqual(restored.population, originalPopulation);
    assert.equal(restored.population.selectionRevisionId, selection.id);
    assert.equal(restored.population.route.length, 0);
    const restoredPreviewPrompt = await cda.inspect(() => ({ text: document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.trim(),
      tableCount: document.querySelectorAll('[data-testid="preview-table-scroll"] [role="table"]').length,
      staleMedicationHeading: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].some(node => /Medication/.test(node.textContent)) }));
    assert.equal(restoredPreviewPrompt.tableCount, 0, 'No table cell exists after restoring a zero-column source');
    assert.equal(restoredPreviewPrompt.staleMedicationHeading, false);
    assert.match(restoredPreviewPrompt.text, /Add a column to see your table/);

    const finalReloadStart = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
    await wait(() => Boolean(document.querySelector('[data-testid="construction-workspace"]')));
    await cda.click(`[data-testid="construction-table-${outputId}"]`);
    await wait(() => document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes('Add a column to see your table.'));
    measured('reload to restored zero-column Specimen table state', finalReloadStart);
    builder = await api(`${authoringPath}/builder`);
    const finalDocument = doc();
    assert.deepEqual(finalDocument.columns, []);
    assert.deepEqual(finalDocument.construction?.steps ?? [], []);
    assert.equal(finalDocument.population.selectionRevisionId, selection.id);
    const finalSelection = await api(`${explorerPath}/selections/${encodeURIComponent(selection.id)}?limit=100`);
    assert.deepEqual(finalSelection.members.map(member => member.ref).sort((a, b) => a.id.localeCompare(b.id)), selectedRefs);
    assert.deepEqual(finalDocument.population, originalPopulation);
    check(8, 'persistence', { columns: finalDocument.columns, constructionSteps: finalDocument.construction?.steps ?? [], population: finalDocument.population, selectedRefs: finalSelection.members.map(member => member.ref), restoredUI: restoredPreviewPrompt });
    check(9, 'performance', { budgetMs: ACTION_BUDGET_MS, checkpoints: report.checkpoints });

    await tracker.flush();
    cda.includeBrowserDiagnostics();
    assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected JavaScript errors');
    assert.deepEqual(report.errors, [], 'Unexpected native browser HTTP or workflow errors');
    report.lifecycle.status = 'passed';
  } catch (error) {
    fatal = error;
    report.lifecycle.status = 'failed';
    report.lifecycle.failure = { message: String(error?.message ?? error), stack: error?.stack,
      bodyText: await cda.inspect(() => document.body.innerText.slice(-12_000)).catch(inspectError => String(inspectError)),
      builder: outputId ? await api(`${authoringPath}/builder`).catch(apiError => ({ error: String(apiError) })) : undefined };
    cda.includeBrowserDiagnostics();
  } finally {
    await tracker?.flush().catch(() => undefined);
    report.lifecycle.nativeRequests = cda.report.nativeRequests.map(entry => ({ method: entry.method, path: entry.path, status: entry.status, body: entry.body, response: entry.response }));
    report.lifecycle.checkpoints = report.checkpoints;
    await cda.attachReport('zero-column-related-medication.json', report);
  }
  if (fatal) throw fatal;
  return report;
}
