import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { loadOwnedCdaTargetConfig } from './verify-ui/helpers/owned-cda-target-config.mjs';
import {
  seedAndScopeScope001Fixture,
  startScope001FenceStub,
} from './verify-ui/helpers/scope-001-restricted-auth-bootstrap.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const defaultTarget = resolve(repositoryRoot, 'scripts/verify-ui/targets/scope-001-restricted-auth.target.example.json');
const composeFile = resolve(repositoryRoot, 'compose.dev.yaml');
const composeOverride = resolve(repositoryRoot, 'scripts/verify-ui/targets/scope-001-restricted-auth.compose.yaml');
const nativeBracket = resolve(repositoryRoot, 'scripts/run-native-verification-bracket.mjs');
const sharedGoVolumes = Object.freeze({
  loom_dev_go_module_cache: 'loom-dev-6d7df93d6a37_loom_dev_go_module_cache',
  loom_dev_go_build_cache: 'loom-dev-6d7df93d6a37_loom_dev_go_build_cache',
});
const privateBearerName = 'LOOM_SCOPE001_AUTH_BEARER';
const trustedIssuerName = 'LOOM_AUTH_CALYPR_TRUSTED_ISSUER';
const scope001FenceIssuerPattern = /^http:\/\/host\.docker\.internal:\d+$/;
const apiImageInputs = [
  'Dockerfile.dev', 'go.mod', 'go.sum', 'loom-dev-api-entrypoint.sh',
  'loom-dev-build.sh', 'loom-dev-build-stamp.sh',
];
const uiImageInputs = [
  'ui/apps/demo/Dockerfile.dev', 'ui/package.json', 'ui/package-lock.json',
  'ui/packages/loom-ui/package.json', 'ui/apps/demo/package.json',
];

const usage = [
  'Usage: node scripts/run-scope-001-restricted-auth.mjs --scenario <id> --case <name> --grep <native-test-regex> [--target <config>]',
  'Starts one isolated Calypr/Fence stack, runs one registered native case, and removes only that stack and its data volumes.',
].join('\n');

function fingerprintInputs(sourceRoot, relativePaths) {
  const hash = createHash('sha256');
  for (const relativePath of relativePaths) {
    hash.update(relativePath).update('\0').update(readFileSync(resolve(sourceRoot, relativePath))).update('\0');
  }
  return hash.digest('hex').slice(0, 24);
}

export function scope001ToolImageTags(sourceRoot) {
  return {
    api: fingerprintInputs(sourceRoot, apiImageInputs),
    ui: fingerprintInputs(sourceRoot, uiImageInputs),
  };
}

export function scope001RuntimeEnvironment(target, baseEnvironment = process.env, bearer, trustedIssuer) {
  const imageTags = scope001ToolImageTags(target.sourceRoot);
  const environment = {
    ...baseEnvironment,
    LOOM_CDA_SOURCE_ROOT: target.sourceRoot,
    LOOM_CDA_DATASET_DIR: target.datasetDir,
    LOOM_CDA_COMPOSE_PROJECT: target.composeProject,
    LOOM_CDA_PROJECT: target.project,
    LOOM_CDA_GENERATION: target.generation,
    LOOM_CDA_API_ORIGIN: target.apiOrigin,
    LOOM_CDA_UI_ORIGIN: target.uiOrigin,
    LOOM_CDA_API_PORT: String(target.apiPort),
    LOOM_CDA_UI_PORT: String(target.uiPort),
    LOOM_CDA_API_CONTAINER: target.apiContainer,
    LOOM_CDA_ARANGO_CONTAINER: target.arangoContainer,
    LOOM_CDA_CLICKHOUSE_CONTAINER: target.clickhouseContainer,
    LOOM_CDA_NO_AUTH: '0',
    LOOM_DEV_SOURCE_ROOT: target.sourceRoot,
    LOOM_DEV_FIXTURE_DIR: target.datasetDir,
    LOOM_DEV_COMPOSE_PROJECT: target.composeProject,
    LOOM_DEV_PROJECT: target.project,
    LOOM_DEV_GENERATION: target.generation,
    LOOM_DEV_HOST: '127.0.0.1',
    LOOM_DEV_API_PORT: String(target.apiPort),
    LOOM_DEV_UI_PORT: String(target.uiPort),
    LOOM_DEV_API_URL: target.apiOrigin,
    LOOM_DEV_UI_URL: target.uiOrigin,
    LOOM_ARANGO_DATABASE: target.arangoDatabase ?? 'loom_dev',
    LOOM_SCOPE001_API_IMAGE_TAG: imageTags.api,
    LOOM_SCOPE001_UI_IMAGE_TAG: imageTags.ui,
  };
  delete environment.LOOM_POPULATION_MAPPING_CURSOR_SECRET;
  delete environment[trustedIssuerName];
  if (trustedIssuer !== undefined) {
    assert.equal(typeof trustedIssuer, 'string', 'trusted Calypr issuer must be a string');
    assert(trustedIssuer.trim(), 'trusted Calypr issuer must not be empty');
    assert.match(trustedIssuer, scope001FenceIssuerPattern,
      'the restricted-auth runner must trust only its local Fence stub');
    environment[trustedIssuerName] = trustedIssuer;
  }
  if (bearer !== undefined) environment[privateBearerName] = bearer;
  else delete environment[privateBearerName];
  return environment;
}

