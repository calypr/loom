import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import {
  readBasicRelatedFieldAfterUnpivotSeedManifest,
  validateBasicRelatedFieldAfterUnpivotRecords,
} from '../helpers/basic-related-field-after-unpivot-fixture-oracle.mjs';
import {
  loadBasicNullableRelatedFieldAfterUnpivotOracle,
  readBasicNullableRelatedFieldAfterUnpivotSeedManifest,
} from '../helpers/basic-nullable-related-field-after-unpivot-oracle.mjs';

const FIXTURE_MAX_BYTES = 4096;
const ACTION_TIMEOUT_MS = 5000;
const panelSelector = '[data-testid="construction-proposal-panel"], [data-testid="construction-choice-proposal-panel"]';

const readOneFixtureResource = (fixtureDir, resourceType) => {
  const path = join(fixtureDir, `${resourceType}.ndjson`);
  const sizeBytes = statSync(path).size;
  assert(sizeBytes > 0 && sizeBytes <= FIXTURE_MAX_BYTES,
    `${resourceType}.ndjson must be a bounded nonempty fixture (<= ${FIXTURE_MAX_BYTES} bytes)`);
  const source = readFileSync(path, 'utf8');
  const records = source.split(/\r?\n/).filter(line => line.trim().length > 0).map(line => JSON.parse(line));
  assert.equal(records.length, 1, `${resourceType}.ndjson must contain exactly one source row`);
  return {
    records,
    path,
    sizeBytes,
    sha256: createHash('sha256').update(source).digest('hex'),
  };
};

const waitFor = (page, predicate, argument, timeout = ACTION_TIMEOUT_MS) =>
  page.waitForFunction(({ predicateSource, value }) =>
    Boolean(Function('value', `return (${predicateSource})(value);`)(value)),
  { predicateSource: predicate.toString(), value: argument }, { timeout: Math.min(timeout, ACTION_TIMEOUT_MS) });

const normalizeVisible = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const expectedVisibleValue = value => {
  if (value === null || value === undefined) return '—';
  if (Array.isArray(value)) {
    const items = value.map(expectedVisibleValue).filter(item => item !== '—');
    return items.length > 0 ? items.join('; ') : '—';
  }
  return normalizeVisible(value);
};

const panelData = page => page.locator(panelSelector).evaluate(panel => ({
  testId: panel.dataset.testid,
  status: panel.dataset.proposalStatus,
  proposalId: panel.dataset.proposalId,
}));

const previewFromPayload = payload => payload?.preview ?? payload;

const assertTypedRows = (payload, expectedValues, label) => {
  const preview = previewFromPayload(payload);
  assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows),
    `${label}: proposal omitted typed preview columns or rows`);
  const previewStatus = preview.previewStatus ?? payload.previewStatus;
  if (previewStatus !== undefined) assert.equal(previewStatus, 'READY', `${label}: preview is not ready`);
  else assert(preview.receiptId, `${label}: saved preview receipt is missing`);
  assert.equal(preview.rowCount, expectedValues.length, `${label}: preview row count differs from the independent oracle`);
  assert.equal(preview.rows.length, expectedValues.length, `${label}: preview omitted typed rows`);
  const actualRows = preview.rows.map(row => preview.columns.map(column => row[column.column]));
  const expectedRows = expectedValues.map(row => [...row]);
  assert.deepEqual(actualRows.map(row => JSON.stringify(row)).sort(),
    expectedRows.map(row => JSON.stringify(row)).sort(),
  `${label}: typed preview values or multiplicity differ from the literal fixture oracle`);
  return preview;
};

