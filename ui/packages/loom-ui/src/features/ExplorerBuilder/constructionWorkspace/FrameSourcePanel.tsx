import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useLoomClient } from '../../../react';
import type { FrameDefinition, FrameSourceOption, SemanticInventoryItem } from '../../../types';
import { catalogChoiceIntent, catalogItemAvailability, type CatalogChoiceIntent, type CatalogItem } from '../catalogItems';

type LoadState<T> =
  | { readonly kind: 'loading' }
  | { readonly kind: 'ready'; readonly value: T }
  | { readonly kind: 'error'; readonly message: string };

const errorMessage = (error: unknown): string =>
  error instanceof Error ? error.message : 'Loom could not load the framing choices.';

const sourceIdentity = (source: Pick<FrameSourceOption, 'bindingId' | 'resourceType' | 'sourcePath' | 'owningScope' | 'keyPath' | 'valuePath' | 'logicalType' | 'route'>): string =>
  JSON.stringify([source.bindingId, source.resourceType, source.sourcePath, source.owningScope,
    source.keyPath, source.valuePath, source.logicalType, source.route.map((step) => [step.edgeId, step.fromNodeId, step.toNodeId,
      step.relationship, step.storageDirection, step.matchMode])]);

const frameIdentity = (frame: FrameDefinition): string =>
  sourceIdentity({ ...frame.source, route: frame.route });

const familyIdentity = (source: FrameSourceOption): string =>
  JSON.stringify([source.bindingId, source.resourceType, source.sourcePath, source.owningScope,
    source.keyPath, source.valuePath, source.logicalType]);

const preferredRoute = (routes: ReadonlyArray<FrameSourceOption>, rowRoot: string): FrameSourceOption | undefined =>
  routes.find((source) => source.route.length === 1 &&
    source.route[0]?.relationship.split('_')[0]?.toLowerCase() === rowRoot.toLowerCase()) ?? routes[0];

const relationshipLabel = (relationship: string): string =>
  relationship.replace(/_[A-Z][A-Za-z]*$/, '').replaceAll('_', ' ');

const routeChoiceLabel = (source: FrameSourceOption): string =>
  source.route.length === 0
    ? `On ${source.resourceType} records`
    : `${source.route[0]?.fromResourceType} → ${source.route.map((step) =>
      `${step.toResourceType} via ${relationshipLabel(step.relationship)}`).join(' → ')}`;

const routeLabel = (frame: Pick<FrameDefinition, 'route' | 'source'>): string =>
  frame.route.length === 0
    ? `On each ${frame.source.resourceType} record`
    : `${frame.route[0]?.fromResourceType} → ${frame.route.map((step) =>
      `${step.toResourceType} via ${relationshipLabel(step.relationship)}`).join(' → ')}`;

const formLabel: Record<FrameDefinition['form'], string> = {
  VALUE: 'One value; flag several matches',
  FIRST: 'First matching value',
  ALL: 'All matching values',
  DISTINCT: 'Distinct matching values',
};

const matchSummary = (frame: FrameDefinition): string =>
  `${formLabel[frame.form]} · no match: ${frame.zeroPolicy === 'EMPTY_LIST' ? 'empty list' : 'empty cell'}`;

