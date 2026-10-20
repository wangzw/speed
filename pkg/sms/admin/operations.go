package admin

import (
	"net/http"
	"reflect"
	"time"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. Every module's fragment carries its own module
// name and the version of the fragment's shape.
const (
	fragmentTitle   = "sms.admin"
	fragmentVersion = "1.0.0"
)

// The paths the operations are declared at, relative to the API root the Spec
// carries: the fragment is generated from the declarations and its paths stay
// relative, while the routes are mounted by resolving the same declaration
// against that root.
const (
	relTemplates     = "/sms/templates"
	relSubmissions   = "/sms/templates/submissions"
	relTemplate      = "/sms/templates/:name"
	relTemplateState = "/sms/templates/:name/enabled"
)

// ListTemplatesInput is the request of the page read: the cursor and the page
// cap.
type ListTemplatesInput struct {
	After string `query:"after" description:"Continue after the template this name names"`
	Limit int    `query:"limit" minimum:"0" description:"The page's maximum size; the read surface caps it"`
}

// registerBody is the request of a registration: the template's logical name,
// the parameter names it substitutes and the carrier code for each language.
type registerBody struct {
	_      struct{}          `description:"A template to take up by the vendor code the deployment already holds: its name, its parameters and the vendor code of each language."`
	Name   string            `json:"name" required:"true" description:"The logical template name to take up; required and non-empty, and the name a caller will later send under. A language the name already holds is a 409."`
	Params []string          `json:"params" required:"true" description:"The parameter names the template substitutes, in the order the vendor approved them."`
	Codes  map[string]string `json:"codes" required:"true" description:"The vendor's template code per language, keyed by locale. Every language named starts pending, and the template starts enabled."`
}

// offerBody is the request of a submission: the fields of a sms.Submission.
type offerBody struct {
	_       struct{} `description:"A template submission to the vendor for one language: the body with its placeholders, what the template is for, and the note its reviewer reads."`
	Name    string   `json:"name" required:"true" description:"The logical template name the submission creates; required and non-empty."`
	Content string   `json:"content" required:"true" description:"The template body, written with ${name} placeholders naming the entries of params. The carrier rewrites them into the vendor's own syntax, in params order."`
	Kind    string   `json:"kind" required:"true" description:"What the template is for, in the shared vocabulary: verification, notification or marketing. The carrier maps it to the vendor's spelling."`
	Locale  string   `json:"locale" required:"true" description:"The language this submission is for, opaque to the capability. A vendor approves per language, so one submission yields one code for one language."`
	Params  []string `json:"params" required:"true" description:"The parameter names content substitutes, in the order the template expects them."`
	Remark  string   `json:"remark" required:"true" description:"A note for the vendor's reviewer; may be empty."`
}

// enabledBody is the request of an enable or a disable. The target state is a
// pointer so that a body naming none is told apart from one naming false: the
// route changes a template's state, and a body that names no state is a request
// this module refuses rather than a disable by omission.
type enabledBody struct {
	Enabled *bool `json:"enabled" required:"true" description:"True lets messages go out under the template, false stops them. The member is required and must be a JSON boolean; a body without it is a 400."`
}

// templateNameInput is the request of a route addressed by a template name.
type templateNameInput struct {
	Name string `path:"name" description:"The logical template name, the natural key the management routes address a template by."`
}

// setEnabledInput is the request of the enable route: the template named in the
// path and the state to set.
type setEnabledInput struct {
	Name    string `path:"name" description:"The logical template name, the natural key the management routes address a template by."`
	Enabled *bool  `json:"enabled" required:"true" description:"True lets messages go out under the template, false stops them. The member is required and must be a JSON boolean; a body without it is a 400."`
}

// SmsAdminTemplate is one template as the management surface reports it: its
// logical name, its source, its ordered parameter names, the languages it holds
// a code for, the vendor's review per language, whether sends are allowed, and
// its write counters. It carries no vendor code.
type SmsAdminTemplate struct {
	_ struct{} `description:"One template as the management surface reports it: its logical name, its source, its ordered parameter names, the languages it holds a code for, the vendor's review per language, whether sends are allowed, and its write counters. It carries no vendor code."`

	Name      string                      `json:"name" required:"true" description:"The logical template name a caller sends under; the management routes address a template by it."`
	Source    string                      `json:"source" required:"true" description:"Where the template came from: config for one read from the configuration, runtime for one this process created. A config template is read-only at runtime, always enabled, and carries no approvals and no version."`
	Params    []string                    `json:"params" required:"true" description:"The parameter names the template substitutes, in the order the vendor approved them; a positional vendor lays values out in this order."`
	Locales   []string                    `json:"locales" required:"true" description:"The languages the template holds a vendor code for. A code is what a send needs but not the whole of what it needs: a runtime template sends only while it is enabled and the language's vendor review is approved, and every language a runtime template names starts pending review whether the template was registered or submitted, so this list normally includes languages that cannot be sent in yet. A configured template is always enabled and its review is not tracked, so each language it holds a code for can be sent in."`
	Approvals map[string]SmsAdminApproval `json:"approvals" required:"true" description:"The vendor's review verdict per language, keyed by locale; a vendor approves a template per language. A configured template carries an empty object because this process does not track its review."`
	Enabled   bool                        `json:"enabled" required:"true" description:"Whether a message may go out under the template. A configured template is always enabled; a runtime template's value is set by the enable route and is independent of the vendor's review."`
	Version   int64                       `json:"version" required:"true" description:"The number of writes this runtime record has taken, used as the optimistic-concurrency condition; 0 for a configured template, which has no version."`
	CreatedAt time.Time                   `json:"created_at" required:"true" description:"When the record was first written, in UTC and RFC 3339 format; the zero time for a configured template."`
	UpdatedAt time.Time                   `json:"updated_at" required:"true" description:"When the record was last written, in UTC and RFC 3339 format; the zero time for a configured template."`
}

// SmsAdminApproval is the vendor's review verdict on one template in one
// language.
type SmsAdminApproval struct {
	_ struct{} `description:"The vendor's review verdict on one template in one language."`

	State  string `json:"state" required:"true" description:"The verdict in this capability's own vocabulary: pending, approved, rejected or withdrawn; an empty string means a review this process does not track. The vendor's raw status numbers are never exposed."`
	Reason string `json:"reason,omitempty" description:"The vendor's own explanation, present when it turned the template down and it gave one; omitted otherwise."`
}

// The success bodies. Each is an alias of an anonymous struct, so the fragment
// publishes the wrapper inline rather than as a component of its own.
type (
	// templatePageBody is the 200 answer of a list.
	templatePageBody = struct {
		_ struct{} `description:"One page of the template set, ordered by name, with the cursor that continues it."`

		Templates  []SmsAdminTemplate `json:"templates" required:"true" description:"The templates on this page, ordered by name."`
		NextCursor string             `json:"next_cursor" required:"true" description:"The last name on this page, to be passed as after on the next request; empty when this page is the last."`
	}

	// registeredTemplateBody is the 201 answer of a registration.
	registeredTemplateBody = struct {
		_ struct{} `description:"The template the registration created."`

		Template SmsAdminTemplate `json:"template" required:"true" description:"The registered template."`
	}

	// offeredTemplateBody is the 201 answer of a submission.
	offeredTemplateBody = struct {
		_ struct{} `description:"The template the submission created."`

		Template SmsAdminTemplate `json:"template" required:"true" description:"The created template, with its language starting pending review."`
	}
)

// The answers every write route shares: the registry's refusal mapping, closed
// and fail-closed. A 404 is written as the status alone, with no problem
// document, and is therefore an empty reply; the others are problem documents.
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

// writeAnswers is the answer set of a write route: the two decisions' refusals,
// the registry's shared failure mapping and the route's own success answer.
func writeAnswers(success speedhttp.Response) []speedhttp.Response {
	answers := append(append([]speedhttp.Response(nil), writeFailures...), success)
	return speedhttp.Failures([]speedhttp.Response{unauthorizedAnswer, writeForbidden}, answers...)
}

// jsonBodyAnswers is writeAnswers for a route whose input carries a JSON body:
// the entry point binds a body only under the declared media type, so a request
// with another one is answered 415 and the declaration has to describe it.
func jsonBodyAnswers(success speedhttp.Response) []speedhttp.Response {
	return speedhttp.Failures(writeAnswers(success), speedhttp.ProblemResponse(
		http.StatusUnsupportedMediaType, "The request body is not application/json"))
}

// The 401 and 403 answers shared by every route. The route is reached with no
// identity only when the authorization middleware let an anonymous request
// through, and the permission shortfall is the handler-internal guard's own
// refusal.
var (
	unauthorizedAnswer = speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity")
	readForbidden      = speedhttp.ProblemResponse(http.StatusForbidden,
		"The caller holds no sms.template.read in the platform domain")
	writeForbidden = speedhttp.ProblemResponse(http.StatusForbidden,
		"The caller holds no sms.template.manage in the platform domain")
)

// listOperation declares GET /sms/templates: one page of the template set,
// configured templates included. The handler returns the page, so the simple
// form binds the query and writes the declared 200.
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

// registerOperation declares POST /sms/templates: a template whose vendor
// code the operator already has is taken up. Its handler builds the answer
// because a registry sentinel is answered 404 with no problem document, which
// the simple form cannot write.
var registerOperation = speedhttp.NewReplyOperation[registerBody](
	"sms.admin.templates.register",
	http.MethodPost,
	relTemplates,
	"Take up a template whose vendor code the operator already has",
	"Takes up a runtime template whose vendor code the operator already has. Every language named "+
		"starts pending and the template starts enabled: enablement is independent of the vendor's "+
		"review, which the registry reads back per language from then on. A name that is empty or only "+
		"whitespace is a 400 before the registry is called, and a language the name already holds is a "+
		"409. The mandatory audit row is written before the action and outside any transaction, so an "+
		"audit failure answers 503 and takes nothing up.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The template to take up: its logical name, the parameter names it substitutes in order, and the vendor code for each language.",
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

// offerOperation declares POST /sms/templates/submissions: a template is
// handed to the vendor for review and the code it answers with is taken up. Its
// handler builds the answer because a registry sentinel is answered 404 with no
// problem document.
var offerOperation = speedhttp.NewReplyOperation[offerBody](
	"sms.admin.templates.offer",
	http.MethodPost,
	relSubmissions,
	"Hand a template to the vendor for review and take up the code it answers with",
	"Hands a template to the vendor for review through the vendor's own API and takes up the code the "+
		"vendor answers with; the submission's language starts pending. Content is written with "+
		"${name} placeholders naming the entries of params, and a carrier whose vendor takes positional "+
		"placeholders rewrites them in params order. An empty or whitespace-only name is a 400 before "+
		"the vendor is called; a submission the vendor or the registry refuses is a 400, and a language "+
		"the name already holds is a 409. The mandatory audit row is written before the action and "+
		"outside any transaction, so an audit failure answers 503 and submits nothing.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The template to submit: its logical name, the body with ${name} placeholders, what the template is for, its language, the parameter names in order, and a note for the reviewer.",
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

// setEnabledOperation declares POST /sms/templates/:name/enabled: a
// runtime template's ability to send is turned on or off. Its handler builds
// the answer because success is 204 and a missing template is a 404 with no
// problem document.
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

// removeOperation declares DELETE /sms/templates/:name: a runtime template
// is dropped from this process, the vendor left alone. Its handler builds the
// answer because success is 204 and a missing template is a 404 with no problem
// document.
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

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	listOperation,
	registerOperation,
	offerOperation,
	setEnabledOperation,
	removeOperation,
}
