import assert from 'node:assert/strict';
import { browserURL } from './builder-url.mjs';
import {
  click,
  configureNativePage,
  fill,
  goto,
  recordPlaywrightTiming,
  reload,
  waitFor,
} from '../helpers/playwright-authoring-page.mjs';
import { readScope001RepeatedPivotOracle } from '../helpers/scope-001-repeated-pivot-oracle.mjs';

const ROOT_ROWS = 3;
const EXPANDED_ROWS = 7;
const PIVOT_ROWS = 3;
const renderBudgetMs = 5_000;

const proposalReady = (rows, status = 'ready') => `(()=>{
  const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
  return panel?.dataset.proposalStatus===${JSON.stringify(status)}
    && document.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length===${rows};
})()`;

const proposalID = async page => page.locator('[data-testid="construction-proposal-panel"]')
  .evaluate(panel => panel.dataset.proposalId ?? null).catch(() => null);

const readyAfterProposalChange = previousID => `${proposalReady(PIVOT_ROWS)}
  &&document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId!==${JSON.stringify(previousID)}`;

const exactGridReady = (headerTerms, expectedRows, proposal = false) => {
  const expected = [...expectedRows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  return `(()=>{
    const isProposal=${proposal};
    const root=document.querySelector(isProposal
      ? '[data-testid="construction-proposal-preview"]'
      : '[data-testid="preview-table-scroll"] [role="table"]');
    const table=isProposal?(root?.querySelector('table')??root):root;
    if(!table)return false;
    const headers=isProposal
      ? [...table.querySelectorAll('thead th')].map(cell=>{
          const spans=[...cell.querySelectorAll(':scope > span')];
          return (spans[0]?.innerText??cell.innerText).split('\\n')[0].replace(/\\s+\\([^)]*\\)$/,'').trim().toLowerCase();
        })
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell=>
          cell.innerText.split('\\n')[0].replace(/\\s+\\([^)]*\\)$/,'').trim().toLowerCase());
    const dataHeaders=headers.filter(header=>! /^(?:row|row number|#)$/i.test(header));
    const indexes=${JSON.stringify(headerTerms)}.map(term=>dataHeaders.findIndex(header=>header.includes(term.toLowerCase())));
    if(indexes.some(index=>index<0))return false;
    const nodes=isProposal
      ? [...table.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
      : [...table.querySelectorAll('[role="row"]')].slice(1);
    if(nodes.length!==${expected.length})return false;
    const actual=nodes.map(row=>{
      const cells=isProposal?[...row.querySelectorAll('td')]:[...row.querySelectorAll('[role="cell"]')];
      return indexes.map(index=>cells[index]?.innerText.trim()??'');
    }).sort((left,right)=>JSON.stringify(left).localeCompare(JSON.stringify(right)));
    return JSON.stringify(actual)===${JSON.stringify(JSON.stringify(expected))}
      &&(isProposal||table.getAttribute('aria-rowcount')===String(${expected.length + 1}));
  })()`;
};

const exactProposalReady = (headerTerms, expectedRows) =>
  `${proposalReady(expectedRows.length)}&&${exactGridReady(headerTerms, expectedRows, true)}`;

const choiceProposalReady = rows => `(()=>{
  const panel=document.querySelector('[data-testid="construction-choice-proposal-panel"]');
  return panel?.dataset.proposalStatus==='ready'
    && document.querySelectorAll('[data-testid="construction-proposal-preview-row"]').length===${rows};
})()`;

const exactChoiceProposalReady = (headerTerms, expectedRows) =>
  `${choiceProposalReady(expectedRows.length)}&&${exactGridReady(headerTerms, expectedRows, true)}`;

const normalizedHeader = value => String(value ?? '').split('\n')[0]
  .replace(/\s+\([^)]*\)$/, '').replace(/\s+/g, ' ').trim();

const isNativeRowHeader = value => /^(?:row|row number|#)$/i.test(normalizedHeader(value));

const exactPivotHeaderSetReady = (categories, alphaLabel = 'alpha', proposal = false) => {
  const expectedCategories = categories.map(category => category === 'alpha' ? alphaLabel : category)
    .map(category => category.toLowerCase()).sort();
  return `(()=>{
    const isProposal=${proposal};
    const root=document.querySelector(isProposal
      ? '[data-testid="construction-proposal-preview"]'
      : '[data-testid="preview-table-scroll"] [role="table"]');
    const table=isProposal?(root?.querySelector('table')??root):root;
    if(!table)return false;
    const headers=isProposal
      ? [...table.querySelectorAll('thead th')].map(cell=>{
          const spans=[...cell.querySelectorAll(':scope > span')];
          return (spans[0]?.innerText??cell.innerText).split('\\n')[0].replace(/\\s+\\([^)]*\\)$/,'').trim().toLowerCase();
        })
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell=>
          cell.innerText.split('\\n')[0].replace(/\\s+\\([^)]*\\)$/,'').trim().toLowerCase());
    const rowHeaders=headers.filter(header=>/^(?:row|row number|#)$/i.test(header));
    const dataHeaders=headers.filter(header=>! /^(?:row|row number|#)$/i.test(header));
    const groupHeaders=dataHeaders.filter(header=>header==='observation id');
    const actualCategories=dataHeaders.filter(header=>header!=='observation id').sort();
    return rowHeaders.length<=1&&groupHeaders.length===1
      &&JSON.stringify(actualCategories)===${JSON.stringify(JSON.stringify(expectedCategories))};
  })()`;
};

const gridSnapshot = async (page, proposal = false) => {
  const selector = proposal
    ? '[data-testid="construction-proposal-preview"]'
    : '[data-testid="preview-table-scroll"] [role="table"]';
  return page.locator(selector).evaluate((root, isProposal) => {
    const table = isProposal ? (root.querySelector('table') ?? root) : root;
    const headers = isProposal
      ? [...table.querySelectorAll('thead th')].map(cell => {
        const spans = [...cell.querySelectorAll(':scope > span')];
        return (spans[0]?.innerText ?? cell.innerText).trim();
      })
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim());
    const rows = isProposal
      ? [...table.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()))
      : [...table.querySelectorAll('[role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
    return { headers, rows };
  }, proposal).then(snapshot => {
    const normalizedHeaders = snapshot.headers.map(normalizedHeader);
    const nativeRowHeaders = proposal ? [] : normalizedHeaders.filter(isNativeRowHeader);
    return {
      ...snapshot,
      nativeRowHeaders,
      headers: normalizedHeaders.filter(header => proposal || !isNativeRowHeader(header)),
    };
  });
};

const expectedExpandedTuples = oracle => oracle.expandedRows
  .map(row => [row.id, row.category, String(row.value)])
  .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));

