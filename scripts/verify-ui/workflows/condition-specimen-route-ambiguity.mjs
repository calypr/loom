import assert from 'node:assert/strict';
import { browserURL } from './builder-url.mjs';
import { collectPreviewRows, previewHeaderIndex } from '../helpers/playwright-preview-rows.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { rootOption } from '../helpers/playwright-authoring.mjs';
import { conditionPatientRowsMatch, readConditionSpecimenRouteOracle } from '../helpers/condition-specimen-route-oracle.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const normalizeCell = value => value === '—' || value === '' ? null : String(value ?? '').trim();
const normalizeText = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const sortRows = rows => [...rows].sort((left, right) =>
  `${left.specimenId ?? ''}\0${left.conditionId ?? ''}\0${left.patientId ?? ''}`
    .localeCompare(`${right.specimenId ?? ''}\0${right.conditionId ?? ''}\0${right.patientId ?? ''}`));

const routeRowsFromGrid = (grid, { includesSpecimenID }) => {
  const conditionIndex = previewHeaderIndex(grid.headers, 'Condition ID');
  const patientIndex = previewHeaderIndex(grid.headers, 'Patient IDs');
  const specimenIndex = includesSpecimenID ? previewHeaderIndex(grid.headers, 'Specimen ID') : -1;
  const values = grid.rows.map(row => Array.isArray(row) ? row : row.values);
  return values.map(row => ({
    specimenId: specimenIndex < 0 ? undefined : normalizeCell(row[specimenIndex]),
    conditionId: normalizeCell(row[conditionIndex]),
    patientId: normalizeCell(row[patientIndex]),
  }));
};
const candidateGrid = async preview => preview.evaluate(element => {
  const table = element.querySelector('table');
  return {
    headers: [...(table?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim()),
    rows: [...(table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
  };
});

export const conditionSpecimenRouteAmbiguityWorkflow = async ({ page, report, check, action }, context) => {
  configureNativePage(page);
  const { target } = context;
  assert.equal(context.custom, false, 'Condition-to-Specimen ambiguity requires the isolated Basic fixture loader.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_') && target.fixtureGeneration && target.fixtureDir,
    'Condition-to-Specimen ambiguity must use a fresh Basic project and exact fixture generation.');
  const independent = readConditionSpecimenRouteOracle(target.fixtureDir);
  const { oracle } = independent;
  const project = target.fixtureProject;
  const generation = target.fixtureGeneration;
  const apiOrigin = target.apiUrl;
  const browserOrigin = new URL(target.uiUrl).origin;
  const explorerCollectionPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const builderPathFor = explorer => `${explorerCollectionPath}/${encodeURIComponent(explorer)}/authoring/v2/builder`;
  const title = `Verify ${context.runID.slice(-10)} Condition routes`;
  const tableTitle = `Condition routes ${context.runID.slice(-6)}`;
  const act = (name, locator, perform, options = {}) => {
    const deadlineAt = performance.now() + ACTION_TIMEOUT_MS;
    const remainingTimeout = () => {
      const remaining = Math.floor(deadlineAt - performance.now());
      assert(remaining > 0, `${name} exceeded its ${ACTION_TIMEOUT_MS} ms action-to-render budget.`);
      return remaining;
    };
    const { after, ...actionOptions } = options;
    return action(name, locator, control => perform(control, { remainingTimeout }), {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      ...actionOptions,
      ...(after ? { after: () => after({ remainingTimeout }) } : {}),
    });
  };
  const apiJSON = async path => {
    const response = await fetch(`${apiOrigin}${path}`, { signal: AbortSignal.timeout(ACTION_TIMEOUT_MS) });
    const value = await response.json();
    assert(response.ok, `Builder read ${path} returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 600)}`);
    return value;
  };
  const requestBody = request => {
    try { return request.postDataJSON(); } catch { return undefined; }
  };
  const waitForRootAssessment = (nodeId, remainingTimeout) => page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    const body = requestBody(request);
    return request.method() === 'POST' && url.origin === browserOrigin && url.pathname.endsWith('/row-change') &&
      body?.outputId === outputId && body.rootNodeId === nodeId;
  }, { timeout: remainingTimeout() });
  let explorer;
  let outputId;
  const readBuilder = async () => {
    const builder = await apiJSON(builderPathFor(explorer));
    assert.equal(builder.catalog?.generation, generation, 'Builder must retain the exact fixture generation.');
    assert(builder.catalog?.snapshotToken && builder.draftDigest && Number.isInteger(builder.draftVersion),
      'Builder must expose its current catalog snapshot and saved-draft checkpoint.');
    return builder;
  };
  const documentForOutput = (builder, id) => {
    const matches = (builder.workspace?.documents ?? []).filter(document => document.output?.id === id);
    assert.equal(matches.length, 1, `Builder must retain exactly one document for output ${id}.`);
    return matches[0];
  };
  const assertVisibleRows = (grid, expected, { includesSpecimenID = false, phase }) => {
    assert.equal(grid.rowCount ?? grid.rows.length, expected.length, `${phase} must show every literal Specimen root row.`);
    if (!includesSpecimenID) {
      const actual = routeRowsFromGrid(grid, { includesSpecimenID });
      assert.equal(conditionPatientRowsMatch(actual, expected), true,
        `${phase} must show the exact independent Condition/Patient values selected by the route oracle.`);
      return actual;
    } else {
      const actual = sortRows(routeRowsFromGrid(grid, { includesSpecimenID }));
      const wanted = sortRows(expected);
      assert.deepEqual(actual, wanted, `${phase} must show exact Specimen, Condition, and Patient source IDs.`);
      return actual;
    }
  };
  const getCurrentRows = async (phase, { includePatientColumn = true } = {}) => {
    const grid = await collectPreviewRows(page, { timeout: ACTION_TIMEOUT_MS });
    const expected = phase === 'condition' ? independent.conditions.map(({ id, patientId }) => ({ id, patientId })) : undefined;
    if (expected) {
      const conditionIndex = previewHeaderIndex(grid.headers, 'Condition ID');
      const actual = grid.rows.map(row => ({
        id: normalizeCell(row.values[conditionIndex]),
        ...(includePatientColumn ? { patientId: normalizeCell(row.values[previewHeaderIndex(grid.headers, 'Patient IDs')]) } : {}),
      }));
      const expectedRows = includePatientColumn ? expected : expected.map(({ id }) => ({ id }));
      const sortConditionRows = rows => [...rows].sort((left, right) =>
        `${left.id}\0${left.patientId ?? ''}`.localeCompare(`${right.id}\0${right.patientId ?? ''}`));
      assert.deepEqual(sortConditionRows(actual), sortConditionRows(expectedRows), includePatientColumn
        ? 'Condition table must retain the exact independent Condition.subject Patient IDs.'
        : 'Initial Condition table must render every literal Condition ID.');
    }
    return grid;
  };
  const openDisclosure = async (selector, label) => {
    const disclosure = page.locator(selector);
    assert.equal(await disclosure.count(), 1, `${label} must identify one disclosure.`);
    if (!await disclosure.evaluate(element => element.open)) {
      const summary = disclosure.locator(':scope > summary');
      assert.equal(await summary.count(), 1, `${label} must expose one summary control.`);
      await act(label, summary, async (control, { remainingTimeout }) =>
        control.click({ timeout: remainingTimeout() }), {
        after: ({ remainingTimeout }) => page.waitForFunction(
          target => document.querySelector(target)?.open === true,
          selector, { timeout: remainingTimeout() },
        ),
      });
    }
    assert.equal(await disclosure.evaluate(element => element.open), true,
      `${label} must leave the disclosure open.`);
  };

  report.target.fixtureRawOracle = {
    fixtureDir: target.fixtureDir,
    project,
    generation,
    sourceIDs: independent.sourceIDs,
    patientSubjectPath: oracle.patientSubjectPath,
    expectedRouteRows: oracle.alternatives.map(choice => ({
      key: choice.key,
      path: choice.path,
      specimenId: choice.specimenId,
      conditionId: choice.conditionId,
      patientId: choice.patientId,
    })),
    unmatchedSpecimen: oracle.unmatched,
  };
  check('correctness', 'independent fixture oracle distinguishes all compiler route results and an unmatched Specimen',
    independent.patientSubjectRows.some(row => row.conditionId) &&
      independent.focusRows.some(row => row.conditionId) && independent.stageRows.some(row => row.conditionId) &&
      new Set(oracle.alternatives.map(choice => choice.specimenId)).size === oracle.alternatives.length &&
      independent.sourceIDs.Specimen.includes(oracle.unmatched.specimenId), report.target.fixtureRawOracle);

  await page.goto(browserURL(target, project, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await act('open native Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const explorerName = page.locator('#new-explorer-name');
  await act('name the isolated Condition route Explorer', explorerName, () => explorerName.fill(title), {
    editable: true,
    after: async () => assert.equal(await explorerName.inputValue(), title),
  });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await act('create the isolated Condition route Explorer', createBlank, () => createBlank.click(), {
    after: async () => page.waitForFunction(expectedTitle =>
      document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expectedTitle,
    title, { timeout: ACTION_TIMEOUT_MS }),
  });
  explorer = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'Condition route coverage must use a fresh Explorer.');
  report.target.explorer = explorer;

  const firstTableName = page.locator('#first-table-name');
  await act('name the Condition source table', firstTableName, () => firstTableName.fill(tableTitle), {
    editable: true,
    after: async () => assert.equal(await firstTableName.inputValue(), tableTitle),
  });
  const chooseCondition = page.getByRole('button', { name: 'Choose Condition rows', exact: true });
  await act('choose Condition as the authored row root', chooseCondition, () => chooseCondition.click(), {
    after: async () => {
      await page.waitForFunction(count => {
        const table = document.querySelector('[data-testid="construction-preview"] [data-testid="preview-table-scroll"] [role="table"]');
        return table?.getAttribute('aria-rowcount') === String(count + 1);
      }, independent.conditions.length, { timeout: ACTION_TIMEOUT_MS });
    },
  });
  let builder = await readBuilder();
  let conditionDocument = (builder.workspace?.documents ?? []).find(document => document.rootResourceType === 'Condition');
  assert(conditionDocument?.output?.id, 'The native Condition table must persist a root output.');
  outputId = conditionDocument.output.id;
  assert.equal(conditionDocument.rows?.kind, 'RECORDS');
  assert.equal(conditionDocument.columns.length, 1, 'The initial Condition table must contain only its default ID field.');
  assert.equal(conditionDocument.columns[0].source?.field?.path, 'id');
  await getCurrentRows('condition', { includePatientColumn: false });

  const openColumns = page.getByTestId('construction-action-add-columns');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-add-columns"]');
    return button && !button.disabled;
  }, null, { timeout: ACTION_TIMEOUT_MS });
  await act('open Condition fields and related data', openColumns, () => openColumns.click(), {
    after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const fieldsRelated = page.getByRole('button', { name: 'Fields and related data', exact: true });
  await act('browse Condition fields and related data', fieldsRelated, () => fieldsRelated.click(), {
    after: () => page.getByTestId('construction-add-columns-source').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  await openDisclosure('[aria-label="Related resources"]', 'open Condition related resources');
  const patientRelatedSources = await page.getByTestId('construction-add-columns-source-option').evaluateAll(options =>
    options.map(option => ({ label: option.getAttribute('aria-label') ?? '', disabled: option.disabled }))
      .filter(option => option.label.includes('Patient') && option.label.includes('Related resource')));
  assert.equal(patientRelatedSources.length, 1,
    `Condition.subject must expose one Patient related source: ${JSON.stringify(patientRelatedSources)}`);
  assert.equal(patientRelatedSources[0].disabled, false,
    `Patient related source must be enabled: ${patientRelatedSources[0].label}`);
  const patientRelatedSource = page.locator(
    `[data-testid="construction-add-columns-source-option"][aria-label=${JSON.stringify(patientRelatedSources[0].label)}]`,
  );
  assert.equal(await patientRelatedSource.count(), 1, 'Patient related source must have one native control.');
  await act('select Patient as the related field source', patientRelatedSource,
    async (control, { remainingTimeout }) => control.click({ timeout: remainingTimeout() }), {
    after: ({ remainingTimeout }) => page.waitForFunction(label => {
      const option = [...document.querySelectorAll('[data-testid="construction-add-columns-source-option"]')]
        .find(candidate => candidate.getAttribute('aria-label') === label);
      return option?.getAttribute('aria-pressed') === 'true';
    }, patientRelatedSources[0].label, { timeout: remainingTimeout() }),
  });
  await openDisclosure('[data-testid="feature-catalog-raw-fields"]', 'open raw FHIR fields for Patient.id');
  const patientIDChoice = page.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
  await patientIDChoice.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  assert.equal(await patientIDChoice.isEnabled(), true,
    'The authored Condition.subject route must expose selectable Patient.id source data.');
  await act('select Patient.id through Condition.subject', patientIDChoice, () => patientIDChoice.check(), {
    after: async () => assert.equal(await patientIDChoice.isChecked(), true),
  });
  const addPatientID = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  const fieldChoiceDialog = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
  await act('open Patient.id route and form choices', addPatientID, control => control.click(), {
    after: ({ remainingTimeout }) => fieldChoiceDialog.waitFor({ state: 'visible', timeout: remainingTimeout() }),
  });
  assert.equal(await fieldChoiceDialog.count(), 1, 'Patient.id must open one compiler-backed route and form dialog.');
  const routeChoices = await fieldChoiceDialog.getByRole('radio').evaluateAll(inputs => inputs.map(input => ({
    label: input.getAttribute('aria-label') ?? '',
    disabled: input.disabled,
  })));
  const directSubjectRoutes = routeChoices.filter(choice => choice.label.includes('Condition -[subject]-> Patient'));
  assert.equal(directSubjectRoutes.length, 1,
    `Patient.id must offer the exact compiler Condition.subject → Patient route once: ${JSON.stringify(routeChoices)}`);
  assert.equal(directSubjectRoutes[0].disabled, false,
    `The compiler Condition.subject → Patient route must be selectable: ${directSubjectRoutes[0].label}`);
  const directSubjectRoute = fieldChoiceDialog.getByRole('radio', { name: directSubjectRoutes[0].label, exact: true });
  await act('select Condition.subject route for Patient.id', directSubjectRoute,
    async (control, { remainingTimeout }) => control.check({ timeout: remainingTimeout() }), {
      after: async ({ remainingTimeout }) => {
        const allForm = fieldChoiceDialog.getByRole('radio', { name: /Keep all matching values$/ });
        await allForm.waitFor({ state: 'visible', timeout: remainingTimeout() });
      },
    });
  const patientIDForms = await fieldChoiceDialog.getByRole('radio').evaluateAll(inputs => inputs.map(input => ({
    label: input.getAttribute('aria-label') ?? '',
    disabled: input.disabled,
  })).filter(choice => choice.label.endsWith(': Keep all matching values')));
  assert.equal(patientIDForms.length, 1,
    `Patient.id must offer one ALL form choice: ${JSON.stringify(patientIDForms)}`);
  assert.equal(patientIDForms[0].disabled, false, `Patient.id ALL form must be selectable: ${patientIDForms[0].label}`);
  const allPatientIDValues = fieldChoiceDialog.getByRole('radio', { name: patientIDForms[0].label, exact: true });
  await act('select ALL form for Patient.id', allPatientIDValues,
    async (control, { remainingTimeout }) => control.check({ timeout: remainingTimeout() }), {
      after: async () => assert.equal(await allPatientIDValues.isChecked(), true,
        'Patient.id must preserve all matching values without deduplication.'),
    });
  const confirmPatientID = fieldChoiceDialog.getByRole('button', { name: 'Add 1 column', exact: true });
  assert.equal(await confirmPatientID.count(), 1, 'The selected Patient.id route/form must expose one Add 1 column action.');
  const fieldProposal = page.getByTestId('construction-proposal-panel');
  await act('confirm Patient.id through Condition.subject and ALL', confirmPatientID,
    async (control, { remainingTimeout }) => control.click({ timeout: remainingTimeout() }), {
      after: ({ remainingTimeout }) => fieldProposal
        .waitFor({ state: 'visible', timeout: remainingTimeout() }),
    });
  await page.waitForFunction(() => ['ready', 'error'].includes(
    document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus),
  null, { timeout: ACTION_TIMEOUT_MS });
  assert.equal(await fieldProposal.getAttribute('data-proposal-status'), 'ready',
    'Condition.subject Patient.id must produce a receipt-bound related-source proposal.');
  const applyPatientID = fieldProposal.getByRole('button', { name: 'Apply change', exact: true });
  await act('apply Patient.id through Condition.subject', applyPatientID, () => applyPatientID.click(), {
    after: () => fieldProposal.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
  });
  builder = await readBuilder();
  conditionDocument = documentForOutput(builder, outputId);
  const patientSourceStep = (conditionDocument.construction?.steps ?? []).find(step =>
    step.operation.kind === 'RELATED_SOURCE' && step.operation.relatedSource.source.resourceType === 'Patient' &&
    step.operation.relatedSource.source.path === 'id');
  assert(patientSourceStep, `Applied Patient.id must retain its compiler-backed related-source step: ${JSON.stringify(conditionDocument.construction)}`);
  const patientSource = patientSourceStep.operation.relatedSource;
  assert.equal(patientSource.form, 'ALL', 'Patient.id must retain all matching values from the selected route.');
  assert.deepEqual(patientSource.route.map(({ fromResourceType, relationship, toResourceType }) =>
    ({ fromResourceType, relationship, toResourceType })), [
    { fromResourceType: 'Condition', relationship: 'subject_Patient', toResourceType: 'Patient' },
  ], 'Patient.id must retain the exact compiler Condition.subject → Patient route.');
  const patientColumn = patientSourceStep.outputs.find(column => column.id === patientSource.outputColumnId);
  assert(patientColumn, 'The authored Patient.id route must retain its output column identity.');
  const patientSourceIdentity = { sourceOccurrenceId: patientSource.sourceOccurrenceId, route: patientSource.route };
  const sourceConditionRows = independent.conditions.map(({ id, patientId }) => ({ id, patientId }));
  const afterAuthoringGrid = await getCurrentRows('condition');
  check('correctness', 'Condition.subject adds Patient IDs through the exact Patient-subject source route', true, {
    outputId,
    route: conditionDocument.route,
    conditionAndPatientColumnIDs: conditionDocument.columns.map(column => column.columnId),
    rows: afterAuthoringGrid.rows.map(row => row.values),
    expectedRows: sourceConditionRows,
  });
  assert.equal(await page.locator('[aria-label="Add columns editor"]').count(), 0,
    'Applying the related-source proposal returns the Builder to the table.');

  builder = await readBuilder();
  conditionDocument = documentForOutput(builder, outputId);
  const authoredConditionDocument = structuredClone(conditionDocument);
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await getCurrentRows('condition');
  builder = await readBuilder();
  conditionDocument = documentForOutput(builder, outputId);
  const reloadedPatientSourceStep = (conditionDocument.construction?.steps ?? []).find(step =>
    step.operation.kind === 'RELATED_SOURCE' && step.operation.relatedSource.source.resourceType === 'Patient' &&
    step.operation.relatedSource.source.path === 'id');
  assert(reloadedPatientSourceStep, 'Reload must preserve the selected Patient.id related-source step.');
  assert.equal(reloadedPatientSourceStep.operation.relatedSource.sourceOccurrenceId, patientSourceIdentity.sourceOccurrenceId,
    'Reload must preserve the selected Patient source identity.');
  assert.deepEqual(reloadedPatientSourceStep.operation.relatedSource.route, patientSourceIdentity.route,
    'Reload must preserve the exact Condition.subject → Patient route.');
  assert.deepEqual(conditionDocument, authoredConditionDocument,
    'Reloaded authored Condition columns must remain persisted.');
  const sourceCheckpoint = {
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
    document: structuredClone(conditionDocument),
    catalog: structuredClone(builder.catalog),
  };

  const openRowSettings = async name => {
    const trigger = page.getByTestId('construction-rows-settings-trigger');
    await act(name, trigger, () => trigger.click(), {
      after: () => page.getByRole('dialog', { name: 'Row definition settings', exact: true })
        .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
    });
    const dialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    const section = dialog.getByRole('region', { name: 'What should each row represent?', exact: true });
    const selector = section.getByRole('combobox', { name: 'Record type', exact: true });
    assert.equal(await selector.count(), 1, 'Row settings must expose one shared Record type picker.');
    assert.equal(await section.getByRole('button', { name: 'Review changes', exact: true }).count(), 0,
      'Selecting a row type must start review automatically without a separate Review changes action.');
    assert.equal(await dialog.getByRole('combobox', { name: 'Choose starting record type', exact: true }).count(), 0,
      'The former connected-root picker must be absent.');
    assert.equal(await dialog.getByRole('combobox', { name: 'Choose replacement starting type', exact: true }).count(), 0,
      'The former replacement picker must be absent.');
    assert.equal(await dialog.getByRole('heading', { name: 'Replace current setup?', exact: true }).count(), 0,
      'Replacement must not appear as a separate card.');
    return { dialog, section, selector };
  };
  const selectSpecimen = async ({ selector }, label) => {
    const choice = await rootOption(selector, 'Specimen');
    assert(choice && !choice.disabled && choice.value.startsWith('node:'),
      `The shared picker must expose the compiler-backed Specimen choice: ${JSON.stringify(choice)}`);
    const nodeId = choice.value.slice('node:'.length);
    let assessment;
    await act(`${label}: select Specimen and assess its routes`, selector,
      async (control, { remainingTimeout }) => {
        const responsePromise = waitForRootAssessment(nodeId, remainingTimeout);
        await control.selectOption(choice.value, { timeout: remainingTimeout() });
        const response = await responsePromise;
        assert.equal(response.status(), 200, 'Selecting Specimen must return an HTTP 200 root assessment.');
        assessment = await response.json();
      }, {
        after: async ({ remainingTimeout }) => {
          await page.waitForFunction(() => [...(document.querySelector('[aria-label="Record type"]')?.options ?? [])]
            .some(option => !option.disabled && option.value.startsWith('compiler-route:')),
          null, { timeout: remainingTimeout() });
        },
      });
    return { choice, assessment };
  };
  const routeOptions = async selector => {
    const options = await selector.locator('option').evaluateAll(nodes => nodes.map(option => ({
      value: option.value,
      label: String(option.textContent ?? '').replace(/\s+/g, ' ').trim(),
      disabled: option.disabled,
    })));
    const choices = options.filter(option => !option.disabled && option.value.startsWith('compiler-route:'));
    assert(choices.length > 0, 'The shared Record type picker must expose compiler-issued route tokens.');
    assert.equal(await page.getByRole('heading', { name: 'Choose how to preserve this table', exact: true }).count(), 0,
      'Compiler route choices must be inline in Record type, without the former route-repair panel.');
    return choices;
  };
  const choosePath = async (selector, { kind, nodeId, assessment }) => {
    const options = await routeOptions(selector);
    const routeReferences = (assessment.unresolved ?? []).filter(reference =>
      reference.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE' && Array.isArray(reference.alternatives));
    assert(routeReferences.length > 0,
      `The root assessment must provide compiler route alternatives: ${JSON.stringify(assessment.unresolved)}`);
    const compilerChoices = routeReferences.flatMap(reference => reference.alternatives
      .filter(routeToken => routeToken.startsWith('row-change-route:v1:'))
      .map(routeToken => {
        const value = `compiler-route:${reference.id}:${routeToken}`;
        const matches = options.filter(option => option.value === value);
        assert.equal(matches.length, 1,
          `Compiler route alternative ${routeToken} must have one enabled native picker option.`);
        return { ...matches[0], referenceId: reference.id, routeToken };
      }));
    assert(compilerChoices.length > 0, 'The root assessment must include compiler-issued route tokens.');
    const option = compilerChoices.find(candidate => kind === 'patient'
      ? candidate.label.includes('Condition → Subject → Patient → Subject → Specimen') && candidate.label.endsWith('from Condition rows')
      : candidate.label.includes('Focus') && candidate.label.includes('Observation') && candidate.label.includes('Specimen') && candidate.label.endsWith('from Condition rows'));
    assert(option, `The ${kind} native route choice must be available: ${JSON.stringify(compilerChoices.map(({ value, label }) => ({ value, label })))}`);
    let previewIdentity;
    let edgeRepairEvidence;
    let rootSelectionRequest;
    if (kind === 'focus') {
      await act('select the focus-inbound Condition-to-Specimen route', selector,
        async (control, { remainingTimeout }) => {
          const responsePromise = waitForRootAssessment(nodeId, remainingTimeout);
          await control.selectOption(option.value, { timeout: remainingTimeout() });
          const response = await responsePromise;
          assert.equal(response.status(), 200, 'The focus route assessment must return HTTP 200.');
          const assessment = await response.json();
          const reference = (assessment.unresolved ?? []).find(entry =>
            entry.code === 'AMBIGUOUS_ROUTE_REBASE_EDGE');
          assert(assessment.status === 'BLOCKED' && reference,
            `The Condition/Observation route must expose the backend AMBIGUOUS_ROUTE_REBASE_EDGE assessment: ${JSON.stringify(assessment)}`);
          assert(Array.isArray(reference.alternatives) && reference.alternatives.length === 2,
            `The fixture must expose its two backend repair alternatives: ${JSON.stringify(reference.alternatives)}`);
          const relationshipAlternatives = reference.alternatives.map(edgeId => {
            const matches = sourceCheckpoint.catalog.edges.filter(edge => edge.edgeId === edgeId);
            assert.equal(matches.length, 1,
              `Compiler alternative ${edgeId} must resolve to one edge in the saved catalog snapshot.`);
            const [edge] = matches;
            const from = sourceCheckpoint.catalog.nodes.find(node => node.nodeId === edge.fromNodeId);
            const to = sourceCheckpoint.catalog.nodes.find(node => node.nodeId === edge.toNodeId);
            assert(from && to, `Compiler alternative ${edgeId} must have catalog endpoint metadata.`);
            return {
              edgeId,
              relationship: edge.label,
              fromResourceType: from.resourceType,
              toResourceType: to.resourceType,
            };
          });
          assert(relationshipAlternatives.every(edge => {
            const endpoints = new Set([edge.fromResourceType, edge.toResourceType]);
            return endpoints.has('Observation') && endpoints.has('Condition');
          }), `Every compiler alternative must connect the same Condition/Observation resources: ${JSON.stringify(relationshipAlternatives)}`);
          const focusAlternatives = relationshipAlternatives.filter(edge =>
            edge.relationship.toLowerCase().includes('focus'));
          assert.equal(focusAlternatives.length, 1,
            `One compiler alternative must identify the fixture's Focus relationship from catalog metadata: ${JSON.stringify(relationshipAlternatives)}`);
          edgeRepairEvidence = {
            status: assessment.status,
            code: reference.code,
            referenceId: reference.id,
            message: reference.message,
            alternatives: reference.alternatives,
            relationshipAlternatives,
            focusAlternative: focusAlternatives[0],
          };
        }, {
          after: async ({ remainingTimeout }) => {
            const values = edgeRepairEvidence.alternatives.map(edgeId =>
              `compiler-edge:${edgeRepairEvidence.referenceId}:${edgeId}`);
            await page.waitForFunction(expectedValues => {
              const options = [...(document.querySelector('[aria-label="Record type"]')?.options ?? [])];
              return expectedValues.every(value => options.some(option => option.value === value && !option.disabled));
            }, values, { timeout: remainingTimeout() });
          },
        });
      const edgeOptions = await selector.locator('option').evaluateAll(nodes => nodes.map(candidate => ({
        value: candidate.value,
        disabled: candidate.disabled,
      })));
      const alternativeOptions = edgeRepairEvidence.alternatives.map(edgeId => {
        const value = `compiler-edge:${edgeRepairEvidence.referenceId}:${edgeId}`;
        const matches = edgeOptions.filter(candidate => candidate.value === value);
        assert.equal(matches.length, 1,
          `Backend edge alternative ${edgeId} must have one native picker option.`);
        assert.equal(matches[0].disabled, false,
          `Backend edge alternative ${edgeId} must remain enabled in the native picker.`);
        return { edgeId, value };
      });
      const focusEdgeID = edgeRepairEvidence.focusAlternative.edgeId;
      const focusEdgeOption = alternativeOptions.find(option => option.edgeId === focusEdgeID);
      const otherEdgeOptions = alternativeOptions.filter(option => option.edgeId !== focusEdgeID);
      assert(focusEdgeOption && otherEdgeOptions.length === 1 && otherEdgeOptions[0].edgeId !== focusEdgeID,
        'The selected Focus edge and the remaining compiler alternative must have distinct IDs.');
      assert.equal(await page.getByRole('heading', { name: 'Choose how to preserve this table', exact: true }).count(), 0,
        'AMBIGUOUS_ROUTE_REBASE_EDGE must remain inline in the Record type picker.');
      await act('select the inline Observation.focus relationship repair', selector,
        async (control, { remainingTimeout }) => {
          const responsePromise = waitForRootAssessment(nodeId, remainingTimeout);
          await control.selectOption(focusEdgeOption.value, { timeout: remainingTimeout() });
          const response = await responsePromise;
          assert.equal(response.status(), 200, 'Applying the inline relationship repair must return HTTP 200.');
          const body = requestBody(response.request());
          assert.deepEqual(body?.routeRebase, [{ occurrenceId: edgeRepairEvidence.referenceId, edgeId: focusEdgeID }],
            'Selecting the inline Focus alternative must submit its exact reference and edge IDs.');
          const assessment = await response.json();
          assert.equal(assessment.status, 'READY',
            `The selected Focus edge must resolve the blocked route into a ready receipt-bound Preview: ${JSON.stringify(assessment)}`);
          edgeRepairEvidence = {
            ...edgeRepairEvidence,
            selectedEdgeId: focusEdgeID,
            selectedRelationship: edgeRepairEvidence.focusAlternative,
            resolutionRequest: body.routeRebase,
            resolvedStatus: assessment.status,
          };
        }, {
          after: async () => {
            await page.getByRole('region', { name: 'Review proposed rows', exact: true })
              .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
            await page.getByRole('dialog', { name: 'Row definition settings', exact: true })
              .waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
          },
        });
    } else {
      await act('select the Patient-subject Condition-to-Specimen route', selector,
        async (control, { remainingTimeout }) => {
          const responsePromise = waitForRootAssessment(nodeId, remainingTimeout);
          await control.selectOption(option.value, { timeout: remainingTimeout() });
          const response = await responsePromise;
          assert.equal(response.status(), 200, 'Selecting the Patient-subject compiler route must return HTTP 200.');
          const body = requestBody(response.request());
          assert.equal(body?.rootOccurrenceId, option.routeToken,
            'The selected compiler route token from the Patient-subject picker option must be submitted exactly as rootOccurrenceId.');
          rootSelectionRequest = {
            rootNodeId: body.rootNodeId,
            optionValue: option.value,
            referenceId: option.referenceId,
            rootOccurrenceId: body.rootOccurrenceId,
          };
        }, {
          after: async () => {
            await page.getByRole('region', { name: 'Review proposed rows', exact: true })
              .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
            await page.getByRole('dialog', { name: 'Row definition settings', exact: true })
              .waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
          },
        });
    }
    const panel = page.getByRole('region', { name: 'Review proposed rows', exact: true });
    const preview = panel.getByTestId('construction-proposal-preview');
    await preview.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    await page.waitForFunction(output => {
      const element = document.querySelector('[aria-label="Review proposed rows"] [data-testid="construction-proposal-preview"]');
      return element?.dataset.previewStatus === 'ready' && element.dataset.previewOutputId === output &&
        Boolean(element.dataset.previewReceiptId);
    }, outputId, { timeout: ACTION_TIMEOUT_MS });
    previewIdentity = await preview.evaluate(element => ({
      status: element.dataset.previewStatus,
      outputId: element.dataset.previewOutputId,
      receiptId: element.dataset.previewReceiptId,
    }));
    assert.equal(previewIdentity.status, 'ready');
    assert.equal(previewIdentity.outputId, outputId);
    expectReceipt(previewIdentity.receiptId);
    const grid = await candidateGrid(preview);
    return { panel, preview, grid, previewIdentity, edgeRepairEvidence, rootSelectionRequest };
  };
  const expectReceipt = value => {
    assert(typeof value === 'string' && value.length > 0, 'Candidate preview must bind to a non-empty receipt.');
    return value;
  };

  const { dialog: firstSettings, selector: firstSelector } = await openRowSettings('open Condition row settings');
  assert.equal(await firstSelector.locator('option:checked').evaluate(option => option.textContent?.trim()), 'Condition (current)');
  assert.equal(await firstSelector.locator('option:checked').evaluate(option => option.disabled), true,
    'The current Condition root must remain a disabled identity choice.');
  const pickerContract = {
    region: 'What should each row represent?',
    comboboxes: await firstSettings.getByRole('region', { name: 'What should each row represent?', exact: true })
      .getByRole('combobox', { name: 'Record type', exact: true }).count(),
    reviewButtons: await firstSettings.getByRole('region', { name: 'What should each row represent?', exact: true })
      .getByRole('button', { name: 'Review changes', exact: true }).count(),
  };
  assert.deepEqual(pickerContract, { region: 'What should each row represent?', comboboxes: 1, reviewButtons: 0 });
  const { choice: specimenChoice, assessment: patientRootAssessment } = await selectSpecimen({ selector: firstSelector }, 'Patient-subject route preview');
  const patientRoute = await choosePath(firstSelector, {
    kind: 'patient', nodeId: specimenChoice.value.slice('node:'.length), assessment: patientRootAssessment,
  });
  const patientPreviewRows = assertVisibleRows(patientRoute.grid, independent.patientSubjectRows, {
    phase: 'Patient-subject Condition-to-Specimen Preview',
  });
  assert(!patientPreviewRows.some(row => row.patientId === 'patient-unmatched'),
    'The unmatched Specimen must not expose its own Patient subject after rebasing the Condition-anchored Patient source.');
  const beforePatientCancel = await readBuilder();
  assert.equal(beforePatientCancel.draftVersion, sourceCheckpoint.draftVersion,
    'Automatic route Preview must not mutate the saved draft version.');
  assert.equal(beforePatientCancel.draftDigest, sourceCheckpoint.draftDigest,
    'Automatic route Preview must not mutate the saved draft digest.');
  assert.deepEqual(documentForOutput(beforePatientCancel, outputId), sourceCheckpoint.document,
    'Automatic route Preview must not mutate the Condition table or selected Patient-subject route.');
  check('correctness', 'Patient-subject route automatically previews its exact fixture rows without saved mutation', true, {
    outputId, selectedValue: specimenChoice.value,
    rootSelectionRequest: patientRoute.rootSelectionRequest,
    receiptId: patientRoute.previewIdentity.receiptId, rows: patientPreviewRows,
    expectedSpecimenId: oracle.alternatives.find(choice => choice.key === 'condition-patient-subject').specimenId,
    savedDraftVersion: beforePatientCancel.draftVersion, savedDraftDigest: beforePatientCancel.draftDigest,
  });
  const keepRows = patientRoute.panel.getByRole('button', { name: 'Keep current rows', exact: true });
  await act('cancel the Patient-subject route Preview', keepRows, control => control.click(), {
    after: () => patientRoute.panel.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
  });
  const afterCancel = await readBuilder();
  assert.equal(afterCancel.draftVersion, sourceCheckpoint.draftVersion);
  assert.equal(afterCancel.draftDigest, sourceCheckpoint.draftDigest);
  assert.deepEqual(documentForOutput(afterCancel, outputId), sourceCheckpoint.document,
    'Cancel must preserve the exact Condition root, Patient-subject source step, columns, and output identity.');
  const visibleAfterPatientCancel = await getCurrentRows('condition');
  check('persistence', 'Cancel leaves the Condition table and Patient-subject source step unchanged', true, {
    outputId, draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest,
    route: documentForOutput(afterCancel, outputId).route,
    patientSourceNodeId: patientSourceIdentity.sourceOccurrenceId,
    visibleRows: visibleAfterPatientCancel.rows.map(row => row.values),
  });
  const { selector: applySelector } = await openRowSettings('reopen Condition row settings for focus route');
  const { choice: applySpecimenChoice, assessment: focusRootAssessment } = await selectSpecimen({ selector: applySelector }, 'Focus route Apply');
  const focusRoute = await choosePath(applySelector, {
    kind: 'focus', nodeId: applySpecimenChoice.value.slice('node:'.length), assessment: focusRootAssessment,
  });
  const focusPreviewRows = assertVisibleRows(focusRoute.grid, independent.focusRows, {
    phase: 'Observation.focus Condition-to-Specimen Preview',
  });
  const beforeApply = await readBuilder();
  assert.equal(beforeApply.draftVersion, sourceCheckpoint.draftVersion,
    'Receipt-bound Preview must leave the source draft unchanged until Apply.');
  assert.equal(beforeApply.draftDigest, sourceCheckpoint.draftDigest);
  assert.deepEqual(documentForOutput(beforeApply, outputId), sourceCheckpoint.document,
    'Focus route Preview must leave the source Condition and Patient documents unchanged until Apply.');
  check('correctness', 'focus-inbound route exposes and resolves AMBIGUOUS_ROUTE_REBASE_EDGE inline',
    focusRoute.edgeRepairEvidence?.status === 'BLOCKED' &&
      focusRoute.edgeRepairEvidence?.code === 'AMBIGUOUS_ROUTE_REBASE_EDGE' &&
      focusRoute.edgeRepairEvidence?.resolvedStatus === 'READY' &&
      focusRoute.edgeRepairEvidence?.selectedEdgeId === focusRoute.edgeRepairEvidence?.selectedRelationship?.edgeId, {
    outputId,
    unresolvedCode: focusRoute.edgeRepairEvidence?.code,
    unresolvedMessage: focusRoute.edgeRepairEvidence?.message,
    alternatives: focusRoute.edgeRepairEvidence?.alternatives,
    selectedEdgeId: focusRoute.edgeRepairEvidence?.selectedEdgeId,
    resolutionRequest: focusRoute.edgeRepairEvidence?.resolutionRequest,
    selectedRelationship: focusRoute.edgeRepairEvidence?.selectedRelationship,
    receiptId: focusRoute.previewIdentity.receiptId,
  });
  check('correctness', 'focus-inbound route Preview matches its distinct exact Condition and Patient fixture rows', true, {
    outputId, rows: focusPreviewRows,
    expectedSpecimenId: oracle.alternatives.find(choice => choice.key === 'observation-focus-inbound').specimenId,
  });
  const applyRowChange = focusRoute.panel.getByRole('button', { name: 'Apply row change', exact: true });
  await act('Apply the receipt-bound Condition-to-Specimen focus route', applyRowChange, control => control.click(), {
    after: () => focusRoute.panel.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
  });
  let appliedBuilder = await readBuilder();
  let appliedDocument = documentForOutput(appliedBuilder, outputId);
  assert.equal(appliedDocument.rootResourceType, 'Specimen');
  assert.equal(appliedDocument.output.id, outputId, 'Apply must preserve the existing table output identity.');
  assert.equal(appliedDocument.columns.length, sourceCheckpoint.document.columns.length,
    'Connected root Apply must preserve the exact authored columns.');
  assert(appliedDocument.route.children.some(child => child.resourceType === 'Observation' && child.relationship === 'specimen_observation'),
    `Applied Specimen route must retain the selected reverse specimen relationship: ${JSON.stringify(appliedDocument.route)}`);
  const observationNode = appliedDocument.route.children.find(child => child.resourceType === 'Observation');
  const selectedFocusEdge = observationNode?.children?.find(child =>
    child.resourceType === 'Condition' && child.relationship === 'focus_Condition');
  assert(selectedFocusEdge,
    `Applied route must retain the literal Observation.focus → Condition edge: ${JSON.stringify(appliedDocument.route)}`);
  assert.equal(selectedFocusEdge.catalogEdgeId, focusRoute.edgeRepairEvidence.selectedEdgeId,
    'Applied route must retain the exact compiler Focus edge selected from AMBIGUOUS_ROUTE_REBASE_EDGE alternatives.');
  const conditionNode = selectedFocusEdge;
  assert(conditionNode.children?.some(child => child.resourceType === 'Patient' && child.relationship === 'subject_Patient'),
    `Applied route must include the exact Patient-subject path: ${JSON.stringify(appliedDocument.route)}`);
  const appliedRoute = structuredClone(appliedDocument.route);
  const appliedPreviewGrid = await collectPreviewRows(page, { timeout: ACTION_TIMEOUT_MS });
  const appliedVisibleRows = assertVisibleRows(appliedPreviewGrid, independent.focusRows, {
    phase: 'Applied Specimen Condition and Patient source rows',
  });

  const addSpecimenID = page.getByTestId('construction-action-add-columns');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-add-columns"]');
    return button && !button.disabled;
  }, null, { timeout: ACTION_TIMEOUT_MS });
  await act('open Specimen fields after route Apply', addSpecimenID, control => control.click(), {
    after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const appliedFields = page.getByRole('button', { name: 'Fields and related data', exact: true });
  await act('browse Specimen fields after route Apply', appliedFields, control => control.click(), {
    after: () => page.getByTestId('construction-add-columns-source').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  await openDisclosure('[data-testid="feature-catalog-raw-fields"]', 'open raw FHIR fields for Specimen.id');
  const specimenIDChoice = page.getByRole('checkbox', { name: 'Select Specimen.id', exact: true });
  await specimenIDChoice.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  assert.equal(await specimenIDChoice.isEnabled(), true,
    'The applied Specimen root must expose its exact root ID for persisted row verification.');
  await act('select Specimen.id for exact root rows', specimenIDChoice, control => control.check(), {
    after: async () => assert.equal(await specimenIDChoice.isChecked(), true),
  });
  const addSelectedSpecimenID = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  await act('preview Specimen.id root rows', addSelectedSpecimenID, control => control.click(), {
    after: async ({ remainingTimeout }) => {
      assert.equal(await page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true }).count(), 0,
        'Root Specimen.id must resolve through one defaultable choice without a route/form dialog.');
      await page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'visible', timeout: remainingTimeout() });
    },
  });
  const specimenIDProposal = page.getByTestId('construction-choice-proposal-panel');
  await page.waitForFunction(() => ['ready', 'error'].includes(
    document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus),
  null, { timeout: ACTION_TIMEOUT_MS });
  assert.equal(await specimenIDProposal.getAttribute('data-proposal-status'), 'ready');
  const applySpecimenID = specimenIDProposal.getByRole('button', { name: 'Apply columns', exact: true });
  await act('apply Specimen.id root field', applySpecimenID, control => control.click(), {
    after: () => specimenIDProposal.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
  });
  appliedBuilder = await readBuilder();
  appliedDocument = documentForOutput(appliedBuilder, outputId);
  assert.equal(appliedDocument.rootResourceType, 'Specimen');
  assert.deepEqual(appliedDocument.route, appliedRoute,
    'Adding the root ID field must not change the selected Condition/Observation relationship route.');
  const savedSpecimenRows = independent.focusRows;
  const visibleAfterApply = await collectPreviewRows(page, { timeout: ACTION_TIMEOUT_MS });
  const exactVisibleAfterApply = assertVisibleRows(visibleAfterApply, savedSpecimenRows, {
    includesSpecimenID: true, phase: 'Applied Specimen exact source rows',
  });
  check('correctness', 'Apply renders exact Specimen IDs with the selected Condition and Patient values', true, {
    outputId, rows: exactVisibleAfterApply, route: appliedDocument.route,
  });
  await act('close Specimen field chooser after Apply', page.getByTestId('construction-close-operation-editor'),
    control => control.click(), {
      after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
    });

  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  const reloadedGrid = await collectPreviewRows(page, { timeout: ACTION_TIMEOUT_MS });
  const exactReloadRows = assertVisibleRows(reloadedGrid, savedSpecimenRows, {
    includesSpecimenID: true, phase: 'Reloaded Specimen exact source rows',
  });
  const reloadedBuilder = await readBuilder();
  const reloadedDocument = documentForOutput(reloadedBuilder, outputId);
  assert.equal(reloadedDocument.rootResourceType, 'Specimen');
  assert.equal(reloadedDocument.output.id, outputId);
  assert.deepEqual(reloadedDocument.route, appliedDocument.route,
    'Reload must preserve the selected catalog route and Patient-subject relationship identities.');
  check('persistence', 'Applied Specimen root, route identities, and exact source rows survive reload', true, {
    outputId, rootResourceType: reloadedDocument.rootResourceType, route: reloadedDocument.route,
    draftVersion: reloadedBuilder.draftVersion, draftDigest: reloadedBuilder.draftDigest,
    rows: exactReloadRows,
  });

  const finalIndependent = readConditionSpecimenRouteOracle(target.fixtureDir);
  assert.deepEqual(finalIndependent.sourceIDs, independent.sourceIDs,
    'All independent Patient, Condition, Observation, and Specimen source records must stay unchanged.');
  assert.deepEqual(finalIndependent.sourceHashes, independent.sourceHashes,
    'All four independent FHIR fixture files must remain byte-for-byte unchanged.');
  check('correctness', 'independent ambiguity fixture source IDs remain unchanged through the lifecycle', true,
    { sourceIDs: finalIndependent.sourceIDs, hashes: finalIndependent.sourceHashes });
  report.target.conditionSpecimenRouteLifecycle = {
    outputId,
    sourceCheckpoint,
    pickerContract,
    selectedCatalogValue: specimenChoice.value,
    canceledPatientSubjectRoute: {
      path: patientRoute.routeLabel,
      receiptId: patientRoute.previewIdentity.receiptId,
      rows: patientPreviewRows,
      savedDocumentUnchanged: true,
    },
    appliedFocusRoute: {
      path: focusRoute.routeLabel,
      resolvedCode: focusRoute.edgeRepairEvidence?.code,
      edgeRepair: focusRoute.edgeRepairEvidence,
      receiptId: focusRoute.previewIdentity.receiptId,
      rows: focusPreviewRows,
      postApplyRows: appliedVisibleRows,
      persistedRows: exactReloadRows,
      route: reloadedDocument.route,
    },
    unmatchedSpecimen: oracle.unmatched,
  };
};
