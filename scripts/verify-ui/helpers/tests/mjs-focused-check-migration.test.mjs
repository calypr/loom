import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const realRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../..');
const selfTestPath = 'scripts/verify-ui/helpers/tests/mjs-focused-check-migration.test.mjs';
const toolSource = readFileSync(path.join(realRoot, 'scripts/migrate-mjs-focused-checks.mjs'), 'utf8');
const plannerSource = readFileSync(path.join(realRoot, 'scripts/verify-ui/helpers/focused-check-groups.mjs'), 'utf8');
const selfTestSource = readFileSync(fileURLToPath(import.meta.url), 'utf8');
const fixtureGeneratorSource = `
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const root = process.cwd();
const outputIndex = process.argv.indexOf('--json');
if (!process.argv.includes('--details') || outputIndex < 0 || !process.argv[outputIndex + 1]) process.exit(2);
const compact = JSON.parse(readFileSync(path.join(root, 'verification/builder-interactions/test-obligations.json'), 'utf8'));
const catalog = JSON.parse(readFileSync(path.join(root, 'verification/builder-interactions/mjs-source-semantics.json'), 'utf8'));
const registryModule = await import(pathToFileURL(path.join(root, 'scripts/verify-ui/registry.mjs')).href);
const active = catalog.files.filter((file) => file.lifecycle === 'active');
const byPath = new Map(active.map((file) => [file.path, file]));
const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const rows = new Map(active.map((file) => [file.path, {
  path: file.path,
  present: existsSync(path.join(root, file.path)),
  currentSha256: sha256(readFileSync(path.join(root, file.path))),
  semanticClaimIds: [...new Set((file.assertions ?? []).map((assertion) => assertion.semanticClaimId))].sort(),
  connections: [],
  journeyMappings: [],
}]));
for (const source of active) {
  const text = readFileSync(path.join(root, source.path), 'utf8');
  const imports = /\\bfrom\\s+(['"])([^'"]+)\\1/g;
  for (const match of text.matchAll(imports)) {
    if (!match[2].startsWith('.')) continue;
    const resolvedPath = path.posix.normalize(path.posix.join(path.posix.dirname(source.path), match[2]));
    const suffix = match[2].split('/').filter((part) => part !== '.' && part !== '..').join('/');
    const candidates = [...byPath.keys()].filter((candidate) => candidate.endsWith(suffix));
    const targetPath = byPath.has(resolvedPath) ? resolvedPath : candidates.length === 1 ? candidates[0] : null;
    if (!targetPath) continue;
    const line = text.slice(0, match.index).split('\\n').length;
    rows.get(targetPath).connections.push({ kind: 'literal-import-candidate', sourcePath: source.path, targetPath, specifier: match[2], line, column: 1 });
  }
}
for (const scenario of registryModule.registry) {
  for (const caseName of registryModule.caseNamesFor(scenario)) {
    const contract = registryModule.scenarioCaseFor(scenario, caseName);
    for (const group of contract.focusedChecks ?? []) {
      const command = group.command ?? [];
      const paths = (command[0] === 'node-test' ? command.slice(1) : command.slice(4)).filter((value) => typeof value === 'string' && value.endsWith('.mjs'));
      for (const testPath of paths) rows.get(testPath)?.connections.push({ kind: 'configured-command', caseId: scenario.id + ':' + caseName, checkId: group.id });
      for (const sourcePath of group.sourceFiles ?? []) {
        if (!paths.includes(sourcePath)) rows.get(sourcePath)?.connections.push({ kind: 'source-file-pinned-only', caseId: scenario.id + ':' + caseName, checkId: group.id });
      }
    }
  }
}
for (const claim of catalog.claims) {
  for (const source of catalog.files) {
    for (const assertion of source.assertions ?? []) {
      if (assertion.semanticClaimId !== claim.id) continue;
      rows.get(source.path)?.journeyMappings.push(...(claim.journeys ?? []).map((journey) => ({ claimId: claim.id, ...journey })));
    }
  }
}
const { projectionKind: _projectionKind, ...compactReport } = compact;
const detailed = { ...compactReport, scripts: [...rows.values()], semanticClaims: catalog.claims.map(({ id, journeys = [] }) => ({ id, journeys })) };
writeFileSync(process.argv[outputIndex + 1], JSON.stringify(detailed, null, 2) + '\\n');
`;
const hash = (value) => createHash('sha256').update(value).digest('hex');
const write = (root, file, value) => {
  const target = path.join(root, file);
  mkdirSync(path.dirname(target), { recursive: true });
  writeFileSync(target, value);
};

