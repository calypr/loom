import assert from 'node:assert/strict';
import {traversalOracle,sourceColumn,assertDefaultTraversalPreview,assertAuditedPivot} from './traversal-first-oracle.mjs';
export const sourceValueCases=Object.freeze({
 OUTPUT:{outputId:'DocumentReference',stepId:'output',kind:'OUTPUT'},
 FIELD_GROUP:{outputId:'DocumentReference',stepId:'fields',kind:'FIELD_GROUP'},
 CATALOG_PROJECTION:{outputId:'ResearchSubject',stepId:'traversals[0].traversals[0].catalogProjections[0]',kind:'CATALOG_PROJECTION'},
 DYNAMIC_COLUMN:{outputId:'DocumentReference',stepId:'dynamicColumns[0]',kind:'DYNAMIC_COLUMN'},
 EXTENSION_COLUMN:{outputId:'DocumentReference',stepId:'extensionColumns[0]',kind:'EXTENSION_COLUMN'},
 PIVOT:{outputId:'ResearchSubject',stepId:'traversals[0].traversals[0].pivots[0]',kind:'PIVOT'},
});
const sorted=rows=>structuredClone(rows).sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b)));
export function assertSourceValueChange(kind,document,preview){
 if(kind==='PIVOT'){assertDefaultTraversalPreview(document,preview);assertAuditedPivot(document,preview);return}
 if(kind==='FIELD_GROUP'){
  const expected=structuredClone(traversalOracle.defaultOutputs.DocumentReference);for(const row of expected)row.title='Reviewed source title';
  const oracle={...traversalOracle,defaultOutputs:{...traversalOracle.defaultOutputs,DocumentReference:expected}};assertDefaultTraversalPreview(document,preview,oracle);return;
 }
 assertDefaultTraversalPreview(document,preview);
 if(kind==='OUTPUT')return;
 const id=sourceColumn(document,'id');
 const name=kind==='CATALOG_PROJECTION'?'patient__observation__valueInteger':kind==='DYNAMIC_COLUMN'?'audited_legacy_identifier':'audited_attachment_flag';
 const key=sourceColumn(document,name);
 const expected=kind==='CATALOG_PROJECTION'?[{id:'rs-zero',value:[]},{id:'rs-one',value:[7]},{id:'rs-many',value:[11,13]},{id:'rs-shared',value:[11,13]}]:[{id:'document-one',value:kind==='DYNAMIC_COLUMN'?'DYNAMIC_REVIEWED':true},{id:'document-study',value:null}];
 assert.deepEqual(sorted(preview.rows.map(row=>{assert(Object.hasOwn(row,key),'Absent differs from null/list');return{id:row[id],value:row[key]}})),sorted(expected));
 if(kind==='DYNAMIC_COLUMN')assert(!document.columns.some(x=>x.source?.imported?.outputColumn==='legacy_identifier'),'Renamed source family must not leave a stale competing column');
 if(kind==='EXTENSION_COLUMN')assert(!document.columns.some(x=>x.source?.imported?.outputColumn==='legacy_root'),'Source-path edit replaces the prior extension family');
}
export function assertSourceValueOwnership(before,after,outputId,stepId,{restored=false}={}){
 assert.deepEqual(after.workspace.documents.filter(x=>x.output.id!==outputId),before.workspace.documents.filter(x=>x.output.id!==outputId));
 const a=before.workspace.documents.find(x=>x.output.id===outputId),b=after.workspace.documents.find(x=>x.output.id===outputId);assert(a&&b);
 for(const key of ['importedRecipe','route','rows','construction'])assert.deepEqual(b[key],a[key]);
 assert.deepEqual(b.importedSource.steps.map(x=>x.id),a.importedSource.steps.map(x=>x.id));
 assert.deepEqual(b.importedSource.steps.filter(x=>x.id!==stepId),a.importedSource.steps.filter(x=>x.id!==stepId));
 if(restored){const withoutBindings=source=>Object.fromEntries(Object.entries(source).filter(([key])=>key!=='fieldBindings'));assert.deepEqual(withoutBindings(b.importedSource),withoutBindings(a.importedSource));for(const binding of a.importedSource.fieldBindings??[])assert.deepEqual(b.importedSource.fieldBindings?.find(x=>x.fieldId===binding.fieldId),binding);assert.deepEqual(b.columns,a.columns)}
}
export function assertIntendedSourceValueParameters(kind,original,candidate,{restore=false}={}){
 const expected=structuredClone(original.parameters),step=candidate.importedSource.steps.filter(x=>x.id===original.id);assert.equal(step.length,1);
 if(!restore){
  if(kind==='OUTPUT')expected.output.rowGrain='resource';
  else if(kind==='FIELD_GROUP'){const fields=expected.fieldGroup.fields.filter(x=>x.name==='title');assert.equal(fields.length,1);fields[0].expr={literal:'Reviewed source title'}}
  else if(kind==='DYNAMIC_COLUMN'){expected.dynamic.columnPrefix='audited';expected.dynamic.value={literal:'DYNAMIC_REVIEWED'}}
  else if(kind==='EXTENSION_COLUMN'){expected.extension.columnPrefix='audited';expected.extension.source={select:'root.content[].attachment.extension[]'}}
  else if(kind==='PIVOT'){delete expected.pivot.discovery;expected.pivot.columnExpr={select:'observation.code.coding[].display'};expected.pivot.valueExpr={select:'observation.valueInteger'};expected.pivot.columns=['Synthetic fixture score'];expected.pivot.columnAliases={'Synthetic fixture score':'observation_values__audited_score'}}
 }
 assert.deepEqual(step[0].parameters,expected,'Only intended existing typed parameter slots may change');
 assert.equal(step[0].enabled,original.enabled);assert.equal(step[0].kind,original.kind);assert.equal(step[0].parentId,original.parentId);assert.equal(step[0].occurrenceId,original.occurrenceId);
 if(kind==='CATALOG_PROJECTION')assert.equal(step[0].valueMode,restore?original.valueMode:'ALL');else assert.equal(step[0].valueMode,original.valueMode);
}
