package admin

import (
	"net/http"
	"reflect"
	"time"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "sms.admin"
	fragmentVersion = "1.0.0"
)

const (
	relTemplates     = "/sms/templates"
	relSubmissions   = "/sms/templates/submissions"
	relTemplate      = "/sms/templates/:name"
	relTemplateState = "/sms/templates/:name/enabled"
	relCarrier       = "/sms/carrier"
)

// ListTemplatesInput is the request of the page read: the cursor and the page cap.
type ListTemplatesInput struct {
	// After is the template name to continue after; empty starts at the beginning.
	After string `query:"after" description:"Continue after the template this name names"`
	// Limit is the page's maximum size; zero takes the default.
	Limit int `query:"limit" minimum:"0" description:"The page's maximum size; the read surface caps it"`
}

type registerBody struct {
	_          struct{}          `description:"A template to take up by the vendor code the deployment already holds: its name, what it is for, its parameters, the vendor code and the body of each language, and its registration identifiers."`
	Name       string            `json:"name" required:"true" description:"The logical template name to take up; required and non-empty, and the name a caller will later send under. A language the name already holds is a 409."`
	Kind       string            `json:"kind" description:"What the template is for: verification, notification or marketing. Optional; an unset value means notification."`
	Params     []string          `json:"params" description:"The parameter names the template substitutes, in the order the vendor approved them. May be empty."`
	Codes      map[string]string `json:"codes" description:"The vendor's template code per language, keyed by locale. Optional; every language named needs a non-empty code, and each language needs a code, a body, or both."`
	Bodies     map[string]string `json:"bodies" description:"The locally rendered body per language, keyed by locale. Optional; every language named needs a non-empty body whose ${name} placeholders all name declared parameters, and each language needs a code, a body, or both."`
	Attributes map[string]string `json:"attributes" description:"The vendor registration identifiers the template carries, keyed by the keys the mounted carrier accepts. Optional."`
}

type offerBody struct {
	_          struct{}          `description:"A template submission to the vendor for one language: the body with its placeholders, what the template is for, and the note its reviewer reads."`
	Name       string            `json:"name" required:"true" description:"The logical template name the submission creates; required and non-empty."`
	Content    string            `json:"content" required:"true" description:"The template body, written with ${name} placeholders naming the entries of params. The carrier rewrites them into the vendor's own syntax, in params order."`
	Kind       string            `json:"kind" required:"true" description:"What the template is for, in the shared vocabulary: verification, notification or marketing. The carrier maps it to the vendor's spelling."`
	Locale     string            `json:"locale" required:"true" description:"The language this submission is for, opaque to the capability. A vendor approves per language, so one submission yields one code for one language."`
	Params     []string          `json:"params" description:"The parameter names content substitutes, in the order the template expects them. May be empty."`
	Attributes map[string]string `json:"attributes" description:"The vendor registration identifiers the template carries, keyed by the keys the mounted carrier accepts. Optional."`
	Remark     string            `json:"remark" required:"true" description:"A note for the vendor's reviewer; may be empty."`
}

type enabledBody struct {
	Enabled *bool `json:"enabled" required:"true" description:"True lets messages go out under the template, false stops them. The member is required and must be a JSON boolean; a body without it is a 400."`
}

type templateNameInput struct {
	Name string `path:"name" description:"The logical template name, the natural key the management routes address a template by."`
}

type setEnabledInput struct {
	Name    string `path:"name" description:"The logical template name, the natural key the management routes address a template by."`
	Enabled *bool  `json:"enabled" required:"true" description:"True lets messages go out under the template, false stops them. The member is required and must be a JSON boolean; a body without it is a 400."`
}

