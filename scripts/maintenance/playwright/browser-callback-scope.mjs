function isBrowserRuntimeLibrary(sourceFile) {
  const path = sourceFile.fileName.replaceAll('\\', '/');
  return /\/typescript\/lib\/lib\.(?:dom|webworker|es[^/]*)[^/]*\.d\.ts$/.test(path);
}

function collectBrowserRuntimeGlobals(ts, program, checker) {
  const names = new Set(['undefined', 'NaN', 'Infinity']);
  for (const sourceFile of program.getSourceFiles()) {
    if (!isBrowserRuntimeLibrary(sourceFile)) continue;
    for (const symbol of checker.getSymbolsInScope(sourceFile, ts.SymbolFlags.Value)) {
      if (symbol.declarations?.some(declaration => isBrowserRuntimeLibrary(declaration.getSourceFile()))) {
        names.add(symbol.getName());
      }
    }
  }
  return names;
}

function callbackFunctions(ts, checker, expression) {
  if (ts.isArrowFunction(expression) || ts.isFunctionExpression(expression) || ts.isFunctionDeclaration(expression)) {
    return [expression];
  }
  if (!ts.isIdentifier(expression)) return [];

  const symbol = checker.getSymbolAtLocation(expression);
  const functions = [];
  for (const declaration of symbol?.declarations ?? []) {
    if (ts.isVariableDeclaration(declaration) && declaration.initializer) {
      functions.push(...callbackFunctions(ts, checker, declaration.initializer));
    } else if (ts.isFunctionDeclaration(declaration)) {
      functions.push(declaration);
    }
  }
  return functions;
}

function isIdentifierPropertyName(ts, node) {
  const parent = node.parent;
  return (ts.isPropertyAccessExpression(parent) && parent.name === node) ||
    (ts.isPropertyAssignment(parent) && parent.name === node) ||
    (ts.isBindingElement(parent) && parent.propertyName === node) ||
    ((ts.isMethodDeclaration(parent) || ts.isMethodSignature(parent) ||
      ts.isPropertyDeclaration(parent) || ts.isPropertySignature(parent)) && parent.name === node) ||
    ((ts.isBreakStatement(parent) || ts.isContinueStatement(parent) || ts.isLabeledStatement(parent)) && parent.label === node);
}

function isInsideFunction(functionNode, declaration) {
  return declaration.getSourceFile() === functionNode.getSourceFile() &&
    declaration.pos >= functionNode.pos && declaration.end <= functionNode.end;
}

function callbackLeaks(ts, checker, browserGlobals, functionNode) {
  const leaks = new Map();
  const visit = node => {
    if (ts.isIdentifier(node) && !isIdentifierPropertyName(ts, node)) {
      // Serialized callbacks execute in the target page, where standard browser globals resolve there.
      if (browserGlobals.has(node.text)) return;
      const symbol = checker.getSymbolAtLocation(node);
      const declarations = symbol?.declarations ?? [];
      const capturesOuterBinding = declarations.some(declaration =>
        !isInsideFunction(functionNode, declaration) && !isBrowserRuntimeLibrary(declaration.getSourceFile()));
      if (capturesOuterBinding || !symbol) {
        if (!leaks.has(node.text)) leaks.set(node.text, node);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(functionNode.body);
  return [...leaks.keys()];
}

function callbackArgument(ts, call) {
  const expression = call.expression;
  if (ts.isPropertyAccessExpression(expression)) {
    const method = expression.name.text;
    if (['evaluate', 'evaluateAll', 'evaluateHandle', 'waitForFunction'].includes(method)) return call.arguments[0];
    if (['inspect', 'wait'].includes(method) && expression.expression.getText() === 'cda') return call.arguments[0];
  }
  if (ts.isIdentifier(expression)) {
    if (expression.text === 'browserEval' || expression.text === 'waitForBrowser') return call.arguments[1];
  }
  return undefined;
}

function forwardedCallbackArgument(ts, checker, call) {
  if (!ts.isIdentifier(call.expression)) return undefined;
  const symbol = checker.getSymbolAtLocation(call.expression);
  for (const declaration of symbol?.declarations ?? []) {
    if (!ts.isVariableDeclaration(declaration) || !declaration.initializer ||
        !(ts.isArrowFunction(declaration.initializer) || ts.isFunctionExpression(declaration.initializer))) continue;
    const body = declaration.initializer.body;
    const returned = ts.isBlock(body) && body.statements.length === 1 && ts.isReturnStatement(body.statements[0])
      ? body.statements[0].expression
      : ts.isBlock(body) ? undefined : body;
    if (!returned || !ts.isCallExpression(returned) || !callbackArgument(ts, returned)) continue;
    const forwarded = returned.arguments[0];
    if (!ts.isIdentifier(forwarded)) continue;
    const parameterIndex = declaration.initializer.parameters.findIndex(parameter =>
      ts.isIdentifier(parameter.name) && parameter.name.text === forwarded.text);
    if (parameterIndex >= 0) return call.arguments[parameterIndex];
  }
  return undefined;
}

export function createBrowserCallbackScopeChecker(ts, program) {
  const checker = program.getTypeChecker();
  const browserGlobals = collectBrowserRuntimeGlobals(ts, program, checker);

  return sourceFile => {
    const issues = [];
    const visit = node => {
      if (ts.isCallExpression(node)) {
        const argument = callbackArgument(ts, node) ?? forwardedCallbackArgument(ts, checker, node);
        if (argument) {
          for (const callback of callbackFunctions(ts, checker, argument)) {
            for (const name of callbackLeaks(ts, checker, browserGlobals, callback)) {
              const { line, character } = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
              issues.push({ line: line + 1, column: character + 1, name });
            }
          }
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
    return issues;
  };
}
