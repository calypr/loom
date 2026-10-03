// Independent raw-storage oracle for the CDA Specimen -> Patient -> Observation route.
// Enumerate all category values; keep one source witness per value instead of all route rows.
export function buildCDAQuantityCategoryOracle(scopeBinds) {
  if(!scopeBinds || typeof scopeBinds!=='object' || Array.isArray(scopeBinds)) throw new Error('Explicit CDA quantity oracle scope binds are required');
  if(scopeBinds.scope_allowed!==true) throw new Error('This oracle requires allow-mode scope');
  if(typeof scopeBinds.project!=='string' || scopeBinds.project.trim()==='') throw new Error('A non-empty project scope is required');
  if(typeof scopeBinds.dataset_generation!=='string' || scopeBinds.dataset_generation.trim()==='') throw new Error('A non-empty dataset generation scope is required');
  if(typeof scopeBinds.auth_resource_paths_unrestricted!=='boolean') throw new Error('Explicit auth_resource_paths_unrestricted scope mode is required');
  if(!Array.isArray(scopeBinds.auth_resource_paths) || scopeBinds.auth_resource_paths.some(path=>typeof path!=='string')) throw new Error('auth_resource_paths must be an explicit string array');
  if(scopeBinds.auth_resource_paths_unrestricted && scopeBinds.auth_resource_paths.length!==0) throw new Error('Unrestricted local scope must not carry auth resource paths');
  const bindVars={};
  for(const key of ['project','dataset_generation','auth_resource_paths','auth_resource_paths_unrestricted']) {
    if(!Object.hasOwn(scopeBinds,key)) throw new Error(`Missing scope ${key}`);
    bindVars[key]=scopeBinds[key];
  }
  const scoped=v=>`${v}.project == @project AND ${v}.dataset_generation == @dataset_generation AND (@auth_resource_paths_unrestricted == true OR ${v}.auth_resource_path IN @auth_resource_paths)`;
  const patients=(s,p,e)=>`FOR ${e} IN fhir_edge FILTER ${e}._from == ${s}._id AND ${e}.label == "subject_Patient" AND ${e}.to_type == "Patient" AND ${scoped(e)} LET ${p}=DOCUMENT(${e}._to) FILTER ${p} != null AND ${p}.resourceType == "Patient" AND ${scoped(p)}`;
  const observations=(p,o,e)=>`FOR ${e} IN fhir_edge FILTER ${e}._to == ${p}._id AND ${e}.label == "subject_Patient" AND ${e}.from_type == "Observation" AND ${scoped(e)} LET ${o}=DOCUMENT(${e}._from) FILTER ${o} != null AND ${o}.resourceType == "Observation" AND ${scoped(o)}`;
  const query=`
LET candidates=(FOR o IN Observation FILTER ${scoped('o')} AND o.resourceType == "Observation"
 COLLECT value=o.payload.valueQuantity.code WITH COUNT INTO count RETURN {value,count})
LET matched=(FOR candidate IN candidates
 LET witness=(FOR o IN Observation FILTER ${scoped('o')} AND o.resourceType == "Observation" AND o.payload.valueQuantity.code == candidate.value
  FOR op IN fhir_edge FILTER op._from == o._id AND op.label == "subject_Patient" AND op.from_type == "Observation" AND ${scoped('op')}
  LET p=DOCUMENT(op._to) FILTER p != null AND p.resourceType == "Patient" AND ${scoped('p')}
  FOR sp IN fhir_edge FILTER sp._to == p._id AND sp.label == "subject_Patient" AND sp.to_type == "Patient" AND ${scoped('sp')}
  LET s=DOCUMENT(sp._from) FILTER s != null AND IS_SAME_COLLECTION("Specimen",s) AND ${scoped('s')}
  LIMIT 1 RETURN {specimen:s._id,patient:p._id,observation:o._id})
 FILTER LENGTH(witness)>0 RETURN {value:candidate.value,witness:FIRST(witness)})
LET has_routed_null=LENGTH(FOR m IN matched FILTER m.value == null RETURN true)>0
LET empty_first=has_routed_null ? [] : (
 FOR s IN Specimen FILTER ${scoped('s')}
 LET children=(${patients('s','p','sp')} LIMIT 1 RETURN true)
 FILTER LENGTH(children)==0 LIMIT 1 RETURN {specimen:s._id})
LET empty_second=has_routed_null ? [] : (
 FOR s IN Specimen FILTER ${scoped('s')}
 ${patients('s','p','sp')}
 LET children=(${observations('p','o','op')} LIMIT 1 RETURN true)
 FILTER LENGTH(children)==0 LIMIT 1 RETURN {specimen:s._id,patient:p._id})
LET expected=(FOR value IN UNION_DISTINCT((FOR m IN matched RETURN m.value), LENGTH(empty_first)+LENGTH(empty_second)>0 ? [null] : []) SORT TYPENAME(value),value RETURN {present:true,value})
RETURN {candidates,matched,emptyFirst:FIRST(empty_first),emptySecond:FIRST(empty_second),nullAlreadyRouted:has_routed_null,expectedCategories:expected}`;
  return {query,bindVars};
}
export function validateCDAQuantityCategoryOracle(result) {
  if(!Array.isArray(result.candidates)||!Array.isArray(result.matched)||!Array.isArray(result.expectedCategories)) throw new Error('Incomplete raw oracle result');
  for(const category of result.expectedCategories) {
    if(category.present!==true || (category.value!==null && typeof category.value!=='string')) throw new Error('Unexpected CDA quantity code category');
    if(category.value===null && (result.nullAlreadyRouted||result.emptyFirst||result.emptySecond)) continue;
    if(!result.matched.some(match=>match.value===category.value && match.witness?.specimen && match.witness?.patient && match.witness?.observation)) throw new Error('Category missing exact route witness');
  }
  return result;
}
export function compareCDAQuantityCategoryValues(oracle, discoveredCategories) {
  validateCDAQuantityCategoryOracle(oracle);
  const expected=oracle.expectedCategories.map(category=>JSON.stringify(category.value)).sort();
  const actual=discoveredCategories.map(category=>{
    if(category.key.kind==='NULL') return 'null';
    if(category.key.kind==='STRING') return JSON.stringify(category.key.string);
    throw new Error('Unexpected discovered quantity code type');
  }).sort();
  if(JSON.stringify(actual)!==JSON.stringify(expected)) throw new Error(`Complete quantity categories differ: expected ${expected}, got ${actual}`);
  return true;
}