function fixture({ ignoreLoader = false } = {}) {
  const root = mkdtempSync(path.join(os.tmpdir(), 'mjs-focused-migration-'));
  const files = new Map();
  const claims = [];
  const assertions = [];
  const sourcePath = 'scripts/verify-ui/helpers/tests/oracle.test.mjs';
  const oraclePath = 'scripts/verify-ui/helpers/oracle.mjs';
  const workflowPath = 'scripts/verify-ui/workflows/workflow.mjs';
  const specPath = 'scripts/verify-ui/specs/scenario.spec.mjs';
  const registryPath = 'scripts/verify-ui/registry.mjs';
  files.set(oraclePath, 'export function anchorA() {}\nexport function anchorB() {}\n');
  files.set(workflowPath, "import { anchorA, anchorB } from '../helpers/oracle.mjs';\nexport async function reviewedWorkflow() { anchorA(); anchorB(); }\n");
  files.set(specPath, `import { reviewedWorkflow } from '../workflows/workflow.mjs';\ntest.use({ cdaScenarioID: 'scenario-a', cdaCaseName: 'case-a' });\ntest('case', async () => reviewedWorkflow());\n`);
  files.set(sourcePath, "import { anchorA, anchorB } from '../helpers/oracle.mjs';\nanchorA();\nanchorB();\n");
  const rows = [
    [oraclePath, 'helper-or-oracle', 'runtime-module'],
    [workflowPath, 'browser-workflow', 'browser-workflow'],
    [specPath, 'browser-spec', 'browser-spec'],
  ];
  const addTest = (file, claimId, contents) => {
    files.set(file, contents);
    rows.push([file, 'test', 'node-test']);
    claims.push({ id: claimId, statement: `claim ${claimId}`, category: 'oracle', journeys: [] });
    assertions.push({ sourceAssertionId: `source:${file}:L1:assertion`, semanticClaimId: claimId, evidenceKind: 'test-assertion', sourceLines: [1] });
  };
  addTest(sourcePath, 'claim-a', files.get(sourcePath));
  const attachmentLoader = ignoreLoader ? '' : `for (const attachment of attachmentData.attachments) { const scenario = registry.find((entry) => entry.id === attachment.scenarioId); scenario.cases[attachment.caseId].focusedChecks = attachment.focusedChecks; }\n`;
  files.set(registryPath, `import attachmentData from './mjs-focused-check-attachments.json' with { type: 'json' };\nexport const registry = [{ id: 'scenario-a', script: 'unrelated.mjs', cases: { 'case-a': { playwrightTest: '${specPath}', requiredChecks: ['exact registered check'] } } }];\n${attachmentLoader}export const caseNamesFor = scenario => Object.keys(scenario.cases);\nexport const scenarioCaseFor = (scenario, name) => ({ ...scenario.cases[name], caseName: name, requiredChecks: scenario.cases[name].requiredChecks });\n`);
  rows.push([registryPath, 'tool-or-module', 'runtime-module']);
  const specs = rows.map(([file, role, testLayer]) => ({ file, role, testLayer }));
  const catalog = {
    schemaVersion: 1,
    sourceCommit: 'a'.repeat(40),
    inventorySha256: 'b'.repeat(64),
    claims,
    files: specs.map(({ file, role, testLayer }) => ({
      path: file,
      sha256: hash(files.get(file)),
      role,
      testLayer,
      lifecycle: 'active',
      purpose: file,
      assertions: file === sourcePath ? assertions : [],
      executionDiscovery: {
        status: 'no-static-path-found',
        evidence: file === sourcePath ? [
          { path: file, line: 1, kind: 'imports node:test; no registry focused-check command names this test file' },
          { path: file, line: 2, kind: 'independent assertion evidence' },
        ] : [],
        unresolvedFacts: ['no registered node command'],
      },
    })).concat([{
      path: 'scripts/retired.mjs', sha256: 'c'.repeat(64), role: 'test', testLayer: 'node-test', lifecycle: 'retired', purpose: 'retired', assertions: [],
    }]),
  };
  const obligations = {
    schemaVersion: 3,
    sources: { semanticCatalog: 'verification/builder-interactions/mjs-source-semantics.json', registry: registryPath },
    mjsCatalogSummary: { baselineFiles: rows.length + 1, baselineRetired: 1, currentFiles: rows.length, currentAdded: 0 },
    summary: {},
    obligations: [],
    tests: [],
    duplicates: { multipleTests: [], testNames: [], checks: [], sharedProofs: [] },
    gaps: [],
    partial: [],
    projectionKind: 'compact-coverage',
  };
  const mappingEvidence = [
    { path: workflowPath, line: 1, kind: 'reviewed oracle call' },
    { path: specPath, line: 1, kind: 'registered case identity' },
  ];
  const families = {
    schemaVersion: 1,
    families: [{
      id: 'oracle-family-a', scenarioId: 'scenario-a', caseId: 'case-a', workflowPath, oraclePath,
      requiredOracleFunctions: ['anchorA', 'anchorB'], fixtureClass: 'synthetic-cda-fhir-v1-rows', mappingEvidence,
    }],
  };
  write(root, 'scripts/migrate-mjs-focused-checks.mjs', toolSource);
  write(root, 'scripts/build-test-obligations.mjs', fixtureGeneratorSource);
  write(root, selfTestPath, selfTestSource);
  write(root, 'scripts/verify-ui/helpers/focused-check-groups.mjs', plannerSource);
  for (const [file, contents] of files) write(root, file, contents);
  write(root, 'verification/builder-interactions/mjs-focused-check-families.json', `${JSON.stringify(families, null, 2)}\n`);
  write(root, 'verification/builder-interactions/mjs-source-semantics.json', `${JSON.stringify(catalog, null, 2)}\n`);
  write(root, 'verification/builder-interactions/test-obligations.json', `${JSON.stringify(obligations, null, 2)}\n`);
  write(root, 'scripts/verify-ui/mjs-focused-check-attachments.json', `${JSON.stringify({ schemaVersion: 1, attachments: [] }, null, 2)}\n`);
  return { root, files, rows, families, mappingEvidence, sourcePath, oraclePath, workflowPath, specPath, registryPath };
}