const actualExpandedTuples = (snapshot, labels) => {
  const [idIndex, categoryIndex, valueIndex] = labels.map(label =>
    snapshot.headers.findIndex(header => header.toLowerCase().includes(label.toLowerCase())));
  assert(idIndex >= 0, `Preview lacks Observation ID: ${JSON.stringify(snapshot.headers)}`);
  assert(categoryIndex >= 0, `Preview lacks component category: ${JSON.stringify(snapshot.headers)}`);
  assert(valueIndex >= 0, `Preview lacks component value: ${JSON.stringify(snapshot.headers)}`);
  return snapshot.rows.map(row => [row[idIndex], row[categoryIndex], row[valueIndex]])
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
};

const expectedPivotTuples = (oracle, { policy = 'SUM' } = {}) => oracle.pivotRows
  .map(row => [row.id,
    policy === 'MAX' && row.id === 'scope-001-observation-a' ? '3' : (row.alpha === null ? '—' : String(row.alpha)),
    row.beta === null ? '—' : String(row.beta),
    row.gamma === null ? '—' : String(row.gamma)])
  .sort((left, right) => left[0].localeCompare(right[0]));

const expectedRootTuples = oracle => oracle.observationIDs.map(id => [id]);

const expectedExpandedIDTuples = oracle => oracle.expandedRows.map(row => [row.id]);

const expectedPivotHeaders = alphaLabel => ['Observation ID', alphaLabel, 'beta', 'gamma'];

const exactPivotReady = (oracle, { proposal = false, policy = 'SUM', alphaLabel = 'alpha' } = {}) =>
  `${exactGridReady(expectedPivotHeaders(alphaLabel), expectedPivotTuples(oracle, { policy }), proposal)}
    &&${exactPivotHeaderSetReady(oracle.categories, alphaLabel, proposal)}`;

const exactPivotProposalReady = (oracle, { policy = 'SUM', alphaLabel = 'alpha' } = {}) =>
  `${exactProposalReady(expectedPivotHeaders(alphaLabel), expectedPivotTuples(oracle, { policy }))}
    &&${exactPivotHeaderSetReady(oracle.categories, alphaLabel, true)}`;

const assertPivotGrid = async (page, oracle, { proposal = false, policy = 'SUM', alphaLabel = 'alpha' } = {}) => {
  const snapshot = await gridSnapshot(page, proposal);
  const groupHeaderCount = snapshot.headers.filter(header => header.toLowerCase() === 'observation id').length;
  const actualCategoryHeaders = snapshot.headers
    .filter(header => header.toLowerCase() !== 'observation id' && !isNativeRowHeader(header))
    .map(header => header.toLowerCase()).sort();
  const expectedCategoryHeaders = oracle.categories
    .map(category => (category === 'alpha' ? alphaLabel : category).toLowerCase()).sort();
  assert.equal(groupHeaderCount, 1, `Pivot must expose exactly one Observation ID group header: ${JSON.stringify(snapshot.headers)}`);
  assert(snapshot.nativeRowHeaders.length <= 1,
    `Native preview may expose at most one row-number gutter header: ${JSON.stringify(snapshot.nativeRowHeaders)}`);
  assert.deepEqual(actualCategoryHeaders, expectedCategoryHeaders,
    'Pivot proposal/output headers must contain exactly the oracle category set');
  const indexes = ['Observation ID', alphaLabel, 'beta', 'gamma'].map(label =>
    snapshot.headers.findIndex(header => header.toLowerCase() === label.toLowerCase()));
  assert(indexes.every(index => index >= 0), `Pivot headers do not match the expected group/category labels: ${JSON.stringify(snapshot.headers)}`);
  const actual = snapshot.rows.map(row => indexes.map(index => row[index])).sort((left, right) => left[0].localeCompare(right[0]));
  const expected = expectedPivotTuples(oracle, { policy });
  assert.deepEqual(actual, expected, `${policy} Pivot rows must match the independent literal fixture oracle`);
  return { headers: snapshot.headers, rows: actual };
};

