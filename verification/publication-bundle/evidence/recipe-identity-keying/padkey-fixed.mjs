import { createDevSession } from '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data/scripts/loom-dev.mjs';
const s = createDevSession(process.env, '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data');
const G = 'loom_dev_verify_zzz-padkey-7k';
const PAD = '  ' + G + '  ';
const M = 'mutation($input:RegisterDataframeRecipeRevisionInput!){registerDataframeRecipeRevision(input:$input){digest projectId}}';
const recipe = {recipeSchemaVersion: 1, name: 'files', translationVersion: '1', outputs: [{name: 'DocumentReference', rootResourceType: 'DocumentReference', rowGrain: 'document_reference'}]};
async function gql(query, variables) {
  const r = await fetch(s.apiUrl + '/graphql/graph', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({query, ...(variables ? {variables} : {})})});
  const t = await r.text();
  try { return JSON.parse(t); } catch { return {raw: t}; }
}
const reg = await gql(M, {input: {projectId: PAD, name: 'files', recipe}});
console.log('register-padded    ', JSON.stringify(reg?.data?.registerDataframeRecipeRevision ?? reg?.errors?.[0]?.extensions?.code));
const cleanList = await gql(`query{dataframeRecipeRevisions(projectId:"${G}",name:"files"){digest}}`);
console.log('list-clean         ', JSON.stringify(cleanList?.data?.dataframeRecipeRevisions ?? cleanList?.errors?.[0]?.extensions?.code));
const padList = await gql(`query{dataframeRecipeRevisions(projectId:"  ${G}  ",name:"files"){digest}}`);
console.log('list-padded        ', JSON.stringify(padList?.data?.dataframeRecipeRevisions ?? padList?.errors?.[0]?.extensions?.code));
const d = cleanList?.data?.dataframeRecipeRevisions?.[0]?.digest;
if (d) {
  const g = await gql(`query{dataframeRecipeRevision(projectId:"${G}",name:"files",digest:"${d}"){digest}}`);
  console.log('get-clean          ', JSON.stringify(g?.data?.dataframeRecipeRevision ?? g?.errors?.[0]?.extensions?.code));
}
process.exit(0);
