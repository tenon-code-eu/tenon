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
	reAssign = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|passwd)\w*["']?\s*[:=]\s*['"]?([^\s'"]{12,})`)
	reIBAN   = regexp.MustCompile(`[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,3})?`)
	reNIR    = regexp.MustCompile(`[12] ?\d{2} ?\d{2} ?(?:\d{2}|2A|2B) ?\d{3} ?\d{3} ?\d{2}`)
	reEmail  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
	reTel    = regexp.MustCompile(`(?:\+33\s?|0)[1-9](?:[\s.-]?\d{2}){4}`)
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
			{typ: "SECRET", re: reAssign, group: 2},
			{typ: "IBAN", re: reIBAN, guard: true, valid: validIBAN, key: stripSpaces},
			{typ: "NIR", re: reNIR, guard: true, valid: validNIR, key: stripSpaces},
			{typ: "EMAIL", re: reEmail, key: strings.ToLower},
			{typ: "TEL", re: reTel, guard: true, key: stripSeparators},
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
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		a, e := loc[2*st.group], loc[2*st.group+1]
		if a < 0 || a < last {
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
