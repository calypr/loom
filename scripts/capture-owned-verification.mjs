import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

function option(name) {
  const index = process.argv.indexOf(name);
  return index < 0 ? undefined : process.argv[index + 1];
}

const phase = option('--phase');
assert(['before', 'after'].includes(phase), 'Use --phase before|after.');
const precheckInput = option('--precheck-input');
if (phase === 'before') {
  assert(precheckInput, 'Before capture requires --precheck-input from owned-stack-verification --mode precheck.');
} else {
  assert(!precheckInput, '--precheck-input is only valid for --phase before.');
}
const outputPaths = {
  source: option('--source-output'),
  docs: option('--docs-output'),
  api: option('--api-output'),
  mounts: option('--mount-output'),
};
for (const [name, outputPath] of Object.entries(outputPaths)) {
  assert(outputPath, `Provide --${name === 'mounts' ? 'mount' : name}-output with the path for the ${phase} capture.`);
}
assert.equal(new Set(Object.values(outputPaths).map(path => resolve(path))).size, 4,
  'Source, docs, API, and mount outputs must use four distinct paths.');
if (precheckInput) {
  const precheckPath = realpathSync(resolve(precheckInput));
  for (const outputPath of Object.values(outputPaths)) {
    let candidate = resolve(outputPath);
    const missingSegments = [];
    let outputDestination;
    for (;;) {
      try {
        outputDestination = resolve(realpathSync(candidate), ...missingSegments);
        break;
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
        const parent = dirname(candidate);
        assert.notEqual(parent, candidate, `Could not resolve capture output path: ${outputPath}`);
        missingSegments.unshift(basename(candidate));
        candidate = parent;
      }
    }
    assert.notEqual(outputDestination, precheckPath,
      `Precheck input must not alias a capture output path: ${outputPath}`);
  }
}

const env = process.env;
const sourceRoot = realpathSync(resolve(env.LOOM_CDA_SOURCE_ROOT ?? ''));
const requiredEnvironment = [
  'LOOM_CDA_SOURCE_ROOT', 'LOOM_CDA_PROJECT', 'LOOM_CDA_API_ORIGIN', 'LOOM_CDA_UI_ORIGIN',
  'LOOM_CDA_API_CONTAINER', 'LOOM_CDA_COMPOSE_PROJECT',
  'LOOM_CDA_ARANGO_CONTAINER', 'LOOM_CDA_CLICKHOUSE_CONTAINER',
];
for (const name of requiredEnvironment) assert(env[name], `Set ${name} in the owned environment file.`);

const importCanonical = async path => import(pathToFileURL(join(sourceRoot, path)).href);
const [
  { assertOwnedCdaTarget },
  { startVerificationIdentity },
  { sourceFingerprintWithManifest },
  { assertFreshApiBuildPrecheck },
] = await Promise.all([
  importCanonical('scripts/verify-ui/helpers/owned-cda-target.mjs'),
  importCanonical('scripts/verify-ui/helpers/cda-verification-identity.mjs'),
  importCanonical('scripts/verify-ui/helpers/source-fingerprint.mjs'),
  importCanonical('scripts/verify-ui/helpers/owned-stack-health.mjs'),
]);

const precheckRecord = phase === 'before'
  ? JSON.parse(readFileSync(resolve(precheckInput), 'utf8'))
  : undefined;
const precheckIdentity = phase === 'before'
  ? assertFreshApiBuildPrecheck(precheckRecord, { targetContainer: env.LOOM_CDA_API_CONTAINER.trim() })
  : undefined;

