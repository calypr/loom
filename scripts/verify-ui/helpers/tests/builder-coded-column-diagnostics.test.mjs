import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { generatedJ01ConceptNDJSON } from '../../../loom-dev.mjs';
import { adjudicatePendingLifecycle, createReport, finishReport } from '../report.mjs';
import { applyInjectedFaultPolicy } from '../network-evidence.mjs';
import { finishCdaReport } from '../cda-fixtures.mjs';
import {
  appendCapabilityCommandInvalidationProof,
  appendFrameSearchInvalidationProof,
  classifyCodedColumnDiagnostics,
  frameSourceFailureCaptureRecord,
  normalizeFrameSourceRequest,
  previewHeaderMatches,
} from '../../workflows/builder-coded-source-column.mjs';

const fixtureIDs = [
  'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
  'dev-pair-001', 'dev-pair-002', 'dev-pair-003',
];
const fixtureHeightRows = [
  { id: 'dev-observation-001', value: '172.5' },
  { id: 'dev-observation-002', value: null },
  { id: 'dev-observation-003', value: '180' },
  { id: 'dev-pair-001', value: null },
  { id: 'dev-pair-002', value: null },
  { id: 'dev-pair-003', value: null },
];
const fixtureOracle = {
  observationCount: fixtureIDs.length,
  observationIDs: fixtureIDs,
  heightRows: fixtureHeightRows,
};

test('coded-column diagnostic classifier keeps capability aborts fatal without an action-bound proof', () => {
  const route = 'http://127.0.0.1:30008/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/construction-capabilities';
  const binding = {
    route,
    snapshotToken: 'snapshot-a',
    draftVersion: 1,
    draftDigest: 'digest-a',
    outputId: 'observations',
    stageId: 'stage-a',
  };
  const verifiedReplacement = {
    sequence: 12,
    status: 200,
    finished: true,
    responseMatches: true,
    failed: false,
    binding: { ...binding, snapshotToken: 'snapshot-b', draftVersion: 2, draftDigest: 'digest-b' },
  };
  const verifiedCancellation = {
    kind: 'network',
    method: 'POST',
    url: route,
    errorText: 'net::ERR_ABORTED',
    canceled: true,
    cancellationReason: 'superseded capability binding has a later successful replacement',
    sequence: 11,
    binding,
    replacement: verifiedReplacement,
  };
  const samePathUnmatchedAbort = {
    ...verifiedCancellation,
    canceled: false,
    cancellationReason: undefined,
    replacement: undefined,
  };
  const wrongPathAbort = {
    ...verifiedCancellation,
    url: route.replace('construction-capabilities', 'semantic-inventory'),
  };

  const result = classifyCodedColumnDiagnostics({
    network: [verifiedCancellation, samePathUnmatchedAbort, wrongPathAbort],
  });

  assert.equal(verifiedCancellation.canceled, true);
  assert.equal(verifiedCancellation.replacement.responseMatches, true);
  assert.notEqual(verifiedCancellation.binding.draftDigest, verifiedCancellation.replacement.binding.draftDigest);
  assert.deepEqual(result.cancelledReads, []);
  assert.deepEqual(result.unexpected, [verifiedCancellation, samePathUnmatchedAbort, wrongPathAbort]);
});

const capabilityInvalidationReport = () => {
  const capabilitiesURL = 'http://127.0.0.1:30008/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/construction-capabilities';
  const commandURL = 'http://127.0.0.1:30008/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/commands';
  const binding = {
    route: capabilitiesURL,
    snapshotToken: 'snapshot-a',
    draftVersion: 4,
    draftDigest: 'draft-v4',
    outputId: 'observations',
    stageId: 'source_projection',
  };
  const action = {
    id: 'action-rename', label: 'save edited Height column label', status: 'passed',
    startedAtMs: 200, endedAtMs: 500, startedAtEpochMs: 1000, finishedAtEpochMs: 1400,
  };
  const failure = {
    kind: 'network', method: 'POST', url: capabilitiesURL, rawURL: capabilitiesURL,
    errorText: 'net::ERR_ABORTED', playwrightRequestId: 'request-capability-3', sequence: 3,
    binding,
    requestTimeline: { requestStartedMs: 100, failedAtMs: 520, durationMs: 420,
      action: { id: 'action-apply', label: 'add and save the Height coded source column' } },
    triggerAction: 'add and save the Height coded source column',
  };
  const assertion = { name: 'edited Height column and exact values survive Builder reload' };
  const report = {
    scenario: 'builder-coded-source-column', case: 'coded-source-column',
    target: { fixtureOracle },
    network: [failure],
    actions: [action],
    assertions: [{
      name: assertion.name, status: 'passed',
      evidence: {
        editedLabel: 'Fixture Height ABC',
        expectedHeightRows: fixtureHeightRows,
        actualHeightRows: fixtureHeightRows,
        headers: ['OBSERVATION ID', 'FIXTURE HEIGHT ABC'],
      },
    }],
  };
  appendCapabilityCommandInvalidationProof(report, {
    networkFailure: failure,
    commandType: 'UPDATE_COLUMN',
    editedLabel: 'Fixture Height ABC',
    mutation: {
      request: {
        url: commandURL, project: 'project-a', explorerId: 'explorer-a',
        body: {
          commandId: 'command-rename', snapshotToken: 'snapshot-a',
          expectedDraftVersion: 4, expectedDraftDigest: 'draft-v4',
          commands: [{ type: 'UPDATE_COLUMN', outputId: 'observations', column: 'height-column',
            columnValue: { label: 'Fixture Height ABC' } }],
        },
      },
      response: { status: 200, completedAtEpochMs: 1300,
        body: { commandId: 'command-rename', draftVersion: 5, draftDigest: 'draft-v5' } },
    },
    action,
    assertion,
  });
  return report;
};

