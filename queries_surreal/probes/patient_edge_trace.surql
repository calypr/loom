RETURN (
  SELECT VALUE (
    IF true {
      LET $specimens = <-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-Specimen;
      LET $specimen_refs = (SELECT VALUE string::concat("Specimen/", _key) FROM $specimens);
      LET $group_edges = (
        SELECT _from, _to, label, from_type, to_type
        FROM fhir_edge
        WHERE project = $project
          AND label = "member_entity_Specimen"
          AND _to IN $specimen_refs
        LIMIT 20
      );
      LET $group_refs = (SELECT VALUE _from FROM $group_edges GROUP BY _from);
      LET $specimen_file_edges = (
        SELECT _from, _to, label, from_type, to_type
        FROM fhir_edge
        WHERE project = $project
          AND label = "subject_Specimen"
          AND _to IN $specimen_refs
        LIMIT 20
      );
      LET $group_file_edges = (
        SELECT _from, _to, label, from_type, to_type
        FROM fhir_edge
        WHERE project = $project
          AND label = "subject_Group"
          AND _to IN $group_refs
        LIMIT 20
      );
      {
        patient_key: _key,
        specimen_count: count($specimens),
        specimen_refs: $specimen_refs,
        group_edges: $group_edges,
        group_refs: $group_refs,
        specimen_file_edges: $specimen_file_edges,
        group_file_edges: $group_file_edges
      }
    }
  )
  FROM Patient
  WHERE project = $project
    AND ($auth_resource_path = "" OR auth_resource_path = $auth_resource_path)
    AND ($patient_key = "" OR _key = $patient_key)
  ORDER BY _key ASC
  LIMIT IF $max_rows > 0 { $max_rows } ELSE { 1 }
  TIMEOUT 20s
);
