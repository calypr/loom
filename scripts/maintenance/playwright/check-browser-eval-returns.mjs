import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const scriptsRoot = path.resolve(scriptDirectory, '../..');
export const repositoryRoot = path.resolve(scriptsRoot, '..');

export const loadTypeScript = (root = repositoryRoot) =>
  createRequire(path.join(root, 'ui/packages/loom-ui/package.json'))('typescript');

const isTransparent = (node, ts) => ts.isParenthesizedExpression(node)
  || ts.isAwaitExpression(node)
  || ts.isVoidExpression(node)
  || ts.isAsExpression(node)
  || ts.isTypeAssertionExpression(node)
  || ts.isNonNullExpression(node)
  || ts.isSatisfiesExpression(node);

const isValueConsumed = (call, ts) => {
  let current = call;
  let discarded = false;
  while (current.parent && isTransparent(current.parent, ts)) {
    if (ts.isVoidExpression(current.parent)) discarded = true;
    current = current.parent;
  }
  if (discarded) return false;
  return !(ts.isExpressionStatement(current.parent) && current.parent.expression === current);
};

const extractBody = (argument, ts) => {
  if (!argument) return { kind: 'unresolved-body' };
  if (ts.isStringLiteralLike(argument) || ts.isNoSubstitutionTemplateLiteral(argument)) {
    return { kind: 'static', text: argument.text };
  }
  if (ts.isTemplateExpression(argument)) {
    const text = argument.head.text + argument.templateSpans
      .map((span) => `__browserEvalInterpolation__${span.literal.text}`)
      .join('');
    return { kind: 'interpolated', text };
  }
  if (ts.isBinaryExpression(argument) && argument.operatorToken.kind === ts.SyntaxKind.PlusToken) {
    const parts = [];
    const collect = (node) => {
      if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
        collect(node.left);
        collect(node.right);
      } else if (ts.isStringLiteralLike(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
        parts.push(node.text);
      } else if (ts.isTemplateExpression(node)) {
        parts.push(node.head.text, ...node.templateSpans.flatMap((span) => [`__browserEvalInterpolation__`, span.literal.text]));
      } else {
        parts.push('__browserEvalInterpolation__');
      }
    };
    collect(argument);
    return { kind: 'interpolated', text: parts.join('') };
  }
  return { kind: 'unresolved-body' };
};

const hasOuterReturn = (body, ts) => {
  const wrapped = `async function __browserEvalBody__(){\n${body}\n}`;
  const sourceFile = ts.createSourceFile('browser-eval-body.js', wrapped, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  if (sourceFile.parseDiagnostics.length) return { kind: 'unparseable-body' };
  const wrapper = sourceFile.statements[0];
  let found = false;
  const visit = (node) => {
    if (node !== wrapper && (ts.isFunctionLike(node) || ts.isClassLike(node))) return;
    if (ts.isReturnStatement(node)) { found = true; return; }
    ts.forEachChild(node, visit);
  };
  visit(wrapper.body);
  return { kind: found ? 'outer-return' : 'no-outer-return' };
};

export const auditBrowserEvalSource = (source, file = '<source>', ts = loadTypeScript()) => {
  const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  const findings = [];
  const visit = (node) => {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'browserEval' && isValueConsumed(node, ts)) {
      const body = extractBody(node.arguments[1], ts);
      const result = body.kind === 'unresolved-body' ? body : hasOuterReturn(body.text, ts);
      const unresolved = body.kind === 'unresolved-body' || result.kind === 'unparseable-body'
        || (body.kind === 'interpolated' && result.kind === 'no-outer-return');
      if (result.kind === 'no-outer-return' || unresolved) {
        const position = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
        findings.push({
          file,
          line: position.line + 1,
          kind: unresolved ? 'unresolved' : 'missing-outer-return',
          body: body.text?.replace(/\s+/g, ' ').slice(0, 180),
        });
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return findings;
};

const verifierModulesUnder = (directory) => {
  if (!fs.existsSync(directory)) return [];
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    if (entry.name === 'node_modules' || entry.name === '.artifacts' || entry.name === 'tests') return [];
    const entryPath = path.join(directory, entry.name);
    if (entry.isDirectory()) return verifierModulesUnder(entryPath);
    return entry.isFile() && entry.name.endsWith('.mjs') ? [entryPath] : [];
  });
};

export const verifierFiles = (root = repositoryRoot) => [
  ...fs.readdirSync(path.join(root, 'scripts'))
    .filter((name) => /^verify-cda.*\.mjs$/.test(name))
    .map((name) => path.join(root, 'scripts', name)),
  ...verifierModulesUnder(path.join(root, 'scripts/verify-ui')),
].sort();

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = path.resolve(process.argv[2] ?? repositoryRoot);
  const ts = loadTypeScript(repositoryRoot);
  const findings = verifierFiles(root).flatMap((file) =>
    auditBrowserEvalSource(fs.readFileSync(file, 'utf8'), path.relative(root, file), ts));
  const defects = findings.filter((finding) => finding.kind === 'missing-outer-return');
  const unresolved = findings.filter((finding) => finding.kind === 'unresolved');
  console.log(`Scanned ${verifierFiles(root).length} verifier files: ${defects.length} missing outer returns, ${unresolved.length} value-consuming calls unresolved.`);
  for (const finding of findings) console.log(`${finding.file}:${finding.line} ${finding.kind}${finding.body ? ` ${finding.body}` : ''}`);
  if (defects.length) process.exitCode = 1;
}
