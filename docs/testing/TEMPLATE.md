<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: <feature area>

> Copy this file into `protocols/<slug>.md` and fill it in. Keep cases
> small and independent — one observable behaviour each. Number them so
> an automated spec can reference the same numbers.

| Field | Value |
|-------|-------|
| **Area** | <e.g. Authentication, Matcher rules, Health> |
| **Owner** | <name> |
| **Automation status** | Manual only · Partially automated · Automated |
| **Automated by** | <path to spec, e.g. `e2e/tests/<x>.spec.ts>` — or "—"> |
| **Last reviewed** | <YYYY-MM-DD> |

## Preconditions

- <Stack state, e.g. "local stack up (`make dev-up`)">
- <Data, e.g. "seed traces sent (`make seed-traces`)">
- <Account/role, e.g. "signed in as org admin">

## Cases

### Case 1 — <short title>

| | |
|--|--|
| **Goal** | <what this proves> |
| **Steps** | 1. <do this><br>2. <then this> |
| **Expected** | <observable result> |
| **Automated** | Yes (`spec.ts` → "<test name>") · No |

### Case 2 — <short title>

| | |
|--|--|
| **Goal** | … |
| **Steps** | 1. …<br>2. … |
| **Expected** | … |
| **Automated** | … |

## Notes

- <Known gaps, environment quirks, data dependencies.>
