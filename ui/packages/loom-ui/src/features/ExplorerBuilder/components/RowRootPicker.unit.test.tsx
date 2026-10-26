// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCatalog } from '../../../types';
import { RowRootPicker } from './RowRootPicker';

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  routePolicy: {},
  nodes: [
    {
      nodeId: 'observation-node',
      resourceType: 'Observation',
      rowRootEligible: true,
      populated: false,
      documentCount: 0,
    },
    {
      nodeId: 'patient-node',
      resourceType: 'Patient',
      rowRootEligible: true,
      populated: true,
      documentCount: 1_234,
    },
    {
      nodeId: 'hidden-node',
      resourceType: 'HiddenInternalType',
      rowRootEligible: false,
      populated: true,
      documentCount: 99,
    },
  ],
  edges: [],
  candidates: [],
};

describe('RowRootPicker', () => {
  it('searches eligible server choices and selects only populated rows', () => {
    const onChoose = vi.fn();
    render(<RowRootPicker catalog={catalog} disabled={false} onChoose={onChoose} />);

    expect(screen.getByText('1,234 authorized records')).toBeInTheDocument();
    expect(screen.getByText('No authorized records in this dataset')).toBeInTheDocument();
    expect(screen.queryByText('HiddenInternalType')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Choose Observation rows' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: 'Choose Patient rows' }));
    expect(onChoose).toHaveBeenCalledOnce();
    expect(onChoose).toHaveBeenCalledWith('patient-node');

    fireEvent.change(screen.getByLabelText('Search row types'), {
      target: { value: 'obser' },
    });
    expect(screen.queryByRole('button', { name: 'Choose Patient rows' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Choose Observation rows' })).toBeInTheDocument();
  });

  it('explains when no eligible row type matches', () => {
    render(<RowRootPicker catalog={catalog} disabled={false} onChoose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('Search row types'), {
      target: { value: 'specimen' },
    });
    expect(screen.getByText('No eligible row types match this search.')).toBeInTheDocument();
  });
});
