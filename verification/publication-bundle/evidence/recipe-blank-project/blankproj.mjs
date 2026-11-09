import { createDevSession } from '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data/scripts/loom-dev.mjs';
const s = createDevSession(process.env, '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data');
const G='loom_dev_verify_zzz-no-such-9x';
const legs=[
 ['run-blank','mutation($input:RunDataframeRecipeInput!){runDataframeRecipe(input:$input){name}}',{input:{name:'r',bindings:{project:'  '}}}],
 ['run-empty','mutation($input:RunDataframeRecipeInput!){runDataframeRecipe(input:$input){name}}',{input:{name:'r',bindings:{project:''}}}],
 ['preview-blank','mutation($input:PreviewDataframeRecipeInput!){previewDataframeRecipe(input:$input){name}}',{input:{name:'r',bindings:{project:'  '}}}],
 ['validate-blank','mutation($input:ValidateDataframeRecipeInput!){validateDataframeRecipe(input:$input){name}}',{input:{name:'r',bindings:{project:'  '}}}],
 ['preflight-blank','query($input:PreflightDataframeRecipeInput!){preflightDataframeRecipe(input:$input){name}}',{input:{name:'r',bindings:{project:'  '}}}],
 ['run-goodshape-ghost','mutation($input:RunDataframeRecipeInput!){runDataframeRecipe(input:$input){name}}',{input:{name:'r',bindings:{project:G}}}],
];
for (const [n,q,v] of legs){
  const r=await fetch(s.apiUrl+'/graphql/graph',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({query:q,variables:v})});
  const t=await r.text(); let code='',m=t.slice(0,140);
  try{const j=JSON.parse(t); if(j?.errors?.length){code=j.errors[0]?.extensions?.code??'?'; m=j.errors[0]?.message??'';} }catch{}
  console.log(n.padEnd(20),r.status,String(code).padEnd(20),String(m).slice(0,90).replace(/\n/g,' '));
}
process.exit(0);
