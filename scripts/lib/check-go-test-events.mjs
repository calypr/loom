#!/usr/bin/env node
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
