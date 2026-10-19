import type { LoomClient } from '../../../api';
import type {
  TableShapeCapabilities,
  TableShapeCategoryDiscovery,
  TableShapeProposal as TableShapeProposalResult,
  TableShapeProposalIntent as WireTableShapeProposalIntent,
  TableShapeResolution,
  TableShapeResolutionRequest,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import type {
  DerivedColumnProposal,
  DerivedOperandIntent,
  PivotCategoryPair,
  TableShapeEditorChoices,
  TableShapeProposalIntent,
} from './tableShapeModel';

export interface EditorScope {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly outputId: string;
}

export interface EditorSession {
  readonly id: number;
  readonly contextKey: string;
  readonly scope: EditorScope;
  readonly catalogId: string;
  readonly choices: TableShapeEditorChoices;
  readonly savedProposalIntent: TableShapeProposalIntent;
  readonly savedShapeExists: boolean;
  readonly savedSupportNotice?: string;
}

export interface TableShapeControllerClient extends Pick<LoomClient,
  'resolveTableShape' | 'proposeTableShape'> {}

export const scopeFromPanelProps = (props: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly table: DraftTable;
}): EditorScope => ({
  project: props.project,
  explorerId: props.explorerId,
  ...(props.authResourcePath ? { authResourcePath: props.authResourcePath } : {}),
  snapshotToken: props.snapshotToken,
  expectedDraftVersion: props.draftVersion,
  expectedDraftDigest: props.draftDigest,
  outputId: props.table.outputId,
});

export const contextKeyFor = (scope: EditorScope): string => JSON.stringify([
  scope.project,
  scope.explorerId,
  scope.authResourcePath ?? '',
  scope.snapshotToken,
  scope.expectedDraftVersion,
  scope.expectedDraftDigest,
  scope.outputId,
]);

export const scopeRequest = (scope: EditorScope) => ({
  project: scope.project,
  explorerId: scope.explorerId,
  ...(scope.authResourcePath ? { authResourcePath: scope.authResourcePath } : {}),
  snapshotToken: scope.snapshotToken,
  expectedDraftVersion: scope.expectedDraftVersion,
  expectedDraftDigest: scope.expectedDraftDigest,
  outputId: scope.outputId,
});

const outputSupportFor = <Kind extends 'unpivotKeyOutput' | 'unpivotValueOutput'>(
  support: TableShapeCapabilities['unpivotKeyOutput'],
  choiceKind: Kind,
) => support.kind === 'unsupported'
  ? { kind: 'unsupported' as const, reason: support.reason }
  : {
      kind: 'supported' as const,
      resultTypeLabel: support.resultTypeLabel,
      suggestions: support.suggestions.map((suggestion) => ({ ...suggestion, choiceKind })),
    };

export const editorChoicesFromCapabilities = (capabilities: TableShapeCapabilities): TableShapeEditorChoices => ({
  reshapeModes: capabilities.reshapeModes,
  groupColumns: capabilities.groupColumns,
  categoryColumns: capabilities.categoryColumns,
  valueColumns: capabilities.valueColumns,
  pivotCategoryDiscovery: capabilities.pivotCategoryDiscovery.kind === 'not-requested'
    ? { kind: 'not-requested' }
    : {
        kind: 'complete',
        discoveryIdentity: capabilities.pivotCategoryDiscovery.discoveryIdentity,
        pair: capabilities.pivotCategoryDiscovery.pair,
        categories: capabilities.pivotCategoryDiscovery.categories,
      },
  duplicatePolicies: capabilities.duplicatePolicies,
  missingCellPolicies: capabilities.missingCellPolicies,
  unlistedCategoryPolicies: capabilities.unlistedCategoryPolicies,
  unpivotColumns: capabilities.unpivotColumns,
  unpivotKeyOutput: outputSupportFor(capabilities.unpivotKeyOutput, 'unpivotKeyOutput'),
  unpivotValueOutput: outputSupportFor(capabilities.unpivotValueOutput, 'unpivotValueOutput'),
  unpivotNullRowPolicies: capabilities.unpivotNullRowPolicies,
  derivedAvailability: capabilities.derivedAvailability,
  unpivotWithDerivedAvailability: capabilities.unpivotWithDerivedAvailability,
  derivedOutputSuggestions: capabilities.derivedOutputSuggestions,
  binaryOperators: capabilities.binaryOperators,
  operands: capabilities.operands,
  missingInputPolicies: capabilities.missingInputPolicies,
  divisionByZeroPolicies: capabilities.divisionByZeroPolicies,
});

