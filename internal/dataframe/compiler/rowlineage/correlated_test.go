package rowlineage_test

func groupRowsMembership(revisionID, project, generation, groupID, memberID string) map[string]any {
	return map[string]any{
		"_key":       "membership-" + revisionID + "-" + groupID + "-" + memberID,
		"revisionId": revisionID, "groupId": groupID, "project": project, "generation": generation,
		"resourceType": "Observation", "id": memberID,
		"ref": map[string]any{"project": project, "generation": generation, "resourceType": "Observation", "id": memberID},
	}
}
