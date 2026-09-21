import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, utimesSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { AUTHORING_SEMANTICS_VERSION, assertJ05ArtifactIdentity, assertJ05ArtifactRows, bootstrapSeedPlan, bootstrapWorkspaceNeedsSeed, canonicalProjectID, collectJ01SemanticConceptPages, commandEnvironment, createDevSession, createVerificationReport, expectedFixtureRelatedValue, fixtureSourceDigest, generatedJ01ConceptNDJSON, generationLoadDisposition, graphQLRowsRequest, inspectJ05ArtifactPackage, j01ColumnIdentitySnapshot, j01ConstructionChoiceCommandIdentities, j01SemanticInventoryRequest, j05ArtifactIdentityIsCurrent, normalizeJ05LogicalValue, sourceMountMatches } from './loom-dev.mjs';

const j05Identity = {
  project: 'loom_dev_j05',
  datasetGeneration: 'fixture-j05',
  receiptId: 'receipt-j05',
  executionId: 'execution-j05',
  outputId: 'patients',
  revisionId: 'revision-j05',
  schemaDigest: 'schema-j05',
  outputContractDigest: 'contract-j05',
};

const j05ArtifactPackage = ({ format, columns, data, rowCount, rowIdentity = { key: '__loom_row_id', sourceResourceType: 'Patient', sourceIdColumn: columns[0].name } }) => {
  const descriptor = {
    version: 1,
    outputKey: j05Identity.outputId,
    receiptFormatVersion: 2,
    compilerContractVersion: 'compiler-v2',
    recipeSchemaVersion: 1,
    translationVersion: 'translation-v1',
    sourceGeneration: j05Identity.datasetGeneration,
    publishedSchemaDigest: j05Identity.schemaDigest,
    resolvedSchemaDigest: 'resolved-schema-j05',
    outputContractDigest: j05Identity.outputContractDigest,
    rowGrain: 'patient',
    rowMultiplication: 'none',
    rowIdentity,
    columns,
  };
  const members = new Map([
    [format === 'CSV' ? 'data.csv' : 'data.jsonl', Buffer.from(data)],
    ['schema.json', Buffer.from(JSON.stringify({ format, columns, nullEncoding: '\\N', arrayEncoding: format === 'JSONL' ? 'native' : 'json' }))],
    ['provenance.json', Buffer.from(JSON.stringify({ project: j05Identity.project, explorerId: 'explorer-j05', ...j05Identity }))],
    ['quality.json', Buffer.from(JSON.stringify({ status: 'COMPLETE', contributors: [{ resourceType: 'Patient', sourcePath: columns[0].sourcePath ?? 'id' }] }))],
    ['README.md', Buffer.from('J05 fixture artifact')],
  ]);
  const checksums = [...members].map(([name, bytes]) => ({
    name,
    bytes: bytes.length,
    sha256: createHash('sha256').update(bytes).digest('hex'),
  }));
  members.set('manifest.json', Buffer.from(JSON.stringify({
    version: 2,
    identity: j05Identity,
    descriptor,
    format,
    selection: { recipe: 'patients', datasetGeneration: j05Identity.datasetGeneration },
    interpretations: [],
    rows: rowCount,
    features: columns.length,
    nullEncoding: '\\N',
    arrayEncoding: format === 'JSONL' ? 'native' : 'json',
    members: checksums,
  })));
  return members;
};

test('development evidence compares canonical project identities', () => {
  assert.equal(canonicalProjectID('loom_dev_verify_run-1234'), 'loom_dev_verify_run/1234');
  assert.equal(canonicalProjectID('study/project'), 'study/project');
  assert.equal(canonicalProjectID('project-a'), 'project-a');
});

test('development commands use the current Go authoring semantics version', () => {
  const source = readFileSync(join(process.cwd(), 'internal/explorer/authoringv2/types.go'), 'utf8');
  const match = source.match(/CurrentSemanticsVersion\s*=\s*(\d+)/);
  assert.ok(match, 'Go authoring semantics version is missing');
  assert.equal(AUTHORING_SEMANTICS_VERSION, Number(match[1]));

  const uiSource = readFileSync(join(process.cwd(), 'ui/packages/loom-ui/src/types.ts'), 'utf8');
  const uiMatch = uiSource.match(/EXPLORER_AUTHORING_SEMANTICS_VERSION\s*=\s*(\d+)/);
  assert.ok(uiMatch, 'UI authoring semantics version is missing');
  assert.equal(Number(uiMatch[1]), Number(match[1]));
});

