package tenant

import (
	"errors"
	"strings"
	"testing"
)

// TestNormalizeTrimsTheProfile checks that Normalize trims whitespace around each
// field while keeping whitespace inside the description.
func TestNormalizeTrimsTheProfile(t *testing.T) {
	got := Profile{
		DisplayName:  "  Acme Corporation \n",
		Description:  "\t First line.\nSecond line.  ",
		ContactEmail: "  ops@acme.test  ",
		Website:      "  https://acme.test  ",
	}.Normalize()
	want := Profile{
		DisplayName:  "Acme Corporation",
		Description:  "First line.\nSecond line.",
		ContactEmail: "ops@acme.test",
		Website:      "https://acme.test",
	}
	if got != want {
		t.Errorf("Normalize gave %+v, want %+v", got, want)
	}
}

// TestValidateAcceptsTheProfileDomain holds the values the profile admits: the
// empty profile, the field bounds, and an absolute http or https website.
func TestValidateAcceptsTheProfileDomain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile Profile
	}{
		{"the empty profile", Profile{}},
		{"a display name at its bound", Profile{DisplayName: strings.Repeat("好", 100)}},
		{"a description at its bound", Profile{Description: strings.Repeat("好", 500)}},
		{"a contact address", Profile{ContactEmail: "ops@acme.test"}},
		{"a contact address with a plus tag", Profile{ContactEmail: "ops+tenancy@acme.test"}},
		{"an https website", Profile{Website: "https://acme.test/about?q=1"}},
		{"an http website", Profile{Website: "http://acme.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.profile.Validate(); err != nil {
				t.Fatalf("Validate(%+v) gave %v, want nil", tc.profile, err)
			}
		})
	}
}

// TestValidateRefusesTheProfileOutsideItsDomain checks each bound and shape the
// profile refuses against ErrInvalidProfile.
func TestValidateRefusesTheProfileOutsideItsDomain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile Profile
	}{
		{"a display name beyond its rune bound", Profile{DisplayName: strings.Repeat("好", 101)}},
		{"a description beyond its rune bound", Profile{Description: strings.Repeat("好", 501)}},
		{"a contact email beyond its byte bound", Profile{ContactEmail: strings.Repeat("a", 250) + "@b.co"}},
		{"a contact email that is not an address", Profile{ContactEmail: "not an address"}},
		{"a contact email carrying a display name", Profile{ContactEmail: "Ops <ops@acme.test>"}},
		{"a website beyond its byte bound", Profile{Website: "https://acme.test/" + strings.Repeat("a", 2048)}},
		{"a relative website", Profile{Website: "/about"}},
		{"a website with no host", Profile{Website: "https:///about"}},
		{"a website that is not http or https", Profile{Website: "ftp://acme.test"}},
		{"a website with userinfo", Profile{Website: "https://user:secret@acme.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.profile.Validate()
			if !errors.Is(err, ErrInvalidProfile) {
				t.Fatalf("Validate(%+v) gave %v, want ErrInvalidProfile", tc.profile, err)
			}
		})
	}
}
