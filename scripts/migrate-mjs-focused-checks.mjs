#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, renameSync, unlinkSync, existsSync, mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { planFocusedCheckGroups } from './verify-ui/helpers/focused-check-groups.mjs';

const FAMILY_PATH = 'verification/builder-interactions/mjs-focused-check-families.json';
const CATALOG_PATH = 'verification/builder-interactions/mjs-source-semantics.json';
const OBLIGATIONS_PATH = 'verification/builder-interactions/test-obligations.json';
const REGISTRY_PATH = 'scripts/verify-ui/registry.mjs';
const ATTACHMENT_PATH = 'scripts/verify-ui/mjs-focused-check-attachments.json';
const TOOL_PATH = 'scripts/migrate-mjs-focused-checks.mjs';
const SELF_TEST_PATH = 'scripts/verify-ui/helpers/tests/mjs-focused-check-migration.test.mjs';
const MIGRATION_ADDED_PATHS = [TOOL_PATH, SELF_TEST_PATH];
const HASH = /^[a-f0-9]{64}$/;
const IDENTIFIER = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const jsonText = (value) => `${JSON.stringify(value, null, 2)}\n`;
const stable = (value) => JSON.stringify(value);

function fail(message) {
  throw new Error(message);
}

function readText(root, relativePath) {
  try {
    return readFileSync(path.join(root, relativePath), 'utf8');
  } catch (error) {
    fail(`cannot read ${relativePath}: ${error.message}`);
  }
}

function fileHash(root, relativePath, { missing = false } = {}) {
  const absolute = path.join(root, relativePath);
  if (!existsSync(absolute)) {
    if (missing) return null;
    fail(`missing required source ${relativePath}`);
  }
  return sha256(readFileSync(absolute));
}

function freshDetailedObligations(root, compactText) {
  const outputDirectory = mkdtempSync(path.join(os.tmpdir(), 'loom-test-obligations-'));
  const outputPath = path.join(outputDirectory, 'details.json');
  try {
    const result = spawnSync(process.execPath, ['scripts/build-test-obligations.mjs', '--details', '--json', outputPath], {
      cwd: root,
      encoding: 'utf8',
      timeout: 120_000,
      maxBuffer: 64 * 1024 * 1024,
    });
    if (result.status !== 0) {
      fail(`fresh test-obligation generation failed: ${result.stderr?.trim() || result.error?.message || result.status}`);
    }
    if (!existsSync(outputPath)) fail('fresh test-obligation generation did not write its external JSON view');
    let detailed;
    try {
      detailed = JSON.parse(readFileSync(outputPath, 'utf8'));
    } catch (error) {
      fail(`fresh test-obligation generation returned invalid JSON: ${error.message}`);
    }
    const { scripts, semanticClaims, ...projection } = detailed;
    if (detailed.schemaVersion !== 3 || Object.hasOwn(detailed, 'projectionKind')
      || !Array.isArray(scripts) || !Array.isArray(semanticClaims)) fail('fresh test-obligation details have an unsupported shape');
    if (jsonText({ ...projection, projectionKind: 'compact-coverage' }) !== compactText) {
      fail(`${OBLIGATIONS_PATH} is stale; run node scripts/build-test-obligations.mjs`);
    }
    return { scripts, semanticClaims };
  } finally {
    rmSync(outputDirectory, { recursive: true, force: true });
  }
}

function checkedPath(value, label) {
  if (typeof value !== 'string' || value.length === 0 || value.includes('\\')
    || path.posix.isAbsolute(value) || value.split('/').some((part) => !part || part === '.' || part === '..')) {
    fail(`${label} must be a repository-relative path without traversal`);
  }
  return value;
}

function literalImports(source, specifier) {
  return [`from '${specifier}'`, `from "${specifier}"`].flatMap((literal) => {
    const sites = [];
    let offset = 0;
    for (;;) {
      const found = source.indexOf(literal, offset);
      if (found < 0) break;
      const start = source.lastIndexOf('import', found);
      sites.push({ clause: source.slice(start, found), after: source.slice(found + literal.length) });
      offset = found + literal.length;
    }
    return sites;
  });
}

function hasRequiredSymbols(sites, names) {
  return names.every((name) => sites.some(({ clause, after }) =>
    new RegExp(`\\b${name}\\b`).test(clause) && new RegExp(`\\b${name}\\s*\\(`).test(after)));
}

function literalEdges(obligation) {
  return (obligation?.connections ?? []).filter((edge) => edge.kind === 'literal-import-candidate');
}

