import { createDevSession } from '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data/scripts/loom-dev.mjs';
const s = createDevSession(process.env, '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data');
const REG = 'mutation($input:RegisterDataframeRecipeRevisionInput!){registerDataframeRecipeRevision(input:$input){digest}}';
function payload(outName) {
  return {
    query: REG,
    variables: {
      input: {
        projectId: 'loom_dev_verify_zzz-nospace-4t',
        name: 'zzz-probe-recipe',
        recipe: {
          recipeSchemaVersion: 1,
          name: 'files',
          translationVersion: '1',
          fragments: { fragments: {} },
          outputs: [{ name: outName, rootResourceType: 'DocumentReference', rowGrain: 'document_reference', fields: [] }],
        },
      },
    },
  };
}
for (const label of ['DocumentReference', '   ']) {
  const r = await fetch(s.apiUrl + '/graphql/graph', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload(label)),
  });
  console.log('OUT=' + JSON.stringify(label), '->', (await r.text()).slice(0, 500));
}
process.exit(0);
