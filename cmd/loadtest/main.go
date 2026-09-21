package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/syncapi"
	"github.com/e6qu/zzira/internal/web"
)

// DSNs come from the environment (CI sets them); no credentials in source.
var (
	loadDSN  = envOr("LOADTEST_DSN", "")
	adminDSN = envOr("LOADTEST_ADMIN_DSN", "")
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type measurement struct {
	Size    int
	Actions int64
	// Slug, Email and Token are what this scenario seeded, so a later phase
	// reads the same workspace rather than seeding over it: seeding again
	// would reset the workspace's sequence and leave nothing to read.
	Slug, Email, Token string
	ColdP50            time.Duration
	ColdP95            time.Duration
	ColdP99            time.Duration
	IncrP95            time.Duration
	SeedSecs           float64
}

func main() {
	ctx := context.Background()
	if err := resetDatabase(ctx); err != nil {
		fatal("reset db", err)
	}
	st, err := store.Open(ctx, loadDSN)
	if err != nil {
		fatal("open", err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		fatal("migrate", err)
	}

	blobs, err := attachments.NewFS("data/loadtest-attachments")
	if err != nil {
		fatal("blobs", err)
	}
	cmdSvc := &commands.Service{Store: st, Blobs: blobs}
	api := &api3.Handler{Store: st, Commands: cmdSvc, Blobs: blobs, BaseURL: "http://loadtest", WorkspaceSlug: "conc"}
	server := httptest.NewServer(httpmux(st, cmdSvc, api))
	defer server.Close()

	sizes := []int{100, 1000, 10000, 100000, 1000000}
	var results []measurement
	for _, size := range sizes {
		m, err := runScenario(ctx, st, server.URL, size)
		if err != nil {
			fatal(fmt.Sprintf("scenario %d", size), err)
		}
		results = append(results, m)
	}
	printReport(results)
	fmt.Println("== concurrent writers ==")
	measureConcurrentWriters(server.URL, st, 8, 10*time.Second)
	// The same reads, taken while the workspace is being written to: a
	// reconnecting client does not wait for the site to go quiet.
	fmt.Println("== reads under concurrent writes ==")
	if err := measureMixed(ctx, st, results[len(results)-1], 8, 10*time.Second); err != nil {
		fatal("mixed", err)
	}
	// LOADTEST_KEEP leaves the seeded database behind, which is how a slow
	// number above is taken apart with EXPLAIN afterwards.
	if os.Getenv("LOADTEST_KEEP") != "" {
		fmt.Println("kept:", loadDSN)
		return
	}
	if err := resetDatabase(ctx); err != nil {
		fmt.Println("cleanup:", err)
	}
}

func fatal(what string, err error) {
	fmt.Println(what+":", err)
	os.Exit(1)
}

func resetDatabase(ctx context.Context) error {
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS zzira_load WITH (FORCE)`); err != nil {
		return err
	}
	_, err = admin.Exec(ctx, `CREATE DATABASE zzira_load`)
	return err
}

// runScenario seeds a workspace with n issues and measures /sync: the
// returning-client cold catch-up (since=0) and a tail incremental.
func runScenario(ctx context.Context, st *store.Store, baseURL string, n int) (measurement, error) {
	m := measurement{Size: n}

	seedStart := time.Now()
	slug := fmt.Sprintf("load%d", n)
	token, email, err := st.SeedLoadWorkspace(ctx, slug, n)
	if err != nil {
		return m, fmt.Errorf("seed: %w", err)
	}
	m.Slug, m.Email, m.Token = slug, email, token
	m.SeedSecs = time.Since(seedStart).Seconds()
	// What is being measured is the read path, not the aftermath of a bulk
	// load: the planner needs the statistics, and the checkpoint would
	// otherwise land in the middle of the timings.
	if err := settle(ctx, st); err != nil {
		return m, err
	}

	head, err := st.Head(ctx, "ws_"+slug)
	if err != nil {
		return m, err
	}
	m.Actions = head

	// A few requests before the clock starts, so the first page's planning
	// and buffer reads are not counted as the latency of every request.
	for range 3 {
		if _, err := timeGet(baseURL+"/sync?workspace="+slug+"&since=0", email, token); err != nil {
			return m, err
		}
	}
	cold := []time.Duration{}
	incr := []time.Duration{}
	for i := 0; i < 30; i++ {
		d1, err := timeGet(baseURL+"/sync?workspace="+slug+"&since=0", email, token)
		if err != nil {
			return m, err
		}
		cold = append(cold, d1)
		d2, err := timeGet(fmt.Sprintf("%s/sync?workspace=%s&since=%d", baseURL, slug, max64(0, head-50)), email, token)
		if err != nil {
			return m, err
		}
		incr = append(incr, d2)
	}
	m.ColdP50, m.ColdP95, m.ColdP99 = percentiles(cold)
	m.IncrP95 = percentile(incr, 95)
	return m, nil
}

// settle leaves the database ready to be read from: statistics current and
// the write-ahead log flushed.
func settle(ctx context.Context, st *store.Store) error {
	if _, err := st.Pool.Exec(ctx, `ANALYZE issues, actions`); err != nil {
		return err
	}
	_, err := st.Pool.Exec(ctx, `CHECKPOINT`)
	return err
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// timeGet times a whole response, not its first byte: a client has caught up
// when it holds the page, and the body of a cold catch-up is most of the work.
// Draining it also lets the connection be reused, and leaves the server with
// nothing to log about a reader that went away.
func timeGet(url, email, token string) (time.Duration, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(email, token)
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 && resp.StatusCode != 304 {
		return 0, fmt.Errorf("sync http %d", resp.StatusCode)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

func percentiles(durs []time.Duration) (p50, p95, p99 time.Duration) {
	return percentile(durs, 50), percentile(durs, 95), percentile(durs, 99)
}

func percentile(durs []time.Duration, p int) time.Duration {
	sorted := append([]time.Duration(nil), durs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(p*(len(sorted)-1))/100]
}

// measureConcurrentWriters drives n goroutines of issue creation through the
// HTTP edge and reports aggregate write throughput and sync tail behavior.
func measureConcurrentWriters(baseURL string, st *store.Store, workers int, dur time.Duration) {
	token, email, err := st.SeedLoadWorkspace(ctxBG(), "conc", 1)
	if err != nil {
		fmt.Println("conc seed:", err)
		return
	}
	var wg sync.WaitGroup
	var created int64
	var refusedOnce sync.Once
	stop := make(chan struct{})
	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				body := fmt.Sprintf(`{"fields":{"project":{"key":"LOAD"},"summary":"conc %d %d","issuetype":{"name":"Task"}}}`, i, time.Now().UnixNano())
				req, err := http.NewRequest("POST", baseURL+"/rest/api/3/issue", strings.NewReader(body))
				if err != nil {
					return
				}
				req.SetBasicAuth(email, token)
				req.Header.Set("Content-Type", "application/json")
				resp, err := httpClient.Do(req)
				if err != nil {
					return
				}
				if err := resp.Body.Close(); err != nil {
					return
				}
				if resp.StatusCode == 201 {
					atomic.AddInt64(&created, 1)
					continue
				}
				refusedOnce.Do(func() { fmt.Printf("write refused: http %d\n", resp.StatusCode) })
			}
		}(i)
	}
	time.Sleep(dur)
	close(stop)
	wg.Wait()
	elapsed := time.Since(start)
	fmt.Printf("| concurrent writers: %d workers, %d issues in %v (%.0f writes/s) |\n",
		workers, atomic.LoadInt64(&created), elapsed.Round(time.Millisecond), float64(created)/elapsed.Seconds())
}

// measureMixed times the sync reads of a client catching up while other
// people are writing to the same workspace, which is the state a real site is
// in and the one the read numbers above leave out.
func measureMixed(ctx context.Context, st *store.Store, seeded measurement, workers int, dur time.Duration) error {
	slug, email, token := seeded.Slug, seeded.Email, seeded.Token
	// The writes must land in the workspace being read, so this server's
	// handlers are bound to it rather than to the small concurrent-write one.
	blobs, err := attachments.NewFS("data/loadtest-attachments")
	if err != nil {
		return err
	}
	cmdSvc := &commands.Service{Store: st, Blobs: blobs}
	api := &api3.Handler{Store: st, Commands: cmdSvc, Blobs: blobs, BaseURL: "http://loadtest", WorkspaceSlug: slug}
	server := httptest.NewServer(httpmuxFor(st, cmdSvc, api, slug))
	defer server.Close()

	head, err := st.Head(ctx, "ws_"+slug)
	if err != nil {
		return err
	}
	var created int64
	var refusedOnce sync.Once
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for worker := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				body := fmt.Sprintf(`{"fields":{"project":{"key":"LOAD"},"summary":"mixed %d %d","issuetype":{"name":"Task"}}}`, worker, time.Now().UnixNano())
				request, err := http.NewRequest("POST", server.URL+"/rest/api/3/issue", strings.NewReader(body))
				if err != nil {
					return
				}
				request.SetBasicAuth(email, token)
				request.Header.Set("Content-Type", "application/json")
				response, err := httpClient.Do(request)
				if err != nil {
					return
				}
				answer, err := io.ReadAll(response.Body)
				if err != nil {
					return
				}
				if err := response.Body.Close(); err != nil {
					return
				}
				if response.StatusCode == 201 {
					atomic.AddInt64(&created, 1)
					continue
				}
				// A writer that is refused says so once: a run that reports
				// no writes should say why rather than look like a stall.
				refusedOnce.Do(func() {
					fmt.Printf("write refused: http %d %s\n", response.StatusCode, strings.TrimSpace(string(answer)))
				})
			}
		}(worker)
	}
	start := time.Now()
	cold := []time.Duration{}
	incr := []time.Duration{}
	for time.Since(start) < dur {
		d1, err := timeGet(server.URL+"/sync?workspace="+slug+"&since=0", email, token)
		if err != nil {
			close(stop)
			wg.Wait()
			return err
		}
		cold = append(cold, d1)
		d2, err := timeGet(fmt.Sprintf("%s/sync?workspace=%s&since=%d", server.URL, slug, max64(0, head-50)), email, token)
		if err != nil {
			close(stop)
			wg.Wait()
			return err
		}
		incr = append(incr, d2)
	}
	close(stop)
	wg.Wait()
	elapsed := time.Since(start)
	coldP50, coldP95, coldP99 := percentiles(cold)
	fmt.Printf("| %d issues | %d reads while %d writers created %d issues (%.0f writes/s) |\n",
		seeded.Size, len(cold), workers, atomic.LoadInt64(&created), float64(created)/elapsed.Seconds())
	fmt.Printf("| cold p50 %v | cold p95 %v | cold p99 %v | incr p95 %v |\n",
		coldP50, coldP95, coldP99, percentile(incr, 95))
	return nil
}

func ctxBG() context.Context { return context.Background() }

func printReport(results []measurement) {
	fmt.Println()
	fmt.Println("| issues | actions | cold p50 | cold p95 | cold p99 | incr p95 | seed (s) |")
	fmt.Println("|--------|---------|----------|----------|----------|----------|----------|")
	for _, m := range results {
		fmt.Printf("| %d | %d | %v | %v | %v | %v | %.1f |\n",
			m.Size, m.Actions, m.ColdP50, m.ColdP95, m.ColdP99, m.IncrP95, m.SeedSecs)
	}
}

// httpmux builds the production route table subset the load test exercises.
func httpmux(st *store.Store, cmdSvc *commands.Service, api *api3.Handler) *http.ServeMux {
	return httpmuxFor(st, cmdSvc, api, "conc")
}

// httpmuxFor serves the real handlers for one workspace: the REST edge
// resolves its workspace from the slug it is bound to, so writing to another
// one means another set of handlers.
func httpmuxFor(st *store.Store, cmdSvc *commands.Service, api *api3.Handler, slug string) *http.ServeMux {
	mux := http.NewServeMux()
	sync := &syncapi.Handler{Store: st}
	webHandler := &web.Handler{Store: st, Commands: cmdSvc, WorkspaceSlug: slug}
	mux.HandleFunc("GET /issues/new", webHandler.CreateDialog)
	mux.Handle("GET /sync", sync)
	mux.Handle("/rest/api/3/", api)
	return mux
}
