#!/usr/bin/env node
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';
import { playwrightDiscoveryCountIssues } from '../../lib/playwright-discovery-counts.mjs';
import { sourcePreimageLedgerIssue } from './source-preimage-provenance.mjs';

const scriptsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const scriptRoot = resolve(scriptsRoot, '..');
const args = process.argv.slice(2);
function option(name, fallback) {
  const index = args.indexOf(name);
  return index < 0 ? fallback : args[index + 1];
}
const sourceRoot = resolve(option('--source-root', scriptRoot));
const manifestPath = resolve(option('--manifest', join(scriptRoot, 'docs', 'verification', 'playwright', 'source-conversion-manifest.json')));
const runnerInventoryPath = resolve(option('--runner-inventory', join(dirname(manifestPath), 'runner-inventory.snapshot.json')));
const preimagesPath = resolve(option('--preimages', join(dirname(manifestPath), 'source-preimages.json')));
const discoveryPath = resolve(option('--discovery', join(dirname(manifestPath), 'discovery.snapshot.json')));
const requireComplete = args.includes('--require-complete');
const issues = [];
const open = [];
const verifiedNewNativeSourceProvenances = [];
const ledger = JSON.parse(readFileSync(manifestPath, 'utf8'));
const runnerInventory = JSON.parse(readFileSync(runnerInventoryPath, 'utf8'));
const preimages = JSON.parse(readFileSync(preimagesPath, 'utf8'));
const discovery = JSON.parse(readFileSync(discoveryPath, 'utf8'));
issues.push(...playwrightDiscoveryCountIssues(discovery));
if (discovery.status !== 'discovery-only' || discovery.runtimeStatus !== 'not-run') addIssue('discovery snapshot must remain explicitly discovery-only with runtimeStatus not-run');
if (ledger.sourceScope.discoverySnapshot?.path !== 'docs/verification/playwright/discovery.snapshot.json' || ledger.sourceScope.discoverySnapshot?.sha256 !== digest(discoveryPath)) addIssue('discovery snapshot path/hash does not match the generated ledger');
const discoveryTests = new Map();
const discoverySpecHashes = new Map();
for (const session of discovery.sessions ?? []) {
  const configPath = join(sourceRoot, session.configPath ?? '');
  if (!statSafe(configPath) || digest(configPath) !== session.configSha256) addIssue(`discovery config hash drift: ${session.configPath}`);
  for (const spec of session.specs ?? []) {
    const specPath = spec.path;
    const specFile = join(sourceRoot, specPath);
    if (!statSafe(specFile) || digest(specFile) !== spec.sha256) addIssue(`discovery spec hash drift: ${specPath}`);
    discoverySpecHashes.set(`${session.sessionID}:${specPath}`, spec.sha256);
    for (const test of spec.cases ?? []) {
      const key = `${session.sessionID}:${specPath}:${test.title}`;
      discoveryTests.set(key, [...(discoveryTests.get(key) ?? []), test]);
    }
  }
}
for (const [sessionID, testCountKey, fileCountKey] of [
  ['main', 'discoveredTestCount', 'discoveredSpecFileCount'],
  ['construction-preview-bench', 'dedicatedBenchmarkTestCount', 'dedicatedBenchmarkSpecFileCount'],
]) {
  const session = (discovery.sessions ?? []).find((item) => item.sessionID === sessionID);
  if (!session || ledger.testDiscoveryEvidence?.[testCountKey] !== session.testCount || ledger.testDiscoveryEvidence?.[fileCountKey] !== session.specFileCount) addIssue(`ledger discovery summary drift: ${sessionID}`);
}
const records = new Map(ledger.sources.map((item) => [item.sourcePath, item]));
const registryCases = new Map(ledger.registryCases.map((item) => [`${item.scenario}/${item.case}`, item]));
const mappedSpecPaths = new Set(ledger.sources.flatMap((item) => item.nativeSpecPaths ?? []));
const registeredSpecPaths = new Set(ledger.registryCases.map((item) => item.nativeSpecPath).filter(Boolean));
const infrastructureSpecs = new Map((ledger.infrastructureSpecs ?? []).map((item) => [item.path, item]));
const infrastructureSpecPaths = new Set(infrastructureSpecs.keys());
const embeddedJourneys = (ledger.additionalBrowserWorkflowSources ?? []).flatMap((item) => item.journeys ?? []);
const additionalBrowserJourneys = (ledger.additionalStandaloneBrowserWorkflowSources ?? []).flatMap((item) => item.journeys ?? []);
for (const journey of embeddedJourneys) {
  for (const specPath of journey.nativeSpecPaths ?? []) mappedSpecPaths.add(specPath);
}
for (const journey of additionalBrowserJourneys) {
  for (const specPath of journey.nativeSpecPaths ?? []) mappedSpecPaths.add(specPath);
}

