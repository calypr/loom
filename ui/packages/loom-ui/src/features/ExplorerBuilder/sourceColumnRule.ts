import type { ImportedSourceParameters } from '../../types';

type DynamicParameters = NonNullable<ImportedSourceParameters['dynamic']>;

export const isIdentifierColumnRule = (rule: DynamicParameters): boolean => {
  const key = rule.key;
  return rule.source.select === 'root.identifier[]'
    && rule.value?.select === 'item.value'
    && (key?.select === 'item.system'
      || (key?.call === 'last_segment' && key.args?.length === 1 && key.args[0]?.select === 'item.system'));
};
