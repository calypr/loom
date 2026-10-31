import assert from 'node:assert/strict';

export function validateOwnedArangoContainer(container) {
  assert.equal(container?.Name?.replace(/^\//, ''), 'loom-dev-6d7df93d6a37-arangodb-1', 'unexpected Arango container name');
  assert.equal(container?.State?.Running, true, 'owned Arango container must already be running');
  const labels = container.Config?.Labels ?? {};
  assert.equal(labels['com.docker.compose.project'], 'loom-dev-6d7df93d6a37', 'Arango container belongs to another Compose project');
  assert.equal(labels['com.docker.compose.service'], 'arangodb', 'container is not the expected Arango service');
  assert((container.Config?.Env ?? []).includes('ARANGO_NO_AUTH=1'), 'owned local Arango must use the no-auth development configuration');
}