const removalInvalidationReport = () => {
  const report = capabilityInvalidationReport();
  const proof = report.expectedObsolete[0];
  const actionLabel = 'remove saved Height coded column';
  proof.mutation.commandType = 'REMOVE_COLUMN';
  proof.mutation.request.body.commands[0] = {
    type: 'REMOVE_COLUMN', outputId: 'observations', column: 'height-column',
  };
  proof.action.label = actionLabel;
  proof.assertion = {
    name: 'native Remove column restores the six Observation ID rows',
    reloadName: 'removed coded column stays absent after Builder reload',
  };
  report.actions[0].label = actionLabel;
  report.assertions = [
    {
      name: proof.assertion.name, status: 'passed',
      evidence: { expectedIDs: fixtureIDs, removedIDs: fixtureIDs, headers: ['OBSERVATION ID'] },
    },
    {
      name: proof.assertion.reloadName, status: 'passed',
      evidence: { expectedIDs: fixtureIDs, finalIDs: fixtureIDs, headers: ['OBSERVATION ID'] },
    },
  ];
  return report;
};

test('production capability proof constructor emits the shared request identity and accepted timestamps', () => {
  const report = capabilityInvalidationReport();
  const failure = report.network[0];
  const proof = report.expectedObsolete[0];
  assert.equal(proof.failedRequest.playwrightRequestId, failure.playwrightRequestId);
  assert.equal(proof.failedRequest.requestStartedAtMs, failure.requestTimeline.requestStartedMs);
  assert.equal(proof.failedRequest.failedAtMs, failure.requestTimeline.failedAtMs);
  assert.equal(Object.hasOwn(proof.failedRequest, 'sequence'), false);
  failure.sequence = 47_002;
  const result = classifyCodedColumnDiagnostics(report);
  assert.equal(result.cancelledReads.length, 1);
  assert.equal(result.cancelledReads[0].expectedObsolete, true);
  assert.equal(result.cancelledReads[0].obsolescenceEvidence.kind, 'capability-command-invalidation');
  assert.deepEqual(result.unexpected, []);
  assert.deepEqual(report.expectedObsoleteReads, result.cancelledReads);
});

test('valid REMOVE_COLUMN proof requires exact nonempty rows and rendered header evidence before and after reload', () => {
  const report = removalInvalidationReport();
  const result = classifyCodedColumnDiagnostics(report);
  assert.equal(result.cancelledReads.length, 1);
  assert.deepEqual(result.unexpected, []);
});

test('UPDATE_COLUMN abort stays fatal when passed evidence omits or empties Height rows or headers', () => {
  const mutations = [
    ['missing independent Height rows', evidence => { delete evidence.expectedHeightRows; }],
    ['undefined rendered Height rows', evidence => { evidence.actualHeightRows = undefined; }],
    ['empty independent Height rows', evidence => { evidence.expectedHeightRows = []; }],
    ['empty rendered Height rows', evidence => { evidence.actualHeightRows = []; }],
    ['truncated but equal Height rows', evidence => {
      evidence.expectedHeightRows = fixtureHeightRows.slice(0, 5);
      evidence.actualHeightRows = fixtureHeightRows.slice(0, 5);
    }],
    ['missing rendered headers', evidence => { delete evidence.headers; }],
    ['empty rendered headers', evidence => { evidence.headers = []; }],
  ];
  for (const [label, mutate] of mutations) {
    const report = capabilityInvalidationReport();
    mutate(report.assertions[0].evidence);
    const result = classifyCodedColumnDiagnostics(report);
    assert.deepEqual(result.cancelledReads, [], label);
    assert.deepEqual(result.unexpected, report.network, label);
  }
});

