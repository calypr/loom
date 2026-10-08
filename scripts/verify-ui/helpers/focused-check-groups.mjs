import path from 'node:path';

const RUNNER_CWD = Object.freeze({
  'node-test': '.',
  vitest: 'ui/packages/loom-ui',
});
const NODE_TEST_EXTENSION = /\.test\.(?:cjs|js|mjs)$/i;
const VITEST_EXTENSION = /\.(?:test|spec)\.(?:[cm]?[jt]sx?)$/i;
const GROUP_ID = /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/;

function fail(location, message) {
  throw new TypeError(`Invalid focused check ${location}: ${message}`);
}

function repoRelativePath(value, location, { allowRoot = false } = {}) {
  if (typeof value !== 'string' || value.length === 0 || value.includes('\0')) {
    fail(location, 'expected a non-empty repository-relative path');
  }
  if (value.includes('\\') || path.posix.isAbsolute(value) || path.win32.isAbsolute(value) || /^[A-Za-z]:/.test(value)) {
    fail(location, 'absolute paths and backslashes are not allowed');
  }
  if (allowRoot && value === '.') return value;

  const parts = value.split('/');
  if (parts.some((part) => part === '' || part === '.' || part === '..')) {
    fail(location, 'path must not contain empty, current-directory, or parent-directory segments');
  }
  if (parts.some((part) => part.startsWith('-'))) {
    fail(location, 'path segments must not start with a dash');
  }
  return value;
}

function assertInsideRoot(root, relative, location) {
  const absolute = path.resolve(root, ...relative.split('/'));
  const fromRoot = path.relative(root, absolute);
  if (fromRoot === '..' || fromRoot.startsWith(`..${path.sep}`) || path.isAbsolute(fromRoot)) {
    fail(location, 'path resolves outside the repository root');
  }
  return absolute;
}

function packageRelative(file, location) {
  const packagePath = 'ui/packages/loom-ui';
  const relative = path.posix.relative(packagePath, file);
  if (relative === '..' || relative.startsWith('../') || path.posix.isAbsolute(relative)) {
    fail(location, 'Vitest files must be inside ui/packages/loom-ui');
  }
  return relative;
}

function parseCommand(command, location) {
  if (!Array.isArray(command) || command.length === 0) {
    fail(`${location}.command`, 'expected a command array');
  }
  const runner = command[0];
  if (runner !== 'node-test' && runner !== 'vitest') {
    fail(`${location}.command[0]`, 'expected allowlisted runner "node-test" or "vitest"');
  }

  if (runner === 'node-test') {
    if (command.length < 2) fail(`${location}.command`, 'node-test requires at least one test file');
    return { runner, fileOffset: 1 };
  }

  if (command.length < 5 || command[1] !== 'run' || command[2] !== '--config' || command[3] !== 'vitest.config.ts') {
    fail(`${location}.command`, 'Vitest command must begin with vitest run --config vitest.config.ts and include test files');
  }
  return { runner, fileOffset: 4 };
}

/**
 * Validate command-array metadata and create argv-safe process plans.
 * This function performs no filesystem access and starts no processes.
 */
export function planFocusedCheckGroups(groups, repoRoot) {
  if (!Array.isArray(groups)) fail('groups', 'expected an array');
  if (typeof repoRoot !== 'string' || !path.isAbsolute(repoRoot)) {
    fail('repoRoot', 'expected an absolute path');
  }

  const root = path.resolve(repoRoot);
  const seenIDs = new Set();
  return groups.map((group, index) => {
    const location = `groups[${index}]`;
    if (group === null || typeof group !== 'object' || Array.isArray(group)) {
      fail(location, 'expected an object');
    }
    if (typeof group.id !== 'string' || !GROUP_ID.test(group.id)) {
      fail(`${location}.id`, 'expected a lowercase hyphen-separated identifier');
    }
    if (seenIDs.has(group.id)) fail(`${location}.id`, `duplicate identifier ${group.id}`);
    seenIDs.add(group.id);

    const { runner, fileOffset } = parseCommand(group.command, location);
    const expectedCwd = RUNNER_CWD[runner];
    const cwd = repoRelativePath(group.cwd, `${location}.cwd`, { allowRoot: true });
    if (cwd !== expectedCwd) fail(`${location}.cwd`, `runner ${runner} must use ${expectedCwd}`);
    const absoluteCwd = assertInsideRoot(root, cwd, `${location}.cwd`);

    const seenFiles = new Set();
    const files = [];
    for (let fileIndex = fileOffset; fileIndex < group.command.length; fileIndex += 1) {
      const fileLocation = `${location}.command[${fileIndex}]`;
      const file = repoRelativePath(group.command[fileIndex], fileLocation);
      if (seenFiles.has(file)) fail(fileLocation, `duplicate file ${file}`);
      seenFiles.add(file);
      assertInsideRoot(root, file, fileLocation);

      if (runner === 'node-test' && !NODE_TEST_EXTENSION.test(file)) {
        fail(fileLocation, 'node-test files must end in .test.js, .test.cjs, or .test.mjs');
      }
      if (runner === 'vitest') {
        if (!VITEST_EXTENSION.test(file)) {
          fail(fileLocation, 'Vitest files must use a .test or .spec JavaScript/TypeScript extension');
        }
        files.push(packageRelative(file, fileLocation));
      } else {
        files.push(file);
      }
    }
    if (group.sourceFiles !== undefined) {
      if (!Array.isArray(group.sourceFiles)) fail(`${location}.sourceFiles`, 'expected an array of repository-relative paths');
      for (let sourceIndex = 0; sourceIndex < group.sourceFiles.length; sourceIndex += 1) {
        const sourcePath = repoRelativePath(group.sourceFiles[sourceIndex], `${location}.sourceFiles[${sourceIndex}]`);
        assertInsideRoot(root, sourcePath, `${location}.sourceFiles[${sourceIndex}]`);
        if (seenFiles.has(sourcePath)) fail(`${location}.sourceFiles[${sourceIndex}]`, `duplicate file ${sourcePath}`);
        seenFiles.add(sourcePath);
      }
    }
    if (files.length === 0) fail(`${location}.command`, 'expected at least one test file');

    const args = runner === 'node-test'
      ? ['--test', ...files]
      : ['../../node_modules/vitest/vitest.mjs', 'run', '--config', 'vitest.config.ts', ...files];
    return {
      id: group.id,
      runner,
      executable: process.execPath,
      args,
      cwd: absoluteCwd,
    };
  });
}
