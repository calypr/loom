// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCatalog } from '../../src/types';
import type { DraftTable } from '../../src/features/ExplorerBuilder/authoring/model';
import { ColumnSelector } from '../../src/features/ExplorerBuilder/components/ColumnSelector';
import { driveColumnSelectorLocalSearch } from './peripheral';

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot',
  generation: 'generation',
  routePolicy: {},
  nodes: [{
    nodeId: 'research-subject',
    resourceType: 'ResearchSubject',
    rowRootEligible: true,
    populated: true,
    documentCount: 1,
  }],
  edges: [],
  candidates: [],
};

const table: DraftTable = {
  outputId: 'Patient',
  tabId: 'patient',
  title: 'Patient',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'Patient', title: 'Patient' },
    rootResourceType: 'ResearchSubject',
    route: { occurrenceId: 'base', resourceType: 'ResearchSubject' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [{
      column: 'research_subject_identifier',
      label: 'Research Subject ID',
      occurrenceId: 'base',
      source: {
        kind: 'field',
        field: { path: 'identifier[].value', projectionMode: 'FIRST' },
      },
      table: { visible: true, order: 0 },
    }],
  },
};

describe('peripheral Builder adapters', () => {
  it('drives the mounted ColumnSelector local search control', () => {
    render(
      <ColumnSelector
        catalog={catalog}
        table={table}
        occurrenceId="base"
        disabled={false}
        onAdd={vi.fn()}
        onAddAll={vi.fn()}
        onChange={vi.fn()}
        onSourceChange={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    expect(screen.queryByRole('textbox', { name: 'Display name for configured Research Subject ID' })).not.toBeNull();
    driveColumnSelectorLocalSearch({
      uiAction: {
        affordanceId: 'builder.column_selector.local_search',
        runnerActionId: 'driveColumnSelectorLocalSearch',
        values: { query: 'no matching column' },
      },
      requestStep: null,
    });

    const search = screen.getByRole('textbox', { name: 'Search columns' });
    if (!(search instanceof HTMLInputElement)) throw new Error('Search columns control is not an input.');
    expect(search.value).toBe('no matching column');
    expect(screen.queryByRole('textbox', { name: 'Display name for configured Research Subject ID' })).toBeNull();
  });
});
