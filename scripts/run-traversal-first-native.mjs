#!/usr/bin/env node
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {getScenario,scenarioCaseFor,caseNamesFor} from './verify-ui/registry.mjs';
const root=fileURLToPath(new URL('../',import.meta.url));
let selected,checksOnly=false;
for(let i=2;i<process.argv.length;i++) {
 const option=process.argv[i];
 if(option==='--case')selected=process.argv[++i];
 else if(option==='--checks-only')checksOnly=true;
 else throw new Error(`Unknown option ${option}; use --case NAME [--checks-only]`);
}
const scenario=getScenario('traversal-first');assert(caseNamesFor(scenario).includes(selected),`Choose one --case ${caseNamesFor(scenario).join('|')}`);
const contract=scenarioCaseFor(scenario,selected);
const run=(args,timeout=240000)=>{
 const result=spawnSync(process.execPath,args,{cwd:root,env:process.env,encoding:'utf8',timeout,maxBuffer:8<<20});
 if(result.stdout)process.stdout.write(result.stdout);if(result.stderr)process.stderr.write(result.stderr);
 assert.ifError(result.error);assert.equal(result.status,0,`Verification failed: node ${args.join(' ')}`);return result.stdout;
};
for(const check of contract.focusedChecks){assert.equal(check.cwd,'.');assert.equal(check.command[0],'node-test');run(['--test',...check.command.slice(1)]);}
if(!checksOnly){
 assert(process.env.LOOM_GRAPH_ROWS_EVIDENCE,'Set a fresh protected evidence directory');
 const args=['scripts/node_modules/playwright/cli.js','test','--config',`scripts/${contract.playwrightConfig}`,'--grep',contract.playwrightGrep];
 assert(/Total: 1 test in 1 file/.test(run([...args,'--list'])),'Official discovery must select exactly one native test');run(args);
}
