import { constructionHistorySteps } from '../constructionWorkspace/constructionHistory';
import type { AuthoringNetworkResponse, ConstructionStep, ExplorerBuilderColumn, ExplorerBuilderDocument, ExplorerBuilderRouteNode } from '../../../types';

export interface RowConnectionTerm {
  readonly occurrenceId: string;
  readonly parentOccurrenceId?: string;
  readonly resourceType: string;
  readonly path: string;
  readonly relationship?: string;
  readonly catalogEdgeId?: string;
  readonly matchMode: 'OPTIONAL' | 'REQUIRED';
}

/** Display-only traversal of the complete authoritative document, including unknown catalog paths. */
export function rowConnectionTerms(route: ExplorerBuilderRouteNode): readonly RowConnectionTerm[] {
  const terms: RowConnectionTerm[] = [];
  const walk = (node: ExplorerBuilderRouteNode, path: string, parentOccurrenceId?: string) => {
    const currentPath = path ? `${path} → ${node.resourceType}${node.relationship ? ` (${node.relationship})` : ''}` : node.resourceType;
    terms.push({ occurrenceId: node.occurrenceId, parentOccurrenceId, resourceType: node.resourceType, path: currentPath,
      relationship: node.relationship, catalogEdgeId: node.catalogEdgeId, matchMode: node.matchMode ?? 'OPTIONAL' });
    node.children?.forEach((child) => walk(child, currentPath, node.occurrenceId));
  };
  walk(route, '');
  return terms;
}

const policyNames: Readonly<Record<string, string>> = {
  ALL: 'all values as a list', DISTINCT: 'distinct values as a list', FIRST: 'first value',
  VALUE: 'one scalar value', ONE: 'one distinct value', AUTO: 'automatic source policy',
  COUNT: 'count of matches', PRESENCE: 'whether a match exists',
};
export const rowValuePolicyLabel = (policy: string) => policyNames[policy] ?? policy;
export const emptyMatchLabel = (policy: string) => ({
  PRESERVE_PARENT: 'keep the input row', EXCLUDE: 'exclude the input row', ERROR: 'report an error',
  GROUP: 'keep a missing-key group', GROUP_AS_MISSING: 'keep a missing-key group',
  GROUP_AS_UNASSIGNED: 'keep an unassigned group',
}[policy] ?? policy);

export function columnPolicy(column: ExplorerBuilderColumn, network?: AuthoringNetworkResponse['network']): {
  readonly label: string; readonly sourceStepId?: string;
} {
  const binding = network?.bindings.find((candidate) => candidate.columnId === (column.columnId ?? column.column));
  const source = column.source;
  const recordSelection = source.kind === 'field' && source.field.relatedSelection
    ? ` from first related record (${source.field.relatedSelection.acknowledged ? 'acknowledged' : 'acknowledgement required'})` : '';
  if (binding?.status === 'EXACT' && binding.valueMode) return { label: `${rowValuePolicyLabel(binding.valueMode)}${recordSelection}`, sourceStepId: binding.sourceStepId };
  if (source.kind === 'field') return { label: `${rowValuePolicyLabel(source.field.projectionMode ?? 'AUTO')}${recordSelection}` };
  if ('lookup' in source) return { label: rowValuePolicyLabel(source.lookup.projectionMode ?? 'AUTO') };
  if (source.kind === 'imported') return { label: 'pinned source policy; provenance unavailable', sourceStepId: binding?.status === 'EXACT' ? binding.sourceStepId : undefined };
  return { label: source.kind === 'projectId' ? 'project identifier' : 'configured source policy' };
}

export function sourceRowExpression(document: ExplorerBuilderDocument): string {
  const rows = document.rows;
  if (rows.kind === 'RECORDS') return `One row per ${document.rootResourceType} record`;
  if (rows.kind === 'EXPANDED') {
    const occurrence = rowConnectionTerms(document.route).find((term) => term.occurrenceId === rows.expanded.occurrenceId);
    return `One row per ${rows.expanded.scopePath} item within ${occurrence?.path ?? 'the saved occurrence'}; when empty, ${emptyMatchLabel(rows.expanded.emptyCollectionPolicy)}`;
  }
  return rows.groups.source.kind === 'EXPLICIT'
    ? `One row per named source group; unassigned members: ${emptyMatchLabel(rows.groups.source.explicit.unassignedMemberPolicy)}`
    : `One row per source key ${rows.groups.source.field.fieldPath}; missing key: ${emptyMatchLabel(rows.groups.source.field.missingKeyPolicy)}`;
}

export function expressionColumnLabels(document: ExplorerBuilderDocument): ReadonlyMap<string, string> {
  const labels = new Map<string, string>();
  document.columns.forEach((column) => { labels.set(column.columnId ?? column.column, column.label); labels.set(column.column, column.label); });
  document.construction?.sourceProjections?.forEach((column) => labels.set(column.columnId, column.label));
  document.construction?.steps.forEach((step) => step.outputs.forEach((column) => labels.set(column.id, column.label)));
  return labels;
}

