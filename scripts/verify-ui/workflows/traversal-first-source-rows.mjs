import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
import { assertDefaultTraversalPreview, assertDefaultStepMetadata } from '../helpers/traversal-first-oracle.mjs';
import { sourceRowIdentityVectors, sourceStep, sourceField, assertTraversalAliasValues, assertGroupMemberSourceValues, assertStableSourceStructure, sourceSubtreeStepIds } from '../helpers/traversal-first-source-rows-oracle.mjs';
const documentFor=(state,id)=>{const documents=state.workspace.documents.filter(document=>document.output.id===id);assert.equal(documents.length,1);return documents[0]};
const sameSaved=(before,after)=>{assert.equal(after.draftVersion,before.draftVersion);assert.equal(after.draftDigest,before.draftDigest);assert.deepEqual(after.workspace,before.workspace);assert.equal(after.catalog.snapshotToken,before.catalog.snapshotToken)};
const expectedSteps={TRAVERSAL:['traversals[0].traversals[0]','traversals[0].traversals[0].fields'],EXPANSION:['expand','identity','fields'],IDENTITY:['identity']};
const argumentLabel=(call,index)=>call==='uuid5'&&index===0?'Namespace':call==='join'&&index===1?'Separator':`Argument ${index+1}`;

/** Drive the real structured controls; never inject recipe state or an API mutation. */
export async function setSourceRecipeExpression(scope,label,expression) {
  const kind=Object.hasOwn(expression,'select')?'select':Object.hasOwn(expression,'call')?'call':'literal';
  await scope.getByRole('combobox',{name:`${label} type`,exact:true}).selectOption(kind);
  if(kind==='select'){await scope.getByRole('textbox',{name:`${label} source path`,exact:true}).fill(expression.select);return}
  if(kind==='literal'){
    const type=expression.literal===null?'null':typeof expression.literal;
    assert(['null','string','number','boolean'].includes(type));
    await scope.getByRole('combobox',{name:`${label} literal type`,exact:true}).selectOption(type);
    if(type==='boolean')await scope.getByRole('checkbox',{name:`${label} boolean`,exact:true}).setChecked(expression.literal);
    else if(type!=='null')await scope.getByRole(type==='number'?'spinbutton':'textbox',{name:`${label} literal value`,exact:true}).fill(String(expression.literal));
    return;
  }
  await scope.getByRole('combobox',{name:`${label} function`,exact:true}).selectOption(expression.call);
  const argumentsList=scope.getByRole('list',{name:`${label} ordered arguments`,exact:true});
  const wanted=expression.args??[];
  while(await argumentsList.locator(':scope > li').count()>wanted.length){const count=await argumentsList.locator(':scope > li').count();await scope.getByRole('button',{name:`${label} remove argument ${count}`,exact:true}).click();await expect(argumentsList.locator(':scope > li')).toHaveCount(count-1)}
  while(await argumentsList.locator(':scope > li').count()<wanted.length){const count=await argumentsList.locator(':scope > li').count();await scope.getByRole('button',{name:`Add argument to ${label}`,exact:true}).click();await expect(argumentsList.locator(':scope > li')).toHaveCount(count+1)}
  for(const[index,argument]of wanted.entries())await setSourceRecipeExpression(scope,`${label} · ${argumentLabel(expression.call,index)}`,argument);
}
async function openSourceStep(page,stepId,document) {
  const navigator=page.getByTestId('traversal-scope-navigator');
  let scoped=sourceStep(document,stepId);
  const visited=new Set();
  while(scoped.kind!=='TRAVERSAL'&&scoped.parentId){assert(!visited.has(scoped.id));visited.add(scoped.id);scoped=sourceStep(document,scoped.parentId);}
  const occurrenceId=scoped.kind==='TRAVERSAL'?scoped.occurrenceId:document.route.occurrenceId;
  assert(occurrenceId,'Source operation must retain an exact authored traversal scope');
  const scope=navigator.getByRole('navigation',{name:'Authored traversal scopes',exact:true}).locator(`button[data-occurrence-id=${JSON.stringify(occurrenceId)}]`);
  await expect(scope).toHaveCount(1);await scope.click();
  const operations=navigator.getByRole('list',{name:'Source operations in selected scope',exact:true});
  const button=operations.locator(`:scope > li[data-operation-id=${JSON.stringify(stepId)}]`).getByRole('button',{name:/^Edit source operation /});
  await expect(button).toBeEnabled();await button.focus();await button.press('Enter');
  await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(1);
  return page.getByTestId('imported-source-step-editor');
}
async function stageDependent(editor,stepId) {
  await editor.getByRole('combobox',{name:'Also edit a dependent operation',exact:true}).selectOption(stepId);
  const operation=editor.locator(`li[data-source-step-id=${JSON.stringify(stepId)}]`);
  await expect(operation).toHaveCount(1);return operation;
}
async function editFieldExpression(operation,descriptor,fieldName,expression) {
  const{field,index}=sourceField(descriptor.document,descriptor.stepId,fieldName);
  const item=operation.locator(`[data-field-id=${JSON.stringify(field.fieldId)}]`);
  const details=item.locator('details').first();
  if(await details.getAttribute('open')===null)await details.locator('summary').first().click();
  await setSourceRecipeExpression(item,`Field ${index+1} expression`,expression);
}
function memberIdentity(alias) {
  return{call:'uuid5',args:[{literal:sourceRowIdentityVectors.originalNamespace},{select:'root.id'},{call:'concat',args:[{literal:','},{call:'reference_id',args:[{select:`${alias}.entity.reference`}]}]}]};
}
async function configure(page,kind,original,{restore=false}={}) {
  const ids=expectedSteps[kind],editor=await openSourceStep(page,ids[0],original);
  if(kind==='TRAVERSAL') {
    const alias=restore?sourceStep(original,ids[0],'TRAVERSAL').parameters.traversal.alias:'audited_observation';
    await editor.getByRole('textbox',{name:'Related record alias',exact:true}).fill(alias);
    const dependent=await stageDependent(editor,ids[1]);
    const{field}=sourceField(original,ids[1],'observation_id');
    const item=dependent.locator(`[data-field-id=${JSON.stringify(field.fieldId)}] > details`);
    if(await item.getAttribute('open')===null)await item.locator('summary').first().click();
    if(!restore)await expect(dependent.getByRole('textbox',{name:'Field 1 expression source path',exact:true})).toHaveValue(field.expr.select);
    await editFieldExpression(dependent,{document:original,stepId:ids[1]},'observation_id',restore?field.expr:{select:'audited_observation.id'});
  }else if(kind==='EXPANSION') {
    const saved=sourceStep(original,'expand','EXPANSION').parameters.expansion;
    await editor.getByRole('textbox',{name:'Expanded item alias',exact:true}).fill(restore?saved.as:'participant');
    await editor.getByRole('combobox',{name:'Empty collection policy',exact:true}).selectOption(restore?saved.emptyPolicy??'':'PRESERVE_PARENT');
    const identity=await stageDependent(editor,'identity');
    await setSourceRecipeExpression(identity,'Row identity expression',restore?sourceStep(original,'identity','IDENTITY').parameters.identity.expr:memberIdentity('participant'));
    const fields=await stageDependent(editor,'fields');const{field,index}=sourceField(original,'fields','member_id');
    const details=fields.locator(`[data-field-id=${JSON.stringify(field.fieldId)}] > details`);
    if(await details.getAttribute('open')===null)await details.locator('summary').first().click();
    if(!restore)await expect(fields.getByRole('textbox',{name:`Field ${index+1} expression · Argument 1 source path`,exact:true})).toHaveValue('member.entity.reference');
    await editFieldExpression(fields,{document:original,stepId:'fields'},'member_id',restore?field.expr:{call:'reference_id',args:[{select:'participant.entity.reference'}]});
  }else{
    const identity=sourceStep(original,'identity','IDENTITY').parameters.identity;
    assert.equal(identity.expr.call,'uuid5');assert.equal(identity.expr.args[0].literal,sourceRowIdentityVectors.originalNamespace);
    await editor.getByRole('textbox',{name:'Row identity expression · Namespace literal value',exact:true}).fill(restore?sourceRowIdentityVectors.originalNamespace:sourceRowIdentityVectors.changedNamespace);
  }
  return editor;
}
async function proposalResponse(page,review,{outputId,stepIds,valid=true}) {
  const responsePromise=page.waitForResponse(response=>{
    if(!response.url().endsWith('/source-relationship-proposals')||response.request().method()!=='POST')return false;
    const body=response.request().postDataJSON();return body.outputId===outputId&&JSON.stringify((body.sourceSteps??[body.sourceStep]).map(step=>step?.stepId))===JSON.stringify(stepIds);
  });
  await review.getByRole('button',{name:'Preview source step',exact:true}).click();
  const response=await responsePromise;assert.equal(response.ok(),valid);
  return{response,body:await response.json(),request:response.request().postDataJSON()};
}
async function captureProposal(page,review,testInfo,kind,record) {
  for(const[sequence,height]of [900,1100,900].entries()){
    await page.setViewportSize({width:1536,height});
    const preview=page.getByTestId('construction-preview');const bounds=await preview.boundingBox();
    const traversal=await page.locator('[aria-label="Authored traversal"]').boundingBox();assert(traversal&&traversal.width>150&&traversal.height>120&&traversal.y>=0&&traversal.y+traversal.height<=height+1,'Relevant authored traversal remains legible beside the source inspector');
    assert(bounds&&bounds.height>=150&&bounds.width>300&&bounds.y>=0&&bounds.y+bounds.height<=height+1,'Useful receipt-bound result remains visible');
    const table=await preview.getByTestId('construction-proposal-preview').boundingBox();assert(table&&table.height>80,'Measure the actual proposed table scroller, not its container');
    const geometry=await page.evaluate(()=>{const visible=node=>node.getClientRects().length>0&&getComputedStyle(node).visibility!=='hidden';return{width:document.documentElement.scrollWidth,height:document.documentElement.scrollHeight,inspectors:[...document.querySelectorAll('[aria-label="Change editor"],[aria-label="Recipe fact inspector"]')].filter(visible).map(node=>{const r=node.getBoundingClientRect();return{x:r.x,y:r.y,width:r.width,height:r.height,right:r.right,bottom:r.bottom}})}});
    assert.equal(geometry.inspectors.length,1);assert(geometry.width<=1537&&geometry.height<=height+1,'No page-level overflow');
    for(const box of [...geometry.inspectors,bounds,traversal,table])assert(box.x>=0&&box.y>=0&&box.x+box.width<=1537&&box.y+box.height<=height+1,'Measured source editor and result remain inside the viewport');
    const controls=[];
    for(const control of [review.getByTestId('imported-source-step-apply'),review.getByRole('button',{name:'Cancel',exact:true})]){await expect(control).toBeEnabled();await expect(control).toBeInViewport({ratio:1});controls.push({label:await control.textContent(),bounds:await control.boundingBox()})}
    await page.screenshot({path:testInfo.outputPath(`${kind}-proposed-${height}-${sequence}.png`)});
    record.layout??=[];record.layout.push({viewportHeight:height,sequence,preview:bounds,traversal,table,controls,...geometry,verified:true});
  }
}
function expectTypedEdit(kind,request,original,{restore=false,candidate}={}) {
  assert.equal(request.outputId,original.output.id);assert.equal(request.change,undefined);
  const ids=expectedSteps[kind], changes=request.sourceSteps??[request.sourceStep];
  assert.deepEqual(changes.map(step=>step.stepId),ids);
  if(kind!=='IDENTITY')assert.equal(request.sourceStep,undefined,'Coupled changes require one batch proposal');
  const expected=structuredClone(original.importedSource.steps);
  const step=id=>{const matches=expected.filter(item=>item.id===id);assert.equal(matches.length,1);return matches[0]};
  if(!restore){
    if(kind==='TRAVERSAL'){
      step(ids[0]).parameters.traversal.alias='audited_observation';
      const fields=step(ids[1]).parameters.fieldGroup.fields.filter(field=>field.name==='observation_id');assert.equal(fields.length,1);fields[0].expr={select:'audited_observation.id'};
    }else if(kind==='EXPANSION'){
      step('expand').parameters.expansion.as='participant';step('expand').parameters.expansion.emptyPolicy='PRESERVE_PARENT';
      step('identity').parameters.identity.expr={call:'uuid5',args:[{literal:sourceRowIdentityVectors.originalNamespace},{select:'root.id'},{call:'concat',args:[{literal:','},{call:'reference_id',args:[{select:'participant.entity.reference'}]}]}]};
      const fields=step('fields').parameters.fieldGroup.fields.filter(field=>field.name==='member_id');assert.equal(fields.length,1);fields[0].expr={call:'reference_id',args:[{select:'participant.entity.reference'}]};
    }else step('identity').parameters.identity.expr.args[0]={literal:sourceRowIdentityVectors.changedNamespace};
  }
  for(const change of changes){const intended=step(change.stepId);assert.deepEqual(change.parameters,intended.parameters,`Exact intended parameters for ${change.stepId}`);assert.equal(change.enabled,intended.enabled,`Preserve enabled for ${change.stepId}`)}
  if(candidate)assert.deepEqual(candidate.importedSource.steps,expected,'Candidate retains every intended source descriptor, sibling, field and operation in order');
}
function assertCandidateRows(kind,document,preview,original,{restore=false}={}) {
  if(restore){assertDefaultTraversalPreview(document,preview);return}
  if(kind==='TRAVERSAL')assertTraversalAliasValues(document,preview,sourceField(original,expectedSteps[kind][1],'observation_id').field.fieldId);
  else assertGroupMemberSourceValues(document,preview,{preserveEmpty:kind==='EXPANSION',changedIdentity:kind==='IDENTITY'});
}

