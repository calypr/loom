import { canonicalProjectID } from '../../loom-dev.mjs';

export function createdExplorerScope(project, response) {
  if (typeof project !== 'string' || !project.trim()) {
    throw new TypeError('A project is required to bind the created Explorer scope');
  }
  const canonicalProject = canonicalProjectID(project);
  if (typeof response?.project !== 'string' || canonicalProjectID(response.project) !== canonicalProject) {
    throw new Error('Explorer creation response must match the requested project');
  }

  const explorerId = response?.explorerId;
  if (typeof explorerId !== 'string' || explorerId.length > 64 || !/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(explorerId)) {
    throw new Error('Explorer creation response must contain a valid server-assigned Explorer ID');
  }

  const explorerRoot = `/api/v1/projects/${encodeURIComponent(canonicalProject)}/explorers/${encodeURIComponent(explorerId)}`;
  const browserExplorerRoot = `/api/v1/projects/${encodeURIComponent(encodeURIComponent(canonicalProject))}/explorers/${encodeURIComponent(explorerId)}`;
  return {
    project: canonicalProject,
    explorerId,
    explorerRoot,
    authoringBase: `${explorerRoot}/authoring/v2`,
    browserExplorerRoot,
    browserAuthoringBase: `${browserExplorerRoot}/authoring/v2`,
  };
}
