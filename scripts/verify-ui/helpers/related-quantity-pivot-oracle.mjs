const assertString = (value, label) => {
  if (typeof value !== 'string' || value.trim() === '') throw new Error(`${label} must be a non-empty string`);
};

const maxPivotCategoryDomain = 256;

export function buildRelatedQuantityOracleExecuteScript(query, bindVars = {}, resultMarker = '__LOOM_RELATED_QUANTITY_ORACLE__') {
  const script = `try {
  const rows = db._query(${JSON.stringify(query)}, ${JSON.stringify(bindVars)}, { maxRuntime: 30, memoryLimit: 268435456 }).toArray();
  print(${JSON.stringify(resultMarker)} + JSON.stringify({ ok: true, rows }));
} catch (error) {
  print(${JSON.stringify(resultMarker)} + JSON.stringify({ ok: false, message: error && (error.errorMessage || error.message) || String(error), errorNum: error && error.errorNum }));
}`;
  return script.replaceAll('@', '\\u0040');
}

export function buildRelatedQuantityPivotOracle(scope, { visibleTextKeys } = {}) {
  if (!scope || typeof scope !== 'object' || Array.isArray(scope)) throw new Error('Explicit related quantity Pivot scope is required');
  if (scope.scope_allowed !== true) throw new Error('Related quantity Pivot oracle requires allow-mode scope');
  assertString(scope.project, 'project');
  assertString(scope.dataset_generation, 'dataset_generation');
  if (!Array.isArray(scope.emptyPolicies) || scope.emptyPolicies.length !== 2
    || scope.emptyPolicies.some(policy => policy !== 'PRESERVE_PARENT')) {
    throw new Error('Streaming related quantity Pivot oracle requires PRESERVE_PARENT at both authored Related boundaries');
  }
  if (typeof scope.auth_resource_paths_unrestricted !== 'boolean') throw new Error('Explicit auth_resource_paths_unrestricted scope mode is required');
  if (!Array.isArray(scope.auth_resource_paths) || scope.auth_resource_paths.some(path => typeof path !== 'string')) throw new Error('auth_resource_paths must be an explicit string array');
  if (scope.auth_resource_paths_unrestricted && scope.auth_resource_paths.length !== 0) throw new Error('Unrestricted local scope must not carry auth resource paths');
  if (!Array.isArray(visibleTextKeys) || visibleTextKeys.length === 0 || visibleTextKeys.length > 25
    || visibleTextKeys.some(value => value !== null && typeof value !== 'string')) {
    throw new Error('Oracle query requires the exact non-empty visible UI text keys, capped at 25');
  }
  if (new Set(visibleTextKeys.map(displayTextKey)).size !== visibleTextKeys.length) throw new Error('Visible UI text keys must be unique');

  const bindVars = {
    project: scope.project,
    generation: scope.dataset_generation,
    auth_resource_paths_unrestricted: scope.auth_resource_paths_unrestricted,
    auth_resource_paths: scope.auth_resource_paths,
    scope_allowed: scope.scope_allowed,
    visible_text_keys: [...visibleTextKeys],
  };
  const query = `LET authResourcePaths = \u0040auth_resource_paths
LET scopedSpecimenCount = LENGTH(FOR s IN Specimen FILTER s.project == \u0040project AND s.dataset_generation == \u0040generation AND s.resourceType == "Specimen" AND s.payload.resourceType == "Specimen" LET allowed = \u0040auth_resource_paths_unrestricted == true OR s.auth_resource_path IN authResourcePaths FILTER allowed == \u0040scope_allowed RETURN 1)
LET scopedPatientCount = LENGTH(FOR p IN Patient FILTER p.project == \u0040project AND p.dataset_generation == \u0040generation AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient" LET allowed = \u0040auth_resource_paths_unrestricted == true OR p.auth_resource_path IN authResourcePaths FILTER allowed == \u0040scope_allowed RETURN 1)
LET scopedObservationCount = LENGTH(FOR o IN Observation FILTER o.project == \u0040project AND o.dataset_generation == \u0040generation AND o.resourceType == "Observation" AND o.payload.resourceType == "Observation" LET allowed = \u0040auth_resource_paths_unrestricted == true OR o.auth_resource_path IN authResourcePaths FILTER allowed == \u0040scope_allowed RETURN 1)
LET specimenPatientEdgeCount = LENGTH(FOR e IN fhir_edge FILTER e.project == \u0040project AND e.dataset_generation == \u0040generation AND e.label == "subject_Patient" AND e.from_type == "Specimen" AND e.to_type == "Patient" LET allowed = \u0040auth_resource_paths_unrestricted == true OR e.auth_resource_path IN authResourcePaths FILTER allowed == \u0040scope_allowed RETURN 1)
LET observationPatientEdgeCount = LENGTH(FOR e IN fhir_edge FILTER e.project == \u0040project AND e.dataset_generation == \u0040generation AND e.label == "subject_Patient" AND e.from_type == "Observation" AND e.to_type == "Patient" LET allowed = \u0040auth_resource_paths_unrestricted == true OR e.auth_resource_path IN authResourcePaths FILTER allowed == \u0040scope_allowed RETURN 1)
LET patientMultiplicities = (
 FOR eSP IN fhir_edge
   FILTER eSP.project == \u0040project AND eSP.dataset_generation == \u0040generation AND eSP.label == "subject_Patient" AND eSP.from_type == "Specimen" AND eSP.to_type == "Patient"
   LET eSPAllowed = \u0040auth_resource_paths_unrestricted == true OR eSP.auth_resource_path IN authResourcePaths
   FILTER eSPAllowed == \u0040scope_allowed
   LET s = DOCUMENT(eSP._from)
   FILTER s != null AND s.project == \u0040project AND s.dataset_generation == \u0040generation AND s.resourceType == "Specimen" AND s.payload.resourceType == "Specimen"
   LET sAllowed = \u0040auth_resource_paths_unrestricted == true OR s.auth_resource_path IN authResourcePaths
   FILTER sAllowed == \u0040scope_allowed
   LET p = DOCUMENT(eSP._to)
   FILTER p != null AND p.project == \u0040project AND p.dataset_generation == \u0040generation AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient"
   LET pAllowed = \u0040auth_resource_paths_unrestricted == true OR p.auth_resource_path IN authResourcePaths
   FILTER pAllowed == \u0040scope_allowed
   COLLECT specimenId = s._id, pairPatientId = p._id WITH COUNT INTO duplicateEdgeCount
   COLLECT patientId = pairPatientId WITH COUNT INTO specimenMultiplicity
   RETURN {patientId, specimenMultiplicity}
)
LET patientGroups = (
 FOR patientEntry IN patientMultiplicities
   LET observationMatches = (
     FOR eOP IN fhir_edge
       FILTER eOP.project == \u0040project AND eOP.dataset_generation == \u0040generation AND eOP.label == "subject_Patient" AND eOP.from_type == "Observation" AND eOP.to_type == "Patient" AND eOP._to == patientEntry.patientId
       LET eOPAllowed = \u0040auth_resource_paths_unrestricted == true OR eOP.auth_resource_path IN authResourcePaths
       FILTER eOPAllowed == \u0040scope_allowed
       LET o = DOCUMENT(eOP._from)
       FILTER o != null AND o.project == \u0040project AND o.dataset_generation == \u0040generation AND o.resourceType == "Observation" AND o.payload.resourceType == "Observation"
       LET oAllowed = \u0040auth_resource_paths_unrestricted == true OR o.auth_resource_path IN authResourcePaths
       FILTER oAllowed == \u0040scope_allowed
       COLLECT observationId = o._id INTO matchingEdges = {observation: o}
       LET chosen = FIRST(matchingEdges)
       RETURN {observationId, observation: chosen.observation, duplicateEdgeCount: LENGTH(matchingEdges)}
   )
   LET observations = APPEND(observationMatches, LENGTH(observationMatches) == 0 ? [null] : [])
   FOR observationRow IN observations
     LET o = observationRow == null ? null : observationRow.observation
     LET concept = o == null ? null : o.payload.valueCodeableConcept
     LET quantity = o == null ? null : o.payload.valueQuantity
     LET conceptPresent = o == null ? true : HAS(o.payload, "valueCodeableConcept")
     LET conceptType = o == null ? "NULL" : !conceptPresent ? "MISSING" : IS_NULL(concept) ? "NULL" : IS_OBJECT(concept) ? "OBJECT" : IS_ARRAY(concept) ? "ARRAY" : IS_STRING(concept) ? "STRING" : IS_NUMBER(concept) ? "NUMBER" : IS_BOOL(concept) ? "BOOL" : "OTHER"
     LET textPresent = o == null ? true : IS_OBJECT(concept) AND HAS(concept, "text")
     LET textValue = textPresent AND o != null ? concept.text : null
     LET textType = !textPresent ? "MISSING" : IS_NULL(textValue) ? "NULL" : IS_STRING(textValue) ? "STRING" : IS_OBJECT(textValue) ? "OBJECT" : IS_ARRAY(textValue) ? "ARRAY" : IS_NUMBER(textValue) ? "NUMBER" : IS_BOOL(textValue) ? "BOOL" : "OTHER"
     LET quantityPresent = o == null ? true : HAS(o.payload, "valueQuantity")
     LET quantityType = o == null ? "NULL" : !quantityPresent ? "MISSING" : IS_NULL(quantity) ? "NULL" : IS_OBJECT(quantity) ? "OBJECT" : IS_ARRAY(quantity) ? "ARRAY" : IS_STRING(quantity) ? "STRING" : IS_NUMBER(quantity) ? "NUMBER" : IS_BOOL(quantity) ? "BOOL" : "OTHER"
     LET codePresent = o == null ? true : IS_OBJECT(quantity) AND HAS(quantity, "code")
     LET codeValue = codePresent AND o != null ? quantity.code : null
     LET codeType = !codePresent ? "MISSING" : IS_NULL(codeValue) ? "NULL" : IS_STRING(codeValue) ? "STRING" : IS_OBJECT(codeValue) ? "OBJECT" : IS_ARRAY(codeValue) ? "ARRAY" : IS_NUMBER(codeValue) ? "NUMBER" : IS_BOOL(codeValue) ? "BOOL" : "OTHER"
     LET valuePresent = o == null ? true : IS_OBJECT(quantity) AND HAS(quantity, "value")
     LET value = o == null ? null : valuePresent ? quantity.value : null
     COLLECT patientIdKey = patientEntry.patientId, specimenMultiplicityKey = patientEntry.specimenMultiplicity, secondHopMissingKey = o == null, observationPresentKey = o != null, conceptPresentKey = conceptPresent, conceptTypeKey = conceptType, textPresentKey = textPresent, textTypeKey = textType, textValueKey = textValue, quantityPresentKey = quantityPresent, quantityTypeKey = quantityType, codePresentKey = codePresent, codeTypeKey = codeType, codeValueKey = codeValue
     AGGREGATE patientRouteRows = COUNT(), patientActualRows = SUM(o != null ? 1 : 0), patientEmptySecondRows = SUM(o == null ? 1 : 0), textMissingRows = SUM(textPresent ? 0 : 1), textNullRows = SUM(textPresent AND IS_NULL(textValue) ? 1 : 0), textStringRows = SUM(textPresent AND IS_STRING(textValue) ? 1 : 0), textOtherRows = SUM(textPresent AND !IS_NULL(textValue) AND !IS_STRING(textValue) ? 1 : 0), numericCount = SUM(IS_NUMBER(value) ? 1 : 0), missingValueCount = SUM(o != null AND !valuePresent ? 1 : 0), explicitNullValueCount = SUM(o != null AND valuePresent AND IS_NULL(value) ? 1 : 0), terminalNullValueRows = SUM(o == null ? 1 : 0), nonNumericValueCount = SUM(o != null AND valuePresent AND !IS_NULL(value) AND !IS_NUMBER(value) ? 1 : 0), numericSum = SUM(IS_NUMBER(value) ? value : 0), numericMax = MAX(IS_NUMBER(value) ? value : null)
     RETURN {patientId: patientIdKey, specimenMultiplicity: specimenMultiplicityKey, firstHopMissing: false, secondHopMissing: secondHopMissingKey, observationPresent: observationPresentKey, conceptPresent: conceptPresentKey, conceptType: conceptTypeKey, textPresent: textPresentKey, textType: textTypeKey, text: textValueKey, quantityPresent: quantityPresentKey, quantityType: quantityTypeKey, codePresent: codePresentKey, codeType: codeTypeKey, code: codeValueKey, patientRouteRows, patientActualRows, patientEmptySecondRows, textMissingRows, textNullRows, textStringRows, textOtherRows, numericCount, missingValueCount, explicitNullValueCount, terminalNullValueRows, nonNumericValueCount, numericSum, numericMax}
)
LET weightedGroups = (
 FOR g IN patientGroups
   COLLECT firstHopMissing = g.firstHopMissing, secondHopMissing = g.secondHopMissing, observationPresent = g.observationPresent, conceptPresent = g.conceptPresent, conceptType = g.conceptType, textPresent = g.textPresent, textType = g.textType, text = g.text, quantityPresent = g.quantityPresent, quantityType = g.quantityType, codePresent = g.codePresent, codeType = g.codeType, code = g.code
   AGGREGATE routeRows = SUM(g.patientRouteRows * g.specimenMultiplicity), actualRouteRows = SUM(g.patientActualRows * g.specimenMultiplicity), emptyFirstHopRows = SUM(0), emptySecondHopRows = SUM(g.patientEmptySecondRows * g.specimenMultiplicity), textMissingRows = SUM(g.textMissingRows * g.specimenMultiplicity), textNullRows = SUM(g.textNullRows * g.specimenMultiplicity), textStringRows = SUM(g.textStringRows * g.specimenMultiplicity), textOtherRows = SUM(g.textOtherRows * g.specimenMultiplicity), numericCount = SUM(g.numericCount * g.specimenMultiplicity), missingValueCount = SUM(g.missingValueCount * g.specimenMultiplicity), explicitNullValueCount = SUM(g.explicitNullValueCount * g.specimenMultiplicity), terminalNullValueRows = SUM(g.terminalNullValueRows * g.specimenMultiplicity), nonNumericValueCount = SUM(g.nonNumericValueCount * g.specimenMultiplicity), numericSum = SUM(g.numericSum * g.specimenMultiplicity), numericMax = MAX(g.numericMax)
   RETURN {firstHopMissing, secondHopMissing, observationPresent, conceptPresent, conceptType, textPresent, textType, text, quantityPresent, quantityType, codePresent, codeType, code, routeRows, actualRouteRows, emptyFirstHopRows, emptySecondHopRows, textMissingRows, textNullRows, textStringRows, textOtherRows, numericCount, missingValueCount, explicitNullValueCount, terminalNullValueRows, nonNumericValueCount, numericSum, numericMax}
)
LET matchedSpecimenCount = LENGTH(
 FOR eSP IN fhir_edge
   FILTER eSP.project == \u0040project AND eSP.dataset_generation == \u0040generation AND eSP.label == "subject_Patient" AND eSP.from_type == "Specimen" AND eSP.to_type == "Patient"
   LET eSPAllowed = \u0040auth_resource_paths_unrestricted == true OR eSP.auth_resource_path IN authResourcePaths
   FILTER eSPAllowed == \u0040scope_allowed
   LET s = DOCUMENT(eSP._from)
   FILTER s != null AND s.project == \u0040project AND s.dataset_generation == \u0040generation AND s.resourceType == "Specimen" AND s.payload.resourceType == "Specimen"
   LET sAllowed = \u0040auth_resource_paths_unrestricted == true OR s.auth_resource_path IN authResourcePaths
   FILTER sAllowed == \u0040scope_allowed
   LET p = DOCUMENT(eSP._to)
   FILTER p != null AND p.project == \u0040project AND p.dataset_generation == \u0040generation AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient"
   LET pAllowed = \u0040auth_resource_paths_unrestricted == true OR p.auth_resource_path IN authResourcePaths
   FILTER pAllowed == \u0040scope_allowed
   COLLECT specimenId = s._id
   RETURN 1
)
LET emptyFirstHopSpecimenCount = scopedSpecimenCount - matchedSpecimenCount
LET unmatchedSpecimenGroups = emptyFirstHopSpecimenCount == 0 ? [] : [{firstHopMissing: true, secondHopMissing: false, observationPresent: false, conceptPresent: true, conceptType: "NULL", textPresent: true, textType: "NULL", text: null, quantityPresent: true, quantityType: "NULL", codePresent: true, codeType: "NULL", code: null, routeRows: emptyFirstHopSpecimenCount, actualRouteRows: 0, emptyFirstHopRows: emptyFirstHopSpecimenCount, emptySecondHopRows: 0, textMissingRows: 0, textNullRows: emptyFirstHopSpecimenCount, textStringRows: 0, textOtherRows: 0, numericCount: 0, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: emptyFirstHopSpecimenCount, nonNumericValueCount: 0, numericSum: 0, numericMax: null}]
LET rawGroups = APPEND(weightedGroups, unmatchedSpecimenGroups)
LET pivotCells = (
 FOR g IN rawGroups
   COLLECT text = g.text, categoryPresent = g.codePresent, categoryType = g.codeType, category = g.code
   AGGREGATE routeRows = SUM(g.routeRows), actualRouteRows = SUM(g.actualRouteRows), emptyFirstHopRows = SUM(g.emptyFirstHopRows), emptySecondHopRows = SUM(g.emptySecondHopRows), textMissingRows = SUM(g.textMissingRows), textNullRows = SUM(g.textNullRows), textStringRows = SUM(g.textStringRows), textOtherRows = SUM(g.textOtherRows), numericCount = SUM(g.numericCount), missingValueCount = SUM(g.missingValueCount), explicitNullValueCount = SUM(g.explicitNullValueCount), terminalNullValueRows = SUM(g.terminalNullValueRows), nonNumericValueCount = SUM(g.nonNumericValueCount), numericSum = SUM(g.numericSum), numericMax = MAX(g.numericMax)
   RETURN {text, categoryPresent, categoryType, category, routeRows, actualRouteRows, emptyFirstHopRows, emptySecondHopRows, textMissingRows, textNullRows, textStringRows, textOtherRows, numericCount, missingValueCount, explicitNullValueCount, terminalNullValueRows, nonNumericValueCount, sum: numericCount == 0 ? null : numericSum, max: numericCount == 0 ? null : numericMax}
)
LET visibleTextGroupCount = LENGTH(FOR c IN pivotCells COLLECT text = c.text RETURN 1)
LET categoryDomainCount = LENGTH(FOR c IN pivotCells COLLECT categoryPresent = c.categoryPresent, categoryType = c.categoryType, category = c.category RETURN 1)
LET categoryDomain = (FOR c IN pivotCells COLLECT categoryPresent = c.categoryPresent, categoryType = c.categoryType, category = c.category SORT categoryPresent, categoryType, category RETURN {categoryPresent, categoryType, category})
LET visiblePivotCellCount = LENGTH(pivotCells)
LET dWitnessCount = LENGTH(FOR c IN pivotCells FILTER c.categoryPresent AND c.categoryType == "STRING" AND c.category == "d" AND c.numericCount >= 2 AND c.sum != c.max RETURN 1)
LET dWitnessPreview = (FOR c IN pivotCells FILTER c.categoryPresent AND c.categoryType == "STRING" AND c.category == "d" AND c.numericCount >= 2 AND c.sum != c.max SORT c.text LIMIT 5 RETURN {text: c.text, categoryPresent: c.categoryPresent, categoryType: c.categoryType, category: c.category, routeRows: c.routeRows, numericCount: c.numericCount, sum: c.sum, max: c.max, textMissingRows: c.textMissingRows, textNullRows: c.textNullRows})
LET actualRouteRowCount = SUM(weightedGroups[*].actualRouteRows)
LET emptySecondHopPatientRowCount = SUM(weightedGroups[*].emptySecondHopRows)
LET leftJoinOutputRowCount = SUM(rawGroups[*].routeRows)
LET weightedObservationPatientPairCount = SUM(patientGroups[*].patientActualRows)
LET patientGroupCount = LENGTH(patientGroups)
LET rawGroupCount = LENGTH(rawGroups)
LET selectedTextGroupKeys = \u0040visible_text_keys
LET selectedTextGroupCount = LENGTH(selectedTextGroupKeys)
LET selectedTextKeysUnique = LENGTH(selectedTextGroupKeys) == LENGTH(UNIQUE(selectedTextGroupKeys))
LET selectedTextKeysWithinLimit = selectedTextGroupCount <= 25
LET selectedTextKeysValid = LENGTH(FOR key IN selectedTextGroupKeys FILTER key == null OR IS_STRING(key) RETURN 1) == selectedTextGroupCount
LET selectedMatchedTextGroupCount = LENGTH(FOR c IN pivotCells FILTER c.text IN selectedTextGroupKeys COLLECT text = c.text RETURN 1)
LET rawTypedTextCodePreview = (FOR g IN rawGroups FILTER g.text IN selectedTextGroupKeys SORT g.textType, g.text, g.codeType, g.code, g.firstHopMissing, g.secondHopMissing RETURN g)
LET visiblePivotCellPreview = (FOR c IN pivotCells FILTER c.text IN selectedTextGroupKeys SORT c.text, c.categoryPresent, c.categoryType, c.category RETURN c)
LET selectedRawGroupCount = LENGTH(rawTypedTextCodePreview)
LET selectedPivotCellCount = LENGTH(visiblePivotCellPreview)
LET resultBounds = {maxPreviewRows: 25, categoryDomainCountFits: categoryDomainCount == LENGTH(categoryDomain)}
LET fullRouteCounts = {totalSpecimens: scopedSpecimenCount, matchedSpecimens: matchedSpecimenCount, emptyFirstHopSpecimenRoots: emptyFirstHopSpecimenCount, distinctSpecimenPatientPairs: SUM(patientMultiplicities[*].specimenMultiplicity), patientsWithSpecimenRoots: LENGTH(patientMultiplicities), patientObservationGroups: patientGroupCount, uniquePatientObservationPairs: weightedObservationPatientPairCount, emptySecondHopPatientRows: emptySecondHopPatientRowCount, actualRouteRows: actualRouteRowCount, leftJoinOutputRows: leftJoinOutputRowCount, rawTextTypedCodeGroupCount: rawGroupCount, visibleTextGroupCount, visiblePivotCellCount, categoryDomainCount, nonNumericValueCount: SUM(weightedGroups[*].nonNumericValueCount), resultBounds}
RETURN {project: \u0040project, generation: \u0040generation, authScope: {auth_resource_paths_unrestricted: \u0040auth_resource_paths_unrestricted, auth_resource_paths: \u0040auth_resource_paths, scope_allowed: \u0040scope_allowed}, scopedDocumentCounts: {Specimen: scopedSpecimenCount, Patient: scopedPatientCount, Observation: scopedObservationCount}, scopedTypedEdgeCounts: {Specimen_subject_Patient: specimenPatientEdgeCount, Observation_subject_Patient: observationPatientEdgeCount}, fullRouteCounts, factoredRouteCounts: fullRouteCounts, visible_text_keys: selectedTextGroupKeys, selectedTextGroupKeys, selectedTextGroupCount, selectedMatchedTextGroupCount, selectedTextKeysUnique, selectedTextKeysWithinLimit, selectedTextKeysValid, selectedRawGroupCount, selectedPivotCellCount, resultBounds, dDuplicateWitnessBucketCount: dWitnessCount, dDuplicateWitnesses: dWitnessPreview, categoryDomainCount, categoryDomainPreview: categoryDomain, rawTypedTextCodePreview, visiblePivotCellPreview}`;
  return { query, bindVars, emptyPolicies: [...scope.emptyPolicies], previewLimit: 25, visibleTextKeys: [...visibleTextKeys] };
}