function docsFingerprint(root) {
  const directory = join(root, 'docs');
  const manifest = {};
  const hash = createHash('sha256');
  let files = 0;
  const visit = relativePath => {
    const absolute = join(directory, relativePath);
    const stat = lstatSync(absolute);
    if (stat.isDirectory()) {
      for (const entry of readdirSync(absolute).sort()) visit(join(relativePath, entry));
      return;
    }
    if (!stat.isFile()) return;
    const content = readFileSync(absolute);
    const path = `docs/${relativePath.split('/').join('/')}`;
    hash.update(path);
    hash.update('\0');
    hash.update(content);
    manifest[path] = createHash('sha256').update(content).digest('hex');
    files += 1;
  };
  for (const entry of readdirSync(directory).sort()) visit(entry);
  return { fingerprint: { sha256: hash.digest('hex'), files }, manifest };
}

const capturedAt = new Date().toISOString();
const ownedTarget = await assertOwnedCdaTarget({
  project: env.LOOM_CDA_PROJECT,
  apiOrigin: env.LOOM_CDA_API_ORIGIN,
  uiOrigin: env.LOOM_CDA_UI_ORIGIN,
  apiContainer: env.LOOM_CDA_API_CONTAINER,
  composeProject: env.LOOM_CDA_COMPOSE_PROJECT,
  sourceRoot,
  arangoContainer: env.LOOM_CDA_ARANGO_CONTAINER,
  clickhouseContainer: env.LOOM_CDA_CLICKHOUSE_CONTAINER,
});
const identity = await startVerificationIdentity(sourceRoot, env.LOOM_CDA_API_CONTAINER);
if (precheckIdentity) {
  assertFreshApiBuildPrecheck(precheckRecord, {
    targetContainer: env.LOOM_CDA_API_CONTAINER.trim(),
    apiBuildIdentity: identity.apiBuildIdentity,
  });
}
const source = sourceFingerprintWithManifest(sourceRoot);
assert.deepEqual(source.fingerprint, identity.sourceFingerprint,
  'Source manifest and verification identity must describe the same watched tree.');
const docs = docsFingerprint(sourceRoot);
const identityCheck = await identity.finish();
assert.deepEqual(sourceFingerprintWithManifest(sourceRoot).fingerprint, source.fingerprint,
  'Source changed while the capture wrapper was running.');
assert.deepEqual(docsFingerprint(sourceRoot), docs,
  'Docs changed while the capture wrapper was running.');

const target = { ...ownedTarget, generation: env.LOOM_CDA_GENERATION ?? null };
const outputs = {
  source: { capturedAt, phase, root: sourceRoot, fingerprint: source.fingerprint, manifest: source.manifest },
  docs: { capturedAt, phase, root: sourceRoot, ...docs },
  api: {
    capturedAt,
    phase,
    target,
    apiBuildIdentity: identity.apiBuildIdentity,
    sourceFingerprint: source.fingerprint,
    identityCheck,
    ...(precheckRecord ? {
      apiBuildPrecheck: {
        ...precheckRecord,
        verifiedCapturedApiBuildIdentity: identity.apiBuildIdentity,
      },
    } : {}),
  },
  mounts: {
    capturedAt,
    phase,
    status: 'PASS',
    validator: 'assertOwnedCdaTarget',
    expectedComposeProject: env.LOOM_CDA_COMPOSE_PROJECT,
    expectedSourceRoot: sourceRoot,
    target,
  },
};
for (const [name, outputPath] of Object.entries(outputPaths)) {
  const absolutePath = resolve(outputPath);
  await import('node:fs/promises').then(({ mkdir, writeFile }) => mkdir(dirname(absolutePath), { recursive: true }).then(() =>
    writeFile(absolutePath, `${JSON.stringify(outputs[name], null, 2)}\n`, { mode: 0o600 })));
}
console.log(JSON.stringify({
  phase,
  sourceRoot,
  sourceFingerprint: source.fingerprint,
  docsFingerprint: docs.fingerprint,
  apiBuildIdentity: identity.apiBuildIdentity,
  mountValidation: { status: 'PASS', validator: 'assertOwnedCdaTarget', target },
  outputPaths: Object.fromEntries(Object.entries(outputPaths).map(([name, path]) => [name, resolve(path)])),
}, null, 2));
