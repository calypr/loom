import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root } from './formal-tools.mjs';
import { apiDoctor, apiSourceFingerprint, checkApiEvents } from './formal-api.mjs';
import { goTestTimeout, verificationTimeoutMs } from './verification-budget.mjs';

export const derivedPolicyTest = 'TestDerivedArithmeticPoliciesAgainstArango';
export const derivedPolicyFile = 'internal/server/derived_policy_rows_integration_test.go';
export const derivedPolicyFaults = [
    {
        "branch": "operation",
        "property": "Inv_DerivedArithmeticUsesAuthoredOperation",
        "case": "present_2_literal_2_reverse_false_ADD_ERROR_",
        "file": "internal/dataframe/compiler/lower/recipe_derived_columns.go",
        "before": "recipe.DerivedAdd:      \"add\",",
        "after": "recipe.DerivedAdd:      \"subtract\","
    },
    {
        "branch": "missing-null",
        "property": "Inv_DerivedMissingPropagatesNull",
        "case": "missing_0_literal_2_reverse_false_ADD_PROPAGATE_NULL_",
        "file": "internal/dataframe/compiler/lower/recipe_derived_columns.go",
        "before": "derivedPhysicalCall(\"or\", derivedPhysicalCall(\"eq\", left, zero), derivedPhysicalCall(\"eq\", right, zero)),\n\t\t\tzero,",
        "after": "derivedPhysicalCall(\"or\", derivedPhysicalCall(\"eq\", left, zero), derivedPhysicalCall(\"eq\", right, zero)),\n\t\t\tderivedLiteralInt64Expression(bindVars, 9),"
    },
    {
        "branch": "missing-error",
        "property": "Inv_DerivedMissingInputRaisesNamedError",
        "case": "missing_0_literal_2_reverse_false_ADD_ERROR_",
        "file": "internal/dataframe/compiler/lower/recipe_derived_columns.go",
        "before": "derivedRawLiteralExpression(bindVars, \"derived_false\", false), missingError)",
        "after": "derivedRawLiteralExpression(bindVars, \"derived_false\", true), missingError)"
    },
    {
        "branch": "zero-null",
        "property": "Inv_DerivedZeroDenominatorReturnsNull",
        "case": "present_2_literal_0_reverse_false_DIVIDE_ERROR_NULL",
        "file": "internal/dataframe/compiler/lower/recipe_derived_columns.go",
        "before": "arguments = append(arguments, condition, zero)",
        "after": "arguments = append(arguments, condition, derivedLiteralInt64Expression(bindVars, 9))"
    },
    {
        "branch": "zero-error",
        "property": "Inv_DerivedZeroDenominatorRaisesNamedError",
        "case": "present_2_literal_0_reverse_false_DIVIDE_ERROR_ERROR",
        "file": "internal/dataframe/compiler/lower/recipe_derived_columns.go",
        "before": "derivedRawLiteralExpression(bindVars, \"derived_false\", false), derivedRawLiteralExpression(bindVars, \"derived_error\", \"DERIVED_DIVISION_BY_ZERO\")",
        "after": "derivedRawLiteralExpression(bindVars, \"derived_false\", true), derivedRawLiteralExpression(bindVars, \"derived_error\", \"DERIVED_DIVISION_BY_ZERO\")"
    }
];
export const derivedPolicySummary = { caseCount: 400, values: 212, nulls: 94, missingErrors: 80, zeroErrors: 14, guardOrderConformance: 8 };
const packageName = 'github.com/calypr/loom/internal/server';
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(name => [name, canonical(value[name])])) : value;
const equal = (left, right, message) => assert.deepEqual(canonical(left), canonical(right), message);
const sha = path => createHash('sha256').update(readFileSync(path)).digest('hex');

