import { describe, expect, it } from 'vitest';
import { facetValues } from './components';

describe('Explorer Viewer facet values', () => {
  it('coalesces duplicate display values for Mantine controls', () => {
    expect(facetValues({
      name: 'identifier-use',
      kind: 'TERMS',
      columns: ['identifier_use'],
      rows: [
        { key: 'official', doc_count: 2 },
        { key: 'official', doc_count: 3 },
        { key: 'secondary', doc_count: 1 },
      ],
    }, 'identifier_use')).toEqual([
      { value: 'official', count: '5' },
      { value: 'secondary', count: '1' },
    ]);
  });
});
