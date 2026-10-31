import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { hasRowValuePolicy } from './shapeCombinePolicy';

type AdapterInput = {
  readonly uiAction: {
    readonly affordanceId: string;
    readonly runnerActionId: string;
    readonly values: Readonly<Record<string, unknown>>;
  };
  readonly requestStep: Readonly<Record<string, unknown>> | null;
};

type ShapeCombineAdapter = {
  readonly adapterId: string;
  readonly runnerActionId: string;
  readonly sourceRefs: ReadonlyArray<string>;
  readonly drive: (input: AdapterInput) => Promise<void>;
};

const IDS = {
  expand: 'builder.reshape.expand_scalar_list',
  codedGroup: 'builder.reshape.group_coded',
  codedPivot: 'builder.reshape.pivot_coded',
  pivot: 'builder.reshape.pivot_values',
  relatedExpand: 'builder.reshape.related_expand',
  unpivot: 'builder.reshape.unpivot',
  append: 'builder.combine.append',
  keyJoin: 'builder.combine.key_join',
  membership: 'builder.combine.membership',
  legacyPivot: 'legacy.table_shape.grouped_pivot',
  legacyUnpivot: 'legacy.table_shape.unpivot',
  expandedRows: 'builder.row_definition.expanded_source_rows',
  explicitGroups: 'builder.row_definition.explicit_groups',
  rowShape: 'builder.row_definition.rows_per_starting_record',
  relatedSource: 'builder.related_source_column',
} as const;

type AffordanceId = typeof IDS[keyof typeof IDS];

const isRecord = (value: unknown): value is Readonly<Record<string, unknown>> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const nonEmptyString = (value: unknown): string | undefined =>
  typeof value === 'string' && value.trim().length > 0 ? value : undefined;

const idFrom = (value: unknown): string | undefined => {
  const direct = nonEmptyString(value);
  if (direct) return direct;
  if (!isRecord(value)) return undefined;
  for (const key of ['choiceId', 'columnId', 'inputColumnId', 'outputColumnId', 'anchorColumnId', 'rowChoiceId', 'fieldId', 'sourceId', 'shapeValue', 'codingPath', 'inputKey', 'key', 'value', 'id']) {
    const candidate = nonEmptyString(value[key]);
    if (candidate) return candidate;
  }
  return undefined;
};

const dimension = (input: AdapterInput, id: string): unknown => {
  if (!(id in input.uiAction.values)) throw new Error(`Missing inventory dimension ${id}.`);
  return input.uiAction.values[id];
};

const requiredId = (input: AdapterInput, id: string): string => {
  const found = idFrom(dimension(input, id));
  if (!found) throw new Error(`Inventory dimension ${id} must identify a mounted UI choice.`);
  return found;
};

const listValues = (input: AdapterInput, id: string): ReadonlyArray<unknown> => {
  const value = dimension(input, id);
  if (!Array.isArray(value)) throw new Error(`Inventory dimension ${id} must be an array.`);
  return value;
};

const listIds = (input: AdapterInput, id: string): ReadonlyArray<string> => {
  const ids = listValues(input, id).map(idFrom);
  if (ids.some((value) => value === undefined)) throw new Error(`Inventory dimension ${id} contains an item without a UI choice id.`);
  return ids.filter((value): value is string => value !== undefined);
};

const textPair = (value: unknown, dimensionId: string): { readonly name: string; readonly label: string } => {
  const output = isRecord(value) && isRecord(value.output) ? value.output : value;
  if (!isRecord(output)) throw new Error(`Inventory dimension ${dimensionId} must include name and label text.`);
  const name = nonEmptyString(output.name) ?? nonEmptyString(output.column);
  const label = nonEmptyString(output.label);
  if (!name || !label) throw new Error(`Inventory dimension ${dimensionId} must include non-empty name and label text.`);
  return { name, label };
};

const selectValue = async (label: string, value: string): Promise<void> => {
  await waitFor(() => {
    const currentControl = screen.getByLabelText(label);
    if (!(currentControl instanceof HTMLSelectElement)) throw new Error(`Expected ${label} to be a select control.`);
    const available = Array.from(currentControl.options).some((option) => option.value === value);
    if (!available) throw new Error(`The mounted ${label} selector has not loaded ${value}.`);
  }, { timeout: 30_000 });
  const control = screen.getByLabelText(label);
  if (!(control instanceof HTMLSelectElement)) throw new Error(`Expected ${label} to be a select control.`);
  const offered = Array.from(control.options).find((option) => option.value === value);
  if (!offered || offered.disabled) throw new Error(`The mounted ${label} selector does not offer ${value}.`);
  if (control.value !== value) fireEvent.change(control, { target: { value } });
  const updated = await screen.findByLabelText(label);
  if (!(updated instanceof HTMLSelectElement) || updated.value !== value) {
    throw new Error(`The mounted ${label} selector did not accept ${value}.`);
  }
};

const textValue = async (label: string, value: string): Promise<void> => {
  const control = await screen.findByLabelText(label);
  if (!(control instanceof HTMLInputElement) && !(control instanceof HTMLTextAreaElement)) {
    throw new Error(`Expected ${label} to be a text input.`);
  }
  fireEvent.change(control, { target: { value } });
  const updated = await screen.findByLabelText(label);
  if (!(updated instanceof HTMLInputElement) && !(updated instanceof HTMLTextAreaElement)) {
    throw new Error(`The mounted ${label} control was replaced by an unsupported element.`);
  }
  if (updated.value !== value) throw new Error(`The mounted ${label} control did not accept its assigned text.`);
};

