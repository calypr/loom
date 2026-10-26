import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { isDeepStrictEqual } from 'node:util';
import { canonicalProjectID, expectedTrainingArtifactMembers } from '../../loom-dev.mjs';
import { browserURL } from './builder-url.mjs';
import { fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';
import {
  inspectDatasetArtifactZip,
} from '../helpers/viewer-output-dataset-zip-oracle.mjs';
import { readFilteredPatientDatasetZipOracle } from '../helpers/viewer-output-filtered-dataset-zip-oracle.mjs';

const outputId = 'patients_filtered_zip';
const outputColumns = [
  { column: 'patient_id', label: 'Patient ID', path: 'id', order: 0 },
  { column: 'gender', label: 'Gender', path: 'gender', order: 1 },
  { column: 'name_family', label: 'Name family', path: 'name[].family', order: 2 },
];
const runtimePath = (project, explorer) =>
  `${fixtureProjectExplorerCollectionPath(project)}/${encodeURIComponent(explorer)}`;
const repositoryConfigPath = (project, generation) =>
  `/api/v1/projects/${encodeURIComponent(project)}/generations/${encodeURIComponent(generation)}/explorer-config`;
const graphPath = '/graphql/graph';

const datasetWorkspace = () => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderWorkspace',
  semanticsVersion: 10,
  explorer: { title: 'Filtered complete Patient dataset ZIP' },
  documents: [{
    kind: 'ExplorerBuilderDocument',
    output: { id: outputId, title: 'Patients', rowLabel: 'Patient' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [
      {
        columnId: 'patient-id', column: 'patient_id', label: 'Patient ID', logicalType: 'string', occurrenceId: 'base',
        source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
        table: { visible: true, order: 0 },
      },
      {
        columnId: 'patient-gender', column: 'gender', label: 'Gender', logicalType: 'string', occurrenceId: 'base',
        source: { kind: 'field', field: { path: 'gender', projectionMode: 'VALUE' } },
        table: { visible: true, order: 1 }, filter: { label: 'Gender', order: 0 },
      },
      {
        columnId: 'patient-name-family', column: 'name_family', label: 'Name family', logicalType: 'string', occurrenceId: 'base',
        source: { kind: 'field', field: { path: 'name[].family', projectionMode: 'ALL' } },
        table: { visible: true, order: 2 },
      },
    ],
  }],
  tabs: [{ id: 'patients-filtered-zip-tab', title: 'Patients', outputId, order: 0, visible: true }],
});

const requestBody = request => {
  try { return request.postDataJSON(); }
  catch { return null; }
};

const sameSelector = (left, right) => Boolean(left && right &&
  left.recipe === right.recipe && left.translationVersion === right.translationVersion && left.output === right.output);

const outputResponseMatches = (response, { origin, project, selector, filtered }) => {
  try {
    const request = response.request();
    const url = new URL(response.url());
    const input = requestBody(request)?.variables?.input;
    if (request.method() !== 'POST' || url.origin !== origin || url.pathname !== graphPath ||
      input?.projectId !== project || !sameSelector(input.selector, selector) ||
      !isDeepStrictEqual(input.columns, outputColumns.map(column => column.column))) return false;
    const filters = Array.isArray(input.filters) ? input.filters : [];
    if (!filtered) return filters.length === 0;
    return filters.length === 1 && filters[0].column === 'gender' &&
      filters[0].op === 'EQ' && filters[0].value === 'female';
  } catch {
    return false;
  }
};

