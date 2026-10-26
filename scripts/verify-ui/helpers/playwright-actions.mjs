const defaultTimeout = 5_000;

const loadExpect = async () => (await import('@playwright/test')).expect;

const requireUniqueWith = async (expect, locator, label, { timeout = defaultTimeout } = {}) => {
  await expect(locator, `${label}: expected exactly one control`).toHaveCount(1, { timeout });
  return locator;
};

export async function requireUnique(locator, label, options) {
  return requireUniqueWith(await loadExpect(), locator, label, options);
}

export async function prepareNativeAction(locator, label, { timeout = defaultTimeout, editable = false } = {}) {
  const expect = await loadExpect();
  await requireUniqueWith(expect, locator, label, { timeout });
  await locator.click({ trial: true, timeout });
  if (editable) {
    await expect(locator, `${label}: control must be editable`).toBeEditable({ timeout });
  }
  return locator;
}

export async function performAction(tracker, label, locator, action, { timeout = defaultTimeout, editable = false } = {}) {
  const expect = await loadExpect();
  const startedAt = Date.now();
  if (tracker) {
    tracker.activeAction = { label, locator: locator.toString(), targetLocator: locator, startedAt };
  }
  await expect(locator, `${label}: control must be visible`).toBeVisible({ timeout });
  await requireUniqueWith(expect, locator, label, { timeout });
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
