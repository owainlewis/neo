# Performance baseline

Neo tracks a small local baseline so contributors can notice meaningful growth
without turning variable timings into flaky CI gates.

Run the release-shaped build and stable microbenchmarks from the repository
root:

```bash
just performance
```

The command reports:

- stripped, `CGO_ENABLED=0` binary size for the current OS and architecture;
- base system prompt construction time, allocations, and prompt bytes;
- representative workflow rendering time and allocations;
- built-in tool catalogue construction time and allocations.

It runs each benchmark five times by default. Use `BENCH_COUNT=10 just
performance` when investigating a suspected regression. Startup, filesystem,
terminal, and provider network timings are omitted because they vary too much
between hosts to be useful gates.

Two deterministic size measurements have deliberately generous regression
budgets:

- the stripped binary must remain at or below 20 MiB;
- the base prompt must remain at or below 2,048 bytes.

The binary budget is checked by `just performance`. The prompt budget is a
normal Go test, so CI checks it without running timing benchmarks.

## Baseline

The baseline below was captured on Go 1.25.8 on Darwin/arm64. Compare like with
like: the same Go version, OS, architecture, and an otherwise idle machine.
To reproduce it when another Go version is active, run
`GOTOOLCHAIN=go1.25.8 just performance`.

| Measurement | Baseline |
| --- | ---: |
| Stripped binary | 16,322,418 bytes |
| Base prompt size | 1,519 bytes |
| Base prompt | ~575 ns/op |
| Base prompt allocation | 1,560 B/op, 2 allocs/op |
| Workflow render | ~256,000 ns/op |
| Workflow render allocation | 20,851 B/op, 548 allocs/op |
| Tool catalogue | ~3,230 ns/op |
| Tool catalogue allocation | 10,032 B/op, 70 allocs/op |

Timing and allocation numbers are informational. There are no timing thresholds
in CI. The stable size budgets above are the only regression gates.

## Updating the baseline

Update this table only for an intentional change or a repeatedly reproduced
regression. Run `just performance` at least twice with the Go version declared
by `go.mod`, use the median result from the reported samples, and include the
before/after output in the pull request. If an intentional change exceeds a
size budget, update the corresponding constant in `scripts/performance.sh` or
`cmd/neo/performance_test.go` and explain why in the pull request. Do not update
a baseline or budget to hide a one-off noisy result.
