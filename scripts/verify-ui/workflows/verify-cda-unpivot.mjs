import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';


export async function runUnpivotWorkflow({ page, cda }, originalArgs = {}) {
  const environment = cda.env ?? process.env;
const includeFixtureDiagnostics = domainReport => {
    const diagnostics = cda.diagnostics;
    domainReport.errors ??= [];
    const add = (entry, same) => { if (!domainReport.errors.some(same)) domainReport.errors.push(entry); };
    for (const failure of diagnostics.pageErrors ?? []) add({ kind: 'runtime', message: failure.message }, item => item.kind === 'runtime' && item.message === failure.message);
    for (const failure of diagnostics.console ?? []) add({ kind: 'console', message: failure.text, location: failure.location }, item => item.kind === 'console' && item.message === failure.text);
    for (const failure of diagnostics.networkFailures ?? []) add({ kind: 'network', path: failure.url, failure: failure.failure }, item => item.kind === 'network' && item.path === failure.url);
    for (const failure of diagnostics.httpFailures ?? []) add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, item => item.kind === 'http' && item.url === failure.url && item.status === failure.status);
    domainReport.incidentalErrors ??= [];
    for (const failure of diagnostics.assetFailures ?? []) if (!domainReport.incidentalErrors.some(item => item.url === failure.url && item.status === failure.status)) domainReport.incidentalErrors.push(failure);
  };
  const captureFailure = async (error, details = {}) => cda.attachReport('failure-evidence', { error: String(error), details, diagnostics: cda.diagnostics });
const project = cda.project;
const apiOrigin = cda.apiOrigin.replace(/\/$/, '');
const uiOrigin = cda.uiOrigin.replace(/\/$/, '');
const explorerId = cda.explorer;
assert(explorerId, 'Set LOOM_CDA_EXPLORER_ID or pass an owned isolated explorer ID.');
assert.notEqual(explorerId, 'cda-builder-full-qa-1790440983382', 'The shared CDA explorer is protected.');
const target = cda.target;
const generation = (cda.generation ?? cda.env?.LOOM_CDA_DATASET_GENERATION) ?? 'cda-fhir-v1';
const pageURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const authoringPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2`;
const evidenceDirectory = cda.evidence;
const report = { project, generation, explorerId, pageURL, target, actions: [], errors: [], nativeRequests: [] };
const tracker = cda.captureRequests(authoringPath, { apiOrigin, uiOrigin });

const inspect = (callback, args = []) => cda.inspect( callback, args);
const wait = (callback, args = {}, timeout = 5000) => cda.wait(callback, args ?? {}, Math.min(timeout, 5000));
const one = selector => {
  const locator = page.locator(selector);
  return locator;
};
const clickOne = async (selector, name) => {
  const locator = one(selector);
  assert.equal(await locator.count(), 1, `${name}: expected a unique control for ${selector}`);
  const state = { name, selector, visible: await locator.isVisible(), enabled: await locator.isEnabled() };
  await cda.click(selector, {}, 5000);
  report.actions.push(state);
};
const previewResponse = async startIndex => {
  await tracker.flush();
  const existing = report.nativeRequests.slice(startIndex).find(entry => entry.path.endsWith('/preview') && entry.status === 200 && entry.response);
  return existing ?? cda.waitForCapturedResponse(tracker,
    entry => entry.path.endsWith('/preview') && report.nativeRequests.indexOf(entry) >= startIndex && entry.status === 200, 5000);
};
const rawCda = aql => {
  const output = execFileSync('docker', ['exec', target.arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(aql)}).toArray()))`], { encoding: 'utf8', timeout: 30000, maxBuffer: 200000 });
  const start = output.indexOf('[');
  assert(start >= 0, 'Raw CDA oracle returned no JSON array');
  return JSON.parse(output.slice(start));
};

