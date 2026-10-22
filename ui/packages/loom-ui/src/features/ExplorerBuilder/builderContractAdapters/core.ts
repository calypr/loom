import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { isConstructionProposalAction, isRowDefinitionAction } from './actionGuards';

type ActionValues = Readonly<Record<string, unknown>>;
type ActionRequestStep = Readonly<Record<string, unknown>> | null;
type CoreAdapterInput = Readonly<{
  readonly uiAction: Readonly<{
    readonly affordanceId: string;
    readonly runnerActionId: string;
    readonly values: unknown;
  }>;
  readonly requestStep: ActionRequestStep;
}>;
type CoreAdapter = Readonly<{
  readonly adapterId: string;
  readonly runnerActionId: string;
  readonly sourceRefs: ReadonlyArray<string>;
  readonly drive: (input: CoreAdapterInput) => Promise<void>;
}>;

export class CoreAdapterGapError extends Error {
  readonly kind = 'ADAPTER_GAP';

  constructor(affordanceId: string, reason: string) {
    super(`ADAPTER_GAP ${affordanceId}: ${reason}`);
    this.name = 'CoreAdapterGapError';
  }
}

const isRecord = (value: unknown): value is Readonly<Record<string, unknown>> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const valuesOf = (input: CoreAdapterInput): ActionValues => {
  if (!isRecord(input.uiAction.values)) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'the action has no concrete values object.');
  }
  return input.uiAction.values;
};

const requiredString = (input: CoreAdapterInput, values: ActionValues, key: string): string => {
  const value = values[key];
  if (typeof value !== 'string' || value.trim().length === 0) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `action value "${key}" must be a non-empty string.`);
  }
  return value;
};

const optionalString = (values: ActionValues, ...keys: ReadonlyArray<string>): string | undefined => {
  for (const key of keys) {
    const value = values[key];
    if (typeof value === 'string' && value.length > 0) return value;
  }
  return undefined;
};

const hasTaggedBrowseSource = (values: ActionValues): boolean =>
  isRecord(values.browse_source) || isRecord(values.sourceOption);

const actionName = (input: CoreAdapterInput, values: ActionValues): string | undefined => {
  const requestAction = input.requestStep?.actionId;
  if (typeof requestAction === 'string') return requestAction;
  return optionalString(values, 'localAction');
};

const assertAdapter = (
  input: CoreAdapterInput,
  affordanceId: string,
  runnerActionId: string,
): ActionValues => {
  if (input.uiAction.affordanceId !== affordanceId || input.uiAction.runnerActionId !== runnerActionId) {
    throw new CoreAdapterGapError(affordanceId, `received action binding ${input.uiAction.affordanceId}/${input.uiAction.runnerActionId}.`);
  }
  return valuesOf(input);
};

const failIfUnavailable = (input: CoreAdapterInput, control: HTMLElement, description: string): void => {
  if (control instanceof HTMLButtonElement || control instanceof HTMLInputElement || control instanceof HTMLSelectElement) {
    if (control.disabled) throw new CoreAdapterGapError(input.uiAction.affordanceId, `${description} is disabled in the mounted workspace.`);
  }
  if (control.getAttribute('aria-disabled') === 'true') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `${description} is aria-disabled in the mounted workspace.`);
  }
};

const clickTestId = async (input: CoreAdapterInput, testId: string, description = testId): Promise<HTMLElement> => {
  const control = await screen.findByTestId(testId);
  failIfUnavailable(input, control, description);
  fireEvent.click(control);
  return control;
};

const clickNamedButton = async (
  input: CoreAdapterInput,
  name: string | RegExp,
  description = String(name),
): Promise<HTMLElement> => {
  const control = await screen.findByRole('button', { name });
  failIfUnavailable(input, control, description);
  fireEvent.click(control);
  return control;
};

const selectLabel = async (
  input: CoreAdapterInput,
  label: string,
  value: string,
): Promise<HTMLSelectElement> => {
  const control = await screen.findByLabelText(label);
  if (!(control instanceof HTMLSelectElement)) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `mounted control "${label}" is not a select.`);
  }
  failIfUnavailable(input, control, label);
  const option = [...control.options].find((candidate) => candidate.value === value);
  if (!option || option.disabled) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `the mounted "${label}" selector does not offer ${JSON.stringify(value)}.`);
  }
  if (control.value !== value) fireEvent.change(control, { target: { value } });
  await waitFor(() => {
    const current = screen.getByLabelText(label);
    if (!(current instanceof HTMLSelectElement) || current.value !== value) {
      throw new Error(`The mounted "${label}" selector did not select ${JSON.stringify(value)}.`);
    }
  });
  return control;
};

const setText = async (
  input: CoreAdapterInput,
  label: string,
  value: string,
): Promise<HTMLElement> => {
  const control = await screen.findByLabelText(label);
  if (!(control instanceof HTMLInputElement || control instanceof HTMLTextAreaElement)) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `mounted control "${label}" is not text-editable.`);
  }
  failIfUnavailable(input, control, label);
  fireEvent.change(control, { target: { value } });
  await waitFor(() => {
    const current = screen.getByLabelText(label);
    if (!(current instanceof HTMLInputElement || current instanceof HTMLTextAreaElement) || current.value !== value) {
      throw new Error(`The mounted "${label}" control did not accept the supplied value.`);
    }
  });
  return control;
};