function invoke(root, ...args) {
  const result = spawnSync(process.execPath, ['scripts/migrate-mjs-focused-checks.mjs', '--root', root, ...args], {
    cwd: root,
    encoding: 'utf8',
  });
  return result;
}

function plan(root) {
  const result = invoke(root, '--plan');
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
}

function cleanup(root) {
  rmSync(root, { recursive: true, force: true });
}

test('plan previews exact nonempty attachment and case-bound claim edits with hash preconditions', () => {
  const state = fixture();
  try {
    const trackedProjection = JSON.parse(readFileSync(path.join(realRoot, 'verification/builder-interactions/test-obligations.json'), 'utf8'));
    assert.equal(trackedProjection.projectionKind, 'compact-coverage');
    assert.equal(Object.hasOwn(trackedProjection, 'scripts'), false);
    assert.equal(Object.hasOwn(trackedProjection, 'semanticClaims'), false);
    const compact = JSON.parse(readFileSync(path.join(state.root, 'verification/builder-interactions/test-obligations.json'), 'utf8'));
    assert.equal(compact.projectionKind, 'compact-coverage');
    assert.equal(Object.hasOwn(compact, 'scripts'), false);
    assert.equal(Object.hasOwn(compact, 'semanticClaims'), false);
    const proposed = plan(state.root);
    assert.equal(proposed.summary.activeSources, 5);
    assert.equal(proposed.summary.baselineActiveMjsSources, 5);
    assert.equal(proposed.summary.addedMjsSources, 2);
    assert.equal(proposed.summary.projectedActiveMjsSources, 7);
    assert.equal(proposed.summary.retiredSources, 1);
    assert.equal(proposed.summary.classifications.supported, 1);
    assert.equal(proposed.summary.classifications['needs-review'], 0);
    assert.equal(proposed.attachments.length, 1);
    assert.deepEqual(proposed.attachments[0], {
      scenarioId: 'scenario-a',
      caseId: 'case-a',
      focusedChecks: [{
        id: 'oracle-family-a',
        cwd: '.',
        command: ['node-test', state.sourcePath],
        sourceFiles: [
          state.oraclePath, state.workflowPath, state.specPath, state.registryPath,
          'scripts/verify-ui/mjs-focused-check-attachments.json',
          'scripts/migrate-mjs-focused-checks.mjs',
          'verification/builder-interactions/mjs-focused-check-families.json',
        ].sort(),
      }],
    });
    assert.deepEqual(proposed.claimEdits.map(({ claimId, after }) => ({ claimId, after })), [{
      claimId: 'claim-a',
      after: [{
        caseId: 'scenario-a:case-a', relation: 'supporting', requiredChecks: [],
        focusedCheckIds: ['oracle-family-a'], evidence: state.mappingEvidence,
      }],
    }]);
    const executionEvidence = proposed.executionDiscoveryEdits.find((edit) => edit.path === state.sourcePath).after.evidence;
    assert.equal(executionEvidence.some((item) => item.kind === 'imports node:test; no registry focused-check command names this test file'), false);
    assert.ok(executionEvidence.some((item) => item.kind === 'independent assertion evidence'));
    assert.ok(executionEvidence.some((item) => item.path === 'scripts/verify-ui/mjs-focused-check-attachments.json'
      && item.kind === 'generated focused node-test command' && item.line > 0));
    assert.ok(proposed.preconditions.some((item) => item.file === state.oraclePath && /^[a-f0-9]{64}$/.test(item.sha256)));
    assert.ok(proposed.edits.length > 0);
    assert.ok(proposed.edits.find((edit) => edit.file.endsWith('.json')));
  } finally {
    cleanup(state.root);
  }
});

