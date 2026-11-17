export const population_exclusion = [
  { name: 'current exclusion submits with current compare-and-swap identity', mode: 'current', stateVersion: 1, serverVersion: 1, expectedSubmit: true, expectedDraftVersion: 1 },
  { name: 'unrelated save refreshes compare-and-swap identity while exclusion is pending', mode: 'unrelated-save', stateVersion: 1, serverVersion: 2, expectedSubmit: true, expectedDraftVersion: 2 },
  { name: 'base selection change rejects the obsolete exclusion schedule', mode: 'base-change', stateVersion: 1, serverVersion: 2, expectedSubmit: false },
] as const;