const openConstructionFamily = async (
  input: CoreAdapterInput,
  family: 'ADD_COLUMNS' | 'KEEP_ROWS' | 'CALCULATE',
): Promise<HTMLElement> => {
  const testId = `construction-action-${family.toLocaleLowerCase().replace('_', '-')}`;
  let button = screen.queryByTestId(testId);
  if (!button && family === 'CALCULATE') {
    const moreActions = await screen.findByTestId('construction-more-actions');
    if (moreActions.getAttribute('aria-expanded') !== 'true') fireEvent.click(moreActions);
    button = await screen.findByTestId(testId);
  }
  if (!button) throw new CoreAdapterGapError(input.uiAction.affordanceId, `the mounted ${family} action is unavailable.`);
  failIfUnavailable(input, button, `${family} action`);
  if (button.getAttribute('aria-pressed') !== 'true') fireEvent.click(button);
  const editor = await screen.findByTestId('construction-operation-editor');
  if (editor.getAttribute('data-operation-family') !== family) {
    throw new Error(`The mounted construction editor did not open ${family}.`);
  }
  return editor;
};

const selectAddColumnsView = async (input: CoreAdapterInput, view: 'coded' | 'fields'): Promise<void> => {
  const label = view === 'coded' ? 'Coded values' : 'Other fields';
  const tab = await screen.findByRole('tab', { name: label });
  if (tab.getAttribute('aria-selected') !== 'true') fireEvent.click(tab);
  await waitFor(() => {
    if (screen.getByRole('tab', { name: label }).getAttribute('aria-selected') !== 'true') {
      throw new Error(`The ${label} catalog tab did not become selected.`);
    }
  });
};

const selectAddSource = async (input: CoreAdapterInput, values: ActionValues): Promise<void> => {
  const source = values.browse_source ?? values.sourceOption;
  if (!isRecord(source) || typeof source.kind !== 'string') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'source selection requires a tagged browse_source/sourceOption value.');
  }
  const sourceKey = typeof source.key === 'string' ? source.key : undefined;
  const kind = source.kind;
  const resourceType = typeof source.resourceType === 'string' ? source.resourceType : undefined;
  const sourceNodeId = typeof source.sourceNodeId === 'string' ? source.sourceNodeId : undefined;
  const stageId = typeof source.stageId === 'string' ? source.stageId : undefined;
  const targetNodeId = typeof source.targetNodeId === 'string' ? source.targetNodeId : undefined;
  const exactKey = sourceKey ?? (
    kind === 'ROOT' && resourceType ? `root:${resourceType}`
      : kind === 'RELATED' && sourceNodeId ? `node:${sourceNodeId}`
        : kind === 'SAVED_OCCURRENCE' && sourceNodeId ? `occurrence:${sourceNodeId}`
          : kind === 'EXACT_RELATED' && stageId && targetNodeId ? `exact:${stageId}:${targetNodeId}`
            : undefined
  );
  if (!exactKey) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'the tagged source value lacks the fields needed for an exact mounted option key.');
  }
  const options = await screen.findAllByTestId('construction-add-columns-source-option');
  const match = options.find((option) => option.getAttribute('data-source-key') === exactKey &&
    option.getAttribute('data-source-kind') === kind);
  if (!match) throw new CoreAdapterGapError(input.uiAction.affordanceId, `the mounted catalog does not offer exact ${kind} source ${exactKey}.`);
  fireEvent.click(match);
  await waitFor(() => {
    if (match.getAttribute('aria-pressed') !== 'true') throw new Error('Catalog source selection did not update the mounted control.');
  });
};

const searchCatalog = async (input: CoreAdapterInput, view: 'coded' | 'fields', query: string): Promise<void> => {
  const label = view === 'fields' ? 'Search dataset fields by name or type' : 'Search coded values by code or label';
  await setText(input, label, query);
  await clickNamedButton(input, 'Search');
};

const chooseCatalogItem = async (input: CoreAdapterInput, values: ActionValues): Promise<void> => {
  const selectionLabel = optionalString(values, 'selectionLabel', 'selection_label', 'candidateLabel', 'candidate_label');
  if (!selectionLabel) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'catalog selection requires a concrete selectionLabel.');
  const control = await screen.findByRole('button', { name: new RegExp(`^Select .*${escapeRegExp(selectionLabel)}.*$`) });
  failIfUnavailable(input, control, `catalog item ${selectionLabel}`);
  fireEvent.click(control);
  await waitFor(() => {
    if (control.getAttribute('aria-pressed') !== 'true') throw new Error(`Catalog item ${selectionLabel} was not selected.`);
  });
};

const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const confirmCatalogSelection = async (input: CoreAdapterInput, values: ActionValues): Promise<void> => {
  const advanced = screen.queryByRole('button', { name: 'Advanced options' });
  if (advanced) {
    failIfUnavailable(input, advanced, 'Advanced options');
    fireEvent.click(advanced);
    await screen.findByTestId('catalog-selection-options');
    const form = optionalString(values, 'constructionForm', 'construction_form', 'form');
    if (form) {
      const radio = screen.queryByRole('radio', { name: new RegExp(`\\b${escapeRegExp(form)}\\b`, 'i') });
      if (radio && radio instanceof HTMLInputElement && !radio.checked) fireEvent.click(radio);
    }
    await clickNamedButton(input, /^Add \d+ selected feature(?:s)?$/);
    return;
  }
  const directAdd = screen.queryByRole('button', { name: /^Add selected features$/ });
  if (directAdd) {
    failIfUnavailable(input, directAdd, 'Add selected features');
    fireEvent.click(directAdd);
    return;
  }
  throw new CoreAdapterGapError(input.uiAction.affordanceId, 'the selected catalog item has no mounted confirmation control.');
};

