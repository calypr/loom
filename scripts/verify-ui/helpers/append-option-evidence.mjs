export const appendColumnOptionMapping = (column) => {
  if (!column?.id) throw new Error('APPEND source column evidence needs a stable column ID.');
  return {
    kind: 'append-column',
    value: 'column:' + column.id,
    selectedLabel: String(column.label || column.name) + ' · ' + String(column.type || column.clickhouseType) +
      (column.nullable ? ' · nullable' : ''),
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
