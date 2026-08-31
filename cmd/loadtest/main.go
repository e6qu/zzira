package main

import (
	"context"
	"fmt"
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
	Size     int
	Actions  int64
	ColdP50  time.Duration
	ColdP95  time.Duration
	ColdP99  time.Duration
	IncrP95  time.Duration
	SeedSecs float64
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

	var results []measurement
	for _, size := range []int{100, 1000, 10000, 100000} {
		m, err := runScenario(ctx, st, server.URL, size)
		if err != nil {
			fatal(fmt.Sprintf("scenario %d", size), err)
		}
		results = append(results, m)
	}
	printReport(results)
	fmt.Println("== concurrent writers ==")
	measureConcurrentWriters(server.URL, st, 8, 10*time.Second)
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
	defer admin.Close(ctx)
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
	m.SeedSecs = time.Since(seedStart).Seconds()

	head, err := st.Head(ctx, "ws_"+slug)
	if err != nil {
		return m, err
	}
	m.Actions = head

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

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

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
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 304 {
		return 0, fmt.Errorf("sync http %d", resp.StatusCode)
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
				}
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
	mux := http.NewServeMux()
	sync := &syncapi.Handler{Store: st}
	webHandler := &web.Handler{Store: st, Commands: cmdSvc, WorkspaceSlug: "conc"}
	mux.HandleFunc("GET /issues/new", webHandler.CreateDialog)
	mux.Handle("GET /sync", sync)
	mux.Handle("/rest/api/3/", api)
	return mux
}