test('REMOVE_COLUMN abort stays fatal when either removal or reloaded rows and headers are absent or empty', () => {
  const mutations = [
    ['missing expected removed IDs', report => { delete report.assertions[0].evidence.expectedIDs; }],
    ['undefined actual removed IDs', report => { report.assertions[0].evidence.removedIDs = undefined; }],
    ['empty expected removed IDs', report => { report.assertions[0].evidence.expectedIDs = []; }],
    ['empty actual removed IDs', report => { report.assertions[0].evidence.removedIDs = []; }],
    ['truncated but equal removed IDs', report => {
      report.assertions[0].evidence.expectedIDs = fixtureIDs.slice(0, 5);
      report.assertions[0].evidence.removedIDs = fixtureIDs.slice(0, 5);
    }],
    ['missing removal headers', report => { delete report.assertions[0].evidence.headers; }],
    ['empty removal headers', report => { report.assertions[0].evidence.headers = []; }],
    ['missing persisted expected IDs', report => { delete report.assertions[1].evidence.expectedIDs; }],
    ['empty persisted final IDs', report => { report.assertions[1].evidence.finalIDs = []; }],
    ['truncated but equal reloaded IDs', report => {
      report.assertions[1].evidence.expectedIDs = fixtureIDs.slice(0, 5);
      report.assertions[1].evidence.finalIDs = fixtureIDs.slice(0, 5);
    }],
    ['missing persisted headers', report => { delete report.assertions[1].evidence.headers; }],
    ['empty persisted headers', report => { report.assertions[1].evidence.headers = []; }],
  ];
  for (const [label, mutate] of mutations) {
    const report = removalInvalidationReport();
    mutate(report);
    const result = classifyCodedColumnDiagnostics(report);
    assert.deepEqual(result.cancelledReads, [], label);
    assert.deepEqual(result.unexpected, report.network, label);
  }
});

test('capability invalidation proof rejects wrong owner/output, failed or unchanged receipts, early aborts, and unrelated actions', () => {
  const mutations = [
    ['wrong project', report => { report.expectedObsolete[0].mutation.request.url =
      report.expectedObsolete[0].mutation.request.url.replace('/project-a/', '/project-b/'); }],
    ['wrong explorer', report => { report.expectedObsolete[0].mutation.request.url =
      report.expectedObsolete[0].mutation.request.url.replace('/explorer-a/', '/explorer-b/'); }],
    ['wrong output', report => { report.expectedObsolete[0].mutation.request.body.commands[0].outputId = 'other-output'; }],
    ['failed command receipt', report => { report.expectedObsolete[0].mutation.response.status = 500; }],
    ['receipt outside the successful UI action', report => {
      report.expectedObsolete[0].mutation.response.completedAtEpochMs = 1500;
    }],
    ['unchanged version and digest', report => {
      report.expectedObsolete[0].mutation.response.body.draftVersion = 4;
      report.expectedObsolete[0].mutation.response.body.draftDigest = 'draft-v4';
    }],
    ['abort before mutation completed', report => {
      report.network[0].requestTimeline.failedAtMs = 450;
      report.expectedObsolete[0].failedRequest.failedAtMs = 450;
    }],
    ['unrelated action', report => {
      report.expectedObsolete[0].action.label = 'open table Columns menu';
    }],
    ['unrelated request identity', report => {
      report.expectedObsolete[0].failedRequest.playwrightRequestId = 'request-other';
    }],
    ['duplicate shared request identity', report => {
      report.network.push({ ...report.network[0], sequence: 48_003 });
    }],
    ['missing proof request start timestamp', report => {
      delete report.expectedObsolete[0].failedRequest.requestStartedAtMs;
    }],
    ['nonfinite proof failure timestamp', report => {
      report.expectedObsolete[0].failedRequest.failedAtMs = Number.NaN;
    }],
    ['missing shared request start timestamp', report => {
      delete report.network[0].requestTimeline.requestStartedMs;
    }],
    ['missing action start timestamp', report => { report.actions[0].startedAtMs = undefined; }],
    ['nonfinite action completion timestamp', report => { report.actions[0].finishedAtEpochMs = Number.NaN; }],
    ['missing mutation receipt completion timestamp', report => {
      delete report.expectedObsolete[0].mutation.response.completedAtEpochMs;
    }],
  ];
  for (const [label, mutate] of mutations) {
    const report = capabilityInvalidationReport();
    mutate(report);
    const result = classifyCodedColumnDiagnostics(report);
    assert.deepEqual(result.cancelledReads, [], label);
    assert.deepEqual(result.unexpected, report.network, label);
  }
});