test('a configured test importing the shared helper but missing a family anchor stays already-wired', () => {
  const state = fixture();
  try {
    const configuredPath = 'scripts/verify-ui/helpers/tests/preconfigured-helper.test.mjs';
    const configuredSource = "import { anchorA } from '../helpers/oracle.mjs';\nanchorA();\n";
    write(state.root, configuredPath, configuredSource);
    const registryPath = path.join(state.root, state.registryPath);
    let registryText = readFileSync(registryPath, 'utf8');
    registryText = registryText.replace(
      "export const registry = [{ id: 'scenario-a'",
      `export const registry = [{ id: 'existing-scenario', cases: { 'existing-case': { playwrightTest: '${state.specPath}', requiredChecks: ['existing helper check'], focusedChecks: [{ id: 'existing-helper-check', cwd: '.', command: ['node-test', '${configuredPath}'], sourceFiles: ['${state.oraclePath}'] }] } } }, { id: 'scenario-a'`,
    );
    write(state.root, state.registryPath, registryText);
    const catalogPath = path.join(state.root, 'verification/builder-interactions/mjs-source-semantics.json');
    const catalog = JSON.parse(readFileSync(catalogPath, 'utf8'));
    catalog.files.push({
      path: configuredPath, sha256: hash(configuredSource), role: 'test', testLayer: 'node-test', lifecycle: 'active', purpose: configuredPath,
      assertions: [], executionDiscovery: { status: 'static-path-found', evidence: [{ path: configuredPath, line: 1, kind: 'configured Node test' }], unresolvedFacts: [] },
    });
    catalog.files.find((row) => row.path === state.registryPath).sha256 = hash(registryText);
    write(state.root, 'verification/builder-interactions/mjs-source-semantics.json', `${JSON.stringify(catalog, null, 2)}\n`);
    const obligationsPath = path.join(state.root, 'verification/builder-interactions/test-obligations.json');
    const obligations = JSON.parse(readFileSync(obligationsPath, 'utf8'));
    obligations.mjsCatalogSummary.currentFiles += 1;
    obligations.mjsCatalogSummary.currentAdded += 1;
    write(state.root, 'verification/builder-interactions/test-obligations.json', `${JSON.stringify(obligations, null, 2)}\n`);

    const proposed = plan(state.root);
    assert.equal(proposed.classifications.find((row) => row.path === configuredPath).classification, 'already-wired');
    assert.deepEqual(proposed.attachments[0].focusedChecks[0].command, ['node-test', state.sourcePath]);
    assert.equal(proposed.attachments[0].focusedChecks[0].command.includes(configuredPath), false);
  } finally {
    cleanup(state.root);
  }
});

