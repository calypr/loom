export async function verifyBuilderResourceChoices(page, expectedResources) {
  await page.getByTestId('preview-table-content').waitFor({ state: 'visible', timeout: 30_000 });
  const initial = await page.content();
  const initialText = await page.locator('body').innerText();
  for (const text of ['FHIR Explorer Studio', 'Publish']) {
    if (!initialText.includes(text)) throw new Error(`Expected ${text} in the loaded Builder view`);
  }
  if (initial.includes('class="loom-error"') || initial.includes('role="alert"')) {
    throw new Error('Loom rendered an error in the loaded Builder view');
  }

  await page.getByRole('button', { name: 'New table', exact: true }).click();
  await page.getByRole('heading', { name: 'Build another table', exact: true }).waitFor({ state: 'visible', timeout: 30_000 });
  await page.getByRole('region', { name: 'Choose row type', exact: true }).waitFor({ state: 'visible', timeout: 30_000 });

  for (const resource of expectedResources) {
    const choice = page.getByRole('button', { name: `Choose ${resource} rows`, exact: true });
    await choice.waitFor({ state: 'visible', timeout: 30_000 });
    if (!(await choice.isEnabled())) throw new Error(`Expected ${resource} row choice to be enabled`);
  }
}
