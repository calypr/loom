import { existsSync, readFileSync, statSync } from 'node:fs';
import { dirname, isAbsolute, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const ledgerPath = 'docs/product/ml-dataframer/execution.json';

// This validates planning records, not the truth of runtime evidence.
export function validatePlan(plan, repositoryRoot = root) {
  const errors = [];
  const check = (condition, message) => { if (!condition) errors.push(message); };
  const localFile = (path) => {
    if (typeof path !== 'string' || !path) return false;
    const rel = relative(repositoryRoot, resolve(repositoryRoot, path));
    return !isAbsolute(path) && !rel.startsWith('..') && existsSync(resolve(repositoryRoot, path)) && statSync(resolve(repositoryRoot, path)).isFile();
  };
  check(plan.schemaVersion === 1, 'schemaVersion must be 1');
  check(plan.planRevision === 3, 'planRevision must be 3 for the detailed code-first plan');
  check(/^[a-f0-9]{40}$/.test(plan.baseline?.sha ?? ''), 'baseline SHA must be explicit');
  for (const field of ['specification', 'architecture', 'acceptance', 'contracts', 'runbook', 'prototype']) {
    check(localFile(plan[field]), `${field} must reference an existing repository file`);
  }
  if (errors.length) return errors;
  const specification = readFileSync(resolve(repositoryRoot, plan.specification), 'utf8');
  const acceptance = readFileSync(resolve(repositoryRoot, plan.acceptance), 'utf8');
  const runbook = readFileSync(resolve(repositoryRoot, plan.runbook), 'utf8');
  const contracts = readFileSync(resolve(repositoryRoot, plan.contracts), 'utf8');
  const packages = plan.workPackages ?? [];
  const ids = new Set(packages.map((wp) => wp.id));
  check(packages.length === 12 && ids.size === 12, 'expected 12 unique C packages');
  const order = plan.executionOrder ?? [];
  check(order.length === ids.size && new Set(order).size === ids.size && order.every((id) => ids.has(id)), 'execution order must contain each package once');
  const expectedGates = ['focused_tests', 'live_dom', 'literal_output', 'negative_cases', 'performance', 'review'];
  const sameSet = (actual, expected) => Array.isArray(actual) && actual.length === expected.length && new Set(actual).size === actual.length && expected.every((value) => actual.includes(value));
  check(sameSet(plan.requiredGates, expectedGates), 'required gates must match the complete verification contract');
  const allTasks = new Set();
  const allKPIs = new Set();
  const states = ['planned', 'in_progress', 'blocked', 'accepted'];
  const readiness = plan.readinessGates ?? [];
  const readinessOwners = {R1: 'C01', R2: 'C01', R3: 'C03', R4: 'C06', R5: 'C09', R6: 'C11'};
  check(readiness.length === 6 && new Set(readiness.map((gate) => gate.id)).size === 6, 'expected six unique readiness gates');
  for (const gate of readiness) {
    check(/^R[1-6]$/.test(gate.id) && ids.has(gate.owner), `invalid readiness gate ${gate.id}`);
    check(readinessOwners[gate.id] === gate.owner, `${gate.id}: readiness owner does not match the contract`);
    check(contracts.includes(`| ${gate.id} `), `${gate.id}: missing contract readiness definition`);
    check(['open', 'resolved'].includes(gate.status), `${gate.id}: invalid readiness status`);
    if (gate.status === 'resolved') check(gate.evidence?.length > 0 && gate.evidence.every(localFile), `${gate.id}: resolved without evidence`);
  }
  for (const wp of packages) {
    check(/^C(?:0[1-9]|1[0-2])$/.test(wp.id), `invalid package ID: ${wp.id}`);
    check(specification.includes(`## ${wp.id}: ${wp.title}`), `${wp.id}: heading missing or stale`);
    check(runbook.includes(`## ${wp.id} — `), `${wp.id}: detailed runbook section missing`);
    const section = specification.split(`## ${wp.id}: `)[1]?.split('\n## ')[0] ?? '';
    const declaration = section.match(/^Depends on: ([^.]+)\./m)?.[1];
    const declaredDependencies = declaration?.includes('all earlier packages')
      ? [...ids].filter((id) => id < wp.id)
      : declaration?.match(/C\d{2}/g) ?? [];
    check(Boolean(declaration) && sameSet(wp.dependsOn, declaredDependencies), `${wp.id}: ledger dependencies differ from specification`);
    check(states.includes(wp.status), `${wp.id}: invalid status`);
    check(/^M[1-4]$/.test(wp.milestone), `${wp.id}: invalid milestone`);
    check(wp.sourcePaths?.length > 0, `${wp.id}: source ownership missing`);
    for (const path of wp.sourcePaths ?? []) check(localFile(path), `${wp.id}: missing source ${path}`);
    for (const id of wp.dependsOn ?? []) {
      check(ids.has(id) && id !== wp.id, `${wp.id}: invalid dependency ${id}`);
      check(order.indexOf(id) >= 0 && order.indexOf(id) < order.indexOf(wp.id), `${wp.id}: execution order precedes dependency ${id}`);
    }
    check(wp.tasks?.length === 4, `${wp.id}: expected four implementation units`);
    for (const task of wp.tasks ?? []) {
      check(task.id.startsWith(`${wp.id}-`) && !allTasks.has(task.id), `${wp.id}: invalid/duplicate task ${task.id}`);
      allTasks.add(task.id);
      check(specification.includes(`- ${task.id}. `), `${task.id}: missing specification`);
      check(states.includes(task.status), `${task.id}: invalid status`);
    }
    check(wp.kpis?.length >= 3, `${wp.id}: measurable KPIs missing`);
    for (const kpi of wp.kpis ?? []) {
      check(kpi.id.startsWith(`K-${wp.id}-`) && !allKPIs.has(kpi.id), `${wp.id}: invalid/duplicate KPI ${kpi.id}`);
      allKPIs.add(kpi.id);
      check(specification.includes(`- ${kpi.id}: `), `${kpi.id}: missing target`);
      check(['unmeasured', 'pass', 'fail'].includes(kpi.result), `${kpi.id}: invalid result`);
      if (kpi.result === 'pass' || kpi.result === 'fail') {
        check(typeof kpi.observation === 'string' && kpi.observation.trim().length > 0, `${kpi.id}: measured result needs an observation`);
        check(kpi.evidence?.length > 0 && kpi.evidence.every(localFile), `${kpi.id}: measured result needs evidence`);
      }
    }
    check(wp.journeys?.length > 0, `${wp.id}: user journey missing`);
    for (const journey of wp.journeys ?? []) check(acceptance.includes(`| ${journey} |`), `${wp.id}: missing journey ${journey}`);
    for (const path of wp.evidence ?? []) check(localFile(path), `${wp.id}: missing evidence ${path}`);
    if (wp.status === 'accepted') {
      for (const gate of readiness.filter((item) => item.owner === wp.id)) check(gate.status === 'resolved', `${wp.id}: unresolved readiness gate ${gate.id}`);
      check(wp.tasks.every((task) => task.status === 'accepted'), `${wp.id}: unfinished implementation units`);
      check(wp.kpis.every((kpi) => kpi.result === 'pass'), `${wp.id}: unproven KPI`);
      check(wp.evidence?.length > 0, `${wp.id}: accepted without evidence`);
      check(/^[a-f0-9]{40}$/.test(wp.verification?.sha ?? ''), `${wp.id}: verified SHA missing`);
      for (const gate of plan.requiredGates ?? []) check(wp.verification?.gates?.[gate] === 'pass', `${wp.id}: gate ${gate} not passed`);
      for (const id of wp.dependsOn ?? []) check(packages.find((item) => item.id === id)?.status === 'accepted', `${wp.id}: unaccepted dependency ${id}`);
    }
  }
  const visiting = new Set();
  const visited = new Set();
  const visit = (id) => {
    if (visiting.has(id)) { errors.push(`dependency cycle at ${id}`); return; }
    if (visited.has(id)) return;
    visiting.add(id);
    for (const dependency of packages.find((wp) => wp.id === id)?.dependsOn ?? []) visit(dependency);
    visiting.delete(id);
    visited.add(id);
  };
  for (const id of ids) visit(id);
  check(allTasks.size === 48, 'expected 48 unique implementation units');
  const specifiedTasks = specification.match(/^- C\d{2}-\d{2}\. /gm) ?? [];
  const specifiedKPIs = specification.match(/^- K-C\d{2}-[a-z]: /gm) ?? [];
  check(specifiedTasks.length === allTasks.size, 'specification/ledger task counts differ');
  check(specifiedKPIs.length === allKPIs.size, 'specification/ledger KPI counts differ');
  return errors;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    const plan = JSON.parse(readFileSync(resolve(root, ledgerPath), 'utf8'));
    const errors = validatePlan(plan);
    if (errors.length) {
      process.stderr.write(`${errors.join('\n')}\n`);
      process.exitCode = 1;
    } else {
      const counts = {unmeasured: 0, pass: 0, fail: 0};
      for (const wp of plan.workPackages) for (const kpi of wp.kpis) counts[kpi.result]++;
      process.stdout.write(`PLAN_VALID: 12 packages, 48 implementation units; recorded KPIs ${counts.unmeasured} unmeasured, ${counts.pass} pass, ${counts.fail} fail. Runtime evidence is not certified by this validator.\n`);
    }
  } catch (error) {
    process.stderr.write(`PLAN_INVALID: ${error.message}\n`);
    process.exitCode = 1;
  }
}
