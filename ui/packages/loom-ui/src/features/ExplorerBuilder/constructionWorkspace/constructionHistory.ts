import type {
  Construction,
  ConstructionStageColumn,
  ConstructionTableScalar,
} from '../../../types';
import type { ConstructionHistoryStep } from './ConstructionWorkspace';

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
        title: 'Keep rows',
        summary: `Keep rows where ${columnLabel(filter.columnId)} ${operators[filter.operator]}${suffix}.`,
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
