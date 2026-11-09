import test from 'node:test';
import assert from 'node:assert/strict';
import { derivedPolicyCases, derivedPolicyExpected, derivedPolicySummary, derivedPolicyFaults, derivedPolicyTest, validateDerivedPolicyPositive, validateDerivedPolicyFault, validateDerivedPolicyCounterexample, replayDerivedPolicyWitness } from './formal-derived-policy.mjs';

const packageName = 'github.com/calypr/loom/internal/server';
const copy = value => structuredClone(value);
const event = (Action, Test, Output) => ({ Package: packageName, Action, ...(Test ? { Test } : {}), ...(Output ? { Output } : {}) });
const stream = events => events.map(JSON.stringify).join('\n');
const inputs = derivedPolicyCases();
const positive = () => [event('run', derivedPolicyTest), ...inputs.flatMap(input => [event('run', `${derivedPolicyTest}/${input.id}`), event('pass', `${derivedPolicyTest}/${input.id}`)]), event('output', derivedPolicyTest, `DERIVED_ARITHMETIC_SUMMARY_JSON ${JSON.stringify(derivedPolicySummary)}\n`), event('pass', derivedPolicyTest), event('pass')];
function counterexample(proof) {
    const input = inputs.find(input => input.id === proof.case);
    const expected = proof.branch === 'operation' ? { kind: 'value', value: 4, errorMarker: '' }
        : proof.branch.endsWith('null') ? { kind: 'null', value: null, errorMarker: '' }
        : { kind: 'error', value: null, errorMarker: proof.branch === 'missing-error' ? 'DERIVED_MISSING_INPUT' : 'DERIVED_DIVISION_BY_ZERO' };
    return { case: input, expected, actual: { rows: 1, value: proof.branch === 'operation' ? 0 : proof.branch.endsWith('null') ? 9 : true, errorNumber: 0, errorMessage: '' } };
}
function negative(proof, report = counterexample(proof)) {
    const child = `${derivedPolicyTest}/${proof.case}`;
    return [event('run', derivedPolicyTest), event('run', child), event('output', child, `derived_policy_rows_integration_test.go:200: DERIVED_ARITHMETIC_FAILURE_JSON ${JSON.stringify(report)}\n`), event('fail', child), event('fail', derivedPolicyTest), event('fail')];
}

test('finite inputs keep nullable source, literal orientation and priority conformance distinct', () => {
    assert.equal(inputs.length, 400); assert.equal(new Set(inputs.map(input => input.id)).size, 400);
    assert.equal(inputs.filter(input => input.guardOrderConformance).length, 8);
    const half = inputs.find(input => input.sourceState === 'present' && input.sourceValue === 2 && input.literal === 4 && !input.sourceOnRight && input.operation === 'DIVIDE' && input.missingPolicy === 'ERROR' && input.zeroPolicy === 'ERROR');
    assert.deepEqual(derivedPolicyExpected(half), { kind: 'value', value: 0.5, errorMarker: '' });
    const opposite = { ...half, sourceOnRight: true, id: half.id.replace('reverse_false', 'reverse_true') };
    assert.equal(derivedPolicyExpected(opposite).value, 2);
    assert.throws(() => derivedPolicyExpected({ ...half, literal: 5 }), /Unknown or changed/);
});

test('positive evidence requires all 400 named executed cases and exact measured summary', () => {
    assert.deepEqual(validateDerivedPolicyPositive(stream(positive())), derivedPolicySummary);
    const changed = positive(); changed.splice(1, 2);
    assert.throws(() => validateDerivedPolicyPositive(stream(changed)), /Missing or duplicate/);
    const duplicate = positive(); duplicate.push(duplicate[1]);
    assert.throws(() => validateDerivedPolicyPositive(stream(duplicate)), /Missing or duplicate/);
    const wrongSummary = positive(); wrongSummary.find(value => value.Output).Output = `DERIVED_ARITHMETIC_SUMMARY_JSON ${JSON.stringify({ ...derivedPolicySummary, caseCount: 399 })}\n`;
    assert.throws(() => validateDerivedPolicyPositive(stream(wrongSummary)), /Incomplete/);
    assert.throws(() => validateDerivedPolicyPositive(stream([event('pass')])));
    const skipped = positive(); skipped[2].Action = 'skip';
    assert.throws(() => validateDerivedPolicyPositive(stream(skipped)), /skip/i);
});

