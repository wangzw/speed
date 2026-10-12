package model

import (
	"maps"
	"net/url"
	"slices"
)

// The purposes a login flow may be started for. The set is closed. Login makes
// a session; the others run on an identity that is already logged in, which the
// flow's account and session record.
const (
	PurposeLogin  = "login"
	PurposeLink   = "link"
	PurposeStepUp = "step-up"
	PurposeEnroll = "enroll"
)

// The actions a client's answer to a flow step may carry. The set is closed:
// Submit is fields the client filled in, Callback is an external party's
// redirect or POST back.
const (
	InputSubmit   = "submit"
	InputCallback = "callback"
)

// Start is what the engine hands a login method to begin a flow.
type Start struct {
	Purpose   string            // one of the Purpose constants
	Account   AccountID         // known for link and step-up
	Fields    map[string]string // the start request's input: at most 8, each at most 512 characters
	Client    ClientInfo        // for logs and audit; no ground for a judgement or a binding
	SendsLeft int               // how many SMS sends the flow may still make
	Locale    string            // zh-CN or en-US, the engine's mapping of the request's Accept-Language; empty reads zh-CN
}

// Clone returns a copy that shares no map with s.
func (s Start) Clone() Start {
	s.Fields = maps.Clone(s.Fields)
	return s
}

// FlowState is the view the engine hands a method of a flow, which the method
// may not change, with the method's own bytes.
type FlowState struct {
	ID        string // the flow's public identifier, for log correlation only; no secret
	Purpose   string
	Step      string // the method's own name for the current step
	Provider  string // the provider the callback path must name: a social method's own name, an enterprise provider's identifier, or empty
	Account   AccountID
	Session   SessionID // an authenticated flow: the login session that started it
	Private   []byte    // the method's own, opaque, at most 8 KiB, kept by the engine as it stands
	SendsLeft int       // how many SMS sends the flow may still make
	Locale    string    // as Start.Locale, kept in the flow record from the request that started it
}

// Clone returns a copy that shares no slice with f.
func (f FlowState) Clone() FlowState {
	f.Private = slices.Clone(f.Private)
	return f
}

// StepInput is a client's answer to the previous step, or an external party's
// redirect back.
type StepInput struct {
	Action string            // InputSubmit or InputCallback
	Fields map[string]string // the submitted fields
	Query  url.Values        // an external redirect back only
	Form   url.Values        // an external redirect back only: SAML's POST binding
	Client ClientInfo
}

// Clone returns a copy that shares no map or slice with in.
func (in StepInput) Clone() StepInput {
	in.Fields = maps.Clone(in.Fields)
	in.Query = cloneValues(in.Query)
	in.Form = cloneValues(in.Form)
	return in
}

// cloneValues copies a url.Values down to its slices. The nil map stays nil.
func cloneValues(v url.Values) url.Values {
	if v == nil {
		return nil
	}
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = slices.Clone(vs)
	}
	return out
}
