import { fireEvent, screen, within } from '@testing-library/react';

type RequestStep = Readonly<{
  readonly flowId: string;
  readonly stepId: string;
  readonly kind: string;
  readonly method: string;
  readonly endpoint: string;
  readonly actionId: string;
  readonly order: number;
}>;

export type PeripheralDriveInput = Readonly<{
  readonly uiAction: Readonly<{
    readonly affordanceId: string;
    readonly runnerActionId: string;
    readonly values: Readonly<Record<string, unknown>>;
  }>;
  readonly requestStep: RequestStep | null;
}>;

type PeripheralAdapter = Readonly<{
  readonly adapterId: string;
  readonly runnerActionId: string;
  readonly sourceRefs: ReadonlyArray<string>;
  readonly drive: (input: PeripheralDriveInput) => void | Promise<void>;
}>;

type Values = PeripheralDriveInput['uiAction']['values'];

const isRecord = (value: unknown): value is Readonly<Record<string, unknown>> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const requiredString = (values: Values, key: string): string => {
  const value = values[key];
  if (typeof value !== 'string' || value.length === 0) {
    throw new Error(`Peripheral UI action requires string value "${key}".`);
  }
  return value;
};

const requiredText = (values: Values, key: string): string => {
  const value = values[key];
  if (typeof value !== 'string') throw new Error(`Peripheral UI action requires text value "${key}".`);
  return value;
};

const optionalString = (values: Values, key: string): string | undefined => {
  const value = values[key];
  if (value === undefined) return undefined;
  if (typeof value !== 'string') throw new Error(`Peripheral UI action value "${key}" must be a string.`);
  return value;
};

const requiredBoolean = (values: Values, key: string): boolean => {
  const value = values[key];
  if (typeof value !== 'boolean') throw new Error(`Peripheral UI action requires boolean value "${key}".`);
  return value;
};

const requiredNumber = (values: Values, key: string): number => {
  const value = values[key];
  if (typeof value !== 'number' || !Number.isSafeInteger(value)) {
    throw new Error(`Peripheral UI action requires safe integer value "${key}".`);
  }
  return value;
};

const enumValue = <const T extends string>(values: Values, key: string, choices: readonly T[]): T => {
  const value = requiredString(values, key);
  const match = choices.find((choice) => choice === value);
  if (match === undefined) throw new Error(`Unsupported value "${value}" for "${key}".`);
  return match;
};

const objectValue = (values: Values, key: string): Readonly<Record<string, unknown>> => {
  const value = values[key];
  if (!isRecord(value)) {
    throw new Error(`Peripheral UI action requires object value "${key}".`);
  }
  return value;
};

const assertAction = (input: PeripheralDriveInput, affordanceId: string, runnerActionId: string): Values => {
  if (input.uiAction.affordanceId !== affordanceId || input.uiAction.runnerActionId !== runnerActionId) {
    throw new Error(`Peripheral adapter ${runnerActionId} received a mismatched UI action.`);
  }
  return input.uiAction.values;
};

const getColumnLabel = (values: Values): string =>
  optionalString(values, 'current_label') ??
  optionalString(values, 'column_label') ??
  requiredString(values, 'column');

const ensureDetailsOpen = (summaryText: string): void => {
  const summary = screen.getByText(summaryText, { exact: true });
  const details = summary.closest('details');
  if (details instanceof HTMLDetailsElement && !details.open) fireEvent.click(summary);
};

const ensurePreviewColumnsOpen = (): void => {
  const button = screen.getByRole('button', { name: 'Columns' });
  if (button.getAttribute('aria-expanded') !== 'true') fireEvent.click(button);
};

const ensureCheck = (control: HTMLElement, checked: boolean): void => {
  if (!(control instanceof HTMLInputElement) || control.type !== 'checkbox') {
    throw new Error('Expected a mounted checkbox control.');
  }
  if (control.checked !== checked) fireEvent.click(control);
};

const selectOption = (control: HTMLElement, value: string): void => {
  if (!(control instanceof HTMLSelectElement)) throw new Error('Expected a mounted select control.');
  fireEvent.change(control, { target: { value } });
};

