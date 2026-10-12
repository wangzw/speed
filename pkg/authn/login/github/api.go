package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// githubUser is the part of GET /user the method reads: the numeric account
// number, which is the subject, and the two names, one of which is shown.
type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

// githubEmail is one entry of GET /user/emails. Only the address and the two
// markers are read; the address is taken when it is both primary and verified.
type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// providerError is a status the provider answered a read with that is not 200.
// It is the provider refusing the request, not the provider being unreachable,
// and the two are answered apart.
type providerError struct{ status int }

// Error names the status and nothing the provider answered.
func (e *providerError) Error() string {
	return fmt.Sprintf("authn.login.github: the provider answered status %d", e.status)
}

// fetchUser reads the account the access token names.
func (m *method) fetchUser(ctx context.Context, token string) (githubUser, error) {
	var u githubUser
	if err := m.get(ctx, userPath, token, &u); err != nil {
		return githubUser{}, err
	}
	return u, nil
}

// fetchPrimaryVerifiedEmail reads the addresses of the account the access token
// names and returns the one that is both primary and verified. When no entry is
// both, the address is empty and the marker is false, and that is not an error:
// an account without a verified address is a login with no address clue, which
// the resolution handles.
func (m *method) fetchPrimaryVerifiedEmail(ctx context.Context, token string) (string, bool, error) {
	var list []githubEmail
	if err := m.get(ctx, emailsPath, token, &list); err != nil {
		return "", false, err
	}
	for _, e := range list {
		if e.Primary && e.Verified && strings.TrimSpace(e.Email) != "" {
			return strings.TrimSpace(e.Email), true, nil
		}
	}
	return "", false, nil
}

// get reads one endpoint with the access token and decodes its body into dst. A
// status that is not 200 is a providerError; a transport failure, a read
// failure and a body that does not decode are errors of the read itself. The
// token travels in the request header and is written nowhere else.
func (m *method) get(ctx context.Context, path, token string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	req.Header.Set("User-Agent", userAgent)

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &providerError{status: resp.StatusCode}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("authn.login.github: the provider's response cannot be read: %w", err)
	}
	return nil
}
