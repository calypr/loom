import { describe, expect, it, vi } from 'vitest';
import { createLoomClient, LoomRequestError } from './api';

// Exercises the requestError boundary in api.ts: retryability must come from an
// explicit server detail (details.retryable) when present, and only fall back to
// the HTTP-status heuristic (408 | 429 | >=500) when the server is silent about it.
// listExplorers() is the thinnest public method that reaches request() and rejects
// with a LoomRequestError on a non-ok response before any response-shape parsing.

const projectArgs = { project: 'project-a' } as const;

const rejectAsRequestError = async (
  promise: Promise<unknown>,
): Promise<LoomRequestError> => {
  const error: unknown = await promise.then(
    (value: unknown) => {
      throw new Error(`Expected the request to reject, but it resolved with ${String(value)}`);
    },
    (reason: unknown) => reason,
  );
  expect(error).toBeInstanceOf(LoomRequestError);
  if (!(error instanceof LoomRequestError)) {
    throw new Error('Expected a LoomRequestError to be thrown.');
  }
  return error;
};

const clientWithStatus = (status: number, body: unknown) =>
  createLoomClient({
    fetch: vi.fn<typeof globalThis.fetch>()
      .mockResolvedValue(new Response(JSON.stringify(body), { status })),
  });

describe('Loom requestError retryability', () => {
  it('honours an explicit details.retryable=true on a non-5xx status instead of inferring from the HTTP status', async () => {
    const body = {
      error: {
        code: 'PUBLICATION_IN_PROGRESS',
        message: 'publication already running',
        details: { retryable: true },
      },
    };
    const client = clientWithStatus(409, body);
    const error = await rejectAsRequestError(client.listExplorers(projectArgs));
    expect(error.info.status).toBe(409);
    expect(error.info.code).toBe('PUBLICATION_IN_PROGRESS');
    expect(error.info.details).toEqual({ retryable: true });
    expect(error.info.retryable).toBe(true);
    expect(error.retryable).toBe(true);
  });

  it('honours an explicit details.retryable=false on a 5xx status instead of inferring from the HTTP status', async () => {
    const body = {
      error: {
        code: 'UPSTREAM_OVERLOADED',
        message: 'upstream is shedding load',
        details: { retryable: false },
      },
    };
    const client = clientWithStatus(503, body);
    const error = await rejectAsRequestError(client.listExplorers(projectArgs));
    expect(error.info.status).toBe(503);
    expect(error.info.code).toBe('UPSTREAM_OVERLOADED');
    expect(error.info.details).toEqual({ retryable: false });
    expect(error.info.retryable).toBe(false);
    expect(error.retryable).toBe(false);
  });

  it('keeps the 5xx fallback retryable when the server omits details entirely', async () => {
    const body = { error: { code: 'GATEWAY_UNAVAILABLE', message: 'gateway is unavailable' } };
    const client = clientWithStatus(503, body);
    const error = await rejectAsRequestError(client.listExplorers(projectArgs));
    expect(error.info.status).toBe(503);
    expect(error.info.details).toBeUndefined();
    expect(error.info.retryable).toBe(true);
    expect(error.retryable).toBe(true);
  });

  it('keeps the non-5xx fallback non-retryable when the server omits details entirely', async () => {
    const body = { error: { code: 'DRAFT_CONFLICT', message: 'draft changed underneath' } };
    const client = clientWithStatus(409, body);
    const error = await rejectAsRequestError(client.listExplorers(projectArgs));
    expect(error.info.status).toBe(409);
    expect(error.info.details).toBeUndefined();
    expect(error.info.retryable).toBe(false);
    expect(error.retryable).toBe(false);
  });

  it('treats a non-boolean details.retryable as absent and falls back to status inference', async () => {
    const body = {
      error: {
        code: 'UPSTREAM_UNSETTLED',
        message: 'upstream is unsettled',
        details: { retryable: 'true' },
      },
    };
    const serviceUnavailableClient = clientWithStatus(503, body);
    const serviceUnavailableError = await rejectAsRequestError(serviceUnavailableClient.listExplorers(projectArgs));
    expect(serviceUnavailableError.info.status).toBe(503);
    expect(serviceUnavailableError.info.details).toEqual({ retryable: 'true' });
    expect(serviceUnavailableError.info.retryable).toBe(true);
    expect(serviceUnavailableError.retryable).toBe(true);

    const conflictClient = clientWithStatus(409, body);
    const conflictError = await rejectAsRequestError(conflictClient.listExplorers(projectArgs));
    expect(conflictError.info.status).toBe(409);
    expect(conflictError.info.details).toEqual({ retryable: 'true' });
    expect(conflictError.info.retryable).toBe(false);
    expect(conflictError.retryable).toBe(false);
  });

  it('honours an explicit details.retryable=false on the status-retryable 408 and 429 instead of inferring from the HTTP status', async () => {
    const body = {
      error: {
        code: 'UPSTREAM_UNAVAILABLE',
        message: 'upstream is unavailable',
        details: { retryable: false },
      },
    };
    const requestTimeoutClient = clientWithStatus(408, body);
    const requestTimeoutError = await rejectAsRequestError(requestTimeoutClient.listExplorers(projectArgs));
    expect(requestTimeoutError.info.status).toBe(408);
    expect(requestTimeoutError.info.details).toEqual({ retryable: false });
    expect(requestTimeoutError.info.retryable).toBe(false);
    expect(requestTimeoutError.retryable).toBe(false);

    const throttledClient = clientWithStatus(429, body);
    const throttledError = await rejectAsRequestError(throttledClient.listExplorers(projectArgs));
    expect(throttledError.info.status).toBe(429);
    expect(throttledError.info.details).toEqual({ retryable: false });
    expect(throttledError.info.retryable).toBe(false);
    expect(throttledError.retryable).toBe(false);
  });

  it('honours an explicit details.retryable=true on the status-retryable 408 and 429 instead of inferring from the HTTP status', async () => {
    const body = {
      error: {
        code: 'PUBLICATION_IN_PROGRESS',
        message: 'publication already running',
        details: { retryable: true },
      },
    };
    const requestTimeoutClient = clientWithStatus(408, body);
    const requestTimeoutError = await rejectAsRequestError(requestTimeoutClient.listExplorers(projectArgs));
    expect(requestTimeoutError.info.status).toBe(408);
    expect(requestTimeoutError.info.details).toEqual({ retryable: true });
    expect(requestTimeoutError.info.retryable).toBe(true);
    expect(requestTimeoutError.retryable).toBe(true);

    const throttledClient = clientWithStatus(429, body);
    const throttledError = await rejectAsRequestError(throttledClient.listExplorers(projectArgs));
    expect(throttledError.info.status).toBe(429);
    expect(throttledError.info.details).toEqual({ retryable: true });
    expect(throttledError.info.retryable).toBe(true);
    expect(throttledError.retryable).toBe(true);
  });

  it('keeps the 408 fallback retryable when the server omits details entirely', async () => {
    const body = { error: { code: 'UPSTREAM_TIMEOUT', message: 'upstream timed out' } };
    const client = clientWithStatus(408, body);
    const error = await rejectAsRequestError(client.listExplorers(projectArgs));
    expect(error.info.status).toBe(408);
    expect(error.info.details).toBeUndefined();
    expect(error.info.retryable).toBe(true);
    expect(error.retryable).toBe(true);
  });

  it('keeps the 429 fallback retryable when the server omits details entirely', async () => {
    const body = { error: { code: 'UPSTREAM_THROTTLED', message: 'upstream is rate limiting' } };
    const client = clientWithStatus(429, body);
    const error = await rejectAsRequestError(client.listExplorers(projectArgs));
    expect(error.info.status).toBe(429);
    expect(error.info.details).toBeUndefined();
    expect(error.info.retryable).toBe(true);
    expect(error.retryable).toBe(true);
  });

  it('falls back to status inference for truthy non-boolean details.retryable values such as a string or a number', async () => {
    const truthyStringClient = clientWithStatus(503, {
      error: {
        code: 'UPSTREAM_UNSETTLED',
        message: 'upstream is unsettled',
        details: { retryable: 'truthy' },
      },
    });
    const truthyStringError = await rejectAsRequestError(truthyStringClient.listExplorers(projectArgs));
    expect(truthyStringError.info.status).toBe(503);
    expect(truthyStringError.info.details).toEqual({ retryable: 'truthy' });
    expect(truthyStringError.info.retryable).toBe(true);
    expect(truthyStringError.retryable).toBe(true);

    const numericClient = clientWithStatus(409, {
      error: {
        code: 'DRAFT_CONFLICT',
        message: 'draft changed underneath',
        details: { retryable: 1 },
      },
    });
    const numericError = await rejectAsRequestError(numericClient.listExplorers(projectArgs));
    expect(numericError.info.status).toBe(409);
    expect(numericError.info.details).toEqual({ retryable: 1 });
    expect(numericError.info.retryable).toBe(false);
    expect(numericError.retryable).toBe(false);
  });
});
