package policy

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// RedactionRule is a user-defined masking rule. Matches are masked under the
// type strings.ToUpper(Name).
type RedactionRule struct{ Name, Pattern string }

type step struct {
	typ   string
	re    *regexp.Regexp
	group int                 // capture group to mask (0 = whole match)
	guard bool                // require non-word neighbours around the match
	valid func(string) bool   // optional checksum; nil = always keep
	key   func(string) string // marker identity of a value; nil = identity
}

// Redactor masks secrets and personal data. It is stateful (same value, same
// marker) and safe for concurrent use.
type Redactor struct {
	steps []step

	mu       sync.Mutex
	markers  map[string]string // type + "\x00" + key -> marker
	counters map[string]int
}

var (
	secretKW = `(?:api[_-]?key|secret|token|password|passwd)`
	// Quoted literal after keyword and [:=]; quotes may be JSON-escaped.
	reAssignQuoted = regexp.MustCompile(`(?i)` + secretKW + `\w*\\?["']?[ \t]*[:=][ \t]*\\?["']([^\s'"\\]{12,})`)
	// Unquoted env/config line: [export] KEY=VALUE alone on its line.
	reAssignLine = regexp.MustCompile(`(?im)^[ \t]*(?:export[ \t]+)?[A-Za-z0-9_.-]*` + secretKW + `\w*[ \t]*[:=][ \t]*([^\s'"\\]{12,}?)[,;]?[ \t]*\r?$`)
	// export KEY=VALUE inside a longer shell command.
	reAssignExport = regexp.MustCompile(`(?i)\bexport[ \t]+[A-Za-z0-9_.-]*` + secretKW + `\w*=([^\s'"\\;&|,]{12,})`)
	reMarker       = regexp.MustCompile(`\[[A-Z_]+_\d+\]`)
	reIdent        = regexp.MustCompile(`^[A-Za-z_][\w.]*$`)
	reSecretKey    = regexp.MustCompile(`(?i)` + secretKW)
	reIBAN         = regexp.MustCompile(`[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,3})?`)
	reNIR          = regexp.MustCompile(`[12] ?\d{2} ?\d{2} ?(?:\d{2}|2A|2B) ?\d{3} ?\d{3} ?\d{2}`)
	reEmail        = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	reTel          = regexp.MustCompile(`(?:\+33\s?|0)[1-9](?:[\s.-]?\d{2}){4}`)
)

// NewRedactor builds a Redactor with the built-in rules followed by custom.
func NewRedactor(custom []RedactionRule) (*Redactor, error) {
	secret := func(p string, group int) step {
		return step{typ: "SECRET", re: regexp.MustCompile(p), group: group}
	}
	r := &Redactor{
		markers:  map[string]string{},
		counters: map[string]int{},
		steps: []step{
			// Private key blocks. Bodies may carry JSON-escaped newlines (a
			// literal backslash-n); lazy [\s\S] covers both forms.
			secret(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`, 0),
			// Truncated key (no END line): mask the header and base64 body.
			secret(`-----BEGIN [A-Z ]*PRIVATE KEY-----(?:[A-Za-z0-9+/=\s]|\\[nrt])*`, 0),
			secret(`AKIA[0-9A-Z]{16}`, 0),
			secret(`gh[pousr]_[A-Za-z0-9]{36}`, 0),
			secret(`\bsk-[A-Za-z0-9_-]{20,}`, 0),
			secret(`eyJ[\w-]+\.[\w-]+\.[\w-]+`, 0),
			secret(`github_pat_[A-Za-z0-9_]{20,}`, 0),
			secret(`(?:sk|rk)_live_[A-Za-z0-9]{16,}`, 0),
			secret(`xox[baprs]-[A-Za-z0-9-]{10,}`, 0),
			secret(`://[^:/\s]+:([^@\s'"\\]+)@`, 1),
			secret(`(?i:bearer)\s+([A-Za-z0-9._~+/=-]{20,})`, 1),
			{typ: "SECRET", re: reAssignQuoted, group: 1, valid: validSecretValue},
			{typ: "SECRET", re: reAssignLine, group: 1, valid: validSecretValue},
			{typ: "SECRET", re: reAssignExport, group: 1, valid: validSecretValue},
			{typ: "IBAN", re: reIBAN, guard: true, valid: validIBAN, key: stripSpaces},
			{typ: "NIR", re: reNIR, guard: true, valid: validNIR, key: stripSpaces},
			{typ: "EMAIL", re: reEmail, valid: validEmail, key: strings.ToLower},
			{typ: "TEL", re: reTel, guard: true, valid: validTel, key: stripSeparators},
		},
	}
	for _, c := range custom {
		if strings.TrimSpace(c.Name) == "" {
			return nil, fmt.Errorf("règle de masquage sans nom")
		}
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			return nil, fmt.Errorf("règle de masquage %q invalide : %w", c.Name, err)
		}
		r.steps = append(r.steps, step{typ: strings.ToUpper(c.Name), re: re})
	}
	return r, nil
}

