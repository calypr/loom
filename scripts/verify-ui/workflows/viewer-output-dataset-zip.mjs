import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { isDeepStrictEqual } from 'node:util';
import {
  canonicalProjectID,
  expectedTrainingArtifactMembers,
} from '../../loom-dev.mjs';
import { browserURL } from './builder-url.mjs';
import { assertViewerPatientRows } from './viewer-query.mjs';
import {
  inspectBasicPatientDatasetZip,
  readBasicPatientDatasetZipOracle,
} from '../helpers/viewer-output-dataset-zip-oracle.mjs';

const outputId = 'patients';
const runtimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
const repositoryConfigPath = (project, generation) =>
  `/api/v1/projects/${encodeURIComponent(project)}/generations/${encodeURIComponent(generation)}/explorer-config`;

const datasetWorkspace = () => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderWorkspace',
  semanticsVersion: 10,
  explorer: { title: 'Basic Patient dataset ZIP' },
  documents: [{
    kind: 'ExplorerBuilderDocument',
    output: { id: outputId, title: 'Patients', rowLabel: 'Patient' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [{
      columnId: 'patient-id',
      column: 'patient_id',
      label: 'Patient ID',
      logicalType: 'string',
      occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
      table: { visible: true, order: 0 },
    }],
  }],
  tabs: [{ id: 'patients-tab', title: 'Patients', outputId, order: 0, visible: true }],
});

const waitForPatientRows = (page, expectedIDs) => page.waitForFunction(ids => {
  const table = document.querySelector('table[aria-label$=" results"]');
  if (!table) return false;
  const rows = [...table.querySelectorAll('tbody tr')].map(row => row.innerText);
  return rows.length === ids.length && ids.every(id => rows.some(row => row.includes(id)));
}, expectedIDs, { timeout: 5_000 });

const readDownloadModal = dialog => dialog.evaluate(element => {
  const value = label => [...element.querySelectorAll('dt')]
    .find(term => term.textContent.trim() === label)
    ?.nextElementSibling?.textContent.trim() ?? '';
  return {
    text: element.innerText,
    representation: value('Representation'),
    valuePreservation: value('Value preservation'),
    sourceGeneration: value('Source generation'),
    schemaDigest: value('Schema digest'),
    types: element.querySelector('[aria-label="Declared output types"]')?.innerText ?? '',
  };
});

const checkDownloadModal = (check, modal, oracle, generation) => {
  const correctRepresentation = [
    'Typed scalar CSV in a ZIP archive',
    'Typed JSON Lines in a ZIP archive',
  ].includes(modal.representation);
  const expectedPreservation = modal.representation === 'Typed scalar CSV in a ZIP archive'
    ? 'Scalar values retain declared types through schema.json; null and empty values remain distinct.'
    : 'Structured and repeated values retain native JSON types, row identity, nulls, and missing values.';
  check('correctness', 'dataset ZIP modal declares the complete Patient population and exact row count',
    modal.text.includes(`${oracle.rows.length} rows from the complete authorized population`) &&
      modal.text.includes('Each row represents Patient.'),
    { expectedRows: oracle.rows.length, rowGrain: 'Patient', modalText: modal.text });
  check('correctness', 'dataset ZIP modal declares a supported typed representation and preservation contract',
    correctRepresentation && modal.valuePreservation === expectedPreservation,
    { representation: modal.representation, valuePreservation: modal.valuePreservation });
  check('correctness', 'dataset ZIP modal binds the source generation and one Patient ID column',
    modal.sourceGeneration === generation && /^[a-f0-9]{64}$/i.test(modal.schemaDigest) &&
      modal.types.includes('Patient ID') && modal.types.includes('string') &&
      modal.text.includes('1 declared output columns'),
    { sourceGeneration: modal.sourceGeneration, expectedGeneration: generation,
      schemaDigest: modal.schemaDigest, declaredTypes: modal.types });
};

