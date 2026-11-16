import { readdir, readFile } from 'node:fs/promises';
import { join, relative } from 'node:path';

const root = new URL('../src/', import.meta.url);
const forbidden = /(?:@gen3\/core|@gen3\/frontend|from ['"]next(?:\/|['"])|ProtectedContent|CalyprAuth)/;
const effectNames = /\b(?:useEffect|useLayoutEffect|useInsertionEffect|useDeepCompareEffect)\b/;
const effectCalls = /\b(?:useEffect|useLayoutEffect|useInsertionEffect|useDeepCompareEffect)\s*\(/g;
const effectBaseline = JSON.parse(await readFile(new URL('./useeffect-baseline.json', import.meta.url), 'utf8'));
const packageRoot = new URL('../', import.meta.url);
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
  if (!/\.test\.tsx?$/.test(file.pathname) && /\bimport\s*\.\s*meta\s*\.\s*env\b/.test(source)) {
    violations.push(`${file.pathname}: Vite environment access belongs in the host app; pass configuration through props`);
  }
  const isViewerFeature = file.pathname.includes('/features/ExplorerViewer/') || /\/Viewer\.tsx$/.test(file.pathname);
  if (isViewerFeature && effectNames.test(source)) violations.push(`${file.pathname}: Viewer code must not use React effects`);
  if (!isViewerFeature && !/\.test\.tsx?$/.test(file.pathname)) {
    const key = relative(packageRoot.pathname, file.pathname);
    const found = (source.match(effectCalls) ?? []).length;
    const allowed = effectBaseline[key] ?? 0;
    if (found > allowed) {
      violations.push(`${key}: ${found} React effect call(s) but the baseline allows ${allowed}. Do not add useEffect; derive state during rendering or update it through its owning action.`);
    } else if (found < allowed) {
      violations.push(`${key}: baseline allows ${allowed} React effect call(s) but only ${found} remain. Lower useeffect-baseline.json to keep the ratchet tight.`);
    }
  }
}
if (violations.length) {
  console.error(violations.join('\n'));
  process.exit(1);
}
console.log(`Loom UI boundary check passed (${files.length} TypeScript files).`);
