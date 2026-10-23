package config

import "testing"

func TestCRAProjectRefused(t *testing.T) {
	_, err := loadSB(t, "", "[cra]\nosv_mirror = \"https://m.example\"\n")
	wantErr(t, err, "config: cra not allowed in project configuration")
	_, err = loadSB(t, "", "[cra]\n")
	wantErr(t, err, "config: cra not allowed in project configuration")
}

func TestCRAGlobal(t *testing.T) {
	cfg, err := loadSB(t, "[cra]\nosv_mirror = \"https://m.example/osv\"\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CRA.OSVMirror != "https://m.example/osv" {
		t.Fatalf("%q", cfg.CRA.OSVMirror)
	}
}