const setCheckbox = async (name: string, checked: boolean): Promise<void> => {
  const control = await screen.findByRole('checkbox', { name });
  if (!(control instanceof HTMLInputElement)) throw new Error(`Expected ${name} to be a checkbox.`);
  if (control.checked !== checked) fireEvent.click(control);
  if (control.checked !== checked) throw new Error(`The mounted ${name} checkbox did not accept its assigned state.`);
};

const openDetails = async (testId: string): Promise<void> => {
  const details = await screen.findByTestId(testId);
  if (!(details instanceof HTMLDetailsElement)) throw new Error(`Expected ${testId} to be a disclosure control.`);
  details.open = true;
};

const click = async (testId: string): Promise<void> => {
  const control = await screen.findByTestId(testId);
  if (!(control instanceof HTMLButtonElement) || control.disabled) throw new Error(`The mounted ${testId} action is unavailable.`);
  fireEvent.click(control);
};

const readyConstructionProposal = async (): Promise<void> => {
  await screen.findByTestId('construction-proposal-ready', {}, { timeout: 30_000 });
};

const requireAction = (input: AdapterInput, affordanceId: AffordanceId, runnerActionId: string): void => {
  if (input.uiAction.affordanceId !== affordanceId) {
    throw new Error(`Shape/combine driver expected ${affordanceId}, received ${input.uiAction.affordanceId}.`);
  }
  if (input.uiAction.runnerActionId !== runnerActionId) {
    throw new Error(`Shape/combine driver expected ${runnerActionId}, received ${input.uiAction.runnerActionId}.`);
  }
};

const openRowsSettings = async (): Promise<void> => {
  const trigger = await screen.findByTestId('construction-rows-settings-trigger');
  if (!(trigger instanceof HTMLButtonElement)) throw new Error('Rows settings trigger is not a button.');
  fireEvent.click(trigger);
  await screen.findByRole('dialog', { name: 'Row definition settings' });
  await screen.findByRole('combobox', { name: 'Rows per starting record' });
};

const openReshapeChooser = async (intent: 'group' | 'reshape'): Promise<void> => {
  const moreActions = await screen.findByTestId('construction-more-actions');
  fireEvent.click(moreActions);
  const menu = await screen.findByTestId('construction-more-actions-menu');
  fireEvent.click(within(menu).getByTestId('construction-action-reshape'));
  await screen.findByTestId(`construction-reshape-intent-${intent}`);
  await click(`construction-reshape-intent-${intent}`);
};

const openRowReshapeMenu = async (): Promise<void> => {
  await openRowsSettings();
  await click('construction-row-intent-reshape');
};

const clickRequestApplyIfCommand = async (input: AdapterInput, buttonTestId: string): Promise<void> => {
  const kind = nonEmptyString(input.requestStep?.kind)?.toUpperCase();
  const actionId = nonEmptyString(input.requestStep?.actionId)?.toLowerCase() ?? '';
  if (kind?.includes('COMMAND') || actionId.includes('apply') || actionId.includes('confirm')) {
    if (buttonTestId === 'construction-proposal-apply') await clickButtonByName('Apply change');
    else await click(buttonTestId);
  }
};

const driveExpandScalarList = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.expand, 'driveExpandScalarList');
  const columnId = requiredId(input, 'scalar_list_input');
  const columnChoice = dimension(input, 'scalar_list_input');
  const label = isRecord(columnChoice) ? nonEmptyString(columnChoice.label) : undefined;
  if (!label) throw new Error(`Could not read the visible label for list column ${columnId}.`);
  const button = await screen.findByRole('button', { name: `Split ${label} values into rows` });
  fireEvent.click(button);
  await screen.findByTestId('construction-reshape-expand');
  await selectValue('Repeated field', columnId);
  const output = textPair(dimension(input, 'item_output_name_and_label'), 'item_output_name_and_label');
  await openDetails('construction-reshape-expand-advanced');
  await textValue('Expanded item name', output.name);
  await textValue('Expanded item label', output.label);
  const position = dimension(input, 'position_output');
  const includePosition = position === 'include' || position === true || (isRecord(position) && position.kind === 'include');
  await setCheckbox('Include item position', includePosition);
  if (includePosition) {
    if (isRecord(position)) {
      const ordinal = textPair(position, 'position_output');
      await textValue('Position column name', ordinal.name);
      await textValue('Position column label', ordinal.label);
    }
  }
  await selectValue('Empty list policy', requiredId(input, 'empty_list_policy'));
  await readyConstructionProposal();
  await clickRequestApplyIfCommand(input, 'construction-proposal-apply');
};

const driveCodedGroup = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.codedGroup, 'driveCodedGroup');
  await openReshapeChooser('group');
  await openDetails('construction-reshape-advanced-grouping');
  await click('construction-reshape-choice-coded-group');
  await screen.findByTestId('construction-reshape-coded-group');
  await selectValue('Coding field', requiredId(input, 'coded_group_choice'));
  const outputChoices = listIds(input, 'coded_group_outputs');
  const expectedOutputs = ['system', 'version', 'code', 'source_record_count'];
  if (outputChoices.length !== expectedOutputs.length || expectedOutputs.some((value) => !outputChoices.includes(value))) {
    throw new Error('Coded group output dimension must preserve the four mounted output roles.');
  }
  await selectValue('When a source record has no code or system', requiredId(input, 'missing_key_policy'));
  await readyConstructionProposal();
  await clickRequestApplyIfCommand(input, 'construction-proposal-apply');
};