test('fixture FIRST expectation follows independently observed storage-key ordering', () => {
  assert.equal(expectedFixtureRelatedValue('loom_dev_verify_mu4ctgo1-4a680895', 'fixture-v1'), 172.5);
  assert.equal(expectedFixtureRelatedValue('loom_dev_verify_mu4d6n33-4abffd57', 'fixture-v1'), 180);
});

test('development session defaults to isolated names, ports, and fixture', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  try {
    const target = createDevSession({ LOOM_DEV_PORT_REGISTRY: join(registryRoot, 'ports.json') }, process.cwd());
    assert.match(target.composeProject, /^loom-dev-[a-f0-9]{12}$/);
    assert.match(target.fixtureProject, /^loom_dev_[a-f0-9]{12}$/);
    assert.equal(target.fixtureGeneration, 'fixture-v1');
    assert.ok(target.apiPort >= 8180 && target.apiPort < 30000);
    assert.ok(target.uiPort >= 30000);
    assert.match(target.fixtureDir, /testdata\/devloop-fixture$/);
    assert.notEqual(target.composeProject, 'loom-demo');
  } finally {
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('development session accepts a read-only external FHIR fixture directory', () => {
  const root = mkdtempSync(join(tmpdir(), 'loom-dev-external-source-'));
  const fixture = mkdtempSync(join(tmpdir(), 'loom-dev-external-fixture-'));
  try {
    writeFileSync(join(root, 'go.mod'), 'module example.test/loom\n');
    mkdirSync(join(root, 'testdata/devloop-fixture'), { recursive: true });
    for (const name of ['Patient.ndjson', 'Observation.ndjson']) writeFileSync(join(fixture, name), '{}\n');
    const target = createDevSession({
      LOOM_DEV_SOURCE_ROOT: root,
      LOOM_DEV_FIXTURE_DIR: fixture,
      LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-external',
      LOOM_DEV_PROJECT: 'loom_dev_external',
      LOOM_DEV_API_PORT: '18180',
      LOOM_DEV_UI_PORT: '33000',
      LOOM_DEV_FIXTURE_TIMEOUT_MS: '900000',
      LOOM_DEV_ARTIFACTS: join(root, '.artifacts'),
    }, root);
    assert.equal(target.fixtureDir, fixture);
    assert.equal(target.fixtureLoadTimeout, 900000);
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('generation load polling distinguishes durable completion from failure', () => {
  assert.equal(generationLoadDisposition({ state: 'LOADING' }), 'loading');
  assert.equal(generationLoadDisposition({ state: 'STAGED' }), 'ready');
  assert.equal(generationLoadDisposition({ state: 'READY' }), 'ready');
  assert.equal(generationLoadDisposition({ state: 'FAILED' }), 'failed');
  assert.equal(generationLoadDisposition({}), 'unknown');
});

test('bootstrap seeding is limited to a new or interrupted default draft', () => {
  const catalog = {
    nodes: [{ nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true }],
    candidates: [],
  };
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'NEW', draftVersion: 0, workspace: null, catalog }), true);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 1, workspace: { documents: [{ output: { title: 'Patients' }, columns: [] }] }, catalog }), true);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 2, workspace: { documents: [] }, catalog }), false);
  assert.equal(bootstrapWorkspaceNeedsSeed({ lifecycleState: 'READY', draftVersion: 2, workspace: { documents: [{ output: { title: 'Patients' }, columns: [] }] }, catalog }), false);
});

test('bootstrap seed plan uses the current Patient catalog, never transient project identities', () => {
  const state = {
    lifecycleState: 'NEW',
    draftVersion: 0,
    workspace: null,
    catalog: {
      nodes: [
        { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true },
        { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true },
      ],
      candidates: [
        { candidateId: 'patient-id-current', nodeId: 'patient', fieldPath: 'root.id', defaultProjectionMode: 'SCALAR', label: 'id' },
        { candidateId: 'patient-family-current', nodeId: 'patient', fieldPath: 'root.name[].family', defaultProjectionMode: 'INDEXED', label: 'name[].family' },
        { candidateId: 'patient-gender-current', nodeId: 'patient', fieldPath: 'gender', defaultProjectionMode: 'SCALAR', label: 'gender' },
        { candidateId: 'observation-id-current', nodeId: 'observation', fieldPath: 'id', defaultProjectionMode: 'SCALAR', label: 'id' },
      ],
    },
  };
  const plan = bootstrapSeedPlan(state);
  assert.equal(plan.createTable, true);
  assert.equal(plan.rootNodeId, 'patient');
  assert.deepEqual(plan.candidates.map((candidate) => candidate.candidateId), [
    'patient-id-current',
    'patient-family-current',
    'patient-gender-current',
  ]);
  assert.equal(JSON.stringify(plan).includes('loom_dev_verify_'), false);
});

