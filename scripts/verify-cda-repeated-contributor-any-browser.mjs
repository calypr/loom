import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const sourceFreezeRoot = process.env.LOOM_SOURCE_FREEZE_ROOT ?? '/private/tmp/loom-construction-implementation';
const explorer = `repeated-contributor-any-browser-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-repeated-contributor-any-browser-${Date.now()}`;
const apiOrigin = (process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188').replace(/\/$/, '');
const uiOrigin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const browserRequestOrigins = new Set([apiOrigin, uiOrigin].map((origin) => new URL(origin).origin));
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const pageURL = `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`;
const candidatePatientLimit = 2000;
const relatedObservationLimit = 2;
const observationEdgeLimit = relatedObservationLimit + 1;
const categoryLimit = 8;
const codingPerCategoryLimit = 8;
const manyWitnessPatientLimit = 10;
const manyCandidateLimit = 500;
const report = {
  explorer, project, generation, protectedExplorer, ownedExplorerId: explorer,
  protectedExplorerUntouched: true, browserExplorerRequests: [],
  controls: {
    covered: ['Related-record field search and selection: Observation.category[].coding[].code',
      'Repeated Contributor predicates: automatic ANY with EXISTS and exact nested code equality',
      'Raw nested-code oracle bounded to 8 categories and 8 codings per category',
      'Preview rows match the bounded raw resource-membership oracle with one row per matching terminal Observation',
      'Zero/one/many Patient witness availability is reported from a bounded first-2000 Patient scan',
      'No-match policies: PRESERVE_PARENT and EXCLUDE',
      'Preview, Cancel, Apply, reload, policy edit, remove Cancel, remove Apply, exact source restoration'],
    uncovered: ['Other Contributor fields and suggestion families',
      'Other repeated code paths such as Observation.code.coding[].code',
      'Alternative related paths and starting anchors', 'ERROR empty-policy recovery (covered by sibling verifier)'],
  },
  cases: [], requests: [], errors: [], started: new Date().toISOString(),
};
await mkdir(evidence, { recursive: true });
assert.notEqual(explorer, protectedExplorer, 'Only a fresh QA Explorer may be used');
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(sourceFreezeRoot);
report.sourceFreeze = { root: sourceFreezeRoot, startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };

let browser;
let builder;
let outputId;
const failedResponses = [];
const networkRequests = new Map();
const contributorSearchRequests = [];

const api = async (path, body) => {
  assert(!path.startsWith(`${root}/${protectedExplorer}/`), `Refusing to call the protected Explorer: ${path}`);
  if (path.startsWith(`${root}/`) && path !== `${root}/selections`) {
    assert(path.startsWith(`${root}/${explorer}/`), `Refusing to call an unowned Explorer route: ${path}`);
  }
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `repeated-contributor-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, status: response.status });
  assert(response.ok, `${path}: ${JSON.stringify(value)}`);
  return value;
};

const rawQuery = (query) => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1',
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 8_000_000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango did not return JSON: ${result.stdout.slice(-500)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
};

const sourceWitnesses = async (catalog) => {
  const findPatient = (bucket, predicate) => {
    const query = `FOR patient IN Patient
      FILTER patient.project == ${JSON.stringify(project)} AND patient.dataset_generation == ${JSON.stringify(generation)}
      SORT patient.id
      LIMIT ${candidatePatientLimit}
      LET sample = (
        FOR edge IN fhir_edge
          FILTER edge._to == patient._id AND edge.label == "subject_Patient"
            AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
            AND STARTS_WITH(edge._from, "Observation/")
          COLLECT observationKey = edge._from
          LIMIT 2
          RETURN observationKey
      )
      LET observationCount = LENGTH(sample)
      LET observations = (
        FOR observationKey IN sample
          LET observation = DOCUMENT(observationKey)
          FILTER observation.project == ${JSON.stringify(project)}
            AND observation.dataset_generation == ${JSON.stringify(generation)}
          LET categories = TO_ARRAY(observation.payload.category)
          FILTER LENGTH(categories) <= ${categoryLimit}
          LET oversizedCodingArrays = LENGTH(
            FOR category IN categories
              FILTER LENGTH(TO_ARRAY(category.coding)) > ${codingPerCategoryLimit}
              RETURN category
          )
          FILTER oversizedCodingArrays == 0
          LET nestedCodes = (
            FOR category IN categories
              FOR coding IN TO_ARRAY(category.coding)
                FILTER IS_STRING(coding.code) AND coding.code != ""
                RETURN { code: coding.code }
          )
          LET hasCodeValue = LENGTH(
            FOR category IN categories
              FOR coding IN TO_ARRAY(category.coding)
                FILTER IS_OBJECT(coding) AND HAS(coding, "code") AND coding.code != null
                RETURN 1
          ) > 0
          RETURN { id: observation.id, _id: observation._id, hasCodeValue,
            categoryCount: LENGTH(categories),
            codingCounts: (FOR category IN categories RETURN LENGTH(TO_ARRAY(category.coding))),
            nestedCodes }
      )
      FILTER ${predicate}
      FILTER LENGTH(observations) == LENGTH(sample)
      SORT patient.id
      LIMIT 1
      RETURN { patient: { id: patient.id, _id: patient._id }, observations }`;
    const [witness] = rawQuery(query);
    return witness?.patient?.id && witness?.patient?._id ? { bucket, ...witness } : undefined;
  };

  const zero = findPatient('zero', 'observationCount == 0');
  const one = findPatient('one', 'observationCount == 1');
  const patientNode = catalog.nodes.find((node) => node.resourceType === 'Patient');
  const observationNode = catalog.nodes.find((node) => node.resourceType === 'Observation');
  assert(patientNode && observationNode, 'Patient or Observation is absent from the current authoring catalog');
  const sourceCandidate = (catalog.candidates ?? []).find((candidate) =>
    candidate.nodeId === observationNode.nodeId
      && candidate.fieldPath === 'category[].coding[].code'
      && candidate.cardinality === 'many'
      && candidate.repeatedBoundaries?.map((boundary) => boundary.path).join('|')
        === 'category[]|category[].coding[]');
  assert(sourceCandidate,
    'Authoring catalog has no repeated Observation.category[].coding[].code code candidate');

  const manyCandidates = rawQuery(`FOR patient IN Patient
      FILTER patient.project == ${JSON.stringify(project)}
        AND patient.dataset_generation == ${JSON.stringify(generation)}
      SORT patient.id
      LIMIT ${candidatePatientLimit}
      LET observationKeys = (
        FOR edge IN fhir_edge
          FILTER edge._to == patient._id AND edge.label == "subject_Patient"
            AND edge.project == ${JSON.stringify(project)}
            AND edge.dataset_generation == ${JSON.stringify(generation)}
            AND STARTS_WITH(edge._from, "Observation/")
          COLLECT observationKey = edge._from
          SORT observationKey
          LIMIT ${observationEdgeLimit}
          RETURN observationKey
      )
      FILTER LENGTH(observationKeys) >= 2 AND LENGTH(observationKeys) < ${observationEdgeLimit}
      LET observations = (
        FOR observationKey IN observationKeys
          LET observation = DOCUMENT(observationKey)
          FILTER observation.project == ${JSON.stringify(project)}
            AND observation.dataset_generation == ${JSON.stringify(generation)}
          LET categories = TO_ARRAY(observation.payload.category)
          FILTER LENGTH(categories) <= ${categoryLimit}
          LET oversizedCodingArrays = LENGTH(
            FOR category IN categories
              FILTER LENGTH(TO_ARRAY(category.coding)) > ${codingPerCategoryLimit}
              RETURN category
          )
          FILTER oversizedCodingArrays == 0
          LET nestedCodes = (
            FOR category IN categories
              FOR coding IN TO_ARRAY(category.coding)
                FILTER IS_STRING(coding.code) AND coding.code != ""
                RETURN { code: coding.code }
          )
          LET hasCodeValue = LENGTH(
            FOR category IN categories
              FOR coding IN TO_ARRAY(category.coding)
                FILTER IS_OBJECT(coding) AND HAS(coding, "code") AND coding.code != null
                RETURN 1
          ) > 0
          RETURN { id: observation.id, _id: observation._id, hasCodeValue,
            categoryCount: LENGTH(categories),
            codingCounts: (FOR category IN categories RETURN LENGTH(TO_ARRAY(category.coding))),
            nestedCodes }
      )
      FILTER LENGTH(observations) == LENGTH(observationKeys)
      AND LENGTH(observations) <= ${relatedObservationLimit}
      LIMIT ${manyCandidateLimit}
      RETURN { patient: { id: patient.id, _id: patient._id }, observations }`);
  const excludedPatientIds = new Set([zero?.patient._id, one?.patient._id].filter(Boolean));
  const eligibleManyCandidates = manyCandidates.filter((candidate) => {
    if (excludedPatientIds.has(candidate.patient._id)) return false;
    const observations = candidate.observations;
    const values = new Set(observations.flatMap((observation) =>
      observation.nestedCodes.map((occurrence) => occurrence.code)));
    return [...values].some((value) => {
      const matchingRecords = observations.filter((observation) =>
        observation.nestedCodes.some((occurrence) => occurrence.code === value)).length;
      return matchingRecords > 0 && matchingRecords < observations.length;
    });
  });
  const noCodeCandidate = manyCandidates.find((candidate) => !excludedPatientIds.has(candidate.patient._id)
    && candidate.observations.every((observation) => observation.nestedCodes.length === 0));
  const sampledManyLimit = manyWitnessPatientLimit - (noCodeCandidate ? 1 : 0);
  const selectedIndexes = Array.from({ length: Math.min(sampledManyLimit, eligibleManyCandidates.length) }, (_, index) =>
    Math.round(index * (eligibleManyCandidates.length - 1) / Math.max(1, Math.min(sampledManyLimit, eligibleManyCandidates.length) - 1)));
  const manyWitnesses = selectedIndexes.map((index) => ({ witnessRole: 'partial-code', ...eligibleManyCandidates[index] }))
    .map((candidate) => ({ bucket: 'many', ...candidate }));
  if (noCodeCandidate) manyWitnesses.unshift({ bucket: 'many', witnessRole: 'no-code', ...noCodeCandidate });
  for (const witness of [one, ...manyWitnesses].filter(Boolean)) {
    for (const observation of witness.observations) {
      assert(observation.categoryCount <= categoryLimit
        && observation.codingCounts.every((count) => count <= codingPerCategoryLimit),
      'Raw nested-code witness exceeded the declared category or coding bounds');
    }
  }
  const witnesses = [zero, one, ...manyWitnesses].filter(Boolean);
  assert.equal(new Set(witnesses.map((item) => item.patient._id)).size, witnesses.length,
    'CDA zero/one/many witnesses must be distinct');
  const allObservations = witnesses.flatMap((witness) => witness.observations);
  const nestedCodeCounts = Object.fromEntries([...new Set(allObservations
    .flatMap((observation) => observation.nestedCodes.map((item) => item.code)))]
    .map((code) => [code, allObservations.filter((observation) =>
      observation.nestedCodes.some((item) => item.code === code)).length]));
  const detailsByCode = new Map();
  for (const observation of allObservations) {
    for (const occurrence of observation.nestedCodes) {
      const detail = detailsByCode.get(occurrence.code) ?? { occurrenceCount: 0 };
      detail.occurrenceCount += 1;
      detailsByCode.set(occurrence.code, detail);
    }
  }
  const nestedCodeDetails = Object.fromEntries([...detailsByCode].map(([code, detail]) => [code, {
    matchingObservationCount: nestedCodeCounts[code] ?? 0,
    occurrenceCount: detail.occurrenceCount,
    duplicateObservationCount: allObservations.filter((observation) =>
      observation.nestedCodes.filter((item) => item.code === code).length > 1).length,
  }]));
  const patientCountByBucket = Object.fromEntries(['zero', 'one', 'many'].map((bucket) => [bucket,
    witnesses.filter((witness) => witness.bucket === bucket).length]));
  report.oracleWitnessAvailability = {
    scan: `first ${candidatePatientLimit} Patient rows sorted by id`,
    patientCountByBucket,
    unavailableBuckets: Object.entries(patientCountByBucket).filter(([, count]) => count === 0).map(([bucket]) => bucket),
    boundedObservationEdgeLimit: observationEdgeLimit,
    categoryLimit,
    codingPerCategoryLimit,
    manyNoCodePatientFound: Boolean(noCodeCandidate),
    partialCodeManyPatientCandidateCount: eligibleManyCandidates.length,
  };
  assert(manyWitnesses.length > 0,
    `CDA fixture has no complete many-Observation Patient among the bounded ${candidatePatientLimit}-patient scan with ${observationEdgeLimit}-edge sentinel and nested-array caps`);
  return {
    witnesses,
    sourceCandidateId: sourceCandidate.candidateId,
    candidateCount: manyCandidates.length,
    nestedCodeCounts,
    nestedCodeDetails,
    observationCount: allObservations.length,
    patientCountByBucket,
    manyNoCodePatientFound: Boolean(noCodeCandidate),
    partialCodeManyPatientCandidateCount: eligibleManyCandidates.length,
  };
};

const command = async (commands) => {
  await api(base + '/commands', {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(base + '/builder');
};

const documentForOutput = (state = builder) => state.workspace.documents.find((item) => item.output.id === outputId);
const withoutStageIdentity = (columns) => columns.map(({ columnId: _stageId, ...column }) => column);

const assertStableSourceProjection = (state, baseline, canonicalSourceColumnId, phase) => {
  const sourceColumnName = baseline.columns[0]?.column;
  const restored = documentForOutput(state).columns.find((column) => column.column === sourceColumnName);
  assert(restored, `${phase}: authored source column ${sourceColumnName} is missing`);
  assert.equal(restored.columnId, canonicalSourceColumnId,
    `${phase}: compiler-owned source column identity changed`);
  assert.deepEqual(withoutStageIdentity(documentForOutput(state).columns), withoutStageIdentity(baseline.columns),
    `${phase}: authored source column binding, label, physical name, or table presentation changed`);
};

const codeRule = (step) => step.operation.relatedExpand;
const typedEqualsValue = (choice, value) => choice.source.logicalType === 'code'
  ? { kind: 'CODE', code: { code: value } }
  : { kind: 'STRING', string: value };
const assertPersistedEqualsRule = (related, choice, value, emptyPolicy, phase) => {
  assert.equal(related.emptyPolicy, emptyPolicy, `${phase}: no-match policy changed`);
  assert.deepEqual(related.contributorRule, {
    policy: 'ALL_MATCHES',
    predicate: { candidateId: choice.source.candidateId, quantifier: 'ANY', operator: 'EQUALS', value: typedEqualsValue(choice, value) },
  }, `${phase}: typed Contributor EQUALS predicate changed`);
  assert.deepEqual(related.contributorSource, choice.source, `${phase}: signed Contributor source changed`);
  assert.equal(related.contributorChoiceId, choice.choiceId, `${phase}: selected Contributor choice identity changed`);
  assert.equal(related.contributorSource.path, nestedCodePath, `${phase}: wrong Contributor field path`);
  assert.equal(related.contributorSource.cardinality, 'many', `${phase}: repeated source cardinality changed`);
  assert.deepEqual(related.contributorSource.repeatedBoundaries, choice.source.repeatedBoundaries,
    `${phase}: exact signed repeated boundaries changed`);
  assert.equal(related.contributorSource.resourceType, 'Observation', `${phase}: wrong Contributor resource type`);
  assert.equal(related.contributorSource.logicalType, choice.source.logicalType, `${phase}: returned Contributor logical type changed`);
  assert.equal(related.contributorRule.predicate.candidateId, related.contributorSource.candidateId,
    `${phase}: predicate and signed source candidate identities diverged`);
};

const rowsForNestedCode = (witnesses, value, preserveParent) => witnesses.flatMap((item) => {
  const matching = item.observations.filter((observation) =>
    observation.nestedCodes.some((occurrence) => occurrence.code === value))
    .map((observation) => [item.patient.id, observation.id]);
  return matching.length || !preserveParent ? matching : [[item.patient.id, '—']];
});

const rowsForRepeatedCodeExists = (witnesses, preserveParent) => witnesses.flatMap((item) => {
  const matching = item.observations.filter((observation) => observation.hasCodeValue)
    .map((observation) => [item.patient.id, observation.id]);
  return matching.length || !preserveParent ? matching : [[item.patient.id, '—']];
});

const assertUniqueTerminalRows = (rows, name) => {
  const terminalRows = rows.filter((row) => row[1] !== '—').map((row) => JSON.stringify(row));
  assert.equal(new Set(terminalRows).size, terminalRows.length,
    `${name}: a repeated primitive occurrence duplicated its terminal Observation record`);
};

const displayedRows = async () => browserEval(browser.cdp,
  `return (async()=>{
    const root=document.querySelector('[data-testid="preview-table-scroll"]');
    const table=root?.querySelector('[role="table"]');
    if(!root||!table)throw new Error('Preview table is not mounted');
    const total=Number(table.getAttribute('aria-rowcount'))-1;
    const rows=new Map();
    const settle=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
    root.scrollTop=0;
    await settle();
    for(let page=0;page<100&&rows.size<total;page++){
      for(const row of table.querySelectorAll('[role="row"]')){
        const ordinal=Number(row.firstElementChild?.textContent?.trim());
        if(!Number.isInteger(ordinal)||ordinal<1)continue;
        const cells=[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim());
        if(cells.length)rows.set(ordinal,cells);
      }
      const next=Math.min(root.scrollTop+Math.max(1,root.clientHeight/2),root.scrollHeight-root.clientHeight);
      if(rows.size>=total||next===root.scrollTop)break;
      root.scrollTop=next;
      await settle();
    }
    const ordered=[...rows.entries()].sort((left,right)=>left[0]-right[0]);
    root.scrollTop=0;
    await settle();
    return {total,ordinals:ordered.map(([ordinal])=>ordinal),rows:ordered.map(([,cells])=>cells)};
  })();`);

const revealControl = async (selector, includes) => {
  const snapshot = await browserEval(browser.cdp,
    `const normalize=value=>String(value??'').replace(/\\s+/g,' ').trim();
      const element=[...document.querySelectorAll(${JSON.stringify(selector)})].find(candidate=>
        ${includes === undefined ? 'true' : `normalize(candidate.getAttribute('aria-label')||candidate.innerText||candidate.textContent).toLowerCase().includes(${JSON.stringify(includes.toLowerCase())})`});
      if(!element)throw new Error('Could not reveal control: '+${JSON.stringify(selector)});
      element.scrollIntoView({block:'center',inline:'nearest',behavior:'instant'});
      await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
      const rect=element.getBoundingClientRect();
      return {top:rect.top,bottom:rect.bottom,viewportHeight:innerHeight,text:normalize(element.getAttribute('aria-label')||element.innerText||element.textContent)};`);
  assert(snapshot.top >= 0 && snapshot.bottom <= snapshot.viewportHeight,
    `Control remains outside the viewport after native scroll: ${JSON.stringify(snapshot)}`);
  return snapshot;
};

const assertRows = (actual, expected, name) => {
  assert(expected.length <= 25, `${name}: oracle has ${expected.length} rows; this verifier requires a fully visible fixture`);
  assertUniqueTerminalRows(expected, name);
  assertUniqueTerminalRows(actual, `${name} actual preview`);
  const sortRows = (rows) => rows.map((row) => JSON.stringify(row)).sort();
  assert.deepEqual(sortRows(actual), sortRows(expected), `${name}: exact rows differ from the independent CDA oracle`);
};

const recordAction = (name, startedAt, details = {}) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, ...details });
  return durationMs;
};

const rendered = async (expectedRows, name) => {
  await waitForBrowser(browser.cdp,
    `document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, expectedRows.length) + 1))} && !document.body.innerText.includes('Loading your table…')`);
  const snapshot = await displayedRows();
  const expectedCount = Math.min(25, expectedRows.length);
  assert.equal(snapshot.total, expectedCount, `${name}: preview aria-rowcount must match the bounded oracle`);
  assert.equal(snapshot.rows.length, expectedCount, `${name}: virtualized preview collector must visit every bounded row`);
  assert.deepEqual(snapshot.ordinals, Array.from({ length: expectedCount }, (_, index) => index + 1),
    `${name}: virtualized preview collector must visit each row ordinal exactly once`);
  assertRows(snapshot.rows, expectedRows, name);
  return snapshot.rows;
};

const proposal = async (name, startedAt, expectedRows) => {
  await waitForBrowser(browser.cdp,
    `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`);
  const result = await browserEval(browser.cdp,
    `const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {
      status:panel?.dataset.proposalStatus,text:panel?.innerText,
      rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};`);
  assert.equal(result.status, 'ready', `${name}: ${result.text}`);
  assertRows(result.rows, expectedRows, name);
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, rowCount: expectedRows.length, preview: result.rows });
  return result;
};

const open = async (expectedRows, name) => {
  const startedAt = Date.now();
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  const rows = await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: rows.length });
};

const startRelatedExpand = async () => {
  const startedAt = Date.now();
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false`);
  await click(browser.cdp, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForBrowser(browser.cdp, `document.querySelector('${panel} select[aria-label="Related record type"]')?.disabled === false`);
  await selectOption(browser.cdp, `${panel} select[aria-label="Related record type"]`, 'Observation');
  const route = 'Patient <-[subject]- Observation';
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(`${panel} input[aria-label="${route}"]`)})`);
  await click(browser.cdp, `${panel} input[aria-label="${route}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('${panel} [data-testid="construction-related-expand-contributor-options"]')`);
  recordAction('open-related-expand-editor', startedAt);
  return panel;
};

