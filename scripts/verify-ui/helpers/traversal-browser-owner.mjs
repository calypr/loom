import assert from'node:assert/strict';
/** Owned demo transport proxies /api on its current UI origin to the REST API. */
export function traversalBrowserApiPath(uiUrl,restApiPath){
 const ui=new URL(uiUrl),rest=new URL(restApiPath);
 for(const url of[ui,rest]){assert(!url.username&&!url.password&&!url.search&&!url.hash);assert.equal(url.protocol,'http:');assert(['127.0.0.1','localhost'].includes(url.hostname))}
 assert(/^\/api\/v1\/projects\/[^/]+\/explorers\/[^/]+\/authoring\/v2$/.test(rest.pathname));
 return ui.origin+rest.pathname;
}
/** Observation only; authorizing writes additionally rejects query/method/command mismatches. */
export function matchesTraversalBrowserOwner(url,browserApiPath){
 if(!browserApiPath)return false;
 try{const actual=new URL(url),active=new URL(browserApiPath);return !actual.username&&!actual.password&&actual.origin===active.origin&&(actual.pathname===active.pathname||actual.pathname.startsWith(active.pathname+'/'))}catch{return false}
}
