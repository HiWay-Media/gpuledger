# 03 · Structure — GL-1 A ledger of who holds which GPU on a Nomad node


---

## Reference

Design: [`02-design.md`](./02-design.md)

---

## Steps

### S1 · <title>

- **Goal:** <one line>
- **Touches:** `src/...`, `tests/...`
- **Depends on:** — (none)
- **Verify:** `pytest tests/test_x.py -q` passes
- **Repo state after:** working, feature not yet exposed

### S2 · <title>

- **Goal:**
- **Touches:**
- **Depends on:** S1
- **Verify:**
- **Repo state after:**

### S3 · <title>

- **Goal:**
- **Touches:**
- **Depends on:** S1
- **Verify:**
- **Repo state after:**

---

## Dependency graph

```
S1 ──┬── S2 ──── S4
     └── S3 ──────┘
```

**Parallelisable:** S2 and S3 (separate worktrees, no shared files).

---

## Recommended execution order

1. S1
2. S2 ‖ S3
3. S4

---

## Per-step risks

| Step | Risk | Fallback |
|---|---|---|
| | | |

---

## Status

- [ ] Decomposition complete
- [ ] Every step has a verification command
- [ ] Every step leaves the repo working
- [ ] Dependencies and parallelism mapped

> Next phase: **Plan**. It receives: this file + `02-design.md`.
