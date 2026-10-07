export const appendColumnOptionMapping = (column) => {
  if (!column?.id) throw new Error('APPEND source column evidence needs a stable column ID.');
  return {
    kind: 'append-column',
    value: 'column:' + column.id,
    selectedLabel: String(column.label || column.name) + ' · ' + String(column.type || column.clickhouseType) +
      (column.nullable ? ' · nullable' : ''),
  };
};

export const resolveAppendCapabilityColumns = ({ event, binding, sourceColumnRefs }) => {
  const body = event?.body ?? {};
  const response = event?.response ?? {};
  const workspaceInputs = Array.isArray(response.workspaceInputs) ? response.workspaceInputs : [];
  const expectedSourceOutputIds = binding?.sourceOutputIds ?? [];
  const uniqueExpectedSourceOutputIds = new Set(expectedSourceOutputIds);
  const actualSourceOutputIds = workspaceInputs.map((input) => input?.outputId).sort();
  const expectedSortedOutputIds = [...expectedSourceOutputIds].sort();
  const requestURL = (() => {
    try { return new URL(event?.url); } catch { return undefined; }
  })();
  const refs = Array.isArray(sourceColumnRefs) ? sourceColumnRefs : [];
  const uniqueKeys = new Set(refs.map((reference) => reference?.key));
  const columns = refs.map((reference) => {
    const inputMatches = workspaceInputs.filter((input) => input?.outputId === reference?.outputId);
    const input = inputMatches.length === 1 ? inputMatches[0] : undefined;
    const columnMatches = (input?.columns ?? []).filter((column) => column?.id === reference?.columnId);
    const column = columnMatches.length === 1 ? columnMatches[0] : undefined;
    const hasCompiledOptionMetadata = Boolean(column &&
      typeof column.name === 'string' && column.name.length > 0 &&
      typeof (column.label || column.name) === 'string' && (column.label || column.name).length > 0 &&
      typeof column.logicalType === 'string' && column.logicalType.length > 0 &&
      typeof column.cardinality === 'string' && column.cardinality.length > 0 &&
      typeof column.nullable === 'boolean');
    const mapping = hasCompiledOptionMetadata ? appendColumnOptionMapping({
      id: column.id,
      name: column.name,
      label: column.label,
      type: column.logicalType,
      nullable: column.nullable,
    }) : undefined;
    return {
      key: reference?.key ?? null,
      outputId: reference?.outputId ?? null,
      columnId: reference?.columnId ?? null,
      inputMatches: inputMatches.length,
      columnMatches: columnMatches.length,
      compiledMetadataValid: hasCompiledOptionMetadata,
      compiledSchema: column ? {
        name: column.name ?? null,
        label: column.label ?? null,
        logicalType: column.logicalType ?? null,
        cardinality: column.cardinality ?? null,
        nullable: typeof column.nullable === 'boolean' ? column.nullable : null,
      } : null,
      mapping,
    };
  });
  const checks = {
    requestPathMatches: requestURL?.pathname === binding?.requestPath,
    requestOriginMatches: requestURL?.origin === binding?.requestOrigin,
    requestOutputMatches: body.outputId === binding?.outputId,
    requestSnapshotMatches: body.snapshotToken === binding?.snapshotToken,
    requestDraftVersionMatches: body.expectedDraftVersion === binding?.draftVersion,
    requestDraftDigestMatches: body.expectedDraftDigest === binding?.draftDigest,
    requestStageMatches: body.stageId === binding?.stageId,
    responseStatusIsSuccess: event?.status === 200,
    responseOutputMatches: response.outputId === binding?.outputId,
    responseSnapshotMatches: response.snapshotToken === binding?.snapshotToken,
    responseDraftVersionMatches: response.draftVersion === binding?.draftVersion,
    responseDraftDigestMatches: response.draftDigest === binding?.draftDigest,
    responseStageMatches: response.stageId === binding?.stageId && response.selectedStage?.id === binding?.stageId,
    builderGenerationMatches: binding?.builderGeneration === binding?.fixtureGeneration,
    exactlyTwoBoundSourceOutputs: expectedSourceOutputIds.length === 2 && uniqueExpectedSourceOutputIds.size === 2,
    exactSourceOutputs: workspaceInputs.length === expectedSourceOutputIds.length &&
      JSON.stringify(actualSourceOutputIds) === JSON.stringify(expectedSortedOutputIds) &&
      new Set(actualSourceOutputIds).size === workspaceInputs.length,
    exactSourceColumns: refs.length === 5 && uniqueKeys.size === 5 && columns.every((column) =>
      expectedSourceOutputIds.includes(column.outputId) && column.inputMatches === 1 &&
      column.columnMatches === 1 && column.compiledMetadataValid),
  };
  return {
    ok: Object.values(checks).every(Boolean),
    checks,
    sourceColumns: columns,
    mappings: Object.fromEntries(columns.filter((column) => column.mapping).map((column) => [column.key, column.mapping])),
    expectedSourceOutputIds,
    actualSourceOutputIds,
  };
};

