---
description: Guided flow to review, validate, and register a copyguard or AST finding exception in accepted-findings.json
allowed-tools: Read, Edit, Bash
---

# /astgate-accept — register a validated copyguard exception in the accepted findings ledger

Use this skill when an AST gate check (astgate) reports a false positive or when a specific structural layout has been explicitly reviewed and approved by the Architect as a legitimate, safe exception.

## Validation Steps

Before modifying the accepted ledger, you must prove that the exception is safe:

1. **Enforce Inbound Path Isolation:** Ensure that the target symbol is not directly reachable from untrusted UI boundaries (violates Golden Rule #20).
2. **Review Encapsulation:** Confirm that the struct does not leak live mutable slices or keymaps.
3. **Verify Lock-Safety:** Ensure no lock-order inversions are introduced if synchronized fields are exposed.

## Execution Flow

1. **Locate Accepted Findings Ledger:** The SSOT is at `internal/foundation/astgate/accepted-findings.json`.
2. **Generate Finding Signature:** Extract the exact AST/diagnostic violation signature reported by the gate run.
3. **Write Entry:** Append the approved signature to `accepted-findings.json` with a required:
   - `reason`: Explaining why this is a safe, approved exception.
   - `approvedBy`: Naming the lead/architect who authorized the override.
   - `date`: ISO timestamp of approval.
4. **Local Verification:** Run the astgate check to verify that the violation is now cleanly bypassed and passes.
