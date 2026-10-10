const describeRead = (details, error) => ({
  ...details,
  ...(error === undefined ? {} : { error: String(error?.message ?? error).slice(0, 1_000) }),
});

export function createPendingResponseReads() {
  const pendingReads = new Set();
  const detailsByRead = new Map();
  const failedReads = new Map();

  const track = (read, details = {}) => {
    const promise = Promise.resolve(read);
    pendingReads.add(promise);
    detailsByRead.set(promise, { ...details });
    void promise.then(() => {
      pendingReads.delete(promise);
      detailsByRead.delete(promise);
    }, error => {
      pendingReads.delete(promise);
      detailsByRead.delete(promise);
      failedReads.set(promise, { details: { ...details }, error });
    });
    return promise;
  };

  const flush = async ({ timeoutMs = 5_000, filter, label = 'response body reads' } = {}) => {
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
      throw new RangeError('Response-read flush timeout must be a finite positive number of milliseconds.');
    }
    if (filter !== undefined && typeof filter !== 'function') {
      throw new TypeError('Response-read flush filter must be a function.');
    }
    const matches = details => filter === undefined || filter(details);
    const deadline = Date.now() + timeoutMs;

    while (true) {
      const batch = [...pendingReads]
        .filter(read => matches(detailsByRead.get(read) ?? {}));
      if (batch.length === 0) break;

      const remainingMs = deadline - Date.now();
      if (remainingMs <= 0) {
        const outstanding = [...pendingReads]
          .filter(read => matches(detailsByRead.get(read) ?? {}))
          .map(read => describeRead(detailsByRead.get(read) ?? {}));
        throw new Error(`Timed out flushing ${label} after ${timeoutMs}ms; outstanding: ${JSON.stringify(outstanding)}`);
      }

      const timeout = Symbol('response-read-timeout');
      let timer;
      let outcome;
      try {
        outcome = await Promise.race([
          Promise.allSettled(batch).then(() => 'settled'),
          new Promise(resolve => { timer = setTimeout(() => resolve(timeout), remainingMs); }),
        ]);
      } finally {
        clearTimeout(timer);
      }
      if (outcome === timeout) {
        const outstanding = [...pendingReads]
          .filter(read => matches(detailsByRead.get(read) ?? {}))
          .map(read => describeRead(detailsByRead.get(read) ?? {}));
        throw new Error(`Timed out flushing ${label} after ${timeoutMs}ms; outstanding: ${JSON.stringify(outstanding)}`);
      }
    }

    const failures = [...failedReads.values()]
      .filter(({ details }) => matches(details))
      .map(({ details, error }) => describeRead(details, error));
    if (failures.length) throw new Error(`Failed ${label}: ${JSON.stringify(failures)}`);
  };

  return { pendingReads, track, flush };
}
