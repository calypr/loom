import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import vm from 'node:vm';
import { chromium } from '@playwright/test';
import test from 'node:test';

import {
  buildRelatedQuantityOracleExecuteScript,
  buildRelatedQuantityPivotOracle,
  expectedTextOnlyQuantityPivotRows,
  positiveRelatedQuantityDuplicateWitness,
  summarizeRelatedQuantityPivotOracleResult,
  summarizeRelatedQuantityRows,
  validateRelatedQuantityPivotOracle,
} from '../related-quantity-pivot-oracle.mjs';
import { buildArangoShellInvocation } from '../owned-arangosh-command.mjs';
import { waitForBrowser } from '../cda-playwright.mjs';
import { constructionTableReady, selectPivotSourceChoice, selectSavedPreviewReconcile } from '../../workflows/verify-cda-quantity-pivot-native-drag-browser.mjs';

const rows = [
  { specimenId: 'specimen-a', patientId: 'patient-a', observationId: 'obs-1', patientPresent: true, observationPresent: true, textPresent: true, text: 'FINAL', codePresent: true, code: 'd', valuePresent: true, value: 1 },
  { specimenId: 'specimen-a', patientId: 'patient-a', observationId: 'obs-2', patientPresent: true, observationPresent: true, textPresent: true, text: 'FINAL', codePresent: true, code: 'd', valuePresent: true, value: 5 },
  { specimenId: 'specimen-b', patientId: 'patient-b', observationId: 'obs-3', patientPresent: true, observationPresent: true, textPresent: true, text: 'FINAL', codePresent: true, code: 'd', valuePresent: true, value: 4 },
  { specimenId: 'specimen-b', patientId: 'patient-b', observationId: 'obs-4', patientPresent: true, observationPresent: true, textPresent: true, text: 'FINAL', codePresent: false, valuePresent: true, value: 8 },
  { specimenId: 'specimen-c', patientId: 'patient-c', observationId: 'obs-5', patientPresent: true, observationPresent: true, textPresent: true, text: 'FINAL', codePresent: true, code: null, valuePresent: true, value: null },
  { specimenId: 'specimen-c', patientId: 'patient-c', observationId: 'obs-6', patientPresent: true, observationPresent: true, textPresent: true, text: null, codePresent: true, code: 'd', valuePresent: true, value: 4 },
  { specimenId: 'specimen-d', patientId: 'patient-d', observationId: 'obs-7', patientPresent: true, observationPresent: true, textPresent: false, codePresent: true, code: 'd', valuePresent: true, value: 3 },
  { specimenId: 'specimen-e', patientId: null, observationId: null, patientPresent: false, observationPresent: false, textPresent: true, text: null, codePresent: true, code: null, valuePresent: true, value: null },
  { specimenId: 'specimen-f', patientId: 'patient-f', observationId: null, patientPresent: true, observationPresent: false, textPresent: true, text: null, codePresent: true, code: null, valuePresent: true, value: null },
];

const factoredFixtureRows = [
  { specimenId: 'Specimen/s1', patientId: 'Patient/p1', observationId: 'Observation/o1', patientPresent: true, observationPresent: true, textPresent: false, codePresent: true, code: 'd', valuePresent: true, value: 2 },
  { specimenId: 'Specimen/s1', patientId: 'Patient/p1', observationId: 'Observation/o2', patientPresent: true, observationPresent: true, textPresent: true, text: null, codePresent: true, code: 'd', valuePresent: true, value: 5 },
  { specimenId: 'Specimen/s1', patientId: 'Patient/p1', observationId: 'Observation/o3', patientPresent: true, observationPresent: true, textPresent: true, text: 'other', codePresent: true, code: null, valuePresent: true, value: null },
  { specimenId: 'Specimen/s1', patientId: 'Patient/p1', observationId: 'Observation/o4', patientPresent: true, observationPresent: true, textPresent: true, text: 'nonnum', codePresent: false, valuePresent: true, value: 'unknown' },
  { specimenId: 'Specimen/s1', patientId: 'Patient/p1', observationId: 'Observation/o5', patientPresent: true, observationPresent: true, textPresent: false, codePresent: true, code: 'd', valuePresent: false },
  { specimenId: 'Specimen/s2', patientId: 'Patient/p1', observationId: 'Observation/o1', patientPresent: true, observationPresent: true, textPresent: false, codePresent: true, code: 'd', valuePresent: true, value: 2 },
  { specimenId: 'Specimen/s2', patientId: 'Patient/p1', observationId: 'Observation/o2', patientPresent: true, observationPresent: true, textPresent: true, text: null, codePresent: true, code: 'd', valuePresent: true, value: 5 },
  { specimenId: 'Specimen/s2', patientId: 'Patient/p1', observationId: 'Observation/o3', patientPresent: true, observationPresent: true, textPresent: true, text: 'other', codePresent: true, code: null, valuePresent: true, value: null },
  { specimenId: 'Specimen/s2', patientId: 'Patient/p1', observationId: 'Observation/o4', patientPresent: true, observationPresent: true, textPresent: true, text: 'nonnum', codePresent: false, valuePresent: true, value: 'unknown' },
  { specimenId: 'Specimen/s2', patientId: 'Patient/p1', observationId: 'Observation/o5', patientPresent: true, observationPresent: true, textPresent: false, codePresent: true, code: 'd', valuePresent: false },
  { specimenId: 'Specimen/s3', patientId: 'Patient/p2', observationId: null, patientPresent: true, observationPresent: false, textPresent: true, text: null, codePresent: true, code: null, valuePresent: true, value: null },
  { specimenId: 'Specimen/s4', patientId: null, observationId: null, patientPresent: false, observationPresent: false, textPresent: true, text: null, codePresent: true, code: null, valuePresent: true, value: null },
];

