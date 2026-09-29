import React, { useEffect, useMemo, useState } from 'react';
import type { LoomClient } from '../../../api';
import type {
  Construction,
  ConstructionProposalRequest,
  ConstructionStep,
  FrameSourceOption,
  SemanticInventoryItem,
} from '../../../types';
import { constructionSchema } from '../../../types';
import { semanticConceptLabel } from '../catalogItems';

type CandidateIntent = Pick<ConstructionProposalRequest, 'candidateConstruction' | 'changedStepId'>;
type Category = {
  readonly key: string;
  readonly system: string;
  readonly code: string;
  readonly choiceId?: string;
  readonly outputColumnId: string;
  readonly name: string;
  readonly label: string;
};

const categoryKey = (system: string, code: string) => `${system}\u0000${code}`;
const newId = (prefix: string) => `${prefix}_${globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2)}`;
const columnName = (label: string, used: ReadonlySet<string>) => {
  const base = label.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'coded_value';
  let name = base;
  for (let suffix = 2; used.has(name); suffix += 1) name = `${base}_${suffix}`;
  return name;
};

export const CodedPivotEditor = ({
  client,
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  rowRoot,
  construction,
  editingStep,
  disabled,
  onCandidateChange,
}: {
  readonly client: Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly construction: Construction;
  readonly editingStep?: ConstructionStep;
  readonly disabled: boolean;
  readonly onCandidateChange: (intent: CandidateIntent | undefined) => void;
}) => {
  const saved = editingStep?.operation.kind === 'CODED_PIVOT' ? editingStep.operation.codedPivot : undefined;
  const [stepId] = useState(() => editingStep?.id ?? newId('coded-pivot'));
  const [sourceQuery, setSourceQuery] = useState('');
  const [sourceCursor, setSourceCursor] = useState<string>();
  const [sourceNextCursor, setSourceNextCursor] = useState<string>();
  const [sources, setSources] = useState<ReadonlyArray<FrameSourceOption>>([]);
  const [sourcesLoading, setSourcesLoading] = useState(true);
  const [sourceError, setSourceError] = useState<string>();
  const [source, setSource] = useState<FrameSourceOption>();
  const [categoryQuery, setCategoryQuery] = useState('');
  const [categoryCursor, setCategoryCursor] = useState<string>();
  const [categoryNextCursor, setCategoryNextCursor] = useState<string>();
  const [inventory, setInventory] = useState<ReadonlyArray<SemanticInventoryItem>>([]);
  const [categoriesLoading, setCategoriesLoading] = useState(false);
  const [categoryError, setCategoryError] = useState<string>();
  const [selected, setSelected] = useState<ReadonlyArray<Category>>(() => saved?.categories.map((category) => {
    const output = editingStep?.outputs.find((column) => column.id === category.outputColumnId);
    const system = category.system ?? '';
    const code = category.code ?? '';
    return {
      key: categoryKey(system, code), system, code,
      outputColumnId: category.outputColumnId,
      name: output?.name ?? code,
      label: output?.label ?? code,
    };
  }) ?? []);
  const [duplicatePolicy, setDuplicatePolicy] = useState<'ERROR' | 'SUM' | 'MIN' | 'MAX'>(saved?.duplicatePolicy ?? 'ERROR');
  const [missingCellPolicy, setMissingCellPolicy] = useState<'NULL' | 'ERROR'>(saved?.missingCellPolicy ?? 'NULL');

  useEffect(() => {
    const controller = new AbortController();
    setSourcesLoading(true);
    setSourceError(undefined);
    void client.browseFrameSourceOptions({
      project, explorerId, authResourcePath, snapshotToken, outputId, resourceType: rowRoot,
      ...(sourceQuery ? { query: sourceQuery } : {}),
      ...(sourceCursor ? { cursor: sourceCursor } : {}), limit: 50,
    }, controller.signal).then((response) => {
      if (controller.signal.aborted) return;
      const direct = response.sources.filter((option) => option.route.length === 0 && option.resourceType === rowRoot);
      setSources((current) => sourceCursor ? [...current, ...direct] : direct);
      setSourceNextCursor(response.nextCursor);
      setSource((current) => current
        ?? direct.find((option) => saved?.source?.family.bindingId === option.bindingId && saved.source.family.sourcePath === option.sourcePath)
        ?? (direct.length === 1 ? direct[0] : undefined));
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setSourceError(error instanceof Error ? error.message : 'Coded sources could not be loaded.');
    }).finally(() => { if (!controller.signal.aborted) setSourcesLoading(false); });
    return () => controller.abort();
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot, sourceQuery, sourceCursor]);

  useEffect(() => {
    if (!source) { setInventory([]); return; }
    const controller = new AbortController();
    setCategoriesLoading(true);
    setCategoryError(undefined);
    void client.browseSemanticInventory({
      project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
      sourceChoiceId: source.choiceId,
      ...(categoryQuery ? { query: categoryQuery } : {}),
      ...(categoryCursor ? { cursor: categoryCursor } : {}), limit: 50,
    }, controller.signal).then((response) => {
      if (!controller.signal.aborted) {
        setInventory((current) => categoryCursor ? [...current, ...response.entries] : response.entries);
        setCategoryNextCursor(response.nextCursor);
      }
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setCategoryError(error instanceof Error ? error.message : 'Coded values could not be loaded.');
    }).finally(() => { if (!controller.signal.aborted) setCategoriesLoading(false); });
    return () => controller.abort();
  }, [client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot, source?.choiceId, categoryQuery, categoryCursor]);

  const available = useMemo(() => inventory.filter((item) =>
    item.code && !item.codingVersion && item.constructionChoice &&
    (item.readiness.status === 'READY' || item.readiness.status === 'READY_WITH_WARNING'),
  ), [inventory]);
  const visibleSources = source && !sources.some((option) => option.choiceId === source.choiceId)
    ? [source, ...sources]
    : sources;

  useEffect(() => {
    if (!source || selected.length === 0 || disabled) { onCandidateChange(undefined); return; }
    const step: ConstructionStep = {
      id: stepId,
      inputs: editingStep?.inputs ?? [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'CODED_PIVOT',
        codedPivot: {
          constructionId: stepId,
          sourceChoiceId: source.choiceId,
          categories: selected.map((category) => category.choiceId
            ? { choiceId: category.choiceId, outputColumnId: category.outputColumnId }
            : { system: category.system, code: category.code, outputColumnId: category.outputColumnId }),
          duplicatePolicy,
          missingCellPolicy,
        },
      },
      outputs: selected.map((category) => ({
        id: category.outputColumnId, name: category.name, label: category.label, type: 'INFER',
      })),
    };
    const draft = constructionSchema.safeParse({
      ...construction,
      steps: editingStep
        ? construction.steps.map((candidate) => candidate.id === editingStep.id ? step : candidate)
        : [...construction.steps, step],
    });
    onCandidateChange(draft.success ? { candidateConstruction: draft.data, changedStepId: stepId } : undefined);
  }, [source?.choiceId, selected, duplicatePolicy, missingCellPolicy, disabled, construction, editingStep, stepId, onCandidateChange]);

  const chooseSource = (choiceId: string) => {
    const next = sources.find((option) => option.choiceId === choiceId);
    if (!next) return;
    setSource(next);
    if (next.choiceId !== source?.choiceId) setSelected([]);
    setCategoryQuery('');
    setCategoryCursor(undefined);
    setCategoryNextCursor(undefined);
    setInventory([]);
  };
  const toggleCategory = (item: SemanticInventoryItem) => {
    const key = categoryKey(item.system, item.code);
    if (selected.some((category) => category.key === key)) {
      setSelected((current) => current.filter((category) => category.key !== key));
      return;
    }
    if (!item.constructionChoice || selected.length >= 50) return;
    const label = semanticConceptLabel(item);
    const name = columnName(label, new Set(selected.map((category) => category.name)));
    setSelected((current) => [...current, {
      key, system: item.system, code: item.code, choiceId: item.constructionChoice!.choiceId,
      outputColumnId: newId('coded-column'), name, label,
    }]);
  };

  return (
    <section aria-label="Coded values as columns" className="space-y-4 rounded-lg border border-slate-200 bg-white p-4">
      <div>
        <h3 className="text-base font-semibold text-slate-950">Coded values as columns</h3>
        <p className="text-sm text-slate-600">Keep one row per {rowRoot} record. Each selected code becomes a column filled by its paired value.</p>
      </div>
      <label className="block text-sm font-medium text-slate-800">Find a coded source
        <input type="search" value={sourceQuery} onChange={(event) => {
          setSourceQuery(event.target.value);
          setSourceCursor(undefined);
          setSourceNextCursor(undefined);
        }}
          disabled={disabled} placeholder="Search sources" className="mt-1 block w-full rounded border border-slate-300 px-3 py-2" />
      </label>
      {sourcesLoading ? <p role="status" className="text-sm text-slate-600">Finding coded sources…</p> : null}
      {sourceError ? <p role="alert" className="text-sm text-red-700">{sourceError}</p> : null}
      {!sourcesLoading && !sourceError && visibleSources.length === 0 ? <p className="text-sm text-slate-600">{
        sourceNextCursor ? 'No direct coded source appears on this page. Browse more sources or search by name.'
          : sourceQuery ? 'No direct coded source matches this search.'
            : 'No direct coded source is available for these rows.'
      }</p> : null}
      {visibleSources.length > 0 ? <fieldset className="grid max-h-60 gap-2 overflow-y-auto sm:grid-cols-2">
        <legend className="mb-1 text-sm font-medium text-slate-800">Source of coded values</legend>
        {visibleSources.map((option) => <label key={option.choiceId} className="flex gap-2 rounded border border-slate-200 p-2 text-sm hover:bg-slate-50">
          <input type="radio" name="coded-pivot-source" checked={source?.choiceId === option.choiceId}
            onChange={() => chooseSource(option.choiceId)} disabled={disabled} />
          <span><strong className="block">{option.title}</strong><span className="text-xs text-slate-600">{option.description}</span></span>
        </label>)}
      </fieldset> : null}
      {sourceNextCursor ? <button type="button" disabled={sourcesLoading} onClick={() => setSourceCursor(sourceNextCursor)}
        className="rounded border border-slate-300 px-2 py-1 text-sm text-blue-800 disabled:opacity-50">More coded sources</button> : null}
      {source ? <>
        <label className="block text-sm font-medium text-slate-800">Find values to turn into columns
          <input type="search" value={categoryQuery} onChange={(event) => {
            setCategoryQuery(event.target.value);
            setCategoryCursor(undefined);
            setCategoryNextCursor(undefined);
          }}
            disabled={disabled} placeholder="Search coded values" className="mt-1 block w-full rounded border border-slate-300 px-3 py-2" />
        </label>
        {categoriesLoading ? <p role="status" className="text-sm text-slate-600">Finding coded values…</p> : null}
        {categoryError ? <p role="alert" className="text-sm text-red-700">{categoryError}</p> : null}
        <div className="grid max-h-72 gap-1 overflow-y-auto sm:grid-cols-2">
          {inventory.map((item) => {
            const key = categoryKey(item.system, item.code);
            const canSelect = available.includes(item);
            return <label key={key} className={`flex items-start gap-2 rounded border border-slate-200 px-2 py-1.5 text-sm ${canSelect ? 'hover:bg-slate-50' : 'opacity-60'}`}>
              <input type="checkbox" checked={selected.some((category) => category.key === key)}
                disabled={disabled || !canSelect || selected.length >= 50 && !selected.some((category) => category.key === key)}
                onChange={() => toggleCategory(item)} />
              <span><strong className="block">{semanticConceptLabel(item)}</strong>
                <span className="text-xs text-slate-500">{canSelect
                  ? `${item.occurrences.toLocaleString()} observed source occurrences`
                  : item.codingVersion ? 'Versioned codes are not supported in this row operation.' : item.readiness.message}</span></span>
            </label>;
          })}
        </div>
        {categoryNextCursor ? <button type="button" disabled={categoriesLoading} onClick={() => setCategoryCursor(categoryNextCursor)}
          className="rounded border border-slate-300 px-2 py-1 text-sm text-blue-800 disabled:opacity-50">More coded values</button> : null}
        {!categoriesLoading && !categoryError && inventory.length === 0 ? <p className="text-sm text-slate-600">No coded values match this search.</p> : null}
        <p className="text-sm font-medium text-slate-800">{selected.length} of 50 columns selected</p>
        {selected.length > 0 ? <ul className="flex flex-wrap gap-1">{selected.map((category) =>
          <li key={category.outputColumnId} title={`${category.system} · ${category.code}`}
            className="rounded bg-emerald-50 px-2 py-1 text-xs text-emerald-950">{category.label}</li>)}</ul> : null}
        <details className="text-sm text-slate-700"><summary className="cursor-pointer font-medium">Advanced value handling</summary>
          <div className="mt-2 grid gap-3 sm:grid-cols-2">
            <label>When several values match
              <select value={duplicatePolicy} onChange={(event) => setDuplicatePolicy(event.target.value as typeof duplicatePolicy)}
                disabled={disabled} className="mt-1 block w-full rounded border border-slate-300 px-2 py-1.5">
                <option value="ERROR">Stop if several values match</option>
                {['integer', 'decimal', 'number'].includes(source.logicalType.toLowerCase()) ? <>
                  <option value="SUM">Add values</option><option value="MIN">Use smallest</option><option value="MAX">Use largest</option>
                </> : null}
              </select>
            </label>
            <label>When a record has no value
              <select value={missingCellPolicy} onChange={(event) => setMissingCellPolicy(event.target.value as typeof missingCellPolicy)}
                disabled={disabled} className="mt-1 block w-full rounded border border-slate-300 px-2 py-1.5">
                <option value="NULL">Leave the cell empty</option><option value="ERROR">Stop when a record has no value</option>
              </select>
            </label>
          </div>
        </details>
      </> : null}
    </section>
  );
};