const driveCatalogChoice = async (input: CoreAdapterInput, semantic: boolean): Promise<void> => {
  const values = valuesOf(input);
  const action = actionName(input, values);
  const view = semantic ? 'coded' : 'fields';
  await openConstructionFamily(input, 'ADD_COLUMNS');
  await selectAddColumnsView(input, optionalString(values, 'view') === 'fields' ? 'fields' : view);
  if (hasTaggedBrowseSource(values)) await selectAddSource(input, values);
  if (action === 'browseEndpoint' || action === 'loadCatalog') {
    const query = optionalString(values, 'query', 'searchQuery', 'search_query');
    if (query !== undefined) await searchCatalog(input, view, query);
    else await screen.findByLabelText(view === 'fields' ? 'Search dataset fields by name or type' : 'Search coded values by code or label');
    return;
  }
  if (action === 'choiceEndpoint' || action === 'inspectChoices') {
    await chooseCatalogItem(input, values);
    await confirmCatalogSelection(input, values);
    return;
  }
  if (isConstructionProposalAction(action)) {
    await chooseCatalogItem(input, values);
    await confirmCatalogSelection(input, values);
    await screen.findByTestId('construction-proposal-ready');
    if (action === 'applyEndpoint' || action === 'apply') await clickTestId(input, 'construction-apply-proposal', 'Apply proposal');
    return;
  }
  throw new CoreAdapterGapError(input.uiAction.affordanceId, `no catalog action is defined for request step ${action ?? '(missing)'}.`);
};

const driveScalarFilter = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  await openConstructionFamily(input, 'KEEP_ROWS');
  const columnId = requiredString(input, values, 'condition_column');
  const condition = requiredString(input, values, 'operator');
  const valueKind = values.value_kind;
  const typedValues = values.typed_values;
  if (!Array.isArray(typedValues)) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'typed_values must be a concrete ordered list.');
  if (condition === 'EXISTS' || condition === 'MISSING') {
    if (valueKind !== null || typedValues.length !== 0) {
      throw new CoreAdapterGapError(input.uiAction.affordanceId, `${condition} requires null value_kind and an empty typed_values list.`);
    }
  } else if (typeof valueKind !== 'string' || !['STRING', 'CODE', 'BOOLEAN', 'INTEGER', 'DECIMAL', 'DATE', 'DATE_TIME'].includes(valueKind)) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'comparison filters require a supported value_kind.');
  }
  await selectLabel(input, 'Column', columnId);
  await selectLabel(input, 'Condition', condition);
  if (condition !== 'EXISTS' && condition !== 'MISSING') {
    if (typedValues.length === 0 || (condition !== 'IN' && typedValues.length !== 1)) {
      throw new CoreAdapterGapError(input.uiAction.affordanceId, `${condition} requires ${condition === 'IN' ? 'one or more' : 'exactly one'} typed_values item(s).`);
    }
    for (let index = 0; index < typedValues.length; index += 1) {
      const value = typedValues[index];
      if (typeof value !== 'string' && typeof value !== 'number' && typeof value !== 'boolean') {
        throw new CoreAdapterGapError(input.uiAction.affordanceId, `typed_values[${index}] must be a scalar of the selected value_kind.`);
      }
      if (index > 0) await clickNamedButton(input, 'Add another value');
      const label = condition === 'IN' ? `Value ${index + 1}` : 'Value';
      if (valueKind === 'BOOLEAN') {
        const bool = typeof value === 'boolean' ? String(value) : value;
        if (bool !== 'true' && bool !== 'false') throw new CoreAdapterGapError(input.uiAction.affordanceId, `typed_values[${index}] is not a boolean value.`);
        await selectLabel(input, label, bool);
      } else {
        await setText(input, label, String(value));
      }
    }
  }
  if (input.requestStep?.actionId === 'proposalEndpoint') await screen.findByTestId('construction-proposal-loading');
};

