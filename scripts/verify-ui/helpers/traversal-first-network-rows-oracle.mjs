import assert from 'node:assert/strict';
/** Resolve provenance from verified server identities, never a label or current graph selection. */
export function patientGenderTraversal(document,columnId,network){
 const source=document.importedSource;assert(source);assert.equal(network?.owner,'IMPORTED_SOURCE');assert.equal(network.outputId,document.output.id);
 const columns=document.columns.filter(x=>x.columnId===columnId);assert.equal(columns.length,1);const column=columns[0];
 const bindings=network.bindings.filter(x=>x.columnId===columnId);assert.equal(bindings.length,1,'Gender needs one actual current network binding');const binding=bindings[0];assert.equal(binding.status,'EXACT');assert(binding.sourceStepId&&binding.occurrenceId);
 const producers=source.steps.filter(x=>x.id===binding.sourceStepId);assert.equal(producers.length,1,'The bound source step must exist exactly once');const producer=producers[0];assert.equal(producer.enabled,true);
 const parents=source.steps.filter(x=>x.id===producer.parentId&&x.kind==='TRAVERSAL'&&x.parameters?.traversal?.toResourceType==='Patient');assert.equal(parents.length,1,'Gender must belong to the actual Patient traversal');const parent=parents[0];assert.equal(parent.id,'traversals[0]');assert.equal(parent.enabled,true);assert.equal(parent.occurrenceId,binding.occurrenceId);
 const occurrences=network.occurrences.filter(x=>x.occurrenceId===binding.occurrenceId);assert.equal(occurrences.length,1);assert.equal(occurrences[0].sourceStepId,parent.id);assert.equal(occurrences[0].resourceType,'Patient');assert.equal(occurrences[0].enabled,true);
 const routes=document.route.children?.filter(x=>x.occurrenceId===parent.occurrenceId&&x.resourceType==='Patient')??[];assert.equal(routes.length,1,'The actual authored Patient occurrence must still exist');
 const saved=source.fieldBindings?.filter(x=>x.columnId===columnId)??[];assert(saved.length<=1,'Existing saved field binding cannot be ambiguous');
 assert.equal(producer.kind,'CATALOG_PROJECTION');assert.equal(producer.id,'traversals[0].catalogProjections[0]');assert.deepEqual(producer.parameters?.catalog,{name:'populated_fields',includePaths:['*'],kinds:['scalar'],naming:'PATH'},'Pristine Patient catalog declaration required');assert.equal(parent.parameters.traversal.alias,'patient');assert.equal(column.source?.imported?.outputColumn,'patient__gender','Bind the pinned semantic output, never a display label');assert.equal(saved.length,0,'Catalog gender cannot claim an unrelated explicit field identity');
 return parent;
}
/** Journey commands are authorized only for the current exact QA owner. */
export function allowTraversalJourneyWrite({url,method,data,apiPath,outputId}){
 if(method!=='POST'||!apiPath||outputId!=='ResearchSubject')return false;
 let actual,active;try{actual=new URL(url);active=new URL(apiPath)}catch{return false}
 if(active.search||active.hash||actual.search||actual.hash||actual.username||actual.password||active.username||active.password)return false;
 if(!/^\/api\/v1\/projects\/[^/]+\/explorers\/[^/]+\/authoring\/v2$/.test(active.pathname))return false;
 if(actual.origin!==active.origin||actual.pathname!==`${active.pathname}/commands`)return false;
 const commands=data?.commands;if(!Array.isArray(commands)||commands.length!==1)return false;
 return commands[0]?.outputId===outputId&&['REFRESH_IMPORTED_SOURCE','APPLY_IMPORTED_RELATIONSHIP_PROPOSAL','APPLY_CONSTRUCTION_PROPOSAL'].includes(commands[0].type);
}