// SmsAdminTemplate is one template as the management surface reports it. It
// carries no vendor code.
type SmsAdminTemplate struct {
	_ struct{} `description:"One template as the management surface reports it: its logical name, its carrier, what it is for, its source, its ordered parameter names, the languages it holds a code or a body for, its bodies and registration identifiers, the vendor's review per language, whether sends are allowed, and its write counters. It carries no vendor code."`

	// Name is the logical template name.
	Name string `json:"name" required:"true" description:"The logical template name a caller sends under; the management routes address a template by it."`
	// Carrier is the carrier the template belongs to.
	Carrier string `json:"carrier" required:"true" description:"The carrier this template belongs to. A database serves one carrier, and every record in it names that one."`
	// Kind is what the template is for.
	Kind string `json:"kind" required:"true" description:"What the template is for: verification, notification or marketing. A template that named none when it was declared is a notification."`
	// Source is where the template came from.
	Source string `json:"source" required:"true" description:"Where the template came from: config for one read from the configuration, runtime for one this process created. A config template is read-only at runtime, always enabled, and carries no approvals and no version."`
	// Params are the parameter names in declared order.
	Params []string `json:"params" required:"true" description:"The parameter names the template substitutes, in the order the vendor approved them; a positional vendor lays values out in this order. May be empty."`
	// Locales is the effective language set.
	Locales []string `json:"locales" required:"true" description:"The effective language set: the languages the template holds a vendor code for, a body for, or both. Whether a language can be sent in follows the mounted carrier's declared review: a carrier that reviews each template sends only while the vendor's review for that language is approved, while a carrier with no per-template review holds every language it has a code or a body for as sendable. A configured template's review is not tracked, so each language it holds can be sent in."`
	// Bodies is the rendered body per language.
	Bodies map[string]string `json:"bodies" required:"true" description:"The locally rendered bodies, keyed by locale. A template that sends only by vendor code carries an empty object."`
	// Attributes are the vendor registration identifiers.
	Attributes map[string]string `json:"attributes" required:"true" description:"The vendor registration identifiers the template carries, keyed by the keys the mounted carrier accepts. Empty when it carries none."`
	// Approvals is the vendor's verdict per language.
	Approvals map[string]SmsAdminApproval `json:"approvals" required:"true" description:"The vendor's review verdict per language, keyed by locale. A carrier that reviews each template approves per language; one with no such review answers not-required per language. A configured template carries an empty object because this process does not track its review."`
	// Enabled reports whether sends are allowed.
	Enabled bool `json:"enabled" required:"true" description:"Whether a message may go out under the template. A configured template is always enabled; a runtime template's value is set by the enable route and is independent of the vendor's review."`
	// Version counts the writes the record has taken.
	Version int64 `json:"version" required:"true" description:"The number of writes this runtime record has taken, used as the optimistic-concurrency condition; 0 for a configured template, which has no version."`
	// CreatedAt is when the record was first written.
	CreatedAt *time.Time `json:"created_at" nullable:"true" required:"true" description:"When the record was first written, as an RFC 3339 instant in UTC; null for a configured template, which carries no write instant."`
	// UpdatedAt is when the record was last written.
	UpdatedAt *time.Time `json:"updated_at" nullable:"true" required:"true" description:"When the record was last written, as an RFC 3339 instant in UTC; null for a configured template, which carries no write instant."`
}

// SmsAdminApproval is the vendor's review verdict on one template in one
// language.
type SmsAdminApproval struct {
	_ struct{} `description:"The vendor's review verdict on one template in one language."`

	// State is the review verdict.
	State string `json:"state" required:"true" description:"The verdict in this capability's own vocabulary: pending, approved, rejected, withdrawn or not-required; an empty string means a review this process does not track. Not-required means the mounted carrier has no per-template review, which is not the same as a review this process does not follow. The vendor's raw status numbers are never exposed."`
	// Reason is the vendor's explanation, if it gave one.
	Reason string `json:"reason,omitempty" description:"The vendor's own explanation, present when it turned the template down and it gave one; omitted otherwise."`
}