// Redact masks s and returns the masked text with the number of masks per type.
func (r *Redactor) Redact(s string) (string, map[string]int) {
	counts := map[string]int{}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, st := range r.steps {
		s = r.apply(st, s, counts)
	}
	return s, counts
}

func (r *Redactor) apply(st step, s string, counts map[string]int) string {
	locs := st.re.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	marks := reMarker.FindAllStringIndex(s, -1)
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		a, e := loc[2*st.group], loc[2*st.group+1]
		if a < 0 || a < last || overlaps(marks, a, e) {
			continue
		}
		val := s[a:e]
		if st.guard && !boundaryOK(s, a, e) {
			continue
		}
		if st.valid != nil && !st.valid(val) {
			continue
		}
		b.WriteString(s[last:a])
		b.WriteString(r.marker(st, val))
		counts[st.typ]++
		last = e
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func (r *Redactor) marker(st step, val string) string {
	k := val
	if st.key != nil {
		k = st.key(val)
	}
	id := st.typ + "\x00" + k
	if m, ok := r.markers[id]; ok {
		return m
	}
	r.counters[st.typ]++
	m := fmt.Sprintf("[%s_%d]", st.typ, r.counters[st.typ])
	r.markers[id] = m
	return m
}

func boundaryOK(s string, a, e int) bool {
	if a > 0 {
		c, _ := utf8.DecodeLastRuneInString(s[:a])
		if isWord(c) || c == '-' {
			return false
		}
	}
	if e < len(s) {
		c, _ := utf8.DecodeRuneInString(s[e:])
		if isWord(c) || c == '-' {
			return false
		}
	}
	return true
}

func isWord(c rune) bool { return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c) }

func stripSpaces(s string) string { return strings.ReplaceAll(s, " ", "") }

func stripSeparators(s string) string {
	s = strings.NewReplacer(" ", "", ".", "", "-", "").Replace(s)
	return strings.TrimPrefix(s, "+33")
}

// validIBAN checks length and the ISO 7064 mod-97 rule.
func validIBAN(s string) bool {
	s = stripSpaces(s)
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	s = s[4:] + s[:4]
	rem := 0
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			rem = (rem*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			rem = (rem*100 + int(c-'A') + 10) % 97
		default:
			return false
		}
	}
	return rem == 1
}

// validNIR checks the French social security number key (2A->19, 2B->18).
func validNIR(s string) bool {
	s = stripSpaces(s)
	if len(s) != 15 {
		return false
	}
	body, key := s[:13], s[13:]
	body = strings.Replace(body, "2A", "19", 1)
	body = strings.Replace(body, "2B", "18", 1)
	var n, k int64
	for _, c := range body {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int64(c-'0')
	}
	for _, c := range key {
		k = k*10 + int64(c-'0')
	}
	return k == 97-n%97
}

func overlaps(marks [][]int, a, e int) bool {
	for _, m := range marks {
		if a < m[1] && m[0] < e {
			return true
		}
	}
	return false
}

// maskSecret masks a whole value as SECRET (used for values under secret keys).
func (r *Redactor) maskSecret(val string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reMarker.MatchString(val) && len(reMarker.FindString(val)) == len(val) {
		return val
	}
	return r.marker(step{typ: "SECRET"}, val)
}

// validSecretValue rejects code-like values: identifiers, dotted chains,
// calls, expansions, and values that are not a letter/digit/symbol mix.
func validSecretValue(v string) bool {
	if len(v) < 12 || strings.ContainsAny(v, "()[]{}$") {
		return false
	}
	var up, lo, dg, sym bool
	for _, c := range v {
		switch {
		case c >= 'A' && c <= 'Z':
			up = true
		case c >= 'a' && c <= 'z':
			lo = true
		case c >= '0' && c <= '9':
			dg = true
		default:
			sym = true
		}
	}
	// Identifier or dotted chain (env var name, member access): code, not a
	// secret, unless it is a dot-free letter/digit mix.
	if reIdent.MatchString(v) && (strings.Contains(v, ".") || !dg) {
		return false
	}
	return ((up || lo) && dg) || (up && lo && sym)
}

func validEmail(s string) bool {
	local, _, _ := strings.Cut(s, "@")
	return !strings.EqualFold(local, "git")
}

func validTel(s string) bool { return stripSeparators(s) != "0123456789" }
