export async function selectRelatedRouteOption({
  waitForRouteOrDisclosure,
  routeIsMounted,
  disclosureIsOpen,
  openDisclosure,
  waitForRouteEnabled,
  clickRoute,
}) {
  await waitForRouteOrDisclosure();
  if (!(await routeIsMounted()) && !(await disclosureIsOpen())) await openDisclosure();
  await waitForRouteEnabled();
  await clickRoute();
}
