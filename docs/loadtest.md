# Sync load test

`cmd/loadtest` measures the delta-sync read path (`GET /sync`) and write throughput. Every browser replica of a [Jira](JIRA_PLATFORM.md) work item uses this path to catch up with the action log, so its latency bounds how quickly an offline client converges. For the architecture, see the [README](../README.md); for status, see [CLOUD_PARITY.md](CLOUD_PARITY.md).

## Run

```sh
LOADTEST_ADMIN_DSN=postgres://…/postgres LOADTEST_DSN=postgres://…/zzira_load make loadtest
```

`LOADTEST_KEEP=1` leaves the seeded database behind, which is how a slow number here is taken apart with `EXPLAIN (ANALYZE, BUFFERS)` afterwards.

What the tool does:
1. Drops and recreates the `zzira_load` database, then migrates it.
2. Serves the real handlers in-process.
3. Seeds workspaces with 100, 1,000, 10,000, 100,000 and 1,000,000 work items, one action each, copied in batches so a million rows are not held in memory at once.
4. Settles each seeded workspace (`ANALYZE`, `CHECKPOINT`) and makes three warm-up requests, so what is timed is the read path rather than the aftermath of a bulk load.
5. Times 30 requests per size for each of two scenarios, reading each response to the end:
   - **Cold catch-up:** `GET /sync?since=0`, the first page of a full history.
   - **Incremental:** `GET /sync?since=<head-50>`, a typical reconnect.
6. Runs 8 goroutines creating work items through the HTTP command path for 10 seconds.
7. Repeats the two reads for 10 seconds against the largest workspace *while* 8 goroutines write to it, which is the state a real site is in.

## Results

These results come from macOS on arm64, with Postgres 17 in a container and a single replica. Compare how the numbers scale with size; the absolute values depend on the machine. Every page is 500 actions, so the 100-item row is the only one reading a shorter page.

| Work items | Actions | Cold p50 | Cold p95 | Cold p99 | Incremental p95 | Seed |
|---|---|---|---|---|---|---|
| 100 | 100 | 14.0 ms | 19.0 ms | 20.9 ms | 13.3 ms | 0.0 s |
| 1,000 | 1,000 | 56.9 ms | 61.0 ms | 61.5 ms | 13.4 ms | 0.1 s |
| 10,000 | 10,000 | 58.3 ms | 63.3 ms | 67.0 ms | 11.5 ms | 0.3 s |
| 100,000 | 100,000 | 61.0 ms | 68.6 ms | 69.9 ms | 14.8 ms | 3.4 s |
| 1,000,000 | 1,000,000 | 65.0 ms | 71.5 ms | 75.3 ms | 20.8 ms | 34.2 s |

With 8 concurrent writers, the run created 1,491 work items in 10 seconds, about 149 writes per second.

Reading the million-action workspace while 8 writers work on it: 117 writes per second, cold p50 116.4 ms, p95 218.1 ms, p99 228.9 ms, incremental p95 160.1 ms.

## Reading the results

- **History does not cost the reader anything.** A thousand times more history moves cold p95 from 61 ms to 72 ms. The page is a key-range scan on `(workspace_id, seq)` bounded to 500 actions, and the permission filter is asked per row of that page, so neither grows with what came before.
- **The remaining cost is the permission filter.** About 0.11 ms per action, most of it `jira_issue_security_visible` asked once per work item a page mentions. It is flat in history size, so it sets the floor rather than the slope.
- **Reconnect cost follows the delta, not the history.** A client that is 10,000 actions behind pages through the delta in page-sized requests.
- **Writing while reading costs about three times the latency, not an order of magnitude.** At a million actions, cold p95 goes from 72 ms to 218 ms with 8 writers on the same workspace, and writes lose about a fifth of their throughput.
- **A posting-list index is not needed.** The threshold for adding one is a sync p95 above 300 ms at the target workspace size. At a million actions it is 72 ms idle and 218 ms under concurrent writes.

## Just-in-time compilation is off

Postgres compiles a plan whose estimated cost crosses `jit_above_cost`, and the estimate for the sync page crosses it at any real size: the permission filter is a large hand-written predicate, and the planner costs it against the whole table. Compiling it took **1.08 s of the 1.14 s** a cold catch-up spent at a million actions — 329 functions, emitted and optimized on every execution, to read 500 rows.

`store.Open` therefore starts every connection with `jit=off` (a DSN that sets `jit` itself is left alone). The same query, same plan, same rows: **1,145 ms → 54 ms**. This is why the table above is flat where the earlier one was not; the read path had always been a key-range scan, and the compiler was the thing that grew.

## Limits

- Not measured beyond 1,000,000 actions, and not with more than 8 concurrent writers.
- One replica, one Postgres, one machine: this measures the query path, not a deployment.

Remaining work is tracked in [PLAN.md](../PLAN.md).
