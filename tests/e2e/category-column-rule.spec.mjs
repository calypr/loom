import assert from 'node:assert/strict';
import test from 'node:test';
import { resolve } from 'node:path';
import { api, postCommands, readFixture, withFreshUIExplorer } from './app.mjs';

const fixtureDirectory = resolve(import.meta.dirname, '../fixtures/category-column-rule');

test('category coding rule keeps each system paired with its own display', { timeout: 180000 }, async () => {
  const observations = await readFixture('Observation', fixtureDirectory);
  const expected = observations.map(observation => ({
    patientID: observation.subject.reference.split('/').at(-1),
    values: Object.fromEntries(observation.category.flatMap(item => item.coding.map(coding => [
      coding.system.split('/').at(-1), coding.display,
    ]))),
  }));
  assert.equal(expected.length, 2);
  assert.deepEqual(observations.map(row => row.category[0].coding[0].code), ['a', 'b']);

  await withFreshUIExplorer('category coding rule', async (scenario, page) => {
    const clone = await api(scenario.root, {
      method: 'POST',
      body: { name: `${scenario.explorerId}-imported`, sourceExplorerId: 'dataframe-default' },
    });
    const url = new URL(scenario.url);
    url.searchParams.set('explorer', clone.explorerId);
    await page.navigate(url.toString());
    return { clone, base: `${scenario.root}/${encodeURIComponent(clone.explorerId)}/authoring/v2` };
  }, async ({ scenario, fixture, page }) => {
    const readDraft = () => api(`${fixture.base}/builder`);
    const buttonReady = text => page.evaluate(`[...document.querySelectorAll('button')].some(button =>
      button.innerText === ${JSON.stringify(text)} && !button.disabled)`);
    try {
      const initial = await readDraft();
      const source = initial.workspace.documents[0];
      const nodes = new Map(initial.catalog.nodes.map(node => [node.nodeId, node.resourceType]));
      const edge = initial.catalog.edges.find(candidate => candidate.label === 'subject_Patient' &&
        nodes.get(candidate.fromNodeId) === 'Patient' && nodes.get(candidate.toNodeId) === 'Observation');
      assert(edge, 'The fixture authorizes an Observation relationship');
      const proposal = await api(`${fixture.base}/source-relationship-proposals`, {
        method: 'POST',
        body: {
          snapshotToken: initial.catalog.snapshotToken, expectedDraftVersion: initial.draftVersion,
          expectedDraftDigest: initial.draftDigest, outputId: source.output.id,
          change: { action: 'ADD', parentOccurrenceId: source.route.occurrenceId, edgeId: edge.edgeId, matchMode: 'OPTIONAL' },
        },
      });
      await postCommands(fixture, [{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: source.output.id, proposalId: proposal.proposalId }]);
      const connected = await readDraft();
      const occurrence = connected.workspace.documents[0].route.children.find(child => child.resourceType === 'Observation');
      const selector = `button[data-occurrence-selector="true"][data-occurrence-id=${JSON.stringify(occurrence.occurrenceId)}]`;
      await page.reload();
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(selector)}))`),
        'saved Observation occurrence is available after reload');
      await page.click(selector);
      await page.click('[data-testid="construction-action-add-columns"]');
      await page.click('[role="tab"][data-column-tab="advanced"]');
      await page.clickText('Category codings');
      assert(!await page.evaluate(`Boolean(document.querySelector('[aria-label="Column name expression"]'))`));
      await page.clickText('Review column rule');
      await page.clickText('Preview column rule');
      await page.until(() => buttonReady('Apply column rule'), 'category coding preview');
      const assertPairedRows = () => page.until(async () => {
        const headers = await page.evaluate(`[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"], thead th')].map(header => header.innerText.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_'))`);
        const rows = await page.tableRows();
        return expected.every(({ patientID, values }) => rows.some(row => row.includes(patientID) &&
          Object.entries(values).every(([key, value]) => row[headers.findIndex(header => header.includes('category_codings_' + key))] === value))) || false;
      }, 'categories remain paired by system');
      await assertPairedRows();
      assert.equal((await readDraft()).draftDigest, connected.draftDigest);
      await page.clickText('Apply column rule');
      const saved = await page.until(async () => {
        const draft = await readDraft();
        return draft.workspace.documents[0].importedSource.ruleAdditions?.length ? draft : false;
      }, 'category rule is saved');
      const [rule] = saved.workspace.documents[0].importedSource.ruleAdditions;
      assert.equal(rule.occurrenceId, occurrence.occurrenceId);
      assert.equal(rule.parameters.dynamic.source.select, 'category[].coding[]');
      await page.reload();
      await assertPairedRows();
    } finally {
      const capability = await api(`${fixture.base}/capability`);
      if (capability.features?.deleteExplorer) {
        await api(`${scenario.root}/${encodeURIComponent(fixture.clone.explorerId)}`, { method: 'DELETE' });
      }
    }
  }, { fixtureDirectory });
});