const driveRelatedEligibility = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  await openConstructionFamily(input, 'KEEP_ROWS');
  const relatedMode = await screen.findByRole('button', { name: 'Related records' });
  failIfUnavailable(input, relatedMode, 'Related records filter mode');
  fireEvent.click(relatedMode);
  const targetType = requiredString(input, values, 'target_resource_type');
  if (!targetType) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'related eligibility requires targetResourceType.');
  await selectLabel(input, 'Related eligibility record type', targetType);
  const requestAction = input.requestStep?.actionId;
  if (requestAction === 'choiceEndpoint') return;
  const routeLabel = optionalString(values, 'routeLabel', 'route_label');
  if (!routeLabel) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'related eligibility proposal requires routeLabel after the server returns choices.');
  await clickNamedButton(input, new RegExp(escapeRegExp(routeLabel)));
  const match = values.related_match;
  if (typeof match !== 'string' || !['EXISTS', 'ABSENT', 'COUNT_AT_LEAST'].includes(match)) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'related_match must be EXISTS, ABSENT, or COUNT_AT_LEAST.');
  }
  await selectLabel(input, 'Related eligibility rule', match);
  if (match === 'COUNT_AT_LEAST') {
    const minimum = values.minimum_matching_records;
    if (typeof minimum !== 'number' || !Number.isSafeInteger(minimum) || minimum < 1) {
      throw new CoreAdapterGapError(input.uiAction.affordanceId, 'COUNT_AT_LEAST requires a positive integer minimum_matching_records.');
    }
    await setText(input, 'Minimum matching records', String(minimum));
  }
  const contributorCondition = values.contributor_condition;
  if (contributorCondition !== 'ALL' && contributorCondition !== 'EXISTS' && contributorCondition !== 'EQUALS') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'contributor_condition must be ALL, EXISTS, or EQUALS.');
  }
  if (contributorCondition !== 'ALL') {
    const summary = screen.getByText('Which related records count?', { exact: true });
    const details = summary.closest('details');
    if (!(details instanceof HTMLDetailsElement)) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'related contributor condition details are not mounted.');
    if (!details.open) fireEvent.click(summary);
    const conditionName = contributorCondition === 'EXISTS' ? /Only records with/ : /Only records where .* equals/;
    const radio = screen.queryByRole('radio', { name: conditionName });
    if (!(radio instanceof HTMLInputElement)) {
      throw new CoreAdapterGapError(input.uiAction.affordanceId, `contributor condition ${contributorCondition} is not mounted.`);
    }
    failIfUnavailable(input, radio, `contributor condition ${contributorCondition}`);
    if (!radio.checked) fireEvent.click(radio);
    if (contributorCondition === 'EQUALS') {
      const value = requiredString(input, values, 'contributor_value');
      const contributor = await screen.findByRole('textbox', { name: /exact value/i });
      if (!(contributor instanceof HTMLInputElement)) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'contributor exact value input is not mounted.');
      fireEvent.change(contributor, { target: { value } });
    }
  }
  if (requestAction === 'contributorEndpoint') return;
  if (requestAction === 'proposalEndpoint') await screen.findByTestId('construction-proposal-loading');
  else if (requestAction === 'previewEndpoint') await screen.findByTestId('construction-proposal-ready');
  else if (requestAction === 'applyEndpoint') await clickTestId(input, 'construction-apply-proposal', 'Apply proposal');
  else if (requestAction !== 'choiceEndpoint') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `unsupported related eligibility actionId ${requestAction ?? '(missing)'}.`);
  }
};

const driveCalculate = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  await openConstructionFamily(input, 'CALCULATE');
  const view = values.expression_view;
  if (view !== 'guided' && view !== 'formula') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'expression_view must be the exact guided or formula assignment.');
  }
  if (view === 'formula') {
    const formula = requiredString(input, values, 'formula_text');
    await clickNamedButton(input, 'Formula editor');
    await setText(input, 'Formula', formula);
  } else {
    const operation = optionalString(values, 'operation');
    if (!operation || !['ADD', 'SUBTRACT', 'MULTIPLY', 'DIVIDE'].includes(operation)) {
      throw new CoreAdapterGapError(input.uiAction.affordanceId, 'guided calculation requires a supported operation.');
    }
    await selectLabel(input, 'Operation', operation);
    for (const [dimension, label] of [['left_operand', 'First value'], ['right_operand', 'Second value']] as const) {
      const operand = values[dimension];
      if (!isRecord(operand)) throw new CoreAdapterGapError(input.uiAction.affordanceId, `${dimension} must be a tagged numeric-column-or-literal value.`);
      const kindControl = `${label} kind`;
      if (operand.kind === 'COLUMN') {
        const columnId = requiredString(input, operand, 'columnId');
        await selectLabel(input, kindControl, 'column');
        await selectLabel(input, `${label} column`, columnId);
      } else if (operand.kind === 'LITERAL' && isRecord(operand.literal)) {
        const literal = operand.literal;
        if (literal.kind !== 'INTEGER' && literal.kind !== 'DECIMAL') {
          throw new CoreAdapterGapError(input.uiAction.affordanceId, `${dimension}.literal.kind must be INTEGER or DECIMAL.`);
        }
        const property = literal.kind === 'INTEGER' ? 'integer' : 'decimal';
        const number = literal[property];
        if (typeof number !== 'number' || !Number.isFinite(number) || (literal.kind === 'INTEGER' && !Number.isSafeInteger(number))) {
          throw new CoreAdapterGapError(input.uiAction.affordanceId, `${dimension} has an invalid ${literal.kind} literal.`);
        }
        await selectLabel(input, kindControl, 'literal');
        await selectLabel(input, `${label} number type`, literal.kind);
        await setText(input, `${label} number`, String(number));
      } else {
        throw new CoreAdapterGapError(input.uiAction.affordanceId, `${dimension}.kind must be COLUMN or LITERAL.`);
      }
    }
  }
  const missingPolicy = optionalString(values, 'missing_input_policy');
  if (missingPolicy) await selectLabel(input, 'Missing input policy', missingPolicy);
  const outputName = optionalString(values, 'output_name');
  const outputLabel = optionalString(values, 'output_label');
  if (outputName) await setText(input, 'Output column name', outputName);
  if (outputLabel) await setText(input, 'Output column label', outputLabel);
  const dividePolicy = optionalString(values, 'division_by_zero_policy');
  if (dividePolicy) await selectLabel(input, 'Division by zero policy', dividePolicy);
  if (input.requestStep?.actionId === 'proposalEndpoint') {
    await screen.findByTestId('construction-proposal-loading');
  }
};

