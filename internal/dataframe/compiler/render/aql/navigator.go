package aql

func (r *physicalPlanRenderer) navigation() navigationRenderer {
	return navigationRenderer{renderContext: &r.renderContext, core: r}
}