const checkDownloadLinkTarget = async ({ dialog, target, check, phase }) => {
  const link = dialog.getByRole('link', { name: 'Download ZIP', exact: true });
  const href = await link.getAttribute('href');
  const downloadName = await link.getAttribute('download');
  assert.equal(typeof href, 'string', 'visible Download ZIP link must have a native artifact URL');
  const downloadURL = new URL(href, target.uiUrl);
  const expectedArtifactPath = new RegExp(
    `^/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/default/authoring/v2/artifacts/artifact_[0-9a-f]{64}$`,
  );
  check('correctness', `${phase} Download ZIP link targets the exact native artifact endpoint`,
    downloadURL.origin === new URL(target.uiUrl).origin && expectedArtifactPath.test(downloadURL.pathname) &&
      downloadURL.search === '' && downloadURL.hash === '' && downloadName === 'loom-dataset-artifact-v2.zip',
    { origin: downloadURL.origin, path: downloadURL.pathname, search: downloadURL.search,
      hash: downloadURL.hash, downloadName });
  return { link, href, downloadName, downloadURL };
};

const readAndCheckArchive = async ({ page, action, check, target, testInfo, oracle, publication, modal, linkInfo, networkObserver, artifactRequestStart, phase }) => {
  const { link, href, downloadName, downloadURL } = linkInfo;
  const artifactID = decodeURIComponent(downloadURL.pathname.split('/').at(-1));
  const artifactCollectionPath = `${runtimePath(target.fixtureProject, 'default')}/authoring/v2/artifacts`;
  const preparation = await networkObserver.waitForResponse(entry =>
    entry.method === 'POST' && entry.path === artifactCollectionPath && entry.response?.id === artifactID,
  { fromIndex: artifactRequestStart, timeoutMs: 5_000 });
  const descriptor = preparation.response;
  const expectedIdentity = {
    project: publication.project,
    datasetGeneration: publication.generation,
    outputId: publication.outputId,
    revisionId: publication.revisionId,
    schemaDigest: modal.schemaDigest,
  };
  const descriptorMatches = preparation.status === 200 && preparation.networkComplete &&
    preparation.responseCaptureComplete && descriptor?.state === 'COMPLETE' && descriptor.id === artifactID &&
    descriptor.project === expectedIdentity.project && descriptor.explorerId === 'default' &&
    descriptor.datasetGeneration === expectedIdentity.datasetGeneration && descriptor.outputId === expectedIdentity.outputId &&
    descriptor.revisionId === expectedIdentity.revisionId && descriptor.schemaDigest === expectedIdentity.schemaDigest &&
    descriptor.rows === oracle.rows.length && descriptor.features === 1 && descriptor.mediaType === 'application/zip' &&
    descriptor.filename === downloadName && Number.isSafeInteger(descriptor.bytes) && descriptor.bytes > 0 &&
    /^[a-f0-9]{64}$/i.test(descriptor.archiveSha256 ?? '');
  check('correctness', `${phase} ZIP link binds the exact complete preparation descriptor`, descriptorMatches,
    { status: preparation.status, artifactId: artifactID, state: descriptor?.state, identity: {
      project: descriptor?.project, explorerId: descriptor?.explorerId,
      datasetGeneration: descriptor?.datasetGeneration, outputId: descriptor?.outputId,
      revisionId: descriptor?.revisionId, schemaDigest: descriptor?.schemaDigest,
    }, rows: descriptor?.rows, features: descriptor?.features, mediaType: descriptor?.mediaType,
    filename: descriptor?.filename, bytes: descriptor?.bytes, archiveSha256: descriptor?.archiveSha256 });

  const downloadPromise = page.waitForEvent('download', { timeout: 5_000 });
  const archivePath = testInfo.outputPath(`patient-dataset-${phase}.zip`);
  let download;
  await action(`download the complete dataset ZIP ${phase}`, link,
    locator => locator.click({ timeout: 5_000 }), {
      timeout: 5_000,
      budget: 5_000,
      requiredCheck: `${phase} dataset ZIP download action-to-file within five seconds`,
      after: async () => {
        download = await downloadPromise;
        await download.saveAs(archivePath);
      },
    });

  await testInfo.attach(`patient-dataset-${phase}.zip`, { path: archivePath, contentType: 'application/zip' });
  const archiveBytes = await readFile(archivePath);
  const archiveSHA256 = createHash('sha256').update(archiveBytes).digest('hex');
  const downloadFailure = await download.failure();
  const downloadMatchesDescriptor = downloadFailure === null && download.url() === downloadURL.href &&
    download.suggestedFilename() === downloadName && archiveBytes.byteLength === descriptor.bytes &&
    archiveSHA256 === descriptor.archiveSha256;
  check('correctness', `${phase} native ZIP download completes and matches the exact preparation bytes`,
    downloadMatchesDescriptor,
    { downloadURL: download.url(), expectedDownloadURL: downloadURL.href, downloadFailure,
      suggestedFilename: download.suggestedFilename(), downloadName, archiveBytes: archiveBytes.byteLength,
      expectedBytes: descriptor.bytes, archiveSHA256, expectedArchiveSHA256: descriptor.archiveSha256 });

  const inspected = inspectBasicPatientDatasetZip(archivePath, {
    expectedIdentity,
    expectedRows: oracle.rows,
    expectedArchiveSHA256: descriptor.archiveSha256,
  });
  const artifact = inspected.artifact;
  const expectedMembers = expectedTrainingArtifactMembers(artifact.manifest.format).sort();
  const actualMembers = [...inspected.memberNames].sort();
  check('correctness', `${phase} ZIP has the exact format-specific member list and verified member hashes`,
    isDeepStrictEqual(actualMembers, expectedMembers),
    { expectedMembers, actualMembers,
      checksums: artifact.manifest.members });

  const manifestIdentity = artifact.manifest.identity;
  const identityMatches = Object.entries(expectedIdentity).every(([field, value]) => manifestIdentity?.[field] === value) &&
    artifact.manifest.descriptor?.sourceGeneration === expectedIdentity.datasetGeneration &&
    artifact.manifest.descriptor?.outputKey === expectedIdentity.outputId &&
    artifact.manifest.descriptor?.publishedSchemaDigest === expectedIdentity.schemaDigest;
  check('correctness', `${phase} ZIP manifest binds the exact published project, generation, revision, output, and schema`,
    identityMatches,
    { expectedIdentity, manifestIdentity, descriptor: artifact.manifest.descriptor });

  const actualRows = artifact.rows.map(row => row.values)
    .sort((left, right) => String(left.patient_id).localeCompare(String(right.patient_id)));
  const expectedRows = [...oracle.rows].sort((left, right) => left.patient_id.localeCompare(right.patient_id));
  const exactRows = isDeepStrictEqual(actualRows, expectedRows);
  check('correctness', `${phase} ZIP data member contains the exact independent Patient ID rows`, exactRows,
    { format: artifact.manifest.format, dataMember: artifact.dataName, expectedRows, actualRows });
  check('correctness', `${phase} ZIP manifest and schema declare exactly the two Patient rows and one Patient ID field`,
    artifact.manifest.rows === expectedRows.length && artifact.manifest.features === 1 &&
      artifact.schema.columns.length === 1 && artifact.schema.columns[0].name === 'patient_id' &&
      artifact.schema.columns[0].outputKey === 'patient_id' && artifact.schema.columns[0].logicalType === 'string' &&
      artifact.manifest.selection?.datasetGeneration === publication.generation,
    { manifestRows: artifact.manifest.rows, manifestFeatures: artifact.manifest.features,
      schemaColumns: artifact.schema.columns, selection: artifact.manifest.selection });

  return { archivePath, archiveSHA256, artifact, descriptor };
};

