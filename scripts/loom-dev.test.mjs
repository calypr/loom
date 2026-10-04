import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, utimesSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { addColumnsActionReadinessCondition, AUTHORING_SEMANTICS_VERSION, authoringCommandSemanticsVersion, assertExternalJ01SourcesUnchanged, assertJ05ArtifactIdentity, assertJ05ArtifactRows, bootstrapSeedPlan, bootstrapWorkspaceNeedsSeed, builderDOMReadyCondition, builderDraftMatchesPreviewDOM, builderPreviewIsFreshForDraft, canonicalProjectID, collectJ01SemanticConceptPages, commandEnvironment, compareJ04Evidence, createDevSession, createVerificationReport, expectedFixtureRelatedValue, explicitGroupPreviewRows, externalJ01PatientScalar, fixtureSourceDigest, generatedJ01ConceptNDJSON, generationLoadDisposition, graphQLRowsRequest, inspectJ01ArtifactRows, inspectJ05ArtifactPackage, j01ArtifactDownloadPlan, j01ColumnIdentitySnapshot, j01ConstructionChoiceCommandIdentities, j01JSONValuesEquivalent, j01OwnerLiteralSnapshot, j01SemanticInventoryRequest, j01ViewerValuesAgree, j04BrowserControlPlan, j04DefaultRecordCellTraceRowID, j04ExactEqual, j04FixtureManifest, j04PatientOperatorDOMPlan, j04PatientOperatorSourceIDs, j04PatientSelectionSeedPlan, j05ArtifactIdentityIsCurrent, loadJ04FixtureContract, normalizeJ04Surface, normalizeJ05LogicalValue, readJ05OutputRows, selectExternalJ01Manifest, shapeJ04Evidence, sourceMountMatches, summarizeTimingSamples, takeJavaScriptDialogCommandParams, validateJ04FixtureContract } from './loom-dev.mjs';

test('JavaScript dialog response overrides apply once and retain prompt defaults', () => {
  const cdp = { nextDialogResponse: { accept: true, promptText: 'Renamed Patients' } };
  assert.deepEqual(takeJavaScriptDialogCommandParams(cdp, { type: 'prompt' }, 'Default name'), {
    accept: true,
    promptText: 'Renamed Patients',
  });
  assert.equal(Object.hasOwn(cdp, 'nextDialogResponse'), false);
  assert.deepEqual(takeJavaScriptDialogCommandParams(cdp, { type: 'prompt' }, 'Default name'), {
    accept: true,
    promptText: 'Default name',
  });
  assert.deepEqual(takeJavaScriptDialogCommandParams(cdp, { type: 'confirm' }, 'Default name'), {
    accept: true,
  });
});

test('automatic preview witness must use a new receipt for the committed Builder draft and snapshot', () => {
  const baseline = {
    snapshotToken: 'snapshot-current',
    draftVersion: 8,
    draftDigest: 'digest-before',
    previewReceiptId: 'receipt-before',
    outputId: 'table-patient',
  };
  const state = {
    lifecycleState: 'READY',
    draftVersion: 9,
    draftDigest: 'digest-after',
    catalog: { snapshotToken: 'snapshot-current' },
    workspace: { documents: [{ output: { id: 'table-patient' } }] },
  };
  const preview = {
    status: 'ready',
    receiptId: 'receipt-after',
    outputId: 'table-patient',
    proposalId: '',
    draftVersion: 9,
    draftDigest: 'digest-after',
  };

  assert.equal(builderPreviewIsFreshForDraft(baseline, state, preview, baseline.outputId), true);
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, { ...preview, receiptId: baseline.previewReceiptId }, baseline.outputId), false,
    'a ready preview with the old receipt must not satisfy freshness');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, {
    ...preview,
    receiptId: baseline.previewReceiptId,
    text: 'Dataframe contract dev-patient-001',
  }, baseline.outputId), false,
    'old fixture text cannot make the previous preview receipt fresh');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, { ...preview, status: 'stale' }, baseline.outputId), false,
    'a stale preview cannot satisfy an exact-value assertion');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, { ...preview, proposalId: 'proposal-candidate' }, baseline.outputId), false,
    'a candidate preview must not stand in for the saved table preview');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, { ...preview, draftDigest: baseline.draftDigest }, baseline.outputId), false,
    'the visible preview draft digest must match the committed Builder response');
  assert.equal(builderPreviewIsFreshForDraft(baseline, { ...state, catalog: { snapshotToken: 'snapshot-stale' } }, preview, baseline.outputId), false,
    'a preview from another source snapshot must not satisfy freshness');
  assert.equal(builderDraftMatchesPreviewDOM(baseline, state, { ...preview, status: 'stale' }, baseline.outputId), true,
    'a failed preview diagnostic can still be tied to the newly committed draft');
  assert.equal(builderDraftMatchesPreviewDOM(baseline, state, { ...preview, draftVersion: 8 }, baseline.outputId), false,
    'a DOM draft version behind the API must not satisfy the committed-draft witness');
});

test('automatic preview accepts candidate receipt reuse only after exact saved-draft reconciliation', () => {
  const baseline = {
    snapshotToken: 'snapshot-current',
    draftVersion: 8,
    draftDigest: 'digest-before',
    previewReceiptId: 'receipt-candidate',
    previewProposalId: 'receipt-candidate',
    outputId: 'table-patient',
  };
  const state = {
    lifecycleState: 'READY',
    draftVersion: 9,
    draftDigest: 'digest-after',
    catalog: { snapshotToken: 'snapshot-current' },
    workspace: { documents: [{ output: { id: 'table-patient' } }] },
  };
  const preview = {
    status: 'ready',
    receiptId: 'receipt-candidate',
    outputId: 'table-patient',
    proposalId: '',
    draftVersion: 9,
    draftDigest: 'digest-after',
  };
  const savedCompile = {
    receiptId: 'receipt-candidate',
    intentDigest: 'digest-after',
    snapshotToken: 'snapshot-current',
    outputs: [{ outputId: 'table-patient' }],
  };

  assert.equal(builderPreviewIsFreshForDraft(baseline, state, preview, baseline.outputId, savedCompile), true,
    'the settled saved preview may reuse the exact candidate receipt after reconciliation proves the saved digest and output');
  assert.equal(builderPreviewIsFreshForDraft({ ...baseline, previewProposalId: '' }, state, preview, baseline.outputId, savedCompile), false,
    'an unchanged receipt from a non-proposal baseline remains stale');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, preview, baseline.outputId), false,
    'the proposal flag alone cannot prove the saved compile reused the candidate receipt');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, preview, baseline.outputId, {
    ...savedCompile,
    intentDigest: 'digest-before',
  }), false, 'the reconciled receipt must compile the newly saved draft');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, preview, baseline.outputId, {
    ...savedCompile,
    outputs: [{ outputId: 'another-table' }],
  }), false, 'the reconciled receipt must contain the selected output');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, { ...preview, outputId: 'another-table' }, baseline.outputId, savedCompile), false,
    'the visible preview must remain tied to the selected output');
  assert.equal(builderPreviewIsFreshForDraft(baseline, state, { ...preview, proposalId: 'receipt-candidate' }, baseline.outputId, savedCompile), false,
    'a candidate proposal still visible in the DOM is not a saved-preview transition');
});

test('Builder browser readiness requires a loaded workspace or the current empty-editor controls', () => {
  const editor = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx'), 'utf8');
  const rowPicker = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowRootPicker.tsx'), 'utf8');
  const workspace = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx'), 'utf8');
  assert.match(editor, /id="first-table-name"/);
  assert.match(rowPicker, /aria-label="Choose row type"/);
  assert.match(workspace, /data-testid="construction-workspace"/);

  const isReady = (presentSelectors) => Function('document', `return (${builderDOMReadyCondition});`)({
    querySelector: (selector) => presentSelectors.has(selector) ? {} : null,
  });
  assert.equal(isReady(new Set()), false, 'loading/error DOM must not count as a ready Builder');
  assert.equal(isReady(new Set(['#first-table-name'])), false, 'the table-name field alone is not enough');
  assert.equal(isReady(new Set(['#first-table-name', '[aria-label="Choose row type"]'])), true);
  assert.equal(isReady(new Set(['[data-testid="construction-workspace"]'])), true);
});

test('Add columns readiness requires the selected table preview for the current saved draft', () => {
  const condition = addColumnsActionReadinessCondition('true');
  const evaluate = (document) => Function('document', `return (${condition});`)(document);
  const workspace = { dataset: { draftVersion: '2', draftDigest: 'digest-current' } };
  const preview = {
    dataset: {
      previewStatus: 'ready',
      previewReceiptId: 'receipt-current',
      previewOutputId: 'out-current',
      currentDraftVersion: '2',
      currentDraftDigest: 'digest-current',
    },
  };
  const selectedTable = { getAttribute: () => 'construction-table-out-current' };
  const addColumns = { disabled: false };
  const makeDocument = ({ currentPreview = preview, button = addColumns, statuses = [] } = {}) => ({
    querySelector: (selector) => ({
      '[data-testid="construction-workspace"]': workspace,
      '[data-testid="construction-preview"]': currentPreview,
      '[data-testid^="construction-table-"][aria-current="page"]': selectedTable,
      '[data-testid="construction-action-add-columns"]:not(:disabled)': button.disabled ? null : button,
      '[data-testid="construction-action-add-columns"]': button,
    })[selector] ?? null,
    querySelectorAll: (selector) => selector === '[role=status]' ? statuses : [],
  });

  assert.equal(evaluate(makeDocument()), true);
  assert.equal(evaluate(makeDocument({ currentPreview: { dataset: { ...preview.dataset, previewStatus: 'empty' } } })), false,
    'the transient enabled button before automatic reconciliation is not readiness');
  assert.equal(evaluate(makeDocument({ currentPreview: { dataset: { ...preview.dataset, currentDraftDigest: 'digest-old' } } })), false,
    'a preview receipt for an older draft cannot satisfy readiness');
  assert.equal(evaluate(makeDocument({ statuses: [{ innerText: 'Loom is refreshing the current table draft. Field selection will return when the refresh completes.' }] })), false,
    'refresh status blocks readiness even if a stale enabled button is present');
  assert.equal(evaluate(makeDocument({ button: { disabled: true } })), false,
    'an enabled Add columns control remains required');
});

test('J04 creates its initial table through the visible row-type control', () => {
  const driver = readFileSync(join(process.cwd(), 'scripts/loom-dev.mjs'), 'utf8');
  const rowPicker = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowRootPicker.tsx'), 'utf8');
  const start = driver.indexOf("await action('create-observation-table-in-builder'");
  const end = driver.indexOf('\n\n    let state = await fetchBuilderState(target, explorerId);', start);
  assert.ok(start >= 0 && end > start, 'J04 browser creation action must remain identifiable');
  const createAction = driver.slice(start, end);
  assert.match(rowPicker, /aria-label=\{`Choose \$\{node\.resourceType\} rows`\}/);
  assert.doesNotMatch(createAction, /clickButton\('Create table'\)/);
  assert.match(createAction, /button\[aria-label="Choose Observation rows"\]/);
  assert.match(createAction, /clickButton\('Choose Observation rows'\)/);
  assert.match(createAction, /input\[aria-label="Search features by field name, concept, or code"\]/);
  assert.match(driver.slice(driver.lastIndexOf('const builderURL =', start), start), /demo-controls span.*target\.fixtureProject.*explorerId/s);
});

