import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { nodeTestInventory } from '../ci/list-node-tests.mjs';
import { profiles as arangoProfiles } from '../run-arango-integration.mjs';
import { apiChecks } from './formal-api.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

function workflowJobs(source, file) {
  const starts = [...source.matchAll(/^  ([a-z][\w-]*):\s*$/gm)];
  return starts.map((match, index) => {
    const body = source.slice(match.index, starts[index + 1]?.index ?? source.length);
    const commands = [];
    let cwd = '.', active;
    for (const line of body.split('\n')) {
      if (/^      - /.test(line)) { cwd = '.'; active = undefined; }
      const directory = /^        working-directory: (.+)$/.exec(line);
      if (directory) cwd = directory[1];
      const run = /^        run: (.*)$/.exec(line);
      if (run) {
        active = { cwd, command: /^[|>]/.test(run[1]) ? '' : run[1], folded: run[1].startsWith('>') };
        commands.push(active);
      } else if (active && /^          \S/.test(line)) active.command += `${active.command ? active.folded ? ' ' : '\n' : ''}${line.slice(10)}`;
      else if (active && line && !/^          /.test(line)) active = undefined;
    }
    return { id: match[1], workflow: file, commands, body };
  });
}
function filesBelow(projectRoot, relative, predicate) {
  const result = [];
  const visit = directory => {
    if (!existsSync(path.join(projectRoot, directory))) return;
    for (const entry of readdirSync(path.join(projectRoot, directory), { withFileTypes: true })) {
      const file = directory ? `${directory}/${entry.name}` : entry.name;
      if (entry.isDirectory() && !['node_modules', '.git', '.gocache', 'dist', 'vendor'].includes(entry.name)) visit(file);
      else if (entry.isFile() && predicate(file)) result.push(file);
    }
  };
  visit(relative);
  return result.sort();
}
const packageMatches = (file, pattern) => {
  const directory = path.posix.dirname(file);
  return pattern.endsWith('/...') ? directory === pattern.slice(0, -4) || directory.startsWith(`${pattern.slice(0, -4)}/`) : directory === pattern;
};

