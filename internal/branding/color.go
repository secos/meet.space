package branding

// DefaultBrand is the platform's default brand color (design.md §2.1).
const DefaultBrand = "#2563eb"

// ValidHex reports whether s matches ^#[0-9a-fA-F]{6}$ exactly.
func ValidHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}
