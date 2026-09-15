import test from 'node:test';
import assert from 'node:assert/strict';
import { createDevSession, createVerificationReport, sourceMountMatches } from './loom-dev.mjs';

test('development session defaults to isolated names, ports, and fixture', () => {
  const target = createDevSession({}, process.cwd());
  assert.equal(target.composeProject, 'loom-dev');
  assert.equal(target.fixtureProject, 'loom_dev_fixture');
  assert.equal(target.fixtureGeneration, 'fixture-v1');
  assert.equal(target.apiPort, 8180);
  assert.equal(target.uiPort, 3180);
  assert.match(target.fixtureDir, /testdata\/devloop-fixture$/);
  assert.notEqual(target.composeProject, 'loom-demo');
});

test('development session rejects canonical Compose and data targets', () => {
  assert.throws(
    () => createDevSession({ LOOM_DEV_COMPOSE_PROJECT: 'research-stack' }, process.cwd()),
    /must use the loom-dev namespace/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_PROJECT: 'another_research_dataset' }, process.cwd()),
    /must use the loom_dev_ namespace/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_COMPOSE_PROJECT: 'loom-demo' }, process.cwd()),
    /must not use the canonical loom-demo project/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_PROJECT: 'NCPI_ACCEPTANCE' }, process.cwd()),
    /must not target the canonical NCPI_ACCEPTANCE project/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_API_URL: 'http://example.test:8080' }, process.cwd()),
    /must point to this session's loopback port/,
  );
  assert.throws(
    () => createDevSession({ LOOM_DEV_HOST: '0.0.0.0' }, process.cwd()),
    /must be a loopback host/,
  );
});

test('verification report starts with an explicit build state and target ownership', () => {
  const target = createDevSession({}, process.cwd());
  const report = createVerificationReport(target);
  assert.equal(report.status, 'building');
  assert.equal(report.scenario, 'builder-preview-publish-viewer-filter-export');
  assert.equal(report.target.project, target.fixtureProject);
  assert.deepEqual(report.assertions, []);
  assert.deepEqual(report.timings, {});
  assert.deepEqual(report.evidencePaths, []);
});

test('Docker Desktop host mount normalization accepts only this checkout', () => {
  const checkout = '/private/tmp/loom-devloop-impl.cDPSXd';
  assert.equal(sourceMountMatches(`${checkout}/cmd`, `${checkout}/cmd`), true);
  assert.equal(sourceMountMatches(`/host_mnt${checkout}/cmd`, `${checkout}/cmd`), true);
  assert.equal(sourceMountMatches('/host_mnt/private/tmp/another-checkout/cmd', `${checkout}/cmd`), false);
});
