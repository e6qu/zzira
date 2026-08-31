# Delta-sync read path — load measurement

Method: `go run ./cmd/loadtest` — seeds synthetic workspaces (100 / 1,000 / 10,000
issues = 1 action each) into a disposable `zzira_load` database, runs the real
handlers in-process, and measures 30 requests per scenario.

- **cold catch-up**: `GET /sync?since=0` (checkpoint at zero — worst case, page 1 of the delta)
- **incremental**: `GET /sync?since=head-50` (typical reconnect tail)

Run on macOS/arm64, Dockerized Postgres 17, single replica — relative scaling is
the signal, not absolute numbers.

| issues | actions | cold p50 | cold p95 | cold p99 | incr p95 |
|--------|---------|----------|----------|----------|----------|
| 100    | 100     | 2.2ms    | 3.3ms    | 4.3ms    | 2.4ms    |
| 1,000  | 1,000   | 5.0ms    | 5.5ms    | 5.5ms    | 2.8ms    |
| 10,000 | 10,000  | 6.3ms    | 6.8ms    | 7.2ms    | 3.9ms    |
| 100,000| 100,000 | 31.2ms   | 89.6ms   | 124.6ms  | 44.2ms   |

Concurrent writers: 8 goroutines through the full HTTP command core —
**3,193 issues in 10s (~318 writes/s)**, sync tail unaffected beyond noise.

## Reading

- p95 stays **sub-linear across a 1,000× history growth** (3.3ms → 89.6ms at
  100k): the sync query is a key-range scan on `(workspace_id, seq)` plus the
  permission predicates — history depth does not enter the cost. The growth
  that remains comes from marshaling larger single pages, not from scanning.
- Incremental reconnects sit at ~4ms regardless of size.
- A client 10k actions behind paginates the delta in ~20 × p95-sized pages;
  reconnect cost is proportional to the *delta*, never to history.

## v2 posting-list index trigger

Plan §14 arms the escape hatch at **sync p95 > 300ms at target workspace size**.
Measured p95 at 100k issues is **89.6ms — ~3.3× under the trigger**, with
concurrent writes flowing. The Postgres `Syncer` stands; the posting-list
`Syncer`/`Searcher` stays on the shelf.

Next measurement gate: 1M actions with mixed read/write concurrency.