const nestedCodePath = 'category[].coding[].code';

const waitForContributorSearch = async (startedAt) => {
  const deadline = startedAt + 5000;
  while (Date.now() < deadline) {
    const request = contributorSearchRequests.findLast((item) => item.startedAt >= startedAt
      && item.body?.query === nestedCodePath && item.completedAt && item.response);
    if (request) return request.response;
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw new Error(`Timed out waiting for the native Observation.${nestedCodePath} Contributor catalog response`);
};

const chooseRepeatedContributorCondition = async (panel, oracle, actionName, condition) => {
  const startedAt = Date.now();
  const options = `${panel} [data-testid="construction-related-expand-contributors"]`;
  await selectOption(browser.cdp, `${panel} select[aria-label="If a current row has no matches"]`, 'PRESERVE_PARENT');
  const disclosure = `${panel} [data-testid="construction-related-expand-contributor-options"]`;
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(disclosure+' summary')})`);
  const disclosureState = await browserEval(browser.cdp,
    `return document.querySelector(${JSON.stringify(disclosure)})?.open ?? false;`);
  if (!disclosureState) {
    await revealControl(`${disclosure} summary`);
    await click(browser.cdp, `${disclosure} summary`);
    await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(disclosure)})?.open === true`);
  }
  const onlyRecords = `${options} label`;
  await revealControl(onlyRecords, 'Only records meeting a condition');
  await click(browser.cdp, onlyRecords, { includes: 'Only records meeting a condition' });
  await waitForBrowser(browser.cdp, `document.querySelector('${options} input[placeholder="Search field name or path"]')`);
  const search = `${options} input[placeholder="Search field name or path"]`;
  await revealControl(search);
  await click(browser.cdp, search);
  const searchStartedAt = Date.now();
  await browser.cdp.send('Input.insertText', { text: nestedCodePath });
  await waitForBrowser(browser.cdp,
    `[...document.querySelectorAll('${options} [role="group"][aria-label="Fields for related-record condition"] button')]
      .some(button=>button.innerText.replace(/\\s+/g,' ').includes(${JSON.stringify(nestedCodePath)}))`);
  const choiceResponse = await waitForContributorSearch(searchStartedAt);
  const visibleChoices = await browserEval(browser.cdp,
    `return [...document.querySelectorAll('${options} [role="group"][aria-label="Fields for related-record condition"] button')]
      .map(button=>({text:button.innerText.trim(),pressed:button.getAttribute('aria-pressed')}));`);
  const rawObservations = oracle.witnesses.flatMap((item) => item.observations);
  const choice = choiceResponse.choices.find((candidate) => candidate.source.path === nestedCodePath
    && candidate.source.resourceType === 'Observation'
    && candidate.source.candidateId === oracle.sourceCandidateId
    && ['string', 'code'].includes(candidate.source.logicalType)
    && candidate.source.cardinality === 'many'
    && candidate.operators.includes('EXISTS')
    && candidate.operators.includes('EQUALS')
    && (condition === 'EXISTS' || candidate.suggestedValues.some((value) => {
      const matchingCount = oracle.nestedCodeCounts[value] ?? 0;
      return matchingCount > 0 && matchingCount < rawObservations.length;
    })));
  assert(choice, `Native Observation.${nestedCodePath} choice did not return the bounded oracle's required field: ${JSON.stringify({
    choices: choiceResponse.choices.map((candidate) => ({ path: candidate.source.path, logicalType: candidate.source.logicalType,
      cardinality: candidate.source.cardinality, repeatedBoundaries: candidate.source.repeatedBoundaries,
      resourceType: candidate.source.resourceType, operators: candidate.operators, suggestedValueCount: candidate.suggestedValues.length })),
    targetValue: oracle.targetValue,
    nestedCodeCounts: oracle.nestedCodeCounts,
  })}`);
  assert(choice.choiceId && choice.source.candidateId && choice.source.nodeId,
    'Native Contributor catalog choice omitted its signed choice or exact candidate identity');
  assert.equal(choice.source.kind, 'FIELD', 'Repeated Contributor code must be a native field choice');
  assert(['string', 'code'].includes(choice.source.logicalType),
    `Compiler-proved repeated code path has unsupported equality type ${choice.source.logicalType}`);
  assert(Array.isArray(choice.source.repeatedBoundaries), 'Repeated Contributor choice omitted its signed boundaries');
  assert.deepEqual(choice.source.repeatedBoundaries.map((item) => item.path),
    ['category[]', 'category[].coding[]'], 'Signed choice omitted or changed the nested repeated boundaries');
  assert(choice.source.repeatedBoundaries.every((item) => Number.isInteger(item.maxItems) && item.maxItems > 0),
    'Repeated-boundary metadata must retain finite positive compiler bounds');
  assert.equal(choice.suggestionsSource, 'catalog', 'Contributor value suggestions must come from the native source catalog');
  let targetValue;
  if (condition === 'EQUALS') {
    const targetOptions = choice.suggestedValues.map((value) => ({ value, details: oracle.nestedCodeDetails[value] }))
      .filter(({ value, details }) => {
        const matchingCount = oracle.nestedCodeCounts[value] ?? 0;
        return details && matchingCount > 0 && matchingCount < rawObservations.length;
      })
      .sort((left, right) => Number(right.details.occurrenceCount > right.details.matchingObservationCount)
        - Number(left.details.occurrenceCount > left.details.matchingObservationCount)
        || right.details.duplicateObservationCount - left.details.duplicateObservationCount
        || left.value.localeCompare(right.value));
    const selectedTarget = targetOptions[0];
    if (!selectedTarget) {
      report.controls.uncovered.push('No native catalog suggestion matched a proper subset of the bounded raw Observation records, so repeated-code EQUALS subset behavior has no fixture witness.');
    }
    assert(selectedTarget,
      'Native repeated-code suggestions have no value that matches some but not all bounded raw Observations');
    const { details: targetDetails } = selectedTarget;
    targetValue = selectedTarget.value;
    oracle.targetValue = targetValue;
    oracle.targetOccurrenceCount = targetDetails.occurrenceCount;
    oracle.targetDuplicateObservationCount = targetDetails.duplicateObservationCount;
    report.oracle.targetCatalogSuggestion = targetValue;
    report.oracle.targetOccurrenceCount = targetDetails.occurrenceCount;
    report.oracle.targetDuplicateObservationCount = targetDetails.duplicateObservationCount;
    report.oracle.matchingObservationCount = targetDetails.matchingObservationCount;
    report.oracle.deduplicatedTerminalObservationCount = targetDetails.matchingObservationCount;
    const matchingObservations = rawObservations.filter((observation) =>
      observation.nestedCodes.some((occurrence) => occurrence.code === targetValue));
    const targetRows = rowsForNestedCode(oracle.witnesses, targetValue, false);
    assert.equal(targetRows.length, matchingObservations.length,
      'The raw equality oracle must emit one row per matching Observation, independent of occurrence count');
    assertUniqueTerminalRows(targetRows, 'raw repeated-code equality oracle');
    report.oracle.duplicateMatchCoverage = {
      rawOccurrenceCount: targetDetails.occurrenceCount,
      matchingObservationCount: targetDetails.matchingObservationCount,
      observationsWithDuplicateValue: targetDetails.duplicateObservationCount,
      duplicateFixtureExercised: targetDetails.duplicateObservationCount > 0,
      expectedTerminalRows: matchingObservations.length,
    };
    if (targetDetails.duplicateObservationCount > 0) {
      report.controls.covered.push('The selected code occurs more than once within at least one Observation; the exact preview still emits one row per terminal Observation.');
    } else {
      report.controls.uncovered.push('The selected code has no duplicate occurrences within one Observation in this bounded fixture, so the duplicate-occurrence case is not exercised.');
    }
    const matchedCount = oracle.nestedCodeCounts[targetValue] ?? 0;
    assert(matchedCount > 0 && matchedCount < rawObservations.length,
      'Returned native suggestion must match some but not all bounded raw Observations');
  }
  const fieldSummary = { path: choice.source.path, resourceType: choice.source.resourceType,
    logicalType: choice.source.logicalType, cardinality: choice.source.cardinality,
    repeatedBoundaries: choice.source.repeatedBoundaries, operators: choice.operators,
    suggestionsComplete: choice.suggestionsComplete, suggestionsSource: choice.suggestionsSource,
    suggestedValueCount: choice.suggestedValues.length };
  assert(visibleChoices.some((candidate) => candidate.text.split('\n')[0].trim() === choice.label
    && candidate.text.split('\n')[1]?.includes(nestedCodePath)
    && candidate.text.split('\n')[1]?.includes(choice.source.logicalType)),
  `The native nested-code choice was not offered as an actionable field: ${JSON.stringify(visibleChoices)}`);
  report.contributorChoice = fieldSummary;
  const contributorFieldButtons = `${options} [role="group"][aria-label="Fields for related-record condition"] button`;
  const fieldIdentity = `${choice.source.path} · ${choice.source.logicalType}`;
  await revealControl(contributorFieldButtons, fieldIdentity);
  await click(browser.cdp, contributorFieldButtons, { includes: fieldIdentity });
  await waitForBrowser(browser.cdp, `document.querySelector('${options} select')?.value === 'EXISTS'`);
  await revealControl(`${options} select`);
  if (condition === 'EQUALS') await selectOption(browser.cdp, `${options} select`, 'EQUALS');
  const helpText = await browserEval(browser.cdp,
    `return document.querySelector('${options} [role="note"]')?.innerText ?? '';`);
  assert(helpText.includes('any value')
    && (condition === 'EXISTS' || (choice.source.logicalType === 'code'
      ? helpText.includes('code alone') : helpText.includes('exact value below'))),
    `Repeated code condition did not explain its ANY/code-only match: ${helpText}`);
  const valueLabel = choice.source.logicalType === 'code' ? 'Code' : 'Exact value';
  if (condition === 'EQUALS') {
    await waitForBrowser(browser.cdp,
      `Boolean([...document.querySelectorAll('${options} label')].find(label=>label.innerText.trim().startsWith(${JSON.stringify(valueLabel)}))?.querySelector('input'))`);
    await revealControl(`${options} label`, valueLabel);
    const suggestions = `${options} [aria-label="Catalog value suggestions"] button`;
    await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll(${JSON.stringify(suggestions)})]
      .find(button=>button.innerText.trim()===${JSON.stringify(targetValue)}))`);
    await revealControl(suggestions, targetValue);
    await click(browser.cdp, suggestions, { name: targetValue });
    await waitForBrowser(browser.cdp,
      `([...document.querySelectorAll('${options} label')].find(label=>label.innerText.trim().startsWith(${JSON.stringify(valueLabel)}))?.querySelector('input')?.value === ${JSON.stringify(targetValue)})`);
  }
  const selected = await browserEval(browser.cdp,
    `const label=[...document.querySelectorAll('${options} label')].find(item=>item.innerText.trim().startsWith(${JSON.stringify(valueLabel)}));
      return {condition:document.querySelector('${options} select')?.value,
        field:document.querySelector('${options} [aria-pressed="true"]')?.innerText.trim(),
        value:label?.querySelector('input')?.value};`);
  assert.equal(selected.condition, condition);
  assert(selected.field?.includes(nestedCodePath) && selected.field?.includes(choice.source.logicalType), JSON.stringify(selected));
  if (condition === 'EQUALS') assert.equal(selected.value, targetValue);
  report.contributorRule = { condition: selected.condition, source: fieldSummary,
    ...(condition === 'EQUALS' ? { selectedCatalogValue: selected.value,
      valueKind: typedEqualsValue(choice, selected.value).kind } : {}),
    quantifier: 'ANY', noMatchPolicy: 'PRESERVE_PARENT' };
  recordAction(actionName, startedAt, { condition: selected.condition, sourceField: nestedCodePath,
    logicalType: choice.source.logicalType, quantifier: 'ANY',
    selectedFromCatalog: condition === 'EQUALS', noMatchPolicy: 'PRESERVE_PARENT' });
  return { condition: selected.condition, value: selected.value, choice, targetValue };
};

const beginEdit = async (stepId) => {
  const startedAt = Date.now();
  await click(browser.cdp, `[data-testid="construction-history-step-${stepId}"]`);
  await click(browser.cdp, `[data-testid="construction-edit-step-${stepId}"]`);
  const policy = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(policy+':not(:disabled)')})`);
  await revealControl(policy);
  recordAction('edit-saved-step-to-policy-control', startedAt);
};