export function scope001ComposeEnvironment(target, baseEnvironment = process.env, trustedIssuer) {
  const environment = scope001RuntimeEnvironment(target, baseEnvironment, undefined, trustedIssuer);
  environment.LOOM_POPULATION_MAPPING_CURSOR_SECRET = createHash('sha256')
    .update(`loom-scope001-restricted-auth-population-cursor\0${target.sourceRoot}\0${target.composeProject}\0${target.project}\0${target.generation}`)
    .digest('hex');
  return environment;
}

export function assertScope001Target(target) {
  assert.equal(target.project, 'loom_dev_scope001-auth');
  assert.equal(target.generation, 'scope001-auth-v1');
  assert.equal(target.composeProject, 'loom-dev-scope001-auth');
  assert.equal(target.noAuth, false, 'restricted auth target must run with Calypr auth enabled');
  assert.equal(target.arangoDatabase ?? 'loom_dev', 'loom_dev');
  assert.equal(target.apiContainer, `${target.composeProject}-loom-api-1`);
  assert.equal(target.arangoContainer, `${target.composeProject}-arangodb-1`);
  assert.equal(target.clickhouseContainer, `${target.composeProject}-clickhouse-1`);
  assert.equal(new URL(target.apiOrigin).hostname, '127.0.0.1');
  assert.equal(new URL(target.uiOrigin).hostname, '127.0.0.1');
  assert.equal(target.datasetDir, resolve(target.sourceRoot, 'testdata/scope-001-restricted-auth'));
  return true;
}

