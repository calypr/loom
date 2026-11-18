import assert from 'node:assert/strict';
import test from 'node:test';
import { readFixture, createPatientTableInUI, withFreshUIExplorer, api } from './app.mjs';

const waitForProposal = async (page, expectedStatus) => {
  const result = await page.until(
    () => page.evaluate(`(() => {const panel=document.querySelector('[data-testid="construction-proposal-panel"]');if(!panel)return false;const error=document.querySelector('[data-testid="construction-proposal-error"]');if(error)return {error:error.innerText};return panel.dataset.proposalStatus===${JSON.stringify(expectedStatus)}?{ready:true}:false})()`),
    `construction proposal ${expectedStatus}`,
  );
  assert(result.ready, `Construction proposal failed: ${result.error}`);
};

const proposalRows = page => page.evaluate(
  `Array.from(document.querySelectorAll('[data-testid="construction-proposal-preview-row"]'),row=>Array.from(row.querySelectorAll('td'),cell=>cell.innerText.trim()))`,
);

test('related Observation values reject ambiguous ONE, accept ALL, and persist across reload', { timeout: 240000 }, async () => {
  const patients = await readFixture('Patient');
  const observations = await readFixture('Observation');
  const patientById = new Map(patients.map(patient => [patient.id, patient]));
  const relatedObservations = observations.flatMap(observation => {
    const patientId = observation.subject?.reference?.split('/').at(-1);
    const patient = patientId ? patientById.get(patientId) : undefined;
    return patient ? [{ patientId, observationId: observation.id, value: observation.valueQuantity?.value ?? null }] : [];
  });
  assert.deepEqual(relatedObservations.map(item => item.observationId).sort(), [
    'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
  ]);
  assert.equal(new Set(relatedObservations.filter(item => item.patientId === 'dev-patient-001').map(item => item.value)).size, 2,
    'The first Patient has two distinct related Observation values, so ONE must reject the Group row value.');

  await withFreshUIExplorer('related-field', (scenario, page) => createPatientTableInUI(scenario, page), async ({ scenario, page }) => {
    const beforeExpansion = await api(`${scenario.base}/builder`);

    const configureObservationExpansion = async () => {
      await page.openRowSettings();
      try {
        await page.click('[data-testid="construction-action-related-rows"]');
      } catch (error) {
        const status = await page.evaluate(`JSON.stringify({button:document.querySelector('[data-testid="construction-action-related-rows"]')?.outerHTML,text:document.body.innerText.slice(-1800)})`);
        throw new Error(`${error.message}; ${status}`);
      }
      const editor = '[data-testid="construction-related-expand-editor"]';
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(`${editor} select[aria-label="Related record type"]`)}))`), 'RelatedExpand editor');
      await page.selectOptionMatching(`${editor} select[aria-label="Related record type"]`, 'Observation');
      await page.until(() => page.evaluate(`Boolean([...document.querySelectorAll(${JSON.stringify(`${editor} input[type="radio"]`)} )].find(input => (input.getAttribute('aria-label') || '').includes('Patient <-[subject]- Observation')))`), 'Patient <- subject -> Observation route');
      const choice = await page.evaluate(`(() => {const root=document.querySelector(${JSON.stringify(editor)});const item=[...(root?.querySelectorAll('input[type="radio"]')??[])].find(input=>(input.getAttribute('aria-label')||'').includes('Patient <-[subject]- Observation'));if(!item)return null;item.setAttribute('data-e2e-route','selected');return '[data-e2e-route="selected"]'})()`);
      assert(choice, 'The UI exposes the exact Patient-to-Observation route');
      await page.click(choice);
      await waitForProposal(page, 'ready');
      const rows = await proposalRows(page);
      assert.equal(rows.length, relatedObservations.length, 'RelatedExpand preview contains each Observation with a Patient reference');
      for (const item of relatedObservations) assert(rows.some(row => row.includes(item.patientId) && row.includes(item.observationId)));
    };

    await configureObservationExpansion();
    await page.pressEscape();
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'RelatedExpand canceled');
    assert.equal((await api(`${scenario.base}/builder`)).draftDigest, beforeExpansion.draftDigest, 'Cancel keeps the original Patient table');

    await configureObservationExpansion();
    await page.click('[data-testid="construction-apply-proposal"]');
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'RelatedExpand applied');
    const expanded = await api(`${scenario.base}/builder`);
    assert.equal(expanded.workspace.documents[0].construction.steps.at(-1).operation.kind, 'RELATED_EXPAND');
    assert.equal(expanded.workspace.documents[0].construction.steps.at(-1).operation.relatedExpand.targetResourceType, 'Observation');

    await page.click('[data-testid="construction-action-add-columns"]');
    await page.click('[aria-label="Observation · current related record"]');
    await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=construction-related-field-editor]"))'), 'related Observation field editor');
    await page.fill('[data-testid="construction-related-field-editor"] input[placeholder="Search field name or path"]', 'valueQuantity.value');
    await page.until(() => page.evaluate(`Boolean([...document.querySelectorAll('[data-testid="construction-related-field-editor"] button')].find(button => button.innerText.includes('valueQuantity.value')))`), 'valueQuantity related field choice');
    await page.clickText('valueQuantity.value', '[data-testid="construction-related-field-editor"]');
    await waitForProposal(page, 'ready');
    const relatedFieldRows = await proposalRows(page);
    assert.equal(relatedFieldRows.length, relatedObservations.length);
    for (const item of relatedObservations) {
      assert(relatedFieldRows.some(row => row.includes(item.patientId) && row.includes(item.observationId) && row.includes(String(item.value))),
        `Related Observation.valueQuantity.value proposal includes ${item.observationId}`);
    }
    await page.click('[data-testid="construction-apply-proposal"]');
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'related Observation field applied');
    const withRelatedValue = await api(`${scenario.base}/builder`);
    const relatedFieldStep = withRelatedValue.workspace.documents[0].construction.steps.at(-1);
    assert.equal(relatedFieldStep.operation.kind, 'RELATED_FIELD');
    assert.equal(relatedFieldStep.operation.relatedField.source.resourceType, 'Observation');

    const createPatientGroup = async () => {
      await page.openRowSettings();
      await page.click('[data-testid="construction-action-group-rows"]');
      await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=construction-reshape-group]"))'), 'Group editor');
      await page.click('label:has(> input[aria-label="Group by Patient ID"])');
      await page.click('[data-testid="construction-group-tab-summaries"]');
      await page.select('[aria-label="Add summary"]', 'COUNT_ROWS');
      await waitForProposal(page, 'ready');
      await page.click('[data-testid="construction-apply-proposal"]');
      await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'Group applied');
      const grouped = await api(`${scenario.base}/builder`);
      const groupStep = grouped.workspace.documents[0].construction.steps.find(step => step.operation.kind === 'GROUP');
      assert(groupStep, 'Group by Patient ID is saved');
      return { grouped, groupStep };
    };

    const configurePatientGroupRowValue = async (groupStep, policy) => {
      await page.openRowSettings();
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(`[data-testid=construction-row-edit-${groupStep.id}]`)}))`), 'saved Group edit control');
      await page.click(`[data-testid="construction-row-edit-${groupStep.id}"]`);
      try {
        await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=construction-group-add-columns-browser]"))'), 'Group contributor editor');
      } catch (error) {
        const diagnostics = await page.evaluate(`({url:location.href,text:document.body.innerText.slice(-1600),buttons:Array.from(document.querySelectorAll('button'),button=>({label:button.getAttribute('aria-label'),text:button.innerText.trim(),testId:button.getAttribute('data-testid')})).filter(button=>button.label||button.text),panels:Array.from(document.querySelectorAll('[data-testid]'),node=>node.getAttribute('data-testid')).filter(value=>value?.includes('construction-row'))})`);
        throw new Error(`${error.message}; Group editor diagnostics=${JSON.stringify(diagnostics)}`);
      }
      const contributorLabels = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid="construction-group-add-columns-browser"] input[type="checkbox"]'),input=>input.getAttribute('aria-label'))`);
      assert(contributorLabels.includes('Add column valueQuantity.value'), `Group contributors expose the related Observation scalar; available=${JSON.stringify(contributorLabels)}`);
      await page.click('label:has(> input[aria-label="Add column valueQuantity.value"])');
      await page.select('[aria-label="Keep values 1"]', policy);
    };

    const { groupStep } = await createPatientGroup();
    const groupBeforePolicy = await api(`${scenario.base}/builder`);
    await configurePatientGroupRowValue(groupStep, 'ONE');
    await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=construction-proposal-error]"))'), 'ONE ambiguity error');
    const oneError = await page.text('[data-testid="construction-proposal-error"]');
    assert.match(oneError, /different values|one distinct value|multiple distinct values/i, 'ONE explains the related Observation ambiguity');
    await page.pressEscape();
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'ONE attempt canceled');
    assert.equal((await api(`${scenario.base}/builder`)).draftDigest, groupBeforePolicy.draftDigest, 'Cancel after ONE failure leaves the saved Group unchanged');

    await configurePatientGroupRowValue(groupStep, 'ALL');
    await waitForProposal(page, 'ready');
    const allRows = await proposalRows(page);
    assert.equal(allRows.length, 2, 'Group by Patient ID returns both fixture Patients');
    const firstPatient = allRows.find(row => row.includes('dev-patient-001'));
    const secondPatient = allRows.find(row => row.includes('dev-patient-002'));
    assert(firstPatient?.some(cell => cell.includes('172.5') && cell.includes('180')), 'ALL keeps both distinct related Observation values for Patient 001');
    assert(secondPatient?.some(cell => cell.includes('68')), 'ALL keeps the related Observation value for Patient 002');
    await page.pressEscape();
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'ALL preview canceled');
    assert.equal((await api(`${scenario.base}/builder`)).draftDigest, groupBeforePolicy.draftDigest, 'Cancel leaves the saved Group unchanged');

    await configurePatientGroupRowValue(groupStep, 'ALL');
    await waitForProposal(page, 'ready');
    await page.click('[data-testid="construction-apply-proposal"]');
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'ALL Group row values applied');
    const saved = await api(`${scenario.base}/builder`);
    const savedGroup = saved.workspace.documents[0].construction.steps.find(step => step.operation.kind === 'GROUP');
    assert(savedGroup.rowValues?.some(value => value.policy === 'ALL'), 'Apply persists the ALL related Observation row value');
    await page.reload();
    assert.equal((await api(`${scenario.base}/builder`)).draftDigest, saved.draftDigest, 'Reload persists the Group and related Observation values');
    const renderedRows = await page.until(async () => { const rows = await page.tableRows(); return rows.length ? rows : false; }, 'reloaded related Group rows');
    assert.equal(renderedRows.length, 2);
    assert(renderedRows.some(row => row.includes('dev-patient-001') && row.some(cell => cell.includes('172.5') && cell.includes('180'))));
    assert(renderedRows.some(row => row.includes('dev-patient-002') && row.some(cell => cell.includes('68'))));
  });
});