export const appendControlSnapshot = () => {
  const normalize = (value) => String(value ?? '').replace(/\s+/g, ' ').trim();
  const state = (element, selector) => {
    if (!element) return { selector, exists: false, visible: false, enabled: false };
    const style = getComputedStyle(element);
    const visible = element.getClientRects().length > 0 && style.display !== 'none' && style.visibility !== 'hidden';
    const enabled = !element.matches(':disabled');
    if (element.tagName === 'SELECT') {
      const option = element.selectedOptions[0] ?? null;
      return {
        selector,
        exists: true,
        visible,
        enabled,
        value: element.value,
        selectedLabel: normalize(option?.textContent),
        selectedGroup: option?.parentElement?.label ?? null,
        selectedDisabled: option?.disabled ?? true,
        optionCount: element.options.length,
      };
    }
    return { selector, exists: true, visible, enabled, value: element.value };
  };
  const select = (selector) => state(document.querySelector(selector), selector);
  const input = (selector) => state(document.querySelector(selector), selector);
  return {
    appendChoice: (() => {
      const selector = 'button[data-testid="construction-combine-choice-append"]';
      const button = document.querySelector(selector);
      return button
        ? {
            ...state(button, selector),
            ariaPressed: button.getAttribute('aria-pressed'),
            ariaSelected: button.getAttribute('aria-selected'),
            dataSelected: button.getAttribute('data-selected'),
            className: String(button.className),
          }
        : state(null, selector);
    })(),
    inputs: [1, 2].map((index) => select('select[aria-label="Input table ' + index + '"]')),
    outputs: [1, 2, 3, 4].map((index) => ({
      name: input('input[aria-label="Output field ' + index + ' name"]'),
      label: input('input[aria-label="Output field ' + index + ' label"]'),
      mappings: [1, 2].map((inputIndex) =>
        select('select[aria-label="Output field ' + index + ' matching field in input ' + inputIndex + '"]')),
    })),
  };
};

export const matchAppendChoiceControls = ({ controls, expectedInputs, expectedOutputs, emptyMapping }) => {
  const controlsVisibleAndEnabled = [controls.appendChoice, ...controls.inputs,
    ...controls.outputs.flatMap((output) => [output.name, output.label, ...output.mappings])]
    .every((control) => control.exists && control.visible && control.enabled);
  const appendChoiceSelected = controls.appendChoice.ariaPressed === 'true';
  const exactChoiceControls = controls.outputs.every((output) => output.mappings.length === 2 &&
    output.mappings.every((mapping) => mapping.selector.includes('matching field in input')));
  const exactInputs = controls.inputs.every((input, index) => input.value === expectedInputs[index] &&
    input.selectedGroup === 'Current draft tables' && !input.selectedDisabled && input.optionCount > 1);
  const exactOutputs = controls.outputs.every((output, index) => {
    const expected = expectedOutputs[index];
    return output.name.value === expected.name && output.label.value === expected.label &&
      output.mappings.every((mapping, inputIndex) => {
        const expectedMapping = expected.mappings[inputIndex];
        if (expectedMapping === emptyMapping) {
          return mapping.value === 'empty-for-this-table' && mapping.selectedLabel === 'Empty for this table' &&
            !mapping.selectedDisabled && mapping.optionCount > 1;
        }
        if (expectedMapping?.kind === 'append-column') {
          return mapping.value === expectedMapping.value && mapping.selectedLabel === expectedMapping.selectedLabel &&
            !mapping.selectedDisabled && mapping.optionCount > 1;
        }
        return mapping.value === '';
      });
  });
  return {
    ok: controlsVisibleAndEnabled && appendChoiceSelected && exactChoiceControls && exactInputs && exactOutputs,
    controlsVisibleAndEnabled,
    appendChoiceSelected,
    exactChoiceControls,
    exactInputs,
    exactOutputs,
    expectedInputs,
    expectedOutputs,
    controls,
  };
};