export function assertScope001ComposeIsolation(model, target, trustedIssuer) {
  assert.equal(model?.name, target.composeProject, 'Compose project must match the isolated auth target');
  assert.deepEqual(Object.keys(model.services ?? {}).sort(), ['arangodb', 'clickhouse', 'loom-api', 'loom-ui']);

  for (const [logicalName, expectedName] of Object.entries(sharedGoVolumes)) {
    const volume = model.volumes?.[logicalName];
    assert.equal(volume?.external, true, `${logicalName} must reuse the explicitly shared existing Go cache`);
    assert.equal(volume?.name, expectedName, `${logicalName} must not create a target-local cache copy`);
  }
  for (const logicalName of ['loom_dev_arangodb_data', 'loom_dev_clickhouse_data']) {
    const volume = model.volumes?.[logicalName];
    assert(volume && volume.external !== true, `${logicalName} must remain a target-owned data volume`);
    assert.equal(volume.name, `${target.composeProject}_${logicalName}`,
      `${logicalName} must be namespaced to the restricted-auth Compose project`);
  }

  assert.equal(typeof trustedIssuer, 'string', 'the local trusted Calypr issuer must be explicit');
  assert.match(trustedIssuer, scope001FenceIssuerPattern,
    'the restricted-auth target must use its local Fence stub as trusted issuer');
  assert.equal(model.services['loom-api'].environment?.[trustedIssuerName], trustedIssuer,
    'API container must receive the exact local trusted Calypr issuer');
  const apiMounts = model.services['loom-api'].volumes ?? [];
  const imageTags = scope001ToolImageTags(target.sourceRoot);
  assert.equal(model.services['loom-api'].image, `loom-dev-scope001-auth-api:${imageTags.api}`,
    'API toolchain image tag must match its pinned build inputs');
  assert.equal(model.services['loom-ui'].image, `loom-dev-scope001-auth-ui:${imageTags.ui}`,
    'UI dependency image tag must match its pinned build inputs');
  for (const [logicalName, containerPath] of [
    ['loom_dev_go_module_cache', '/go/pkg/mod'],
    ['loom_dev_go_build_cache', '/root/.cache/go-build'],
  ]) {
    assert(apiMounts.some(mount => mount.type === 'volume'
      && mount.source === logicalName && mount.target === containerPath),
    `${logicalName} must be mounted at its existing compiler-cache path`);
  }
  assert((model.services.arangodb.volumes ?? []).some(mount => mount.source === 'loom_dev_arangodb_data'
    && mount.target === '/var/lib/arangodb3'), 'ArangoDB must use its isolated target-owned data volume');
  assert((model.services.clickhouse.volumes ?? []).some(mount => mount.source === 'loom_dev_clickhouse_data'
    && mount.target === '/var/lib/clickhouse'), 'ClickHouse must use its isolated target-owned data volume');
  const apiConfig = apiMounts.find(mount => mount.target === '/workspace/scope-001-restricted-auth.server.yaml');
  const airConfig = apiMounts.find(mount => mount.target === '/workspace/.air.dev.toml');
  assert(apiConfig?.read_only, 'Calypr server config must be mounted read-only');
  assert(airConfig?.read_only, 'Air config must be mounted read-only');
  assert.equal(apiConfig.source, resolve(target.sourceRoot, 'scripts/verify-ui/targets/scope-001-restricted-auth.server.yaml'));
  assert.equal(airConfig.source, resolve(target.sourceRoot, 'scripts/verify-ui/targets/scope-001-restricted-auth.air.toml'));
  assert((model.services['loom-api'].extra_hosts ?? []).some(value =>
    value === 'host.docker.internal:host-gateway'
      || value === 'host.docker.internal=host-gateway'
      || value.host === 'host.docker.internal'),
  'API container must be able to reach the local Fence stub through the Docker host gateway');
  return true;
}

function parseCli(argv) {
  const { values, positionals } = parseArgs({
    args: argv,
    options: {
      help: { type: 'boolean' },
      target: { type: 'string' },
      scenario: { type: 'string' },
      case: { type: 'string' },
      grep: { type: 'string' },
    },
    allowPositionals: true,
  });
  assert.equal(positionals.length, 0, 'Unexpected positional arguments.');
  if (values.help) return { help: true };
  for (const name of ['scenario', 'case', 'grep']) {
    assert.equal(typeof values[name], 'string', `--${name} is required.`);
    assert(values[name].trim(), `--${name} must not be empty.`);
  }
  return { ...values, target: values.target ?? defaultTarget };
}

function runCaptured(command, args, { cwd, env, timeout = 30_000 } = {}) {
  try {
    return execFileSync(command, args, { cwd, env, encoding: 'utf8', timeout, maxBuffer: 8 * 1024 * 1024 });
  } catch (error) {
    const stderr = String(error?.stderr ?? '').replace(/(authorization|token|secret|password)(\s*[:=]\s*)\S+/gi, '$1$2[redacted]')
      .replace(/\bBearer\s+\S+/gi, 'Bearer [redacted]').replace(/\s+/g, ' ').trim().slice(0, 1000);
    const status = Number.isInteger(error?.status) ? `exit ${error.status}` : error?.signal ? `signal ${error.signal}` : String(error?.code ?? 'failed');
    throw new Error(`${command} preflight ${status}${stderr ? `: ${stderr}` : ''}`);
  }
}

