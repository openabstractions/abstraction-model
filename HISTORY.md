# History: abstraction.model

Linked from [CONTRACT.md](CONTRACT.md)'s "Reading this page."

## Superseded

- **`Inventory()`** (Go package). Removed; local-store discovery moved to
  `abstraction.storage/inventory@1`, composed by the runtime over its
  declared sources. Recorded in
  [docs/REMOVED.md](https://github.com/openabstractions/abstractions/blob/main/docs/REMOVED.md#12-2026-09-22-go-modelinventory).

## Design records

- `research/vocabulary/DECISION.md` D30 — a still-shipped earlier interface
  is named `deprecated`. Applied here to the in-process resolver
  (`model.NewRegistry`, `model.Default`) that `modelget` and the
  local-store adapters `local.go` uses run directly, without a runtime.
- `research/vocabulary/DECISION.md` §3 (S1–S13) — the contract shape this
  page and its "Reading this page" section follow.

## Reviewed and left unchanged

- `research/reviews/providers-2026-09-23.md` F13 — "Family" in modelbridge's
  `LEND-6` and `LEND-7` is not tied to `MODEL-C2`. `MODEL-C2` already closes
  the descriptor keys, `family` among them; the fix (`Lend` gains `family`,
  `LEND-6` cites `MODEL-C2`) belongs to
  `abstraction-provider-modelbridge/CONTRACT.md`.
- `research/reviews/providers-2026-09-23.md` F15 — a digest field the
  descriptor does not have. `MODEL-C2`'s descriptor keys carry no `digest`;
  the fix ("the entry's `digest`") belongs to
  `abstraction-storage-over-local-stores/CONTRACT.md`.
- `research/reviews/router-2026-09-23.md` F5 — `ROUTE-7`'s tail match on a
  host's aliases; no descriptor or family rule of this contract changes.
- `research/reviews/router-2026-09-23.md` F10 — confirms `quant` and
  `format`, not `build`, are `MODEL-C2`'s descriptor keys; the fix
  (`ROUTE-5`'s wording) belongs to `abstraction-router/CONTRACT.md`.
