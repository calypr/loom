import { z } from 'zod';

export interface SourceRecipeExpression {
  readonly select?: string;
  readonly literal?: unknown;
  readonly call?: string;
  readonly args?: readonly SourceRecipeExpression[];
  readonly document?: { readonly context?: string };
}
export const sourceRecipeEditFunctions = ['first', 'all', 'reference_id', 'last_segment', 'coalesce', 'coalesce_string', 'join', 'concat', 'uuid5'] as const;
const text = z.string().max(2048);
export const sourceRecipeExpressionSchema: z.ZodType<SourceRecipeExpression> = z.lazy(() => z.object({
  select: text.min(1).optional(), literal: z.unknown().optional(), call: text.min(1).optional(),
  args: z.array(sourceRecipeExpressionSchema).max(4096).optional(),
  document: z.object({ context: text.optional() }).strict().optional(),
}).strict().superRefine((value, context) => {
  if (['select', 'literal', 'call', 'document'].filter((key) => Object.hasOwn(value, key)).length !== 1) {
    context.addIssue({ code: 'custom', message: 'Choose exactly one recipe expression.' });
  }
  if (value.args !== undefined && value.call === undefined) context.addIssue({ code: 'custom', message: 'Only a function has arguments.' });
}));
const expressions = z.array(sourceRecipeExpressionSchema).max(4096);
const paths = z.array(text).max(4096);
const catalogOutputSchema = z.object({
  fieldPath: z.string().trim().min(1).max(512),
  enabled: z.boolean(),
  name: z.string().regex(/^[A-Za-z_][A-Za-z0-9_]*$/).optional(),
}).strict();
const pivotOutputSchema = z.object({
  key: z.string().max(2048).refine((key) => key.trim().length > 0, 'Pivot key cannot be blank'),
  enabled: z.boolean(),
  name: z.string().max(128).regex(/^$|^[A-Za-z_][A-Za-z0-9_]*$/).optional(),
}).strict();
const dynamicOutputSchema = z.object({
  key: z.string().max(2048).refine((key) => key.trim().length > 0, 'Dynamic key cannot be blank'),
  enabled: z.boolean(),
}).strict();
const extensionOutputSchema = z.object({
  key: z.string().regex(/^ext-v1:[0-9a-f]{64}$/),
  enabled: z.boolean(),
}).strict();
export const importedSourceParametersSchema = z.object({
  output: z.object({ rowGrain: text.min(1), collisionPolicy: z.enum(['', 'error', 'overwrite', 'coalesce']).optional() }).strict().optional(),
  fieldGroup: z.object({ fields: z.array(z.object({
    fieldId: text.min(1), enabled: z.boolean(), name: text.min(1), label: text.optional(), expr: sourceRecipeExpressionSchema,
    fallbacks: expressions.optional(), valueMode: z.enum(['', 'AUTO', 'FIRST', 'ALL', 'DISTINCT']).optional(),
  }).strict()).max(4096) }).strict().optional(),
  catalog: z.object({ name: text.min(1), includePaths: paths.optional(), excludePaths: paths.optional(), kinds: paths.optional(), naming: z.enum(['', 'PATH', 'PATH_SUFFIX']).optional(), outputs: z.array(catalogOutputSchema).max(4096).optional() }).strict().optional(),
  dynamic: z.object({ name: text.min(1), columnPrefix: text.optional(), source: sourceRecipeExpressionSchema, key: sourceRecipeExpressionSchema.optional(), value: sourceRecipeExpressionSchema.optional(), outputs: z.array(dynamicOutputSchema).max(4096).optional() }).strict().optional(),
  extension: z.object({ name: text.min(1), columnPrefix: text.optional(), source: sourceRecipeExpressionSchema, outputs: z.array(extensionOutputSchema).max(256).optional() }).strict().optional(),
  pivot: z.object({ name: text.min(1), discovery: z.object({ family: text.optional(), path: text.optional() }).strict().optional(),
    columnExpr: sourceRecipeExpressionSchema.optional(), valueExpr: sourceRecipeExpressionSchema.optional(), valueFallbacks: expressions.optional(),
    itemSource: sourceRecipeExpressionSchema.optional(), itemResourceType: text.optional(), columns: paths.optional(), columnAliases: z.record(z.string(), text).optional(),
    outputs: z.array(pivotOutputSchema).max(256).optional(),
  }).strict().optional(),
  expansion: z.object({ from: sourceRecipeExpressionSchema, as: text.min(1), ordinality: text.optional(), emptyPolicy: z.enum(['', 'ERROR', 'EXCLUDE', 'PRESERVE_PARENT']).optional() }).strict().optional(),
  identity: z.object({ name: text.min(1), expr: sourceRecipeExpressionSchema }).strict().optional(),
  traversal: z.object({ name: text.min(1), toResourceType: text.min(1), alias: text.optional(), from: sourceRecipeExpressionSchema.optional() }).strict().optional(),
}).strict().superRefine((value, context) => {
  if (Object.values(value).filter((entry) => entry !== undefined).length !== 1) context.addIssue({ code: 'custom', message: 'Source parameters must describe exactly one operation kind.' });
  const outputPaths = value.catalog?.outputs?.map((output) => output.fieldPath) ?? [];
  if (new Set(outputPaths).size !== outputPaths.length) context.addIssue({ code: 'custom', path: ['catalog', 'outputs'], message: 'Catalog output overrides must use unique field paths.' });
  const pivotOutputKeys = value.pivot?.outputs?.map((output) => output.key) ?? [];
  if (new Set(pivotOutputKeys).size !== pivotOutputKeys.length) context.addIssue({ code: 'custom', path: ['pivot', 'outputs'], message: 'Pivot output overrides must use unique raw keys.' });
  const dynamicOutputKeys = value.dynamic?.outputs?.map((output) => output.key) ?? [];
  if (new Set(dynamicOutputKeys).size !== dynamicOutputKeys.length) context.addIssue({ code: 'custom', path: ['dynamic', 'outputs'], message: 'Dynamic output overrides must use unique keys.' });
  const extensionOutputKeys = value.extension?.outputs?.map((output) => output.key) ?? [];
  if (new Set(extensionOutputKeys).size !== extensionOutputKeys.length) context.addIssue({ code: 'custom', path: ['extension', 'outputs'], message: 'Extension output overrides must use unique keys.' });
});
export type ImportedSourceParameters = z.infer<typeof importedSourceParametersSchema>;

