package sms

import (
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/sms"
)

// TestTheTemplatesWorkOnABodyOnlyCarrier checks that the templates work on a body-only carrier.
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
