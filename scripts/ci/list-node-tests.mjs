import { access, readdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

// Tokenize enough to ignore quoted test fixtures/comments; this is a dependency
// inventory, not a JavaScript parser or proof about computed imports.
function tokens(source) {
  const result = [], contexts = [];
  let index = 0, expressionStart = true;
  const prefixes = new Set(['return', 'throw', 'case', 'delete', 'void', 'typeof', 'new', 'in', 'instanceof', 'yield', 'await', 'else', 'do']);
  const controls = new Set(['if', 'while', 'for', 'with', 'switch', 'catch']);
  function regexEnd(start) {
    let characterClass = false;
    for (let i = start + 1; i < source.length; i++) {
      const character = source[i];
      if (character === '\\') { i++; continue; }
      if (character === '\n' || character === '\r') return undefined;
      if (character === '[') characterClass = true;
      else if (character === ']') characterClass = false;
      else if (character === '/' && !characterClass) {
        i++;
        while (i < source.length && /[a-z]/i.test(source[i])) i++;
        return i;
      }
    }
  }
  while (index < source.length) {
    const start = index, character = source[index];
    if (/\s/.test(character)) { index++; continue; }
    if (source.startsWith('//', index)) {
      const end = source.indexOf('\n', index + 2);
      index = end < 0 ? source.length : end + 1;
      continue;
    }
    if (source.startsWith('/*', index)) {
      const end = source.indexOf('*/', index + 2);
      index = end < 0 ? source.length : end + 2;
      continue;
    }
    if (character === '"' || character === "'" || character === '`') {
      index++;
      while (index < source.length) {
        if (source[index] === '\\') index += 2;
        else if (source[index++] === character) break;
      }
      result.push({ value: source.slice(start, index), string: character !== '`' });
      expressionStart = false;
      continue;
    }
    if (character === '/' && expressionStart) {
      const end = regexEnd(index);
      if (end !== undefined) {
        result.push({ value: source.slice(start, end), string: false });
        index = end;
        expressionStart = false;
        continue;
      }
    }
    const word = /^[A-Za-z_$][\w$]*/.exec(source.slice(index));
    if (word) {
      index += word[0].length;
      result.push({ value: word[0], string: false });
      expressionStart = prefixes.has(word[0]);
      continue;
    }
    const previous = result.at(-1)?.value;
    const operator = /^(?:=>|\+\+|--|\?\.|&&|\|\||\?\?)/.exec(source.slice(index))?.[0] || character;
    index += operator.length;
    if (operator === '(') contexts.push(controls.has(previous));
    expressionStart = operator === ')' ? Boolean(contexts.pop())
      : operator === ']' || operator === '++' || operator === '--' || operator === '?.' || operator === '.' ? false
        // A closing block can begin a new regex statement. Ambiguous brace
        // contexts prefer retaining a test over treating regex text as imports.
        : operator === '}' ? true : !/^[0-9]$/.test(operator);
    result.push({ value: operator, string: false });
  }
  return result;
}
function references(source) {
  const result = [], input = tokens(source);
  const string = token => token?.string ? token.value.slice(1, -1) : undefined;
  for (let i = 0; i < input.length; i++) {
    const value = input[i].value;
    if (value === 'import' || value === 'require' || value === 'export') {
      let specifier;
      if (input[i + 1]?.value === '(') specifier = string(input[i + 2]);
      else if (value === 'import' && input[i + 1]?.string) specifier = string(input[i + 1]);
      else if (value !== 'require') {
        for (let j = i + 1; j < input.length && input[j].value !== ';'; j++)
          if (input[j].value === 'from') { specifier = string(input[j + 1]); break; }
      }
      if (specifier) result.push({ specifier, asset: false });
    }
    if (value === 'new' && input[i + 1]?.value === 'URL' && input[i + 2]?.value === '(' && input[i + 3]?.string &&
      input.slice(i + 4, i + 11).map(token => token.value).join('') === ',import.meta.url)')
      result.push({ specifier: string(input[i + 3]), asset: true });
  }
  return result;
}

/** Explain the same dependency-closure policy used by the Node CI shards. */
export async function nodeTestInventory(projectRoot = process.cwd()) {
  const scriptsRoot = path.join(projectRoot, 'scripts');
  const candidates = [];
  const graph = new Map();
  const relative = file => path.relative(projectRoot, file).split(path.sep).join('/');
  async function visit(directory) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const file = path.join(directory, entry.name);
      if (entry.isDirectory()) await visit(file);
      else if (entry.name.endsWith('.test.mjs')) candidates.push(file);
    }
  }
  async function load(file) {
    if (graph.has(file)) return;
    const node = { dependencies: [], problem: undefined };
    graph.set(file, node); // Register before recursion; cycles are checked per root below.
    let source;
    try { source = await readFile(file, 'utf8'); }
    catch (error) {
      node.problem = { reason: 'missing-or-unreadable-import', file: relative(file), code: error.code };
      return;
    }
    for (const { specifier, asset } of references(source)) {
      if (asset) {
        if (!file.endsWith('.test.mjs') || !specifier.startsWith('.')) continue;
        const dependency = path.resolve(path.dirname(file), specifier);
        try { await access(dependency); }
        catch (error) { node.problem = { reason: 'missing-source-fixture', file: relative(dependency), code: error.code }; }
        continue;
      }
      if (/^(?:@playwright\/test|playwright(?:-core)?)(?:\/|$)/.test(specifier)) {
        node.problem = { reason: 'browser-package-import', file: relative(file), specifier };
        continue;
      }
      if (!specifier.startsWith('.')) continue;
      let dependency = path.resolve(path.dirname(file), specifier);
      if (!path.extname(dependency)) dependency += '.mjs';
      node.dependencies.push(dependency);
      await load(dependency);
    }
  }
  await visit(scriptsRoot);
  const skillRoot = path.join(projectRoot, '.codex/skills');
  try { await visit(skillRoot); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const included = [], excluded = [];
  for (const candidate of candidates.sort()) {
    if (relative(candidate).startsWith('scripts/maintenance/playwright/')) {
      excluded.push({ file: relative(candidate), reason: 'retired-browser-directory', chain: [relative(candidate)] });
      continue;
    }
    await load(candidate);
    const queue = [[candidate]], seen = new Set();
    let problem;
    for (let index = 0; index < queue.length; index++) {
      const chain = queue[index], file = chain.at(-1);
      if (seen.has(file)) continue;
      seen.add(file);
      const node = graph.get(file);
      if (node.problem) {
        problem = { ...node.problem, chain: chain.map(relative) };
        break;
      }
      for (const dependency of node.dependencies) queue.push([...chain, dependency]);
    }
    if (problem) excluded.push({ ...problem, file: relative(candidate), dependency: problem.file });
    else included.push(relative(candidate));
  }
  return { included, excluded, policy: 'Relative static and literal dynamic import/require closure plus literal test-owned URL fixture existence; executable Playwright package imports excluded, incidental strings retained. Computed imports and external package availability are not proven.' };
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  if (process.argv.slice(2).some(argument => argument !== '--explain') || process.argv.filter(argument => argument === '--explain').length > 1)
    throw new Error('Usage: node scripts/ci/list-node-tests.mjs [--explain]');
  const result = await nodeTestInventory();
  const output = process.argv.includes('--explain') ? JSON.stringify(result, null, 2) : result.included.join('\n');
  await new Promise((resolve, reject) => process.stdout.write(`${output}\n`, error => error ? reject(error) : resolve()));
}
