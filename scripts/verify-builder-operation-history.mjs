#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { execFileSync, spawnSync } from 'node:child_process';
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { exploreOperationHistory } from '../verification/builder-interactions/operationHistoryModel.mjs';

const ROOT = realpathSync(resolve(dirname(fileURLToPath(import.meta.url)), '..'));
const OPERATION_SOURCES = [
  {
    path: 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx',
    sha256: '254c17ff7de4308bb2268e505b2fd5befc44545adc449a68af020cea74c5bb20',
  },
  {
    path: 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx',
    sha256: '1a24c75f5e2ff2c7a11740ef5305e33cd3c169661108930edfbc1cae9fe27a14',
  },
  {
    path: 'internal/explorer/authoringv2/construction_change.go',
    sha256: '8f092b755eb0dd653b53a6cb07774874064ffe001e3176c91e3f38dff677ec1a',
  },
  {
    path: 'internal/explorer/authoringv2/construction_validation.go',
    sha256: 'f78efd6fb5d151939126865d868020b7f9c21b2324f068bf45432eb92ac2bdd1',
  },
  {
    path: 'internal/explorer/lifecycle/construction_proposal.go',
    sha256: 'ea2f2ef7733e3a7d804a196c55ea69673ebe3bd269b7b76f1888dbc06150f2a7',
  },
  {
    path: 'internal/server/openapi_explorer_construction_proposal.go',
    sha256: 'e4e62c8756d30a3d8cdce973063e9810ee50450dade01d68a53e073e1794bcb8',
  },
];
const OPERATION_SERVER_TEST = {
  path: 'internal/server/openapi_explorer_construction_proposal_test.go',
  sha256: 'a8a51c2eb72207d0e0ec2e347c136ca0d79dc154a4d07b88aed3a49b9747a767',
};
const GO_SERVER_TESTS = [
  'TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly',
  'TestConstructionProposalSemanticGroupChoiceReconcilesCompiledOutputMetadata',
];
const OPERATION_TEST = 'src/features/ExplorerBuilder/BuilderWorkspace.operationHistory.formal.test.tsx';
const GO_TEST = 'TestConstructionProposalOperationHistoryTraces';
const MODEL = 'verification/builder-interactions/operationHistoryModel.mjs';
const SKILL = '.codex/skills/builder-interaction-formal-verification/SKILL.md';
const GO_TEST_FILE = 'internal/explorer/lifecycle/construction_proposal_trace_test.go';
const SUPPORTED_ACTIONS = new Set(['editAggregate', 'editKey', 'restoreKey', 'propose', 'cancel', 'apply', 'applyAck']);
const WIRING_TEST_NAME = 'wires BuilderWorkspace history and the actual Group editor to its proposal request';

