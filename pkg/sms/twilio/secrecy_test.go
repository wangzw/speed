package twilio

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestTheCredentialsTheParametersAndTheBodyReachNoErrorText holds that a module
// holding a secret configuration value and a message's content keeps both out of
// the text of every failure it returns, even when the carrier echoes them back in
// its own answer. The auth token authenticates the request and is registered as a
// literal redaction rule for logs; what the seams return must not carry it
// either.
func TestTheCredentialsTheParametersAndTheBodyReachNoErrorText(t *testing.T) {
	const (
		code = "135790"
		body = "the rendered body nobody may log"
	)
	echoed := `{"code":21610,"message":"the code ` + code + ` and the token ` + testAuthToken +
		` and the body ` + body + ` were rejected","status":400}`

	for _, c := range []struct {
		name string
		call func(*sender) error
	}{
		{"send", func(s *sender) error {
			message := oneResolved()
			message.Params = map[string]string{"code": code, "ttl_minutes": "5"}
			message.Ordered = []string{code, "5"}
			message.Body = body
			_, err := s.Send(t.Context(), message)
			return err
		}},
		{"offer", func(s *sender) error {
			sub := oneSubmission()
			sub.Content = body + " ${code}"
			_, err := s.Offer(t.Context(), sub)
			return err
		}},
		{"retire", func(s *sender) error {
			return s.Retire(t.Context(), "HX1")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(func() http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, echoed)
				}
			}())
			defer server.Close()

			s := senderOver(t, server.URL, server.URL)
			err := c.call(s)
			if !errors.Is(err, sms.ErrRejected) {
				t.Fatalf("%s answered %v, want the rejected sentinel", c.name, err)
			}
			for _, secret := range []string{testAuthToken, code, body} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the %s failure %q carries %q", c.name, err, secret)
				}
			}
		})
	}
}

// TestTheRenderedBodyIsNotSentBesideTheContentTemplate holds that a resolved
// message's body never reaches the vendor: this carrier sends the content
// template, and the request carries no Body.
func TestTheRenderedBodyIsNotSentBesideTheContentTemplate(t *testing.T) {
	const body = "a body the vendor must not receive"
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		request = r.PostForm.Encode()
		messageReply(`{"sid":"SM1"}`)(w, r)
	}))
	defer server.Close()

	s := senderOver(t, server.URL, server.URL)
	message := oneResolved()
	message.Body = body
	if _, err := s.Send(t.Context(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if strings.Contains(request, body) || strings.Contains(request, "Body=") {
		t.Errorf("the request carries the rendered body:\n%s", request)
	}
}
