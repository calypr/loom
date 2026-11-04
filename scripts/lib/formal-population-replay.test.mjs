import test from 'node:test';
import assert from 'node:assert/strict';
import { expectedPopulation, validatePopulationInput, checkPopulationResults, populationCounterexampleWitness, replayPopulationWitness, populationProperties } from './formal-population-replay.mjs';

const input = extra => ({ authorizedScopeMatches: true, selectionComplete: true, selectionBindingMatches: true, memberReadCompletes: true, mappingCompletes: true, ...extra });
const concrete = (id, value) => ({ id, ...value, alias: false, deadline: false });
const observed = (id, value) => ({ id, ...expectedPopulation(value), error: expectedPopulation(value).outcome === 'rejected' ? 'controlled rejection' : '' });
const record = results => ({ schemaVersion: 1, results });
const initial = () => ({ populationPhase: 'choose', populationInput: input(), outcome: 'pending', memberReads: 0, mappingCalls: 0, counts: {} });

test('the finite population contract has successful, incomplete and rejected cases', () => {
    const fields = Object.keys(input()), totals = { rejected: 0, incomplete: 0, complete: 0 };
    for (let mask = 0; mask < 32; mask++) totals[expectedPopulation(Object.fromEntries(fields.map((field, index) => [field, Boolean(mask & (1 << index))]))).outcome]++;
    assert.deepEqual(totals, { rejected: 28, incomplete: 3, complete: 1 });
    for (const value of [null, {}, { ...input(), extra: true }, input({ mappingCompletes: 'true' })]) assert.throws(() => validatePopulationInput(value));
});

test('untrusted population observations cannot disclose counts or execute mapping', () => {
    const value = input({ selectionBindingMatches: false }), row = observed('untrusted', input());
    assert.throws(() => checkPopulationResults(record([row]), [concrete('untrusted', value)]), error => error.code === 'FORMAL_PROPERTY' && error.property === populationProperties[0] && error.witness.actual.mappingCalls === 1);
});

test('a mislabeled complete response cannot evade the incomplete input contract', () => {
    const value = input({ mappingCompletes: false });
    assert.throws(() => checkPopulationResults(record([observed('partial', input())]), [concrete('partial', value)]), error => error.code === 'FORMAL_PROPERTY' && error.property === populationProperties[1]);
});

test('rejecting all calls and wrong counts violate the complete contract', () => {
    const value = input();
    for (const row of [observed('complete', input({ selectionComplete: false })), { ...observed('complete', value), counts: [3, 1, 2, 1] }])
        assert.throws(() => checkPopulationResults(record([row]), [concrete('complete', value)]), error => error.code === 'FORMAL_PROPERTY' && error.property === populationProperties[2]);
});

test('the actual result reader rejects missing, duplicate and malformed observations', () => {
    const value = input(), cases = [concrete('complete', value)];
    assert.deepEqual(checkPopulationResults(record([observed('complete', value)]), cases), { cases: 1, complete: 1, incomplete: 0, rejected: 0 });
    assert.throws(() => checkPopulationResults(record([]), []), /No population cases/);
    for (const rows of [[], [observed('other', value)], [observed('complete', value), observed('complete', value)], [{ ...observed('complete', value), counts: null }], [{ ...observed('complete', value), mappingCalls: -1 }], [{ ...observed('complete', value), error: 'contradiction' }]]) assert.throws(() => checkPopulationResults(record(rows), cases));
});

test('decoded population counterexamples retain the input-conditioned property', () => {
    const value = input({ mappingCompletes: false });
    const decoded = { property: populationProperties[1], states: [{ action: '', vars: initial() }, { action: 'ChoosePopulation', vars: { ...initial(), populationPhase: 'candidate', populationInput: value } }, { action: 'ClassifyPopulation', vars: { populationPhase: 'checked', populationInput: value, outcome: 'complete', memberReads: 1, mappingCalls: 1, counts: { 1: 3, 2: 2, 3: 1, 4: 1 } } }] };
    const witness = populationCounterexampleWitness(decoded); assert.deepEqual(witness.expected.counts, []); assert.equal(witness.expected.outcome, 'incomplete');
    assert.throws(() => populationCounterexampleWitness({ ...decoded, property: populationProperties[2] }), /property mismatch/);
    assert.throws(() => populationCounterexampleWitness({ ...decoded, states: decoded.states.slice(1) }));
    const malformed = structuredClone(decoded); malformed.states[2].vars.counts = { 2: 3 }; assert.throws(() => populationCounterexampleWitness(malformed), /Malformed decoded count tuple/);
});

test('unmodeled observations block without claiming a named population violation', () => {
    for (const value of [input({ selectionComplete: false }), input({ memberReadCompletes: false }), input()]) {
        const row = { ...observed('binding', value), bindingCorrect: !expectedPopulation(value).bindingCorrect };
        assert.throws(() => checkPopulationResults(record([row]), [concrete('binding', value)]), error => error.code !== 'FORMAL_PROPERTY' && /Observation mismatch outside/.test(error.message));
    }
    for (const value of [input({ memberReadCompletes: false }), input({ mappingCompletes: false })]) {
        const row = { ...observed('partial', value), memberReads: 0 };
        assert.throws(() => checkPopulationResults(record([row]), [concrete('partial', value)]), error => error.code !== 'FORMAL_PROPERTY' && /Observation mismatch outside/.test(error.message));
        const { bindingCorrect, ...expected } = expectedPopulation(value);
        const decoded = { property: populationProperties[1], states: [{ action: '', vars: initial() }, { action: 'ChoosePopulation', vars: { ...initial(), populationPhase: 'candidate', populationInput: value } }, { action: 'ClassifyPopulation', vars: { populationPhase: 'checked', populationInput: value, ...expected, memberReads: 0 } }] };
        assert.throws(() => populationCounterexampleWitness(decoded), /does not violate its named property/);
    }
});
test('population result ordering cannot hide a genuine named violation behind an unrelated mismatch', () => {
    const value = input({ memberReadCompletes: false });
    const unrelated = { ...observed('earlier', value), memberReads: 0 }, broken = observed('later', input());
    assert.throws(() => checkPopulationResults(record([unrelated, broken]), [concrete('earlier', value), concrete('later', value)]), error => error.code === 'FORMAL_PROPERTY' && error.property === populationProperties[1] && error.witness.actual.outcome === 'complete');
    assert.throws(() => checkPopulationResults(record([unrelated]), [concrete('earlier', value)]), error => error.code === 'FORMAL_CONFORMANCE');
});
test('explicit empty or malformed population source overrides never fall back to pristine source', async () => {
    const value = input(), witness = { schemaVersion: 1, kind: 'population-mapping', model: 'PopulationMapping', property: populationProperties[2], input: value, alias: false, deadline: false, expected: expectedPopulation(value) };
    for (const productionSource of ['', ' \n ', null, 0, false]) await assert.rejects(() => replayPopulationWitness(witness, { productionSource }), /Production source override must be/);
});
