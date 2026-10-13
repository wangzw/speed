package authn

// The closed set of message languages. It is the set the login engine maps a
// request into and the SMS module hands to a carrier: a carrier approves a
// template per language, so the language is one dimension of the template lookup
// and a value outside the set is a caller defect, not a language to send in.
const (
	// LocaleEN is English. It is the default, read from a value that names none.
	LocaleEN = "en-US"
	// LocaleZH is the other language of the closed set.
	LocaleZH = "zh-CN"
)

// LocaleOf reads the message language a caller named: the empty value, read as
// nothing named, is LocaleEN, and a value the caller named is returned unchanged.
// The mapping fills in the default and never replaces one language with another,
// so a value outside the closed set stays what the caller named and is the
// caller's defect to refuse.
//
// The default is English, matching the default the i18n capability renders user
// facing text in. A caller on the request path names a language from the i18n
// context first; this mapping's default stands for the case where nothing named
// one at all, i18n absent included.
func LocaleOf(locale string) string {
	if locale == "" {
		return LocaleEN
	}
	return locale
}
