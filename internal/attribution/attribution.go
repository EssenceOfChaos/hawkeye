// Package attribution records how a person found HawkEye when they sign up: the campaign tags or ad click their browser first arrived with
// (web/src/attribution.ts), otherwise the site that linked to us, and the page they landed on.
//
// The browser sends what it remembered with the sign-up request. It is untrusted input, so every field is cleaned here: a value that is not a short
// plain label is dropped, never stored, which keeps email addresses, tokens and full addresses out. A bad value never fails a sign-up.
package attribution

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Attribution is what the browser reports. Every field is optional.
type Attribution struct {
	Source       string `json:"source,omitempty"`
	Medium       string `json:"medium,omitempty"`
	Campaign     string `json:"campaign,omitempty"`
	ClickID      string `json:"click_id,omitempty"`
	ReferrerHost string `json:"referrer_host,omitempty"`
	LandingPath  string `json:"landing_path,omitempty"`
	// FirstSeenAt is when the browser first arrived with this, as the browser's clock tells it.
	FirstSeenAt *time.Time `json:"first_seen_at,omitempty"`
}

var (
	// Campaign names are written by whoever made the link, so spaces and a little punctuation are allowed, but not @ (no email addresses) or = & ? #.
	label   = regexp.MustCompile(`^[A-Za-z0-9 ._+:/()-]{1,100}$`)
	host    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
	path    = regexp.MustCompile(`^/[A-Za-z0-9/._-]{0,199}$`)
	clickID = map[string]bool{"google_ads": true, "microsoft_ads": true, "meta_ads": true, "openai_ads": true}
)

func cleanLabel(s string, lower bool) string {
	s = strings.TrimSpace(s)
	if !label.MatchString(s) {
		return ""
	}
	if lower {
		return strings.ToLower(s)
	}
	return s
}

// Clean returns a with every field that is not acceptable removed, and whether anything is left.
func Clean(a Attribution, now time.Time) (Attribution, bool) {
	out := Attribution{
		Source:   cleanLabel(a.Source, true),
		Medium:   cleanLabel(a.Medium, true),
		Campaign: cleanLabel(a.Campaign, false),
	}
	if clickID[a.ClickID] {
		out.ClickID = a.ClickID
	}
	if h := strings.ToLower(strings.TrimSpace(a.ReferrerHost)); len(h) <= 253 && host.MatchString(h) {
		out.ReferrerHost = h
	}
	if path.MatchString(a.LandingPath) {
		out.LandingPath = a.LandingPath
	}
	// The browser keeps what it remembered for 90 days; allow a little clock skew either way.
	if t := a.FirstSeenAt; t != nil && t.After(now.AddDate(0, 0, -100)) && t.Before(now.Add(24*time.Hour)) {
		u := t.UTC()
		out.FirstSeenAt = &u
	}
	empty := out.Source == "" && out.Medium == "" && out.Campaign == "" && out.ClickID == "" && out.ReferrerHost == "" && out.LandingPath == ""
	if empty {
		return Attribution{}, false
	}
	return out, true
}

func null(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Save cleans a and stores it for a new user, inside the sign-up transaction. It stores nothing when no field survives cleaning.
func Save(ctx context.Context, tx pgx.Tx, userID uuid.UUID, a *Attribution) error {
	if a == nil {
		return nil
	}
	c, ok := Clean(*a, time.Now())
	if !ok {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO signup_attributions (user_id, source, medium, campaign, click_id, referrer_host, landing_path, first_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		userID, null(c.Source), null(c.Medium), null(c.Campaign), null(c.ClickID), null(c.ReferrerHost), null(c.LandingPath), c.FirstSeenAt)
	return err
}

// ForUser returns what was recorded when the user signed up, or nil. Used by the data export.
func ForUser(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID uuid.UUID) (*Attribution, error) {
	var (
		a                                   Attribution
		src, med, camp, click, ref, landing *string
	)
	err := q.QueryRow(ctx, `SELECT source, medium, campaign, click_id, referrer_host, landing_path, first_seen_at
		FROM signup_attributions WHERE user_id = $1`, userID).Scan(&src, &med, &camp, &click, &ref, &landing, &a.FirstSeenAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	a.Source, a.Medium, a.Campaign, a.ClickID, a.ReferrerHost, a.LandingPath = deref(src), deref(med), deref(camp), deref(click), deref(ref), deref(landing)
	return &a, nil
}
