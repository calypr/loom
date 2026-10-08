import assert from 'node:assert/strict';
import { readFile, realpath, stat } from 'node:fs/promises';
import { isAbsolute, resolve } from 'node:path';

const configFields = [
  'schemaVersion',
  'sourceRoot',
  'datasetDir',
  'composeProject',
  'project',
  'generation',
  'apiOrigin',
  'uiOrigin',
  'apiContainer',
  'arangoContainer',
  'clickhouseContainer',
  'noAuth',
];
const localHosts = new Set(['127.0.0.1', 'localhost', '[::1]']);

function requireString(value, field) {
  assert.equal(typeof value, 'string', `${field} must be a string.`);
  assert(value.trim(), `${field} must not be empty.`);
  assert.equal(value, value.trim(), `${field} must not contain surrounding whitespace.`);
  return value;
}

function requireName(value, field, pattern) {
  requireString(value, field);
  assert.match(value, pattern, `${field} contains unsupported characters.`);
  return value;
}

function parseLocalOrigin(value, field) {
  let url;
  try {
    url = new URL(requireString(value, field));
  } catch {
    throw new Error(`${field} must be a valid local HTTP origin.`);
  }
  assert(['http:', 'https:'].includes(url.protocol), `${field} must use HTTP or HTTPS.`);
  assert(!url.username && !url.password, `${field} must not embed credentials.`);
  assert(localHosts.has(url.hostname), `${field} must target a local owned stack.`);
  assert(url.port, `${field} must include its explicit published port.`);
  assert(url.pathname === '/', `${field} must be an origin without a path.`);
  assert(url.search === '', `${field} must not include a query.`);
  assert(url.hash === '', `${field} must not include a fragment.`);
  return { origin: url.origin, port: url.port };
}

function validateConfigShape(config) {
  assert(config && typeof config === 'object' && !Array.isArray(config), 'Owned CDA target config must be a JSON object.');
  const unknown = Object.keys(config).filter((field) => !configFields.includes(field));
  const missing = configFields.filter((field) => !Object.hasOwn(config, field));
  assert.equal(unknown.length, 0, `Unsupported target config fields: ${unknown.join(', ')}.`);
  assert.equal(missing.length, 0, `Missing target config fields: ${missing.join(', ')}.`);
  assert.equal(config.schemaVersion, 1, 'Owned CDA target config schemaVersion must be 1.');
  assert.equal(typeof config.noAuth, 'boolean', 'noAuth must be a boolean.');
}

function validateExpectedIdentity(expectedIdentity) {
  assert(expectedIdentity && typeof expectedIdentity === 'object' && !Array.isArray(expectedIdentity),
    'The selected registry case must provide an independent targetIdentity contract.');
  requireName(expectedIdentity.project, 'Registry targetIdentity.project', /^[A-Za-z0-9_-]+$/);
  requireName(expectedIdentity.generation, 'Registry targetIdentity.generation', /^[A-Za-z0-9_.-]+$/);
}

function validateEnvironmentOverrides(env, expected) {
  for (const [name, value] of Object.entries(expected)) {
    if (!Object.hasOwn(env, name)) continue;
    if (String(env[name]) !== value) {
      throw new Error(`Conflicting inherited target environment variable ${name}; use the selected target config consistently.`);
    }
  }
}

/**
 * Load one explicitly selected machine-local CDA target config and bind it to
 * the selected case's independent registry identity before Docker/browser work.
 */
