import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
import { assertDefaultTraversalPreview, assertAuditedPivot, assertDefaultTraversalHistory, assertDefaultStepMetadata } from '../helpers/traversal-first-oracle.mjs';
const output = state => { const doc=state.workspace.documents.filter(doc=>doc.output.id==='ResearchSubject');assert.equal(doc.length,1);return doc[0]; };
const stepId='traversals[0].traversals[0].pivots[0]';
const occurrenceStepId='traversals[0].traversals[0]';
const sameSaved=(before,after)=>{assert.deepEqual(after.workspace,before.workspace);assert.equal(after.draftVersion,before.draftVersion);assert.equal(after.draftDigest,before.draftDigest);assert.equal(after.catalog.snapshotToken,before.catalog.snapshotToken)};

/** Real native controls, existing official Playwright page/request fixtures; no browser or persisted view model. */
export async function runTraversalFirstComparison({page,run,testInfo,composition,uiUrl}) {
  const capture=async(label,{candidate=false,inspector=false,controls=[]}={})=>{
    for(const [index,height]of [900,1100,900].entries()) {
      await page.setViewportSize({width:1536,height});
      await expect(page.locator('[data-composition]')).toHaveCount(1);
      await expect(page.locator('[data-composition]')).toHaveAttribute('data-composition',composition);
      const geometry=await page.evaluate(({candidate})=>{
        const rect=node=>{if(!node)return null;const r=node.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height,right:r.right,bottom:r.bottom}};
        const visible=node=>node.getClientRects().length>0&&getComputedStyle(node).visibility!=='hidden';
        const inspectors=[...document.querySelectorAll('[aria-label="Change editor"],[aria-label="Recipe fact inspector"]')].filter(visible);
        return{viewport:{width:innerWidth,height:innerHeight},scroll:{width:document.documentElement.scrollWidth,height:document.documentElement.scrollHeight},navigation:rect(document.querySelector('[aria-label="Recipe navigation"]')),traversal:rect(document.querySelector('[aria-label="Authored traversal"]')),preview:rect(document.querySelector('[data-testid="construction-preview"]')),table:rect(document.querySelector(candidate?'[data-testid="construction-preview"] [data-testid="construction-proposal-preview"]':'[data-testid="construction-preview"] [data-testid="preview-table-scroll"]')),inspectors:inspectors.map(rect)};
      },{candidate});
      const record={label,composition,height,index,verified:false,...geometry};run.report.comparison.push(record);
      const fits=r=>r&&r.x>=0&&r.y>=0&&r.right<=1537&&r.bottom<=height+1;
      assert(geometry.inspectors.length<=1,'One active editing context');
      if(inspector)assert.equal(geometry.inspectors.length,1,'Required contextual inspector');
      for(const r of [geometry.navigation,geometry.traversal,geometry.preview,...geometry.inspectors])assert(fits(r),'Workspace content must remain inside the viewport');
      assert(geometry.traversal.width>150&&geometry.traversal.height>120);
      assert(geometry.preview.width>300&&geometry.preview.height>=150);
      assert(geometry.table&&geometry.table.height>80,'Measure the actual saved/proposed table scroller');
      assert(geometry.scroll.width<=1537&&geometry.scroll.height<=height+1,'No page-level overflow');
      for(const control of controls){await expect(control).toBeVisible();await expect(control).toBeEnabled();const bounds=await control.boundingBox();assert(bounds&&bounds.y>=0&&bounds.y+bounds.height<=height+1,'Apply/Cancel must remain visible without page scrolling');await expect(control).toBeInViewport({ratio:1})}
      await page.screenshot({path:testInfo.outputPath(`${composition}-${label}-${height}-${index}.png`)});record.verified=true;
    }
  };
  const open=async target=>{
    await run.useTarget(target);
    const url=new URL(uiUrl);url.searchParams.set('project',target.project);url.searchParams.set('explorer',target.explorerId);
    await page.goto(url.href,{waitUntil:'domcontentloaded'});
    await page.getByTestId('construction-table-ResearchSubject').click();
    await expect(page.getByTestId('construction-preview')).toHaveAttribute('data-preview-status','ready');
    await expect(page.getByRole('navigation',{name:'Recipe presentation',exact:true})).toHaveCount(0);
    await expect(page.getByTestId('row-recipe-expression')).toHaveCount(0);
  };
  await open(run.target.baseline);
  const baseline=await run.state();const document=output(baseline);
  await run.action('All five default outputs,74 typed operations and32 authored field targets remain available',async()=>{
    for(const outputId of ['DocumentReference','ResearchSubject','MedicationAdministration','Specimen','GroupMember']) {
      await page.getByTestId(`construction-table-${outputId}`).click();
      const doc=baseline.workspace.documents.find(doc=>doc.output.id===outputId);assert(doc);
      assertDefaultTraversalPreview(doc,await run.preview());const expected=assertDefaultStepMetadata(doc);
      const nav=page.getByTestId('traversal-scope-navigator');const complete=nav.getByRole('list',{name:'Complete ordered source plan',exact:true,hidden:true});
      assert.deepEqual(await complete.locator(':scope > li').evaluateAll(nodes=>nodes.map(node=>node.dataset.operationId)),expected.map(x=>x.stepId));
      const buttons=nav.getByRole('navigation',{name:'Authored traversal scopes',exact:true}).locator('button[data-occurrence-id]');const count=await buttons.count();const seen=new Set(),fieldTargets=new Set();
      for(let i=0;i<count;i++) {
        await buttons.nth(i).click();const scoped=nav.getByRole('list',{name:'Source operations in selected scope',exact:true});
        for(const id of await scoped.locator(':scope > li').evaluateAll(nodes=>nodes.map(node=>node.dataset.operationId)))seen.add(id);
        for(const item of await scoped.locator('[data-source-field-id]').evaluateAll(nodes=>nodes.map(node=>({stepId:node.dataset.sourceStepId,fieldId:node.dataset.sourceFieldId}))))fieldTargets.add(`${item.stepId}/${item.fieldId}`);
      }
      assert.deepEqual([...seen].sort(),expected.map(x=>x.stepId).sort());assert.equal(fieldTargets.size,expected.reduce((n,x)=>n+(x.fieldNames?.length??0),0));

    }
    await page.getByTestId('construction-table-ResearchSubject').click();
  });
  await run.action('Initial traversal-first workspace at900/1100/back',()=>capture('initial'));
  const scopes=document.importedSource.steps.filter(step=>step.id===occurrenceStepId);assert.equal(scopes.length,1);assert(scopes[0].occurrenceId);
  const navigator=page.getByTestId('traversal-scope-navigator');
  await run.action('Keyboard selects the exact authored Observation scope',async()=>{
    const scope=navigator.getByRole('navigation',{name:'Authored traversal scopes',exact:true}).locator(`button[data-occurrence-id="${scopes[0].occurrenceId}"]`);
    await expect(scope).toHaveCount(1);await scope.focus();await scope.press('Enter');await expect(scope).toHaveAttribute('aria-pressed','true');
    sameSaved(baseline,await run.state());await capture('selection',{inspector:true});
  });
  const sourceStep=document.importedSource.steps.filter(step=>step.id===stepId);assert.equal(sourceStep.length,1);assert.equal(sourceStep[0].kind,'PIVOT');
  const operation=navigator.getByRole('list',{name:'Source operations in selected scope',exact:true}).locator(`[data-operation-id="${stepId}"]`).getByRole('button',{name:/^Edit source operation /});
  await run.action('Open the existing scoped Pivot and inspect its actual parameters',async()=>{
    await operation.focus();await operation.press('Enter');await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(1);
    await expect(page.getByRole('textbox',{name:'Pivot name',exact:true})).toHaveValue('observation_values');
    await capture('complex-editor',{inspector:true});
  });
  await run.action('Preview a meaningful existing Pivot edit with exact row values',async()=>{
    const editor=page.getByTestId('imported-source-step-editor');
    await editor.getByRole('checkbox',{name:'Discover pivot columns',exact:true}).uncheck();
    for(const [label,value]of [['Pivot column expression','observation.code.coding[].display'],['Pivot value expression','observation.valueInteger']]) {
      await editor.getByRole('checkbox',{name:`Use ${label}`,exact:true}).check();
      await editor.getByRole('combobox',{name:`${label} type`,exact:true}).selectOption('select');
      await editor.getByRole('textbox',{name:`${label} source path`,exact:true}).fill(value);
    }
    await editor.getByRole('button',{name:'Add Explicit pivot column',exact:true}).click();
    await editor.getByRole('textbox',{name:'Explicit pivot column 1',exact:true}).fill('Synthetic fixture score');
    await editor.getByRole('button',{name:'Add pivot alias',exact:true}).click();
    await editor.getByRole('textbox',{name:'Alias 1 source column',exact:true}).fill('Synthetic fixture score');
    await editor.getByRole('textbox',{name:'Alias 1 output name',exact:true}).fill('observation_values__audited_score');
    const review=page.getByRole('region',{name:'Proposed source step',exact:true});
    const responsePromise=page.waitForResponse(response=>response.url().endsWith('/source-relationship-proposals')&&response.status()===200&&response.request().postDataJSON()?.sourceStep?.stepId===stepId);
    await review.getByRole('button',{name:'Preview source step',exact:true}).click();
    const response=await responsePromise;const proposal=await response.json();const actualRequest=response.request().postDataJSON();
    assert.equal(actualRequest.expectedDraftVersion,baseline.draftVersion);assert.equal(actualRequest.expectedDraftDigest,baseline.draftDigest);assert.equal(actualRequest.snapshotToken,baseline.catalog.snapshotToken);
    const expectedParameters=structuredClone(sourceStep[0].parameters);delete expectedParameters.pivot.discovery;Object.assign(expectedParameters.pivot,{columnExpr:{select:'observation.code.coding[].display'},valueExpr:{select:'observation.valueInteger'},columns:['Synthetic fixture score'],columnAliases:{'Synthetic fixture score':'observation_values__audited_score'}});assert.deepEqual(proposal.candidateDocument.importedSource.steps.find(step=>step.id===stepId).parameters,expectedParameters,'The existing Pivot must reflect the exact supported selector/key/alias edit');
    assert.equal(proposal.outputId,document.output.id);assert.deepEqual(proposal.candidateDocument.importedRecipe,document.importedRecipe);
    assert.deepEqual(proposal.candidateDocument.route,document.route);assert.deepEqual(proposal.candidateDocument.rows,document.rows);
    assert.deepEqual(proposal.candidateDocument.importedSource.steps.filter(step=>step.id!==stepId),document.importedSource.steps.filter(step=>step.id!==stepId));
    assertAuditedPivot(proposal.candidateDocument,await run.preview({candidate:true}));
    run.report.sourceCandidate={request:actualRequest,candidateDocument:proposal.candidateDocument};
    const apply=review.getByTestId('imported-source-step-apply'),cancel=review.getByRole('button',{name:'Cancel',exact:true});
    await capture('proposal',{candidate:true,inspector:true,controls:[apply,cancel]});
    await cancel.click();await expect(editor).toHaveCount(0);await expect(operation).toBeFocused();sameSaved(baseline,await run.state());
    assertDefaultTraversalPreview(document,await run.preview());
  });
  await open(run.target.history);const history=await run.state();const historyDocument=output(history);
  await run.action('Same default source with100 ordered operations and400 exact calculated cells',async()=>{
    assertDefaultTraversalHistory(historyDocument,await run.preview());
    const list=navigator.getByRole('list',{name:'Ordered resulting-table operations',exact:true,hidden:true});
    await expect(list.locator('[data-operation-id]')).toHaveCount(100);
    await list.locator('xpath=parent::details').locator('summary').first().click();
    const item=list.locator('[data-operation-id="stress_calc_099"]').getByRole('button',{name:/^Edit table operation 100:/});
    await item.scrollIntoViewIfNeeded();await item.focus();await item.press('Enter');
    await expect(page.getByTestId('construction-calculate-editor')).toHaveCount(1);
    await page.getByRole('textbox',{name:'Output column label',exact:true}).fill('Reviewed default history');
    await expect(page.getByTestId('construction-proposal-panel')).toHaveAttribute('data-proposal-status','ready');
    assertDefaultTraversalHistory(historyDocument,await run.preview({candidate:true}));
    const apply=page.getByTestId('construction-apply-proposal'),cancel=page.getByTestId('construction-cancel-proposal');
    await capture('long-history',{candidate:true,inspector:true,controls:[apply,cancel]});
    await cancel.click();await expect(page.getByTestId('construction-calculate-editor')).toHaveCount(0);sameSaved(history,await run.state());
    assertDefaultTraversalHistory(historyDocument,await run.preview());
  });
  run.report.canonicalBaseline=baseline.workspace;run.report.canonicalHistory=history.workspace;
}

