import type { ExplorerBuilderCommand } from '../../../types';
import type { DraftTable } from './model';

export type AdmissionRefusal = {
  readonly kind: 'imported-recipe-immutable';
  readonly message: string;
  readonly outputId: string;
  readonly commandIndex?: number;
};

export type Admission =
  | { readonly kind: 'admitted' }
  | { readonly kind: 'refused'; readonly refusal: AdmissionRefusal };

export type AuthoringRequest =
  | { readonly kind: 'commands'; readonly commands: ReadonlyArray<ExplorerBuilderCommand> }
  | { readonly kind: 'add-columns-proposal'; readonly outputId: string };

const sourceWritingCommands = new Set<ExplorerBuilderCommand['type']>([
  'ADD_COLUMN_SOURCE',
  'UPDATE_COLUMN_SOURCE',
  'APPLY_CONSTRUCTION_CHOICE',
  'ADD_SEMANTIC_SELECTIONS',
]);

const importedRecipeMessage =
  'This table is built from an imported recipe. Edit the imported source steps instead of changing its source columns here.';

const importedSourceMessage =
  'Imported source columns are read-only in the Builder. This change would replace an imported source with an authored one.';

const refused = (
  outputId: string,
  message: string,
  commandIndex?: number,
): Admission => ({
  kind: 'refused',
  refusal: {
    kind: 'imported-recipe-immutable',
    message,
    outputId,
    ...(commandIndex === undefined ? {} : { commandIndex }),
  },
});

const tableForOutput = (
  tables: ReadonlyArray<DraftTable>,
  outputId: string | undefined,
): DraftTable | undefined => outputId
  ? tables.find((table) => table.outputId === outputId)
  : undefined;

// Add columns can bring related fields or calculations onto an imported-recipe
// table without rewriting its immutable source columns. Source writes are
// still refused per command below, where the concrete command is known.
const admitAddColumnsProposal = (
  _tables: ReadonlyArray<DraftTable>,
  _outputId: string,
): Admission => ({ kind: 'admitted' });

const admitCommand = (
  tables: ReadonlyArray<DraftTable>,
  command: ExplorerBuilderCommand,
  index: number,
): Admission => {
  if (!sourceWritingCommands.has(command.type)) return { kind: 'admitted' };
  const outputId = 'outputId' in command ? command.outputId : undefined;
  const table = tableForOutput(tables, outputId);
  if (!table || !outputId) return { kind: 'admitted' };
  if (table.document.importedRecipe) return refused(outputId, importedRecipeMessage, index);
  const source = 'source' in command ? command.source : undefined;
  if (source?.kind === 'imported') return refused(outputId, importedSourceMessage, index);
  return { kind: 'admitted' };
};

export const admitAuthoringRequest = (
  tables: ReadonlyArray<DraftTable>,
  request: AuthoringRequest,
): Admission => {
  if (request.kind === 'add-columns-proposal') {
    return admitAddColumnsProposal(tables, request.outputId);
  }
  for (const [index, command] of request.commands.entries()) {
    const admission = admitCommand(tables, command, index);
    if (admission.kind === 'refused') return admission;
  }
  return { kind: 'admitted' };
};