test('apply rejects a source hash that changed after plan and writes nothing', () => {
  const state = fixture();
  try {
    const proposed = plan(state.root);
    const attachmentPath = path.join(state.root, 'scripts/verify-ui/mjs-focused-check-attachments.json');
    const catalogPath = path.join(state.root, 'verification/builder-interactions/mjs-source-semantics.json');
    const before = [readFileSync(attachmentPath), readFileSync(catalogPath)];
    write(state.root, state.oraclePath, `${state.files.get(state.oraclePath)}\n`);
    const result = invoke(state.root, '--apply', '--plan-token', proposed.planToken);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /stale source hash/);
    assert.deepEqual([readFileSync(attachmentPath), readFileSync(catalogPath)], before);
  } finally {
    cleanup(state.root);
  }
});

test('apply restores attachment and catalogue bytes when the fresh registry ignores the JSON', () => {
  const state = fixture({ ignoreLoader: true });
  try {
    const proposed = plan(state.root);
    const attachmentPath = path.join(state.root, 'scripts/verify-ui/mjs-focused-check-attachments.json');
    const catalogPath = path.join(state.root, 'verification/builder-interactions/mjs-source-semantics.json');
    const before = [readFileSync(attachmentPath), readFileSync(catalogPath)];
    const applied = invoke(state.root, '--apply', '--plan-token', proposed.planToken);
    assert.notEqual(applied.status, 0);
    assert.match(applied.stderr, /fresh registry import does not consume exact focused attachment/);
    assert.deepEqual([readFileSync(attachmentPath), readFileSync(catalogPath)], before);
  } finally {
    cleanup(state.root);
  }
});

test('apply and check are idempotent and do not duplicate semantic mappings', () => {
  const state = fixture();
  try {
    const first = plan(state.root);
    const applied = invoke(state.root, '--apply', '--plan-token', first.planToken);
    assert.equal(applied.status, 0, applied.stderr);
    const attachmentPath = path.join(state.root, 'scripts/verify-ui/mjs-focused-check-attachments.json');
    const catalogPath = path.join(state.root, 'verification/builder-interactions/mjs-source-semantics.json');
    const before = [readFileSync(attachmentPath), readFileSync(catalogPath)];
    const second = plan(state.root);
    assert.equal(second.noOp, true);
    const reapplied = invoke(state.root, '--apply', '--plan-token', second.planToken);
    assert.equal(reapplied.status, 0, reapplied.stderr);
    assert.match(reapplied.stdout, /^NO-OP/);
    assert.deepEqual([readFileSync(attachmentPath), readFileSync(catalogPath)], before);
    const checked = invoke(state.root, '--check');
    assert.equal(checked.status, 0, checked.stderr);
    const catalog = JSON.parse(readFileSync(catalogPath, 'utf8'));
    assert.equal(catalog.claims[0].journeys.filter((mapping) => mapping.caseId === 'scenario-a:case-a').length, 1);
    const afterCheck = [readFileSync(attachmentPath), readFileSync(catalogPath)];
    assert.deepEqual(afterCheck, before);
  } finally {
    cleanup(state.root);
  }
});

test('plan rejects a previously generated focused check that no current family covers', () => {
  const state = fixture();
  try {
    const first = plan(state.root);
    const applied = invoke(state.root, '--apply', '--plan-token', first.planToken);
    assert.equal(applied.status, 0, applied.stderr);
    const attachmentPath = path.join(state.root, 'scripts/verify-ui/mjs-focused-check-attachments.json');
    const attachmentData = JSON.parse(readFileSync(attachmentPath, 'utf8'));
    attachmentData.attachments[0].focusedChecks.push({
      id: 'unmanaged-old-family-check',
      cwd: '.',
      command: ['node-test', selfTestPath],
      sourceFiles: [selfTestPath],
    });
    write(state.root, 'scripts/verify-ui/mjs-focused-check-attachments.json', `${JSON.stringify(attachmentData, null, 2)}\n`);
    const before = readFileSync(attachmentPath);
    const result = invoke(state.root, '--plan');
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /generated focused check unmanaged-old-family-check .* is no longer covered by a reviewed family/);
    assert.deepEqual(readFileSync(attachmentPath), before);
  } finally {
    cleanup(state.root);
  }
});