function digest(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}
function filesUnder(directory) {
  const results = [];
  if (!statSafe(directory)) return results;
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name === '.artifacts' || entry.name === 'tests') continue;
    const path = join(directory, entry.name);
    if (entry.isDirectory()) results.push(...filesUnder(path));
    else results.push(path);
  }
  return results;
}
function statSafe(path) {
  try { statSync(path); return true; } catch { return false; }
}
function discoverSources() {
  const found = [];
  const scripts = join(sourceRoot, 'scripts');
  for (const path of readdirSync(scripts, { withFileTypes: true })) {
    if (path.isFile() && /^verify[-_].+\.mjs$/.test(path.name) && !path.name.endsWith('.test.mjs')) {
      found.push(relative(sourceRoot, join(scripts, path.name)).replaceAll('\\', '/'));
    }
  }
  for (const uiSourceDir of ['workflows', 'helpers']) {
    const uiSources = join(scripts, 'verify-ui', uiSourceDir);
    for (const path of filesUnder(uiSources)) {
      if (basename(path).endsWith('.mjs') && /^verify[-_].+\.mjs$/.test(basename(path)) && !path.endsWith('.test.mjs')) {
        found.push(relative(sourceRoot, path).replaceAll('\\', '/'));
      }
    }
  }
  const uiPackages = join(sourceRoot, 'ui', 'packages');
  for (const path of filesUnder(uiPackages)) {
    if (basename(path) && path.endsWith('.mjs') && /^verify[-_].+\.mjs$/.test(basename(path)) && dirname(path).split('/').at(-1) === 'scripts' && !path.endsWith('.test.mjs')) {
      found.push(relative(sourceRoot, path).replaceAll('\\', '/'));
    }
  }
  return [...new Set(found)].sort();
}
function basename(path) {
  return path.split(/[\\/]/).at(-1);
}
function ownsBrowser(path) {
  const source = readFileSync(path, 'utf8');
  return /\b(?:launchBrowser|launchCdaBrowser|launchPlaywrightBrowser|launchPlaywrightEvidenceBrowser)\s*\(|\bimport\s*\{[^}]*\b(?:launchBrowser|launchCdaBrowser|launchPlaywrightBrowser|launchPlaywrightEvidenceBrowser)\b[^}]*\}|\bexport\s+(?:async\s+)?(?:function|const|let)\s+(?:launchBrowser|launchCdaBrowser|launchPlaywrightBrowser|launchPlaywrightEvidenceBrowser)\b|\b(?:chromium|firefox|webkit)\.(?:launch(?:PersistentContext)?|connect(?:OverCDP)?)\s*\(|\bcdp\s*\.\s*send\s*\(|chrome-remote-interface|remote-debugging-port/s.test(source);
}
function addOpen(message) { open.push(message); }
function addIssue(message) { issues.push(message); }
const mappedDiscoveryCases = [];
const actualDiscoveryOwnerRows = [];
function stableJson(value) {
  if (Array.isArray(value)) return `[${value.map(stableJson).join(',')}]`;
  if (value && typeof value === 'object') return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stableJson(value[key])}`).join(',')}}`;
  return JSON.stringify(value);
}
function checkDiscoveryBinding(caseRow, sourcePath, outerSpecPaths = [], ownerKind = 'source') {
  const binding = caseRow.discoveryBinding;
  if (!binding?.sessionID || !binding?.specPath || !binding?.testTitle) {
    addIssue(`native case lacks an exact official discovery binding: ${sourcePath} (${caseRow.scenarioID ?? 'unknown'}/${caseRow.caseName ?? caseRow.caseTitle ?? 'unnamed'})`);
    return;
  }
  if (caseRow.caseTitle !== binding.testTitle) addIssue(`native case title differs from its exact discovery title: ${sourcePath} (${caseRow.scenarioID ?? 'unknown'}/${caseRow.caseName ?? 'unnamed'})`);
  const specPaths = caseRow.nativeSpecPaths?.length ? caseRow.nativeSpecPaths : outerSpecPaths;
  if (!specPaths.includes(binding.specPath)) addIssue(`native case discovery spec is outside its declared mapping: ${sourcePath} -> ${binding.specPath}`);
  const key = `${binding.sessionID}:${binding.specPath}:${binding.testTitle}`;
  const discovered = discoveryTests.get(key) ?? [];
  if (!discovered.length) addIssue(`native case title/spec is absent from official discovery: ${sourcePath} -> ${key}`);
  if (binding.matchingTestCount !== discovered.length) addIssue(`native case discovery count drift: ${sourcePath} -> ${key}`);
  const ids = discovered.map((item) => item.id).filter(Boolean);
  if (JSON.stringify(binding.matchingTestIDs ?? []) !== JSON.stringify(ids)) addIssue(`native case discovery IDs drift: ${sourcePath} -> ${key}`);
  if (caseRow.expectedVariants === undefined || caseRow.expectedVariants === null || caseRow.expectedVariants === '') addIssue(`native case has no explicit expected variant identity: ${sourcePath} (${caseRow.scenarioID ?? 'unknown'}/${caseRow.caseName ?? 'unnamed'})`);
  const ownerIdentity = {
    ownerKind,
    sourcePath,
    scenarioID: caseRow.scenarioID ?? null,
    caseName: caseRow.caseName ?? null,
    expectedVariants: caseRow.expectedVariants ?? null,
  };
  actualDiscoveryOwnerRows.push({
    ...ownerIdentity,
    sessionID: binding.sessionID,
    specPath: binding.specPath,
    testTitle: binding.testTitle,
  });
  for (const variant of caseRow.variantDiscoveryBindings ?? []) {
    const variantKey = `${variant.sessionID}:${binding.specPath}:${variant.testTitle}`;
    if (!discoveryTests.has(variantKey)) addIssue(`expected native variant is absent from official discovery: ${sourcePath} -> ${variantKey}`);
    actualDiscoveryOwnerRows.push({
      ...ownerIdentity,
      sessionID: variant.sessionID,
      specPath: binding.specPath,
      testTitle: variant.testTitle,
    });
  }
  mappedDiscoveryCases.push({ sourcePath, caseRow, sessionID: binding.sessionID, specPath: binding.specPath, testTitle: binding.testTitle });
}