const driveProposalLifecycle = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const actionId = input.requestStep?.actionId;
  if (input.requestStep === null && values.localAction === 'cancel_construction_proposal') {
    await clickTestId(input, 'construction-cancel-proposal', 'Cancel proposal');
    return;
  }
  switch (actionId) {
    case 'edit_or_remove_construction_step': {
      const stepId = requiredString(input, values, 'stepId');
      await clickTestId(input, `construction-history-step-${stepId}`, `saved construction step ${stepId}`);
      if (values.operation === 'remove') {
        await clickTestId(input, `construction-remove-step-${stepId}`, `remove saved construction step ${stepId}`);
      } else if (values.operation !== 'edit') {
        throw new CoreAdapterGapError(input.uiAction.affordanceId, 'edit_or_remove_construction_step requires operation "edit" or "remove".');
      }
      return;
    }
    case 'review_construction_change':
      await screen.findByTestId('construction-proposal-ready');
      return;
    case 'accept_construction_proposal':
      await clickTestId(input, 'construction-apply-proposal', 'Apply proposal');
      return;
    case 'retry_construction_preview':
      await clickTestId(input, 'construction-retry-proposal', 'Retry proposal');
      return;
    default:
      throw new CoreAdapterGapError(input.uiAction.affordanceId, `proposal lifecycle actionId ${actionId ?? '(missing)'} is not supported.`);
  }
};

const driveStepEditorRouting = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  if (input.requestStep?.actionId !== 'select_operation_or_saved_step') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `step editor routing requires actionId "select_operation_or_saved_step", received ${String(input.requestStep?.actionId)}.`);
  }
  const stepId = requiredString(input, values, 'stepId');
  await clickTestId(input, `construction-history-step-${stepId}`, `saved construction step ${stepId}`);
  await waitFor(() => {
    if (!screen.queryByTestId('construction-operation-editor') &&
      !screen.queryByTestId('construction-related-source-editor') &&
      !screen.queryByTestId('construction-related-field-editor')) {
      throw new Error(`Saved step ${stepId} did not route to a mounted operation editor.`);
    }
  });
};

const driveGroupedRowValuePolicy = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  await openConstructionFamily(input, 'ADD_COLUMNS');
  const policy = requiredString(input, values, 'rowValuePolicy');
  await selectLabel(input, 'Values per grouped row', policy);
};

const driveRelatedField = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  await openConstructionFamily(input, 'ADD_COLUMNS');
  await selectAddColumnsView(input, 'fields');
  if (hasTaggedBrowseSource(values)) await selectAddSource(input, values);
  const fieldChoice = optionalString(values, 'fieldChoiceLabel', 'field_choice_label');
  const outputName = optionalString(values, 'outputName', 'output_name');
  const outputLabel = optionalString(values, 'outputLabel', 'output_label');
  const action = actionName(input, values);
  if (action === 'activeRelatedFieldEndpoint' || action === 'relatedFieldChoices') {
    await screen.findByTestId('construction-related-field-editor');
    return;
  }
  if (!fieldChoice || !outputName || !outputLabel) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'related-field selection requires fieldChoiceLabel, outputName, and outputLabel.');
  }
  await clickNamedButton(input, new RegExp(escapeRegExp(fieldChoice)));
  await setText(input, 'Output name', outputName);
  await setText(input, 'Output label', outputLabel);
  await screen.findByTestId('construction-proposal-loading');
};

const driveColumnFilterPresentation = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const label = requiredString(input, values, 'columnLabel');
  const desired = values.enabled;
  if (typeof desired !== 'boolean') throw new CoreAdapterGapError(input.uiAction.affordanceId, 'column filter presentation requires boolean enabled.');
  const control = await screen.findByRole('checkbox', { name: `Use ${label} as filter` });
  if (!(control instanceof HTMLInputElement) || control.type !== 'checkbox') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `filter presentation for ${label} is not a mounted checkbox.`);
  }
  failIfUnavailable(input, control, `filter presentation for ${label}`);
  if (control.checked !== desired) fireEvent.click(control);
  await waitFor(() => {
    const current = screen.getByRole('checkbox', { name: `Use ${label} as filter` });
    if (!(current instanceof HTMLInputElement) || current.checked !== desired) throw new Error(`Filter presentation for ${label} did not update.`);
  });
};

const driveFirstTable = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const resourceType = requiredString(input, values, 'resourceType');
  const search = optionalString(values, 'searchQuery', 'search_query');
  if (search) await setText(input, 'Search row types', search);
  await clickNamedButton(input, `Choose ${resourceType} rows`, `Choose ${resourceType} rows`);
};

const driveRowDefinition = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const action = actionName(input, values);
  if (!screen.queryByRole('dialog', { name: 'Row definition settings' })) {
    await clickTestId(input, 'construction-rows-settings-trigger', 'row definition settings');
  }
  const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
  if (action === 'open_row_definition_settings') return;
  if (action === 'cancel_row_definition') {
    const close = within(dialog).queryByRole('button', { name: /close|cancel/i });
    if (!close) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'row-definition dialog has no visible cancel control.');
    fireEvent.click(close);
    return;
  }
  if (!isRowDefinitionAction(action)) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `row-definition actionId ${action ?? '(missing)'} is unsupported.`);
  }
  if (action === 'apply_row_definition') {
    await clickNamedButton(input, 'Apply row definition');
    return;
  }
  const intent = optionalString(values, 'intent');
  if (intent === 'group') await clickTestId(input, 'construction-action-group-rows', 'Group rows');
  if (intent === 'reshape') await clickTestId(input, 'construction-row-intent-reshape', 'reshape row intent');
  if (intent === 'records') await clickTestId(input, 'construction-row-intent-records', 'record rows intent');
  const rowOperation = optionalString(values, 'rowOperation', 'row_operation');
  if (rowOperation) {
    const rowButton = screen.queryByTestId(`construction-action-${rowOperation}-rows`);
    if (!rowButton) throw new CoreAdapterGapError(input.uiAction.affordanceId, `row operation ${rowOperation} is not mounted.`);
    failIfUnavailable(input, rowButton, `row operation ${rowOperation}`);
    fireEvent.click(rowButton);
  }
  const count = optionalString(values, 'rowsPerStartingRecord', 'rows_per_starting_record');
  if (count) await selectLabel(input, 'Rows per starting record', count);
};

