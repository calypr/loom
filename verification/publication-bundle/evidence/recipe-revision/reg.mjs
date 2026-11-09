import { createDevSession } from '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data/scripts/loom-dev.mjs';
const s = createDevSession(process.env, '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data');
const G='loom_dev_verify_zzz-no-such-9x';
const M='mutation($input:RegisterDataframeRecipeRevisionInput!){registerDataframeRecipeRevision(input:$input){projectId name digest parentDigest recipe createdAt}}';
const cases=[
 ['malformed-json','{{{'],
 ['json-null',null],
 ['json-array',[]],
 ['empty-object',{}],
 ['bad-shape',{name:null,steps:'x'}],
 ['minimal-valid',{name:'zzz_probe_recipe'}],
];
for (const [n,recipe] of cases){
  const r=await fetch(s.apiUrl+'/graphql/graph',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({query:M,variables:{input:{projectId:G,name:'zzz-probe-recipe',recipe}}})});
  const t=await r.text(); let code='',m=t.slice(0,170);
  try{const j=JSON.parse(t); if(j?.errors?.length){code=j.errors[0]?.extensions?.code??'?'; m=j.errors.map(e=>String(e.message).slice(0,70)).join(' || ');}}catch{}
  console.log(String(n).padEnd(16),r.status,String(code).padEnd(18),m.replace(/\n/g,' '));
}
process.exit(0);
