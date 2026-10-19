import assert from 'node:assert/strict';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import {
  BASIC_SAVED_RELATED_ROUTE_A,
  BASIC_SAVED_RELATED_ROUTE_B,
  assertRouteSignature,
  readBasicSavedRelatedRouteOracle,
} from '../helpers/basic-saved-related-route-oracle.mjs';

const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const visibleScalar = value => {
  const result = normalize(value);
  return result === '' || result === '—' ? null : result;
};
const sortedPairs = pairs => [...pairs].sort((left, right) =>
  left[0].localeCompare(right[0]) || String(left[1] ?? '').localeCompare(String(right[1] ?? '')));

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
  const patientIndex = headers.indexOf(contract.patientLabel);
  const relatedIndex = headers.indexOf(contract.relatedLabel);
  if (patientIndex < 0 || relatedIndex < 0 || rows.length !== contract.expected.length) return false;
  const actual = rows.map(row => [row[patientIndex], asScalar(row[relatedIndex])])
    .sort((left, right) => left[0].localeCompare(right[0])
      || String(left[1] ?? '').localeCompare(String(right[1] ?? '')));
  return JSON.stringify(actual) === JSON.stringify(contract.expected);
};

const patientIDsMatch = contract => {
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
  const patientIndex = headers.indexOf(contract.patientLabel);
  if (patientIndex < 0 || rows.length !== contract.expected.length) return false;
  const actual = rows.map(row => row[patientIndex]).sort();
  return JSON.stringify(actual) === JSON.stringify(contract.expected);
};

const waitForGrid = (page, { patientLabel, relatedLabel, expected, proposal = false }) =>
  page.waitForFunction(gridMatches, { patientLabel, relatedLabel, expected, proposal }, { timeout: 5000 });

const waitForPatientIDs = (page, { patientLabel, expected, proposal = false }) =>
  page.waitForFunction(patientIDsMatch, { patientLabel, expected, proposal }, { timeout: 5000 });

const assertVisiblePairs = async (page, {
  patientLabel, relatedLabel, expected, proposal = false, phase,
}) => {
  const grid = await readGrid(page, proposal);
  const patientIndex = grid.headers.indexOf(patientLabel);
  const relatedIndex = grid.headers.indexOf(relatedLabel);
  assert(patientIndex >= 0 && relatedIndex >= 0,
    phase + ': preview omitted one of the exact fixture identity columns: ' + JSON.stringify(grid.headers));
  const pairs = sortedPairs(grid.rows.map(row => [row[patientIndex], visibleScalar(row[relatedIndex])]));
  assert.deepEqual(pairs, expected, phase + ': rendered Patient/Observation pairs differ from independent raw fixture rows.');
  return { headers: grid.headers, pairs };
};

const assertVisiblePatientIDs = async (page, { patientLabel, expected, proposal = false, phase }) => {
  const grid = await readGrid(page, proposal);
  const patientIndex = grid.headers.indexOf(patientLabel);
  assert(patientIndex >= 0,
    phase + ': preview omitted the exact Patient identity column: ' + JSON.stringify(grid.headers));
  const patientIDs = grid.rows.map(row => row[patientIndex]).sort();
  assert.deepEqual(patientIDs, expected, phase + ': rendered Patient IDs differ from the literal fixture rows.');
  return { headers: grid.headers, patientIDs };
};

const apiJSON = async (target, apiRoot, suffix) => {
  const response = await fetch(target.apiUrl + apiRoot + suffix, {
    signal: AbortSignal.timeout(10_000),
  });
  assert(response.ok, 'Read-only Builder inspection returned HTTP ' + response.status + ' for ' + suffix);
  return response.json();
};

const tableForOutput = (builder, outputId) => {
  const matches = (builder.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, 'The exact output ID must identify one saved Builder table.');
  return matches[0];
};

const savedRelatedState = (builder, outputId) => {
  const document = tableForOutput(builder, outputId);
  const steps = (document.construction?.steps ?? []).filter(step => step.operation?.kind === 'RELATED_EXPAND');
  assert.equal(steps.length, 1, 'The saved Patient table must contain exactly one RELATED_EXPAND step.');
  const step = steps[0];
  const related = step.operation.relatedExpand;
  const output = (step.outputs ?? []).find(column => column.id === related.relatedRecordColumnId);
  assert(output, 'The saved RelatedExpand must retain its exact output column identity.');
  return {
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
    document,
    step,
    stepId: step.id,
    outputId,
    relatedColumnId: related.relatedRecordColumnId,
    relatedLabel: output.label,
    route: related.route,
    emptyPolicy: related.emptyPolicy,
    targetResourceType: related.targetResourceType,
  };
};

const savedRootState = (builder, outputId) => {
  const document = tableForOutput(builder, outputId);
  const steps = document.construction?.steps ?? [];
  assert.deepEqual(steps, [], 'Removed RelatedExpand must leave no saved construction steps.');
  return { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, document, outputId };
};

