import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {validatePlan} from './validate_ml_dataframer_plan.mjs';

const root=resolve(dirname(fileURLToPath(import.meta.url)), '..');
const plan=JSON.parse(readFileSync(resolve(root,'docs/product/ml-dataframer/execution.json'),'utf8'));
test('current detailed plan is structurally valid',()=>{
  assert.deepEqual(validatePlan(plan),[]);
});
const cases=[
  ['obsolete revision',p=>{p.planRevision=2;},'planRevision'],
  ['missing contracts',p=>{p.contracts='docs/product/not-a-real-contract.md';},'contracts'],
  ['missing runbook',p=>{delete p.runbook;},'runbook'],
  ['unsafe document path',p=>{p.runbook='../not-in-repository.md';},'runbook'],
  ['dependency before its prerequisite',p=>{p.executionOrder.reverse();},'execution order precedes'],
  ['cyclic dependencies',p=>{p.workPackages[0].dependsOn=['C03'];},'dependency cycle'],
  ['stale source owner',p=>{p.workPackages[0].sourcePaths=['internal/not-an-owner.go'];},'missing source'],
  ['false acceptance',p=>{p.workPackages[0].status='accepted';},'unproven KPI'],
  ['unresolved gate at acceptance',p=>{p.workPackages[0].status='accepted';},'unresolved readiness gate'],
  ['gate resolved without evidence',p=>{p.readinessGates[0].status='resolved';},'resolved without evidence'],
  ['duplicate readiness gate',p=>{p.readinessGates[1]=structuredClone(p.readinessGates[0]);},'unique readiness gates'],
  ['weakened verification gates',p=>{p.requiredGates=['review'];},'complete verification contract'],
  ['reassigned readiness owner',p=>{p.readinessGates[0].owner='C12';},'readiness owner'],
  ['missing declared dependency',p=>{p.workPackages[2].dependsOn=[];},'dependencies differ'],
  ['unmeasured pass',p=>{p.workPackages[0].kpis[0].result='pass';},'needs an observation'],
  ['observation without evidence',p=>{p.workPackages[0].kpis[0].result='pass';p.workPackages[0].kpis[0].observation='All values matched';},'needs evidence'],
];
for(const [name,mutate,expected]of cases)test(name,()=>{
  const candidate=structuredClone(plan);mutate(candidate);
  assert.ok(validatePlan(candidate).some(error=>error.includes(expected)),`expected diagnostic: ${expected}`);
});
