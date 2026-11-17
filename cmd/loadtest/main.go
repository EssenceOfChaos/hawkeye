// Command loadtest measures what a shorter check interval costs, before customers can choose one.
//
// It never touches real data: each run creates its own scratch database next to the one -db names, migrates it, and drops it at the end
// (unless -keep). Point -db at a throwaway PostgreSQL sized like production, not at a production server.
//
// Two modes:
//
//	live  runs the real scheduler and worker (production settings) against a local fake site and reports whether monitors were checked on
//	      time: gaps between checks, queue depth, scheduler lag, missed checks and database work per second.
//	read  fills check_results with history at an interval and times the queries that read it (dashboard uptime, response-time chart,
//	      status page, monthly report, history page), and reports storage per monitor-year.
//
// Examples (run each with -interval 60 and -interval 30 and compare):
//
//	go run ./cmd/loadtest -db postgres://postgres:pw@localhost:55432/postgres?sslmode=disable live -interval 30 -orgs 5 -duration 5m
//	go run ./cmd/loadtest -db postgres://postgres:pw@localhost:55432/postgres?sslmode=disable read -interval 30 -monitors 50 -days 90
//
// An interval under 60 seconds needs the schema change sub-minute checks bring (monitors' CHECK and the Agency plan's minimum); the tool
// applies that change to its scratch database only.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"

	"hawkeye/internal/checks"
	"hawkeye/internal/config"
	"hawkeye/internal/database"
	"hawkeye/internal/incidents"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/queue"
	"hawkeye/internal/reports"
	"hawkeye/internal/rollups"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/statuspage"
	"hawkeye/internal/worker"
	"hawkeye/migrations"
)

func main() {
	global := flag.NewFlagSet("loadtest", flag.ExitOnError)
	dbURL := global.String("db", os.Getenv("LOADTEST_DATABASE_URL"), "admin URL of a throwaway PostgreSQL (or LOADTEST_DATABASE_URL); a scratch database is created beside it")
	keep := global.Bool("keep", false, "keep the scratch database afterward (its name is printed)")
	global.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: loadtest -db URL [-keep] live|read [flags]")
		global.PrintDefaults()
	}
	_ = global.Parse(os.Args[1:])
	if *dbURL == "" || global.NArg() < 1 {
		global.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var run func(context.Context, string) error
	mode, args := global.Arg(0), global.Args()[1:]
	switch mode {
	case "live":
		run = parseLive(args).run
	case "read":
		run = parseRead(args).run
	default:
		global.Usage()
		os.Exit(2)
	}

	scratch, drop, err := scratchDatabase(ctx, *dbURL)
	if err != nil {
		fail(err)
	}
	if *keep {
		fmt.Printf("scratch database kept: %s\n", redact(scratch))
	} else {
		defer drop()
	}
	if err := run(ctx, scratch); err != nil {
		if !*keep {
			drop()
		}
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "loadtest:", err)
	os.Exit(1)
}

func redact(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "(unparseable URL)"
	}
	return p.Redacted()
}

// scratchDatabase creates and migrates hawkeye_load_<random> on the server admin points at, and returns its URL and a function that drops it.
func scratchDatabase(ctx context.Context, admin string) (string, func(), error) {
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "hawkeye_load_" + hex.EncodeToString(b[:])
	c, err := pgx.Connect(ctx, admin)
	if err != nil {
		return "", nil, fmt.Errorf("connect: %w", err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", nil, fmt.Errorf("create database: %w", err)
	}
	drop := func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "loadtest: could not drop %s: %v\n", name, err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			fmt.Fprintf(os.Stderr, "loadtest: could not drop %s: %v\n", name, err)
		}
	}
	u, err := url.Parse(admin)
	if err != nil {
		drop()
		return "", nil, err
	}
	u.Path = "/" + name
	if err := database.MigrateUp(u.String(), migrations.FS); err != nil {
		drop()
		return "", nil, fmt.Errorf("migrate: %w", err)
	}
	return u.String(), drop, nil
}

