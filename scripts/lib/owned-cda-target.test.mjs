import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assertOwnedCdaTarget } from './owned-cda-target.mjs';

const composeProject = 'loom-dev-isolated-qa';
const sourceRoot = '/checkout';
const makeContainer = (name, service, port, mounts = []) => ({
  Name: `/${name}`,
  Config: { Labels: { 'com.docker.compose.project': composeProject, 'com.docker.compose.service': service } },
  State: { Running: true },
  NetworkSettings: { Ports: { '8080/tcp': [{ HostIp: '127.0.0.1', HostPort: String(port) }] } },
  Mounts: mounts.map(([Source, Destination]) => ({ Source, Destination })),
});
const containers = [
  makeContainer(`${composeProject}-loom-api-1`, 'loom-api', 8282, [['/checkout/cmd', '/workspace/cmd']]),
  makeContainer(`${composeProject}-loom-ui-1`, 'loom-ui', 30102, [['/checkout/ui/packages/loom-ui/src', '/workspace/packages/loom-ui/src']]),
  makeContainer(`${composeProject}-arangodb-1`, 'arangodb', 8529),
  makeContainer(`${composeProject}-clickhouse-1`, 'clickhouse', 8123),
];

function dockerFixture() {
  return args => {
    if (args[0] === 'ps') return containers.map(container => container.Name.slice(1)).join('\n');
    if (args[0] === 'inspect') return JSON.stringify(containers.filter(container => args.slice(1).includes(container.Name.slice(1))));
    throw new Error(`unexpected Docker call: ${args.join(' ')}`);
  };
}

const target = () => ({
  project: 'loom_dev_cda_fhir',
  apiOrigin: 'http://127.0.0.1:8282',
  uiOrigin: 'http://127.0.0.1:30102',
  apiContainer: `${composeProject}-loom-api-1`,
  composeProject,
  sourceRoot,
  arangoContainer: `${composeProject}-arangodb-1`,
  clickhouseContainer: `${composeProject}-clickhouse-1`,
});

test('owned CDA target requires matching local services, ports, source mounts, and raw-source containers', async () => {
  const result = await assertOwnedCdaTarget(target(), {
    docker: dockerFixture(),
    realpathImpl: async value => value,
  });
  assert.deepEqual(result, {
    project: 'loom_dev_cda_fhir', composeProject,
    apiContainer: `${composeProject}-loom-api-1`, uiContainer: `${composeProject}-loom-ui-1`,
    arangoContainer: `${composeProject}-arangodb-1`, clickhouseContainer: `${composeProject}-clickhouse-1`,
    apiPort: '8282', uiPort: '30102', sourceRoot,
  });
});

test('shared ports and a mismatched source mount are refused', async () => {
  const shared = target();
  shared.apiOrigin = 'http://127.0.0.1:8188';
  await assert.rejects(assertOwnedCdaTarget(shared, { docker: dockerFixture(), realpathImpl: async value => value }), /shared CDA port/);

  const wrongMount = target();
  const altered = containers.map(container => container.Config.Labels['com.docker.compose.service'] === 'loom-ui'
    ? { ...container, Mounts: [{ Source: '/other/ui/packages/loom-ui/src', Destination: '/workspace/packages/loom-ui/src' }] }
    : container);
  const docker = args => args[0] === 'ps'
    ? altered.map(container => container.Name.slice(1)).join('\n')
    : JSON.stringify(altered.filter(container => args.slice(1).includes(container.Name.slice(1))));
  await assert.rejects(assertOwnedCdaTarget(wrongMount, { docker, realpathImpl: async value => value }), /UI container must be mounted from this isolated source checkout/);
});
