import { z } from 'zod';

const cellTraceBindingSchema = z.object({
  receiptId: z.string().min(1),
  outputId: z.string().min(1),
  project: z.string().min(1),
  explorerId: z.string().min(1),
  generation: z.string().min(1),
  scopeDigest: z.string().min(1),
}).strict();

const cellTraceContributionSchema = z.object({
  resourceType: z.string().min(1).optional(),
  resourceId: z.string().min(1).optional(),
  value: z.unknown(),
}).strict();

const cellTraceFeatureSchema = z.object({
  outputId: z.string().min(1),
  column: z.string().min(1),
  authoredColumn: z.string().min(1),
  occurrenceId: z.string().min(1),
  label: z.string().min(1),
  logicalType: z.string().min(1),
  sourceResourceType: z.string().optional(),
  sourcePath: z.string().optional(),
  projectionMode: z.string(),
  lossless: z.boolean(),
  lossReasons: z.array(z.string()),
}).strict();

const completeTraceFields = {
  rowId: z.string().min(1),
  column: z.string().min(1),
  value: z.unknown(),
  contributions: z.array(cellTraceContributionSchema),
  hasMore: z.boolean(),
  nextOffset: z.number().int().nonnegative(),
  omissionCode: z.string().optional(),
  complete: z.literal(true),
};

const cellTraceValueSchema = z.object({
  ...completeTraceFields,
  status: z.literal('VALUE'),
}).strict();

const cellTraceNoMatchSchema = z.object({
  ...completeTraceFields,
  status: z.literal('NO_MATCH'),
}).strict();

const cellTraceRecordedNullSchema = z.object({
  ...completeTraceFields,
  status: z.literal('RECORDED_NULL'),
}).strict();

const cellTraceAmbiguousSchema = z.object({
  ...completeTraceFields,
  status: z.literal('AMBIGUOUS'),
}).strict();

const cellTraceInvalidTypeSchema = z.object({
  ...completeTraceFields,
  status: z.literal('INVALID_TYPE'),
}).strict();

const cellTraceIncompatibleUnitSchema = z.object({
  ...completeTraceFields,
  status: z.literal('INCOMPATIBLE_UNIT'),
}).strict();

const cellTraceIncompleteSchema = z.object({
  rowId: z.string().min(1),
  column: z.string().min(1),
  value: z.unknown(),
  status: z.literal('INCOMPLETE'),
  contributions: z.array(cellTraceContributionSchema),
  hasMore: z.literal(false),
  nextOffset: z.number().int().nonnegative(),
  omissionCode: z.string().min(1),
  complete: z.literal(false),
}).strict();

export const cellTraceResponseSchema = z.object({
  binding: cellTraceBindingSchema,
  feature: cellTraceFeatureSchema,
  trace: z.discriminatedUnion('status', [
    cellTraceValueSchema,
    cellTraceNoMatchSchema,
    cellTraceRecordedNullSchema,
    cellTraceAmbiguousSchema,
    cellTraceInvalidTypeSchema,
    cellTraceIncompatibleUnitSchema,
    cellTraceIncompleteSchema,
  ]),
}).strict();

export type CellTraceResponse = z.infer<typeof cellTraceResponseSchema>;
export type CellTrace = CellTraceResponse['trace'];
export type CellTraceContribution = CellTrace['contributions'][number];