const drivePivotValues = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.pivot, 'drivePivotValues');
  await openRowReshapeMenu();
  const tablePivot = screen.queryByTestId('construction-action-table-pivot-rows');
  if (tablePivot) fireEvent.click(tablePivot);
  else {
    await click('construction-action-pivot-rows');
    await click('construction-reshape-intent-reshape');
    await click('construction-reshape-choice-pivot');
  }
  await screen.findByTestId('construction-reshape-pivot');
  const groupKeys = listIds(input, 'group_key_column');
  for (let index = 0; index < groupKeys.length; index += 1) {
    const item = listValues(input, 'group_key_column')[index];
    const groupLabel = isRecord(item) ? nonEmptyString(item.label) : undefined;
    if (!groupLabel) throw new Error('group_key_column items must include the visible choice label.');
    const label = `Pivot group ${groupLabel}`;
    await setCheckbox(label, true);
  }
  await selectValue('Pivot category field', requiredId(input, 'category_column'));
  await selectValue('Pivot values field', requiredId(input, 'value_column'));
  const categoryDetails = await screen.findByTestId('construction-reshape-pivot-categories');
  if (categoryDetails instanceof HTMLDetailsElement) categoryDetails.open = true;
  const discover = screen.queryByTestId('construction-reshape-pivot-discover-categories');
  if (discover) fireEvent.click(discover);
  await screen.findByTestId('construction-reshape-pivot-category-status', {}, { timeout: 30_000 });
  const categories = listValues(input, 'pivot_category');
  if (categories.length === 0) throw new Error('Pivot requires at least one discovered category.');
  for (const category of categories) {
    const label = isRecord(category) ? nonEmptyString(category.label) : nonEmptyString(category);
    if (!label) throw new Error('pivot_category items must include a visible category label.');
    await setCheckbox(`Include category ${label}`, true);
  }
  await openDetails('construction-reshape-pivot-advanced');
  await selectValue('Pivot duplicate policy', requiredId(input, 'duplicate_policy'));
  await selectValue('Pivot missing cell policy', requiredId(input, 'missing_cell_policy'));
  await selectValue('Pivot unlisted category policy', requiredId(input, 'unlisted_category_policy'));
  await readyConstructionProposal();
  await clickRequestApplyIfCommand(input, 'construction-proposal-apply');
};

const driveUnpivot = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.unpivot, 'driveUnpivot');
  await openRowReshapeMenu();
  await click('construction-action-unpivot-rows');
  await screen.findByTestId('construction-reshape-unpivot');
  const inputIds = listIds(input, 'input_column');
  if (inputIds.length === 0) throw new Error('Construction unpivot requires at least one selected input column.');
  for (let index = 0; index < inputIds.length; index += 1) {
    const column = listValues(input, 'input_column')[index];
    const label = isRecord(column) ? nonEmptyString(column.label) : undefined;
    if (!label) throw new Error('input_column items must include the visible source-column label.');
    await setCheckbox(`Unpivot ${label}`, true);
  }
  const keyValue = dimension(input, 'key_value');
  const outputDimension = dimension(input, 'output_columns');
  const outputs = Array.isArray(outputDimension)
    ? outputDimension
    : isRecord(outputDimension) ? [outputDimension.key, outputDimension.value] : [];
  if (outputs.length !== 2) throw new Error('Construction unpivot requires exactly key and value output specifications.');
  for (const [index, label] of ['Unpivot key', 'Unpivot value'].entries()) {
    const output = textPair(outputs[index], 'output_columns');
    await textValue(`${label} output name`, output.name);
    await textValue(`${label} output label`, output.label);
  }
  const typedKeys = Array.isArray(keyValue) ? keyValue : inputIds.map(() => keyValue);
  if (typedKeys.length !== inputIds.length) throw new Error('key_value must provide one typed scalar key for each input column.');
  for (let index = 0; index < inputIds.length; index += 1) {
    const key = typedKeys[index];
    if (!isRecord(key)) throw new Error('Each key_value item must include a scalar kind and value.');
    const keyType = nonEmptyString(key.kind);
    if (!keyType || !['STRING', 'INTEGER', 'DECIMAL', 'BOOLEAN'].includes(keyType)) {
      throw new Error('Unpivot keys must use STRING, INTEGER, DECIMAL, or BOOLEAN scalar values.');
    }
    await selectValue(`Unpivot key type ${inputIds[index]}`, keyType);
    if (keyType === 'BOOLEAN') await selectValue(`Unpivot key value ${inputIds[index]}`, String(key.value));
    else await textValue(`Unpivot key value ${inputIds[index]}`, String(key.value ?? ''));
  }
  await selectValue('Unpivot null row policy', requiredId(input, 'null_row_policy'));
  await readyConstructionProposal();
  await clickRequestApplyIfCommand(input, 'construction-proposal-apply');
};

const driveSourceExpansion = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.expandedRows, 'driveSourceExpansion');
  await openRowsSettings();
  const advanced = await screen.findByTestId('construction-advanced-row-operations');
  if (!(advanced instanceof HTMLDetailsElement)) throw new Error('Source expansion action is not in row settings.');
  advanced.open = true;
  await click('construction-reshape-expand-source');
  await screen.findByRole('dialog', { name: 'Expand a repeated source field' });
  await selectValue('Repeated source field', requiredId(input, 'repeated_source_field'));
  await selectValue('When a record has no values', requiredId(input, 'empty_collection_policy'));
  const apply = await screen.findByTestId('construction-source-expand-apply');
  if (!(apply instanceof HTMLButtonElement) || apply.disabled) {
    await screen.findByTestId('construction-source-expand-apply', {}, { timeout: 30_000 });
  }
  await clickRequestApplyIfCommand(input, 'construction-source-expand-apply');
};

