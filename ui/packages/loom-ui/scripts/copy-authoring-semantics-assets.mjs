import { copyFile, mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const packageDirectory = dirname(dirname(fileURLToPath(import.meta.url)));
const sourceDirectory = resolve(packageDirectory, 'src');
const declarationDirectory = resolve(packageDirectory, 'dist');

await mkdir(declarationDirectory, { recursive: true });
for (const file of ['authoringSemanticsVersion.mjs', 'authoringSemanticsVersion.d.mts']) {
  await copyFile(resolve(sourceDirectory, file), resolve(declarationDirectory, file));
}
