import type { Dispatch, SetStateAction } from 'react';
import type { GetConstructionCapabilitiesArgs } from '../../../api';
import type { ConstructionCapabilitiesResponse, ConstructionChoiceSelection, ConstructionStep } from '../../../types';
import type { BuilderAuthoringState, DraftTable } from './model';
import type { useConstructionLifecycle } from '../constructionWorkspace/useConstructionLifecycle';
import { sanitizeReasonText, unavailableOperationReason } from '../catalogItems';
import { sourceProjectionAvailability } from '../constructionWorkspace/sourceProjectionAvailability';
import { constructionAppendStageFor } from '../constructionWorkspace/constructionStages';

type UseReshapeAvailabilityOptions = {
  readonly constructionLifecycle: Pick<ReturnType<typeof useConstructionLifecycle>, 'capabilities'>;
  readonly sourceStageDescriptors: ConstructionCapabilitiesResponse['stages'] | undefined;
  readonly rowValuePolicy: NonNullable<ConstructionChoiceSelection['rowValuePolicy']>;
  readonly setRowValuePolicy: Dispatch<SetStateAction<NonNullable<ConstructionChoiceSelection['rowValuePolicy']>>>;
  readonly hasUnsupportedSavedSourceColumns: boolean;
  readonly table: DraftTable | undefined;
  readonly previewIsCurrent: boolean;
  readonly capabilitiesRequest: GetConstructionCapabilitiesArgs | undefined;
  readonly editingConstructionStep: ConstructionStep | undefined;
  readonly pendingCommands: number;
  readonly state: BuilderAuthoringState;
};

