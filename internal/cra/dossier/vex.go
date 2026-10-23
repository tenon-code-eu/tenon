package dossier

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// VEXEntry est une déclaration VEX : une vulnérabilité pour un composant.
type VEXEntry struct {
	ID, Ref, State, Justification string
}

// États VEX.
const (
	VEXNotAffected        = "not_affected"
	VEXAffected           = "affected"
	VEXFixed              = "fixed"
	VEXUnderInvestigation = "under_investigation"
	vexInTriage           = "in_triage"
)

type rawVEX struct {
	Vulnerabilities []struct {
		ID       string `json:"id"`
		Analysis struct {
			State         string `json:"state"`
			Justification string `json:"justification"`
		} `json:"analysis"`
		Affects []struct {
			Ref string `json:"ref"`
		} `json:"affects"`
	} `json:"vulnerabilities"`
}

// LoadVEX lit un VEX CycloneDX ; un fichier absent donne une liste vide.
func LoadVEX(path string) ([]VEXEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cra: vex: %w", err)
	}
	var raw rawVEX
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cra: vex: %w", err)
	}
	var out []VEXEntry
	for _, v := range raw.Vulnerabilities {
		state := v.Analysis.State
		switch state {
		case "", vexInTriage:
			state = VEXUnderInvestigation
		case "exploitable":
			state = VEXAffected
		case "resolved", "resolved_with_pedigree":
			state = VEXFixed
		case "false_positive":
			state = VEXNotAffected
		case VEXNotAffected, VEXAffected, VEXFixed, VEXUnderInvestigation:
		default:
			return nil, fmt.Errorf("cra: vex: %s: unknown state %q", v.ID, state)
		}
		if state == VEXNotAffected && v.Analysis.Justification == "" {
			return nil, fmt.Errorf("cra: vex: %s: not_affected requires a justification", v.ID)
		}
		for _, a := range v.Affects {
			out = append(out, VEXEntry{ID: v.ID, Ref: a.Ref, State: state, Justification: v.Analysis.Justification})
		}
	}
	return out, nil
}
