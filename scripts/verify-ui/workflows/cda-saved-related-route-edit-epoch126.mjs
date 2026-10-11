import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';
import {
  buildArangoShellInvocation,
  buildBoundedArangoQueryScript,
  summarizeArangoShellResult,
} from '../helpers/owned-arangosh-command.mjs';
import {
  assertCdaSavedRelatedRouteRows,
  buildCdaSavedRelatedRouteQuery,
  projectVisibleRoutePairs,
  readCdaSavedRelatedRouteOracle,
} from '../helpers/cda-saved-related-route-oracle.mjs';
import { collectPreviewRows } from '../helpers/playwright-preview-rows.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const scalar = value => {
  const result = normalize(value);
  return result === '' || result === '—' ? null : result;
};
const sortPairs = pairs => [...pairs].sort((left, right) =>
  left[0].localeCompare(right[0]) || String(left[1] ?? '').localeCompare(String(right[1] ?? '')));
const outputRelatedLabel = 'Observation FHIR resource ID';
const VALID_EXPLORER_ID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;
const remainingUntil = deadline => Math.max(1, deadline - Date.now());

export function resolveSavedRouteBootstrapExplorer(summaries, { project, preferredExplorer = null } = {}) {
  assert(Array.isArray(summaries), 'The exact CDA project Explorer list must be an array.');
  assert.equal(typeof project, 'string');
  assert(project.trim(), 'The owned CDA project is required to select a bootstrap Explorer.');
  assert(summaries.every(summary => summary && summary.project === project),
    'Every bootstrap candidate must belong to the exact owned CDA project.');
  const ids = summaries.map(summary => summary.explorerId);
  assert(ids.every(id => typeof id === 'string' && VALID_EXPLORER_ID.test(id)),
    'The exact CDA project Explorer list must contain valid Explorer identities.');
  assert.equal(new Set(ids).size, ids.length, 'The exact CDA project Explorer list must not duplicate identities.');
  assert(ids.length > 0, 'The exact owned CDA project must have an existing Explorer for native creation navigation.');
  if (preferredExplorer !== null) {
    assert(ids.includes(preferredExplorer), 'Configured CDA Explorer must be present in the exact owned project list.');
    return preferredExplorer;
  }
  return [...ids].sort()[0];
}

export function resolveSavedRouteSourceIDColumn(document, rootResourceType) {
  assert.equal(document?.kind, 'ExplorerBuilderDocument');
  assert.equal(document?.rootResourceType, rootResourceType);
  const matches = (document.columns ?? []).filter(column => column.occurrenceId === 'base'
    && column.source?.kind === 'field'
    && column.source.field?.path === 'id'
    && column.source.field?.projectionMode === 'VALUE');
  assert.equal(matches.length, 1,
    rootResourceType + ' must expose exactly one base VALUE id source column.');
  const [column] = matches;
  assert.equal(typeof column.columnId, 'string',
    rootResourceType + ' source columns use stable columnId values.');
  assert(column.columnId.trim(), rootResourceType + ' source columnId must be nonempty.');
  assert.equal(Object.hasOwn(column, 'id'), false,
    'Persisted Builder source columns use columnId; construction stage columns use id.');
  return column;
}

export async function ensureExactRelatedRouteChoices(page, {
  directLabel,
  viaPatientLabel,
  phase,
  onOpenDisclosure,
  timeout = ACTION_TIMEOUT_MS,
}) {
  const deadline = Date.now() + timeout;
  const remaining = () => Math.max(1, deadline - Date.now());
  const assertWithinDeadline = () => assert(Date.now() < deadline,
    phase + ': exact related route choices exceeded their ' + timeout + 'ms deadline.');
  const direct = page.getByRole('radio', { name: directLabel, exact: true });
  const viaPatient = page.getByRole('radio', { name: viaPatientLabel, exact: true });
  const otherPaths = page.getByTestId('construction-related-expand-other-routes');
  await otherPaths.waitFor({ state: 'visible', timeout: remaining() });
  assertWithinDeadline();
  assert.equal(await otherPaths.count(), 1,
    phase + ': alternate relationship paths disclosure must be unique.');
  const waitForBothRoutes = async () => {
    const routeTimeout = remaining();
    await Promise.all([
      direct.waitFor({ state: 'visible', timeout: routeTimeout }),
      viaPatient.waitFor({ state: 'visible', timeout: routeTimeout }),
    ]);
    assertWithinDeadline();
  };
  const [directVisible, viaPatientVisible] = await Promise.all([
    direct.isVisible(),
    viaPatient.isVisible(),
  ]);
  assertWithinDeadline();
  if (!directVisible || !viaPatientVisible) {
    if (await otherPaths.getAttribute('open') === null) {
      assert.equal(typeof onOpenDisclosure, 'function',
        phase + ': opening the collapsed alternate paths requires the native action wrapper.');
      const summary = otherPaths.locator(':scope > summary');
      await onOpenDisclosure({
        phase,
        summary,
        perform: () => summary.click(),
        after: waitForBothRoutes,
        timeout: remaining(),
        budget: timeout,
      });
      assertWithinDeadline();
    } else {
      await waitForBothRoutes();
    }
  } else {
    await waitForBothRoutes();
  }
  assert.equal(await direct.count(), 1, phase + ': direct route label must be unique.');
  assert.equal(await viaPatient.count(), 1, phase + ': Patient route label must be unique.');
  assert.equal(await direct.isEnabled(), true, phase + ': direct route must be enabled.');
  assert.equal(await viaPatient.isEnabled(), true, phase + ': Patient route must be enabled.');
  assertWithinDeadline();
  return { direct, viaPatient };
}

export function projectSavedRouteSignature(route, phase = 'Saved route') {
  assert(Array.isArray(route) && route.length > 0, phase + ': saved route must contain at least one edge.');
  return route.map((edge, index) => {
    assert(edge && typeof edge === 'object' && !Array.isArray(edge),
      phase + ': saved route edge ' + index + ' must be an object.');
    assert.equal(typeof edge.fromResourceType, 'string', phase + ': route source type must be present.');
    assert.equal(typeof edge.toResourceType, 'string', phase + ': route target type must be present.');
    assert.equal(typeof edge.relationship, 'string', phase + ': route relationship must be present.');
    assert(['INBOUND', 'OUTBOUND'].includes(edge.storageDirection),
      phase + ': route storage direction must be INBOUND or OUTBOUND.');
    return {
      fromResourceType: edge.fromResourceType,
      toResourceType: edge.toResourceType,
      relationship: edge.relationship,
      storageDirection: edge.storageDirection,
    };
  });
}