function fail(message) {
  throw new Error(message);
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function bytes(relativePath) {
  return readFileSync(resolve(ROOT, relativePath));
}

function json(path, label) {
  try {
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch (error) {
    fail(label + ' is missing or invalid at ' + path + ': ' + error.message);
  }
}

function canonicalJson(value) {
  if (Array.isArray(value)) return '[' + value.map(canonicalJson).join(',') + ']';
  if (value && typeof value === 'object') {
    return '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}';
  }
  return JSON.stringify(value);
}

function gitHead(root) {
  return execFileSync('git', ['-C', root, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
}

function mutationReferenceArgument() {
  const index = process.argv.indexOf('--allow-source-drift-for-mutation');
  if (index < 0) return undefined;
  if (index !== process.argv.length - 2 || !process.argv[index + 1]) {
    fail('--allow-source-drift-for-mutation requires exactly one pristine worktree root');
  }
  const reference = realpathSync(process.argv[index + 1]);
  if (reference === ROOT) fail('mutation reference must be a different worktree');
  return reference;
}

function requirePinnedSources(mutationReference) {
  const changed = OPERATION_SOURCES.map((source) => ({
    ...source,
    actual: sha256(bytes(source.path)),
  })).filter((source) => source.sha256 !== source.actual);
  if (!mutationReference && changed.length > 0) {
    fail('operation-history sources changed; inspect their mapping and update the pin deliberately:\n' + changed.map((source) =>
      '  ' + source.path + '\n    expected ' + source.sha256 + '\n    found    ' + source.actual,
    ).join('\n'));
  }
  if (mutationReference) {
    if (gitHead(ROOT) !== gitHead(mutationReference)) fail('mutation and pristine worktrees must have the same Git HEAD');
    const referenceDrift = OPERATION_SOURCES.filter((source) =>
      sha256(readFileSync(resolve(mutationReference, source.path))) !== source.sha256,
    );
    if (sha256(readFileSync(resolve(mutationReference, OPERATION_SERVER_TEST.path))) !== OPERATION_SERVER_TEST.sha256) {
      fail('pristine mutation reference does not match pinned server route test ' + OPERATION_SERVER_TEST.path);
    }
    if (referenceDrift.length > 0) fail('pristine mutation reference does not match pinned operation sources: ' + referenceDrift.map(({ path }) => path).join(', '));
    if (changed.length !== 1 || changed[0].path !== 'internal/explorer/lifecycle/construction_proposal.go') {
      fail('mutation mode requires exactly one changed proposal lifecycle source (construction_proposal.go): ' + JSON.stringify(changed.map(({ path }) => path)));
    }
    const verificationInputs = [MODEL, 'scripts/verify-builder-operation-history.mjs', SKILL,
      'ui/packages/loom-ui/' + OPERATION_TEST, GO_TEST_FILE, OPERATION_SERVER_TEST.path];
    const differentInputs = verificationInputs.filter((path) =>
      sha256(bytes(path)) !== sha256(readFileSync(resolve(mutationReference, path))),
    );
    if (differentInputs.length > 0) fail('model, runner, skill, and behavioral tests must match the pristine reference: ' + differentInputs.join(', '));
    console.log('Controlled source mutation: ' + changed[0].path + ' differs from pristine; verifier inputs and Git HEAD match.');
  } else {
    const serverTestHash = sha256(bytes(OPERATION_SERVER_TEST.path));
    if (serverTestHash !== OPERATION_SERVER_TEST.sha256) {
      fail('operation-history server route test changed; review the handler boundary mapping deliberately:\n' +
        '  ' + OPERATION_SERVER_TEST.path + '\n    expected ' + OPERATION_SERVER_TEST.sha256 + '\n    found    ' + serverTestHash);
    }
    for (const source of OPERATION_SOURCES) console.log('Pinned operation source: ' + source.path + ' ' + source.sha256);
    console.log('Pinned server HTTP conversion test: ' + OPERATION_SERVER_TEST.path + ' ' + OPERATION_SERVER_TEST.sha256);
  }
}

function captureRunInputs() {
  const paths = [
    ...OPERATION_SOURCES.map(({ path }) => path),
    OPERATION_SERVER_TEST.path,
    MODEL,
    'scripts/verify-builder-operation-history.mjs',
    SKILL,
    'ui/packages/loom-ui/' + OPERATION_TEST,
    GO_TEST_FILE,
  ];
  return new Map(paths.map((path) => [path, sha256(bytes(path))]));
}

function requireSameRunInputs(snapshot, phase) {
  const changes = [...snapshot].flatMap(([path, expected]) => {
    const actual = sha256(bytes(path));
    return actual === expected ? [] : [{ path, expected, actual }];
  });
  if (changes.length > 0) fail('operation-history inputs changed during ' + phase + ': ' + JSON.stringify(changes));
}

function captured(label, command, args, cwd, env) {
  console.log('\n' + label);
  console.log(cwd + '$ ' + [command, ...args].join(' '));
  const result = spawnSync(command, args, { cwd, env, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  return { status: result.status, error: result.error, output: (result.stdout || '') + (result.stderr || '') };
}

function operationEnv() {
  const env = { ...process.env };
  for (const name of [
    'BUILDER_OPERATION_PREPARE', 'BUILDER_OPERATION_PHASE', 'BUILDER_OPERATION_TRACES',
    'BUILDER_OPERATION_SEEDS', 'BUILDER_OPERATION_SEED_IDENTITIES', 'BUILDER_OPERATION_CANDIDATES',
    'BUILDER_OPERATION_CASE_ID', 'BUILDER_OPERATION_RESPONSES', 'BUILDER_OPERATION_PROPOSAL_RESPONSES',
    'BUILDER_OPERATION_PANEL_CHECK',
  ]) delete env[name];
  return env;
}

function validateModel(cases, stats) {
  if (stats.emittedTraceCount !== cases.length || stats.proposalInputTraceCount + stats.lifecycleWitnessCount !== cases.length) {
    fail('model case inventory changed: ' + JSON.stringify(stats));
  }
  const ids = new Set();
  const fingerprints = new Set();
  let proposals = 0;
  let applies = 0;
  let cancels = 0;
  let acknowledgments = 0;
  let ready = 0;
  let needsRepair = 0;
  let applyAckSuccess = 0;
  let applyAckFailure = 0;
  const proposalIndices = new Map();
  for (const trace of cases) {
    if (!trace.id || ids.has(trace.id)) fail('missing or duplicate model trace ID ' + trace.id);
    ids.add(trace.id);
    if (!Array.isArray(trace.actions) || !Array.isArray(trace.expectations) || trace.actions.length !== trace.expectations.length) {
      fail('trace action/expectation count mismatch in ' + trace.id);
    }
    const indices = [];
    trace.actions.forEach((action, actionIndex) => {
      if (!SUPPORTED_ACTIONS.has(action.kind)) fail('unsupported generated action ' + action.kind + ' in ' + trace.id);
      if (trace.expectations[actionIndex]?.actionIndex !== actionIndex) fail('expectation index mismatch in ' + trace.id);
      if (action.kind === 'propose') {
        proposals += 1;
        indices.push(actionIndex);
        const fingerprint = trace.expectations[actionIndex]?.requestFingerprint;
        if (!fingerprint) fail('proposal checkpoint has no request fingerprint in ' + trace.id);
        fingerprints.add(fingerprint);
        if (trace.expectations[actionIndex].proposalStatus === 'READY') ready += 1;
        else if (trace.expectations[actionIndex].proposalStatus === 'NEEDS_REPAIR') needsRepair += 1;
        else fail('unsupported model proposal status in ' + trace.id + ' at action ' + actionIndex);
      }
      if (action.kind === 'apply') applies += 1;
      if (action.kind === 'cancel') cancels += 1;
      if (action.kind === 'applyAck') {
        acknowledgments += 1;
        if (action.result === 'success') applyAckSuccess += 1;
        else if (action.result === 'failure') applyAckFailure += 1;
        else fail('unsupported apply acknowledgment result in ' + trace.id);
      }
    });
    proposalIndices.set(trace.id, indices);
  }
  if (ids.size !== cases.length || fingerprints.size !== stats.uniqueProposalRequestCount || proposals === 0 ||
    applies !== acknowledgments || applyAckSuccess + applyAckFailure !== acknowledgments) {
    fail('model transition counts changed: ' + JSON.stringify({
      traces: ids.size, proposals, uniqueRequests: fingerprints.size, applies, cancels, acknowledgments,
    }));
  }
  return {
    proposalIndices,
    proposalCount: proposals,
    abstractUniqueRequestCount: fingerprints.size,
    readyProposalCount: ready,
    needsRepairProposalCount: needsRepair,
    applies,
    cancels,
    acknowledgments,
    applyAckSuccess,
    applyAckFailure,
  };
}

function exactIds(actualCases, traces, label) {
  if (!Array.isArray(actualCases)) fail(label + ' has no case list');
  const expected = traces.map(({ id }) => id).sort();
  const actual = actualCases.map(({ id }) => id).sort();
  if (actual.length !== expected.length || actual.some((id, index) => id !== expected[index])) {
    fail(label + ' case IDs/count differ: expected ' + expected.length + ', got ' + actual.length);
  }
}

function validateVitestReport(reportPath, packageRoot, allowedPending = 0, reportMode = 'replay') {
  const report = json(reportPath, 'Vitest report');
  const expectedSuite = realpathSync(resolve(packageRoot, OPERATION_TEST));
  const suiteNames = (report.testResults ?? []).map(({ name }) => resolve(name));
  const assertions = (report.testResults ?? []).flatMap(({ assertionResults }) => assertionResults ?? []);
  const pending = assertions.filter(({ status }) => status === 'pending' || status === 'skipped');
  const validPending = allowedPending === 0 || (
    allowedPending === 1 && pending.length === 1 && pending[0].fullName?.endsWith(WIRING_TEST_NAME)
  );
  let passed;
  let filteredCount = 0;
  if (reportMode === 'panel') {
    const isSelected = ({ fullName }) => fullName?.endsWith('renders the Go-resolved key cascade before Apply and preserves the raw editor request') ||
      fullName?.endsWith('accounts for every generated trace and proposal checkpoint');
    const selected = assertions.filter(isSelected);
    const filtered = assertions.filter(({ status }) => status === 'pending' || status === 'skipped');
    filteredCount = filtered.length;
    passed = report.success === true && suiteNames.length === 1 && suiteNames[0] === expectedSuite &&
      selected.length === 2 && selected.every(({ status }) => status === 'passed') &&
      report.numPassedTests === 2 && report.numFailedTests === 0 && report.numTodoTests === 0 &&
      assertions.length === report.numTotalTests && filtered.length === report.numTotalTests - 2 &&
      report.numPendingTests === filtered.length &&
      assertions.filter((assertion) => !isSelected(assertion)).every(({ status }) => status === 'pending' || status === 'skipped');
  } else {
    passed = report.success === true && suiteNames.length === 1 && suiteNames[0] === expectedSuite &&
      report.numTotalTests > allowedPending && report.numPassedTests === report.numTotalTests - allowedPending &&
      report.numFailedTests === 0 && report.numPendingTests === allowedPending && report.numTodoTests === 0 &&
      assertions.length === report.numTotalTests && pending.length === allowedPending && validPending &&
      assertions.every(({ status }) => status === 'passed' || status === 'pending' || status === 'skipped');
  }
  if (reportMode === 'panel') {
    console.log('Vitest panel: 2/2 selected assertions passed; ' + filteredCount + ' nonmatching tests filtered by name; zero selected skips/todos.');
  } else {
    console.log('Vitest: ' + report.numPassedTests + '/' + report.numTotalTests + ' passed; ' + report.numPendingTests + ' skipped.');
  }
  if (!passed) fail('operation-history UI suite was missing, skipped unexpectedly, or failed');
}

function runVitest(packageRoot, vitestPath, reportPath, env, label, allowedPending = 0, reportMode = 'replay', testNamePattern) {
  const args = [vitestPath, 'run', '--reporter=json', '--outputFile=' + reportPath];
  if (testNamePattern) args.push('--testNamePattern', testNamePattern);
  args.push(OPERATION_TEST);
  const result = captured(label, process.execPath, args, packageRoot, env);
  if (result.error || result.status !== 0) fail(label + ' failed: ' + (result.error?.message ?? 'Vitest exited ' + result.status));
  validateVitestReport(reportPath, packageRoot, allowedPending, reportMode);
}

function validateSeedArtifact(artifact, traces) {
  if (artifact.schemaVersion !== 1) fail('frontend seed artifact has unsupported schema');
  exactIds(artifact.cases, traces, 'frontend seed artifact');
  for (const seed of artifact.cases) {
    if (!seed.acceptedWorkspace || !Array.isArray(seed.acceptedWorkspace.documents) || seed.acceptedWorkspace.documents.length !== 1) {
      fail('frontend seed omitted accepted workspace for ' + seed.id);
    }
  }
  return new Map(artifact.cases.map(({ id, acceptedWorkspace }) => [id, acceptedWorkspace]));
}

function validateIdentityArtifact(artifact, traces) {
  if (artifact.schemaVersion !== 1) fail('Go seed identity artifact has unsupported schema');
  exactIds(artifact.cases, traces, 'Go seed identity artifact');
  const identities = new Map();
  for (const identity of artifact.cases) {
    const trace = traces.find(({ id }) => id === identity.id);
    if (identities.has(identity.id) || !Number.isInteger(identity.acceptedVersion) || identity.acceptedVersion < 1 ||
      typeof identity.acceptedDigest !== 'string' || !identity.acceptedDigest ||
      identity.snapshotToken !== trace.initial.workspaceContext.snapshotToken ||
      identity.outputId !== trace.initial.workspaceContext.outputId) {
      fail('invalid Go draft identity in ' + identity.id + ': ' + JSON.stringify(identity));
    }
    identities.set(identity.id, identity);
  }
  return identities;
}

function validateCandidateArtifact(artifact, traces, modelInventory, seedWorkspaces, identities) {
  if (artifact.schemaVersion !== 1) fail('frontend candidate artifact has unsupported schema');
  exactIds(artifact.cases, traces, 'frontend candidate artifact');
  const traceById = new Map(traces.map((trace) => [trace.id, trace]));
  let total = 0;
  for (const candidate of artifact.cases) {
    const expectedIndices = modelInventory.proposalIndices.get(candidate.id);
    const checkpoints = candidate.checkpoints;
    const actualIndices = (checkpoints ?? []).map(({ actionIndex }) => actionIndex);
    if (!Array.isArray(checkpoints) || actualIndices.length !== expectedIndices.length ||
      actualIndices.some((index, position) => index !== expectedIndices[position])) {
      fail('frontend proposal checkpoint mismatch for ' + candidate.id +
        ': expected ' + JSON.stringify(expectedIndices) + ', got ' + JSON.stringify(actualIndices));
    }
    const trace = traceById.get(candidate.id);
    const identity = identities.get(candidate.id);
    const seedWorkspace = seedWorkspaces.get(candidate.id);
    for (const checkpoint of checkpoints) {
      if (!checkpoint.acceptedWorkspace || !checkpoint.acceptedDocument || !checkpoint.proposalRequest ||
        !checkpoint.candidateConstruction || typeof checkpoint.proposalRequest.requestId !== 'string' ||
        checkpoint.proposalRequest.requestId.length === 0) {
        fail('frontend omitted an actual proposal request in ' + candidate.id + ' action ' + checkpoint.actionIndex);
      }
      if (canonicalJson(checkpoint.acceptedWorkspace) !== canonicalJson(seedWorkspace) ||
        canonicalJson(checkpoint.acceptedDocument) !== canonicalJson(seedWorkspace.documents[0]) ||
        checkpoint.changedStepId !== 'group-step' || JSON.stringify(checkpoint.removeStepIds ?? []) !== '[]' ||
        JSON.stringify(checkpoint.proposalRequest.removeStepIds ?? []) !== '[]' ||
        checkpoint.proposalRequest.project !== trace.initial.workspaceContext.project ||
        checkpoint.proposalRequest.explorerId !== trace.initial.workspaceContext.explorerId ||
        checkpoint.proposalRequest.authResourcePath !== trace.initial.workspaceContext.authResourcePath ||
        checkpoint.proposalRequest.expectedDraftVersion !== identity.acceptedVersion ||
        checkpoint.proposalRequest.expectedDraftDigest !== identity.acceptedDigest ||
        checkpoint.proposalRequest.outputId !== identity.outputId ||
        checkpoint.proposalRequest.snapshotToken !== identity.snapshotToken ||
        checkpoint.proposalRequest.snapshotToken !== trace.initial.workspaceContext.snapshotToken ||
        checkpoint.proposalRequest.changedStepId !== 'group-step' ||
        JSON.stringify(checkpoint.proposalRequest.candidateConstruction) !== JSON.stringify(checkpoint.candidateConstruction)) {
        fail('frontend request identity/candidate mismatch in ' + candidate.id + ' action ' + checkpoint.actionIndex);
      }
      total += 1;
    }
  }
  if (total !== modelInventory.proposalCount) fail('frontend captured ' + total + ' proposals; model expects ' + modelInventory.proposalCount);
  return artifact.cases;
}

function validateProposalResponses(artifact, traces, candidateCases, modelInventory) {
  if (artifact.schemaVersion !== 1) fail('Go proposal response artifact has unsupported schema');
  exactIds(artifact.cases, traces, 'Go proposal response artifact');
  const candidates = new Map(candidateCases.map((candidate) => [candidate.id, candidate]));
  const tracesById = new Map(traces.map((trace) => [trace.id, trace]));
  let count = 0;
  let previewCount = 0;
  for (const entry of artifact.cases) {
    const expectedIndices = modelInventory.proposalIndices.get(entry.id);
    const candidate = candidates.get(entry.id);
    const actual = (entry.checkpoints ?? []).map(({ actionIndex }) => actionIndex);
    if (!Array.isArray(entry.checkpoints) || actual.length !== expectedIndices.length ||
      actual.some((index, position) => index !== expectedIndices[position])) {
      fail('Go proposal response checkpoints differ from model for ' + entry.id);
    }
    for (let index = 0; index < entry.checkpoints.length; index += 1) {
      const response = entry.checkpoints[index];
      const checkpoint = candidate.checkpoints[index];
      const expectation = tracesById.get(entry.id).expectations[response.actionIndex];
      if (response.requestId !== checkpoint.proposalRequest.requestId || !response.proposalResponse ||
        !response.proposalResponse.candidateConstruction || typeof response.proposalResponse.previewStatus !== 'string' ||
        response.requestFingerprint !== checkpoint.expected?.requestFingerprint) {
        fail('Go response omitted or changed captured request at ' + entry.id + ' action ' + response.actionIndex);
      }
      if (expectation.proposalStatus === 'READY') {
        const preview = response.previewResult;
        if (!preview || preview.receiptId !== response.proposalResponse.proposalId ||
          preview.outputId !== response.proposalResponse.outputId || !Array.isArray(preview.columns) ||
          !preview.summary || typeof preview.summary !== 'object') {
          fail('Go response omitted the actual Service.Preview result for READY proposal ' + entry.id + ' action ' + response.actionIndex);
        }
        previewCount += 1;
      } else if (response.previewResult != null) {
        fail('Go response unexpectedly attached a preview to a non-READY proposal ' + entry.id + ' action ' + response.actionIndex);
      }
      count += 1;
    }
  }
  if (count !== modelInventory.proposalCount) fail('Go response artifact has ' + count + ' proposal responses; model expects ' + modelInventory.proposalCount);
  if (previewCount !== modelInventory.readyProposalCount) fail('Go response artifact has ' + previewCount + ' actual previews; model expects ' + modelInventory.readyProposalCount);
  return artifact.cases;
}

function validateReplaySummary(output, traces, candidateCases, modelInventory) {
  const lines = [...output.matchAll(/BUILDER_OPERATION_REPLAY_JSON\s+(\{[^\n]+\})/g)];
  if (lines.length !== 1) fail('Go replay did not emit exactly one machine-readable summary');
  const result = JSON.parse(lines[0][1]);
  const rawFingerprints = new Set(candidateCases.flatMap((candidate) => candidate.checkpoints.map(({ proposalRequest }) => {
    const { requestId: _requestId, ...withoutRequestId } = proposalRequest;
    return sha256(canonicalJson(withoutRequestId));
  })));
  if (result.schemaVersion !== 1 || result.traceCaseCount !== traces.length ||
    result.candidateCaseCount !== traces.length || result.replayedCaseCount !== traces.length ||
    result.skippedCaseCount !== 0 || result.proposalCheckpointCount !== modelInventory.proposalCount ||
    result.uniqueAbstractRequestFingerprintCount !== modelInventory.abstractUniqueRequestCount ||
    result.uniqueRawRequestFingerprintCount !== rawFingerprints.size ||
    result.readyProposalCount !== modelInventory.readyProposalCount ||
    result.needsRepairProposalCount !== modelInventory.needsRepairProposalCount ||
    result.applyInvocationCount !== modelInventory.applies || result.applyAckSuccessCount !== modelInventory.applyAckSuccess ||
    result.applyAckFailureCount !== modelInventory.applyAckFailure ||
    result.servicePreviewCount !== modelInventory.readyProposalCount) {
    fail('Go replay count mismatch: ' + JSON.stringify(result));
  }
  exactIds(result.cases, traces, 'Go replay summary');
  const candidatesById = new Map(candidateCases.map((item) => [item.id, item]));
  const tracesById = new Map(traces.map((item) => [item.id, item]));
  for (const replayCase of result.cases) {
    const expectedIndices = modelInventory.proposalIndices.get(replayCase.id);
    const actual = replayCase.proposalCheckpoints ?? [];
    if (actual.length !== expectedIndices.length || actual.some((entry, index) => entry.actionIndex !== expectedIndices[index])) {
      fail('Go replay omitted proposal checkpoints for ' + replayCase.id);
    }
    const captured = candidatesById.get(replayCase.id).checkpoints;
    for (let index = 0; index < actual.length; index += 1) {
      const expectation = tracesById.get(replayCase.id).expectations[actual[index].actionIndex];
      if (actual[index].requestId !== captured[index].proposalRequest.requestId) {
        fail('Go changed the captured request ID in ' + replayCase.id + ' action ' + expectedIndices[index]);
      }
      if (actual[index].status !== expectation.proposalStatus ||
        actual[index].proposalIdPresent !== (expectation.proposalStatus === 'READY')) {
        fail('Go proposal status/receipt identity differs from model in ' + replayCase.id + ' action ' + expectedIndices[index]);
      }
    }
    const trace = tracesById.get(replayCase.id);
    const expectedAcks = trace.actions.filter(({ kind }) => kind === 'applyAck').length;
    if ((replayCase.applyAck ?? []).length !== expectedAcks) fail('Go replay omitted apply acknowledgments for ' + replayCase.id);
  }
  return result;
}

function reportGoCounterexample(output, traces) {
  const match = output.match(/trace (operation-history-[^\s]+) action (\d+)/);
  if (!match) return;
  const [, id, indexText] = match;
  const actionIndex = Number(indexText);
  const trace = traces.find((candidate) => candidate.id === id);
  if (!trace || !Number.isInteger(actionIndex)) return;
  const assertion = output.split('\n').find((line) => line.includes('trace ' + id + ' action ' + actionIndex));
  console.log('Go counterexample: ' + id + ' at action ' + actionIndex);
  console.log('Actions through failure: ' + JSON.stringify(trace.actions.slice(0, actionIndex + 1)));
  if (assertion) console.log('Go assertion: ' + assertion.trim());
}

async function main() {
  if (realpathSync(process.cwd()) !== ROOT) fail('run from the repository root: ' + ROOT);
  const mutationReference = mutationReferenceArgument();
  requirePinnedSources(mutationReference);
  const sourceSnapshot = captureRunInputs();
  const runDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-operation-history-'));
  const tracePath = join(runDirectory, 'operation-history-cases.json');
  const seedsPath = join(runDirectory, 'accepted-workspace-seeds.json');
  const identitiesPath = join(runDirectory, 'accepted-draft-identities.json');
  const candidatePath = join(runDirectory, 'frontend-candidate-requests.json');
  const responsesPath = join(runDirectory, 'go-proposal-responses.json');
  const prepareReportPath = join(runDirectory, 'vitest-prepare.json');
  const replayReportPath = join(runDirectory, 'vitest-replay.json');
  const panelReportPath = join(runDirectory, 'vitest-panel.json');
  const { cases, stats } = exploreOperationHistory();
  writeFileSync(tracePath, JSON.stringify({ schemaVersion: 1, cases }, null, 2) + '\n');
  const modelInventory = validateModel(cases, stats);
  console.log('Operation-history bounded model: ' + JSON.stringify(stats));
  console.log('Model inventory: ' + JSON.stringify({ traceCount: cases.length, ...modelInventory }));
  console.log('Shared fresh trace file: ' + tracePath);

  const packageRoot = resolve(ROOT, 'ui/packages/loom-ui');
  const vitestPath = resolve(ROOT, 'ui/node_modules/vitest/vitest.mjs');
  if (!existsSync(vitestPath)) fail('UI dependencies are missing; install the existing workspace dependencies in ui/.');
  const env = operationEnv();
  runVitest(packageRoot, vitestPath, prepareReportPath, {
    ...env,
    BUILDER_OPERATION_PREPARE: '1',
    BUILDER_OPERATION_TRACES: tracePath,
    BUILDER_OPERATION_SEEDS: seedsPath,
  }, 'Export every accepted workspace seed from the actual UI adapter');
  requireSameRunInputs(sourceSnapshot, 'UI seed export');
  const seedWorkspaces = validateSeedArtifact(json(seedsPath, 'frontend seed artifact'), cases);

  const goPrepare = captured('Validate and compile each accepted workspace to obtain its canonical draft identity',
    'go', ['test', '-count=1', './internal/explorer/lifecycle', '-run', '^' + GO_TEST + '$', '-v'], ROOT, {
      ...env,
      BUILDER_OPERATION_PHASE: 'prepare',
      BUILDER_OPERATION_TRACES: tracePath,
      BUILDER_OPERATION_SEEDS: seedsPath,
      BUILDER_OPERATION_SEED_IDENTITIES: identitiesPath,
    });
  if (goPrepare.error || goPrepare.status !== 0) fail('Go seed preparation failed: ' + (goPrepare.error?.message ?? 'go test exited ' + goPrepare.status));
  requireSameRunInputs(sourceSnapshot, 'Go seed compilation');
  const identities = validateIdentityArtifact(json(identitiesPath, 'Go draft identity artifact'), cases);
  console.log('Prepared workspaces and identities: ' + seedWorkspaces.size + '/' + cases.length + ' and ' + identities.size + '/' + cases.length + '.');

  runVitest(packageRoot, vitestPath, replayReportPath, {
    ...env,
    BUILDER_OPERATION_TRACES: tracePath,
    BUILDER_OPERATION_SEEDS: seedsPath,
    BUILDER_OPERATION_SEED_IDENTITIES: identitiesPath,
    BUILDER_OPERATION_CANDIDATES: candidatePath,
  }, 'Replay bounded actions through the actual Group editor and capture every proposal request');
  requireSameRunInputs(sourceSnapshot, 'UI action replay');
  const candidateCases = validateCandidateArtifact(
    json(candidatePath, 'frontend candidate artifact'), cases, modelInventory, seedWorkspaces, identities,
  );
  console.log('Actual UI candidate requests: ' + candidateCases.length + '/' + cases.length + ' traces, ' + modelInventory.proposalCount + ' proposal checkpoints.');

  const goReplay = captured('Replay captured requests through real Go proposal, analyzer, Apply, and acknowledgment paths',
    'go', ['test', '-count=1', './internal/explorer/lifecycle', '-run', '^' + GO_TEST + '$', '-v'], ROOT, {
      ...env,
      BUILDER_OPERATION_PHASE: 'full',
      BUILDER_OPERATION_TRACES: tracePath,
      BUILDER_OPERATION_SEEDS: seedsPath,
      BUILDER_OPERATION_SEED_IDENTITIES: identitiesPath,
      BUILDER_OPERATION_CANDIDATES: candidatePath,
      BUILDER_OPERATION_RESPONSES: responsesPath,
    });
  if (mutationReference && goReplay.status !== 0 && !goReplay.error) {
    requireSameRunInputs(sourceSnapshot, 'controlled Go mutation replay');
    reportGoCounterexample(goReplay.output, cases);
    const behavioralAssertion = /trace operation-history-[^\s]+ action \d+ READY model got status\/id=NEEDS_REPAIR\//.test(goReplay.output);
    if (!behavioralAssertion) fail('mutant failed outside the intended READY-versus-NEEDS_REPAIR Go behavioral assertion');
    console.log('PASS controlled source mutation reached the READY-versus-NEEDS_REPAIR cascade assertion.');
    console.log('Mutation evidence: ' + goReplay.output.split('\n').filter((line) => /trace operation-history-.*READY model got status\/id=NEEDS_REPAIR\//.test(line)).join('\n'));
    console.log('Evidence directory: ' + runDirectory);
    return;
  }
  if (goReplay.error || goReplay.status !== 0) {
    reportGoCounterexample(goReplay.output, cases);
    fail('Go lifecycle replay failed: ' + (goReplay.error?.message ?? 'go test exited ' + goReplay.status));
  }
  if (mutationReference) fail('controlled proposal-cascade mutation survived the intended assertion');
  requireSameRunInputs(sourceSnapshot, 'Go lifecycle replay');
  const goCheckedCandidateHash = sha256(readFileSync(candidatePath));
  const replay = validateReplaySummary(goReplay.output, cases, candidateCases, modelInventory);
  validateProposalResponses(json(responsesPath, 'Go proposal response artifact'), cases, candidateCases, modelInventory);
  const serverTestPattern = '^(' + GO_SERVER_TESTS.join('|') + ')$';
  const serverHTTP = captured('Verify the existing HTTP proposal conversion from PREVIEW_PENDING to READY with preview',
    'go', ['test', '-count=1', './internal/server', '-run', serverTestPattern, '-v'], ROOT, env);
  if (serverHTTP.error || serverHTTP.status !== 0) fail('server proposal HTTP boundary tests failed: ' + (serverHTTP.error?.message ?? 'go test exited ' + serverHTTP.status));
  for (const testName of GO_SERVER_TESTS) {
    if (!serverHTTP.output.includes('--- PASS: ' + testName) || serverHTTP.output.includes('--- SKIP: ' + testName)) {
      fail('server proposal HTTP boundary test did not run and pass: ' + testName);
    }
  }
  requireSameRunInputs(sourceSnapshot, 'server HTTP boundary checks');
  runVitest(packageRoot, vitestPath, panelReportPath, {
    ...env,
    BUILDER_OPERATION_PANEL_CHECK: '1',
    BUILDER_OPERATION_TRACES: tracePath,
    BUILDER_OPERATION_CANDIDATES: candidatePath,
    BUILDER_OPERATION_PROPOSAL_RESPONSES: responsesPath,
  }, 'Render the actual proposal response in the Builder removal panel', 0, 'panel',
  '(accounts for every generated trace and proposal checkpoint|renders the Go-resolved key cascade before Apply and preserves the raw editor request)');
  requireSameRunInputs(sourceSnapshot, 'Go-response panel verification');
  if (sha256(readFileSync(candidatePath)) !== goCheckedCandidateHash) {
    fail('panel verification modified the Go-checked candidate artifact');
  }
  console.log('Go replay summary: ' + JSON.stringify({
    traceCaseCount: replay.traceCaseCount,
    replayedCaseCount: replay.replayedCaseCount,
    skippedCaseCount: replay.skippedCaseCount,
    proposalCheckpointCount: replay.proposalCheckpointCount,
    uniqueAbstractRequestFingerprintCount: replay.uniqueAbstractRequestFingerprintCount,
    uniqueRawRequestFingerprintCount: replay.uniqueRawRequestFingerprintCount,
    readyProposalCount: replay.readyProposalCount,
    needsRepairProposalCount: replay.needsRepairProposalCount,
    applyInvocationCount: replay.applyInvocationCount,
    applyAckSuccessCount: replay.applyAckSuccessCount,
    returnedProposalIdCount: replay.returnedProposalIdCount,
    applyAckFailureCount: replay.applyAckFailureCount,
    servicePreviewCount: replay.servicePreviewCount,
  }));
  console.log('Go response panel: ' + modelInventory.proposalCount + ' service DTOs matched by trace/action; panel uses actual Go impact/candidate/preview data in a presentation fixture, not wire JSON.');
  console.log('Evidence directory: ' + runDirectory);
  console.log('\nPASS bounded model-based operation-history conformance.');
}

main().catch((error) => {
  console.error('\nFAIL bounded model-based operation-history conformance: ' + error.message);
  process.exitCode = 1;
});
