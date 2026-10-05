import { expect } from '@playwright/test';

const defaultTimeout = 5_000;

export async function requireUnique(locator, label, { timeout = defaultTimeout } = {}) {
  await expect(locator, `${label}: expected exactly one control`).toHaveCount(1, { timeout });
  return locator;
}

export async function prepareNativeAction(locator, label, { timeout = defaultTimeout, editable = false } = {}) {
  await requireUnique(locator, label, { timeout });
  await locator.click({ trial: true, timeout });
  if (editable) {
    await expect(locator, `${label}: control must be editable`).toBeEditable({ timeout });
  }
  return locator;
}

export async function performAction(tracker, label, locator, action, { timeout = defaultTimeout, editable = false } = {}) {
  const startedAt = Date.now();
  if (tracker) {
    tracker.activeAction = { label, locator: locator.toString(), targetLocator: locator, startedAt };
  }
  await expect(locator, `${label}: control must be visible`).toBeVisible({ timeout });
  await requireUnique(locator, label, { timeout });
  if (editable) {
    await expect(locator, `${label}: control must be editable`).toBeEditable({ timeout });
  }
  await action(locator, { timeout });
  const elapsedMs = Date.now() - startedAt;
  if (tracker) {
    tracker.actions ??= [];
    tracker.actions.push({ label, elapsedMs });
    tracker.activeAction = undefined;
  }
  return elapsedMs;
}