function validateFamilyRules(families) {
  if (families?.schemaVersion !== 1 || !Array.isArray(families.families) || families.families.length === 0) {
    fail('family rules must have schemaVersion 1 and a non-empty families array');
  }
  const ids = new Set();
  const destinations = new Set();
  for (const [index, family] of families.families.entries()) {
    const where = `families[${index}]`;
    if (!family || typeof family !== 'object' || Array.isArray(family)) fail(`${where} must be an object`);
    if (!/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(family.id ?? '')) fail(`${where}.id is invalid`);
    if (ids.has(family.id)) fail(`duplicate family id ${family.id}`);
    ids.add(family.id);
    if (!/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(family.scenarioId ?? '')
      || !/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(family.caseId ?? '')) fail(`${where} needs an exact scenarioId and caseId`);
    const destination = `${family.scenarioId}:${family.caseId}`;
    if (destinations.has(destination)) fail(`ambiguous destination ${destination}`);
    destinations.add(destination);
    checkedPath(family.workflowPath, `${where}.workflowPath`);
    checkedPath(family.oraclePath, `${where}.oraclePath`);
    if (!Array.isArray(family.requiredOracleFunctions) || family.requiredOracleFunctions.length === 0
      || new Set(family.requiredOracleFunctions).size !== family.requiredOracleFunctions.length
      || family.requiredOracleFunctions.some((name) => !IDENTIFIER.test(name))) {
      fail(`${where}.requiredOracleFunctions must be unique function identifiers`);
    }
    if (typeof family.fixtureClass !== 'string' || family.fixtureClass.length === 0) fail(`${where}.fixtureClass is required`);
    if (!Array.isArray(family.mappingEvidence) || family.mappingEvidence.length === 0) fail(`${where}.mappingEvidence is required`);
    for (const evidence of family.mappingEvidence) {
      checkedPath(evidence?.path, `${where}.mappingEvidence.path`);
      if (!Number.isInteger(evidence.line) || evidence.line < 1 || typeof evidence.kind !== 'string' || !evidence.kind) {
        fail(`${where}.mappingEvidence entries need path, positive line, and kind`);
      }
    }
    if (Object.hasOwn(family, 'testPaths') || Object.hasOwn(family, 'tests')) fail(`${where} cannot enumerate test paths`);
  }
  return families.families;
}

function registryIndex(registryModule) {
  const entries = registryModule.registry;
  if (!Array.isArray(entries)) fail('registry must export an array');
  const scenarios = new Map();
  const cases = new Map();
  const checkIDs = new Map();
  for (const scenario of entries) {
    if (typeof scenario?.id !== 'string' || !scenario.id) fail('registry contains a scenario without an id');
    if (scenarios.has(scenario.id)) fail(`duplicate registry scenario id ${scenario.id}`);
    scenarios.set(scenario.id, scenario);
    for (const caseName of registryModule.caseNamesFor(scenario)) {
      const fullId = `${scenario.id}:${caseName}`;
      if (cases.has(fullId)) fail(`duplicate registry case identity ${fullId}`);
      const contract = registryModule.scenarioCaseFor(scenario, caseName);
      cases.set(fullId, { scenario, caseName, contract });
      for (const group of contract.focusedChecks ?? []) {
        if (!checkIDs.has(group.id)) checkIDs.set(group.id, []);
        checkIDs.get(group.id).push({ fullId, group });
      }
    }
  }
  return { cases, checkIDs };
}

function assertRegistryConsumes(registryModule, attachments) {
  const { cases } = registryIndex(registryModule);
  for (const attachment of attachments) {
    const destination = `${attachment.scenarioId}:${attachment.caseId}`;
    const entry = cases.get(destination);
    if (!entry) fail(`generated attachment has unknown registry case ${destination}`);
    for (const expected of attachment.focusedChecks) {
      const groups = (entry.contract.focusedChecks ?? []).filter((group) => group.id === expected.id);
      if (groups.length !== 1 || stable(groups[0]) !== stable(expected)) {
        fail(`registry does not consume exact focused attachment ${expected.id} at ${destination}`);
      }
    }
  }
}

function assertFreshRegistryConsumes(root, attachments) {
  const moduleURL = pathToFileURL(path.join(root, REGISTRY_PATH)).href;
  const program = `const mod = await import(${JSON.stringify(moduleURL)}); const expected = ${JSON.stringify(attachments)}; const rows = expected.map(({scenarioId, caseId}) => { const scenario = mod.registry.find((entry) => entry.id === scenarioId); if (!scenario) throw new Error('unknown scenario ' + scenarioId); const contract = mod.scenarioCaseFor(scenario, caseId); return {scenarioId, caseId, focusedChecks: contract.focusedChecks ?? []}; }); process.stdout.write(JSON.stringify(rows));`;
  const result = spawnSync(process.execPath, ['--input-type=module', '-e', program], { cwd: root, encoding: 'utf8', timeout: 10_000 });
  if (result.status !== 0) fail(`fresh registry import failed: ${result.stderr.trim() || result.error?.message || result.status}`);
  let rows;
  try {
    rows = JSON.parse(result.stdout);
  } catch (error) {
    fail(`fresh registry import returned invalid JSON: ${error.message}`);
  }
  for (const attachment of attachments) {
    const matching = rows.filter((row) => row.scenarioId === attachment.scenarioId && row.caseId === attachment.caseId);
    if (matching.length !== 1) fail(`fresh registry import did not resolve exact case ${attachment.scenarioId}:${attachment.caseId}`);
    const entry = matching[0];
    for (const expected of attachment.focusedChecks) {
      const groups = (entry.focusedChecks ?? []).filter((group) => group.id === expected.id);
      if (groups.length !== 1 || stable(groups[0]) !== stable(expected)) {
        fail(`fresh registry import does not consume exact focused attachment ${expected.id} at ${attachment.scenarioId}:${attachment.caseId}`);
      }
    }
  }
}

