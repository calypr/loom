import assert from 'node:assert/strict';
import test from 'node:test';
import { resolve } from 'node:path';
import { api, postCommands, readFixture, withFreshUIExplorer } from './app.mjs';

test('create an identifier column rule, preview its values, and reopen it from Recipe steps', { timeout: 180000 }, async () => {
  const patients = await readFixture('Patient');
  const expected = patients.flatMap(patient => patient.identifier?.map(item => item.value) ?? []);
  assert(expected.length > 2);

  await withFreshUIExplorer('identifier rule', async (scenario, page) => {
    const clone = await api(scenario.root, {
      method: 'POST',
      body: { name: `${scenario.explorerId}-imported`, sourceExplorerId: 'dataframe-default' },
    });
    const base = `${scenario.root}/${encodeURIComponent(clone.explorerId)}/authoring/v2`;
    const url = new URL(scenario.url);
    url.searchParams.set('explorer', clone.explorerId);
    await page.navigate(url.toString());
    return { base, clone };
  }, async ({ scenario, fixture, page }) => {
    const readDraft = () => api(`${fixture.base}/builder`);
    const before = await readDraft();
    assert.equal(before.workspace.documents[0].rootResourceType, 'Patient');
    assert(before.workspace.documents[0].importedRecipe);

    try {
      const addColumns = '[data-testid="construction-action-add-columns"]';
      await page.until(() => page.evaluate(`Boolean(document.querySelector('${addColumns}:not(:disabled)'))`), 'Add columns is ready');
      await page.click(addColumns);
      await page.click('[role="tab"][data-column-tab="advanced"]');
      await page.clickText('Identifiers');
      await page.clickText('Review column rule');
      await page.clickText('Preview column rule');
      await page.until(() => page.evaluate(`[...document.querySelectorAll('button')].some(button => button.innerText === 'Apply column rule' && !button.disabled)`), 'identifier rule preview is ready');
      await page.until(async () => {
        const rows = await page.tableRows();
        return expected.every(value => rows.some(row => row.includes(value)));
      }, 'candidate table shows the fixture identifier values');
      assert.equal((await readDraft()).draftDigest, before.draftDigest, 'Preview must leave the saved draft unchanged');
      await page.clickText('Apply column rule');

      const saved = await page.until(async () => {
        const draft = await readDraft();
        return draft.draftDigest !== before.draftDigest ? draft : false;
      }, 'identifier rule is saved');
      const savedDocument = saved.workspace.documents[0];
      assert.deepEqual(savedDocument.importedRecipe, before.workspace.documents[0].importedRecipe);
      const [addition] = savedDocument.importedSource.ruleAdditions;
      assert.equal(addition.parameters.dynamic.name, 'identifiers');
      assert.equal(savedDocument.importedSource.steps.find(step => step.id === addition.stepId).editable, true);

      await page.reload();
      const rows = await page.until(async () => {
        const current = await page.tableRows();
        return expected.every(value => current.some(row => row.includes(value))) ? current : false;
      }, 'reloaded rows contain the fixture identifier values');
      for (const patient of patients) {
        assert(rows.some(row => row.includes(patient.id) && patient.identifier.every(item => row.includes(item.value))),
          `Identifiers stay paired with patient ${patient.id}`);
      }
      const editRule = 'button[aria-label^="Edit source operation"][aria-label$="identifiers"]';
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(editRule)}))`),
        'created rule is available in Recipe steps');
      await page.click(editRule);
      await page.until(() => page.evaluate('Boolean(document.querySelector(\'input[aria-label="Rule name"][value="identifiers"]\'))'),
        'saved rule opens the shared identifier form');
      await page.selectOptionMatching('fieldset[aria-label="Source step parameters"] select', 'Last part of the identifier system');
      await page.clickText('Preview source step');
      await page.until(() => page.evaluate(`Boolean(document.querySelector('[data-testid="imported-source-step-apply"]:not(:disabled)'))`),
        'edited rule preview is ready');
      await page.click('[data-testid="imported-source-step-apply"]');
      const edited = await page.until(async () => {
        const draft = await readDraft();
        const rule = draft.workspace.documents[0].importedSource.ruleAdditions[0];
        return rule.stepId === addition.stepId && rule.parameters.dynamic.key.call === 'last_segment' ? draft : false;
      }, 'edited rule keeps its saved step identity');
      const originalIds = new Set(before.workspace.documents[0].columns.map(column => column.columnId));
      const generated = edited.workspace.documents[0].columns.filter(column => !originalIds.has(column.columnId));
      assert(generated.length > 0, 'the edited rule generates columns');
      for (const enabled of [false, true]) {
        await page.reload();
        await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(editRule)}))`),
          'saved rule is available after reload');
        await page.click(editRule);
        await page.click('[data-testid="imported-source-rule-enabled"]');
        await page.clickText('Preview source step');
        await page.until(() => page.evaluate(`Boolean(document.querySelector('[data-testid="imported-source-step-apply"]:not(:disabled)'))`),
          'rule enablement preview is ready');
        await page.click('[data-testid="imported-source-step-apply"]');
        const updated = await page.until(async () => {
          const draft = await readDraft();
          const rule = draft.workspace.documents[0].importedSource.ruleAdditions[0];
          return rule.stepId === addition.stepId && rule.enabled === enabled ? draft : false;
        }, enabled ? 'rule is re-enabled' : 'rule is disabled');
        const names = new Set(updated.workspace.documents[0].columns.map(column => column.column));
        for (const column of generated) assert.equal(names.has(column.column), enabled, column.column);
      }
    } finally {
      const capability = await api(`${fixture.base}/capability`);
      if (capability.features?.deleteExplorer) {
        await api(`${scenario.root}/${encodeURIComponent(fixture.clone.explorerId)}`, { method: 'DELETE' });
      }
    }
  });
});

