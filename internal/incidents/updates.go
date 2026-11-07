package incidents

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/validation"
)

// Updates are notes an owner posts on an incident. They are public wherever a published status page lists the incident's monitor, so they are
// plain text only: nothing here or on the page turns them into markup or links.
const (
	MaxUpdateLength       = 1000
	MaxUpdatesPerIncident = 50
)

// UpdateStatuses are the stages an update can announce, in the order an incident usually goes through them.
var UpdateStatuses = []string{"investigating", "identified", "monitoring", "resolved"}

var (
	ErrIncidentNotFound = errors.New("incident not found")
	ErrUpdateNotFound   = errors.New("incident update not found")
)

type Update struct {
	ID          uuid.UUID `json:"id"`
	IncidentID  uuid.UUID `json:"incident_id"`
	Status      string    `json:"status"`
	Message     string    `json:"message"`
	AuthorEmail *string   `json:"author_email"` // for the team only; never on a public page. Null once the author's account is gone.
	CreatedAt   time.Time `json:"created_at"`
}

// PageRef is a published status page that shows an incident, so the person posting knows who will read it.
type PageRef struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

// CleanMessage trims a message, normalizes line breaks and refuses what a status page should not show. Exported for the tests of the rules.
func CleanMessage(s string) (string, error) {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
	if s == "" {
		return "", validation.New("message", "is required")
	}
	if n := len([]rune(s)); n > MaxUpdateLength {
		return "", validation.New("message", fmt.Sprintf("must be at most %d characters", MaxUpdateLength))
	}
	for _, r := range s {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || r == ' ' || r == ' ' || unicode.Is(unicode.Bidi_Control, r)) {
			return "", validation.New("message", "contains a control character")
		}
	}
	return s, nil
}

// orgIncident confirms the incident belongs to the organization (through its monitor), so another organization's incident is ErrIncidentNotFound.
func orgIncident(ctx context.Context, q pgx.Tx, orgID, incidentID uuid.UUID, lock bool) error {
	sql := `SELECT 1 FROM incidents i JOIN monitors m ON m.id = i.monitor_id WHERE i.id = $1 AND m.organization_id = $2`
	if lock {
		sql += ` FOR UPDATE OF i`
	}
	var one int
	err := q.QueryRow(ctx, sql, incidentID, orgID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIncidentNotFound
	}
	return err
}

// ListUpdates returns an incident's updates, newest first.
func ListUpdates(ctx context.Context, pool *pgxpool.Pool, orgID, incidentID uuid.UUID) ([]Update, error) {
	out := []Update{}
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := orgIncident(ctx, tx, orgID, incidentID, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT u.id, u.incident_id, u.status, u.message, us.email, u.created_at
			FROM incident_updates u LEFT JOIN users us ON us.id = u.author_id WHERE u.incident_id = $1 ORDER BY u.created_at DESC, u.id`, incidentID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Update, error) {
			var u Update
			return u, r.Scan(&u.ID, &u.IncidentID, &u.Status, &u.Message, &u.AuthorEmail, &u.CreatedAt)
		})
		return err
	})
	if out == nil {
		out = []Update{}
	}
	return out, err
}

// PostUpdate adds an update to one of the organization's incidents, open or resolved.
func PostUpdate(ctx context.Context, pool *pgxpool.Pool, orgID, incidentID, authorID uuid.UUID, status, message string) (Update, error) {
	if !slices.Contains(UpdateStatuses, status) {
		return Update{}, validation.New("status", "must be one of "+strings.Join(UpdateStatuses, ", "))
	}
	message, err := CleanMessage(message)
	if err != nil {
		return Update{}, err
	}
	var u Update
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// The lock serializes posts on one incident, so two at once cannot both slip under the limit.
		if err := orgIncident(ctx, tx, orgID, incidentID, true); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM incident_updates WHERE incident_id = $1`, incidentID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxUpdatesPerIncident {
			return validation.New("message", fmt.Sprintf("this incident already has %d updates, the most it can have", MaxUpdatesPerIncident))
		}
		return tx.QueryRow(ctx, `WITH ins AS (INSERT INTO incident_updates (incident_id, status, message, author_id) VALUES ($1, $2, $3, $4)
				RETURNING id, incident_id, status, message, author_id, created_at)
			SELECT ins.id, ins.incident_id, ins.status, ins.message, us.email, ins.created_at FROM ins LEFT JOIN users us ON us.id = ins.author_id`,
			incidentID, status, message, authorID).Scan(&u.ID, &u.IncidentID, &u.Status, &u.Message, &u.AuthorEmail, &u.CreatedAt)
	})
	return u, err
}

// DeleteUpdate removes one update, for a typo or a message posted on the wrong incident.
func DeleteUpdate(ctx context.Context, pool *pgxpool.Pool, orgID, incidentID, updateID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM incident_updates u USING incidents i, monitors m
		WHERE u.id = $1 AND u.incident_id = $2 AND i.id = u.incident_id AND m.id = i.monitor_id AND m.organization_id = $3`, updateID, incidentID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUpdateNotFound
	}
	return nil
}

// PagesShowing lists the published status pages that show this incident, which is every one that lists its monitor.
func PagesShowing(ctx context.Context, pool *pgxpool.Pool, orgID, incidentID uuid.UUID) ([]PageRef, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT p.title, p.slug FROM incidents i
		JOIN monitors m ON m.id = i.monitor_id
		JOIN status_page_monitors pm ON pm.monitor_id = m.id
		JOIN status_pages p ON p.id = pm.page_id AND p.published
		WHERE i.id = $1 AND m.organization_id = $2 ORDER BY p.title, p.slug`, incidentID, orgID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[PageRef])
	if out == nil {
		out = []PageRef{}
	}
	return out, err
}
