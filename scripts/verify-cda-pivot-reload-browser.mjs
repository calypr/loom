import assert from 'node:assert/strict';
import {readFile,mkdir,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {captureSourceFreeze} from './lib/source-freeze.mjs';
import {launchBrowser,navigate,click,waitForBrowser,browserEval} from './lib/browser.mjs';
const evidence=process.argv[2] ?? `/tmp/loom-pivot-reload-${Date.now()}`;
const apiOrigin=process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin=process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const sourceFreezeStartedAt=new Date().toISOString();
const report={evidence,project:'loom_dev_cda_fhir',cases:[],errors:[],network:[],started:new Date().toISOString(),sourceFreeze:{startedAt:sourceFreezeStartedAt,available:false,invalidatesRun:false,productFailure:false}};
let seed,baseline,table,source,patient,oracleIds,expectedCategoryLabels,base,sourceFreeze,browser;
let verificationPhase='setup';
try{
 await mkdir(evidence,{recursive:true});
 sourceFreeze=await captureSourceFreeze(process.env.LOOM_SOURCE_FREEZE_ROOT ?? fileURLToPath(new URL('..',import.meta.url)));
 report.sourceFreeze={startedAt:sourceFreezeStartedAt,available:true,watchedFileCount:sourceFreeze.watchedFileCount};
 seed=JSON.parse(await readFile(process.env.LOOM_PIVOT_RELOAD_SEED ?? '/tmp/loom-pivot-category-edit-final-2/report.json','utf8'));
 report.explorer=seed.explorer;
 assert(/^pivot-category-edit-browser-\d+$/.test(seed.explorer),'Only an owned Pivot QA Explorer may be replayed');
 assert.notEqual(seed.explorer,'cda-builder-full-qa-1790440983382','The protected full QA Explorer must never be replayed or mutated');
 base=`/api/v1/projects/loom_dev_cda_fhir/explorers/${seed.explorer}/authoring/v2`;
 const response=await fetch(apiOrigin+base+'/builder');assert(response.ok);
 baseline=await response.json();
 table=baseline.workspace.documents[0];
 const pivotStep=table.construction.steps.find(s=>s.operation.kind==='PIVOT');
 assert(pivotStep,'The saved owned workspace must contain a Pivot');
 source=seed.oracle.source.id;
 patient=seed.oracle.chain[0].witnesses[0].values[1];
 oracleIds=new Set(seed.oracle.chain.at(-1).witnesses.map(w=>w.values.at(-1)));
 assert.deepEqual(new Set(pivotStep.operation.pivot.categories.map(c=>c.key.string)),oracleIds);
 const outputsById=new Map(pivotStep.outputs.map(output=>[output.id,output]));
 expectedCategoryLabels=new Set(pivotStep.operation.pivot.categories.map(category=>{
  const output=outputsById.get(category.outputColumnId);
  assert(output?.label?.trim(),'Each raw category must resolve to its saved Pivot output by stable outputColumnId');
  return output.label.trim().toLocaleLowerCase();
 }));
 assert.equal(expectedCategoryLabels.size,oracleIds.size,'Every raw category must have a distinct saved presentation output label');
 assert.equal(expectedCategoryLabels.size,31,'The wide Pivot replay requires all 31 independently witnessed Observation categories');
 report.sourceFreeze={...report.sourceFreeze,rawOracleCategoryCount:oracleIds.size,expectedCategoryLabels:[...expectedCategoryLabels].sort()};
 browser=await launchBrowser(evidence);
 verificationPhase='browser';
 const pending=new Map();let cycle=0;
 browser.cdp.on('Network.requestWillBeSent',e=>{if(e.request.url.startsWith(uiOrigin))pending.set(e.requestId,{cycle,url:e.request.url,start:e.timestamp});});
 browser.cdp.on('Network.responseReceived',e=>{const item=pending.get(e.requestId);if(item){item.status=e.response.status;item.responseMs=(e.timestamp-item.start)*1000;}if(e.response.status>=400&&!e.response.url.endsWith('/favicon.ico'))report.errors.push({url:e.response.url,status:e.response.status});});
 browser.cdp.on('Network.loadingFinished',e=>{const item=pending.get(e.requestId);if(item){report.network.push({...item,durationMs:(e.timestamp-item.start)*1000,bytes:e.encodedDataLength});pending.delete(e.requestId);}});
 browser.cdp.on('Runtime.exceptionThrown',e=>report.errors.push(e.exceptionDetails));
 for(cycle=1;cycle<=5;cycle++){
  const start=Date.now();
  await navigate(browser.cdp,`${uiOrigin}/?project=loom_dev_cda_fhir&explorer=${seed.explorer}&mode=builder`);
  await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="construction-table-${table.output.id}"]')`);
  await click(browser.cdp,`[data-testid="construction-table-${table.output.id}"]`);
  await waitForBrowser(browser.cdp,`document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')==='2' && document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  const coverage=await browserEval(browser.cdp,`const scroll=document.querySelector('[data-testid="preview-table-scroll"]');const table=scroll?.querySelector('[role="table"]');if(!scroll||!table)throw new Error('Preview table scroll surface is missing');const originalScrollLeft=scroll.scrollLeft;const samples=[];const frame=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));let result;try{const step=Math.max(1,Math.floor(scroll.clientWidth*0.65));const max=Math.max(0,scroll.scrollWidth-scroll.clientWidth);const offsets=[];for(let left=0;left<max;left+=step)offsets.push(left);offsets.push(max);for(const left of [...new Set(offsets)]){scroll.scrollLeft=left;await frame();await new Promise(resolve=>setTimeout(resolve,25));const rows=[...table.querySelectorAll('[role="row"]')];const headers=[...table.querySelectorAll('[role="columnheader"]')];const dataRows=rows.slice(1);if(dataRows.length!==1)throw new Error('Expected exactly one rendered data row at horizontal offset '+left);const rowOrdinal=dataRows[0].firstElementChild?.innerText.trim();const cells=[...dataRows[0].querySelectorAll('[role="cell"]')];if(headers.length!==cells.length)throw new Error('Mounted header/cell count mismatch at horizontal offset '+left);samples.push({scrollLeft:scroll.scrollLeft,rowOrdinal,ariaColCount:Number(table.getAttribute('aria-colcount')),headers:headers.map(header=>({left:parseFloat(header.style.left),width:parseFloat(header.style.width),label:header.innerText.trim()})),cells:cells.map(cell=>({left:parseFloat(cell.style.left),width:parseFloat(cell.style.width),value:cell.innerText.trim()}))});}result={columnCount:Number(table.getAttribute('aria-colcount')),clientWidth:scroll.clientWidth,scrollWidth:scroll.scrollWidth,maxScrollLeft:max,samples};}finally{scroll.scrollLeft=originalScrollLeft;await frame();}return{...result,originalScrollLeft,restoredScrollLeft:scroll.scrollLeft};`);
  assert.equal(coverage.columnCount,oracleIds.size+1,'Rendered table must expose Specimen ID plus all raw-observed Pivot categories');
  assert.equal(coverage.restoredScrollLeft,coverage.originalScrollLeft,'The full-width verification must restore the user-visible scroll position');
  assert(coverage.samples.length>1,'A wide Pivot must be checked across multiple horizontal virtual windows');
  const headerByIndex=new Map();const cellByIndex=new Map();
  for(const sample of coverage.samples){
   assert.equal(sample.rowOrdinal,'1','The full horizontal sweep must retain data row ordinal 1');
   assert.equal(sample.ariaColCount,coverage.columnCount);
   for(let i=0;i<sample.headers.length;i++){
    const header=sample.headers[i];const cell=sample.cells[i];
    const index=Math.round((header.left-coverage.samples[0].headers[0].left)/header.width);
    assert.equal(Math.round((cell.left-coverage.samples[0].headers[0].left)/header.width),index,'Cell ordinal must agree with the mounted header identity');
    if(headerByIndex.has(index))assert.equal(headerByIndex.get(index),header.label,'A column ordinal must keep the same identity in every virtual window');
    if(cellByIndex.has(index))assert.equal(cellByIndex.get(index),cell.value,'A column ordinal must keep the same row value in every virtual window');
    headerByIndex.set(index,header.label);cellByIndex.set(index,cell.value);
   }
  }
  assert.equal(headerByIndex.size,coverage.columnCount,'The horizontal sweep must mount every table column');
  assert.equal(cellByIndex.size,coverage.columnCount,'The horizontal sweep must capture every cell in data row ordinal 1');
  const labels=new Set([...headerByIndex.values()].map(label=>label.trim().toLocaleLowerCase()));
  for(const label of expectedCategoryLabels)assert(labels.has(label),'Missing Pivot category column after horizontal sweep: '+label);
  const orderedCells=[...cellByIndex.entries()].sort(([left],[right])=>left-right).map(([,value])=>value);
  assert.equal(orderedCells[0],source,'Data row ordinal 1 must retain its Specimen source identity');
  assert(orderedCells.slice(1).every(value=>value===patient),'Every Pivot category cell in data row ordinal 1 must retain its Patient identity');
  const durationMs=Date.now()-start;
  report.cases.push({cycle,durationMs,renderedRowOrdinal:1,wideCoverage:{columnCount:coverage.columnCount,observedColumnCount:headerByIndex.size,scrollWindows:coverage.samples.length,maxScrollLeft:coverage.maxScrollLeft,headers:[...headerByIndex.entries()].sort(([left],[right])=>left-right).map(([,label])=>label),cells:orderedCells}});
 }
 const finalResponse=await fetch(apiOrigin+base+'/builder');assert(finalResponse.ok);
 const final=await finalResponse.json();assert.equal(final.draftDigest,baseline.draftDigest);assert.deepEqual(final.workspace,baseline.workspace);
 assert.deepEqual(report.errors,[]);
 assert(report.cases.every(c=>c.durationMs<=5000),'Reload exceeded five seconds: '+JSON.stringify(report.cases.map(c=>c.durationMs)));
 report.status='passed';
 report.productFailure=false;
}catch(error){
 const setupFailure=verificationPhase==='setup';
 report.status=setupFailure?'unverified':'failed';
 report.productFailure=!setupFailure;
 if(setupFailure)report.unverified={kind:'harness-setup',message:String(error.message??error),diagnostics:{explorer:report.explorer,phase:verificationPhase},productFailure:false};
 report.error=String(error.stack ?? error);
 process.exitCode=1;
}
finally{
 await browser?.close();
 const sourceFreezeFinishedAt=new Date().toISOString();
 if(sourceFreeze){
  try{report.sourceFreeze={...report.sourceFreeze,...(await sourceFreeze.assertUnchanged()),finishedAt:sourceFreezeFinishedAt};}
  catch(error){report.priorStatus=report.status;report.status='invalidated';report.productFailure=false;report.sourceFreeze={...report.sourceFreeze,unchanged:false,changedPaths:error.changedPaths??[],invalidatesRun:true,productFailure:false,error:String(error),finishedAt:sourceFreezeFinishedAt};process.exitCode=1;}
 }else report.sourceFreeze={...report.sourceFreeze,finishedAt:sourceFreezeFinishedAt};
 report.finished=new Date().toISOString();
 await mkdir(evidence,{recursive:true});
 await writeFile(join(evidence,'report.json'),JSON.stringify(report,null,2));
}
console.log(JSON.stringify({status:report.status,evidence,cases:report.cases.map(c=>({cycle:c.cycle,ms:c.durationMs})),error:report.error}));
