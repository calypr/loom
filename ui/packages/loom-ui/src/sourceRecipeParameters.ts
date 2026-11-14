import { z } from 'zod';

/** The existing recipe expression wire format; this is not another authored AST. */
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
export const importedSourceParametersSchema = z.object({
  output: z.object({ rowGrain: text.min(1), collisionPolicy: z.enum(['', 'error', 'overwrite', 'coalesce']).optional() }).strict().optional(),
  fieldGroup: z.object({ fields: z.array(z.object({
    fieldId: text.min(1), enabled: z.boolean(), name: text.min(1), label: text.optional(), expr: sourceRecipeExpressionSchema,
    fallbacks: expressions.optional(), valueMode: z.enum(['', 'AUTO', 'FIRST', 'ALL', 'DISTINCT']).optional(),
  }).strict()).max(4096) }).strict().optional(),
  catalog: z.object({ name: text.min(1), includePaths: paths.optional(), excludePaths: paths.optional(), kinds: paths.optional(), naming: z.enum(['', 'PATH', 'PATH_SUFFIX']).optional() }).strict().optional(),
  dynamic: z.object({ name: text.min(1), columnPrefix: text.optional(), source: sourceRecipeExpressionSchema, key: sourceRecipeExpressionSchema.optional(), value: sourceRecipeExpressionSchema.optional() }).strict().optional(),
  extension: z.object({ name: text.min(1), columnPrefix: text.optional(), source: sourceRecipeExpressionSchema }).strict().optional(),
  pivot: z.object({ name: text.min(1), discovery: z.object({ family: text.optional(), path: text.optional() }).strict().optional(),
    columnExpr: sourceRecipeExpressionSchema.optional(), valueExpr: sourceRecipeExpressionSchema.optional(), valueFallbacks: expressions.optional(),
    itemSource: sourceRecipeExpressionSchema.optional(), itemResourceType: text.optional(), columns: paths.optional(), columnAliases: z.record(z.string(), text).optional(),
  }).strict().optional(),
  expansion: z.object({ from: sourceRecipeExpressionSchema, as: text.min(1), ordinality: text.optional(), emptyPolicy: z.enum(['', 'ERROR', 'EXCLUDE', 'PRESERVE_PARENT']).optional() }).strict().optional(),
  identity: z.object({ name: text.min(1), expr: sourceRecipeExpressionSchema }).strict().optional(),
  traversal: z.object({ name: text.min(1), toResourceType: text.min(1), alias: text.optional(), from: sourceRecipeExpressionSchema.optional() }).strict().optional(),
}).strict().superRefine((value, context) => {
  if (Object.values(value).filter((entry) => entry !== undefined).length !== 1) context.addIssue({ code: 'custom', message: 'Source parameters must describe exactly one operation kind.' });
});
export type ImportedSourceParameters = z.infer<typeof importedSourceParametersSchema>;

/** Read descriptors preserve existing pinned expressions; new edits use the bounded supported functions. */
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
  // Only typed expression slots are expressions. In particular, literal pivot
  // alias keys may legitimately be named "call", "document", or "literal".
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
