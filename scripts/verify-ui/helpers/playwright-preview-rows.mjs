import assert from 'node:assert/strict';

/** Read every rendered preview row while scrolling with Playwright's native wheel input. */
export async function collectPreviewRows(page, {
  containerSelector = '[data-testid="preview-table-scroll"]',
  tableSelector = '[role="table"]',
  rowSelector = '[role="row"]',
  cellSelector = '[role="cell"]',
  timeout = 5000,
} = {}) {
  const container = page.locator(containerSelector);
  await container.waitFor({ state: 'visible', timeout });
  assert.equal(await container.count(), 1, `Preview container must be unique: ${containerSelector}`);
  const table = container.locator(tableSelector);
  await table.waitFor({ state: 'visible', timeout });
  assert.equal(await table.count(), 1, `Preview table must be unique: ${tableSelector}`);
  const metadata = await table.evaluate(element => ({
    rowCount: Math.max(0, Number(element.getAttribute('aria-rowcount')) - 1),
    ariaRowCount: element.getAttribute('aria-rowcount'),
    headers: [...element.querySelectorAll('[role="columnheader"], th')].map(cell => cell.innerText.trim().split('\n')[0]),
  }));
  assert(Number.isInteger(metadata.rowCount), `Preview table has invalid aria-rowcount: ${metadata.ariaRowCount}`);
  const rows = new Map();
  await container.scrollIntoViewIfNeeded();
  const box = await container.boundingBox();
  assert(box, 'Visible preview container must have a bounding box');
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  const pointerInside = await page.evaluate(({ x, y, selector }) => {
    const containerElement = document.querySelector(selector);
    const target = document.elementFromPoint(x, y);
    return Boolean(containerElement && target && containerElement.contains(target));
  }, { x: box.x + box.width / 2, y: box.y + box.height / 2, selector: containerSelector });
  assert(pointerInside, `Preview scrolling pointer must hit the preview container: ${containerSelector}`);
  for (let pass = 0; pass < 200 && rows.size < metadata.rowCount; pass += 1) {
    const mounted = await table.locator(rowSelector).evaluateAll((elements, selector) => elements.map(row => {
      const first = row.firstElementChild;
      const rowNumber = Number(first?.textContent?.trim());
      const values = [...row.querySelectorAll(selector)].map(cell => cell.innerText.trim());
      if (!Number.isInteger(rowNumber) || rowNumber < 1 || !values.length) return null;
      return {
        rowNumber,
        rowIdentityLabel: row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label'),
        values,
      };
    }).filter(Boolean), cellSelector);
    for (const row of mounted) rows.set(row.rowNumber, row);
    if (rows.size >= metadata.rowCount) break;
    const position = await container.evaluate(element => ({
      top: element.scrollTop,
      maxTop: Math.max(0, element.scrollHeight - element.clientHeight),
      height: element.clientHeight,
    }));
    if (position.top >= position.maxTop) break;
    await page.mouse.wheel(0, Math.min(Math.max(1, position.height * 0.65), position.maxTop - position.top));
    await page.waitForFunction(({ selector, previousTop }) => {
      const element = document.querySelector(selector);
      return element && element.scrollTop > previousTop;
    }, { selector: containerSelector, previousTop: position.top }, { timeout });
  }
  const position = await container.evaluate(element => ({ top: element.scrollTop, maxTop: element.scrollHeight - element.clientHeight }));
  if (position.top > 0) {
    await page.mouse.wheel(0, -(position.maxTop + position.top + 100));
    await page.waitForFunction(selector => document.querySelector(selector)?.scrollTop === 0,
      containerSelector, { timeout });
  }
  const ordered = [...rows.values()].sort((left, right) => left.rowNumber - right.rowNumber);
  assert.equal(ordered.length, metadata.rowCount,
    `Preview collection stopped at ${ordered.length} of ${metadata.rowCount} rows`);
  assert.deepEqual(ordered.map(row => row.rowNumber),
    Array.from({ length: metadata.rowCount }, (_, index) => index + 1),
    'Preview row ordinals must be contiguous and complete');
  return {
    ...metadata,
    rowOrdinals: ordered.map(row => row.rowNumber),
    rows: ordered,
  };
}