export function assertSavedRouteSignature(route, expectedSignature, phase = 'Saved route') {
  const signature = projectSavedRouteSignature(route, phase);
  assert.deepEqual(signature, expectedSignature, phase + ': saved route semantic signature changed.');
  return signature;
}

const readGrid = (page, proposal = false) => page.evaluate(isProposal => {
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const proposalRoot = document.querySelector('[data-testid="construction-proposal-preview"]');
  const table = isProposal && proposalRoot
    ? proposalRoot.querySelector('table')
    : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return { headers: [], rows: [] };
  if (isProposal && proposalRoot) {
    return {
      headers: [...table.querySelectorAll('thead th')]
        .map(cell => normalize(cell.querySelector('span')?.textContent ?? cell.textContent)),
      rows: [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => normalize(cell.innerText))),
    };
  }
  const rows = [...table.querySelectorAll('[role="row"]')];
  return {
    headers: [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
      .map(cell => normalize(cell.textContent)),
    rows: rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')]
      .map(cell => normalize(cell.innerText))),
  };
}, proposal);

const gridMatches = contract => {
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const asScalar = value => {
    const result = normalize(value);
    return result === '' || result === '—' ? null : result;
  };
  const proposalRoot = document.querySelector('[data-testid="construction-proposal-preview"]');
  const table = contract.proposal && proposalRoot
    ? proposalRoot.querySelector('table')
    : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return false;
  let headers;
  let rows;
  if (contract.proposal && proposalRoot) {
    headers = [...table.querySelectorAll('thead th')]
      .map(cell => normalize(cell.querySelector('span')?.textContent ?? cell.textContent));
    rows = [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => normalize(cell.innerText)));
  } else {
    const rendered = [...table.querySelectorAll('[role="row"]')];
    headers = [...(rendered[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
      .map(cell => normalize(cell.textContent));
    rows = rendered.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')]
      .map(cell => normalize(cell.innerText)));
  }
  const rootIndex = headers.indexOf(contract.rootLabel);
  const relatedIndex = headers.indexOf(contract.relatedLabel);
  if (rootIndex < 0 || relatedIndex < 0 || rows.length !== contract.expected.length) return false;
  const actual = rows.map(row => [row[rootIndex], asScalar(row[relatedIndex])])
    .sort((left, right) => left[0].localeCompare(right[0])
      || String(left[1] ?? '').localeCompare(String(right[1] ?? '')));
  return JSON.stringify(actual) === JSON.stringify(contract.expected);
};

const waitForGrid = async (page, contract, deadline = Date.now() + ACTION_TIMEOUT_MS) => {
  if (contract.proposal) {
    return page.waitForFunction(gridMatches, contract, { timeout: remainingUntil(deadline) });
  }
  await page.waitForFunction(expectedCount => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === String(expectedCount + 1)
      && !document.body.innerText.includes('Loading your table…')
      && !document.body.innerText.includes('Preview failed:');
  }, contract.expected.length, { timeout: remainingUntil(deadline) });
  return assertVisiblePairs(page, {
    ...contract,
    phase: contract.phase ?? 'native preview',
    timeout: remainingUntil(deadline),
  });
};

const assertVisiblePairs = async (page, {
  rootLabel, relatedLabel, expected, proposal = false, phase, timeout = ACTION_TIMEOUT_MS,
}) => {
  const collected = proposal ? undefined : await collectPreviewRows(page, { timeout });
  if (collected) {
    assert.equal(collected.rowCount, expected.length,
      phase + ': Preview aria-rowcount differs from the exact independent oracle.');
  }
  const grid = await readGrid(page, proposal);
  const rootIndex = grid.headers.indexOf(rootLabel);
  const relatedIndex = grid.headers.indexOf(relatedLabel);
  assert(rootIndex >= 0 && relatedIndex >= 0,
    phase + ': preview omitted an exact semantic header: ' + JSON.stringify(grid.headers));
  const visibleGrid = collected
    ? { headers: grid.headers, rows: collected.rows.map(row => row.values) }
    : grid;
  const pairs = sortPairs(projectVisibleRoutePairs(visibleGrid, rootLabel, relatedLabel, scalar));
  assert.deepEqual(pairs, expected, phase + ': rendered Specimen/Observation pairs differ from the raw CDA oracle.');
  return { headers: grid.headers, pairs, rowCount: collected?.rowCount ?? grid.rows.length };
};

const assertCandidateSubset = async (page, { rootLabel, rootID, relatedLabel, expectedIDs, phase }) => {
  const grid = await readGrid(page, true);
  const rootIndex = grid.headers.indexOf(rootLabel);
  const relatedIndex = grid.headers.indexOf(relatedLabel);
  assert(rootIndex >= 0 && relatedIndex >= 0,
    phase + ': proposal preview omitted a semantic root or related header: ' + JSON.stringify(grid.headers));
  const rows = grid.rows.map(row => [row[rootIndex], scalar(row[relatedIndex])]);
  assert(rows.length > 0 && rows.length <= expectedIDs.length,
    phase + ': proposal preview must be a nonempty, bounded subset of the raw route.');
  assert(rows.every(([observedRootID, observationID]) => observedRootID === rootID
    && expectedIDs.includes(observationID)), phase + ': proposal contains an unexpected raw identity.');
  assert.equal(new Set(rows.map(([, observationID]) => observationID)).size, rows.length,
    phase + ': proposal preview contains duplicate Observation IDs.');
  return rows;
};

export const builderJSON = async (request, apiOrigin, apiRoot) => {
  const response = await request.get(new URL(apiRoot + '/builder', apiOrigin).toString(), { timeout: 10_000 });
  assert.equal(response.status(), 200, 'Read-only Builder inspection must return HTTP 200.');
  return response.json();
};

const documentForOutput = (builder, outputId) => {
  const matches = (builder.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, 'The exact output ID must identify one saved Specimen table.');
  return matches[0];
};

const savedState = (builder, outputId, expectedRootColumnId) => {
  const document = documentForOutput(builder, outputId);
  const filterSteps = (document.construction?.steps ?? []).filter(step => step.operation?.kind === 'FILTER');
  const relatedSteps = (document.construction?.steps ?? []).filter(step => step.operation?.kind === 'RELATED_EXPAND');
  assert.equal(filterSteps.length, 1, 'The saved Specimen table must retain exactly one pinned Specimen filter.');
  assert.equal(relatedSteps.length, 1, 'The saved Specimen table must contain exactly one RELATED_EXPAND step.');
  const relatedStep = relatedSteps[0];
  const related = relatedStep.operation.relatedExpand;
  const output = (relatedStep.outputs ?? []).find(column => column.id === related.relatedRecordColumnId);
  assert(output, 'The saved RelatedExpand must retain its exact output column identity.');
  const rootColumn = resolveSavedRouteSourceIDColumn(document, 'Specimen');
  assert.equal(rootColumn.columnId, expectedRootColumnId,
    'The Specimen source identity column must retain its exact stable columnId.');
  return {
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
    document,
    filterStep: filterSteps[0],
    filterStepId: filterSteps[0].id,
    relatedStep,
    stepId: relatedStep.id,
    outputId,
    rootColumnId: rootColumn.columnId,
    rootLabel: rootColumn.label,
    relatedColumnId: related.relatedRecordColumnId,
    relatedLabel: output.label,
    route: related.route,
    routeSignature: projectSavedRouteSignature(related.route, 'Persisted route'),
    anchorColumnId: related.anchorColumnId,
    emptyPolicy: related.emptyPolicy,
    targetResourceType: related.targetResourceType,
    population: structuredClone(document.population ?? null),
  };
};

