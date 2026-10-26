export const browserURL = (target, project, explorer, mode) => {
  const url = new URL(target.uiUrl);
  url.searchParams.set('project', project);
  url.searchParams.set('explorer', explorer);
  url.searchParams.set('mode', mode);
  return url.toString();
};