test('default development sessions separate worktree identities and ports', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  const registry = join(registryRoot, 'ports.json');
  const roots = [mkdtempSync(join(tmpdir(), 'loom-dev-session-a-')), mkdtempSync(join(tmpdir(), 'loom-dev-session-b-'))];
  try {
    for (const root of roots) {
      mkdirSync(join(root, 'testdata/devloop-fixture'), { recursive: true });
      writeFileSync(join(root, 'go.mod'), 'module example.test\n');
      for (const file of ['Patient.ndjson', 'Observation.ndjson', 'recipe.json']) {
        writeFileSync(join(root, 'testdata/devloop-fixture', file), '{}\n');
      }
    }
    const env = { LOOM_DEV_PORT_REGISTRY: registry };
    const first = createDevSession(env, roots[0]);
    const second = createDevSession(env, roots[1]);
    assert.notEqual(first.composeProject, second.composeProject);
    assert.notEqual(first.fixtureProject, second.fixtureProject);
    assert.notEqual(first.apiPort, second.apiPort);
    assert.notEqual(first.uiPort, second.uiPort);
    assert.notEqual(first.artifacts, second.artifacts);
  } finally {
    for (const root of roots) rmSync(root, { recursive: true, force: true });
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('derived ports are forwarded to Compose', () => {
  const target = createDevSession({}, process.cwd());
  const env = commandEnvironment(target);
  assert.equal(env.LOOM_DEV_SOURCE_ROOT, target.sourceRoot);
  assert.equal(env.LOOM_DEV_COMPOSE_PROJECT, target.composeProject);
  assert.equal(env.LOOM_DEV_PROJECT, target.fixtureProject);
  assert.equal(env.LOOM_DEV_GENERATION, target.fixtureGeneration);
  assert.equal(env.LOOM_DEV_HOST, target.host);
  assert.equal(env.LOOM_DEV_API_PORT, String(target.apiPort));
  assert.equal(env.LOOM_DEV_UI_PORT, String(target.uiPort));
  assert.equal(env.LOOM_DEV_EXPLORER, 'loom-dev-bootstrap');
  assert.match(target.populationMappingCursorSecret, /^[a-f0-9]{64}$/);
  assert.equal(env.LOOM_POPULATION_MAPPING_CURSOR_SECRET, target.populationMappingCursorSecret);
});

test('development cursor secret can be explicitly supplied', () => {
  const target = createDevSession({ LOOM_POPULATION_MAPPING_CURSOR_SECRET: 'local-session-cursor-secret' }, process.cwd());
  assert.equal(target.populationMappingCursorSecret, 'local-session-cursor-secret');
  assert.equal(commandEnvironment(target).LOOM_POPULATION_MAPPING_CURSOR_SECRET, 'local-session-cursor-secret');
});

test('explicit ports bypass an unusable port registry', () => {
  const target = createDevSession({
    LOOM_DEV_API_PORT: '8281',
    LOOM_DEV_UI_PORT: '3281',
    LOOM_DEV_PORT_REGISTRY: '/private/tmp/loom-dev-unusable-registry/ports.json',
  }, process.cwd());
  assert.equal(target.apiPort, 8281);
  assert.equal(target.uiPort, 3281);
});

test('stale port registry locks are recoverable', () => {
  const registryRoot = mkdtempSync(join(tmpdir(), 'loom-dev-registry-'));
  const registry = join(registryRoot, 'ports.json');
  const lock = `${registry}.lock`;
  try {
    writeFileSync(lock, 'stale');
    const staleTime = new Date(Date.now() - 60000);
    utimesSync(lock, staleTime, staleTime);
    const target = createDevSession({ LOOM_DEV_PORT_REGISTRY: registry }, process.cwd());
    assert.match(target.composeProject, /^loom-dev-[a-f0-9]{12}$/);
    assert.equal(target.apiPort, 8180);
  } finally {
    rmSync(registryRoot, { recursive: true, force: true });
  }
});

test('development session rejects canonical Compose and data targets', () => {
  assert.throws(
    () => createDevSession({ LOOM_DEV_COMPOSE_PROJECT: 'research-stack' }, process.cwd()),
    /must use the loom-dev namespace/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_PROJECT: 'another_research_dataset' }, process.cwd()),
    /must use the loom_dev_ namespace/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_COMPOSE_PROJECT: 'loom-demo' }, process.cwd()),
    /must not use the canonical loom-demo project/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_PROJECT: 'NCPI_ACCEPTANCE' }, process.cwd()),
    /must not target the canonical NCPI_ACCEPTANCE project/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_API_URL: 'http://example.test:8080' }, process.cwd()),
    /must point to this session's loopback port/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_HOST: '0.0.0.0' }, process.cwd()),
    /must be a loopback host/,
  );
});