const safeCount = (value, label) => {
  if (!Number.isSafeInteger(value) || value < 0) throw new Error(`${label} must be a non-negative safe integer`);
  return value;
};

const discoveryAuthScope = scope => {
  if (!scope || typeof scope !== 'object' || Array.isArray(scope)) throw new Error('Explicit related quantity Pivot scope is required');
  if (scope.scope_allowed !== true) throw new Error('Related quantity Pivot discovery requires allow-mode scope');
  assertString(scope.project, 'project');
  assertString(scope.dataset_generation, 'dataset_generation');
  if (!Array.isArray(scope.emptyPolicies) || scope.emptyPolicies.length !== 2
    || scope.emptyPolicies.some(policy => policy !== 'PRESERVE_PARENT')) {
    throw new Error('Related quantity Pivot discovery requires PRESERVE_PARENT at both authored Related boundaries');
  }
  if (typeof scope.auth_resource_paths_unrestricted !== 'boolean') throw new Error('Explicit auth_resource_paths_unrestricted scope mode is required');
  if (!Array.isArray(scope.auth_resource_paths) || scope.auth_resource_paths.some(path => typeof path !== 'string')) throw new Error('auth_resource_paths must be an explicit string array');
  if (scope.auth_resource_paths_unrestricted && scope.auth_resource_paths.length !== 0) throw new Error('Unrestricted local scope must not carry auth resource paths');
  return {
    project: scope.project,
    generation: scope.dataset_generation,
    authScope: {
      auth_resource_paths_unrestricted: scope.auth_resource_paths_unrestricted,
      auth_resource_paths: [...scope.auth_resource_paths],
      scope_allowed: scope.scope_allowed,
    },
  };
};

const discoveryBindVars = scope => ({
  project: scope.project,
  generation: scope.dataset_generation,
  auth_resource_paths_unrestricted: scope.auth_resource_paths_unrestricted,
  auth_resource_paths: [...scope.auth_resource_paths],
  scope_allowed: scope.scope_allowed,
});

const boundedLimit = (value, label, { maximum = 100000 } = {}) => {
  if (!Number.isSafeInteger(value) || value < 1 || value > maximum) throw new Error(`${label} must be an integer from 1 to ${maximum}`);
  return value;
};

const distinctNonEmptyIds = (values, label, maximum = 1000) => {
  if (!Array.isArray(values) || values.length === 0 || values.length > maximum
    || values.some(value => typeof value !== 'string' || value.trim() === '')) {
    throw new Error(`${label} must be a non-empty bounded array of document IDs`);
  }
  if (new Set(values).size !== values.length) throw new Error(`${label} must not contain duplicate IDs`);
  return [...values];
};

const scopeEchoMatches = (payload, scopeIdentity) => payload?.project === scopeIdentity.project
  && payload?.generation === scopeIdentity.generation
  && JSON.stringify(payload?.authScope) === JSON.stringify(scopeIdentity.authScope);

const singleDiscoveryPayload = result => {
  const payload = Array.isArray(result) && result.length === 1 ? result[0] : result;
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('Related quantity discovery query must return one result object');
  return payload;
};

export function buildRelatedQuantityPivotSpecimenPage(scope, { afterSpecimenKey = '', pageSize = 100 } = {}) {
  discoveryAuthScope(scope);
  if (typeof afterSpecimenKey !== 'string') throw new Error('afterSpecimenKey must be a string');
  boundedLimit(pageSize, 'pageSize', { maximum: 1000 });
  const bindVars = {
    ...discoveryBindVars(scope),
    '@root_collection': 'Specimen',
    after_specimen_key: afterSpecimenKey,
    page_size: pageSize,
  };
  const query = `LET authResourcePaths = @auth_resource_paths
LET candidates = (
  FOR s IN @@root_collection
    FILTER s._key > @after_specimen_key
      AND s.project == @project AND s.dataset_generation == @generation
      AND s.resourceType == "Specimen" AND s.payload.resourceType == "Specimen"
    LET allowed = @auth_resource_paths_unrestricted == true OR s.auth_resource_path IN authResourcePaths
    FILTER allowed == @scope_allowed
    SORT s._key
    LIMIT @page_size + 1
    RETURN {specimenId: s._id, specimenKey: s._key}
)
LET specimens = SLICE(candidates, 0, @page_size)
LET hasMore = LENGTH(candidates) > @page_size
RETURN {
  project: @project,
  generation: @generation,
  authScope: {auth_resource_paths_unrestricted: @auth_resource_paths_unrestricted, auth_resource_paths: @auth_resource_paths, scope_allowed: @scope_allowed},
  afterSpecimenKey: @after_specimen_key,
  pageSize: @page_size,
  specimens,
  hasMore,
  nextAfterSpecimenKey: hasMore ? LAST(specimens).specimenKey : null
}`;
  return { query, bindVars, pageSize, afterSpecimenKey };
}

