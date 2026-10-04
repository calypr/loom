import { runRepeatedEmpty } from './builder-repeated.mjs';
import { runCohortExpand } from './builder-cohort-expand.mjs';
import { executeScenario, runBrowserCase } from './common.mjs';
import { randomUUID } from 'node:crypto';
import { click, evaluate, fill, reload, inspectAction, captureDOM, waitFor, recordBrowserTiming } from './browser.mjs';
import { isActionable, recordCheck, recordUntested } from './report.mjs';
import { addPatientTableRoot, configurePatientColumns, createBlankExplorer, previewPatientRows, publishPatientExplorer } from './workflows.mjs';

const setSelectValue = async (cdp, selector, value) => {
  const action = await inspectAction(cdp, selector);
  if (!isActionable(action)) throw new Error(`Select control is not actionable: ${JSON.stringify(action)}`);
  return evaluate(cdp,
    `(()=>{const select=document.querySelector(${JSON.stringify(selector)});if(!select)throw Error('select not found: '+${JSON.stringify(selector)});const setter=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value')?.set;if(!setter)throw Error('native select setter unavailable');setter.call(select,${JSON.stringify(value)});select.dispatchEvent(new Event('input',{bubbles:true}));select.dispatchEvent(new Event('change',{bubbles:true}));return select.value})()`);
};

