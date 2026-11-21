// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The email domains an SSO provider speaks for (migration 0109).
//
// An identity provider asserts whatever email address it likes, and on a
// cell with several organizations each org admin can configure one. So a
// provider is trusted with an email address only inside the domains a
// cell operator assigned it: that decides which accounts it may sign in
// to or create, and which provider the login page sends an address to.
// One provider per domain on the whole cell.

package identity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDomainTaken is returned when a domain is already assigned to another
// provider.
var ErrDomainTaken = errors.New("identity: email domain already assigned to another sign-in")

// ErrInvalidDomain is returned for a value that is not a bare domain.
var ErrInvalidDomain = errors.New("identity: not an email domain")

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// NormalizeDomain lowercases and validates a bare email domain
// ("example.com"). A leading "@" is tolerated, since that is how people
// write it.
func NormalizeDomain(d string) (string, error) {
	d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(d), "@")))
	if len(d) > 253 || !domainRe.MatchString(d) {
		return "", ErrInvalidDomain
	}
	return d, nil
}

// EmailDomain returns the lowercased domain of an email address, or ""
// when there is none.
func EmailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[at+1:]))
}

// ProviderDomains lists the domains assigned to one provider.
func (s *Store) ProviderDomains(ctx context.Context, providerID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT domain FROM auth_provider_domains WHERE provider_id=$1 ORDER BY domain`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetProviderDomains replaces a provider's domains. A domain held by a
// different provider fails the whole change with ErrDomainTaken.
func (s *Store) SetProviderDomains(ctx context.Context, providerID uuid.UUID, domains []string) ([]string, error) {
	seen := map[string]bool{}
	clean := make([]string, 0, len(domains))
	for _, d := range domains {
		n, err := NormalizeDomain(d)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrInvalidDomain, d)
		}
		if !seen[n] {
			seen[n] = true
			clean = append(clean, n)
		}
	}
	sort.Strings(clean)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM auth_provider_domains WHERE provider_id=$1`, providerID); err != nil {
		return nil, err
	}
	for _, d := range clean {
		if _, err := tx.Exec(ctx, `INSERT INTO auth_provider_domains (domain, provider_id) VALUES ($1,$2)`, d, providerID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return nil, fmt.Errorf("%w: %s", ErrDomainTaken, d)
			}
			return nil, err
		}
	}
	return clean, tx.Commit(ctx)
}

// ProviderForEmail returns the enabled provider that holds an email
// address's domain. ok=false when no enabled provider does.
func (s *Store) ProviderForEmail(ctx context.Context, email string) (AuthProvider, bool, error) {
	d := EmailDomain(email)
	if d == "" {
		return AuthProvider{}, false, nil
	}
	p, err := s.scanAuthProvider(s.pool.QueryRow(ctx,
		`SELECT `+prefixedProviderCols+` FROM auth_providers p
		   JOIN auth_provider_domains d ON d.provider_id = p.id
		  WHERE d.domain = $1 AND p.enabled`, d))
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthProvider{}, false, nil
	}
	if err != nil {
		return AuthProvider{}, false, err
	}
	return p, true, nil
}

// CountOrgs is the number of organizations on the cell. A cell with one
// has a single identity provider owner, which is what lets a provider
// without assigned domains keep linking by email there.
func (s *Store) CountOrgs(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM orgs`).Scan(&n)
	return n, err
}

// prefixedProviderCols is authProviderCols qualified for a join.
var prefixedProviderCols = func() string {
	cols := strings.Split(authProviderCols, ",")
	for i, c := range cols {
		cols[i] = "p." + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}()
