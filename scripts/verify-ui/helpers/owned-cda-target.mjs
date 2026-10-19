import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { realpath } from 'node:fs/promises';

const localHosts = new Set(['127.0.0.1', 'localhost', '::1']);

function validateOrigin(name, raw) {
  assert(raw, `Set ${name} to the owned isolated CDA stack.`);
  const url = new URL(raw);
  assert(['http:', 'https:'].includes(url.protocol), `${name} must use HTTP or HTTPS`);
  assert(!url.username && !url.password, `${name} must not embed credentials`);
  assert(localHosts.has(url.hostname), `${name} must target a local isolated stack`);
  assert(url.port, `${name} must include its explicit published port`);
  assert(url.pathname === '/' && !url.search && !url.hash, `${name} must be an origin without path, query, or fragment`);
  return url;
}

function publishedPort(container, expectedHostPort, service) {
  assert.equal(container.Config.Labels['com.docker.compose.service'], service);
  const bindings = container.NetworkSettings.Ports?.['8080/tcp'] ?? [];
  assert(bindings.some(binding => binding.HostIp === '127.0.0.1' && binding.HostPort === expectedHostPort),
    `${service} must publish 8080/tcp on 127.0.0.1:${expectedHostPort}`);
}

function inspectContainers(names, docker) {
  return names.length ? JSON.parse(docker(['inspect', ...names])) : [];
}

function inspectVolumes(names, docker) {
  return names.length ? JSON.parse(docker(['volume', 'inspect', ...names])) : [];
}

