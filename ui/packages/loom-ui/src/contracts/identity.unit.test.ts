import { describe, expect, it } from 'vitest';
import {
  sameDraftRevision,
  sameSemanticItemIdentity,
  semanticIdentityForItem,
  semanticItemMatchesCatalogIdentity,
  semanticItemMatchesClientIdentity,
} from './identity';
import type { SemanticInventoryItemIdentity } from './types';

const concept = { conceptId: 'concept', bindingId: 'binding' };
const category = { ...concept, selectionMode: 'SYSTEM_VERSION_CATEGORY' as const, familyId: 'family' };
const exactIdentity = { kind: 'EXACT_CONCEPT', ...concept } satisfies SemanticInventoryItemIdentity;
const categoryIdentity = {
  kind: 'SYSTEM_VERSION_CATEGORY',
  familyId: category.familyId,
  bindingId: category.bindingId,
} satisfies SemanticInventoryItemIdentity;

describe('semantic inventory identity', () => {
  it.each([
    { name: 'exact concept', item: concept, expected: exactIdentity },
    { name: 'system/version category', item: category, expected: categoryIdentity },
    { name: 'category mode without a family', item: { ...category, familyId: undefined }, expected: undefined },
    { name: 'family without category mode', item: { ...concept, familyId: 'family' }, expected: undefined },
    { name: 'empty family', item: { ...category, familyId: '' }, expected: undefined },
  ])('derives the identity for $name', ({ item, expected }) => {
    expect(semanticIdentityForItem(item)).toEqual(expected);
  });

  it.each([
    { name: 'matching concept', item: concept, identity: exactIdentity, expected: true },
    { name: 'different concept', item: concept, identity: { ...exactIdentity, conceptId: 'other' }, expected: false },
    { name: 'different concept binding', item: concept, identity: { ...exactIdentity, bindingId: 'other' }, expected: false },
    { name: 'matching category', item: category, identity: categoryIdentity, expected: true },
    { name: 'different category family', item: category, identity: { ...categoryIdentity, familyId: 'other' }, expected: false },
    { name: 'different category binding', item: category, identity: { ...categoryIdentity, bindingId: 'other' }, expected: false },
    { name: 'different selection mode', item: category, identity: exactIdentity, expected: false },
    { name: 'category mode without a family', item: { ...category, familyId: undefined }, identity: categoryIdentity, expected: false },
    { name: 'family without category mode', item: { ...concept, familyId: 'family' }, identity: exactIdentity, expected: false },
  ])('checks $name', ({ item, identity, expected }) => {
    expect(semanticItemMatchesCatalogIdentity(item, identity)).toBe(expected);
    expect(semanticItemMatchesClientIdentity(item, identity)).toBe(expected);
  });

  const divergentCases = [
    {
      name: 'empty family on both category sides',
      item: { ...category, familyId: '' },
      identity: { ...categoryIdentity, familyId: '' },
      clientMatches: true,
    },
    {
      name: 'missing family on both category sides',
      item: { ...category, familyId: undefined },
      identity: { ...categoryIdentity, familyId: undefined },
      clientMatches: true,
    },
    {
      name: 'null family on both category sides',
      item: { ...category, familyId: null },
      identity: { ...categoryIdentity, familyId: null },
      clientMatches: true,
    },
    {
      name: 'missing concept versus null',
      item: { ...concept, conceptId: undefined },
      identity: { ...exactIdentity, conceptId: null },
      clientMatches: false,
    },
    {
      name: 'missing binding versus null',
      item: { ...concept, bindingId: undefined },
      identity: { ...exactIdentity, bindingId: null },
      clientMatches: false,
    },
    {
      name: 'unknown category identity kind',
      item: category,
      identity: { ...categoryIdentity, kind: 'UNKNOWN' },
      clientMatches: true,
    },
    {
      name: 'distinct concept objects with the same JSON',
      item: { ...concept, conceptId: { id: 'same' } },
      identity: { ...exactIdentity, conceptId: { id: 'same' } },
      clientMatches: false,
    },
    {
      name: 'NaN on both concept sides',
      item: { ...concept, conceptId: Number.NaN },
      identity: { ...exactIdentity, conceptId: Number.NaN },
      clientMatches: false,
    },
  ].map(({ item, identity, ...testCase }) => ({
    ...testCase,
    item: item as unknown as Parameters<typeof semanticIdentityForItem>[0],
    identity: identity as unknown as SemanticInventoryItemIdentity,
  }));

  it.each(divergentCases)('retains each original matcher for $name', ({ item, identity, clientMatches }) => {
    expect(semanticItemMatchesClientIdentity(item, identity)).toBe(clientMatches);
    expect(semanticItemMatchesCatalogIdentity(item, identity)).toBe(!clientMatches);
  });

  for (const { name, item, identity } of divergentCases) {
    it.fails(`cannot use one derived matcher for ${name}`, () => {
      const derived = semanticIdentityForItem(item);
      const merged = derived !== undefined && sameSemanticItemIdentity(derived, identity);
      expect([merged, merged]).toEqual([
        semanticItemMatchesClientIdentity(item, identity),
        semanticItemMatchesCatalogIdentity(item, identity),
      ]);
    });
  }
});

describe('draft revision identity', () => {
  const revision = { snapshotToken: 'snapshot', draftVersion: 2, draftDigest: 'digest' };

  it('accepts the same revision', () => {
    expect(sameDraftRevision(revision, { ...revision })).toBe(true);
  });

  it.each([
    { ...revision, snapshotToken: 'previous' },
    { ...revision, draftVersion: 1 },
    { ...revision, draftDigest: 'other' },
  ])('rejects a different revision %j', (other) => {
    expect(sameDraftRevision(revision, other)).toBe(false);
  });
});
