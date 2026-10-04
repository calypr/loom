import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';

const root = resolve(new URL('..', import.meta.url).pathname);
const scripts = join(root, 'scripts');
const legacyDrivers = new Set([
  'scripts/lib/browser.mjs',
  'scripts/verify-ui/browser.mjs',
]);
const patterns = [
  ['legacy browser import', /\bfrom\s*['"][^'"]*\/browser\.mjs['"]/],
  ['legacy loom-dev browser import', /\bimport\s*\{[^}]*\b(?:browserEval|launchBrowser|navigate|waitForBrowser|snapshot)\b[^}]*\}\s*from\s*['"][^'"]*loom-dev\.mjs['"]/s],
  ['CDP command', /\bcdp\.send\s*\(/],
  ['CDP protocol command', /['"](?:Runtime\.evaluate|Page\.navigate|Page\.captureScreenshot|Input\.dispatch\w+|Network\.getResponseBody|Browser\.setDownloadBehavior)['"]/],
  ['Chrome DevTools transport', /chrome-remote-interface|remote-debugging-port/],
];

function* files(directory) {
  for (const name of readdirSync(directory).sort()) {
    if (name === 'node_modules' || name === '.artifacts') continue;
    const path = join(directory, name);
    if (statSync(path).isDirectory()) yield* files(path);
    else if (name.endsWith('.mjs')) yield path;
  }
}

const remaining = [];
for (const path of files(scripts)) {
  const name = relative(root, path);
  if (name === 'scripts/check-playwright-migration.mjs') continue;
  const source = readFileSync(path, 'utf8');
  const reasons = patterns.filter(([, pattern]) => pattern.test(source)).map(([label]) => label);
  if (legacyDrivers.has(name)) reasons.unshift('legacy driver file');
  if (reasons.length) remaining.push({ path: name, reasons });
}

for (const item of remaining) console.log(`${item.path}\t${item.reasons.join(', ')}`);
console.log(`${remaining.length} .mjs files still contain or call Chrome/CDP browser machinery`);
if (process.argv.includes('--check') && remaining.length) process.exitCode = 1;
