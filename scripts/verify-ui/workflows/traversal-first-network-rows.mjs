import {patientGenderTraversal} from '../helpers/traversal-first-network-rows-oracle.mjs';
import {resolveGraphRowsGroupPreviewKeys} from '../helpers/graph-rows-complementary-oracle.mjs';
import {resolveGraphRowsObservationSource} from './graph-rows-mixed.mjs';
import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
import { validateGraphRowsFixture } from '../helpers/graph-rows-fixture-oracle.mjs';
export const sorted = rows => structuredClone(rows).sort((a,b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
const documentFor = state => state.workspace.documents.find(document => document.output.id === 'ResearchSubject');
const identity = state => [state.draftVersion, state.draftDigest];
const draftIdentity=identity;
const routeName = route => route[0].fromResourceType + route.map(edge => {
  const field = [edge.fromResourceType, edge.toResourceType].reduce((label, resource) => label.endsWith(`_${resource}`) ? label.slice(0,-resource.length-1) : label, edge.relationship);
  return edge.storageDirection === 'INBOUND' ? ` <-[${field}]- ${edge.toResourceType}` : ` -[${field}]-> ${edge.toResourceType}`;
}).join('');

export async function runTraversalRelatedRows({run,page,existingList,onExpanded,stopAfterExpanded=false}) {
  const oracle = validateGraphRowsFixture();
  await run.open();
  const original = await run.state(), document = documentFor(original), originalRows = (await run.preview()).rows;
  const rootID = document.columns.find(column => column.source?.imported?.outputColumn === 'id').column;
  const status = document.columns.find(column => column.source?.imported?.outputColumn === 'status').column;
  const editor = page.getByTestId('construction-related-expand-editor');
  const proposal = page.getByTestId('construction-proposal-panel');
  let anchor;
  const open = async () => {
    await selectScope(page,'base');
    await navigator(page).getByRole('button',{name:'Expand related records into rows',exact:true}).click();
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
    sameSaved(original,await run.state()); assert.deepEqual(sorted((await run.preview()).rows),sorted(originalRows));
  });
  await open(); await assertPairs({candidate:true});
  }
  await run.action('Apply preserve-parent related rows',async()=>{await page.getByTestId('construction-apply-proposal').click();await expect(proposal).toHaveCount(0);await assertPairs();});
  let saved = await run.state();
  const step = documentFor(saved).construction.steps.find(step=>step.operation.kind==='RELATED_EXPAND');
  assert(step); assert.equal(step.operation.relatedExpand.anchorColumnId,anchor.anchorColumnId);assert.equal(step.inputs[0].kind,'SOURCE_PROJECTION');
  assert.equal(step.operation.relatedExpand.emptyPolicy,'PRESERVE_PARENT');assert.deepEqual(documentFor(saved).importedRecipe,document.importedRecipe);
  const assertVisibleExpression=async(preserve)=>{const term=await operationTerm(page,step.id);await expect(term).toContainText('For each input row, one row per matching Observation');await expect(term).toContainText(preserve?'keep parents without a match':'omit parents without a match');await expect(term).toContainText(/Input:.*Source/);await expect(term).toHaveAttribute('data-operation-kind','construction');assert.equal(documentFor(await run.state()).construction.steps.find(x=>x.id===step.id).operation.relatedExpand.emptyPolicy,preserve?'PRESERVE_PARENT':'EXCLUDE');};
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
  await selectScope(page,patient.occurrenceId);
  assert.deepEqual(identity(await run.state()),identity(saved),'Graph selection is presentation state and cannot retarget or save the expansion');
  await editOperation(page,step.id);await expect(editor).toBeVisible();
  const selector=editor.getByRole('combobox',{name:'Start from',exact:true});if(await selector.count())await expect(selector).toHaveValue(anchor.anchorColumnId);
  await editor.getByRole('combobox',{name:'If a current row has no matches',exact:true}).selectOption('EXCLUDE');
  await expect(proposal).toHaveAttribute('data-proposal-status','ready');await assertPairs({candidate:true,preserve:false});
  await run.action('Apply explicit missing-match exclusion',async()=>{await page.getByTestId('construction-apply-proposal').click();await expect(proposal).toHaveCount(0);await assertPairs({preserve:false});});
  await run.action('Reload edited related-row policy and anchor',async()=>{await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId('construction-table-ResearchSubject').click();await assertPairs({preserve:false});await assertVisibleExpression(false);});
  saved=await run.state();const edited=documentFor(saved).construction.steps.find(item=>item.id===step.id);
  assert.equal(edited.operation.relatedExpand.anchorColumnId,step.operation.relatedExpand.anchorColumnId);assert.equal(edited.operation.relatedExpand.relatedRecordColumnId,step.operation.relatedExpand.relatedRecordColumnId);assert.equal(edited.operation.relatedExpand.emptyPolicy,'EXCLUDE');
  await editOperation(page,step.id);await page.getByTestId(`construction-remove-step-${step.id}`).click();await expect(proposal).toHaveAttribute('data-proposal-status','ready');
  await run.action('Apply related-row removal',async()=>{await page.getByTestId('construction-apply-proposal').click();await expect(proposal).toHaveCount(0);});
  await run.action('Reload restored source rows after expansion removal',async()=>{await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId('construction-table-ResearchSubject').click();assert.deepEqual(sorted((await run.preview()).rows),sorted(originalRows));});
  const restored=documentFor(await run.state());assert(!(restored.construction?.steps??[]).some(item=>item.id===step.id));assert.deepEqual(restored.importedRecipe,document.importedRecipe);assert.deepEqual(restored.columns,document.columns);assert.deepEqual(restored.construction?.steps??[],document.construction?.steps??[]);assert.deepEqual(restored.importedSource,document.importedSource);
}