export async function runTraversalSourceRows({page,run,testInfo,kind}) {
  assert(Object.hasOwn(expectedSteps,kind));const outputId=kind==='TRAVERSAL'?'ResearchSubject':'GroupMember';
  const target=run.target.lifecycle[kind];assert(target,`Fresh owned ${kind} target`);await run.useTarget(target);
  const url=new URL(run.uiUrl);url.searchParams.set('project',target.project);url.searchParams.set('explorer',target.explorerId);
  const openPage=async()=>{await page.goto(url.href,{waitUntil:'domcontentloaded'});await page.getByTestId(`construction-table-${outputId}`).click();await expect(page.getByTestId('construction-preview')).toHaveAttribute('data-preview-status','ready')};
  await openPage();const before=await run.state(),original=documentFor(before,outputId);assertDefaultStepMetadata(original);assertDefaultTraversalPreview(original,await run.preview());
  assert.equal(typeof run.network,'function');assert.equal(typeof run.assertSourceColumnIntegrity,'function');const originalNetwork=await run.network(before,outputId);
  const affectedStepIds=kind==='TRAVERSAL'?sourceSubtreeStepIds(original,expectedSteps[kind][0]):expectedSteps[kind];
  run.report.sourceRows??=[];const record={kind,outputId,primaryStepId:expectedSteps[kind][0],beforeVersion:before.draftVersion,status:'running'};run.report.sourceRows.push(record);
  const checkColumns=(phase,prior,next,priorNetwork,nextNetwork,{allowFamilyShapeChanges=kind==='TRAVERSAL'}={})=>{
    const result=run.assertSourceColumnIntegrity(prior,next,priorNetwork,nextNetwork,{sourceStepIds:affectedStepIds,allowFamilyShapeChanges});
    record.integrity??=[];record.integrity.push({phase,sourceStepIds:affectedStepIds,allowFamilyShapeChanges,...result});
  };
  try {
  if(kind==='TRAVERSAL')await run.action('TRAVERSAL: reject a dangling alias without deleting dependent operations',async()=>{
    const editor=await openSourceStep(page,expectedSteps[kind][0],original);await editor.getByRole('textbox',{name:'Related record alias',exact:true}).fill('audited_observation');
    const review=page.getByRole('region',{name:'Proposed source step',exact:true});
    const negative=await proposalResponse(page,review,{outputId,stepIds:[expectedSteps[kind][0]],valid:false});
    assert([400,422].includes(negative.response.status()),'Invalid alias is a precise validation rejection, not a stale-draft waiver');
    const code=negative.body.error?.code??negative.body.code;assert.equal(typeof code,'string');
    run.report.expectedRejections??=[];run.report.expectedRejections.push({pathname:new URL(negative.response.url()).pathname,status:negative.response.status(),requestBody:negative.request,code,responseBody:negative.body});
    await expect(review.getByRole('alert')).toContainText(/alias|selector|observation|scope|context/i);await expect(review.getByTestId('imported-source-step-apply')).toBeDisabled();
    sameSaved(before,await run.state());await review.getByRole('button',{name:'Cancel',exact:true}).click();await expect(editor).toHaveCount(0);sameSaved(before,await run.state());
  });
  let candidate;
  const previewEdit=async({restore=false,capture=false}={})=>{
    await configure(page,kind,original,{restore});const review=page.getByRole('region',{name:'Proposed source step',exact:true});
    const base=await run.state(),baseNetwork=await run.network(base,outputId);
    const proposal=await proposalResponse(page,review,{outputId,stepIds:expectedSteps[kind]});expectTypedEdit(kind,proposal.request,original,{restore,candidate:proposal.body.candidateDocument});
    const saved=await run.state();assert.equal(proposal.request.expectedDraftVersion,saved.draftVersion);assert.equal(proposal.request.expectedDraftDigest,saved.draftDigest);assert.equal(proposal.request.snapshotToken,saved.catalog.snapshotToken);
    assert.equal(proposal.body.outputId,outputId);assert.equal(proposal.body.draftVersion,proposal.request.expectedDraftVersion);assert.equal(proposal.body.draftDigest,proposal.request.expectedDraftDigest);assert.equal(proposal.body.snapshotToken,proposal.request.snapshotToken);assert.equal(proposal.body.candidateNetwork.outputId,outputId);
    await expect(review.getByTestId('imported-source-step-apply')).toBeEnabled();candidate=proposal.body.candidateDocument;
    const preview=await run.preview({candidate:true});assert.equal(preview.receiptId,proposal.body.proposalId,'Both the displayed result and Apply must belong to this exact proposal');
    assertStableSourceStructure(original,candidate);checkColumns(restore?'restore-candidate':'candidate',documentFor(base,outputId),candidate,baseNetwork,proposal.body.candidateNetwork);assertCandidateRows(kind,candidate,preview,original,{restore});
    if(capture)await captureProposal(page,review,testInfo,kind,record);return{review,proposal};
  };
  await run.action(`${kind}: Preview independently expected rows and Cancel the complete staged change`,async()=>{
    const{review}=await previewEdit({capture:true});sameSaved(before,await run.state());await review.getByRole('button',{name:'Cancel',exact:true}).click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);sameSaved(before,await run.state());assertDefaultTraversalPreview(original,await run.preview());
  });
  await run.action(`${kind}: reload discards an unapplied local proposal while preserving the complete saved workspace`,async()=>{
    await previewEdit();await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId(`construction-table-${outputId}`).click();await expect(page.getByRole('region',{name:'Proposed source step',exact:true})).toHaveCount(0);sameSaved(before,await run.state());assertDefaultTraversalPreview(original,await run.preview());
  });
  await run.action(`${kind}: Apply the same intended operations and reload exact rows and values`,async()=>{
    const{review}=await previewEdit();const expectedWorkspace={...before.workspace,documents:before.workspace.documents.map(document=>document.output.id===outputId?candidate:document)};
    await review.getByTestId('imported-source-step-apply').click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
    const applied=await run.state();assert(applied.draftVersion>before.draftVersion);assert.deepEqual(applied.workspace,expectedWorkspace,'Apply exactly the reviewed target and retain all other outputs');record.appliedVersion=applied.draftVersion;checkColumns('saved',original,documentFor(applied,outputId),originalNetwork,await run.network(applied,outputId));
    await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId(`construction-table-${outputId}`).click();const reloaded=await run.state();assert.deepEqual(reloaded.workspace,applied.workspace);checkColumns('reloaded',original,documentFor(reloaded,outputId),originalNetwork,await run.network(reloaded,outputId));assertCandidateRows(kind,documentFor(reloaded,outputId),await run.preview(),original);record.appliedParameters=documentFor(reloaded,outputId).importedSource.steps.filter(step=>expectedSteps[kind].includes(step.id)).map(step=>({stepId:step.id,parameters:step.parameters}));
  });
  await run.action(`${kind}: edit existing operations back to their original settings and reload`,async()=>{
    const saved=await run.state();const{review}=await previewEdit({restore:true});sameSaved(saved,await run.state());await review.getByTestId('imported-source-step-apply').click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);
    await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId(`construction-table-${outputId}`).click();const restored=await run.state();assert(restored.draftVersion>saved.draftVersion);const document=documentFor(restored,outputId);
    assertStableSourceStructure(original,document);checkColumns('restored-reloaded',original,document,originalNetwork,await run.network(restored,outputId),{allowFamilyShapeChanges:false});assertDefaultTraversalPreview(document,await run.preview());assert.deepEqual(document.importedSource.steps,original.importedSource.steps,'Restore every effective source parameter and descriptor');
    assert.deepEqual(restored.workspace.documents.filter(document=>document.output.id!==outputId),before.workspace.documents.filter(document=>document.output.id!==outputId));record.restoredVersion=restored.draftVersion;
  });
  record.status='passed';record.coverage='Actual source edit/Preview/Cancel/Apply/edit restore/reload; reload ownership. Real competing-draft rejection is covered by FIELD_GROUP workflow, not claimed here.';
  } catch(error) {record.status='failed';record.error=error.message;throw error;}
}
