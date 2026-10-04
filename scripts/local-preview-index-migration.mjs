import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const defaultSourceRoot = resolve(scriptDirectory, '..');
const defaultManifest = join(scriptDirectory, 'local-preview-index-migration/manifest.json');
const helperSource = join(scriptDirectory, 'local-preview-index-migration.go');
const expected = Object.freeze({
  composeProject: 'loom-dev-6d7df93d6a37',
  apiContainer: 'loom-dev-6d7df93d6a37-loom-api-1',
  arangoContainer: 'loom-dev-6d7df93d6a37-arangodb-1',
});

function parseArgs(args) {
  const options = {
    manifest: defaultManifest,
    sourceRoot: resolve(process.env.LOOM_LOCAL_INDEX_SOURCE_ROOT ?? defaultSourceRoot),
    apply: false,
    authorized: false,
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--apply') options.apply = true;
    else if (arg === '--authorize-local-migration') options.authorized = true;
    else if (arg === '--dry-run') options.apply = false;
    else if (['--manifest', '--compiler-spec', '--report', '--source-root'].includes(arg)) {
      const value = args[++index];
      if (!value) throw new Error(`${arg} requires a path`);
      const optionName = arg.slice(2).replaceAll('-', '');
      options[optionName === 'sourceroot' ? 'sourceRoot' : optionName] = resolve(value);
    } else {
      throw new Error(`unknown option ${arg}`);
    }
  }
  if (options.apply !== options.authorized) throw new Error('--apply and --authorize-local-migration must be supplied together');
  if (!options.compilerspec) throw new Error('--compiler-spec is required and must be the exact emitted PreviewCoveringIndexSpec for the native-shaped query');
  return options;
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    encoding: 'utf8',
    timeout: options.timeout ?? 30000,
    maxBuffer: 8 * 1024 * 1024,
    ...options,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status}): ${result.stderr || result.stdout}`);
  return result.stdout;
}

function inspectContainer(name, service) {
  const rows = JSON.parse(run('docker', ['inspect', name]));
  assert.equal(rows.length, 1, `docker inspect must resolve exactly ${name}`);
  const container = rows[0];
  const labels = container.Config?.Labels ?? {};
  assert.equal(container.Name?.replace(/^\//, ''), name, 'container name changed');
  assert.equal(container.State?.Running, true, `${name} must already be running`);
  assert.equal(labels['com.docker.compose.project'], expected.composeProject, `${name} belongs to a different Compose project`);
  assert.equal(labels['com.docker.compose.service'], service, `${name} is not the expected ${service} service`);
  return container;
}

function sharedComposeNetwork(api, arango) {
  const apiNetworks = Object.keys(api.NetworkSettings?.Networks ?? {});
  const arangoNetworks = new Set(Object.keys(arango.NetworkSettings?.Networks ?? {}));
  const shared = apiNetworks.filter((network) => arangoNetworks.has(network));
  assert(shared.length > 0, 'owned API and Arango containers must share a Compose network');
  return shared.sort();
}

async function verifyApiSourceMount(api, sourceRoot) {
  const root = await realpath(sourceRoot);
  const mounts = api.Mounts ?? [];
  const internal = mounts.find((mount) => mount.Destination === '/workspace/internal');
  const module = mounts.find((mount) => mount.Destination === '/workspace/go.mod');
  assert(internal && module, 'owned API container must mount internal/ and go.mod from the active source checkout');
  assert.equal(await realpath(internal.Source), join(root, 'internal'), 'API internal source mount differs from this checkout');
  assert.equal(await realpath(module.Source), join(root, 'go.mod'), 'API module source mount differs from this checkout');
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const [manifestText, compilerSpecText, helperText] = await Promise.all([
    readFile(options.manifest, 'utf8'),
    readFile(options.compilerspec, 'utf8'),
    readFile(helperSource, 'utf8'),
  ]);
  const manifest = JSON.parse(manifestText);
  const compilerSpec = JSON.parse(compilerSpecText);
  assert.equal(manifest.authorization?.composeProjectLabel, expected.composeProject);
  assert.equal(manifest.authorization?.apiContainer, expected.apiContainer);
  assert.equal(manifest.authorization?.arangoContainer, expected.arangoContainer);
  assert.equal(manifest.authorization?.notCompilerInferredSupersedence, true);
  assert.equal(compilerSpec.collection, 'Observation');
  assert.equal(typeof compilerSpec.name, 'string');
  assert(Array.isArray(compilerSpec.fields) && Array.isArray(compilerSpec.storedValues));

  const api = inspectContainer(expected.apiContainer, 'loom-api');
  const arango = inspectContainer(expected.arangoContainer, 'arangodb');
  await verifyApiSourceMount(api, options.sourceRoot);
  const networks = sharedComposeNetwork(api, arango);
  const reportPath = options.report ?? `/tmp/loom-local-preview-index-migration-${Date.now()}.json`;
  const work = await mkdtemp(join(tmpdir(), 'loom-local-preview-index-migration-'));
  const remote = `/tmp/loom-local-preview-index-migration-${randomUUID()}`;
  const virtualSource = '/workspace/scripts/local-preview-index-migration.go';
  const overlay = { Replace: { [virtualSource]: `${remote}/main.go` } };
  let helperReport;
  let scriptError;
  try {
    await Promise.all([
      writeFile(join(work, 'main.go'), helperText),
      writeFile(join(work, 'manifest.json'), manifestText),
      writeFile(join(work, 'compiler-spec.json'), compilerSpecText),
      writeFile(join(work, 'overlay.json'), JSON.stringify(overlay)),
    ]);
    run('docker', ['exec', expected.apiContainer, 'mkdir', '-p', remote]);
    for (const file of ['main.go', 'manifest.json', 'compiler-spec.json', 'overlay.json']) {
      run('docker', ['cp', join(work, file), `${expected.apiContainer}:${remote}/${file}`]);
    }

    const helperArgs = [
      'go', 'run', `-overlay=${remote}/overlay.json`, './scripts/local-preview-index-migration.go',
      `--manifest=${remote}/manifest.json`, `--compiler-spec=${remote}/compiler-spec.json`,
      '--endpoint=http://arangodb:8529',
    ];
    if (options.apply) helperArgs.push('--apply', '--authorize-local-migration');
    const command = `cd /workspace && GOTOOLCHAIN=local GOCACHE=${shellQuote(`${remote}/gocache`)} ${helperArgs.map(shellQuote).join(' ')}`;
    const result = spawnSync('docker', ['exec', expected.apiContainer, 'sh', '-lc', command], {
      encoding: 'utf8', timeout: 120000, maxBuffer: 8 * 1024 * 1024,
    });
    if (result.error) throw result.error;
    try { helperReport = JSON.parse(result.stdout); } catch { /* include process output in the failure report below */ }
    if (result.status !== 0) throw new Error(`Go migration helper failed (${result.status}): ${result.stderr || result.stdout}`);
    if (!helperReport) throw new Error(`Go migration helper returned invalid JSON: ${result.stdout}`);
  } catch (error) {
    scriptError = error;
  } finally {
    try { execFileSync('docker', ['exec', expected.apiContainer, 'rm', '-rf', remote], { encoding: 'utf8', timeout: 10000 }); } catch { /* the report retains the primary operation result */ }
    await rm(work, { recursive: true, force: true });
  }

  const report = {
    schemaVersion: 1,
    operation: 'owned local index migration for verification setup',
    authorizationBoundary: 'Explicit local setup authorization only; not compiler-inferred same-plan supersedence.',
    applyRequested: options.apply,
    composeProject: expected.composeProject,
    sourceRoot: await realpath(options.sourceRoot),
    apiContainer: expected.apiContainer,
    arangoContainer: expected.arangoContainer,
    sharedComposeNetworks: networks,
    compilerSpecPath: options.compilerspec,
    compilerSpecSHA256: createHash('sha256').update(compilerSpecText).digest('hex'),
    manifestPath: options.manifest,
    helper: helperReport,
    error: scriptError?.message,
  };
  await writeFile(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  process.stdout.write(`${JSON.stringify({ reportPath, ...report }, null, 2)}\n`);
  if (scriptError) process.exitCode = 1;
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

await main();
