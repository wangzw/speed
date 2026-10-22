package sms

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestTheTemplatesWorkOnABodyOnlyCarrier holds the template contract across
// carrier shapes: the three templates this module sends under are satisfied by a
// declaration that carries a body per language and no vendor code, the startup
// check accepts it, and a send resolves the body the declaration rendered with
// the module's parameters filled in.
//
// The text is the configuration's, not this module's: the case builds the bodies
// it declares, so nothing in the package carries a carrier-specific copy.
func TestTheTemplatesWorkOnABodyOnlyCarrier(t *testing.T) {
	specs := make(map[string]sms.TemplateSpec, len(requiredTemplates))
	for _, name := range requiredTemplates {
		bodies := make(map[string]string, len(testLocales))
		for _, locale := range testLocales {
			bodies[locale] = name + " [" + locale + "] code=${code} ttl=${ttl_minutes}"
		}
		specs[name] = sms.TemplateSpec{
			Params: append([]string(nil), templateParams...),
			Bodies: bodies,
		}
	}
	declaration, err := sms.NewDeclaration(specs)
	if err != nil {
		t.Fatalf("the body-only declaration was refused: %v", err)
	}
	registry := newRegistry()
	registry.setDeclaration(declaration)
	if err := checkSender(registry.Declared(), testDefaultLocale); err != nil {
		t.Fatalf("a body-only declaration did not satisfy the startup check: %v", err)
	}

	r := newRigOver(t, openSQLite(t), registry, newSender(), testDefaultLocale)
	r.allow()
	if _, err := r.sendLogin(); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := len(r.p.pool.queue); got != 1 {
		t.Fatalf("the queue holds %d messages, want 1", got)
	}
	msg := <-r.p.pool.queue
	if msg.Code != "" {
		t.Errorf("the resolved message carries the vendor code %q, and this carrier sends by body", msg.Code)
	}
	if !strings.Contains(msg.Body, "["+testDefaultLocale+"]") {
		t.Errorf("the resolved body %q is not the one the declaration rendered for %s", msg.Body, testDefaultLocale)
	}
	if strings.Contains(msg.Body, "${") {
		t.Errorf("the resolved body %q still holds a placeholder", msg.Body)
	}
	if code := msg.Params["code"]; code == "" || !strings.Contains(msg.Body, code) {
		t.Errorf("the resolved body %q does not carry the code parameter %q", msg.Body, code)
	}
}
