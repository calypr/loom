import { z } from 'zod';
import { explorerColumnSourceSchema, contributorPredicateSchema } from './types';

const idSchema = z.string().trim().min(1);

const interpretationApplicabilitySchema = z.object({
  resourceTypes: z.array(idSchema).optional(),
  sourceProfiles: z.array(z.string()).optional(),
  sourceCanonical: z.array(z.string()).optional(),
  logicalTypes: z.array(z.string()).optional(),
  cardinalities: z.array(z.string()).optional(),
  schemaDigests: z.array(z.string()).optional(),
}).strict();

const interpretationStructuralMatchSchema = z.object({
  resourceType: z.string().optional(),
  sourceProfile: z.string().optional(),
  sourceCanonical: z.string().optional(),
  owningScope: z.string().optional(),
  system: z.string().optional(),
  code: z.string().optional(),
  extensionUrlPath: z.array(z.string()).optional(),
  logicalType: z.string().optional(),
  cardinality: z.string().optional(),
}).strict();

const interpretationFeatureDefinitionSchema = z.object({
  source: explorerColumnSourceSchema,
  contributor: contributorPredicateSchema.optional(),
}).strict();

const interpretationRuleSchema = z.object({
  id: idSchema,
  priority: z.number().int().optional(),
  match: interpretationStructuralMatchSchema,
  definition: interpretationFeatureDefinitionSchema,
}).strict();

export const interpretationRevisionSchema = z.object({
  id: idSchema,
  project: idSchema,
  libraryId: idSchema,
  parentRevisionId: idSchema.optional(),
  parentDigest: idSchema.optional(),
  contentDigest: idSchema,
  applicability: interpretationApplicabilitySchema,
  rules: z.array(interpretationRuleSchema),
  author: idSchema,
  explanation: z.string().min(1),
  createdAt: z.string().min(1),
}).strict();

const interpretationLibrarySchema = z.object({
  id: idSchema,
  project: idSchema,
  headRevisionId: idSchema.optional(),
  headDigest: idSchema.optional(),
  createdAt: z.string().min(1),
  updatedAt: z.string().min(1),
}).strict();

const interpretationLibraryViewSchema = z.object({
  library: interpretationLibrarySchema,
  head: interpretationRevisionSchema.optional(),
}).strict();

export const interpretationLibraryListResponseSchema = z.object({
  project: idSchema,
  libraries: z.array(interpretationLibraryViewSchema),
}).strict();

const interpretationRevisionSummarySchema = z.object({
  id: idSchema,
  libraryId: idSchema,
  contentDigest: idSchema,
  author: idSchema,
  explanation: z.string().min(1),
  createdAt: z.string().min(1),
}).strict();

const interpretationLibrarySummarySchema = z.object({
  id: idSchema,
  headRevisionId: idSchema.optional(),
  headDigest: idSchema.optional(),
  head: interpretationRevisionSummarySchema.optional(),
  updatedAt: z.string().min(1),
}).strict();

const configuredColumnReadyResolutionSchema = z.object({
  state: z.literal('READY'),
  capabilityCandidateIds: z.array(idSchema).min(1),
  applicableRevisionIds: z.array(idSchema),
}).strict();

const configuredColumnUnavailableResolutionSchema = (
  state: 'MISSING' | 'AMBIGUOUS' | 'UNSUPPORTED',
) => z.object({
  state: z.literal(state),
  reason: z.string().min(1),
}).strict();

const configuredColumnResolutionSchema = z.discriminatedUnion('state', [
  configuredColumnReadyResolutionSchema,
  configuredColumnUnavailableResolutionSchema('MISSING'),
  configuredColumnUnavailableResolutionSchema('AMBIGUOUS'),
  configuredColumnUnavailableResolutionSchema('UNSUPPORTED'),
]);

const configuredColumnContextSchema = z.object({
  outputId: idSchema,
  column: idSchema,
  occurrenceId: idSchema,
  resolution: configuredColumnResolutionSchema,
}).strict();

export const configuredColumnContextResponseSchema = z.object({
  snapshotToken: idSchema,
  draftVersion: z.number().int().positive(),
  draftDigest: idSchema,
  libraries: z.array(interpretationLibrarySummarySchema),
  pinnedRevisions: z.array(interpretationRevisionSummarySchema),
  columns: z.array(configuredColumnContextSchema),
}).strict();

const interpretationPreviewSampleSchema = z.object({
  rowId: idSchema,
  before: z.record(z.string(), z.unknown()),
  after: z.record(z.string(), z.unknown()),
  state: z.enum(['UNCHANGED', 'CHANGED', 'RESOLVED', 'UNRESOLVED']),
}).strict();

export const interpretationPreviewResponseSchema = z.object({
  baseReceiptId: idSchema,
  candidateReceiptId: idSchema,
  outputId: idSchema,
  column: idSchema,
  revisionId: idSchema,
  completeness: z.enum(['COMPLETE', 'INCOMPLETE']),
  samples: z.array(interpretationPreviewSampleSchema),
  counts: z.object({
    compared: z.number().int().nonnegative(),
    changed: z.number().int().nonnegative(),
    resolved: z.number().int().nonnegative(),
    unresolved: z.number().int().nonnegative(),
  }).strict(),
}).strict();

export type InterpretationApplicability = z.infer<typeof interpretationApplicabilitySchema>;
type InterpretationFeatureDefinition = z.infer<typeof interpretationFeatureDefinitionSchema>;
export type InterpretationRule = z.infer<typeof interpretationRuleSchema>;
export type InterpretationRevision = z.infer<typeof interpretationRevisionSchema>;
export type InterpretationLibraryView = z.infer<typeof interpretationLibraryViewSchema>;
export type InterpretationPreviewResponse = z.infer<typeof interpretationPreviewResponseSchema>;
export type InterpretationRevisionSummary = z.infer<typeof interpretationRevisionSummarySchema>;
type InterpretationLibrarySummary = z.infer<typeof interpretationLibrarySummarySchema>;
type ConfiguredColumnResolution = z.infer<typeof configuredColumnResolutionSchema>;
type ConfiguredColumnContext = z.infer<typeof configuredColumnContextSchema>;
export type ConfiguredColumnContextResponse = z.infer<typeof configuredColumnContextResponseSchema>;