export async function verificationInventory(projectRoot = root) {
  const sources = {};
  const jobs = [];
  for (const file of ['.github/workflows/tests.yaml', '.github/workflows/builder-formal.yml']) {
    const source = readFileSync(path.join(projectRoot, file), 'utf8');
    sources[file] = createHash('sha256').update(source).digest('hex');
    jobs.push(...workflowJobs(source, file));
  }
  const main = id => jobs.find(job => job.id === id && job.workflow.endsWith('/tests.yaml'));
  const node = await nodeTestInventory(projectRoot);
  const ui = ['src', 'testSupport'].flatMap(directory => filesBelow(projectRoot, `ui/packages/loom-ui/${directory}`, file => /\.(test|spec)\.[cm]?[jt]sx?$/.test(file)));
  const python = filesBelow(projectRoot, 'scripts', file => /\/test_[^/]+\.py$/.test(file));
  const pythonNames = [...main('python-unit').body.matchAll(/^          - (test_\w+)$/gm)].map(match => match[1]);
  const pythonRoute = file => {
    if ((pythonNames.includes(path.basename(file, '.py')) || main('python-unit').body.includes('unittest discover -s scripts/tests')) && file.startsWith('scripts/tests/')) return 'python-unit';
    const module = file.replaceAll('/', '.').replace(/\.py$/, '');
    return jobs.find(job => job.commands.some(run => run.command.includes(`unittest ${module}`)))?.id;
  };
  const pythonIncluded = python.filter(pythonRoute);
  const shell = filesBelow(projectRoot, 'scripts', file => /\/test_[^/]+\.sh$/.test(file));
  const shellIncluded = shell.filter(file => main('shell-unit').body.includes(file));
  const go = ['internal', 'cmd', 'generated', 'conformance', 'scripts'].flatMap(directory => filesBelow(projectRoot, directory, file => file.endsWith('_test.go')));
  go.push(...readdirSync(projectRoot).filter(file => file.endsWith('_test.go')));
  const packagePatterns = [...main('go-unit').body.matchAll(/\.\/[A-Za-z0-9_./-]+/g)].map(match => match[0].slice(2));
  const clickhousePackages = [...main('go-clickhouse').body.matchAll(/\.\/[A-Za-z0-9_./-]+/g)].map(match => match[0].slice(2));
  const goRoutes = go.map(file => ({ file, route: clickhousePackages.some(pattern => packageMatches(file, pattern)) ? 'go-clickhouse'
    : (path.posix.dirname(file) === '.' && /go list \./.test(main('go-unit').body)) || packagePatterns.some(pattern => packageMatches(file, pattern)) ? 'go-unit'
      : main('contracts').body.includes(`./${file}`) ? 'contracts' : undefined }));
  const extraGoJobs = jobs.filter(job => ['go-arango', 'go-publication-rows'].includes(job.id));
  for (const row of goRoutes) {
    const names = [...readFileSync(path.join(projectRoot, row.file), 'utf8').matchAll(/^func (Test\w+)\(/gm)].map(match => match[1]);
    row.additionalRoutes = extraGoJobs.filter(job => {
      if (job.id === 'go-arango') return arangoProfiles.some(profile => packageMatches(row.file, profile.package.slice(2)) && names.some(name => (!profile.run || name === profile.run) && (!profile.skip || !new RegExp(profile.skip).test(name))));
      if (job.id === 'go-publication-rows') return apiChecks.filter(check => check.requiredEnvironment?.includes('LOOM_CLICKHOUSE_URL')).some(check => check.tests.some(name => names.includes(name)) && (job.body.includes(`--feature ${check.id}`) || check.id === 'append-rows' && job.body.includes('scripts/lib/formal-append-rows.mjs')));
      return [...job.body.matchAll(/\.\/[A-Za-z0-9_./-]+/g)].some(match => packageMatches(row.file, match[0].slice(2)));
    }).map(job => job.id);
  }
  const makefile = readFileSync(path.join(projectRoot, 'Makefile'), 'utf8');
  sources.Makefile = createHash('sha256').update(makefile).digest('hex');
  const makeTarget = (target, seen = new Set()) => {
    if (seen.has(target)) return { target, status: 'cyclic-prerequisite' };
    const definition = new RegExp(`^${target}:([^\\n]*)\\n((?:\\t[^\\n]*\\n|\\n)*)`, 'm').exec(makefile);
    if (definition?.[2].includes('\t')) return { target, status: 'recipe-present-not-executed', recipe: definition[2].trim() };
    const dependencies = definition?.[1].trim().split(/\s+/).filter(Boolean) || [];
    const next = new Set([...seen, target]);
    if (dependencies.length && dependencies.every(dependency => /present-not-executed$/.test(makeTarget(dependency, next).status)))
      return { target, status: 'alias-present-not-executed', dependencies };
    return { target, status: dependencies.length ? 'blocked-prerequisite' : 'no-recipe', dependencies };
  };
  const makeTargets = ['verify-fast', 'verify-api', 'verify-formal', 'verify-inventory', 'verify-full', 'verify-current', 'dev-doctor', 'test-scripts'].map(target => makeTarget(target));
  const policy = 'Inventory of existing execution routes, not evidence that checks passed. CI jobs retain their declared service/tool prerequisites. Only declared tests and static dependencies are classified.';
  return {
    schemaVersion: 1, status: 'inventory-only', policy, sources,
    jobs: jobs.map(({ body, ...job }) => job),
    groups: [
      { id: 'ui', boundary: 'Production React/functions; jsdom and mocked external systems, not browser acceptance', route: 'ui-unit', files: ui, count: ui.length, featureMap: '.codex/skills/builder-interaction-formal-verification/features/README.md' },
      { id: 'node', boundary: 'Native Node tests and their available source closure', route: 'node-unit', count: node.included.length, included: node.included, excluded: node.excluded, policy: node.policy },
      { id: 'python', boundary: 'Python script and workflow contracts', route: 'python-unit', count: pythonIncluded.length, included: pythonIncluded, files: python.map(file => ({ file, route: pythonRoute(file) })), excluded: python.filter(file => !pythonIncluded.includes(file)).map(file => ({ file, reason: 'no-matching-declared-Python-CI-route' })) },
      { id: 'shell', boundary: 'Shell cleanup contracts', route: 'shell-unit', count: shellIncluded.length, included: shellIncluded, excluded: shell.filter(file => !shellIncluded.includes(file)).map(file => ({ file, reason: 'not-in-CI-shell-job' })) },
      { id: 'go', boundary: 'Go package tests; ClickHouse integration requires the CI service; in-memory HTTP fixtures do not prove disk durability', count: goRoutes.filter(test => test.route).length, files: goRoutes, excluded: goRoutes.filter(test => !test.route).map(({ file }) => ({ file, reason: 'no-matching-declared-Go-CI-route' })) },
      { id: 'population-attachment', boundary: '482 finite public Search/Apply cases; controlled capabilities/store; not TLA, arbitrary routes or database atomicity', route: 'audit', command: 'node scripts/lib/formal-population-attachment.mjs --generate /tmp/loom-attachment-check', conditionalGoTest: 'TestPopulationAttachmentGeneratedReplay', featureMap: '.codex/skills/builder-interaction-formal-verification/SKILL.md' },
      { id: 'journeys', boundary: '11 fixed editor/service schedules, five Apply/fresh reads, one Cancel and one saved Group witness through two remounts; no full TLA, browser reload, transport or durable storage claim', route: 'audit', command: 'node scripts/verify-builder-formal-core.mjs --phase journey --feature operation-history-routine', conditionalGoTest: 'TestConstructionProposalOperationHistoryTraces', featureMap: '.codex/skills/builder-interaction-formal-verification/features/drafts.md' },
      { id: 'derived-policy', boundary: '400 finite source/literal arithmetic cases against actual Arango, plus five source controls; eight guard-order conformance inputs; not TLA or arbitrary numeric proof', route: 'go-arango', command: 'node scripts/lib/formal-derived-policy.mjs --out /tmp/loom-derived-policy-check', requiredEnvironment: ['LOOM_TEST_ARANGO_URL'], conditionalGoTest: 'TestDerivedArithmeticPoliciesAgainstArango', featureMap: '.codex/skills/builder-interaction-formal-verification/SKILL.md' },
      { id: 'append-rows', boundary: 'Fixed actual Arango/ClickHouse Group/Calculate/APPEND edit and restore witness with four source-fault controls; authored recipe fixtures, not public saves or a TLA bound', route: 'go-publication-rows', command: 'node scripts/lib/formal-append-rows.mjs --out /tmp/loom-append-rows-check', requiredEnvironment: ['LOOM_TEST_ARANGO_URL', 'LOOM_CLICKHOUSE_URL'], conditionalGoTest: 'TestCurrentDraftAppendRowsAgainstArangoClickHouse', featureMap: '.codex/skills/builder-interaction-formal-verification/SKILL.md' },
      { id: 'draft-transaction', boundary: 'One bounded Store.SaveDraft protocol replay with trusted transaction primitive; separate real Arango schedules through explicit draft-cas API profile', route: 'audit', command: 'node scripts/verify-builder-formal-core.mjs --model draft-transaction', conditionalGoTest: 'TestDraftTransactionModelReplay', featureMap: '.codex/skills/builder-interaction-formal-verification/SKILL.md' },
      { id: 'formal', boundary: 'Stated bounded TLA models and source mutation/replay probes, not full frontend refinement', route: 'audit', featureMap: '.codex/skills/builder-interaction-formal-verification/SKILL.md' },
    ], makeTargets,
  };
}
