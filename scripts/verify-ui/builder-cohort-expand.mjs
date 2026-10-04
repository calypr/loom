import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { runBrowserCase } from './common.mjs';
import { click, evaluate, fill, inspectAction, recordBrowserTiming, reload, waitFor } from './browser.mjs';
import { isActionable, recordCheck } from './report.mjs';
import { addPatientTableRoot, createBlankExplorer } from './workflows.mjs';

const expectedSourceIDs = ['dev-patient-001', 'dev-patient-002'];
const cohortLabel = 'Two fixture Patients';
const tableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
const rowTableReady = (count) => `(()=>{const table=document.querySelector(${JSON.stringify(tableSelector)});return Boolean(table&&table.getAttribute('aria-rowcount')===${JSON.stringify(String(count + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;
const proposalReady = `Boolean(document.querySelector('[data-testid="construction-proposal-panel"][data-proposal-status="ready"]'))`;
const proposalTableReady = (count) => `(()=>{const proposal=document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]');const table=proposal?.querySelector('table');return Boolean(table&&table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length===${count})})()`;
const normalize = (value) => String(value ?? '').replace(/\s+/g, ' ').trim();

const setSelectValue = async (cdp, selector, value) => {
  const action = await inspectAction(cdp, selector);
  if (!isActionable(action)) throw new Error(`Select control is not actionable: ${JSON.stringify(action)}`);
  return evaluate(cdp,
    `(()=>{const select=document.querySelector(${JSON.stringify(selector)});if(!select)throw Error('select not found: '+${JSON.stringify(selector)});const setter=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value')?.set;if(!setter)throw Error('native select setter unavailable');setter.call(select,${JSON.stringify(value)});select.dispatchEvent(new Event('input',{bubbles:true}));select.dispatchEvent(new Event('change',{bubbles:true}));return select.value})()`);
};

const requireCheck = (report, dimension, name, passed, evidence = {}) => {
  recordCheck(report, dimension, name, passed, evidence);
  if (!passed) throw new Error('required cohort-expand check failed: ' + name);
};

const parseNDJSON = (path) => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(Boolean)
  .map((line) => JSON.parse(line));

const readGrid = async (cdp) => evaluate(cdp, `(()=>{
  const proposal=document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]');
  const table=proposal?.querySelector('table')??document.querySelector(${JSON.stringify(tableSelector)});
  if(!table)return {ready:false,headers:[],rows:[],ariaRowCount:null};
  const normalize=value=>String(value??'').replace(/\\s+/g,' ').trim();
  const headers=proposal
    ? [...table.querySelectorAll('thead th')].map(cell=>normalize(cell.querySelector('span')?.innerText ?? cell.innerText))
    : [...table.querySelectorAll('[role="columnheader"]')].map(cell=>normalize(cell.innerText));
  const rows=proposal
    ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>normalize(cell.innerText)))
    : [...table.querySelectorAll('[role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>normalize(cell.innerText)));
  return {ready:true,headers,rows,proposal:Boolean(proposal),ariaRowCount:table.getAttribute('aria-rowcount')};
})()`);

const assertGroupPairs = (grid, expectedIDs, expectedItemLabel, phase) => {
  assert(grid.ready, `${phase} omitted the rendered Builder Preview`);
  const groupIndex = grid.headers.findIndex((header) => header.toLowerCase() === 'group label');
  const itemIndex = grid.headers.findIndex((header) => header.toLowerCase() === expectedItemLabel.toLowerCase());
  assert(groupIndex >= 0, `${phase} omitted the named cohort label column: ${JSON.stringify(grid.headers)}`);
  assert(itemIndex >= 0, `${phase} omitted ${expectedItemLabel}: ${JSON.stringify(grid.headers)}`);
  const pairs = grid.rows.map((row) => [row[groupIndex], row[itemIndex]])
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const expected = expectedIDs.map((id) => [cohortLabel, id])
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  assert.deepEqual(pairs, expected, `${phase} must retain the cohort label and exact member IDs`);
  return { headers: grid.headers, rowCount: grid.rows.length, pairs };
};

const run = (context) => runBrowserCase(context, 'builder-authoring', 'cohort-expand', async ({ cdp, report }) => {
  assert.equal(context.custom, false, 'This authoring case requires an owned isolated fixture project.');
  assert.equal(context.seed?.fresh, true, 'This case must use a fresh verification project.');
  assert(context.target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected a fresh loom_dev_verify project.');
  assert(context.target.fixtureGeneration && context.target.fixtureDir, 'Expected a project fixture generation and directory.');

  const rawPatients = parseNDJSON(join(context.target.fixtureDir, 'Patient.ndjson'))
    .filter((resource) => resource?.resourceType === 'Patient');
  const sourceIDs = rawPatients.map((resource) => resource.id).sort();
  assert.deepEqual(sourceIDs, expectedSourceIDs, 'The basic cohort fixture must contain exactly the two expected Patient IDs.');
  report.target.fixtureRawOracle = {
    source: 'fresh project Patient.ndjson',
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    resourceType: 'Patient',
    sourceIDs,
    listValue: sourceIDs,
  };
  requireCheck(report, 'correctness', 'two fixture Patient rows are read from the independent fixture oracle', true, report.target.fixtureRawOracle);

  const { explorer } = await createBlankExplorer(cdp, context.target, context.runID, 'cohort-expand', report);
  report.target.explorer = explorer;
  await addPatientTableRoot(cdp, report, 'Patients');

  const project = context.target.fixtureProject;
  const generation = context.target.fixtureGeneration;
  const selectionProject = project.replace('-', '/');
  const apiRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
  report.target.fixtureSetupRequests = [];
  const requestJSON = async (path, body) => {
    const response = await fetch(context.target.apiUrl + path, {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    report.target.fixtureSetupRequests.push({ method: body === undefined ? 'GET' : 'POST', path, status: response.status });
    if (!response.ok) throw new Error(`Fixture cohort setup ${path} returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 900)}`);
    return value;
  };
  const readBuilder = () => requestJSON(`${apiRoot}/authoring/v2/builder`);
  const builderHasPatientTable = async () => {
    const builder = await readBuilder();
    assert.equal(builder.catalog?.generation, generation, 'Builder catalog generation differs from the isolated fixture.');
    assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest, 'Builder must return a scoped current snapshot.');
    const document = builder.workspace.documents.find((item) => item.rootResourceType === 'Patient');
    assert(document?.output?.id, 'Fresh Explorer must contain its native Patient table.');
    return { builder, outputId: document.output.id };
  };

  let { builder, outputId } = await builderHasPatientTable();
  const refs = sourceIDs.map((id) => ({ project: selectionProject, generation, resourceType: 'Patient', id }));
  const selection = await requestJSON(`${apiRoot}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `verify-cohort-expand-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs } },
  });
  assert.equal(selection.project, selectionProject);
  assert.equal(selection.generation, generation);
  assert.equal(selection.resourceType, 'Patient');
  assert.equal(selection.scopeDigest, builder.catalog.authorizationScopeDigest);
  assert.equal(selection.memberCount, sourceIDs.length);
  const selectionPage = await requestJSON(`${apiRoot}/selections/${encodeURIComponent(selection.id)}?limit=100`);
  const selectedRefs = (selectionPage.members ?? []).map((member) => member.ref)
    .map((ref) => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  const expectedRefs = refs.map((ref) => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  assert.equal(selectionPage.revision?.id, selection.id);
  assert.equal(selectionPage.revision?.scopeDigest, builder.catalog.authorizationScopeDigest);
  assert.deepEqual(selectedRefs, expectedRefs, 'Selection readback must exactly match the raw Patient fixture IDs.');
  const memberKeys = selectionPage.members.map((member) => member.memberKey);
  assert.equal(new Set(memberKeys).size, sourceIDs.length);
  const cohort = await requestJSON(`${apiRoot}/selections/${encodeURIComponent(selection.id)}/explicit-groups`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `verify-cohort-expand-group-${randomUUID()}`,
    groups: [{ id: 'verify-cohort-expand', label: cohortLabel, ordinal: 0, memberIds: memberKeys }],
  });
  assert.equal(cohort.sourceSelectionRevisionId, selection.id);
  assert.equal(cohort.groupCount, 1);
  assert.equal(cohort.memberCount, sourceIDs.length);
  assert.equal(cohort.groups?.[0]?.label, cohortLabel);
  assert.equal(cohort.groups?.[0]?.memberCount, sourceIDs.length);
  report.target.fixtureCohort = {
    project: selectionProject,
    generation,
    sourceIDs,
    label: cohortLabel,
    selectionRevisionId: selection.id,
    revisionId: cohort.revisionId,
    scopeDigest: selection.scopeDigest,
    groupCount: cohort.groupCount,
    memberCount: cohort.memberCount,
  };
  report.target.fixtureSetupBoundary = 'POST selection and explicit-group endpoints seed fixture state only; row configuration, member-field authoring, EXPAND, edit, cancel, apply, and removal are all native Builder controls.';
  requireCheck(report, 'correctness', 'fixture named cohort revision contains exactly the two Patients', true, report.target.fixtureCohort);

  const outputSelector = `[data-testid="construction-table-${outputId}"]`;
  await reload(cdp, `Boolean(document.querySelector(${JSON.stringify(outputSelector)}))`);
  await click(cdp, outputSelector);
  await waitFor(cdp, "document.querySelector('[data-testid=construction-rows-settings-trigger]')?.disabled === false", 10000);

  const rowShapeSelector = 'select[aria-label="What should each row represent?"]';
  const cohortShape = `explicit:${cohort.revisionId}`;
  const unmatchedSelector = 'select[aria-label="Unmatched record policy"]';
  await recordBrowserTiming(report, cdp, {
    name: 'select exact named cohort row shape',
    action: async () => {
      await click(cdp, '[data-testid="construction-rows-settings-trigger"]');
      await waitFor(cdp, `Boolean(document.querySelector(${JSON.stringify(rowShapeSelector)}))`, 5000);
      const shapeAction = await inspectAction(cdp, rowShapeSelector);
      if (!isActionable(shapeAction)) throw new Error(`Named cohort row-shape select is not actionable: ${JSON.stringify(shapeAction)}`);
      await setSelectValue(cdp, rowShapeSelector, cohortShape);
      await waitFor(cdp, `document.querySelector(${JSON.stringify(unmatchedSelector)})?.disabled === false`, 5000);
      await setSelectValue(cdp, unmatchedSelector, `${cohortShape}:ERROR`);
    },
    after: `(()=>{const preview=document.querySelector('[aria-label="Row definition preview"]')?.innerText||'';const apply=[...document.querySelectorAll('[aria-label="Row definition settings"] button')].find(button=>button.innerText.trim()==='Apply row definition');return preview.includes('2 rows → 1 rows')&&Boolean(apply&&!apply.disabled)})()`,
    timeout: 10000,
    budget: 5000,
  });
  requireCheck(report, 'usability', 'native Configure rows exposes and selects the exact saved cohort', true, { revisionId: cohort.revisionId, policy: 'ERROR' });
  await recordBrowserTiming(report, cdp, {
    name: 'apply named cohort row definition',
    action: () => click(cdp, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' }),
    after: rowTableReady(1),
    timeout: 10000,
    budget: 5000,
  });
  let grid = await readGrid(cdp);
  const groupLabelIndex = grid.headers.findIndex((header) => header.toLowerCase() === 'group label');
  assert(groupLabelIndex >= 0 && grid.rows.length === 1 && grid.rows[0][groupLabelIndex] === cohortLabel,
    `Applying the cohort must render its exact name: ${JSON.stringify(grid)}`);
  builder = await readBuilder();
  let document = builder.workspace.documents.find((item) => item.output.id === outputId);
  assert.equal(document?.rows?.kind, 'GROUPS');
  assert.equal(document?.rows?.groups?.source?.explicit?.revisionId, cohort.revisionId);
  requireCheck(report, 'correctness', 'native Configure rows applies the exact cohort and retains its displayed name', true, { grid, revisionId: cohort.revisionId });

  await waitFor(cdp, "document.querySelector('[data-testid=construction-action-add-columns]')?.disabled === false", 10000);
  const memberPolicySelector = 'select[aria-label="Values per grouped row"]';
  const rawFieldDetails = '[data-testid="feature-catalog-raw-fields"]';
  const patientIDChoice = 'input[aria-label="Select Patient.id"]';
  await recordBrowserTiming(report, cdp, {
    name: 'propose Patient.id cohort member field with ALL',
    action: async () => {
      await click(cdp, '[data-testid="construction-action-add-columns"]');
      await waitFor(cdp, `Boolean(document.querySelector('[aria-label="Add columns editor"]'))`, 5000);
      await click(cdp, '[aria-label="Add columns editor"] button', { includes: 'Fields and related data' });
      await waitFor(cdp, `document.querySelector(${JSON.stringify(memberPolicySelector)})?.disabled === false`, 5000);
      const policyAction = await inspectAction(cdp, memberPolicySelector);
      if (!isActionable(policyAction)) throw new Error(`Grouped member policy select is not actionable: ${JSON.stringify(policyAction)}`);
      await setSelectValue(cdp, memberPolicySelector, 'ALL');
      await waitFor(cdp, `Boolean(document.querySelector(${JSON.stringify(rawFieldDetails)}))`, 5000);
      const rawFieldsOpen = await evaluate(cdp, `Boolean(document.querySelector(${JSON.stringify(rawFieldDetails)})?.open)`);
      if (!rawFieldsOpen) await click(cdp, `${rawFieldDetails} summary`);
      await waitFor(cdp, `Boolean(document.querySelector(${JSON.stringify(patientIDChoice)}))`, 5000);
      const candidateAction = await inspectAction(cdp, patientIDChoice);
      if (!isActionable(candidateAction)) throw new Error(`Patient.id field is not natively selectable: ${JSON.stringify(candidateAction)}`);
      await click(cdp, patientIDChoice);
      await click(cdp, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
    },
    after: `document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus==='ready'`,
    timeout: 10000,
    budget: 5000,
  });
  const memberProposal = await evaluate(cdp, `(()=>{const panel=document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {status:panel?.dataset.proposalStatus,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}})()`);
  assert.equal(memberProposal.status, 'ready');
  assert.equal(memberProposal.rows.length, 1);
  assert.equal(memberProposal.rows[0].at(-1), sourceIDs.join('; '), `Native ALL member preview must contain both exact source IDs: ${JSON.stringify(memberProposal)}`);
  report.target.memberFieldProposal = memberProposal;
  await recordBrowserTiming(report, cdp, {
    name: 'apply Patient.id member field with ALL',
    action: () => click(cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' }),
    after: `!document.querySelector('[data-testid="construction-choice-proposal-panel"]')&&${rowTableReady(1)}`,
    timeout: 10000,
    budget: 5000,
  });
  grid = await readGrid(cdp);
  const idLabel = grid.headers.find((header) => header.toLowerCase() === 'patient id');
  assert(idLabel, `ALL Preview must render the Patient ID member field: ${JSON.stringify(grid.headers)}`);
  assert.equal(grid.rows.length, 1);
  const idIndex = grid.headers.indexOf(idLabel);
  assert.equal(grid.rows[0][idIndex], sourceIDs.join('; '), `Saved ALL list must render exact fixture IDs: ${JSON.stringify(grid)}`);
  builder = await readBuilder();
  document = builder.workspace.documents.find((item) => item.output.id === outputId);
  const idColumn = document.columns.find((column) => column.source?.kind === 'field' && column.source.field?.path === 'id' && column.columnId && document.rows.groups.rowValues?.some((binding) => binding.columnId === column.columnId));
  assert(idColumn?.columnId && idColumn.column, `Native Add columns did not create a stable Patient.id member field: ${JSON.stringify(document.columns)}`);
  const memberBinding = document.rows.groups.rowValues.find((binding) => binding.columnId === idColumn.columnId);
  assert.equal(memberBinding?.policy, 'ALL');
  assert.equal(document.rows.groups.source.explicit.revisionId, cohort.revisionId);
  const cohortBaseline = { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, columnId: idColumn.columnId, physicalColumn: idColumn.column, binding: memberBinding };
  report.target.memberField = { ...cohortBaseline, label: idColumn.label, policy: 'ALL', rawValues: sourceIDs };
  requireCheck(report, 'correctness', 'Patient.id is added natively with ALL and its exact two-value array', true, { grid, memberField: report.target.memberField, revisionId: cohort.revisionId });

  const openRowsDialog = async () => {
    if (!await evaluate(cdp, `Boolean(document.querySelector('[aria-label="Row definition settings"]'))`)) {
      if (!await evaluate(cdp, `Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]'))`)) {
        await click(cdp, '[data-testid="construction-close-operation-editor"]');
      }
      await waitFor(cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`, 5000);
      await click(cdp, '[data-testid="construction-rows-settings-trigger"]');
      await waitFor(cdp, `Boolean(document.querySelector('[aria-label="Row definition settings"]'))`, 5000);
    }
  };
  const enterExpansion = async (name) => {
    await openRowsDialog();
    const action = await inspectAction(cdp, '[data-testid="construction-action-expand-rows"]');
    report.target.expandRowsControl = action;
    requireCheck(report, 'usability', 'native Configure rows exposes an actionable Make one row per list value control', isActionable(action), action);
    if (!isActionable(action)) throw new Error(`Authored list EXPAND is unavailable for the saved cohort member ALL array: ${JSON.stringify(action)}`);
    await recordBrowserTiming(report, cdp, {
      name,
      action: () => click(cdp, '[data-testid="construction-action-expand-rows"]'),
      after: `Boolean(document.querySelector('[aria-label="Expand repeated values"]'))&&${proposalReady}&&${proposalTableReady(2)}`,
      timeout: 10000,
      budget: 5000,
    });
    const selection = await evaluate(cdp, `(()=>{const select=document.querySelector('select[aria-label="Repeated field"]');return {value:select?.value,label:select?.selectedOptions?.[0]?.textContent?.trim()}})()`);
    assert(selection.value && selection.label?.toLowerCase().includes(idColumn.label.toLowerCase()), `EXPAND must default to the saved Patient.id ALL column: ${JSON.stringify(selection)}`);
    return selection;
  };
  const checkCohortState = async (value, expectedSteps, phase) => {
    const current = await readBuilder();
    const saved = current.workspace.documents.find((item) => item.output.id === outputId);
    assert.equal(saved?.rows?.kind, 'GROUPS', `${phase} must keep the named-cohort row source.`);
    assert.equal(saved?.rows?.groups?.source?.explicit?.revisionId, cohort.revisionId, `${phase} must keep the exact cohort revision.`);
    assert.deepEqual(saved?.rows?.groups?.rowValues?.find((binding) => binding.columnId === idColumn.columnId)?.policy, 'ALL', `${phase} must keep the ALL member binding.`);
    assert.equal(saved?.construction?.steps?.length ?? 0, expectedSteps, `${phase} must have the expected authored row-operation history.`);
    if (value) {
      const currentGrid = await readGrid(cdp);
      const rendered = assertGroupPairs(currentGrid, sourceIDs, value, phase);
      return { builder: current, document: saved, rendered };
    }
    return { builder: current, document: saved };
  };
  const assertAppliedCohortRows = async (label, phase) => {
    const rendered = assertGroupPairs(await readGrid(cdp), sourceIDs, label, phase);
    const state = await checkCohortState(label, 1, phase);
    return { rendered, document: state.document, builder: state.builder };
  };

  await enterExpansion('open authored list EXPAND proposal for Cancel');
  const firstExpandSelection = await evaluate(cdp, `(()=>{const select=document.querySelector('select[aria-label="Repeated field"]');return {value:select?.value,label:select?.selectedOptions?.[0]?.textContent?.trim()}})()`);
  const canceledPreview = assertGroupPairs(await readGrid(cdp), sourceIDs, idColumn.label + ' item', 'Initial automatic EXPAND Preview before Cancel');
  requireCheck(report, 'correctness', 'automatic authored EXPAND proposal renders both exact IDs and repeats the named cohort label', true, { preview: canceledPreview, input: firstExpandSelection, cohort: report.target.fixtureCohort });
  const beforeCancel = await readBuilder();
  assert.equal(beforeCancel.draftVersion, cohortBaseline.draftVersion);
  assert.equal(beforeCancel.draftDigest, cohortBaseline.draftDigest);
  const cancelButton = await inspectAction(cdp, '[data-testid="construction-cancel-proposal"]');
  requireCheck(report, 'usability', 'native EXPAND proposal exposes an actionable Cancel control', isActionable(cancelButton), cancelButton);
  await recordBrowserTiming(report, cdp, {
    name: 'cancel authored list EXPAND proposal',
    action: () => click(cdp, '[data-testid="construction-cancel-proposal"]'),
    after: `!document.querySelector('[data-testid="construction-proposal-panel"]')&&${rowTableReady(1)}`,
    timeout: 10000,
    budget: 5000,
  });
  grid = await readGrid(cdp);
  assert.equal(grid.rows.length, 1);
  assert.equal(grid.rows[0][grid.headers.findIndex(header => header.toLowerCase() === 'patient id')], sourceIDs.join('; '));
  const canceledState = await checkCohortState(undefined, 0, 'Cancel');
  assert.equal(canceledState.builder.draftVersion, cohortBaseline.draftVersion);
  assert.equal(canceledState.builder.draftDigest, cohortBaseline.draftDigest);
  requireCheck(report, 'correctness', 'Cancel leaves cohort shape, membership, and ALL array unchanged', true, { grid, revisionId: cohort.revisionId, sourceIDs, draftVersion: canceledState.builder.draftVersion, draftDigest: canceledState.builder.draftDigest });

  await enterExpansion('open authored list EXPAND proposal for Apply');
  const actualInput = await evaluate(cdp, `(()=>{const select=document.querySelector('select[aria-label="Repeated field"]');return {value:select?.value,label:select?.selectedOptions?.[0]?.textContent?.trim()}})()`);
  assert.deepEqual(actualInput, firstExpandSelection, 'Cancel/re-entry must retain the same source list binding.');
  await click(cdp, '[data-testid="construction-reshape-expand-advanced"] summary');
  const initialOutputLabel = await evaluate(cdp, `document.querySelector('input[aria-label="Expanded item label"]')?.value||''`);
  assert(initialOutputLabel, 'EXPAND must expose its saved item label for editing.');
  const editedOutputLabel = 'Cohort member ID';
  await recordBrowserTiming(report, cdp, {
    name: 'edit authored EXPAND item label before Apply',
    action: () => fill(cdp, 'input[aria-label="Expanded item label"]', editedOutputLabel),
    after: `Boolean(document.querySelector('[data-testid="construction-proposal-panel"][data-proposal-status="ready"]'))&&${proposalTableReady(2)}&&[...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')].some(header=>(header.querySelector('span')?.innerText ?? header.innerText).trim().toLowerCase()===${JSON.stringify(editedOutputLabel.toLowerCase())})`,
    timeout: 10000,
    budget: 5000,
  });
  const proposedRows = assertGroupPairs(await readGrid(cdp), sourceIDs, editedOutputLabel, 'Edited automatic EXPAND proposal');
  builder = await readBuilder();
  assert.equal(builder.draftVersion, cohortBaseline.draftVersion, 'Proposal preview must not alter the saved Builder draft.');
  assert.equal(builder.draftDigest, cohortBaseline.draftDigest, 'Proposal preview must not alter the saved Builder digest.');
  const applyProposal = await inspectAction(cdp, '[data-testid="construction-apply-proposal"]');
  requireCheck(report, 'usability', 'native EXPAND proposal exposes an actionable Apply control after exact preview', isActionable(applyProposal), applyProposal);
  await recordBrowserTiming(report, cdp, {
    name: 'apply authored list EXPAND',
    action: () => click(cdp, '[data-testid="construction-apply-proposal"]'),
    after: `!document.querySelector('[data-testid="construction-proposal-panel"]')&&${rowTableReady(2)}`,
    timeout: 10000,
    budget: 5000,
  });
  let applied = await assertAppliedCohortRows(editedOutputLabel, 'Applied EXPAND');
  let savedStep = applied.document.construction.steps.at(-1);
  assert.equal(applied.document.construction.steps.length, 1);
  assert.equal(savedStep.operation.kind, 'EXPAND');
  assert.equal(savedStep.operation.expand.inputColumnId, actualInput.value);
  assert.equal(savedStep.operation.expand.emptyPolicy, 'PRESERVE_PARENT');
  const savedOutput = savedStep.outputs.find((column) => column.id === savedStep.operation.expand.outputColumnId);
  assert.equal(savedOutput?.label, editedOutputLabel);
  assert.equal(savedOutput?.id, savedStep.operation.expand.outputColumnId);
  report.target.appliedExpansion = {
    stepId: savedStep.id,
    inputColumnId: savedStep.operation.expand.inputColumnId,
    outputColumnId: savedStep.operation.expand.outputColumnId,
    outputLabel: savedOutput.label,
    emptyPolicy: savedStep.operation.expand.emptyPolicy,
    preview: proposedRows,
  };
  requireCheck(report, 'correctness', 'Apply saves EXPAND from the exact member ID list with retained cohort rows', true, { applied: report.target.appliedExpansion, source: applied.document.rows.groups.source.explicit });

  const reloadAndCheckExpansion = async (timingName, label) => {
    await recordBrowserTiming(report, cdp, {
      name: timingName,
      action: async () => {
        await reload(cdp, `Boolean(document.querySelector(${JSON.stringify(outputSelector)}))`);
        if (!await evaluate(cdp, `Boolean(document.querySelector(${JSON.stringify(tableSelector)}))`)) await click(cdp, outputSelector);
      },
      after: rowTableReady(2),
      timeout: 10000,
      budget: 5000,
    });
    await waitFor(cdp, rowTableReady(2), 10000);
    const value = await assertAppliedCohortRows(label, timingName);
    savedStep = value.document.construction.steps.at(-1);
    assert.equal(savedStep.id, report.target.appliedExpansion.stepId);
    assert.equal(savedStep.operation.expand.inputColumnId, report.target.appliedExpansion.inputColumnId);
    assert.equal(savedStep.operation.expand.outputColumnId, report.target.appliedExpansion.outputColumnId);
    return value;
  };
  applied = await reloadAndCheckExpansion('reload applied authored list EXPAND', editedOutputLabel);
  requireCheck(report, 'persistence', 'saved EXPAND, exact IDs, and named cohort label survive reload', true, { revisionId: cohort.revisionId, sourceIDs, stepId: savedStep.id, outputId, rendered: applied.rendered });

  const reopenSavedExpand = async () => {
    await openRowsDialog();
    await waitFor(cdp, `Boolean(document.querySelector('[data-testid="construction-row-operation-history"]'))`, 5000);
    const editSelector = `[data-testid="construction-row-edit-${savedStep.id}"]`;
    const action = await inspectAction(cdp, editSelector);
    if (!isActionable(action)) throw new Error(`Saved EXPAND Edit is not actionable: ${JSON.stringify(action)}`);
    await click(cdp, editSelector);
    await waitFor(cdp, `document.querySelector('input[aria-label="Expanded item label"]')?.value===${JSON.stringify(editedOutputLabel)}`, 5000);
    const advancedSummary = '[data-testid="construction-reshape-expand-advanced"] summary';
    const advancedAction = await inspectAction(cdp, advancedSummary);
    if (!isActionable(advancedAction)) throw new Error(`Saved EXPAND Advanced options are not actionable: ${JSON.stringify(advancedAction)}`);
    await recordBrowserTiming(report, cdp, {
      name: 'open saved EXPAND item-label options',
      action: () => click(cdp, advancedSummary),
      after: `document.querySelector('[data-testid="construction-reshape-expand-advanced"]')?.open===true&&document.querySelector('input[aria-label="Expanded item label"]')?.value===${JSON.stringify(editedOutputLabel)}`,
      timeout: 5000,
      budget: 5000,
    });
  };
  await reopenSavedExpand();
  const savedEditLabel = 'Expanded Patient identifier';
  await recordBrowserTiming(report, cdp, {
    name: 'edit saved authored EXPAND item label',
    action: () => fill(cdp, 'input[aria-label="Expanded item label"]', savedEditLabel),
      after: `Boolean(document.querySelector('[data-testid="construction-proposal-panel"][data-proposal-status="ready"]'))&&${proposalTableReady(2)}&&[...document.querySelectorAll('[data-testid="construction-proposal-preview"] th')].some(header=>(header.querySelector('span')?.innerText ?? header.innerText).trim().toLowerCase()===${JSON.stringify(savedEditLabel.toLowerCase())})`,
    timeout: 10000,
    budget: 5000,
  });
  const editGrid = assertGroupPairs(await readGrid(cdp), sourceIDs, savedEditLabel, 'Saved EXPAND edit Preview');
  await recordBrowserTiming(report, cdp, {
    name: 'apply saved authored EXPAND label edit',
    action: () => click(cdp, '[data-testid="construction-apply-proposal"]'),
    after: `!document.querySelector('[data-testid="construction-proposal-panel"]')&&${rowTableReady(2)}`,
    timeout: 10000,
    budget: 5000,
  });
  applied = await assertAppliedCohortRows(savedEditLabel, 'Applied saved EXPAND label edit');
  savedStep = applied.document.construction.steps.at(-1);
  assert.equal(savedStep.id, report.target.appliedExpansion.stepId, 'Editing must retain EXPAND step identity.');
  assert.equal(savedStep.operation.expand.inputColumnId, report.target.appliedExpansion.inputColumnId, 'Editing must retain the exact ALL-list input.');
  assert.equal(savedStep.operation.expand.outputColumnId, report.target.appliedExpansion.outputColumnId, 'Editing the label must retain output identity.');
  assert.equal(savedStep.outputs.find((column) => column.id === savedStep.operation.expand.outputColumnId)?.label, savedEditLabel);
  report.target.editedExpansion = { stepId: savedStep.id, inputColumnId: savedStep.operation.expand.inputColumnId, outputColumnId: savedStep.operation.expand.outputColumnId, outputLabel: savedEditLabel, preview: editGrid };
  requireCheck(report, 'correctness', 'editing saved EXPAND preserves its input and output identities and exact values', true, report.target.editedExpansion);

  applied = await reloadAndCheckExpansion('reload edited authored list EXPAND', savedEditLabel);
  requireCheck(report, 'persistence', 'edited EXPAND label and exact rows survive reload', true, { stepId: savedStep.id, label: savedEditLabel, rendered: applied.rendered });

  await openRowsDialog();
  await waitFor(cdp, `Boolean(document.querySelector('[data-testid="construction-row-operation-history"]'))`, 5000);
  const removeSelector = `[data-testid="construction-row-remove-${savedStep.id}"]`;
  const removeAction = await inspectAction(cdp, removeSelector);
  requireCheck(report, 'usability', 'native Rows history exposes an actionable saved EXPAND removal', isActionable(removeAction), removeAction);
  await recordBrowserTiming(report, cdp, {
    name: 'propose removing authored list EXPAND',
    action: () => click(cdp, removeSelector),
    after: `Boolean(document.querySelector('[data-testid="construction-proposal-panel"][data-proposal-status="ready"]'))&&${proposalTableReady(1)}&&Boolean(document.querySelector('[data-testid="construction-removal-summary"]'))`,
    timeout: 10000,
    budget: 5000,
  });
  grid = await readGrid(cdp);
  assert.equal(grid.rows.length, 1);
  assert.equal(grid.rows[0][grid.headers.findIndex(header => header.toLowerCase() === 'group label')], cohortLabel);
  assert.equal(grid.rows[0][grid.headers.findIndex(header => header.toLowerCase() === 'patient id')], sourceIDs.join('; '));
  const removal = await inspectAction(cdp, '[data-testid="construction-apply-proposal"]');
  requireCheck(report, 'correctness', 'EXPAND removal proposal previews the restored cohort and exact ALL ID array', isActionable(removal), { action: removal, grid, revisionId: cohort.revisionId });
  await recordBrowserTiming(report, cdp, {
    name: 'apply EXPAND removal and restore cohort list',
    action: () => click(cdp, '[data-testid="construction-apply-proposal"]'),
    after: `!document.querySelector('[data-testid="construction-proposal-panel"]')&&${rowTableReady(1)}`,
    timeout: 10000,
    budget: 5000,
  });
  const restored = await checkCohortState(undefined, 0, 'Applied EXPAND removal');
  grid = await readGrid(cdp);
  assert.equal(grid.rows.length, 1);
  assert.equal(grid.rows[0][grid.headers.findIndex(header => header.toLowerCase() === 'group label')], cohortLabel);
  assert.equal(grid.rows[0][grid.headers.findIndex(header => header.toLowerCase() === 'patient id')], sourceIDs.join('; '));
  requireCheck(report, 'persistence', 'removing EXPAND restores the original one-row named cohort with its exact ALL array', true, { grid, revisionId: cohort.revisionId, sourceIDs, draftVersion: restored.builder.draftVersion, draftDigest: restored.builder.draftDigest });

  await recordBrowserTiming(report, cdp, {
    name: 'reload restored named cohort and ALL member list',
    action: async () => {
      await reload(cdp, `Boolean(document.querySelector(${JSON.stringify(outputSelector)}))`);
      if (!await evaluate(cdp, `Boolean(document.querySelector(${JSON.stringify(tableSelector)}))`)) await click(cdp, outputSelector);
    },
    after: rowTableReady(1),
    timeout: 10000,
    budget: 5000,
  });
  await waitFor(cdp, rowTableReady(1), 10000);
  restored.builder = await readBuilder();
  const finalDocument = restored.builder.workspace.documents.find((item) => item.output.id === outputId);
  assert.equal(finalDocument?.rows?.groups?.source?.explicit?.revisionId, cohort.revisionId);
  assert.equal(finalDocument?.rows?.groups?.rowValues?.find((binding) => binding.columnId === idColumn.columnId)?.policy, 'ALL');
  assert.equal(finalDocument?.construction?.steps?.length ?? 0, 0, 'Final reload must restore a cohort-only construction with no authored EXPAND step.');
  grid = await readGrid(cdp);
  assert.equal(grid.rows.length, 1, 'Final reload must restore exactly one named-cohort row.');
  assert.equal(grid.rows[0][grid.headers.findIndex(header => header.toLowerCase() === 'group label')], cohortLabel);
  assert.equal(grid.rows[0][grid.headers.findIndex(header => header.toLowerCase() === 'patient id')], sourceIDs.join('; '));
  requireCheck(report, 'persistence', 'final reload restores the exact cohort source and raw ALL member values', true, { grid, revisionId: cohort.revisionId, sourceIDs, memberColumnId: idColumn.columnId, constructionStepCount: finalDocument.construction?.steps?.length ?? 0 });
});

export const runCohortExpand = (context) => run(context);