function assertCatalogState(root, catalog, obligations) {
  if (catalog?.schemaVersion !== 1 || !Array.isArray(catalog.files) || !Array.isArray(catalog.claims)) {
    fail('semantic source catalogue has an unsupported shape');
  }
  if (!Array.isArray(obligations?.scripts) || !Array.isArray(obligations.semanticClaims)) {
    fail('generated test obligations have an unsupported shape');
  }
  if (obligations.sources?.semanticCatalog !== CATALOG_PATH || obligations.sources?.registry !== REGISTRY_PATH) {
    fail('generated obligations do not point at the canonical semantic catalogue and registry');
  }
  const active = catalog.files.filter((file) => file.lifecycle === 'active');
  const retiredCount = catalog.files.filter((file) => file.lifecycle === 'retired').length;
  if (catalog.files.length !== active.length + retiredCount) fail('catalogue file lifecycle is invalid');
  if (obligations.mjsCatalogSummary?.currentFiles !== active.length) fail('obligation active-source count differs from the semantic catalogue');
  if (!Number.isInteger(obligations.mjsCatalogSummary?.baselineFiles)
    || !Number.isInteger(obligations.mjsCatalogSummary?.baselineRetired)
    || !Number.isInteger(obligations.mjsCatalogSummary?.currentAdded)) fail('obligation baseline/addition counts are missing');
  const sourceRows = new Map();
  const obligationRows = new Map();
  for (const row of catalog.files) {
    if (sourceRows.has(row.path)) fail(`duplicate semantic catalogue path ${row.path}`);
    sourceRows.set(row.path, row);
  }
  for (const row of obligations.scripts) {
    if (obligationRows.has(row.path)) fail(`duplicate generated-obligation source path ${row.path}`);
    obligationRows.set(row.path, row);
  }
  const claimIDs = new Set(catalog.claims.map((claim) => claim.id));
  if (claimIDs.size !== catalog.claims.length) fail('semantic catalogue has duplicate claim ids');
  const obligationClaimIDs = new Set(obligations.semanticClaims.map((claim) => claim.id));
  if (obligationClaimIDs.size !== obligations.semanticClaims.length) fail('generated obligations have duplicate semantic claim ids');
  for (const row of active) {
    checkedPath(row.path, 'semantic catalogue file path');
    if (!HASH.test(row.sha256 ?? '')) fail(`invalid source hash for ${row.path}`);
    const actual = fileHash(root, row.path);
    if (actual !== row.sha256) fail(`stale source hash for ${row.path}: expected ${row.sha256}, found ${actual}`);
    const obligation = obligationRows.get(row.path);
    if (!obligation || obligation.present !== true || obligation.currentSha256 !== actual) {
      fail(`generated obligations are stale or missing active source ${row.path}`);
    }
    if (stable(obligation.semanticClaimIds ?? []) !== stable([...new Set((row.assertions ?? []).map((assertion) => assertion.semanticClaimId))].sort())) {
      fail(`generated obligation semantic claims differ for ${row.path}`);
    }
    for (const assertion of row.assertions ?? []) {
      if (!claimIDs.has(assertion.semanticClaimId) || !obligationClaimIDs.has(assertion.semanticClaimId)) {
        fail(`unknown semantic claim ${assertion.semanticClaimId} referenced by ${row.path}`);
      }
    }
  }
  return { active, retiredCount, obligationRows };
}