async function runVisible(command, args, { cwd, env, timeoutMs = 10 * 60_000 } = {}) {
  const child = spawn(command, args, {
    cwd,
    env,
    detached: process.platform !== 'win32',
    stdio: 'inherit',
  });
  let timeout;
  let killTimeout;
  try {
    return await new Promise((resolvePromise, reject) => {
      child.once('error', reject);
      child.once('close', (code, signal) => resolvePromise({ code: code ?? 1, signal }));
      timeout = setTimeout(() => {
        if (process.platform !== 'win32' && child.pid) {
          try { process.kill(-child.pid, 'SIGTERM'); } catch {}
          killTimeout = setTimeout(() => {
            try { process.kill(-child.pid, 'SIGKILL'); } catch {}
          }, 1_000);
        } else child.kill('SIGTERM');
      }, timeoutMs);
    });
  } finally {
    clearTimeout(timeout);
    clearTimeout(killTimeout);
  }
}

function composeArgs(target, args) {
  return ['compose', '--project-name', target.composeProject, '--file', composeFile, '--file', composeOverride, ...args];
}

function volumeNames(target, composeEnvironment) {
  const model = JSON.parse(runCaptured('docker', composeArgs(target, ['config', '--format', 'json']), {
    cwd: target.sourceRoot, env: composeEnvironment,
  }));
  assertScope001ComposeIsolation(model, target, composeEnvironment[trustedIssuerName]);
  return {
    database: ['loom_dev_arangodb_data', 'loom_dev_clickhouse_data'].map(name => model.volumes[name].name),
    sharedCaches: Object.values(sharedGoVolumes),
  };
}

function missingToolImages(target, composeEnvironment) {
  const tags = scope001ToolImageTags(target.sourceRoot);
  const images = new Set(runCaptured('docker', ['image', 'ls', '--format', '{{.Repository}}:{{.Tag}}'], {
    cwd: target.sourceRoot, env: composeEnvironment,
  }).split(/\r?\n/).map(value => value.trim()).filter(Boolean));
  return [
    ...(!images.has(`loom-dev-scope001-auth-api:${tags.api}`) ? ['loom-api'] : []),
    ...(!images.has(`loom-dev-scope001-auth-ui:${tags.ui}`) ? ['loom-ui'] : []),
  ];
}

function listedVolumes(environment) {
  return runCaptured('docker', ['volume', 'ls', '--format', '{{.Name}}'], { env: environment })
    .split(/\r?\n/).map(name => name.trim()).filter(Boolean);
}

function verifyFreshOwnership(target, composeEnvironment, names) {
  const containers = runCaptured('docker', ['ps', '-aq', '--filter', `label=com.docker.compose.project=${target.composeProject}`], {
    env: composeEnvironment,
  }).trim();
  assert.equal(containers, '', 'restricted-auth Compose project already owns containers; refusing to reuse it');
  const volumes = new Set(listedVolumes(composeEnvironment));
  for (const name of names.database) assert(!volumes.has(name), `refusing to reuse prior target data volume ${name}`);
  for (const name of names.sharedCaches) assert(volumes.has(name), `required shared Go cache volume is missing: ${name}`);
}