const assertStableIdentity = (actual, expected, phase) => {
  assert.equal(actual.outputId, expected.outputId, phase + ': saved table output identity changed.');
  assert.equal(actual.filterStepId, expected.filterStepId, phase + ': Specimen filter step identity changed.');
  assert.equal(actual.stepId, expected.stepId, phase + ': RelatedExpand step identity changed.');
  assert.equal(actual.rootColumnId, expected.rootColumnId, phase + ': Specimen root column identity changed.');
  assert.equal(actual.relatedColumnId, expected.relatedColumnId, phase + ': related output column identity changed.');
  assert.equal(actual.relatedLabel, expected.relatedLabel, phase + ': related output label changed.');
  assert.equal(actual.anchorColumnId, expected.anchorColumnId, phase + ': RelatedExpand anchor column changed.');
  assert.equal(actual.emptyPolicy, expected.emptyPolicy, phase + ': RelatedExpand empty-parent policy changed.');
  assert.deepEqual(actual.population, expected.population, phase + ': selected Specimen population changed.');
  assert.deepEqual(actual.filterStep, expected.filterStep, phase + ': pinned Specimen row filter changed.');
  assert.equal(actual.document.rootResourceType, 'Specimen', phase + ': table root resource changed.');
};

const waitForProposal = (page, timeout = ACTION_TIMEOUT_MS) => page.waitForFunction(() =>
  document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
    && document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready',
undefined, { timeout });

const waitForCurrentPreview = (page, timeout = ACTION_TIMEOUT_MS) => page.waitForFunction(() => {
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  return Boolean(table) && !document.body.innerText.includes('Loading your table…')
    && !document.body.innerText.includes('Preview failed:');
}, undefined, { timeout });