const navigator=page=>page.getByTestId('traversal-scope-navigator');
export async function selectScope(page,id){const graph=page.getByTestId('authored-traversal-editor');const scope=graph.locator(`button[data-occurrence-id="${id}"]`);if(await scope.count())await scope.click();else await navigator(page).getByRole('navigation',{name:'Authored traversal scopes'}).locator(`button[data-occurrence-id="${id}"]`).click();}
export async function operationTerm(page,id){const nav=navigator(page);const sequence=nav.getByRole('list',{name:'Ordered resulting-table operations'});const disclosure=sequence.locator('xpath=ancestor::details[1]');if(await disclosure.getAttribute('open')===null)await disclosure.locator('summary').first().click();const term=sequence.locator(`[data-operation-id="${id}"]`);await expect(term).toHaveCount(1);await term.scrollIntoViewIfNeeded();return term;}
export async function editOperation(page,id){await(await operationTerm(page,id)).getByRole('button',{name:/^Edit table operation/}).click();}
const sameSaved=(a,b)=>{assert.deepEqual(b.workspace,a.workspace);assert.deepEqual(identity(b),identity(a));};
const reload=async(page)=>{await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId('construction-table-ResearchSubject').click();};
const unaffected=(original,current)=>assert.deepEqual(current.workspace.documents.filter(x=>x.output.id!=='ResearchSubject'),original.workspace.documents.filter(x=>x.output.id!=='ResearchSubject'));
export async function runTraversalNetwork({run,page}){
 await run.open();const original=await run.state(),doc=documentFor(original),rows=(await run.preview()).rows,oracle=validateGraphRowsFixture();
 const graph=page.getByTestId('authored-traversal-editor');
 const start=async()=>{await selectScope(page,'base');await graph.getByRole('button',{name:'Explore available connections',exact:true}).click();await page.getByRole('checkbox',{name:'Show full network',exact:true}).check();const node=page.locator('.react-flow__node').filter({hasText:'ResearchStudy'});await expect(node).toHaveCount(1);await node.focus();await node.press('Enter');await page.getByRole('button',{name:'Add traversal',exact:true}).click();};
 const review=page.getByRole('region',{name:'Proposed related records',exact:true});
 const preview=async()=>{await review.getByRole('button',{name:'Preview relationship',exact:true}).click();await expect(review.getByRole('button',{name:'Apply relationship',exact:true})).toBeEnabled();await expect(graph).toHaveAttribute('data-projection-kind','proposed');};
 await start();await preview();sameSaved(original,await run.state());await review.getByRole('button',{name:'Cancel',exact:true}).click();sameSaved(original,await run.state());assert.deepEqual(sorted((await run.preview()).rows),sorted(rows));
 await start();await preview();await review.getByRole('button',{name:'Apply relationship',exact:true}).click();await expect(review).toHaveCount(0);await reload(page);
 const saved=await run.state(),addition=documentFor(saved).importedSource.relationships.additions.filter(x=>x.resourceType==='ResearchStudy');assert.equal(addition.length,1);const branch=addition[0];assert.equal(branch.parentOccurrenceId,'base');assert.equal(branch.matchMode,'OPTIONAL');const field=branch.fields.filter(x=>['id','ResearchStudy.id'].includes(x.fieldPath));assert.equal(field.length,1);assert.equal(field[0].valueMode,'ALL');const col=documentFor(saved).columns.find(x=>x.columnId===field[0].columnId),root=doc.columns.find(x=>x.source?.imported?.outputColumn==='id');assert(col&&root);
 const values=async(required,candidate=false)=>{const p=await run.preview({candidate});assert.deepEqual(sorted(p.rows.map(x=>({identity:[x[root.column]],studyIds:x[col.column]}))),sorted(oracle.nativeStudy.filter(x=>!required||x.studyIds.length).map(({identity,studyIds})=>({identity,studyIds}))));};await values(false);
 await selectScope(page,branch.occurrenceId);await page.getByTestId('authored-occurrence-inspector').getByRole('combobox',{name:/Match policy for .*ResearchStudy.*OPTIONAL/}).selectOption('REQUIRED');await preview();await values(true,true);await review.getByRole('button',{name:'Apply relationship',exact:true}).click();await expect(review).toHaveCount(0);await reload(page);await values(true);
 await selectScope(page,branch.occurrenceId);await page.getByTestId('authored-occurrence-inspector').getByRole('button',{name:/^Remove .* relationship$/}).click();await preview();await review.getByRole('button',{name:'Apply relationship',exact:true}).click();await expect(review).toHaveCount(0);await reload(page);const restored=await run.state();assert.deepEqual(sorted((await run.preview()).rows),sorted(rows));assert.deepEqual(documentFor(restored).importedRecipe,doc.importedRecipe);assert.deepEqual(documentFor(restored).construction,doc.construction);assert.deepEqual(documentFor(restored).columns,doc.columns);assert.deepEqual(documentFor(restored).route,doc.route);assert.deepEqual(documentFor(restored).importedSource.steps.filter(x=>doc.importedSource.steps.some(y=>y.id===x.id)),doc.importedSource.steps);assert.deepEqual(documentFor(restored).importedSource.fieldBindings,doc.importedSource.fieldBindings);unaffected(original,restored);assert(!run.report.requests.flatMap(x=>x.commands??x.commandTypes??[]).includes('ADD_ROUTE'));
}