const nonEmpty = <Value,>(values: ReadonlyArray<Value>): readonly [Value, ...Value[]] | undefined => {
  const first = values[0];
  if (first === undefined) return undefined;
  const rest: Value[] = [];
  for (let index = 1; index < values.length; index += 1) {
    const value = values[index];
    if (value !== undefined) rest.push(value);
  }
  return [first, ...rest];
};

const operandIntentFromWire = (operand: WireTableShapeProposalIntent['derivedColumns'][number]['leftOperand']): DerivedOperandIntent => {
  switch (operand.kind) {
    case 'base':
      return { kind: 'base', reference: operand.reference };
    case 'derived':
      return { kind: 'derived', localId: operand.localId };
    case 'pivotOutput':
      return { kind: 'pivotOutput', reference: operand.reference };
    case 'literal':
      return { kind: 'literal', representation: operand.representation, text: operand.text };
    default: {
      const exhaustive: never = operand;
      return exhaustive;
    }
  }
};

const derivedColumnFromWire = (
  column: WireTableShapeProposalIntent['derivedColumns'][number],
): DerivedColumnProposal => ({
  localId: column.localId,
  output: column.output,
  operator: column.operator,
  leftOperand: operandIntentFromWire(column.leftOperand),
  rightOperand: operandIntentFromWire(column.rightOperand),
  missingInputPolicy: column.missingInputPolicy,
  ...(column.divisionByZeroPolicy ? { divisionByZeroPolicy: column.divisionByZeroPolicy } : {}),
});

export const editorIntentFromWire = (intent: WireTableShapeProposalIntent): TableShapeProposalIntent => {
  const derivedColumns = intent.derivedColumns.map(derivedColumnFromWire);
  if (intent.kind === 'NONE') {
    return { kind: 'NONE', reshapeMode: intent.reshapeMode, derivedColumns };
  }
  if (intent.kind === 'GROUPED_PIVOT') {
    const groupColumns = nonEmpty(intent.pivot.groupColumns);
    const includedCategories = nonEmpty(intent.pivot.includedCategories.map((category) => ({
      category: category.category,
      output: category.output,
    })));
    if (!groupColumns || !includedCategories) {
      throw new Error('The saved pivot has no group columns or categories. Reload the table shape settings.');
    }
    return {
      kind: 'GROUPED_PIVOT',
      reshapeMode: intent.reshapeMode,
      pivot: {
        groupColumns,
        categoryColumn: intent.pivot.categoryColumn,
        valueColumn: intent.pivot.valueColumn,
        categoryDiscoveryIdentity: intent.pivot.categoryDiscoveryIdentity,
        includedCategories,
        duplicatePolicy: intent.pivot.duplicatePolicy,
        missingCellPolicy: intent.pivot.missingCellPolicy,
        unlistedCategoryPolicy: intent.pivot.unlistedCategoryPolicy,
      },
      derivedColumns,
    };
  }
  const inputColumns = nonEmpty(intent.unpivot.inputColumns);
  if (!inputColumns) throw new Error('The saved unpivot has no selected inputs. Reload the table shape settings.');
  return {
    kind: 'UNPIVOT',
    reshapeMode: intent.reshapeMode,
    unpivot: {
      inputColumns,
      keyOutput: intent.unpivot.keyOutput,
      valueOutput: intent.unpivot.valueOutput,
      nullRowPolicy: intent.unpivot.nullRowPolicy,
    },
    derivedColumns,
  };
};

export const pivotPairMatches = (
  discovery: TableShapeCategoryDiscovery,
  pair: PivotCategoryPair,
): boolean => discovery.pair.categoryColumn.choiceId === pair.categoryColumn.choiceId &&
  discovery.pair.valueColumn.choiceId === pair.valueColumn.choiceId;