const applyProposal = async (expectedRows, name) => {
  const startedAt = Date.now();
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: expectedRows.length });
  builder = await api(base + '/builder');
};

try {
  await api(root, { name: explorer, title: 'Repeated Contributor code lifecycle QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, generation);
  const oracle = await sourceWitnesses(builder.catalog);
  const witnesses = oracle.witnesses;
  report.oracle = {
    project,
    generation,
    relationship: 'Observation --subject_Patient--> Patient',
    boundedManyCandidateCount: oracle.candidateCount,
    candidatePatientLimit,
    relatedObservationLimit,
    observationEdgeLimit,
    categoryLimit,
    codingPerCategoryLimit,
    manyWitnessPatientLimit,
    manyCandidateLimit,
    sourceCandidateId: oracle.sourceCandidateId,
    boundedObservationCount: oracle.observationCount,
    nestedCodeCounts: oracle.nestedCodeCounts,
    nestedCodeDetails: oracle.nestedCodeDetails,
    literalCodeMatching: true,
    codingSystemCorrelation: false,
    witnessAvailability: report.oracleWitnessAvailability,
    witnesses,
    patientCountByBucket: Object.fromEntries([...new Set(witnesses.map((item) => item.bucket))]
      .map((bucket) => [bucket, witnesses.filter((item) => item.bucket === bucket).length])),
    observationCountByBucket: Object.fromEntries([...new Set(witnesses.map((item) => item.bucket))]
      .map((bucket) => [bucket, witnesses.filter((item) => item.bucket === bucket)
        .reduce((count, item) => count + item.observations.length, 0)])),
  };
  const baselineRows = witnesses.map((item) => [item.patient.id]);

  const patientNode = builder.catalog.nodes.find((node) => node.resourceType === 'Patient');
  assert(patientNode, 'Patient source type is absent from the current catalog');
  await command([{ type: 'CREATE_TABLE', title: 'Contributor code QA', rootNodeId: patientNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const patientIDField = builder.catalog.candidates.find((candidate) => candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'id');
  assert(patientIDField, 'Patient ID is absent from the current catalog');
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: patientIDField.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Patient ID' }]);
  const selection = await api(base.replace('/authoring/v2', '/selections'), {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: witnesses.map(({ patient }) => ({
      project, generation, resourceType: 'Patient', id: patient.id,
    })) } },
  });
  assert.equal(selection.memberCount, witnesses.length,
    'Selection must contain each independently witnessed source row');
  const routes = await api(base + '/population-routes', {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
  });
  const direct = routes.choices.find((choice) => choice.route.length === 0);
  assert(direct, 'Selected Patient resources have no direct population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  const original = structuredClone(documentForOutput(builder));
  assert.equal(original.population.selectionRevisionId, selection.id);

  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.exceptionThrown', (event) => report.errors.push({ kind: 'runtime', details: event.exceptionDetails }));
  browser.cdp.on('Runtime.consoleAPICalled', (event) => {
    if (event.type === 'error') report.errors.push({ kind: 'console', args: event.args });
  });
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request }) => {
    const url = new URL(request.url);
    if (!browserRequestOrigins.has(url.origin)) return;
    if (url.pathname.startsWith(`${root}/${protectedExplorer}/`)) {
      report.protectedExplorerUntouched = false;
      report.errors.push({ kind: 'protected-explorer-request', path: url.pathname });
      return;
    }
    if (url.pathname === root || url.pathname.startsWith(`${root}/`)) {
      report.browserExplorerRequests.push({ path: url.pathname });
      if (url.pathname !== `${root}/selections` && url.pathname !== root
          && !url.pathname.startsWith(`${root}/${explorer}/`)) {
        report.errors.push({ kind: 'foreign-explorer-request', path: url.pathname });
        return;
      }
    }
    if (!url.pathname.startsWith(`${root}/${explorer}/`)) return;
    if (url.pathname !== `${base}/related-expand-contributors`) return;
    let body;
    try { body = request.postData ? JSON.parse(request.postData) : undefined; } catch { body = request.postData; }
    const nativeRequest = { path: url.pathname, body, startedAt: Date.now() };
    contributorSearchRequests.push(nativeRequest);
    networkRequests.set(requestId, nativeRequest);
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const nativeRequest = networkRequests.get(requestId);
    if (nativeRequest) {
      nativeRequest.completedAt = Date.now();
      failedResponses.push(browser.cdp.send('Network.getResponseBody', { requestId })
        .then((response) => {
          const responseBody = response.base64Encoded ? Buffer.from(response.body, 'base64').toString('utf8') : response.body;
          try { nativeRequest.response = JSON.parse(responseBody); }
          catch { nativeRequest.responseText = responseBody; }
        }).catch((error) => { nativeRequest.responseCaptureError = String(error); }));
    }
  });
  browser.cdp.on('Network.responseReceived', ({ response, requestId }) => {
    const nativeRequest = networkRequests.get(requestId);
    if (nativeRequest) nativeRequest.status = response.status;
    if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) {
      const failure = { kind: 'http', url: response.url, status: response.status,
        requestPath: nativeRequest?.path, observedAfter: report.cases.at(-1)?.name };
      report.errors.push(failure);
    }
  });
  browser.cdp.on('Network.loadingFailed', (event) => {
    if (event.type === 'Script' && event.errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', error: event.errorText });
  });

  await open(baselineRows, 'source-selection-bounded-witness-buckets');
  let panel = await startRelatedExpand();
  let startedAt = Date.now();
  const existsChoice = await chooseRepeatedContributorCondition(panel, oracle,
    'configure-repeated-nested-code-exists', 'EXISTS');
  const existsPreserveRows = rowsForRepeatedCodeExists(witnesses, true);
  const existsExcludedRows = rowsForRepeatedCodeExists(witnesses, false);
  report.existsRule = { condition: 'EXISTS', quantifier: 'ANY', sourceField: nestedCodePath,
    logicalType: existsChoice.choice.source.logicalType,
    matchingObservationCount: witnesses.flatMap((witness) => witness.observations)
      .filter((observation) => observation.hasCodeValue).length,
    preservedNoMatchRows: existsPreserveRows.length - existsExcludedRows.length };
  startedAt = Date.now();
  await proposal('repeated-nested-code-exists-preview', startedAt, existsPreserveRows);
  const beforeExistsCancel = await api(base + '/builder');
  startedAt = Date.now();
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await rendered(baselineRows, 'cancel-repeated-nested-code-exists-preview');
  recordAction('cancel-repeated-nested-code-exists-preview', startedAt, { rowCount: baselineRows.length });
  builder = await api(base + '/builder');
  assert.deepEqual(builder.workspace, beforeExistsCancel.workspace,
    'Cancel must leave the workspace unchanged after the repeated EXISTS preview');
  assert.deepEqual(documentForOutput(builder), original,
    'Cancel must preserve the source table after the repeated EXISTS preview');
  report.cases.push({ name: 'repeated-exists-cancel-preserves-source', workspaceUnchanged: true });

  panel = await startRelatedExpand();
  const cancelledChoice = await chooseRepeatedContributorCondition(panel, oracle,
    'configure-nested-code-contributor-controls', 'EQUALS');
  const cancelledPreserveRows = rowsForNestedCode(witnesses, cancelledChoice.targetValue, true);
  const cancelledExcludedRows = rowsForNestedCode(witnesses, cancelledChoice.targetValue, false);
  report.oracle.equalityPreservedNoMatchRows = cancelledPreserveRows.length - cancelledExcludedRows.length;
  if (cancelledPreserveRows.length === cancelledExcludedRows.length) {
    report.controls.uncovered.push('No source Patient in the bounded selected witness set had zero related Observations matching the selected code, so PRESERVE_PARENT versus EXCLUDE is not distinguished by this fixture.');
  }
  startedAt = Date.now();
  await proposal('contributor-nested-code-equals-preview', startedAt, cancelledPreserveRows);
  const beforeCancel = await api(base + '/builder');
  startedAt = Date.now();
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await rendered(baselineRows, 'cancel-contributor-preview');
  recordAction('cancel-contributor-preview', startedAt, { rowCount: baselineRows.length });
  builder = await api(base + '/builder');
  assert.deepEqual(builder.workspace, beforeCancel.workspace, 'Cancel must leave the saved workspace unchanged');
  assert.deepEqual(documentForOutput(builder), original, 'Cancel must preserve the exact source table');
  report.cases.push({ name: 'contributor-rule-cancel-preserves-source', workspaceUnchanged: true });

  panel = await startRelatedExpand();
  const selectedChoice = await chooseRepeatedContributorCondition(panel, oracle,
    'reconfigure-nested-code-after-cancel', 'EQUALS');
  assert.equal(selectedChoice.targetValue, cancelledChoice.targetValue,
    'The same native catalog suggestion must be selected after Cancel');
  assert.equal(selectedChoice.choice.choiceId, cancelledChoice.choice.choiceId,
    'The same signed field choice must be selected after Cancel');
  assert.deepEqual(selectedChoice.choice.source, cancelledChoice.choice.source,
    'The same signed source field must be selected after Cancel');
  const preserveRows = rowsForNestedCode(witnesses, selectedChoice.targetValue, true);
  const excludedRows = rowsForNestedCode(witnesses, selectedChoice.targetValue, false);
  report.oracle.selectedCatalogValue = selectedChoice.targetValue;
  report.oracle.matchedNestedCodeCountInManyWitness = oracle.nestedCodeCounts[selectedChoice.targetValue];
  assert.equal(preserveRows.length - excludedRows.length, report.oracle.equalityPreservedNoMatchRows,
    'Cancel and reconfiguration must retain the same independently witnessed no-match rows');
  startedAt = Date.now();
  await proposal('confirmed-contributor-nested-code-equals-preview', startedAt, preserveRows);
  await applyProposal(preserveRows, 'apply-contributor-nested-code-rule-to-render');
  let relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
  assert(relatedStep, 'Applied contributor rule is missing from saved construction');
  const sourceProjection = relatedStep.outputs.find((column) => column.name === original.columns[0].column);
  assert(sourceProjection?.id, 'Applied related-expand step omitted the compiler-owned source projection identity');
  const canonicalSourceColumnId = sourceProjection.id;
  assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'initial contributor Apply');
  report.sourceColumnIdentity = {
    sourceColumnName: sourceProjection.name,
    compilerCanonicalSourceColumnId: canonicalSourceColumnId,
  };
  assertPersistedEqualsRule(codeRule(relatedStep), selectedChoice.choice, selectedChoice.targetValue, 'PRESERVE_PARENT',
    'initial Contributor category[].coding[].code Apply');
  report.persistedNestedCodeCondition = {
    operator: 'EQUALS', valueKind: typedEqualsValue(selectedChoice.choice, selectedChoice.targetValue).kind,
    sourceLogicalType: selectedChoice.choice.source.logicalType, selectedCatalogValue: selectedChoice.targetValue,
    source: report.contributorChoice, signedSourcePersisted: true, selectedChoicePersisted: true,
    noMatchPolicy: 'PRESERVE_PARENT',
  };
  await open(preserveRows, 'reload-contributor-nested-code-equals');

  const beforeEdit = await api(base + '/builder');
  assertStableSourceProjection(beforeEdit, original, canonicalSourceColumnId, 'reload after initial contributor Apply');
  relatedStep = documentForOutput(beforeEdit).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
  assertPersistedEqualsRule(codeRule(relatedStep), selectedChoice.choice, selectedChoice.targetValue, 'PRESERVE_PARENT',
    'reload after initial Contributor category[].coding[].code Apply');
  await beginEdit(relatedStep.id);
  const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
  assert.equal(await browserEval(browser.cdp, `return document.querySelector(${JSON.stringify(policySelector)})?.value;`), 'PRESERVE_PARENT');
  startedAt = Date.now();
  await selectOption(browser.cdp, policySelector, 'EXCLUDE');
  await proposal('edit-policy-exclude-preview', startedAt, excludedRows);
  await applyProposal(excludedRows, 'apply-exclude-policy-to-render');
  relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
  assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'edited EXCLUDE policy Apply');
  assertPersistedEqualsRule(codeRule(relatedStep), selectedChoice.choice, selectedChoice.targetValue, 'EXCLUDE',
    'edited EXCLUDE policy Apply');
  report.persistedNestedCodeCondition.policyEditKeptCondition = true;
  report.persistedNestedCodeCondition.noMatchPolicyAfterEdit = 'EXCLUDE';
  await open(excludedRows, 'reload-edited-exclude-policy');

  const beforeRemoval = await api(base + '/builder');
  assertStableSourceProjection(beforeRemoval, original, canonicalSourceColumnId, 'reload after edited EXCLUDE policy');
  relatedStep = documentForOutput(beforeRemoval).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
  assertPersistedEqualsRule(codeRule(relatedStep), selectedChoice.choice, selectedChoice.targetValue, 'EXCLUDE',
    'reload after edited EXCLUDE policy');
  const remove = async () => {
    await click(browser.cdp, `[data-testid="construction-history-step-${relatedStep.id}"]`);
    startedAt = Date.now();
    await click(browser.cdp, `[data-testid="construction-remove-step-${relatedStep.id}"]`);
    await proposal('remove-contributor-rule-preview', startedAt, baselineRows);
  };
  await remove();
  startedAt = Date.now();
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`);
  await rendered(excludedRows, 'cancel-remove-to-render');
  recordAction('cancel-remove-to-render', startedAt, { rowCount: excludedRows.length });
  builder = await api(base + '/builder');
  assert.deepEqual(builder.workspace, beforeRemoval.workspace, 'Cancel removal must preserve the authored Contributor rule');
  assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'cancel remove');
  report.cases.push({ name: 'remove-cancel-preserves-contributor-rule', workspaceUnchanged: true });
  await remove();
  await applyProposal(baselineRows, 'apply-remove-to-render');
  const restored = documentForOutput(builder);
  assert.deepEqual(restored.construction?.steps ?? [], original.construction?.steps ?? [],
    'Removing Contributor rules must restore the exact authored source construction');
  assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'remove Apply');
  assert.deepEqual(restored.rows, original.rows, 'Removing Contributor rules must restore the exact row definition');
  assert.deepEqual(restored.population, original.population, 'Removing Contributor rules must restore the exact selected population');
  await open(baselineRows, 'reload-restored-source-table');
  builder = await api(base + '/builder');
  assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'reload after Contributor removal');
  await Promise.all(failedResponses);
  assert(report.protectedExplorerUntouched, `A request unexpectedly targeted protected Explorer ${protectedExplorer}`);
  assert(report.browserExplorerRequests.every(({ path }) => path === root || path === `${root}/selections`
    || path.startsWith(`${root}/${explorer}/`)), 'Browser requests must remain within the fresh owned Explorer and global selection route');
  assert.deepEqual(report.errors, [], 'Unexpected browser errors were reported');
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  report.failureUI = browser ? await browserEval(browser.cdp,
    `const body=document.body.innerText;return {
      body:body.slice(0,6000),bodyTail:body.slice(-12000),
      alerts:[...document.querySelectorAll('[role="alert"]')].map(item=>item.innerText),
      selects:[...document.querySelectorAll('select')].map(select=>({
        label:select.getAttribute('aria-label'),value:select.value,disabled:select.disabled,
        options:[...select.options].map(option=>({label:option.textContent.trim(),value:option.value,disabled:option.disabled}))
      })),
      relatedRoutes:[...document.querySelectorAll('input[name^="related-expand-route-"]')].map(input=>({
        label:input.getAttribute('aria-label'),checked:input.checked,disabled:input.disabled,
        visible:!input.closest('details:not([open])')
      }))};`).catch(String) : undefined;
  process.exitCode = 1;
} finally {
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      ...(await sourceFreeze.assertUnchanged()),
      finishedAt: sourceFreezeFinishedAt,
    };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      error: String(error),
      finishedAt: sourceFreezeFinishedAt,
    };
    process.exitCode = 1;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map((item) => item.name), error: report.error }));
