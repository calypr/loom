# External Calypr UI coverage

- [Builder load](builder-load.md): authenticated routing, API envelope, visible load failure, and browser diagnostics.
- [Catalog navigation](catalog-navigation.md): returned catalog summary; navigation itself is not automated.

The read-only helper covers initial Builder loading on the external Calypr
deployment, which is not managed in this repository. Draft authoring, preview,
publication, graph navigation, and recovery after clicking retry are not covered.
A new write-capable case needs a dedicated owned Explorer and explicit scope.
Local Builder e2e cases are documented in [verify](../../verify/SKILL.md).
