export type UnknownRecordDisplay = 'json' | 'parts';

export interface ValueDisplayOptions {
  readonly unknownRecord?: UnknownRecordDisplay;
  readonly maxDepth?: number;
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const scalarText = (value: unknown): string | undefined => {
  if (typeof value === 'string') return value.trim() || undefined;
  if (
    typeof value === 'number' ||
    typeof value === 'boolean' ||
    typeof value === 'bigint'
  ) {
    return String(value);
  }
  return undefined;
};

const losslessText = (value: unknown): string => {
  if (value === undefined || value === null) return '—';
  const scalar = scalarText(value);
  if (scalar !== undefined) return scalar;
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
};

const displayRecord = (
  value: Record<string, unknown>,
  options: Required<ValueDisplayOptions>,
  depth: number,
): string => {
  for (const key of ['text', 'display', 'value', 'code', 'reference']) {
    const preferred = value[key];
    if (preferred === undefined) continue;
    const scalar = scalarText(preferred);
    if (scalar !== undefined) return scalar;
    if (key === 'reference' || key === 'value') {
      return displayValue(preferred, { ...options, maxDepth: options.maxDepth - 1 }, depth + 1);
    }
  }
  const coding = value.coding;
  if (coding !== undefined) {
    return displayValue(coding, { ...options, maxDepth: options.maxDepth - 1 }, depth + 1);
  }
  if (options.unknownRecord === 'parts' && depth < options.maxDepth) {
    const parts = Object.entries(value)
      .map(([key, nested]) => {
        const formatted = displayValue(nested, options, depth + 1);
        return formatted === '—' ? undefined : `${key}: ${formatted}`;
      })
      .filter((part): part is string => part !== undefined);
    if (parts.length > 0) return parts.join(' · ');
  }
  return losslessText(value);
};

const displayValue = (
  value: unknown,
  options: ValueDisplayOptions = {},
  depth = 0,
): string => {
  const resolved: Required<ValueDisplayOptions> = {
    unknownRecord: options.unknownRecord ?? 'json',
    maxDepth: options.maxDepth ?? 3,
  };
  if (value === undefined || value === null) return '—';
  const scalar = scalarText(value);
  if (scalar !== undefined) return scalar;
  if (Array.isArray(value)) {
    const items = value
      .map((item) => displayValue(item, resolved, depth + 1))
      .filter((item) => item !== '—');
    return items.length > 0 ? items.join('; ') : '—';
  }
  if (isRecord(value)) return displayRecord(value, resolved, depth);
  return losslessText(value);
};

export { displayValue, losslessText };
