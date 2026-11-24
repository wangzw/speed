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

const identifierRetries = 3

const lockTTL = 30 * time.Second

var errIdentifierUnavailable = errors.New("the organization's name or generated identifier is unavailable")

// handleCreate serves POST /api/v1/orgs. The decisions run in this order:
//
//  1. an identity is required;
//  2. an impersonating identity is refused before anything is read;
//  3. a service account and a static subject are refused;
//  4. the account's email address must not be pending (a read failure is 503);
//  5. the profile and name are normalized and held to their shape, the
//     reserved-name set included;
//  6. the name is checked against the roster's one namespace;
//  7. the per-account creation lock is taken;
//  8. the active-business-organization cap is checked;
//  9. the hourly count is checked;
//  10. a free identifier is generated;
//  11. one attempt is counted;
//  12. the mandatory audit row lands;
//  13. the tenant is provisioned.
//
// A refusal in steps 1-10 writes no count; step 11 consumes one place before the
// audit and the provisioner, so the count limits attempts, not successes.
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

	name := strings.TrimSpace(in.Name)
	profile := tenant.Profile{
		DisplayName: in.DisplayName,
		Description: in.Description,
	}.Normalize()
	if profile.DisplayName == "" || profile.Validate() != nil || !tenant.IsName(name) {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusBadRequest}
	}
	reserved, err := p.reservations.ReservedUsername(ctx, name)
	switch {
	case errors.Is(err, authn.ErrInvalidIdentifier):
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusBadRequest}
	case err != nil:
		p.logger.Error(ModuleName+": the reserved-name set could not be read, so the creation was not carried out",
			"account", account.String(), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	if reserved {
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusBadRequest}
	}

	available, nameErr := p.nameAvailable(ctx, name)
	if nameErr != nil {
		p.logger.Error(ModuleName+": the name could not be checked against the roster",
			"account", account.String(), "cause", nameErr.Error())
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

	record, err := p.newOrganization(ctx, name, account, profile)
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
		Object:  record.Name,
		Outcome: outcomeSuccess,
		Detail: map[string]string{
			detailActionID: actionID,
			detailAccount:  account.String(),
			detailTenant:   record.Name,
		},
	}); err != nil {
		return OrganizationCreatedReply{}, err
	}

	if err := p.provisioner.Provision(ctx, record, account.String()); err != nil {
		if errors.Is(err, tenant.ErrTenantExists) || errors.Is(err, tenant.ErrNameConflict) {
			p.logger.Warn(ModuleName+": the name or generated identifier was taken by another replica",
				"account", account.String(), "tenant", string(record.ID), "cause", err.Error())
			return OrganizationCreatedReply{}, speedhttp.Fault{
				Status: http.StatusConflict,
				Type:   problemPrefix + identifierUnavailable,
			}
		}
		p.auditBestEffort(ctx, audit.Entry{
			Action:  auditAction,
			Object:  record.Name,
			Outcome: outcomeFailed,
			Detail:  map[string]string{detailActionID: actionID},
		})
		p.logger.Error(ModuleName+": the organization could not be provisioned",
			"account", account.String(), "tenant", string(record.ID), "cause", err.Error())
		return OrganizationCreatedReply{}, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}

	return OrganizationCreatedReply{
		Tenant: OrganizationCreatedTenant{
			Name:        record.Name,
			DisplayName: record.Profile.DisplayName,
			Description: record.Profile.Description,
			Status:      record.Status.String(),
		},
	}, nil
}

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

func (p *product) recordAttempt(ctx context.Context, account authn.AccountID) error {
	now := p.now()
	ttl := nextHour(now).Sub(now) + time.Minute
	_, err := p.kv.IncrByFloatWithTTL(ctx, hourlyKey(account, now), 1, ttl)
	return err
}

func (p *product) retryAfter() int {
	now := p.now()
	seconds := int(math.Ceil(nextHour(now).Sub(now).Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (p *product) nameAvailable(ctx context.Context, name string) (bool, error) {
	if _, found, err := p.roster.ByName(ctx, name); err != nil {
		return false, fmt.Errorf("reading the roster's name %q: %w", name, err)
	} else if found {
		return false, nil
	}
	if _, found, err := p.roster.Get(ctx, tenant.ID(name)); err != nil {
		return false, fmt.Errorf("reading the roster for %q: %w", name, err)
	} else if found {
		return false, nil
	}
	return true, nil
}

func (p *product) newOrganization(ctx context.Context, name string, account authn.AccountID, profile tenant.Profile) (tenant.Tenant, error) {
	for attempt := 0; attempt <= identifierRetries; attempt++ {
		identifier := p.newIdentifier()
		if !tenant.IsIdentifier(identifier) {
			return tenant.Tenant{}, fmt.Errorf("%w: the generated identifier is outside the roster's domain",
				errIdentifierUnavailable)
		}
		if identifier == name {
			continue
		}
		available, err := p.identifierAvailable(ctx, tenant.ID(identifier))
		if err != nil {
			return tenant.Tenant{}, err
		}
		if available {
			return tenant.Tenant{
				ID:           tenant.ID(identifier),
				Name:         name,
				Status:       tenant.StatusActive,
				OwnerAccount: account.String(),
				Profile:      profile,
			}, nil
		}
	}
	return tenant.Tenant{}, errIdentifierUnavailable
}

func (p *product) identifierAvailable(ctx context.Context, id tenant.ID) (bool, error) {
	if _, found, err := p.roster.Get(ctx, id); err != nil {
		return false, fmt.Errorf("reading the roster for %q: %w", id, err)
	} else if found {
		return false, nil
	}
	if _, found, err := p.roster.ByName(ctx, string(id)); err != nil {
		return false, fmt.Errorf("reading the roster's name %q: %w", id, err)
	} else if found {
		return false, nil
	}
	return true, nil
}

func (p *product) releaseLock(key string) {
	if err := p.kv.Delete(context.Background(), key); err != nil {
		p.logger.Error(ModuleName+": the creation lock could not be released; its TTL will expire it",
			"key", key, "cause", err.Error())
	}
}

func (p *product) auditRequired(ctx context.Context, entry audit.Entry) error {
	if err := p.audit.Record(ctx, entry); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the organization was not created",
			"action", entry.Action, "tenant", entry.Object, "cause", err.Error())
		return speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	return nil
}

func (p *product) auditBestEffort(ctx context.Context, entry audit.Entry) {
	if err := p.audit.Record(ctx, entry); err != nil {
		p.logger.Error(ModuleName+": a failed-action audit row could not be written",
			"action", entry.Action, "tenant", entry.Object, "cause", err.Error())
	}
}

func newIdentifier() string {
	return tenant.NewOrganizationID()
}

func lockKeyOf(account authn.AccountID) string {
	return ModuleName + ".lock." + account.String()
}

func hourlyKey(account authn.AccountID, now time.Time) string {
	return ModuleName + ".hourly." + account.String() + "." +
		strconv.FormatInt(now.UTC().Truncate(time.Hour).Unix(), 10)
}

func nextHour(now time.Time) time.Time {
	return now.UTC().Truncate(time.Hour).Add(time.Hour)
}
