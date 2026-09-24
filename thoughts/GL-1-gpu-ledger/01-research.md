# 01 · Research — GL-1 A ledger of who holds which GPU on a Nomad node


This is the most expensive phase and the one with the highest compression ratio
(~30-50×). It is also the one that most needs subagents.

---

## Reference questions

From `00-questions.md` — the answers that steer this research:

- <Q1 → answer>
- <Q2 → answer>

---

## Map of the territory

### Components involved

| Area | Path | Role |
|---|---|---|
| <name> | `src/...` | <one line> |

### Entry points

| Symbol | Path:line | What it does |
|---|---|---|
| `<func>` | `src/...:120` | <one line> |

---

## Existing patterns and conventions

### <Pattern 1>

- **Where:** `src/...:45-80`
- **How it works:** <2-3 lines>
- **Who already uses it:** `src/a.py:12`, `src/b.py:200`

---

## Constraints

| Constraint | Source | Impact |
|---|---|---|
| <e.g. the `status` column is a DB enum> | `migrations/0042_*.sql:8` | <one line> |

---

## Reuse candidates

What already exists and should not be rewritten:

| What | Path | Note |
|---|---|---|
| | | |

---

## Existing tests

| What it covers | Path | How to run it |
|---|---|---|
| | | |

---

## Blind spots

Things you could not determine, and why:

- <...>

---

## Facts that contradict the assumptions

If research disproved an assumption from `00-questions.md`, write it here in large
letters. It is the most valuable output of the phase.

- <...>

---

## Status

- [ ] Research complete
- [ ] Self-contained (explicit paths, no reference to session context)
- [ ] Zero solution proposals
- [ ] Reviewed

> **Compression ratio:** <tokens burned> → <artifact tokens> = <N>×
> Next phase: **Design**. It receives: this file + `00-questions.md` + the ticket.