export function validateEditableSourceParameters(value: ImportedSourceParameters, context: z.RefinementCtx): void {
  let nodes = 0;
  const visit = (expression: SourceRecipeExpression | undefined, path: (string | number)[], depth = 0): void => {
    if (!expression) return;
    if (depth > 64 || ++nodes > 4096) {
      context.addIssue({ code: 'custom', path, message: 'Source parameters exceed the supported expression size.' });
      return;
    }
    if (expression.document !== undefined) context.addIssue({ code: 'custom', path, message: 'Source edits cannot reference other documents.' });
    if (expression.call !== undefined && !sourceRecipeEditFunctions.some((name) => name === expression.call)) context.addIssue({ code: 'custom', path: [...path, 'call'], message: 'This function cannot be introduced by a source edit.' });
    if (Object.hasOwn(expression, 'literal') && expression.literal !== null && !['string', 'number', 'boolean'].includes(typeof expression.literal)) context.addIssue({ code: 'custom', path: [...path, 'literal'], message: 'Source edits support primitive literal values.' });
    expression.args?.forEach((argument, index) => visit(argument, [...path, 'args', index], depth + 1));
  };

  const at = (expression: SourceRecipeExpression | undefined, ...path: (string | number)[]) => visit(expression, ['parameters', ...path]);
  value.fieldGroup?.fields.forEach((field, index) => {
    at(field.expr, 'fieldGroup', 'fields', index, 'expr');
    field.fallbacks?.forEach((expression, fallback) => at(expression, 'fieldGroup', 'fields', index, 'fallbacks', fallback));
  });
  at(value.dynamic?.source, 'dynamic', 'source');
  at(value.dynamic?.key, 'dynamic', 'key');
  at(value.dynamic?.value, 'dynamic', 'value');
  at(value.extension?.source, 'extension', 'source');
  at(value.pivot?.columnExpr, 'pivot', 'columnExpr');
  at(value.pivot?.valueExpr, 'pivot', 'valueExpr');
  at(value.pivot?.itemSource, 'pivot', 'itemSource');
  value.pivot?.valueFallbacks?.forEach((expression, index) => at(expression, 'pivot', 'valueFallbacks', index));
  at(value.expansion?.from, 'expansion', 'from');
  at(value.identity?.expr, 'identity', 'expr');
  at(value.traversal?.from, 'traversal', 'from');
}
