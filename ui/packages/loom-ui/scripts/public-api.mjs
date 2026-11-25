import { createRequire } from 'node:module';
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const entryFiles = {
  '.': 'index.ts',
  './viewer': 'viewer-entry.ts',
  './builder': 'builder-entry.ts',
  './saved-views': 'saved-views-entry.ts',
};
const publishedEntryTargets = {
  '.': { bundle: 'loom-ui.js', declarations: 'index.d.ts' },
  './viewer': { bundle: 'viewer.js', declarations: 'viewer-entry.d.ts' },
  './builder': { bundle: 'builder.js', declarations: 'builder-entry.d.ts' },
  './saved-views': { bundle: 'saved-views.js', declarations: 'saved-views-entry.d.ts' },
};
const viteEntryNames = {
  '.': 'loom-ui',
  './viewer': 'viewer',
  './builder': 'builder',
  './saved-views': 'saved-views',
};

const resolvePackageRoot = (packageRoot) =>
  packageRoot instanceof URL
    ? fileURLToPath(packageRoot)
    : resolve(packageRoot);

const loadTypeScript = (packageRoot) => {
  const require = createRequire(join(packageRoot, 'package.json'));
  return require('typescript');
};

const listExports = (packageRoot) => {
  const ts = loadTypeScript(packageRoot);
  const configPath = ts.findConfigFile(packageRoot, ts.sys.fileExists, 'tsconfig.json');
  if (!configPath) throw new Error(`No tsconfig.json found under ${packageRoot}`);

  const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
  if (configFile.error) {
    throw new Error(ts.flattenDiagnosticMessageText(configFile.error.messageText, '\n'));
  }

  const config = ts.parseJsonConfigFileContent(
    configFile.config,
    ts.sys,
    dirname(configPath),
    { noEmit: true },
    configPath,
  );
  const program = ts.createProgram(config.fileNames, config.options);
  const checker = program.getTypeChecker();
  const entries = {};

  for (const [entry, fileName] of Object.entries(entryFiles)) {
    const entryPath = resolve(packageRoot, 'src', fileName);
    const sourceFile = program.getSourceFile(entryPath);
    if (!sourceFile) throw new Error(`TypeScript did not load package entry ${entryPath}`);

    const symbol = checker.getSymbolAtLocation(sourceFile);
    if (!symbol) throw new Error(`TypeScript found no module symbol for ${entryPath}`);

    entries[entry] = checker
      .getExportsOfModule(symbol)
      .map((exported) => exported.getName())
      .sort((left, right) => left.localeCompare(right));
  }

  return entries;
};

const loadManifest = async (packageRoot) =>
  JSON.parse(await readFile(join(packageRoot, 'public-api.json'), 'utf8'));

const sortedJson = (value) => `${JSON.stringify(value, null, 2)}\n`;

const validateConsumerContract = (manifest) => {
  for (const [consumer, contract] of Object.entries(manifest.consumerContract)) {
    if (consumer === 'stylesSubpath') continue;
    for (const [entry, names] of Object.entries(contract.entrypoints)) {
      const exportedNames = new Set(manifest.entries[entry] ?? []);
      const missing = names.filter((name) => !exportedNames.has(name));
      if (missing.length > 0) {
        throw new Error(
          `${consumer} consumer contract names missing from ${entry}: ${missing.join(', ')}`,
        );
      }
    }
  }
};

