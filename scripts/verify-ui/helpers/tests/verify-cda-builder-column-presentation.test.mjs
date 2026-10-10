import assert from 'node:assert/strict';
import test from 'node:test';
import { assertHeaders, assertHiddenPresentation, assertRestoredPresentation, assertRowsMatchByColumnIdentity } from '../../workflows/verify-cda-builder-column-presentation.mjs';

test('column visibility must remove only the named visible column', () => {
  assertHiddenPresentation({ before: ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE'],
    hidden: ['SPECIMEN ID', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE'], column: 'SUBJECT.REFERENCE' });
  assert.throws(() => assertHiddenPresentation({ before: ['SPECIMEN ID', 'SUBJECT.REFERENCE'],
    hidden: ['SPECIMEN ID', 'SUBJECT.REFERENCE'], column: 'SUBJECT.REFERENCE' }), /must be hidden/);
  assert.throws(() => assertHiddenPresentation({ before: ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'BODY SITE'],
    hidden: ['SPECIMEN ID'], column: 'SUBJECT.REFERENCE' }), /remove exactly one/);
});

test('exact order, duplicate values, null display, and row identities survive presentation restore', () => {
  const before = ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE'];
  const rows = [
    { ordinal: 1, cells: ['sp-1', 'Patient/p-1', '—'] },
    { ordinal: 2, cells: ['sp-2', 'Patient/p-1', 'BodyStructure/b-1'] },
  ];
  assertHeaders(before, before);
  assertRestoredPresentation({ before, restored: before.slice(), beforeRows: rows, restoredRows: structuredClone(rows) });
  assert.throws(() => assertHeaders(before, before.slice().reverse()), /exact expected order/);
  assert.throws(() => assertRestoredPresentation({ before, restored: before.slice().reverse() }), /exact original header/);
  assert.throws(() => assertRestoredPresentation({ before, restored: before, beforeRows: rows,
    restoredRows: [{ ...rows[0], ordinal: 2 }, rows[1]] }), /exact visible row values and identities/);
});

test('reordering columns cannot hide corrupted values behind an unordered comparison', () => {
  const beforeHeaders = ['SPECIMEN ID', 'SUBJECT.REFERENCE'];
  const beforeRows = [
    { ordinal: 1, cells: ['sp-1', 'Patient/p-1'] },
    { ordinal: 2, cells: ['sp-2', 'Patient/p-1'] },
  ];
  assertRowsMatchByColumnIdentity({ beforeHeaders, beforeRows,
    afterHeaders: ['SUBJECT.REFERENCE', 'SPECIMEN ID'],
    afterRows: [{ ordinal: 1, cells: ['Patient/p-1', 'sp-1'] }, { ordinal: 2, cells: ['Patient/p-1', 'sp-2'] }] });
  assert.throws(() => assertRowsMatchByColumnIdentity({ beforeHeaders, beforeRows,
    afterHeaders: ['SUBJECT.REFERENCE', 'SPECIMEN ID'],
    afterRows: [{ ordinal: 1, cells: ['Patient/p-2', 'sp-1'] }, { ordinal: 2, cells: ['Patient/p-1', 'sp-2'] }] }), /each exact value/);
});