export function buildRelatedQuantityPivotSpecimenPatientPairs(scope, specimenIds, { maxRows = 10000 } = {}) {
  discoveryAuthScope(scope);
  const ids = distinctNonEmptyIds(specimenIds, 'specimenIds');
  boundedLimit(maxRows, 'maxRows');
  const bindVars = {
    ...discoveryBindVars(scope),
    '@root_collection': 'Specimen',
    specimen_ids: ids,
    max_rows: maxRows,
  };
  const query = `LET authResourcePaths = @auth_resource_paths
LET scopedSpecimenIds = (
  FOR specimenId IN @specimen_ids
    LET s = DOCUMENT(specimenId)
    FILTER s != null AND s.project == @project AND s.dataset_generation == @generation
      AND s.resourceType == "Specimen" AND s.payload.resourceType == "Specimen"
    LET allowed = @auth_resource_paths_unrestricted == true OR s.auth_resource_path IN authResourcePaths
    FILTER allowed == @scope_allowed
    RETURN s._id
)
LET patientPairs = (
  FOR e IN fhir_edge
    FILTER e._from IN scopedSpecimenIds AND e.project == @project AND e.dataset_generation == @generation
      AND e.label == "subject_Patient" AND e.from_type == "Specimen" AND e.to_type == "Patient"
    LET edgeAllowed = @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN authResourcePaths
    FILTER edgeAllowed == @scope_allowed
    LET s = DOCUMENT(e._from)
    FILTER s != null AND s.project == @project AND s.dataset_generation == @generation
      AND s.resourceType == "Specimen" AND s.payload.resourceType == "Specimen"
    LET specimenAllowed = @auth_resource_paths_unrestricted == true OR s.auth_resource_path IN authResourcePaths
    FILTER specimenAllowed == @scope_allowed
    LET p = DOCUMENT(e._to)
    FILTER p != null AND p.project == @project AND p.dataset_generation == @generation
      AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient"
    LET patientAllowed = @auth_resource_paths_unrestricted == true OR p.auth_resource_path IN authResourcePaths
    FILTER patientAllowed == @scope_allowed
    COLLECT patientId = p._id, specimenId = s._id
    SORT specimenId, patientId
    LIMIT @max_rows + 1
    RETURN {specimenId, patientId}
)
LET matchedSpecimenIds = UNIQUE(patientPairs[*].specimenId)
LET emptyRootRows = (
  FOR specimenId IN scopedSpecimenIds
    FILTER specimenId NOT IN matchedSpecimenIds
    RETURN {specimenId, patientId: null}
)
LET allRows = APPEND(patientPairs, emptyRootRows)
LET sortedRows = (FOR row IN allRows SORT row.specimenId, row.patientId RETURN row)
LET overflow = LENGTH(sortedRows) > @max_rows
RETURN {
  project: @project,
  generation: @generation,
  authScope: {auth_resource_paths_unrestricted: @auth_resource_paths_unrestricted, auth_resource_paths: @auth_resource_paths, scope_allowed: @scope_allowed},
  specimenIds: @specimen_ids,
  rows: SLICE(sortedRows, 0, @max_rows),
  overflow,
  truncated: overflow
}`;
  return { query, bindVars, specimenIds: ids, maxRows };
}

export function buildRelatedQuantityPivotPatientBatch(scope, patientIds, { maxRows = 25000 } = {}) {
  discoveryAuthScope(scope);
  const ids = distinctNonEmptyIds(patientIds, 'patientIds', 100);
  boundedLimit(maxRows, 'maxRows');
  const bindVars = {
    ...discoveryBindVars(scope),
    patient_ids: ids,
    max_rows: maxRows,
  };
  const query = `LET authResourcePaths = @auth_resource_paths
LET scopedPatientIds = (
  FOR patientId IN @patient_ids
    LET p = DOCUMENT(patientId)
    FILTER p != null AND p.project == @project AND p.dataset_generation == @generation
      AND p.resourceType == "Patient" AND p.payload.resourceType == "Patient"
    LET allowed = @auth_resource_paths_unrestricted == true OR p.auth_resource_path IN authResourcePaths
    FILTER allowed == @scope_allowed
    RETURN p._id
)
LET observationGroups = (
  FOR e IN fhir_edge
    FILTER e._to IN scopedPatientIds AND e.project == @project AND e.dataset_generation == @generation
      AND e.label == "subject_Patient" AND e.from_type == "Observation" AND e.to_type == "Patient"
    LET edgeAllowed = @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN authResourcePaths
    FILTER edgeAllowed == @scope_allowed
    LET candidate = DOCUMENT(e._from)
    FILTER candidate != null AND candidate.project == @project AND candidate.dataset_generation == @generation
      AND candidate.resourceType == "Observation" AND candidate.payload.resourceType == "Observation"
    LET observationAllowed = @auth_resource_paths_unrestricted == true OR candidate.auth_resource_path IN authResourcePaths
    FILTER observationAllowed == @scope_allowed
    COLLECT observationId = candidate._id, patientId = e._to
    LET o = DOCUMENT(observationId)
    LET conceptPresent = HAS(o.payload, "valueCodeableConcept")
    LET concept = o.payload.valueCodeableConcept
    LET textPresent = IS_OBJECT(concept) AND HAS(concept, "text")
    LET text = textPresent ? concept.text : null
    LET textType = !textPresent ? "MISSING" : IS_NULL(text) ? "NULL" : IS_STRING(text) ? "STRING" : IS_OBJECT(text) ? "OBJECT" : IS_ARRAY(text) ? "ARRAY" : IS_NUMBER(text) ? "NUMBER" : IS_BOOL(text) ? "BOOL" : "OTHER"
    LET quantityPresent = HAS(o.payload, "valueQuantity")
    LET quantity = o.payload.valueQuantity
    LET quantityType = !quantityPresent ? "MISSING" : IS_NULL(quantity) ? "NULL" : IS_OBJECT(quantity) ? "OBJECT" : IS_ARRAY(quantity) ? "ARRAY" : IS_STRING(quantity) ? "STRING" : IS_NUMBER(quantity) ? "NUMBER" : IS_BOOL(quantity) ? "BOOL" : "OTHER"
    LET codePresent = IS_OBJECT(quantity) AND HAS(quantity, "code")
    LET code = codePresent ? quantity.code : null
    LET codeType = !codePresent ? "MISSING" : IS_NULL(code) ? "NULL" : IS_STRING(code) ? "STRING" : IS_OBJECT(code) ? "OBJECT" : IS_ARRAY(code) ? "ARRAY" : IS_NUMBER(code) ? "NUMBER" : IS_BOOL(code) ? "BOOL" : "OTHER"
    LET valuePresent = IS_OBJECT(quantity) AND HAS(quantity, "value")
    LET value = valuePresent ? quantity.value : null
    COLLECT patientIdKey = patientId,
      conceptPresentKey = conceptPresent, conceptTypeKey = !conceptPresent ? "MISSING" : IS_NULL(concept) ? "NULL" : IS_OBJECT(concept) ? "OBJECT" : IS_ARRAY(concept) ? "ARRAY" : IS_STRING(concept) ? "STRING" : IS_NUMBER(concept) ? "NUMBER" : IS_BOOL(concept) ? "BOOL" : "OTHER",
      textPresentKey = textPresent, textTypeKey = textType, textKey = text,
      quantityPresentKey = quantityPresent, quantityTypeKey = quantityType,
      codePresentKey = codePresent, codeTypeKey = codeType, codeKey = code
    AGGREGATE routeRows = COUNT(), actualRouteRows = COUNT(), emptySecondHopRows = SUM(0),
      textMissingRows = SUM(textPresent ? 0 : 1), textNullRows = SUM(textPresent AND IS_NULL(text) ? 1 : 0),
      textStringRows = SUM(textPresent AND IS_STRING(text) ? 1 : 0), textOtherRows = SUM(textPresent AND !IS_NULL(text) AND !IS_STRING(text) ? 1 : 0),
      numericCount = SUM(IS_NUMBER(value) ? 1 : 0), missingValueCount = SUM(!valuePresent ? 1 : 0),
      explicitNullValueCount = SUM(valuePresent AND IS_NULL(value) ? 1 : 0), terminalNullValueRows = SUM(0),
      nonNumericValueCount = SUM(valuePresent AND !IS_NULL(value) AND !IS_NUMBER(value) ? 1 : 0),
      numericSum = SUM(IS_NUMBER(value) ? value : 0), numericMax = MAX(IS_NUMBER(value) ? value : null)
    SORT patientIdKey, textTypeKey, TO_STRING(textKey), codeTypeKey, TO_STRING(codeKey)
    LIMIT @max_rows + 1
    RETURN {
      patientId: patientIdKey, firstHopMissing: false, secondHopMissing: false, observationPresent: true,
      conceptPresent: conceptPresentKey, conceptType: conceptTypeKey,
      textPresent: textPresentKey, textType: textTypeKey, text: textKey,
      quantityPresent: quantityPresentKey, quantityType: quantityTypeKey, codePresent: codePresentKey, codeType: codeTypeKey, code: codeKey,
      routeRows, actualRouteRows, emptyFirstHopRows: 0, emptySecondHopRows, textMissingRows, textNullRows, textStringRows, textOtherRows,
      numericCount, missingValueCount, explicitNullValueCount, terminalNullValueRows, nonNumericValueCount, numericSum, numericMax
    }
)
LET matchedPatientIds = UNIQUE(observationGroups[*].patientId)
LET emptyPatientGroups = (
  FOR patientId IN scopedPatientIds
    FILTER patientId NOT IN matchedPatientIds
    RETURN {
      patientId, firstHopMissing: false, secondHopMissing: true, observationPresent: false,
      conceptPresent: true, conceptType: "NULL", textPresent: true, textType: "NULL", text: null,
      quantityPresent: true, quantityType: "NULL", codePresent: true, codeType: "NULL", code: null,
      routeRows: 1, actualRouteRows: 0, emptyFirstHopRows: 0, emptySecondHopRows: 1,
      textMissingRows: 0, textNullRows: 1, textStringRows: 0, textOtherRows: 0,
      numericCount: 0, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 1,
      nonNumericValueCount: 0, numericSum: 0, numericMax: null
    }
)
LET allGroups = APPEND(observationGroups, emptyPatientGroups)
LET sortedGroups = (FOR group IN allGroups SORT group.patientId, group.textType, TO_STRING(group.text), group.codeType, TO_STRING(group.code), group.secondHopMissing RETURN group)
LET boundedGroups = SLICE(sortedGroups, 0, @max_rows + 1)
LET overflow = LENGTH(boundedGroups) > @max_rows
RETURN {
  project: @project,
  generation: @generation,
  authScope: {auth_resource_paths_unrestricted: @auth_resource_paths_unrestricted, auth_resource_paths: @auth_resource_paths, scope_allowed: @scope_allowed},
  patientIds: @patient_ids,
  groups: SLICE(boundedGroups, 0, @max_rows),
  overflow,
  truncated: overflow
}`;
  return { query, bindVars, patientIds: ids, maxRows };
}

const stableValue = value => {
  if (Array.isArray(value)) return value.map(stableValue);
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map(key => [key, stableValue(value[key])]));
  return value;
};

const stableJson = value => JSON.stringify(stableValue(value));

const typeValueIdentity = (present, type, value) => JSON.stringify([present === true, type, stableValue(value)]);

const validateTypedField = (row, stem) => {
  const present = row[`${stem}Present`];
  const type = row[`${stem}Type`];
  const value = row[stem];
  if (typeof present !== 'boolean' || typeof type !== 'string') throw new Error(`Patient group must preserve typed ${stem} presence and type`);
  if (!['MISSING', 'NULL', 'STRING', 'OBJECT', 'ARRAY', 'NUMBER', 'BOOL', 'OTHER'].includes(type)) throw new Error(`Unsupported ${stem} type ${JSON.stringify(type)}`);
  if ((!present && (type !== 'MISSING' || value !== null)) || (present && type === 'MISSING')) throw new Error(`${stem} type/presence mismatch`);
  if ((type === 'NULL' && value !== null) || (type === 'STRING' && typeof value !== 'string')
    || (type === 'OBJECT' && (!value || typeof value !== 'object' || Array.isArray(value)))
    || (type === 'ARRAY' && !Array.isArray(value)) || (type === 'NUMBER' && (typeof value !== 'number' || !Number.isFinite(value)))
    || (type === 'BOOL' && typeof value !== 'boolean')) throw new Error(`${stem} value does not match its declared type`);
  return { present, type, value };
};

const rawGroupIdentity = row => stableJson([
  row.firstHopMissing, row.secondHopMissing, row.observationPresent,
  row.conceptPresent, row.conceptType,
  row.textPresent, row.textType, row.text,
  row.quantityPresent, row.quantityType,
  row.codePresent, row.codeType, row.code,
]);

const addWeightedRawGroup = (groups, row, multiplicity) => {
  const identity = rawGroupIdentity(row);
  let target = groups.get(identity);
  const counterFields = [
    'routeRows', 'actualRouteRows', 'emptyFirstHopRows', 'emptySecondHopRows',
    'textMissingRows', 'textNullRows', 'textStringRows', 'textOtherRows',
    'numericCount', 'missingValueCount', 'explicitNullValueCount', 'terminalNullValueRows', 'nonNumericValueCount',
  ];
  if (!target) {
    target = {
      patientId: null,
      firstHopMissing: row.firstHopMissing,
      secondHopMissing: row.secondHopMissing,
      observationPresent: row.observationPresent,
      conceptPresent: row.conceptPresent,
      conceptType: row.conceptType,
      textPresent: row.textPresent,
      textType: row.textType,
      text: row.text,
      quantityPresent: row.quantityPresent,
      quantityType: row.quantityType,
      codePresent: row.codePresent,
      codeType: row.codeType,
      code: row.code,
      routeRows: 0,
      actualRouteRows: 0,
      emptyFirstHopRows: 0,
      emptySecondHopRows: 0,
      textMissingRows: 0,
      textNullRows: 0,
      textStringRows: 0,
      textOtherRows: 0,
      numericCount: 0,
      missingValueCount: 0,
      explicitNullValueCount: 0,
      terminalNullValueRows: 0,
      nonNumericValueCount: 0,
      numericSum: 0,
      numericMax: null,
    };
    groups.set(identity, target);
  }
  for (const field of counterFields) target[field] += safeCount(row[field], `patient group ${field}`) * multiplicity;
  if (counterFields.some(field => !Number.isSafeInteger(target[field]))) throw new Error('Weighted route count exceeded the safe integer range');
  if (typeof row.numericSum !== 'number' || !Number.isFinite(row.numericSum)) throw new Error('Patient group numericSum must be finite');
  target.numericSum += row.numericSum * multiplicity;
  if (!Number.isFinite(target.numericSum)) throw new Error('Weighted numericSum exceeded the finite numeric range');
  if (row.numericMax !== null && (typeof row.numericMax !== 'number' || !Number.isFinite(row.numericMax))) throw new Error('Patient group numericMax must be null or finite');
  if (row.numericMax !== null) target.numericMax = target.numericMax === null ? row.numericMax : Math.max(target.numericMax, row.numericMax);
};

