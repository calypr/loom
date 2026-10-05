import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { sanitizeBody } from './lib/playwright-browser.mjs';

export async function verifyPopulationRowUI({ page, cda, selectionEvidencePath }) {
assert(selectionEvidencePath, 'Set an explicit selection evidence path for the population row lifecycle');
const selectionEvidencePathResolved = resolve(selectionEvidencePath);
const evidenceInput = JSON.parse(await readFile(selectionEvidencePathResolved, 'utf8'));
const selection = evidenceInput.selections?.explicit;
assert.ok(selection?.id, 'Selection evidence has no explicit revision');
assert.ok(evidenceInput.target?.uiUrl && evidenceInput.target?.project && evidenceInput.explorerId, 'Selection evidence has no browser target');
const project = cda.project;
const explorer = evidenceInput.explorerId;
const uiOrigin = cda.uiOrigin;
const apiOrigin = cda.apiOrigin;
const arangoContainer = cda.target.arangoContainer ?? process.env.LOOM_CDA_ARANGO_CONTAINER;
const arangoDatabase = process.env.LOOM_CDA_ARANGO_DATABASE;
assert(arangoContainer && arangoDatabase, 'Set LOOM_CDA_ARANGO_CONTAINER and LOOM_CDA_ARANGO_DATABASE for the independent raw-source oracle');
assert.equal(project, evidenceInput.target.project, 'Selection handoff project must match the explicitly owned project');
assert.equal(new URL(evidenceInput.target.uiUrl).origin, uiOrigin, 'Selection handoff UI origin must match the explicit isolated UI origin');
assert.equal(explorer, cda.explorer, 'Selection handoff explorer must match the native CDA fixture explorer');
const expectedFileIds = ['dev-file-001', 'dev-file-002', 'dev-file-004'];
const selectedRefs = evidenceInput.expectedRefs;
assert(Array.isArray(selectedRefs), 'Selection evidence must include its independently expected refs');
assert.deepEqual(selectedRefs.map(ref => ref.id).sort(), [...expectedFileIds].sort(), 'Selection handoff must contain the exact fixture DocumentReferences');
assert(selectedRefs.every(ref => ref.resourceType === 'DocumentReference'), 'Selection handoff must contain only DocumentReference identities');
assert(selectedRefs.every(ref => ref.project === selectedRefs[0]?.project && ref.generation === 'cda-fhir-v1'), 'Selection handoff must preserve one exact project and CDA generation');
assert.equal(project, evidenceInput.target.project, 'Selection handoff project must match the explicitly owned project');
assert.equal(new URL(evidenceInput.target.uiUrl).origin, uiOrigin, 'Selection handoff UI origin must match the explicit isolated UI origin');
const apiBase = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
const selectionBase = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/selections`;
const query = new URLSearchParams({ project, explorer, mode: 'builder', selection: selection.id });
const url = `${uiOrigin}/?${query}`;
const evidence = cda.evidence;
const artifact = process.env.LOOM_VERIFY_SCREENSHOTS === '1' ? join(evidence, 'population-row-ui.png') : undefined;
const report = { status: 'running', scope: 'selection handoff and visible Builder population lifecycle', selectionEvidencePath: selectionEvidencePathResolved, selectionRevisionId: selection.id, project, explorer, url, evidence, assertions: [], apiReads: [], transitions: [] };
await mkdir(evidence, { recursive: true });
const button = name => page.getByRole('button', { name, exact: true });
const browserErrors = [];
page.on('pageerror', error => browserErrors.push({ kind: 'page-error', message: error.message }));
page.on('console', message => { if (message.type() === 'error') browserErrors.push({ kind: 'console', message: message.text() }); });
page.on('requestfailed', request => browserErrors.push({ kind: 'network', url: request.url(), error: request.failure()?.errorText }));
page.on('response', response => {
  if (response.status() >= 400 && new URL(response.url()).pathname !== '/favicon.ico') {
    browserErrors.push({ kind: 'http', url: response.url(), status: response.status() });
  }
});
async function click(label, control) { await cda.action(label, control, target => target.click()); }
async function timedClick(label, control, settled) {
  const startedAt = Date.now();
  await click(label, control);
  report.activeAction = { label, locator: control.toString(), startedAt };
  await settled();
  const elapsedMs = Date.now() - startedAt;
  report.transitions.push({ name: label, elapsedMs, limitMs: 5000, passed: elapsedMs <= 5000 });
  report.activeAction = undefined;
  assert(elapsedMs <= 5000, `${label} took ${elapsedMs} ms to render`);
}
async function readBuilder() {
  const response = await fetch(`${apiBase}/builder`, { signal: AbortSignal.timeout(30000) });
  const text = await response.text();
  report.apiReads.push({ path: '/builder', status: response.status, ...(response.ok ? {} : { body: sanitizeBody(text) }) });
  assert(response.ok, `Builder read returned ${response.status}: ${sanitizeBody(text)}`);
  return JSON.parse(text);
}
async function waitText(text, timeout = 5000) {
  await page.getByText(text, { exact: true }).waitFor({ state: 'visible', timeout });
}
function readRawMapping(fileIds) {
  const query = `FOR f IN DocumentReference FILTER f.project == ${JSON.stringify(project)} AND f.dataset_generation == "cda-fhir-v1" AND f.id IN ${JSON.stringify(fileIds)} LET specimenIds = (FOR e IN fhir_edge FILTER e._from == f._id AND e.from_type == "DocumentReference" AND e.to_type == "Specimen" AND e.label == "subject_Specimen" AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == "cda-fhir-v1" LET s = DOCUMENT(e._to) FILTER s != null AND s.resourceType == "Specimen" AND s.project == ${JSON.stringify(project)} AND s.dataset_generation == "cda-fhir-v1" RETURN s.id) RETURN {fileId:f.id,specimenIds:SORTED_UNIQUE(specimenIds)}`;
  const output = execFileSync('docker', ['exec', arangoContainer, 'arangosh', '--server.database', arangoDatabase, '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`], { encoding: 'utf8', maxBuffer: 200000 });
  return JSON.parse(output.slice(output.indexOf('[')));
}
async function readSelection(revisionId) {
  const members = [];
  let cursor;
  let revision;
  do {
    const query = new URLSearchParams({ limit: '100', ...(cursor ? { cursor } : {}) });
    const response = await fetch(`${selectionBase}/${encodeURIComponent(revisionId)}?${query}`, { signal: AbortSignal.timeout(30000) });
    const text = await response.text();
    report.apiReads.push({ path: `/selections/${revisionId}`, status: response.status, ...(response.ok ? {} : { body: sanitizeBody(text) }) });
    assert(response.ok, `Selection read returned ${response.status}: ${sanitizeBody(text)}`);
    const page = JSON.parse(text);
    assert(page.revision, 'Selection page omitted revision identity');
    assert.equal(page.revision.id, revisionId, 'Selection response returned a different immutable revision');
    if (revision) {
      assert.equal(page.revision.id, revision.id, 'Selection pagination changed revision identity');
      assert.equal(page.revision.membershipDigest, revision.membershipDigest, 'Selection pagination changed membership digest');
      assert.equal(page.revision.memberCount, revision.memberCount, 'Selection pagination changed member count');
    }
    revision = page.revision;
    assert(Array.isArray(page.members), 'Selection page omitted member list');
    members.push(...page.members.map(member => member.ref));
    assert(members.length <= revision.memberCount, 'Selection pagination repeated members');
    cursor = page.nextCursor;
  } while (cursor);
  assert.equal(revision?.complete, true, 'Selection membership revision is incomplete');
  assert.equal(members.length, revision.memberCount, 'Selection pagination omitted members');
  assert.equal(revision.project, selectedRefs[0].project, 'Selection revision changed project identity');
  assert.equal(revision.generation, 'cda-fhir-v1', 'Selection revision changed dataset generation');
  assert.equal(revision.resourceType, 'DocumentReference', 'Selection revision changed resource type');
  return { revision, members };
}
const canonicalRefs = refs => refs.map(({ project, generation, resourceType, id }) => ({ project, generation, resourceType, id }))
  .sort((a, b) => `${a.project}/${a.generation}/${a.resourceType}/${a.id}`.localeCompare(`${b.project}/${b.generation}/${b.resourceType}/${b.id}`));
