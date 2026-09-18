import { z } from 'zod';
import { explorerColumnSourceSchema, contributorPredicateSchema } from './types';

const idSchema = z.string().trim().min(1);

export const interpretationApplicabilitySchema = z.object({
  resourceTypes: z.array(idSchema).optional(),
  sourceProfiles: z.array(z.string()).optional(),
  sourceCanonical: z.array(z.string()).optional(),
  logicalTypes: z.array(z.string()).optional(),
  cardinalities: z.array(z.string()).optional(),
  schemaDigests: z.array(z.string()).optional(),
}).strict();

export const interpretationStructuralMatchSchema = z.object({
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

export const interpretationFeatureDefinitionSchema = z.object({
  source: explorerColumnSourceSchema,
  contributor: contributorPredicateSchema.optional(),
}).strict();

export const interpretationRuleSchema = z.object({
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

export const interpretationLibrarySchema = z.object({
  id: idSchema,
  project: idSchema,
  headRevisionId: idSchema.optional(),
  headDigest: idSchema.optional(),
  createdAt: z.string().min(1),
  updatedAt: z.string().min(1),
}).strict();

export const interpretationLibraryViewSchema = z.object({
  library: interpretationLibrarySchema,
  head: interpretationRevisionSchema.optional(),
}).strict();

export const interpretationLibraryListResponseSchema = z.object({
  project: idSchema,
  libraries: z.array(interpretationLibraryViewSchema),
}).strict();

export const interpretationPreviewSampleSchema = z.object({
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
export type InterpretationFeatureDefinition = z.infer<typeof interpretationFeatureDefinitionSchema>;
export type InterpretationRule = z.infer<typeof interpretationRuleSchema>;
export type InterpretationRevision = z.infer<typeof interpretationRevisionSchema>;
export type InterpretationLibraryView = z.infer<typeof interpretationLibraryViewSchema>;
export type InterpretationPreviewResponse = z.infer<typeof interpretationPreviewResponseSchema>;
