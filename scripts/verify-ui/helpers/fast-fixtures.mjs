import { test as base, expect } from '@playwright/test';

const required = (name) => {
  const value = String(process.env[name] ?? '').trim();
  if (!value) throw new Error(name + ' is required; run the fast global setup first');
  return value;
};

export { expect };
export const test = base.extend({
  fastState: [async ({}, use) => {
    await use({
      project: required('LOOM_FAST_PROJECT'),
      explorerId: required('LOOM_FAST_EXPLORER'),
      uiUrl: required('LOOM_FAST_UI_URL'),
      apiUrl: required('LOOM_FAST_API_URL'),
    });
  }, { scope: 'worker' }],

  fastContext: [async ({ browser }, use) => {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      ...(process.env.LOOM_FAST_STORAGE_STATE ? { storageState: process.env.LOOM_FAST_STORAGE_STATE } : {}),
    });
    await use(context);
    await context.close();
  }, { scope: 'worker' }],

  fastPage: [async ({ fastContext }, use) => {
    const page = await fastContext.newPage();
    page.setDefaultTimeout(5_000);
    page.setDefaultNavigationTimeout(5_000);
    await use(page);
    await page.close();
  }, { scope: 'test' }],

  fastUrl: [async ({ fastState }, use) => {
    const url = new URL(fastState.uiUrl);
    url.searchParams.set('project', fastState.project);
    url.searchParams.set('explorer', fastState.explorerId);
    url.searchParams.set('mode', 'builder');
    await use(url.toString());
  }, { scope: 'test' }],
});