const savedFrame = (frameId: string) => {
  const frame = screen.getByTestId(`saved-frame-${frameId}`);
  return within(frame);
};

const populationPanel = () => within(screen.getByRole('region', { name: 'Starting collection' }));

const setInput = (control: HTMLElement, value: string): void => {
  fireEvent.change(control, { target: { value } });
  fireEvent.blur(control);
};

const setCheckboxByLabel = (label: string, value: boolean): void => {
  ensureCheck(screen.getByRole('checkbox', { name: label }), value);
};

const screenAction = (values: Values): string => requiredString(values, 'action');

export const driveGroupedRowValuePolicy = (input: PeripheralDriveInput): void => {
  const values = assertAction(input, 'builder.column.grouped_row_value_policy', 'driveGroupedRowValuePolicy');
  const label = getColumnLabel(values);
  const column = optionalString(values, 'column') ?? label;
  ensurePreviewColumnsOpen();
  const policy = enumValue(values, 'row_value_policy', ['ONE', 'ALL']);
  selectOption(screen.getByRole('combobox', { name: `Values per cohort member for ${label} (${column})` }), policy);
};

export const driveDatasetReviewNavigation = async (input: PeripheralDriveInput): Promise<void> => {
  const values = assertAction(input, 'builder.dataset_review.navigation', 'driveDatasetReviewNavigation');
  const action = screenAction(values);
  if (action === 'open') {
    fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
    await screen.findByRole('region', { name: 'Dataset review' });
    return;
  }
  if (action === 'close') {
    fireEvent.click(screen.getByRole('button', { name: 'Close review' }));
    return;
  }
  if (action === 'focus') {
    fireEvent.click(screen.getByRole('button', { name: requiredString(values, 'target_action') }));
    return;
  }
  throw new Error(`Unsupported dataset review action "${action}".`);
};

export const driveFrameSourceManagement = async (input: PeripheralDriveInput): Promise<void> => {
  const values = assertAction(input, 'builder.frame_source.manage_existing', 'driveFrameSourceManagement');
  ensureDetailsOpen('Manage saved coded-value sources');
  const action = screenAction(values);
  if (action === 'remove_column') {
    fireEvent.click(screen.getByRole('button', { name: `Remove ${requiredString(values, 'column_label')} column` }));
    return;
  }
  const frame = savedFrame(requiredString(values, 'frame_id'));
  if (action === 'remove_frame') {
    fireEvent.click(frame.getByRole('button', { name: 'Remove' }));
    const confirm = screen.queryByRole('button', { name: 'Apply this change' });
    if (confirm) fireEvent.click(confirm);
    return;
  }
  if (action === 'open_source_choices') {
    fireEvent.click(frame.getByRole('button', { name: 'Change source' }));
    await screen.findByRole('searchbox', { name: 'Search framing sources' });
    return;
  }
  if (action === 'replace_source') {
    const sourceChoiceId = requiredString(values, 'source_choice_id');
    const change = frame.queryByRole('button', { name: 'Change source' });
    if (change) fireEvent.click(change);
    const sourceButton = screen.getByTestId(`frame-source-choice-${sourceChoiceId}`);
    const sourceGroup = sourceButton.closest('div.px-3.py-2');
    if (!(sourceGroup instanceof HTMLElement)) throw new Error(`No mounted source choice row for ${sourceChoiceId}.`);
    const form = optionalString(values, 'frame_form');
    if (form) {
      const formSelect = within(sourceGroup).queryByRole('combobox', { name: /Multiple values for/ });
      if (formSelect) selectOption(formSelect, form);
    }
    fireEvent.click(sourceButton);
    const confirm = screen.queryByRole('button', { name: 'Apply this change' });
    if (confirm) fireEvent.click(confirm);
    return;
  }
  throw new Error(`Unsupported frame source action "${action}".`);
};

