// @vitest-environment jsdom
import React from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCatalog, ConstructionStep } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { RowRecipeExpression, type RowRecipeExpressionProps } from './RowRecipeExpression';

// Exercise the actual workspace callback through a real source-column term.
// Outputs of row-preserving steps are inventories, not proof of column ownership.
const workspaceSource = readFileSync(resolve(process.cwd(), 'src/features/ExplorerBuilder/BuilderWorkspace.tsx'), 'utf8');
const marker = 'onEditColumn={(columnId) => {';
const callbackStart = workspaceSource.indexOf(marker) + marker.length;
const callbackEnd = workspaceSource.indexOf('\n                  }}', callbackStart);
if (callbackStart < marker.length || callbackEnd < callbackStart) throw new Error('Row source-column callback was not found.');
const makeColumnEditor = new Function('table', 'editConstructionStep', 'savedNetwork', 'relationshipOwner', 'sourceStepEditAction',
  'recipeMutationAllowed', 'rememberRecipeFocus', 'recipeOwner', 'recipeEditToken', 'setEditingRecipeColumn',
  `return (columnId) => {${workspaceSource.slice(callbackStart, callbackEnd)}};`) as (
    table: DraftTable, edit: (id: string) => void, network: undefined, owner: string, sourceAction: { current?: (id: string) => void },
    allowed: () => boolean, focus: () => void, recipeOwner: { begin: (target: {kind: 'column'; columnId: string}) => object },
    token: { current?: object }, inspect: (value: {owner: string; columnId: string}) => void,
  ) => (columnId: string) => void;
const score = { id: 'score-id', name: 'score', label: 'Score', type: 'decimal' };
const steps: ConstructionStep[] = [{ id: 'filter-step', inputs: [{kind: 'SOURCE_PROJECTION'}],
  operation: {kind: 'FILTER', filter: {columnId: score.id, operator: 'EXISTS'}}, outputs: [score] },
{ id: 'derive-step', inputs: [{kind: 'STEP_OUTPUT', stepId: 'filter-step'}], operation: {kind: 'DERIVE', derive: {
  constructionId: 'calculation', outputColumnId: 'calculated-id', operation: 'ADD', left: {kind: 'COLUMN', columnId: score.id},
  right: {kind: 'LITERAL', literal: {kind: 'INTEGER', integer: 1}}, missingInputPolicy: 'PROPAGATE_NULL',
}}, outputs: [score, {id: 'calculated-id', name: 'calculated', label: 'Calculated score', type: 'decimal'}] }];
const table: DraftTable = {outputId: 'observations', tabId: 'observations', title: 'Observations', document: {
  kind: 'ExplorerBuilderDocument', output: {id: 'observations', title: 'Observations'}, rootResourceType: 'Observation',
  route: {occurrenceId: 'base', resourceType: 'Observation'}, rows: {kind: 'RECORDS', records: {}},
  columns: [{columnId: score.id, column: score.name, label: score.label, occurrenceId: 'base', source: {kind: 'field', field: {path: 'valueInteger', projectionMode: 'VALUE'}}}],
  construction: {version: 1, steps},
}};
const catalog = {nodes: [{nodeId: 'observation-node', resourceType: 'Observation', rowRootEligible: true}], edges: [], routePolicy: {}} as unknown as ExplorerBuilderCatalog;
afterEach(cleanup);
it('edits a source value policy at its source identity despite downstream pass-through outputs', () => {
  const before = JSON.stringify(table);
  const editConstructionStep = vi.fn(), begin = vi.fn().mockReturnValue({requestId: 1}), inspect = vi.fn();
  const onEditColumn = makeColumnEditor(table, editConstructionStep, undefined, 'saved-owner', {}, () => true, vi.fn(), {begin}, {}, inspect);
  const props: RowRecipeExpressionProps = {table, catalog, projectionKind: 'saved', onEditColumn, onEditOperation: editConstructionStep,
    onEditSourceRows: vi.fn(), onAddConnection: vi.fn(), onSetMatchMode: vi.fn(), onEditImportedSourceStep: vi.fn(),
    onAddRelatedValues: vi.fn(), onAddRelatedRows: vi.fn(), onAddGroup: vi.fn()};
  render(<RowRecipeExpression {...props} />);
  fireEvent.click(screen.getByRole('button', {name: 'Edit column Score: one scalar value'}));
  expect(editConstructionStep).not.toHaveBeenCalled();
  expect(begin).toHaveBeenCalledWith({kind: 'column', columnId: 'score-id'});
  expect(inspect).toHaveBeenCalledWith({owner: 'saved-owner', columnId: 'score-id'});
  expect(JSON.stringify(table)).toBe(before);
});
it('keeps downstream operations editable through their own ordered terms', () => {
  const edit = vi.fn();
  render(<RowRecipeExpression table={table} catalog={catalog} projectionKind="saved" onEditColumn={vi.fn()} onEditOperation={edit}
    onEditSourceRows={vi.fn()} onAddConnection={vi.fn()} onSetMatchMode={vi.fn()} onEditImportedSourceStep={vi.fn()}
    onAddRelatedValues={vi.fn()} onAddRelatedRows={vi.fn()} onAddGroup={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', {name: /^Edit operation 2: derive:/}));
  expect(edit).toHaveBeenCalledWith('derive-step');
});
