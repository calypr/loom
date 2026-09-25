import React, { useEffect, useState } from 'react';
import { useLoomClient } from '../../../react';
import type {
  ExplorerBuilderCatalog,
  TableShapeCapabilities,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import type { CatalogChoiceIntent } from '../catalogItems';
import {
  ConceptCatalog,
  type CatalogRouteContext,
  type CatalogSourceProjectionAvailability,
} from '../components/ConceptCatalog';
import { TableShapeSettingsPanel } from '../components/TableShapeSettingsPanel';
import {
  familyPresentation,
  type ConstructionOperationFamily,
  type ConstructionOperationIntent,
  type OperationAvailability,
  type OperationIntention,
} from './operationFamilies';

type ShapeCapabilitiesState =
  | { readonly kind: 'not-needed' }
  | { readonly kind: 'loading' }
  | { readonly kind: 'ready'; readonly capabilities: TableShapeCapabilities }
  | { readonly kind: 'error'; readonly message: string };

export interface ConstructionOperationsPanelProps {
  readonly family: ConstructionOperationFamily;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly table: DraftTable;
  readonly catalog: ExplorerBuilderCatalog;
  readonly rowRoot: string;
  readonly routeContext?: CatalogRouteContext;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly sourceProjectionAvailability?: CatalogSourceProjectionAvailability;
  readonly disabled: boolean;
  readonly onAddSelected: (selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<void>;
  readonly onApplyProposal: (proposalId: string) => Promise<boolean>;
}

const supported = (): OperationAvailability => ({ kind: 'supported' });
const unavailable = (reason: string): OperationAvailability => ({ kind: 'unavailable', reason });
const unresolvedAvailability = (state: ShapeCapabilitiesState): OperationAvailability =>
  state.kind === 'loading'
    ? { kind: 'loading', message: 'Checking the choices supported for this table…' }
    : state.kind === 'error'
      ? { kind: 'unknown', message: state.message }
      : { kind: 'unknown', message: 'Operation choices have not loaded yet.' };

const unsupportedReason = (
  capabilities: TableShapeCapabilities,
  mode: TableShapeCapabilities['reshapeModes'][number]['mode'],
  fallback: string,
): string => {
  const choice = capabilities.reshapeModes.find((candidate) => candidate.mode === mode);
  if (choice?.availability.kind === 'unsupported') return choice.availability.reason;
  return fallback;
}

const intentionsFor = (args: {
  readonly family: ConstructionOperationFamily;
  readonly rootAvailable: boolean;
  readonly shapeState: ShapeCapabilitiesState;
  readonly constructionPresent: boolean;
}): ReadonlyArray<OperationIntention> => {
  const { family, rootAvailable, shapeState, constructionPresent } = args;
  const capability = shapeState.kind === 'ready' ? shapeState.capabilities : undefined;
  const calcAvailability = constructionPresent
    ? unavailable('This table’s saved steps cannot be changed from this editor yet.')
    : capability
      ? capability.derivedAvailability.kind === 'supported' &&
        capability.binaryOperators.some((choice) => choice.availability.kind === 'supported')
        ? supported()
        : unavailable(capability.derivedAvailability.kind === 'unsupported'
          ? capability.derivedAvailability.reason
          : 'Loom did not return a supported binary calculation for this table.')
      : unresolvedAvailability(shapeState);
  const pivotAvailability = constructionPresent
    ? unavailable('This table’s saved steps cannot be changed from this editor yet.')
    : capability
      ? capability.reshapeModes.find((choice) => choice.mode === 'GROUPED_PIVOT')?.availability.kind === 'supported'
        ? supported()
        : unavailable(unsupportedReason(capability, 'GROUPED_PIVOT', 'Pivoting is not available for the current columns.'))
      : unresolvedAvailability(shapeState);
  const unpivotAvailability = constructionPresent
    ? unavailable('This table’s saved steps cannot be changed from this editor yet.')
    : capability
      ? capability.reshapeModes.find((choice) => choice.mode === 'UNPIVOT')?.availability.kind === 'supported'
        ? supported()
        : unavailable(unsupportedReason(capability, 'UNPIVOT', 'Unpivoting is not available for the current columns.'))
      : unresolvedAvailability(shapeState);

  switch (family) {
    case 'ADD_COLUMNS':
      return [
        {
          value: { family, intent: 'FIND_INFORMATION' },
          label: 'Find information',
          description: 'Search fields, observed codes, and concepts available for these records.',
          availability: rootAvailable
            ? supported()
            : unavailable('Choose a starting table before searching its fields and concepts.'),
        },
        {
          value: { family, intent: 'SUMMARIZE_RELATED' },
          label: 'Summarize related records',
          description: 'Add a count, total, or other summary of matching records to each row.',
          availability: unavailable('You can bring related values into each row. Counts and totals for related records are not available here yet.'),
        },
        {
          value: { family, intent: 'REUSE_CALCULATION' },
          label: 'Reuse a saved calculation',
          description: 'Choose a saved definition and connect its inputs to this table.',
          availability: unavailable('Saved calculations cannot be connected here yet. Create a new calculation from this table instead.'),
        },
      ];
    case 'KEEP_ROWS':
      return [
        {
          value: { family, intent: 'MATCH_CONDITIONS' },
          label: 'Match conditions',
          description: 'Keep rows that satisfy rules about their values.',
          availability: unavailable('Rows in this table cannot be filtered by their values here yet.'),
        },
        {
          value: { family, intent: 'MATCH_RELATED' },
          label: 'Match related records',
          description: 'Keep rows based on the presence or contents of related records.',
          availability: unavailable('This table cannot yet keep rows based on matching related records.'),
        },
        {
          value: { family, intent: 'REMOVE_DUPLICATES' },
          label: 'Remove duplicates',
          description: 'Keep one row for each chosen combination of values.',
          availability: unavailable('This table cannot yet keep one row per chosen combination of values.'),
        },
        {
          value: { family, intent: 'KEEP_RANKED' },
          label: 'Keep ranked rows',
          description: 'Keep a chosen number of rows, overall or within each group.',
          availability: unavailable('This table cannot yet rank rows or choose what to do with ties.'),
        },
      ];
    case 'CALCULATE':
      return [
        {
          value: { family, intent: 'CALCULATE_VALUE' },
          label: 'Calculate a value',
          description: 'Combine supported numeric columns or literals into a new value.',
          availability: calcAvailability,
        },
        {
          value: { family, intent: 'SET_BY_CONDITION' },
          label: 'Set values by condition',
          description: 'Assign values when rules match, with an explicit fallback.',
          availability: unavailable('Conditional assignments are not available here yet. You can create a numeric calculation when Loom offers compatible inputs.'),
        },
        {
          value: { family, intent: 'RECODE' },
          label: 'Recode values',
          description: 'Map existing values or categories to new ones.',
          availability: unavailable('Category value mappings are not available here yet.'),
        },
        {
          value: { family, intent: 'HANDLE_MISSING' },
          label: 'Handle missing values',
          description: 'Fill missing values using a chosen value or supported calculation.',
          availability: unavailable('A missing-value rule can be chosen inside a supported calculation. Filling a column on its own is not available here.'),
        },
        {
          value: { family, intent: 'CALCULATE_ACROSS_ROWS' },
          label: 'Calculate across rows',
          description: 'Use ordered or grouped rows to calculate ranks, changes, or running values.',
          availability: unavailable('Running values, row-to-row changes, and ranks are not available here yet.'),
        },
      ];
    case 'RESHAPE':
      return [
        {
          value: { family, intent: 'SUMMARIZE_GROUPS' },
          label: 'Summarize into groups',
          description: 'Make one row for each group and calculate summaries.',
          availability: unavailable('General group summaries are not available here yet. Pivoting and unpivoting may be available for this table.'),
        },
        {
          value: { family, intent: 'PIVOT' },
          label: 'Turn values into columns',
          description: 'Make selected category values into named columns.',
          availability: pivotAvailability,
        },
        {
          value: { family, intent: 'UNPIVOT' },
          label: 'Turn columns into rows',
          description: 'Stack selected columns into a name column and a value column.',
          availability: unpivotAvailability,
        },
        {
          value: { family, intent: 'EXPAND_REPEATED' },
          label: 'Expand repeated values',
          description: 'Make a separate row for each item in a repeated value.',
          availability: unavailable('Repeated values cannot yet be expanded into separate rows here.'),
        },
      ];
    case 'COMBINE':
      return [
        {
          value: { family, intent: 'MATCH_COLUMNS' },
          label: 'Add matching columns',
          description: 'Match rows in another table and bring over selected columns.',
          availability: unavailable('Another saved table cannot be selected as an input here yet.'),
        },
        {
          value: { family, intent: 'APPEND_ROWS' },
          label: 'Append rows',
          description: 'Stack tables with an explicit alignment of their columns.',
          availability: unavailable('Rows from another saved table cannot be appended here yet.'),
        },
        {
          value: { family, intent: 'COMPARE_MEMBERSHIP' },
          label: 'Compare membership',
          description: 'Keep rows that also appear, or do not appear, in another table.',
          availability: unavailable('This table cannot yet be compared with another saved table by matching columns.'),
        },
        {
          value: { family, intent: 'MAKE_COMBINATIONS' },
          label: 'Make combinations',
          description: 'Create rows from supported pairs of records in two tables.',
          availability: unavailable('Rows from two saved tables cannot yet be paired here.'),
        },
      ];
    default: {
      const exhaustive: never = family;
      return exhaustive;
    }
  }
};

const requestFailureMessage = (): string =>
  'Could not check operation choices. Reload the table and try again.';

const intentionKey = (intention: ConstructionOperationIntent): string =>
  `${intention.family}:${intention.intent}`;

const availabilityText = (availability: OperationAvailability): string | undefined => {
  switch (availability.kind) {
    case 'supported': return undefined;
    case 'loading':
    case 'unknown': return availability.message;
    case 'unavailable': return availability.reason;
    default: {
      const exhaustive: never = availability;
      return exhaustive;
    }
  }
};

export const ConstructionOperationsPanel = (props: ConstructionOperationsPanelProps) => {
  const client = useLoomClient();
  const [shapeState, setShapeState] = useState<ShapeCapabilitiesState>({ kind: 'not-needed' });
  const [selectedIntention, setSelectedIntention] = useState<ConstructionOperationIntent | null>(null);
  const constructionPresent = 'construction' in props.table.document && props.table.document.construction !== undefined;
  useEffect(() => {
    setSelectedIntention(null);
    if (props.family !== 'CALCULATE' && props.family !== 'RESHAPE') {
      setShapeState({ kind: 'not-needed' });
      return;
    }
    if (constructionPresent) {
      setShapeState({ kind: 'not-needed' });
      return;
    }
    if (!props.snapshotToken || !props.draftDigest || props.draftVersion < 1) {
      setShapeState({ kind: 'error', message: 'The saved table version is not available yet. Reload the Builder to check operation choices.' });
      return;
    }

    const controller = new AbortController();
    setShapeState({ kind: 'loading' });
    void client.getTableShapeCapabilities({
      project: props.project,
      explorerId: props.explorerId,
      authResourcePath: props.authResourcePath,
      snapshotToken: props.snapshotToken,
      expectedDraftVersion: props.draftVersion,
      expectedDraftDigest: props.draftDigest,
      outputId: props.table.outputId,
    }, controller.signal).then((capabilities) => {
      if (controller.signal.aborted) return;
      if (capabilities.outputId !== props.table.outputId) {
        throw new Error('Loom returned operation choices for another table. Reload the Builder and try again.');
      }
      setShapeState({ kind: 'ready', capabilities });
    }).catch(() => {
      if (controller.signal.aborted) return;
      setShapeState({ kind: 'error', message: requestFailureMessage() });
    });
    return () => controller.abort();
  }, [client, constructionPresent, props.authResourcePath, props.draftDigest, props.draftVersion, props.explorerId, props.family, props.project, props.snapshotToken, props.table.outputId]);

  const intentions = intentionsFor({
    family: props.family,
    rootAvailable: Boolean(props.rowRoot.trim()),
    shapeState,
    constructionPresent,
  });
  const availableIntentions = intentions.filter((intention) => intention.availability.kind === 'supported');
  const checkingIntentions = intentions.filter((intention) =>
    intention.availability.kind === 'loading' || intention.availability.kind === 'unknown',
  );
  const unavailableIntentions = intentions.filter((intention) => intention.availability.kind === 'unavailable');
  const selectedKey = selectedIntention ? intentionKey(selectedIntention) : undefined;
  const presentation = familyPresentation(props.family);
  const selectedSupported = selectedIntention
    ? intentions.find((intention) => intentionKey(intention.value) === intentionKey(selectedIntention))?.availability.kind === 'supported'
    : false;
  const selectedInputNames = (props.selectedColumns ?? []).filter((column) => column.trim() !== '');

  const renderIntention = (intention: OperationIntention) => {
    const key = intentionKey(intention.value);
    const disabled = props.disabled || intention.availability.kind !== 'supported';
    return (
      <li key={key} className="rounded-lg border border-slate-200 bg-white p-3">
        <button
          type="button"
          aria-pressed={selectedKey === key}
          disabled={disabled}
          data-testid={`construction-operation-intention-${intention.value.family.toLowerCase()}-${intention.value.intent.toLowerCase()}`}
          onClick={() => setSelectedIntention(intention.value)}
          className="text-left font-semibold text-slate-900 disabled:cursor-not-allowed disabled:text-slate-500"
        >
          {intention.label}
        </button>
        <p className="mt-1 text-sm text-slate-600">{intention.description}</p>
        {availabilityText(intention.availability) ? (
          <p className={`mt-2 text-sm ${intention.availability.kind === 'unavailable' ? 'text-amber-900' : 'text-slate-600'}`} role="status">
            {intention.availability.kind === 'unavailable' ? `Unavailable here: ${intention.availability.reason}` : availabilityText(intention.availability)}
          </p>
        ) : null}
      </li>
    );
  };

  return (
    <section aria-label={`${presentation.title} operations`} data-testid="construction-operations-panel" className="grid gap-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
      <header>
        <div>
          <h2 className="text-lg font-semibold text-slate-950">{presentation.title}</h2>
          <p className="mt-1 text-sm text-slate-600">{presentation.introduction}</p>
        </div>
      </header>

      {selectedInputNames.length > 0 ? (
        <section aria-label="Selected inputs" data-testid="construction-operation-selected-inputs" className="rounded-md bg-blue-50 px-3 py-2">
          <h3 className="text-sm font-medium text-blue-950">Selected columns</h3>
          <ul className="mt-1 flex flex-wrap gap-2 text-sm text-blue-900">
            {selectedInputNames.map((column) => <li key={column} className="rounded-full bg-white px-2 py-0.5">{column}</li>)}
          </ul>
        </section>
      ) : null}

      {availableIntentions.length > 0 ? (
        <section aria-label="Available intentions" data-testid="construction-operation-available">
          <h3 className="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-600">Available here</h3>
          <ul className="grid gap-2">{availableIntentions.map(renderIntention)}</ul>
        </section>
      ) : null}

      {checkingIntentions.length > 0 ? (
        <section aria-label="Checking operation availability" data-testid="construction-operation-checking">
          <h3 className="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-600">Checking availability</h3>
          <ul className="grid gap-2">{checkingIntentions.map(renderIntention)}</ul>
        </section>
      ) : null}

      {availableIntentions.length === 0 && checkingIntentions.length === 0 && unavailableIntentions.length > 0 ? (
        <p role="status" className="rounded-md bg-slate-50 px-3 py-2 text-sm text-slate-700">
          No supported actions are available here yet. Open “See more options” to review the alternatives.
        </p>
      ) : null}

      {unavailableIntentions.length > 0 ? (
        <details data-testid="construction-operation-unavailable" className="rounded-lg border border-slate-200 px-3 py-2">
          <summary className="cursor-pointer font-medium text-slate-800 focus:outline-none focus:ring-2 focus:ring-blue-500">See more options</summary>
          <ul className="grid gap-2">{unavailableIntentions.map(renderIntention)}</ul>
        </details>
      ) : null}

      {shapeState.kind === 'error' && (props.family === 'CALCULATE' || props.family === 'RESHAPE') ? (
        <p role="alert" data-testid="construction-operation-capability-error" className="rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-950">
          {shapeState.message}
        </p>
      ) : null}

      {selectedSupported && selectedIntention?.family === 'ADD_COLUMNS' && selectedIntention.intent === 'FIND_INFORMATION' ? (
        <div data-testid="construction-operation-editor" className="grid gap-3">
          <div>
            <h3 className="font-semibold text-slate-900">Find information</h3>
            <p className="mt-1 text-sm text-slate-600">Inspect a field or concept, then choose an available construction supplied by Loom.</p>
          </div>
          <ConceptCatalog
            key={`${props.project}:${props.explorerId}:${props.snapshotToken}:${props.table.outputId}:${props.routeContext?.occurrenceId ?? 'root'}`}
            project={props.project}
            explorerId={props.explorerId}
            authResourcePath={props.authResourcePath}
            snapshotToken={props.snapshotToken}
            outputId={props.table.outputId}
            rowRoot={props.rowRoot}
            routeContext={props.routeContext}
            layout="panel"
            catalog={props.catalog}
            sourceProjectionAvailability={props.sourceProjectionAvailability}
            disabled={props.disabled}
            onAddSelected={props.onAddSelected}
          />
        </div>
      ) : null}

      {selectedSupported &&
      ((selectedIntention?.family === 'CALCULATE' && selectedIntention.intent === 'CALCULATE_VALUE') ||
        (selectedIntention?.family === 'RESHAPE' && (selectedIntention.intent === 'PIVOT' || selectedIntention.intent === 'UNPIVOT'))) ? (
        <div data-testid="construction-operation-editor" className="grid gap-3">
          <div>
            <h3 className="font-semibold text-slate-900">
              {selectedIntention.family === 'CALCULATE' ? 'Calculate a value' : selectedIntention.intent === 'PIVOT' ? 'Turn values into columns' : 'Turn columns into rows'}
            </h3>
            <p className="mt-1 text-sm text-slate-600">
              Loom supplies the supported inputs and policies. A matching row preview is required before the change can be applied.
            </p>
          </div>
          {shapeState.kind === 'ready' && shapeState.capabilities.savedProposalIntent.kind !== 'NONE' ? (
            <p role="status" className="rounded-md bg-blue-50 px-3 py-2 text-sm text-blue-950">
              Saved calculations and reshape settings stay together in one review. Check existing settings before applying a change.
            </p>
          ) : null}
          <TableShapeSettingsPanel
            client={client}
            project={props.project}
            explorerId={props.explorerId}
            authResourcePath={props.authResourcePath}
            snapshotToken={props.snapshotToken}
            draftVersion={props.draftVersion}
            draftDigest={props.draftDigest}
            table={props.table}
            disabled={props.disabled}
            onApply={props.onApplyProposal}
          />
        </div>
      ) : null}
    </section>
  );
};
