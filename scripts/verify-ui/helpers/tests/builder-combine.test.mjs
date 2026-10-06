import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { URL } from 'node:url';
import test from 'node:test';
import { registry, scenarioCaseFor } from '../../registry.mjs';
import {
  appendEditorConfigurationEvidence,
  appendNullPaddingRows,
  isOwnedConstructionCapabilitiesRequest,
  builderRequestURL,
  builderResponseIdentity,
  constructionProposalPreviewEvidence,
  currentPublishedRevisionForOutput,
  displayAppendNullPaddingRows,
  findColumn,
  isCombineInputIDColumn,
  isJoinableStringColumn,
  isMembershipWorkspaceKeyColumn,
  membershipGroupCapabilityKeyEvidence,
  membershipOutputNullabilityEvidence,
  isNumericClickHouseType,
  isScalarStringColumn,
  joinOracleRows,
  nativeCombineTargetBindingEvidence,
  rootedEmptyTargetRestorationEvidence,
  sameSourceDocuments,
  snapshotSourceDocument,
  summarizePublishedInputEntries,
  unwrapNullableClickHouseType,
  rootedEmptyTargetAppliedExpression,
} from '../builder-combine-helpers.mjs';

const parseCallArguments = (source, openParen) => {
  const args = [];
  let start = openParen + 1;
  const stack = [];
  let quote;
  let escaped = false;
  let lineComment = false;
  let blockComment = false;
  for (let index = start; index < source.length; index += 1) {
    const char = source[index];
    const next = source[index + 1];
    if (lineComment) {
      if (char === '\n') lineComment = false;
      continue;
    }
    if (blockComment) {
      if (char === '*' && next === '/') { blockComment = false; index += 1; }
      continue;
    }
    if (quote) {
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === quote) quote = undefined;
      continue;
    }
    if (char === '/' && next === '/') { lineComment = true; index += 1; continue; }
    if (char === '/' && next === '*') { blockComment = true; index += 1; continue; }
    if (char === "'" || char === '"' || char === '`') { quote = char; continue; }
    if (char === '(' || char === '[' || char === '{') { stack.push(char); continue; }
    if (char === ')' || char === ']' || char === '}') {
      if (char === ')' && stack.length === 0) {
        args.push(source.slice(start, index).trim());
        return args;
      }
      stack.pop();
      continue;
    }
    if (char === ',' && stack.length === 0) {
      args.push(source.slice(start, index).trim());
      start = index + 1;
    }
  }
  throw new Error('Unterminated check(report, ...) call in the Combine driver.');
};

test('construction capability diagnostics bind exact owned origin, project, explorer, and route', () => {
  const owner = { uiUrl: 'http://127.0.0.1:30008/', project: 'loom_dev_verify_owned', explorer: 'verify-owned-combine' };
  const requestURL = 'http://127.0.0.1:30008/api/v1/projects/loom_dev_verify_owned/explorers/verify-owned-combine/authoring/v2/construction-capabilities';
  assert.equal(isOwnedConstructionCapabilitiesRequest({ requestURL, method: 'POST', ...owner }), true);
  assert.equal(isOwnedConstructionCapabilitiesRequest({ requestURL: requestURL + '/extra', method: 'POST', ...owner }), false);
  assert.equal(isOwnedConstructionCapabilitiesRequest({ requestURL: requestURL.replace('verify-owned-combine', 'another-explorer'), method: 'POST', ...owner }), false);
  assert.equal(isOwnedConstructionCapabilitiesRequest({ requestURL: requestURL.replace('loom_dev_verify_owned', 'another-project'), method: 'POST', ...owner }), false);
  assert.equal(isOwnedConstructionCapabilitiesRequest({ requestURL: requestURL.replace('127.0.0.1:30008', '127.0.0.1:8188'), method: 'POST', ...owner }), false);
  assert.equal(isOwnedConstructionCapabilitiesRequest({ requestURL, method: 'GET', ...owner }), false);
});

