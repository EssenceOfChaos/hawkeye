package notifications_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/notifications"
)

var errTransient = errors.New("smtp: 451 try later")

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// site adds a monitor with an open incident, and returns the incident.
func (e *env) site(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var monitor, inc uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds)
		VALUES ($1, $2, 'https://'||lower($2)||'.example.com', 60) RETURNING id`, e.org, name).Scan(&monitor); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO incidents (monitor_id, started_at, cause_category, summary)
		VALUES ($1, now() - interval '1 minute', 'http_status', 'Received HTTP 502') RETURNING id`, monitor).Scan(&inc); err != nil {
		t.Fatal(err)
	}
	return inc
}

func (e *env) setting(t *testing.T, addr string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination, verified_at)
		VALUES ($1, 'email', $2, now()) RETURNING id`, e.org, addr).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) queue(t *testing.T, inc, setting uuid.UUID, event string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type)
		VALUES ($1, $2, $3) RETURNING id`, inc, setting, event).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// drain runs the dispatcher until nothing is due.
func drain(t *testing.T, d *notifications.Dispatcher) {
	t.Helper()
	for range 50 {
		worked, err := d.ProcessOne(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("the dispatcher never ran out of work")
}

// makeDue brings held deliveries forward, as if the group window had passed.
func (e *env) makeDue(t *testing.T) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `UPDATE notification_deliveries SET next_attempt_at = now() WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
}

func TestAlertsDueTogetherAreSentAsOneMessage(t *testing.T) {
	e := newEnv(t, 5)
	s := e.setting(t, "agency@example.com")
	for _, n := range []string{"Alpha", "Bravo", "Charlie"} {
		e.queue(t, e.site(t, n), s, "outage")
	}
	mail := &scriptedSender{}
	drain(t, dispatcher(e, mail, notifications.DispatcherConfig{}))
	if len(mail.sent) != 1 {
		t.Fatalf("want one message, got %d", len(mail.sent))
	}
	m := mail.sent[0]
	if m.Subject != "HawkEye alert: 3 monitors may be offline" {
		t.Fatalf("subject %q", m.Subject)
	}
	for _, n := range []string{"Alpha", "Bravo", "Charlie", "Received HTTP 502"} {
		if !strings.Contains(m.Text, n) {
			t.Errorf("the message lists %q:\n%s", n, m.Text)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE status = 'sent'`); n != 3 {
		t.Fatalf("every alert in the group is marked sent: %d", n)
	}
}

