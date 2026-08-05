# ADR-0042: Verify the resolved active script binding instead of whole-profile metadata

Status: Accepted
Date: 2026-08-05

## Context

ADR-0038 required a live package to fail closed when the active Clash profile or script drifted. The first live trial originally implemented that rule by hashing all of `profiles.yaml`. Clash Verge Rev later rewrote unrelated registry metadata while keeping the same current profile, script reference, script file, and candidate bytes. The manager therefore rejected a safe verification and rollback even though the security-relevant binding had not changed.

The whole file contains more state than SmartRoute owns. Treating every metadata rewrite as a script-binding change blocks recovery without protecting a different target.

## Decision

Every verify, install, and rollback operation reparses `profiles.yaml` and resolves this exact chain:

```mermaid
flowchart LR
    P["profiles.yaml current UID"] --> C["Exactly one current item"]
    C --> R["option.script UID"]
    R --> S["Exactly one script item"]
    S --> F["file under profiles directory"]
    F --> A["Exact active_script_path in private package"]
    A --> H["Original or candidate content SHA-256"]
```

The operation fails closed if parsing fails, an item is missing or duplicated, the resolved path escapes the profiles directory, the resolved file is not the package's exact active path, or its content is neither the verified original nor candidate state required by the action. Unrelated metadata changes are allowed. The preparation-time whole-file checksum remains package evidence but is no longer an execution gate.

## Alternatives

| Alternative | Reason rejected |
| --- | --- |
| Continue requiring the whole-file hash | Blocks rollback after harmless Verge metadata maintenance |
| Ignore `profiles.yaml` after package creation | Could overwrite a script that is no longer active |
| Match only the stored script filename | Does not prove the current profile still references it and is weaker against path ambiguity |
| Automatically update the package fingerprint | Mutates recovery authority after the fact and can bless real drift |

## Consequences

- Verge may update unrelated registry metadata without disabling verified recovery.
- A real profile or script rebinding still fails before any active write.
- The package remains tied to one exact active script path and two exact content states.
- YAML parsing becomes part of every management action and malformed input is an explicit stop condition.

## Validation

`make active-candidate-test` changes unrelated synthetic profile metadata and requires verification to succeed, then binds the current profile to a different valid script and requires verification to fail. Exact install and byte-for-byte rollback continue to pass. The live candidate with changed registry metadata verified as `candidate` after the change.
