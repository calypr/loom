import type {
  Construction,
  ConstructionStageColumn,
  ConstructionTableScalar,
  ContributorPredicate,
} from '../../../types';
import type { ConstructionHistoryStep } from './ConstructionWorkspace';
import { relationshipLabel } from './routeDisplay';

const scalarLabel = (value: ConstructionTableScalar): string => {
  switch (value.kind) {
    case 'STRING': return `“${value.string}”`;
    case 'INTEGER': return String(value.integer);
    case 'DECIMAL': return String(value.decimal);
    case 'BOOLEAN': return String(value.boolean);
    case 'NULL': return 'null';
    case 'MISSING': return 'missing';
    default: {
      const exhaustive: never = value;
      return exhaustive;
    }
  }
};

const operationLabel = (operation: string): string => {
  switch (operation) {
    case 'ADD': return '+';
    case 'SUBTRACT': return '−';
    case 'MULTIPLY': return '×';
    case 'DIVIDE': return '÷';
    default: return operation;
  }
};

const contributorCondition = (predicate: ContributorPredicate | undefined, path: string): string => {
  if (!predicate) return '';
  if (predicate.operator === 'EXISTS') return `; only records with ${path}`;
  const value = predicate.value?.kind === 'STRING'
    ? `“${predicate.value.string}”`
    : predicate.value?.kind === 'CODE'
      ? `code “${predicate.value.code.code}”`
      : 'the selected value';
  return `; only records where ${path} equals ${value}`;
};

