import React from 'react';
import { MantineProvider } from '@mantine/core';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { LoomOutputResult } from '../../api';
import type { ExplorerRuntimeBindingV1, ExplorerRuntimeOutputV1, ExplorerRuntimeV1 } from '../../types';
import { createViewerReducerState } from './reducer';
import { ChartPanel, OutputTable, facetValues, textFor } from './components';

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


describe('Viewer file action URLs', () => {
  const id = '11111111-2222-4333-8444-555555555555';
  const href = (baseURL: string | undefined, action = 'file_download'): string => {
    const output: ExplorerRuntimeOutputV1 = {
      outputId: 'files', name: 'files', title: 'Files', rowLabel: 'file',
      selector: { recipe: 'recipe', translationVersion: 'v1', output: 'files' },
      columns: [{ column: 'file', label: 'File', logicalType: 'string', visible: true, order: 0, filterable: false, chartable: false }],
      table: { columns: [{ column: 'file', label: 'File', visible: true, cellRenderer: 'fileActions' }] },
      filters: [], charts: [], fixedFilters: {},
    };
    const runtime: ExplorerRuntimeV1 = {
      outputs: [output], sharedFilters: {}, diagnostics: [],
      fileActions: { extensions: { default: [action] }, actions: baseURL === undefined ? {} : { [action]: baseURL } },
    };
    const result: LoomOutputResult = {
      columns: ['file'], rows: [{ file: id, file_name: 'fixture.txt' }], rowIds: ['file-row'],
      totalCount: 1, pageInfo: { hasNextPage: false }, facets: [],
    };
    const markup = renderToStaticMarkup(React.createElement(MantineProvider, null,
      React.createElement(OutputTable, { runtime, output, result, state: createViewerReducerState(runtime), dispatch: () => undefined })));
    const match = /href="([^"]+)"[^>]+aria-label="file_(?:download|image)"/.exec(markup);
    expect(match, markup).not.toBeNull();
    return match![1].replaceAll('&amp;', '&');
  };
  it.each([
    [undefined, `/download/${id}?redirect=true`],
    ['/download/', `/download/${id}?redirect=true`],
    ['/download?source=viewer', `/download/${id}?source=viewer&redirect=true`],
    ['../download?source=viewer#details', `../download/${id}?source=viewer&redirect=true#details`],
    ['https://files.example/download/?source=viewer#details', `https://files.example/download/${id}?source=viewer&redirect=true#details`],
    ['//files.example/download?source=viewer#details', `//files.example/download/${id}?source=viewer&redirect=true#details`],
    ['/download?redirect=false&source=viewer&redirect=false#details', `/download/${id}?redirect=true&source=viewer#details`],
    ['/download?source=a%20b&source=c#details/', `/download/${id}?source=a+b&source=c&redirect=true#details/`],
  ])('renders the requested file before query/fragment with one redirect: %s', (base, expected) => {
    expect(href(base)).toBe(expected);
  });
  it('preserves image action query/fragment without adding a download redirect', () => {
    expect(href('/image-viewer/view?mode=full#page2', 'file_image')).toBe(`/image-viewer/view/${id}?mode=full#page2`);
  });
});
