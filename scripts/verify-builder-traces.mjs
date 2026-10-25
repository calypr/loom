#!/usr/bin/env node
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  renameSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const ROOT = realpathSync(resolve(SCRIPT_DIR, '..'));
const MODEL_DIR = 'verification/builder-interactions';
const CONTRACT_RELATIVE = MODEL_DIR + '/builder-trace-contract.json';
const SOURCE_MANIFEST_RELATIVE = MODEL_DIR + '/builder-preview-contract.json';
const TLC_VERSION = '1.7.4';
const TLC_SHA256 = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88';
const TLC_URL = 'https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar';
const DEFAULT_EXPECTED_TRACE_COUNT = 69;
const DEFAULT_EXPECTED_SCHEDULE_COUNT = 69;
const DEFAULT_EXPECTED_FINDING_COUNT = 0;
const STATUSES = new Set([
  'idle',
  'previewing',
  'ready',
  'error',
  'needs-repair',
  'applying',
  'requesting',
  'succeeded',
  'stale',
  'conflict',
  'invalid',
  'failed',
]);
const MODEL_STATUSES = new Map([
  ['BuilderJourney', new Set(['idle', 'selected', 'reshaping', 'pivot-ready', 'previewing', 'ready', 'error'])],
  ['ReconcileLifecycle', new Set(['empty', 'ready', 'stale', 'error', 'previewing', 'needs-repair'])],
]);

const OBSERVATION_BOOLEAN_FIELDS = [
  'canApply',
  'proposalIdPresent',
  'previewReceiptPresent',
  'rowsPresent',
  'receiptPresent',
];
const BUILDER_JOURNEY_PREVIEW_SURFACES = new Set(['row-operation', 'main-table', 'unknown']);
const BUILDER_GROUP_LIFECYCLE_LABELS = new Set([
  'Preview',
  'PreviewComplete',
  'PreviewError',
  'LatePreviewCompleteIgnored',
  'LatePreviewErrorIgnored',
  'PreviewRequestCancelled',
  'Group',
  'EditGroup',
  'EditGroupAgain',
  'RemoveGroup',
  'GroupEditCancel',
  'GroupRemovalCancel',
  'Reload',
]);
const BUILDER_GROUP_TRACE_NAMES = new Set([
  'builder-authoring:group-entry',
  'builder-authoring:group-entry-raw-basic',
]);
const BUILDER_CDA_GROUP_RELATED_TRACE_NAME = 'cda-builder-group-related-source';
const BUILDER_CDA_EXPECTED_DISPLAYED_ROUTE_LABELS = new Map([
  [BUILDER_CDA_GROUP_RELATED_TRACE_NAME, 'Subject → Subject'],
]);
const BUILDER_CDA_GROUP_RELATED_ACTIONS = new Set([
  'Group', 'EditGroup', 'EditGroupAgain', 'GroupEditCancel', 'Reload',
  'ExpandRows', 'RelatedSourcePreview', 'RelatedSourceApply', 'RelatedSourceCancel',
]);
const BUILDER_CDA_ROW_EXPANSION_STAGES = ['SpecimenPatient', 'PatientObservation'];
const BUILDER_CDA_SNAPSHOTLESS_SETTLEMENT_LABELS = new Set([
  'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored',
  'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
]);
const BUILDER_CDA_OBSERVATIONLESS_SETTLEMENT_LABELS = new Set([
  'Preview', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
]);
const BUILDER_GROUP_PREVIEW_LABELS = new Set([
  'Preview',
  'PreviewComplete',
  'PreviewError',
  'LatePreviewCompleteIgnored',
  'LatePreviewErrorIgnored',
  'PreviewRequestCancelled',
]);
const BUILDER_GROUP_SETTLEMENT_LABELS = new Set([
  'PreviewComplete',
  'PreviewError',
  'LatePreviewCompleteIgnored',
  'LatePreviewErrorIgnored',
  'PreviewRequestCancelled',
]);
const BUILDER_GROUP_APPLY_LABELS = new Set(['Group', 'EditGroup', 'EditGroupAgain', 'RemoveGroup']);
const BUILDER_GROUP_CANCEL_LABELS = new Set(['GroupEditCancel', 'GroupRemovalCancel']);
const BUILDER_GROUP_STEP_TOKENS = ['G1', 'G2'];
const BUILDER_GROUP_CAPTURE_TOKENS = Array.from({ length: 12 }, (_, index) => 'c' + (index + 1));
const BUILDER_GROUP_KEY_TOKENS = ['ID', 'Gender', 'Race', 'Other'];
const BUILDER_CDA_OUTPUT_TOKENS = ['O0'];
const BUILDER_CDA_DIGEST_TOKENS = Array.from({ length: 8 }, (_, index) => 'D' + index);

function fail(message) {
  throw new Error(message);
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function readJson(pathOrRelative, root = ROOT) {
  return JSON.parse(readFileSync(resolve(root, pathOrRelative), 'utf8'));
}

function canonicalJson(value) {
  if (Array.isArray(value)) return '[' + value.map(canonicalJson).join(',') + ']';
  if (value && typeof value === 'object') {
    return '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + canonicalJson(value[key])).join(',') + '}';
  }
  return JSON.stringify(value);
}

function currentSourceCommit() {
  const result = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: ROOT, encoding: 'utf8' });
  if (result.error) fail('cannot resolve current Git HEAD: ' + result.error.message);
  if (result.status !== 0) fail('cannot resolve current Git HEAD: ' + (result.stderr || result.stdout || '').trim());
  const commit = result.stdout.trim();
  if (!/^[0-9a-f]{40}$/.test(commit)) fail('current Git HEAD is not a 40-character lowercase SHA');
  return commit;
}

function validateSourceProvenance(payload, contract) {
  if (payload.sourceProfile !== contract.sourceProfile) {
    fail('trace sourceProfile ' + String(payload.sourceProfile) + ' does not match contract sourceProfile ' + contract.sourceProfile);
  }
  if (typeof payload.sourceCommit !== 'string' || !/^[0-9a-f]{40}$/.test(payload.sourceCommit)) {
    fail('trace sourceCommit must be a 40-character lowercase Git SHA');
  }
  const head = currentSourceCommit();
  if (payload.sourceCommit !== head) {
    fail('trace sourceCommit ' + payload.sourceCommit + ' does not match current Git HEAD ' + head);
  }
  if (typeof payload.contractSourceCommit !== 'string' || !/^[0-9a-f]{40}$/.test(payload.contractSourceCommit)) {
    fail('trace contractSourceCommit must be a 40-character lowercase Git SHA');
  }
  if (payload.contractSourceCommit !== contract.sourceCommit) {
    fail('trace contractSourceCommit ' + payload.contractSourceCommit + ' does not match selected contract sourceCommit ' + contract.sourceCommit);
  }
  if (typeof payload.sourceFingerprint !== 'string' || !/^[0-9a-f]{64}$/.test(payload.sourceFingerprint)) {
    fail('trace sourceFingerprint must be a 64-character lowercase SHA-256');
  }

  const sourceManifest = readJson(SOURCE_MANIFEST_RELATIVE);
  if (!Array.isArray(sourceManifest.sources) || sourceManifest.sources.length === 0) {
    fail('reviewed source manifest must contain a non-empty sources array');
  }
  const seenPaths = new Set();
  const actualEntries = sourceManifest.sources.map((source, index) => {
    if (!isPlainObject(source) || typeof source.path !== 'string' || typeof source.sha256 !== 'string' ||
        !/^[0-9a-f]{64}$/.test(source.sha256)) {
      fail('reviewed source manifest sources[' + index + '] must contain path and SHA-256');
    }
    if (seenPaths.has(source.path)) fail('reviewed source manifest repeats path ' + source.path);
    seenPaths.add(source.path);
    const absolute = resolve(ROOT, source.path);
    const local = relative(ROOT, absolute);
    if (!local || local.startsWith('..') || resolve(ROOT, local) !== absolute) {
      fail('reviewed source path escapes repository root: ' + source.path);
    }
    if (!existsSync(absolute)) fail('reviewed source file is missing: ' + source.path);
    const actual = sha256(readFileSync(absolute));
    if (actual !== source.sha256) {
      fail('reviewed source SHA mismatch at ' + source.path + ': expected ' + source.sha256 + ', found ' + actual);
    }
    return { path: source.path, sha256: actual };
  }).sort((left, right) => left.path.localeCompare(right.path));
  const fingerprint = sha256(canonicalJson(actualEntries));
  if (payload.sourceFingerprint !== fingerprint) {
    fail('trace sourceFingerprint ' + payload.sourceFingerprint + ' does not match reviewed source content ' + fingerprint);
  }
  return { profile: contract.sourceProfile, head, sourceFingerprint: fingerprint, sourceCount: actualEntries.length };
}

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function groupLifecycleEnabled(contract) {
  const requiredCounts = contract.phase1?.requiredActionCountsByTrace;
  return isPlainObject(requiredCounts) &&
    Object.keys(requiredCounts).some((traceName) => BUILDER_GROUP_TRACE_NAMES.has(traceName));
}

function builderJourneyCaseForTrace(contract, trace) {
  const journeyCase = contract.journeyCase;
  return isPlainObject(journeyCase) && trace?.name === journeyCase.traceName && trace?.model === journeyCase.model
    ? journeyCase
    : null;
}

function groupProjectionProblem(group, where, allowedAggregates = ['COUNT_ROWS']) {
  if (group === null) return null;
  if (!isPlainObject(group)) return where + ' must be a Group projection or null';
  if (typeof group.stepId !== 'string' || group.stepId.trim().length === 0) {
    return where + '.stepId must be a non-empty raw string';
  }
  if (!Array.isArray(group.keys) || group.keys.length > 2 ||
      group.keys.some((key) => typeof key !== 'string' || key.trim().length === 0)) {
    return where + '.keys must be an array of at most two non-empty raw key IDs';
  }
  if (new Set(group.keys).size !== group.keys.length) return where + '.keys cannot repeat a raw key ID';
  if (!allowedAggregates.includes(group.aggregate)) {
    return where + '.aggregate must be one of the bounded Group lifecycle aggregates: ' + allowedAggregates.join(', ');
  }
  if (group.selectedKeyLabels !== undefined && (!Array.isArray(group.selectedKeyLabels) ||
      group.selectedKeyLabels.some((label) => typeof label !== 'string' || label.trim().length === 0))) {
    return where + '.selectedKeyLabels must be an array of non-empty strings when present';
  }
  return null;
}

function savedGroupProjectionProblem(saved, where, allowedAggregates = ['COUNT_ROWS']) {
  if (!isPlainObject(saved)) return where + ' must contain the actual saved Builder projection';
  if (!Object.hasOwn(saved, 'group')) return where + '.group is required';
  const groupProblem = groupProjectionProblem(saved.group, where + '.group', allowedAggregates);
  if (groupProblem) return groupProblem;
  if (!isPlainObject(saved.workspace) || !Array.isArray(saved.workspace.documents)) {
    return where + '.workspace.documents must preserve the actual Builder workspace documents';
  }
  if (typeof saved.outputId !== 'string' || saved.outputId.trim().length === 0) {
    return where + '.outputId must identify the selected Builder output';
  }
  const selectedDocuments = saved.workspace.documents.filter((document) => document?.output?.id === saved.outputId);
  if (selectedDocuments.length !== 1) {
    return where + '.outputId must select exactly one document in the raw workspace';
  }
  const construction = selectedDocuments[0]?.construction;
  const rawSteps = construction?.steps;
  const omittedEmptyConstruction = construction === undefined && saved.group === null;
  if (!Array.isArray(rawSteps) && !omittedEmptyConstruction) {
    return where + '.workspace selected document must preserve its raw construction steps';
  }
  const groupSteps = (rawSteps ?? [])
    .filter((step) => step?.operation?.kind === 'GROUP');
  if (groupSteps.length > 1) return where + '.workspace selected document must contain at most one GROUP step';
  if (saved.group === null) {
    if (groupSteps.length !== 0) return where + '.group null diverges from a GROUP step in the raw workspace';
  } else {
    const rawStep = groupSteps[0];
    if (!rawStep || rawStep.id !== saved.group.stepId) {
      return where + '.group stepId diverges from the GROUP step in the selected raw workspace document';
    }
    const rawGroup = rawStep.operation.group;
    const rawKeys = Array.isArray(rawGroup?.keys)
      ? rawGroup.keys.map((key) => typeof key === 'string' ? key : key?.inputColumnId)
      : rawGroup?.keys === undefined && saved.group.keys.length === 0 ? [] : null;
    if (!rawKeys || rawKeys.some((key) => typeof key !== 'string' || key.trim().length === 0)) {
      return where + '.workspace GROUP keys must preserve raw inputColumnId strings';
    }
    const rawAggregates = Array.isArray(rawGroup?.aggregates)
      ? rawGroup.aggregates.map((aggregate) => aggregate?.operation)
      : null;
    if (!rawKeys || canonicalJson(rawKeys) !== canonicalJson(saved.group.keys)) {
      return where + '.group keys diverge from the GROUP step in the selected raw workspace document';
    }
    if (!rawAggregates || rawAggregates.length !== 1 || rawAggregates[0] !== saved.group.aggregate) {
      return where + '.group aggregate diverges from the GROUP step in the selected raw workspace document';
    }
  }
  if (!(typeof saved.draftVersion === 'number' && Number.isFinite(saved.draftVersion)) &&
      typeof saved.draftVersion !== 'string') {
    return where + '.draftVersion must preserve the actual numeric or string version';
  }
  if (typeof saved.draftDigest !== 'string' || saved.draftDigest.trim().length === 0) {
    return where + '.draftDigest must preserve the actual digest string';
  }
  return null;
}

function cdaCreateGroupTargetStepProblem(intent, where, savedBefore = undefined) {
  if (!isPlainObject(intent) || intent.kind !== 'create') return null;
  if (typeof intent.targetStepId !== 'string' || intent.targetStepId.trim().length === 0 ||
      intent.targetStepId !== intent.proposed?.stepId) {
    return where + '.targetStepId must match the actual proposed new Group stepId';
  }
  const body = intent.rawProposalBody;
  const candidateConstruction = body?.candidateConstruction;
  const steps = candidateConstruction?.steps;
  const groupSteps = Array.isArray(steps)
    ? steps.filter((step) => step?.operation?.kind === 'GROUP')
    : [];
  const matchingGroups = groupSteps.filter((step) => step?.id === intent.targetStepId);
  if (body?.changedStepId !== intent.targetStepId || groupSteps.length !== 1 || matchingGroups.length !== 1) {
    return where + '.targetStepId must match changedStepId and candidateConstruction must contain exactly one proposed GROUP step';
  }
  if (new Set(steps.map((step) => step?.id)).size !== steps.length ||
      steps.some((step) => typeof step?.id !== 'string' || step.id.length === 0)) {
    return where + '.rawProposalBody.candidateConstruction step IDs must be unique';
  }
  if (!isPlainObject(savedBefore) || savedBefore.group !== null) {
    return where + ' create intent requires a savedBefore projection with no Group';
  }
  const selected = savedBuilderDocument(savedBefore, where + '.savedBefore');
  if (selected.problem) return selected.problem;
  const savedConstruction = selected.document.construction;
  if (!isPlainObject(savedConstruction) || !Array.isArray(savedConstruction.steps)) {
    return where + '.savedBefore must retain the actual construction before appending a new Group';
  }
  const savedSteps = savedConstruction.steps;
  if (savedSteps.some((step) => step?.id === intent.targetStepId)) {
    return where + '.targetStepId must be new and absent from the savedBefore construction';
  }
  if (steps.length !== savedSteps.length + 1 || steps[savedSteps.length]?.id !== intent.targetStepId ||
      steps[savedSteps.length]?.operation?.kind !== 'GROUP') {
    return where + '.rawProposalBody.candidateConstruction must append exactly one new GROUP after the savedBefore steps';
  }
  for (let index = 0; index < savedSteps.length; index += 1) {
    if (canonicalJson(steps[index]) !== canonicalJson(savedSteps[index])) {
      return where + '.rawProposalBody.candidateConstruction must preserve every savedBefore step before appending the new Group';
    }
  }
  const savedConstructionMetadata = structuredClone(savedConstruction);
  const candidateConstructionMetadata = structuredClone(candidateConstruction);
  delete savedConstructionMetadata.steps;
  delete candidateConstructionMetadata.steps;
  if (canonicalJson(savedConstructionMetadata) !== canonicalJson(candidateConstructionMetadata)) {
    return where + '.rawProposalBody.candidateConstruction must preserve savedBefore construction metadata';
  }
  return null;
}

function candidateIntentProblem(intent, where, allowedAggregates = ['COUNT_ROWS'], allowCdaCreateTargetStep = false, savedBefore = undefined) {
  if (!isPlainObject(intent)) return where + ' must be an object';
  if (!['create', 'edit', 'remove'].includes(intent.kind)) return where + '.kind must be create, edit, or remove';
  if (intent.targetStepId !== null && (typeof intent.targetStepId !== 'string' || intent.targetStepId.trim().length === 0)) {
    return where + '.targetStepId must be a non-empty raw step ID or null';
  }
  if (!Object.hasOwn(intent, 'base')) return where + '.base must preserve the actual pre-proposal Group';
  if (!Object.hasOwn(intent, 'proposed')) return where + '.proposed must preserve the actual candidate Group';
  if (!Object.hasOwn(intent, 'rawProposalBody') || !isPlainObject(intent.rawProposalBody)) {
    return where + '.rawProposalBody must preserve the parsed construction-proposal request body';
  }
  for (const field of ['base', 'proposed']) {
    const problem = groupProjectionProblem(intent[field], where + '.' + field, allowedAggregates);
    if (problem) return problem;
  }
  if (intent.kind === 'create') {
    if (intent.base !== null || intent.proposed === null) {
      return where + ' create must have no saved base and a proposed Group';
    }
    if (allowCdaCreateTargetStep) {
      const targetProblem = cdaCreateGroupTargetStepProblem(intent, where, savedBefore);
      if (targetProblem) return targetProblem;
    } else if (intent.targetStepId !== null) {
      return where + ' create must have a null target/base and a proposed Group';
    }
  }
  if (intent.kind === 'edit' && (intent.targetStepId === null || intent.base === null || intent.proposed === null)) {
    return where + ' edit must have a target, saved base Group, and proposed Group';
  }
  if (intent.kind === 'remove' && (intent.targetStepId === null || intent.base === null || intent.proposed !== null)) {
    return where + ' remove must have a target and saved base Group with no proposed Group';
  }
  return null;
}

function previewOwnerProjectionProblem(owner, where, nullable = false, maxMountEpoch = 2, allowBrowserRequestId = false) {
  if (nullable && owner === null) return null;
  if (!isPlainObject(owner)) return where + ' must be an actual Preview owner or null when no proposal request fired';
  if (!Number.isInteger(owner.mountEpoch) || owner.mountEpoch < 0 || owner.mountEpoch > maxMountEpoch) {
    return where + '.mountEpoch must be a finite epoch in 0..' + maxMountEpoch;
  }
  if (typeof owner.captureId !== 'string' || owner.captureId.trim().length === 0) {
    if (allowBrowserRequestId && typeof owner.browserRequestId === 'string' && owner.browserRequestId.trim().length > 0) return null;
    return where + '.captureId must preserve the actual Preview request captureId';
  }
  return null;
}

function groupLifecycleShapeProblem(action, where, required, maxMountEpoch = 2, allowBrowserRequestId = false, allowedAggregates = ['COUNT_ROWS'], requireReloadEditorProjection = true, cdaJourneyCase = false) {
  const lifecycle = action.groupLifecycle;
  if (lifecycle === undefined) {
    return required ? where + '.groupLifecycle is required by the selected Group lifecycle contract' : null;
  }
  if (!isPlainObject(lifecycle)) return where + '.groupLifecycle must be an object';
  if (!Number.isInteger(lifecycle.mountEpoch) || lifecycle.mountEpoch < 0 || lifecycle.mountEpoch > maxMountEpoch) {
    return where + '.groupLifecycle.mountEpoch must be a finite epoch in 0..' + maxMountEpoch;
  }
  if (typeof lifecycle.documentTimeOrigin !== 'number' || !Number.isFinite(lifecycle.documentTimeOrigin) ||
      lifecycle.documentTimeOrigin <= 0) {
    return where + '.groupLifecycle.documentTimeOrigin must be a positive finite browser timeOrigin';
  }

  if (BUILDER_GROUP_PREVIEW_LABELS.has(action.label)) {
    if (!isPlainObject(lifecycle.owner)) return where + '.groupLifecycle.owner is required for a Group proposal request/settlement';
    if (!Number.isInteger(lifecycle.owner.mountEpoch) || lifecycle.owner.mountEpoch < 0 || lifecycle.owner.mountEpoch > maxMountEpoch) {
      return where + '.groupLifecycle.owner.mountEpoch must be a finite epoch in 0..' + maxMountEpoch;
    }
    const ownerIdentity = lifecycle.owner.captureId ?? (allowBrowserRequestId ? lifecycle.owner.browserRequestId : null);
    if (typeof ownerIdentity !== 'string' || ownerIdentity.trim().length === 0) {
      return where + '.groupLifecycle.owner.captureId must preserve the actual request captureId';
    }
    const requestIdentity = action.request?.captureId ?? (allowBrowserRequestId ? action.request?.browserRequestId : null);
    if (typeof requestIdentity !== 'string' || ownerIdentity !== requestIdentity) {
      return where + '.groupLifecycle.owner request identity must match the actual construction-proposal request';
    }
    const settlementIdentity = action.settlement?.captureId ?? (allowBrowserRequestId ? action.settlement?.browserRequestId : null);
    if (BUILDER_GROUP_SETTLEMENT_LABELS.has(action.label) && ownerIdentity !== settlementIdentity) {
      return where + '.groupLifecycle.owner request identity must match the actual settlement';
    }
  }
  if (action.label === 'Preview') {
    const problem = candidateIntentProblem(
      lifecycle.candidateIntent, where + '.groupLifecycle.candidateIntent', allowedAggregates,
      cdaJourneyCase, lifecycle.savedBefore,
    );
    if (problem) return problem;
  }
  if (BUILDER_GROUP_SETTLEMENT_LABELS.has(action.label)) {
    const problem = candidateIntentProblem(
      lifecycle.candidateIntent, where + '.groupLifecycle.candidateIntent', allowedAggregates,
      cdaJourneyCase, lifecycle.savedBefore,
    );
    if (problem) return problem;
  }
  if (BUILDER_GROUP_APPLY_LABELS.has(action.label) || BUILDER_GROUP_CANCEL_LABELS.has(action.label) || action.label === 'Reload') {
    for (const field of ['savedBefore', 'savedAfter']) {
      const problem = savedGroupProjectionProblem(lifecycle[field], where + '.groupLifecycle.' + field, allowedAggregates);
      if (problem) return problem;
    }
  }
  if (BUILDER_GROUP_APPLY_LABELS.has(action.label) || action.label === 'GroupRemovalCancel') {
    const ownerProblemText = previewOwnerProjectionProblem(lifecycle.candidateOwner, where + '.groupLifecycle.candidateOwner', false, maxMountEpoch, allowBrowserRequestId);
    if (ownerProblemText) return ownerProblemText;
    const problem = candidateIntentProblem(
      lifecycle.candidateIntent, where + '.groupLifecycle.candidateIntent', allowedAggregates,
      cdaJourneyCase, lifecycle.savedBefore,
    );
    if (problem) return problem;
  }
  if (action.label === 'GroupEditCancel') {
    const ownerProblemText = previewOwnerProjectionProblem(
      lifecycle.candidateOwner,
      where + '.groupLifecycle.candidateOwner',
      true,
      maxMountEpoch,
      allowBrowserRequestId,
    );
    if (ownerProblemText) return ownerProblemText;
    if (!Object.hasOwn(lifecycle, 'candidateIntent')) {
      return where + '.groupLifecycle.candidateIntent must explicitly preserve the proposal intent or null';
    }
    if (lifecycle.candidateIntent !== null) {
      const problem = candidateIntentProblem(
        lifecycle.candidateIntent, where + '.groupLifecycle.candidateIntent', allowedAggregates,
        cdaJourneyCase, lifecycle.savedBefore,
      );
      if (problem) return problem;
    }
  }
  if (action.label === 'Reload') {
    if (cdaJourneyCase) {
      const mountedGroup = lifecycle.mountedGroup;
      if (mountedGroup !== null && (!isPlainObject(mountedGroup) ||
          typeof mountedGroup.stepId !== 'string' || mountedGroup.stepId.trim().length === 0 ||
          !allowedAggregates.includes(mountedGroup.aggregate))) {
        return where + '.groupLifecycle.mountedGroup must preserve its observed Group step and aggregate';
      }
    } else {
      const problem = groupProjectionProblem(lifecycle.mountedGroup, where + '.groupLifecycle.mountedGroup', allowedAggregates);
      if (problem) return problem;
    }
    if (!requireReloadEditorProjection) {
      const mountedGroupMatchesSaved = cdaJourneyCase
        ? lifecycle.savedAfter.group === null
          ? lifecycle.mountedGroup === null
          : lifecycle.mountedGroup !== null &&
            lifecycle.mountedGroup.stepId === lifecycle.savedAfter.group.stepId &&
            lifecycle.mountedGroup.aggregate === lifecycle.savedAfter.group.aggregate
        : lifecycle.mountedGroup !== null && canonicalJson({
          stepId: lifecycle.mountedGroup.stepId,
          keys: lifecycle.mountedGroup.keys,
          aggregate: lifecycle.mountedGroup.aggregate,
        }) === canonicalJson({
          stepId: lifecycle.savedAfter.group?.stepId,
          keys: lifecycle.savedAfter.group?.keys,
          aggregate: lifecycle.savedAfter.group?.aggregate,
        });
      if (!mountedGroupMatchesSaved) {
        return where + '.groupLifecycle.mountedGroup must match the actual postreload Builder Group projection';
      }
      if (!Object.hasOwn(lifecycle, 'mountedRelatedSource')) {
        return where + '.groupLifecycle.mountedRelatedSource must explicitly preserve the actual mounted related source or null';
      }
      if (lifecycle.mountedGroup !== null &&
          (lifecycle.mountedGroup.evidence !== 'construction-history-dom' ||
          typeof lifecycle.mountedGroup.historyText !== 'string' || lifecycle.mountedGroup.historyText.trim().length === 0)) {
        return where + '.groupLifecycle.mountedGroup must retain its actual construction-history DOM evidence';
      }
    } else if (lifecycle.mountedGroup !== null) {
      const target = lifecycle.clickedRowTarget;
      if (!isPlainObject(target) || target.evidence !== 'clicked-row-action-provenance' ||
          typeof target.testId !== 'string' || !target.testId.startsWith('construction-row-edit-') ||
          typeof target.stepId !== 'string' || target.stepId.trim().length === 0 ||
          target.testId !== 'construction-row-edit-' + target.stepId) {
        return where + '.groupLifecycle.clickedRowTarget must retain the clicked saved-row Edit identity';
      }
      if (lifecycle.mountedGroup.stepId !== target.stepId) {
        return where + '.groupLifecycle.mountedGroup.stepId must match its actual clicked saved-row Edit identity';
      }
      if (typeof lifecycle.mountedEditorOutputId !== 'string' ||
          lifecycle.mountedEditorOutputId !== lifecycle.savedAfter.outputId) {
        return where + '.groupLifecycle.mountedEditorOutputId must match the selected output of the mounted editor';
      }
      const selectedDocument = lifecycle.savedAfter.workspace.documents.find((document) =>
        document.output?.id === lifecycle.savedAfter.outputId);
      const expectedLabels = lifecycle.mountedGroup.keys.map((key) => {
        const column = selectedDocument?.columns?.find((candidate) =>
          (candidate.columnId ?? candidate.id) === key);
        return column ? 'Group by ' + column.label : null;
      });
      if (expectedLabels.some((label) => label === null) ||
          canonicalJson(expectedLabels) !== canonicalJson(lifecycle.mountedGroup.selectedKeyLabels)) {
        return where + '.groupLifecycle.mountedGroup selectedKeyLabels must identify its exact raw selected key IDs';
      }
    }
    if (requireReloadEditorProjection && lifecycle.savedAfter.group === null) {
      const rendered = lifecycle.renderedSourceRows;
      if (!isPlainObject(rendered) || !Array.isArray(rendered.headers) || rendered.headers.length === 0 ||
          rendered.headers.some((header) => typeof header !== 'string' || header.trim().length === 0) ||
          !Array.isArray(rendered.rawRows) || rendered.rawRows.some((row) => !Array.isArray(row) ||
            row.length !== rendered.headers.length || row.some((cell) => !isPlainObject(cell) ||
              typeof cell.text !== 'string' || (cell.title !== null && typeof cell.title !== 'string')))) {
        return where + '.groupLifecycle.renderedSourceRows must retain the raw rendered headers and cells after Group removal';
      }
    }
  }
  return null;
}

function savedBuilderDocument(saved, where) {
  if (!isPlainObject(saved) || !isPlainObject(saved.workspace) || !Array.isArray(saved.workspace.documents)) {
    return { problem: where + '.workspace.documents must preserve the actual Builder workspace documents' };
  }
  if (typeof saved.outputId !== 'string' || saved.outputId.trim().length === 0) {
    return { problem: where + '.outputId must identify the selected Builder output' };
  }
  const documents = saved.workspace.documents.filter((document) => document?.output?.id === saved.outputId);
  if (documents.length !== 1) return { problem: where + '.outputId must select exactly one document in the raw workspace' };
  return { document: documents[0] };
}

function builderColumnLabelForId(document, columnId, where) {
  if (typeof columnId !== 'string' || columnId.trim().length === 0) {
    return { problem: where + ' must preserve a non-empty raw column ID' };
  }
  if (!isPlainObject(document) || !Array.isArray(document.columns)) {
    return { problem: where + '.document.columns must preserve the raw source column list' };
  }
  const sourceMatches = document.columns
    .filter((column) => (column?.columnId ?? column?.id) === columnId)
    .map((column) => ({ label: column.label, source: 'document.columns' }));
  if (sourceMatches.length > 1) {
    return { problem: where + ' raw column ID must not be duplicated in document.columns' };
  }
  const matches = [...sourceMatches];
  const steps = Array.isArray(document.construction?.steps) ? document.construction.steps : [];
  for (const step of steps) {
    for (const output of Array.isArray(step?.outputs) ? step.outputs : []) {
      if (output?.id === columnId) matches.push({ label: output.label, source: 'construction step outputs' });
    }
  }
  if (matches.length === 0) {
    return { problem: where + ' raw column ID must exist in document.columns or a construction step output' };
  }
  if (matches.some((match) => typeof match.label !== 'string' || match.label.trim().length === 0)) {
    return { problem: where + ' every exact raw column ID match must retain a non-empty label' };
  }
  const labels = new Set(matches.map((match) => match.label));
  if (labels.size !== 1) {
    return { problem: where + ' exact raw column ID matches have conflicting labels' };
  }
  return { label: matches[0].label };
}

function mountedGroupKeyLabelsProblem(mountedGroup, savedGroup, document, where) {
  if (savedGroup === null) {
    return mountedGroup === null ? null : where + ' must be null when the saved Builder projection has no Group';
  }
  if (!isPlainObject(mountedGroup) || !Array.isArray(mountedGroup.keyLabels) ||
      !Array.isArray(savedGroup?.keys)) {
    return where + '.keyLabels must preserve the actual displayed Group labels for the saved key IDs';
  }
  const expectedLabels = [];
  for (const key of savedGroup.keys) {
    const result = builderColumnLabelForId(document, key, where + '.keyLabels');
    if (result.problem) return result.problem;
    expectedLabels.push(result.label);
  }
  if (canonicalJson(mountedGroup.keyLabels) !== canonicalJson(expectedLabels) ||
      typeof mountedGroup.historyText !== 'string' || mountedGroup.historyText.trim().length === 0 ||
      expectedLabels.some((label) => !mountedGroup.historyText.includes(label))) {
    return where + '.keyLabels must match actual construction-history labels joined to saved source/output IDs';
  }
  return null;
}

function relatedSourceProjectionFromSteps(steps, columns, group, where, journeyCase) {
  if (!Array.isArray(steps)) return { problem: where + '.construction.steps must preserve the raw selected construction steps' };
  const relatedSteps = steps.filter((step) => step?.operation?.kind === 'RELATED_SOURCE');
  if (relatedSteps.length > 1) return { problem: where + ' must contain at most one RELATED_SOURCE step' };
  if (relatedSteps.length === 0) return { relatedSource: null };
  if (!isPlainObject(group)) return { problem: where + ' RELATED_SOURCE must be bound to a persisted Group step' };
  const step = relatedSteps[0];
  const related = step.operation.relatedSource;
  if (typeof step.id !== 'string' || step.id.trim().length === 0 || !isPlainObject(related)) {
    return { problem: where + ' RELATED_SOURCE step must preserve its raw step ID and relatedSource operation' };
  }
  if (!Array.isArray(step.inputs) || step.inputs.length !== 1 ||
      step.inputs[0]?.kind !== 'STEP_OUTPUT' || step.inputs[0]?.stepId !== group.stepId) {
    return { problem: where + '.inputs must bind the RELATED_SOURCE step to the actual Group output stepId' };
  }
  const source = related.source;
  if (!isPlainObject(source) || source.resourceType !== journeyCase.relatedSource.resourceType ||
      source.path !== journeyCase.relatedSource.fieldPath ||
      typeof source.candidateId !== 'string' || source.candidateId.trim().length === 0 ||
      typeof source.nodeId !== 'string' || source.nodeId.trim().length === 0) {
    return { problem: where + ' source must preserve the Observation.status candidate and node identities' };
  }
  if (!Array.isArray(related.route) || related.route.length !== journeyCase.relatedSource.sourceRoute.length ||
      related.route.some((hop, index) => hop?.fromResourceType !== journeyCase.relatedSource.sourceRoute[index].fromResourceType ||
        hop?.toResourceType !== journeyCase.relatedSource.sourceRoute[index].toResourceType)) {
    return { problem: where + '.route must preserve the exact Specimen-to-Patient-to-Observation route' };
  }
  if (related.form !== journeyCase.relatedSource.form ||
      related.contributorRule?.policy !== journeyCase.relatedSource.contributorPolicy ||
      (related.rowValuePolicy ?? 'ALL') !== journeyCase.relatedSource.rowValuePolicy) {
    return { problem: where + ' must preserve the native ALL form, ALL_MATCHES contributor rule, and ALL row-value policy' };
  }
  if (typeof related.choiceId !== 'string' || related.choiceId.trim().length === 0 ||
      typeof related.outputColumnId !== 'string' || related.outputColumnId.trim().length === 0) {
    return { problem: where + ' must preserve the native choice identity and output column identity' };
  }
  const constructionOutputMatches = steps.flatMap((candidateStep) =>
    (Array.isArray(candidateStep?.outputs) ? candidateStep.outputs : [])
      .filter((candidateOutput) => candidateOutput?.id === related.outputColumnId)
      .map((candidateOutput) => ({ stepId: candidateStep.id, output: candidateOutput })));
  if (constructionOutputMatches.length !== 1 || constructionOutputMatches[0].stepId !== step.id) {
    return { problem: where + '.construction.steps outputs must identify the RELATED_SOURCE outputColumnId exactly once' };
  }
  const output = constructionOutputMatches[0].output;
  if (typeof output.name !== 'string' || typeof output.label !== 'string' || typeof output.type !== 'string') {
    return { problem: where + ' RELATED_SOURCE output must preserve its name, label, and logical type' };
  }
  if (columns !== undefined) {
    if (!Array.isArray(columns)) return { problem: where + '.document.columns must preserve the raw column identity list' };
    const outputLabel = builderColumnLabelForId(
      { columns, construction: { steps } }, related.outputColumnId, where + '.RELATED_SOURCE outputColumnId',
    );
    if (outputLabel.problem) return outputLabel;
    if (outputLabel.label !== output.label) {
      return { problem: where + ' RELATED_SOURCE output label conflicts with the same raw ID in document.columns or construction outputs' };
    }
  }
  return {
    relatedSource: {
      stepId: step.id,
      inputStepId: step.inputs[0].stepId,
      source: structuredClone(source),
      route: structuredClone(related.route),
      choiceId: related.choiceId,
      form: related.form,
      contributorPolicy: related.contributorRule.policy,
      rowValuePolicy: related.rowValuePolicy ?? null,
      outputColumnId: related.outputColumnId,
      output: structuredClone(output),
    },
  };
}

function relatedSourceSnapshotProblem(saved, where, journeyCase, requireGroupProjection = true, allowedAggregates = ['COUNT_ROWS', 'COUNT_DISTINCT']) {
  if (requireGroupProjection) {
    const problem = savedGroupProjectionProblem(saved, where, allowedAggregates);
    if (problem) return { problem };
  }
  const selected = savedBuilderDocument(saved, where);
  if (selected.problem) return selected;
  if (!Array.isArray(selected.document.columns)) {
    return { problem: where + '.workspace.documents selected document must preserve its raw columns array' };
  }
  const steps = selected.document.construction?.steps ?? [];
  const groupSteps = steps.filter((step) => step?.operation?.kind === 'GROUP');
  if (groupSteps.length > 1) return { problem: where + ' must contain at most one GROUP step' };
  const group = requireGroupProjection
    ? saved.group
    : groupSteps.length === 0 ? null : { stepId: groupSteps[0].id };
  const derived = relatedSourceProjectionFromSteps(steps, selected.document.columns, group, where, journeyCase);
  if (derived.problem) return derived;
  if (requireGroupProjection) {
    if (!Object.hasOwn(saved, 'relatedSource')) return { problem: where + '.relatedSource must explicitly preserve the actual related binding or null' };
    if (canonicalJson(saved.relatedSource) !== canonicalJson(derived.relatedSource)) {
      return { problem: where + '.relatedSource projection does not match the selected raw Builder workspace document' };
    }
  }
  return { relatedSource: derived.relatedSource, document: selected.document };
}

function cdaCASProblem(saved, where) {
  if (!isPlainObject(saved)) return where + ' must contain an actual saved Builder projection';
  if (!Object.hasOwn(saved, 'group') || !Object.hasOwn(saved, 'relatedSource') ||
      typeof saved.explorerId !== 'string' || saved.explorerId.trim().length === 0 ||
      !Number.isInteger(saved.draftVersion) || saved.draftVersion < 0 ||
      typeof saved.draftDigest !== 'string' || saved.draftDigest.trim().length === 0 ||
      typeof saved.outputId !== 'string' || saved.outputId.trim().length === 0) {
    return where + ' must preserve explorerId, group, relatedSource, outputId, draftVersion, and draftDigest';
  }
  return null;
}

function cdaEventTimeSnapshotUnavailable(action, lifecycle) {
  return BUILDER_CDA_SNAPSHOTLESS_SETTLEMENT_LABELS.has(action?.label) &&
    (action?.eventTimeSnapshot?.available === false || lifecycle?.eventTimeSnapshot?.available === false);
}

function cdaEventTimeMarkerUnavailable(action, lifecycle) {
  return action?.eventTimeSnapshot?.available === false || lifecycle?.eventTimeSnapshot?.available === false;
}

function cdaSnapshotPairUnavailableProblem(action, lifecycle, where) {
  const missingBefore = lifecycle?.savedBefore === null || lifecycle?.savedBefore === undefined;
  const missingAfter = lifecycle?.savedAfter === null || lifecycle?.savedAfter === undefined;
  if (missingBefore && missingAfter && cdaEventTimeSnapshotUnavailable(action, lifecycle)) return null;
  if (missingBefore || missingAfter) {
    return where + ' may omit event-time saved snapshots only when eventTimeSnapshot.available is false on a state-preserving preview settlement';
  }
  return null;
}

function cdaEventTimeObservationUnavailable(action, lifecycle) {
  return cdaEventTimeMarkerUnavailable(action, lifecycle) &&
    BUILDER_CDA_OBSERVATIONLESS_SETTLEMENT_LABELS.has(action?.label);
}

function cdaCASProjection(saved, tokens, where) {
  const problem = cdaCASProblem(saved, where);
  if (problem) return { problem };
  const workspaceKey = saved.explorerId;
  const outputToken = tokens.outputTokens.get(saved.outputId);
  const versionStep = tokens.versionSteps.get(saved.draftVersion);
  const digestToken = tokens.digestTokens.get(saved.draftDigest);
  if (!tokens.workspaceTokens.has(workspaceKey) || !outputToken || versionStep === undefined || !digestToken) {
    return { problem: where + ' CAS fields do not map to the finite observed-run token sequence' };
  }
  return {
    value: '[workspaceToken |-> ' + tlaString(tokens.workspaceTokens.get(workspaceKey)) +
      ', outputToken |-> ' + tlaString(outputToken) +
      ', versionStep |-> ' + versionStep +
      ', digestToken |-> ' + tlaString(digestToken) + ']',
  };
}

function sameSavedDraft(left, right) {
  return canonicalJson({
    explorerId: left?.explorerId ?? null,
    outputId: left?.outputId ?? null,
    draftVersion: left?.draftVersion ?? null,
    draftDigest: left?.draftDigest ?? null,
    workspace: left?.workspace ?? null,
  }) === canonicalJson({
    explorerId: right?.explorerId ?? null,
    outputId: right?.outputId ?? null,
    draftVersion: right?.draftVersion ?? null,
    draftDigest: right?.draftDigest ?? null,
    workspace: right?.workspace ?? null,
  });
}

function relatedCandidateIntentProblem(intent, lifecycle, where, journeyCase) {
  if (!isPlainObject(intent) || !['add', 'remove'].includes(intent.kind)) {
    return where + '.candidateIntent.kind must be add or remove';
  }
  if (typeof intent.targetStepId !== 'string' || intent.targetStepId.trim().length === 0) {
    return where + '.candidateIntent.targetStepId must preserve the actual related step identity';
  }
  if (!Object.hasOwn(intent, 'base') || !Object.hasOwn(intent, 'proposed') ||
      !isPlainObject(intent.rawProposalBody)) {
    return where + '.candidateIntent must preserve base, proposed, and rawProposalBody';
  }
  const before = lifecycle.savedBefore;
  const beforeSnapshot = relatedSourceSnapshotProblem(before, where + '.savedBefore', journeyCase, true, ['COUNT_ROWS', 'COUNT_DISTINCT']);
  if (beforeSnapshot.problem) return beforeSnapshot.problem;
  if (canonicalJson(intent.base) !== canonicalJson(beforeSnapshot.relatedSource)) {
    return where + '.candidateIntent.base must match the actual saved related-source binding';
  }
  if (canonicalJson(intent.rawProposalBody) !== canonicalJson(lifecycle.request?.request)) {
    return where + '.candidateIntent.rawProposalBody must match the actual construction-proposal request body';
  }
  const body = intent.rawProposalBody;
  if (body.outputId !== before.outputId) {
    return where + '.candidateIntent.rawProposalBody.outputId must match the actual selected Builder output';
  }
  if (intent.kind === 'add') {
    if (intent.base !== null || !isPlainObject(intent.proposed)) {
      return where + ' add intent must have no related-source base and a proposed related-source binding';
    }
    const candidateSteps = body.candidateConstruction?.steps;
    if (!Array.isArray(candidateSteps) || body.changedStepId !== intent.targetStepId ||
        intent.proposed.stepId !== intent.targetStepId) {
      return where + ' add intent must bind its targetStepId to the actual changed RELATED_SOURCE step';
    }
    const rawGroupSteps = candidateSteps.filter((step) => step?.operation?.kind === 'GROUP');
    if (rawGroupSteps.length !== 1 || rawGroupSteps[0].id !== before.group?.stepId) {
      return where + ' add proposal must retain the actual Group step as its input';
    }
    const candidate = relatedSourceProjectionFromSteps(candidateSteps, undefined, before.group,
      where + '.candidateIntent.rawProposalBody.candidateConstruction', journeyCase);
    if (candidate.problem) return candidate.problem;
    if (!candidate.relatedSource || canonicalJson(candidate.relatedSource) !== canonicalJson(intent.proposed)) {
      return where + '.candidateIntent.proposed must match the native candidate construction source binding';
    }
    const responseSteps = lifecycle.response.candidateConstruction?.steps;
    const responseCandidate = relatedSourceProjectionFromSteps(responseSteps, undefined, before.group,
      where + '.response.candidateConstruction', journeyCase);
    if (responseCandidate.problem) return responseCandidate.problem;
    if (canonicalJson(responseCandidate.relatedSource) !== canonicalJson(intent.proposed)) {
      return where + '.response candidate construction must preserve the requested related-source binding';
    }
    const requestRelatedStep = candidateSteps.find((step) => step?.id === intent.targetStepId);
    const responseRelatedStep = responseSteps.find((step) => step?.id === intent.targetStepId);
    if (canonicalJson(requestRelatedStep) !== canonicalJson(responseRelatedStep)) {
      return where + ' native request/response candidate RELATED_SOURCE steps do not match';
    }
  } else {
    if (!isPlainObject(intent.base) || intent.proposed !== null ||
        intent.targetStepId !== intent.base.stepId ||
        !Array.isArray(body.removeStepIds) || body.removeStepIds.length !== 1 ||
        body.removeStepIds[0] !== intent.targetStepId) {
      return where + ' remove intent must target exactly the saved RELATED_SOURCE step';
    }
    if (Array.isArray(body.candidateConstruction?.steps) &&
        body.candidateConstruction.steps.some((step) => step?.operation?.kind === 'RELATED_SOURCE')) {
      return where + ' remove proposal candidate must omit the targeted RELATED_SOURCE step';
    }
    const selected = savedBuilderDocument(before, where + '.savedBefore');
    if (selected.problem) return selected.problem;
    const beforeSteps = selected.document.construction?.steps ?? [];
    const expectedCandidateSteps = beforeSteps.filter((step) => step.id !== intent.targetStepId);
    const responseSteps = lifecycle.response.candidateConstruction?.steps;
    if (!Array.isArray(responseSteps) || responseSteps.some((step) => step?.operation?.kind === 'RELATED_SOURCE') ||
        canonicalJson(responseSteps) !== canonicalJson(expectedCandidateSteps)) {
      return where + '.response removal candidate must restore the exact saved Group construction';
    }
  }
  return null;
}

function relatedSourceLifecycleShapeProblem(action, where, required, journeyCase) {
  const lifecycle = action.relatedSourceLifecycle;
  if (lifecycle === undefined) {
    return required ? where + '.relatedSourceLifecycle is required for the selected CDA related-source journey' : null;
  }
  if (!isPlainObject(lifecycle)) return where + '.relatedSourceLifecycle must be an object';
  if (!Number.isInteger(lifecycle.mountEpoch) || lifecycle.mountEpoch < 0 || lifecycle.mountEpoch > journeyCase.maxMountEpoch) {
    return where + '.relatedSourceLifecycle.mountEpoch must be a finite observed epoch in 0..' + journeyCase.maxMountEpoch;
  }
  if (typeof lifecycle.documentTimeOrigin !== 'number' || !Number.isFinite(lifecycle.documentTimeOrigin) ||
      lifecycle.documentTimeOrigin <= 0) {
    return where + '.relatedSourceLifecycle.documentTimeOrigin must preserve the positive browser timeOrigin';
  }
  if (!isPlainObject(lifecycle.owner) || lifecycle.owner.mountEpoch !== lifecycle.mountEpoch ||
      !observedRequestKey(lifecycle.owner) || typeof lifecycle.owner.documentTimeOrigin !== 'number' ||
      lifecycle.owner.documentTimeOrigin !== lifecycle.documentTimeOrigin) {
    return where + '.relatedSourceLifecycle.owner must preserve the actual request identity, mount epoch, and document time origin';
  }
  const requestProblemText = requestProblem(lifecycle.request, where + '.relatedSourceLifecycle.request');
  if (requestProblemText) return requestProblemText;
  if (lifecycle.request.method !== 'POST' || lifecycle.request.path !== '/construction-proposals') {
    return where + '.relatedSourceLifecycle.request must preserve the native construction-proposal request';
  }
  const responseProblemText = responseProblem(lifecycle.response, where + '.relatedSourceLifecycle.response');
  if (responseProblemText) return responseProblemText;
  const settlementProblemText = settlementProblem(lifecycle.settlement, where + '.relatedSourceLifecycle.settlement');
  if (settlementProblemText) return settlementProblemText;
  const identityProblem = actualRequestIdentityProblem(
    lifecycle.owner,
    lifecycle.request,
    lifecycle.settlement,
    where + '.relatedSourceLifecycle',
  );
  if (identityProblem) return identityProblem;
  if (action.label === 'RelatedSourcePreview' &&
      (!Number.isInteger(lifecycle.settlementObservedEpoch) ||
       lifecycle.settlementObservedEpoch !== action.observedEpoch ||
       lifecycle.settlementObservedEpoch !== lifecycle.mountEpoch)) {
    return where + '.relatedSourceLifecycle.settlementObservedEpoch must match the actual response-settlement document epoch';
  }
  if (lifecycle.settlement.kind !== 'http-response' ||
      typeof lifecycle.response.status !== 'number' || lifecycle.response.status < 200 || lifecycle.response.status >= 300 ||
      lifecycle.response.proposalPreviewStatus !== 'READY') {
    return where + '.relatedSourceLifecycle must retain a successful READY proposal response and settlement';
  }
  const savedBeforeProblem = relatedSourceSnapshotProblem(lifecycle.savedBefore,
    where + '.relatedSourceLifecycle.savedBefore', journeyCase);
  if (savedBeforeProblem.problem) return savedBeforeProblem.problem;
  const savedAfterProblem = relatedSourceSnapshotProblem(lifecycle.savedAfter,
    where + '.relatedSourceLifecycle.savedAfter', journeyCase);
  if (savedAfterProblem.problem) return savedAfterProblem.problem;
  if (canonicalJson(lifecycle.savedGroupInput) !== canonicalJson(lifecycle.savedBefore.group) ||
      lifecycle.savedBefore.group?.stepId !== lifecycle.savedAfter.group?.stepId) {
    return where + '.relatedSourceLifecycle.savedGroupInput must match the actual stable saved Group projection';
  }
  const intentProblem = relatedCandidateIntentProblem(
    lifecycle.candidateIntent,
    lifecycle,
    where + '.relatedSourceLifecycle',
    journeyCase,
  );
  if (intentProblem) return intentProblem;
  const renderedObservation = lifecycle.renderedObservation;
  const responsePreview = lifecycle.response.preview;
  if (!Object.hasOwn(lifecycle, 'renderedObservation')) {
    return where + '.relatedSourceLifecycle.renderedObservation must be retained explicitly as a matching observation or null';
  }
  if (renderedObservation !== null) {
    const rendered = renderedObservation?.proposalPanel;
    const panelObservedAt = renderedObservation?.proposalPanelObservedAt;
    const panelBrowserRequestId = renderedObservation?.proposalPanelBrowserRequestId;
    const renderedMount = lifecycle.renderedObservationMount;
    const eventTimeSnapshot = lifecycle.eventTimeSnapshot;
    if (!isPlainObject(renderedObservation) || !isPlainObject(renderedObservation.builder) ||
        !isPlainObject(rendered) || !Object.keys(rendered).length || rendered.status !== 'ready' ||
        typeof rendered.id !== 'string' || rendered.id !== lifecycle.response.proposalId ||
        !Array.isArray(rendered.rows) ||
        typeof responsePreview?.rowCount !== 'number' || rendered.rows.length !== responsePreview.rowCount ||
        rendered.rows.some((row) => !Array.isArray(row) || row.length === 0 || row.some((cell) =>
          !isPlainObject(cell) || typeof cell.text !== 'string' ||
          (cell.title !== null && typeof cell.title !== 'string'))) ||
        eventTimeSnapshot?.available !== true || !Number.isFinite(panelObservedAt) ||
        panelObservedAt < lifecycle.settlement.completedAt ||
        panelBrowserRequestId !== lifecycle.owner.browserRequestId ||
        !isPlainObject(renderedMount) || renderedMount.mountEpoch !== lifecycle.settlementObservedEpoch ||
        renderedMount.documentTimeOrigin !== eventTimeSnapshot.observationMount?.documentTimeOrigin ||
        renderedMount.documentTimeOrigin !== eventTimeSnapshot.responseMount?.documentTimeOrigin ||
        renderedMount.documentTimeOrigin !== lifecycle.owner.documentTimeOrigin) {
      return where + '.relatedSourceLifecycle rendered candidate-ready observation must be a later DOM read owned by its actual settlement request and mount';
    }
  }
  if (typeof lifecycle.response.proposalId !== 'string' || lifecycle.response.proposalId.trim().length === 0 ||
      lifecycle.response.preview?.receiptId !== lifecycle.response.proposalId ||
      lifecycle.response.preview?.rowsPresent !== true ||
      lifecycle.response.preview?.outputId !== lifecycle.response.proposalOutputId) {
    return where + '.relatedSourceLifecycle response preview must retain its proposal, receipt, output, and rows binding';
  }
  if (action.label === 'RelatedSourcePreview' && action.request !== undefined) {
    if (action.request.browserRequestId !== lifecycle.request.browserRequestId ||
        action.request.captureId !== lifecycle.request.captureId ||
        canonicalJson(action.request.request) !== canonicalJson(lifecycle.request.request)) {
      return where + ' action request must match the retained RELATED_SOURCE proposal request';
    }
    if (canonicalJson(action.response) !== canonicalJson(lifecycle.response) ||
        canonicalJson(action.settlement) !== canonicalJson(lifecycle.settlement)) {
      return where + ' action response and settlement must match the retained RELATED_SOURCE proposal request';
    }
    if (action.observation == null) {
      if (!cdaEventTimeObservationUnavailable(action, lifecycle)) {
        return where + ' RelatedSourcePreview may omit its response-event observation only with the explicit unavailable marker';
      }
    } else if (!isPlainObject(action.observation)) {
      return where + ' RelatedSourcePreview must preserve its actual response-event observation or explicit null marker';
    }
  }
  if (['RelatedSourceApply', 'RelatedSourceCancel'].includes(action.label)) {
    const observed = action.observation;
    if (!isPlainObject(observed) || !['idle', 'absent', 'unknown'].includes(observed.status) ||
        observed.canApply !== false || observed.proposalIdPresent !== false ||
        observed.previewReceiptPresent !== false || observed.rowsPresent !== false || observed.receiptPresent !== false ||
        observed.proposalId !== null || observed.previewReceiptId !== null || observed.previewOutputId !== null) {
      return where + ' ' + action.label + ' must retain a separate actual closed proposal observation';
    }
  }
  return null;
}

function rowExpansionLifecycleShapeProblem(action, where, required, journeyCase) {
  const lifecycle = action.rowExpansionLifecycle;
  if (lifecycle === undefined) {
    return required ? where + '.rowExpansionLifecycle is required for the observed CDA row expansion' : null;
  }
  if (!isPlainObject(lifecycle) || !journeyCase.rowExpansionStages.includes(lifecycle.stage)) {
    return where + '.rowExpansionLifecycle.stage must be one of the ordered CDA row expansion stages';
  }
  if (typeof lifecycle.stepId !== 'string' || lifecycle.stepId.trim().length === 0 ||
      !(lifecycle.inputStepId === null || (typeof lifecycle.inputStepId === 'string' && lifecycle.inputStepId.trim().length > 0)) ||
      !Array.isArray(lifecycle.route) || lifecycle.route.length !== 1) {
    return where + '.rowExpansionLifecycle must preserve its step, input step, and one-hop route';
  }
  const expectedRoute = lifecycle.stage === 'SpecimenPatient'
    ? { fromResourceType: 'Specimen', toResourceType: 'Patient' }
    : { fromResourceType: 'Patient', toResourceType: 'Observation' };
  if (lifecycle.route[0]?.fromResourceType !== expectedRoute.fromResourceType ||
      lifecycle.route[0]?.toResourceType !== expectedRoute.toResourceType) {
    return where + '.rowExpansionLifecycle.route does not match its declared stage';
  }
  if (!Number.isInteger(lifecycle.mountEpoch) || lifecycle.mountEpoch < 0 ||
      lifecycle.mountEpoch > journeyCase.maxMountEpoch || !isPlainObject(lifecycle.owner) ||
      lifecycle.owner.mountEpoch !== lifecycle.mountEpoch || !observedRequestKey(lifecycle.owner) ||
      typeof lifecycle.owner.documentTimeOrigin !== 'number' || lifecycle.owner.documentTimeOrigin <= 0) {
    return where + '.rowExpansionLifecycle must preserve its bounded observed mountEpoch and actual request identity';
  }
  const requestProblemText = requestProblem(lifecycle.request, where + '.rowExpansionLifecycle.request');
  if (requestProblemText) return requestProblemText;
  if (lifecycle.request.method !== 'POST' || lifecycle.request.path !== '/construction-proposals' ||
      typeof lifecycle.request.request?.candidateConstruction !== 'object') {
    return where + '.rowExpansionLifecycle.request must preserve the native candidate construction body';
  }
  const previewSettlement = new Set([
    'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
  ]).has(action.label);
  const isSettledAction = previewSettlement || action.label === 'ExpandRows';
  if (lifecycle.response !== undefined && lifecycle.response !== null) {
    const responseProblemText = responseProblem(lifecycle.response, where + '.rowExpansionLifecycle.response');
    if (responseProblemText) return responseProblemText;
  } else if (['PreviewComplete', 'LatePreviewCompleteIgnored', 'ExpandRows'].includes(action.label)) {
    return where + '.rowExpansionLifecycle.response is required for a successful proposal response';
  }
  const identityProblem = actualRequestIdentityProblem(lifecycle.owner, lifecycle.request,
    previewSettlement ? action.settlement : undefined,
    where + '.rowExpansionLifecycle');
  if (identityProblem) return identityProblem;
  if (isSettledAction && lifecycle.renderedObservation == null &&
      (action.label === 'PreviewComplete' || !cdaEventTimeSnapshotUnavailable(action, lifecycle))) {
    return where + '.rowExpansionLifecycle.renderedObservation may be null only when eventTimeSnapshot.available is false';
  }
  if (isSettledAction && lifecycle.renderedObservation != null &&
      (!isPlainObject(lifecycle.renderedObservation) || !Object.keys(lifecycle.renderedObservation).length)) {
    return where + '.rowExpansionLifecycle.renderedObservation must preserve the actual proposal DOM';
  }
  const unavailablePairProblem = cdaSnapshotPairUnavailableProblem(action, lifecycle, where + '.rowExpansionLifecycle');
  if (unavailablePairProblem) return unavailablePairProblem;
  if (lifecycle.savedBefore !== null && lifecycle.savedBefore !== undefined) {
    for (const field of ['savedBefore', 'savedAfter']) {
      const snapshot = lifecycle[field];
      const problem = cdaCASProblem(snapshot, where + '.rowExpansionLifecycle.' + field);
      if (problem) return problem;
      const selected = savedBuilderDocument(snapshot, where + '.rowExpansionLifecycle.' + field);
      if (selected.problem) return selected.problem;
      const relation = relatedSourceSnapshotProblem(snapshot, where + '.rowExpansionLifecycle.' + field, journeyCase, true);
      if (relation.problem) return relation.problem;
      if (snapshot.group !== null || relation.relatedSource !== null) {
        return where + '.rowExpansionLifecycle ' + field + ' cannot invent a RELATED_SOURCE before the Group journey';
      }
    }
  }
  return null;
}

function cdaGroupCandidateResponseProblem(intent, savedBefore, requestedConstruction, responseConstruction, where) {
  const mismatch = where + ' native response candidate must match the actual proposal request construction';
  const countRowsEdit = intent?.kind === 'edit' && intent.base?.aggregate === 'COUNT_DISTINCT' &&
    intent.proposed?.aggregate === 'COUNT_ROWS';
  if (!countRowsEdit && canonicalJson(responseConstruction) === canonicalJson(requestedConstruction)) return null;
  if (!isPlainObject(intent) || intent.kind !== 'edit' || intent.base?.aggregate !== 'COUNT_DISTINCT' ||
      intent.proposed?.aggregate !== 'COUNT_ROWS' || !isPlainObject(savedBefore) ||
      savedBefore.group?.aggregate !== 'COUNT_DISTINCT' ||
      canonicalJson(intent.base) !== canonicalJson(savedBefore.group) ||
      canonicalJson(intent.rawProposalBody?.candidateConstruction) !== canonicalJson(requestedConstruction) ||
      !Array.isArray(requestedConstruction?.steps) || !Array.isArray(responseConstruction?.steps) ||
      requestedConstruction.steps.length !== responseConstruction.steps.length) {
    return mismatch;
  }
  const beforeDocument = savedBuilderDocument(savedBefore, where + '.savedBefore');
  if (beforeDocument.problem) return beforeDocument.problem;
  const savedConstructionSteps = beforeDocument.document.construction?.steps ?? [];
  const savedGroupSteps = savedConstructionSteps.filter((step) =>
    step?.id === savedBefore.group.stepId && step?.operation?.kind === 'GROUP');
  const requestedGroupSteps = requestedConstruction.steps.filter((step) =>
    step?.id === intent.targetStepId && step?.operation?.kind === 'GROUP');
  const responseGroupSteps = responseConstruction.steps.filter((step) =>
    step?.id === intent.targetStepId && step?.operation?.kind === 'GROUP');
  if (savedGroupSteps.length !== 1 || requestedGroupSteps.length !== 1 || responseGroupSteps.length !== 1 ||
      intent.targetStepId !== savedBefore.group.stepId || intent.proposed.stepId !== intent.targetStepId) {
    return mismatch;
  }
  const savedGroupStep = savedGroupSteps[0];
  const requestedGroupStep = requestedGroupSteps[0];
  const responseGroupStep = responseGroupSteps[0];
  const savedGroupOperation = savedGroupStep.operation.group;
  const requestedGroupOperation = requestedGroupStep.operation.group;
  const responseGroupOperation = responseGroupStep.operation.group;
  const savedAggregates = savedGroupOperation?.aggregates ?? [];
  const requestedAggregates = requestedGroupOperation?.aggregates ?? [];
  const responseAggregates = responseGroupOperation?.aggregates ?? [];
  if (savedAggregates.length !== 1 || savedAggregates[0]?.operation !== 'COUNT_DISTINCT' ||
      requestedAggregates.length !== 1 || requestedAggregates[0]?.operation !== 'COUNT_ROWS' ||
      responseAggregates.length !== 1 || responseAggregates[0]?.operation !== 'COUNT_ROWS' ||
      requestedAggregates[0]?.inputColumnId !== undefined ||
      canonicalJson(requestedGroupStep) !== canonicalJson(responseGroupStep)) {
    return mismatch;
  }
  const affectedOutputId = savedAggregates[0].outputColumnId;
  if (typeof affectedOutputId !== 'string' || requestedAggregates[0].outputColumnId !== affectedOutputId ||
      responseAggregates[0].outputColumnId !== affectedOutputId) {
    return mismatch;
  }
  const savedKeys = (savedGroupOperation?.keys ?? []).map((key) => typeof key === 'string' ? key : key?.inputColumnId);
  const requestedKeys = (requestedGroupOperation?.keys ?? []).map((key) => typeof key === 'string' ? key : key?.inputColumnId);
  const proposedKeys = (intent.proposed.keys ?? []).map((key) => typeof key === 'string' ? key : key?.inputColumnId);
  if (!Array.isArray(savedGroupOperation?.keys) || !Array.isArray(requestedGroupOperation?.keys) ||
      canonicalJson(savedKeys) !== canonicalJson(requestedKeys) ||
      canonicalJson(requestedKeys) !== canonicalJson(proposedKeys)) {
    return mismatch;
  }
  const oldGroupOperation = structuredClone(savedGroupOperation);
  const newGroupOperation = structuredClone(requestedGroupOperation);
  delete oldGroupOperation.aggregates;
  delete newGroupOperation.aggregates;
  const oldStepWithoutOutputs = structuredClone(savedGroupStep);
  const requestedStepWithoutOutputs = structuredClone(requestedGroupStep);
  delete oldStepWithoutOutputs.outputs;
  delete requestedStepWithoutOutputs.outputs;
  oldStepWithoutOutputs.operation.group = oldGroupOperation;
  requestedStepWithoutOutputs.operation.group = newGroupOperation;
  if (canonicalJson(oldStepWithoutOutputs) !== canonicalJson(requestedStepWithoutOutputs)) return mismatch;

  const oldAggregateShape = structuredClone(savedAggregates[0]);
  const requestedAggregateShape = structuredClone(requestedAggregates[0]);
  delete oldAggregateShape.operation;
  delete oldAggregateShape.inputColumnId;
  delete requestedAggregateShape.operation;
  delete requestedAggregateShape.inputColumnId;
  if (canonicalJson(oldAggregateShape) !== canonicalJson(requestedAggregateShape)) return mismatch;

  const savedGroupOutputs = savedGroupStep.outputs ?? [];
  const requestedGroupOutputs = requestedGroupStep.outputs ?? [];
  if (savedGroupOutputs.length !== requestedGroupOutputs.length) return mismatch;
  const savedAffectedGroupOutputs = savedGroupOutputs.filter((output) => output?.id === affectedOutputId);
  if (savedAffectedGroupOutputs.length !== 1) return mismatch;
  let changedAggregateOutput = false;
  for (let outputIndex = 0; outputIndex < savedGroupOutputs.length; outputIndex += 1) {
    const savedOutput = savedGroupOutputs[outputIndex];
    const requestedOutput = requestedGroupOutputs[outputIndex];
    if (savedOutput?.id !== requestedOutput?.id) return mismatch;
    if (savedOutput.id === affectedOutputId) {
      const savedComparable = { ...savedOutput };
      const requestedComparable = { ...requestedOutput };
      delete savedComparable.name;
      delete savedComparable.label;
      delete requestedComparable.name;
      delete requestedComparable.label;
      if (canonicalJson(savedComparable) !== canonicalJson(requestedComparable)) return mismatch;
      changedAggregateOutput = true;
    } else if (canonicalJson(savedOutput) !== canonicalJson(requestedOutput)) {
      return mismatch;
    }
  }
  if (!changedAggregateOutput) return mismatch;
  const groupOutputMatches = requestedGroupOutputs.filter((output) => output?.id === affectedOutputId);
  const responseGroupOutputMatches = (responseGroupStep.outputs ?? []).filter((output) => output?.id === affectedOutputId);
  if (groupOutputMatches.length !== 1 || responseGroupOutputMatches.length !== 1 ||
      canonicalJson(groupOutputMatches[0]) !== canonicalJson(responseGroupOutputMatches[0])) return mismatch;
  const groupOutput = responseGroupOutputMatches[0];
  const groupStepIndex = requestedConstruction.steps.indexOf(requestedGroupStep);
  const savedGroupStepIndex = savedConstructionSteps.indexOf(savedGroupStep);
  const savedRelatedSteps = savedConstructionSteps.filter((step) => step?.operation?.kind === 'RELATED_SOURCE' &&
    Array.isArray(step.inputs) && step.inputs.length === 1 && step.inputs[0]?.kind === 'STEP_OUTPUT' &&
    step.inputs[0]?.stepId === intent.targetStepId &&
    (step.outputs ?? []).some((output) => output?.id === affectedOutputId));
  if (savedRelatedSteps.length !== 1 || savedConstructionSteps.indexOf(savedRelatedSteps[0]) <= savedGroupStepIndex) {
    return mismatch;
  }
  const savedRelatedOutputMatches = (savedRelatedSteps[0].outputs ?? []).filter((output) =>
    output?.id === affectedOutputId);
  if (savedRelatedOutputMatches.length !== 1 || typeof savedRelatedOutputMatches[0].name !== 'string' ||
      typeof savedRelatedOutputMatches[0].label !== 'string' ||
      savedRelatedOutputMatches[0].id !== savedAffectedGroupOutputs[0].id) {
    return mismatch;
  }
  const savedRelatedOutput = savedRelatedOutputMatches[0];
  const relatedStepIndexes = [];
  for (let stepIndex = groupStepIndex + 1; stepIndex < requestedConstruction.steps.length; stepIndex += 1) {
    const step = requestedConstruction.steps[stepIndex];
    const inputs = step?.inputs;
    if (step?.id === savedRelatedSteps[0].id && step?.operation?.kind === 'RELATED_SOURCE' &&
        Array.isArray(inputs) && inputs.length === 1 &&
        inputs[0]?.kind === 'STEP_OUTPUT' && inputs[0]?.stepId === intent.targetStepId &&
        (step.outputs ?? []).some((output) => output?.id === affectedOutputId)) {
      relatedStepIndexes.push(stepIndex);
    }
  }
  if (relatedStepIndexes.length !== 1) return mismatch;
  const relatedStepIndex = relatedStepIndexes[0];

  for (let stepIndex = 0; stepIndex < requestedConstruction.steps.length; stepIndex += 1) {
    const requestedStep = requestedConstruction.steps[stepIndex];
    const responseStep = responseConstruction.steps[stepIndex];
    if (!isPlainObject(requestedStep) || !isPlainObject(responseStep) ||
        !Array.isArray(requestedStep.outputs) || !Array.isArray(responseStep.outputs) ||
        requestedStep.outputs.length !== responseStep.outputs.length) {
      return mismatch;
    }
    const requestedRest = { ...requestedStep };
    const responseRest = { ...responseStep };
    delete requestedRest.outputs;
    delete responseRest.outputs;
    if (canonicalJson(requestedRest) !== canonicalJson(responseRest)) return mismatch;
    for (let outputIndex = 0; outputIndex < requestedStep.outputs.length; outputIndex += 1) {
      const requestedOutput = requestedStep.outputs[outputIndex];
      const responseOutput = responseStep.outputs[outputIndex];
      if (requestedOutput?.id !== responseOutput?.id) return mismatch;
      if (stepIndex === relatedStepIndex && requestedOutput.id === affectedOutputId) {
        if (requestedOutput.name !== savedRelatedOutput.name || requestedOutput.label !== savedRelatedOutput.label) {
          return where + ' request RELATED_SOURCE carried Group output name/label must match the saved raw RELATED_SOURCE output';
        }
        const requestedComparable = { ...requestedOutput };
        const responseComparable = { ...responseOutput };
        delete requestedComparable.name;
        delete requestedComparable.label;
        delete responseComparable.name;
        delete responseComparable.label;
        if (canonicalJson(requestedComparable) !== canonicalJson(responseComparable) ||
            responseOutput.name !== groupOutput.name || responseOutput.label !== groupOutput.label) {
          return mismatch;
        }
      } else if (canonicalJson(requestedOutput) !== canonicalJson(responseOutput)) {
        return mismatch;
      }
    }
  }
  return null;
}

function cdaGroupLifecycleSnapshotProblem(action, where, journeyCase) {
  const lifecycle = action.groupLifecycle;
  if (!isPlainObject(lifecycle)) return null;
  if (BUILDER_GROUP_PREVIEW_LABELS.has(action.label)) {
    if (!isPlainObject(lifecycle.owner) || lifecycle.owner.mountEpoch !== lifecycle.mountEpoch ||
        lifecycle.owner.documentTimeOrigin !== lifecycle.documentTimeOrigin) {
      return where + '.groupLifecycle.owner must retain the request-start mount epoch and document time origin';
    }
    if (journeyCase) {
      const targetProblem = cdaCreateGroupTargetStepProblem(
        lifecycle.candidateIntent,
        where + '.groupLifecycle.candidateIntent',
        lifecycle.savedBefore,
      );
      if (targetProblem) return targetProblem;
    }
    const rawBodyProblem = candidateIntentRawBodyProblem(
      lifecycle.candidateIntent,
      where + '.groupLifecycle.candidateIntent',
    );
    if (rawBodyProblem) return rawBodyProblem;
    if (!isPlainObject(action.request?.request) ||
        canonicalJson(lifecycle.candidateIntent.rawProposalBody) !== canonicalJson(action.request.request)) {
      return where + '.groupLifecycle.candidateIntent.rawProposalBody must match the actual native proposal request body';
    }
    if (action.response?.candidateConstruction !== undefined) {
      const responseCandidateProblem = cdaGroupCandidateResponseProblem(
        lifecycle.candidateIntent,
        lifecycle.savedBefore,
        action.request.request.candidateConstruction,
        action.response.candidateConstruction,
        where + '.groupLifecycle',
      );
      if (responseCandidateProblem) return responseCandidateProblem;
    }
  }
  const unavailablePairProblem = cdaSnapshotPairUnavailableProblem(action, lifecycle, where + '.groupLifecycle');
  if (unavailablePairProblem) return unavailablePairProblem;
  if ((lifecycle.savedBefore === null || lifecycle.savedBefore === undefined) &&
      (lifecycle.savedAfter === null || lifecycle.savedAfter === undefined)) return null;
  const before = lifecycle.savedBefore;
  const after = lifecycle.savedAfter;
  if (!isPlainObject(before) || !isPlainObject(after)) {
    return where + '.groupLifecycle must preserve actual savedBefore and savedAfter Builder projections';
  }
  for (const [field, saved] of [['savedBefore', before], ['savedAfter', after]]) {
    const snapshot = relatedSourceSnapshotProblem(saved, where + '.groupLifecycle.' + field, journeyCase);
    if (snapshot.problem) return snapshot.problem;
    const casProblem = cdaCASProblem(saved, where + '.groupLifecycle.' + field);
    if (casProblem) return casProblem;
  }
  if (before.explorerId !== after.explorerId) {
    return where + '.groupLifecycle cannot change the owning Explorer across one observed action';
  }
  if (['Preview', 'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
      'GroupEditCancel', 'Reload'].includes(action.label) && !sameSavedDraft(before, after)) {
    return where + '.groupLifecycle ' + action.label + ' must preserve the exact saved Builder workspace and draft CAS';
  }
  if (['Group', 'EditGroup', 'EditGroupAgain', 'RemoveGroup'].includes(action.label) &&
      canonicalJson(before.relatedSource) !== canonicalJson(after.relatedSource)) {
    return where + '.groupLifecycle Group mutation must retain the exact related-source binding';
  }
  if (['Group', 'EditGroup', 'EditGroupAgain'].includes(action.label)) {
    const intent = lifecycle.candidateIntent;
    const candidateSteps = intent?.rawProposalBody?.candidateConstruction?.steps;
    if (!Array.isArray(candidateSteps)) {
      return where + '.groupLifecycle candidate must retain the complete native construction proposal';
    }
    const candidateRelated = relatedSourceProjectionFromSteps(candidateSteps, undefined,
      intent.proposed, where + '.groupLifecycle candidateConstruction', journeyCase);
    if (candidateRelated.problem) return candidateRelated.problem;
    if (canonicalJson(candidateRelated.relatedSource) !== canonicalJson(before.relatedSource)) {
      return where + '.groupLifecycle Group proposal must preserve the exact preexisting related-source binding';
    }
    const beforeDocument = savedBuilderDocument(before, where + '.groupLifecycle.savedBefore');
    if (beforeDocument.problem) return beforeDocument.problem;
    const savedRelatedStep = beforeDocument.document.construction?.steps?.find((step) =>
      step?.operation?.kind === 'RELATED_SOURCE') ?? null;
    const candidateRelatedStep = candidateSteps.find((step) => step?.operation?.kind === 'RELATED_SOURCE') ?? null;
    if (canonicalJson(savedRelatedStep) !== canonicalJson(candidateRelatedStep)) {
      return where + '.groupLifecycle Group candidate must retain the exact related-source step from the saved workspace';
    }
  }
  if (action.label === 'Reload') {
    const mountedGroup = lifecycle.mountedGroup;
    const afterDocument = savedBuilderDocument(after, where + '.groupLifecycle.savedAfter');
    if (afterDocument.problem) return afterDocument.problem;
    const mountedGroupProblem = mountedGroupKeyLabelsProblem(
      mountedGroup, after.group, afterDocument.document, where + '.groupLifecycle.mountedGroup',
    );
    if (mountedGroupProblem) return mountedGroupProblem;
    const mountedProblem = mountedRelatedSourceProblem(
      lifecycle.mountedRelatedSource,
      after.relatedSource,
      where + '.groupLifecycle.mountedRelatedSource',
      journeyCase,
    );
    if (mountedProblem) return mountedProblem;
  }
  return null;
}

function mountedRelatedSourceProblem(mounted, savedRelatedSource, where, journeyCase) {
  if (savedRelatedSource === null) {
    return mounted === null ? null : where + ' must be null when the postreload workspace has no related source';
  }
  const expectedDisplayedRouteLabel = BUILDER_CDA_EXPECTED_DISPLAYED_ROUTE_LABELS.get(journeyCase.traceName);
  if (!isPlainObject(mounted) ||
      typeof mounted.stepId !== 'string' || mounted.stepId !== savedRelatedSource.stepId ||
      typeof mounted.inputStepId !== 'string' || mounted.inputStepId !== savedRelatedSource.inputStepId ||
      typeof mounted.outputColumnId !== 'string' || mounted.outputColumnId !== savedRelatedSource.outputColumnId ||
      mounted.source?.resourceType !== journeyCase.relatedSource.resourceType ||
      mounted.source?.path !== journeyCase.relatedSource.fieldPath ||
      mounted.sourceEvidence !== 'construction-history-button-title-attribute' ||
      !Array.isArray(mounted.route) ||
      canonicalJson(mounted.route) !== canonicalJson(savedRelatedSource.route) ||
      mounted.routeEvidence !== 'saved-builder-projection' ||
      canonicalJson(mounted.route.map((hop) => ({
        fromResourceType: hop?.fromResourceType,
        toResourceType: hop?.toResourceType,
      }))) !== canonicalJson(journeyCase.relatedSource.sourceRoute) ||
      typeof expectedDisplayedRouteLabel !== 'string' ||
      mounted.displayedRouteLabel !== expectedDisplayedRouteLabel ||
      mounted.displayedRouteEvidence !== 'construction-history-button-title-attribute' ||
      !isPlainObject(mounted.visibleColumn) || mounted.visibleColumn.id !== savedRelatedSource.outputColumnId ||
      mounted.visibleColumn.visible !== true || typeof mounted.visibleColumn.label !== 'string' ||
      mounted.visibleColumn.label !== savedRelatedSource.output.label ||
      typeof (mounted.visibleColumn.evidence ?? mounted.visibleColumn.source) !== 'string' ||
      (mounted.visibleColumn.evidence ?? mounted.visibleColumn.source).trim().length === 0 ||
      typeof mounted.historyText !== 'string' || mounted.historyText.trim().length === 0 ||
      !mounted.historyText.includes(journeyCase.relatedSource.resourceType + '.' + journeyCase.relatedSource.fieldPath) ||
      !mounted.historyText.includes(expectedDisplayedRouteLabel)) {
    return where + ' must bind the actual displayed route title and visible output to the saved full route/source projection';
  }
  return null;
}

function cdaNativePathSuffix(path) {
  if (typeof path !== 'string') return null;
  const authoringPrefix = '/authoring/v2';
  const marker = authoringPrefix + '/';
  const markerIndex = path.lastIndexOf(marker);
  if (markerIndex >= 0) return path.slice(markerIndex + authoringPrefix.length);
  return path.startsWith('/') ? path : null;
}

function cdaNativePathBase(path) {
  if (typeof path !== 'string') return null;
  const marker = '/authoring/v2/';
  const markerIndex = path.lastIndexOf(marker);
  return markerIndex > 0 ? path.slice(0, markerIndex) : null;
}

function cdaLatePreviewObservationProblem(trace, action, actionIndex) {
  const panel = action.renderedObservation?.proposalPanel;
  const responseEventObservation = action.responseEventObservation;
  const responseEventPanel = responseEventObservation?.proposalPanel;
  const classification = action.lateClassification;
  const panelObservedAt = classification?.proposalPanelObservedAt;
  const panelBrowserRequestId = action.renderedObservation?.proposalPanelBrowserRequestId;
  const eventSnapshot = action.eventTimeSnapshot;
  const responseMount = eventSnapshot?.responseMount;
  const observationMount = eventSnapshot?.observationMount;
  const newerOwnerRequest = Array.isArray(trace.nativeRequests)
    ? trace.nativeRequests.find(request => request.browserRequestId === classification?.ownerBrowserRequestId)
    : null;
  const newerOwnerStartIndex = trace.actions.findIndex((candidate, candidateIndex) =>
    candidateIndex < actionIndex &&
    ['Preview', 'RelatedSourcePreview'].includes(candidate.label) &&
    candidate.request?.browserRequestId === classification?.ownerBrowserRequestId);
  const newerOwnerStart = newerOwnerStartIndex >= 0 ? trace.actions[newerOwnerStartIndex] : null;
  const newerOwnerLifecycle = newerOwnerStart?.relatedSourceLifecycle ??
    newerOwnerStart?.groupLifecycle ?? newerOwnerStart?.rowExpansionLifecycle;
  const newerOwner = newerOwnerLifecycle?.owner;
  const newerOwnerSettlement = trace.actions.find(candidate =>
    candidate.request?.browserRequestId === classification?.ownerBrowserRequestId &&
    ['PreviewComplete', 'RelatedSourcePreview'].includes(candidate.label));
  const newerOwnerCompletionTimes = [
    newerOwnerRequest?.completedAt,
    newerOwnerRequest?.responseReceivedAt,
    newerOwnerSettlement?.settlement?.completedAt,
    newerOwnerSettlement?.settlement?.responseReceivedAt,
  ].filter(Number.isFinite);
  const staleSettlementCompletedAt = Number.isFinite(action.settlement?.completedAt)
    ? action.settlement.completedAt
    : action.settlement?.responseReceivedAt;
  const invalid = classification?.available !== true || !isPlainObject(panel) ||
    typeof panel.id !== 'string' || panel.id.length === 0 || panel.status !== 'ready' ||
    !isPlainObject(action.renderedObservation?.builder) ||
    !isPlainObject(responseEventObservation) || !isPlainObject(responseEventPanel) ||
    responseEventPanel.id !== panel.id || responseEventPanel.status !== panel.status ||
    !Number.isFinite(responseEventObservation.proposalPanelObservedAt) ||
    responseEventObservation.proposalPanelObservedAt !== panelObservedAt ||
    responseEventObservation.proposalPanelBrowserRequestId !== classification.ownerBrowserRequestId ||
    !Number.isFinite(action.renderedObservation?.proposalPanelObservedAt) ||
    action.renderedObservation.proposalPanelObservedAt !== classification.proposalPanelObservedAt ||
    panelBrowserRequestId !== classification.ownerBrowserRequestId ||
    classification.ownerBrowserRequestId === action.request?.browserRequestId ||
    classification.renderedPanelProposalId !== panel.id ||
    typeof classification.ownerBrowserRequestId !== 'string' ||
    !Number.isFinite(panelObservedAt) ||
    !Number.isFinite(classification.ownerStartedAt) || !Number.isFinite(classification.ownerCompletedAt) ||
    classification.ownerStartedAt <= action.request.startedAt ||
    !Number.isFinite(staleSettlementCompletedAt) || classification.ownerStartedAt >= staleSettlementCompletedAt ||
    newerOwnerRequest?.method !== 'POST' ||
    cdaNativePathSuffix(newerOwnerRequest?.path) !== '/construction-proposals' ||
    newerOwnerRequest.isMutation !== false ||
    newerOwnerRequest.startedAt !== classification.ownerStartedAt ||
    newerOwnerRequest.index <= action.request.index ||
    !newerOwnerStart ||
    newerOwnerStart.request?.browserRequestId !== newerOwnerRequest.browserRequestId ||
    newerOwnerStart.request?.startedAt !== classification.ownerStartedAt ||
    newerOwner?.browserRequestId !== newerOwnerRequest.browserRequestId ||
    !Number.isInteger(newerOwner?.mountEpoch) ||
    newerOwner.mountEpoch !== newerOwnerLifecycle?.mountEpoch ||
    newerOwner.documentTimeOrigin !== newerOwnerLifecycle?.documentTimeOrigin ||
    typeof newerOwner.documentTimeOrigin !== 'number' || !Number.isFinite(newerOwner.documentTimeOrigin) ||
    newerOwner.documentTimeOrigin <= 0 ||
    !newerOwnerSettlement ||
    newerOwnerSettlement.response?.status < 200 || newerOwnerSettlement.response?.status >= 300 ||
    newerOwnerSettlement.response?.proposalId !== panel.id ||
    newerOwnerSettlement.response?.proposalPreviewStatus !== 'READY' ||
    !newerOwnerCompletionTimes.includes(classification.ownerCompletedAt) ||
    classification.ownerCompletedAt < classification.ownerStartedAt ||
    classification.ownerCompletedAt > panelObservedAt ||
    panelObservedAt < staleSettlementCompletedAt ||
    eventSnapshot?.available !== true ||
    !Number.isInteger(responseMount?.mountEpoch) ||
    responseMount.mountEpoch !== action.observedEpoch ||
    typeof responseMount.documentTimeOrigin !== 'number' ||
    !Number.isFinite(responseMount.documentTimeOrigin) || responseMount.documentTimeOrigin <= 0 ||
    !Number.isInteger(observationMount?.mountEpoch) ||
    observationMount.mountEpoch !== action.observedEpoch ||
    typeof observationMount.documentTimeOrigin !== 'number' ||
    !Number.isFinite(observationMount.documentTimeOrigin) || observationMount.documentTimeOrigin <= 0 ||
    observationMount.documentTimeOrigin !== responseMount.documentTimeOrigin ||
    !Number.isFinite(eventSnapshot.observedAt) || eventSnapshot.observedAt !== panelObservedAt ||
    action.renderedObservationMount !== undefined &&
      (!isPlainObject(action.renderedObservationMount) ||
       action.renderedObservationMount.mountEpoch !== observationMount.mountEpoch ||
       action.renderedObservationMount.documentTimeOrigin !== observationMount.documentTimeOrigin) ||
    action.label === 'LatePreviewCompleteIgnored' &&
      (!isPlainObject(action.response) || action.response.status < 200 || action.response.status >= 300 ||
       action.response.proposalPreviewStatus !== 'READY' || action.settlement?.kind !== 'http-response') ||
    action.label === 'LatePreviewErrorIgnored' &&
      !(action.response === null && action.settlement?.kind === 'request-failed') &&
      !(isPlainObject(action.response) &&
        (action.response.status < 200 || action.response.status >= 300 ||
         action.response.proposalPreviewStatus !== 'READY') && action.settlement?.kind === 'http-response');
  return invalid
    ? 'late preview transition must retain actual newer owner, request/settlement chronology, and event-time DOM mounts'
    : null;
}

function cdaMutatingRequestCoverageProblem(trace, where, journeyCase, contract) {
  if (!Array.isArray(trace.nativeRequests)) {
    return { message: where + '.nativeRequests must preserve the complete native observer request ledger for CDA coverage' };
  }
  const modeledRequestIDs = new Map();
  const wireRequestIDs = new Map();
  const browserRequestIDs = new Map();
  const captureRequestIDs = new Map();
  const unmodeledRequestIDs = new Set();
  const classifiedNativeRequests = new Map();
  const observedRequestStarts = [];
  const previewStarts = new Map();
  const previewSettlements = new Map();
  const settlementTimingProblem = (request, settlement, actionIndex, label) => {
    const completedAt = Number.isFinite(settlement?.completedAt)
      ? settlement.completedAt
      : settlement?.responseReceivedAt;
    if (!isPlainObject(settlement) || !Number.isFinite(settlement.startedAt) ||
        !Number.isFinite(completedAt) || settlement.startedAt !== request.startedAt ||
        completedAt < request.startedAt) {
      return where + '.actions[' + actionIndex + '] ' + label + ' settlement must preserve matching request start and later completion times';
    }
    return null;
  };
  const registerRequest = (request, actionIndex, label) => {
    const requestID = observedRequestKey(request);
    if (typeof request.isMutation !== 'boolean') {
      return where + '.actions[' + actionIndex + '] ' + label + ' request must preserve the actual observer mutation flag';
    }
    if (!requestID) return where + '.actions[' + actionIndex + '] ' + label + ' must preserve its actual captureId or browserRequestId';
    if (typeof request.browserRequestId !== 'string' || request.browserRequestId.length === 0 ||
        !Number.isInteger(request.index) || request.index < 0 ||
        typeof request.startedAt !== 'number' || !Number.isFinite(request.startedAt) || request.startedAt <= 0) {
      return where + '.actions[' + actionIndex + '] ' + label + ' must preserve its native observer index and request start time';
    }
    const prior = modeledRequestIDs.get(requestID);
    if (prior) {
      return where + '.actions[' + actionIndex + '] mutating request identity ' + requestID +
        ' is already mapped to ' + prior.label + ' at action ' + prior.actionIndex;
    }
    modeledRequestIDs.set(requestID, { label, actionIndex });
    if (browserRequestIDs.has(request.browserRequestId)) {
      return where + '.actions[' + actionIndex + '] browserRequestId duplicates another observed native request';
    }
    browserRequestIDs.set(request.browserRequestId, { label, actionIndex });
    if (typeof request.captureId === 'string' && request.captureId.length > 0) {
      if (captureRequestIDs.has(request.captureId)) {
        return where + '.actions[' + actionIndex + '] captureId duplicates another observed native request';
      }
      captureRequestIDs.set(request.captureId, { label, actionIndex });
    }
    const wireRequestID = request.requestId;
    if (typeof wireRequestID === 'string' && wireRequestID.length > 0) {
      const priorWireRequest = wireRequestIDs.get(wireRequestID);
      if (priorWireRequest) {
        return where + '.actions[' + actionIndex + '] wire requestId is already mapped to ' +
          priorWireRequest.label + ' at action ' + priorWireRequest.actionIndex;
      }
      wireRequestIDs.set(wireRequestID, {
        label, actionIndex, method: request.method, path: request.path, isMutation: request.isMutation,
        readOnlySupportKind: null,
      });
    }
    classifiedNativeRequests.set(request.browserRequestId, {
      method: request.method,
      path: request.path,
      index: request.index,
      startedAt: request.startedAt,
      requestId: request.requestId,
      isMutation: request.isMutation,
      label,
      actionIndex,
    });
    observedRequestStarts.push({ index: request.index, startedAt: request.startedAt, label, actionIndex });
    return null;
  };
  const previewSettlementLabels = new Set([
    'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
  ]);
  const applyLabels = new Set(['Group', 'GroupAgain', 'EditGroup', 'EditGroupAgain', 'RemoveGroup', 'ExpandRows', 'RelatedSourceApply']);
  for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
    const action = trace.actions[actionIndex];
    const actionWhere = where + '.actions[' + actionIndex + ']';
    if (action.label === 'Preview') {
      if (action.request?.method !== 'POST' || action.request?.path !== '/construction-proposals') {
        return { message: actionWhere + ' Preview must preserve the native construction-proposals request', actionIndex };
      }
      const requestID = observedRequestKey(action.request);
      if (!requestID) return { message: actionWhere + ' Preview has no actual request identity', actionIndex };
      if (previewStarts.has(requestID)) {
        return { message: actionWhere + ' Preview request identity duplicates a prior proposal request', actionIndex };
      }
      const problem = registerRequest(action.request, actionIndex, 'Preview');
      if (problem) return { message: problem, actionIndex };
      previewStarts.set(requestID, { action, actionIndex });
    } else if (previewSettlementLabels.has(action.label)) {
      const requestID = observedRequestKey(action.request);
      const start = previewStarts.get(requestID);
      if (!start) return { message: actionWhere + ' proposal settlement has no modeled Preview request owner', actionIndex };
      if (previewSettlements.has(requestID)) {
        return { message: actionWhere + ' request identity has more than one proposal settlement', actionIndex };
      }
      previewSettlements.set(requestID, actionIndex);
    } else if (action.label === 'RelatedSourcePreview') {
      if (action.request?.method !== 'POST' || action.request?.path !== '/construction-proposals') {
        return { message: actionWhere + ' RelatedSourcePreview must preserve the native construction-proposals request', actionIndex };
      }
      const problem = registerRequest(action.request, actionIndex, 'RelatedSourcePreview');
      if (problem) return { message: problem, actionIndex };
      const timingProblem = settlementTimingProblem(action.request, action.settlement, actionIndex, 'RelatedSourcePreview');
      if (timingProblem) return { message: timingProblem, actionIndex };
    } else if (applyLabels.has(action.label)) {
      if (action.request?.method !== 'POST' || action.request.path !== '/commands') {
        return { message: actionWhere + ' Apply must preserve the native /commands POST request', actionIndex };
      }
      const problem = registerRequest(action.request, actionIndex, action.label + ' Apply');
      if (problem) return { message: problem, actionIndex };
      const timingProblem = settlementTimingProblem(action.request, action.settlement, actionIndex, action.label + ' Apply');
      if (timingProblem) return { message: timingProblem, actionIndex };
    }
  }
  for (const [requestID, start] of previewStarts) {
    if (!previewSettlements.has(requestID)) {
      return { message: where + '.actions[' + start.actionIndex + '] Preview request has no modeled response settlement', actionIndex: start.actionIndex };
    }
  }
  for (const event of trace.unmodeledEvents ?? []) {
    const eventKind = event?.kind;
    const supportRoute = contract.readOnlySupportRoutes?.[eventKind];
    if (!journeyCase.readOnlySupportKinds.includes(eventKind) || !contract.unmodeledEventPolicy?.[eventKind] ||
        !supportRoute || typeof event.reason !== 'string' || event.reason.trim().length === 0) {
      return { message: where + '.unmodeledEvents may contain only allowlisted support kinds with an explicit reason' };
    }
    const supportRequests = Array.isArray(event.requests)
      ? event.requests
      : [isPlainObject(event.request) ? event.request : event];
    if (supportRequests.length === 0 || supportRequests.some((request) => !isPlainObject(request))) {
      return { message: where + '.unmodeledEvents[' + eventKind + '].requests must retain each actual support request' };
    }
    for (const supportRequest of supportRequests) {
      const requestID = observedRequestKey(supportRequest);
      const method = supportRequest.method;
      const path = supportRequest.path;
      if (typeof supportRequest.browserRequestId !== 'string' || supportRequest.browserRequestId.length === 0 ||
          !Number.isInteger(supportRequest.index) || supportRequest.index < 0 ||
          typeof supportRequest.startedAt !== 'number' || !Number.isFinite(supportRequest.startedAt) ||
          supportRequest.startedAt <= 0 || supportRequest.isMutation !== false || method !== supportRoute.method ||
          path !== supportRoute.pathSuffix || !requestID) {
        return { message: where + '.unmodeledEvents[' + eventKind + '] request must be independently marked non-mutating and match its method/path allowlist' };
      }
      if (modeledRequestIDs.has(requestID) || unmodeledRequestIDs.has(requestID)) {
        return { message: where + '.unmodeledEvents request identity duplicates a modeled mutating request' };
      }
      const wireRequestID = supportRequest.requestId;
      if (browserRequestIDs.has(supportRequest.browserRequestId)) {
        return { message: where + '.unmodeledEvents browserRequestId duplicates another observed native request' };
      }
      browserRequestIDs.set(supportRequest.browserRequestId, { label: eventKind, actionIndex: -1 });
      if (typeof supportRequest.captureId === 'string' && supportRequest.captureId.length > 0) {
        if (captureRequestIDs.has(supportRequest.captureId)) {
          return { message: where + '.unmodeledEvents captureId duplicates another observed native request' };
        }
        captureRequestIDs.set(supportRequest.captureId, { label: eventKind, actionIndex: -1 });
      }
      if (typeof wireRequestID === 'string' && wireRequestID.length > 0) {
        const priorWireRequest = wireRequestIDs.get(wireRequestID);
        if (priorWireRequest) {
          const allowedReconcileReuse = eventKind === 'draft-reconcile-read' &&
            priorWireRequest.readOnlySupportKind === 'draft-reconcile-read' &&
            supportRoute.method === 'POST' && supportRoute.pathSuffix === '/reconcile' &&
            method === priorWireRequest.method && path === priorWireRequest.path &&
            method === 'POST' && path === '/reconcile' && supportRequest.isMutation === false &&
            priorWireRequest.isMutation === false;
          if (!allowedReconcileReuse) {
            return { message: where + '.unmodeledEvents wire requestId may be reused only by distinct read-only POST /reconcile support requests' };
          }
        } else {
          wireRequestIDs.set(wireRequestID, {
            label: eventKind, actionIndex: -1, method, path, isMutation: false,
            readOnlySupportKind: eventKind,
          });
        }
      }
      unmodeledRequestIDs.add(requestID);
      classifiedNativeRequests.set(supportRequest.browserRequestId, {
        method,
        path,
        index: supportRequest.index,
        startedAt: supportRequest.startedAt,
        requestId: supportRequest.requestId,
        isMutation: false,
        label: eventKind,
        actionIndex: -1,
      });
      observedRequestStarts.push({ index: supportRequest.index, startedAt: supportRequest.startedAt, label: eventKind, actionIndex: -1 });
      modeledRequestIDs.set(requestID, { label: eventKind, actionIndex: -1 });
    }
  }
  const nativeBrowserIDs = new Set();
  const nativeWireIDs = new Map();
  const nativePathBases = new Set();
  const supportRoutesBySuffix = new Map(Object.entries(contract.readOnlySupportRoutes ?? {})
    .map(([kind, route]) => [route.method + ' ' + route.pathSuffix, kind]));
  for (const [nativeIndex, entry] of trace.nativeRequests.entries()) {
    if (!isPlainObject(entry) || typeof entry.browserRequestId !== 'string' || entry.browserRequestId.length === 0 ||
        !Number.isInteger(entry.index) || entry.index < 0 || typeof entry.startedAt !== 'number' ||
        !Number.isFinite(entry.startedAt) || entry.startedAt <= 0 || typeof entry.method !== 'string' ||
        typeof entry.path !== 'string' || typeof entry.isMutation !== 'boolean') {
      return { message: where + '.nativeRequests[' + nativeIndex + '] must preserve native browserRequestId, index, start time, method, path, and isMutation' };
    }
    if (nativeBrowserIDs.has(entry.browserRequestId)) {
      return { message: where + '.nativeRequests browserRequestId must be unique across the complete observer ledger' };
    }
    nativeBrowserIDs.add(entry.browserRequestId);
    const pathSuffix = cdaNativePathSuffix(entry.path);
    if (!pathSuffix) return { message: where + '.nativeRequests[' + nativeIndex + '].path must be an owned absolute authoring route' };
    const pathBase = cdaNativePathBase(entry.path);
    if (pathBase !== null) nativePathBases.add(pathBase);
    const classified = classifiedNativeRequests.get(entry.browserRequestId);
    const modeledProposalRequest = !entry.isMutation && entry.method === 'POST' &&
      pathSuffix === '/construction-proposals' &&
      ['Preview', 'RelatedSourcePreview'].includes(classified?.label);
    if (entry.isMutation) {
      if (entry.method !== 'POST' || pathSuffix !== '/commands') {
        return { message: where + '.nativeRequests contains an unknown mutating request; all mutating routes must be modeled' };
      }
    } else if (!modeledProposalRequest && !supportRoutesBySuffix.has(entry.method + ' ' + pathSuffix)) {
      return { message: where + '.nativeRequests contains an unknown read-only method/path route outside the support allowlist' };
    }
    if (!classified) {
      return { message: where + '.nativeRequests contains an observed request with no modeled action or explicit read-only support binding' };
    }
    if (typeof entry.requestId === 'string' && entry.requestId.length > 0) {
      const supportKind = supportRoutesBySuffix.get(entry.method + ' ' + pathSuffix);
      const readOnlySupportKind = !entry.isMutation && !modeledProposalRequest &&
        journeyCase.readOnlySupportKinds.includes(supportKind) && classified.label === supportKind
        ? supportKind
        : null;
      const priorWireRequest = nativeWireIDs.get(entry.requestId);
      if (priorWireRequest) {
        const allowedReconcileReuse = readOnlySupportKind === 'draft-reconcile-read' &&
          priorWireRequest.readOnlySupportKind === 'draft-reconcile-read' &&
          entry.method === priorWireRequest.method && pathSuffix === priorWireRequest.pathSuffix &&
          entry.method === 'POST' && pathSuffix === '/reconcile' &&
          entry.isMutation === false && priorWireRequest.isMutation === false;
        if (!allowedReconcileReuse) {
          return { message: where + '.nativeRequests wire requestId may be reused only by distinct read-only POST /reconcile support requests' };
        }
      } else {
        nativeWireIDs.set(entry.requestId, {
          browserRequestId: entry.browserRequestId, method: entry.method, pathSuffix,
          isMutation: entry.isMutation, readOnlySupportKind,
        });
      }
    }
    const classifiedRequestId = typeof classified.requestId === 'string' ? classified.requestId : null;
    const ledgerRequestId = typeof entry.requestId === 'string' ? entry.requestId : null;
    if (classified.isMutation !== entry.isMutation || classified.method !== entry.method ||
        cdaNativePathSuffix(classified.path) !== pathSuffix || classified.index !== entry.index ||
        classified.startedAt !== entry.startedAt || classifiedRequestId !== ledgerRequestId) {
      return { message: where + '.nativeRequests entry must match its exact action/support binding and chronology' };
    }
    if (!entry.isMutation && !modeledProposalRequest &&
        !journeyCase.readOnlySupportKinds.includes(supportRoutesBySuffix.get(entry.method + ' ' + pathSuffix))) {
      return { message: where + '.nativeRequests read-only request has no declared support kind' };
    }
  }
  if (nativePathBases.size > 1) {
    return { message: where + '.nativeRequests must retain one owned project authoring base path' };
  }
  for (const browserRequestId of classifiedNativeRequests.keys()) {
    if (!nativeBrowserIDs.has(browserRequestId)) {
      return { message: where + ' modeled/support request is absent from the complete native observer ledger' };
    }
  }
  const byObserverIndex = [...observedRequestStarts].sort((left, right) => left.index - right.index);
  for (let index = 1; index < byObserverIndex.length; index += 1) {
    const previous = byObserverIndex[index - 1];
    const current = byObserverIndex[index];
    if (current.index === previous.index) {
      return { message: where + ' native request observer index must be unique across modeled and support requests' };
    }
    if (current.startedAt < previous.startedAt) {
      return { message: where + ' native request start times must follow the observer index chronology' };
    }
  }
  for (const [requestID, settlementIndex] of previewSettlements) {
    const start = previewStarts.get(requestID);
    const settlement = trace.actions[settlementIndex];
    const timingProblem = settlementTimingProblem(
      start.action.request,
      settlement?.settlement,
      settlementIndex,
      'Preview',
    );
    if (timingProblem) return { message: timingProblem, actionIndex: settlementIndex };
  }
  return null;
}

function rawRelatedExpandStep(steps, stepId, where) {
  if (!Array.isArray(steps)) return { problem: where + '.candidateConstruction.steps must preserve the raw proposal steps' };
  const matches = steps.filter((step) => step?.id === stepId && step?.operation?.kind === 'RELATED_EXPAND');
  if (matches.length !== 1) return { problem: where + ' must identify exactly one native RELATED_EXPAND candidate step' };
  return { step: matches[0] };
}

function cdaRowExpansionConsistencyProblem(trace, where, journeyCase) {
  const actions = trace.actions;
  const rowStarts = actions
    .map((action, index) => ({ action, index }))
    .filter(({ action }) => action.label === 'Preview' && isPlainObject(action.rowExpansionLifecycle));
  const applyActions = actions
    .map((action, index) => ({ action, index }))
    .filter(({ action }) => action.label === 'ExpandRows');
  if (applyActions.length !== journeyCase.rowExpansionStages.length) {
    return { message: where + '.actions must contain exactly two modeled ExpandRows applies' };
  }
  let priorAfter = null;
  const latestRequestByStage = new Map();
  const latestReadyByStage = new Map();
  const settlementLabels = new Set([
    'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
  ]);
  const startForRequest = new Map();
  const startByIndex = new Map();
  for (const start of rowStarts) {
    const lifecycle = start.action.rowExpansionLifecycle;
    const requestKey = observedRequestKey(start.action.request);
    if (!requestKey || observedRequestKey(lifecycle.owner) !== requestKey) {
      return { message: where + '.actions[' + start.index + '] row Preview must bind actual request and lifecycle owners' };
    }
    if (startForRequest.has(requestKey)) {
      return { message: where + '.actions[' + start.index + '] repeats a row Preview request identity' };
    }
    startForRequest.set(requestKey, start);
    startByIndex.set(start.index, { requestKey, stage: lifecycle.stage });
  }
  for (let actionIndex = 0; actionIndex < actions.length; actionIndex += 1) {
    const action = actions[actionIndex];
    const start = startByIndex.get(actionIndex);
    if (start) {
      latestRequestByStage.set(start.stage, start.requestKey);
      latestReadyByStage.delete(start.stage);
      continue;
    }
    if (!settlementLabels.has(action.label) || !isPlainObject(action.rowExpansionLifecycle)) continue;
    const lifecycle = action.rowExpansionLifecycle;
    const requestKey = observedRequestKey(action.request);
    const requestStart = startForRequest.get(requestKey);
    if (!requestStart || actionIndex <= requestStart.index) {
      return { message: where + '.actions[' + actionIndex + '] row preview settlement has no preceding request owner' };
    }
    if (lifecycle.stage !== requestStart.action.rowExpansionLifecycle.stage ||
        lifecycle.stepId !== requestStart.action.rowExpansionLifecycle.stepId ||
        lifecycle.inputStepId !== requestStart.action.rowExpansionLifecycle.inputStepId ||
        canonicalJson(lifecycle.route) !== canonicalJson(requestStart.action.rowExpansionLifecycle.route)) {
      return { message: where + '.actions[' + actionIndex + '] row preview settlement changed stage, step, input, or route identity' };
    }
    if (action.label === 'PreviewComplete') {
      if (latestRequestByStage.get(lifecycle.stage) !== requestKey) {
        return { message: where + '.actions[' + actionIndex + '] current row PreviewComplete must settle the latest request for its stage' };
      }
      latestReadyByStage.set(lifecycle.stage, requestKey);
    } else if (['PreviewError', 'PreviewRequestCancelled'].includes(action.label) &&
        latestRequestByStage.get(lifecycle.stage) === requestKey) {
      latestReadyByStage.delete(lifecycle.stage);
    }
  }
  const appliesByStage = applyActions.map(({ action }) => action.rowExpansionLifecycle?.stage);
  if (canonicalJson(appliesByStage) !== canonicalJson(journeyCase.rowExpansionStages)) {
    return { message: where + ' ExpandRows applies must occur in SpecimenPatient then PatientObservation order' };
  }
  for (let stageIndex = 0; stageIndex < journeyCase.rowExpansionStages.length; stageIndex += 1) {
    const expectedStage = journeyCase.rowExpansionStages[stageIndex];
    const applied = applyActions[stageIndex];
    const applyLifecycle = applied.action.rowExpansionLifecycle;
    const requestKey = observedRequestKey(applyLifecycle?.owner);
    const start = startForRequest.get(requestKey);
    const settlementMatches = actions
      .map((action, index) => ({ action, index }))
      .filter(({ action, index }) => action.label === 'PreviewComplete' &&
        isPlainObject(action.rowExpansionLifecycle) && observedRequestKey(action.request) === requestKey && index < applied.index);
    if (!start || settlementMatches.length !== 1 || latestReadyByStage.get(expectedStage) !== requestKey ||
        applyLifecycle?.stage !== expectedStage || start.action.rowExpansionLifecycle.stage !== expectedStage) {
      return { message: where + '.actions[' + applied.index + '] ExpandRows must consume the latest successful row proposal for its stage' };
    }
    const settlement = settlementMatches[0];
    const startLifecycle = start.action.rowExpansionLifecycle;
    const settlementLifecycle = settlement.action.rowExpansionLifecycle;
    if (!(start.index < settlement.index && settlement.index < applied.index) ||
        [settlementLifecycle, applyLifecycle].some((lifecycle) =>
          lifecycle?.stepId !== startLifecycle.stepId || lifecycle?.inputStepId !== startLifecycle.inputStepId ||
          canonicalJson(lifecycle?.route) !== canonicalJson(startLifecycle.route))) {
      return { message: where + ' row expansion request, response, and Apply lifecycle identities must remain in order' };
    }
    if (start.action.owner?.attempt !== settlement.action.owner?.attempt ||
        start.action.owner?.attempt !== applied.action.owner?.attempt) {
      return { message: where + ' row expansion request, response, and Apply must retain the same P1/P2 owner' };
    }
    const startOwnerProblem = actualRequestIdentityProblem(startLifecycle.owner, startLifecycle.request,
      undefined, where + '.actions[' + start.index + '].rowExpansionLifecycle');
    if (startOwnerProblem) return { message: startOwnerProblem, actionIndex: start.index };
    for (const record of [start, settlement]) {
      if (observedRequestKey(record.action.request) !== requestKey ||
          canonicalJson(record.action.request?.request) !== canonicalJson(startLifecycle.request.request)) {
        return { message: where + '.actions[' + record.index + '] must retain the actual row proposal request identity/body' };
      }
    }
    if (observedRequestKey(startLifecycle.owner) !== requestKey ||
        observedRequestKey(settlementLifecycle.owner) !== requestKey ||
        observedRequestKey(applyLifecycle.owner) !== requestKey) {
      return { message: where + ' row expansion lifecycle owner must bind to its actual Preview request', actionIndex: start.index };
    }
    const previewResponse = settlementLifecycle.response;
    const applyProposalResponse = applyLifecycle.response;
    if (typeof previewResponse?.proposalId !== 'string' ||
        previewResponse.proposalId !== settlement.action.response?.proposalId ||
        canonicalJson(previewResponse.candidateConstruction) !== canonicalJson(settlement.action.response?.candidateConstruction) ||
        previewResponse.proposalId !== applyProposalResponse?.proposalId ||
        canonicalJson(previewResponse.candidateConstruction) !== canonicalJson(applyProposalResponse.candidateConstruction)) {
      return { message: where + ' row expansion preview and Apply must retain the same actual proposal response' };
    }
    const rendered = settlementLifecycle.renderedObservation?.proposalPanel ?? settlementLifecycle.renderedObservation;
    const observed = settlement.action.observation;
    const snapshotlessDOM = rendered == null && cdaEventTimeSnapshotUnavailable(settlement.action, settlementLifecycle);
    if (!snapshotlessDOM && (!isPlainObject(rendered) || rendered.status !== 'ready' ||
        rendered.id !== previewResponse.proposalId ||
        observed?.proposalId !== previewResponse.proposalId ||
        observed?.previewReceiptId !== previewResponse.preview?.receiptId ||
        observed?.previewOutputId !== previewResponse.preview?.outputId ||
        !Array.isArray(rendered.rows) || rendered.rows.length !== previewResponse.preview?.rowCount ||
        rendered.rows.some((row) => !Array.isArray(row) || row.length === 0 || row.some((cell) =>
          !isPlainObject(cell) || typeof cell.text !== 'string' ||
          (cell.title !== null && typeof cell.title !== 'string'))))) {
      return { message: where + ' PreviewComplete must retain the actual ready row expansion proposal DOM' };
    }
    const body = startLifecycle.request.request;
    const candidateRequest = rawRelatedExpandStep(body.candidateConstruction?.steps, startLifecycle.stepId,
      where + '.actions[' + start.index + '].rowExpansionLifecycle.request');
    if (candidateRequest.problem) return { message: candidateRequest.problem };
    const candidateResponse = rawRelatedExpandStep(previewResponse.candidateConstruction?.steps, startLifecycle.stepId,
      where + '.actions[' + settlement.index + '].rowExpansionLifecycle.response');
    if (candidateResponse.problem) return { message: candidateResponse.problem };
    const candidateStep = candidateRequest.step;
    const responseStep = candidateResponse.step;
    const candidateResponseProblem = cdaRowExpansionCandidateResponseProblem(
      body.candidateConstruction, previewResponse.candidateConstruction, body.changedStepId,
      startLifecycle.savedBefore, startLifecycle.inputStepId, startLifecycle.route,
      where + '.actions[' + settlement.index + '].rowExpansionLifecycle',
    );
    if (candidateResponseProblem || body.changedStepId !== startLifecycle.stepId) {
      if (candidateResponseProblem) return { message: candidateResponseProblem };
      return { message: where + ' row expansion raw proposal body and response must retain the same changed step' };
    }
    if (previewResponse.changedStepId !== body.changedStepId) {
      return { message: where + ' row expansion response must echo the exact request changedStepId' };
    }
    const rawRoute = candidateStep.operation.relatedExpand.route;
    if (canonicalJson(rawRoute) !== canonicalJson(startLifecycle.route)) {
      return { message: where + '.rowExpansionLifecycle.route must match the native proposal RELATED_EXPAND route' };
    }
    const rawStepInput = candidateStep.inputs?.find((input) => input.kind === 'STEP_OUTPUT')?.stepId ?? null;
    if (rawStepInput !== startLifecycle.inputStepId) {
      return { message: where + '.rowExpansionLifecycle.inputStepId must match the native proposal step input' };
    }
    if (stageIndex === 0 && startLifecycle.inputStepId !== null) {
      return { message: where + ' SpecimenPatient must start from source_projection without a prior construction step' };
    }
    if (stageIndex > 0 && startLifecycle.inputStepId !== applyActions[stageIndex - 1].action.rowExpansionLifecycle.stepId) {
      return { message: where + ' PatientObservation must consume the preceding RELATED_EXPAND step output' };
    }
    const before = startLifecycle.savedBefore;
    const after = applyLifecycle.savedAfter;
    if (priorAfter && !sameSavedDraft(priorAfter, before)) {
      return { message: where + ' row expansion stages must apply to the actual preceding saved Builder workspace' };
    }
    const beforeDocument = savedBuilderDocument(before, where + '.rowExpansionLifecycle.savedBefore');
    const afterDocument = savedBuilderDocument(after, where + '.rowExpansionLifecycle.savedAfter');
    if (beforeDocument.problem) return { message: beforeDocument.problem };
    if (afterDocument.problem) return { message: afterDocument.problem };
    const beforeStepCount = (beforeDocument.document.construction?.steps ?? []).filter((step) => step.id === startLifecycle.stepId).length;
    const afterSteps = afterDocument.document.construction?.steps ?? [];
    const savedStepMatches = afterSteps.filter((step) => step.id === startLifecycle.stepId && step.operation?.kind === 'RELATED_EXPAND');
    if (beforeStepCount !== 0 || savedStepMatches.length !== 1 ||
        canonicalJson(savedStepMatches[0]) !== canonicalJson(responseStep)) {
      return { message: where + ' applied row expansion must persist the exact native response candidate step in the selected Builder workspace' };
    }
    const applyCommands = applied.action.request?.request?.commands;
    if (!Array.isArray(applyCommands) || !applyCommands.some((command) =>
      command?.type === 'APPLY_CONSTRUCTION_PROPOSAL' && command.proposalId === previewResponse.proposalId)) {
      return { message: where + '.actions[' + applied.index + '] must apply the current row proposal ID through /commands' };
    }
    if (applied.action.response?.status < 200 || applied.action.response?.status >= 300 ||
        canonicalJson(applied.action.response.workspace) !== canonicalJson(after.workspace) ||
        applied.action.response.draftVersion !== after.draftVersion ||
        applied.action.response.draftDigest !== after.draftDigest) {
      return { message: where + '.actions[' + applied.index + '] Apply response must match the saved Builder workspace and draft CAS' };
    }
    priorAfter = after;
  }
  return null;
}

function cdaRowExpansionCandidateStepProblem(requestedStep, responseStep, savedBefore, expectedInputStepId, expectedRoute, where) {
  const mismatch = where + ' row expansion response may refresh only omitted nullable metadata from its saved input stage';
  if (!isPlainObject(requestedStep) || !isPlainObject(responseStep) ||
      requestedStep.operation?.kind !== 'RELATED_EXPAND' || responseStep.operation?.kind !== 'RELATED_EXPAND' ||
      canonicalJson(requestedStep.operation.relatedExpand?.route) !== canonicalJson(expectedRoute) ||
      !Array.isArray(requestedStep.outputs) || !Array.isArray(responseStep.outputs) ||
      requestedStep.outputs.length !== responseStep.outputs.length) {
    return mismatch;
  }
  if (expectedInputStepId === null) {
    return Array.isArray(requestedStep.inputs) && requestedStep.inputs.length === 1 &&
      requestedStep.inputs[0]?.kind === 'SOURCE_PROJECTION' &&
      canonicalJson(requestedStep) === canonicalJson(responseStep)
      ? null
      : mismatch;
  }
  if (typeof expectedInputStepId !== 'string' || !Array.isArray(requestedStep.inputs) ||
      requestedStep.inputs.length !== 1 || requestedStep.inputs[0]?.kind !== 'STEP_OUTPUT' ||
      requestedStep.inputs[0]?.stepId !== expectedInputStepId) return mismatch;
  const selected = savedBuilderDocument(savedBefore, where + '.savedBefore');
  if (selected.problem) return selected.problem;
  const inputStepId = requestedStep.inputs[0].stepId;
  const inputSteps = (selected.document.construction?.steps ?? []).filter((step) => step?.id === inputStepId);
  const savedSteps = selected.document.construction?.steps ?? [];
  if (inputSteps.length !== 1 || savedSteps.at(-1)?.id !== inputStepId || !Array.isArray(inputSteps[0].outputs)) return mismatch;
  const inputOutputs = inputSteps[0].outputs;
  const relatedRecordColumnId = requestedStep.operation.relatedExpand?.relatedRecordColumnId;
  if (typeof relatedRecordColumnId !== 'string' || relatedRecordColumnId.length === 0 ||
      inputOutputs.some((output) => output?.id === relatedRecordColumnId) ||
      requestedStep.outputs.length !== inputOutputs.length + 1) {
    return mismatch;
  }
  const requestedRest = structuredClone(requestedStep);
  const responseRest = structuredClone(responseStep);
  delete requestedRest.outputs;
  delete responseRest.outputs;
  if (canonicalJson(requestedRest) !== canonicalJson(responseRest)) return mismatch;
  const requestedById = new Map();
  const responseById = new Map();
  for (const [outputs, byId] of [[requestedStep.outputs, requestedById], [responseStep.outputs, responseById]]) {
    for (const output of outputs) {
      if (!isPlainObject(output) || typeof output.id !== 'string' || byId.has(output.id)) return mismatch;
      byId.set(output.id, output);
    }
  }
  const orderedIds = [...inputOutputs.map((output) => output?.id), relatedRecordColumnId];
  if (orderedIds.some((id) => typeof id !== 'string') ||
      canonicalJson(requestedStep.outputs.map((output) => output.id)) !== canonicalJson(orderedIds) ||
      canonicalJson(responseStep.outputs.map((output) => output.id)) !== canonicalJson(orderedIds)) return mismatch;
  for (const savedOutput of inputOutputs) {
    if (!isPlainObject(savedOutput) || typeof savedOutput.id !== 'string') return mismatch;
    const requestedOutput = requestedById.get(savedOutput.id);
    const responseOutput = responseById.get(savedOutput.id);
    if (canonicalJson(responseOutput) !== canonicalJson(savedOutput)) return mismatch;
    if (canonicalJson(requestedOutput) !== canonicalJson(savedOutput)) {
      const inputStep = inputSteps[0];
      if (savedOutput.nullable !== true || Object.hasOwn(requestedOutput, 'nullable') ||
          inputStep.operation?.kind !== 'RELATED_EXPAND' ||
          inputStep.operation.relatedExpand?.emptyPolicy !== 'PRESERVE_PARENT' ||
          inputStep.operation.relatedExpand?.relatedRecordColumnId !== savedOutput.id) return mismatch;
      const requestWithSavedNullable = structuredClone(requestedOutput);
      requestWithSavedNullable.nullable = true;
      if (canonicalJson(requestWithSavedNullable) !== canonicalJson(savedOutput)) return mismatch;
    }
  }
  const newRequestOutput = requestedById.get(relatedRecordColumnId);
  const newResponseOutput = responseById.get(relatedRecordColumnId);
  if (!newRequestOutput || !newResponseOutput ||
      canonicalJson(newRequestOutput) !== canonicalJson(newResponseOutput) ||
      newResponseOutput.nullable !== (requestedStep.operation.relatedExpand.emptyPolicy === 'PRESERVE_PARENT')) {
    return mismatch;
  }
  return null;
}

function cdaRowExpansionCandidateResponseProblem(requestedConstruction, responseConstruction, changedStepId,
  savedBefore, expectedInputStepId, expectedRoute, where) {
  const mismatch = where + ' must retain one unique appended RELATED_EXPAND delta and its exact saved input schema';
  if (!isPlainObject(requestedConstruction) || !isPlainObject(responseConstruction) ||
      !Array.isArray(requestedConstruction.steps) || !Array.isArray(responseConstruction.steps) ||
      typeof changedStepId !== 'string' || changedStepId.length === 0 ||
      requestedConstruction.steps.length !== responseConstruction.steps.length) {
    return mismatch;
  }
  const selected = savedBuilderDocument(savedBefore, where + '.savedBefore');
  if (selected.problem) return selected.problem;
  const savedSteps = selected.document.construction?.steps ?? [];
  if (requestedConstruction.steps.length !== savedSteps.length + 1 ||
      savedSteps.some((step) => step?.id === changedStepId) ||
      requestedConstruction.steps[savedSteps.length]?.id !== changedStepId ||
      responseConstruction.steps[savedSteps.length]?.id !== changedStepId) {
    return mismatch;
  }
  for (let index = 0; index < savedSteps.length; index += 1) {
    if (canonicalJson(requestedConstruction.steps[index]) !== canonicalJson(savedSteps[index]) ||
        canonicalJson(responseConstruction.steps[index]) !== canonicalJson(savedSteps[index])) {
      return mismatch;
    }
  }
  const requestDeltas = requestedConstruction.steps.filter((step) => step?.id === changedStepId &&
    step?.operation?.kind === 'RELATED_EXPAND');
  const responseDeltas = responseConstruction.steps.filter((step) => step?.id === changedStepId &&
    step?.operation?.kind === 'RELATED_EXPAND');
  if (requestDeltas.length !== 1 || responseDeltas.length !== 1) return mismatch;
  return cdaRowExpansionCandidateStepProblem(
    requestDeltas[0], responseDeltas[0], savedBefore, expectedInputStepId, expectedRoute, where,
  );
}

function cdaJourneyLifecycleConsistencyProblem(trace, where, journeyCase, contract) {
  const mutatingLabels = new Set(['ExpandRows', 'Group', 'EditGroup', 'EditGroupAgain', 'RelatedSourceApply']);
  const epochs = new Set();
  const epochOrigins = new Map();
  const originEpochs = new Map();
  const relatedPreviewRequests = new Set();
  const previewSettlements = new Map();
  const groupMutations = [];
  const groupCancels = [];
  const reloads = [];
  const groupProposalPreviews = new Map();
  let currentSaved = null;
  let draftTransitions = 0;
  let activeRelatedCandidate = null;
  const cancellationKinds = [];
  const applyKinds = [];

  const snapshotsFor = (action) => {
    if (isPlainObject(action.rowExpansionLifecycle)) return action.rowExpansionLifecycle;
    if (isPlainObject(action.relatedSourceLifecycle)) return action.relatedSourceLifecycle;
    if (isPlainObject(action.groupLifecycle)) return action.groupLifecycle;
    return null;
  };
  const originProblem = (epoch, origin, actionWhere) => {
    epochs.add(epoch);
    const priorOrigin = epochOrigins.get(epoch);
    if (priorOrigin !== undefined && priorOrigin !== origin) {
      return actionWhere + ' documentTimeOrigin changed within one mount epoch';
    }
    const priorEpoch = originEpochs.get(origin);
    if (priorEpoch !== undefined && priorEpoch !== epoch) {
      return actionWhere + ' reused one documentTimeOrigin across distinct mount epochs';
    }
    epochOrigins.set(epoch, origin);
    originEpochs.set(origin, epoch);
    return null;
  };
  const lifecycleEvidenceMatches = (source, target, actionWhere) => {
    if (observedRequestKey(source.owner) !== observedRequestKey(target.owner) ||
        canonicalJson(source.candidateIntent) !== canonicalJson(target.candidateIntent) ||
        canonicalJson(source.request) !== canonicalJson(target.request) ||
        canonicalJson(source.response) !== canonicalJson(target.response) ||
        canonicalJson(source.settlement) !== canonicalJson(target.settlement)) {
      return actionWhere + ' must retain the current related-source proposal owner, intent, request, response, and settlement';
    }
    return null;
  };

  for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
    const action = trace.actions[actionIndex];
    const actionWhere = where + '.actions[' + actionIndex + ']';
    const previewSettlement = new Set([
      'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored',
      'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
    ]).has(action.label);
    const lifecycle = snapshotsFor(action);
    if (!lifecycle || !isPlainObject(lifecycle.savedBefore) || !isPlainObject(lifecycle.savedAfter)) {
      const unavailableProblem = cdaSnapshotPairUnavailableProblem(action, lifecycle, actionWhere);
      if (unavailableProblem) return { message: unavailableProblem, actionIndex };
      const lifecycleOrigin = lifecycle.documentTimeOrigin ?? lifecycle.owner?.documentTimeOrigin;
      if (Number.isInteger(lifecycle.mountEpoch) && lifecycleOrigin !== undefined) {
        const originProblemText = originProblem(lifecycle.mountEpoch, lifecycleOrigin, actionWhere);
        if (originProblemText) return { message: originProblemText, actionIndex };
        epochs.add(lifecycle.mountEpoch);
      }
      if (Number.isInteger(action.observedEpoch)) epochs.add(action.observedEpoch);
      if (lifecycle.owner?.mountEpoch !== undefined) epochs.add(lifecycle.owner.mountEpoch);
      if (lifecycle.candidateOwner?.mountEpoch !== undefined) epochs.add(lifecycle.candidateOwner.mountEpoch);
      if (action.label === 'PreviewComplete' && isPlainObject(action.groupLifecycle)) {
        const requestKey = observedRequestKey(action.groupLifecycle.owner);
        if (requestKey) {
          const preview = groupProposalPreviews.get(requestKey) ?? {};
          preview.settlement = action;
          groupProposalPreviews.set(requestKey, preview);
        }
      }
      continue;
    }
    const before = lifecycle.savedBefore;
    const after = lifecycle.savedAfter;
    const beforeCAS = cdaCASProblem(before, actionWhere + '.savedBefore');
    if (beforeCAS) return { message: beforeCAS, actionIndex };
    const afterCAS = cdaCASProblem(after, actionWhere + '.savedAfter');
    if (afterCAS) return { message: afterCAS, actionIndex };
    if (before.explorerId !== after.explorerId) {
      return { message: actionWhere + ' cannot change the owning Explorer across one observed action', actionIndex };
    }
    const lifecycleOrigin = lifecycle.documentTimeOrigin ?? lifecycle.owner?.documentTimeOrigin;
    if (lifecycleOrigin !== undefined) {
      if (!Number.isInteger(lifecycle.mountEpoch) || lifecycle.owner?.mountEpoch !== undefined &&
          lifecycle.owner.mountEpoch !== lifecycle.mountEpoch ||
          lifecycle.documentTimeOrigin !== undefined && lifecycle.owner?.documentTimeOrigin !== undefined &&
          lifecycle.documentTimeOrigin !== lifecycle.owner.documentTimeOrigin) {
        return { message: actionWhere + ' request owner and lifecycle must preserve the same actual request-start epoch/timeOrigin', actionIndex };
      }
      const problem = originProblem(lifecycle.mountEpoch, lifecycleOrigin, actionWhere);
      if (problem) return { message: problem, actionIndex };
    }
    if (Number.isInteger(lifecycle.mountEpoch)) epochs.add(lifecycle.mountEpoch);
    if (Number.isInteger(action.observedEpoch)) epochs.add(action.observedEpoch);
    if (action.observedEpoch !== undefined &&
        (!Number.isInteger(action.observedEpoch) || action.observedEpoch < 0 || action.observedEpoch > journeyCase.maxMountEpoch)) {
      return { message: actionWhere + '.observedEpoch must be an actual finite mount epoch in 0..' + journeyCase.maxMountEpoch, actionIndex };
    }
    if (lifecycle.owner?.mountEpoch !== undefined) epochs.add(lifecycle.owner.mountEpoch);
    if (lifecycle.candidateOwner?.mountEpoch !== undefined) epochs.add(lifecycle.candidateOwner.mountEpoch);
    if (Number.isInteger(lifecycle.mountEpoch) && lifecycle.owner?.mountEpoch !== undefined &&
        lifecycle.owner.mountEpoch !== lifecycle.mountEpoch &&
        ['RelatedSourcePreview', 'RelatedSourceApply', 'RelatedSourceCancel'].includes(action.label)) {
      return { message: actionWhere + ' related-source proposal owner epoch must match its captured mount epoch', actionIndex };
    }
    if (lifecycle.owner?.mountEpoch !== undefined && lifecycle.mountEpoch !== undefined &&
        ['RelatedSourcePreview', 'RelatedSourceApply', 'RelatedSourceCancel'].includes(action.label) &&
        lifecycle.owner.mountEpoch !== lifecycle.mountEpoch) {
      return { message: actionWhere + ' related-source proposal owner must belong to the current mount epoch', actionIndex };
    }
    if (lifecycle.owner?.mountEpoch !== undefined && lifecycle.mountEpoch !== undefined &&
        action.label === 'Preview' && lifecycle.owner.mountEpoch !== lifecycle.mountEpoch) {
      return { message: actionWhere + ' Preview request owner must belong to the current mount epoch', actionIndex };
    }
    if (currentSaved === null) currentSaved = before;
    else if (!previewSettlement && !sameSavedDraft(currentSaved, before)) {
      return { message: actionWhere + ' savedBefore does not match the preceding observed Builder state', actionIndex };
    }
    if (mutatingLabels.has(action.label)) {
      if (after.draftVersion !== before.draftVersion + 1 || after.draftDigest === before.draftDigest ||
          canonicalJson(after.workspace) === canonicalJson(before.workspace)) {
        return { message: actionWhere + ' Apply must advance exactly one real Builder draft CAS and change saved workspace', actionIndex };
      }
      draftTransitions += 1;
      currentSaved = after;
    } else {
      if (!sameSavedDraft(before, after)) {
        return { message: actionWhere + ' state-preserving event must retain exact saved workspace and draft CAS', actionIndex };
      }
      if (!previewSettlement) currentSaved = before;
    }

    if (['Group', 'EditGroup', 'EditGroupAgain'].includes(action.label)) {
      groupMutations.push({ label: action.label, before, after });
    }
    if (action.label === 'GroupEditCancel') groupCancels.push(action);
    if (action.label === 'Reload') reloads.push(action);

    if (['Preview', 'PreviewComplete'].includes(action.label) && isPlainObject(action.groupLifecycle)) {
      const requestKey = observedRequestKey(action.groupLifecycle.owner);
      if (requestKey) {
        const preview = groupProposalPreviews.get(requestKey) ?? {};
        if (action.label === 'Preview') preview.start = action;
        else preview.settlement = action;
        groupProposalPreviews.set(requestKey, preview);
      }
    }

    if (['Group', 'EditGroup', 'EditGroupAgain'].includes(action.label)) {
      const groupLifecycle = action.groupLifecycle;
      const candidateKey = observedRequestKey(groupLifecycle.candidateOwner);
      const preview = groupProposalPreviews.get(candidateKey);
      if (!preview?.start || !preview?.settlement ||
          canonicalJson(preview.settlement.groupLifecycle.candidateIntent) !== canonicalJson(groupLifecycle.candidateIntent) ||
          preview.start.owner?.attempt !== action.owner?.attempt) {
        return { message: actionWhere + ' Group Apply must consume its current observed Preview candidate owner', actionIndex };
      }
      const applyCommand = action.request?.request?.commands?.find((command) =>
        command?.type === 'APPLY_CONSTRUCTION_PROPOSAL');
      if (!applyCommand || applyCommand.proposalId !== preview.settlement.response?.proposalId) {
        return { message: actionWhere + ' Group Apply request must consume the proposalId from its current Preview response', actionIndex };
      }
      const response = action.response;
      if (!isPlainObject(response) || response.status < 200 || response.status >= 300 ||
          canonicalJson(response.workspace) !== canonicalJson(after.workspace) ||
          response.draftVersion !== after.draftVersion || response.draftDigest !== after.draftDigest) {
        return { message: actionWhere + ' Group Apply response must match the actual saved Builder workspace and draft CAS', actionIndex };
      }
    }

    if (action.label === 'RelatedSourcePreview') {
      const related = action.relatedSourceLifecycle;
      const requestKey = observedRequestKey(related.owner);
      if (!requestKey || relatedPreviewRequests.has(requestKey)) {
        return { message: actionWhere + '.relatedSourceLifecycle.owner must identify one unique actual proposal request', actionIndex };
      }
      relatedPreviewRequests.add(requestKey);
      previewSettlements.set(requestKey, action);
      activeRelatedCandidate = { requestKey, action };
    } else if (['RelatedSourceCancel', 'RelatedSourceApply'].includes(action.label)) {
      const related = action.relatedSourceLifecycle;
      const requestKey = observedRequestKey(related.owner);
      if (!activeRelatedCandidate) {
        return { message: actionWhere + ' has no current related-source proposal candidate', actionIndex };
      }
      if (requestKey !== activeRelatedCandidate.requestKey ||
          canonicalJson(related.candidateIntent) !== canonicalJson(activeRelatedCandidate.action.relatedSourceLifecycle.candidateIntent) ||
          action.owner?.attempt !== activeRelatedCandidate.action.owner?.attempt) {
        return { message: actionWhere + ' must consume the current related-source candidate owner and intent', actionIndex };
      }
      const evidenceProblem = lifecycleEvidenceMatches(activeRelatedCandidate.action.relatedSourceLifecycle, related, actionWhere);
      if (evidenceProblem) return { message: evidenceProblem, actionIndex };
      const selectedPreviewLifecycle = activeRelatedCandidate.action.relatedSourceLifecycle;
      if (!isPlainObject(selectedPreviewLifecycle.renderedObservation) ||
          selectedPreviewLifecycle.renderedObservation.proposalPanel?.status !== 'ready' ||
          selectedPreviewLifecycle.renderedObservation.proposalPanel?.id !== selectedPreviewLifecycle.response.proposalId ||
          selectedPreviewLifecycle.renderedObservation.proposalPanelBrowserRequestId !==
            selectedPreviewLifecycle.owner.browserRequestId ||
          !Number.isFinite(selectedPreviewLifecycle.renderedObservation.proposalPanelObservedAt) ||
          selectedPreviewLifecycle.renderedObservation.proposalPanelObservedAt < selectedPreviewLifecycle.settlement.completedAt ||
          selectedPreviewLifecycle.renderedObservationMount?.mountEpoch !== selectedPreviewLifecycle.settlementObservedEpoch ||
          selectedPreviewLifecycle.renderedObservationMount?.documentTimeOrigin !==
            selectedPreviewLifecycle.owner.documentTimeOrigin) {
        return {
          message: actionWhere + ' must consume a candidate with its own later READY panel observation from the exact proposal owner and mount',
          actionIndex,
        };
      }
      const intent = related.candidateIntent;
      if (action.label === 'RelatedSourceCancel') {
        cancellationKinds.push(intent.kind);
      } else {
        applyKinds.push(intent.kind);
        const body = action.request?.request;
        const command = Array.isArray(body?.commands)
          ? body.commands.find((candidate) => candidate?.type === 'APPLY_CONSTRUCTION_PROPOSAL')
          : null;
        if (!command || command.proposalId !== related.response.proposalId) {
          return { message: actionWhere + ' Apply request must consume the proposalId from the current candidate', actionIndex };
        }
        if ((intent.kind === 'add' &&
              (before.relatedSource !== null || canonicalJson(after.relatedSource) !== canonicalJson(intent.proposed))) ||
            (intent.kind === 'remove' &&
              (canonicalJson(before.relatedSource) !== canonicalJson(intent.base) || after.relatedSource !== null))) {
          return { message: actionWhere + ' Apply saved related-source state does not match the actual candidate intent', actionIndex };
        }
        if (canonicalJson(before.group) !== canonicalJson(after.group)) {
          return { message: actionWhere + ' related-source Apply must retain the exact saved Group configuration', actionIndex };
        }
        const response = action.response;
        if (!isPlainObject(response) || response.status < 200 || response.status >= 300 ||
            canonicalJson(response.workspace) !== canonicalJson(after.workspace) ||
            response.draftVersion !== after.draftVersion || response.draftDigest !== after.draftDigest) {
          return { message: actionWhere + ' Apply response must match the actual saved Builder workspace and draft CAS', actionIndex };
        }
        const afterDocument = savedBuilderDocument(after, actionWhere + '.relatedSourceLifecycle.savedAfter');
        if (afterDocument.problem) return { message: afterDocument.problem, actionIndex };
        const candidateSteps = related.response.candidateConstruction?.steps;
        const savedSteps = afterDocument.document.construction?.steps ?? [];
        if (!Array.isArray(candidateSteps) || canonicalJson(candidateSteps) !== canonicalJson(savedSteps)) {
          return { message: actionWhere + ' Apply must persist the exact current proposal construction', actionIndex };
        }
      }
      activeRelatedCandidate = null;
    }
  }

  if (draftTransitions !== journeyCase.casTransitionCount) {
    return { message: where + ' observed ' + draftTransitions + ' draft transitions, expected ' + journeyCase.casTransitionCount };
  }
  if (canonicalJson(groupMutations.map(({ label }) => label)) !== canonicalJson(['Group', 'EditGroup', 'EditGroupAgain']) ||
      canonicalJson(groupMutations.map(({ after }) => after.group?.aggregate)) !==
        canonicalJson(['COUNT_ROWS', 'COUNT_DISTINCT', 'COUNT_ROWS'])) {
    return { message: where + ' Group lifecycle must create, edit to COUNT_DISTINCT, then round-trip to COUNT_ROWS' };
  }
  if (groupCancels.length !== 2 || groupCancels.some((action) =>
      !['COUNT_DISTINCT', 'COUNT_ROWS'].includes(action.groupLifecycle?.candidateIntent?.proposed?.aggregate))) {
    return { message: where + ' must retain the first COUNT_DISTINCT Cancel and the Group round-trip Cancel' };
  }
  if (canonicalJson(cancellationKinds) !== canonicalJson(['add', 'remove']) ||
      canonicalJson(applyKinds) !== canonicalJson(['add', 'remove'])) {
    return { message: where + ' must observe add/cancel/reopen/apply and removal/cancel/reopen/apply related-source lifecycles' };
  }
  const reloadStates = reloads.map((action) => ({
    aggregate: action.groupLifecycle?.savedAfter?.group?.aggregate ?? null,
    related: action.groupLifecycle?.savedAfter?.relatedSource === null ? 'None' : 'ObservationStatus',
  }));
  const expectedReloadStates = [
    { aggregate: 'COUNT_ROWS', related: 'None' },
    { aggregate: 'COUNT_DISTINCT', related: 'None' },
    { aggregate: 'COUNT_DISTINCT', related: 'ObservationStatus' },
    { aggregate: 'COUNT_ROWS', related: 'ObservationStatus' },
    { aggregate: 'COUNT_ROWS', related: 'None' },
  ];
  if (canonicalJson(reloadStates) !== canonicalJson(expectedReloadStates)) {
    return { message: where + ' reload snapshots must preserve the Group round-trip and exact related-source state' };
  }
  if (epochs.size !== journeyCase.maxMountEpoch + 1 ||
      [...epochs].sort((left, right) => left - right).some((epoch, index) => epoch !== index) ||
      epochOrigins.size !== journeyCase.maxMountEpoch + 1 || originEpochs.size !== journeyCase.maxMountEpoch + 1) {
    return { message: where + ' must derive six distinct documentTimeOrigin values for mount epochs 0..5' };
  }
  const requestCoverageProblem = cdaMutatingRequestCoverageProblem(trace, where, journeyCase, contract);
  if (requestCoverageProblem) return requestCoverageProblem;
  const rowExpansionProblem = cdaRowExpansionConsistencyProblem(trace, where, journeyCase);
  if (rowExpansionProblem) return rowExpansionProblem;
  return null;
}

function candidateIntentRawBodyProblem(intent, where) {
  if (!isPlainObject(intent) || !isPlainObject(intent.rawProposalBody)) return null;
  const body = intent.rawProposalBody;
  if (intent.kind === 'remove') {
    if (!Array.isArray(body.removeStepIds) || body.removeStepIds.length !== 1 ||
        body.removeStepIds[0] !== intent.targetStepId) {
      return where + '.rawProposalBody.removeStepIds must match the actual removal targetStepId';
    }
    return null;
  }
  const steps = body.candidateConstruction?.steps;
  if (!Array.isArray(steps) || body.changedStepId !== intent.proposed.stepId) {
    return where + '.rawProposalBody changedStepId/candidateConstruction must retain the proposed Group step';
  }
  const step = steps.find((candidate) => candidate?.id === intent.proposed.stepId);
  const group = step?.operation?.kind === 'GROUP' ? step.operation.group : null;
  if (!isPlainObject(group) || !Array.isArray(group.keys) || !Array.isArray(group.aggregates)) {
    return where + '.rawProposalBody candidateConstruction must contain the proposed GROUP operation';
  }
  const rawKeys = group.keys.map((key) => typeof key === 'string' ? key : key?.inputColumnId);
  if (rawKeys.some((key) => typeof key !== 'string' || key.length === 0) ||
      canonicalJson(rawKeys) !== canonicalJson(intent.proposed.keys)) {
    return where + '.rawProposalBody Group keys do not match the retained candidateIntent.proposed.keys';
  }
  const rawAggregates = group.aggregates.map((aggregate) => aggregate?.operation);
  if (rawAggregates.length !== 1 || rawAggregates[0] !== intent.proposed.aggregate) {
    return where + '.rawProposalBody Group aggregate does not match the retained candidateIntent.proposed.aggregate';
  }
  return null;
}

function journeyGroupLifecycleConsistencyProblem(trace, where, enabled, {
  maxMountEpoch = 2,
  allowBrowserRequestId = false,
  allowedAggregates = ['COUNT_ROWS'],
  cdaJourneyCase = false,
} = {}) {
  const stepIds = new Map();
  const rawGroupKeys = new Map();
  const captureIds = new Map();
  const captureEpochs = new Map();
  const documentOrigins = new Map();
  const previewIntents = new Map();
  const addStepId = (stepId, actionWhere) => {
    if (stepId === null || stepId === undefined) return null;
    if (typeof stepId !== 'string' || stepId.trim().length === 0) return actionWhere + ' has a non-string raw Group step ID';
    if (!stepIds.has(stepId)) stepIds.set(stepId, stepIds.size);
    if (stepIds.size > BUILDER_GROUP_STEP_TOKENS.length) {
      return actionWhere + ' introduces more raw Group step IDs than the bounded model can represent';
    }
    return null;
  };
  const addGroup = (group, actionWhere) => {
    if (group === null || group === undefined) return null;
    const stepProblem = addStepId(group.stepId, actionWhere);
    if (stepProblem) return stepProblem;
    for (const key of group.keys) {
      if (typeof key !== 'string' || key.length === 0) return actionWhere + ' has an invalid raw Group key ID';
      if (!rawGroupKeys.has(key)) rawGroupKeys.set(key, rawGroupKeys.size);
      if (rawGroupKeys.size > BUILDER_GROUP_KEY_TOKENS.length) {
        return actionWhere + ' introduces more raw Group key IDs than the bounded model can represent';
      }
    }
    return null;
  };
  const addIntent = (intent, actionWhere) => {
    if (!isPlainObject(intent)) return null;
    let problem = addStepId(intent.targetStepId, actionWhere);
    if (problem) return problem;
    for (const field of ['base', 'proposed']) {
      problem = addGroup(intent[field], actionWhere);
      if (problem) return problem;
    }
    return candidateIntentRawBodyProblem(intent, actionWhere + '.groupLifecycle.candidateIntent');
  };

  for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
    const action = trace.actions[actionIndex];
    const actionWhere = where + '.actions[' + actionIndex + ']';
    const lifecycle = action.groupLifecycle;
    if (!lifecycle) continue;
    if (!enabled && !BUILDER_GROUP_LIFECYCLE_LABELS.has(action.label)) continue;
    if (!Number.isInteger(lifecycle.mountEpoch) || lifecycle.mountEpoch < 0 || lifecycle.mountEpoch > maxMountEpoch) {
      return { message: actionWhere + '.groupLifecycle.mountEpoch is outside the selected bounded epoch domain', actionIndex };
    }
    if (lifecycle.documentTimeOrigin !== undefined) {
      const priorOrigin = documentOrigins.get(lifecycle.mountEpoch);
      if (priorOrigin !== undefined && priorOrigin !== lifecycle.documentTimeOrigin) {
        return { message: actionWhere + '.groupLifecycle.documentTimeOrigin changed within one mount epoch', actionIndex };
      }
      documentOrigins.set(lifecycle.mountEpoch, lifecycle.documentTimeOrigin);
    }
    if (lifecycle.owner) {
      const rawCaptureId = observedRequestKey(lifecycle.owner);
      if (!rawCaptureId || (!allowBrowserRequestId && !rawCaptureId.startsWith('capture:'))) {
        return { message: actionWhere + '.groupLifecycle.owner must preserve a bounded native capture identity', actionIndex };
      }
      if (!Number.isInteger(lifecycle.owner.mountEpoch) || lifecycle.owner.mountEpoch < 0 || lifecycle.owner.mountEpoch > maxMountEpoch) {
        return { message: actionWhere + '.groupLifecycle.owner.mountEpoch is outside the selected bounded epoch domain', actionIndex };
      }
      if (!captureIds.has(rawCaptureId)) captureIds.set(rawCaptureId, captureIds.size);
      if (!allowBrowserRequestId && captureIds.size > BUILDER_GROUP_CAPTURE_TOKENS.length) {
        return { message: actionWhere + '.groupLifecycle.owner.captureId exceeds the finite capture token bound', actionIndex };
      }
      const priorEpoch = captureEpochs.get(rawCaptureId);
      if (priorEpoch !== undefined && priorEpoch !== lifecycle.owner.mountEpoch) {
        return { message: actionWhere + '.groupLifecycle.owner reused a raw captureId with a different mountEpoch', actionIndex };
      }
      captureEpochs.set(rawCaptureId, lifecycle.owner.mountEpoch);
      if (action.label === 'Preview') {
        const previousIntent = previewIntents.get(rawCaptureId);
        if (previousIntent) {
          return { message: actionWhere + '.groupLifecycle.owner.captureId duplicates a Group Preview request', actionIndex };
        }
        previewIntents.set(rawCaptureId, canonicalJson(lifecycle.candidateIntent));
      } else if (BUILDER_GROUP_SETTLEMENT_LABELS.has(action.label)) {
        const startIntent = previewIntents.get(rawCaptureId);
        if (!startIntent) {
          return { message: actionWhere + '.groupLifecycle.owner.captureId has no matching Group Preview intent', actionIndex };
        }
        if (startIntent !== canonicalJson(lifecycle.candidateIntent)) {
          return { message: actionWhere + '.groupLifecycle candidateIntent changed between Preview and its settlement', actionIndex };
        }
      }
    }
    if (lifecycle.candidateOwner) {
      const rawCaptureId = observedRequestKey(lifecycle.candidateOwner);
      if (!rawCaptureId || (!allowBrowserRequestId && !rawCaptureId.startsWith('capture:'))) {
        return { message: actionWhere + '.groupLifecycle.candidateOwner must preserve a bounded native capture identity', actionIndex };
      }
      if (!Number.isInteger(lifecycle.candidateOwner.mountEpoch) || lifecycle.candidateOwner.mountEpoch < 0 ||
          lifecycle.candidateOwner.mountEpoch > maxMountEpoch) {
        return { message: actionWhere + '.groupLifecycle.candidateOwner.mountEpoch is outside the selected bounded epoch domain', actionIndex };
      }
      if (!captureIds.has(rawCaptureId)) captureIds.set(rawCaptureId, captureIds.size);
      if (!allowBrowserRequestId && captureIds.size > BUILDER_GROUP_CAPTURE_TOKENS.length) {
        return { message: actionWhere + '.groupLifecycle.candidateOwner.captureId exceeds the finite capture token bound', actionIndex };
      }
      const priorEpoch = captureEpochs.get(rawCaptureId);
      if (priorEpoch !== undefined && priorEpoch !== lifecycle.candidateOwner.mountEpoch) {
        return { message: actionWhere + '.groupLifecycle.candidateOwner reused a raw captureId with a different mountEpoch', actionIndex };
      }
      captureEpochs.set(rawCaptureId, lifecycle.candidateOwner.mountEpoch);
      if (!previewIntents.has(rawCaptureId)) {
        return { message: actionWhere + '.groupLifecycle.candidateOwner.captureId has no preceding Group Preview request', actionIndex };
      }
    }
    if (BUILDER_GROUP_CANCEL_LABELS.has(action.label)) {
      const intent = lifecycle.candidateIntent;
      const candidateOwner = lifecycle.candidateOwner;
      if (intent === null) {
        if (candidateOwner !== null) {
          return { message: actionWhere + '.groupLifecycle.candidateOwner must be null when GroupEditCancel has no proposal intent', actionIndex };
        }
      } else {
        const expectedIntent = previewIntents.get(observedRequestKey(candidateOwner));
        if (!expectedIntent || expectedIntent !== canonicalJson(intent)) {
          return { message: actionWhere + '.groupLifecycle cancel candidateIntent does not match its actual candidateOwner Preview', actionIndex };
        }
      }
    }
    const intentProblem = addIntent(lifecycle.candidateIntent, actionWhere);
    if (intentProblem) return { message: intentProblem, actionIndex };
    if (cdaJourneyCase) {
      const targetProblem = cdaCreateGroupTargetStepProblem(
        lifecycle.candidateIntent,
        actionWhere + '.groupLifecycle.candidateIntent',
        lifecycle.savedBefore,
      );
      if (targetProblem) return { message: targetProblem, actionIndex };
    }
    for (const field of ['savedBefore', 'savedAfter']) {
      const saved = lifecycle[field];
      if (!isPlainObject(saved)) continue;
      const groupProblem = groupProjectionProblem(saved.group, actionWhere + '.groupLifecycle.' + field + '.group', allowedAggregates);
      if (groupProblem) return { message: groupProblem, actionIndex };
      const problem = addGroup(saved.group, actionWhere);
      if (problem) return { message: problem, actionIndex };
    }
    if (cdaJourneyCase) {
      if (action.label === 'Reload' && !Object.hasOwn(lifecycle, 'mountedGroup')) {
        return { message: actionWhere + '.groupLifecycle.mountedGroup must explicitly preserve the actual mounted Group or null', actionIndex };
      }
      if (Object.hasOwn(lifecycle, 'mountedGroup')) {
        const mounted = cdaMountedGroupProjection(lifecycle.mountedGroup, lifecycle.savedAfter,
          actionWhere + '.groupLifecycle.mountedGroup');
        if (mounted.problem) return { message: mounted.problem, actionIndex };
        const mountedProblem = addGroup(mounted.projection, actionWhere);
        if (mountedProblem) return { message: mountedProblem, actionIndex };
      }
    } else {
      const mountedProblem = addGroup(lifecycle.mountedGroup, actionWhere);
      if (mountedProblem) return { message: mountedProblem, actionIndex };
    }
    if (lifecycle.clickedRowTarget?.stepId) {
      const targetProblem = addStepId(lifecycle.clickedRowTarget.stepId, actionWhere);
      if (targetProblem) return { message: targetProblem, actionIndex };
      if (lifecycle.mountedGroup?.stepId && lifecycle.mountedGroup.stepId !== lifecycle.clickedRowTarget.stepId) {
        return { message: actionWhere + '.groupLifecycle.clickedRowTarget.stepId disagrees with mountedGroup.stepId', actionIndex };
      }
    }
  }
  const originEpochs = new Map();
  for (const [epoch, origin] of documentOrigins) {
    const priorEpoch = originEpochs.get(origin);
    if (priorEpoch !== undefined && priorEpoch !== epoch) {
      return {
        message: where + ' reused one browser documentTimeOrigin across distinct mount epochs',
        actionIndex: trace.actions.findIndex((action) => action.groupLifecycle?.mountEpoch === epoch),
      };
    }
    originEpochs.set(origin, epoch);
  }
  return null;
}

function verifyJava() {
  const result = spawnSync('java', ['-version'], { encoding: 'utf8' });
  if (result.error) fail('Java is required to run TLC ' + TLC_VERSION + ': ' + result.error.message);
  if (result.status !== 0) fail('Java is required to run TLC ' + TLC_VERSION + ': ' + (result.stderr || result.stdout || '').trim());
  return (result.stderr || result.stdout || '').trim().split('\n')[0];
}

function verifyJar(path) {
  if (!existsSync(path)) fail('TLC jar does not exist at ' + path);
  const absolute = realpathSync(path);
  const actual = sha256(readFileSync(absolute));
  if (actual !== TLC_SHA256) {
    fail('TLC ' + TLC_VERSION + ' checksum mismatch at ' + absolute + ': expected ' + TLC_SHA256 + ', found ' + actual);
  }
  return absolute;
}

async function acquireTlcJar() {
  if (process.env.TLA2TOOLS_JAR) {
    return verifyJar(process.env.TLA2TOOLS_JAR);
  }
  const cacheDirectory = join(tmpdir(), 'loom-builder-interactions-tools');
  const cachePath = join(cacheDirectory, 'tla2tools-' + TLC_VERSION + '.jar');
  if (existsSync(cachePath)) return verifyJar(cachePath);
  console.log('Downloading TLC ' + TLC_VERSION + ' from the official TLA+ release URL.');
  const response = await fetch(TLC_URL, { signal: AbortSignal.timeout(30000) });
  if (!response.ok) fail('TLC download failed with HTTP ' + response.status);
  const payload = Buffer.from(await response.arrayBuffer());
  const actual = sha256(payload);
  if (actual !== TLC_SHA256) fail('downloaded TLC checksum mismatch: expected ' + TLC_SHA256 + ', found ' + actual);
  mkdirSync(cacheDirectory, { recursive: true });
  const temporaryPath = cachePath + '.partial-' + process.pid + '-' + randomUUID();
  writeFileSync(temporaryPath, payload, { flag: 'wx' });
  renameSync(temporaryPath, cachePath);
  return verifyJar(cachePath);
}

function parseArgs() {
  const args = process.argv.slice(2);
  const options = { allowPartial: false, quiet: false, shapeOnly: false, traces: null, output: null,
    contract: null };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--allow-partial') options.allowPartial = true;
    else if (arg === '--quiet') options.quiet = true;
    else if (arg === '--shape-only') options.shapeOnly = true;
    else if (arg === '--traces') options.traces = args[++index];
    else if (arg === '--output') options.output = args[++index];
    else if (arg === '--contract') options.contract = args[++index];
    else if (arg === '--help' || arg === '-h') {
      console.log('usage: node scripts/verify-builder-traces.mjs --traces <traces.json> [--output <report.json>] [--contract <contract.json>] [--allow-partial] [--shape-only] [--quiet]');
      process.exit(0);
    } else {
      fail('unknown argument ' + arg + '; use --help for usage');
    }
  }
  if (!options.traces) fail('missing --traces <traces.json>');
  return options;
}

function validateContract(contract) {
  if (!isPlainObject(contract)) fail('trace contract must be a JSON object');
  if (contract.schemaVersion !== 1) fail('trace contract schemaVersion must be 1');
  if (typeof contract.sourceCommit !== 'string' || !/^[0-9a-f]{40}$/.test(contract.sourceCommit)) {
    fail('trace contract sourceCommit must be a 40-character lowercase Git SHA');
  }
  if (!['formal', 'journey'].includes(contract.sourceProfile)) {
    fail('trace contract sourceProfile must be "formal" or "journey"');
  }
  if (!isPlainObject(contract.models)) fail('trace contract models must be an object');
  for (const [model, definition] of Object.entries(contract.models)) {
    if (!isPlainObject(definition)) fail('trace contract model ' + model + ' must be an object');
    if (!Array.isArray(definition.actions) || definition.actions.length === 0) {
      fail('trace contract model ' + model + ' must list actions');
    }
    if (definition.actions.some((action) => typeof action !== 'string' || action.length === 0)) {
      fail('trace contract model ' + model + ' has a non-string action');
    }
  }
  if (!isPlainObject(contract.actionBindings)) fail('trace contract actionBindings must be an object');
  for (const [label, binding] of Object.entries(contract.actionBindings)) {
    if (!isPlainObject(binding) || typeof binding.event !== 'string' || binding.event.length === 0) {
      fail('trace contract actionBinding ' + label + ' must have an event');
    }
  }
  if (contract.unmodeledEventPolicy !== undefined) {
    if (!isPlainObject(contract.unmodeledEventPolicy)) {
      fail('trace contract unmodeledEventPolicy must be an object when present');
    }
    for (const [kind, reason] of Object.entries(contract.unmodeledEventPolicy)) {
      if (typeof reason !== 'string' || reason.trim().length === 0) {
        fail('trace contract unmodeledEventPolicy ' + kind + ' must have a reason');
      }
    }
  }
  if (!isPlainObject(contract.phase1)) fail('trace contract phase1 must be an object');
  const phase1 = contract.phase1;
  if (!Array.isArray(phase1.requiredActions) || phase1.requiredActions.some((action) => typeof action !== 'string')) {
    fail('trace contract phase1.requiredActions must be an array of strings');
  }
  if (phase1.requiredActionsByTrace !== undefined) {
    if (!isPlainObject(phase1.requiredActionsByTrace)) {
      fail('trace contract phase1.requiredActionsByTrace must be an object when present');
    }
    for (const [traceName, actions] of Object.entries(phase1.requiredActionsByTrace)) {
      if (!traceName.trim() || !Array.isArray(actions) || actions.some((action) => typeof action !== 'string')) {
        fail('trace contract phase1.requiredActionsByTrace entries must map non-empty trace names to arrays of strings');
      }
    }
  }
  if (phase1.requiredActionCountsByTrace !== undefined) {
    if (!isPlainObject(phase1.requiredActionCountsByTrace)) {
      fail('trace contract phase1.requiredActionCountsByTrace must be an object when present');
    }
    for (const [traceName, actionCounts] of Object.entries(phase1.requiredActionCountsByTrace)) {
      if (!traceName.trim() || !isPlainObject(actionCounts)) {
        fail('trace contract phase1.requiredActionCountsByTrace entries must map non-empty trace names to action-count objects');
      }
      for (const [action, count] of Object.entries(actionCounts)) {
        if (typeof action !== 'string' || action.length === 0 || !Number.isInteger(count) || count < 0) {
          fail('trace contract phase1.requiredActionCountsByTrace values must be non-negative integer counts');
        }
        if (!Object.values(contract.models).some((definition) => definition.actions.includes(action))) {
          fail('trace contract phase1.requiredActionCountsByTrace names unknown action ' + action);
        }
      }
    }
  }
  if (phase1.requiredActionMinimumsByTrace !== undefined) {
    if (!isPlainObject(phase1.requiredActionMinimumsByTrace)) {
      fail('trace contract phase1.requiredActionMinimumsByTrace must be an object when present');
    }
    for (const [traceName, actionCounts] of Object.entries(phase1.requiredActionMinimumsByTrace)) {
      if (!traceName.trim() || !isPlainObject(actionCounts)) {
        fail('trace contract phase1.requiredActionMinimumsByTrace entries must map non-empty trace names to action-count objects');
      }
      for (const [action, count] of Object.entries(actionCounts)) {
        if (!Number.isInteger(count) || count < 0 ||
            !Object.values(contract.models).some((definition) => definition.actions.includes(action))) {
          fail('trace contract phase1.requiredActionMinimumsByTrace values must name known actions and non-negative integer counts');
        }
      }
    }
  }
  if (!Array.isArray(phase1.requiredEvents) || phase1.requiredEvents.some((event) => typeof event !== 'string')) {
    fail('trace contract phase1.requiredEvents must be an array of strings');
  }
  if (phase1.requiredModels !== undefined && (!Array.isArray(phase1.requiredModels) || phase1.requiredModels.some((model) => typeof model !== 'string'))) {
    fail('trace contract phase1.requiredModels must be an array of strings when present');
  }
  for (const model of phase1.requiredModels ?? []) {
    if (!contract.models[model]) fail('trace contract phase1.requiredModels names unknown model ' + model);
  }
  if (contract.journeyCase !== undefined) {
    const journeyCase = contract.journeyCase;
    if (!isPlainObject(journeyCase) || journeyCase.model !== 'BuilderJourney' ||
        journeyCase.traceName !== BUILDER_CDA_GROUP_RELATED_TRACE_NAME ||
        typeof journeyCase.workflow !== 'string' || journeyCase.workflow.trim().length === 0) {
      fail('trace contract journeyCase must identify the cda-builder-group-related-source BuilderJourney');
    }
    if (journeyCase.exactActionCounts !== true || journeyCase.requireNoUnmodeledMutations !== true ||
        journeyCase.maxMountEpoch !== 5 || journeyCase.casTransitionCount !== 7) {
      fail('trace contract journeyCase must require exact semantic counts, no unmodeled mutations, five reload epochs, and seven draft transitions');
    }
    const nativeRequestLedger = journeyCase.nativeRequestLedger;
    if (!isPlainObject(nativeRequestLedger) || nativeRequestLedger.traceField !== 'nativeRequests' ||
        nativeRequestLedger.identityField !== 'browserRequestId' ||
        canonicalJson(nativeRequestLedger.requiredFields) !== canonicalJson([
          'index', 'browserRequestId', 'method', 'path', 'startedAt', 'isMutation',
        ]) || canonicalJson(nativeRequestLedger.optionalWhenUnavailable) !== canonicalJson(['requestId'])) {
      fail('trace contract journeyCase must define the complete nativeRequests observer ledger schema');
    }
    if (!Array.isArray(journeyCase.requiredMutationControlNames) ||
        journeyCase.requiredMutationControlNames.length === 0 ||
        journeyCase.requiredMutationControlNames.some((name) => typeof name !== 'string' || name.trim().length === 0) ||
        new Set(journeyCase.requiredMutationControlNames).size !== journeyCase.requiredMutationControlNames.length ||
        journeyCase.requiredMutationControlNames.some((name) => name.startsWith('request-owner-mount-epoch-')) ||
        journeyCase.observedOwnerEpochControlPrefix !== 'request-owner-mount-epoch-') {
      fail('trace contract journeyCase must define unique required mutation control names and the observed-owner epoch prefix');
    }
    if (!Array.isArray(journeyCase.readOnlySupportKinds) ||
        journeyCase.readOnlySupportKinds.some((kind) => typeof kind !== 'string' || kind.trim().length === 0) ||
        canonicalJson([...journeyCase.readOnlySupportKinds].sort()) !==
          canonicalJson(Object.keys(contract.unmodeledEventPolicy ?? {}).sort())) {
      fail('trace contract journeyCase must explicitly enumerate every allowed read-only support event kind');
    }
    if (!isPlainObject(contract.readOnlySupportRoutes) ||
        canonicalJson(Object.keys(contract.readOnlySupportRoutes).sort()) !==
          canonicalJson([...journeyCase.readOnlySupportKinds].sort())) {
      fail('trace contract readOnlySupportRoutes must bind every support kind to an explicit method and path suffix');
    }
    for (const [kind, route] of Object.entries(contract.readOnlySupportRoutes)) {
      if (!isPlainObject(route) || !['GET', 'POST'].includes(route.method) ||
          typeof route.pathSuffix !== 'string' || !route.pathSuffix.startsWith('/')) {
        fail('trace contract read-only route ' + kind + ' must define GET/POST and a path suffix');
      }
    }
    if (!Array.isArray(journeyCase.optionalActions) || journeyCase.optionalActions.some((action) => typeof action !== 'string')) {
      fail('trace contract journeyCase.optionalActions must list the modelled actions with variable counts');
    }
    if (!Array.isArray(journeyCase.rowExpansionStages) ||
        canonicalJson(journeyCase.rowExpansionStages) !== canonicalJson(BUILDER_CDA_ROW_EXPANSION_STAGES)) {
      fail('trace contract journeyCase must declare the ordered SpecimenPatient and PatientObservation row expansion stages');
    }
    const relatedSource = journeyCase.relatedSource;
    const route = relatedSource?.sourceRoute;
    if (!isPlainObject(relatedSource) ||
        canonicalJson(relatedSource.finiteStates) !== canonicalJson(['None', 'ObservationStatus']) ||
        relatedSource.resourceType !== 'Observation' || relatedSource.fieldPath !== 'status' ||
        relatedSource.form !== 'ALL' || relatedSource.contributorPolicy !== 'ALL_MATCHES' ||
        relatedSource.rowValuePolicy !== 'ALL' || !Array.isArray(route) || route.length !== 2 ||
        route[0]?.fromResourceType !== 'Specimen' || route[0]?.toResourceType !== 'Patient' ||
        route[1]?.fromResourceType !== 'Patient' || route[1]?.toResourceType !== 'Observation') {
      fail('trace contract journeyCase must bind the finite Observation.status related-source route and ALL policies');
    }
    const builderActions = contract.models.BuilderJourney?.actions;
    const caseCounts = phase1.requiredActionCountsByTrace?.[journeyCase.traceName];
    const minimumCounts = phase1.requiredActionMinimumsByTrace?.[journeyCase.traceName];
    const countedActions = new Set([
      ...Object.keys(caseCounts ?? {}),
      ...Object.keys(minimumCounts ?? {}),
      ...journeyCase.optionalActions,
    ]);
    if (!Array.isArray(builderActions) || !isPlainObject(caseCounts) || !isPlainObject(minimumCounts) ||
        canonicalJson([...countedActions].sort()) !== canonicalJson([...builderActions].sort())) {
      fail('trace contract journeyCase must assign every BuilderJourney action an exact, minimum, or variable count policy');
    }
    if (journeyCase.optionalActions.some((action) =>
      Object.hasOwn(caseCounts, action) || Object.hasOwn(minimumCounts, action))) {
      fail('trace contract journeyCase optional action labels cannot also have exact or minimum counts');
    }
    for (const action of Object.keys(minimumCounts)) {
      if (Object.hasOwn(caseCounts, action)) fail('trace contract journeyCase action ' + action + ' cannot have both exact and minimum counts');
    }
    for (const action of BUILDER_CDA_GROUP_RELATED_ACTIONS) {
      if (!builderActions.includes(action) || !contract.actionBindings[action]) {
        fail('trace contract journeyCase is missing the BuilderJourney binding for ' + action);
      }
    }
    if (!phase1.requiredActionsByTrace?.[journeyCase.traceName]) {
      fail('trace contract journeyCase must declare required actions for its exact trace');
    }
  }
  return contract;
}

function expectedCounts(contract) {
  const phase1 = contract.phase1;
  const scheduleCount = Number.isInteger(phase1.expectedScheduleCount)
    ? phase1.expectedScheduleCount : DEFAULT_EXPECTED_SCHEDULE_COUNT;
  const findingCount = Number.isInteger(phase1.expectedFindingCount)
    ? phase1.expectedFindingCount : DEFAULT_EXPECTED_FINDING_COUNT;
  const traceCount = Number.isInteger(phase1.expectedTraceCount)
    ? phase1.expectedTraceCount : DEFAULT_EXPECTED_TRACE_COUNT;
  return { scheduleCount, findingCount, traceCount };
}

function allowedEvents(contract) {
  const events = new Set(Object.values(contract.actionBindings).map((binding) => binding.event));
  for (const event of contract.phase1.requiredEvents) events.add(event);
  return events;
}

function normalizedActionLabel(model, label, actions) {
  if (actions.has(label)) return label;
  // BuilderPreview's DiscoveryComplete action records its outcome in lastAction.
  if (model === 'BuilderPreview' && /^DiscoveryComplete\(/.test(label) && actions.has('DiscoveryComplete')) {
    return 'DiscoveryComplete';
  }
  return null;
}

function ownerProblem(owner, where, expectedKind = null, expectedAttempt = null) {
  if (!isPlainObject(owner)) return where + ' must be an object';
  if (!['proposal', 'preview', 'apply', 'discovery'].includes(owner.kind)) {
    return where + '.kind must be proposal, preview, apply, or discovery';
  }
  const attempts = owner.kind === 'discovery' ? new Set(['D1']) : new Set(['P1', 'P2']);
  if (!attempts.has(owner.attempt)) return where + '.attempt is not a bounded attempt for ' + owner.kind;
  if (expectedKind && owner.kind !== expectedKind) {
    return where + '.kind must be ' + expectedKind + ' for this action';
  }
  if (expectedAttempt && owner.attempt !== expectedAttempt) {
    return where + '.attempt ' + owner.attempt + ' does not match action attempt ' + expectedAttempt;
  }
  return null;
}

function requiredOwnerForAction(model, label) {
  if (model === 'ConstructionLifecycle') {
    if (/^(?:StartProposal\(P[12]\)|ReplaceProposalOwner|RetryProposal\(P1-to-P2\)|CancelProposalOwner|ProposalComplete\(P[12]\)|ProposalError\(P[12]\)|LateProposal(?:Error|Complete)Ignored\(P[12]\)|StartPreview\(P[12]\)|Preview(?:Error|Complete)\(P[12]\)|LatePreview(?:Error|Complete)Ignored\(P[12]\)|BeginApply\((?:accepted|rejected)\))$/.test(label)) return true;
    return false;
  }
  if (model === 'BuilderPreview') {
    return new Set([
      'StartProposal', 'ReplaceProposal', 'ProposalComplete', 'StartPreview',
      'PreviewComplete', 'BeginApply', 'ApplyAck', 'ScheduleDiscovery', 'DiscoveryComplete',
    ]).has(label);
  }
  if (model === 'BuilderJourney') {
    if (/^(?:Preview|PreviewComplete|PreviewError|LatePreviewCompleteIgnored|LatePreviewErrorIgnored|PreviewRequestCancelled|ChoicePreview|ChoicePreviewComplete|ChoicePreviewError|RelatedSourcePreview|RelatedSourceCancel)$/.test(label)) return true;
    return new Set([
      'Group', 'GroupAgain', 'AddColumn', 'EditGroup', 'EditGroupAgain', 'RemoveGroup',
      'ExpandRows', 'RelatedSourceApply',
    ]).has(label);
  }
  return false;
}

function expectedOwnerKind(model, label) {
  if (model === 'ConstructionLifecycle') {
    if (/^(?:StartProposal|ReplaceProposalOwner|RetryProposal|CancelProposalOwner|ProposalComplete|ProposalError|LateProposal)/.test(label)) return 'proposal';
    if (/^(?:StartPreview|PreviewComplete|PreviewError|LatePreview)/.test(label)) return 'preview';
    if (/^BeginApply/.test(label)) return 'proposal';
  }
  if (model === 'BuilderPreview') {
    if (/^(?:StartProposal|ReplaceProposal|ProposalComplete)/.test(label)) return 'proposal';
    if (/^(?:StartPreview|PreviewComplete)/.test(label)) return 'preview';
    if (/^(?:BeginApply|ApplyAck)/.test(label)) return 'apply';
    if (/^(?:ScheduleDiscovery|DiscoveryComplete)/.test(label)) return 'discovery';
  }
  if (model === 'BuilderJourney') {
    if (/^(?:Preview|PreviewComplete|PreviewError|LatePreviewCompleteIgnored|LatePreviewErrorIgnored|PreviewRequestCancelled|ChoicePreview|ChoicePreviewComplete|ChoicePreviewError|RelatedSourcePreview|RelatedSourceCancel)$/.test(label)) return 'proposal';
    if (/^(?:Group|GroupAgain|AddColumn|EditGroup|EditGroupAgain|RemoveGroup|ExpandRows|RelatedSourceApply)$/.test(label)) return 'apply';
  }
  return null;
}

function requestProblem(request, where) {
  if (!isPlainObject(request)) return where + ' must be an object';
  if (typeof request.method !== 'string' || typeof request.path !== 'string') {
    return where + ' must include string method and path';
  }
  if (request.requestId !== undefined && request.requestId !== null && typeof request.requestId !== 'string') {
    return where + '.requestId must be a string or null when present';
  }
  if (request.captureId !== undefined && request.captureId !== null && typeof request.captureId !== 'string') return where + '.captureId must be a string or null when present';
  if (request.commandId !== undefined && request.commandId !== null && typeof request.commandId !== 'string') return where + '.commandId must be a string or null when present';
  if (request.browserRequestId !== undefined && typeof request.browserRequestId !== 'string') return where + '.browserRequestId must be a string when present';
  return null;
}

function responseProblem(response, where) {
  if (!isPlainObject(response)) return where + ' must be an object';
  if (!(typeof response.status === 'string' || typeof response.status === 'number')) {
    return where + '.status must be a string or number';
  }
  if (response.requestId !== undefined && response.requestId !== null && typeof response.requestId !== 'string') return where + '.requestId must be a string or null when present';
  if (response.captureId !== undefined && response.captureId !== null && typeof response.captureId !== 'string') return where + '.captureId must be a string or null when present';
  if (response.commandId !== undefined && response.commandId !== null && typeof response.commandId !== 'string') return where + '.commandId must be a string or null when present';
  if (response.browserRequestId !== undefined && typeof response.browserRequestId !== 'string') return where + '.browserRequestId must be a string when present';
  if (response.proposalId !== undefined && response.proposalId !== null && typeof response.proposalId !== 'string') {
    return where + '.proposalId must be a string or null when present';
  }
  for (const field of ['proposalOutputId', 'proposalSnapshotToken', 'proposalDraftDigest', 'proposalPreviewStatus']) {
    if (response[field] !== undefined && response[field] !== null && typeof response[field] !== 'string') {
      return where + '.' + field + ' must be a string or null when present';
    }
  }
  if (response.proposalDraftVersion !== undefined && response.proposalDraftVersion !== null && typeof response.proposalDraftVersion !== 'number') {
    return where + '.proposalDraftVersion must be a number or null when present';
  }
  if (response.errorCode !== undefined && response.errorCode !== null && typeof response.errorCode !== 'string') {
    return where + '.errorCode must be a string or null when present';
  }
  if (response.preview !== undefined && response.preview !== null) {
    if (!isPlainObject(response.preview)) return where + '.preview must be an object or null';
    for (const field of ['receiptId', 'outputId']) {
      if (response.preview[field] !== undefined && response.preview[field] !== null && typeof response.preview[field] !== 'string') {
        return where + '.preview.' + field + ' must be a string or null';
      }
    }
    if (response.preview.rowCount !== undefined && response.preview.rowCount !== null && typeof response.preview.rowCount !== 'number') {
      return where + '.preview.rowCount must be a number or null';
    }
    if (response.preview.rowsPresent !== undefined && typeof response.preview.rowsPresent !== 'boolean') {
      return where + '.preview.rowsPresent must be a boolean';
    }
  }
  return null;
}

function observedRequestKey(value) {
  if (!isPlainObject(value)) return null;
  if (typeof value.captureId === 'string' && value.captureId.length > 0) return 'capture:' + value.captureId;
  if (typeof value.browserRequestId === 'string' && value.browserRequestId.length > 0) return 'browser:' + value.browserRequestId;
  return null;
}

function initialPreviewIdentityProblem(initialPreview, trace, where, cdaRelatedCase) {
  const request = initialPreview.request;
  const response = initialPreview.response;
  const settlement = initialPreview.settlement;
  if (!cdaRelatedCase) {
    const captureId = request.captureId;
    if (typeof captureId !== 'string' || captureId.length === 0 ||
        response.captureId !== captureId || settlement.captureId !== captureId ||
        settlement.kind !== 'http-response') {
      return where + ' request/response captureId must be present and identical';
    }
    if (request.requestId !== null && response.requestId !== null && request.requestId !== response.requestId) {
      return where + ' non-null wire requestIds do not match';
    }
    return null;
  }

  const browserRequestId = initialPreview.owner?.browserRequestId;
  const records = [initialPreview.owner, request, response, settlement];
  if (typeof browserRequestId !== 'string' || browserRequestId.length === 0 ||
      records.some((record) => typeof record?.browserRequestId !== 'string' ||
        record.browserRequestId.length === 0 || record.browserRequestId !== browserRequestId)) {
    return where + ' owner, request, response, and settlement must preserve the same actual browserRequestId';
  }
  if (settlement.kind !== 'http-response') {
    return where + ' settlement must preserve the actual http-response';
  }
  if (records.some((record) => record.captureId !== null && record.captureId !== undefined)) {
    return where + ' must preserve captureId as absent or null rather than aliasing browserRequestId';
  }
  if (records.some((record) => record.requestId !== request.requestId)) {
    return where + ' request, response, owner, and settlement must preserve the same raw wire requestId value';
  }
  if (!Array.isArray(trace.nativeRequests)) {
    return where + ' must retain the complete native request ledger for browserRequestId binding';
  }
  const ledgerMatches = trace.nativeRequests.filter((entry) => entry?.browserRequestId === browserRequestId);
  if (ledgerMatches.length !== 1) {
    return where + ' browserRequestId must identify exactly one native request ledger entry';
  }
  const ledger = ledgerMatches[0];
  if (!Number.isInteger(request.index) || request.index < 0 || ledger.index !== request.index ||
      request.method !== 'POST' || request.path !== '/preview' ||
      ledger.method !== request.method || cdaNativePathSuffix(ledger.path) !== request.path ||
      ledger.startedAt !== request.startedAt || ledger.isMutation !== request.isMutation ||
      ledger.requestId !== request.requestId) {
    return where + ' request must match its exact native request ledger entry and observer index';
  }
  if (request.isMutation !== false) {
    return where + ' source-table preview request must preserve the observer non-mutating flag';
  }
  for (const record of [initialPreview.owner, response, settlement]) {
    if (record.index !== undefined && record.index !== request.index) {
      return where + ' exposed request observer indices do not match';
    }
    if (record.method !== undefined && record.method !== request.method) {
      return where + ' exposed request methods do not match';
    }
    if (record.path !== undefined && record.path !== request.path) {
      return where + ' exposed request paths do not match';
    }
  }
  return null;
}

function actualRequestIdentityProblem(owner, request, settlement, where) {
  const ownerKey = observedRequestKey(owner);
  const requestKey = observedRequestKey(request);
  const settlementKey = observedRequestKey(settlement);
  if (!ownerKey || !requestKey || ownerKey !== requestKey) {
    return where + ' owner identity must match the actual native request captureId or browserRequestId';
  }
  if (settlement && settlementKey !== ownerKey) {
    return where + ' owner identity must match the actual request settlement captureId or browserRequestId';
  }
  for (const field of ['captureId', 'browserRequestId', 'requestId']) {
    if (owner?.[field] !== undefined && owner?.[field] !== null &&
        request?.[field] !== undefined && request?.[field] !== null && owner[field] !== request[field]) {
      return where + ' owner.' + field + ' must match the native request';
    }
    if (owner?.[field] !== undefined && owner?.[field] !== null &&
        settlement?.[field] !== undefined && settlement?.[field] !== null && owner[field] !== settlement[field]) {
      return where + ' owner.' + field + ' must match the native settlement';
    }
  }
  return null;
}

function journeyPreviewEvidenceProblem(action, where) {
  if (action.label !== 'PreviewComplete' || action.observation?.status !== 'ready' || action.response === null) return null;
  const response = action.response;
  const observation = action.observation;
  const embedded = observation.embeddedProposalPreview;
  if (typeof response.proposalId !== 'string' || response.proposalId.length === 0 ||
      typeof observation.proposalId !== 'string' || observation.proposalId.length === 0 ||
      response.proposalId !== observation.proposalId) {
    return where + ' response.proposalId does not match displayed observation.proposalId';
  }
  if (!['row-operation', 'main-table'].includes(observation.proposalPreviewSurface)) {
    return where + ' ready proposal has no matching rendered proposal preview surface';
  }
  const pairs = [
    ['proposalOutputId', observation.proposalOutputId, 'response.proposalOutputId does not match displayed observation.proposalOutputId'],
    ['proposalSnapshotToken', observation.proposalSnapshotToken, 'response.proposalSnapshotToken does not match displayed observation.proposalSnapshotToken'],
    ['proposalDraftVersion', observation.proposalDraftVersion, 'response.proposalDraftVersion does not match displayed observation.proposalDraftVersion'],
    ['proposalDraftDigest', observation.proposalDraftDigest, 'response.proposalDraftDigest does not match displayed observation.proposalDraftDigest'],
  ];
  for (const [responseField, observedValue, message] of pairs) {
    const responseValue = response[responseField];
    if (responseValue !== undefined && responseValue !== null &&
        (observedValue === undefined || observedValue === null || responseValue !== observedValue)) {
      return where + ' ' + message;
    }
  }
  if (isPlainObject(response.preview)) {
    const previewPairs = [
      ['receiptId', embedded?.receiptId, 'preview.receiptId', 'embeddedProposalPreview.receiptId'],
      ['outputId', embedded?.outputId, 'preview.outputId', 'embeddedProposalPreview.outputId'],
    ];
    for (const [responseField, observedValue, responseName, observedName] of previewPairs) {
      const responseValue = response.preview[responseField];
      if (responseValue !== undefined && responseValue !== null &&
          (observedValue === undefined || observedValue === null || responseValue !== observedValue)) {
        return where + ' response.' + responseName + ' does not match displayed observation.' + observedName;
      }
    }
    const renderedRows = embedded?.rowsPresent;
    if (typeof response.preview.rowsPresent === 'boolean' &&
        (typeof renderedRows !== 'boolean' || response.preview.rowsPresent !== renderedRows)) {
      return where + ' response.preview.rowsPresent does not match displayed observation.embeddedProposalPreview.rowsPresent';
    }
  }
  return null;
}

function journeyChoiceTraceProblem(trace, where) {
  const startsByCapture = new Map();
  const settlementsByCapture = new Map();
  const completedByCommandId = new Map();
  const addColumnsByCommandId = new Map();
  const choiceSettlementLabels = new Set(['ChoicePreviewComplete', 'ChoicePreviewError']);

  for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
    const action = trace.actions[actionIndex];
    const actionWhere = where + '.actions[' + actionIndex + ']';
    if (action.label === 'ChoicePreview') {
      const captureId = action.request?.captureId;
      if (startsByCapture.has(captureId)) {
        return { message: actionWhere + '.request.captureId duplicates another ChoicePreview request', actionIndex };
      }
      startsByCapture.set(captureId, { action, actionIndex });
    } else if (choiceSettlementLabels.has(action.label)) {
      const captureId = action.request?.captureId;
      if (settlementsByCapture.has(captureId)) {
        return { message: actionWhere + ' duplicates a ChoicePreview settlement for the same captureId', actionIndex };
      }
      settlementsByCapture.set(captureId, { action, actionIndex });
    } else if (action.label === 'AddColumn') {
      const commandId = action.request?.commandId;
      if (typeof commandId !== 'string' || commandId.trim().length === 0) {
        return { message: actionWhere + '.request.commandId must be a non-empty string for AddColumn', actionIndex };
      }
      if (addColumnsByCommandId.has(commandId)) {
        return { message: actionWhere + '.request.commandId duplicates another AddColumn command', actionIndex };
      }
      addColumnsByCommandId.set(commandId, { action, actionIndex });
    }
  }

  for (const [captureId, start] of startsByCapture) {
    const settlement = settlementsByCapture.get(captureId);
    if (!settlement) {
      return {
        message: where + '.actions[' + start.actionIndex + '] ChoicePreview request has no matching ChoicePreviewComplete/Error with the same captureId',
        actionIndex: start.actionIndex,
      };
    }
    const request = start.action.request;
    const resultRequest = settlement.action.request;
    if (canonicalJson(start.action.owner) !== canonicalJson(settlement.action.owner) ||
        request.method !== resultRequest.method || request.path !== resultRequest.path ||
        request.path !== 'construction-choice-proposals' || resultRequest.captureId !== captureId) {
      return {
        message: where + '.actions[' + settlement.actionIndex + '] ChoicePreview settlement must match its request owner, method, path, and captureId',
        actionIndex: settlement.actionIndex,
      };
    }
    if (settlement.action.label === 'ChoicePreviewError') continue;

    const choiceCommandId = request.commandId;
    const choiceResponse = settlement.action.response;
    if (typeof choiceCommandId !== 'string' || choiceCommandId.trim().length === 0 ||
        resultRequest.commandId !== choiceCommandId || choiceResponse?.commandId !== choiceCommandId) {
      return {
        message: where + '.actions[' + settlement.actionIndex + '] ChoicePreviewComplete request/response commandId values must match the ChoicePreview request commandId',
        actionIndex: settlement.actionIndex,
      };
    }
    if (typeof choiceResponse.status !== 'number' || choiceResponse.status < 200 || choiceResponse.status >= 300 ||
        choiceResponse.proposalPreviewStatus !== 'READY') {
      return {
        message: where + '.actions[' + settlement.actionIndex + '] ChoicePreviewComplete must carry a successful 2xx response with previewStatus READY',
        actionIndex: settlement.actionIndex,
      };
    }
    if (completedByCommandId.has(choiceCommandId)) {
      return {
        message: where + '.actions[' + settlement.actionIndex + '] ChoicePreviewComplete commandId duplicates another choice preview',
        actionIndex: settlement.actionIndex,
      };
    }
    const preview = settlement.action.response?.preview;
    if (!isPlainObject(preview) || typeof preview.receiptId !== 'string' || preview.receiptId.trim().length === 0 ||
        typeof preview.outputId !== 'string' || preview.outputId.trim().length === 0 ||
        preview.rowsPresent !== true || typeof preview.rowCount !== 'number' || !Number.isFinite(preview.rowCount) || preview.rowCount < 0) {
      return {
        message: where + '.actions[' + settlement.actionIndex + '] ChoicePreviewComplete response.preview must include non-empty receipt/output IDs, rowsPresent true, and typed rowCount',
        actionIndex: settlement.actionIndex,
      };
    }
    completedByCommandId.set(choiceCommandId, { action: settlement.action, actionIndex: settlement.actionIndex, preview });
  }

  for (const [captureId, settlement] of settlementsByCapture) {
    if (!startsByCapture.has(captureId)) {
      return {
        message: where + '.actions[' + settlement.actionIndex + '] ChoicePreview settlement has no ChoicePreview request with the same captureId',
        actionIndex: settlement.actionIndex,
      };
    }
  }

  const consumedChoiceCommandIds = new Set();
  for (const [commandId, addColumn] of addColumnsByCommandId) {
    const action = addColumn.action;
    const actionWhere = where + '.actions[' + addColumn.actionIndex + ']';
    const responseCommandId = action.response?.commandId;
    if (typeof action.response?.status !== 'number' || action.response.status < 200 || action.response.status >= 300) {
      return { message: actionWhere + ' AddColumn response.status must be a successful 2xx status', actionIndex: addColumn.actionIndex };
    }
    if (responseCommandId !== commandId) {
      return { message: actionWhere + ' AddColumn response.commandId does not match request.commandId', actionIndex: addColumn.actionIndex };
    }
    const choice = completedByCommandId.get(commandId);
    if (!choice) {
      return { message: actionWhere + ' AddColumn commandId has no matching completed ChoicePreview proof', actionIndex: addColumn.actionIndex };
    }
    if (consumedChoiceCommandIds.has(commandId)) {
      return { message: actionWhere + ' AddColumn consumes a ChoicePreview commandId more than once', actionIndex: addColumn.actionIndex };
    }
    consumedChoiceCommandIds.add(commandId);
    const sourcePreview = action.observation?.sourceTablePreview;
    if (!isPlainObject(sourcePreview) || sourcePreview.status !== 'ready' || sourcePreview.rowsPresent !== true ||
        typeof sourcePreview.renderedRowCount !== 'number' || !Number.isFinite(sourcePreview.renderedRowCount) ||
        sourcePreview.renderedRowCount < 0) {
      return { message: actionWhere + ' AddColumn must capture a ready rendered sourceTablePreview with typed row count after command settlement', actionIndex: addColumn.actionIndex };
    }
    if (sourcePreview.receiptId !== choice.preview.receiptId || sourcePreview.outputId !== choice.preview.outputId) {
      return { message: actionWhere + ' AddColumn sourceTablePreview receipt/output does not match the ChoicePreviewComplete response.preview', actionIndex: addColumn.actionIndex };
    }
  }

  for (const [commandId, choice] of completedByCommandId) {
    if (!consumedChoiceCommandIds.has(commandId)) {
      return {
        message: where + '.actions[' + choice.actionIndex + '] ChoicePreviewComplete commandId was not consumed by an AddColumn command',
        actionIndex: choice.actionIndex,
      };
    }
  }
  return null;
}

function settlementProblem(settlement, where) {
  if (!isPlainObject(settlement)) return where + ' must be an object';
  if (!['http-response', 'request-failed'].includes(settlement.kind)) {
    return where + '.kind must be http-response or request-failed';
  }
  const hasCaptureId = typeof settlement.captureId === 'string' && settlement.captureId.length > 0;
  const hasBrowserRequestId = typeof settlement.browserRequestId === 'string' && settlement.browserRequestId.length > 0;
  if (!hasCaptureId && !hasBrowserRequestId) {
    return where + ' must preserve a non-empty captureId or browserRequestId';
  }
  if (settlement.requestId !== undefined && settlement.requestId !== null && typeof settlement.requestId !== 'string') {
    return where + '.requestId must be a string or null when present';
  }
  if (settlement.browserRequestId !== undefined && typeof settlement.browserRequestId !== 'string') {
    return where + '.browserRequestId must be a string when present';
  }
  if (settlement.errorText !== undefined && typeof settlement.errorText !== 'string') {
    return where + '.errorText must be a string when present';
  }
  return null;
}

function observationProblem(observation, where, model) {
  if (!isPlainObject(observation)) return where + ' must be an object';
  if (typeof observation.status !== 'string') return where + '.status must be a string';
  const statuses = MODEL_STATUSES.get(model) ?? STATUSES;
  if (!(statuses.has(observation.status) || (model === 'BuilderJourney' && ['absent', 'unknown'].includes(observation.status)))) {
    return where + '.status "' + observation.status + '" has no model observation mapping for ' + model;
  }
  if (observation.tablePreviewStatus !== undefined && typeof observation.tablePreviewStatus !== 'string') {
    return where + '.tablePreviewStatus must be a string when present';
  }
  if (model === 'ReconcileLifecycle') {
    return reconcileObservationProblem(observation.reconcile, where + '.reconcile');
  }
  for (const field of OBSERVATION_BOOLEAN_FIELDS) {
    if (typeof observation[field] !== 'boolean') return where + '.' + field + ' must be a boolean';
  }
  const identityFields = ['proposalId', 'proposalOutputId', 'proposalSnapshotToken', 'proposalDraftDigest', 'previewReceiptId', 'previewOutputId'];
  for (const field of identityFields) {
    if (observation[field] !== undefined && observation[field] !== null && typeof observation[field] !== 'string') {
      return where + '.' + field + ' must be a string or null';
    }
  }
  if (observation.proposalDraftVersion !== undefined && observation.proposalDraftVersion !== null && typeof observation.proposalDraftVersion !== 'number') {
    return where + '.proposalDraftVersion must be a number or null';
  }
  if (model !== 'BuilderJourney' && identityFields.some((field) => observation[field] === undefined) ||
      model !== 'BuilderJourney' && observation.proposalDraftVersion === undefined) {
    return where + ' must include explicit null/string hook proposal and preview identity fields';
  }
  for (const [field, nested] of [['sourceTablePreview', observation.sourceTablePreview], ['embeddedProposalPreview', observation.embeddedProposalPreview]]) {
    if (nested === undefined || nested === null) continue;
    if (!isPlainObject(nested)) return where + '.' + field + ' must be an object or null';
    if (nested.status !== undefined && nested.status !== null && typeof nested.status !== 'string') return where + '.' + field + '.status must be a string or null';
    for (const idField of ['receiptId', 'outputId', 'proposalId', 'draftDigest']) {
      if (nested[idField] !== undefined && nested[idField] !== null && typeof nested[idField] !== 'string') {
        return where + '.' + field + '.' + idField + ' must be a string or null';
      }
    }
    for (const numberField of ['draftVersion', 'rowCount', 'renderedRowCount']) {
      if (nested[numberField] !== undefined && nested[numberField] !== null && typeof nested[numberField] !== 'number') {
        return where + '.' + field + '.' + numberField + ' must be a number or null';
      }
    }
    if (nested.rowsPresent !== undefined && typeof nested.rowsPresent !== 'boolean') return where + '.' + field + '.rowsPresent must be a boolean';
  }
  if (observation.apply !== undefined && observation.apply !== null) {
    if (!isPlainObject(observation.apply)) return where + '.apply must be an object or null';
    for (const field of ['available', 'enabled', 'applying']) {
      if (observation.apply[field] !== undefined && observation.apply[field] !== null && typeof observation.apply[field] !== 'boolean') {
        return where + '.apply.' + field + ' must be a boolean or null';
      }
    }
  }
  if (model === 'BuilderJourney') {
    const surface = observation.proposalPreviewSurface;
    if (typeof surface !== 'string' || !BUILDER_JOURNEY_PREVIEW_SURFACES.has(surface)) {
      return where + '.proposalPreviewSurface must be row-operation, main-table, or unknown';
    }
    if (!Object.hasOwn(observation, 'embeddedProposalPreview')) {
      return where + '.embeddedProposalPreview must be explicit for the selected proposal preview surface';
    }
    const embedded = observation.embeddedProposalPreview;
    if (surface === 'unknown') {
      if (embedded !== null) {
        return where + '.proposalPreviewSurface "unknown" cannot claim an embedded proposal preview';
      }
      if (observation.previewReceiptPresent || observation.rowsPresent || observation.receiptPresent ||
          observation.previewReceiptId !== null || observation.previewOutputId !== null) {
        return where + '.proposalPreviewSurface "unknown" cannot populate proposal preview fields';
      }
    } else {
      if (!isPlainObject(embedded)) {
        return where + '.proposalPreviewSurface ' + JSON.stringify(surface) + ' requires an embedded proposal preview';
      }
      if (typeof observation.proposalId !== 'string' || observation.proposalId.length === 0) {
        return where + '.proposalPreviewSurface ' + JSON.stringify(surface) + ' requires a rendered proposalId';
      }
      if (surface === 'main-table') {
        const rawMainPreview = observation.sourceTablePreview;
        const rawMainProposalId = rawMainPreview?.proposalId;
        if (typeof rawMainProposalId !== 'string' || rawMainProposalId.length === 0) {
          return where + '.main-table proposal preview requires sourceTablePreview.proposalId';
        }
        if (rawMainProposalId !== observation.proposalId) {
          return where + '.main-table sourceTablePreview.proposalId does not match displayed proposalId';
        }
        const rawMainReceiptId = rawMainPreview.receiptId;
        if (typeof rawMainReceiptId !== 'string' || rawMainReceiptId.length === 0) {
          return where + '.main-table proposal preview requires sourceTablePreview.receiptId';
        }
        if (rawMainReceiptId !== observation.proposalId) {
          return where + '.main-table sourceTablePreview.receiptId does not match displayed proposalId';
        }
        for (const field of ['status', 'receiptId', 'outputId', 'rowsPresent']) {
          if (!Object.hasOwn(rawMainPreview, field) || !Object.hasOwn(embedded, field) ||
              rawMainPreview[field] !== embedded[field]) {
            return where + '.main-table embeddedProposalPreview.' + field + ' does not match raw sourceTablePreview.' + field;
          }
        }
        if (rawMainPreview.rowCount !== undefined && rawMainPreview.rowCount !== null &&
            rawMainPreview.rowCount !== embedded.rowCount) {
          return where + '.main-table embeddedProposalPreview.rowCount does not match raw sourceTablePreview.rowCount';
        }
      }
      if (embedded.proposalId !== observation.proposalId) {
        return where + '.embeddedProposalPreview.proposalId does not match displayed proposalId';
      }
      if (observation.previewReceiptPresent !== Boolean(embedded.receiptId) ||
          observation.receiptPresent !== Boolean(embedded.receiptId) ||
          observation.rowsPresent !== Boolean(embedded.rowsPresent) ||
          observation.previewReceiptId !== (embedded.receiptId ?? null) ||
          observation.previewOutputId !== (embedded.outputId ?? null)) {
        return where + '.proposal preview fields must be flattened from the selected surface only';
      }
    }
  }
  return null;
}

function reconcileWorkspaceProblem(workspaceKey, identity, where, checkOutput = true) {
  const expected = workspaceKey === 'W1'
    ? { draftVersion: 1, draftDigest: 'sha256:draft-1' }
    : { draftVersion: 2, draftDigest: 'sha256:draft-2' };
  if (workspaceKey !== 'W1' && workspaceKey !== 'W2') return where + '.workspaceKey must be W1 or W2';
  if (identity.draftVersion !== expected.draftVersion || identity.draftDigest !== expected.draftDigest ||
      identity.snapshotToken !== 'snapshot-1' || (checkOutput && identity.outputId !== 'specimens') || identity.explorerId !== 'test') {
    return where + ' raw request/workspace identity does not match ' + workspaceKey;
  }
  return null;
}

function reconcileOutputModelKey(outputId) {
  if (outputId === 'specimens') return 'O1';
  if (outputId === 'patients') return 'O2';
  return null;
}

function reconcileDigestModelKey(digest) {
  if (digest === 'sha256:draft-1') return 'D1';
  if (digest === 'sha256:draft-2') return 'D2';
  return null;
}

function reconcileSnapshotModelKey(snapshotToken) {
  return snapshotToken === 'snapshot-1' ? 'S1' : null;
}

function reconcileExplorerModelKey(explorerId) {
  if (explorerId === 'test') return 'E1';
  if (explorerId === 'other-explorer') return 'E2';
  return null;
}

function reconcileIdentityProblem(trace, where) {
  const expectedLabels = [
    'WorkspaceChangeStartsReconcile(B)',
    'ReconcileSuccess(B)',
    'LateReconcileIgnored(A)',
  ];
  if (trace.name.split(' > ').at(-1) !== 'formal mounted reconcile race keeps B visible after late A settles') {
    return { message: where + ' ReconcileLifecycle traces must be captured by the named mounted race test' };
  }
  if (!trace.initialObservation?.reconcile || trace.actions.length !== expectedLabels.length ||
      trace.initialAction?.label !== 'RequestReconcile(A)' || trace.initialAction.event !== 'requestA') {
    return { message: where + ' must contain the captured initial A request and three subsequent mounted observations' };
  }
  const initial = trace.initialObservation.reconcile;
  if (initial.current.modelKey !== 'W1' ||
      initial.request?.modelKey !== 'A' || initial.receipt !== null || initial.returned !== null) {
    return { message: where + '.initialObservation must capture pending A against mounted workspace W1' };
  }
  const initialWorkspaceProblem = reconcileWorkspaceProblem('W1', initial.current, where + '.initialObservation.reconcile.current', false);
  if (initialWorkspaceProblem) return { message: initialWorkspaceProblem };
  const calls = new Map();
  const requestIds = new Set();
  const initialRequest = initial.request;
  if (!initialRequest) return { message: where + '.initialObservation must capture request A arguments' };
  const initialRequestProblem = reconcileWorkspaceProblem('W1', initialRequest, where + '.initialObservation.reconcile.request');
  if (initialRequestProblem) return { message: initialRequestProblem };
  calls.set('A', initialRequest);
  requestIds.add(initialRequest.rawRequestId);
  for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
    const action = trace.actions[actionIndex];
    const actionWhere = where + '.actions[' + actionIndex + ']';
    if (action.label !== expectedLabels[actionIndex]) {
      return { message: actionWhere + ' must be ' + expectedLabels[actionIndex], actionIndex };
    }
    const observation = action.observation.reconcile;
    const expectedRequestKey = action.label.endsWith('(A)') ? 'A' : 'B';
    const request = observation.request;
    if (!request || request.modelKey !== expectedRequestKey) {
      return { message: actionWhere + ' must carry the actual ' + expectedRequestKey + ' reconcile request', actionIndex };
    }
    const expectedWorkspace = expectedRequestKey === 'A' ? 'W1' : 'W2';
    const workspaceProblem = reconcileWorkspaceProblem(expectedWorkspace, request, actionWhere + '.observation.reconcile.request');
    if (workspaceProblem) return { message: workspaceProblem, actionIndex };
    const priorCall = calls.get(expectedRequestKey);
    if (priorCall && canonicalJson(priorCall) !== canonicalJson(request)) {
      return { message: actionWhere + ' request identity differs from the original ' + expectedRequestKey + ' request', actionIndex };
    }
    if (!priorCall) {
      if (requestIds.has(request.rawRequestId)) {
        return { message: actionWhere + ' raw requestId is shared by distinct reconciliation owners', actionIndex };
      }
      requestIds.add(request.rawRequestId);
      calls.set(expectedRequestKey, request);
    }

    const current = observation.current;
    const currentProblem = reconcileWorkspaceProblem(current.modelKey, current, actionWhere + '.observation.reconcile.current', false);
    if (currentProblem) return { message: currentProblem, actionIndex };
    const returned = observation.returned;
    if (actionIndex === 0) {
      if (returned !== null) return { message: actionWhere + ' cannot claim a response before it settles', actionIndex };
      continue;
    }
    const returnedKey = actionIndex === 1 ? 'B' : 'A';
    const expectedReceiptId = returnedKey === 'A' ? 'receipt-A' : 'receipt-B';
    const expectedReceiptModelKey = returnedKey === 'A' ? 'R1' : 'R2';
    if (!returned || returned.requestKey !== returnedKey || returned.receiptId !== expectedReceiptId ||
        returned.receiptModelKey !== expectedReceiptModelKey || returned.workspaceKey !== expectedWorkspaceFor(returnedKey) ||
        returned.outputId !== 'specimens' || returned.outputModelKey !== 'O1' ||
        !returned.outputIds.includes(returned.outputId)) {
      return { message: actionWhere + ' returned response does not match the actual ' + returnedKey + ' request/receipt/output identity', actionIndex };
    }
    const receipt = observation.receipt;
    if (actionIndex === 1) {
      if (!receipt || receipt.modelKey !== 'R2' || receipt.receiptId !== 'receipt-B' ||
          receipt.requestKey !== 'B' || receipt.workspaceKey !== 'W2' ||
          receipt.outputModelKey !== 'O1' || receipt.outputId !== 'specimens' || current.modelKey !== 'W2') {
        return { message: actionWhere + ' mounted success must display B receipt/output for current workspace W2', actionIndex };
      }
    } else if (!receipt || current.modelKey !== 'W2' ||
        action.observation.status !== trace.actions[actionIndex - 1].observation.status) {
      return { message: actionWhere + ' late A settlement must retain the mounted status and current workspace', actionIndex };
    }
  }
  if (calls.size !== 2) return { message: where + ' must bind two distinct actual reconcile request IDs' };
  return null;
}

function expectedWorkspaceFor(requestKey) {
  return requestKey === 'A' ? 'W1' : 'W2';
}

function reconcileObservationProblem(reconcile, where) {
  if (!isPlainObject(reconcile)) return where + ' must be an object for ReconcileLifecycle observations';
  const identity = (value, field, allowed = null) => {
    if (typeof value !== 'string' || value.length === 0) return where + '.' + field + ' must be a non-empty string';
    if (allowed && !allowed.includes(value)) return where + '.' + field + ' has an unsupported model key';
    return null;
  };
  const integer = (value, field) => Number.isInteger(value) && value > 0
    ? null : where + '.' + field + ' must be a positive integer';
  const workspace = (value, field) => identity(value, field, ['W1', 'W2']);
  const outputKey = (value, field) => identity(value, field, ['O1', 'O2']);
  const current = reconcile.current;
  if (!isPlainObject(current)) return where + '.current must be an object';
  let problem = workspace(current.modelKey, 'current.modelKey');
  if (problem) return problem;
  for (const [field, value] of [
    ['draftVersion', current.draftVersion],
    ['draftDigest', current.draftDigest],
    ['snapshotToken', current.snapshotToken],
    ['outputId', current.outputId],
    ['explorerId', current.explorerId],
  ]) {
    problem = field === 'draftVersion' ? integer(value, 'current.' + field) : identity(value, 'current.' + field);
    if (problem) return problem;
  }
  if (!reconcileOutputModelKey(current.outputId)) return where + '.current.outputId has no output model identity';
  const provenance = current.provenance;
  if (!isPlainObject(provenance) || provenance.draftIdentity !== 'rendered-dom' || provenance.outputId !== 'rendered-dom' ||
      provenance.snapshotToken !== 'matching-reconcile-request' || provenance.explorerId !== 'matching-reconcile-request') {
    return where + '.current.provenance must distinguish rendered DOM values from captured request identity';
  }
  if (reconcile.request !== null) {
    const request = reconcile.request;
    if (!isPlainObject(request)) return where + '.request must be an object or null';
    problem = identity(request.modelKey, 'request.modelKey', ['A', 'B']);
    if (problem) return problem;
    problem = identity(request.rawRequestId, 'request.rawRequestId');
    if (problem) return problem;
    problem = workspace(request.workspaceKey, 'request.workspaceKey');
    if (problem) return problem;
    if (request.workspaceKey !== expectedWorkspaceFor(request.modelKey)) {
      return where + '.request.workspaceKey does not match its request owner';
    }
    for (const field of ['draftVersion', 'draftDigest', 'snapshotToken', 'outputId', 'explorerId']) {
      problem = field === 'draftVersion' ? integer(request[field], 'request.' + field) : identity(request[field], 'request.' + field);
      if (problem) return problem;
    }
    if (!reconcileOutputModelKey(request.outputId)) return where + '.request.outputId has no output model identity';
  }
  if (reconcile.receipt !== null) {
    const receipt = reconcile.receipt;
    if (!isPlainObject(receipt)) return where + '.receipt must be an object or null';
    problem = identity(receipt.modelKey, 'receipt.modelKey', ['R1', 'R2']);
    if (problem) return problem;
    for (const field of ['receiptId', 'requestKey', 'workspaceKey', 'outputModelKey', 'outputId']) {
      const allowed = field === 'requestKey' ? ['A', 'B'] :
        field === 'workspaceKey' ? ['W1', 'W2'] :
          field === 'outputModelKey' ? ['O1', 'O2'] : null;
      problem = field === 'workspaceKey' ? workspace(receipt[field], 'receipt.' + field) :
        field === 'outputModelKey' ? outputKey(receipt[field], 'receipt.' + field) :
          identity(receipt[field], 'receipt.' + field, allowed);
      if (problem) return problem;
    }
    const expectedRequestKey = receipt.modelKey === 'R1' ? 'A' : 'B';
    const expectedReceiptId = expectedRequestKey === 'A' ? 'receipt-A' : 'receipt-B';
    if (receipt.requestKey !== expectedRequestKey || receipt.workspaceKey !== expectedWorkspaceFor(expectedRequestKey) ||
        receipt.receiptId !== expectedReceiptId || reconcileOutputModelKey(receipt.outputId) !== receipt.outputModelKey) {
      return where + '.receipt raw identity does not match its model receipt/request/output keys';
    }
  }
  if (reconcile.returned !== null) {
    const returned = reconcile.returned;
    if (!isPlainObject(returned)) return where + '.returned must be an object or null';
    for (const [field, allowed] of [
      ['requestKey', ['A', 'B']],
      ['receiptModelKey', ['R1', 'R2']],
      ['receiptId', null],
      ['workspaceKey', ['W1', 'W2']],
      ['outputModelKey', ['O1', 'O2']],
      ['outputId', null],
    ]) {
      problem = field === 'workspaceKey' ? workspace(returned[field], 'returned.' + field) :
        field === 'outputModelKey' ? outputKey(returned[field], 'returned.' + field) :
          identity(returned[field], 'returned.' + field, allowed);
      if (problem) return problem;
    }
    if (!Array.isArray(returned.outputIds) || returned.outputIds.length === 0 ||
        returned.outputIds.some((outputId) => typeof outputId !== 'string' || outputId.length === 0)) {
      return where + '.returned.outputIds must contain non-empty output IDs';
    }
    const expectedReceiptKey = returned.requestKey === 'A' ? 'R1' : 'R2';
    const expectedReceiptId = returned.requestKey === 'A' ? 'receipt-A' : 'receipt-B';
    if (returned.receiptModelKey !== expectedReceiptKey || returned.receiptId !== expectedReceiptId ||
        returned.workspaceKey !== expectedWorkspaceFor(returned.requestKey) ||
        reconcileOutputModelKey(returned.outputId) !== returned.outputModelKey) {
      return where + '.returned raw identity does not match its request/receipt/output model keys';
    }
  }
  return null;
}

function cdaRowExpansionLifecycleActionIndexes(trace) {
  const actions = Array.isArray(trace.actions) ? trace.actions : [];
  return new Set(actions
    .map((action, index) => ({ action, index }))
    .filter(({ action }) => action?.rowExpansionLifecycle !== undefined || action?.label === 'ExpandRows')
    .map(({ index }) => index));
}

function traceShapeProblem(trace, index, contract, events) {
  const where = 'traces[' + index + ']';
  if (!isPlainObject(trace)) return { message: where + ' must be an object' };
  if (typeof trace.name !== 'string' || trace.name.length === 0) return { message: where + '.name must be a non-empty string' };
  if (typeof trace.model !== 'string' || !contract.models[trace.model]) {
    return { message: where + '.model "' + String(trace.model) + '" is not defined by the contract' };
  }
  const journeyCase = contract.journeyCase;
  if (journeyCase && (trace.name !== journeyCase.traceName || trace.model !== journeyCase.model)) {
    return { message: where + ' must match the exact static BuilderJourney case ' + journeyCase.traceName };
  }
  const actions = new Set(contract.models[trace.model].actions);
  const hasCdaRelatedCase = Boolean(builderJourneyCaseForTrace(contract, trace));
  const cdaRowLifecycleIndexes = hasCdaRelatedCase ? cdaRowExpansionLifecycleActionIndexes(trace) : new Set();
  const hasGroupLifecycleContract = trace.model === 'BuilderJourney' && (
    (groupLifecycleEnabled(contract) && BUILDER_GROUP_TRACE_NAMES.has(trace.name)) || hasCdaRelatedCase);
  if (!Array.isArray(trace.actions)) return { message: where + '.actions must be an array' };
  if (trace.unmodeledEvents !== undefined) {
    if (!Array.isArray(trace.unmodeledEvents)) {
      return { message: where + '.unmodeledEvents must be an array' };
    }
    for (let eventIndex = 0; eventIndex < trace.unmodeledEvents.length; eventIndex += 1) {
      const event = trace.unmodeledEvents[eventIndex];
      const eventWhere = where + '.unmodeledEvents[' + eventIndex + ']';
      if (!isPlainObject(event) || typeof event.kind !== 'string' || typeof event.note !== 'string' ||
          (event.classification !== 'unsupported' && !(hasCdaRelatedCase && event.classification === 'read-only-support')) ||
          typeof event.reason !== 'string' || event.reason.trim().length === 0) {
        return { message: eventWhere + ' must include kind, note, classification "unsupported", and a non-empty reason' };
      }
    }
  }
  const initialProblem = observationProblem(trace.initialObservation, where + '.initialObservation', trace.model);
  if (initialProblem) return { message: initialProblem };
  if (trace.model === 'ReconcileLifecycle') {
    const action = trace.initialAction;
    if (!isPlainObject(action) || action.label !== 'RequestReconcile(A)' || action.event !== 'requestA') {
      return { message: where + '.initialAction must record the captured RequestReconcile(A) event' };
    }
    const binding = contract.actionBindings[action.label];
    if (!binding || binding.event !== action.event) {
      return { message: where + '.initialAction label/event does not match the selected trace contract' };
    }
    if (action.note !== undefined && typeof action.note !== 'string') {
      return { message: where + '.initialAction.note must be a string when present' };
    }
  } else if (trace.initialAction !== undefined) {
    return { message: where + '.initialAction is supported only for the mounted ReconcileLifecycle trace' };
  }
  if (trace.initialPreview !== undefined) {
    if (!isPlainObject(trace.initialPreview)) return { message: where + '.initialPreview must be an object' };
    const ownerProblemText = ownerProblem(trace.initialPreview.owner, where + '.initialPreview.owner', 'preview');
    if (ownerProblemText) return { message: ownerProblemText };
    const requestProblemText = requestProblem(trace.initialPreview.request, where + '.initialPreview.request');
    if (requestProblemText) return { message: requestProblemText };
    const responseProblemText = responseProblem(trace.initialPreview.response, where + '.initialPreview.response');
    if (responseProblemText) return { message: responseProblemText };
    const settlementProblemText = settlementProblem(trace.initialPreview.settlement, where + '.initialPreview.settlement');
    if (settlementProblemText) return { message: settlementProblemText };
    const sourcePreview = trace.initialObservation.sourceTablePreview;
    const responsePreview = trace.initialPreview.response.preview;
    if (trace.initialObservation.tablePreviewStatus === 'ready') {
      if (!isPlainObject(sourcePreview) || !isPlainObject(responsePreview)) {
        return { message: where + '.initialPreview must retain both response preview and rendered sourceTablePreview evidence' };
      }
      for (const [domField, responseField] of [['receiptId', 'receiptId'], ['outputId', 'outputId']]) {
        const domValue = sourcePreview[domField];
        const responseValue = responsePreview[responseField];
        if (typeof domValue !== 'string' || domValue.length === 0 ||
            typeof responseValue !== 'string' || responseValue.length === 0 || domValue !== responseValue) {
          return { message: where + '.initialPreview response ' + responseField + ' must match rendered sourceTablePreview ' + domField };
        }
      }
      if (sourcePreview.rowsPresent !== true || responsePreview.rowsPresent !== true) {
        return { message: where + '.initialPreview ready evidence requires rendered source and response rows' };
      }
    }
    const identityProblem = initialPreviewIdentityProblem(
      trace.initialPreview, trace, where + '.initialPreview', hasCdaRelatedCase,
    );
    if (identityProblem) return { message: identityProblem };
  }
  if (trace.model === 'BuilderJourney' && trace.initialObservation.tablePreviewStatus === 'ready' && !trace.initialPreview) {
    return { message: where + '.initialPreview is required for an observed ready source table preview' };
  }
  for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
    const action = trace.actions[actionIndex];
    const actionWhere = where + '.actions[' + actionIndex + ']';
    if (!isPlainObject(action)) return { message: actionWhere + ' must be an object', actionIndex };
    if (typeof action.label !== 'string' || action.label.length === 0) {
      return { message: actionWhere + '.label must be a non-empty string', actionIndex };
    }
    if (typeof action.event !== 'string' || action.event.length === 0) {
      return { message: actionWhere + '.event must be a non-empty string', actionIndex };
    }
    const normalizedLabel = normalizedActionLabel(trace.model, action.label, actions);
    if (!normalizedLabel) {
      return { message: actionWhere + '.label "' + action.label + '" is not an action of ' + trace.model, actionIndex };
    }
    if (!events.has(action.event)) {
      return { message: actionWhere + '.event "' + action.event + '" is not defined by the contract', actionIndex };
    }
    const binding = contract.actionBindings[normalizedLabel];
    if (!binding) {
      return { message: actionWhere + '.label "' + action.label + '" has no action binding in the selected contract', actionIndex };
    }
    if (binding.event !== action.event) {
      return {
        message: actionWhere + '.event "' + action.event + '" does not match contract event "' + binding.event + '" for ' + action.label,
        actionIndex,
      };
    }
    const expectedKind = expectedOwnerKind(trace.model, action.label);
    if (action.owner === undefined && requiredOwnerForAction(trace.model, action.label)) {
      return { message: actionWhere + '.owner is required for ' + action.label, actionIndex };
    }
    if (action.owner === null) {
      if (trace.model !== 'ConstructionLifecycle' || action.label !== 'BeginApply(rejected)') {
        return { message: actionWhere + '.owner may be null only for an ownerless rejected BeginApply', actionIndex };
      }
    } else if (action.owner !== undefined) {
      const expectedAttempt = trace.model === 'ConstructionLifecycle'
        ? action.label.match(/\((P[12])\)$/)?.[1] ??
          (action.label === 'ReplaceProposalOwner' || action.label === 'RetryProposal(P1-to-P2)' ? 'P2' : null)
        : trace.model === 'BuilderPreview' && action.label === 'StartProposal'
          ? 'P1'
          : trace.model === 'BuilderPreview' && action.label === 'ReplaceProposal'
            ? 'P2'
            : null;
      const ownerProblemText = ownerProblem(action.owner, actionWhere + '.owner', expectedKind, expectedAttempt);
      if (ownerProblemText) return { message: ownerProblemText, actionIndex };
    }
    if (action.expectedProposal !== undefined) {
      if (!['P1', 'P2'].includes(action.expectedProposal)) {
        return { message: actionWhere + '.expectedProposal must be P1 or P2 when present', actionIndex };
      }
      if (!/^BeginApply(?:\(|$)/.test(action.label)) {
        return { message: actionWhere + '.expectedProposal is only valid on BeginApply actions', actionIndex };
      }
    }
    if (action.request !== undefined) {
      const problem = requestProblem(action.request, actionWhere + '.request');
      if (problem) return { message: problem, actionIndex };
    }
    if (action.response !== undefined && action.response !== null) {
      const problem = responseProblem(action.response, actionWhere + '.response');
      if (problem) return { message: problem, actionIndex };
    }
    if (action.settlement !== undefined) {
      const problem = settlementProblem(action.settlement, actionWhere + '.settlement');
      if (problem) return { message: problem, actionIndex };
    }
    if (trace.model === 'BuilderJourney') {
      if (action.owner !== undefined && action.owner === null) return { message: actionWhere + '.owner cannot be null for a BuilderJourney action', actionIndex };
      if (action.owner !== undefined && !requiredOwnerForAction(trace.model, action.label)) {
        return { message: actionWhere + '.owner is not valid for a local BuilderJourney event', actionIndex };
      }
      const localLifecycleOwner = hasCdaRelatedCase && action.label === 'RelatedSourceCancel';
      const previewSettlement = ['PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled']
        .includes(action.label) || (hasCdaRelatedCase && action.label === 'RelatedSourcePreview');
      const choiceSettlement = ['ChoicePreviewComplete', 'ChoicePreviewError'].includes(action.label);
      const requestStart = ['Preview', 'ChoicePreview'].includes(action.label);
      if (action.owner && action.request === undefined && !localLifecycleOwner) return { message: actionWhere + '.request is required for an owned BuilderJourney action', actionIndex };
      if (!requestStart && !localLifecycleOwner && action.owner && action.response === undefined) {
        return { message: actionWhere + '.response is required for a BuilderJourney settlement action', actionIndex };
      }
      if (!requestStart && !localLifecycleOwner && action.owner && action.settlement === undefined) {
        return { message: actionWhere + '.settlement is required for a BuilderJourney settlement action', actionIndex };
      }
      if (action.owner && !localLifecycleOwner) {
        const requestKey = observedRequestKey(action.request);
        if (!requestKey) return { message: actionWhere + '.request must preserve its captureId or browserRequestId', actionIndex };
        if (!hasCdaRelatedCase && (typeof action.request?.captureId !== 'string' || action.request.captureId.length === 0)) {
          return { message: actionWhere + '.request.captureId is required for an owned BuilderJourney action', actionIndex };
        }
        if (!requestStart && observedRequestKey(action.settlement) !== requestKey) {
          return { message: actionWhere + ' request identity does not match settlement identity', actionIndex };
        }
        if (!requestStart && action.response !== null && action.response !== undefined) {
          for (const field of ['captureId', 'browserRequestId', 'requestId']) {
            if (action.request[field] != null && action.response[field] != null && action.request[field] !== action.response[field]) {
              return { message: actionWhere + ' request and response ' + field + ' values do not match', actionIndex };
            }
          }
        }
      }
      if (action.owner && !requestStart && !localLifecycleOwner && action.response === null &&
          (!['PreviewError', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled', 'ChoicePreviewError'].includes(action.label) || action.settlement?.kind !== 'request-failed')) {
        return { message: actionWhere + ' response:null is allowed only for a request-failed preview, cancellation, or choice-preview error settlement', actionIndex };
      }
      if (action.owner && !requestStart && !localLifecycleOwner && action.response !== null && action.settlement?.kind !== 'http-response') {
        return { message: actionWhere + ' HTTP response requires settlement.kind http-response', actionIndex };
      }
      if (action.owner && !requestStart && !localLifecycleOwner && action.response === null && action.settlement?.kind !== 'request-failed') {
        return { message: actionWhere + ' response:null requires settlement.kind request-failed', actionIndex };
      }
      if (previewSettlement || choiceSettlement) {
        const requestKey = observedRequestKey(action.request);
        if (!requestKey || observedRequestKey(action.settlement) !== requestKey) {
          return { message: actionWhere + ' request/response settlement must preserve the same captureId or browserRequestId', actionIndex };
        }
        if (!hasCdaRelatedCase && (typeof action.request?.captureId !== 'string' || action.request.captureId.length === 0 ||
            action.settlement?.captureId !== action.request.captureId ||
            (action.response !== null && action.response?.captureId !== action.request.captureId))) {
          return { message: actionWhere + ' request/response captureId must be present and identical', actionIndex };
        }
      }
      if (['Preview', 'PreviewComplete', 'PreviewError'].includes(action.label) &&
          action.request?.path === 'construction-choice-proposals') {
        return { message: actionWhere + ' construction-choice-proposals must use ChoicePreview actions, not proposal-panel Preview actions', actionIndex };
      }
      if (['ChoicePreview', 'ChoicePreviewComplete', 'ChoicePreviewError'].includes(action.label) &&
          action.request?.path !== 'construction-choice-proposals') {
        return { message: actionWhere + ' ChoicePreview actions require path construction-choice-proposals', actionIndex };
      }
      if (action.label === 'PreviewRequestCancelled') {
        const previous = trace.actions[actionIndex - 1];
        const expectedProposalPath = hasCdaRelatedCase ? '/construction-proposals' : 'construction-proposals';
        const sameRequest = previous?.request && action.request &&
          previous.request.method === 'POST' &&
          previous.request.method === action.request.method &&
          previous.request.path === action.request.path &&
          previous.request.captureId === action.request.captureId;
        if (!previous || previous.label !== 'Preview' ||
            canonicalJson(previous.owner) !== canonicalJson(action.owner) || !sameRequest ||
          action.request?.path !== expectedProposalPath) {
          return {
            message: actionWhere + ' PreviewRequestCancelled must match the immediately preceding Preview owner and request capture',
            actionIndex,
          };
        }
        if (action.response !== null || action.settlement?.kind !== 'request-failed' ||
            action.settlement?.errorText !== 'net::ERR_ABORTED') {
          return {
            message: actionWhere + ' PreviewRequestCancelled requires response:null and settlement.errorText net::ERR_ABORTED',
            actionIndex,
          };
        }
      }
      if (action.label === 'AddColumn' && action.request?.path !== 'commands') {
        return { message: actionWhere + ' AddColumn requires the actual commands request', actionIndex };
      }
      if (hasCdaRelatedCase && action.label === 'RelatedSourcePreview' &&
          action.request?.path !== '/construction-proposals') {
        return { message: actionWhere + ' RelatedSourcePreview requires the actual construction-proposals request', actionIndex };
      }
      if (hasCdaRelatedCase && ['ExpandRows', 'RelatedSourceApply'].includes(action.label) &&
          action.request?.path !== '/commands') {
        return { message: actionWhere + ' apply action requires the actual commands request', actionIndex };
      }
    }
    const absentCdaObservationAllowed = hasCdaRelatedCase && action.observation == null &&
      cdaEventTimeObservationUnavailable(action, action.groupLifecycle ?? action.rowExpansionLifecycle ?? action.relatedSourceLifecycle);
    if (hasCdaRelatedCase && action.observation == null && !absentCdaObservationAllowed) {
      return { message: actionWhere + '.observation may be absent only for an explicitly unavailable event-time observation not consumed by its Cda operator', actionIndex };
    }
    const observation = absentCdaObservationAllowed
      ? null
      : observationProblem(action.observation, actionWhere + '.observation', trace.model);
    if (observation) return { message: observation, actionIndex };
    if (trace.model === 'BuilderJourney') {
      const evidenceProblem = journeyPreviewEvidenceProblem(action, actionWhere);
      if (evidenceProblem) return { message: evidenceProblem, actionIndex };
      if (hasCdaRelatedCase && action.label === 'PreviewComplete' && action.observation?.status === 'ready') {
        const panel = action.renderedObservation?.proposalPanel;
        const preview = action.response?.preview;
        const eventSnapshot = action.eventTimeSnapshot;
        const renderedMount = action.renderedObservationMount;
        const owner = action.groupLifecycle?.owner ?? action.rowExpansionLifecycle?.owner;
        const panelObservedAt = action.renderedObservation?.proposalPanelObservedAt;
        const panelBrowserRequestId = action.renderedObservation?.proposalPanelBrowserRequestId;
        if (!isPlainObject(panel) || panel.status !== 'ready' || panel.id !== action.response?.proposalId ||
            !Array.isArray(panel.rows) || !isPlainObject(preview) || preview.rowsPresent !== true ||
            !Number.isInteger(preview.rowCount) || panel.rows.length !== preview.rowCount ||
            panel.rows.some((row) => !Array.isArray(row) || row.length === 0 || row.some((cell) =>
              !isPlainObject(cell) || typeof cell.text !== 'string' ||
              (cell.title !== null && typeof cell.title !== 'string'))) ||
            eventSnapshot?.available !== true || !Number.isFinite(panelObservedAt) ||
            panelObservedAt < action.settlement?.completedAt ||
            panelBrowserRequestId !== action.request?.browserRequestId ||
            !isPlainObject(renderedMount) || renderedMount.mountEpoch !== action.observedEpoch ||
            renderedMount.documentTimeOrigin !== eventSnapshot.observationMount?.documentTimeOrigin ||
            renderedMount.documentTimeOrigin !== eventSnapshot.responseMount?.documentTimeOrigin ||
            !Number.isInteger(owner?.mountEpoch) || owner.mountEpoch !== action.observedEpoch) {
          return { message: actionWhere + ' PreviewComplete must retain its actual ready proposal panel and rendered rows', actionIndex };
        }
      }
      if (hasCdaRelatedCase && ['LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored'].includes(action.label)) {
        const lateProblem = cdaLatePreviewObservationProblem(trace, action, actionIndex);
        if (lateProblem) return { message: actionWhere + ' ' + lateProblem, actionIndex };
      }
      const isCdaRowExpansionAction = hasCdaRelatedCase && cdaRowLifecycleIndexes.has(actionIndex);
      if (hasCdaRelatedCase) {
        if (BUILDER_GROUP_PREVIEW_LABELS.has(action.label) &&
            (!Number.isInteger(action.observedEpoch) || action.observedEpoch < 0 ||
             action.observedEpoch > journeyCase.maxMountEpoch)) {
          return { message: actionWhere + '.observedEpoch must preserve the actual current document epoch for each proposal request and settlement', actionIndex };
        }
        if (isCdaRowExpansionAction) {
          if (action.groupLifecycle !== undefined) {
            return { message: actionWhere + ' row expansion proposal cannot claim a Group candidate lifecycle', actionIndex };
          }
          const rowProblem = rowExpansionLifecycleShapeProblem(action, actionWhere, true, journeyCase);
          if (rowProblem) return { message: rowProblem, actionIndex };
        } else if (action.rowExpansionLifecycle !== undefined) {
          return { message: actionWhere + '.rowExpansionLifecycle is only valid on the two row previews and ExpandRows actions', actionIndex };
        }
        if (['RelatedSourcePreview', 'RelatedSourceApply', 'RelatedSourceCancel'].includes(action.label)) {
          const relatedProblem = relatedSourceLifecycleShapeProblem(action, actionWhere, true, journeyCase);
          if (relatedProblem) return { message: relatedProblem, actionIndex };
        } else if (action.relatedSourceLifecycle !== undefined) {
          return { message: actionWhere + '.relatedSourceLifecycle is only valid on related-source lifecycle actions', actionIndex };
        }
      }
      const groupLifecycleProblem = groupLifecycleShapeProblem(
        action,
        actionWhere,
        hasGroupLifecycleContract && BUILDER_GROUP_LIFECYCLE_LABELS.has(action.label) && !isCdaRowExpansionAction,
        journeyCase?.maxMountEpoch ?? 2,
        hasCdaRelatedCase,
        hasCdaRelatedCase ? ['COUNT_ROWS', 'COUNT_DISTINCT'] : ['COUNT_ROWS'],
        !hasCdaRelatedCase,
        hasCdaRelatedCase,
      );
      if (groupLifecycleProblem) return { message: groupLifecycleProblem, actionIndex };
      if (hasCdaRelatedCase && action.groupLifecycle !== undefined) {
        const cdaGroupProblem = cdaGroupLifecycleSnapshotProblem(action, actionWhere, journeyCase);
        if (cdaGroupProblem) return { message: cdaGroupProblem, actionIndex };
      }
    }
  }
  if (hasCdaRelatedCase) {
    const expectedExactCounts = contract.phase1.requiredActionCountsByTrace[journeyCase.traceName];
    const expectedMinimumCounts = contract.phase1.requiredActionMinimumsByTrace[journeyCase.traceName];
    const actualCounts = new Map();
    for (const action of trace.actions) actualCounts.set(action.label, (actualCounts.get(action.label) ?? 0) + 1);
    for (const [label, expected] of Object.entries(expectedExactCounts)) {
      const actual = actualCounts.get(label) ?? 0;
      if (actual !== expected) {
        return { message: 'trace "' + trace.name + '" action ' + label + ' count expected ' + expected + ', found ' + actual };
      }
    }
    for (const [label, minimum] of Object.entries(expectedMinimumCounts)) {
      const actual = actualCounts.get(label) ?? 0;
      if (actual < minimum) {
        return { message: 'trace "' + trace.name + '" action ' + label + ' minimum expected ' + minimum + ', found ' + actual };
      }
    }
  }
  if (trace.model === 'ReconcileLifecycle') {
    const problem = reconcileIdentityProblem(trace, where);
    if (problem) return problem;
  }
  if (trace.model === 'BuilderJourney') {
    const groupLifecycleConsistencyProblem = journeyGroupLifecycleConsistencyProblem(
      trace,
      where,
      hasGroupLifecycleContract,
      {
        maxMountEpoch: journeyCase?.maxMountEpoch ?? 2,
        allowBrowserRequestId: hasCdaRelatedCase,
        allowedAggregates: hasCdaRelatedCase ? ['COUNT_ROWS', 'COUNT_DISTINCT'] : ['COUNT_ROWS'],
        cdaJourneyCase: hasCdaRelatedCase,
      },
    );
    if (groupLifecycleConsistencyProblem) return groupLifecycleConsistencyProblem;
    if (hasCdaRelatedCase) {
      const cdaLifecycleProblem = cdaJourneyLifecycleConsistencyProblem(trace, where, journeyCase, contract);
      if (cdaLifecycleProblem) return cdaLifecycleProblem;
    }
    const choiceProblem = journeyChoiceTraceProblem(trace, where);
    if (choiceProblem) return choiceProblem;
    const previewStartByCapture = new Map();
    const pairedPreviewCaptures = new Set();
    const previewSettlementLabels = new Set([
      'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
    ]);
    for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
      const action = trace.actions[actionIndex];
      const actionWhere = where + '.actions[' + actionIndex + ']';
      if (action.label === 'Preview') {
        const captureId = observedRequestKey(action.request);
        if (!captureId) return { message: actionWhere + '.request must preserve a captureId or browserRequestId', actionIndex };
        if (previewStartByCapture.has(captureId)) {
          return { message: actionWhere + '.request identity duplicates another Preview request', actionIndex };
        }
        previewStartByCapture.set(captureId, { action, actionIndex });
      }
      if (!previewSettlementLabels.has(action.label)) continue;
      const captureId = observedRequestKey(action.request);
      const start = previewStartByCapture.get(captureId);
      if (!start) {
        return { message: actionWhere + ' has no Preview request with the same captureId or browserRequestId', actionIndex };
      }
      if (pairedPreviewCaptures.has(captureId)) {
        return { message: actionWhere + ' duplicates a preview settlement for the same captureId', actionIndex };
      }
      const request = start.action.request;
      const settlementRequest = action.request;
      if (canonicalJson(start.action.owner) !== canonicalJson(action.owner) ||
          request.method !== settlementRequest.method || request.path !== settlementRequest.path ||
          observedRequestKey(request) !== captureId || observedRequestKey(settlementRequest) !== captureId) {
        return { message: actionWhere + ' preview settlement must match its Preview owner, method, path, and request identity', actionIndex };
      }
      for (const field of ['captureId', 'browserRequestId', 'requestId']) {
        const identities = [request[field], settlementRequest[field], action.response?.[field]]
          .filter((value) => value !== null && value !== undefined);
        if (identities.some((value) => value !== identities[0])) {
          return { message: actionWhere + ' preview settlement has mismatched non-null ' + field + ' values', actionIndex };
        }
      }
      pairedPreviewCaptures.add(captureId);
    }
    for (const [captureId, start] of previewStartByCapture) {
      if (!pairedPreviewCaptures.has(captureId)) {
        return {
          message: where + '.actions[' + start.actionIndex + '] Preview request has no matching settlement with the same captureId',
          actionIndex: start.actionIndex,
        };
      }
    }
    const requestCaptureIds = new Set();
    const registerCapture = (captureId, location) => {
      if (typeof captureId !== 'string' || captureId.length === 0) return null;
      if (requestCaptureIds.has(captureId)) return location + ' duplicates another native request identity';
      requestCaptureIds.add(captureId);
      return null;
    };
    if (trace.initialPreview) {
      const problem = registerCapture(observedRequestKey(trace.initialPreview.request), where + '.initialPreview.request');
      if (problem) return { message: problem };
    }
    for (let actionIndex = 0; actionIndex < trace.actions.length; actionIndex += 1) {
      const action = trace.actions[actionIndex];
      if (!action.owner || !action.request) continue;
      if ([
        'PreviewComplete', 'PreviewError', 'LatePreviewCompleteIgnored', 'LatePreviewErrorIgnored', 'PreviewRequestCancelled',
        'ChoicePreviewComplete', 'ChoicePreviewError', 'RelatedSourceCancel',
      ].includes(action.label)) continue;
      const problem = registerCapture(observedRequestKey(action.request), where + '.actions[' + actionIndex + '].request');
      if (problem) return { message: problem, actionIndex };
    }
  }
  return null;
}

function traceKey(trace) {
  return canonicalJson({
    model: trace.model,
    initialAction: trace.initialAction ?? null,
    initialObservation: trace.initialObservation,
    initialPreview: trace.initialPreview ?? null,
    actions: trace.actions.map((action) => ({
      label: action.label,
      event: action.event,
      observation: action.observation,
      owner: action.owner ?? null,
      expectedProposal: action.expectedProposal ?? null,
      request: action.request ?? null,
      response: action.response ?? null,
      settlement: action.settlement ?? null,
      groupLifecycle: action.groupLifecycle ?? null,
      rowExpansionLifecycle: action.rowExpansionLifecycle ?? null,
      relatedSourceLifecycle: action.relatedSourceLifecycle ?? null,
    })),
  });
}

function isFindingTrace(trace) {
  if (trace.expectedFailure === true) return true;
  if (trace.kind === 'finding' || trace.kind === 'expected-failure') return true;
  return /\bfinding\b|^finding:/i.test(trace.name);
}

function tlaString(value) {
  return JSON.stringify(value);
}

function tlaBool(value) {
  return value ? 'TRUE' : 'FALSE';
}

function tlaOptionalBoolean(value) {
  if (value === true) return '"true"';
  if (value === false) return '"false"';
  return '"unknown"';
}

function tlaObservedValue(value) {
  if (value === undefined || value === null) return '"NoObservedValue"';
  if (typeof value === 'string') return tlaString(value);
  if (typeof value === 'number') return String(value);
  if (typeof value === 'boolean') return tlaBool(value);
  return '"NoObservedValue"';
}

function nestedObservedValue(observation, objectField, field) {
  const nested = observation[objectField];
  return isPlainObject(nested) ? nested[field] : undefined;
}

function reconcileObservedValue(observation, objectField, field) {
  const reconcile = observation.reconcile;
  const nested = isPlainObject(reconcile) ? reconcile[objectField] : undefined;
  return isPlainObject(nested) ? nested[field] : undefined;
}

function reconcileOutputKey(observation, objectField) {
  const outputId = reconcileObservedValue(observation, objectField, 'outputId');
  return reconcileOutputModelKey(outputId);
}

function reconcileModelValue(observation, objectField, field, mapper) {
  const value = reconcileObservedValue(observation, objectField, field);
  return typeof value === 'string' ? mapper(value) : undefined;
}

function tlaObservation(observation) {
  return '[status |-> ' + tlaObservedValue(observation.status) +
    ', tablePreviewStatus |-> ' + tlaObservedValue(observation.tablePreviewStatus) +
    ', proposalId |-> ' + tlaObservedValue(observation.proposalId) +
    ', proposalOutputId |-> ' + tlaObservedValue(observation.proposalOutputId) +
    ', proposalSnapshotToken |-> ' + tlaObservedValue(observation.proposalSnapshotToken) +
    ', proposalDraftVersion |-> ' + tlaObservedValue(observation.proposalDraftVersion) +
    ', proposalDraftDigest |-> ' + tlaObservedValue(observation.proposalDraftDigest) +
    ', previewReceiptId |-> ' + tlaObservedValue(observation.previewReceiptId) +
    ', previewOutputId |-> ' + tlaObservedValue(observation.previewOutputId) +
    ', sourcePreviewStatus |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'status')) +
    ', sourcePreviewReceiptId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'receiptId')) +
    ', sourcePreviewOutputId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'outputId')) +
    ', sourcePreviewProposalId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'proposalId')) +
    ', sourcePreviewDraftVersion |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'draftVersion')) +
    ', sourcePreviewDraftDigest |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'draftDigest')) +
    ', sourcePreviewRowsPresent |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'sourceTablePreview', 'rowsPresent')) +
    ', sourcePreviewRowCount |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'rowCount')) +
    ', sourcePreviewRenderedRowCount |-> ' + tlaObservedValue(nestedObservedValue(observation, 'sourceTablePreview', 'renderedRowCount')) +
    ', embeddedPreviewStatus |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'status')) +
    ', embeddedPreviewReceiptId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'receiptId')) +
    ', embeddedPreviewOutputId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'outputId')) +
    ', embeddedPreviewProposalId |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'proposalId')) +
    ', embeddedPreviewDraftVersion |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'draftVersion')) +
    ', embeddedPreviewDraftDigest |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'draftDigest')) +
    ', embeddedPreviewRowsPresent |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'embeddedProposalPreview', 'rowsPresent')) +
    ', embeddedPreviewRowCount |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'rowCount')) +
    ', embeddedPreviewRenderedRowCount |-> ' + tlaObservedValue(nestedObservedValue(observation, 'embeddedProposalPreview', 'renderedRowCount')) +
    ', applyAvailable |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'apply', 'available')) +
    ', applyEnabled |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'apply', 'enabled')) +
    ', applying |-> ' + tlaOptionalBoolean(nestedObservedValue(observation, 'apply', 'applying')) +
    ', canApply |-> ' + tlaBool(observation.canApply) +
    ', proposalIdPresent |-> ' + tlaBool(observation.proposalIdPresent) +
    ', previewReceiptPresent |-> ' + tlaBool(observation.previewReceiptPresent) +
    ', rowsPresent |-> ' + tlaBool(observation.rowsPresent) +
    ', receiptPresent |-> ' + tlaBool(observation.receiptPresent ?? Boolean(observation.reconcile?.receipt)) +
    ', reconcileCurrentKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'current', 'modelKey')) +
    ', reconcileCurrentDraftVersion |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'current', 'draftVersion')) +
    ', reconcileCurrentDraftDigest |-> ' + tlaObservedValue(reconcileModelValue(observation, 'current', 'draftDigest', reconcileDigestModelKey)) +
    ', reconcileCurrentSnapshotToken |-> ' + tlaObservedValue(reconcileModelValue(observation, 'current', 'snapshotToken', reconcileSnapshotModelKey)) +
    ', reconcileCurrentOutputModelKey |-> ' + tlaObservedValue(reconcileOutputKey(observation, 'current')) +
    ', reconcileCurrentOutputId |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'current', 'outputId')) +
    ', reconcileCurrentExplorerId |-> ' + tlaObservedValue(reconcileModelValue(observation, 'current', 'explorerId', reconcileExplorerModelKey)) +
    ', reconcileRequestModelKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'request', 'modelKey')) +
    ', reconcileRequestRawId |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'request', 'rawRequestId')) +
    ', reconcileRequestWorkspaceKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'request', 'workspaceKey')) +
    ', reconcileRequestDraftVersion |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'request', 'draftVersion')) +
    ', reconcileRequestDraftDigest |-> ' + tlaObservedValue(reconcileModelValue(observation, 'request', 'draftDigest', reconcileDigestModelKey)) +
    ', reconcileRequestSnapshotToken |-> ' + tlaObservedValue(reconcileModelValue(observation, 'request', 'snapshotToken', reconcileSnapshotModelKey)) +
    ', reconcileRequestOutputModelKey |-> ' + tlaObservedValue(reconcileOutputKey(observation, 'request')) +
    ', reconcileRequestOutputId |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'request', 'outputId')) +
    ', reconcileRequestExplorerId |-> ' + tlaObservedValue(reconcileModelValue(observation, 'request', 'explorerId', reconcileExplorerModelKey)) +
    ', reconcileReceiptModelKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'receipt', 'modelKey')) +
    ', reconcileReceiptId |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'receipt', 'receiptId')) +
    ', reconcileReceiptRequestKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'receipt', 'requestKey')) +
    ', reconcileReceiptWorkspaceKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'receipt', 'workspaceKey')) +
    ', reconcileReceiptOutputModelKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'receipt', 'outputModelKey')) +
    ', reconcileReceiptOutputId |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'receipt', 'outputId')) +
    ', reconcileReturnedRequestKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'returned', 'requestKey')) +
    ', reconcileReturnedReceiptModelKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'returned', 'receiptModelKey')) +
    ', reconcileReturnedReceiptId |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'returned', 'receiptId')) +
    ', reconcileReturnedWorkspaceKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'returned', 'workspaceKey')) +
    ', reconcileReturnedOutputModelKey |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'returned', 'outputModelKey')) +
    ', reconcileReturnedOutputId |-> ' + tlaObservedValue(reconcileObservedValue(observation, 'returned', 'outputId')) +
    ', reconcileReturnedOutputIds |-> ' + tlaString(Buffer.from(canonicalJson(reconcileObservedValue(observation, 'returned', 'outputIds') ?? [])).toString('hex')) +
    ', selectedObservation |-> ' + tlaString(Buffer.from(canonicalJson(observation)).toString('hex')) + ']';
}

function tlaSequence(items) {
  return '<<' + items.map((item) => '  ' + item).join(',\n') + '>>';
}

function builderPreviewObservationMapping(prime) {
  return [
    '  /\\ (obs.status = "previewing" =>',
    '       ((proposalPhase' + prime + ' \\in {"pending", "ready"}) /\\',
    '        (previewPhase' + prime + ' \\in {"none", "pending"})))',
    '  /\\ (obs.status = "ready" =>',
    '       ((proposalPhase' + prime + ' = "ready") /\\ (previewPhase' + prime + ' = "ready")))',
    '  /\\ (obs.status = "error" =>',
    '       ((proposalPhase' + prime + ' = "error") \\/ (previewPhase' + prime + ' = "error")))',
    '  /\\ (obs.status = "needs-repair" =>',
    '       ((proposalPhase' + prime + ' = "error") \\/ (previewPhase' + prime + ' = "error")))',
    '  /\\ (obs.status = "applying" => (applyPhase' + prime + ' = "applying"))',
    '  /\\ (obs.canApply =>',
    '       ((proposalPhase' + prime + ' = "ready") /\\',
    '        (previewPhase' + prime + ' = "ready")))',
    '  /\\ (obs.canApply => (obs.previewReceiptId = obs.proposalId))',
    '  /\\ (obs.canApply => (obs.previewOutputId = obs.proposalOutputId))',
    '  /\\ (obs.previewReceiptPresent => (previewPhase' + prime + ' = "ready"))',
    '  /\\ (obs.rowsPresent => (previewPhase' + prime + ' = "ready"))',
  ].join('\n');
}


function builderJourneyObservationMapping(prime) {
  const preview = 'preview' + prime;
  const proposalIdPresent = 'proposalIdPresent' + prime;
  const tablePreview = 'tablePreview' + prime;
  const sourceRowsPresent = 'sourceRowsPresent' + prime;
  return [
    '  /\\ (obs.status = "previewing" => (' + preview + ' = "pending"))',
    '  /\\ (obs.status = "ready" => (' + preview + ' = "ready"))',
    '  /\\ (obs.status \\in {"error", "needs-repair"} => (' + preview + ' = "error"))',
    '  /\\ (obs.embeddedPreviewStatus = "ready" => (' + preview + ' = "ready"))',
    '  /\\ (obs.embeddedPreviewStatus = "error" => (' + preview + ' = "error"))',
    '  /\\ (obs.tablePreviewStatus = "ready" => (' + tablePreview + ' = "ready"))',
    '  /\\ (obs.tablePreviewStatus = "error" => (' + tablePreview + ' = "error"))',
    '  /\\ (obs.sourcePreviewRowsPresent = "true" => ' + sourceRowsPresent + ')',
    '  /\\ (obs.sourcePreviewRowsPresent = "false" => ~' + sourceRowsPresent + ')',
    '  /\\ obs.proposalIdPresent <=> ' + proposalIdPresent,
    '  /\\ obs.previewReceiptPresent <=> (' + preview + ' = "ready")',
    '  /\\ obs.receiptPresent <=> (' + preview + ' = "ready")',
    '  /\\ obs.rowsPresent <=> rowsPresent' + prime,
    '  /\\ obs.canApply <=> ((' + preview + ' = "ready") /\\ ' + proposalIdPresent + ' /\\ rowsPresent' + prime + ')',
    '  /\\ (obs.canApply => obs.previewReceiptId = obs.proposalId)',
    '  /\\ (obs.canApply => obs.previewOutputId = obs.proposalOutputId)',
    '  /\\ (obs.embeddedPreviewRowsPresent = "true" => rowsPresent' + prime + ')',
    '  /\\ (obs.embeddedPreviewRowsPresent = "false" => ~rowsPresent' + prime + ')',
    '  /\\ (obs.applyEnabled = "true" => obs.applyAvailable = "true")',
    '  /\\ (obs.applying = "true" => obs.applyAvailable = "true")',
  ].join('\n');
}


function reconcileLifecycleObservationMapping(prime) {
  const state = prime ? "r'" : 'r';
  const requestIds = prime ? "traceRequestIds'" : 'traceRequestIds';
  return [
    '  /\\ (obs.status = "empty" => (' + state + '.phase \\in {"idle", "requesting"}))',
    '  /\\ (obs.status = "ready" => (' + state + '.phase = "succeeded"))',
    '  /\\ obs.reconcileCurrentKey = ' + state + '.currentWorkspace',
    '  /\\ obs.reconcileCurrentDraftVersion = WorkspaceFor(' + state + '.currentWorkspace).draft',
    '  /\\ obs.reconcileCurrentDraftDigest = WorkspaceFor(' + state + '.currentWorkspace).digest',
    '  /\\ obs.reconcileCurrentSnapshotToken = WorkspaceFor(' + state + '.currentWorkspace).snapshot',
    '  /\\ obs.reconcileCurrentOutputModelKey = WorkspaceFor(' + state + '.currentWorkspace).outputKey',
    '  /\\ obs.reconcileCurrentExplorerId = WorkspaceFor(' + state + '.currentWorkspace).explorer',
    '  /\\ (obs.reconcileRequestModelKey = "NoObservedValue" =>',
    '       /\\ obs.reconcileRequestRawId = "NoObservedValue"',
    '       /\\ obs.reconcileRequestWorkspaceKey = "NoObservedValue")',
    ...['A', 'B'].flatMap((key) => [
      '  /\\ (obs.reconcileRequestModelKey = "' + key + '" =>',
      '       /\\ ' + state + '.requestLedger["' + key + '"].workspace = obs.reconcileRequestWorkspaceKey',
      '       /\\ obs.reconcileRequestRawId = ' + requestIds + '["' + key + '"]',
      '       /\\ obs.reconcileRequestDraftVersion = WorkspaceFor(' + state + '.requestLedger["' + key + '"].workspace).draft',
      '       /\\ obs.reconcileRequestDraftDigest = WorkspaceFor(' + state + '.requestLedger["' + key + '"].workspace).digest',
      '       /\\ obs.reconcileRequestSnapshotToken = WorkspaceFor(' + state + '.requestLedger["' + key + '"].workspace).snapshot',
      '       /\\ obs.reconcileRequestOutputModelKey = WorkspaceFor(' + state + '.requestLedger["' + key + '"].workspace).outputKey',
      '       /\\ obs.reconcileRequestExplorerId = WorkspaceFor(' + state + '.requestLedger["' + key + '"].workspace).explorer)',
    ]),
    '  /\\ obs.reconcileReceiptModelKey = IF ' + state + '.receipt.id = "NONE" THEN "NoObservedValue" ELSE ' + state + '.receipt.id',
    '  /\\ obs.reconcileReceiptRequestKey = IF ' + state + '.receipt.id = "NONE" THEN "NoObservedValue" ELSE ' + state + '.receipt.requestKey',
    '  /\\ obs.reconcileReceiptWorkspaceKey = IF ' + state + '.receipt.id = "NONE" THEN "NoObservedValue" ELSE ' + state + '.receipt.workspace',
    '  /\\ obs.reconcileReceiptOutputModelKey = IF ' + state + '.receipt.id = "NONE" THEN "NoObservedValue" ELSE ' + state + '.receipt.outputKey',
    '  /\\ obs.reconcileReturnedRequestKey = IF ' + state + '.lastReturned.requestKey = "NONE" THEN "NoObservedValue" ELSE ' + state + '.lastReturned.requestKey',
    '  /\\ obs.reconcileReturnedReceiptModelKey = IF ' + state + '.lastReturned.requestKey = "NONE" THEN "NoObservedValue" ELSE ' + state + '.lastReturned.id',
    '  /\\ obs.reconcileReturnedWorkspaceKey = IF ' + state + '.lastReturned.requestKey = "NONE" THEN "NoObservedValue" ELSE ' + state + '.lastReturned.workspace',
    '  /\\ obs.reconcileReturnedOutputModelKey = IF ' + state + '.lastReturned.requestKey = "NONE" THEN "NoObservedValue" ELSE ' + state + '.lastReturned.outputKey',
    '  /\\ (obs.reconcileReturnedRequestKey = "NoObservedValue" => obs.reconcileReturnedOutputIds = "5b5d")',
    '  /\\ (obs.receiptPresent <=> (' + state + '.receipt.id # "NONE"))',
  ].join('\n');
}

function constructionLifecycleObservationMapping(prime) {
  const applicable = prime ? 'ResourceIsApplicableNext(attempt)' : 'ResourceIsApplicable(attempt)';
  const overrideState = prime ? "overrideState'" : 'overrideState';
  const owner = prime ? "owner'" : 'owner';
  const currentIdentity = prime ? "currentIdentity'" : 'currentIdentity';
  const attemptPhase = 'attemptPhase' + prime;
  const requestIdentity = 'requestIdentity' + prime;
  const proposalIds = 'proposalIdPresent' + prime;
  const previewReceipts = 'previewReceipt' + prime;
  const previewRows = 'previewRowsNonNull' + prime;
  const activeAttempt = (predicate) =>
    '(\\E attempt \\in Attempts: ' + owner + ' = attempt /\\ (' + requestIdentity + ')[attempt] = ' + currentIdentity + ' /\\ ' + predicate + ')';
  const visibleProposalId = activeAttempt('(' + attemptPhase + ')[attempt] \\in {"ready", "needs-repair"} /\\ (' + proposalIds + ')[attempt]');
  const visiblePreviewReceipt = activeAttempt('(' + attemptPhase + ')[attempt] = "ready" /\\ (' + previewReceipts + ')[attempt] # NoReceipt');
  const visiblePreviewRows = activeAttempt('(' + attemptPhase + ')[attempt] = "ready" /\\ (' + previewRows + ')[attempt]');
  return [
    '  /\\ (obs.status = "idle") <=> (' + owner + ' = NoAttempt \\/ (\\E attempt \\in Attempts: ' + owner + ' = attempt /\\ (' + requestIdentity + ')[attempt] # ' + currentIdentity + '))',
    '  /\\ (obs.status = "previewing") <=> (' + overrideState + ' # "applying" /\\ ' + activeAttempt('(' + attemptPhase + ')[attempt] \\in {"proposal-pending", "preview-pending"}') + ')',
    '  /\\ (obs.status = "ready") <=> (' + overrideState + ' # "applying" /\\ ' + activeAttempt('(' + attemptPhase + ')[attempt] = "ready"') + ')',
    '  /\\ (obs.status = "error") <=> (' + overrideState + ' # "applying" /\\ ' + activeAttempt('(' + attemptPhase + ')[attempt] = "error"') + ')',
    '  /\\ (obs.status = "needs-repair") <=> (' + overrideState + ' # "applying" /\\ ' + activeAttempt('(' + attemptPhase + ')[attempt] = "needs-repair"') + ')',
    '  /\\ (obs.status = "applying") <=> (overrideState' + prime + ' = "applying")',
    '  /\\ obs.canApply <=> (' + activeAttempt(applicable) + ' /\\ ' + overrideState + ' # "applying")',
    '  /\\ (obs.canApply => obs.previewReceiptId = obs.proposalId)',
    '  /\\ (obs.canApply => obs.previewOutputId = obs.proposalOutputId)',
    '  /\\ obs.proposalIdPresent <=> ' + visibleProposalId,
    '  /\\ obs.previewReceiptPresent <=> ' + visiblePreviewReceipt,
    '  /\\ obs.receiptPresent <=> ' + visiblePreviewReceipt,
    '  /\\ obs.rowsPresent <=> ' + visiblePreviewRows,
  ].join('\n');
}

function actionLabelMatches(model) {
  if (model !== 'BuilderPreview') return 'ActionLabelMatches(label) == lastAction\' = label';
  return [
    "ActionLabelMatches(label) ==",
    "  lastAction' = label",
    '  \\/ (label = "DiscoveryComplete"',
    '      /\\ lastAction\' \\in {"DiscoveryComplete(complete)",',
    '                          "DiscoveryComplete(ignored)",',
    '                          "DiscoveryComplete(missing-unsupported)"})',
  ].join('\n');
}

function traceOwnerValue(action) {
  return action.owner && typeof action.owner === 'object' ? action.owner.attempt : 'NoAttempt';
}

function traceExpectedProposalValue(action) {
  return action.expectedProposal ?? 'NoExpectedProposal';
}

function collectGroupLifecycleTokens(trace) {
  const stepTokens = new Map();
  const captureTokens = new Map();
  const keyTokens = new Map();
  const addStep = (value) => {
    if (typeof value === 'string' && !stepTokens.has(value)) {
      stepTokens.set(value, BUILDER_GROUP_STEP_TOKENS[stepTokens.size]);
    }
  };
  const addGroup = (group) => {
    if (!isPlainObject(group)) return;
    addStep(group.stepId);
    for (const key of group.keys) {
      if (typeof key === 'string' && !keyTokens.has(key)) {
        keyTokens.set(key, BUILDER_GROUP_KEY_TOKENS[keyTokens.size]);
      }
    }
  };
  const addIntent = (intent) => {
    if (!isPlainObject(intent)) return;
    addStep(intent.targetStepId);
    addGroup(intent.base);
    addGroup(intent.proposed);
  };
  for (const action of trace.actions) {
    const lifecycle = action.groupLifecycle;
    if (!isPlainObject(lifecycle)) continue;
    if (isPlainObject(lifecycle.owner) && typeof lifecycle.owner.captureId === 'string' &&
        !captureTokens.has(lifecycle.owner.captureId)) {
      captureTokens.set(lifecycle.owner.captureId, BUILDER_GROUP_CAPTURE_TOKENS[captureTokens.size]);
    }
    if (isPlainObject(lifecycle.candidateOwner) && typeof lifecycle.candidateOwner.captureId === 'string' &&
        !captureTokens.has(lifecycle.candidateOwner.captureId)) {
      captureTokens.set(lifecycle.candidateOwner.captureId, BUILDER_GROUP_CAPTURE_TOKENS[captureTokens.size]);
    }
    addIntent(lifecycle.candidateIntent);
    addGroup(lifecycle.savedBefore?.group);
    addGroup(lifecycle.savedAfter?.group);
    addGroup(lifecycle.mountedGroup);
  }
  return { stepTokens, captureTokens, keyTokens };
}

function cdaLifecycleOwner(action) {
  return action.groupLifecycle?.owner ?? action.rowExpansionLifecycle?.owner ?? action.relatedSourceLifecycle?.owner ?? null;
}

function cdaCandidateOwner(action) {
  return action.groupLifecycle?.candidateOwner ?? action.rowExpansionLifecycle?.owner ?? action.relatedSourceLifecycle?.owner ?? null;
}

function cdaSavedSnapshots(action) {
  const lifecycle = action.groupLifecycle ?? action.rowExpansionLifecycle ?? action.relatedSourceLifecycle;
  return isPlainObject(lifecycle) ? [lifecycle.savedBefore, lifecycle.savedAfter].filter(isPlainObject) : [];
}

function collectCdaTokens(trace, journeyCase) {
  const stepTokens = new Map();
  const captureTokens = new Map();
  const keyTokens = new Map();
  const outputTokens = new Map();
  const digestTokens = new Map();
  const versionSteps = new Map();
  const workspaceTokens = new Map();
  const snapshotByVersion = new Map();
  let priorRawVersion;
  let latestVersionStep = -1;
  const addStep = (stepId) => {
    if (typeof stepId === 'string' && !stepTokens.has(stepId)) {
      stepTokens.set(stepId, BUILDER_GROUP_STEP_TOKENS[stepTokens.size]);
    }
  };
  const addGroup = (group) => {
    if (!isPlainObject(group)) return;
    addStep(group.stepId);
    for (const key of group.keys ?? []) {
      if (!keyTokens.has(key)) keyTokens.set(key, BUILDER_GROUP_KEY_TOKENS[keyTokens.size]);
    }
  };
  const addGroupIntent = (intent) => {
    if (!isPlainObject(intent)) return;
    addStep(intent.targetStepId);
    addGroup(intent.base);
    addGroup(intent.proposed);
  };
  const addOwner = (owner) => {
    const identity = observedRequestKey(owner);
    if (identity && !captureTokens.has(identity)) {
      captureTokens.set(identity, 'c' + (captureTokens.size + 1));
    }
  };

  for (const action of trace.actions) {
    addOwner(cdaLifecycleOwner(action));
    addOwner(action.groupLifecycle?.candidateOwner);
    addOwner(cdaCandidateOwner(action));
    addGroupIntent(action.groupLifecycle?.candidateIntent);
    for (const saved of cdaSavedSnapshots(action)) {
      addGroup(saved.group);
      const cdaCASProblemText = cdaCASProblem(saved, 'CDA token collection');
      if (cdaCASProblemText) fail(cdaCASProblemText);
      const workspaceKey = saved.explorerId;
      if (!workspaceTokens.has(workspaceKey)) workspaceTokens.set(workspaceKey, 'W1');
      if (workspaceTokens.size > 1) fail('CDA trace changed Explorer identity across its saved Builder snapshots');
      if (!outputTokens.has(saved.outputId)) {
        const token = BUILDER_CDA_OUTPUT_TOKENS[outputTokens.size];
        if (!token) fail('CDA trace observed more output identities than its finite output-token domain');
        outputTokens.set(saved.outputId, token);
      }
      if (!versionSteps.has(saved.draftVersion)) {
        const versionStep = versionSteps.size;
        if (versionStep > journeyCase.casTransitionCount) fail('CDA trace observed more draft versions than its finite snapshot domain');
        if (priorRawVersion !== undefined && saved.draftVersion !== priorRawVersion + 1) {
          fail('CDA trace draftVersion sequence must advance by one for each persisted Apply');
        }
        versionSteps.set(saved.draftVersion, versionStep);
        priorRawVersion = saved.draftVersion;
      }
      if (!digestTokens.has(saved.draftDigest)) {
        const digestToken = BUILDER_CDA_DIGEST_TOKENS[digestTokens.size];
        if (!digestToken) fail('CDA trace observed more raw draft digests than its finite digest-token domain');
        digestTokens.set(saved.draftDigest, digestToken);
      }
      const currentVersionStep = versionSteps.get(saved.draftVersion);
      if (currentVersionStep < latestVersionStep && !BUILDER_GROUP_SETTLEMENT_LABELS.has(action.label)) {
        fail('CDA trace saved Builder state snapshots cannot move backward outside a retained proposal settlement');
      }
      latestVersionStep = Math.max(latestVersionStep, currentVersionStep);
      const signature = canonicalJson({
        explorerId: saved.explorerId,
        outputId: saved.outputId,
        draftVersion: saved.draftVersion,
        draftDigest: saved.draftDigest,
        workspace: saved.workspace,
      });
      const priorSignature = snapshotByVersion.get(saved.draftVersion);
      if (priorSignature !== undefined && priorSignature !== signature) {
        fail('CDA trace reuses a draftVersion with different raw workspace/output/digest state');
      }
      snapshotByVersion.set(saved.draftVersion, signature);
    }
  }
  if (versionSteps.size !== journeyCase.casTransitionCount + 1) {
    fail('CDA trace must preserve the initial draft plus all seven observed Apply CAS states');
  }
  for (const action of trace.actions) {
    for (const lifecycle of [action.groupLifecycle, action.relatedSourceLifecycle]) {
      if (!isPlainObject(lifecycle)) continue;
      addGroup(lifecycle.savedBefore?.group);
      addGroup(lifecycle.savedAfter?.group);
    }
    addGroupIntent(action.groupLifecycle?.candidateIntent);
    if (action.label === 'Reload' && isPlainObject(action.groupLifecycle)) {
      addGroup(cdaMountedGroupProjectionForTla(
        action.groupLifecycle.mountedGroup,
        action.groupLifecycle.savedAfter,
        'CDA token collection Reload mounted Group',
      ));
    }
  }
  if (stepTokens.size > BUILDER_GROUP_STEP_TOKENS.length || keyTokens.size > BUILDER_GROUP_KEY_TOKENS.length) {
    fail('CDA trace exceeds the finite Group step or data-key token domain');
  }
  return { stepTokens, captureTokens, keyTokens, outputTokens, digestTokens, versionSteps, workspaceTokens };
}

function cdaCaptureToken(owner, tokens) {
  const identity = observedRequestKey(owner);
  const token = tokens.captureTokens.get(identity);
  if (!token) fail('CDA request owner does not map to its unique finite trace token');
  return tlaString(token);
}

function cdaGroupProjectionForTla(group, tokens) {
  return groupRecordForTla(group, tokens.stepTokens, tokens.keyTokens);
}

function cdaMountedGroupProjection(mountedGroup, saved, where) {
  if (!isPlainObject(saved)) return { problem: where + ' must retain the actual postreload saved Builder snapshot' };
  if (saved.group === null) {
    if (mountedGroup !== null) return { problem: where + ' must be null when the saved Builder snapshot has no Group' };
    return { projection: null };
  }
  const selected = savedBuilderDocument(saved, where);
  if (selected.problem) return selected;
  const labelProblem = mountedGroupKeyLabelsProblem(
    mountedGroup, saved.group, selected.document, where,
  );
  if (labelProblem) return { problem: labelProblem };
  if (mountedGroup.stepId !== saved.group.stepId || mountedGroup.aggregate !== saved.group.aggregate) {
    return { problem: where + ' observed Group step and aggregate must match the saved Group projection' };
  }
  const keys = [];
  for (let index = 0; index < mountedGroup.keyLabels.length; index += 1) {
    const label = mountedGroup.keyLabels[index];
    const rawKeyId = saved.group.keys[index];
    const resolved = builderColumnLabelForId(selected.document, rawKeyId, where + '.keyLabels[' + index + ']');
    if (resolved.problem) return resolved;
    if (label !== resolved.label) {
      return { problem: where + '.keyLabels must join to the saved Group key at the same position' };
    }
    keys.push(rawKeyId);
  }
  return { projection: { stepId: mountedGroup.stepId, keys, aggregate: mountedGroup.aggregate } };
}

function cdaMountedGroupProjectionForTla(mountedGroup, saved, where) {
  const result = cdaMountedGroupProjection(mountedGroup, saved, where);
  if (result.problem) fail(result.problem);
  return result.projection;
}

function cdaRelatedSourceRecordForTla(related, tokens) {
  if (related === null || related === undefined) {
    return '[sourceToken |-> "None", relatedStepToken |-> "None", inputGroupStepToken |-> "None", outputColumnToken |-> "None", routeToken |-> "None"]';
  }
  if (!isPlainObject(related) || !tokens.stepTokens.has(related.inputStepId)) {
    fail('CDA related-source projection must map its actual input Group step');
  }
  return '[sourceToken |-> "ObservationStatus", relatedStepToken |-> "R1", inputGroupStepToken |-> ' +
    tlaString(tokens.stepTokens.get(related.inputStepId)) + ', outputColumnToken |-> "O1", routeToken |-> "SpecimenPatientObservation"]';
}

function cdaSnapshotRecordForTla(saved, tokens, journeyCase, where) {
  const problem = relatedSourceSnapshotProblem(saved, where, journeyCase);
  if (problem.problem) fail(problem.problem);
  const workspaceToken = tokens.workspaceTokens.get(saved.explorerId);
  const outputToken = tokens.outputTokens.get(saved.outputId);
  const versionStep = tokens.versionSteps.get(saved.draftVersion);
  const digestToken = tokens.digestTokens.get(saved.draftDigest);
  if (!workspaceToken || !outputToken || versionStep === undefined || !digestToken) {
    fail(where + ' snapshot values do not map to the finite observed-run tokens');
  }
  return '[workspaceToken |-> ' + tlaString(workspaceToken) +
    ', group |-> ' + cdaGroupProjectionForTla(saved.group, tokens) +
    ', relatedSource |-> ' + cdaRelatedSourceRecordForTla(saved.relatedSource, tokens) +
    ', outputToken |-> ' + tlaString(outputToken) +
    ', draftVersion |-> ' + versionStep +
    ', draftDigest |-> ' + tlaString(digestToken) + ']';
}

function cdaRelatedIntentForTla(intent, tokens) {
  if (!isPlainObject(intent)) return 'NoRelatedSourceIntent';
  const targetStep = intent.kind === 'add' ? 'None' : 'R1';
  return '[kind |-> ' + tlaString(intent.kind) +
    ', targetStepId |-> ' + tlaString(targetStep) +
    ', groupInputStep |-> "G1"' +
    ', base |-> ' + cdaRelatedSourceRecordForTla(intent.base, tokens) +
    ', proposed |-> ' + cdaRelatedSourceRecordForTla(intent.proposed, tokens) + ']';
}

function cdaRowExpansionIntentForTla(lifecycle) {
  if (lifecycle?.stage === 'SpecimenPatient') return 'CdaSpecimenPatientIntent';
  if (lifecycle?.stage === 'PatientObservation') return 'CdaPatientObservationIntent';
  fail('CDA row expansion proposal has no bounded observed stage');
}

function cdaOwnerRecordForTla(owner, tokens) {
  if (!isPlainObject(owner)) return 'NoPreviewOwner';
  const ownerEpoch = owner.mountEpoch;
  if (!Number.isInteger(ownerEpoch)) fail('CDA proposal owner must bind to its actual request-start mountEpoch');
  return '[mountEpoch |-> ' + ownerEpoch + ', captureId |-> ' + cdaCaptureToken(owner, tokens) + ']';
}

function cdaGroupInputToken(saved, tokens) {
  return saved.group === null ? '"None"' : tlaString(tokens.stepTokens.get(saved.group.stepId));
}

function groupRecordForTla(group, stepTokens, keyTokens) {
  if (group === null || group === undefined) return 'NoGroup';
  const shape = ['EmptyKeys', 'ByID', 'ByIDGender'][group.keys.length];
  const keys = '<<' + group.keys.map((key) => tlaString(keyTokens.get(key))).join(', ') + '>>';
  return '[stepId |-> ' + tlaString(stepTokens.get(group.stepId)) +
    ', shape |-> ' + tlaString(shape) +
    ', keys |-> ' + keys +
    ', aggregate |-> ' + tlaString(group.aggregate) + ']';
}

function candidateIntentForTla(intent, stepTokens, keyTokens) {
  if (!isPlainObject(intent)) return 'NoCandidateIntent';
  return '[kind |-> ' + tlaString(intent.kind) +
    ', targetStepId |-> ' + tlaString(intent.targetStepId === null ? 'NoStep' : stepTokens.get(intent.targetStepId)) +
    ', base |-> ' + groupRecordForTla(intent.base, stepTokens, keyTokens) +
    ', proposed |-> ' + groupRecordForTla(intent.proposed, stepTokens, keyTokens) + ']';
}

function cdaCandidateIntentForTla(intent, stepTokens, keyTokens, where, savedBefore) {
  if (!isPlainObject(intent) || intent.kind !== 'create') {
    return candidateIntentForTla(intent, stepTokens, keyTokens);
  }
  const targetProblem = cdaCreateGroupTargetStepProblem(intent, where, savedBefore);
  if (targetProblem) fail(targetProblem);
  return candidateIntentForTla({ ...intent, targetStepId: null }, stepTokens, keyTokens);
}

function tlaCaptureToken(value) {
  if (typeof value !== 'string' || !/^c(?:[1-9]|1[0-2])$/.test(value)) {
    fail('mapped Group capture token is outside the finite c1..c12 domain');
  }
  return tlaString(value);
}

function groupLifecycleRecords(trace) {
  const { stepTokens, captureTokens, keyTokens } = collectGroupLifecycleTokens(trace);
  return trace.actions.map((action) => {
    const lifecycle = action.groupLifecycle;
    if (!isPlainObject(lifecycle)) return '"NoGroupLifecycle"';
    const owner = isPlainObject(lifecycle.owner) ? lifecycle.owner : null;
    const savedBefore = lifecycle.savedBefore;
    const savedAfter = lifecycle.savedAfter;
    const workspaceSame = isPlainObject(savedBefore) && isPlainObject(savedAfter) &&
      canonicalJson(savedBefore.workspace) === canonicalJson(savedAfter.workspace);
    const versionSame = isPlainObject(savedBefore) && isPlainObject(savedAfter) &&
      canonicalJson(savedBefore.draftVersion) === canonicalJson(savedAfter.draftVersion);
    const digestSame = isPlainObject(savedBefore) && isPlainObject(savedAfter) &&
      canonicalJson(savedBefore.draftDigest) === canonicalJson(savedAfter.draftDigest);
    const candidateOwner = isPlainObject(lifecycle.candidateOwner)
      ? '[mountEpoch |-> ' + lifecycle.candidateOwner.mountEpoch +
        ', captureId |-> ' + tlaCaptureToken(captureTokens.get(lifecycle.candidateOwner.captureId)) + ']'
      : 'NoPreviewOwner';
    return '[mountEpoch |-> ' + lifecycle.mountEpoch +
      ', ownerEpoch |-> ' + (owner ? owner.mountEpoch : '"NoObservedValue"') +
      ', captureId |-> ' + (owner ? tlaCaptureToken(captureTokens.get(owner.captureId)) : '"NoCapture"') +
      ', candidateOwner |-> ' + candidateOwner +
      ', intent |-> ' + candidateIntentForTla(lifecycle.candidateIntent, stepTokens, keyTokens) +
      ', savedBeforeGroup |-> ' + groupRecordForTla(savedBefore?.group, stepTokens, keyTokens) +
      ', savedAfterGroup |-> ' + groupRecordForTla(savedAfter?.group, stepTokens, keyTokens) +
      ', mountedGroup |-> ' + groupRecordForTla(lifecycle.mountedGroup, stepTokens, keyTokens) +
      ', workspaceSame |-> ' + tlaBool(workspaceSame) +
      ', versionSame |-> ' + tlaBool(versionSame) +
      ', digestSame |-> ' + tlaBool(digestSame) + ']';
  });
}

function cdaSnapshotForActionLifecycle(action, lifecycle, field, tokens, journeyCase, where) {
  if ((lifecycle[field] === null || lifecycle[field] === undefined) &&
      cdaEventTimeSnapshotUnavailable(action, lifecycle)) return '"NoCdaSnapshot"';
  return cdaSnapshotRecordForTla(lifecycle[field], tokens, journeyCase, where + '.' + field);
}

function cdaLifecycleRecords(trace, tokens, journeyCase) {
  return trace.actions.map((action, index) => {
    const where = 'trace.actions[' + index + ']';
    const lifecycle = action.groupLifecycle ?? action.rowExpansionLifecycle ?? action.relatedSourceLifecycle;
    if (!isPlainObject(lifecycle)) return '"NoCdaLifecycle"';
    const owner = action.groupLifecycle?.owner ?? action.rowExpansionLifecycle?.owner ?? action.relatedSourceLifecycle?.owner;
    const lifecycleEpoch = Number.isInteger(lifecycle.mountEpoch) ? lifecycle.mountEpoch : null;
    const observedEpoch = Number.isInteger(action.observedEpoch)
      ? action.observedEpoch
      : Number.isInteger(lifecycle.observedEpoch) ? lifecycle.observedEpoch : lifecycleEpoch;
    const rawCandidateOwner = action.groupLifecycle?.candidateOwner ??
      action.relatedSourceLifecycle?.owner ?? action.rowExpansionLifecycle?.owner;
    const ownerEpochValue = Number.isInteger(owner?.mountEpoch)
      ? owner.mountEpoch
      : Number.isInteger(rawCandidateOwner?.mountEpoch)
        ? rawCandidateOwner.mountEpoch
        : lifecycleEpoch;
    if (!Number.isInteger(observedEpoch) || !Number.isInteger(ownerEpochValue)) {
      fail(where + ' must preserve actual observed and proposal-owner mount epochs');
    }
    const ownerEpoch = ownerEpochValue;
    const captureId = owner ? cdaCaptureToken(owner, tokens) : '"NoCapture"';
    const candidateOwner = cdaOwnerRecordForTla(
      rawCandidateOwner,
      tokens,
    );
    const before = cdaSnapshotForActionLifecycle(action, lifecycle, 'savedBefore', tokens, journeyCase, where);
    const after = cdaSnapshotForActionLifecycle(action, lifecycle, 'savedAfter', tokens, journeyCase, where);
    const mountEpoch = observedEpoch;
    const noEventSnapshot = (lifecycle.savedBefore === null || lifecycle.savedBefore === undefined) &&
      (lifecycle.savedAfter === null || lifecycle.savedAfter === undefined) &&
      cdaEventTimeSnapshotUnavailable(action, lifecycle);
    const savedGroupInput = noEventSnapshot
      ? '"NoGroupInputObserved"'
      : cdaGroupInputToken(lifecycle.savedBefore, tokens);
    const intent = action.relatedSourceLifecycle
      ? cdaRelatedIntentForTla(lifecycle.candidateIntent, tokens)
      : action.rowExpansionLifecycle
        ? cdaRowExpansionIntentForTla(lifecycle)
        : cdaCandidateIntentForTla(
          lifecycle.candidateIntent, tokens.stepTokens, tokens.keyTokens,
          where + '.groupLifecycle.candidateIntent', lifecycle.savedBefore,
        );
    const mountedGroupProjection = action.label === 'Reload' && isPlainObject(action.groupLifecycle)
      ? cdaMountedGroupProjectionForTla(
        action.groupLifecycle.mountedGroup,
        action.groupLifecycle.savedAfter,
        where + '.groupLifecycle.mountedGroup',
      )
      : null;
    const mountedGroup = mountedGroupProjection
      ? cdaGroupProjectionForTla(mountedGroupProjection, tokens)
      : 'NoGroup';
    const mountedRelated = action.label === 'Reload'
      ? cdaRelatedSourceRecordForTla(action.groupLifecycle.mountedRelatedSource, tokens)
      : cdaRelatedSourceRecordForTla(null, tokens);
    const stageToken = action.rowExpansionLifecycle
      ? tlaString(action.rowExpansionLifecycle.stage)
      : '"NoStage"';
    const tableTokens = action.rowExpansionLifecycle?.stage === 'SpecimenPatient'
      ? ['Specimen', 'Patient']
      : action.rowExpansionLifecycle?.stage === 'PatientObservation'
        ? ['Patient', 'Observation']
        : ['NoTable', 'NoTable'];
    return '[mountEpoch |-> ' + mountEpoch +
      ', ownerEpoch |-> ' + ownerEpoch +
      ', captureId |-> ' + captureId +
      ', candidateOwner |-> ' + candidateOwner +
      ', intent |-> ' + intent +
      ', savedGroupInput |-> ' + savedGroupInput +
      ', savedBefore |-> ' + before +
      ', savedAfter |-> ' + after +
      ', stageToken |-> ' + stageToken +
      ', savedBeforeTable |-> ' + tlaString(tableTokens[0]) +
      ', savedAfterTable |-> ' + tlaString(tableTokens[1]) +
      ', observedMountedGroup |-> ' + mountedGroup +
      ', observedMountedRelated |-> ' + mountedRelated + ']';
  });
}

function actionGroupLifecycleMatches(enabled) {
  if (!enabled) return 'ActionGroupLifecycleMatches(index) == TRUE';
  return [
    'ActionGroupLifecycleMatches(index) ==',
    '  LET label == TraceActions[index]',
    '      lifecycle == TraceGroupLifecycles[index]',
    '      attempt == TraceOwnerAttempts[index]',
    '  IN CASE label = "Preview" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ lifecycle.mountEpoch = lifecycle.ownerEpoch',
    '         /\\ Preview(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId, lifecycle.intent)',
    '       [] label = "PreviewComplete" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ lifecycle.mountEpoch = lifecycle.ownerEpoch',
    '         /\\ PreviewComplete(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId)',
    '       [] label = "PreviewError" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ lifecycle.mountEpoch = lifecycle.ownerEpoch',
    '         /\\ PreviewError(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId, TraceObservations[index].proposalIdPresent)',
    '       [] label = "PreviewRequestCancelled" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ lifecycle.mountEpoch = lifecycle.ownerEpoch',
    '         /\\ PreviewRequestCancelled(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId)',
    '       [] label = "LatePreviewCompleteIgnored" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ LatePreviewCompleteIgnored(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId)',
    '       [] label = "LatePreviewErrorIgnored" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ LatePreviewErrorIgnored(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId)',
    '       [] label = "Group" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ Group(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedBeforeGroup, lifecycle.savedAfterGroup)',
    '       [] label = "EditGroup" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ EditGroup(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedBeforeGroup, lifecycle.savedAfterGroup)',
    '       [] label = "EditGroupAgain" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ EditGroupAgain(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedBeforeGroup, lifecycle.savedAfterGroup)',
    '       [] label = "RemoveGroup" ->',
    '         /\\ lifecycle.mountEpoch = mountEpoch',
    '         /\\ RemoveGroup(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedBeforeGroup, lifecycle.savedAfterGroup)',
    '       [] label = "GroupEditCancel" ->',
    '         GroupEditCancel(lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.savedBeforeGroup, lifecycle.savedAfterGroup, lifecycle.workspaceSame, lifecycle.versionSame, lifecycle.digestSame)',
    '       [] label = "GroupRemovalCancel" ->',
    '         GroupRemovalCancel(lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.savedBeforeGroup, lifecycle.savedAfterGroup, lifecycle.workspaceSame, lifecycle.versionSame, lifecycle.digestSame)',
    '       [] label = "Reload" ->',
    '         Reload(lifecycle.mountEpoch, lifecycle.savedBeforeGroup, lifecycle.savedAfterGroup, lifecycle.mountedGroup)',
    '       [] OTHER -> TRUE',
  ].join('\n');
}

function actionCdaLifecycleMatches() {
  return [
    'ActionCdaLifecycleMatches(index) ==',
    '  LET label == TraceActions[index + 1]',
    '      lifecycle == TraceCdaLifecycles[index + 1]',
    '      attempt == TraceOwnerAttempts[index + 1]',
    '  IN CASE label = "Preview" ->',
    '         /\\ CdaPreview(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId, lifecycle.intent, lifecycle.savedGroupInput, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] label = "PreviewComplete" ->',
    '         /\\ CdaPreviewComplete(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId)',
    '       [] label = "PreviewError" ->',
    '         /\\ CdaPreviewError(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId, TraceObservations[index + 1].proposalIdPresent)',
    '       [] label = "PreviewRequestCancelled" ->',
    '         /\\ CdaPreviewRequestCancelled(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId)',
    '       [] label = "LatePreviewCompleteIgnored" ->',
    '         /\\ CdaLatePreviewCompleteIgnored(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId)',
    '       [] label = "LatePreviewErrorIgnored" ->',
    '         /\\ CdaLatePreviewErrorIgnored(attempt, lifecycle.mountEpoch, lifecycle.ownerEpoch, lifecycle.captureId)',
    '       [] label = "ExpandRows" ->',
    '         /\\ ExpandRows(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.stageToken, lifecycle.savedBeforeTable, lifecycle.savedAfterTable, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] label = "Group" ->',
    '         /\\ CdaGroup(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedGroupInput, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] label = "EditGroup" ->',
    '         /\\ CdaEditGroup(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedGroupInput, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] label = "EditGroupAgain" ->',
    '         /\\ CdaEditGroupAgain(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedGroupInput, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] label = "GroupEditCancel" ->',
    '         /\\ CdaGroupEditCancel(lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.savedGroupInput, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] label = "Reload" ->',
    '         /\\ CdaReload(lifecycle.mountEpoch, lifecycle.savedBefore, lifecycle.savedAfter, lifecycle.observedMountedGroup, lifecycle.observedMountedRelated)',
    '       [] label = "RelatedSourcePreview" ->',
    '         /\\ RelatedSourcePreview(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedGroupInput, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] label = "RelatedSourceApply" ->',
    '         /\\ RelatedSourceApply(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedGroupInput, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] label = "RelatedSourceCancel" ->',
    '         /\\ RelatedSourceCancel(attempt, lifecycle.mountEpoch, lifecycle.candidateOwner, lifecycle.intent, lifecycle.savedGroupInput, lifecycle.savedBefore, lifecycle.savedAfter)',
    '       [] OTHER -> FALSE',
  ].join('\n');
}

function actionOwnerMatches(model, hasGroupLifecycle = false, hasCdaRelatedCase = false) {
  if (model === 'ConstructionLifecycle') {
    return [
      'ActionOwnerMatches(index) ==',
      '  LET label == TraceActions[index]',
      '      attempt == TraceOwnerAttempts[index]',
      '      expected == TraceExpectedProposals[index]',
      '  IN CASE label = "StartProposal(P1)" -> attempt = "P1" /\\ owner\' = Attempt1',
      '       [] label = "StartProposal(P2)" -> attempt = "P2" /\\ owner\' = Attempt2',
      '       [] label = "ReplaceProposalOwner" -> /\\ owner\' = Attempt2 /\\ attempt = "P2"',
      '       [] label = "RetryProposal(P1-to-P2)" -> /\\ owner\' = Attempt2 /\\ attempt = "P2"',
      '       [] label = "CancelProposalOwner" -> owner = IF attempt = "P1" THEN Attempt1 ELSE Attempt2',
      '       [] label \\in {"ProposalComplete(P1)", "ProposalError(P1)", "PreviewComplete(P1)", "PreviewError(P1)", "StartPreview(P1)", "LateProposalCompleteIgnored(P1)", "LateProposalErrorIgnored(P1)", "LatePreviewCompleteIgnored(P1)", "LatePreviewErrorIgnored(P1)"} -> attempt = "P1"',
      '       [] label \\in {"ProposalComplete(P2)", "ProposalError(P2)", "PreviewComplete(P2)", "PreviewError(P2)", "StartPreview(P2)", "LateProposalCompleteIgnored(P2)", "LateProposalErrorIgnored(P2)", "LatePreviewCompleteIgnored(P2)", "LatePreviewErrorIgnored(P2)"} -> attempt = "P2"',
      '       [] label \\in {"BeginApply(accepted)", "BeginApply(rejected)"} ->',
      '            /\\ owner = IF attempt = "P1" THEN Attempt1 ELSE IF attempt = "P2" THEN Attempt2 ELSE NoAttempt',
      '            /\\ (expected = "P1" => lastBeginApplyExpected\' = Attempt1)',
      '            /\\ (expected = "P2" => lastBeginApplyExpected\' = Attempt2)',
      '            /\\ lastBeginApplyExpected\' = IF expected = "P1" THEN Attempt1 ELSE IF expected = "P2" THEN Attempt2 ELSE NoExpectedProposal',
      '       [] OTHER -> TRUE',
    ].join('\n');
  }
  if (model === 'BuilderPreview') {
    return [
      'ActionOwnerMatches(index) ==',
      '  LET label == TraceActions[index]',
      '      attempt == TraceOwnerAttempts[index]',
      '      generation == IF attempt = "P1" THEN 1 ELSE 2',
      '  IN CASE label = "StartProposal" -> /\\ proposalOwner\'.generation = generation /\\ attempt = "P1"',
      '       [] label = "ReplaceProposal" -> /\\ proposalOwner\'.generation = generation /\\ attempt = "P2"',
      '       [] label = "ProposalComplete" -> proposalOwner.generation = generation',
      '       [] label = "StartPreview" -> previewOwner\'.generation = generation',
      '       [] label = "PreviewComplete" -> previewOwner.generation = generation',
      '       [] label = "BeginApply" -> applyOwner\'.generation = generation',
      '       [] label = "ApplyAck" -> applyOwner.generation = generation',
      '       [] OTHER -> TRUE',
    ].join('\n');
  }
  if (model === 'BuilderJourney') {
    const latePreviewOwnerMatch = hasGroupLifecycle
      ? '/\\ proposalPreviewOwner \\in {"P1", "P2"}'
      : '/\\ proposalPreviewOwner \\in {"P1", "P2"} /\\ proposalPreviewOwner # attempt';
    return [
      'ActionOwnerMatches(index) ==',
      '  LET label == TraceActions[index]',
      '      attempt == TraceOwnerAttempts[index]',
      '  IN CASE label = "Preview" -> /\\ attempt \\in {"P1", "P2"} /\\ proposalPreviewOwner\' = attempt',
      '       [] label \\in {"PreviewComplete", "PreviewError"} -> proposalPreviewOwner = attempt',
      '       [] label = "PreviewRequestCancelled" -> /\\ attempt \\in {"P1", "P2"} /\\ proposalPreviewOwner = attempt',
      '       [] label \\in {"LatePreviewCompleteIgnored", "LatePreviewErrorIgnored"} -> ' + latePreviewOwnerMatch,
      '       [] label = "ChoicePreview" -> /\\ attempt \\in {"P1", "P2"} /\\ choicePreviewOwner\' = attempt',
      '       [] label \\in {"ChoicePreviewComplete", "ChoicePreviewError"} -> choicePreviewOwner = attempt',
      '       [] label = "AddColumn" -> /\\ attempt = choicePreviewOwner /\\ choicePreview = "ready"',
      '       [] label \\in {"Group", "GroupAgain", "EditGroup", "EditGroupAgain", "RemoveGroup"} -> attempt = proposalPreviewOwner',
      ...(hasCdaRelatedCase ? [
        '       [] label = "RelatedSourcePreview" -> /\\ attempt \\in {"P1", "P2"} /\\ proposalPreviewOwner\' = attempt',
        '       [] label \\in {"RelatedSourceCancel", "ExpandRows", "RelatedSourceApply"} -> attempt = proposalPreviewOwner',
      ] : []),
      '       [] OTHER -> TRUE',
    ].join('\n');
  }
  return 'ActionOwnerMatches(index) == TRUE';
}

function actionIdentityPreserved(model) {
  const labels = [
    'LateProposalCompleteIgnored',
    'LateProposalCompleteIgnored(P1)', 'LateProposalCompleteIgnored(P2)',
    'LateProposalErrorIgnored(P1)', 'LateProposalErrorIgnored(P2)',
    'LatePreviewCompleteIgnored',
    'LatePreviewCompleteIgnored(P1)', 'LatePreviewCompleteIgnored(P2)',
    'LatePreviewErrorIgnored',
    'LatePreviewErrorIgnored(P1)', 'LatePreviewErrorIgnored(P2)',
    'BeginApply(rejected)',
  ].map(tlaString);
  const fields = [
    'status', 'tablePreviewStatus', 'canApply', 'proposalIdPresent', 'previewReceiptPresent', 'rowsPresent', 'receiptPresent',
    'proposalId', 'proposalOutputId', 'proposalSnapshotToken', 'proposalDraftVersion', 'proposalDraftDigest',
    'previewReceiptId', 'previewOutputId',
    'sourcePreviewStatus', 'sourcePreviewReceiptId', 'sourcePreviewOutputId', 'sourcePreviewProposalId', 'sourcePreviewDraftVersion', 'sourcePreviewDraftDigest', 'sourcePreviewRowsPresent', 'sourcePreviewRowCount', 'sourcePreviewRenderedRowCount',
    'embeddedPreviewStatus', 'embeddedPreviewReceiptId', 'embeddedPreviewOutputId', 'embeddedPreviewProposalId',
    'embeddedPreviewDraftVersion', 'embeddedPreviewDraftDigest', 'embeddedPreviewRowsPresent', 'embeddedPreviewRowCount', 'embeddedPreviewRenderedRowCount',
    'applyAvailable', 'applyEnabled', 'applying',
  ];
  const equalities = fields.map((field) =>
    '       /\\ before.' + field + ' = after.' + field,
  );
  const reconcileFields = [
    'status', 'reconcileCurrentKey', 'reconcileCurrentDraftVersion',
    'reconcileCurrentDraftDigest', 'reconcileCurrentSnapshotToken', 'reconcileCurrentOutputModelKey',
    'reconcileCurrentExplorerId', 'reconcileReceiptModelKey', 'reconcileReceiptRequestKey',
    'reconcileReceiptWorkspaceKey', 'reconcileReceiptOutputModelKey',
  ];
  const reconcileEqualities = reconcileFields.map((field) =>
    '       /\\ before.' + field + ' = after.' + field,
  );
  return [
    'ObservedIdentityUnchanged(before, after) ==',
    ...equalities,
    '',
    'ActionIdentityPreserved(index) ==',
    '  LET label == TraceActions[index]',
    '      before == TraceObservations[index - 1]',
    '      after == TraceObservations[index]',
    '  IN IF label = "PreviewRequestCancelled"',
    '     THEN /\\ ObservedIdentityUnchanged(before, after)',
    '          /\\ before.selectedObservation = after.selectedObservation',
    '     ELSE IF label \\in {' + labels.join(', ') + '}',
    '          THEN ObservedIdentityUnchanged(before, after)',
    ...(model === 'ReconcileLifecycle'
      ? [
          '          ELSE IF label = "LateReconcileIgnored(A)"',
          '               THEN /\\ before.status = after.status',
          ...reconcileEqualities,
        ]
      : []),
    ...(model === 'BuilderPreview'
      ? [
          '          ELSE IF label = "ApplyAck" /\\ applyOwner.generation # proposalOwner.generation',
          '               THEN ObservedIdentityUnchanged(before, after)',
          '               ELSE TRUE',
        ]
      : ['          ELSE TRUE']),
  ].join('\n');
}

function generateModule(trace, moduleName, hasGroupLifecycle, journeyCase = null) {
  if (journeyCase) return generateCdaTraceModule(trace, moduleName, journeyCase);
  const actions = ['"Init"', ...trace.actions.map((action) => tlaString(action.label))];
  const ownerAttempts = ['"NoAttempt"', ...trace.actions.map((action) => tlaString(traceOwnerValue(action)))];
  const expectedProposals = ['"NoExpectedProposal"', ...trace.actions.map((action) => tlaString(traceExpectedProposalValue(action)))];
  const observations = [
    tlaObservation(trace.initialObservation),
    ...trace.actions.map((action) => tlaObservation(action.observation)),
  ];
  const groupLifecycleObservations = ['"NoGroupLifecycle"', ...groupLifecycleRecords(trace)];
  if (groupLifecycleObservations.length !== actions.length) {
    fail('generated trace Group lifecycle rows must align with TraceActions, including Init');
  }
  const modelMapping = trace.model === 'BuilderPreview'
    ? builderPreviewObservationMapping
    : trace.model === 'ReconcileLifecycle'
      ? reconcileLifecycleObservationMapping
      : trace.model === 'BuilderJourney'
        ? builderJourneyObservationMapping
        : constructionLifecycleObservationMapping;
  const reconcileTrace = trace.model === 'ReconcileLifecycle';
  const legacyBuilderJourneyTrace = trace.model === 'BuilderJourney';
  const traceVariables = reconcileTrace ? 'VARIABLES traceIndex, traceRequestIds' : 'VARIABLES traceIndex';
  const traceTuple = reconcileTrace
    ? '<<vars, traceIndex, traceRequestIds>>'
    : legacyBuilderJourneyTrace ? '<<vars, CdaVars, traceIndex>>' : '<<vars, traceIndex>>';
  const traceInitIdentity = reconcileTrace
    ? [
        '  /\\ traceIndex = 0',
        '  /\\ traceRequestIds = [key \\in RequestKeys |-> "NoObservedValue"]',
      ]
    : [
        '  /\\ traceIndex = 1',
        '  /\\ ObsMatches(TraceObservations[1])',
      ];
  const reconcileTraceNext = reconcileTrace
    ? [
        '  IF traceIndex = 0',
        '  THEN /\\ RequestReconcile("A")',
        '       /\\ ActionLabelMatches("RequestReconcile(A)")',
        '       /\\ traceRequestIds\' = [key \\in RequestKeys |-> IF key = "A" THEN TraceObservations[1].reconcileRequestRawId ELSE "NoObservedValue"]',
        '       /\\ ObsMatchesNext(TraceObservations[1])',
        '       /\\ traceIndex\' = 1',
        '  ELSE IF traceIndex < Len(TraceActions)',
        '       THEN /\\ Next',
        "            /\\ ActionLabelMatches(TraceActions[traceIndex + 1])",
        '            /\\ ActionOwnerMatches(traceIndex + 1)',
        '            /\\ ActionIdentityPreserved(traceIndex + 1)',
        '            /\\ traceRequestIds\' = [key \\in RequestKeys |-> IF traceRequestIds[key] = "NoObservedValue" /\\ TraceObservations[traceIndex + 1].reconcileRequestModelKey = key THEN TraceObservations[traceIndex + 1].reconcileRequestRawId ELSE traceRequestIds[key]]',
        '            /\\ ObsMatchesNext(TraceObservations[traceIndex + 1])',
        "            /\\ traceIndex' = traceIndex + 1",
        '       ELSE /\\ UNCHANGED vars',
        '            /\\ UNCHANGED traceIndex',
        '            /\\ UNCHANGED traceRequestIds',
      ]
    : [
        '  IF traceIndex < Len(TraceActions)',
        '  THEN /\\ Next',
        "       /\\ ActionLabelMatches(TraceActions[traceIndex + 1])",
        '       /\\ ActionOwnerMatches(traceIndex + 1)',
        '       /\\ ActionIdentityPreserved(traceIndex + 1)',
        '       /\\ ActionGroupLifecycleMatches(traceIndex + 1)',
        '       /\\ ObsMatchesNext(TraceObservations[traceIndex + 1])',
        ...(legacyBuilderJourneyTrace ? ['       /\\ UNCHANGED CdaVars'] : []),
        "       /\\ traceIndex' = traceIndex + 1",
        ...(legacyBuilderJourneyTrace
          ? ['  ELSE UNCHANGED <<vars, CdaVars, traceIndex>>']
          : ['  ELSE /\\ UNCHANGED vars', '       /\\ UNCHANGED traceIndex']),
      ];
  const nextApplicable = trace.model === 'ConstructionLifecycle'
    ? [
        'ResourceIsApplicableNext(attempt) ==',
        '  /\\ attempt \\in Attempts',
        "  /\\ (attemptPhase')[attempt] = ProposalReady",
        "  /\\ (requestIdentity')[attempt] \\in Identities",
        "  /\\ (responseMatches')[attempt]",
        "  /\\ (proposalIdPresent')[attempt]",
        "  /\\ (proposalReceipt')[attempt] = ReceiptFor(attempt)",
        "  /\\ (responseOutput')[attempt] = OutputFor((requestIdentity')[attempt])",
        "  /\\ (previewReceipt')[attempt] = (proposalReceipt')[attempt]",
        "  /\\ (previewOutput')[attempt] = OutputFor((requestIdentity')[attempt])",
        "  /\\ (previewRowsNonNull')[attempt]",
        '',
      ]
    : [];
  return [
    '---------------- MODULE ' + moduleName + ' ----------------',
    'EXTENDS ' + trace.model + ', Naturals, Sequences, TLC',
    '',
    traceVariables,
    '',
    'TraceActions == ' + tlaSequence(actions),
    '',
    'TraceObservations == ' + tlaSequence(observations),
    '',
    'TraceOwnerAttempts == ' + tlaSequence(ownerAttempts),
    '',
    ...(hasGroupLifecycle ? ['TraceGroupLifecycles == ' + tlaSequence(groupLifecycleObservations), ''] : []),
    'TraceExpectedProposals == ' + tlaSequence(expectedProposals),
    '',
    actionLabelMatches(trace.model),
    '',
    actionOwnerMatches(trace.model, hasGroupLifecycle),
    '',
    actionGroupLifecycleMatches(hasGroupLifecycle),
    '',
    actionIdentityPreserved(trace.model),
    '',
    ...nextApplicable,
    'ObsMatches(obs) ==',
    modelMapping(''),
    '',
    'ObsMatchesNext(obs) ==',
    modelMapping("'"),
    '',
    'TraceInit ==',
    '  /\\ Init',
    ...(legacyBuilderJourneyTrace ? ['  /\\ CdaInit'] : []),
    ...traceInitIdentity,
    '',
    'TraceNext ==',
    ...reconcileTraceNext,
    '',
    '(* The usual [][TraceNext]_<<vars, traceIndex>> spec accepts stuttering at any state.',
    '   TraceProgress makes a short stutter before the last action a',
    '   violation. It is a trace-completion check, not a model safety',
    '   invariant; retained finding paths remain legal. *)',
    'TraceProgress ==',
    '  \\/ traceIndex = Len(TraceActions)',
    '  \\/ ENABLED TraceNext',
    '',
    'TraceSpec == TraceInit /\\ [][TraceNext]_' + traceTuple,
    '',
    '====',
    '',
  ].join('\n');
}

function generateCdaTraceModule(trace, moduleName, journeyCase) {
  const tokens = collectCdaTokens(trace, journeyCase);
  const actions = trace.actions.map((action) => tlaString(action.label));
  const ownerAttempts = trace.actions.map((action) => tlaString(traceOwnerValue(action)));
    const observations = trace.actions.map((action) => action.observation == null
      ? '"NoEventTimeObservation"'
      : tlaObservation(action.observation));
  const lifecycles = cdaLifecycleRecords(trace, tokens, journeyCase);
  if (lifecycles.length !== actions.length || ownerAttempts.length !== actions.length || observations.length !== actions.length) {
    fail('generated CDA lifecycle, observation, owner, and action rows must align exactly');
  }
  return [
    '---------------- MODULE ' + moduleName + ' ----------------',
    'EXTENDS ' + trace.model + ', Naturals, Sequences, TLC',
    '',
    'VARIABLES traceIndex',
    '',
    'TraceActions == ' + tlaSequence(actions),
    '',
    'TraceObservations == ' + tlaSequence(observations),
    '',
    'TraceOwnerAttempts == ' + tlaSequence(ownerAttempts),
    '',
    'TraceCdaLifecycles == ' + tlaSequence(lifecycles),
    '',
    actionCdaLifecycleMatches(),
    '',
    'TraceInit ==',
    '  /\\ Init',
    '  /\\ CdaInit',
    '  /\\ traceIndex = 0',
    '',
    'TraceNext ==',
    '  IF traceIndex < Len(TraceActions)',
    '  THEN /\\ CdaNext',
    '       /\\ ActionCdaLifecycleMatches(traceIndex)',
    "       /\\ traceIndex' = traceIndex + 1",
    '       /\\ UNCHANGED <<vars, cdaWitnessStage>>',
    '  ELSE UNCHANGED <<vars, CdaVars, traceIndex>>',
    '',
    'TraceProgress ==',
    '  \\/ traceIndex = Len(TraceActions)',
    '  \\/ ENABLED TraceNext',
    '',
    'TraceSpec == TraceInit /\\ [][TraceNext]_<<vars, CdaVars, traceIndex>>',
    '',
    '====',
    '',
  ].join('\n');
}

function generateConfig(trace, hasGroupLifecycle, journeyCase = null, tokens = null) {
  if (journeyCase) {
    const captureIds = [...tokens.captureTokens.values()];
    if (captureIds.length === 0) fail('CDA trace must retain unique observed proposal owner IDs');
    return [
      'SPECIFICATION TraceSpec',
      'CONSTANTS CaptureIds = {' + captureIds.map(tlaString).join(', ') + '}',
      'INVARIANT CdaTypeOK',
      'INVARIANT CdaGroupEditsPreserveRelatedBinding',
      'INVARIANT CdaCanceledProposalsPreserveExactSavedStateAndCAS',
      'INVARIANT CdaRelatedApplyRequiresCurrentOwnerAndSavedGroupInput',
      'INVARIANT CdaRelatedRemovalRestoresPriorGroupAndSourceState',
      'INVARIANT CdaReloadRestoresPersistedGroupAndRelatedBinding',
      'INVARIANT CdaApplyUsesCurrentCandidate',
      'INVARIANT CdaRowsOnlyAfterProposalPreview',
      'INVARIANT CdaOldOwnerCannotSettleCurrentPreview',
      'INVARIANT TraceProgress',
      '',
    ].join('\n');
  }
  return [
    'SPECIFICATION TraceSpec',
    ...(trace.model === 'BuilderJourney'
      ? ['CONSTANTS CaptureIds = {' + (hasGroupLifecycle
        ? BUILDER_GROUP_CAPTURE_TOKENS.map(tlaString)
        : ['c1', 'c2']).join(', ') + '}']
      : []),
    'INVARIANT TypeOK',
    'INVARIANT TraceProgress',
    ...(trace.model === 'BuilderJourney' && hasGroupLifecycle
      ? [
          'INVARIANT CancelPreservesPersistedGroup',
          'INVARIANT ApplyUsesCurrentCandidate',
          'INVARIANT ReloadRestoresPersistedGroup',
          'INVARIANT OldOwnerCannotSettleCurrentPreview',
          'INVARIANT PivotRequiresReshapeOrDirect',
          'INVARIANT RowsOnlyAfterProposalPreview',
          'INVARIANT SourceRowsRequireReadyTablePreview',
        ]
      : []),
    '',
  ].join('\n');
}

function parseTlc(text) {
  const statesMatch = text.match(/([0-9,]+) states generated, ([0-9,]+) distinct states found/);
  const depthMatch = text.match(/The depth of the complete state graph search is ([0-9]+)/);
  return {
    stateCount: statesMatch ? Number(statesMatch[1].replaceAll(',', '')) : null,
    distinctStateCount: statesMatch ? Number(statesMatch[2].replaceAll(',', '')) : null,
    depth: depthMatch ? Number(depthMatch[1]) : null,
    passed: text.includes('Model checking completed. No error has been found.'),
  };
}

function outputTail(text, lines = 30) {
  const split = text.trimEnd().split('\n');
  return split.slice(-lines).join('\n');
}

function runTlc(jarPath, trace, uniqueIndex, hasGroupLifecycle, journeyCase = null) {
  const runDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-traces-'));
  const modelDirectory = join(runDirectory, 'model');
  const metadataDirectory = join(runDirectory, 'states');
  mkdirSync(modelDirectory);
  mkdirSync(metadataDirectory);
  const moduleName = 'TraceCheck' + uniqueIndex;
  const moduleFile = moduleName + '.tla';
  const configFile = moduleName + '.cfg';
  copyFileSync(resolve(ROOT, MODEL_DIR, trace.model + '.tla'), join(modelDirectory, trace.model + '.tla'));
  const cdaTokens = journeyCase ? collectCdaTokens(trace, journeyCase) : null;
  writeFileSync(join(modelDirectory, moduleFile), generateModule(trace, moduleName, hasGroupLifecycle, journeyCase));
  writeFileSync(join(modelDirectory, configFile), generateConfig(trace, hasGroupLifecycle, journeyCase, cdaTokens));
  const args = [
    '-XX:+UseParallelGC', '-cp', jarPath, 'tlc2.TLC',
    '-workers', '4',
    '-metadir', metadataDirectory,
    '-config', configFile,
    moduleFile,
  ];
  const result = spawnSync('java', args, { cwd: modelDirectory, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
  const text = (result.stdout || '') + (result.stderr || '');
  const parsed = parseTlc(text);
  if (parsed.passed && parsed.stateCount > 0) {
    return { ...parsed, status: 'pass', modulePath: join(modelDirectory, moduleFile), runDirectory };
  }
  const traceIndexes = [...text.matchAll(/traceIndex = ([0-9]+)/g)].map((match) => Number(match[1]));
  const lastTraceIndex = traceIndexes.length > 0 ? traceIndexes[traceIndexes.length - 1] : null;
  const actionIndex = lastTraceIndex === null ? null : lastTraceIndex - 1;
  const label = actionIndex !== null && actionIndex >= 0 && actionIndex < trace.actions.length
    ? trace.actions[actionIndex].label
    : null;
  return {
    ...parsed,
    status: 'fail',
    modulePath: join(modelDirectory, moduleFile),
    runDirectory,
    failure: {
      message: result.error
        ? result.error.message
        : parsed.passed && parsed.stateCount === 0
          ? 'TLC found no initial state'
          : 'TLC did not complete the trace',
      actionIndex,
      actionLabel: label,
      modulePath: join(modelDirectory, moduleFile),
      outputTail: outputTail(text),
    },
  };
}

function writeReport(outputPath, report) {
  const absolute = resolve(process.cwd(), outputPath);
  mkdirSync(dirname(absolute), { recursive: true });
  writeFileSync(absolute, JSON.stringify(report, null, 2) + '\n');
  return absolute;
}

function reportUnmodeledEvents(traces, policy = {}) {
  const byKind = new Map();
  const entries = [];
  for (const trace of traces) {
    const events = Array.isArray(trace.unmodeledEvents) ? trace.unmodeledEvents : [];
    if (events.length === 0) continue;
    for (const event of events) {
      const kind = typeof event === 'string' ? event : event.kind;
      byKind.set(kind, (byKind.get(kind) ?? 0) + 1);
    }
    entries.push({ name: trace.name, model: trace.model, events });
  }
  const unclassified = [...byKind.entries()]
    .filter(([kind]) => !(kind in policy))
    .sort(([left], [right]) => left.localeCompare(right));
  return {
    total: entries.reduce((sum, entry) => sum + entry.events.length, 0),
    byKind: Object.fromEntries([...byKind.entries()].sort(([left], [right]) => left.localeCompare(right))),
    unclassified: {
      total: unclassified.reduce((sum, [, count]) => sum + count, 0),
      byKind: Object.fromEntries(unclassified),
    },
    traces: entries,
  };
}

function cdaRequestCoverageReceipt(trace) {
  const modeledRequestLabels = new Set([
    'Preview', 'RelatedSourcePreview', 'Group', 'EditGroup', 'EditGroupAgain',
    'ExpandRows', 'RelatedSourceApply',
  ]);
  const requests = Array.isArray(trace.nativeRequests) ? trace.nativeRequests : [];
  const observedModeledRequests = requests.filter((request) => request?.isMutation === true ||
    request?.method === 'POST' && cdaNativePathSuffix(request.path) === '/construction-proposals');
  const modeledBrowserIDs = trace.actions
    .filter((action) => modeledRequestLabels.has(action.label))
    .map((action) => action.request?.browserRequestId)
    .filter((browserRequestId) => typeof browserRequestId === 'string' && browserRequestId.length > 0);
  const mapped = new Set(modeledBrowserIDs);
  const observedIDs = observedModeledRequests.map((request) => request?.browserRequestId).filter(Boolean);
  const uniqueObservedIDs = new Set(observedIDs);
  return {
    observed: observedModeledRequests.length,
    modeled: observedModeledRequests.filter((request) => mapped.has(request.browserRequestId)).length,
    unmapped: observedModeledRequests.filter((request) => !mapped.has(request.browserRequestId)).length,
    duplicates: observedIDs.length - uniqueObservedIDs.size,
  };
}

function cdaRequiredMutationControlNames(trace, journeyCase) {
  const actions = Array.isArray(trace.actions) ? trace.actions : [];
  const observedOwnerEpochs = new Set(actions
    .filter((action) => ['Preview', 'RelatedSourcePreview'].includes(action.label))
    .map((action) => (action.groupLifecycle?.owner ?? action.rowExpansionLifecycle?.owner ??
      action.relatedSourceLifecycle?.owner)?.mountEpoch)
    .filter(Number.isInteger));
  return [
    ...journeyCase.requiredMutationControlNames,
    ...[...observedOwnerEpochs].sort((left, right) => left - right)
      .map((epoch) => journeyCase.observedOwnerEpochControlPrefix + epoch),
  ];
}

async function main() {
  const options = parseArgs();
  const contract = validateContract(readJson(options.contract ? resolve(process.cwd(), options.contract) : CONTRACT_RELATIVE));
  const counts = expectedCounts(contract);
  const hasGroupLifecycle = groupLifecycleEnabled(contract);

  let payload;
  let traceBytes;
  try {
    traceBytes = readFileSync(resolve(process.cwd(), options.traces));
    payload = JSON.parse(traceBytes.toString('utf8'));
  } catch (error) {
    fail('cannot read trace JSON ' + options.traces + ': ' + error.message);
  }
  const traceSha256 = sha256(traceBytes);
  if (!isPlainObject(payload)) fail('trace JSON must be an object');
  if (payload.schemaVersion !== 1) fail('trace JSON schemaVersion must be 1');
  const provenance = validateSourceProvenance(payload, contract);
  if (!Array.isArray(payload.traces)) fail('trace JSON traces must be an array');

  const events = allowedEvents(contract);
  const traces = payload.traces;
  const shapeProblems = new Map();
  for (let index = 0; index < traces.length; index += 1) {
    const problem = traceShapeProblem(traces[index], index, contract, events);
    if (problem) shapeProblems.set(index, problem);
  }

  const findingCount = traces.filter(isFindingTrace).length;
  const scheduleCount = traces.length - findingCount;
  const countProblems = [];
  if (!options.allowPartial) {
    if (traces.length !== counts.traceCount) {
      countProblems.push('expected ' + counts.traceCount + ' traces, found ' + traces.length);
    }
    if (scheduleCount !== counts.scheduleCount) {
      countProblems.push('expected ' + counts.scheduleCount + ' schedule traces, found ' + scheduleCount);
    }
    if (findingCount !== counts.findingCount) {
      countProblems.push('expected ' + counts.findingCount + ' expected-failure finding traces, found ' + findingCount);
    }
  }

  const actionSet = new Set();
  const eventSet = new Set();
  for (const trace of traces) {
    const modelActions = new Set(contract.models[trace.model].actions);
    if (trace.initialAction) {
      actionSet.add(normalizedActionLabel(trace.model, trace.initialAction.label, modelActions) ?? trace.initialAction.label);
      eventSet.add(trace.initialAction.event);
    }
    for (const action of trace.actions) {
      actionSet.add(normalizedActionLabel(trace.model, action.label, modelActions) ?? action.label);
      eventSet.add(action.event);
    }
  }
  const missingActions = contract.phase1.requiredActions.filter((action) => !actionSet.has(action));
  const missingEvents = contract.phase1.requiredEvents.filter((event) => !eventSet.has(event));
  const missingModels = (contract.phase1.requiredModels ?? []).filter((model) => !traces.some((trace) => trace.model === model));
  const missingActionsByTrace = {};
  for (const [traceName, requiredActions] of Object.entries(contract.phase1.requiredActionsByTrace ?? {})) {
    const trace = traces.find((candidate) => candidate.name === traceName);
    if (!trace) continue;
    const modelActions = new Set(contract.models[trace?.model]?.actions ?? []);
    const traceActions = new Set((trace?.actions ?? []).map((action) =>
      normalizedActionLabel(trace?.model, action.label, modelActions) ?? action.label));
    const missing = requiredActions.filter((action) => !traceActions.has(action));
    if (missing.length > 0) missingActionsByTrace[traceName] = missing;
  }
  const actionCountMismatchesByTrace = {};
  for (const [traceName, expectedCountsByAction] of Object.entries(contract.phase1.requiredActionCountsByTrace ?? {})) {
    const trace = traces.find((candidate) => candidate.name === traceName);
    if (!trace) continue;
    const modelActions = new Set(contract.models[trace?.model]?.actions ?? []);
    const actualCounts = new Map();
    for (const action of trace?.actions ?? []) {
      const label = normalizedActionLabel(trace.model, action.label, modelActions) ?? action.label;
      actualCounts.set(label, (actualCounts.get(label) ?? 0) + 1);
    }
    const mismatches = {};
    for (const [action, expected] of Object.entries(expectedCountsByAction)) {
      const actual = actualCounts.get(action) ?? 0;
      if (actual !== expected) mismatches[action] = { expected, actual };
    }
    if (Object.keys(mismatches).length > 0) actionCountMismatchesByTrace[traceName] = mismatches;
  }
  const actionCountMinimumMismatchesByTrace = {};
  for (const [traceName, minimumCountsByAction] of Object.entries(contract.phase1.requiredActionMinimumsByTrace ?? {})) {
    const trace = traces.find((candidate) => candidate.name === traceName);
    if (!trace) continue;
    const modelActions = new Set(contract.models[trace?.model]?.actions ?? []);
    const actualCounts = new Map();
    for (const action of trace?.actions ?? []) {
      const label = normalizedActionLabel(trace.model, action.label, modelActions) ?? action.label;
      actualCounts.set(label, (actualCounts.get(label) ?? 0) + 1);
    }
    const mismatches = {};
    for (const [action, minimum] of Object.entries(minimumCountsByAction)) {
      const actual = actualCounts.get(action) ?? 0;
      if (actual < minimum) mismatches[action] = { minimum, actual };
    }
    if (Object.keys(mismatches).length > 0) actionCountMinimumMismatchesByTrace[traceName] = mismatches;
  }
  const coverageProblems = [];
  if (!options.allowPartial) {
    if (missingActions.length > 0) coverageProblems.push('required actions missing: ' + missingActions.join(', '));
    if (missingEvents.length > 0) coverageProblems.push('required events missing: ' + missingEvents.join(', '));
    if (missingModels.length > 0) coverageProblems.push('required models missing: ' + missingModels.join(', '));
    for (const [traceName, missing] of Object.entries(missingActionsByTrace)) {
      coverageProblems.push('trace "' + traceName + '" missing required actions: ' + missing.join(', '));
    }
    for (const [traceName, mismatches] of Object.entries(actionCountMismatchesByTrace)) {
      for (const [action, count] of Object.entries(mismatches)) {
        coverageProblems.push('trace "' + traceName + '" action ' + action + ' count expected ' + count.expected + ', found ' + count.actual);
      }
    }
    for (const [traceName, mismatches] of Object.entries(actionCountMinimumMismatchesByTrace)) {
      for (const [action, count] of Object.entries(mismatches)) {
        coverageProblems.push('trace "' + traceName + '" action ' + action + ' minimum expected ' + count.minimum + ', found ' + count.actual);
      }
    }
  }

  const uniqueByKey = new Map();
  for (let index = 0; index < traces.length; index += 1) {
    const key = traceKey(traces[index]);
    if (!uniqueByKey.has(key)) uniqueByKey.set(key, []);
    uniqueByKey.get(key).push(index);
  }

  let javaVersion = null;
  let jarPath = null;
  const runResults = new Map();
  if (shapeProblems.size < traces.length && !options.shapeOnly) {
    javaVersion = verifyJava();
    jarPath = await acquireTlcJar();
  }

  let uniqueIndex = 0;
  for (const [key, indexes] of uniqueByKey.entries()) {
    const firstIndex = indexes[0];
    if (shapeProblems.has(firstIndex)) {
      runResults.set(key, {
        status: 'fail',
        structuralFailure: shapeProblems.get(firstIndex),
      });
      continue;
    }
    if (options.shapeOnly) {
      runResults.set(key, { status: 'shape-pass' });
      continue;
    }
    uniqueIndex += 1;
    const trace = traces[firstIndex];
    if (!options.quiet) console.log('Validating ' + trace.model + ' trace "' + trace.name + '" (' + trace.actions.length + ' actions, ' + indexes.length + ' copy/copies)...');
    const traceHasGroupLifecycle = hasGroupLifecycle && BUILDER_GROUP_TRACE_NAMES.has(trace.name);
    const traceJourneyCase = builderJourneyCaseForTrace(contract, trace);
    runResults.set(key, runTlc(jarPath, trace, uniqueIndex, traceHasGroupLifecycle, traceJourneyCase));
  }

  const modelCounts = {};
  const reportTraces = traces.map((trace, index) => {
    const key = traceKey(trace);
    const run = runResults.get(key);
    const modelCount = modelCounts[trace.model] ?? 0;
    modelCounts[trace.model] = modelCount + 1;
    const structuralFailure = shapeProblems.get(index) ?? (run?.structuralFailure ?? null);
    const actionCount = Array.isArray(trace.actions) ? trace.actions.length : 0;
    const expectedRunStatus = options.shapeOnly ? 'shape-pass' : 'pass';
    const status = !structuralFailure && run && run.status === expectedRunStatus
      ? expectedRunStatus
      : 'fail';
    const failure = structuralFailure ? { ...structuralFailure } : (run?.failure ? { ...run.failure } : null);
    const entry = {
      name: trace.name,
      model: trace.model,
      status,
      actionCount,
      ...(run?.structuralFailure
        ? {}
        : {
            tlc: run
              ? {
                  stateCount: run.stateCount ?? null,
                  distinctStateCount: run.distinctStateCount ?? null,
                  depth: run.depth ?? null,
                }
              : { stateCount: null, distinctStateCount: null, depth: null },
          }),
      ...(failure ? { failure } : {}),
    };
    const indexes = uniqueByKey.get(key);
    if (indexes.length > 1 && indexes[0] !== index) entry.duplicateOf = traces[indexes[0]].name;
    return entry;
  });

  const expectedPassingStatus = options.shapeOnly ? 'shape-pass' : 'pass';
  const invalid = reportTraces.filter((trace) => trace.status !== expectedPassingStatus);
  const unmodeledEventSummary = reportUnmodeledEvents(traces, contract.unmodeledEventPolicy);
  const report = {
    traceSha256,
    schemaVersion: 1,
    validationMode: options.shapeOnly ? 'shape-only' : 'formal-replay',
    formalReplayPerformed: !options.shapeOnly,
    sourceProfile: provenance.profile,
    sourceCommit: provenance.head,
    contractSourceCommit: payload.contractSourceCommit,
    sourceFingerprint: provenance.sourceFingerprint,
    traceCount: traces.length,
    uniqueTraceCount: uniqueByKey.size,
    validatedCount: reportTraces.length - invalid.length,
    invalidCount: invalid.length,
    modelCounts,
    coverage: {
      requiredModels: { missing: missingModels },
      requiredActions: { missing: missingActions },
      requiredActionsByTrace: { missing: missingActionsByTrace },
      requiredActionCountsByTrace: { mismatches: actionCountMismatchesByTrace },
      requiredActionMinimumsByTrace: { mismatches: actionCountMinimumMismatchesByTrace },
      requiredEvents: { missing: missingEvents },
    },
    traces: reportTraces,
    unmodeledEvents: unmodeledEventSummary,
    ...(options.allowPartial ? { partial: true } : {}),
  };
  const journeyCase = contract.journeyCase;
  if (journeyCase) {
    const cdaTrace = traces.find((trace) => trace.name === journeyCase.traceName);
    const traceEntry = reportTraces.find((trace) => trace.name === journeyCase.traceName);
    const replayStatus = options.shapeOnly
      ? 'not-run'
      : traceEntry?.status === 'pass' && countProblems.length === 0 && coverageProblems.length === 0 &&
          unmodeledEventSummary.unclassified.total === 0
        ? 'pass'
        : 'fail';
    report.builderJourneyTrace = {
      traceName: journeyCase.traceName,
      nativeCaseKey: journeyCase.workflow,
      tracePath: resolve(process.cwd(), options.traces),
      traceSha256,
      replayReportPath: options.output ? resolve(process.cwd(), options.output) : null,
      selfTestReportPath: null,
      actionLabels: cdaTrace?.actions?.map((action) => action.label) ?? [],
      requiredMutationControlNames: cdaTrace
        ? cdaRequiredMutationControlNames(cdaTrace, journeyCase)
        : [...journeyCase.requiredMutationControlNames],
      status: replayStatus === 'pass' ? 'pending-self-test' : 'fail',
      replayStatus,
      selfTestStatus: 'not-run',
      requestCoverage: cdaTrace ? cdaRequestCoverageReceipt(cdaTrace) : {
        observed: 0, modeled: 0, unmapped: 0, duplicates: 0,
      },
      requiredActionCountsByTrace: { mismatches: actionCountMismatchesByTrace },
      requiredActionMinimumsByTrace: { mismatches: actionCountMinimumMismatchesByTrace },
      unmodeledEvents: {
        total: unmodeledEventSummary.total,
        unclassified: { total: unmodeledEventSummary.unclassified.total },
      },
    };
  }
  if (javaVersion) report.tool = { name: 'TLC', version: TLC_VERSION, sha256: TLC_SHA256, java: javaVersion };
  const outputPath = options.output ? writeReport(options.output, report) : null;

  const traceReport = reportTraces.length >= 0 ? reportUnmodeledEvents(traces, contract.unmodeledEventPolicy) : null;
  const unclassifiedEvents = traceReport?.unclassified?.total ?? 0;
  if (!options.allowPartial && unclassifiedEvents > 0) {
    coverageProblems.push('unclassified implementation events: ' + JSON.stringify(traceReport.unclassified.byKind));
  }
  const globalProblems = [...countProblems, ...coverageProblems];
  if (options.allowPartial) {
    console.log('Partial mode: trace/schedule/finding counts and required coverage are reported, not enforced.');
    if (missingActions.length > 0) console.log('Missing required actions: ' + missingActions.join(', '));
    if (missingEvents.length > 0) console.log('Missing required events: ' + missingEvents.join(', '));
    if (missingModels.length > 0) console.log('Missing required models: ' + missingModels.join(', '));
    for (const [traceName, mismatches] of Object.entries(actionCountMismatchesByTrace)) {
      console.log('Required action counts for ' + traceName + ': ' + JSON.stringify(mismatches));
    }
    for (const [traceName, mismatches] of Object.entries(actionCountMinimumMismatchesByTrace)) {
      console.log('Required action minimums for ' + traceName + ': ' + JSON.stringify(mismatches));
    }
  }

  if (outputPath) console.log('Wrote trace validation report to ' + outputPath);
  console.log('Traces: ' + report.traceCount + ' total, ' + report.uniqueTraceCount + ' unique, ' +
    report.validatedCount + ' validated, ' + report.invalidCount + ' invalid.');
  if (!options.quiet) {
    for (const trace of reportTraces) {
      const tlc = trace.tlc && trace.tlc.stateCount !== null
        ? ' [' + trace.tlc.stateCount + ' generated / ' + trace.tlc.distinctStateCount + ' distinct, depth ' + trace.tlc.depth + ']'
        : '';
      console.log((trace.status === 'pass' ? 'PASS ' : 'FAIL ') + trace.model + ' "' + trace.name + '"' + tlc);
    }
  }

  if (globalProblems.length > 0 || invalid.length > 0) {
    for (const problem of globalProblems) console.error('ERROR ' + problem);
    for (const trace of invalid) {
      const failure = trace.failure ?? {};
      const location = failure.actionIndex === null || failure.actionIndex === undefined
        ? ''
        : ' action ' + failure.actionIndex + (failure.actionLabel ? ' ("' + failure.actionLabel + '")' : '');
      console.error('INVALID trace "' + trace.name + '" model=' + trace.model + location + ': ' + (failure.message ?? 'trace did not validate'));
      if (failure.outputTail) console.error('TLC output tail:\n' + failure.outputTail);
      if (failure.modulePath) console.error('Generated module: ' + failure.modulePath);
    }
    fail('Builder trace validation failed');
  }
  console.log(options.shapeOnly
    ? 'PASS Builder trace structural validation: all ' + report.traceCount + ' traces satisfy captured lifecycle projections; TLC replay skipped.'
    : 'PASS Builder trace validation: all ' + report.traceCount + ' traces match TLC model paths.');
}

export {
  builderColumnLabelForId,
  cdaGroupLifecycleSnapshotProblem,
  cdaRowExpansionCandidateResponseProblem,
  cdaGroupProjectionForTla,
  cdaCandidateIntentForTla,
  cdaLifecycleRecords,
  cdaLatePreviewObservationProblem,
  cdaMutatingRequestCoverageProblem,
  cdaRequestCoverageReceipt,
  collectCdaTokens,
  groupLifecycleShapeProblem,
  initialPreviewIdentityProblem,
  journeyGroupLifecycleConsistencyProblem,
  mountedGroupKeyLabelsProblem,
  mountedRelatedSourceProblem,
  relatedSourceSnapshotProblem,
  relatedSourceProjectionFromSteps,
};

const isDirectExecution = (() => {
  if (!process.argv[1]) return false;
  try {
    return realpathSync(resolve(process.argv[1])) === realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return false;
  }
})();

if (isDirectExecution) {
  main().catch((error) => {
    console.error('\nFAIL Builder trace validation: ' + error.message);
    process.exitCode = 1;
  });
}
