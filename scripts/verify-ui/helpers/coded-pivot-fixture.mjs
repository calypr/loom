export const CODED_PIVOT_OBSERVATION_ID = '485e2567-b566-56f3-b5bd-5f025f37cd95';

export function codedPivotRestoredSourceRowVisible({ id }) {
  return document.querySelector('[data-testid="preview-table-scroll"]')?.innerText.includes(id);
}

export function codedPivotRemovalProposalReady({ id }) {
  const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
  return panel?.getAttribute('data-proposal-status') === 'ready' && panel.innerText.includes(id);
}

export function codedPivotFirstTableReady() {
  return document.body.innerText.includes('Build your first table') &&
    Boolean(document.querySelector('button[aria-label="Choose Observation rows"]:not(:disabled)'));
}

export function codedPivotSourceControls() {
  return [...document.querySelectorAll('input[name="coded-pivot-source"]')].map(input => ({
    text: input.closest('label')?.innerText,
    checked: input.checked,
    disabled: input.disabled,
  }));
}

export function codedPivotSourceRadioFor(page, label) {
  const normalizedLabel = String(label ?? '').replace(/\s+/g, ' ').trim();
  return page.locator('section[aria-label="Coded values as columns"]')
    .getByRole('radio', { name: normalizedLabel, exact: true });
}

export function codedPivotFailureDomSnapshot({ mode }) {
  const section = document.querySelector('section[aria-label="Coded values as columns"]');
  const allSourceControls = [...(section?.querySelectorAll('input[name="coded-pivot-source"]') ?? [])].map(input => ({
    type: input.type,
    name: input.name,
    checked: input.checked,
    disabled: input.disabled,
    labelText: input.closest('label')?.innerText ?? '',
  }));
  const maxSourceControls = 50;
  const modeLabel = allSourceControls.find(control => {
    const label = control.labelText.toLowerCase();
    return label.includes('component') && label.includes(String(mode).toLowerCase());
  });
  const sourceControlPreview = allSourceControls.slice(0, maxSourceControls);
  const sourceControls = modeLabel && !sourceControlPreview.includes(modeLabel)
    ? [...sourceControlPreview.slice(0, maxSourceControls - (sourceControlPreview.length === maxSourceControls ? 1 : 0)), modeLabel]
    : sourceControlPreview;
  const sectionHTML = section?.outerHTML ?? null;
  const maxSectionCharacters = 48_000;
  return {
    capturedAt: new Date().toISOString(),
    url: location.href,
    mode,
    bodyText: document.body.innerText.slice(0, 6_000),
    codedPivotSectionPresent: Boolean(section),
    codedPivotSectionHTML: sectionHTML?.slice(0, maxSectionCharacters) ?? null,
    codedPivotSectionHTMLTruncated: sectionHTML !== null && sectionHTML.length > maxSectionCharacters,
    sourceControlCount: allSourceControls.length,
    sourceControlsTruncated: allSourceControls.length > maxSourceControls,
    sourceControls,
  };
}

const modes = Object.freeze({
  integer: Object.freeze([
    Object.freeze({ system: 'https://cda.readthedocs.io', code: 'days_to_collection', label: 'Days to collection', type: 'integer', value: '162' }),
  ]),
  string: Object.freeze([
    Object.freeze({ system: 'https://cda.readthedocs.io', code: 'specimen_type', label: 'Specimen type', type: 'string', value: 'analyte' }),
    Object.freeze({ system: 'https://cda.readthedocs.io', code: 'primary_disease_type', label: 'Primary disease type', type: 'string', value: 'Ductal and lobular neoplasms' }),
  ]),
});

export const codedPivotFixtureFor = mode => {
  const fixture = modes[mode];
  if (!fixture) throw new TypeError(`Unsupported coded Pivot fixture mode: ${mode}`);
  return fixture.map(entry => ({ ...entry }));
};

