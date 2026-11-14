import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
export const traversalFixtureURL = new URL('../../../testdata/traversal-first/', import.meta.url);
export const traversalOracle = JSON.parse(readFileSync(new URL('oracle.json', traversalFixtureURL), 'utf8'));
const sorted = rows => structuredClone(rows).sort((a,b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));

/** Bind actual public keys to authored names; expected values never come from metadata. */
export function sourceColumn(document, name) {
  const matches = document.columns.filter(column => column.source?.imported?.outputColumn === name);
  assert.equal(matches.length, 1, `Exactly one source column must identify ${name}`);
  return matches[0].column;
}
export function assertDefaultTraversalPreview(document, preview, oracle = traversalOracle) {
  assert.equal(preview.outputId, document.output.id);
  assert.equal(preview.sampled, false);
  assert(Array.isArray(preview.rows));
  const expected = oracle.defaultOutputs[document.output.id];
  assert(expected, `No independent oracle for ${document.output.id}`);
  const names = Object.keys(expected[0]);
  const columns = names.map(name => [name, sourceColumn(document, name)]);
  const actual = preview.rows.map(row => Object.fromEntries(columns.map(([name,key]) => {
    assert(Object.hasOwn(row,key), `Missing returned key for ${name}; absent is not null`);
    return [name,row[key]];
  })));
  assert.deepEqual(sorted(actual), sorted(expected));
  if(document.output.id==='GroupMember')assertOriginalGroupMemberIdentities(document,preview);
  return actual;
}
export function assertAuditedPivot(document, preview) {
  const id = sourceColumn(document,'id');
  const value = sourceColumn(document,traversalOracle.auditedPivotColumn);
  assert.equal(preview.sampled,false);
  assert.deepEqual(sorted(preview.rows.map(row => {
    assert(Object.hasOwn(row,id) && Object.hasOwn(row,value));
    return {id:row[id],value:row[value]};
  })),sorted(traversalOracle.auditedPivotValues));
}
export function assertDefaultTraversalHistory(document, preview) {
  assert.equal(document.output.id,'ResearchSubject');
  assert.equal(document.importedRecipe?.name,'calypr-meta-default','Long history must retain the actual pinned default source');
  const steps=document.construction?.steps ?? [];
  assert.deepEqual(steps.map(step=>step.id),Array.from({length:100},(_,i)=>`stress_calc_${String(i).padStart(3,'0')}`));
  for(const [index,step]of steps.entries()) {
    assert.deepEqual(step.inputs,index ? [{kind:'STEP_OUTPUT',stepId:steps[index-1].id}] : [{kind:'SOURCE_PROJECTION'}]);
    assert.deepEqual(step.operation,{kind:'DERIVE',derive:{constructionId:step.id,outputColumnId:`stress_column_${String(index).padStart(3,'0')}`,operation:'ADD',left:{kind:'LITERAL',literal:{kind:'INTEGER',integer:1}},right:{kind:'LITERAL',literal:{kind:'INTEGER',integer:2}},missingInputPolicy:'ERROR'}});
  }
  assertDefaultTraversalPreview(document,preview);
  for(const row of preview.rows) for(let i=0;i<100;i++) {
    const key=`stress_value_${String(i).padStart(3,'0')}`;
    assert(Object.hasOwn(row,key),`Missing calculated ${key}`); assert.equal(row[key],3);
  }
  assert.equal(preview.rows.length,4,'Same-default ResearchSubject history proves400 calculated cells');
}
export function validateTraversalSeed(resources) {
  for(const [type,count]of Object.entries(traversalOracle.recordCounts)) {
    assert.equal(resources[type].length,count,`${type} seed count`);
    assert.equal(new Set(resources[type].map(row=>row.id)).size,count,`${type} duplicate source IDs`);
  }
  const find=(type,id)=>{const row=resources[type].find(row=>row.id===id);assert(row,`${type}/${id}`);return row};
  const doc=find('DocumentReference','document-one');
  assert.equal(doc.identifier[0].value,'DOC_ID_ONE'); assert.equal(doc.category[0].coding[0].display,'DOC_CATEGORY_ONE');
  assert.equal(doc.extension[0].valueString,'DOC_EXT_ONE'); assert.equal(doc.content[0].attachment.extension[0].valueBoolean,true);
  assert.deepEqual(find('Observation','obs-one').focus,[{reference:'DocumentReference/document-one'},{reference:'Specimen/specimen-one'}]);
  assert.equal(find('Observation','obs-one').component[0].valueString,'COMPONENT_ONE');
  assert.deepEqual(find('Group','group-empty').member,[]);
  assert.equal(find('Patient','p-one').identifier[0].value,'PAT_ID_ONE');
  for(const [type,id,value]of [['ResearchSubject','rs-one','RS_EXT_ONE'],['Patient','p-one','PAT_EXT_ONE'],['Specimen','specimen-one','SPEC_EXT_ONE'],['MedicationAdministration','medication-one','MED_EXT_ONE'],['ResearchStudy','study-one','STUDY_EXT_ONE'],['Group','group-one','GROUP_EXT_ONE']])
    assert.equal(find(type,id).extension[0].valueString,value);
  assert.equal(find('DocumentReference','document-study').subject.reference,'ResearchStudy/study-one');
  return true;
}

