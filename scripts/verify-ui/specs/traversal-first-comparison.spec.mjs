import{traversalBrowserApiPath,matchesTraversalBrowserOwner}from'../helpers/traversal-browser-owner.mjs';
import{allowTraversalActiveWrite,traversalRequestIsWrite}from'../helpers/traversal-active-write-guard.mjs';
import assert from'node:assert/strict';import{readFileSync,writeFileSync}from'node:fs';import{join}from'node:path';
import{test,expect}from'playwright/test';import{createDevSession,assertOwnedDevSession,fixtureSourceDigest}from'../../loom-dev.mjs';
import{fixtureProjectExplorerCollectionPath}from'../helpers/fixture-context.mjs';
import{graphRowsNativeIdentity,assertGraphRowsNativeIdentity}from'../helpers/graph-rows-native-identity.mjs';
import{readTraversalTarget,validateDefaultHistoryProof,assertTraversalOwner,sha256}from'../helpers/traversal-first-target.mjs';
import{runTraversalFirstComparison,runTraversalLegacyFieldLifecycle}from'../workflows/traversal-first-comparison.mjs';
const documentFor=(state,id)=>{const documents=state.workspace.documents.filter(x=>x.output.id===id);assert.equal(documents.length,1);return documents[0]};
for(const composition of ['inspector-right','inspector-below'])test(`Traversal-first working composition ${composition}`,async({page,request},testInfo)=>{
  await execute({page,request,testInfo,composition,legacy:false});
});
test('Traversal-first existing legacy source upgrade edit restore and reload',async({page,request},testInfo)=>{
  await execute({page,request,testInfo,composition:'inspector-right',legacy:true});
});
async function execute({page,request,testInfo,composition,legacy}){
  const artifact=process.env.LOOM_TRAVERSAL_COMPARISON_TARGET,artifactSHA=process.env.LOOM_TRAVERSAL_COMPARISON_TARGET_SHA256;
  const target=readTraversalTarget(artifact,artifactSHA);const session=createDevSession(process.env);await assertOwnedDevSession(session,{uiRouting:'explicit-query'});
  assert.equal(target.apiOrigin,session.apiUrl);const before=graphRowsNativeIdentity(session);assert.equal(before.sourceFingerprint,target.sourceFingerprint);
  const fixtureDir=join(session.sourceRoot,'testdata/traversal-first');const fixtureBefore=fixtureSourceDigest(fixtureDir);assert.equal(fixtureBefore,target.fixtureSourceDigest);
  if(!legacy)validateDefaultHistoryProof(target);else assert(target.legacy,'A preserved existing legacy QA target is required, never fabricated');
  const report={scope:'Owned standalone comparison/native authoring; authenticated Calypr frame is NOT established',composition,legacy,before,targetArtifactSHA256:artifactSHA,fixtureSourceDigest:fixtureBefore,status:'running',actions:[],comparison:[],requests:[],browserErrors:[],attemptedWrites:[],readFailures:[]};
  let descriptor,apiPath,browserApiPath;const pending=[];
  page.on('pageerror',error=>report.browserErrors.push(error.message));
  page.on('requestfailed',request=>{if(matchesTraversalBrowserOwner(request.url(),browserApiPath)&&!request.failure()?.errorText?.includes('ERR_ABORTED'))report.readFailures.push({path:new URL(request.url()).pathname,error:request.failure()?.errorText})});
  page.on('response',response=>{if(!matchesTraversalBrowserOwner(response.url(),browserApiPath))return;const req=response.request();report.requests.push({path:new URL(response.url()).pathname,status:response.status(),method:req.method(),...(response.url().endsWith('/commands')?{commands:req.postDataJSON()?.commands?.map(x=>x.type)}:{})})});
  await page.route('**/authoring/v2/**',async route=>{
    const req=route.request(),url=new URL(req.url());if(traversalRequestIsWrite(req.method())){report.attemptedWrites.push({path:url.pathname,method:req.method()});await route.abort();return}if(req.method()==='POST'&&(/\/(?:commands|publish)$/.test(url.pathname))){
      const commands=req.postDataJSON()?.commands??[];const allowed=allowTraversalActiveWrite({url:req.url(),method:req.method(),data:req.postDataJSON(),apiPath:browserApiPath,outputId:'DocumentReference',allowSourceWrites:legacy});
      if(!allowed){report.attemptedWrites.push({path:url.pathname,commands:commands.map(x=>x.type)});await route.abort();return}
    }await route.continue();
  });
  const run={target,report,async useTarget(next){descriptor=next;apiPath=`${target.apiOrigin}${fixtureProjectExplorerCollectionPath(next.project)}/${encodeURIComponent(next.explorerId)}/authoring/v2`;browserApiPath=traversalBrowserApiPath(target.compositions[composition].uiUrl,apiPath);const state=await this.state();assertTraversalOwner(next,state,target.generation);return state},async state(){assert(apiPath);const response=await request.get(apiPath+'/builder',{timeout:30000});assert.equal(response.status(),200);return response.json()},async preview({candidate=false}={}){const container=page.getByTestId('construction-preview');await expect(container).toHaveAttribute('data-preview-status','ready');await expect(container).toHaveAttribute('data-preview-proposal-id',candidate?/\S+/:'');const receiptId=await container.getAttribute('data-preview-receipt-id'),outputId=await container.getAttribute('data-preview-output-id');assert(receiptId&&outputId);const response=await request.post(apiPath+'/preview',{timeout:30000,data:{receiptId,outputId,limit:100}});assert.equal(response.status(),200);const value=await response.json();assert.equal(value.receiptId,receiptId);assert.equal(value.outputId,outputId);assert.equal(value.sampled,false);return value},async action(name,callback){const started=performance.now();try{const result=await callback();report.actions.push({name,status:'passed',elapsedMs:performance.now()-started});return result}catch(error){report.actions.push({name,status:'failed',elapsedMs:performance.now()-started,error:error.message});throw error}}};
  try{
    if(legacy)await runTraversalLegacyFieldLifecycle({page,run,testInfo,uiUrl:target.compositions[composition].uiUrl});
    else await runTraversalFirstComparison({page,run,testInfo,composition,uiUrl:target.compositions[composition].uiUrl});
    await Promise.all(pending);assert.deepEqual(report.browserErrors,[]);assert.deepEqual(report.attemptedWrites,[]);assert.deepEqual(report.readFailures,[]);assert.equal(report.requests.filter(x=>x.status>=400).length,0,'Unexpected owned API errors');report.status='passed';
  }catch(error){report.status='failed';report.error=error.message;throw error}
  finally{let integrityError;try{report.after=graphRowsNativeIdentity(session);assertGraphRowsNativeIdentity(before,report.after);assert.equal(fixtureSourceDigest(fixtureDir),fixtureBefore);assert.equal(sha256(readFileSync(artifact)),artifactSHA)}catch(error){integrityError=error;report.status='failed';report.integrityError=error.message}await testInfo.attach('traversal-first-native-evidence.json',{body:Buffer.from(JSON.stringify(report,null,2)),contentType:'application/json'});writeFileSync(testInfo.outputPath('traversal-first-native-evidence.json'),JSON.stringify(report,null,2),{mode:0o600,flag:'wx'});if(integrityError)throw integrityError}
}
