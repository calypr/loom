# Related Expand field lifecycle fixture

This isolated Basic fixture has two Observation roots. Both follow
`Observation.subject_Patient` to a populated Patient record. The
`field-lifecycle-observation-matched` row reaches two MedicationAdministration records through the reverse
`MedicationAdministration.subject_Patient` relationship; the
`field-lifecycle-observation-unmatched` row reaches none.

The matched Patient has `gender=female` and `birthDate=1980-04-03`; the
unmatched Patient has `gender=male` and `birthDate=1990-02-10`. Both fields
are present on both Patients, so a related-source field added after the row
expansion and a preserving filter can be checked independently for matched and
unmatched MedicationAdministration rows.

The lifecycle carries `Observation.status` (`final` / `preliminary`), adds the
related `MedicationAdministration.status` values (`completed` / `in-progress`),
and groups by the public Observation status column. With parent preservation,
the exact expanded rows are:

| Observation status | MedicationAdministration status | Patient gender | Patient birthDate |
| --- | --- | --- | --- |
| final | completed | female | 1980-04-03 |
| final | in-progress | female | 1980-04-03 |
| preliminary | null | male | 1990-02-10 |

Grouping by Observation status therefore yields `final: 2` and `preliminary: 1`.
The fixture is synthetic Basic data and makes no claim about a loaded CDA dataset.
