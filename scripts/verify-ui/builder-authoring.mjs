import { runRepeatedEmpty } from './builder-repeated.mjs';
import { runCohortExpand } from './builder-cohort-expand.mjs';
import { browserURL, executeScenario } from './common.mjs';
import { createHash, randomUUID } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { createInterface } from 'node:readline';
import { join } from 'node:path';
import assert from 'node:assert/strict';
import { assertCohortPatientIDColumns } from './cohort-identities.mjs';
import { runPlaywrightCase } from './playwright-case.mjs';
import { runPlaywrightAuthoring } from './playwright-authoring.mjs';
import { runPlaywrightSuggestions } from './playwright-suggestions.mjs';

const readPatientOracle = async fixtureDir => {
  const sourcePath = join(fixtureDir, 'Patient.ndjson');
  const source = createReadStream(sourcePath);
  const hash = createHash('sha256');
  source.on('data', chunk => hash.update(chunk));
  const lines = createInterface({ input: source, crlfDelay: Infinity });
  const sourceIDs = [];
  const seenIDs = new Set();
  let patientRecordCount = 0;
  let lineNumber = 0;
  for await (const line of lines) {
    lineNumber += 1;
    if (!line.trim()) continue;
    patientRecordCount += 1;
    if (sourceIDs.length >= 2) continue;
    let patient;
    try { patient = JSON.parse(line); }
    catch (error) { throw new Error(`Invalid Patient.ndjson JSON at line ${lineNumber}: ${error.message}`); }
    if (typeof patient.id !== 'string' || !patient.id || patient.resourceType && patient.resourceType !== 'Patient') continue;
    if (seenIDs.has(patient.id)) continue;
    seenIDs.add(patient.id);
    sourceIDs.push(patient.id);
  }
  if (sourceIDs.length !== 2) throw new Error(`Independent Patient source must contain two distinct IDs; found ${sourceIDs.length}`);
  return { sourcePath, sha256: hash.digest('hex'), patientRecordCount, sourceIDs };
};

