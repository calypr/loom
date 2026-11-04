import assert from 'node:assert/strict';

export const UNPIVOT_KEYS = Object.freeze({ id: 'u_id', unit: 'u_unit', subject: 'u_sub' });

export const UNPIVOT_REQUIRED_CHECKS = Object.freeze([
  'step-0-behavior-capabilities', 'discovery-hides-date-family', 'step-1-prelude-filter-step', 'step-2-discovery', 'step-3-configure-oracle-preview-preserve',
  'step-4-cancel-byte-stable', 'step-5-apply-exact-rows', 'step-6-reload-materialized-echo', 'step-7-edit-rename-and-drop',
  'step-8-cleared-melt-lane-stays-cleared', 'step-9-re-add-honest-rematerialization', 'step-10-remove-clean', 'date-column-survives-string-melt',
  'negative-C0-public-control', 'negative-C1-signed-decorates', 'negative-C2-forged', 'negative-C3-cross-snapshot',
  'negative-C4-tampered', 'negative-C5-empty-choiceId', 'negative-C6-missing-choiceId', 'negative-C7-duplicate',
  'negative-C8-column-mismatch', 'negative-C9-over-selection', 'negative-C10-types-incompatible',
  'negative-C11-already-projected-not-500', 'negative-C12-payloadless-requires-unpivot',
  'contract-C13-non-melt-step-undecorated', 'negative-C14-new-carrier-stale-stage', 'negative-C15-signed-date-melt-refused',
  'blank-and-exact-melt-evidence',
]);

export const UNPIVOT_SCREENSHOT_FLOOR = 13;

const UNPIVOT_REPORT_METADATA = Object.freeze({ schemaVersion: 1, mode: 'raw-cdp-unpivot-row-source-journey' });

function withAssertionId(assertionId, run) {
  try {
    return run();
  } catch (error) {
    if (error && typeof error === 'object' && !error.assertionId) error.assertionId = assertionId;
    throw error;
  }
}

export function buildUnpivotOracle(observations) {
  return withAssertionId('unpivot.oracle.source', () => {
    assert.ok(Array.isArray(observations), 'Fixture oracle observations must be an array');
    const rows = observations.map((observation) => ({
      id: observation.id,
      status: observation.status,
      unit: observation?.valueQuantity?.unit ?? null,
      subject: observation?.subject?.reference ?? null,
      valueInteger: observation?.valueInteger ?? null,
      effective: observation?.effectiveDateTime ?? null,
    }));
    const byId = new Map(rows.map((row) => [row.id, row]));
    assert.ok(rows.length >= 1006, `Fixture oracle expects at least six devloop + 1000 generated J01 observations, got ${rows.length}`);
    assert.ok(rows.every((row) => typeof row.id === 'string' && row.id.length > 0), 'every observation must have an id');
    assert.ok(rows.every((row) => row.status === 'final'), 'every fixture observation is status final (the prelude filter is row-preserving)');
    assert.ok(rows.some((row) => row.unit === 'cm'), 'Fixture oracle expects the two devloop cm Observations');
    const unitBlank = rows.filter((row) => row.unit === null);
    assert.ok(unitBlank.length >= 1, 'Fixture oracle expects rows without valueQuantity.unit for the PRESERVE blank-melt evidence');
    const unitPopulated = rows.filter((row) => row.unit !== null);
    assert.ok(unitPopulated.length >= 1, 'Fixture oracle expects rows with valueQuantity.unit for the exact-melt evidence');
    const subjectBlank = rows.filter((row) => row.subject === null);
    assert.ok(subjectBlank.length >= 1, 'Fixture oracle expects rows without subject.reference for the PRESERVE blank-melt evidence');
    const subjectPopulated = rows.filter((row) => row.subject !== null);
    assert.ok(subjectPopulated.length >= 1, 'Fixture oracle expects rows with subject.reference for the exact-melt evidence');
    const effectivePopulated = rows.filter((row) => typeof row.effective === 'string' && row.effective.length > 0);
    assert.ok(effectivePopulated.length >= 1, 'Fixture oracle expects rows with effectiveDateTime for the date-column survival leg');
    const effectiveValues = [...new Set(effectivePopulated.map((row) => row.effective))];
    return { byId, rows, unitBlankCount: unitBlank.length, subjectBlankCount: subjectBlank.length, effectiveValues };
  });
}

export function isDateWitnessHeader(header) {
  return /^Date(?:date_time|date)?$/i.test((header ?? '').trim());
}

function assertNonEmpty(values, label) {
  assert.ok(Array.isArray(values) && values.length > 0, `${label}: zero rows matched, expected at least one oracle row`);
}