const formatStep = (
  step: Construction['steps'][number],
  input: ReadonlyMap<string, ConstructionStageColumn>,
): ConstructionHistoryStep => {
  const columnLabel = (id: string) => input.get(id)?.label ?? 'a column';
  switch (step.operation.kind) {
    case 'PIVOT': {
      const pivot = step.operation.pivot;
      const groupKeys = pivot.groupKeyIds.map(columnLabel).join(', ');
      const outputs = pivot.categories
        .map((category) => step.outputs.find((column) => column.id === category.outputColumnId)?.label)
        .filter((label): label is string => Boolean(label));
      return {
        id: step.id,
        title: 'Pivot',
        summary: `Group by ${groupKeys}; use ${columnLabel(pivot.categoryColumnId)} to create ${outputs.join(', ') || 'columns'} from ${columnLabel(pivot.valueColumnId)}.`,
        editable: true,
      };
    }
    case 'CODED_PIVOT': {
      const outputs = step.operation.codedPivot.categories
        .map((category) => step.outputs.find((column) => column.id === category.outputColumnId)?.label)
        .filter((label): label is string => Boolean(label));
      return {
        id: step.id,
        title: 'Coded values to columns',
        summary: `Keep one row per source record and make ${outputs.join(', ') || 'the selected coded values'} into columns.`,
        editable: true,
      };
    }
    case 'DERIVE': {
      const derive = step.operation.derive;
      const output = step.outputs.find((column) => column.id === derive.outputColumnId)?.label ?? 'a new column';
      const operandLabel = (operand: typeof derive.left): string =>
        operand.kind === 'COLUMN'
          ? columnLabel(operand.columnId)
          : String(operand.literal.kind === 'INTEGER' ? operand.literal.integer : operand.literal.decimal);
      return {
        id: step.id,
        title: 'Calculate',
        summary: `${output} = ${operandLabel(derive.left)} ${operationLabel(derive.operation)} ${operandLabel(derive.right)}.`,
        editable: true,
      };
    }
    case 'FILTER': {
      const filter = step.operation.filter;
      const operators = {
        EQUALS: 'equals',
        NOT_EQUALS: 'does not equal',
        IN: 'matches one of',
        EXISTS: 'has a value',
        MISSING: 'is missing',
        CONTAINS_TEXT: 'contains text',
        GT: 'is greater than',
        GTE: 'is at least',
        LT: 'is less than',
        LTE: 'is at most',
      } as const;
      const values = (filter.values ?? []).map((value) => {
        switch (value.kind) {
          case 'STRING': return `“${value.string}”`;
          case 'CODE': return value.code.display ?? value.code.code;
          case 'BOOLEAN': return String(value.boolean);
          case 'INTEGER': return String(value.integer);
          case 'DECIMAL': return String(value.decimal);
          case 'DATE': return value.date;
          case 'DATE_TIME': return value.dateTime;
          default: {
            const exhaustive: never = value;
            return exhaustive;
          }
        }
      });
      const suffix = values.length > 0 ? ` ${values.join(', ')}` : '';
      return {
        id: step.id,
        title: 'Filter rows',
        summary: `Filter output rows where ${columnLabel(filter.columnId)} ${operators[filter.operator]}${suffix}.`,
        editable: true,
      };
    }
    case 'UNPIVOT': {
      const unpivot = step.operation.unpivot;
      const inputs = unpivot.inputs.map(({ columnId, key }) => `${columnLabel(columnId)} as ${scalarLabel(key)}`);
      const keyOutput = step.outputs.find((column) => column.id === unpivot.keyOutputColumnId)?.label ?? 'key';
      const valueOutput = step.outputs.find((column) => column.id === unpivot.valueOutputColumnId)?.label ?? 'value';
      return {
        id: step.id,
        title: 'Unpivot',
        summary: `Turn ${inputs.join(', ')} into ${keyOutput} and ${valueOutput}.`,
        editable: true,
      };
    }
    case 'GROUP': {
      const group = step.operation.group;
      const keys = (group.keys ?? []).map((key) => columnLabel(key.inputColumnId));
      const aggregates = (group.aggregates ?? []).map((aggregate) => {
        const output = step.outputs.find((column) => column.id === aggregate.outputColumnId)?.label ?? 'a value';
        switch (aggregate.operation) {
          case 'COUNT_ROWS': return `${output} counts rows`;
          case 'COUNT_NON_NULL': return `${output} counts non-missing ${columnLabel(aggregate.inputColumnId)}`;
          case 'COUNT_DISTINCT': return `${output} counts distinct ${columnLabel(aggregate.inputColumnId)}`;
          case 'SUM': return `${output} sums ${columnLabel(aggregate.inputColumnId)}`;
          case 'MEAN': return `${output} averages ${columnLabel(aggregate.inputColumnId)}`;
          default: {
            const exhaustive: never = aggregate;
            return exhaustive;
          }
        }
      });
      const grouping = keys.length > 0 ? ` by ${keys.join(', ')}` : '';
      const measures = aggregates.length > 0 ? `; ${aggregates.join('; ')}` : '';
      return {
        id: step.id,
        title: 'Group',
        summary: `Group rows${grouping}${measures}.`,
        editable: true,
      };
    }
    case 'CODED_GROUP': {
      const group = step.operation.codedGroup;
      const missing = group.missingKeyPolicy === 'GROUP'
        ? 'Records without a complete code form a missing-code row.'
        : group.missingKeyPolicy === 'EXCLUDE'
          ? 'Records without a complete code are excluded.'
          : 'Missing codes stop this step.';
      return {
        id: step.id,
        title: 'Group by coded value',
        summary: `One row per distinct system, version, and code in ${group.source.codingPath}; count each source record once per code. ${missing}`,
        editable: true,
      };
    }
    case 'EXPAND': {
      const expand = step.operation.expand;
      const input = columnLabel(expand.inputColumnId);
      const output = step.outputs.find((column) => column.id === expand.outputColumnId)?.label ?? 'expanded values';
      const ordinal = expand.ordinalColumnId
        ? ` and ${step.outputs.find((column) => column.id === expand.ordinalColumnId)?.label ?? 'an ordinal column'}`
        : '';
      const policy = expand.emptyPolicy ? ` Empty lists: ${expand.emptyPolicy.toLowerCase().replaceAll('_', ' ')}.` : '';
      return {
        id: step.id,
        title: 'Expand',
        summary: `Expand ${input} into rows with ${output}${ordinal} columns.${policy}`,
        editable: true,
      };
    }
    case 'COMBINE': {
      const tableRefs = step.inputs.filter((input) => input.kind === 'TABLE_REVISION');
      const tables = tableRefs.map((input) => `${input.tableId} (revision ${input.revisionId})`);
      const combine = step.operation.combine;
      const description = combine.kind === 'KEY_JOIN'
        ? `Join ${combine.joinType?.toLowerCase() ?? 'matching'} rows`
        : combine.kind === 'APPEND'
          ? 'Append rows'
          : `${combine.membershipMode?.toLowerCase() ?? 'include'} rows by membership`;
      const source = tables.length > 0 ? ` from ${tables.join(', ')}` : ' from another table';
      return {
        id: step.id,
        title: 'Combine',
        summary: `${description}${source}.`,
      };
    }
    case 'RELATED_SOURCE': {
      const related = step.operation.relatedSource;
      const output = step.outputs.find((column) => column.id === related.outputColumnId)?.label ?? 'a related field';
      const source = `${related.source.resourceType}.${related.source.path}`;
      const route = related.route.length > 0
        ? ` via ${related.route.map(relationshipLabel).join(' → ')}`
        : '';
      const action = related.form === 'COUNT'
        ? `Count matching ${related.source.resourceType} records`
        : related.form === 'PRESENCE'
          ? `Show whether matching ${related.source.resourceType} records exist`
          : `Add all matching values from ${source}`;
      return {
        id: step.id,
        title: 'Related source',
        summary: `${action}${route}${contributorCondition(related.contributorRule.predicate, related.source.path)} as ${output}.`,
        editable: true,
      };
    }
    case 'RELATED_EXPAND': {
      const expansion = step.operation.relatedExpand;
      const path = expansion.route.map(relationshipLabel).join(' → ');
      const empty = expansion.emptyPolicy === 'PRESERVE_PARENT'
        ? 'keep parents without a match'
        : expansion.emptyPolicy === 'ERROR'
          ? 'refuse parents without a match'
          : 'omit parents without a match';
      return {
        id: step.id,
        title: 'Expand related records',
        summary: `One row per distinct ${expansion.targetResourceType} via ${path}${contributorCondition(expansion.contributorRule.predicate, expansion.contributorSource?.path ?? 'the selected field')}; ${empty}.`,
        editable: true,
      };
    }
    case 'RELATED_ELIGIBILITY': {
      const eligibility = step.operation.relatedEligibility;
      const rule = eligibility.match.kind === 'ABSENT'
        ? 'no matching records'
        : eligibility.match.kind === 'COUNT_AT_LEAST'
          ? `at least ${eligibility.match.threshold} matching records`
          : 'at least one matching record';
      return {
        id: step.id,
        title: 'Filter by related records',
        summary: `Keep rows with ${rule} from ${eligibility.targetResourceType} via ${eligibility.route.map(relationshipLabel).join(' → ')}${contributorCondition(eligibility.contributorRule.predicate, eligibility.contributorSource?.path ?? 'the selected field')}.`,
        editable: true,
      };
    }
    case 'RELATED_FIELD': {
      const field = step.operation.relatedField;
      const output = step.outputs.find((column) => column.id === field.outputColumnId)?.label ?? field.source.path;
      return {
        id: step.id,
        title: 'Field from this row’s related record',
        summary: `Add ${field.source.resourceType}.${field.source.path} as ${output}.`,
        editable: true,
      };
    }
    default: {
      const exhaustive: never = step.operation;
      return exhaustive;
    }
  }
};

