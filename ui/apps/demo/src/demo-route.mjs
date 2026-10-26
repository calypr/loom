/** @typedef {{ readonly outputId: string; readonly column: string; readonly label: string }} RepairFocus */
/** @typedef {{ readonly searchParams: URLSearchParams; readonly configuredProject: string; readonly configuredExplorer?: string }} DemoInitialRouteInput */
/** @typedef {{ readonly project: string; readonly explorerId: string }} DemoInitialRoute */
/** @typedef {{ readonly kind: 'mode'; readonly project: string; readonly organization?: string; readonly explorerId: string; readonly mode: 'builder' | 'viewer' } | { readonly kind: 'explorer'; readonly project: string; readonly organization?: string; readonly explorerId: string } | { readonly kind: 'repair'; readonly project: string; readonly organization?: string; readonly explorerId: string; readonly focus: RepairFocus }} DemoNavigation */

/**
 * Resolve an initial project and Explorer without carrying a build-time Explorer
 * identity into a different project selected by the URL.
 * @param {DemoInitialRouteInput} input
 * @returns {DemoInitialRoute}
 */
export function initialDemoRoute({ searchParams, configuredProject, configuredExplorer }) {
  const projectOverride = searchParams.get('project');
  const project = projectOverride ?? configuredProject;
  const requestedExplorer = searchParams.get('explorer');
  const projectChanged = projectOverride !== null && projectOverride !== configuredProject;
  // `default` is the Builder's existing policy: use a real default Explorer
  // when present, otherwise select the first Explorer in the new project's list.
  const explorerId = requestedExplorer ?? (projectChanged
    ? 'default'
    : configuredExplorer ?? 'default');
  return { project, explorerId };
}

/**
 * Update demo-shell navigation while retaining unrelated query parameters.
 * The optional organization is written consistently for every internal route
 * transition and omitted for legacy project-only URLs.
 * @param {URL} currentURL
 * @param {DemoNavigation} navigation
 */
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

/**
 * The Viewer accepts a single project identifier. Supply Calypr's canonical
 * organization/project identifier there while keeping the route query split.
 * @param {string | undefined} organization
 * @param {string} project
 */
export function viewerProjectID(organization, project) {
  return organization ? `${organization}/${project}` : project;
}
