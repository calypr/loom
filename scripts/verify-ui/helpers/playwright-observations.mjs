/** Wait for observable DOM state using a Playwright callback and serializable condition data. */
export function waitForCondition(page, condition, timeout = 30000) {
  return page.waitForFunction((current) => {
    const element = selector => document.querySelector(selector);
    const includesText = (value, text) => String(value ?? '').includes(text);
    const check = item => {
      switch (item.kind) {
        case 'present': return Boolean(element(item.selector));
        case 'hidden': {
          const target = element(item.selector);
          return !target || target.getClientRects().length === 0
            || getComputedStyle(target).visibility === 'hidden';
        }
        case 'enabled': {
          const target = element(item.selector);
          return Boolean(target) && !target.disabled && target.getAttribute('aria-disabled') !== 'true';
        }
        case 'checked': return element(item.selector)?.checked === item.value;
        case 'open': return element(item.selector)?.open === item.value;
        case 'value': return element(item.selector)?.value === item.value;
        case 'text-includes': return includesText(element(item.selector)?.innerText, item.text);
        case 'body-text-excludes': return !includesText(document.body.innerText, item.text);
        case 'status-in': return item.statuses.includes(element(item.selector)?.dataset[item.attribute ?? 'proposalStatus']);
        case 'rows': {
          const table = element(item.selector);
          const rowCount = Number(table?.getAttribute('aria-rowcount'));
          const countMatches = item.allowEmptyParent && item.count === 0
            ? rowCount <= 1 : rowCount === item.count + 1;
          return Boolean(table) && countMatches && !includesText(document.body.innerText, item.loadingText ?? 'Loading your table');
        }
        case 'all': return item.conditions.every(check);
        case 'any': return item.conditions.some(check);
        case 'not': return !check(item.condition);
        case 'some-text': return [...document.querySelectorAll(item.selector)].some(node => includesText(node.innerText, item.text));
        case 'some-text-exact': return [...document.querySelectorAll(item.selector)].some(node => String(node.innerText ?? '').trim() === item.text);
        case 'label-input-starts': return [...document.querySelectorAll(item.selector)].some(node =>
          String(node.innerText ?? '').trim().startsWith(item.text) && Boolean(node.querySelector('input')));
        case 'label-input-value': return [...document.querySelectorAll(item.selector)].some(node =>
          String(node.innerText ?? '').trim().startsWith(item.text) && node.querySelector('input')?.value === item.value);
        case 'no-text': return ![...document.querySelectorAll(item.selector)].some(node => includesText(node.innerText, item.text));
        case 'count': return document.querySelectorAll(item.selector).length === item.count;
        case 'query': {
          const url = new URL(location.href);
          return Object.entries(item.values).every(([key, value]) => url.searchParams.get(key) === value);
        }
        case 'publish-complete': return [...document.querySelectorAll('button')].some(button =>
          button.textContent?.trim() === 'Publish' && button.getAttribute('aria-busy') !== 'true' && button.disabled);
        case 'viewer-pairs': {
          const tables = [...document.querySelectorAll('[role="table"],table')];
          for (const table of tables) {
            if (item.selector && !table.matches(item.selector)) continue;
            const headers = [...table.querySelectorAll('[role="columnheader"],thead th')]
              .map(cell => (cell.innerText || cell.textContent || '').trim());
            const idIndex = headers.findIndex(header => header.split(String.fromCharCode(10))[0].trim().toUpperCase() === 'OBSERVATION ID');
            const valueIndex = headers.findIndex(header => /component.*value.?string/i.test(header));
            if (idIndex < 0 || valueIndex < 0) continue;
            const rows = [...table.querySelectorAll('[role="row"],tbody tr')]
              .filter(row => !row.querySelector('[role="columnheader"],th'));
            const parse = cell => {
              let value;
              try { value = JSON.parse(cell.title); } catch { value = (cell.innerText || cell.textContent || '').trim(); }
              if (Array.isArray(value)) { if (value.length !== 1) return null; value = value[0]; }
              return typeof value === 'string' ? value : null;
            };
            const pairs = rows.map(row => {
              const cells = [...row.querySelectorAll('[role="cell"],td')];
              return [parse(cells[idIndex]), parse(cells[valueIndex])];
            });
            if (pairs.length !== item.pairs.length || pairs.some(pair => pair.some(value => value === null))) continue;
            pairs.sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
            const expected = item.pairs.slice().sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
            if (JSON.stringify(pairs) === JSON.stringify(expected)) return true;
          }
          return false;
        }
        default: throw new Error(`Unsupported Playwright observation condition: ${item.kind}`);
      }
    };
    return check(current);
  }, condition, { timeout });
}