function verifyFamilyChain(root, family, index, activeByPath, obligationRows) {
  const destination = `${family.scenarioId}:${family.caseId}`;
  const entry = index.cases.get(destination);
  if (!entry) fail(`unknown registry case ${destination}`);
  const { scenario, contract } = entry;
  if (!scenario.id) fail(`unknown registry scenario for ${destination}`);
  const specPath = checkedPath(contract.playwrightTest, `${destination}.playwrightTest`);
  const specSource = readText(root, specPath);
  const workflowSource = readText(root, family.workflowPath);
  const oracleSource = readText(root, family.oraclePath);
  const oracleEdges = literalEdges(obligationRows.get(family.oraclePath)).filter((edge) => edge.sourcePath === family.workflowPath && edge.targetPath === family.oraclePath);
  const workflowEdges = literalEdges(obligationRows.get(family.workflowPath)).filter((edge) => edge.sourcePath === specPath && edge.targetPath === family.workflowPath);
  if (oracleEdges.length === 0 || !oracleEdges.some((edge) => literalImports(workflowSource, edge.specifier).length)) {
    fail(`${family.workflowPath} has no exact direct import edge to ${family.oraclePath}`);
  }
  if (workflowEdges.length === 0 || !workflowEdges.some((edge) => literalImports(specSource, edge.specifier).length)) {
    fail(`${specPath} has no exact direct import edge to ${family.workflowPath}`);
  }
  const identity = (key, value) => new RegExp(`${key}\\s*:\\s*(['"])${value}\\1`).test(specSource);
  if (!identity('cdaScenarioID', family.scenarioId) || !identity('cdaCaseName', family.caseId)) {
    fail(`${specPath} does not declare exact test.use identity ${destination}`);
  }
  const workflowExports = [...workflowSource.matchAll(/\bexport\s+(?:async\s+)?function\s+([A-Za-z_$][\w$]*)/g)].map((match) => match[1]);
  if (!workflowExports.some((name) => new RegExp(`\\b${name}\\s*\\(`).test(specSource))) {
    fail(`${specPath} does not call a workflow function from ${family.workflowPath}`);
  }
  const workflowImports = oracleEdges.flatMap((edge) => literalImports(workflowSource, edge.specifier));
  for (const name of family.requiredOracleFunctions) {
    if (!new RegExp(`export\\s+(?:async\\s+)?function\\s+${name}\\b|export\\s+const\\s+${name}\\s*=`, 'm').test(oracleSource)) {
      fail(`${family.oraclePath} does not export required function ${name}`);
    }
    if (!hasRequiredSymbols(workflowImports, [name])) {
      fail(`${family.workflowPath} does not use required oracle function ${name}`);
    }
  }
  for (const evidence of family.mappingEvidence) {
    const source = readText(root, evidence.path);
    const lines = source.split(/\r?\n/);
    if (evidence.line > lines.length || !lines[evidence.line - 1]?.trim()) {
      fail(`mapping evidence is outside source ${evidence.path}:${evidence.line}`);
    }
  }
  for (const sourcePath of [family.oraclePath, family.workflowPath, specPath, REGISTRY_PATH]) {
    const row = activeByPath.get(sourcePath);
    if (!row) fail(`source chain file is not an active catalog row: ${sourcePath}`);
    const actual = fileHash(root, sourcePath);
    if (row.sha256 !== actual) fail(`stale source hash for ${sourcePath}`);
  }
  return { destination, contract, specPath, specSource, workflowSource, oracleSource };
}

function matchingFamilyImports(row, source, family, obligationRows) {
  const edges = literalEdges(obligationRows.get(family.oraclePath)).filter((edge) =>
    edge.sourcePath === row.path && edge.targetPath === family.oraclePath);
  if (edges.length === 0) return { direct: false, matched: false };
  const imports = edges.flatMap((edge) => literalImports(source, edge.specifier));
  if (imports.length === 0) fail(`stale literal-import candidate for ${row.path} -> ${family.oraclePath}`);
  const matched = hasRequiredSymbols(imports, family.requiredOracleFunctions);
  return { direct: true, matched };
}

function registeredNodePaths(group) {
  if (group?.command?.[0] !== 'node-test') return null;
  return group.command.slice(1);
}

function updateClaimMappings(catalog, family, destination, testRows) {
  const testClaims = [...new Set(testRows.flatMap(({ row }) => (row.assertions ?? []).map((assertion) => assertion.semanticClaimId)))].sort();
  const claimEdits = [];
  for (const claimId of testClaims) {
    const claim = catalog.claims.find((item) => item.id === claimId);
    if (!claim) fail(`missing semantic claim ${claimId}`);
    const journeys = claim.journeys ?? [];
    if (!Array.isArray(journeys)) fail(`claim ${claimId} has invalid journey mappings`);
    const matches = journeys.filter((mapping) => mapping.caseId === destination);
    if (matches.length > 1) fail(`claim ${claimId} has duplicate mappings for ${destination}`);
    const before = structuredClone(journeys);
    if (matches.length === 0) {
      claim.journeys = [...journeys, {
        caseId: destination,
        relation: 'supporting',
        requiredChecks: [],
        focusedCheckIds: [family.id],
        evidence: structuredClone(family.mappingEvidence),
      }];
    } else {
      const mapping = matches[0];
      if (!['direct', 'partial', 'supporting'].includes(mapping.relation)) fail(`claim ${claimId} has conflicting mapping relation for ${destination}`);
      if (mapping.focusedCheckIds !== undefined && !Array.isArray(mapping.focusedCheckIds)) fail(`claim ${claimId} has invalid focusedCheckIds`);
      if ((mapping.focusedCheckIds ?? []).includes(family.id)) {
        if ((mapping.focusedCheckIds ?? []).filter((id) => id === family.id).length !== 1) fail(`claim ${claimId} duplicates focused check ${family.id}`);
      } else {
        mapping.focusedCheckIds = [...(mapping.focusedCheckIds ?? []), family.id].sort();
      }
    }
    if (stable(before) !== stable(claim.journeys)) claimEdits.push({ claimId, before, after: structuredClone(claim.journeys) });
  }
  return { testClaims, claimEdits };
}