const CategoryPicker = ({
  frame,
  project,
  explorerId,
  authResourcePath,
  snapshotToken,
  outputId,
  rowRoot,
  disabled,
  onAddSelected,
}: {
  readonly frame: FrameDefinition;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly disabled: boolean;
  readonly onAddSelected: (selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<unknown>;
}) => {
  const client = useLoomClient();
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');
  const [cursor, setCursor] = useState<string>();
  const [state, setState] = useState<LoadState<{ entries: ReadonlyArray<SemanticInventoryItem>; nextCursor?: string }>>({ kind: 'loading' });
  const [selected, setSelected] = useState<ReadonlyMap<string, SemanticInventoryItem>>(() => new Map());
  const [adding, setAdding] = useState(false);
  const [message, setMessage] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    setState({ kind: 'loading' });
    void client.browseSemanticInventory({
      project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot,
      frameId: frame.id, query, cursor, limit: 50,
      requestId: `frame-categories-${window.crypto.randomUUID()}`,
    }, controller.signal).then((response) => {
      if (controller.signal.aborted) return;
      if (response.frameId !== frame.id || response.frameSource?.id !== frame.id) {
        throw new Error('Loom returned categories for a different framing source.');
      }
      if (response.state !== 'complete') {
        throw new Error(`The coded-value inventory is ${response.state}. Try again when it is complete.`);
      }
      setState({ kind: 'ready', value: { entries: response.entries, nextCursor: response.nextCursor } });
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setState({ kind: 'error', message: errorMessage(error) });
    });
    return () => controller.abort();
  }, [authResourcePath, client, cursor, explorerId, frame.id, outputId, project, query, rowRoot, snapshotToken]);

  const selectedEntries = useMemo(() => [...selected.values()], [selected]);
  const addColumns = async () => {
    const choices: CatalogChoiceIntent[] = [];
    for (const entry of selectedEntries) {
      const item = { kind: 'SEMANTIC', item: entry, constructionChoice: entry.constructionChoice } satisfies CatalogItem;
      const choice = entry.constructionChoice;
      if (!choice || !catalogItemAvailability(item).selectable ||
        !choice.options.some((option) => option.form === frame.form && option.support === 'SUPPORTED')) {
        setMessage(`${entry.display || entry.code} is no longer available for this source. Refresh the choices.`);
        return;
      }
      const intent = catalogChoiceIntent({ item, choice, form: frame.form, rowRoot });
      choices.push({ ...intent, constructionChoice: { ...intent.constructionChoice, frameId: frame.id } });
    }
    setAdding(true);
    setMessage(undefined);
    try {
      await onAddSelected(choices);
      setSelected(new Map());
      setMessage('Coded-value columns added to this table.');
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setAdding(false);
    }
  };

  return (
    <div className="mt-3 rounded-lg border border-slate-200 bg-white p-3" data-testid={`frame-categories-${frame.id}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h4 className="text-sm font-semibold text-slate-900">Choose coded values for columns</h4>
          <p className="text-xs text-slate-600">Each code becomes a column filled by its paired value. The chosen match rule keeps the current rows.</p>
        </div>
        <span className="text-xs text-slate-600">{matchSummary(frame)}</span>
      </div>
      <form className="mt-3 flex gap-2" onSubmit={(event) => { event.preventDefault(); setCursor(undefined); setQuery(searchInput.trim()); }}>
        <input aria-label={`Search coded values in ${frame.title}`} type="search" value={searchInput}
          onChange={(event) => setSearchInput(event.currentTarget.value)} placeholder="Find a code or label"
          className="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1.5 text-sm" />
        <button type="submit" className="rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50">Search</button>
      </form>
      {state.kind === 'loading' ? <p className="mt-3 text-sm text-slate-600" role="status">Finding available coded values…</p> : null}
      {state.kind === 'error' ? <p className="mt-3 text-sm text-red-800" role="alert">{state.message}</p> : null}
      {state.kind === 'ready' ? (
        <>
          {state.value.entries.length === 0 ? <p className="mt-3 text-sm text-slate-600">No coded values match this search.</p> : null}
          <div className="mt-2 max-h-64 divide-y divide-slate-100 overflow-y-auto">
            {state.value.entries.map((entry) => {
              const key = `${entry.bindingId}:${entry.conceptId}`;
              const choice = entry.constructionChoice;
              const available = Boolean(choice && catalogItemAvailability({ kind: 'SEMANTIC', item: entry, constructionChoice: choice }).selectable &&
                choice.options.some((option) => option.form === frame.form && option.support === 'SUPPORTED'));
              return (
                <label key={key} className={`flex items-start gap-2 px-1 py-2 text-sm ${available ? 'cursor-pointer' : 'opacity-60'}`}>
                  <input type="checkbox" aria-label={`Select ${entry.display || entry.code}`} checked={selected.has(key)}
                    disabled={disabled || !available} onChange={() => setSelected((current) => {
                      const next = new Map(current);
                      if (next.has(key)) next.delete(key); else next.set(key, entry);
                      return next;
                    })} />
                  <span className="min-w-0 flex-1">
                    <strong className="block text-slate-900">{entry.display || entry.code}</strong>
                    <span className="block text-xs text-slate-600">{entry.resourceType} · {entry.valueType} · {entry.occurrences.toLocaleString()} observed occurrences</span>
                    {!available ? <span className="block text-xs text-amber-800">No executable column choice for this framing source</span> : null}
                  </span>
                </label>
              );
            })}
          </div>
          <div className="mt-2 flex justify-end">
            <button type="button" disabled={!state.value.nextCursor} onClick={() => setCursor(state.value.nextCursor)}
              className="text-xs font-medium text-blue-800 disabled:opacity-40">More coded values</button>
          </div>
        </>
      ) : null}
      <button type="button" disabled={disabled || adding || selected.size === 0} onClick={() => void addColumns()}
        className="mt-3 rounded bg-blue-700 px-3 py-2 text-sm font-semibold text-white disabled:opacity-40">
        Add {selected.size || ''} {selected.size === 1 ? 'column' : 'columns'}
      </button>
      {message ? <p className="mt-2 text-xs text-slate-700" role="status">{message}</p> : null}
    </div>
  );
};

export const FrameSourcePanel = ({
  project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot, frames, columns, disabled,
  onSet, onReplace, onRemove, onAddSelected, onRemoveColumn,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly rowRoot: string;
  readonly frames: ReadonlyArray<FrameDefinition>;
  readonly columns: ReadonlyArray<{ readonly column: string; readonly label: string; readonly frameId?: string }>;
  readonly disabled: boolean;
  readonly onSet: (choice: FrameSourceOption, form: FrameDefinition['form']) => Promise<boolean>;
  readonly onReplace: (frameId: string, choice: FrameSourceOption, form: FrameDefinition['form']) => Promise<boolean>;
  readonly onRemove: (frameId: string) => Promise<boolean>;
  readonly onAddSelected: (selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<unknown>;
  readonly onRemoveColumn: (column: string) => Promise<boolean>;
}) => {
  const client = useLoomClient();
  const [queryInput, setQueryInput] = useState('');
  const [query, setQuery] = useState('');
  const [cursor, setCursor] = useState<string>();
  const [state, setState] = useState<LoadState<{ sources: ReadonlyArray<FrameSourceOption>; nextCursor?: string }>>({ kind: 'loading' });
  const [editingFrame, setEditingFrame] = useState<string>();
  const [openFrame, setOpenFrame] = useState<string>();
  const [sourcePickerOpen, setSourcePickerOpen] = useState(frames.length === 0);
  const previousFrameIds = useRef(new Set(frames.map((frame) => frame.id)));
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const [showAllSources, setShowAllSources] = useState(false);
  const [selectedForms, setSelectedForms] = useState<ReadonlyMap<string, FrameDefinition['form']>>(() => new Map());
  const [selectedRoutes, setSelectedRoutes] = useState<ReadonlyMap<string, string>>(() => new Map());
  const [loadingMore, setLoadingMore] = useState(false);
  const [pendingImpact, setPendingImpact] = useState<
    | { readonly kind: 'remove'; readonly frameId: string }
    | { readonly kind: 'replace'; readonly frameId: string; readonly source: FrameSourceOption; readonly form: FrameDefinition['form'] }
  >();

  useEffect(() => {
    const added = frames.find((frame) => !previousFrameIds.current.has(frame.id));
    if (added) setOpenFrame(added.id);
    previousFrameIds.current = new Set(frames.map((frame) => frame.id));
  }, [frames]);

  useEffect(() => {
    const controller = new AbortController();
    if (cursor) setLoadingMore(true);
    else setState({ kind: 'loading' });
    void client.browseFrameSourceOptions({
      project, explorerId, authResourcePath, snapshotToken, outputId, query, cursor, limit: 20,
      requestId: `frame-source-options-${window.crypto.randomUUID()}`,
    }, controller.signal).then((response) => {
      if (controller.signal.aborted) return;
      if (response.outputId !== outputId || response.snapshotToken !== snapshotToken) {
        throw new Error('Loom returned framing choices for another table or dataset.');
      }
      setState((current) => ({ kind: 'ready', value: {
        sources: cursor && current.kind === 'ready' ? [...current.value.sources, ...response.sources] : response.sources,
        nextCursor: response.nextCursor,
      } }));
      setLoadingMore(false);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) {
        setState({ kind: 'error', message: errorMessage(error) });
        setLoadingMore(false);
      }
    });
    return () => controller.abort();
  }, [authResourcePath, client, cursor, explorerId, outputId, project, query, snapshotToken]);

  const saveSource = async (source: FrameSourceOption, form: FrameDefinition['form'], frameId?: string) => {
    setBusy(true);
    setMessage(undefined);
    try {
      const applied = frameId ? await onReplace(frameId, source, form) : await onSet(source, form);
      if (applied) {
        setEditingFrame(undefined);
        setSourcePickerOpen(false);
        setPendingImpact(undefined);
        setMessage('Framing source saved. Choose the coded values that should become columns.');
      }
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setBusy(false);
    }
  };

  const choose = (source: FrameSourceOption) => {
    const form = selectedForms.get(source.choiceId) ?? source.defaultForm;
    if (editingFrame && columns.some((column) => column.frameId === editingFrame)) {
      setPendingImpact({ kind: 'replace', frameId: editingFrame, source, form });
      return;
    }
    void saveSource(source, form, editingFrame);
  };

  const remove = async (frameId: string) => {
    setBusy(true);
    setMessage(undefined);
    try {
      if (await onRemove(frameId)) {
        setPendingImpact(undefined);
        setOpenFrame(undefined);
        setMessage('Framing source removed.');
      }
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setBusy(false);
    }
  };

  const removeColumn = async (column: string) => {
    setBusy(true);
    setMessage(undefined);
    try {
      if (await onRemoveColumn(column)) setMessage('Coded-value column removed.');
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setBusy(false);
    }
  };

  const usedSources = new Set(frames.filter((frame) => frame.id !== editingFrame).map(frameIdentity));
  const visibleSources = state.kind === 'ready'
    ? state.value.sources.filter((source, index, all) =>
      !usedSources.has(sourceIdentity(source)) && all.findIndex((candidate) => candidate.choiceId === source.choiceId) === index)
    : [];
  const sourceGroups = [...visibleSources.reduce((groups, source) => {
    const key = familyIdentity(source);
    groups.set(key, [...(groups.get(key) ?? []), source]);
    return groups;
  }, new Map<string, FrameSourceOption[]>())];

  return (
    <section aria-label="Frame coded values into columns" data-testid="frame-source-panel" className="min-w-0">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-slate-900">{frames.length === 0 ? 'Choose a coded source' : 'Coded sources'}</h2>
          <p className="text-xs text-slate-600">Each code names a column; its paired value fills the cells.</p>
        </div>
        <button type="button" aria-expanded={sourcePickerOpen} onClick={() => setSourcePickerOpen((open) => !open)}
          className="shrink-0 text-sm font-semibold text-blue-800 hover:underline">
          {sourcePickerOpen ? 'Hide source choices' : editingFrame ? 'Choose replacement' : frames.length === 0 ? 'Choose coded-value columns' : 'Add coded source'}
          <span aria-hidden="true"> {sourcePickerOpen ? '▴' : '▾'}</span>
        </button>
      </div>
      {frames.map((frame) => (
        <div key={frame.id} className="mt-2 rounded-md border border-emerald-200 bg-emerald-50/40 p-2" data-testid={`saved-frame-${frame.id}`}>
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h3 className="text-sm font-semibold text-slate-900">{frame.title}</h3>
              <p className="text-xs text-slate-600">{routeLabel(frame)} · {matchSummary(frame)}</p>
            </div>
            <div className="flex gap-2 text-xs">
              <button type="button" disabled={disabled || busy} onClick={() => { setOpenFrame(openFrame === frame.id ? undefined : frame.id); setEditingFrame(undefined); }}
                className="font-semibold text-blue-800">{openFrame === frame.id ? 'Close values' : 'Choose values'}</button>
              <button type="button" disabled={disabled || busy} onClick={() => { setEditingFrame(frame.id); setOpenFrame(undefined); setSourcePickerOpen(true); }}
                className="font-semibold text-blue-800">Change source</button>
              <button type="button" disabled={disabled || busy} onClick={() => {
                if (columns.some((column) => column.frameId === frame.id)) setPendingImpact({ kind: 'remove', frameId: frame.id });
                else void remove(frame.id);
              }}
                className="font-semibold text-red-800">Remove</button>
            </div>
          </div>
          {pendingImpact?.frameId === frame.id ? (
            <div className="mt-3 rounded border border-amber-300 bg-amber-50 p-2 text-xs text-amber-950" role="alert">
              <p>{pendingImpact.kind === 'replace' ? 'Changing this source' : 'Removing this source'} also removes {columns.filter((column) => column.frameId === frame.id).map((column) => column.label).join(', ')} from the table.</p>
              <div className="mt-2 flex gap-3">
                <button type="button" disabled={disabled || busy} className="font-semibold text-red-800" onClick={() => {
                  if (pendingImpact.kind === 'replace') void saveSource(pendingImpact.source, pendingImpact.form, pendingImpact.frameId);
                  else void remove(pendingImpact.frameId);
                }}>Apply this change</button>
                <button type="button" onClick={() => setPendingImpact(undefined)}>Cancel</button>
              </div>
            </div>
          ) : null}
          {columns.filter((column) => column.frameId === frame.id).map((column) => (
            <div key={column.column} className="mt-2 flex items-center justify-between gap-2 rounded bg-white px-2 py-1.5 text-xs">
              <span className="min-w-0 truncate text-slate-800">{column.label}</span>
              <button type="button" disabled={disabled || busy} onClick={() => void removeColumn(column.column)}
                aria-label={`Remove ${column.label} column`} className="shrink-0 font-semibold text-red-800 disabled:opacity-40">Remove column</button>
            </div>
          ))}
          {openFrame === frame.id ? <CategoryPicker frame={frame} project={project} explorerId={explorerId}
            authResourcePath={authResourcePath} snapshotToken={snapshotToken} outputId={outputId} rowRoot={rowRoot}
            disabled={disabled || busy} onAddSelected={onAddSelected} /> : null}
        </div>
      ))}
      {sourcePickerOpen ? <div className="mt-2 border-t border-slate-100 pt-2">
        {editingFrame ? <p className="mt-1 text-xs text-slate-600">Choose the same source with a different match rule to change how repeated values are handled.</p> : null}
        <form className="mt-3 flex gap-2" onSubmit={(event) => { event.preventDefault(); setCursor(undefined); setQuery(queryInput.trim()); }}>
          <input type="search" aria-label="Search framing sources" value={queryInput} onChange={(event) => setQueryInput(event.currentTarget.value)}
            placeholder="Search a code, record type, or source" className="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1.5 text-sm" />
          <button type="submit" className="rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50">Search</button>
        </form>
        {state.kind === 'loading' ? <p className="mt-3 text-sm text-slate-600" role="status">Finding executable sources…</p> : null}
        {state.kind === 'error' ? <p className="mt-3 text-sm text-red-800" role="alert">{state.message}</p> : null}
        {state.kind === 'ready' ? (
          <>
            {sourceGroups.length === 0 ? <p className="mt-3 text-sm text-slate-600">No other executable coded sources match this search.</p> : null}
            <div className="mt-2 max-h-80 divide-y divide-slate-100 overflow-y-auto rounded-md border border-slate-200">
              {(showAllSources ? sourceGroups : sourceGroups.slice(0, 4)).map(([key, routes]) => {
                const source = routes.find((route) => route.choiceId === selectedRoutes.get(key)) ?? preferredRoute(routes, rowRoot);
                if (!source) return null;
                return (
                  <div key={key} className="px-3 py-2">
                    <strong className="block text-sm text-slate-900">{source.title}</strong>
                    <span className="block text-xs text-slate-500">Example: {source.exampleConcept} · {source.observedOccurrences.toLocaleString()} source occurrences</span>
                    {routes.length > 1 ? (
                      <label className="mt-2 block text-xs font-medium text-slate-700">
                        Relationship path
                        <select aria-label={`Relationship path for ${source.title}`} value={source.choiceId}
                          onChange={(event) => {
                            const choiceId = event.currentTarget.value;
                            setSelectedRoutes((current) => new Map(current).set(key, choiceId));
                          }}
                          className="mt-1 block w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-xs font-normal">
                          {routes.map((route) => <option key={route.choiceId} value={route.choiceId}>{routeChoiceLabel(route)}</option>)}
                        </select>
                      </label>
                    ) : <p className="mt-2 text-xs text-slate-600">{routeChoiceLabel(source)}</p>}
                    <p className="mt-1 text-[11px] text-slate-500">Loom checks matching rows when you add a column.</p>
                    {source.forms.length > 1 ? (
                      <details className="mt-2 text-xs text-slate-600">
                        <summary className="cursor-pointer">When a row has several values</summary>
                        <select aria-label={`Multiple values for ${source.title}`} value={selectedForms.get(source.choiceId) ?? source.defaultForm}
                          onChange={(event) => {
                            const chosenForm = event.currentTarget.value;
                            const form = source.forms.find((option) => option.form === chosenForm)?.form;
                            if (form) setSelectedForms((current) => new Map(current).set(source.choiceId, form));
                          }}
                          className="mt-1 w-full rounded border border-slate-300 bg-white px-2 py-1.5">
                          {source.forms.map((option) => <option key={option.form} value={option.form}>{formLabel[option.form]}</option>)}
                        </select>
                      </details>
                    ) : null}
                    <button type="button" disabled={disabled || busy || !source.forms.some((form) => form.form === source.defaultForm)}
                      onClick={() => choose(source)} data-testid={`frame-source-choice-${source.choiceId}`}
                      className="mt-2 rounded bg-blue-700 px-3 py-1.5 text-xs font-semibold text-white hover:bg-blue-800 disabled:opacity-40">
                      Use this source
                    </button>
                  </div>
                );
              })}
            </div>
            {sourceGroups.length > 4 ? <button type="button" onClick={() => setShowAllSources((current) => !current)}
              className="mt-2 text-xs font-semibold text-blue-800">{showAllSources ? 'Show fewer families' : `Show ${sourceGroups.length - 4} more families`}</button> : null}
            {state.value.nextCursor ? <button type="button" disabled={loadingMore} onClick={() => setCursor(state.value.nextCursor)}
              className="mt-2 text-xs font-semibold text-blue-800 disabled:opacity-40">{loadingMore ? 'Loading more paths…' : 'More sources and paths'}</button> : null}
          </>
        ) : null}
      </div> : null}
      {editingFrame ? <button type="button" className="mt-2 text-xs text-slate-600" onClick={() => setEditingFrame(undefined)}>Cancel source change</button> : null}
      {message ? <p className="mt-2 text-xs text-slate-700" role="status">{message}</p> : null}
    </section>
  );
};