test('verification report starts with an explicit build state and target ownership', () => {
  const target = createDevSession({}, process.cwd());
  const report = createVerificationReport(target);
  assert.equal(report.status, 'building');
  assert.equal(report.scenario, 'builder-preview-publish-viewer-filter-export');
  assert.equal(report.target.project, target.fixtureProject);
  assert.deepEqual(report.assertions, []);
  assert.deepEqual(report.timings, {});
  assert.deepEqual(report.evidencePaths, []);
  assert.deepEqual(report.limitations, []);
});

test('fixture source digest is stable across file enumeration order and excludes non-source files', () => {
  const fixture = mkdtempSync(join(tmpdir(), 'loom-dev-fixture-digest-'));
  try {
    writeFileSync(join(fixture, 'Observation.ndjson'), '{"id":"observation"}\n');
    writeFileSync(join(fixture, 'Patient.ndjson'), '{"id":"patient"}\n');
    writeFileSync(join(fixture, 'recipe.json'), '{"not":"source"}\n');
    const first = fixtureSourceDigest(fixture);
    writeFileSync(join(fixture, 'README.txt'), 'ignored\n');
    assert.equal(fixtureSourceDigest(fixture), first);
    writeFileSync(join(fixture, 'Patient.ndjson'), '{"id":"changed"}\n');
    assert.notEqual(fixtureSourceDigest(fixture), first);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('J01 fixture generates 1,000 distinct scalar concepts and hostile owner records', () => {
  const fixture = join(process.cwd(), 'testdata/devloop-fixture');
  const generated = generatedJ01ConceptNDJSON(fixture)?.trim().split('\n').map((line) => JSON.parse(line));
  assert.equal(generated?.length, 1000);
  assert.deepEqual(generated?.[0], {
    resourceType: 'Observation',
    id: 'dev-j01-concept-0000',
    status: 'final',
    code: { coding: [{ system: 'urn:loom:j01:catalog', code: 'concept-0000', display: 'J01 concept 0000' }] },
    valueInteger: 0,
  });
  assert.equal(generated?.at(-1)?.code.coding[0].code, 'concept-0999');
  assert.equal(generated?.at(-1)?.valueInteger, 999);

  const paired = readFileSync(join(fixture, 'Observation.ndjson'), 'utf8')
    .trim().split('\n').map((line) => JSON.parse(line))
    .find((resource) => resource.id === 'dev-pair-001');
  assert.equal(paired.component[0].unmodeledSignal.flag, true);
  assert.equal(paired.component[0].extension[0].extension[0].valueString, 'nested-owner');
  assert.equal(paired.component[2].code.coding[0].code, 'shared');
  assert.equal(paired.component[2].valueString, undefined);
  assert.equal(paired.component[2]._valueString.extension[0].valueBoolean, true);
});

test('J01 fixture generation rejects invalid counts and incomplete identity metadata', () => {
  const fixture = mkdtempSync(join(tmpdir(), 'loom-j01-invalid-fixture-'));
  try {
    const specPath = join(fixture, 'j01-concepts.fixture.json');
    writeFileSync(specPath, JSON.stringify({ count: 0, system: 'urn:test', codePrefix: 'code-', displayPrefix: 'Concept' }));
    assert.throws(() => generatedJ01ConceptNDJSON(fixture), /count must be an integer/);
    writeFileSync(specPath, JSON.stringify({ count: 2, codePrefix: 'code-', displayPrefix: 'Concept' }));
    assert.throws(() => generatedJ01ConceptNDJSON(fixture), /requires system, codePrefix, and displayPrefix/);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('J01 semantic inventory requests use the saved snapshot and omit an absent cursor', () => {
  assert.deepEqual(j01SemanticInventoryRequest({
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    resourceType: 'Observation',
    query: 'J01 concept',
  }), {
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    resourceType: 'Observation',
    query: 'J01 concept',
    limit: 50,
  });
  assert.deepEqual(j01SemanticInventoryRequest({
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    cursor: 'cursor-2',
  }), {
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    cursor: 'cursor-2',
    limit: 50,
  });
  assert.throws(() => j01SemanticInventoryRequest({ rowRoot: 'observation-node' }), /requires a catalog snapshot/);
});

test('J01 semantic pagination retains one context identity and finds each expected code exactly once', async () => {
  const fixture = {
    count: 5,
    system: 'urn:loom:j01:catalog',
    codePrefix: 'concept-',
    displayPrefix: 'J01 concept',
  };
  const pages = [
    ['0000', '0001'],
    ['0002', '0003'],
    ['0004'],
  ];
  const requests = [];
  const inventory = await collectJ01SemanticConceptPages(async (body) => {
    requests.push(body);
    const pageIndex = requests.length - 1;
    return {
      response: { ok: true, status: 200 },
      value: {
        contextToken: 'ctx-1',
        buildId: 'build-1',
        state: 'complete',
        sourceAvailability: 'unknown',
        entries: pages[pageIndex].map((suffix) => ({
          conceptId: `concept-id-${suffix}`,
          bindingId: `binding-id-${suffix}`,
          resourceType: 'Observation',
          system: fixture.system,
          code: `${fixture.codePrefix}${suffix}`,
          display: `${fixture.displayPrefix} ${suffix}`,
        })),
        ...(pageIndex < pages.length - 1 ? { nextCursor: `cursor-${pageIndex + 1}` } : {}),
      },
    };
  }, {
    snapshotToken: 'snapshot-1',
    rowRoot: 'observation-node',
    resourceType: 'Observation',
    query: fixture.displayPrefix,
  }, fixture);

  assert.equal(inventory.count, 5);
  assert.equal(inventory.countBasis, 'exact-paginated');
  assert.equal(inventory.pages.length, 3);
  assert.equal(inventory.contextToken, 'ctx-1');
  assert.equal(inventory.buildId, 'build-1');
  assert.equal(inventory.sourceAvailability, 'unknown');
  assert.deepEqual(requests.map((request) => request.cursor), [undefined, 'cursor-1', 'cursor-2']);
  assert.deepEqual(inventory.entries.map((entry) => entry.code).sort(), [
    'concept-0000', 'concept-0001', 'concept-0002', 'concept-0003', 'concept-0004',
  ]);
});

test('J01 semantic pagination rejects failure, identity drift, duplicate pages, and examples', async (t) => {
  const fixture = { count: 2, system: 'urn:loom:j01:catalog', codePrefix: 'concept-', displayPrefix: 'J01 concept' };
  const request = { snapshotToken: 'snapshot-1', rowRoot: 'observation-node', query: fixture.displayPrefix };
  const entry = (suffix, extra = {}) => ({
    conceptId: `concept-id-${suffix}`,
    bindingId: `binding-id-${suffix}`,
    resourceType: 'Observation',
    system: fixture.system,
    code: `concept-${suffix}`,
    display: `J01 concept ${suffix}`,
    ...extra,
  });
  const response = (entries, nextCursor, overrides = {}) => ({
    response: { ok: true, status: 200 },
    value: {
      contextToken: 'ctx-1',
      buildId: 'build-1',
      state: 'complete',
      sourceAvailability: 'verified',
      entries,
      ...(nextCursor ? { nextCursor } : {}),
      ...overrides,
    },
  });

  await t.test('HTTP failure', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => ({ response: { ok: false, status: 503 }, value: {} }), request, fixture), /HTTP 503/);
  });
  await t.test('catalog context changes between pages', async () => {
    let page = 0;
    await assert.rejects(collectJ01SemanticConceptPages(async () => {
      page += 1;
      return page === 1
        ? response([entry('0000')], 'cursor-1')
        : response([entry('0001')], undefined, { contextToken: 'ctx-2' });
    }, request, fixture), /changed its context, build identity, or source availability/);
  });
  await t.test('source availability changes between pages', async () => {
    let page = 0;
    await assert.rejects(collectJ01SemanticConceptPages(async () => {
      page += 1;
      return page === 1
        ? response([entry('0000')], 'cursor-1')
        : response([entry('0001')], undefined, { sourceAvailability: 'unproven' });
    }, request, fixture), /changed its context, build identity, or source availability/);
  });
  await t.test('cursor repeats', async () => {
    let page = 0;
    await assert.rejects(collectJ01SemanticConceptPages(async () => {
      page += 1;
      return page === 1
        ? response([entry('0000')], 'cursor-1')
        : response([], 'cursor-1');
    }, request, fixture), /repeated a pagination cursor/);
  });
  await t.test('duplicate generated code', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => response([entry('0000'), entry('0000')]), request, fixture), /repeated concept identity/);
  });
  await t.test('example values leak into browse results', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => response([entry('0000', { examples: ['private value'] })]), request, fixture), /exposed example values/);
  });
  await t.test('global inventory count leaks into browse response', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => response([entry('0000'), entry('0001')], undefined, { totalCount: 2 }), request, fixture), /global count field/);
  });
  await t.test('missing expected concept identity', async () => {
    await assert.rejects(collectJ01SemanticConceptPages(async () => response([entry('0000')]), request, fixture), /returned 1 of 2 expected concepts/);
  });
});

