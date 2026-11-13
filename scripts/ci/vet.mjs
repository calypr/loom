import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const root = path.resolve(import.meta.dirname, '../..');

export function goVet({ packagePatterns = ['./...'], analyzerArgs = [], overlayFile } = {}) {
  if (!Array.isArray(packagePatterns) || packagePatterns.length === 0) throw new Error('packagePatterns required');
  if (packagePatterns.some(pattern => pattern.startsWith('-'))) throw new Error('packagePatterns must not start with - (vet flag escape)');
  if (analyzerArgs.some(argument => !String(argument).startsWith('-')) || analyzerArgs.some(argument => argument.toLowerCase().includes('overlay')))
    throw new Error('analyzerArgs must be vet analyzer flags; use overlayFile for the overlay flag');
  const args = ['vet'];
  if (overlayFile) args.push(`-overlay=${overlayFile}`);
  const result = spawnSync('go', [...args, ...analyzerArgs, ...packagePatterns], {
    cwd: root, encoding: 'utf8', timeout: 20 * 60 * 1000, maxBuffer: 32 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  return { ok: result.status === 0, status: result.status, output: `${result.stdout ?? ''}${result.stderr ?? ''}` };
}

export function vetGate({ packagePatterns = ['./...'], overlayFile } = {}) {
  const passes = [
    { name: 'default', ...goVet({ packagePatterns, overlayFile }) },
    { name: 'copylocks', ...goVet({ packagePatterns, analyzerArgs: ['-copylocks'], overlayFile }) },
  ];
  return { passed: passes.every(pass => pass.ok), passes };
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const extra = process.argv.slice(2);
  if (extra.length !== 0 && !(extra.length === 1 && extra[0].startsWith('--packages=')))
    throw new Error('Usage: node scripts/ci/vet.mjs [--packages=a/...,b/...]');
  const packagePatterns = extra.length
    ? extra[0].slice('--packages='.length).split(',').map(pattern => pattern.trim()).filter(Boolean)
    : ['./...'];
  const result = vetGate({ packagePatterns });
  for (const pass of result.passes) if (!pass.ok) process.stdout.write(`--- go vet ${pass.name} FAILED ---\n${pass.output}\n`);
  process.stdout.write(`${result.passed ? 'VET-GATE PASS' : 'VET-GATE FAIL'} packages=${packagePatterns.join(',')}\n`);
  process.exitCode = result.passed ? 0 : 1;
}