const actualSources = discoverSources();
for (const path of actualSources) {
  if (!records.has(path)) addIssue(`unledgered source file: ${path}`);
}
for (const path of ledger.sourceScope.currentSourcePaths) {
  if (!actualSources.includes(path)) addIssue(`stale current-source path in ledger: ${path}`);
}

let currentRegistry;
try {
  const registryPath = join(sourceRoot, ledger.sourceSnapshot.registryPath);
  const module = await import(pathToFileURL(registryPath));
  currentRegistry = module.registry.flatMap((scenario) => module.caseNamesFor(scenario).map((caseName) => {
    const caseDefinition = module.scenarioCaseFor(scenario, caseName);
    const customCaseDefinition = module.scenarioCaseFor(scenario, caseName, true);
    const customPreservedAssertions = JSON.stringify(customCaseDefinition.requiredChecks) === JSON.stringify(caseDefinition.requiredChecks)
      ? null
      : customCaseDefinition.requiredChecks;
    return {
      scenario: scenario.id,
      case: caseName,
      nativeSpecPath: caseDefinition?.playwrightTest ?? null,
      preservedAssertions: caseDefinition?.requiredChecks ?? [],
      customPreservedAssertions,
    };
  }));
} catch (error) {
  addIssue(`could not load official registry: ${error.message}`);
  currentRegistry = [];
}
const currentRegistryMap = new Map(currentRegistry.map((item) => [`${item.scenario}/${item.case}`, item]));
if (currentRegistry.length !== ledger.counts.registryCases) {
  addIssue(`registry case count drift: manifest ${ledger.counts.registryCases}, source ${currentRegistry.length}`);
}
const registryRequiredCheckEntries = currentRegistry.reduce((total, item) => total + item.preservedAssertions.length + (item.customPreservedAssertions?.length ?? 0), 0);
const registryCustomAssertionVariants = currentRegistry.filter((item) => item.customPreservedAssertions !== null).length;
if (registryRequiredCheckEntries !== ledger.counts.registryRequiredCheckEntries) {
  addIssue(`registry required-check entry count drift: manifest ${ledger.counts.registryRequiredCheckEntries}, source ${registryRequiredCheckEntries}`);
}
if (registryCustomAssertionVariants !== ledger.counts.registryCustomAssertionVariants) {
  addIssue(`registry custom assertion variant count drift: manifest ${ledger.counts.registryCustomAssertionVariants}, source ${registryCustomAssertionVariants}`);
}
for (const [key, item] of currentRegistryMap) {
  const saved = registryCases.get(key);
  if (!saved) addIssue(`registry case missing from ledger: ${key}`);
  else if (saved.nativeSpecPath !== item.nativeSpecPath) addIssue(`registry spec mapping drift for ${key}: ${saved.nativeSpecPath} != ${item.nativeSpecPath}`);
  else if (JSON.stringify(saved.preservedAssertions ?? []) !== JSON.stringify(item.preservedAssertions ?? [])) addIssue(`registry assertion/oracle mapping drift for ${key}`);
  else if (JSON.stringify(saved.customPreservedAssertions ?? null) !== JSON.stringify(item.customPreservedAssertions)) addIssue(`registry custom assertion/oracle mapping drift for ${key}`);
  if (saved && (saved.domainLifecycleStatus !== 'unverified' || saved.runtimeEvidence?.status !== 'not-run')) addIssue(`registry case must remain explicitly lifecycle-unverified: ${key}`);
  if (!item.nativeSpecPath) addOpen(`registry case has no native spec: ${key}`);
  else if (!statSafe(join(sourceRoot, item.nativeSpecPath))) addIssue(`registered native spec is missing: ${item.nativeSpecPath} (${key})`);
  else {
    const specPath = item.nativeSpecPath;
    const spec = (discovery.sessions ?? []).find((session) => session.sessionID === 'main')?.specs?.find((candidate) => candidate.path === specPath);
    if (!spec) addIssue(`registered case spec has no exact main-suite discovery: ${key} -> ${specPath}`);
    else if (saved && (saved.discoverySpecSha256 !== spec.sha256 || JSON.stringify(saved.discoveryTestTitles ?? []) !== JSON.stringify([...new Set(spec.cases.map((test) => test.title))].sort()) || saved.discoveryTestCount !== spec.cases.length)) addIssue(`registered case discovery title/spec mapping drift: ${key}`);
  }
}
for (const key of registryCases.keys()) {
  if (!currentRegistryMap.has(key)) addIssue(`stale registry case in ledger: ${key}`);
}

