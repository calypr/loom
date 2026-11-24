package aql

func (r *physicalPlanRenderer) rowLineage() rowLineageRenderer {
	return rowLineageRenderer{renderContext: &r.renderContext, core: r}
}