export function createRelatedQuantityPivotDiscoveryAccumulator(scope, {
  specimenPageSize = 100,
  maxSpecimenPatientRows = 10000,
  maxPatientGroups = 25000,
} = {}) {
  const scopeIdentity = discoveryAuthScope(scope);
  boundedLimit(specimenPageSize, 'specimenPageSize', { maximum: 1000 });
  boundedLimit(maxSpecimenPatientRows, 'maxSpecimenPatientRows');
  boundedLimit(maxPatientGroups, 'maxPatientGroups');
  let expectedAfterKey = '';
  let previousPageAdvertisedMore = false;
  let currentPage = null;
  let pagesDone = false;
  let totalSpecimens = 0;
  let emptyFirstHopSpecimenRoots = 0;
  let distinctSpecimenPatientPairs = 0;
  const patientMultiplicity = new Map();
  let patientBatchCursor = '';
  let activePatientBatch = null;
  let patientObservationGroups = 0;
  let uniquePatientObservationPairs = 0;
  let emptySecondHopPatientRows = 0;
  let emptySecondHopPatientCount = 0;
  const rawGroups = new Map();
  let finalized = false;

  const assertPayloadScope = payload => {
    if (!scopeEchoMatches(payload, scopeIdentity)) throw new Error('Related quantity discovery scope identity drifted between bounded queries');
  };

  const validateGroup = row => {
    if (!row || typeof row !== 'object' || Array.isArray(row)) throw new Error('Patient batch group must be an object');
    assertString(row.patientId, 'patientId');
    for (const field of ['firstHopMissing', 'secondHopMissing', 'observationPresent', 'conceptPresent', 'quantityPresent', 'codePresent']) {
      if (typeof row[field] !== 'boolean') throw new Error(`Patient batch group must preserve ${field}`);
    }
    if (row.firstHopMissing !== false || row.observationPresent !== !row.secondHopMissing) throw new Error('Patient batch group has inconsistent join-boundary presence');
    const conceptType = row.conceptType;
    if (typeof conceptType !== 'string' || !['MISSING', 'NULL', 'STRING', 'OBJECT', 'ARRAY', 'NUMBER', 'BOOL', 'OTHER'].includes(conceptType)) throw new Error('Patient batch group has unsupported valueCodeableConcept type');
    if ((!row.conceptPresent && conceptType !== 'MISSING') || (row.conceptPresent && conceptType === 'MISSING')) throw new Error('valueCodeableConcept type/presence mismatch');
    validateTypedField(row, 'text');
    validateTypedField(row, 'code');
    if (typeof row.quantityType !== 'string' || !['MISSING', 'NULL', 'STRING', 'OBJECT', 'ARRAY', 'NUMBER', 'BOOL', 'OTHER'].includes(row.quantityType)) throw new Error('Patient batch group has unsupported valueQuantity type');
    if ((!row.quantityPresent && row.quantityType !== 'MISSING') || (row.quantityPresent && row.quantityType === 'MISSING')) throw new Error('valueQuantity type/presence mismatch');
    const routeRows = safeCount(row.routeRows, 'patient group routeRows');
    const actualRows = safeCount(row.actualRouteRows, 'patient group actualRouteRows');
    const emptySecondRows = safeCount(row.emptySecondHopRows, 'patient group emptySecondHopRows');
    if (safeCount(row.emptyFirstHopRows, 'patient group emptyFirstHopRows') !== 0) throw new Error('Patient batch cannot contain a first-hop sentinel');
    if (routeRows === 0 || routeRows !== actualRows + emptySecondRows
      || (row.secondHopMissing && (routeRows !== 1 || actualRows !== 0 || emptySecondRows !== 1))
      || (!row.secondHopMissing && (actualRows !== routeRows || emptySecondRows !== 0))) {
      throw new Error('Patient group route counts do not match its second-hop presence');
    }
    if (row.secondHopMissing && (row.conceptPresent !== true || row.conceptType !== 'NULL'
      || row.textPresent !== true || row.textType !== 'NULL'
      || row.quantityPresent !== true || row.quantityType !== 'NULL'
      || row.codePresent !== true || row.codeType !== 'NULL')) throw new Error('Empty-second-hop sentinel must preserve explicit NULL projected values');
    const textCounts = ['textMissingRows', 'textNullRows', 'textStringRows', 'textOtherRows'].map(field => safeCount(row[field], `patient group ${field}`));
    if (textCounts.reduce((sum, count) => sum + count, 0) !== routeRows) throw new Error('Patient group text counts do not reconcile to route rows');
    const expectedTextField = row.textType === 'MISSING' ? 'textMissingRows' : row.textType === 'NULL' ? 'textNullRows'
      : row.textType === 'STRING' ? 'textStringRows' : 'textOtherRows';
    if (row[expectedTextField] !== routeRows || textCounts.filter((_, index) => ['textMissingRows', 'textNullRows', 'textStringRows', 'textOtherRows'][index] !== expectedTextField).some(count => count !== 0)) {
      throw new Error('Patient group text counts do not match its typed text key');
    }
    const valueCounts = ['numericCount', 'missingValueCount', 'explicitNullValueCount', 'terminalNullValueRows', 'nonNumericValueCount'].map(field => safeCount(row[field], `patient group ${field}`));
    if (valueCounts.reduce((sum, count) => sum + count, 0) !== routeRows
      || row.terminalNullValueRows !== (row.secondHopMissing ? routeRows : 0)) throw new Error('Patient group value counts do not reconcile to route rows');
    if (typeof row.numericSum !== 'number' || !Number.isFinite(row.numericSum)
      || (row.numericMax !== null && (typeof row.numericMax !== 'number' || !Number.isFinite(row.numericMax)))) throw new Error('Patient group numeric aggregates must be finite');
    if ((row.numericCount === 0 && (row.numericSum !== 0 || row.numericMax !== null))
      || (row.numericCount > 0 && row.numericMax === null)) throw new Error('Patient group numeric count does not match SUM/MAX');
  };

  return {
    addSpecimenPage(result) {
      if (finalized || pagesDone || currentPage) throw new Error('Cannot add a specimen page before consuming the current page or after pagination ends');
      const payload = singleDiscoveryPayload(result);
      assertPayloadScope(payload);
      if (payload.overflow === true || payload.truncated === true) throw new Error('Specimen page query reported overflow or truncation');
      if (payload.afterSpecimenKey !== expectedAfterKey || payload.pageSize !== specimenPageSize) throw new Error('Specimen page cursor/page-size does not match the requested bounded page');
      if (typeof payload.hasMore !== 'boolean' || !Array.isArray(payload.specimens) || payload.specimens.length > specimenPageSize) throw new Error('Specimen page is malformed or exceeds its requested limit');
      if (payload.hasMore && payload.specimens.length !== specimenPageSize) throw new Error('Specimen page claims more rows without filling the bounded page');
      if (previousPageAdvertisedMore && payload.specimens.length === 0) throw new Error('Specimen continuation page is empty after its predecessor advertised more rows');
      const ids = [];
      let previousKey = expectedAfterKey;
      for (const specimen of payload.specimens) {
        if (!specimen || typeof specimen !== 'object' || Array.isArray(specimen)) throw new Error('Specimen page row must be an object');
        assertString(specimen.specimenId, 'specimenId');
        assertString(specimen.specimenKey, 'specimenKey');
        if (specimen.specimenKey <= previousKey || specimen.specimenId.split('/').at(-1) !== specimen.specimenKey) throw new Error('Specimen page keys are duplicate, stalled, or inconsistent with document IDs');
        previousKey = specimen.specimenKey;
        ids.push(specimen.specimenId);
      }
      if (payload.hasMore) {
        if (ids.length === 0 || payload.nextAfterSpecimenKey !== previousKey) throw new Error('Specimen page continuation cursor is missing, stalled, or inconsistent');
        expectedAfterKey = previousKey;
      } else if (payload.nextAfterSpecimenKey !== null) {
        throw new Error('Final specimen page must not advertise another cursor');
      }
      totalSpecimens += ids.length;
      if (!Number.isSafeInteger(totalSpecimens)) throw new Error('Specimen count exceeded the safe integer range');
      currentPage = ids.length === 0 ? null : { ids, hasMore: payload.hasMore };
      previousPageAdvertisedMore = payload.hasMore;
      if (!payload.hasMore) pagesDone = true;
      if (ids.length === 0 && payload.hasMore) throw new Error('Empty specimen page cannot advertise a continuation cursor');
      return this;
    },

    addSpecimenPatientPairs(result) {
      if (finalized || !currentPage) throw new Error('Specimen patient pairs require one unconsumed specimen page');
      const payload = singleDiscoveryPayload(result);
      assertPayloadScope(payload);
      if (JSON.stringify(payload.specimenIds) !== JSON.stringify(currentPage.ids)) throw new Error('Specimen patient-pair query does not match the current page IDs');
      if (payload.overflow === true || payload.truncated === true) throw new Error('Specimen patient-pair result overflowed its fatal row bound');
      if (payload.overflow !== false || payload.truncated !== false || !Array.isArray(payload.rows) || payload.rows.length > maxSpecimenPatientRows) throw new Error('Specimen patient-pair result is malformed or unbounded');
      const roots = new Map(currentPage.ids.map(id => [id, new Set()]));
      for (const row of payload.rows) {
        if (!row || typeof row !== 'object' || !roots.has(row.specimenId)) throw new Error('Specimen patient-pair row is outside the current root page');
        const patients = roots.get(row.specimenId);
        const patientId = row.patientId;
        if (patientId !== null && (typeof patientId !== 'string' || patientId.trim() === '')) throw new Error('Specimen patient-pair patientId must be a document ID or null sentinel');
        const identity = patientId === null ? '\u0000NULL_PATIENT' : patientId;
        if (patients.has(identity)) throw new Error('Specimen patient-pair result contains a duplicate pair or sentinel');
        if (patientId === null && patients.size > 0) throw new Error('First-hop sentinel cannot coexist with a valid Patient for one Specimen');
        if (patientId !== null && patients.has('\u0000NULL_PATIENT')) throw new Error('Valid Patient cannot coexist with a first-hop sentinel for one Specimen');
        patients.add(identity);
      }
      for (const [specimenId, patients] of roots) {
        if (patients.size === 0) throw new Error(`Specimen ${specimenId} is missing its valid Patient pair or PRESERVE_PARENT sentinel`);
      }
      for (const [specimenId, patients] of roots) {
        if (patients.has('\u0000NULL_PATIENT')) {
          emptyFirstHopSpecimenRoots += 1;
          addWeightedRawGroup(rawGroups, {
            patientId: null, firstHopMissing: true, secondHopMissing: false, observationPresent: false,
            conceptPresent: true, conceptType: 'NULL', textPresent: true, textType: 'NULL', text: null,
            quantityPresent: true, quantityType: 'NULL', codePresent: true, codeType: 'NULL', code: null,
            routeRows: 1, actualRouteRows: 0, emptyFirstHopRows: 1, emptySecondHopRows: 0,
            textMissingRows: 0, textNullRows: 1, textStringRows: 0, textOtherRows: 0,
            numericCount: 0, missingValueCount: 0, explicitNullValueCount: 0, terminalNullValueRows: 1,
            nonNumericValueCount: 0, numericSum: 0, numericMax: null,
          }, 1);
        } else {
          for (const identity of patients) {
            const multiplicity = patientMultiplicity.get(identity) ?? 0;
            patientMultiplicity.set(identity, multiplicity + 1);
            distinctSpecimenPatientPairs += 1;
          }
        }
      }
      currentPage = null;
      if (!Number.isSafeInteger(distinctSpecimenPatientPairs) || !Number.isSafeInteger(emptyFirstHopSpecimenRoots)) throw new Error('Specimen pair counts exceeded the safe integer range');
      return this;
    },

    nextPatientBatch(limit = 100) {
      if (finalized || !pagesDone || currentPage) throw new Error('Patient batches begin only after all specimen pages and pairs are consumed');
      if (activePatientBatch) throw new Error('Current patient batch must be consumed before requesting another');
      boundedLimit(limit, 'patient batch limit', { maximum: 100 });
      const ids = [...patientMultiplicity.keys()].filter(id => id > patientBatchCursor).sort().slice(0, limit);
      if (ids.length > 0) activePatientBatch = ids;
      return ids;
    },

    addPatientBatch(result) {
      if (finalized || !activePatientBatch) throw new Error('Patient batch result has no active bounded request');
      const payload = singleDiscoveryPayload(result);
      assertPayloadScope(payload);
      if (JSON.stringify(payload.patientIds) !== JSON.stringify(activePatientBatch)) throw new Error('Patient batch result does not match the requested patient IDs');
      if (payload.overflow === true || payload.truncated === true) throw new Error('Patient batch result overflowed its fatal group bound');
      if (payload.overflow !== false || payload.truncated !== false || !Array.isArray(payload.groups) || payload.groups.length > maxPatientGroups) throw new Error('Patient batch result is malformed or unbounded');
      if (patientObservationGroups + payload.groups.length > maxPatientGroups) throw new Error(`Global patient group count exceeds ${maxPatientGroups}`);
      const allowedPatients = new Set(activePatientBatch);
      const groupsByPatient = new Map(activePatientBatch.map(id => [id, []]));
      const identitiesByPatient = new Map(activePatientBatch.map(id => [id, new Set()]));
      for (const row of payload.groups) {
        validateGroup(row);
        if (!allowedPatients.has(row.patientId)) throw new Error('Patient batch group is outside the requested patient IDs');
        const identities = identitiesByPatient.get(row.patientId);
        const identity = rawGroupIdentity(row);
        if (identities.has(identity)) throw new Error('Patient batch contains duplicate typed groups for one Patient');
        identities.add(identity);
        groupsByPatient.get(row.patientId).push(row);
      }
      for (const patientId of activePatientBatch) {
        const patientGroups = groupsByPatient.get(patientId);
        if (patientGroups.length === 0) throw new Error(`Patient ${patientId} is missing an Observation group or PRESERVE_PARENT sentinel`);
        if (patientGroups.some(group => group.secondHopMissing) && (patientGroups.length !== 1 || patientGroups[0].actualRouteRows !== 0)) throw new Error('Empty-second-hop sentinel cannot coexist with Observation groups');
        const multiplicity = patientMultiplicity.get(patientId);
        if (!Number.isSafeInteger(multiplicity) || multiplicity < 1) throw new Error('Patient batch has no validated Specimen multiplicity');
      }
      for (const patientId of activePatientBatch) {
        const patientGroups = groupsByPatient.get(patientId);
        const multiplicity = patientMultiplicity.get(patientId);
        for (const row of patientGroups) {
          addWeightedRawGroup(rawGroups, row, multiplicity);
          patientObservationGroups += 1;
          uniquePatientObservationPairs += safeCount(row.actualRouteRows, 'patient group actualRouteRows');
          emptySecondHopPatientRows += safeCount(row.emptySecondHopRows, 'patient group emptySecondHopRows') * multiplicity;
        }
        if (patientGroups[0].secondHopMissing) emptySecondHopPatientCount += 1;
      }
      patientBatchCursor = activePatientBatch.at(-1);
      activePatientBatch = null;
      for (const count of [patientObservationGroups, uniquePatientObservationPairs, emptySecondHopPatientRows, emptySecondHopPatientCount]) {
        if (!Number.isSafeInteger(count)) throw new Error('Patient route count exceeded the safe integer range');
      }
      return this;
    },

    finalize() {
      if (finalized) throw new Error('Related quantity discovery accumulator was already finalized');
      if (!pagesDone || currentPage || activePatientBatch) throw new Error('Cannot finalize before all bounded pages and patient batches are consumed');
      if ([...patientMultiplicity.keys()].some(id => id > patientBatchCursor)) throw new Error('Cannot finalize before all distinct Patients are aggregated');
      finalized = true;
      const groups = [...rawGroups.values()].sort((a, b) => rawGroupIdentity(a).localeCompare(rawGroupIdentity(b)));
      const sourceRows = groups.reduce((sum, group) => sum + group.routeRows, 0);
      const actualRouteRows = groups.reduce((sum, group) => sum + group.actualRouteRows, 0);
      const emptyFirstHopRows = groups.reduce((sum, group) => sum + group.emptyFirstHopRows, 0);
      const emptySecondHopRows = groups.reduce((sum, group) => sum + group.emptySecondHopRows, 0);
      const nonNumericValueCount = groups.reduce((sum, group) => sum + group.nonNumericValueCount, 0);
      if (![sourceRows, actualRouteRows, emptyFirstHopRows, emptySecondHopRows, nonNumericValueCount].every(Number.isSafeInteger)) throw new Error('Final route totals exceeded the safe integer range');
      if (emptyFirstHopRows !== emptyFirstHopSpecimenRoots || emptySecondHopRows !== emptySecondHopPatientRows
        || sourceRows !== actualRouteRows + emptyFirstHopRows + emptySecondHopRows) throw new Error('Final related route rows do not reconcile across observations and both PRESERVE_PARENT sentinels');
      const textDomain = new Map();
      const categoryDomain = new Map();
      const pivotCells = new Set();
      const unsupportedText = new Set();
      for (const group of groups) {
        const textType = group.textType === 'MISSING' ? 'NULL' : group.textType;
        const textValue = textType === 'NULL' ? null : group.text;
        const textKey = typeValueIdentity(textType !== 'MISSING', textType, textValue);
        textDomain.set(textKey, { textType, text: textValue });
        if (!['NULL', 'STRING'].includes(textType)) unsupportedText.add(textKey);
        const categoryKey = typeValueIdentity(group.codePresent, group.codeType, group.code);
        categoryDomain.set(categoryKey, { categoryPresent: group.codePresent, categoryType: group.codeType, category: group.code });
        pivotCells.add(JSON.stringify([textKey, categoryKey]));
      }
      const visibleTextDomain = [...textDomain.values()].sort((a, b) => stableJson([a.textType, a.text]).localeCompare(stableJson([b.textType, b.text])));
      const typedCategoryDomain = [...categoryDomain.values()].sort((a, b) => stableJson([a.categoryPresent, a.categoryType, a.category]).localeCompare(stableJson([b.categoryPresent, b.categoryType, b.category])));
      if (typedCategoryDomain.length > maxPivotCategoryDomain) throw new Error(`Related quantity Pivot category domain exceeds ${maxPivotCategoryDomain}`);
      const fullRouteCounts = {
        totalSpecimens: totalSpecimens,
        matchedSpecimens: totalSpecimens - emptyFirstHopSpecimenRoots,
        emptyFirstHopSpecimenRoots,
        distinctSpecimenPatientPairs,
        patientsWithSpecimenRoots: patientMultiplicity.size,
        patientObservationGroups,
        uniquePatientObservationPairs,
        emptySecondHopPatientRows,
        emptySecondHopPatientCount,
        actualRouteRows,
        leftJoinOutputRows: sourceRows,
        nonNumericValueCount,
        rawTextTypedCodeGroupCount: groups.length,
        visibleTextGroupCount: visibleTextDomain.length,
        unsupportedTextGroupCount: unsupportedText.size,
        visiblePivotCellCount: pivotCells.size,
        categoryDomainCount: typedCategoryDomain.length,
      };
      return {
        project: scopeIdentity.project,
        generation: scopeIdentity.generation,
        authScope: scopeIdentity.authScope,
        complete: true,
        sourceRows,
        specimenCount: totalSpecimens,
        emptySpecimenCount: emptyFirstHopSpecimenRoots,
        matchedPatientRows: distinctSpecimenPatientPairs,
        emptyPatientObservationCount: emptySecondHopPatientCount,
        matchedObservationRows: actualRouteRows,
        preservedEmptySpecimenRows: emptyFirstHopRows,
        preservedEmptyPatientRows: emptySecondHopRows,
        fullRouteCounts,
        groups,
        categoryDomain: typedCategoryDomain,
        visibleTextDomain,
        unsupportedTextGroupCount: unsupportedText.size,
      };
    },
  };
}