const assertSavedIdentity = (actual, expected, phase) => {
  assert.equal(actual.outputId, expected.outputId, phase + ': table output identity changed.');
  assert.equal(actual.stepId, expected.stepId, phase + ': RelatedExpand step identity changed.');
  assert.equal(actual.relatedColumnId, expected.relatedColumnId, phase + ': related output column identity changed.');
  assert.equal(actual.relatedLabel, expected.relatedLabel, phase + ': related output label changed.');
};

const assertSavedRoute = (actual, {
  outputId, stepId, relatedColumnId, route, emptyPolicy = 'PRESERVE_PARENT', phase,
}) => {
  assert.equal(actual.outputId, outputId, phase + ': table output identity changed.');
  assert.equal(actual.stepId, stepId, phase + ': RelatedExpand step identity changed.');
  assert.equal(actual.relatedColumnId, relatedColumnId, phase + ': related output column identity changed.');
  assert.equal(actual.targetResourceType, 'Observation', phase + ': target resource type changed.');
  assert.equal(actual.emptyPolicy, emptyPolicy, phase + ': empty-parent policy changed.');
  assertRouteSignature(actual.route, route, phase);
};

export const savedRelatedExpandRouteEditWorkflow = async (workflow, context, { includeRemovalLifecycle = false } = {}) => {
  const { page, report, action, check } = workflow;
  configureNativePage(page);
  const target = context.target;
  const oracle = readBasicSavedRelatedRouteOracle(target.fixtureDir);
  const routeA = oracle.routeA.rows.map(row => [row.patientID, row.observationID]);
  const routeB = oracle.routeB.rows.map(row => [row.patientID, row.observationID]);
  report.target.savedRelatedRouteFixture = {
    files: oracle.files,
    patientIDs: oracle.patientIDs,
    routeA: { label: oracle.routeA.label, signature: oracle.routeA.signature, exactRows: routeA },
    routeB: { label: oracle.routeB.label, signature: oracle.routeB.signature, exactRows: routeB },
    scope: { project: target.fixtureProject, generation: target.fixtureGeneration, root: 'Patient', target: 'Observation' },
  };
  check('correctness', 'raw Basic fixture derives distinct literal rows for direct and J02 related routes',
    routeA.length === 3 && routeB.length === 2 && routeB[1][1] === null && JSON.stringify(routeA) !== JSON.stringify(routeB),
    report.target.savedRelatedRouteFixture);
  check('correctness', 'literal direct route has exactly one Observation for the second Patient',
    oracle.routeA.matchCountsByPatient['dev-patient-002'] === 1
      && routeA.filter(([patientID]) => patientID === 'dev-patient-002').length === 1,
    { patientID: 'dev-patient-002', directObservationCount: oracle.routeA.matchCountsByPatient['dev-patient-002'] });

  const title = 'Verify ' + context.runID.slice(-10) + ' saved route edit';
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'),
    { waitUntil: 'domcontentloaded', timeout: 5000 });
  const newExplorerLink = page.getByText('New explorer', { exact: true });
  await newExplorerLink.waitFor({ state: 'visible', timeout: 5000 });
  await action('open Explorer creation', newExplorerLink, () => newExplorerLink.click(), {
    after: async () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: 5000 }),
  });
  const explorerName = page.locator('#new-explorer-name');
  await action('name fresh route-edit Explorer', explorerName, () => explorerName.fill(title), { editable: true });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create fresh route-edit Explorer', createBlank, () => createBlank.click(), {
    after: async () => page.waitForFunction(expectedTitle =>
      document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expectedTitle,
    title, { timeout: 5000 }),
  });
  const explorer = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'Native route-edit setup must create its own fresh Explorer.');
  report.target.explorer = explorer;
  const routeApiRoot = '/api/v1/projects/' + encodeURIComponent(target.fixtureProject)
    + '/explorers/' + encodeURIComponent(explorer) + '/authoring/v2';

  const tableName = page.locator('#first-table-name');
  await action('name Patient source table', tableName, () => tableName.fill('Patients'), { editable: true });
  const choosePatientRows = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  const chooseStarted = Date.now();
  await action('create the native Patient root table', choosePatientRows, () => choosePatientRows.click(), {
    after: async () => {
      await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible', timeout: 5000 });
      await page.waitForFunction(() => {
        const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
        return table?.getAttribute('aria-rowcount') === '3'
          && !document.body.innerText.includes('Loading your table…');
      }, undefined, { timeout: 5000 });
    },
  });
  const chooseMs = Date.now() - chooseStarted;
  report.timings['create Patient root table action-to-render'] = chooseMs;
  check('performance', 'native Patient table setup finishes within five seconds', chooseMs <= 5000,
    { elapsedMs: chooseMs, budgetMs: 5000 });

  const initialBuilder = await apiJSON(target, routeApiRoot, '/builder');
  assert.equal(initialBuilder.catalog?.generation, target.fixtureGeneration,
    'Builder must read the exact fixture generation selected for this Basic run.');
  assert(initialBuilder.catalog?.snapshotToken && initialBuilder.catalog?.authorizationScopeDigest,
    'The native source table must stay within the current authorized Builder catalog.');
  const sourceDocument = (initialBuilder.workspace?.documents ?? [])
    .find(document => document.rootResourceType === 'Patient');
  assert(sourceDocument?.output?.id, 'Native first-table authoring must retain its Patient output identity.');
  const idColumns = (sourceDocument.columns ?? []).filter(column => column.source?.field?.path === 'id');
  assert.equal(idColumns.length, 1, 'Native Patient root must expose exactly one default Patient.id identity column.');
  const idColumn = idColumns[0];
  assert(idColumn.label, 'Patient.id output must retain its user-visible field label.');
  const outputId = sourceDocument.output.id;
  const patientLabel = idColumn.label;
  const expectedDirectRows = sortedPairs(routeA);
  const expectedJ02Rows = sortedPairs(routeB);
  const rootGrid = await readGrid(page);
  const rootPatientIndex = rootGrid.headers.indexOf(patientLabel);
  assert(rootPatientIndex >= 0, 'Native Patient root preview must render the exact Patient.id source column.');
  const rootIDs = rootGrid.rows.map(row => row[rootPatientIndex]).sort();
  assert.deepEqual(rootIDs, oracle.patientIDs, 'Native root preview must contain the exact two fixture Patient IDs.');
  check('correctness', 'native Patient root preview starts with the exact two fixture IDs', true,
    { patientLabel, rows: rootIDs, rawOracle: oracle.patientIDs });

  const openRowSettings = async label => action(label, page.getByTestId('construction-rows-settings-trigger'),
    () => page.getByTestId('construction-rows-settings-trigger').click(), {
      after: async () => page.getByTestId('construction-action-related-rows').waitFor({ state: 'visible', timeout: 5000 }),
    });
  const openRelatedEditor = async label => action(label, page.getByTestId('construction-action-related-rows'),
    () => page.getByTestId('construction-action-related-rows').click(), {
      after: async () => page.getByTestId('construction-related-expand-editor').waitFor({ state: 'visible', timeout: 5000 }),
    });
  const waitProposal = () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
    && document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready',
  undefined, { timeout: 5000 });
  const observeOfferedRoutes = async () => {
    const direct = page.getByRole('radio', { name: oracle.routeA.label, exact: true });
    const other = page.getByTestId('construction-related-expand-other-routes');
    await direct.waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await direct.isEnabled(), true, 'Direct Patient-to-Observation route must be an enabled native choice.');
    assert.equal(await other.count(), 1, 'The route selector must show at least one distinct alternative Observation route.');
    await action('open alternate related routes', other.locator('summary'),
      () => other.locator('summary').click(), {
        after: async () => page.getByRole('radio', { name: oracle.routeB.label, exact: true })
          .waitFor({ state: 'visible', timeout: 5000 }),
      });
    const alternate = page.getByRole('radio', { name: oracle.routeB.label, exact: true });
    assert.equal(await direct.count(), 1, 'Direct route choice must be unique.');
    assert.equal(await alternate.count(), 1, 'J02 route choice must be unique.');
    assert.equal(await alternate.isEnabled(), true, 'J02 route must be an enabled native choice.');
    report.target.offeredObservationRoutes = {
      direct: { label: oracle.routeA.label, visible: await direct.isVisible(), enabled: await direct.isEnabled() },
      j02: { label: oracle.routeB.label, visible: await alternate.isVisible(), enabled: await alternate.isEnabled() },
    };
    check('usability', 'native RelatedExpand offers both the direct and J02 Patient-to-Observation routes',
      await direct.isVisible() && await alternate.isVisible() && await direct.isEnabled() && await alternate.isEnabled(),
      report.target.offeredObservationRoutes);
    return { direct, alternate };
  };
  const chooseRoute = async (radio, label, expectedPairs) => action(label, radio, () => radio.check(), {
    after: async () => {
      await waitProposal();
      await waitForGrid(page, {
        proposal: true,
        patientLabel,
        relatedLabel: 'Observation FHIR resource ID',
        expected: expectedPairs,
      });
    },
  });
  const applyProposal = async (label, expectedPairs) => action(label, page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: async () => waitForGrid(page, {
        patientLabel, relatedLabel: 'Observation FHIR resource ID', expected: expectedPairs,
      }),
    });
  const openSavedEdit = async (stepId, label) => {
    await action(label + ': open saved row history', page.getByTestId('construction-rows-settings-trigger'),
      () => page.getByTestId('construction-rows-settings-trigger').click(), {
        after: async () => {
          await page.getByTestId('construction-action-related-rows').waitFor({ state: 'visible', timeout: 5000 });
          await page.getByTestId('construction-row-operation-history').waitFor({ state: 'visible', timeout: 5000 });
        },
      });
    const edit = page.getByTestId('construction-row-edit-' + stepId);
    await action(label + ': edit saved RelatedExpand', edit, () => edit.click(), {
      after: async () => {
        await page.getByTestId('construction-related-expand-editor').waitFor({ state: 'visible', timeout: 5000 });
        await page.getByTestId('construction-related-expand-other-routes').waitFor({ state: 'visible', timeout: 5000 });
      },
    });
    return await observeOfferedRoutesForSavedEdit();
  };
  const observeOfferedRoutesForSavedEdit = async () => {
    const other = page.getByTestId('construction-related-expand-other-routes');
    assert.equal(await other.count(), 1, 'Saved RelatedExpand edit must expose alternate route choices.');
    if (!await other.evaluate(element => element.open)) {
      await action('open alternate saved routes', other.locator('summary'),
        () => other.locator('summary').click(), {
          after: async () => page.getByRole('radio', { name: oracle.routeA.label, exact: true })
            .waitFor({ state: 'visible', timeout: 5000 }),
        });
    }
    const direct = page.getByRole('radio', { name: oracle.routeA.label, exact: true });
    const alternate = page.getByRole('radio', { name: oracle.routeB.label, exact: true });
    await Promise.all([
      direct.waitFor({ state: 'visible', timeout: 5000 }),
      alternate.waitFor({ state: 'visible', timeout: 5000 }),
    ]);
    assert.equal(await direct.isEnabled(), true, 'Saved edit must offer the direct route as a native choice.');
    assert.equal(await alternate.isEnabled(), true, 'Saved edit must offer the J02 route as a native choice.');
    return { direct, alternate };
  };
  const inspectSaved = async (phase, expectedRoute, expectedPairs, prior) => {
    const builder = await apiJSON(target, routeApiRoot, '/builder');
    const saved = savedRelatedState(builder, outputId);
    assertSavedRoute(saved, {
      outputId,
      stepId: prior.stepId,
      relatedColumnId: prior.relatedColumnId,
      route: expectedRoute,
      emptyPolicy: 'PRESERVE_PARENT',
      phase,
    });
    assert.equal(saved.relatedLabel, 'Observation FHIR resource ID', phase + ': related ID column label changed.');
    assert.equal(saved.document.rootResourceType, 'Patient', phase + ': table root changed.');
    await assertVisiblePairs(page, {
      patientLabel, relatedLabel: saved.relatedLabel, expected: expectedPairs, phase,
    });
    return saved;
  };
  const reloadAndAssert = async (phase, expectedRoute, expectedPairs, prior) => {
    const reloadStarted = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
    const tableCard = page.getByTestId('construction-table-' + outputId);
    await tableCard.waitFor({ state: 'visible', timeout: 5000 });
    await action('open Patient output after ' + phase + ' reload', tableCard, () => tableCard.click(), {
      after: async () => waitForGrid(page, {
        patientLabel,
        relatedLabel: 'Observation FHIR resource ID',
        expected: expectedPairs,
      }),
    });
    const saved = await inspectSaved(phase + ' reload', expectedRoute, expectedPairs, prior);
    const elapsedMs = Date.now() - reloadStarted;
    report.timings[phase + ' reload'] = elapsedMs;
    check('performance', phase + ' reload-to-settled rows completes within five seconds', elapsedMs <= 5000,
      { elapsedMs, budgetMs: 5000 });
    return saved;
  };

  const inspectRemovedState = async (phase, prior) => {
    const builder = await apiJSON(target, routeApiRoot, '/builder');
    const saved = savedRootState(builder, outputId);
    assert.equal(saved.document.rootResourceType, 'Patient', phase + ': table root changed.');
    assert.equal(saved.document.output.id, prior.outputId, phase + ': source table output identity changed.');
    const idColumns = (saved.document.columns ?? []).filter(column => column.source?.field?.path === 'id');
    assert.deepEqual(idColumns.map(column => column.id), [idColumn.id],
      phase + ': removal must preserve only the original Patient.id source column.');
    assert.equal((saved.document.columns ?? []).filter(column => column.id === prior.relatedColumnId).length, 0,
      phase + ': removed RelatedExpand output column remains in the saved document.');
    await assertVisiblePatientIDs(page, {
      patientLabel, expected: oracle.patientIDs, phase,
    });
    return saved;
  };
  const reloadAndAssertRemoved = async (phase, prior) => {
    const reloadStarted = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
    const tableCard = page.getByTestId('construction-table-' + outputId);
    await tableCard.waitFor({ state: 'visible', timeout: 5000 });
    await action('open Patient output after ' + phase + ' reload', tableCard, () => tableCard.click(), {
      after: async () => waitForPatientIDs(page, { patientLabel, expected: oracle.patientIDs }),
    });
    const saved = await inspectRemovedState(phase + ' reload', prior);
    const elapsedMs = Date.now() - reloadStarted;
    report.timings[phase + ' reload'] = elapsedMs;
    check('performance', phase + ' reload-to-settled rows completes within five seconds', elapsedMs <= 5000,
      { elapsedMs, budgetMs: 5000 });
    return saved;
  };
  const previewRelatedRemoval = async (phase, prior) => {
    await openRowSettings(phase + ': open saved row history');
    await page.getByTestId('construction-row-operation-history').waitFor({ state: 'visible', timeout: 5000 });
    const remove = page.getByTestId('construction-row-remove-' + prior.stepId);
    await action(phase + ': preview removal of exact RelatedExpand', remove, () => remove.click(), {
      after: async () => {
        await page.getByTestId('construction-proposal-ready').waitFor({ state: 'visible', timeout: 5000 });
        await page.getByTestId('construction-removal-step-' + prior.stepId)
          .waitFor({ state: 'visible', timeout: 5000 });
        await waitForPatientIDs(page, {
          proposal: true, patientLabel, expected: oracle.patientIDs,
        });
      },
    });
    const preview = await assertVisiblePatientIDs(page, {
      proposal: true, patientLabel, expected: oracle.patientIDs, phase: phase + ' source-only removal preview',
    });
    assert.equal(preview.headers.includes(prior.relatedLabel), false,
      phase + ': removal preview must omit the RelatedExpand output column.');
    assert.equal(await page.getByTestId('construction-apply-proposal').textContent(), 'Apply removal',
      phase + ': native removal preview must expose the Apply removal action.');
    return preview;
  };

  await openRowSettings('open row settings for direct route');
  await openRelatedEditor('open RelatedExpand editor for direct route');
  const recordType = page.getByRole('combobox', { name: 'Related record type', exact: true });
  await action('choose Observation as related record type', recordType, () => recordType.selectOption('Observation'), {
    after: async () => {
      await page.getByRole('radio', { name: oracle.routeA.label, exact: true })
        .waitFor({ state: 'visible', timeout: 5000 });
      await page.getByTestId('construction-related-expand-other-routes')
        .waitFor({ state: 'visible', timeout: 5000 });
    },
  });
  const offered = await observeOfferedRoutes();
  const emptyPolicy = page.getByRole('combobox', { name: 'If a current row has no matches', exact: true });
  assert.equal(await emptyPolicy.inputValue(), 'PRESERVE_PARENT',
    'Initial RelatedExpand must keep the PRESERVE_PARENT policy used by the exact null-row oracle.');
  await chooseRoute(offered.direct, 'preview direct Patient-to-Observation route', expectedDirectRows);
  await assertVisiblePairs(page, {
    patientLabel, relatedLabel: 'Observation FHIR resource ID',
    expected: expectedDirectRows, proposal: true, phase: 'direct route proposal',
  });
  check('correctness', 'direct route proposal equals all three literal Patient and Observation pairs', true,
    { rows: expectedDirectRows, rawOracle: routeA, route: oracle.routeA.signature });
  await applyProposal('Apply direct Patient-to-Observation route', expectedDirectRows);
  const directSaved = savedRelatedState(await apiJSON(target, routeApiRoot, '/builder'), outputId);
  assertSavedRoute(directSaved, {
    outputId, stepId: directSaved.stepId, relatedColumnId: directSaved.relatedColumnId,
    route: BASIC_SAVED_RELATED_ROUTE_A, phase: 'direct route Apply',
  });
  assert.equal(directSaved.relatedLabel, 'Observation FHIR resource ID');
  check('persistence', 'direct route Apply retains exact step and output IDs with route A', true, {
    outputId, stepId: directSaved.stepId, relatedColumnId: directSaved.relatedColumnId,
    exactRows: expectedDirectRows, draftVersion: directSaved.draftVersion, draftDigest: directSaved.draftDigest,
  });
  await assertVisiblePairs(page, {
    patientLabel, relatedLabel: directSaved.relatedLabel, expected: expectedDirectRows, phase: 'direct route after Apply',
  });

  if (includeRemovalLifecycle) {
    const removalBaseline = {
      ...directSaved,
      document: structuredClone(directSaved.document),
    };
    await previewRelatedRemoval('RelatedExpand removal Cancel', directSaved);
    const beforeCancel = savedRelatedState(await apiJSON(target, routeApiRoot, '/builder'), outputId);
    assert.deepEqual(beforeCancel.document, removalBaseline.document,
      'Removal preview must leave the complete saved source document unchanged before Cancel.');
    await action('Cancel exact-one RelatedExpand removal', page.getByTestId('construction-cancel-proposal'),
      () => page.getByTestId('construction-cancel-proposal').click(), {
        after: async () => page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached', timeout: 5000 }),
      });
    await assertVisiblePairs(page, {
      patientLabel, relatedLabel: directSaved.relatedLabel, expected: expectedDirectRows,
      phase: 'direct rows after removal Cancel',
    });
    const afterCancelRemoval = savedRelatedState(await apiJSON(target, routeApiRoot, '/builder'), outputId);
    assertSavedIdentity(afterCancelRemoval, removalBaseline, 'Cancel RelatedExpand removal');
    assert.equal(afterCancelRemoval.draftVersion, removalBaseline.draftVersion,
      'Cancel removal must preserve the saved draft version.');
    assert.equal(afterCancelRemoval.draftDigest, removalBaseline.draftDigest,
      'Cancel removal must preserve the saved draft digest.');
    assert.deepEqual(afterCancelRemoval.document, removalBaseline.document,
      'Cancel removal must preserve the complete saved table document.');
    check('persistence', 'Cancel removal preserves the direct route, all exact rows, and draft identity', true, {
      outputId, stepId: afterCancelRemoval.stepId, relatedColumnId: afterCancelRemoval.relatedColumnId,
      draftVersion: afterCancelRemoval.draftVersion, draftDigest: afterCancelRemoval.draftDigest,
      rows: expectedDirectRows,
    });
    const cancelReload = await reloadAndAssert(
      'RelatedExpand removal Cancel', BASIC_SAVED_RELATED_ROUTE_A, expectedDirectRows, removalBaseline);
    check('persistence', 'Cancel removal keeps exact direct rows after a real reload', true, {
      outputId, stepId: cancelReload.stepId, relatedColumnId: cancelReload.relatedColumnId,
      rows: expectedDirectRows,
    });

    await previewRelatedRemoval('RelatedExpand removal Apply', cancelReload);
    const beforeApply = savedRelatedState(await apiJSON(target, routeApiRoot, '/builder'), outputId);
    assert.deepEqual(beforeApply.document, removalBaseline.document,
      'Removal preview must not mutate the saved document before Apply.');
    await action('Apply exact-one RelatedExpand removal', page.getByTestId('construction-apply-proposal'),
      () => page.getByTestId('construction-apply-proposal').click(), {
        after: async () => waitForPatientIDs(page, { patientLabel, expected: oracle.patientIDs }),
      });
    const removed = await inspectRemovedState('RelatedExpand removal Apply', { outputId, relatedColumnId: removalBaseline.relatedColumnId });
    assert(removed.draftVersion > removalBaseline.draftVersion,
      'Applying removal must advance the saved draft version.');
    assert.notEqual(removed.draftDigest, removalBaseline.draftDigest,
      'Applying removal must change the saved draft digest.');
    check('persistence', 'Apply removes only the saved RelatedExpand and preserves the exact Patient root rows', true, {
      outputId, removedStepId: removalBaseline.stepId, patientColumnId: idColumn.id,
      draftVersion: removed.draftVersion, draftDigest: removed.draftDigest, rows: oracle.patientIDs,
    });
    const removedReload = await reloadAndAssertRemoved('RelatedExpand removal Apply', {
      outputId, relatedColumnId: removalBaseline.relatedColumnId,
    });
    check('persistence', 'saved source-only table remains exact after a real removal reload', true, {
      outputId, patientColumnId: idColumn.id, rows: oracle.patientIDs,
    });

    await openRowSettings('recreate RelatedExpand from source rows');
    await openRelatedEditor('open RelatedExpand editor after removal');
    const recordTypeAfterRemoval = page.getByRole('combobox', { name: 'Related record type', exact: true });
    await action('reselect Observation after removal', recordTypeAfterRemoval,
      () => recordTypeAfterRemoval.selectOption('Observation'), {
        after: async () => page.getByRole('radio', { name: oracle.routeA.label, exact: true })
          .waitFor({ state: 'visible', timeout: 5000 }),
      });
    const directRouteAfterRemoval = page.getByRole('radio', { name: oracle.routeA.label, exact: true });
    await chooseRoute(directRouteAfterRemoval, 'preview exact-one direct route recreation', expectedDirectRows);
    await assertVisiblePairs(page, {
      patientLabel, relatedLabel: 'Observation FHIR resource ID', expected: expectedDirectRows,
      proposal: true, phase: 'recreated direct route proposal',
    });
    await applyProposal('Apply exact-one direct route recreation', expectedDirectRows);
    const recreated = savedRelatedState(await apiJSON(target, routeApiRoot, '/builder'), outputId);
    assertSavedRoute(recreated, {
      outputId, stepId: recreated.stepId, relatedColumnId: recreated.relatedColumnId,
      route: BASIC_SAVED_RELATED_ROUTE_A, phase: 'RelatedExpand recreation Apply',
    });
    assert.equal(recreated.relatedLabel, 'Observation FHIR resource ID',
      'RelatedExpand recreation must restore the exact Observation identity column label.');
    assert.equal(recreated.document.rootResourceType, 'Patient',
      'RelatedExpand recreation must preserve the Patient root.');
    await assertVisiblePairs(page, {
      patientLabel, relatedLabel: recreated.relatedLabel, expected: expectedDirectRows,
      phase: 'RelatedExpand recreation Apply',
    });
    assert.notEqual(recreated.stepId, removalBaseline.stepId,
      'Recreation must create a new RelatedExpand step after the prior step was removed.');
    assert.notEqual(recreated.relatedColumnId, removalBaseline.relatedColumnId,
      'Recreation must create a new related output identity after the prior column was removed.');
    assert(recreated.draftVersion > removedReload.draftVersion,
      'Recreating the RelatedExpand must advance beyond the removal draft.');
    const recreatedReload = await reloadAndAssert(
      'RelatedExpand recreation', BASIC_SAVED_RELATED_ROUTE_A, expectedDirectRows, recreated);
    check('persistence', 'recreated direct RelatedExpand reloads the exact three rows', true, {
      outputId, stepId: recreatedReload.stepId, relatedColumnId: recreatedReload.relatedColumnId,
      rows: expectedDirectRows, exactOnePatientID: 'dev-patient-002', exactOneMatches: 1,
    });
    report.target.savedRelatedRouteFinal = {
      status: 'complete',
      case: 'exact-one removal and recreation',
      outputId,
      originalStepId: removalBaseline.stepId,
      stepId: recreatedReload.stepId,
      relatedColumnId: recreatedReload.relatedColumnId,
      route: oracle.routeA.signature,
      rows: expectedDirectRows,
      reloadRows: expectedDirectRows,
      constructionSteps: recreatedReload.document.construction.steps.length,
      exactOnePatientID: 'dev-patient-002',
      exactOneMatches: 1,
    };
    assert.deepEqual(report.errors, [], 'Unexpected browser HTTP, runtime, console, or module errors occurred');
    check('correctness', 'exact-one removal and recreation has no unexpected browser errors',
      report.errors.length === 0, { errors: report.errors });
    return;
  }

  const directAppliedBaseline = {
    ...directSaved,
    document: structuredClone(directSaved.document),
  };
  const editForCancel = await openSavedEdit(directSaved.stepId, 'open direct route edit for Cancel');
  await chooseRoute(editForCancel.alternate, 'preview J02 route before Cancel', expectedJ02Rows);
  await assertVisiblePairs(page, {
    patientLabel, relatedLabel: directSaved.relatedLabel, expected: expectedJ02Rows,
    proposal: true, phase: 'J02 edit proposal before Cancel',
  });
  await action('Cancel saved route edit to preserve direct route A', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(), {
      after: async () => page.getByTestId('construction-related-expand-editor')
        .waitFor({ state: 'detached', timeout: 5000 }),
    });
  await assertVisiblePairs(page, {
    patientLabel, relatedLabel: directSaved.relatedLabel, expected: expectedDirectRows,
    phase: 'direct route after edit Cancel',
  });
  const afterCancel = savedRelatedState(await apiJSON(target, routeApiRoot, '/builder'), outputId);
  assertSavedIdentity(afterCancel, directAppliedBaseline, 'Cancel saved route edit');
  assert.equal(afterCancel.draftVersion, directAppliedBaseline.draftVersion,
    'Cancel must preserve the exact saved draft version.');
  assert.equal(afterCancel.draftDigest, directAppliedBaseline.draftDigest,
    'Cancel must preserve the exact saved draft digest.');
  assert.deepEqual(afterCancel.document, directAppliedBaseline.document,
    'Cancel must preserve the complete saved Patient table document.');
  assertRouteSignature(afterCancel.route, BASIC_SAVED_RELATED_ROUTE_A, 'Cancel saved route edit');
  check('persistence', 'Cancel preserves saved route A, exact rows, and unchanged draft identity', true, {
    stepId: afterCancel.stepId, outputId, relatedColumnId: afterCancel.relatedColumnId,
    draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest, rows: expectedDirectRows,
  });
  const directAfterCancelReload = await reloadAndAssert(
    'Cancel route edit preserves direct route A', BASIC_SAVED_RELATED_ROUTE_A, expectedDirectRows, directAppliedBaseline);
  check('persistence', 'Cancel route edit keeps exact route A rows after reload', true, {
    stepId: directAfterCancelReload.stepId, outputId, relatedColumnId: directAfterCancelReload.relatedColumnId,
    draftVersion: directAfterCancelReload.draftVersion, draftDigest: directAfterCancelReload.draftDigest,
    rows: expectedDirectRows,
  });

  const editForApply = await openSavedEdit(directSaved.stepId, 'open direct route edit for Apply');
  await chooseRoute(editForApply.alternate, 'preview J02 route for saved Apply', expectedJ02Rows);
  const j02Proposal = await assertVisiblePairs(page, {
    patientLabel, relatedLabel: directSaved.relatedLabel, expected: expectedJ02Rows,
    proposal: true, phase: 'J02 route edit proposal',
  });
  check('correctness', 'J02 route proposal includes its exact Observation row and preserved null Patient row', true, {
    rows: j02Proposal.pairs, rawOracle: routeB, route: oracle.routeB.signature, emptyPolicy: await emptyPolicy.inputValue(),
  });
  await applyProposal('Apply J02 Patient-Group-Specimen-Observation route', expectedJ02Rows);
  const j02Saved = await inspectSaved('J02 route Apply', BASIC_SAVED_RELATED_ROUTE_B, expectedJ02Rows, directSaved);
  assert.equal(j02Saved.emptyPolicy, 'PRESERVE_PARENT');
  assert(j02Saved.draftVersion > directSaved.draftVersion,
    'Applying route B must advance the saved draft beyond direct route A.');
  assert.notEqual(j02Saved.draftDigest, directSaved.draftDigest,
    'Applying route B must change the saved draft digest.');
  check('persistence', 'J02 route Apply retains the original step and output IDs', true, {
    outputId, stepId: j02Saved.stepId, relatedColumnId: j02Saved.relatedColumnId,
    rows: expectedJ02Rows, draftVersion: j02Saved.draftVersion, draftDigest: j02Saved.draftDigest,
  });
  const j02AfterReload = await reloadAndAssert(
    'J02 route Apply', BASIC_SAVED_RELATED_ROUTE_B, expectedJ02Rows, directSaved);
  check('persistence', 'J02 route A-to-B edit persists exact preserved-parent rows after reload', true, {
    outputId, stepId: j02AfterReload.stepId, relatedColumnId: j02AfterReload.relatedColumnId,
    rows: expectedJ02Rows, draftVersion: j02AfterReload.draftVersion, draftDigest: j02AfterReload.draftDigest,
  });

  const editForRestore = await openSavedEdit(directSaved.stepId, 'open J02 route edit for restoration');
  await chooseRoute(editForRestore.direct, 'preview direct route for restoration', expectedDirectRows);
  const restoredProposal = await assertVisiblePairs(page, {
    patientLabel, relatedLabel: j02AfterReload.relatedLabel, expected: expectedDirectRows,
    proposal: true, phase: 'direct route restoration proposal',
  });
  check('correctness', 'direct route restoration proposal restores all exact source pairs', true, {
    rows: restoredProposal.pairs, rawOracle: routeA, route: oracle.routeA.signature,
  });
  await applyProposal('Apply direct Patient-to-Observation route restoration', expectedDirectRows);
  const restoredSaved = await inspectSaved('direct route restoration Apply', BASIC_SAVED_RELATED_ROUTE_A,
    expectedDirectRows, directSaved);
  assert(restoredSaved.draftVersion > j02AfterReload.draftVersion,
    'Restoring route A must advance the saved draft beyond route B.');
  assert.notEqual(restoredSaved.draftDigest, j02AfterReload.draftDigest,
    'Restoring route A must change the route B draft digest.');
  check('persistence', 'route restoration retains the original step and output IDs', true, {
    outputId, stepId: restoredSaved.stepId, relatedColumnId: restoredSaved.relatedColumnId,
    rows: expectedDirectRows, draftVersion: restoredSaved.draftVersion, draftDigest: restoredSaved.draftDigest,
  });
  const restoredReload = await reloadAndAssert(
    'direct route restoration', BASIC_SAVED_RELATED_ROUTE_A, expectedDirectRows, directSaved);
  check('persistence', 'restored direct route A renders the exact three pairs after reload', true, {
    outputId, stepId: restoredReload.stepId, relatedColumnId: restoredReload.relatedColumnId,
    rows: expectedDirectRows, draftVersion: restoredReload.draftVersion, draftDigest: restoredReload.draftDigest,
  });

  assertSavedIdentity(restoredReload, directAppliedBaseline, 'final restored direct route');
  report.target.savedRelatedRouteFinal = {
    status: 'complete',
    outputId,
    stepId: restoredReload.stepId,
    relatedColumnId: restoredReload.relatedColumnId,
    route: oracle.routeA.signature,
    rows: expectedDirectRows,
    reloadRows: expectedDirectRows,
    constructionSteps: restoredReload.document.construction.steps.length,
  };
  assert.deepEqual(report.errors, [], 'Unexpected browser HTTP, runtime, console, or module errors occurred');
  check('correctness', 'saved related-route edit lifecycle has no unexpected browser errors',
    report.errors.length === 0, { errors: report.errors });
};
