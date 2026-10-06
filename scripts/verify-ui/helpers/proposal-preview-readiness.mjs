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