const driveRootChange = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const rootType = optionalString(values, 'resourceType', 'rootResourceType', 'root_resource_type');
  const action = actionName(input, values);
  const selectionAction = action === 'change_connected_row_root' || action === 'replace_table_root';
  const reviewAction = action === 'review_connected_row_root' || action === 'review_root_replacement';
  const applyAction = action === 'apply_connected_row_root' || action === 'apply_root_replacement';
  if (!selectionAction && !reviewAction && !applyAction) {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `root-change actionId ${action ?? '(missing)'} is unsupported.`);
  }
  if (!screen.queryByRole('dialog', { name: 'Row definition settings' })) {
    await clickTestId(input, 'construction-rows-settings-trigger', 'row definition settings');
  }
  const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
  if (reviewAction) {
    await clickNamedButton(input, /Review.*row change|Preview.*row change|Check.*row change/i);
    return;
  }
  if (applyAction) {
    await clickNamedButton(input, /Apply.*row change|Apply.*replacement/i);
    return;
  }
  const select = within(dialog).queryByRole('combobox', { name: 'Record type' });
  if (!select) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'root change requires the mounted Record type selector.');
  if (rootType) await selectLabel(input, 'Record type', rootType);
  if (action === 'replace_table_root') {
    const replace = within(dialog).queryByRole('button', { name: /Replace starting record type/ });
    if (!replace) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'the selected root has no replace action in the mounted dialog.');
    fireEvent.click(replace);
    return;
  }
  if (!rootType) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'root-change action requires resourceType or an explicit resolve/replace action.');
};

const driveExplorerManagement = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const action = actionName(input, values);
  if (action === 'select_explorer') {
    await selectLabel(input, 'Explorer', requiredString(input, values, 'explorerId'));
    return;
  }
  if (action === 'delete_explorer') {
    const button = await screen.findByRole('button', { name: 'Delete explorer' });
    failIfUnavailable(input, button, 'Delete explorer');
    fireEvent.click(button);
    return;
  }
  if (action === 'create_explorer') {
    const name = requiredString(input, values, 'name');
    const summary = await screen.findByText('New explorer', { exact: true });
    fireEvent.click(summary);
    await setText(input, 'Explorer name', name);
    const create = await screen.findByRole('button', { name: /^Create (blank|copy)$/ });
    failIfUnavailable(input, create, 'Create explorer');
    fireEvent.click(create);
    return;
  }
  throw new CoreAdapterGapError(input.uiAction.affordanceId, `explorer management action ${action ?? '(missing)'} is unsupported.`);
};

const driveRestorePreviousDraft = async (input: CoreAdapterInput): Promise<void> => {
  const action = actionName(input, valuesOf(input));
  if (action !== 'undo_last_saved_draft_change') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, `restore action ${action ?? '(missing)'} is unsupported.`);
  }
  await clickTestId(input, 'construction-undo', 'Undo last saved draft change');
};

const driveReconciliation = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const action = actionName(input, values);
  if (action === 'compile_current_builder_draft') {
    await clickNamedButton(input, 'Recompile');
    return;
  }
  if (action === 'refresh_after_stale_or_desynchronized_draft') {
    await clickTestId(input, 'builder-suggestions-retry', 'Retry finding columns');
    return;
  }
  if (action === 'save_builder_draft_change') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'save_builder_draft_change is issued by the owning editor interaction, not a standalone mounted control.');
  }
  if (action === 'observe_reconciliation_state') {
    const state = optionalString(values, 'reconciliationState', 'reconciliation_state');
    if (!state) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'reconciliation observation requires reconciliationState.');
    await screen.findByText(new RegExp(escapeRegExp(state), 'i'));
    return;
  }
  throw new CoreAdapterGapError(input.uiAction.affordanceId, `reconciliation action ${action ?? '(missing)'} has no mounted control driver.`);
};

const driveRouteSelection = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const relation = optionalString(values, 'relationship', 'edgeLabel');
  const action = actionName(input, values);
  if (action === 'select_route_occurrence') {
    const occurrenceName = requiredString(input, values, 'occurrenceLabel');
    const occurrence = await screen.findByText(occurrenceName, { exact: true });
    fireEvent.click(occurrence);
    return;
  }
  if (action === 'edit_table_route' && relation) {
    const currentTraversal = screen.queryByRole('combobox', { name: 'Relationship for occurrence' });
    if (currentTraversal) {
      await selectLabel(input, 'Relationship for occurrence', relation);
      return;
    }
    const addRelationship = screen.queryByRole('combobox', { name: 'Relationship to add' });
    if (addRelationship) {
      await selectLabel(input, 'Relationship to add', relation);
      await clickNamedButton(input, 'Add branch');
      return;
    }
  }
  throw new CoreAdapterGapError(input.uiAction.affordanceId, `route actionId ${action ?? '(missing)'} has no matching mounted occurrence or relationship control.`);
};

