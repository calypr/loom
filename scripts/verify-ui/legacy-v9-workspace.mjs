import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { browserURL } from './builder-url.mjs';
import { configureNativePage, evaluate, goto, reload, waitFor } from '../lib/playwright-authoring-page.mjs';
import { sanitizeBody, sanitizePayload } from '../lib/playwright-browser.mjs';
import { recordCheck } from './report.mjs';

const EXPECTED_IDS = ['dev-patient-001', 'dev-patient-002'];
const tableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
const readNDJSON = path => readFileSync(path, 'utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));

const requireCheck = (report, dimension, name, passed, evidence = {}) => {
  recordCheck(report, dimension, name, passed, evidence);
  assert(passed, `required legacy workspace check failed: ${name}`);
};

const commandDiagnostics = result => JSON.stringify(sanitizePayload({
  status: result.status, error: result.error?.message,
  stdout: String(result.stdout ?? '').slice(0, 1200), stderr: String(result.stderr ?? '').slice(0, 1200),
})).slice(0, 2400);

const apiJSON = async (target, path, body) => {
  const response = await fetch(target.apiUrl + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  if (!response.ok) throw new Error(`owned QA API ${path} returned HTTP ${response.status}: ${JSON.stringify(sanitizePayload(value)).slice(0, 800)}`);
  return value;
};

const storageOwnerKey = (project, explorer) => 'explorer_' + createHash('sha256')
  .update(Buffer.concat([Buffer.from([0]), Buffer.from(project), Buffer.from([0]), Buffer.from(explorer)]))
  .digest('hex');

const canonicalLegacyV9 = (sourceRoot, workspace) => {
  const directory = mkdtempSync(join(tmpdir(), 'loom-legacy-v9-digest-'));
  const inputPath = join(directory, 'workspace.json');
  const outputPath = join(directory, 'canonical-result.json');
  const testPath = join(directory, 'legacy_digest_overlay_test.go');
  const virtualPath = resolve(sourceRoot, 'internal/explorer/authoringv2', `zz_loom_legacy_digest_${process.pid}_${Date.now()}_test.go`);
  const overlayPath = join(directory, 'overlay.json');
  const helper = `package authoringv2
import (
  "encoding/json"
  "os"
  "testing"
)
func TestVerificationLegacyV9CanonicalDigest(t *testing.T) {
  raw, err := os.ReadFile(os.Getenv("LOOM_VERIFY_WORKSPACE")); if err != nil { t.Fatal(err) }
  var workspace Workspace
  if err := json.Unmarshal(raw, &workspace); err != nil { t.Fatal(err) }
  if workspace.SemanticsVersion != 9 { t.Fatalf("semanticsVersion = %d, want 9", workspace.SemanticsVersion) }
  canonical, err := workspace.CanonicalJSON(); if err != nil { t.Fatal(err) }
  digest, err := workspace.Digest(); if err != nil { t.Fatal(err) }
  output, err := json.Marshal(struct { Canonical json.RawMessage \`json:"canonical"\`; Digest string \`json:"digest"\` }{canonical, digest}); if err != nil { t.Fatal(err) }
  if err := os.WriteFile(os.Getenv("LOOM_VERIFY_CANONICAL_RESULT"), output, 0600); err != nil { t.Fatal(err) }
}
`;
  try {
    writeFileSync(inputPath, JSON.stringify(workspace), { mode: 0o600 });
    writeFileSync(testPath, helper, { mode: 0o600 });
    writeFileSync(overlayPath, JSON.stringify({ Replace: { [virtualPath]: testPath } }), { mode: 0o600 });
    const result = spawnSync('go', ['test', '-overlay', overlayPath, './internal/explorer/authoringv2', '-run', '^TestVerificationLegacyV9CanonicalDigest$', '-count=1'], {
      cwd: sourceRoot,
      encoding: 'utf8',
      env: { ...process.env, LOOM_VERIFY_WORKSPACE: inputPath, LOOM_VERIFY_CANONICAL_RESULT: outputPath },
      timeout: 120000,
      maxBuffer: 2 * 1024 * 1024,
    });
    if (result.status !== 0) throw new Error(`Go canonical workspace digest helper failed: ${commandDiagnostics(result)}`);
    return JSON.parse(readFileSync(outputPath, 'utf8'));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
};

const assertOwnedArango = target => {
  const args = ['compose', '--project-name', target.composeProject, '--file', target.composeFile, 'ps', '-q', 'arangodb'];
  const listed = spawnSync('docker', args, { cwd: target.sourceRoot, encoding: 'utf8', timeout: 15000 });
  const ids = listed.stdout.trim().split(/\s+/).filter(Boolean);
  assert.equal(listed.status, 0, `owned Compose must resolve its Arango service: ${commandDiagnostics(listed)}`);
  assert.equal(ids.length, 1, 'owned Compose must resolve exactly one Arango container');
  const inspected = spawnSync('docker', ['inspect', ids[0]], { cwd: target.sourceRoot, encoding: 'utf8', timeout: 15000 });
  assert.equal(inspected.status, 0, `owned Arango container must be inspectable: ${commandDiagnostics(inspected)}`);
  const [container] = JSON.parse(inspected.stdout);
  assert.equal(container.Config?.Labels?.['com.docker.compose.project'], target.composeProject);
  assert.equal(container.Config?.Labels?.['com.docker.compose.service'], 'arangodb');
  assert.equal(container.State?.Running, true, 'owned Arango container must be running');
  const mounts = (container.Mounts ?? []).filter(mount => mount.Destination === '/var/lib/arangodb3');
  assert.equal(mounts.length, 1, 'owned Arango must use exactly one data volume');
  assert.equal(mounts[0].Type, 'volume');
  assert.equal(mounts[0].Name, `${target.composeProject}_loom_dev_arangodb_data`);
  const volume = spawnSync('docker', ['volume', 'inspect', mounts[0].Name], { cwd: target.sourceRoot, encoding: 'utf8', timeout: 15000 });
  assert.equal(volume.status, 0, `owned Arango data volume must be inspectable: ${commandDiagnostics(volume)}`);
  const [volumeInfo] = JSON.parse(volume.stdout);
  assert.equal(volumeInfo.Labels?.['com.docker.compose.project'], target.composeProject);
};

const seedLegacyV9Draft = ({ target, explorer, title, builder }) => {
  assertOwnedArango(target);
  const rawProject = target.fixtureProject;
  const workspace = structuredClone(builder.workspace);
  workspace.semanticsVersion = 9;
  assert.equal(workspace.explorer.title, title, 'legacy workspace title must match the freshly created owner');
  const canonical = canonicalLegacyV9(target.sourceRoot, workspace);
  const key = storageOwnerKey(rawProject, explorer);
  const airConfig = readFileSync(join(target.sourceRoot, '.air.dev.toml'), 'utf8');
  const database = /--database\s+([A-Za-z0-9_-]+)/.exec(airConfig)?.[1];
  assert(database, 'validated dev source root must declare the Loom API Arango database');
  assert.equal(database, 'loom_dev', 'legacy seed may only update the owned development database');
  const query = `LET owners = (FOR d IN loom_explorers FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorer RETURN d)
FOR owner IN owners
FILTER LENGTH(owners) == 1 AND owner.title == @title AND owner.managementMode == "INTERACTIVE"
  AND owner.draftVersion == @version AND owner.draftDigest == @previousDigest
  AND (!HAS(owner, "activeRevisionId") OR owner.activeRevisionId == "")
UPDATE owner WITH { draftConfig: @draftConfig, draftDigest: @newDigest } IN loom_explorers
RETURN { project: NEW.project, explorerId: NEW.explorerId, title: NEW.title, managementMode: NEW.managementMode, draftVersion: NEW.draftVersion, draftDigest: NEW.draftDigest }`;
  const bind = {
    key, project: rawProject, explorer, title, version: builder.draftVersion,
    previousDigest: builder.draftDigest, draftConfig: canonical.canonical, newDigest: canonical.digest,
  };
  const js = `const db = require('@arangodb').db; db._useDatabase(${JSON.stringify(database)}); const rows = db._query(${JSON.stringify(query)}, ${JSON.stringify(bind)}).toArray(); if (rows.length !== 1) throw new Error('legacy seed compare-and-swap did not update exactly one owned draft'); print(JSON.stringify(rows[0]));`;
  const seeded = spawnSync('docker', [
    'compose', '--project-name', target.composeProject, '--file', target.composeFile,
    'exec', '-T', 'arangodb', 'arangosh', '--server.endpoint', 'tcp://127.0.0.1:8529',
    '--server.database', database, '--javascript.execute-string', js.replaceAll('@', '\\u0040'),
  ], { cwd: target.sourceRoot, encoding: 'utf8', timeout: 30000, maxBuffer: 2 * 1024 * 1024 });
  if (seeded.status !== 0) throw new Error(`owned legacy v9 compare-and-swap seed failed: ${commandDiagnostics(seeded)}`);
  const row = JSON.parse(seeded.stdout.trim().split(/\r?\n/).at(-1));
  assert.equal(row.project, rawProject);
  assert.equal(row.explorerId, explorer);
  assert.equal(row.title, title);
  assert.equal(row.managementMode, 'INTERACTIVE');
  assert.equal(row.draftVersion, builder.draftVersion);
  assert.equal(row.draftDigest, canonical.digest);
  return { canonical: canonical.canonical, digest: canonical.digest, storageKey: key, draftVersion: builder.draftVersion };
};

const exactWorkspace = (builder, { generation, scopeDigest, title, outputId, draftVersion, draftDigest }) => {
  assert.equal(builder.catalog?.generation, generation);
  assert.equal(builder.catalog?.authorizationScopeDigest, scopeDigest);
  assert(builder.catalog?.snapshotToken);
  assert.equal(builder.workspace?.explorer?.title, title);
  const document = builder.workspace?.documents?.find(item => item.output?.id === outputId);
  assert(document, `Builder workspace must retain output ${outputId}`);
  if (draftVersion !== undefined) assert.equal(builder.draftVersion, draftVersion);
  if (draftDigest !== undefined) assert.equal(builder.draftDigest, draftDigest);
  return document;
};

export const legacyV9WorkspaceWorkflow = async (workflow, context) => {
  const { page, report } = workflow;
  configureNativePage(page);
  assert.equal(context.custom, false, 'legacy v9 case requires a fresh owned development fixture');
  assert.equal(context.seed?.fresh, true, 'legacy v9 case must use a fresh verification project');
  assert(context.target.fixtureProject.startsWith('loom_dev_verify_'));
  assert.equal(context.target.fixtureDir.endsWith('/testdata/devloop-fixture'), true);

  const patientIDs = readNDJSON(join(context.target.fixtureDir, 'Patient.ndjson'))
    .filter(resource => resource?.resourceType === 'Patient').map(resource => resource.id).sort();
  assert.deepEqual(patientIDs, EXPECTED_IDS, 'basic fixture must retain its independent Patient identity oracle');
  report.target.fixtureRawOracle = { source: 'fresh project Patient.ndjson', project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration, resourceType: 'Patient', patientIDs };
  requireCheck(report, 'correctness', 'basic Patient fixture contains the two independently known records', true, report.target.fixtureRawOracle);

  const title = `Verify ${context.runID.slice(-10)} legacy v9 rows`;
  const project = context.target.fixtureProject;
  await goto(page, browserURL(context.target, project, context.target.bootstrapExplorerId, 'builder'),
    "document.body.innerText.includes('Build your first table') || document.body.innerText.includes('Dataset graph')");
  await workflow.action('open Explorer creation', page.getByText('New explorer', { exact: true }), () => page.getByText('New explorer', { exact: true }).click());
  await waitFor(page, "Boolean(document.querySelector('#new-explorer-name'))");
  await workflow.action('name legacy Explorer', page.locator('#new-explorer-name'), () => page.locator('#new-explorer-name').fill(title), { editable: true });
  const expectedUIOrigin = new URL(context.target.uiUrl).origin;
  const createdResponsePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === expectedUIOrigin
      && url.pathname === `/api/v1/projects/${encodeURIComponent(project)}/explorers`
      && response.request().method() === 'POST';
  }, { timeout: 5000 });
  await workflow.action('create blank legacy Explorer', page.getByRole('button', { name: 'Create blank', exact: true }),
    () => page.getByRole('button', { name: 'Create blank', exact: true }).click(), {
      after: () => page.waitForFunction(expectedTitle => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expectedTitle, title),
    });
  const createdResponse = await createdResponsePromise;
  assert.equal(createdResponse.status(), 201, 'fresh API-created Explorer owner must be created successfully');
  const createdOwner = await createdResponse.json();
  const explorer = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorer && explorer !== context.target.bootstrapExplorerId);
  assert.equal(createdOwner.explorerId, explorer);
  assert.equal(createdOwner.title, title);
  assert(createdOwner.project, 'fresh Explorer creation must return its API-canonical project scope');
  report.target.apiProject = createdOwner.project;
  report.target.explorer = explorer;

  await workflow.action('name Patient table', page.locator('#first-table-name'), () => page.locator('#first-table-name').fill('Patients'), { editable: true });
  await workflow.action('create Patient root table', page.getByRole('button', { name: 'Choose Patient rows', exact: true }),
    () => page.getByRole('button', { name: 'Choose Patient rows', exact: true }).click(), {
      after: () => page.waitForFunction(() => {
        const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
        return Boolean(table && table.querySelectorAll('[role="columnheader"]').length > 0
          && Number(table.getAttribute('aria-rowcount')) > 1 && !document.body.innerText.includes('Loading your table…'));
      }, undefined, { timeout: 5000 }),
    });
  await page.getByTestId('construction-action-add-columns').waitFor({ state: 'visible' });
  await workflow.action('open field picker for nested Patient list', page.getByTestId('construction-action-add-columns'),
    () => page.getByTestId('construction-action-add-columns').click());
  await workflow.action('choose Patient fields and related data', page.getByRole('button', { name: 'Fields and related data', exact: true }),
    () => page.getByRole('button', { name: 'Fields and related data', exact: true }).click());
  await workflow.action('open raw FHIR fields', page.getByText('Raw FHIR fields (advanced)', { exact: true }),
    () => page.getByText('Raw FHIR fields (advanced)', { exact: true }).click());
  const apiRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
  const ownerListPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const readBuilder = () => apiJSON(context.target, `${apiRoot}/authoring/v2/builder`);
  const readOwnedSummary = async () => {
    const summaries = await apiJSON(context.target, ownerListPath);
    assert(Array.isArray(summaries), 'owned Explorer list endpoint must return its public summary array');
    const matches = summaries.filter(summary => summary.explorerId === explorer);
    assert.equal(matches.length, 1, 'raw fixture project plus API-created Explorer ID must identify exactly one owner');
    const [summary] = matches;
    assert.equal(summary.project, createdOwner.project, 'public Explorer summary must prove raw project maps to its canonical project ID');
    assert.equal(summary.explorerId, explorer);
    assert.equal(summary.title, title);
    assert.equal(summary.management, 'INTERACTIVE');
    return summary;
  };
  const initialOwnerSummary = await readOwnedSummary();
  report.target.ownerBoundary = {
    createMethod: createdResponse.request().method(),
    createPath: new URL(createdResponse.url()).pathname,
    builderPath: `${apiRoot}/authoring/v2/builder`,
    summaryMethod: 'GET', summaryPath: ownerListPath,
    rawFixtureProject: project, apiProject: initialOwnerSummary.project,
    explorerId: initialOwnerSummary.explorerId, title: initialOwnerSummary.title,
    management: initialOwnerSummary.management,
  };
  const initialBuilder = await readBuilder();
  assert.equal(initialBuilder.catalog?.generation, context.target.fixtureGeneration);
  assert(initialBuilder.catalog?.snapshotToken && initialBuilder.catalog?.authorizationScopeDigest);
  const initialPatient = initialBuilder.workspace?.documents?.find(document => document.rootResourceType === 'Patient');
  assert(initialPatient?.columns?.some(column => column.source?.field?.path === 'id'),
    'native Patient root creation must retain its default Patient.id identity column');
  const patientGivenField = page.getByRole('checkbox', { name: 'Select Patient.name[].given[]', exact: true });
  await patientGivenField.waitFor({ state: 'visible' });
  await workflow.action('select repeated Patient given-name field', patientGivenField, () => patientGivenField.check());
  const addGiven = page.getByRole('button', { name: /Add 1 selected feature/ });
  await workflow.action('add repeated Patient given-name field', addGiven, () => addGiven.click(), {
    after: () => page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true }).waitFor({ state: 'visible' }),
  });
  const initialGivenState = await readBuilder();
  assert.equal(initialGivenState.catalog?.generation, context.target.fixtureGeneration);
  assert.equal(initialGivenState.catalog?.authorizationScopeDigest, initialBuilder.catalog.authorizationScopeDigest);
  const patientNode = initialGivenState.catalog?.nodes?.find(node => node.resourceType === 'Patient');
  assert(patientNode?.nodeId, 'Builder catalog must identify its Patient root node');
  const givenCandidate = initialGivenState.catalog?.candidates?.find(candidate =>
    candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'name[].given[]');
  assert(givenCandidate?.label, 'Builder catalog must provide the exact Patient given-name field label');
  const choiceDialog = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
  const givenDisplayLabel = 'Name Given';
  const givenChoiceHeading = choiceDialog.getByRole('heading', { name: `Patient · ${givenDisplayLabel}`, exact: true });
  assert.equal(await givenChoiceHeading.count(), 1, 'the exact Patient given-name catalog item must be visible in the choice dialog');
  const allGivenValues = choiceDialog.getByRole('radio', { name: `${givenDisplayLabel}: Keep all matching values`, exact: true });
  assert.equal(await allGivenValues.count(), 1, 'the repeated Patient given-name field must expose the exact native ALL list choice');
  await workflow.action('choose ALL for Patient.name[].given[] list output', allGivenValues,
    () => allGivenValues.check(), { after: async () => assert.equal(await allGivenValues.isChecked(), true) });
  const confirmGiven = choiceDialog.getByRole('button', { name: 'Add 1 column', exact: true });
  await workflow.action('confirm Patient given-name ALL column', confirmGiven, () => confirmGiven.click(), {
    after: () => page.getByRole('button', { name: 'Apply columns', exact: true }).waitFor({ state: 'visible' }),
  });
  await workflow.action('apply independent array-backed Patient column', page.getByRole('button', { name: 'Apply columns', exact: true }),
    () => page.getByRole('button', { name: 'Apply columns', exact: true }).click(), {
      after: () => page.waitForFunction(expectedLabel => {
        const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
        return Boolean(table && [...table.querySelectorAll('[role="columnheader"]')].some(cell => cell.textContent.trim() === expectedLabel)
          && [...table.querySelectorAll('[role="row"]')].slice(1).length === 2);
      }, givenDisplayLabel, { timeout: 5000 }),
    });
  const current = await readBuilder();
  assert.equal(current.catalog?.generation, context.target.fixtureGeneration);
  assert(current.catalog?.authorizationScopeDigest && current.catalog?.snapshotToken);
  assert.equal(current.catalog.authorizationScopeDigest, initialBuilder.catalog.authorizationScopeDigest);
  assert(current.draftVersion > 0 && current.draftDigest);
  const currentPatient = current.workspace?.documents?.find(document => document.rootResourceType === 'Patient');
  const givenColumn = currentPatient?.columns?.find(column => column.source?.field?.path === 'name[].given[]');
  const idColumn = currentPatient?.columns?.find(column => column.source?.field?.path === 'id');
  assert.equal(givenColumn?.label, givenDisplayLabel, 'the saved list column must keep the exact native Patient given-name display label');
  assert(givenColumn && givenColumn.source?.field?.projectionMode === 'ALL',
    'the saved workspace must contain the repeated Patient.name[].given[] field');
  assert(idColumn, 'the saved workspace must retain Patient.id as its independent row identity');
  const givenCandidateSaved = current.catalog?.candidates?.find(candidate =>
    candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'name[].given[]');
  assert(givenCandidateSaved?.repeated === true && givenCandidateSaved.projectionModes?.includes('ALL'),
    'ListExpand fixture field must be catalog-proven repeated and support the native ALL list projection');
  const rawGivenByID = new Map(readNDJSON(join(context.target.fixtureDir, 'Patient.ndjson'))
    .filter(resource => resource?.resourceType === 'Patient')
    .map(resource => [resource.id, (resource.name ?? []).flatMap(name => name.given ?? [])]));
  const initialGrid = await evaluate(page, `(()=>{const table=document.querySelector(${JSON.stringify(tableSelector)});if(!table)return {headers:[],rows:[]};const rows=[...table.querySelectorAll('[role=row]')];return {headers:[...(rows[0]?.querySelectorAll('[role=columnheader]')??[])].map(cell=>cell.textContent.trim()),rows:rows.slice(1).map(row=>[...row.querySelectorAll('[role=cell]')].map(cell=>cell.innerText.trim()))}})()`);
  const idIndex = initialGrid.headers.findIndex(label => label === idColumn.label);
  const givenIndex = initialGrid.headers.findIndex(label => label === givenColumn.label);
  assert(idIndex >= 0 && givenIndex >= 0, `native preview omitted the independent ID/list columns: ${JSON.stringify(initialGrid.headers)}`);
  const initialPairs = initialGrid.rows.map(row => [row[idIndex], row[givenIndex]]).sort((a, b) => a[0].localeCompare(b[0]));
  assert.deepEqual(initialPairs.map(([id]) => id), EXPECTED_IDS, 'native preview must contain exactly the two independent Patient IDs without duplicates');
  for (const [id, rendered] of initialPairs) {
    assert(rawGivenByID.has(id), `native preview contains an unexpected Patient ID ${id}`);
    assert.equal(rendered, rawGivenByID.get(id).join('; '), `native ALL list preview must render the exact raw given names for ${id}`);
  }

  const seeded = seedLegacyV9Draft({ target: context.target, explorer, title, builder: current });
  report.target.legacySeed = { semanticsVersion: 9, storageKey: seeded.storageKey, draftVersion: seeded.draftVersion,
    digest: seeded.digest, rawProject: project, explorer };
  let legacy = await readBuilder();
  exactWorkspace(legacy, { generation: context.target.fixtureGeneration,
    scopeDigest: current.catalog.authorizationScopeDigest, title, outputId: currentPatient.output.id,
    draftVersion: seeded.draftVersion, draftDigest: seeded.digest });
  const seededOwnerSummary = await readOwnedSummary();
  requireCheck(report, 'correctness', 'saved v9 Builder workspace preserves the Go canonical draft digest',
    legacy.workspace.semanticsVersion === 9 && legacy.draftDigest === seeded.digest,
      { semanticsVersion: legacy.workspace.semanticsVersion, draftVersion: legacy.draftVersion,
      digestMatched: legacy.draftDigest === seeded.digest, generation: legacy.catalog.generation,
      scopeDigestMatched: legacy.catalog.authorizationScopeDigest === current.catalog.authorizationScopeDigest,
      apiProject: seededOwnerSummary.project, rawStorageProject: project,
      explorer: seededOwnerSummary.explorerId, title: seededOwnerSummary.title });

  const capabilityPath = `${apiRoot}/authoring/v2/construction-capabilities`;
  const capabilityResponseOutcomePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === expectedUIOrigin && url.pathname === capabilityPath && response.request().method() === 'POST';
  }, { timeout: 5000 }).then(
    response => ({ response }),
    error => ({ error: error instanceof Error ? error.message : String(error) }),
  );
  let capabilityResponse;
  let capabilityBody;
  let capabilityBodyDiagnostic = 'response body not read';
  await workflow.action('reload saved legacy v9 workspace', page.locator('body'),
    () => reload(page, "document.body.innerText.includes('DATASET WORKSPACE')"));
  legacy = await readBuilder();
  exactWorkspace(legacy, { generation: context.target.fixtureGeneration,
    scopeDigest: current.catalog.authorizationScopeDigest, title, outputId: currentPatient.output.id,
    draftVersion: seeded.draftVersion, draftDigest: seeded.digest });
  const reloadedOwnerSummary = await readOwnedSummary();
  requireCheck(report, 'persistence', 'reload keeps legacy semantics v9 and its exact saved digest',
    legacy.workspace.semanticsVersion === 9 && legacy.draftDigest === seeded.digest
      && legacy.draftVersion === seeded.draftVersion,
    { semanticsVersion: legacy.workspace.semanticsVersion, draftVersion: legacy.draftVersion,
      digest: legacy.draftDigest, rawStorageProject: project, apiProject: reloadedOwnerSummary.project,
      explorer: reloadedOwnerSummary.explorerId, generation: legacy.catalog.generation,
      authorizationScopeDigest: legacy.catalog.authorizationScopeDigest });

  const table = legacy.workspace.documents.find(document => document.rootResourceType === 'Patient');
  assert(table?.output?.id, 'legacy workspace must retain the Patient output identity');
  await workflow.action('select legacy Patient table', page.getByTestId(`construction-table-${table.output.id}`),
    () => page.getByTestId(`construction-table-${table.output.id}`).click(), {
      after: async () => {
        await page.waitForFunction(outputId => document.querySelector(`[data-testid="construction-table-${CSS.escape(outputId)}"]`)?.getAttribute('aria-current') === 'page'
          && Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')), table.output.id);
        const outcome = await capabilityResponseOutcomePromise;
        assert.equal(outcome.error, undefined, `no exact scoped construction-capabilities response arrived: ${outcome.error ?? ''}`);
        capabilityResponse = outcome.response;
        let timeoutID;
        const bodyOutcome = await Promise.race([
          capabilityResponse.text().then(text => ({ text }), error => ({ error: error instanceof Error ? error.message : String(error) })),
          new Promise(resolve => { timeoutID = setTimeout(() => resolve({ timedOut: true }), 1200); }),
        ]);
        clearTimeout(timeoutID);
        if (bodyOutcome.text !== undefined) {
          capabilityBodyDiagnostic = sanitizeBody(bodyOutcome.text);
          try { capabilityBody = JSON.parse(bodyOutcome.text); } catch { capabilityBody = undefined; }
        } else {
          capabilityBodyDiagnostic = bodyOutcome.timedOut
            ? 'response body read exceeded 1200 ms'
            : `response body read failed: ${bodyOutcome.error}`;
        }
      },
    });
  await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible' });
  await workflow.action('open legacy Patient row settings', page.getByTestId('construction-rows-settings-trigger'),
    () => page.getByTestId('construction-rows-settings-trigger').click(), {
      after: async () => {
        assert.equal(capabilityResponse.status(), 200,
          `row controls must receive exact-draft construction capabilities: ${capabilityBodyDiagnostic}`);
        assert(capabilityBody, `successful construction capabilities response must have bounded valid JSON: ${capabilityBodyDiagnostic}`);
        await page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible' });
      },
    });
  const capabilityRequest = capabilityResponse.request().postDataJSON();
  requireCheck(report, 'correctness', 'construction capabilities echo the exact legacy output, source stage, snapshot, version, and digest',
    capabilityRequest?.outputId === table.output.id
      && capabilityRequest?.stageId === 'source_projection'
      && capabilityRequest?.snapshotToken === legacy.catalog.snapshotToken
      && capabilityRequest?.expectedDraftVersion === seeded.draftVersion
      && capabilityRequest?.expectedDraftDigest === seeded.digest
      && capabilityBody?.outputId === table.output.id
      && capabilityBody?.stageId === 'source_projection'
      && capabilityBody?.selectedStage?.id === 'source_projection'
      && capabilityBody?.snapshotToken === legacy.catalog.snapshotToken
      && capabilityBody?.draftVersion === seeded.draftVersion
      && capabilityBody?.draftDigest === seeded.digest,
    { status: capabilityResponse.status(), path: capabilityPath,
      outputIdMatched: capabilityRequest?.outputId === table.output.id,
      stageId: capabilityRequest?.stageId,
      snapshotTokenMatched: capabilityRequest?.snapshotToken === legacy.catalog.snapshotToken,
      expectedDraftVersion: capabilityRequest?.expectedDraftVersion,
      expectedDraftDigestMatched: capabilityRequest?.expectedDraftDigest === seeded.digest,
      responseOutputId: capabilityBody?.outputId, responseStageId: capabilityBody?.stageId,
      responseSnapshotMatched: capabilityBody?.snapshotToken === legacy.catalog.snapshotToken,
      responseDraftVersion: capabilityBody?.draftVersion,
      responseDraftDigestMatched: capabilityBody?.draftDigest === seeded.digest,
      selectedStageId: capabilityBody?.selectedStage?.id,
      rawFixtureProject: project, apiProject: reloadedOwnerSummary.project, explorer,
      generation: legacy.catalog.generation,
      authorizationScopeDigest: legacy.catalog.authorizationScopeDigest });
  await waitFor(page, "Boolean(document.querySelector('[data-testid=construction-action-group-rows]'))");
  const actionIDs = ['construction-action-group-rows', 'construction-action-related-rows', 'construction-action-expand-rows'];
  const actionStates = {};
  for (const id of actionIDs) {
    const locator = page.getByTestId(id);
    const before = { count: await locator.count(), visible: await locator.isVisible(), enabled: await locator.isEnabled(), receivesEvents: false };
    if (before.count === 1 && before.visible && before.enabled) {
      try { await locator.click({ trial: true }); before.receivesEvents = true; } catch {}
    }
    actionStates[id] = before;
  }
  requireCheck(report, 'usability', 'Group, RelatedExpand, and ListExpand are enabled and actionable for the saved legacy draft',
    actionIDs.every(id => actionStates[id].count === 1 && actionStates[id].visible && actionStates[id].enabled && actionStates[id].receivesEvents),
    { actions: actionStates, repeatedField: 'Patient.name[].given[]', repeated: givenCandidateSaved.repeated,
      projectionModes: givenCandidateSaved.projectionModes, projectionMode: givenColumn.source.field.projectionMode,
      rawArrayValues: Object.fromEntries(rawGivenByID) });

  // Exercise the two availability-gated actions without saving their editors.
  await workflow.action('open RelatedExpand from legacy row settings', page.getByTestId('construction-action-related-rows'),
    () => page.getByTestId('construction-action-related-rows').click(), {
      after: () => page.getByTestId('construction-related-expand-editor').waitFor({ state: 'visible' }),
    });
  await workflow.action('cancel RelatedExpand row editor', page.getByRole('button', { name: 'Close operation editor', exact: true }),
    () => page.getByRole('button', { name: 'Close operation editor', exact: true }).click(), {
      after: () => page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible' }),
    });
  await workflow.action('reopen legacy Patient row settings for ListExpand', page.getByTestId('construction-rows-settings-trigger'),
    () => page.getByTestId('construction-rows-settings-trigger').click(), {
      after: () => page.getByTestId('construction-action-expand-rows').waitFor({ state: 'visible' }),
    });
  await workflow.action('open ListExpand from legacy row settings', page.getByTestId('construction-action-expand-rows'),
    () => page.getByTestId('construction-action-expand-rows').click(), {
      after: () => page.getByTestId('construction-reshape-expand').waitFor({ state: 'visible' }),
    });
  await workflow.action('cancel ListExpand row editor', page.getByRole('button', { name: 'Close operation editor', exact: true }),
    () => page.getByRole('button', { name: 'Close operation editor', exact: true }).click(), {
      after: () => page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible' }),
    });
  const afterCancels = await readBuilder();
  const afterCancelsOwnerSummary = await readOwnedSummary();
  requireCheck(report, 'persistence', 'canceling RelatedExpand and ListExpand leaves the v9 draft version and digest unchanged',
    afterCancels.workspace.semanticsVersion === 9 && afterCancels.draftVersion === seeded.draftVersion
      && afterCancels.draftDigest === seeded.digest
      && afterCancels.catalog.generation === legacy.catalog.generation
      && afterCancels.catalog.authorizationScopeDigest === legacy.catalog.authorizationScopeDigest
      && afterCancelsOwnerSummary.explorerId === explorer && afterCancelsOwnerSummary.project === createdOwner.project,
    { semanticsVersion: afterCancels.workspace.semanticsVersion, draftVersion: afterCancels.draftVersion,
      digestMatched: afterCancels.draftDigest === seeded.digest });

  const groupProposalRequests = [];
  const observedExplorerPath = `${apiRoot}/authoring/v2/construction-proposals`;
  const requestListener = request => {
    try {
      const url = new URL(request.url());
      if (url.origin !== new URL(context.target.uiUrl).origin || url.pathname !== observedExplorerPath || request.method() !== 'POST') return;
      const body = request.postDataJSON();
      if (body?.outputId === table.output.id) groupProposalRequests.push(body);
    } catch {}
  };
  page.on('request', requestListener);
  await workflow.action('open legacy Patient row settings for Group', page.getByTestId('construction-rows-settings-trigger'),
    () => page.getByTestId('construction-rows-settings-trigger').click(), {
      after: () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible' }),
    });
  const group = page.getByTestId('construction-action-group-rows');
  await workflow.action('open Group proposal from the exact legacy draft', group, () => group.click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'),
  });
  const proposal = await evaluate(page, `(()=>{const panel=document.querySelector('[data-testid="construction-proposal-panel"]');const preview=document.querySelector('[data-testid="construction-proposal-preview"]');const table=preview?.querySelector('table');const headers=[...(table?.querySelectorAll('thead th')??[])].map(cell=>(cell.querySelector('span')?.innerText??cell.innerText).replace(/\\s+/g,' ').trim());const rows=[...(table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')??[])].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()));const countIndex=headers.findIndex(header=>header.toLowerCase()==='row count');const groupKeys=[...document.querySelectorAll('[data-testid="construction-reshape-group"] input[aria-label^="Group by"]')].filter(input=>input.checked).map(input=>input.getAttribute('aria-label'));return {status:panel?.dataset.proposalStatus,preview:preview?.dataset.previewStatus,summary:document.querySelector('select[aria-label="Summary 1"]')?.value,headers,rows,count:countIndex<0?null:rows[0]?.[countIndex],groupKeys}})()`);
  requireCheck(report, 'correctness', 'legacy Group action produces a ready proposal preview',
    proposal.status === 'ready' && proposal.preview === 'ready' && proposal.summary === 'COUNT_ROWS'
      && proposal.groupKeys.length === 0 && proposal.rows.length === 1 && proposal.count === String(patientIDs.length),
    { ...proposal, independentPatientIDs: patientIDs });
  const exactProposal = groupProposalRequests.at(-1);
  const groupStep = exactProposal?.candidateConstruction?.steps?.at(-1);
  requireCheck(report, 'correctness', 'Group proposal is pinned to the exact legacy project, snapshot, draft version, and digest',
    Boolean(exactProposal && exactProposal.outputId === table.output.id
      && exactProposal.snapshotToken === legacy.catalog.snapshotToken
      && exactProposal.expectedDraftVersion === seeded.draftVersion
      && exactProposal.expectedDraftDigest === seeded.digest
      && groupStep?.operation?.kind === 'GROUP' && groupStep.operation.group?.keys?.length === 0
      && groupStep.operation.group?.aggregates?.length === 1
      && groupStep.operation.group.aggregates[0].operation === 'COUNT_ROWS'),
    { outputIdMatched: exactProposal?.outputId === table.output.id,
      snapshotTokenMatched: exactProposal?.snapshotToken === legacy.catalog.snapshotToken,
      expectedDraftVersion: exactProposal?.expectedDraftVersion,
      expectedDraftDigestMatched: exactProposal?.expectedDraftDigest === seeded.digest,
      path: observedExplorerPath,
      scopeDigest: legacy.catalog.authorizationScopeDigest });
  await workflow.action('cancel legacy Group preview', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(), {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]')),
    });
  const afterGroupCancel = await readBuilder();
  const afterGroupCancelOwnerSummary = await readOwnedSummary();
  requireCheck(report, 'persistence', 'Cancel keeps the exact legacy Group draft digest',
    afterGroupCancel.workspace.semanticsVersion === 9 && afterGroupCancel.draftVersion === seeded.draftVersion
      && afterGroupCancel.draftDigest === seeded.digest
      && afterGroupCancel.catalog.generation === legacy.catalog.generation
      && afterGroupCancel.catalog.authorizationScopeDigest === legacy.catalog.authorizationScopeDigest
      && afterGroupCancelOwnerSummary.explorerId === explorer && afterGroupCancelOwnerSummary.project === createdOwner.project,
    { semanticsVersion: afterGroupCancel.workspace.semanticsVersion, draftVersion: afterGroupCancel.draftVersion,
      digestMatched: afterGroupCancel.draftDigest === seeded.digest });

  await workflow.action('reopen legacy Patient row settings for Group Apply', page.getByTestId('construction-rows-settings-trigger'),
    () => page.getByTestId('construction-rows-settings-trigger').click(), {
      after: () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible' }),
    });
  await workflow.action('reopen Group proposal for Apply', page.getByTestId('construction-action-group-rows'),
    () => page.getByTestId('construction-action-group-rows').click(), {
      after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'),
    });
  await workflow.action('Apply Group proposal from the exact legacy draft', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]')),
    });
  const applied = await readBuilder();
  assert(applied.draftVersion > seeded.draftVersion, 'Group Apply must persist a new draft version');
  assert(applied.draftDigest && applied.draftDigest !== seeded.digest, 'Group Apply must persist a new exact draft digest');
  report.target.appliedDraft = { draftVersion: applied.draftVersion, draftDigest: applied.draftDigest,
    semanticsVersion: applied.workspace.semanticsVersion, project, explorer,
    generation: applied.catalog.generation, authorizationScopeDigest: applied.catalog.authorizationScopeDigest };
  await workflow.action('reload applied Group draft', page.locator('body'),
    () => reload(page, `(()=>{if(!document.body.innerText.includes('DATASET WORKSPACE'))return false;const table=document.querySelector(${JSON.stringify(tableSelector)});if(!table)return false;const rows=[...table.querySelectorAll('[role="row"]')];const headers=[...(rows[0]?.querySelectorAll('[role="columnheader"]')??[])].map(cell=>cell.innerText.trim().toLowerCase());const countIndex=headers.indexOf('row count');const cells=[...(rows[1]?.querySelectorAll('[role="cell"]')??[])].map(cell=>cell.innerText.trim());return countIndex>=0&&rows.length===2&&cells[countIndex]==='${patientIDs.length}'})()`));
  const reloaded = await readBuilder();
  const finalOwnerSummary = await readOwnedSummary();
  exactWorkspace(reloaded, { generation: context.target.fixtureGeneration,
    scopeDigest: current.catalog.authorizationScopeDigest, title, outputId: table.output.id,
    draftVersion: applied.draftVersion, draftDigest: applied.draftDigest });
  const reloadedGroupGrid = await evaluate(page, `(()=>{const table=document.querySelector(${JSON.stringify(tableSelector)});if(!table)return {headers:[],rows:[]};const rows=[...table.querySelectorAll('[role="row"]')];return {headers:[...(rows[0]?.querySelectorAll('[role="columnheader"]')??[])].map(cell=>cell.innerText.trim()),rows:rows.slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim()))}})()`);
  const reloadedCountIndex = reloadedGroupGrid.headers.findIndex(header => header.toLowerCase() === 'row count');
  const persistedDocument = reloaded.workspace.documents.find(document => document.output.id === table.output.id);
  const persistedSteps = persistedDocument?.construction?.steps ?? [];
  const persistedGroupSteps = persistedSteps.filter(step => step.operation?.kind === 'GROUP');
  const persistedGroup = persistedGroupSteps[0];
  const persistedGroupOperation = persistedGroup?.operation;
  const persistedGroupBody = persistedGroupOperation?.group;
  const persistedGroupKeysOmitted = Boolean(persistedGroupBody)
    && !Object.prototype.hasOwnProperty.call(persistedGroupBody, 'keys');
  const persistedGroupKeysEmptyArray = Array.isArray(persistedGroupBody?.keys)
    && persistedGroupBody.keys.length === 0;
  const persistedGroupHasDefaultEmptyKeys = Boolean(persistedGroupBody)
    && (persistedGroupKeysOmitted || persistedGroupKeysEmptyArray);
  const persistedGroupAggregates = Array.isArray(persistedGroupBody?.aggregates)
    ? persistedGroupBody.aggregates : [];
  const persistedGroupHasSingleCountRowsAggregate = persistedGroupAggregates.length === 1
    && persistedGroupAggregates[0]?.operation === 'COUNT_ROWS'
    && typeof persistedGroupAggregates[0]?.outputColumnId === 'string'
    && persistedGroupAggregates[0].outputColumnId.length > 0;
  const appliedDraftVersionMatches = reloaded.draftVersion === applied.draftVersion;
  const appliedDraftDigestMatches = reloaded.draftDigest === applied.draftDigest;
  const appliedSemanticsVersionMatches = reloaded.workspace.semanticsVersion === applied.workspace.semanticsVersion;
  const appliedOwnerMatches = finalOwnerSummary.project === createdOwner.project
    && finalOwnerSummary.explorerId === explorer;
  const appliedScopeMatches = reloaded.catalog.generation === context.target.fixtureGeneration
    && reloaded.catalog.authorizationScopeDigest === current.catalog.authorizationScopeDigest;
  const persistedGroupShapeMatches = persistedGroupSteps.length === 1
    && persistedGroupOperation?.kind === 'GROUP'
    && persistedGroupHasDefaultEmptyKeys
    && persistedGroupHasSingleCountRowsAggregate;
  requireCheck(report, 'persistence', 'applied Group draft survives Builder reload with exact digest and scope',
    appliedDraftVersionMatches && appliedDraftDigestMatches && appliedSemanticsVersionMatches
      && appliedOwnerMatches && appliedScopeMatches && persistedGroupShapeMatches,
    { draftVersion: reloaded.draftVersion, appliedDraftVersion: applied.draftVersion,
      draftVersionMatches: appliedDraftVersionMatches, draftDigest: reloaded.draftDigest,
      appliedDraftDigest: applied.draftDigest, draftDigestMatches: appliedDraftDigestMatches,
      semanticsVersion: reloaded.workspace.semanticsVersion,
      appliedSemanticsVersion: applied.workspace.semanticsVersion,
      semanticsVersionMatches: appliedSemanticsVersionMatches,
      ownerMatches: appliedOwnerMatches, rawFixtureProject: project,
      apiProject: finalOwnerSummary.project, explorer: finalOwnerSummary.explorerId,
      generation: reloaded.catalog.generation, expectedGeneration: context.target.fixtureGeneration,
      authorizationScopeDigest: reloaded.catalog.authorizationScopeDigest,
      scopeMatches: appliedScopeMatches,
      persistedGroupStepCount: persistedGroupSteps.length,
      persistedGroupKind: persistedGroupOperation?.kind ?? null,
      persistedGroupObjectPresent: Boolean(persistedGroupBody),
      persistedGroupKeysOmitted: persistedGroupKeysOmitted,
      persistedGroupKeysEmptyArray: persistedGroupKeysEmptyArray,
      persistedGroupHasDefaultEmptyKeys: persistedGroupHasDefaultEmptyKeys,
      persistedGroupAggregateCount: persistedGroupAggregates.length,
      persistedGroupAggregateOperation: persistedGroupAggregates[0]?.operation ?? null,
      persistedGroupAggregateOutputColumnIdPresent: typeof persistedGroupAggregates[0]?.outputColumnId === 'string'
        && persistedGroupAggregates[0].outputColumnId.length > 0,
      persistedGroupHasSingleCountRowsAggregate: persistedGroupHasSingleCountRowsAggregate });
  requireCheck(report, 'correctness', 'reloaded Group result visibly renders literal COUNT_ROWS 2',
    reloadedCountIndex >= 0 && reloadedGroupGrid.rows.length === 1
      && reloadedGroupGrid.rows[0][reloadedCountIndex] === String(patientIDs.length),
    { headers: reloadedGroupGrid.headers, rows: reloadedGroupGrid.rows, expectedCount: patientIDs.length });
  const scopedConflicts = report.network.filter(record => record.status === 409 && record.rawURL
    && (() => { try { const url = new URL(record.rawURL); return url.origin === expectedUIOrigin && url.pathname.startsWith(apiRoot); } catch { return false; } })());
  requireCheck(report, 'correctness', 'native row-action lifecycle issued no scoped draft conflict', scopedConflicts.length === 0,
    { scoped409Responses: scopedConflicts.map(record => ({ status: record.status, method: record.method,
      url: record.url, diagnostic: record.responseBody?.body })) });
  page.off('request', requestListener);
};
