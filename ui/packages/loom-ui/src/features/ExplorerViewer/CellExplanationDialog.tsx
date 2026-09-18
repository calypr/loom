import React from 'react';
import { Alert, Badge, Button, Group, Loader, Modal, Paper, Stack, Text } from '@mantine/core';
import type { CellTrace, CellTraceContribution } from '../../cellTrace';
import { displayValue } from '../../valueDisplay';

export interface CellExplanationCoordinate {
  readonly outputId: string;
  readonly rowId: string;
  readonly column: string;
  readonly label: string;
  readonly displayedValue: unknown;
}

const explanationFor = (trace: CellTrace): string => {
  switch (trace.status) {
    case 'VALUE':
      if (trace.omissionCode && trace.contributions.length === 0) {
        return 'Loom reproduced this value, but detailed source records are unavailable for this feature shape.';
      }
      return trace.contributions.length === 1
        ? 'One source record supplied this value.'
        : `${trace.contributions.length.toLocaleString()} source records contributed before Loom applied the feature rule.`;
    case 'NO_MATCH':
      return 'No authorized source record matched this feature’s relationship and matching rules.';
    case 'RECORDED_NULL':
      return 'A matching source record exists, but its selected value is empty.';
    case 'AMBIGUOUS':
      return 'More than one source value matched. Review the feature rule before using this value for training.';
    case 'INCOMPLETE':
      return 'Loom reached its evidence limit before locating this row. This result does not mean the source value is absent.';
    default: {
      const exhaustive: never = trace;
      return exhaustive;
    }
  }
};

const repairLabel = (trace: CellTrace): string | undefined => {
  switch (trace.status) {
    case 'NO_MATCH': return 'Review matching rules';
    case 'RECORDED_NULL': return 'Review null handling';
    case 'AMBIGUOUS': return 'Resolve matching rule';
    case 'VALUE':
    case 'INCOMPLETE': return undefined;
    default: {
      const exhaustive: never = trace;
      return exhaustive;
    }
  }
};

export const CellExplanationDialog = ({
  coordinate,
  trace,
  contributions,
  loading,
  error,
  onClose,
  onLoadMore,
  onRepair,
}: {
  readonly coordinate?: CellExplanationCoordinate;
  readonly trace?: CellTrace;
  readonly contributions?: ReadonlyArray<CellTraceContribution>;
  readonly loading: boolean;
  readonly error?: string;
  readonly onClose: () => void;
  readonly onLoadMore: () => void;
  readonly onRepair?: (coordinate: CellExplanationCoordinate, status: CellTrace['status']) => void;
}) => (
  <Modal
    opened={Boolean(coordinate)}
    onClose={onClose}
    title={coordinate ? `Why is ${coordinate.label} ${displayValue(coordinate.displayedValue)}?` : 'Cell explanation'}
    closeButtonProps={{ 'aria-label': 'Close cell explanation' }}
    centered
    size="lg"
  >
    {coordinate ? (
      <Stack gap="sm">
        <Text c="dimmed" size="xs">Published row {coordinate.rowId}</Text>
        {loading && !trace ? <Group gap="xs" role="status"><Loader size="xs" /><Text size="sm">Tracing this value to its source…</Text></Group> : null}
        {error ? <Alert color="red" title="Explanation unavailable">{error}</Alert> : null}
        {trace ? (
          <>
            <Group justify="space-between" align="flex-start">
              <Stack gap={2}>
                <Text fw={700}>{displayValue(trace.value)}</Text>
                <Text size="sm">{explanationFor(trace)}</Text>
              </Stack>
              <Badge color={trace.status === 'VALUE' ? 'green' : trace.status === 'INCOMPLETE' ? 'orange' : 'blue'} variant="light">
                {trace.status.replaceAll('_', ' ').toLowerCase()}
              </Badge>
            </Group>
            {(contributions ?? trace.contributions).length > 0 ? (
              <details>
                <summary className="cursor-pointer text-sm font-semibold text-blue-700">Source details ({(contributions ?? trace.contributions).length})</summary>
                <Stack gap="xs" mt="xs">
                  {(contributions ?? trace.contributions).map((contribution, index) => (
                    <Paper withBorder p="xs" key={`${contribution.resourceType ?? 'resource'}-${contribution.resourceId ?? index}-${index}`}>
                      <Text size="xs" fw={700}>{contribution.resourceType || 'FHIR resource'}{contribution.resourceId ? ` / ${contribution.resourceId}` : ''}</Text>
                      <Text size="xs" c="dimmed" mt={2}>{displayValue(contribution.value)}</Text>
                    </Paper>
                  ))}
                </Stack>
              </details>
            ) : null}
            <Group justify="flex-end">
              {onRepair && repairLabel(trace) ? (
                <Button variant="default" onClick={() => onRepair(coordinate, trace.status)}>{repairLabel(trace)}</Button>
              ) : null}
              {trace.hasMore ? <Button variant="light" loading={loading} onClick={onLoadMore}>Load more source details</Button> : null}
            </Group>
          </>
        ) : null}
      </Stack>
    ) : null}
  </Modal>
);
