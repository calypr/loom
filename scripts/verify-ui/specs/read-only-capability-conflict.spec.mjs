import { test, expect } from '@playwright/test';

const env = process.env;
const project = env.LOOM_CDA_PROJECT?.trim();
const explorer = (env.LOOM_CDA_EXPLORER_ID ?? env.LOOM_CDA_EXPLORER)?.trim();
const uiOrigin = env.LOOM_CDA_UI_ORIGIN?.trim();
const apiOrigin = env.LOOM_CDA_API_ORIGIN?.trim();
for (const [name, value] of Object.entries({
  LOOM_CDA_PROJECT: project,
  LOOM_CDA_EXPLORER_ID: explorer,
  LOOM_CDA_UI_ORIGIN: uiOrigin,
  LOOM_CDA_API_ORIGIN: apiOrigin,
})) {
  if (!value) throw new Error(`${name} must identify the owned read-only Builder target.`);
}

const origins = new Set([new URL(uiOrigin).origin, new URL(apiOrigin).origin]);
const projectPath = encodeURIComponent(encodeURIComponent(project));
const explorerPath = `/api/v1/projects/${projectPath}/explorers/${encodeURIComponent(explorer)}`;
const builderPath = `${explorerPath}/authoring/v2/builder`;
const capabilitiesPath = `${explorerPath}/authoring/v2/construction-capabilities`;
const readOnlyPosts = /\/authoring\/v2\/(?:reconcile|preview|construction-capabilities|construction-proposals|row-definition-choices|row-definition-proposals|semantic-inventory|configured-column-context)$/;

const builderURL = () => {
  const url = new URL(uiOrigin);
  url.searchParams.set('project', project);
  url.searchParams.set('explorer', explorer);
  url.searchParams.set('mode', 'builder');
  return url.toString();
};
const authHeaders = () => {
  const token = env.LOOM_CDA_API_TOKEN ?? env.LOOM_CDA_TOKEN;
  return token ? { Authorization: `Bearer ${token}` } : {};
};
const readBuilder = async request => {
  const response = await request.get(`${apiOrigin}${builderPath}`, { headers: authHeaders() });
  expect(response.ok(), 'saved Builder state is readable').toBe(true);
  const builder = await response.json();
  expect(builder.draftVersion).toEqual(expect.any(Number));
  expect(builder.draftDigest).toEqual(expect.any(String));
  return builder;
};
const identity = builder => ({ draftVersion: builder.draftVersion, draftDigest: builder.draftDigest });
const protectWrites = async (page, conflictOnce = false) => {
  const blockedWrites = [];
  const capabilities = [];
  const guard = { blockedWrites, capabilities, injectedConflicts: 0 };
  await page.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (!origins.has(url.origin)) {
      await route.continue();
      return;
    }
    const method = request.method().toUpperCase();
    if (url.pathname === capabilitiesPath && method === 'POST') {
      capabilities.push(request.postDataJSON());
      if (conflictOnce && capabilities.length === 1) {
        guard.injectedConflicts += 1;
        await route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({ error: { code: 'DRAFT_CONFLICT', message: 'the Explorer draft changed; reload before requesting construction capabilities' } }),
        });
        return;
      }
      await route.continue();
      return;
    }
    const isTargetReadOnlyPost = url.pathname.startsWith(`${explorerPath}/`) && readOnlyPosts.test(url.pathname);
    if (['GET', 'HEAD', 'OPTIONS'].includes(method) || (method === 'POST' && isTargetReadOnlyPost)) {
      await route.continue();
      return;
    }
    blockedWrites.push(`${method} ${url.pathname}`);
    await route.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({ error: { code: 'READ_ONLY_PROBE_BLOCKED', message: 'This probe blocks Builder writes.' } }) });
  });
  return guard;
};
const openBuilder = async page => {
  await page.goto(builderURL(), { waitUntil: 'domcontentloaded' });
  await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible' });
};
const finishReadOnly = async (request, before, blockedWrites) => {
  const after = await readBuilder(request);
  expect(identity(after), 'the saved draft identity stayed unchanged').toEqual(identity(before));
  expect(blockedWrites, 'no user write escaped the read-only route guard').toEqual([]);
};

test.describe('read-only Builder proposal and DRAFT_CONFLICT probe', () => {
  test('shows a ready proposal in the DOM and Cancel leaves the saved draft unchanged', async ({ page }) => {
    const before = await readBuilder(page.request);
    const guard = await protectWrites(page);
    try {
      await openBuilder(page);
      await page.getByTestId('construction-rows-settings-trigger').click();
      const group = page.getByTestId('construction-action-source-group-rows');
      await expect(group).toBeVisible();
      await expect(group).toBeEnabled();
      const proposalResponse = page.waitForResponse(response => {
        const url = new URL(response.url());
        return origins.has(url.origin) && url.pathname === `${explorerPath}/authoring/v2/construction-proposals`
          && response.request().method() === 'POST';
      }, { timeout: 10_000 });
      const [response] = await Promise.all([proposalResponse, group.click()]);
      const panel = page.getByTestId('construction-proposal-panel');
      const preview = page.getByTestId('construction-proposal-preview');
      await expect(panel).toHaveAttribute('data-proposal-status', 'ready');
      await expect(preview).toHaveAttribute('data-preview-status', 'ready');
      await expect(page.getByTestId('construction-proposal-preview-row').first()).toBeVisible();
      expect(response.status()).toBe(200);
      const body = await response.json();
      await expect(panel).toHaveAttribute('data-proposal-id', body.proposalId);
      await expect(preview).toHaveAttribute('data-preview-receipt-id', body.preview.receiptId);
      await page.getByTestId('construction-cancel-proposal').click();
      await expect(panel).toBeHidden();
    } finally {
      await finishReadOnly(page.request, before, guard.blockedWrites);
    }
  });

  test('recovers current capabilities after one read-only DRAFT_CONFLICT', async ({ page }) => {
    const before = await readBuilder(page.request);
    const guard = await protectWrites(page, true);
    try {
      await openBuilder(page);
      await expect.poll(() => guard.capabilities.length, { message: 'construction capabilities request was observed' }).toBeGreaterThan(0);
      expect(guard.injectedConflicts, 'exactly one synthetic read-only DRAFT_CONFLICT was returned').toBe(1);
      await expect.poll(() => guard.capabilities.length,
        { message: 'the Builder retries capabilities with its current draft identity after DRAFT_CONFLICT' }).toBeGreaterThan(1);
      expect(guard.capabilities[0].expectedDraftVersion).toBe(before.draftVersion);
      expect(guard.capabilities[0].expectedDraftDigest).toBe(before.draftDigest);
      expect(guard.capabilities[1].expectedDraftVersion).toBe(before.draftVersion);
      expect(guard.capabilities[1].expectedDraftDigest).toBe(before.draftDigest);
      await page.getByTestId('construction-rows-settings-trigger').click();
      const group = page.getByTestId('construction-action-source-group-rows');
      await expect(group).toBeEnabled();
      const proposal = page.waitForResponse(response => new URL(response.url()).pathname === `${explorerPath}/authoring/v2/construction-proposals`
        && response.request().method() === 'POST', { timeout: 10_000 });
      const [proposalResponse] = await Promise.all([proposal, group.click()]);
      await expect(page.getByTestId('construction-proposal-panel')).toHaveAttribute('data-proposal-status', 'ready');
      expect(proposalResponse.status()).toBe(200);
      await page.getByTestId('construction-cancel-proposal').click();
    } finally {
      await finishReadOnly(page.request, before, guard.blockedWrites);
    }
  });
});
