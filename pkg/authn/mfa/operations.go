package mfa

import (
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "authn mfa"
	fragmentVersion = "1.0.0"
)

const (
	factorsPath = "/auth/factors"
	factorPath  = "/auth/factors/:id"
)

// AuthnMfaFactor is one factor row of the caller's account: what the register
// holds about the factor, with nothing a kind keeps for itself.
type AuthnMfaFactor struct {
	_ struct{} `description:"One factor row of the caller's account: what the register holds about the factor, with nothing a kind keeps for itself."`

	// ID is the factor's identifier.
	ID string "json:\"id\" required:\"true\" description:\"The factor's identifier; use it as the `id` path parameter of `DELETE /auth/factors/{id}` or `PATCH /auth/factors/{id}`.\""
	// Kind is the factor kind.
	Kind string "json:\"kind\" required:\"true\" description:\"The factor kind: `totp`, `sms` or `webauthn`.\""
	// Label is the display label stored with the factor.
	Label string `json:"label" required:"true" description:"The display label stored with the factor: what the caller submitted at registration or at a later rename, with surrounding whitespace trimmed. Empty means the factor has no name; the module generates none."`
	// Status is the lifecycle status of the row.
	Status string "json:\"status\" required:\"true\" enum:\"pending,active,disabled\" description:\"Lifecycle status: `pending` is registered but not yet confirmed and is never used to verify, `active` may verify, and `disabled` (left by an administrator reset or a clone warning) stays as a row that no longer counts.\""
	// CreatedAt is when the row was created.
	CreatedAt time.Time `json:"created_at" required:"true" description:"When the factor row was created, as an RFC 3339 instant"`
	// ActivatedAt is when the factor became active, absent until it is.
	ActivatedAt *time.Time `json:"activated_at,omitempty" description:"When the factor became active, as an RFC 3339 instant; absent until it is"`
	// LastUsedAt is when the factor last passed a verification, absent until it first does.
	LastUsedAt *time.Time `json:"last_used_at,omitempty" description:"When the factor last passed a verification, as an RFC 3339 instant; absent until it first does"`
}

// AuthnMfaFactorList is the object the listing answers: the caller's factor
// rows, and nothing a kind keeps for itself.
type AuthnMfaFactorList struct {
	_ struct{} `description:"An object whose single member lists every factor the caller's account has registered."`

	// Factors lists the caller's factor rows, oldest first.
	Factors []AuthnMfaFactor `json:"factors" required:"true" description:"The caller's factor rows, ordered oldest first by creation time and then by identifier; an empty array when the account has registered none."`
}

type removeFactorInput struct {
	ID string "path:\"id\" description:\"The factor identifier, as returned in `id` by `GET /auth/factors`. Empty or longer than 64 bytes is refused as a bad request without reading the register.\""
}

type updateFactorInput struct {
	_ struct{} `description:"The new display label of one factor the caller's account registered."`

	ID    string "path:\"id\" description:\"The factor identifier, as returned in `id` by `GET /auth/factors`. Empty or longer than 64 bytes is refused as a bad request without reading the register.\""
	Label string "json:\"label\" required:\"true\" description:\"The factor's new display label: at most 64 characters with no control character. Surrounding whitespace is trimmed before it is stored, and an empty value clears the name; the module generates none.\""
}

var plainChallengeHeader = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The capability's challenge: the Bearer and Cookie schemes, never Basic.",
}

var removeChallengeHeader = speedhttp.Header{
	Name: "WWW-Authenticate",
	Description: "The capability's challenge. An unusable credential is challenged with the " +
		"Bearer and Cookie schemes; a fresher authentication is challenged with the scheme the " +
		"identity authenticated with, the level it must reach and, when one applies, a maximum age.",
}

func unauthorized(header speedhttp.Header, description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{header}
	return response
}