test('verify-fast creates its Observation and Patient tables through the current row picker and Add columns editor', () => {
  const driver = readFileSync(join(process.cwd(), 'scripts/loom-dev.mjs'), 'utf8');
  const builder = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx'), 'utf8');
  const rowPicker = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowRootPicker.tsx'), 'utf8');
  const catalog = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ConceptCatalog.tsx'), 'utf8');
  const actionBar = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx'), 'utf8');
  const choiceDialog = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/CatalogSelectionDialog.tsx'), 'utf8');
  const start = driver.indexOf('const verifyBrowserScenario =');
  const end = driver.indexOf('\nconst measureHotReload =', start);
  assert.ok(start >= 0 && end > start, 'generic verify-fast scenario must remain identifiable');
  const scenario = driver.slice(start, end);
  const ownerStart = scenario.indexOf("setInput('first-table-name', 'Observation owner records')");
  const patientStart = scenario.indexOf("setInput('first-table-name', 'Patients with observations')");
  assert.ok(ownerStart >= 0 && patientStart > ownerStart, 'both fixture table setup flows must remain in order');
  const ownerFlow = scenario.slice(ownerStart, patientStart);
  const patientFlow = scenario.slice(patientStart);
  const addColumnsReadyStart = driver.indexOf('export const addColumnsActionReadinessCondition =');
  const addColumnsReadyEnd = driver.indexOf('\n};', addColumnsReadyStart);
  assert.ok(addColumnsReadyStart >= 0 && addColumnsReadyEnd > addColumnsReadyStart, 'Add columns readiness wait must be identifiable');
  const addColumnsWaitStart = driver.indexOf('const waitForAddColumnsAction =', addColumnsReadyEnd);
  const addColumnsWaitEnd = driver.indexOf('\n};', addColumnsWaitStart);
  assert.ok(addColumnsWaitStart > addColumnsReadyEnd && addColumnsWaitEnd > addColumnsWaitStart, 'Add columns browser wait must use the ready condition');
  const addColumnsReady = `${driver.slice(addColumnsReadyStart, addColumnsReadyEnd)}\n${driver.slice(addColumnsWaitStart, addColumnsWaitEnd)}`;

  assert.match(rowPicker, /aria-label=\{`Choose \$\{node\.resourceType\} rows`\}/);
  assert.match(builder, /id="first-table-name"/);
  assert.match(actionBar, /data-testid=\{`construction-action-\$\{family\.toLowerCase\(\)\.replace\('_', '-'\)\}`\}/);
  assert.match(builder, /aria-label=\{`\$\{activeOperation\.label\} editor`\}/);
  assert.match(builder, /data-testid="construction-source-setup"/);
  assert.match(builder, /Advanced source setup/);
  assert.match(builder, /tablePreview\.rows !== null/);
  assert.match(builder, /state\.reconciliation === 'resolved'/);
  assert.match(builder, /tablePreview\.receiptId === state\.receipt\.receiptId/);
  assert.match(actionBar, /label: 'Add columns'/);
  assert.match(builder, /Fields and related data/);
  assert.match(catalog, /data-testid="feature-catalog-raw-fields"/);
  assert.match(choiceDialog, /Choose how to add these fields/);
  assert.match(choiceDialog, /`Add \$\{groups\.length\} \$\{groups\.length === 1 \? 'column' : 'columns'\}`/);

  assert.doesNotMatch(scenario, /clickButton\('Create table'\)|clickButton\('Preview'\)|textContent\.trim\(\) === 'Preview'|One row per|Source and column setup|Search fields and concepts|Choose output forms/);
  assert.match(ownerFlow, /button\[aria-label="Choose Observation rows"\]/);
  assert.match(ownerFlow, /await waitForAddColumnsAction\(cdp,/);
  assert.match(ownerFlow, /construction-action-add-columns/);
  assert.match(ownerFlow, /Add columns action is unavailable/);
  assert.match(ownerFlow, /clickButton\('Fields and related data'\)/);
  assert.match(ownerFlow, /shared: Keep each matching record/);
  assert.match(ownerFlow, /candidate\.textContent\.trim\(\) === 'Add 1 column'/);
  assert.match(ownerFlow, /clickButton\('Apply columns'\)/);
  assert.match(ownerFlow, /builder-persists-owner-record-construction/);
  assert.match(ownerFlow, /const ownerRecordsStateIsApplied = \(builder\) =>/);
  assert.match(ownerFlow, /source\?\.kind === 'ownerRecords'/);
  assert.match(ownerFlow, /source\.ownerRecords\?\.binding\?\.valuePath === 'valueQuantity\.value'/);
  assert.match(ownerFlow, /builder\?\.draftVersion > ownerPreviewBaseline\.draftVersion/);
  assert.match(ownerFlow, /builder\?\.draftDigest && builder\.draftDigest !== ownerPreviewBaseline\.draftDigest/);
  assert.match(ownerFlow, /source\.ownerRecords\?\.key\?\.system === 'urn:study:A'/);
  assert.match(ownerFlow, /source\.ownerRecords\?\.key\?\.code === 'shared'/);
  assert.match(ownerFlow, /ownerRecordsStateIsApplied\(ownerRecordsBuilder\)/);
  assert.match(ownerFlow, /const ownerRecordsColumn = ownerRecordsTable\.columns\.find\(\(column\) => column\.label === 'shared'\)/);
  assert.match(ownerFlow, /kind: 'ownerRecords'/);
  assert.match(ownerFlow, /system: 'urn:study:A'/);
  assert.match(ownerFlow, /code: 'shared'/);
  assert.match(ownerFlow, /ownerPath: 'component\[\]'/);
  assert.match(ownerFlow, /valuePath: 'valueQuantity\.value'/);
  const applyOwnerRecords = ownerFlow.indexOf("clickButton('Apply columns')");
  const savedOwnerRecordsWait = ownerFlow.indexOf('ownerApplyDeadline = Date.now() + 30000');
  const exactSavedOwnerRecordsPoll = ownerFlow.indexOf('if (ownerRecordsStateIsApplied(ownerRecordsBuilder)) break;');
  const exactSavedOwnerRecordsAssertion = ownerFlow.indexOf("recordAssertion(report, 'builder-persists-owner-record-construction'");
  const closeOwnerEditor = ownerFlow.indexOf("data-testid=\"construction-close-operation-editor\"");
  const closedOwnerEditor = ownerFlow.indexOf("!document.querySelector('[aria-label=\"Add columns editor\"]')");
  const configuredOwnerInput = ownerFlow.indexOf('Display name for configured ${ownerRecordsColumn.label}');
  assert.ok(applyOwnerRecords >= 0 && savedOwnerRecordsWait > applyOwnerRecords && exactSavedOwnerRecordsPoll > savedOwnerRecordsWait && exactSavedOwnerRecordsAssertion > exactSavedOwnerRecordsPoll && closeOwnerEditor > exactSavedOwnerRecordsAssertion && closedOwnerEditor > closeOwnerEditor && configuredOwnerInput > closedOwnerEditor,
    'the exact saved ownerRecords binding and new draft are observed before closing the editor that hides configured-column controls');
  assert.match(ownerFlow, /const ownerApplyDeadline = Date\.now\(\) \+ 30000/);
  assert.match(ownerFlow, /while \(Date\.now\(\) < ownerApplyDeadline\)/);
  assert.match(ownerFlow, /input\.getAttribute\('aria-label'\) === \$\{JSON\.stringify\(configuredOwnerLabel\)\} && !input\.disabled/);
  assert.match(builder, /!activeOperation && !workspaceEditor \? <details/);
  assert.match(ownerFlow, /captureBuilderPreviewBaseline\(target, cdp, ownerRecordsExplorerId\)/);
  assert.match(ownerFlow, /waitForFreshBuilderPreview\(/);
  assert.match(ownerFlow, /readBuilderPreviewDOM\(cdp\)\)\.receiptId/);
  assert.match(driver, /previewProposalId: preview\.proposalId/);
  assert.match(driver, /reconcileSavedBuilderDraft\(target, explorerId, state\)/);
  assert.match(driver, /savedCompileReceipt\.intentDigest === state\.draftDigest/);
  assert.match(ownerFlow, /preview-owner-record-inspector-preserves-value-unit-code-and-source/);

  assert.match(patientFlow, /button\[aria-label="Choose Patient rows"\]/);
  assert.match(patientFlow, /await waitForAddColumnsAction\(cdp,/);
  assert.match(patientFlow, /construction-action-add-columns/);
  assert.match(patientFlow, /feature-catalog-raw-fields/);
  assert.match(patientFlow, /Select Patient\.id/);
  assert.match(patientFlow, /clickButton\('Apply columns'\)/);
  assert.match(addColumnsReady, /const selector = '\[data-testid="construction-action-add-columns"\]'/);
  assert.match(addColumnsReady, /\$\{selector\}:not\(:disabled\)/);
  assert.equal((addColumnsReady.match(/await waitForBrowser/g) || []).length, 1,
    'the original browser-ready deadline must cover the entire first-table provisioning wait');
  assert.match(addColumnsReady, /preview\.dataset\.previewStatus === 'ready'/);
  assert.match(addColumnsReady, /preview\.dataset\.previewReceiptId/);
  assert.match(addColumnsReady, /preview\.dataset\.previewOutputId === selectedOutputId/);
  assert.match(addColumnsReady, /preview\.dataset\.currentDraftVersion === workspace\.dataset\.draftVersion/);
  assert.match(addColumnsReady, /preview\.dataset\.currentDraftDigest === workspace\.dataset\.draftDigest/);
  assert.match(addColumnsReady, /Loom is \(\?:refreshing the current table draft\|finishing the previous table update\)/);
  assert.match(addColumnsReady, /document\.querySelectorAll\('\[role=status\]'\)/);
  assert.match(addColumnsReady, /pending status|no pending Builder status/);
  assert.match(patientFlow, /advanced source setup is unavailable/);
  assert.match(patientFlow, /Feature authoring view/);
  assert.match(patientFlow, /builder-catalog-adds-default-root-field-without-graph/);
  assert.match(patientFlow, /builder-configures-exact-root-fields/);
  assert.match(patientFlow, /RELATIONSHIP_CARDINALITY_VIOLATION/);
  assert.match(patientFlow, /TEMPORAL_TIE_AMBIGUOUS/);
  assert.match(patientFlow, /preview-shows-exact-fixture-table/);
  assert.match(patientFlow, /waitForFreshBuilderPreview\(target, cdp, explorerId, exactTablePreviewBaseline/);
  assert.match(patientFlow, /waitForFreshBuilderPreview\(target, cdp, explorerId, valueCountPreviewBaseline/);
  assert.match(patientFlow, /waitForFreshBuilderDiagnostic\(target, cdp, explorerId, requireOnePreviewBaseline, 'RELATIONSHIP_CARDINALITY_VIOLATION'/);
  assert.match(patientFlow, /waitForFreshBuilderDiagnostic\(target, cdp, explorerId, temporalPreviewBaseline, 'TEMPORAL_TIE_AMBIGUOUS'/);
  assert.match(patientFlow, /waitForFreshBuilderPreview\(target, cdp, explorerId, tiePolicyPreviewBaseline/);
  assert.match(patientFlow, /waitForFreshBuilderPreview\(target, cdp, explorerId, maximumPreviewBaseline/);
  assert.match(patientFlow, /clickButton\('Publish'\)/);
  assert.match(patientFlow, /clickButton\('Viewer'\)/);
  assert.match(patientFlow, /clickButton\('Download dataset'\)/);
  assert.match(scenario, /demo-controls span.*target\.fixtureProject.*bootstrapExplorerId/s);
});

test('J04 fixture keeps valid Observation values, recorded absence, Patient aggregates, and pivot types in separate row scopes', () => {
  const fixtureDir = join(process.cwd(), 'testdata/devloop-fixture');
  const loaded = loadJ04FixtureContract(fixtureDir);
  assert.equal(loaded.contract.sourceFile, 'j04-records.ndjson.fixture');
  assert.equal(loaded.sourceRecords.length, 18);
  assert.equal(validateJ04FixtureContract(loaded.contract, loaded.sourceRecords), true);
  const manifest = j04FixtureManifest(fixtureDir, loaded);
  assert.deepEqual(manifest.summary, { sourceFile: 'j04-records.ndjson.fixture', sourceRecords: 18 });
  assert.deepEqual(manifest.files.map(({ name }) => name), ['Observation.ndjson', 'Patient.ndjson']);
  assert.deepEqual(manifest.files.map(({ contents }) => contents.toString('utf8').trim().split('\n').length), [16, 2]);
  assert.equal(loaded.contract.baseRowResourceType, 'Observation');
  assert.equal(loaded.contract.aggregateScope.rowResourceType, 'Patient');
  assert.deepEqual(loaded.contract.aggregateScope.selectedRowIdentities, ['Patient/j04-patient-001']);
  assert.equal(loaded.contract.aggregateScope.anchorPath, 'meta.lastUpdated');
  assert.equal(loaded.sourceRecords.find((record) => record.id === 'j04-patient-001').meta.lastUpdated, '2025-01-03T00:00:00Z');
  assert.deepEqual(loaded.contract.expectedAggregates.map(({ rowIdentity }) => rowIdentity), ['Patient/j04-patient-001']);
  assert.deepEqual(
    Object.fromEntries(['count', 'exists', 'min', 'max', 'mean', 'sum'].map((key) => [key, loaded.contract.expectedAggregates[0][key]])),
    { count: 3, exists: true, min: 0, max: 180, mean: 120, sum: 360 },
  );
  assert.equal(loaded.contract.pivot.categoryColumn.logicalType, 'string');
  assert.equal(loaded.contract.pivot.valueColumn.logicalType, 'number');
  assert.equal(loaded.contract.pivot.derivedColumns[0].name, 'j04_alpha_plus_beta');
  assert.deepEqual(loaded.contract.pivot.derivedColumns[0].expectedByGroup.map(({ presence, value }) => ({ presence, value })), [
    { presence: 'value', value: 7.5 },
    { presence: 'null', value: null },
  ]);
  assert.deepEqual(loaded.contract.unsupportedUnitRefusal, {
    rowIdentity: 'Patient/j04-patient-002',
    sourceRecordId: 'j04-unsupported-unit',
    status: 'REFUSED',
    reason: 'UNIT_IDENTITY_UNKNOWN',
    applied: false,
  });

  const changedIdentity = structuredClone(loaded.contract);
  changedIdentity.sourceRecords[0].id = 'j04-patient-substituted';
  assert.throws(() => validateJ04FixtureContract(changedIdentity, loaded.sourceRecords), /identities or order differ/);

  const changedSource = structuredClone(loaded.sourceRecords);
  changedSource.find((record) => record.id === 'j04-scalar-blank').valueString = 'not blank';
  assert.throws(() => validateJ04FixtureContract(loaded.contract, changedSource), /blank scalar evidence/);
  const invalidNull = structuredClone(loaded.sourceRecords);
  invalidNull.find((record) => record.id === 'j04-scalar-absence').valueString = null;
  assert.throws(() => validateJ04FixtureContract(loaded.contract, invalidNull), /scalar evidence/);
  const absence = loaded.sourceRecords.find((record) => record.id === 'j04-scalar-absence');
  assert.equal(Object.hasOwn(absence, 'valueString'), false);
  assert.deepEqual(absence.dataAbsentReason.coding, [{
    system: 'http://terminology.hl7.org/CodeSystem/data-absent-reason', code: 'unknown', display: 'Unknown',
  }]);
  const ambiguousAbsence = structuredClone(loaded.sourceRecords);
  ambiguousAbsence.find((record) => record.id === 'j04-scalar-absence').dataAbsentReason.coding.push({ system: 'urn:other', code: 'unknown' });
  assert.throws(() => validateJ04FixtureContract(loaded.contract, ambiguousAbsence), /exact official data-absent-reason coding/);
});

test('J04 fixture contract recalculates exact aggregate and temporal source evidence', () => {
  const { contract, sourceRecords } = loadJ04FixtureContract(join(process.cwd(), 'testdata/devloop-fixture'));
  const missingOperator = structuredClone(contract);
  delete missingOperator.expectedAggregates[0].sum;
  assert.throws(() => validateJ04FixtureContract(missingOperator, sourceRecords), /declare COUNT, EXISTS, MIN, MAX, MEAN, and SUM/);

  const changedTie = structuredClone(contract);
  changedTie.temporalOutcomes.latestSelectedRecordId = 'j04-window-excluded';
  assert.throws(() => validateJ04FixtureContract(changedTie, sourceRecords), /deterministic latest tie winner/);

  const changedSum = structuredClone(contract);
  changedSum.expectedAggregates[0].sum = 361;
  assert.throws(() => validateJ04FixtureContract(changedSum, sourceRecords), /SUM for Patient\/j04-patient-001/);

  const changedPopulation = structuredClone(contract);
  changedPopulation.aggregateScope.selectedRowIdentities[0] = 'Observation/j04-measure-001';
  assert.throws(() => validateJ04FixtureContract(changedPopulation, sourceRecords), /unique existing Patient rows/);

  const substitutedAnchor = structuredClone(sourceRecords);
  substitutedAnchor.find((record) => record.id === 'j04-patient-001').meta.lastUpdated = '2025-01-04T00:00:00Z';
  assert.throws(() => validateJ04FixtureContract(contract, substitutedAnchor), /Jan 1 inclusive to Jan 3 exclusive/);

  const changedAnchorPath = structuredClone(contract);
  changedAnchorPath.aggregateScope.anchorPath = 'birthDate';
  assert.throws(() => validateJ04FixtureContract(changedAnchorPath, sourceRecords), /Jan 1 inclusive to Jan 3 exclusive/);

  const appliedRefusal = structuredClone(contract);
  appliedRefusal.unsupportedUnitRefusal.applied = true;
  assert.throws(() => validateJ04FixtureContract(appliedRefusal, sourceRecords), /separate refused preview/);
});

test('J04 fixture rejects type-mixed pivot inputs and changed contribution or information-loss evidence', () => {
  const { contract, sourceRecords } = loadJ04FixtureContract(join(process.cwd(), 'testdata/devloop-fixture'));
  const mixedCategory = structuredClone(contract);
  mixedCategory.pivot.categoryColumn.logicalType = 'number';
  assert.throws(() => validateJ04FixtureContract(mixedCategory, sourceRecords), /one string category column/);

  const mixedValue = structuredClone(sourceRecords);
  mixedValue.find((record) => record.id === 'j04-pivot-alpha-a').valueQuantity.value = '2.5';
  assert.throws(() => validateJ04FixtureContract(contract, mixedValue), /numeric cm value type/);

  const changedContributor = structuredClone(contract);
  changedContributor.pivot.expectedContributors[0].sourceRecordIds[0] = 'j04-measure-001';
  assert.throws(() => validateJ04FixtureContract(changedContributor, sourceRecords), /contributor evidence/);

  const changedInformationLoss = structuredClone(contract);
  changedInformationLoss.pivot.expectedInformationLoss.unlistedExcludedRecordCount = 10;
  assert.throws(() => validateJ04FixtureContract(changedInformationLoss, sourceRecords), /information-loss evidence/);

  const changedPivotDerived = structuredClone(contract);
  changedPivotDerived.pivot.expectedRows[0].values.j04_alpha_plus_beta = 8;
  assert.throws(() => validateJ04FixtureContract(changedPivotDerived, sourceRecords), /pivot output rows/);
});

test('J04 fixture preserves the receipt, refusal, reload, Preview, Viewer, and typed-artifact acceptance contract', () => {
  const { contract, sourceRecords } = loadJ04FixtureContract(join(process.cwd(), 'testdata/devloop-fixture'));
  assert.equal(contract.acceptanceExpectations.previewProposalIsNonMutating, true);
  assert.equal(contract.acceptanceExpectations.staleApplyStatus, 409);
  assert.equal(contract.acceptanceExpectations.reloadPreservesAppliedDefinition, true);
  assert.equal(contract.acceptanceExpectations.previewViewerAndTypedArtifactAgree, true);
  assert.equal(contract.acceptanceExpectations.typedArtifactPreservesNativeJSONTypes, true);

  const changedStatus = structuredClone(contract);
  changedStatus.acceptanceExpectations.staleApplyStatus = 200;
  assert.throws(() => validateJ04FixtureContract(changedStatus, sourceRecords), /retain receipt, reload, cross-surface, and native-typing checks/);

  const changedPresence = structuredClone(contract);
  changedPresence.presenceCases.find((item) => item.presence === 'false').value = true;
  assert.throws(() => validateJ04FixtureContract(changedPresence, sourceRecords), /declared false literal/);
});

test('J04 browser control plan carries both row scopes and fixture-owned operator literals', () => {
  const { contract } = loadJ04FixtureContract(join(process.cwd(), 'testdata/devloop-fixture'));
  const plan = j04BrowserControlPlan(contract);
  assert.equal(plan.aggregate.rowResourceType, 'Patient');
  assert.deepEqual(plan.aggregate.selectedRowIdentities, ['Patient/j04-patient-001']);
  assert.deepEqual(plan.aggregate.operations, [
    { operation: 'COUNT', expected: 3 },
    { operation: 'EXISTS', expected: true },
    { operation: 'MIN', expected: 0 },
    { operation: 'MAX', expected: 180 },
    { operation: 'MEAN', expected: 120 },
    { operation: 'SUM', expected: 360 },
  ]);
  assert.equal(plan.aggregate.contributorWindow.latestSelectedRecordId, 'j04-measure-002');
  assert.equal(plan.aggregate.unitNormalization.targetUnit, 'cm');
  assert.equal(plan.aggregate.unitNormalization.refusal.applied, false);
  assert.equal(plan.observation.rowResourceType, 'Observation');
  assert.notEqual(plan.aggregate.rowResourceType, plan.observation.rowResourceType);
  assert.deepEqual(plan.observation.pivot.categories.map(({ code }) => code), ['alpha', 'beta']);
  assert.equal(plan.observation.pivot.expectedInformationLoss.unlistedExcludedRecordCount, 12);
  assert.deepEqual(plan.observation.presenceCases.map(({ presence }) => presence), ['missing', 'missing', 'false', 'zero', 'blank']);
  assert.deepEqual(plan.observation.recordedAbsence, {
    sourceRecordId: 'j04-scalar-absence', valuePath: 'valueString', codingPath: 'dataAbsentReason.coding[].code',
    systemPath: 'dataAbsentReason.coding[].system', displayPath: 'dataAbsentReason.coding[].display',
    system: 'http://terminology.hl7.org/CodeSystem/data-absent-reason', code: 'unknown', display: 'Unknown',
  });
  assert.throws(() => j04BrowserControlPlan({ ...contract, expectedAggregates: [{ count: 3 }] }), /all six aggregate literals/);
});

test('J04 Patient selection seed plan pins only the fixture-owned selected Patient', () => {
  const { contract } = loadJ04FixtureContract(join(process.cwd(), 'testdata/devloop-fixture'));
  assert.deepEqual(j04PatientSelectionSeedPlan(contract), {
    resourceType: 'Patient',
    selectedRowIdentities: ['Patient/j04-patient-001'],
    refs: [{ resourceType: 'Patient', id: 'j04-patient-001' }],
    memberCount: 1,
  });
  assert.deepEqual(j04PatientSelectionSeedPlan({
    ...contract,
    aggregateScope: { ...contract.aggregateScope, selectedRowIdentities: ['Patient/j04-patient-002'] },
  }).refs, [{ resourceType: 'Patient', id: 'j04-patient-002' }]);
  assert.throws(() => j04PatientSelectionSeedPlan({ aggregateScope: { rowResourceType: 'Patient', selectedRowIdentities: ['Observation/o1'] } }), /identity is invalid/);
});

test('J04 Patient DOM plan includes visible selection, contributor-window, operator, and unit controls', () => {
  const { contract } = loadJ04FixtureContract(join(process.cwd(), 'testdata/devloop-fixture'));
  const plan = j04PatientOperatorDOMPlan(contract);
  assert.deepEqual(plan.selectionAttachment, [
    'New explorer', 'Explorer name', 'Create blank', 'first-table-name', 'Create table',
    'Choose Patient rows', 'Starting collection', 'Use selected resources',
  ]);
  assert.deepEqual(plan.refusalTable, {
    visibleCreateAction: 'New table', rowIdentity: 'Patient/j04-patient-002', sourceRecordId: 'j04-unsupported-unit',
    expectedCode: 'UNIT_IDENTITY_UNKNOWN', mustNotPublish: true,
  });
  assert.deepEqual(plan.aggregateOperations, [
    { operation: 'COUNT', expected: 3, contributorWindowRequired: true }, { operation: 'EXISTS', expected: true, contributorWindowRequired: true },
    { operation: 'MIN', expected: 0, contributorWindowRequired: true }, { operation: 'MAX', expected: 180, contributorWindowRequired: true },
    { operation: 'MEAN', expected: 120, contributorWindowRequired: true }, { operation: 'SUM', expected: 360, contributorWindowRequired: true },
  ]);
  assert.deepEqual(plan.contributorWindow, {
    recordDatePath: 'effectiveDateTime', anchorPath: 'meta.lastUpdated', lookbackDays: 2,
    window: { startInclusive: '2025-01-01T00:00:00Z', endExclusive: '2025-01-03T00:00:00Z' },
    earliestRecordId: 'j04-measure-001', latestSelectedRecordId: 'j04-measure-002', tiePolicy: 'RESOURCE_KEY',
  });
  for (const control of ['Add date window', 'Edit date window', 'Record date', 'Compare with row date', 'Look back days', 'Include start boundary', 'Include end boundary', 'Date selection direction', 'Equal date handling', 'Apply date window', 'Apply date selection']) {
    assert.ok(plan.visibleActions.includes(control), `missing visible contributor-window control: ${control}`);
  }
  assert.equal(plan.unitNormalization.targetUnit, 'cm');
  assert.equal(plan.unitNormalization.targetSystem, 'http://unitsofmeasure.org');
  assert.equal(plan.unitNormalization.refusal.reason, 'UNIT_IDENTITY_UNKNOWN');
  assert.ok(plan.visibleActions.includes('Apply normalization'));
  assert.deepEqual(j04PatientOperatorSourceIDs(contract, 'MIN'), ['j04-measure-003']);
  assert.deepEqual(j04PatientOperatorSourceIDs(contract, 'MAX'), ['j04-measure-001', 'j04-measure-002']);
  assert.deepEqual(j04PatientOperatorSourceIDs(contract, 'SUM'), ['j04-measure-001', 'j04-measure-002', 'j04-measure-003']);
});

test('J04 dataframe evidence can still represent null, false, zero, and empty distinctly', () => {
  const columns = [
    { id: 'missing-id', name: 'Missing', logicalType: 'string' },
    { id: 'null-id', name: 'Recorded null', logicalType: 'string' },
    { id: 'false-id', name: 'False', logicalType: 'boolean' },
    { id: 'zero-id', name: 'Zero', logicalType: 'integer' },
    { id: 'empty-id', name: 'Empty', logicalType: 'string' },
  ];
  const input = { columns, rows: [{ rowId: 'j04-row-1', values: { 'null-id': null, 'false-id': false, 'zero-id': 0, 'empty-id': '' } }] };
  const shaped = shapeJ04Evidence(input);
  assert.deepEqual(shaped.rows[0].values.map((cell) => cell.presence), ['missing', 'null', 'false', 'zero', 'empty']);
  assert.equal(j04ExactEqual(shaped, shapeJ04Evidence(structuredClone(input))), true);

  const substitutedNull = structuredClone(input);
  substitutedNull.rows[0].values['missing-id'] = null;
  assert.equal(compareJ04Evidence(input, substitutedNull).matches, false);
  const zeroAsNull = structuredClone(input);
  zeroAsNull.rows[0].values['zero-id'] = null;
  assert.equal(compareJ04Evidence(input, zeroAsNull).matches, false);
  assert.equal(j04ExactEqual({ value: 0 }, { value: false }), false);
  assert.equal(j04ExactEqual({ value: '' }, {}), false);
});

test('J04 evidence comparison rejects changed stable schema identity and literal output', () => {
  const expected = {
    columns: [{ id: 'column-j04-1', name: 'Total', logicalType: 'number' }],
    rows: [{ rowId: 'Patient/j04-patient-001', values: { 'column-j04-1': 360 } }],
  };
  assert.equal(compareJ04Evidence(expected, structuredClone(expected)).matches, true);
  const changedColumn = structuredClone(expected);
  changedColumn.columns[0].id = 'replacement-column';
  assert.equal(compareJ04Evidence(expected, changedColumn).matches, false);
  const changedValue = structuredClone(expected);
  changedValue.rows[0].values['column-j04-1'] = 361;
  assert.equal(compareJ04Evidence(expected, changedValue).matches, false);
});

test('J04 Preview, Viewer, and artifact normalize to one grouped schema without losing units or native scalar presence', () => {
  const identity = ['subject-id', 'status-id'];
  const unit = { system: 'http://unitsofmeasure.org', code: 'cm', display: 'centimeter' };
  const preview = normalizeJ04Surface({
    columns: [
      { column: 'subject-id', label: 'Subject reference', logicalType: 'string', sourcePath: 'subject.reference', nullable: false },
      { column: 'status-id', label: 'Status', logicalType: 'string', sourcePath: 'status', nullable: false },
      { column: 'alpha-id', label: 'j04_alpha', logicalType: 'number', resultUnit: unit, nullable: true },
      { column: 'total-id', label: 'j04_alpha_plus_beta', logicalType: 'number', resultUnit: unit, nullable: true },
    ],
    rows: [
      { 'subject-id': 'Patient/2', 'status-id': 'final', 'alpha-id': 8, 'total-id': null },
      { 'subject-id': 'Patient/1', 'status-id': 'final', 'alpha-id': 4, 'total-id': 7.5 },
    ],
    identityColumns: identity,
  });
  const viewer = normalizeJ04Surface({
    columns: preview.columns.map(({ id, name, logicalType, ...metadata }) => ({ column: id, label: name, logicalType, ...metadata })),
    rows: [
      { 'subject-id': 'Patient/1', 'status-id': 'final', 'alpha-id': 4, 'total-id': 7.5 },
      { 'subject-id': 'Patient/2', 'status-id': 'final', 'alpha-id': 8, 'total-id': null },
    ],
    identityColumns: identity,
  });
  const artifact = normalizeJ04Surface({
    columns: preview.columns.map(({ id, name, logicalType, ...metadata }) => ({
      id,
      name,
      rowKey: `${id}-artifact`,
      logicalType,
      ...metadata,
    })),
    rows: [
      { 'subject-id-artifact': 'Patient/1', 'status-id-artifact': 'final', 'alpha-id-artifact': 4, 'total-id-artifact': 7.5 },
      { 'subject-id-artifact': 'Patient/2', 'status-id-artifact': 'final', 'alpha-id-artifact': 8, 'total-id-artifact': null },
    ],
    identityColumns: identity,
  });
  assert.equal(compareJ04Evidence(preview, viewer).matches, true);
  assert.equal(compareJ04Evidence(preview, artifact).matches, true);
  assert.deepEqual(preview.columns.find((column) => column.id === 'total-id').resultUnit, unit);
  assert.deepEqual(preview.rows.map((row) => row.rowId), ['["Patient/1","final"]', '["Patient/2","final"]']);
});

test('J04 surface normalization reuses J05 row parsing and preserves missing versus recorded null', () => {
  const parsed = readJ05OutputRows([
    { 'group-id': 'Patient/1', 'missing-id': null },
    { 'group-id': 'Patient/2' },
  ], ['group-id', 'missing-id'], { preserveMissing: true });
  assert.deepEqual(parsed, [
    { 'group-id': 'Patient/1', 'missing-id': null },
    { 'group-id': 'Patient/2' },
  ]);
  const surface = normalizeJ04Surface({
    columns: [
      { column: 'group-id', label: 'Group', logicalType: 'string' },
      { column: 'missing-id', label: 'Missing', logicalType: 'string' },
      { column: 'false-id', label: 'False', logicalType: 'boolean' },
      { column: 'zero-id', label: 'Zero', logicalType: 'integer' },
      { column: 'blank-id', label: 'Blank', logicalType: 'string' },
    ],
    rows: [{ 'group-id': 'Patient/1', 'missing-id': null, 'false-id': false, 'zero-id': 0, 'blank-id': ' ' }, { 'group-id': 'Patient/2' }],
    identityColumns: ['group-id'],
  });
  const first = shapeJ04Evidence({ columns: surface.columns, rows: surface.rows }).rows[0];
  const second = shapeJ04Evidence({ columns: surface.columns, rows: surface.rows }).rows[1];
  assert.deepEqual(first.values.map((cell) => cell.presence), ['value', 'null', 'false', 'zero', 'blank']);
  assert.deepEqual(second.values.map((cell) => cell.presence), ['value', 'missing', 'missing', 'missing', 'missing']);
  const altered = structuredClone(surface);
  altered.rows[1].values['missing-id'] = null;
  assert.equal(compareJ04Evidence(surface, altered).matches, false);
  assert.throws(() => normalizeJ04Surface({ columns: surface.columns, rows: [{ 'missing-id': 'no group' }], identityColumns: ['group-id'] }), /omits grouped identity column/);
});

const j05Identity = {
  project: 'loom_dev_j05',
  datasetGeneration: 'fixture-j05',
  receiptId: 'receipt-j05',
  executionId: 'execution-j05',
  outputId: 'patients',
  revisionId: 'revision-j05',
  schemaDigest: 'schema-j05',
  outputContractDigest: 'contract-j05',
};

const j05ArtifactPackage = ({ format, columns, data, rowCount, rowIdentity = { key: '__loom_row_id', sourceResourceType: 'Patient', sourceIdColumn: columns[0].name } }) => {
  const descriptor = {
    version: 1,
    outputKey: j05Identity.outputId,
    receiptFormatVersion: 2,
    compilerContractVersion: 'compiler-v2',
    recipeSchemaVersion: 1,
    translationVersion: 'translation-v1',
    sourceGeneration: j05Identity.datasetGeneration,
    publishedSchemaDigest: j05Identity.schemaDigest,
    resolvedSchemaDigest: 'resolved-schema-j05',
    outputContractDigest: j05Identity.outputContractDigest,
    rowGrain: 'patient',
    rowMultiplication: 'none',
    rowIdentity,
    columns,
  };
  const members = new Map([
    [format === 'CSV' ? 'data.csv' : 'data.jsonl', Buffer.from(data)],
    ['schema.json', Buffer.from(JSON.stringify({ format, columns, nullEncoding: '\\N', arrayEncoding: format === 'JSONL' ? 'native' : 'json' }))],
    ['provenance.json', Buffer.from(JSON.stringify({ project: j05Identity.project, explorerId: 'explorer-j05', ...j05Identity }))],
    ['quality.json', Buffer.from(JSON.stringify({ status: 'COMPLETE', contributors: [{ resourceType: 'Patient', sourcePath: columns[0].sourcePath ?? 'id' }] }))],
    ['README.md', Buffer.from('J05 fixture artifact')],
  ]);
  const checksums = [...members].map(([name, bytes]) => ({
    name,
    bytes: bytes.length,
    sha256: createHash('sha256').update(bytes).digest('hex'),
  }));
  members.set('manifest.json', Buffer.from(JSON.stringify({
    version: 2,
    identity: j05Identity,
    descriptor,
    format,
    selection: { recipe: 'patients', datasetGeneration: j05Identity.datasetGeneration },
    interpretations: [],
    rows: rowCount,
    features: columns.length,
    nullEncoding: '\\N',
    arrayEncoding: format === 'JSONL' ? 'native' : 'json',
    members: checksums,
  })));
  return members;
};

test('J03 reads explicit group revision identity from the preview row identity object', () => {
  const rows = explicitGroupPreviewRows([{
    group_id: 'j03-reviewed',
    members: [{ source_identity: { id: 'dev-patient-001' } }],
    __loom_row_id: { group_revision_id: 'grouprev_j03', group_id: 'j03-reviewed' },
  }]);

  assert.deepEqual(rows.map(({ rawIdentity, ...row }) => row), [{
    rowIdentity: 'map[group_id:j03-reviewed group_revision_id:grouprev_j03]',
    groupRevisionId: 'grouprev_j03',
    groupId: 'j03-reviewed',
    sourceMemberIDs: ['dev-patient-001'],
  }]);
});

test('development evidence compares canonical project identities', () => {
  assert.equal(canonicalProjectID('loom_dev_verify_run-1234'), 'loom_dev_verify_run/1234');
  assert.equal(canonicalProjectID('study/project'), 'study/project');
  assert.equal(canonicalProjectID('project-a'), 'project-a');
});

test('development commands use the current Go authoring semantics version', () => {
  const source = readFileSync(join(process.cwd(), 'internal/explorer/authoringv2/types.go'), 'utf8');
  const match = source.match(/CurrentSemanticsVersion\s*=\s*(\d+)/);
  assert.ok(match, 'Go authoring semantics version is missing');
  assert.equal(AUTHORING_SEMANTICS_VERSION, Number(match[1]));

  const uiTypesSource = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/types.ts'), 'utf8');
  assert.match(uiTypesSource, /export \{ EXPLORER_AUTHORING_SEMANTICS_VERSION \} from '\.\/authoringSemanticsVersion\.mjs';/);
  const uiVersionSource = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/authoringSemanticsVersion.mjs'), 'utf8');
  const uiMatch = uiVersionSource.match(/EXPLORER_AUTHORING_SEMANTICS_VERSION\s*=\s*(\d+)/);
  assert.ok(uiMatch, 'shared UI authoring semantics version is missing');
  assert.equal(Number(uiMatch[1]), Number(match[1]));
});

test('bootstrap commands prefer Builder workspace semantics and use the shared UI contract for a new workspace', () => {
  assert.equal(authoringCommandSemanticsVersion({ workspace: { semanticsVersion: 7 } }), 7);
  assert.equal(authoringCommandSemanticsVersion({ workspace: null }), AUTHORING_SEMANTICS_VERSION);
  assert.equal(authoringCommandSemanticsVersion({}), AUTHORING_SEMANTICS_VERSION);
});

test('fixture FIRST expectation follows independently observed storage-key ordering', () => {
  assert.equal(expectedFixtureRelatedValue('loom_dev_verify_mu4ctgo1-4a680895', 'fixture-v1'), 172.5);
  assert.equal(expectedFixtureRelatedValue('loom_dev_verify_mu4d6n33-4abffd57', 'fixture-v1'), 180);
});

test('J04 cell-trace identity matches the generation-qualified default row key contract', () => {
  assert.equal(
    j04DefaultRecordCellTraceRowID('loom_dev_verify_sample-12345678', 'fixture-v1', 'Observation', 'j04-scalar-null'),
    'fa57df4194a5b84dba53099505ef7bab8904aefbdf473f2bb27619b7dcec1bda',
  );
  assert.notEqual(
    j04DefaultRecordCellTraceRowID('loom_dev_verify_sample-12345678', 'fixture-v1', 'Observation', 'j04-scalar-null'),
    j04DefaultRecordCellTraceRowID('loom_dev_verify_sample-12345678', 'fixture-v1', 'Observation', 'j04-scalar-missing'),
  );
  assert.throws(() => j04DefaultRecordCellTraceRowID('project', '', 'Observation', 'record'), /requires project, generation/);
});

test('development session defaults to isolated names, ports, and fixture', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  try {
    const target = createDevSession({ LOOM_DEV_PORT_REGISTRY: join(registryRoot, 'ports.json') }, process.cwd());
    assert.match(target.composeProject, /^loom-dev-[a-f0-9]{12}$/);
    assert.match(target.fixtureProject, /^loom_dev_[a-f0-9]{12}$/);
    assert.equal(target.fixtureGeneration, 'fixture-v1');
    assert.ok(target.apiPort >= 8180 && target.apiPort < 30000);
    assert.ok(target.uiPort >= 30000);
    assert.match(target.fixtureDir, /testdata\/devloop-fixture$/);
    assert.notEqual(target.composeProject, 'loom-demo');
  } finally {
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('development session accepts a read-only external FHIR fixture directory', () => {
  const root = mkdtempSync(join(tmpdir(), 'loom-dev-external-source-'));
  const fixture = mkdtempSync(join(tmpdir(), 'loom-dev-external-fixture-'));
  try {
    writeFileSync(join(root, 'go.mod'), 'module example.test/loom\n');
    mkdirSync(join(root, 'testdata/devloop-fixture'), { recursive: true });
    for (const name of ['Patient.ndjson', 'Observation.ndjson']) writeFileSync(join(fixture, name), '{}\n');
    const target = createDevSession({
      LOOM_DEV_SOURCE_ROOT: root,
      LOOM_DEV_FIXTURE_DIR: fixture,
      LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-external',
      LOOM_DEV_PROJECT: 'loom_dev_external',
      LOOM_DEV_API_PORT: '18180',
      LOOM_DEV_UI_PORT: '33000',
      LOOM_DEV_FIXTURE_TIMEOUT_MS: '900000',
      LOOM_DEV_ARTIFACTS: join(root, '.artifacts'),
    }, root);
    assert.equal(target.fixtureDir, fixture);
    assert.equal(target.fixtureLoadTimeout, 900000);
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('external J01 manifest selects exact deterministic records and preserves source stats', async () => {
  const fixture = mkdtempSync(join(tmpdir(), 'loom-dev-j01-manifest-'));
  try {
    const patientRecords = Array.from({ length: 35 }, (_, index) => JSON.stringify({
      resourceType: 'Patient', id: `patient-${index + 1}`, gender: index % 2 ? 'female' : 'male',
    }));
    const observationRecords = Array.from({ length: 40 }, (_, index) => JSON.stringify({
      resourceType: 'Observation', id: `observation-${index + 1}`, status: 'final',
      ...(index === 39 ? { component: [
        { code: { coding: [{ system: 'https://cda.test', code: 'specimen_type', display: 'specimen type' }] }, valueString: 'blood' },
        { code: { coding: [{ system: 'https://cda.test', code: 'collection_method', display: 'collection method' }] }, valueString: 'venipuncture' },
      ] } : {}),
    }));
    writeFileSync(join(fixture, 'Patient.ndjson'), `${patientRecords.join('\n')}\n`);
    writeFileSync(join(fixture, 'Observation.ndjson'), `${observationRecords.join('\n')}\n`);

    const manifest = await selectExternalJ01Manifest(fixture);
    assert.equal(manifest.summary.version, 'cda-fhir-meta-j01-v1');
    assert.equal(manifest.summary.selection.firstPatientRecords, 32);
    assert.equal(manifest.summary.selection.firstObservationRecords, 32);
    assert.equal(manifest.files[0].records.length, 32);
    assert.deepEqual(manifest.files[0].records.map((record) => record.id), Array.from({ length: 32 }, (_, index) => `patient-${index + 1}`));
    assert.equal(manifest.files[1].records.length, 33);
    assert.equal(manifest.files[1].records.at(-1).id, 'observation-40');
    assert.deepEqual(manifest.files[1].repeatedComponentRecord, { recordNumber: 40, sourceLine: 40, id: 'observation-40', componentCount: 2 });
    assert.match(manifest.summary.sourceSHA256, /^sha256:[a-f0-9]{64}$/);
    assert.equal(assertExternalJ01SourcesUnchanged(manifest), true);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('external J01 source guard rejects a changed fixture file', async () => {
  const fixture = mkdtempSync(join(tmpdir(), 'loom-dev-j01-mutated-'));
  try {
    const patientPath = join(fixture, 'Patient.ndjson');
    const observationPath = join(fixture, 'Observation.ndjson');
    writeFileSync(patientPath, `${Array.from({ length: 32 }, (_, index) => JSON.stringify({ resourceType: 'Patient', id: `patient-${index + 1}` })).join('\n')}\n`);
    writeFileSync(observationPath, `${Array.from({ length: 32 }, (_, index) => JSON.stringify({
      resourceType: 'Observation', id: `observation-${index + 1}`,
      ...(index === 31 ? { component: [
        { code: { coding: [{ system: 'https://cda.test', code: 'one' }] } },
        { code: { coding: [{ system: 'https://cda.test', code: 'two' }] } },
      ] } : {}),
    })).join('\n')}\n`);
    const manifest = await selectExternalJ01Manifest(fixture);
    assert.equal(assertExternalJ01SourcesUnchanged(manifest), true);
    writeFileSync(observationPath, `${readFileSync(observationPath, 'utf8')}\n`);
    assert.throws(() => assertExternalJ01SourcesUnchanged(manifest), /source changed during verification/);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('external J01 Patient scalar comes from the selected live catalog', () => {
  const patientNode = { nodeId: 'patient-node', resourceType: 'Patient' };
  const state = {
    catalog: {
      nodes: [patientNode, { nodeId: 'observation-node', resourceType: 'Observation' }],
      candidates: [
        { nodeId: 'patient-node', fieldPath: 'gender', logicalType: 'string', cardinality: 'optional_one' },
        {
          nodeId: 'patient-node', fieldPath: 'resourceType', label: 'resourceType',
          logicalType: 'string', cardinality: 'optional_one',
          constructionChoice: { options: [{ form: 'VALUE', support: 'SUPPORTED' }] },
        },
        {
          nodeId: 'observation-node', fieldPath: 'resourceType', label: 'resourceType',
          logicalType: 'string', cardinality: 'optional_one',
          constructionChoice: { options: [{ form: 'VALUE', support: 'SUPPORTED' }] },
        },
      ],
    },
  };
  assert.deepEqual(externalJ01PatientScalar(state), {
    fieldPath: 'resourceType', label: 'resourceType', checkboxLabel: 'Select Patient.resourceType',
  });
  assert.throws(() => externalJ01PatientScalar({ catalog: { nodes: [patientNode], candidates: [] } }), /does not expose a supported scalar Patient\.resourceType/);
});

test('timing summaries use actual samples and reject invalid durations', () => {
  assert.deepEqual(summarizeTimingSamples(Array.from({ length: 30 }, (_, index) => index)), {
    count: 30, p50Ms: 14.5, p95Ms: 28, minMs: 0, maxMs: 29,
  });
  assert.throws(() => summarizeTimingSamples([]), /non-empty array/);
  assert.throws(() => summarizeTimingSamples([1, Number.NaN]), /finite, non-negative/);
  assert.throws(() => summarizeTimingSamples([1, -1]), /finite, non-negative/);
});

test('J01 artifact controls support both the integration modal and direct-download Viewer', () => {
  assert.deepEqual(j01ArtifactDownloadPlan(['Download dataset']), {
    triggerLabel: 'Download dataset', confirmationLabel: 'Download ZIP',
  });
  assert.deepEqual(j01ArtifactDownloadPlan(['Download training artifact']), {
    triggerLabel: 'Download training artifact', confirmationLabel: undefined,
  });
  assert.throws(() => j01ArtifactDownloadPlan(['Export CSV']), /no supported artifact download control/);
  assert.throws(() => j01ArtifactDownloadPlan(undefined), /array of button labels/);
});

test('J01 owner literal snapshots compare source identity semantically', () => {
  assert.deepEqual(j01OwnerLiteralSnapshot({
    status: 'VALUE', value: 111, unit: 'cm', choiceArm: 'valueQuantity',
    codings: [{ system: 'urn:study:A', code: 'shared' }],
    source: { ownerOrdinal: 0, ownerPath: 'component[]', resourceId: 'dev-pair-001', resourceType: 'Observation' },
  }), {
    status: 'VALUE', value: 111, unit: 'cm', choiceArm: 'valueQuantity',
    system: 'urn:study:A', code: 'shared',
    source: { resourceType: 'Observation', resourceId: 'dev-pair-001', ownerPath: 'component[]', ownerOrdinal: 0 },
  });
});

test('J01 typed artifact values treat omitted nullable fields as null', () => {
  assert.equal(j01JSONValuesEquivalent(
    [{ status: 'ABSENT', values: [] }],
    [{ status: 'ABSENT', unit: null, value: null, values: [] }],
  ), true);
  assert.equal(j01JSONValuesEquivalent({ value: 0 }, { value: null }), false);
  assert.equal(j01JSONValuesEquivalent([1, 2], [2, 1]), false);
});

test('J01 Viewer comparison accepts rows outside the bounded Preview only when the complete artifact agrees', () => {
  const columns = [
    { column: 'value', label: 'Value' },
    { column: 'owners', label: 'Owners' },
    { column: 'id', label: 'Identifier' },
  ];
  const viewerTable = {
    headers: ['Value', 'Owners', 'Identifier'],
    rows: [['1', '—', 'previewed'], ['999', '—', 'outside-preview']],
  };
  const previewByID = new Map([['previewed', { id: 'previewed', value: 1, owners: [] }]]);
  const artifactByID = new Map([
    ['previewed', { id: 'previewed', value: 1, owners: [] }],
    ['outside-preview', { id: 'outside-preview', value: 999, owners: [] }],
  ]);
  const input = { viewerTable, columns, previewByID, artifactByID, idColumn: 'id', structuredColumn: 'owners' };
  assert.equal(j01ViewerValuesAgree(input), true);
  assert.equal(j01ViewerValuesAgree({ ...input, artifactByID: new Map([['previewed', artifactByID.get('previewed')]]) }), false);
  assert.equal(j01ViewerValuesAgree({ ...input, previewByID: new Map() }), false);
});

test('generation load polling distinguishes durable completion from failure', () => {
  assert.equal(generationLoadDisposition({ state: 'LOADING' }), 'loading');
  assert.equal(generationLoadDisposition({ state: 'STAGED' }), 'ready');
  assert.equal(generationLoadDisposition({ state: 'READY' }), 'ready');
  assert.equal(generationLoadDisposition({ state: 'FAILED' }), 'failed');
  assert.equal(generationLoadDisposition({}), 'unknown');
});

test('bootstrap seeding is limited to a new or interrupted default draft', () => {
  const catalog = {
    nodes: [{ nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true }],
    candidates: [],
  };
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'NEW', draftVersion: 0, workspace: null, catalog }), true);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 1, workspace: { documents: [{ output: { title: 'Patients' }, columns: [] }] }, catalog }), true);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 2, workspace: { documents: [] }, catalog }), false);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 2, workspace: { documents: [{ output: { title: 'Patients' }, columns: [] }] }, catalog }), false);
});

test('bootstrap seed plan uses the current Patient catalog, never transient project identities', () => {
  const state = {
    lifecycleState: 'NEW',
    draftVersion: 0,
    workspace: null,
    catalog: {
      nodes: [
        { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true },
        { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true },
      ],
      candidates: [
        { candidateId: 'patient-id-current', nodeId: 'patient', fieldPath: 'root.id', defaultProjectionMode: 'SCALAR', label: 'id' },
        { candidateId: 'patient-family-current', nodeId: 'patient', fieldPath: 'root.name[].family', defaultProjectionMode: 'INDEXED', label: 'name[].family' },
        { candidateId: 'patient-gender-current', nodeId: 'patient', fieldPath: 'gender', defaultProjectionMode: 'SCALAR', label: 'gender' },
        { candidateId: 'observation-id-current', nodeId: 'observation', fieldPath: 'id', defaultProjectionMode: 'SCALAR', label: 'id' },
      ],
    },
  };
  const plan = bootstrapSeedPlan(state);
  assert.equal(plan.createTable, true);
  assert.equal(plan.rootNodeId, 'patient');
  assert.deepEqual(plan.candidates.map((candidate) => candidate.candidateId), [
    'patient-id-current',
    'patient-family-current',
    'patient-gender-current',
  ]);
  assert.equal(JSON.stringify(plan).includes('loom_dev_verify_'), false);
});

test('default development sessions separate worktree identities and ports', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  const registry = join(registryRoot, 'ports.json');
  const roots = [mkdtempSync(join(tmpdir(), 'loom-dev-session-a-')), mkdtempSync(join(tmpdir(), 'loom-dev-session-b-'))];
  try {
    for (const root of roots) {
      mkdirSync(join(root, 'testdata/devloop-fixture'), { recursive: true });
      writeFileSync(join(root, 'go.mod'), 'module example.test\n');
      for (const file of ['Patient.ndjson', 'Observation.ndjson', 'recipe.json']) {
        writeFileSync(join(root, 'testdata/devloop-fixture', file), '{}\n');
      }
    }
    const env = { LOOM_DEV_PORT_REGISTRY: registry };
    const first = createDevSession(env, roots[0]);
    const second = createDevSession(env, roots[1]);
    assert.notEqual(first.composeProject, second.composeProject);
    assert.notEqual(first.fixtureProject, second.fixtureProject);
    assert.notEqual(first.apiPort, second.apiPort);
    assert.notEqual(first.uiPort, second.uiPort);
    assert.notEqual(first.artifacts, second.artifacts);
  } finally {
    for (const root of roots) rmSync(root, { recursive: true, force: true });
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('named Compose projects in one worktree receive stable, distinct ports', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-compose-registry-'));
  const root = mkdtempSync(join(tmpdir(), 'loom-dev-compose-source-'));
  const registry = join(registryRoot, 'ports.json');
  try {
    mkdirSync(join(root, 'testdata/devloop-fixture'), { recursive: true });
    writeFileSync(join(root, 'go.mod'), 'module example.test\n');
    for (const file of ['Patient.ndjson', 'Observation.ndjson', 'recipe.json']) {
      writeFileSync(join(root, 'testdata/devloop-fixture', file), '{}\n');
    }

    const first = createDevSession({
      LOOM_DEV_PORT_REGISTRY: registry,
      LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-compose-first',
    }, root);
    const second = createDevSession({
      LOOM_DEV_PORT_REGISTRY: registry,
      LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-compose-second',
    }, root);
    const firstAgain = createDevSession({
      LOOM_DEV_PORT_REGISTRY: registry,
      LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-compose-first',
    }, root);
    const defaultProject = createDevSession({ LOOM_DEV_PORT_REGISTRY: registry }, root);
    const defaultProjectAgain = createDevSession({
      LOOM_DEV_PORT_REGISTRY: registry,
      LOOM_DEV_COMPOSE_PROJECT: defaultProject.composeProject,
    }, root);

    assert.notEqual(first.apiPort, second.apiPort);
    assert.notEqual(first.uiPort, second.uiPort);
    assert.equal(firstAgain.apiPort, first.apiPort);
    assert.equal(firstAgain.uiPort, first.uiPort);
    assert.equal(defaultProjectAgain.apiPort, defaultProject.apiPort);
    assert.equal(defaultProjectAgain.uiPort, defaultProject.uiPort);
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('derived ports are forwarded to Compose', () => {
  const target = createDevSession({}, process.cwd());
  const env = commandEnvironment(target);
  assert.equal(env.LOOM_DEV_SOURCE_ROOT, target.sourceRoot);
  assert.equal(env.LOOM_DEV_COMPOSE_PROJECT, target.composeProject);
  assert.equal(env.LOOM_DEV_PROJECT, target.fixtureProject);
  assert.equal(env.LOOM_DEV_GENERATION, target.fixtureGeneration);
  assert.equal(env.LOOM_DEV_HOST, target.host);
  assert.equal(env.LOOM_DEV_API_PORT, String(target.apiPort));
  assert.equal(env.LOOM_DEV_UI_PORT, String(target.uiPort));
  assert.equal(env.LOOM_DEV_EXPLORER, 'loom-dev-bootstrap');
  assert.match(target.populationMappingCursorSecret, /^[a-f0-9]{64}$/);
  assert.equal(env.LOOM_POPULATION_MAPPING_CURSOR_SECRET, target.populationMappingCursorSecret);
});

test('development cursor secret can be explicitly supplied', () => {
  const target = createDevSession({ LOOM_POPULATION_MAPPING_CURSOR_SECRET: 'local-session-cursor-secret' }, process.cwd());
  assert.equal(target.populationMappingCursorSecret, 'local-session-cursor-secret');
  assert.equal(commandEnvironment(target).LOOM_POPULATION_MAPPING_CURSOR_SECRET, 'local-session-cursor-secret');
});

test('explicit ports bypass an unusable port registry', () => {
  const target = createDevSession({
    LOOM_DEV_API_PORT: '8281',
    LOOM_DEV_UI_PORT: '3281',
    LOOM_DEV_PORT_REGISTRY: '/private/tmp/loom-dev-unusable-registry/ports.json',
  }, process.cwd());
  assert.equal(target.apiPort, 8281);
  assert.equal(target.uiPort, 3281);
});

test('stale port registry locks are recoverable', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  const registry = join(registryRoot, 'ports.json');
  const lock = `${registry}.lock`;
  try {
    writeFileSync(lock, 'stale');
    const staleTime = new Date(Date.now() - 60000);
    utimesSync(lock, staleTime, staleTime);
    const target = createDevSession({ LOOM_DEV_PORT_REGISTRY: registry }, process.cwd());
    assert.match(target.composeProject, /^loom-dev-[a-f0-9]{12}$/);
    assert.equal(target.apiPort, 8180);
  } finally {
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('development session rejects canonical Compose and data targets', () => {
  assert.throws(
    () => createDevSession({ LOOM_DEV_COMPOSE_PROJECT: 'research-stack' }, process.cwd()),
    /must use the loom-dev namespace/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_PROJECT: 'another_research_dataset' }, process.cwd()),
    /must use the loom_dev_ namespace/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_COMPOSE_PROJECT: 'loom-demo' }, process.cwd()),
    /must not use the canonical loom-demo project/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_PROJECT: 'NCPI_ACCEPTANCE' }, process.cwd()),
    /must not target the canonical NCPI_ACCEPTANCE project/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_API_URL: 'http://example.test:8080' }, process.cwd()),
    /must point to this session's loopback port/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_HOST: '0.0.0.0' }, process.cwd()),
    /must be a loopback host/,
  );
});

test('verification report starts with an explicit build state and target ownership', () => {
  const target = createDevSession({}, process.cwd());
  const report = createVerificationReport(target);
  assert.equal(report.status, 'building');
  assert.equal(report.scenario, 'builder-preview-publish-viewer-filter-export');
  assert.equal(report.target.project, target.fixtureProject);
  assert.deepEqual(report.assertions, []);
  assert.deepEqual(report.timings, {});
  assert.deepEqual(report.evidencePaths, []);
  assert.deepEqual(report.limitations, []);
});

test('fixture source digest is stable across file enumeration order and excludes non-source files', () => {
  const fixture = mkdtempSync(join(tmpdir(), 'loom-dev-fixture-digest-'));
  try {
    writeFileSync(join(fixture, 'Observation.ndjson'), '{"id":"observation"}\n');
    writeFileSync(join(fixture, 'Patient.ndjson'), '{"id":"patient"}\n');
    writeFileSync(join(fixture, 'recipe.json'), '{"not":"source"}\n');
    const first = fixtureSourceDigest(fixture);
    writeFileSync(join(fixture, 'README.txt'), 'ignored\n');
    assert.equal(fixtureSourceDigest(fixture), first);
    writeFileSync(join(fixture, 'Patient.ndjson'), '{"id":"changed"}\n');
    assert.notEqual(fixtureSourceDigest(fixture), first);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('J01 fixture generates 1,000 distinct scalar concepts and hostile owner records', () => {
  const fixture = join(process.cwd(), 'testdata/devloop-fixture');
  const generated = generatedJ01ConceptNDJSON(fixture)?.trim().split('\n').map((line) => JSON.parse(line));
  assert.equal(generated?.length, 1000);
  assert.deepEqual(generated?.[0], {
    resourceType: 'Observation',
    id: 'dev-j01-concept-0000',
    status: 'final',
    code: { coding: [{ system: 'urn:loom:j01:catalog', code: 'concept-0000', display: 'J01 concept 0000' }] },
    valueInteger: 0,
  });
  assert.equal(generated?.at(-1)?.code.coding[0].code, 'concept-0999');
  assert.equal(generated?.at(-1)?.valueInteger, 999);

  const paired = readFileSync(join(fixture, 'Observation.ndjson'), 'utf8')
    .trim().split('\n').map((line) => JSON.parse(line))
    .find((resource) => resource.id === 'dev-pair-001');
  assert.equal(paired.component[0].unmodeledSignal.flag, true);
  assert.equal(paired.component[0].extension[0].extension[0].valueString, 'nested-owner');
  assert.equal(paired.component[2].code.coding[0].code, 'shared');
  assert.equal(paired.component[2].valueString, undefined);
  assert.equal(paired.component[2]._valueString.extension[0].valueBoolean, true);
});

test('J01 fixture generation rejects invalid counts and incomplete identity metadata', () => {
  const fixture = mkdtempSync(join(tmpdir(), 'loom-j01-invalid-fixture-'));
  try {
    const specPath = join(fixture, 'j01-concepts.fixture.json');
    writeFileSync(specPath, JSON.stringify({ count: 0, system: 'urn:test', codePrefix: 'code-', displayPrefix: 'Concept' }));
    assert.throws(() => generatedJ01ConceptNDJSON(fixture), /count must be an integer/);
    writeFileSync(specPath, JSON.stringify({ count: 2, codePrefix: 'code-', displayPrefix: 'Concept' }));
    assert.throws(() => generatedJ01ConceptNDJSON(fixture), /requires system, codePrefix, and displayPrefix/);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('J01 semantic inventory requests use the saved snapshot and omit an absent cursor', () => {
  assert.deepEqual(j01SemanticInventoryRequest({
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    resourceType: 'Observation',
    query: 'J01 concept',
  }), {
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    resourceType: 'Observation',
    query: 'J01 concept',
    limit: 50,
  });
  assert.deepEqual(j01SemanticInventoryRequest({
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    cursor: 'cursor-2',
  }), {
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    cursor: 'cursor-2',
    limit: 50,
  });
  assert.throws(() => j01SemanticInventoryRequest({ rowRoot: 'observation-node' }), /requires a catalog snapshot/);
});

test('J01 semantic pagination retains one context identity and finds each expected code exactly once', async () => {
  const fixture = {
    count: 5,
    system: 'urn:loom:j01:catalog',
    codePrefix: 'concept-',
    displayPrefix: 'J01 concept',
  };
  const pages = [
    ['0000', '0001'],
    ['0002', '0003'],
    ['0004'],
  ];
  const requests = [];
  const inventory = await collectJ01SemanticConceptPages(async (body) => {
    requests.push(body);
    const pageIndex = requests.length - 1;
    return {
      response: { ok: true, status: 200 },
      value: {
        contextToken: 'ctx-1',
        buildId: 'build-1',
        state: 'complete',
        sourceAvailability: 'unknown',
        entries: pages[pageIndex].map((suffix) => ({
          conceptId: `concept-id-${suffix}`,
          bindingId: `binding-id-${suffix}`,
          resourceType: 'Observation',
          system: fixture.system,
          code: `${fixture.codePrefix}${suffix}`,
          display: `${fixture.displayPrefix} ${suffix}`,
        })),
        ...(pageIndex < pages.length - 1 ? { nextCursor: `cursor-${pageIndex + 1}` } : {}),
      },
    };
  }, {
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    resourceType: 'Observation',
    query: fixture.displayPrefix,
  }, fixture);

  assert.equal(inventory.count, 5);
  assert.equal(inventory.countBasis, 'exact-paginated');
  assert.equal(inventory.pages.length, 3);
  assert.equal(inventory.contextToken, 'ctx-1');
  assert.equal(inventory.buildId, 'build-1');
  assert.equal(inventory.sourceAvailability, 'unknown');
  assert.deepEqual(requests.map((request) => request.cursor), [undefined, 'cursor-1', 'cursor-2']);
  assert.deepEqual(inventory.entries.map((entry) => entry.code).sort(), [
    'concept-0000', 'concept-0001', 'concept-0002', 'concept-0003', 'concept-0004',
  ]);
});

test('J01 semantic pagination rejects failure, identity drift, duplicate pages, and examples', async (t) => {
  const fixture = { count: 2, system: 'urn:loom:j01:catalog', codePrefix: 'concept-', displayPrefix: 'J01 concept' };
  const request = { snapshotToken: 'snapshot-1', rowRoot: 'observation-node', query: fixture.displayPrefix };
  const entry = (suffix, extra = {}) => ({
    conceptId: `concept-id-${suffix}`,
    bindingId: `binding-id-${suffix}`,
    resourceType: 'Observation',
    system: fixture.system,
    code: `concept-${suffix}`,
    display: `J01 concept ${suffix}`,
    ...extra,
  });
  const response = (entries, nextCursor, overrides = {}) => ({
    response: { ok: true, status: 200 },
    value: {
      contextToken: 'ctx-1',
      buildId: 'build-1',
      state: 'complete',
      sourceAvailability: 'verified',
      entries,
      ...(nextCursor ? { nextCursor } : {}),
      ...overrides,
    },
  });

  await t.test('HTTP failure', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => ({ response: { ok: false, status: 503 }, value: {} }), request, fixture), /HTTP 503/);
  });
  await t.test('catalog context changes between pages', async () => {
    let page = 0;
    await assert.rejects(collectJ01SemanticConceptPages(async () => {
      page += 1;
      return page === 1
        ? response([entry('0000')], 'cursor-1')
        : response([entry('0001')], undefined, { contextToken: 'ctx-2' });
    }, request, fixture), /changed its context, build identity, or source availability/);
  });
  await t.test('source availability changes between pages', async () => {
    let page = 0;
    await assert.rejects(collectJ01SemanticConceptPages(async () => {
      page += 1;
      return page === 1
        ? response([entry('0000')], 'cursor-1')
        : response([entry('0001')], undefined, { sourceAvailability: 'unproven' });
    }, request, fixture), /changed its context, build identity, or source availability/);
  });
  await t.test('cursor repeats', async () => {
    let page = 0;
    await assert.rejects(collectJ01SemanticConceptPages(async () => {
      page += 1;
      return page === 1
        ? response([entry('0000')], 'cursor-1')
        : response([], 'cursor-1');
    }, request, fixture), /repeated a pagination cursor/);
  });
  await t.test('duplicate generated code', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => response([entry('0000'), entry('0000')]), request, fixture), /repeated concept identity/);
  });
  await t.test('example values leak into browse results', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => response([entry('0000', { examples: ['private value'] })]), request, fixture), /exposed example values/);
  });
  await t.test('global inventory count leaks into browse response', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => response([entry('0000'), entry('0001')], undefined, { totalCount: 2 }), request, fixture), /global count field/);
  });
  await t.test('missing expected concept identity', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => response([entry('0000')]), request, fixture), /returned 1 of 2 expected concepts/);
  });
});

