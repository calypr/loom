import assert from 'node:assert/strict';

export function previewHeaderIndex(headers, label) {
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim().toLowerCase();
  const expected = normalize(label);
  const matches = headers.flatMap((header, index) => normalize(header) === expected ? [index] : []);
  assert.equal(matches.length, 1, `Rendered header ${JSON.stringify(label)} must occur exactly once`);
  return matches[0];
}

/** Read every rendered preview row while scrolling with Playwright's native wheel input. */
export async function collectPreviewRows(page, {
  containerSelector = '[data-testid="preview-table-scroll"]',
  tableSelector = '[role="table"]',
  rowSelector = '[role="row"]',
  cellSelector = '[role="cell"]',
  timeout = 5000,
} = {}) {
  assert(Number.isFinite(timeout) && timeout > 0, 'Preview collection timeout must be a positive finite number');
  const deadline = Date.now() + timeout;
  const remaining = () => Math.max(1, deadline - Date.now());
  const assertWithinDeadline = () => assert(Date.now() < deadline,
    `Preview row collection exceeded its ${timeout}ms total deadline`);
  const container = page.locator(containerSelector);
  await container.waitFor({ state: 'visible', timeout: remaining() });
  assertWithinDeadline();
  assert.equal(await container.count(), 1, `Preview container must be unique: ${containerSelector}`);
  const table = container.locator(tableSelector);
  await table.waitFor({ state: 'visible', timeout: remaining() });
  assertWithinDeadline();
  assert.equal(await table.count(), 1, `Preview table must be unique: ${tableSelector}`);
  const metadata = await table.evaluate(element => ({
    rowCount: Math.max(0, Number(element.getAttribute('aria-rowcount')) - 1),
    ariaRowCount: element.getAttribute('aria-rowcount'),
    headers: [...element.querySelectorAll('[role="columnheader"], th')].map(cell => cell.innerText.trim().split('\n')[0]),
  }));
  assert(Number.isInteger(metadata.rowCount), `Preview table has invalid aria-rowcount: ${metadata.ariaRowCount}`);
  const rows = new Map();
  await container.scrollIntoViewIfNeeded({ timeout: remaining() });
  assertWithinDeadline();
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
    assertWithinDeadline();
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
    assertWithinDeadline();
    for (const row of mounted) {
      assert(row.rowNumber <= metadata.rowCount,
        `Preview exposed row ${row.rowNumber} outside aria-rowcount ${metadata.rowCount}`);
      const prior = rows.get(row.rowNumber);
      if (prior) {
        assert.deepEqual(row.values, prior.values,
          `Preview row ${row.rowNumber} changed between scroll windows`);
      } else {
        rows.set(row.rowNumber, row);
      }
    }
    if (rows.size >= metadata.rowCount) break;
    const position = await container.evaluate(element => ({
      top: element.scrollTop,
      maxTop: Math.max(0, element.scrollHeight - element.clientHeight),
      height: element.clientHeight,
    }));
    assertWithinDeadline();
    if (position.top >= position.maxTop) break;
    const nextOrdinal = Math.max(...rows.keys()) + 1;
    await page.mouse.wheel(0, Math.min(Math.max(1, position.height * 0.9), position.maxTop - position.top));
    await page.waitForFunction(({ selector, previousTop, nextOrdinal, rowSelector }) => {
      const element = document.querySelector(selector);
      const tableElement = element?.querySelector('[role="table"]');
      const rowNumbers = [...(tableElement?.querySelectorAll(rowSelector) ?? [])].slice(1)
        .map(row => Number(row.firstElementChild?.textContent?.trim()))
        .filter(Number.isInteger);
      return element && element.scrollTop > previousTop && rowNumbers.some(number => number >= nextOrdinal);
    }, { selector: containerSelector, previousTop: position.top, nextOrdinal, rowSelector },
    { timeout: remaining() });
    assertWithinDeadline();
  }
  const position = await container.evaluate(element => ({ top: element.scrollTop, maxTop: element.scrollHeight - element.clientHeight }));
  if (position.top > 0) {
    await page.mouse.wheel(0, -(position.maxTop + position.top + 100));
    await page.waitForFunction(({ selector, rowSelector }) => {
      const element = document.querySelector(selector);
      if (element?.scrollTop !== 0) return false;
      const rows = [...(element.querySelector('[role="table"]')?.querySelectorAll(rowSelector) ?? [])].slice(1);
      return rows.some(row => Number(row.firstElementChild?.textContent?.trim()) === 1);
    }, { selector: containerSelector, rowSelector }, { timeout: remaining() });
    assertWithinDeadline();
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
