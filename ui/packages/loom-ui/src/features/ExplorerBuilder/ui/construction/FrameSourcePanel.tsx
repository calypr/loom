import React, { useMemo, useState } from 'react';
import { useEscapeToClose } from '../../dom/useEscapeToClose';
import { useBuilderSession } from '../../session/BuilderSessionProvider';
import { useFrameCategories, useFrameSourceOptions } from '../../session/frameSourceQueries';
import type { FrameDefinition, FrameSourceOption, SemanticInventoryItem } from '../../../../contracts/types';
import { catalogChoiceIntent, catalogItemAvailability, type CatalogChoiceIntent, type CatalogItem } from '../../model/catalogItems';

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
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');
  const [cursorState, setCursorState] = useState<{ readonly searchKey: string; readonly cursor: string }>();
  const [loadedPages, setLoadedPages] = useState<{
    readonly searchKey: string;
    readonly value: { readonly entries: ReadonlyArray<SemanticInventoryItem>; readonly nextCursor?: string };
  }>();
  const [selected, setSelected] = useState<ReadonlyMap<string, SemanticInventoryItem>>(() => new Map());
  const [adding, setAdding] = useState(false);
  const [message, setMessage] = useState<string>();
  const searchKey = JSON.stringify([
    project, explorerId, authResourcePath ?? '', snapshotToken, outputId, rowRoot,
    frameIdentity(frame), query,
  ]);
  const currentCursor = cursorState?.searchKey === searchKey ? cursorState.cursor : undefined;
  const categoryQuery = useFrameCategories({
    project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot, query,
    frameId: frame.id, frameKey: frameIdentity(frame), currentCursor,
  });
  const previousPage = loadedPages?.searchKey === searchKey ? loadedPages.value : undefined;
  const state: LoadState<{ entries: ReadonlyArray<SemanticInventoryItem>; nextCursor?: string }> = categoryQuery.error
    ? { kind: 'error', message: errorMessage(categoryQuery.error) }
    : categoryQuery.data
      ? { kind: 'ready', value: {
        entries: currentCursor && previousPage ? [...previousPage.entries, ...categoryQuery.data.entries] : categoryQuery.data.entries,
        nextCursor: categoryQuery.data.nextCursor,
      } }
      : categoryQuery.isLoading && currentCursor && previousPage
        ? { kind: 'ready', value: previousPage }
        : categoryQuery.isLoading
          ? { kind: 'loading' }
          : { kind: 'loading' };

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
      <form className="mt-3 flex gap-2" onSubmit={(event) => {
        event.preventDefault();
        const nextQuery = searchInput.trim();
        const nextKey = JSON.stringify([
          project, explorerId, authResourcePath ?? '', snapshotToken, outputId, rowRoot, frameIdentity(frame), nextQuery,
        ]);
        setLoadedPages({ searchKey: nextKey, value: { entries: [] } });
        setCursorState(undefined);
        setQuery(nextQuery);
      }}>
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
            <button type="button" disabled={!state.value.nextCursor} onClick={() => {
              if (!state.value.nextCursor) return;
              setLoadedPages({ searchKey, value: state.value });
              setCursorState({ searchKey, cursor: state.value.nextCursor });
            }}
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
  outputId, rowRoot, frames, columns, disabled, allowNewSource = true, addSourceRoute, unassignedFrameIds,
  onSet, onReplace, onRemove, onAddSelected, onRemoveColumn,
}: {
  readonly outputId: string;
  readonly rowRoot: string;
  readonly frames: ReadonlyArray<FrameDefinition>;
  readonly columns: ReadonlyArray<{ readonly column: string; readonly label: string; readonly frameId?: string }>;
  readonly disabled: boolean;
  readonly allowNewSource?: boolean;
  readonly unassignedFrameIds?: ReadonlySet<string>;
  readonly addSourceRoute?: { readonly resourceType: string; readonly edgeIds: readonly string[] };
  readonly onSet: (choice: FrameSourceOption, form: FrameDefinition['form']) => Promise<boolean>;
  readonly onReplace: (frameId: string, choice: FrameSourceOption, form: FrameDefinition['form']) => Promise<boolean>;
  readonly onRemove: (frameId: string) => Promise<boolean>;
  readonly onAddSelected: (selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<unknown>;
  readonly onRemoveColumn: (column: string) => Promise<boolean>;
}) => {
  const { project, explorerId, authResourcePath, snapshotToken } = useBuilderSession(({ builderQueryArgs, state }) => ({
    ...builderQueryArgs,
    explorerId: state.explorerId,
    snapshotToken: state.catalog.snapshotToken,
  }));
  const [queryInput, setQueryInput] = useState('');
  const [query, setQuery] = useState('');
  const [cursorState, setCursorState] = useState<{ readonly searchKey: string; readonly cursor: string }>();
  const [editingFrame, setEditingFrame] = useState<string>();
  const [openFrame, setOpenFrame] = useState<string>();
  const [sourcePickerOpen, setSourcePickerOpen] = useState(Boolean(addSourceRoute));
  const [pendingNewFrame, setPendingNewFrame] = useState<{
    readonly scopeKey: string;
    readonly sourceIdentity: string;
    readonly previousFrameIds: ReadonlySet<string>;
  }>();
  const [loadedSourcePages, setLoadedSourcePages] = useState<{
    readonly searchKey: string;
    readonly value: { readonly sources: ReadonlyArray<FrameSourceOption>; readonly nextCursor?: string };
  }>();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const [showAllSources, setShowAllSources] = useState(false);
  const [selectedForms, setSelectedForms] = useState<ReadonlyMap<string, FrameDefinition['form']>>(() => new Map());
  const [selectedRoutes, setSelectedRoutes] = useState<ReadonlyMap<string, string>>(() => new Map());
  const [pendingImpact, setPendingImpact] = useState<
    | { readonly kind: 'remove'; readonly frameId: string }
    | { readonly kind: 'replace'; readonly frameId: string; readonly source: FrameSourceOption; readonly form: FrameDefinition['form'] }
  >();

  const canCloseSourcePicker = sourcePickerOpen && (!addSourceRoute || Boolean(editingFrame));
  useEscapeToClose(Boolean(pendingImpact || pendingNewFrame || canCloseSourcePicker || editingFrame || (addSourceRoute && openFrame)), () => {
    if (pendingImpact) setPendingImpact(undefined);
    else if (pendingNewFrame) setPendingNewFrame(undefined);
    else if (canCloseSourcePicker) setSourcePickerOpen(false);
    else if (editingFrame) setEditingFrame(undefined);
    else if (addSourceRoute) setOpenFrame(undefined);
  });
  const sourceResourceType = addSourceRoute?.resourceType;
  const sourceSearchKeyFor = (search: string) => JSON.stringify([
    project, explorerId, authResourcePath ?? '', snapshotToken, outputId, rowRoot, sourceResourceType ?? '', search,
  ]);
  const sourceSearchKey = sourceSearchKeyFor(query);
  const cursor = cursorState?.searchKey === sourceSearchKey ? cursorState.cursor : undefined;
  const sourcePickerEnabled = sourcePickerOpen && Boolean(snapshotToken && outputId && rowRoot);
  const sourceQuery = useFrameSourceOptions({
    project, explorerId, authResourcePath, snapshotToken, outputId, rowRoot, query,
    cursor, sourceResourceType, sourcePickerEnabled,
  });
  const priorSourcePage = loadedSourcePages?.searchKey === sourceSearchKey ? loadedSourcePages.value : undefined;
  const state: LoadState<{ sources: ReadonlyArray<FrameSourceOption>; nextCursor?: string }> = !sourcePickerEnabled
    ? { kind: 'loading' }
    : sourceQuery.error
      ? { kind: 'error', message: errorMessage(sourceQuery.error) }
      : sourceQuery.data
        ? { kind: 'ready', value: {
          sources: cursor && priorSourcePage ? [...priorSourcePage.sources, ...sourceQuery.data.sources] : sourceQuery.data.sources,
          nextCursor: sourceQuery.data.nextCursor,
        } }
        : sourceQuery.isLoading && cursor && priorSourcePage
          ? { kind: 'ready', value: priorSourcePage }
          : sourceQuery.isLoading
            ? { kind: 'loading' }
            : { kind: 'loading' };
  const loadingMore = cursor !== undefined && sourceQuery.isLoading;
  const frameScopeKey = JSON.stringify([project, explorerId, authResourcePath ?? '', snapshotToken, outputId]);
  const automaticOpenFrame = pendingNewFrame?.scopeKey === frameScopeKey
    ? frames.find((frame) => !pendingNewFrame.previousFrameIds.has(frame.id) &&
      frameIdentity(frame) === pendingNewFrame.sourceIdentity)?.id
    : undefined;
  const activeOpenFrame = openFrame ?? automaticOpenFrame;

  const saveSource = async (source: FrameSourceOption, form: FrameDefinition['form'], frameId?: string) => {
    setBusy(true);
    setMessage(undefined);
    try {
      const applied = frameId ? await onReplace(frameId, source, form) : await onSet(source, form);
      if (applied) {
        if (!frameId) {
          setPendingNewFrame({
            scopeKey: frameScopeKey,
            sourceIdentity: sourceIdentity(source),
            previousFrameIds: new Set(frames.map((frame) => frame.id)),
          });
          setOpenFrame(undefined);
        }
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
        setPendingNewFrame(undefined);
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
  const matchesGraphSource = (resourceType: string, route: FrameSourceOption['route']) =>
    !addSourceRoute || (resourceType === addSourceRoute.resourceType &&
      route.length === addSourceRoute.edgeIds.length &&
      route.every((step, depth) => step.edgeId === addSourceRoute.edgeIds[depth]));
  const visibleSources = state.kind === 'ready'
    ? state.value.sources.filter((source, index, all) =>
      matchesGraphSource(source.resourceType, source.route) &&
      !usedSources.has(sourceIdentity(source)) && all.findIndex((candidate) => candidate.choiceId === source.choiceId) === index)
    : [];
  const visibleFrames = frames.filter((frame) => matchesGraphSource(frame.source.resourceType, frame.route));
  const sourceGroups = [...visibleSources.reduce((groups, source) => {
    const key = familyIdentity(source);
    groups.set(key, [...(groups.get(key) ?? []), source]);
    return groups;
  }, new Map<string, FrameSourceOption[]>())];

  return (
    <section aria-label="Frame coded values into columns" data-testid="frame-source-panel" className="min-w-0">
      {allowNewSource || editingFrame || sourcePickerOpen ? <div className="flex flex-wrap items-center justify-between gap-2">
        {allowNewSource ? <div className="min-w-0">
          <h2 className="text-sm font-semibold text-slate-900">{addSourceRoute ? 'Columns from code values' : 'Coded sources'}</h2>
          <p className="text-xs text-slate-600">{addSourceRoute ? 'Choose a source, then select the codes to turn into columns.' : frames.length === 0
            ? 'Browse coded-value sources Loom can add to this table.'
            : 'Each code names a column; its paired value fills the cells.'}</p>
        </div> : null}
        {!addSourceRoute ? <button type="button" aria-expanded={sourcePickerOpen} onClick={() => setSourcePickerOpen((open) => !open)}
            className="shrink-0 text-sm font-semibold text-blue-800 hover:underline">
            {sourcePickerOpen ? 'Hide source choices' : editingFrame ? 'Choose replacement' : frames.length === 0 ? 'Browse sources' : 'Add coded source'}
            <span aria-hidden="true"> {sourcePickerOpen ? '▴' : '▾'}</span>
        </button> : null}
      </div> : null}
      {visibleFrames.map((frame) => (
        <div key={frame.id} className={allowNewSource ? "mt-2 rounded-md border border-emerald-200 bg-emerald-50/40 p-2" : "border-b border-slate-100 py-2"} data-testid={`saved-frame-${frame.id}`}>
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h3 className="text-sm font-semibold text-slate-900">{frame.title}</h3>
              <p className="text-xs text-slate-600">{routeLabel(frame)} · {matchSummary(frame)}</p>
              {unassignedFrameIds?.has(frame.id) ? <p className="text-xs text-slate-600">Source route has no unique graph node.</p> : null}
            </div>
            <div className="flex gap-2 text-xs">
              <button type="button" disabled={disabled || busy} onClick={() => {
                setPendingNewFrame(undefined);
                setOpenFrame(activeOpenFrame === frame.id ? undefined : frame.id);
                setEditingFrame(undefined);
              }}
                className="font-semibold text-blue-800">{activeOpenFrame === frame.id ? 'Close values' : addSourceRoute ? 'Browse addable values' : 'Choose values'}</button>
              {!addSourceRoute ? <>
                <button type="button" disabled={disabled || busy} onClick={() => { setPendingNewFrame(undefined); setEditingFrame(frame.id); setOpenFrame(undefined); setSourcePickerOpen(true); }}
                  className="font-semibold text-blue-800">Change source</button>
                <button type="button" disabled={disabled || busy} onClick={() => {
                  if (columns.some((column) => column.frameId === frame.id)) setPendingImpact({ kind: 'remove', frameId: frame.id });
                  else void remove(frame.id);
                }}
                  className="font-semibold text-red-800">Remove</button>
              </> : null}
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
          {!addSourceRoute ? columns.filter((column) => column.frameId === frame.id).map((column) => (
            <div key={column.column} className="mt-2 flex items-center justify-between gap-2 rounded bg-white px-2 py-1.5 text-xs">
              <span className="min-w-0 truncate text-slate-800">{column.label}</span>
              <button type="button" disabled={disabled || busy} onClick={() => void removeColumn(column.column)}
                aria-label={`Remove ${column.label} column`} className="shrink-0 font-semibold text-red-800 disabled:opacity-40">Remove column</button>
            </div>
          )) : null}
          {activeOpenFrame === frame.id ? <CategoryPicker frame={frame} project={project} explorerId={explorerId}
            authResourcePath={authResourcePath} snapshotToken={snapshotToken} outputId={outputId} rowRoot={rowRoot}
            disabled={disabled || busy} onAddSelected={onAddSelected} /> : null}
        </div>
      ))}
      {sourcePickerOpen ? <div className="mt-2 border-t border-slate-100 pt-2">
        {editingFrame ? <p className="mt-1 text-xs text-slate-600">Choose the same source with a different match rule to change how repeated values are handled.</p> : null}
        <form className="mt-3 flex gap-2" onSubmit={(event) => {
          event.preventDefault();
          const nextQuery = queryInput.trim();
          const nextKey = sourceSearchKeyFor(nextQuery);
          setLoadedSourcePages({ searchKey: nextKey, value: { sources: [] } });
          setCursorState(undefined);
          setQuery(nextQuery);
        }}>
          <input type="search" aria-label="Search framing sources" value={queryInput} onChange={(event) => setQueryInput(event.currentTarget.value)}
            placeholder="Search a code, record type, or source" className="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1.5 text-sm" />
          <button type="submit" className="rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-50">Search</button>
        </form>
        {state.kind === 'loading' ? <p className="mt-3 text-sm text-slate-600" role="status">Finding executable sources…</p> : null}
        {state.kind === 'error' ? <p className="mt-3 text-sm text-red-800" role="alert">{state.message}</p> : null}
        {state.kind === 'ready' ? (
          <>
            {sourceGroups.length === 0 ? <p className="mt-3 text-sm text-slate-600">{state.value.nextCursor ? 'No sources for this graph path on this page. More results are available.' : query ? 'No coded sources match your search.' : 'No coded sources are available on this node.'}</p> : null}
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
            {state.value.nextCursor ? <button type="button" disabled={loadingMore} onClick={() => {
              if (!state.value.nextCursor) return;
              setLoadedSourcePages({ searchKey: sourceSearchKey, value: state.value });
              setCursorState({ searchKey: sourceSearchKey, cursor: state.value.nextCursor });
            }}
              className="mt-2 text-xs font-semibold text-blue-800 disabled:opacity-40">{loadingMore ? 'Loading more paths…' : 'More sources and paths'}</button> : null}
          </>
        ) : null}
      </div> : null}
      {editingFrame ? <button type="button" className="mt-2 text-xs text-slate-600" onClick={() => setEditingFrame(undefined)}>Cancel source change</button> : null}
      {message ? <p className="mt-2 text-xs text-slate-700" role="status">{message}</p> : null}
    </section>
  );
};
