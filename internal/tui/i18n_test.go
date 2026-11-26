package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestCatalogWellFormed(t *testing.T) {
	verbs := func(s string) string {
		v := verbRe.FindAllString(strings.ReplaceAll(s, "%%", ""), -1)
		sort.Strings(v)
		return strings.Join(v, " ")
	}
	for src, row := range cat.formats {
		for i, tr := range row {
			if strings.TrimSpace(tr) == "" && strings.TrimSpace(src) != "" {
				t.Errorf("%q : traduction vide (colonne %d)", src, i)
			}
			if verbs(tr) != verbs(src) {
				t.Errorf("%q : variables %q ≠ %q en colonne %d (%q)", src, verbs(tr), verbs(src), i, tr)
			}
		}
	}
	// libellés de menu : 16 colonnes au plus dans toutes les langues
	a := NewApp(&Client{}, 80, 25)
	for _, l := range Languages {
		SetLang(l.Code)
		for _, tab := range a.Tabs {
			for _, it := range tab.Items {
				if w := width(T(it.Label)); w > menuW-2 {
					t.Errorf("%s : menu %q → %q (%d colonnes)", l.Code, it.Label, T(it.Label), w)
				}
			}
		}
	}
	SetLang(DefaultLang)
}

// exercise parcourt la plupart des écrans et états de la console.
func exercise(t *testing.T, lang string) {
	h := newHarnessAt(t, true)
	SetLang(lang)
	h.settle()
	h.keys("F12", "Esc", "Enter")
	for ti, tab := range h.a.Tabs {
		h.keys("F" + string(rune('1'+ti)))
		for i := range tab.Items {
			h.a.Focus = false
			h.a.switchView(func() { tab.Sel = i })
			h.settle()
			h.keys("Enter")
			h.keys("Esc")
		}
	}
	// assistant d'ajout de disque jusqu'à l'adoption
	h.keys("F2", "Home", "Down", "Enter", "Enter")
	mkDisk(t, h.guest, "sdb", "naa.6000c29d111122223333444455556666", 8<<30, "0:0:1:0", "8:16")
	h.keys("Enter", "Enter")
	h.typeText("sdc")
	h.keys("Enter", "Backspace")
	h.typeText("b")
	h.keys("Enter", "Enter", "Esc", "Up", "End", "Enter", "Enter", "o")
	// système : erreurs de saisie, confirmations
	h.keys("F3", "Home", "Down", "Enter", "Right", "Enter", "Esc", "Esc")
	h.keys("Down", "Down", "Enter")
	h.typeText("abc")
	h.keys("Enter", "Esc", "Esc")
	// réseau : cible manquante, formulaires d'ajout
	h.keys("F1", "Home", "Enter", "Enter", "Esc", "Down", "Down", "Enter", "a", "Esc", "Esc")
	h.keys("End", "Up", "Up", "Enter", "a", "Esc", "Esc")
}

// TestI18nDump (VIGIE_I18N_DUMP=fichier) : liste les textes affichés sans traduction.
func TestI18nDump(t *testing.T) {
	out := os.Getenv("VIGIE_I18N_DUMP")
	if out == "" {
		t.Skip("VIGIE_I18N_DUMP non défini")
	}
	missing, tracking = map[string]bool{}, true
	defer func() { tracking = false }()
	exercise(t, "de")
	var l []string
	for s := range missing {
		l = append(l, s)
	}
	sort.Strings(l)
	_ = os.WriteFile(filepath.Clean(out), []byte(strings.Join(l, "\n")+"\n"), 0o644)
}

// Aucun texte français ne doit rester à l'écran dans les autres langues.
var technical = regexp.MustCompile(`^[\s\d.,:/%()<>_+·|-]*$`)

func TestNoFrenchLeft(t *testing.T) {
	for _, l := range Languages {
		if l.Code == "fr" {
			continue
		}
		missing, tracking = map[string]bool{}, true
		exercise(t, l.Code)
		tracking = false
		for s := range missing {
			if needsTranslation(s) && !technical.MatchString(s) {
				t.Errorf("%s : texte non traduit %q", l.Code, s)
			}
		}
	}
}

// « %% sur » n'est pas une variable « % s » : le gabarit garde ses variables et son « % ».
func TestPercentLiteralInPattern(t *testing.T) {
	defer SetLang(DefaultLang)
	SetLang("en")
	got := T("au plus 6,2 % sur 4 palier(s) (seuil 9 %)")
	if want := "at most 6.2 % over 4 step(s) (threshold 9 %)"; got != want && got != "at most 6,2 % over 4 step(s) (threshold 9 %)" {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// À préfixe égal, le gabarit le plus précis l'emporte sur un gabarit générique (« %s — %s »).
func TestSpecificPatternFirst(t *testing.T) {
	defer SetLang(DefaultLang)
	SetLang("en")
	got := T("[ds] vm/testdisk.vmdk — 4 Gio thin sur ds — prérempli 4 Gio")
	if want := "[ds] vm/testdisk.vmdk — 4 GiB thin on ds — prefilled 4 GiB"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
