import React, { useMemo } from 'react';
import {
  Badge,
  Button,
  Checkbox,
  Group,
  Paper,
  Select,
  Stack,
  Table,
  Text,
  UnstyledButton,
} from '@mantine/core';
import { flexRender, getCoreRowModel, useReactTable, type ColumnDef } from '@tanstack/react-table';
import type { EChartsOption } from 'echarts';
import ReactECharts from 'echarts-for-react';
import type { LoomFacetResult, LoomOutputResult } from '../../api';
import type { ExplorerRuntimeBindingV1, ExplorerRuntimeOutputV1, ExplorerRuntimeV1 } from '../../types';
import { PAGE_SIZES, activeOutputState, isPageSize, type ViewerState } from './model';
import type { ViewerAction } from './reducer';
import { chartFacetName, facetName, filterLabel, filterType } from './serialization';

export type ViewerRow = Readonly<Record<string, unknown>>;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

export const textFor = (value: unknown): string => {
  if (value === undefined || value === null || value === '') return '—';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') return String(value);
  if (Array.isArray(value)) return value.map(textFor).filter((entry) => entry !== '—').join('; ') || '—';
  if (isRecord(value)) {
    for (const key of ['text', 'display', 'value', 'code', 'reference']) {
      if (value[key] !== undefined) return textFor(value[key]);
    }
    return JSON.stringify(value);
  }
  return String(value);
};

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const fileIdentifier = (value: unknown, row: ViewerRow): string | undefined => [
  value,
  row.id,
  row.file_id,
  row.file_uuid,
  row.document_reference_id,
  row.document_reference_identifier,
  row.uuid,
  row.sha256,
].find((candidate): candidate is string => typeof candidate === 'string' && UUID_PATTERN.test(candidate.trim()));

const fileName = (value: unknown, row: ViewerRow): string => {
  for (const candidate of [
    row.document_reference_source_path,
    row.document_reference_content_attachment_url,
    row.document_reference_content_attachment_title,
    row.file_name,
    value,
  ]) {
    if (typeof candidate === 'string' && candidate) return candidate;
  }
  return '';
};

const fileActionURL = (action: string, configuredURL: string | undefined, fileId: string): string | undefined => {
  const baseURL = (configuredURL ?? (action === 'file_download' ? '/download' : action === 'file_image' ? '/image-viewer/view' : '')).replace(/\/+$/, '');
  if (!baseURL) return undefined;
  const separator = baseURL.includes('?') ? '&' : '?';
  return `${baseURL}/${encodeURIComponent(fileId)}${action === 'file_download' ? `${separator}redirect=true` : ''}`;
};