test('create an Identifier rule on a connected Observation occurrence', { timeout: 240000 }, async () => {
  const fixtureDirectory = resolve(import.meta.dirname, '../fixtures/builder-rule-related');
  const patients = await readFixture('Patient', fixtureDirectory);
  const observations = await readFixture('Observation', fixtureDirectory);
  const expectedByPatient = new Map(observations.map(observation => [
    observation.subject.reference.split('/').at(-1),
    observation.identifier.map(item => [item.system.split(':').at(-1), item.value]),
  ]));

  await withFreshUIExplorer('related identifier rule', async (scenario, page) => {
    const clone = await api(scenario.root, {
      method: 'POST',
      body: { name: `${scenario.explorerId}-imported`, sourceExplorerId: 'dataframe-default' },
    });
    const base = `${scenario.root}/${encodeURIComponent(clone.explorerId)}/authoring/v2`;
    const url = new URL(scenario.url);
    url.searchParams.set('explorer', clone.explorerId);
    await page.navigate(url.toString());
    return { base, clone };
  }, async ({ scenario, fixture, page }) => {
    const readDraft = () => api(`${fixture.base}/builder`);
    const waitForPairedRows = async () => {
      let headers;
      let rows;
      try {
        await page.until(async () => {
          headers = await page.evaluate(`[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"], thead th')]
            .map(header => header.innerText.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_'))`);
          rows = await page.tableRows();
          return patients.every(patient => rows.some(row => row.includes(patient.id) &&
            expectedByPatient.get(patient.id).every(([key, value]) =>
              row[headers.findIndex(header => header.includes('identifiers_urn_rule_observation_' + key))]?.includes(value))));
        }, 'Identifier values are paired with their generated columns and patient rows');
      } catch (error) {
        throw new Error(`${error.message}: ${JSON.stringify({ headers, rows })}`, { cause: error });
      }
    };
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
      assert.equal((await readDraft()).draftDigest, initial.draftDigest,
        'Graph Connect preview does not change the saved draft');
      await postCommands(fixture, [{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: source.output.id, proposalId: proposal.proposalId }]);
      const connected = await readDraft();
      const document = connected.workspace.documents[0];
      const observationOccurrence = document.route.children.find(child => child.resourceType === 'Observation');
      assert(observationOccurrence?.occurrenceId, 'Connected Observation has a stable occurrence ID');
      const occurrenceSelector = `button[data-occurrence-selector="true"][data-occurrence-id=${JSON.stringify(observationOccurrence.occurrenceId)}]`;
      await page.reload();
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(occurrenceSelector)}))`),
        'saved Observation occurrence is available after reload');
      await page.click(occurrenceSelector);
      await page.click('[data-testid="construction-action-add-columns"]');
      await page.click('[role="tab"][data-column-tab="advanced"]');
      await page.clickText('Identifiers');
      const observedSystems = [...new Set(observations.flatMap(observation => observation.identifier.map(item => item.system)))];
      await page.until(() => page.evaluate(`(() => {
        const text = document.querySelector('[aria-label="Observed identifier systems"]')?.innerText ?? '';
        return ${JSON.stringify(observedSystems)}.every(system => text.includes(system));
      })()`), 'resource-type Identifier systems are shown without search');
      await page.clickText('Review column rule');
      await page.clickText('Preview column rule');
      await page.until(() => page.evaluate(`Boolean([...document.querySelectorAll('button')].find(button => button.innerText === 'Apply column rule' && !button.disabled))`),
        'related Identifier rule preview is ready');
      await waitForPairedRows();
      assert.equal((await readDraft()).draftDigest, connected.draftDigest,
        'Identifier rule preview on a related occurrence leaves the draft unchanged');
      await page.clickText('Apply column rule');

      const saved = await page.until(async () => {
        const draft = await readDraft();
        const addition = draft.workspace.documents[0].importedSource.ruleAdditions?.[0];
        return addition?.occurrenceId === observationOccurrence.occurrenceId ? draft : false;
      }, 'related Identifier rule is saved to the selected occurrence');
      const savedDocument = saved.workspace.documents[0];
      const addition = savedDocument.importedSource.ruleAdditions[0];
      assert.equal(addition.parameters.dynamic.source.select, 'identifier[]');
      assert.equal(addition.kind, 'DYNAMIC_COLUMN');
      assert.equal(savedDocument.importedSource.steps.find(step => step.id === addition.stepId).parentId,
        `relationships.${observationOccurrence.occurrenceId}`);
      const previousColumnIds = new Set(document.columns.map(column => column.columnId));
      const generatedColumns = savedDocument.columns.filter(column => !previousColumnIds.has(column.columnId));
      assert(generatedColumns.length > 0, 'The related Identifier rule generates columns');
      const { network } = await api(`${fixture.base}/authoring-network`, {
        method: 'POST',
        body: {
          snapshotToken: saved.catalog.snapshotToken, expectedDraftVersion: saved.draftVersion,
          expectedDraftDigest: saved.draftDigest, outputId: savedDocument.output.id,
        },
      });
      for (const column of generatedColumns) {
        const binding = network.bindings.find(item => item.columnId === column.columnId);
        assert.equal(binding?.occurrenceId, observationOccurrence.occurrenceId);
        assert.equal(binding?.sourceStepId, addition.stepId);
      }


      await page.reload();
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(occurrenceSelector)}))`),
        'related rule occurrence reloads');
      await page.click(occurrenceSelector);
      await waitForPairedRows();
    } finally {
      const capability = await api(`${fixture.base}/capability`);
      if (capability.features?.deleteExplorer) {
        await api(`${scenario.root}/${encodeURIComponent(fixture.clone.explorerId)}`, { method: 'DELETE' });
      }
    }
  }, { fixtureDirectory });
});
