import assert from 'node:assert/strict';
import { test } from 'node:test';
import { sanitizeBody, sanitizePayload } from './playwright-browser.mjs';

test('large CDA report payloads remain complete while credential fields are redacted', () => {
  const rows = Array.from({ length: 1000 }, (_, index) => ({ id: `patient-${index}`, value: index % 2 ? null : 'repeat' }));
  const sanitized = sanitizePayload({ rows, authorization: 'Bearer secret', nested: { access_token: 'secret' } });
  assert.equal(sanitized.rows.length, 1000);
  assert.deepEqual(sanitized.rows[999], { id: 'patient-999', value: null });
  assert.equal(sanitized.authorization, '[REDACTED]');
  assert.equal(sanitized.nested.access_token, '[REDACTED]');
});

test('body sanitization redacts structured credentials, text secrets, and clips long payloads', () => {
  assert.equal(sanitizeBody('{"diagnostic":"safe","password":"private"}'), '{"diagnostic":"safe","password":"[REDACTED]"}');
  const sanitizedText = sanitizeBody('authorization: Bearer private-token');
  assert.equal(sanitizedText.includes('authorization'), false);
  assert.equal(sanitizedText.includes('private-token'), false);
  assert.equal(sanitizeBody('x'.repeat(12_100)).length, 12_000);
});

test('only exact public snapshot hashes and typed identity metadata survive sanitization', () => {
  const snapshotToken = `sha256:${'a'.repeat(64)}`;
  const sanitized = sanitizePayload({
    snapshotToken,
    malformedSnapshot: { snapshotToken: `sha256:${'A'.repeat(64)}` },
    otherToken: snapshotToken,
    snapshot_token: snapshotToken,
    privateValues: {
      authorization: snapshotToken,
      access_token: snapshotToken,
      cookie: snapshotToken,
      secret: snapshotToken,
      session: snapshotToken,
    },
    authorizationHeaderPresent: false,
    flags: { authorizationHeaderPresent: true, snapshotTokenMatched: true },
    snapshotTokenMatched: false,
    booleanStringFields: {
      authorizationHeaderPresent: 'Bearer private-secret-token',
      snapshotTokenMatched: 'true',
    },
  });

  assert.equal(sanitized.snapshotToken, snapshotToken);
  assert.equal(sanitized.malformedSnapshot.snapshotToken, '[REDACTED]');
  assert.equal(sanitized.otherToken, '[REDACTED]');
  assert.equal(sanitized.snapshot_token, '[REDACTED]');
  assert.deepEqual(sanitized.privateValues, {
    authorization: '[REDACTED]',
    access_token: '[REDACTED]',
    cookie: '[REDACTED]',
    secret: '[REDACTED]',
    session: '[REDACTED]',
  });
  assert.equal(sanitized.authorizationHeaderPresent, false);
  assert.equal(sanitized.flags.authorizationHeaderPresent, true);
  assert.equal(sanitized.snapshotTokenMatched, false);
  assert.equal(sanitized.booleanStringFields.authorizationHeaderPresent, '[REDACTED]');
  assert.equal(sanitized.booleanStringFields.snapshotTokenMatched, '[REDACTED]');
  assert.equal(sanitized.flags.snapshotTokenMatched, true);
  assert.deepEqual(JSON.parse(sanitizeBody(JSON.stringify({ snapshotToken, authorizationHeaderPresent: false }))), {
    snapshotToken,
    authorizationHeaderPresent: false,
  });
});
