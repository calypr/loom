import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assertOwnedCdaTarget } from '../owned-cda-target.mjs';

const composeProject = 'loom-dev-6d7df93d6a37';
const sourceRoot = '/checkout';
const arangoVolumeName = `${composeProject}_loom_dev_arangodb_data`;
const clickhouseVolumeName = `${composeProject}_loom_dev_clickhouse_data`;
const makeContainer = (name, service, port, mounts = [], workingDirectory = sourceRoot) => ({
  Name: `/${name}`,
  Config: { Labels: {
    'com.docker.compose.project': composeProject,
    'com.docker.compose.service': service,
    'com.docker.compose.project.working_dir': workingDirectory,
    'com.docker.compose.project.config_files': `${workingDirectory}/compose.dev.yaml`,
  } },
  State: { Running: true },
  NetworkSettings: { Ports: { '8080/tcp': [{ HostIp: '127.0.0.1', HostPort: String(port) }] } },
  Mounts: mounts.map(([Source, Destination, Type = 'bind', Name]) => ({ Source, Destination, Type, ...(Name ? { Name } : {}) })),
});
const containers = [
  makeContainer(`${composeProject}-loom-api-1`, 'loom-api', 8188, [['/checkout/cmd', '/workspace/cmd']]),
  makeContainer(`${composeProject}-loom-ui-1`, 'loom-ui', 30008, [['/checkout/ui/packages/loom-ui/src', '/workspace/packages/loom-ui/src']]),
  makeContainer(`${composeProject}-arangodb-1`, 'arangodb', 8529, [
    [arangoVolumeName, '/var/lib/arangodb3', 'volume', arangoVolumeName],
    ['anonymous-arango-apps-volume', '/var/lib/arangodb3-apps', 'volume', 'anonymous-arango-apps-volume'],
  ], '/private/tmp/loom-feature-catalog-rebuild'),
  makeContainer(`${composeProject}-clickhouse-1`, 'clickhouse', 8123, [
    [clickhouseVolumeName, '/var/lib/clickhouse', 'volume', clickhouseVolumeName],
  ], '/private/tmp/loom-feature-catalog-rebuild'),
];
const volumes = [
  { Name: arangoVolumeName, Labels: { 'com.docker.compose.project': composeProject, 'com.docker.compose.volume': 'loom_dev_arangodb_data' } },
  { Name: clickhouseVolumeName, Labels: { 'com.docker.compose.project': composeProject, 'com.docker.compose.volume': 'loom_dev_clickhouse_data' } },
];

function dockerFixture(selectedContainers = containers, selectedVolumes = volumes) {
  return args => {
    if (args[0] === 'ps') return selectedContainers.map(container => container.Name.slice(1)).join('\n');
    if (args[0] === 'inspect') return JSON.stringify(selectedContainers.filter(container => args.slice(1).includes(container.Name.slice(1))));
    if (args[0] === 'volume' && args[1] === 'inspect') return JSON.stringify(selectedVolumes.filter(volume => args.slice(2).includes(volume.Name)));
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

test('owned CDA target accepts checkout-owned API/UI with historically labelled DB containers and owned named volumes', async () => {
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
  await assert.rejects(assertOwnedCdaTarget(wrongMount, {
    docker: dockerFixture(altered), realpathImpl: async value => value,
  }), /UI container must be mounted from this isolated source checkout/);
});

test('database primary mounts reject foreign names, projects, and logical volume labels', async () => {
  const foreignMount = containers.map(container => container.Config.Labels['com.docker.compose.service'] === 'arangodb'
    ? { ...container, Mounts: container.Mounts.map(mount => mount.Destination === '/var/lib/arangodb3'
      ? { ...mount, Name: 'other-project_loom_dev_arangodb_data' }
      : mount) }
    : container);
  await assert.rejects(assertOwnedCdaTarget(target(), {
    docker: dockerFixture(foreignMount), realpathImpl: async value => value,
  }), /primary data volume must be loom-dev-6d7df93d6a37_loom_dev_arangodb_data/);

  const foreignProjectVolume = volumes.map(volume => volume.Name === arangoVolumeName
    ? { ...volume, Labels: { ...volume.Labels, 'com.docker.compose.project': 'other-compose-project' } }
    : volume);
  await assert.rejects(assertOwnedCdaTarget(target(), {
    docker: dockerFixture(containers, foreignProjectVolume), realpathImpl: async value => value,
  }), /belongs to a different Compose project/);

  const foreignLogicalVolume = volumes.map(volume => volume.Name === clickhouseVolumeName
    ? { ...volume, Labels: { ...volume.Labels, 'com.docker.compose.volume': 'other-volume' } }
    : volume);
  await assert.rejects(assertOwnedCdaTarget(target(), {
    docker: dockerFixture(containers, foreignLogicalVolume), realpathImpl: async value => value,
  }), /is not Compose volume loom_dev_clickhouse_data/);
});

test('database containers still require exact Compose project, service, and running identity', async () => {
  const foreignProject = containers.map(container => container.Config.Labels['com.docker.compose.service'] === 'clickhouse'
    ? { ...container, Config: { ...container.Config, Labels: { ...container.Config.Labels,
      'com.docker.compose.project': 'other-compose-project' } } }
    : container);
  await assert.rejects(assertOwnedCdaTarget(target(), {
    docker: dockerFixture(foreignProject), realpathImpl: async value => value,
  }), /Container Compose ownership changed/);

  const wrongService = containers.map(container => container.Config.Labels['com.docker.compose.service'] === 'arangodb'
    ? { ...container, Config: { ...container.Config, Labels: { ...container.Config.Labels,
      'com.docker.compose.service': 'other-service' } } }
    : container);
  await assert.rejects(assertOwnedCdaTarget(target(), {
    docker: dockerFixture(wrongService), realpathImpl: async value => value,
  }), /not service arangodb/);

  const stopped = containers.map(container => container.Config.Labels['com.docker.compose.service'] === 'clickhouse'
    ? { ...container, State: { Running: false } }
    : container);
  await assert.rejects(assertOwnedCdaTarget(target(), {
    docker: dockerFixture(stopped), realpathImpl: async value => value,
  }), /must already be running/);
});
