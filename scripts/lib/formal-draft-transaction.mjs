import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, readdirSync, existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root, tools, digest } from './formal-tools.mjs';
import { apiDoctor, checkApiEvents } from './formal-api.mjs';
import { verificationTimeoutMs, goTestTimeout } from './verification-budget.mjs';

export const draftTransactionModel = 'DraftSaveTransaction';
export const draftTransactionFile = 'internal/explorer/arango/drafts.go';
export const draftTransactionAdapter = 'internal/explorer/arango/draft_transaction_formal_replay_test.go';
export const draftTransactionProperties = ['Inv_NoPartialDraftHistory', 'Inv_ConflictIsDomainConflict', 'Inv_SuccessRequiresConfirmedCommit', 'Inv_MissingCASAborts', 'Inv_ValidSaveWorks', 'Inv_KnownFailureDoesNotPublish'];
const modelDirectory = join(root, 'verification/builder-interactions');
const testName = 'TestDraftTransactionModelReplay';
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(name => [name, canonical(value[name])])) : value;
const key = value => JSON.stringify(canonical(value));
const files = () => ['go.mod', 'go.sum', draftTransactionAdapter, ...['internal/explorer/arango', 'internal/explorer', 'internal/store/arango'].flatMap(directory => readdirSync(join(root, directory)).filter(file => file.endsWith('.go') && !file.endsWith('_test.go')).map(file => `${directory}/${file}`))].sort();
export const draftTransactionSourceHashes = () => Object.fromEntries(files().map(file => [file, digest(join(root, file))]));
const fields = ['begin', 'read', 'archive', 'update', 'commit'];
const initial = () => ({ phase: 'begin', outcomes: Object.fromEntries(fields.map(name => [name, 'none'])), stagedHistory: false, stagedHead: false, history: false, head: false, transactionResult: 'none', result: 'none', backendConflict: false });
const outcomesFor = phase => phase === 'begin' || phase === 'archive' ? ['ok', 'conflict', 'other'] : phase === 'commit' ? ['ok', 'conflict', 'reject', 'unknown'] : ['ok', 'missing', 'conflict', 'other'];
function successors(state) {
    const next = [];
    const add = (action, edit) => next.push({ action, vars: { ...structuredClone(state), ...edit } });
    if (state.phase === 'abort') add('Abort', { phase: 'return', stagedHistory: false, stagedHead: false });
    else if (state.phase === 'return') add('Return', { phase: 'done', result: ['conflict', 'domain'].includes(state.transactionResult) ? 'draft-conflict' : state.transactionResult === 'ok' ? 'success' : 'other-error' });
    else if (state.phase !== 'done') for (const outcome of outcomesFor(state.phase)) {
        const outcomes = { ...state.outcomes, [state.phase]: outcome }, backendConflict = outcome === 'conflict';
        if (state.phase === 'begin') add('Begin', { outcomes, backendConflict, phase: outcome === 'ok' ? 'read' : 'return', transactionResult: outcome === 'ok' ? 'none' : outcome });
        else if (state.phase === 'commit') add('Commit', { outcomes, backendConflict, phase: 'return', history: state.stagedHistory && ['ok', 'unknown'].includes(outcome), head: state.stagedHead && ['ok', 'unknown'].includes(outcome), transactionResult: outcome === 'reject' ? 'other' : outcome });
        else add('Query', { outcomes, backendConflict, phase: outcome === 'ok' ? { read: 'archive', archive: 'update', update: 'commit' }[state.phase] : 'abort', transactionResult: outcome === 'missing' ? 'domain' : outcome === 'ok' ? 'none' : outcome, stagedHistory: state.stagedHistory || state.phase === 'archive' && outcome === 'ok', stagedHead: state.stagedHead || state.phase === 'update' && outcome === 'ok' });
    }
    return next;
}
export function draftTransactionFrontier() {
    const states = new Map(), steps = [], queue = [initial()];
    while (queue.length) {
        const value = queue.shift(), id = key(value); if (states.has(id)) continue; states.set(id, value);
        for (const next of successors(value)) { steps.push([id, next.action, key(next.vars)]); queue.push(next.vars); }
    }
    return { states: [...states.values()], steps };
}
function validateSource(source) { if (source !== undefined) assert.ok(typeof source === 'string' && source.trim().length > 0, 'Production source override must be nonempty text'); }
export function validateDraftTransactionGraph(graph, { productionSource } = {}) {
    validateSource(productionSource);
    assert.equal(graph.schemaVersion, 1); assert.equal(graph.model, draftTransactionModel);
    assert.equal(graph.modelSha256, digest(join(modelDirectory, `${draftTransactionModel}.tla`)), 'Stale draft transaction model');
    assert.equal(graph.configSha256, digest(join(modelDirectory, `${draftTransactionModel}.cfg`)), 'Stale draft transaction config');
    assert.deepEqual(Object.keys(graph.productionSha256).sort(), files(), 'Changed draft transaction source set');
    for (const file of files()) if (!(productionSource !== undefined && file === draftTransactionFile)) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Stale production source: ${file}`);
    const expected = draftTransactionFrontier(), expectedStates = new Set(expected.states.map(key));
    assert.equal(graph.states.length, expected.states.length, 'Incomplete draft transaction frontier');
    const states = new Map();
    for (const state of graph.states) { assert.equal(typeof state.id, 'string'); assert.equal(typeof state.start, 'boolean'); assert.ok(!states.has(state.id), 'Duplicate state ID'); states.set(state.id, state); assert.ok(expectedStates.delete(key(state.vars)), 'Unknown or duplicate transaction state'); }
    assert.equal(expectedStates.size, 0); const starts = graph.states.filter(state => state.start); assert.equal(starts.length, 1); assert.deepEqual(starts[0].vars, initial());
    const edges = new Set(expected.steps.map(key)); assert.equal(graph.steps.length, edges.size, 'Incomplete transaction edges');
    for (const edge of graph.steps) { assert.ok(states.has(edge.from) && states.has(edge.to), 'Dangling transaction edge'); assert.ok(edges.delete(key([key(states.get(edge.from).vars), edge.action, key(states.get(edge.to).vars)])), 'Unknown or duplicate transaction edge'); }
    assert.equal(edges.size, 0);
    return graph.states.filter(state => state.vars.phase === 'done').sort((a,b) => key(a.vars.outcomes).localeCompare(key(b.vars.outcomes))).map((state,index) => ({ id: `draft-transaction-${index}`, outcomes: state.vars.outcomes, expected: state.vars }));
}
export async function draftTransactionGraph(output) {
    const { scripts, jar } = tools(), scratch = mkdtempSync(join(tmpdir(), 'loom-draft-transaction-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(scratch,'states'), '-config', `${draftTransactionModel}.cfg`, '-dump', 'dot,actionlabels', join(scratch,'graph'), `${draftTransactionModel}.tla`], { cwd:modelDirectory, encoding:'utf8', timeout:verificationTimeoutMs, maxBuffer:8*1024*1024 });
        assert.ifError(result.error);assert.equal(result.status,0,result.stdout+result.stderr);
        const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(scripts,'frontends/dot.ts')).href), import(pathToFileURL(join(scripts,'frontends/view.ts')).href)]);
        const parsed=parseDot(readFileSync(join(scratch,'graph.dot'),'utf8'));
        const graph={schemaVersion:1,model:draftTransactionModel,modelSha256:digest(join(modelDirectory,`${draftTransactionModel}.tla`)),configSha256:digest(join(modelDirectory,`${draftTransactionModel}.cfg`)),productionSha256:draftTransactionSourceHashes(),states:[...parsed.states].map(([id,state])=>({id,start:state.start,vars:Object.fromEntries(Object.entries(state.vars).map(([name,value])=>[name,json(value)]))})),steps:parsed.steps};
        validateDraftTransactionGraph(graph);if(output)writeFileSync(output,JSON.stringify(graph));return graph;
    } finally {rmSync(scratch,{recursive:true,force:true});}
}
function expectedQueries(outcomes) {const result=[];if(outcomes.begin!=='ok')return result;for(const stage of ['read','archive','update']) {result.push(stage);if(outcomes[stage]!=='ok')break;}return result;}
function propertyError(property,row,observation) {const witness={schemaVersion:1,kind:'draft-transaction',model:draftTransactionModel,property,outcomes:row.outcomes,expected:row.expected,observation};const error=new Error(`${property}: ${key({outcomes:row.outcomes,observation})}`);Object.assign(error,{code:'FORMAL_PROPERTY',property,witness});return error;}
export function checkDraftTransactionResults(record,cases,{ property }={}) {
    assert.deepEqual(Object.keys(record).sort(),['schemaVersion','results'].sort());assert.equal(record.schemaVersion,1);assert.ok(Array.isArray(record.results));assert.equal(record.results.length,cases.length,'Missing Go observations');
    const results=new Map();const bools=['backendConflict','history','head','exactHistory','exactHead','stablePublished','exactReturned','callbackError'];
    for(const row of record.results) {assert.deepEqual(Object.keys(row).sort(),['id','queries','result','transactions',...bools].sort());assert.equal(typeof row.id,'string');assert.ok(!results.has(row.id),'Duplicate observation');for(const name of bools)assert.equal(typeof row[name],'boolean');assert.ok(['success','draft-conflict','other-error'].includes(row.result));assert.ok(Array.isArray(row.queries));assert.equal(row.transactions,1,'SaveDraft retried transaction');results.set(row.id,row);}
    const failures=[],conformance=[];
    for(const row of cases) {
        const actual=results.get(row.id);assert.ok(actual,'Missing case');const expected=row.expected,local=[],fail=property=>local.push(propertyError(property,row,actual));
        if(actual.history!==actual.head)fail(draftTransactionProperties[0]);
        if(expected.backendConflict&&(actual.result!=='draft-conflict'||!actual.backendConflict))fail(draftTransactionProperties[1]);
        if(actual.result==='success'&&(row.outcomes.commit!=='ok'||!actual.history||!actual.head||!actual.exactReturned))fail(draftTransactionProperties[2]);
        if(['read','update'].some(stage=>row.outcomes[stage]==='missing')&&(actual.result!=='draft-conflict'||actual.history||actual.head))fail(draftTransactionProperties[3]);
        if(row.outcomes.commit==='ok'&&actual.result!=='success')fail(draftTransactionProperties[4]);
        if(!['ok','unknown'].includes(expected.transactionResult)&&(actual.history||actual.head))fail(draftTransactionProperties[5]);
        failures.push(...local);
        try {
            assert.deepEqual(actual.queries,expectedQueries(row.outcomes),'FORMAL_CONFORMANCE: changed query sequence');
            assert.equal(actual.callbackError,['read','archive','update'].some(stage=>!['none','ok'].includes(row.outcomes[stage])),'FORMAL_CONFORMANCE: callback result');
            if(!local.length){for(const name of ['history','head','result','backendConflict'])assert.equal(actual[name],expected[name],`FORMAL_CONFORMANCE: ${name}`);for(const name of ['exactHistory','exactHead','stablePublished','exactReturned'])assert.equal(actual[name],true,`FORMAL_CONFORMANCE: ${name}`);}
        } catch(error) {error.code="FORMAL_CONFORMANCE";conformance.push(error);}
    }
    const selected=property?failures.find(error=>error.property===property):failures[0];
    if(selected){selected.violations=failures.map(error=>error.witness);throw selected;}
    if(conformance.length)throw conformance[0];
    if(failures.length){const error=new Error(`FORMAL_CONFORMANCE: selected property ${property} did not fail; other properties failed`);error.code='FORMAL_CONFORMANCE';throw error;}
    return{cases:cases.length,success:record.results.filter(row=>row.result==='success').length,conflicts:record.results.filter(row=>row.result==='draft-conflict').length,unknownCommit:cases.filter(row=>row.outcomes.commit==='unknown').length};
}

function runGo(cases,{productionSource,output}={}) {
    validateSource(productionSource);const scratch=mkdtempSync(join(tmpdir(),'loom-draft-transaction-go-')),tool=apiDoctor(),before=draftTransactionSourceHashes();
    try {
        const input=join(scratch,'input.json'),out=join(scratch,'output.json');writeFileSync(input,JSON.stringify({schemaVersion:1,cases:cases.map(({id,outcomes})=>({id,outcomes}))}));const inputSha=digest(input);
        const args=['test','-json',`-timeout=${goTestTimeout}`,'-count=1','-run',`^${testName}$`];
        if(productionSource!==undefined){const replacement=join(scratch,'drafts.go'),overlay=join(scratch,'overlay.json');writeFileSync(replacement,productionSource);writeFileSync(overlay,JSON.stringify({Replace:{[join(root,draftTransactionFile)]:replacement}}));args.push('-overlay',overlay);}
        args.push('./internal/explorer/arango');const env={...process.env,GOFLAGS:'',GOWORK:'off',GOTOOLCHAIN:'local',LOOM_FORMAL_DRAFT_TRANSACTION_INPUT:input,LOOM_FORMAL_DRAFT_TRANSACTION_OUTPUT:out};
        if(tool.executable==='/workspace/.loom-setup/go/bin/go'){if(!env.GOMODCACHE&&existsSync('/workspace/.loom-setup/gomod'))env.GOMODCACHE='/workspace/.loom-setup/gomod';if(!env.GOCACHE&&existsSync('/workspace/loom/.gocache'))env.GOCACHE='/workspace/loom/.gocache';}
        const result=spawnSync(tool.executable,args,{cwd:root,env,encoding:'utf8',timeout:verificationTimeoutMs,maxBuffer:8*1024*1024,detached:true});
        if(result.error&&result.pid){try{process.kill(-result.pid,'SIGKILL');}catch(error){if(error.code!=='ESRCH')throw error;}}
        if(output){mkdirSync(output,{recursive:true});writeFileSync(join(output,'go.jsonl'),result.stdout||'');writeFileSync(join(output,'process.json'),JSON.stringify({command:[tool.executable,...args],exit:result.status,error:result.error?.message,stderr:result.stderr},null,2));}
        assert.ifError(result.error);assert.equal(result.status,0,result.stdout+result.stderr);checkApiEvents(result.stdout,{tests:[testName]});assert.equal(digest(input),inputSha,'Input changed during replay');assert.deepEqual(draftTransactionSourceHashes(),before,'Production changed during replay');assert.ok(existsSync(out),'No Go observations');const record=JSON.parse(readFileSync(out,'utf8'));if(output)writeFileSync(join(output,'observations.json'),JSON.stringify(record,null,2));return record;
    }finally{rmSync(scratch,{recursive:true,force:true});}
}
export async function checkDraftTransactionGraph(graph,{productionSource,output,witnessFile}={}) {
    const cases=validateDraftTransactionGraph(graph,{productionSource});try{const result=checkDraftTransactionResults(runGo(cases,{productionSource,output}),cases);return{...result,states:graph.states.length,transitions:graph.steps.length,boundary:'actual Store.SaveDraft, one writer, three controlled QueryRows boundaries, finite query outcomes and commit acknowledgements; transaction publication/rollback is a trusted adapter primitive, not a proof of Arango isolation or its query engine'};}catch(error){if(error.witness&&witnessFile)writeFileSync(witnessFile,JSON.stringify(error.witness,null,2));throw error;}
}
function witnessCase(witness) {
    assert.equal(witness.schemaVersion,1);assert.equal(witness.kind,'draft-transaction');assert.equal(witness.model,draftTransactionModel);assert.ok(draftTransactionProperties.includes(witness.property),'Unknown transaction property');
    const expected=draftTransactionFrontier().states.find(state=>state.phase==='done'&&key(state.outcomes)===key(witness.outcomes));assert.ok(expected,'Unknown transaction path');assert.deepEqual(witness.expected,expected,'Witness changed model expectation');return{id:'witness',outcomes:expected.outcomes,expected};
}
export async function replayDraftTransactionWitness(witness,{productionSource,output}={}) {
    const row=witnessCase(witness);try{checkDraftTransactionResults(runGo([row],{productionSource,output}),[row],{property:witness.property});return{status:'property-held',property:witness.property};}catch(error){if(error.code!=='FORMAL_PROPERTY')throw error;assert.equal(error.property,witness.property,'Witness violated a different property');return{status:'reproduced',...error.witness};}
}
export const draftTransactionModelControls = [
    [draftTransactionProperties[0], 'Query', 'IF outcome = "ok" THEN next ELSE "abort"', 'IF stage = "update" THEN "commit" ELSE IF outcome = "ok" THEN next ELSE "abort"'],
    [draftTransactionProperties[1], 'Return', 'transactionResult \\in {"conflict", "domain"}', 'transactionResult \\in {"domain"}'],
    [draftTransactionProperties[2], 'Return', 'ELSE "other-error"', 'ELSE "success"'],
    [draftTransactionProperties[3], 'Return', 'result\' = IF transactionResult', 'result\' = IF outcomes.update = "missing" THEN "other-error" ELSE IF transactionResult'],
    [draftTransactionProperties[4], 'Return', 'THEN "success" ELSE "other-error"', 'THEN "other-error" ELSE "other-error"'],
    [draftTransactionProperties[5], 'Query', 'IF outcome = "ok" THEN next ELSE "abort"', 'IF stage = "update" /\\ outcome = "other" THEN "commit" ELSE IF outcome = "ok" THEN next ELSE "abort"'],
];
export const draftTransactionSourceProofs=[
    {model:draftTransactionModel,property:draftTransactionProperties[1],kind:'draft-transaction-replay',branch:'backend-conflict',edits:[[draftTransactionFile,'errors.Join(explorer.ErrDraftConflict, err)','errors.Join(err)']]},
    {model:draftTransactionModel,property:draftTransactionProperties[0],checkedProperties:[draftTransactionProperties[0],draftTransactionProperties[5]],kind:'draft-transaction-replay',branch:'update-error-rollback',edits:[[draftTransactionFile,'return fmt.Errorf("save Explorer draft with history: %w", err)','return nil']]},
    {model:draftTransactionModel,property:draftTransactionProperties[2],kind:'draft-transaction-replay',branch:'unconfirmed-commit-success',edits:[[draftTransactionFile,'\t\treturn nil, err\n\t}\n\treturn out, nil','\t\treturn out, nil\n\t}\n\treturn out, nil']]},
    {model:draftTransactionModel,property:draftTransactionProperties[4],kind:'draft-transaction-replay',branch:'reject-valid-save',edits:[[draftTransactionFile,'\treturn out, nil\n}\n\nfunc (s *Store) GetDraftRevision', '\treturn out, explorer.ErrDraftConflict\n}\n\nfunc (s *Store) GetDraftRevision']]},
    {model:draftTransactionModel,property:draftTransactionProperties[3],kind:'draft-transaction-replay',branch:'missing-update-row',edits:[[draftTransactionFile,'if updated == nil {\n\t\t\treturn explorer.ErrDraftConflict\n\t\t}','if updated == nil {\n\t\t\treturn nil\n\t\t}']]},
];
export async function checkDraftTransactionSourceProof(proof,graph,output) {
    assert.deepEqual(proof,draftTransactionSourceProofs.find(row=>row.branch===proof.branch),'Unknown transaction source proof');mkdirSync(output,{recursive:true});const [[file,before,after]]=proof.edits,original=readFileSync(join(root,file),'utf8');assert.equal(original.split(before).length,2,'Nonunique transaction source fault');const broken=original.replace(before,after);
    await checkDraftTransactionGraph(graph,{output:join(output,'positive')});
    const cases=validateDraftTransactionGraph(graph),record=runGo(cases,{productionSource:broken,output:join(output,'negative')}),checked=[];
    for(const property of proof.checkedProperties||[proof.property]) {
        let failure;try{checkDraftTransactionResults(record,cases,{property});}catch(error){failure=error;}assert.ok(failure&&failure.code==='FORMAL_PROPERTY'&&failure.property===property,`Fault did not yield named semantic violation ${property}`);
        const witness=failure.witness,filename=property===proof.property?'counterexample.json':`${property}.json`;writeFileSync(join(output,filename),JSON.stringify(witness,null,2));
        assert.equal((await replayDraftTransactionWitness(witness)).status,'property-held');assert.equal((await replayDraftTransactionWitness(witness,{productionSource:broken})).status,'reproduced');checked.push(property);
    }
    await checkDraftTransactionGraph(graph,{output:join(output,'restored')});
    const evidence={model:draftTransactionModel,property:proof.property,checkedProperties:checked,kind:proof.kind,branch:proof.branch,sourceFiles:[file],positive:true,negative:true,restored:true,witness:'counterexample.json'};writeFileSync(join(output,'proof.json'),JSON.stringify(evidence,null,2));return evidence;
}
function validateAbstractState(value) {
    assert.deepEqual(Object.keys(value).sort(),Object.keys(initial()).sort(),'Malformed transaction state');
    assert.ok(['begin','read','archive','update','commit','abort','return','done'].includes(value.phase));
    assert.deepEqual(Object.keys(value.outcomes).sort(),fields.slice().sort());
    for(const field of fields)assert.ok(['none',...outcomesFor(field)].includes(value.outcomes[field]),'Unknown transaction outcome');
    for(const field of ['stagedHistory','stagedHead','history','head','backendConflict'])assert.equal(typeof value[field],'boolean');
    assert.ok(['none','ok','domain','conflict','other','unknown'].includes(value.transactionResult));assert.ok(['none','success','draft-conflict','other-error'].includes(value.result));
}
function modelFaultSuccessors(state,property) {
    return successors(state).map(edge=>{
        const vars=structuredClone(edge.vars);
        if(edge.action==='Query'&&state.phase==='update'&&(property===draftTransactionProperties[0]||property===draftTransactionProperties[5]&&vars.outcomes.update==='other'))vars.phase='commit';
        if(edge.action==='Return') {
            if(property===draftTransactionProperties[1]&&state.transactionResult==='conflict')vars.result='other-error';
            if(property===draftTransactionProperties[2]&&vars.result==='other-error')vars.result='success';
            if(property===draftTransactionProperties[3]&&state.outcomes.update==='missing')vars.result='other-error';
            if(property===draftTransactionProperties[4]&&state.transactionResult==='ok')vars.result='other-error';
        }
        return{action:edge.action,vars};
    });
}
function violates(property,state) {
    const done=state.phase==='done',o=state.outcomes;
    if(property===draftTransactionProperties[0])return state.history!==state.head;
    if(property===draftTransactionProperties[1])return done&&state.backendConflict&&state.result!=='draft-conflict';
    if(property===draftTransactionProperties[2])return done&&state.result==='success'&&(o.commit!=='ok'||!state.history||!state.head);
    if(property===draftTransactionProperties[3])return done&&(o.read==='missing'||o.update==='missing')&&(state.result!=='draft-conflict'||state.history||state.head);
    if(property===draftTransactionProperties[4])return done&&o.commit==='ok'&&state.result!=='success';
    if(property===draftTransactionProperties[5])return done&&(fields.slice(0,4).some(field=>!['ok','none'].includes(o[field]))||['conflict','reject'].includes(o.commit))&&(state.history||state.head);
    return false;
}
function canonicalOutcomes(value) {
    const result={...value};let stopped=false;
    for(const field of fields){if(stopped)result[field]='none';else if(result[field]!=='ok'&&field!=='commit')stopped=true;}
    return result;
}
export function draftTransactionCounterexampleWitness(counterexample) {
    assert.ok(counterexample&&draftTransactionProperties.includes(counterexample.property),'Unknown transaction property');assert.ok(Array.isArray(counterexample.states)&&counterexample.states.length>1);
    const property=counterexample.property;assert.equal(counterexample.states[0].action,'Initial','Counterexample must start at initial action');assert.deepEqual(counterexample.states[0].vars,initial());let changed=false;
    for(let index=1;index<counterexample.states.length;index++) {
        const before=counterexample.states[index-1].vars,row=counterexample.states[index];validateAbstractState(row.vars);
        assert.ok(modelFaultSuccessors(before,property).some(edge=>edge.action===row.action&&key(edge.vars)===key(row.vars)),'Unsupported or fabricated transaction counterexample edge');
        if(!successors(before).some(edge=>edge.action===row.action&&key(edge.vars)===key(row.vars)))changed=true;
    }
    const last=counterexample.states.at(-1).vars;assert.ok(changed,'Counterexample contains no supported model fault');assert.ok(violates(property,last),'Counterexample does not violate the named transaction invariant');
    const outcomes=canonicalOutcomes(last.outcomes),expected=draftTransactionFrontier().states.find(state=>state.phase==='done'&&key(state.outcomes)===key(outcomes));assert.ok(expected,'Counterexample has no healthy transaction input');
    return{schemaVersion:1,kind:'draft-transaction',model:draftTransactionModel,property,outcomes,expected};
}
export async function checkDraftTransactionGraphControls(graph) {
    const mutations=[value=>value.states.pop(),value=>value.steps.pop(),value=>value.steps.push({...value.steps[0]}),value=>{value.steps[0].action='Unknown';},value=>{value.steps[0].to='missing';},value=>{value.modelSha256='stale';},value=>{value.productionSha256[draftTransactionFile]='stale';},value=>{value.states.find(state=>state.vars.phase==='done').vars.history=!value.states.find(state=>state.vars.phase==='done').vars.history;}];
    for(const mutation of mutations){const changed=structuredClone(graph);mutation(changed);assert.throws(()=>validateDraftTransactionGraph(changed));}return mutations.length;
}
if(process.argv[1]&&pathToFileURL(resolve(process.argv[1])).href===import.meta.url){const[command,file,extra]=process.argv.slice(2);if(command==='--generate'){assert.ok(!extra||extra==='--self-test');assert.ok(!existsSync(file)||readdirSync(file).length===0,'Output must be empty');mkdirSync(file,{recursive:true});const graph=await draftTransactionGraph(join(file,'graph.json'));const replay=await checkDraftTransactionGraph(graph,{output:join(file,'go'),witnessFile:join(file,'counterexample.json')});replay.graphControls=await checkDraftTransactionGraphControls(graph);if(extra){replay.sourceControls=[];for(const proof of draftTransactionSourceProofs)replay.sourceControls.push(await checkDraftTransactionSourceProof(proof,graph,join(file,proof.branch)));}writeFileSync(join(file,'replay.json'),JSON.stringify(replay,null,2));console.log(JSON.stringify(replay));}else if(command==='--witness'){const record=JSON.parse(readFileSync(file,'utf8')),witness=record.counterexamples?draftTransactionCounterexampleWitness(record.counterexamples.find(value=>draftTransactionProperties.includes(value.property))):record;const result=await replayDraftTransactionWitness(witness,extra!==undefined?{productionSource:readFileSync(extra,'utf8')}:{});console.log(JSON.stringify(result));process.exitCode=result.status==='reproduced'?1:0;}else throw new Error('Use --generate DIRECTORY [--self-test] or --witness FILE [SCRATCH_DRAFTS_SOURCE]');}