const requireCheck = (check, dimension, name, passed, evidence = {}) => {
  check(dimension, name, passed, evidence);
  assert(passed, `SCOPE-001 required check failed: ${name}`);
};

const timed = (report, page, workflow, name, action, after, dimension = 'usability') =>
  recordPlaywrightTiming(report, page, workflow, {
    name,
    action,
    after,
    timeout: renderBudgetMs,
    budget: renderBudgetMs,
    dimension,
  });

const sourceExpansionDialog = '[role="dialog"][aria-label="Expand a repeated source field"]';
const pivotEditor = '[data-testid="construction-reshape-pivot"]';

export const isRootComponentChoiceLabel = label =>
  /Occurrence base \(root\)\s+\(component\[\]\)\s*$/i.test(String(label ?? '').replace(/\s+/g, ' ').trim());

const sourceComponentChoice = async page => {
  const options = await page.locator(`${sourceExpansionDialog} select[aria-label="Repeated source field"]`)
    .evaluate(select => [...select.options].map(option => ({
      value: option.value,
      label: option.textContent.trim(),
    })));
  return options.filter(option => option.value && isRootComponentChoiceLabel(option.label));
};

const addComponentFields = async ({ page, report, workflow }) => {
  await timed(report, page, workflow, 'open native Add columns for expanded components',
    () => click(workflow, 'button', { includes: 'Add columns:' }),
    `Boolean(document.querySelector('[aria-label="Add columns editor"]'))`);
  await timed(report, page, workflow, 'show native Fields and related data catalog',
    () => click(workflow, '[aria-label="Add columns editor"] button', { name: 'Fields and related data' }),
    `Boolean(document.querySelector('[data-testid="feature-catalog-raw-fields"]'))`);
  const rawFields = '[aria-label="Add columns editor"] [data-testid="feature-catalog-raw-fields"]';
  if (!(await page.locator(rawFields).evaluate(node => node.open))) {
    await timed(report, page, workflow, 'open native Raw FHIR fields catalog',
      () => click(workflow, `${rawFields} > summary`, { name: 'Raw FHIR fields (advanced)' }),
      `document.querySelector(${JSON.stringify(rawFields)})?.open===true`);
  }
  const available = await page.locator('[aria-label="Add columns editor"] input[type="checkbox"][aria-label]')
    .evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label')).filter(Boolean));
  const codeField = available.find(label => /Observation\.component\[\].*code.*text/i.test(label));
  const valueField = available.find(label => /Observation\.component\[\].*valueInteger/i.test(label));
  assert(codeField, `The expanded fixture must expose its repeated code field: ${JSON.stringify(available)}`);
  assert(valueField, `The expanded fixture must expose its repeated integer field: ${JSON.stringify(available)}`);
  await timed(report, page, workflow, 'select the component category and integer features', async () => {
    for (const field of [codeField, valueField]) {
      const checkbox = page.getByLabel(field, { exact: true });
      if (!(await checkbox.isChecked())) await click(workflow, '[aria-label="Add columns editor"] input[type="checkbox"][aria-label]', { name: field });
    }
  }, `Boolean([...document.querySelectorAll('[aria-label="Add columns editor"] button')]
    .find(button=>button.innerText.includes('Add 2 selected features')&&!button.disabled))`);
  await recordPlaywrightTiming(report, page, workflow, {
    name: 'open native scalar forms for the component features',
    action: () => click(workflow, '[aria-label="Add columns editor"] button', { includes: 'Add 2 selected features' }),
    after: `Boolean([...document.querySelectorAll('[role="dialog"] h2')]
      .some(heading => heading.textContent.trim() === 'Choose how to add these fields'))`,
    settle: ({ timeoutMs }) => page.getByRole('dialog', {
      name: 'Choose how to add these fields', exact: true,
    }).waitFor({ state: 'visible', timeout: timeoutMs }),
    timeout: renderBudgetMs,
    budget: renderBudgetMs,
  });
  const radios = await page.getByRole('dialog').getByRole('radio').evaluateAll(inputs =>
    inputs.map(input => ({ label: input.getAttribute('aria-label'), name: input.name, checked: input.checked })));
  const codeChoice = radios.find(item => /Component Code Text: Use the first value/i.test(item.label ?? ''));
  const valueChoice = radios.find(item => /Component Value Integer: Use the first value/i.test(item.label ?? ''));
  assert(codeChoice, `Native choice dialog omitted FIRST component code form: ${JSON.stringify(radios)}`);
  assert(valueChoice, `Native choice dialog omitted FIRST component integer form: ${JSON.stringify(radios)}`);
  await timed(report, page, workflow, 'choose FIRST for component category and value fields', async () => {
    for (const choice of [codeChoice, valueChoice]) {
      if (!choice.checked) {
        const radio = page.getByRole('radio', { name: choice.label, exact: true });
        await workflow.action(`select native FIRST form ${choice.label}`, radio, () => radio.click());
      }
    }
  }, `Boolean([...document.querySelectorAll('[role="dialog"] button')]
    .find(button=>button.innerText.trim()==='Add 2 columns'&&!button.disabled))`);
  await timed(report, page, workflow, 'preview native FIRST category and value columns',
    () => click(workflow, '[role="dialog"] button', { name: 'Add 2 columns' }),
    exactChoiceProposalReady(['Observation ID', 'Component Code Text', 'Component Value Integer'],
      expectedExpandedTuples(report.target.fixtureOracle)));
  const proposal = await gridSnapshot(page, true);
  const expanded = actualExpandedTuples(proposal, ['Observation ID', 'Component Code Text', 'Component Value Integer']);
  const expected = expectedExpandedTuples(report.target.fixtureOracle);
  requireCheck(workflow.check, 'correctness', 'native component field proposal preserves all seven exact expanded category/value tuples',
    JSON.stringify(expanded) === JSON.stringify(expected), { headers: proposal.headers, actual: expanded, expected });
  await timed(report, page, workflow, 'apply exact component category and value fields',
    () => click(workflow, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' }),
    exactGridReady(['Observation ID', 'Component Code Text', 'Component Value Integer'], expectedExpandedTuples(report.target.fixtureOracle)), 'persistence');
  await timed(report, page, workflow, 'close native column editor to render expanded source rows',
    () => click(workflow, 'button', { name: 'Close operation editor' }),
    exactGridReady(['Observation ID', 'Component Code Text', 'Component Value Integer'], expectedExpandedTuples(report.target.fixtureOracle)));
  const applied = await gridSnapshot(page);
  const appliedTuples = actualExpandedTuples(applied, ['Observation ID', 'Component Code Text', 'Component Value Integer']);
  requireCheck(workflow.check, 'correctness', 'expanded source table contains every exact component category/value tuple',
    JSON.stringify(appliedTuples) === JSON.stringify(expected), { headers: applied.headers, actual: appliedTuples, expected });
  return {
    idLabel: applied.headers.find(header => /Observation ID/i.test(header)),
    codeLabel: applied.headers.find(header => /Component Code Text/i.test(header)),
    valueLabel: applied.headers.find(header => /Component Value Integer/i.test(header)),
  };
};

const optionForField = async (page, label, matcher) => page.locator(`select[aria-label=${JSON.stringify(label)}]`)
  .evaluate((select, pattern) => [...select.options]
    .filter(option => option.value && new RegExp(pattern, 'i').test(option.textContent ?? ''))
    .map(option => ({ value: option.value, label: option.textContent.trim() })), matcher.source);

const configurePivot = async ({ page, report, workflow, fields, policy }) => {
  if (!await page.locator(`${pivotEditor} select[aria-label="Pivot category field"]`).count()) {
    await timed(report, page, workflow, 'open native Pivot rows editor', async () => {
      await click(workflow, '[data-testid="construction-rows-settings-trigger"]');
      await waitFor(page, `document.querySelector('[role="dialog"][aria-label="Row definition settings"]')?.querySelector('[data-testid="construction-action-pivot-rows"]')?.disabled===false`);
      await click(workflow, '[role="dialog"][aria-label="Row definition settings"] button', {
        includes: 'Turn categories into columns',
      });
    }, `Boolean(document.querySelector('${pivotEditor} select[aria-label="Pivot category field"]'))`);
  }
  const groupLabels = await page.locator(`${pivotEditor} input[type="checkbox"][aria-label^="Pivot group "]`)
    .evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label'), checked: input.checked })));
  const identityGroup = groupLabels.find(item => /Observation ID/i.test(item.label ?? ''));
  assert(identityGroup, `Pivot editor omitted the source Observation ID group field: ${JSON.stringify(groupLabels)}`);
  if (!identityGroup.checked) {
    await timed(report, page, workflow, 'group Pivot by Observation ID',
      () => click(workflow, `${pivotEditor} input[type="checkbox"][aria-label]`, { name: identityGroup.label }),
      `document.querySelector(${JSON.stringify(`${pivotEditor} input[type="checkbox"][aria-label=${JSON.stringify(identityGroup.label)}]`)})?.checked===true`);
  }

  const categoryOptions = await optionForField(page, 'Pivot category field', /component.*code.*text/i);
  const valueOptions = await optionForField(page, 'Pivot values field', /component.*value.?integer/i);
  assert.equal(categoryOptions.length, 1, `Pivot must expose one expanded component category source: ${JSON.stringify(categoryOptions)}`);
  assert.equal(valueOptions.length, 1, `Pivot must expose one expanded component integer value source: ${JSON.stringify(valueOptions)}`);
  const categorySelect = page.locator('select[aria-label="Pivot category field"]');
  const valueSelect = page.locator('select[aria-label="Pivot values field"]');
  await timed(report, page, workflow, 'select expanded component category source',
    () => workflow.action('select Pivot category source', categorySelect, () => categorySelect.selectOption(categoryOptions[0].value)),
    `document.querySelector('select[aria-label="Pivot category field"]')?.value===${JSON.stringify(categoryOptions[0].value)}`);
  await timed(report, page, workflow, 'select expanded component integer value source',
    () => workflow.action('select Pivot value source', valueSelect, () => valueSelect.selectOption(valueOptions[0].value)),
    `document.querySelector('select[aria-label="Pivot values field"]')?.value===${JSON.stringify(valueOptions[0].value)}`);
  const categoryDisclosure = '[data-testid="construction-reshape-pivot-categories"]';
  await waitFor(page, `Boolean(document.querySelector(${JSON.stringify(categoryDisclosure)}))`);
  if (!(await page.locator(categoryDisclosure).evaluate(node => node.open))) {
    await timed(report, page, workflow, 'open discovered Pivot categories',
      () => click(workflow, `${categoryDisclosure} > summary`),
      `document.querySelector(${JSON.stringify(categoryDisclosure)})?.open===true`);
  }
  await waitFor(page, `document.querySelectorAll('${categoryDisclosure} input[aria-label^="Include category "]').length>=3`);
  const categoryControls = await page.locator(`${categoryDisclosure} input[aria-label^="Include category "]`)
    .evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label'), checked: input.checked })));
  const actualCategoryChoices = categoryControls.map(item => item.label).sort();
  const expectedCategoryChoices = report.target.fixtureOracle.categories
    .map(category => `Include category ${category}`).sort();
  assert.deepEqual(actualCategoryChoices, expectedCategoryChoices,
    'Native Pivot discovery must expose exactly the independent oracle category set');
  for (const category of report.target.fixtureOracle.categories) {
    const choice = categoryControls.find(item => item.label === `Include category ${category}`);
    assert(choice, `Pivot discovery omitted fixture category ${category}: ${JSON.stringify(categoryControls)}`);
    if (!choice.checked) {
      await timed(report, page, workflow, `include Pivot category ${category}`,
        () => click(workflow, `${categoryDisclosure} input[type="checkbox"][aria-label]`, { name: choice.label }),
        `document.querySelector(${JSON.stringify(`${categoryDisclosure} input[aria-label=${JSON.stringify(choice.label)}]`)})?.checked===true`);
    }
  }
  const advanced = '[data-testid="construction-reshape-pivot-advanced"]';
  if (!(await page.locator(advanced).evaluate(node => node.open))) {
    await timed(report, page, workflow, 'open Pivot advanced settings',
      () => click(workflow, `${advanced} > summary`),
      `document.querySelector(${JSON.stringify(advanced)})?.open===true`);
  }
  const policySelect = page.locator('select[aria-label="Pivot duplicate policy"]');
  if (await policySelect.inputValue() !== policy) {
    const previousID = await proposalID(page);
    await timed(report, page, workflow, `select Pivot duplicate policy ${policy}`,
      () => workflow.action(`select Pivot duplicate policy ${policy}`, policySelect,
        () => policySelect.selectOption(policy)),
      `${readyAfterProposalChange(previousID)}&&document.querySelector('select[aria-label="Pivot duplicate policy"]')?.value===${JSON.stringify(policy)}
        &&${exactPivotProposalReady(report.target.fixtureOracle, { policy })}`);
  }
  await waitFor(page, exactPivotProposalReady(report.target.fixtureOracle, { policy }));
  return { categoryOption: categoryOptions[0], valueOption: valueOptions[0], fields };
};

const openHistoryStep = async (page, report, workflow, selectorPrefix, pattern) => {
  const steps = await page.locator('[data-testid^="construction-history-step-"]')
    .evaluateAll(nodes => nodes.map(node => ({ testId: node.getAttribute('data-testid'), text: node.innerText })));
  const step = steps.findLast(item => pattern.test(item.text));
  assert(step, `Saved construction history omitted ${pattern}: ${JSON.stringify(steps)}`);
  await timed(report, page, workflow, `select saved ${pattern.source} history step`,
    () => click(workflow, `[data-testid=${JSON.stringify(step.testId)}]`),
    `Boolean(document.querySelector('[data-testid^=${JSON.stringify(selectorPrefix)}]'))`);
  const controls = await page.locator(`[data-testid^="${selectorPrefix}"]`).evaluateAll(nodes =>
    nodes.map(node => node.getAttribute('data-testid')));
  assert.equal(controls.length, 1, `Expected one ${selectorPrefix} control: ${JSON.stringify(controls)}`);
  return controls[0];
};

export const scope001RepeatedExpandPivotWorkflow = async (workflow, context) => {
  const { page, report } = workflow;
  configureNativePage(page);
  const oracle = readScope001RepeatedPivotOracle(context.target.fixtureDir);
  report.target.fixtureOracle = {
    fixtureDir: context.target.fixtureDir,
    patientIDs: oracle.patientIDs,
    observationIDs: oracle.observationIDs,
    expandedRows: oracle.expandedRows,
    categories: oracle.categories,
    pivotRows: oracle.pivotRows,
  };
  requireCheck(workflow.check, 'correctness', 'fresh fixture contains the exact three Observation roots and seven typed component values',
    oracle.observationIDs.length === ROOT_ROWS && oracle.expandedRows.length === EXPANDED_ROWS &&
      JSON.stringify(oracle.categories) === JSON.stringify(['alpha', 'beta', 'gamma']), report.target.fixtureOracle);

  const { target, runID } = context;
  await timed(report, page, workflow, 'open Builder in fresh fixture project',
    () => goto(page, browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'),
      "document.body.innerText.includes('New explorer')"),
    `document.body.innerText.includes('New explorer')`);
  await timed(report, page, workflow, 'open native New explorer form',
    () => click(workflow, 'summary', { name: 'New explorer' }),
    `Boolean(document.querySelector('#new-explorer-name'))`);
  const title = `Verify ${runID.slice(-10)} SCOPE-001`;
  await fill(workflow, '#new-explorer-name', title);
  await timed(report, page, workflow, 'create disposable SCOPE-001 Explorer',
    () => click(workflow, 'button', { name: 'Create blank' }),
    `document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent.trim()===${JSON.stringify(title)}`);
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'SCOPE-001 must author in a new fixture Explorer');
  report.target.explorer = explorer;

  await fill(workflow, '#first-table-name', 'Observations');
  await timed(report, page, workflow, 'choose Observation root and render all three source records',
    () => click(workflow, 'button', { name: 'Choose Observation rows' }),
    exactGridReady(['Observation ID'], expectedRootTuples(oracle)), 'correctness');
  const rootGrid = await gridSnapshot(page);
  const rootIDIndex = rootGrid.headers.findIndex(header => /Observation ID/i.test(header));
  assert(rootIDIndex >= 0, `Initial root preview omitted Observation ID: ${JSON.stringify(rootGrid.headers)}`);
  const rootIDs = rootGrid.rows.map(row => row[rootIDIndex]).sort();
  requireCheck(workflow.check, 'correctness', 'native Observation root preview renders the exact independent fixture IDs',
    JSON.stringify(rootIDs) === JSON.stringify(oracle.observationIDs), { headers: rootGrid.headers, rootIDs, expected: oracle.observationIDs });
  const tableButton = page.locator('[data-testid^="construction-table-"]');
  await tableButton.waitFor({ state: 'visible' });
  const outputTabId = await tableButton.getAttribute('data-testid');
  const outputId = outputTabId.replace(/^construction-table-/, '');
  report.target.outputId = outputId;

  const openExpansion = async name => {
    if (!await page.locator('[aria-label="Row definition settings"]').count()) {
      await timed(report, page, workflow, 'open Configure rows for repeated source expansion',
        () => click(workflow, '[data-testid="construction-rows-settings-trigger"]'),
        `Boolean(document.querySelector('[aria-label="Row definition settings"]'))`);
    }
    await timed(report, page, workflow, name,
      () => click(workflow, 'button[data-testid="construction-reshape-expand-source"]'),
      `Boolean(document.querySelector(${JSON.stringify(`${sourceExpansionDialog} select[aria-label="Repeated source field"]`)}))`);
    const options = await sourceComponentChoice(page);
    assert.equal(options.length, 1, `Repeated-source choices must expose one Observation.component[]: ${JSON.stringify(options)}`);
    const repeatedField = page.locator(`${sourceExpansionDialog} select[aria-label="Repeated source field"]`);
    await timed(report, page, workflow, 'preview Observation.component[] with PRESERVE_PARENT',
      () => workflow.action('select Observation.component[] for source expansion', repeatedField,
        () => repeatedField.selectOption(options[0].value)),
      `(()=>{const d=document.querySelector(${JSON.stringify(sourceExpansionDialog)});return d?.querySelector('[aria-label="Source expansion preview"]')?.innerText.includes('3 rows → 7 rows')&&d?.querySelector('select[aria-label="When a record has no values"]')?.value==='PRESERVE_PARENT'&&d?.querySelector('[data-testid="construction-source-expand-apply"]')?.disabled===false})()`);
    const dialog = await page.locator(sourceExpansionDialog).evaluate(node => ({
      field: node.querySelector('select[aria-label="Repeated source field"]')?.selectedOptions[0]?.textContent.trim(),
      policy: node.querySelector('select[aria-label="When a record has no values"]')?.value,
      preview: node.querySelector('[aria-label="Source expansion preview"]')?.innerText.trim(),
    }));
    requireCheck(workflow.check, 'correctness', 'native repeated-source chooser previews seven component rows with PRESERVE_PARENT',
      isRootComponentChoiceLabel(dialog.field) && dialog.policy === 'PRESERVE_PARENT' && /3 rows\s*→\s*7 rows/.test(dialog.preview ?? ''), dialog);
  };

  await openExpansion('open native Observation.component[] expansion for Cancel');
  await timed(report, page, workflow, 'Cancel initial repeated component expansion',
    () => click(workflow, `${sourceExpansionDialog} header button`, { name: 'Cancel' }),
    `!document.querySelector(${JSON.stringify(sourceExpansionDialog)})`);
  await timed(report, page, workflow, 'return to root table after expansion Cancel',
    () => click(workflow, '[aria-label="Row definition settings"] button', { name: 'Back to table' }),
    exactGridReady(['Observation ID'], expectedRootTuples(oracle)));
  const canceledRoot = await gridSnapshot(page);
  const canceledIDs = canceledRoot.rows.map(row => row[canceledRoot.headers.findIndex(header => /Observation ID/i.test(header))]).sort();
  requireCheck(workflow.check, 'persistence', 'expansion Cancel preserves the three original Observation root rows',
    JSON.stringify(canceledIDs) === JSON.stringify(oracle.observationIDs), { canceledIDs, expected: oracle.observationIDs });

  await openExpansion('reopen native Observation.component[] expansion for Apply');
  await timed(report, page, workflow, 'Apply PRESERVE_PARENT Observation.component[] expansion',
    () => click(workflow, `${sourceExpansionDialog} [data-testid="construction-source-expand-apply"]`),
    `!document.querySelector(${JSON.stringify(sourceExpansionDialog)})&&${exactGridReady(['Observation ID'], expectedExpandedIDTuples(oracle))}`, 'persistence');
  await timed(report, page, workflow, 'return to expanded component rows',
    () => click(workflow, '[aria-label="Row definition settings"] button', { name: 'Back to table' }),
    exactGridReady(['Observation ID'], expectedExpandedIDTuples(oracle)));
  const expandedGrid = await gridSnapshot(page);
  const expandedIDs = expandedGrid.rows.map(row => row[expandedGrid.headers.findIndex(header => /Observation ID/i.test(header))]).sort();
  const expectedExpandedIDs = oracle.expandedRows.map(row => row.id).sort();
  requireCheck(workflow.check, 'correctness', 'PRESERVE_PARENT expansion retains each populated Observation.component[] item identity',
    JSON.stringify(expandedIDs) === JSON.stringify(expectedExpandedIDs), { expandedIDs, expected: expectedExpandedIDs });

  const fields = await addComponentFields({ page, report, workflow });
  requireCheck(workflow.check, 'correctness', 'expanded table exposes the exact native Pivot group, category, and integer fields',
    Boolean(fields.idLabel && fields.codeLabel && fields.valueLabel), fields);

  const configureAndPreviewPivot = async policy => configurePivot({ page, report, workflow, fields, policy });
  await configureAndPreviewPivot('SUM');
  const initialPivotPreview = await assertPivotGrid(page, oracle, { proposal: true });
  requireCheck(workflow.check, 'correctness', 'SUM Pivot proposal exactly aggregates alpha, beta, and gamma by Observation ID',
    initialPivotPreview.rows.length === PIVOT_ROWS, initialPivotPreview);
  await timed(report, page, workflow, 'Cancel exact repeated component Pivot preview',
    () => click(workflow, '[data-testid="construction-cancel-proposal"]'),
    `!document.querySelector('[data-testid="construction-proposal-panel"]')&&${exactGridReady(['Observation ID', 'Component Code Text', 'Component Value Integer'], expectedExpandedTuples(oracle))}`);
  const canceledPivotSource = await gridSnapshot(page);
  const canceledExpanded = actualExpandedTuples(canceledPivotSource, ['Observation ID', 'Component Code Text', 'Component Value Integer']);
  requireCheck(workflow.check, 'persistence', 'Pivot Cancel preserves every expanded source component value',
    JSON.stringify(canceledExpanded) === JSON.stringify(expectedExpandedTuples(oracle)), { actual: canceledExpanded, expected: expectedExpandedTuples(oracle) });

  await configureAndPreviewPivot('SUM');
  const applyPivotPreview = await assertPivotGrid(page, oracle, { proposal: true });
  await timed(report, page, workflow, 'Apply SUM Pivot to exact repeated component categories',
    () => click(workflow, '[data-testid="construction-apply-proposal"]'),
    `!document.querySelector('[data-testid="construction-proposal-panel"]')&&${exactPivotReady(oracle)}`, 'persistence');
  const appliedPivot = await assertPivotGrid(page, oracle);
  requireCheck(workflow.check, 'persistence', 'SUM Pivot Apply renders the exact three distinct Observation groups',
    JSON.stringify(appliedPivot.rows) === JSON.stringify(applyPivotPreview.rows), appliedPivot);

  await timed(report, page, workflow, 'reload applied repeated component Pivot and render exact rows', async () => {
    await reload(page, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`);
    await click(workflow, `[data-testid=${JSON.stringify(`construction-table-${outputId}`)}]`);
  }, exactPivotReady(oracle), 'persistence');
  const reloadedPivot = await assertPivotGrid(page, oracle);
  requireCheck(workflow.check, 'persistence', 'SUM Pivot and all distinct aggregate values survive Builder reload',
    JSON.stringify(reloadedPivot.rows) === JSON.stringify(applyPivotPreview.rows), reloadedPivot);

  const editControl = await openHistoryStep(page, report, workflow, 'construction-edit-step-', /pivot|categories into columns/i);
  await timed(report, page, workflow, 'open saved repeated component Pivot editor',
    () => click(workflow, `[data-testid=${JSON.stringify(editControl)}]`),
    `Boolean(document.querySelector(${JSON.stringify(`${pivotEditor} select[aria-label="Pivot category field"]`)}))`);
  const advanced = '[data-testid="construction-reshape-pivot-advanced"]';
  if (!(await page.locator(advanced).evaluate(node => node.open))) {
    await timed(report, page, workflow, 'open saved Pivot duplicate-policy edit control',
      () => click(workflow, `${advanced} > summary`),
      `document.querySelector(${JSON.stringify(advanced)})?.open===true`);
  }
  const policySelect = page.locator('select[aria-label="Pivot duplicate policy"]');
  const priorPolicyProposal = await proposalID(page);
  await timed(report, page, workflow, 'edit duplicate Pivot bucket from SUM to MAX',
    () => workflow.action('select Pivot duplicate policy MAX for edit', policySelect,
      () => policySelect.selectOption('MAX')),
    `${readyAfterProposalChange(priorPolicyProposal)}&&document.querySelector('select[aria-label="Pivot duplicate policy"]')?.value==='MAX'&&${exactPivotProposalReady(oracle, { policy: 'MAX' })}`);
  const alphaHeading = page.getByLabel('Pivot output label alpha', { exact: true });
  const priorLabelProposal = await proposalID(page);
  await timed(report, page, workflow, 'edit alpha Pivot output label',
    () => workflow.action('rename alpha Pivot heading', alphaHeading, () => alphaHeading.fill('alpha maximum'), { editable: true }),
    `${readyAfterProposalChange(priorLabelProposal)}&&document.querySelector('[aria-label="Pivot output label alpha"]')?.value==='alpha maximum'
      &&${exactPivotProposalReady(oracle, { policy: 'MAX', alphaLabel: 'alpha maximum' })}`);
  const editedPivot = await assertPivotGrid(page, oracle, { proposal: true, policy: 'MAX', alphaLabel: 'alpha maximum' });
  requireCheck(workflow.check, 'correctness', 'MAX Pivot edit changes duplicate alpha from sum 5 to maximum 3 and retains other values',
    editedPivot.rows.find(row => row[0] === 'scope-001-observation-a')?.[1] === '3', editedPivot);
  await timed(report, page, workflow, 'Apply edited MAX Pivot and alpha heading',
    () => click(workflow, '[data-testid="construction-apply-proposal"]'),
    `!document.querySelector('[data-testid="construction-proposal-panel"]')&&${exactPivotReady(oracle, { policy: 'MAX', alphaLabel: 'alpha maximum' })}`, 'persistence');
  const appliedEditedPivot = await assertPivotGrid(page, oracle, { policy: 'MAX', alphaLabel: 'alpha maximum' });
  await timed(report, page, workflow, 'reload edited MAX Pivot and render exact rows', async () => {
    await reload(page, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`);
    await click(workflow, `[data-testid=${JSON.stringify(`construction-table-${outputId}`)}]`);
  }, exactPivotReady(oracle, { policy: 'MAX', alphaLabel: 'alpha maximum' }), 'persistence');
  const reloadedEditedPivot = await assertPivotGrid(page, oracle, { policy: 'MAX', alphaLabel: 'alpha maximum' });
  requireCheck(workflow.check, 'persistence', 'edited MAX policy and alpha heading survive Builder reload',
    JSON.stringify(reloadedEditedPivot.rows) === JSON.stringify(appliedEditedPivot.rows), reloadedEditedPivot);

  const removeControl = await openHistoryStep(page, report, workflow, 'construction-remove-step-', /pivot|categories into columns/i);
  await timed(report, page, workflow, 'propose removing the saved repeated component Pivot',
    () => click(workflow, `[data-testid=${JSON.stringify(removeControl)}]`),
    exactProposalReady(['Observation ID', 'Component Code Text', 'Component Value Integer'], expectedExpandedTuples(oracle)));
  const removalPreview = await gridSnapshot(page, true);
  const removalTuples = actualExpandedTuples(removalPreview, ['Observation ID', 'Component Code Text', 'Component Value Integer']);
  requireCheck(workflow.check, 'correctness', 'Pivot removal proposal restores all seven exact expanded component tuples',
    JSON.stringify(removalTuples) === JSON.stringify(expectedExpandedTuples(oracle)), { headers: removalPreview.headers, actual: removalTuples, expected: expectedExpandedTuples(oracle) });
  await timed(report, page, workflow, 'apply Pivot removal and restore expanded component rows',
    () => click(workflow, '[data-testid="construction-apply-proposal"]'),
    `!document.querySelector('[data-testid="construction-proposal-panel"]')&&${exactGridReady(['Observation ID', 'Component Code Text', 'Component Value Integer'], expectedExpandedTuples(oracle))}`, 'persistence');
  const restored = await gridSnapshot(page);
  const restoredTuples = actualExpandedTuples(restored, ['Observation ID', 'Component Code Text', 'Component Value Integer']);
  requireCheck(workflow.check, 'persistence', 'Pivot removal restores exact expanded category/value source rows',
    JSON.stringify(restoredTuples) === JSON.stringify(expectedExpandedTuples(oracle)), { actual: restoredTuples, expected: expectedExpandedTuples(oracle) });

  await timed(report, page, workflow, 'reload restored expanded component rows and render exact values', async () => {
    await reload(page, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`);
    await click(workflow, `[data-testid=${JSON.stringify(`construction-table-${outputId}`)}]`);
  }, exactGridReady(['Observation ID', 'Component Code Text', 'Component Value Integer'], expectedExpandedTuples(oracle)), 'persistence');
  const finalGrid = await gridSnapshot(page);
  const finalTuples = actualExpandedTuples(finalGrid, ['Observation ID', 'Component Code Text', 'Component Value Integer']);
  requireCheck(workflow.check, 'persistence', 'final reload preserves the restored expanded rows and removes Pivot output columns',
    JSON.stringify(finalTuples) === JSON.stringify(expectedExpandedTuples(oracle)) &&
      !finalGrid.headers.some(header => ['alpha', 'alpha maximum', 'beta', 'gamma'].includes(header.toLowerCase())),
    { headers: finalGrid.headers, actual: finalTuples, expected: expectedExpandedTuples(oracle) });

  report.target.lifecycle = [
    'Observation root', 'component[] PRESERVE_PARENT EXPANDED preview and Cancel', 'EXPANDED Apply',
    'native category/value columns', 'Pivot preview and Cancel', 'SUM Pivot Apply/reload',
    'saved Pivot edit to MAX and label/reload', 'Pivot removal restores expanded component rows/reload',
  ];
  report.target.actionToRenderBudgetMs = renderBudgetMs;
};
