import { expect } from '@playwright/test';

const sourceApiFreezeChecks = [
  'watched source fingerprint baseline captured before browser lifecycle',
  'API build identity baseline captured before browser lifecycle',
  'watched source stayed unchanged during browser run',
  'API build identity stayed unchanged during browser run',
];

export function assertNoUnexpectedFixtureFailures({ observerFailures, reportErrors, fixtureErrors }) {
  expect(observerFailures, 'Unexpected browser transport, runtime, or console failures').toEqual([]);
  expect(reportErrors, 'Unexpected HTTP, transport, runtime, or application errors').toEqual([]);
  expect(fixtureErrors, 'fixture lifecycle reported unexpected network/runtime errors').toEqual([]);
}

export function assertFixtureSourceApiFreeze(assertions) {
  const failures = sourceApiFreezeChecks.flatMap(name => {
    const checks = assertions.filter(assertion => assertion.name === name);
    if (checks.length !== 1) return [{ name, status: 'missing or duplicated' }];
    return checks.filter(check => check.status !== 'passed');
  });
  expect(failures, 'Loom source/API freeze checks must pass').toEqual([]);
}