const categoryIdentityFromStream = (present, type, value) => {
  if (!present && type === 'MISSING' && value === null) return 'MISSING';
  if (present && type === 'NULL' && value === null) return 'NULL';
  if (present && type === 'STRING' && typeof value === 'string') return `STRING:${JSON.stringify(value)}`;
  throw new Error(`Unsupported streamed quantity category ${JSON.stringify({ present, type, value })}`);
};

const displayTextKey = value => {
  if (value !== null && typeof value !== 'string') throw new Error('Visible Pivot group keys must be strings or null');
  return JSON.stringify(value);
};

export function summarizeRelatedQuantityPivotOracleResult(result, expectedScope, { visibleTextKeys } = {}) {
  const payload = Array.isArray(result) && result.length === 1 ? result[0] : result;
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('Streaming related quantity Pivot oracle must return one result object');
  if (!expectedScope || typeof expectedScope !== 'object') throw new Error('Expected related quantity Pivot scope is required');
  if (!Array.isArray(visibleTextKeys) || visibleTextKeys.length === 0 || visibleTextKeys.length > 25) {
    throw new Error('Oracle preview requires the exact non-empty visible UI group keys, capped at 25 rows');
  }
  const requestedTextKeys = visibleTextKeys.map(displayTextKey);
  if (new Set(requestedTextKeys).size !== requestedTextKeys.length) throw new Error('Visible UI group keys must be unique');
  if (payload.project !== expectedScope.project || payload.generation !== expectedScope.dataset_generation) throw new Error('Streaming oracle project/generation does not match the requested scope');
  if (JSON.stringify(payload.authScope) !== JSON.stringify({
    auth_resource_paths_unrestricted: expectedScope.auth_resource_paths_unrestricted,
    auth_resource_paths: expectedScope.auth_resource_paths,
    scope_allowed: expectedScope.scope_allowed,
  })) throw new Error('Streaming oracle authorization scope does not match the requested scope');

  const counts = payload.fullRouteCounts ?? payload.factoredRouteCounts;
  if (!counts || typeof counts !== 'object') throw new Error('Streaming oracle is missing fullRouteCounts');
  const specimenCount = safeCount(counts.totalSpecimens, 'totalSpecimens');
  const emptySpecimenCount = safeCount(counts.emptyFirstHopSpecimenRoots, 'emptyFirstHopSpecimenRoots');
  const emptyPatientObservationCount = safeCount(counts.emptySecondHopPatientRows, 'emptySecondHopPatientRows');
  const matchedObservationRows = safeCount(counts.actualRouteRows, 'actualRouteRows');
  const sourceRows = safeCount(counts.leftJoinOutputRows, 'leftJoinOutputRows');
  const rawGroupCount = safeCount(counts.rawTextTypedCodeGroupCount, 'rawTextTypedCodeGroupCount');
  const visibleTextGroupCount = safeCount(counts.visibleTextGroupCount, 'visibleTextGroupCount');
  const visiblePivotCellCount = safeCount(counts.visiblePivotCellCount, 'visiblePivotCellCount');
  const categoryDomainCount = safeCount(counts.categoryDomainCount, 'categoryDomainCount');
  const fullNonNumericValueCount = safeCount(counts.nonNumericValueCount, 'nonNumericValueCount');
  const bounds = payload.resultBounds ?? counts.resultBounds;
  if (!bounds || bounds.categoryDomainCountFits !== true) {
    throw new Error('Streaming oracle category domain is truncated; exact Pivot outputs cannot be proven');
  }
  if (!Number.isSafeInteger(bounds.maxPreviewRows) || bounds.maxPreviewRows < 1) {
    throw new Error('Streaming oracle is missing its bounded result limit');
  }
  if (visibleTextKeys.length > bounds.maxPreviewRows) throw new Error('Visible UI text-key request exceeds the query preview row limit');
  if (categoryDomainCount > maxPivotCategoryDomain || visibleTextGroupCount > visiblePivotCellCount || visiblePivotCellCount > rawGroupCount
    || sourceRows !== matchedObservationRows + emptySpecimenCount + emptyPatientObservationCount) {
    throw new Error('Streaming oracle full-route counts are internally inconsistent or the category domain exceeds 256 categories');
  }
  const selectedKeys = payload.selectedTextGroupKeys;
  if (!Array.isArray(selectedKeys) || JSON.stringify(selectedKeys.map(displayTextKey)) !== JSON.stringify(requestedTextKeys)) {
    throw new Error('Streaming oracle did not bind the exact visible UI group keys');
  }
  if (payload.selectedTextKeysUnique !== true || payload.selectedTextKeysWithinLimit !== true || payload.selectedTextKeysValid !== true) {
    throw new Error('Streaming oracle rejected the visible UI group-key request');
  }
  if (safeCount(payload.selectedTextGroupCount, 'selectedTextGroupCount') !== visibleTextKeys.length
    || safeCount(payload.selectedMatchedTextGroupCount, 'selectedMatchedTextGroupCount') !== visibleTextKeys.length) {
    throw new Error('Streaming oracle did not find every visible UI group key in the full route domain');
  }
  for (const [field, length, expected] of [
    ['rawTypedTextCodePreview', payload.rawTypedTextCodePreview?.length, payload.selectedRawGroupCount],
    ['visiblePivotCellPreview', payload.visiblePivotCellPreview?.length, payload.selectedPivotCellCount],
    ['categoryDomainPreview', payload.categoryDomainPreview?.length, categoryDomainCount],
  ]) {
    const expectedCount = field === 'categoryDomainPreview' ? categoryDomainCount : safeCount(expected, field === 'rawTypedTextCodePreview' ? 'selectedRawGroupCount' : 'selectedPivotCellCount');
    if (!Number.isSafeInteger(length) || length !== expectedCount || (field === 'categoryDomainPreview' && expectedCount > maxPivotCategoryDomain)) {
      throw new Error(`Streaming oracle ${field} is not complete for its bounded request`);
    }
  }

  const groupsByIdentity = new Map();
  for (const raw of payload.rawTypedTextCodePreview) {
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('Streaming raw group must be an object');
    const textType = raw.textType;
    if (!['MISSING', 'NULL', 'STRING'].includes(textType)) throw new Error(`Unsupported related group text type ${JSON.stringify(textType)}`);
    const groupTextPresent = raw.textPresent === true;
    if (typeof raw.textPresent !== 'boolean' || groupTextPresent !== (textType !== 'MISSING')) throw new Error('Streamed group text type/presence mismatch');
    if ((textType === 'MISSING' || textType === 'NULL') && raw.text !== null) throw new Error('Missing/null group text must project to null');
    if (textType === 'STRING' && typeof raw.text !== 'string') throw new Error('String group text must retain its exact value');
    const category = categoryIdentityFromStream(raw.codePresent, raw.codeType, raw.code);
    const visibleTextValue = groupTextPresent ? raw.text : null;
    if (!requestedTextKeys.includes(displayTextKey(visibleTextValue))) throw new Error('Streaming raw group is outside the exact visible UI group-key request');
    const identity = JSON.stringify([groupTextPresent, raw.text, category]);
    let group = groupsByIdentity.get(identity);
    if (!group) {
      group = {
        groupTextPresent,
        groupText: raw.text,
        categoryPresent: raw.codePresent,
        category: raw.code,
        sourceRows: 0,
        missingTextRows: 0,
        explicitNullTextRows: 0,
        missingCodeRows: 0,
        explicitNullCodeRows: 0,
        missingValueRows: 0,
        explicitNullValueRows: 0,
        terminalNullValueRows: 0,
        numericRows: 0,
        nonNumericValueRows: 0,
        valueSum: 0,
        valueMax: null,
      };
      groupsByIdentity.set(identity, group);
    }
    const routeRows = safeCount(raw.routeRows, 'raw routeRows');
    group.sourceRows += routeRows;
    group.missingTextRows += safeCount(raw.textMissingRows, 'raw textMissingRows');
    group.explicitNullTextRows += safeCount(raw.textNullRows, 'raw textNullRows');
    group.missingCodeRows += raw.codePresent ? 0 : routeRows;
    group.explicitNullCodeRows += raw.codePresent && raw.codeType === 'NULL' ? routeRows : 0;
    group.missingValueRows += safeCount(raw.missingValueCount, 'raw missingValueCount');
    group.explicitNullValueRows += safeCount(raw.explicitNullValueCount, 'raw explicitNullValueCount');
    group.terminalNullValueRows += safeCount(raw.terminalNullValueRows, 'raw terminalNullValueRows');
    group.numericRows += safeCount(raw.numericCount, 'raw numericCount');
    group.nonNumericValueRows += safeCount(raw.nonNumericValueCount, 'raw nonNumericValueCount');
    if (typeof raw.numericSum !== 'number' || !Number.isFinite(raw.numericSum)) throw new Error('Streamed numericSum must be finite');
    group.valueSum += raw.numericSum;
    if (raw.numericMax !== null && (typeof raw.numericMax !== 'number' || !Number.isFinite(raw.numericMax))) throw new Error('Streamed numericMax must be finite or null');
    if (raw.numericMax !== null) group.valueMax = group.valueMax === null ? raw.numericMax : Math.max(group.valueMax, raw.numericMax);
  }

  const groups = [...groupsByIdentity.values()];
  const sampleSourceRows = groups.reduce((total, group) => total + group.sourceRows, 0);
  const oracle = {
    specimenCount,
    emptySpecimenCount,
    emptyPatientObservationCount,
    matchedObservationRows,
    preservedEmptySpecimenRows: emptySpecimenCount,
    preservedEmptyPatientRows: emptyPatientObservationCount,
    sourceRows,
    sampleSourceRows,
    groups,
    complete: true,
    previewTextGroupKeys: [...visibleTextKeys],
    categoryDomain: payload.categoryDomainPreview.map(category => ({
      key: !category.categoryPresent ? { kind: 'MISSING' }
        : category.categoryType === 'NULL' ? { kind: 'NULL' }
          : category.categoryType === 'STRING' ? { kind: 'STRING', string: category.category }
            : null,
    })),
    fullRouteCounts: counts,
  };
  if (oracle.categoryDomain.some(entry => entry.key === null)) throw new Error('Streaming category domain contains an unsupported typed key');
  validateRelatedQuantityPivotOracle(oracle);
  const visibleTexts = new Set(groups.map(group => displayTextKey(group.groupTextPresent ? group.groupText : null)));
  if (visibleTexts.size !== visibleTextKeys.length || requestedTextKeys.some(identity => !visibleTexts.has(identity))) {
    throw new Error('Selected raw route groups do not completely cover the exact visible UI group keys');
  }
  const categoryPreview = new Set(payload.categoryDomainPreview.map(category => categoryIdentityFromStream(category.categoryPresent, category.categoryType, category.category)));
  const categories = new Set(oracle.categoryDomain.map(({ key }) => categoryIdentity(key)));
  if (categoryPreview.size !== categories.size || [...categories].some(identity => !categoryPreview.has(identity))) throw new Error('Streaming category preview does not match the full raw category domain');
  const sampleCells = new Map();
  for (const cell of payload.visiblePivotCellPreview) {
    const category = categoryIdentityFromStream(cell.categoryPresent, cell.categoryType, cell.category);
    const identity = JSON.stringify([cell.text, category]);
    if (sampleCells.has(identity)) throw new Error('Streaming Pivot preview contains duplicate visible text/category cells');
    sampleCells.set(identity, cell);
  }
  const derivedCells = new Map();
  for (const text of coalescedPivotGroups(oracle)) {
    for (const [category, group] of text.groups) derivedCells.set(JSON.stringify([text.value, category]), group);
  }
  if (sampleCells.size !== derivedCells.size) throw new Error('Streaming Pivot preview does not contain every selected text/category cell');
  for (const [identity, group] of derivedCells) {
    const cell = sampleCells.get(identity);
    if (!cell || cell.routeRows !== group.sourceRows || cell.numericCount !== group.numericRows
      || cell.sum !== (group.numericRows ? group.valueSum : null) || cell.max !== group.valueMax
      || cell.missingValueCount !== group.missingValueRows || cell.explicitNullValueCount !== group.explicitNullValueRows
      || cell.terminalNullValueRows !== group.terminalNullValueRows || cell.nonNumericValueCount !== group.nonNumericValueRows) {
      throw new Error('Streaming Pivot cell does not match the exact selected raw route aggregate');
    }
  }
  if (safeCount(counts.nonNumericValueCount, 'nonNumericValueCount') !== fullNonNumericValueCount) throw new Error('Streaming oracle nonnumeric-value count is inconsistent');
  const dWitnesses = quantityDuplicateWitnesses(oracle).filter(({ key }) => key === 'STRING:"d"');
  const dDuplicateWitnessBucketCount = safeCount(payload.dDuplicateWitnessBucketCount, 'dDuplicateWitnessBucketCount');
  if (!Array.isArray(payload.dDuplicateWitnesses) || payload.dDuplicateWitnesses.length !== Math.min(dDuplicateWitnessBucketCount, 5)) throw new Error('Streaming d duplicate-witness preview is incomplete');
  if (dDuplicateWitnessBucketCount > visiblePivotCellCount || dWitnesses.length > dDuplicateWitnessBucketCount) throw new Error('d duplicate-witness count exceeds the full visible cell count or selected sample');
  const globalWitnesses = payload.dDuplicateWitnesses.map(witness => {
    if (!witness || typeof witness !== 'object' || (witness.text !== null && typeof witness.text !== 'string')
      || witness.categoryPresent !== true || witness.categoryType !== 'STRING' || witness.category !== 'd'
      || safeCount(witness.routeRows, 'd witness routeRows') < 2 || safeCount(witness.numericCount, 'd witness numericCount') < 2
      || typeof witness.sum !== 'number' || !Number.isFinite(witness.sum) || typeof witness.max !== 'number' || !Number.isFinite(witness.max)
      || witness.sum === witness.max) throw new Error('Streaming d witness is not a positive typed SUM/MAX difference');
    return { ...witness };
  });
  for (const witness of globalWitnesses) {
    if (!requestedTextKeys.includes(displayTextKey(witness.text))) continue;
    const selectedGroup = coalescedPivotGroups(oracle).find(text => displayTextKey(text.value) === displayTextKey(witness.text))?.groups.get('STRING:"d"');
    if (!selectedGroup || witness.routeRows !== selectedGroup.sourceRows || witness.numericCount !== selectedGroup.numericRows
      || witness.sum !== selectedGroup.valueSum || witness.max !== selectedGroup.valueMax) {
      throw new Error('Visible d duplicate witness does not match its selected raw route aggregate');
    }
  }
  oracle.fullGroupCount = visibleTextGroupCount;
  oracle.fullCellCount = visiblePivotCellCount;
  oracle.fullCategoryCount = categoryDomainCount;
  oracle.dDuplicateWitnessBucketCount = dDuplicateWitnessBucketCount;
  oracle.dDuplicateWitnesses = globalWitnesses;
  oracle.fullNonNumericValueCount = fullNonNumericValueCount;
  return oracle;
}
const categoryIdentity = category => {
  if (!category || typeof category !== 'object') throw new Error('Pivot category must be a typed key');
  if (category.kind === 'MISSING') return 'MISSING';
  if (category.kind === 'NULL') return 'NULL';
  if (category.kind === 'STRING' && typeof category.string === 'string') return `STRING:${JSON.stringify(category.string)}`;
  throw new Error(`Unsupported typed quantity code category ${JSON.stringify(category)}`);
};

