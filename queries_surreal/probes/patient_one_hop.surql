RETURN (
  SELECT VALUE {
    patient_key: _key,
    specimen_count: count(<-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-Specimen),
    direct_file_count: count(<-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-DocumentReference),
    condition_count: count(<-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-Condition),
    research_subject_count: count(<-(fhir_edge WHERE project = $project AND label = "individual_Patient")<-ResearchSubject)
  }
  FROM Patient
  WHERE project = $project
    AND ($auth_resource_path = "" OR auth_resource_path = $auth_resource_path)
  ORDER BY _key ASC
  LIMIT IF $max_rows > 0 { $max_rows } ELSE { 1000 }
  TIMEOUT 15s
);
