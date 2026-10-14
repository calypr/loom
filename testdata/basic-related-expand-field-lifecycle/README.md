# Related Expand field lifecycle fixture

This isolated Basic fixture has two Observation roots. Both follow
`Observation.subject_Patient` to a Patient. The `field-lifecycle-observation-matched`
row reaches two MedicationAdministration records through the reverse
`MedicationAdministration.subject_Patient` relationship; the
`field-lifecycle-observation-unmatched` row reaches none.

The lifecycle carries `Observation.status` (`final` / `preliminary`), adds the
related `MedicationAdministration.status` values (`completed` / `in-progress`),
and groups by the public Observation status column. With parent preservation,
the exact expanded rows are:

| Observation status | MedicationAdministration status |
| --- | --- |
| final | completed |
| final | in-progress |
| preliminary | null |

Grouping by Observation status therefore yields `final: 2` and `preliminary: 1`.
The fixture is synthetic Basic data and makes no claim about a loaded CDA dataset.