test('J01 command evidence accepts only distinct compiler choice IDs for the selected table', () => {
  const body = {
    commandId: 'cmd-1',
    snapshotToken: 'snapshot-1',
    commands: [
      { type: 'APPLY_CONSTRUCTION_CHOICE', outputId: 'table-1', constructionChoice: { choiceId: 'choice-id', form: 'VALUE' }, title: 'valueInteger' },
      { type: 'APPLY_CONSTRUCTION_CHOICE', outputId: 'table-1', constructionChoice: { choiceId: 'choice-records', form: 'OWNER_RECORDS' }, title: 'shared' },
    ],
  };
  assert.deepEqual(j01ConstructionChoiceCommandIdentities(body, 'table-1'), [
    { choiceId: 'choice-id', form: 'VALUE', outputId: 'table-1', title: 'valueInteger' },
    { choiceId: 'choice-records', form: 'OWNER_RECORDS', outputId: 'table-1', title: 'shared' },
  ]);
  assert.throws(() => j01ConstructionChoiceCommandIdentities({ ...body, commands: [{ ...body.commands[0], fieldPath: 'valueInteger' }] }, 'table-1'), /client-derived source field/);
  assert.throws(() => j01ConstructionChoiceCommandIdentities(body, 'table-2'), /different table/);
  assert.throws(() => j01ConstructionChoiceCommandIdentities({ ...body, commands: [body.commands[0], body.commands[0]] }, 'table-1'), /distinct selected choices/);
  assert.throws(() => j01ConstructionChoiceCommandIdentities({ ...body, commands: [{ ...body.commands[0], constructionChoice: { choiceId: 'choice-id', form: 'VALUE', fieldPath: 'valueInteger' } }] }, 'table-1'), /only a compiler-issued choice identity and form/);
});