function updateExecutionDiscovery(catalog, attachmentRows, generatedText) {
  const edits = [];
  for (const attachment of attachmentRows) {
    for (const testPath of attachment.focusedChecks[0].command.slice(1)) {
      const row = catalog.files.find((item) => item.path === testPath);
      const before = structuredClone(row.executionDiscovery ?? {});
      const token = JSON.stringify(testPath);
      const commandOffset = generatedText.indexOf(token);
      if (commandOffset < 0) fail(`generated attachment omits command path ${testPath}`);
      const line = generatedText.slice(0, commandOffset).split('\n').length;
      const evidence = (row.executionDiscovery?.evidence ?? []).filter((item) =>
        item.kind !== 'imports node:test; no registry focused-check command names this test file'
          && !(item.path === ATTACHMENT_PATH && item.kind === 'generated focused node-test command'));
      evidence.push({ path: ATTACHMENT_PATH, line, kind: 'generated focused node-test command' });
      row.executionDiscovery = {
        ...(row.executionDiscovery ?? {}),
        status: 'static-path-found',
        evidence,
        unresolvedFacts: [],
      };
      if (stable(before) !== stable(row.executionDiscovery)) {
        edits.push({ path: testPath, before, after: structuredClone(row.executionDiscovery) });
      }
    }
  }
  return edits;
}

function assertGeneratedGroupsRemainManaged(existingGenerated, attachmentRows) {
  if (!existingGenerated) return;
  if (existingGenerated.invalid) fail('existing generated focused attachment JSON is invalid');
  for (const existingAttachment of existingGenerated.attachments) {
    const destination = `${existingAttachment.scenarioId}:${existingAttachment.caseId}`;
    const currentAttachment = attachmentRows.find((item) => `${item.scenarioId}:${item.caseId}` === destination);
    for (const existingGroup of existingAttachment.focusedChecks ?? []) {
      const currentGroup = currentAttachment?.focusedChecks.find((item) => item.id === existingGroup.id);
      if (!currentGroup) {
        fail(`generated focused check ${existingGroup.id} at ${destination} is no longer covered by a reviewed family; explicit retirement is unsupported`);
      }
      const previousPaths = registeredNodePaths(existingGroup);
      const currentPaths = registeredNodePaths(currentGroup);
      if (!previousPaths || !currentPaths || previousPaths.some((testPath) => !currentPaths.includes(testPath))) {
        fail(`generated focused check ${existingGroup.id} at ${destination} would drop a previously attached Node test`);
      }
    }
  }
}

function readGenerated(root) {
  const absolute = path.join(root, ATTACHMENT_PATH);
  if (!existsSync(absolute)) return null;
  const source = readFileSync(absolute, 'utf8');
  try {
    const parsed = JSON.parse(source);
    if (parsed.schemaVersion !== 1 || !Array.isArray(parsed.attachments)) return { source, invalid: true };
    return { source, attachments: parsed.attachments };
  } catch {
    return { source, invalid: true };
  }
}

