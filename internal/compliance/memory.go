package compliance

import (
	"github.com/tenon-code-eu/tenon/internal/event"
)

// MemoryWrite : une écriture de mémoire. Ni titre ni contenu : l'événement
// n'en porte pas.
type MemoryWrite struct {
	Scope     string `json:"scope"`
	ID        string `json:"id"`
	Author    string `json:"author"`
	Sensitive bool   `json:"sensitive"`
	Redacted  bool   `json:"redacted"`
}

// MemoryForgotten compte les suppressions par origine.
type MemoryForgotten struct {
	User   int `json:"user"`
	Model  int `json:"model"`
	Expiry int `json:"expiry"`
}

// Memory est la section mémoire du rapport ; omise sans événement mémoire.
type Memory struct {
	Writes         []MemoryWrite   `json:"writes"`
	SensitiveReads int             `json:"sensitive_reads"`
	Forgotten      MemoryForgotten `json:"forgotten"`
	Trusted        int             `json:"trusted"`
}

// memorySection lit les événements mémoire de toutes les sessions, y compris
// les sessions courtes « memory » (purge au démarrage, tenon memory).
func memorySection(ds Dataset, o Options) *Memory {
	m := &Memory{Writes: []MemoryWrite{}}
	seen := false
	for _, l := range ds.Lines {
		if IsMetadataPayload(l.Payload) {
			continue
		}
		switch l.Type {
		case event.MemoryWritten:
			var p event.MemoryWrittenPayload
			if decode(l, &p) {
				seen = true
				m.Writes = append(m.Writes, MemoryWrite{Scope: o.clean(p.Scope), ID: o.clean(p.ID), Author: o.clean(p.Author), Sensitive: p.Sensitive, Redacted: p.Redacted})
			}
		case event.MemoryRead:
			var p event.MemoryReadPayload
			if decode(l, &p) {
				seen = true
				if p.Sensitive {
					m.SensitiveReads++
				}
			}
		case event.MemoryForgotten:
			var p event.MemoryForgottenPayload
			if decode(l, &p) {
				seen = true
				switch p.By {
				case "user":
					m.Forgotten.User++
				case "model":
					m.Forgotten.Model++
				case "expiry":
					m.Forgotten.Expiry++
				}
			}
		case event.MemoryTrusted:
			var p event.MemoryTrustedPayload
			if decode(l, &p) {
				seen = true
				m.Trusted++
			}
		}
	}
	if !seen {
		return nil
	}
	return m
}