export const constructionHistorySteps = (
  construction: Construction | undefined,
  sourceColumns: ReadonlyArray<ConstructionStageColumn>,
): ReadonlyArray<ConstructionHistoryStep> => {
  if (!construction) return [];
  const source = new Map(sourceColumns.map((column) => [column.id, column] as const));
  const stages = new Map<string, ReadonlyMap<string, ConstructionStageColumn>>();
  const result: ConstructionHistoryStep[] = [];

  for (const step of construction.steps) {
    const inputRef = step.inputs[0];
    const input = inputRef?.kind === 'STEP_OUTPUT'
      ? stages.get(inputRef.stepId) ?? source
      : source;
    result.push(formatStep(step, input));
    stages.set(step.id, new Map(step.outputs.map((column) => [column.id, column] as const)));
  }

  return result;
};

export const constructionRowMeaning = (
  sourceMeaning: string,
  construction: Construction | undefined,
  sourceColumns: ReadonlyArray<ConstructionStageColumn>,
): string => {
  if (!construction) return sourceMeaning;
  const source = new Map(sourceColumns.map((column) => [column.id, column] as const));
  const stages = new Map<string, ReadonlyMap<string, ConstructionStageColumn>>();
  let meaning = sourceMeaning;

  for (const step of construction.steps) {
    const inputRef = step.inputs[0];
    const input = inputRef?.kind === 'STEP_OUTPUT'
      ? stages.get(inputRef.stepId) ?? source
      : source;
    const column = (id: string) => input.get(id)?.label ?? step.outputs.find((item) => item.id === id)?.label ?? 'a selected column';
    switch (step.operation.kind) {
      case 'GROUP': {
        const keys = (step.operation.group.keys ?? []).map((key) => column(key.inputColumnId));
        meaning = keys.length > 0
          ? `One row per distinct combination of ${keys.join(', ')}.`
          : 'One row summarizing all input rows.';
        break;
      }
      case 'CODED_GROUP':
        meaning = 'One row per distinct code, including its system and version.';
        break;
      case 'PIVOT': {
        const keys = step.operation.pivot.groupKeyIds.map(column);
        meaning = keys.length > 0
          ? `One row per distinct combination of ${keys.join(', ')}.`
          : 'One row summarizing all input rows.';
        break;
      }
      case 'EXPAND':
        meaning = `One row per value in ${column(step.operation.expand.inputColumnId)} from each input row.`;
        break;
      case 'RELATED_EXPAND':
        meaning = `One row per matching ${step.operation.relatedExpand.targetResourceType} for each input row${
          step.operation.relatedExpand.emptyPolicy === 'PRESERVE_PARENT' ? '; rows with no match remain' : ''}.`;
        break;
      case 'UNPIVOT':
        meaning = `One row per selected column from each input row${
          step.operation.unpivot.nullRowPolicy === 'DROP' ? ' when it has a value' : ''}.`;
        break;
      case 'COMBINE':
        if (step.operation.combine.kind !== 'MEMBERSHIP') {
          meaning = 'Rows from combined tables; matching or appended records may change the row count.';
        }
        break;
      default:
        break;
    }
    stages.set(step.id, new Map(step.outputs.map((output) => [output.id, output] as const)));
  }
  return meaning;
};
