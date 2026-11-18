import assert from 'node:assert/strict';
import test from 'node:test';
import { verifyBuilderResourceChoices } from '../../scripts/demo-builder-resource-check.mjs';

const makePage = (choices = ['Patient', 'Specimen']) => {
  const actions = [];
  const page = {
    getByTestId(id) {
      actions.push(`getByTestId:${id}`);
      return { async waitFor() { actions.push('waitFor:loaded-preview'); }, async innerText() { return 'Row\nPatient ID\n1\nfixture-id'; } };
    },
    getByRole(role, options) {
      actions.push(`getByRole:${role}:${String(options.name)}`);
      return {
        async waitFor() { actions.push(`waitFor:${role}`); },
        async click() { actions.push(`click:${role}:${String(options.name)}`); },
        async isEnabled() { return choices.includes(options.name.replace(/^Choose (.*) rows$/, '$1')); },
      };
    },
    getByText(text) {
      actions.push(`getByText:${text}`);
      return { async click() { actions.push(`click:text:${text}`); } };
    },
    content: async () => '<main>FHIR Explorer Studio Publish</main>',
    locator(selector) {
      assert.equal(selector, 'body');
      return { async innerText() { return 'FHIR Explorer Studio\nPublish'; } };
    },
  };
  return { page, actions };
};

test('resource smoke waits for the loaded preview and opens the New table resource chooser through user actions', async () => {
  const { page, actions } = makePage();
  await verifyBuilderResourceChoices(page, ['Patient', 'Specimen']);
  assert(actions.indexOf('getByTestId:preview-table-content') < actions.indexOf('click:button:New table'));
  assert(actions.includes('getByRole:button:Choose Patient rows'));
  assert(actions.includes('getByRole:button:Choose Specimen rows'));
  assert(!actions.some((action) => action.startsWith('click:button:Choose ')));
});

test('resource smoke fails when an expected resource is absent or unavailable', async () => {
  const { page } = makePage(['Patient']);
  await assert.rejects(verifyBuilderResourceChoices(page, ['Patient', 'Specimen']), /Expected Specimen row choice to be enabled/);
});
