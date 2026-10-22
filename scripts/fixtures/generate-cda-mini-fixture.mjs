import { mkdirSync, writeFileSync } from 'node:fs';

const ROOT = new URL('../../testdata/cda-mini-fixture/', import.meta.url);
mkdirSync(ROOT, { recursive: true });

const PART_OF_STUDY = 'http://fhir-aggregator.org/fhir/StructureDefinition/part-of-study';
const BIRTHSEX = 'http://hl7.org/fhir/us/core/StructureDefinition/us-core-birthsex';
const SUBJECT_ID = 'https://cda.readthedocs.io/subject_id';
const PATIENT_ID = 'https://portal.imaging.datacommons.cancer.gov/PatientID';
const CDA_SYSTEM = 'https://cda.readthedocs.io/system';
const CDA_SPECIMEN = 'https://cda.readthedocs.io/specimen';
const CDA_OBSERVATION = 'https://cda.readthedocs.io/specimen_observation';

const partOf = (study) => ({
  url: PART_OF_STUDY,
  valueReference: { reference: `ResearchStudy/${study}` },
});
const birthsex = (code) => ({ url: BIRTHSEX, valueCode: code });
const identifier = (system, value, use = 'official') => ({ use, system, value });

const studies = [
  {
    resourceType: 'ResearchStudy',
    id: 'cda-mini-study-001',
    extension: [partOf('cda-mini-study-001')],
    identifier: [identifier(CDA_SYSTEM, 'MINI-A')],
    name: 'Lung Cohort A',
    title: 'Lung Cohort A',
    status: 'active',
  },
  {
    resourceType: 'ResearchStudy',
    id: 'cda-mini-study-002',
    extension: [partOf('cda-mini-study-002')],
    identifier: [identifier(CDA_SYSTEM, 'MINI-B')],
    name: 'Sarcoma Cohort B',
    title: 'Sarcoma Cohort B',
    status: 'active',
  },
];

const patients = [
  {
    resourceType: 'Patient',
    id: 'cda-mini-patient-001',
    extension: [partOf('cda-mini-study-001'), birthsex('F')],
    identifier: [
      identifier(SUBJECT_ID, 'mini.subject.001'),
      identifier(PATIENT_ID, 'MINI-001', 'secondary'),
      identifier('https://portal.imaging.datacommons.cancer.gov/idc_case_id', 'mini-case-001', 'secondary'),
    ],
  },
  {
    resourceType: 'Patient',
    id: 'cda-mini-patient-002',
    extension: [partOf('cda-mini-study-001'), birthsex('M')],
    identifier: [
      identifier(SUBJECT_ID, 'mini.subject.002'),
      identifier(PATIENT_ID, 'MINI-002', 'secondary'),
    ],
  },
  {
    resourceType: 'Patient',
    id: 'cda-mini-patient-003',
    extension: [partOf('cda-mini-study-001'), birthsex('F')],
    identifier: [
      identifier(SUBJECT_ID, 'mini.subject.003'),
      identifier(PATIENT_ID, 'MINI-003', 'secondary'),
    ],
  },
  {
    resourceType: 'Patient',
    id: 'cda-mini-patient-004',
    extension: [partOf('cda-mini-study-002'), birthsex('M')],
    identifier: [identifier(SUBJECT_ID, 'mini.subject.004')],
  },
  {
    resourceType: 'Patient',
    id: 'cda-mini-patient-005',
    extension: [partOf('cda-mini-study-002'), birthsex('F')],
    identifier: [
      identifier(SUBJECT_ID, 'mini.subject.005'),
      identifier(PATIENT_ID, 'MINI-005', 'secondary'),
      identifier('https://portal.imaging.datacommons.cancer.gov/submitter_case_id', 'MINI-SUBMIT-005', 'secondary'),
    ],
  },
  {
    resourceType: 'Patient',
    id: 'cda-mini-patient-006',
    extension: [partOf('cda-mini-study-002')],
    identifier: [identifier(SUBJECT_ID, 'mini.subject.006')],
  },
];

