import { createDevSession, assertOwnedDevSession, canonicalProjectID, createFreshVerificationFixture } from '../../loom-dev.mjs';
import { getScenario, scenarioCaseFor } from '../registry.mjs';
import { mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';

export { environmentForFixtureDir } from './fixture-environment.mjs';

export const fixtureProjectExplorerCollectionPath = project =>
  `/api/v1/projects/${encodeURIComponent(encodeURIComponent(canonicalProjectID(project)))}/explorers`;

export const fixtureExplorerRequestPathMatches = (pathname, project, explorer) => {
  const collectionPath = fixtureProjectExplorerCollectionPath(project);
  if (pathname === collectionPath) return true;
  if (typeof explorer !== 'string' || !explorer.trim()) return false;
  const explorerPath = `${collectionPath}/${encodeURIComponent(explorer)}`;
  return pathname === explorerPath || pathname.startsWith(`${explorerPath}/`);
};

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

export const createRunContext = async ({ caseName }, scenario, { env = process.env, populateBootstrap = false } = {}) => {
  assertNamedDevSessionEnvironment(env);
  const session = createDevSession(env);
  await assertOwnedDevSession(session, { uiRouting: 'explicit-query' });
  const id = Date.now().toString(36) + '-' + Math.random().toString(16).slice(2, 9);
  const fixture = await createFreshVerificationFixture(session, id, { populateBootstrap });
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
  if (!caseName) return undefined;
  return scenarioCaseFor(scenario, caseName);
};

export const scenarioFor = id => getScenario(id);
