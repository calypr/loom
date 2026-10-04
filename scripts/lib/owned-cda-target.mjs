import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { realpath } from 'node:fs/promises';

const sharedPrefix = 'loom-dev-6d7df93d6a37';
const localHosts = new Set(['127.0.0.1', 'localhost', '::1']);

function validateOrigin(name, raw, forbiddenPort) {
  assert(raw, `Set ${name} to the owned isolated CDA stack.`);
  const url = new URL(raw);
  assert(['http:', 'https:'].includes(url.protocol), `${name} must use HTTP or HTTPS`);
  assert(!url.username && !url.password, `${name} must not embed credentials`);
  assert(localHosts.has(url.hostname), `${name} must target a local isolated stack`);
  assert(url.port, `${name} must include its explicit published port`);
  assert.notEqual(url.port, forbiddenPort, `${name} must not use the shared CDA port ${forbiddenPort}`);
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
  assert(!composeProject.startsWith(sharedPrefix), 'Do not use the shared CDA Compose project');
  assert(apiContainer, 'Set LOOM_CDA_API_CONTAINER to the isolated Loom API container.');
  assert.match(apiContainer, /^[A-Za-z0-9_.-]+$/, 'LOOM_CDA_API_CONTAINER contains unsupported characters');
  assert(!apiContainer.startsWith(sharedPrefix), 'Do not use the shared CDA API container');
  assert(sourceRoot, 'sourceRoot must identify this verifier checkout');
  for (const [label, value] of [['ArangoDB', arangoContainer], ['ClickHouse', clickhouseContainer]]) {
    if (!value) continue;
    assert.match(value, /^[A-Za-z0-9_.-]+$/, `${label} container name contains unsupported characters`);
    assert(!value.startsWith(sharedPrefix), `Do not use the shared CDA ${label} container`);
  }

  const apiURL = validateOrigin('LOOM_CDA_API_ORIGIN', apiOrigin, '8188');
  const uiURL = validateOrigin('LOOM_CDA_UI_ORIGIN', uiOrigin, '30008');
  const names = docker(['ps', '--filter', `label=com.docker.compose.project=${composeProject}`, '--format', '{{.Names}}'])
    .trim().split('\n').filter(Boolean);
  assert(names.includes(apiContainer), `The named API container is not running in Compose project ${composeProject}`);
  for (const name of [arangoContainer, clickhouseContainer].filter(Boolean)) {
    assert(names.includes(name), `The named source container ${name} is not running in Compose project ${composeProject}`);
  }
  const containers = inspectContainers(names, docker);
  const byName = new Map(containers.map(container => [container.Name?.replace(/^\//, ''), container]));
  const api = byName.get(apiContainer);
  const ui = containers.find(container => container.Config.Labels['com.docker.compose.service'] === 'loom-ui');
  assert(api, 'The named API container is missing from the isolated Compose project');
  assert(ui, 'The isolated Compose project must have a Loom UI container');
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
    assert.equal(container.State.Running, true, `Container ${name} must already be running`);
  }
  publishedPort(api, apiURL.port, 'loom-api');
  publishedPort(ui, uiURL.port, 'loom-ui');

  const hostMount = async (container, destination) => {
    const source = container.Mounts?.find(mount => mount.Destination === destination)?.Source;
    assert(source, `Owned ${container.Config.Labels['com.docker.compose.service']} service is missing source mount ${destination}`);
    const hostPath = source.startsWith('/host_mnt/') ? source.slice('/host_mnt'.length) : source;
    return realpathImpl(hostPath);
  };
  const source = await realpathImpl(sourceRoot);
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