export async function runTraversalGroup({run,page,request}){await run.open();const restored=await run.state(),originalDocument=documentFor(restored),originalPreview=await run.preview();
    const status = documentFor(restored).columns.find(column => column.source?.imported?.outputColumn === 'status');
    assert(status, 'Group must bind the actual root status column');
    const genders = documentFor(restored).columns.filter(column => /gender/i.test(`${column.label} ${column.source?.imported?.outputColumn}`));
    assert.equal(genders.length, 1, 'The default fixture has one explicit Patient gender projection');
    const gender = genders[0];
    const tupleOracle = validateGraphRowsFixture().groupKeepMissing;
    const currentGroupKeyBindings = async preview => {
      const matches = () => run.constructionProposalResponses.filter(record => record.proposal.preview?.receiptId === preview.receiptId);
      await expect.poll(() => matches().length, { message: 'The visible Group preview must have one exact READY proposal response' }).toBe(1);
      const bindings = resolveGraphRowsGroupPreviewKeys({ ...matches()[0], preview, sourceColumns: [gender, status],
        identity: { snapshotToken: restored.catalog.snapshotToken, draftVersion: restored.draftVersion, draftDigest: restored.draftDigest, outputId: 'ResearchSubject' } });
      (run.report.groupPreviewKeyBindings ??= []).push({ receiptId: preview.receiptId, bindings });
      return bindings;
    };
    await selectScope(page,'base');await navigator(page).getByRole('button',{name:'Group resulting rows',exact:true}).click();
    const editor = page.getByTestId('construction-reshape-group');
    await expect(editor).toBeVisible();
    await editor.getByText('Add grouping field', { exact: true }).click();
    for (const key of [gender, status]) await editor.getByTestId('construction-group-by-field-list').locator(`input[type="checkbox"][value="${key.columnId}"]`).click();
    await expect(editor.getByTestId('construction-group-selected-keys').locator(`input[type="checkbox"][value="${status.columnId}"]`)).toBeChecked();
    await expect(editor.getByTestId('construction-group-selected-keys')).toContainText(status.label);
    await editor.getByText('Add grouping field', { exact: true }).click();
    await expect(editor.getByTestId('construction-group-selected-keys')).toContainText(status.label);
    const proposal = page.getByTestId('construction-proposal-panel');
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    const candidate = page.getByTestId('construction-preview');
    await expect(candidate).toHaveCount(1);
    await expect(candidate).toBeVisible();
    await expect(editor.getByTestId('construction-group-selected-keys')).toContainText(gender.label);
    const tuplePreview = await run.preview({ candidate: true });
    const tupleKeys = await currentGroupKeyBindings(tuplePreview);
    assert.deepEqual(sorted(tuplePreview.rows.map(row => tupleKeys.map(key => row[key.name]))), sorted(tupleOracle.map(row => row.identity)), 'Group preview must prove the complete literal key tuple, including the missing gender');
    await run.action('Cancel Group without saved mutation', async () => { await page.getByTestId('construction-cancel-proposal').click(); await expect(proposal).toHaveCount(0); sameSaved(restored,await run.state()); });
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows));

    await expect(page.getByRole('dialog', { name: 'Row definition settings', exact: true })).toHaveCount(0);

    // Reopen after the carry-in Cancel and complete the actual saved Group
    // lifecycle. Contributor selection is bound to the returned root occurrence
    // and stage capability, never to an arbitrary same-labelled graph node.
    await selectScope(page,'base');await navigator(page).getByRole('button',{name:'Group resulting rows',exact:true}).click();
    await expect(editor).toBeVisible();
    await editor.getByText('Add grouping field', { exact: true }).click();
    for (const key of [gender, status]) await editor.getByTestId('construction-group-by-field-list').locator(`input[type="checkbox"][value="${key.columnId}"]`).click();
    await expect(editor.getByTestId('construction-group-selected-keys').locator(`input[type="checkbox"][value="${status.columnId}"]`)).toBeChecked();
    await editor.getByText('Add grouping field', { exact: true }).click();
    await editor.getByRole('textbox', { name: 'Summary output name 1', exact: true }).fill('subject_count');
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    const initialGroupPreview = await run.preview({ candidate: true });
    const initialKeys = await currentGroupKeyBindings(initialGroupPreview);
    assert.deepEqual(sorted(initialGroupPreview.rows.map(row => [...initialKeys.map(key => row[key.name]), row.subject_count])), sorted(tupleOracle.map(row => [...row.identity, row.count])));
    await run.action('Apply initial Group before selecting contributor values', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
    });
    const initialGroupedState = await run.state();
    await run.action('Return to table after initial Group', async () => {
      const rowDialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
      await expect(rowDialog).toHaveCount(0);
      assert.deepEqual(draftIdentity(await run.state()), draftIdentity(initialGroupedState));
    });
    const initialGroup = documentFor(initialGroupedState).construction.steps.find(step => step.operation.kind === 'GROUP');
    assert(initialGroup);
    const statusKey = initialGroup.operation.group.keys.find(key => key.inputColumnId === status.columnId);
    assert(statusKey);
    const statusGroupOutput = initialGroup.outputs.find(column => column.id === statusKey.outputColumnId);
    const genderKey = initialGroup.operation.group.keys.find(key => key.inputColumnId === gender.columnId);
    assert(genderKey);
    const genderGroupOutput = initialGroup.outputs.find(column => column.id === genderKey.outputColumnId);
    assert(genderGroupOutput);
    assert(statusGroupOutput, 'Resolve the saved Group key by output identity rather than its source name');
    await editOperation(page,initialGroup.id);
    await expect(editor).toBeVisible();
    const rootIDColumns = originalDocument.columns.filter(column => column.source?.imported?.outputColumn === 'id');
    assert.equal(rootIDColumns.length, 1, 'Use the explicit pinned root ID binding, not a same-labelled related field');
    const rootID = rootIDColumns[0];
    assert.deepEqual(initialGroup.inputs, [{ kind: 'SOURCE_PROJECTION' }]);
    const matchingInputColumns = () => [...run.capabilityResponses].reverse().find(response =>
      response.outputId === 'ResearchSubject' && response.draftVersion === initialGroupedState.draftVersion &&
      response.selectedStage.id === 'source_projection')?.selectedStage.columns.filter(column => column.id === rootID.columnId) ?? [];
    await expect.poll(() => matchingInputColumns().length, { message: 'The exact current Group input must advertise the pinned root ID column.' }).toBe(1);
    assert.equal(matchingInputColumns()[0].type, 'string');
    run.report.contributorBinding = { sourceStageId: 'source_projection', inputColumnId: rootID.columnId, sourceOutputColumn: 'id', policy: 'ALL' };
    await editor.getByText('Add contributor value', { exact: true }).click();
    await editor.getByTestId(`construction-group-add-column-input-${rootID.columnId}`).click();
    await expect(editor.getByTestId(`construction-group-add-column-input-${rootID.columnId}`)).toHaveCount(0);
    await expect(editor.getByTestId('construction-group-row-value')).toHaveCount(1);
    await expect(editor.getByTestId('construction-group-retained-values')).toContainText(rootID.label);
    await editor.getByRole('combobox', { name: 'Keep values 1', exact: true }).selectOption('ALL');
    await editor.getByRole('textbox', { name: 'Added column name 1', exact: true }).fill('contributor_subject_ids');
    await editor.getByText('Add contributor value', { exact: true }).click();
    await expect(editor.getByTestId('construction-group-retained-values')).toContainText(rootID.label);
    const exactGroups = tupleOracle.map(row => ({ gender: row.identity[0], status: row.identity[1], count: row.count, contributorIDs: [...row.contributorSubjectIds].sort() }));
    const assertGroupRows = async ({ candidate = false, expected = exactGroups } = {}) => {
      const preview = await run.preview({ candidate });
      // ALL has no authored contributor order. Assert the complete multiset,
      // retaining duplicates; raw post-Apply/reload rows are compared separately.
      const normalized = preview.rows.map(row => {
        assert(Array.isArray(row.contributor_subject_ids), 'ALL contributor values must be a list');
        return { gender: row[genderGroupOutput.name], status: row[statusGroupOutput.name], count: row.subject_count, contributorIDs: [...row.contributor_subject_ids].sort() };
      });
      assert.deepEqual(sorted(normalized), sorted(expected));
      return preview;
    };
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await assertGroupRows({ candidate: true });
    await run.action('Apply Group with retained contributors', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
      await assertGroupRows();
    });
    const grouped = await run.state();
    const savedGroup = documentFor(grouped).construction.steps.find(step => step.operation.kind === 'GROUP');
    assert(savedGroup, 'Native Apply must save the Group operation');
    const rowGroupTerm=await operationTerm(page,savedGroup.id);await expect(rowGroupTerm).toContainText(gender.label);await expect(rowGroupTerm).toContainText(status.label);await expect(rowGroupTerm).toContainText(/Input:.*Source/i);

    assert.equal(savedGroup.id, initialGroup.id);
    assert.deepEqual(savedGroup.operation, initialGroup.operation, 'Adding contributor values edits the saved Group without retargeting its key/summary identities');
    assert.equal(savedGroup.operation.group.keys.length, 2);
    assert.deepEqual(savedGroup.operation.group.keys.map(key => key.inputColumnId), [gender.columnId, status.columnId]);
    assert.equal(savedGroup.operation.group.missingKeyPolicy, 'GROUP');
    assert.equal(savedGroup.operation.group.aggregates[0].operation, 'COUNT_ROWS');
    assert.equal(savedGroup.rowValues.length, 1);
    assert.equal(savedGroup.rowValues[0].policy, 'ALL');
    assert.equal(savedGroup.rowValues[0].inputColumnId, rootID.columnId);
    assert.equal(savedGroup.rowValues[0].fieldSource, undefined, 'Retained projected values use the exact stage input, without inventing a source choice');
    const contributorOutput = savedGroup.outputs.find(output => output.id === savedGroup.rowValues[0].outputColumnId);
    assert.equal(contributorOutput.name, 'contributor_subject_ids');
    assert.deepEqual(documentFor(grouped).importedRecipe, originalDocument.importedRecipe);
    const appliedGroupRows = (await assertGroupRows()).rows;
    await run.action('Reload Group with exact contributor values and policy', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId('construction-table-ResearchSubject').click();
      assert.deepEqual(sorted((await assertGroupRows()).rows), sorted(appliedGroupRows));
      assert.deepEqual(documentFor(await run.state()).construction.steps.find(step => step.id === savedGroup.id), savedGroup);
    });
    const dependencySaved=await run.state(),dependencyDoc=documentFor(dependencySaved);
    const networkResponse=await request.post(`${run.apiPath}/authoring-network`,{timeout:30000,data:{snapshotToken:dependencySaved.catalog.snapshotToken,expectedDraftVersion:dependencySaved.draftVersion,expectedDraftDigest:dependencySaved.draftDigest,outputId:'ResearchSubject'}});assert.equal(networkResponse.status(),200);const networkResult=await networkResponse.json();assert.equal(networkResult.snapshotToken,dependencySaved.catalog.snapshotToken);assert.equal(networkResult.draftVersion,dependencySaved.draftVersion);assert.equal(networkResult.draftDigest,dependencySaved.draftDigest);const patientStep=patientGenderTraversal(dependencyDoc,gender.columnId,networkResult.network);sameSaved(dependencySaved,await run.state());
    await selectScope(page,patientStep.occurrenceId);const sourceTerm=navigator(page).getByRole('list',{name:'Source operations in selected scope'}).locator(`[data-operation-id="${patientStep.id}"]`);await sourceTerm.getByRole('button',{name:/^Edit source operation/}).click();await page.getByRole('checkbox',{name:'Enable this source step',exact:true}).uncheck();const sourceReview=page.getByRole('region',{name:'Proposed source step',exact:true});const rejection=page.waitForResponse(r=>r.url().endsWith('/source-relationship-proposals'));await sourceReview.getByRole('button',{name:'Preview source step',exact:true}).click();const rejected=await rejection;assert.equal(rejected.status(),422);const error=await rejected.json();assert.equal(error.error?.code,'INVALID_IMPORTED_RELATIONSHIP');assert.match(error.error.message,/column|GROUP|group/i);await expect(sourceReview.getByTestId('imported-source-step-apply')).toBeDisabled();sameSaved(dependencySaved,await run.state());await assertGroupRows();run.report.expectedRejectedResponses=[{method:'POST',pathname:new URL(rejected.url()).pathname,status:422,errorCode:error.error.code}];await sourceReview.getByRole('button',{name:'Cancel',exact:true}).click();
    const policyBaseline = await run.state();
    const excludedGroups = validateGraphRowsFixture().groupExcludeMissing.map(row => ({ gender: row.identity[0], status: row.identity[1], count: row.count, contributorIDs: [...row.contributorSubjectIds].sort() }));
    const editMissingPolicy = async policy => {
      await rowGroupTerm.getByRole('button', { name: /^Edit table operation [0-9]+:/ }).click();
      await expect(editor).toBeVisible();
      const advanced = editor.getByTestId('construction-reshape-group-advanced');
      if (await advanced.getAttribute('open') === null) await advanced.locator('summary').first().click();
      await editor.getByRole('combobox', { name: 'Missing group key policy', exact: true }).selectOption(policy);
      await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    };
    const assertMissingPolicySaved = async policy => {
      const current = await run.state();
      const expectedDocument = structuredClone(documentFor(policyBaseline));
      expectedDocument.construction.steps.find(step => step.id === savedGroup.id).operation.group.missingKeyPolicy = policy;
      assert.deepEqual(documentFor(current), expectedDocument, 'Policy editing must preserve the same Group, key/output IDs, contributors, pin and operation order');
      assert.deepEqual(current.workspace.documents.filter(document => document.output.id !== 'ResearchSubject'), policyBaseline.workspace.documents.filter(document => document.output.id !== 'ResearchSubject'));
      await assertGroupRows({ expected: policy === 'EXCLUDE' ? excludedGroups : exactGroups });
    };
    await run.action('Preview missing-key exclusion through the saved navigator operation', async () => {
      await editMissingPolicy('EXCLUDE'); await assertGroupRows({ candidate: true, expected: excludedGroups });
    });
    await run.action('Cancel missing-key exclusion without changing the saved Group', async () => {
      await page.getByTestId('construction-cancel-proposal').click(); await expect(proposal).toHaveCount(0);
      const canceled = await run.state(); assert.deepEqual(draftIdentity(canceled), draftIdentity(policyBaseline));
      assert.deepEqual(canceled.workspace, policyBaseline.workspace); await assertMissingPolicySaved('GROUP');
    });
    await run.action('Apply EXCLUDE to the existing Group', async () => {
      await editMissingPolicy('EXCLUDE'); await assertGroupRows({ candidate: true, expected: excludedGroups });
      await page.getByTestId('construction-apply-proposal').click(); await expect(proposal).toHaveCount(0); await assertMissingPolicySaved('EXCLUDE');
    });
    await run.action('Reload excluded Group keys and contributors', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); await assertMissingPolicySaved('EXCLUDE');
    });
    await run.action('Restore GROUP through the same navigator operation', async () => {
      await editMissingPolicy('GROUP'); await assertGroupRows({ candidate: true });
      await page.getByTestId('construction-apply-proposal').click(); await expect(proposal).toHaveCount(0); await assertMissingPolicySaved('GROUP');
    });
    await run.action('Reload restored missing Group and contributors', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); await assertMissingPolicySaved('GROUP');
    });
    await editOperation(page,savedGroup.id);
    await expect(editor).toBeVisible();
    await expect(editor.getByRole('combobox', { name: 'Keep values 1', exact: true })).toHaveValue('ALL');
    await editor.getByTestId('construction-reshape-group-advanced').locator('summary').first().click();
    await editor.getByRole('textbox', { name: 'Summary output label 1', exact: true }).fill('Subject count');
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await assertGroupRows({ candidate: true });
    await run.action('Apply saved Group edit', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
      await assertGroupRows();
    });
    await run.action('Reload edited Group bindings', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId('construction-table-ResearchSubject').click();
      await assertGroupRows();
    });
    const editedGroup = documentFor(await run.state()).construction.steps.find(step => step.id === savedGroup.id);
    assert.deepEqual(editedGroup.operation, savedGroup.operation, 'Editing a label preserves Group key and summary identities');
    assert.deepEqual(editedGroup.rowValues, savedGroup.rowValues, 'Editing retains the exact contributor binding and ALL policy');
    assert.equal(editedGroup.outputs.find(output => output.id === editedGroup.operation.group.aggregates[0].outputColumnId).label, 'Subject count');
    await editOperation(page,savedGroup.id);
    await page.getByTestId(`construction-remove-step-${savedGroup.id}`).click();
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await run.action('Apply Group removal', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
    });
    await run.action('Reload Group removal and restore exact source values', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId('construction-table-ResearchSubject').click();
      assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows));
    });
    const ungrouped = documentFor(await run.state());
    assert(!(ungrouped.construction?.steps ?? []).some(step => step.id === savedGroup.id));
    assert.deepEqual(ungrouped.importedRecipe, originalDocument.importedRecipe);
    assert.deepEqual(ungrouped.columns, originalDocument.columns);assert.deepEqual(ungrouped.construction?.steps??[],originalDocument.construction?.steps??[]);assert.deepEqual(ungrouped.importedSource,originalDocument.importedSource);