export const useReshapeAvailability = ({
  constructionLifecycle, sourceStageDescriptors, rowValuePolicy, setRowValuePolicy,
    hasUnsupportedSavedSourceColumns, table, previewIsCurrent, capabilitiesRequest,
    editingConstructionStep, pendingCommands, state
}: UseReshapeAvailabilityOptions) => {
  const sourceProjectionSummary = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.sourceProjectionAvailability
    : undefined;
  const capabilityNodeOccurrences = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.nodeOccurrences
    : undefined;
  const existingSourceKeys = constructionLifecycle.capabilities.status === 'ready'
    ? new Set(
        constructionLifecycle.capabilities.response.selectedStage.columns
          .map((column) => column.sourceKey)
          .filter((key): key is string => Boolean(key)),
      )
    : undefined;
  const selectedCapabilityStageId = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.selectedStage.id
    : undefined;
  const rowValuesSupported = sourceStageDescriptors?.at(-1)?.capabilities.some(
    (capability) => capability.kind === 'ROW_VALUES' && capability.supported,
  ) ?? false;
  const groupedRowValuePolicy = rowValuesSupported
    ? { value: rowValuePolicy, onChange: setRowValuePolicy }
    : undefined;
  const sourceAvailability = hasUnsupportedSavedSourceColumns
    ? { available: false, reason: 'Remove unsupported saved source fields before adding more fields.' }
    : constructionLifecycle.capabilities.status === 'error'
    ? { available: false, reason: sanitizeReasonText(constructionLifecycle.capabilities.message) }
    : sourceProjectionAvailability(sourceStageDescriptors, sourceProjectionSummary, selectedCapabilityStageId);
  const capabilityStage = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.selectedStage
    : undefined;
  const constructionCapabilitiesResponse = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response
    : undefined;
  const relatedSourceCapability = capabilityStage?.capabilities.find(
    (candidate) => candidate.kind === 'RELATED_SOURCE',
  );
  const relatedSourceReason = [
    relatedSourceCapability?.reasonCode,
    relatedSourceCapability?.reason,
  ]
    .map((part) => (part === undefined ? '' : sanitizeReasonText(part).trim()))
    .filter((part) => part !== '')
    .join(': ');
  const capabilityIsForAppendStage = constructionLifecycle.capabilities.status === 'ready' &&
    capabilityStage?.id === constructionAppendStageFor(
      constructionLifecycle.capabilities.response.baseConstruction,
      table?.document.rows,
    );
  const deriveCapability = capabilityStage?.capabilities.find(
    (candidate) => candidate.kind === 'DERIVE',
  );
  const calculateUnavailableReason = !table?.document.rootResourceType
    ? 'Choose a row source before calculating.'
    : constructionLifecycle.capabilities.status === 'loading'
      ? 'Checking calculation support for this table stage…'
      : constructionLifecycle.capabilities.status === 'error'
        ? sanitizeReasonText(constructionLifecycle.capabilities.message)
        : constructionLifecycle.capabilities.status === 'idle'
          ? 'Calculation support is not available until the table capabilities load.'
          : !capabilityIsForAppendStage
            ? 'The table stage changed; reload calculation support.'
            : deriveCapability?.reason
              ? sanitizeReasonText(deriveCapability.reason)
              : 'The current table stage does not support calculated columns.';
  const capabilitiesErrorMessage = constructionLifecycle.capabilities.status === 'error'
    ? constructionLifecycle.capabilities.message
    : '';
  const reshapeAvailabilityFor = (kind: 'GROUP' | 'PIVOT' | 'CODED_PIVOT') => {
    const capability = capabilityStage?.capabilities.find((candidate) => candidate.kind === kind);
    return capabilityIsForAppendStage && capability?.supported
      ? { supported: true }
      : {
          supported: false,
          reason: unavailableOperationReason({
            stage: constructionLifecycle.capabilities.status === 'error'
              ? 'error'
              : constructionLifecycle.capabilities.status !== 'ready' || !capabilityIsForAppendStage
                ? 'checking'
                : 'ready',
            errorMessage: capabilitiesErrorMessage,
            checkingMessage: 'Checking this table’s available row operations…',
            label: 'This row operation is unavailable for the current table.',
            capability,
          }),
        };
  };
  const codedGroupingAvailable = capabilityIsForAppendStage &&
    capabilityStage?.capabilities.some((candidate) => candidate.kind === 'CODED_GROUP' && candidate.supported) &&
    Boolean(capabilityStage.codedGroupChoices?.length);
  const sourceGroupingAvailable = capabilityIsForAppendStage &&
    constructionLifecycle.capabilities.status === 'ready' &&
    constructionLifecycle.capabilities.response.sourceInput?.supported &&
    constructionLifecycle.capabilities.response.sourceInput.choices.some((choice) => choice.isPopulated);
  const stageGroupingAvailability = reshapeAvailabilityFor('GROUP');
  const codedPivotAvailability = reshapeAvailabilityFor('CODED_PIVOT');
  const ordinaryGroupAvailable = stageGroupingAvailability.supported || codedPivotAvailability.supported;
  const tablePivotAvailability = constructionLifecycle.capabilities.status === 'ready' && constructionLifecycle.capabilities.response.pivotSourceInput?.supported
    ? { supported: true } : reshapeAvailabilityFor('PIVOT');
  const expandCapability = capabilityStage?.capabilities.find((candidate) => candidate.kind === 'EXPAND');
  const signedExpandAvailable = constructionLifecycle.capabilities.status === 'ready'
    && previewIsCurrent && capabilityIsForAppendStage
    && (constructionLifecycle.capabilities.response.expandSourceInput?.supported ?? false)
    && (constructionLifecycle.capabilities.response.expandSourceInput?.choices ?? []).some((choice) => choice.isPopulated);
  const groupAlternatives = [
    ...(sourceGroupingAvailable ? [{ kind: 'source-group' as const, label: 'Source fields' }] : []),
    ...(codedGroupingAvailable ? [{ kind: 'coded-group' as const, label: 'Recorded code' }] : []),
  ];
  const reshapeRowsAvailability = {
    group: ordinaryGroupAvailable ? { supported: true } : stageGroupingAvailability,
    groupEntry: 'group' as const,
    groupAlternatives,
    expandSigned: signedExpandAvailable ? { supported: true as const } : undefined,
    pivot: codedPivotAvailability.supported ? codedPivotAvailability : tablePivotAvailability,
    pivotPending: Boolean(capabilitiesRequest) && constructionLifecycle.capabilities.status === 'loading',
    pivotEntry: codedPivotAvailability.supported ? 'coded-pivot' as const : 'pivot' as const,
    pivotAlternative: codedPivotAvailability.supported && tablePivotAvailability.supported ? 'pivot' as const : undefined,
  };
  const relatedExpandCapability = capabilityStage?.capabilities.find(
    (candidate) => candidate.kind === 'RELATED_EXPAND',
  );
  const relatedRowsAvailability = capabilityIsForAppendStage &&
    relatedExpandCapability?.supported &&
    Boolean(capabilityStage?.relatedExpandAnchors?.length)
    ? { supported: true }
    : {
        supported: false,
        reason: unavailableOperationReason({
          stage: constructionLifecycle.capabilities.status === 'error'
            ? 'error'
            : constructionLifecycle.capabilities.status !== 'ready'
              ? 'checking'
              : 'ready',
          errorMessage: capabilitiesErrorMessage,
          checkingMessage: 'Checking available related row paths…',
          label: 'This table has no executable related-record row path at the current stage.',
          capability: relatedExpandCapability,
        }),
      };
  const relatedSourceUnavailableReason = hasUnsupportedSavedSourceColumns
    ? 'Remove unsupported saved source fields before adding related fields.'
    : editingConstructionStep
      ? 'Close the saved-step editor before adding related fields; proposals follow the current final step.'
    : constructionLifecycle.capabilities.status === 'ready' && !capabilityIsForAppendStage
      ? 'Related fields can only be added after the current final step.'
      : undefined;
  const relatedSourceAvailability = !relatedSourceUnavailableReason &&
    capabilityIsForAppendStage &&
    relatedSourceCapability?.supported &&
    capabilityStage?.rowIdentityColumn
    ? { supported: true }
    : {
        supported: false,
        reason: relatedSourceUnavailableReason || relatedSourceReason || (
          constructionLifecycle.capabilities.status === 'error'
            ? sanitizeReasonText(constructionLifecycle.capabilities.message)
            : constructionLifecycle.capabilities.status === 'ready'
              ? 'Loom has not proved that this stage retains a source row anchor.'
              : 'Loom is checking whether this stage supports related-source fields.'
        ),
      };
  const sourceSelectionDisabledReason = pendingCommands > 0
    ? 'Loom is finishing the previous table update. Field selection will return when the draft refresh completes.'
    : state.reconciliation === 'pending'
      ? 'Loom is refreshing the current table draft. Field selection will return when the refresh completes.'
      : undefined;
  return { capabilityNodeOccurrences, existingSourceKeys, rowValuesSupported, groupedRowValuePolicy, sourceAvailability, capabilityStage, constructionCapabilitiesResponse, capabilityIsForAppendStage, calculateUnavailableReason, reshapeRowsAvailability, relatedRowsAvailability, relatedSourceAvailability, sourceSelectionDisabledReason };
};
