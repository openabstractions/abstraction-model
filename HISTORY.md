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

## 2026-10-21: contract truth pass (0.8.0 W0-03)

The pass checked `CONTRACT.md` and the README against current source
(`research/release-0.8.0/audit/model.md` §3.8, P1-6). Each change, with its
class:

- "Not built: Python service transport." Outdated rule: the facade's
  `Machine().resolve_model` carries the shared transport. "Not built" now
  names the missing Python conformance record.
- `manifest@1` read as served. Outdated rule: no server registers it; the
  opening paragraph names the one served service, `resolver@1`.
- The README's "11 tests" described the in-process tests only. Outdated
  rule: it now names the service, credential, descriptor and client tests.
- Two Python packages, `abstraction-model` and `abstraction-model-protocol`.
  Accepted divergence: both stay, and both carry one version from 0.8.0
  (`VISION.md:1230-1248`).
- `model.thrift` publishes the native Go method names as constants. Code
  defect: a TODO repair row.
- `ARCHITECTURE.md:49` says "catalogue" and the contract has no catalogue
  clause. Outdated rule outside this page: a TODO row; the pinned model-set
  record (audit P1-1) is the word's owner when it lands.
- The remote form is stated as "Not built" with the receiver rule of
  `research/release-0.8.0/critique/security.md` item 15.