const textIdentity = group => JSON.stringify([group.groupTextPresent === true, group.groupTextPresent === true ? group.groupText : null]);

const coalescedPivotGroups = oracle => {
  const texts = new Map();
  for (const group of oracle.groups) {
    const textValue = group.groupTextPresent ? group.groupText : null;
    const textKey = JSON.stringify(textValue);
    let text = texts.get(textKey);
    if (!text) {
      text = { value: textValue, groups: new Map() };
      texts.set(textKey, text);
    }
    const key = !group.categoryPresent ? 'MISSING' : group.category === null ? 'NULL' : `STRING:${JSON.stringify(group.category)}`;
    const current = text.groups.get(key);
    if (!current) text.groups.set(key, { ...group });
    else {
      for (const field of ['sourceRows', 'missingTextRows', 'explicitNullTextRows', 'missingCodeRows', 'explicitNullCodeRows', 'missingValueRows', 'explicitNullValueRows', 'terminalNullValueRows', 'numericRows', 'nonNumericValueRows']) {
        current[field] += group[field];
      }
      current.valueSum += group.valueSum;
      if (group.valueMax !== null) current.valueMax = current.valueMax === null ? group.valueMax : Math.max(current.valueMax, group.valueMax);
    }
  }
  const groups = [...texts.values()];
  if (!Array.isArray(oracle.previewTextGroupKeys)) return groups;
  const byValue = new Map(groups.map(text => [JSON.stringify(text.value), text]));
  return oracle.previewTextGroupKeys.map(value => byValue.get(JSON.stringify(value)));
};

export function validateRelatedQuantityPivotOracle(oracle) {
  if (!oracle || typeof oracle !== 'object' || !Array.isArray(oracle.groups)) throw new Error('Related quantity Pivot oracle must contain aggregate groups');
  if (!Number.isSafeInteger(oracle.sourceRows) || oracle.sourceRows < 0) throw new Error('Related quantity Pivot oracle sourceRows must be a non-negative safe integer');
  for (const field of ['specimenCount', 'emptySpecimenCount', 'emptyPatientObservationCount', 'matchedObservationRows', 'preservedEmptySpecimenRows', 'preservedEmptyPatientRows']) {
    if (!Number.isSafeInteger(oracle[field]) || oracle[field] < 0) throw new Error(`Related route summary ${field} must be a non-negative safe integer`);
  }
  if (Object.hasOwn(oracle, 'matchedPatientRows') && (!Number.isSafeInteger(oracle.matchedPatientRows) || oracle.matchedPatientRows < 0)) throw new Error('Related route summary matchedPatientRows must be a non-negative safe integer');
  if (oracle.emptySpecimenCount > oracle.specimenCount || (Object.hasOwn(oracle, 'matchedPatientRows') && oracle.emptyPatientObservationCount > oracle.matchedPatientRows)
    || oracle.preservedEmptySpecimenRows > oracle.emptySpecimenCount || oracle.preservedEmptyPatientRows > oracle.emptyPatientObservationCount) {
    throw new Error('Related route empty-boundary counts exceed their parent populations');
  }
  if (oracle.sourceRows !== oracle.matchedObservationRows + oracle.preservedEmptySpecimenRows + oracle.preservedEmptyPatientRows) {
    throw new Error('Related route rows must account for matched observations and preserved empty parents');
  }
  let countedRows = 0;
  const groups = new Set();
  for (const group of oracle.groups) {
    if (typeof group.groupTextPresent !== 'boolean') throw new Error('Aggregate group must preserve text presence');
    if (group.groupTextPresent && group.groupText !== null && typeof group.groupText !== 'string') throw new Error('Present Observation text must be string or null');
    if (typeof group.categoryPresent !== 'boolean') throw new Error('Aggregate group must preserve quantity code presence');
    if (!group.categoryPresent && group.category !== null) throw new Error('Missing quantity code category must project null with presence=false');
    if (group.categoryPresent && group.category !== null && typeof group.category !== 'string') throw new Error('Present quantity code category must be string or null');
    for (const field of ['sourceRows', 'missingTextRows', 'explicitNullTextRows', 'missingCodeRows', 'explicitNullCodeRows', 'missingValueRows', 'explicitNullValueRows', 'terminalNullValueRows', 'numericRows', 'nonNumericValueRows']) {
      if (!Number.isSafeInteger(group[field]) || group[field] < 0) throw new Error(`Aggregate ${field} must be a non-negative safe integer`);
    }
    if (group.sourceRows === 0 || group.numericRows + group.nonNumericValueRows + group.missingValueRows + group.explicitNullValueRows + group.terminalNullValueRows !== group.sourceRows) throw new Error('Aggregate quantity counts must account for every route row');
    if (group.missingTextRows + group.explicitNullTextRows > group.sourceRows) throw new Error('Aggregate text presence counts exceed route row count');
    if (group.missingCodeRows + group.explicitNullCodeRows > group.sourceRows) throw new Error('Aggregate code presence counts exceed route row count');
    if ((!group.categoryPresent && (group.missingCodeRows !== group.sourceRows || group.explicitNullCodeRows !== 0))
      || (group.categoryPresent && group.category === null && (group.explicitNullCodeRows !== group.sourceRows || group.missingCodeRows !== 0))
      || (group.categoryPresent && typeof group.category === 'string' && (group.missingCodeRows !== 0 || group.explicitNullCodeRows !== 0))) {
      throw new Error('Aggregate typed category identity must match its missing/null presence counts');
    }
    if ((!group.groupTextPresent && (group.groupText !== null || group.missingTextRows !== group.sourceRows || group.explicitNullTextRows !== 0))
      || (group.groupTextPresent && group.groupText === null && !Array.isArray(oracle.previewTextGroupKeys) && group.explicitNullTextRows !== group.sourceRows)) {
      throw new Error('Aggregate group text identity must match its missing/null presence counts');
    }
    if (group.numericRows === 0) {
      if (group.valueSum !== 0 || group.valueMax !== null) throw new Error('Non-numeric groups must have zero sum and null maximum');
    } else if (typeof group.valueSum !== 'number' || !Number.isFinite(group.valueSum) || typeof group.valueMax !== 'number' || !Number.isFinite(group.valueMax)) {
      throw new Error('Numeric groups must retain finite SUM and MAX values');
    }
    const identity = JSON.stringify([textIdentity(group), group.categoryPresent, group.category]);
    if (groups.has(identity)) throw new Error('Aggregate oracle contains duplicate typed text/category groups');
    groups.add(identity);
    countedRows += group.sourceRows;
  }
  if (Array.isArray(oracle.previewTextGroupKeys)) {
    if (!Number.isSafeInteger(oracle.sampleSourceRows) || oracle.sampleSourceRows < 0 || oracle.sampleSourceRows > oracle.sourceRows) {
      throw new Error('Selected text-key route row count must be within the full related-route population');
    }
    if (countedRows !== oracle.sampleSourceRows) throw new Error('Selected raw groups do not account for their complete route multiplicity');
    const groupKeys = new Set(oracle.groups.map(group => JSON.stringify(group.groupTextPresent ? group.groupText : null)));
    const requestedKeys = new Set(oracle.previewTextGroupKeys.map(displayTextKey));
    if (groupKeys.size !== requestedKeys.size || [...requestedKeys].some(key => !groupKeys.has(key))) {
      throw new Error('Selected raw groups do not match the exact UI text-key set');
    }
  } else if (countedRows !== oracle.sourceRows) {
    throw new Error('Aggregate groups do not account for full related-route multiplicity');
  }
  return oracle;
}

