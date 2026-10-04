import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { runBrowserCase } from './common.mjs';
import { click, evaluate, fill, recordBrowserTiming, reload, waitFor } from './browser.mjs';
import { recordCheck } from './report.mjs';
import { createBlankExplorer } from './workflows.mjs';

const sourceIDs = ['verify-repeat-empty', 'verify-repeat-missing', 'verify-repeat-two'];
const preservePairs = [
  ['verify-repeat-empty', 'NO_ITEM'],
  ['verify-repeat-missing', 'NO_ITEM'],
  ['verify-repeat-two', 'ALPHA'],
  ['verify-repeat-two', 'BETA'],
];
const excludePairs = [
  ['verify-repeat-two', 'ALPHA'],
  ['verify-repeat-two', 'BETA'],
];
const restoredSourcePairs = [
  ['verify-repeat-empty', 'NO_ITEM'],
  ['verify-repeat-missing', 'NO_ITEM'],
  ['verify-repeat-two', 'ALPHA'],
];

const requireCheck = (report, dimension, name, passed, evidence = {}) => {
  recordCheck(report, dimension, name, passed, evidence);
  if (!passed) throw new Error('required repeated-empty check failed: ' + name);
};

const parseNDJSON = (path) => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(Boolean)
  .map((line) => JSON.parse(line));

const fixtureContract = (context) => {
  const errors = [];
  const target = context?.target;
  const fixtureDir = target?.fixtureDir;
  if (context?.custom !== false || context?.seed?.fresh !== true || context?.seed?.reused === true) {
    errors.push('expected a fresh owned verification project');
  }
  if (target?.kind !== 'isolated' || !target.fixtureProject?.startsWith('loom_dev_verify_')) {
    errors.push('expected an isolated loom_dev_verify project');
  }
  if (!target?.fixtureGeneration || !fixtureDir) errors.push('expected a target generation and fixture directory');

  let patientRows = [];
  let observations = [];
  let ndjsonFiles = [];
  if (fixtureDir) {
    ndjsonFiles = readdirSync(fixtureDir).filter((name) => name.endsWith('.ndjson')).sort();
    if (JSON.stringify(ndjsonFiles) !== JSON.stringify(['Observation.ndjson', 'Patient.ndjson'])) {
      errors.push('expected only Patient.ndjson and Observation.ndjson in the isolated fixture');
    }
    try {
      patientRows = parseNDJSON(join(fixtureDir, 'Patient.ndjson'));
      observations = parseNDJSON(join(fixtureDir, 'Observation.ndjson'));
    } catch (error) {
      errors.push('fixture NDJSON could not be parsed: ' + (error instanceof Error ? error.message : String(error)));
    }
  }

  const expectedPatient = patientRows.length === 1 &&
    patientRows[0]?.resourceType === 'Patient' &&
    patientRows[0]?.id === 'verify-repeated-patient';
  if (!expectedPatient) errors.push('expected the single verify-repeated-patient Patient');

  const byID = new Map(observations.map((resource) => [resource?.id, resource]));
  if (observations.length !== 3 ||
      JSON.stringify([...byID.keys()].sort()) !== JSON.stringify(sourceIDs)) {
    errors.push('expected exactly the three named Observation fixture rows');
  }
  const populated = byID.get('verify-repeat-two');
  const empty = byID.get('verify-repeat-empty');
  const missing = byID.get('verify-repeat-missing');
  if (observations.some((resource) => resource?.resourceType !== 'Observation')) {
    errors.push('all fixture rows must be Observations');
  }
  if (!Array.isArray(populated?.component) || populated.component.length !== 2 ||
      populated.component[0]?.code?.text !== 'component-alpha' ||
      populated.component[1]?.code?.text !== 'component-beta') {
    errors.push('verify-repeat-two must contain the two named component items in order');
  }
  if (!Array.isArray(empty?.component) || empty.component.length !== 0) {
    errors.push('verify-repeat-empty must contain the literal empty component array');
  }
  if (!missing || Object.hasOwn(missing, 'component')) {
    errors.push('verify-repeat-missing must omit the component property');
  }
  if (observations.some((resource) => resource?.subject?.reference !== 'Patient/verify-repeated-patient')) {
    errors.push('all Observation subjects must point at the fixture Patient');
  }

  return {
    passed: errors.length === 0,
    errors,
    summary: {
      project: target?.fixtureProject,
      generation: target?.fixtureGeneration,
      fixtureDir,
      ndjsonFiles,
      patientIDs: patientRows.map((row) => row?.id),
      observationIDs: observations.map((row) => row?.id).sort(),
      populatedComponentCount: Array.isArray(populated?.component) ? populated.component.length : null,
      literalEmptyArray: Array.isArray(empty?.component) && empty.component.length === 0,
      missingComponentProperty: Boolean(missing) && !Object.hasOwn(missing, 'component'),
    },
  };
};

