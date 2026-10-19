import { fireEvent, screen } from '@testing-library/react';

const GROUP_FIELD_AFFORDANCE = 'builder.reshape.group_field';
const GROUP_EDIT_RUNNER_ACTION = 'builder.group-field.edit-aggregate';

type GroupAggregateOperation =
  | 'COUNT_ROWS'
  | 'COUNT_NON_NULL'
  | 'COUNT_DISTINCT'
  | 'SUM'
  | 'MEAN';

type AdapterInput = {
  readonly uiAction: {
    readonly affordanceId: string;
    readonly runnerActionId: string;
    readonly values: Readonly<Record<string, unknown>>;
  };
  readonly requestStep: Readonly<Record<string, unknown>> | null;
};

type RelationalAdapter = {
  readonly adapterId: string;
  readonly runnerActionId: string;
  readonly sourceRefs: ReadonlyArray<string>;
  readonly drive: (input: AdapterInput) => Promise<void>;
};

const isRecord = (value: unknown): value is Readonly<Record<string, unknown>> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const aggregateOperationFrom = (value: unknown): GroupAggregateOperation | undefined => {
  switch (value) {
    case 'COUNT_ROWS':
    case 'COUNT_NON_NULL':
    case 'COUNT_DISTINCT':
    case 'SUM':
    case 'MEAN':
      return value;
    default:
      return undefined;
  }
};

const nonEmptyString = (value: unknown): string | undefined =>
  typeof value === 'string' && value.trim().length > 0 ? value : undefined;

const aggregateIndexFrom = (values: Readonly<Record<string, unknown>>, aggregate: unknown): number => {
  const aggregateRecord = isRecord(aggregate) ? aggregate : undefined;
  const value = values.aggregateIndex ?? aggregateRecord?.aggregateIndex ?? aggregateRecord?.index;
  if (value === undefined) return 0;
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
    throw new Error('Group aggregate index must be a non-negative integer.');
  }
  return value;
};

const selectValue = async (label: string, value: string): Promise<void> => {
  const initialControl = await screen.findByLabelText(label);
  if (!(initialControl instanceof HTMLSelectElement)) {
    throw new Error(`Expected the ${label} control to be a select.`);
  }

  const option = Array.from(initialControl.options).find((candidate) => candidate.value === value);
  if (!option || option.disabled) {
    throw new Error(`The mounted ${label} selector does not offer ${value}.`);
  }
  if (initialControl.value !== value) {
    fireEvent.change(initialControl, { target: { value } });
  }

  await screen.findByLabelText(label);
  const currentControl = screen.getByLabelText(label);
  if (!(currentControl instanceof HTMLSelectElement) || currentControl.value !== value) {
    throw new Error(`The mounted ${label} selector did not accept ${value}.`);
  }
};

const editSavedGroupAggregate = async (input: AdapterInput): Promise<void> => {
  const { uiAction } = input;
  if (uiAction.affordanceId !== GROUP_FIELD_AFFORDANCE) {
    throw new Error(`Group editor received unexpected affordance ${uiAction.affordanceId}.`);
  }
  if (uiAction.runnerActionId !== GROUP_EDIT_RUNNER_ACTION) {
    throw new Error(`Group editor received unexpected runner action ${uiAction.runnerActionId}.`);
  }

  const { values } = uiAction;
  const groupStepId = nonEmptyString(values.groupStepId);
  if (!groupStepId) throw new Error('Saved Group editing requires groupStepId.');

  const aggregateValue = values.aggregate;
  const aggregateRecord = isRecord(aggregateValue) ? aggregateValue : undefined;
  const aggregateIndex = aggregateIndexFrom(values, aggregateValue);
  const operation = aggregateOperationFrom(
    values.operation ?? aggregateRecord?.operation ?? aggregateRecord?.kind ?? aggregateValue,
  );
  if (!operation) throw new Error('Saved Group editing requires a supported aggregate operation.');

  const inputColumnId = nonEmptyString(values.inputColumnId ?? aggregateRecord?.inputColumnId);
  if (operation !== 'COUNT_ROWS' && !inputColumnId) {
    throw new Error(`${operation} requires a summary input column.`);
  }

  fireEvent.click(screen.getByTestId(`construction-history-step-${groupStepId}`));
  await selectValue(`Summary ${aggregateIndex + 1}`, operation);
  if (inputColumnId) {
    await selectValue(`Summary field ${aggregateIndex + 1}`, inputColumnId);
  }

  // The mounted Builder proposes a changed construction as the user edits it.
  // Wait for its visible ready state without applying or inspecting the proposal.
  await screen.findByTestId('construction-proposal-ready', {}, { timeout: 30_000 });
};

export const relationalAdapters = {
  [GROUP_FIELD_AFFORDANCE]: {
    adapterId: 'builder.reshape.group_field.edit_saved_aggregate',
    runnerActionId: GROUP_EDIT_RUNNER_ACTION,
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx:404',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3320',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:873',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:3320',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:2158',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.ts:325',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionProposalPanel.tsx:258',
    ],
    drive: editSavedGroupAggregate,
  },
} satisfies Readonly<Record<string, RelationalAdapter>>;
