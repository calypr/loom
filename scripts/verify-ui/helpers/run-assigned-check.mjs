#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { lstatSync, realpathSync } from 'node:fs';
import { isAbsolute, relative, resolve, sep } from 'node:path';

const CHECKS_ONLY_ENTRYPOINT = 'scripts/run-native-verification-bracket.mjs';

function reject(message) {
  console.error(`refusing check: ${message}`);
  process.exit(64);
}

const args = process.argv.slice(2);
const separator = args.indexOf('--');
const optionArgs = separator < 0 ? args : args.slice(0, separator);
const checkArgs = separator < 0 ? [] : args.slice(separator + 1);
const options = new Map();
for (let index = 0; index < optionArgs.length; index += 2) {
  const name = optionArgs[index];
  const value = optionArgs[index + 1];
  if (!['--expected-root', '--forbidden-root'].includes(name) || !value || options.has(name)) {
    reject('usage: run-assigned-check.mjs --expected-root <path> --forbidden-root <path> [-- node --check|--test <file> | git diff --check | node scripts/run-native-verification-bracket.mjs --scenario <id> --case <name> --checks-only]');
  }
  options.set(name, value);
}
if (!options.has('--expected-root') || !options.has('--forbidden-root')) {
  reject('both --expected-root and --forbidden-root are required');
}

let expectedRoot;
let forbiddenRoot;
let actualRoot;
let gitRoot;
try {
  expectedRoot = realpathSync(options.get('--expected-root'));
  forbiddenRoot = realpathSync(options.get('--forbidden-root'));
  actualRoot = realpathSync(process.cwd());
  gitRoot = realpathSync(execFileSync('git', ['rev-parse', '--show-toplevel'], { encoding: 'utf8' }).trim());
} catch (error) {
  reject(`could not resolve assigned worktree identity: ${error.message}`);
}

if (actualRoot === forbiddenRoot || gitRoot === forbiddenRoot) {
  reject(`forbidden live checkout ${forbiddenRoot}`);
}
if (actualRoot !== expectedRoot || gitRoot !== expectedRoot) {
  reject(`expected physical cwd and Git top-level ${expectedRoot}; got cwd ${actualRoot}, Git top-level ${gitRoot}`);
}

console.log(`PASS assigned worktree ${expectedRoot}`);
if (checkArgs.length === 0) {
  process.exit(0);
}

const [command, ...commandArgs] = checkArgs;
if ((command === 'node' || command === process.execPath)
  && ['--check', '--test'].includes(commandArgs[0])
  && commandArgs.length === 2) {
  let checkFile;
  try {
    checkFile = realpathSync(resolve(expectedRoot, commandArgs[1]));
  } catch (error) {
    reject(`check file must exist inside the assigned worktree: ${error.message}`);
  }
  const checkRelative = relative(expectedRoot, checkFile);
  if (checkRelative === '..' || checkRelative.startsWith(`..${sep}`) || isAbsolute(checkRelative)) {
    reject(`check file must be inside the assigned worktree: ${checkFile}`);
  }
  execFileSync(process.execPath, [commandArgs[0], checkFile], { cwd: expectedRoot, stdio: 'inherit' });
  process.exit(0);
}
if (command === 'git' && commandArgs.length === 2 && commandArgs[0] === 'diff' && commandArgs[1] === '--check') {
  execFileSync('git', ['diff', '--check'], { cwd: expectedRoot, stdio: 'inherit' });
  process.exit(0);
}

if ((command === 'node' || command === process.execPath)
  && commandArgs[0]?.endsWith('run-native-verification-bracket.mjs')) {
  if (commandArgs[0] !== CHECKS_ONLY_ENTRYPOINT) {
    reject(`the registered checks-only entrypoint must be the relative path ${CHECKS_ONLY_ENTRYPOINT}`);
  }

  const entrypointPath = resolve(expectedRoot, CHECKS_ONLY_ENTRYPOINT);
  let entrypointInfo;
  let realEntrypoint;
  try {
    entrypointInfo = lstatSync(entrypointPath);
    realEntrypoint = realpathSync(entrypointPath);
  } catch (error) {
    reject(`registered checks-only entrypoint must exist in the assigned worktree: ${error.message}`);
  }
  if (entrypointInfo.isSymbolicLink() || realEntrypoint !== entrypointPath) {
    reject('registered checks-only entrypoint must be a regular file in the assigned worktree, not a symlink');
  }
  if (!entrypointInfo.isFile()) {
    reject('registered checks-only entrypoint must be a regular file in the assigned worktree');
  }
  if (!commandArgs.slice(1).includes('--checks-only')) {
    reject('browser mode is not permitted through the assigned-check guard; pass --checks-only');
  }

  let scenario;
  let caseName;
  let checksOnly = false;
  for (let index = 1; index < commandArgs.length; index += 1) {
    const name = commandArgs[index];
    if (name === '--checks-only') {
      if (checksOnly) reject('--checks-only may be supplied only once');
      checksOnly = true;
      continue;
    }
    if (name === '--scenario' || name === '--case') {
      const value = commandArgs[index + 1];
      if (!value || value.startsWith('--')) reject(`${name} requires a value`);
      const normalized = value.trim();
      if (!normalized) reject(`${name} requires a non-empty value`);
      if (name === '--scenario') {
        if (scenario !== undefined) reject('--scenario may be supplied only once');
        scenario = normalized;
      } else {
        if (caseName !== undefined) reject('--case may be supplied only once');
        caseName = normalized;
      }
      index += 1;
      continue;
    }
    reject(`unsupported checks-only argument ${name}`);
  }

  if (!checksOnly) reject('registered checks-only mode requires --checks-only');
  if (!scenario || !caseName) reject('registered checks-only mode requires --scenario and --case');

  execFileSync(process.execPath, [
    entrypointPath,
    '--scenario', scenario,
    '--case', caseName,
    '--checks-only',
  ], { cwd: expectedRoot, stdio: 'inherit' });
  process.exit(0);
}

reject('only node --check, node --test, git diff --check, and the registered checks-only entrypoint may run through this entrypoint');
