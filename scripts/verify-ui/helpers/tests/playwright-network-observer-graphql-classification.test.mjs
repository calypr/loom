import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import {
  createPlaywrightNetworkObserver,
  isMutationRequest,
} from '../playwright-network-observer.mjs';

const origin = 'http://observer.test';
const outputReadRequest = {
  query: 'query LoomOutput($input: DataframeRowsInput!) { dataframeRows(input: $input) { materialization { id name revision projectId datasetGeneration state rowCount selector { recipe translationVersion output } } columns rows rowIds totalCount pageInfo { hasNextPage endCursor } } }',
  variables: {
    input: {
      projectId: 'fixture-project',
      selector: { recipe: 'fixture-recipe', translationVersion: 'authoring-v2-native-10', output: 'fixture-output' },
      columns: ['fixture-column'],
      first: 20,
    },
  },
};

function requestWithBody(body) {
  const postData = JSON.stringify(body);
  return {
    url: () => origin + '/graphql/graph',
    method: () => 'POST',
    headers: () => ({ 'content-type': 'application/json' }),
    resourceType: () => 'fetch',
    postData: () => postData,
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  };
}

test('captures LoomOutput as a read while mutation, mixed, malformed, and unknown POSTs remain non-exemptible', () => {
  const page = new EventEmitter();
  page.url = () => origin + '/';
  const report = { errors: [] };
  const observer = createPlaywrightNetworkObserver(page, {
    scopeRequest: request => new URL(request.url()).origin === origin &&
      new URL(request.url()).pathname === '/graphql/graph',
    appOrigins: [origin],
    report,
  });

  try {
    const queryRequest = requestWithBody(outputReadRequest);
    page.emit('request', queryRequest);
    const query = observer.byRequest.get(queryRequest);
    assert.equal(query.isMutation, false);
    assert.deepEqual(observer.requestBody(query), outputReadRequest);

    observer.markExpectedTransportFailure(queryRequest, {
      reason: 'the Viewer Retry test injects one exact result-read fault',
      action: 'abort selected GraphQL request',
    });
    page.emit('requestfailed', queryRequest);
    assert.equal(query.expectedTransportFailure.reason, 'the Viewer Retry test injects one exact result-read fault');
    assert.equal(query.readCancellationDiagnostic, false);
    assert.equal(observer.failures.some(failure => failure.entry === query), false);

    const mutationBody = {
      query: 'mutation UpdateExplorer($input: UpdateInput!) { updateExplorer(input: $input) { id } }',
      operationName: 'UpdateExplorer',
      variables: { input: { id: 'fixture-explorer' } },
    };
    const mutationRequest = requestWithBody(mutationBody);
    page.emit('request', mutationRequest);
    const mutation = observer.byRequest.get(mutationRequest);
    assert.equal(mutation.isMutation, true);
    assert.throws(() => observer.markExpectedTransportFailure(mutationRequest, {
      reason: 'a mutation must remain fatal',
      action: 'abort selected GraphQL request',
    }), /cannot exempt a mutation request/);
    page.emit('requestfailed', mutationRequest);
    assert.ok(observer.failures.some(failure => failure.entry === mutation));
    assert.equal(mutation.expectedTransportFailure, undefined);

    const conservativeRequests = [
      {
        query: 'query LoomOutput($input: DataframeRowsInput!) { dataframeRows(input: $input) { rows } } mutation UpdateExplorer { updateExplorer { id } }',
        operationName: 'UpdateExplorer',
      },
      {
        query: 'query LoomOutput($input: DataframeRowsInput!) { dataframeRows(input: $input) { rows } } query Other { other }',
        operationName: 'Other',
      },
      { query: 'query LoomOutput', operationName: 'LoomOutput' },
      { operationName: 'LoomOutput' },
    ];
    for (const body of conservativeRequests) {
      assert.equal(isMutationRequest('POST', '/graphql/graph', body), true);
    }
    assert.equal(isMutationRequest('POST', '/unowned/write', outputReadRequest), true);
    assert.equal(isMutationRequest('POST', '/graphql/graph', outputReadRequest), false);
    assert.equal(report.errors.some(error => error.browserRequestId === mutation.browserRequestId && error.kind === 'network'), true);
    assert.equal(report.errors.some(error => error.browserRequestId === query.browserRequestId), false);
  } finally {
    observer.stop();
  }
});