export const codedPivotValuesFor = (source, { mode, project, generation }) => {
  if (!source || typeof source !== 'object') throw new TypeError('Coded Pivot oracle needs one raw Observation document.');
  if (source.id !== CODED_PIVOT_OBSERVATION_ID || source.resourceType !== 'Observation') {
    throw new Error('Coded Pivot oracle source must be the exact Observation fixture.');
  }
  if (source.project !== project || source.generation !== generation) {
    throw new Error('Coded Pivot oracle source project and generation must match the owned target.');
  }

  const expected = codedPivotFixtureFor(mode);
  return expected.map(({ code, type, value }) => {
    const matches = (source.component ?? []).filter(component =>
      (component.code?.coding ?? []).some(coding =>
        coding.system === 'https://cda.readthedocs.io' && coding.code === code));
    if (matches.length !== 1) throw new Error(`Expected exactly one raw component for Coding.code ${code}; found ${matches.length}.`);
    const component = matches[0];
    const actual = type === 'integer' ? component.valueInteger : component.valueString;
    if (type === 'integer' && (!Number.isSafeInteger(actual) || String(actual) !== value)) {
      throw new Error(`Raw integer Coding.code ${code} must equal ${value}.`);
    }
    if (type === 'string' && (typeof actual !== 'string' || actual !== value)) {
      throw new Error(`Raw string Coding.code ${code} must equal ${JSON.stringify(value)}.`);
    }
    return { code, type, value: String(actual) };
  });
};

export const codedPivotExpectedHeaderValuesFor = (codedStep, expected) => {
  const categories = codedStep?.operation?.codedPivot?.categories;
  const outputs = codedStep?.outputs;
  if (!Array.isArray(categories) || !Array.isArray(outputs) || categories.length !== expected.length) {
    throw new Error('Persisted CODED_PIVOT categories and declared outputs must match the scoped oracle.');
  }

  const expectedHeaders = expected.map(pair => {
    const categoryMatches = categories.filter(category => category.system === pair.system && category.code === pair.code);
    if (categoryMatches.length !== 1) throw new Error(`Expected one persisted category for ${pair.system}|${pair.code}.`);
    const category = categoryMatches[0];
    const outputMatches = outputs.filter(output => output.id === category.outputColumnId);
    if (outputMatches.length !== 1 || !outputMatches[0].label) {
      throw new Error(`Persisted category ${pair.system}|${pair.code} must resolve to one labeled output column.`);
    }
    return { system: pair.system, code: pair.code, outputColumnId: category.outputColumnId,
      label: outputMatches[0].label, value: pair.value };
  });

  if (new Set(expectedHeaders.map(({ label }) => label)).size !== expectedHeaders.length) {
    throw new Error('Each scoped coded category must resolve to a distinct rendered header.');
  }
  return expectedHeaders;
};

export const codedPivotRenderedValuesFor = ({ headers, rows }, codedStep, expected) => {
  if (!Array.isArray(headers) || !Array.isArray(rows) || rows.length !== 1) {
    throw new Error('Coded Pivot render oracle requires exactly one visible source row.');
  }
  const headerLabel = value => String(value ?? '').trim().split(/\r?\n/, 1)[0].replace(/\s+/g, ' ').trim().toLowerCase();
  const resolved = codedPivotExpectedHeaderValuesFor(codedStep, expected).map(binding => {
    const { label } = binding;
    const headerIndexes = headers.flatMap((header, index) => headerLabel(header) === headerLabel(label) ? [index] : []);
    if (headerIndexes.length !== 1) throw new Error(`Rendered header ${JSON.stringify(label)} must occur exactly once.`);
    const value = rows[0][headerIndexes[0]];
    if (String(value) !== binding.value) {
      throw new Error(`Rendered ${binding.system}|${binding.code} value must be ${JSON.stringify(binding.value)} under ${JSON.stringify(label)}; got ${JSON.stringify(value)}.`);
    }
    return { ...binding, value: String(value) };
  });
  return resolved;
};
