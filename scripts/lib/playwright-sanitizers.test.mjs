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
