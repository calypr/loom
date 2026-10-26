const AUTHORING_PREFIX = '/authoring/v2/';

export const isCdaDataset = () => ['cda', 'mini'].includes(process.env.LOOM_JOURNEY_DATASET ?? '');

export const waitForPreviewReady = async (page, timeout = 45_000) => {
  await page.waitForFunction(() => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && Boolean(preview.dataset.previewReceiptId);
  }, undefined, { timeout });
};

export const waitForProposalSettled = async (page, timeout = 30_000) => {
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  }, undefined, { timeout });
};

export const addField = async (page, fieldName) => {
  await page.getByTestId('construction-action-add-columns').click();
  await page.getByRole('tab', { name: 'Other fields' }).click();
  const field = page.getByRole('button', { name: fieldName, exact: true });
  await field.waitFor({ timeout: 15_000 });
  await field.click();
  const proposal = page.waitForResponse((response) => {
    if (response.request().method() !== 'POST') return false;
    if (!new URL(response.url()).pathname.includes(AUTHORING_PREFIX)) return false;
    return new URL(response.url()).pathname.endsWith('/construction-choice-proposals');
  }, { timeout: 30_000 });
  const commands = page.waitForResponse((response) => {
    if (response.request().method() !== 'POST') return false;
    if (!new URL(response.url()).pathname.includes(AUTHORING_PREFIX)) return false;
    return new URL(response.url()).pathname.endsWith('/commands');
  }, { timeout: 30_000 });
  await page.getByRole('button', { name: /^Add \d+ selected feature$/ }).click();
  const proposalResponse = await proposal;
  const body = await proposalResponse.json();
  const commandsResponse = await commands;
  if (!proposalResponse.ok() || !commandsResponse.ok()) {
    throw new Error(`add field ${fieldName} failed: proposal HTTP ${proposalResponse.status()}, command HTTP ${commandsResponse.status()}`);
  }
  const back = page.getByRole('button', { name: 'Back to table' });
  if (await back.isVisible().catch(() => false)) await back.click();
  await waitForPreviewReady(page);
  return body.preview ?? body;
};

export const previewLabels = (preview) => (preview?.columns ?? []).map((column) => column.label);
export const previewRowCount = (preview) => Number(preview?.rowCount ?? preview?.rows?.length ?? 0);
