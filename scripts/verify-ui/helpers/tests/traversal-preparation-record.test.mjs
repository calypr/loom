import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {publicTraversalSeed,traversalProgressRecorder} from '../traversal-preparation-record.mjs';
test('actual official seed undefined/private properties cannot leak into creation proof',()=>{
 const seed={fresh:true,reused:false,bootstrapExplorerId:'loom-dev-bootstrap',bootstrapWorkspace:undefined,fixtureManifest:undefined,privateValue:'not-public'};
 assert.deepEqual(publicTraversalSeed(seed),{fresh:true,reused:false,bootstrapExplorerId:'loom-dev-bootstrap'});
 assert.throws(()=>publicTraversalSeed({...seed,reused:true}));assert.throws(()=>publicTraversalSeed({...seed,fresh:false}));
});
test('a failed final request retains the completed steps, exact values and last saved owner',async()=>{
 const dir=mkdtempSync(join(tmpdir(),'traversal-progress-'));try{
  const path=join(dir,'progress.json'),record=traversalProgressRecorder(path),preparation=Array.from({length:53},(_,i)=>({stepId:`step-${i}`,values:[{id:'rs-one',value:3}]})),owner={draftVersion:54,draftDigest:'saved-digest'};
  await record({index:53,preparation,owner,elapsedMs:500000});await record({status:'failed',phase:'fixture-setup',elapsedMs:900001,error:'deadline'});
  assert.deepEqual(JSON.parse(readFileSync(path)),{index:53,preparation,owner,status:'failed',phase:'fixture-setup',elapsedMs:900001,error:'deadline'});
 }finally{rmSync(dir,{recursive:true,force:true})}
});
