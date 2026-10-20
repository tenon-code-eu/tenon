package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Le JSON échoue après l'écriture du HTML (dossier parent absent, invisible
// au contrôle préalable) : l'erreur remonte et le HTML est retiré.
func TestWriteExportJSONFailsRemovesHTML(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "r.html")
	jsonPath := filepath.Join(dir, "absent", "r.json")
	err := writeExport(htmlPath, []byte("<html>"), jsonPath, []byte("{}"))
	if err == nil {
		t.Fatal("pas d'erreur")
	}
	var fe errFileExists
	if errors.As(err, &fe) {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if _, err := os.Lstat(htmlPath); !os.IsNotExist(err) {
		t.Fatalf("HTML laissé : %v", err)
	}
}
