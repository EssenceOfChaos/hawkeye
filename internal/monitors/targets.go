package monitors

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Covers reports, for each proposed monitor (type, URL and DNS record type), whether one of the organization's monitors already checks the same
// thing. URLs match regardless of case and of a trailing slash; a DNS monitor matches on host name and record type.
func (s *Service) Covers(ctx context.Context, orgID uuid.UUID, proposed []Target) ([]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT kind, url, COALESCE(dns_record_type, '') FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL`, orgID)
	if err != nil {
		return nil, err
	}
	existing, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Target])
	if err != nil {
		return nil, err
	}
	have := map[Target]bool{}
	for _, t := range existing {
		have[t.key()] = true
	}
	out := make([]bool, len(proposed))
	for i, p := range proposed {
		out[i] = have[p.key()]
	}
	return out, nil
}

// Target is what a monitor checks, as far as duplicates are concerned.
type Target struct {
	Type          string
	URL           string
	DNSRecordType string
}

func (t Target) key() Target {
	k := Target{Type: t.Type, URL: strings.TrimSuffix(strings.ToLower(strings.TrimSpace(t.URL)), "/")}
	if k.Type == "" {
		k.Type = TypeHTTP
	}
	if k.Type == TypeDNS {
		k.DNSRecordType = strings.ToUpper(t.DNSRecordType)
	}
	return k
}
