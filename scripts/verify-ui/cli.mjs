import { createDevSession, assertOwnedDevSession, createFreshVerificationFixture, doctor } from '../loom-dev.mjs';
import { registry, getScenario } from './registry.mjs';
import { writeReport } from './report.mjs';
import { mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';

export const parseArgs = (argv) => {
  const args = { caseName: undefined, url: undefined, project: undefined, explorer: undefined, reportPath: undefined, help: false, list: false, reuseOwnedDataset: false };
  for (let index = 0; index < argv.length; index += 1) {
    const value = argv[index];
    if (value === '--help' || value === '-h') args.help = true;
    else if (value === '--list') args.list = true;
    else if (value === '--reuse-owned-dataset') args.reuseOwnedDataset = true;
    else if (['--case', '--url', '--project', '--explorer', '--report'].includes(value)) {
      const next = argv[index + 1];
      if (!next || next.startsWith('--')) throw new Error(value + ' requires a value');
      index += 1;
      if (value === '--case') args.caseName = next;
      if (value === '--url') args.url = next;
      if (value === '--project') args.project = next;
      if (value === '--explorer') args.explorer = next;
      if (value === '--report') args.reportPath = next;
    } else {
      throw new Error('unknown option: ' + value);
    }
  }
  return args;
};

export const usage = (id) =>
  'Usage: node scripts/verify-ui/' + getScenario(id).script + ' [--help] [--list] [--case NAME] [--reuse-owned-dataset] [--url URL --project PROJECT --explorer EXPLORER] [--report PATH]\n'
  + 'Without --url, the script validates the owned loopback Loom dev stack and seeds a fresh disposable fixture.\n'
  + '--reuse-owned-dataset is limited to migrated Builder authoring and read-only Builder load checks; it uses an already loaded, named, owned development generation.\n'
  + 'A custom URL requires project and explorer and runs read-only browser workflows.\n';

export const supportsOwnedDatasetReuse = (scenarioID, caseName) =>
  (scenarioID === 'builder-authoring' && ['authoring', 'suggestions', 'cohort-recode'].includes(caseName)) ||
  (scenarioID === 'builder-load' && ['list', 'state'].includes(caseName));

const runID = () => Date.now().toString(36) + '-' + Math.random().toString(16).slice(2, 9);

const namedSessionEnvironment = [
  'LOOM_DEV_SOURCE_ROOT',
  'LOOM_DEV_COMPOSE_PROJECT',
  'LOOM_DEV_API_PORT',
  'LOOM_DEV_UI_PORT',
  'LOOM_DEV_PROJECT',
];

export const assertNamedDevSessionEnvironment = (env = process.env) => {
  const missing = namedSessionEnvironment.filter((key) => !String(env[key] ?? '').trim());
  if (missing.length) throw new Error('set the named development target explicitly: ' + missing.join(', '));
};

export const createRunContext = async (args, scenario, { mutating = false } = {}) => {
  if (args.url) {
    if (!args.project || !args.explorer) throw new Error('--url requires explicit --project and --explorer');
    if (mutating) throw new Error('authoring workflows only run against the validated owned dev fixture');
    const url = new URL(args.url);
    if (!['http:', 'https:'].includes(url.protocol)) throw new Error('--url must use HTTP or HTTPS');
    const target = {
      kind: 'read-only-custom',
      uiUrl: url.toString(),
      apiUrl: url.origin,
      fixtureProject: args.project,
      fixtureGeneration: null,
      bootstrapExplorerId: args.explorer,
      artifacts: resolve('.artifacts/verify-ui'),
    };
    return { target, runID: runID(), custom: true, scenario, args };
  }

  assertNamedDevSessionEnvironment();
  const session = createDevSession();
  await assertOwnedDevSession(session);
  if (args.reuseOwnedDataset) {
    if (!supportsOwnedDatasetReuse(scenario.id, args.caseName)) {
      throw new Error('--reuse-owned-dataset is limited to builder-authoring --case authoring, suggestions, or cohort-recode, and read-only builder-load --case list or state');
    }
    const health = await doctor(session);
    if (health.api !== 200 || health.ui !== 200 || health.generation !== 200 ||
        health.builder !== 200 || !health.bootstrapExplorerId) {
      throw new Error('owned dataset is not fully loaded with a bootstrap Builder');
    }
    const id = runID();
    return {
      target: { ...session, bootstrapExplorerId: health.bootstrapExplorerId },
      runID: id,
      custom: false,
      scenario,
      args,
    };
  }
  const fixture = await createFreshVerificationFixture(session, runID());
  return {
    target: { ...fixture.target, bootstrapExplorerId: fixture.seed.bootstrapExplorerId },
    seed: fixture.seed,
    runID: fixture.target.fixtureProject.replace(/^loom_dev_verify_/, ''),
    custom: false,
    scenario,
    args,
  };
};

export const makeReportLocation = (context, scenario, caseName) => {
  const reportPath = context.args.reportPath
    ? resolve(context.args.reportPath) + (context.scenario.cases.length > 1 && caseName ? '.' + caseName : '')
    : resolve(context.target.artifacts, 'verify-ui', scenario + '-' + (caseName ?? 'default') + '-' + context.runID + '.json');
  const evidenceDirectory = dirname(reportPath) + '/' + scenario + '-' + (caseName ?? 'default') + '-' + context.runID;
  mkdirSync(evidenceDirectory, { recursive: true, mode: 0o700 });
  return { reportPath, evidenceDirectory };
};

export const writeRunReport = (path, report) => writeReport(path, report);

export const printList = () => {
  for (const scenario of registry) console.log(scenario.id + '\t' + scenario.workflow + '\t' + scenario.script);
};

export const validateScenarioCase = (scenario, caseName) => {
  if (!caseName) return;
  if (!scenario.cases.includes(caseName)) throw new Error('unknown case for ' + scenario.id + ': ' + caseName + ' (use --list)');
};

export const scenarioFor = (id) => getScenario(id);
