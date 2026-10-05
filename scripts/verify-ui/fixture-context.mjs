import { createDevSession, assertOwnedDevSession, createFreshVerificationFixture } from '../loom-dev.mjs';
import { getScenario } from './registry.mjs';
import { mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';

export { environmentForFixtureDir } from './fixture-environment.mjs';

const namedSessionEnvironment = [
  'LOOM_DEV_SOURCE_ROOT',
  'LOOM_DEV_COMPOSE_PROJECT',
  'LOOM_DEV_API_PORT',
  'LOOM_DEV_UI_PORT',
  'LOOM_DEV_PROJECT',
];

export const assertNamedDevSessionEnvironment = (env = process.env) => {
  const missing = namedSessionEnvironment.filter(key => !String(env[key] ?? '').trim());
  if (missing.length) throw new Error('set the named development target explicitly: ' + missing.join(', '));
};

export const createRunContext = async ({ caseName }, scenario, { env = process.env } = {}) => {
  assertNamedDevSessionEnvironment(env);
  const session = createDevSession(env);
  await assertOwnedDevSession(session);
  const id = Date.now().toString(36) + '-' + Math.random().toString(16).slice(2, 9);
  const fixture = await createFreshVerificationFixture(session, id);
  return {
    target: { ...fixture.target, bootstrapExplorerId: fixture.seed.bootstrapExplorerId },
    seed: fixture.seed,
    runID: fixture.target.fixtureProject.replace(/^loom_dev_verify_/, ''),
    custom: false,
    scenario,
    args: { caseName },
  };
};

export const makeReportLocation = (context, scenario, caseName) => {
  const reportPath = resolve(context.target.artifacts, 'verify-ui', scenario + '-' + (caseName ?? 'default') + '-' + context.runID + '.json');
  const evidenceDirectory = dirname(reportPath) + '/' + scenario + '-' + (caseName ?? 'default') + '-' + context.runID;
  mkdirSync(evidenceDirectory, { recursive: true, mode: 0o700 });
  return { reportPath, evidenceDirectory };
};

export const validateScenarioCase = (scenario, caseName) => {
  if (!caseName) return;
  if (!scenario.cases.includes(caseName)) throw new Error('unknown case for ' + scenario.id + ': ' + caseName);
};

export const scenarioFor = id => getScenario(id);