const rowsFromOutputResponse = async (response, label) => {
  assert(response, `${label} must have a matching native Viewer query response`);
  assert(response.status() >= 200 && response.status() < 300,
    `${label} GraphQL query returned HTTP ${response.status()}`);
  const payload = await response.json();
  assert(!Array.isArray(payload.errors) || payload.errors.length === 0,
    `${label} GraphQL response must not contain errors`);
  const connection = payload.data?.dataframeRows;
  assert(connection && Array.isArray(connection.columns) && Array.isArray(connection.rows),
    `${label} GraphQL response must include dataframe columns and rows`);
  const columns = connection.columns.map(column => typeof column === 'string' ? column : column.column);
  assert.deepEqual(columns, outputColumns.map(column => column.column),
    `${label} query must retain the exact three visible output columns`);
  const rows = connection.rows.map(row => {
    if (Array.isArray(row)) return Object.fromEntries(columns.map((column, index) => [column, row[index]]));
    if (row && typeof row === 'object' && Array.isArray(row.values)) {
      return Object.fromEntries(columns.map((column, index) => [column, row.values[index]]));
    }
    return Object.fromEntries(columns.map(column => [column, row?.[column]]));
  }).map(row => ({
    patient_id: row.patient_id,
    gender: row.gender,
    name_family: row.name_family,
  })).sort((left, right) => String(left.patient_id).localeCompare(String(right.patient_id)));
  return { rows, payload, columns };
};

const waitForVisiblePatientIDs = (page, expectedIDs) => page.waitForFunction(ids => {
  const tables = [...document.querySelectorAll('table[aria-label$=" results"]')];
  if (tables.length !== 1) return false;
  const rows = [...tables[0].querySelectorAll('tbody tr')].map(row => row.innerText);
  const actual = rows.map(text => ids.find(id => text.includes(id)) ?? null).sort();
  return actual.length === ids.length && actual.every((id, index) => id === [...ids].sort()[index]);
}, expectedIDs, { timeout: 5_000 });

const waitForVisibleFemalePatient = (page, expectedID) => page.waitForFunction(id => {
  const tables = [...document.querySelectorAll('table[aria-label$=" results"]')];
  if (tables.length !== 1) return false;
  const rows = [...tables[0].querySelectorAll('tbody tr')];
  return rows.length === 1 && rows[0].innerText.includes(id);
}, expectedID, { timeout: 5_000 });

const readModal = dialog => dialog.evaluate(element => {
  const value = label => [...element.querySelectorAll('dt')]
    .find(term => term.textContent.trim() === label)?.nextElementSibling?.textContent.trim() ?? '';
  return {
    text: element.innerText,
    representation: value('Representation'),
    valuePreservation: value('Value preservation'),
    sourceGeneration: value('Source generation'),
    schemaDigest: value('Schema digest'),
    types: element.querySelector('[aria-label="Declared output types"]')?.innerText ?? '',
  };
});

const modalMatches = (modal, oracle, generation) =>
  modal.text.includes(`${oracle.rows.length} rows from the complete authorized population`) &&
  modal.text.includes('Each row represents Patient.') &&
  modal.sourceGeneration === generation && /^[a-f0-9]{64}$/i.test(modal.schemaDigest) &&
  modal.text.includes('3 declared output columns') &&
  ['Patient ID', 'Gender', 'Name family', 'repeated'].every(value => modal.types.includes(value)) &&
  modal.representation === 'Typed JSON Lines in a ZIP archive' &&
  modal.valuePreservation === 'Structured and repeated values retain native JSON types, row identity, nulls, and missing values.';

const checkModal = (check, modal, oracle, generation) => {
  const valid = modalMatches(modal, oracle, generation);
  check('correctness', 'download modal declares complete authorized two-Patient scope and exact typed schema',
    valid, { rows: oracle.rows.length, generation: modal.sourceGeneration, schemaDigest: modal.schemaDigest,
      types: modal.types, representation: modal.representation, valuePreservation: modal.valuePreservation,
      modalText: modal.text });
  return valid;
};