// SmsAdminCarrier is the mounted carrier's declaration of what it can carry:
// its name, content and review support, identities, countries, attribute keys,
// body bound and receipt mode.
type SmsAdminCarrier struct {
	_ struct{} `description:"The mounted carrier's declaration of what it can carry."`

	// Name is the carrier name.
	Name string `json:"name" required:"true" description:"The carrier this process mounted; one process sends through exactly one carrier."`
	// Content is the template shapes it sends.
	Content SmsAdminContentSupport `json:"content" required:"true" description:"Which template shape the carrier sends under: the vendor's template code, the body the declaration rendered, or both."`
	// Review is how the vendor reviews and whether a send may pick an identity.
	Review SmsAdminReviewSupport `json:"review" required:"true" description:"How the vendor reviews a template, and whether a send may pick its identity."`
	// Identities are the sending identities this deployment has.
	Identities []SmsAdminIdentity `json:"identities" required:"true" description:"The sending identities this deployment has, each with the countries it can serve. Empty for a carrier that sends from one fixed account."`
	// Countries are the countries it reaches.
	Countries []string `json:"countries" required:"true" description:"The countries this carrier can reach. Empty means it declares no restriction of its own."`
	// AttributeKeys are the accepted template attribute keys.
	AttributeKeys []string `json:"attribute_keys" required:"true" description:"The template attribute keys this carrier accepts: the vendor registration identifiers a template may carry. An attribute under any other key is refused at declaration, load or write."`
	// MaxBodyRunes is the body bound; zero means no bound of its own.
	MaxBodyRunes int `json:"max_body_runes" required:"true" description:"The bound on a rendered body, in runes. Zero means the capability sets no bound of its own."`
	// AsyncReceipt reports that the final delivery state arrives asynchronously.
	AsyncReceipt bool `json:"async_receipt" required:"true" description:"Whether the vendor's final delivery state arrives asynchronously: a synchronous answer carries only the vendor's message identifier, and acceptance is not delivery."`
}

// SmsAdminContentSupport is which template shape a carrier sends under.
type SmsAdminContentSupport struct {
	_ struct{} `description:"Which template shape a carrier sends under."`

	// TemplateCode reports sending by vendor code.
	TemplateCode bool `json:"template_code" required:"true" description:"Whether the carrier sends by the vendor's template code."`
	// TextBody reports sending the rendered body.
	TextBody bool `json:"text_body" required:"true" description:"Whether the carrier sends the body the declaration rendered."`
}

// SmsAdminReviewSupport is how a vendor reviews a template and whether a send
// may pick its identity.
type SmsAdminReviewSupport struct {
	_ struct{} `description:"How a vendor reviews a template and whether a send may pick its identity."`

	// PerTemplate reports a per-template vendor review.
	PerTemplate bool `json:"per_template" required:"true" description:"Whether the vendor reviews each template, so a template is only sendable once the review has come back approved. False means there is no such review and every language starts not-required."`
	// Originator reports that a send may pick its identity.
	Originator bool `json:"originator" required:"true" description:"Whether a send may name the identity it goes out from. False means the carrier has one fixed sending identity."`
}

// SmsAdminIdentity is one sending identity this deployment has.
type SmsAdminIdentity struct {
	_ struct{} `description:"One sending identity this deployment has, and the countries it can serve."`

	// Name is the identity name a message names.
	Name string `json:"name" required:"true" description:"What a message names the identity by."`
	// Kind is the identity kind.
	Kind string `json:"kind" required:"true" description:"What sort of identity it is: sign-name, sender-id, long-code, short-code, toll-free, pool or messaging-service."`
	// Value is the vendor value.
	Value string `json:"value" required:"true" description:"The vendor's own value for it: a signature, a number, a service identifier."`
	// Countries are the countries it serves.
	Countries []string `json:"countries" required:"true" description:"The countries it can serve. Empty means it serves every country the carrier reaches."`
	// Attributes are vendor values beside the identity.
	Attributes map[string]string `json:"attributes" required:"true" description:"Vendor-specific values the identity needs, such as a sender address that lives beside a messaging service. Empty when it needs none."`
}

type (
	templatePageBody = struct {
		_ struct{} `description:"One page of the template set, ordered by name, with the cursor that continues it."`

		Templates  []SmsAdminTemplate `json:"templates" required:"true" description:"The templates on this page, ordered by name."`
		NextCursor string             `json:"next_cursor" required:"true" description:"The last name on this page, to be passed as after on the next request; empty when this page is the last."`
	}

	registeredTemplateBody = struct {
		_ struct{} `description:"The template the registration created."`

		Template SmsAdminTemplate `json:"template" required:"true" description:"The registered template."`
	}

	offeredTemplateBody = struct {
		_ struct{} `description:"The template the submission created."`

		Template SmsAdminTemplate `json:"template" required:"true" description:"The created template, with its language in the review state the mounted carrier declares."`
	}
)