export function assertUnpivotMeltPreview(preview, label, activeKeys, oracle) {
  return withAssertionId('unpivot.preview.oracle', () => {
    assert.ok(preview && preview.headers?.length >= 3, `${label}: melt preview has no columns`);
    const varIndex = preview.headers.findIndex((header) => /variable|melt name/i.test(header));
    const valIndex = preview.headers.findIndex((header) => /value|melted/i.test(header));
    const numIndex = preview.headers.findIndex((header) => /Number/i.test(header));
    assert.ok(varIndex >= 0 && valIndex >= 0 && numIndex >= 0, `${label}: melt headers ${JSON.stringify(preview.headers)}`);
    const dateIndex = preview.headers.findIndex(isDateWitnessHeader);
    assert.ok(dateIndex >= 0, `${label}: the Date survival-witness column must be carried through the melt: ${JSON.stringify(preview.headers)}`);
    const rows = preview.rows.filter((row) => row.length >= preview.headers.length);
    assertNonEmpty(rows, `${label} melt preview rows`);
    const groupCounts = new Map();
    for (const row of rows) {
      const anchor = (row[numIndex] ?? '').trim();
      groupCounts.set(anchor, (groupCounts.get(anchor) || 0) + 1);
    }
    const completeGroups = [...groupCounts.values()].filter((count) => count >= activeKeys.length).length;
    assert.ok(rows.length >= 15 && completeGroups >= 5,
      `${label}: melt preview needs >= 15 rows with >= 5 complete groups, got ${rows.length} rows / ${completeGroups} groups`);
    const blank = (cell) => { const text = (cell ?? '').trim(); return text === '' || text === '\u2014'; };
    const blocks = [];
    let current = null;
    for (const row of rows) {
      if (row[varIndex] === UNPIVOT_KEYS.id) { current = [row]; blocks.push({ rows: current, anchored: true }); continue; }
      if (current && current.length < activeKeys.length) { current.push(row); continue; }
      current = null;
      if (blocks.length && !blocks.at(-1).anchored) blocks.at(-1).rows.push(row);
      else blocks.push({ rows: [row], anchored: false });
    }
    const attributedRows = blocks.filter((block) => block.anchored && block.rows.length >= activeKeys.length).reduce((sum, block) => sum + block.rows.length, 0);
    assert.ok(attributedRows >= 4 * activeKeys.length,
      `${label}: expected at least 4 fully attributed record blocks, got ${attributedRows / activeKeys.length | 0}`);
    const unitDomain = new Set(oracle.rows.map((entry) => entry.unit).filter((value) => value !== null).map((value) => String(value)));
    const subjectDomain = new Set(oracle.rows.map((entry) => entry.subject).filter((value) => value !== null).map((value) => String(value)));
    const evidence = { blankMeltCells: 0, exactMeltCells: 0, idMeltCells: 0, dateSurvivalCells: 0 };
    for (const block of blocks) {
      const idRow = block.rows.find((candidate) => candidate[varIndex] === UNPIVOT_KEYS.id);
      const source = block.anchored && idRow ? oracle.byId.get((idRow[valIndex] ?? '').trim()) : null;
      const complete = block.anchored && block.rows.length >= activeKeys.length
        && new Set(block.rows.map((candidate) => candidate[varIndex])).size === activeKeys.length;
      for (const row of block.rows) {
        const variable = row[varIndex];
        const valueCell = (row[valIndex] ?? '').trim();
        const numberCell = (row[numIndex] ?? '').trim();
        assert.ok(activeKeys.includes(variable), `${label}: record block has unexpected melt key ${JSON.stringify(variable)} (expected one of ${JSON.stringify(activeKeys)})`);
        if (!blank(numberCell)) {
          assert.ok(Number.isFinite(Number.parseFloat(numberCell)), `${label}: Number anchor ${JSON.stringify(numberCell)} is neither blank nor numeric`);
          if (source) assert.equal(Number.parseFloat(numberCell), source.valueInteger, `${label}: Number anchor ${numberCell} != oracle valueInteger ${source.valueInteger}`);
        }
        const dateCell = (row[dateIndex] ?? '').trim();
        if (!blank(dateCell)) {
          if (source && source.effective != null) {
            assert.equal(dateCell, String(source.effective), `${label}: Date survival cell ${JSON.stringify(dateCell)} != oracle effectiveDateTime ${source.effective} — the string melt mutated the carried date column`);
          } else {
            assert.ok(oracle.effectiveValues.includes(dateCell), `${label}: Date cell ${JSON.stringify(dateCell)} is not a fixture effectiveDateTime`);
          }
          evidence.dateSurvivalCells += 1;
        } else if (source && source.effective != null) {
          assert.fail(`${label}: attributed record ${source.id} HAS effectiveDateTime ${source.effective} but its carried Date cell rendered blank — survival witness mutated`);
        }
        if (variable === UNPIVOT_KEYS.id) {
          assert.ok(/^dev-/.test(valueCell), `${label}: id melt cell ${JSON.stringify(valueCell)} is not a seeded observation id`);
          assert.ok(oracle.byId.has(valueCell), `${label}: melted id ${valueCell} not part of the live observation oracle`);
          assert.equal(valueCell, String((source || {}).id || valueCell), `${label}: id melt cell must equal the block record identity`);
          evidence.idMeltCells += 1;
          evidence.exactMeltCells += 1;
        } else if (variable === UNPIVOT_KEYS.unit) {
          if (blank(valueCell)) {
            assert.ok(!complete || !source || source.unit === null, `${label}: blank unit cell attributed to record ${source?.id} which HAS a unit`);
            evidence.blankMeltCells += 1;
          } else if (source) {
            assert.equal(valueCell, String(source.unit), `${label}: valueQuantity.unit cell ${JSON.stringify(valueCell)} != oracle ${source.unit}`);
            evidence.exactMeltCells += 1;
          } else {
            assert.ok(unitDomain.has(valueCell), `${label}: unattributed unit cell ${JSON.stringify(valueCell)} not in oracle unit domain`);
            evidence.exactMeltCells += 1;
          }
        } else if (variable === UNPIVOT_KEYS.subject) {
          if (blank(valueCell)) {
            assert.ok(!complete || !source || source.subject === null, `${label}: blank subject cell attributed to record ${source?.id} which HAS a subject.reference`);
            evidence.blankMeltCells += 1;
          } else if (source) {
            assert.equal(valueCell, String(source.subject), `${label}: subject.reference cell ${JSON.stringify(valueCell)} != oracle ${source.subject}`);
            evidence.exactMeltCells += 1;
          } else {
            assert.ok(subjectDomain.has(valueCell), `${label}: unattributed subject cell ${JSON.stringify(valueCell)} not in oracle subject domain`);
            evidence.exactMeltCells += 1;
          }
        }
      }
      if (complete) {
        const keySet = [...new Set(block.rows.map((candidate) => candidate[varIndex]))].sort();
        assert.deepEqual(keySet, [...activeKeys].sort(), `${label}: complete record block must melt exactly the active keys, got ${JSON.stringify(keySet)}`);
      }
    }
    return { rows, evidence };
  });
}