const openDownloadModal = async ({ page, action, check, oracle, generation, phase }) => {
  const dialog = page.getByRole('dialog').filter({ hasText: 'Download dataset' });
  const button = page.getByRole('button', { name: 'Download dataset', exact: true });
  await action(`open filtered dataset ZIP modal ${phase}`, button, locator => locator.click({ timeout: 5_000 }), {
    timeout: 5_000, budget: 5_000,
    requiredCheck: `${phase} filtered dataset ZIP modal opens within five seconds`,
    after: () => dialog.getByRole('link', { name: 'Download ZIP', exact: true })
      .waitFor({ state: 'visible', timeout: 5_000 }),
  });
  const modal = await readModal(dialog);
  const validModal = modalMatches(modal, oracle, generation);
  if (phase === 'after-reload') checkModal(check, modal, oracle, generation);
  else check('correctness', `${phase} female-filtered modal retains complete population and repeated schema`,
    validModal, { modalText: modal.text, generation: modal.sourceGeneration, schemaDigest: modal.schemaDigest, types: modal.types });
  const link = dialog.getByRole('link', { name: 'Download ZIP', exact: true });
  const href = await link.getAttribute('href');
  const downloadName = await link.getAttribute('download');
  assert.equal(typeof href, 'string', `${phase} ZIP modal must expose the native Download ZIP link`);
  return { dialog, modal, link, href, downloadName };
};

