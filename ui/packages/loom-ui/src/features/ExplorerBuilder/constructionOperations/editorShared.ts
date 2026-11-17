import type { ConstructionCapabilitiesResponse, ConstructionStageDescriptor, ConstructionStep } from '../../../types';

export const createOpaqueId = (prefix: string): string => {
  const randomPart = globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2);
  return `${prefix}_${randomPart}`;
};

export const normalizedLogicalType = (type: string | undefined): string =>
  (type ?? '').trim().toLowerCase().replace(/[\s-]+/g, '_');

export const isNumericColumn = (column: ConstructionStageDescriptor['columns'][number]): boolean => {
  const type = normalizedLogicalType(column.type);
  return type === 'integer' || type === 'decimal' || type === 'number';
};

export const normalizedName = (value: string): string => {
  const normalized = value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'column';
  return /^[0-9]/.test(normalized) ? `column_${normalized}` : normalized;
};

export const uniqueName = (base: string, used: ReadonlySet<string>): string => {
  const normalized = normalizedName(base);
  if (!used.has(normalized)) return normalized;
  let suffix = 2;
  while (used.has(`${normalized}_${suffix}`)) suffix += 1;
  return `${normalized}_${suffix}`;
};

export const sourceFieldPathLabel = (path: string): string => {
  const words: string[] = [];
  for (const segment of path.replace(/^root\./, '').split('.')) {
    const readable = segment.replace(/\[\]/g, '').replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' ').trim();
    if (!readable) continue;
    words.push(readable.toLowerCase() === 'id' ? 'ID' : readable[0].toUpperCase() + readable.slice(1));
  }
  return words.join(' ');
};

export const sourceLabel = (choice: Pick<ConstructionStageDescriptor['columns'][number], 'label'> & { readonly fieldPath: string }): string => {
  const rawPath = choice.fieldPath.replace(/^root\./, '');
  const label = choice.label.trim().replace(/^root\./, '');
  const prefixedPath = label.endsWith(`.${rawPath}`) ? label.slice(0, -rawPath.length - 1) : undefined;
  const rawSegments = rawPath.split('.');
  const rawPathResource = rawSegments.length > 1 && /^[A-Z]/.test(rawSegments[0] ?? '') ? rawSegments[0] : undefined;
  const context = prefixedPath ?? (label === rawPath ? rawPathResource : undefined);
  const fieldPath = rawPathResource && context === rawPathResource ? rawSegments.slice(1).join('.') : rawPath;
  const labelIsPath = label === rawPath || Boolean(prefixedPath) || label === choice.fieldPath;
  const field = labelIsPath ? sourceFieldPathLabel(fieldPath) : label;
  return context ? `${context} · ${field}` : field;
};

export const editorContextKey = (
  capabilities: Pick<ConstructionCapabilitiesResponse, 'snapshotToken' | 'draftVersion' | 'draftDigest' | 'outputId' | 'stageId'>,
  editingStep: Pick<ConstructionStep, 'id'> | undefined,
  ...context: ReadonlyArray<string>
): string => [
  capabilities.snapshotToken,
  capabilities.draftVersion,
  capabilities.draftDigest,
  capabilities.outputId,
  capabilities.stageId,
  editingStep?.id ?? 'new',
  ...context,
].join(':');