const researchSubjects = [
  ['cda-mini-rs-001', 'cda-mini-patient-001', 'cda-mini-study-001'],
  ['cda-mini-rs-002', 'cda-mini-patient-001', 'cda-mini-study-002'],
  ['cda-mini-rs-003', 'cda-mini-patient-002', 'cda-mini-study-001'],
  ['cda-mini-rs-004', 'cda-mini-patient-003', 'cda-mini-study-001'],
  ['cda-mini-rs-005', 'cda-mini-patient-004', 'cda-mini-study-002'],
  ['cda-mini-rs-006', 'cda-mini-patient-005', 'cda-mini-study-002'],
  ['cda-mini-rs-007', 'cda-mini-patient-006', 'cda-mini-study-002'],
].map(([id, patient, study]) => ({
  resourceType: 'ResearchSubject',
  id,
  extension: [partOf(study)],
  identifier: [identifier('https://cda.readthedocs.io/researchsubject', `${id}.RS`)],
  status: 'active',
  study: { reference: `ResearchStudy/${study}` },
  subject: { reference: `Patient/${patient}` },
}));

const specimen = (id, patient, study, code, extra = {}) => ({
  resourceType: 'Specimen',
  id,
  extension: [partOf(study)],
  identifier: [identifier(CDA_SPECIMEN, `${id}.specimen`)],
  type: { coding: [{ system: 'https://cda.readthedocs.io/', code, display: code }] },
  subject: { reference: `Patient/${patient}` },
  ...extra,
});

const specimens = [
  specimen('cda-mini-specimen-001', 'cda-mini-patient-001', 'cda-mini-study-001', 'Tumor'),
  specimen('cda-mini-specimen-002', 'cda-mini-patient-001', 'cda-mini-study-001', 'Normal', {
    parent: [{ reference: 'Specimen/cda-mini-specimen-001' }],
  }),
  specimen('cda-mini-specimen-003', 'cda-mini-patient-002', 'cda-mini-study-001', 'Tumor'),
  specimen('cda-mini-specimen-004', 'cda-mini-patient-003', 'cda-mini-study-001', 'Tumor'),
  specimen('cda-mini-specimen-005', 'cda-mini-patient-004', 'cda-mini-study-002', 'Normal'),
  specimen('cda-mini-specimen-006', 'cda-mini-patient-005', 'cda-mini-study-002', 'Tumor'),
];

const observation = (id, patient, study, code, extra = {}) => ({
  resourceType: 'Observation',
  id,
  extension: [partOf(study)],
  identifier: [identifier(CDA_OBSERVATION, `${id}.observation`)],
  status: 'final',
  category: [{
    coding: [{
      system: 'http://terminology.hl7.org/CodeSystem/observation-category',
      code: 'laboratory',
      display: 'Laboratory',
    }],
  }],
  code: { coding: [code] },
  subject: { reference: `Patient/${patient}` },
  ...extra,
});

const quantity = (value, unit = 'days', code = 'd') => ({
  value,
  ...(unit ? { unit, system: 'http://unitsofmeasure.org', code } : {}),
});
const daysCode = {
  system: 'https://ontobee.org/',
  code: 'NCIT_C156418',
  display: 'Days Between Birth and Diagnosis',
};
const stageCode = {
  system: 'http://snomed.info/sct',
  code: '1222593009',
  display: 'American Joint Committee on Cancer pathological stage group allowable value',
};
const methodCode = {
  system: 'https://ontobee.org/',
  code: 'NCIT_C177576',
  display: 'Diagnostic Method',
};

