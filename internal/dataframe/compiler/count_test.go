package compiler

func relatedCountEdge(project, generation, key, from, to, label, fromType, toType, authPath string) map[string]any {
	return map[string]any{
		"_key": project + "_" + key, "_from": from, "_to": to, "project": project, "project_id": project,
		"dataset_generation": generation, "auth_resource_path": authPath,
		"label": label, "from_type": fromType, "to_type": toType,
	}
}
