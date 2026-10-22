# Builder browser case proposals

Status: generated review queue. These proposals are not browser evidence until a passing report exists.

## Summary

| Measure | Count |
| --- | ---: |
| routes | 28 |
| tier1 | 17 |
| tier2 | 11 |
| stateful | 8 |
| readLike | 20 |
| high | 7 |
| medium | 11 |
| low | 10 |

## Review queue

| Tier | Route | Status | Mode | Priority | Proposed case | Callers |
| ---: | --- | --- | --- | --- | --- | --- |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-category-discoveries` | trace-tested | read | medium | `builder-post-construction-category-discoveries` | `builder.reshape.pivot_values` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-choice-proposals` | modeled-only | read | medium | `builder-post-construction-choice-proposals` | `builder.add_columns.catalog_field`, `builder.add_columns.catalog_semantic`, `builder.add_columns.grouped_row_values` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-proposals` | contract-tested | read | medium | `builder-post-construction-proposals` | `builder.add_columns.related_field`, `builder.calculate.derive`, `builder.combine.append`, `builder.combine.key_join`, `builder.combine.membership`, `builder.construction_proposal_apply_cancel_retry`, `builder.filter_rows.related_eligibility`, `builder.filter_rows.scalar_condition`, `builder.related_source_column`, `builder.reshape.expand_scalar_list`, `builder.reshape.group_coded`, `builder.reshape.group_field`, `builder.reshape.pivot_coded`, `builder.reshape.pivot_values`, `builder.reshape.related_expand`, `builder.reshape.unpivot` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-preview` | contract-tested | read | medium | `builder-post-interpretation-preview` | `builder.interpretation.preview_and_apply_candidate` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-revisions` | modeled-only | stateful | high | `builder-post-interpretation-revisions` | `builder.interpretation.create_revision_from_column` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-mapping` | modeled-only | stateful | high | `builder-post-population-mapping` | `builder.population.selected_resource_coverage_check` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-member-proposals` | contract-tested | read | medium | `builder-post-population-member-proposals` | `builder.population.review_and_remove_attached_member` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-routes` | modeled-only | read | medium | `builder-post-population-routes` | `builder.population.attach_or_replace_selection`, `builder.population.exclude_unmapped_member` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/publish` | unproven | stateful | high | `builder-post-publish` | `builder.publish` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-change` | modeled-only | stateful | high | `builder-post-row-change` | `builder.root.change_or_replace` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-definition-proposals` | contract-tested | read | medium | `builder-post-row-definition-proposals` | `builder.reshape.group_field`, `builder.row_definition.expanded_source_rows`, `builder.row_definition.explicit_groups`, `builder.row_definition.rows_per_starting_record`, `builder.row_definition_proposal_apply_cancel` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-category-discoveries` | unproven | read | medium | `builder-post-table-shape-category-discoveries` | `legacy.table_shape.category_discovery` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-proposals` | contract-tested | read | medium | `builder-post-table-shape-proposals` | `builder.calculate.table_shape_derived_column`, `legacy.table_shape.grouped_pivot`, `legacy.table_shape.unpivot` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-resolutions` | modeled-only | stateful | high | `builder-post-table-shape-resolutions` | `builder.calculate.table_shape_derived_column`, `legacy.table_shape.grouped_pivot`, `legacy.table_shape.unpivot` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections` | modeled-only | stateful | high | `builder-post-selections` | `builder.population.exclude_unmapped_member` |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}` | modeled-only | read | medium | `builder-get-selectionrevision` | `builder.demo_entry_route`, `builder.population.review_and_remove_attached_member` |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}/explicit-groups` | modeled-only | stateful | high | `builder-post-explicit-groups` | `builder.row_definition.explicit_groups` |
| 2 | `DELETE /api/v1/projects/{project}/explorers/{explorerId}` | modeled-only | stateful | medium | `builder-delete-explorerid` | `builder.explorer_create_delete` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/column-source` | modeled-only | read | low | `builder-post-column-source` | `builder.column_source.inspect` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-choices` | contract-tested | read | low | `builder-post-construction-choices` | `builder.add_columns.catalog_field`, `builder.add_columns.catalog_semantic`, `builder.add_columns.source_selection`, `builder.related_source_column` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-inputs` | contract-tested | read | low | `builder-post-construction-inputs` | `builder.combine.append`, `builder.combine.key_join`, `builder.combine.membership` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/frame-source-options` | modeled-only | read | low | `builder-post-frame-source-options` | `builder.add_columns.source_selection`, `builder.frame_source.manage_existing`, `builder.reshape.pivot_coded` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/related-expand-choices` | contract-tested | read | low | `builder-post-related-expand-choices` | `builder.filter_rows.related_eligibility`, `builder.reshape.related_expand` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/related-expand-contributors` | contract-tested | read | low | `builder-post-related-expand-contributors` | `builder.filter_rows.related_eligibility`, `builder.reshape.related_expand` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/related-field-choices` | modeled-only | read | low | `builder-post-related-field-choices` | `builder.add_columns.related_field` |
| 2 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-definition-choices` | contract-tested | read | low | `builder-get-row-definition-choices` | `builder.reshape.group_field`, `builder.row_definition.expanded_source_rows`, `builder.row_definition.explicit_groups`, `builder.row_definition.rows_per_starting_record`, `builder.row_definition_proposal_apply_cancel` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-lineage` | modeled-only | read | low | `builder-post-row-lineage` | `builder.preview_table.inspect_row_sources` |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-capabilities` | modeled-only | read | low | `builder-post-table-shape-capabilities` | `builder.calculate.table_shape_derived_column`, `legacy.table_shape.grouped_pivot`, `legacy.table_shape.unpivot` |

## Bounds

A browser proposal is a target, not proof. Run the lifecycle and regenerate `browser-evidence.json` from the passing Playwright report before changing the route proof.