/** Validate that browser/API calls and raw source oracles resolve to one isolated Compose project. */
export async function assertOwnedCdaTarget({
  project,
  apiOrigin,
  uiOrigin,
  apiContainer,
  composeProject,
  sourceRoot,
  arangoContainer,
  clickhouseContainer,
}, {
  docker = args => execFileSync('docker', args, { encoding: 'utf8', timeout: 15000 }),
  realpathImpl = realpath,
} = {}) {
  assert(project, 'Set LOOM_CDA_PROJECT explicitly to the project containing the loaded CDA dataset.');
  assert.match(project, /^[A-Za-z0-9_-]+$/, 'LOOM_CDA_PROJECT contains unsupported characters');
  assert(composeProject, 'Set LOOM_CDA_COMPOSE_PROJECT to the isolated Compose project.');
  assert.match(composeProject, /^[A-Za-z0-9_.-]+$/, 'LOOM_CDA_COMPOSE_PROJECT contains unsupported characters');
  assert(apiContainer, 'Set LOOM_CDA_API_CONTAINER to the isolated Loom API container.');
  assert.match(apiContainer, /^[A-Za-z0-9_.-]+$/, 'LOOM_CDA_API_CONTAINER contains unsupported characters');
  assert(sourceRoot, 'sourceRoot must identify this verifier checkout');
  for (const [label, value] of [['ArangoDB', arangoContainer], ['ClickHouse', clickhouseContainer]]) {
    if (!value) continue;
    assert.match(value, /^[A-Za-z0-9_.-]+$/, `${label} container name contains unsupported characters`);
  }

  const apiURL = validateOrigin('LOOM_CDA_API_ORIGIN', apiOrigin);
  const uiURL = validateOrigin('LOOM_CDA_UI_ORIGIN', uiOrigin);
  const names = docker(['ps', '--filter', `label=com.docker.compose.project=${composeProject}`, '--format', '{{.Names}}'])
    .trim().split('\n').filter(Boolean);
  assert(names.includes(apiContainer), `The named API container is not running in Compose project ${composeProject}`);
  for (const name of [arangoContainer, clickhouseContainer].filter(Boolean)) {
    assert(names.includes(name), `The named source container ${name} is not running in Compose project ${composeProject}`);
  }
  const containers = inspectContainers(names, docker);
  const byName = new Map(containers.map(container => [container.Name?.replace(/^\//, ''), container]));
  const source = await realpathImpl(sourceRoot);
  const composeFile = await realpathImpl(`${source}/compose.dev.yaml`);
  const api = byName.get(apiContainer);
  const uiServices = containers.filter(container => container.Config.Labels['com.docker.compose.service'] === 'loom-ui');
  assert(api, 'The named API container is missing from the isolated Compose project');
  assert.equal(uiServices.length, 1, 'The isolated Compose project must have exactly one Loom UI container');
  const [ui] = uiServices;
  const extras = [
    ...(arangoContainer ? [[arangoContainer, 'arangodb']] : []),
    ...(clickhouseContainer ? [[clickhouseContainer, 'clickhouse']] : []),
  ].map(([name, service]) => [name, byName.get(name), service]);
  const selected = [
    [apiContainer, api, 'loom-api'],
    [ui.Name.replace(/^\//, ''), ui, 'loom-ui'],
    ...extras,
  ];
  for (const [name, container, service] of selected) {
    assert(container, `Owned Compose container ${name} is missing`);
    const labels = container.Config.Labels ?? {};
    assert.equal(labels['com.docker.compose.project'], composeProject, 'Container Compose ownership changed');
    assert.equal(labels['com.docker.compose.service'], service, `Container ${name} is not service ${service}`);
    if (service === 'loom-api' || service === 'loom-ui') {
      const workingDirectory = labels['com.docker.compose.project.working_dir'];
      assert(workingDirectory, `Container ${name} has no Compose working-directory identity`);
      assert.equal(await realpathImpl(workingDirectory.startsWith('/host_mnt/')
        ? workingDirectory.slice('/host_mnt'.length) : workingDirectory), source,
        `Container ${name} Compose project must be owned by this source checkout`);
      const configuredFiles = String(labels['com.docker.compose.project.config_files'] ?? '')
        .split(',').map(value => value.trim().replace(/^['"]|['"]$/g, '')).filter(Boolean);
      const resolvedConfigFiles = await Promise.all(configuredFiles.map(file =>
        realpathImpl(file.startsWith('/host_mnt/') ? file.slice('/host_mnt'.length) : file)));
      assert(resolvedConfigFiles.includes(composeFile),
        `Container ${name} Compose project must use this checkout's compose.dev.yaml`);
    }
    assert.equal(container.State.Running, true, `Container ${name} must already be running`);
  }

  const databaseVolumeSpecs = [
    ...(arangoContainer ? [[byName.get(arangoContainer), '/var/lib/arangodb3', 'loom_dev_arangodb_data']] : []),
    ...(clickhouseContainer ? [[byName.get(clickhouseContainer), '/var/lib/clickhouse', 'loom_dev_clickhouse_data']] : []),
  ];
  const expectedVolumes = databaseVolumeSpecs.map(([container, destination, logicalName]) => {
    const service = container.Config.Labels['com.docker.compose.service'];
    const matches = (container.Mounts ?? []).filter(mount => mount.Destination === destination);
    assert.equal(matches.length, 1, `${service} must have exactly one primary data mount at ${destination}`);
    const [mount] = matches;
    assert.equal(mount.Type, 'volume', `${service} primary data mount must be a named Docker volume`);
    const name = `${composeProject}_${logicalName}`;
    assert.equal(mount.Name, name, `${service} primary data volume must be ${name}`);
    return { name, logicalName };
  });
  const volumes = inspectVolumes(expectedVolumes.map(volume => volume.name), docker);
  const volumeByName = new Map(volumes.map(volume => [volume.Name, volume]));
  for (const { name, logicalName } of expectedVolumes) {
    const volume = volumeByName.get(name);
    assert(volume, `Owned persistent volume ${name} is missing`);
    assert.equal(volume.Labels?.['com.docker.compose.project'], composeProject,
      `Persistent volume ${name} belongs to a different Compose project`);
    assert.equal(volume.Labels?.['com.docker.compose.volume'], logicalName,
      `Persistent volume ${name} is not Compose volume ${logicalName}`);
  }

  publishedPort(api, apiURL.port, 'loom-api');
  publishedPort(ui, uiURL.port, 'loom-ui');

  const hostMount = async (container, destination) => {
    const source = container.Mounts?.find(mount => mount.Destination === destination)?.Source;
    assert(source, `Owned ${container.Config.Labels['com.docker.compose.service']} service is missing source mount ${destination}`);
    const hostPath = source.startsWith('/host_mnt/') ? source.slice('/host_mnt'.length) : source;
    return realpathImpl(hostPath);
  };
  assert.equal(await hostMount(api, '/workspace/cmd'), await realpathImpl(`${source}/cmd`),
    'API container must be mounted from this isolated source checkout');
  assert.equal(await hostMount(ui, '/workspace/packages/loom-ui/src'), await realpathImpl(`${source}/ui/packages/loom-ui/src`),
    'UI container must be mounted from this isolated source checkout');

  return {
    project,
    composeProject,
    apiContainer,
    uiContainer: ui.Name.replace(/^\//, ''),
    ...(arangoContainer ? { arangoContainer } : {}),
    ...(clickhouseContainer ? { clickhouseContainer } : {}),
    apiPort: apiURL.port,
    uiPort: uiURL.port,
    sourceRoot: source,
  };
}
