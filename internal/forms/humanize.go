package forms

import (
	"strings"
	"unicode"
)

// HumanizePath returns a display name from a URL path by humanizing the last segment.
// Splits on "-" and "_", title-cases each word. Returns "Home" for root or empty paths.
func HumanizePath(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "Home"
	}
	seg := path[strings.LastIndex(path, "/")+1:]
	if seg == "" {
		return "Home"
	}
	words := splitDelimiters(seg)
	return titleJoin(words)
}

// HumanizeKey returns a display label from a form field key.
// Splits on "_", "-", and camelCase boundaries, title-cases each word.
// All-uppercase runs (ID, HTML, URL) stay grouped as single tokens.
func HumanizeKey(key string) string {
	if key == "" {
		return ""
	}
	// First split on explicit delimiters.
	parts := splitDelimiters(key)
	// Then split each part on camelCase boundaries.
	var words []string
	for _, part := range parts {
		words = append(words, splitCamel(part)...)
	}
	return titleJoin(words)
}

// splitDelimiters splits s on "-" and "_".
func splitDelimiters(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_'
	})
}

// splitCamel splits a string on camelCase boundaries.
// Uppercase runs are kept together: "HTMLContent" -> ["HTML", "Content"],
// "userID" -> ["user", "ID"].
func splitCamel(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		cur := runes[i]
		// Split before an uppercase letter that follows a lowercase letter: "lastName" -> "last|Name"
		if unicode.IsLower(prev) && unicode.IsUpper(cur) {
			words = append(words, string(runes[start:i]))
			start = i
			continue
		}
		// Split inside an uppercase run when followed by lowercase: "HTMLContent" -> "HTML|Content"
		if i >= 2 && unicode.IsUpper(runes[i-2]) && unicode.IsUpper(prev) && unicode.IsLower(cur) {
			words = append(words, string(runes[start:i-1]))
			start = i - 1
		}
	}
	words = append(words, string(runes[start:]))
	return words
}

// titleJoin title-cases each word and joins with spaces.
// Words that are all uppercase and len >= 2 are kept as-is (ID, HTML, URL).
func titleJoin(words []string) string {
	var b strings.Builder
	for i, w := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		if len(w) >= 2 && isAllUpper(w) {
			b.WriteString(w)
		} else {
			b.WriteString(titleCase(w))
		}
	}
	return b.String()
}

func isAllUpper(s string) bool {
	for _, r := range s {
		if !unicode.IsUpper(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func titleCase(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}