async function loadInputs(root) {
  const familyText = readText(root, FAMILY_PATH);
  const catalogText = readText(root, CATALOG_PATH);
  const obligationsText = readText(root, OBLIGATIONS_PATH);
  const familyRules = JSON.parse(familyText);
  const catalog = JSON.parse(catalogText);
  const compactObligations = JSON.parse(obligationsText);
  const detailedObligations = freshDetailedObligations(root, obligationsText);
  const obligations = { ...compactObligations, ...detailedObligations };
  const registryPath = path.join(root, REGISTRY_PATH);
  const registryModule = await import(`${pathToFileURL(registryPath).href}?migration=${Date.now()}`);
  const { active, retiredCount, obligationRows } = assertCatalogState(root, catalog, obligations);
  const activeByPath = new Map(active.map((row) => [row.path, row]));
  const { cases, checkIDs } = registryIndex(registryModule);
  const families = validateFamilyRules(familyRules);
  const familyInfo = families.map((family) => ({
    family,
    chain: verifyFamilyChain(root, family, { cases, checkIDs }, activeByPath, obligationRows),
  }));
  const familyByID = new Map(familyInfo.map((item) => [item.family.id, item]));
  const familyTests = new Map(families.map((family) => [family.id, []]));
  const classifications = active.map((row) => {
    const obligation = obligationRows.get(row.path);
    const connections = obligation.connections ?? [];
    const configured = connections.some((connection) => connection.kind === 'configured-command');
    const pinned = connections.some((connection) => connection.kind === 'source-file-pinned-only');
    const mapped = (obligation.journeyMappings ?? []).length > 0;
    return {
      path: row.path,
      classification: configured ? 'already-wired'
        : pinned || mapped || (row.role === 'test' && row.testLayer === 'node-test') ? 'needs-review' : 'not-applicable',
    };
  });
  const classificationByPath = new Map(classifications.map((row) => [row.path, row]));
  const candidateRows = active.filter((row) => row.role === 'test' && row.testLayer === 'node-test' && row.path.endsWith('.test.mjs'));
  for (const row of candidateRows) {
    const source = readText(root, row.path);
    const matches = [];
    for (const { family } of familyInfo) {
      const match = matchingFamilyImports(row, source, family, obligationRows);
      if (!match.direct) continue;
      if (match.matched) matches.push(family);
      else if (classificationByPath.get(row.path).classification !== 'already-wired') {
        classificationByPath.get(row.path).classification = 'needs-review';
      }
    }
    if (matches.length > 1) fail(`ambiguous test match ${row.path} matches ${matches.map((family) => family.id).join(', ')}`);
    if (matches.length === 1) {
      const family = matches[0];
      const { chain } = familyByID.get(family.id);
      const fullCase = chain.destination;
      for (const occurrences of checkIDs.values()) {
        for (const occurrence of occurrences) {
          const paths = registeredNodePaths(occurrence.group);
          if (paths?.includes(row.path) && (occurrence.fullId !== fullCase || occurrence.group.id !== family.id)) {
            fail(`test ${row.path} is already attached to a different focused check ${occurrence.group.id} at ${occurrence.fullId}`);
          }
        }
      }
      familyTests.get(family.id).push({ path: row.path, row, source });
      classificationByPath.get(row.path).classification = 'supported';
      classificationByPath.get(row.path).familyId = family.id;
    }
  }
  for (const { family, chain } of familyInfo) {
    const occurrences = checkIDs.get(family.id) ?? [];
    if (occurrences.some((item) => item.fullId !== chain.destination)) fail(`focused-check ID collision for ${family.id} outside ${chain.destination}`);
    if (occurrences.length > 1) fail(`focused-check ID ${family.id} is registered more than once`);
    const existing = occurrences[0]?.group;
    if (existing && !registeredNodePaths(existing)) fail(`focused-check ${family.id} collides with a non-Node attachment`);
    const attachedPaths = registeredNodePaths(existing) ?? [];
    const matched = familyTests.get(family.id);
    for (const oldPath of attachedPaths) {
      if (!matched.some((candidate) => candidate.path === oldPath)) fail(`registered attachment ${family.id} contains an unmatched test ${oldPath}`);
    }
    if (matched.length === 0) fail(`family ${family.id} has no matching direct-import Node tests`);
    for (const candidate of matched) {
      if (attachedPaths.includes(candidate.path)) classificationByPath.get(candidate.path).classification = 'already-wired';
    }
  }
  const classifiedCounts = Object.fromEntries(['supported', 'already-wired', 'needs-review', 'not-applicable']
    .map((status) => [status, classifications.filter((row) => row.classification === status).length]));
  return {
    root, familyText, catalogText, obligationsText, familyRules, catalog, obligations, registryModule,
    active, retiredCount, obligationRows, activeByPath, familyInfo, familyTests, classifications,
    classifiedCounts, checkIDs,
  };
}

