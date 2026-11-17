#!/usr/bin/env node
// Reject successful packages that did not execute the explicitly selected tests.
import assert from 'node:assert/strict';

const expected = process.argv.slice(2);
assert.ok(expected.length, 'Pass the expected Go test names');
let output = '';
for await (const chunk of process.stdin) {
    process.stdout.write(chunk);
    output += chunk;
}
const events = output.split('\n').filter(Boolean).map(line => JSON.parse(line));
assert.ok(events.some(event => !event.Test && event.Action === 'pass'), 'Go package did not pass');
assert.ok(!events.some(event => event.Action === 'fail'), 'Go reported a failure');
assert.ok(!events.some(event => event.Action === 'skip'), 'Go test or subtest skipped');
for (const name of expected) {
    assert.ok(events.some(event => event.Test === name && event.Action === 'run'), `Go test did not run: ${name}`);
    assert.ok(events.some(event => event.Test === name && event.Action === 'pass'), `Go test did not pass: ${name}`);
    assert.ok(!events.some(event => event.Test === name && event.Action === 'skip'), `Go test skipped: ${name}`);
}
