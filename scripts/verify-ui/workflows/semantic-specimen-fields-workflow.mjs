import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { createNativeCdaWorkflowTools } from '../helpers/native-cda-workflow-tools.mjs';
import { parseSpecimenSemanticWitnessReport } from '../helpers/semantic-specimen-witness.mjs';

const normalize = (value) => String(value ?? '').normalize('NFKC').replace(/\s+/g, ' ').trim();
const nativePreview = (response) => response?.preview?.rows !== undefined ? response.preview : response;
const columnKey = (column) => column?.column ?? column?.id ?? column?.columnId;
const semanticTitle = (item) => {
  const label = item.display.trim() || item.code.trim() || item.sourcePath;
  return /^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$/.test(label)
    ? label.replaceAll('_', ' ').replace(/^./, (first) => first.toUpperCase())
    : label;
};

export async function semanticSpecimenFieldsWorkflow({ page, cda, caseOptions = {} }) {
  const { click, fill, navigate, inspect, waitForBrowser } =
    createNativeCdaWorkflowTools({ page, cda });
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const witnessReportPath = caseOptions.witnessReportPath;
  assert(project && generation && apiOrigin && uiOrigin, 'The owned CDA fixture must bind project, generation, API, and UI origins.');
  assert.equal(generation, 'cda-fhir-v1');
  assert(witnessReportPath, 'Set LOOM_CDA_SPECIMEN_SEMANTIC_WITNESS_REPORT to the independent bounded source-read report.');
  const witnessReport = JSON.parse(await readFile(witnessReportPath, 'utf8'));
  const witness = parseSpecimenSemanticWitnessReport(witnessReport, { project, generation });
  if (cda.target.authResourcePath !== undefined) {
    assert.equal(witness.source.authResourcePath ?? null, cda.target.authResourcePath ?? null,
      'The retained Specimen witness must match the owned CDA authorization scope.');
  }

  const explorer = `semantic-specimen-${Date.now()}-${randomUUID().slice(0, 8)}`;
  const explorerPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const rootPath = `${explorerPath}/${encodeURIComponent(explorer)}`;
  const authoringPath = `${rootPath}/authoring/v2`;
  const pageUrl = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  const report = {
    started: new Date().toISOString(),
    explorer,
    witness: {
      project: witness.source.project,
      generation: witness.source.generation,
      resourceType: witness.source.resourceType,
      id: witness.source.id,
      rawType: witness.raw.type,
      categoryIdentity: { system: witness.coding.system, version: witness.coding.version ?? '', code: witness.coding.code },
      noncodedField: witness.noncoded,
      sourceReadEvidence: witnessReport.evidence,
    },
    nativeRequests: cda.report.nativeRequests,
    assertions: [],
    errors: [],
    scopeNotes: [{
      id: 'reopen-saved-raw-source-binding-in-picker',
      status: 'out-of-scope-for-existing-root-column-lifecycle',
      reason: 'Saved direct root columns expose label editing and removal, but no source-binding editor that reopens the raw field picker. This SC05 case verifies the exact binding across add, rename, remove, and reload; reopening an existing related-field source belongs to SC09 and initialFieldItem coverage.',
    }],
  };
  let builder;
  let outputId;
  let selection;
  let requestView;
  let originalWorkspace;

  const record = (dimension, name, evidence) => {
    cda.check(dimension, name, true, evidence);
    report.assertions.push({ dimension, name, status: 'passed', evidence });
  };
  const api = async (path, body) => {
    const headers = { 'Content-Type': 'application/json', 'X-Request-ID': `semantic-specimen-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30000 });
    const value = await response.json();
    report.apiCalls ??= [];
    report.apiCalls.push({ path, status: response.status(), body, response: value });
    assert(response.ok(), `${path}: ${JSON.stringify(value)}`);
    return value;
  };
  const currentDocument = (state = builder) =>
    state.workspace.documents.find((document) => document.output.id === outputId);
  const command = async (commands) => {
    await api(`${authoringPath}/commands`, {
      commandId: randomUUID(),
      semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    });
    builder = await api(`${authoringPath}/builder`);
  };
  const nativeIndex = () => requestView.requests.length;
  const waitNative = async (predicate, fromIndex = 0, timeoutMs = 10000) => {
    const entry = requestView.requests.slice(fromIndex).find((candidate) =>
      candidate.status !== undefined && candidate.response !== undefined && !candidate.failure && predicate(candidate),
    ) ?? await requestView.waitForResponse((candidate) =>
      candidate.status !== undefined && candidate.response !== undefined && !candidate.failure && predicate(candidate),
    { fromIndex, timeoutMs });
    assert.equal(entry.status, 200, `Native ${entry.path} returned ${entry.status}: ${JSON.stringify(entry.response).slice(0, 1200)}`);
    return entry;
  };
  const renderPreview = async (expectedColumns, phase) => {
    await waitForBrowser(page, () => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return Boolean(table && table.getAttribute('aria-rowcount') === '2' &&
        table.getAttribute('aria-colcount') === String(expectedColumns) &&
        !document.body.innerText.includes('Loading your table…') &&
        !document.body.innerText.includes('Preview failed:'));
    }, [], 10000);
    const view = await inspect(page, () => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return {
        headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
          .map((cell) => normalize(cell.innerText)),
        rows: [...(table?.querySelectorAll('[role="row"]') ?? [])].slice(1).map((row) =>
          [...row.querySelectorAll('[role="cell"]')].map((cell) => normalize(cell.innerText)),
        ).filter((row) => row.length),
      };
    });
    assert.equal(view.rows.length, 1, `${phase} must render the one independently selected Specimen witness.`);
    return view;
  };
  const openTable = async (expectedColumns, phase) => {
    const fromIndex = nativeIndex();
    await navigate(page, pageUrl);
    await waitForBrowser(page, () => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`)), [], 10000);
    await click(page, `[data-testid="construction-table-${outputId}"]`);
    const dom = await renderPreview(expectedColumns, phase);
    const native = await waitNative((entry) => entry.path.endsWith('/preview') && entry.request?.outputId === outputId, fromIndex);
    const preview = nativePreview(native.response);
    assert.equal(preview.outputId, outputId, `${phase} native preview must belong to the exact table output.`);
    assert.equal(preview.rows.length, 1);
    return { dom, native, preview };
  };
  const openAddColumns = async () => {
    if (!(await page.locator('[data-testid="construction-operation-editor"]').count())) {
      await click(page, '[data-testid="construction-action-add-columns"]');
    }
    await waitForBrowser(page, () => Boolean(document.querySelector('[data-testid="construction-operation-editor"]')), [], 10000);
  };
  const selectTab = async (name) => {
    const tab = page.getByRole('tab', { name, exact: true });
    await tab.waitFor({ state: 'visible', timeout: 10000 });
    if (await tab.getAttribute('aria-selected') !== 'true') await tab.click({ timeout: 5000 });
  };
  const setSearch = async (value) => {
    const search = page.getByRole('searchbox', { name: 'Search features by field name, concept, or code', exact: true });
    await search.waitFor({ state: 'visible', timeout: 10000 });
    await search.fill(value, { timeout: 5000 });
    await page.getByRole('button', { name: 'Search', exact: true }).click({ timeout: 5000 });
  };
  const itemArticle = async (identity, label) => {
    await waitForBrowser(page, ([expectedIdentity]) => Boolean(
      [...document.querySelectorAll('article[data-semantic-identity]')]
        .some((element) => element.getAttribute('data-semantic-identity') === expectedIdentity),
    ), [identity], 10000);
    const cards = page.locator('article[data-semantic-identity]');
    const found = await cards.evaluateAll((elements, expected) => elements.map((element, index) => ({
      index,
      identity: element.getAttribute('data-semantic-identity'),
      text: element.innerText,
      label: element.querySelector('button[aria-pressed]')?.getAttribute('aria-label'),
    })).find((candidate) => candidate.identity === expected), identity);
    assert(found, `Native Coded values omitted the exact proved category ${label}.`);
    assert(found.label?.includes(witness.coding.system), 'The accessible category selection name must disambiguate its Coding system.');
    assert(found.label?.includes(witness.coding.version?.trim() || 'unversioned'),
      'The accessible category selection name must disambiguate its exact Coding version.');
    return cards.nth(found.index);
  };
  const fieldButton = async (fieldPath, { required = true } = {}) => {
    const buttons = page.locator('button[aria-pressed]');
    const values = await buttons.evaluateAll((elements) => elements.map((element, index) => ({
      index,
      name: element.getAttribute('aria-label') ?? '',
      disabled: element.hasAttribute('disabled'),
    })));
    const found = values.filter((item) => item.name === `Select Specimen.${fieldPath}`);
    if (!required) {
      assert.equal(found.length, 0, `Grouped raw field ${fieldPath} must not reappear in the default Other fields list.`);
      return undefined;
    }
    assert.equal(found.length, 1, `Expected one accessible source-field selection for Specimen.${fieldPath}; saw ${JSON.stringify(values)}`);
    assert.equal(found[0].disabled, false, `Specimen.${fieldPath} must retain its existing eligible selection state.`);
    return buttons.nth(found[0].index);
  };
  const focusCategoryFromRawPath = async (identity, label) => {
    await selectTab('Other fields');
    await setSearch('type.coding[].code');
    const includedLinks = page.locator('button[aria-label^="Included in "]');
    await waitForBrowser(page, ([expectedIdentity]) => Boolean(
      [...document.querySelectorAll('button[aria-label^="Included in "]')]
        .some((element) => element.getAttribute('data-semantic-identity') === expectedIdentity),
    ), [identity], 10000);
    const matching = await includedLinks.evaluateAll((elements, expectedIdentity) => elements
      .map((element, index) => ({ index, identity: element.getAttribute('data-semantic-identity'), label: element.getAttribute('aria-label') }))
      .filter((entry) => entry.identity === expectedIdentity), identity);
    assert.equal(matching.length, 1, 'The raw-path search must identify one exact semantic family destination.');
    await includedLinks.nth(matching[0].index).click({ timeout: 5000 });
    await waitForBrowser(page, () => document.querySelector('[role="tab"][data-view="coded"]')?.getAttribute('aria-selected') === 'true', [], 10000);
    return itemArticle(identity, label);
  };
  const applyOneSelection = async (expectedLabel) => {
    const fromIndex = nativeIndex();
    const add = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    await add.waitFor({ state: 'visible', timeout: 10000 });
    await add.click({ timeout: 5000 });
    const proposal = await waitNative((entry) =>
      entry.path.endsWith('/construction-choice-proposals') && entry.request?.outputId === outputId &&
      entry.request?.constructionChoices?.length === 1,
    fromIndex);
    assert.equal(typeof proposal.request.constructionChoices[0]?.title, 'string');
    assert(proposal.request.constructionChoices[0].title.trim());
    if (expectedLabel !== undefined) assert.equal(proposal.request.constructionChoices[0].title, expectedLabel);
    const commandEntry = await waitNative((entry) =>
      entry.path.endsWith('/commands') && entry.request?.commands?.some((item) => item.type === 'APPLY_CONSTRUCTION_CHOICE'),
    fromIndex);
    return { proposal, commandEntry };
  };

  try {
    await api(explorerPath, { name: explorer, title: 'CDA semantic Specimen fields QA' });
    builder = await api(`${authoringPath}/builder`);
    const specimenNode = builder.catalog.nodes.find((node) => node.resourceType === 'Specimen' && node.rowRootEligible) ??
      builder.catalog.nodes.find((node) => node.resourceType === 'Specimen');
    assert(specimenNode, 'The owned CDA catalog must expose a Specimen row-root node.');
    const rootCandidates = builder.catalog.candidates.filter((candidate) => candidate.nodeId === specimenNode.nodeId);
    const idCandidate = rootCandidates.find((candidate) => candidate.fieldPath === 'id');
    const rawCodeCandidate = rootCandidates.find((candidate) => candidate.fieldPath === 'type.coding[].code');
    const rawSystemCandidate = rootCandidates.find((candidate) => candidate.fieldPath === 'type.coding[].system');
    const noncodedCandidate = rootCandidates.find((candidate) => candidate.fieldPath === witness.noncoded.path);
    assert(idCandidate, 'The Specimen root table must retain its physical id field.');
    assert(rawCodeCandidate?.populated === true, 'The independently populated Specimen.type Coding code must remain an eligible raw field.');
    assert(rawSystemCandidate?.populated === true, 'The independently populated Specimen.type Coding system must remain an eligible raw field.');
    assert(noncodedCandidate?.populated === true, `Specimen.${witness.noncoded.path} must remain an eligible noncoded raw field.`);

    await command([{ type: 'CREATE_TABLE', title: 'Semantic Specimen source rows', rootNodeId: specimenNode.nodeId }]);
    const document = currentDocument();
    outputId = document.output.id;
    await command([{
      type: 'ADD_COLUMN',
      outputId,
      occurrenceId: 'base',
      candidateId: idCandidate.candidateId,
      projectionMode: 'VALUE',
      initialPresentation: 'TABLE',
      title: 'Specimen ID',
    }]);
    selection = await api(`${explorerPath}/${encodeURIComponent(explorer)}/selections`, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: `semantic-specimen-${explorer}`,
      source: { kind: 'resources', resources: { refs: [{
        project,
        generation,
        resourceType: 'Specimen',
        id: witness.source.id,
      }] } },
    });
    const routes = await api(`${authoringPath}/population-routes`, {
      snapshotToken: builder.catalog.snapshotToken,
      outputId,
      selectionRevisionId: selection.id,
      limit: 25,
    });
    const rootRoute = routes.choices.find((choice) => choice.route.length === 0);
    assert(rootRoute, 'The exact Specimen witness selection must resolve to the direct row-root route.');
    await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: rootRoute.routeChoiceId }]);
    const sourceDocument = currentDocument();
    assert.equal(sourceDocument.rootResourceType, 'Specimen');
    assert.equal(sourceDocument.rows.kind, 'RECORDS');
    assert.equal(sourceDocument.population.selectionRevisionId, selection.id);
    assert.equal(sourceDocument.population.route?.length ?? 0, 0);
    assert.equal(sourceDocument.columns.length, 1);
    originalWorkspace = structuredClone(builder.workspace);
    requestView = cda.captureRequests(`${explorerPath}/${encodeURIComponent(explorer)}`, {
      responsePaths: /semantic-inventory|construction-choices|construction-choice-proposals|\/commands|\/preview/,
    });

    const initial = await openTable(1, 'initial exact Specimen row');
    const idColumnId = columnKey(sourceDocument.columns[0]);
    assert.equal(initial.preview.rows[0]?.[idColumnId], witness.source.id,
      'Initial native preview must show the exact independently read Specimen ID.');
    record('correctness', 'native Specimen table rendered the exact bounded source witness', {
      outputId,
      rowRoot: sourceDocument.rootResourceType,
      selectionRevisionId: selection.id,
      sourceId: witness.source.id,
      sourceKey: witness.source.sourceKey,
      initialReceiptId: initial.preview.receiptId,
    });

    await openAddColumns();
    await selectTab('Coded values');
    const codeBrowse = await waitNative((entry) => entry.path.endsWith('/semantic-inventory') &&
      entry.request?.fieldPresentation?.sourceNodeId === specimenNode.nodeId &&
      entry.request?.fieldPresentation?.occurrenceId === 'base' &&
      entry.request?.fieldPresentation?.candidateIds?.includes(rawCodeCandidate.candidateId), 0);
    assert.equal(codeBrowse.request.outputId, outputId);
    assert.equal(codeBrowse.request.fieldPresentation.expectedDraftVersion, builder.draftVersion);
    assert.equal(codeBrowse.request.fieldPresentation.expectedDraftDigest, builder.draftDigest);
    assert(codeBrowse.request.fieldPresentation.candidateIds.length <= 50,
      'The field-presentation request must keep its exact source-candidate batch bounded.');
    const proof = codeBrowse.response.fieldPresentation;
    assert.equal(proof?.kind, 'READY', 'The native root Specimen browse must return a current READY field-coverage proof.');
    const codeDecision = proof.decisions.find((decision) => decision.candidateId === rawCodeCandidate.candidateId);
    assert.equal(codeDecision?.kind, 'GROUP_UNDER_SEMANTIC',
      'Only the exact independently populated Specimen Coding leaf may be grouped under a semantic category.');
    assert.equal(codeDecision.access, 'SOURCE_FIELDS');
    const categoryEntries = proof.replacements.filter((item) =>
      codeDecision.replacementIds.some((identity) => identity.kind === 'SYSTEM_VERSION_CATEGORY' &&
        identity.familyId === item.familyId && identity.bindingId === item.bindingId) &&
      item.resourceType === 'Specimen' && item.system === witness.coding.system &&
      item.codingVersion === (witness.coding.version ?? ''),
    );
    assert.equal(categoryEntries.length, 1, 'The READY proof must resolve the exact raw system/version/code family from the independent source witness.');
    const categoryEntry = categoryEntries[0];
    const categoryIdentity = JSON.stringify(['SYSTEM_VERSION_CATEGORY', categoryEntry.familyId, categoryEntry.bindingId]);
    const categoryTitle = semanticTitle(categoryEntry);
    const accessibleCategoryLabel = `${categoryTitle} · system ${witness.coding.system} · version ${witness.coding.version?.trim() || 'unversioned'}`;
    record('correctness', 'native bounded field proof binds the exact raw Specimen Coding to its current category family', {
      outputId,
      nodeId: specimenNode.nodeId,
      occurrenceId: 'base',
      draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest,
      candidateId: rawCodeCandidate.candidateId,
      decision: codeDecision,
      categoryIdentity,
      categoryCode: categoryEntry.code,
      categorySystem: categoryEntry.system,
      categoryVersion: categoryEntry.codingVersion,
    });

    await selectTab('Other fields');
    await fieldButton(witness.noncoded.path);
    await fieldButton('type.coding[].code', { required: false });
    await setSearch('type.coding[].code');
    const includedLinks = page.locator('button[aria-label^="Included in "]');
    await includedLinks.first().waitFor({ state: 'visible', timeout: 10000 });
    const linkIdentity = await includedLinks.evaluateAll((elements) => elements.map((element) => ({
      identity: element.getAttribute('data-semantic-identity'),
      label: element.getAttribute('aria-label'),
    })));
    const matchingIncluded = linkIdentity.filter((item) => item.identity === categoryIdentity);
    assert.equal(matchingIncluded.length, 1, 'Searching the raw Coding path must retain one Included in link to the exact proved family.');
    assert(matchingIncluded[0].label.includes(witness.coding.system));
    await includedLinks.nth(linkIdentity.findIndex((item) => item.identity === categoryIdentity)).click({ timeout: 5000 });
    await waitForBrowser(page, () => document.querySelector('[role="tab"][data-view="coded"]')?.getAttribute('aria-selected') === 'true', [], 10000);
    await itemArticle(categoryIdentity, accessibleCategoryLabel);
    record('usability', 'raw-path search reaches the exact semantic category through Included in', {
      rawPath: 'type.coding[].code',
      noncodedField: witness.noncoded.path,
      categoryIdentity,
      includedLink: matchingIncluded[0].label,
    });

    const categoryCard = await itemArticle(categoryIdentity, accessibleCategoryLabel);
    const categoryGroup = categoryCard.locator('xpath=..');
    const sourceFields = categoryGroup.getByText('Source fields', { exact: true });
    await sourceFields.click({ timeout: 5000 });
    const rawCodeButton = await fieldButton('type.coding[].code');
    assert(rawCodeButton, 'The grouped raw Coding path must be reachable under the semantic Source fields disclosure.');
    const sourceButtons = categoryGroup.locator('details button[aria-pressed]');
    const inDisclosure = await sourceButtons.evaluateAll((elements) => elements.some((element) =>
      element.getAttribute('aria-label') === 'Select Specimen.type.coding[].code' &&
      element.closest('details')?.querySelector('summary')?.innerText.trim() === 'Source fields',
    ));
    assert(inDisclosure, 'Specimen.type.coding[].code must be nested under the semantic Source fields disclosure.');
    await rawCodeButton.click({ timeout: 5000 });
    const semanticButton = categoryCard.locator('button[aria-pressed]');
    if (await semanticButton.getAttribute('aria-pressed') !== 'true') await semanticButton.click({ timeout: 5000 });
    const preSubmitBuilder = await api(`${authoringPath}/builder`);
    const commandCountBeforeCancel = requestView.requests.filter((entry) => entry.path.endsWith('/commands')).length;
    // Cancel the unsubmitted selection before invoking the single Add/Save action.
    await click(page, '[data-testid="construction-close-operation-editor"]');
    await waitForBrowser(page, () => !document.querySelector('[data-testid="construction-operation-editor"]'), [], 10000);
    builder = await api(`${authoringPath}/builder`);
    assert.deepEqual(builder.workspace, preSubmitBuilder.workspace, 'Closing the unsubmitted picker must preserve the saved Builder workspace.');
    assert.equal(requestView.requests.filter((entry) => entry.path.endsWith('/commands')).length, commandCountBeforeCancel,
      'Pre-submit cancellation must send no draft command.');
    record('persistence', 'pre-submit Cancel leaves the exact Specimen draft and population unchanged', {
      draftDigest: builder.draftDigest,
      commandCount: commandCountBeforeCancel,
      rootResourceType: currentDocument().rootResourceType,
    });

    await openAddColumns();
    const categoryCardAgain = await focusCategoryFromRawPath(categoryIdentity, accessibleCategoryLabel);
    const categoryButtonAgain = categoryCardAgain.locator('button[aria-pressed]');
    const categoryPressed = await categoryButtonAgain.getAttribute('aria-pressed');
    if (categoryPressed !== 'true') await categoryButtonAgain.click({ timeout: 5000 });
    const semanticAddStart = Date.now();
    const semanticAdd = await applyOneSelection(categoryTitle);
    builder = await api(`${authoringPath}/builder`);
    const semanticColumn = currentDocument().columns.find((column) =>
      column.source?.kind === 'codedValue' && column.source.lookup?.key?.system === witness.coding.system &&
      column.source.lookup?.binding?.selectionScope === 'SYSTEM_VERSION' &&
      (column.source.lookup.key.version ?? '') === (witness.coding.version ?? '') &&
      column.source.lookup.key.code === '',
    );
    assert(semanticColumn, 'Saved semantic category must retain its exact existing system/version family binding.');
    const semanticColumnId = columnKey(semanticColumn);
    assert.equal(semanticColumn.label, categoryTitle,
      'The saved semantic field label must use the semantic concept title without rewriting it to a source path or system/version qualifier.');
    const semanticChoice = semanticAdd.proposal.request.constructionChoices[0];
    assert.equal(semanticChoice.form, 'ALL', 'The system/version category must preserve all matching Coding values.');
    assert.equal(semanticChoice.title, categoryTitle);
    assert.equal(semanticChoice.source.system, witness.coding.system);
    assert.equal(semanticChoice.source.version ?? '', witness.coding.version ?? '');
    const semanticPreview = nativePreview(semanticAdd.proposal.response);
    assert.deepEqual(semanticPreview.rows[0]?.[semanticAdd.proposal.response.candidateColumnIds[0]],
      witness.expectedSemanticValues,
    'The semantic category preview must equal the independently read source Coding display/code values.');
    record('correctness', 'single Add action saves the exact Specimen semantic category and raw value', {
      categoryIdentity,
      fieldLabel: semanticColumn.label,
      source: semanticColumn.source,
      form: semanticChoice.form,
      rawExpectedValues: witness.expectedSemanticValues,
      preview: semanticPreview.rows[0]?.[semanticAdd.proposal.response.candidateColumnIds[0]],
      commandId: semanticAdd.proposal.request.commandId,
      savedCommandStatus: semanticAdd.commandEntry.status,
      durationMs: Date.now() - semanticAddStart,
    });

    await openAddColumns();
    const rawCategoryCard = await focusCategoryFromRawPath(categoryIdentity, accessibleCategoryLabel);
    const rawDisclosure = rawCategoryCard.locator('xpath=..').getByText('Source fields', { exact: true });
    await rawDisclosure.click({ timeout: 5000 });
    const rawCodeToAdd = await fieldButton('type.coding[].code');
    const rawAddStart = Date.now();
    await rawCodeToAdd.click({ timeout: 5000 });
    const rawAdd = await applyOneSelection();
    builder = await api(`${authoringPath}/builder`);
    const rawColumn = currentDocument().columns.find((column) =>
      column.source?.kind === 'field' && column.source.field?.path === 'type.coding[].code',
    );
    assert(rawColumn, 'The grouped source leaf must remain a saved direct raw-field column when selected through Source fields.');
    assert.equal(rawColumn.occurrenceId, 'base');
    assert.equal(rawColumn.source.field.projectionMode, 'ALL');
    const rawColumnId = columnKey(rawColumn);
    const rawChoice = rawAdd.proposal.request.constructionChoices[0];
    assert.equal(rawChoice.form, 'ALL');
    const rawPreview = nativePreview(rawAdd.proposal.response);
    const expectedRawCodes = witness.raw.type.coding.map((item) => item.code).filter((code) => typeof code === 'string');
    assert.deepEqual(rawPreview.rows[0]?.[rawAdd.proposal.response.candidateColumnIds[0]], expectedRawCodes,
      'The disclosed direct field preview must equal the independent raw Specimen.type.coding[].code array.');
    record('correctness', 'Source fields adds the physical Coding leaf with its original list cardinality', {
      rawPath: rawColumn.source.field.path,
      projectionMode: rawColumn.source.field.projectionMode,
      rawExpectedValues: expectedRawCodes,
      preview: rawPreview.rows[0]?.[rawAdd.proposal.response.candidateColumnIds[0]],
      durationMs: Date.now() - rawAddStart,
    });

    const oldRawLabel = rawColumn.label;
    const renamedRawLabel = 'Reviewed raw Specimen code';
    const renameStart = nativeIndex();
    if (!(await page.locator('[aria-label="Table columns"]').count())) {
      const table = await openTable(currentDocument().columns.length, 'pre-edit Specimen semantic and raw values');
      assert.equal(table.preview.rows.length, 1);
      await page.getByRole('button', { name: 'Columns', exact: true }).click({ timeout: 5000 });
    }
    const renameInput = page.getByRole('textbox', { name: `Column name for ${oldRawLabel}`, exact: true });
    await renameInput.waitFor({ state: 'visible', timeout: 10000 });
    await renameInput.fill(renamedRawLabel, { timeout: 5000 });
    await renameInput.press('Enter', { timeout: 5000 });
    const renameCommand = await waitNative((entry) => entry.path.endsWith('/commands') &&
      entry.request?.commands?.some((item) => item.type === 'UPDATE_COLUMN' && item.column === rawColumnId), renameStart);
    assert.equal(renameCommand.status, 200);
    builder = await api(`${authoringPath}/builder`);
    const editedRawColumn = currentDocument().columns.find((column) => columnKey(column) === rawColumnId);
    assert.equal(editedRawColumn?.label, renamedRawLabel);
    assert.equal(editedRawColumn?.source.field.path, rawColumn.source.field.path,
      'Editing the existing raw field label must preserve its physical source binding.');
    assert.equal(editedRawColumn?.source.field.projectionMode, 'ALL');
    record('persistence', 'reopening the saved column settings and renaming the raw output preserves its physical source identity', {
      columnId: rawColumnId,
      before: { label: oldRawLabel, source: rawColumn.source.field },
      after: { label: editedRawColumn.label, source: editedRawColumn.source.field },
    });

    const reloaded = await openTable(currentDocument().columns.length, 'reload saved semantic and raw Specimen values');
    assert(reloaded.dom.headers.some((header) => header === renamedRawLabel),
      'Reloaded native table must retain the edited direct-source field label.');
    const reloadedDocument = currentDocument(await api(`${authoringPath}/builder`));
    const semanticSaved = reloadedDocument.columns.find((column) => columnKey(column) === semanticColumnId);
    const rawSaved = reloadedDocument.columns.find((column) => columnKey(column) === rawColumnId);
    const identitySaved = reloadedDocument.columns.find((column) => columnKey(column) === idColumnId);
    assert(semanticSaved && rawSaved && identitySaved);
    assert.equal(semanticSaved.source?.kind, 'codedValue');
    assert.equal(rawSaved.source?.field?.path, 'type.coding[].code');
    assert.equal(rawSaved.source.field.projectionMode, 'ALL');
    const previewColumns = reloaded.preview.columns;
    const semanticPreviewColumn = previewColumns.find((column) => column.column === semanticColumnId);
    const rawPreviewColumn = previewColumns.find((column) => column.column === rawColumnId);
    assert(semanticPreviewColumn && rawPreviewColumn, 'Reloaded native preview must retain both semantic and raw output identities.');
    const row = reloaded.preview.rows[0];
    assert.deepEqual(row[semanticColumnId], witness.expectedSemanticValues,
      'Reloaded semantic output differs from the independent raw Coding witness.');
    assert.deepEqual(row[rawColumnId], expectedRawCodes,
      'Reloaded raw output differs from the independent Specimen.type.coding[].code witness.');
    assert.equal(row[idColumnId], witness.source.id);
    record('persistence', 'reload preserves exact semantic value, direct raw binding, and source row identity', {
      receiptId: reloaded.preview.receiptId,
      columns: reloadedDocument.columns.map((column) => ({ id: columnKey(column), label: column.label, source: column.source })),
      row: { [idColumnId]: row[idColumnId], [semanticColumnId]: row[semanticColumnId], [rawColumnId]: row[rawColumnId] },
      expectedSemanticValues: witness.expectedSemanticValues,
      expectedRawCodes,
    });

    const removeColumn = async (column) => {
      const id = columnKey(column);
      const from = nativeIndex();
      if (!(await page.locator('[aria-label="Table columns"]').count())) {
        await page.getByRole('button', { name: 'Columns', exact: true }).click({ timeout: 5000 });
      }
      await page.getByRole('button', { name: `Remove ${column.label} column`, exact: true }).click({ timeout: 5000 });
      await waitNative((entry) => entry.path.endsWith('/commands') &&
        entry.request?.commands?.some((item) => item.type === 'REMOVE_COLUMN' && item.column === id), from);
      builder = await api(`${authoringPath}/builder`);
    };
    await removeColumn(rawSaved);
    const semanticAfterRawRemoval = currentDocument().columns.find((column) => columnKey(column) === semanticColumnId);
    assert(semanticAfterRawRemoval, 'Removing the raw display column must leave the semantic category intact.');
    await removeColumn(semanticAfterRawRemoval);
    builder = await api(`${authoringPath}/builder`);
    assert.deepEqual(builder.workspace, originalWorkspace,
      'Removing semantic and raw additions must restore the exact original Specimen table and population.');
    const restored = await openTable(1, 'reload restored raw Specimen table');
    assert.equal(restored.preview.rows[0]?.[idColumnId], witness.source.id);
    record('persistence', 'removal and reload restore the original raw Specimen table without changing its population', {
      outputId,
      selectionRevisionId: selection.id,
      restoredColumns: currentDocument().columns.map((column) => ({ id: columnKey(column), source: column.source })),
      restoredRow: restored.preview.rows[0],
    });

    report.finished = new Date().toISOString();
    report.final = {
      status: 'passed',
      outputId,
      explorer,
      witnessId: witness.source.id,
      rowRoot: 'Specimen',
      scopeNotes: report.scopeNotes,
    };
  } finally {
    report.finished ??= new Date().toISOString();
    await cda.attachReport('semantic-specimen-fields-domain.json', { ...cda.report, domain: report });
  }
}
