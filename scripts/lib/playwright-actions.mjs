import assert from 'node:assert/strict';

export async function requireUnique(locator, label) {
  const count = await locator.count();
  assert.equal(count, 1, `${label}: expected one control, found ${count}`);
  return locator;
}

export async function performAction(tracker, label, locator, action, { timeout = 5000, editable = false } = {}) {
  const startedAt = Date.now();
  if (tracker) {
    tracker.activeAction = { label, locator: locator.toString(), targetLocator: locator, startedAt };
  }
  await locator.waitFor({ state: 'visible', timeout });
  await requireUnique(locator, label);
  if (editable) assert.equal(await locator.isEditable(), true, `${label}: control is not editable`);
  await action(locator, { timeout });
  const elapsedMs = Date.now() - startedAt;
  if (tracker) {
    tracker.actions ??= [];
    tracker.actions.push({ label, elapsedMs });
    tracker.activeAction = undefined;
  }
  return elapsedMs;
}
