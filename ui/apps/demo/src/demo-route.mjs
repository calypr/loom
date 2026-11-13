
export function initialDemoRoute({ searchParams, configuredProject, configuredExplorer }) {
  const projectOverride = searchParams.get('project');
  const project = projectOverride ?? configuredProject;
  const requestedExplorer = searchParams.get('explorer');
  const projectChanged = projectOverride !== null && projectOverride !== configuredProject;
  const explorerId = requestedExplorer ?? (projectChanged
    ? 'default'
    : configuredExplorer ?? 'default');
  return { project, explorerId };
}

export function updateDemoNavigationURL(currentURL, navigation) {
  const nextURL = new URL(currentURL);
  nextURL.searchParams.set('project', navigation.project);
  if (navigation.organization) nextURL.searchParams.set('organization', navigation.organization);
  else nextURL.searchParams.delete('organization');
  nextURL.searchParams.set('explorer', navigation.explorerId);

  switch (navigation.kind) {
    case 'mode':
      nextURL.searchParams.set('mode', navigation.mode);
      break;
    case 'explorer':
      break;
    case 'repair':
      nextURL.searchParams.set('mode', 'builder');
      nextURL.searchParams.set('focusOutput', navigation.focus.outputId);
      nextURL.searchParams.set('focusColumn', navigation.focus.column);
      nextURL.searchParams.set('focusLabel', navigation.focus.label);
      break;
    default: {
      const exhaustive = navigation;
      throw new TypeError(`Unsupported demo navigation: ${JSON.stringify(exhaustive)}`);
    }
  }

  return nextURL;
}

export function viewerProjectID(organization, project) {
  return organization ? `${organization}/${project}` : project;
}
