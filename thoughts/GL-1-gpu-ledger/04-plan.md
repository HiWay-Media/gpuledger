# 04 · Plan — GL-1 A ledger of who holds which GPU on a Nomad node


---

## References

- Structure: [`03-structure.md`](./03-structure.md)
- Design: [`02-design.md`](./02-design.md)

---

## Minimum context for the executor

Everything the executor needs to know so it never has to explore:

| Fact | Where |
|---|---|
| <e.g. services use the Result[T, Err] pattern> | `src/core/result.py:1-40` |
| <e.g. tests run with `make test`> | `Makefile:12` |

**Conventions to respect:** <naming, error handling, logging, import order>

---

## S1 · <title>

### Files touched

| Path | Action |
|---|---|
| `src/services/x.py` | modify |
| `tests/services/test_x.py` | new |

### Changes

#### `src/services/x.py`

**Where:** inside `class XService`, after `def get()` (line ~85).

**What to add:**

```python
def process_refund(self, order_id: str, amount: Decimal) -> Result[Refund, RefundError]:
    """<one line>"""
```

**Expected behaviour:**
1. <step>
2. <step>
3. Errors: `RefundError.NOT_FOUND` if the order does not exist;
   `RefundError.EXCEEDS_TOTAL` if `amount` is above the order total.

**Do NOT touch:** `def get()` — used by `src/api/orders.py:44`.

### Tests

| Case | Input | Expected |
|---|---|---|
| happy path | valid order, partial amount | `Ok(Refund(...))` |
| missing order | `order_id="nope"` | `Err(NOT_FOUND)` |
| amount too high | amount > total | `Err(EXCEEDS_TOTAL)` |
| zero amount | `Decimal("0")` | `Err(INVALID_AMOUNT)` |

### Verify

```bash
pytest tests/services/test_x.py -q
ruff check src/services/x.py
mypy src/services/x.py
```

### Acceptance criteria

- [ ] Every test case above passes
- [ ] No regressions: `make test` green
- [ ] No new lint or type warnings

---

## S2 · <title>

<same structure>

---

## Rollback

How to back out if something goes wrong in production:

- <feature flag / commit revert / down migration>

---

## Status

- [ ] Every step has exact paths
- [ ] Every new function has a complete signature
- [ ] Every test case has inputs and expected outputs
- [ ] Every verification command is copy-pasteable
- [ ] **Zero-context test:** an agent reading only this file can execute it
- [ ] Rollback plan present

> Next phase: **Implement**. It receives: this file + `99-progress.md`.
> One session per step.
