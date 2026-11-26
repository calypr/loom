import { defineConfig } from 'vitest/config';
import ts from 'typescript';
import type { Plugin } from 'vite';
import { resolve } from 'node:path';

const clientTracePlugin = (): Plugin => ({
  name: 'loom-ui-trace-mocked-clients',
  enforce: 'pre',
  transform(code, id) {
    if (!process.env.LOOM_UI_TRACE || !id.includes('/src/') || id.includes('.test.')
      || !/\.tsx?$/.test(id)) return null;

    const source = ts.createSourceFile(id, code, ts.ScriptTarget.Latest, true,
      id.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
    const edits: Array<{ start: number; end: number; text: string }> = [];
    const visit = (node: ts.Node): void => {
      if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)
        && node.expression.text === 'useLoomClient' && node.arguments.length === 0) {
        edits.push({
          start: node.getStart(source),
          end: node.end,
          text: `__traceLoomClient(${node.getText(source)})`,
        });
      }
      ts.forEachChild(node, visit);
    };
    visit(source);
    if (edits.length === 0) return null;

    let transformed = code;
    for (const edit of edits.sort((left, right) => right.start - left.start)) {
      transformed = transformed.slice(0, edit.start) + edit.text + transformed.slice(edit.end);
    }
    const helper = resolve(import.meta.dirname, 'testSupport/traceLoomClient.ts');
    return {
      code: `import { traceLoomClient as __traceLoomClient } from ${JSON.stringify(helper)};\n${transformed}`,
      map: null,
    };
  },
});

export default defineConfig({
  plugins: [clientTracePlugin()],
  cacheDir: process.env.LOOM_VITEST_CACHE_DIR,
  test: {
    environment: 'node',
    globals: true,
    setupFiles: ['./vitest.setup.ts'],
    runner: process.env.LOOM_UI_TRACE ? './vitest.trace.runner.ts' : undefined,
    testTimeout: process.env.CI ? 600_000 : 30_000,
    hookTimeout: process.env.CI ? 600_000 : 30_000,
  },
});
