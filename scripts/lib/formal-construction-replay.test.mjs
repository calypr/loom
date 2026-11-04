import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { root } from './formal-tools.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { expectedChainAcceptance, validateChainInput, checkConstructionResults, constructionCounterexampleWitness, constructionProperties, replayConstructionWitness } from './formal-construction-replay.mjs';

const input = (firstKind = 'FILTER', secondInput = 'PREVIOUS', secondColumn = 'x') => ({ firstKind, secondInput, secondColumn });
const concrete = (id, value) => ({ id, ...value, rename: false, reorder: false });
const result = (id, accepted) => ({ id, accepted, error: accepted ? '' : 'controlled rejection' });
const record = results => ({ schemaVersion: 1, results });
const initial = () => ({ chainPhase: 'choose', chainInput: input(), accepted: false });

test('bounded chain contract includes valid derived columns and rejects nonpredecessors', () => {
    const accepted = [];
    for (const first of ['FILTER', 'DERIVE']) for (const reference of ['PREVIOUS', 'SOURCE', 'SELF', 'FOREIGN', 'REVISION']) for (const column of ['x', 'd', 'foreign']) {
        const value = input(first, reference, column);
        if (expectedChainAcceptance(value)) accepted.push(value);
    }
    assert.deepEqual(accepted, [input(), input('DERIVE'), input('DERIVE', 'PREVIOUS', 'd')]);
    for (const bad of [null, {}, { ...input(), extra: true }, input('UNKNOWN'), input('FILTER', 'UNKNOWN'), input('FILTER', 'PREVIOUS', 'UNKNOWN')]) assert.throws(() => validateChainInput(bad));
});

test('actual-result reader rejects missing, duplicate, malformed and unexecuted observations', () => {
    const cases = [concrete('valid', input()), concrete('invalid', input('FILTER', 'SELF'))];
    assert.deepEqual(checkConstructionResults(record([result('valid', true), result('invalid', false)]), cases), { cases: 2, accepted: 1, rejected: 1 });
    for (const rows of [[], [result('valid', true)], [result('valid', true), result('valid', true)], [result('missing', true), result('invalid', false)], [{ id: 'valid', accepted: 'true', error: '' }, result('invalid', false)], [{ id: 'valid', accepted: true, error: 'contradiction' }, result('invalid', false)]]) assert.throws(() => checkConstructionResults(record(rows), cases));
    assert.throws(() => checkConstructionResults({ ...record([]), extra: true }, []));
});

test('wrong acceptance retains the lineage property and concrete witness', () => {
    const value = concrete('foreign', input('FILTER', 'FOREIGN'));
    assert.throws(() => checkConstructionResults(record([result(value.id, true)]), [value]), error => error.code === 'FORMAL_PROPERTY' && error.property === constructionProperties[0] && error.witness.expectedAccepted === false && error.witness.actualAccepted === true);
});

test('rejecting every response fails the valid derived-column contract', () => {
    const value = concrete('derived', input('DERIVE', 'PREVIOUS', 'd'));
    assert.throws(() => checkConstructionResults(record([result(value.id, false)]), [value]), error => error.code === 'FORMAL_PROPERTY' && error.property === constructionProperties[1] && error.witness.expectedAccepted === true);
});

test('decoded counterexamples must cross both real model actions and their named reader rule', () => {
    const value = input('DERIVE', 'PREVIOUS', 'd');
    const decoded = { property: constructionProperties[1], states: [{ action: '', vars: initial() }, { action: 'ChooseChain', vars: { chainPhase: 'candidate', chainInput: value, accepted: false } }, { action: 'ClassifyChain', vars: { chainPhase: 'checked', chainInput: value, accepted: false } }] };
    const witness = constructionCounterexampleWitness(decoded);
    assert.deepEqual(witness.input, value); assert.equal(witness.expectedAccepted, true);
    assert.throws(() => constructionCounterexampleWitness({ ...decoded, property: constructionProperties[0] }), /property mismatch/);
    assert.throws(() => constructionCounterexampleWitness({ ...decoded, states: decoded.states.slice(1) }));
    const wrong = structuredClone(decoded); wrong.states[2].action = 'Unknown'; assert.throws(() => constructionCounterexampleWitness(wrong));
    const holding = structuredClone(decoded); holding.states[2].vars.accepted = true; assert.throws(() => constructionCounterexampleWitness(holding), /does not violate/);
});


test('an explicit empty or malformed source override never falls back to pristine code', async () => {
    const witness = { schemaVersion: 1, kind: 'construction-chain', model: 'ConstructionChain', property: constructionProperties[0], input: input('FILTER', 'SELF'), rename: false, reorder: false, expectedAccepted: false };
    for (const productionSource of ['', '  \n ', null, false, 0])
        await assert.rejects(() => replayConstructionWitness(witness, { productionSource }), /Production source override must be/);
});


test('a nonlocal Go tool keeps its own cache defaults', () => {
    const directory = mkdtempSync(join(tmpdir(), 'loom-chain-tool-env-'));
    try {
        const executable = join(directory, 'go-probe.mjs'), observation = join(directory, 'environment.json'), witness = join(directory, 'witness.json');
        writeFileSync(executable, `#!${process.execPath}\nimport {writeFileSync} from 'node:fs';\nif (process.argv[2] === 'version') { console.log('go version go1.26.5 linux/amd64'); process.exit(0); }\nwriteFileSync(${JSON.stringify(observation)}, JSON.stringify({GOMODCACHE:process.env.GOMODCACHE??null,GOCACHE:process.env.GOCACHE??null}));\nprocess.exit(42);\n`, { mode: 0o700 });
        writeFileSync(witness, JSON.stringify({ schemaVersion: 1, kind: 'construction-chain', model: 'ConstructionChain', property: constructionProperties[0], input: input('FILTER', 'SELF'), rename: false, reorder: false, expectedAccepted: false }));
        const env = { ...process.env, GO: executable };
        delete env.GOMODCACHE; delete env.GOCACHE; delete env.NODE_TEST_CONTEXT;
        const child = spawnSync(process.execPath, ['scripts/lib/formal-construction-replay.mjs', '--witness', witness], { cwd: root, env, encoding: 'utf8', timeout: 10000 });
        assert.ifError(child.error);
        assert.equal(child.status, 1);
        assert.deepEqual(JSON.parse(readFileSync(observation, 'utf8')), { GOMODCACHE: null, GOCACHE: null });
        assert.ok(!child.stdout.includes('property-held') && !child.stdout.includes('reproduced'));
    } finally { rmSync(directory, { recursive: true, force: true }); }
});