export function summarizeRelatedQuantityPivotDiscoveryResult(summary, expectedScope, { visibleTextKeys } = {}) {
  const scopeIdentity = discoveryAuthScope(expectedScope);
  if (!summary || typeof summary !== 'object' || Array.isArray(summary) || summary.complete !== true) throw new Error('Related quantity Pivot discovery summary must be complete');
  if (!scopeEchoMatches(summary, scopeIdentity)) throw new Error('Related quantity Pivot discovery scope identity does not match the requested project, generation, and authorization scope');
  if (!Array.isArray(visibleTextKeys) || visibleTextKeys.length === 0 || visibleTextKeys.length > 25
    || visibleTextKeys.some(value => value !== null && typeof value !== 'string')) throw new Error('Discovery adapter requires the exact non-empty visible UI text keys, capped at 25');
  const requestedIdentities = visibleTextKeys.map(displayTextKey);
  if (new Set(requestedIdentities).size !== requestedIdentities.length) throw new Error('Visible UI text keys must be unique');
  if (!Array.isArray(summary.groups) || !Array.isArray(summary.visibleTextDomain) || !Array.isArray(summary.categoryDomain)
    || !summary.fullRouteCounts || typeof summary.fullRouteCounts !== 'object') throw new Error('Complete discovery summary is missing its typed groups or full domains');

  const textDomain = new Map();
  for (const entry of summary.visibleTextDomain) {
    if (!entry || typeof entry !== 'object' || Array.isArray(entry) || typeof entry.textType !== 'string') throw new Error('Discovery visible text domain entry is malformed');
    const type = entry.textType;
    const value = entry.text;
    if (!['NULL', 'STRING', 'OBJECT', 'ARRAY', 'NUMBER', 'BOOL', 'OTHER'].includes(type)
      || (type === 'NULL' && value !== null) || (type === 'STRING' && typeof value !== 'string')
      || (type === 'OBJECT' && (!value || typeof value !== 'object' || Array.isArray(value)))
      || (type === 'ARRAY' && !Array.isArray(value)) || (type === 'NUMBER' && (typeof value !== 'number' || !Number.isFinite(value)))
      || (type === 'BOOL' && typeof value !== 'boolean')) throw new Error('Discovery visible text domain value does not match its AQL type');
    const identity = typeValueIdentity(true, type, value);
    if (textDomain.has(identity)) throw new Error('Discovery visible text domain contains duplicate typed keys');
    textDomain.set(identity, { textType: type, text: value });
  }
  for (const key of visibleTextKeys) {
    const type = key === null ? 'NULL' : 'STRING';
    if (!textDomain.has(typeValueIdentity(true, type, key))) throw new Error(`Preview text key ${JSON.stringify(key)} is absent from the complete independent discovery domain`);
  }

  const categoryDomain = new Map();
  for (const entry of summary.categoryDomain) {
    if (!entry || typeof entry !== 'object' || Array.isArray(entry)) throw new Error('Discovery category domain entry is malformed');
    validateTypedField({ codePresent: entry.categoryPresent, codeType: entry.categoryType, code: entry.category }, 'code');
    const identity = typeValueIdentity(entry.categoryPresent, entry.categoryType, entry.category);
    if (categoryDomain.has(identity)) throw new Error('Discovery category domain contains duplicate typed keys');
    categoryDomain.set(identity, { categoryPresent: entry.categoryPresent, categoryType: entry.categoryType, category: entry.category });
  }
  const legacyCategoryDomain = [...categoryDomain.values()].map(entry => {
    if (!entry.categoryPresent && entry.categoryType === 'MISSING') return { key: { kind: 'MISSING' } };
    if (entry.categoryPresent && entry.categoryType === 'NULL') return { key: { kind: 'NULL' } };
    if (entry.categoryPresent && entry.categoryType === 'STRING') return { key: { kind: 'STRING', string: entry.category } };
    throw new Error(`Unsupported typed quantity category prevents a complete Pivot oracle: ${JSON.stringify(entry)}`);
  });

  const groupIdentities = new Set();
  const derivedTextDomain = new Map();
  const derivedCategoryDomain = new Map();
  const derivedCells = new Set();
  let allSourceRows = 0;
  let allActualRows = 0;
  let allEmptyFirstRows = 0;
  let allEmptySecondRows = 0;
  let allNonNumericRows = 0;
  for (const row of summary.groups) {
    if (!row || typeof row !== 'object' || Array.isArray(row)) throw new Error('Discovery raw group must be an object');
    validateTypedField(row, 'text');
    validateTypedField(row, 'code');
    if (typeof row.firstHopMissing !== 'boolean' || typeof row.secondHopMissing !== 'boolean' || typeof row.observationPresent !== 'boolean') throw new Error('Discovery raw group must preserve both route-boundary presence bits');
    const routeRows = safeCount(row.routeRows, 'discovery routeRows');
    const actualRows = safeCount(row.actualRouteRows, 'discovery actualRouteRows');
    const firstRows = safeCount(row.emptyFirstHopRows, 'discovery emptyFirstHopRows');
    const secondRows = safeCount(row.emptySecondHopRows, 'discovery emptySecondHopRows');
    const nonNumericRows = safeCount(row.nonNumericValueCount, 'discovery nonNumericValueCount');
    if (routeRows === 0 || routeRows !== actualRows + firstRows + secondRows
      || (row.firstHopMissing && (actualRows !== 0 || firstRows !== routeRows || secondRows !== 0))
      || (row.secondHopMissing && (actualRows !== 0 || firstRows !== 0 || secondRows !== routeRows))
      || (!row.firstHopMissing && !row.secondHopMissing && (actualRows !== routeRows || firstRows !== 0 || secondRows !== 0))) throw new Error('Discovery raw group route counters do not match its sentinel identity');
    const textRowCount = safeCount(row.textMissingRows, 'discovery textMissingRows') + safeCount(row.textNullRows, 'discovery textNullRows')
      + safeCount(row.textStringRows, 'discovery textStringRows') + safeCount(row.textOtherRows, 'discovery textOtherRows');
    const valueRowCount = safeCount(row.numericCount, 'discovery numericCount') + safeCount(row.missingValueCount, 'discovery missingValueCount')
      + safeCount(row.explicitNullValueCount, 'discovery explicitNullValueCount') + safeCount(row.terminalNullValueRows, 'discovery terminalNullValueRows') + nonNumericRows;
    if (textRowCount !== routeRows || valueRowCount !== routeRows || row.terminalNullValueRows !== firstRows + secondRows) throw new Error('Discovery raw group text/value counters do not reconcile to its route rows');
    const expectedTextField = row.textType === 'MISSING' ? 'textMissingRows' : row.textType === 'NULL' ? 'textNullRows'
      : row.textType === 'STRING' ? 'textStringRows' : 'textOtherRows';
    if (row[expectedTextField] !== routeRows || ['textMissingRows', 'textNullRows', 'textStringRows', 'textOtherRows']
      .some(field => field !== expectedTextField && row[field] !== 0)) throw new Error('Discovery raw group text counters do not match its typed key');
    if (typeof row.numericSum !== 'number' || !Number.isFinite(row.numericSum)
      || (row.numericMax !== null && (typeof row.numericMax !== 'number' || !Number.isFinite(row.numericMax)))
      || (row.numericCount === 0 && (row.numericSum !== 0 || row.numericMax !== null))
      || (row.numericCount > 0 && row.numericMax === null)) throw new Error('Discovery raw group numeric aggregates are inconsistent');
    const rawIdentity = rawGroupIdentity(row);
    if (groupIdentities.has(rawIdentity)) throw new Error('Discovery summary contains duplicate raw typed groups');
    groupIdentities.add(rawIdentity);
    allSourceRows += routeRows;
    allActualRows += actualRows;
    allEmptyFirstRows += firstRows;
    allEmptySecondRows += secondRows;
    allNonNumericRows += nonNumericRows;
    const visibleType = row.textType === 'MISSING' ? 'NULL' : row.textType;
    const visibleValue = visibleType === 'NULL' ? null : row.text;
    const visibleIdentity = typeValueIdentity(true, visibleType, visibleValue);
    derivedTextDomain.set(visibleIdentity, { textType: visibleType, text: visibleValue });
    const categoryIdentity = typeValueIdentity(row.codePresent, row.codeType, row.code);
    derivedCategoryDomain.set(categoryIdentity, { categoryPresent: row.codePresent, categoryType: row.codeType, category: row.code });
    derivedCells.add(JSON.stringify([visibleIdentity, categoryIdentity]));
  }
  const counts = summary.fullRouteCounts;
  const expectedFull = {
    totalSpecimens: safeCount(summary.specimenCount, 'discovery specimenCount'),
    emptyFirstHopSpecimenRoots: safeCount(summary.emptySpecimenCount, 'discovery emptySpecimenCount'),
    distinctSpecimenPatientPairs: safeCount(summary.matchedPatientRows, 'discovery matchedPatientRows'),
    emptySecondHopPatientRows: safeCount(summary.preservedEmptyPatientRows, 'discovery preservedEmptyPatientRows'),
    actualRouteRows: safeCount(summary.matchedObservationRows, 'discovery matchedObservationRows'),
    leftJoinOutputRows: safeCount(summary.sourceRows, 'discovery sourceRows'),
    rawTextTypedCodeGroupCount: summary.groups.length,
    visibleTextGroupCount: derivedTextDomain.size,
    visiblePivotCellCount: derivedCells.size,
    categoryDomainCount: derivedCategoryDomain.size,
    nonNumericValueCount: allNonNumericRows,
  };
  for (const [field, expected] of Object.entries(expectedFull)) {
    if (safeCount(counts[field], `fullRouteCounts.${field}`) !== expected) throw new Error(`Discovery summary ${field} does not reconcile to its complete typed groups`);
  }
  if (safeCount(summary.preservedEmptySpecimenRows, 'discovery preservedEmptySpecimenRows') !== allEmptyFirstRows
    || safeCount(counts.emptyFirstHopSpecimenRoots, 'fullRouteCounts.emptyFirstHopSpecimenRoots') !== allEmptyFirstRows
    || safeCount(counts.emptySecondHopPatientRows, 'fullRouteCounts.emptySecondHopPatientRows') !== allEmptySecondRows
    || safeCount(summary.preservedEmptyPatientRows, 'discovery preservedEmptyPatientRows') !== allEmptySecondRows
    || allSourceRows !== safeCount(summary.sourceRows, 'discovery sourceRows')
    || allActualRows !== safeCount(summary.matchedObservationRows, 'discovery matchedObservationRows')) throw new Error('Discovery summary full-route totals do not reconcile');
  const derivedUnsupportedTextCount = [...derivedTextDomain.values()].filter(entry => !['NULL', 'STRING'].includes(entry.textType)).length;
  if (safeCount(summary.unsupportedTextGroupCount, 'discovery unsupportedTextGroupCount') !== derivedUnsupportedTextCount
    || safeCount(counts.unsupportedTextGroupCount, 'fullRouteCounts.unsupportedTextGroupCount') !== derivedUnsupportedTextCount
    || textDomain.size !== derivedTextDomain.size
    || [...derivedTextDomain.keys()].some(identity => !textDomain.has(identity))
    || categoryDomain.size !== derivedCategoryDomain.size
    || [...derivedCategoryDomain.keys()].some(identity => !categoryDomain.has(identity))) throw new Error('Discovery typed text/category domains do not match the complete raw groups');

  const selectedIdentity = new Set(requestedIdentities);
  const selectedGroupsByIdentity = new Map();
  for (const row of summary.groups) {
    const key = row.textType === 'MISSING' || row.textType === 'NULL' ? null : row.textType === 'STRING' ? row.text : undefined;
    if (key === undefined || !selectedIdentity.has(displayTextKey(key))) continue;
    const categoryKey = categoryIdentityFromStream(row.codePresent, row.codeType, row.code);
    const identity = JSON.stringify([row.textPresent, row.text, categoryKey]);
    let group = selectedGroupsByIdentity.get(identity);
    if (!group) {
      group = {
        groupTextPresent: row.textPresent,
        groupText: row.text,
        categoryPresent: row.codePresent,
        category: row.codeType === 'STRING' ? row.code : null,
        sourceRows: 0,
        missingTextRows: 0,
        explicitNullTextRows: 0,
        missingCodeRows: 0,
        explicitNullCodeRows: 0,
        missingValueRows: 0,
        explicitNullValueRows: 0,
        terminalNullValueRows: 0,
        numericRows: 0,
        nonNumericValueRows: 0,
        valueSum: 0,
        valueMax: null,
      };
      selectedGroupsByIdentity.set(identity, group);
    }
    group.sourceRows += safeCount(row.routeRows, 'selected discovery routeRows');
    group.missingTextRows += safeCount(row.textMissingRows, 'selected discovery textMissingRows');
    group.explicitNullTextRows += safeCount(row.textNullRows, 'selected discovery textNullRows');
    group.missingCodeRows += row.codePresent ? 0 : safeCount(row.routeRows, 'selected discovery code routeRows');
    group.explicitNullCodeRows += row.codePresent && row.codeType === 'NULL' ? safeCount(row.routeRows, 'selected discovery code routeRows') : 0;
    group.missingValueRows += safeCount(row.missingValueCount, 'selected discovery missingValueCount');
    group.explicitNullValueRows += safeCount(row.explicitNullValueCount, 'selected discovery explicitNullValueCount');
    group.terminalNullValueRows += safeCount(row.terminalNullValueRows, 'selected discovery terminalNullValueRows');
    group.numericRows += safeCount(row.numericCount, 'selected discovery numericCount');
    group.nonNumericValueRows += safeCount(row.nonNumericValueCount, 'selected discovery nonNumericValueCount');
    if (typeof row.numericSum !== 'number' || !Number.isFinite(row.numericSum)) throw new Error('Selected discovery numericSum must be finite');
    group.valueSum += row.numericSum;
    if (!Number.isFinite(group.valueSum)) throw new Error('Selected discovery numericSum exceeded the finite range');
    if (row.numericMax !== null) {
      if (typeof row.numericMax !== 'number' || !Number.isFinite(row.numericMax)) throw new Error('Selected discovery numericMax must be finite or null');
      group.valueMax = group.valueMax === null ? row.numericMax : Math.max(group.valueMax, row.numericMax);
    }
  }
  const selectedGroups = [...selectedGroupsByIdentity.values()];
  const sampleSourceRows = selectedGroups.reduce((sum, group) => sum + group.sourceRows, 0);
  const fullNonNumericValueCount = safeCount(counts.nonNumericValueCount, 'fullRouteCounts.nonNumericValueCount');
  const selectedCellsByIdentity = new Map();
  for (const row of summary.groups) {
    if (!['MISSING', 'NULL', 'STRING'].includes(row.textType)) continue;
    const textType = row.textType === 'MISSING' ? 'NULL' : row.textType;
    const text = textType === 'NULL' ? null : row.text;
    if (!selectedIdentity.has(displayTextKey(text))) continue;
    const categoryKey = categoryIdentityFromStream(row.codePresent, row.codeType, row.code);
    const identity = JSON.stringify([textType, text, categoryKey]);
    let cell = selectedCellsByIdentity.get(identity);
    if (!cell) {
      cell = {
        text,
        categoryPresent: row.codePresent,
        categoryType: row.codeType,
        category: row.code,
        routeRows: 0,
        actualRouteRows: 0,
        emptyFirstHopRows: 0,
        emptySecondHopRows: 0,
        textMissingRows: 0,
        textNullRows: 0,
        textStringRows: 0,
        textOtherRows: 0,
        numericCount: 0,
        missingValueCount: 0,
        explicitNullValueCount: 0,
        terminalNullValueRows: 0,
        nonNumericValueCount: 0,
        numericSum: 0,
        numericMax: null,
      };
      selectedCellsByIdentity.set(identity, cell);
    }
    for (const field of ['routeRows', 'actualRouteRows', 'emptyFirstHopRows', 'emptySecondHopRows', 'textMissingRows', 'textNullRows', 'textStringRows', 'textOtherRows', 'numericCount', 'missingValueCount', 'explicitNullValueCount', 'terminalNullValueRows', 'nonNumericValueCount']) {
      cell[field] += safeCount(row[field], `selected cell ${field}`);
    }
    cell.numericSum += row.numericSum;
    if (row.numericMax !== null) cell.numericMax = cell.numericMax === null ? row.numericMax : Math.max(cell.numericMax, row.numericMax);
  }
  const selectedCells = [...selectedCellsByIdentity.values()].sort((left, right) => stableJson([left.text, left.categoryType, left.category]).localeCompare(stableJson([right.text, right.categoryType, right.category])))
    .map(cell => ({ ...cell, sum: cell.numericCount === 0 ? null : cell.numericSum, max: cell.numericCount === 0 ? null : cell.numericMax }));
  const oracle = {
    specimenCount: summary.specimenCount,
    emptySpecimenCount: summary.emptySpecimenCount,
    matchedPatientRows: summary.matchedPatientRows,
    emptyPatientObservationCount: summary.emptyPatientObservationCount,
    matchedObservationRows: summary.matchedObservationRows,
    preservedEmptySpecimenRows: summary.preservedEmptySpecimenRows,
    preservedEmptyPatientRows: summary.preservedEmptyPatientRows,
    sourceRows: summary.sourceRows,
    sampleSourceRows,
    groups: selectedGroups,
    complete: true,
    previewTextGroupKeys: [...visibleTextKeys],
    categoryDomain: legacyCategoryDomain,
    fullRouteCounts: counts,
    fullGroupCount: counts.visibleTextGroupCount,
    fullCellCount: counts.visiblePivotCellCount,
    fullCategoryCount: counts.categoryDomainCount,
    fullNonNumericValueCount,
    selectedTextGroupCount: visibleTextKeys.length,
    selectedMatchedTextGroupCount: visibleTextKeys.length,
    selectedTextKeysUnique: true,
    selectedTextKeysWithinLimit: visibleTextKeys.length <= 25,
    selectedTextKeysValid: true,
    selectedRawGroupCount: selectedGroups.length,
    selectedPivotCellCount: selectedCells.length,
    selectedCells,
    resultBounds: { maxPreviewRows: 25, categoryDomainCountFits: true },
  };
  const dCells = new Map();
  for (const row of summary.groups) {
    if (!(row.codePresent && row.codeType === 'STRING' && row.code === 'd')
      || !['MISSING', 'NULL', 'STRING'].includes(row.textType)) continue;
    const textType = row.textType === 'MISSING' ? 'NULL' : row.textType;
    const text = textType === 'NULL' ? null : row.text;
    if (!selectedIdentity.has(displayTextKey(text))) continue;
    const identity = typeValueIdentity(true, textType, text);
    let cell = dCells.get(identity);
    if (!cell) {
      cell = { text, routeRows: 0, numericCount: 0, sum: 0, max: null, textMissingRows: 0, textNullRows: 0 };
      dCells.set(identity, cell);
    }
    cell.routeRows += safeCount(row.routeRows, 'd cell routeRows');
    cell.numericCount += safeCount(row.numericCount, 'd cell numericCount');
    cell.sum += row.numericSum;
    if (row.numericMax !== null) cell.max = cell.max === null ? row.numericMax : Math.max(cell.max, row.numericMax);
    cell.textMissingRows += safeCount(row.textMissingRows, 'd cell textMissingRows');
    cell.textNullRows += safeCount(row.textNullRows, 'd cell textNullRows');
  }
  const dWitnesses = [...dCells.values()].filter(cell => cell.numericCount >= 2 && cell.sum !== cell.max)
    .sort((left, right) => stableJson(left.text).localeCompare(stableJson(right.text)));
  oracle.dDuplicateWitnessBucketCount = dWitnesses.length;
  oracle.dDuplicateWitnesses = dWitnesses.slice(0, 5).map(({ text, routeRows, numericCount, sum, max, textMissingRows, textNullRows }) => ({
    text, categoryPresent: true, categoryType: 'STRING', category: 'd', routeRows, numericCount, sum, max, textMissingRows, textNullRows,
  }));
  validateRelatedQuantityPivotOracle(oracle);
  return oracle;
}

