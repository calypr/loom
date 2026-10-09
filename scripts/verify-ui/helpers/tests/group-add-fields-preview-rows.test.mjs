import assert from 'node:assert/strict';
import { chmod, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { chromium } from 'playwright';
import { flushNativeAbortProbeEvents } from '../native-abort-probe.mjs';
import {
  expectedGroupAddFieldsRows,
  groupAddFieldsPreviewHeaders,
  groupAddFieldsPreviewWaitState,
  matchesGroupAddFieldsRemovalRequest,
  matchesGroupAddFieldsRenameRequest,
  runGroupAddFieldsBrowserWorkflow,
} from '../../workflows/verify-cda-group-add-fields-browser.mjs';

const commandPath = '/api/v1/projects/loom_dev_cda_fhir/explorers/qa-reshape-group-add-fields-f3a413a5-cd66-4085-b83e-878076cc1238/authoring/v2/commands';
const renameExpected = {
  commandPath,
  draftVersion: 7,
  draftDigest: 'sha256:9bf00f78ea3660f3277e38161a5ba0734226bcb9e72b47809cdd2583953f1c46',
  outputId: 'out_0ce2ec4739afb2d74801b704',
  stepId: 'group_6ef5835a-5b06-4b98-89f8-5343fdcb850d',
  sourceColumnId: 'source_b40816f95982f462646f7c04',
  outputColumnId: 'row_value_e885c152162968a4b1a6fb70',
  label: 'Specimen Resource Type',
};
const renameRequest = {
  method: 'POST',
  path: commandPath,
  body: {
    expectedDraftVersion: renameExpected.draftVersion,
    expectedDraftDigest: renameExpected.draftDigest,
    commands: [{
      type: 'UPDATE_CONSTRUCTION_OUTPUT',
      outputId: renameExpected.outputId,
      constructionOutput: {
        stepId: renameExpected.stepId,
        columnId: renameExpected.outputColumnId,
        label: renameExpected.label,
      },
    }],
  },
};

test('Group Add Columns expectations follow the applied presentation order and retain raw counts', () => {
  const rawGroupedRows = [
    ['raw-specimen-1', '12'],
    ['raw-specimen-2', '3'],
  ];

  const expected = expectedGroupAddFieldsRows(rawGroupedRows, 'Specimen');

  assert.deepEqual(groupAddFieldsPreviewHeaders, ['Specimen ID', 'Row count', 'Resource Type']);
  assert.deepEqual(expected, [
    ['raw-specimen-1', '12', 'Specimen'],
    ['raw-specimen-2', '3', 'Specimen'],
  ]);
  assert(expected.every(row => row.length === groupAddFieldsPreviewHeaders.length), 'Expected rows must match all three displayed columns');
  assert.notDeepEqual(expected, [
    ['raw-specimen-1', 'Specimen', '12'],
    ['raw-specimen-2', 'Specimen', '3'],
  ], 'The applied Group aggregate precedes the row-value field in output order');
});

test('Group Add Fields correlates native rename and removal with distinct source and Group output identities', () => {
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, renameExpected), true,
    'The retained successful UPDATE_CONSTRUCTION_OUTPUT command must match its derived row-value output');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, outputColumnId: renameExpected.sourceColumnId,
  }), false, 'The authored source field identity must not be mistaken for the derived Group output identity');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, outputId: 'out-other',
  }), false, 'A command for another output must not satisfy the rename wait');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, stepId: 'group-other',
  }), false, 'A command for another Group step must not satisfy the rename wait');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, draftVersion: 6,
  }), false, 'A stale draft version must not satisfy the rename wait');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, draftDigest: 'sha256:stale',
  }), false, 'A stale draft digest must not satisfy the rename wait');
  assert.equal(matchesGroupAddFieldsRenameRequest(renameRequest, {
    ...renameExpected, commandPath: '/api/v1/projects/other/explorers/qa/authoring/v2/commands',
  }), false, 'A command from another Explorer must not satisfy the rename wait');

  const removalExpected = {
    commandPath,
    draftVersion: 8,
    draftDigest: 'sha256:32f286b1a37f44962c611d9694522e42c91d4d1074848a6c6bac680f8f99dc5a',
    outputId: renameExpected.outputId,
    sourceColumn: 'col_cc83aceeefd5acaaad33fc87',
    sourceColumnId: renameExpected.sourceColumnId,
    outputColumnId: renameExpected.outputColumnId,
  };
  const removalRequest = {
    method: 'POST',
    path: commandPath,
    body: {
      expectedDraftVersion: removalExpected.draftVersion,
      expectedDraftDigest: removalExpected.draftDigest,
      commands: [{
        type: 'REMOVE_COLUMN',
        outputId: removalExpected.outputId,
        column: removalExpected.sourceColumn,
      }],
    },
  };
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, removalExpected), true,
    'Removing the Group row-value must target its exact authored source column');
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, {
    ...removalExpected, sourceColumn: removalExpected.outputColumnId,
  }), false, 'The remove adapter must use the authored physical column name, not the derived output ID');
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, {
    ...removalExpected, outputId: 'out-other',
  }), false, 'A removal for another output must not satisfy the removal wait');
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, {
    ...removalExpected, draftVersion: 7,
  }), false, 'A stale draft version must not satisfy the removal wait');
  assert.equal(matchesGroupAddFieldsRemovalRequest(removalRequest, {
    ...removalExpected, draftDigest: 'sha256:stale',
  }), false, 'A stale draft digest must not satisfy the removal wait');
});