const FileCell = ({ value, row, runtime }: { readonly value: unknown; readonly row: ViewerRow; readonly runtime: ExplorerRuntimeV1 }) => {
  const fileId = fileIdentifier(value, row);
  if (!fileId) return <Text size="xs">{textFor(value)}</Text>;
  const name = fileName(value, row).split(/[?#]/, 1)[0];
  const extension = name.includes('.') ? name.split('.').pop()?.toLowerCase() ?? '' : '';
  const configuredActions = runtime.fileActions?.extensions?.[extension]
    ?? runtime.fileActions?.extensions?.[`.${extension}`]
    ?? runtime.fileActions?.extensions?.default
    ?? ['file_download'];
  const actions = configuredActions.flatMap((action) => {
    const url = fileActionURL(action, runtime.fileActions?.actions?.[action], fileId);
    return url ? [{ action, url }] : [];
  });
  if (actions.length === 0) return null;
  return (
    <Group gap="xs" wrap="wrap">
      {actions.map(({ action, url }) => (
        <Button component="a" href={url} target="_blank" rel="noreferrer" variant="light" size="compact-xs" key={action} aria-label={action} title={action} onClick={(event) => event.stopPropagation()}>
          {action === 'file_download' ? 'Download' : action === 'file_image' ? 'View image' : action}
        </Button>
      ))}
    </Group>
  );
};

export const facetValues = (facet: LoomFacetResult | undefined, column: string): Array<{ readonly value: string; readonly count?: string }> => {
  if (!facet) return [];
  const values = new Map<string, number | undefined>();
  for (const row of facet.rows) {
    const raw = row.key ?? row[column];
    if (raw === undefined || raw === null) continue;
    const value = textFor(raw);
    const count = Number(row.doc_count);
    const previous = values.get(value);
    values.set(value, Number.isFinite(count) ? (previous ?? 0) + count : previous);
  }
  return [...values].map(([value, count]) => ({
    value,
    ...(count === undefined ? {} : { count: String(count) }),
  }));
};

const COLLAPSED_FACET_VALUES = 6;

const FacetFilter = ({ id, label, description, facet, column, values, multiple, onChange, state, dispatch }: { readonly id: string; readonly label: string; readonly description?: string; readonly facet?: LoomFacetResult; readonly column: string; readonly values: ReadonlyArray<string>; readonly multiple: boolean; readonly onChange: (values: ReadonlyArray<string>) => void; readonly state: ViewerState; readonly dispatch: React.Dispatch<ViewerAction> }) => {
  const expanded = state.expandedFacets.includes(id);
  const options = facetValues(facet, column);
  const selected = new Set(values);
  const orderedOptions = [...options].sort((left, right) => Number(selected.has(right.value)) - Number(selected.has(left.value)));
  const visibleOptions = expanded ? orderedOptions : orderedOptions.slice(0, COLLAPSED_FACET_VALUES);
  const hiddenCount = Math.max(0, orderedOptions.length - COLLAPSED_FACET_VALUES);
  const toggleValue = (value: string, checked: boolean) => {
    if (!checked) {
      onChange(values.filter((candidate) => candidate !== value));
      return;
    }
    onChange(multiple ? [...values, value] : [value]);
  };

  return (
    <section className="border-b border-slate-200 px-4 py-4 last:border-b-0" aria-labelledby={`${id}-label`}>
      <Group justify="space-between" align="flex-start" gap="xs" mb={description ? 2 : "xs"} wrap="nowrap">
        <Text id={`${id}-label`} fw={650} size="sm" className="min-w-0 break-words">{label}</Text>
        {values.length > 0 ? (
          <Button variant="subtle" size="compact-xs" px={4} onClick={() => onChange([])}>Clear</Button>
        ) : null}
      </Group>
      {description ? <Text c="dimmed" size="xs" mb="xs">{description}</Text> : null}
      {visibleOptions.length > 0 ? (
        <Stack gap={3}>
          {visibleOptions.map((entry) => (
            <Checkbox
              key={entry.value}
              size="xs"
              checked={selected.has(entry.value)}
              onChange={(event) => toggleValue(entry.value, event.currentTarget.checked)}
              label={(
                <Group justify="space-between" gap="xs" wrap="nowrap">
                  <Text size="xs" truncate title={entry.value}>{entry.value}</Text>
                  <Text c="dimmed" size="xs" className="shrink-0 tabular-nums">{entry.count ?? '—'}</Text>
                </Group>
              )}
              styles={{ root: { width: '100%' }, body: { width: '100%', alignItems: 'center' }, input: { borderWidth: 1 }, labelWrapper: { flex: 1, minWidth: 0 }, label: { width: '100%', paddingInlineStart: 8 } }}
            />
          ))}
        </Stack>
      ) : <Text c="dimmed" size="xs">No values available</Text>}
      {hiddenCount > 0 ? (
        <Button variant="subtle" size="compact-xs" px={0} mt="xs" aria-expanded={expanded} onClick={() => dispatch({ type: 'toggleFacet', facet: id })}>
          {expanded ? 'Show fewer' : `Show ${hiddenCount} more`}
        </Button>
      ) : null}
      {facet?.truncated ? <Text c="dimmed" size="xs" mt={4}>Showing the most common values</Text> : null}
    </section>
  );
};

const filterBindings = (output: ExplorerRuntimeOutputV1): ReadonlyArray<ExplorerRuntimeBindingV1> =>
  output.filters.filter((binding, index, all) => all.findIndex((candidate) => candidate.column === binding.column) === index);

export const FilterRail = ({ runtime, output, result, state, dispatch }: { readonly runtime: ExplorerRuntimeV1; readonly output: ExplorerRuntimeOutputV1; readonly result?: LoomOutputResult; readonly state: ViewerState; readonly dispatch: React.Dispatch<ViewerAction> }) => {
  const outputState = activeOutputState(state, output.outputId);
  const sharedEntries = Object.entries(runtime.sharedFilters).filter(([, bindings]) => bindings.some((binding) => !binding.outputId || binding.outputId === output.outputId));
  const facets = result?.facets ?? [];
  const facetFor = (column: string) => facets.find((facet) => facet.name === facetName(output.outputId, column));
  const bindings = filterBindings(output).filter((binding) => !output.fixedFilters[binding.column]);

  return (
    <Paper component="aside" withBorder radius="md" aria-label="Explorer filters" className="self-start overflow-hidden lg:sticky lg:top-4">
      <Group justify="space-between" px="md" py="sm" className="border-b border-slate-200 bg-slate-50">
        <Text fw={700} size="sm">Filters</Text>
        <Badge variant="light" radius="xl">{bindings.length + sharedEntries.length}</Badge>
      </Group>
      <Stack gap={0}>
        {sharedEntries.map(([name, sharedBindings]) => {
          const binding = sharedBindings.find((candidate) => !candidate.outputId || candidate.outputId === output.outputId) ?? sharedBindings[0];
          const values = state.sharedFilters[name] ?? [];
          return (
            <FacetFilter key={`shared-${name}`} id={`${output.outputId}:shared:${name}`} label={name} description="Shared across linked outputs" facet={facetFor(binding.column)} column={binding.column} values={values} multiple onChange={(nextValues) => dispatch({ type: 'proposeSharedFilter', proposal: { name, values: nextValues } })} state={state} dispatch={dispatch} />
          );
        })}
        {bindings.map((binding) => {
          const values = outputState.filterValues[binding.column] ?? [];
          const kind = filterType(binding);
          return (
            <FacetFilter key={binding.column} id={`${output.outputId}:filter:${binding.column}`} label={filterLabel(binding)} facet={facetFor(binding.column)} column={binding.column} values={values} multiple={kind !== 'enum'} onChange={(nextValues) => dispatch({ type: 'setFilter', outputId: output.outputId, column: binding.column, values: nextValues })} state={state} dispatch={dispatch} />
          );
        })}
        {bindings.length === 0 && sharedEntries.length === 0 ? <Text c="dimmed" size="xs" p="md">No runtime filters declared.</Text> : null}
      </Stack>
    </Paper>
  );
};

const chartValues = (facet: LoomFacetResult | undefined, column: string): Array<{ name: string; value: number }> => facetValues(facet, column).flatMap((entry) => {
  const value = Number(entry.count);
  return Number.isFinite(value) ? [{ name: entry.value, value }] : [];
});

export const ChartPanel = ({ binding, output, result }: { readonly binding: ExplorerRuntimeBindingV1; readonly output: ExplorerRuntimeOutputV1; readonly result?: LoomOutputResult }) => {
  const chartType = binding.type?.trim().toLowerCase() ?? '';
  const supported = ['bar', 'horizontalstacked', 'pie', 'fullpie', 'donut'].includes(chartType);
  if (!supported) return <Paper withBorder radius="md" p="md"><Text c="orange" size="sm">Unsupported chart type <code>{binding.type || 'unknown'}</code>.</Text></Paper>;
  const values = chartValues(result?.facets.find((facet) => facet.name === chartFacetName(output.outputId, binding.column)), binding.column);
  const pie = chartType === 'pie' || chartType === 'fullpie' || chartType === 'donut';
  const option: EChartsOption = pie
    ? { tooltip: { trigger: 'item' }, legend: { type: 'scroll', bottom: 0 }, series: [{ type: 'pie', radius: chartType === 'donut' ? ['42%', '70%'] : ['0%', '70%'], data: values }] }
    : { tooltip: { trigger: 'axis' }, grid: { left: 48, right: 18, top: 18, bottom: 52 }, xAxis: chartType === 'horizontalstacked' ? { type: 'value' } : { type: 'category', data: values.map((entry) => entry.name), axisLabel: { rotate: 25 } }, yAxis: chartType === 'horizontalstacked' ? { type: 'category', data: values.map((entry) => entry.name) } : { type: 'value' }, series: [{ name: binding.title ?? binding.column, type: 'bar', data: values.map((entry) => entry.value) }] };
  return (
    <Paper component="article" withBorder radius="md" p="md" className="min-w-0">
      <Text fw={700} size="sm">{binding.title ?? binding.label ?? binding.column}</Text>
      {values.length === 0 ? <Text c="dimmed" size="xs" mt="xs">No facet values are available for this chart.</Text> : <ReactECharts option={option} notMerge lazyUpdate style={{ height: 250, width: '100%' }} />}
    </Paper>
  );
};

export const OutputCharts = ({ output, result, visible }: { readonly output: ExplorerRuntimeOutputV1; readonly result?: LoomOutputResult; readonly visible: boolean }) => {
  if (!visible || output.charts.length === 0) return null;
  return <section className="mb-4 grid grid-cols-1 gap-3 xl:grid-cols-2" aria-label="Charts">{output.charts.map((binding, index) => <ChartPanel key={`${binding.column}-${index}`} binding={binding} output={output} result={result} />)}</section>;
};

export const OutputTable = ({ runtime, output, result, state, dispatch }: { readonly runtime: ExplorerRuntimeV1; readonly output: ExplorerRuntimeOutputV1; readonly result?: LoomOutputResult; readonly state: ViewerState; readonly dispatch: React.Dispatch<ViewerAction> }) => {
  const bindings = useMemo(() => output.table.columns.filter((binding) => binding.visible && output.columns.some((column) => column.column === binding.column && column.visible)), [output]);
  const tableData = useMemo(() => [...(result?.rows ?? [])], [result?.rows]);
  const tableColumns = useMemo<ColumnDef<ViewerRow>[]>(() => bindings.map((binding) => ({
    id: binding.column,
    accessorKey: binding.column,
    size: 180,
    header: binding.label ?? output.columns.find((column) => column.column === binding.column)?.label ?? binding.column,
    cell: (info) => binding.cellRenderer === 'fileActions' ? <FileCell value={info.row.original[binding.column]} row={info.row.original} runtime={runtime} /> : <Text size="xs" lineClamp={3} title={textFor(info.row.original[binding.column])}>{textFor(info.row.original[binding.column])}</Text>,
  })), [bindings, output.columns, runtime]);
  const table = useReactTable({ data: tableData, columns: tableColumns, getCoreRowModel: getCoreRowModel(), enableColumnPinning: true, initialState: { columnPinning: { left: bindings.filter((binding) => binding.pinned).map((binding) => binding.column) } } });
  const activeSort = activeOutputState(state, output.outputId).sort;

  return (
    <Paper withBorder radius="md" shadow="xs" className="overflow-hidden">
      <div className="overflow-x-auto">
        <Table aria-label={`${output.title} results`} highlightOnHover stickyHeader layout="fixed" className="loom-viewer-table" style={{ minWidth: table.getTotalSize() }}>
          <Table.Thead>{table.getHeaderGroups().map((headerGroup) => (
            <Table.Tr key={headerGroup.id}>{headerGroup.headers.map((header) => {
              const sortable = output.columns.find((column) => column.column === header.column.id)?.sortable;
              const sorted = activeSort?.column === header.column.id ? activeSort : undefined;
              const pinned = header.column.getIsPinned();
              return (
                <Table.Th key={header.id} aria-sort={sorted ? (sorted.desc ? 'descending' : 'ascending') : 'none'} data-pinned={pinned || undefined} style={{ width: header.getSize(), ...(pinned ? { left: header.column.getStart('left') } : {}) }}>
                  <UnstyledButton disabled={!sortable} fw={700} fz="xs" tt="uppercase" onClick={() => dispatch({ type: 'setSort', outputId: output.outputId, sort: sortable ? { column: header.column.id, desc: sorted ? !sorted.desc : false } : undefined })}>
                    {flexRender(header.column.columnDef.header, header.getContext())}{sorted ? (sorted.desc ? ' ↓' : ' ↑') : null}
                  </UnstyledButton>
                </Table.Th>
              );
            })}</Table.Tr>
          ))}</Table.Thead>
          <Table.Tbody>{table.getRowModel().rows.map((row) => (
            <Table.Tr key={row.id}>{row.getVisibleCells().map((cell, index) => {
              const pinned = cell.column.getIsPinned();
              const opensDetails = index === 0 && bindings.find((binding) => binding.column === cell.column.id)?.cellRenderer !== 'fileActions';
              return (
                <Table.Td key={cell.id} data-pinned={pinned || undefined} style={{ width: cell.column.getSize(), ...(pinned ? { left: cell.column.getStart('left') } : {}) }}>
                  {opensDetails
                    ? <UnstyledButton className="w-full text-left text-blue-700" onClick={() => dispatch({ type: 'showRowDetails', outputId: output.outputId, row: row.original })}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</UnstyledButton>
                    : flexRender(cell.column.columnDef.cell, cell.getContext())}
                </Table.Td>
              );
            })}</Table.Tr>
          ))}</Table.Tbody>
        </Table>
      </div>
      {(result?.rows.length ?? 0) === 0 ? <Text c="dimmed" ta="center" p="xl" size="sm">No rows match the current filters.</Text> : null}
    </Paper>
  );
};

export const PageControls = ({ output, result, state, dispatch }: { readonly output: ExplorerRuntimeOutputV1; readonly result?: LoomOutputResult; readonly state: ViewerState; readonly dispatch: React.Dispatch<ViewerAction> }) => {
  const outputState = activeOutputState(state, output.outputId);
  const page = outputState.cursorHistory.length;
  const setSize = (value: string | null) => {
    const numeric = Number(value);
    if (isPageSize(numeric)) dispatch({ type: 'setPageSize', outputId: output.outputId, pageSize: numeric });
  };
  return (
    <Paper component="nav" withBorder radius="md" px="sm" py={7} mb="xs" aria-label="Table controls">
      <Group justify="space-between" gap="sm" wrap="wrap">
        <Text c="dimmed" size="xs">
          Showing {(result?.rows.length ?? 0).toLocaleString()}{result?.totalCount === null || result?.totalCount === undefined ? ' rows' : ` of ${result.totalCount.toLocaleString()} rows`}
        </Text>
        <Group gap="xs" wrap="nowrap">
          <Button variant="default" size="compact-sm" disabled={page <= 1} onClick={() => dispatch({ type: 'previousPage', outputId: output.outputId })}>Previous</Button>
          <Text size="xs" className="whitespace-nowrap tabular-nums">Page {page}</Text>
          <Button variant="default" size="compact-sm" disabled={!result?.pageInfo.hasNextPage || !result.pageInfo.endCursor} onClick={() => { if (result?.pageInfo.endCursor) dispatch({ type: 'nextPage', outputId: output.outputId, cursor: result.pageInfo.endCursor }); }}>Next</Button>
          <Text c="dimmed" size="xs" ml="xs" className="whitespace-nowrap">Rows</Text>
          <Select aria-label="Rows per page" size="xs" w={68} data={PAGE_SIZES.map(String)} value={String(outputState.pageSize)} onChange={setSize} allowDeselect={false} />
        </Group>
      </Group>
    </Paper>
  );
};