export const proposalMatches = (
  proposal: TableShapeProposalResult,
  session: EditorSession,
): boolean => proposal.outputId === session.scope.outputId &&
  proposal.snapshotToken === session.scope.snapshotToken &&
  proposal.draftVersion === session.scope.expectedDraftVersion &&
  proposal.draftDigest === session.scope.expectedDraftDigest;

export const savedShapeExists = (intent: TableShapeProposalIntent): boolean =>
  intent.kind !== 'NONE' || intent.derivedColumns.length > 0;

type TableShapeOperandSelection = Extract<TableShapeResolutionRequest, { readonly kind: 'DERIVED' }>['derived']['left'];

export const tableShapeOperandSelectionFor = (
  operand: DerivedOperandIntent,
  derivedIds: ReadonlyMap<string, string>,
  pivot: TableShapeResolution | undefined,
): TableShapeOperandSelection => {
  switch (operand.kind) {
    case 'base':
      if (!operand.reference) throw new Error('A base operand selection is incomplete.');
      return { kind: 'CATALOG_CHOICE', choiceId: operand.reference.choiceId };
    case 'derived': {
      const resolutionId = derivedIds.get(operand.localId);
      if (!resolutionId) throw new Error(`The earlier derived column ${operand.localId} has not resolved.`);
      return { kind: 'RESOLUTION_OUTPUT', resolutionId };
    }
    case 'pivotOutput': {
      if (!pivot || !operand.reference) throw new Error('A pivot output operand has no matching pivot resolution.');
      const descriptor = pivot.outputDescriptors.find((candidate) => {
        if (operand.reference.kind === 'group') {
          return candidate.kind === 'group' && candidate.groupColumn.choiceId === operand.reference.column.choiceId;
        }
        return candidate.kind === 'category' && candidate.category.choiceId === operand.reference.category.choiceId;
      });
      if (!descriptor || (descriptor.kind !== 'group' && descriptor.kind !== 'category') || !descriptor.operandChoiceId) {
        throw new Error('The selected pivot output has no matching server operand choice.');
      }
      return { kind: 'CATALOG_CHOICE', choiceId: descriptor.operandChoiceId };
    }
    case 'literal': {
      const text = operand.text.trim();
      const value = Number(text);
      if (!text || !Number.isFinite(value)) throw new Error('Enter a finite numeric literal before resolving this column.');
      if (operand.representation === 'integer') {
        if (!Number.isSafeInteger(value)) throw new Error('This integer is outside the exact range supported by the browser.');
        return { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: value } };
      }
      return { kind: 'LITERAL', literal: { kind: 'DECIMAL', decimal: value } };
    }
    default: {
      const exhaustive: never = operand;
      return exhaustive;
    }
  }
};

export const pivotOutputLabelMap = (
  intent: TableShapeProposalIntent,
  table: DraftTable,
): Readonly<Record<string, string>> => {
  const labels: Record<string, string> = {};
  for (const column of table.document.columns) labels[column.column] = column.label;
  for (const derived of intent.derivedColumns) labels[derived.output.column] = derived.output.label;
  if (intent.kind === 'GROUPED_PIVOT') {
    for (const category of intent.pivot.includedCategories) labels[category.output.column] = category.output.label;
  }
  if (intent.kind === 'UNPIVOT') {
    labels[intent.unpivot.keyOutput.column] = intent.unpivot.keyOutput.label;
    labels[intent.unpivot.valueOutput.column] = intent.unpivot.valueOutput.label;
  }
  return labels;
};