export const viewerOutputDatasetZipWorkflow = async ({ page, report, action, check, networkObserver }, context, testInfo) => {
  const target = context.target;
  const project = target.fixtureProject;
  const canonicalProject = canonicalProjectID(project);
  const generation = target.fixtureGeneration;
  const explorer = 'default';
  const oracle = await readBasicPatientDatasetZipOracle(target.fixtureDir, {
    project: canonicalProject,
    generation,
    outputId,
  });
  const expectedPatientIDs = [...oracle.patientIDs].sort();
  report.target.project = project;
  report.target.canonicalProject = canonicalProject;
  report.target.explorer = explorer;
  report.target.datasetZipOracle = {
    path: oracle.patientPath,
    sha256: oracle.patientSHA256,
    patientIDs: oracle.patientIDs,
    rows: oracle.rows,
    outputId,
    rowGrain: oracle.rowGrain,
  };

  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: target.sourceRoot,
    encoding: 'utf8',
  }).trim();
  const configPath = repositoryConfigPath(project, generation);
  const seedResponse = await fetch(`${target.apiUrl}${configPath}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Loom-Source-Commit': sourceCommit,
    },
    body: JSON.stringify(datasetWorkspace()),
    signal: AbortSignal.timeout(30_000),
  });
  const seedText = await seedResponse.text();
  let seedBody;
  try { seedBody = JSON.parse(seedText); }
  catch { seedBody = { body: seedText.slice(0, 1_000) }; }
  const seedPassed = seedResponse.ok && seedBody.project === canonicalProject &&
    seedBody.generation === generation && seedBody.explorerId === explorer && seedBody.activated === true;
  check('correctness', 'repository config publishes the exact Patient ZIP output to the fixture generation', seedPassed,
    { status: seedResponse.status, path: configPath, project, canonicalProject,
      generation, explorer, response: seedBody, sourceCommit });
  report.target.repositoryConfigSeed = {
    project, canonicalProject, generation, explorer, sourceCommit,
    receiptId: seedBody.receiptId, revisionId: seedBody.revisionId,
  };

  const runtimeStart = networkObserver.requests.length;
  const runtimePromise = networkObserver.waitForResponse(entry =>
    entry.method === 'GET' && entry.path === runtimePath(project, explorer),
  { fromIndex: runtimeStart, timeoutMs: 5_000 });
  await page.goto(browserURL(target, project, explorer, 'viewer'), { waitUntil: 'domcontentloaded' });
  const runtimeEntry = await runtimePromise;
  const runtimeEnvelope = runtimeEntry.response;
  const runtime = runtimeEnvelope?.runtime;
  const outputs = runtime?.outputs ?? [];
  const output = outputs.find(candidate => candidate.outputId === outputId);
  const publishedGeneration = runtime?.generation ?? runtime?.publication?.generation;
  const revisionId = runtime?.publication?.revisionId;
  const publication = {
    project: runtimeEnvelope?.project,
    generation: publishedGeneration,
    revisionId,
    outputId: output?.outputId,
  };
  check('correctness', 'Viewer runtime retains the exact published Patient project, generation, revision, and output',
    runtimeEntry.status >= 200 && runtimeEntry.status < 300 && runtimeEnvelope?.project === canonicalProject &&
      runtimeEnvelope?.explorerId === explorer && publishedGeneration === generation &&
      typeof revisionId === 'string' && revisionId.length > 0 && output?.outputId === outputId,
    { status: runtimeEntry.status, project: runtimeEnvelope?.project, expectedProject: canonicalProject,
      explorer: runtimeEnvelope?.explorerId, generation: publishedGeneration, expectedGeneration: generation,
      revisionId, outputId: output?.outputId, expectedOutputId: outputId });
  assert(output, 'published runtime must expose the seeded Patient output');
  const outputColumns = output.columns ?? [];
  check('correctness', 'published Viewer output declares the exact Patient ID projection',
    outputColumns.length === 1 && outputColumns[0].column === 'patient_id' &&
      outputColumns[0].label === 'Patient ID' && outputColumns[0].logicalType === 'string',
    { outputColumns });
  report.target.viewerOutputId = output.outputId;
  report.target.datasetPublication = publication;

  const outputTable = page.locator('table[aria-label$=" results"]');
  await outputTable.waitFor({ state: 'visible', timeout: 5_000 });
  await waitForPatientRows(page, expectedPatientIDs);
  const viewerRows = await outputTable.locator('tbody tr').allInnerTexts();
  const viewerIDs = assertViewerPatientRows(viewerRows, expectedPatientIDs);
  check('correctness', 'Viewer renders exactly the independent Basic Patient IDs before export',
    isDeepStrictEqual(viewerIDs, expectedPatientIDs),
    { expectedPatientIDs, actualPatientIDs: viewerIDs, renderedRows: viewerRows });

  const dialog = page.getByRole('dialog').filter({ hasText: 'Download dataset' });
  const openModal = async phase => {
    const button = page.getByRole('button', { name: 'Download dataset', exact: true });
    await action(`open dataset ZIP modal ${phase}`, button,
      locator => locator.click({ timeout: 5_000 }), {
        timeout: 5_000,
        budget: 5_000,
        requiredCheck: `${phase} dataset ZIP modal action-to-render within five seconds`,
        after: () => dialog.getByRole('link', { name: 'Download ZIP', exact: true })
          .waitFor({ state: 'visible', timeout: 5_000 }),
      });
    const modal = await readDownloadModal(dialog);
    checkDownloadModal(check, modal, oracle, generation);
    const linkInfo = await checkDownloadLinkTarget({ dialog, target, check, phase });
    return { modal, linkInfo };
  };

  await openModal('before cancel');
  const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true });
  await action('cancel dataset ZIP preview', cancel,
    locator => locator.click({ timeout: 5_000 }), {
      timeout: 5_000,
      budget: 5_000,
      requiredCheck: 'cancel dataset ZIP preview action-to-close within five seconds',
      after: () => dialog.waitFor({ state: 'hidden', timeout: 5_000 }),
    });
  check('correctness', 'Cancel closes the dataset ZIP modal without leaving an active download link',
    await dialog.isHidden() && await dialog.getByRole('link', { name: 'Download ZIP', exact: true }).count() === 0,
    { dialogVisible: await dialog.isVisible() });

  await openModal('after cancel');
  const cancelAgain = dialog.getByRole('button', { name: 'Cancel', exact: true });
  await action('close reopened dataset ZIP preview before reload', cancelAgain,
    locator => locator.click({ timeout: 5_000 }), {
      timeout: 5_000,
      budget: 5_000,
      requiredCheck: 'reopened dataset ZIP preview closes before reload within five seconds',
      after: () => dialog.waitFor({ state: 'hidden', timeout: 5_000 }),
    });

  const reloadStart = networkObserver.requests.length;
  const reloadRuntimePromise = networkObserver.waitForResponse(entry =>
    entry.method === 'GET' && entry.path === runtimePath(project, explorer),
  { fromIndex: reloadStart, timeoutMs: 5_000 });
  let reloadedEntry;
  await action('reload the published Patient Viewer and restore exact rows',
    page.getByRole('button', { name: 'Download dataset', exact: true }),
    () => page.reload({ waitUntil: 'domcontentloaded' }), {
      timeout: 5_000,
      budget: 5_000,
      requiredCheck: 'published Patient Viewer reload restores exact rows within five seconds',
      after: async () => {
        [reloadedEntry] = await Promise.all([reloadRuntimePromise, waitForPatientRows(page, expectedPatientIDs)]);
      },
    });
  const reloadedRuntime = reloadedEntry.response?.runtime;
  const reloadedOutput = reloadedRuntime?.outputs?.find(candidate => candidate.outputId === outputId);
  const reloadedIDs = assertViewerPatientRows(
    await outputTable.locator('tbody tr').allInnerTexts(), expectedPatientIDs,
  );
  check('persistence', 'reload retains the same published Patient revision, output, generation, and exact rows',
    reloadedEntry.status >= 200 && reloadedEntry.status < 300 && reloadedEntry.response?.project === canonicalProject &&
      reloadedEntry.response?.explorerId === explorer && reloadedOutput?.outputId === outputId &&
      (reloadedRuntime?.generation ?? reloadedRuntime?.publication?.generation) === generation &&
      reloadedRuntime?.publication?.revisionId === revisionId &&
      isDeepStrictEqual(reloadedIDs, expectedPatientIDs),
    { project: reloadedEntry.response?.project, generation: reloadedRuntime?.generation ?? reloadedRuntime?.publication?.generation,
      explorer: reloadedEntry.response?.explorerId, outputId: reloadedOutput?.outputId, expectedOutputId: outputId,
      revisionId: reloadedRuntime?.publication?.revisionId, expectedRevisionId: revisionId,
      patientIDs: reloadedIDs, expectedPatientIDs });

  const artifactRequestStart = networkObserver.requests.length;
  const finalModal = await openModal('after reload');
  const downloaded = await readAndCheckArchive({
    page, action, check, target, testInfo, oracle, publication, modal: finalModal.modal, linkInfo: finalModal.linkInfo,
    networkObserver, artifactRequestStart, phase: 'after-reload',
  });
  report.target.datasetZipDownload = {
    path: downloaded.archivePath,
    sha256: downloaded.archiveSHA256,
    bytes: downloaded.descriptor.bytes,
    format: downloaded.artifact.manifest.format,
    rows: downloaded.artifact.manifest.rows,
    members: expectedTrainingArtifactMembers(downloaded.artifact.manifest.format),
    identity: downloaded.artifact.manifest.identity,
  };
};