export function assertNoUncaughtPageExceptions(exceptions) {
  return withAssertionId('unpivot.report.page-exceptions', () => {
    assert.deepEqual(exceptions, [], `uncaught page exceptions: ${JSON.stringify(exceptions)}`);
  });
}

export function assertMeltEvidence(evidence) {
  return withAssertionId('unpivot.report.melt-evidence', () => {
    assert.ok(evidence.blankMeltCells > 0 && evidence.exactMeltCells > 0 && evidence.idMeltCells > 0,
      `melt evidence: blanks=${evidence.blankMeltCells} exact=${evidence.exactMeltCells} idCells=${evidence.idMeltCells} (all must be > 0)`);
  });
}

export function assertRequiredChecks(checks, includeAggregate = false) {
  return withAssertionId('unpivot.report.required-checks', () => {
    assert.ok(Array.isArray(checks), 'unpivot report checks must be an array');
    const present = new Set(checks.filter((entry) => entry.status === 'passed').map((entry) => entry.name));
    if (includeAggregate) {
      const missing = UNPIVOT_REQUIRED_CHECKS.filter((name) => !present.has(name));
      assert.equal(missing.length, 0, `closed check set incomplete after aggregation: ${JSON.stringify(missing)}`);
    } else {
      const skipped = UNPIVOT_REQUIRED_CHECKS.filter((name) => name !== 'blank-and-exact-melt-evidence' && !present.has(name));
      assert.equal(skipped.length, 0, `skipped checks (each assertion must run): ${JSON.stringify(skipped)}`);
    }
  });
}

export function assertClosedReport(report) {
  return withAssertionId('unpivot.report.closed', () => {
    assert.equal(report?.status, 'passed', 'unpivot report status must be passed');
    assert.equal(report?.schemaVersion, UNPIVOT_REPORT_METADATA.schemaVersion,
      `unpivot report schemaVersion must be ${UNPIVOT_REPORT_METADATA.schemaVersion}`);
    assert.equal(report?.mode, UNPIVOT_REPORT_METADATA.mode, `unpivot report mode must be ${UNPIVOT_REPORT_METADATA.mode}`);
    assertRequiredChecks(report.checks, true);
    assert.ok(Array.isArray(report.screenshots), 'unpivot report screenshots must be an array');
    assert.ok(report.screenshots.length >= UNPIVOT_SCREENSHOT_FLOOR,
      `screenshot tally below the per-leg floor: wrote ${report.screenshots.length}, expected at least ${UNPIVOT_SCREENSHOT_FLOOR}`);
    assert.equal(new Set(report.screenshots).size, report.screenshots.length, `duplicate screenshot names written: ${JSON.stringify(report.screenshots)}`);
  });
}
