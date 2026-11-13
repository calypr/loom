import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { test, expect } from 'playwright/test';
import { createDevSession, createFreshVerificationFixture, assertOwnedDevSession } from '../../loom-dev.mjs';
import { assertNamedDevSessionEnvironment, fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';
import { resolveGraphRowsGroupPreviewKeys } from '../helpers/graph-rows-complementary-oracle.mjs';
import { validateGraphRowsFixture } from '../helpers/graph-rows-fixture-oracle.mjs';
import { graphRowsNativeIdentity, assertGraphRowsNativeIdentity } from '../helpers/graph-rows-native-identity.mjs';
import { sanitizePayload } from '../helpers/playwright-browser.mjs';
import { runGraphRowsRelatedRows } from '../workflows/graph-rows-related-rows.mjs';
import { runGraphRowsHistoryStress } from '../workflows/graph-rows-history-stress.mjs';
import { runGraphRowsRootProposal } from '../workflows/graph-rows-root-proposal.mjs';
import { runGraphRowsOrdinaryPolicy } from '../workflows/graph-rows-ordinary-policy.mjs';
import { runGraphRowsMixed } from '../workflows/graph-rows-mixed.mjs';
import { runGraphRowsComplementaryConnection } from '../workflows/graph-rows-complementary.mjs';
import { runGraphRowsUpstreamDependency } from '../workflows/graph-rows-upstream-dependency.mjs';

const sorted = rows => structuredClone(rows).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
const documentFor = state => {
  const document = state.workspace?.documents.find(document => document.output.id === 'ResearchSubject');
  assert(document, 'The unchanged Helm recipe must expose its ResearchSubject output');
  return document;
};
const draftIdentity = state => ({ version: state.draftVersion, digest: state.draftDigest });

async function setup(page, request, testInfo, { ordinary = false, preparedHistory = false } = {}) {
  assertNamedDevSessionEnvironment();
  const session = createDevSession({ ...process.env, LOOM_DEV_FIXTURE_DIR: `${process.env.LOOM_DEV_SOURCE_ROOT}/testdata/graph-rows` });
  await assertOwnedDevSession(session, { uiRouting: 'explicit-query' });
  const runId = `gr-${Date.now().toString(36)}-${testInfo.workerIndex}`;
  const historyPrepared = preparedHistory ? JSON.parse(readFileSync(process.env.LOOM_GRAPH_ROWS_HISTORY_PREPARATION, 'utf8')) : undefined;
  const { target, seed } = historyPrepared ?? await createFreshVerificationFixture(session, runId, { populateBootstrap: false });
  const explorerId = ordinary ? seed.bootstrapExplorerId : 'dataframe-default';
  const before = graphRowsNativeIdentity(target);
  if (historyPrepared) assertGraphRowsNativeIdentity(historyPrepared.after, before);
  const apiPath = `${target.apiUrl}${fixtureProjectExplorerCollectionPath(target.fixtureProject)}/${encodeURIComponent(explorerId)}/authoring/v2`;
  const report = { performanceTargetMs: 5000, performanceTargetIsGate: false, functionalWaitTimeoutMs: 30000, status: 'running', target: { project: target.fixtureProject, generation: target.fixtureGeneration, explorerId }, before, actions: [], requests: [], browserErrors: [], writeFailures: [] };
  const capabilityResponses = [];
  const constructionProposalResponses = [];
  const routeChoiceResponses = [];
  const observePage = observed => {
    observed.on('pageerror', error => report.browserErrors.push(error.message));
    observed.on('requestfailed', request => {
      if (request.url().endsWith('/commands')) report.writeFailures.push({ pathname: new URL(request.url()).pathname, method: request.method(), error: request.failure()?.errorText });
    });
    observed.on('response', async response => {
      if (response.url().includes('/authoring/v2/')) {
        if (response.url().endsWith('/construction-proposals') && response.status() === 200) {
          try { constructionProposalResponses.push({ request: response.request().postDataJSON(), proposal: await response.json() }); } catch { /* A cancelled response cannot establish current candidate correspondence. */ }
        }
        if (response.url().endsWith('/construction-capabilities') && response.status() === 200) {
          try { capabilityResponses.push(await response.json()); } catch { /* A cancelled read remains a diagnostic response, never a usable choice. */ }
        }
        if (response.url().endsWith('/related-expand-choices') && response.status() === 200) {
          try { routeChoiceResponses.push(await response.json()); } catch { /* Cancelled read is not a usable route. */ }
        }
        const request = response.request();
        const commandTypes = response.url().endsWith('/commands') ? (request.postDataJSON()?.commands ?? []).map(command => command.type) : undefined;
        let errorCode;
        if (response.status() >= 400) { try { errorCode = (await response.json()).error?.code; } catch { /* Retain missing code as an explicit failed expectation. */ } }
        report.requests.push({ ...(errorCode ? { errorCode } : {}), method: request.method(), pathname: new URL(response.url()).pathname, status: response.status(), ...(commandTypes ? { commandTypes } : {}) });
      }
    });
  };
  observePage(page);
  const state = async (options = {}) => {
    const response = await request.get(`${apiPath}/builder`, { timeout: 30_000, ...options });
    assert.equal(response.status(), 200, `Builder read returned ${response.status()}: ${(await response.text()).slice(0, 500)}`);
    return response.json();
  };
  const baselineStarted = performance.now();
  const baseline = await state({ timeout: 30_000 });
  assert.equal(baseline.kind, 'ExplorerBuilderState');
  assert.equal(typeof baseline.catalog?.snapshotToken, 'string');
  if (!ordinary) {
    assert.deepEqual(baseline.workspace?.documents.map(document => document.output.id), ['DocumentReference', 'ResearchSubject', 'MedicationAdministration', 'Specimen', 'GroupMember'], 'Target-specific minimum-envelope doctor must resolve the unchanged default before Chrome');
  }
  report.minimumEnvelope = { setupBudgetMs: 30_000, durationMs: performance.now() - baselineStarted, kind: baseline.kind, lifecycleState: baseline.lifecycleState, outputs: baseline.workspace?.documents.map(document => document.output.id) ?? [], draftVersion: baseline.draftVersion, draftDigest: baseline.draftDigest };
  const action = async (name, run) => {
    const started = performance.now();
    try {
      const value = await run();
      const elapsedMs = performance.now() - started;
      report.actions.push({ name, elapsedMs, exceedsPerformanceTarget: elapsedMs > report.performanceTargetMs, status: 'passed' });
      return value;
    } catch (error) {
      report.actions.push({ name, elapsedMs: performance.now() - started, status: 'failed', error: error.message });
      report.firstFailure ??= report.actions.at(-1);
      throw error;
    }
  };
  const preview = async ({ candidate = false } = {}) => {
    const container = page.getByTestId('construction-preview');
    await expect(container).toHaveAttribute('data-preview-proposal-id', candidate ? /\S+/ : '');
    await expect(container.getByTestId('recipe-result-identity')).toHaveText(candidate ? 'Proposed recipe result' : 'Saved recipe result');
    await expect(container).toHaveCount(1);
    await expect(container).toHaveAttribute('data-preview-status', 'ready');
    const receiptId = await container.getAttribute('data-preview-receipt-id');
    assert(receiptId, 'A saved preview must retain its receipt identity');
    const outputId = await container.getAttribute('data-preview-output-id');
    assert(outputId);
    const response = await request.post(`${apiPath}/preview`, { timeout: 30_000, data: { receiptId, outputId, limit: 100 } });
    assert.equal(response.status(), 200, `Saved preview read failed: ${(await response.text()).slice(0, 500)}`);
    const value = await response.json();
    assert.equal(value.receiptId, receiptId);
    assert.equal(value.outputId, outputId);
    assert.equal(value.sampled, false, 'The bounded fixture must be completely returned, not treated as a universal cardinality sample');
    assert(Array.isArray(value.rows));
    return value;
  };
  const open = async (selectedPage = page) => {
    const url = new URL(target.uiUrl);
    url.searchParams.set('project', target.fixtureProject);
    url.searchParams.set('explorer', explorerId);
    await selectedPage.goto(url.href, { waitUntil: 'domcontentloaded' });
    await selectedPage.getByTestId('construction-table-ResearchSubject').click();
    await expect(selectedPage.getByTestId('authored-traversal-editor')).toBeVisible();
    await expect(selectedPage.getByTestId('construction-preview')).toHaveAttribute('data-preview-status', 'ready');
  };
  let sentenceProjection;
  const compareRowNotation = async (savedState, label) => {
    const document = documentFor(savedState);
    const response = await request.post(`${apiPath}/authoring-network`, { timeout: 30_000, data: {
      snapshotToken: savedState.catalog.snapshotToken, expectedDraftVersion: savedState.draftVersion,
      expectedDraftDigest: savedState.draftDigest, outputId: document.output.id,
    } });
    assert.equal(response.status(), 200); const { network } = await response.json();
    assert.equal(network.outputId, document.output.id);
    if (!sentenceProjection) {
      const requireUI = createRequire(`${target.sourceRoot}/ui/package.json`);
      const { build } = requireUI('esbuild');
      const bundle = await build({ entryPoints: [`${target.sourceRoot}/ui/packages/loom-ui/src/features/ExplorerBuilder/components/rowRecipeExpression.ts`], bundle: true,
        write: false, platform: 'node', format: 'esm' });
      sentenceProjection = (await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`)).rowRecipeSentence;
    }
    const sentence = sentenceProjection(document, network);
    assert(sentence.includes(document.rootResourceType));
    const measured = await page.evaluate((sentence) => {
      const clauses = document.querySelector('[data-testid="row-recipe-expression"]');
      if (!clauses) throw new Error('The real editable clause form must exist for the notation comparison');
      const bounds = clauses.getBoundingClientRect(); const computed = getComputedStyle(clauses);
      const prototype = document.createElement('div');
      // This offscreen, read-only text measurement is a prototype comparison,
      // not a substitute for any native product input or visibility assertion.
      Object.assign(prototype.style, { position: 'fixed', left: '-100000px', top: '0', width: `${bounds.width}px`,
        fontFamily: computed.fontFamily, fontSize: computed.fontSize, fontWeight: computed.fontWeight, fontStyle: computed.fontStyle, lineHeight: computed.lineHeight, whiteSpace: 'normal', overflowWrap: 'anywhere' });
      prototype.textContent = sentence;
      try { document.body.appendChild(prototype); return { width: bounds.width, clauseViewportHeight: bounds.height,
        sentenceHeight: prototype.getBoundingClientRect().height, font: computed.font, lineHeight: computed.lineHeight }; }
      finally { prototype.remove(); }
    }, sentence);
    assert(measured.width > 150 && measured.clauseViewportHeight > 80);
    assert(measured.sentenceHeight > 0);
    report.notationComparison ??= [];
    report.notationComparison.push({ label, scope: 'Read-only notation prototype; native acceptance targets the real editable clauses',
      outputId: document.output.id, recipePin: document.importedRecipe, sourceFingerprint: before.sourceFingerprint,
      sourceColumnCount: document.columns.length, operationCount: document.construction?.steps.length ?? 0,
      sentence, characterCount: sentence.length, ...measured, selected: 'editable clauses',
      reason: `${measured.sentenceHeight > measured.clauseViewportHeight ? `The full sentence needs ${Math.ceil(measured.sentenceHeight)}px while the available clause viewport is ${Math.floor(measured.clauseViewportHeight)}px.` : 'Both display notations fit this fixture.'} Named editable clauses keep long column lists bounded and Group keys separate from summaries and retained values.` });
  };
  const captureLayout = async (label, { view = 'Both editors', inspector = false, controls = [] } = {}) => {
    const navigation = page.getByRole('navigation', { name: 'Recipe presentation', exact: true });
    await navigation.getByRole('button', { name: view, exact: true }).click();
    const pageY = await page.evaluate(() => scrollY);
    const activeInspector = page.locator('[aria-label="Change editor"]:visible, [data-testid="authored-occurrence-inspector"]:visible');
    if (inspector) { await expect(activeInspector).toHaveCount(1); await activeInspector.scrollIntoViewIfNeeded(); }
    for (const control of controls) await control.scrollIntoViewIfNeeded();
    assert.equal(await page.evaluate(() => scrollY), pageY, 'Routine proposal controls must not require page-level scrolling');
    const metrics = await page.evaluate(() => {
      const rect = selector => {
        const node = document.querySelector(selector);
        if (!node || node.getClientRects().length === 0) return null;
        const { x, y, width, height, right, bottom } = node.getBoundingClientRect();
        return { x, y, width, height, right, bottom };
      };
      const visibleInspectors = [...document.querySelectorAll('[aria-label="Change editor"], [data-testid="authored-occurrence-inspector"]')]
        .filter(node => node.getClientRects().length > 0);
      const meaning = document.querySelector('[data-testid="row-recipe-expression"] [data-term-kind="source-rows"]');
      const graphMeaning = document.querySelector('[data-testid="authored-traversal-editor"] p[aria-live="polite"]');
      const text = meaning?.getClientRects().length ? meaning.textContent : graphMeaning?.textContent;
      return { viewport: { width: innerWidth, height: innerHeight }, scroll: { width: document.documentElement.scrollWidth, height: document.documentElement.scrollHeight },
        expression: rect('[data-testid="row-recipe-expression"]'), network: rect('[data-testid="authored-traversal-editor"]'),
        preview: rect('[data-testid="construction-preview"]'), activeInspectors: visibleInspectors.length,
        inspectorBounds: visibleInspectors.map(node => { const { x, y, width, height, right, bottom } = node.getBoundingClientRect(); return { x, y, width, height, right, bottom }; }), rowMeaning: text?.trim() };
    });
    report.layoutComparison ??= [];
    const layoutEvidence = { label, embedding: 'owned standalone fixture; authenticated Calypr remains separate', view, ...metrics, status: 'unverified' };
    report.layoutComparison.push(layoutEvidence);
    const fits = (name, bounds) => {
      assert(bounds && bounds.width > 150 && bounds.height > 80, `${name} must retain useful visible space`);
      assert(bounds.x >= 0 && bounds.right <= metrics.viewport.width + 1 && bounds.y >= 0 && bounds.bottom <= metrics.viewport.height + 1, `${name} must remain inside the viewport`);
    };
    if (view !== 'Record network') fits('Row expression', metrics.expression);
    if (view !== 'Define a row') fits('Record network', metrics.network);
    fits('Result preview', metrics.preview); assert(metrics.preview.height >= 150, 'Result preview must remain at least150px high');
    assert(metrics.rowMeaning?.includes('ResearchSubject'), 'Current row meaning must remain available in each presentation');
    const meaning = view === 'Record network' ? page.getByTestId('authored-traversal-editor').locator('p[aria-live="polite"]') : page.getByTestId('row-recipe-expression').locator('[data-term-kind="source-rows"]');
    await expect(meaning).toBeInViewport({ ratio: 1 });
    assert(metrics.scroll.width <= metrics.viewport.width + 1 && metrics.scroll.height <= metrics.viewport.height + 1, 'Routine editing must use bounded internal navigation');
    assert(metrics.activeInspectors <= 1, 'Recipe editors cannot show competing active inspectors');
    if (inspector) {
      assert.equal(metrics.activeInspectors, 1, 'Exactly one active inspector must describe the selected or proposed fact');
      fits('Active inspector', metrics.inspectorBounds[0]);
      await expect(activeInspector).toBeInViewport({ ratio: 1 });
    }
    for (const control of controls) {
      await expect(control).toBeInViewport({ ratio: 1 });
      const bounds = await control.boundingBox();
      assert(bounds && bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= metrics.viewport.width + 1 && bounds.y + bounds.height <= metrics.viewport.height + 1, 'Proposal controls must remain reachable inside the viewport');
    }
    const path = testInfo.outputPath(`${label}.png`); await page.screenshot({ path });
    layoutEvidence.status = 'passed'; layoutEvidence.screenshot = path;
  };
  const finish = async () => {
    try {
      report.after = graphRowsNativeIdentity(target);
      assertGraphRowsNativeIdentity(before, report.after);
      assert.deepEqual(report.browserErrors, [], 'Unexpected browser exception');
      assert.deepEqual(report.writeFailures, [], 'Failed or cancelled native save');
      assert.deepEqual(report.requests.filter(request => request.status >= 400 && request.status !== 409), report.expectedRejectedResponses ?? [], 'Unexpected authoring HTTP failure');
      assert.equal(report.requests.filter(request => request.status === 409).length, report.expectedConflictCount ?? 0, 'Unexpected or missing authoring conflicts');
    } catch (error) {
      report.status = 'failed'; report.finalIntegrityError = error.message;
      // Preserve the first native failure while retaining every integrity failure.
      if (!report.error) throw error;
    } finally {
      if (report.status !== 'failed') report.status = testInfo.status === testInfo.expectedStatus ? 'passed' : 'failed';
      await testInfo.attach('graph-rows-native-evidence.json', { body: Buffer.from(JSON.stringify(sanitizePayload(report), null, 2)), contentType: 'application/json' });
    }
  };
  return { target, explorerId, apiPath, report, baseline, state, action, preview, open, finish, historyPrepared, observePage, capabilityResponses, constructionProposalResponses, routeChoiceResponses, captureLayout, compareRowNotation };
}

async function expectRootIDs(preview, document, ids) {
  const id = document.columns.find(column => column.source?.imported?.outputColumn === 'id');
  assert(id, 'The default root ID binding is required');
  assert.deepEqual(sorted(preview.rows.map(row => row[id.column])), sorted(ids));
}

async function previewRelationship(page, action) {
  const review = page.getByRole('region', { name: 'Proposed related records' });
  await action('Preview relationship', async () => {
    await review.getByRole('button', { name: 'Preview relationship', exact: true }).click();
    await expect(review.getByRole('button', { name: 'Apply relationship', exact: true })).toBeEnabled();
    await expect(page.getByTestId('construction-preview')).toBeVisible();
  });
  return review;
}

async function addStudy(page) {
  const graph = page.getByTestId('authored-traversal-editor');
  const explore = graph.getByRole('button', { name: 'Explore available connections', exact: true });
  if (await explore.count()) await explore.click();
  await graph.locator('button[data-occurrence-id="base"]').click();
  await page.getByRole('checkbox', { name: 'Show full network', exact: true }).check();
  const node = page.locator('.react-flow__node').filter({ hasText: 'ResearchStudy' });
  await expect(node).toHaveCount(1);
  // Enter is a native keyboard path through the production graph handler.
  await node.focus();
  await node.press('Enter');
  await page.getByRole('button', { name: 'Add traversal', exact: true }).click();
}

async function studyInspector(page) {
  const graph = page.getByTestId('authored-traversal-editor');
  const authored = graph.getByRole('button', { name: 'Show authored traversal', exact: true });
  if (await authored.count()) await authored.click();
  await graph.getByRole('navigation', { name: 'Authored occurrences', exact: true }).getByRole('button').filter({ hasText: 'ResearchStudy' }).click();
  return page.getByTestId('authored-occurrence-inspector');
}

async function changeStudyMatch(page, mode) {
  const inspector = await studyInspector(page);
  await inspector.getByRole('combobox', { name: /Match policy for .*ResearchStudy/ }).selectOption(mode);
}

test('GRAPH-ROWS imported source add cancel apply edit remove reload and real stale draft', async ({ page, request, context }, testInfo) => {
  const run = await setup(page, request, testInfo);
  const oracle = validateGraphRowsFixture();
  try {
    await run.open();
    const original = run.baseline;
    assert.equal(original.workspace.documents.length, 5, 'The real default recipe must import all five outputs');
    const pin = documentFor(original).importedRecipe;
    assert.equal(pin.name, 'calypr-meta-default');
    run.report.defaultOutputRows = {};
    for (const [outputId, expected] of Object.entries(oracle.defaultOutputIdentities)) {
      await run.action(`Inspect exact default ${outputId} rows`, async () => {
        await page.getByTestId(`construction-table-${outputId}`).click();
        const preview = await run.preview();
        const document = original.workspace.documents.find(document => document.output.id === outputId);
        assert(document);
        if (outputId === 'GroupMember') {
          const group = document.columns.find(column => column.source?.imported?.outputColumn === 'group_id');
          const member = document.columns.find(column => column.source?.imported?.outputColumn === 'member_id');
          assert(group && member);
          const pairs = preview.rows.map(row => [row[group.column], row[member.column]]);
          assert.deepEqual(sorted(pairs), sorted(expected));
          run.report.defaultOutputRows[outputId] = pairs;
        } else {
          await expectRootIDs(preview, document, expected);
          run.report.defaultOutputRows[outputId] = expected;
        }
      });
    }
    await page.getByTestId('construction-table-ResearchSubject').click();
    const originalPreview = await run.preview();
    await expectRootIDs(originalPreview, documentFor(original), ['rs-zero', 'rs-one', 'rs-many', 'rs-shared']);
    const layout = async () => {
      const bounds = await page.getByTestId('construction-preview').boundingBox();
      assert(bounds && bounds.height >= 150 && bounds.y + 150 <= 900, 'Useful actual preview must remain visible at 1536x900');
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'No page-level horizontal overflow');
      await expect(page.getByTestId('row-recipe-expression').getByRole('button', { name: 'Edit source rows: One row per ResearchSubject record', exact: true })).toBeVisible();
    };
    await layout();
    await page.screenshot({ path: testInfo.outputPath('main-editor-1536x900.png') });
    await page.setViewportSize({ width: 1536, height: 1100 });
    await page.setViewportSize({ width: 1536, height: 900 });
    await layout();

    await addStudy(page);
    let review = await previewRelationship(page, run.action);
    await run.action('Cancel relationship without mutation', async () => {
      await review.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(review).toHaveCount(0);
      assert.deepEqual(draftIdentity(await run.state()), draftIdentity(original));
    });
    await addStudy(page);
    review = await previewRelationship(page, run.action);
    await run.action('Apply optional Study relationship', async () => {
      await review.getByRole('button', { name: 'Apply relationship', exact: true }).click();
      await expect(review).toHaveCount(0);
      await expect(page.getByTestId('construction-preview')).toHaveAttribute('data-preview-status', 'ready');
    });
    let added = await run.state();
    const addition = documentFor(added).importedSource.relationships.additions.find(item => item.resourceType === 'ResearchStudy');
    assert(addition, 'Apply must persist the typed native source overlay');
    assert.equal(addition.parentOccurrenceId, 'base');
    assert.equal(addition.matchMode, 'OPTIONAL');
    assert.deepEqual(documentFor(added).importedRecipe, pin);
    const studyID = addition.fields.find(field => field.fieldPath === 'id' || field.fieldPath === 'ResearchStudy.id');
    assert(studyID && studyID.valueMode === 'ALL', 'Added related IDs need the explicit ALL value policy');
    const studyColumns = documentFor(added).columns.filter(column => column.columnId === studyID.columnId);
    assert.equal(studyColumns.length, 1, 'The stable authored field ID must map to exactly one resolved public source column');
    const studyColumn = studyColumns[0];
    const networkResponse = await request.post(`${run.apiPath}/authoring-network`, { timeout: 30_000, data: { snapshotToken: added.catalog.snapshotToken, expectedDraftVersion: added.draftVersion, expectedDraftDigest: added.draftDigest, outputId: 'ResearchSubject' } });
    assert.equal(networkResponse.status(), 200);
    const network = await networkResponse.json();
    const bindings = network.network.bindings.filter(binding => binding.columnId === studyID.columnId && binding.occurrenceId === addition.occurrenceId);
    assert.equal(bindings.length, 1);
    assert.equal(bindings[0].status, 'EXACT', 'The native source field must retain explicit server-owned occurrence provenance');
    assert.equal(bindings[0].valueMode, 'ALL', 'Network inspection must expose the canonical authored policy rather than a compiler shape');
    run.report.studyBinding = { fieldId: studyID.columnId, publicColumn: studyColumn.column, occurrenceId: addition.occurrenceId, sourceStepId: bindings[0].sourceStepId };
    const assertStudyRows = async (expected) => {
      const current = await run.preview();
      const document = documentFor(await run.state());
      const id = document.columns.find(column => column.source?.imported?.outputColumn === 'id');
      assert.deepEqual(sorted(current.rows.map(row => ({ identity: [row[id.column]], studyIds: row[studyColumn.column] }))),
        sorted(expected.map(({ identity, studyIds }) => ({ identity, studyIds }))));
    };
    await assertStudyRows(oracle.nativeStudy);
    await run.action('Reload optional Study relationship', async () => { await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); await assertStudyRows(oracle.nativeStudy); });
    assert.equal(documentFor(await run.state()).importedSource.relationships.additions[0].occurrenceId, addition.occurrenceId);

    await changeStudyMatch(page, 'REQUIRED');
    review = await previewRelationship(page, run.action);
    await run.action('Apply required Study match', async () => {
      await review.getByRole('button', { name: 'Apply relationship', exact: true }).click();
      await expect(review).toHaveCount(0);
      await assertStudyRows(oracle.nativeStudy.filter(row => row.studyIds.length));
    });
    await run.action('Reload required Study match', async () => { await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); await assertStudyRows(oracle.nativeStudy.filter(row => row.studyIds.length)); });

    // Two real editors prepare against the same saved version. The winning edit
    // is a native UI save, not an injected conflict response or API write.
    const competing = await context.newPage();
    run.observePage(competing);
    await run.open(competing);
    await changeStudyMatch(competing, 'OPTIONAL');
    await previewRelationship(competing, run.action);
    await page.bringToFront();
    await changeStudyMatch(page, 'OPTIONAL');
    review = await previewRelationship(page, run.action);
    await review.getByRole('button', { name: 'Apply relationship', exact: true }).click();
    await expect(review).toHaveCount(0);
    const winner = await run.state();
    await competing.bringToFront();
    const staleResponse = competing.waitForResponse(response => response.url().endsWith('/commands') && response.request().method() === 'POST');
    await competing.getByRole('button', { name: 'Apply relationship', exact: true }).click();
    const staleReply = await staleResponse;
    const staleStatus = staleReply.status();
    const staleBody = await staleReply.json();
    assert.equal(staleBody.error?.code, 'DRAFT_CONFLICT', 'Only the real competing draft conflict establishes stale recovery');
    run.report.staleConflict = { rejectedOwner: staleReply.request().postDataJSON(), winner: draftIdentity(winner), errorCode: staleBody.error.code };
    if (staleStatus === 409) run.report.expectedConflictCount = 1;
    assert.equal(staleStatus, 409, 'The second real editor must not overwrite the winner');
    await expect(competing.getByRole('region', { name: 'Proposed related records' })).toHaveCount(0);
    const cancelStaleOwner = competing.getByRole('button', { name: 'Cancel earlier recipe change', exact: true });
    await expect(cancelStaleOwner).toBeVisible();
    assert.deepEqual(draftIdentity(await run.state()), draftIdentity(winner));
    await cancelStaleOwner.click();
    await expect((await studyInspector(competing)).getByRole('combobox', { name: /Match policy for .*ResearchStudy/ })).toBeEnabled();
    assert.deepEqual(draftIdentity(await run.state()), draftIdentity(winner), 'Stale Apply must preserve the winning saved draft');
    await competing.close();
    await page.bringToFront();

    await (await studyInspector(page)).getByRole('button', { name: /^Remove .* relationship$/ }).click();
    review = await previewRelationship(page, run.action);
    await run.action('Remove Study relationship', async () => { await review.getByRole('button', { name: 'Apply relationship', exact: true }).click(); await expect(review).toHaveCount(0); });
    await run.action('Reload removed relationship', async () => { await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); });
    const removed = await run.state();
    const removedOverlay = documentFor(removed).importedSource.relationships;
    assert(removedOverlay.removals.includes(addition.occurrenceId), 'Removal records the explicit occurrence tombstone');
    assert.equal(removedOverlay.additions.filter(item => !removedOverlay.removals.includes(item.occurrenceId)).length, 0, 'No project-local relationship remains active');
    assert(!documentFor(removed).columns.some(column => column.columnId === addition.fields[0].columnId), 'Removed relationship output column is absent');
    assert.deepEqual(documentFor(removed).importedRecipe, pin);
    await expectRootIDs(await run.preview(), documentFor(removed), ['rs-zero', 'rs-one', 'rs-many', 'rs-shared']);
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows), 'Removal restores exact original values');
    assert(!run.report.requests.flatMap(request => request.commandTypes ?? []).includes('ADD_ROUTE'), 'Imported graph actions must never send ordinary ADD_ROUTE');
  } catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; } finally { await run.finish(); }
});

test('GRAPH-ROWS carry-in source step edit reload restore and native Group contributor lifecycle', async ({ page, request }, testInfo) => {
  const run = await setup(page, request, testInfo);
  try {
    await run.open();
    const original = run.baseline;
    const originalDocument = documentFor(original);
    const originalPreview = await run.preview();
    const step = originalDocument.importedSource.steps.find(step => step.kind === 'CATALOG_PROJECTION' && step.editable && step.maxColumns > 1);
    assert(step, 'The real recipe must provide a bounded editable source projection');
    const openStep = async () => {
      const sourceStep = page.getByTestId(`imported-source-step-${step.id}`);
      const details = sourceStep.locator('xpath=ancestor::details[1]');
      if (await details.count()) {
        if (await details.getAttribute('open') === null) await details.locator('summary').click();
      }
      await sourceStep.click();
      await expect(page.getByTestId('imported-source-step-editor')).toBeVisible();
    };
    const previewSourceStep = async (name) => {
      const review = page.getByRole('region', { name: 'Proposed source step', exact: true });
      await run.action(name, async () => {
        await review.getByRole('button', { name: 'Preview source step', exact: true }).click();
        await expect(review.getByTestId('imported-source-step-apply')).toBeEnabled();
        const displayed = page.getByTestId('construction-preview');
        await expect(displayed).toBeVisible();
        const receiptId = await displayed.getAttribute('data-preview-receipt-id');
        assert(receiptId, 'Source preview must be bound to its candidate receipt');
        const response = await request.post(`${run.apiPath}/preview`, { timeout: 30_000, data: { receiptId, outputId: 'ResearchSubject', limit: 100 } });
        assert.equal(response.status(), 200);
        const candidate = await response.json();
        assert.equal(candidate.receiptId, receiptId);
        assert.deepEqual(sorted(candidate.rows), sorted(originalPreview.rows), 'The bounded source proposal must show exact unchanged fixture values');
      });
      return review;
    };
    await openStep();
    await page.getByTestId('imported-source-max-columns').fill(String(step.maxColumns - 1));
    let sourceReview = await previewSourceStep('Preview bounded source step before Cancel');
    await run.action('Cancel source preview without mutation', async () => {
      await sourceReview.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
      assert.deepEqual(draftIdentity(await run.state()), draftIdentity(original));
      assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows));
    });
    await openStep();
    await page.getByTestId('imported-source-max-columns').fill(String(step.maxColumns - 1));
    sourceReview = await previewSourceStep('Preview bounded source-step edit');
    await run.action('Apply bounded source-step edit', async () => { await page.getByTestId('imported-source-step-apply').click(); await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0); });
    await run.action('Reload edited source step', async () => { await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); });
    let changed = await run.state();
    assert.equal(documentFor(changed).importedSource.steps.find(item => item.id === step.id).maxColumns, step.maxColumns - 1);
    assert(changed.draftVersion > original.draftVersion, 'The legitimate revision must advance');
    assert.deepEqual(documentFor(changed).importedRecipe, originalDocument.importedRecipe);
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows), 'A cap above populated columns preserves the exact values');
    await openStep();
    await page.getByTestId('imported-source-max-columns').fill(String(step.maxColumns));
    sourceReview = await previewSourceStep('Preview restored source-step settings');
    await run.action('Restore bounded source-step settings', async () => { await page.getByTestId('imported-source-step-apply').click(); await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0); });
    await run.action('Reload restored source step', async () => { await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); });
    const restored = await run.state();
    assert.equal(documentFor(restored).importedSource.steps.find(item => item.id === step.id).maxColumns, step.maxColumns);
    assert(restored.draftVersion > changed.draftVersion);
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows));
    const status = documentFor(restored).columns.find(column => column.source?.imported?.outputColumn === 'status');
    assert(status, 'Group must bind the actual root status column');
    const genders = documentFor(restored).columns.filter(column => /gender/i.test(`${column.label} ${column.source?.imported?.outputColumn}`));
    assert.equal(genders.length, 1, 'The default fixture has one explicit Patient gender projection');
    const gender = genders[0];
    const tupleOracle = validateGraphRowsFixture().groupKeepMissing;
    const currentGroupKeyBindings = async preview => {
      const matches = () => run.constructionProposalResponses.filter(record => record.proposal.preview?.receiptId === preview.receiptId);
      await expect.poll(() => matches().length, { message: 'The visible Group preview must have one exact READY proposal response' }).toBe(1);
      const bindings = resolveGraphRowsGroupPreviewKeys({ ...matches()[0], preview, sourceColumns: [gender, status],
        identity: { snapshotToken: restored.catalog.snapshotToken, draftVersion: restored.draftVersion, draftDigest: restored.draftDigest, outputId: 'ResearchSubject' } });
      (run.report.groupPreviewKeyBindings ??= []).push({ receiptId: preview.receiptId, bindings });
      return bindings;
    };
    await page.getByTestId('row-recipe-expression').getByRole('button', { name: 'Edit source rows: One row per ResearchSubject record', exact: true }).click();
    await page.getByTestId('construction-action-group-rows').click();
    const editor = page.getByTestId('construction-reshape-group');
    await expect(editor).toBeVisible();
    await editor.getByText('Add grouping field', { exact: true }).click();
    for (const key of [gender, status]) await editor.getByTestId('construction-group-by-field-list').locator(`input[type="checkbox"][value="${key.columnId}"]`).click();
    await expect(editor.getByTestId('construction-group-selected-keys').locator(`input[type="checkbox"][value="${status.columnId}"]`)).toBeChecked();
    await expect(editor.getByTestId('construction-group-selected-keys')).toContainText(status.label);
    await editor.getByText('Add grouping field', { exact: true }).click();
    await expect(editor.getByTestId('construction-group-selected-keys')).toContainText(status.label);
    const proposal = page.getByTestId('construction-proposal-panel');
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    const candidate = page.getByTestId('construction-preview');
    await expect(candidate).toHaveCount(1);
    await expect(candidate).toBeVisible();
    await expect(editor.getByTestId('construction-group-selected-keys')).toContainText(gender.label);
    const tuplePreview = await run.preview({ candidate: true });
    const tupleKeys = await currentGroupKeyBindings(tuplePreview);
    assert.deepEqual(sorted(tuplePreview.rows.map(row => tupleKeys.map(key => row[key.name]))), sorted(tupleOracle.map(row => row.identity)), 'Group preview must prove the complete literal key tuple, including the missing gender');
    await run.action('Cancel Group without saved mutation', async () => { await page.getByTestId('construction-cancel-proposal').click(); await expect(proposal).toHaveCount(0); assert.deepEqual(draftIdentity(await run.state()), draftIdentity(restored)); });
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows));

    await expect(page.getByRole('dialog', { name: 'Row definition settings', exact: true })).toHaveCount(0);

    // Reopen after the carry-in Cancel and complete the actual saved Group
    // lifecycle. Contributor selection is bound to the returned root occurrence
    // and stage capability, never to an arbitrary same-labelled graph node.
    await page.getByTestId('row-recipe-expression').getByRole('button', { name: 'Edit source rows: One row per ResearchSubject record', exact: true }).click();
    await page.getByTestId('construction-action-group-rows').click();
    await expect(editor).toBeVisible();
    await editor.getByText('Add grouping field', { exact: true }).click();
    for (const key of [gender, status]) await editor.getByTestId('construction-group-by-field-list').locator(`input[type="checkbox"][value="${key.columnId}"]`).click();
    await expect(editor.getByTestId('construction-group-selected-keys').locator(`input[type="checkbox"][value="${status.columnId}"]`)).toBeChecked();
    await editor.getByText('Add grouping field', { exact: true }).click();
    await editor.getByRole('textbox', { name: 'Summary output name 1', exact: true }).fill('subject_count');
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    const initialGroupPreview = await run.preview({ candidate: true });
    const initialKeys = await currentGroupKeyBindings(initialGroupPreview);
    assert.deepEqual(sorted(initialGroupPreview.rows.map(row => [...initialKeys.map(key => row[key.name]), row.subject_count])), sorted(tupleOracle.map(row => [...row.identity, row.count])));
    await run.action('Apply initial Group before selecting contributor values', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
    });
    const initialGroupedState = await run.state();
    await run.action('Return to table after initial Group', async () => {
      const rowDialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
      await expect(rowDialog).toHaveCount(0);
      assert.deepEqual(draftIdentity(await run.state()), draftIdentity(initialGroupedState));
    });
    const initialGroup = documentFor(initialGroupedState).construction.steps.find(step => step.operation.kind === 'GROUP');
    assert(initialGroup);
    const statusKey = initialGroup.operation.group.keys.find(key => key.inputColumnId === status.columnId);
    assert(statusKey);
    const statusGroupOutput = initialGroup.outputs.find(column => column.id === statusKey.outputColumnId);
    const genderKey = initialGroup.operation.group.keys.find(key => key.inputColumnId === gender.columnId);
    assert(genderKey);
    const genderGroupOutput = initialGroup.outputs.find(column => column.id === genderKey.outputColumnId);
    assert(genderGroupOutput);
    assert(statusGroupOutput, 'Resolve the saved Group key by output identity rather than its source name');
    await page.getByTestId(`construction-history-step-${initialGroup.id}`).click();
    await expect(editor).toBeVisible();
    const rootIDColumns = originalDocument.columns.filter(column => column.source?.imported?.outputColumn === 'id');
    assert.equal(rootIDColumns.length, 1, 'Use the explicit pinned root ID binding, not a same-labelled related field');
    const rootID = rootIDColumns[0];
    assert.deepEqual(initialGroup.inputs, [{ kind: 'SOURCE_PROJECTION' }]);
    const matchingInputColumns = () => [...run.capabilityResponses].reverse().find(response =>
      response.outputId === 'ResearchSubject' && response.draftVersion === initialGroupedState.draftVersion &&
      response.selectedStage.id === 'source_projection')?.selectedStage.columns.filter(column => column.id === rootID.columnId) ?? [];
    await expect.poll(() => matchingInputColumns().length, { message: 'The exact current Group input must advertise the pinned root ID column.' }).toBe(1);
    assert.equal(matchingInputColumns()[0].type, 'string');
    run.report.contributorBinding = { sourceStageId: 'source_projection', inputColumnId: rootID.columnId, sourceOutputColumn: 'id', policy: 'ALL' };
    await editor.getByText('Add contributor value', { exact: true }).click();
    await editor.getByTestId(`construction-group-add-column-input-${rootID.columnId}`).click();
    await expect(editor.getByTestId(`construction-group-add-column-input-${rootID.columnId}`)).toHaveCount(0);
    await expect(editor.getByTestId('construction-group-row-value')).toHaveCount(1);
    await expect(editor.getByTestId('construction-group-retained-values')).toContainText(rootID.label);
    await editor.getByRole('combobox', { name: 'Keep values 1', exact: true }).selectOption('ALL');
    await editor.getByRole('textbox', { name: 'Added column name 1', exact: true }).fill('contributor_subject_ids');
    await editor.getByText('Add contributor value', { exact: true }).click();
    await expect(editor.getByTestId('construction-group-retained-values')).toContainText(rootID.label);
    const exactGroups = tupleOracle.map(row => ({ gender: row.identity[0], status: row.identity[1], count: row.count, contributorIDs: [...row.contributorSubjectIds].sort() }));
    const assertGroupRows = async ({ candidate = false, expected = exactGroups } = {}) => {
      const preview = await run.preview({ candidate });
      // ALL has no authored contributor order. Assert the complete multiset,
      // retaining duplicates; raw post-Apply/reload rows are compared separately.
      const normalized = preview.rows.map(row => {
        assert(Array.isArray(row.contributor_subject_ids), 'ALL contributor values must be a list');
        return { gender: row[genderGroupOutput.name], status: row[statusGroupOutput.name], count: row.subject_count, contributorIDs: [...row.contributor_subject_ids].sort() };
      });
      assert.deepEqual(sorted(normalized), sorted(expected));
      return preview;
    };
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await assertGroupRows({ candidate: true });
    await run.action('Apply Group with retained contributors', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
      await assertGroupRows();
    });
    const grouped = await run.state();
    const savedGroup = documentFor(grouped).construction.steps.find(step => step.operation.kind === 'GROUP');
    assert(savedGroup, 'Native Apply must save the Group operation');
    await run.compareRowNotation(grouped, 'actual-default-with-complete-Group-key-tuple');
    const rowGroupTerm = page.getByTestId('row-recipe-expression').locator(`[data-term-kind="operation"][data-operation-id="${savedGroup.id}"]`);
    const graphGroupTerm = page.getByTestId('authored-traversal-editor').locator(`[data-term-kind="operation"][data-operation-id="${savedGroup.id}"]`);
    for (const term of [rowGroupTerm, graphGroupTerm]) {
      await expect(term).toContainText(gender.label);
      await expect(term).toContainText(status.label);
      await expect(term).toContainText(/Input(?: stage)?:.*Source/i);
    }

    assert.equal(savedGroup.id, initialGroup.id);
    assert.deepEqual(savedGroup.operation, initialGroup.operation, 'Adding contributor values edits the saved Group without retargeting its key/summary identities');
    assert.equal(savedGroup.operation.group.keys.length, 2);
    assert.deepEqual(savedGroup.operation.group.keys.map(key => key.inputColumnId), [gender.columnId, status.columnId]);
    assert.equal(savedGroup.operation.group.missingKeyPolicy, 'GROUP');
    assert.equal(savedGroup.operation.group.aggregates[0].operation, 'COUNT_ROWS');
    assert.equal(savedGroup.rowValues.length, 1);
    assert.equal(savedGroup.rowValues[0].policy, 'ALL');
    assert.equal(savedGroup.rowValues[0].inputColumnId, rootID.columnId);
    assert.equal(savedGroup.rowValues[0].fieldSource, undefined, 'Retained projected values use the exact stage input, without inventing a source choice');
    const contributorOutput = savedGroup.outputs.find(output => output.id === savedGroup.rowValues[0].outputColumnId);
    assert.equal(contributorOutput.name, 'contributor_subject_ids');
    assert.deepEqual(documentFor(grouped).importedRecipe, originalDocument.importedRecipe);
    const appliedGroupRows = (await assertGroupRows()).rows;
    await run.action('Reload Group with exact contributor values and policy', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId('construction-table-ResearchSubject').click();
      assert.deepEqual(sorted((await assertGroupRows()).rows), sorted(appliedGroupRows));
      assert.deepEqual(documentFor(await run.state()).construction.steps.find(step => step.id === savedGroup.id), savedGroup);
    });
    const policyBaseline = await run.state();
    const excludedGroups = validateGraphRowsFixture().groupExcludeMissing.map(row => ({ gender: row.identity[0], status: row.identity[1], count: row.count, contributorIDs: [...row.contributorSubjectIds].sort() }));
    const editMissingPolicy = async policy => {
      await rowGroupTerm.getByRole('button', { name: /^Edit operation [0-9]+:/ }).click();
      await expect(editor).toBeVisible();
      const advanced = editor.getByTestId('construction-reshape-group-advanced');
      if (await advanced.getAttribute('open') === null) await advanced.locator('summary').first().click();
      await editor.getByRole('combobox', { name: 'Missing group key policy', exact: true }).selectOption(policy);
      await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    };
    const assertMissingPolicySaved = async policy => {
      const current = await run.state();
      const expectedDocument = structuredClone(documentFor(policyBaseline));
      expectedDocument.construction.steps.find(step => step.id === savedGroup.id).operation.group.missingKeyPolicy = policy;
      assert.deepEqual(documentFor(current), expectedDocument, 'Policy editing must preserve the same Group, key/output IDs, contributors, pin and operation order');
      assert.deepEqual(current.workspace.documents.filter(document => document.output.id !== 'ResearchSubject'), policyBaseline.workspace.documents.filter(document => document.output.id !== 'ResearchSubject'));
      await assertGroupRows({ expected: policy === 'EXCLUDE' ? excludedGroups : exactGroups });
    };
    await run.action('Preview missing-key exclusion through the existing row expression term', async () => {
      await editMissingPolicy('EXCLUDE'); await assertGroupRows({ candidate: true, expected: excludedGroups });
    });
    await run.action('Cancel missing-key exclusion without changing the saved Group', async () => {
      await page.getByTestId('construction-cancel-proposal').click(); await expect(proposal).toHaveCount(0);
      const canceled = await run.state(); assert.deepEqual(draftIdentity(canceled), draftIdentity(policyBaseline));
      assert.deepEqual(canceled.workspace, policyBaseline.workspace); await assertMissingPolicySaved('GROUP');
    });
    await run.action('Apply EXCLUDE to the existing Group', async () => {
      await editMissingPolicy('EXCLUDE'); await assertGroupRows({ candidate: true, expected: excludedGroups });
      await page.getByTestId('construction-apply-proposal').click(); await expect(proposal).toHaveCount(0); await assertMissingPolicySaved('EXCLUDE');
    });
    await run.action('Reload excluded Group keys and contributors', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); await assertMissingPolicySaved('EXCLUDE');
    });
    await run.action('Restore GROUP through the same row expression term', async () => {
      await editMissingPolicy('GROUP'); await assertGroupRows({ candidate: true });
      await page.getByTestId('construction-apply-proposal').click(); await expect(proposal).toHaveCount(0); await assertMissingPolicySaved('GROUP');
    });
    await run.action('Reload restored missing Group and contributors', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); await assertMissingPolicySaved('GROUP');
    });
    await page.getByTestId(`construction-history-step-${savedGroup.id}`).click();
    await expect(editor).toBeVisible();
    await expect(editor.getByRole('combobox', { name: 'Keep values 1', exact: true })).toHaveValue('ALL');
    await editor.getByTestId('construction-reshape-group-advanced').locator('summary').first().click();
    await editor.getByRole('textbox', { name: 'Summary output label 1', exact: true }).fill('Subject count');
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await assertGroupRows({ candidate: true });
    await run.action('Apply saved Group edit', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
      await assertGroupRows();
    });
    await run.action('Reload edited Group bindings', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId('construction-table-ResearchSubject').click();
      await assertGroupRows();
    });
    const editedGroup = documentFor(await run.state()).construction.steps.find(step => step.id === savedGroup.id);
    assert.deepEqual(editedGroup.operation, savedGroup.operation, 'Editing a label preserves Group key and summary identities');
    assert.deepEqual(editedGroup.rowValues, savedGroup.rowValues, 'Editing retains the exact contributor binding and ALL policy');
    assert.equal(editedGroup.outputs.find(output => output.id === editedGroup.operation.group.aggregates[0].outputColumnId).label, 'Subject count');
    await page.getByTestId(`construction-history-step-${savedGroup.id}`).click();
    await page.getByTestId(`construction-remove-step-${savedGroup.id}`).click();
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await run.action('Apply Group removal', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
    });
    await run.action('Reload Group removal and restore exact source values', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId('construction-table-ResearchSubject').click();
      assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows));
    });
    const ungrouped = documentFor(await run.state());
    assert(!(ungrouped.construction?.steps ?? []).some(step => step.id === savedGroup.id));
    assert.deepEqual(ungrouped.importedRecipe, originalDocument.importedRecipe);
    assert.deepEqual(ungrouped.columns, originalDocument.columns);
    const commands = run.report.requests.flatMap(request => request.commandTypes ?? []);
    assert(!commands.includes('UPDATE_IMPORTED_SOURCE_STEP'), 'Native source settings must save the reviewed receipt rather than bypassing Preview');
    assert.equal(commands.filter(command => command === 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL').length, 2);
  } catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; } finally { await run.finish(); }
});


test('GRAPH-ROWS ordinary document route survives add required removal and reload', async ({ page, request }, testInfo) => {
  const run = await setup(page, request, testInfo, { ordinary: true });
  try {
    const url = new URL(run.target.uiUrl);
    url.searchParams.set('project', run.target.fixtureProject);
    url.searchParams.set('explorer', run.explorerId);
    await page.goto(url.href, { waitUntil: 'domcontentloaded' });
    await run.action('Create ordinary ResearchSubject table', async () => {
      await page.getByRole('button', { name: 'Choose ResearchSubject rows', exact: true }).click();
      await expect(page.getByTestId('construction-preview')).toHaveAttribute('data-preview-status', 'ready');
    });
    const original = await run.state();
    const document = original.workspace.documents[0];
    assert.equal(document.rootResourceType, 'ResearchSubject');
    assert.equal(document.importedRecipe, undefined);
    assert.equal(document.columns.length, 1);
    assert.equal(document.columns[0].label, 'ResearchSubject ID');
    const idColumn = document.columns[0].column;
    const assertIDs = async expected => assert.deepEqual(sorted((await run.preview()).rows.map(row => row[idColumn])), sorted(expected));
    await assertIDs(['rs-zero', 'rs-one', 'rs-many', 'rs-shared']);
    await page.getByTestId('authored-traversal-editor').getByRole('button', { name: 'Explore available connections', exact: true }).click();
    await page.getByRole('checkbox', { name: 'Show full network', exact: true }).check();
    const studyNode = page.locator('.react-flow__node').filter({ hasText: 'ResearchStudy' });
    await studyNode.focus();
    await run.action('Add ordinary Study route', async () => {
      await studyNode.press('Enter');
      await expect((await studyInspector(page)).getByRole('combobox', { name: /Match policy for .*ResearchStudy/ })).toHaveValue('OPTIONAL');
      await assertIDs(['rs-zero', 'rs-one', 'rs-many', 'rs-shared']);
    });
    const added = await run.state();
    const occurrence = added.workspace.documents[0].route.children[0];
    assert.equal(occurrence.resourceType, 'ResearchStudy');
    assert(occurrence.catalogEdgeId);
    await run.action('Reload ordinary route', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect((await studyInspector(page)).getByRole('combobox', { name: /Match policy for .*ResearchStudy/ })).toHaveValue('OPTIONAL');
      assert.deepEqual((await run.state()).workspace.documents[0].route, added.workspace.documents[0].route);
    });
    await run.action('Require ordinary Study match', async () => {
      await changeStudyMatch(page, 'REQUIRED');
      await expect((await studyInspector(page)).getByRole('combobox', { name: /Match policy for .*ResearchStudy/ })).toHaveValue('REQUIRED');
      await assertIDs(['rs-one', 'rs-many', 'rs-shared']);
    });
    page.once('dialog', dialog => dialog.accept());
    await run.action('Remove ordinary Study route', async () => {
      await (await studyInspector(page)).getByRole('button', { name: /^Remove .* relationship$/ }).click();
      await expect(page.getByTestId('authored-traversal-editor').getByRole('navigation', { name: 'Authored occurrences', exact: true }).getByRole('button').filter({ hasText: 'ResearchStudy' })).toHaveCount(0);
    });
    await run.action('Reload removed ordinary route', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await assertIDs(['rs-zero', 'rs-one', 'rs-many', 'rs-shared']);
    });
    const removed = await run.state();
    assert.equal(removed.workspace.documents[0].route.children?.length ?? 0, 0);
    assert.deepEqual(removed.workspace.documents[0].columns, document.columns);
    assert(run.report.requests.flatMap(request => request.commandTypes ?? []).includes('ADD_ROUTE'), 'Ordinary graph must retain its existing authoritative command');
  } catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; } finally { await run.finish(); }
});


test('GRAPH-ROWS related row pairs preview cancel apply edit removal reload and anchor', async ({page,request},testInfo)=>{
  const run=await setup(page,request,testInfo);
  try { await runGraphRowsRelatedRows({run,page,request}); }
  catch(error){run.report.status='failed';run.report.error=error.message;throw error;}
  finally{await run.finish();}
});

test('GRAPH-ROWS 100 ordered operations remain usable with native keyboard and preview', async ({page,request},testInfo)=>{
  const run=await setup(page,request,testInfo,{ordinary:true,preparedHistory:true});
  try { await runGraphRowsHistoryStress({run,page,request}); }
  catch(error){run.report.status='failed';run.report.error=error.message;throw error;}
  finally{await run.finish();}
});


test('GRAPH-ROWS upstream source dependency rejects without altering Group and workspace', async ({ page, request }, testInfo) => {
  const run = await setup(page, request, testInfo);
  try { await runGraphRowsUpstreamDependency({ run, page }); }
  catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; }
  finally { await run.finish(); }
});

for (const mode of ['graph-first', 'row-first']) {
  test(`GRAPH-ROWS ${mode} complementary connection expression and graph lifecycle`, async ({ page, request }, testInfo) => {
    const run = await setup(page, request, testInfo);
    try { await runGraphRowsComplementaryConnection({ run, page, request, mode }); }
    catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; }
    finally { await run.finish(); }
  });
}

test('GRAPH-ROWS mixed existing list term and related pair lifecycle', async ({ page, request }, testInfo) => {
  const run = await setup(page, request, testInfo);
  try { await runGraphRowsMixed({ run, page, request }); }
  catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; }
  finally { await run.finish(); }
});

test('GRAPH-ROWS earlier row term preserves expansion and scalar downstream', async ({ page, request }, testInfo) => {
  const run = await setup(page, request, testInfo);
  try { await runGraphRowsMixed({ run, page, request, earlierTerm: true }); }
  catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; }
  finally { await run.finish(); }
});

test('GRAPH-ROWS ordinary column policy preview cancel apply edit reload', async ({ page, request }, testInfo) => {
  const run = await setup(page, request, testInfo, { ordinary: true });
  try { await runGraphRowsOrdinaryPolicy({ run, page }); }
  catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; }
  finally { await run.finish(); }
});

test('GRAPH-ROWS root proposal preserves imported boundary and previews explicit replacement', async ({ page, request }, testInfo) => {
  const run = await setup(page, request, testInfo);
  try { await runGraphRowsRootProposal({ run, page }); }
  catch (error) { run.report.status = 'failed'; run.report.error = error.message; throw error; }
  finally { await run.finish(); }
});
