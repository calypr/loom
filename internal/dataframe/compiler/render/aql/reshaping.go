package aql

func (r *physicalPlanRenderer) tableShape() tableShapeRenderer {
	return tableShapeRenderer{renderContext: &r.renderContext, core: r}
}