test('J01 reload identity snapshot retains exactly the renamed, reordered saved sources', () => {
  const state = {
    workspace: { documents: [{ output: { id: 'table-1' }, columns: [
      { column: 'col-id', label: 'Observation identifier', table: { order: 2 }, source: { kind: 'field', field: { path: 'root.id' } } },
      { column: 'col-int', label: 'Integer value', table: { order: 0 }, source: { kind: 'field', field: { path: 'valueInteger' } } },
      { column: 'col-owner', label: 'Study A records', table: { order: 1 }, source: { kind: 'ownerRecords', ownerRecords: { key: { system: 'urn:study:A', code: 'shared' }, binding: { ownerPath: 'component[]', valuePath: 'valueQuantity.value' } } } },
    ] }] },
  };
  const expected = [
    { columnId: 'col-int', label: 'Integer value', order: 0, source: { kind: 'field', path: 'valueInteger' } },
    { columnId: 'col-owner', label: 'Study A records', order: 1, source: { kind: 'ownerRecords', system: 'urn:study:A', code: 'shared', ownerPath: 'component[]', valuePath: 'valueQuantity.value' } },
    { columnId: 'col-id', label: 'Observation identifier', order: 2, source: { kind: 'field', path: 'id' } },
  ];
  assert.deepEqual(j01ColumnIdentitySnapshot(state, 'table-1'), expected);
  assert.throws(() => j01ColumnIdentitySnapshot({ workspace: { documents: [{ output: { id: 'table-1' }, columns: state.workspace.documents[0].columns.slice(1) }] } }, 'table-1'), /exactly three selected columns/);
  assert.throws(() => j01ColumnIdentitySnapshot({ workspace: { documents: [{ output: { id: 'table-1' }, columns: state.workspace.documents[0].columns.map((column) => column.column === 'col-id' ? { ...column, table: { order: 1 } } : column) }] } }, 'table-1'), /contiguous saved column positions/);
});

