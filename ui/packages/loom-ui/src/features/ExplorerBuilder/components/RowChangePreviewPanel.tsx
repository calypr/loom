import React from 'react';
import type { ExplorerBuilderPreviewResult } from '../../../types';
import {
  ConstructionProposalPreview,
  rowCountImpactSummary,
} from '../constructionWorkspace/ConstructionProposalPreview';

const PARTIAL_VALIDATION_MESSAGE = 'Only displayed rows were checked in this preview.';

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
}) => {
  // Only a ready preview has an authoritative count, so loading and error carry none.
  const impact = status === 'ready' && preview
    ? rowCountImpactSummary(preview, `${candidateRoot} row root`, PARTIAL_VALIDATION_MESSAGE)
    : '';
  return (
    <section
      aria-label="Review proposed rows"
      data-testid="row-change-preview-panel"
      className="rounded-xl border border-blue-200 bg-white p-4 shadow-sm"
    >
      <h2 className="text-base font-semibold text-slate-950">One row per {candidateRoot}</h2>
      <p className="mt-1 text-sm text-slate-600">
        Review the rows and existing columns before changing this table.
      </p>
      {/* AT only announces mutations to an already-mounted live region, so this node persists
          across status changes instead of mounting with each proposal. It holds no text until
          the current preview is ready, so an earlier root change's count can never persist. */}
      <p
        role="status"
        aria-live="polite"
        data-testid="row-change-row-count-impact"
        className={impact ? 'mt-3 rounded-md bg-slate-50 px-3 py-2 text-sm font-medium text-slate-800' : 'sr-only'}
      >
        {impact}
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
            partialValidationMessage={PARTIAL_VALIDATION_MESSAGE}
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
};
