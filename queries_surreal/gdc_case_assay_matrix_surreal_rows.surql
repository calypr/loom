RETURN (
  SELECT VALUE (
    IF true {
      LET $patient = payload;
      LET $patient_ids = IF $patient.identifier { $patient.identifier } ELSE { [] };
      LET $patient_exts = IF $patient.extension { $patient.extension } ELSE { [] };

      LET $case_id = IF $patient_ids {
        $patient_ids[WHERE $this.system AND string::contains($this.system, "case_id")][0].value
      } ELSE {
        NONE
      };
      LET $case_submitter_id = IF $patient_ids {
        $patient_ids[WHERE $this.system AND string::contains($this.system, "case_submitter_id")][0].value
      } ELSE {
        NONE
      };

      LET $race_ext = $patient_exts[WHERE $this.url AND string::contains($this.url, "us-core-race")][0];
      LET $ethnicity_ext = $patient_exts[WHERE $this.url AND string::contains($this.url, "us-core-ethnicity")][0];
      LET $birth_sex_ext = $patient_exts[WHERE $this.url AND string::contains($this.url, "us-core-birthsex")][0];
      LET $patient_age_ext = $patient_exts[WHERE $this.url AND string::contains($this.url, "Patient-age")][0];
      LET $patient_study_ext = $patient_exts[WHERE $this.url AND string::contains($this.url, "part-of-study")][0];

      LET $race = IF $race_ext.valueString { $race_ext.valueString } ELSE { $race_ext.valueCode };
      LET $ethnicity = IF $ethnicity_ext.valueString { $ethnicity_ext.valueString } ELSE { $ethnicity_ext.valueCode };
      LET $birth_sex = IF $birth_sex_ext.valueCode { $birth_sex_ext.valueCode } ELSE { $birth_sex_ext.valueString };
      LET $patient_age = IF $patient_age_ext.valueQuantity { $patient_age_ext.valueQuantity.value } ELSE { NONE };
      LET $patient_study_ref = IF $patient_study_ext.valueReference { $patient_study_ext.valueReference.reference } ELSE { NONE };

      LET $specimens = <-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-Specimen;
      LET $rollup = (
        SELECT *
        FROM patient_file_rollup
        WHERE project = $project
          AND patient_key = _key
        LIMIT 1
      )[0];
      LET $specimen_types = IF $rollup AND $rollup.specimen_types { $rollup.specimen_types } ELSE { [] };
      LET $preservation_methods = IF $rollup AND $rollup.preservation_methods { $rollup.preservation_methods } ELSE { [] };

      LET $patient_files = <-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-DocumentReference;
      LET $patient_file_refs = (
        SELECT VALUE string::concat("DocumentReference/", _key)
        FROM $patient_files
      );
      LET $rollup_file_refs = (
        IF $rollup AND $rollup.file_keys {
          $rollup.file_keys.map(|$key| string::concat("DocumentReference/", $key))
        } ELSE {
          []
        }
      );
      LET $all_file_refs = $rollup_file_refs + $patient_file_refs;
      LET $file_refs = $all_file_refs;
      LET $files = (
        SELECT *
        FROM DocumentReference
        WHERE project = $project
          AND _key IN (IF $rollup AND $rollup.file_keys { $rollup.file_keys } ELSE { [] })
      );

      LET $file_summaries = (
        SELECT VALUE (
          IF true {
            LET $doc = payload;
            LET $doc_ids = IF $doc.identifier { $doc.identifier } ELSE { [] };
            LET $content = IF $doc.content AND $doc.content[0] { $doc.content[0] } ELSE { {} };
            LET $attachment = IF $content.attachment { $content.attachment } ELSE { {} };
            LET $type_codings = IF $doc.type AND $doc.type.coding { $doc.type.coding } ELSE { [] };
            LET $category_codings = (
              IF $doc.category {
                $doc.category
                  .map(|$category| IF $category.coding { $category.coding } ELSE { [] })
                  .reduce(|$one, $two| $one + $two)
              } ELSE {
                []
              }
            );
            LET $file_id = IF $doc_ids {
              $doc_ids[WHERE $this.system AND string::contains($this.system, "file_id")][0].value
            } ELSE {
              NONE
            };
            LET $data_format = IF $type_codings[0] {
              IF $type_codings[0].display { $type_codings[0].display } ELSE { $type_codings[0].code }
            } ELSE {
              NONE
            };
            LET $data_category_coding = $category_codings[WHERE $this.system AND string::contains($this.system, "data_category")][0];
            LET $data_type_coding = $category_codings[WHERE $this.system AND string::contains($this.system, "data_type")][0];
            LET $experimental_strategy_coding = $category_codings[WHERE $this.system AND string::contains($this.system, "experimental_strategy")][0];
            LET $workflow_type_coding = $category_codings[WHERE $this.system AND string::contains($this.system, "workflow_type")][0];
            LET $platform_coding = $category_codings[WHERE $this.system AND string::contains($this.system, "platform")][0];
            LET $file_access_coding = $category_codings[WHERE $this.system AND string::contains($this.system, "access")][0];

            LET $data_category = IF $data_category_coding.display { $data_category_coding.display } ELSE { $data_category_coding.code };
            LET $data_type = IF $data_type_coding.display { $data_type_coding.display } ELSE { $data_type_coding.code };
            LET $experimental_strategy = IF $experimental_strategy_coding.display { $experimental_strategy_coding.display } ELSE { $experimental_strategy_coding.code };
            LET $workflow_type = IF $workflow_type_coding.display { $workflow_type_coding.display } ELSE { $workflow_type_coding.code };
            LET $platform = IF $platform_coding.display { $platform_coding.display } ELSE { $platform_coding.code };
            LET $file_access = IF $file_access_coding.display { $file_access_coding.display } ELSE { $file_access_coding.code };

            {
              file_did: $doc.id,
              file_id: $file_id,
              file_name: $attachment.title,
              file_url: $attachment.url,
              file_size: $attachment.size,
              data_category: $data_category,
              data_type: $data_type,
              data_format: $data_format,
              experimental_strategy: $experimental_strategy,
              workflow_type: $workflow_type,
              platform: $platform,
              access: $file_access,
              is_snv: $data_category = "Simple Nucleotide Variation",
              is_annotated_somatic: $data_category = "Simple Nucleotide Variation" AND $data_type = "Annotated Somatic Mutation",
              is_raw_somatic: $data_category = "Simple Nucleotide Variation" AND $data_type = "Raw Simple Somatic Mutation",
              is_expression: $data_category = "Transcriptome Profiling" AND $data_type = "Gene Expression Quantification",
              is_fusion: $data_type = "Transcript Fusion",
              is_cnv: $data_category = "Copy Number Variation",
              is_methylation: $data_category = "DNA Methylation",
              is_slide: $data_type = "Slide Image",
              is_aligned_reads: $data_type = "Aligned Reads",
              is_clinical: $data_category = "Clinical",
              is_wxs: $experimental_strategy = "WXS",
              is_wgs: $experimental_strategy = "WGS",
              is_rnaseq: $experimental_strategy = "RNA-Seq"
            }
          }
        )
        FROM $files
      );

      LET $file_count = count($file_summaries);
      LET $snv_file_count = count($file_summaries[WHERE is_snv = true]);
      LET $annotated_somatic_file_count = count($file_summaries[WHERE is_annotated_somatic = true]);
      LET $raw_somatic_file_count = count($file_summaries[WHERE is_raw_somatic = true]);
      LET $expression_file_count = count($file_summaries[WHERE is_expression = true]);
      LET $fusion_file_count = count($file_summaries[WHERE is_fusion = true]);
      LET $cnv_file_count = count($file_summaries[WHERE is_cnv = true]);
      LET $methylation_file_count = count($file_summaries[WHERE is_methylation = true]);
      LET $slide_file_count = count($file_summaries[WHERE is_slide = true]);
      LET $aligned_read_file_count = count($file_summaries[WHERE is_aligned_reads = true]);
      LET $clinical_file_count = count($file_summaries[WHERE is_clinical = true]);
      LET $has_wxs_count = count($file_summaries[WHERE is_wxs = true]);
      LET $has_wgs_count = count($file_summaries[WHERE is_wgs = true]);
      LET $has_rnaseq_count = count($file_summaries[WHERE is_rnaseq = true]);

      LET $data_categories = (
        SELECT VALUE data_category
        FROM $file_summaries
        WHERE data_category != NONE
        GROUP BY data_category
      );
      LET $data_types = (
        SELECT VALUE data_type
        FROM $file_summaries
        WHERE data_type != NONE
        GROUP BY data_type
      );
      LET $experimental_strategies = (
        SELECT VALUE experimental_strategy
        FROM $file_summaries
        WHERE experimental_strategy != NONE
        GROUP BY experimental_strategy
      );
      LET $workflow_types = (
        SELECT VALUE workflow_type
        FROM $file_summaries
        WHERE workflow_type != NONE
        GROUP BY workflow_type
      );

      LET $representative_snv_files = (SELECT * FROM $file_summaries WHERE is_snv = true LIMIT 5);
      LET $representative_expression_files = (SELECT * FROM $file_summaries WHERE is_expression = true LIMIT 5);
      LET $representative_cnv_files = (SELECT * FROM $file_summaries WHERE is_cnv = true LIMIT 5);
      LET $representative_methylation_files = (SELECT * FROM $file_summaries WHERE is_methylation = true LIMIT 5);
      LET $representative_slide_files = (SELECT * FROM $file_summaries WHERE is_slide = true LIMIT 5);

      LET $conditions = <-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-Condition;
      LET $diagnosis_count = count($conditions);
      LET $primary_condition = IF $conditions[0] { $conditions[0].payload } ELSE { NONE };
      LET $primary_condition_ids = IF $primary_condition.identifier { $primary_condition.identifier } ELSE { [] };
      LET $primary_condition_codings = IF $primary_condition.code AND $primary_condition.code.coding { $primary_condition.code.coding } ELSE { [] };
      LET $primary_diagnosis_preferred_coding = $primary_condition_codings[WHERE $this.system AND string::contains($this.system, "primary_diagnosis")][0];
      LET $primary_diagnosis_preferred = IF $primary_diagnosis_preferred_coding.display {
        $primary_diagnosis_preferred_coding.display
      } ELSE {
        $primary_diagnosis_preferred_coding.code
      };
      LET $primary_diagnosis = IF $primary_diagnosis_preferred {
        $primary_diagnosis_preferred
      } ELSE IF $primary_condition_codings[0] {
        IF $primary_condition_codings[0].display { $primary_condition_codings[0].display } ELSE { $primary_condition_codings[0].code }
      } ELSE {
        NONE
      };
      LET $diagnosis_id = IF $primary_condition_ids {
        $primary_condition_ids[WHERE $this.system AND string::contains($this.system, "diagnosis_id")][0].value
      } ELSE {
        NONE
      };
      LET $diagnosis_body_site = IF $primary_condition.bodySite AND $primary_condition.bodySite[0] AND $primary_condition.bodySite[0].coding AND $primary_condition.bodySite[0].coding[0] {
        IF $primary_condition.bodySite[0].coding[0].display {
          $primary_condition.bodySite[0].coding[0].display
        } ELSE {
          $primary_condition.bodySite[0].coding[0].code
        }
      } ELSE {
        NONE
      };

      LET $research_subject = (<-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-ResearchSubject)[0];
      LET $research_subject_payload = IF $research_subject { $research_subject.payload } ELSE { NONE };
      LET $study_ref = IF $research_subject_payload.study {
        $research_subject_payload.study.reference
      } ELSE {
        $patient_study_ref
      };
      LET $study_id = IF $study_ref AND string::contains($study_ref, "/") {
        string::split($study_ref, "/")[1]
      } ELSE {
        NONE
      };
      LET $study = IF $study_id {
        (SELECT * FROM ONLY type::record("ResearchStudy", $study_id))
      } ELSE {
        NONE
      };
      LET $study_payload = IF $study { $study.payload } ELSE { NONE };
      LET $study_ids = IF $study_payload.identifier { $study_payload.identifier } ELSE { [] };

      LET $treatments = <-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-MedicationAdministration;
      LET $treatment_categories = (
        IF $treatments {
          $treatments
            .map(|$treatment|
              IF $treatment.payload.category {
                $treatment.payload.category
                  .map(|$category|
                    IF $category.coding {
                      $category.coding.map(|$coding|
                        IF $coding.display { $coding.display } ELSE { $coding.code }
                      )
                    } ELSE {
                      []
                    }
                  )
                  .reduce(|$one, $two| $one + $two)
              } ELSE {
                []
              }
            )
            .reduce(|$one, $two| $one + $two)
        } ELSE {
          []
        }
      );
      LET $specimen_count = IF $rollup { $rollup.specimen_count } ELSE { count($specimens) };
      LET $treatment_count = count($treatments);

      {
        _key: _key,
        project: project,
        recipe: "gdc_case_assay_matrix",
        case_fhir_id: $patient.id,
        case_id: $case_id,
        case_submitter_id: $case_submitter_id,
        gender: $patient.gender,
        deceased: $patient.deceasedBoolean,
        birth_sex: $birth_sex,
        race: $race,
        ethnicity: $ethnicity,
        patient_age: $patient_age,
        diagnosis_count: IF $diagnosis_count { $diagnosis_count } ELSE { 0 },
        diagnosis_id: $diagnosis_id,
        primary_diagnosis: $primary_diagnosis,
        diagnosis_body_site: $diagnosis_body_site,
        research_subject_id: IF $research_subject_payload { $research_subject_payload.id } ELSE { NONE },
        research_subject_status: IF $research_subject_payload { $research_subject_payload.status } ELSE { NONE },
        study_id: IF $study_payload { $study_payload.id } ELSE { NONE },
        study_identifier: IF $study_ids[0] { $study_ids[0].value } ELSE { NONE },
        study_name: IF $study_payload { $study_payload.name } ELSE { NONE },
        study_title: IF $study_payload { $study_payload.title } ELSE { NONE },
        specimen_count: IF $specimen_count { $specimen_count } ELSE { 0 },
        specimen_types: $specimen_types,
        preservation_methods: $preservation_methods,
        file_count: IF $file_count { $file_count } ELSE { 0 },
        data_categories: $data_categories,
        data_types: $data_types,
        experimental_strategies: $experimental_strategies,
        workflow_types: $workflow_types,
        has_snv: IF $snv_file_count > 0 { true } ELSE { false },
        snv_file_count: IF $snv_file_count { $snv_file_count } ELSE { 0 },
        annotated_somatic_file_count: IF $annotated_somatic_file_count { $annotated_somatic_file_count } ELSE { 0 },
        raw_somatic_file_count: IF $raw_somatic_file_count { $raw_somatic_file_count } ELSE { 0 },
        has_wxs: IF $has_wxs_count > 0 { true } ELSE { false },
        has_wgs: IF $has_wgs_count > 0 { true } ELSE { false },
        has_rnaseq: IF $has_rnaseq_count > 0 { true } ELSE { false },
        has_expression: IF $expression_file_count > 0 { true } ELSE { false },
        expression_file_count: IF $expression_file_count { $expression_file_count } ELSE { 0 },
        has_fusion: IF $fusion_file_count > 0 { true } ELSE { false },
        fusion_file_count: IF $fusion_file_count { $fusion_file_count } ELSE { 0 },
        has_cnv: IF $cnv_file_count > 0 { true } ELSE { false },
        cnv_file_count: IF $cnv_file_count { $cnv_file_count } ELSE { 0 },
        has_methylation: IF $methylation_file_count > 0 { true } ELSE { false },
        methylation_file_count: IF $methylation_file_count { $methylation_file_count } ELSE { 0 },
        has_slide: IF $slide_file_count > 0 { true } ELSE { false },
        slide_file_count: IF $slide_file_count { $slide_file_count } ELSE { 0 },
        aligned_read_file_count: IF $aligned_read_file_count { $aligned_read_file_count } ELSE { 0 },
        clinical_file_count: IF $clinical_file_count { $clinical_file_count } ELSE { 0 },
        treatment_count: IF $treatment_count { $treatment_count } ELSE { 0 },
        treatment_categories: $treatment_categories,
        representative_snv_files: $representative_snv_files,
        representative_expression_files: $representative_expression_files,
        representative_cnv_files: $representative_cnv_files,
        representative_methylation_files: $representative_methylation_files,
        representative_slide_files: $representative_slide_files
      }
    }
  )
  FROM Patient
  WHERE project = $project
    AND ($auth_resource_path = "" OR auth_resource_path = $auth_resource_path)
  ORDER BY _key ASC
  LIMIT IF $max_rows > 0 { $max_rows } ELSE { 1000000000 }
  TIMEOUT 30s
);