const validatePackageExports = async (packageRoot, manifest) => {
  const packageJson = JSON.parse(await readFile(join(packageRoot, 'package.json'), 'utf8'));
  const errors = [];

  for (const [entry, targets] of Object.entries(publishedEntryTargets)) {
    const mapping = packageJson.exports?.[entry];
    const expected = {
      types: `./dist/${targets.declarations}`,
      import: `./dist/${targets.bundle}`,
      default: `./dist/${targets.bundle}`,
    };
    if (!mapping || typeof mapping !== 'object') {
      errors.push(`package.json exports is missing entry ${entry}`);
      continue;
    }
    for (const [condition, target] of Object.entries(expected)) {
      if (mapping[condition] !== target) {
        errors.push(
          `package.json exports ${entry}.${condition} must map to ${target}`,
        );
      }
    }
  }

  const stylesPath = manifest.consumerContract.stylesSubpath;
  if (packageJson.exports?.[stylesPath] !== './dist/styles.css') {
    errors.push(`package.json exports ${stylesPath} must map to ./dist/styles.css`);
  }

  const ts = loadTypeScript(packageRoot);
  const viteConfigPath = join(packageRoot, 'vite.config.ts');
  const viteConfigText = await readFile(viteConfigPath, 'utf8');
  const viteConfig = ts.createSourceFile(
    viteConfigPath,
    viteConfigText,
    ts.ScriptTarget.Latest,
    true,
  );
  const inputs = new Map();
  const visit = (node) => {
    if (ts.isPropertyAssignment(node) && node.name.getText(viteConfig) === 'input') {
      if (ts.isObjectLiteralExpression(node.initializer)) {
        for (const property of node.initializer.properties) {
          if (!ts.isPropertyAssignment(property)) continue;
          const name = ts.isStringLiteral(property.name) || ts.isIdentifier(property.name)
            ? property.name.text
            : undefined;
          const initializer = property.initializer;
          const sourcePath = ts.isCallExpression(initializer) && initializer.arguments.length > 1 &&
            ts.isStringLiteral(initializer.arguments[1])
            ? initializer.arguments[1].text
            : undefined;
          if (name && sourcePath) inputs.set(name, sourcePath);
        }
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(viteConfig);

  for (const [entry, sourceFile] of Object.entries(entryFiles)) {
    const inputName = viteEntryNames[entry];
    const expectedSource = `src/${sourceFile}`;
    if (inputs.get(inputName) !== expectedSource) {
      errors.push(`vite.config.ts build input ${inputName} must resolve to ${expectedSource}`);
    }
    if (!manifest.entries[entry]) {
      errors.push(`public API manifest is missing published entry ${entry}`);
    }
  }

  if (errors.length > 0) {
    throw new Error(`Published package contract drift:\n${errors.join('\n')}`);
  }
};

export async function generatePublicApi(packageRoot) {
  const absoluteRoot = resolvePackageRoot(packageRoot);
  const manifest = await loadManifest(absoluteRoot);
  const generated = { ...manifest, entries: listExports(absoluteRoot) };
  validateConsumerContract(generated);
  await validatePackageExports(absoluteRoot, generated);
  return generated;
}

export async function checkPublicApi(packageRoot) {
  const absoluteRoot = resolvePackageRoot(packageRoot);
  const manifest = await loadManifest(absoluteRoot);
  const actual = { ...manifest, entries: listExports(absoluteRoot) };
  validateConsumerContract(actual);
  await validatePackageExports(absoluteRoot, actual);
  const expectedText = sortedJson(manifest);
  const actualText = sortedJson(actual);

  if (expectedText !== actualText) {
    throw new Error(
      `Public API manifest drift in ${absoluteRoot}/public-api.json. ` +
        'Run node scripts/public-api.mjs --update to review and record the new surface.',
    );
  }

  return actual;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const packageRoot = resolve(scriptDirectory, '..');
  const update = process.argv.includes('--update');
  const unknownArguments = process.argv.slice(2).filter((argument) => argument !== '--update');
  if (unknownArguments.length > 0) {
    throw new Error(`Unknown arguments: ${unknownArguments.join(', ')}`);
  }

  if (update) {
    const manifest = await generatePublicApi(packageRoot);
    await writeFile(join(packageRoot, 'public-api.json'), sortedJson(manifest));
    console.log(`Updated ${packageRoot}/public-api.json`);
  } else {
    await checkPublicApi(packageRoot);
    console.log(`Public API manifest matches ${packageRoot}/public-api.json`);
  }
}
