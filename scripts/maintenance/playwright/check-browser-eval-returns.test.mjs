import assert from 'node:assert/strict';
import test from 'node:test';
import {
  auditBrowserEvalSource,
  auditBrowserWaitPredicateSyntax,
  loadTypeScript,
  repositoryRoot,
} from './check-browser-eval-returns.mjs';

const ts = loadTypeScript(repositoryRoot);

test('flags a value assignment whose only return belongs to a nested IIFE', () => {
  const findings = auditBrowserEvalSource(`
    const snapshot = await browserEval(cdp, \`(() => { const value = 1; return { value }; })()\`);
  `, 'cohort-regression.mjs', ts);

  assert.deepEqual(findings.map(({ kind }) => kind), ['missing-outer-return']);
});

test('ignores an effect-only awaited call without a return', () => {
  const findings = auditBrowserEvalSource(`
    await browserEval(cdp, 'document.querySelector("button").click();');
  `, 'effect-only.mjs', ts);

  assert.deepEqual(findings, []);
});

test('ignores browserEval calls explicitly discarded with void', () => {
  const findings = auditBrowserEvalSource(`
    void browserEval(cdp, 'document.querySelector("button").click();');
    void await browserEval(cdp, 'document.querySelector("button").click();');
  `, 'void-effect-only.mjs', ts);

  assert.deepEqual(findings, []);
});

test('accepts a top-level return with additional nested returns', () => {
  const findings = auditBrowserEvalSource(`
    const rows = await browserEval(cdp, 'return values.map(value => { return value.id; });');
  `, 'nested-return.mjs', ts);

  assert.deepEqual(findings, []);
});

test('flags returned and conditional values while keeping interpolated bodies unresolved', () => {
  const findings = auditBrowserEvalSource(`
    return browserEval(cdp, 'document.querySelector("button").click();');
    if (await browserEval(cdp, 'const present = true;')) {}
    const state = await browserEval(cdp, \`const select = document.querySelector(\${selector});\`);
  `, 'value-contexts.mjs', ts);

  assert.deepEqual(findings.map(({ kind }) => kind), [
    'missing-outer-return', 'missing-outer-return', 'unresolved',
  ]);
});

test('rejects the malformed edit and remove waitFunction predicates from the Group/Pivot Join failure', () => {
  const historicalBad = [
    "const editReady = async stepId => waitFunction(`Boolean(document.querySelector('[data-testid=\"construction-edit-step-${stepId}\"]:not(:disabled))`));",
    "const removeReady = async stepId => waitFunction(`Boolean(document.querySelector('[data-testid=\"construction-remove-step-${stepId}\"]:not(:disabled))`));",
  ].join('\n');
  assert.deepEqual(auditBrowserEvalSource(historicalBad, 'epoch90-history-readiness.mjs', ts), [],
    'The old return-only checker ignores waitFunction predicates.');
  const findings = auditBrowserWaitPredicateSyntax(historicalBad, 'epoch90-history-readiness.mjs', ts);

  assert.deepEqual(findings.map(({ kind, line }) => [kind, line]), [
    ['invalid-predicate-syntax', 1], ['invalid-predicate-syntax', 2],
  ]);
  assert(findings.every(finding => /Invalid or unexpected token/.test(finding.message)));

  const currentNativeWaits = [
    'const editAction = page.getByTestId("construction-edit-step-" + stepId);',
    "await action('Open saved Join history step', history, click, async () => waitEnabled(editAction));",
    'const removeAction = page.getByTestId("construction-remove-step-" + stepId);',
    "await action('Open saved Join history for removal', history, click, async () => waitEnabled(removeAction));",
  ].join('\n');
  assert.deepEqual(auditBrowserWaitPredicateSyntax(currentNativeWaits, 'current-native-readiness.mjs', ts), []);

  const correctedPredicate = "waitFunction(`Boolean(document.querySelector('[data-testid=\"construction-edit-step-${stepId}\"]:not(:disabled)'))`);";
  assert.deepEqual(auditBrowserWaitPredicateSyntax(correctedPredicate, 'corrected-readiness.mjs', ts), []);
});
