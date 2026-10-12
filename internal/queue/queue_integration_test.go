package queue_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/queue"
	"hawkeye/internal/testutil"
)

func seed(t *testing.T, pool *pgxpool.Pool, n int) []uuid.UUID {
	t.Helper()
	var org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for range n {
		var id uuid.UUID
		if err := pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds)
			VALUES ($1, 'm', 'https://example.com', 300) RETURNING id`, org).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `INSERT INTO check_jobs (monitor_id) VALUES ($1)`, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestConcurrentClaimsNeverShareAJob(t *testing.T) {
	pool := testutil.NewPoolSize(t, 30)
	const jobs = 40
	seed(t, pool, jobs)

	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				j, _, err := queue.Claim(t.Context(), pool, time.Minute, 3)
				if err != nil {
					t.Error(err)
					return
				}
				if j == nil {
					return
				}
				mu.Lock()
				seen[j.ID]++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(seen) != jobs {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), jobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %d claimed %d times", id, n)
		}
	}
}

func TestLeaseExpiryMakesAbandonedJobsClaimableAgain(t *testing.T) {
	pool := testutil.NewPool(t)
	seed(t, pool, 1)

	j, _, _ := queue.Claim(t.Context(), pool, time.Minute, 3)
	if j == nil || j.Attempts != 1 || j.Reclaimed {
		t.Fatalf("%+v", j)
	}
	if again, _, _ := queue.Claim(t.Context(), pool, time.Minute, 3); again != nil {
		t.Fatal("a leased job must not be claimable while its lease is valid")
	}

	// The worker "crashes": its lease runs out without Complete being called.
	pool.Exec(t.Context(), `UPDATE check_jobs SET locked_until = now() - interval '1 second'`)
	re, _, _ := queue.Claim(t.Context(), pool, time.Minute, 3)
	if re == nil || re.ID != j.ID || re.Attempts != 2 || !re.Reclaimed {
		t.Fatalf("expected the same job reclaimed, got %+v", re)
	}
	if err := queue.Complete(t.Context(), pool, re.ID); err != nil {
		t.Fatal(err)
	}
	if j, _, _ := queue.Claim(t.Context(), pool, time.Minute, 3); j != nil {
		t.Fatal("completed job came back")
	}
}

func TestPoisonJobsAreDroppedAfterMaxAttempts(t *testing.T) {
	pool := testutil.NewPool(t)
	seed(t, pool, 1)
	for i := 1; i <= 3; i++ {
		j, _, _ := queue.Claim(t.Context(), pool, time.Minute, 3)
		if j == nil || j.Attempts != i {
			t.Fatalf("attempt %d: %+v", i, j)
		}
		pool.Exec(t.Context(), `UPDATE check_jobs SET locked_until = now() - interval '1 second'`)
	}
	j, dropped, _ := queue.Claim(t.Context(), pool, time.Minute, 3)
	if j != nil || dropped != 1 {
		t.Fatalf("job=%+v dropped=%d", j, dropped)
	}
}

func TestReleaseDelaysAndOnlyOneJobPerMonitor(t *testing.T) {
	pool := testutil.NewPool(t)
	ids := seed(t, pool, 1)
	j, _, _ := queue.Claim(t.Context(), pool, time.Minute, 3)
	if err := queue.Release(t.Context(), pool, j.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := queue.Claim(t.Context(), pool, time.Minute, 3); again != nil {
		t.Fatal("released job must wait for its delay")
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO check_jobs (monitor_id) VALUES ($1)`, ids[0]); err == nil {
		t.Fatal("a second job for the same monitor must violate the unique constraint")
	}
}

func TestSnapshot(t *testing.T) {
	pool := testutil.NewPool(t)
	seed(t, pool, 3)
	queue.Claim(t.Context(), pool, time.Minute, 3)
	pool.Exec(t.Context(), `UPDATE check_jobs SET run_at = now() - interval '90 seconds' WHERE status = 'queued'`)
	s, err := queue.Snapshot(t.Context(), pool)
	if err != nil || s.Queued != 2 || s.Running != 1 || s.OldestRunnable < 85*time.Second {
		t.Fatalf("%+v %v", s, err)
	}
}
