import { createDevSession } from '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data/scripts/loom-dev.mjs';
const s = createDevSession(process.env, '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data');
const G = 'loom_dev_verify_zzz-nospace-4t';
const REG = 'mutation($input:RegisterDataframeRecipeRevisionInput!){registerDataframeRecipeRevision(input:$input){digest projectId name}}';
const DESC = 'query($input:DescribeDataframeRecipeInput!){describeDataframeRecipe(input:$input){name outputs{name}}}';
const EXPL = 'query($input:ExplainDataframeRecipeInput!){explainDataframeRecipe(input:$input){name}}';
const VAL = 'mutation($input:ValidateDataframeRecipeInput!){validateDataframeRecipe(input:$input){name}}';

const mk = (name) => ({
  recipeSchemaVersion: 1, name: 'files', translationVersion: '1',
  fragments: { fragments: {} },
  outputs: [{ name, rootResourceType: 'DocumentReference', rowGrain: 'document_reference', fields: [] }],
});

async function call(label, query, variables) {
  const r = await fetch(s.apiUrl + '/graphql/graph', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ query, variables }),
  });
  const t = await r.text();
  let code = '', m = t.slice(0, 110);
  try { const j = JSON.parse(t); if (j?.errors?.length) { code = j.errors[0]?.extensions?.code ?? '?'; m = j.errors[0]?.message ?? ''; } } catch {}
  console.log(String(label).padEnd(26), r.status, String(code).padEnd(20), String(m).slice(0, 80).replace(/\n/g, ' '));
  try { return JSON.parse(t); } catch { return null; }
}

const reg = await call('register-name-blank', REG, { input: { projectId: G, name: 'zzz-probe-recipe', recipe: mk('   ') } });
const regClean = await call('register-name-valid', REG, { input: { projectId: G, name: 'zzz-probe-recipe-ok', recipe: mk('DocumentReference') } });
const digest = regClean?.data?.registerDataframeRecipeRevision?.digest;
console.log('valid-registered-digest   ', digest ? String(digest).slice(0, 16) : 'NONE');
for (const [label, q, shape] of [
  ['revision-name-blank', 'query($input:String!){__typename}', null],
]) { /* describe field does not exist; revision read below covers stored-name visibility */ void q; void shape; }
await call('explain-name-blank', EXPL, { input: { name: 'zzz-probe-recipe', bindings: { project: G, datasetGeneration: 'aigenerated-1779823203692-1165034000' } } });
await call('explain-name-valid', EXPL, { input: { name: 'zzz-probe-recipe-ok', bindings: { project: G, datasetGeneration: 'aigenerated-1779823203692-1165034000' } } });
await call('validate-name-blank', VAL, { input: { name: 'zzz-probe-recipe', bindings: { project: G } } });
if (digest) {
  const LIST = 'query{dataframeRecipeRevisions(projectId:"' + G + '",name:"zzz-probe-recipe"){digest}}';
  await call('list-name-blank', LIST, null);
}
process.exit(0);
