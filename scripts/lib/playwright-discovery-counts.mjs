export function playwrightDiscoveryCountIssues(discovery) {
  const issues = [];
  const countsBySession = new Map();
  let totalTestCount = 0;
  for (const session of discovery.sessions ?? []) {
    const specs = session.specs ?? [];
    const testCount = specs.reduce((total, spec) => total + (spec.cases ?? []).length, 0);
    const specFileCount = specs.length;
    totalTestCount += testCount;
    if (countsBySession.has(session.sessionID)) {
      issues.push(`duplicate discovery session id: ${session.sessionID}`);
    } else {
      countsBySession.set(session.sessionID, { testCount, specFileCount });
    }
    if (testCount !== session.testCount || specFileCount !== session.specFileCount) {
      issues.push(`discovery case/spec count drift: ${session.sessionID}; session counts must match its literal spec case arrays`);
    }
  }

  for (const [sessionID, testCountField, specFileCountField] of [
    ['main', 'mainDiscoveryTestCount', 'mainDiscoverySpecFileCount'],
    ['construction-preview-bench', 'dedicatedBenchmarkTestCount', 'dedicatedBenchmarkSpecFileCount'],
  ]) {
    const actual = countsBySession.get(sessionID);
    if (!actual || discovery[testCountField] !== actual.testCount || discovery[specFileCountField] !== actual.specFileCount) {
      issues.push(`discovery snapshot summary count drift: ${sessionID} session totals must match its literal spec case arrays`);
    }
  }

  if (discovery.totalDeclaredTestCount !== totalTestCount) {
    issues.push('discovery snapshot total count drift: totalDeclaredTestCount must equal actual case arrays from every session');
  }
  return issues;
}