export async function loadOwnedCdaTargetConfig({
  targetPath,
  repositoryRoot,
  expectedIdentity,
  env = process.env,
} = {}) {
  requireString(targetPath, 'targetPath');
  requireString(repositoryRoot, 'repositoryRoot');
  validateExpectedIdentity(expectedIdentity);

  const repository = await realpath(repositoryRoot);
  const requestedConfigPath = resolve(isAbsolute(targetPath) ? targetPath : resolve(repository, targetPath));
  const configPath = await realpath(requestedConfigPath);
  const configInfo = await stat(configPath);
  assert(configInfo.isFile(), 'Owned CDA target config must be a regular file.');

  let config;
  try {
    config = JSON.parse(await readFile(configPath, 'utf8'));
  } catch {
    throw new Error('Owned CDA target config must contain valid JSON; config contents are omitted.');
  }
  validateConfigShape(config);

  const sourceRoot = await realpath(resolve(repository, requireString(config.sourceRoot, 'sourceRoot')));
  assert.equal(sourceRoot, repository, 'sourceRoot must resolve to the repository checkout being verified.');
  const datasetDir = await realpath(resolve(repository, requireString(config.datasetDir, 'datasetDir')));
  assert((await stat(datasetDir)).isDirectory(), 'datasetDir must resolve to a directory.');

  const project = requireName(config.project, 'project', /^[A-Za-z0-9_-]+$/);
  const generation = requireName(config.generation, 'generation', /^[A-Za-z0-9_.-]+$/);
  assert(project === expectedIdentity.project,
    'Target project does not match the selected case registry targetIdentity.');
  assert(generation === expectedIdentity.generation,
    'Target generation does not match the selected case registry targetIdentity.');

  const composeProject = requireName(config.composeProject, 'composeProject', /^[A-Za-z0-9_.-]+$/);
  const apiContainer = requireName(config.apiContainer, 'apiContainer', /^[A-Za-z0-9_.-]+$/);
  const arangoContainer = requireName(config.arangoContainer, 'arangoContainer', /^[A-Za-z0-9_.-]+$/);
  const clickhouseContainer = requireName(config.clickhouseContainer, 'clickhouseContainer', /^[A-Za-z0-9_.-]+$/);
  assert.equal(new Set([apiContainer, arangoContainer, clickhouseContainer]).size, 3,
    'API, ArangoDB, and ClickHouse container names must be distinct.');

  const api = parseLocalOrigin(config.apiOrigin, 'apiOrigin');
  const ui = parseLocalOrigin(config.uiOrigin, 'uiOrigin');
  assert(api.origin !== ui.origin, 'API and UI origins must be distinct.');
  assert(api.port !== ui.port, 'API and UI ports must be distinct.');

  const target = Object.freeze({
    sourceRoot,
    datasetDir,
    composeProject,
    project,
    generation,
    apiOrigin: api.origin,
    apiPort: api.port,
    uiOrigin: ui.origin,
    uiPort: ui.port,
    apiContainer,
    arangoContainer,
    clickhouseContainer,
    noAuth: config.noAuth,
  });
  const environment = Object.freeze({
    LOOM_CDA_SOURCE_ROOT: target.sourceRoot,
    LOOM_CDA_DATASET_DIR: target.datasetDir,
    LOOM_CDA_COMPOSE_PROJECT: target.composeProject,
    LOOM_CDA_PROJECT: target.project,
    LOOM_CDA_GENERATION: target.generation,
    LOOM_CDA_API_ORIGIN: target.apiOrigin,
    LOOM_CDA_UI_ORIGIN: target.uiOrigin,
    LOOM_CDA_API_PORT: target.apiPort,
    LOOM_CDA_UI_PORT: target.uiPort,
    LOOM_CDA_API_CONTAINER: target.apiContainer,
    LOOM_CDA_ARANGO_CONTAINER: target.arangoContainer,
    LOOM_CDA_CLICKHOUSE_CONTAINER: target.clickhouseContainer,
    LOOM_CDA_NO_AUTH: target.noAuth ? '1' : '0',
    LOOM_DEV_SOURCE_ROOT: target.sourceRoot,
    LOOM_DEV_COMPOSE_PROJECT: target.composeProject,
    LOOM_DEV_PROJECT: target.project,
    LOOM_DEV_GENERATION: target.generation,
    LOOM_DEV_API_URL: target.apiOrigin,
    LOOM_DEV_UI_URL: target.uiOrigin,
  });
  validateEnvironmentOverrides(env, environment);

  return Object.freeze({
    target,
    environment,
    configPath,
    validationScope: 'configuration-only',
    runtimeDatasetIdentity: 'not-checked',
  });
}
