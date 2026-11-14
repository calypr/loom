import assert from 'node:assert/strict';
import { traversalOracle, sourceColumn } from './traversal-first-oracle.mjs';
const sorted = rows => structuredClone(rows).sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b)));

// Independently authored fixture identities, computed with Python stdlib uuid:
// n=uuid.uuid3(uuid.NAMESPACE_DNS, namespace); uuid.uuid5(n, group+','+member).
// A preserved empty collection has a null member; concat retains its literal comma.
export const sourceRowIdentityVectors = Object.freeze({
  originalNamespace: 'aced-idp.org', changedNamespace: 'traversal-identity.example',
  changedNamespaceUUID: '1a52f347-76cf-30b0-8012-dbca6ccdc876',
  original: [
    {group_id:'group-one',member_id:'p-one',rowIdentity:'aa5112d6-e05f-5afd-ac7a-3ea24d5326d0'},
    {group_id:'group-one',member_id:'p-many',rowIdentity:'f618be31-ccd0-5f5a-b863-015739e85b87'},
  ],
  preservedEmpty: {group_id:'group-empty',member_id:null,rowIdentity:'969ee011-e2f6-512f-be9f-bf2cddd9f33e'},
  changed: [
    {group_id:'group-one',member_id:'p-one',rowIdentity:'e85f04c7-a4f3-5cd3-bc41-1b03d741f3c1'},
    {group_id:'group-one',member_id:'p-many',rowIdentity:'7a8138c2-5d77-5441-a20d-12ee45954c38'},
  ],
});
export function sourceStep(document,id,kind) {
  const matches=document.importedSource?.steps?.filter(step=>step.id===id)??[];
  assert.equal(matches.length,1,`Exact existing source step ${id}`);
  if(kind)assert.equal(matches[0].kind,kind);
  assert(matches[0].parameters,'Actual editable parameters are required');
  return matches[0];
}
export function sourceField(document,stepId,name) {
  const fields=sourceStep(document,stepId,'FIELD_GROUP').parameters.fieldGroup.fields;
  const matches=fields.filter(field=>field.name===name);assert.equal(matches.length,1,`Exact authored field ${name}`);
  return {field:matches[0],index:fields.indexOf(matches[0])};
}
export function columnForSourceField(document,stepId,fieldId) {
  assert.equal(sourceStep(document,stepId,'FIELD_GROUP').parameters.fieldGroup.fields.filter(field=>field.fieldId===fieldId).length,1,'Field belongs to the intended existing operation');
  const bindings=document.importedSource.fieldBindings?.filter(binding=>binding.fieldId===fieldId)??[];
  assert.equal(bindings.length,1,'Do not infer field provenance or choose an arbitrary output column');
  const columns=document.columns.filter(column=>column.columnId===bindings[0].columnId);
  assert.equal(columns.length,1,'The explicit field binding must identify one actual output column');
  return columns[0];
}
export function assertTraversalAliasValues(document,preview,fieldId) {
  assert.equal(document.output.id,'ResearchSubject');assert.equal(preview.outputId,'ResearchSubject');assert.equal(preview.sampled,false);
  const expected=traversalOracle.defaultOutputs.ResearchSubject;
  const observation=columnForSourceField(document,'traversals[0].traversals[0].fields',fieldId).column;
  const keys=Object.keys(expected[0]).map(name=>[name,name==='patient__observation__observation_id'?observation:sourceColumn(document,name)]);
  assert.deepEqual(sorted(preview.rows.map(row=>Object.fromEntries(keys.map(([name,key])=>{assert(Object.hasOwn(row,key),`Missing ${name} is not null`);return[name,row[key]]})))),sorted(expected));
  assert.equal(preview.rows.length,4,'Alias editing preserves four ResearchSubject output records');
}
export function assertGroupMemberSourceValues(document,preview,{preserveEmpty=false,changedIdentity=false}={}) {
  assert.equal(document.output.id,'GroupMember');assert.equal(preview.outputId,'GroupMember');assert.equal(preview.sampled,false);
  const group=sourceColumn(document,'group_id'),member=sourceColumn(document,'member_id');
  assert.equal(document.columns.filter(column=>column.source?.imported?.outputColumn==='id').length,0,'Recipe row identity is metadata, not an invented projected field');
  const expected=structuredClone(changedIdentity?sourceRowIdentityVectors.changed:sourceRowIdentityVectors.original);
  if(preserveEmpty){assert.equal(changedIdentity,false);expected.push(structuredClone(sourceRowIdentityVectors.preservedEmpty));}
  const actual=preview.rows.map(row=>{
    for(const key of [group,member,'__loom_row_id'])assert(Object.hasOwn(row,key),`Missing returned key ${key} cannot be normalized to null`);
    return{group_id:row[group],member_id:row[member],rowIdentity:row.__loom_row_id};
  });
  assert.deepEqual(sorted(actual),sorted(expected));return actual;
}
// Newly hydrated mappings are verified by the shared actual-network integrity check.
export function assertStableSourceStructure(before,after) {
  assert.deepEqual(after.importedRecipe,before.importedRecipe,'Immutable recipe pin');
  const topology=(node,parentId)=>[{occurrenceId:node.occurrenceId,parentId,resourceType:node.resourceType,catalogEdgeId:node.catalogEdgeId,matchMode:node.matchMode},...(node.children??[]).flatMap(child=>topology(child,node.occurrenceId))];
  assert.deepEqual(topology(after.route),topology(before.route),'No traversal occurrence or relationship retargeting');
  assert.deepEqual(after.construction,before.construction,'Source edits preserve unrelated construction');
  assert.deepEqual(after.importedSource.steps.map(step=>step.id),before.importedSource.steps.map(step=>step.id),'Edit existing operations in their original positions');
  for(const original of before.importedSource.steps.filter(step=>step.kind==='FIELD_GROUP')) {
    const next=sourceStep(after,original.id,'FIELD_GROUP');
    assert.deepEqual(next.parameters.fieldGroup.fields.map(field=>field.fieldId),original.parameters.fieldGroup.fields.map(field=>field.fieldId),'Authored field identity/order is immutable');
  }
  for(const binding of before.importedSource.fieldBindings??[])assert.deepEqual(after.importedSource.fieldBindings?.find(next=>next.fieldId===binding.fieldId),binding,'Previously known source field-to-column mappings remain immutable');
}

export function sourceSubtreeStepIds(document,rootStepId) {
  sourceStep(document,rootStepId);const included=new Set([rootStepId]);let changed=true;
  while(changed){changed=false;for(const step of document.importedSource.steps)if(step.parentId&&included.has(step.parentId)&&!included.has(step.id)){included.add(step.id);changed=true;}}
  return document.importedSource.steps.filter(step=>included.has(step.id)).map(step=>step.id);
}