const driveRowsPerStartingRecord = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.rowShape, 'driveRowsPerStartingRecord');
  await openRowsSettings();
  const rowShape = dimension(input, 'row_shape');
  const rowValue = isRecord(rowShape)
    ? nonEmptyString(rowShape.shapeValue) ?? nonEmptyString(rowShape.choiceId)
    : nonEmptyString(rowShape);
  if (!rowValue) throw new Error('row_shape must identify one mounted row-definition option.');
  await selectValue('Rows per starting record', rowValue);
  const choiceKind = isRecord(rowShape) ? nonEmptyString(rowShape.kind) : undefined;
  if (choiceKind === 'EXPANDED' && screen.queryByLabelText('Unmatched record policy')) {
    await selectValue('Unmatched record policy', requiredId(input, 'empty_collection_policy'));
  }
  if (choiceKind === 'EXPLICIT_GROUP' && screen.queryByLabelText('Unmatched record policy')) {
    await selectValue('Unmatched record policy', requiredId(input, 'unassigned_member_policy'));
  }
  const kind = nonEmptyString(input.requestStep?.kind)?.toUpperCase();
  if (kind?.includes('COMMAND')) await clickButtonByName('Apply row definition');
};

const clickButtonByName = async (name: string): Promise<void> => {
  const button = await screen.findByRole('button', { name });
  if (!(button instanceof HTMLButtonElement) || button.disabled) throw new Error(`The mounted ${name} action is unavailable.`);
  fireEvent.click(button);
};

const driveExplicitGroups = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.explicitGroups, 'driveExplicitGroups');
  const countValue = dimension(input, 'explicit_group_count');
  const count = typeof countValue === 'number' ? countValue : isRecord(countValue) ? countValue.count : undefined;
  if (typeof count !== 'number' || !Number.isSafeInteger(count) || count < 1 || count > 1000) {
    throw new Error('explicit_group_count must be an integer from 1 through the UI limit of 1000.');
  }
  const labels = isRecord(countValue) && Array.isArray(countValue.labels) ? countValue.labels : undefined;
  const shapeValue = isRecord(countValue) ? nonEmptyString(countValue.shapeValue) : undefined;
  if (!labels || labels.length !== count || labels.some((label) => typeof label !== 'string' || label.trim().length === 0) || !shapeValue) {
    throw new Error('Explicit-group inventory values lack authored group labels and the resulting dynamic row-shape value, so this driver cannot reproduce the exact selection.');
  }
  const memberValues = listValues(input, 'selection_member');
  const members = memberValues.map((member) => {
    if (!isRecord(member)) throw new Error('selection_member items must identify a member and its explicit group assignments.');
    const memberId = nonEmptyString(member.memberId) ?? nonEmptyString(member.id);
    const label = nonEmptyString(member.label);
    const groupIndexes = member.groupIndexes;
    if (!memberId || !label || !Array.isArray(groupIndexes) || groupIndexes.some((value) => typeof value !== 'number')) {
      throw new Error('selection_member items need memberId, visible label, and groupIndexes to drive exact UI membership.');
    }
    return { memberId, label, groupIndexes: groupIndexes.filter((value): value is number => typeof value === 'number') };
  });
  await openRowsSettings();
  await clickButtonByName('Create groups from this selection');
  await screen.findByRole('region', { name: 'Create explicit groups' });
  for (let index = 0; index < count; index += 1) {
    const name = `Group ${index + 1} name`;
    const label = labels[index];
    if (typeof label !== 'string') throw new Error('Missing explicit group label.');
    await textValue(name, label);
    if (index + 1 < count) await click('Add group');
  }
  for (const member of members) {
    for (let groupIndex = 0; groupIndex < count; groupIndex += 1) {
      const groupLabel = labels?.[groupIndex];
      if (typeof groupLabel !== 'string') throw new Error('Missing authored group label for membership selector.');
      const checkbox = await screen.findByRole('checkbox', { name: `Assign ${member.label} to ${groupLabel}` });
      if (!(checkbox instanceof HTMLInputElement)) throw new Error('Explicit group assignment control is not a checkbox.');
      const selected = member.groupIndexes.includes(groupIndex);
      if (checkbox.checked !== selected) fireEvent.click(checkbox);
    }
  }
  await clickButtonByName('Create group revision');
  await screen.findByRole('combobox', { name: 'Rows per starting record' });
  await selectValue('Rows per starting record', shapeValue);
  await selectValue('Unmatched record policy', requiredId(input, 'unassigned_member_policy'));
  const kind = nonEmptyString(input.requestStep?.kind)?.toUpperCase();
  if (kind?.includes('COMMAND')) await clickButtonByName('Apply row definition');
};

const parseSourceKey = (value: unknown): string => {
  const direct = nonEmptyString(value);
  if (direct) return direct;
  if (isRecord(value)) {
    const key = nonEmptyString(value.inputKey) ?? nonEmptyString(value.key) ?? nonEmptyString(value.sourceKey);
    if (key) return key;
    if (typeof value.kind === 'string' && typeof value.id === 'string') return JSON.stringify([value.kind, value.id]);
  }
  throw new Error('Each input_tables item must identify an exact mounted input option value.');
};

const outputFieldSpecs = (input: AdapterInput): ReadonlyArray<Readonly<Record<string, unknown>>> => {
  const outputs = listValues(input, 'output_projection').map((value) => {
    if (!isRecord(value)) throw new Error('Every output_projection item must include a named output and source mapping.');
    return value;
  });
  if (outputs.length === 0) throw new Error('At least one output projection is required.');
  return outputs;
};

