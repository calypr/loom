import assert from 'node:assert/strict';
import test from 'node:test';
import { auditBrowserEvalSource, loadTypeScript, repositoryRoot } from './check-browser-eval-returns.mjs';

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
