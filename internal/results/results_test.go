package results

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	s := Open(t.TempDir())
	if s.Exists() {
		t.Fatal("fichier inattendu")
	}
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	v := 1.03
	pts := []Point{
		{T: now.Add(-40 * 24 * time.Hour), Job: "a", Profile: "standard", Host: "esx1", Datastore: "ds1", WriteMBps: 400},
		{T: now.Add(-2 * time.Hour), Job: "b", Profile: "standard", Host: "esx1", Datastore: "ds1", WriteMBps: 450, TxRatio: &v, Triggered: []string{"x", "y"}},
		{T: now.Add(-1 * time.Hour), Job: "c", Profile: "local", Scheduled: true, Host: "local", Datastore: "ds2", WriteMBps: 300, IOProfile: "écriture, bloc 1 Mio"},
	}
	if err := s.Append(pts[1:]); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(pts[:1]); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Query(Filter{})
	if len(all) != 3 || all[0].Job != "a" {
		t.Fatalf("tri : %+v", all)
	}
	if got, _ := s.Query(Filter{Host: "esx1", From: now.Add(-24 * time.Hour)}); len(got) != 1 || got[0].Job != "b" {
		t.Errorf("filtre : %+v", got)
	}
	if ser, _ := s.Series(); len(ser) != 2 {
		t.Errorf("séries : %v", ser)
	}
	if n, err := s.Prune(30, now); err != nil || n != 1 {
		t.Errorf("rétention : %d %v", n, err)
	}
	if all, _ = s.Query(Filter{}); len(all) != 2 {
		t.Errorf("après rétention : %d", len(all))
	}
	var b bytes.Buffer
	if err := WriteCSV(&b, all); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "time_utc,") || !strings.Contains(lines[1], "1.030") || !strings.Contains(lines[1], "x | y") {
		t.Errorf("CSV :\n%s", b.String())
	}
	if err := s.Reset(nil); err != nil || !s.Exists() {
		t.Error("Reset vide doit créer le fichier")
	}
}