test('Group Add Fields reload wait matches semantic headers and rejects stale or incomplete previews', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`
      <div data-testid="preview-table-scroll" style="width: 360px; height: 120px; overflow: auto">
        <div role="table" aria-rowcount="2" aria-colcount="3">
          <div role="row">
            <div role="columnheader" style="text-transform: uppercase">Specimen ID</div>
            <div role="columnheader" style="text-transform: uppercase">Row count</div>
            <div role="columnheader" style="text-transform: uppercase">Resource Type</div>
          </div>
          <div role="row">
            <div role="cell">00001c68-2c20-5003-a144-b2442469d8de</div>
            <div role="cell">12</div>
            <div role="cell">Specimen</div>
          </div>
        </div>
      </div>
    `);

    const expected = { dataRowCount: 1, columnCount: 3, header: 'Resource Type' };
    await page.waitForFunction(groupAddFieldsPreviewWaitState, expected, { timeout: 1000 });
    const ready = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(ready.ready, true);
    assert.deepEqual(ready.headers, groupAddFieldsPreviewHeaders);
    assert.deepEqual(ready.visibleHeaders, ['SPECIMEN ID', 'ROW COUNT', 'RESOURCE TYPE']);

    const wrongHeader = await page.evaluate(groupAddFieldsPreviewWaitState, {
      ...expected, header: 'Wrong label', diagnostic: true,
    });
    assert.equal(wrongHeader.headerMatches, false);
    assert.equal(wrongHeader.ready, false);

    await page.locator('[role="table"]').evaluate(table => table.setAttribute('aria-rowcount', '3'));
    const wrongRowCount = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(wrongRowCount.rowCountMatches, false);
    assert.equal(wrongRowCount.ready, false);

    await page.locator('[role="table"]').evaluate(table => {
      table.setAttribute('aria-rowcount', '2');
      table.setAttribute('aria-colcount', '4');
    });
    const wrongColumnCount = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(wrongColumnCount.columnCountMatches, false);
    assert.equal(wrongColumnCount.ready, false);

    await page.locator('[role="table"]').evaluate(table => table.setAttribute('aria-colcount', '3'));
    await page.locator('body').evaluate(body => {
      const loading = document.createElement('p');
      loading.textContent = 'Loading your table…';
      loading.id = 'preview-loading';
      body.append(loading);
    });
    const loading = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(loading.globalLoadingSentinel, true);
    assert.equal(loading.ready, false);

    await page.locator('#preview-loading').evaluate(node => node.remove());
    await page.locator('body').evaluate(body => {
      const error = document.createElement('p');
      error.textContent = 'Preview failed: fixture failure';
      error.id = 'preview-error';
      body.append(error);
    });
    const previewError = await page.evaluate(groupAddFieldsPreviewWaitState, { ...expected, diagnostic: true });
    assert.equal(previewError.globalPreviewErrorSentinel, true);
    assert.equal(previewError.ready, false);
  } finally {
    await browser.close();
  }
});