const ensureCombine = async (kind: 'APPEND' | 'KEY_JOIN' | 'MEMBERSHIP'): Promise<void> => {
  const moreActions = await screen.findByTestId('construction-more-actions');
  fireEvent.click(moreActions);
  const menu = await screen.findByTestId('construction-more-actions-menu');
  fireEvent.click(within(menu).getByTestId('construction-action-combine'));
  await screen.findByTestId('construction-combine-editor');
  await click(`construction-combine-choice-${kind.toLowerCase()}`);
};

const configureCombine = async (input: AdapterInput, kind: 'APPEND' | 'KEY_JOIN' | 'MEMBERSHIP'): Promise<void> => {
  const sourceValues = listValues(input, 'input_tables');
  const inputKeys = sourceValues.map(parseSourceKey);
  if (inputKeys.length < 2 || (kind !== 'APPEND' && inputKeys.length !== 2)) {
    throw new Error(`${kind} requires ${kind === 'APPEND' ? 'at least two' : 'exactly two'} inputs.`);
  }
  await ensureCombine(kind);
  for (let index = 0; index < inputKeys.length; index += 1) {
    if (index > 1) await click('Add another table');
    await selectValue(`Input table ${index + 1}`, inputKeys[index]);
  }

  if (kind === 'KEY_JOIN' || kind === 'MEMBERSHIP') {
    const keyPairs = listValues(input, 'matching_key_pair');
    if (keyPairs.length === 0) throw new Error(`${kind} requires at least one matching key pair.`);
    for (let index = 0; index < keyPairs.length; index += 1) {
      const pair = keyPairs[index];
      if (!isRecord(pair)) throw new Error('Each matching_key_pair item must identify left and right field selectors.');
      if (index > 0) await click('Add another matching pair');
      await selectValue(`Matching pair ${index + 1} first field`, requiredPairId(pair, ['first', 'left', 'leftColumnId', 'firstFieldId']));
      await selectValue(`Matching pair ${index + 1} second field`, requiredPairId(pair, ['second', 'right', 'rightColumnId', 'secondFieldId']));
    }
    if (kind === 'KEY_JOIN') await selectValue('If a row in the first table has no match', requiredId(input, 'join_type'));
    else await selectValue('Which rows should stay?', requiredId(input, 'membership_mode'));
  }

  const outputs = kind === 'APPEND' ? listValues(input, 'output_field') : outputFieldSpecs(input);
  if (outputs.length === 0) throw new Error(`${kind} requires at least one output field.`);
  for (let index = 0; index < outputs.length; index += 1) {
    const output = outputs[index];
    if (!isRecord(output)) throw new Error('Each output field must include its name, label, and source mappings.');
    await clickButtonByName('Add output field');
    const name = nonEmptyString(output.name);
    const label = nonEmptyString(output.label);
    if (!name || !label) throw new Error('Each output field needs non-empty name and label text.');
    await textValue(`Output field ${index + 1} name`, name);
    await textValue(`Output field ${index + 1} label`, label);
    const appendMappings = listValues(input, 'output_field_mapping');
    const mappings = kind === 'APPEND'
      ? appendMappings[index]
      : output.sources;
    if (!Array.isArray(mappings) || mappings.length !== inputKeys.length) {
      throw new Error(`Output field ${index + 1} must map once for each selected input table.`);
    }
    for (let inputIndex = 0; inputIndex < mappings.length; inputIndex += 1) {
      const mapping = mappings[inputIndex];
      const rawMapping = mapping === 'EMPTY' || mapping === null ? 'EMPTY_FOR_INPUT' : requiredMappingId(mapping);
      const value = kind === 'APPEND'
        ? rawMapping === 'EMPTY_FOR_INPUT' ? 'empty-for-this-table' : `column:${rawMapping}`
        : rawMapping;
      const selectorLabel = kind === 'APPEND'
        ? `Output field ${index + 1} matching field in input ${inputIndex + 1}`
        : `Output field ${index + 1} source field in input ${inputIndex + 1}`;
      await selectValue(selectorLabel, value);
    }
  }
};

const requiredPairId = (pair: Readonly<Record<string, unknown>>, keys: ReadonlyArray<string>): string => {
  for (const key of keys) {
    const value = idFrom(pair[key]);
    if (value) return value;
  }
  throw new Error('matching_key_pair item must name the first and second input fields.');
};

const requiredMappingId = (mapping: unknown): string => {
  const value = idFrom(mapping);
  if (!value) throw new Error('Output mapping must identify an offered source field or EMPTY.');
  return value;
};

const driveAppend = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.append, 'driveAppend');
  await configureCombine(input, 'APPEND');
};

const driveKeyJoin = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.keyJoin, 'driveKeyJoin');
  await configureCombine(input, 'KEY_JOIN');
};

const driveMembership = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.membership, 'driveMembership');
  await configureCombine(input, 'MEMBERSHIP');
};

