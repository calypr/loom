import type { ConstructionRouteStep, SemanticInventoryItem, SemanticInventoryItemIdentity } from './types';

type SemanticIdentityFields = Pick<SemanticInventoryItem, 'selectionMode' | 'familyId' | 'conceptId' | 'bindingId'>;

export const sameConstructionRoute = (
  left: ReadonlyArray<ConstructionRouteStep>,
  right: ReadonlyArray<ConstructionRouteStep>,
): boolean =>
  left.length === right.length &&
  left.every((step, index) => {
    const candidate = right[index];
    return candidate !== undefined &&
      step.edgeId === candidate.edgeId &&
      step.fromNodeId === candidate.fromNodeId &&
      step.toNodeId === candidate.toNodeId &&
      step.fromResourceType === candidate.fromResourceType &&
      step.toResourceType === candidate.toResourceType &&
      step.relationship === candidate.relationship &&
      step.storageDirection === candidate.storageDirection &&
      step.matchMode === candidate.matchMode;
  });

export const semanticInventoryIdentityKey = (
  identity: SemanticInventoryItemIdentity,
): string => {
  switch (identity.kind) {
    case 'EXACT_CONCEPT':
      return JSON.stringify([identity.kind, identity.conceptId, identity.bindingId]);
    case 'SYSTEM_VERSION_CATEGORY':
      return JSON.stringify([identity.kind, identity.familyId, identity.bindingId]);
    default: {
      const exhaustive: never = identity;
      return exhaustive;
    }
  }
};

export const semanticIdentityForItem = (
  item: SemanticIdentityFields,
): SemanticInventoryItemIdentity | undefined => {
  if (item.selectionMode === 'SYSTEM_VERSION_CATEGORY' && item.familyId) {
    return {
      kind: 'SYSTEM_VERSION_CATEGORY',
      familyId: item.familyId,
      bindingId: item.bindingId,
    };
  }
  if (item.selectionMode === undefined && item.familyId === undefined) {
    return { kind: 'EXACT_CONCEPT', conceptId: item.conceptId, bindingId: item.bindingId };
  }
  return undefined;
};

export const sameSemanticItemIdentity = (
  left: SemanticInventoryItemIdentity,
  right: SemanticInventoryItemIdentity,
): boolean => left.kind === right.kind && left.bindingId === right.bindingId &&
  (left.kind === 'EXACT_CONCEPT'
    ? right.kind === 'EXACT_CONCEPT' && left.conceptId === right.conceptId
    : right.kind === 'SYSTEM_VERSION_CATEGORY' && left.familyId === right.familyId);

export const semanticItemMatchesIdentity = (
  item: SemanticIdentityFields,
  identity: SemanticInventoryItemIdentity,
): boolean => {
  const candidate = semanticIdentityForItem(item);
  return candidate !== undefined && sameSemanticItemIdentity(candidate, identity);
};

type ChoiceRequestScope = {
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId: string;
  readonly stageId: string;
};

export const sameChoiceRequestScope = (
  response: ChoiceRequestScope,
  request: ChoiceRequestScope,
): boolean => response.snapshotToken === request.snapshotToken
  && response.draftVersion === request.draftVersion
  && response.draftDigest === request.draftDigest
  && response.outputId === request.outputId
  && response.stageId === request.stageId;

type DraftRevision = {
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
};

export type DraftIdentity = DraftRevision & {
  readonly ownerKey: string;
  readonly outputId?: string;
  readonly proposalId?: string;
  readonly limit?: number;
};

export const sameDraftRevision = (left: DraftRevision, right: DraftRevision): boolean =>
  left.snapshotToken === right.snapshotToken
  && left.draftVersion === right.draftVersion
  && left.draftDigest === right.draftDigest;