test('saved preview correlation resolves the current reconcile among repeated request IDs', () => {
  const fixture = JSON.parse(readFileSync(new URL('./fixtures/wave149-saved-preview-reconcile.json', import.meta.url), 'utf8'));
  const { authoringRequests, outputId, state } = fixture;
  const preview = authoringRequests.find(request => request.endpoint === 'preview');
  const reconciles = authoringRequests.filter(request => request.endpoint === 'reconcile');
  const stale = reconciles.find(request => request.requestDraftVersion === 3);
  const current = reconciles.find(request => request.requestDraftVersion === 6 && request.requestDraftDigest === state.draftDigest);
  const wrongDigest = reconciles.find(request => request.requestDraftDigest !== state.draftDigest && request.requestDraftVersion === state.draftVersion);

  assert.equal(reconciles.length, 3);
  assert.equal(new Set(reconciles.map(request => request.requestId)).size, 1);
  assert.notEqual(stale.responseReceiptId, preview.requestReceiptId);
  assert.equal(current.responseReceiptId, preview.requestReceiptId);
  assert.equal(selectSavedPreviewReconcile(authoringRequests, preview, state, outputId), current);

  assert.throws(
    () => selectSavedPreviewReconcile([stale, wrongDigest, preview], preview, state, outputId),
    /exactly one successful reconcile.*found 0/,
  );
  const wrongSnapshot = { ...current, body: { ...current.body, snapshotToken: 'sha256:wrong-snapshot' } };
  assert.throws(
    () => selectSavedPreviewReconcile([wrongSnapshot, preview], preview, state, outputId),
    /exactly one successful reconcile.*found 0/,
  );
  const wrongReceipt = { ...current, responseReceiptId: 'receipt_wrong', response: { ...current.response, receiptId: 'receipt_wrong' } };
  assert.throws(
    () => selectSavedPreviewReconcile([wrongReceipt, preview], preview, state, outputId),
    /exactly one successful reconcile.*found 0/,
  );
  const wrongOutput = { ...current, response: { ...current.response, builder: { documents: [{ output: { id: 'out_other' } }] } } };
  assert.throws(
    () => selectSavedPreviewReconcile([wrongOutput, preview], preview, state, outputId),
    /exactly one successful reconcile.*found 0/,
  );
  assert.throws(
    () => selectSavedPreviewReconcile([...authoringRequests, structuredClone(current)], preview, state, outputId),
    /exactly one successful reconcile.*found 2/,
  );
  const wrongPreviewReceipt = { ...preview, responseReceiptId: 'receipt_wrong', response: { ...preview.response, receiptId: 'receipt_wrong' } };
  assert.throws(
    () => selectSavedPreviewReconcile([current, wrongPreviewReceipt], wrongPreviewReceipt, state, outputId),
    /Saved preview response receipt must match its request receipt/,
  );
});