try {
  await cda.navigate(pageURL);
  await wait(() => document.body.innerText.includes('DATASET WORKSPACE'));
  const before = await inspect(() => ({
    explorer: document.querySelector('select[aria-label="Explorer"]')?.value,
    tables: [...document.querySelectorAll('button')].filter(button => button.innerText.trim().startsWith('▤')).map(button => button.innerText.trim()),
    history: document.querySelectorAll('[data-testid^="construction-history-step-"]').length,
  }));
  assert.equal(before.explorer, explorerId);
  assert(before.tables.includes('▤\nSpecimen'), `Specimen table missing: ${JSON.stringify(before.tables)}`);
  assert.equal(before.history, 0, 'QA specimen table should start without reshape history');

  const fields = ['subject.reference', 'collection.bodySite.reference.reference'];
  const currentColumns = await inspect(() => {
    const text = document.body.innerText;
    return text.slice(text.indexOf('Select columns for an action'), text.indexOf('Preview and configure'));
  });
  let source = null;
  let selected = null;
  let added = null;
  if (!fields.every(field => currentColumns.includes(field))) {
    await clickOne('button[aria-label^="Add columns:"]', 'Open Add columns');
    const sourceSelector = `[data-testid="construction-add-columns-source-option"][aria-label^="Specimen,"]`;
    await one(sourceSelector).waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await one(sourceSelector).count(), 1, 'Specimen row source must be unique');
    source = await one(sourceSelector).getAttribute('data-source-key');
    assert(source, 'Specimen row source is missing');
    await clickOne(sourceSelector, 'Choose Specimen rows as the feature source');
    for (const field of fields) {
      const selector = `input[aria-label=${JSON.stringify(`Select Specimen.${field}`)}]`;
      await one(selector).waitFor({ state: 'visible', timeout: 5000 });
      assert(await one(selector).isEnabled(), `Specimen.${field} selector is disabled`);
      await clickOne(selector, `Select Specimen.${field}`);
    }
    await wait(() => [...document.querySelectorAll('button')].some(button => button.textContent?.trim() === 'Add 2 selected features' && !button.disabled));
    selected = await inspect(() => ({
      checked: [...document.querySelectorAll('input[aria-label^="Select Specimen."]')].filter(input => input.checked).map(input => input.getAttribute('aria-label')),
      button: [...document.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Add 2 selected features')?.innerText,
    }));
    assert.equal(selected.checked.length, 2, 'Expected exactly two selected scalar fields');
    await clickOne('button:text-is("Add 2 selected features")', 'Preview selected features');
    await wait(() => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready');
    const addProposalApply = 'button:text-is("Apply columns")';
    assert.equal(await one(`[data-testid="construction-choice-proposal-panel"] ${addProposalApply}`).isEnabled(), true, 'Selected features proposal must be applicable');
    await clickOne(`[data-testid="construction-choice-proposal-panel"] ${addProposalApply}`, 'Apply selected features');
    await wait((fieldNames) => fieldNames.every(field => document.body.innerText.includes(field)), fields);
    added = await inspect(() => ({
      dialog: [...document.querySelectorAll('[role="dialog"]')].map(dialog => dialog.innerText),
      tableTabs: [...document.querySelectorAll('button')].filter(button => button.innerText.trim().startsWith('▤')).map(button => button.innerText.trim()),
      alerts: [...document.querySelectorAll('[role="alert"]')].map(element => element.innerText),
    }));
  }

  const sourcePreviewRequestStart = report.nativeRequests.length;
  await clickOne('button:text-is("Preview")', 'Preview source');
  await one('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: 5000 });
  const sourcePreviewEntry = await previewResponse(sourcePreviewRequestStart);
  const sourcePreview = sourcePreviewEntry.response;
  assert(sourcePreview?.columns?.some(column => column.label === 'subject.reference'));
  assert(sourcePreview.columns.some(column => column.label === 'collection.bodySite.reference.reference'));
  assert(sourcePreview.rows.some(row => row.col_69ee827a3bd92ad6cb1c86c0?.startsWith('Patient/')));
  assert(sourcePreview.rows.some(row => row.col_d9e50113230ca5d6c7600beb?.startsWith('BodyStructure/')));
  const sourceIds = sourcePreview.rows.map(row => row.col_17edcedbd7920c717b54b398);
  const sourceAQL = `FOR d IN Specimen FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(generation)} AND d.id IN ${JSON.stringify(sourceIds)} RETURN {id:d.id,subject:d.payload.subject.reference,bodySite:d.payload.collection.bodySite.reference.reference}`;
  const sourceRecords = rawCda(sourceAQL);
  const sourceById = new Map(sourceRecords.map(row => [row.id, row]));
  assert.equal(sourceById.size, sourceIds.length, 'The CDA source oracle did not return every previewed Specimen');
  for (const row of sourcePreview.rows) {
    const record = sourceById.get(row.col_17edcedbd7920c717b54b398);
    assert.equal(row.col_69ee827a3bd92ad6cb1c86c0, record.subject);
    assert.equal(row.col_d9e50113230ca5d6c7600beb, record.bodySite);
  }

  await clickOne('button[aria-label^="Reshape:"]', 'Open reshape menu');
  const unpivotButton = page.getByRole('button', { name: /^Turn columns into rows/ });
  await unpivotButton.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await unpivotButton.count(), 1, 'Unpivot menu choice must be unique');
  await clickOne('button:has-text("Turn columns into rows")', 'Choose Unpivot');
  await one('[data-testid="construction-reshape-unpivot"]').waitFor({ state: 'visible', timeout: 5000 });
  const editor = await inspect(() => {
    const panel = document.querySelector('[data-testid="construction-reshape-unpivot"]');
    return { text: panel?.innerText, advancedClosed: !panel.querySelector('[data-testid="construction-unpivot-advanced"]')?.open,
      effect: panel.querySelector('[data-testid="construction-unpivot-effect"]')?.innerText,
      inputs: [...panel.querySelectorAll('input,select')].map(input => ({ label: input.getAttribute('aria-label'), value: input.value, checked: input.checked, disabled: input.disabled })) };
  });
  assert(editor.inputs.some(input => input.label === 'Unpivot subject.reference'));
  assert(editor.inputs.some(input => input.label === 'Unpivot collection.bodySite.reference.reference'));
  assert(editor.advancedClosed && editor.effect?.includes('Rows with missing values stay in the table'), 'Unpivot must show a preserving default with Advanced options closed');
  const defaultProposalStart = report.nativeRequests.length;
  const defaultPreviewStarted = Date.now();
  for (const field of fields) {
    const selector = `input[aria-label=${JSON.stringify(`Unpivot ${field}`)}]`;
    const checkbox = one(selector);
    assert.equal(await checkbox.count(), 1, `Unique unpivot input missing for ${field}`);
    if (!await checkbox.isChecked()) await clickOne(selector, `Select Unpivot ${field}`);
  }
  await wait(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready');
  const defaultPreviewMs = Date.now() - defaultPreviewStarted;
  const defaultEffect = await inspect(() => document.querySelector('[data-testid="construction-unpivot-effect"]')?.innerText);
  assert(defaultEffect?.includes('2 new rows per original row. Rows with missing values stay in the table.'), 'Unpivot does not explain the default row effect after selecting columns');
  await tracker.flush();
  const defaultProposalRequest = report.nativeRequests.slice(defaultProposalStart).filter(request => request.path.endsWith('/construction-proposals')).at(-1);
  assert(defaultProposalRequest?.body, 'Preserving Unpivot proposal request was not captured');
  const defaultOperation = defaultProposalRequest.body.candidateConstruction.steps.at(-1).operation.unpivot;
  assert.equal(defaultOperation.inputs.length, 2, 'Default preview must include both selected columns');
  assert.equal(defaultOperation.nullRowPolicy, 'PRESERVE');
  assert.equal(defaultProposalRequest.status, 200, 'Preserving Unpivot proposal did not succeed');
  const defaultPreview = defaultProposalRequest.response?.preview;
  assert(defaultPreview?.rows?.some(row => row.variable === 'collection.bodySite.reference.reference' && row.value === null), 'Preserving Unpivot preview omitted a missing CDA value');
  for (const row of defaultPreview.rows) {
    const record = sourceById.get(row.col_17edcedbd7920c717b54b398);
    assert(record, 'A preserving Unpivot row has no CDA source Specimen');
    assert.equal(row.value, row.variable === 'subject.reference' ? record.subject : record.bodySite, 'Preserving Unpivot value differs from its CDA source field');
  }
  assert(defaultPreviewMs <= 5000, `Default Unpivot preview took ${defaultPreviewMs} ms`);

  await clickOne('[data-testid="construction-unpivot-advanced"] summary', 'Open Unpivot advanced options');
  await cda.selectOption('select[aria-label="Unpivot null row policy"]', 'DROP');
  for (const [label, value] of [['Unpivot key output name', 'qa_unpivot_key'], ['Unpivot key output label', 'QA unpivot key'], ['Unpivot value output name', 'qa_unpivot_value'], ['Unpivot value output label', 'QA unpivot value']]) {
    await cda.fill(`input[aria-label=${JSON.stringify(label)}]`, value, {}, 5000);
  }
  const configured = await inspect(() => {
    const panel = document.querySelector('[data-testid="construction-reshape-unpivot"]');
    return { text: panel?.innerText, inputs: [...panel.querySelectorAll('input,select')].map(input => ({ label: input.getAttribute('aria-label'), value: input.value, checked: input.checked, disabled: input.disabled })) };
  });
  await wait(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready' && document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.includes('QA unpivot key'));
  const proposalDOM = await inspect(() => {
    const table = document.querySelector('[data-testid="construction-proposal-preview"]');
    return { status: document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status'),
      panel: document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,
      previewText: table?.innerText.slice(0, 2500), applyDisabled: document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled };
  });
  await tracker.flush();
  const proposalEntry = report.nativeRequests.filter(response => response.path.endsWith('/construction-proposals')).at(-1);
  const proposalBody = proposalEntry?.response;
  assert.equal(proposalEntry?.status, 200, 'Configured Unpivot proposal request must succeed');
  assert.equal(proposalEntry?.body?.candidateConstruction?.steps?.at(-1)?.operation?.unpivot?.nullRowPolicy, 'DROP');
  const proposal = { ...proposalDOM, response: proposalBody };
  const proposalSummary = { status: proposal.status, panel: proposal.panel, applyDisabled: proposal.applyDisabled, responseStatus: proposalEntry?.status, responseKeys: proposalBody ? Object.keys(proposalBody) : [] };
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'unpivot-proposal.json'), JSON.stringify({ pageURL, before, currentColumns, source, selected, added, sourcePreview, editor, defaultPreviewMs, defaultEffect, defaultProposalRequest, defaultPreview, configured, proposal, nativeRequests: report.nativeRequests }, null, 2));
  assert.equal(proposal.status, 'ready', `Unpivot proposal did not become ready: ${proposal.panel}`);
  assert.equal(proposal.applyDisabled, false, 'Apply should be enabled for a ready unpivot proposal');
  await clickOne('[data-testid="construction-apply-proposal"]', 'Apply Unpivot');
  await wait(() => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1);
  const savedHistory = await inspect(() => document.querySelector('[data-testid^="construction-history-step-"]')?.innerText);
  await cda.navigate(pageURL);
  await wait(() => document.body.innerText.includes('DATASET WORKSPACE') && document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1);
  let historyLabelsResolved = true;
  try { await wait(() => !document.querySelector('[data-testid^="construction-history-step-"]')?.innerText.includes('a column as'), [], 3000); }
  catch { historyLabelsResolved = false; }
  const reloadedHistory = await inspect(() => document.querySelector('[data-testid^="construction-history-step-"]')?.innerText);
  const appliedPreviewStart = report.nativeRequests.length;
  await clickOne('button:text-is("Preview")', 'Preview applied Unpivot');
  await one('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: 5000 });
  const appliedPreviewEntry = await previewResponse(appliedPreviewStart);
  const appliedPreview = appliedPreviewEntry.response;
  const appliedRows = appliedPreview.rows.map(row => ({ id: row.col_17edcedbd7920c717b54b398, key: row.qa_unpivot_key, value: row.qa_unpivot_value }));
  const proposedRows = proposalBody.preview.rows.map(row => ({ id: row.col_17edcedbd7920c717b54b398, key: row.qa_unpivot_key, value: row.qa_unpivot_value }));
  assert(appliedRows.every(row => fields.includes(row.key)), 'Unpivot key values should name visible source fields, not internal column IDs');
  assert(fields.every(field => appliedRows.some(row => row.key === field)), 'Both chosen source fields should appear as unpivot keys');
  assert(!reloadedHistory.includes('col_'), 'Saved Unpivot summary should not expose internal column IDs');
  for (const row of appliedRows) {
    const record = sourceById.get(row.id);
    assert(record, 'An Unpivot row has no CDA source Specimen');
    assert.equal(row.value, row.key === 'subject.reference' ? record.subject : record.bodySite, 'An Unpivot value differs from its CDA source field');
  }
  assert.equal(appliedPreview.rowCount, proposalBody.preview.rowCount, 'Applied preview row count differs from the reviewed proposal');
  assert.deepEqual(appliedRows, proposedRows, 'Applied preview values differ from the reviewed proposal sample');
  await clickOne('[data-testid^="construction-history-step-"]', 'Select saved Unpivot history');
  await one('[data-testid^="construction-edit-step-"]').waitFor({ state: 'visible', timeout: 5000 });
  await clickOne('[data-testid^="construction-edit-step-"]', 'Edit saved Unpivot');
  await one('[data-testid="construction-reshape-unpivot"]').waitFor({ state: 'visible', timeout: 5000 });
  const restoredEditor = await inspect(() => {
    const panel = document.querySelector('[data-testid="construction-reshape-unpivot"]');
    return { inputs: [...panel.querySelectorAll('input,select')].map(input => ({ label: input.getAttribute('aria-label'), value: input.value, checked: input.checked })), text: panel.innerText };
  });
  assert(restoredEditor.inputs.some(input => input.label === 'Unpivot subject.reference' && input.checked));
  assert(restoredEditor.inputs.some(input => input.label === 'Unpivot collection.bodySite.reference.reference' && input.checked));
  assert(restoredEditor.inputs.some(input => input.label === 'Unpivot key output name' && input.value === 'qa_unpivot_key'));
  assert(restoredEditor.inputs.some(input => input.label === 'Unpivot value output name' && input.value === 'qa_unpivot_value'));
  const stepTestId = await inspect(() => document.querySelector('[data-testid^="construction-history-step-"]')?.getAttribute('data-testid'));
  const stepId = stepTestId?.replace('construction-history-step-', '');
  assert(stepId, 'Saved unpivot step id is missing');
  await clickOne(`[data-testid=${JSON.stringify(stepTestId)}]`, 'Select Unpivot step');
  const removeSelector = `[data-testid=${JSON.stringify(`construction-remove-step-${stepId}`)}]`;
  await one(removeSelector).waitFor({ state: 'visible', timeout: 5000 });
  assert(await one(removeSelector).isEnabled(), 'Remove Unpivot step control is disabled');
  const removeProposalStart = report.nativeRequests.length;
  await clickOne(removeSelector, 'Propose Unpivot removal');
  await wait(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready');
  await tracker.flush();
  const removeProposalEntry = report.nativeRequests.slice(removeProposalStart).find(entry => entry.path.endsWith('/construction-proposals'));
  assert(removeProposalEntry?.body, 'Remove step should send a construction proposal request');
  assert.deepEqual(removeProposalEntry.body.removeStepIds, [stepId]);
  const removeProposal = await inspect(() => ({ panel: document.querySelector('[data-testid="construction-proposal-panel"]')?.innerText,
    preview: document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.slice(0, 1500),
    disabled: document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled }));
  assert.equal(removeProposal.disabled, false, `Remove proposal Apply should be enabled: ${removeProposal.panel}`);
  const removeCommandStart = report.nativeRequests.length;
  await clickOne('[data-testid="construction-apply-proposal"]', 'Apply Unpivot removal');
  const removeCommand = await cda.waitForCapturedResponse(tracker, entry => entry.path.endsWith('/commands') && report.nativeRequests.indexOf(entry) >= removeCommandStart, 5000);
  assert.equal(removeCommand.status, 200, 'Removing the unpivot command failed');
  await wait(() => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0);
  await cda.navigate(pageURL);
  await wait(() => document.body.innerText.includes('DATASET WORKSPACE') && document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0);
  const restoredPreviewStart = report.nativeRequests.length;
  await one('button:text-is("Preview")').waitFor({ state: 'visible', timeout: 5000 });
  await clickOne('button:text-is("Preview")', 'Preview restored source');
  await one('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible', timeout: 5000 });
  const restoredEntry = await previewResponse(restoredPreviewStart);
  const restoredBody = restoredEntry.response;
  const restoredRows = restoredBody.rows.map(row => ({ id: row.col_17edcedbd7920c717b54b398, subject: row.col_69ee827a3bd92ad6cb1c86c0, bodySite: row.col_d9e50113230ca5d6c7600beb }));
  const expectedRows = sourcePreview.rows.map(row => ({ id: row.col_17edcedbd7920c717b54b398, subject: row.col_69ee827a3bd92ad6cb1c86c0, bodySite: row.col_d9e50113230ca5d6c7600beb }));
  assert.deepEqual(restoredRows, expectedRows, 'Removing the unpivot should restore the original source rows exactly');
  includeFixtureDiagnostics(report);
  assert.deepEqual(report.errors, [], `Unexpected browser or owned API errors: ${JSON.stringify(report.errors)}`);
  await writeFile(join(evidenceDirectory, 'unpivot-end-to-end.json'), JSON.stringify({ pageURL, before, currentColumns, sourceRecords, sourcePreview: { outputId: sourcePreview.outputId, rowCount: sourcePreview.rowCount, columns: sourcePreview.columns, rows: expectedRows }, editor, defaultPreviewMs, defaultEffect, defaultProposalRequest, defaultPreview, configured, proposal, savedHistory, reloadedHistory, historyLabelsResolved, appliedPreview: { rowCount: appliedPreview.rowCount, columns: appliedPreview.columns, rows: appliedRows }, restoredEditor, removeProposalRequest: removeProposalEntry.body, removeProposal, removeProposalStatus: removeProposalEntry.status, removeApplyStatus: removeCommand.status, restored: { outputId: restoredBody.outputId, rowCount: restoredBody.rowCount, columns: restoredBody.columns, rows: restoredRows }, nativeRequests: report.nativeRequests, actions: report.actions, errors: report.errors }, null, 2));
  assert.equal(restoredBody.rowCount, sourcePreview.rowCount, 'Restored preview row count differs from source');
  assert(historyLabelsResolved, 'Saved Unpivot history did not resolve the selected field labels');
  assert.deepEqual(report.nativeRequests.filter(request => request.path.endsWith('/publish')), [], 'Focused unpivot verification must not publish');
} catch (error) {
  await captureFailure(error, { action: report.activeAction ?? report.actions.at(-1), project, explorerId, requests: report.nativeRequests.slice(-20) });
  throw error;
} finally {
  includeFixtureDiagnostics(report);
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'unpivot-report.json'), JSON.stringify({ ...report, diagnostics: cda.diagnostics }, null, 2)).catch(() => undefined);
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-unpivot.mjs workflow failed');
  await cda.attachReport('verify-cda-unpivot.mjs', report);
  return report;
}