unaffected(restored,await run.state());}

export async function runTraversalRelatedWithList({run,page,request}){
 await run.open();const before=await run.state(),doc=documentFor(before);const response=await request.post(`${run.apiPath}/authoring-network`,{timeout:30000,data:{snapshotToken:before.catalog.snapshotToken,expectedDraftVersion:before.draftVersion,expectedDraftDigest:before.draftDigest,outputId:'ResearchSubject'}});assert.equal(response.status(),200);const {network}=await response.json();const {occurrence,step,column}=resolveGraphRowsObservationSource(doc,network);
 await selectScope(page,occurrence.occurrenceId);const term=navigator(page).getByRole('list',{name:'Source operations in selected scope'}).locator(`[data-operation-id="${step.id}"]`);await expect(term).toHaveCount(1);await term.getByRole('button',{name:/^Edit source operation/}).click();await page.getByTestId('imported-source-value-mode').selectOption('ALL');const review=page.getByRole('region',{name:'Proposed source step',exact:true});await review.getByRole('button',{name:'Preview source step',exact:true}).click();await expect(review.getByTestId('imported-source-step-apply')).toBeEnabled();const root=doc.columns.find(x=>x.source?.imported?.outputColumn==='id');const candidate=await run.preview({candidate:true});assert.deepEqual(sorted(candidate.rows.map(x=>({identity:[x[root.column]],observationValues:x[column.column]}))),sorted(validateGraphRowsFixture().preserving.map(({identity,observationValues})=>({identity,observationValues}))));await review.getByTestId('imported-source-step-apply').click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);await reload(page);assert.equal(documentFor(await run.state()).importedSource.steps.find(x=>x.id===step.id).valueMode,'ALL');await runTraversalRelatedRows({run,page,existingList:{column,sourceStepId:step.id},onExpanded:makeTraversalScalar({run,page,step,valueColumn:column})});const final=await run.state();unaffected(before,final);assert.deepEqual(documentFor(final).importedRecipe,doc.importedRecipe);
}