const sourceOnlyGridMatches = contract => {
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const proposalRoot = document.querySelector('[data-testid="construction-proposal-preview"]');
  const table = contract.proposal && proposalRoot
    ? proposalRoot.querySelector('table')
    : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return false;
  let headers;
  let rows;
  if (contract.proposal && proposalRoot) {
    headers = [...table.querySelectorAll('thead th')]
      .map(cell => normalize(cell.querySelector('span')?.textContent ?? cell.textContent));
    rows = [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => normalize(cell.innerText)));
  } else {
    const rendered = [...table.querySelectorAll('[role="row"]')];
    headers = [...(rendered[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
      .map(cell => normalize(cell.textContent));
    rows = rendered.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')]
      .map(cell => normalize(cell.innerText)));
  }
  const rootIndex = headers.indexOf(contract.rootLabel);
  return rootIndex >= 0 && !headers.includes(contract.relatedLabel)
    && JSON.stringify(headers) === JSON.stringify(contract.headers)
    && rows.length === 1 && rows[0][rootIndex] === contract.rootID;
};

const waitForSourceOnlyGrid = (page, contract) => page.waitForFunction(sourceOnlyGridMatches, contract, {
  timeout: ACTION_TIMEOUT_MS,
});

const assertSourceOnlyGrid = async (page, contract, { proposal = false, phase }) => {
  const grid = await readGrid(page, proposal);
  const rootIndex = grid.headers.indexOf(contract.rootLabel);
  assert(rootIndex >= 0, phase + ': source-only table omitted the exact Specimen id header.');
  assert(!grid.headers.includes(contract.relatedLabel), phase + ': source-only table retained the Observation output column.');
  assert.deepEqual(grid.headers, contract.headers, phase + ': source-only schema differs from the filtered native source schema.');
  assert.deepEqual(grid.rows.map(row => row[rootIndex]), [contract.rootID],
    phase + ': source-only preview must contain only the pinned Specimen ID.');
  return grid;
};

const sourceOnlyState = (builder, outputId, { rootColumnId, filterStepId, relatedColumnId, population }, phase) => {
  const document = documentForOutput(builder, outputId);
  assert.equal(document.rootResourceType, 'Specimen', phase + ': saved table root resource changed.');
  const steps = document.construction?.steps ?? [];
  const filterSteps = steps.filter(step => step.operation?.kind === 'FILTER');
  const relatedSteps = steps.filter(step => step.operation?.kind === 'RELATED_EXPAND');
  assert.equal(filterSteps.length, 1, phase + ': source-only table must retain one pinned FILTER step.');
  assert.equal(relatedSteps.length, 0, phase + ': source-only table must have no RELATED_EXPAND step.');
  assert.equal(filterSteps[0].id, filterStepId, phase + ': source filter step identity changed.');
  assert(document.columns?.some(column => column.columnId === rootColumnId),
    phase + ': Specimen source identity column was removed.');
  const retainedStageOutputIds = steps.flatMap(step => step.outputs ?? []).map(output => output.id);
  assert(!retainedStageOutputIds.includes(relatedColumnId),
    phase + ': removed Observation output column remains in the construction outputs.');
  assert.deepEqual(document.population ?? null, population, phase + ': selected Specimen population changed.');
  return { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, document };
};

const waitForExplorerTitle = (page, title) => page.waitForFunction(expectedTitle =>
  document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expectedTitle,
title, { timeout: ACTION_TIMEOUT_MS });

export async function cdaSavedRelatedRouteEditEpoch126Workflow({ page, cda }) {
  configureNativePage(page);
  const target = cda.target;
  const oracle = readCdaSavedRelatedRouteOracle();
  const project = target.fixtureProject;
  const generation = target.fixtureGeneration ?? target.generation;
  assert.equal(project, oracle.scope.project, 'Native workflow must run against the exact epoch126 CDA project.');
  assert.equal(generation, oracle.scope.generation, 'Native workflow must run against the exact epoch126 generation.');

  const requiredChecks = cda.report.requiredChecks;
  const requiredCheckSet = new Set(requiredChecks);
  const check = (dimension, name, passed, evidence = {}) => {
    assert(requiredCheckSet.has(name), 'Unregistered saved-CDA route-edit check: ' + name);
    cda.check(dimension, name, passed, evidence);
    assert(passed, name + ' failed: ' + JSON.stringify(evidence).slice(0, 1_500));
  };
  const title = 'Epoch126 route edit ' + randomUUID().slice(0, 8);
  const routeA = oracle.routeA.rows;
  const routeB = oracle.routeB.rows;
  const expectedA = sortPairs(routeA);
  const expectedB = sortPairs(routeB);
  const apiRoot = '/api/v1/projects/' + encodeURIComponent(project) + '/explorers/';

  const arangoContainer = validatedArangoContainer(target);
  const arangoDatabase = process.env.LOOM_ARANGO_DATABASE ?? 'loom_dev';
  const rawQuery = buildCdaSavedRelatedRouteQuery({ project, generation });
  const rawScript = buildBoundedArangoQueryScript({
    query: rawQuery,
    maxRuntimeSeconds: 8,
    memoryLimitBytes: 256 * 1024 * 1024,
  });
  const invocation = buildArangoShellInvocation({
    container: arangoContainer,
    database: arangoDatabase,
    script: rawScript,
  });
  const rawStarted = Date.now();
  const rawResult = spawnSync(invocation.command, invocation.args, {
    encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024,
  });
  const rawSummary = summarizeArangoShellResult(rawResult, { project, generation });
  assert.equal(rawSummary.processSucceeded, true,
    'Read-only epoch126 route oracle query failed: ' + JSON.stringify(rawSummary));
  assert.equal(rawSummary.stdoutJsonComplete, true, 'Raw epoch126 query must return complete JSON rows.');
  const jsonStart = rawResult.stdout.indexOf('[');
  assert(jsonStart >= 0, 'Raw epoch126 query returned no JSON array.');
  const rawRows = JSON.parse(rawResult.stdout.slice(jsonStart));
  const checkedRaw = assertCdaSavedRelatedRouteRows(rawRows, oracle);
  cda.report.target.cdaSavedRouteRawOracle = {
    fixturePath: oracle.provenance?.report,
    fixtureSha256: oracle.provenance?.attachmentSha256,
    scope: oracle.scope,
    routeA: { label: oracle.routeA.displayLabel, signature: oracle.routeA.signature, rows: routeA },
    routeB: { label: oracle.routeB.displayLabel, signature: oracle.routeB.signature, rows: routeB },
    rawReferenceCounts: checkedRaw.rawReferenceCounts,
    querySummary: rawSummary,
    elapsedMs: Date.now() - rawStarted,
  };
  check('correctness', 'raw epoch126 oracle confirms exact direct and Patient route Observation identities',
    checkedRaw.routeA.rows.length === 1 && checkedRaw.routeB.rows.length === 31
      && checkedRaw.rawReferenceCounts.present === 29 && checkedRaw.rawReferenceCounts.nullOrMissing === 2,
    cda.report.target.cdaSavedRouteRawOracle);

  const explorersResponse = await cda.request.get(
    `${target.apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers`, { timeout: 30_000 });
  assert.equal(explorersResponse.status(), 200, 'The exact owned CDA project Explorer list must return HTTP 200.');
  const bootstrapExplorerId = resolveSavedRouteBootstrapExplorer(await explorersResponse.json(), {
    project,
    preferredExplorer: target.explorer ?? null,
  });
  target.bootstrapExplorerId = bootstrapExplorerId;
  cda.report.target.bootstrapExplorerId = bootstrapExplorerId;
  const entryURL = browserURL(target, project, bootstrapExplorerId, 'builder');
  const navigationStarted = Date.now();
  await page.goto(entryURL, { waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await page.getByText('New explorer', { exact: true }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const navigationMs = Date.now() - navigationStarted;
  cda.report.timings['open owned CDA Builder'] = navigationMs;
  check('performance', 'open owned CDA Builder completes within five seconds', navigationMs <= ACTION_TIMEOUT_MS,
    { elapsedMs: navigationMs, budgetMs: ACTION_TIMEOUT_MS });

  const newExplorer = page.getByText('New explorer', { exact: true });
  await cda.action('open fresh epoch126 route-edit Explorer creation', newExplorer, () => newExplorer.click(), {
    after: async () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
  });
  const explorerName = page.locator('#new-explorer-name');
  await cda.action('name fresh epoch126 route-edit Explorer', explorerName, () => explorerName.fill(title), {
    timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS, editable: true,
  });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await cda.action('create fresh epoch126 route-edit Explorer', createBlank, () => createBlank.click(), {
    after: async () => waitForExplorerTitle(page, title),
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
  });
  const explorerId = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorerId && explorerId !== bootstrapExplorerId,
    'Native setup must create its own fresh CDA Explorer.');
  target.explorer = explorerId;
  cda.report.target.explorer = explorerId;
  const authoringRoot = apiRoot + encodeURIComponent(explorerId) + '/authoring/v2';

  const tableName = page.locator('#first-table-name');
  await cda.action('name the native Specimen root table', tableName, () => tableName.fill('Specimen route witness'), {
    timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS, editable: true,
  });
  const chooseSpecimen = page.getByRole('button', { name: 'Choose Specimen rows', exact: true });
  const rootStarted = Date.now();
  await cda.action('create native Specimen source table', chooseSpecimen, () => chooseSpecimen.click(), {
    after: async () => {
      await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      await waitForCurrentPreview(page);
    },
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
  });
  const rootMs = Date.now() - rootStarted;
  check('performance', 'native Specimen source table renders within five seconds', rootMs <= ACTION_TIMEOUT_MS,
    { elapsedMs: rootMs, budgetMs: ACTION_TIMEOUT_MS });

  let builder = await builderJSON(cda.request, target.apiOrigin, authoringRoot);
  assert.equal(builder.catalog?.generation, generation,
    'Native source table must stay in the exact pinned epoch126 generation.');
  assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest,
    'Native source table must use the authorized CDA Builder catalog.');
  let rootDocument = (builder.workspace?.documents ?? []).find(document => document.rootResourceType === 'Specimen');
  assert(rootDocument?.output?.id, 'Native first-table authoring must retain the Specimen output identity.');
  const sourceIDColumn = resolveSavedRouteSourceIDColumn(rootDocument, 'Specimen');
  const outputId = rootDocument.output.id;
  const rootLabel = sourceIDColumn.label;
  assert(rootLabel, 'Specimen.id must retain its user-visible source label.');

  const filterRows = page.getByTestId('construction-action-keep-rows');
  await cda.action('open native Specimen row filter', filterRows, () => filterRows.click(), {
    after: async () => page.getByTestId('construction-filter-editor').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
    timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
  });
  const filterColumn = page.getByRole('combobox', { name: 'Column', exact: true });
  const nativeFilterColumnIDs = await filterColumn.locator('option').evaluateAll(options =>
    options.map(option => option.value));
  assert.deepEqual(nativeFilterColumnIDs, [sourceIDColumn.columnId],
    'Native Filter rows options must bind the exact Builder source columnId.');
  await cda.action('select Specimen.id for the pinned native row filter', filterColumn,
    () => filterColumn.selectOption(sourceIDColumn.columnId), {
      after: async () => page.getByRole('combobox', { name: 'Condition', exact: true }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  const filterCondition = page.getByRole('combobox', { name: 'Condition', exact: true });
  await cda.action('set Specimen.id filter to exact equality', filterCondition,
    () => filterCondition.selectOption('EQUALS'), { timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS });
  const filterValue = page.getByRole('textbox', { name: 'Value', exact: true });
  await cda.action('pin native Specimen row filter to epoch126 witness', filterValue,
    () => filterValue.fill(oracle.scope.specimen.id), {
      after: async () => waitForProposal(page),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS, editable: true,
    });
  const filterProposalGrid = await readGrid(page, true);
  const filterIDIndex = filterProposalGrid.headers.indexOf(rootLabel);
  assert(filterIDIndex >= 0, 'Specimen filter proposal must show the semantic Specimen.id header.');
  assert.deepEqual(filterProposalGrid.rows.map(row => row[filterIDIndex]), [oracle.scope.specimen.id],
    'Native filter proposal must select only the pinned Specimen witness.');
  const applyFilter = page.getByTestId('construction-apply-proposal');
  const filterApplyDeadline = Date.now() + ACTION_TIMEOUT_MS;
  await cda.action('apply native Specimen witness row filter', applyFilter, () => applyFilter.click(), {
    after: async () => waitForGrid(page, {
      rootLabel, relatedLabel: rootLabel, expected: [[oracle.scope.specimen.id, oracle.scope.specimen.id]],
    }, filterApplyDeadline),
    timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
  });
  // The root preview has one semantic column before RelatedExpand; inspect its exact text separately.
  const rootGrid = await readGrid(page);
  assert.equal(rootGrid.headers.includes(rootLabel), true, 'Pinned Specimen root preview must retain its exact id column.');
  assert.deepEqual(rootGrid.rows.map(row => row[rootGrid.headers.indexOf(rootLabel)]), [oracle.scope.specimen.id],
    'Pinned Specimen root preview must contain only the exact epoch126 witness.');
  const sourceOnlyContract = {
    rootLabel,
    rootID: oracle.scope.specimen.id,
    relatedLabel: outputRelatedLabel,
    headers: rootGrid.headers,
  };

  builder = await builderJSON(cda.request, target.apiOrigin, authoringRoot);
  rootDocument = documentForOutput(builder, outputId);
  assert.equal(rootDocument.rootResourceType, 'Specimen');
  const filterSteps = (rootDocument.construction?.steps ?? []).filter(step => step.operation?.kind === 'FILTER');
  assert.equal(filterSteps.length, 1, 'Native filter Apply must persist exactly one FILTER step.');
  assert(JSON.stringify(filterSteps[0]).includes(oracle.scope.specimen.id),
    'Persisted native FILTER step must retain the exact pinned Specimen ID.');
  const filterStepBaseline = structuredClone(filterSteps[0]);
  const populationBaseline = structuredClone(rootDocument.population ?? null);
  check('correctness', 'native Specimen filter preserves one exact selected root row without population replacement', true, {
    outputId, rootColumnId: sourceIDColumn.columnId, filterStepId: filterStepBaseline.id,
    specimenId: oracle.scope.specimen.id, population: populationBaseline,
  });

  const openRelatedEditor = async label => {
    const settings = page.getByTestId('construction-rows-settings-trigger');
    await cda.action(label + ': open row settings', settings, () => settings.click(), {
      after: async () => page.getByTestId('construction-action-related-rows').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
    const actionButton = page.getByTestId('construction-action-related-rows');
    await cda.action(label + ': open RelatedExpand editor', actionButton, () => actionButton.click(), {
      after: async () => page.getByTestId('construction-related-expand-editor').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  };
  const observeRoutes = async phase => {
    const { direct, viaPatient } = await ensureExactRelatedRouteChoices(page, {
      directLabel: oracle.routeA.displayLabel,
      viaPatientLabel: oracle.routeB.displayLabel,
      phase,
      onOpenDisclosure: async ({ phase: actionPhase, summary, perform, after, timeout, budget }) =>
        cda.action(actionPhase + ': disclose alternate relationship paths', summary, perform, {
          after,
          timeout,
          budget,
        }),
    });
    const state = {
      direct: { label: oracle.routeA.displayLabel, visible: await direct.isVisible(), enabled: await direct.isEnabled() },
      viaPatient: { label: oracle.routeB.displayLabel, visible: await viaPatient.isVisible(), enabled: await viaPatient.isEnabled() },
    };
    cda.report.target.offeredSpecimenObservationRoutes = state;
    check('usability', 'native chooser offers exact direct and Patient routes',
      state.direct.visible && state.direct.enabled && state.viaPatient.visible && state.viaPatient.enabled,
      { phase, ...state });
    return { direct, viaPatient };
  };
  const selectRoute = async (radio, label, expected, proposalExpectedIDs) => {
    const deadline = Date.now() + ACTION_TIMEOUT_MS;
    return cda.action(label, radio, () => radio.check(), {
      after: async () => {
        await waitForProposal(page, remainingUntil(deadline));
        if (proposalExpectedIDs.length <= 25) {
          await waitForGrid(page, {
            rootLabel, relatedLabel: outputRelatedLabel, expected, proposal: true,
          }, deadline);
        }
      },
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  };
  const applyRelatedProposal = async (label, expectedVisiblePairs) => {
    const apply = page.getByTestId('construction-apply-proposal');
    const applyDeadline = Date.now() + ACTION_TIMEOUT_MS;
    let rendered;
    await cda.action(label, apply, () => apply.click(), {
      after: async () => {
        await waitForCurrentPreview(page, remainingUntil(applyDeadline));
        const limit = page.getByRole('combobox', { name: 'Preview row limit', exact: true });
        if (Number(await limit.inputValue()) >= expectedVisiblePairs.length) {
          rendered = await waitForGrid(page, {
            rootLabel, relatedLabel: outputRelatedLabel, expected: expectedVisiblePairs, phase: label,
          }, applyDeadline);
        }
      },
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
    const limit = page.getByRole('combobox', { name: 'Preview row limit', exact: true });
    if (Number(await limit.inputValue()) < expectedVisiblePairs.length) {
      const limitDeadline = Date.now() + ACTION_TIMEOUT_MS;
      await cda.action(label + ': set visible preview limit to 50', limit,
        () => limit.selectOption('50'), {
          after: async () => {
            rendered = await waitForGrid(page, {
              rootLabel, relatedLabel: outputRelatedLabel, expected: expectedVisiblePairs, phase: label,
            }, limitDeadline);
          },
          timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
        });
    }
    assert(rendered, label + ': exact virtualized table rows were not collected.');
    return rendered;
  };
  const inspectSaved = async (phase, expectedRouteSignature, expectedPairs, baseline, routeIdentity = null) => {
    const builderValue = await builderJSON(cda.request, target.apiOrigin, authoringRoot);
    const saved = savedState(builderValue, outputId, sourceIDColumn.columnId);
    assertStableIdentity(saved, baseline, phase);
    assertSavedRouteSignature(saved.route, expectedRouteSignature, phase);
    if (routeIdentity) {
      assert.deepEqual(saved.route, routeIdentity.route,
        phase + ': saved route identities or match mode changed.');
    } else if (baseline && isDeepStrictEqual(expectedRouteSignature, baseline.routeSignature)) {
      assert.deepEqual(saved.route, baseline.route,
        phase + ': unchanged saved route identities or match mode changed.');
    }
    assert.equal(saved.targetResourceType, 'Observation', phase + ': target resource type changed.');
    assert.equal(saved.relatedLabel, outputRelatedLabel, phase + ': Observation identity label changed.');
    return saved;
  };
  const reloadAndAssert = async (phase, expectedRouteSignature, expectedPairs, baseline, routeIdentity = null) => {
    const card = page.getByTestId('construction-table-' + outputId);
    await cda.action('reload saved CDA Builder after ' + phase, card,
      async () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
        after: async () => page.getByTestId('construction-table-' + outputId)
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
        timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
      });
    const tableCard = page.getByTestId('construction-table-' + outputId);
    const openOutputDeadline = Date.now() + ACTION_TIMEOUT_MS;
    let rendered;
    await cda.action('open saved Specimen output after ' + phase + ' reload', tableCard,
      () => tableCard.click(), {
        after: async () => {
          await waitForCurrentPreview(page, remainingUntil(openOutputDeadline));
          const rowLimit = page.getByRole('combobox', { name: 'Preview row limit', exact: true });
          if (Number(await rowLimit.inputValue()) >= expectedPairs.length) {
            rendered = await waitForGrid(page, {
              rootLabel, relatedLabel: outputRelatedLabel, expected: expectedPairs, phase: phase + ' reload',
            }, openOutputDeadline);
          }
        },
        timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
      });
    const rowLimit = page.getByRole('combobox', { name: 'Preview row limit', exact: true });
    if (Number(await rowLimit.inputValue()) < expectedPairs.length) {
      const limitDeadline = Date.now() + ACTION_TIMEOUT_MS;
      await cda.action('show all saved CDA route rows after ' + phase + ' reload', rowLimit,
        () => rowLimit.selectOption('50'), {
          after: async () => {
            rendered = await waitForGrid(page, {
              rootLabel, relatedLabel: outputRelatedLabel, expected: expectedPairs, phase: phase + ' reload',
            }, limitDeadline);
          },
          timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
        });
    }
    assert(rendered, phase + ' reload: exact virtualized table rows were not collected.');
    const saved = await inspectSaved(phase + ' reload', expectedRouteSignature, expectedPairs, baseline, routeIdentity);
    return saved;
  };

  await openRelatedEditor('add initial RelatedExpand');
  const relatedType = page.getByRole('combobox', { name: 'Related record type', exact: true });
  await cda.action('choose Observation related records', relatedType,
    () => relatedType.selectOption('Observation'), {
      after: async () => {
        await page.getByRole('radio', { name: oracle.routeA.displayLabel, exact: true })
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await page.getByTestId('construction-related-expand-other-routes')
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      },
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  const initialRoutes = await observeRoutes('initial native RelatedExpand editor');
  await selectRoute(initialRoutes.direct, 'preview direct Specimen-to-Observation route', expectedA, [
    oracle.routes.direct.observationIds[0],
  ]);
  await assertVisiblePairs(page, {
    rootLabel, relatedLabel: outputRelatedLabel, expected: expectedA, proposal: true,
    phase: 'direct route A proposal',
  });
  await applyRelatedProposal('apply direct Specimen-to-Observation route A', expectedA);
  let directSaved = savedState(await builderJSON(cda.request, target.apiOrigin, authoringRoot), outputId, sourceIDColumn.columnId);
  directSaved.routeSignature = assertSavedRouteSignature(directSaved.route, oracle.routeA.signature,
    'Initial route A');
  assert.equal(directSaved.anchorColumnId, '_key',
    'RelatedExpand must continue to use the original Specimen _key row anchor.');
  assert.equal(directSaved.emptyPolicy, 'PRESERVE_PARENT',
    'RelatedExpand route changes must preserve their existing parent-row policy.');
  assert.deepEqual(directSaved.filterStep, filterStepBaseline,
    'Initial RelatedExpand must preserve the exact native Specimen filter.');
  assert.deepEqual(directSaved.population, populationBaseline,
    'Initial RelatedExpand must preserve the selected Specimen population.');
  check('persistence', 'direct route A retains the pinned Specimen root and exact filter', true, {
    outputId, stepId: directSaved.stepId, relatedColumnId: directSaved.relatedColumnId,
    route: directSaved.route, specimenId: oracle.scope.specimen.id,
  });

  const directBaseline = { ...directSaved, document: structuredClone(directSaved.document) };
  const openSavedEdit = async phase => {
    const settings = page.getByTestId('construction-rows-settings-trigger');
    await cda.action(phase + ': open saved row history', settings, () => settings.click(), {
      after: async () => page.getByTestId('construction-row-operation-history')
        .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
    const edit = page.getByTestId('construction-row-edit-' + directBaseline.stepId);
    const editor = page.getByTestId('construction-related-expand-editor');
    const alternatePaths = page.getByTestId('construction-related-expand-other-routes');
    const readyDeadline = Date.now() + ACTION_TIMEOUT_MS;
    const remaining = () => Math.max(1, readyDeadline - Date.now());
    await cda.action(phase + ': edit the saved RelatedExpand step', edit, () => edit.click(), {
      after: async () => {
        await editor.waitFor({ state: 'visible', timeout: remaining() });
        await alternatePaths.waitFor({ state: 'visible', timeout: remaining() });
      },
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
    return await observeRoutes(phase + ' saved-route editor');
  };

  const cancelRoutes = await openSavedEdit('prepare A-to-B Cancel');
  await selectRoute(cancelRoutes.viaPatient, 'preview exact Patient-mediated route before Cancel', expectedB,
    oracle.routes.viaPatient.observationIds);
  const cancelProposal = await assertCandidateSubset(page, {
    rootLabel, rootID: oracle.scope.specimen.id, relatedLabel: outputRelatedLabel,
    expectedIDs: oracle.routes.viaPatient.observationIds,
    phase: 'Patient-mediated route proposal before Cancel',
  });
  check('correctness', 'Patient-mediated candidate contains only raw epoch126 Observation IDs', true, {
    candidatePreviewRows: cancelProposal.length, rawRouteRows: expectedB.length,
  });
  const cancel = page.getByTestId('construction-cancel-proposal');
  await cda.action('Cancel A-to-B saved route edit', cancel, () => cancel.click(), {
    after: async () => page.getByTestId('construction-related-expand-editor')
      .waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
    timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
  });
  await assertVisiblePairs(page, {
    rootLabel, relatedLabel: outputRelatedLabel, expected: expectedA, phase: 'route A after edit Cancel',
  });
  directSaved = await inspectSaved('A-to-B Cancel', oracle.routeA.signature, expectedA, directBaseline);
  assert.equal(directSaved.draftVersion, directBaseline.draftVersion,
    'Cancel must preserve the exact saved draft version.');
  assert.equal(directSaved.draftDigest, directBaseline.draftDigest,
    'Cancel must preserve the exact saved draft digest.');
  assert.deepEqual(directSaved.document, directBaseline.document,
    'Cancel must preserve the complete saved Specimen table document.');
  check('persistence', 'Cancel leaves saved route A, filter, population, and output identity unchanged', true, {
    outputId, stepId: directSaved.stepId, relatedColumnId: directSaved.relatedColumnId,
    draftVersion: directSaved.draftVersion, draftDigest: directSaved.draftDigest, rows: expectedA,
  });
  await reloadAndAssert('A-to-B Cancel', oracle.routeA.signature, expectedA, directBaseline);

  const applyRoutes = await openSavedEdit('prepare A-to-B Apply');
  await selectRoute(applyRoutes.viaPatient, 'preview Patient-mediated route for saved Apply', expectedB,
    oracle.routes.viaPatient.observationIds);
  const applyCandidateRows = await assertCandidateSubset(page, {
    rootLabel, rootID: oracle.scope.specimen.id, relatedLabel: outputRelatedLabel,
    expectedIDs: oracle.routes.viaPatient.observationIds,
    phase: 'Patient-mediated route proposal for Apply',
  });
  await applyRelatedProposal('Apply A-to-B Patient-mediated route edit', expectedB);
  const viaPatientSaved = await inspectSaved('A-to-B Apply', oracle.routeB.signature, expectedB, directBaseline);
  assert(viaPatientSaved.draftVersion > directBaseline.draftVersion,
    'Applying route B must advance the saved draft version.');
  assert.notEqual(viaPatientSaved.draftDigest, directBaseline.draftDigest,
    'Applying route B must change the saved draft digest.');
  check('persistence', 'A-to-B Apply keeps the original step, output, filter, population, and all 31 rows', true, {
    outputId, stepId: viaPatientSaved.stepId, relatedColumnId: viaPatientSaved.relatedColumnId,
    candidatePreviewRows: applyCandidateRows.length, exactRows: expectedB.length,
    draftVersion: viaPatientSaved.draftVersion, draftDigest: viaPatientSaved.draftDigest,
  });
  const viaPatientReload = await reloadAndAssert(
    'A-to-B Apply', oracle.routeB.signature, expectedB, directBaseline, viaPatientSaved);
  check('persistence', 'Patient-mediated route B survives reload with the exact 31-row multiset', true, {
    outputId, stepId: viaPatientReload.stepId, relatedColumnId: viaPatientReload.relatedColumnId, rows: expectedB,
  });

  const restoreRoutes = await openSavedEdit('prepare B-to-A restoration');
  await selectRoute(restoreRoutes.direct, 'preview direct route for B-to-A restoration', expectedA,
    oracle.routes.direct.observationIds);
  await assertVisiblePairs(page, {
    rootLabel, relatedLabel: outputRelatedLabel, expected: expectedA, proposal: true,
    phase: 'direct route A restoration proposal',
  });
  await applyRelatedProposal('Apply B-to-A direct route restoration', expectedA);
  const restoredSaved = await inspectSaved('B-to-A restoration Apply', oracle.routeA.signature, expectedA, directBaseline);
  assert(restoredSaved.draftVersion > viaPatientReload.draftVersion,
    'Restoring route A must advance the draft beyond route B.');
  assert.notEqual(restoredSaved.draftDigest, viaPatientReload.draftDigest,
    'Restoring route A must change the route B draft digest.');
  check('persistence', 'B-to-A restoration keeps original route identities and exact one-row result', true, {
    outputId, stepId: restoredSaved.stepId, relatedColumnId: restoredSaved.relatedColumnId,
    rows: expectedA, draftVersion: restoredSaved.draftVersion,
  });
  const restoredReload = await reloadAndAssert('B-to-A restoration', oracle.routeA.signature, expectedA, directBaseline);
  assert.equal(restoredReload.stepId, directBaseline.stepId);
  assert.equal(restoredReload.relatedColumnId, directBaseline.relatedColumnId);

  const openSavedRemovalControls = async phase => {
    const settings = page.getByTestId('construction-rows-settings-trigger');
    const history = page.getByTestId('construction-row-operation-history');
    const remove = page.getByTestId('construction-row-remove-' + restoredReload.stepId);
    await cda.action(phase, settings, () => settings.click(), {
      after: async () => {
        await history.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await remove.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        assert(await remove.isEnabled(), `${phase}: saved RelatedExpand removal control must be enabled.`);
      },
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
    return remove;
  };
  const removeRelatedStep = await openSavedRemovalControls('open row operations before RelatedExpand removal preview');
  await cda.action('open native removal preview for saved RelatedExpand', removeRelatedStep,
    () => removeRelatedStep.click(), {
      after: async () => {
        await waitForProposal(page);
        await waitForSourceOnlyGrid(page, { ...sourceOnlyContract, proposal: true });
      },
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  const removalCandidate = await assertSourceOnlyGrid(page, sourceOnlyContract, {
    proposal: true, phase: 'RelatedExpand removal proposal',
  });
  const beforeRemoval = await builderJSON(cda.request, target.apiOrigin, authoringRoot);
  const beforeRemovalState = savedState(beforeRemoval, outputId, sourceIDColumn.columnId);
  assert.equal(beforeRemovalState.draftVersion, restoredReload.draftVersion,
    'Removal preview must leave the saved draft version unchanged.');
  assert.equal(beforeRemovalState.draftDigest, restoredReload.draftDigest,
    'Removal preview must leave the saved draft digest unchanged.');
  assert.deepEqual(beforeRemovalState.document, restoredReload.document,
    'Removal preview must leave the saved route-A document unchanged.');
  check('correctness', 'native RelatedExpand removal preview returns the exact filtered source-only schema', true, {
    candidateRows: removalCandidate.rows.length, headers: removalCandidate.headers,
  });
  const cancelRemoval = page.getByTestId('construction-cancel-proposal');
  await cda.action('Cancel native RelatedExpand removal', cancelRemoval,
    () => cancelRemoval.click(), {
      after: async () => page.getByTestId('construction-proposal-panel')
        .waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  await assertVisiblePairs(page, {
    rootLabel, relatedLabel: outputRelatedLabel, expected: expectedA,
    phase: 'route A after removal Cancel',
  });
  const afterRemovalCancel = savedState(await builderJSON(cda.request, target.apiOrigin, authoringRoot), outputId, sourceIDColumn.columnId);
  assert.equal(afterRemovalCancel.draftVersion, restoredReload.draftVersion,
    'Removal Cancel must preserve the exact saved draft version.');
  assert.equal(afterRemovalCancel.draftDigest, restoredReload.draftDigest,
    'Removal Cancel must preserve the exact saved draft digest.');
  assert.deepEqual(afterRemovalCancel.document, restoredReload.document,
    'Removal Cancel must preserve the complete saved route-A document.');
  check('persistence', 'Cancel removal preserves route A, source row, filter, and saved identities', true, {
    outputId, stepId: afterRemovalCancel.stepId, relatedColumnId: afterRemovalCancel.relatedColumnId,
    draftVersion: afterRemovalCancel.draftVersion, draftDigest: afterRemovalCancel.draftDigest,
    rows: expectedA,
  });

  const removeAgain = await openSavedRemovalControls('reopen row operations before RelatedExpand removal Apply');
  await cda.action('preview saved RelatedExpand removal for Apply', removeAgain,
    () => removeAgain.click(), {
      after: async () => {
        await waitForProposal(page);
        await waitForSourceOnlyGrid(page, { ...sourceOnlyContract, proposal: true });
      },
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  await assertSourceOnlyGrid(page, sourceOnlyContract, {
    proposal: true, phase: 'RelatedExpand removal before Apply',
  });
  const applyRemoval = page.getByTestId('construction-apply-proposal');
  await cda.action('Apply native RelatedExpand removal to filtered Specimen source table',
    applyRemoval, () => applyRemoval.click(), {
      after: async () => waitForSourceOnlyGrid(page, sourceOnlyContract),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  await assertSourceOnlyGrid(page, sourceOnlyContract, { phase: 'filtered source-only table after removal Apply' });
  const sourceOnlyApplied = sourceOnlyState(await builderJSON(cda.request, target.apiOrigin, authoringRoot), outputId, {
    rootColumnId: sourceIDColumn.columnId,
    filterStepId: filterStepBaseline.id,
    relatedColumnId: restoredReload.relatedColumnId,
    population: populationBaseline,
  }, 'RelatedExpand removal Apply');
  assert(sourceOnlyApplied.draftVersion > restoredReload.draftVersion,
    'Applying removal must advance the saved draft version.');
  assert.notEqual(sourceOnlyApplied.draftDigest, restoredReload.draftDigest,
    'Applying removal must change the saved draft digest.');
  check('persistence', 'Apply removal retains the original Specimen output and source-only schema', true, {
    outputId, rootColumnId: sourceIDColumn.columnId, filterStepId: filterStepBaseline.id,
    sourceRows: [oracle.scope.specimen.id], draftVersion: sourceOnlyApplied.draftVersion,
    draftDigest: sourceOnlyApplied.draftDigest,
  });
  const sourceOnlyCard = page.getByTestId('construction-table-' + outputId);
  await cda.action('reload filtered Specimen source-only table after removal', sourceOnlyCard,
    async () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => page.getByTestId('construction-table-' + outputId)
        .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  await cda.action('open filtered Specimen source-only output after removal reload',
    page.getByTestId('construction-table-' + outputId),
    () => page.getByTestId('construction-table-' + outputId).click(), {
      after: async () => waitForSourceOnlyGrid(page, sourceOnlyContract),
      timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
    });
  const sourceOnlyReload = sourceOnlyState(await builderJSON(cda.request, target.apiOrigin, authoringRoot), outputId, {
    rootColumnId: sourceIDColumn.columnId,
    filterStepId: filterStepBaseline.id,
    relatedColumnId: restoredReload.relatedColumnId,
    population: populationBaseline,
  }, 'filtered source-only reload');
  await assertSourceOnlyGrid(page, sourceOnlyContract, { phase: 'filtered source-only reload' });
  assert.equal(sourceOnlyReload.document.output.id, outputId,
    'Source-only reload must retain the exact original output identity.');
  check('persistence', 'filtered Specimen source-only schema and row survive removal reload', true, {
    outputId, rootColumnId: sourceIDColumn.columnId, filterStepId: filterStepBaseline.id,
    rows: [oracle.scope.specimen.id], headers: sourceOnlyContract.headers,
  });

  const selectionWrites = cda.report.nativeRequests.filter(request => request.isMutation
    && /\/selections(?:\/|$)/.test(request.path ?? ''));
  assert.deepEqual(selectionWrites, [], 'Route editing must not replace or mutate the selected population.');
  check('persistence', 'RelatedExpand route edits do not issue selection replacement requests',
    selectionWrites.length === 0, { requests: selectionWrites });
  cda.report.target.savedCdaRouteEditFinal = {
    status: 'complete', project, generation, explorerId, outputId,
    rootResourceType: 'Specimen', rootColumnId: sourceIDColumn.columnId,
    filterStepId: filterStepBaseline.id, relatedStepId: restoredReload.stepId,
    relatedColumnId: restoredReload.relatedColumnId,
    route: oracle.routeA.signature,
    restoredRows: expectedA,
    sourceOnlyOutputId: sourceOnlyReload.document.output.id,
    sourceOnlyRows: [oracle.scope.specimen.id],
    routeBRows: expectedB,
    rawReferenceCounts: checkedRaw.rawReferenceCounts,
  };
  assert.deepEqual(cda.report.errors, [], 'Unexpected browser HTTP, runtime, console, or module errors occurred.');
  check('correctness', 'browser-errors', cda.report.errors.length === 0, { errors: cda.report.errors });
}
