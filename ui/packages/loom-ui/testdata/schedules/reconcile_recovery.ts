export const reconcile_recovery = {
  "acceptance": [
    {
      "name": "current receipt accepted",
      "identity": {
        "generation": 2,
        "currentGeneration": 2,
        "submittedExplorerId": "patients",
        "currentExplorerId": "patients",
        "submittedFingerprint": "F1",
        "currentFingerprint": "F1"
      },
      "request": {
        "snapshotToken": "S1",
        "outputId": "patients"
      },
      "value": {
        "snapshotToken": "S1",
        "outputs": [
          {
            "outputId": "patients"
          }
        ],
        "diagnostics": []
      },
      "expected": "accepted"
    },
    {
      "name": "late generation is obsolete",
      "identity": {
        "generation": 1,
        "currentGeneration": 2,
        "submittedExplorerId": "patients",
        "currentExplorerId": "patients",
        "submittedFingerprint": "F1",
        "currentFingerprint": "F1"
      },
      "request": {
        "snapshotToken": "S1",
        "outputId": "patients"
      },
      "value": {
        "snapshotToken": "S1",
        "outputs": [
          {
            "outputId": "patients"
          }
        ],
        "diagnostics": []
      },
      "expected": "obsolete"
    },
    {
      "name": "receipt for another snapshot is rejected",
      "identity": {
        "generation": 2,
        "currentGeneration": 2,
        "submittedExplorerId": "patients",
        "currentExplorerId": "patients",
        "submittedFingerprint": "F1",
        "currentFingerprint": "F1"
      },
      "request": {
        "snapshotToken": "S1",
        "outputId": "patients"
      },
      "value": {
        "snapshotToken": "S2",
        "outputs": [
          {
            "outputId": "patients"
          }
        ],
        "diagnostics": []
      },
      "expected": "mismatch"
    },
    {
      "name": "error diagnostics are not admitted",
      "identity": {
        "generation": 2,
        "currentGeneration": 2,
        "submittedExplorerId": "patients",
        "currentExplorerId": "patients",
        "submittedFingerprint": "F1",
        "currentFingerprint": "F1"
      },
      "request": {
        "snapshotToken": "S1",
        "outputId": "patients"
      },
      "value": {
        "snapshotToken": "S1",
        "outputs": [
          {
            "outputId": "patients"
          }
        ],
        "diagnostics": [
          {
            "severity": "error",
            "code": "INVALID"
          }
        ]
      },
      "expected": "diagnostics"
    }
  ],
  "recovery": [
    {
      "name": "stale snapshot refreshes before retry limit",
      "error": {
        "code": "STALE_CATALOG_SNAPSHOT",
        "retryable": false
      },
      "options": {
        "attempt": 1,
        "maxAttempts": 3,
        "staleRefreshes": 0,
        "maxStaleRefreshes": 2
      },
      "expected": "refresh-and-retry"
    },
    {
      "name": "draft conflict refreshes and stops",
      "error": {
        "code": "DRAFT_CONFLICT",
        "retryable": false
      },
      "options": {
        "attempt": 1,
        "maxAttempts": 3,
        "staleRefreshes": 0,
        "maxStaleRefreshes": 2
      },
      "expected": "refresh-and-stop"
    },
    {
      "name": "transient failure retries while budget remains",
      "error": {
        "code": "BACKEND_UNAVAILABLE",
        "retryable": true
      },
      "options": {
        "attempt": 2,
        "maxAttempts": 3,
        "staleRefreshes": 0,
        "maxStaleRefreshes": 2
      },
      "expected": "retry"
    },
    {
      "name": "retry limit returns repair",
      "error": {
        "code": "BACKEND_UNAVAILABLE",
        "retryable": true
      },
      "options": {
        "attempt": 3,
        "maxAttempts": 3,
        "staleRefreshes": 0,
        "maxStaleRefreshes": 2
      },
      "expected": "repair"
    },
    {
      "name": "stale refresh limit returns repair",
      "error": {
        "code": "STALE_SNAPSHOT",
        "retryable": false
      },
      "options": {
        "attempt": 1,
        "maxAttempts": 3,
        "staleRefreshes": 2,
        "maxStaleRefreshes": 2
      },
      "expected": "repair"
    }
  ]
} as const;

