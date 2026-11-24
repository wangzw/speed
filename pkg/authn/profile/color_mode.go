package profile

// ColorModeAttributeKey is the account attribute key a user's color-mode
// preference is stored under; its value is light, dark or system.
const ColorModeAttributeKey = "presentation.color-mode"

// ValidColorMode reports whether value is light, dark or system. The empty
// value is not valid; a body's empty string clears the attribute instead.
func ValidColorMode(value string) bool {
	switch value {
	case "light", "dark", "system":
		return true
	default:
		return false
	}
}
