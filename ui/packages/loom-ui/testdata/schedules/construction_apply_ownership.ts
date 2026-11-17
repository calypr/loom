export const construction_apply_ownership = [
  {
    "name": "matching completion settles its owner",
    "owner": "A",
    "requestKey": "workspace-1",
    "generation": 1,
    "applied": true,
    "current": {
      "owner": "A",
      "requestKey": "workspace-1",
      "generation": 1
    },
    "accepted": true,
    "settlements": [
      true
    ]
  },
  {
    "name": "old generation completion is ignored",
    "owner": "A",
    "requestKey": "workspace-1",
    "generation": 1,
    "applied": true,
    "current": {
      "owner": "A",
      "requestKey": "workspace-1",
      "generation": 2
    },
    "accepted": false,
    "settlements": []
  },
  {
    "name": "old workspace completion is ignored",
    "owner": "A",
    "requestKey": "workspace-1",
    "generation": 1,
    "applied": true,
    "current": {
      "owner": "A",
      "requestKey": "workspace-2",
      "generation": 1
    },
    "accepted": false,
    "settlements": []
  },
  {
    "name": "owner-only change rejects completion with the same request and generation",
    "owner": "A",
    "requestKey": "workspace-1",
    "generation": 1,
    "applied": true,
    "current": {
      "owner": "B",
      "requestKey": "workspace-1",
      "generation": 1
    },
    "accepted": false,
    "settlements": []
  },
  {
    "name": "matching unapplied completion settles false",
    "owner": "A",
    "requestKey": "workspace-1",
    "generation": 1,
    "applied": false,
    "current": {
      "owner": "A",
      "requestKey": "workspace-1",
      "generation": 1
    },
    "accepted": true,
    "settlements": [false]
  },
  {
    "name": "duplicate completion settles only once",
    "owner": "A",
    "requestKey": "workspace-1",
    "generation": 1,
    "applied": true,
    "current": {
      "owner": "A",
      "requestKey": "workspace-1",
      "generation": 1
    },
    "accepted": true,
    "duplicateCompletionRejected": true,
    "settlements": [true]
  },
  {
    "name": "cancelled owner cannot settle the replacement attempt",
    "owner": "A",
    "requestKey": "workspace-1",
    "generation": 1,
    "replacement": {
      "owner": "B",
      "requestKey": "workspace-2",
      "generation": 2,
      "applied": true
    },
    "accepted": false,
    "replacementAccepted": true,
    "settlements": [
      true
    ]
  }
] as const;

export const construction_apply_admission = [
  {
    name: 'occupied Apply slot refuses a second begin without replacing the first attempt',
    current: { owner: 'A', requestKey: 'workspace-1', generation: 1 },
    requested: { owner: 'B', requestKey: 'workspace-2', generation: 2 },
    expectedAdmitted: false,
  },
] as const;