function makePlan(inputs) {
  const { root, familyInfo, familyTests, catalog, catalogText, obligations, familyText, obligationsText, checkIDs } = inputs;
  const catalogBefore = sha256(catalogText);
  const outputBefore = fileHash(root, ATTACHMENT_PATH, { missing: true });
  const claimEditSummaries = [];
  const attachmentRows = [];
  const discoveryEdits = [];
  const sourceHashes = new Map([
    [FAMILY_PATH, sha256(familyText)],
    [CATALOG_PATH, catalogBefore],
    [OBLIGATIONS_PATH, sha256(obligationsText)],
    [REGISTRY_PATH, fileHash(root, REGISTRY_PATH)],
    [TOOL_PATH, fileHash(root, TOOL_PATH)],
  ]);
  for (const { family, chain } of familyInfo) {
    for (const sourcePath of [family.oraclePath, family.workflowPath, chain.specPath]) sourceHashes.set(sourcePath, fileHash(root, sourcePath));
    const existing = (checkIDs.get(family.id) ?? []).find((item) => item.fullId === chain.destination)?.group;
    const previousPaths = registeredNodePaths(existing) ?? [];
    const candidates = familyTests.get(family.id);
    const orderedPaths = [...previousPaths, ...candidates.map((item) => item.path).filter((testPath) => !previousPaths.includes(testPath)).sort()];
    if (orderedPaths.length === 0) fail(`family ${family.id} would create an empty focused check`);
    for (const testPath of orderedPaths) {
      const sourceHash = fileHash(root, testPath);
      sourceHashes.set(testPath, sourceHash);
      const obligation = inputs.obligationRows.get(testPath);
      if (!obligation || obligation.currentSha256 !== sourceHash) fail(`missing or stale generated obligation for candidate ${testPath}`);
    }
    const sourceFiles = [...new Set([
      family.oraclePath, family.workflowPath, chain.specPath, REGISTRY_PATH, FAMILY_PATH,
      ATTACHMENT_PATH, TOOL_PATH,
    ])].sort();
    const focusedCheck = {
      id: family.id,
      cwd: '.',
      command: ['node-test', ...orderedPaths],
      sourceFiles,
    };
    planFocusedCheckGroups([focusedCheck], root);
    const attachment = {
      scenarioId: family.scenarioId,
      caseId: family.caseId,
      focusedChecks: [focusedCheck],
    };
    attachmentRows.push(attachment);
    const updated = updateClaimMappings(catalog, family, chain.destination, candidates.map((candidate) => ({ row: candidate.row })));
    claimEditSummaries.push(...updated.claimEdits.map((edit) => ({ ...edit, familyId: family.id })));
  }
  const existingGenerated = readGenerated(root);
  assertGeneratedGroupsRemainManaged(existingGenerated, attachmentRows);
  const reusable = existingGenerated && !existingGenerated.invalid
    && stable(existingGenerated.attachments) === stable(attachmentRows)
    && claimEditSummaries.length === 0;
  const inputPreconditions = [...sourceHashes.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([file, sha256Value]) => ({ file, sha256: sha256Value }));
  const outputPreconditions = [
    { file: CATALOG_PATH, sha256: catalogBefore },
    { file: ATTACHMENT_PATH, sha256: outputBefore },
  ];
  const generatedText = jsonText({ schemaVersion: 1, attachments: attachmentRows });
  discoveryEdits.push(...updateExecutionDiscovery(catalog, attachmentRows, generatedText));
  const catalogAfterText = jsonText(catalog);
  const catalogFinalText = reusable && discoveryEdits.length === 0 ? catalogText : catalogAfterText;
  const unlistedMigrationSources = MIGRATION_ADDED_PATHS.filter((file) =>
    existsSync(path.join(root, file)) && !inputs.activeByPath.has(file)).length;
  const planToken = sha256(stable({
    inputPreconditions,
    outputPreconditions,
    attachments: attachmentRows,
    claimEdits: claimEditSummaries,
    executionDiscoveryEdits: discoveryEdits,
  }));
  const edits = [];
  if (generatedText !== (existsSync(path.join(root, ATTACHMENT_PATH)) ? readText(root, ATTACHMENT_PATH) : null)) {
    edits.push({ file: ATTACHMENT_PATH, beforeSha256: outputBefore, afterSha256: sha256(generatedText) });
  }
  if (catalogFinalText !== catalogText) {
    edits.push({ file: CATALOG_PATH, beforeSha256: catalogBefore, afterSha256: sha256(catalogFinalText), claimEdits: claimEditSummaries, executionDiscoveryEdits: discoveryEdits });
  }
  return {
    schemaVersion: 1,
    planToken,
    summary: {
      activeSources: inputs.active.length,
      baselineActiveMjsSources: obligations.mjsCatalogSummary.baselineFiles - obligations.mjsCatalogSummary.baselineRetired,
      addedMjsSources: obligations.mjsCatalogSummary.currentAdded + unlistedMigrationSources,
      projectedActiveMjsSources: obligations.mjsCatalogSummary.currentFiles + unlistedMigrationSources,
      retiredSources: inputs.retiredCount,
      classifications: inputs.classifiedCounts,
      matchedTests: attachmentRows.reduce((sum, item) => sum + item.focusedChecks[0].command.length - 1, 0),
      families: attachmentRows.length,
    },
    preconditions: inputPreconditions,
    outputPreconditions,
    attachments: attachmentRows,
    claimEdits: claimEditSummaries,
    executionDiscoveryEdits: discoveryEdits,
    edits,
    generatedText,
    catalogFinalText,
    noOp: edits.length === 0,
  };
}

function verifyNoOp(inputs, plan, requestedToken) {
  return plan.noOp && requestedToken === plan.planToken;
}

