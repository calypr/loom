# Builder verification checkpoint

This checkpoint retains the source inventory, UI drivers, and Go lifecycle fixtures for the planned TLC-based Builder verification. It is based on application commit `ce0c726e5e56584b0b3b8d156cc20533d2ecbba4`.

The inventory describes 57 affordances and 159 request checkpoints from source commit `63e330d12cc62c30563e3735d6e33ab6f88afdb0`. Its source hashes deliberately retain that baseline. Later source changes require review before refreshing those hashes; a source-drift failure on the current checkout is expected. Coverage metadata remains pending.

The abandoned `full-ui` JavaScript state explorer and its comprehensive runner are excluded. The existing `ConstructionLifecycle.tla` and its runner remain the narrow proposal-ownership check. The operation-history verifier remains a separate bounded regression check. Neither establishes comprehensive Builder coverage.

The retained comprehensive UI capture and Go replay fixtures have no replacement TLC orchestrator yet. Their focused tests establish only the exercised adapter and fixture behavior. The expanded TLA+ model, TLC-to-implementation trace translation, and combined report are unfinished. The future report must distinguish design counterexamples, implementation contract failures, and unmodeled or untested behavior.

Product code and unrelated worktree changes are outside this checkpoint.
