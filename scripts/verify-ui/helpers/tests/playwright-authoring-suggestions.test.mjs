import assert from 'node:assert/strict';
import test from 'node:test';
import { classifySuggestionDiagnostics } from '../playwright-authoring-suggestions.mjs';

const target = {
  uiUrl: 'http://127.0.0.1:30008',
  fixtureProject: 'loom_dev_verify_run-1',
};
const explorer = 'explorer-new';
const suggestionsURL = `${target.uiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}` +
  `/explorers/${encodeURIComponent(explorer)}/authoring/v2/suggestions`;

const injectedSuggestionFailure = () => ({
  kind: 'network',
  method: 'POST',
  url: suggestionsURL,
  rawURL: suggestionsURL,
  errorText: 'net::ERR_FAILED',
  injectedFault: true,
  injectedAction: 'abort',
  injectedRequestId: 'suggestions-run-1',
  playwrightRequestId: 'playwright-request-9',
  requestDetails: { requestId: 'suggestions-run-1' },
});

test('suggestion abort classifier suppresses one exact correlated console diagnostic only', () => {
  const failure = injectedSuggestionFailure();
  const exactAbortConsole = {
    kind: 'console-error',
    text: 'Failed to load resource: net::ERR_FAILED',
    location: suggestionsURL,
    rawLocation: suggestionsURL,
  };
  const samePathUnrelatedConsole = {
    kind: 'console-error',
    text: 'Unrelated renderer error',
    location: suggestionsURL,
    rawLocation: suggestionsURL,
  };
  const wrongPathSameMessage = {
    kind: 'console-error',
    text: 'Failed to load resource: net::ERR_FAILED',
    location: `${target.uiUrl}/api/v1/projects/other/explorers/${explorer}/authoring/v2/suggestions`,
    rawLocation: `${target.uiUrl}/api/v1/projects/other/explorers/${explorer}/authoring/v2/suggestions`,
  };
  const unmatchedAbort = {
    kind: 'network', method: 'POST', url: suggestionsURL, rawURL: suggestionsURL,
    errorText: 'net::ERR_ABORTED', requestDetails: { requestId: 'unmatched' },
  };
  const report = {
    target: { explorer },
    network: [failure, exactAbortConsole, samePathUnrelatedConsole, wrongPathSameMessage,
      unmatchedAbort],
  };

  const result = classifySuggestionDiagnostics(report, target, failure);

  assert.equal(result.expectedAbortConsoleError, exactAbortConsole);
  assert.equal(exactAbortConsole.kind, 'network');
  assert.equal(exactAbortConsole.observedAs, 'console-error');
  assert.equal(exactAbortConsole.injectedRequestId, failure.injectedRequestId);
  assert.deepEqual(result.unexpected, [samePathUnrelatedConsole, wrongPathSameMessage,
    unmatchedAbort]);
});

test('suggestion abort classifier fails closed when the exact console diagnostic is duplicated', () => {
  const failure = injectedSuggestionFailure();
  const exact = () => ({
    kind: 'console-error',
    text: 'Failed to load resource: net::ERR_FAILED',
    location: suggestionsURL,
    rawLocation: suggestionsURL,
  });
  const first = exact();
  const duplicate = exact();
  const report = { target: { explorer }, network: [failure, first, duplicate] };

  const result = classifySuggestionDiagnostics(report, target, failure);

  assert.equal(result.expectedAbortConsoleError, undefined);
  assert.deepEqual(result.unexpected, [first, duplicate]);
});

test('suggestion abort classifier refuses console attribution when another failed request shares the URL', () => {
  const failure = injectedSuggestionFailure();
  const exactAbortConsole = {
    kind: 'console-error',
    text: 'Failed to load resource: net::ERR_FAILED',
    location: suggestionsURL,
    rawLocation: suggestionsURL,
  };
  const competingRequest = {
    ...injectedSuggestionFailure(),
    injectedRequestId: 'other-request',
    playwrightRequestId: 'playwright-request-other',
    requestDetails: { requestId: 'other-request' },
  };
  const report = { target: { explorer }, network: [failure, competingRequest, exactAbortConsole] };

  const result = classifySuggestionDiagnostics(report, target, failure);

  assert.equal(result.expectedAbortConsoleError, undefined);
  assert.deepEqual(result.competingSameRequestURLFailures, [competingRequest]);
  assert.deepEqual(result.unexpected, [competingRequest, exactAbortConsole]);
});