// The first alert is never held back; the ones that follow within the window wait for it and go out together.
func TestTheFirstAlertGoesOutAtOnceAndTheNextOnesTogether(t *testing.T) {
	e := newEnv(t, 5)
	s := e.setting(t, "agency@example.com")
	e.queue(t, e.site(t, "Alpha"), s, "outage")
	mail := &scriptedSender{}
	d := dispatcher(e, mail, notifications.DispatcherConfig{})
	drain(t, d)
	if len(mail.sent) != 1 || mail.sent[0].Subject != "HawkEye alert: Alpha may be offline" {
		t.Fatalf("the first alert goes out on its own, at once: %+v", mail.sent)
	}

	b := e.queue(t, e.site(t, "Bravo"), s, "outage")
	c := e.queue(t, e.site(t, "Charlie"), s, "outage")
	drain(t, d)
	if len(mail.sent) != 1 {
		t.Fatal("alerts right after the first are held")
	}
	var held int
	var latest time.Time
	e.pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE group_held), max(next_attempt_at) FROM notification_deliveries WHERE id = ANY($1)`,
		[]uuid.UUID{b, c}).Scan(&held, &latest)
	if held != 2 || time.Until(latest) > notifications.DefaultGroupWindow || time.Until(latest) < notifications.DefaultGroupWindow-30*time.Second {
		t.Fatalf("held=%d, due in %v", held, time.Until(latest))
	}

	e.makeDue(t)
	drain(t, d)
	if len(mail.sent) != 2 || mail.sent[1].Subject != "HawkEye alert: 2 monitors may be offline" {
		t.Fatalf("%d messages; last %q", len(mail.sent), mail.sent[len(mail.sent)-1].Subject)
	}
	if strings.Contains(mail.sent[1].Text, "Alpha") {
		t.Fatal("an alert already sent is not repeated")
	}
}

// Grouping changes how alerts are packaged, never whether they are sent.
func TestAHeldAlertIsSentEvenIfItsMonitorRecoversFirst(t *testing.T) {
	e := newEnv(t, 5)
	s := e.setting(t, "agency@example.com")
	e.queue(t, e.site(t, "Alpha"), s, "outage")
	mail := &scriptedSender{}
	d := dispatcher(e, mail, notifications.DispatcherConfig{})
	drain(t, d)
	inc := e.site(t, "Bravo")
	id := e.queue(t, inc, s, "outage")
	drain(t, d) // held
	tx, err := e.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := notifications.CancelHeldOutage(t.Context(), tx, inc); err != nil {
		t.Fatal(err)
	}
	tx.Commit(t.Context())
	if r := e.row(t, id); r.Status != "pending" {
		t.Fatalf("a recovery must not cancel an alert held only for grouping: %+v", r)
	}
	e.makeDue(t)
	drain(t, d)
	if len(mail.sent) != 2 || e.row(t, id).Status != "sent" {
		t.Fatal("the held alert is sent")
	}
}

func TestGroupingIsPerDestinationAndPerKind(t *testing.T) {
	e := newEnv(t, 5)
	a, b := e.setting(t, "a@example.com"), e.setting(t, "b@example.com")
	x, y := e.site(t, "Alpha"), e.site(t, "Bravo")
	e.queue(t, x, a, "outage")
	e.queue(t, y, a, "outage")
	e.queue(t, x, b, "outage")
	e.queue(t, y, a, "recovery")
	mail := &scriptedSender{}
	drain(t, dispatcher(e, mail, notifications.DispatcherConfig{}))
	got := map[string]string{}
	for _, m := range mail.sent {
		got[m.To+" "+m.Subject] = m.Text
	}
	for _, want := range []string{"a@example.com HawkEye alert: 2 monitors may be offline", "b@example.com HawkEye alert: Alpha may be offline",
		"a@example.com HawkEye recovery: Bravo is back online"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if len(mail.sent) != 3 {
		t.Fatalf("want 3 messages, got %d", len(mail.sent))
	}
}

func TestGroupingCanBeTurnedOff(t *testing.T) {
	for name, setup := range map[string]func(e *env) notifications.DispatcherConfig{
		"by the owner": func(e *env) notifications.DispatcherConfig {
			e.pool.Exec(t.Context(), `UPDATE organizations SET alert_grouping = false`)
			return notifications.DispatcherConfig{}
		},
		"for the installation": func(*env) notifications.DispatcherConfig { return notifications.DispatcherConfig{GroupWindow: -1} },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, 5)
			cfg := setup(e)
			s := e.setting(t, "a@example.com")
			for _, n := range []string{"Alpha", "Bravo", "Charlie"} {
				e.queue(t, e.site(t, n), s, "outage")
			}
			mail := &scriptedSender{}
			drain(t, dispatcher(e, mail, cfg))
			if len(mail.sent) != 3 {
				t.Fatalf("one message per alert without grouping, got %d", len(mail.sent))
			}
		})
	}
}

// Two dispatchers racing for one destination: the first alert still goes out at once, and nothing is lost.
func TestConcurrentDispatchersNeverHoldTheFirstAlert(t *testing.T) {
	for range 5 {
		e := newEnv(t, 10)
		s := e.setting(t, "a@example.com")
		for _, n := range []string{"Alpha", "Bravo", "Charlie", "Delta"} {
			e.queue(t, e.site(t, n), s, "outage")
		}
		mail := &scriptedSender{}
		d1, d2 := dispatcher(e, mail, notifications.DispatcherConfig{}), dispatcher(e, mail, notifications.DispatcherConfig{})
		var wg sync.WaitGroup
		for _, d := range []*notifications.Dispatcher{d1, d2} {
			wg.Add(1)
			go func() { defer wg.Done(); drain(t, d) }()
		}
		wg.Wait()
		if mail.callCount() == 0 {
			t.Fatal("nothing went out at once")
		}
		e.makeDue(t)
		drain(t, d1)
		if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE status = 'sent'`); n != 4 {
			t.Fatalf("every alert is sent: %d", n)
		}
		if mail.callCount() > 2 {
			t.Fatalf("four alerts at once make at most two messages: %d", mail.callCount())
		}
	}
}

func TestAGroupThatFailsIsRetriedTogether(t *testing.T) {
	e := newEnv(t, 5)
	s := e.setting(t, "a@example.com")
	e.queue(t, e.site(t, "Alpha"), s, "outage")
	e.queue(t, e.site(t, "Bravo"), s, "outage")
	mail := &scriptedSender{errs: []error{errTransient}}
	drain(t, dispatcher(e, mail, notifications.DispatcherConfig{}))
	if mail.callCount() != 2 || len(mail.sent) != 1 || mail.sent[0].Subject != "HawkEye alert: 2 monitors may be offline" {
		t.Fatalf("calls=%d sent=%+v", mail.callCount(), mail.sent)
	}
}