/** Existing saved v21 source descriptors must upgrade through their real explicit control, never fixture injection. */
export async function runTraversalLegacyFieldLifecycle({page,run,uiUrl}) {
  const target=run.target.legacy;await run.useTarget(target);
  const url=new URL(uiUrl);url.searchParams.set('project',target.project);url.searchParams.set('explorer',target.explorerId);
  await page.goto(url.href,{waitUntil:'domcontentloaded'});await page.getByTestId('construction-table-DocumentReference').click();
  const before=await run.state();const doc=state=>{const docs=state.workspace.documents.filter(d=>d.output.id==='DocumentReference');assert.equal(docs.length,1);return docs[0]};
  const pin=doc(before).importedRecipe;
  const assertTitle=async expected=>{
    const preview=await run.preview();const actual=doc(await run.state());
    const resolve=name=>{const cols=actual.columns.filter(c=>c.source?.imported?.outputColumn===name);assert.equal(cols.length,1);return cols[0].column};
    const id=resolve('id'),title=resolve('title'),status=resolve('status');assert.equal(preview.rows.length,1);
    assert.deepEqual(preview.rows.map(row=>({id:row[id],title:row[title],status:row[status]})),[{id:'document-one',title:expected,status:'current'}]);
    return preview;
  };
  await run.action('Existing legacy recipe loads exact original values before any upgrade',async()=>{
    await expect(page.getByRole('button',{name:'Load editable source settings',exact:true})).toHaveCount(1);
    await assertTitle('Fixture document');
  });
  await run.action('Explicitly load current editable descriptors without losing other outputs or the pin',async()=>{
    await page.getByRole('button',{name:'Load editable source settings',exact:true}).click();
    await expect(page.getByRole('button',{name:'Load editable source settings',exact:true})).toHaveCount(0);
    const upgraded=await run.state();assert(upgraded.draftVersion>before.draftVersion);assert.deepEqual(doc(upgraded).importedRecipe,pin);
    assert.deepEqual(upgraded.workspace.documents.filter(d=>d.output.id!=='DocumentReference'),before.workspace.documents.filter(d=>d.output.id!=='DocumentReference'));
    assert.deepEqual(doc(upgraded).route,doc(before).route);assert.deepEqual(doc(upgraded).rows,doc(before).rows);await assertTitle('Fixture document');
  });
  const upgraded=await run.state(),originalDocument=doc(upgraded);const groups=originalDocument.importedSource.steps.filter(step=>step.id==='fields');assert.equal(groups.length,1);
  const fields=groups[0].parameters?.fieldGroup?.fields;assert(Array.isArray(fields));const titles=fields.filter(field=>field.name==='title');assert.equal(titles.length,1);const titleField=titles[0],index=fields.findIndex(field=>field.fieldId===titleField.fieldId)+1;
  const open=async()=>{
    const navigator=page.getByTestId('traversal-scope-navigator');const root=navigator.getByRole('navigation',{name:'Authored traversal scopes',exact:true}).locator('button[data-occurrence-id="base"]');await root.click();
    const group=navigator.getByRole('list',{name:'Source operations in selected scope',exact:true}).locator('[data-operation-id="fields"]');
    const term=group.locator(`[data-source-step-id="fields"][data-source-field-id="${titleField.fieldId}"]`);await expect(term).toHaveCount(1);
    const details=term.locator('xpath=ancestor::details[1]');if(await details.getAttribute('open')===null)await details.locator('summary').first().click();
    const button=term.getByRole('button');await button.focus();await button.press('Enter');await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(1);return button;
  };
  const propose=async restore=>{
    const initiator=await open(),editor=page.getByTestId('imported-source-step-editor');
    await editor.getByRole('combobox',{name:`Field ${index} expression type`,exact:true}).selectOption(restore?'select':'literal');
    await editor.getByRole('textbox',{name:`Field ${index} expression ${restore?'source path':'literal value'}`,exact:true}).fill(restore?titleField.expr.select:'Legacy reviewed fixture document');
    const review=page.getByRole('region',{name:'Proposed source step',exact:true});await review.getByRole('button',{name:'Preview source step',exact:true}).click();
    await expect(review.getByTestId('imported-source-step-apply')).toBeEnabled();
    const proposed=await run.preview({candidate:true});const id=originalDocument.columns.find(c=>c.source?.imported?.outputColumn==='id').column;const title=originalDocument.columns.find(c=>c.source?.imported?.outputColumn==='title').column;
    assert.deepEqual(proposed.rows.map(row=>({id:row[id],title:row[title]})),[{id:'document-one',title:restore?'Fixture document':'Legacy reviewed fixture document'}]);return{review,initiator};
  };
  await run.action('Legacy exact field Preview and Cancel preserve the upgraded saved document',async()=>{
    const{review,initiator}=await propose(false);await review.getByRole('button',{name:'Cancel',exact:true}).click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);await expect(initiator).toBeFocused();sameSaved(upgraded,await run.state());await assertTitle('Fixture document');
  });
  for(const restore of [false,true])await run.action(restore?'Restore the original field, Apply and reload exact original values':'Apply the intended existing field and reload exact changed values',async()=>{
    const{review}=await propose(restore);await review.getByTestId('imported-source-step-apply').click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
    await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId('construction-table-DocumentReference').click();await assertTitle(restore?'Fixture document':'Legacy reviewed fixture document');
    const state=await run.state(),actual=doc(state);assert.deepEqual(actual.importedRecipe,pin);assert.deepEqual(actual.route,originalDocument.route);assert.deepEqual(actual.rows,originalDocument.rows);
    assert.deepEqual(state.workspace.documents.filter(d=>d.output.id!=='DocumentReference'),upgraded.workspace.documents.filter(d=>d.output.id!=='DocumentReference'));
    assert.deepEqual(actual.importedSource.steps.map(step=>step.id),originalDocument.importedSource.steps.map(step=>step.id));
    const group=actual.importedSource.steps.find(step=>step.id==='fields');assert.deepEqual(group.parameters.fieldGroup.fields.map(field=>field.fieldId),fields.map(field=>field.fieldId));
    assert.deepEqual(group.parameters.fieldGroup.fields.filter(field=>field.fieldId!==titleField.fieldId),fields.filter(field=>field.fieldId!==titleField.fieldId));assert.deepEqual(group.parameters.fieldGroup.fields.find(field=>field.fieldId===titleField.fieldId),restore?titleField:{...titleField,expr:{literal:'Legacy reviewed fixture document'}});
    assert.deepEqual(actual.importedSource.steps.filter(step=>step.id!=='fields'),originalDocument.importedSource.steps.filter(step=>step.id!=='fields'));
  });
}