test('Group Add Fields workflow installs exact abort diagnostics before its first navigation and leaves request terminals untouched', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  const fakeRtkDirectory = await mkdtemp(path.join(os.tmpdir(), 'group-add-fields-fake-rtk-'));
  const originalPath = process.env.PATH;
  const originalFetch = globalThis.fetch;
  try {
    const fakeRtk = path.join(fakeRtkDirectory, 'rtk');
    const witness = {
      source: { id: 'Specimen/fixture-1', _id: 'Specimen/fixture-1', generation: 'cda-fhir-v1', resourceType: 'Specimen' },
      patient: { id: 'Patient/fixture-1', _id: 'Patient/fixture-1', generation: 'cda-fhir-v1', resourceType: 'Patient' },
      observations: [
        { id: 'Observation/fixture-1', _id: 'Observation/fixture-1', generation: 'cda-fhir-v1', resourceType: 'Observation' },
        { id: 'Observation/fixture-2', _id: 'Observation/fixture-2', generation: 'cda-fhir-v1', resourceType: 'Observation' },
      ],
    };
    await writeFile(fakeRtk, `#!/usr/bin/env node\nprocess.stdout.write(${JSON.stringify(JSON.stringify([witness])) + " + '\\n'"});\n`);
    await chmod(fakeRtk, 0o755);
    process.env.PATH = `${fakeRtkDirectory}${path.delimiter}${originalPath ?? ''}`;

    const page = await browser.newPage();
    await page.setContent('<main>before navigation</main>');
    const project = 'loom_dev_cda_fhir';
    const requestedExplorer = 'qa-reshape-group-add-fields-fixture';
    const createdExplorer = 'qa-reshape-group-add-fields-server-assigned';
    const apiOrigin = 'http://127.0.0.1:8188';
    const uiOrigin = 'http://127.0.0.1:30008';
    const builder = {
      catalog: {
        generation: 'cda-fhir-v1',
        snapshotToken: 'fixture-snapshot',
        nodes: [{ resourceType: 'Specimen', nodeId: 'node-specimen' }],
        candidates: [{ nodeId: 'node-specimen', fieldPath: 'id', candidateId: 'candidate-specimen-id' }],
      },
      workspace: { semanticsVersion: 10, documents: [{ output: { id: 'output-fixture' } }] },
      draftVersion: 1,
      draftDigest: 'sha256:fixture-draft',
    };
    const report = { target: { project, generation: 'cda-fhir-v1', uiUrl: `${uiOrigin}/` }, errors: [], nativeRequests: [] };
    const navigationSentinel = new Error('navigation sentinel');
    let attachedReport;
    globalThis.fetch = async (input, init = {}) => {
      const url = new URL(input);
      const method = init.method ?? 'GET';
      let status = 200;
      let body = {};
      if (url.pathname === `/api/v1/projects/${project}/explorers` && method === 'POST') {
        status = 201;
        body = { project, explorerId: createdExplorer };
      } else if (url.pathname === `/api/v1/projects/${project}/explorers/${createdExplorer}/authoring/v2/builder`) {
        body = builder;
      } else if (url.pathname === `/api/v1/projects/${project}/explorers/${createdExplorer}/selections` && method === 'POST') {
        body = { id: 'selection-fixture', project, generation: 'cda-fhir-v1', resourceType: 'Specimen', memberCount: 1, scopeDigest: 'sha256:fixture-scope' };
      } else if (url.pathname === `/api/v1/projects/${project}/explorers/${createdExplorer}/authoring/v2/population-routes`) {
        body = { choices: [{ route: [], routeChoiceId: 'route-direct' }] };
      }
      return { ok: status >= 200 && status < 300, status, json: async () => body };
    };

    const cda = {
      project,
      explorer: requestedExplorer,
      apiOrigin,
      uiOrigin,
      evidence: {},
      env: { LOOM_ARANGO_CONTAINER: 'fixture-arango' },
      target: { fixtureGeneration: 'cda-fhir-v1', arangoContainer: 'fixture-arango' },
      report,
      check: (_dimension, _name, passed, evidence) => assert(passed, JSON.stringify(evidence)),
      captureRequests: async prefix => ({ prefix }),
      navigate: async url => {
        const navigationURL = new URL(url);
        assert.equal(navigationURL.origin, uiOrigin);
        assert.equal(navigationURL.searchParams.get('project'), project);
        assert.equal(navigationURL.searchParams.get('explorer'), createdExplorer,
          'The actual workflow must use the server-assigned Explorer for its first navigation.');
        assert.equal(navigationURL.searchParams.get('mode'), 'builder');
        assert.equal(await page.evaluate(() => globalThis.__loomNativeAbortProbeInstalled === true), true,
          'The actual workflow must install the probe before invoking its navigation callback.');
        await flushNativeAbortProbeEvents(page);
        assert.deepEqual(report.nativeAbortProbeEvents.map(({ kind, project: eventProject, explorer, explorerScope }) =>
          ({ kind, project: eventProject, explorer, explorerScope })), [{
          kind: 'probe-installed', project, explorer: createdExplorer, explorerScope: 'exact',
        }], 'The actual caller must retain one exact project/created-Explorer probe event before navigation.');
        throw navigationSentinel;
      },
      click: async () => {},
      selectOption: async () => {},
      fill: async () => {},
      press: async () => {},
      inspect: async fn => page.evaluate(fn),
      wait: async () => {},
      attachReport: async (_name, value) => { attachedReport = value; },
    };

    await assert.rejects(runGroupAddFieldsBrowserWorkflow({ page, cda }), error => error === navigationSentinel);
    assert.equal(attachedReport.explorer, createdExplorer);
    assert.equal(attachedReport.nativeRequests, report.nativeRequests);
    assert.deepEqual(report.nativeAbortProbeEvents.map(({ kind, project: eventProject, explorer, explorerScope }) =>
      ({ kind, project: eventProject, explorer, explorerScope })), [{
      kind: 'probe-installed', project, explorer: createdExplorer, explorerScope: 'exact',
    }]);
    assert.deepEqual(report.nativeRequests, [],
      'Installing diagnostics does not synthesize native requestfinished or requestfailed terminal evidence.');
  } finally {
    globalThis.fetch = originalFetch;
    if (originalPath === undefined) delete process.env.PATH;
    else process.env.PATH = originalPath;
    await browser.close();
    await rm(fakeRtkDirectory, { recursive: true, force: true });
  }
});
