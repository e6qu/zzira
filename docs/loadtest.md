# Sync load test

`cmd/loadtest` measures the delta-sync read path (`GET /sync`) and write throughput. Replicas use this path to catch up with the action log. For the architecture, see the [README](../README.md).

## Run

```sh
LOADTEST_ADMIN_DSN=postgres://…/postgres LOADTEST_DSN=postgres://…/zzira_load make loadtest
```

What the tool does:
1. Drops and recreates the `zzira_load` database, then migrates it.
2. Serves the real handlers in-process.
3. Seeds workspaces with 100, 1,000, 10,000 and 100,000 work items, one action each.
4. Times 30 requests per size for each of two scenarios:
   - **Cold catch-up:** `GET /sync?since=0`, the first page of a full history.
   - **Incremental:** `GET /sync?since=<head-50>`, a typical reconnect.
5. Runs 8 goroutines creating work items through the HTTP command path for 10 seconds.

## Results

These results come from macOS on arm64, with Postgres 17 in a container and a single replica. Compare how the numbers scale with size; the absolute values depend on the machine.

| Work items | Actions | Cold p50 | Cold p95 | Cold p99 | Incremental p95 |
|---|---|---|---|---|---|
| 100 | 100 | 2.2 ms | 3.3 ms | 4.3 ms | 2.4 ms |
| 1,000 | 1,000 | 5.0 ms | 5.5 ms | 5.5 ms | 2.8 ms |
| 10,000 | 10,000 | 6.3 ms | 6.8 ms | 7.2 ms | 3.9 ms |
| 100,000 | 100,000 | 31.2 ms | 89.6 ms | 124.6 ms | 44.2 ms |

With 8 concurrent writers, the run created 3,193 work items in 10 seconds, about 318 writes per second. Sync latency did not change beyond noise.

## Reading the results

- **p95 grows far more slowly than history.** History grew 1,000 times, while p95 rose from 3.3 ms to 89.6 ms.
  - The sync query is a key-range scan on `(workspace_id, seq)` plus permission checks, so older history does not add scan cost.
  - The remaining growth comes from serializing larger pages.
- **Reconnect cost follows the delta, not the history.** A client that is 10,000 actions behind pages through the delta in page-sized requests.
- **A posting-list index is not needed yet.** The threshold for adding one is a sync p95 above 300 ms at the target workspace size. At 100,000 work items, p95 is 89.6 ms, about 3.3 times under that threshold, even with concurrent writes.

## Limits

- Not measured beyond 100,000 actions, and not with mixed read and write concurrency at 1M actions.

Remaining work is tracked in [PLAN.md](../PLAN.md).