test('a malformed action-bound proof stays fatal even when generic replacement metadata is present', () => {
  const report = capabilityInvalidationReport();
  const failure = report.network[0];
  failure.canceled = true;
  failure.cancellationReason = 'superseded capability binding has a later successful replacement';
  failure.replacement = {
    sequence: 4, status: 200, finished: true, responseMatches: true, failed: false,
    binding: { ...failure.binding, snapshotToken: 'snapshot-new', draftVersion: 5, draftDigest: 'digest-new' },
  };
  report.expectedObsolete[0].mutation.request.url =
    report.expectedObsolete[0].mutation.request.url.replace('/project-a/', '/project-b/');

  const result = classifyCodedColumnDiagnostics(report);
  assert.deepEqual(result.cancelledReads, []);
  assert.deepEqual(result.unexpected, [failure]);
});

const frameSearchInvalidationReport = () => {
  const sourceURL = 'http://127.0.0.1:30008/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/frame-source-options';
  const commandURL = 'http://127.0.0.1:30008/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/commands';
  const action = {
    id: 'action-search', label: 'search native Observation framing choices', status: 'passed',
    startedAtMs: 1100, endedAtMs: 1300,
  };
  const source = {
    choiceId: 'choice-height', resourceType: 'Observation', route: [], sourcePath: 'code',
    valuePath: 'valueQuantity.value', exampleConcept: 'Height', observedOccurrences: 6,
  };
  const failure = {
    kind: 'network', method: 'POST', url: sourceURL, rawURL: sourceURL, errorText: 'net::ERR_ABORTED',
    playwrightRequestId: 'request-source-1', triggerAction: 'browse coded source values',
    requestDetails: { requestId: 'frame-source-options-initial', outputId: 'observations' },
    requestTimeline: { requestStartedMs: 1020, failedAtMs: 1150, durationMs: 130,
      action: { id: 'action-browse', label: 'browse coded source values' } },
  };
  const report = {
    scenario: 'builder-coded-source-column', case: 'coded-source-column',
    network: [failure], actions: [action],
    assertions: [{
      name: 'native Coded values controls save the direct Observation Height frame', status: 'passed',
      evidence: { sourceChoiceId: source.choiceId, frameId: 'frame-a', savedFrameText: 'Observation code values · On each Observation record' },
    }],
  };
  appendFrameSearchInvalidationProof(report, {
    networkFailure: failure,
    failedRequestCapture: {
      method: 'POST', url: sourceURL, startedAtMs: 20, failedAtMs: 150,
      body: { requestId: 'frame-source-options-initial', query: '', outputId: 'observations', snapshotToken: 'snapshot-a' },
    },
    replacementRequestCapture: {
      url: sourceURL, project: 'project-a', explorerId: 'explorer-a', startedAtMs: 120,
      body: { query: 'Observation', outputId: 'observations', snapshotToken: 'snapshot-a' },
    },
    replacementResponse: {
      status: 200, finished: true,
      body: { query: 'Observation', outputId: 'observations', snapshotToken: 'snapshot-a', sources: [source] },
    },
    action,
    selection: {
      choiceId: source.choiceId, source,
      setFrameSource: {
        request: { url: commandURL, body: { snapshotToken: 'snapshot-a', commands: [
          { type: 'SET_FRAME_SOURCE', outputId: 'observations', frameChoiceId: source.choiceId },
        ] } },
        response: { status: 200 },
      },
    },
    assertion: report.assertions[0],
  });
  return report;
};

