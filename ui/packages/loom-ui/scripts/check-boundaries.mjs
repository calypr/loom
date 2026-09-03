import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';

const root = new URL('../src/', import.meta.url);
const forbidden = /(?:@gen3\/core|@gen3\/frontend|from ['"]next(?:\/|['"])|ProtectedContent|CalyprAuth)/;
const effectNames = /\b(?:useEffect|useLayoutEffect|useInsertionEffect|useDeepCompareEffect)\b/;
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
  const isViewerFeature = file.pathname.includes('/features/ExplorerViewer/') || /\/Viewer\.tsx$/.test(file.pathname);
  if (isViewerFeature && effectNames.test(source)) violations.push(`${file.pathname}: Viewer code must not use React effects`);
}
if (violations.length) {
  console.error(violations.join('\n'));
  process.exit(1);
}
console.log(`Loom UI boundary check passed (${files.length} TypeScript files).`);
