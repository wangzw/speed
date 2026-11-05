package selfcreate

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
)

// identifierRetries is how many times a generated identifier is retried after
// the first when the roster already holds it. The pre-check can only see this
// replica's snapshot, so a cross-replica collision is still possible and is
// answered by the provisioner's own refusal.
const identifierRetries = 3

// lockTTL is how long one account's creation lock lives when the process that
// took it stops before releasing it.
const lockTTL = 30 * time.Second

// errIdentifierUnavailable reports an alias or generated identifier the roster
// already holds. Retrying does not succeed.
var errIdentifierUnavailable = errors.New("the organization's alias or generated identifier is unavailable")

// handleCreate serves POST /api/v1/orgs. The order of the decisions is the
// product's and each step reads only what it needs:
//
//  1. an identity is required;
//  2. an impersonating identity is refused before anything is read;
//  3. a service account and a static subject are refused;
//  4. the account's email address must not be pending (a read failure is 503);
//  5. the profile and alias are normalized and held to their shape, the
//     reserved-name set included;
//  6. the alias is checked against the roster's one namespace;
//  7. the per-account creation lock is taken;
//  8. the active-business-organization cap is checked;
//  9. the hourly count is checked;
//  10. a free identifier is generated;
//  11. one attempt is counted;
//  12. the mandatory audit row lands;
//  13. the tenant is provisioned.
//
// A refusal in steps 1-10 writes no count. Step 11 consumes one place before
// the mandatory audit and the provisioner, so a failure in either step consumes
// it: the count limits attempts, not successes.
func (p *product) handleCreate(ctx context.Context, in createInput) (OrganizationCreatedReply, error) {
	identity, ok := authn.FromContext(ctx)
	if !ok {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusUnauthorized}
	}
	if identity.Impersonating() {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}
	if slices.Contains(identity.Methods(), authn.MethodClientSecret) {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}
	account := identity.Account()
	if account == "" || account.IsStatic() {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusForbidden}
	}

	status, err := p.verification.SignupStateOf(ctx, account)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		// No account row holds no email address; the rest of the pipeline answers
		// for a caller that should not have an identity at all.
	case err != nil:
		p.logger.Error(ModuleName+": the signup state could not be read, so the creation was not carried out",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	case status.State == authn.SignupPending:
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status: http.StatusForbidden,
			Type:   authn.ContactRequiredProblemType,
		}
	}

	alias := strings.TrimSpace(in.Alias)
	profile := tenant.Profile{
		DisplayName: in.DisplayName,
		Description: in.Description,
	}.Normalize()
	if profile.DisplayName == "" || profile.Validate() != nil || !tenant.IsAlias(alias) {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusBadRequest}
	}
	reserved, err := p.reservations.ReservedUsername(ctx, alias)
	if err != nil {
		p.logger.Error(ModuleName+": the reserved-name set could not be read, so the creation was not carried out",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if reserved {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusBadRequest}
	}

	available, aliasErr := p.aliasAvailable(ctx, alias)
	if aliasErr != nil {
		p.logger.Error(ModuleName+": the alias could not be checked against the roster",
			"account", account.String(), "cause", aliasErr.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if !available {
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   problemPrefix + identifierUnavailable,
		}
	}

	lockKey := lockKeyOf(account)
	locked, err := p.kv.SetIfAbsent(ctx, lockKey, []byte(p.now().UTC().Format(time.RFC3339Nano)), lockTTL)
	if err != nil {
		p.logger.Error(ModuleName+": the creation lock could not be taken",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if !locked {
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   problemPrefix + creationInProgressCode,
		}
	}
	defer p.releaseLock(lockKey)

	limits, err := p.limits()
	if err != nil {
		p.logger.Error(ModuleName+": the creation limits could not be read, so the creation was not carried out",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	active, err := p.activeBusinessOrganizations(ctx, account)
	if err != nil {
		p.logger.Error(ModuleName+": the account's business organizations could not be counted",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if active >= limits.MaxOrganizations {
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status: http.StatusConflict,
			Type:   problemPrefix + organizationLimitCode,
		}
	}

	count, err := p.hourlyCount(ctx, account)
	if err != nil {
		p.logger.Error(ModuleName+": the hourly creation count could not be read",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if count >= limits.MaxPerHour {
		return OrganizationCreatedReply{}, speedhttp.Fault{
			Status:  http.StatusTooManyRequests,
			Type:    problemPrefix + rateLimitedCode,
			Headers: []speedhttp.HeaderValue{{Name: "Retry-After", Value: strconv.Itoa(p.retryAfter())}},
		}
	}

	record, err := p.newOrganization(ctx, alias, account, profile)
	if err != nil {
		if errors.Is(err, errIdentifierUnavailable) {
			return OrganizationCreatedReply{}, speedhttp.Fault{
				Status: http.StatusConflict,
				Type:   problemPrefix + identifierUnavailable,
			}
		}
		p.logger.Error(ModuleName+": a free identifier could not be chosen",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	if err := p.recordAttempt(ctx, account); err != nil {
		p.logger.Error(ModuleName+": the creation could not be counted, so it was not carried out",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	actionID := uuid.NewString()
	if err := p.auditRequired(ctx, audit.Entry{
		Action:  auditAction,
		Object:  string(record.ID),
		Outcome: outcomeSuccess,
		Detail: map[string]string{
			detailActionID: actionID,
			detailAccount:  account.String(),
			detailTenant:   string(record.ID),
		},
	}); err != nil {
		return OrganizationCreatedReply{}, err
	}

	if err := p.provisioner.Provision(ctx, record, account.String()); err != nil {
		if errors.Is(err, tenant.ErrTenantExists) || errors.Is(err, tenant.ErrAliasConflict) {
			// Another replica won the alias or identifier between this call's
			// pre-check and its provisioning. The domain belongs to the winner,
			// and the mandatory row this request wrote already records the
			// attempt, so no second row is written for the same request.
			p.logger.Warn(ModuleName+": the alias or generated identifier was taken by another replica",
				"account", account.String(), "tenant", string(record.ID), "cause", err.Error())
			return OrganizationCreatedReply{}, speedhttp.Fault{
				Status: http.StatusConflict,
				Type:   problemPrefix + identifierUnavailable,
			}
		}
		p.auditBestEffort(ctx, audit.Entry{
			Action:  auditAction,
			Object:  string(record.ID),
			Outcome: outcomeFailed,
			Detail:  map[string]string{detailActionID: actionID},
		})
		p.logger.Error(ModuleName+": the organization could not be provisioned",
			"account", account.String(), "tenant", string(record.ID), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	return OrganizationCreatedReply{
		Tenant: OrganizationCreatedTenant{
			ID:          string(record.ID),
			Alias:       record.Alias,
			DisplayName: record.Profile.DisplayName,
			Description: record.Profile.Description,
			Status:      record.Status.String(),
		},
	}, nil
}

// limits reads the two runtime settings from the global layer. A value outside
// the declaration's domain is refused rather than read as a limit: the
// declaration refuses such a write, but a row written around the store is
// served as it is, and a limit of zero would make every creation a refusal
// whose reason the caller cannot act on.
func (p *product) limits() (limitDefaults, error) {
	var limits limitDefaults
	if err := p.settings.Get(settings.Global(), maxOrganizationsPath, &limits.MaxOrganizations); err != nil {
		return limitDefaults{}, fmt.Errorf("reading %s: %w", maxOrganizationsPath, err)
	}
	if err := p.settings.Get(settings.Global(), maxPerHourPath, &limits.MaxPerHour); err != nil {
		return limitDefaults{}, fmt.Errorf("reading %s: %w", maxPerHourPath, err)
	}
	if limits.MaxOrganizations <= 0 || limits.MaxPerHour <= 0 {
		return limitDefaults{}, fmt.Errorf("%s: a stored limit is not a positive integer", ModuleName)
	}
	return limits, nil
}

// activeBusinessOrganizations counts the organizations an account holds an
// active membership in that are active and not personal. The memberships are
// read first and the roster's rows in one call, so the count is two reads
// however many memberships the account holds.
func (p *product) activeBusinessOrganizations(ctx context.Context, account authn.AccountID) (int, error) {
	held, err := p.memberships.Tenants(ctx, account)
	if err != nil {
		return 0, fmt.Errorf("reading the account's memberships: %w", err)
	}
	rows, err := p.roster.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading the roster: %w", err)
	}
	status := make(map[string]tenant.Status, len(rows))
	for _, row := range rows {
		status[string(row.ID)] = row.Status
	}
	count := 0
	for _, membership := range held {
		if !membership.Active || tenantpersonal.IsPersonalTenantID(membership.Tenant) {
			continue
		}
		if status[membership.Tenant] == tenant.StatusActive {
			count++
		}
	}
	return count, nil
}

// hourlyCount reads the creation count of the account's current hourly window.
func (p *product) hourlyCount(ctx context.Context, account authn.AccountID) (int, error) {
	raw, present, err := p.kv.Get(ctx, hourlyKey(account, p.now()))
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, nil
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("the hourly counter is not a number: %w", err)
	}
	return int(value), nil
}

// recordAttempt consumes one place in the account's hourly window. A missing
// key starts from zero with an expiry just past the next window boundary; a
// live key keeps its expiry, so the window is not extended by being counted.
func (p *product) recordAttempt(ctx context.Context, account authn.AccountID) error {
	now := p.now()
	ttl := nextHour(now).Sub(now) + time.Minute
	_, err := p.kv.IncrByFloatWithTTL(ctx, hourlyKey(account, now), 1, ttl)
	return err
}

// retryAfter answers the whole seconds until the current window closes, at
// least one.
func (p *product) retryAfter() int {
	now := p.now()
	seconds := int(math.Ceil(nextHour(now).Sub(now).Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

// aliasAvailable reports whether no tenant holds the alias, either as its alias
// or as its identifier. It also refuses an alias that reads as a personal
// tenant's alias, which is how an existing username reserves the name: the
// personal tenant carries the username's alias, so the roster lookup answers
// for both namespaces at once.
func (p *product) aliasAvailable(ctx context.Context, alias string) (bool, error) {
	if _, found, err := p.roster.ByAlias(ctx, alias); err != nil {
		return false, fmt.Errorf("reading the roster's alias %q: %w", alias, err)
	} else if found {
		return false, nil
	}
	if _, found, err := p.roster.Get(ctx, tenant.ID(alias)); err != nil {
		return false, fmt.Errorf("reading the roster for %q: %w", alias, err)
	} else if found {
		return false, nil
	}
	return true, nil
}

// newOrganization generates one candidate identifier and pre-checks it against
// the roster, retrying the generation up to identifierRetries times. The
// pre-check reads this replica's snapshot, so a row another replica wrote since
// the snapshot is invisible here; the provisioner's own refusal is the arbiter
// for that race.
func (p *product) newOrganization(ctx context.Context, alias string, account authn.AccountID, profile tenant.Profile) (tenant.Tenant, error) {
	for attempt := 0; attempt <= identifierRetries; attempt++ {
		identifier := p.newIdentifier()
		if !tenant.IsIdentifier(identifier) {
			return tenant.Tenant{}, fmt.Errorf("%w: the generated identifier is outside the roster's domain",
				errIdentifierUnavailable)
		}
		// The identifier is one namespace with the aliases: a generated value
		// equal to the alias would make one slug name the tenant twice.
		if identifier == alias {
			continue
		}
		available, err := p.identifierAvailable(ctx, tenant.ID(identifier))
		if err != nil {
			return tenant.Tenant{}, err
		}
		if available {
			return tenant.Tenant{
				ID:           tenant.ID(identifier),
				Alias:        alias,
				Status:       tenant.StatusActive,
				OwnerAccount: account.String(),
				Profile:      profile,
			}, nil
		}
	}
	return tenant.Tenant{}, errIdentifierUnavailable
}

// identifierAvailable reports whether neither the identifier nor the alias (the
// same string) is held. Both lookups are made because the roster keeps the two
// as separate unique columns: a row whose identifier is another string may hold
// the alias.
func (p *product) identifierAvailable(ctx context.Context, id tenant.ID) (bool, error) {
	if _, found, err := p.roster.Get(ctx, id); err != nil {
		return false, fmt.Errorf("reading the roster for %q: %w", id, err)
	} else if found {
		return false, nil
	}
	if _, found, err := p.roster.ByAlias(ctx, string(id)); err != nil {
		return false, fmt.Errorf("reading the roster's alias %q: %w", id, err)
	} else if found {
		return false, nil
	}
	return true, nil
}

// releaseLock drops the account's creation lock. The TTL is the backstop for a
// process that stops before this runs, so a failure here changes no answer and
// is only logged.
func (p *product) releaseLock(key string) {
	if err := p.kv.Delete(context.Background(), key); err != nil {
		p.logger.Error(ModuleName+": the creation lock could not be released; its TTL will expire it",
			"key", key, "cause", err.Error())
	}
}

// auditRequired writes the mandatory row of a creation. A row that does not land
// is the audit module's state, and the creation is not carried out: the caller
// answers 503, with the cause in the log and never in the answer.
func (p *product) auditRequired(ctx context.Context, entry audit.Entry) error {
	if err := p.audit.Record(ctx, entry); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the organization was not created",
			"action", entry.Action, "tenant", entry.Object, "cause", err.Error())
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	return nil
}

// auditBestEffort writes the failed row that closes a failed attempt. Its own
// failure is a log line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, entry audit.Entry) {
	if err := p.audit.Record(ctx, entry); err != nil {
		p.logger.Error(ModuleName+": a failed-action audit row could not be written",
			"action", entry.Action, "tenant", entry.Object, "cause", err.Error())
	}
}

// newIdentifier mints one candidate identifier: the fixed prefix and a random
// UUID.
func newIdentifier() string {
	return idPrefix + uuid.NewString()
}

// lockKeyOf names the per-account creation lock.
func lockKeyOf(account authn.AccountID) string {
	return ModuleName + ".lock." + account.String()
}

// hourlyKey names the account's count in the hourly window the instant belongs
// to. The window is the calendar hour in UTC, so every replica agrees on it
// without sharing a clock beyond the one the key arithmetic encodes.
func hourlyKey(account authn.AccountID, now time.Time) string {
	return ModuleName + ".hourly." + account.String() + "." +
		strconv.FormatInt(now.UTC().Truncate(time.Hour).Unix(), 10)
}

// nextHour is the instant the window now belongs to closes.
func nextHour(now time.Time) time.Time {
	return now.UTC().Truncate(time.Hour).Add(time.Hour)
}
