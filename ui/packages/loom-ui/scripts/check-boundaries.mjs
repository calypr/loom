import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';

const root = new URL('../src/', import.meta.url);
const forbidden = /(?:@gen3\/core|@gen3\/frontend|from ['"]next(?:\/|['"])|ProtectedContent|CalyprAuth)/;
const effectImports = /import\s*\{[^}]*\b(?:useEffect|useLayoutEffect|useDeepCompareEffect)\b[^}]*\}\s*from\s*['"]react['"]/s;
const files = [];
const walk = async (url) => {
  for (const entry of await readdir(url, { withFileTypes: true })) {
    const child = new URL(entry.name + (entry.isDirectory() ? '/' : ''), url);
    if (entry.isDirectory()) await walk(child);
    else if (/\.(ts|tsx)$/.test(entry.name)) files.push(child);
  }
};
await walk(root);
const violations = [];
for (const file of files) {
  const source = await readFile(file, 'utf8');
  if (forbidden.test(source)) violations.push(`${file.pathname}: forbidden Calypr dependency`);
  if (/Builder\.tsx$|Viewer\.tsx$/.test(file.pathname) && effectImports.test(source)) violations.push(`${file.pathname}: direct React effect import`);
}
if (violations.length) {
  console.error(violations.join('\n'));
  process.exit(1);
}
console.log(`Loom UI boundary check passed (${files.length} TypeScript files).`);
