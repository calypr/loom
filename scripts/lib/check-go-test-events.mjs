#!/usr/bin/env node
// Named Go test-event gate: reads `go test -json` output on stdin and fails
// unless every required test (and every required subtest) emits run -> pass
// with zero skips anywhere in the stream. A package exit code is never
// acceptance: a filtered run that selects zero tests, a skip, or a silent
// rename of a required test all fail this gate.
//
// Reuses the authoritative checkGoEvents from run-postgres-integration.mjs so
// CI named gates and live integration runners share one acceptance semantics.
//
// Usage: go test -json -count=1 <pkg> -run '^TestX$' \
//   | node scripts/lib/check-go-test-events.mjs TestX TestX/sub_a ...
import { readFileSync } from 'node:fs';
import { checkGoEvents } from '../run-postgres-integration.mjs';

const required = process.argv.slice(2);
if (required.length === 0) {
  console.error('usage: check-go-test-events.mjs <requiredTest>... < go-test.json');
  process.exit(2);
}
for (const name of required) {
  if (!/^Test\w+(\/\S+)?$/.test(name)) {
    console.error(`invalid required test name: ${name}`);
    process.exit(2);
  }
}

const stdin = readFileSync(0, 'utf8');
try {
  const { passes } = checkGoEvents(stdin, required);
  console.log(
    `named event gate OK: ${required.length} required events run+passed, zero skips, ` +
      `${passes.length} total passing events`,
  );
} catch (error) {
  console.error(`named event gate FAILED: ${error.message}`);
  process.exit(1);
}