export const driveGuidedGraphViewState = async (input: PeripheralDriveInput): Promise<void> => {
  const values = assertAction(input, 'builder.guided_graph.view_state', 'driveGuidedGraphViewState');
  const action = screenAction(values);
  if (action === 'show_orphans') {
    setCheckboxByLabel('Show orphans', requiredBoolean(values, 'show_orphans'));
    return;
  }
  if (action === 'expand') {
    const close = screen.queryByRole('button', { name: 'Close graph' });
    if (!close) fireEvent.click(screen.getByTestId('graph-viewport'));
    return;
  }
  if (action === 'close') {
    const close = screen.queryByRole('button', { name: 'Close graph' });
    if (close) fireEvent.click(close);
    return;
  }
  if (action === 'select_occurrence') {
    const occurrenceId = requiredString(values, 'occurrence_id');
    const traversal = screen.getByRole('navigation', { name: 'Current traversal' });
    const button = Array.from(traversal.querySelectorAll('button[data-occurrence-id]')).find(
      (candidate) => candidate.getAttribute('data-occurrence-id') === occurrenceId,
    );
    if (!(button instanceof HTMLButtonElement)) throw new Error(`No traversal button is mounted for occurrence ${occurrenceId}.`);
    fireEvent.click(button);
    return;
  }
  throw new Error(`Unsupported guided graph view action "${action}".`);
};

const interpretationPanelFor = (columnLabel: string) => {
  const matches = screen.getAllByText(columnLabel, { exact: true });
  const panel = matches.map((match) => match.closest('div.col-span-full')).find((candidate) => candidate !== null);
  if (!(panel instanceof HTMLElement)) throw new Error(`No InterpretationPanel is mounted for ${columnLabel}.`);
  return within(panel);
};

export const driveInterpretationCreateRevision = async (input: PeripheralDriveInput): Promise<void> => {
  const values = assertAction(input, 'builder.interpretation.create_revision_from_column', 'driveInterpretationCreateRevision');
  const panel = interpretationPanelFor(getColumnLabel(values));
  const mode = enumValue(values, 'revision_mode', ['new', 'revise']);
  const mappings = panel.getByText('Reusable mappings', { exact: true });
  const details = mappings.closest('details');
  if (details instanceof HTMLDetailsElement && !details.open) fireEvent.click(mappings);
  selectOption(panel.getByRole('combobox', { name: 'Revision mode' }), mode);
  if (mode === 'new') {
    fireEvent.change(panel.getByRole('textbox', { name: 'Library name' }), { target: { value: requiredString(values, 'library_id_new') } });
  } else {
    selectOption(panel.getByRole('combobox', { name: 'Library head' }), requiredString(values, 'applicable_library_head'));
  }
  fireEvent.change(panel.getByRole('textbox', { name: 'Explanation' }), { target: { value: requiredString(values, 'explanation') } });
  fireEvent.click(panel.getByRole('button', { name: 'Save reusable mapping' }));
};

export const driveInterpretationCandidate = async (input: PeripheralDriveInput): Promise<void> => {
  const values = assertAction(input, 'builder.interpretation.preview_and_apply_candidate', 'driveInterpretationCandidate');
  const panel = interpretationPanelFor(getColumnLabel(values));
  const stepKind = input.requestStep?.kind;
  if (stepKind === 'preview' || screenAction(values) === 'review') {
    const libraryId = requiredString(values, 'library_id');
    const mappings = panel.getByText('Reusable mappings', { exact: true });
    const details = mappings.closest('details');
    if (details instanceof HTMLDetailsElement && !details.open) fireEvent.click(mappings);
    const library = panel.getByText(libraryId, { exact: true });
    const row = library.closest('div.flex.flex-wrap.items-start');
    if (!(row instanceof HTMLElement)) throw new Error(`No reusable mapping row is mounted for ${libraryId}.`);
    fireEvent.click(within(row).getByRole('button', { name: 'Review' }));
    return;
  }
  if (stepKind === 'command' || screenAction(values) === 'apply') {
    fireEvent.click(panel.getByRole('button', { name: 'Apply' }));
    return;
  }
  if (screenAction(values) === 'cancel') {
    fireEvent.click(panel.getByRole('button', { name: 'Cancel' }));
    return;
  }
  throw new Error(`Unsupported interpretation candidate action for request step ${stepKind ?? 'none'}.`);
};

const selectionRef = (values: Values, key: string): Readonly<{ resourceType: string; id: string }> => {
  const value = objectValue(values, key);
  if (typeof value.resourceType !== 'string' || typeof value.id !== 'string') {
    throw new Error(`Peripheral UI action value "${key}" requires resourceType and id.`);
  }
  return { resourceType: value.resourceType, id: value.id };
};

