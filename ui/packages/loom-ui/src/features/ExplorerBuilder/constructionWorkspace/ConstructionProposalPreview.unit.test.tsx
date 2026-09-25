// @vitest-environment jsdom

import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ExplorerBuilderPreviewResult } from '../../../types';
import { ConstructionProposalPreview } from './ConstructionProposalPreview';

const preview = (sampled: boolean): ExplorerBuilderPreviewResult => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderPreview',
  receiptId: 'receipt-1',
  outputId: 'patients',
  columns: [{ column: 'id', label: 'ID', logicalType: 'string', filterable: true, chartable: false }],
  rows: [{ id: 'patient-1' }],
  rowCount: 1,
  sampled,
  diagnostics: [],
});

describe('ConstructionProposalPreview', () => {
  it('does not describe a bounded preview as the full table', () => {
    render(<ConstructionProposalPreview preview={preview(true)} />);
    expect(screen.getByText(/Full-output coverage is unavailable before publication/)).toBeInTheDocument();
  });

  it('identifies a preview that exhausted the output', () => {
    render(<ConstructionProposalPreview preview={preview(false)} />);
    expect(screen.getByText('Showing all 1 rows in this proposal.')).toBeInTheDocument();
  });
});