test('native Pivot source selection verifies role-specific controlled rerenders', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<!doctype html>
      <fieldset id="groups">
        <input type="checkbox" aria-label="Pivot group Specimen ID" checked>
        <select aria-label="Add pivot group field">
          <option value="">Add a field to identify each row</option>
          <option value="source:group-choice">Observation.valueCodeableConcept.text</option>
          <option value="source:wrong-group-choice">Unrelated group field</option>
        </select>
      </fieldset>
      <label>Category field
        <select aria-label="Pivot category field">
          <option value="">Choose a category field</option>
          <optgroup label="Available source fields"><option value="source:category-choice">Observation.valueQuantity.code</option></optgroup>
          <optgroup label="Available source fields"><option value="source:wrong-category-choice">Unrelated category field</option></optgroup>
        </select>
      </label>
      <label>Values field
        <select aria-label="Pivot values field">
          <option value="">Choose a values field</option>
          <optgroup label="Available source fields"><option value="source:value-choice">Observation.valueQuantity.value</option></optgroup>
        </select>
      </label>
      <script>
        const groupPicker = document.querySelector('select[aria-label="Add pivot group field"]');
        groupPicker.addEventListener('change', () => {
          const source = groupPicker.selectedOptions[0];
          const checkbox = document.createElement('input');
          checkbox.type = 'checkbox';
          checkbox.setAttribute('aria-label', 'Pivot group ' + source.textContent);
          checkbox.checked = true;
          document.querySelector('#groups').insertBefore(checkbox, groupPicker);
          source.remove();
          groupPicker.value = '';
        });
        let outputId = 0;
        for (const [label, id] of [['Pivot category field', 'category'], ['Pivot values field', 'value']]) {
          const picker = document.querySelector('select[aria-label="' + label + '"]');
          picker.addEventListener('change', () => {
            const source = picker.selectedOptions[0];
            const output = new Option(source.textContent, 'pivot-input-' + id + '-' + (++outputId));
            source.remove();
            picker.add(output);
            picker.value = output.value;
          });
        }
      </script>`);

    const selectOption = (selector, value) => page.locator(selector).selectOption(value);
    const waitFor = (predicate, args, timeout) => waitForBrowser(page, predicate, args, timeout);
    const group = await selectPivotSourceChoice({
      page, label: 'Add pivot group field', path: 'Observation.valueCodeableConcept.text',
      value: 'source:group-choice', role: 'group', selectOption, waitFor,
    });
    assert.deepEqual(group, { role: 'group', addedGroupLabel: 'Pivot group Observation.valueCodeableConcept.text', pickerValue: '' });
    assert.equal(await page.getByLabel('Pivot group Observation.valueCodeableConcept.text', { exact: true }).isChecked(), true);
    assert.equal(await page.getByLabel('Add pivot group field', { exact: true }).inputValue(), '');

    const category = await selectPivotSourceChoice({
      page, label: 'Pivot category field', path: 'Observation.valueQuantity.code',
      value: 'source:category-choice', role: 'category', selectOption, waitFor,
    });
    assert.equal(category.columnId, 'pivot-input-category-1');
    assert.equal(category.label, 'Observation.valueQuantity.code');
    assert.equal(category.sourceOptionPresent, false);

    const value = await selectPivotSourceChoice({
      page, label: 'Pivot values field', path: 'Observation.valueQuantity.value',
      value: 'source:value-choice', role: 'value', selectOption, waitFor,
    });
    assert.equal(value.columnId, 'pivot-input-value-2');
    assert.equal(value.label, 'Observation.valueQuantity.value');
    assert.equal(value.sourceOptionPresent, false);

    await assert.rejects(selectPivotSourceChoice({
      page, label: 'Add pivot group field', path: 'Observation.valueCodeableConcept.text',
      value: 'source:wrong-group-choice', role: 'group', selectOption, waitFor,
    }), /New Pivot group member .* must identify Observation\.valueCodeableConcept\.text/);
    await assert.rejects(selectPivotSourceChoice({
      page, label: 'Pivot category field', path: 'Observation.valueQuantity.code',
      value: 'source:wrong-category-choice', role: 'category', selectOption, waitFor,
    }), /category output field Unrelated category field must identify Observation\.valueQuantity\.code/);
  } finally {
    await browser.close();
  }
});

test('serialized table readiness receives outputId through Playwright args', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent('<div data-testid="construction-table-output-a"></div><div data-testid="construction-table-output-b"></div>');
    const outputId = 'output-b';
    const oldCapturedPredicate = () => Boolean(document.querySelector(`[data-testid="construction-table-${outputId}"]`));

    await assert.rejects(page.waitForFunction(oldCapturedPredicate, undefined, { timeout: 1000 }), /outputId is not defined/);
    const selected = await page.waitForFunction(constructionTableReady, { outputId }, { timeout: 1000 });
    assert.equal(await selected.jsonValue(), true);
    assert.equal(await page.evaluate(constructionTableReady, { outputId: 'missing-output' }), false);
    await selected.dispose();
  } finally {
    await browser.close();
  }
});

test('execute-file transport preserves escaped AQL bind markers after the owned shell invocation', () => {
  const request = buildRelatedQuantityPivotOracle({
    scope_allowed: true,
    project: 'loom_dev_cda_fhir',
    dataset_generation: 'cda-fhir-v1',
    emptyPolicies: ['PRESERVE_PARENT', 'PRESERVE_PARENT'],
    auth_resource_paths_unrestricted: true,
    auth_resource_paths: [],
  }, { visibleTextKeys: ['FINAL', null] });
  const { query, bindVars } = request;
  const resultMarker = '__LOOM_RELATED_QUANTITY_ORACLE__';
  const script = buildRelatedQuantityOracleExecuteScript(query, bindVars, resultMarker);

  assert.match(query, /@project/);
  assert.match(query, /@visible_text_keys/);
  assert.doesNotMatch(script, /@(project|visible_text_keys)\b/);
  assert.match(script, /\\u0040project/);
  assert.match(script, /\\u0040visible_text_keys/);

  const invocation = buildArangoShellInvocation({ container: 'owned-arango-test', script, database: 'loom_dev' });
  const shellCommand = invocation.args.at(-1);
  const shellHarness = `arangosh() {
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--javascript.execute" ]; then
      shift
      cat "$1"
      return 0
    fi
    shift
  done
  return 42
}
${shellCommand}`;
  const transported = spawnSync('sh', ['-c', shellHarness], {
    encoding: 'utf8',
    env: { ...process.env, ARANGO_ROOT_PASSWORD: 'fixture-only-secret' },
  });
  assert.equal(transported.status, 0, transported.stderr);
  assert.equal(transported.stdout, script, 'owned invocation shell quoting must preserve the execute-string source');
  assert.equal(transported.stdout.includes('fixture-only-secret'), false, 'the local shell shim must not expose the container password');

  const calls = [];
  const printed = [];
  vm.runInNewContext(transported.stdout, {
    db: {
      _query(actualQuery, actualBindVars, options) {
        calls.push({ query: actualQuery, bindVars: actualBindVars, options });
        return { toArray: () => [{ fixture: true }] };
      },
    },
    print: value => printed.push(value),
  });
  assert.deepEqual(JSON.parse(JSON.stringify(calls)), [{
    query,
    bindVars,
    options: { maxRuntime: 30, memoryLimit: 268435456 },
  }]);
  assert.deepEqual(printed, [`${resultMarker}{"ok":true,"rows":[{"fixture":true}]}`]);
});

const categoryOutputs = [
  { key: { kind: 'MISSING' }, output: { name: 'quantity_missing', label: 'Missing' } },
  { key: { kind: 'NULL' }, output: { name: 'quantity_null', label: 'NULL' } },
  { key: { kind: 'STRING', string: 'd' }, output: { name: 'quantity_d', label: 'd' } },
];

const streamResultFromLiteralRows = (rawRows, scope, visibleTextKeys) => {
  const oracle = summarizeRelatedQuantityRows(rawRows);
  const categories = new Map();
  const allCells = new Map();
  for (const group of oracle.groups) {
    const text = group.groupTextPresent ? group.groupText : null;
    const category = { categoryPresent: group.categoryPresent, categoryType: !group.categoryPresent ? 'MISSING' : group.category === null ? 'NULL' : 'STRING', category: group.category };
    categories.set(JSON.stringify([category.categoryPresent, category.categoryType, category.category]), category);
    const identity = JSON.stringify([text, category.categoryPresent, category.category]);
    const current = allCells.get(identity) ?? {
      text, ...category, routeRows: 0, numericCount: 0, sum: 0, max: null,
      missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 0, nonNumericValueCount: 0,
    };
    current.routeRows += group.sourceRows;
    current.numericCount += group.numericRows;
    current.sum += group.valueSum;
    current.missingValueCount += group.missingValueRows;
    current.explicitNullValueCount += group.explicitNullValueRows;
    current.terminalNullValueRows += group.terminalNullValueRows;
    current.nonNumericValueCount += group.nonNumericValueRows;
    if (group.valueMax !== null) current.max = current.max === null ? group.valueMax : Math.max(current.max, group.valueMax);
    allCells.set(identity, current);
  }
  const selected = new Set(visibleTextKeys.map(JSON.stringify));
  const rawTypedTextCodePreview = oracle.groups.filter(group => selected.has(JSON.stringify(group.groupTextPresent ? group.groupText : null))).map(group => ({
    textPresent: group.groupTextPresent,
    textType: !group.groupTextPresent ? 'MISSING' : group.groupText === null ? 'NULL' : 'STRING',
    text: group.groupText,
    codePresent: group.categoryPresent,
    codeType: !group.categoryPresent ? 'MISSING' : group.category === null ? 'NULL' : 'STRING',
    code: group.category,
    routeRows: group.sourceRows,
    textMissingRows: group.missingTextRows,
    textNullRows: group.explicitNullTextRows,
    missingValueCount: group.missingValueRows,
    explicitNullValueCount: group.explicitNullValueRows,
    terminalNullValueRows: group.terminalNullValueRows,
    numericCount: group.numericRows,
    nonNumericValueCount: group.nonNumericValueRows,
    numericSum: group.valueSum,
    numericMax: group.valueMax,
  }));
  const visiblePivotCellPreview = [...allCells.values()].filter(cell => selected.has(JSON.stringify(cell.text)))
    .map(cell => ({ ...cell, sum: cell.numericCount === 0 ? null : cell.sum }));
  const dGroups = [...allCells.values()].filter(cell => cell.categoryPresent && cell.category === 'd');
  const dDuplicateWitnesses = dGroups
    .filter(group => group.routeRows > 1 && group.numericCount > 1 && group.sum !== group.max)
    .sort((left, right) => left.text === right.text ? 0 : left.text === null ? -1 : right.text === null ? 1 : left.text.localeCompare(right.text))
    .map(group => ({ ...group }));
  const counts = {
    totalSpecimens: oracle.specimenCount,
    emptyFirstHopSpecimenRoots: oracle.emptySpecimenCount,
    emptySecondHopPatientRows: oracle.emptyPatientObservationCount,
    actualRouteRows: oracle.matchedObservationRows,
    leftJoinOutputRows: oracle.sourceRows,
    rawTextTypedCodeGroupCount: oracle.groups.length,
    visibleTextGroupCount: new Set(oracle.groups.map(group => JSON.stringify(group.groupTextPresent ? group.groupText : null))).size,
    visiblePivotCellCount: allCells.size,
    categoryDomainCount: categories.size,
    nonNumericValueCount: oracle.groups.reduce((sum, group) => sum + group.nonNumericValueRows, 0),
  };
  const selectedTextGroupCount = new Set(oracle.groups.filter(group => selected.has(JSON.stringify(group.groupTextPresent ? group.groupText : null)))
    .map(group => JSON.stringify(group.groupTextPresent ? group.groupText : null))).size;
  return {
    project: scope.project,
    generation: scope.dataset_generation,
    authScope: {
      auth_resource_paths_unrestricted: scope.auth_resource_paths_unrestricted,
      auth_resource_paths: scope.auth_resource_paths,
      scope_allowed: scope.scope_allowed,
    },
    selectedTextGroupKeys: visibleTextKeys,
    selectedTextGroupCount: visibleTextKeys.length,
    selectedMatchedTextGroupCount: selectedTextGroupCount,
    selectedTextKeysUnique: new Set(visibleTextKeys.map(JSON.stringify)).size === visibleTextKeys.length,
    selectedTextKeysWithinLimit: visibleTextKeys.length <= 25,
    selectedTextKeysValid: visibleTextKeys.every(value => value === null || typeof value === 'string'),
    selectedRawGroupCount: rawTypedTextCodePreview.length,
    selectedPivotCellCount: visiblePivotCellPreview.length,
    resultBounds: { maxPreviewRows: 100, categoryDomainCountFits: true },
    factoredRouteCounts: counts,
    rawTypedTextCodePreview,
    categoryDomainPreview: [...categories.values()],
    visiblePivotCellPreview,
    dDuplicateWitnessBucketCount: dDuplicateWitnesses.length,
    dDuplicateWitnesses: dDuplicateWitnesses.slice(0, 5),
  };
};

test('related quantity oracle preserves route multiplicity and missing/null presence while deriving SUM and MAX', () => {
  const oracle = { ...summarizeRelatedQuantityRows(rows), complete: true };
  assert.equal(oracle.sourceRows, 9, 'route multiplicity includes every terminal path and preserved empty parent');
  assert.deepEqual({ specimenCount: oracle.specimenCount, emptySpecimenCount: oracle.emptySpecimenCount, matchedPatientRows: oracle.matchedPatientRows,
    emptyPatientObservationCount: oracle.emptyPatientObservationCount, matchedObservationRows: oracle.matchedObservationRows,
    preservedEmptySpecimenRows: oracle.preservedEmptySpecimenRows, preservedEmptyPatientRows: oracle.preservedEmptyPatientRows },
  { specimenCount: 6, emptySpecimenCount: 1, matchedPatientRows: 5, emptyPatientObservationCount: 1, matchedObservationRows: 7,
    preservedEmptySpecimenRows: 1, preservedEmptyPatientRows: 1 });
  const finalDays = oracle.groups.find(group => group.groupText === 'FINAL' && group.category === 'd');
  assert.deepEqual({ sourceRows: finalDays.sourceRows, numericRows: finalDays.numericRows, valueSum: finalDays.valueSum, valueMax: finalDays.valueMax },
    { sourceRows: 3, numericRows: 3, valueSum: 10, valueMax: 5 });
  const finalMissingCode = oracle.groups.find(group => group.groupText === 'FINAL' && !group.categoryPresent);
  assert.deepEqual({ sourceRows: finalMissingCode.sourceRows, missingCodeRows: finalMissingCode.missingCodeRows, explicitNullCodeRows: finalMissingCode.explicitNullCodeRows },
    { sourceRows: 1, missingCodeRows: 1, explicitNullCodeRows: 0 });
  const finalNullCode = oracle.groups.find(group => group.groupText === 'FINAL' && group.categoryPresent && group.category === null);
  assert.deepEqual({ sourceRows: finalNullCode.sourceRows, missingCodeRows: finalNullCode.missingCodeRows, explicitNullCodeRows: finalNullCode.explicitNullCodeRows },
    { sourceRows: 1, missingCodeRows: 0, explicitNullCodeRows: 1 });
  assert.deepEqual(positiveRelatedQuantityDuplicateWitness(oracle), {
    text: { value: null }, category: { kind: 'STRING', string: 'd' },
    sourceRows: 2, numericRows: 2, sum: 7, max: 4,
  });
  const sum = expectedTextOnlyQuantityPivotRows(oracle, {
    groupColumn: { name: 'group_text', label: 'Observation.valueCodeableConcept.text' },
    categories: categoryOutputs,
    duplicatePolicy: 'SUM',
  });
  const max = expectedTextOnlyQuantityPivotRows(oracle, {
    groupColumn: { name: 'group_text', label: 'Observation.valueCodeableConcept.text' },
    categories: categoryOutputs,
    duplicatePolicy: 'MAX',
  });
  assert.equal(sum.rowCount, 2, 'missing and explicit-null text group values coalesce in the visible Pivot row');
  assert.deepEqual(
    sum.rows.map(row => [row.group_text, row.quantity_missing, row.quantity_null, row.quantity_d]).map(JSON.stringify).sort(),
    [['FINAL', 8, null, 10], [null, null, null, 7]].map(JSON.stringify).sort(),
  );
  assert.equal(sum.rows.find(row => row.group_text === 'FINAL').quantity_d, 10);
  assert.equal(max.rows.find(row => row.group_text === 'FINAL').quantity_d, 5);
  assert.equal(max.rows.find(row => row.group_text === null).quantity_d, 4, 'MAX combines missing and explicit-null text values for one output row');
  assert.notEqual(sum.rows.find(row => row.group_text === 'FINAL').quantity_d, max.rows.find(row => row.group_text === 'FINAL').quantity_d);
});

test('factored-route literal matches duplicate-edge dedup, shared-root multiplicity, and both preserved sentinels', () => {
  const fixtureEdges = {
    specimenPatient: [
      ['Specimen/s1', 'Patient/p1'], ['Specimen/s1', 'Patient/p1'],
      ['Specimen/s2', 'Patient/p1'], ['Specimen/s3', 'Patient/p2'],
    ],
    observationPatient: [
      ['Observation/o1', 'Patient/p1'], ['Observation/o1', 'Patient/p1'],
      ['Observation/o2', 'Patient/p1'], ['Observation/o3', 'Patient/p1'],
      ['Observation/o4', 'Patient/p1'], ['Observation/o5', 'Patient/p1'],
    ],
  };
  const uniqueSpecimenPatientPairs = new Set(fixtureEdges.specimenPatient.map(JSON.stringify));
  const uniqueObservationPatientPairs = new Set(fixtureEdges.observationPatient.map(JSON.stringify));
  assert.equal(fixtureEdges.specimenPatient.length, 4);
  assert.equal(uniqueSpecimenPatientPairs.size, 3, 'duplicate Specimen→Patient edges do not duplicate the path');
  assert.equal(fixtureEdges.observationPatient.length, 6);
  assert.equal(uniqueObservationPatientPairs.size, 5, 'duplicate Observation→Patient edges do not duplicate the path');
  assert.equal(factoredFixtureRows.length, 12, 'the shared Patient observations repeat once per distinct Specimen root plus both parent sentinels');

  const oracle = summarizeRelatedQuantityRows(factoredFixtureRows);
  assert.deepEqual({ sourceRows: oracle.sourceRows, specimens: oracle.specimenCount, emptySpecimens: oracle.emptySpecimenCount,
    patientPaths: oracle.matchedPatientRows, emptyPatients: oracle.emptyPatientObservationCount,
    observations: oracle.matchedObservationRows, emptySpecimenRows: oracle.preservedEmptySpecimenRows,
    emptyPatientRows: oracle.preservedEmptyPatientRows },
  { sourceRows: 12, specimens: 4, emptySpecimens: 1, patientPaths: 3, emptyPatients: 1,
    observations: 10, emptySpecimenRows: 1, emptyPatientRows: 1 });
  const nullTextDays = oracle.groups.find(group => !group.groupTextPresent && group.category === 'd');
  const explicitNullTextDays = oracle.groups.find(group => group.groupTextPresent && group.groupText === null && group.category === 'd');
  assert.deepEqual([nullTextDays.sourceRows, nullTextDays.missingTextRows, nullTextDays.numericRows, nullTextDays.missingValueRows, nullTextDays.valueSum, nullTextDays.valueMax], [4, 4, 2, 2, 4, 2]);
  assert.deepEqual([explicitNullTextDays.sourceRows, explicitNullTextDays.explicitNullTextRows, explicitNullTextDays.numericRows, explicitNullTextDays.valueSum, explicitNullTextDays.valueMax], [2, 2, 2, 10, 5]);
  assert.equal(positiveRelatedQuantityDuplicateWitness(oracle).sum, 14);
  assert.equal(positiveRelatedQuantityDuplicateWitness(oracle).max, 5);
  assert.equal(oracle.groups.reduce((sum, group) => sum + group.nonNumericValueRows, 0), 2);
  assert.equal(oracle.groups.reduce((sum, group) => sum + group.terminalNullValueRows, 0), 2,
    'the no-Patient and no-Observation PRESERVE_PARENT rows are distinct terminal NULLs');
  assert.throws(() => expectedTextOnlyQuantityPivotRows({ ...oracle, complete: true }, {
    groupColumn: { name: 'group_text', label: 'Observation.valueCodeableConcept.text' },
    categories: categoryOutputs,
    duplicatePolicy: 'SUM',
  }), /cannot prove rows with non-null nonnumeric Observation\.valueQuantity\.value values/);
});

test('one Observation linked to distinct Patients contributes once to each Patient route', () => {
  // One raw Observation document has two subject_Patient edges and is reached
  // through two different Specimen roots. The route oracle must retain both
  // Patient lineages rather than globally deduplicating the Observation ID.
  const rows = [
    { specimenId: 'Specimen/root-a', patientId: 'Patient/patient-a', observationId: 'Observation/shared', patientPresent: true, observationPresent: true, textPresent: true, text: 'FINAL', codePresent: true, code: 'd', valuePresent: true, value: 7 },
    { specimenId: 'Specimen/root-b', patientId: 'Patient/patient-b', observationId: 'Observation/shared', patientPresent: true, observationPresent: true, textPresent: true, text: 'FINAL', codePresent: true, code: 'd', valuePresent: true, value: 7 },
  ];
  const patientsByObservation = new Map();
  for (const row of rows) {
    const patients = patientsByObservation.get(row.observationId) ?? new Set();
    patients.add(row.patientId);
    patientsByObservation.set(row.observationId, patients);
  }
  assert.deepEqual([...patientsByObservation.get('Observation/shared')], ['Patient/patient-a', 'Patient/patient-b']);

  const oracle = summarizeRelatedQuantityRows(rows);
  assert.deepEqual({ sourceRows: oracle.sourceRows, specimens: oracle.specimenCount, patientPaths: oracle.matchedPatientRows,
    observations: oracle.matchedObservationRows, sum: oracle.groups[0].valueSum, max: oracle.groups[0].valueMax },
  { sourceRows: 2, specimens: 2, patientPaths: 2, observations: 2, sum: 14, max: 7 });
});

test('raw related-route oracle is scoped to the exact Specimen→Patient→Observation joins and retains code/text presence', () => {
  const scope = {
    project: 'loom_dev_cda_fhir', dataset_generation: 'cda-fhir-v1',
    emptyPolicies: ['PRESERVE_PARENT', 'PRESERVE_PARENT'],
    auth_resource_paths: [], auth_resource_paths_unrestricted: true, scope_allowed: true,
  };
  const visibleTextKeys = [null, 'FINAL'];
  const { query, bindVars, visibleTextKeys: boundTextKeys, previewLimit } = buildRelatedQuantityPivotOracle(scope, { visibleTextKeys });
  assert.deepEqual(bindVars, {
    project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1',
    auth_resource_paths: [], auth_resource_paths_unrestricted: true, scope_allowed: true,
    visible_text_keys: visibleTextKeys,
  });
  assert.deepEqual(boundTextKeys, visibleTextKeys);
  assert.equal(previewLimit, 25, 'selected text-key preview cannot exceed the native 25-row UI preview');
  assert.match(query, /LET selectedTextGroupKeys = @visible_text_keys/);
  assert.match(query, /selectedTextGroupKeys, selectedTextGroupCount, selectedMatchedTextGroupCount, selectedTextKeysUnique, selectedTextKeysWithinLimit, selectedTextKeysValid/);
  assert.match(query, /FILTER c\.text IN selectedTextGroupKeys/);
  assert.match(query, /s\.payload\.resourceType == "Specimen"/);
  assert.match(query, /eSP\.from_type == "Specimen" AND eSP\.to_type == "Patient"/);
  assert.match(query, /eOP\.from_type == "Observation" AND eOP\.to_type == "Patient"/);
  assert.match(query, /o\.payload\.resourceType == "Observation"/);
  assert.match(query, /terminalNullValueRows = SUM\(o == null \? 1 : 0\)/);
  assert.throws(() => buildRelatedQuantityPivotOracle(scope), /exact non-empty visible UI text keys/);
  assert.throws(() => buildRelatedQuantityPivotOracle(scope, { visibleTextKeys: ['FINAL', 'FINAL'] }), /must be unique/);
  assert.throws(() => buildRelatedQuantityPivotOracle(scope, { visibleTextKeys: ['FINAL', 4] }), /capped at 25/);
  assert.throws(() => buildRelatedQuantityPivotOracle(scope, { visibleTextKeys: Array.from({ length: 26 }, (_, index) => `text-${index}`) }), /capped at 25/);
  assert.throws(() => buildRelatedQuantityPivotOracle({
    project: 'loom_dev_cda_fhir', dataset_generation: 'cda-fhir-v1',
    emptyPolicies: ['PRESERVE_PARENT', 'EXCLUDE'],
    auth_resource_paths: [], auth_resource_paths_unrestricted: true, scope_allowed: true,
  }), /requires PRESERVE_PARENT at both authored Related boundaries/);
});

test('oracle validation rejects route-count loss, missing presence counts, and collapsed missing/null categories', () => {
  const oracle = { ...summarizeRelatedQuantityRows(rows), complete: true };
  assert.throws(() => validateRelatedQuantityPivotOracle({ ...oracle, sourceRows: 6 }), /Related route rows must account for matched observations and preserved empty parents/);
  const missingPresence = structuredClone(oracle);
  delete missingPresence.groups[0].missingCodeRows;
  assert.throws(() => validateRelatedQuantityPivotOracle(missingPresence), /missingCodeRows/);
  const collapsedCode = structuredClone(oracle);
  const missingCode = collapsedCode.groups.find(group => group.groupText === 'FINAL' && !group.categoryPresent);
  missingCode.categoryPresent = true;
  assert.throws(() => validateRelatedQuantityPivotOracle(collapsedCode), /typed category identity must match its missing\/null presence counts/);
  assert.throws(() => summarizeRelatedQuantityRows([...rows, rows[0]]), /deduplicate terminal identities per Specimen root/);
  assert.throws(() => buildRelatedQuantityPivotOracle({ project: 'loom_dev_cda_fhir', dataset_generation: 'cda-fhir-v1',
    auth_resource_paths: [], auth_resource_paths_unrestricted: true, scope_allowed: true }), /requires PRESERVE_PARENT at both authored Related boundaries/);
  assert.throws(() => expectedTextOnlyQuantityPivotRows(oracle, {
    groupColumn: { name: 'group_text', label: 'Observation.valueCodeableConcept.text' },
    categories: [{ key: { kind: 'STRING', string: 'wrong' }, output: { name: 'wrong', label: 'wrong' } }],
    duplicatePolicy: 'SUM',
  }), /exactly cover the raw related-route category domain/);
  const nonnumericRows = [...rows, {
    specimenId: 'specimen-g', patientId: 'patient-g', observationId: 'obs-8', patientPresent: true, observationPresent: true,
    textPresent: true, text: 'FINAL', codePresent: true, code: 'd', valuePresent: true, value: 'not-numeric',
  }];
  const nonnumericOracle = { ...summarizeRelatedQuantityRows(nonnumericRows), complete: true };
  assert.throws(() => expectedTextOnlyQuantityPivotRows(nonnumericOracle, {
    groupColumn: { name: 'group_text', label: 'Observation.valueCodeableConcept.text' },
    categories: categoryOutputs,
    duplicatePolicy: 'SUM',
  }), /cannot prove rows with non-null nonnumeric Observation\.valueQuantity\.value/);
});

test('streaming oracle adapter accepts only a complete bounded result for the requested full route', () => {
  const scope = {
    project: 'loom_dev_cda_fhir', dataset_generation: 'cda-fhir-v1',
    emptyPolicies: ['PRESERVE_PARENT', 'PRESERVE_PARENT'],
    auth_resource_paths: [], auth_resource_paths_unrestricted: true, scope_allowed: true,
  };
  const streamRows = [...rows, {
    specimenId: 'specimen-g', patientId: 'patient-g', observationId: 'obs-8', patientPresent: true, observationPresent: true,
    textPresent: true, text: 'ZZZ', codePresent: true, code: 'd', valuePresent: true, value: 100,
  }];
  const visibleTextKeys = [null, 'FINAL'];
  const payload = streamResultFromLiteralRows(streamRows, scope, visibleTextKeys);
  const oracle = summarizeRelatedQuantityPivotOracleResult([payload], scope, { visibleTextKeys });
  assert.equal(oracle.complete, true);
  assert.equal(oracle.sourceRows, streamRows.length);
  assert.equal(oracle.sampleSourceRows, rows.length);
  assert.equal(oracle.fullGroupCount, 3, 'full text-group count remains separate from the visible selected preview');
  assert.deepEqual(oracle.previewTextGroupKeys, visibleTextKeys, 'selected keys follow UI row order rather than a lexical oracle order');
  const expected = expectedTextOnlyQuantityPivotRows(oracle, {
    groupColumn: { name: 'group_text', label: 'Observation.valueCodeableConcept.text' },
    categories: categoryOutputs,
    duplicatePolicy: 'SUM',
  });
  assert.equal(expected.rowCount, 2);
  assert.equal(expected.fullGroupCount, 3);
  assert.equal(expected.sourceRows, streamRows.length);
  assert.equal(expected.previewSourceRows, rows.length);
  assert.deepEqual(expected.completeTextKeys, visibleTextKeys);
  assert.equal(expected.rows.find(row => row.group_text === 'FINAL').quantity_d, 10);
  assert.equal(expected.rows[0].group_text, null);
  const truncated = structuredClone(payload);
  truncated.selectedPivotCellCount += 1;
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(truncated, scope, { visibleTextKeys }), /not complete for its bounded request/);
  const wrongScope = structuredClone(payload);
  wrongScope.generation = 'another-generation';
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(wrongScope, scope, { visibleTextKeys }), /does not match the requested scope/);
  const wrongTextKeys = structuredClone(payload);
  wrongTextKeys.selectedTextGroupKeys = ['FINAL', null];
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(wrongTextKeys, scope, { visibleTextKeys }), /did not bind the exact visible UI group keys/);
  const missingTextKey = structuredClone(payload);
  missingTextKey.selectedMatchedTextGroupCount -= 1;
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(missingTextKey, scope, { visibleTextKeys }), /did not find every visible UI group key/);
  const unvalidatedTextKey = structuredClone(payload);
  unvalidatedTextKey.selectedTextKeysValid = false;
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(unvalidatedTextKey, scope, { visibleTextKeys }), /rejected the visible UI group-key request/);
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(payload, scope, { visibleTextKeys: [null, null] }), /must be unique/);
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(payload, scope), /exact non-empty visible UI group keys/);
  const nonnumericOutsidePreview = structuredClone(payload);
  nonnumericOutsidePreview.factoredRouteCounts.nonNumericValueCount = 1;
  const nonnumericFullOracle = summarizeRelatedQuantityPivotOracleResult(nonnumericOutsidePreview, scope, { visibleTextKeys });
  assert.throws(() => expectedTextOnlyQuantityPivotRows(nonnumericFullOracle, {
    groupColumn: { name: 'group_text', label: 'Observation.valueCodeableConcept.text' },
    categories: categoryOutputs,
    duplicatePolicy: 'SUM',
  }), /cannot prove rows with non-null nonnumeric Observation\.valueQuantity\.value values/);
  const extraUnrequestedGroup = structuredClone(payload);
  extraUnrequestedGroup.rawTypedTextCodePreview.push({
    textPresent: true, textType: 'STRING', text: 'ZZZ', codePresent: true, codeType: 'STRING', code: 'd',
    routeRows: 1, textMissingRows: 0, textNullRows: 0, missingValueCount: 0, explicitNullValueCount: 0,
    terminalNullValueRows: 0, numericCount: 1, nonNumericValueCount: 0, numericSum: 100, numericMax: 100,
  });
  extraUnrequestedGroup.selectedRawGroupCount += 1;
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(extraUnrequestedGroup, scope, { visibleTextKeys }), /outside the exact visible UI group-key request/);
  const incompleteCategoryDomain = structuredClone(payload);
  incompleteCategoryDomain.categoryDomainPreview.pop();
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(incompleteCategoryDomain, scope, { visibleTextKeys }), /categoryDomainPreview is not complete/);
  const witnessOutsidePreviewPayload = streamResultFromLiteralRows(streamRows, scope, ['ZZZ']);
  const witnessOutsidePreview = summarizeRelatedQuantityPivotOracleResult(witnessOutsidePreviewPayload, scope, { visibleTextKeys: ['ZZZ'] });
  const witnessOutsideExpected = expectedTextOnlyQuantityPivotRows(witnessOutsidePreview, {
    groupColumn: { name: 'group_text', label: 'Observation.valueCodeableConcept.text' },
    categories: categoryOutputs,
    duplicatePolicy: 'SUM',
  });
  assert.equal(witnessOutsideExpected.positiveDuplicateWitness, null, 'global raw witness alone does not claim a visible SUM/MAX bucket was exercised');
  assert.equal(witnessOutsidePreview.dDuplicateWitnessBucketCount, 2);
  assert.equal(witnessOutsidePreview.dDuplicateWitnesses.length, 2, 'the independent full-route witness remains available for UI row navigation');
  const largeFullDomain = structuredClone(payload);
  largeFullDomain.factoredRouteCounts.rawTextTypedCodeGroupCount = 1800;
  largeFullDomain.factoredRouteCounts.visibleTextGroupCount = 1200;
  largeFullDomain.factoredRouteCounts.visiblePivotCellCount = 1500;
  largeFullDomain.factoredRouteCounts.categoryDomainCount = 30;
  largeFullDomain.categoryDomainCount = 30;
  largeFullDomain.categoryDomainPreview = Array.from({ length: 30 }, (_, index) => ({
    categoryPresent: true, categoryType: 'STRING', category: `category-${index}`,
  }));
  assert.equal(summarizeRelatedQuantityPivotOracleResult(largeFullDomain, scope, { visibleTextKeys }).fullCellCount, 1500,
    'the 25-row UI-key limit does not cap the complete category domain at 25');
  const overLimitCategoryDomain = structuredClone(largeFullDomain);
  overLimitCategoryDomain.factoredRouteCounts.categoryDomainCount = 257;
  overLimitCategoryDomain.categoryDomainCount = 257;
  overLimitCategoryDomain.categoryDomainPreview = Array.from({ length: 257 }, (_, index) => ({
    categoryPresent: true, categoryType: 'STRING', category: `category-${index}`,
  }));
  assert.throws(() => summarizeRelatedQuantityPivotOracleResult(overLimitCategoryDomain, scope, { visibleTextKeys }), /category domain exceeds 256 categories/);
});