const downloadAndInspect = async ({ page, action, check, target, testInfo, oracle, publication, opened, networkObserver, filteredViewerRows }) => {
  const downloadURL = new URL(opened.href, target.uiUrl);
  const expectedPath = new RegExp(`^${runtimePath(target.fixtureProject, 'default')}/authoring/v2/artifacts/artifact_[0-9a-f]{64}$`);
  check('correctness', 'final Download ZIP link targets the exact native artifact endpoint',
    downloadURL.origin === new URL(target.uiUrl).origin && expectedPath.test(downloadURL.pathname) &&
      downloadURL.search === '' && downloadURL.hash === '' && opened.downloadName === 'loom-dataset-artifact-v2.zip',
    { origin: downloadURL.origin, path: downloadURL.pathname, search: downloadURL.search,
      hash: downloadURL.hash, downloadName: opened.downloadName });
  const artifactID = decodeURIComponent(downloadURL.pathname.split('/').at(-1));
  const artifactRequestStart = networkObserver.requests.length;
  // The Viewer prepares the artifact when the modal opens; a response captured before this helper
  // starts must still be considered, so also consult the already-owned request ledger.
  const priorPreparation = networkObserver.requests.slice(0, artifactRequestStart).find(entry =>
    entry.method === 'POST' && entry.path === `${runtimePath(target.fixtureProject, 'default')}/authoring/v2/artifacts` &&
    entry.response?.id === artifactID,
  );
  const preparation = priorPreparation ?? await networkObserver.waitForResponse(entry =>
    entry.method === 'POST' && entry.path === `${runtimePath(target.fixtureProject, 'default')}/authoring/v2/artifacts` &&
    entry.response?.id === artifactID,
  { fromIndex: artifactRequestStart, timeoutMs: 5_000 });
  const descriptor = preparation.response;
  const expectedIdentity = {
    project: publication.project,
    datasetGeneration: publication.generation,
    outputId: publication.outputId,
    revisionId: publication.revisionId,
    schemaDigest: opened.modal.schemaDigest,
  };
  const descriptorMatches = preparation.status === 200 && preparation.networkComplete &&
    preparation.responseCaptureComplete && descriptor?.state === 'COMPLETE' && descriptor.id === artifactID &&
    descriptor.project === expectedIdentity.project && descriptor.explorerId === 'default' &&
    descriptor.datasetGeneration === expectedIdentity.datasetGeneration && descriptor.outputId === expectedIdentity.outputId &&
    descriptor.revisionId === expectedIdentity.revisionId && descriptor.schemaDigest === expectedIdentity.schemaDigest &&
    descriptor.rows === oracle.rows.length && descriptor.features === outputColumns.length &&
    descriptor.mediaType === 'application/zip' && descriptor.filename === opened.downloadName &&
    Number.isSafeInteger(descriptor.bytes) && descriptor.bytes > 0 && /^[a-f0-9]{64}$/i.test(descriptor.archiveSha256 ?? '');
  check('correctness', 'ZIP preparation descriptor binds current publication and complete two-row scope',
    descriptorMatches, { status: preparation.status, artifactID, descriptor, expectedIdentity });

  const downloadPromise = page.waitForEvent('download', { timeout: 5_000 });
  const archivePath = testInfo.outputPath('filtered-complete-patient-dataset.zip');
  let download;
  await action('download complete filtered-scope Patient ZIP', opened.link, locator => locator.click({ timeout: 5_000 }), {
    timeout: 5_000, budget: 5_000,
    requiredCheck: 'filtered ZIP download completes within five seconds',
    after: async () => {
      download = await downloadPromise;
      await download.saveAs(archivePath);
    },
  });
  await testInfo.attach('filtered-complete-patient-dataset.zip', { path: archivePath, contentType: 'application/zip' });
  const archiveBytes = await readFile(archivePath);
  const archiveSHA256 = createHash('sha256').update(archiveBytes).digest('hex');
  const downloadFailure = await download.failure();
  const bytesMatch = downloadFailure === null && download.url() === downloadURL.href &&
    download.suggestedFilename() === opened.downloadName && archiveBytes.byteLength === descriptor.bytes &&
    archiveSHA256 === descriptor.archiveSha256;
  const inspected = inspectDatasetArtifactZip(archivePath, {
    expectedIdentity,
    expectedExplorerId: 'default',
    expectedRows: oracle.rows,
    expectedArchiveSHA256: descriptor.archiveSha256,
  });
  const expectedMembers = expectedTrainingArtifactMembers(inspected.artifact.manifest.format).sort();
  const actualMembers = [...inspected.memberNames].sort();
  check('correctness', 'archive bytes and members match the complete preparation descriptor',
    bytesMatch && isDeepStrictEqual(actualMembers, expectedMembers),
    { downloadFailure, archiveBytes: archiveBytes.byteLength, expectedBytes: descriptor.bytes,
      archiveSHA256, expectedSHA256: descriptor.archiveSha256, expectedMembers, actualMembers,
      memberChecksums: inspected.artifact.manifest.members });

  const { artifact } = inspected;
  const expectedSchema = outputColumns.map(({ column, label }) => ({ name: column, outputKey: column, label }));
  const actualSchema = artifact.schema.columns.map(({ name, outputKey, label }) => ({ name, outputKey, label }));
  const family = artifact.schema.columns.find(column => column.outputKey === 'name_family');
  const schemaValid = artifact.manifest.format === 'JSONL' && artifact.manifest.rows === 2 && artifact.manifest.features === 3 &&
    isDeepStrictEqual(actualSchema, expectedSchema) && family?.logicalType === 'string' &&
    family.shape === 'array' && family.repeated === true &&
    family.sourceResourceType === 'Patient' && String(family.sourcePath).replace(/^root\./, '') === 'name[].family' &&
    typeof family.candidateId === 'string' && family.candidateId.length > 0 &&
    typeof family.occurrenceId === 'string' && family.occurrenceId.length > 0;
  check('correctness', 'ZIP schema preserves Patient.name[].family repeated-field lineage',
    schemaValid, { format: artifact.manifest.format, rows: artifact.manifest.rows, features: artifact.manifest.features,
      expectedSchema, actualSchema, family });
  const rowIdentity = artifact.manifest.descriptor?.rowIdentity;
  const archivePatientIDs = artifact.rows.map(row => row.values?.[rowIdentity?.sourceIdColumn]).sort();
  check('correctness', 'ZIP row identity declares and preserves the exact Patient source IDs',
    rowIdentity?.sourceResourceType === 'Patient' && rowIdentity.sourceIdColumn === 'patient_id' &&
      isDeepStrictEqual(archivePatientIDs, [...oracle.patientIDs].sort()),
    { rowIdentity, expectedPatientIDs: oracle.patientIDs, archivePatientIDs,
      opaqueRowIDs: artifact.rows.map(row => row.rowId) });
  const actualRows = artifact.rows.map(row => row.values)
    .sort((left, right) => String(left.patient_id).localeCompare(String(right.patient_id)));
  check('correctness', 'ZIP data preserves both exact raw Patient rows, missing gender, and repeated family values while the Viewer is filtered',
    isDeepStrictEqual(actualRows, oracle.rows) && filteredViewerRows.length === 1 &&
      filteredViewerRows[0].includes(oracle.femalePatientIDs[0]),
    { format: artifact.manifest.format, expectedRows: oracle.rows, actualRows, filteredViewerRows });

  return { archivePath, archiveSHA256, descriptor, artifact };
};

