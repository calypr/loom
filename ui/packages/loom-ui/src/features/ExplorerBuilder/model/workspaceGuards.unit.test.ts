import { describe, expect, it } from 'vitest';
import {
  hasCommandSelectedTable,
  hasPairedPreviewRows,
  isCompilerNavigationSelection,
} from './workspaceGuards';

describe('workspace guards', () => {
  it('requires a ready preview with a candidate column and rows', () => {
    expect(hasPairedPreviewRows('READY', 'column-1', [{ id: 1 }])).toBe(true);
    expect(hasPairedPreviewRows('NEEDS_REPAIR', 'column-1', [{ id: 1 }])).toBe(false);
    expect(hasPairedPreviewRows('READY', undefined, [{ id: 1 }])).toBe(false);
    expect(hasPairedPreviewRows('READY', 'column-1', undefined)).toBe(false);
    expect(hasPairedPreviewRows('READY', 'column-1', null)).toBe(false);
  });

  it('remembers only a command-selected table with an output identity', () => {
    expect(hasCommandSelectedTable(true, 'output-1')).toBe(true);
    expect(hasCommandSelectedTable(false, 'output-1')).toBe(false);
    expect(hasCommandSelectedTable(true, undefined)).toBe(false);
    expect(hasCommandSelectedTable(true, null)).toBe(false);
  });

  it('treats route and occurrence selections as compiler navigation', () => {
    expect(isCompilerNavigationSelection({ kind: 'compiler-route', nodeId: 'node', referenceId: 'ref', routeToken: 'route' })).toBe(true);
    expect(isCompilerNavigationSelection({ kind: 'compiler-occurrence', nodeId: 'node', referenceId: 'ref', occurrenceId: 'occ' })).toBe(true);
    expect(isCompilerNavigationSelection({ kind: 'catalog-node', nodeId: 'node' })).toBe(false);
    expect(isCompilerNavigationSelection({ kind: 'authored-occurrence', nodeId: 'node', occurrenceId: 'occ' })).toBe(false);
  });
});