var factorsOperation = speedhttp.NewOperation[struct{}, AuthnMfaFactorList](
	"authn.mfa.factors",
	http.MethodGet,
	factorsPath,
	"List the caller's own registered factors",
	"Answers every factor row the caller's account has registered, ordered oldest first by creation "+
		"time and then by identifier. Each row carries the kind, label, lifecycle status and the "+
		"instants it was created, activated and last used; it carries no seed, key, public key or "+
		"other data a kind keeps for itself. The list is empty when the account has registered none.",
	nil,
	speedhttp.Failures(nil,
		unauthorized(plainChallengeHeader, "No usable credential"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The factors could not be listed: the factor store was unavailable. The cause is logged and not sent"),
		speedhttp.Response{
			Status:      http.StatusOK,
			Description: "The caller's factors, without any seed, key or other secret of a kind's own",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
	),
)

var removeFactorOperation = speedhttp.NewReplyOperation[removeFactorInput](
	"authn.mfa.removeFactor",
	http.MethodDelete,
	factorPath,
	"Remove one of the caller's own factors",
	"Removes one factor of the caller's account: whatever the factor's kind keeps for it is removed, "+
		"then its register row. After an active factor has been removed, the account's other sessions "+
		"are ended first and the session that made the request is then rotated: the 204 carries that "+
		"session's new cookie, and the token the request was made with no longer authenticates. A "+
		"pending or disabled factor ends no session and rotates none, and a session store that fails "+
		"after the factor is gone is reported as an outage. "+
		"The call requires a fresh multi-factor authentication verified "+
		"locally, so one merely declared by an identity provider does not satisfy it; the requirement is "+
		"the effective account's and the proof is the caller's own, so an impersonating identity removes "+
		"the impersonated account's factor. A factor that is not the caller's answers the same as one "+
		"already gone, so the route never answers 404 and repeating a removal is idempotent: it finishes "+
		"any session revocation and rotation an earlier attempt could not. When the account has "+
		"mfa_required set, its last active factor cannot be removed.",
	nil,
	speedhttp.Failures(nil,
		speedhttp.ProblemResponse(http.StatusBadRequest, "The factor identifier is malformed"),
		unauthorized(removeChallengeHeader,
			"No usable credential, or a fresh multi-factor authentication is required"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The factor is the account's last active factor, which cannot be removed while the account "+
				"requires multi-factor authentication"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The removal could not be completed: the factor store, the account data it needs or the "+
				"session store was unavailable. The cause is logged and not sent. When the factor itself "+
				"was already removed, repeating the request finishes ending the other sessions and "+
				"rotating the requesting session"),
		speedhttp.Response{
			Status: http.StatusNoContent,
			Description: "The factor was removed, or is not the caller's and reads as already gone; " +
				"repeating a removal is idempotent. When an active factor was removed, the answer also " +
				"sets the requesting session's new cookie, and the token the request was made with no " +
				"longer authenticates",
		},
	),
)

var updateFactorOperation = speedhttp.NewOperation[updateFactorInput, AuthnMfaFactor](
	"authn.mfa.updateFactor",
	http.MethodPatch,
	factorPath,
	"Rename one of the caller's own factors",
	"Writes the display label of one factor of the caller's account and answers the updated row, "+
		"as `GET /auth/factors` shows it. The label is metadata, so the call asks for no freshness "+
		"beyond the login session itself, and an impersonating identity renames the impersonated "+
		"account's factor. The value is trimmed of surrounding whitespace and must be at most 64 "+
		"characters with no control character; an empty value clears the name, and the module "+
		"generates none. A factor that is not the caller's answers 404 with the problem type "+
		"urn:speed:authn:factor-not-found, the same as one that is not there, so another account's "+
		"identifier says nothing about whether it exists.",
	&speedhttp.RequestBody{
		MediaType: "application/json",
		Description: "The factor's new label. The member is required and an empty value clears the " +
			"name; surrounding whitespace is trimmed, and at most 64 characters with no control " +
			"character is stored. An unknown member is refused.",
		Required: true,
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The factor row as it reads after the rename",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponseOf(http.StatusBadRequest,
			"The factor identifier is malformed, or the label is outside its domain",
			typePrefix+mount.CodeBadRequest),
		unauthorized(plainChallengeHeader, "No usable credential"),
		speedhttp.ProblemResponseOf(http.StatusNotFound,
			"No factor of the caller's account has this identifier: it is not there, or it belongs "+
				"to another account. Both cases answer the problem type urn:speed:authn:factor-not-found",
			typePrefix+mount.CodeFactorNotFound),
		speedhttp.ProblemResponseOf(http.StatusUnsupportedMediaType, "The request body is not application/json",
			typePrefix+mount.CodeUnsupportedMediaType),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The factor could not be renamed: the factor store was unavailable. The cause is logged and not sent"),
	},
)

var operations = speedhttp.Operations{
	factorsOperation,
	removeFactorOperation,
	updateFactorOperation,
}