export function operationExpression(step: ConstructionStep, labels: ReadonlyMap<string, string>): {
  readonly title: string; readonly clauses: readonly string[];
} {
  const label = (id: string) => labels.get(id) ?? `Column ${id}`;
  const op = step.operation;
  switch (op.kind) {
    case 'GROUP': return {
      title: `Group rows by (${(op.group.keys ?? []).map((key) => 'inputColumnId' in key ? label(key.inputColumnId) : label(key.outputColumnId)).join(', ') || 'no keys: one group'})`,
      clauses: [
        `Missing keys: ${emptyMatchLabel(op.group.missingKeyPolicy ?? 'GROUP')}`,
        `Summaries: ${(op.group.aggregates ?? []).map((aggregate) => `${aggregate.operation.toLowerCase().replaceAll('_', ' ')}${'inputColumnId' in aggregate ? ` of ${label(aggregate.inputColumnId)}` : ''} as ${label(aggregate.outputColumnId)}`).join('; ') || 'none'}`,
        `Retained values: ${(step.rowValues ?? []).map((value) => `${'inputColumnId' in value ? label(value.inputColumnId) : label(value.outputColumnId)} as ${rowValuePolicyLabel(value.policy)}`).join('; ') || 'none'}`,
      ],
    };
    case 'RELATED_EXPAND': return { title: `One row per input–${op.relatedExpand.targetResourceType} pair`, clauses: [
      `Anchor: ${label(op.relatedExpand.anchorColumnId)}`,
      `Path: ${op.relatedExpand.route.map((edge) => `${edge.fromResourceType} → ${edge.toResourceType} (${edge.relationship})`).join('; ')}`,
      `When no related record: ${emptyMatchLabel(op.relatedExpand.emptyPolicy)}`,
      'Existing list columns stay lists on every pair. Choose a separate column explicitly for a per-match scalar.',
    ] };
    case 'RELATED_SOURCE': return { title: `Include ${label(op.relatedSource.outputColumnId)} as ${rowValuePolicyLabel(op.relatedSource.form)}`, clauses: [
      `Rows preserved; anchor: ${label(op.relatedSource.anchorColumnId)}`,
      `Path: ${op.relatedSource.route.map((edge) => `${edge.fromResourceType} → ${edge.toResourceType} (${edge.relationship})`).join('; ') || 'current occurrence'}`,
      ...(op.relatedSource.rowValuePolicy ? [`Contributor value policy: ${rowValuePolicyLabel(op.relatedSource.rowValuePolicy)}`] : []),
    ] };
    case 'EXPAND': return { title: `One row per item in ${label(op.expand.inputColumnId)}`, clauses: [`When empty: ${emptyMatchLabel(op.expand.emptyPolicy ?? 'ERROR')}`] };
    case 'FILTER': return { title: `Keep rows where ${label(op.filter.columnId)} ${op.filter.operator.toLowerCase().replaceAll('_', ' ')}`, clauses: ['May exclude rows; preserves the identity of retained rows.'] };
    case 'RELATED_ELIGIBILITY': return { title: `Require related ${op.relatedEligibility.targetResourceType}: ${op.relatedEligibility.match.kind.toLowerCase().replaceAll('_', ' ')}`, clauses: ['Filters input rows without expanding them.', `Anchor: ${label(op.relatedEligibility.anchorColumnId)}`] };
    default: {
      const summary = constructionHistorySteps({ version: 1, steps: [step] }, [...labels].map(([id, text]) => ({ id, name: id, label: text })))[0];
      const output = op.kind === 'DERIVE' ? label(op.derive.outputColumnId) : op.kind === 'RELATED_FIELD' ? label(op.relatedField.outputColumnId) : undefined;
      return { title: `${summary?.title ?? op.kind.toLowerCase().replaceAll('_', ' ')}${output ? `: ${output}` : ''}`,
        clauses: [summary?.summary ?? 'Retained in execution order; edit using its existing inspector.'] };
    }
  }
}

/** Compact notation prototype built from the same document as the editable clause layout. */
export function rowRecipeSentence(document: ExplorerBuilderDocument, network?: AuthoringNetworkResponse['network']): string {
  const connections = rowConnectionTerms(document.route).filter((term) => term.parentOccurrenceId);
  const labels = expressionColumnLabels(document);
  return [sourceRowExpression(document),
    connections.length ? `connected through ${connections.map((term) => `${term.path} (${term.matchMode === 'REQUIRED' ? 'match required' : 'keep unmatched input'})`).join('; ')}` : 'no related connections',
    document.columns.length ? `include ${document.columns.map((column) => `${column.label} as ${columnPolicy(column, network).label}`).join('; ')}` : 'no selected columns',
    ...(document.construction?.steps ?? []).map((step) => {
      const expression = operationExpression(step, labels);
      return `${expression.title}; ${expression.clauses.join('; ')}`;
    }),
  ].join('. ');
}