test('frame-source blank-query abort requires its exact successful same-owner Observation response and selected Height source', () => {
  const report = frameSearchInvalidationReport();
  const proof = report.expectedObsolete[0];
  assert.equal(proof.clockAlignment.localToReportOffsetMs, 1000);
  assert.equal(proof.replacement.request.localStartedAtMs, 120);
  assert.equal(proof.replacement.request.requestStartedMs, 1120);
  assert(proof.replacement.request.localStartedAtMs < report.actions[0].startedAtMs,
    'local capture time intentionally precedes the shared-clock action timestamp');
  const result = classifyCodedColumnDiagnostics(report);
  assert.equal(result.cancelledReads.length, 1);
  assert.equal(result.cancelledReads[0].expectedObsolete, true);
  assert.deepEqual(result.unexpected, []);

  const mutations = [
    ['wrong response project', proof => { proof.replacement.request.project = 'project-b'; }],
    ['wrong response origin', proof => { proof.replacement.request.url = proof.replacement.request.url.replace('127.0.0.1:30008', '127.0.0.1:30009'); }],
    ['wrong response output', proof => { proof.replacement.response.body.outputId = 'other-output'; }],
    ['failed replacement response', proof => { proof.replacement.response.status = 503; }],
    ['mixed-clock replacement timestamp', proof => {
      proof.replacement.request.requestStartedMs = proof.replacement.request.localStartedAtMs;
    }],
    ['incorrect local-to-report clock alignment', proof => { proof.clockAlignment.localToReportOffsetMs = 0; }],
    ['different selected choice', proof => { proof.selection.choiceId = 'choice-other'; }],
    ['wrong source value path', proof => { proof.selection.source.valuePath = 'effectiveDateTime'; }],
  ];
  for (const [label, mutate] of mutations) {
    const invalid = frameSearchInvalidationReport();
    mutate(invalid.expectedObsolete[0]);
    const rejected = classifyCodedColumnDiagnostics(invalid);
    assert.deepEqual(rejected.cancelledReads, [], label);
    assert.deepEqual(rejected.unexpected, invalid.network, label);
  }
});

test('rendered uppercase coded-column headers match labels after semantic normalization', () => {
  assert.equal(previewHeaderMatches('FIXTURE HEIGHT ABC (KG)', 'Fixture Height ABC'), true);
  assert.equal(previewHeaderMatches('OBSERVATION ID', 'Observation ID'), true);
  assert.equal(previewHeaderMatches('OTHER HEIGHT ABC (KG)', 'Fixture Height ABC'), false);
});

test('coded-source-column fixture keeps the six-row independent oracle without global J01 expansion', () => {
  const repositoryRoot = fileURLToPath(new URL('../../../../', import.meta.url));
  const fixtureDir = join(repositoryRoot, 'testdata', 'builder-coded-source-column');
  const originalDir = join(repositoryRoot, 'testdata', 'devloop-fixture');
  const patients = readFileSync(join(fixtureDir, 'Patient.ndjson'));
  const observations = readFileSync(join(fixtureDir, 'Observation.ndjson'));
  assert.deepEqual(patients, readFileSync(join(originalDir, 'Patient.ndjson')));
  assert.deepEqual(observations, readFileSync(join(originalDir, 'Observation.ndjson')));
  assert.deepEqual(patients.toString('utf8').trim().split(/\r?\n/).map(line => JSON.parse(line).id), [
    'dev-patient-001', 'dev-patient-002',
  ]);
  assert.deepEqual(observations.toString('utf8').trim().split(/\r?\n/).map(line => JSON.parse(line).id), [
    'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
    'dev-pair-001', 'dev-pair-002', 'dev-pair-003',
  ]);
  assert.equal(generatedJ01ConceptNDJSON(fixtureDir), undefined);
});