test('J01 command evidence accepts only distinct compiler choice IDs for the selected table', () => {
  const body = {
    commandId: 'cmd-1',
    snapshotToken: 'snapshot-1',
    commands: [
      { type: 'APPLY_CONSTRUCTION_CHOICE', outputId: 'table-1', constructionChoice: { choiceId: 'choice-id', form: 'VALUE' }, title: 'valueInteger' },
      { type: 'APPLY_CONSTRUCTION_CHOICE', outputId: 'table-1', constructionChoice: { choiceId: 'choice-records', form: 'OWNER_RECORDS' }, title: 'shared' },
    ],
  };
  assert.deepEqual(j01ConstructionChoiceCommandIdentities(body, 'table-1'), [
    { choiceId: 'choice-id', form: 'VALUE', outputId: 'table-1', title: 'valueInteger' },
    { choiceId: 'choice-records', form: 'OWNER_RECORDS', outputId: 'table-1', title: 'shared' },
  ]);
  assert.throws(() => j01ConstructionChoiceCommandIdentities({ ...body, commands: [{ ...body.commands[0], fieldPath: 'valueInteger' }] }, 'table-1'), /client-derived source field/);
  assert.throws(() => j01ConstructionChoiceCommandIdentities(body, 'table-2'), /different table/);
  assert.throws(() => j01ConstructionChoiceCommandIdentities({ ...body, commands: [body.commands[0], body.commands[0]] }, 'table-1'), /distinct selected choices/);
  assert.throws(() => j01ConstructionChoiceCommandIdentities({ ...body, commands: [{ ...body.commands[0], constructionChoice: { choiceId: 'choice-id', form: 'VALUE', fieldPath: 'valueInteger' } }] }, 'table-1'), /only a compiler-issued choice identity and form/);
});

