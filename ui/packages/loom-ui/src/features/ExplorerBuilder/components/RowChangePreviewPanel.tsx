import React from 'react';
import type { ExplorerBuilderPreviewResult } from '../../../types';
import { ConstructionProposalPreview } from '../constructionWorkspace/ConstructionProposalPreview';

export const RowChangePreviewPanel = ({
  candidateRoot,
  preview,
  status,
  error,
  disabled,
  onApply,
  onCancel,
}: {
  readonly candidateRoot: string;
  readonly preview?: ExplorerBuilderPreviewResult;
  readonly status: 'loading' | 'ready' | 'error';
  readonly error?: string;
  readonly disabled: boolean;
  readonly onApply: () => void;
  readonly onCancel: () => void;
}) => (
  <section
    aria-label="Review proposed rows"
    data-testid="row-change-preview-panel"
    className="rounded-xl border border-blue-200 bg-white p-4 shadow-sm"
  >
    <h2 className="text-base font-semibold text-slate-950">One row per {candidateRoot}</h2>
    <p className="mt-1 text-sm text-slate-600">
      Review the rows and existing columns before changing this table.
    </p>
    {status === 'loading' ? (
      <p role="status" className="mt-3 text-sm text-slate-600">Loading proposed rows…</p>
    ) : null}
    {status === 'error' ? (
      <p role="alert" className="mt-3 text-sm text-red-800">{error}</p>
    ) : null}
    {status === 'ready' && preview ? (
      <div className="mt-3 max-h-80 overflow-auto rounded-md border border-slate-200">
        <ConstructionProposalPreview
          preview={preview}
          partialValidationMessage="Only displayed rows were checked in this preview."
        />
      </div>
    ) : null}
    <div className="mt-3 flex gap-2">
      <button
        type="button"
        disabled={status !== 'ready' || !preview || disabled}
        onClick={onApply}
        className="rounded-md bg-blue-700 px-3 py-2 text-sm font-semibold text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-50"
      >
        Apply row change
      </button>
      <button
        type="button"
        disabled={disabled}
        onClick={onCancel}
        className="rounded-md border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50"
      >
        Keep current rows
      </button>
    </div>
  </section>
);