const populationDocument = value => value.workspace.documents.find(document => document.population?.selectionRevisionId);
async function visiblePreviewIds(expectedIds) {
  const table = page.getByTestId('preview-table-scroll').getByRole('table');
  await table.waitFor({ state: 'visible', timeout: 5000 });
  await page.waitForFunction(() => !document.body.innerText.includes('Loading the preview…'), null, { timeout: 5000 });
  const rows = await table.getByRole('row').evaluateAll(elements => elements.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length));
  const actualIds = rows.map(row => row[0]);
  assert.deepEqual(actualIds, expectedIds, 'Visible preview identities, order, or multiplicity differ from raw DocumentReference→Specimen source mapping');
  return rows;
}
try {
  const rawMapping = readRawMapping(expectedFileIds);
  assert.deepEqual(rawMapping.map(row => row.fileId).sort(), [...expectedFileIds].sort(), 'Raw CDA source is missing a selected DocumentReference');
  const sourceMapping = Object.fromEntries(rawMapping.map(row => [row.fileId, row.specimenIds]));
  assert.deepEqual(sourceMapping, {
    'dev-file-001': ['dev-specimen-001'],
    'dev-file-002': ['dev-specimen-001'],
    'dev-file-004': [],
  }, 'Raw CDA file-to-Specimen relationships differ from the fixture oracle');
  report.sourceOracle = { generation: 'cda-fhir-v1', resourceType: 'DocumentReference', mapping: sourceMapping, expectedRows: ['dev-specimen-001'] };
  report.before = await readBuilder();
  await cda.navigate(url);
  if (artifact) await page.screenshot({ path: artifact, fullPage: true });
  await page.waitForFunction(() => document.body.innerText.includes('3 selected DocumentReference resources are ready to constrain this table.')
    || [...document.querySelectorAll('button')].some(control => control.textContent.trim() === 'Use all authorized rows'), null, { timeout: 5000 });
  const useAll = button('Use all authorized rows');
  const useAllCount = await useAll.count();
  assert(useAllCount <= 1, `Expected at most one Use all authorized rows control, found ${useAllCount}`);
  if (useAllCount === 1 && await useAll.isVisible()) {
    await timedClick('use all authorized rows for selection handoff', useAll,
      () => waitText('3 selected DocumentReference resources are ready to constrain this table.'));
  }
  await timedClick('attach selected resources', button('Use selected resources'),
    () => waitText('3 DocumentReference resources constrain one row per Specimen.'));
  const preview = button('Preview');
  await preview.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await preview.isEnabled(), true);
  await timedClick('preview selected-resource population', preview, async () => {
    report.initialVisibleRows = await visiblePreviewIds(['dev-specimen-001']);
  });
  const coverage = button('Check selected-resource coverage');
  await coverage.waitFor({ state: 'visible', timeout: 5000 });
  await timedClick('check selected-resource coverage', coverage,
    () => waitText('3 selected · 2 produce rows · 1 needs attention'));
  const reportText = await page.locator('body').innerText();
  assert(reportText.includes('DocumentReference/dev-file-004'), 'Coverage report omitted the bounded unmatched resource');
  const attachedBuilder = await readBuilder();
  const attachedDocument = populationDocument(attachedBuilder);
  assert(attachedDocument, 'The UI did not persist its selected-resource population');
  assert.equal(attachedDocument.population.selectionRevisionId, selection.id, 'The Builder attached a different immutable selection');
  report.outputId = attachedDocument.output.id;
  report.attachedPopulation = attachedDocument.population;
  const attachedMembers = await readSelection(selection.id);
  assert.equal(attachedMembers.revision.complete, true);
  assert.equal(attachedMembers.revision.memberCount, expectedFileIds.length, 'Attached immutable selection count differs from its evidence');
  assert.equal(attachedMembers.revision.membershipDigest, selection.membershipDigest, 'Attached immutable selection digest differs from its evidence');
  assert.deepEqual(canonicalRefs(attachedMembers.members), canonicalRefs(selectedRefs), 'Attached immutable selection membership differs from its full project/generation/resource/id evidence');
  report.assertions.push('Builder loads immutable selection handoff', 'preview excludes the unselected specimen', 'coverage report retains the exact unmatched DocumentReference');

  await timedClick('remove unmatched resource from collection', button('Remove from collection'),
    () => waitText('2 DocumentReference resources constrain one row per Specimen.'));
  await preview.waitFor({ state: 'visible', timeout: 5000 });
  await timedClick('preview revised selection', preview, async () => {
    report.revisedVisibleRows = await visiblePreviewIds(['dev-specimen-001']);
  });
  await coverage.waitFor({ state: 'visible', timeout: 5000 });
  await timedClick('check revised selection coverage', coverage,
    () => waitText('2 selected · 2 produce rows · 0 needs attention'));
  const reloadStartedAt = Date.now();
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitText('2 DocumentReference resources constrain one row per Specimen.');
  const reloadElapsedMs = Date.now() - reloadStartedAt;
  report.transitions.push({ name: 'reload revised population', elapsedMs: reloadElapsedMs, limitMs: 5000, passed: reloadElapsedMs <= 5000 });
  assert(reloadElapsedMs <= 5000, `reload revised population took ${reloadElapsedMs} ms to render`);
  const reloaded = await page.locator('body').innerText();
  assert.equal(reloaded.includes('2 selected · 2 produce rows · 0 needs attention'), false, 'Reload must clear stale coverage evidence');
  assert.equal(reloaded.includes('3 DocumentReference resources constrain one row per Specimen.'), false, 'Reload restored the old population selection');
  report.after = await readBuilder();
  const revisedDocument = report.after.workspace.documents.find(document => document.output.id === report.outputId);
  assert(revisedDocument?.population?.selectionRevisionId, 'Revised population did not persist a selection revision');
  assert.notEqual(revisedDocument.population.selectionRevisionId, selection.id, 'Removing a member must create a new immutable selection revision');
  report.revisedPopulation = revisedDocument.population;
  const revisedMembers = await readSelection(revisedDocument.population.selectionRevisionId);
  assert.equal(revisedMembers.revision.complete, true);
  assert.equal(revisedMembers.revision.memberCount, 2, 'Revised selection must contain exactly two members');
  assert.notEqual(revisedMembers.revision.membershipDigest, attachedMembers.revision.membershipDigest, 'Removing file 004 must change the immutable membership digest');
  assert.deepEqual(canonicalRefs(revisedMembers.members), canonicalRefs(selectedRefs.filter(ref => ref.id !== 'dev-file-004')), 'Removing file 004 must preserve exact remaining project/generation/resource/id membership');
  const reloadedBuilder = await readBuilder();
  const reloadedDocument = reloadedBuilder.workspace.documents.find(document => document.output.id === report.outputId);
  assert.equal(reloadedDocument?.population?.selectionRevisionId, revisedDocument.population.selectionRevisionId, 'Reload must retain the exact revised immutable selection revision');
  report.assertions.push('removing DocumentReference/dev-file-004 creates and attaches a new two-member selection', 'revised selection survives reload and stale coverage evidence clears');
  report.assertions.push('raw CDA source independently maps files 001/002 to one Specimen and file 004 to none', 'preview rows and immutable selection membership exactly match raw-source expectations before and after removal');
  report.diagnostics = browserErrors;
  assert.deepEqual(browserErrors, [], 'Unexpected native Playwright browser failure');
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = { label: report.activeAction.label, locator: report.activeAction.locator, elapsedMs: Date.now() - report.activeAction.startedAt };
  report.diagnostics = browserErrors;
  throw error;
} finally {
  report.finishedAt = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await cda.attachReport('population-row-ui', report);
}
cda.check('correctness', 'population handoff rows and immutable membership match raw CDA source', report.status === 'passed',
  { selectionRevisionId: selection.id, assertions: report.assertions });
return report;
}
