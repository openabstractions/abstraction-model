namespace * abstraction.model.api
include "../abstraction-download/request.thrift"

// Shared model concepts; native binding supplements are explicit below.
encoding json {
 escape="minimal"
 indent="2"
 map_keys="utf8-bytes"
 numbers="integer-decimal"
 opaque="verbatim"
 terminator="newline"
 duplicate_keys="refuse"
 depth_limit="64"
}
refusal {
 1: malformed(stage="grammar")
 2: bad_string(stage="grammar")
 3: number_spelling(stage="grammar")
 4: wrong_type(stage="grammar")
 5: depth_exceeded(stage="grammar")
 6: duplicate_key(stage="grammar")
 7: duplicate_field(stage="structure")
 8: unknown_field(stage="structure")
 9: missing_field(stage="structure")
 10: bad_enum(stage="structure")
 11: trailing_bytes(stage="document")
}
struct Ref {
 1: required string registry
 2: required string repo
 3: required string revision
 4: required string quant
 5: required string file
}(document="true",unknown_fields="refuse",doc="Native model reference: hf uses repository, revision, optional quant or explicit file; ollama uses repository and tag revision. Empty revision means provider default. Custom registry schemes retain the opaque locator in repo. Parsing and provider validation remain native.")
const list<string> resolver_operations = ["Registry", "Resolve"]
const list<string> registry_operations = ["Add", "SetLocal", "Resolve"]
// Registry mutation and explicit local/store adapters remain native provider APIs.
enum LookupOutcome {
 1: resolved
 2: invalid
 3: unavailable
 4: unsupported_mapping
 5: forbidden
}(unknown="refuse")
struct LookupResult {
 1: required LookupOutcome outcome
 2: optional request.Request request (omit="absent")
}(unknown_fields="refuse",doc="Request is present exactly when outcome is resolved. Unavailable reports an unsuccessful lookup without declaring it transient or permanent. Unsupported_mapping preserves the refusal to discard native source metadata, credentials, private locations or destination authority. No job is submitted by lookup.")
service ModelResolver {
 LookupResult Resolve(1: Ref ref (rust.name = "reference"))
}(wire_name="abstraction.model/resolver@1",doc="Same-account identity-bound model lookup through explicitly configured registry providers. A resolved request contains a nonempty verified-format digest and a complete anonymous HTTP(S) mapping. Providers refuse mappings needing capabilities absent from the portable download request. Local adapters and registry configuration remain explicit provider-side APIs.")
