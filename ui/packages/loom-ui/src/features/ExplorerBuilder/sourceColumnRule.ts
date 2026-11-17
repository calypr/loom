import type { ImportedSourceParameters, SourceRuleCreate } from '../../types';

type DynamicParameters = NonNullable<ImportedSourceParameters['dynamic']>;

export const isIdentifierColumnRule = (rule: DynamicParameters): boolean => {
  const key = rule.key;
  return (rule.source.select === 'root.identifier[]' || rule.source.select === 'identifier[]')
    && rule.value?.select === 'item.value'
    && (key?.select === 'item.system'
      || (key?.call === 'last_segment' && key.args?.length === 1 && key.args[0]?.select === 'item.system'));
};

export const isCategoryCodingRule = (rule: DynamicParameters): boolean =>
  (rule.source.select === 'root.category[].coding[]' || rule.source.select === 'category[].coding[]')
  && rule.value?.select === 'item.display'
  && rule.key?.call === 'last_segment'
  && rule.key.args?.length === 1
  && rule.key.args[0]?.select === 'item.system';

export const columnRulePresets = [
  { id: 'identifier', kind: 'DYNAMIC_COLUMN', label: 'Identifiers', description: 'Generate one column for each identifier system.' },
  { id: 'extension', kind: 'EXTENSION_COLUMN', label: 'Extensions', description: 'Generate one column for each extension URL at the selected path.' },
  { id: 'categoryCoding', kind: 'DYNAMIC_COLUMN', label: 'Category codings', description: 'Generate one column for each category coding system.' },
  { id: 'catalog', kind: 'CATALOG_PROJECTION', label: 'Catalog fields', description: 'Generate columns from the populated fields covered by the rule.' },
] as const satisfies ReadonlyArray<{ id: string; kind: SourceRuleCreate['kind']; label: string; description: string }>;

export type ColumnRulePreset = (typeof columnRulePresets)[number];

export interface ColumnRuleOccurrence {
  readonly id: string;
  readonly resourceType: string;
  readonly label: string;
}

export const columnRuleDefaults: Partial<Record<ColumnRulePreset['id'], ImportedSourceParameters>> = {
  identifier: {
    dynamic: {
      name: 'identifiers',
      source: { select: 'identifier[]' },
      key: { select: 'item.system' },
      value: { select: 'item.value' },
    },
  },
  categoryCoding: {
    dynamic: {
      name: 'category_codings',
      source: { select: 'category[].coding[]' },
      key: { call: 'last_segment', args: [{ select: 'item.system' }] },
      value: { select: 'item.display' },
    },
  },
  catalog: { catalog: { name: 'catalog_fields', includePaths: [], naming: 'PATH' } },
};
