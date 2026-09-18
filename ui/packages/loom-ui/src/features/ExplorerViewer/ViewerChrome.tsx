import React from 'react';
import { Badge, Button, CloseButton, Group, Paper, Stack, Text } from '@mantine/core';
import type { LoomOutputResult } from '../../api';
import type { ExplorerRuntimeOutputV1, ExplorerRuntimeV1 } from '../../types';
import { activeOutputState, type ViewerState } from './model';
import type { ViewerAction } from './reducer';
import { filterLabel } from './serialization';

export const QuerySummary = ({ output, runtime, state, result, dispatch }: { readonly output: ExplorerRuntimeOutputV1; readonly runtime: ExplorerRuntimeV1; readonly state: ViewerState; readonly result?: LoomOutputResult; readonly dispatch: React.Dispatch<ViewerAction> }) => {
  const outputState = activeOutputState(state, output.outputId);
  const fixed = Object.entries(output.fixedFilters).flatMap(([column, selected]) => selected.map((value) => ({ kind: 'fixed' as const, column, value })));
  const local = Object.entries(outputState.filterValues).flatMap(([column, selected]) => selected.map((value) => ({ kind: 'local' as const, column, value })));
  const shared = Object.entries(state.sharedFilters).flatMap(([name, selected]) => selected.map((value) => ({ kind: 'shared' as const, name, value })));
  const chips = [...fixed, ...local, ...shared];
  const sort = activeOutputState(state, output.outputId).sort;

  return (
    <Paper withBorder radius="md" p="sm" className="mb-3">
      <Group justify="space-between" align="flex-start" gap="sm">
        <Stack gap={1}>
          <Text fw={700} size="sm">{output.rowLabel || output.title}</Text>
          <Text c="dimmed" size="xs">
            {result?.totalCount === null || result?.totalCount === undefined
              ? 'Published result'
              : `${result.totalCount.toLocaleString()} matching rows`}
          </Text>
        </Stack>
        {sort ? <Text c="dimmed" size="xs">Sorted by {sort.column}</Text> : null}
      </Group>
      {chips.length > 0 ? (
        <Group gap="xs" mt="sm" aria-label="Active filters">
          {chips.map((chip) => (
            <Badge
              key={`${chip.kind}-${chip.kind === 'shared' ? chip.name : chip.column}-${chip.value}`}
              variant="light"
              radius="xl"
              tt="none"
              rightSection={chip.kind === 'fixed' ? undefined : <CloseButton size={14} aria-label={`Remove ${chip.value}`} onClick={() => chip.kind === 'local' ? dispatch({ type: 'setFilter', outputId: output.outputId, column: chip.column, values: [] }) : dispatch({ type: 'setSharedFilter', name: chip.name, values: [] })} />}
            >
              {chip.kind === 'shared' ? chip.name : filterLabel(output.filters.find((binding) => binding.column === chip.column) ?? { column: chip.column })}: {chip.value}
            </Badge>
          ))}
        </Group>
      ) : <Text c="dimmed" size="xs" mt="xs">No filters applied</Text>}
    </Paper>
  );
};

export const ChartToggle = ({ outputId, visible, dispatch }: { readonly outputId: string; readonly visible: boolean; readonly dispatch: React.Dispatch<ViewerAction> }) => (
  <Button variant="subtle" size="compact-sm" aria-pressed={visible} onClick={() => dispatch({ type: 'toggleCharts', outputId })}>
    {visible ? 'Hide charts' : 'Show charts'}
  </Button>
);

export const QualitySummary = ({ output, runtime }: { readonly output: ExplorerRuntimeOutputV1; readonly runtime: ExplorerRuntimeV1 }) => {
  const report = runtime.qualityReports?.find((candidate) => candidate.output === output.outputId || candidate.output === output.name);
  if (!report) return null;
  const affectedColumns = report.columns.filter((column) => column.missing + column.recordedNull + column.emptyArray > 0);
  const absentCells = affectedColumns.reduce((total, column) => total + column.missing + column.recordedNull + column.emptyArray, 0);
  return (
    <Paper component="section" withBorder radius="md" p="sm" mb="sm" aria-label="Dataset quality">
      <Group justify="space-between" align="flex-start" gap="sm" wrap="wrap">
        <Stack gap={1}>
          <Text fw={700} size="sm">Dataset quality</Text>
          <Text c="dimmed" size="xs">
            {report.rowCount.toLocaleString()} rows checked across the complete published output
          </Text>
        </Stack>
        <Badge color={report.verdict === 'PASSED' && report.completeness === 'COMPLETE' ? 'green' : 'orange'} variant="light">
          {report.completeness === 'COMPLETE' ? 'Complete evidence' : 'Incomplete evidence'}
        </Badge>
      </Group>
      <Text size="xs" mt="xs">
        {affectedColumns.length === 0
          ? `All ${report.columns.length.toLocaleString()} feature columns are populated.`
          : `${absentCells.toLocaleString()} absent values appear across ${affectedColumns.length.toLocaleString()} feature columns.`}
      </Text>
      {report.keyIntegrity.missing > 0 || report.keyIntegrity.duplicate > 0 ? (
        <Text c="orange" size="xs" mt={4}>
          Row identity needs attention: {report.keyIntegrity.missing.toLocaleString()} missing and {report.keyIntegrity.duplicate.toLocaleString()} duplicate keys.
        </Text>
      ) : null}
    </Paper>
  );
};
