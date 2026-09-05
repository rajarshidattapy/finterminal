# finterminal

**The AI-native terminal for Razorpay finance operations.**

> The model plans and explains. It does not calculate, and it does not move money.
> Everything in this repository exists to make that sentence true.

Ask questions in English, Hindi or Hinglish; get answers computed in Go, with an
audit trail, a policy-gated write plane, and an eval suite that fails the build
if the model starts inventing figures.

```
$ finterminal "why did revenue drop this week"

  Captured revenue fell 25.6% period over period.

    This period  (31 Aug – 5 Sep)     ₹13,27,800  ·  161 payments
    Previous     (25 Aug – 30 Aug)     ₹17,83,700  ·  219 payments
    Difference                        −₹4,55,900

    Success rate       86.2% → 77.4%  (8.8pp)
    Failed payments    27 → 43
    Unpaid high-value  10 orders   ₹2,72,700 outstanding

    upi               ₹7,58,400  ·   103 captured · 34 failed
    card              ₹3,41,900  ·    39 captured · 5 failed
    netbanking        ₹1,52,900  ·    12 captured · 3 failed
    wallet              ₹74,600  ·     7 captured · 1 failed

  ↳ 4 queries over 472 rows · 0ms · /explain for detail
```

## Built on Razorpay's open source

Two Razorpay projects sit next to each other and don't talk. This is the layer
between them.

