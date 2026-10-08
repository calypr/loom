export function createdExplorerScope(project, response) {
  if (typeof project !== 'string' || !project.trim()) {
    throw new TypeError('A project is required to bind the created Explorer scope');
  }
  if (response?.project !== project) {
    throw new Error('Explorer creation response must match the requested project');
  }

  const explorerId = response?.explorerId;
  if (typeof explorerId !== 'string' || explorerId.length > 64 || !/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(explorerId)) {
    throw new Error('Explorer creation response must contain a valid server-assigned Explorer ID');
  }

  const explorerRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerId)}`;
  return {
    explorerId,
    explorerRoot,
    authoringBase: `${explorerRoot}/authoring/v2`,
  };
}
