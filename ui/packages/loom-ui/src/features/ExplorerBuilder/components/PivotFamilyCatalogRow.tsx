import React, { useState } from 'react';
import type { CatalogChoiceIntent } from '../catalogItems';
import type { PivotFamily } from '../pivotFamilies';

const MAX_PIVOT_COLUMNS = 100;

export type PivotAnalysis = {
  readonly commandId: string;
  readonly draftDigest: string;
  readonly sampledRows: number;
  readonly rowsWithMultipleCodes: number;
  readonly columns: ReadonlyArray<{
    readonly code: string;
    readonly label: string;
    readonly rowsWithValue: number;
  }>;
};

export const PivotFamilyCatalogRow = ({
  family,
  disabled,
  queuedFeatureIds,
  onAnalyzeSelected,
  onAddSelected,
}: {
  readonly family: PivotFamily;
  readonly disabled: boolean;
  readonly queuedFeatureIds: ReadonlySet<string>;
  readonly onAnalyzeSelected: (familyId: string, selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<PivotAnalysis>;
  readonly onAddSelected: (selections: ReadonlyArray<CatalogChoiceIntent>, commandId?: string, expectedDraftDigest?: string) => Promise<void>;
}) => {
  const [expanded, setExpanded] = useState(false);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const [analysis, setAnalysis] = useState<PivotAnalysis>();
  const panelId = `code-set-${family.id}`;
  const exampleNames = family.codes.slice(0, 2).map((code) => code.item.title).join(' + ');
  const remainingNames = family.codes.length > 2 ? ` + ${family.codes.length - 2} more` : '';

  const selectedChoices = family.codes.flatMap((code): ReadonlyArray<CatalogChoiceIntent> => {
    const choice = code.item.constructionChoice;
    if (code.configured || queuedFeatureIds.has(code.item.featureId) || !selected.has(code.item.featureId) || !choice) return [];
    return [{
      constructionChoice: { choiceId: choice.choiceId, form: code.form },
      title: code.item.title,
    }];
  });

  const analyzeFamily = async () => {
    if (selectedChoices.length === 0) return;
    setBusy(true);
    setMessage(undefined);
    try {
      setAnalysis(await onAnalyzeSelected(family.id, selectedChoices));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Loom could not analyze these table rows.');
    } finally {
      setBusy(false);
    }
  };

  const addFamily = async () => {
    if (!analysis) return;
    setBusy(true);
    setMessage(undefined);
    try {
      await onAddSelected(selectedChoices, analysis.commandId, analysis.draftDigest);
      setAnalysis(undefined);
      setSelected(new Set());
      setMessage(`${analysis.columns.length} columns were added. Preview the table to inspect their values.`);
    } catch (error) {
      setAnalysis(undefined);
      setMessage(error instanceof Error ? error.message : 'Loom could not add these pivot columns.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <article role="group" aria-label={`Code set ${family.title}: ${exampleNames}${remainingNames}`} data-testid="pivot-family-catalog-row" className="rounded-md border border-blue-200 bg-blue-50/40 px-3 py-2.5">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <h4 className="text-sm font-semibold leading-5 text-slate-900">{exampleNames}{remainingNames}</h4>
          <p className="mt-0.5 text-xs text-slate-600">
            {family.title} · {family.codes.length.toLocaleString()} code values
          </p>
          {family.codes.some((code) => code.configured) ? (
            <p className="mt-1 text-xs text-blue-800">
              {family.codes.filter((code) => code.configured).length} already added. Edit these columns in Preview.
            </p>
          ) : null}
        </div>
        <button
          type="button"
          aria-expanded={expanded}
          aria-controls={panelId}
          disabled={disabled || busy}
          onClick={() => setExpanded((current) => !current)}
          className="min-h-9 shrink-0 rounded-md border border-blue-300 bg-white px-3 py-1.5 text-xs font-semibold text-blue-800 hover:bg-blue-50 disabled:opacity-50"
        >
          {expanded ? 'Hide codes' : 'Choose codes'}
        </button>
      </div>
      <details className="mt-1 text-[11px] text-slate-600">
        <summary className="min-h-6 cursor-pointer font-medium text-blue-700">Technical source and route</summary>
        <dl className="mt-2 grid gap-1 rounded-md bg-white p-2">
          <div><dt className="inline font-medium">Relationship: </dt><dd className="inline">{family.relationship}</dd></div>
          {family.sourceRecords === undefined ? null : (
            <div><dt className="inline font-medium">Source code matches: </dt><dd className="inline">{family.sourceRecords.toLocaleString()} (not table-row coverage)</dd></div>
          )}
          {family.codes[0]?.item.constructionChoice?.presentation.facts.map((fact) => (
            <div key={`${fact.label}:${fact.value}`}><dt className="inline font-medium">{fact.label}: </dt><dd className="inline break-all">{fact.value}</dd></div>
          ))}
          {family.codes[0]?.item.constructionChoice?.route.map((step, index) => (
            <div key={`${index}:${step.edgeId}`}><dt className="inline font-medium">Connection {index + 1}: </dt><dd className="inline break-all">{step.fromResourceType} → {step.toResourceType} via {step.relationship}</dd></div>
          ))}
          {family.codes.map((code) => (
            <div key={code.item.featureId}><dt className="inline font-medium">{code.item.title} code: </dt><dd className="inline break-all">{code.code}</dd></div>
          ))}
        </dl>
      </details>
      {expanded ? (
        <div id={panelId} className="mt-2 border-t border-blue-100 pt-2">
          <p className="text-xs text-slate-600">Choose the code values to add. Check row coverage before adding columns.</p>
          <div className="mt-2 max-h-52 space-y-1 overflow-y-auto">
            {family.codes.map((code) => (
              <label key={code.item.featureId} className="flex items-start gap-2 rounded px-1 py-1 text-xs hover:bg-white">
                <input
                  type="checkbox"
                  aria-label={`Select ${code.item.title} as a column`}
                  checked={selected.has(code.item.featureId)}
                  disabled={busy || disabled || code.configured || queuedFeatureIds.has(code.item.featureId) || (!selected.has(code.item.featureId) && selected.size >= MAX_PIVOT_COLUMNS)}
                  onChange={() => {
                    setSelected((current) => {
                      const next = new Set(current);
                      if (next.has(code.item.featureId)) next.delete(code.item.featureId);
                      else next.add(code.item.featureId);
                      return next;
                    });
                    setAnalysis(undefined);
                    setMessage(undefined);
                  }}
                  className="mt-0.5 h-4 w-4 rounded border-slate-300 text-blue-700"
                />
                <span className="min-w-0 break-words text-slate-800">
                  {code.item.title}
                  {code.configured ? <span className="ml-1 font-semibold text-blue-800">Added</span> : null}
                  {queuedFeatureIds.has(code.item.featureId) ? <span className="ml-1 font-semibold text-blue-800">Selected</span> : null}
                </span>
              </label>
            ))}
          </div>
          {family.codes.length > MAX_PIVOT_COLUMNS ? (
            <p className="mt-1 text-xs text-slate-600">Select up to {MAX_PIVOT_COLUMNS} columns per operation.</p>
          ) : null}
          {analysis ? (
            <div data-testid="pivot-analysis" className="mt-3 rounded-md border border-blue-200 bg-white p-2 text-xs text-slate-800">
              <p className="font-semibold">{analysis.sampledRows} preview rows checked</p>
              {analysis.columns.length > 1 ? (
                <p>{analysis.rowsWithMultipleCodes} of {analysis.sampledRows} checked rows have more than one selected value.</p>
              ) : null}
              <ul className="mt-1 space-y-0.5">
                {analysis.columns.map((column) => (
                  <li key={column.code}>{column.label}: {column.rowsWithValue} of {analysis.sampledRows} checked rows have a value</li>
                ))}
              </ul>
              <p className="mt-1 text-amber-800">Coverage reflects this preview sample, not the whole table.</p>
            </div>
          ) : null}
          <div className="mt-2 flex flex-wrap gap-2">
            <button
              type="button"
              disabled={busy || disabled || selectedChoices.length === 0}
              onClick={() => void analyzeFamily()}
              className="min-h-9 rounded-md border border-blue-300 bg-white px-3 py-1.5 text-xs font-semibold text-blue-800 hover:bg-blue-50 disabled:opacity-50"
            >
              Check row coverage
            </button>
            <button
              type="button"
              disabled={busy || disabled || !analysis || selectedChoices.length === 0}
              onClick={() => void addFamily()}
              className="min-h-9 rounded-md bg-blue-700 px-3 py-1.5 text-xs font-semibold text-white hover:bg-blue-800 disabled:opacity-50"
            >
              Add {selectedChoices.length} {selectedChoices.length === 1 ? 'column' : 'columns'}
            </button>
          </div>
        </div>
      ) : null}
      {message ? <p className="mt-2 text-xs text-slate-700" role="status">{message}</p> : null}
    </article>
  );
};
