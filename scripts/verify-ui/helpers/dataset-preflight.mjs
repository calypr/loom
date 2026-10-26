const READY_STATES = new Set(['STAGED', 'ACTIVE', 'READY']);

const requestJSON = async (url, init = {}) => {
  const response = await fetch(url, { ...init, signal: AbortSignal.timeout(5_000) });
  let body;
  try { body = await response.json(); } catch { body = undefined; }
  return { response, body };
};

const legacyProject = (project) => String(project).replaceAll('/', '-');

const generationStatus = async (apiOrigin, project, generation) => {
  for (const candidate of [...new Set([project, legacyProject(project)])]) {
    const { response, body } = await requestJSON(
      `${apiOrigin}/api/v1/datasets/${encodeURIComponent(candidate)}/generations/${encodeURIComponent(generation)}`,
    );
    if (response.ok) return { project: candidate, status: body };
    if (response.status !== 404) {
      throw new Error(`dataset generation preflight returned HTTP ${response.status} for ${candidate}/${generation}`);
    }
  }
  throw new Error(`dataset generation ${project}/${generation} is absent; load the dataset before running tests`);
};

export async function assertLoadedDataset({ apiOrigin, project, generation, explorerId }) {
  if (!apiOrigin || !project || !generation) {
    throw new Error('dataset preflight requires apiOrigin, project, and generation');
  }
  const status = await generationStatus(apiOrigin, project, generation);
  const state = String(status.status?.state ?? '').toUpperCase();
  if (!READY_STATES.has(state) || status.status?.reusable !== true) {
    throw new Error(`dataset generation ${project}/${generation} is not reusable-ready (state=${state || 'missing'}, reusable=${status.status?.reusable ?? 'missing'})`);
  }

  const explorers = await requestJSON(`${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers`);
  if (!explorers.response.ok || !Array.isArray(explorers.body) || explorers.body.length === 0) {
    throw new Error(`dataset project ${project} has no usable Explorer; publish an Explorer config before running tests`);
  }
  const selected = explorerId
    ? explorers.body.find((explorer) => explorer.explorerId === explorerId)
    : explorers.body[0];
  if (!selected?.explorerId) {
    throw new Error(`dataset project ${project} has no Explorer ${explorerId ?? '(default)'}`);
  }

  const builder = await requestJSON(
    `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(selected.explorerId)}/authoring/v2/builder`,
  );
  if (!builder.response.ok) {
    throw new Error(`dataset Explorer ${selected.explorerId} Builder state returned HTTP ${builder.response.status}`);
  }
  const documents = builder.body?.workspace?.documents;
  if (!Array.isArray(documents) || documents.length === 0) {
    throw new Error(`dataset Explorer ${selected.explorerId} has no authored dataframe document`);
  }

  return {
    apiOrigin,
    project,
    generation,
    explorerId: selected.explorerId,
    explorerCount: explorers.body.length,
    documentCount: documents.length,
  };
}
