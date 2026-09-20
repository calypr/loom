import { createHash } from 'node:crypto';
import { existsSync, readFileSync, realpathSync, statSync } from 'node:fs';
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const ledgerPath = 'docs/product/ml-dataframer/execution.json';
const specificationPath = 'docs/product/ML_DATAFRAMER_DELIVERY_PLAN.md';
const acceptancePath = 'docs/product/ml-dataframer/ACCEPTANCE.md';
const archiveManifestPath = 'docs/product/history/20260919-superseded/manifest.json';
const packageIds = ['S01', 'S02', 'S03', 'S04', 'S05'];
const requiredGates = ['focused_tests', 'live_dom', 'literal_output', 'negative_cases', 'performance', 'review'];
const statuses = new Set(['planned', 'in_progress', 'blocked', 'accepted']);
const kpiResults = new Set(['unmeasured', 'pass', 'fail']);

export function validatePlan(plan, repositoryRoot = root) {
  const errors = [];
  const check = (condition, message) => { if (!condition) errors.push(message); };
  const isRecord = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
  const sameItems = (actual, expected) => Array.isArray(actual)
    && actual.length === expected.length
    && new Set(actual).size === actual.length
    && expected.every((value) => actual.includes(value));
  const sameOrder = (actual, expected) => Array.isArray(actual)
    && actual.length === expected.length
    && actual.every((value, index) => value === expected[index]);

  if (!isRecord(plan)) return ['plan must be a JSON object'];

  let repoRoot;
  try {
    repoRoot = realpathSync(resolve(repositoryRoot));
  } catch {
    return ['repository root must exist'];
  }

  const safePath = (value, { mustExist = true, file = true } = {}) => {
    if (typeof value !== 'string' || value.length === 0 || value.includes('\0') || value.includes('\\')) return false;
    if (isAbsolute(value) || /^[A-Za-z]:/.test(value) || value.split('/').includes('..')) return false;
    const target = resolve(repoRoot, value);
    const lexical = relative(repoRoot, target);
    if (isAbsolute(lexical) || lexical === '..' || lexical.startsWith(`..${sep}`)) return false;

    let existing = target;
    while (!existsSync(existing)) {
      if (mustExist) return false;
      const parent = dirname(existing);
      if (parent === existing) return false;
      existing = parent;
    }
    try {
      const actual = realpathSync(existing);
      const resolvedPath = relative(repoRoot, actual);
      if (isAbsolute(resolvedPath) || resolvedPath === '..' || resolvedPath.startsWith(`..${sep}`)) return false;
      return !file || (existing === target && statSync(actual).isFile());
    } catch {
      return false;
    }
  };

  const expectedPaths = {
    specification: specificationPath,
    acceptance: acceptancePath,
    archiveManifest: archiveManifestPath,
  };
  let documentsAvailable = true;
  for (const [field, expectedPath] of Object.entries(expectedPaths)) {
    check(plan[field] === expectedPath, `${field} must be ${expectedPath}`);
    const valid = safePath(plan[field]);
    check(valid, `${field} must reference an existing safe repository file`);
    documentsAvailable &&= valid;
  }

  if (!documentsAvailable) return errors;

  let specification;
  let acceptance;
  let manifestText;
  try {
    specification = readFileSync(resolve(repoRoot, specificationPath), 'utf8');
    acceptance = readFileSync(resolve(repoRoot, acceptancePath), 'utf8');
    manifestText = readFileSync(resolve(repoRoot, archiveManifestPath), 'utf8');
  } catch (error) {
    return [`could not read plan documents: ${error.message}`];
  }

  check(plan.schemaVersion === 2, 'schemaVersion must be 2');
  check(plan.planRevision === 4, 'planRevision must be 4');
  check(plan.status === 'planned', 'status must be planned');
  check(isRecord(plan.baseline), 'baseline must be an object');
  const baseline = isRecord(plan.baseline) ? plan.baseline : {};
  check(baseline.branch === 'arch/integration', 'baseline branch must be arch/integration');
  check(/^[a-f0-9]{40}$/.test(baseline.sha ?? ''), 'baseline SHA must be 40 hexadecimal characters');
  check(baseline.date === '2026-09-19', 'baseline date must be 2026-09-19');
  check(baseline.requiresCheckpoint === true, 'baseline requiresCheckpoint must be true');
  check(sameOrder(plan.requiredGates, requiredGates), 'requiredGates must match the six required gates exactly');
  check(sameOrder(plan.executionOrder, packageIds), 'executionOrder must be S01, S02, S03, S04, S05');

  let manifest;
  try {
    manifest = JSON.parse(manifestText);
  } catch (error) {
    errors.push(`archive manifest is invalid JSON: ${error.message}`);
  }
  if (isRecord(manifest)) {
    check(manifest.schemaVersion === 1, 'archive manifest schemaVersion must be 1');
    check(manifest.status === 'superseded', 'archive manifest status must be superseded');
    check(manifest.replacement === specificationPath, 'archive manifest replacement must name the new delivery plan');
    check(Array.isArray(manifest.files) && manifest.files.length > 0, 'archive manifest files must be a non-empty array');
    const originals = new Set();
    const archived = new Set();
    for (const entry of Array.isArray(manifest.files) ? manifest.files : []) {
      if (!isRecord(entry)) {
        errors.push('archive manifest contains a malformed file entry');
        continue;
      }
      const originalSafe = safePath(entry.original, {mustExist: false, file: false});
      const archivedSafe = safePath(entry.archived);
      check(originalSafe, `archive manifest original path is unsafe: ${String(entry.original)}`);
      check(archivedSafe, `archive manifest archived path is unsafe or missing: ${String(entry.archived)}`);
      check(typeof entry.sha256 === 'string' && /^[a-f0-9]{64}$/.test(entry.sha256), `archive manifest has an invalid SHA-256 for ${String(entry.archived)}`);
      check(!originals.has(entry.original), `archive manifest repeats original path ${String(entry.original)}`);
      check(!archived.has(entry.archived), `archive manifest repeats archived path ${String(entry.archived)}`);
      if (typeof entry.original === 'string') originals.add(entry.original);
      if (typeof entry.archived === 'string') archived.add(entry.archived);
      if (archivedSafe && typeof entry.sha256 === 'string' && /^[a-f0-9]{64}$/.test(entry.sha256)) {
        try {
          const actualHash = createHash('sha256').update(readFileSync(resolve(repoRoot, entry.archived))).digest('hex');
          check(actualHash === entry.sha256, `archive manifest SHA-256 does not match ${entry.archived}`);
        } catch (error) {
          errors.push(`could not verify archived bytes for ${entry.archived}: ${error.message}`);
        }
      }
    }
  } else errors.push('archive manifest must be a JSON object');

  const packages = Array.isArray(plan.workPackages) ? plan.workPackages : [];
  check(Array.isArray(plan.workPackages) && packages.length === packageIds.length, 'workPackages must contain exactly five packages');
  const byId = new Map();
  for (const workPackage of packages) {
    if (!isRecord(workPackage)) {
      errors.push('workPackages contains a malformed package');
      continue;
    }
    if (typeof workPackage.id !== 'string' || !packageIds.includes(workPackage.id)) {
      errors.push(`unknown package ID: ${String(workPackage.id)}`);
      continue;
    }
    if (byId.has(workPackage.id)) errors.push(`duplicate package ID: ${workPackage.id}`);
    else byId.set(workPackage.id, workPackage);
  }
  for (const id of packageIds) check(byId.has(id), `missing package ${id}`);

  const headingMatches = [...specification.matchAll(/^## ([A-Z]\d{2}): (.+)$/gm)];
  const headingIds = headingMatches.map((match) => match[1]);
  check(sameItems(headingIds, packageIds), 'specification must contain each S01-S05 heading exactly once');
  const sections = new Map();
  for (const match of headingMatches) {
    const start = match.index + match[0].length;
    const rest = specification.slice(start);
    const nextHeading = rest.search(/^## /m);
    sections.set(match[1], {
      title: match[2].trim(),
      text: nextHeading < 0 ? rest : rest.slice(0, nextHeading),
    });
  }

  const allTasks = new Set();
  const allKpis = new Set();
  const allJourneys = new Set();
  const orderIndex = new Map(packageIds.map((id, index) => [id, index]));
  for (const id of packageIds) {
    const workPackage = byId.get(id);
    if (!workPackage) continue;
    const section = sections.get(id);
    check(typeof workPackage.title === 'string' && workPackage.title.trim().length > 0, `${id}: title must be non-empty`);
    if (section) check(section.title === workPackage.title, `${id}: title differs from specification heading`);
    check(statuses.has(workPackage.status), `${id}: invalid status`);

    const dependencyLines = section ? [...section.text.matchAll(/^Depends on: (.+)\.$/gm)] : [];
    check(dependencyLines.length === 1, `${id}: specification must have one explicit Depends on line`);
    let declaredDependencies = null;
    if (dependencyLines.length === 1) {
      const declaration = dependencyLines[0][1];
      if (declaration === 'none') declaredDependencies = [];
      else {
        declaredDependencies = declaration.split(/,\s*/);
        check(declaredDependencies.every((dependency) => /^S\d{2}$/.test(dependency)), `${id}: malformed specification dependencies`);
        check(new Set(declaredDependencies).size === declaredDependencies.length, `${id}: duplicate specification dependency`);
      }
    }
    check(Array.isArray(workPackage.dependsOn), `${id}: dependsOn must be an explicit array`);
    if (declaredDependencies && Array.isArray(workPackage.dependsOn)) {
      check(sameOrder(workPackage.dependsOn, declaredDependencies), `${id}: ledger dependencies differ from specification`);
    }

    check(Array.isArray(workPackage.sourcePaths) && workPackage.sourcePaths.length > 0, `${id}: sourcePaths must be a non-empty array`);
    for (const path of Array.isArray(workPackage.sourcePaths) ? workPackage.sourcePaths : []) {
      check(safePath(path), `${id}: missing or unsafe source ${String(path)}`);
    }
    check(Array.isArray(workPackage.evidence), `${id}: evidence must be an array`);
    for (const path of Array.isArray(workPackage.evidence) ? workPackage.evidence : []) {
      check(safePath(path), `${id}: missing or unsafe evidence ${String(path)}`);
    }
    check(Array.isArray(workPackage.journeys) && workPackage.journeys.length > 0, `${id}: journeys must be a non-empty array`);
    if (Array.isArray(workPackage.journeys)) {
      check(new Set(workPackage.journeys).size === workPackage.journeys.length, `${id}: duplicate journey`);
      for (const journey of workPackage.journeys) {
        check(typeof journey === 'string' && /^J\d{2}$/.test(journey), `${id}: invalid journey ${String(journey)}`);
        if (typeof journey === 'string') allJourneys.add(journey);
      }
    }

    const expectedTaskIds = Array.from({length: 4}, (_, index) => `${id}-${String(index + 1).padStart(2, '0')}`);
    const tasks = Array.isArray(workPackage.tasks) ? workPackage.tasks : [];
    check(Array.isArray(workPackage.tasks) && tasks.length === 4, `${id}: expected four implementation tasks`);
    const packageTaskIds = [];
    for (const task of tasks) {
      if (!isRecord(task)) {
        errors.push(`${id}: malformed task`);
        continue;
      }
      check(typeof task.id === 'string' && expectedTaskIds.includes(task.id), `${id}: unknown task ID ${String(task.id)}`);
      check(!allTasks.has(task.id), `${id}: duplicate task ID ${String(task.id)}`);
      if (typeof task.id === 'string') {
        allTasks.add(task.id);
        packageTaskIds.push(task.id);
      }
      check(statuses.has(task.status), `${String(task.id)}: invalid status`);
    }
    check(sameItems(packageTaskIds, expectedTaskIds), `${id}: tasks must be exactly ${expectedTaskIds.join(', ')}`);
    if (section) {
      const sectionTaskIds = [...section.text.matchAll(/^- ([A-Za-z][A-Za-z0-9_-]*-[A-Za-z0-9_-]+)\. /gm)].map((match) => match[1]);
      check(sameItems(sectionTaskIds, expectedTaskIds), `${id}: specification tasks must be exactly ${expectedTaskIds.join(', ')}`);
    }

    const expectedKpiIds = ['a', 'b', 'c'].map((suffix) => `K-${id}-${suffix}`);
    const kpis = Array.isArray(workPackage.kpis) ? workPackage.kpis : [];
    check(Array.isArray(workPackage.kpis) && kpis.length === 3, `${id}: expected three KPIs`);
    const packageKpiIds = [];
    for (const kpi of kpis) {
      if (!isRecord(kpi)) {
        errors.push(`${id}: malformed KPI`);
        continue;
      }
      check(typeof kpi.id === 'string' && expectedKpiIds.includes(kpi.id), `${id}: unknown KPI ID ${String(kpi.id)}`);
      check(!allKpis.has(kpi.id), `${id}: duplicate KPI ID ${String(kpi.id)}`);
      if (typeof kpi.id === 'string') {
        allKpis.add(kpi.id);
        packageKpiIds.push(kpi.id);
      }
      check(kpiResults.has(kpi.result), `${String(kpi.id)}: invalid result`);
      if (Object.hasOwn(kpi, 'evidence')) check(Array.isArray(kpi.evidence), `${String(kpi.id)}: evidence must be an array`);
      for (const path of Array.isArray(kpi.evidence) ? kpi.evidence : []) {
        check(safePath(path), `${String(kpi.id)}: missing or unsafe evidence ${String(path)}`);
      }
      if (kpi.result === 'pass' || kpi.result === 'fail') {
        check(typeof kpi.observation === 'string' && kpi.observation.trim().length > 0, `${String(kpi.id)}: measured result needs an observation`);
        check(Array.isArray(kpi.evidence) && kpi.evidence.length > 0, `${String(kpi.id)}: measured result needs evidence`);
      }
    }
    check(sameItems(packageKpiIds, expectedKpiIds), `${id}: KPIs must be exactly ${expectedKpiIds.join(', ')}`);
    if (section) {
      const sectionKpiIds = [...section.text.matchAll(/^- (K-[A-Za-z0-9_-]+): /gm)].map((match) => match[1]);
      check(sameItems(sectionKpiIds, expectedKpiIds), `${id}: specification KPIs must be exactly ${expectedKpiIds.join(', ')}`);
    }

    check(workPackage.verification === null || isRecord(workPackage.verification), `${id}: verification must be null or an object`);
    const verification = isRecord(workPackage.verification) ? workPackage.verification : {};
    if (Object.hasOwn(verification, 'sha')) {
      check(/^[a-f0-9]{40}$/.test(verification.sha), `${id}: verification SHA must be 40 hexadecimal characters`);
    }
    if (Object.hasOwn(verification, 'gates')) {
      check(isRecord(verification.gates), `${id}: verification gates must be an object`);
      if (isRecord(verification.gates)) {
        for (const gate of Object.keys(verification.gates)) check(requiredGates.includes(gate), `${id}: unknown verification gate ${gate}`);
      }
    }

    for (const dependency of Array.isArray(workPackage.dependsOn) ? workPackage.dependsOn : []) {
      check(packageIds.includes(dependency) && dependency !== id, `${id}: invalid dependency ${String(dependency)}`);
      if (orderIndex.has(dependency)) check(orderIndex.get(dependency) < orderIndex.get(id), `${id}: execution order precedes dependency ${dependency}`);
    }

    if (workPackage.status === 'accepted') {
      check(tasks.length === 4 && tasks.every((task) => isRecord(task) && task.status === 'accepted'), `${id}: unfinished implementation tasks`);
      check(kpis.length === 3 && kpis.every((kpi) => isRecord(kpi) && kpi.result === 'pass'), `${id}: unproven KPI`);
      check(Array.isArray(workPackage.evidence) && workPackage.evidence.length > 0, `${id}: accepted without evidence`);
      check(/^[a-f0-9]{40}$/.test(verification.sha ?? ''), `${id}: verification SHA missing`);
      for (const gate of requiredGates) check(verification.gates?.[gate] === 'pass', `${id}: gate ${gate} not passed`);
      for (const dependency of Array.isArray(workPackage.dependsOn) ? workPackage.dependsOn : []) {
        check(byId.get(dependency)?.status === 'accepted', `${id}: unaccepted dependency ${dependency}`);
      }
    }
  }

  const visiting = new Set();
  const visited = new Set();
  const visit = (id) => {
    if (visiting.has(id)) { errors.push(`dependency cycle at ${id}`); return; }
    if (visited.has(id)) return;
    visiting.add(id);
    for (const dependency of byId.get(id)?.dependsOn ?? []) if (packageIds.includes(dependency)) visit(dependency);
    visiting.delete(id);
    visited.add(id);
  };
  for (const id of packageIds) if (byId.has(id)) visit(id);

  const documentTasks = [...specification.matchAll(/^- ([A-Za-z][A-Za-z0-9_-]*-[A-Za-z0-9_-]+)\. /gm)].map((match) => match[1]);
  const expectedTaskIds = packageIds.flatMap((id) => Array.from({length: 4}, (_, index) => `${id}-${String(index + 1).padStart(2, '0')}`));
  check(allTasks.size === 20 && sameItems([...allTasks], expectedTaskIds), 'ledger must contain exactly 20 unique tasks');
  check(sameItems(documentTasks, expectedTaskIds), 'specification and ledger task counts or IDs differ');

  const documentKpis = [...specification.matchAll(/^- (K-[A-Za-z0-9_-]+): /gm)].map((match) => match[1]);
  const expectedKpiIds = packageIds.flatMap((id) => ['a', 'b', 'c'].map((suffix) => `K-${id}-${suffix}`));
  check(allKpis.size === 15 && sameItems([...allKpis], expectedKpiIds), 'ledger must contain exactly 15 unique KPIs');
  check(sameItems(documentKpis, expectedKpiIds), 'specification and ledger KPI counts or IDs differ');

  const acceptanceJourneys = [...acceptance.matchAll(/^\|\s*(J\d{2})\s*\|/gm)].map((match) => match[1]);
  check(sameItems(acceptanceJourneys, [...allJourneys]), 'acceptance journey rows must match ledger journeys exactly');
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
      for (const workPackage of plan.workPackages) for (const kpi of workPackage.kpis) counts[kpi.result]++;
      process.stdout.write(`PLAN_VALID: 5 packages, 20 tasks; recorded KPIs ${counts.unmeasured} unmeasured, ${counts.pass} pass, ${counts.fail} fail. Runtime evidence is not certified by this validator.\n`);
    }
  } catch (error) {
    process.stderr.write(`PLAN_INVALID: ${error.message}\n`);
    process.exitCode = 1;
  }
}
