import {readdirSync,lstatSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {join} from 'node:path';

const root=fileURLToPath(new URL('../../../',import.meta.url));
export function traversalVerificationFiles(sourceRoot=root) {
  const files=[];
  for(const directory of ['scripts','scripts/verify-ui/helpers','scripts/verify-ui/helpers/tests','scripts/verify-ui/specs','scripts/verify-ui/workflows']) {
    if(lstatSync(join(sourceRoot,directory)).isSymbolicLink())throw new Error(`Traversal source directory cannot be a symlink: ${directory}`);
    for(const entry of readdirSync(join(sourceRoot,directory),{withFileTypes:true}).sort((a,b)=>a.name.localeCompare(b.name))) {
      if(!entry.name.startsWith('traversal-')||!entry.name.endsWith('.mjs'))continue;
      if(entry.isSymbolicLink())throw new Error(`Traversal verification source cannot be a symlink: ${directory}/${entry.name}`);
      if(entry.isFile())files.push(`${directory}/${entry.name}`);
    }
  }
  return files;
}
const checks=()=>{
  const files=traversalVerificationFiles();
  const tests=files.filter(path=>path.includes('/helpers/tests/'));
  return[{id:'traversal-first-exact-oracles-and-guards',cwd:'.',command:['node-test',...tests],sourceFiles:files.filter(path=>!tests.includes(path))}];
};
const lifecycleChecks=label=>[
  `Locate the exact saved ${label} occurrence, field or operation through native traversal controls`,
  'Preview the intended parameter change against independently expected exact rows, values and identities',
  'Cancel preserves the complete saved workspace, version, digest and initiating focus',
  'Apply only the reviewed receipt and preserve immutable pins, unrelated operations and outputs',
  'Reload retains the intended parameters, identities, row policies and exact result',
  'Edit the same occurrence or operation without appending a duplicate or retargeting its input',
  'Restore or remove through the supported native workflow and reload the independently expected original result',
];
const sourceKinds=['OUTPUT','FIELD_GROUP','CATALOG_PROJECTION','DYNAMIC_COLUMN','EXTENSION_COLUMN','PIVOT','TRAVERSAL','EXPANSION','IDENTITY'];
const cases=Object.fromEntries(sourceKinds.map(kind=>[`source-${kind.toLowerCase().replaceAll('_','-')}`,{
  playwrightConfig:'traversal-first-source.playwright.config.mjs',playwrightTest:'traversal-first-source-lifecycle.spec.mjs',
  playwrightGrep:`Traversal-first ${kind} native source lifecycle$`,focusedChecks:checks(),requiredChecks:lifecycleChecks(kind),
}]));
for(const [name,grep]of [['network','Traversal-first imported network lifecycle$'],['related-rows','Traversal-first related rows lifecycle$'],['group','Traversal-first Group lifecycle$']])cases[name]={
  playwrightConfig:'traversal-first-network-rows.playwright.config.mjs',playwrightTest:'traversal-first-network-rows.spec.mjs',playwrightGrep:grep,focusedChecks:checks(),requiredChecks:lifecycleChecks(name),
};
for(const [name,composition]of [['compare-right','inspector-right'],['compare-below','inspector-below']])cases[name]={
  playwrightConfig:'traversal-first.playwright.config.mjs',playwrightTest:'traversal-first-comparison.spec.mjs',playwrightGrep:`Traversal-first working composition ${composition}$`,focusedChecks:checks(),
  requiredChecks:['Same actual default recipe, fixture, saved target and current source/API identities for both compositions','Initial, selected occurrence, complex editor, proposed result and default-source100-operation history use real native controls','Exact rows, values, identities, operation order and unchanged saved workspace after Cancel','Capture1536x900,1536x1100 and resize back with useful preview, bounded inspector, keyboard access and no clipping'],
};
cases.legacy={playwrightConfig:'traversal-first.playwright.config.mjs',playwrightTest:'traversal-first-comparison.spec.mjs',playwrightGrep:'Traversal-first existing legacy source upgrade edit restore and reload$',focusedChecks:checks(),requiredChecks:lifecycleChecks('real saved legacy source')};
export const traversalFirstScenario=Object.freeze({
  id:'traversal-first',workflow:'native-traversal-first-default-recipe',hooks:['useRecipeEditingOwner','useConstructionLifecycle'],
  endpoints:['GET /authoring/v2/builder','POST /authoring/v2/source-relationship-proposals','POST /authoring/v2/construction-proposals','POST /authoring/v2/commands','POST /authoring/v2/preview'],
  requiredTransitions:['Locate and edit every default source-operation family through the authored traversal','Native Preview, Cancel, Apply, edit, restore, reload, dependency rejection and real stale recovery','Graph relationships, explicit related rows and construction Group retain distinct authoritative semantics'],
  gateReasons:['Owned fresh fixture and exact current source/API/binary/configuration proofs are required','Compare actual working compositions before choosing the final default','Owned standalone success does not establish authenticated Calypr frame acceptance'],
  script:'run-traversal-first-native.mjs',cases,
  coverage:Object.keys(cases).map(name=>({feature:`Traversal-first ${name}`,acceptance:name.startsWith('compare-')?{intent:'row-lifecycle',kind:'probe',case:name}:{intent:'row-lifecycle',kind:'lifecycle',case:name,checks:{choice:0,proposal:1,cancel:2,apply:3,savedRows:3,reload:4,edit:5,restoration:6}},status:'untested',reason:'Registered contract; final native execution and authenticated integration are recorded separately.'})),
});
