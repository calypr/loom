#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { realpathSync } from 'node:fs';
import { isAbsolute, relative, resolve, sep } from 'node:path';

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
    reject('usage: run-assigned-check.mjs --expected-root <path> --forbidden-root <path> [-- node --check|--test <file> | git diff --check]');
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
reject('only node --check, node --test, and git diff --check may run through this entrypoint');
