import test from 'node:test';
import assert from 'node:assert/strict';
import { assertOwnedDevSession } from '../../../loom-dev.mjs';
import { assertCdaExplicitUIRoute, createCdaNavigator } from '../cda-fixtures.mjs';

const target = {
  uiUrl: 'http://127.0.0.1:30008',
  fixtureProject: 'loom_dev_cda_fhir',
};
const otherSessionDefaults = {
  VITE_LOOM_PROJECT: 'loom_dev_c89a69d7e137',
  VITE_LOOM_EXPLORER: 'loom-dev-bootstrap',
};
const sessionTarget = {
  sourceRoot: '/fixture/source',
  composeProject: 'loom-cda-owned',
  composeFile: '/fixture/source/compose.dev.yaml',
  fixtureProject: target.fixtureProject,
  fixtureGeneration: 'cda-fhir-v1',
  host: '127.0.0.1',
  apiPort: 18188,
  uiPort: 30008,
};
const explicitMembershipRoute =
  'http://127.0.0.1:30008/?project=loom_dev_cda_fhir&explorer=qa-membership&mode=builder';

const fakeOwnedResourceCommands = (uiDefaults = otherSessionDefaults) => {
  const labels = service => ({
    'com.docker.compose.project': sessionTarget.composeProject,
    'com.docker.compose.service': service,
  });
  const api = {
    Id: 'api-id',
    Config: { Labels: labels('loom-api') },
    Mounts: [{ Source: `${sessionTarget.sourceRoot}/cmd`, Destination: '/workspace/cmd' }],
    NetworkSettings: { Ports: { '8080/tcp': [{ HostPort: String(sessionTarget.apiPort), HostIp: '127.0.0.1' }] } },
  };
  const ui = {
    Id: 'ui-id',
    Config: {
      Labels: labels('loom-ui'),
      Env: Object.entries(uiDefaults).map(([name, value]) => `${name}=${value}`),
    },
    Mounts: [{
      Source: `${sessionTarget.sourceRoot}/ui/packages/loom-ui/src`,
      Destination: '/workspace/packages/loom-ui/src',
    }],
    NetworkSettings: { Ports: { '8080/tcp': [{ HostPort: String(sessionTarget.uiPort), HostIp: '127.0.0.1' }] } },
  };
  const containers = [
    { Id: 'arangodb-id', Config: { Labels: labels('arangodb') } },
    { Id: 'clickhouse-id', Config: { Labels: labels('clickhouse') } },
    api,
    ui,
  ];

  return {
    compose: async args => {
      assert.deepEqual(args, ['ps', '-aq']);
      return { code: 0, stdout: containers.map(({ Id }) => Id).join('\n'), stderr: '' };
    },
    docker: async args => {
      if (args[0] === 'inspect') return { code: 0, stdout: JSON.stringify(containers), stderr: '' };
      if (args[0] === 'volume' && args[1] === 'ls') return { code: 0, stdout: '', stderr: '' };
      throw new Error(`unexpected Docker command: ${args.join(' ')}`);
    },
  };
};

test('owned session allows mismatched UI defaults only for explicit-query mode with owned resources intact', async () => {
  await assert.rejects(
    assertOwnedDevSession(sessionTarget, { resourceCommands: fakeOwnedResourceCommands() }),
    /development UI defaults do not target this session fixture and bootstrap Explorer/,
  );
  await assert.doesNotReject(assertOwnedDevSession(sessionTarget, {
    uiRouting: 'explicit-query',
    resourceCommands: fakeOwnedResourceCommands(),
  }));

  assert.deepEqual(assertCdaExplicitUIRoute(explicitMembershipRoute, target), {
    project: 'loom_dev_cda_fhir',
    explorer: 'qa-membership',
    mode: 'builder',
  });
});

test('Membership fixture navigator rejects bad route identity before calling Playwright', () => {
  const visits = [];
  const navigate = createCdaNavigator({
    target,
    uiRouting: 'explicit-query',
    navigateTo: url => { visits.push(url); return 'navigated'; },
  });

  assert.equal(navigate(explicitMembershipRoute), 'navigated');
  assert.deepEqual(visits, [explicitMembershipRoute]);
  assert.throws(() => navigate('http://127.0.0.1:30008/?explorer=qa-membership&mode=builder'));
  assert.deepEqual(visits, [explicitMembershipRoute]);
});

test('default-route session still requires the fixture project and bootstrap Explorer', async () => {
  await assert.rejects(assertOwnedDevSession(sessionTarget, {
    resourceCommands: fakeOwnedResourceCommands({
      VITE_LOOM_PROJECT: target.fixtureProject,
      VITE_LOOM_EXPLORER: 'another-bootstrap',
    }),
  }), /development UI defaults do not target this session fixture and bootstrap Explorer/);
  await assert.doesNotReject(assertOwnedDevSession(sessionTarget, {
    resourceCommands: fakeOwnedResourceCommands({
      VITE_LOOM_PROJECT: target.fixtureProject,
      VITE_LOOM_EXPLORER: 'loom-dev-bootstrap',
    }),
  }));
});

test('explicit CDA UI routing rejects malformed, missing, duplicate, or wrong scope values', async (t) => {
  const invalidRoutes = [
    ['malformed URL', 'not a URL'],
    ['foreign UI origin', explicitMembershipRoute.replace('127.0.0.1:30008', '127.0.0.1:30009')],
    ['missing project', 'http://127.0.0.1:30008/?explorer=qa-membership&mode=builder'],
    ['wrong project', explicitMembershipRoute.replace('loom_dev_cda_fhir', 'loom_dev_other')],
    ['duplicate project', `${explicitMembershipRoute}&project=loom_dev_cda_fhir`],
    ['missing Explorer', 'http://127.0.0.1:30008/?project=loom_dev_cda_fhir&mode=builder'],
    ['invalid Explorer', 'http://127.0.0.1:30008/?project=loom_dev_cda_fhir&explorer=bad%20id&mode=builder'],
    ['duplicate Explorer', `${explicitMembershipRoute}&explorer=another-explorer`],
    ['missing mode', 'http://127.0.0.1:30008/?project=loom_dev_cda_fhir&explorer=qa-membership'],
    ['non-Builder mode', explicitMembershipRoute.replace('mode=builder', 'mode=viewer')],
    ['nested route', explicitMembershipRoute.replace('/?', '/nested?')],
  ];

  for (const [name, route] of invalidRoutes) {
    await t.test(name, () => assert.throws(() => assertCdaExplicitUIRoute(route, target)));
  }
});