test('each source fault requires its actual typed input, independent expectation and named violation', () => {
    for (const proof of derivedPolicyFaults) {
        const witness = validateDerivedPolicyFault(stream(negative(proof)), 1, proof);
        assert.equal(witness.property, proof.property); assert.equal(witness.case.id, proof.case);
        for (const other of derivedPolicyFaults.filter(value => value !== proof)) assert.throws(() => validateDerivedPolicyFault(stream(negative(proof)), 1, other));
        const wrongExpected = counterexample(proof); wrongExpected.expected.errorMarker = 'OTHER';
        assert.throws(() => validateDerivedPolicyFault(stream(negative(proof, wrongExpected)), 1, proof), /Changed independent/);
        const unknownInput = counterexample(proof); unknownInput.case = { ...unknownInput.case, literal: 999 };
        assert.throws(() => validateDerivedPolicyFault(stream(negative(proof, unknownInput)), 1, proof), /Unknown or changed/);
        const held = counterexample(proof);
        held.actual = held.expected.kind === 'error' ? { rows: 0, value: null, errorNumber: 1593, errorMessage: held.expected.errorMarker } : { rows: 1, value: held.expected.value, errorNumber: 0, errorMessage: '' };
        assert.throws(() => validateDerivedPolicyFault(stream(negative(proof, held)), 1, proof), /no actual policy violation/);
    }
});

test('compiler, transport, cleanup, foreign and duplicate output cannot receive property credit', () => {
    const proof = derivedPolicyFaults[0];
    for (const output of ['panic: controlled', '[build failed]', 'DERIVED_ARITHMETIC_QUERY invalid query', 'DERIVED_ARITHMETIC_COMPILE unsupported type', 'owned derived database cleanup HTTP500', 'unrelated failure']) {
        const events = negative(proof); events.splice(3, 0, event('output', derivedPolicyTest, output + '\n'));
        assert.throws(() => validateDerivedPolicyFault(stream(events), 1, proof));
    }
    const duplicate = negative(proof); duplicate.splice(3, 0, duplicate[2]);
    assert.throws(() => validateDerivedPolicyFault(stream(duplicate), 1, proof));
    const foreign = negative(proof); foreign[2].Package = 'foreign/package';
    assert.throws(() => validateDerivedPolicyFault(stream(foreign), 1, proof), /Foreign/);
    const wrongChild = negative(proof); wrongChild[2].Test = derivedPolicyTest;
    assert.throws(() => validateDerivedPolicyFault(stream(wrongChild), 1, proof));
    const unknownError = counterexample(proof); unknownError.actual = { rows: 0, value: null, errorNumber: 1501, errorMessage: 'DERIVED_MISSING_INPUT syntax error' };
    assert.throws(() => validateDerivedPolicyCounterexample(unknownError), /not policy evidence/);
    assert.throws(() => validateDerivedPolicyFault(stream(negative(proof)), 0, proof));
});

test('guard-order conformance and malformed observations cannot become public-policy counterexamples', () => {
    const input = inputs.find(input => input.guardOrderConformance);
    const report = { case: input, expected: derivedPolicyExpected(input), actual: { rows: 1, value: 9, errorNumber: 0, errorMessage: '' } };
    assert.throws(() => validateDerivedPolicyCounterexample(report), /conformance/);
    for (const edit of [value => { value.extra = true; }, value => { value.actual.rows = -1; }, value => { value.actual.value = {}; }, value => { value.actual.errorNumber = 1593; }, value => { value.case.guardOrderConformance = true; }]) {
        const changed = copy(counterexample(derivedPolicyFaults[0])); edit(changed);
        assert.throws(() => validateDerivedPolicyCounterexample(changed));
    }
});

test('held numeric serialization and invalid witness overrides cannot produce counterexample credit', () => {
    const report = counterexample(derivedPolicyFaults[0]); report.actual.value = '4';
    assert.throws(() => validateDerivedPolicyCounterexample(report), /no actual policy violation/);
    const witness = validateDerivedPolicyCounterexample(counterexample(derivedPolicyFaults[0]));
    assert.throws(() => replayDerivedPolicyWitness({ ...witness, foreign: true }, { output: '/tmp/not-created-derived-policy' }));
    assert.throws(() => replayDerivedPolicyWitness(witness, { output: '/tmp/not-created-derived-policy', productionSource: '' }), /Empty production/);
});
