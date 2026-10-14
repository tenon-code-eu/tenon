package policy

import (
	"bytes"
	"encoding/json"
	"strings"
)

// RedactJSON masks a JSON payload and returns the masked text with the number
// of masks per type. Each string is masked once decoded: in raw JSON text a
// newline is the two characters `\n`, so line-anchored rules (KEY=value alone
// on its line) would not apply. Values under a secret-looking key are masked
// whole. The re-encoded text then goes through raw masking (patterns spanning
// a key and its value); if that pass breaks the JSON, the per-string result is
// kept. Text that is not a single JSON value is masked raw.
func (r *Redactor) RedactJSON(s string) (string, map[string]int) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return r.Redact(s)
	}
	counts := map[string]int{}
	v = r.walk(v, counts)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return r.Redact(s)
	}
	walked := strings.TrimSuffix(buf.String(), "\n")
	out, c := r.Redact(walked)
	if !json.Valid([]byte(out)) {
		return walked, counts
	}
	for k, n := range c {
		counts[k] += n
	}
	return out, counts
}

// walk masks every string (and map key) of a decoded JSON value in place.
func (r *Redactor) walk(v any, counts map[string]int) any {
	merge := func(c map[string]int) {
		for k, n := range c {
			counts[k] += n
		}
	}
	switch t := v.(type) {
	case string:
		s, c := r.Redact(t)
		merge(c)
		return s
	case []any:
		for i := range t {
			t[i] = r.walk(t[i], counts)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			nk, c := r.Redact(k)
			merge(c)
			if xs, ok := x.(string); ok && len(xs) >= 8 && reSecretKey.MatchString(k) {
				m := r.maskSecret(xs)
				if m != xs {
					counts["SECRET"]++
				}
				out[nk] = m
				continue
			}
			out[nk] = r.walk(x, counts)
		}
		return out
	}
	return v
}
