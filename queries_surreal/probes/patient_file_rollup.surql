RETURN (
  SELECT VALUE (
    IF true {
      LET $specimens = <-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-Specimen;
      LET $specimen_refs = (SELECT VALUE string::concat("Specimen/", _key) FROM $specimens);
      LET $group_refs = (
        SELECT VALUE _from
        FROM fhir_edge
        WHERE project = $project
          AND label = "member_entity_Specimen"
          AND from_type = "Group"
          AND _to IN $specimen_refs
        GROUP BY _from
      );
      LET $groups = (
        SELECT *
        FROM Group
        WHERE string::concat("Group/", _key) IN $group_refs
      );

      LET $direct_file_refs = (
        SELECT VALUE _from
        FROM fhir_edge
        WHERE project = $project
          AND label = "subject_Specimen"
          AND from_type = "DocumentReference"
          AND _to IN $specimen_refs
        GROUP BY _from
      );
      LET $grouped_file_refs = (
        SELECT VALUE _from
        FROM fhir_edge
        WHERE project = $project
          AND label = "subject_Group"
          AND from_type = "DocumentReference"
          AND _to IN $group_refs
        GROUP BY _from
      );
      LET $patient_files = <-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-DocumentReference;
      LET $patient_file_refs = (
        SELECT VALUE string::concat("DocumentReference/", _key)
        FROM $patient_files
      );
      LET $all_file_refs = $direct_file_refs + $grouped_file_refs + $patient_file_refs;
      LET $file_refs = $all_file_refs;
      LET $files = (
        SELECT *
        FROM DocumentReference
        WHERE string::concat("DocumentReference/", _key) IN $file_refs
      );
      LET $file_summaries = (
        SELECT VALUE {
          file_id: id,
          data_category: IF payload.category AND payload.category[0] AND payload.category[0].coding AND payload.category[0].coding[0] {
            IF payload.category[0].coding[0].display {
              payload.category[0].coding[0].display
            } ELSE {
              payload.category[0].coding[0].code
            }
          } ELSE {
            NONE
          }
        }
        FROM $files
      );

      {
        patient_key: _key,
        specimen_count: count($specimens),
        group_count: count($group_refs),
        direct_file_ref_count: count($direct_file_refs),
        grouped_file_ref_count: count($grouped_file_refs),
        patient_file_ref_count: count($patient_file_refs),
        file_ref_count: count($file_refs),
        file_count: count($files),
        direct_file_refs: $direct_file_refs[0..10],
        grouped_file_refs: $grouped_file_refs[0..10],
        patient_file_refs: $patient_file_refs[0..10],
        file_refs: $file_refs[0..10],
        sample_categories: (
          SELECT VALUE data_category
          FROM $file_summaries
          WHERE data_category != NONE
          GROUP BY data_category
          LIMIT 10
        )
      }
    }
  )
  FROM Patient
  WHERE project = $project
    AND ($auth_resource_path = "" OR auth_resource_path = $auth_resource_path)
    AND ($patient_key = "" OR _key = $patient_key)
  ORDER BY _key ASC
  LIMIT IF $max_rows > 0 { $max_rows } ELSE { 250 }
  TIMEOUT 20s
);