const runCohortRecode = (context) => runBrowserCase(context, 'builder-authoring', 'cohort-recode', async ({ cdp, report }) => {
  const { explorer } = await createBlankExplorer(cdp, context.target, context.runID, 'cohort-recode', report);
  report.target.explorer = explorer;
  await addPatientTableRoot(cdp, report, 'Patients');

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
  if (builder.catalog?.generation !== generation || !builder.catalog?.snapshotToken || !builder.catalog?.authorizationScopeDigest) {
    throw new Error('Fixture cohort setup requires a current Builder snapshot and exact fixture generation.');
  }
  const sourceIDs = ['dev-patient-001', 'dev-patient-002'];
  const refs = sourceIDs.map(id => ({ project: selectionProject, generation, resourceType: 'Patient', id }));
  report.target.fixtureRawOracle = {
    project: selectionProject, storageProject: project, generation, resourceType: 'Patient', sourceIDs,
    rawMemberValues: sourceIDs,
    sharedCategory: 'Shared fixture category',
  };
  const selection = (await requestJSON(`${apiRoot}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `verify-cohort-recode-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs } },
  })).value;
  if (selection.project !== selectionProject || selection.generation !== generation || selection.resourceType !== 'Patient' ||
      selection.scopeDigest !== builder.catalog.authorizationScopeDigest || selection.memberCount !== sourceIDs.length) {
    throw new Error(`Fixture selection did not preserve the exact current Patient scope: ${JSON.stringify(selection)}`);
  }
  const selectionPage = (await requestJSON(`${apiRoot}/selections/${encodeURIComponent(selection.id)}?limit=100`)).value;
  const selectedRefs = (selectionPage.members ?? []).map(member => member.ref)
    .map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  const expectedRefs = refs.map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  if (selectionPage.revision?.id !== selection.id || selectionPage.revision?.scopeDigest !== builder.catalog.authorizationScopeDigest ||
      selectionPage.members?.length !== sourceIDs.length || JSON.stringify(selectedRefs) !== JSON.stringify(expectedRefs)) {
    throw new Error(`Fixture selection members differ from the two literal Patient refs: ${JSON.stringify({ expectedRefs, selectedRefs, selectionPage }).slice(0, 1200)}`);
  }
  const memberKeys = selectionPage.members.map(member => member.memberKey);
  if (memberKeys.some(key => typeof key !== 'string' || !key) || new Set(memberKeys).size !== sourceIDs.length) {
    throw new Error('Fixture selection did not issue two distinct opaque member keys.');
  }
  const cohort = (await requestJSON(`${apiRoot}/selections/${encodeURIComponent(selection.id)}/explicit-groups`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `verify-cohort-recode-group-${randomUUID()}`,
    groups: [{ id: 'verify-cohort', label: 'Two fixture Patients', ordinal: 0, memberIds: memberKeys }],
  })).value;
  if (cohort.sourceSelectionRevisionId !== selection.id || cohort.groupCount !== 1 || cohort.memberCount !== sourceIDs.length ||
      cohort.groups?.[0]?.memberCount !== sourceIDs.length) {
    throw new Error(`Fixture named cohort differs from its exact two-member selection: ${JSON.stringify(cohort)}`);
  }
  report.target.fixtureCohort = { selectionRevisionId: selection.id, revisionId: cohort.revisionId, groupCount: cohort.groupCount, memberCount: cohort.memberCount, sourceIDs };

  await reload(cdp, `Boolean(document.querySelector('[data-testid="construction-table-${builder.workspace.documents[0].output.id}"]'))`);
  const outputId = builder.workspace.documents.find(document => document.rootResourceType === 'Patient')?.output?.id;
  if (!outputId) throw new Error('Fixture Patient table has no output identity after reload.');
  await click(cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitFor(cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`, 10000);
  await click(cdp, '[data-testid="construction-rows-settings-trigger"]');
  const rowShapeSelector = 'select[aria-label="What should each row represent?"]';
  await waitFor(cdp, `document.querySelector(${JSON.stringify(rowShapeSelector)})?.disabled === false`, 10000);
  const cohortShape = `explicit:${cohort.revisionId}`;
  await setSelectValue(cdp, rowShapeSelector, cohortShape);
  const unmatchedSelector = 'select[aria-label="Unmatched record policy"]';
  await waitFor(cdp, `document.querySelector(${JSON.stringify(unmatchedSelector)})?.disabled === false`, 10000);
  await setSelectValue(cdp, unmatchedSelector, `${cohortShape}:ERROR`);
  await waitFor(cdp, "[...document.querySelectorAll('[aria-label=\"Row definition settings\"] button')].some(button=>button.innerText==='Apply row definition'&&!button.disabled)", 10000);
  const groupPreviewText = await evaluate(cdp, "document.querySelector('[aria-label=\"Row definition preview\"]')?.innerText || ''");
  if (!groupPreviewText.includes('2 rows → 1 rows')) throw new Error(`Fixture named cohort should collapse exactly its two Patient records: ${groupPreviewText}`);
  await click(cdp, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
  await waitFor(cdp, `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:')`, 10000);
  builder = await readBuilder();
  let document = builder.workspace.documents.find(item => item.output.id === outputId);
  if (document?.rows?.groups?.source?.explicit?.revisionId !== cohort.revisionId) throw new Error('Applying the named cohort did not save its exact explicit-group revision.');
  const legacyIDColumn = document.columns.find(column => column.source?.kind === 'field' && column.source.field?.path === 'id');
  if (!legacyIDColumn?.column || legacyIDColumn.columnId) {
    throw new Error(`Expected the first-table legacy ID to remain a non-member identity column: ${JSON.stringify(legacyIDColumn)}`);
  }

  const previewEntries = [];
  const previewByRequestId = new Map();
  const choiceProposalEntries = [];
  const commandEntries = [];
  const reconciliationEntries = [];
  const lifecycleByRequestId = new Map();
  cdp.on('Network.requestWillBeSent', event => {
    const url = new URL(event.request.url);
    const path = url.pathname;
    let body;
    try { body = event.request.postData ? JSON.parse(event.request.postData) : undefined; } catch { body = undefined; }
    if (path.endsWith('/preview')) {
      const entry = { path, outputId: body?.outputId, requestId: event.requestId, status: undefined, response: undefined };
      previewByRequestId.set(event.requestId, entry);
      previewEntries.push(entry);
      return;
    }
    const collection = path.endsWith('/construction-choice-proposals') ? choiceProposalEntries
      : path.endsWith('/commands') ? commandEntries
        : path.endsWith('/reconcile') ? reconciliationEntries
          : undefined;
    if (!collection) return;
    const entry = { path, body, requestId: event.requestId, startedAt: Date.now(), status: undefined, response: undefined };
    lifecycleByRequestId.set(event.requestId, entry);
    collection.push(entry);
  });
  cdp.on('Network.responseReceived', event => {
    const entry = previewByRequestId.get(event.requestId) ?? lifecycleByRequestId.get(event.requestId);
    if (entry) entry.status = event.response.status;
  });
  cdp.on('Network.loadingFinished', event => {
    const entry = previewByRequestId.get(event.requestId) ?? lifecycleByRequestId.get(event.requestId);
    if (!entry) return;
    cdp.send('Network.getResponseBody', { requestId: event.requestId }).then(result => {
      const text = result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      entry.response = JSON.parse(text);
      entry.completedAt = Date.now();
    }).catch(error => { entry.responseReadError = String(error); });
  });
  const waitPreview = async (afterIndex, label) => {
    const deadline = Date.now() + 5000;
    while (Date.now() < deadline) {
      const entry = previewEntries.slice(afterIndex).findLast(item => item.outputId === outputId && item.status !== undefined && (item.response || item.responseReadError));
      if (entry) {
        if (entry.status !== 200 || !entry.response?.rows) throw new Error(`${label} did not return a typed Preview: ${JSON.stringify(entry).slice(0, 1200)}`);
        return entry;
      }
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error(`${label} did not return within five seconds.`);
  };
  const waitAcceptedChoicePreview = async (proposalIndex, commandIndex, reconcileIndex, label) => {
    const deadline = Date.now() + 5000;
    while (Date.now() < deadline) {
      const proposalEntry = choiceProposalEntries.slice(proposalIndex).findLast(entry => entry.status !== undefined && (entry.response || entry.responseReadError));
      if (!proposalEntry) {
        await new Promise(resolve => setTimeout(resolve, 25));
        continue;
      }
      const proposal = proposalEntry.response;
      if (proposalEntry.status !== 200 || proposalEntry.responseReadError || proposal?.previewStatus !== 'READY' ||
          proposal.outputId !== outputId || proposal.preview?.outputId !== outputId || proposal.preview?.receiptId === undefined ||
          !Array.isArray(proposal.preview?.rows) || !proposal.candidateWorkspaceDigest || !proposal.snapshotToken) {
        throw new Error(`${label} did not capture a ready typed construction-choice Preview: ${JSON.stringify(proposalEntry).slice(0, 1400)}`);
      }
      const commandEntry = commandEntries.slice(commandIndex).findLast(entry => entry.status !== undefined && (entry.response || entry.responseReadError) &&
        entry.body?.commandId === proposal.commandId);
      if (!commandEntry) {
        await new Promise(resolve => setTimeout(resolve, 25));
        continue;
      }
      const commandResponse = commandEntry.response;
      const expectedColumns = proposal.candidateColumnIds ?? [];
      const addedColumns = (commandResponse?.results ?? [])
        .filter(result => result.type === 'COLUMN_ADDED' && result.outputId === outputId)
        .map(result => result.column).filter(Boolean);
      if (commandEntry.status !== 200 || commandEntry.responseReadError || commandResponse?.commandId !== proposal.commandId ||
          expectedColumns.length === 0 || expectedColumns.some(column => !addedColumns.includes(column))) {
        throw new Error(`${label} proposal was not applied as its exact command: ${JSON.stringify({ proposal: { commandId: proposal.commandId, candidateColumnIds: expectedColumns }, command: commandResponse }).slice(0, 1400)}`);
      }
      const reconcileEntry = reconciliationEntries.slice(reconcileIndex).findLast(entry =>
        entry.status !== undefined && (entry.response || entry.responseReadError) &&
        entry.body?.snapshotToken === proposal.snapshotToken &&
        entry.body?.draftVersion === commandResponse.draftVersion &&
        entry.body?.draftDigest === commandResponse.draftDigest);
      if (!reconcileEntry) {
        await new Promise(resolve => setTimeout(resolve, 25));
        continue;
      }
      const receipt = reconcileEntry.response;
      if (reconcileEntry.status !== 200 || reconcileEntry.responseReadError ||
          receipt?.snapshotToken !== proposal.snapshotToken || receipt?.intentDigest !== proposal.candidateWorkspaceDigest ||
          receipt?.receiptId !== proposal.preview.receiptId || !receipt.outputs?.some(output => output.outputId === outputId)) {
        throw new Error(`${label} proposal Preview was not accepted by the exact current reconcile receipt: ${JSON.stringify({ proposal: { candidateWorkspaceDigest: proposal.candidateWorkspaceDigest, previewReceiptId: proposal.preview.receiptId, outputId: proposal.outputId }, reconcileRequest: reconcileEntry.body, receipt: receipt && { receiptId: receipt.receiptId, snapshotToken: receipt.snapshotToken, intentDigest: receipt.intentDigest, outputs: receipt.outputs?.map(output => output.outputId) } }).slice(0, 1600)}`);
      }
      return {
        outputId,
        status: 200,
        response: proposal.preview,
        acceptedByReceipt: { receiptId: receipt.receiptId, snapshotToken: receipt.snapshotToken, intentDigest: receipt.intentDigest },
        delivery: 'accepted-construction-choice-proposal',
      };
    }
    throw new Error(`${label} did not produce a matching applied command and current reconcile receipt within five seconds.`);
  };
  const rawIDs = [...sourceIDs].sort();
  await waitFor(cdp, "document.querySelector('[data-testid=construction-action-add-columns]')?.disabled === false", 10000);
  await click(cdp, '[data-testid="construction-action-add-columns"]');
  await click(cdp, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitFor(cdp, "Boolean(document.querySelector('[data-testid=\"construction-add-columns-source\"]'))", 10000);
  const groupedPolicySelector = 'select[aria-label="Values per grouped row"]';
  await waitFor(cdp, `document.querySelector(${JSON.stringify(groupedPolicySelector)})?.disabled === false`, 5000);
  const groupedPolicyOptions = await evaluate(cdp,
    `[...document.querySelector(${JSON.stringify(groupedPolicySelector)}).options].map(option=>option.value)`);
  if (!groupedPolicyOptions.includes('ALL') || !groupedPolicyOptions.includes('ONE')) {
    throw new Error(`Native grouped-member policy choices are unavailable: ${JSON.stringify(groupedPolicyOptions)}`);
  }
  await setSelectValue(cdp, groupedPolicySelector, 'ALL');
  const rawFieldsOpen = await evaluate(cdp, `Boolean(document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open)`);
  if (!rawFieldsOpen) await click(cdp, '[data-testid="feature-catalog-raw-fields"] summary');
  const patientIDChoice = 'input[aria-label="Select Patient.id"]';
  await waitFor(cdp, `Boolean(document.querySelector(${JSON.stringify(patientIDChoice)}))`, 10000);
  const patientIDChoiceAction = await inspectAction(cdp, patientIDChoice);
  report.target.patientIdMemberChoice = patientIDChoiceAction;
  if (!patientIDChoiceAction.found || patientIDChoiceAction.disabled || !isActionable(patientIDChoiceAction)) {
    throw new Error(`The native Patient.id member-field candidate is unavailable or already disabled; no API fallback was used: ${JSON.stringify(patientIDChoiceAction)}`);
  }
  await click(cdp, patientIDChoice);
  await click(cdp, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitFor(cdp, `['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`, 10000);
  const fieldProposal = await evaluate(cdp, `(()=>{const panel=document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:panel?.dataset.proposalStatus,text:panel?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}})()`);
  report.target.patientIdMemberProposal = fieldProposal;
  if (fieldProposal.status !== 'ready' || fieldProposal.rows.length !== 1 || fieldProposal.rows[0].at(-1) !== rawIDs.join('; ')) {
    throw new Error(`Native ALL Patient.id proposal must expose the exact two raw fixture values on one group row: ${JSON.stringify(fieldProposal)}`);
  }
  const fieldChoiceProposalIndex = choiceProposalEntries.length - 1;
  const fieldCommandIndex = commandEntries.length;
  const fieldReconcileIndex = reconciliationEntries.length;
  await recordBrowserTiming(report, cdp, {
    name: 'apply Patient.id member field with ALL',
    action: () => click(cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' }),
    after: `!document.querySelector('[data-testid="construction-choice-proposal-panel"]')&&!document.body.innerText.includes('Loading your table…')&&document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2'`,
    timeout: 10000,
    budget: 5000,
  });
  const fieldPreview = await waitAcceptedChoicePreview(fieldChoiceProposalIndex, fieldCommandIndex, fieldReconcileIndex, 'Native ALL Patient.id member-field Preview');
  report.target.patientIdMemberPreviewDelivery = {
    delivery: fieldPreview.delivery,
    receiptId: fieldPreview.acceptedByReceipt.receiptId,
    outputId: fieldPreview.outputId,
    intentDigest: fieldPreview.acceptedByReceipt.intentDigest,
  };
  await click(cdp, 'button', { name: 'Close operation editor' });
  await waitFor(cdp, "!document.querySelector('[data-testid=\"construction-add-columns-source\"]')", 5000);
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  const idFields = document.columns.filter(column => column.source?.kind === 'field' && column.source.field?.path === 'id');
  let idColumn = idFields.find(column => column.columnId && document.rows.groups.rowValues?.some(binding => binding.columnId === column.columnId));
  if (!idFields.some(column => column.column === legacyIDColumn.column && !column.columnId)) {
    throw new Error('Adding the member Patient.id field must preserve the first-table legacy ID column.');
  }
  if (!idColumn?.columnId || !idColumn.column || idColumn.logicalType?.toLowerCase() !== 'string') {
    throw new Error(`Native Add columns did not produce a stable scalar Patient.id member column: ${JSON.stringify(idFields)}`);
  }
  const idBinding = document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId);
  if (idBinding?.policy !== 'ALL') throw new Error(`The native Patient.id member field must save its explicit ALL policy: ${JSON.stringify(idBinding)}`);
  if (JSON.stringify(fieldPreview.response.rows[0]?.[idColumn.column]) !== JSON.stringify(rawIDs)) {
    throw new Error(`The native ALL Preview must retain both literal Patient IDs on the member field: ${JSON.stringify(fieldPreview.response.rows[0]?.[idColumn.column])}`);
  }
  let renderedTypedPreview = fieldPreview.response;
  const assertRenderedMemberCell = async (expectedText, label) => {
    const typedColumns = renderedTypedPreview.columns.map(column => ({ column: column.column, label: column.label }));
    const rendered = await evaluate(cdp, `(()=>{
      const normalize=value=>String(value??'').replace(/\\s+/g,' ').trim();
      const typedColumns=${JSON.stringify(typedColumns)};
      const sourceColumn=${JSON.stringify(idColumn.column)};
      const table=document.querySelector('[data-testid=\"preview-table-scroll\"] [role=\"table\"]');
      if(!table)return {error:'Preview table is missing'};
      const rows=[...table.querySelectorAll('[role=\"row\"]')];
      const headers=[...(rows[0]?.querySelectorAll('[role=\"columnheader\"]')??[])].map(cell=>normalize(cell.innerText));
      const index=typedColumns.findIndex(column=>column.column===sourceColumn);
      const dataRows=rows.slice(1);
      const cells=[...(dataRows[0]?.querySelectorAll('[role=\"cell\"]')??[])];
      return {sourceColumn,typedColumns,typedColumnCount:typedColumns.length,headers,rowCount:dataRows.length,cellCount:cells.length,index,typedColumn:index<0?null:typedColumns[index],header:index<0?null:headers[index],value:index<0?null:normalize(cells[index]?.innerText)};
    })()`);
    if (rendered.rowCount !== 1 || rendered.typedColumns.filter(column => column.column === idColumn.column).length !== 1 ||
        rendered.typedColumn?.label?.toLowerCase() !== idColumn.label.toLowerCase() ||
        rendered.typedColumnCount !== rendered.headers.length || rendered.cellCount !== rendered.headers.length ||
        rendered.index < 0 || rendered.headers[rendered.index]?.toLowerCase() !== rendered.typedColumn.label.toLowerCase() ||
        rendered.value !== expectedText) {
      throw new Error(`${label} did not render the exact Patient.id cell at its accepted-preview source identity ${idColumn.column}: ${JSON.stringify(rendered)}`);
    }
    return rendered;
  };
  const columnsMenuOpen = async () => evaluate(cdp, "Boolean(document.querySelector('[aria-label=\"Table columns\"]'))");
  const columnsMenu = async () => {
    if (!await columnsMenuOpen()) await click(cdp, 'button', { name: 'Columns' });
    await waitFor(cdp, "Boolean(document.querySelector('[aria-label=\"Table columns\"]'))", 5000);
  };
  const closeColumnsMenu = async () => {
    if (!await columnsMenuOpen()) return;
    await click(cdp, 'button', { name: 'Columns' });
    await waitFor(cdp, "!document.querySelector('[aria-label=\"Table columns\"]')", 5000);
  };
  const configuredFeatureRowSelector = async () => {
    await columnsMenu();
    const sourceSetup = await evaluate(cdp, `(()=>{const section=document.querySelector('details[data-testid="construction-source-setup"]');return {found:Boolean(section),open:Boolean(section?.open)}})()`);
    if (!sourceSetup.found || sourceSetup.open) {
      throw new Error(`The ordinary recode control must be available while Advanced source setup remains closed: ${JSON.stringify(sourceSetup)}`);
    }
    const selector = `[aria-label="Table columns"] [role="listitem"][data-column-name=${JSON.stringify(idColumn.column)}]`;
    const state = await evaluate(cdp, `(()=>{const rows=[...document.querySelectorAll(${JSON.stringify(selector)})];return {count:rows.length,names:rows.map(row=>row.getAttribute('data-column-name')),text:rows[0]?.innerText.trim()}})()`);
    if (state.count !== 1 || state.names[0] !== idColumn.column) {
      throw new Error(`Expected one PreviewTable Columns row for stable physical source column ${idColumn.column}: ${JSON.stringify(state)}`);
    }
    report.target.memberFieldRecodeAccess = {
      path: 'Preview and configure → Columns → Patient.id → Recode exact category values',
      sourceColumn: idColumn.column,
      advancedSourceSetupOpened: false,
      advancedSourceSetupRemainedClosed: true,
      ordinaryMenuRowCount: state.count,
    };
    return selector;
  };
  const configuredFeatureControl = async control => `${await configuredFeatureRowSelector()} ${control}`;
  const openConfiguredFeatureEditor = async summaryText => {
    const selector = await configuredFeatureControl('summary');
    const state = await evaluate(cdp, `(()=>{const summary=document.querySelector(${JSON.stringify(selector)});return {found:Boolean(summary),text:summary?.innerText.trim(),open:Boolean(summary?.closest('details')?.open)}})()`);
    if (!state.found || state.text !== summaryText) {
      throw new Error(`Configured Patient.id feature editor did not expose ${summaryText}: ${JSON.stringify(state)}`);
    }
    if (!state.open) await click(cdp, selector, { name: summaryText });
  };
  const clickConfiguredFeatureControl = async (control, name) => {
    await click(cdp, await configuredFeatureControl(control), { name });
  };
  const policySelector = `[aria-label="Table columns"] [data-column-name=${JSON.stringify(idColumn.column)}] select[aria-label^="Values per cohort member for "]`;
  const changePolicy = async (fromPolicy, toPolicy) => {
    await columnsMenu();
    await waitFor(cdp, `document.querySelector(${JSON.stringify(policySelector)})?.value===${JSON.stringify(fromPolicy)}`, 5000);
    const previewIndex = previewEntries.length;
    await setSelectValue(cdp, policySelector, toPolicy);
    const nextPreview = await waitPreview(previewIndex, `Patient.id ${fromPolicy}→${toPolicy} automatic Preview`);
    renderedTypedPreview = nextPreview.response;
    return nextPreview;
  };
  const rawValues = await evaluate(cdp, `(()=>{const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');const rows=[...table.querySelectorAll('[role="row"]')];return rows.slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim()))})()`);
  if (!rawValues.some(row => row.some(value => rawIDs.every(id => value.includes(id))))) {
    throw new Error(`Grouped Preview does not expose exactly the two raw Patient IDs yet: ${JSON.stringify(rawValues)}`);
  }
  recordCheck(report, 'correctness', 'fixture cohort binds exactly the two independent Patient IDs', true, { sourceIDs, revisionId: cohort.revisionId });
  const initialRawCell = await assertRenderedMemberCell(rawIDs.join('; '), 'Initial raw ALL Preview');
  recordCheck(report, 'correctness', 'Patient.id starts as distinct raw values under ALL', true, { expected: rawIDs, rendered: { rawValues, cell: initialRawCell } });

  const category = 'Shared fixture category';
  const mappings = rawIDs.map(from => ({ from, to: category }));
  const transform = { kind: 'EXACT_CATEGORY_RECODE', exactCategoryRecode: { mappings, unknownPolicy: 'KEEP_ORIGINAL' } };
  await openConfiguredFeatureEditor('Recode exact category values');
  for (let index = 0; index < mappings.length; index += 1) {
    const mapping = mappings[index];
    await clickConfiguredFeatureControl('button', 'Add mapping');
    await fill(cdp, await configuredFeatureControl(`input[aria-label=${JSON.stringify(`Recorded category ${index + 1} for ${idColumn.label}`)}]`), mapping.from);
    await fill(cdp, await configuredFeatureControl(`input[aria-label=${JSON.stringify(`Replacement value ${index + 1} for ${idColumn.label}`)}]`), mapping.to);
  }
  await setSelectValue(cdp, await configuredFeatureControl(`select[aria-label=${JSON.stringify(`Unmapped value policy for ${idColumn.label}`)}]`), 'KEEP_ORIGINAL');
  const recodePreviewIndex = previewEntries.length;
  await clickConfiguredFeatureControl('button', 'Save recoding');
  let preview = await waitPreview(recodePreviewIndex, 'Recoded Patient.id ALL Preview');
  renderedTypedPreview = preview.response;
  if (JSON.stringify(preview.response.rows[0]?.[idColumn.column]) !== JSON.stringify([category])) {
    throw new Error(`ALL must apply recoding to both raw Patient IDs before reducing unique values: ${JSON.stringify(preview.response.rows[0]?.[idColumn.column])}`);
  }
  const recodedAllCell = await assertRenderedMemberCell(category, 'Recoded ALL Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  idColumn = document.columns.find(column => column.columnId === idColumn.columnId);
  if (JSON.stringify(idColumn.valueTransformation) !== JSON.stringify(transform) ||
      document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy !== 'ALL') {
    throw new Error('Saved category recoding or initial ALL policy differs from the literal browser-authored transformation.');
  }
  recordCheck(report, 'correctness', 'different literal Patient IDs recode to one shared category under ALL', true, { rawIDs, mappings, previewValue: preview.response.rows[0]?.[idColumn.column], rendered: recodedAllCell });

  await recordBrowserTiming(report, cdp, {
    name: 'edit transformed cohort ALL to ONE',
    action: async () => { preview = await changePolicy('ALL', 'ONE'); },
    after: `document.body.innerText.includes(${JSON.stringify(category)})&&!document.body.innerText.includes('Loading your table…')`,
    timeout: 5000,
    budget: 5000,
  });
  if (preview.response.rows[0]?.[idColumn.column] !== category) throw new Error(`ONE must return the shared scalar category: ${JSON.stringify(preview.response.rows[0]?.[idColumn.column])}`);
  const recodedOneCell = await assertRenderedMemberCell(category, 'Recoded ONE Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  if (document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy !== 'ONE') throw new Error('The accepted ONE edit was not saved on the same Patient.id binding.');
  recordCheck(report, 'correctness', 'ONE accepts different raw Patient IDs after they recode to the same category', true, { value: preview.response.rows[0]?.[idColumn.column], rendered: recodedOneCell });
  await click(cdp, 'button', { name: 'Columns' });
  await waitFor(cdp, "!document.querySelector('[aria-label=\"Table columns\"]')", 5000);
  await reload(cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitFor(cdp, "!document.body.innerText.includes('Loading your table…')&&document.body.innerText.includes('Shared fixture category')", 10000);
  const reloadedOneCell = await assertRenderedMemberCell(category, 'Reloaded transformed ONE Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  idColumn = document.columns.find(column => column.columnId === idColumn.columnId);
  if (document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy !== 'ONE' ||
      JSON.stringify(idColumn.valueTransformation) !== JSON.stringify(transform)) {
    throw new Error('Reload did not preserve the exact transformed ONE cohort binding.');
  }
  recordCheck(report, 'persistence', 'transformed ONE policy and exact recoding survive reload', true, { policy: 'ONE', transformation: idColumn.valueTransformation, rendered: reloadedOneCell });

  const allPreview = await changePolicy('ONE', 'ALL');
  if (JSON.stringify(allPreview.response.rows[0]?.[idColumn.column]) !== JSON.stringify([category])) {
    throw new Error(`Returning ONE→ALL must preserve the single shared category array: ${JSON.stringify(allPreview.response.rows[0]?.[idColumn.column])}`);
  }
  const recodedAllAgainCell = await assertRenderedMemberCell(category, 'Recoded ALL restoration Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  if (document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy !== 'ALL') throw new Error('The return to ALL was not saved on the same Patient.id binding.');
  recordCheck(report, 'correctness', 'returning from ONE to ALL restores the shared-category array', true, { value: allPreview.response.rows[0]?.[idColumn.column], rendered: recodedAllAgainCell });
  await openConfiguredFeatureEditor('Edit exact category recoding');
  const restorePreviewIndex = previewEntries.length;
  await clickConfiguredFeatureControl('button', 'Remove recoding');
  preview = await waitPreview(restorePreviewIndex, 'Raw Patient.id ALL Preview after removing recoding');
  renderedTypedPreview = preview.response;
  if (JSON.stringify(preview.response.rows[0]?.[idColumn.column]) !== JSON.stringify(rawIDs)) {
    throw new Error(`Removing recoding must restore the two exact raw Patient IDs under ALL: ${JSON.stringify(preview.response.rows[0]?.[idColumn.column])}`);
  }
  const restoredRawCell = await assertRenderedMemberCell(rawIDs.join('; '), 'Raw ALL Preview after removing recoding');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  idColumn = document.columns.find(column => column.columnId === idColumn.columnId);
  if (idColumn.valueTransformation || document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId)?.policy !== 'ALL') {
    throw new Error('Removing recoding must preserve the same stable Patient.id binding as raw ALL.');
  }
  recordCheck(report, 'correctness', 'removing recoding restores both exact raw Patient IDs under ALL', true, { value: preview.response.rows[0]?.[idColumn.column], rendered: restoredRawCell });
  await closeColumnsMenu();
  await reload(cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitFor(cdp, "!document.body.innerText.includes('Loading your table…')", 10000);
  const finalRawCell = await assertRenderedMemberCell(rawIDs.join('; '), 'Final reloaded raw ALL Preview');
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  idColumn = document.columns.find(column => column.columnId === idColumn.columnId);
  const finalIDBinding = document.rows.groups.rowValues.find(binding => binding.columnId === idColumn.columnId);
  if (idColumn.valueTransformation || finalIDBinding?.policy !== 'ALL') throw new Error('Raw untransformed ALL cohort state was not retained after the final reload.');
  recordCheck(report, 'persistence', 'raw ALL restoration survives reload on the same cohort and column identity', true, { columnId: idColumn.columnId, revisionId: cohort.revisionId, rendered: finalRawCell });
  report.target.uncoveredAdjacentBehavior = 'Raw ALL→ONE rejection for the distinct Patient IDs is not exercised in this basic fixture cycle; the CDA transformed-category driver retains its raw disagreement rejection assertion.';
});

const runSuggestions = (context) => runBrowserCase(context, 'builder-authoring', 'suggestions', async ({ cdp, report }) => {
  const { explorer } = await createBlankExplorer(cdp, context.target, context.runID, 'suggestions', report);
  await addPatientTableRoot(cdp, report, 'Patients');
  await waitFor(cdp, "document.body.innerText.includes('dev-patient-001') && document.querySelector('[data-testid=construction-action-add-columns]:not(:disabled)')", 30000);
  await click(cdp, 'button', { includes: 'Add columns:' });
  await waitFor(cdp, "document.querySelector('[aria-label=\"Add columns editor\"]')", 10000);
  await click(cdp, 'button', { name: 'Fields and related data' });
  await waitFor(cdp, "document.querySelector('#feature-catalog-search')", 10000);
  await captureDOM(report, cdp, 'add-columns-editor');
  const candidateCount = await evaluate(cdp, "document.querySelectorAll('[aria-label=\"Add columns editor\"] input[aria-label^=\"Select Patient.\"]').length");
  await click(cdp, 'summary', { name: 'Raw FHIR fields (advanced)' });
  await waitFor(cdp, "document.querySelector('input[aria-label=\"Select Patient.id\"]')", 10000);
  await click(cdp, 'input[type=checkbox][aria-label]', { name: 'Select Patient.id' });
  const idCandidate = await inspectAction(cdp, 'input[type=checkbox][aria-label]', { name: 'Select Patient.id' });
  recordCheck(report, 'correctness', 'catalog-backed Patient candidates are rendered', Number(candidateCount) > 0, { candidateCount });
  recordCheck(report, 'usability', 'Patient ID candidate control is visible and actionable', isActionable(idCandidate), idCandidate ?? {});
  recordUntested(report, 'usability', 'lazy suggestion request failure and recovery', 'The isolated development catalog already includes Patient candidates, so ensureSuggestions returns without issuing a suggestions request.');
  report.target.explorer = explorer;
});

const runAuthoring = (context) => runBrowserCase(context, 'builder-authoring', 'authoring', async ({ cdp, report }) => {
  const { explorer, title } = await createBlankExplorer(cdp, context.target, context.runID, 'authoring', report);
  await addPatientTableRoot(cdp, report);
  await configurePatientColumns(cdp, report);
  await previewPatientRows(cdp, report);
  await publishPatientExplorer(cdp, report);
  await reload(cdp, "document.body.innerText.includes('DATASET WORKSPACE') && document.querySelector('button[aria-label^=\"Select Patient ID\"]') && document.querySelector('button[aria-label^=\"Select Gender\"]')");
  const restored = await evaluate(cdp, "({explorer:document.querySelector('select[aria-label=\"Explorer\"]')?.value,title:document.querySelector('select[aria-label=\"Explorer\"] option:checked')?.textContent.trim(),id:Boolean(document.querySelector('button[aria-label^=\"Select Patient ID\"]')),gender:Boolean(document.querySelector('button[aria-label^=\"Select Gender\"]'))})");
  recordCheck(report, 'persistence', 'published Builder table and configured fields survive reload', restored.explorer === explorer && restored.title === title && Boolean(restored.id && restored.gender), restored);
  report.target.explorer = explorer;
  report.target.table = 'Patients';
});

export const runBuilderAuthoring = async (context, caseNames) => {
  const reports = [];
  for (const caseName of caseNames) {
    if (caseName === 'suggestions') reports.push(await runSuggestions(context));
    else if (caseName === 'authoring') reports.push(await runAuthoring(context));
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