const driveAutomaticPreview = async (input: CoreAdapterInput): Promise<void> => {
  const action = actionName(input, valuesOf(input));
  if (action === 'retry_preview_once' || action === 'refresh_preview_catalog') {
    const retry = screen.queryByRole('button', { name: /refresh preview|retry preview/i });
    if (!retry) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'the mounted preview has no refresh/retry control.');
    failIfUnavailable(input, retry, 'preview refresh');
    fireEvent.click(retry);
  }
  const preview = screen.queryByRole('region', { name: /preview/i }) ?? screen.queryByTestId('construction-preview-stale-notice');
  if (!preview) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'the real preview surface is not mounted in this Builder fixture.');
  if (['preview_selected_output', 'retry_preview_once', 'refresh_preview_catalog', 'rebuild_preview_after_catalog_refresh',
    'fetch_preview_after_catalog_refresh', 'rebuild_stale_preview_receipt', 'refresh_preview_after_recompile'].includes(action ?? '')) return;
  throw new CoreAdapterGapError(input.uiAction.affordanceId, `preview actionId ${action ?? '(missing)'} is unsupported.`);
};

const driveDeleteNoFallback = async (input: CoreAdapterInput): Promise<void> => {
  const button = await screen.findByRole('button', { name: 'Delete explorer' });
  const style = getComputedStyle(button);
  const visible = !button.closest('[hidden],[aria-hidden="true"]') && style.display !== 'none' && style.visibility !== 'hidden';
  if (!visible) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'Delete explorer is not visibly mounted.');
  if (!button.hasAttribute('disabled') && button.getAttribute('aria-disabled') !== 'true') {
    throw new CoreAdapterGapError(input.uiAction.affordanceId, 'Delete explorer is enabled; this witness expects the no-fallback safety-disabled state.');
  }
};

const driveWorkspaceHydration = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const explorer = optionalString(values, 'explorerId', 'selectedExplorerId');
  const control = await screen.findByLabelText('Explorer');
  if (!(control instanceof HTMLSelectElement)) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'Explorer selection is not a native select.');
  if (explorer) await selectLabel(input, 'Explorer', explorer);
  else if (!control.value) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'mounted Builder has no selected explorer to observe.');
};

const driveTableShapeDerived = async (input: CoreAdapterInput): Promise<void> => {
  const values = valuesOf(input);
  const label = optionalString(values, 'outputLabel', 'output_label');
  const shapeEditor = screen.queryByRole('region', { name: /table shape/i });
  if (!shapeEditor) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'the legacy TableShape settings editor is not mounted by this Builder workspace state.');
  const outputName = optionalString(values, 'outputName', 'output_name');
  if (outputName) await setText(input, 'Derived column 1 output name', outputName);
  if (label) await setText(input, 'Derived column 1 output label', label);
  if (!outputName && !label) throw new CoreAdapterGapError(input.uiAction.affordanceId, 'table-shape derived column requires outputName or outputLabel.');
};

const driveDemoEntryRoute = async (input: CoreAdapterInput): Promise<void> => {
  throw new CoreAdapterGapError(input.uiAction.affordanceId, 'demo entry routing belongs to the outer Builder/router; this adapter is invoked inside the mounted BuilderWorkspace and cannot observe that route boundary.');
};

const adapter = (
  id: string,
  adapterId: string,
  runnerActionId: string,
  sourceRefs: ReadonlyArray<string>,
  drive: (input: CoreAdapterInput) => Promise<void> | void,
): CoreAdapter => ({
  adapterId,
  runnerActionId,
  sourceRefs,
  drive: async (input) => {
    if (input.uiAction.affordanceId !== id || input.uiAction.runnerActionId !== runnerActionId) {
      throw new CoreAdapterGapError(id, `received action binding ${input.uiAction.affordanceId}/${input.uiAction.runnerActionId}.`);
    }
    await drive(input);
  },
});

