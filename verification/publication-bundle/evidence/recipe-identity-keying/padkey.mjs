import { createDevSession } from '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data/scripts/loom-dev.mjs';

const s = createDevSession(
  process.env,
  '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data',
);

const G = 'loom_dev_verify_zzz-padkey-7k';
const PAD = '  ' + G + '  ';
const M = 'mutation($input:RegisterDataframeRecipeRevisionInput!){registerDataframeRecipeRevision(input:$input){digest}}';
const recipe = {recipeSchemaVersion: 1, name: 'files', translationVersion: '1', outputs: [{name: 'DocumentReference', rootResourceType: 'DocumentReference', rowGrain: 'document_reference'}]};

async function gql(query, variables) {
  const res = await fetch(s.apiUrl + '/graphql/graph', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({query, ...(variables ? {variables} : {})}),
  });
  const text = await res.text();
  try { return JSON.parse(text); } catch { return {raw: text}; }
}

const reg = await gql(M, {input: {projectId: PAD, name: 'files', recipe}});
const digest = reg?.data?.registerDataframeRecipeRevision?.digest ?? null;
console.log('register-padded   digest=', digest, JSON.stringify(reg?.errors?.[0]?.extensions?.details ?? reg?.errors?.[0]?.extensions?.code ?? ''));

const cleanList = await gql(`query{dataframeRecipeRevisions(projectId:"${G}",name:"files"){digest}}`);
console.log('list-clean        ', JSON.stringify(cleanList?.data ?? cleanList?.errors?.[0]?.extensions?.code));
const padList = await gql(`query{dataframeRecipeRevisions(projectId:"  ${G}  ",name:"files"){digest}}`);
console.log('list-padded       ', JSON.stringify(padList?.data ?? padList?.errors?.[0]?.extensions?.code));
if (digest) {
  const cleanGet = await gql(`query{dataframeRecipeRevision(projectId:"${G}",name:"files",digest:"${digest}"){digest}}`);
  console.log('get-clean         ', JSON.stringify(cleanGet?.data ?? cleanGet?.errors?.[0]?.extensions?.code));
}
process.exit(0);