const mainReadyExpression = (rowCount) => `(()=>{const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(table&&table.getAttribute('aria-rowcount')===${JSON.stringify(String(rowCount + 1))}&&!document.querySelector('[data-testid="construction-proposal-preview"]'));})()`;
const rowDefinitionComparisonReadyExpression = (baseRows, candidateRows) => `(()=>{const section=document.querySelector('[aria-label="Row definition settings"] [aria-label="Row definition preview"]');const summary=section?.querySelector('p')?.innerText||'';const apply=[...document.querySelectorAll('[aria-label="Row definition settings"] button')].find(button=>button.innerText.trim()==='Apply row definition');return Boolean(summary.includes(${JSON.stringify(`${baseRows} rows → ${candidateRows} rows`)})&&apply&&!apply.disabled)})()`;
const rowDefinitionOpenExpression = `Boolean(document.querySelector('[aria-label="Row definition settings"]'))`;
const rowDefinitionReadyExpression = `Boolean(document.querySelector('[aria-label="Row definition settings"] select[aria-label="What should each row represent?"]'))`;

const readGrid = async (cdp, proposal = false) => evaluate(cdp, `(()=>{
  const root=document.querySelector(${JSON.stringify(proposal ? '[data-testid="construction-proposal-preview"]' : '[data-testid="preview-table-scroll"]')});
  const table=${proposal ? "root?.querySelector('table')" : "root?.querySelector('[role=table]')"};
  if(!table)return {ready:false,headers:[],rows:[],idIndex:-1,itemIndex:-1};
  const headerSelector=${JSON.stringify(proposal ? 'thead th' : '[role=columnheader]')};
  const headers=[...table.querySelectorAll(headerSelector)].map((cell)=>String(cell.innerText||cell.textContent||'').replace(/\\s+/g,' ').trim());
  const rows=${proposal
    ? "[...table.querySelectorAll('tbody tr')].map((row)=>[...row.querySelectorAll('td')].map((cell)=>String(cell.innerText||cell.textContent||'').replace(/\\s+/g,' ').trim()))"
    : "[...table.querySelectorAll('[role=row]')].slice(1).map((row)=>[...row.querySelectorAll('[role=cell]')].map((cell)=>String(cell.innerText||cell.textContent||'').replace(/\\s+/g,' ').trim()))"};
  const idIndex=headers.findIndex((header)=>/\\bid\\b/i.test(header));
  const itemIndex=headers.findIndex((header)=>{const normalized=header.toLowerCase().replace(/[^a-z0-9]+/g,' ');return normalized.includes('component')&&(normalized.includes('item')||normalized.includes('code')||normalized.includes('text'));});
  return {ready:true,headers,rows,idIndex,itemIndex,rowCount:table.getAttribute('aria-rowcount')};
})()`);

const classifyItem = (value) => {
  const item = String(value ?? '').trim();
  if (!item || item === '—') return 'NO_ITEM';
  if (item === 'component-alpha') return 'ALPHA';
  if (item === 'component-beta') return 'BETA';
  return `UNEXPECTED:${item}`;
};

