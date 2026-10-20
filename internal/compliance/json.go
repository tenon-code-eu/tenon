package compliance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// SchemaVersion est la version du schéma JSON du rapport.
const SchemaVersion = 1

// envelope enveloppe le rapport ; l'ordre des champs fixe l'ordre des clés.
type envelope struct {
	SchemaVersion int    `json:"schema_version"`
	Generator     string `json:"generator"`
	ReportID      string `json:"report_id"`
	HTMLSHA256    string `json:"html_sha256"`
	Report        Report `json:"report"`
}

// ReportID est l'empreinte du rapport : "sha256:" + hex du sha256 de son JSON.
func ReportID(r Report) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// RenderJSON rend le rapport indenté de deux espaces, dans son enveloppe,
// avec un saut de ligne final.
func RenderJSON(r Report, reportID, htmlSHA256 string) ([]byte, error) {
	b, err := json.MarshalIndent(envelope{
		SchemaVersion: SchemaVersion,
		Generator:     r.Generator,
		ReportID:      reportID,
		HTMLSHA256:    htmlSHA256,
		Report:        r,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
