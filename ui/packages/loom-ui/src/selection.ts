import { z } from 'zod';
import type { LoomOutputFilter } from './api';

const identity = z.string().min(1);
export const resourceRefSchema = z.object({
  project: identity,
  generation: identity,
  resourceType: identity,
  id: identity,
}).strict();
export type ResourceRef = z.infer<typeof resourceRefSchema>;

export type SelectionSourceIntent =
  | { readonly kind: 'resources'; readonly resources: { readonly refs: ReadonlyArray<ResourceRef>; readonly resourceType?: string } }
  | { readonly kind: 'publishedOutput'; readonly publishedOutput: {
    readonly revisionId: string;
    readonly outputId: string;
    readonly filters?: ReadonlyArray<LoomOutputFilter>;
  } };

const filterSchema = z.object({
  column: identity,
  op: identity,
  value: z.unknown().optional(),
}).strict();

export const selectionRevisionSchema = z.object({
  id: identity,
  project: identity,
  generation: identity,
  resourceType: identity,
  rule: z.object({
    kind: z.enum(['EXPLICIT', 'ALL_MATCHING']),
    filters: z.array(filterSchema).optional(),
  }).strict(),
  source: z.object({
    kind: z.enum(['EXPLICIT_REFS', 'PUBLISHED_OUTPUT']),
    generation: identity.optional(),
    revisionId: identity.optional(),
    receiptId: identity.optional(),
    executionId: identity.optional(),
    outputId: identity.optional(),
    schemaDigest: identity.optional(),
    resourceType: identity.optional(),
    sourceIdColumn: identity.optional(),
  }).strict(),
  exclusions: z.array(resourceRefSchema).optional(),
  scopeDigest: identity,
  ruleDigest: identity,
  membershipDigest: identity,
  memberCount: z.number().int().nonnegative().safe(),
  memberBytes: z.number().int().nonnegative().safe(),
  complete: z.literal(true),
  idempotencyKey: identity.optional(),
  createdAt: z.string().datetime(),
  completedAt: z.string().datetime().optional(),
}).strict();
export type SelectionRevision = z.infer<typeof selectionRevisionSchema>;

export const selectionPageSchema = z.object({
  revision: selectionRevisionSchema,
  members: z.array(z.object({
    ref: resourceRefSchema,
    ordinal: z.number().int().nonnegative().safe().optional(),
    memberKey: identity.optional(),
  }).strict()),
  nextCursor: identity.optional(),
}).strict();
export type SelectionPage = z.infer<typeof selectionPageSchema>;