const driveLegacyGroupedPivot = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.legacyPivot, 'driveLegacyGroupedPivot');
  await click('ui04-open-table-shape-settings');
  await screen.findByTestId('ui04-reshape-mode');
  await selectValue('Reshape mode', requiredId(input, 'reshape_mode'));
  const groups = listValues(input, 'group_column');
  for (const group of groups) {
    if (!isRecord(group) || !nonEmptyString(group.label)) throw new Error('group_column items must include their visible choice label.');
    await setCheckbox(`Pivot group columns: ${group.label}`, true);
  }
  await selectValue('Pivot category column', requiredId(input, 'category_column'));
  await selectValue('Pivot value column', requiredId(input, 'value_column'));
  await click('ui04-pivot-discover-categories');
  await screen.findByTestId('ui04-pivot-category-discovery-state', {}, { timeout: 30_000 });
  const categories = listValues(input, 'included_category');
  if (categories.length === 0) throw new Error('Legacy grouped pivot requires one or more discovered categories.');
  for (const category of categories) {
    const label = isRecord(category) ? nonEmptyString(category.label) : nonEmptyString(category);
    if (!label) throw new Error('included_category items must include their visible label.');
    await setCheckbox(`Include ${label} category`, true);
  }
  const policies = dimension(input, 'pivot_policy_choices');
  if (!isRecord(policies)) throw new Error('pivot_policy_choices must include duplicate, missing-cell, and unlisted choices.');
  await selectValue('Duplicate cell policy', requiredPairId(policies, ['duplicate', 'duplicatePolicy']));
  await selectValue('Missing cell policy', requiredPairId(policies, ['missing', 'missingCellPolicy']));
  await selectValue('Unlisted category policy', requiredPairId(policies, ['unlisted', 'unlistedCategoryPolicy']));
  await click('ui04-preview-table-shape');
  await screen.findByTestId('ui04-confirm-table-shape', {}, { timeout: 30_000 });
  await clickRequestApplyIfCommand(input, 'ui04-confirm-table-shape');
};

const driveLegacyUnpivot = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.legacyUnpivot, 'driveLegacyUnpivot');
  await click('ui04-open-table-shape-settings');
  await screen.findByTestId('ui04-reshape-mode');
  await selectValue('Reshape mode', requiredId(input, 'reshape_mode'));
  const inputIds = listIds(input, 'input_column');
  if (inputIds.length === 0) throw new Error('Legacy unpivot requires at least one input column.');
  const inputChoices = listValues(input, 'input_column');
  for (let index = 0; index < inputIds.length; index += 1) {
    const choice = inputChoices[index];
    if (!isRecord(choice) || !nonEmptyString(choice.label)) throw new Error('input_column items must include their visible column label.');
    await setCheckbox(`Unpivot input columns: ${choice.label}`, true);
  }
  const keyOutput = textPair(dimension(input, 'key_output'), 'key_output');
  const valueOutput = textPair(dimension(input, 'value_output'), 'value_output');
  await textValue('Unpivot key output column', keyOutput.name);
  await textValue('Unpivot key output label', keyOutput.label);
  await textValue('Unpivot value output column', valueOutput.name);
  await textValue('Unpivot value output label', valueOutput.label);
  await selectValue('Unpivot null-row policy', requiredId(input, 'null_row_policy'));
  await click('ui04-preview-table-shape');
  await screen.findByTestId('ui04-confirm-table-shape', {}, { timeout: 30_000 });
  await clickRequestApplyIfCommand(input, 'ui04-confirm-table-shape');
};

const driveCodedPivot = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.codedPivot, 'driveCodedPivot');
  await openReshapeChooser('reshape');
  await click('construction-reshape-choice-coded-pivot');
  await screen.findByRole('region', { name: 'Coded values as columns' });
  const sourceChoice = dimension(input, 'direct_coded_source');
  const sourceTitle = isRecord(sourceChoice) ? nonEmptyString(sourceChoice.title) ?? nonEmptyString(sourceChoice.label) : undefined;
  if (!sourceTitle) throw new Error('direct_coded_source must include the visible source title.');
  const sourceSearch = await screen.findByLabelText('Find a coded source');
  if (!(sourceSearch instanceof HTMLInputElement)) throw new Error('Coded source search is not a text input.');
  fireEvent.change(sourceSearch, { target: { value: sourceTitle } });
  const source = await screen.findByRole('radio', { name: new RegExp(sourceTitle) });
  fireEvent.click(source);
  const categories = listValues(input, 'coded_category');
  if (categories.length === 0 || categories.length > 50) throw new Error('Coded pivot requires between 1 and 50 selected supported categories.');
  for (const category of categories) {
    if (!isRecord(category)) throw new Error('Each coded_category must include its visible label and category identity.');
    const label = nonEmptyString(category.label);
    if (!label) throw new Error('Coded categories require a visible semantic label.');
    const checkbox = await screen.findByRole('checkbox', { name: new RegExp(label) });
    if (!(checkbox instanceof HTMLInputElement)) throw new Error('Coded category control is not a checkbox.');
    if (!checkbox.checked) fireEvent.click(checkbox);
  }
  const advanced = await screen.findByText('Advanced value handling');
  const details = advanced.closest('details');
  if (!(details instanceof HTMLDetailsElement)) throw new Error('Coded pivot advanced value controls are unavailable.');
  details.open = true;
  const duplicate = requiredId(input, 'duplicate_policy');
  const missing = requiredId(input, 'missing_cell_policy');
  await selectValue('When several values match', duplicate);
  await selectValue('When a record has no value', missing);
  await readyConstructionProposal();
  await clickRequestApplyIfCommand(input, 'construction-proposal-apply');
};