export function derivedPolicyCases() {
    const result = [];
    for (const sourceOnRight of [false, true]) for (const [sourceState, sourceValue] of [['missing', 0], ['null', 0], ['present', -2], ['present', 0], ['present', 2]])
        for (const literal of [-2, 0, 2, 4]) for (const operation of ['ADD', 'SUBTRACT', 'MULTIPLY', 'DIVIDE']) for (const missingPolicy of ['PROPAGATE_NULL', 'ERROR'])
            for (const zeroPolicy of operation === 'DIVIDE' ? ['NULL', 'ERROR'] : ['']) {
                result.push({ id: `${sourceState}_${sourceValue}_literal_${literal}_reverse_${sourceOnRight}_${operation}_${missingPolicy}_${zeroPolicy}`, operation, missingPolicy, zeroPolicy, sourceState, sourceValue, literal, sourceOnRight,
                    guardOrderConformance: sourceState !== 'present' && !sourceOnRight && literal === 0 && operation === 'DIVIDE' });
            }
    return result;
}
const casesByID = new Map(derivedPolicyCases().map(value => [value.id, value]));
export function derivedPolicyExpected(input) {
    equal(input, casesByID.get(input?.id), 'Unknown or changed bounded arithmetic input');
    if (input.sourceState !== 'present') return input.missingPolicy === 'ERROR' ? { kind: 'error', value: null, errorMarker: 'DERIVED_MISSING_INPUT' } : { kind: 'null', value: null, errorMarker: '' };
    const [left, right] = input.sourceOnRight ? [input.literal, input.sourceValue] : [input.sourceValue, input.literal];
    if (input.operation === 'DIVIDE' && right === 0) return input.zeroPolicy === 'ERROR' ? { kind: 'error', value: null, errorMarker: 'DERIVED_DIVISION_BY_ZERO' } : { kind: 'null', value: null, errorMarker: '' };
    const value = { ADD: () => left + right, SUBTRACT: () => left - right, MULTIPLY: () => left * right, DIVIDE: () => left / right }[input.operation]();
    assert.ok(Number.isFinite(value)); return { kind: 'value', value, errorMarker: '' };
}
function propertyFor(input, expected) {
    assert.equal(input.guardOrderConformance, false, 'Guard-order conformance is not a public policy counterexample');
    if (input.sourceState !== 'present') return derivedPolicyFaults.find(proof => proof.branch === (input.missingPolicy === 'ERROR' ? 'missing-error' : 'missing-null')).property;
    if (expected.kind === 'value') return derivedPolicyFaults[0].property;
    return derivedPolicyFaults.find(proof => proof.branch === (expected.kind === 'error' ? 'zero-error' : 'zero-null')).property;
}
function validateObservation(actual) {
    equal(Object.keys(actual).sort(), ['errorMessage', 'errorNumber', 'rows', 'value']);
    assert.ok(Number.isSafeInteger(actual.rows) && actual.rows >= 0);
    assert.ok(actual.value === null || typeof actual.value === 'boolean' || typeof actual.value === 'string' || typeof actual.value === 'number' && Number.isFinite(actual.value));
    assert.ok([0, 1593].includes(actual.errorNumber), 'Transport or non-policy query error is not policy evidence');
    assert.equal(typeof actual.errorMessage, 'string');
    if (actual.errorNumber === 0) assert.equal(actual.errorMessage, '');
    else { assert.equal(actual.rows, 0); assert.equal(actual.value, null); assert.match(actual.errorMessage, /DERIVED_(?:MISSING_INPUT|DIVISION_BY_ZERO)/); }
}
function holds(expected, actual) {
    if (expected.kind === 'error') return actual.errorNumber === 1593 && actual.errorMessage.includes(expected.errorMarker);
    const value = typeof actual.value === 'string' && actual.value !== '' && actual.value.trim() === actual.value ? Number(actual.value) : actual.value;
    return actual.errorNumber === 0 && actual.rows === 1 && (expected.kind === 'null' ? actual.value === null : typeof value === 'number' && Number.isFinite(value) && value === expected.value);
}
export function validateDerivedPolicyCounterexample(report, property) {
    equal(Object.keys(report).sort(), ['actual', 'case', 'expected']);
    const expected = derivedPolicyExpected(report.case); equal(report.expected, expected, 'Changed independent arithmetic expectation'); validateObservation(report.actual);
    const named = propertyFor(report.case, expected); if (property !== undefined) assert.equal(named, property, 'Counterexample violates a different arithmetic policy');
    assert.equal(holds(expected, report.actual), false, 'Counterexample contains no actual policy violation');
    return { schemaVersion: 1, kind: 'derived-policy', property: named, ...report };
}
function readEvents(stdout) {
    const events = stdout.trim().split('\n').filter(Boolean).map(JSON.parse);
    assert.ok(events.length); assert.ok(events.every(event => event.Package === packageName), 'Foreign Go package');
    assert.ok(!events.some(event => event.Action === 'skip'), 'Skipped arithmetic case');
    for (const event of events) if (event.Test) {
        assert.ok(event.Test === derivedPolicyTest || event.Test.startsWith(derivedPolicyTest + '/') && casesByID.has(event.Test.slice(derivedPolicyTest.length + 1)), 'Foreign arithmetic test');
    }
    const output = events.filter(event => event.Action === 'output').map(event => event.Output || '').join('');
    assert.doesNotMatch(output, /panic:|fatal error:|test timed out|\[build failed\]|undefined:|syntax error:|no required module|DERIVED_ARITHMETIC_(?:QUERY|COMPILE|SCHEMA|SERIALIZATION)|owned derived database cleanup/i);
    return events;
}
function reports(events, marker) {
    const found = [];
    for (const event of events) if (event.Action === 'output') for (const match of (event.Output || '').matchAll(new RegExp(`${marker}\\s+([^\\n]+)`, 'g'))) {
        assert.ok(event.Test, 'Foreign arithmetic report'); found.push({ test: event.Test, report: JSON.parse(match[1]) });
    }
    return found;
}
function selectedEvents(events, ids, outcome) {
    const expected = new Set([derivedPolicyTest, ...ids.map(id => `${derivedPolicyTest}/${id}`)]);
    for (const name of expected) {
        assert.equal(events.filter(event => event.Test === name && event.Action === 'run').length, 1, `Missing or duplicate Go run: ${name}`);
        assert.equal(events.filter(event => event.Test === name && event.Action === outcome).length, 1, `Missing or duplicate Go ${outcome}: ${name}`);
    }
    assert.ok(events.every(event => !event.Test || expected.has(event.Test)), 'Extra arithmetic cases');
    assert.ok(events.some(event => !event.Test && event.Action === outcome));
}
export function validateDerivedPolicyPositive(stdout, exit = 0, caseID) {
    assert.equal(exit, 0); checkApiEvents(stdout, { tests: [derivedPolicyTest] }); const events = readEvents(stdout);
    const selected = caseID === undefined ? [...casesByID.values()] : [casesByID.get(caseID)]; assert.ok(selected.every(Boolean));
    selectedEvents(events, selected.map(input => input.id), 'pass');
    const found = reports(events, 'DERIVED_ARITHMETIC_SUMMARY_JSON'); assert.equal(found.length, 1); assert.equal(found[0].test, derivedPolicyTest);
    assert.equal(reports(events, 'DERIVED_ARITHMETIC_FAILURE_JSON').length, 0); assert.equal(reports(events, 'DERIVED_ARITHMETIC_CONFORMANCE_JSON').length, 0);
    const expected = caseID === undefined ? derivedPolicySummary : summaryFor(selected); equal(found[0].report, expected, 'Incomplete actual arithmetic frontier'); return expected;
}
function summaryFor(inputs) {
    const summary = { caseCount: inputs.length, values: 0, nulls: 0, missingErrors: 0, zeroErrors: 0, guardOrderConformance: 0 };
    for (const input of inputs) { const expected = derivedPolicyExpected(input); summary[expected.kind === 'value' ? 'values' : expected.kind === 'null' ? 'nulls' : expected.errorMarker === 'DERIVED_MISSING_INPUT' ? 'missingErrors' : 'zeroErrors']++; if (input.guardOrderConformance) summary.guardOrderConformance++; }
    return summary;
}
export function validateDerivedPolicyFault(stdout, exit, proof) {
    equal(proof, derivedPolicyFaults.find(value => value.branch === proof?.branch), 'Unknown source fault'); assert.equal(exit, 1);
    const events = readEvents(stdout); selectedEvents(events, [proof.case], 'fail'); assert.ok(!events.some(event => event.Action === 'pass'), 'Mixed pass/fail arithmetic evidence');
    assert.equal(reports(events, 'DERIVED_ARITHMETIC_SUMMARY_JSON').length, 0); assert.equal(reports(events, 'DERIVED_ARITHMETIC_CONFORMANCE_JSON').length, 0);
    const found = reports(events, 'DERIVED_ARITHMETIC_FAILURE_JSON'); assert.equal(found.length, 1); assert.equal(found[0].test, `${derivedPolicyTest}/${proof.case}`);
    equal(found[0].report.case, casesByID.get(proof.case), 'Unknown or changed source-fault input');
    const lines = events.filter(event => event.Test && event.Action === 'output').flatMap(event => (event.Output || '').split('\n')).map(line => line.trim()).filter(Boolean);
    assert.ok(lines.every(line => line.startsWith('=== RUN   ' + derivedPolicyTest) || line.startsWith('--- FAIL: ' + derivedPolicyTest) || /^(?:[^\s]+\.go:\d+: )?DERIVED_ARITHMETIC_FAILURE_JSON /.test(line)), 'Unexpected failure alongside arithmetic counterexample');
    return validateDerivedPolicyCounterexample(found[0].report, proof.property);
}
function runGo(output, { proof, caseID, productionSource, setup = false } = {}) {
    const scratch = mkdtempSync(join(tmpdir(), 'loom-derived-policy-')); mkdirSync(output, { recursive: true });
    try {
        const tool = apiDoctor(), args = ['test', '-json', '-count=1', `-timeout=${goTestTimeout}`, '-run', setup ? '^$' : `^${derivedPolicyTest}$${caseID ? '/^' + caseID + '$' : ''}`];
        if (productionSource !== undefined) {
            assert.ok(typeof productionSource === 'string' && productionSource.trim(), 'Empty production source override');
            const replacement = join(scratch, 'mutant.go'), overlay = join(scratch, 'overlay.json'); writeFileSync(replacement, productionSource); writeFileSync(overlay, JSON.stringify({ Replace: { [join(root, derivedPolicyFaults[0].file)]: replacement } })); args.push('-overlay', overlay);
            writeFileSync(join(output, 'source.json'), JSON.stringify({ file: derivedPolicyFaults[0].file, before: sha(join(root, derivedPolicyFaults[0].file)), after: sha(replacement), property: proof?.property }, null, 2));
        }
        args.push('./internal/server'); const env = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', GOMAXPROCS: '2' };
        if (tool.executable === '/workspace/.loom-setup/go/bin/go') { if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod'; if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache'; }
        const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: setup ? Math.max(60_000, verificationTimeoutMs) : verificationTimeoutMs, maxBuffer: 32 * 1024 * 1024, detached: true });
        if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        writeFileSync(join(output, 'go.jsonl'), result.stdout || ''); writeFileSync(join(output, 'process.json'), JSON.stringify({ command: [tool.executable, ...args], exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2)); assert.ifError(result.error); assert.equal(result.stderr || '', '');
        if (setup) { assert.equal(result.status, 0); const events = readEvents(result.stdout); assert.ok(events.some(event => !event.Test && event.Action === 'pass')); assert.ok(events.every(event => !event.Test && event.Action !== 'fail')); return { status: 'compiled-no-proof', namedCasesExecuted: 0 }; }
        return proof ? validateDerivedPolicyFault(result.stdout, result.status, proof) : validateDerivedPolicyPositive(result.stdout, result.status, caseID);
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}
export function runDerivedPolicy(output) {
    assert.ok(process.env.LOOM_TEST_ARANGO_URL, 'Set LOOM_TEST_ARANGO_URL to an owned integration instance');
    const directory = resolve(output); assert.ok(!existsSync(directory) || readdirSync(directory).length === 0, 'Output must be empty'); mkdirSync(directory, { recursive: true });
    const before = apiSourceFingerprint(), adapterHash = sha(join(root, derivedPolicyFile)), wrapperHash = sha(fileURLToPath(import.meta.url));
    const evidence = { status: 'running', boundary: '400 finite actual compiler/Arango executions over one optional numeric source and typed literal in both orientations; 392 contract inputs and 8 guard-order conformance inputs; no TLA, FHIR validation, public save, browser, overflow or arbitrary-input claim', sourceBefore: before, adapterHash, wrapperHash, sourceControls: [] };
    try {
        evidence.setup = runGo(join(directory, 'setup'), { setup: true }); evidence.positive = runGo(join(directory, 'positive'));
        for (const proof of derivedPolicyFaults) { const original = readFileSync(join(root, proof.file), 'utf8'); assert.equal(original.split(proof.before).length, 2, 'Nonunique source fault anchor'); const witness = runGo(join(directory, proof.branch), { proof, caseID: proof.case, productionSource: original.replace(proof.before, proof.after) }); writeFileSync(join(directory, proof.branch, 'counterexample.json'), JSON.stringify(witness, null, 2)); evidence.sourceControls.push({ branch: proof.branch, ...witness }); }
        evidence.restored = runGo(join(directory, 'restored')); evidence.sourceVariantCount = evidence.sourceControls.length; evidence.properties = evidence.sourceControls.map(value => value.property);
        assert.equal(apiSourceFingerprint(), before, 'Sources changed during policy replay'); assert.equal(sha(join(root, derivedPolicyFile)), adapterHash); assert.equal(sha(fileURLToPath(import.meta.url)), wrapperHash); evidence.status = 'passed'; return evidence;
    } catch (error) { evidence.status = 'failed'; evidence.error = error.message; throw error; }
    finally { evidence.sourceAfter = apiSourceFingerprint(); writeFileSync(join(directory, 'derived-policy.json'), JSON.stringify(evidence, null, 2)); }
}
export function replayDerivedPolicyWitness(witness, { output, productionSource } = {}) {
    equal(Object.keys(witness).sort(), ['actual', 'case', 'expected', 'kind', 'property', 'schemaVersion']);
    assert.equal(witness.schemaVersion, 1); assert.equal(witness.kind, 'derived-policy');
    if (productionSource !== undefined) assert.ok(typeof productionSource === 'string' && productionSource.trim(), 'Empty production source override');
    const report = { case: witness.case, expected: witness.expected, actual: witness.actual }; validateDerivedPolicyCounterexample(report, witness.property);
    assert.ok(process.env.LOOM_TEST_ARANGO_URL, 'Set LOOM_TEST_ARANGO_URL to an owned integration instance');
    const directory = resolve(output); assert.ok(!existsSync(directory) || readdirSync(directory).length === 0); mkdirSync(directory, { recursive: true });
    const proof = { ...derivedPolicyFaults.find(value => value.property === witness.property), case: witness.case.id };
    try { runGo(join(directory, 'current'), { caseID: witness.case.id, productionSource }); return { status: 'property-held', property: witness.property }; }
    catch (error) { if (!existsSync(join(directory, 'current', 'process.json'))) throw error; const process = JSON.parse(readFileSync(join(directory, 'current', 'process.json'), 'utf8')); const stdout = readFileSync(join(directory, 'current', 'go.jsonl'), 'utf8');
        const events = readEvents(stdout); selectedEvents(events, [witness.case.id], 'fail'); assert.ok(!events.some(event => event.Action === 'pass')); assert.equal(reports(events, 'DERIVED_ARITHMETIC_SUMMARY_JSON').length, 0); assert.equal(reports(events, 'DERIVED_ARITHMETIC_CONFORMANCE_JSON').length, 0); const lines = events.filter(event => event.Test && event.Action === 'output').flatMap(event => (event.Output || '').split('\n')).map(line => line.trim()).filter(Boolean); assert.ok(lines.every(line => line.startsWith('=== RUN   ' + derivedPolicyTest) || line.startsWith('--- FAIL: ' + derivedPolicyTest) || /^(?:[^\s]+\.go:\d+: )?DERIVED_ARITHMETIC_FAILURE_JSON /.test(line)), 'Unexpected failure alongside witness'); const found = reports(events, 'DERIVED_ARITHMETIC_FAILURE_JSON'); assert.equal(found.length, 1); assert.equal(found[0].test, `${derivedPolicyTest}/${witness.case.id}`); assert.equal(process.exit, 1); const actual = validateDerivedPolicyCounterexample(found[0].report, proof.property); return { status: 'reproduced', ...actual }; }
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, output, witnessFile, sourceFile] = process.argv.slice(2);
    if (command === '--out') { assert.equal(process.argv.length, 4); console.log(JSON.stringify(runDerivedPolicy(output))); }
    else if (command === '--witness') { assert.ok(process.argv.length === 5 || process.argv.length === 6); assert.ok(output && witnessFile); const result = replayDerivedPolicyWitness(JSON.parse(readFileSync(witnessFile, 'utf8')), { output, ...(sourceFile !== undefined ? { productionSource: readFileSync(sourceFile, 'utf8') } : {}) }); console.log(JSON.stringify(result)); process.exitCode = result.status === 'reproduced' ? 1 : 0; }
    else throw new Error('Use --out FRESH_DIRECTORY or --witness FRESH_DIRECTORY WITNESS_JSON [SCRATCH_GO_SOURCE]');
}
