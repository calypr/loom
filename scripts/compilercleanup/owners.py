WHOLE = {
    'ancestry':'rowlineage','bind':'render/aql','compat':'render/aql',
    'composed':'rowlineage','construction':'rowlineage','diagnostics':'ir',
    'extension':'render/aql','frontier':'population','grain':'/spec','guard':'population',
    'limit':'render/aql','lineage':'rowlineage','lowering':'lower','mapping':'population',
    'match':'render/aql','optimize':'optimize','physical':'lower',
    'render':'render/aql','row':'rowlineage','rows':'rowlineage','scope':'ir',
    'trace':'celltrace','typed':'render/aql','version':'render/aql',
}
TEST = {
    'TestTypedCategoryTerminalOwnsRootHintDistinctAndLimitWithoutMutatingSource': 'render/aql@internal',
    'TestGroupRowLineagePartitionsForwardRowsAgainstArango':'rowlineage',
    'TestFilteredSourceCellTraceMatchesForwardValueAgainstArango':'celltrace',
    'TestCurrentConstructionEvidencePrototype':'celltrace',
    'TestCurrentConstructionTracePrototypeRetainsFilteredStageFallback':'celltrace',
    'TestConstructionGroupRowLineagePagesScopedContributorsAgainstArango':'rowlineage',
    'TestConstructionGroupFilterRowLineagePagesScopedContributorsAgainstArango':'rowlineage',
    'TestRelatedSourceProjectionLivenessForKeylessCount':'lower',
    'TestTerminalRepeatedPrimitiveSelectorValuesAgainstArango':'render/aql',
    'TestTypedFilterValidation':'/spec','TestFilterValueDateValidation':'/spec',
    'TestTypedFilterRejectsPartialTemporalOrderedComparison':'/spec',
    'TestCodeValueRequiresTerminologyCode':'/spec','TestOperatorSupportsKind':'/spec',
    'TestConstructionCodedGroupRowLineagePagesScopedContributorsAgainstArango':'rowlineage',
    'TestPhysicalPlanValidateRejectsRecursiveObjectExpression':'ir',
    'TestValidateTypedFilterForResourceUsesGeneratedPrimitiveMetadata':'/spec',
    'TestValidateTypedFilterForResourceRejectsUnpairedCodingSystem':'/spec',
    'TestCurrentUnpivotPreservesAuthoredSlotsAndCanonicalRendererOrdering':'render/aql',
}
def get_owner(filename,name):
    if name.startswith('TestPopulationMapping') and filename in ('exists','group'):
        return 'population'
    if filename=='plan' and name.startswith('Test'):
        if name.startswith('TestPhysicalPlanValidate'):
            return 'ir'
        if name.startswith(('TestBuildAndRender','TestRenderGeneric')):
            return 'render/aql'
        if name.startswith(('TestBuildGeneric','TestPreparedSelectors','TestChildPivot')):
            return 'lower'
    return TEST.get(name,WHOLE.get(filename,'root'))
