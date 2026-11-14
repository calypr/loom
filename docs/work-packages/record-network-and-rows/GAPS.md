# Finish the two recipe editors

The follow-up implementation starts from planning commit
`9613310fefacc987933275b9a9f92f3e456e886a`, preserving the original `58544dcbb`
work. Implementation is integrated; verification remains in progress. The earlier
Calypr screenshots explain the product requirement, not current acceptance.
[VERIFICATION.md](VERIFICATION.md) separates historical and current evidence.

Both **Record network** and **Define a row** now edit the same authoritative recipe.
The ephemeral owner coordinates one candidate, Apply, Cancel, errors and stale
responses across both interfaces. [EDITING-CONTRACT.md](EDITING-CONTRACT.md)
records exact fact identities, typed APIs, supported edits and boundaries.

## Intended experience and supported workflow

Users can work mainly in either editor or alternate between them. Both remain easy
to reach and share the same result preview. The graph explains how records connect;
the row expression explains how those records become rows and column values.
Neither interface owns a separate saved recipe.

For example, connect ResearchSubject to Patient and Observation in the graph,
then define one row per subject with observation values retained as a list. An
explicit related-record expansion changes this to one row per subject–observation
pair while preserving that list; a per-match scalar is a separate choice. Group by
chosen fields as another explicit operation. The graph distinguishes the source
relationships from these output-row stages, including the complete Group key tuple.
Required matches can filter rows without expanding them. Existing other operations
remain ordered, inspectable and intact when users switch interfaces.

## Integrated behavior and remaining acceptance

| Area | Integrated behavior | Remaining proof | Ticket |
| --- | --- | --- | --- |
| Shared editing | One proposal owner; receipt-bound source, row, column and root candidates; presentation switching does not mutate the plan | Owned final134 proof passes; corresponding authenticated host journey/frame remains unverified (missing token). | 08, 06, 07 |
| Row expression | Editable connections, source rows, Include policies and exact existing operations; complete Group tuple; explicit expansion | Owned final134 proof passes; corresponding authenticated host journey/frame remains unverified (missing token). | 09, 07 |
| Traversal | Authored occurrence paths distinct from catalog possibilities; row-stage, pair and Group annotations | Owned final134 proof passes; corresponding authenticated host journey/frame remains unverified (missing token). | 10, 07 |
| Workspace | Both/focused editors, one inspector, shared saved/proposed preview, bounded history/catalogs | Owned final134 proof passes; corresponding authenticated host journey/frame remains unverified (missing token). | 11, 07 |
| Persistence | Existing complete-plan receipt/CAS paths reused; pins and unrelated operations preserved | Owned final134 proof passes; corresponding authenticated host journey/frame remains unverified (missing token). | 06, 07 |
| Product acceptance | Independent fixtures, production checks, reviewed 12-case native registration | Owned final134 proof passes; corresponding authenticated host journey/frame remains unverified (missing token). | 07 |

Imported preserve-root changes remain blocked; explicit replacement discloses pin
detachment and reset. Imported in-place edge replacement is unavailable: removal
with dependency review and separate addition remain explicit. Independent related
products and source `GROUPS/FIELD` remain unsupported. Legacy columns without stable
IDs remain intact/readable; the new stable-ID policy proposal is unavailable for
those columns and existing source settings remain accessible. No second persisted
canvas, new compiler, or LLM integration was introduced.

Completion requires independently expected exact rows and values, equivalent
canonical semantics, native persistence/ownership, and legible editing in the
actual Calypr frame. Automated passes and case registration do not close that scope.


Final closure: implementation and all12 owned native cases/105actions pass at134faf8b…,
including canonical graph/row equivalence and100-operation layout/lifecycle. The
remaining gap is authenticated host acceptance: missing token, host authoring journeys
and actual267-column Calypr frame unverified. Earlier remaining-proof rows describe
original acceptance goals; their owned scope is now satisfied, as VERIFICATION.md records.
