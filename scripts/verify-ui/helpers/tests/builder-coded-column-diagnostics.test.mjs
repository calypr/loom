import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { generatedJ01ConceptNDJSON } from '../../../loom-dev.mjs';
import {
  appendCapabilityCommandInvalidationProof,
  appendFrameSearchInvalidationProof,
  classifyCodedColumnDiagnostics,
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