test('--check rejects a generated attachment that the registry loader ignores', () => {
  const state = fixture();
  try {
    const first = plan(state.root);
    const applied = invoke(state.root, '--apply', '--plan-token', first.planToken);
    assert.equal(applied.status, 0, applied.stderr);
    let registryText = readFileSync(path.join(state.root, state.registryPath), 'utf8');
    registryText = registryText.replace(/for \(const attachment of attachmentData\.attachments\) \{[^\n]*\}\n/, '');
    write(state.root, state.registryPath, registryText);
    const catalogPath = path.join(state.root, 'verification/builder-interactions/mjs-source-semantics.json');
    const catalog = JSON.parse(readFileSync(catalogPath, 'utf8'));
    catalog.files.find((row) => row.path === state.registryPath).sha256 = hash(registryText);
    write(state.root, 'verification/builder-interactions/mjs-source-semantics.json', `${JSON.stringify(catalog, null, 2)}\n`);
    const result = invoke(state.root, '--check');
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /registry does not consume exact focused attachment/);
  } finally {
    cleanup(state.root);
  }
});

test('new matching test is discovered from obligation import edges without changing family rules', () => {
  const state = fixture();
  try {
    const familyPath = path.join(state.root, 'verification/builder-interactions/mjs-focused-check-families.json');
    const familyBefore = readFileSync(familyPath);
    const newPath = 'scripts/verify-ui/helpers/tests/oracle-second.test.mjs';
    const contents = "import { anchorA, anchorB } from '../helpers/oracle.mjs';\nanchorA();\nanchorB();\n";
    state.rows.push([newPath, 'test', 'node-test']);
    state.files.set(newPath, contents);
    const catalogPath = path.join(state.root, 'verification/builder-interactions/mjs-source-semantics.json');
    const catalog = JSON.parse(readFileSync(catalogPath, 'utf8'));
    catalog.files.push({
      path: newPath, sha256: hash(contents), role: 'test', testLayer: 'node-test', lifecycle: 'active', purpose: newPath,
      assertions: [{ sourceAssertionId: `source:${newPath}:L1:assertion`, semanticClaimId: 'claim-b', evidenceKind: 'test-assertion', sourceLines: [1] }],
      executionDiscovery: { status: 'no-static-path-found', evidence: [], unresolvedFacts: ['no registered node command'] },
    });
    catalog.claims.push({ id: 'claim-b', statement: 'claim claim-b', category: 'oracle', journeys: [] });
    write(state.root, newPath, contents);
    write(state.root, 'verification/builder-interactions/mjs-source-semantics.json', `${JSON.stringify(catalog, null, 2)}\n`);
    const obligationsPath = path.join(state.root, 'verification/builder-interactions/test-obligations.json');
    const obligations = JSON.parse(readFileSync(obligationsPath, 'utf8'));
    obligations.mjsCatalogSummary.currentFiles += 1;
    obligations.mjsCatalogSummary.currentAdded += 1;
    write(state.root, 'verification/builder-interactions/test-obligations.json', `${JSON.stringify(obligations, null, 2)}\n`);
    const proposed = plan(state.root);
    assert.deepEqual(proposed.attachments[0].focusedChecks[0].command, ['node-test', newPath, state.sourcePath]);
    assert.deepEqual(readFileSync(familyPath), familyBefore);
  } finally {
    cleanup(state.root);
  }
});