const retainedCodedReport = () => {
  const project = 'loom_dev_verify_mv0k3tqk-5211624';
  const explorerId = 'verify-qk-5211624-coded-source';
  const outputId = 'out_55eb6102c01a77480b5a05a5';
  const capabilitiesURL = `http://127.0.0.1:30008/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/construction-capabilities`;
  const commandsURL = `http://127.0.0.1:30008/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/commands`;
  const snapshotToken = 'sha256:636914970357cfc8758a44fb2c064b6951f87d51fed53942e06d9366ae0f3cac';
  const generatedRename = capabilityInvalidationReport();
  const generatedRemove = removalInvalidationReport();
  const report = createReport({
    scenario: 'builder-coded-source-column', caseName: 'coded-source-column',
    target: { fixtureOracle },
  });
  const renameRecord = generatedRename.network[0];
  const removeRecord = generatedRemove.network[0];
  const renameProof = generatedRename.expectedObsolete[0];
  const removeProof = generatedRemove.expectedObsolete[0];
  const renameAction = generatedRename.actions[0];
  const removeAction = generatedRemove.actions[0];
  const renameAssertion = generatedRename.assertions[0];
  const removeAssertion = generatedRemove.assertions[0];
  const reloadAssertion = generatedRemove.assertions[1];

  Object.assign(renameRecord, {
    url: capabilitiesURL, rawURL: capabilitiesURL, playwrightRequestId: 'request-148', sequence: 3,
    requestTimeline: { requestStartedMs: 5006, failedAtMs: 5677, durationMs: 671, action: null },
    binding: { route: capabilitiesURL, snapshotToken, draftVersion: 4,
      draftDigest: 'sha256:2e75fb1a2b30c890e52fda598cf34727cdbb3e1d23ec59bb4ff777a6def5169d',
      outputId, stageId: 'source_projection' },
  });
  Object.assign(removeRecord, {
    url: capabilitiesURL, rawURL: capabilitiesURL, playwrightRequestId: 'request-271', sequence: 5,
    requestTimeline: { requestStartedMs: 6960, failedAtMs: 7718, durationMs: 759, action: null },
    binding: { route: capabilitiesURL, snapshotToken, draftVersion: 5,
      draftDigest: 'sha256:8060c342d26f290eb89dedf3bcdccf68aeccd9a7ea294ead94064a26ca229407',
      outputId, stageId: 'source_projection' },
  });

  Object.assign(renameAction, { id: 'action-18', label: 'save edited Height column label',
    startedAtMs: 5552, endedAtMs: 5668, startedAtEpochMs: 1791525641828,
    finishedAtEpochMs: 1791525641944 });
  Object.assign(removeAction, { id: 'action-21', label: 'remove saved Height coded column',
    startedAtMs: 7580, endedAtMs: 7710, startedAtEpochMs: 1791525643856,
    finishedAtEpochMs: 1791525643986 });
  renameProof.failedRequest = { playwrightRequestId: 'request-148', requestStartedAtMs: 5006,
    failedAtMs: 5677, binding: renameRecord.binding };
  removeProof.failedRequest = { playwrightRequestId: 'request-271', requestStartedAtMs: 6960,
    failedAtMs: 7718, binding: removeRecord.binding };
  renameProof.action = { ...renameAction };
  removeProof.action = { ...removeAction };
  renameProof.mutation.editedLabel = 'Fixture Height 211624';
  removeProof.mutation.editedLabel = 'Fixture Height 211624';
  renameAssertion.evidence.editedLabel = 'Fixture Height 211624';
  renameAssertion.evidence.headers = ['OBSERVATION ID', 'FIXTURE HEIGHT 211624'];
  renameProof.mutation.request = { url: commandsURL, project, explorerId, body: {
    commandId: '590def5e-8183-440e-9dd3-b0f3d843d9e2', snapshotToken,
    expectedDraftVersion: 4,
    expectedDraftDigest: 'sha256:2e75fb1a2b30c890e52fda598cf34727cdbb3e1d23ec59bb4ff777a6def5169d',
    commands: [{ type: 'UPDATE_COLUMN', outputId, column: 'col_fb0f5fa7b82a09ae92120682',
      columnValue: { label: 'Fixture Height 211624' } }],
  } };
  renameProof.mutation.response = { status: 200, completedAtEpochMs: 1791525641944, body: {
    commandId: '590def5e-8183-440e-9dd3-b0f3d843d9e2', draftVersion: 5,
    draftDigest: 'sha256:8060c342d26f290eb89dedf3bcdccf68aeccd9a7ea294ead94064a26ca229407',
  } };
  removeProof.mutation.request = { url: commandsURL, project, explorerId, body: {
    commandId: '443e401e-b83f-4ecf-9ae7-a8220283b2a5', snapshotToken,
    expectedDraftVersion: 5,
    expectedDraftDigest: 'sha256:8060c342d26f290eb89dedf3bcdccf68aeccd9a7ea294ead94064a26ca229407',
    commands: [{ type: 'REMOVE_COLUMN', outputId, column: 'col_fb0f5fa7b82a09ae92120682' }],
  } };
  removeProof.mutation.response = { status: 200, completedAtEpochMs: 1791525643986, body: {
    commandId: '443e401e-b83f-4ecf-9ae7-a8220283b2a5', draftVersion: 6,
    draftDigest: 'sha256:15b5914227cb29bd120eca02cc67fc3924dd9c1cfcc6a29beafb2d6b48b4b870',
  } };

  const frameSearchAbort = {
    kind: 'network', method: 'POST',
    url: `http://127.0.0.1:30008/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/frame-source-options`,
    resourceType: 'fetch', errorText: 'net::ERR_ABORTED', playwrightRequestId: 'request-126',
    requestDetails: { requestId: 'frame-source-options-8612512d-9a0f-4439-a95f-7b7d42f39b31',
      draftVersion: null, draftDigest: null, outputId, stageId: null },
    requestTimeline: { requestStartedMs: 2077, failedAtMs: 2206, durationMs: 129,
      action: { id: 'action-8', label: 'browse coded source values' }, mainFrameNavigations: [] },
    triggerAction: 'browse coded source values',
  };
  report.network = [frameSearchAbort, renameRecord, removeRecord];
  report.actions = [renameAction, removeAction];
  report.assertions = [renameAssertion, removeAssertion, reloadAssertion];
  report.expectedObsolete = [renameProof, removeProof];
  return report;
};