test('J01 reload identity snapshot retains exactly the renamed, reordered saved sources', () => {
  const state = {
    workspace: { documents: [{ output: { id: 'table-1' }, columns: [
      { column: 'col-id', label: 'Observation identifier', table: { order: 2 }, source: { kind: 'field', field: { path: 'root.id' } } },
      { column: 'col-int', label: 'Integer value', table: { order: 0 }, source: { kind: 'field', field: { path: 'valueInteger' } } },
      { column: 'col-owner', label: 'Study A records', table: { order: 1 }, source: { kind: 'ownerRecords', ownerRecords: { key: { system: 'urn:study:A', code: 'shared' }, binding: { ownerPath: 'component[]', valuePath: 'valueQuantity.value' } } } },
    ] }] },
  };
  const expected = [
    { columnId: 'col-int', label: 'Integer value', order: 0, source: { kind: 'field', path: 'valueInteger' } },
    { columnId: 'col-owner', label: 'Study A records', order: 1, source: { kind: 'ownerRecords', system: 'urn:study:A', code: 'shared', ownerPath: 'component[]', valuePath: 'valueQuantity.value' } },
    { columnId: 'col-id', label: 'Observation identifier', order: 2, source: { kind: 'field', path: 'id' } },
  ];
  assert.deepEqual(j01ColumnIdentitySnapshot(state, 'table-1'), expected);
  assert.throws(() => j01ColumnIdentitySnapshot({ workspace: { documents: [{ output: { id: 'table-1' }, columns: state.workspace.documents[0].columns.slice(1) }] } }, 'table-1'), /exactly three selected columns/);
  assert.throws(() => j01ColumnIdentitySnapshot({ workspace: { documents: [{ output: { id: 'table-1' }, columns: state.workspace.documents[0].columns.map((column) => column.column === 'col-id' ? { ...column, table: { order: 1 } } : column) }] } }, 'table-1'), /contiguous saved column positions/);
});

