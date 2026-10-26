const deletedModules = new Set([
  'native-abort-probe',
  'native-request-ledger',
  'native-request-ownership',
  'pending-response-reads',
  'cda-playwright-controls',
]);

const removedIdentifiers = new Set([
  'nativeAbortDomOwnerRules',
  'createNativeAbortProbeSource',
  'installNativeAbortProbe',
  'flushNativeAbortProbeEvents',
  'nativeAbortNetworkFailureClock',
  'nativeAbortProbeEvidenceForRequest',
  'nativeAbortSignalObservationForRequest',
  'nativeAbortOwnerRetirementActionFor',
  'nativeReadRequestMatchesExpectedScope',
  'classifyNativeRequestOwnerRetirement',
  'classifyExpectedOwnedCancellation',
  'createFixtureNativeRequestLedger',
  'openBasicFixtureNativeRequestScope',
  'projectFixtureNetworkDiagnostics',
  'finalizeFixtureNativeRequestReport',
  'runAfterNativeRequestDrain',
  'adjudicatePendingLifecycle',
  'registerValidatedObsoleteNetworkRead',
  'createPendingResponseReads',
  'createFixtureBrowserDiagnostics',
  'classifyExpectedCdaCancellation',
  'markExpectedOwnedPreviewAborts',
  'navigateAfterOwnedConstructionCapabilities',
  'captureOwnedPreviewLifecycle',
  'drainPendingResponseReads',
  'drainResponses',
  'nativeRequestLedger',
]);

const removedCdaFixtureMembers = new Set([
  'includeBrowserDiagnostics',
  'waitForCapturedResponse',
  'flushHttpDiagnostics',
]);

const removedRequestCaptureMethods = new Set(['flush', 'waitFor']);
const requestCaptureOwners = new Set([
  'tracker',
  'requests',
  'requestCapture',
  'browserRequestCapture',
  'browserEvents',
  'browserRequestTracker',
  'requestTracker',
  'nativeRequestLedger',
  'requestLedger',
  'ledger',
  'capture',
]);

function deletedModuleName(specifier) {
  const normalized = specifier.replaceAll('\\', '/').replace(/\.(?:mjs|cjs|js)$/, '');
  return [...deletedModules].find(name => normalized === name || normalized.endsWith(`/${name}`));
}

