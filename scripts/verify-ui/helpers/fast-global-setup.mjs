import { createDevSession, assertOwnedDevSession, createFreshVerificationFixture } from '../../loom-dev.mjs';
import { readFastState, writeFastState } from './fast-fixture-state.mjs';
import { assertLoadedDataset } from './dataset-preflight.mjs';

const day = () => new Date().toISOString().slice(0, 10);

const builderAvailable = async (state) => {
  try {
    const response = await fetch(`${state.apiUrl}/api/v1/projects/${encodeURIComponent(state.project)}/explorers/${encodeURIComponent(state.explorerId)}/authoring/v2/builder`, {
      signal: AbortSignal.timeout(5_000),
    });
    return response.ok;
  } catch {
    return false;
  }
};

export default async function fastGlobalSetup() {
  if (process.env.LOOM_FAST_SKIP_GLOBAL_SETUP === '1') {
    for (const name of ['LOOM_FAST_PROJECT', 'LOOM_FAST_EXPLORER', 'LOOM_FAST_UI_URL', 'LOOM_FAST_API_URL']) {
      if (!String(process.env[name] ?? '').trim()) throw new Error(name + ' is required when LOOM_FAST_SKIP_GLOBAL_SETUP=1');
    }
    return;
  }
  const session = createDevSession(process.env);
  await assertOwnedDevSession(session, { uiRouting: 'explicit-query' });
  if (process.env.LOOM_FAST_PROJECT && process.env.LOOM_FAST_GENERATION) {
    const checked = await assertLoadedDataset({
      apiOrigin: session.apiUrl,
      project: process.env.LOOM_FAST_PROJECT,
      generation: process.env.LOOM_FAST_GENERATION,
      explorerId: process.env.LOOM_FAST_EXPLORER,
    });
    process.env.LOOM_FAST_PROJECT = checked.project;
    process.env.LOOM_FAST_EXPLORER = checked.explorerId;
    process.env.LOOM_FAST_UI_URL = session.uiUrl;
    process.env.LOOM_FAST_API_URL = session.apiUrl;
    return;
  }
  const cached = readFastState();
  if (cached
    && cached.day === day()
    && cached.uiUrl === session.uiUrl
    && cached.apiUrl === session.apiUrl
    && await builderAvailable(cached)) {
    process.env.LOOM_FAST_PROJECT = cached.project;
    process.env.LOOM_FAST_EXPLORER = cached.explorerId;
    process.env.LOOM_FAST_UI_URL = cached.uiUrl;
    process.env.LOOM_FAST_API_URL = cached.apiUrl;
    return;
  }
  const runID = 'f' + Date.now().toString(36).slice(-8) + Math.random().toString(36).slice(2, 5);
  const fixture = await createFreshVerificationFixture(session, runID, { populateBootstrap: true });
  const state = {
    day: day(),
    uiUrl: session.uiUrl,
    apiUrl: session.apiUrl,
    project: fixture.target.fixtureProject,
    explorerId: fixture.seed.bootstrapExplorerId,
    createdAt: new Date().toISOString(),
  };
  writeFastState(state);
  process.env.LOOM_FAST_PROJECT = state.project;
  process.env.LOOM_FAST_EXPLORER = state.explorerId;
  process.env.LOOM_FAST_UI_URL = state.uiUrl;
  process.env.LOOM_FAST_API_URL = state.apiUrl;
}