function writeAtomically(root, plan) {
  const catalogPath = path.join(root, CATALOG_PATH);
  const generatedPath = path.join(root, ATTACHMENT_PATH);
  const writes = [
    { file: generatedPath, content: plan.generatedText },
    { file: catalogPath, content: plan.catalogFinalText },
  ].filter(({ file, content }) => !existsSync(file) || readFileSync(file, 'utf8') !== content);
  const staged = [];
  try {
    for (const write of writes) {
      const temporary = `${write.file}.migration-${process.pid}`;
      writeFileSync(temporary, write.content, { flag: 'wx' });
      staged.push({ ...write, temporary, previous: existsSync(write.file) ? readFileSync(write.file) : null, committed: false });
    }
    for (const write of staged) {
      renameSync(write.temporary, write.file);
      write.committed = true;
    }
  } catch (error) {
    for (const write of staged.reverse()) {
      try {
        if (write.committed) {
          if (write.previous === null) unlinkSync(write.file);
          else writeFileSync(write.file, write.previous);
        } else if (existsSync(write.temporary)) unlinkSync(write.temporary);
      } catch {}
    }
    fail(`could not apply migration atomically: ${error.message}`);
  }
  return () => {
    for (const write of [...staged].reverse()) {
      if (write.previous === null) {
        if (existsSync(write.file)) unlinkSync(write.file);
      } else writeFileSync(write.file, write.previous);
    }
  };
}

function parseArgs(args) {
  let mode;
  let root;
  let planToken;
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (['--plan', '--apply', '--check'].includes(arg)) {
      if (mode) fail('choose exactly one of --plan, --apply, or --check');
      mode = arg.slice(2);
    } else if (arg === '--root') {
      if (root || !args[index + 1]) fail('--root requires one path');
      root = path.resolve(args[++index]);
    } else if (arg === '--plan-token') {
      if (planToken || !HASH.test(args[index + 1] ?? '')) fail('--plan-token requires one SHA-256 value');
      planToken = args[++index];
    } else fail(`unknown argument ${arg}`);
  }
  if (!mode) fail('usage: migrate-mjs-focused-checks.mjs --plan | --apply --plan-token <sha256> | --check [--root <repo>]');
  if (mode === 'apply' && !planToken) fail('--apply requires --plan-token from a reviewed --plan');
  if (mode !== 'apply' && planToken) fail('--plan-token is only valid with --apply');
  return { mode, root: root ?? process.cwd(), planToken };
}

export async function runMigration(args = process.argv.slice(2)) {
  const options = parseArgs(args);
  const root = path.resolve(options.root);
  const inputs = await loadInputs(root);
  const plan = makePlan(inputs);
  if (options.mode === 'plan') {
    if (plan.noOp) assertRegistryConsumes(inputs.registryModule, plan.attachments);
    const { generatedText, catalogFinalText, ...review } = plan;
    process.stdout.write(`${jsonText({ ...review, classifications: inputs.classifications })}`);
    return;
  }
  if (options.mode === 'check') {
    if (!plan.noOp) fail(`applied output is stale; run --plan and apply token ${plan.planToken}`);
    const generated = readGenerated(root);
    if (!generated || generated.invalid || stable(generated.attachments) !== stable(plan.attachments)) fail('generated attachment JSON does not match the validated plan');
    assertRegistryConsumes(inputs.registryModule, plan.attachments);
    process.stdout.write(`PASS checked ${plan.attachments.length} focused attachments and ${plan.claimEdits.length} pending claim edits\n`);
    return;
  }
  if (verifyNoOp(inputs, plan, options.planToken)) {
    assertRegistryConsumes(inputs.registryModule, plan.attachments);
    process.stdout.write(`NO-OP ${options.planToken}\n`);
    return;
  }
  if (plan.planToken !== options.planToken) fail(`stale reviewed plan token; current plan is ${plan.planToken}`);
  if (plan.noOp) fail('outputs are already applied under a different plan token');
  const stale = plan.preconditions.filter(({ file, sha256: expected }) => fileHash(root, file) !== expected);
  if (stale.length) fail(`stale source preconditions: ${stale.map((entry) => entry.file).join(', ')}`);
  const staleOutputs = plan.outputPreconditions.filter(({ file, sha256: expected }) => fileHash(root, file, { missing: true }) !== expected);
  if (staleOutputs.length) fail(`stale output preconditions: ${staleOutputs.map((entry) => entry.file).join(', ')}`);
  const rollback = writeAtomically(root, plan);
  try {
    assertFreshRegistryConsumes(root, plan.attachments);
  } catch (error) {
    rollback();
    throw error;
  }
  process.stdout.write(`APPLIED ${options.planToken}\n`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  runMigration().catch((error) => {
    process.stderr.write(`migration refused: ${error.message}\n`);
    process.exitCode = 1;
  });
}