// allowInterval applies, to the scratch database only, the schema change sub-minute checks need: monitors may run every `seconds`, and so may
// the Agency plan.
func allowInterval(ctx context.Context, pool *pgxpool.Pool, seconds int) error {
	if seconds >= 60 {
		return nil
	}
	_, err := pool.Exec(ctx, fmt.Sprintf(`ALTER TABLE monitors DROP CONSTRAINT monitors_check_interval_seconds_check,
		ADD CONSTRAINT monitors_check_interval_seconds_check CHECK (check_interval_seconds BETWEEN %d AND CASE WHEN kind = 'heartbeat' THEN 2592000 ELSE 86400 END);
		UPDATE plans SET minimum_check_interval_seconds = %d WHERE code = 'agency'`, seconds, seconds))
	return err
}

func newOrg(ctx context.Context, pool *pgxpool.Pool, name, plan string) (uuid.UUID, error) {
	var org uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ($1) RETURNING id`, name).Scan(&org); err != nil {
		return org, err
	}
	tag, err := pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = $2`, org, plan)
	if err == nil && tag.RowsAffected() != 1 {
		err = fmt.Errorf("no plan %q", plan)
	}
	return org, err
}

// ---- live

type liveOpts struct {
	interval, baseInterval      int
	orgs, perOrg, base          int
	concurrency, timeout        int
	duration, warmup            time.Duration
	latency, slowLatency        time.Duration
	slowFrac, hangFrac          float64
	schedBatch, schedQueueDepth int
}

func parseLive(args []string) *liveOpts {
	o := &liveOpts{}
	fs := flag.NewFlagSet("live", flag.ExitOnError)
	fs.IntVar(&o.interval, "interval", 30, "check interval of the Agency monitors under test, seconds")
	fs.IntVar(&o.orgs, "orgs", 1, "Agency organizations")
	fs.IntVar(&o.perOrg, "per-org", 200, "monitors per Agency organization (the plan allows 200)")
	fs.IntVar(&o.base, "base", 0, "extra monitors on a Pro organization, standing in for everyone else's load")
	fs.IntVar(&o.baseInterval, "base-interval", 60, "check interval of the -base monitors, seconds")
	fs.IntVar(&o.concurrency, "concurrency", 10, "WORKER_CONCURRENCY (production: 10)")
	fs.IntVar(&o.timeout, "timeout", 10, "monitors' timeout_seconds (default 10; customers may choose up to 30)")
	fs.DurationVar(&o.duration, "duration", 5*time.Minute, "measured run time, after the warm-up")
	fs.DurationVar(&o.warmup, "warmup", 0, "unmeasured start-up time (default: two intervals)")
	fs.DurationVar(&o.latency, "latency", 150*time.Millisecond, "typical response time of the fake site (each response takes 0.5x to 1.5x this)")
	fs.DurationVar(&o.slowLatency, "slow-latency", 3*time.Second, "response time of slow sites")
	fs.Float64Var(&o.slowFrac, "slow", 0.05, "share of monitors pointing at a slow site")
	fs.Float64Var(&o.hangFrac, "hang", 0.01, "share of monitors pointing at a site that never answers (each check waits the full timeout)")
	fs.IntVar(&o.schedBatch, "scheduler-batch", 200, "SCHEDULER_BATCH")
	fs.IntVar(&o.schedQueueDepth, "queue-max-depth", 1000, "QUEUE_MAX_DEPTH")
	_ = fs.Parse(args)
	if o.warmup == 0 {
		o.warmup = 2 * time.Duration(max(o.interval, o.baseInterval)) * time.Second
	}
	return o
}

// fakeSite answers /ok quickly, /slow slowly and /hang never (until the client gives up).
func (o *liveOpts) fakeSite() *httptest.Server {
	wait := func(r *http.Request, d time.Duration) {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
		}
	}
	spread := func(d time.Duration) time.Duration { return time.Duration(float64(d) * (0.5 + mrand.Float64())) }
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { wait(r, spread(o.latency)); w.WriteHeader(200) })
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) { wait(r, spread(o.slowLatency)); w.WriteHeader(200) })
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) { wait(r, time.Hour) })
	return httptest.NewServer(mux)
}

