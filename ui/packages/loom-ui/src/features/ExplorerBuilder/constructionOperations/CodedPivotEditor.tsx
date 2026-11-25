import { fieldPickerList, fieldPickerToggleRow } from '../components/fieldPickerStyles';
import React, { useId, useMemo, useRef, useState } from 'react';
import { isAvailableCodedValue, useCodedPivotCategories, useCodedPivotSources, type CodedPivotTransport } from '../session/codedPivotQueries';
import type {
  Construction,
  ConstructionProposalRequest,
  ConstructionStep,
  FrameSourceOption,
  SemanticInventoryItem,
} from '../../../contracts/types';
import { constructionWithStep, createOpaqueId } from '../model/editorShared';
import { semanticConceptLabel } from '../model/catalogItems';

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
type CodedPivotEditorProps = {
  readonly client: CodedPivotTransport;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly construction: Construction;
  readonly editingStep?: ConstructionStep;
  readonly disabled: boolean;
  readonly unavailableReason?: string;
  readonly onCandidateChange: (intent: CandidateIntent | undefined) => void;
};

const categoryKey = (system: string, code: string) => `${system}\u0000${code}`;
const selectedCategoryContext = (category: Category, categories: ReadonlyArray<Category>) => {
  const sameLabel = categories.filter((candidate) =>
    candidate.outputColumnId !== category.outputColumnId && candidate.label === category.label);
  return {
    showCode: sameLabel.length > 0,
    showSystem: sameLabel.some((candidate) => candidate.code === category.code && candidate.system !== category.system),
  };
};
const columnName = (label: string, used: ReadonlySet<string>) => {
  const base = label.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'coded_value';
  let name = base;
  for (let suffix = 2; used.has(name); suffix += 1) name = `${base}_${suffix}`;
  return name;
};

