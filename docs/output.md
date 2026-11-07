# Output contract, schema version 1

Every successful data command emits exactly one JSON object to stdout:

```json
{"schemaVersion":"1","data":[],"meta":{"source":"play-developer-reporting","dataStatus":"no_data","profile":"default","fetchedAt":"2026-11-07T00:00:00Z"}}
```

An error also emits exactly one JSON object to stdout, including in table mode:

```json
{"schemaVersion":"1","data":null,"meta":{"profile":"default"},"error":{"code":"INVALID_ARGUMENT","message":"Google Play writes require --confirm; use --dry-run to preview local inputs","retryable":false}}
```

Progress and diagnostics belong on stderr. `--output table` replaces successful
JSON with a table of flattened fields and emits metadata as JSON on stderr.
Human-oriented `--help` remains text; agents should use `schema` for discovery.
Operational JSON fields and exit meanings follow this versioned contract;
changes to their meaning require a new schema version. New optional fields may
be added. Google-provided resource fields and enums may evolve upstream.

| Exit | Meaning | Typical codes |
|---|---|---|
| 0 | Success, including no data | — |
| 1 | Unexpected local failure | `INTERNAL_ERROR` |
| 2 | Invalid input, report format or Google validation rejection | `INVALID_ARGUMENT`, `REPORT_FORMAT`, `GOOGLE_REJECTED` |
| 3 | Authentication, permissions or failed doctor | `AUTH_CONFIGURATION`, `AUTH_PERMISSION`, `DOCTOR_FAILED` |
| 4 | State conflict or uncertain commit | `CONFLICT`, `COMMIT_OUTCOME_UNKNOWN` |
| 5 | Network, quota or service failure | `NETWORK_ERROR`, `SERVICE_UNAVAILABLE`, `PAGINATION_ERROR` |

`retryable` may be true for a transient **read** failure. Release write failures
always set it to false. Inspect the operation and fresh server state before
retrying any write, regardless of the exit code. `error.details` can contain
diagnostic results or identifiers needed for recovery; it never contains keys
or access tokens. Google validation messages are retained for actionable input
errors, but raw response bodies and headers are not returned.

v0.2 adds `PREFLIGHT_FAILED` (exit 2, checks in details) and `LISTING_NOT_FOUND`
(exit 2), plus `INVALID_RESPONSE` (exit 5, do not replay writes). Google validation messages are scrubbed for private keys, credential
tokens and control characters while retaining actionable validation context.
New commands use the same schema version 1 envelope. Plans/snapshots have their
own `formatVersion: 1`; plan digests use canonical JSON and are not signatures.

Release verification returns an inspection status independently of process exit:
`match`, `drift`, `pending` (explicit lifecycle evidence only), or `unknown`.
Completed inspections return exit 0; permission/network failures remain errors.
`missingEvidence` explains uncertainty. `committed: null` means readback cannot
establish which transaction committed the state. Image inheritance uses
`unknown`/null rather than fabricated booleans. Incomplete lifecycle snapshots
have `complete: false`, missing fields and no usable planning baseline.