func (o *liveOpts) seed(ctx context.Context, pool *pgxpool.Pool, site string, org uuid.UUID, n, interval int) error {
	// Spread first checks over one interval, as a running system's jitter would; otherwise every monitor starts in the same second.
	_, err := pool.Exec(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, timeout_seconds, next_check_at)
		SELECT $1, 'load ' || g, $2 || CASE WHEN r < $5 THEN '/hang' WHEN r < $5 + $6 THEN '/slow' ELSE '/ok' END || '?m=' || g,
			$3::int, $4, now() + random() * make_interval(secs => $3::int)
		FROM (SELECT g, random() AS r FROM generate_series(1, $7) g) s`, org, site, interval, o.timeout, o.hangFrac, o.slowFrac, n)
	return err
}

type sample struct {
	queued, running int
	oldest, lag     time.Duration
}

type dbStats struct{ commits, inserted, updated, deleted int64 }

func readDBStats(ctx context.Context, pool *pgxpool.Pool) (dbStats, error) {
	var s dbStats
	err := pool.QueryRow(ctx, `SELECT xact_commit, tup_inserted, tup_updated, tup_deleted FROM pg_stat_database WHERE datname = current_database()`).
		Scan(&s.commits, &s.inserted, &s.updated, &s.deleted)
	return s, err
}

func (o *liveOpts) run(ctx context.Context, dsn string) error {
	conns := config.Config{WorkerConcurrency: o.concurrency, DBMaxConns: 20}.WorkerPoolConns() // production: DB_MAX_CONNS=20
	pool, err := database.Connect(ctx, dsn, conns, 10*time.Second)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := allowInterval(ctx, pool, min(o.interval, o.baseInterval)); err != nil {
		return fmt.Errorf("allow %ds interval: %w", o.interval, err)
	}

	site := o.fakeSite()
	defer site.Close()
	for i := range o.orgs {
		org, err := newOrg(ctx, pool, fmt.Sprintf("agency %d", i+1), "agency")
		if err != nil {
			return err
		}
		if err := o.seed(ctx, pool, site.URL, org, o.perOrg, o.interval); err != nil {
			return fmt.Errorf("seed monitors: %w", err)
		}
	}
	if o.base > 0 {
		org, err := newOrg(ctx, pool, "everyone else", "pro")
		if err != nil {
			return err
		}
		if err := o.seed(ctx, pool, site.URL, org, o.base, o.baseInterval); err != nil {
			return fmt.Errorf("seed base monitors: %w", err)
		}
	}

	total := o.orgs*o.perOrg + o.base
	nominal := float64(o.orgs*o.perOrg)/float64(o.interval) + float64(o.base)/float64(o.baseInterval)
	fmt.Printf("live: %d monitors (%d Agency at %ds, %d others at %ds), %.1f checks/s nominal; worker concurrency %d, pool %d connections\n",
		total, o.orgs*o.perOrg, o.interval, o.base, o.baseInterval, nominal, o.concurrency, conns)
	fmt.Printf("      fake site: %v typical, %.0f%% slow (%v), %.0f%% never answer (timeout %ds); warm-up %v, measuring %v\n",
		o.latency, o.slowFrac*100, o.slowLatency, o.hangFrac*100, o.timeout, o.warmup, o.duration)

	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := metrics.New()
	loopback := safehttp.Config{
		AllowIP:   func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") },
		AllowPort: func(int) bool { return true },
	}
	sched := scheduler.New(pool, scheduler.Config{Interval: time.Second, Batch: o.schedBatch, MaxQueueDepth: o.schedQueueDepth}, m, quiet)
	w := worker.New(pool, checks.NewChecker(loopback), checks.NewStore(pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{}),
		worker.Config{Concurrency: o.concurrency}, m, quiet)

	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sched.Run(runCtx) }()
	go func() { defer wg.Done(); w.Run(runCtx) }()

	var mu sync.Mutex
	var samples []sample
	measuring := false
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
			}
			q, err1 := queue.Snapshot(runCtx, pool)
			lag, err2 := scheduler.Lag(runCtx, pool)
			if err := errors.Join(err1, err2); err != nil {
				continue
			}
			mu.Lock()
			if measuring {
				samples = append(samples, sample{q.Queued, q.Running, q.OldestRunnable, lag})
			}
			mu.Unlock()
		}
	}()

	select {
	case <-ctx.Done():
	case <-time.After(o.warmup):
	}
	var start time.Time
	if err := pool.QueryRow(ctx, `SELECT now()`).Scan(&start); err != nil {
		cancel()
		wg.Wait()
		return err
	}
	before, _ := readDBStats(ctx, pool)
	poolBefore := pool.Stat()
	counters := func() [5]float64 {
		return [5]float64{promtest.ToFloat64(m.Missed), promtest.ToFloat64(m.Skipped), promtest.ToFloat64(m.Backpressure),
			promtest.ToFloat64(m.JobsDropped), promtest.ToFloat64(m.JobsReclaimed)}
	}
	c0 := counters()
	mu.Lock()
	measuring = true
	mu.Unlock()
	fmt.Printf("      warm-up done, measuring...\n")

	select {
	case <-ctx.Done():
	case <-time.After(o.duration):
	}
	var end time.Time
	_ = pool.QueryRow(context.WithoutCancel(ctx), `SELECT now()`).Scan(&end)
	after, _ := readDBStats(context.WithoutCancel(ctx), pool)
	poolAfter := pool.Stat()
	c1 := counters()
	cancel()
	wg.Wait()

	window := end.Sub(start).Seconds()
	fmt.Printf("\nTimeliness (gap between the starts of consecutive checks of one monitor; 'late' is more than 1.5x the interval)\n\n")
	fmt.Printf("| interval | monitors | checks/s achieved | of nominal | gap p50 | gap p95 | gap p99 | gap max | late |\n|---|---|---|---|---|---|---|---|---|\n")
	rows, err := pool.Query(context.WithoutCancel(ctx), `WITH r AS (
			SELECT r.monitor_id, m.check_interval_seconds AS iv, r.checked_at,
				extract(epoch FROM r.checked_at - lag(r.checked_at) OVER (PARTITION BY r.monitor_id ORDER BY r.checked_at))::float8 AS gap
			FROM check_results r JOIN monitors m ON m.id = r.monitor_id WHERE r.checked_at < $2)
		SELECT iv, count(DISTINCT monitor_id), count(*) FILTER (WHERE checked_at >= $1),
			COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY gap) FILTER (WHERE checked_at >= $1), 0),
			COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY gap) FILTER (WHERE checked_at >= $1), 0),
			COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY gap) FILTER (WHERE checked_at >= $1), 0),
			COALESCE(max(gap) FILTER (WHERE checked_at >= $1), 0),
			COALESCE(count(*) FILTER (WHERE checked_at >= $1 AND gap > iv * 1.5)::float8 / NULLIF(count(gap) FILTER (WHERE checked_at >= $1), 0), 0)
		FROM r GROUP BY iv ORDER BY iv`, start, end)
	if err != nil {
		return err
	}
	for rows.Next() {
		var iv, mons, n int
		var p50, p95, p99, mx, late float64
		if err := rows.Scan(&iv, &mons, &n, &p50, &p95, &p99, &mx, &late); err != nil {
			rows.Close()
			return err
		}
		got := float64(n) / window
		want := float64(mons) / float64(iv)
		fmt.Printf("| %ds | %d | %.2f | %.0f%% | %.1fs | %.1fs | %.1fs | %.1fs | %.2f%% |\n", iv, mons, got, got/want*100, p50, p95, p99, mx, late*100)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var maxQ, maxRun int
	var lags, oldest []float64
	for _, s := range samples {
		maxQ, maxRun = max(maxQ, s.queued), max(maxRun, s.running)
		lags = append(lags, s.lag.Seconds())
		oldest = append(oldest, s.oldest.Seconds())
	}
	fmt.Printf("\nEngine\n\n| measure | value |\n|---|---|\n")
	fmt.Printf("| queued jobs, max | %d |\n| running jobs, max | %d (of %d) |\n", maxQ, maxRun, o.concurrency)
	fmt.Printf("| oldest queued job, p95 / max | %.1fs / %.1fs |\n", pct(oldest, 0.95), pct(oldest, 1))
	fmt.Printf("| scheduler lag, p95 / max | %.1fs / %.1fs |\n", pct(lags, 0.95), pct(lags, 1))
	fmt.Printf("| missed checks (a full interval late) | %.0f |\n| skipped (previous check still running) | %.0f |\n", c1[0]-c0[0], c1[1]-c0[1])
	fmt.Printf("| backpressure ticks | %.0f |\n| jobs dropped / reclaimed | %.0f / %.0f |\n", c1[2]-c0[2], c1[3]-c0[3], c1[4]-c0[4])
	fmt.Printf("| DB commits/s | %.1f |\n| DB rows written/s (insert+update+delete) | %.1f |\n", float64(after.commits-before.commits)/window,
		float64((after.inserted-before.inserted)+(after.updated-before.updated)+(after.deleted-before.deleted))/window)
	fmt.Printf("| waits for a pool connection | %d (avg acquire %v) |\n", poolAfter.EmptyAcquireCount()-poolBefore.EmptyAcquireCount(),
		avgAcquire(poolBefore, poolAfter))
	return nil
}

func avgAcquire(a, b *pgxpool.Stat) time.Duration {
	n := b.AcquireCount() - a.AcquireCount()
	if n <= 0 {
		return 0
	}
	return ((b.AcquireDuration() - a.AcquireDuration()) / time.Duration(n)).Round(time.Microsecond)
}

func pct(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	return s[min(len(s)-1, int(p*float64(len(s)-1)+0.5))]
}

// ---- read

type readOpts struct {
	interval, monitors, days, repeat int
	failRate                         float64
	rollups                          bool
}

func parseRead(args []string) *readOpts {
	o := &readOpts{}
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	fs.IntVar(&o.interval, "interval", 30, "interval of the generated history, seconds")
	fs.IntVar(&o.monitors, "monitors", 50, "monitors with history, all on one Agency organization and its status page")
	fs.IntVar(&o.days, "days", 90, "days of history per monitor (status pages show up to 90)")
	fs.IntVar(&o.repeat, "repeat", 5, "times each query runs")
	fs.Float64Var(&o.failRate, "fail", 0.002, "share of failed checks in the history")
	fs.BoolVar(&o.rollups, "rollups", true, "build the hourly totals (internal/rollups) before timing the queries, as the worker does; false times raw results only")
	_ = fs.Parse(args)
	return o
}

func (o *readOpts) run(ctx context.Context, dsn string) error {
	pool, err := database.Connect(ctx, dsn, 5, 10*time.Second)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := allowInterval(ctx, pool, o.interval); err != nil {
		return err
	}
	org, err := newOrg(ctx, pool, "agency", "agency")
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	rows, err := pool.Query(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, enabled)
		SELECT $1, 'site ' || g, 'https://site' || g || '.example', $2, false FROM generate_series(1, $3) g RETURNING id`, org, o.interval, o.monitors)
	if err != nil {
		return err
	}
	ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var page uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO status_pages (organization_id, slug, title, published) VALUES ($1, 'load-test', 'Load test', true) RETURNING id`,
		org).Scan(&page); err != nil {
		return fmt.Errorf("status page: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO status_page_monitors (page_id, monitor_id, position) SELECT $1, id, row_number() OVER () FROM unnest($2::uuid[]) id`,
		page, ids); err != nil {
		return fmt.Errorf("status page monitors: %w", err)
	}

	perMonitor := o.days * 86400 / o.interval
	fmt.Printf("read: generating %d days at %ds for %d monitors (%d rows each, %d in all)...\n", o.days, o.interval, o.monitors, perMonitor, perMonitor*o.monitors)
	began := time.Now()
	for i, id := range ids {
		if _, err := pool.Exec(ctx, `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms, error_category)
			SELECT $1, t, t + interval '200 milliseconds', ok, CASE WHEN ok THEN 200 ELSE 503 END, (60 + random() * 400)::int, CASE WHEN ok THEN NULL ELSE 'http_status' END
			FROM (SELECT t, random() >= $4 AS ok FROM generate_series(now() - make_interval(days => $2), now(), make_interval(secs => $3)) t) s`,
			id, o.days, o.interval, o.failRate); err != nil {
			return err
		}
		if (i+1)%10 == 0 || i+1 == len(ids) {
			fmt.Printf("      %d/%d monitors (%v)\n", i+1, len(ids), time.Since(began).Round(time.Second))
		}
	}
	if _, err := pool.Exec(ctx, `VACUUM ANALYZE check_results`); err != nil {
		return err
	}

	var n, heap, total int64
	if err := pool.QueryRow(ctx, `SELECT count(*), pg_relation_size('check_results'), pg_total_relation_size('check_results') FROM check_results`).
		Scan(&n, &heap, &total); err != nil {
		return err
	}
	perRow := float64(total) / float64(n)
	yearRows := 365 * 86400 / o.interval
	fmt.Printf("\nStorage\n\n| measure | value |\n|---|---|\n")
	fmt.Printf("| rows | %d |\n| table + indexes | %s (table %s) |\n| bytes per result, with indexes | %.0f |\n", n, size(total), size(heap), perRow)
	fmt.Printf("| one monitor, 365 days at %ds | %d rows, %s |\n", o.interval, yearRows, size(int64(perRow*float64(yearRows))))
	fmt.Printf("| a full Agency account (200 monitors, 365 days) | %d rows, %s |\n", 200*yearRows, size(int64(perRow*float64(200*yearRows))))

	if o.rollups {
		began := time.Now()
		job := rollups.NewJob(pool, rollups.JobConfig{}, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		for {
			more, err := job.Step(ctx)
			if err != nil {
				return fmt.Errorf("rollups: %w", err)
			}
			if !more {
				break
			}
		}
		if _, err := pool.Exec(ctx, `VACUUM ANALYZE check_rollups`); err != nil {
			return err
		}
		var rows, bytes int64
		if err := pool.QueryRow(ctx, `SELECT count(*), pg_total_relation_size('check_rollups') FROM check_rollups`).Scan(&rows, &bytes); err != nil {
			return err
		}
		perMonitorYear := float64(bytes) / float64(o.monitors) * 365 / float64(o.days)
		fmt.Printf("| hourly totals (internal/rollups) | %d rows, %s, built in %v |\n", rows, size(bytes), time.Since(began).Round(time.Second))
		fmt.Printf("| totals for a full Agency account (200 monitors, 365 days) | %s, whatever the interval |\n", size(int64(perMonitorYear*200)))
	}

	now := time.Now()
	mon := ids[0]
	lastMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	span := func(d time.Duration) reports.Range {
		return reports.Range{From: now.Add(-d), To: now, RetentionDays: 365}
	}
	day := 24 * time.Hour
	type q struct {
		name string
		fn   func() error
	}
	qs := []q{
		{"dashboard uptime, all monitors, 24 h", func() error { _, err := reports.ComputeUptime(ctx, pool, org, nil, span(day)); return err }},
		{"dashboard uptime, all monitors, 7 days", func() error { _, err := reports.ComputeUptime(ctx, pool, org, nil, span(7*day)); return err }},
		{"dashboard uptime, all monitors, 30 days", func() error { _, err := reports.ComputeUptime(ctx, pool, org, nil, span(30*day)); return err }},
		{"dashboard uptime, all monitors, 90 days", func() error { _, err := reports.ComputeUptime(ctx, pool, org, nil, span(90*day)); return err }},
		{"one monitor's uptime, 90 days", func() error { _, err := reports.ComputeUptime(ctx, pool, org, &mon, span(90*day)); return err }},
		{"response-time chart, one monitor, 30 days", func() error { _, _, err := reports.Series(ctx, pool, org, &mon, span(30*day)); return err }},
		{"history page (50 rows + total), 30 days", func() error { _, _, err := reports.Results(ctx, pool, org, mon, span(30*day), 50, 0); return err }},
		{"monthly report, one monitor", func() error {
			_, err := reports.BuildMonthly(ctx, pool, org, mon, lastMonth, now, 365)
			return err
		}},
		{fmt.Sprintf("public status page, %d monitors, 90 days (uncached)", o.monitors), func() error {
			_, err := statuspage.NewService(pool, plans.NewService("http://localhost")).Public(ctx, "load-test")
			return err
		}},
	}
	fmt.Printf("\nQueries (%d runs each; first run is the coldest)\n\n| query | first | median | max |\n|---|---|---|---|\n", o.repeat)
	for _, x := range qs {
		var ds []float64
		for range o.repeat {
			t := time.Now()
			if err := x.fn(); err != nil {
				return fmt.Errorf("%s: %w", x.name, err)
			}
			ds = append(ds, float64(time.Since(t).Microseconds())/1000)
		}
		fmt.Printf("| %s | %s | %s | %s |\n", x.name, ms(ds[0]), ms(pct(ds, 0.5)), ms(pct(ds, 1)))
	}
	return nil
}

func ms(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.2f s", v/1000)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func size(b int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(b)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", f), "0"), ".") + " " + units[i]
}
