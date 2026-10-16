// Package branding validates the values a group can choose for its pages:
// the look and (paid groups) the brand color. Security Rule 10: nothing
// reaches a <style> block or data-look unless it passes these checks.
package branding

import "slices"

// The four looks. Clean is the default for every group and the platform.
const (
	LookClean    = "clean"
	LookLively   = "lively"
	LookConfetti = "confetti"
	LookPoster   = "poster"
)

// Looks is the allowlist, matching the groups.look CHECK constraint.
var Looks = []string{LookClean, LookLively, LookConfetti, LookPoster}

// ValidLook reports whether look is one of the four allowlisted names
// (case-sensitive).
func ValidLook(look string) bool { return slices.Contains(Looks, look) }

// LookOrClean returns look if it is allowlisted, otherwise Clean.
func LookOrClean(look string) string {
	if ValidLook(look) {
		return look
	}
	return LookClean
}