var writeFailures = []speedhttp.Response{
	speedhttp.ProblemResponse(http.StatusBadRequest,
		"The name is empty, the body is not one object, or the registry refused the request as a configuration or a message it cannot run on"),
	{
		Status:      http.StatusNotFound,
		Description: "The registry holds no template by the name; the answer is the status alone, with no problem document",
	},
	speedhttp.ProblemResponse(http.StatusConflict,
		"The registry already holds the name, or the template came from the configuration and is immutable at runtime"),
	speedhttp.ProblemResponse(http.StatusServiceUnavailable,
		"A dependency could not answer"),
}

func writeAnswers(success speedhttp.Response) []speedhttp.Response {
	answers := append(append([]speedhttp.Response(nil), writeFailures...), success)
	return speedhttp.Failures([]speedhttp.Response{unauthorizedAnswer, writeForbidden}, answers...)
}

func jsonBodyAnswers(success speedhttp.Response) []speedhttp.Response {
	return speedhttp.Failures(writeAnswers(success), speedhttp.ProblemResponse(
		http.StatusUnsupportedMediaType, "The request body is not application/json"))
}

var (
	unauthorizedAnswer = speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity")
	readForbidden      = speedhttp.ProblemResponse(http.StatusForbidden,
		"The caller holds no sms.template.read in the platform domain")
	writeForbidden = speedhttp.ProblemResponse(http.StatusForbidden,
		"The caller holds no sms.template.manage in the platform domain")
)

var listOperation = speedhttp.NewOperation[ListTemplatesInput, templatePageBody](
	"sms.admin.templates.list",
	http.MethodGet,
	relTemplates,
	"Page through the template set, configured templates included",
	"Returns one page of every template the registry holds, configured ones included, ordered by "+
		"template name. A page holds 50 entries when the request names no limit; a limit of 0 reads the "+
		"same way, and a limit above 200 is capped at 200. The decision is made in the platform domain "+
		"on sms.template.read, so a grant in a tenant does not reach this set.",
	nil,
	speedhttp.Failures(
		[]speedhttp.Response{unauthorizedAnswer, readForbidden},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The limit is not a non-negative integer"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{
			Status:      http.StatusOK,
			Description: "One page of templates, ordered by name, and the cursor of the next, empty on the last page",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
	),
)

var registerOperation = speedhttp.NewReplyOperation[registerBody](
	"sms.admin.templates.register",
	http.MethodPost,
	relTemplates,
	"Take up a template whose vendor code the operator already has",
	"Takes up a runtime template whose vendor code the operator already has. The body carries the name, an "+
		"optional kind that defaults to notification, the parameter names (which may be empty), a vendor code "+
		"and a locally rendered body per language, and the registration identifiers the carrier accepts. Every "+
		"language named needs a non-empty code, a non-empty body, or both, and every ${name} placeholder in a "+
		"body must name a declared parameter; a shape that does not fit the mounted carrier is a 400. Each "+
		"language starts in the review state the carrier declares: pending where the vendor reviews each "+
		"template, not-required where it has no such review. The template starts enabled, independently of "+
		"the review. A name that is empty or only whitespace is a 400 before the registry is called, and a "+
		"language the name already holds is a 409. The mandatory audit row is written before the action and "+
		"outside any transaction, so an audit failure answers 503 and takes nothing up.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The template to take up: its logical name, what it is for, the parameter names it substitutes in order, the vendor code and the body of each language, and its registration identifiers.",
		Required:    true,
		Name:        "SmsAdminRegister",
	},
	jsonBodyAnswers(speedhttp.Response{
		Status:      http.StatusCreated,
		Description: "The registered template",
		Body: &speedhttp.Body{
			MediaType: "application/json",
			Structure: reflect.TypeOf(registeredTemplateBody{}),
		},
	}),
)