test('retained request-126 diagnostics preserve exact frame-search request binding and timing without excusing the abort', () => {
  const report = retainedCodedReport();
  const failed = report.network[0];
  const normalizedBody = normalizeFrameSourceRequest({
    outputId: failed.requestDetails.outputId,
    snapshotToken: 'sha256:retained-frame-search-snapshot',
  }, failed.requestDetails.requestId);
  const capture = {
    kind: 'frame-source-options', method: 'POST', url: failed.url,
    project: 'loom_dev_verify_mv0k3tqk-5211624', explorerId: 'verify-qk-5211624-coded-source',
    body: normalizedBody,
    errorText: 'net::ERR_ABORTED', startedAtMs: 100, failedAtMs: 229, durationMs: 129,
  };
  const evidence = frameSourceFailureCaptureRecord(capture, failed);
  report.target.frameSourceFailureCaptures = [evidence];
  assert.deepEqual(evidence, {
    playwrightRequestId: 'request-126',
    method: 'POST',
    route: failed.url,
    project: 'loom_dev_verify_mv0k3tqk-5211624',
    explorerId: 'verify-qk-5211624-coded-source',
    requestId: 'frame-source-options-8612512d-9a0f-4439-a95f-7b7d42f39b31',
    requestIdSource: 'header',
    query: '',
    queryFieldPresent: false,
    outputId: 'out_55eb6102c01a77480b5a05a5',
    snapshotToken: 'sha256:retained-frame-search-snapshot',
    errorText: 'net::ERR_ABORTED',
    requestStartedMs: 2077,
    failedAtMs: 2206,
    localRequestStartedMs: 100,
    localFailedAtMs: 229,
    durationMs: 129,
  });
  const classified = classifyCodedColumnDiagnostics(report);
  assert(classified.unexpected.includes(failed), 'Request 126 stays fatal without independently validated replacement proof.');
  finishReport(report);
  assert(report.errors.some(error => error.playwrightRequestId === 'request-126' && error.errorText === 'net::ERR_ABORTED'));
});

test('frame-source capture normalizes header request IDs and omitted empty queries while preserving wire presence', () => {
  assert.deepEqual(normalizeFrameSourceRequest({ outputId: 'out-a', snapshotToken: 'snapshot-a' }, 'request-from-header'), {
    requestId: 'request-from-header', requestIdSource: 'header', query: '', queryFieldPresent: false,
    outputId: 'out-a', snapshotToken: 'snapshot-a',
  });
  assert.deepEqual(normalizeFrameSourceRequest({ requestId: 'request-from-body', outputId: 'out-a',
    snapshotToken: 'snapshot-a', query: 'Observation' }, 'different-header-id'), {
    requestId: 'request-from-body', requestIdSource: 'body', query: 'Observation', queryFieldPresent: true,
    outputId: 'out-a', snapshotToken: 'snapshot-a',
  });
});

test('finishReport consumes only the exact validated request-148 and request-271 retained proofs', () => {
  const report = retainedCodedReport();
  const classified = classifyCodedColumnDiagnostics(report);
  assert.deepEqual(classified.cancelledReads.map(record => record.playwrightRequestId), ['request-148', 'request-271']);
  assert.deepEqual(classified.unexpected.map(record => record.playwrightRequestId), ['request-126']);

  finishReport(report);
  assert.deepEqual(report.errors.filter(error => error.errorText === 'net::ERR_ABORTED')
    .map(error => error.playwrightRequestId), ['request-126']);
  assert.equal(report.assertions.at(-1).name, 'no unexpected network, module, or browser errors');
  assert.equal(report.assertions.at(-1).evidence.count, 1);
  report.lifecycle = { status: 'pending-final-adjudication' };
  adjudicatePendingLifecycle(report);
  assert.equal(report.lifecycle.status, 'failed');
  assert.deepEqual(report.lifecycle.failure.unexpectedNetwork.map(record => record.url), [report.network[0].url]);
});

