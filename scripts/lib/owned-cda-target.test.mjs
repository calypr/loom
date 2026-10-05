import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assertOwnedCdaTarget } from './owned-cda-target.mjs';

const composeProject = 'loom-dev-6d7df93d6a37';
const sourceRoot = '/checkout';
const makeContainer = (name, service, port, mounts = []) => ({
  Name: `/${name}`,
  Config: { Labels: {
    'com.docker.compose.project': composeProject,
    'com.docker.compose.service': service,
    'com.docker.compose.project.working_dir': sourceRoot,
    'com.docker.compose.project.config_files': `${sourceRoot}/compose.dev.yaml`,
  } },
  State: { Running: true },
  NetworkSettings: { Ports: { '8080/tcp': [{ HostIp: '127.0.0.1', HostPort: String(port) }] } },
  Mounts: mounts.map(([Source, Destination]) => ({ Source, Destination })),
});
const containers = [
  makeContainer(`${composeProject}-loom-api-1`, 'loom-api', 8188, [['/checkout/cmd', '/workspace/cmd']]),
  makeContainer(`${composeProject}-loom-ui-1`, 'loom-ui', 30008, [['/checkout/ui/packages/loom-ui/src', '/workspace/packages/loom-ui/src']]),
  makeContainer(`${composeProject}-arangodb-1`, 'arangodb', 8529),
  makeContainer(`${composeProject}-clickhouse-1`, 'clickhouse', 8123),
];

function dockerFixture(selectedContainers = containers) {
  return args => {
    if (args[0] === 'ps') return selectedContainers.map(container => container.Name.slice(1)).join('\n');
    if (args[0] === 'inspect') return JSON.stringify(selectedContainers.filter(container => args.slice(1).includes(container.Name.slice(1))));
    throw new Error(`unexpected Docker call: ${args.join(' ')}`);
  };
}

const target = () => ({
  project: 'loom_dev_cda_fhir',
  apiOrigin: 'http://127.0.0.1:8188',
  uiOrigin: 'http://127.0.0.1:30008',
  apiContainer: `${composeProject}-loom-api-1`,
  composeProject,
  sourceRoot,
  arangoContainer: `${composeProject}-arangodb-1`,
  clickhouseContainer: `${composeProject}-clickhouse-1`,
});

test('owned CDA target accepts the current named stack when Compose identity and source mounts match this checkout', async () => {
  const result = await assertOwnedCdaTarget(target(), {
    docker: dockerFixture(),
    realpathImpl: async value => value,
  });
  assert.deepEqual(result, {
    project: 'loom_dev_cda_fhir', composeProject,
    apiContainer: `${composeProject}-loom-api-1`, uiContainer: `${composeProject}-loom-ui-1`,
    arangoContainer: `${composeProject}-arangodb-1`, clickhouseContainer: `${composeProject}-clickhouse-1`,
    apiPort: '8188', uiPort: '30008', sourceRoot,
  });
});

test('foreign Compose working directories, ports, and source mounts are refused', async () => {
  const foreignWorkingDirectory = containers.map(container => container.Config.Labels['com.docker.compose.service'] === 'loom-api'
    ? { ...container, Config: { ...container.Config, Labels: { ...container.Config.Labels,
      'com.docker.compose.project.working_dir': '/other-checkout' } } }
    : container);
  await assert.rejects(assertOwnedCdaTarget(target(), {
    docker: dockerFixture(foreignWorkingDirectory), realpathImpl: async value => value,
  }), /Compose project must be owned by this source checkout/);

  const foreignComposeFile = containers.map(container => container.Config.Labels['com.docker.compose.service'] === 'loom-ui'
    ? { ...container, Config: { ...container.Config, Labels: { ...container.Config.Labels,
      'com.docker.compose.project.config_files': '/other-checkout/compose.dev.yaml' } } }
    : container);
  await assert.rejects(assertOwnedCdaTarget(target(), {
    docker: dockerFixture(foreignComposeFile), realpathImpl: async value => value,
  }), /must use this checkout's compose.dev.yaml/);

  const wrongPort = target();
  wrongPort.apiOrigin = 'http://127.0.0.1:8282';
  await assert.rejects(assertOwnedCdaTarget(wrongPort, {
    docker: dockerFixture(), realpathImpl: async value => value,
  }), /must publish 8080\/tcp on 127\.0\.0\.1:8282/);

  const wrongMount = target();
  const altered = containers.map(container => container.Config.Labels['com.docker.compose.service'] === 'loom-ui'
    ? { ...container, Mounts: [{ Source: '/other/ui/packages/loom-ui/src', Destination: '/workspace/packages/loom-ui/src' }] }
    : container);
  const docker = args => args[0] === 'ps'
    ? altered.map(container => container.Name.slice(1)).join('\n')
    : JSON.stringify(altered.filter(container => args.slice(1).includes(container.Name.slice(1))));
  await assert.rejects(assertOwnedCdaTarget(wrongMount, { docker, realpathImpl: async value => value }), /UI container must be mounted from this isolated source checkout/);
});
