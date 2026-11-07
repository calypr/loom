import assert from 'node:assert/strict';
import { verifyBrowserDisclosure } from '../verify-ui/helpers/verify-browser-disclosure.mjs';

const historical = 'e736e1300716e8cdfe4dbfbe370e87b6713bebd2^';
const routeA = [['p1', 'o1'], ['p2', 'o2']];
const routeB = [['p1', 'o2']];
const saved = page => page.getByTestId('route-saved').textContent().then(JSON.parse);
const rows = page => page.locator('[data-testid="route-preview"] [role="row"]').evaluateAll(elements =>
  elements.map(row => Array.from(row.querySelectorAll('[role="cell"]'), cell => cell.textContent.trim())
    .filter(Boolean)).filter(row => row.length));

export const cases = [
  {
    id: 'preview-load',
    provenance: `${historical}:scripts/verify-ui/fast-specs/builder-preview-read-only.spec.mjs`,
    boundary: 'Production PreviewTable exact rows and labels across component reload; controlled data. No live preview endpoint claim.',
    async run(page, origin) {
      await page.goto(`${origin}/?feature=drag`, { waitUntil: 'domcontentloaded' });
      const observations = [];
      for (const pass of [0, 1]) {
        if (pass) await page.reload({ waitUntil: 'domcontentloaded' });
        await page.getByTestId('preview-table-scroll').waitFor({ state: 'visible' });
        const headers = await page.getByRole('columnheader').allTextContents();
        const cells = await page.locator('[role="cell"]').allTextContents();
        assert.deepEqual(headers, ['First', 'Second']);
        assert.deepEqual(cells, ['patient-A', 'observation-A', 'patient-B', 'observation-B']);
        observations.push({ headers, cells });
      }
      return observations;
    },
  },
  {
    id: 'preview-identity',
    provenance: `${historical}:scripts/verify-ui/fast-specs/builder-preview-read-only.spec.mjs`,
    retainedBoundary: 'scripts/browser/run.mjs: native row identity inspection',
    boundary: 'Production PreviewTable; test-owned data and caller. No API or database claim.',
    async run(page, origin) {
      const observations = [];
      await page.goto(`${origin}/?feature=drag`, { waitUntil: 'domcontentloaded' });
      for (const pass of [0, 1]) {
        if (pass) await page.reload({ waitUntil: 'domcontentloaded' });
        await page.getByTestId('preview-table-scroll').waitFor({ state: 'visible' });
        assert.deepEqual(await page.getByRole('columnheader').allTextContents(), ['First', 'Second']);
        for (const [index, identity] of [[1, 'canonical-row-A'], [2, 'canonical-row-B']]) {
          await page.getByRole('button', { name: `Inspect row ${index} identity`, exact: true }).click();
          const dialog = page.getByRole('dialog');
          await dialog.waitFor({ state: 'visible' });
          assert.ok((await dialog.textContent()).includes(identity));
          observations.push(identity);
          await dialog.getByRole('button').click();
          await dialog.waitFor({ state: 'hidden' });
        }
        assert.deepEqual(await page.locator('[role="cell"]').allTextContents(),
          ['patient-A', 'observation-A', 'patient-B', 'observation-B']);
      }
      return { identities: observations, rows: 2, reloads: 1 };
    },
  },
  {
    id: 'disclosure',
    provenance: 'scripts/verify-ui/helpers/verify-browser-disclosure.mjs',
    boundary: 'Retained Playwright helper, unchanged native disclosure and visibility assertions.',
    async run(page) { return verifyBrowserDisclosure({ page }); },
  },
  {
    id: 'saved-route',
    provenance: `${historical}:scripts/verify-ui/specs/builder-saved-route-edit.spec.mjs`,
    boundary: 'Production RelatedExpandEditor, proposal lifecycle and PreviewTable; controlled service and sessionStorage caller. Not deployed Builder acceptance.',
    async run(page, origin) {
      const choose = async id => {
        if (await page.locator(`input[aria-label*="${id}"]`).count() === 0)
          await page.getByTestId('construction-related-expand-other-routes').locator('summary').click();
        await page.locator(`input[aria-label*="${id}"]`).click();
        await page.waitForFunction(() => document.querySelector('[data-testid="route-proposal"]').textContent === 'ready');
      };
      const expectState = async (route, version, writes, expectedRows) => {
        assert.deepEqual(await saved(page), { route, version, writes });
        assert.deepEqual(await rows(page), expectedRows);
        assert.ok((await page.locator('input[type="radio"][name^="related-expand-route"]:checked').getAttribute('aria-label')).includes(route));
      };
      const reload = async () => {
        await page.reload({ waitUntil: 'domcontentloaded' });
        await page.getByTestId('route-saved').waitFor({ state: 'visible' });
      };
      await page.goto(`${origin}/?feature=route`, { waitUntil: 'domcontentloaded' });
      await page.getByTestId('route-saved').waitFor({ state: 'visible' });
      await expectState('route-A', 1, 0, routeA);
      await choose('route-B');
      assert.deepEqual(await rows(page), routeB);
      await page.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expectState('route-A', 1, 0, routeA);
      await reload();
      await expectState('route-A', 1, 0, routeA);
      await choose('route-B');
      await page.getByRole('button', { name: 'Apply change', exact: true }).click();
      await expectState('route-B', 2, 1, routeB);
      await reload();
      await expectState('route-B', 2, 1, routeB);
      await choose('route-A');
      await page.getByRole('button', { name: 'Apply change', exact: true }).click();
      await expectState('route-A', 3, 2, routeA);
      await page.getByTestId('route-hold').click();
      if (await page.locator('input[aria-label*="route-B"]').count() === 0)
        await page.getByTestId('construction-related-expand-other-routes').locator('summary').click();
      await page.locator('input[aria-label*="route-B"]').click();
      await page.waitForFunction(() => !document.querySelector('[data-testid="route-release"]').disabled);
      await choose('route-A');
      await page.getByTestId('route-release').click();
      await page.waitForFunction(() => document.querySelector('[data-testid="route-released"]').textContent === '1');
      await page.waitForTimeout(100);
      await expectState('route-A', 3, 2, routeA);
      await page.getByRole('button', { name: 'Cancel', exact: true }).click();
      await reload();
      await expectState('route-A', 3, 2, routeA);
      return { routeA, routeB, versions: [1, 2, 3], writes: 2, cancelWrites: 0, lateResponseIgnored: true };
    },
  },
];