export const reconcile_callback_schedules = [
  { name: 'stale snapshot refreshes then retries with the fresh catalog', mode: 'stale-refresh-retry', actions: ['ChooseRequest', 'RefreshCatalog', 'RetryRequest', 'AcceptReceipt'], expectedRequests: 2, expectedRefetches: 1, expectedPreview: true },
  { name: 'draft conflict refreshes then stops without retry', mode: 'conflict-refresh-stop', actions: ['ChooseRequest', 'RefreshCatalog', 'Stop'], expectedRequests: 1, expectedRefetches: 1, expectedPreview: false },
  { name: 'transient failure retries within its attempt budget', mode: 'transient-retry', actions: ['ChooseRequest', 'Backoff', 'RetryRequest', 'AcceptReceipt'], expectedRequests: 2, expectedRefetches: 0, expectedPreview: true },
  { name: 'transient retries exhaust and enter repair', mode: 'retry-exhaustion', actions: ['ChooseRequest', 'Backoff', 'RetryRequest', 'Backoff', 'RetryRequest', 'Repair'], expectedRequests: 3, expectedRefetches: 0, expectedPreview: false },
  { name: 'late stale failure after owner detach cannot refresh or retry', mode: 'owner-detach', actions: ['ChooseRequest', 'DetachOwner', 'RejectStale'], expectedRequests: 1, expectedRefetches: 0, expectedPreview: false },
  { name: 'late stale failure after draft identity advance cannot refresh or retry', mode: 'identity-advance', actions: ['ChooseRequest', 'AdvanceDraft', 'RejectStale'], expectedRequests: 1, expectedRefetches: 0, expectedPreview: false },
  { name: 'refresh-and-retry stops if the owner changes while refetch is pending', mode: 'stale-refetch-invalidated', actions: ['ChooseRequest', 'RejectStale', 'AwaitRefresh', 'ChangeOwner', 'ResumeRefresh'], expectedRequests: 1, expectedRefetches: 1, expectedPreview: false },
  { name: 'refresh-and-retry preserves a same-owner draft advanced while refetch is pending', mode: 'stale-refetch-draft-changed', actions: ['ChooseRequest', 'RejectStale', 'AwaitRefresh', 'AdvanceDraft', 'ResumeRefresh'], expectedRequests: 1, expectedRefetches: 1, expectedPreview: true },
  { name: 'refresh-and-stop does not synchronize a late DTO after Explorer change', mode: 'conflict-refetch-invalidated', actions: ['ChooseRequest', 'RejectConflict', 'AwaitRefresh', 'ChangeExplorer', 'ResumeRefresh'], expectedRequests: 1, expectedRefetches: 1, expectedPreview: false },
  { name: 'draft conflict with no refreshed state repairs without retry', mode: 'conflict-no-data', actions: ['ChooseRequest', 'RejectConflict', 'AwaitRefresh', 'ResumeWithoutData'], expectedRequests: 1, expectedRefetches: 1, expectedPreview: false },
  { name: 'transient retry stops after workspace detaches during backoff', mode: 'backoff-detach', actions: ['ChooseRequest', 'RejectTransient', 'AwaitBackoff', 'DetachOwner', 'ResumeBackoff'], expectedRequests: 1, expectedRefetches: 0, expectedPreview: false },
  { name: 'automatic reconcile canceled by workspace detach does not retry', mode: 'automatic-cancel', actions: ['ChooseRequest', 'CancelOwner', 'RejectCancelled'], expectedRequests: 1, expectedRefetches: 0, expectedPreview: false },
] as const;
