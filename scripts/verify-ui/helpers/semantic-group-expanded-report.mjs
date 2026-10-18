import { createHash } from 'node:crypto';

export const stableDigest = value => createHash('sha256').update(JSON.stringify(value)).digest('hex');

export function protectNativeRequestBodies(report) {
  const protectEntry = entry => {
    for (const property of ['request', 'response']) {
      let digestOnly = entry[property] === undefined ? undefined : {
        redacted: true,
        sha256: stableDigest(entry[property]),
      };
      Object.defineProperty(entry, property, {
        configurable: true,
        enumerable: true,
        get: () => digestOnly,
        set: value => {
          digestOnly = value === undefined ? undefined : { redacted: true, sha256: stableDigest(value) };
        },
      });
    }
    return entry;
  };
  const entries = report.nativeRequests ?? [];
  entries.forEach(protectEntry);
  report.nativeRequests = new Proxy(entries, {
    get(target, property, receiver) {
      if (property === 'push') {
        return (...newEntries) => Array.prototype.push.apply(target, newEntries.map(protectEntry));
      }
      return Reflect.get(target, property, receiver);
    },
  });
}

export function assertExactRows(actualRows, expectedRows, message) {
  if (!Array.isArray(actualRows) || !Array.isArray(expectedRows)) {
    throw new Error(`${message}: rows must be arrays.`);
  }
  const canonical = rows => [...rows].map(row => JSON.stringify(row)).sort();
  const actual = canonical(actualRows);
  const expected = canonical(expectedRows);
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`${message} (actualCount=${actualRows.length}, expectedCount=${expectedRows.length}, ` +
      `actualSha256=${stableDigest(actual)}, expectedSha256=${stableDigest(expected)}).`);
  }
}

export function summarizeOracle(oracle) {
  return {
    sourceResourceType: oracle.sourceResourceType,
    sourcePath: oracle.sourcePath,
    occurrenceId: oracle.occurrenceId,
    scanLimit: oracle.scanLimit,
    scannedRowCount: oracle.scannedRows,
    selectedRootCount: oracle.selectedRoots.length,
    expandedOccurrenceCount: oracle.expectedExpandedRowCount,
    codingIdentityCount: oracle.familyTuples.length,
    subjectCodingGroupCount: oracle.expectedSubjectGroups.length,
    displayLabelCount: oracle.displayLabels.length,
    digest: stableDigest(oracle.occurrenceRows),
  };
}