const CodedPivotEditorBody = ({
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
  unavailableReason,
  onCandidateChange,
}: CodedPivotEditorProps) => {
  const saved = editingStep?.operation.kind === 'CODED_PIVOT' ? editingStep.operation.codedPivot : undefined;
  const [stepId] = useState(() => editingStep?.id ?? createOpaqueId('coded-pivot'));
  const sourceDetailsId = useId();
  const [source, setSource] = useState<FrameSourceOption>();
  const [categoryQuery, setCategoryQuery] = useState('');
  const [categoryCursor, setCategoryCursor] = useState<string>();
  const [categorySeed, setCategorySeed] = useState<ReadonlyArray<SemanticInventoryItem>>([]);
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
  const sourceRef = useRef(source);
  sourceRef.current = source;
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const duplicatePolicyRef = useRef(duplicatePolicy);
  duplicatePolicyRef.current = duplicatePolicy;
  const missingCellPolicyRef = useRef(missingCellPolicy);
  missingCellPolicyRef.current = missingCellPolicy;

  const emitCandidate = (
    nextSource: FrameSourceOption | undefined,
    nextSelected: ReadonlyArray<Category>,
    nextDuplicatePolicy = duplicatePolicyRef.current,
    nextMissingCellPolicy = missingCellPolicyRef.current,
  ) => {
    if (!nextSource || nextSelected.length === 0 || disabled) {
      onCandidateChange(undefined);
      return;
    }
    const step: ConstructionStep = {
      id: stepId,
      inputs: editingStep?.inputs ?? [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'CODED_PIVOT',
        codedPivot: {
          constructionId: stepId,
          sourceChoiceId: nextSource.choiceId,
          categories: nextSelected.map((category) => category.choiceId
            ? { choiceId: category.choiceId, outputColumnId: category.outputColumnId }
            : { system: category.system, code: category.code, outputColumnId: category.outputColumnId }),
          duplicatePolicy: nextDuplicatePolicy,
          missingCellPolicy: nextMissingCellPolicy,
        },
      },
      outputs: nextSelected.map((category) => ({
        id: category.outputColumnId, name: category.name, label: category.label, type: 'INFER',
      })),
    };
    const candidateConstruction = constructionWithStep({ construction, editingStep, step });
    onCandidateChange(candidateConstruction ? { candidateConstruction, changedStepId: stepId } : undefined);
  };

  const { sources, sourceQuery, sourceNextCursor, setSourceCursor, sourcesLoading, sourceError, searchSources } = useCodedPivotSources({
    client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
    onSources(direct) {
      const currentSource = sourceRef.current;
      const nextSource = currentSource
        ?? direct.find((option) => saved?.source?.family.bindingId === option.bindingId && saved.source.family.sourcePath === option.sourcePath)
        ?? (direct.length === 1 ? direct[0] : undefined);
      if (nextSource !== currentSource) {
        setSource(nextSource);
        emitCandidate(nextSource, selectedRef.current);
      }
    },
  });
  const { inventory, inventoryState, inventorySourceAvailability, categoryNextCursor, categoriesLoading, categoryError } = useCodedPivotCategories({
    client, project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
    categorySourceChoiceId: source?.choiceId, categoryQuery, categoryCursor, categorySeed,
  });
  const available = useMemo(() => inventory.filter((item) =>
    isAvailableCodedValue(item),
  ), [inventory]);
  const inventoryCategoryContexts = useMemo(() => {
    const itemsByLabel = new Map<string, SemanticInventoryItem[]>();
    for (const item of inventory) {
      const label = semanticConceptLabel(item);
      const items = itemsByLabel.get(label) ?? [];
      items.push(item);
      itemsByLabel.set(label, items);
    }
    const contexts = new Map<string, { readonly showCode: boolean; readonly showSystem: boolean }>();
    for (const items of itemsByLabel.values()) {
      const systemsByCode = new Map<string, Set<string>>();
      for (const item of items) {
        const systems = systemsByCode.get(item.code) ?? new Set<string>();
        systems.add(item.system);
        systemsByCode.set(item.code, systems);
      }
      for (const item of items) {
        contexts.set(categoryKey(item.system, item.code), {
          showCode: items.length > 1,
          showSystem: (systemsByCode.get(item.code)?.size ?? 0) > 1,
        });
      }
    }
    return contexts;
  }, [inventory]);
  const displayedInventory = useMemo(() => [...inventory].sort((left, right) =>
    Number(isAvailableCodedValue(right)) - Number(isAvailableCodedValue(left))), [inventory]);
  const visibleSources = source && !sources.some((option) => option.choiceId === source.choiceId)
    ? [source, ...sources]
    : sources;

  const chooseSource = (choiceId: string) => {
    const next = sources.find((option) => option.choiceId === choiceId);
    if (!next) return;
    const nextSelected = next.choiceId !== source?.choiceId ? [] : selected;
    setSource(next);
    setSelected(nextSelected);
    setCategoryQuery('');
    setCategoryCursor(undefined);
    setCategorySeed([]);
    emitCandidate(next, nextSelected);
  };
  const toggleCategory = (item: SemanticInventoryItem) => {
    const key = categoryKey(item.system, item.code);
    if (selected.some((category) => category.key === key)) {
      const nextSelected = selected.filter((category) => category.key !== key);
      setSelected(nextSelected);
      emitCandidate(source, nextSelected);
      return;
    }
    if (!item.constructionChoice || selected.length >= 50) return;
    const label = semanticConceptLabel(item);
    const name = columnName(label, new Set(selected.map((category) => category.name)));
    const nextSelected = [...selected, {
      key, system: item.system, code: item.code, choiceId: item.constructionChoice!.choiceId,
      outputColumnId: createOpaqueId('coded-column'), name, label,
    }];
    setSelected(nextSelected);
    emitCandidate(source, nextSelected);
  };

  return (
    <section aria-label="Pivot coded values into columns" className="space-y-4 rounded-lg border border-slate-200 bg-white p-4">
      {unavailableReason ? <p role="status" className="m-0 rounded bg-amber-50 px-3 py-2 text-sm text-amber-950">{unavailableReason}</p> : null}
      <p className="m-0 text-sm text-slate-600">Keep one row per {rowRoot} record. Each selected code becomes a column filled by its paired value.</p>
      <label className="block text-sm font-medium text-slate-800">Find a coded source
        <input type="search" value={sourceQuery} onChange={(event) => searchSources(event.target.value)}
          disabled={disabled} placeholder="Search sources" className="mt-1 block w-full rounded border border-slate-300 px-3 py-2" />
      </label>
      {sourcesLoading ? <p role="status" className="m-0 text-sm text-slate-600">Finding coded sources…</p> : null}
      {sourceError ? <p role="alert" className="m-0 text-sm text-red-700">{sourceError}</p> : null}
      {!sourcesLoading && !sourceError && visibleSources.length === 0 ? <p className="m-0 text-sm text-slate-600">{
          sourceNextCursor ? 'No direct coded source appears on this page. Browse more sources or search by name.'
          : sourceQuery ? 'No direct coded source matches this search.'
            : 'No direct coded source is available on these starting records. Use category and value fields already in this table, or add a coded-value column from a related record and then reopen Categories to columns.'
      }</p> : null}
      {visibleSources.length > 0 ? <fieldset className={fieldPickerList}>
        <legend className="mb-1 text-sm font-medium text-slate-800">Source of coded values</legend>
        {visibleSources.map((option, index) => {
          const detailsId = `${sourceDetailsId}-source-${index}`;
          return (
            <div key={option.choiceId} className="border-b border-slate-100 last:border-b-0">
              <label className={fieldPickerToggleRow}>
                <input type="radio" name="coded-pivot-source" checked={source?.choiceId === option.choiceId}
                  onChange={() => chooseSource(option.choiceId)} disabled={disabled} aria-describedby={detailsId} />
                <span>
                  <strong className="block">{option.title}</strong>
                  <span className="block text-xs text-slate-600">{option.description}</span>
                </span>
              </label>
              <span id={detailsId} className="block pl-6 text-xs leading-tight text-slate-500">
                Value type {option.logicalType} · example {option.exampleConcept} · {option.observedOccurrences.toLocaleString()} observed source occurrences
              </span>
            </div>
          );
        })}
      </fieldset> : null}
      {sourceNextCursor ? <button type="button" disabled={sourcesLoading} onClick={() => setSourceCursor(sourceNextCursor)}
        className="rounded border border-slate-300 px-2 py-1 text-sm text-blue-800 disabled:opacity-50">More coded sources</button> : null}
      {source ? <>
        <label className="block text-sm font-medium text-slate-800">Find values to turn into columns
          <input type="search" value={categoryQuery} onChange={(event) => {
            setCategoryQuery(event.target.value);
            setCategoryCursor(undefined);
            setCategorySeed([]);
          }}
            disabled={disabled} placeholder="Search coded values" className="mt-1 block w-full rounded border border-slate-300 px-3 py-2" />
        </label>
        {categoriesLoading ? <p role="status" className="m-0 text-sm text-slate-600">Finding coded values…</p> : null}
        {categoryError ? <p role="alert" className="m-0 text-sm text-red-700">{categoryError}</p> : null}
        <div className="grid max-h-72 gap-1 overflow-y-auto sm:grid-cols-2">
          {displayedInventory.map((item) => {
            const key = categoryKey(item.system, item.code);
            const canSelect = available.includes(item);
            const context = inventoryCategoryContexts.get(key);
            return <label key={key} className={`flex items-start gap-2 rounded border border-slate-200 px-2 py-1.5 text-sm ${canSelect ? 'hover:bg-slate-50' : 'opacity-60'}`}>
              <input type="checkbox" checked={selected.some((category) => category.key === key)}
                disabled={disabled || !canSelect || selected.length >= 50 && !selected.some((category) => category.key === key)}
                onChange={() => toggleCategory(item)} />
              <span>
                <strong className="block">{semanticConceptLabel(item)}</strong>
                {context?.showSystem ? <span className="block break-all text-xs leading-tight text-slate-500">
                  System {item.system || 'not specified'} · Code {item.code || 'not specified'}
                </span> : context?.showCode ? <span className="block break-all text-xs leading-tight text-slate-500">
                  Code {item.code || 'not specified'}
                </span> : null}
                <span className="text-xs text-slate-500">{canSelect
                  ? `${item.occurrences.toLocaleString()} observed source occurrences`
                  : item.codingVersion ? 'Versioned codes are not supported in this row operation.' : item.readiness.message}</span></span>
            </label>;
          })}
        </div>
        {categoryNextCursor ? <button type="button" disabled={categoriesLoading} onClick={() => {
          setCategorySeed(inventory);
          setCategoryCursor(categoryNextCursor);
        }}
          className="rounded border border-slate-300 px-2 py-1 text-sm text-blue-800 disabled:opacity-50">More coded values</button> : null}
        {!categoriesLoading && !categoryError && inventory.length === 0 ? <p role={inventoryState !== 'complete' || inventorySourceAvailability !== 'verified' ? 'status' : undefined}
          className="m-0 text-sm text-slate-600">{inventoryState !== 'complete' || inventorySourceAvailability !== 'verified'
            ? 'Coded-value availability has not been confirmed for this source.'
            : categoryQuery ? 'No coded values match this search.'
              : 'No coded values are available for this source.'}</p> : null}
        <p className="m-0 text-sm font-medium text-slate-800">{selected.length} of 50 columns selected</p>
        {selected.length > 0 ? <ul className="flex flex-wrap gap-1">{selected.map((category) => {
          const context = selectedCategoryContext(category, selected);
          const system = category.system || 'not specified';
          const code = category.code || 'not specified';
          const identity = `system ${system}; code ${code}`;
          return (
            <li key={category.outputColumnId}
              aria-label={`${category.label}; ${identity}`}
              title={`${category.label}; ${identity}`}
              className="max-w-72 rounded bg-emerald-50 px-2 py-1 text-xs text-emerald-950">
              <span className="block truncate font-medium">{category.label}</span>
              {context.showSystem ? <span className="block break-all text-xs leading-tight text-emerald-800">System {system} · Code {code}</span>
                : context.showCode ? <span className="block break-all text-xs leading-tight text-emerald-800">Code {code}</span> : null}
            </li>
          );
        })}</ul> : null}
        <details className="text-sm text-slate-700"><summary className="cursor-pointer font-medium">Advanced value handling</summary>
          <div className="mt-2 grid gap-3 sm:grid-cols-2">
            <label>When several values match
              <select value={duplicatePolicy} onChange={(event) => {
                const next = event.target.value as typeof duplicatePolicy;
                setDuplicatePolicy(next);
                emitCandidate(source, selected, next);
              }}
                disabled={disabled} className="mt-1 block w-full rounded border border-slate-300 px-2 py-1.5">
                <option value="ERROR">Stop if several values match</option>
                {['integer', 'decimal', 'number'].includes(source.logicalType.toLowerCase()) ? <>
                  <option value="SUM">Add values</option><option value="MIN">Use smallest</option><option value="MAX">Use largest</option>
                </> : null}
              </select>
            </label>
            <label>When a record has no value
              <select value={missingCellPolicy} onChange={(event) => {
                const next = event.target.value as typeof missingCellPolicy;
                setMissingCellPolicy(next);
                emitCandidate(source, selected, duplicatePolicy, next);
              }}
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

export const CodedPivotEditor = (props: CodedPivotEditorProps) => {
  const scopeKey = JSON.stringify([
    props.project,
    props.explorerId,
    props.authResourcePath ?? null,
    props.snapshotToken,
    props.outputId,
    props.rowRoot,
  ]);
  return <CodedPivotEditorBody key={scopeKey} {...props} />;
};
