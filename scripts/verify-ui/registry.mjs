export const registry = Object.freeze([
  Object.freeze({
    id: 'builder-controls',
    workflow: 'supported-builder-controls',
    hooks: ['useCreateExplorerAuthoringMutation', 'useApplyExplorerBuilderCommandsV2Mutation', 'useReconcileExplorerBuilderV2Mutation', 'usePreviewExplorerAuthoringV2Mutation', 'useGetExplorerAuthoringCapabilityV2Query', 'useDeleteExplorerAuthoringMutation', 'useKeyedQuery'],
    endpoints: ['POST /api/v1/projects/{project}/explorers', 'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/commands', 'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/reconcile'],
    requiredTransitions: ['duplicate, rename, delete table and reload', 'copy Explorer and retain fixture rows', 'Recompile calls the backend and restores preview', 'create a verified-ID Patient table and wait for its current-draft preview before Add columns is enabled'],
    gateReasons: ['Delete table needs a selected table', 'Recompile must obtain a new backend receipt after a failed automatic compile', 'the first-table readiness case observes only the verified-ID Patient flow'],
    script: 'builder-controls.mjs',
    cases: ['tables', 'recompile', 'first-table'],
    requiredChecks: {
      tables: ['duplicated and renamed tables survive reload', 'deleted table stays absent after reload', 'copied Explorer retains configured fields after reload', 'deleting the last table persists an empty workspace'],
      recompile: ['Recompile invokes the backend compiler completed', 'Recompile returned a successful compilation response', 'Preview renders both independent fixture Patients'],
      'first-table': [
        'Add columns stays disabled until a current-draft preview is accepted',
        'Add columns becomes enabled after the accepted current-draft preview',
        'first Patient table uses its verified ID field',
        'Preview renders both independent fixture Patients',
        'Add columns opens from the verified-ID first table',
        'Add columns closes from the verified-ID first table',
      ],
    },
    coverage: [
      { feature: 'table duplicate/rename/delete and Explorer copy', status: 'implemented' },
      { feature: 'Recompile after failed compilation', status: 'implemented', reason: 'The registered recovery case passes after the automatic-preview ownership fix; save/reload remains outside this case.' },
      { feature: 'Explorer deletion and capability controls', status: 'untested', reason: 'The Builder capability query feeds this feature gate, but registered cases exercise table controls and do not test the capability response or Explorer deletion path.' },
    ],
  }),
  Object.freeze({
    id: 'builder-load',
    workflow: 'builder-load-errors',
    hooks: [
      'useGetExplorerAuthoringExplorersQuery',
      'useGetExplorerBuilderStateV2Query',
    ],
    endpoints: [
      'GET /api/v1/projects/{project}/explorers',
      'GET /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/builder',
    ],
    requiredTransitions: [
      'builder shell loads the selected owned Explorer',
      'list and builder-state read faults show the V2 error alert and an actionable in-app retry',
      'reload separately restores list or builder-state reads when Retry is unavailable',
    ],
    gateReasons: [
      'builder load error currently has no retry control',
    ],
    script: 'builder-load.mjs',
    cases: ['list', 'state'],
    requiredChecks: {
      list: ['builder failure is exposed in an alert', 'builder failure exposes an actionable in-app retry', 'builder recovered through in-app Retry'],
      state: ['builder failure is exposed in an alert', 'builder failure exposes an actionable in-app retry', 'builder recovered through in-app Retry'],
    },
    coverage: [
      { feature: 'builder initial load', status: 'implemented' },
      { feature: 'builder list failure display', status: 'implemented' },
      { feature: 'builder state failure display', status: 'implemented' },
      { feature: 'builder reload recovery', status: 'implemented' },
      { feature: 'builder in-app retry', status: 'implemented' },
    ],
  }),
  Object.freeze({
    id: 'builder-authoring',
    workflow: 'builder-authoring-preview-persistence',
    hooks: [
      'useGetExplorerAuthoringExplorersQuery',
      'useGetExplorerBuilderStateV2Query',
      'useCreateExplorerAuthoringMutation',
      'useApplyExplorerBuilderCommandsV2Mutation',
      'useReconcileExplorerBuilderV2Mutation',
      'useGetExplorerCandidateSuggestionsV2Mutation',
      'usePreviewExplorerAuthoringV2Mutation',
      'usePublishExplorerAuthoringV2Mutation',
      'useAssessExplorerRowChangeMutation',
      'useResolveConfiguredColumnContextsQuery',
      'useResolvePopulationSelectionQuery',
      'usePopulationMappingMutation',
    ],
    endpoints: [
      'POST /api/v1/projects/{project}/explorers',
      'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/commands',
      'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/reconcile',
      'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/suggestions',
      'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/preview',
      'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/publish',
    ],
    requiredTransitions: [
      'create blank Explorer and first table',
      'select Patient root and wait for suggestions',
      'add patient id and gender filter through actionable controls',
      'automatic Preview returns real fixture rows after Apply columns within 5 seconds',
      'Publish and reload preserve the selected table and configured columns',
    ],
    gateReasons: [
      'all clicks require visible enabled controls and center-point hit testing',
      'Patient candidates are loaded from the Builder catalog and rendered as controls',
      'suggestion transport failure and retry remain untested because the dev catalog already supplies candidates',
      'Apply columns must render its automatic preview and retain both fixture Patient identities',
    ],
    script: 'builder-authoring.mjs',
    cases: ['suggestions', 'authoring'],
    requiredChecks: {
      suggestions: ['catalog-backed Patient candidates are rendered', 'Patient ID candidate control is visible and actionable'],
      authoring: ['Preview renders both independent fixture Patients', 'publish endpoint returned success', 'published Builder table and configured fields survive reload'],
    },
    coverage: [
      { feature: 'create blank Explorer and first table', status: 'implemented' },
      { feature: 'Patient graph root selection', status: 'implemented' },
      { feature: 'catalog-backed Patient candidate controls', status: 'implemented' },
      { feature: 'suggestion transport failure and retry', status: 'untested', reason: 'The isolated development Builder catalog already contains Patient candidates, so ensureSuggestions returns without issuing the lazy suggestions request.' },
      { feature: 'column projection and filter authoring', status: 'implemented' },
      { feature: 'automatic Preview and action-to-render timing', status: 'implemented' },
      { feature: 'Publish and reload persistence', status: 'implemented' },
      { feature: 'Explorer selection', status: 'untested' },
      { feature: 'table reorder', status: 'untested' },
      { feature: 'existing table row-root change assessment', status: 'untested', reason: 'The registered authoring case selects the root when creating its first table; it does not assess replacing the root on an existing table.' },
      { feature: 'configured-column interpretation context', status: 'untested', reason: 'Basic authoring adds columns and publishes, but it does not inspect or assert the saved-column context.' },
      { feature: 'graph route editing', status: 'untested' },
      { feature: 'starting-record selection', status: 'untested', reason: 'No registered authoring case attaches or resolves a saved starting selection.' },
      { feature: 'named cohort selection', status: 'untested', reason: 'No registered authoring case attaches or resolves a named cohort.' },
      { feature: 'selected-resource population coverage check', status: 'untested', reason: 'No registered case attaches a saved selection and published receipt, then activates the coverage check.' },
      { feature: 'grouping rows', status: 'untested' },
      { feature: 'related-record rows', status: 'untested' },
      { feature: 'repeated-value rows', status: 'untested' },
      { feature: 'ordinary Pivot', status: 'untested' },
      { feature: 'coded Pivot', status: 'untested' },
      { feature: 'Unpivot', status: 'untested' },
      { feature: 'Filter rows', status: 'untested' },
      { feature: 'direct columns', status: 'untested' },
      { feature: 'coded columns', status: 'untested' },
      { feature: 'related columns', status: 'untested' },
      { feature: 'ONE/ALL contributing values', status: 'untested' },
      { feature: 'contributor rules', status: 'untested' },
      { feature: 'missing-match policies', status: 'untested' },
      { feature: 'coverage inspection', status: 'untested' },
      { feature: 'column renaming', status: 'untested' },
      { feature: 'column chart configuration', status: 'untested' },
      { feature: 'column removal', status: 'untested' },
      { feature: 'preview limits', status: 'untested' },
      { feature: 'preview contract panels', status: 'untested' },
    ],
  }),
  Object.freeze({
    id: 'viewer-query',
    workflow: 'viewer-query-fault-retry',
    hooks: ['useLoomRuntime', 'useLoomOutput', 'useCellTraceMutation'],
    endpoints: [
      'GET /api/v1/projects/{project}/explorers/{explorer}',
      'POST /graphql/graph',
      'LoomClient.exportOutput',
    ],
    requiredTransitions: [
      'publish a fixture Explorer through Builder UI',
      'Viewer runtime loads and renders fixture rows',
      'one query read fault shows an actionable Retry control',
      'Retry loads real rows and filter facets',
    ],
    gateReasons: [
      'viewer Retry is accepted only after hit testing the real control',
      'fault injection targets one POST /graphql/graph read and is restored in finally',
      'custom URLs verify query recovery without assuming development fixture row or facet contents',
      'filter selection persistence across a full reload is not claimed',
    ],
    script: 'viewer-query.mjs',
    cases: ['output'],
    requiredChecks: {
      output: {
        owned: ['injected result-query error is visibly reported', 'result-query error exposes an actionable Retry control', 'retry result query completed', 'retried Viewer results contain both independent fixture Patients'],
        custom: ['injected result-query error is visibly reported', 'result-query error exposes an actionable Retry control', 'retry result query completed', 'Retry restored the custom output table'],
      },
    },
    coverage: [
      { feature: 'Viewer runtime load', status: 'implemented' },
      { feature: 'output query failure display and Retry', status: 'implemented' },
      { feature: 'facet checkbox and filtered query', status: 'untested', reason: 'The published development fixture declares no runtime filters; this case cannot open a facet.' },
      { feature: 'filter selection persistence across reload', status: 'untested' },
      { feature: 'shared filters', status: 'untested' },
      { feature: 'sorting', status: 'untested' },
      { feature: 'pagination', status: 'untested' },
      { feature: 'charts', status: 'untested' },
      { feature: 'row details', status: 'untested' },
      { feature: 'receipt-bound cell explanation/source trace', status: 'untested', reason: 'The Viewer query case verifies result-query recovery; it does not activate the feature-cell Explain action.' },
      { feature: 'custom actions', status: 'untested' },
      { feature: 'CSV export', status: 'untested' },
    ],
  }),
]);

export const getScenario = (id) => {
  const scenario = registry.find((entry) => entry.id === id);
  if (!scenario) throw new Error('unknown scenario: ' + id);
  return scenario;
};

export const requiredChecksFor = (scenario, caseName, custom = false) => {
  const checks = scenario.requiredChecks?.[caseName];
  const selected = Array.isArray(checks) ? checks : checks?.[custom ? 'custom' : 'owned'];
  if (!Array.isArray(selected) || !selected.length) throw new Error(`missing required checks for ${scenario.id}/${caseName}`);
  return selected;
};

export const coverageDrift = (entries = registry) => entries.flatMap((entry) => [
  ...entry.coverage.filter((coverage) => !['implemented', 'untested', 'failed'].includes(coverage.status))
    .map((coverage) => entry.id + ': invalid coverage status ' + coverage.status),
  ...entry.cases.flatMap((caseName) => {
    try { requiredChecksFor(entry, caseName); return []; }
    catch (error) { return [error.message]; }
  }),
]);