test('APPEND capability diagnostics use the seeded project and always release listeners after the full lifecycle', () => {
  const source = readFileSync(new URL('../../workflows/builder-combine.mjs', import.meta.url), 'utf8');
  const appendWorkflow = source.indexOf('export const appendWorkflow = async ({ page, report, action }, context) =>');
  const capture = source.indexOf('captureConstructionCapabilitiesFailuresWithPlaywright(page, report', appendWorkflow);
  const lifecycle = source.indexOf('try {\n    const builderAtTarget', capture);
  const completedLifecycle = source.lastIndexOf('report.target.combineTarget = target;');
  const finalizer = source.indexOf('capabilitiesFailures.stop();', lifecycle);
  assert.ok(appendWorkflow >= 0 && capture > appendWorkflow && lifecycle > capture && completedLifecycle > lifecycle && finalizer > completedLifecycle);
  assert.doesNotMatch(source, /const runAppend\s*=/, 'APPEND must not remain an executable legacy runner path');
  assert.doesNotMatch(source, /runBuilderCombine|runJoin|runPlaywrightCase|executeScenario|parseArgs/,
    'Combine lifecycles must be driven through their native Playwright specifications');
  const officialSpec = readFileSync(new URL('../../specs/append.spec.mjs', import.meta.url), 'utf8');
  assert.match(officialSpec, /import \{ test \} from '\.\.\/helpers\/fixtures\.mjs'/);
  assert.match(officialSpec, /test\.use\(\{ scenarioID: 'builder-combine', caseName: 'append', fixtureDir: 'testdata\/verify-combine' \}\)/);
  assert.match(officialSpec, /appendWorkflow\(\{ page, report: workflow\.report, action: workflow\.action \}, loomContext\)/);
  assert.match(source.slice(capture, lifecycle), /project: context\.target\.fixtureProject/);
  assert.match(source.slice(lifecycle, finalizer), /const builderAtTarget = await readBuilder/);
  assert.match(source.slice(completedLifecycle, finalizer + 40), /finally/);
  const diagnosticsHelper = source.slice(source.indexOf('const captureConstructionCapabilitiesFailuresWithPlaywright'),
    source.indexOf('const captureOwnedConstructionProposals'));
  assert.match(diagnosticsHelper, /page\.off\('request'/);
  assert.match(diagnosticsHelper, /sanitizeBody/);
  assert.doesNotMatch(source.slice(0, source.indexOf('const expectedPatients')), /isDeepStrictEqual/);
});

test('KEY_JOIN lifecycle is exported for the native Playwright spec', () => {
  const source = readFileSync(new URL('../../workflows/builder-combine.mjs', import.meta.url), 'utf8');
  const joinWorkflow = source.indexOf('export const joinWorkflow = async ({ page, report, action }, context) =>');
  const appendWorkflow = source.indexOf('export const appendWorkflow = async ({ page, report, action }, context) =>');
  assert.ok(joinWorkflow >= 0 && appendWorkflow > joinWorkflow, 'Join lifecycle must be available before the APPEND workflow');
  const body = source.slice(joinWorkflow, appendWorkflow);
  for (const name of [
    'fixture contains one bootstrap Patient, four Observations, and three DiagnosticReports',
    'INNER preview returns the three exact rows matched on shared required IDs',
    'Canceling the LEFT edit leaves the saved INNER operation unchanged',
    'LEFT preview before Apply includes null projections for the unmatched row',
    "await removeCombineAndRestoreEmptyRootWithPlaywright(context, page, action, report, explorer, target, emptyTargetBaseline, step.id, 'KEY_JOIN');",
    'await assertSourceImmutability(context, explorer, docs, api, report);',
  ]) assert.ok(body.includes(name), `Join workflow must retain its literal lifecycle evidence: ${name}`);
  const officialSpec = readFileSync(new URL('../../specs/join.spec.mjs', import.meta.url), 'utf8');
  assert.match(officialSpec, /import \{ test \} from '\.\.\/helpers\/fixtures\.mjs'/);
  assert.match(officialSpec, /test\.use\(\{ scenarioID: 'builder-combine', caseName: 'join', fixtureDir: 'testdata\/verify-combine' \}\)/);
  assert.match(officialSpec, /joinWorkflow\(\{ page, report: workflow\.report, action: workflow\.action \}, loomContext\)/);
});

test('last-step removal waits for the exact selected empty target instead of nonexistent history', () => {
  const outputId = 'out_owned_target';
  const expression = rootedEmptyTargetAppliedExpression(outputId);
  const evaluate = (historyExists = false) => Function('document', 'return ' + expression)({
    querySelector(selector) {
      if (selector === '[data-testid="construction-table-' + outputId + '"]') return { getAttribute: (name) => name === 'aria-current' ? 'page' : null };
      if (selector === '[data-testid="preview-table-scroll"]') return { textContent: 'Add a column to see your table.' };
      if (historyExists && selector === '[data-testid="construction-history"]') return {};
      return null;
    },
  });
  assert.equal(evaluate(false), true);
  assert.equal(evaluate(true), false);
  assert.match(expression, /construction-proposal-panel/);
  assert.match(expression, /construction-combine-editor/);
});

test('reloaded last-step removal matches the full pre-Combine empty target document', () => {
  const target = { outputId: 'out_owned_target' };
  const baseline = {
    output: { id: target.outputId, title: 'Observation' },
    rootResourceType: 'Observation',
    columns: [],
    fixedFilters: [],
  };
  const restored = { ...structuredClone(baseline), construction: { version: 1, steps: [] } };
  assert.deepEqual(rootedEmptyTargetRestorationEvidence(restored, baseline, target), {
    ok: true, emptyRoot: true, unchanged: true, emptyConstructionNormalized: true,
  });
  const changedSemanticField = structuredClone(baseline);
  changedSemanticField.fixedFilters.push({ path: 'status', values: ['final'] });
  assert.deepEqual(rootedEmptyTargetRestorationEvidence(changedSemanticField, baseline, target), {
    ok: false, emptyRoot: true, unchanged: false, emptyConstructionNormalized: false,
  });
  assert.deepEqual(rootedEmptyTargetRestorationEvidence(structuredClone(baseline), baseline, target), {
    ok: true, emptyRoot: true, unchanged: true, emptyConstructionNormalized: false,
  });
  const wrongRoot = { ...structuredClone(baseline), rootResourceType: 'Patient' };
  assert.equal(rootedEmptyTargetRestorationEvidence(wrongRoot, baseline, target).ok, false);
  const unknownConstructionField = { ...structuredClone(baseline), construction: { version: 1, steps: [], mode: 'changed' } };
  assert.equal(rootedEmptyTargetRestorationEvidence(unknownConstructionField, baseline, target).ok, false);
  const changedConstructionVersion = { ...structuredClone(baseline), construction: { version: 2, steps: [] } };
  assert.equal(rootedEmptyTargetRestorationEvidence(changedConstructionVersion, baseline, target).ok, false);
});

const combineCheckCalls = (source) => {
  const calls = [];
  const pattern = /\bcheck\s*\(\s*report\s*,/g;
  for (const match of source.matchAll(pattern)) {
    const openParen = source.indexOf('(', match.index);
    calls.push(parseCallArguments(source, openParen));
  }
  return calls;
};

test('Combine registry requires the observed APPEND null assertion and retains Join input pin coverage', () => {
  const scenario = registry.find((entry) => entry.id === 'builder-combine');
  const joinChecks = scenarioCaseFor(scenario, 'join').requiredChecks;
  const appendChecks = scenarioCaseFor(scenario, 'append').requiredChecks;
  assert.ok(joinChecks.includes('native Combine inputs pin the exact current Observation and DiagnosticReport revisions'));
  assert.ok(joinChecks.includes('source schemas expose compatible nullable scalar ID keys, scalar status fields, and a numeric Observation value'));
  assert.equal(joinChecks.includes('saved Combine step refers to the exact published source revisions'), false);
  assert.ok(appendChecks.includes('APPEND proposal response has literal JSON nulls at exactly omitted source positions'));
  assert.ok(appendChecks.includes('APPEND editor has exactly three named output rows with the intended mappings before preview'));
  assert.ok(appendChecks.includes('APPEND editor requires explicit Empty for this table choices while blank mappings stay unconfigured'));
  assert.ok(appendChecks.includes('editing a saved APPEND reconstructs each omitted mapping as explicit Empty for this table'));
  assert.equal(appendChecks.includes('APPEND preview contains nulls exactly in the fields absent from each source schema'), false);
  assert.ok(joinChecks.includes('open Combine operation chooser action-to-render within budget'));
  assert.ok(joinChecks.includes('open saved Combine editor and load pinned sources action-to-render within budget'));
  assert.ok(joinChecks.includes('choose KEY_JOIN and load the initial two input selectors action-to-render within budget'));
  assert.ok(appendChecks.includes('open Combine operation chooser action-to-render within budget'));
  assert.ok(appendChecks.includes('choose APPEND and load the initial two input selectors action-to-render within budget'));
  assert.ok(appendChecks.includes('add APPEND input table 3 action-to-render within budget'));
  assert.ok(appendChecks.includes('open saved Combine editor and load pinned sources action-to-render within budget'));
});

test('every Combine check call has a valid dimension and a separate assertion name', () => {
  const source = readFileSync(new URL('../../workflows/builder-combine.mjs', import.meta.url), 'utf8');
  const calls = combineCheckCalls(source);
  assert.ok(calls.length > 20, 'the source audit should inspect the complete Combine check surface');
  for (const [index, args] of calls.entries()) {
    const dimension = /^(['"])(correctness|persistence|usability|performance)\1$/.exec(args[1] ?? '');
    assert.ok(dimension, `check call ${index + 1} has an invalid or shifted dimension: ${args.join(' | ')}`);
    assert.ok(args.length === 4 || args.length === 5,
      `check call ${index + 1} must separate name, condition, and optional evidence: ${args.join(' | ')}`);
    assert.ok(args[2] && !/^(true|false|null|undefined|\d+)\b/.test(args[2]),
      `check call ${index + 1} has no assertion name: ${args.join(' | ')}`);
    assert.ok(args[3], `check call ${index + 1} has no condition: ${args.join(' | ')}`);
  }
  for (const name of [
    'APPEND editor requires explicit Empty for this table choices while blank mappings stay unconfigured',
    'editing a saved APPEND reconstructs each omitted mapping as explicit Empty for this table',
    'APPEND proposal response has literal JSON nulls at exactly omitted source positions',
  ]) {
    assert.ok(source.includes("'" + name + "'"), `required Combine assertion must retain its literal name: ${name}`);
  }
  assert.match(source, /map\(cell\s*=>\s*tidy\(cell\.textContent\)\)/,
    'saved preview headers must use semantic textContent, not CSS-uppercased innerText');
  assert.ok(source.includes("page.getByRole('button', { name: 'Add another table', exact: true })"),
    'APPEND inputs beyond the initial two must use the unique native Add another table control');
  assert.ok(source.includes("page.locator('select[aria-label=\"Input table 1\"]')") &&
    source.includes("page.locator('select[aria-label=\"Input table 2\"]')"),
    'operation selection must wait for the two exact slots the editor initially renders');
  assert.ok(source.includes('`add APPEND input table ${index + 1}`'),
    'every added APPEND input slot must have its own registered render timing');
});

test('saved Combine edit timing waits for controls outside inherited disabled fieldsets', () => {
  const source = readFileSync(new URL('../../workflows/builder-combine.mjs', import.meta.url), 'utf8');
  const start = source.indexOf('const editSavedStepWithPlaywright =');
  const readiness = source.slice(start, source.indexOf('\nconst createAndPublishSourcesWithPlaywright', start));
  assert.ok(readiness.includes("'open saved Combine editor and load pinned sources'"));
  assert.match(readiness, /select\[aria-label="Input table 1"\]/);
  assert.match(readiness, /input\[aria-label="Output field 1 label"\]/);
  assert.match(readiness, /waitForFunction\(\(\) => !document\.querySelector\('select\[aria-label="Input table 1"\]'\)\?\.disabled/,
    'saved editor readiness must wait until inherited disabled state clears');
  assert.match(source.slice(source.indexOf('export const joinWorkflow ='), source.indexOf('const assertAppendNullPaddingStep')),
    /budget: 5000/, 'Playwright user actions must retain the five-second render budget');
});

test('published column matcher prefers the exact semantic field and rejects ambiguity', () => {
  const exact = {
    id: 'observation-status', name: 'status', label: 'Status', semanticPath: 'Observation.status',
    clickhouseType: 'String', nullable: false, repeated: false,
  };
  const revision = {
    outputId: 'observations',
    columns: [
      { ...exact, id: 'display-status', semanticPath: 'Observation.display' },
      exact,
    ],
  };
  assert.equal(findColumn(revision, 'Observation', 'status'), exact);
  assert.throws(() => findColumn({
    outputId: 'ambiguous',
    columns: [exact, { ...exact, id: 'other-status', name: 'status_copy' }],
  }, 'Observation', 'status'), /Could not identify one published Observation\.status column/);
});

test('Builder API identity binds the scoped request route to the returned workspace and source outputs', () => {
  const apiBase = 'http://127.0.0.1:30008';
  const project = 'loom_dev_verify_123';
  const explorer = 'verify-123-combine';
  const title = 'Verify 123 combine';
  const builder = {
    apiVersion: 'loom.calypr.org/explorer-authoring/v2',
    catalog: { generation: 'generation-123', authorizationScopeDigest: 'scope-123' },
    draftDigest: 'draft-123',
    draftVersion: 2,
    kind: 'ExplorerBuilderState',
    lifecycleState: 'READY',
    previousDraftRevisionId: 'draft-revision-previous',
    workspace: {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderWorkspace',
      semanticsVersion: 10,
      explorer: { title },
      documents: [
        { rootResourceType: 'Observation', output: { id: 'out-observations', title: 'Observations' } },
        { rootResourceType: 'DiagnosticReport', output: { id: 'out-reports', title: 'Diagnostic reports' } },
      ],
      tabs: [],
    },
  };
  const expectedSources = [
    { rootResourceType: 'Observation', title: 'Observations', outputId: 'out-observations' },
    { rootResourceType: 'DiagnosticReport', title: 'Diagnostic reports', outputId: 'out-reports' },
  ];
  const route = builderRequestURL(apiBase, project, explorer);
  assert.equal(Object.hasOwn(builder, 'explorerId'), false);
  assert.equal(route.pathname, `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/builder`);
  const identity = builderResponseIdentity(builder, apiBase, project, explorer, title, expectedSources);
  assert.equal(identity.bound, true);
  assert.deepEqual(identity.outputIDs, ['out-observations', 'out-reports']);
  assert.equal(builderResponseIdentity(builder, apiBase, project, explorer, 'different workspace', expectedSources).bound, false);
  assert.equal(builderResponseIdentity(builder, apiBase, project, explorer, title,
    [{ ...expectedSources[0], outputId: 'wrong-output' }, expectedSources[1]]).bound, false);
});

test('KEY_JOIN accepts only scalar String keys with type and nullability metadata in agreement', () => {
  const key = { clickhouseType: 'String', nullable: false, repeated: false };
  assert.equal(isJoinableStringColumn(key), true);
  assert.equal(isJoinableStringColumn({ clickhouseType: 'Nullable(String)', nullable: true, repeated: false }), true);
  assert.equal(isJoinableStringColumn({ ...key, nullable: true }), false);
  assert.equal(isJoinableStringColumn({ clickhouseType: 'Nullable(String)', nullable: false, repeated: false }), false);
  assert.equal(isJoinableStringColumn({ ...key, repeated: true }), false);
  assert.equal(isJoinableStringColumn({ ...key, clickhouseType: 'Nullable(UInt64)', nullable: true }), false);
});

test('nullable scalar status projections retain consistent published type and nullability metadata', () => {
  assert.equal(isScalarStringColumn({ clickhouseType: 'String', nullable: false, repeated: false }), true);
  assert.equal(isScalarStringColumn({ clickhouseType: 'Nullable(String)', nullable: true, repeated: false }), true);
  assert.equal(isScalarStringColumn({ clickhouseType: 'Nullable(String)', nullable: false, repeated: false }), false);
  assert.equal(isScalarStringColumn({ clickhouseType: 'String', nullable: false, repeated: true }), false);
});

test('APPEND, KEY_JOIN, and MEMBERSHIP accept only consistently declared scalar String nullability', () => {
  const nullableIdentity = { clickhouseType: 'Nullable(String)', nullable: true, repeated: false };
  const requiredIdentity = { clickhouseType: 'String', nullable: false, repeated: false };
  assert.equal(isCombineInputIDColumn(nullableIdentity, 'APPEND'), true);
  assert.equal(isCombineInputIDColumn(nullableIdentity, 'KEY_JOIN'), true);
  assert.equal(isCombineInputIDColumn(requiredIdentity, 'KEY_JOIN'), true);
  assert.equal(isCombineInputIDColumn(requiredIdentity, 'MEMBERSHIP'), true);
  assert.equal(isCombineInputIDColumn(nullableIdentity, 'MEMBERSHIP'), true);
  assert.equal(isCombineInputIDColumn(nullableIdentity, 'UNKNOWN'), false);
  assert.equal(isCombineInputIDColumn({ ...nullableIdentity, repeated: true }, 'APPEND'), false);
  assert.equal(isCombineInputIDColumn({ ...nullableIdentity, repeated: true }, 'KEY_JOIN'), false);
  assert.equal(isCombineInputIDColumn({ ...nullableIdentity, nullable: false }, 'MEMBERSHIP'), false);
  assert.equal(isCombineInputIDColumn({ ...requiredIdentity, nullable: true }, 'MEMBERSHIP'), false);
  assert.equal(isCombineInputIDColumn({ ...nullableIdentity, clickhouseType: 'Nullable(UInt64)' }, 'MEMBERSHIP'), false);
  assert.equal(isCombineInputIDColumn({ ...nullableIdentity, repeated: true }, 'MEMBERSHIP'), false);
});

test('current-draft MEMBERSHIP accepts only compatible scalar String columns with boolean nullability metadata', () => {
  const required = { logicalType: 'string', cardinality: 'required_one', nullable: false, joinCompatibilityKey: 'String' };
  const optional = { logicalType: 'string', cardinality: 'optional_one', nullable: true, joinCompatibilityKey: 'String' };
  assert.equal(isMembershipWorkspaceKeyColumn(required), true);
  assert.equal(isMembershipWorkspaceKeyColumn(optional), true);
  assert.equal(isMembershipWorkspaceKeyColumn({ ...optional, nullable: false }), true);
  assert.equal(isMembershipWorkspaceKeyColumn({ ...required, nullable: true }), true);
  assert.equal(isMembershipWorkspaceKeyColumn({ ...optional, logicalType: 'integer' }), false);
  assert.equal(isMembershipWorkspaceKeyColumn({ ...optional, joinCompatibilityKey: 'UInt64' }), false);
  assert.equal(isMembershipWorkspaceKeyColumn({ ...optional, cardinality: 'optional_many' }), false);
  assert.equal(isMembershipWorkspaceKeyColumn({ ...optional, cardinality: 'many' }), false);
  assert.equal(isMembershipWorkspaceKeyColumn({ ...optional, nullable: undefined }), false);
});

test('MEMBERSHIP uses compiled Group-key nullability when the authored output omits it and binds by exact column ID', () => {
  const authoredGroupOutput = { id: 'group-key-id', name: 'col_observation_id', label: 'Observation ID' };
  assert.equal(Object.hasOwn(authoredGroupOutput, 'nullable'), false,
    'Group key output nullable is an optional author declaration, not compiled schema metadata');

  const compiledCapabilityColumn = {
    id: 'group-key-id', name: 'col_observation_id', label: 'Observation ID',
    logicalType: 'string', cardinality: 'optional_one', nullable: true, joinCompatibilityKey: 'String',
  };
  const matched = membershipGroupCapabilityKeyEvidence([compiledCapabilityColumn], authoredGroupOutput.id);
  assert.equal(matched.ok, true);
  assert.equal(matched.exactOutputColumnId, true);
  assert.equal(matched.compiledNullable, true);

  const wrongID = membershipGroupCapabilityKeyEvidence(
    [{ ...compiledCapabilityColumn, id: 'different-group-key-id' }], authoredGroupOutput.id,
  );
  assert.equal(wrongID.ok, false, 'a same-label capability column with a different ID must not bind');
  assert.equal(wrongID.exactOutputColumnId, false);

  assert.deepEqual(membershipOutputNullabilityEvidence({ type: 'string', nullable: true }, matched.compiledNullable), {
    ok: true, sourceCompiledNullable: true, outputNullable: true,
  });
  assert.equal(membershipOutputNullabilityEvidence({ type: 'string', nullable: false }, matched.compiledNullable).ok, false);
  assert.equal(membershipOutputNullabilityEvidence({ type: 'string' }, matched.compiledNullable).ok, false);
  assert.deepEqual(membershipOutputNullabilityEvidence({ type: 'string' }, false), {
    ok: true, sourceCompiledNullable: false, outputNullable: false,
  }, 'an omitted authoring bool encodes false under the Go JSON omitempty contract');
  assert.equal(membershipOutputNullabilityEvidence({ type: 'string', nullable: null }, false).ok, false);
  assert.equal(membershipOutputNullabilityEvidence(undefined, false).ok, false);
});

test('numeric type detection unwraps actual Nullable(...) ClickHouse declarations', () => {
  assert.equal(unwrapNullableClickHouseType('Nullable(Int64)'), 'Int64');
  assert.equal(unwrapNullableClickHouseType('Int64'), 'Int64');
  assert.equal(isNumericClickHouseType('Nullable(Int64)'), true);
  assert.equal(isNumericClickHouseType('Nullable(Float64)'), true);
  assert.equal(isNumericClickHouseType('Nullable(Decimal(10,2))'), true);
  assert.equal(isNumericClickHouseType('Nullable(String)'), false);
});

test('fixture key matcher yields exact duplicate INNER pairs and unmatched LEFT row', () => {
  const observations = [
    { id: 'o1', status: 'final' },
    { id: 'o2', status: 'final' },
    { id: 'o3', status: 'preliminary' },
    { id: 'o4', status: 'unknown' },
  ];
  const reports = [
    { id: 'r1', status: 'final' },
    { id: 'r2', status: 'final' },
    { id: 'r3', status: 'preliminary' },
  ];
  assert.deepEqual(joinOracleRows(observations, reports, 'INNER', 'status', 'status'), [
    ['o1', 'final', 'r1', 'final'], ['o1', 'final', 'r2', 'final'],
    ['o2', 'final', 'r1', 'final'], ['o2', 'final', 'r2', 'final'],
    ['o3', 'preliminary', 'r3', 'preliminary'],
  ]);
  assert.deepEqual(joinOracleRows(observations, reports, 'LEFT', 'status', 'status'), [
    ['o1', 'final', 'r1', 'final'], ['o1', 'final', 'r2', 'final'],
    ['o2', 'final', 'r1', 'final'], ['o2', 'final', 'r2', 'final'],
    ['o3', 'preliminary', 'r3', 'preliminary'], ['o4', 'unknown', '—', '—'],
  ]);
});

test('three-source APPEND fixture oracle preserves the full row union and exact null padding', () => {
  const fixtureDir = new URL('../../../../testdata/verify-combine/', import.meta.url);
  const readRows = (name) => readFileSync(new URL(name, fixtureDir), 'utf8')
    .split(/\r?\n/)
    .filter(Boolean)
    .map((line) => JSON.parse(line));
  const rows = appendNullPaddingRows({
    observations: readRows('Observation.ndjson'),
    diagnosticReports: readRows('DiagnosticReport.ndjson'),
    patients: readRows('Patient.ndjson'),
  });
  assert.deepEqual(rows, [
    ['combine-observation-final-1', 'final', null],
    ['combine-observation-final-2', 'final', null],
    ['combine-observation-preliminary', 'preliminary', null],
    ['combine-observation-unmatched', 'unknown', null],
    ['combine-observation-final-1', 'final', null],
    ['combine-observation-final-2', 'final', null],
    ['combine-observation-preliminary', 'preliminary', null],
    ['combine-fixture-patient', null, 'female'],
  ]);
  assert.equal(rows.length, 4 + 3 + 1);
  assert.deepEqual(displayAppendNullPaddingRows(rows), [
    ['combine-observation-final-1', 'final', '—'],
    ['combine-observation-final-2', 'final', '—'],
    ['combine-observation-preliminary', 'preliminary', '—'],
    ['combine-observation-unmatched', 'unknown', '—'],
    ['combine-observation-final-1', 'final', '—'],
    ['combine-observation-final-2', 'final', '—'],
    ['combine-observation-preliminary', 'preliminary', '—'],
    ['combine-fixture-patient', '—', 'female'],
  ]);
});

test('APPEND proposal response proves raw null padding and binds it to the candidate request and receipt', () => {
  const expectedRows = [
    ['obs-1', 'final', null],
    ['report-1', 'preliminary', null],
    ['patient-1', null, 'female'],
  ];
  const requestBody = {
    outputId: 'out-target', snapshotToken: 'snapshot-1', expectedDraftVersion: 4, expectedDraftDigest: 'draft-4',
    candidateConstruction: { version: 1, steps: [{ id: 'append-1', operation: { combine: { kind: 'APPEND' } } }] },
  };
  const response = {
    proposalId: 'proposal-1', outputId: 'out-target', snapshotToken: 'snapshot-1', draftVersion: 4,
    draftDigest: 'draft-4', candidateConstruction: structuredClone(requestBody.candidateConstruction),
    previewStatus: 'READY', preview: {
      receiptId: 'proposal-1', outputId: 'out-target', rowCount: 3,
      columns: [{ column: 'record_id' }, { column: 'status' }, { column: 'patient_gender' }],
      rows: [
        { record_id: 'obs-1', status: 'final', patient_gender: null },
        { record_id: 'report-1', status: 'preliminary', patient_gender: null },
        { record_id: 'patient-1', status: null, patient_gender: 'female' },
      ],
    },
  };
  const evidence = constructionProposalPreviewEvidence({
    responseStatus: 200, response, requestBody, expectedOutputId: 'out-target',
    expectedColumns: ['record_id', 'status', 'patient_gender'], expectedRows,
    domProposalId: 'proposal-1', domReceiptId: 'proposal-1',
  });
  assert.equal(evidence.ok, true);
  assert.deepEqual(evidence.rawRows, expectedRows);
  assert.deepEqual(evidence.nullPositions, [[0, 2], [1, 2], [2, 1]]);

  const reordered = structuredClone(response);
  reordered.preview.rows.reverse();
  const checkResponse = (candidate, rows = expectedRows) => constructionProposalPreviewEvidence({
    responseStatus: 200, response: candidate, requestBody, expectedOutputId: 'out-target',
    expectedColumns: ['record_id', 'status', 'patient_gender'], expectedRows: rows,
    domProposalId: 'proposal-1', domReceiptId: 'proposal-1',
  }).ok;
  assert.equal(checkResponse(reordered), true, 'an unordered APPEND must match the exact tuple multiset');
  const missingField = structuredClone(response);
  delete missingField.preview.rows[0].patient_gender;
  assert.equal(checkResponse(missingField), false, 'a missing field must not satisfy a literal null');
  const duplicated = structuredClone(response);
  duplicated.preview.rows.push(structuredClone(duplicated.preview.rows[0]));
  duplicated.preview.rowCount = 4;
  const expectedDuplicates = [...expectedRows, expectedRows[0]];
  assert.equal(checkResponse(duplicated, expectedDuplicates), true);
  duplicated.preview.rows[3] = structuredClone(duplicated.preview.rows[1]);
  assert.equal(checkResponse(duplicated, expectedDuplicates), false, 'duplicate multiplicity must remain exact');


  const renderedDash = structuredClone(response);
  renderedDash.preview.rows[0].patient_gender = '—';
  assert.equal(constructionProposalPreviewEvidence({
    responseStatus: 200, response: renderedDash, requestBody, expectedOutputId: 'out-target',
    expectedColumns: ['record_id', 'status', 'patient_gender'], expectedRows,
    domProposalId: 'proposal-1', domReceiptId: 'proposal-1',
  }).ok, false, 'a displayed em dash must not satisfy the raw JSON null oracle');
  assert.equal(constructionProposalPreviewEvidence({
    responseStatus: 200, response, requestBody, expectedOutputId: 'different-output',
    expectedColumns: ['record_id', 'status', 'patient_gender'], expectedRows,
    domProposalId: 'proposal-1', domReceiptId: 'proposal-1',
  }).ok, false, 'a preview from another output must not satisfy the candidate request');
  const wrongReceipt = structuredClone(response);
  wrongReceipt.preview.receiptId = 'other-receipt';
  assert.equal(constructionProposalPreviewEvidence({
    responseStatus: 200, response: wrongReceipt, requestBody, expectedOutputId: 'out-target',
    expectedColumns: ['record_id', 'status', 'patient_gender'], expectedRows,
    domProposalId: 'proposal-1', domReceiptId: 'other-receipt',
  }).ok, false, 'a preview receipt detached from the candidate proposal must fail');
  const wrongCandidate = structuredClone(response);
  wrongCandidate.candidateConstruction.steps[0].id = 'other-step';
  assert.equal(constructionProposalPreviewEvidence({
    responseStatus: 200, response: wrongCandidate, requestBody, expectedOutputId: 'out-target',
    expectedColumns: ['record_id', 'status', 'patient_gender'], expectedRows,
    domProposalId: 'proposal-1', domReceiptId: 'proposal-1',
  }).ok, false, 'a response for a different candidate construction must fail');
});

test('published input evidence reports exact output, revision, and current markers from the catalog shape', () => {
  const entries = [
    { tableId: 'project:g:explorer:v1:out-observations', revisionId: 'exec-current', outputId: 'out-observations', isCurrent: true, tableTitle: 'Explorer', outputTitle: 'out-observations' },
    { tableId: 'project:g:explorer:v1:out-reports', revisionId: 'exec-old', outputId: 'out-reports', isCurrent: false, tableTitle: 'Explorer', outputTitle: 'out-reports' },
    { tableId: 'project:g:other:v1:other', revisionId: 'exec-other', outputId: 'other', isCurrent: true },
  ];
  assert.deepEqual(summarizePublishedInputEntries(entries, ['out-observations', 'out-reports']), {
    expectedOutputIDs: ['out-observations', 'out-reports'],
    entryCount: 3,
    matchingEntries: [
      { tableId: entries[0].tableId, revisionId: 'exec-current', outputId: 'out-observations', isCurrent: true, tableTitle: 'Explorer', outputTitle: 'out-observations' },
      { tableId: entries[1].tableId, revisionId: 'exec-old', outputId: 'out-reports', isCurrent: false, tableTitle: 'Explorer', outputTitle: 'out-reports' },
    ],
    catalogOutputIDs: ['out-observations', 'out-reports', 'other'],
  });
});

test('current published revision lookup preserves catalog identity evidence when no current output matches', () => {
  const entries = [
    { tableId: 'project:g:explorer:v1:out-observations', revisionId: 'exec-old', outputId: 'out-observations', isCurrent: false, tableTitle: 'Explorer', outputTitle: 'out-observations' },
    { tableId: 'project:g:explorer:v1:other', revisionId: 'exec-other', outputId: 'other', isCurrent: true },
  ];
  assert.throws(() => currentPublishedRevisionForOutput(entries, 'out-observations'), (error) => {
    assert.match(error.message, /Expected one current published revision for output out-observations; found 0/);
    assert.match(error.message, /"revisionId":"exec-old"/);
    assert.match(error.message, /"isCurrent":false/);
    assert.match(error.message, /"catalogOutputIDs":\["out-observations","other"\]/);
    return true;
  });
  assert.equal(currentPublishedRevisionForOutput([
    { ...entries[0], isCurrent: true },
  ], 'out-observations').revisionId, 'exec-old');
});

test('source immutability comparison detects changes to semantic fields omitted by the old projection snapshot', () => {
  const document = {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'observations', title: 'Observations' },
    rootResourceType: 'Observation',
    population: { kind: 'ALL' },
    route: { kind: 'root', resourceType: 'Observation' },
    rows: { kind: 'RECORDS' },
    columns: [],
    fixedFilters: [{ column: 'status', values: ['final'] }],
    actions: [{ type: 'DOWNLOAD', title: 'Export' }],
  };
  const before = snapshotSourceDocument(document);
  const after = snapshotSourceDocument(document);
  assert.equal(sameSourceDocuments(after, before), true);
  after.fixedFilters[0].values[0] = 'preliminary';
  assert.equal(sameSourceDocuments(after, before), false);
});
test('three-input APPEND configures its retained rows and verifies the exact mapping matrix before preview', () => {
  const source = readFileSync(new URL('../../workflows/builder-combine.mjs', import.meta.url), 'utf8');
  const removal = source.indexOf("const removeNumeric = page.getByRole('button', { name: 'Remove output field 3'");
  const preview = source.indexOf("'automatically preview three-input APPEND with explicit absent-field mappings'");
  assert.ok(removal >= 0 && preview > removal, 'the numeric-field removal must precede the APPEND preview');
  const setup = source.slice(removal, preview);
  assert.match(setup, /await configureOutputWithPlaywright\(page, action, 1, 'record_id'/);
  assert.match(setup, /await configureOutputWithPlaywright\(page, action, 2, 'status'/);
  assert.match(setup, /await addOutputWithPlaywright\(page, action, 3, 'patient_gender'/);
  assert.doesNotMatch(setup, /await addOutputWithPlaywright\(page, action, [12],/,
    'retained output rows must be configured without appending duplicate blank rows');
  assert.match(setup, /APPEND editor has exactly three named output rows with the intended mappings before preview/);
});

test('APPEND output configuration evidence rejects duplicate blank rows and incorrect sparse mappings', () => {
  const expected = [
    { name: 'record_id', label: 'Record ID', mappings: ['column:observation-id', 'column:report-id', 'column:patient-id'] },
    { name: 'status', label: 'Status', mappings: ['column:observation-status', 'column:report-status', 'empty-for-this-table'] },
    { name: 'patient_gender', label: 'Patient gender', mappings: ['empty-for-this-table', 'empty-for-this-table', ''] },
  ];
  assert.equal(appendEditorConfigurationEvidence(expected, expected).ok, true);
  assert.equal(appendEditorConfigurationEvidence([...expected, { name: '', label: '', mappings: ['', '', ''] }], expected).ok, false);
  assert.equal(appendEditorConfigurationEvidence(
    expected.map((row, index) => index === 1 ? { ...row, mappings: [...row.mappings.slice(0, 2), ''] } : row),
    expected,
  ).ok, false);
});

test('native Combine verifier captures returned target identity instead of relying on a visible table tab or title', () => {
  const source = readFileSync(new URL('../../workflows/builder-combine.mjs', import.meta.url), 'utf8');
  assert.match(source, /page\.waitForResponse\(response => createRequest\(response\.request\(\)\)/);
  assert.match(source, /request\.postDataJSON\(\)/);
  assert.match(source, /nativeCombineTargetBindingEvidence\(\{/);
  assert.match(source, /data-operation-family="COMBINE"/);
  assert.doesNotMatch(source, /const selectedTarget = async/);
  assert.doesNotMatch(source, /target\.title\.toLowerCase\(\)/);
  assert.doesNotMatch(source, /Network\.requestWillBeSent|Network\.getResponseBody/);
});

test('native Combine target identity binds its CREATE_TABLE command, returned workspace, and mounted editor', () => {
  const requestBody = {
    commandId: 'native-combine-command',
    commands: [{ type: 'CREATE_TABLE', title: 'Guessed title is irrelevant', rootNodeId: 'observation-root-node' }],
  };
  const response = {
    commandId: 'native-combine-command',
    results: [{ type: 'TABLE_CREATED', outputId: 'observations_combined' }],
    workspace: { documents: [{
      output: { id: 'observations_combined', title: 'Any server title' },
      rootResourceType: 'Observation',
      columns: [],
      construction: { steps: [] },
    }] },
  };
  const evidence = nativeCombineTargetBindingEvidence({
    requestBody,
    responseStatus: 200,
    response,
    expectedRootNodeIds: ['observation-root-node'],
    expectedRootResourceType: 'Observation',
    previousOutputIds: ['observations', 'reports'],
    mountedOutputId: 'observations_combined',
  });
  assert.equal(evidence.ok, true);
  assert.equal(evidence.outputId, 'observations_combined');

  assert.equal(nativeCombineTargetBindingEvidence({
    requestBody, responseStatus: 200, response,
    expectedRootNodeIds: ['other-root-node'],
    expectedRootResourceType: 'Observation',
    previousOutputIds: ['observations', 'reports'], mountedOutputId: 'observations_combined',
  }).ok, false);
  assert.equal(nativeCombineTargetBindingEvidence({
    requestBody, responseStatus: 200, response,
    expectedRootNodeIds: ['observation-root-node'],
    expectedRootResourceType: 'Observation',
    previousOutputIds: ['observations', 'reports'], mountedOutputId: 'observations',
  }).ok, false);
  assert.equal(nativeCombineTargetBindingEvidence({
    requestBody, responseStatus: 200, response: { ...response, workspace: { documents: [] } },
    expectedRootNodeIds: ['observation-root-node'],
    expectedRootResourceType: 'Observation',
    previousOutputIds: ['observations', 'reports'], mountedOutputId: 'observations_combined',
  }).ok, false);
  assert.equal(nativeCombineTargetBindingEvidence({
    requestBody, responseStatus: 200, response: { ...response, commandId: 'another-command' },
    expectedRootNodeIds: ['observation-root-node'],
    expectedRootResourceType: 'Observation',
    previousOutputIds: ['observations', 'reports'], mountedOutputId: 'observations_combined',
  }).ok, false);
  assert.equal(nativeCombineTargetBindingEvidence({
    requestBody, responseStatus: 200, response,
    expectedRootNodeIds: ['observation-root-node'],
    expectedRootResourceType: 'Observation',
    previousOutputIds: ['observations', 'reports', 'observations_combined'], mountedOutputId: 'observations_combined',
  }).ok, false);
});