export const proposeTableShapeIntent = async (args: {
  readonly client: TableShapeControllerClient;
  readonly session: EditorSession;
  readonly intent: TableShapeProposalIntent;
  readonly isCurrent: () => boolean;
  readonly onProposing: () => void;
}): Promise<TableShapeProposalResult | undefined> => {
  const { client, session, intent, isCurrent, onProposing } = args;
  const removesShape = intent.kind === 'NONE' && intent.derivedColumns.length === 0;
  if (removesShape && !session.savedShapeExists) throw new Error('There is no saved table shape to remove.');

  let reshape: TableShapeResolution | undefined;
  const scopeArgs = scopeRequest(session.scope);
  if (intent.kind === 'GROUPED_PIVOT') {
    reshape = await client.resolveTableShape({
      ...scopeArgs,
      catalogId: session.catalogId,
      kind: 'PIVOT',
      pivot: {
        categoryDiscoveryId: intent.pivot.categoryDiscoveryIdentity,
        groupColumnChoiceIds: intent.pivot.groupColumns.map((reference) => reference.choiceId),
        categoryColumnChoiceId: intent.pivot.categoryColumn.choiceId,
        valueColumnChoiceId: intent.pivot.valueColumn.choiceId,
        categories: intent.pivot.includedCategories.map((category) => ({
          choiceId: category.category.choiceId,
          outputColumn: category.output.column,
          outputLabel: category.output.label,
        })),
        duplicatePolicyChoiceId: intent.pivot.duplicatePolicy.choiceId,
        missingPolicyChoiceId: intent.pivot.missingCellPolicy.choiceId,
        unlistedPolicyChoiceId: intent.pivot.unlistedCategoryPolicy.choiceId,
      },
    });
  } else if (intent.kind === 'UNPIVOT') {
    reshape = await client.resolveTableShape({
      ...scopeArgs,
      catalogId: session.catalogId,
      kind: 'UNPIVOT',
      unpivot: {
        inputColumnChoiceIds: intent.unpivot.inputColumns.map((reference) => reference.choiceId),
        nullPolicyChoiceId: intent.unpivot.nullRowPolicy.choiceId,
        keyOutputColumn: intent.unpivot.keyOutput.column,
        keyOutputLabel: intent.unpivot.keyOutput.label,
        valueOutputColumn: intent.unpivot.valueOutput.column,
        valueOutputLabel: intent.unpivot.valueOutput.label,
      },
    });
  }
  if (reshape) {
    const expectedKind = intent.kind === 'GROUPED_PIVOT' ? 'PIVOT' : 'UNPIVOT';
    if (!isCurrent()) return undefined;
    if (reshape.catalogId !== session.catalogId || reshape.kind !== expectedKind) {
      throw new Error('Loom resolved a different table-shape selection. Reload settings and try again.');
    }
  }

  const derivedIds = new Map<string, string>();
  const orderedDerivedResolutionIds: string[] = [];
  for (const column of intent.derivedColumns) {
    const left = tableShapeOperandSelectionFor(column.leftOperand, derivedIds, reshape);
    const right = tableShapeOperandSelectionFor(column.rightOperand, derivedIds, reshape);
    const resolution = await client.resolveTableShape({
      ...scopeArgs,
      catalogId: session.catalogId,
      kind: 'DERIVED',
      derived: {
        outputColumn: column.output.column,
        outputLabel: column.output.label,
        operatorChoiceId: column.operator.choiceId,
        left,
        right,
        missingPolicyChoiceId: column.missingInputPolicy.choiceId,
        ...(column.divisionByZeroPolicy
          ? { divisionByZeroPolicyChoiceId: column.divisionByZeroPolicy.choiceId }
          : {}),
        ...(reshape?.kind === 'PIVOT' ? { pivotResolutionId: reshape.resolutionId } : {}),
      },
    });
    if (!isCurrent()) return undefined;
    if (resolution.catalogId !== session.catalogId || resolution.kind !== 'DERIVED') {
      throw new Error(`Loom could not resolve derived column "${column.output.label}" for this table.`);
    }
    derivedIds.set(column.localId, resolution.resolutionId);
    orderedDerivedResolutionIds.push(resolution.resolutionId);
  }

  if (!isCurrent()) return undefined;
  onProposing();
  const mode = removesShape ? 'REMOVE' : session.savedShapeExists ? 'REPLACE' : 'ADD';
  const proposal = await client.proposeTableShape({
    ...scopeArgs,
    mode,
    catalogId: session.catalogId,
    ...(reshape ? { reshapeResolutionId: reshape.resolutionId } : {}),
    derivedResolutionIds: orderedDerivedResolutionIds,
  });
  if (!isCurrent()) return undefined;
  return proposal;
};