const accounting = new Map(ledger.oldBrowserLauncherConsumers.map((item) => [item.file, item]));
const oldConsumers = runnerInventory.browserLauncherConsumers ?? [];
if (oldConsumers.length !== accounting.size) addIssue(`old browser consumer count drift: ledger ${accounting.size}, inventory ${oldConsumers.length}`);
const knownRetainedConsumerStates = new Set([
  'retained-legacy-runner-helper',
  'retained-benchmark-browser-tool',
  'retained-development-browser-tool',
  'embedded-browser-workflow-mappings-pending',
  'mapped-embedded-browser-workflows',
  'additional-browser-workflow-mappings-pending',
  'mapped-additional-browser-workflows',
  'inventory-keyword-false-positive',
]);
for (const consumer of oldConsumers) {
  const path = consumer.file;
  const ledgerRow = accounting.get(path);
  if (!ledgerRow) {
    addIssue(`old launcher consumer missing ledger disposition: ${path}`);
    continue;
  }
  if (ledgerRow.role !== consumer.role) addIssue(`old launcher consumer role drift: ${path}`);
  if (consumer.role === 'standalone verifier entrypoint') {
    const source = records.get(path);
    if (!source) addIssue(`standalone browser source missing ledger record: ${path}`);
    else if (source.sourceStillPresent && statSafe(join(sourceRoot, path))) {
      const sourcePath = join(sourceRoot, path);
      if (digest(sourcePath) !== source.currentSha256) addIssue(`source hash drift: ${path}`);
      const browserOwner = ownsBrowser(sourcePath);
      if (source.legacyBrowserOwnershipRemoved && browserOwner) addIssue(`map claims old browser ownership removed while source still owns browser: ${path}`);
      if (!source.legacyBrowserOwnershipRemoved && browserOwner) addOpen(`legacy browser owner remains: ${path}`);
      if (source.nativeSpecPaths.length === 0 && !source.disposition.startsWith('retained-')) addOpen(`standalone source has no native case mapping/disposition: ${path}`);
    } else if (source?.disposition !== 'obsolete-deleted' && !(source?.sourceRelocatedToNativeWorkflow && source?.disposition === 'native-spec-mapped-source')) {
      addOpen(`standalone source file absent without obsolete-deleted disposition: ${path}`);
    } else if (source?.sourceRelocatedToNativeWorkflow && (!source.preimageSha256 || !source.dispositionEvidence?.replacementWorkflowPaths?.length)) {
      addIssue(`relocated standalone source lacks preimage or replacement-workflow evidence: ${path}`);
    }
  } else if (ledgerRow.disposition !== 'inventory-keyword-false-positive' && !knownRetainedConsumerStates.has(ledgerRow.disposition)) {
    addOpen(`old consumer has no accepted explicit disposition: ${path} (${ledgerRow.disposition})`);
  }
}
for (const [path, row] of accounting) {
  if (!oldConsumers.some((consumer) => consumer.file === path)) addIssue(`stale old consumer accounting row: ${path}`);
  if (!row.disposition || !row.evidence) addIssue(`old consumer disposition lacks evidence: ${path}`);
}

