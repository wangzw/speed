//go:build review

package main

// The review configuration widens the per-source and per-account send budgets
// so a walkthrough is not throttled. This case holds the widened keys to three
// facts at once: each is seeded, each lies inside the domain its module
// declares, and each is above the declared default. The limits are read from
// the declarations rather than restated here, so a module that moves a bound
// moves the case with it.

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/settings"
)

// reviewWidenedBudget is one setting the review configuration widens: the
// namespace its module declares it under and the item's key within it.
type reviewWidenedBudget struct {
	namespace string
	key       string
}

// reviewWidenedBudgets are the send budgets the review configuration raises
// above their declared defaults.
var reviewWidenedBudgets = []reviewWidenedBudget{
	{"authn.sms", "per-source-hourly"},
	{"authn.sms", "per-source-daily"},
	{"authn.login", "email-per-account-daily"},
	{"authn.login", "email-per-source-hourly"},
	{"authn.login", "email-per-source-daily"},
}

// wholeNumberRange reads the range a declared integer domain states. The
// declaration's rule is its own statement of what a write may store, so the
// case reads the limits from it instead of restating them. A domain phrased
// another way is reported rather than passed silently.
var wholeNumberRange = regexp.MustCompile(`^whole numbers from ([0-9]+) to ([0-9]+)$`)

// TestReviewWidensTheSendBudgets holds the review configuration's widened send
// budgets to the declarations of the modules that own them: the keys are
// seeded, and every value is inside its declared domain and above the declared
// default.
func TestReviewWidensTheSendBudgets(t *testing.T) {
	var seed seedInput
	readEffective(t, "the review configuration", "file:review/sqlite.yaml", "seed", &seed)

	seeded := make(map[string]string, len(seed.Settings))
	for _, item := range seed.Settings {
		seeded[item.Key] = item.Value
	}

	for _, budget := range reviewWidenedBudgets {
		key := budget.namespace + "." + budget.key
		raw, present := seeded[key]
		if !present {
			t.Errorf("the review configuration seeds no %s", key)
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil {
			t.Errorf("the review configuration seeds %s as %q, which is not a whole number", key, raw)
			continue
		}
		item, fallback := declaredIntegerSetting(t, budget)
		if value <= fallback {
			t.Errorf("the review configuration seeds %s as %d, which is not above the declared default %d",
				key, value, fallback)
		}
		rule := item.Domain.Rule()
		if rule == "" {
			// The zero domain admits every value of the declared type.
			continue
		}
		match := wholeNumberRange.FindStringSubmatch(rule)
		if match == nil {
			t.Errorf("the setting %s states the domain %q in a shape this case cannot read", key, rule)
			continue
		}
		low, lowErr := strconv.Atoi(match[1])
		high, highErr := strconv.Atoi(match[2])
		if lowErr != nil || highErr != nil {
			t.Errorf("the setting %s states the domain %q, whose bounds are not whole numbers", key, rule)
			continue
		}
		if value < low || value > high {
			t.Errorf("the review configuration seeds %s as %d, outside its declared domain %d to %d",
				key, value, low, high)
		}
	}
}

// declaredIntegerSetting answers one integer setting's declaration and its
// default, read off the schema the declaring module registers: the item
// carries the domain, and the mount struct carries the default in the field
// the same key tags.
func declaredIntegerSetting(t *testing.T, budget reviewWidenedBudget) (settings.Item, int) {
	t.Helper()
	for _, resource := range core.Resources[settings.Schema](core.ProcessRegistry) {
		if resource.Value.Namespace != budget.namespace {
			continue
		}
		item, declared := resource.Value.Items[budget.key]
		if !declared {
			t.Fatalf("the module %q declares no setting %q", budget.namespace, budget.key)
		}
		fallback, found := mountIntegerDefault(resource.Value.Mounts, budget.key)
		if !found {
			t.Fatalf("the module %q carries no %q integer field in its settings mount",
				budget.namespace, budget.key)
		}
		return item, fallback
	}
	t.Fatalf("the review build declares no settings schema for %q", budget.namespace)
	return settings.Item{}, 0
}

// mountIntegerDefault reads the default of one integer item off a schema's
// mount structs, matched by the config tag the item key is written under.
func mountIntegerDefault(mounts []settings.Mount, key string) (int, bool) {
	for _, mount := range mounts {
		value := reflect.ValueOf(mount.Value)
		if value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct {
			continue
		}
		typ := value.Type()
		for i := 0; i < typ.NumField(); i++ {
			tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("config"), ",")
			if tag != key {
				continue
			}
			fallback, ok := value.Field(i).Interface().(int)
			return fallback, ok
		}
	}
	return 0, false
}