const observedPairs = (grid) => {
  if (!grid.ready || grid.idIndex < 0 || grid.itemIndex < 0) return [];
  return grid.rows.map((row) => [row[grid.idIndex] ?? '', classifyItem(row[grid.itemIndex])])
    .sort((left, right) => left[0].localeCompare(right[0]) || left[1].localeCompare(right[1]));
};

const requirePairs = async (report, cdp, { name, expected, proposal = false }) => {
  const grid = await readGrid(cdp, proposal);
  const actual = observedPairs(grid);
  const sortedExpected = [...expected].sort((left, right) => left[0].localeCompare(right[0]) || left[1].localeCompare(right[1]));
  requireCheck(report, 'correctness', name,
    JSON.stringify(actual) === JSON.stringify(sortedExpected),
    { headers: grid.headers, pairs: actual, expected: sortedExpected, rowCount: grid.rows.length });
  return grid;
};

const readRowDefinitionComparison = async (cdp) => evaluate(cdp, `(()=>{const section=document.querySelector('[aria-label="Row definition settings"] [aria-label="Row definition preview"]');const summary=section?.querySelector('p')?.innerText||'';const rowCounts=summary.match(/(\\d+)\\s+rows\\s+→\\s+(\\d+)\\s+rows/);const membership=[...Array.from(section?.querySelector('[aria-label="Membership changes"]')?.querySelectorAll('li')??[])].map(item=>{const text=(item.innerText||'').replace(/\\s+/g,' ').trim();const [status,...identity]=text.split(' · ');return {status,identity:identity.join(' · ')}});return {ready:Boolean(section&&rowCounts),summary,baseRows:rowCounts?Number(rowCounts[1]):undefined,candidateRows:rowCounts?Number(rowCounts[2]):undefined,membership,removedCount:membership.filter(item=>item.status==='Removed').length,unchangedCount:membership.filter(item=>item.status==='Unchanged').length,addedCount:membership.filter(item=>item.status==='Added').length,affectedColumns:section?.querySelector('p:nth-of-type(2)')?.innerText}})()`);

const requireRowDefinitionComparison = async (report, cdp, { name, baseRows, candidateRows, removedCount, unchangedCount }) => {
  const comparison = await readRowDefinitionComparison(cdp);
  const membershipMatches = removedCount === undefined || (
    comparison.removedCount === removedCount && comparison.unchangedCount === unchangedCount &&
    comparison.membership.length === removedCount + unchangedCount
  );
  requireCheck(report, 'correctness', name,
    comparison.ready && comparison.baseRows === baseRows && comparison.candidateRows === candidateRows && membershipMatches,
    {
      ...comparison,
      expected: { baseRows, candidateRows, removedCount, unchangedCount },
      identityNote: 'The visible comparison lists row-identity digests, not fixture IDs or cell values; exact output identities and values are checked after Apply against the fixture oracle.',
    });
  return comparison;
};

const requireSourceIDs = async (report, cdp, { name, proposal = false, includeItem = false, checkItem = true }) => {
  const grid = await readGrid(cdp, proposal);
  const actual = grid.ready && grid.idIndex >= 0
    ? grid.rows.map((row) => row[grid.idIndex] ?? '').sort()
    : [];
  const expected = [...sourceIDs].sort();
  requireCheck(report, 'correctness', name,
    JSON.stringify(actual) === JSON.stringify(expected) && (!checkItem || (includeItem ? grid.itemIndex >= 0 : grid.itemIndex < 0)),
    { headers: grid.headers, observationIDs: actual, expectedObservationIDs: expected, itemColumnIndex: grid.itemIndex });
  return grid;
};

const openRowPanel = async (report, cdp, name, after) => {
  await recordBrowserTiming(report, cdp, {
    name,
    action: () => click(cdp, 'button', { name: 'Configure rows' }),
    after,
    timeout: 30000,
    budget: 5000,
  });
};

const openRowDefinition = (report, cdp, name) =>
  openRowPanel(report, cdp, name, rowDefinitionReadyExpression);