var offerOperation = speedhttp.NewReplyOperation[offerBody](
	"sms.admin.templates.offer",
	http.MethodPost,
	relSubmissions,
	"Hand a template to the vendor for review and take up the code it answers with",
	"Hands a template to the vendor for review through the vendor's own API and takes up the code the "+
		"vendor answers with. A carrier whose declaration has no per-template review records the body "+
		"locally, calls no vendor, and files the template under the code it mints. The submission's "+
		"language starts in the review state the carrier declares: pending where the vendor reviews each "+
		"template, not-required where it has no such review. Content is written with ${name} placeholders "+
		"naming the entries of params, and a carrier whose vendor takes positional placeholders rewrites "+
		"them in params order. An empty or whitespace-only name is a 400 before the vendor is called; a "+
		"submission the vendor or the registry refuses is a 400, and a language the name already holds is "+
		"a 409. The mandatory audit row is written before the action and outside any transaction, so an "+
		"audit failure answers 503 and submits nothing.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The template to submit: its logical name, the body with ${name} placeholders, what the template is for, its language, the parameter names in order, its registration identifiers, and a note for the reviewer.",
		Required:    true,
		Name:        "SmsAdminSubmission",
	},
	jsonBodyAnswers(speedhttp.Response{
		Status:      http.StatusCreated,
		Description: "The template the submission created",
		Body: &speedhttp.Body{
			MediaType: "application/json",
			Structure: reflect.TypeOf(offeredTemplateBody{}),
		},
	}),
)

var setEnabledOperation = speedhttp.NewReplyOperation[setEnabledInput](
	"sms.admin.templates.setEnabled",
	http.MethodPost,
	relTemplateState,
	"Turn a runtime template's ability to send on or off",
	"Turns a runtime template's ability to send on or off. Enablement is orthogonal to the vendor's "+
		"review: a template may be enabled while its review is still pending, and a send still waits for "+
		"the review. The body must carry enabled; a body without it is a 400 rather than a disable by "+
		"omission. A configured template is a 409 whose problem type is urn:speed:sms:config-template, "+
		"and an unknown name is a 404. The mandatory audit row is written before the action and outside "+
		"any transaction, so an audit failure answers 503 and changes nothing. Success is 204 with no "+
		"body.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The state to set on the template.",
		Required:    true,
	},
	jsonBodyAnswers(speedhttp.Response{
		Status:      http.StatusNoContent,
		Description: "The state was changed",
	}),
)

var removeOperation = speedhttp.NewReplyOperation[templateNameInput](
	"sms.admin.templates.remove",
	http.MethodDelete,
	relTemplate,
	"Drop a runtime template from this process, the vendor left alone",
	"Drops a runtime template from this process; the vendor's copy is left alone. A template that "+
		"came from the configuration cannot be removed at runtime: naming one is a 409 whose problem "+
		"type is urn:speed:sms:config-template, while a name the registry does not hold is a 404 with no "+
		"problem body. The mandatory audit row is written before the action and outside any transaction, "+
		"so an audit failure answers 503 and removes nothing. Success is 204 with no body.",
	nil,
	writeAnswers(speedhttp.Response{
		Status:      http.StatusNoContent,
		Description: "The template was removed",
	}),
)

var carrierOperation = speedhttp.NewOperation[struct{}, SmsAdminCarrier](
	"sms.admin.carrier.read",
	http.MethodGet,
	relCarrier,
	"Read the mounted carrier's declaration",
	"Reads what the mounted carrier can carry: its content support, its review support, the sending "+
		"identities this deployment has with their kinds, values, countries and attributes, the countries "+
		"it reaches, the template attribute keys it accepts, its body bound and whether its receipts are "+
		"asynchronous. One process mounts exactly one carrier, so the answer is that carrier's model and "+
		"the model is fixed for the life of the process. The decision is made in the platform domain on "+
		"sms.template.read. Reads write no audit row.",
	nil,
	speedhttp.Failures(
		[]speedhttp.Response{unauthorizedAnswer, readForbidden},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
		speedhttp.Response{
			Status:      http.StatusOK,
			Description: "The mounted carrier's declaration",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
	),
)

var operations = speedhttp.Operations{
	listOperation,
	registerOperation,
	offerOperation,
	setEnabledOperation,
	removeOperation,
	carrierOperation,
}
