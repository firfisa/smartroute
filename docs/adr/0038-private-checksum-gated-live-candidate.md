# ADR-0038: Use a private checksum-gated package for live integration

Status: Accepted
Date: 2026-08-03

## Context

The active Clash profile is remote and binds several enhancement layers. Editing generated output is not durable, while blindly overwriting the active script risks losing user behavior or rolling back the wrong revision. A live trial therefore needs an exact candidate and backup tied to the current binding.

## Decision

Prepare a private package outside both the repository and active application directory. It contains the byte-exact original script, composed script, source and candidate checksums, redacted semantic diff, and private rollback metadata. Preparation transforms the current generated config and requires pinned Mihomo validation before the package becomes complete.

Installation and rollback are separate checksum-gated actions. They atomically replace only the active script, require explicit `--confirm-write`, and never reload Clash. Profile or script drift is a hard failure. The package is mode `0700`, files are `0600`, and the credential-bearing transformed full config is temporary only.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Edit generated `clash-verge.yaml` | Subscription/reload overwrites it |
| Replace the active script without preserving its body | Drops existing transformations |
| Copy transformed full config into the package | Unnecessarily duplicates node credentials and secrets |
| Install and reload in one command | Removes the operator's verification and rollback boundary |
| Ignore source drift | Can overwrite a newly selected or updated profile |

## Consequences

- Candidate generation and verification are fully read-only with respect to the active installation.
- A live window changes one file and has a byte-exact rollback source.
- Package loss is safe; it can be regenerated. Package disclosure is sensitive and must be treated like the active script.
- Reload and real-traffic validation remain separate, user-coordinated actions.

## Validation

The synthetic test covers private permissions, source immutability, semantic equality, pinned-Mihomo parsing, existing-output refusal, ambiguity cleanup, unconfirmed-write rejection, atomic install, exact rollback, and drift refusal. The current active package passed read-only verification; no install or reload was performed.