export function findRemovedNativePlaywrightApiReferences(source, ts) {
  if (!ts?.createSourceFile) throw new TypeError('TypeScript parser is required to scan native Playwright source.');
  const sourceFile = ts.createSourceFile('native-playwright-source.mjs', String(source),
    ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  const findings = [];
  const add = (name, node) => findings.push({
    name,
    reference: node.getText(sourceFile),
    line: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
    start: node.getStart(sourceFile),
  });
  const inspectModule = moduleSpecifier => {
    if (!moduleSpecifier || !ts.isStringLiteral(moduleSpecifier)) return;
    const name = deletedModuleName(moduleSpecifier.text);
    if (name) add(`deleted helper module "${name}"`, moduleSpecifier);
  };
  const requestViewFactories = new Set(['createCDARequestView']);
  const requestViewNames = new Set();
  const requestViewWrappers = new Map();
  const unwrapExpression = node => {
    while (node && (ts.isAwaitExpression(node) || ts.isParenthesizedExpression(node) ||
      ts.isAsExpression(node) || ts.isTypeAssertionExpression(node) || ts.isNonNullExpression(node))) {
      node = node.expression;
    }
    return node;
  };
  const isRequestViewFactoryCall = node => {
    node = unwrapExpression(node);
    if (!node || !ts.isCallExpression(node)) return false;
    const expression = node.expression;
    return (ts.isIdentifier(expression) && requestViewFactories.has(expression.text)) ||
      (ts.isPropertyAccessExpression(expression) && expression.name.text === 'captureRequests');
  };
  const isFixtureOwnedObserver = node => ts.isPropertyAccessExpression(node) &&
    node.name.text === 'networkObserver' && ts.isIdentifier(node.expression) &&
    new Set(['cda', 'context', 'workflow']).has(node.expression.text);
  const isRequestViewExpression = (expression, knownWrappers) => {
    expression = unwrapExpression(expression);
    if (!expression) return false;
    if (isFixtureOwnedObserver(expression) || isRequestViewFactoryCall(expression)) return true;
    if (ts.isConditionalExpression(expression)) {
      return isRequestViewExpression(expression.whenTrue, knownWrappers) ||
        isRequestViewExpression(expression.whenFalse, knownWrappers);
    }
    return ts.isCallExpression(expression) && ts.isIdentifier(expression.expression) &&
      knownWrappers.has(expression.expression.text);
  };
  const collectFunctionWrappers = node => {
    if (ts.isFunctionDeclaration(node) && node.name && node.body) {
      requestViewWrappers.set(node.name.text, node.body);
    } else if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name)) {
      const initializer = unwrapExpression(node.initializer);
      if (initializer && (ts.isArrowFunction(initializer) || ts.isFunctionExpression(initializer))) {
        requestViewWrappers.set(node.name.text, initializer.body);
      }
    }
    ts.forEachChild(node, collectFunctionWrappers);
  };
  collectFunctionWrappers(sourceFile);
  const returnedExpressions = body => {
    if (!body) return [];
    if (!ts.isBlock(body)) return [body];
    const expressions = [];
    const visitReturns = node => {
      if (node !== body && (ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node))) return;
      if (ts.isReturnStatement(node) && node.expression) expressions.push(node.expression);
      ts.forEachChild(node, visitReturns);
    };
    visitReturns(body);
    return expressions;
  };
  let wrappersChanged = true;
  while (wrappersChanged) {
    wrappersChanged = false;
    for (const [name, body] of requestViewWrappers) {
      if (!requestViewNames.has(name) && returnedExpressions(body).some(expression =>
        isRequestViewExpression(expression, requestViewNames))) {
        requestViewNames.add(name);
        wrappersChanged = true;
      }
    }
  }
  const collectRequestViews = node => {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) &&
        isRequestViewExpression(node.initializer, requestViewNames)) {
      requestViewNames.add(node.name.text);
    } else if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
        ts.isIdentifier(node.left) && isRequestViewExpression(node.right, requestViewNames)) {
      requestViewNames.add(node.left.text);
    }
    ts.forEachChild(node, collectRequestViews);
  };
  collectRequestViews(sourceFile);

  const visit = node => {
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) {
      inspectModule(node.moduleSpecifier);
    } else if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword) {
      inspectModule(node.arguments[0]);
    }

    if (ts.isIdentifier(node) && removedIdentifiers.has(node.text)) {
      add('removed native verification API', node);
    }
    if (ts.isPropertyAccessExpression(node)) {
      if (removedCdaFixtureMembers.has(node.name.text)) add('removed CDA fixture member', node);
      if (node.name.text === 'pendingReads') add('removed request-capture field', node);
      if (node.name.text === 'stop' && ts.isCallExpression(node.parent) && node.parent.expression === node
        && ts.isIdentifier(node.expression) && requestViewNames.has(node.expression.text)) {
        add('removed captured request-view stop method', node.parent);
      }
      if (removedRequestCaptureMethods.has(node.name.text) && ts.isIdentifier(node.expression)
        && requestCaptureOwners.has(node.expression.text)) {
        add('removed request-capture lifecycle method', node);
      }
      if (node.name.text === 'entries' && ts.isCallExpression(node.parent)
        && node.parent.expression === node && ts.isPropertyAccessExpression(node.expression)
        && node.expression.name.text === 'byRequest') {
        add('removed request-map iteration API', node.parent);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);

  const seen = new Set();
  return findings
    .sort((a, b) => a.start - b.start || a.name.localeCompare(b.name))
    .filter(finding => {
      const key = `${finding.name}\0${finding.start}`;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    })
    .map(({ start: _start, ...finding }) => finding);
}
