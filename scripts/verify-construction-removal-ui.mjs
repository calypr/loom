import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { sanitizeBody } from './lib/playwright-browser.mjs';

export async function verifyConstructionRemovalUI({ page, cda, output, step, apply = false }) {
  const args = {
    project: cda.project,
    explorer: cda.explorer,
    output: output ?? process.env.LOOM_CDA_OUTPUT_ID,
    step: step ?? process.env.LOOM_CDA_STEP_ID,
    origin: cda.uiOrigin,
    'api-origin': cda.apiOrigin,
    'api-container': cda.target.apiContainer,
    'compose-project': cda.target.composeProject,
    evidence: cda.evidence,
    apply,
  };
  assert(args.project && args.explorer && args.output && args.step && args.origin && args['api-origin'],
    'Set an owned project/explorer and explicit output/step in the native CDA fixture');
  const apiBase = `${args['api-origin']}/api/v1/projects/${encodeURIComponent(args.project)}/explorers/${encodeURIComponent(args.explorer)}/authoring/v2`;
  const proposalPath = new URL(`${apiBase}/construction-proposals`).pathname;
  const appOrigins = new Set([args.origin, args['api-origin']].map(origin => new URL(origin).origin));
  const builderURL = `${args.origin}/?project=${encodeURIComponent(args.project)}&explorer=${encodeURIComponent(args.explorer)}&mode=builder`;
  const ids = { table: `construction-table-${args.output}`, step: `construction-history-step-${args.step}`, remove: `construction-remove-step-${args.step}` };
  const report = { status: 'running', target: { project: args.project, explorer: args.explorer, apiOrigin: args['api-origin'], uiOrigin: args.origin },
    builderURL, applyRequested: args.apply, evidence: args.evidence, transitions: [], apiReads: [], proposalResponses: [] };
  const browserErrors = [];
  page.on('pageerror', error => browserErrors.push({ kind: 'page-error', message: error.message }));
  page.on('console', message => { if (message.type() === 'error') browserErrors.push({ kind: 'console', message: message.text() }); });
  page.on('requestfailed', request => browserErrors.push({ kind: 'network', url: request.url(), error: request.failure()?.errorText }));
  const locator = id => page.getByTestId(id);
  async function click(label, control) { return cda.action(label, control, target => target.click()); }
  function recordTransition(name, startedAt) {
    const elapsedMs = Date.now() - startedAt;
    report.transitions.push({ name, elapsedMs, limitMs: 5000, passed: elapsedMs <= 5000 });
    assert(elapsedMs <= 5000, `${name} took ${elapsedMs} ms to render`);
    report.activeAction = undefined;
  }
  async function readBuilder() {
    const response = await fetch(`${apiBase}/builder`, { signal: AbortSignal.timeout(30000) });
    const text = await response.text();
    report.apiReads.push({ path: '/builder', status: response.status, ...(response.ok ? {} : { body: sanitizeBody(text) }) });
    assert(response.ok, `Builder read returned ${response.status}: ${sanitizeBody(text)}`);
    return JSON.parse(text);
  }
  function waitForProposalResponse() {
    return page.waitForResponse(response => {
      const responseURL = new URL(response.url());
      return appOrigins.has(responseURL.origin) && responseURL.pathname === proposalPath && response.request().method() === 'POST';
    }, { timeout: 5000 });
  }
  async function observeProposal(label, startedAt, responsePromise) {
    report.activeAction = { label, locator: '[data-testid="construction-proposal-panel"]', startedAt };
    const response = await responsePromise;
    const body = await response.text();
    report.proposalResponses.push({ path: new URL(response.url()).pathname, status: response.status(), ...(response.ok() ? {} : { body: sanitizeBody(body) }) });
    assert(response.ok(), `Removal proposal returned ${response.status()}: ${sanitizeBody(body)}`);
    const proposal = JSON.parse(body);
    const panel = locator('construction-proposal-panel');
    await panel.waitFor({ state: 'visible', timeout: 5000 });
    await page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', null, { timeout: 5000 });
    const view = await panel.evaluate(node => ({ status: node.getAttribute('data-proposal-status'), text: node.innerText, hasApply: Boolean(document.querySelector('[data-testid="construction-apply-proposal"]')) }));
    recordTransition(label, startedAt);
    assert.equal(view.status, 'ready', view.text);
    assert.equal(view.hasApply, true, 'Complete removal must be applicable');
    assert(view.text.includes('Nothing is saved until you apply this removal'));
    assert(proposal.dependencyImpact?.removedStepIds?.length > 0, 'Proposal must identify the complete removed-step set');
    const document = report.baseline.workspace.documents.find(value => value.output.id === args.output);
    const topLevel = document.construction.steps.filter(step => proposal.dependencyImpact.removedStepIds.includes(step.id) && !step.ownerStepId);
    for (const step of topLevel) {
      const warning = locator(`construction-removal-step-${step.id}`);
      assert.equal(await warning.count(), 1, `Removal warning for ${step.id} must be unique`);
      assert.equal(await warning.isVisible(), true, `Removal warning must name ${step.id}`);
    }
    report.activeAction = undefined;
    return proposal;
  }

  await mkdir(args.evidence, { recursive: true });
  try {
    report.baseline = await readBuilder();
    const initialDocument = report.baseline.workspace.documents.find(document => document.output.id === args.output);
    assert(initialDocument, `Output ${args.output} is absent`);
    assert(initialDocument.construction.steps.some(step => step.id === args.step), `Step ${args.step} is absent`);
    await page.setViewportSize({ width: 1280, height: 900 });
    report.activeAction = { label: 'navigate to Builder', locator: builderURL, startedAt: Date.now() };
    await page.goto(builderURL, { waitUntil: 'domcontentloaded' });
    report.activeAction = undefined;
    const table = locator(ids.table);
    await table.waitFor({ state: 'visible', timeout: 5000 });
    await click('select output table', table);
    const stepLocator = locator(ids.step);
    await stepLocator.waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await stepLocator.isEnabled(), true, 'Requested history step must be enabled');
    await click('select construction step', stepLocator);
    const responsePromise = waitForProposalResponse();
    const startedAt = Date.now();
    await click('request removal proposal', locator(ids.remove));
    const proposal = await observeProposal('remove-to-result', startedAt, responsePromise);
    report.preview = { candidateConstruction: proposal.candidateConstruction, removedStepIds: proposal.dependencyImpact.removedStepIds };
    const cancelStartedAt = Date.now();
    await click('cancel removal proposal', locator('construction-cancel-proposal'));
    report.activeAction = { label: 'cancel removal and confirm unchanged workspace', locator: 'construction-proposal-panel', startedAt: cancelStartedAt };
    await locator('construction-proposal-panel').waitFor({ state: 'hidden', timeout: 5000 });
    const afterCancel = await readBuilder();
    assert.deepEqual(afterCancel.workspace, report.baseline.workspace, 'Cancel must leave workspace unchanged');
    assert.equal(afterCancel.draftVersion, report.baseline.draftVersion, 'Cancel must not advance draft version');
    assert.equal(afterCancel.draftDigest, report.baseline.draftDigest, 'Cancel must not change draft digest');
    recordTransition('cancel-to-hidden-and-unchanged-workspace', cancelStartedAt);
    report.cancelCheck = { unchanged: true, draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest };
    if (args.apply) {
      await click('reselect construction step', stepLocator);
      const applyResponse = waitForProposalResponse();
      const proposalStartedAt = Date.now();
      await click('reopen removal proposal', locator(ids.remove));
      const applyProposal = await observeProposal('apply-proposal-ready', proposalStartedAt, applyResponse);
      assert.deepEqual(applyProposal.candidateConstruction, proposal.candidateConstruction, 'Reopened proposal must match preview');
      const applyStartedAt = Date.now();
      await click('apply exact preview', locator('construction-apply-proposal'));
      report.activeAction = { label: 'apply removal and confirm saved state', locator: 'construction-apply-proposal', startedAt: applyStartedAt };
      await locator('construction-proposal-panel').waitFor({ state: 'hidden', timeout: 5000 });
      report.applied = await readBuilder();
      assert(report.applied.draftVersion > report.baseline.draftVersion, 'Apply must advance the draft');
      const appliedDocument = report.applied.workspace.documents.find(document => document.output.id === args.output);
      assert.deepEqual(appliedDocument.construction, proposal.candidateConstruction, 'Apply must save the exact previewed construction');
      await locator(ids.step).waitFor({ state: 'detached', timeout: 5000 });
      recordTransition('apply-to-saved-construction-visible-in-history', applyStartedAt);
      const reloadStartedAt = Date.now();
      report.activeAction = { label: 'reload and confirm restored removal history', locator: builderURL, startedAt: reloadStartedAt };
      await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
      await table.waitFor({ state: 'visible', timeout: 5000 });
      await click('reopen output after reload', table);
      report.activeAction = { label: 'reload and confirm restored removal history', locator: builderURL, startedAt: reloadStartedAt };
      for (const id of proposal.dependencyImpact.removedStepIds) await locator(`construction-history-step-${id}`).waitFor({ state: 'detached', timeout: 5000 });
      report.reloaded = await readBuilder();
      assert.equal(report.reloaded.draftDigest, report.applied.draftDigest, 'Saved removal must persist after reload');
      const reloadedDocument = report.reloaded.workspace.documents.find(document => document.output.id === args.output);
      assert.deepEqual(reloadedDocument.construction, proposal.candidateConstruction, 'Reload must restore the exact applied construction');
      recordTransition('reload-to-restored-removal-history', reloadStartedAt);
    }
    assert.deepEqual(browserErrors, [], 'Unexpected native Playwright browser errors');
    report.status = args.apply ? 'passed' : 'partial';
    cda.check('correctness', 'construction removal cancel preserves exact workspace', true,
      { draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest });
  } catch (error) {
    report.status = 'failed'; report.error = String(error.stack ?? error);
    if (report.activeAction) report.firstFailedAction = { label: report.activeAction.label, locator: report.activeAction.locator, elapsedMs: Date.now() - report.activeAction.startedAt };
    report.browserErrors = browserErrors;
    throw error;
  } finally {
    report.finishedAt = new Date().toISOString();
    await writeFile(join(args.evidence, 'report.json'), JSON.stringify(report, null, 2));
    await cda.attachReport('construction-removal-ui', report);
  }
  return report;
}