export function assertTraversalCompositionsEquivalent(a,b) {
  assert.deepEqual([a.status,b.status],['passed','passed']);assert.equal(a.composition,'inspector-right');assert.equal(b.composition,'inspector-below');
  assert.equal(a.targetArtifactSHA256,b.targetArtifactSHA256);assert.equal(a.fixtureSourceDigest,b.fixtureSourceDigest);
  for(const report of [a,b]){assert.equal(report.before.sourceFingerprint,report.after.sourceFingerprint);assert.equal(report.before.api.liveSource,report.after.api.liveSource);assert.equal(report.before.api.runningBinary,report.after.api.runningBinary);assert.equal(report.comparison.length,15);assert(report.comparison.every(x=>x.verified))}
  assert.equal(a.before.sourceFingerprint,b.before.sourceFingerprint);assert.equal(a.before.api.liveSource,b.before.api.liveSource);assert.equal(a.before.api.runningBinary,b.before.api.runningBinary);
  assert.deepEqual(a.canonicalBaseline,b.canonicalBaseline);assert.deepEqual(a.canonicalHistory,b.canonicalHistory);assert.deepEqual(a.sourceCandidate,b.sourceCandidate);
  return{sameSource:true,sameSavedRecipe:true,sameProposedRecipe:true,states:5,capturesPerComposition:15};
}

export function assertDefaultStepMetadata(document) {
  const expected=traversalOracle.sourcePlanByOutput[document.output.id];assert(expected);
  const steps=document.importedSource?.steps;assert(Array.isArray(steps));
  assert.deepEqual(steps.map(step=>({stepId:step.id,kind:step.kind})),expected.map(({stepId,kind})=>({stepId,kind})));
  const parameterKeys={OUTPUT:'output',FIELD_GROUP:'fieldGroup',CATALOG_PROJECTION:'catalog',DYNAMIC_COLUMN:'dynamic',EXTENSION_COLUMN:'extension',PIVOT:'pivot',TRAVERSAL:'traversal',EXPANSION:'expansion',IDENTITY:'identity'};
  for(const [index,step]of steps.entries()) {
    assert.equal(step.editable,true,`${document.output.id}/${step.id} must expose its existing parameters`);
    assert(step.parameters?.[parameterKeys[step.kind]],`Missing typed parameters for ${document.output.id}/${step.id}`);
    if(step.kind==='FIELD_GROUP') {
      assert.deepEqual(step.parameters.fieldGroup.fields.map(field=>field.name),expected[index].fieldNames);
      assert(step.parameters.fieldGroup.fields.every(field=>typeof field.fieldId==='string'&&field.fieldId.length>0));
      assert.equal(new Set(step.parameters.fieldGroup.fields.map(field=>field.fieldId)).size,expected[index].fieldNames.length);
    }
  }
  return expected;
}

export function assertOriginalGroupMemberIdentities(document,preview) {
  const spec=traversalOracle.defaultGroupMemberIdentity;
  assert.equal(document.output.id,'GroupMember');
  const group=sourceColumn(document,'group_id'),member=sourceColumn(document,'member_id');
  assert.equal(document.columns.filter(column=>column.source?.imported?.outputColumn===spec.recipeIdentityName).length,0,'Named recipe identity is compiler row metadata, not an invented projected field');
  assert(preview.rows.every(row=>Object.hasOwn(row,spec.previewIdentityKey)),'Original UUID identity must be returned explicitly');
  assert.deepEqual(sorted(preview.rows.map(row=>({group_id:row[group],member_id:row[member],rowIdentity:row[spec.previewIdentityKey]}))),sorted(spec.vectors.map(({group_id,member_id,rowIdentity})=>({group_id,member_id,rowIdentity}))));
  assert(preview.rows.every(row=>!spec.defaultAbsentGroupIds.includes(row[group])),'Default EXCLUDE has no synthetic empty-group row');
  const identity=document.importedSource.steps.find(step=>step.id==='identity');
  if(identity?.parameters?.identity)assert.equal(identity.parameters.identity.name,spec.recipeIdentityName);
}