export async function basicRelatedFieldAfterUnpivotWorkflow({ page, action, check, networkObserver, report, target, seed, genderMode = 'populated' }) {
  configureNativePage(page);
  assert.equal(report.scenario, 'basic-related-field-after-unpivot');
  assert.equal(seed?.fresh, true, 'This contract requires a fresh isolated Basic fixture project.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected a fresh loom_dev_verify project.');
  assert(target.fixtureGeneration && target.fixtureDir, 'The fixture project and generation must be explicit.');
  assert(['populated', 'missing'].includes(genderMode), `Unsupported Patient.gender fixture mode: ${genderMode}`);

  const fixtureSeedManifest = genderMode === 'missing'
    ? readBasicNullableRelatedFieldAfterUnpivotSeedManifest(target.fixtureDir)
    : readBasicRelatedFieldAfterUnpivotSeedManifest(target.fixtureDir);
  report.target.fixtureSeedManifest = fixtureSeedManifest;
  const expectedPatientCount = genderMode === 'missing' ? 2 : 1;
  const manifestCheck = genderMode === 'missing'
    ? 'Basic seed manifest contains one linked Patient, one unlinked schema witness, empty Observation, and one Specimen'
    : 'Basic seed manifest contains one Patient, empty Observation, and one Specimen NDJSON file';
  check('correctness', manifestCheck,
    fixtureSeedManifest.map(file => file.name).join(',') === 'Observation.ndjson,Patient.ndjson,Specimen.ndjson' &&
      fixtureSeedManifest[0].sizeBytes === 0 && fixtureSeedManifest[0].recordCount === 0 &&
      fixtureSeedManifest[1].recordCount === expectedPatientCount && fixtureSeedManifest[2].recordCount === 1,
    fixtureSeedManifest);

  const specimenFile = readOneFixtureResource(target.fixtureDir, 'Specimen');
  const oracle = genderMode === 'missing'
    ? loadBasicNullableRelatedFieldAfterUnpivotOracle(target.fixtureDir)
    : validateBasicRelatedFieldAfterUnpivotRecords({
      patients: readOneFixtureResource(target.fixtureDir, 'Patient').records,
      specimens: specimenFile.records,
    });
  report.target.fixtureOracle = {
    kind: 'Basic fixture only; does not close the loaded CDA CASE-042 witness gap',
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sourceFiles: fixtureSeedManifest,
    patient: oracle.patient,
    ...(oracle.schemaWitnessPatient ? { schemaWitnessPatient: oracle.schemaWitnessPatient } : {}),
    specimen: oracle.specimen,
    literalExpectedPostUnpivotRows: oracle.rows,
  };
  const oracleMatches = genderMode === 'missing'
    ? oracle.patient.id === 'basic-nullable-gender-patient-001' && oracle.patient.gender === null &&
      oracle.patient.genderPropertyPresent === false && oracle.schemaWitnessPatient?.gender === 'female' &&
      oracle.specimen.subjectReference === `Patient/${oracle.patient.id}` &&
      oracle.rows.length === 1 && JSON.stringify(oracle.rows[0].patientGenderAll) === '[null]'
    : oracle.patient.id === 'basic-gender-patient-001' && oracle.patient.gender === 'female' &&
      oracle.specimen.subjectReference === `Patient/${oracle.patient.id}` && oracle.rows.length === 1;
  const oracleCheck = genderMode === 'missing'
    ? 'independent Basic fixture oracle identifies a linked Patient with missing gender and exact ALL [null] row'
    : 'independent Basic fixture oracle identifies one linked populated-gender Patient and exact Specimen row';
  check('correctness', oracleCheck, oracleMatches, report.target.fixtureOracle);

  const pageAction = (label, locator, perform, after) => action(label, locator, perform, {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    ...(after ? { after } : {}),
  });
  const click = (label, locator, after) => pageAction(label, locator,
    () => locator.click({ timeout: ACTION_TIMEOUT_MS }), after);
  const fill = (label, locator, value, after) => pageAction(label, locator,
    () => locator.fill(value, { timeout: ACTION_TIMEOUT_MS }), after);
  const select = (label, locator, value, after) => pageAction(label, locator,
    () => locator.selectOption(value, { timeout: ACTION_TIMEOUT_MS }), after);
  const waitVisible = (locator, timeout = ACTION_TIMEOUT_MS) => locator.waitFor({ state: 'visible', timeout });
  const waitHidden = (locator, timeout = ACTION_TIMEOUT_MS) => locator.waitFor({ state: 'hidden', timeout });

  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  await waitFor(page, () => document.body.innerText.includes('Build your first table') ||
    document.body.innerText.includes('Dataset graph'), undefined);
  const newExplorerSummary = page.locator('summary').filter({ hasText: /^New explorer$/ });
  await click('open fresh Explorer creation', newExplorerSummary,
    () => waitVisible(page.locator('#new-explorer-name')));
  const explorerTitle = `Verify ${report.target.project.slice(-10)} Basic related field after Unpivot`;
  await fill('name fresh Basic related-field Explorer', page.locator('#new-explorer-name'), explorerTitle);
  await click('create fresh Basic related-field Explorer', page.getByRole('button', { name: 'Create blank', exact: true }),
    () => waitFor(page, title => document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === title &&
      document.body.innerText.includes('Build your first table'), explorerTitle));
  const explorer = await page.locator('select[aria-label="Explorer"]').inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The workflow must use a fresh non-bootstrap Explorer.');
  report.target.explorer = explorer;

  const firstTableName = 'Specimen related-field reshape';
  await fill('name Specimen root table', page.locator('#first-table-name'), firstTableName);
  await click('create native Specimen root table', page.getByRole('button', { name: 'Choose Specimen rows', exact: true }),
    () => waitFor(page, title => document.body.innerText.includes('DATASET WORKSPACE') &&
      document.body.innerText.includes(title) && !document.body.innerText.includes('Build your first table'), firstTableName));

  const project = target.fixtureProject;
  const base = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
  report.target.verificationReads = [];
  const readJSON = async (path, body) => {
    const response = await fetch(`${target.apiUrl}${base}${path}`, {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    report.target.verificationReads.push({ method: body === undefined ? 'GET' : 'POST', path, status: response.status });
    assert(response.ok, `Read-only Builder verification ${path} failed with HTTP ${response.status}: ${JSON.stringify(value).slice(0, 800)}`);
    return value;
  };
  const readBuilder = () => readJSON('/builder');
  let builder = await readBuilder();
  assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog must use the fixture generation.');
  let document = builder.workspace.documents.find(item => item.rootResourceType === 'Specimen');
  assert(document?.output?.id, 'The native first table must have Specimen as its row root.');
  const outputId = document.output.id;
  report.target.outputId = outputId;

  const documentFor = state => state.workspace.documents.find(item => item.output.id === outputId);
  const readStep = (state, kind, predicate = () => true) =>
    (documentFor(state)?.construction?.steps ?? []).findLast(step => step.operation.kind === kind && predicate(step));
  const relatedStepFor = (state, fieldPath) => readStep(state, 'RELATED_SOURCE', step => {
    const path = step.operation.relatedSource?.source?.path ?? '';
    return path === fieldPath || path.endsWith(`.${fieldPath}`);
  });
  const checkBinding = (state, fieldPath, expectedLabel) => {
    const step = relatedStepFor(state, fieldPath);
    assert(step, `Saved RELATED_SOURCE for Patient.${fieldPath} is missing.`);
    const related = step.operation.relatedSource;
    assert.equal(related.source.resourceType, 'Patient');
    assert(related.source.path === fieldPath || related.source.path.endsWith(`.${fieldPath}`),
      `Unexpected saved Patient source path: ${related.source.path}`);
    assert.equal(related.form, 'ALL');
    assert.equal(related.route.length, 1);
    assert.equal(related.route[0].fromResourceType, 'Specimen');
    assert.equal(related.route[0].toResourceType, 'Patient');
    assert.equal(related.route[0].relationship, 'subject_Patient');
    assert.equal(related.route[0].storageDirection, 'OUTBOUND');
    const output = step.outputs.find(candidate => candidate.id === related.outputColumnId);
    assert(output, `Patient.${fieldPath} operation lost its output column.`);
    if (expectedLabel) assert.equal(output.label, expectedLabel);
    return { step, output };
  };
  const assertVisiblePreviewRows = async (label, expectedValues) => {
    await waitFor(page, output => {
      const node = document.querySelector('[data-testid="construction-preview"]');
      return node?.dataset.previewStatus === 'ready' && node.dataset.previewOutputId === output &&
        Boolean(node.dataset.previewReceiptId);
    }, outputId);
    const visible = await page.locator('[data-testid="preview-table-scroll"] [role="table"]').evaluate(table => ({
      rowCount: table.getAttribute('aria-rowcount'),
      headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => String(cell.innerText ?? '').replace(/\s+/g, ' ').trim()),
      rows: [...table.querySelectorAll('[role="row"]')].slice(1)
        .filter(row => row.querySelector('[role="cell"]'))
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => String(cell.innerText ?? '').replace(/\s+/g, ' ').trim())),
    }));
    const expectedRows = expectedValues.map(row => row.map(expectedVisibleValue));
    assert.deepEqual(visible.rows, expectedRows,
      `${label}: visible Builder cell values and row multiplicity differ from the literal fixture oracle`);
    assert.equal(visible.headers.length, expectedRows[0]?.length ?? 0,
      `${label}: visible Builder columns differ from the literal fixture row width`);
    return visible;
  };
  const readReceiptPreview = async (label, expectedValues) => {
    const previewNode = page.getByTestId('construction-preview');
    await waitFor(page, output => {
      const node = document.querySelector('[data-testid="construction-preview"]');
      return node?.dataset.previewStatus === 'ready' && node.dataset.previewOutputId === output &&
        Boolean(node.dataset.previewReceiptId);
    }, outputId);
    const receiptId = await previewNode.getAttribute('data-preview-receipt-id');
    assert(receiptId, `${label}: saved Preview receipt is missing after reload.`);
    const preview = await readJSON('/preview', { receiptId, outputId, limit: 100 });
    assert.equal(preview.receiptId, receiptId);
    assert.equal(preview.outputId, outputId);
    assertTypedRows(preview, expectedValues, label);
    const visible = await assertVisiblePreviewRows(label, expectedValues);
    assert.equal(visible.headers.length, preview.columns.length, `${label}: visible output columns differ from the typed receipt.`);
    report.target.verificationReads.at(-1).visible = {
      rowCount: visible.rows.length,
      headers: visible.headers,
      rows: visible.rows,
    };
    return { builder: await readBuilder(), preview, visible };
  };
  const reloadAndRead = async (label, expectedValues) => {
    const tableLocator = page.getByTestId(`construction-table-${outputId}`);
    const savedDraft = { digest: builder.draftDigest, version: String(builder.draftVersion) };
    assert(savedDraft.digest && savedDraft.version, `${label}: saved draft identity is required before reload.`);
    await pageAction(`reload Builder for ${label}`, tableLocator, async () => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
      await tableLocator.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      if (await tableLocator.getAttribute('aria-current') !== 'page') {
        await tableLocator.click({ timeout: ACTION_TIMEOUT_MS });
      }
    }, async () => {
      await waitFor(page, expected => {
        const selected = document.querySelector(`[data-testid="construction-table-${expected.outputId}"][aria-current="page"]`);
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return Boolean(selected && preview?.dataset.previewStatus === 'ready' &&
          preview.dataset.previewOutputId === expected.outputId &&
          preview.dataset.currentDraftDigest === expected.digest &&
          preview.dataset.currentDraftVersion === expected.version &&
          preview.dataset.previewReceiptId);
      }, { outputId, ...savedDraft });
      await assertVisiblePreviewRows(label, expectedValues);
    });
    return readReceiptPreview(label, expectedValues);
  };
  const openAddColumns = async label => {
    const editor = page.locator('[aria-label="Add columns editor"]');
    if (!await editor.isVisible()) {
      await waitFor(page, () => {
        const button = document.querySelector('[data-testid="construction-action-add-columns"]');
        return Boolean(button && !button.disabled);
      }, undefined);
      await click(`${label}: open Add columns`, page.getByTestId('construction-action-add-columns'),
        () => waitVisible(editor));
    }
    assert.equal(await editor.count(), 1, 'Exactly one native Add columns editor must be open.');
    const familyButton = page.locator('[aria-label="Column types"]')
      .getByRole('button', { name: 'Fields and related data', exact: true });
    assert.equal(await familyButton.count(), 1, 'The native Fields and related data family control must be available.');
    if (await familyButton.getAttribute('aria-pressed') !== 'true') {
      await click(`${label}: choose Fields and related data`, familyButton,
        () => waitVisible(page.getByTestId('construction-add-columns-source')));
    } else {
      await waitVisible(page.getByTestId('construction-add-columns-source'));
    }
  };
  const openRawFields = async () => {
    const details = page.getByTestId('feature-catalog-raw-fields');
    assert.equal(await details.count(), 1, 'The active feature catalog must expose the raw FHIR fields section.');
    if (!await details.evaluate(element => element.open)) {
      await click('open raw FHIR fields', details.locator(':scope > summary'),
        () => waitFor(page, () => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true, undefined));
    }
  };
  const waitPanelReady = async () => waitFor(page, () => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]') ??
      document.querySelector('[data-testid="construction-choice-proposal-panel"]');
    return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  }, undefined);
  const awaitProposal = async (fromIndex, label) => {
    await waitPanelReady();
    const panel = await panelData(page);
    assert.equal(panel.status, 'ready', `${label}: ${panel.testId} status ${panel.status}`);
    const entry = await networkObserver.waitForResponse(candidate =>
      candidate.index >= fromIndex && candidate.status !== undefined && candidate.response &&
      (candidate.path.endsWith('/construction-proposals') || candidate.path.endsWith('/construction-choice-proposals')) &&
      (!panel.proposalId || candidate.response.proposalId === panel.proposalId || candidate.response.commandId === panel.proposalId),
    { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    assert.equal(entry.status, 200, `${label}: native proposal must return HTTP 200.`);
    report.target.verificationReads.push({ method: entry.method, path: entry.path, status: entry.status, browserRequestId: entry.browserRequestId });
    return entry.response;
  };
  const cancelProposal = async (label, savedWorkspace) => {
    const panel = await panelData(page);
    const cancel = panel.testId === 'construction-choice-proposal-panel'
      ? page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Cancel', exact: true })
      : page.getByTestId('construction-cancel-proposal');
    await click(`${label}: Cancel proposal`, cancel,
      () => waitHidden(page.locator(panelSelector)));
    builder = await readBuilder();
    assert.deepEqual(builder.workspace, savedWorkspace, `${label}: Cancel must preserve the exact saved workspace.`);
    return builder;
  };
  const applyProposal = async (label, expectedValues) => {
    const panel = await panelData(page);
    const previousDraft = { digest: builder.draftDigest, version: Number(builder.draftVersion) };
    assert(previousDraft.digest && Number.isFinite(previousDraft.version), `${label}: saved draft identity is required before Apply.`);
    const apply = panel.testId === 'construction-choice-proposal-panel'
      ? page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Apply columns', exact: true })
      : page.getByTestId('construction-apply-proposal');
    await click(`${label}: Apply proposal`, apply, async () => {
      await waitFor(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]') &&
        !document.querySelector('[data-testid="construction-choice-proposal-panel"]'), undefined);
      await waitFor(page, expected => {
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expected.outputId &&
          preview.dataset.previewReceiptId &&
          (preview.dataset.currentDraftDigest !== expected.digest || Number(preview.dataset.currentDraftVersion) > expected.version);
      }, { outputId, ...previousDraft });
      await assertVisiblePreviewRows(label, expectedValues);
    });
    builder = await readBuilder();
    const receipt = await readReceiptPreview(label, expectedValues);
    builder = receipt.builder;
    return { builder, preview: receipt.preview };
  };
  const startProposal = () => networkObserver.requests.length;
  const selectRawFeature = async (fieldPath, label) => {
    const checkbox = page.getByRole('checkbox', { name: `Select Specimen.${fieldPath}`, exact: true });
    await waitVisible(checkbox);
    assert.equal(await checkbox.count(), 1, `Expected exactly one Specimen.${fieldPath} raw field candidate.`);
    await pageAction(label, checkbox, () => checkbox.check({ timeout: ACTION_TIMEOUT_MS }),
      () => waitFor(page, path => document.querySelector(`input[aria-label=${JSON.stringify(`Select Specimen.${path}`)}]`)?.checked === true, fieldPath));
  };
  const addRootFields = async () => {
    const initialRootID = document.columns.filter(column =>
      (column.source?.field?.path ?? column.source?.path ?? '') === 'id');
    assert.equal(initialRootID.length, 1,
      `Native Specimen root creation must provide exactly one direct ID column: ${JSON.stringify(initialRootID)}`);
    await openAddColumns('Add Specimen status');
    await openRawFields();
    await selectRawFeature('status', 'select native Specimen.status');
    const add = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    const fromIndex = startProposal();
    await click('propose native Specimen.status', add,
      () => waitVisible(page.getByTestId('construction-choice-proposal-panel')));
    const payload = await awaitProposal(fromIndex, 'Add Specimen status');
    assertTypedRows(payload, [[oracle.specimen.id, oracle.specimen.status]], 'initial root fields Preview');
    const savedWorkspace = structuredClone(builder.workspace);
    await applyProposal('apply exact Specimen status root field', [[oracle.specimen.id, oracle.specimen.status]]);
    builder = await readBuilder();
    document = documentFor(builder);
    const rootFieldPaths = document.columns.map(column => column.source?.field?.path ?? column.source?.path ?? '');
    assert(rootFieldPaths.includes('id') && rootFieldPaths.includes('status'),
      `Native root fields must retain Specimen.id and Specimen.status: ${JSON.stringify(rootFieldPaths)}`);
    check('correctness', 'native Specimen.id and Specimen.status fields preview, apply, and reload as the exact independent fixture row',
      true, { paths: rootFieldPaths, row: [oracle.specimen.id, oracle.specimen.status], workspaceCreated: Boolean(savedWorkspace.documents.length) });
  };
  const openRelatedField = async (fieldPath, label) => {
    await openAddColumns(`Patient.${fieldPath} ${label}`);
    const related = page.locator('[aria-label="Related resources"]');
    await waitVisible(related);
    if (!await related.evaluate(element => element.open)) {
      await click(`open Patient related resources for ${fieldPath}`, related.locator(':scope > summary'),
        () => waitFor(page, () => document.querySelector('[aria-label="Related resources"]')?.open === true, undefined));
    }
    const sourceOptions = await page.getByTestId('construction-add-columns-source-option').evaluateAll(options => options
      .map(option => ({ label: option.getAttribute('aria-label') ?? '', disabled: option.disabled }))
      .filter(option => option.label.includes('Patient') && option.label.includes('Related resource')));
    assert.equal(sourceOptions.length, 1, `Expected one direct related Patient source: ${JSON.stringify(sourceOptions)}`);
    assert.equal(sourceOptions[0].disabled, false);
    const option = page.locator(`[data-testid="construction-add-columns-source-option"][aria-label=${JSON.stringify(sourceOptions[0].label)}]`);
    await click(`select related Patient source for ${fieldPath}`, option,
      () => waitFor(page, () => document.querySelector('[data-testid="construction-add-columns-source"]')?.innerText.includes('Patient'), undefined));
    await openRawFields();
    const field = page.getByRole('checkbox', { name: `Select Patient.${fieldPath}`, exact: true });
    await waitVisible(field);
    assert.equal(await field.count(), 1, `Expected one raw Patient.${fieldPath} candidate.`);
    await pageAction(`select related Patient.${fieldPath}`, field,
      () => field.check({ timeout: ACTION_TIMEOUT_MS }),
      () => waitFor(page, path => document.querySelector(`input[aria-label=${JSON.stringify(`Select Patient.${path}`)}]`)?.checked === true, fieldPath));
    const addSelected = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    await click(`add Patient.${fieldPath} selected feature`, addSelected,
      () => waitVisible(page.getByRole('dialog')));
    const routeRadios = page.getByRole('dialog').locator('input[type="radio"]');
    const routeLabels = await routeRadios.evaluateAll(radios => radios.map(radio => radio.getAttribute('aria-label') ?? ''));
    const routeLabel = routeLabels.find(value => value.includes('Specimen -[subject]-> Patient'));
    assert(routeLabel, `Exact Specimen.subject → Patient route is missing: ${JSON.stringify(routeLabels)}`);
    await click(`choose Specimen.subject to Patient for ${fieldPath}`,
      page.getByRole('dialog').locator(`input[type="radio"][aria-label=${JSON.stringify(routeLabel)}]`),
      () => waitFor(page, () => [...document.querySelectorAll('[role="dialog"] input[type="radio"]')]
        .some(radio => (radio.getAttribute('aria-label') ?? '').includes('Keep all matching values')), undefined));
    const allLabel = 'Keep all matching values';
    const formLabel = (await page.getByRole('dialog').locator('input[type="radio"]')
      .evaluateAll(radios => radios.map(radio => radio.getAttribute('aria-label') ?? '')))
      .find(value => value.includes(allLabel));
    assert(formLabel, `Patient.${fieldPath} must offer the native ALL form.`);
    await click(`choose Patient.${fieldPath} ALL`,
      page.getByRole('dialog').locator(`input[type="radio"][aria-label=${JSON.stringify(formLabel)}]`));
    const groupedPolicy = page.getByRole('dialog').getByRole('combobox', { name: 'Values per grouped row', exact: true });
    if (await groupedPolicy.count() === 1 && await groupedPolicy.isVisible()) {
      await select(`set Patient.${fieldPath} grouped value policy to ALL`, groupedPolicy, 'ALL');
    }
    const fromIndex = startProposal();
    await click(`propose Patient.${fieldPath} ALL`, page.getByRole('dialog').getByRole('button', { name: 'Add 1 column', exact: true }),
      () => waitVisible(page.locator(panelSelector)));
    return { payload: await awaitProposal(fromIndex, `Patient.${fieldPath} ALL ${label}`), routeLabel, formLabel };
  };
  const beginUnpivot = async fieldLabel => {
    await click(`open row settings before Unpivot ${fieldLabel}`, page.getByTestId('construction-rows-settings-trigger'),
      () => waitVisible(page.getByTestId('construction-action-unpivot-rows')));
    await click(`open native Unpivot field chooser for ${fieldLabel}`, page.getByTestId('construction-action-unpivot-rows'),
      () => waitVisible(page.locator(`input[aria-label=${JSON.stringify(`Unpivot ${fieldLabel}`)}]`)));
    const source = page.locator(`input[aria-label=${JSON.stringify(`Unpivot ${fieldLabel}`)}]`);
    const fromIndex = startProposal();
    await pageAction(`choose native Unpivot source ${fieldLabel}`, source,
      () => source.check({ timeout: ACTION_TIMEOUT_MS }),
      () => waitVisible(page.locator(panelSelector)));
    return await awaitProposal(fromIndex, `Unpivot ${fieldLabel}`);
  };
  const removeStep = async (step, label) => {
    await click(`select saved ${label} history step`, page.getByTestId(`construction-history-step-${step.id}`));
    const fromIndex = startProposal();
    await click(`propose removal of ${label}`, page.getByTestId(`construction-remove-step-${step.id}`),
      () => waitVisible(page.locator(panelSelector)));
    return await awaitProposal(fromIndex, `Remove ${label}`);
  };
  const editRelatedLabel = async (step, nextLabel) => {
    await click('select saved Patient.gender history step for edit', page.getByTestId(`construction-history-step-${step.id}`));
    await click('open saved Patient.gender editor', page.getByTestId(`construction-edit-step-${step.id}`),
      () => waitVisible(page.locator('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]')));
    const input = page.locator('[data-testid="related-source-step-editor"] input[aria-label="Output column label"]');
    const fromIndex = startProposal();
    await fill(`edit Patient.gender output label to ${nextLabel}`, input, nextLabel,
      () => waitVisible(page.locator(panelSelector)));
    return await awaitProposal(fromIndex, `Edit Patient.gender label to ${nextLabel}`);
  };

  await addRootFields();
  const beforeRelatedID = await readBuilder();
  const idProposal = await openRelatedField('id', 'before Unpivot');
  assertTypedRows(idProposal.payload, [[oracle.specimen.id, oracle.specimen.status, [oracle.patient.id]]],
    'pre-Unpivot Patient.id ALL Preview');
  check('correctness', 'pre-Unpivot Patient.id ALL choice uses the exact Specimen.subject to Patient route',
    idProposal.routeLabel.includes('Specimen -[subject]-> Patient') && idProposal.formLabel.includes('Keep all matching values'),
    { route: idProposal.routeLabel, form: idProposal.formLabel });
  check('correctness', 'pre-Unpivot Patient.id ALL preview returns exactly the linked fixture Patient ID', true,
    { route: idProposal.routeLabel, form: idProposal.formLabel, patientIDs: [oracle.patient.id] });
  await cancelProposal('cancel pre-Unpivot Patient.id ALL', beforeRelatedID.workspace);
  check('correctness', 'Cancel preserves the exact Specimen root workspace before Patient.id ALL Apply', true,
    { draftVersion: builder.draftVersion, workspacePreserved: true });

  const confirmedID = await openRelatedField('id', 'before Unpivot Apply');
  assertTypedRows(confirmedID.payload, [[oracle.specimen.id, oracle.specimen.status, [oracle.patient.id]]],
    'confirmed pre-Unpivot Patient.id ALL Preview');
  await applyProposal('apply pre-Unpivot Patient.id ALL', [[oracle.specimen.id, oracle.specimen.status, [oracle.patient.id]]]);
  builder = await readBuilder();
  const savedPatientID = checkBinding(builder, 'id');
  const savedIDLabel = savedPatientID.output.label;
  check('correctness', 'Apply saves Patient.id ALL on the exact Specimen.subject to Patient route', true,
    { label: savedIDLabel, stepId: savedPatientID.step.id, route: savedPatientID.step.operation.relatedSource.route });
  const idReload = await reloadAndRead('reloaded pre-Unpivot Patient.id ALL',
    [[oracle.specimen.id, oracle.specimen.status, [oracle.patient.id]]]);
  builder = idReload.builder;
  checkBinding(builder, 'id', savedIDLabel);
  check('persistence', 'pre-Unpivot Patient.id ALL exact array survives Builder reload', true,
    { patientIDs: [oracle.patient.id], rows: idReload.preview.rows });

  const beforeUnpivot = await readBuilder();
  const specimenIDSourceColumnID = documentFor(beforeUnpivot).columns
    .find(column => column.label === 'Specimen ID')?.columnId;
  assert(specimenIDSourceColumnID, 'Saved Specimen.id output must have a stable column identity before Unpivot.');
  const unpivotProposal = await beginUnpivot('Specimen ID');
  assertTypedRows(unpivotProposal, [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]],
    'Unpivot with retained Patient.id ALL Preview');
  check('correctness', 'Unpivot Preview preserves Specimen status and Patient.id while producing the exact key/value pair', true,
    { status: oracle.specimen.status, patientIDs: [oracle.patient.id], key: 'Specimen ID', value: oracle.specimen.id });
  await cancelProposal('cancel Unpivot with retained Patient.id ALL', beforeUnpivot.workspace);
  check('correctness', 'Cancel preserves exact pre-Unpivot Patient.id ALL state and rows', true,
    { draftVersion: builder.draftVersion, workspacePreserved: true });

  const confirmedUnpivot = await beginUnpivot('Specimen ID');
  assertTypedRows(confirmedUnpivot, [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]],
    'confirmed Unpivot Preview');
  await applyProposal('apply Unpivot retaining Patient.id ALL',
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]]);
  builder = await readBuilder();
  const savedUnpivot = readStep(builder, 'UNPIVOT');
  const savedIDAfterUnpivot = checkBinding(builder, 'id', savedIDLabel);
  assert(savedUnpivot, 'Saved construction omitted Unpivot.');
  assert.deepEqual(savedUnpivot.operation.unpivot.inputs.map(input => input.columnId),
    [specimenIDSourceColumnID],
  'Unpivot must consume the exact Specimen.id source column.');
  check('correctness', 'saved Unpivot retains the exact Patient.id ALL binding and status scalar', true,
    { unpivotStepId: savedUnpivot.id, patientIdStepId: savedIDAfterUnpivot.step.id });
  const unpivotReload = await reloadAndRead('reloaded Unpivot with retained Patient.id ALL',
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]]);
  builder = unpivotReload.builder;
  assert(readStep(builder, 'UNPIVOT'));
  checkBinding(builder, 'id', savedIDLabel);
  check('persistence', 'Unpivot key/value rows and retained Patient.id ALL survive Builder reload', true,
    { rows: unpivotReload.preview.rows });

  const beforeGender = await readBuilder();
  const genderProposal = await openRelatedField('gender', 'after Unpivot');
  assertTypedRows(genderProposal.payload,
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id, [oracle.patient.gender]]],
    'post-Unpivot Patient.gender ALL Preview');
  check('correctness', genderMode === 'missing'
    ? 'post-Unpivot Patient.gender ALL preserves the absent source scalar as exact [null] on the same route'
    : 'post-Unpivot Patient.gender ALL remains on the same exact Patient route and previews the literal gender', true,
    { route: genderProposal.routeLabel, form: genderProposal.formLabel, gender: oracle.patient.gender });
  await cancelProposal('cancel post-Unpivot Patient.gender ALL', beforeGender.workspace);
  check('correctness', 'Cancel preserves exact saved Unpivot rows before Patient.gender ALL Apply', true,
    { draftVersion: builder.draftVersion, workspacePreserved: true });

  const confirmedGender = await openRelatedField('gender', 'after Unpivot Apply');
  assertTypedRows(confirmedGender.payload,
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id, [oracle.patient.gender]]],
    'confirmed post-Unpivot Patient.gender ALL Preview');
  await applyProposal('apply post-Unpivot Patient.gender ALL',
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id, [oracle.patient.gender]]]);
  builder = await readBuilder();
  const savedGender = checkBinding(builder, 'gender');
  const originalGenderLabel = savedGender.output.label;
  check('correctness', genderMode === 'missing'
    ? 'Apply saves Patient.gender ALL after Unpivot as exact [null] for the absent source scalar'
    : 'Apply saves Patient.gender ALL after Unpivot with exact source value', true,
    { label: originalGenderLabel, stepId: savedGender.step.id, gender: oracle.patient.gender });
  const genderReload = await reloadAndRead('reloaded post-Unpivot Patient.gender ALL',
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id, [oracle.patient.gender]]]);
  builder = genderReload.builder;
  checkBinding(builder, 'gender', originalGenderLabel);
  check('persistence', genderMode === 'missing'
    ? 'post-Unpivot Patient.gender ALL exact [null] member survives Builder reload'
    : 'post-Unpivot Patient.gender ALL array survives Builder reload', true,
    { rows: genderReload.preview.rows });

  const beforeEdit = await readBuilder();
  const editCancelled = await editRelatedLabel(savedGender.step, 'Patient Gender edited');
  assertTypedRows(editCancelled, [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id, [oracle.patient.gender]]],
    'edited Patient.gender Cancel Preview');
  await cancelProposal('cancel Patient.gender label edit', beforeEdit.workspace);
  check('correctness', 'Cancel preserves saved Patient.gender ALL binding and values after label edit', true,
    { label: originalGenderLabel, gender: oracle.patient.gender });

  const beforeEditApply = await readBuilder();
  const editApplied = await editRelatedLabel(savedGender.step, 'Patient Gender edited');
  assertTypedRows(editApplied, [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id, [oracle.patient.gender]]],
    'edited Patient.gender Apply Preview');
  await applyProposal('apply Patient.gender label edit',
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id, [oracle.patient.gender]]]);
  builder = await readBuilder();
  const editedGender = checkBinding(builder, 'gender', 'Patient Gender edited');
  assert.equal(editedGender.step.id, savedGender.step.id, 'Editing the display label must retain the saved step identity.');
  const editedReload = await reloadAndRead('reloaded edited Patient.gender ALL',
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id, [oracle.patient.gender]]]);
  builder = editedReload.builder;
  checkBinding(builder, 'gender', 'Patient Gender edited');
  check('persistence', 'edited Patient.gender label and exact ALL value survive Apply and reload', true,
    { label: 'Patient Gender edited', gender: oracle.patient.gender, rows: editedReload.preview.rows,
      priorDraftDigest: beforeEditApply.draftDigest });

  const beforeGenderRemoval = await readBuilder();
  const removeGenderProposal = await removeStep(editedGender.step, 'Patient.gender ALL');
  assertTypedRows(removeGenderProposal, [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]],
    'Patient.gender removal Preview');
  await cancelProposal('cancel Patient.gender ALL removal', beforeGenderRemoval.workspace);
  check('correctness', 'Cancel preserves edited Patient.gender ALL after removal Preview', true,
    { stepId: editedGender.step.id, label: 'Patient Gender edited' });

  const removeGenderApply = await removeStep(editedGender.step, 'Patient.gender ALL');
  assertTypedRows(removeGenderApply, [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]],
    'confirmed Patient.gender removal Preview');
  await applyProposal('remove Patient.gender ALL', [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]]);
  builder = await readBuilder();
  assert.equal(relatedStepFor(builder, 'gender'), undefined, 'Removing Patient.gender must remove its saved step.');
  assert(readStep(builder, 'UNPIVOT'), 'Removing Patient.gender must retain Unpivot.');
  const genderRemovalReload = await reloadAndRead('reloaded Unpivot after Patient.gender removal',
    [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]]);
  builder = genderRemovalReload.builder;
  assert.equal(relatedStepFor(builder, 'gender'), undefined);
  check('correctness', 'removing Patient.gender and reloading restores the exact Unpivot-only rows and Patient.id binding', true,
    { rows: genderRemovalReload.preview.rows, patientIDStepId: checkBinding(builder, 'id', savedIDLabel).step.id });

  const beforeUnpivotRemoval = await readBuilder();
  const removeUnpivotProposal = await removeStep(readStep(builder, 'UNPIVOT'), 'Unpivot');
  assertTypedRows(removeUnpivotProposal, [[oracle.specimen.id, oracle.specimen.status, [oracle.patient.id]]],
    'Unpivot removal Preview');
  await cancelProposal('cancel Unpivot removal', beforeUnpivotRemoval.workspace);
  check('correctness', 'Cancel preserves saved Unpivot rows after Unpivot removal Preview', true,
    { workspacePreserved: true, rows: (await readReceiptPreview('Unpivot after removal Cancel',
      [[oracle.specimen.status, [oracle.patient.id], 'Specimen ID', oracle.specimen.id]])).preview.rows });

  const removeUnpivotApply = await removeStep(readStep(builder, 'UNPIVOT'), 'Unpivot');
  assertTypedRows(removeUnpivotApply, [[oracle.specimen.id, oracle.specimen.status, [oracle.patient.id]]],
    'confirmed Unpivot removal Preview');
  await applyProposal('remove Unpivot and restore Patient.id Related state',
    [[oracle.specimen.id, oracle.specimen.status, [oracle.patient.id]]]);
  builder = await readBuilder();
  assert.equal(readStep(builder, 'UNPIVOT'), undefined, 'Removing Unpivot must restore the pre-Unpivot construction.');
  checkBinding(builder, 'id', savedIDLabel);
  const restoredReload = await reloadAndRead('restored pre-Unpivot Patient.id ALL',
    [[oracle.specimen.id, oracle.specimen.status, [oracle.patient.id]]]);
  builder = restoredReload.builder;
  assert.equal(readStep(builder, 'UNPIVOT'), undefined);
  checkBinding(builder, 'id', savedIDLabel);
  check('persistence', 'removing Unpivot and reloading restores exact Specimen row, status, and Patient.id ALL values', true,
    { rows: restoredReload.preview.rows });

  const unexpected = networkObserver.failures.map(failure => ({
    kind: failure.kind,
    path: failure.entry?.path,
    status: failure.entry?.status ?? failure.status,
    message: failure.message ?? failure.errorText ?? failure.entry?.failure?.errorText,
  }));
  const actionFailures = report.actions.filter(item => item.status !== 'passed' || item.elapsedMs > ACTION_TIMEOUT_MS);
  check('performance', 'All native action and action-to-render checkpoints complete within five seconds',
    actionFailures.length === 0, { actionCount: report.actions.length, actionFailures });
  check('correctness', 'No unexpected native HTTP or network errors occurred', unexpected.length === 0, unexpected);
}
