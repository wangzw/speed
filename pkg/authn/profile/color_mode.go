package profile

// ColorModeAttributeKey is the account attribute key a user's color-mode
// preference is stored under. It is one of the named uses of the account
// attribute set; the value is light, dark or system.
const ColorModeAttributeKey = "presentation.color-mode"

// ValidColorMode reports whether value is one of the color modes the preference
// admits: light, dark or system. The empty value is none of them; a body's empty
// string is the clear instruction and is handled before this check.
func ValidColorMode(value string) bool {
	switch value {
	case "light", "dark", "system":
		return true
	default:
		return false
	}
}
