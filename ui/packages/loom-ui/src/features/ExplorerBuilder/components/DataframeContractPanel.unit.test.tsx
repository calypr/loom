// @vitest-environment jsdom
import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { EXPLORER_AUTHORING_API_VERSION, type ExplorerBuilderCompileResult } from '../../../types';
import { DataframeContractPanel } from './DataframeContractPanel';

const receipt: ExplorerBuilderCompileResult = {
  apiVersion: EXPLORER_AUTHORING_API_VERSION,
  kind: 'ExplorerBuilderReceipt',
  receiptId: 'receipt-1',
  snapshotToken: 'snapshot-1',
  builder: {
    apiVersion: EXPLORER_AUTHORING_API_VERSION,
    kind: 'ExplorerBuilderWorkspace',
    semanticsVersion: 4,
    explorer: { title: 'Test' },
    documents: [],
    tabs: [],
  },
  outputs: [{ outputId: 'observations', columns: [] }],
  diagnostics: [],
};

describe('Dataframe contract evidence', () => {
  it('does not turn absent evidence into lossless or ML-ready claims', () => {
    render(<DataframeContractPanel receipt={receipt} outputId="observations" />);
    expect(screen.getAllByText(/Not assessed/)).toHaveLength(2);
    expect(screen.queryByText(/ML-ready/)).toBeNull();
    expect(screen.getByText(/Data quality and feature meaning still need review/)).toBeInTheDocument();
  });

  it('explains record loss even for scalar output', () => {
    render(<DataframeContractPanel receipt={{ ...receipt, outputs: [{
      outputId: 'observations', columns: [], lossless: false,
      structuralSuitability: 'scalar', lossReasons: ['RELATED_RESOURCE_FIRST_LOSSY'],
    }] }} outputId="observations" />);
    expect(screen.getByText(/Only the first related record is kept/)).toBeInTheDocument();
    expect(screen.getByText(/Structure:/).parentElement).toHaveTextContent('Scalar columns');
    expect(screen.getByText(/Lossless:/).parentElement).toHaveTextContent('No');
  });
});