test('J02 fixture contains the documented five-edge FHIR reference chain and distinct report route', () => {
  const fixture = join(process.cwd(), 'testdata/devloop-fixture');
  const readResources = (filename) => readFileSync(join(fixture, filename), 'utf8')
    .trim()
    .split('\n')
    .map((line) => JSON.parse(line));
  const patient = readResources('Patient.ndjson').find((item) => item.id === 'dev-patient-001');
  const group = readResources('Group.ndjson').find((item) => item.id === 'dev-j02-group');
  const specimen = readResources('Specimen.ndjson').find((item) => item.id === 'dev-j02-specimen');
  const observation = readResources('Observation.ndjson').find((item) => item.id === 'dev-observation-001');
  const report = readResources('DiagnosticReport.ndjson').find((item) => item.id === 'dev-j02-report');
  const study = readResources('ResearchStudy.ndjson').find((item) => item.id === 'dev-j02-study');
  assert.ok(patient);
  assert.deepEqual(group?.member.map((member) => member.entity.reference), ['Patient/dev-patient-001']);
  assert.equal(specimen?.subject.reference, 'Group/dev-j02-group');
  assert.equal(observation?.specimen.reference, 'Specimen/dev-j02-specimen');
  assert.deepEqual(report?.result.map((result) => result.reference), ['Observation/dev-observation-001']);
  assert.equal(report?.subject.reference, 'Patient/dev-patient-001');
  assert.deepEqual(study?.result.map((result) => result.reference), ['DiagnosticReport/dev-j02-report']);
  assert.equal(study?.status, 'active');
  assert.equal(study?.title, 'J02 route study');

  const generated = readFileSync(join(process.cwd(), 'generated/fhirschema/generated.go'), 'utf8');
  for (const traversal of [
    'Patient|member_entity_Patient|Group',
    'Group|subject_Group|Specimen',
    'Specimen|specimen_Specimen|Observation',
    'Observation|result|DiagnosticReport',
    'DiagnosticReport|result_DiagnosticReport|ResearchStudy',
    'Patient|subject_Patient|DiagnosticReport',
  ]) assert.ok(generated.includes(`"${traversal}"`), `generated schema lacks ${traversal}`);

  const readme = readFileSync(join(fixture, 'README.md'), 'utf8');
  assert.match(readme, /Patient → Group → Specimen → Observation → DiagnosticReport →\s+ResearchStudy/);
  assert.match(readme, /semantically distinct routes/);
  assert.match(readme, /ResearchStudy\.title` is the\s+literal `J02 route study`/);
});

test('Docker Desktop host mount normalization accepts only this checkout', () => {
  const checkout = '/private/tmp/loom-devloop-impl.cDPSXd';
  assert.equal(sourceMountMatches(`${checkout}/cmd`, `${checkout}/cmd`), true);
  assert.equal(sourceMountMatches(`/host_mnt${checkout}/cmd`, `${checkout}/cmd`), true);
  assert.equal(sourceMountMatches('/host_mnt/private/tmp/another-checkout/cmd', `${checkout}/cmd`), false);
});

test('dataframe verification uses the versioned frontend output contract', () => {
  const request = graphQLRowsRequest(
    { fixtureProject: 'loom_dev_contract' },
    { recipe: 'cohort', translationVersion: 'v1', output: 'patients' },
    ['id', 'status'],
    [{ column: 'status', op: 'IN', value: ['active'] }],
  );
  assert.equal(
    request.query,
    'query VerifyRows($input: DataframeRowsInput!) { dataframeRows(input: $input) { materialization { id name revision projectId datasetGeneration state rowCount selector { recipe translationVersion output } } columns rows rowIds totalCount pageInfo { hasNextPage endCursor } } }',
  );
  assert.deepEqual(request.variables.input, {
    projectId: 'loom_dev_contract',
    selector: { recipe: 'cohort', translationVersion: 'v1', output: 'patients' },
    columns: ['id', 'status'],
    filters: [{ column: 'status', op: 'IN', value: ['active'] }],
    first: 25,
  });
});

test('J05 CSV artifact inspection preserves typed values, nulls, empty strings, and literal null markers', () => {
  const columns = [
    { name: 'patient_id', outputKey: 'patientId', logicalType: 'string', shape: 'scalar', sourcePath: 'id' },
    { name: 'age', outputKey: 'age', logicalType: 'integer', shape: 'scalar', nullable: false },
    { name: 'note', outputKey: 'note', logicalType: 'string', shape: 'scalar', nullable: true },
  ];
  const artifact = inspectJ05ArtifactPackage(j05ArtifactPackage({
    format: 'CSV',
    columns,
    rowCount: 3,
    data: 'patient_id,age,note\npatient-a,0,\\N\npatient-b,42,"\\N"\npatient-c,7,""\n',
  }));
  assert.equal(artifact.dataName, 'data.csv');
  assert.deepEqual(artifact.rows.map((row) => row.values), [
    { patient_id: 'patient-a', age: 0, note: null },
    { patient_id: 'patient-b', age: 42, note: '\\N' },
    { patient_id: 'patient-c', age: 7, note: '' },
  ]);
  assert.doesNotThrow(() => assertJ05ArtifactRows(artifact, [...artifact.rows].reverse()));
  assertJ05ArtifactIdentity(artifact, j05Identity);
});

test('J05 JSONL artifact inspection selects data.jsonl and preserves structured row IDs and native arrays', () => {
  const columns = [
    { name: 'patient_id', outputKey: 'patientId', logicalType: 'string', shape: 'scalar', sourcePath: 'id' },
    { name: 'family', outputKey: 'familyNames', logicalType: 'string', shape: 'array', repeated: true, sourcePath: 'name[].family' },
  ];
  const artifact = inspectJ05ArtifactPackage(j05ArtifactPackage({
    format: 'JSONL',
    columns,
    rowCount: 2,
    data: [
      JSON.stringify({ rowId: { groupId: 'group-a', revisionId: 'revision-a' }, values: { patientId: 'patient-a', familyNames: ['Example', 'Example-Smith'] } }),
      JSON.stringify({ rowId: { groupId: 'group-b', revisionId: 'revision-a' }, values: { patientId: 'patient-b', familyNames: ['Builder'] } }),
    ].join('\n') + '\n',
  }));
  assert.equal(artifact.dataName, 'data.jsonl');
  assert.equal(artifact.manifest.format, 'JSONL');
  assert.equal(artifact.rows.length, 2);
  assert.deepEqual(artifact.rows[0], {
    rowId: { groupId: 'group-a', revisionId: 'revision-a' },
    values: { patient_id: 'patient-a', family: ['Example', 'Example-Smith'] },
  });
  assert.equal(artifact.rows.some((row) => row.values.family === 'Example; Example-Smith'), false);
});

test('J01 artifact row reader maps CSV scalar columns by schema name to stable source IDs', () => {
  const columns = [
    { name: 'observation-id', outputKey: 'observationIdentifier', logicalType: 'string', shape: 'scalar', sourcePath: 'id' },
    { name: 'observation-status', outputKey: 'statusValue', logicalType: 'string', shape: 'scalar', sourcePath: 'status' },
  ];
  const artifact = inspectJ01ArtifactRows(j05ArtifactPackage({
    format: 'CSV', columns, rowCount: 1,
    rowIdentity: { key: '__loom_row_id', sourceResourceType: 'Observation', sourceIdColumn: 'observation-id' },
    data: 'observation-id,observation-status\nobs-stable-001,final\n',
  }), 'observation-id');
  assert.equal(artifact.dataName, 'data.csv');
  assert.deepEqual(artifact.rowsByID.get('obs-stable-001'), {
    'observation-id': 'obs-stable-001', 'observation-status': 'final',
  });
});

test('J01 artifact row reader maps JSONL output keys and preserves repeated owner objects', () => {
  const ownerRecords = [{
    status: 'VALUE', value: 0, unit: 'mmol/L', choiceArm: 'valueQuantity',
    codings: [{ system: 'urn:cda:meta', code: 'observation-component' }],
    source: { resourceType: 'Observation', resourceId: 'obs-stable-001', ownerPath: 'component[]', ownerOrdinal: 0 },
  }];
  const columns = [
    { name: 'observation-id', outputKey: 'fhirId', logicalType: 'string', shape: 'scalar', sourcePath: 'id' },
    { name: 'owner-records', outputKey: 'owners', logicalType: 'array', shape: 'array', repeated: true, sourcePath: 'component[]' },
  ];
  const artifact = inspectJ01ArtifactRows(j05ArtifactPackage({
    format: 'JSONL', columns, rowCount: 1,
    rowIdentity: { key: '__loom_row_id', sourceResourceType: 'Observation', sourceIdColumn: 'observation-id' },
    data: `${JSON.stringify({ rowId: { resourceId: 'obs-stable-001' }, values: { fhirId: 'obs-stable-001', owners: ownerRecords } })}\n`,
  }), 'observation-id');
  assert.equal(artifact.dataName, 'data.jsonl');
  assert.equal(artifact.rowsByID.get('obs-stable-001')['observation-id'], 'obs-stable-001');
  assert.deepEqual(artifact.rowsByID.get('obs-stable-001')['owner-records'], ownerRecords);
});

test('J05 prepared modal identity is rejected after publication generation or schema changes', () => {
  const prepared = { project: j05Identity.project, datasetGeneration: j05Identity.datasetGeneration, outputId: j05Identity.outputId, revisionId: j05Identity.revisionId, schemaDigest: j05Identity.schemaDigest };
  assert.equal(j05ArtifactIdentityIsCurrent(prepared, prepared), true);
  assert.equal(j05ArtifactIdentityIsCurrent(prepared, { ...prepared, datasetGeneration: 'fixture-next' }), false);
  assert.equal(j05ArtifactIdentityIsCurrent(prepared, { ...prepared, schemaDigest: 'schema-next' }), false);
  assert.equal(j05ArtifactIdentityIsCurrent(prepared, { ...prepared, revisionId: 'revision-next' }), false);
});

test('J05 Viewer transport values are decoded through the declared logical type', () => {
  assert.equal(normalizeJ05LogicalValue('2', { column: 'count', logicalType: 'integer' }), 2);
  assert.equal(normalizeJ05LogicalValue('false', { column: 'flag', logicalType: 'boolean' }), false);
  assert.equal(normalizeJ05LogicalValue('2.5', { column: 'value', logicalType: 'decimal' }), 2.5);
  assert.equal(normalizeJ05LogicalValue('002', { column: 'code', logicalType: 'string' }), '002');
  assert.throws(
    () => normalizeJ05LogicalValue('9007199254740993', { column: 'count', logicalType: 'integer' }),
    /exceeds JavaScript's exact integer range/,
  );
});

test('J05 artifact mismatches fail deterministically on publication identity and literal row values', () => {
  const artifact = inspectJ05ArtifactPackage(j05ArtifactPackage({
    format: 'CSV',
    columns: [{ name: 'patient_id', outputKey: 'patientId', logicalType: 'string', shape: 'scalar' }],
    rowCount: 1,
    data: 'patient_id\npatient-a\n',
  }));
  assert.throws(
    () => assertJ05ArtifactIdentity(artifact, { ...j05Identity, schemaDigest: 'schema-stale' }),
    (error) => error.message === 'J05 artifact schemaDigest differs from the current publication: expected "schema-stale", got "schema-j05"',
  );
  assert.throws(
    () => assertJ05ArtifactRows(artifact, [{ values: { patient_id: 'patient-b' } }]),
    (error) => error.message === 'J05 artifact literal rows differ from Preview/Viewer: expected [{"values":{"patient_id":"patient-b"}}], got [{"values":{"patient_id":"patient-a"}}]',
  );
});
