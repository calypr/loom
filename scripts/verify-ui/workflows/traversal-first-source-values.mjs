import assert from'node:assert/strict';import{expect}from'playwright/test';
import{sourceValueCases,assertSourceValueChange,assertSourceValueOwnership,assertIntendedSourceValueParameters}from'../helpers/traversal-source-values-oracle.mjs';
import{EXPLORER_AUTHORING_SEMANTICS_VERSION}from'../../../ui/packages/loom-ui/src/authoringSemanticsVersion.mjs';
import{assertDefaultTraversalPreview}from'../helpers/traversal-first-oracle.mjs';
export async function setNativeSourceExpression(editor,label,value){
 const kind=value.select!==undefined?'select':value.call!==undefined?'call':'literal';
 await editor.getByRole('combobox',{name:`${label} type`,exact:true}).selectOption(kind);
 if(kind==='select'){await editor.getByRole('textbox',{name:`${label} source path`,exact:true}).fill(value.select);return}
 if(kind==='literal'){
  const type=value.literal===null?'null':typeof value.literal;await editor.getByRole('combobox',{name:`${label} literal type`,exact:true}).selectOption(type);
  if(type==='boolean')await editor.getByRole('checkbox',{name:`${label} boolean`,exact:true}).setChecked(value.literal);
  else if(type!=='null')await editor.getByRole(type==='number'?'spinbutton':'textbox',{name:`${label} literal value`,exact:true}).fill(String(value.literal));return;
 }
 await editor.getByRole('combobox',{name:`${label} function`,exact:true}).selectOption(value.call);
 const args=value.args??[],list=editor.getByRole('list',{name:`${label} ordered arguments`,exact:true});
 let count=await list.locator(':scope > li').count();while(count>args.length){await editor.getByRole('button',{name:`${label} remove argument ${count}`,exact:true}).click();count--}
 while(count<args.length){await editor.getByRole('button',{name:`Add argument to ${label}`,exact:true}).click();count++}
 for(const[index,arg]of args.entries()){const part=value.call==='uuid5'&&index===0?'Namespace':value.call==='join'&&index===1?'Separator':`Argument ${index+1}`;await setNativeSourceExpression(editor,`${label} · ${part}`,arg)}
}
const same=(a,b)=>{assert.deepEqual(b.workspace,a.workspace);assert.equal(b.draftVersion,a.draftVersion);assert.equal(b.draftDigest,a.draftDigest);assert.equal(b.catalog.snapshotToken,a.catalog.snapshotToken)};
const doc=(state,id)=>{const a=state.workspace.documents.filter(x=>x.output.id===id);assert.equal(a.length,1);return a[0]};
async function optionalExpression(editor,label,value){await editor.getByRole('checkbox',{name:`Use ${label}`,exact:true}).setChecked(value!==undefined);if(value!==undefined)await setNativeSourceExpression(editor,label,value)}
async function configure(editor,kind,original,restore){
 const p=original.parameters;
 if(kind==='OUTPUT'){await editor.getByRole('textbox',{name:'Row grain',exact:true}).fill(restore?p.output.rowGrain:'resource');return}
 if(kind==='FIELD_GROUP'){
  const index=p.fieldGroup.fields.findIndex(x=>x.name==='title');assert(index>=0);await editor.getByRole('textbox',{name:'Find projected field',exact:true}).fill('title');
  const summary=editor.getByRole('list',{name:'Projected fields',exact:true}).locator('details > summary');await expect(summary).toHaveCount(1);if(await summary.locator('xpath=parent::details').getAttribute('open')===null)await summary.click();
  await setNativeSourceExpression(editor,`Field ${index+1} expression`,restore?p.fieldGroup.fields[index].expr:{literal:'Reviewed source title'});return;
 }
 if(kind==='CATALOG_PROJECTION'){await editor.getByTestId('imported-source-value-mode').selectOption(restore?original.valueMode:'ALL');return}
 if(kind==='DYNAMIC_COLUMN'){
  await editor.getByRole('checkbox',{name:'Override Dynamic column prefix',exact:true}).setChecked(restore?p.dynamic.columnPrefix!==undefined:true);
  if(!restore||p.dynamic.columnPrefix!==undefined)await editor.getByRole('textbox',{name:'Dynamic column prefix',exact:true}).fill(restore?p.dynamic.columnPrefix:'audited');
  await optionalExpression(editor,'Dynamic value',restore?p.dynamic.value:{literal:'DYNAMIC_REVIEWED'});return;
 }
 if(kind==='EXTENSION_COLUMN'){
  await editor.getByRole('checkbox',{name:'Override Extension column prefix',exact:true}).setChecked(restore?p.extension.columnPrefix!==undefined:true);
  if(!restore||p.extension.columnPrefix!==undefined)await editor.getByRole('textbox',{name:'Extension column prefix',exact:true}).fill(restore?p.extension.columnPrefix:'audited');
  await setNativeSourceExpression(editor,'Extension item source',restore?p.extension.source:{select:'root.content[].attachment.extension[]'});return;
 }
 if(kind==='PIVOT'){
  await editor.getByRole('checkbox',{name:'Discover pivot columns',exact:true}).setChecked(restore?!!p.pivot.discovery:false);
  if(restore&&p.pivot.discovery)for(const[label,value]of [['Pivot discovery family',p.pivot.discovery.family??''],['Pivot discovery path',p.pivot.discovery.path??'']])await editor.getByRole('textbox',{name:label,exact:true}).fill(value);
  for(const[label,value]of [['Pivot column expression',restore?p.pivot.columnExpr:{select:'observation.code.coding[].display'}],['Pivot value expression',restore?p.pivot.valueExpr:{select:'observation.valueInteger'}]])await optionalExpression(editor,label,value);
  const values=restore?p.pivot.columns??[]:['Synthetic fixture score'];let count=await editor.getByRole('textbox',{name:/^Explicit pivot column \d+$/}).count();while(count>values.length){await editor.getByRole('button',{name:`Remove Explicit pivot column ${count}`,exact:true}).click();count--}while(count<values.length){await editor.getByRole('button',{name:'Add Explicit pivot column',exact:true}).click();count++}for(const[i,value]of values.entries())await editor.getByRole('textbox',{name:`Explicit pivot column ${i+1}`,exact:true}).fill(value);
  const aliases=Object.entries(restore?p.pivot.columnAliases??{}:{'Synthetic fixture score':'observation_values__audited_score'});let aliasCount=await editor.getByRole('textbox',{name:/^Alias \d+ source column$/}).count();while(aliasCount>aliases.length){await editor.getByRole('button',{name:`Remove alias ${aliasCount}`,exact:true}).click();aliasCount--}while(aliasCount<aliases.length){await editor.getByRole('button',{name:'Add pivot alias',exact:true}).click();aliasCount++}for(const[i,[key,value]]of aliases.entries()){await editor.getByRole('textbox',{name:`Alias ${i+1} source column`,exact:true}).fill(key);await editor.getByRole('textbox',{name:`Alias ${i+1} output name`,exact:true}).fill(value)}return;
 }
 throw Error(`Unsupported values lifecycle ${kind}`);
}
export async function runTraversalSourceValues({page,run,kind}){
 const info=sourceValueCases[kind];assert(info);const target=run.target.lifecycle[kind];await run.useTarget(target);
 const url=new URL(run.uiUrl);url.searchParams.set('project',target.project);url.searchParams.set('explorer',target.explorerId);await page.goto(url.href,{waitUntil:'domcontentloaded'});await page.getByTestId(`construction-table-${info.outputId}`).click();
 const before=await run.state(),original=doc(before,info.outputId),matches=original.importedSource.steps.filter(x=>x.id===info.stepId);assert.equal(matches.length,1);const source=matches[0],beforeNetwork=await run.network(before,info.outputId);const fieldIds=kind==='FIELD_GROUP'?[source.parameters.fieldGroup.fields.find(x=>x.name==='title').fieldId]:undefined;const integrityOptions={sourceStepIds:[info.stepId],fieldIds,allowFamilyShapeChanges:['DYNAMIC_COLUMN','EXTENSION_COLUMN','PIVOT'].includes(kind)};assert.equal(source.kind,kind);assertDefaultTraversalPreview(original,await run.preview());
 const open=async()=>{
  const nav=page.getByTestId('traversal-scope-navigator');const scopes=nav.getByRole('navigation',{name:'Authored traversal scopes',exact:true}).locator('button[data-occurrence-id]');let found;
  for(let i=0;i<await scopes.count();i++){await scopes.nth(i).click();const term=nav.getByRole('list',{name:'Source operations in selected scope',exact:true}).locator(`[data-operation-id="${info.stepId}"]`);if(await term.count()){assert(!found,'One operation belongs to one scope');found=await scopes.nth(i).getAttribute('data-occurrence-id')}}
  assert(found);await nav.getByRole('navigation',{name:'Authored traversal scopes',exact:true}).locator(`button[data-occurrence-id="${found}"]`).click();
  const button=nav.getByRole('list',{name:'Source operations in selected scope',exact:true}).locator(`[data-operation-id="${info.stepId}"]`).getByRole('button',{name:/^Edit source operation /});await expect(button).toHaveCount(1);await button.focus();await button.press('Enter');await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(1);return button;
 };
 const propose=async restore=>{
  const initiator=await open(),editor=page.getByTestId('imported-source-step-editor');await configure(editor,kind,source,restore);
  const saved=await run.state(),review=page.getByRole('region',{name:'Proposed source step',exact:true});
  const responsePromise=page.waitForResponse(x=>x.url().endsWith('/source-relationship-proposals')&&x.request().postDataJSON()?.sourceStep?.stepId===info.stepId);
  await review.getByRole('button',{name:'Preview source step',exact:true}).click();const response=await responsePromise;assert.equal(response.status(),200);const proposal=await response.json();assert.equal(proposal.previewStatus,'READY');
  const data=response.request().postDataJSON();assert.equal(data.expectedDraftVersion,saved.draftVersion);assert.equal(data.expectedDraftDigest,saved.draftDigest);assert.equal(data.snapshotToken,saved.catalog.snapshotToken);
  assert.equal(proposal.outputId,info.outputId);assert(proposal.proposalId);const candidate=proposal.candidateDocument;assert(candidate);assert.deepEqual(candidate.importedRecipe,original.importedRecipe);assertIntendedSourceValueParameters(kind,source,candidate,{restore});run.assertSourceColumnIntegrity(original,candidate,beforeNetwork,proposal.candidateNetwork,integrityOptions);
  assertSourceValueOwnership(before,{workspace:{...before.workspace,documents:before.workspace.documents.map(x=>x.output.id===info.outputId?candidate:x)}},info.outputId,info.stepId,{restored:restore});
  const preview=await run.preview({candidate:true});if(restore)assertDefaultTraversalPreview(candidate,preview);else assertSourceValueChange(kind,candidate,preview);
  if(kind==='OUTPUT'&&!restore)assert.equal(candidate.importedSource.steps.find(x=>x.id===info.stepId).parameters.output.rowGrain,'resource');
  run.report.proposals.push({kind,restore,request:data,proposal,preview});return{review,initiator,saved,proposal};
 };
 await run.action(`${kind}: parameter edit Preview exact rows and Cancel`,async()=>{const{review,initiator,saved}=await propose(false);await review.getByRole('button',{name:'Cancel',exact:true}).click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);await expect(initiator).toBeFocused();same(saved,await run.state());assertDefaultTraversalPreview(original,await run.preview())});
 for(const restore of[false,true])await run.action(`${kind}: ${restore?'edit original operation and restore':'Apply intended operation'} then reload`,async()=>{const{review,saved}=await propose(restore);await review.getByTestId('imported-source-step-apply').click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId(`construction-table-${info.outputId}`).click();const after=await run.state();assert(after.draftVersion>saved.draftVersion);assertSourceValueOwnership(before,after,info.outputId,info.stepId,{restored:restore});assertIntendedSourceValueParameters(kind,source,doc(after,info.outputId),{restore});run.assertSourceColumnIntegrity(original,doc(after,info.outputId),beforeNetwork,await run.network(after,info.outputId),integrityOptions);if(restore)assertDefaultTraversalPreview(doc(after,info.outputId),await run.preview());else assertSourceValueChange(kind,doc(after,info.outputId),await run.preview())});
 if(kind==='FIELD_GROUP'){
  await run.action('FIELD_GROUP: actual competing Apply yields real stale409 and preserves winner',async()=>{
   const{review,saved,proposal}=await propose(false);
   const collection=run.target.apiOrigin; // Exact current owned descriptor, never a caller supplied endpoint.
   const {fixtureProjectExplorerCollectionPath}=await import('../helpers/fixture-context.mjs');
   const apiPath=`${collection}${fixtureProjectExplorerCollectionPath(target.project)}/${encodeURIComponent(target.explorerId)}/authoring/v2`;
   const data={commandId:`traversal-field-stale-winner-${saved.draftVersion}`,semanticsVersion:EXPLORER_AUTHORING_SEMANTICS_VERSION,snapshotToken:saved.catalog.snapshotToken,expectedDraftVersion:saved.draftVersion,expectedDraftDigest:saved.draftDigest,commands:[{type:'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL',outputId:info.outputId,proposalId:proposal.proposalId}]};
   const winnerResponse=await run.request.post(apiPath+'/commands',{timeout:30000,data});assert.equal(winnerResponse.status(),200);const winner=await winnerResponse.json();assert.equal(winner.draftVersion,saved.draftVersion+1);
   const staleResponsePromise=page.waitForResponse(x=>x.url().endsWith('/commands')&&x.request().postDataJSON()?.expectedDraftVersion===saved.draftVersion);
   await review.getByTestId('imported-source-step-apply').click();const staleResponse=await staleResponsePromise;assert.equal(staleResponse.status(),409);const error=await staleResponse.json();assert.equal(error.error?.code,'DRAFT_CONFLICT');
   run.report.expectedRejections.push({pathname:new URL(staleResponse.url()).pathname,status:409,requestBody:staleResponse.request().postDataJSON(),code:'DRAFT_CONFLICT'});
   const actual=await run.state();assert.equal(actual.draftVersion,winner.draftVersion);assert.equal(actual.draftDigest,winner.draftDigest);assert.deepEqual(actual.workspace,winner.workspace);assertSourceValueOwnership(before,actual,info.outputId,info.stepId);run.assertSourceColumnIntegrity(original,doc(actual,info.outputId),beforeNetwork,await run.network(actual,info.outputId),integrityOptions);
   await expect(page.getByRole('button',{name:'Cancel earlier recipe change',exact:true})).toBeVisible();await page.getByRole('button',{name:'Cancel earlier recipe change',exact:true}).click();await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId(`construction-table-${info.outputId}`).click();assertSourceValueChange(kind,doc(actual,info.outputId),await run.preview());
   run.report.stale={winnerRequest:data,winner,actual,error};
  });
  await run.action('FIELD_GROUP: recover stale owner, restore exact original field and reload',async()=>{const{review}=await propose(true);await review.getByTestId('imported-source-step-apply').click();await expect(page.getByTestId('imported-source-step-editor')).toHaveCount(0);await page.reload({waitUntil:'domcontentloaded'});await page.getByTestId(`construction-table-${info.outputId}`).click();const actual=await run.state();assertSourceValueOwnership(before,actual,info.outputId,info.stepId,{restored:true});run.assertSourceColumnIntegrity(original,doc(actual,info.outputId),beforeNetwork,await run.network(actual,info.outputId),integrityOptions);assertDefaultTraversalPreview(doc(actual,info.outputId),await run.preview())});
 }

}
export {configure as configureSourceValueEditor};
