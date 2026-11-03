import { createDevSession } from '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data/scripts/loom-dev.mjs';

const s = createDevSession(
  process.env,
  '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data',
);

const blank = '  ';

const cases = [
  [
    'register-blank',
    'mutation($input:RegisterDataframeRecipeRevisionInput!){registerDataframeRecipeRevision(input:$input){digest}}',
    {input: {projectId: blank, name: 'zzz-probe-recipe', recipe: {recipeSchemaVersion: 1, name: 'files', translationVersion: '1', fragments: {fragments: {}}, outputs: []}}},
  ],
  [
    'revisions-blank',
    'query{dataframeRecipeRevisions(projectId:"  ",name:"x"){digest}}',
    null,
  ],
  [
    'revision-blank',
    'query{dataframeRecipeRevision(projectId:"  ",name:"x",digest:"sha256:deadbeef"){digest}}',
    null,
  ],
  [
    'explain-blank',
    'query($input:ExplainDataframeRecipeInput!){explainDataframeRecipe(input:$input){recipeDigest}}',
    {input: {name: 'zzz-probe-recipe', bindings: {project: blank, datasetGeneration: 'aigenerated-1779823203692-1165034000'}}},
  ],
];

for (const [name, query, variables] of cases) {
  const body = variables ? {query, variables} : {query};
  const res = await fetch(s.apiUrl + '/graphql/graph', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(body),
  });
  const text = await res.text();
  let code = '';
  let message = text.slice(0, 120);
  try {
    const j = JSON.parse(text);
    if (j?.errors?.length) {
      code = j.errors[0]?.extensions?.code ?? '?';
      message = j.errors[0]?.message ?? '';
    }
  } catch {}
  console.log(String(name).padEnd(16), res.status, String(code).padEnd(18), String(message).slice(0, 70).replace(/\n/g, ' '));
}
process.exit(0);