export const viewerOutputFilteredDatasetZipWorkflow = async ({ page, report, action, check, networkObserver }, context, testInfo) => {
  const target = context.target;
  const project = target.fixtureProject;
  const canonicalProject = canonicalProjectID(project);
  const generation = target.fixtureGeneration;
  const explorer = 'default';
  const oracle = await readFilteredPatientDatasetZipOracle(target.fixtureDir, { project: canonicalProject, generation });
  const sourceHashBefore = oracle.patientSHA256;
  report.target.project = project;
  report.target.canonicalProject = canonicalProject;
  report.target.explorer = explorer;
  report.target.filteredDatasetZipOracle = {
    path: oracle.patientPath,
    sha256: oracle.patientSHA256,
    patientIDs: oracle.patientIDs,
    femalePatientIDs: oracle.femalePatientIDs,
    rows: oracle.rows,
    outputId,
  };

  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: target.sourceRoot, encoding: 'utf8' }).trim();
  const configPath = repositoryConfigPath(project, generation);
  const seedResponse = await fetch(`${target.apiUrl}${configPath}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Loom-Source-Commit': sourceCommit },
    body: JSON.stringify(datasetWorkspace()),
    signal: AbortSignal.timeout(30_000),
  });
  const seedText = await seedResponse.text();
  let seedBody;
  try { seedBody = JSON.parse(seedText); }
  catch { seedBody = { body: seedText.slice(0, 1_000) }; }
  const seedPassed = seedResponse.ok && seedBody.project === canonicalProject && seedBody.generation === generation &&
    seedBody.explorerId === explorer && seedBody.activated === true;
  check('correctness', 'repository config activates the exact filtered ZIP publication',
    seedPassed && ['receiptId', 'revisionId'].every(key => typeof seedBody[key] === 'string' && seedBody[key].length > 0),
    { status: seedResponse.status, path: configPath, project, canonicalProject, generation, explorer,
      outputId, response: seedBody, sourceCommit });
  report.target.repositoryConfigSeed = {
    project, canonicalProject, generation, explorer, sourceCommit,
    receiptId: seedBody.receiptId, revisionId: seedBody.revisionId,
  };

  const origin = new URL(target.uiUrl).origin;
  const runtimeStart = networkObserver.requests.length;
  const runtimePromise = networkObserver.waitForResponse(entry => entry.method === 'GET' && entry.path === runtimePath(project, explorer),
    { fromIndex: runtimeStart, timeoutMs: 5_000 });
  const initialRowsPromise = page.waitForResponse(response => {
    try {
      const request = response.request();
      const url = new URL(response.url());
      const input = requestBody(request)?.variables?.input;
      return request.method() === 'POST' && url.origin === origin && url.pathname === graphPath &&
        input?.projectId === canonicalProject && input?.selector?.output === outputId &&
        isDeepStrictEqual(input.columns, outputColumns.map(column => column.column)) &&
        (!Array.isArray(input.filters) || input.filters.length === 0);
    } catch { return false; }
  }, { timeout: 5_000 });
  await page.goto(browserURL(target, project, explorer, 'viewer'), { waitUntil: 'domcontentloaded' });
  const [runtimeEntry, initialResponse] = await Promise.all([runtimePromise, initialRowsPromise]);
  const runtimeEnvelope = runtimeEntry.response;
  const runtime = runtimeEnvelope?.runtime;
  const output = runtime?.outputs?.find(candidate => candidate.outputId === outputId);
  const publishedGeneration = runtime?.generation ?? runtime?.publication?.generation;
  const revisionId = runtime?.publication?.revisionId;
  const publication = { project: runtimeEnvelope?.project, generation: publishedGeneration, revisionId, outputId: output?.outputId };
  const runtimeColumns = output?.columns ?? [];
  const genderBinding = output?.filters?.find(binding => binding.column === 'gender' && binding.label === 'Gender');
  const runtimeMatches = runtimeEntry.status >= 200 && runtimeEntry.status < 300 && runtimeEnvelope?.project === canonicalProject &&
    runtimeEnvelope?.explorerId === explorer && publishedGeneration === generation && typeof revisionId === 'string' &&
    revisionId.length > 0 && output?.outputId === outputId &&
    isDeepStrictEqual(runtimeColumns.map(column => ({ column: column.column, label: column.label })),
      outputColumns.map(({ column, label }) => ({ column, label }))) &&
    runtimeColumns[0]?.logicalType === 'string' && runtimeColumns[1]?.logicalType === 'string' &&
    runtimeColumns[2]?.logicalType === 'string' && runtimeColumns[2]?.repeated === true &&
    Boolean(genderBinding) && seedBody.revisionId === revisionId;
  check('correctness', 'filtered ZIP output binds exact fixture project, generation, revision, and three columns',
    seedPassed && runtimeMatches, { status: runtimeEntry.status, publication, expectedProject: canonicalProject,
      expectedGeneration: generation, expectedOutputId: outputId, columns: runtimeColumns, genderBinding });
  assert(output, 'published runtime must expose the seeded three-column Patient output');
  assert(genderBinding, 'published runtime must expose the exact native Gender facet binding');
  report.target.datasetPublication = publication;
  report.target.viewerOutputId = outputId;

  const outputTable = page.locator('table[aria-label$=" results"]');
  await outputTable.waitFor({ state: 'visible', timeout: 5_000 });
  await waitForVisiblePatientIDs(page, oracle.patientIDs);
  const initialQuery = await rowsFromOutputResponse(initialResponse, 'initial unfiltered Viewer query');
  const missingGenderID = oracle.missingGenderPatientIDs[0];
  const missingGenderProjection = initialQuery.rows.find(row => row.patient_id === missingGenderID);
  const nullableProjectionMatches = Object.hasOwn(missingGenderProjection ?? {}, 'gender') &&
    missingGenderProjection.gender === null;
  const unfilteredRowsMatch = isDeepStrictEqual(initialQuery.rows, oracle.rows) && nullableProjectionMatches;
  const initialDOMRows = await outputTable.locator('tbody tr').allInnerTexts();
  check('correctness', 'unfiltered Viewer rows match the independent Patient oracle',
    unfilteredRowsMatch && oracle.patientIDs.every(id => initialDOMRows.some(row => row.includes(id))),
    { expectedRows: oracle.rows, responseRows: initialQuery.rows, visibleRows: initialDOMRows,
      rawMissingGenderPatientIDs: oracle.missingGenderPatientIDs,
      nullableProjectionMatches,
      requestProject: requestBody(initialResponse.request())?.variables?.input?.projectId,
      selector: requestBody(initialResponse.request())?.variables?.input?.selector });

  const loadValues = page.getByRole('button', { name: 'Load values', exact: true });
  await action('load native Gender facet values', loadValues, locator => locator.click({ timeout: 5_000 }), {
    timeout: 5_000, budget: 5_000, after: () => page.getByRole('checkbox', { name: /^female(?:\s|$)/ }).waitFor({ state: 'visible', timeout: 5_000 }),
  });
  const female = page.getByRole('checkbox', { name: /^female(?:\s|$)/ });
  const applyFemaleFilter = async phase => {
    const responsePromise = page.waitForResponse(response => outputResponseMatches(response, {
      origin, project: canonicalProject, selector: output.selector, filtered: true,
    }), { timeout: 5_000 });
    let response;
    await action(`apply native female facet ${phase}`, female, locator => locator.check({ timeout: 5_000 }), {
      timeout: 5_000,
      budget: 5_000,
      requiredCheck: `${phase} female facet apply and render within five seconds`,
      after: async () => {
        [response] = await Promise.all([responsePromise, waitForVisibleFemalePatient(page, oracle.femalePatientIDs[0])]);
      },
    });
    const actualRows = await rowsFromOutputResponse(response, `${phase} female-filtered Viewer query`);
    return { response, rows: actualRows.rows, input: requestBody(response.request())?.variables?.input };
  };
  const firstFiltered = await applyFemaleFilter('before modal');
  const firstFilteredDOMRows = await outputTable.locator('tbody tr').allInnerTexts();
  check('correctness', 'native female facet query and visible rows contain only the independent female Patient',
    isDeepStrictEqual(firstFiltered.rows, oracle.rows.filter(row => row.gender === 'female')) &&
      firstFilteredDOMRows.length === 1 && firstFilteredDOMRows[0].includes(oracle.femalePatientIDs[0]) &&
      firstFiltered.input.projectId === canonicalProject && sameSelector(firstFiltered.input.selector, output.selector) &&
      isDeepStrictEqual(firstFiltered.input.filters, [{ column: 'gender', op: 'EQ', value: 'female' }]),
      { requestProject: firstFiltered.input.projectId, selector: firstFiltered.input.selector,
        filters: firstFiltered.input.filters, responseRows: firstFiltered.rows, visibleRows: firstFilteredDOMRows });

  const firstModal = await openDownloadModal({ page, action, check, oracle, generation, phase: 'after-filter' });
  const cancel = firstModal.dialog.getByRole('button', { name: 'Cancel', exact: true });
  await action('cancel filtered dataset ZIP modal', cancel, locator => locator.click({ timeout: 5_000 }), {
    timeout: 5_000, budget: 5_000, after: () => firstModal.dialog.waitFor({ state: 'hidden', timeout: 5_000 }),
  });
  check('correctness', 'Cancel closes the filtered ZIP modal and removes its download link',
    await firstModal.dialog.isHidden() && await firstModal.dialog.getByRole('link', { name: 'Download ZIP', exact: true }).count() === 0,
    { visible: await firstModal.dialog.isVisible() });
  const reopened = await openDownloadModal({ page, action, check, oracle, generation, phase: 'after-cancel' });
  check('persistence', 'reopening the ZIP modal retains complete scope while the female facet remains selected',
    reopened.modal.text.includes('2 rows from the complete authorized population') &&
      firstFilteredDOMRows.length === 1 && firstFilteredDOMRows[0].includes(oracle.femalePatientIDs[0]),
    { modalRows: 2, filteredViewerRows: firstFilteredDOMRows });
  await action('cancel reopened filtered dataset ZIP modal before reload',
    reopened.dialog.getByRole('button', { name: 'Cancel', exact: true }), locator => locator.click({ timeout: 5_000 }), {
      timeout: 5_000, budget: 5_000, after: () => reopened.dialog.waitFor({ state: 'hidden', timeout: 5_000 }),
    });

  const reloadRuntimeStart = networkObserver.requests.length;
  const reloadRuntimePromise = networkObserver.waitForResponse(entry => entry.method === 'GET' && entry.path === runtimePath(project, explorer),
    { fromIndex: reloadRuntimeStart, timeoutMs: 5_000 });
  const reloadOutputPromise = page.waitForResponse(response => outputResponseMatches(response, {
    origin, project: canonicalProject, selector: output.selector, filtered: false,
  }), { timeout: 5_000 });
  let reloadedRuntimeEntry;
  let reloadedOutputResponse;
  await action('reload filtered Viewer and restore the unfiltered complete table',
    page.getByRole('button', { name: 'Download dataset', exact: true }), () => page.reload({ waitUntil: 'domcontentloaded' }), {
      timeout: 5_000, budget: 5_000,
      requiredCheck: 'filtered Viewer reload restores exact unfiltered rows within five seconds',
      after: async () => {
        [reloadedRuntimeEntry, reloadedOutputResponse] = await Promise.all([
          reloadRuntimePromise, reloadOutputPromise, waitForVisiblePatientIDs(page, oracle.patientIDs),
        ]).then(([runtimeEntry, response]) => [runtimeEntry, response]);
      },
    });
  const reloadedRows = await rowsFromOutputResponse(reloadedOutputResponse, 'Viewer reload');
  const reloadedRuntime = reloadedRuntimeEntry.response?.runtime;
  check('persistence', 'Viewer reload preserves publication and resets the transient female facet to both exact rows',
    reloadedRuntimeEntry.status >= 200 && reloadedRuntimeEntry.response?.project === canonicalProject &&
      reloadedRuntimeEntry.response?.explorerId === explorer &&
      (reloadedRuntime?.generation ?? reloadedRuntime?.publication?.generation) === generation &&
      reloadedRuntime?.publication?.revisionId === revisionId && isDeepStrictEqual(reloadedRows.rows, oracle.rows),
    { project: reloadedRuntimeEntry.response?.project, explorer: reloadedRuntimeEntry.response?.explorerId,
      generation: reloadedRuntime?.generation ?? reloadedRuntime?.publication?.generation,
      revisionId: reloadedRuntime?.publication?.revisionId, expectedRevisionId: revisionId,
      rows: reloadedRows.rows, expectedRows: oracle.rows });

  await action('reload Gender facet values after Viewer reload', loadValues, locator => locator.click({ timeout: 5_000 }), {
    timeout: 5_000, budget: 5_000, after: () => female.waitFor({ state: 'visible', timeout: 5_000 }),
  });
  assert.equal(await female.isChecked(), false, 'Viewer reload must reset the transient female selection');
  const finalFiltered = await applyFemaleFilter('after reload');
  const finalFilteredDOMRows = await outputTable.locator('tbody tr').allInnerTexts();
  check('correctness', 'reapplying the female facet after reload again leaves one exact Viewer row',
    isDeepStrictEqual(finalFiltered.rows, oracle.rows.filter(row => row.gender === 'female')) &&
      finalFilteredDOMRows.length === 1 && finalFilteredDOMRows[0].includes(oracle.femalePatientIDs[0]),
    { rows: finalFiltered.rows, visibleRows: finalFilteredDOMRows, femalePatientIDs: oracle.femalePatientIDs });

  const finalModal = await openDownloadModal({ page, action, check, oracle, generation, phase: 'after-reload' });
  const downloaded = await downloadAndInspect({ page, action, check, target, testInfo, oracle, publication,
    opened: finalModal, networkObserver, filteredViewerRows: finalFilteredDOMRows });
  report.target.datasetZipDownload = {
    path: downloaded.archivePath,
    sha256: downloaded.archiveSHA256,
    bytes: downloaded.descriptor.bytes,
    format: downloaded.artifact.manifest.format,
    rows: downloaded.artifact.manifest.rows,
    features: downloaded.artifact.manifest.features,
    outputId,
    schemaDigest: downloaded.artifact.manifest.identity.schemaDigest,
  };
  const sourceHashAfter = createHash('sha256').update(await readFile(oracle.patientPath)).digest('hex');
  check('correctness', 'raw Patient fixture remains unchanged through the export',
    sourceHashAfter === sourceHashBefore,
    { path: oracle.patientPath, before: sourceHashBefore, after: sourceHashAfter });
};