const driveRelatedExpand = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.relatedExpand, 'driveRelatedExpand');
  await openRowsSettings();
  await click('construction-action-related-rows');
  await screen.findByTestId('construction-related-expand-editor');
  const anchor = requiredId(input, 'starting_anchor');
  const anchorControl = screen.queryByLabelText('Start from');
  if (anchorControl) await selectValue('Start from', anchor);
  await selectValue('Related record type', requiredId(input, 'target_resource_type'));
  const route = dimension(input, 'relationship_path');
  const routeLabel = isRecord(route) ? nonEmptyString(route.label) : undefined;
  if (!routeLabel) throw new Error('relationship_path must include the visible route label.');
  const relationship = await screen.findByRole('radio', { name: new RegExp(routeLabel) });
  fireEvent.click(relationship);
  await selectValue('If a current row has no matches', requiredId(input, 'no_match_policy'));
  const mode = requiredId(input, 'contributor_mode');
  await openDetails('construction-related-expand-contributor-options');
  if (mode === 'ALL') {
    const all = await screen.findByRole('radio', { name: 'All matching records' });
    fireEvent.click(all);
  } else if (mode === 'CONDITION') {
    const condition = await screen.findByRole('radio', { name: 'Only records meeting a condition' });
    fireEvent.click(condition);
    const contributor = dimension(input, 'contributor_field');
    const contributorLabel = isRecord(contributor) ? nonEmptyString(contributor.label) : undefined;
    if (!contributorLabel) throw new Error('contributor_field must include the visible field label.');
    const search = await screen.findByLabelText(/Find a field on/);
    if (!(search instanceof HTMLInputElement)) throw new Error('Contributor field search is not a text input.');
    fireEvent.change(search, { target: { value: contributorLabel } });
    const fieldChoice = await screen.findByRole('button', { name: new RegExp(contributorLabel) });
    fireEvent.click(fieldChoice);
    await selectValue('Condition', requiredId(input, 'contributor_operator'));
  } else {
    throw new Error(`Unsupported contributor_mode ${mode}.`);
  }
  const output = dimension(input, 'related_record_id_output');
  if (isRecord(output)) {
    const pair = textPair(output, 'related_record_id_output');
    await openDetails('construction-related-expand-advanced');
    await textValue('Related record ID column', pair.name);
    await textValue('Column label', pair.label);
  }
  await readyConstructionProposal();
  await clickRequestApplyIfCommand(input, 'construction-proposal-apply');
};

const driveRelatedSource = async (input: AdapterInput): Promise<void> => {
  requireAction(input, IDS.relatedSource, 'driveRelatedSource');
  const scopeValue = dimension(input, 'add_columns_source_scope');
  const scope = isRecord(scopeValue) ? nonEmptyString(scopeValue.kind) ?? nonEmptyString(scopeValue.scope) : nonEmptyString(scopeValue);
  if (scope !== 'RELATED') {
    throw new Error('Related source column driver handles RELATED scope only; EXACT_RELATED routes to the distinct RELATED_FIELD editor.');
  }
  if (!screen.queryByRole('tab', { name: 'Other fields' })) await click('construction-action-add-columns');
  const otherFieldsTab = await screen.findByRole('tab', { name: 'Other fields' });
  fireEvent.click(otherFieldsTab);
  const fieldsets = await screen.findByTestId('construction-add-columns-source');
  const allOptions = within(fieldsets).getAllByTestId('construction-add-columns-source-option');
  const resourceType = isRecord(scopeValue) ? nonEmptyString(scopeValue.resourceType) : undefined;
  const sourceNodeId = isRecord(scopeValue) ? nonEmptyString(scopeValue.sourceNodeId) : undefined;
  const target = allOptions.find((option) =>
    option.getAttribute('data-source-kind') === scope &&
    (!resourceType || option.textContent?.includes(resourceType)) &&
    (!sourceNodeId || option.getAttribute('data-source-key')?.includes(sourceNodeId)),
  );
  if (!target) throw new Error(`The current Builder stage does not offer ${scope} source scope.`);
  fireEvent.click(target);
  const form = requiredId(input, 'related_source_form');
  const relatedChoices = listValues(input, 'selected_related_fields');
  if (relatedChoices.length === 0) throw new Error('At least one related source field must be selected.');
  const fieldChoices: { readonly resourceType: string; readonly path: string; readonly label: string }[] = [];
  for (const field of relatedChoices) {
    if (!isRecord(field)) throw new Error('selected_related_fields items must include resourceType, path, and label.');
    const fieldResource = nonEmptyString(field.resourceType);
    const path = nonEmptyString(field.path);
    const label = nonEmptyString(field.label);
    if (!fieldResource || !path || !label) throw new Error('selected_related_fields items must include resourceType, path, and label.');
    fieldChoices.push({ resourceType: fieldResource, path, label });
    await textValue('Search dataset fields by name or type', label);
    const search = screen.getByRole('button', { name: 'Search' });
    fireEvent.click(search);
    const candidate = await screen.findByRole('button', { name: `Select ${fieldResource}.${path}` });
    fireEvent.click(candidate);
  }
  await clickButtonByName('Advanced options');
  const options = await screen.findByTestId('catalog-selection-options');
  const formLabel = form === 'ALL' ? 'All values' : form === 'COUNT' ? 'Matching record count' : form === 'PRESENCE' ? 'Has matching record' : undefined;
  if (!formLabel) throw new Error(`Unsupported related_source_form ${form}.`);
  for (const field of fieldChoices) {
    const selectionLabel = `Select ${field.resourceType}.${field.path}`;
    const formChoice = within(options).getByRole('radio', { name: `${selectionLabel}: ${formLabel}` });
    fireEvent.click(formChoice);
  }
  const predicate = dimension(input, 'contributor_predicate');
  if (predicate !== null && predicate !== undefined && predicate !== 'NONE') {
    const operator = requiredId(input, 'contributor_predicate');
    if (fieldChoices.length !== 1) throw new Error('A contributor_predicate can only be driven when exactly one related field is selected.');
    const field = fieldChoices[0];
    if (!field) throw new Error('Related source field selection is empty.');
    const matchingDetails = within(options).getByText(new RegExp(`Matching records:.*${field.label}`)).closest('details');
    if (!(matchingDetails instanceof HTMLDetailsElement)) throw new Error('The mounted related field has no matching-record condition controls.');
    matchingDetails.open = true;
    const predicateLabel = operator === 'EXISTS'
      ? `Only records with ${field.label}`
      : `Only records where ${field.label} equals`;
    fireEvent.click(within(matchingDetails).getByRole('radio', { name: predicateLabel }));
  }
  const rowPolicy = dimension(input, 'row_value_policy');
  if (hasRowValuePolicy(rowPolicy)) {
    const policy = screen.queryByLabelText('Values per grouped row');
    if (policy) await selectValue('Values per grouped row', requiredId(input, 'row_value_policy'));
  }
  await clickButtonByName(`Add ${fieldChoices.length} selected ${fieldChoices.length === 1 ? 'feature' : 'features'}`);
  await screen.findByTestId('construction-proposal-ready', {}, { timeout: 30_000 });
  await clickRequestApplyIfCommand(input, 'construction-proposal-apply');
};

