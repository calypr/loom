import { createDevSession } from '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data/scripts/loom-dev.mjs';
const s = createDevSession(process.env, '/var/lib/docker/volumes/arc-loom-qwen-current-20261031/_data');
const G='loom_dev_verify_zzz-no-such-9x';
const M='mutation($input:RegisterDataframeRecipeRevisionInput!){registerDataframeRecipeRevision(input:$input){digest}}';
const base=(extra,fieldExpr)=>({recipeSchemaVersion:1,name:'files',translationVersion:'1',
 fragments:{fragments:{f:{name:'f',version:'1',params:['x'],expr:{select:'$x'}}}},
 outputs:[{name:'DocumentReference',rootResourceType:'DocumentReference',rowGrain:'document_reference',
  fields:[{name:'a',...(fieldExpr?{expr:fieldExpr}:{})},...(extra||[])]}]});
const cases=[
 ['unknown-fragment',{recipeSchemaVersion:1,name:'files',translationVersion:'1',fragments:{fragments:{f:{name:'f',version:'1',params:['x'],expr:{select:'$x'}}}},outputs:[{name:'DocumentReference',rootResourceType:'DocumentReference',rowGrain:'document_reference',fields:[{name:'a',expr:{call:'fragment:nope'}}]}]}],
 ['fragment-arity',base(null,{call:'fragment:f'})],
 ['ctrl-empty-fragments',{recipeSchemaVersion:1,name:'files',translationVersion:'1',fragments:{},outputs:[{name:'DocumentReference',rootResourceType:'DocumentReference',rowGrain:'document_reference',fields:[]}]}],
 ['ctrl-bogus-op',{recipeSchemaVersion:1,name:'files',translationVersion:'1',fragments:{fragments:{}},outputs:[{name:'DocumentReference',rootResourceType:'DocumentReference',rowGrain:'document_reference',fields:[{name:'a',expr:{call:'bogus_op'}}]}]}],
];
for (const [n,recipe] of cases){
  const r=await fetch(s.apiUrl+'/graphql/graph',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({query:M,variables:{input:{projectId:G,name:'zzz-probe-recipe',recipe}}})});
  const t=await r.text(); let code='',m=t.slice(0,130);
  try{const j=JSON.parse(t); if(j?.errors?.length){code=j.errors[0]?.extensions?.code??'?'; m=j.errors[0]?.message??'';}}catch{}
  console.log(String(n).padEnd(19),r.status,String(code).padEnd(17),String(m).slice(0,80).replace(/\n/g,' '));
}
process.exit(0);