export const drivePopulationAttachReplace = (input: PeripheralDriveInput): void => {
  const values = assertAction(input, 'builder.population.attach_or_replace_selection', 'drivePopulationAttachReplace');
  const panel = populationPanel();
  const routeIndex = optionalString(values, 'connection_index');
  if (routeIndex && Number(routeIndex) > 0) {
    const other = panel.queryByRole('button', { name: 'Other connections' });
    if (other) fireEvent.click(other);
  }
  const connection = panel.queryByRole('combobox', { name: 'Population connection' });
  if (connection && routeIndex !== undefined) selectOption(connection, routeIndex);
  const action = screenAction(values);
  const buttonName = action === 'replace' ? 'Replace current collection' : 'Use selected resources';
  fireEvent.click(panel.getByRole('button', { name: buttonName }));
};

export const drivePopulationClear = (input: PeripheralDriveInput): void => {
  assertAction(input, 'builder.population.clear_attached_selection', 'drivePopulationClear');
  fireEvent.click(populationPanel().getByRole('button', { name: 'Use all authorized rows' }));
};

export const drivePopulationCoverageCheck = (input: PeripheralDriveInput): void => {
  assertAction(input, 'builder.population.selected_resource_coverage_check', 'drivePopulationCoverageCheck');
  fireEvent.click(populationPanel().getByRole('button', { name: /^(Check selected-resource coverage|Checking selected-resource coverage…)$/ }));
};

export const drivePopulationExcludeMember = (input: PeripheralDriveInput): void => {
  const values = assertAction(input, 'builder.population.exclude_unmapped_member', 'drivePopulationExcludeMember');
  const ref = selectionRef(values, 'excluded_resource_ref');
  const panel = populationPanel();
  const label = `${ref.resourceType}/${ref.id}`;
  const matchingText = panel.getByText(label, { exact: true });
  const item = matchingText.closest('li');
  if (!item) throw new Error(`No unmapped population row is mounted for ${label}.`);
  fireEvent.click(within(item).getByRole('button', { name: 'Remove from collection' }));
};

export const drivePopulationMemberRemoval = (input: PeripheralDriveInput): void => {
  const values = assertAction(input, 'builder.population.review_and_remove_attached_member', 'drivePopulationMemberRemoval');
  const action = input.requestStep?.kind === 'proposal' ? 'review' :
    input.requestStep?.kind === 'command' ? 'apply' : screenAction(values);
  if (action === 'review') {
    const ref = selectionRef(values, 'selection_member');
    fireEvent.click(screen.getByRole('button', { name: `Review removal of ${ref.resourceType}/${ref.id}` }));
    return;
  }
  if (action === 'apply') {
    fireEvent.click(screen.getByRole('button', { name: 'Apply member removal' }));
    return;
  }
  if (action === 'cancel') {
    fireEvent.click(screen.getByRole('button', { name: 'Cancel member removal' }));
    return;
  }
  throw new Error(`Unsupported population member-removal action "${action}".`);
};

export const drivePreviewRowSources = async (input: PeripheralDriveInput): Promise<void> => {
  const values = assertAction(input, 'builder.preview_table.inspect_row_sources', 'drivePreviewRowSources');
  const action = screenAction(values);
  if (action === 'inspect_row') {
    const rowNumber = requiredNumber(values, 'row_number');
    fireEvent.click(screen.getByRole('button', { name: `Inspect row ${rowNumber} identity` }));
    await screen.findByRole('dialog', { name: `Row ${rowNumber} identity` });
    return;
  }
  if (action === 'next_lineage_page') {
    fireEvent.click(screen.getByRole('button', { name: 'Show more source records' }));
    return;
  }
  if (action === 'inspect_owner_records') {
    const rowNumber = requiredNumber(values, 'row_number');
    const label = requiredString(values, 'column_label');
    fireEvent.click(screen.getByRole('button', { name: `Inspect ${label} for row ${rowNumber}` }));
    return;
  }
  if (action === 'close_row') {
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    return;
  }
  throw new Error(`Unsupported preview row-source action "${action}".`);
};