export function positiveRelatedQuantityDuplicateWitness(oracle) {
  validateRelatedQuantityPivotOracle(oracle);
  for (const text of coalescedPivotGroups(oracle)) {
    for (const [key, group] of text.groups) {
      if (group.sourceRows <= 1 || group.numericRows <= 1 || group.valueSum === group.valueMax) continue;
      return {
        text: { value: text.value },
        category: key === 'MISSING' ? { kind: 'MISSING' }
          : key === 'NULL' ? { kind: 'NULL' } : { kind: 'STRING', string: group.category },
        sourceRows: group.sourceRows,
        numericRows: group.numericRows,
        sum: group.valueSum,
        max: group.valueMax,
      };
    }
  }
  return null;
}

const quantityDuplicateWitnesses = oracle => coalescedPivotGroups(oracle)
  .flatMap(text => [...text.groups].filter(([, group]) => group.sourceRows > 1 && group.numericRows > 1 && group.valueSum !== group.valueMax)
    .map(([key, group]) => ({ text: text.value, key, group })));

export function expectedTextOnlyQuantityPivotRows(oracle, { groupColumn, categories, duplicatePolicy }) {
  validateRelatedQuantityPivotOracle(oracle);
  if (oracle.complete !== true) throw new Error('Text-only Pivot expectations require a complete bounded raw-route oracle');
  if (oracle.groups.some(group => group.nonNumericValueRows > 0) || (oracle.fullNonNumericValueCount ?? 0) > 0) throw new Error('Text-only SUM/MAX cannot prove rows with non-null nonnumeric Observation.valueQuantity.value values');
  if (!groupColumn || typeof groupColumn.name !== 'string' || typeof groupColumn.label !== 'string') throw new Error('Text-only Pivot requires its exact authored group output');
  if (!Array.isArray(categories) || categories.length === 0) throw new Error('Text-only Pivot requires the exact authored category outputs');
  if (!['SUM', 'MAX'].includes(duplicatePolicy)) throw new Error('Text-only expected rows support SUM and MAX only');

  const outputByCategory = new Map();
  for (const category of categories) {
    if (!category?.output || typeof category.output.name !== 'string' || typeof category.output.label !== 'string') throw new Error('Each category must bind an exact Pivot output');
    const identity = categoryIdentity(category.key);
    if (outputByCategory.has(identity)) throw new Error(`Duplicate Pivot category output ${identity}`);
    outputByCategory.set(identity, category.output);
  }
  const domainKeys = Array.isArray(oracle.categoryDomain) ? oracle.categoryDomain.map(entry => entry.key) : oracle.groups.map(group => !group.categoryPresent
    ? { kind: 'MISSING' }
    : group.category === null ? { kind: 'NULL' } : { kind: 'STRING', string: group.category });
  const oracleCategories = [...new Set(domainKeys.map(categoryIdentity))].sort();
  if (JSON.stringify([...outputByCategory.keys()].sort()) !== JSON.stringify(oracleCategories)) throw new Error('Candidate category outputs must exactly cover the raw related-route category domain');

  const texts = coalescedPivotGroups(oracle);
  if (texts.some(text => !text)) throw new Error('Selected visible text key is missing its complete raw aggregate');
  const rows = texts.map(text => {
    const row = { [groupColumn.name]: text.value };
    for (const [key, output] of outputByCategory) {
      const group = text.groups.get(key);
      const value = group?.numericRows ? (duplicatePolicy === 'SUM' ? group.valueSum : group.valueMax) : null;
      row[output.name] = value;
    }
    return row;
  });
  return {
    columns: [groupColumn, ...categories.map(category => category.output)],
    rows,
    rowCount: rows.length,
    sourceRows: oracle.sourceRows,
    previewSourceRows: oracle.sampleSourceRows ?? oracle.sourceRows,
    fullGroupCount: oracle.fullRouteCounts?.visibleTextGroupCount ?? rows.length,
    fullCellCount: oracle.fullRouteCounts?.visiblePivotCellCount ?? rows.length * categories.length,
    fullCategoryCount: oracle.fullRouteCounts?.categoryDomainCount ?? categories.length,
    completeTextKeys: Array.isArray(oracle.previewTextGroupKeys) ? [...oracle.previewTextGroupKeys] : rows.map(row => row[groupColumn.name]),
    duplicateBuckets: texts.reduce((count, text) => count + [...text.groups.values()].filter(group => group.sourceRows > 1).length, 0),
    positiveDuplicateWitness: positiveRelatedQuantityDuplicateWitness(oracle),
  };
}

export function summarizeRelatedQuantityRows(rawRows) {
  if (!Array.isArray(rawRows)) throw new Error('Raw related-route rows must be an array');
  const groups = new Map();
  const routeTerminals = new Set();
  for (const row of rawRows) {
    if (!row || typeof row !== 'object' || Array.isArray(row)) throw new Error('Raw route row must be an object');
    assertString(row.specimenId, 'specimenId');
    if (typeof row.patientPresent !== 'boolean' || typeof row.observationPresent !== 'boolean') throw new Error('Raw route row must preserve both Related presence boundaries');
    if (row.patientPresent !== (typeof row.patientId === 'string' && row.patientId.length > 0)) throw new Error('Patient document ID must match its path-presence bit');
    if (row.observationPresent !== (typeof row.observationId === 'string' && row.observationId.length > 0)) throw new Error('Observation document ID must match its path-presence bit');
    if (row.observationPresent && !row.patientPresent) throw new Error('Observation route cannot exist without a Patient path');
    const terminalIdentity = JSON.stringify([row.specimenId, row.patientPresent ? row.patientId : null, row.observationPresent ? row.observationId : null]);
    if (routeTerminals.has(terminalIdentity)) throw new Error('Raw related route must deduplicate terminal identities per Specimen root and parent path');
    routeTerminals.add(terminalIdentity);
    for (const field of ['textPresent', 'codePresent', 'valuePresent']) if (typeof row[field] !== 'boolean') throw new Error(`Raw route row must preserve ${field}`);
    if (row.textPresent && row.text !== null && typeof row.text !== 'string') throw new Error('Present Observation.valueCodeableConcept.text must be string or null');
    if (row.codePresent && row.code !== null && typeof row.code !== 'string') throw new Error('Present Observation.valueQuantity.code must be string or null');
    if (row.valuePresent && typeof row.value === 'number' && !Number.isFinite(row.value)) throw new Error('Present numeric Observation.valueQuantity.value must be finite');
    if (!row.observationPresent && (!row.textPresent || !row.codePresent || !row.valuePresent || row.text !== null || row.code !== null || row.value !== null)) {
      throw new Error('PRESERVE_PARENT terminal must project present NULL text, category, and amount values');
    }

    const category = !row.codePresent ? 'MISSING' : row.code === null ? 'NULL' : `STRING:${JSON.stringify(row.code)}`;
    const groupTextPresent = row.textPresent;
    const groupText = row.textPresent ? row.text : null;
    const identity = JSON.stringify([groupTextPresent, groupText, category]);
    let group = groups.get(identity);
    if (!group) {
      group = {
        groupTextPresent,
        groupText,
        categoryPresent: row.codePresent,
        category: row.codePresent && row.code !== null ? row.code : null,
        sourceRows: 0,
        missingTextRows: 0,
        explicitNullTextRows: 0,
        missingCodeRows: 0,
        explicitNullCodeRows: 0,
        missingValueRows: 0,
        explicitNullValueRows: 0,
        terminalNullValueRows: 0,
        numericRows: 0,
        nonNumericValueRows: 0,
        valueSum: 0,
        valueMax: null,
        routeWitnesses: [],
      };
      groups.set(identity, group);
    }
    group.sourceRows += 1;
    group.missingTextRows += row.textPresent ? 0 : 1;
    group.explicitNullTextRows += row.textPresent && row.text === null ? 1 : 0;
    group.missingCodeRows += row.codePresent ? 0 : 1;
    group.explicitNullCodeRows += row.codePresent && row.code === null ? 1 : 0;
    if (!row.observationPresent) {
      group.terminalNullValueRows += 1;
    } else if (row.valuePresent && typeof row.value === 'number') {
      group.numericRows += 1;
      group.valueSum += row.value;
      group.valueMax = group.valueMax === null ? row.value : Math.max(group.valueMax, row.value);
    } else if (!row.valuePresent) {
      group.missingValueRows += 1;
    } else if (row.value === null) {
      group.explicitNullValueRows += 1;
    } else {
      group.nonNumericValueRows += 1;
    }
    if (group.routeWitnesses.length < 3) group.routeWitnesses.push([row.specimenId, row.patientId, row.observationId]);
  }
  const result = {
    specimenCount: new Set(rawRows.map(row => row.specimenId)).size,
    emptySpecimenCount: new Set(rawRows.filter(row => !row.patientPresent).map(row => row.specimenId)).size,
    matchedPatientRows: new Set(rawRows.filter(row => row.patientPresent).map(row => JSON.stringify([row.specimenId, row.patientId]))).size,
    emptyPatientObservationCount: new Set(rawRows.filter(row => row.patientPresent && !row.observationPresent).map(row => JSON.stringify([row.specimenId, row.patientId]))).size,
    matchedObservationRows: rawRows.filter(row => row.observationPresent).length,
    preservedEmptySpecimenRows: rawRows.filter(row => !row.patientPresent).length,
    preservedEmptyPatientRows: rawRows.filter(row => row.patientPresent && !row.observationPresent).length,
    sourceRows: rawRows.length,
    groups: [...groups.values()].sort((left, right) => JSON.stringify([left.groupTextPresent, left.groupText, left.categoryPresent, left.category]).localeCompare(JSON.stringify([right.groupTextPresent, right.groupText, right.categoryPresent, right.category]))),
  };
  return validateRelatedQuantityPivotOracle(result);
}