const runCohortRecode = context => runPlaywrightCase(context, 'builder-authoring', 'cohort-recode', async ({ page, report, check, action }) => {
  const oracleBefore = await readPatientOracle(context.target.fixtureDir);
  const sourceIDs = oracleBefore.sourceIDs;
  const title = `Verify ${context.runID.slice(-10)} cohort recode`;
  const bootstrapPath = `/api/v1/projects/${encodeURIComponent(context.target.fixtureProject)}/explorers/${encodeURIComponent(context.target.bootstrapExplorerId)}/authoring/v2/construction-capabilities`;
  const bootstrapRequests = new Map();
  const bootstrapRequestEvidence = [];
  report.target.bootstrapCapabilitiesRequests = bootstrapRequestEvidence;
  let requestSequence = 0;
  let explorerCreationStarted = false;
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.origin !== new URL(context.target.uiUrl).origin || url.pathname !== bootstrapPath || request.method() !== 'POST') return;
    const observed = {
      identity: `bootstrap-capabilities-${++requestSequence}`,
      requestObjectIdentity: `playwright-request-${requestSequence}`,
      origin: url.origin, path: url.pathname, method: request.method(),
      beganBeforeExplorerCreation: !explorerCreationStarted, phase: explorerCreationStarted ? 'after-explorer-creation-started' : 'bootstrap-builder-before-explorer-creation',
      requestId: request.headers()['x-request-id'] ?? null, failure: null, status: null,
    };
    bootstrapRequests.set(request, observed);
    bootstrapRequestEvidence.push(observed);
  });
  page.on('response', response => {
    const observed = bootstrapRequests.get(response.request());
    if (!observed) return;
    observed.status = response.status();
    observed.requestId ??= response.headers()['x-request-id'] ?? null;
  });
  page.on('requestfailed', request => {
    const observed = bootstrapRequests.get(request);
    if (observed) observed.failure = request.failure()?.errorText ?? 'unknown request failure';
  });
  const bootstrapResponsePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === new URL(context.target.uiUrl).origin
      && url.pathname === bootstrapPath && response.request().method() === 'POST';
  });
  await page.goto(browserURL(context.target, context.target.fixtureProject, context.target.bootstrapExplorerId, 'builder'),
    { waitUntil: 'domcontentloaded' });
  const bootstrapResponse = await bootstrapResponsePromise;
  const bootstrapResponseBody = await bootstrapResponse.json();
  const bootstrapRequest = bootstrapRequests.get(bootstrapResponse.request());
  assert(bootstrapRequest, 'bootstrap construction-capabilities response must match its exact observed Playwright Request');
  assert.equal(bootstrapResponse.status(), 200, 'bootstrap construction-capabilities request must complete successfully before Explorer creation');
  report.target.bootstrapCapabilitiesRequest = {
    ...bootstrapRequest,
    responseBodyReadable: Boolean(bootstrapResponseBody),
  };
  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible' });
  explorerCreationStarted = true;
  await action('open Explorer creation', newExplorer, () => newExplorer.click(), {
    after: async () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
  });
  const explorerName = page.locator('#new-explorer-name');
  await action('name Explorer', explorerName, () => explorerName.fill(title), { editable: true });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create blank Explorer', createBlank, () => createBlank.click(), {
    after: async () => page.waitForFunction(expectedTitle => {
      const select = document.querySelector('select[aria-label="Explorer"]');
      return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
    }, title),
  });
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  const explorer = await explorerControl.inputValue();
  assert(explorer && explorer !== context.target.bootstrapExplorerId, 'cohort recode requires a newly created Explorer');
  report.target.explorer = explorer;

  const tableName = page.locator('#first-table-name');
  await action('name Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await action('choose Patient rows', choosePatients, () => choosePatients.click(), {
    after: async () => page.waitForFunction(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return table && Number(table.getAttribute('aria-rowcount')) > 1
        && !document.body.innerText.includes('Loading your table…');
    }),
  });
  const previewTable = page.getByTestId('preview-table-scroll').getByRole('table');
  await previewTable.waitFor({ state: 'visible' });

  const project = context.target.fixtureProject;
  const generation = context.target.fixtureGeneration;
  // Selection revisions expose canonical program/project IDs; fixture storage uses the legacy alias.
  const selectionProject = project.replace('-', '/');
  const apiRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
  const requestJSON = async (path, body) => {
    const response = await fetch(context.target.apiUrl + path, {
      method: body ? 'POST' : 'GET',
      headers: { 'Content-Type': 'application/json' },
      ...(body ? { body: JSON.stringify(body) } : {}),
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    if (!response.ok) throw new Error(`Fixture cohort setup ${path} returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 900)}`);
    return { status: response.status, value };
  };
  const readBuilder = async () => (await requestJSON(`${apiRoot}/authoring/v2/builder`)).value;
  let builder = await readBuilder();
  assert.equal(builder.catalog?.generation, generation, 'fixture Builder must expose the exact requested generation');
  assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest,
    'fixture Builder must expose its current snapshot and authorization scope');
  assert(builder.workspace?.documents?.length, 'new Explorer Builder must return the Patient document');
  const refs = sourceIDs.map(id => ({ project: selectionProject, generation, resourceType: 'Patient', id }));
  report.target.fixtureRawOracle = {
    path: oracleBefore.sourcePath, sha256: oracleBefore.sha256, sourcePatientCount: oracleBefore.patientRecordCount,
    project: selectionProject, storageProject: project, generation, resourceType: 'Patient', sourceIDs,
    rawMemberValues: sourceIDs,
    sharedCategory: 'Shared fixture category',
  };
  const selection = (await requestJSON(`${apiRoot}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `verify-cohort-recode-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs } },
  })).value;
  assert.equal(selection.project, selectionProject, 'selection must retain the canonical fixture project');
  assert.equal(selection.generation, generation, 'selection must retain the exact fixture generation');
  assert.equal(selection.resourceType, 'Patient', 'selection must retain the exact resource type');
  assert.equal(selection.scopeDigest, builder.catalog.authorizationScopeDigest, 'selection must retain the authorized Builder scope');
  assert.equal(selection.memberCount, sourceIDs.length, 'selection must contain exactly two literal fixture Patients');

  const selectionPage = (await requestJSON(`${apiRoot}/selections/${encodeURIComponent(selection.id)}?limit=100`)).value;
  const selectedRefs = (selectionPage.members ?? []).map(member => member.ref)
    .map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  const expectedRefs = refs.map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  assert.equal(selectionPage.revision?.id, selection.id, 'selection read must return the exact revision');
  assert.equal(selectionPage.revision?.scopeDigest, builder.catalog.authorizationScopeDigest,
    'selection revision must retain its exact authorization scope');
  assert.equal(selectionPage.members?.length, sourceIDs.length, 'selection revision must contain exactly two members');
  assert.deepEqual(selectedRefs, expectedRefs, 'selection revision members must equal the two literal project/generation/resource/id refs');
  const memberKeys = selectionPage.members.map(member => member.memberKey);
  assert(memberKeys.every(key => typeof key === 'string' && key) && new Set(memberKeys).size === sourceIDs.length,
    'selection revision must issue two distinct opaque member keys');

  const cohort = (await requestJSON(`${apiRoot}/selections/${encodeURIComponent(selection.id)}/explicit-groups`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `verify-cohort-recode-group-${randomUUID()}`,
    groups: [{ id: 'verify-cohort', label: 'Two fixture Patients', ordinal: 0, memberIds: memberKeys }],
  })).value;
  assert.equal(cohort.sourceSelectionRevisionId, selection.id, 'named cohort must reference the exact selection revision');
  assert.equal(cohort.groupCount, 1, 'named cohort must contain exactly one group');
  assert.equal(cohort.memberCount, sourceIDs.length, 'named cohort must retain exactly the two selected members');
  assert.equal(cohort.groups?.[0]?.memberCount, sourceIDs.length, 'the sole named cohort group must contain both fixture members');
  report.target.fixtureCohort = {
    selectionRevisionId: selection.id, revisionId: cohort.revisionId,
    groupCount: cohort.groupCount, memberCount: cohort.memberCount, sourceIDs,
  };
  check('correctness', 'fixture cohort binds exactly the two independent Patient IDs', true,
    { sourceIDs, project: selectionProject, generation, revisionId: cohort.revisionId });

  const outputId = builder.workspace.documents.find(document => document.rootResourceType === 'Patient')?.output?.id;
  assert(outputId, 'fixture Patient table must have an output identity after reload');
  const tableControl = page.getByTestId(`construction-table-${outputId}`);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await tableControl.waitFor({ state: 'visible' });
  await action('open Patient table after reload', tableControl, () => tableControl.click(), {
    after: async () => page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible' }),
  });
  const rowSettings = page.getByTestId('construction-rows-settings-trigger');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-rows-settings-trigger"]');
    return button && !button.disabled;
  });
  await action('open row definition settings', rowSettings, () => rowSettings.click(), {
    after: async () => page.getByRole('combobox', { name: 'What should each row represent?' }).waitFor({ state: 'visible' }),
  });
  const cohortShape = `explicit:${cohort.revisionId}`;
  const rowShape = page.getByRole('combobox', { name: 'What should each row represent?', exact: true });
  await action('choose exact named cohort revision', rowShape, () => rowShape.selectOption(cohortShape), {
    after: async () => page.getByRole('combobox', { name: 'Unmatched record policy', exact: true }).waitFor({ state: 'visible' }),
  });
  const unmatchedPolicy = page.getByRole('combobox', { name: 'Unmatched record policy', exact: true });
  await action('set unmatched record policy to ERROR', unmatchedPolicy,
    () => unmatchedPolicy.selectOption(`${cohortShape}:ERROR`), {
      after: async () => page.waitForFunction(() => {
      const button = [...document.querySelectorAll('[aria-label="Row definition settings"] button')]
        .find(candidate => candidate.innerText.trim() === 'Apply row definition');
      return button && !button.disabled;
    }),
    });
  const groupPreview = page.locator('[aria-label="Row definition preview"]');
  const groupPreviewText = await groupPreview.innerText();
  const groupPreviewCounts = groupPreviewText.match(/(\d+)\s+rows\s*→\s*(\d+)\s+rows/);
  assert(groupPreviewCounts, `named cohort preview must expose its input and output row counts: ${groupPreviewText}`);
  const displayedInputRows = Number(groupPreviewCounts[1]);
  const namedCohortOutputRows = Number(groupPreviewCounts[2]);
  assert(displayedInputRows > 0 && namedCohortOutputRows === 1,
    `the exact two-member named cohort must preview as one output row; the input count may describe a bounded sample: ${groupPreviewText}`);
  report.target.namedCohortRowDefinitionPreview = {
    displayedInputRows,
    displayedInputIsSampled: /\bsampled\b/i.test(groupPreviewText),
    outputRows: namedCohortOutputRows,
    exactSelectedMemberCount: cohort.memberCount,
    sourceIDs,
  };
  const applyRows = page.getByRole('button', { name: 'Apply row definition', exact: true });
  await action('apply named cohort row definition', applyRows, () => applyRows.click(), {
    after: async () => page.waitForFunction(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return table?.getAttribute('aria-rowcount') === '2'
        && !document.body.innerText.includes('Loading your table…')
        && !document.body.innerText.includes('Preview failed:');
    }),
  });
  assert.equal(await previewTable.getAttribute('aria-rowcount'), '2', 'named cohort must render one group row and one header');
  builder = await readBuilder();
  let document = builder.workspace.documents.find(item => item.output.id === outputId);
  assert.equal(document?.rows?.groups?.source?.explicit?.revisionId, cohort.revisionId,
    'applying the named cohort must save its exact explicit-group revision');
  const legacyIDColumns = document.columns.filter(column => column.source?.kind === 'field' && column.source.field?.path === 'id');
  assert.equal(legacyIDColumns.length, 1, 'first-table Patient ID source must be a unique legacy identity column');
  const legacyIDColumn = legacyIDColumns[0];
  assert(legacyIDColumn.column,
    `first-table Patient ID must retain its physical column identity: ${JSON.stringify(legacyIDColumn)}`);
  const legacyIdentity = { column: legacyIDColumn.column, columnId: legacyIDColumn.columnId, source: legacyIDColumn.source };

  const previewEntries = [];
  const previewByRequest = new Map();
  const choiceProposalEntries = [];
  const commandEntries = [];
  const reconciliationEntries = [];
  const lifecycleByRequest = new Map();
  const networkWaiters = new Set();
  const signalNetworkChange = () => {
    for (const wake of networkWaiters) wake();
    networkWaiters.clear();
  };
  const entryFor = request => {
    let url;
    try { url = new URL(request.url()); } catch { return undefined; }
    if (url.origin !== new URL(context.target.uiUrl).origin || !url.pathname.startsWith(`${apiRoot}/`)) return undefined;
    const path = url.pathname;
    let body;
    try { body = request.postDataJSON(); } catch { body = undefined; }
    const requestObjectIdentity = `playwright-request-${++requestSequence}`;
    const requestId = request.headers()['x-request-id'] ?? null;
    const requestOrigin = url.origin;
    if (path.endsWith('/preview')) {
      const entry = { identity: `preview-${requestSequence}`, requestObjectIdentity, requestId, origin: requestOrigin, method: request.method(), path, outputId: body?.outputId, request, status: undefined, response: undefined };
      previewByRequest.set(request, entry);
      previewEntries.push(entry);
      return entry;
    }
    const collection = path.endsWith('/construction-choice-proposals') ? choiceProposalEntries
      : path.endsWith('/commands') ? commandEntries
        : path.endsWith('/reconcile') ? reconciliationEntries
          : undefined;
    if (!collection) return undefined;
    const entry = { identity: `${collection === choiceProposalEntries ? 'proposal' : collection === commandEntries ? 'command' : 'reconcile'}-${requestSequence}`, requestObjectIdentity, requestId, origin: requestOrigin, method: request.method(), path, body, request, startedAt: Date.now(), status: undefined, response: undefined };
    lifecycleByRequest.set(request, entry);
    collection.push(entry);
    return entry;
  };
  page.on('request', entryFor);
  page.on('response', response => {
    const request = response.request();
    const entry = previewByRequest.get(request) ?? lifecycleByRequest.get(request);
    if (!entry) return;
    entry.status = response.status();
    response.json().then(value => { entry.response = value; entry.completedAt = Date.now(); signalNetworkChange(); })
      .catch(error => { entry.responseReadError = String(error); signalNetworkChange(); });
  });
  const waitEntry = async (predicate, description) => {
    const deadline = Date.now() + 5000;
    while (true) {
      const entry = predicate();
      if (entry) return entry;
      const remaining = deadline - Date.now();
      if (remaining <= 0) throw new Error(`${description} did not complete within five seconds.`);
      await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => {
          networkWaiters.delete(wake);
          reject(new Error(`${description} did not complete within five seconds.`));
        }, remaining);
        const wake = () => { clearTimeout(timeout); resolve(); };
        networkWaiters.add(wake);
      });
    }
  };
  const waitPreview = (afterIndex, label) => waitEntry(() => {
    const entry = previewEntries.slice(afterIndex).findLast(item => item.outputId === outputId && item.status !== undefined && (item.response || item.responseReadError));
    if (!entry) return undefined;
    assert.equal(entry.status, 200, `${label} must return HTTP 200`);
    assert(!entry.responseReadError, `${label} response must be readable`);
    assert(Array.isArray(entry.response?.rows), `${label} must return typed Preview rows`);
    return entry;
  }, label);
  const waitAcceptedChoicePreview = async (proposalIndex, commandIndex, reconcileIndex, label) => {
    const proposalEntry = await waitEntry(() => choiceProposalEntries.slice(proposalIndex)
      .findLast(entry => entry.status !== undefined && (entry.response || entry.responseReadError)), `${label} choice proposal`);
    const proposal = proposalEntry.response;
    assert.equal(proposalEntry.status, 200, `${label} choice proposal must return HTTP 200`);
    assert(!proposalEntry.responseReadError, `${label} choice proposal response must be readable`);
    assert(proposal?.previewStatus === 'READY' && proposal.outputId === outputId && proposal.preview?.outputId === outputId &&
      proposal.preview?.receiptId !== undefined && Array.isArray(proposal.preview?.rows) && proposal.candidateWorkspaceDigest && proposal.snapshotToken,
    `${label} must capture a ready typed construction-choice Preview`);
    const commandEntry = await waitEntry(() => commandEntries.slice(commandIndex)
      .findLast(entry => entry.status !== undefined && (entry.response || entry.responseReadError) && entry.body?.commandId === proposal.commandId),
    `${label} matching construction command`);
    const commandResponse = commandEntry.response;
    const expectedColumns = proposal.candidateColumnIds ?? [];
    const addedColumns = (commandResponse?.results ?? [])
      .filter(result => result.type === 'COLUMN_ADDED' && result.outputId === outputId)
      .map(result => result.column).filter(Boolean);
    assert.equal(commandEntry.status, 200, `${label} command must return HTTP 200`);
    assert(!commandEntry.responseReadError, `${label} command response must be readable`);
    assert.equal(commandResponse?.commandId, proposal.commandId, `${label} command must retain the exact proposal command identity`);
    assert(expectedColumns.length > 0 && expectedColumns.every(column => addedColumns.includes(column)),
      `${label} command must add every exact proposed column`);
    const reconcileEntry = await waitEntry(() => reconciliationEntries.slice(reconcileIndex).findLast(entry =>
      entry.status !== undefined && (entry.response || entry.responseReadError) &&
      entry.body?.snapshotToken === proposal.snapshotToken &&
      entry.body?.draftVersion === commandResponse.draftVersion &&
      entry.body?.draftDigest === commandResponse.draftDigest), `${label} current reconcile receipt`);
    const receipt = reconcileEntry.response;
    assert.equal(reconcileEntry.status, 200, `${label} reconcile must return HTTP 200`);
    assert(!reconcileEntry.responseReadError, `${label} reconcile response must be readable`);
    assert.equal(receipt?.snapshotToken, proposal.snapshotToken, `${label} receipt must match proposal snapshot`);
    assert.equal(receipt?.intentDigest, proposal.candidateWorkspaceDigest, `${label} receipt must accept proposal workspace digest`);
    assert.equal(receipt?.receiptId, proposal.preview.receiptId, `${label} receipt must accept proposal preview receipt`);
    assert(receipt.outputs?.some(output => output.outputId === outputId), `${label} receipt must contain this output`);
    return {
      outputId,
      status: 200,
      response: proposal.preview,
      proposalCommandId: proposal.commandId,
      proposalRequest: proposalEntry.identity,
      commandRequest: commandEntry.identity,
      reconcileRequest: reconcileEntry.identity,
      acceptedByReceipt: { receiptId: receipt.receiptId, snapshotToken: receipt.snapshotToken, intentDigest: receipt.intentDigest },
      delivery: 'accepted-construction-choice-proposal',
    };
  };
  const rawIDs = [...sourceIDs].sort();
  const addColumns = page.getByTestId('construction-action-add-columns');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-add-columns"]');
    return button && !button.disabled;
  });
  await action('open Add columns', addColumns, () => addColumns.click(), {
    after: async () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' }),
  });
  const fieldsRelated = page.getByRole('button', { name: 'Fields and related data', exact: true });
  await action('choose Fields and related data', fieldsRelated, () => fieldsRelated.click(), {
    after: async () => page.getByTestId('construction-add-columns-source').waitFor({ state: 'visible' }),
  });
  const groupedPolicy = page.getByRole('combobox', { name: 'Values per grouped row', exact: true });
  const groupedPolicyOptions = await groupedPolicy.locator('option').evaluateAll(options => options.map(option => ({ value: option.value, disabled: option.disabled })));
  assert(groupedPolicyOptions.some(option => option.value === 'ALL' && !option.disabled),
    `fixture member-field choice must offer the native ALL policy: ${JSON.stringify(groupedPolicyOptions)}`);
  await action('choose ALL values per grouped row', groupedPolicy, () => groupedPolicy.selectOption('ALL'), {
    after: async () => assert.equal(await groupedPolicy.inputValue(), 'ALL'),
  });
  const rawFields = page.getByTestId('feature-catalog-raw-fields');
  if (!await rawFields.evaluate(element => element.open)) {
    await action('open raw FHIR fields', rawFields.locator('summary'), () => rawFields.locator('summary').click(), {
      after: async () => assert.equal(await rawFields.evaluate(element => element.open), true),
    });
  }
  const patientIDChoice = rawFields.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
  await patientIDChoice.waitFor({ state: 'visible' });
  const patientIDChoiceState = {
    visible: await patientIDChoice.isVisible(),
    enabled: await patientIDChoice.isEnabled(),
    checked: await patientIDChoice.isChecked(),
  };
  report.target.patientIdMemberChoice = patientIDChoiceState;
  assert(patientIDChoiceState.visible && patientIDChoiceState.enabled && !patientIDChoiceState.checked,
    `native Patient.id member-field candidate must be available and unchecked: ${JSON.stringify(patientIDChoiceState)}`);
  await action('select Patient.id member-field candidate', patientIDChoice, () => patientIDChoice.check(), {
    after: async () => assert.equal(await patientIDChoice.isChecked(), true),
  });
  const addSelected = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  await action('add selected Patient.id feature', addSelected, () => addSelected.click(), {
    after: async () => page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'visible' }),
  });
  const proposalPanel = page.getByTestId('construction-choice-proposal-panel');
  await page.waitForFunction(() => ['ready', 'error'].includes(
    document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus));
  const fieldProposal = await proposalPanel.evaluate(panel => ({
    status: panel.dataset.proposalStatus,
    text: panel.innerText,
    rows: [...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
  }));
  report.target.patientIdMemberProposal = fieldProposal;
  assert(fieldProposal.status === 'ready' && fieldProposal.rows.length === 1 && fieldProposal.rows[0].at(-1) === rawIDs.join('; '),
    `native ALL Patient.id proposal must expose the exact two raw fixture values on one group row: ${JSON.stringify(fieldProposal)}`);
  const fieldChoiceProposalIndex = choiceProposalEntries.length - 1;
  const fieldCommandIndex = commandEntries.length;
  const fieldReconcileIndex = reconciliationEntries.length;
  let fieldPreview;
  const applyColumns = proposalPanel.getByRole('button', { name: 'Apply columns', exact: true });
  await applyColumns.waitFor({ state: 'visible' });
  await action('apply Patient.id member field with ALL', applyColumns, () => applyColumns.click(), {
    after: async () => {
      fieldPreview = await waitAcceptedChoicePreview(fieldChoiceProposalIndex, fieldCommandIndex, fieldReconcileIndex,
        'Native ALL Patient.id member-field Preview');
      await page.waitForFunction(() => !document.querySelector('[data-testid="construction-choice-proposal-panel"]')
        && !document.body.innerText.includes('Loading your table…')
        && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '2');
    },
  });
  report.target.patientIdMemberPreviewDelivery = {
    delivery: fieldPreview.delivery,
    proposalCommandId: fieldPreview.proposalCommandId,
    proposalRequest: fieldPreview.proposalRequest,
    commandRequest: fieldPreview.commandRequest,
    reconcileRequest: fieldPreview.reconcileRequest,
    receiptId: fieldPreview.acceptedByReceipt.receiptId,
    outputId: fieldPreview.outputId,
    intentDigest: fieldPreview.acceptedByReceipt.intentDigest,
  };
  const closeEditor = page.getByRole('button', { name: 'Close operation editor', exact: true });
  await action('close operation editor', closeEditor, () => closeEditor.click(), {
    after: async () => page.getByTestId('construction-add-columns-source').waitFor({ state: 'hidden' }),
  });
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  const { legacy: preservedLegacyIDColumn, member: firstMemberIDColumn, binding: idBinding } =
    assertCohortPatientIDColumns(document, legacyIdentity);
  let idColumn = firstMemberIDColumn;
  assert.equal(idBinding?.policy, 'ALL', 'native Patient.id member field must save its explicit ALL policy');
  assert.deepEqual(fieldPreview.response.rows[0]?.[idColumn.column], rawIDs,
    'native ALL preview must retain both literal Patient IDs on the member field');
  report.target.patientIdColumnIdentities = {
    legacyIdentity: { column: preservedLegacyIDColumn.column, columnId: preservedLegacyIDColumn.columnId, source: preservedLegacyIDColumn.source, memberBinding: false },
    memberIdentity: { column: idColumn.column, columnId: idColumn.columnId, source: idColumn.source, policy: idBinding.policy, expectedValues: rawIDs },
  };

  let renderedTypedPreview = fieldPreview.response;
  const assertRenderedMemberCell = async (expectedText, label) => {
    const typedColumns = renderedTypedPreview.columns.map(column => ({ column: column.column, label: column.label }));
    const sourceColumn = idColumn.column;
    const rendered = await page.getByTestId('preview-table-scroll').getByRole('table').evaluate((table, data) => {
      const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
      const rows = [...table.querySelectorAll('[role="row"]')];
      const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => normalize(cell.innerText));
      const index = data.typedColumns.findIndex(column => column.column === data.sourceColumn);
      const dataRows = rows.slice(1);
      const cells = [...(dataRows[0]?.querySelectorAll('[role="cell"]') ?? [])];
      return {
        sourceColumn: data.sourceColumn,
        typedColumns: data.typedColumns,
        typedColumnCount: data.typedColumns.length,
        headers,
        rowCount: dataRows.length,
        cellCount: cells.length,
        index,
        typedColumn: index < 0 ? null : data.typedColumns[index],
        header: index < 0 ? null : headers[index],
        value: index < 0 ? null : normalize(cells[index]?.innerText),
      };
    }, { typedColumns, sourceColumn });
    assert(rendered.rowCount === 1 && rendered.typedColumns.filter(column => column.column === sourceColumn).length === 1 &&
      rendered.typedColumn?.label?.toLowerCase() === idColumn.label.toLowerCase() &&
      rendered.typedColumnCount === rendered.headers.length && rendered.cellCount === rendered.headers.length &&
      rendered.index >= 0 && rendered.headers[rendered.index]?.toLowerCase() === rendered.typedColumn.label.toLowerCase() &&
      rendered.value === expectedText,
    `${label} must render exact Patient.id at its accepted-preview source identity ${sourceColumn}: ${JSON.stringify(rendered)}`);
    return rendered;
  };
  const columnsToggle = page.getByRole('button', { name: 'Columns', exact: true });
  const columnsMenu = page.locator('[aria-label="Table columns"]');
  const ensureColumnsMenu = async () => {
    if (!await columnsMenu.isVisible().catch(() => false)) {
      await action('open table columns menu', columnsToggle, () => columnsToggle.click(), {
        after: async () => columnsMenu.waitFor({ state: 'visible' }),
      });
    }
  };
  const closeColumnsMenu = async () => {
    if (!await columnsMenu.isVisible().catch(() => false)) return;
    await action('close table columns menu', columnsToggle, () => columnsToggle.click(), {
      after: async () => columnsMenu.waitFor({ state: 'hidden' }),
    });
  };
  const configuredFeatureRow = async () => {
    await ensureColumnsMenu();
    const sourceSetup = await page.getByTestId('construction-source-setup').evaluate(section => ({ found: true, open: section.open }));
    assert.equal(sourceSetup.open, false,
      `ordinary recode control must be available while Advanced source setup remains closed: ${JSON.stringify(sourceSetup)}`);
    const row = columnsMenu.locator(`[role="listitem"][data-column-name=${JSON.stringify(idColumn.column)}]`);
    assert.equal(await row.count(), 1, `Preview Columns menu must contain one source row for ${idColumn.column}`);
    report.target.memberFieldRecodeAccess = {
      path: 'Preview and configure → Columns → Patient.id → Recode exact category values',
      sourceColumn: idColumn.column,
      advancedSourceSetupOpened: false,
      advancedSourceSetupRemainedClosed: true,
      ordinaryMenuRowCount: await row.count(),
    };
    return row;
  };
  const configuredFeatureControl = async control => (await configuredFeatureRow()).locator(control);
  const openConfiguredFeatureEditor = async summaryText => {
    const summary = await configuredFeatureControl('summary');
    assert.equal((await summary.innerText()).trim(), summaryText,
      `configured Patient.id feature editor must expose ${summaryText}`);
    const details = summary.locator('..');
    if (!await details.evaluate(element => element.open)) {
      await action(`open ${summaryText}`, summary, () => summary.click(), {
        after: async () => assert.equal(await details.evaluate(element => element.open), true),
      });
    }
  };
  const policy = await configuredFeatureControl('select[aria-label^="Values per cohort member for "]');
  const changePolicy = async (fromPolicy, toPolicy) => {
    await ensureColumnsMenu();
    assert.equal(await policy.inputValue(), fromPolicy, `member policy must start at ${fromPolicy}`);
    const previewIndex = previewEntries.length;
    await action(`change Patient.id policy ${fromPolicy} to ${toPolicy}`, policy,
      () => policy.selectOption(toPolicy), {
        after: async () => {
          await waitPreview(previewIndex, `Patient.id ${fromPolicy}→${toPolicy} automatic Preview`);
          await page.waitForFunction(() => !document.body.innerText.includes('Loading your table…')
            && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '2');
        },
      });
    const nextPreview = await waitPreview(previewIndex, `Patient.id ${fromPolicy}→${toPolicy} automatic Preview`);
    return nextPreview;
  };
  const tableRows = await previewTable.evaluate(table => [...table.querySelectorAll('[role="row"]')].slice(1)
    .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())));
  assert(tableRows.some(row => row.some(value => rawIDs.every(id => value.includes(id)))),
    `grouped Preview must expose exactly the two raw Patient IDs before recoding: ${JSON.stringify(tableRows)}`);
  check('correctness', 'Patient.id starts as distinct raw values under ALL', true,
    { expected: rawIDs, rendered: tableRows });
  const initialRawCell = await assertRenderedMemberCell(rawIDs.join('; '), 'Initial raw ALL Preview');
  check('correctness', 'raw ALL Patient.id values retain their exact fixture identities', true,
    { expected: rawIDs, rendered: initialRawCell });

  const category = 'Shared fixture category';
  const mappings = rawIDs.map(from => ({ from, to: category }));
  const transform = { kind: 'EXACT_CATEGORY_RECODE', exactCategoryRecode: { mappings, unknownPolicy: 'KEEP_ORIGINAL' } };
  await openConfiguredFeatureEditor('Recode exact category values');
  for (let index = 0; index < mappings.length; index += 1) {
    const mapping = mappings[index];
    const row = await configuredFeatureRow();
    const addMapping = row.getByRole('button', { name: 'Add mapping', exact: true });
    await action(`add recode mapping ${index + 1}`, addMapping, () => addMapping.click());
    const sourceValue = await configuredFeatureControl(`input[aria-label=${JSON.stringify(`Recorded category ${index + 1} for ${idColumn.label}`)}]`);
    await action(`enter source Patient.id ${index + 1}`, sourceValue, () => sourceValue.fill(mapping.from), { editable: true });
    const replacementValue = await configuredFeatureControl(`input[aria-label=${JSON.stringify(`Replacement value ${index + 1} for ${idColumn.label}`)}]`);
    await action(`enter replacement category ${index + 1}`, replacementValue, () => replacementValue.fill(mapping.to), { editable: true });
  }
  const unknownPolicy = await configuredFeatureControl(`select[aria-label=${JSON.stringify(`Unmapped value policy for ${idColumn.label}`)}]`);
  await action('keep unmapped category values', unknownPolicy, () => unknownPolicy.selectOption('KEEP_ORIGINAL'));
  const recodePreviewIndex = previewEntries.length;
  let preview;
  const saveRecode = await configuredFeatureRow().then(row => row.getByRole('button', { name: 'Save recoding', exact: true }));
  await action('save exact category recoding', saveRecode, () => saveRecode.click(), {
      after: async () => {
        await waitPreview(recodePreviewIndex, 'Recoded Patient.id ALL Preview');
        await page.waitForFunction(() => !document.body.innerText.includes('Loading your table…')
          && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '2');
      },
    });
  preview = await waitPreview(recodePreviewIndex, 'Recoded Patient.id ALL Preview');
  renderedTypedPreview = preview.response;
  assert.deepEqual(preview.response.rows[0]?.[idColumn.column], [category],
    'ALL must recode both source IDs before producing the unique shared category');
  const recodedAllCell = await assertRenderedMemberCell(category, 'Recoded ALL Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  idColumn = document.columns.find(column => column.columnId === idColumn.columnId);
  assert.deepEqual(idColumn.valueTransformation, transform, 'saved recoding must match the literal browser-authored transformation');
  assert.equal(document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy, 'ALL',
    'saving recoding must retain the initial ALL policy');
  check('correctness', 'different literal Patient IDs recode to one shared category under ALL', true,
    { rawIDs, mappings, previewValue: preview.response.rows[0]?.[idColumn.column], rendered: recodedAllCell });

  preview = await changePolicy('ALL', 'ONE');
  renderedTypedPreview = preview.response;
  assert.equal(preview.response.rows[0]?.[idColumn.column], category, 'ONE must return the shared scalar category');
  const recodedOneCell = await assertRenderedMemberCell(category, 'Recoded ONE Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  assert.equal(document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy, 'ONE',
    'accepted ONE edit must save on the same Patient.id binding');
  check('correctness', 'ONE accepts different raw Patient IDs after they recode to the same category', true,
    { value: preview.response.rows[0]?.[idColumn.column], rendered: recodedOneCell });
  await closeColumnsMenu();
  await page.reload({ waitUntil: 'domcontentloaded' });
  const transformedTableControl = page.getByTestId(`construction-table-${outputId}`);
  await transformedTableControl.waitFor({ state: 'visible' });
  await action('open Patient table after transformed ONE reload', transformedTableControl,
    () => transformedTableControl.click(), {
      after: async () => page.waitForFunction(() => !document.body.innerText.includes('Loading your table…')
        && document.body.innerText.includes('Shared fixture category')),
    });
  renderedTypedPreview = preview.response;
  const reloadedOneCell = await assertRenderedMemberCell(category, 'Reloaded transformed ONE Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  idColumn = document.columns.find(column => column.columnId === idColumn.columnId);
  assert.equal(document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy, 'ONE',
    'reload must preserve ONE on the same member binding');
  assert.deepEqual(idColumn.valueTransformation, transform, 'reload must preserve the exact recoding transformation');
  check('persistence', 'transformed ONE policy and exact recoding survive reload', true,
    { policy: 'ONE', transformation: idColumn.valueTransformation, rendered: reloadedOneCell });

  await ensureColumnsMenu();
  const allPreview = await changePolicy('ONE', 'ALL');
  renderedTypedPreview = allPreview.response;
  assert.deepEqual(allPreview.response.rows[0]?.[idColumn.column], [category],
    'returning ONE to ALL must preserve the single shared-category array');
  const recodedAllAgainCell = await assertRenderedMemberCell(category, 'Recoded ALL restoration Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  assert.equal(document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy, 'ALL',
    'return to ALL must be saved on the same Patient.id binding');
  check('correctness', 'returning from ONE to ALL restores the shared-category array', true,
    { value: allPreview.response.rows[0]?.[idColumn.column], rendered: recodedAllAgainCell });
  await openConfiguredFeatureEditor('Edit exact category recoding');
  const restorePreviewIndex = previewEntries.length;
  const removeRecode = await configuredFeatureRow().then(row => row.getByRole('button', { name: 'Remove recoding', exact: true }));
  await action('remove exact category recoding', removeRecode, () => removeRecode.click(), {
    after: async () => {
      await waitPreview(restorePreviewIndex, 'Raw Patient.id ALL Preview after removing recoding');
      await page.waitForFunction(() => !document.body.innerText.includes('Loading your table…')
        && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '2');
    },
  });
  preview = await waitPreview(restorePreviewIndex, 'Raw Patient.id ALL Preview after removing recoding');
  renderedTypedPreview = preview.response;
  assert.deepEqual(preview.response.rows[0]?.[idColumn.column], rawIDs,
    'removing recoding must restore both exact raw Patient IDs under ALL');
  const restoredRawCell = await assertRenderedMemberCell(rawIDs.join('; '), 'Raw ALL Preview after removing recoding');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  idColumn = document.columns.find(column => column.columnId === idColumn.columnId);
  assert(!idColumn.valueTransformation, 'removing recoding must clear the transformation from the same stable binding');
  assert.equal(document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy, 'ALL',
    'removing recoding must preserve ALL on the same member binding');
  check('correctness', 'removing recoding restores both exact raw Patient IDs under ALL', true,
    { value: preview.response.rows[0]?.[idColumn.column], rendered: restoredRawCell });
  await closeColumnsMenu();
  await page.reload({ waitUntil: 'domcontentloaded' });
  const restoredTableControl = page.getByTestId(`construction-table-${outputId}`);
  await restoredTableControl.waitFor({ state: 'visible' });
  await action('open Patient table after raw ALL reload', restoredTableControl,
    () => restoredTableControl.click(), {
      after: async () => page.waitForFunction(() => !document.body.innerText.includes('Loading your table…')),
    });
  renderedTypedPreview = preview.response;
  const finalRawCell = await assertRenderedMemberCell(rawIDs.join('; '), 'Final reloaded raw ALL Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  idColumn = document.columns.find(column => column.columnId === idColumn.columnId);
  const finalIDBinding = document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId);
  const finalIDColumns = assertCohortPatientIDColumns(document, legacyIdentity);
  assert.equal(finalIDColumns.member.columnId, idColumn.columnId,
    'reload must preserve the same distinct Patient.id member column identity');
  assert(!idColumn.valueTransformation && finalIDBinding?.policy === 'ALL',
    'final reload must retain raw untransformed ALL state on the same cohort and column identity');
  check('persistence', 'raw ALL restoration survives reload on the same cohort and column identity', true,
    { columnId: idColumn.columnId, revisionId: cohort.revisionId, rendered: finalRawCell });
  const oracleAfter = await readPatientOracle(context.target.fixtureDir);
  check('correctness', 'independent Patient source stayed unchanged during browser run', oracleAfter.sha256 === oracleBefore.sha256,
    { path: oracleBefore.sourcePath, before: oracleBefore.sha256, after: oracleAfter.sha256, sourcePatientCount: oracleBefore.patientRecordCount });
  report.target.browserRequestEvidence = [...bootstrapRequestEvidence, ...previewEntries, ...choiceProposalEntries, ...commandEntries, ...reconciliationEntries]
    .map(entry => ({ identity: entry.identity, requestObjectIdentity: entry.requestObjectIdentity, requestId: entry.requestId, method: entry.method, path: entry.path, origin: entry.origin, status: entry.status, failure: entry.failure, outputId: entry.outputId }));
  report.target.uncoveredAdjacentBehavior = 'Raw ALL→ONE rejection for distinct Patient IDs is not exercised in this basic fixture cycle; the CDA transformed-category driver retains its raw disagreement rejection assertion.';
});

export const runBuilderAuthoring = async (context, caseNames) => {
  const reports = [];
  for (const caseName of caseNames) {
    if (caseName === 'suggestions') reports.push(await runPlaywrightSuggestions(context));
    else if (caseName === 'authoring') reports.push(await runPlaywrightAuthoring(context));
    else if (caseName === 'repeated-empty') reports.push(await runRepeatedEmpty(context));
    else if (caseName === 'cohort-expand') reports.push(await runCohortExpand(context));
    else if (caseName === 'cohort-recode') reports.push(await runCohortRecode(context));
    else throw new Error(`unsupported Builder authoring case: ${caseName}`);
  }
  return reports;
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  await executeScenario({ id: 'builder-authoring', argv: process.argv.slice(2), runner: runBuilderAuthoring, mutating: true });
}
