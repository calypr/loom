const panelSelector = '[data-testid="construction-proposal-panel"][data-proposal-status="ready"]';
const previewSelector = '[data-testid="construction-proposal-preview"][data-preview-status="ready"]';
const previewRowSelector = 'tbody tr[data-testid="construction-proposal-preview-row"]';

// The proposal summary panel and preview table are siblings in the Builder DOM.
// Bind them by receipt and output identity so stale or unrelated preview tables
// cannot satisfy a readiness wait.
export const proposalPreviewReadinessExpression = (expectedOutputId, expectedRowCount) => {
  if (typeof expectedOutputId !== 'string' || expectedOutputId.trim() === '') {
    throw new TypeError('proposal preview readiness requires the expected output ID');
  }
  if (expectedRowCount !== undefined && (!Number.isInteger(expectedRowCount) || expectedRowCount < 0)) {
    throw new TypeError('proposal preview row count must be a non-negative integer');
  }

  const rowCountCheck = expectedRowCount === undefined
    ? ''
    : `&&preview.querySelectorAll(${JSON.stringify(previewRowSelector)}).length===${expectedRowCount}`;

  return `(()=>{const panel=document.querySelector(${JSON.stringify(panelSelector)});` +
    `const preview=document.querySelector(${JSON.stringify(previewSelector)});` +
    `const proposalId=panel?.getAttribute('data-proposal-id')??'';` +
    `const previewReceiptId=preview?.getAttribute('data-preview-receipt-id')??'';` +
    `const previewOutputId=preview?.getAttribute('data-preview-output-id')??'';` +
    `return Boolean(panel&&preview&&proposalId&&previewReceiptId===proposalId&&` +
    `previewOutputId===${JSON.stringify(expectedOutputId)}${rowCountCheck})})()`;
};

export function proposalPreviewStateInPage(expectedOutputId) {
  const panels = [...document.querySelectorAll('[data-testid="construction-proposal-panel"]')];
  const results = [...document.querySelectorAll('[data-testid="construction-preview"]')]
    .filter(node => node.getAttribute('data-preview-output-id') === expectedOutputId);
  const previews = results.flatMap(result => [...result.querySelectorAll('[data-testid="construction-proposal-preview"]')]);
  const panel = panels.length === 1 ? panels[0] : undefined;
  const result = results.length === 1 ? results[0] : undefined;
  const preview = previews.length === 1 ? previews[0] : undefined;
  return {
    proposalPanelCount: panels.length,
    proposalStatus: panel?.getAttribute('data-proposal-status') ?? null,
    proposalId: panel?.getAttribute('data-proposal-id') ?? null,
    resultSectionCount: results.length,
    resultStatus: result?.getAttribute('data-preview-status') ?? null,
    resultOutputId: result?.getAttribute('data-preview-output-id') ?? null,
    resultReceiptId: result?.getAttribute('data-preview-receipt-id') ?? null,
    resultProposalId: result?.getAttribute('data-preview-proposal-id') ?? null,
    proposalPreviewCount: previews.length,
    previewStatus: preview?.getAttribute('data-preview-status') ?? null,
    previewOutputId: preview?.getAttribute('data-preview-output-id') ?? null,
    previewReceiptId: preview?.getAttribute('data-preview-receipt-id') ?? null,
    statusText: preview?.querySelector('[role="status"]')?.textContent?.trim() ?? '',
    footerText: preview?.querySelector(':scope > p:last-child')?.textContent?.replace(/\s+/g, ' ').trim() ?? '',
    tableCount: preview?.querySelectorAll('table').length ?? -1,
  };
}

export const readProposalPreviewState = (page, expectedOutputId) =>
  page.evaluate(proposalPreviewStateInPage, expectedOutputId);