const observations = [
  observation('cda-mini-observation-001', 'cda-mini-patient-001', 'cda-mini-study-001', daysCode, {
    focus: [{ reference: 'Specimen/cda-mini-specimen-001' }],
    specimen: { reference: 'Specimen/cda-mini-specimen-001' },
    effectiveDateTime: '2021-01-02',
    valueQuantity: quantity(16447),
  }),
  observation('cda-mini-observation-002', 'cda-mini-patient-001', 'cda-mini-study-001', stageCode, {
    focus: [{ reference: 'Specimen/cda-mini-specimen-001' }],
    effectiveDateTime: '2021-01-03',
    valueCodeableConcept: { coding: [{ system: 'http://snomed.info/sct', code: '1222593009', display: 'Stage IIA' }] },
  }),
  observation('cda-mini-observation-003', 'cda-mini-patient-002', 'cda-mini-study-001', daysCode, {
    focus: [{ reference: 'Specimen/cda-mini-specimen-003' }],
    effectiveDateTime: '2021-02-01',
    valueQuantity: quantity(12000),
  }),
  observation('cda-mini-observation-004', 'cda-mini-patient-003', 'cda-mini-study-001', daysCode, {
    focus: [{ reference: 'Specimen/cda-mini-specimen-004' }],
    effectiveDateTime: '2021-03-01',
    valueQuantity: quantity(20000),
  }),
  observation('cda-mini-observation-005', 'cda-mini-patient-004', 'cda-mini-study-002', stageCode, {
    focus: [{ reference: 'Specimen/cda-mini-specimen-005' }],
    effectiveDateTime: '2021-04-01',
    valueCodeableConcept: { coding: [{ system: 'http://snomed.info/sct', code: '1222593009', display: 'Stage IA' }] },
  }),
  observation('cda-mini-observation-006', 'cda-mini-patient-005', 'cda-mini-study-002', methodCode, {
    focus: [{ reference: 'Specimen/cda-mini-specimen-006' }],
    effectiveDateTime: '2021-05-01',
    valueString: 'Surgical Resection',
  }),
  observation('cda-mini-observation-007', 'cda-mini-patient-006', 'cda-mini-study-002', daysCode, {
    effectiveDateTime: '2021-06-01',
    valueQuantity: quantity(8000),
  }),
  observation('cda-mini-observation-008', 'cda-mini-patient-001', 'cda-mini-study-001', {
    system: 'http://loinc.org',
    code: '81247-9',
    display: 'Master HL7 genetic variant reporting panel',
  }, {
    focus: [{ reference: 'Specimen/cda-mini-specimen-001' }],
    specimen: { reference: 'Specimen/cda-mini-specimen-001' },
    effectiveDateTime: '2021-01-04',
    component: [
      {
        code: { coding: [{ system: 'https://cda.readthedocs.io', code: 'specimen_type', display: 'specimen_type' }] },
        valueString: 'Tumor',
      },
      {
        code: { coding: [{ system: 'https://cda.readthedocs.io', code: 'primary_disease_type', display: 'primary_disease_type' }] },
        valueString: 'Lung Adenocarcinoma',
      },
    ],
  }),
  observation('cda-mini-observation-009', 'cda-mini-patient-002', 'cda-mini-study-001', daysCode, {
    effectiveDateTime: '2021-02-02',
    valueQuantity: quantity(999, null),
  }),
  observation('cda-mini-observation-010', 'cda-mini-patient-003', 'cda-mini-study-001', stageCode, {
    effectiveDateTime: '2021-03-02',
  }),
];

const group = (id, patients) => ({
  resourceType: 'Group',
  id,
  type: 'person',
  actual: true,
  member: patients.map((patient) => ({ entity: { reference: `Patient/${patient}` } })),
});
const groups = [
  group('cda-mini-group-001', ['cda-mini-patient-001', 'cda-mini-patient-002', 'cda-mini-patient-003']),
  group('cda-mini-group-002', ['cda-mini-patient-004', 'cda-mini-patient-005', 'cda-mini-patient-006']),
];

const write = (name, rows) => {
  writeFileSync(new URL(name, ROOT), `${rows.map((row) => JSON.stringify(row)).join('\n')}\n`);
};

write('Patient.ndjson', patients);
write('ResearchStudy.ndjson', studies);
write('ResearchSubject.ndjson', researchSubjects);
write('Specimen.ndjson', specimens);
write('Observation.ndjson', observations);
write('Group.ndjson', groups);

console.log('wrote cda-mini-fixture', {
  patients: patients.length,
  studies: studies.length,
  researchSubjects: researchSubjects.length,
  specimens: specimens.length,
  observations: observations.length,
  groups: groups.length,
});
