# Draft authoring

## Sub-features

Document creation, route changes, field selections, presentation settings, commands, and reconcile.

## How to get to it (user POV)

Load Builder, create or select an output document, then change its route or selected columns.

## Driving it with Chrome CDP

Not automated yet. Use a dedicated Explorer before adding write actions and assert the returned draft version and digest after each command.

## Gotchas

Commands use compare-and-swap draft identity. Never automate writes against a researcher-owned Explorer.
