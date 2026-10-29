package config

import "testing"

func TestUIDefaults(t *testing.T) {
	cfg, err := loadSB(t, "", "")
	if err != nil || cfg.UI.Theme != "tenon" || cfg.UI.Animation == nil || !*cfg.UI.Animation {
		t.Fatalf("%v %+v", err, cfg.UI)
	}
	cfg, err = loadSB(t, "[ui]\ntheme = \"gruvbox\"\nanimation = false\n", "")
	if err != nil || cfg.UI.Theme != "gruvbox" || cfg.UI.Animation == nil || *cfg.UI.Animation {
		t.Fatalf("%v %+v", err, cfg.UI)
	}
	// Thème seul : l'animation reste activée.
	cfg, err = loadSB(t, "[ui]\ntheme = \"light\"\n", "")
	if err != nil || cfg.UI.Animation == nil || !*cfg.UI.Animation {
		t.Fatalf("%v %+v", err, cfg.UI)
	}
}

func TestUIUnknownTheme(t *testing.T) {
	_, err := loadSB(t, "[ui]\ntheme = \"nope\"\n", "")
	wantErr(t, err, `config: unknown ui.theme "nope"`)
}

func TestUIRefusedInProject(t *testing.T) {
	_, err := loadSB(t, "", "[ui]\ntheme = \"light\"\n")
	wantErr(t, err, "config: ui not allowed in project configuration")
	_, err = loadSB(t, "", "[ui]\n")
	wantErr(t, err, "config: ui not allowed in project configuration")
}
