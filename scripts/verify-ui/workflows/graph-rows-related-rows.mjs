import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
import { validateGraphRowsFixture } from '../helpers/graph-rows-fixture-oracle.mjs';
const sorted = rows => structuredClone(rows).sort((a,b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
const documentFor = state => state.workspace.documents.find(document => document.output.id === 'ResearchSubject');
const identity = state => [state.draftVersion, state.draftDigest];
const routeName = route => route[0].fromResourceType + route.map(edge => {
  const field = [edge.fromResourceType, edge.toResourceType].reduce((label, resource) => label.endsWith(`_${resource}`) ? label.slice(0,-resource.length-1) : label, edge.relationship);
  return edge.storageDirection === 'INBOUND' ? ` <-[${field}]- ${edge.toResourceType}` : ` -[${field}]-> ${edge.toResourceType}`;
}).join('');

export async function runGraphRowsRelatedRows({run,page,existingList,onExpanded,stopAfterExpanded=false}) {
  const oracle = validateGraphRowsFixture();
  await run.open();
  const original = await run.state(), document = documentFor(original), originalRows = (await run.preview()).rows;
  const rootID = document.columns.find(column => column.source?.imported?.outputColumn === 'id').column;
  const status = document.columns.find(column => column.source?.imported?.outputColumn === 'status').column;
  const editor = page.getByTestId('construction-related-expand-editor');
  const proposal = page.getByTestId('construction-proposal-panel');
  let anchor;
  const open = async () => {
    const rowSettings = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    if (await rowSettings.isVisible()) await rowSettings.getByRole('button', { name: 'Back to table', exact: true }).click();
    await page.getByTestId('row-recipe-expression').getByRole('button', { name: 'Edit source rows: One row per ResearchSubject record', exact: true }).click();
    await page.getByTestId('construction-action-related-rows').click();
    await expect(editor).toBeVisible();
    const capabilities = [...run.capabilityResponses].reverse().find(response => response.outputId === 'ResearchSubject' && response.draftVersion === original.draftVersion);
    const anchors = capabilities?.selectedStage.relatedExpandAnchors?.filter(candidate => candidate.kind === 'root' && candidate.resourceType === 'ResearchSubject') ?? [];
    assert.equal(anchors.length,1,'The current stage must explicitly advertise one ResearchSubject root anchor');
    anchor = anchors[0];
    const selector = editor.getByRole('combobox',{name:'Start from',exact:true});
    if (await selector.count()) await selector.selectOption(anchor.anchorColumnId);
    await editor.getByRole('combobox',{name:'Related record type',exact:true}).selectOption('Observation');
    const choices = () => [...new Map(run.routeChoiceResponses.filter(response => response.outputId === 'ResearchSubject' && response.draftVersion === original.draftVersion)
      .flatMap(response => response.choices).filter(choice => choice.anchorColumnId === anchor.anchorColumnId && choice.targetResourceType === 'Observation' && choice.route.length === 2 &&
        choice.route[0].fromResourceType === 'ResearchSubject' && choice.route[0].toResourceType === 'Patient' && choice.route[0].storageDirection === 'OUTBOUND' && ['subject','subject_Patient'].includes(choice.route[0].relationship) &&
        choice.route[1].toResourceType === 'Observation' && choice.route[1].storageDirection === 'INBOUND' && ['subject','subject_Patient'].includes(choice.route[1].relationship)).map(choice=>[choice.choiceId,choice])).values()];
    await expect.poll(()=>choices().length,{message:'The known two-hop subject/Patient/Observation route must be unambiguous and supported'}).toBe(1);
    await editor.getByRole('radio',{name:routeName(choices()[0].route),exact:true}).check();
    await editor.getByRole('combobox',{name:'If a current row has no matches',exact:true}).selectOption('PRESERVE_PARENT');
    await editor.getByTestId('construction-related-expand-advanced').locator('summary').click();
    await editor.getByRole('textbox',{name:'Related record ID column',exact:true}).fill('expanded_observation_id');
    await editor.getByRole('textbox',{name:'Column label',exact:true}).fill('Expanded Observation ID');
    await expect(proposal).toHaveAttribute('data-proposal-status','ready');
    await expect(page.getByTestId('construction-preview').getByRole('columnheader',{name:/Expanded Observation ID/})).toBeVisible();
  };
  const assertPairs = async ({candidate=false,preserve=true}={}) => {
    const preview = await run.preview({candidate});
    const rows = preview.rows.map(row=>({identity:[row[rootID],row.expanded_observation_id],subjectStatus:row[status]}));
    const expected = [...oracle.expandedDropMissing,...(preserve?[oracle.expandedKeepMissingExtra]:[])].map(({identity,subjectStatus})=>({identity,subjectStatus}));
    assert.deepEqual(sorted(rows),sorted(expected),'Related rows must preserve complete input/related pairs and repeat the upstream scalar values');
    if (existingList) {
      const expectedLists = [...oracle.expandedListsAndExplicitScalars, ...(preserve ? [oracle.expandedListsKeepMissingExtra] : [])]
        .map(({ identity, observationValues }) => ({ identity, observationValues }));
      assert.deepEqual(sorted(preview.rows.map(row => ({ identity: [row[rootID], row.expanded_observation_id], observationValues: row[existingList.column.column] }))), sorted(expectedLists),
        'Existing ALL lists repeat unchanged on each input/related-record pair');
      const current = documentFor(await run.state());
      assert(current.columns.some(column => column.columnId === existingList.column.columnId));
      assert.equal(current.importedSource.steps.find(step => step.id === existingList.sourceStepId).valueMode, 'ALL');
    }
    return preview;
  };
  await open();
  await assertPairs({candidate:true});
  if (!stopAfterExpanded) {
  await run.action('Cancel related-row expansion without mutation',async()=>{
    await page.getByTestId('construction-cancel-proposal').click(); await expect(proposal).toHaveCount(0);
    assert.deepEqual(identity(await run.state()),identity(original)); assert.deepEqual(sorted((await run.preview()).rows),sorted(originalRows));
  });
  await open(); await assertPairs({candidate:true});
  }
  await run.action('Apply preserve-parent related rows',async()=>{await page.getByTestId('construction-apply-proposal').click();await expect(proposal).toHaveCount(0);await assertPairs();});
  let saved = await run.state();
  const step = documentFor(saved).construction.steps.find(step=>step.operation.kind==='RELATED_EXPAND');
  assert(step); assert.equal(step.operation.relatedExpand.anchorColumnId,anchor.anchorColumnId);assert.equal(step.inputs[0].kind,'SOURCE_PROJECTION');
  assert.equal(step.operation.relatedExpand.emptyPolicy,'PRESERVE_PARENT');assert.deepEqual(documentFor(saved).importedRecipe,document.importedRecipe);
  const assertVisibleExpression = async (preserve) => {
    await page.getByRole('navigation', { name: 'Recipe presentation', exact: true }).getByRole('button', { name: 'Both editors', exact: true }).click();
    const rowTerm = page.getByTestId('row-recipe-expression').locator(`[data-term-kind="operation"][data-operation-id="${step.id}"]`);
    await rowTerm.scrollIntoViewIfNeeded(); await expect(rowTerm).toBeVisible();
    await expect(rowTerm).toHaveAttribute('data-stage-id', step.id);
    await expect(rowTerm).toContainText('One row per input–Observation pair');
    await expect(rowTerm).toContainText('Input stage: Source');
    await expect(rowTerm).toContainText(`When no related record: ${preserve ? 'keep' : 'exclude'} the input row`);
    const graphTerm = page.getByTestId('authored-traversal-editor').locator(`[data-term-kind="operation"][data-operation-id="${step.id}"]`);
    await graphTerm.scrollIntoViewIfNeeded(); await expect(graphTerm).toBeVisible();
    await expect(graphTerm).toHaveAttribute('data-stage-id', step.id);
    await expect(graphTerm).toContainText('input/related-record pair');
    await expect(graphTerm).toContainText(`Input: Source`);
    await expect(graphTerm).toContainText(preserve ? 'keep parents without a match' : 'omit parents without a match');
  };
  await run.action('Reload exact shared related-record pairs',async()=>{await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId('construction-table-ResearchSubject').click();await assertPairs();await assertVisibleExpression(true);});
  if (onExpanded) {
    await onExpanded({ rootID, status, expansion: step });
    // The explicit scalar lifecycle legitimately advances the draft. Graph
    // inspection must preserve that current owner, not the earlier expansion's.
    saved = await run.state();
    assert.deepEqual(documentFor(saved).construction.steps.find(item => item.id === step.id), step, 'The scalar lifecycle must preserve the saved expansion');
    assert.deepEqual(documentFor(saved).importedRecipe, document.importedRecipe);
    if (existingList) assert.equal(documentFor(saved).importedSource.steps.find(item => item.id === existingList.sourceStepId).valueMode, 'ALL');
  }
  if (stopAfterExpanded) return;
  const patient = document.route.children.find(child=>child.resourceType==='Patient'); assert(patient);
  await page.getByTestId('authored-traversal-editor').getByRole('navigation', { name: 'Authored occurrences', exact: true }).locator(`button[data-occurrence-id="${patient.occurrenceId}"]`).click();
  assert.deepEqual(identity(await run.state()),identity(saved),'Graph selection is presentation state and cannot retarget or save the expansion');
  await page.getByTestId(`construction-history-step-${step.id}`).click();await expect(editor).toBeVisible();
  const selector=editor.getByRole('combobox',{name:'Start from',exact:true});if(await selector.count())await expect(selector).toHaveValue(anchor.anchorColumnId);
  await editor.getByRole('combobox',{name:'If a current row has no matches',exact:true}).selectOption('EXCLUDE');
  await expect(proposal).toHaveAttribute('data-proposal-status','ready');await assertPairs({candidate:true,preserve:false});
  await run.action('Apply explicit missing-match exclusion',async()=>{await page.getByTestId('construction-apply-proposal').click();await expect(proposal).toHaveCount(0);await assertPairs({preserve:false});});
  await run.action('Reload edited related-row policy and anchor',async()=>{await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId('construction-table-ResearchSubject').click();await assertPairs({preserve:false});await assertVisibleExpression(false);});
  saved=await run.state();const edited=documentFor(saved).construction.steps.find(item=>item.id===step.id);
  assert.equal(edited.operation.relatedExpand.anchorColumnId,step.operation.relatedExpand.anchorColumnId);assert.equal(edited.operation.relatedExpand.relatedRecordColumnId,step.operation.relatedExpand.relatedRecordColumnId);assert.equal(edited.operation.relatedExpand.emptyPolicy,'EXCLUDE');
  await page.getByTestId(`construction-history-step-${step.id}`).click();await page.getByTestId(`construction-remove-step-${step.id}`).click();await expect(proposal).toHaveAttribute('data-proposal-status','ready');
  await run.action('Apply related-row removal',async()=>{await page.getByTestId('construction-apply-proposal').click();await expect(proposal).toHaveCount(0);});
  await run.action('Reload restored source rows after expansion removal',async()=>{await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId('construction-table-ResearchSubject').click();assert.deepEqual(sorted((await run.preview()).rows),sorted(originalRows));});
  const restored=documentFor(await run.state());assert(!(restored.construction?.steps??[]).some(item=>item.id===step.id));assert.deepEqual(restored.importedRecipe,document.importedRecipe);assert.deepEqual(restored.columns,document.columns);
}