export const coreAdapters = {
  'builder.add_columns.catalog_field': adapter(
    'builder.add_columns.catalog_field', 'core.add_columns.catalog_field', 'core.driveCatalogField',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3684', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ConceptCatalog.tsx:1163', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ConceptCatalog.tsx:1505'],
    (input) => driveCatalogChoice(input, false),
  ),
  'builder.add_columns.catalog_semantic': adapter(
    'builder.add_columns.catalog_semantic', 'core.add_columns.catalog_semantic', 'core.driveCatalogSemantic',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3637', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ConceptCatalog.tsx:1045', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ConceptCatalog.tsx:1735'],
    (input) => driveCatalogChoice(input, true),
  ),
  'builder.add_columns.grouped_row_values': adapter(
    'builder.add_columns.grouped_row_values', 'core.add_columns.grouped_row_values', 'core.driveGroupedRowValuePolicy',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3433', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3652'],
    driveGroupedRowValuePolicy,
  ),
  'builder.add_columns.related_field': adapter(
    'builder.add_columns.related_field', 'core.add_columns.related_field', 'core.driveRelatedField',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1557', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/RelatedFieldEditor.tsx:134', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/RelatedFieldEditor.tsx:237'],
    driveRelatedField,
  ),
  'builder.add_columns.source_selection': adapter(
    'builder.add_columns.source_selection', 'core.add_columns.source_selection', 'core.driveAddColumnsSourceSelection',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1430', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1511', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1522'],
    async (input) => {
      const values = valuesOf(input);
      await openConstructionFamily(input, 'ADD_COLUMNS');
      const view = optionalString(values, 'view') === 'coded' ? 'coded' : 'fields';
      await selectAddColumnsView(input, view);
      await selectAddSource(input, values);
    },
  ),
  'builder.filter_rows.related_eligibility': adapter(
    'builder.filter_rows.related_eligibility', 'core.filter_rows.related_eligibility', 'core.driveRelatedEligibility',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/FilterRowsEditor.tsx:37', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/RelatedEligibilityEditor.tsx:186'],
    driveRelatedEligibility,
  ),
  'builder.filter_rows.scalar_condition': adapter(
    'builder.filter_rows.scalar_condition', 'core.filter_rows.scalar_condition', 'core.driveScalarFilterCondition',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationEditor.tsx:784', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationEditor.tsx:846', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationEditor.tsx:868'],
    driveScalarFilter,
  ),
  'builder.calculate.derive': adapter(
    'builder.calculate.derive', 'core.calculate.derive', 'core.driveDerivedCalculation',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4432', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationEditor.tsx:1135', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionOperationEditor.tsx:1178'],
    driveCalculate,
  ),
  'builder.calculate.table_shape_derived_column': adapter(
    'builder.calculate.table_shape_derived_column', 'core.calculate.table_shape_derived_column', 'core.driveTableShapeDerivedColumn',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4865', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/DerivedColumnsEditor.tsx:242', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/DerivedColumnsEditor.tsx:268'],
    driveTableShapeDerived,
  ),
  'builder.column_filter_presentation': adapter(
    'builder.column_filter_presentation', 'core.column_filter_presentation', 'core.driveColumnFilterPresentation',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/ColumnSelector.tsx:298', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ColumnSelector.tsx:440'],
    driveColumnFilterPresentation,
  ),
  'builder.automatic_preview_refresh_and_recovery': adapter(
    'builder.automatic_preview_refresh_and_recovery', 'core.automatic_preview_refresh_and_recovery', 'core.driveAutomaticPreviewRecovery',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2608', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2911', 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/previewRecovery.ts:32'],
    driveAutomaticPreview,
  ),
  'builder.construction_proposal_apply_cancel_retry': adapter(
    'builder.construction_proposal_apply_cancel_retry', 'core.construction_proposal_apply_cancel_retry', 'core.driveConstructionProposalLifecycle',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1800', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.ts:233', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionProposalPanel.tsx:307'],
    driveProposalLifecycle,
  ),
  'builder.construction_step_editor_routing': adapter(
    'builder.construction_step_editor_routing', 'core.construction_step_editor_routing', 'core.driveConstructionStepEditorRouting',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1017', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1036', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.ts:173'],
    driveStepEditorRouting,
  ),
  'builder.delete_explorer_no_fallback': adapter(
    'builder.delete_explorer_no_fallback', 'core.delete_explorer_no_fallback', 'core.observeDeleteExplorerDisabled',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3136', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/BuilderToolbar.tsx:354', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/BuilderToolbar.tsx:361'],
    driveDeleteNoFallback,
  ),
  'builder.demo_entry_route': adapter(
    'builder.demo_entry_route', 'core.demo_entry_route', 'core.driveDemoEntryRoute',
    ['ui/packages/loom-ui/src/Builder.tsx:85', 'ui/packages/loom-ui/src/Builder.tsx:119'],
    driveDemoEntryRoute,
  ),
  'builder.explorer_create_delete': adapter(
    'builder.explorer_create_delete', 'core.explorer_create_delete', 'core.driveExplorerManagement',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2443', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2461', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/BuilderToolbar.tsx:72'],
    driveExplorerManagement,
  ),
  'builder.first_table_from_root': adapter(
    'builder.first_table_from_root', 'core.first_table_from_root', 'core.driveFirstTableFromRoot',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2911', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowRootPicker.tsx:43', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowRootPicker.tsx:57'],
    driveFirstTable,
  ),
  'builder.restore_previous_draft_undo': adapter(
    'builder.restore_previous_draft_undo', 'core.restore_previous_draft_undo', 'core.driveRestorePreviousDraft',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3384', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4053', 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx:80'],
    driveRestorePreviousDraft,
  ),
  'builder.root.change_or_replace': adapter(
    'builder.root.change_or_replace', 'core.root.change_or_replace', 'core.driveRootChangeOrReplace',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2032', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2102', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionPanel.tsx:141'],
    driveRootChange,
  ),
  'builder.row_definition_proposal_apply_cancel': adapter(
    'builder.row_definition_proposal_apply_cancel', 'core.row_definition_proposal_apply_cancel', 'core.driveRowDefinitionLifecycle',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:718', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4403', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:520'],
    driveRowDefinition,
  ),
  'builder.server_saved_draft_and_reconcile': adapter(
    'builder.server_saved_draft_and_reconcile', 'core.server_saved_draft_and_reconcile', 'core.driveServerDraftReconciliation',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2500', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2561', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:778'],
    driveReconciliation,
  ),
  'builder.table_route_and_occurrence_selection': adapter(
    'builder.table_route_and_occurrence_selection', 'core.table_route_and_occurrence_selection', 'core.driveTableRouteSelection',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:2274', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4615', 'ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx:610'],
    driveRouteSelection,
  ),
  'builder.workspace_hydration_and_explorer_selection': adapter(
    'builder.workspace_hydration_and_explorer_selection', 'core.workspace_hydration_and_explorer_selection', 'core.observeWorkspaceHydrationAndSelectExplorer',
    ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:427', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:447', 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:455'],
    driveWorkspaceHydration,
  ),
} satisfies Readonly<Record<string, CoreAdapter>>;
