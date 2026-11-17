import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { validateModelVerdict, configuredModelRules } from '../../../lib/formal-verdict.mjs';
import { tools } from '../../../lib/formal-tools.mjs';

const positive = () => ({
    exit: 0,
    counterexamples: [],
    states: { distinct: 8073, initial: 1, complete: true, cut: false },
    rules: [{ name: 'TypeOK', outcome: 'holds' }, { name: 'Inv_ReadyProposalIsCurrent', outcome: 'holds' }],
});
const negative = () => ({
    exit: 1,
    counterexamples: [{ property: 'Inv_OldReturnPreservesCurrentApply', states: [{ action: 'Initial', vars: { phase: 'violated' } }] }],
    states: { distinct: 339, initial: 1, complete: false, cut: false },
    rules: [{ name: 'Inv_OldReturnPreservesCurrentApply', outcome: 'broken' }],
});
const expected = 'Inv_OldReturnPreservesCurrentApply';
const positiveRules = ['TypeOK', 'Inv_ReadyProposalIsCurrent'];
const negativeRules = [expected];

test('structured current-code passes require complete nonempty checked state space', () => {
    const result = positive();
    assert.equal(validateModelVerdict(result, 0, undefined, positiveRules), result);
    for (const edit of [
        value => { value.states.distinct = 0; value.states.initial = 0; },
        value => { value.states.initial = 0; },
        value => { value.states.complete = false; },
        value => { value.states.cut = true; },
        value => { value.states.complete = 'true'; },
        value => { value.states.distinct = -1; },
        value => { value.states.distinct = NaN; },
        value => { value.states.initial = 9000; },
        value => { delete value.states; },
        value => { value.rules = []; },
        value => { value.rules[0].outcome = 'not checked'; },
        value => { value.rules[0].outcome = 'unknown'; },
        value => { value.rules.push({ ...value.rules[0] }); },
        value => { value.rules[0].name = ''; },
        value => { value.rules[1].outcome = 'broken'; },
        value => { value.exit = 2; },
    ]) {
        const changed = positive(); edit(changed);
        assert.throws(() => validateModelVerdict(changed, 0, undefined, positiveRules));
    }
    for (const exit of [null, 1, 2, 12, '0']) assert.throws(() => validateModelVerdict(positive(), exit, undefined, positiveRules));
    for (const value of [null, [], undefined, 'TLC reports success']) assert.throws(() => validateModelVerdict(value, 0, undefined, positiveRules));
});

test('negative controls require exactly their named violation and actual property-failure exits', () => {
    const result = negative();
    assert.equal(validateModelVerdict(result, 1, expected, negativeRules), result);
    for (const edit of [
        value => { value.exit = 0; },
        value => { value.exit = 2; },
        value => { value.rules[0].name = 'TypeOK'; },
        value => { value.rules[0].outcome = 'holds'; },
        value => { value.rules[0].outcome = 'not checked'; },
        value => { value.rules[0].outcome = 'unknown'; },
        value => { value.rules.push({ name: 'TypeOK', outcome: 'broken' }); },
        value => { value.states.cut = true; },
    ]) {
        const changed = negative(); edit(changed);
        assert.throws(() => validateModelVerdict(changed, 1, expected, negativeRules));
    }
    for (const exit of [null, 0, 2, 12]) assert.throws(() => validateModelVerdict(negative(), exit, expected, negativeRules));
    for (const name of ['', null, false]) assert.throws(() => validateModelVerdict(negative(), 1, name, negativeRules));
});

test('a genuine initial-state violation remains a caught control with zero recorded states', () => {
    const initial = { exit: 1, states: { distinct: 0, initial: 0, complete: false, cut: false },
        rules: [{ name: 'Inv_ApplyRequiresReadyPreview', outcome: 'broken' }],
        counterexamples: [{ property: 'Inv_ApplyRequiresReadyPreview', states: [{ action: '', vars: { canApply: true } }] }] };
    validateModelVerdict(initial, 1, 'Inv_ApplyRequiresReadyPreview', ['Inv_ApplyRequiresReadyPreview']);
    assert.throws(() => validateModelVerdict(initial, 0, undefined, ['Inv_ApplyRequiresReadyPreview']));
});

test('current tool resolver never falls back from an explicit invalid source-tree override', () => {
    const scratch = mkdtempSync(join(tmpdir(), 'loom-explicit-tool-test-'));
    const original = process.env.PSTACK_STATECHART_ROOT;
    const override = join(scratch, 'explicit-missing-statechart');
    try {
        process.env.PSTACK_STATECHART_ROOT = override;
        assert.throws(() => tools(), error => error.code === 'ENOENT' && error.path === override);
    } finally {
        if (original === undefined) delete process.env.PSTACK_STATECHART_ROOT;
        else process.env.PSTACK_STATECHART_ROOT = original;
        rmSync(scratch, { recursive: true, force: true });
    }
});


test('a successful verdict must account for every configured rule, not only TypeOK', () => {
    const rules = configuredModelRules('SPECIFICATION Spec\nINVARIANT TypeOK\nINVARIANT Inv_ReadyProposalIsCurrent\n');
    const result = positive();
    result.rules = [result.rules[0]];
    assert.throws(() => validateModelVerdict(result, 0, undefined, rules), /configured model rules/);
    assert.throws(() => validateModelVerdict(positive(), 0), /configured rule inventory/);
    assert.throws(() => validateModelVerdict(positive(), 0, undefined, ['TypeOK']), /configured model rules/);
    assert.throws(() => validateModelVerdict(positive(), 0, undefined, ['TypeOK', 'TypeOK']), /Duplicate configured/);
    assert.throws(() => configuredModelRules('INVARIANTS TypeOK Other\n'), /Unsupported/);
    assert.throws(() => configuredModelRules('INVARIANT\n TypeOK\n'), /Unsupported/);
    assert.throws(() => configuredModelRules('SPECIFICATION Spec\n'), /no model rules/);
    assert.deepEqual(configuredModelRules('  INVARIANT TypeOK \\* explanatory comment\nPROPERTY EventuallyReady\n'), ['TypeOK', 'EventuallyReady']);
});


test('caught controls retain a decoded path for the named invariant', () => {
    for (const edit of [
        value => { delete value.counterexamples; },
        value => { value.counterexamples = []; },
        value => { value.counterexamples.push(structuredClone(value.counterexamples[0])); },
        value => { value.counterexamples[0].property = 'DifferentInvariant'; },
        value => { value.counterexamples[0].states = []; },
        value => { value.counterexamples[0].states = [null]; },
        value => { value.counterexamples[0].states[0].action = null; },
        value => { delete value.counterexamples[0].states[0].vars; },
        value => { value.counterexamples[0].states[0].vars = {}; },
    ]) {
        const result = negative(); edit(result);
        assert.throws(() => validateModelVerdict(result, 1, expected, negativeRules));
    }
    const contradictory = positive();
    contradictory.counterexamples = negative().counterexamples;
    assert.throws(() => validateModelVerdict(contradictory, 0, undefined, positiveRules), /Successful verdict contains/);
});
