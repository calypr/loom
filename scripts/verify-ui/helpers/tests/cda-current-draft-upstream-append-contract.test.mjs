import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { scenarioCaseFor } from '../../registry.mjs';

const scenarioID = 'cda-current-draft-upstream-append';
const caseName = 'upstream-append';

test('CDA spec binds its report to all 26 registered upstream APPEND requirements', async () => {
  const spec = await readFile(new URL('../../specs/cda-current-draft-upstream-append.spec.mjs', import.meta.url), 'utf8');
  const fixture = await readFile(new URL('../cda-fixtures.mjs', import.meta.url), 'utf8');
  assert.match(spec, /cdaScenarioID:\s*'cda-current-draft-upstream-append'/);
  assert.match(spec, /cdaCaseName:\s*'upstream-append'/);

  const registered = scenarioCaseFor(scenarioID, caseName).requiredChecks;
  const makeReportStart = fixture.indexOf('function makeReport(');
  const makeReportEnd = fixture.indexOf('\n}\n', makeReportStart);
  assert(makeReportStart >= 0 && makeReportEnd > makeReportStart, 'CDA fixture must retain its report builder');
  const makeReport = fixture.slice(makeReportStart, makeReportEnd);
  assert.match(makeReport, /requiredChecks:\s*scenario\s*\?\s*scenarioCaseFor\(scenario,\s*caseName\)\.requiredChecks\s*:\s*\[\]/);
  assert.equal(registered.length, 26);
  assert.equal(new Set(registered).size, registered.length);
});
