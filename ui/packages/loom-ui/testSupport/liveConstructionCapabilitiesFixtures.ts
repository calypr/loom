export const liveFilterSourceUnmaterializedProvenance = {
  "capturedAt": "2026-11-02",
  "capturePath": "/tmp/opencode/rso-journey/journey-12.net.json",
  "endpoint": "POST .../authoring/v2/construction-capabilities (stageId=source_projection, HTTP 200)",
  "project": "loom_dev_verify_rso-journey",
  "explorerId": "loom-dev-bootstrap",
  "outputId": "out_c6e21c8f31fe10937fb1e634",
  "snapshotToken": "sha256:b7b1352c74b184235ee44ba39de06913594eb30de3d0c5cf202928dab9212112",
  "draftVersion": 2,
  "draftDigest": "sha256:8274b83e6a5552a355507d12de7a3742669a2a99df93aa6cac0ef48406a177c6",
  "choiceCount": 4,
  "unmaterializedChoiceCount": 4,
  "serverSource": "internal/explorer/lifecycle/filter_source.go emits ColumnID=\"\" while no materialization exists"
} as const;

export const liveFilterSourceUnmaterializedResponseJson = `{
  "baseConstruction": {
    "steps": [],
    "version": 1
  },
  "draftDigest": "sha256:8274b83e6a5552a355507d12de7a3742669a2a99df93aa6cac0ef48406a177c6",
  "draftVersion": 2,
  "filterSourceInput": {
    "choices": [
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6ImJpcnRoRGF0ZSIsImZoaXJUeXBlIjoiZGF0ZSIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfQ.68265ca7c4d119a50f8ba78c1180375458c630dedf4bce3af1192d91e94d66c1",
        "columnId": "",
        "fieldPath": "birthDate",
        "isPopulated": true,
        "label": "Patient.birthDate",
        "logicalType": "date"
      },
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6ImdlbmRlciIsImZoaXJUeXBlIjoic3RyaW5nIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9.600c609c72cde333edadfda1829f97d84807732fb8193a6b029c5e3ec0275b86",
        "columnId": "",
        "fieldPath": "gender",
        "isPopulated": true,
        "label": "Patient.gender",
        "logicalType": "string"
      },
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6ImlkIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0.5a82184456c134b8f14e3fdb7e9e0bb629fa547cacd5a7ef9855c5e84589148a",
        "columnId": "",
        "fieldPath": "id",
        "isPopulated": true,
        "label": "Patient.id",
        "logicalType": "string"
      },
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6Im1ldGEubGFzdFVwZGF0ZWQiLCJmaGlyVHlwZSI6ImRhdGVUaW1lIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9.f11807baab8bea27764a7cba524bc0f72c950ab9e59b9529d30251b0b6d487fa",
        "columnId": "",
        "fieldPath": "meta.lastUpdated",
        "isPopulated": true,
        "label": "Patient.meta.lastUpdated",
        "logicalType": "date_time"
      }
    ],
    "stageId": "source_projection",
    "supported": true
  },
  "groupFieldChoices": [
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6ZWYzMjE4ODMzNjMxODMwNWI2NTAyZjBlMDhhYTM4ZTU2OGNiOWUwYzY3YjM1YmJkZTMxM2IxYjk1MTNmMDg1NiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJpbnRlZ2VyIiwiY2FuZGlkYXRlSWQiOiJjXzY0YTUwNjUyOGQ3OTk0ZDQ3YzFkYWMzNCIsImNhbm9uaWNhbFBhdGgiOiJ2YWx1ZUludGVnZXIiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwiZmhpclR5cGUiOiJpbnRlZ2VyIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9LCJyb3V0ZSI6W3siZWRnZUlkIjoiZV9jNjM1YmNkNmM0MmY0YzBiN2E1MDQ0Y2QiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJ0b05vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwiZnJvbVJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJ0b1Jlc291cmNlVHlwZSI6Ik9ic2VydmF0aW9uIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.441dc691f71b9a0a021b3c7349c2538f4c7951e1d0e86f3d33d7181cedec234a"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:ef32188336318305b6502f0e08aa38e568cb9e0c67b35bbde313b1b9513f0856",
      "candidateId": "c_64a506528d7994d47c1dac34",
      "label": "Actual result",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "integer",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "integer",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "valueInteger",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6ZGU5ZTFiOGJiMTVhNjZiMWZlNWJkNjc4ZDJiYmRjZmRmYTRjMzk5YmFkOWVjY2RjMjc4YWM5NWMzMTA3MDYxMSIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJkYXRlVGltZSIsImNhbmRpZGF0ZUlkIjoiY19iYmY5ZjIxYTliMGZmNDk4ZmQwNzg0OTQiLCJjYW5vbmljYWxQYXRoIjoiZWZmZWN0aXZlRGF0ZVRpbWUiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwiZmhpclR5cGUiOiJkYXRlVGltZSIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJPYnNlcnZhdGlvbiIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.af0240b1914ca717c6ffd62d4fb8b37c9e9195fd43f9dcf1ea7c345a450f33a7"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:de9e1b8bb15a66b1fe5bd678d2bbdcfdfa4c399bad9eccdc278ac95c31070611",
      "candidateId": "c_bbf9f21a9b0ff498fd078494",
      "label": "Clinically relevant time/time-period for observation",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "date_time",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "dateTime",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "effectiveDateTime",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6YzA3NDcyODJlNzY0ZDBiY2NhYjMyOTBlY2MyNGNjMmEzZDg3OWYxNmU0OWU5MWQ2MzA5NDRiY2JiYWIwODM0NCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCJ9LHsic3RlcEluZGV4IjoxLCJmcm9tT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwiZnJvbU5vZGVJZCI6Im5fYTBhMWUyMjhkOGYwY2M4NjM1NzBmY2E4IiwiZnJvbVJlc291cmNlVHlwZSI6IkRpYWdub3N0aWNSZXBvcnQiLCJlZGdlSWQiOiJlXzIxMzAzYTRiNjkzZTcxODlkNTllNDE0YiIsInJlbGF0aW9uc2hpcCI6InJlc3VsdF9EaWFnbm9zdGljUmVwb3J0Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjdiZmQzZjY2ZDJkMzJkNzIwNjZkNDM4YTA2YmYyYmNiIiwidG9Ob2RlSWQiOiJuX2QxNDhiNGQ5MzRjYzg5MThlMDVkZGU4NyIsInRvUmVzb3VyY2VUeXBlIjoiUmVzZWFyY2hTdHVkeSJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjdiZmQzZjY2ZDJkMzJkNzIwNjZkNDM4YTA2YmYyYmNiIiwidGFyZ2V0Tm9kZUlkIjoibl9kMTQ4YjRkOTM0Y2M4OTE4ZTA1ZGRlODciLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJSZXNlYXJjaFN0dWR5IiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjX2U0NDA5NmI3NDhjNDI2Y2Y4MTlmYTM5MSIsImNhbm9uaWNhbFBhdGgiOiJ0aXRsZSIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl9kMTQ4YjRkOTM0Y2M4OTE4ZTA1ZGRlODciLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9LHsiZWRnZUlkIjoiZV8yMTMwM2E0YjY5M2U3MTg5ZDU5ZTQxNGIiLCJmcm9tTm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJ0b05vZGVJZCI6Im5fZDE0OGI0ZDkzNGNjODkxOGUwNWRkZTg3IiwiZnJvbVJlc291cmNlVHlwZSI6IkRpYWdub3N0aWNSZXBvcnQiLCJ0b1Jlc291cmNlVHlwZSI6IlJlc2VhcmNoU3R1ZHkiLCJyZWxhdGlvbnNoaXAiOiJyZXN1bHRfRGlhZ25vc3RpY1JlcG9ydCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.ff4044b57fd68f74742c6b7a74ac609e911de4ae879c371a41669c4f3fc1ed18"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:c0747282e764d0bccab3290ecc24cc2a3d879f16e49e91d630944bcbbab08344",
      "candidateId": "c_e44096b748c426cf819fa391",
      "label": "Human readable name of the study",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 result_DiagnosticReport \u2192 ResearchStudy (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:7bfd3f66d2d32d72066d438a06bf2bcb",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:7bfd3f66d2d32d72066d438a06bf2bcb",
      "ownerResourceType": "ResearchStudy",
      "canonicalPath": "title",
      "relatedOccurrence": {
        "routeId": "related-route-v1:b0db7a56ad28f9a4130de7f6efd715cad40db5f2fb2be404eee21ed04e8e0529",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 result_DiagnosticReport \u2192 ResearchStudy (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6ODY2M2QwODM1NTU3ZDQwMjA0ZTRkMTVkNmFiMTg5MGYwNzI0YWI5M2IxODY0ODU1OTJmNGI0MzZhOWM5NzRhMiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifSx7InN0ZXBJbmRleCI6MSwiZnJvbU9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwiLCJ0b09jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToyNDBiOGFhOWI3NDI3MGFiZWIxMzQ5M2EwYjhmNmE0MyIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIn0seyJzdGVwSW5kZXgiOjIsImZyb21PY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6MjQwYjhhYTliNzQyNzBhYmViMTM0OTNhMGI4ZjZhNDMiLCJmcm9tTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiU3BlY2ltZW4iLCJlZGdlSWQiOiJlXzQzOTQxN2Y0NjQ0MWEyMjNkMzUzM2MwMCIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfU3BlY2ltZW4iLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIiwidG9PY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6YzY2MzQ1YzNkZGI5MTNiNTdhMjg4MGI0MzZkNTkxNzAiLCJ0b05vZGVJZCI6Im5fMjcxNmM0ZTYzNWZmOTNlNDRjYTkzM2RmIiwidG9SZXNvdXJjZVR5cGUiOiJEb2N1bWVudFJlZmVyZW5jZSJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOmM2NjM0NWMzZGRiOTEzYjU3YTI4ODBiNDM2ZDU5MTcwIiwidGFyZ2V0Tm9kZUlkIjoibl8yNzE2YzRlNjM1ZmY5M2U0NGNhOTMzZGYiLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJEb2N1bWVudFJlZmVyZW5jZSIsInJlc291cmNlVHlwZSI6InN0cmluZyIsImNhbmRpZGF0ZUlkIjoiY180OWE0MTQwYTE5NDUyYzVmODY1NGE1ZGIiLCJjYW5vbmljYWxQYXRoIjoiZGVzY3JpcHRpb24iLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fMjcxNmM0ZTYzNWZmOTNlNDRjYTkzM2RmIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlXzJmMzA4ZjExODRmNzMxNmZhOTlkMGZiNSIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl80NjczNmM3ZTZkMGM5NzgyZjUyNGE0ZDIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZWxhdGlvbnNoaXAiOiJtZW1iZXJfZW50aXR5X1BhdGllbnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn0seyJlZGdlSWQiOiJlX2I0NTY4YzhmM2E0MWNmMGY1MjhkZTJjZSIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifSx7ImVkZ2VJZCI6ImVfNDM5NDE3ZjQ2NDQxYTIyM2QzNTMzYzAwIiwiZnJvbU5vZGVJZCI6Im5fYzgyMGU2NDljMTY4Njc5MWFiMjBhMzEyIiwidG9Ob2RlSWQiOiJuXzI3MTZjNGU2MzVmZjkzZTQ0Y2E5MzNkZiIsImZyb21SZXNvdXJjZVR5cGUiOiJTcGVjaW1lbiIsInRvUmVzb3VyY2VUeXBlIjoiRG9jdW1lbnRSZWZlcmVuY2UiLCJyZWxhdGlvbnNoaXAiOiJzdWJqZWN0X1NwZWNpbWVuIiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.e2c7284bd4b5b41c687115496d7994e1398c4d6d4a51c4a2906861c516eaf323"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:8663d0835557d40204e4d15d6ab1890f0724ab93b186485592f4b436a9c974a2",
      "candidateId": "c_49a4140a19452c5f8654a5db",
      "label": "Human-readable description",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 subject_Specimen \u2192 DocumentReference (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:c66345c3ddb913b57a2880b436d59170",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:c66345c3ddb913b57a2880b436d59170",
      "ownerResourceType": "DocumentReference",
      "canonicalPath": "description",
      "relatedOccurrence": {
        "routeId": "related-route-v1:8a4522e226456d4319418de86bd1bd36ba1432f68002db15c85050f15151b0fe",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 subject_Specimen \u2192 DocumentReference (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6ZGQxZjhmMjQyOWM4YzA3ZTQ1MjU1OTdlYzA5ZjQyN2QzZThlNzViZWQ3ZWQ2ZWIxODAzMTNkYjNhOTA0YTYyMCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifSx7InN0ZXBJbmRleCI6MSwiZnJvbU9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwiLCJ0b09jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToyNDBiOGFhOWI3NDI3MGFiZWIxMzQ5M2EwYjhmNmE0MyIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIn1dLCJ0YXJnZXRPY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6MjQwYjhhYTliNzQyNzBhYmViMTM0OTNhMGI4ZjZhNDMiLCJ0YXJnZXROb2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsInRhcmdldFJlc291cmNlVHlwZSI6IlNwZWNpbWVuIiwicmVzb3VyY2VUeXBlIjoiQ29kaW5nIiwiZW1wdHlQb2xpY3kiOiJQUkVTRVJWRV9QQVJFTlQiLCJtdWx0aXBsaWNpdHkiOiJESVNUSU5DVF9UQVJHRVRfUEVSX0lOUFVUIiwiY29kaW5nU2VsZWN0b3IiOiJ0eXBlLmNvZGluZ1tdIiwiY29kaW5nRW1wdHlQb2xpY3kiOiJHUk9VUCIsIm9yZGluYWxTZW1hbnRpY3MiOiJaRVJPX0JBU0VEX1BPU0lUSU9OX0lOX0ZMQVRURU5FRF9FWFBBTlNJT05fQVJSQVkifX0sInJvd0Nob2ljZUlkIjoicmMxLmV5SjJaWEp6YVc5dUlqb2ljbTkzTFdOb2IybGpaUzkyTVNJc0ltdHBibVFpT2lKRldGQkJUa1JGUkY5VFEwOVFSU0lzSW5OdVlYQnphRzkwVkc5clpXNGlPaUp6YUdFeU5UWTZZamRpTVRNMU1tTTNOR0l4T0RReU16VmxaVFEwWW1Fek9XUmxNRFk1TVRNMU9UUmxZak13WkdVelpEQmpOV05tTWpBeU9USTRaR0ZpT1RJeE1qRXhNaUlzSW5OamFHVnRZVVJwWjJWemRDSTZJamxsT1RZd09UTTJNelEzTW1JelpHSmxPREkyTXpGak4yVmhaamMyWXpRelpUTmlPV1EzTURrek1HTm1OekppTWpBMU1tWTJaVFZtTXpZd1ltUTBaV1VpTENKdlkyTjFjbkpsYm1ObElqcDdJazlqWTNWeWNtVnVZMlZKUkNJNkluSmxiR0YwWldRdGIyTmpkWEp5Wlc1alpTMTJNVG95TkRCaU9HRmhPV0kzTkRJM01HRmlaV0l4TXpRNU0yRXdZamhtTm1FME15SXNJazV2WkdWSlJDSTZJbTVmWXpneU1HVTJORGxqTVRZNE5qYzVNV0ZpTWpCaE16RXlJaXdpVW1WemIzVnlZMlZVZVhCbElqb2lVM0JsWTJsdFpXNGlMQ0pTYjNWMFpTSTZXM3NpWldSblpVbGtJam9pWlY4eVpqTXdPR1l4TVRnMFpqY3pNVFptWVRrNVpEQm1ZalVpTENKbWNtOXRUbTlrWlVsa0lqb2libDgzWlRNMVlXTTRNVGN6WVROaU9ERTFPV0UwWWpaalpqRWlMQ0owYjA1dlpHVkpaQ0k2SW01Zk5EWTNNelpqTjJVMlpEQmpPVGM0TW1ZMU1qUmhOR1F5SWl3aVpuSnZiVkpsYzI5MWNtTmxWSGx3WlNJNklsQmhkR2xsYm5RaUxDSjBiMUpsYzI5MWNtTmxWSGx3WlNJNklrZHliM1Z3SWl3aWNtVnNZWFJwYjI1emFHbHdJam9pYldWdFltVnlYMlZ1ZEdsMGVWOVFZWFJwWlc1MElpd2ljM1J2Y21GblpVUnBjbVZqZEdsdmJpSTZJa2xPUWs5VlRrUWlMQ0p0WVhSamFFMXZaR1VpT2lKUFVGUkpUMDVCVENKOUxIc2laV1JuWlVsa0lqb2laVjlpTkRVMk9HTTRaak5oTkRGalpqQm1OVEk0WkdVeVkyVWlMQ0ptY205dFRtOWtaVWxrSWpvaWJsODBOamN6Tm1NM1pUWmtNR001TnpneVpqVXlOR0UwWkRJaUxDSjBiMDV2WkdWSlpDSTZJbTVmWXpneU1HVTJORGxqTVRZNE5qYzVNV0ZpTWpCaE16RXlJaXdpWm5KdmJWSmxjMjkxY21ObFZIbHdaU0k2SWtkeWIzVndJaXdpZEc5U1pYTnZkWEpqWlZSNWNHVWlPaUpUY0dWamFXMWxiaUlzSW5KbGJHRjBhVzl1YzJocGNDSTZJbk4xWW1wbFkzUmZSM0p2ZFhBaUxDSnpkRzl5WVdkbFJHbHlaV04wYVc5dUlqb2lTVTVDVDFWT1JDSXNJbTFoZEdOb1RXOWtaU0k2SWs5UVZFbFBUa0ZNSW4xZGZTd2ljR0YwYUNJNkluUjVjR1V1WTI5a2FXNW5XMTBpTENKbWFHbHlWSGx3WlNJNklrTnZaR2x1WnlJc0ltTmhjbVJwYm1Gc2FYUjVJam9pVFVGT1dTSXNJbk5vWVhCbElqb2lRVkpTUVZraUxDSnlaV1psY21WdVkyVWlPbVpoYkhObGZRLjE1ZjEyMWE5OTFjYjhiYWVhYjNmMGZlOTI3ZTUxZTRjYTA5OGMzNTI0MzRmYzRjNTZlNTAyOGI0OWNjMGVlMjMiLCJ2YWx1ZUtpbmQiOiJDT0RJTkdfSURFTlRJVFkiLCJub2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsImZoaXJUeXBlIjoiQ29kaW5nIiwiY2FyZGluYWxpdHkiOiJNQU5ZIiwic2hhcGUiOiJDT0RJTkdfSURFTlRJVFkiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJHcm91cCIsInJlbGF0aW9uc2hpcCI6Im1lbWJlcl9lbnRpdHlfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifSx7ImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwiZnJvbU5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwidG9Ob2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsInRvUmVzb3VyY2VUeXBlIjoiU3BlY2ltZW4iLCJyZWxhdGlvbnNoaXAiOiJzdWJqZWN0X0dyb3VwIiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.657bbfeeb8117aa794e2546f62b00254b31f80c6b8a95d022db6bf6237f9fa03"
      },
      "valueKind": "CODING_IDENTITY",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:dd1f8f2429c8c07e4525597ec09f427d3e8e75bed7ed6eb180313db3a904a620",
      "label": "Kind of material that forms the specimen",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:240b8aa9b74270abeb13493a0b8f6a43",
      "resourceType": "Coding",
      "ownerOccurrenceId": "related-occurrence-v1:240b8aa9b74270abeb13493a0b8f6a43",
      "ownerResourceType": "Specimen",
      "canonicalPath": "type.coding[]",
      "relatedOccurrence": {
        "routeId": "related-route-v1:d35851cff28f9f8dd5faaeadb55ac53f150dbcc7874b2436f6134424e13f57a1",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6MTEyNDdkYTE2NTkwZGExMWVlMDIwNTdlNWI4MTFkMjA1ZTYxYmJjZGI3OTNlNzU5ZTQwMDc2MmIwMDJjMjBkNCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsInRhcmdldE5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfMzRlNzg0OTA3NzdlMjM4ZmExM2M5OTA4IiwiY2Fub25pY2FsUGF0aCI6Im5hbWUiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlXzJmMzA4ZjExODRmNzMxNmZhOTlkMGZiNSIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl80NjczNmM3ZTZkMGM5NzgyZjUyNGE0ZDIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZWxhdGlvbnNoaXAiOiJtZW1iZXJfZW50aXR5X1BhdGllbnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn1dfQ.ccfcb4c46f56647de00cbd30e927c334817741581b6d23082e9c520fd3c62c0d"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:11247da16590da11ee02057e5b811d205e61bbcdb793e759e400762b002c20d4",
      "candidateId": "c_34e78490777e238fa13c9908",
      "label": "Label for Group",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:9fe14187ab1c954417b9eb6ec9b3e7bf",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:9fe14187ab1c954417b9eb6ec9b3e7bf",
      "ownerResourceType": "Group",
      "canonicalPath": "name",
      "relatedOccurrence": {
        "routeId": "related-route-v1:72ab7fffd53ee431b8f711ab7c9df8dd2f9bcc4a44fac990dff8526feaa7b91a",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6MWE0OTIzZTI3NTc5OTA3NDhlOTM0NzQ4ZTEzYzUyZjU2YjkzM2I4NDM5N2I1YjdmYzAxMWY4YmY2MTA3OWM0YiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfZWVjMmMyOTYwMTI2ZjM2NWI2MjIwMDcyIiwiY2Fub25pY2FsUGF0aCI6InNwZWNpbWVuLnJlZmVyZW5jZSIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl8wYWFmMTVlOGQyNDZiNTFmODI1NzNkNmQiLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJPYnNlcnZhdGlvbiIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.047ea6fa7841f2a04fdc0991906600c7a0a8f798d0a42a4baf12561bdab3cf49"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:1a4923e2757990748e934748e13c52f56b933b84397b5b7fc011f8bf61079c4b",
      "candidateId": "c_eec2c2960126f365b6220072",
      "label": "Literal reference, Relative, internal or absolute URL",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "specimen.reference",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6NTJjOTczMmNjZjRjYTAyYmQwNTU2OTA0MjgzMTJjNjcxMGFiY2JmZDc2NzY5MTRiMzRlOTZlNjkyMTZjNDY2OCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidGFyZ2V0Tm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjX2ZiZTA3OTMyZDU3N2Y3ZGE2NzAxNGYxZSIsImNhbm9uaWNhbFBhdGgiOiJzdWJqZWN0LnJlZmVyZW5jZSIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.450c806ebf0940d810bb6a0245a05bd984a35c4968a7ffb79cb557a053f284e5"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:52c9732ccf4ca02bd055690428312c6710abcbfd7676914b34e96e69216c4668",
      "candidateId": "c_fbe07932d577f7da67014f1e",
      "label": "Literal reference, Relative, internal or absolute URL",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:2f5fb4d05061e79a677e2ca7078c8d89",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:2f5fb4d05061e79a677e2ca7078c8d89",
      "ownerResourceType": "DiagnosticReport",
      "canonicalPath": "subject.reference",
      "relatedOccurrence": {
        "routeId": "related-route-v1:f457fa79bf98ad8128157c0a6c7028c0aae5a39fc60770b2f8c5c227b45e438a",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6MTBkMTVlOTBhOWVjZTdiZjliYzQ1MzQ3MWViMGQ2YjU5MjdlNWFhMTVlNTFjNmIwOGI0YTdmMzI1NDdlOWViYSIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifSx7InN0ZXBJbmRleCI6MSwiZnJvbU9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwiLCJ0b09jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToyNDBiOGFhOWI3NDI3MGFiZWIxMzQ5M2EwYjhmNmE0MyIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIn0seyJzdGVwSW5kZXgiOjIsImZyb21PY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6MjQwYjhhYTliNzQyNzBhYmViMTM0OTNhMGI4ZjZhNDMiLCJmcm9tTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiU3BlY2ltZW4iLCJlZGdlSWQiOiJlXzQzOTQxN2Y0NjQ0MWEyMjNkMzUzM2MwMCIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfU3BlY2ltZW4iLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIiwidG9PY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6YzY2MzQ1YzNkZGI5MTNiNTdhMjg4MGI0MzZkNTkxNzAiLCJ0b05vZGVJZCI6Im5fMjcxNmM0ZTYzNWZmOTNlNDRjYTkzM2RmIiwidG9SZXNvdXJjZVR5cGUiOiJEb2N1bWVudFJlZmVyZW5jZSJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOmM2NjM0NWMzZGRiOTEzYjU3YTI4ODBiNDM2ZDU5MTcwIiwidGFyZ2V0Tm9kZUlkIjoibl8yNzE2YzRlNjM1ZmY5M2U0NGNhOTMzZGYiLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJEb2N1bWVudFJlZmVyZW5jZSIsInJlc291cmNlVHlwZSI6InN0cmluZyIsImNhbmRpZGF0ZUlkIjoiY182OTZhZTA2OWMxMTE3Y2RlZjhjNDM2MmEiLCJjYW5vbmljYWxQYXRoIjoic3ViamVjdC5yZWZlcmVuY2UiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fMjcxNmM0ZTYzNWZmOTNlNDRjYTkzM2RmIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlXzJmMzA4ZjExODRmNzMxNmZhOTlkMGZiNSIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl80NjczNmM3ZTZkMGM5NzgyZjUyNGE0ZDIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZWxhdGlvbnNoaXAiOiJtZW1iZXJfZW50aXR5X1BhdGllbnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn0seyJlZGdlSWQiOiJlX2I0NTY4YzhmM2E0MWNmMGY1MjhkZTJjZSIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifSx7ImVkZ2VJZCI6ImVfNDM5NDE3ZjQ2NDQxYTIyM2QzNTMzYzAwIiwiZnJvbU5vZGVJZCI6Im5fYzgyMGU2NDljMTY4Njc5MWFiMjBhMzEyIiwidG9Ob2RlSWQiOiJuXzI3MTZjNGU2MzVmZjkzZTQ0Y2E5MzNkZiIsImZyb21SZXNvdXJjZVR5cGUiOiJTcGVjaW1lbiIsInRvUmVzb3VyY2VUeXBlIjoiRG9jdW1lbnRSZWZlcmVuY2UiLCJyZWxhdGlvbnNoaXAiOiJzdWJqZWN0X1NwZWNpbWVuIiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.bcc5ef4944424ce9967fa5df56c04a51f8b4ada3fe56def6ed9d400723f8dbe5"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:10d15e90a9ece7bf9bc453471eb0d6b5927e5aa15e51c6b08b4a7f32547e9eba",
      "candidateId": "c_696ae069c1117cdef8c4362a",
      "label": "Literal reference, Relative, internal or absolute URL",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 subject_Specimen \u2192 DocumentReference (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:c66345c3ddb913b57a2880b436d59170",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:c66345c3ddb913b57a2880b436d59170",
      "ownerResourceType": "DocumentReference",
      "canonicalPath": "subject.reference",
      "relatedOccurrence": {
        "routeId": "related-route-v1:8a4522e226456d4319418de86bd1bd36ba1432f68002db15c85050f15151b0fe",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 subject_Specimen \u2192 DocumentReference (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6ZDdhOGEwNjM1YzRiOGU2MzJjNGMzMWFkZTMzZWJhMzIzOWEzMmZkMzJkNDgyZjZjODQwMWFkNGNlMTQ4ZDhjZSIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfYTM1ZTVkMTYyMWVlYWVhMjE4MzFhYjBhIiwiY2Fub25pY2FsUGF0aCI6InN1YmplY3QucmVmZXJlbmNlIiwiZW1wdHlQb2xpY3kiOiJQUkVTRVJWRV9QQVJFTlQiLCJtdWx0aXBsaWNpdHkiOiJESVNUSU5DVF9UQVJHRVRfUEVSX0lOUFVUIn19LCJ2YWx1ZUtpbmQiOiJTQ0FMQVIiLCJub2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsImZoaXJUeXBlIjoic3RyaW5nIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9LCJyb3V0ZSI6W3siZWRnZUlkIjoiZV9jNjM1YmNkNmM0MmY0YzBiN2E1MDQ0Y2QiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJ0b05vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwiZnJvbVJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJ0b1Jlc291cmNlVHlwZSI6Ik9ic2VydmF0aW9uIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.9ab06840c17b20a0c86c654af015575a5fc029789fcb113658536054061f8a2d"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:d7a8a0635c4b8e632c4c31ade33eba3239a32fd32d482f6c8401ad4ce148d8ce",
      "candidateId": "c_a35e5d1621eeaea21831ab0a",
      "label": "Literal reference, Relative, internal or absolute URL",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "subject.reference",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6NzFmNjE1ZmFlODViMGNmMzU2Yjc2YzY0Y2E4MzQ0ZDc1MjUxOTIzNWFkZjZiY2ZlZjY3NDA0OGY4YjdhYjcyNSIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifSx7InN0ZXBJbmRleCI6MSwiZnJvbU9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwiLCJ0b09jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToyNDBiOGFhOWI3NDI3MGFiZWIxMzQ5M2EwYjhmNmE0MyIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIn1dLCJ0YXJnZXRPY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6MjQwYjhhYTliNzQyNzBhYmViMTM0OTNhMGI4ZjZhNDMiLCJ0YXJnZXROb2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsInRhcmdldFJlc291cmNlVHlwZSI6IlNwZWNpbWVuIiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjXzVhY2QwZjM3YzRiNDU0N2U2NmQ3ODExZSIsImNhbm9uaWNhbFBhdGgiOiJzdWJqZWN0LnJlZmVyZW5jZSIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJHcm91cCIsInJlbGF0aW9uc2hpcCI6Im1lbWJlcl9lbnRpdHlfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifSx7ImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwiZnJvbU5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwidG9Ob2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsInRvUmVzb3VyY2VUeXBlIjoiU3BlY2ltZW4iLCJyZWxhdGlvbnNoaXAiOiJzdWJqZWN0X0dyb3VwIiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.d68debb4548909e5c5fc86e09dfa4229b5c0c729b240d9aaac1c410b70314ec5"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:71f615fae85b0cf356b76c64ca8344d752519235adf6bcfef674048f8b7ab725",
      "candidateId": "c_5acd0f37c4b4547e66d7811e",
      "label": "Literal reference, Relative, internal or absolute URL",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:240b8aa9b74270abeb13493a0b8f6a43",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:240b8aa9b74270abeb13493a0b8f6a43",
      "ownerResourceType": "Specimen",
      "canonicalPath": "subject.reference",
      "relatedOccurrence": {
        "routeId": "related-route-v1:d35851cff28f9f8dd5faaeadb55ac53f150dbcc7874b2436f6134424e13f57a1",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6MDZlYjVhNWFiNDY5MjI5NDYyMjg0NGNlNDMxNGM5YjNlZjI3NDU5NmFiYWEyMjEwNjg3YzZmZGVlZDQ5YTBhNyIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidGFyZ2V0Tm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjX2I2MDFkMGZkZDIxMDhjMDY0NzQ2NzcwYyIsImNhbm9uaWNhbFBhdGgiOiJpZCIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.a200de971bddb8e08f1abbaa77a9c7b3d5d2cd3be2f14ff49d9cbca2f934941d"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:06eb5a5ab4692294622844ce4314c9b3ef274596abaa2210687c6fdeed49a0a7",
      "candidateId": "c_b601d0fdd2108c064746770c",
      "label": "Logical id of this artifact",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:2f5fb4d05061e79a677e2ca7078c8d89",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:2f5fb4d05061e79a677e2ca7078c8d89",
      "ownerResourceType": "DiagnosticReport",
      "canonicalPath": "id",
      "relatedOccurrence": {
        "routeId": "related-route-v1:f457fa79bf98ad8128157c0a6c7028c0aae5a39fc60770b2f8c5c227b45e438a",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6NTNmMTQ5MjMxMmQ5MWNlN2Y5Mzc2MGZlMGJjZTdjNDQ1NzUyOGIyYmEwMDE3NDQ3OGQ4NGI3OWI1YzUyNGRmYSIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifSx7InN0ZXBJbmRleCI6MSwiZnJvbU9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwiLCJ0b09jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToyNDBiOGFhOWI3NDI3MGFiZWIxMzQ5M2EwYjhmNmE0MyIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIn0seyJzdGVwSW5kZXgiOjIsImZyb21PY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6MjQwYjhhYTliNzQyNzBhYmViMTM0OTNhMGI4ZjZhNDMiLCJmcm9tTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiU3BlY2ltZW4iLCJlZGdlSWQiOiJlXzQzOTQxN2Y0NjQ0MWEyMjNkMzUzM2MwMCIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfU3BlY2ltZW4iLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIiwidG9PY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6YzY2MzQ1YzNkZGI5MTNiNTdhMjg4MGI0MzZkNTkxNzAiLCJ0b05vZGVJZCI6Im5fMjcxNmM0ZTYzNWZmOTNlNDRjYTkzM2RmIiwidG9SZXNvdXJjZVR5cGUiOiJEb2N1bWVudFJlZmVyZW5jZSJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOmM2NjM0NWMzZGRiOTEzYjU3YTI4ODBiNDM2ZDU5MTcwIiwidGFyZ2V0Tm9kZUlkIjoibl8yNzE2YzRlNjM1ZmY5M2U0NGNhOTMzZGYiLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJEb2N1bWVudFJlZmVyZW5jZSIsInJlc291cmNlVHlwZSI6InN0cmluZyIsImNhbmRpZGF0ZUlkIjoiY18zN2UyYmYwYzhlOTRiYzMwZmIwMzAyMDciLCJjYW5vbmljYWxQYXRoIjoiaWQiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fMjcxNmM0ZTYzNWZmOTNlNDRjYTkzM2RmIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlXzJmMzA4ZjExODRmNzMxNmZhOTlkMGZiNSIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl80NjczNmM3ZTZkMGM5NzgyZjUyNGE0ZDIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZWxhdGlvbnNoaXAiOiJtZW1iZXJfZW50aXR5X1BhdGllbnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn0seyJlZGdlSWQiOiJlX2I0NTY4YzhmM2E0MWNmMGY1MjhkZTJjZSIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifSx7ImVkZ2VJZCI6ImVfNDM5NDE3ZjQ2NDQxYTIyM2QzNTMzYzAwIiwiZnJvbU5vZGVJZCI6Im5fYzgyMGU2NDljMTY4Njc5MWFiMjBhMzEyIiwidG9Ob2RlSWQiOiJuXzI3MTZjNGU2MzVmZjkzZTQ0Y2E5MzNkZiIsImZyb21SZXNvdXJjZVR5cGUiOiJTcGVjaW1lbiIsInRvUmVzb3VyY2VUeXBlIjoiRG9jdW1lbnRSZWZlcmVuY2UiLCJyZWxhdGlvbnNoaXAiOiJzdWJqZWN0X1NwZWNpbWVuIiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.314a914edeb985b6ea01c8cd737e5348074e5820229fda9fc54dc5cfe830ac65"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:53f1492312d91ce7f93760fe0bce7c4457528b2ba00174478d84b79b5c524dfa",
      "candidateId": "c_37e2bf0c8e94bc30fb030207",
      "label": "Logical id of this artifact",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 subject_Specimen \u2192 DocumentReference (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:c66345c3ddb913b57a2880b436d59170",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:c66345c3ddb913b57a2880b436d59170",
      "ownerResourceType": "DocumentReference",
      "canonicalPath": "id",
      "relatedOccurrence": {
        "routeId": "related-route-v1:8a4522e226456d4319418de86bd1bd36ba1432f68002db15c85050f15151b0fe",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 subject_Specimen \u2192 DocumentReference (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6YjA1Zjc0ZWViNDU4NTZiY2ZjN2QzZjkyZWZhMzc2ODhlNDc1ZTJiMmQzOGM0ODM5OTcxM2FjMGIyOWZkMzMzOCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsInRhcmdldE5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfYzFjZDJkZjMzOTFkNGUwYjFkY2UyNjE4IiwiY2Fub25pY2FsUGF0aCI6ImlkIiwiZW1wdHlQb2xpY3kiOiJQUkVTRVJWRV9QQVJFTlQiLCJtdWx0aXBsaWNpdHkiOiJESVNUSU5DVF9UQVJHRVRfUEVSX0lOUFVUIn19LCJ2YWx1ZUtpbmQiOiJTQ0FMQVIiLCJub2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZoaXJUeXBlIjoic3RyaW5nIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9LCJyb3V0ZSI6W3siZWRnZUlkIjoiZV8yZjMwOGYxMTg0ZjczMTZmYTk5ZDBmYjUiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJ0b05vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwiZnJvbVJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJ0b1Jlc291cmNlVHlwZSI6Ikdyb3VwIiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.5cc0121d6bc0def4f4e9744c09fb80944b3a829de6c68bbec7ee454ff78640ef"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:b05f74eeb45856bcfc7d3f92efa37688e475e2b2d38c48399713ac0b29fd3338",
      "candidateId": "c_c1cd2df3391d4e0b1dce2618",
      "label": "Logical id of this artifact",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:9fe14187ab1c954417b9eb6ec9b3e7bf",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:9fe14187ab1c954417b9eb6ec9b3e7bf",
      "ownerResourceType": "Group",
      "canonicalPath": "id",
      "relatedOccurrence": {
        "routeId": "related-route-v1:72ab7fffd53ee431b8f711ab7c9df8dd2f9bcc4a44fac990dff8526feaa7b91a",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6YjQ0ZjljN2ViOTcyMDZlZTdjODJhZTI3YzY5MGY0NGIyOGYzNzQwYjgyMTYwYmQwOWQxYTBmODBmNTBkYTg0MCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfNDY5NjhiMzhkMzJjOGJhZjliMmM2ZDk2IiwiY2Fub25pY2FsUGF0aCI6ImlkIiwiZW1wdHlQb2xpY3kiOiJQUkVTRVJWRV9QQVJFTlQiLCJtdWx0aXBsaWNpdHkiOiJESVNUSU5DVF9UQVJHRVRfUEVSX0lOUFVUIn19LCJ2YWx1ZUtpbmQiOiJTQ0FMQVIiLCJub2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsImZoaXJUeXBlIjoic3RyaW5nIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9LCJyb3V0ZSI6W3siZWRnZUlkIjoiZV9jNjM1YmNkNmM0MmY0YzBiN2E1MDQ0Y2QiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJ0b05vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwiZnJvbVJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJ0b1Jlc291cmNlVHlwZSI6Ik9ic2VydmF0aW9uIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.12d5361fffe208f57425305184f1f0eeb5914086ea19a52a539bb4b4c5ab1d9f"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:b44f9c7eb97206ee7c82ae27c690f44b28f3740b82160bd09d1a0f80f50da840",
      "candidateId": "c_46968b38d32c8baf9b2c6d96",
      "label": "Logical id of this artifact",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "id",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6OTJkMzE5ZjcwZmU0ZDlmM2M3YWM5NGQ5NDUyYzljMjMxN2RmMzViMzgxNzMxYzk5NWViNzliNmJkZGYxMTllNiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCJ9LHsic3RlcEluZGV4IjoxLCJmcm9tT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwiZnJvbU5vZGVJZCI6Im5fYTBhMWUyMjhkOGYwY2M4NjM1NzBmY2E4IiwiZnJvbVJlc291cmNlVHlwZSI6IkRpYWdub3N0aWNSZXBvcnQiLCJlZGdlSWQiOiJlXzIxMzAzYTRiNjkzZTcxODlkNTllNDE0YiIsInJlbGF0aW9uc2hpcCI6InJlc3VsdF9EaWFnbm9zdGljUmVwb3J0Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjdiZmQzZjY2ZDJkMzJkNzIwNjZkNDM4YTA2YmYyYmNiIiwidG9Ob2RlSWQiOiJuX2QxNDhiNGQ5MzRjYzg5MThlMDVkZGU4NyIsInRvUmVzb3VyY2VUeXBlIjoiUmVzZWFyY2hTdHVkeSJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjdiZmQzZjY2ZDJkMzJkNzIwNjZkNDM4YTA2YmYyYmNiIiwidGFyZ2V0Tm9kZUlkIjoibl9kMTQ4YjRkOTM0Y2M4OTE4ZTA1ZGRlODciLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJSZXNlYXJjaFN0dWR5IiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjX2MwYmFjN2IxMzA4OTgzZDMzOTk2ZGE0YiIsImNhbm9uaWNhbFBhdGgiOiJpZCIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl9kMTQ4YjRkOTM0Y2M4OTE4ZTA1ZGRlODciLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9LHsiZWRnZUlkIjoiZV8yMTMwM2E0YjY5M2U3MTg5ZDU5ZTQxNGIiLCJmcm9tTm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJ0b05vZGVJZCI6Im5fZDE0OGI0ZDkzNGNjODkxOGUwNWRkZTg3IiwiZnJvbVJlc291cmNlVHlwZSI6IkRpYWdub3N0aWNSZXBvcnQiLCJ0b1Jlc291cmNlVHlwZSI6IlJlc2VhcmNoU3R1ZHkiLCJyZWxhdGlvbnNoaXAiOiJyZXN1bHRfRGlhZ25vc3RpY1JlcG9ydCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.3ff9cba95465456ffbb8f9bfc1a2c8ce7a306f8d04c7b12bc70cb0c067d8009a"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:92d319f70fe4d9f3c7ac94d9452c9c2317df35b381731c995eb79b6bddf119e6",
      "candidateId": "c_c0bac7b1308983d33996da4b",
      "label": "Logical id of this artifact",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 result_DiagnosticReport \u2192 ResearchStudy (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:7bfd3f66d2d32d72066d438a06bf2bcb",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:7bfd3f66d2d32d72066d438a06bf2bcb",
      "ownerResourceType": "ResearchStudy",
      "canonicalPath": "id",
      "relatedOccurrence": {
        "routeId": "related-route-v1:b0db7a56ad28f9a4130de7f6efd715cad40db5f2fb2be404eee21ed04e8e0529",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 result_DiagnosticReport \u2192 ResearchStudy (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6MGY0MTk3MjAxMDk1NTAyMDA1NTI4ZGJlMDgxZmVhNWRiMjI1YjY4NDdjOTg0ZjdmOTM4ZjI0YmEwZmMwNTQ2MSIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifSx7InN0ZXBJbmRleCI6MSwiZnJvbU9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwiLCJ0b09jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToyNDBiOGFhOWI3NDI3MGFiZWIxMzQ5M2EwYjhmNmE0MyIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIn1dLCJ0YXJnZXRPY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6MjQwYjhhYTliNzQyNzBhYmViMTM0OTNhMGI4ZjZhNDMiLCJ0YXJnZXROb2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsInRhcmdldFJlc291cmNlVHlwZSI6IlNwZWNpbWVuIiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjXzk1NjljMzViZmY5ZmUzNDM0OTc2NGY3NCIsImNhbm9uaWNhbFBhdGgiOiJpZCIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJHcm91cCIsInJlbGF0aW9uc2hpcCI6Im1lbWJlcl9lbnRpdHlfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifSx7ImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwiZnJvbU5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwidG9Ob2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsInRvUmVzb3VyY2VUeXBlIjoiU3BlY2ltZW4iLCJyZWxhdGlvbnNoaXAiOiJzdWJqZWN0X0dyb3VwIiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.6d49012a3a0659b7cd7bce6d3070b0b2b56809bde10e9a8f43a9a4edac7b0daa"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:0f4197201095502005528dbe081fea5db225b6847c984f7f938f24ba0fc05461",
      "candidateId": "c_9569c35bff9fe34349764f74",
      "label": "Logical id of this artifact",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:240b8aa9b74270abeb13493a0b8f6a43",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:240b8aa9b74270abeb13493a0b8f6a43",
      "ownerResourceType": "Specimen",
      "canonicalPath": "id",
      "relatedOccurrence": {
        "routeId": "related-route-v1:d35851cff28f9f8dd5faaeadb55ac53f150dbcc7874b2436f6134424e13f57a1",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "inputColumnId": "source_ef82eef4462a7f20fbcecfff",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtc291cmNlLXNoYXBlLXYxOmRhMDFhMDkzOGMyYTczMDU3YTViY2ZmYTk5MmY2M2ZjMDFhN2UxOGM4MjI5ZTBmMDQ5ZGUwNmVhOTc1ZTQxYmEiLCJzb3VyY2VJZGVudGl0eUNvbHVtbiI6Il9rZXkiLCJpbnB1dCI6eyJraW5kIjoiQ1VSUkVOVF9TVEFHRSIsImN1cnJlbnRTdGFnZSI6eyJzb3VyY2VSb2xlIjoiUk9PVCIsIm9jY3VycmVuY2VJZCI6ImJhc2UiLCJvd25lck9jY3VycmVuY2VJZCI6ImJhc2UiLCJvd25lclJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJyZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiY2FuZGlkYXRlSWQiOiJjXzJkMTNhZDMwMzE0Y2I5N2E1NTUzOWE3ZiIsImNhbm9uaWNhbFBhdGgiOiJpZCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOltdfQ.3c0306e3560e90b34339f05d4def962b1ca91beb458e21c9c46627f3872b9c0a"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-source-shape-v1:da01a0938c2a73057a5bcffa992f63fc01a7e18c8229e0f049de06ea975e41ba",
      "candidateId": "c_2d13ad30314cb97a55539a7f",
      "label": "Logical id of this artifact",
      "scopeLabel": "Patient \u00b7 root",
      "valueType": "string",
      "sourceRole": "ROOT",
      "occurrenceId": "base",
      "resourceType": "Patient",
      "ownerOccurrenceId": "base",
      "ownerResourceType": "Patient",
      "canonicalPath": "id"
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6NWNjM2UxMTFmYTUwMGQwNTk5ZjZhMmEyZmQ3Y2MzNDkzZjFkMGEwYzM4ZDQxYzQ1ZjFmMzZhMDVhNzAzYjBkMiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidGFyZ2V0Tm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVzb3VyY2VUeXBlIjoiQ29kaW5nIiwiZW1wdHlQb2xpY3kiOiJQUkVTRVJWRV9QQVJFTlQiLCJtdWx0aXBsaWNpdHkiOiJESVNUSU5DVF9UQVJHRVRfUEVSX0lOUFVUIiwiY29kaW5nU2VsZWN0b3IiOiJjb2RlLmNvZGluZ1tdIiwiY29kaW5nRW1wdHlQb2xpY3kiOiJHUk9VUCIsIm9yZGluYWxTZW1hbnRpY3MiOiJaRVJPX0JBU0VEX1BPU0lUSU9OX0lOX0ZMQVRURU5FRF9FWFBBTlNJT05fQVJSQVkifX0sInJvd0Nob2ljZUlkIjoicmMxLmV5SjJaWEp6YVc5dUlqb2ljbTkzTFdOb2IybGpaUzkyTVNJc0ltdHBibVFpT2lKRldGQkJUa1JGUkY5VFEwOVFSU0lzSW5OdVlYQnphRzkwVkc5clpXNGlPaUp6YUdFeU5UWTZZamRpTVRNMU1tTTNOR0l4T0RReU16VmxaVFEwWW1Fek9XUmxNRFk1TVRNMU9UUmxZak13WkdVelpEQmpOV05tTWpBeU9USTRaR0ZpT1RJeE1qRXhNaUlzSW5OamFHVnRZVVJwWjJWemRDSTZJamxsT1RZd09UTTJNelEzTW1JelpHSmxPREkyTXpGak4yVmhaamMyWXpRelpUTmlPV1EzTURrek1HTm1OekppTWpBMU1tWTJaVFZtTXpZd1ltUTBaV1VpTENKdlkyTjFjbkpsYm1ObElqcDdJazlqWTNWeWNtVnVZMlZKUkNJNkluSmxiR0YwWldRdGIyTmpkWEp5Wlc1alpTMTJNVG95WmpWbVlqUmtNRFV3TmpGbE56bGhOamMzWlRKallUY3dOemhqT0dRNE9TSXNJazV2WkdWSlJDSTZJbTVmWVRCaE1XVXlNamhrT0dZd1kyTTROak0xTnpCbVkyRTRJaXdpVW1WemIzVnlZMlZVZVhCbElqb2lSR2xoWjI1dmMzUnBZMUpsY0c5eWRDSXNJbEp2ZFhSbElqcGJleUpsWkdkbFNXUWlPaUpsWDJFNE1tUm1NRFZsWkRNMU9HVXdZelprTkRRNU9HSTBOaUlzSW1aeWIyMU9iMlJsU1dRaU9pSnVYemRsTXpWaFl6Z3hOek5oTTJJNE1UVTVZVFJpTm1ObU1TSXNJblJ2VG05a1pVbGtJam9pYmw5aE1HRXhaVEl5T0dRNFpqQmpZemcyTXpVM01HWmpZVGdpTENKbWNtOXRVbVZ6YjNWeVkyVlVlWEJsSWpvaVVHRjBhV1Z1ZENJc0luUnZVbVZ6YjNWeVkyVlVlWEJsSWpvaVJHbGhaMjV2YzNScFkxSmxjRzl5ZENJc0luSmxiR0YwYVc5dWMyaHBjQ0k2SW5OMVltcGxZM1JmVUdGMGFXVnVkQ0lzSW5OMGIzSmhaMlZFYVhKbFkzUnBiMjRpT2lKSlRrSlBWVTVFSWl3aWJXRjBZMmhOYjJSbElqb2lUMUJVU1U5T1FVd2lmVjE5TENKd1lYUm9Jam9pWTI5a1pTNWpiMlJwYm1kYlhTSXNJbVpvYVhKVWVYQmxJam9pUTI5a2FXNW5JaXdpWTJGeVpHbHVZV3hwZEhraU9pSk5RVTVaSWl3aWMyaGhjR1VpT2lKQlVsSkJXU0lzSW5KbFptVnlaVzVqWlNJNlptRnNjMlY5LjlhMmJiMjk2NjNjZGEwNTY5MGM1NzQ5ZGE3YTQ0NDVmMDRlMTRjYzQ4ZTRmNmZiYWEwNzA4YzlhZDAxNDE0M2QiLCJ2YWx1ZUtpbmQiOiJDT0RJTkdfSURFTlRJVFkiLCJub2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsImZoaXJUeXBlIjoiQ29kaW5nIiwiY2FyZGluYWxpdHkiOiJNQU5ZIiwic2hhcGUiOiJDT0RJTkdfSURFTlRJVFkiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.387924940a9feffa5fbfed5736b42cfbd6fb21f11486a133696c2de895a9d11a"
      },
      "valueKind": "CODING_IDENTITY",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:5cc3e111fa500d0599f6a2a2fd7cc3493f1d0a0c38d41c45f1f36a05a703b0d2",
      "label": "Name/Code for this diagnostic report",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:2f5fb4d05061e79a677e2ca7078c8d89",
      "resourceType": "Coding",
      "ownerOccurrenceId": "related-occurrence-v1:2f5fb4d05061e79a677e2ca7078c8d89",
      "ownerResourceType": "DiagnosticReport",
      "canonicalPath": "code.coding[]",
      "relatedOccurrence": {
        "routeId": "related-route-v1:f457fa79bf98ad8128157c0a6c7028c0aae5a39fc60770b2f8c5c227b45e438a",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6NTg4M2EwNTdjYTA2N2U1NmM2NDdjNmY3ZGRkMGQ0ZTBkNjdmYTI1MjdmMDhlYmI5NGYxYjU1ODdkZTg2OTE2OCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJkZWNpbWFsIiwiY2FuZGlkYXRlSWQiOiJjXzU5ZDlmYTYwZjZhMTZiNjFmZjdmY2U2ZSIsImNhbm9uaWNhbFBhdGgiOiJ2YWx1ZVF1YW50aXR5LnZhbHVlIiwiZW1wdHlQb2xpY3kiOiJQUkVTRVJWRV9QQVJFTlQiLCJtdWx0aXBsaWNpdHkiOiJESVNUSU5DVF9UQVJHRVRfUEVSX0lOUFVUIn19LCJ2YWx1ZUtpbmQiOiJTQ0FMQVIiLCJub2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsImZoaXJUeXBlIjoiZGVjaW1hbCIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJPYnNlcnZhdGlvbiIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.9d6c72b0c30a8b638319b7a50c125604643838a6e648eb355519a543859d4885"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:5883a057ca067e56c647c6f7ddd0d4e0d67fa2527f08ebb94f1b5587de869168",
      "candidateId": "c_59d9fa60f6a16b61ff7fce6e",
      "label": "Numerical value (with implicit precision)",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "decimal",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "decimal",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "valueQuantity.value",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtc291cmNlLXNoYXBlLXYxOmRhMDFhMDkzOGMyYTczMDU3YTViY2ZmYTk5MmY2M2ZjMDFhN2UxOGM4MjI5ZTBmMDQ5ZGUwNmVhOTc1ZTQxYmEiLCJzb3VyY2VJZGVudGl0eUNvbHVtbiI6Il9rZXkiLCJpbnB1dCI6eyJraW5kIjoiQ1VSUkVOVF9TVEFHRSIsImN1cnJlbnRTdGFnZSI6eyJzb3VyY2VSb2xlIjoiUk9PVCIsIm9jY3VycmVuY2VJZCI6ImJhc2UiLCJvd25lck9jY3VycmVuY2VJZCI6ImJhc2UiLCJvd25lclJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJyZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiY2FuZGlkYXRlSWQiOiJjXzdiZDc4YWExZTBhNTI0OTc5MDQ3OWM1OCIsImNhbm9uaWNhbFBhdGgiOiJiaXJ0aERhdGUifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwiZmhpclR5cGUiOiJkYXRlIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9LCJyb3V0ZSI6W119.e86148a4ccfcb995385ee62bc930a1900a0c748de7bb6b63293df39f27b5f397"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-source-shape-v1:da01a0938c2a73057a5bcffa992f63fc01a7e18c8229e0f049de06ea975e41ba",
      "candidateId": "c_7bd78aa1e0a5249790479c58",
      "label": "The date of birth for the individual",
      "scopeLabel": "Patient \u00b7 root",
      "valueType": "date",
      "sourceRole": "ROOT",
      "occurrenceId": "base",
      "resourceType": "Patient",
      "ownerOccurrenceId": "base",
      "ownerResourceType": "Patient",
      "canonicalPath": "birthDate"
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6ZGRjMWQ0ZDAwYzM0Y2IwYzRkNDI1NjVlZTAyYjY1ZmJiMWMyYWFlOGY4OWI0MTg2MzRhZDdhMzljNTBjYTgxMCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJDb2RpbmciLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQiLCJjb2RpbmdTZWxlY3RvciI6ImNvbXBvbmVudFtdLmNvZGUuY29kaW5nW10iLCJjb2RpbmdFbXB0eVBvbGljeSI6IkdST1VQIiwib3JkaW5hbFNlbWFudGljcyI6IlpFUk9fQkFTRURfUE9TSVRJT05fSU5fRkxBVFRFTkVEX0VYUEFOU0lPTl9BUlJBWSJ9fSwicm93Q2hvaWNlSWQiOiJyYzEuZXlKMlpYSnphVzl1SWpvaWNtOTNMV05vYjJsalpTOTJNU0lzSW10cGJtUWlPaUpGV0ZCQlRrUkZSRjlUUTA5UVJTSXNJbk51WVhCemFHOTBWRzlyWlc0aU9pSnphR0V5TlRZNllqZGlNVE0xTW1NM05HSXhPRFF5TXpWbFpUUTBZbUV6T1dSbE1EWTVNVE0xT1RSbFlqTXdaR1V6WkRCak5XTm1NakF5T1RJNFpHRmlPVEl4TWpFeE1pSXNJbk5qYUdWdFlVUnBaMlZ6ZENJNklqbGxPVFl3T1RNMk16UTNNbUl6WkdKbE9ESTJNekZqTjJWaFpqYzJZelF6WlROaU9XUTNNRGt6TUdObU56SmlNakExTW1ZMlpUVm1Nell3WW1RMFpXVWlMQ0p2WTJOMWNuSmxibU5sSWpwN0lrOWpZM1Z5Y21WdVkyVkpSQ0k2SW5KbGJHRjBaV1F0YjJOamRYSnlaVzVqWlMxMk1Ub3hOV0UxWW1VeE1qbGpNakprTXpoaFltRXpNR1kzWldSbU9XWXdZVFpoTkNJc0lrNXZaR1ZKUkNJNkltNWZNR0ZoWmpFMVpUaGtNalEyWWpVeFpqZ3lOVGN6WkRaa0lpd2lVbVZ6YjNWeVkyVlVlWEJsSWpvaVQySnpaWEoyWVhScGIyNGlMQ0pTYjNWMFpTSTZXM3NpWldSblpVbGtJam9pWlY5ak5qTTFZbU5rTm1NME1tWTBZekJpTjJFMU1EUTBZMlFpTENKbWNtOXRUbTlrWlVsa0lqb2libDgzWlRNMVlXTTRNVGN6WVROaU9ERTFPV0UwWWpaalpqRWlMQ0owYjA1dlpHVkpaQ0k2SW01Zk1HRmhaakUxWlRoa01qUTJZalV4WmpneU5UY3paRFprSWl3aVpuSnZiVkpsYzI5MWNtTmxWSGx3WlNJNklsQmhkR2xsYm5RaUxDSjBiMUpsYzI5MWNtTmxWSGx3WlNJNklrOWljMlZ5ZG1GMGFXOXVJaXdpY21Wc1lYUnBiMjV6YUdsd0lqb2ljM1ZpYW1WamRGOVFZWFJwWlc1MElpd2ljM1J2Y21GblpVUnBjbVZqZEdsdmJpSTZJa2xPUWs5VlRrUWlMQ0p0WVhSamFFMXZaR1VpT2lKUFVGUkpUMDVCVENKOVhYMHNJbkJoZEdnaU9pSmpiMjF3YjI1bGJuUmJYUzVqYjJSbExtTnZaR2x1WjF0ZElpd2labWhwY2xSNWNHVWlPaUpEYjJScGJtY2lMQ0pqWVhKa2FXNWhiR2wwZVNJNklrMUJUbGtpTENKemFHRndaU0k2SWtGU1VrRlpJaXdpY21WbVpYSmxibU5sSWpwbVlXeHpaWDAuNDRjOGUyZTRlM2E2M2Q4YTA5YTFlMjU1ZjAzZmRmMTBiYjc0YWI4MzliODE2NGM0YWY3NmVhMGIyOWIwZTQ4ZiIsInZhbHVlS2luZCI6IkNPRElOR19JREVOVElUWSIsIm5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwiZmhpclR5cGUiOiJDb2RpbmciLCJjYXJkaW5hbGl0eSI6Ik1BTlkiLCJzaGFwZSI6IkNPRElOR19JREVOVElUWSIsInJlZmVyZW5jZSI6ZmFsc2V9LCJyb3V0ZSI6W3siZWRnZUlkIjoiZV9jNjM1YmNkNmM0MmY0YzBiN2E1MDQ0Y2QiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJ0b05vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwiZnJvbVJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJ0b1Jlc291cmNlVHlwZSI6Ik9ic2VydmF0aW9uIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9XX0.bdfb9ed31e838a6fb46033a19cca7f3ab7dcda1e616bea90e896b08f69eccdc4"
      },
      "valueKind": "CODING_IDENTITY",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:ddc1d4d00c34cb0c4d42565ee02b65fbb1c2aae8f89b418634ad7a39c50ca810",
      "label": "Type of component observation (code / type)",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "Coding",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "component[].code.coding[]",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6NTU2YTI5M2E3OWI1MDY2M2Y1MzIyNTdkN2NiMmI0NWYxMjIwMDc5ZDJhNGM4OGRhY2I2M2ZjNjAzYzg3OTQ2NiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJDb2RpbmciLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQiLCJjb2RpbmdTZWxlY3RvciI6ImNvZGUuY29kaW5nW10iLCJjb2RpbmdFbXB0eVBvbGljeSI6IkdST1VQIiwib3JkaW5hbFNlbWFudGljcyI6IlpFUk9fQkFTRURfUE9TSVRJT05fSU5fRkxBVFRFTkVEX0VYUEFOU0lPTl9BUlJBWSJ9fSwicm93Q2hvaWNlSWQiOiJyYzEuZXlKMlpYSnphVzl1SWpvaWNtOTNMV05vYjJsalpTOTJNU0lzSW10cGJtUWlPaUpGV0ZCQlRrUkZSRjlUUTA5UVJTSXNJbk51WVhCemFHOTBWRzlyWlc0aU9pSnphR0V5TlRZNllqZGlNVE0xTW1NM05HSXhPRFF5TXpWbFpUUTBZbUV6T1dSbE1EWTVNVE0xT1RSbFlqTXdaR1V6WkRCak5XTm1NakF5T1RJNFpHRmlPVEl4TWpFeE1pSXNJbk5qYUdWdFlVUnBaMlZ6ZENJNklqbGxPVFl3T1RNMk16UTNNbUl6WkdKbE9ESTJNekZqTjJWaFpqYzJZelF6WlROaU9XUTNNRGt6TUdObU56SmlNakExTW1ZMlpUVm1Nell3WW1RMFpXVWlMQ0p2WTJOMWNuSmxibU5sSWpwN0lrOWpZM1Z5Y21WdVkyVkpSQ0k2SW5KbGJHRjBaV1F0YjJOamRYSnlaVzVqWlMxMk1Ub3hOV0UxWW1VeE1qbGpNakprTXpoaFltRXpNR1kzWldSbU9XWXdZVFpoTkNJc0lrNXZaR1ZKUkNJNkltNWZNR0ZoWmpFMVpUaGtNalEyWWpVeFpqZ3lOVGN6WkRaa0lpd2lVbVZ6YjNWeVkyVlVlWEJsSWpvaVQySnpaWEoyWVhScGIyNGlMQ0pTYjNWMFpTSTZXM3NpWldSblpVbGtJam9pWlY5ak5qTTFZbU5rTm1NME1tWTBZekJpTjJFMU1EUTBZMlFpTENKbWNtOXRUbTlrWlVsa0lqb2libDgzWlRNMVlXTTRNVGN6WVROaU9ERTFPV0UwWWpaalpqRWlMQ0owYjA1dlpHVkpaQ0k2SW01Zk1HRmhaakUxWlRoa01qUTJZalV4WmpneU5UY3paRFprSWl3aVpuSnZiVkpsYzI5MWNtTmxWSGx3WlNJNklsQmhkR2xsYm5RaUxDSjBiMUpsYzI5MWNtTmxWSGx3WlNJNklrOWljMlZ5ZG1GMGFXOXVJaXdpY21Wc1lYUnBiMjV6YUdsd0lqb2ljM1ZpYW1WamRGOVFZWFJwWlc1MElpd2ljM1J2Y21GblpVUnBjbVZqZEdsdmJpSTZJa2xPUWs5VlRrUWlMQ0p0WVhSamFFMXZaR1VpT2lKUFVGUkpUMDVCVENKOVhYMHNJbkJoZEdnaU9pSmpiMlJsTG1OdlpHbHVaMXRkSWl3aVptaHBjbFI1Y0dVaU9pSkRiMlJwYm1jaUxDSmpZWEprYVc1aGJHbDBlU0k2SWsxQlRsa2lMQ0p6YUdGd1pTSTZJa0ZTVWtGWklpd2ljbVZtWlhKbGJtTmxJanBtWVd4elpYMC45N2UyZWMxZTM0Y2FlMGE1ODZkMzcwMzFhNDJlODA5ZmMzYmY5OGMyMDU0NTFmYzIyZTJmMzBmZjY1NmI1NGY2IiwidmFsdWVLaW5kIjoiQ09ESU5HX0lERU5USVRZIiwibm9kZUlkIjoibl8wYWFmMTVlOGQyNDZiNTFmODI1NzNkNmQiLCJmaGlyVHlwZSI6IkNvZGluZyIsImNhcmRpbmFsaXR5IjoiTUFOWSIsInNoYXBlIjoiQ09ESU5HX0lERU5USVRZIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlX2M2MzViY2Q2YzQyZjRjMGI3YTUwNDRjZCIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl8wYWFmMTVlOGQyNDZiNTFmODI1NzNkNmQiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZWxhdGlvbnNoaXAiOiJzdWJqZWN0X1BhdGllbnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn1dfQ.2fd33e97558df6d05c8243cd2dca3d82c514e149fa4eb6f4e0cc771fe648bb05"
      },
      "valueKind": "CODING_IDENTITY",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:556a293a79b50663f532257d7cb2b45f1220079d2a4c88dacb63fc603c879466",
      "label": "Type of observation (code / type)",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "Coding",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "code.coding[]",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6ZDQ3ZmM1YzAzOGVkNWEyOTI3YTc5ZTE0NjI5OTA0ODAwMTU5NDZkMzI3ZTIzNzNlMjk1MTU5MGFiOTQxN2FhYiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfY2VhZDA3ODE4MDU3NjY2YmVmMjY2OWEyIiwiY2Fub25pY2FsUGF0aCI6InZhbHVlUXVhbnRpdHkudW5pdCIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl8wYWFmMTVlOGQyNDZiNTFmODI1NzNkNmQiLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJPYnNlcnZhdGlvbiIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.e828197e7bff495fa57a7f315fefed2289c3a5154ce077206e64d4562e08676a"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:d47fc5c038ed5a2927a79e1462990480015946d327e2373e2951590ab9417aab",
      "candidateId": "c_cead07818057666bef2669a2",
      "label": "Unit representation",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "valueQuantity.unit",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtc291cmNlLXNoYXBlLXYxOmRhMDFhMDkzOGMyYTczMDU3YTViY2ZmYTk5MmY2M2ZjMDFhN2UxOGM4MjI5ZTBmMDQ5ZGUwNmVhOTc1ZTQxYmEiLCJzb3VyY2VJZGVudGl0eUNvbHVtbiI6Il9rZXkiLCJpbnB1dCI6eyJraW5kIjoiQ1VSUkVOVF9TVEFHRSIsImN1cnJlbnRTdGFnZSI6eyJzb3VyY2VSb2xlIjoiUk9PVCIsIm9jY3VycmVuY2VJZCI6ImJhc2UiLCJvd25lck9jY3VycmVuY2VJZCI6ImJhc2UiLCJvd25lclJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJyZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiY2FuZGlkYXRlSWQiOiJjXzFiYzM1OTEwYmRmNzYyYmY1NDExNjkzYyIsImNhbm9uaWNhbFBhdGgiOiJtZXRhLmxhc3RVcGRhdGVkIn19LCJ2YWx1ZUtpbmQiOiJTQ0FMQVIiLCJub2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsImZoaXJUeXBlIjoiZGF0ZVRpbWUiLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbXX0.184c6928784b1f35354949cd0e17f94a28a3ab4e02fbbee53ff7a66ab0e6bfc4"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-source-shape-v1:da01a0938c2a73057a5bcffa992f63fc01a7e18c8229e0f049de06ea975e41ba",
      "candidateId": "c_1bc35910bdf762bf5411693c",
      "label": "When the resource version last changed",
      "scopeLabel": "Patient \u00b7 root",
      "valueType": "date_time",
      "sourceRole": "ROOT",
      "occurrenceId": "base",
      "resourceType": "Patient",
      "ownerOccurrenceId": "base",
      "ownerResourceType": "Patient",
      "canonicalPath": "meta.lastUpdated"
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6NTJiY2M3MjhlYjdkNjJjM2Q3ZmYyMzY1YjY0YTI4MDQ4ZjNhMzg4NmViZjU5YmNkYTEzZjQ0MWRkM2ViNDA3ZCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifSx7InN0ZXBJbmRleCI6MSwiZnJvbU9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwiLCJ0b09jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToyNDBiOGFhOWI3NDI3MGFiZWIxMzQ5M2EwYjhmNmE0MyIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIn1dLCJ0YXJnZXRPY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6MjQwYjhhYTliNzQyNzBhYmViMTM0OTNhMGI4ZjZhNDMiLCJ0YXJnZXROb2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsInRhcmdldFJlc291cmNlVHlwZSI6IlNwZWNpbWVuIiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjX2IzMzA0ZDhhYzAwZjQzYjBjZDliYTMzNCIsImNhbm9uaWNhbFBhdGgiOiJzdGF0dXMiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fYzgyMGU2NDljMTY4Njc5MWFiMjBhMzEyIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlXzJmMzA4ZjExODRmNzMxNmZhOTlkMGZiNSIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl80NjczNmM3ZTZkMGM5NzgyZjUyNGE0ZDIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZWxhdGlvbnNoaXAiOiJtZW1iZXJfZW50aXR5X1BhdGllbnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn0seyJlZGdlSWQiOiJlX2I0NTY4YzhmM2E0MWNmMGY1MjhkZTJjZSIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.52b3724dc89b21a228fe3c0ee67e8cd0020ef52752c394bd2356031778f0fd40"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:52bcc728eb7d62c3d7ff2365b64a28048f3a3886ebf59bcda13f441dd3eb407d",
      "candidateId": "c_b3304d8ac00f43b0cd9ba334",
      "label": "available | unavailable | unsatisfactory | entered-in-error",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:240b8aa9b74270abeb13493a0b8f6a43",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:240b8aa9b74270abeb13493a0b8f6a43",
      "ownerResourceType": "Specimen",
      "canonicalPath": "status",
      "relatedOccurrence": {
        "routeId": "related-route-v1:d35851cff28f9f8dd5faaeadb55ac53f150dbcc7874b2436f6134424e13f57a1",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6ZmViMDU1M2FlYzU3NTExMTQ0ODk5NzIwMjA1ZDdhMDYzNGQyY2Y0NTVjMzljMzU4N2JlMjE5OGZkNmQ1NjhjZiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifSx7InN0ZXBJbmRleCI6MSwiZnJvbU9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsImZyb21Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsImZyb21SZXNvdXJjZVR5cGUiOiJHcm91cCIsImVkZ2VJZCI6ImVfYjQ1NjhjOGYzYTQxY2YwZjUyOGRlMmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9Hcm91cCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwiLCJ0b09jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToyNDBiOGFhOWI3NDI3MGFiZWIxMzQ5M2EwYjhmNmE0MyIsInRvTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJ0b1Jlc291cmNlVHlwZSI6IlNwZWNpbWVuIn0seyJzdGVwSW5kZXgiOjIsImZyb21PY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6MjQwYjhhYTliNzQyNzBhYmViMTM0OTNhMGI4ZjZhNDMiLCJmcm9tTm9kZUlkIjoibl9jODIwZTY0OWMxNjg2NzkxYWIyMGEzMTIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiU3BlY2ltZW4iLCJlZGdlSWQiOiJlXzQzOTQxN2Y0NjQ0MWEyMjNkMzUzM2MwMCIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfU3BlY2ltZW4iLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIiwidG9PY2N1cnJlbmNlSWQiOiJyZWxhdGVkLW9jY3VycmVuY2UtdjE6YzY2MzQ1YzNkZGI5MTNiNTdhMjg4MGI0MzZkNTkxNzAiLCJ0b05vZGVJZCI6Im5fMjcxNmM0ZTYzNWZmOTNlNDRjYTkzM2RmIiwidG9SZXNvdXJjZVR5cGUiOiJEb2N1bWVudFJlZmVyZW5jZSJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOmM2NjM0NWMzZGRiOTEzYjU3YTI4ODBiNDM2ZDU5MTcwIiwidGFyZ2V0Tm9kZUlkIjoibl8yNzE2YzRlNjM1ZmY5M2U0NGNhOTMzZGYiLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJEb2N1bWVudFJlZmVyZW5jZSIsInJlc291cmNlVHlwZSI6InN0cmluZyIsImNhbmRpZGF0ZUlkIjoiY183YzE4NDFjZTE0MTgzODRjOGY1NTVhMDEiLCJjYW5vbmljYWxQYXRoIjoic3RhdHVzIiwiZW1wdHlQb2xpY3kiOiJQUkVTRVJWRV9QQVJFTlQiLCJtdWx0aXBsaWNpdHkiOiJESVNUSU5DVF9UQVJHRVRfUEVSX0lOUFVUIn19LCJ2YWx1ZUtpbmQiOiJTQ0FMQVIiLCJub2RlSWQiOiJuXzI3MTZjNGU2MzVmZjkzZTQ0Y2E5MzNkZiIsImZoaXJUeXBlIjoic3RyaW5nIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9LCJyb3V0ZSI6W3siZWRnZUlkIjoiZV8yZjMwOGYxMTg0ZjczMTZmYTk5ZDBmYjUiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJ0b05vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwiZnJvbVJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJ0b1Jlc291cmNlVHlwZSI6Ikdyb3VwIiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCJ9LHsiZWRnZUlkIjoiZV9iNDU2OGM4ZjNhNDFjZjBmNTI4ZGUyY2UiLCJmcm9tTm9kZUlkIjoibl80NjczNmM3ZTZkMGM5NzgyZjUyNGE0ZDIiLCJ0b05vZGVJZCI6Im5fYzgyMGU2NDljMTY4Njc5MWFiMjBhMzEyIiwiZnJvbVJlc291cmNlVHlwZSI6Ikdyb3VwIiwidG9SZXNvdXJjZVR5cGUiOiJTcGVjaW1lbiIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfR3JvdXAiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn0seyJlZGdlSWQiOiJlXzQzOTQxN2Y0NjQ0MWEyMjNkMzUzM2MwMCIsImZyb21Ob2RlSWQiOiJuX2M4MjBlNjQ5YzE2ODY3OTFhYjIwYTMxMiIsInRvTm9kZUlkIjoibl8yNzE2YzRlNjM1ZmY5M2U0NGNhOTMzZGYiLCJmcm9tUmVzb3VyY2VUeXBlIjoiU3BlY2ltZW4iLCJ0b1Jlc291cmNlVHlwZSI6IkRvY3VtZW50UmVmZXJlbmNlIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9TcGVjaW1lbiIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.e5a8c3427b55c27f59f2b6d565b0622b9d263ec7c4cf631aaca000c54ccbf055"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:feb0553aec57511144899720205d7a0634d2cf455c39c3587be2198fd6d568cf",
      "candidateId": "c_7c1841ce1418384c8f555a01",
      "label": "current | superseded | entered-in-error",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 subject_Specimen \u2192 DocumentReference (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:c66345c3ddb913b57a2880b436d59170",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:c66345c3ddb913b57a2880b436d59170",
      "ownerResourceType": "DocumentReference",
      "canonicalPath": "status",
      "relatedOccurrence": {
        "routeId": "related-route-v1:8a4522e226456d4319418de86bd1bd36ba1432f68002db15c85050f15151b0fe",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 subject_Group \u2192 Specimen (Optional: parent row is kept when unmatched) \u00b7 subject_Specimen \u2192 DocumentReference (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6Y2FiYWU0MjhiYTg4MjA1MjI3NTU2YWZmMWJmMzM4MzhlZWJkNjIwYjU0YTg5YTc0ZGEwNGI4ODVjNWM5MWU0NCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsInRhcmdldE5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfYzRhYjJmZWNjOTRjZWZhYzc1ZjdhZDZlIiwiY2Fub25pY2FsUGF0aCI6Im1lbWJlcnNoaXAiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlXzJmMzA4ZjExODRmNzMxNmZhOTlkMGZiNSIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl80NjczNmM3ZTZkMGM5NzgyZjUyNGE0ZDIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZWxhdGlvbnNoaXAiOiJtZW1iZXJfZW50aXR5X1BhdGllbnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn1dfQ.36cf2af1bdbe4f39fa70eb638b708587c50daa0e1c0e7a799fb0450635866881"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:cabae428ba88205227556aff1bf33838eebd620b54a89a74da04b885c5c91e44",
      "candidateId": "c_c4ab2fecc94cefac75f7ad6e",
      "label": "definitional | enumerated",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:9fe14187ab1c954417b9eb6ec9b3e7bf",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:9fe14187ab1c954417b9eb6ec9b3e7bf",
      "ownerResourceType": "Group",
      "canonicalPath": "membership",
      "relatedOccurrence": {
        "routeId": "related-route-v1:72ab7fffd53ee431b8f711ab7c9df8dd2f9bcc4a44fac990dff8526feaa7b91a",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6OGUwMDUxOGE3MDhjNGFiOTMxODRlMThiNGY5Y2NhMDljODdiZDc0OGZhMGM5NjgyZmE4MDhiMWI5ZjUyY2NhZiIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCJ9LHsic3RlcEluZGV4IjoxLCJmcm9tT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwiZnJvbU5vZGVJZCI6Im5fYTBhMWUyMjhkOGYwY2M4NjM1NzBmY2E4IiwiZnJvbVJlc291cmNlVHlwZSI6IkRpYWdub3N0aWNSZXBvcnQiLCJlZGdlSWQiOiJlXzIxMzAzYTRiNjkzZTcxODlkNTllNDE0YiIsInJlbGF0aW9uc2hpcCI6InJlc3VsdF9EaWFnbm9zdGljUmVwb3J0Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjdiZmQzZjY2ZDJkMzJkNzIwNjZkNDM4YTA2YmYyYmNiIiwidG9Ob2RlSWQiOiJuX2QxNDhiNGQ5MzRjYzg5MThlMDVkZGU4NyIsInRvUmVzb3VyY2VUeXBlIjoiUmVzZWFyY2hTdHVkeSJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjdiZmQzZjY2ZDJkMzJkNzIwNjZkNDM4YTA2YmYyYmNiIiwidGFyZ2V0Tm9kZUlkIjoibl9kMTQ4YjRkOTM0Y2M4OTE4ZTA1ZGRlODciLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJSZXNlYXJjaFN0dWR5IiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjXzRiMTZiMDRhNmIyZTlhNWI4OTIxODQxYyIsImNhbm9uaWNhbFBhdGgiOiJzdGF0dXMiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fZDE0OGI0ZDkzNGNjODkxOGUwNWRkZTg3IiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlX2E4MmRmMDVlZDM1OGUwYzZkNDQ5OGI0NiIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifSx7ImVkZ2VJZCI6ImVfMjEzMDNhNGI2OTNlNzE4OWQ1OWU0MTRiIiwiZnJvbU5vZGVJZCI6Im5fYTBhMWUyMjhkOGYwY2M4NjM1NzBmY2E4IiwidG9Ob2RlSWQiOiJuX2QxNDhiNGQ5MzRjYzg5MThlMDVkZGU4NyIsImZyb21SZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwidG9SZXNvdXJjZVR5cGUiOiJSZXNlYXJjaFN0dWR5IiwicmVsYXRpb25zaGlwIjoicmVzdWx0X0RpYWdub3N0aWNSZXBvcnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn1dfQ.8e9657dd7d6fdfd24a88e488b3a536d8af1ab610e6e0048616d0de6ebcee9a3b"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:8e00518a708c4ab93184e18b4f9cca09c87bd748fa0c9682fa808b1b9f52ccaf",
      "candidateId": "c_4b16b04a6b2e9a5b8921841c",
      "label": "draft | active | retired | unknown",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 result_DiagnosticReport \u2192 ResearchStudy (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:7bfd3f66d2d32d72066d438a06bf2bcb",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:7bfd3f66d2d32d72066d438a06bf2bcb",
      "ownerResourceType": "ResearchStudy",
      "canonicalPath": "status",
      "relatedOccurrence": {
        "routeId": "related-route-v1:b0db7a56ad28f9a4130de7f6efd715cad40db5f2fb2be404eee21ed04e8e0529",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 result_DiagnosticReport \u2192 ResearchStudy (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtc291cmNlLXNoYXBlLXYxOmRhMDFhMDkzOGMyYTczMDU3YTViY2ZmYTk5MmY2M2ZjMDFhN2UxOGM4MjI5ZTBmMDQ5ZGUwNmVhOTc1ZTQxYmEiLCJzb3VyY2VJZGVudGl0eUNvbHVtbiI6Il9rZXkiLCJpbnB1dCI6eyJraW5kIjoiQ1VSUkVOVF9TVEFHRSIsImN1cnJlbnRTdGFnZSI6eyJzb3VyY2VSb2xlIjoiUk9PVCIsIm9jY3VycmVuY2VJZCI6ImJhc2UiLCJvd25lck9jY3VycmVuY2VJZCI6ImJhc2UiLCJvd25lclJlc291cmNlVHlwZSI6IlBhdGllbnQiLCJyZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiY2FuZGlkYXRlSWQiOiJjX2Q5YTQ5YjAxYTFiZjlkMDJiOTgyNzZkYiIsImNhbm9uaWNhbFBhdGgiOiJnZW5kZXIifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbXX0.b27a6d5d18322a759ff4b146d2a2909f2ee08b14de73ec22192545775c44cb21"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-source-shape-v1:da01a0938c2a73057a5bcffa992f63fc01a7e18c8229e0f049de06ea975e41ba",
      "candidateId": "c_d9a49b01a1bf9d02b98276db",
      "label": "male | female | other | unknown",
      "scopeLabel": "Patient \u00b7 root",
      "valueType": "string",
      "sourceRole": "ROOT",
      "occurrenceId": "base",
      "resourceType": "Patient",
      "ownerOccurrenceId": "base",
      "ownerResourceType": "Patient",
      "canonicalPath": "gender"
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6OTI0MTVmYWU4MWY0OTg0N2UxN2UwMWFmNDdiZjE2YjNiOGE4YmQwMmY1ODQ0OTI1MjcxMzdlZmE1ZGZiOGQzOCIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfMmYzMDhmMTE4NGY3MzE2ZmE5OWQwZmI1IiwicmVsYXRpb25zaGlwIjoibWVtYmVyX2VudGl0eV9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjlmZTE0MTg3YWIxYzk1NDQxN2I5ZWI2ZWM5YjNlN2JmIiwidG9Ob2RlSWQiOiJuXzQ2NzM2YzdlNmQwYzk3ODJmNTI0YTRkMiIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MTo5ZmUxNDE4N2FiMWM5NTQ0MTdiOWViNmVjOWIzZTdiZiIsInRhcmdldE5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfODc1MGFlNGNjMWMyZTRjZTI2MmE2YTgwIiwiY2Fub25pY2FsUGF0aCI6InR5cGUiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fNDY3MzZjN2U2ZDBjOTc4MmY1MjRhNGQyIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlXzJmMzA4ZjExODRmNzMxNmZhOTlkMGZiNSIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl80NjczNmM3ZTZkMGM5NzgyZjUyNGE0ZDIiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiR3JvdXAiLCJyZWxhdGlvbnNoaXAiOiJtZW1iZXJfZW50aXR5X1BhdGllbnQiLCJzdG9yYWdlRGlyZWN0aW9uIjoiSU5CT1VORCIsIm1hdGNoTW9kZSI6Ik9QVElPTkFMIn1dfQ.f1a366cf32292a881e2789847df25e9ddaaf4a6d355963f9e0b763f7f11c3d0a"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:92415fae81f49847e17e01af47bf16b3b8a8bd02f584492527137efa5dfb8d38",
      "candidateId": "c_8750ae4cc1c2e4ce262a6a80",
      "label": "person | animal | practitioner | device | careteam | healthcareservice | location | organization | relatedperson | specimen",
      "scopeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:9fe14187ab1c954417b9eb6ec9b3e7bf",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:9fe14187ab1c954417b9eb6ec9b3e7bf",
      "ownerResourceType": "Group",
      "canonicalPath": "type",
      "relatedOccurrence": {
        "routeId": "related-route-v1:72ab7fffd53ee431b8f711ab7c9df8dd2f9bcc4a44fac990dff8526feaa7b91a",
        "routeLabel": "Patient \u00b7 member_entity_Patient \u2192 Group (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6YWM0MDljNDk1ZjhkN2VjOWRhNjI2NDAyNzExYmY0YmQ0ZGZjN2MyYzRmYjVkY2IyMmRjZWU1MzI4MDQ5NThhMyIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYTgyZGYwNWVkMzU4ZTBjNmQ0NDk4YjQ2IiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidG9Ob2RlSWQiOiJuX2EwYTFlMjI4ZDhmMGNjODYzNTcwZmNhOCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCJ9XSwidGFyZ2V0T2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjJmNWZiNGQwNTA2MWU3OWE2NzdlMmNhNzA3OGM4ZDg5IiwidGFyZ2V0Tm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJ0YXJnZXRSZXNvdXJjZVR5cGUiOiJEaWFnbm9zdGljUmVwb3J0IiwicmVzb3VyY2VUeXBlIjoic3RyaW5nIiwiY2FuZGlkYXRlSWQiOiJjXzQyYmMxNTY1MzU0OTY5MWMxMzkzMjI1NiIsImNhbm9uaWNhbFBhdGgiOiJzdGF0dXMiLCJlbXB0eVBvbGljeSI6IlBSRVNFUlZFX1BBUkVOVCIsIm11bHRpcGxpY2l0eSI6IkRJU1RJTkNUX1RBUkdFVF9QRVJfSU5QVVQifX0sInZhbHVlS2luZCI6IlNDQUxBUiIsIm5vZGVJZCI6Im5fYTBhMWUyMjhkOGYwY2M4NjM1NzBmY2E4IiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0sInJvdXRlIjpbeyJlZGdlSWQiOiJlX2E4MmRmMDVlZDM1OGUwYzZkNDQ5OGI0NiIsImZyb21Ob2RlSWQiOiJuXzdlMzVhYzgxNzNhM2I4MTU5YTRiNmNmMSIsInRvTm9kZUlkIjoibl9hMGExZTIyOGQ4ZjBjYzg2MzU3MGZjYTgiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsInRvUmVzb3VyY2VUeXBlIjoiRGlhZ25vc3RpY1JlcG9ydCIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.b1ad6261c309184ec1685d43692a2b1aad4a1268b7843c8dfaa11f59ad935f2c"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:ac409c495f8d7ec9da626402711bf4bd4dfc7c2c4fb5dcb22dcee532804958a3",
      "candidateId": "c_42bc15653549691c13932256",
      "label": "registered | partial | preliminary | modified | final | amended | corrected | appended | cancelled | entered-in-error | unknown",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:2f5fb4d05061e79a677e2ca7078c8d89",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:2f5fb4d05061e79a677e2ca7078c8d89",
      "ownerResourceType": "DiagnosticReport",
      "canonicalPath": "status",
      "relatedOccurrence": {
        "routeId": "related-route-v1:f457fa79bf98ad8128157c0a6c7028c0aae5a39fc60770b2f8c5c227b45e438a",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 DiagnosticReport (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    },
    {
      "kind": "FIELD_SOURCE",
      "fieldSource": {
        "kind": "CHOICE",
        "choiceId": "cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJHUk9VUF9GSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6YjdiMTM1MmM3NGIxODQyMzVlZTQ0YmEzOWRlMDY5MTM1OTRlYjMwZGUzZDBjNWNmMjAyOTI4ZGFiOTIxMjExMiIsInNvdXJjZSI6eyJraW5kIjoiR1JPVVBfRklFTEQiLCJzb3VyY2VTdGFnZUlkIjoic291cmNlX3Byb2plY3Rpb24iLCJzb3VyY2VTaGFwZUlkIjoiZ3JvdXAtcmVsYXRlZC1zb3VyY2Utc2hhcGUtdjE6YTI3YjVkOWI4NzZjNzQ0Njg5NWExMWRhNGMzM2NiOTNmYWM1MDE1MTkxYTM1ZTM5NWNkMjgwNjhmNDYyY2RiOSIsInNvdXJjZUlkZW50aXR5Q29sdW1uIjoiX2tleSIsImlucHV0Ijp7ImtpbmQiOiJSRUxBVEVEX09DQ1VSUkVOQ0UiLCJyZWxhdGVkT2NjdXJyZW5jZSI6eyJhbmNob3JPY2N1cnJlbmNlSWQiOiJiYXNlIiwiYW5jaG9yTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJhbmNob3JSZXNvdXJjZVR5cGUiOiJQYXRpZW50Iiwicm91dGUiOlt7InN0ZXBJbmRleCI6MCwiZnJvbU9jY3VycmVuY2VJZCI6ImJhc2UiLCJmcm9tTm9kZUlkIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJmcm9tUmVzb3VyY2VUeXBlIjoiUGF0aWVudCIsImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwicmVsYXRpb25zaGlwIjoic3ViamVjdF9QYXRpZW50Iiwic3RvcmFnZURpcmVjdGlvbiI6IklOQk9VTkQiLCJtYXRjaE1vZGUiOiJPUFRJT05BTCIsInRvT2NjdXJyZW5jZUlkIjoicmVsYXRlZC1vY2N1cnJlbmNlLXYxOjE1YTViZTEyOWMyMmQzOGFiYTMwZjdlZGY5ZjBhNmE0IiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsInRvUmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24ifV0sInRhcmdldE9jY3VycmVuY2VJZCI6InJlbGF0ZWQtb2NjdXJyZW5jZS12MToxNWE1YmUxMjljMjJkMzhhYmEzMGY3ZWRmOWYwYTZhNCIsInRhcmdldE5vZGVJZCI6Im5fMGFhZjE1ZThkMjQ2YjUxZjgyNTczZDZkIiwidGFyZ2V0UmVzb3VyY2VUeXBlIjoiT2JzZXJ2YXRpb24iLCJyZXNvdXJjZVR5cGUiOiJzdHJpbmciLCJjYW5kaWRhdGVJZCI6ImNfNjEzMzcyYTMxM2U3MDk0ZGVjN2Y0YWY2IiwiY2Fub25pY2FsUGF0aCI6InN0YXR1cyIsImVtcHR5UG9saWN5IjoiUFJFU0VSVkVfUEFSRU5UIiwibXVsdGlwbGljaXR5IjoiRElTVElOQ1RfVEFSR0VUX1BFUl9JTlBVVCJ9fSwidmFsdWVLaW5kIjoiU0NBTEFSIiwibm9kZUlkIjoibl8wYWFmMTVlOGQyNDZiNTFmODI1NzNkNmQiLCJmaGlyVHlwZSI6InN0cmluZyIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfSwicm91dGUiOlt7ImVkZ2VJZCI6ImVfYzYzNWJjZDZjNDJmNGMwYjdhNTA0NGNkIiwiZnJvbU5vZGVJZCI6Im5fN2UzNWFjODE3M2EzYjgxNTlhNGI2Y2YxIiwidG9Ob2RlSWQiOiJuXzBhYWYxNWU4ZDI0NmI1MWY4MjU3M2Q2ZCIsImZyb21SZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwidG9SZXNvdXJjZVR5cGUiOiJPYnNlcnZhdGlvbiIsInJlbGF0aW9uc2hpcCI6InN1YmplY3RfUGF0aWVudCIsInN0b3JhZ2VEaXJlY3Rpb24iOiJJTkJPVU5EIiwibWF0Y2hNb2RlIjoiT1BUSU9OQUwifV19.e7d15f914b46b079aa766adbfa56bc793d59d9b140a6a423f7a2da42d6757746"
      },
      "valueKind": "SCALAR",
      "sourceStageId": "source_projection",
      "sourceShapeId": "group-related-source-shape-v1:a27b5d9b876c7446895a11da4c33cb93fac5015191a35e395cd28068f462cdb9",
      "candidateId": "c_613372a313e7094dec7f4af6",
      "label": "registered | preliminary | final | amended +",
      "scopeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it",
      "valueType": "string",
      "sourceRole": "RELATED_OCCURRENCE",
      "occurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "resourceType": "string",
      "ownerOccurrenceId": "related-occurrence-v1:15a5be129c22d38aba30f7edf9f0a6a4",
      "ownerResourceType": "Observation",
      "canonicalPath": "status",
      "relatedOccurrence": {
        "routeId": "related-route-v1:c5c4a9ad0b3641191d73b2240fe7305a5f29f194125d87a68f68f5b107401e4c",
        "routeLabel": "Patient \u00b7 subject_Patient \u2192 Observation (Optional: parent row is kept when unmatched) \u00b7 One distinct target per input row; duplicate paths to the same target do not multiply it"
      }
    }
  ],
  "groupRowValueChoices": [],
  "outputId": "out_c6e21c8f31fe10937fb1e634",
  "pivotSourceInput": {
    "choices": [
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6ImJpcnRoRGF0ZSIsImZoaXJUeXBlIjoiZGF0ZSIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfQ.68265ca7c4d119a50f8ba78c1180375458c630dedf4bce3af1192d91e94d66c1",
        "fhirType": "date",
        "fieldPath": "birthDate",
        "isIdentifier": false,
        "isPopulated": true,
        "isReference": false,
        "label": "Patient.birthDate",
        "logicalType": "date",
        "occurrenceId": "base",
        "valueType": "STRING"
      },
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6ImdlbmRlciIsImZoaXJUeXBlIjoic3RyaW5nIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9.600c609c72cde333edadfda1829f97d84807732fb8193a6b029c5e3ec0275b86",
        "fhirType": "string",
        "fieldPath": "gender",
        "isIdentifier": false,
        "isPopulated": true,
        "isReference": false,
        "label": "Patient.gender",
        "logicalType": "string",
        "occurrenceId": "base",
        "valueType": "STRING"
      },
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6ImlkIiwiZmhpclR5cGUiOiJzdHJpbmciLCJjYXJkaW5hbGl0eSI6Ik9ORSIsInNoYXBlIjoiU0NBTEFSIiwicmVmZXJlbmNlIjpmYWxzZX0.5a82184456c134b8f14e3fdb7e9e0bb629fa547cacd5a7ef9855c5e84589148a",
        "fhirType": "string",
        "fieldPath": "id",
        "isIdentifier": true,
        "isPopulated": true,
        "isReference": false,
        "label": "Patient.id",
        "logicalType": "string",
        "occurrenceId": "base",
        "valueType": "STRING"
      },
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6Im1ldGEubGFzdFVwZGF0ZWQiLCJmaGlyVHlwZSI6ImRhdGVUaW1lIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9.f11807baab8bea27764a7cba524bc0f72c950ab9e59b9529d30251b0b6d487fa",
        "fhirType": "dateTime",
        "fieldPath": "meta.lastUpdated",
        "isIdentifier": false,
        "isPopulated": true,
        "isReference": false,
        "label": "Patient.meta.lastUpdated",
        "logicalType": "date_time",
        "occurrenceId": "base",
        "valueType": "STRING"
      }
    ],
    "stageId": "source_projection",
    "supported": true
  },
  "selectedStage": {
    "capabilities": [
      {
        "kind": "DERIVE",
        "reason": "derive requires at least one public scalar numeric column or a numeric literal operand",
        "reasonCode": "NO_NUMERIC_COLUMN",
        "supported": false
      },
      {
        "kind": "FILTER",
        "supported": true
      },
      {
        "kind": "PIVOT",
        "reason": "pivot requires public scalar group, category, and value columns",
        "reasonCode": "INSUFFICIENT_SCALAR_COLUMNS",
        "supported": false
      },
      {
        "kind": "UNPIVOT",
        "supported": true
      },
      {
        "kind": "GROUP",
        "supported": true
      },
      {
        "kind": "CODED_GROUP",
        "supported": true
      },
      {
        "kind": "CODED_PIVOT",
        "supported": true
      },
      {
        "kind": "EXPAND",
        "reason": "expand requires a public array-valued column",
        "reasonCode": "NO_ARRAY_COLUMNS",
        "supported": false
      },
      {
        "kind": "RELATED_SOURCE",
        "supported": true
      },
      {
        "kind": "RELATED_EXPAND",
        "supported": true
      },
      {
        "kind": "RELATED_ELIGIBILITY",
        "supported": true
      },
      {
        "kind": "RELATED_FIELD",
        "reason": "related field requires the exact terminal resource identity to survive this stage",
        "reasonCode": "NO_ACTIVE_RELATED_RECORD",
        "supported": false
      }
    ],
    "columns": [
      {
        "cardinality": "optional_one",
        "id": "source_ef82eef4462a7f20fbcecfff",
        "label": "ID",
        "name": "col_cab5631258280f504d027209",
        "type": "string"
      }
    ],
    "groupFieldSourceContext": {
      "fieldOwners": [
        {
          "occurrenceId": "base",
          "ownerResourceType": "Patient",
          "resourceType": "Patient",
          "role": "ROOT",
          "stepIndex": 0
        }
      ],
      "sourceIdentityColumn": "_key",
      "sourceShapeId": "group-source-shape-v1:da01a0938c2a73057a5bcffa992f63fc01a7e18c8229e0f049de06ea975e41ba",
      "sourceStageId": "source_projection"
    },
    "id": "source_projection",
    "inputStageId": "",
    "relatedExpandAnchors": [
      {
        "anchorColumnId": "_key",
        "kind": "root",
        "label": "Original Patient",
        "resourceType": "Patient"
      }
    ],
    "rowIdentityColumn": "_key"
  },
  "snapshotToken": "sha256:b7b1352c74b184235ee44ba39de06913594eb30de3d0c5cf202928dab9212112",
  "sourceInput": {
    "choices": [
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6ImJpcnRoRGF0ZSIsImZoaXJUeXBlIjoiZGF0ZSIsImNhcmRpbmFsaXR5IjoiT05FIiwic2hhcGUiOiJTQ0FMQVIiLCJyZWZlcmVuY2UiOmZhbHNlfQ.68265ca7c4d119a50f8ba78c1180375458c630dedf4bce3af1192d91e94d66c1",
        "fhirType": "date",
        "fieldPath": "birthDate",
        "isIdentifier": false,
        "isPopulated": true,
        "isReference": false,
        "label": "BirthDate",
        "logicalType": "date",
        "occurrenceId": "base",
        "valueType": "STRING"
      },
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6Im1ldGEubGFzdFVwZGF0ZWQiLCJmaGlyVHlwZSI6ImRhdGVUaW1lIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9.f11807baab8bea27764a7cba524bc0f72c950ab9e59b9529d30251b0b6d487fa",
        "fhirType": "dateTime",
        "fieldPath": "meta.lastUpdated",
        "isIdentifier": false,
        "isPopulated": true,
        "isReference": false,
        "label": "Meta / LastUpdated",
        "logicalType": "date_time",
        "occurrenceId": "base",
        "valueType": "STRING"
      },
      {
        "choiceId": "rc1.eyJ2ZXJzaW9uIjoicm93LWNob2ljZS92MSIsImtpbmQiOiJGSUVMRF9HUk9VUF9LRVkiLCJzbmFwc2hvdFRva2VuIjoic2hhMjU2OmI3YjEzNTJjNzRiMTg0MjM1ZWU0NGJhMzlkZTA2OTEzNTk0ZWIzMGRlM2QwYzVjZjIwMjkyOGRhYjkyMTIxMTIiLCJzY2hlbWFEaWdlc3QiOiI5ZTk2MDkzNjM0NzJiM2RiZTgyNjMxYzdlYWY3NmM0M2UzYjlkNzA5MzBjZjcyYjIwNTJmNmU1ZjM2MGJkNGVlIiwib2NjdXJyZW5jZSI6eyJPY2N1cnJlbmNlSUQiOiJiYXNlIiwiTm9kZUlEIjoibl83ZTM1YWM4MTczYTNiODE1OWE0YjZjZjEiLCJSZXNvdXJjZVR5cGUiOiJQYXRpZW50IiwiUm91dGUiOltdfSwicGF0aCI6ImdlbmRlciIsImZoaXJUeXBlIjoic3RyaW5nIiwiY2FyZGluYWxpdHkiOiJPTkUiLCJzaGFwZSI6IlNDQUxBUiIsInJlZmVyZW5jZSI6ZmFsc2V9.600c609c72cde333edadfda1829f97d84807732fb8193a6b029c5e3ec0275b86",
        "fhirType": "string",
        "fieldPath": "gender",
        "isIdentifier": false,
        "isPopulated": true,
        "isReference": false,
        "label": "Gender",
        "logicalType": "string",
        "occurrenceId": "base",
        "valueType": "STRING"
      }
    ],
    "stageId": "source_projection",
    "supported": true
  },
  "sourceProjectionAvailability": {
    "stageId": "source_projection",
    "supported": true
  },
  "stageId": "source_projection",
  "stages": [
    {
      "capabilities": [
        {
          "kind": "DERIVE",
          "reason": "derive requires at least one public scalar numeric column or a numeric literal operand",
          "reasonCode": "NO_NUMERIC_COLUMN",
          "supported": false
        },
        {
          "kind": "FILTER",
          "supported": true
        },
        {
          "kind": "PIVOT",
          "reason": "pivot requires public scalar group, category, and value columns",
          "reasonCode": "INSUFFICIENT_SCALAR_COLUMNS",
          "supported": false
        },
        {
          "kind": "UNPIVOT",
          "supported": true
        },
        {
          "kind": "GROUP",
          "supported": true
        },
        {
          "kind": "CODED_GROUP",
          "supported": true
        },
        {
          "kind": "CODED_PIVOT",
          "supported": true
        },
        {
          "kind": "EXPAND",
          "reason": "expand requires a public array-valued column",
          "reasonCode": "NO_ARRAY_COLUMNS",
          "supported": false
        },
        {
          "kind": "RELATED_SOURCE",
          "supported": true
        },
        {
          "kind": "RELATED_EXPAND",
          "supported": true
        },
        {
          "kind": "RELATED_ELIGIBILITY",
          "supported": true
        },
        {
          "kind": "RELATED_FIELD",
          "reason": "related field requires the exact terminal resource identity to survive this stage",
          "reasonCode": "NO_ACTIVE_RELATED_RECORD",
          "supported": false
        }
      ],
      "columns": [
        {
          "cardinality": "optional_one",
          "id": "source_ef82eef4462a7f20fbcecfff",
          "label": "ID",
          "name": "col_cab5631258280f504d027209",
          "type": "string"
        }
      ],
      "groupFieldSourceContext": {
        "fieldOwners": [
          {
            "occurrenceId": "base",
            "ownerResourceType": "Patient",
            "resourceType": "Patient",
            "role": "ROOT",
            "stepIndex": 0
          }
        ],
        "sourceIdentityColumn": "_key",
        "sourceShapeId": "group-source-shape-v1:da01a0938c2a73057a5bcffa992f63fc01a7e18c8229e0f049de06ea975e41ba",
        "sourceStageId": "source_projection"
      },
      "id": "source_projection",
      "inputStageId": "",
      "relatedExpandAnchors": [
        {
          "anchorColumnId": "_key",
          "kind": "root",
          "label": "Original Patient",
          "resourceType": "Patient"
        }
      ],
      "rowIdentityColumn": "_key"
    }
  ],
  "workspaceInputs": []
}`;

export const liveFilterSourceUnmaterializedResponse: unknown = JSON.parse(
  liveFilterSourceUnmaterializedResponseJson,
);