test('CDA network projection clones validated records and strips rawURL without losing exact coded proof ownership', () => {
  const report = retainedCodedReport();
  const originalRecords = report.network;
  const classified = classifyCodedColumnDiagnostics(report);
  assert.deepEqual(classified.cancelledReads.map(record => record.playwrightRequestId), ['request-148', 'request-271']);
  assert(report.expectedObsoleteReads.every(record => record.rawURL === record.url));

  report.network = applyInjectedFaultPolicy(report.network, []);
  assert.notEqual(report.network, originalRecords, 'Production fault projection replaces the network array.');
  assert.notEqual(report.network[1], originalRecords[1], 'Production fault projection shallow-clones each record.');
  assert(report.network.every(record => !Object.hasOwn(record, 'rawURL')),
    'Production fault projection removes rawURL from finalized network records.');
  const projectedNetwork = report.network;
  finishCdaReport(report);
  assert.equal(report.network, projectedNetwork, 'CDA finish restores the projected network after final status calculation.');
  assert.deepEqual(report.errors.filter(error => error.errorText === 'net::ERR_ABORTED')
    .map(error => error.playwrightRequestId), ['request-126']);
});

test('finalization allows only URL-identical rawURL enrichment after proof validation', () => {
  const enriched = retainedCodedReport();
  enriched.network.slice(1).forEach(record => { delete record.rawURL; });
  classifyCodedColumnDiagnostics(enriched);
  enriched.network.slice(1).forEach(record => { record.rawURL = record.url; });
  finishReport(enriched);
  assert.deepEqual(enriched.errors.filter(error => error.errorText === 'net::ERR_ABORTED')
    .map(error => error.playwrightRequestId), ['request-126']);

  const mismatched = retainedCodedReport();
  classifyCodedColumnDiagnostics(mismatched);
  mismatched.network[1].rawURL = `${mismatched.network[1].url}?unmatched=1`;
  finishReport(mismatched);
  assert.deepEqual(mismatched.errors.filter(error => error.errorText === 'net::ERR_ABORTED')
    .map(error => error.playwrightRequestId), ['request-126', 'request-148']);
});

test('finishReport rejects forged read lists, missing proofs, and mismatched request IDs', () => {
  const forgedList = retainedCodedReport();
  forgedList.expectedObsolete = [];
  forgedList.expectedObsoleteReads = forgedList.network.slice(1);
  forgedList.network.slice(1).forEach(record => {
    record.canceled = true;
    record.expectedObsolete = true;
  });
  finishReport(forgedList);
  assert.deepEqual(forgedList.errors.filter(error => error.errorText === 'net::ERR_ABORTED')
    .map(error => error.playwrightRequestId), ['request-126', 'request-148', 'request-271']);

  const mismatchedIdentity = retainedCodedReport();
  classifyCodedColumnDiagnostics(mismatchedIdentity);
  mismatchedIdentity.expectedObsolete[0].failedRequest.playwrightRequestId = 'forged-request-id';
  finishReport(mismatchedIdentity);
  assert.deepEqual(mismatchedIdentity.errors.filter(error => error.errorText === 'net::ERR_ABORTED')
    .map(error => error.playwrightRequestId), ['request-126', 'request-148']);

  const missingProof = retainedCodedReport();
  classifyCodedColumnDiagnostics(missingProof);
  missingProof.expectedObsolete = missingProof.expectedObsolete.filter(proof =>
    proof.failedRequest.playwrightRequestId !== 'request-271');
  finishReport(missingProof);
  assert.deepEqual(missingProof.errors.filter(error => error.errorText === 'net::ERR_ABORTED')
    .map(error => error.playwrightRequestId), ['request-126', 'request-271']);
});

test('finishReport rejects proof or request-field mutations made after validation', () => {
  const cases = [
    ['changed command', report => {
      report.expectedObsolete[0].mutation.request.body.commands[0].type = 'REMOVE_COLUMN';
    }, 'request-148'],
    ['changed command body', report => {
      report.expectedObsolete[0].mutation.request.body.commands[0].outputId = 'other-output';
    }, 'request-148'],
    ['changed request scope', report => {
      report.expectedObsolete[0].mutation.request.explorerId = 'other-explorer';
    }, 'request-148'],
    ['changed draft digest', report => {
      report.expectedObsolete[0].mutation.request.body.expectedDraftDigest = 'sha256:forged-digest';
    }, 'request-148'],
    ['changed network binding', report => {
      report.network.find(record => record.playwrightRequestId === 'request-271').binding.draftDigest = 'sha256:forged-binding';
    }, 'request-271'],
  ];
  for (const [label, mutate, expectedFatalID] of cases) {
    const report = retainedCodedReport();
    classifyCodedColumnDiagnostics(report);
    mutate(report);
    finishReport(report);
    const fatalIDs = report.errors.filter(error => error.errorText === 'net::ERR_ABORTED')
      .map(error => error.playwrightRequestId);
    assert(fatalIDs.includes('request-126'), `${label}: the unsupported frame-search abort must remain fatal`);
    assert(fatalIDs.includes(expectedFatalID), `${label}: the mutated validated record must become fatal`);
  }
});
