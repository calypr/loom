export const normalizeRenderedBuilderHeader = value => String(value ?? '').replace(/\s+/g, ' ').trim().toLowerCase();

export const findRenderedBuilderHeaderIndex = (headers, expectedLabel) =>
  headers.findIndex(header => normalizeRenderedBuilderHeader(header) === normalizeRenderedBuilderHeader(expectedLabel));

export function builderRenderedGridWaitPredicate({ tableSelector, expectedRows }) {
  const table = document.querySelector(tableSelector);
  if (!table) return false;
  const rows = [...table.querySelectorAll('[role="row"]')];
  if (rows.length !== expectedRows.length + 1) return false;
  const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
    .map(cell => cell.innerText.trim());
  const headerIndex = label => headers.findIndex(header =>
    String(header ?? '').replace(/\s+/g, ' ').trim().toLowerCase()
      === String(label ?? '').replace(/\s+/g, ' ').trim().toLowerCase());
  const actualRows = rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')]);
  const valueMatches = (cell, expectation) => {
    if (!cell) return false;
    if (typeof expectation === 'string') return cell.innerText.trim() === expectation;
    if (expectation && typeof expectation === 'object' && Object.hasOwn(expectation, 'text')) {
      return cell.innerText.trim() === expectation.text;
    }
    const title = cell.getAttribute('title') ?? cell.querySelector('[title]')?.getAttribute('title') ?? null;
    if (expectation && typeof expectation === 'object' && Object.hasOwn(expectation, 'title')) {
      return title === expectation.title;
    }
    if (expectation && typeof expectation === 'object' && Array.isArray(expectation.titleJsonArray)) {
      let actual;
      try { actual = JSON.parse(title); } catch { return false; }
      return Array.isArray(actual)
        && JSON.stringify([...actual].sort()) === JSON.stringify([...expectation.titleJsonArray].sort());
    }
    return false;
  };
  const unusedRows = new Set(actualRows);
  for (const expectedRow of expectedRows) {
    const entries = Object.entries(expectedRow);
    const indexes = entries.map(([label]) => headerIndex(label));
    if (indexes.some(index => index < 0)) return false;
    const match = [...unusedRows].find(actualRow => entries.every(([, expectation], index) =>
      valueMatches(actualRow[indexes[index]], expectation)));
    if (!match) return false;
    unusedRows.delete(match);
  }
  return unusedRows.size === 0;
}

export const waitForBuilderRenderedGrid = (page, contract, timeout = 5000) =>
  page.waitForFunction(builderRenderedGridWaitPredicate, contract, { timeout });