test('J02 fixture contains the documented five-edge FHIR reference chain and distinct report route', () => {
  const fixture = join(process.cwd(), 'testdata/devloop-fixture');
  const readResources = (filename) => readFileSync(join(fixture, filename), 'utf8')
    .trim()
    .split('\n')
    .map((line) => JSON.parse(line));
  const patient = readResources('Patient.ndjson').find((item) => item.id === 'dev-patient-001');
  const group = readResources('Group.ndjson').find((item) => item.id === 'dev-j02-group');
  const specimen = readResources('Specimen.ndjson').find((item) => item.id === 'dev-j02-specimen');
  const observation = readResources('Observation.ndjson').find((item) => item.id === 'dev-observation-001');
  const report = readResources('DiagnosticReport.ndjson').find((item) => item.id === 'dev-j02-report');
  const study = readResources('ResearchStudy.ndjson').find((item) => item.id === 'dev-j02-study');
  assert.ok(patient);
  assert.deepEqual(group?.member.map((member) => member.entity.reference), ['Patient/dev-patient-001']);
  assert.equal(specimen?.subject.reference, 'Group/dev-j02-group');
  assert.equal(observation?.specimen.reference, 'Specimen/dev-j02-specimen');
  assert.deepEqual(report?.result.map((result) => result.reference), ['Observation/dev-observation-001']);
  assert.equal(report?.subject.reference, 'Patient/dev-patient-001');
  assert.deepEqual(study?.result.map((result) => result.reference), ['DiagnosticReport/dev-j02-report']);
  assert.equal(study?.status, 'active');
  assert.equal(study?.title, 'J02 route study');

  const generated = readFileSync(join(process.cwd(), 'generated/fhirschema/generated.go'), 'utf8');
  for (const traversal of [
    'Patient|member_entity_Patient|Group',
    'Group|subject_Group|Specimen',
    'Specimen|specimen_Specimen|Observation',
    'Observation|result|DiagnosticReport',
    'DiagnosticReport|result_DiagnosticReport|ResearchStudy',
    'Patient|subject_Patient|DiagnosticReport',
  ]) assert.ok(generated.includes(`"${traversal}"`), `generated schema lacks ${traversal}`);

  const readme = readFileSync(join(fixture, 'README.md'), 'utf8');
  assert.match(readme, /Patient → Group → Specimen → Observation → DiagnosticReport →\s+ResearchStudy/);
  assert.match(readme, /semantically distinct routes/);
  assert.match(readme, /ResearchStudy\.title` is the\s+literal `J02 route study`/);
});

test('Docker Desktop host mount normalization accepts only this checkout', () => {
  const checkout = '/private/tmp/loom-devloop-impl.cDPSXd';
  assert.equal(sourceMountMatches(`${checkout}/cmd`, `${checkout}/cmd`), true);
  assert.equal(sourceMountMatches(`/host_mnt${checkout}/cmd`, `${checkout}/cmd`), true);
  assert.equal(sourceMountMatches('/host_mnt/private/tmp/another-checkout/cmd', `${checkout}/cmd`), false);
});

test('dataframe verification uses the versioned frontend output contract', () => {
  const request = graphQLRowsRequest(
    { fixtureProject: 'loom_dev_contract' },
    { recipe: 'cohort', translationVersion: 'v1', output: 'patients' },
    ['id', 'status'],
    [{ column: 'status', op: 'IN', value: ['active'] }],
  );
  assert.equal(
    request.query,
    'query VerifyRows($input: DataframeRowsInput!) { dataframeRows(input: $input) { materialization { id name revision projectId datasetGeneration state rowCount selector { recipe translationVersion output } } columns rows rowIds totalCount pageInfo { hasNextPage endCursor } } }',
  );
  assert.deepEqual(request.variables.input, {
    projectId: 'loom_dev_contract',
    selector: { recipe: 'cohort', translationVersion: 'v1', output: 'patients' },
    columns: ['id', 'status'],
    filters: [{ column: 'status', op: 'IN', value: ['active'] }],
    first: 25,
  });
});

test('J05 CSV artifact inspection preserves typed values, nulls, empty strings, and literal null markers', () => {
  const columns = [
    { name: 'patient_id', outputKey: 'patientId', logicalType: 'string', shape: 'scalar', sourcePath: 'id' },
    { name: 'age', outputKey: 'age', logicalType: 'integer', shape: 'scalar', nullable: false },
    { name: 'note', outputKey: 'note', logicalType: 'string', shape: 'scalar', nullable: true },
  ];
  const artifact = inspectJ05ArtifactPackage(j05ArtifactPackage({
    format: 'CSV',
    columns,
    rowCount: 3,
    data: 'patient_id,age,note\npatient-a,0,\\N\npatient-b,42,"\\N"\npatient-c,7,""\n',
  }));
  assert.equal(artifact.dataName, 'data.csv');
  assert.deepEqual(artifact.rows.map((row) => row.values), [
    { patient_id: 'patient-a', age: 0, note: null },
    { patient_id: 'patient-b', age: 42, note: '\\N' },
    { patient_id: 'patient-c', age: 7, note: '' },
  ]);
  assert.doesNotThrow(() => assertJ05ArtifactRows(artifact, [...artifact.rows].reverse()));
  assertJ05ArtifactIdentity(artifact, j05Identity);
});

test('J05 JSONL artifact inspection selects data.jsonl and preserves structured row IDs and native arrays', () => {
  const columns = [
    { name: 'patient_id', outputKey: 'patientId', logicalType: 'string', shape: 'scalar', sourcePath: 'id' },
    { name: 'family', outputKey: 'familyNames', logicalType: 'string', shape: 'array', repeated: true, sourcePath: 'name[].family' },
  ];
  const artifact = inspectJ05ArtifactPackage(j05ArtifactPackage({
    format: 'JSONL',
    columns,
    rowCount: 2,
    data: [
      JSON.stringify({ rowId: { groupId: 'group-a', revisionId: 'revision-a' }, values: { patientId: 'patient-a', familyNames: ['Example', 'Example-Smith'] } }),
      JSON.stringify({ rowId: { groupId: 'group-b', revisionId: 'revision-a' }, values: { patientId: 'patient-b', familyNames: ['Builder'] } }),
    ].join('\n') + '\n',
  }));
  assert.equal(artifact.dataName, 'data.jsonl');
  assert.equal(artifact.manifest.format, 'JSONL');
  assert.equal(artifact.rows.length, 2);
  assert.deepEqual(artifact.rows[0], {
    rowId: { groupId: 'group-a', revisionId: 'revision-a' },
    values: { patient_id: 'patient-a', family: ['Example', 'Example-Smith'] },
  });
  assert.equal(artifact.rows.some((row) => row.values.family === 'Example; Example-Smith'), false);
});

test('J05 prepared modal identity is rejected after publication generation or schema changes', () => {
  const prepared = { project: j05Identity.project, datasetGeneration: j05Identity.datasetGeneration, outputId: j05Identity.outputId, revisionId: j05Identity.revisionId, schemaDigest: j05Identity.schemaDigest };
  assert.equal(j05ArtifactIdentityIsCurrent(prepared, prepared), true);
  assert.equal(j05ArtifactIdentityIsCurrent(prepared, { ...prepared, datasetGeneration: 'fixture-next' }), false);
  assert.equal(j05ArtifactIdentityIsCurrent(prepared, { ...prepared, schemaDigest: 'schema-next' }), false);
  assert.equal(j05ArtifactIdentityIsCurrent(prepared, { ...prepared, revisionId: 'revision-next' }), false);
});

test('J05 Viewer transport values are decoded through the declared logical type', () => {
  assert.equal(normalizeJ05LogicalValue('2', { column: 'count', logicalType: 'integer' }), 2);
  assert.equal(normalizeJ05LogicalValue('false', { column: 'flag', logicalType: 'boolean' }), false);
  assert.equal(normalizeJ05LogicalValue('2.5', { column: 'value', logicalType: 'decimal' }), 2.5);
  assert.equal(normalizeJ05LogicalValue('002', { column: 'code', logicalType: 'string' }), '002');
  assert.throws(
    () => normalizeJ05LogicalValue('9007199254740993', { column: 'count', logicalType: 'integer' }),
    /exceeds JavaScript's exact integer range/,
  );
});

test('J05 artifact mismatches fail deterministically on publication identity and literal row values', () => {
  const artifact = inspectJ05ArtifactPackage(j05ArtifactPackage({
    format: 'CSV',
    columns: [{ name: 'patient_id', outputKey: 'patientId', logicalType: 'string', shape: 'scalar' }],
    rowCount: 1,
    data: 'patient_id\npatient-a\n',
  }));
  assert.throws(
    () => assertJ05ArtifactIdentity(artifact, { ...j05Identity, schemaDigest: 'schema-stale' }),
    (error) => error.message === 'J05 artifact schemaDigest differs from the current publication: expected "schema-stale", got "schema-j05"',
  );
  assert.throws(
    () => assertJ05ArtifactRows(artifact, [{ values: { patient_id: 'patient-b' } }]),
    (error) => error.message === 'J05 artifact literal rows differ from Preview/Viewer: expected [{"values":{"patient_id":"patient-b"}}], got [{"values":{"patient_id":"patient-a"}}]',
  );
});
