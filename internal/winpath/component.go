// Package winpath defines the shared conservative Windows filename policy.
// Path rooting, normalization and filesystem identity remain caller concerns.
package winpath

import "strings"

// ValidComponent accepts one ordinary filename component, not a path or a dot
// navigation segment. Callers must check raw components before normalization.
func ValidComponent(part string) bool {
	if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `\/:<>"|?*`) {
		return false
	}
	for _, c := range part {
		if c < 32 || c == 127 {
			return false
		}
	}
	stem, _, _ := strings.Cut(strings.ToUpper(part), ".")
	stem = strings.TrimRight(stem, " ")
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$", "CONIN$", "CONOUT$":
		return false
	}
	if strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT") {
		suffix := stem[3:]
		// Keep the existing strict refusal of superscript device prefixes even
		// with extra suffix text; ASCII device numbers match exactly one digit.
		if len(suffix) == 1 && suffix[0] >= '0' && suffix[0] <= '9' ||
			strings.HasPrefix(suffix, "\u00b9") || strings.HasPrefix(suffix, "\u00b2") || strings.HasPrefix(suffix, "\u00b3") {
			return false
		}
	}
	return true
}