export const runRepeatedEmpty = (context) => runBrowserCase(
  context,
  'builder-authoring',
  'repeated-empty',
  async ({ cdp, report }) => {
    const contract = fixtureContract(context);
    requireCheck(report, 'correctness', 'case started with a fresh owned project and the exact repeated-empty fixture contract',
      contract.passed, { ...contract.summary, errors: contract.errors });
    report.target.fixtureGeneration = context.target.fixtureGeneration;

    const { explorer } = await createBlankExplorer(cdp, context.target, context.runID, 'repeated-empty', report);
    report.target.explorer = explorer;
    const assertScope = async (name) => {
      const scope = await evaluate(cdp, `(()=>{const query=new URLSearchParams(location.search);return {project:query.get('project'),explorer:query.get('explorer'),mode:query.get('mode')}})()`);
      const passed = scope.project === context.target.fixtureProject && scope.explorer === explorer && scope.mode === 'builder' &&
        report.target.fixtureGeneration === context.target.fixtureGeneration;
      requireCheck(report, 'correctness', name, passed, {
        ...scope,
        fixtureProject: context.target.fixtureProject,
        fixtureGeneration: context.target.fixtureGeneration,
      });
    };
    await assertScope('native source-row lifecycle remains in the exact isolated project, generation, and Explorer');
    await recordBrowserTiming(report, cdp, {
      name: 'choose Observation root and render the three source rows',
      action: () => click(cdp, 'button', { name: 'Choose Observation rows' }),
      after: mainReadyExpression(3),
      timeout: 30000,
      budget: 5000,
    });
    const source = await requireSourceIDs(report, cdp, {
      name: 'Observation root Preview contains the three exact fixture IDs before source expansion',
    });
    requireCheck(report, 'correctness', 'source Preview starts with one direct Observation identity column',
      source.headers[source.idIndex]?.toLowerCase().includes('id') && source.itemIndex < 0,
      { headers: source.headers, idIndex: source.idIndex, itemIndex: source.itemIndex });

    const openSourceEditor = async (name) => {
      if (!await evaluate(cdp, rowDefinitionOpenExpression)) {
        await openRowDefinition(report, cdp, 'open row-shape settings for source expansion');
      }
      await recordBrowserTiming(report, cdp, {
        name,
        action: () => click(cdp, 'button[data-testid="construction-reshape-expand-source"]'),
        after: `Boolean(document.querySelector('[role="dialog"][aria-label="Expand a repeated source field"] select[aria-label="Repeated source field"]'))`,
        timeout: 10000,
        budget: 5000,
      });
    };
    const chooseComponentSource = async (name) => {
      const choices = await evaluate(cdp, `(()=>{const select=document.querySelector('[role="dialog"][aria-label="Expand a repeated source field"] select[aria-label="Repeated source field"]');return [...(select?.options||[])].map(option=>({value:option.value,label:(option.textContent||'').trim()})).filter(option=>option.value)})()`);
      const choice = choices.find((option) => /component\[\]/i.test(option.label));
      requireCheck(report, 'correctness', 'source EXPANDED chooser exposes the exact Observation.component[] collection',
        Boolean(choice), { choices, selected: choice ?? null });
      if (!choice) throw new Error('The native repeated-source chooser did not expose Observation.component[]');
      await recordBrowserTiming(report, cdp, {
        name,
        action: () => fill(cdp, '[role="dialog"][aria-label="Expand a repeated source field"] select[aria-label="Repeated source field"]', choice.value),
        after: `(()=>{const dialog=document.querySelector('[role="dialog"][aria-label="Expand a repeated source field"]');const apply=dialog?.querySelector('[data-testid="construction-source-expand-apply"]');const preview=dialog?.querySelector('[aria-label="Source expansion preview"]');const policy=dialog?.querySelector('select[aria-label="When a record has no values"]');return Boolean(apply&&!apply.disabled&&preview?.innerText.includes('3 rows → 4 rows')&&policy?.value==='PRESERVE_PARENT')})()`,
        timeout: 30000,
        budget: 5000,
      });
      const candidate = await evaluate(cdp, `(()=>{const dialog=document.querySelector('[role="dialog"][aria-label="Expand a repeated source field"]');return {field:dialog?.querySelector('select[aria-label="Repeated source field"]')?.selectedOptions[0]?.textContent?.trim(),policy:dialog?.querySelector('select[aria-label="When a record has no values"]')?.value,preview:dialog?.querySelector('[aria-label="Source expansion preview"]')?.innerText,applyDisabled:dialog?.querySelector('[data-testid="construction-source-expand-apply"]')?.disabled}})()`);
      requireCheck(report, 'correctness', 'source-row preview defaults to PRESERVE_PARENT and predicts four rows from three source records',
        candidate.policy === 'PRESERVE_PARENT' && candidate.preview?.includes('3 rows → 4 rows') && candidate.applyDisabled === false,
        candidate);
      report.target.sourceExpandedChoice = { label: choice.label, policy: candidate.policy, preview: candidate.preview };
      return choice;
    };
    const closeRowDefinition = async (name) => {
      if (!await evaluate(cdp, rowDefinitionOpenExpression)) return;
      await recordBrowserTiming(report, cdp, {
        name,
        action: () => click(cdp, '[aria-label="Row definition settings"] button', { name: 'Back to table' }),
        after: `!(${rowDefinitionOpenExpression})`,
        timeout: 10000,
        budget: 5000,
      });
    };

    await openSourceEditor('open the native repeated-source expansion dialog');
    await chooseComponentSource('preview PRESERVE_PARENT for Observation.component[] without applying it');
    await recordBrowserTiming(report, cdp, {
      name: 'Cancel the source expansion proposal',
      action: () => click(cdp, '[role="dialog"][aria-label="Expand a repeated source field"] button', { name: 'Cancel' }),
      after: `!document.querySelector('[role="dialog"][aria-label="Expand a repeated source field"]')`,
      timeout: 10000,
      budget: 5000,
    });
    await closeRowDefinition('return to the table after cancelling source expansion');
    await waitFor(cdp, mainReadyExpression(3), 10000);
    await requireSourceIDs(report, cdp, {
      name: 'Cancel leaves all three original source Observation IDs unchanged',
    });

    await openSourceEditor('reopen the native repeated-source expansion dialog');
    const componentChoice = await chooseComponentSource('preview the source expansion that will be applied');
    await recordBrowserTiming(report, cdp, {
      name: 'Apply source EXPANDED row definition with PRESERVE_PARENT',
      action: () => click(cdp, '[role="dialog"][aria-label="Expand a repeated source field"] button[data-testid="construction-source-expand-apply"]'),
      after: `!document.querySelector('[role="dialog"][aria-label="Expand a repeated source field"]')&&${mainReadyExpression(4)}`,
      timeout: 30000,
      budget: 5000,
    });
    await closeRowDefinition('return to the table after applying source expansion');
    const preserveIDs = await readGrid(cdp);
    const preserveIDMultiset = preserveIDs.ready && preserveIDs.idIndex >= 0
      ? preserveIDs.rows.map((row) => row[preserveIDs.idIndex] ?? '').sort()
      : [];
    const expectedPreserveIDs = ['verify-repeat-empty', 'verify-repeat-missing', 'verify-repeat-two', 'verify-repeat-two'].sort();
    requireCheck(report, 'correctness', 'PRESERVE_PARENT keeps the literal-empty and missing parent while expanding both component items',
      JSON.stringify(preserveIDMultiset) === JSON.stringify(expectedPreserveIDs),
      { headers: preserveIDs.headers, observationIDMultiset: preserveIDMultiset, expected: expectedPreserveIDs });

    await waitFor(cdp, "document.querySelector('[data-testid=construction-action-add-columns]:not(:disabled)')", 30000);
    await click(cdp, 'button', { includes: 'Add columns:' });
    await waitFor(cdp, "document.querySelector('[aria-label=\"Add columns editor\"]')", 10000);
    await click(cdp, 'button', { name: 'Fields and related data' });
    const rawFieldsOpen = await evaluate(cdp, `Boolean(document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open)`);
    if (!rawFieldsOpen) await click(cdp, '[data-testid="feature-catalog-raw-fields"] summary');
    await waitFor(cdp, "document.querySelector('[aria-label=\"Add columns editor\"] input[type=\"checkbox\"][aria-label]')", 10000);
    const componentCodeChoices = await evaluate(cdp, `([...document.querySelectorAll('[aria-label="Add columns editor"] input[type="checkbox"][aria-label]')].map(input=>input.getAttribute('aria-label')).filter(label=>/Observation\.component.*code.*text/i.test(label||'')))`);
    requireCheck(report, 'correctness', 'source EXPANDED rows expose the scalar component code text field for exact item verification',
      componentCodeChoices.length > 0, { componentCodeChoices });
    if (componentCodeChoices.length === 0) throw new Error('No native Observation.component[].code.text field is available after source expansion');
    const componentCodeChoice = componentCodeChoices.find((label) => /component\[\]/i.test(label)) ?? componentCodeChoices[0];
    await click(cdp, '[aria-label="Add columns editor"] input[type="checkbox"][aria-label]', { name: componentCodeChoice });
    await recordBrowserTiming(report, cdp, {
      name: 'open native source and form choices for the component code text field',
      action: () => click(cdp, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' }),
      after: `Boolean(document.querySelector('[role="dialog"] input[type="radio"][aria-label="Component Code Text: Use the first value"]'))`,
      timeout: 30000,
      budget: 5000,
    });
    const scalarForm = await evaluate(cdp, `(()=>{const dialog=document.querySelector('[role="dialog"]');const field=dialog?.querySelector('input[type="radio"][aria-label="Component Code Text: Use the first value"]');const title=dialog?.querySelector('#catalog-selection-dialog-title')?.innerText?.trim();const buttons=[...Array.from(dialog?.querySelectorAll('button')??[])];return {dialogTitle:title,firstValueAvailable:Boolean(field),firstValueChecked:Boolean(field?.checked),addButton:buttons.find(button=>button.innerText.trim()==='Add 1 column')?.disabled}})()`);
    requireCheck(report, 'correctness', 'native component code field offers a selectable FIRST scalar form',
      scalarForm.dialogTitle === 'Choose how to add these fields' && scalarForm.firstValueAvailable && scalarForm.addButton === false,
      scalarForm);
    await recordBrowserTiming(report, cdp, {
      name: 'preview component code text with the native FIRST form',
      action: async () => {
        await click(cdp, '[role="dialog"] input[type="radio"]', { name: 'Component Code Text: Use the first value' });
        await waitFor(cdp, `document.querySelector('[role="dialog"] input[type="radio"][aria-label="Component Code Text: Use the first value"]')?.checked === true`, 5000);
        await click(cdp, '[role="dialog"] button', { name: 'Add 1 column' });
      },
      after: `['ready','error'].includes(document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus)`,
      timeout: 30000,
      budget: 5000,
    });
    const componentColumnProposal = await readGrid(cdp, true);
    const componentProposalPairs = observedPairs(componentColumnProposal);
    const sortedPreservePairs = [...preservePairs].sort((left, right) => left[0].localeCompare(right[0]) || left[1].localeCompare(right[1]));
    requireCheck(report, 'correctness', 'native item-field proposal renders exact alpha, beta, explicit-empty, and missing pairs',
      JSON.stringify(componentProposalPairs) === JSON.stringify(sortedPreservePairs),
      { headers: componentColumnProposal.headers, pairs: componentProposalPairs, expected: sortedPreservePairs });
    await recordBrowserTiming(report, cdp, {
      name: 'apply component code text field to the expanded source rows',
      action: () => click(cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' }),
      after: mainReadyExpression(4),
      timeout: 30000,
      budget: 5000,
    });
    await click(cdp, 'button', { name: 'Close operation editor' });
    await requirePairs(report, cdp, {
      name: 'saved PRESERVE_PARENT source rows show exact Observation/component item multiplicity',
      expected: preservePairs,
    });

    const readSavedExpandedShape = async (name, expectedPolicy) => {
      await waitFor(cdp, `Boolean(document.querySelector('[aria-label="Row definition settings"] select[aria-label="What should each row represent?"]')&&document.querySelector('[aria-label="Row definition settings"] select[aria-label="Unmatched record policy"]'))`, 10000);
      const state = await evaluate(cdp, `(()=>{const shape=document.querySelector('[aria-label="Row definition settings"] select[aria-label="What should each row represent?"]');const policy=document.querySelector('[aria-label="Row definition settings"] select[aria-label="Unmatched record policy"]');return {shapeValue:shape?.value,shapeLabel:shape?.selectedOptions[0]?.textContent?.trim(),policyValue:policy?.value,policyLabel:policy?.selectedOptions[0]?.textContent?.trim(),hasAuthoredHistory:Boolean(document.querySelector('[aria-label="Row definition settings"] section[aria-label="Applied row changes"]'))}})()`);
      requireCheck(report, 'persistence', name,
        Boolean(state.shapeValue?.startsWith('expanded:') && /component/i.test(state.shapeLabel ?? '') && state.policyValue?.endsWith(':' + expectedPolicy) && !state.hasAuthoredHistory), state);
      return state;
    };
    await reload(cdp, mainReadyExpression(4));
    await requirePairs(report, cdp, {
      name: 'PRESERVE_PARENT source EXPANDED rows and item values survive Builder reload',
      expected: preservePairs,
    });
    await assertScope('reload keeps the same authorized fixture project, generation, and Explorer');
    await openRowDefinition(report, cdp, 'open source EXPANDED row settings after reload');
    const savedPreserveShape = await readSavedExpandedShape('reloaded Rows controls restore component[] and PRESERVE_PARENT with no authored construction step', 'PRESERVE_PARENT');

    const policyChoice = async (policy) => evaluate(cdp, `(()=>{const select=document.querySelector('[aria-label="Row definition settings"] select[aria-label="Unmatched record policy"]');const option=[...(select?.options||[])].find(candidate=>candidate.value.endsWith(${JSON.stringify(':' + policy)}));return option?{value:option.value,label:(option.textContent||'').trim()}:undefined})()`);
    const chooseRowPolicy = async (policy, name, candidateRows, removedCount, unchangedCount) => {
      const option = await policyChoice(policy);
      requireCheck(report, 'correctness', `${policy} is available in the saved EXPANDED row definition`, Boolean(option), option ?? {});
      if (!option) throw new Error(`The saved EXPANDED row definition has no ${policy} policy option`);
      await recordBrowserTiming(report, cdp, {
        name,
        action: () => fill(cdp, '[aria-label="Row definition settings"] select[aria-label="Unmatched record policy"]', option.value),
        after: rowDefinitionComparisonReadyExpression(4, candidateRows),
        timeout: 30000,
        budget: 5000,
      });
      await requireRowDefinitionComparison(report, cdp, {
        name: `${policy} row-definition comparison shows four base rows and ${candidateRows} candidate rows`,
        baseRows: 4,
        candidateRows,
        removedCount,
        unchangedCount,
      });
      return option;
    };

    await chooseRowPolicy('EXCLUDE', 'preview EXCLUDE through the native saved-row policy control before Cancel', 2, 2, 2);
    await recordBrowserTiming(report, cdp, {
      name: 'Cancel the EXCLUDE row-definition edit',
      action: () => click(cdp, '[aria-label="Row definition settings"] button', { name: 'Cancel' }),
      after: mainReadyExpression(4),
      timeout: 10000,
      budget: 5000,
    });
    await requirePairs(report, cdp, {
      name: 'Cancel retains saved PRESERVE_PARENT pairs and all four rows',
      expected: preservePairs,
    });

    await openRowDefinition(report, cdp, 'reopen the source EXPANDED row definition after Cancel');
    const afterCancelShape = await readSavedExpandedShape('Cancel retains the same source row choice and PRESERVE_PARENT policy', 'PRESERVE_PARENT');
    requireCheck(report, 'persistence', 'Cancel keeps the exact same expanded source row choice',
      afterCancelShape.shapeValue === savedPreserveShape.shapeValue && afterCancelShape.policyValue === savedPreserveShape.policyValue,
      { before: savedPreserveShape, after: afterCancelShape });
    await chooseRowPolicy('EXCLUDE', 'preview EXCLUDE again from the same saved source-row definition', 2, 2, 2);
    await recordBrowserTiming(report, cdp, {
      name: 'apply EXCLUDE to the saved source EXPANDED row definition',
      action: () => click(cdp, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' }),
      after: mainReadyExpression(2),
      timeout: 30000,
      budget: 5000,
    });
    await requirePairs(report, cdp, {
      name: 'EXCLUDE source rows retain the two exact populated component items',
      expected: excludePairs,
    });

    await reload(cdp, mainReadyExpression(2));
    await requirePairs(report, cdp, {
      name: 'EXCLUDE source EXPANDED rows and component items survive Builder reload',
      expected: excludePairs,
    });
    await assertScope('EXCLUDE reload keeps the same authorized fixture project, generation, and Explorer');
    await openRowDefinition(report, cdp, 'open EXCLUDE source row definition after reload');
    await readSavedExpandedShape('reloaded Rows controls restore component[] and EXCLUDE without authored history', 'EXCLUDE');

    const recordsOption = await evaluate(cdp, `(()=>{const select=document.querySelector('[aria-label="Row definition settings"] select[aria-label="What should each row represent?"]');const option=[...(select?.options||[])].find(candidate=>candidate.value==='records');return option?{value:option.value,label:(option.textContent||'').trim()}:undefined})()`);
    requireCheck(report, 'correctness', 'Rows settings offers the original one-row-per-source-record shape for removal',
      Boolean(recordsOption), recordsOption ?? {});
    if (!recordsOption) throw new Error('The source row shape cannot be restored to records from the native Rows controls');
    await recordBrowserTiming(report, cdp, {
      name: 'preview removing source expansion by restoring one row per source record',
      action: () => fill(cdp, '[aria-label="Row definition settings"] select[aria-label="What should each row represent?"]', recordsOption.value),
      after: rowDefinitionComparisonReadyExpression(2, 3),
      timeout: 30000,
      budget: 5000,
    });
    await requireRowDefinitionComparison(report, cdp, {
      name: 'removing source EXPANDED previews a two-to-three row comparison',
      baseRows: 2,
      candidateRows: 3,
    });
    await recordBrowserTiming(report, cdp, {
      name: 'apply source-row removal and restore all three fixture Observation IDs',
      action: () => click(cdp, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' }),
      after: mainReadyExpression(3),
      timeout: 30000,
      budget: 5000,
    });
    await requirePairs(report, cdp, {
      name: 'source RECORDS restoration retains exact Observation/component first-value pairs after Apply',
      expected: restoredSourcePairs,
    });

    await reload(cdp, mainReadyExpression(3));
    await requirePairs(report, cdp, {
      name: 'source Observation/component first-value pairs survive Builder reload after row restoration',
      expected: restoredSourcePairs,
    });
    await assertScope('final reload keeps the exact fixture project, generation, and Explorer');
    await openRowDefinition(report, cdp, 'verify the restored source row definition after reload');
    const restoredShape = await evaluate(cdp, `(()=>{const shape=document.querySelector('[aria-label="Row definition settings"] select[aria-label="What should each row represent?"]');return {value:shape?.value,label:shape?.selectedOptions[0]?.textContent?.trim(),history:Boolean(document.querySelector('[aria-label="Row definition settings"] section[aria-label="Applied row changes"]'))}})()`);
    requireCheck(report, 'persistence', 'removal persists the source RECORDS row definition without an authored EXPAND step',
      restoredShape.value === 'records' && !restoredShape.history, restoredShape);
    report.target.explorer = explorer;
    report.target.componentCodeChoice = componentCodeChoice;
  },
);
