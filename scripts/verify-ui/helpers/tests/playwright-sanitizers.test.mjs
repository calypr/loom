import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fingerprintAuthorizationScopeDigest, sanitizeBody, sanitizePayload, sanitizeText } from '../playwright-browser.mjs';

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
    catalogSnapshotToken: snapshotToken,
    malformedSnapshot: { snapshotToken: `sha256:${'A'.repeat(64)}` },
    malformedCatalogSnapshot: { catalogSnapshotToken: `sha256:${'A'.repeat(64)}` },
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
  assert.equal(sanitized.catalogSnapshotToken, snapshotToken,
    'A correctly shaped catalog snapshot identity remains comparable in captured assessment proposals.');
  assert.equal(sanitized.malformedSnapshot.snapshotToken, '[REDACTED]');
  assert.equal(sanitized.malformedCatalogSnapshot.catalogSnapshotToken, '[REDACTED]',
    'Malformed snapshot-like values remain redacted.');
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

test('authorization scope digests stay comparable as one-way fingerprints while credentials remain redacted', () => {
  const scopeDigest = 'a'.repeat(64);
  const otherScopeDigest = 'b'.repeat(64);
  const fingerprint = fingerprintAuthorizationScopeDigest(scopeDigest);
  const captured = sanitizePayload({ authorizationScopeDigest: scopeDigest });
  const otherCaptured = sanitizePayload({ authorizationScopeDigest: otherScopeDigest });

  assert.match(fingerprint, /^scope-fingerprint:sha256:[a-f0-9]{64}$/);
  assert.equal(captured.authorizationScopeDigest, fingerprint);
  assert.equal(sanitizePayload(captured).authorizationScopeDigest, fingerprint,
    'Sanitizing captured diagnostics again must not alter the scope fingerprint');
  assert.notEqual(otherCaptured.authorizationScopeDigest, fingerprint,
    'Different scope digests must remain distinguishable to the native equality assertion');
  assert.equal(fingerprintAuthorizationScopeDigest('not-a-sha256-digest'), undefined);
  assert.deepEqual(sanitizePayload({ authorization: scopeDigest, access_token: scopeDigest }), {
    authorization: '[REDACTED]', access_token: '[REDACTED]',
  });
  assert.equal(sanitizePayload({ authorizationScopeDigest: 'not-a-sha256-digest' }).authorizationScopeDigest, '[REDACTED]');
});

test('text redaction preserves benign assignments and signed choice IDs while removing credential variants', () => {
  const signedChoiceId = 'choice.v3.payload-7f9a.signature-a81c';
  const text = [
    'displayName=Specimen',
    `signedChoiceId=${signedChoiceId}`,
    '"refresh-token"="quoted-private-token"',
    'x-upstream-api-key=prefixed-private-key',
    'authorization=Bearer bearer-private-token',
  ].join(' ');

  const sanitized = sanitizeText(text);
  assert.match(sanitized, /displayName=Specimen/);
  assert.match(sanitized, new RegExp(`signedChoiceId=${signedChoiceId}`));
  assert.doesNotMatch(sanitized, /quoted-private-token|prefixed-private-key|bearer-private-token/);
  assert.match(sanitized, /\[REDACTED\]/);
});

test('plain 60,000-character words sanitize within the bounded scan budget', () => {
  const input = 'x'.repeat(60_000);
  const startedAt = performance.now();
  const sanitized = sanitizeText(input);
  const elapsedMs = performance.now() - startedAt;

  assert.equal(sanitized, input);
  assert.ok(elapsedMs < 500, `60,000-character sanitization took ${elapsedMs.toFixed(1)}ms`);
});

test('text redaction still finds credentials embedded inside a benign message assignment', () => {
  for (const text of [
    'message:cookie=private',
    'message:"cookie=private"',
    'message:{"access_token":"private"}',
  ]) {
    assert.doesNotMatch(sanitizeText(text), /private/);
  }
});
