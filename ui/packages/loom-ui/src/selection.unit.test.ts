import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from './api';
import { selectionRevisionSchema } from './selection';

const ref = { project: 'org/project', generation: 'g1', resourceType: 'Specimen', id: 'specimen-1' };
const revision = {
  id: 'selection-1', project: 'org/project', generation: 'g1', resourceType: 'Specimen',
  rule: { kind: 'EXPLICIT' }, source: { kind: 'EXPLICIT_REFS' },
  scopeDigest: 'scope', ruleDigest: 'rule', membershipDigest: 'membership',
  memberCount: 1, memberBytes: 80, complete: true,
  createdAt: '2026-09-16T00:00:00Z',
};

describe('immutable selection client', () => {
  it('preserves explicit resource identity and mutation authorization', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(revision)));
    const client = createLoomClient({ fetch });
    expect(await client.createSelection({
      project: 'org/project', explorerId: 'files', authResourcePath: '/projects/one',
      snapshotToken: 'snapshot', idempotencyKey: 'command-1',
      source: { kind: 'resources', resources: { refs: [ref] } },
    })).toEqual(revision);
    expect(String(fetch.mock.calls[0][0])).toBe('/api/v1/projects/org%252Fproject/explorers/files/selections?auth_resource_path=%2Fprojects%2Fone');
    expect(JSON.parse(String(fetch.mock.calls[0][1]?.body))).toEqual({
      snapshotToken: 'snapshot', idempotencyKey: 'command-1',
      source: { kind: 'resources', resources: { refs: [ref] } },
    });
  });

  it('sends exact published revision, filters, and exclusions without execution metadata', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(revision)));
    const client = createLoomClient({ fetch });
    await client.createSelection({
      project: 'org/project', explorerId: 'files', snapshotToken: 'snapshot', idempotencyKey: 'command-2',
      source: { kind: 'publishedOutput', publishedOutput: {
        revisionId: 'published-1', outputId: 'files', filters: [{ column: 'size', op: 'GT', value: 12 }],
      } },
      exclusions: [ref],
    });
    expect(JSON.parse(String(fetch.mock.calls[0][1]?.body))).toEqual({
      snapshotToken: 'snapshot', idempotencyKey: 'command-2',
      source: { kind: 'publishedOutput', publishedOutput: {
        revisionId: 'published-1', outputId: 'files', filters: [{ column: 'size', op: 'GT', value: 12 }],
      } }, exclusions: [ref],
    });
  });

  it('rechecks authorization on every page request instead of caching counts', async () => {
    const page = { revision, members: [{ ref }], nextCursor: 'next+/=' };
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockImplementationOnce(async () => new Response(JSON.stringify(page)))
      .mockImplementationOnce(async () => new Response(JSON.stringify({ error: { code: 'FORBIDDEN', message: 'scope changed' } }), { status: 403 }));
    const client = createLoomClient({ fetch });
    const args = { project: 'org/project', explorerId: 'files', selectionRevision: 'selection-1', cursor: 'next+/=', limit: 10 };
    expect(await client.getSelection(args)).toEqual(page);
    await expect(client.getSelection(args)).rejects.toMatchObject({ code: 'FORBIDDEN' });
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(String(fetch.mock.calls[0][0])).toBe('/api/v1/projects/org%252Fproject/explorers/files/selections/selection-1?cursor=next%2B%2F%3D&limit=10');
  });

  it('rejects incomplete membership and invalid counts', () => {
    expect(selectionRevisionSchema.safeParse({ ...revision, complete: false }).success).toBe(false);
    expect(selectionRevisionSchema.safeParse({ ...revision, memberCount: -1 }).success).toBe(false);
    expect(selectionRevisionSchema.safeParse({ ...revision, memberCount: Number.MAX_SAFE_INTEGER + 1 }).success).toBe(false);
  });
});