async function main(argv = process.argv.slice(2), { write = console.log } = {}) {
  const options = parseCli(argv);
  if (options.help) {
    write(usage);
    return 0;
  }
  const loaded = await loadOwnedCdaTargetConfig({
    targetPath: options.target,
    repositoryRoot,
    expectedIdentity: { project: 'loom_dev_scope001-auth', generation: 'scope001-auth-v1' },
    env: {},
  });
  const target = loaded.target;
  assertScope001Target(target);
  for (const path of [composeFile, composeOverride, nativeBracket]) {
    assert(existsSync(path), `missing restricted-auth launcher input: ${path}`);
  }

  let fence;
  let composeEnvironment;
  let names;
  let composeMayOwnResources = false;
  let exitCode = 1;
  let cleanupError;
  try {
    fence = await startScope001FenceStub();
    composeEnvironment = scope001ComposeEnvironment(target, process.env, fence.issuer);
    names = volumeNames(target, composeEnvironment);
    verifyFreshOwnership(target, composeEnvironment, names);

    const nativeEnvironment = scope001RuntimeEnvironment(target, process.env, fence.bearer, fence.issuer);
    assert.equal(nativeEnvironment.LOOM_CDA_NO_AUTH, '0');
    const missingImages = missingToolImages(target, composeEnvironment);
    if (missingImages.length) {
      const build = await runVisible('docker', composeArgs(target, ['build', ...missingImages]), {
        cwd: target.sourceRoot,
        env: composeEnvironment,
      });
      if (build.code !== 0) throw new Error(`restricted-auth toolchain image build failed (${build.signal ?? build.code})`);
    }
    composeMayOwnResources = true;
    const up = await runVisible('docker', composeArgs(target, ['up', '-d', '--no-build', '--wait', '--wait-timeout', '600']), {
      cwd: target.sourceRoot,
      env: composeEnvironment,
    });
    if (up.code !== 0) throw new Error(`restricted-auth Compose startup failed (${up.signal ?? up.code})`);

    const processList = runCaptured('docker', ['top', target.apiContainer, '-eo', 'pid,args'], { env: composeEnvironment });
    const { assertScope001RestrictedAuthRuntime } = await import('./verify-ui/helpers/scope-001-restricted-auth-runtime.mjs');
    assertScope001RestrictedAuthRuntime({ apiContainer: target.apiContainer, dockerTop: () => processList });

    const fixtureEvidence = await seedAndScopeScope001Fixture({ target, bearer: fence.bearer });
    write(`Restricted-auth fixture ready: state ${fixtureEvidence.generationState}; ${fixtureEvidence.allowedDocumentCount} allowed and ${fixtureEvidence.blockedDocumentCount} blocked Observation documents remain loaded; ${fixtureEvidence.subjectEdgeCount} subject edges remain readable. Token and resource bodies were not retained.`);

    const bracket = await runVisible(process.execPath, [nativeBracket,
      '--scenario', options.scenario,
      '--case', options.case,
      '--grep', options.grep,
      '--target', options.target,
    ], { cwd: target.sourceRoot, env: nativeEnvironment, timeoutMs: 20 * 60_000 });
    exitCode = bracket.code;
    const fenceCounts = fence.counts();
    assert(fenceCounts.authorizedRequests > 0, 'native restricted-auth workflow never reached the local Fence /user endpoint');
    assert.equal(fenceCounts.rejectedRequests, 0, 'native restricted-auth workflow sent a bearer rejected by its local Fence stub');
    write(`Local Fence requests: ${fenceCounts.authorizedRequests} authorized; ${fenceCounts.rejectedRequests} rejected. Synthetic token and response bodies were not retained.`);
  } catch (error) {
    write(`Restricted-auth target failed: ${error instanceof Error ? error.message : 'unknown failure'}`);
    exitCode = 1;
  } finally {
    if (composeMayOwnResources) {
      try {
        const down = await runVisible('docker', composeArgs(target, ['down', '--volumes', '--remove-orphans']), {
          cwd: target.sourceRoot,
          env: composeEnvironment,
          timeoutMs: 2 * 60_000,
        });
        if (down.code !== 0) throw new Error(`owned Compose teardown exited ${down.signal ?? down.code}`);
        const remainingVolumes = new Set(listedVolumes(composeEnvironment));
        for (const name of names.database) assert(!remainingVolumes.has(name), `owned data volume remains after teardown: ${name}`);
        for (const name of names.sharedCaches) assert(remainingVolumes.has(name), `shared compiler cache was removed: ${name}`);
      } catch (error) {
        cleanupError = error;
      }
    }
    if (fence) {
      try { await fence.close(); } catch (error) { cleanupError ??= error; }
    }
  }
  if (cleanupError) {
    write(`Restricted-auth cleanup failed: ${cleanupError instanceof Error ? cleanupError.message : 'unknown failure'}`);
    return 1;
  }
  return exitCode;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().then(code => { process.exitCode = code; }).catch(error => {
    console.error(`Restricted-auth launcher rejected: ${error instanceof Error ? error.message : 'unknown failure'}`);
    process.exitCode = 1;
  });
}