export const peripheralAdapters = {
  'builder.column.grouped_row_value_policy': {
    adapterId: 'peripheral.column.grouped-row-value-policy', runnerActionId: 'driveGroupedRowValuePolicy',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/PreviewTable.tsx:700', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/PreviewTable.tsx:703', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4478'],
    drive: driveGroupedRowValuePolicy,
  },
  'builder.dataset_review.navigation': {
    adapterId: 'peripheral.dataset-review.navigation', runnerActionId: 'driveDatasetReviewNavigation',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/DatasetReviewPanel.tsx:553', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/DatasetReviewPanel.tsx:623', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1271', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3151'],
    drive: driveDatasetReviewNavigation,
  },
  'builder.frame_source.manage_existing': {
    adapterId: 'peripheral.frame-source.manage-existing', runnerActionId: 'driveFrameSourceManagement',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3749', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3755', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/FrameSourcePanel.tsx:302', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/FrameSourcePanel.tsx:337', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/FrameSourcePanel.tsx:408', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/FrameSourcePanel.tsx:429'],
    drive: driveFrameSourceManagement,
  },
  'builder.guided_graph.view_state': {
    adapterId: 'peripheral.guided-graph.view-state', runnerActionId: 'driveGuidedGraphViewState',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx:85', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx:481', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx:500', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx:544', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx:604'],
    drive: driveGuidedGraphViewState,
  },
  'builder.interpretation.create_revision_from_column': {
    adapterId: 'peripheral.interpretation.create-revision-from-column', runnerActionId: 'driveInterpretationCreateRevision',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.tsx:145', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.tsx:153', 'ui/packages/loom-ui/src/api.ts:1766'],
    drive: driveInterpretationCreateRevision,
  },
  'builder.interpretation.preview_and_apply_candidate': {
    adapterId: 'peripheral.interpretation.preview-and-apply-candidate', runnerActionId: 'driveInterpretationCandidate',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.tsx:122', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.tsx:138', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/InterpretationPanel.tsx:177', 'ui/packages/loom-ui/src/api.ts:1781'],
    drive: driveInterpretationCandidate,
  },
  'builder.population.attach_or_replace_selection': {
    adapterId: 'peripheral.population.attach-or-replace-selection', runnerActionId: 'drivePopulationAttachReplace',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/PopulationPanel.tsx:188', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/PopulationPanel.tsx:248', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4331'],
    drive: drivePopulationAttachReplace,
  },
  'builder.population.clear_attached_selection': {
    adapterId: 'peripheral.population.clear-attached-selection', runnerActionId: 'drivePopulationClear',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/PopulationPanel.tsx:236', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4343'],
    drive: drivePopulationClear,
  },
  'builder.population.selected_resource_coverage_check': {
    adapterId: 'peripheral.population.selected-resource-coverage-check', runnerActionId: 'drivePopulationCoverageCheck',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/PopulationPanel.tsx:167', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/PopulationPanel.tsx:240'],
    drive: drivePopulationCoverageCheck,
  },
  'builder.population.exclude_unmapped_member': {
    adapterId: 'peripheral.population.exclude-unmapped-member', runnerActionId: 'drivePopulationExcludeMember',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/PopulationPanel.tsx:305', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1936', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1980'],
    drive: drivePopulationExcludeMember,
  },
  'builder.population.review_and_remove_attached_member': {
    adapterId: 'peripheral.population.review-and-remove-attached-member', runnerActionId: 'drivePopulationMemberRemoval',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/PopulationMemberRemoval.tsx:217', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/PopulationMemberRemoval.tsx:313', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4349'],
    drive: drivePopulationMemberRemoval,
  },
  'builder.preview_table.inspect_row_sources': {
    adapterId: 'peripheral.preview-table.inspect-row-sources', runnerActionId: 'drivePreviewRowSources',
    sourceRefs: ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/PreviewTable.tsx:895', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/PreviewTable.tsx:903', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/PreviewTable.tsx:1001', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4489'],
    drive: drivePreviewRowSources,
  },
} satisfies Readonly<Record<string, PeripheralAdapter>>;
