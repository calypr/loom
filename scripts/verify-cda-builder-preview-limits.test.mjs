import assert from 'node:assert/strict';
import test from 'node:test';
import { assertPreviewLimitState, assertPreviewWindowMatchesSource } from './verify-cda-builder-preview-limits.mjs';

test('Preview limit row counts are bounded by independent source cardinality', () => {
  assertPreviewLimitState({ limit: 100, sourceCount: 73, ariaRowCount: 74 });
  assertPreviewLimitState({ limit: 500, sourceCount: 800, ariaRowCount: 501 });
  assert.throws(() => assertPreviewLimitState({ limit: 500, sourceCount: 800, ariaRowCount: 400 }), /rowcount/);
  assert.throws(() => assertPreviewLimitState({ limit: 25, sourceCount: 0, ariaRowCount: 1 }), /Unsupported/);
});

test('Preview limit changes must preserve exact source values and ordinal identity', () => {
  const sourceRows = [
    { id: 'specimen-1', subject: null, bodySite: 'BodyStructure/site-1' },
    { id: 'specimen-2', subject: 'Patient/patient-2', bodySite: null },
  ];
  const visible = [
    { ordinal: 1, cells: ['specimen-1', '—', 'BodyStructure/site-1'] },
    { ordinal: 2, cells: ['specimen-2', 'Patient/patient-2', '—'] },
  ];
  assertPreviewWindowMatchesSource({ rows: visible, sourceRows });
  assert.throws(() => assertPreviewWindowMatchesSource({ rows: visible.slice(1), sourceRows }), /deep-equal|altered/);
  assert.throws(() => assertPreviewWindowMatchesSource({ rows: [{ ...visible[0], cells: ['wrong', '—', 'BodyStructure/site-1'] }, visible[1]], sourceRows }), /deep-equal|altered/);
});
