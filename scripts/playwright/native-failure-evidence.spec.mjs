import { expect, test } from '@playwright/test';
import { captureNativeFailureEvidence } from './native-failure-evidence.mjs';

const ownedOrigins = new Set(['http://loom.test']);

test('closed pages return a failure summary without querying page or locator', async () => {
  const evidence = await captureNativeFailureEvidence({
    page: { isClosed: () => true },
    ownedOrigins,
    reason: 'page closed during a failed action',
    locator: { toString: () => 'getByRole(button)' },
  });

  expect(evidence.page).toEqual({ state: 'closed' });
  expect(evidence.reason).toBe('page closed during a failed action');
});

test('unowned page origins do not expose URL, DOM text, or locator state', async () => {
  let pageQueried = false;
  const page = {
    isClosed: () => false,
    url: () => 'https://outside.example/private?token=credential',
    locator: () => {
      pageQueried = true;
      throw new Error('unowned page must not be queried');
    },
  };

  const evidence = await captureNativeFailureEvidence({ page, ownedOrigins, reason: 'workflow failed' });

  expect(pageQueried).toBe(false);
  expect(evidence.page).toEqual({ state: 'skipped', reason: 'page origin is outside the owned fixture' });
  expect(JSON.stringify(evidence)).not.toContain('outside.example');
  expect(JSON.stringify(evidence)).not.toContain('credential');
});

test('failure text drops unrelated URLs and credentials from URL queries', async () => {
  const evidence = await captureNativeFailureEvidence({
    page: { isClosed: () => true },
    ownedOrigins,
    reason: 'request failed at https://outside.example/private?token=credential',
  });

  expect(evidence.reason).toBe('request failed at [external URL]');
  expect(JSON.stringify(evidence)).not.toContain('outside.example');
  expect(JSON.stringify(evidence)).not.toContain('credential');
});

test('owned page snapshots preserve control state and redact credential values', async () => {
  const attributes = {
    type: 'password',
    'aria-label': 'API token',
    name: 'apiToken',
    placeholder: 'token',
  };
  const element = {
    tagName: 'INPUT',
    id: 'api-token',
    value: 'secret-value',
    checked: false,
    labels: [],
    getAttribute: name => attributes[name] ?? null,
  };
  const locator = {
    toString: () => 'getByLabel("API token")',
    count: async () => 1,
    isVisible: async () => true,
    isEnabled: async () => false,
    isEditable: async () => true,
    evaluate: async callback => callback(element),
  };
  const page = {
    isClosed: () => false,
    url: () => 'http://loom.test/builder?session=ignored',
    locator: selector => ({
      evaluate: async callback => callback({ innerText: selector === 'body' ? 'Visible page text' : '' }),
    }),
  };

  const evidence = await captureNativeFailureEvidence({
    page,
    ownedOrigins,
    reason: 'control action failed',
    label: 'Save draft',
    locator,
    elapsedMs: 4_812,
  });

  expect(evidence.action).toMatchObject({ label: 'Save draft', locator: 'getByLabel("API token")', elapsedMs: 4_812 });
  expect(evidence.page).toMatchObject({ url: 'http://loom.test/builder', bodyText: 'Visible page text' });
  expect(evidence.locator).toMatchObject({
    state: 'captured',
    count: 1,
    visible: { state: 'captured', value: true },
    enabled: { state: 'captured', value: false },
    editable: { state: 'captured', value: true },
    control: { state: 'captured', value: { value: '[REDACTED]' } },
  });
  expect(JSON.stringify(evidence)).not.toContain('secret-value');
  expect(JSON.stringify(evidence)).not.toContain('session=ignored');
});

test('hung native reads are bounded and reported as timed out', async () => {
  const never = () => new Promise(() => {});
  const page = {
    isClosed: () => false,
    url: () => 'http://loom.test/builder',
    locator: () => ({ evaluate: never }),
  };
  const locator = {
    count: never,
    toString: () => 'getByRole(button)',
  };

  const evidence = await captureNativeFailureEvidence({ page, ownedOrigins, locator, reason: 'capture timeout case' });

  expect(evidence.page.bodyTextState).toBe('timed-out');
  expect(evidence.locator.state).toBe('timed-out');
  expect(evidence.captureLimitMs).toBe(1_000);
  expect(evidence.captureDurationMs).toBeLessThanOrEqual(1_000);
});
