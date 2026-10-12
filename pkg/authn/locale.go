package authn

// The closed set of message languages. It is the set the login engine maps a
// request into and the SMS module hands to a carrier: a carrier approves a
// template per language, so the language is one dimension of the template lookup
// and a value outside the set is a caller defect, not a language to send in.
const (
	// LocaleZH is the default language, read from a value that names none.
	LocaleZH = "zh-CN"
	// LocaleEN is the other language of the closed set.
	LocaleEN = "en-US"
)

// LocaleOf reads the message language a caller named: the empty value, read as
// nothing named, is LocaleZH, and a value the caller named is returned unchanged.
// The mapping fills in the default and never replaces one language with another,
// so a value outside the closed set stays what the caller named and is the
// caller's defect to refuse.
func LocaleOf(locale string) string {
	if locale == "" {
		return LocaleZH
	}
	return locale
}
