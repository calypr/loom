const capabilitySuffix = '/construction-capabilities';
const mutationTypes = new Set(['UPDATE_COLUMN', 'REMOVE_COLUMN']);

const capabilityPath = (request) => typeof request?.path === 'string' &&
  /^\/api\/v1\/projects\/[^/]+\/explorers\/[^/]+\/authoring\/v2\/construction-capabilities$/.test(request.path);
const sameSnapshot = (left, right) => left?.snapshotToken === right?.snapshotToken;
const uniqueRequestMembership = (requests, request) => {
  if (typeof request?.browserRequestId !== 'string' || typeof request?.requestId !== 'string') return false;
  return requests.filter((candidate) => candidate === request).length === 1 &&
    requests.filter((candidate) => candidate.browserRequestId === request.browserRequestId).length === 1 &&
    requests.filter((candidate) => candidate.requestId === request.requestId).length === 1;
};

/**
 * Prove that a failed capabilities request was aborted as a stale read after a
 * concrete column mutation changed the draft key and a matching fresh read began.
 * Returns detailed chronology evidence or undefined; it never classifies by error
 * text, endpoint, or later response alone.
 */
export function proveSupersededCapabilitiesAbort(requests, abortedRequest) {
  if (!Array.isArray(requests)) throw new TypeError('requests must be an array');
  if (!capabilityPath(abortedRequest) || abortedRequest.method !== 'POST' || abortedRequest.failure !== 'net::ERR_ABORTED' ||
    !abortedRequest.body || abortedRequest.status !== undefined || abortedRequest.responseReceivedAt !== undefined ||
    !Number.isFinite(abortedRequest.startedAt) || !Number.isFinite(abortedRequest.completedAt) ||
    typeof abortedRequest.origin !== 'string' || !uniqueRequestMembership(requests, abortedRequest)) return undefined;

  const before = abortedRequest.body;
  if (typeof before.snapshotToken !== 'string' || typeof before.outputId !== 'string' ||
    typeof before.stageId !== 'string' || !Number.isInteger(before.expectedDraftVersion) ||
    typeof before.expectedDraftDigest !== 'string') return undefined;
  const ownerPrefix = abortedRequest.path.slice(0, -capabilitySuffix.length);
  const ownerCommandPath = `${ownerPrefix}/commands`;

  const mutations = requests.filter((candidate) => {
    const commands = candidate.body?.commands;
    return candidate.path === ownerCommandPath && candidate.method === 'POST' && candidate.origin === abortedRequest.origin &&
      uniqueRequestMembership(requests, candidate) && candidate.status === 200 &&
      Number.isFinite(candidate.responseReceivedAt) && candidate.response &&
      Array.isArray(commands) && commands.length > 0 &&
      commands.every((command) => command.outputId === before.outputId && mutationTypes.has(command.type)) &&
      sameSnapshot(candidate.body, before) &&
      candidate.body.expectedDraftVersion === before.expectedDraftVersion &&
      candidate.body.expectedDraftDigest === before.expectedDraftDigest &&
      Number.isFinite(candidate.startedAt) && candidate.startedAt > abortedRequest.startedAt &&
      candidate.responseReceivedAt < abortedRequest.completedAt &&
      candidate.response.draftVersion === before.expectedDraftVersion + 1 &&
      typeof candidate.response.draftDigest === 'string' &&
      candidate.response.draftDigest !== before.expectedDraftDigest &&
      Array.isArray(candidate.response.results) &&
      candidate.response.results.some((result) => result.outputId === before.outputId);
  });
  if (mutations.length !== 1) return undefined;
  const mutation = mutations[0];

  const after = {
    snapshotToken: before.snapshotToken,
    outputId: before.outputId,
    stageId: before.stageId,
    expectedDraftVersion: mutation.response.draftVersion,
    expectedDraftDigest: mutation.response.draftDigest,
  };
  const replacements = requests.filter((candidate) => capabilityPath(candidate) &&
    candidate.path === abortedRequest.path && candidate.method === 'POST' && candidate.origin === abortedRequest.origin &&
    uniqueRequestMembership(requests, candidate) && candidate.status === 200 &&
    Number.isFinite(candidate.completedAt) && sameSnapshot(candidate.body, after) &&
    candidate.body.outputId === after.outputId && candidate.body.stageId === after.stageId &&
    candidate.body.expectedDraftVersion === after.expectedDraftVersion &&
    candidate.body.expectedDraftDigest === after.expectedDraftDigest &&
    Number.isFinite(candidate.startedAt) && candidate.startedAt > mutation.responseReceivedAt);
  if (replacements.length !== 1) return undefined;
  const replacement = replacements[0];

  const chronology = {
    abortedRequestStartedBeforeMutation: abortedRequest.startedAt < mutation.startedAt,
    abortedRequestWasPendingAtIdentityChange: abortedRequest.completedAt > mutation.responseReceivedAt,
    replacementStartedAfterIdentityChange: replacement.startedAt > mutation.responseReceivedAt,
    replacementMatchesMutationResult: replacement.body.expectedDraftVersion === after.expectedDraftVersion &&
      replacement.body.expectedDraftDigest === after.expectedDraftDigest,
  };
  if (!Object.values(chronology).every(Boolean)) return undefined;

  return {
    request: {
      requestId: abortedRequest.requestId,
      browserRequestId: abortedRequest.browserRequestId,
      origin: abortedRequest.origin,
      path: abortedRequest.path,
      body: { ...before },
      startedAt: abortedRequest.startedAt,
      completedAt: abortedRequest.completedAt,
      failure: abortedRequest.failure,
    },
    mutation: {
      requestId: mutation.requestId,
      browserRequestId: mutation.browserRequestId,
      origin: mutation.origin,
      path: mutation.path,
      action: mutation.triggerAction,
      commands: mutation.body.commands.map((command) => ({ ...command })),
      startedAt: mutation.startedAt,
      responseReceivedAt: mutation.responseReceivedAt,
      status: mutation.status,
      requestIdentity: {
        snapshotToken: mutation.body.snapshotToken,
        outputId: before.outputId,
        draftVersion: mutation.body.expectedDraftVersion,
        draftDigest: mutation.body.expectedDraftDigest,
      },
      resultingIdentity: { ...after },
    },
    replacement: {
      requestId: replacement.requestId,
      browserRequestId: replacement.browserRequestId,
      origin: replacement.origin,
      path: replacement.path,
      startedAt: replacement.startedAt,
      responseReceivedAt: replacement.responseReceivedAt,
      completedAt: replacement.completedAt,
      status: replacement.status,
      body: { ...replacement.body },
    },
    chronology,
  };
}