const shapeCombineAdapters = {
  [IDS.expand]: {
    adapterId: `shape-combine.${IDS.expand}`,
    runnerActionId: 'driveExpandScalarList',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/PreviewTable.tsx:732',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3488',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:3962',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:4044',
    ],
    drive: driveExpandScalarList,
  },
  [IDS.codedGroup]: {
    adapterId: `shape-combine.${IDS.codedGroup}`,
    runnerActionId: 'driveCodedGroup',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:980',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:2325',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:2703',
    ],
    drive: driveCodedGroup,
  },
  [IDS.codedPivot]: {
    adapterId: `shape-combine.${IDS.codedPivot}`,
    runnerActionId: 'driveCodedPivot',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/CodedPivotEditor.tsx:108',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/CodedPivotEditor.tsx:130',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/CodedPivotEditor.tsx:356',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/CodedPivotEditor.tsx:405',
    ],
    drive: driveCodedPivot,
  },
  [IDS.pivot]: {
    adapterId: `shape-combine.${IDS.pivot}`,
    runnerActionId: 'drivePivotValues',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:980',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:4326',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:4400',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:4463',
    ],
    drive: drivePivotValues,
  },
  [IDS.relatedExpand]: {
    adapterId: `shape-combine.${IDS.relatedExpand}`,
    runnerActionId: 'driveRelatedExpand',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:1000',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/RelatedExpandEditor.tsx:386',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/RelatedExpandEditor.tsx:416',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/RelatedExpandContributorEditor.tsx:167',
    ],
    drive: driveRelatedExpand,
  },
  [IDS.unpivot]: {
    adapterId: `shape-combine.${IDS.unpivot}`,
    runnerActionId: 'driveUnpivot',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:1003',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:4590',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx:4641',
    ],
    drive: driveUnpivot,
  },
  [IDS.append]: {
    adapterId: `shape-combine.${IDS.append}`,
    runnerActionId: 'driveAppend',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.tsx:163',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:246',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:634',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:749',
    ],
    drive: driveAppend,
  },
  [IDS.keyJoin]: {
    adapterId: `shape-combine.${IDS.keyJoin}`,
    runnerActionId: 'driveKeyJoin',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3172',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:267',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:677',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:717',
    ],
    drive: driveKeyJoin,
  },
  [IDS.membership]: {
    adapterId: `shape-combine.${IDS.membership}`,
    runnerActionId: 'driveMembership',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3172',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:267',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:726',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionCombineEditor.tsx:749',
    ],
    drive: driveMembership,
  },
  [IDS.legacyPivot]: {
    adapterId: `shape-combine.${IDS.legacyPivot}`,
    runnerActionId: 'driveLegacyGroupedPivot',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4865',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.tsx:344',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeEditor.tsx:165',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/GroupedPivotEditor.tsx:79',
    ],
    drive: driveLegacyGroupedPivot,
  },
  [IDS.legacyUnpivot]: {
    adapterId: `shape-combine.${IDS.legacyUnpivot}`,
    runnerActionId: 'driveLegacyUnpivot',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:4865',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.tsx:344',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeEditor.tsx:165',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/UnpivotEditor.tsx:15',
    ],
    drive: driveLegacyUnpivot,
  },
  [IDS.expandedRows]: {
    adapterId: `shape-combine.${IDS.expandedRows}`,
    runnerActionId: 'driveSourceExpansion',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:294',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/SourceCollectionExpandEditor.tsx:269',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/SourceCollectionExpandEditor.tsx:319',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/SourceCollectionExpandEditor.tsx:373',
    ],
    drive: driveSourceExpansion,
  },
  [IDS.explicitGroups]: {
    adapterId: `shape-combine.${IDS.explicitGroups}`,
    runnerActionId: 'driveExplicitGroups',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:1078',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ExplicitGroupAuthoring.tsx:167',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ExplicitGroupAuthoring.tsx:221',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/ExplicitGroupAuthoring.tsx:192',
    ],
    drive: driveExplicitGroups,
  },
  [IDS.rowShape]: {
    adapterId: `shape-combine.${IDS.rowShape}`,
    runnerActionId: 'driveRowsPerStartingRecord',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:294',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:311',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/components/RowDefinitionSettingsPanel.tsx:332',
    ],
    drive: driveRowsPerStartingRecord,
  },
  [IDS.relatedSource]: {
    adapterId: `shape-combine.${IDS.relatedSource}`,
    runnerActionId: 'driveRelatedSource',
    sourceRefs: [
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1483',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1509',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1572',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1637',
      'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1842',
    ],
    drive: driveRelatedSource,
  },
} satisfies Readonly<Record<AffordanceId, ShapeCombineAdapter>>;

export { shapeCombineAdapters };