function makeTraversalScalar({run,page,step,valueColumn}){const oracle=validateGraphRowsFixture();
  const explicitScalar = async ({ rootID, status, expansion }) => {
    const beforeScalar = await run.state();
    await selectScope(page,'base');await navigator(page).getByRole('button',{name:'Collect related values',exact:true}).click();
    const fieldsTab = page.getByRole('tab', { name: 'Other fields', exact: true });
    if (await fieldsTab.count()) await fieldsTab.click();
    const exact = page.locator('[data-testid="construction-add-columns-source-option"][data-source-kind="EXACT_RELATED"]');
    await expect(exact).toHaveCount(1);
    await expect(exact).toContainText('Observation');
    await exact.click();
    const fieldEditor = page.getByTestId('construction-related-field-editor');
    await expect(fieldEditor).toBeVisible();
    await fieldEditor.getByRole('textbox', { name: 'Find a field', exact: true }).fill('valueInteger');
    const field = fieldEditor.getByRole('group', { name: 'Fields from this related record', exact: true }).getByRole('button').filter({ hasText: /(?:Observation\.)?valueInteger · integer/ });
    await expect(field).toHaveCount(1);
    await field.click();
    await fieldEditor.getByRole('textbox', { name: 'Column name', exact: true }).fill('per_match_observation_value');
    await fieldEditor.getByRole('textbox', { name: 'Column label', exact: true }).fill('Per-match Observation value');
    const proposal = page.getByTestId('construction-proposal-panel');
    const assertScalar = async (candidate = false) => {
      const preview = await run.preview({ candidate });
      const expected = [...oracle.expandedListsAndExplicitScalars, oracle.expandedListsKeepMissingExtra]
        .map(({ identity, subjectStatus, observationValues, perMatchObservationValue }) => ({ identity, subjectStatus, observationValues, perMatchObservationValue }));
      assert.deepEqual(sorted(preview.rows.map(row => ({ identity: [row[rootID], row.expanded_observation_id], subjectStatus: row[status],
        observationValues: row[valueColumn.column], perMatchObservationValue: row.per_match_observation_value }))), sorted(expected),
      'Explicit scalar selection must retain the original list and give each input/related-record pair its own scalar');
    };
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await assertScalar(true);
    await run.action('Traversal: Apply a separately selected per-match scalar', async () => {
      await page.getByTestId('construction-apply-proposal').click(); await expect(proposal).toHaveCount(0); await assertScalar();
    });
    const scalarState = await run.state();
    const added = documentFor(scalarState).construction.steps.filter(item => !documentFor(beforeScalar).construction.steps.some(before => before.id === item.id));
    assert.equal(added.length, 1); const scalar = added[0];
    assert.equal(scalar.operation.kind, 'RELATED_FIELD');
    assert.equal(scalar.operation.relatedField.source.resourceType, 'Observation');
    assert(/(?:^|\.)valueInteger$/.test(scalar.operation.relatedField.source.path));
    assert.deepEqual(scalar.inputs, [{ kind: 'STEP_OUTPUT', stepId: expansion.id }]);
    assert.notEqual(scalar.operation.relatedField.outputColumnId, valueColumn.columnId);
    const scalarOutput = scalar.outputs.find(output => output.id === scalar.operation.relatedField.outputColumnId);
    assert.equal(scalarOutput.name, 'per_match_observation_value'); assert.equal(scalarOutput.type, 'integer');
    assert.equal(documentFor(scalarState).importedSource.steps.find(item => item.id === step.id).valueMode, 'ALL');
    await run.action('Traversal: reload the distinct list and per-match scalar columns', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click(); await assertScalar();
      assert.deepEqual(documentFor(await run.state()).construction.steps.find(item => item.id === scalar.id), scalar);
    });
    await editOperation(page,scalar.id);
    await page.getByTestId(`construction-remove-step-${scalar.id}`).click();
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await run.action('Traversal: remove only the explicit scalar before editing the earlier expansion', async () => {
      await page.getByTestId('construction-apply-proposal').click(); await expect(proposal).toHaveCount(0);
      const state = documentFor(await run.state());
      assert(!state.construction.steps.some(item => item.id === scalar.id));
      assert.deepEqual(state.construction.steps.find(item => item.id === expansion.id), expansion);
      assert.equal(state.importedSource.steps.find(item => item.id === step.id).valueMode, 'ALL');
    });
  };
return explicitScalar;
}