| Repo | What it is | What it contributes here |
|---|---|---|
| [**razorpay/razorpay-cli**](https://github.com/razorpay/razorpay-cli) | Go/Cobra CLI over the Razorpay API, MIT | The home this belongs in. `rzp-ai` is designed to land as `cmd/ai/` — a `razorpay ai` subcommand following the repo's one-package-per-resource extension pattern — and to import its `api/` package directly as a Go library for the resources MCP doesn't expose (invoices, disputes, customers, subscriptions, Route, Smart Collect). |
| [**razorpay/razorpay-mcp-server**](https://github.com/razorpay/razorpay-mcp-server) | Go MCP server exposing ~45 Razorpay tools, MIT | The primary executor. `internal/mcp` speaks stdio JSON-RPC to it and launches it with `--read-only` for every non-elevated session, so a read session physically cannot mutate. It's the officially maintained AI surface, and using it is the point. |

Both share one gap: **no aggregation.** Every tool and command is fetch-one or
list-with-filters — nothing computes a trend, a rate or a delta. That gap is
what `internal/analytics` fills, and filling it in Go rather than in a prompt is
what makes the answers auditable.

The `internal/mcp` client is written against the documented flags (`--key`,
`--secret`, `--read-only`, `--toolsets`) and needs a `razorpay-mcp-server`
binary to exercise; `sync` currently uses the bundled fixture generator so the
project runs with no credentials at all.

## Eval results

Run them yourself: `finterminal eval` (or `go test ./...`).

| Set | What it checks | Result |
|---|---|---|
| **A · planning accuracy** | 60 utterances — paraphrases, Hinglish, typos, ambiguous windows. Exact match on capability, tolerance match on window. Target ≥ 90%. | **60/60 (100%)** |
| **B · numeric fidelity** | 22 assertions. Every reported figure recomputed by an independent row-by-row path, plus the guard that rejects any numeral absent from the computed fact set. | **22/22 (100%)** |
| **C · safety** | 29 adversarial cases — injection, over-ceiling refunds, already-refunded payments, "skip the confirmation", write attempts in a read session, non-TTY writes. Any failure blocks release. | **29/29 (100%)** |

## Install and run

```bash
go build -o finterminal .
./finterminal sync                              # seeds the local read model, ~150ms
./finterminal "why did revenue drop this week"
./finterminal                                   # REPL
```

No credentials are needed: `sync` ships with a deterministic fixture generator
(90 days, ~4,000 payments, a real revenue drop driven by a UPI failure spike in
a bank downtime window) so a reviewer goes from `git clone` to a working answer
in under a minute. Set `ANTHROPIC_API_KEY` to enable the LLM planner and
narrator — **the numbers are identical either way, because Go computes them.**

The REPL opens on the wordmark, drawn in Razorpay's brand blues — Dodger Blue
(`#0D94FB`) fading toward Green Vogue (`#012652`):

```
  ░█████████  ░█████████ ░█████████             ░███    ░██████
  ░██     ░██       ░██  ░██     ░██           ░██░██     ░██
  ░██     ░██      ░██   ░██     ░██          ░██  ░██    ░██
  ░█████████     ░███    ░█████████  ░██████ ░█████████   ░██
  ░██   ░██     ░██      ░██                 ░██    ░██   ░██
  ░██    ░██   ░██       ░██                 ░██    ░██   ░██
  ░██     ░██ ░█████████ ░██                 ░██    ░██ ░██████
  the model plans and explains — it does not calculate, and it does not move money

  rzp-ai · test mode · read-only · synced 2m ago · 4,056 payments · rules + templates

rzp-ai>
```

`WRITE ENABLED` and `live mode` are painted so they cannot be skimmed past.
Colour degrades to plain ASCII when piped, when `NO_COLOR` is set, with
`--no-color`, or on a terminal that won't take ANSI.

```bash
finterminal                              # REPL
finterminal "settlement total" --json    # scriptable, no narration
finterminal --write                      # enables the write plane
finterminal sync [--days 90]             # refresh the local mirror
finterminal audit [--last 20]            # read the append-only audit log
finterminal eval [--set a|b|c|all]       # run the eval harness
finterminal version                      # wordmark and version
finterminal help                         # every flag, including --no-color
```

## Architecture

```
   utterance ─▶ PLANNER ─┬─▶ READ PLANE ──▶ analytics (Go, exact) ─┐
                (LLM #1) │    SQL over the local mirror            │
                or rules │                                        ▼
                         └─▶ WRITE PLANE ──▶ policy engine     NARRATOR ─▶ answer
                              · allowlist   · ceilings         (LLM #2,
                              · re-read     · idempotency     no tools)
                              · human confirmation                │
                                                                  ▼
                                                            AUDIT (JSONL)
```

| Package | Role |
|---|---|
| `internal/planner` | Utterance → typed `QueryPlan` / `WriteIntent` / refusal. Deterministic rules first, LLM only for what the rules decline. No SQL, URLs or free-form fields in the contract. |
| `internal/analytics` | Every number the program prints. Exact integer arithmetic over paise. Emits a `FactSet` — the closed set of values a narration may contain. |
| `internal/narrator` | `FactSet` → English. No tools, no DB handle. `Guard` rejects any numeral not in the fact set. |
| `internal/policy` | The write plane: allowlist, ceilings, fresh re-read, idempotency, confirmation card. |
| `internal/store` | SQLite read model at `~/.razorpay/cache.db`. Reads may use it; the write plane never does. |
| `internal/mcp` | stdio JSON-RPC client for `razorpay-mcp-server`, launched with `--read-only` unless the session is elevated. |
| `internal/audit` | Append-only JSONL at `~/.razorpay/ai-audit.log`, redacted field by field. |
| `internal/eval` | Sets A, B and C. Also runs under `go test`. |
| `internal/llm` | The one-method seam every model provider sits behind. |
| `internal/ui` | Banner, brand palette and colour-level detection (truecolor / basic / none, with Windows VT switching). |

## The invariants

Violating one of these is a bug, not a tradeoff.

1. **The model never produces a number that is displayed as fact.** All arithmetic
   is computed in Go and injected into the narration. `narrator.Guard` enforces
   this literally: a narration containing a numeral absent from the fact set is
   discarded and the deterministic template is printed instead. Eval Set B is the
   regression test.
2. **Reads are cheap and automatic. Writes are typed, validated and confirmed.**
   No code path lets free-form model output reach a money-moving call.
3. **Confirmation prompts are rendered from a fresh read** — not from conversation
   context, not from the model's restatement, not from the cache. The card says
   how old the read is.
4. **Every answer is attributable.** `--explain` prints the plan, the window, the
   source, the query count, the row count and where the arithmetic lives.
5. **Read-only by default, and it means it.** The MCP subprocess runs with
   `--read-only` unless the session was started with `--write`. Policy engine
   *and* transport-level restriction.
6. **Money is stored in paise, formatted at the edge.** Exactly one conversion
   boundary, in `internal/money`, unit-tested for round-trip fidelity.
7. **Refuse rather than guess.** An utterance that maps to no capability gets a
   refusal and the list of what is supported — never a plausible half-answer.

## The write plane

Six layers, all enforced in Go — the prompt is never the only line of defence.

1. **Transport** — the MCP subprocess is launched `--read-only` for normal sessions.
2. **Allowlist** — `capability` is a closed enum; unknown values are a hard failure.
3. **Typed params** — `int64` paise, id prefix patterns, range checks, ceilings
   (refund ₹25,000; payment link ₹1,00,000; 5 sent links per session).
4. **Fresh re-read** — the target entity is re-fetched before the prompt is drawn,
   and state changes since planning (already refunded, already captured) abort.
5. **Idempotency** — a key is persisted *before* the call; a retry reuses it.
6. **Audit** — every plan, policy decision, confirmation and response id, redacted.

```
  !  REFUND — test mode confirmation

     Payment      pay_29000000481019
     Customer     ada@example.com  ·  *******3210
     Captured     ₹1,900.00  on 5 Sep 2026, 14:22
     Refunding    ₹100.00  (partial)
     Remaining    ₹1,800.00
     Speed        normal
     Idempotency  rzpai_7K2M4N7PQR3V8X

     Source: read fresh 0.3s ago — not from cache, not from the model.

  Type the payment ID to confirm, or press Enter to cancel:
```

Typing the entity id rather than `y` is deliberate: it defeats reflexive
confirmation and makes an accidental refund require intent.

**Prompt-injection posture.** Fetched merchant data (notes, descriptions, names)
is untrusted and passed to the narrator inside a delimited block. More
importantly, **the narrator has no tool access at all** — it receives computed
facts and returns prose. A successful injection can produce a misleading
sentence; it cannot produce an API call, and `Guard` catches the figure anyway.

**Refunds are blocked outright in live mode in v1.** Deliberate, not an omission.


MIT.
