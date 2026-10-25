// Package branding stores an organization's report branding (the name and logo printed on PDF reports) and whether it gets the monthly report email.
// Who may use which part is decided by the plan, in the callers; this package only stores and validates.
package branding

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/reportpdf"
	"hawkeye/internal/validation"
)

// ErrNoLogo: the organization has not uploaded a logo.
var ErrNoLogo = errors.New("no logo")

// MaxNameLength bounds the display name.
const MaxNameLength = 100

// Branding is what the settings page shows.
type Branding struct {
	DisplayName        string `json:"display_name"`
	HasLogo            bool   `json:"has_logo"`
	LogoWidth          int    `json:"logo_width"`
	LogoHeight         int    `json:"logo_height"`
	MonthlyReportEmail bool   `json:"monthly_report_email"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Get returns the organization's branding; an organization that never saved any has the zero value.
func (s *Service) Get(ctx context.Context, orgID uuid.UUID) (Branding, error) {
	var b Branding
	var w, h *int
	err := s.pool.QueryRow(ctx, `SELECT display_name, logo IS NOT NULL, logo_width, logo_height, monthly_report_email
		FROM organization_branding WHERE organization_id = $1`, orgID).Scan(&b.DisplayName, &b.HasLogo, &w, &h, &b.MonthlyReportEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return Branding{}, nil
	}
	if w != nil && h != nil {
		b.LogoWidth, b.LogoHeight = *w, *h
	}
	return b, err
}

// CleanName validates a display name: one line of plain text, trimmed.
func CleanName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > MaxNameLength {
		return "", validation.New("display_name", "must be 100 characters or fewer")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", validation.New("display_name", "must be a single line of text")
		}
	}
	return s, nil
}

// SaveName stores the display name (empty clears it), leaving the rest of the branding alone.
func (s *Service) SaveName(ctx context.Context, orgID uuid.UUID, name string) error {
	name, err := CleanName(name)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO organization_branding (organization_id, display_name) VALUES ($1, $2)
		ON CONFLICT (organization_id) DO UPDATE SET display_name = EXCLUDED.display_name`, orgID, name)
	return err
}

// SetMonthlyEmail turns the monthly report email on or off, leaving the rest alone.
func (s *Service) SetMonthlyEmail(ctx context.Context, orgID uuid.UUID, on bool) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO organization_branding (organization_id, monthly_report_email) VALUES ($1, $2)
		ON CONFLICT (organization_id) DO UPDATE SET monthly_report_email = EXCLUDED.monthly_report_email`, orgID, on)
	return err
}

// SaveLogo validates, cleans and stores an uploaded image, replacing any earlier logo.
func (s *Service) SaveLogo(ctx context.Context, orgID uuid.UUID, raw []byte) error {
	l, err := NormalizeLogo(raw)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO organization_branding (organization_id, logo, logo_type, logo_width, logo_height) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id) DO UPDATE SET logo = EXCLUDED.logo, logo_type = EXCLUDED.logo_type,
			logo_width = EXCLUDED.logo_width, logo_height = EXCLUDED.logo_height`, orgID, l.Data, l.Type, l.Width, l.Height)
	return err
}

// DeleteLogo removes the logo; it succeeds when there is none.
func (s *Service) DeleteLogo(ctx context.Context, orgID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE organization_branding SET logo = NULL, logo_type = NULL, logo_width = NULL, logo_height = NULL WHERE organization_id = $1`, orgID)
	return err
}

// Logo returns the stored image and its content type.
func (s *Service) Logo(ctx context.Context, orgID uuid.UUID) (data []byte, contentType string, err error) {
	var typ *string
	err = s.pool.QueryRow(ctx, `SELECT logo, logo_type FROM organization_branding WHERE organization_id = $1`, orgID).Scan(&data, &typ)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (data == nil || typ == nil)) {
		return nil, "", ErrNoLogo
	}
	if err != nil {
		return nil, "", err
	}
	return data, "image/" + *typ, nil
}

// Brand is the look to print on an organization's reports. With branded false (the plan does not include branding) it is the standard HawkEye look,
// whatever is stored: the stored name and logo are kept for when the plan allows them again.
func (s *Service) Brand(ctx context.Context, orgID uuid.UUID, branded bool) (reportpdf.Brand, error) {
	if !branded {
		return reportpdf.Brand{}, nil
	}
	var b reportpdf.Brand
	var w, h *int
	err := s.pool.QueryRow(ctx, `SELECT display_name, COALESCE(logo, ''::bytea), COALESCE(logo_type, ''), logo_width, logo_height
		FROM organization_branding WHERE organization_id = $1`, orgID).Scan(&b.Name, &b.Logo, &b.LogoType, &w, &h)
	if errors.Is(err, pgx.ErrNoRows) {
		return reportpdf.Brand{}, nil
	}
	if w != nil && h != nil {
		b.LogoWidth, b.LogoHeight = *w, *h
	}
	return b, err
}
