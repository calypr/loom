import assert from 'node:assert/strict';
import {writeFileSync} from 'node:fs';
export function publicTraversalSeed(seed) {
 const value={fresh:seed.fresh,reused:seed.reused,bootstrapExplorerId:seed.bootstrapExplorerId};
 assert.deepEqual(value,{fresh:true,reused:false,bootstrapExplorerId:'loom-dev-bootstrap'});
 return value;
}
export function traversalProgressRecorder(path) {
 let cumulative={};
 return async event=>{
  cumulative={...cumulative,...event};
  writeFileSync(path,JSON.stringify(cumulative,null,2)+'\n',{mode:0o600});
 };
}
