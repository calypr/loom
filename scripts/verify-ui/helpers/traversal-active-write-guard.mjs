import assert from'node:assert/strict';
/** Pure authorization for an intercepted write: an unset or changed owner never grants access. */
export function allowTraversalActiveWrite({url,method,data,apiPath,outputId,allowSourceWrites=false}){
 if(!allowSourceWrites||method!=='POST'||!apiPath||!outputId)return false;
 let actual,active;try{actual=new URL(url);active=new URL(apiPath)}catch{return false}
 if(active.search||active.hash||actual.search||actual.hash||actual.username||actual.password||active.username||active.password)return false;
 if(!/^\/api\/v1\/projects\/[^/]+\/explorers\/[^/]+\/authoring\/v2$/.test(active.pathname))return false;
 if(actual.origin!==active.origin||actual.pathname!==`${active.pathname}/commands`)return false;
 const commands=data?.commands;if(!Array.isArray(commands)||commands.length!==1)return false;
 const command=commands[0];return command?.outputId===outputId&&['REFRESH_IMPORTED_SOURCE','APPLY_IMPORTED_RELATIONSHIP_PROPOSAL'].includes(command.type);
}
export function traversalRequestIsWrite(method){return !['GET','HEAD','OPTIONS','POST'].includes(method)}