test('ambiguous family destination and ambiguous test-family matches fail closed', () => {
  const duplicateDestination = fixture();
  try {
    const familyPath = path.join(duplicateDestination.root, 'verification/builder-interactions/mjs-focused-check-families.json');
    const rules = JSON.parse(readFileSync(familyPath, 'utf8'));
    rules.families.push({ ...rules.families[0], id: 'other-family' });
    write(duplicateDestination.root, 'verification/builder-interactions/mjs-focused-check-families.json', `${JSON.stringify(rules, null, 2)}\n`);
    const result = invoke(duplicateDestination.root, '--plan');
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /ambiguous destination/);
  } finally {
    cleanup(duplicateDestination.root);
  }
  const duplicateMatch = fixture();
  try {
    const workflowB = 'scripts/verify-ui/workflows/workflow-b.mjs';
    const specB = 'scripts/verify-ui/specs/scenario-b.spec.mjs';
    const registryPath = path.join(duplicateMatch.root, duplicateMatch.registryPath);
    write(duplicateMatch.root, workflowB, "import { anchorA, anchorB } from '../helpers/oracle.mjs';\nexport async function workflowB() { anchorA(); anchorB(); }\n");
    write(duplicateMatch.root, specB, "import { workflowB } from '../workflows/workflow-b.mjs';\ntest.use({ cdaScenarioID: 'scenario-b', cdaCaseName: 'case-b' });\ntest('case', async () => workflowB());\n");
    const catalogPath = path.join(duplicateMatch.root, 'verification/builder-interactions/mjs-source-semantics.json');
    const catalog = JSON.parse(readFileSync(catalogPath, 'utf8'));
    for (const [file, role, testLayer] of [[workflowB, 'browser-workflow', 'browser-workflow'], [specB, 'browser-spec', 'browser-spec']]) {
      const contents = readFileSync(path.join(duplicateMatch.root, file), 'utf8');
      catalog.files.push({ path: file, sha256: hash(contents), role, testLayer, lifecycle: 'active', purpose: file, assertions: [], executionDiscovery: { status: 'static-path-found', evidence: [{ path: file, line: 1, kind: 'source' }], unresolvedFacts: [] } });
    }
    write(duplicateMatch.root, 'verification/builder-interactions/mjs-source-semantics.json', `${JSON.stringify(catalog, null, 2)}\n`);
    const obligationsPath = path.join(duplicateMatch.root, 'verification/builder-interactions/test-obligations.json');
    const obligations = JSON.parse(readFileSync(obligationsPath, 'utf8'));
    obligations.mjsCatalogSummary.currentFiles += 2;
    obligations.mjsCatalogSummary.currentAdded += 2;
    write(duplicateMatch.root, 'verification/builder-interactions/test-obligations.json', `${JSON.stringify(obligations, null, 2)}\n`);
    let registryText = readFileSync(registryPath, 'utf8');
    registryText = registryText.replace("export const registry = [{ id: 'scenario-a'", "export const registry = [{ id: 'scenario-b', script: 'irrelevant.mjs', cases: { 'case-b': { playwrightTest: 'scripts/verify-ui/specs/scenario-b.spec.mjs', requiredChecks: ['exact'] } } }, { id: 'scenario-a'");
    write(duplicateMatch.root, duplicateMatch.registryPath, registryText);
    catalog.files.find((row) => row.path === duplicateMatch.registryPath).sha256 = hash(registryText);
    write(duplicateMatch.root, 'verification/builder-interactions/mjs-source-semantics.json', `${JSON.stringify(catalog, null, 2)}\n`);
    write(duplicateMatch.root, 'verification/builder-interactions/test-obligations.json', `${JSON.stringify(obligations, null, 2)}\n`);
    const rulesPath = path.join(duplicateMatch.root, 'verification/builder-interactions/mjs-focused-check-families.json');
    const rules = JSON.parse(readFileSync(rulesPath, 'utf8'));
    rules.families.push({ ...rules.families[0], id: 'oracle-family-b', scenarioId: 'scenario-b', caseId: 'case-b', workflowPath: workflowB, mappingEvidence: [{ path: workflowB, line: 1, kind: 'oracle call' }, { path: specB, line: 1, kind: 'case identity' }] });
    write(duplicateMatch.root, 'verification/builder-interactions/mjs-focused-check-families.json', `${JSON.stringify(rules, null, 2)}\n`);
    const result = invoke(duplicateMatch.root, '--plan');
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /ambiguous test match/);
  } finally {
    cleanup(duplicateMatch.root);
  }
});
