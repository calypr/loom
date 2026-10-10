import React from 'react';
import { MantineProvider } from '@mantine/core';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { LoomOutputResult } from '../../api';
import type { ExplorerRuntimeBindingV1, ExplorerRuntimeOutputV1 } from '../../types';
import { ChartPanel, facetValues, textFor } from './components';

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

  it('shares scalar, FHIR, and array display policy with Preview', () => {
    expect(textFor('  Tissue  ')).toBe('Tissue');
    expect(textFor({ text: 'Fixation' })).toBe('Fixation');
    expect(textFor({ coding: [{ code: 'fix' }] })).toBe('fix');
    expect(textFor([null, 'active', { display: 'Ready' }])).toBe('active; Ready');
    expect(textFor({ nested: { value: 'sample' } })).toBe('{"nested":{"value":"sample"}}');
  });
});

describe('Explorer Viewer chart summary', () => {
  it('renders the exact category count from chart values and a separate missing count', () => {
    const output = {
      outputId: 'patients',
      name: 'patients',
      title: 'Patients',
      rowLabel: 'patient',
      selector: { recipe: 'recipe', translationVersion: 'v1', output: 'patients' },
      columns: [],
      table: { columns: [] },
      filters: [],
      charts: [],
      fixedFilters: {},
    } satisfies ExplorerRuntimeOutputV1;
    const binding = { column: 'gender', type: 'bar', title: 'Gender' } satisfies ExplorerRuntimeBindingV1;
    const result = {
      columns: ['gender'],
      rows: [],
      rowIds: [],
      totalCount: 0,
      pageInfo: { hasNextPage: false },
      facets: [{
        name: 'loom:patients:chart:gender',
        kind: 'TERMS',
        columns: ['gender'],
        rows: [{ key: 'female', doc_count: 1 }],
        missingCount: 1,
      }],
    } satisfies LoomOutputResult;

    const markup = renderToStaticMarkup(React.createElement(MantineProvider, null, React.createElement(ChartPanel, { binding, output, result })));

    expect(markup).toContain('aria-label="Gender chart values"');
    expect(markup).toMatch(/<th[^>]*>Category<\/th>/);
    expect(markup).toMatch(/<th[^>]*>Count<\/th>/);
    expect(markup).toMatch(/<td[^>]*>female<\/td>/);
    expect(markup).toMatch(/<td[^>]*>1<\/td>/);
    expect(markup).toContain('Missing values: 1');
  });
});
