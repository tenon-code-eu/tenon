package tools

import "fmt"

// Truncate coupe s à limit caractères et signale la troncature.
func Truncate(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	n := 0
	for i := range s {
		if n == limit {
			return s[:i] + fmt.Sprintf("\n[… output truncated to %d characters]", limit)
		}
		n++
	}
	return s
}