const embeddedGroup = ledger.additionalBrowserWorkflowSources?.find((item) => item.sourcePath === 'scripts/loom-dev.mjs');
if (embeddedGroup) {
  const loomDevPath = join(sourceRoot, 'scripts', 'loom-dev.mjs');
  if (!statSafe(loomDevPath)) addIssue('embedded browser source is missing: scripts/loom-dev.mjs');
  else if (digest(loomDevPath) !== embeddedGroup.sourceSha256) addIssue('embedded browser source hash drift: scripts/loom-dev.mjs');
  const source = statSafe(loomDevPath) ? readFileSync(loomDevPath, 'utf8') : '';
  if (statSafe(loomDevPath) && ownsBrowser(loomDevPath)) addIssue('embedded loom-dev source still contains direct browser ownership');
  const lines = source.split(/\r?\n/);
  const launchLines = [];
  const declarations = [];
  for (let index = 0; index < lines.length; index += 1) {
    const declaration = /^(?:const\s+(\w+)\s*=\s*async\b|async\s+function\s+(\w+))/.exec(lines[index]);
    if (declaration) declarations.push({ line: index + 1, name: declaration[1] ?? declaration[2] });
    if (/\blaunchPlaywrightEvidenceBrowser\s*\(/.test(lines[index])) launchLines.push(index + 1);
  }
  const journeys = embeddedGroup.journeys ?? [];
  const liveJourneys = journeys.filter((item) => item.sourceLaunchStillPresent !== false);
  if (launchLines.length !== liveJourneys.length) addIssue(`loom-dev browser launch-site count drift: ledger ${liveJourneys.length} active owners, source ${launchLines.length}`);
  for (const launchLine of launchLines) {
    const row = liveJourneys.find((item) => item.launchLine === launchLine);
    const nearest = [...declarations].reverse().find((item) => item.line < launchLine);
    if (!row) addIssue(`loom-dev browser launch site missing from journey ledger: line ${launchLine}`);
    else if (row.function !== nearest?.name) addIssue(`loom-dev journey owner drift at line ${launchLine}: ${row.function} != ${nearest?.name}`);
  }
  for (const journey of journeys) {
    if (journey.sourceLaunchStillPresent !== false && !launchLines.includes(journey.launchLine)) addIssue(`stale loom-dev journey line: ${journey.journeyID} at ${journey.launchLine}`);
    if (journey.sourceLaunchStillPresent === false && launchLines.some((line) => {
      const nearest = [...declarations].reverse().find((item) => item.line < line);
      return nearest?.name === journey.function;
    })) addIssue(`loom-dev journey claims owner removed but launch remains: ${journey.journeyID} (${journey.function})`);
    if (!journey.nativeSpecPaths?.length || !journey.nativeCases?.length || !journey.legacyBrowserOwnershipRemoved) {
      addOpen(`embedded loom-dev journey mapping remains open: ${journey.journeyID} (${journey.function}; ${journey.commands.join(', ') || 'no command mapped'})`);
    }
    for (const specPath of journey.nativeSpecPaths ?? []) {
      const specFile = join(sourceRoot, specPath);
      if (!statSafe(specFile)) addOpen(`embedded loom-dev mapped spec missing: ${journey.journeyID} -> ${specPath}`);
      else if (!/\b\w*test\s*\(/i.test(readFileSync(specFile, 'utf8'))) addIssue(`embedded loom-dev mapped spec has no test registration: ${specPath}`);
      const artifact = (journey.nativeSpecArtifacts ?? []).find((item) => item.path === specPath);
      if (!artifact?.sha256) addOpen(`embedded loom-dev mapped spec has no source hash: ${journey.journeyID} -> ${specPath}`);
      else if (statSafe(specFile) && digest(specFile) !== artifact.sha256) addIssue(`embedded loom-dev spec hash drift: ${journey.journeyID} -> ${specPath}`);
    }
    for (const item of journey.nativeCases ?? []) {
      if (!item.scenarioID || !item.caseTitle) addIssue(`embedded loom-dev case lacks scenarioID/caseTitle: ${journey.journeyID}`);
      if (!Array.isArray(item.preservedAssertions) || item.preservedAssertions.length === 0) addOpen(`embedded loom-dev case lacks preserved assertion/oracle summary: ${journey.journeyID}`);
      checkDiscoveryBinding(item, journey.sourcePath, journey.nativeSpecPaths ?? [], 'embedded');
    }
    if (journey.runtimeEvidence?.status !== 'not-run') addOpen(`embedded loom-dev runtime evidence requires separate review: ${journey.journeyID}`);
  }
} else {
  addIssue('ledger omits the additional scripts/loom-dev.mjs browser workflow scope');
}

if (!(ledger.additionalStandaloneBrowserWorkflowSources ?? []).some((item) => item.sourcePath === 'scripts/measurements/construction-preview/construction_preview_bench.mjs')) {
  addIssue('ledger omits the additional scripts/measurements/construction-preview/construction_preview_bench.mjs browser workflow scope');
} else {
  for (const group of ledger.additionalStandaloneBrowserWorkflowSources) {
    const sourcePath = join(sourceRoot, group.sourcePath);
    if (!statSafe(sourcePath)) addIssue(`additional browser workflow source is missing: ${group.sourcePath}`);
    else if (digest(sourcePath) !== group.sourceSha256) addIssue(`additional browser workflow source hash drift: ${group.sourcePath}`);
    if (statSafe(sourcePath) && ownsBrowser(sourcePath)) addIssue(`additional browser workflow source still contains direct browser ownership: ${group.sourcePath}`);
    const source = statSafe(sourcePath) ? readFileSync(sourcePath, 'utf8') : '';
    const launchLines = source.split(/\r?\n/).flatMap((line, index) => /\blaunchBrowser\s*\(/.test(line) ? [index + 1] : []);
    const journeys = group.journeys ?? [];
    const active = journeys.filter((item) => item.sourceLaunchStillPresent !== false);
    if (launchLines.length !== active.length) addIssue(`additional browser launch-site count drift for ${group.sourcePath}: ledger ${active.length}, source ${launchLines.length}`);
    for (const line of launchLines) {
      if (!active.some((item) => item.launchLine === line)) addIssue(`additional browser launch site missing from case ledger: ${group.sourcePath}:${line}`);
    }
    for (const journey of journeys) {
      if (journey.sourceLaunchStillPresent !== false && !launchLines.includes(journey.launchLine)) addIssue(`additional browser journey has stale launch line: ${journey.journeyID}`);
      if (journey.sourceLaunchStillPresent === false && launchLines.length > 0) addIssue(`additional browser journey claims ownership removed while launch remains: ${journey.journeyID}`);
      if (!journey.legacyBrowserOwnershipRemoved || !journey.nativeSpecPaths?.length || !journey.nativeCases?.length) addOpen(`additional browser journey mapping or launcher removal remains open: ${journey.journeyID}`);
      for (const specPath of journey.nativeSpecPaths ?? []) {
        const specFile = join(sourceRoot, specPath);
        if (!statSafe(specFile)) addOpen(`additional browser mapped spec missing: ${journey.journeyID} -> ${specPath}`);
        else if (!/\b\w*test\s*\(/i.test(readFileSync(specFile, 'utf8'))) addIssue(`additional browser mapped spec has no test registration: ${specPath}`);
        const artifact = (journey.nativeSpecArtifacts ?? []).find((item) => item.path === specPath);
        if (!artifact?.sha256) addOpen(`additional browser mapped spec has no source hash: ${journey.journeyID} -> ${specPath}`);
        else if (statSafe(specFile) && digest(specFile) !== artifact.sha256) addIssue(`additional browser mapped spec hash drift: ${journey.journeyID} -> ${specPath}`);
      }
      for (const item of journey.nativeCases ?? []) {
        if (!item.scenarioID || !item.caseTitle) addIssue(`additional browser case lacks scenarioID/caseTitle: ${journey.journeyID}`);
        if (!Array.isArray(item.preservedAssertions) || item.preservedAssertions.length === 0) addOpen(`additional browser case lacks preserved assertion/oracle summary: ${journey.journeyID}`);
        checkDiscoveryBinding(item, journey.sourcePath, journey.nativeSpecPaths ?? [], 'additional-browser');
      }
      if (journey.runtimeEvidence?.status !== 'not-run') addOpen(`additional browser runtime evidence requires separate review: ${journey.journeyID}`);
    }
  }
}

for (const item of ledger.legacyLauncherImplementations ?? []) {
  const path = join(sourceRoot, item.path);
  if (item.sourceStillPresent) {
    if (!statSafe(path)) addIssue(`legacy launcher implementation is missing: ${item.path}`);
    else if (!item.sha256 || digest(path) !== item.sha256) addIssue(`legacy launcher implementation hash drift: ${item.path}`);
    else if (ownsBrowser(path) !== item.directBrowserOwnershipPresent) addIssue(`legacy launcher implementation ownership drift: ${item.path}`);
  } else {
    if (statSafe(path)) addIssue(`retired legacy launcher unexpectedly exists: ${item.path}`);
    if (item.disposition !== 'deleted-after-native-port' || !item.preimageSha256 || !/^[0-9a-f]{64}$/.test(item.preimageSha256) || !item.dispositionEvidence?.patchSha256 || !/^[0-9a-f]{64}$/.test(item.dispositionEvidence.patchSha256) || !item.dispositionEvidence?.reason) addIssue(`deleted legacy launcher lacks historical preimage/port evidence: ${item.path}`);
  }
  if (item.directBrowserOwnershipPresent) addOpen(`legacy browser launcher implementation remains: ${item.path}`);
  if (item.runtimeEvidence?.status !== 'not-run') addOpen(`legacy launcher implementation runtime status is not separated: ${item.path}`);
}

for (const source of ledger.sources) {
  const sourcePath = join(sourceRoot, source.sourcePath);
  const sourceExists = statSafe(sourcePath);
  if (sourceExists && (!source.currentSha256 || digest(sourcePath) !== source.currentSha256)) addIssue(`current source artifact hash drift: ${source.sourcePath}`);
  const preimageIssue = sourcePreimageLedgerIssue(source.sourcePath, source, preimages[source.sourcePath], scriptRoot);
  if (preimageIssue) addIssue(preimageIssue);
  else if (source.historicalPreimageStatus === 'new-native-source-unverified') verifiedNewNativeSourceProvenances.push(source.sourcePath);
  if (source.sourceStillPresent !== sourceExists && actualSources.includes(source.sourcePath)) addIssue(`source-presence drift: ${source.sourcePath}`);
  if (source.nativeSpecPaths.length > 0) {
    if (!Array.isArray(source.nativeCases) || source.nativeCases.length === 0) addOpen(`native spec mapping has no case map: ${source.sourcePath}`);
    for (const specPath of source.nativeSpecPaths) {
      const specFile = join(sourceRoot, specPath);
      if (!statSafe(specFile)) addOpen(`mapped spec missing: ${source.sourcePath} -> ${specPath}`);
      else if (!/\b\w*test\s*\(/i.test(readFileSync(specFile, 'utf8'))) addIssue(`mapped spec has no test registration: ${specPath}`);
    }
    for (const [field, label] of [
      ['nativeSpecArtifacts', 'native spec'],
      ['workflowArtifacts', 'workflow'],
      ['oracleHelperArtifacts', 'oracle helper'],
    ]) {
      for (const artifact of source[field] ?? []) {
        const artifactPath = join(sourceRoot, artifact.path);
        if (!artifact.sha256) addOpen(`${label} artifact has no source hash: ${source.sourcePath} -> ${artifact.path}`);
        else if (!statSafe(artifactPath)) addOpen(`${label} artifact is not present in the canonical source tree: ${source.sourcePath} -> ${artifact.path}`);
        else if (digest(artifactPath) !== artifact.sha256) addIssue(`${label} artifact hash drift: ${source.sourcePath} -> ${artifact.path}`);
      }
    }
    for (const item of source.nativeCases ?? []) {
      if (!item.scenarioID || !item.caseTitle) addIssue(`case mapping lacks scenarioID/caseTitle: ${source.sourcePath}`);
      if (!item.nativeSpecPaths?.length || item.nativeSpecPaths.some((path) => !source.nativeSpecPaths.includes(path))) addIssue(`case-to-spec mapping is missing or outside its source mapping: ${source.sourcePath} (${item.scenarioID ?? 'unknown'})`);
      if (!Array.isArray(item.preservedAssertions) || item.preservedAssertions.length === 0) addOpen(`case mapping lacks preserved assertion/oracle summary: ${source.sourcePath} (${item.scenarioID ?? 'unknown'})`);
      checkDiscoveryBinding(item, source.sourcePath, source.nativeSpecPaths);
    }
    if (source.legacyBrowserOwnershipRemoved && sourceExists && ownsBrowser(sourcePath)) {
      addIssue(`converted module still contains direct browser ownership: ${source.sourcePath}`);
    }
    if (!source.legacyBrowserOwnershipRemoved && sourceExists && source.sourceStillOwnsBrowser) {
      addOpen(`source owner removal not recorded: ${source.sourcePath}`);
    }
  }
  if (source.disposition === 'obsolete-deleted') {
    if (sourceExists) addIssue(`obsolete-deleted source still exists: ${source.sourcePath}`);
    if (!source.reason || !source.dispositionEvidence) addIssue(`obsolete deletion lacks evidence: ${source.sourcePath}`);
    if (!source.preimageSha256 || preimages[source.sourcePath] !== source.preimageSha256) addIssue(`obsolete deletion lacks a verified historical source preimage: ${source.sourcePath}`);
    if (source.nativeReplacementNotRequired && !source.dispositionEvidence?.retirementEvidence) addIssue(`retired experiment lacks evidence that native replacement is unnecessary: ${source.sourcePath}`);
  }
  if (source.sourceRelocatedToNativeWorkflow) {
    if (!source.preimageSha256 || !source.dispositionEvidence?.replacementWorkflowPaths?.length) addIssue(`relocated source lacks preserved preimage or replacement-workflow evidence: ${source.sourcePath}`);
    if (!source.sourceStillPresent && source.legacyBrowserOwnershipRemoved !== true) addOpen(`relocated source browser-owner removal is not recorded: ${source.sourcePath}`);
  }
  if (source.disposition.startsWith('retained-api-') || source.disposition === 'retained-evidence-oracle-cli' || source.disposition === 'retained-unit-check-cli') {
    if (!sourceExists || !source.reason || !source.dispositionEvidence) addIssue(`retained API-only source lacks source evidence: ${source.sourcePath}`);
    else if (ownsBrowser(sourcePath)) addIssue(`retained API-only source still owns a browser: ${source.sourcePath}`);
  }
  if (source.disposition.startsWith('retained-') && (!source.reason || !source.dispositionEvidence)) {
    addIssue(`retained source disposition lacks evidence: ${source.sourcePath}`);
  }
  if (source.runtimeEvidence?.status !== 'not-run') addOpen(`runtime evidence requires separate review: ${source.sourcePath} (${source.runtimeEvidence?.status ?? 'unknown'})`);
}

const allSpecs = readdirSync(join(sourceRoot, 'scripts', 'verify-ui', 'specs'), { withFileTypes: true })
  .filter((entry) => entry.isFile() && entry.name.endsWith('.spec.mjs'))
  .map((entry) => `scripts/verify-ui/specs/${entry.name}`);
for (const specPath of allSpecs) {
  if (!mappedSpecPaths.has(specPath) && !registeredSpecPaths.has(specPath) && !infrastructureSpecPaths.has(specPath)) addOpen(`native spec has no legacy source/registry mapping or explicit infrastructure classification: ${specPath}`);
}
for (const [specPath, item] of infrastructureSpecs) {
  const specFile = join(sourceRoot, specPath);
  if (!statSafe(specFile)) addIssue(`classified infrastructure spec is missing: ${specPath}`);
  else if (!item.sha256 || digest(specFile) !== item.sha256) addIssue(`classified infrastructure spec hash drift: ${specPath}`);
  if (!item.kind || !item.reason || item.runtimeEvidence?.status !== 'not-run') addIssue(`infrastructure spec classification/evidence is incomplete: ${specPath}`);
  const discovered = (discovery.sessions ?? []).find((session) => session.sessionID === 'main')?.specs?.find((candidate) => candidate.path === specPath);
  if (!discovered || item.discoverySpecSha256 !== discovered.sha256 || item.discoveryTestCount !== discovered.cases.length || JSON.stringify(item.discoveryTestTitles ?? []) !== JSON.stringify([...new Set(discovered.cases.map((test) => test.title))].sort())) addIssue(`infrastructure discovery title classification drift: ${specPath}`);
}
const discoveryOwners = new Map();
for (const row of mappedDiscoveryCases) {
  const key = `${row.sessionID}:${row.specPath}:${row.testTitle}`;
  discoveryOwners.set(key, [...(discoveryOwners.get(key) ?? []), row]);
  for (const variant of row.caseRow.variantDiscoveryBindings ?? []) {
    const variantKey = `${variant.sessionID}:${row.specPath}:${variant.testTitle}`;
    discoveryOwners.set(variantKey, [...(discoveryOwners.get(variantKey) ?? []), row]);
  }
}
for (const [key, tests] of discoveryTests) {
  const [sessionID, specPath] = key.split(':');
  const owners = discoveryOwners.get(key) ?? [];
  if (sessionID.startsWith('related-one-all-')) {
    if (!owners.length) addIssue(`configured native variant has no explicit source case owner: ${key}`);
    continue;
  }
  if (sessionID === 'main' && registeredSpecPaths.has(specPath)) continue;
  if (sessionID === 'main' && infrastructureSpecPaths.has(specPath)) continue;
  if (!owners.length) {
    addIssue(`discovered test has no explicit source/registry/infrastructure owner: ${key}`);
    continue;
  }
  if (tests.length > 1 && owners.length < tests.length) addIssue(`discovered repeated-title variants are missing source case rows: ${key} has ${owners.length}/${tests.length}`);
}
const expectedOwnerRows = ledger.sourceScope.expectedDiscoveryCaseOwners ?? [];
const ownerKey = (item) => stableJson({
  ownerKind: item.ownerKind,
  sourcePath: item.sourcePath,
  scenarioID: item.scenarioID,
  caseName: item.caseName,
  expectedVariants: item.expectedVariants,
  sessionID: item.sessionID,
  specPath: item.specPath,
  testTitle: item.testTitle,
});
const expectedOwnerKeys = expectedOwnerRows.map(ownerKey).sort();
const actualOwnerKeys = actualDiscoveryOwnerRows.map(ownerKey).sort();
if (!expectedOwnerRows.length) addIssue('ledger omits the independent expected source-case/discovery-owner snapshot');
if (expectedOwnerKeys.length !== actualOwnerKeys.length || expectedOwnerKeys.some((key, index) => key !== actualOwnerKeys[index])) {
  const expectedSet = new Set(expectedOwnerKeys);
  const actualSet = new Set(actualOwnerKeys);
  const missing = expectedOwnerKeys.filter((key) => !actualSet.has(key)).length;
  const unexpected = actualOwnerKeys.filter((key) => !expectedSet.has(key)).length;
  addIssue(`source-case owner snapshot drift: ${missing} expected owner rows missing, ${unexpected} unexpected rows present`);
}
for (const consumer of ledger.oldBrowserLauncherConsumers) {
}

const uniqueOpen = [...new Set(open)].sort();
const uniqueIssues = [...new Set(issues)].sort();
for (const path of [...new Set(verifiedNewNativeSourceProvenances)].sort()) console.log(`VERIFIED new-native-source provenance: ${path}`);
console.log(`Standalone Playwright source ledger: ${ledger.counts.currentSourceFiles} verify files, ${ledger.counts.registryCases} registered cases, ${ledger.counts.oldBrowserLauncherConsumerSources} old launcher consumer sources, and ${ledger.counts.loomDevEmbeddedLaunchSites} additional loom-dev launch sites.`);
console.log(`Conversion: ${ledger.counts.convertedSources} source mappings complete, ${ledger.counts.pendingSourceMappingsOrDisposition} source rows pending, ${ledger.counts.pendingEmbeddedBrowserJourneys} embedded journeys pending; runtime evidence: ${ledger.runtimeEvidenceStatus}.`);
for (const issue of uniqueIssues) console.error(`ERROR ${issue}`);
for (const gap of uniqueOpen) console.log(`OPEN ${gap}`);
if (uniqueIssues.length || (requireComplete && uniqueOpen.length)) process.exitCode = 1;
