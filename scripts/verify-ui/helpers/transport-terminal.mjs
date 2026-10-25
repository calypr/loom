import assert from 'node:assert/strict';

export const classifyTransportTerminal = ({ responseHeadersObserved, requestFailed }) => {
  assert.equal(typeof responseHeadersObserved, 'boolean');
  assert.equal(typeof requestFailed, 'boolean');
  const settlement = requestFailed
    ? 'request-failed'
    : responseHeadersObserved ? 'response' : 'pending';
  return {
    settlement,
    responseObservedBeforeFailure: requestFailed && responseHeadersObserved,
    responseStatusCredit: settlement === 'response',
    responseOwnerCredit: settlement === 'response',
  };
};

export const classifyCapturedTransport = ({
  responseHeadersObserved,
  requestFailed,
  networkComplete,
  responseCaptureComplete,
}) => {
  const terminal = classifyTransportTerminal({ responseHeadersObserved, requestFailed });
  const settlement = terminal.settlement === 'response'
    ? networkComplete && responseCaptureComplete ? 'response' : 'response-body-pending'
    : terminal.settlement;
  return {
    ...terminal,
    settlement,
    responseStatusCredit: settlement === 'response',
    responseOwnerCredit: settlement === 'response',
  };
};
