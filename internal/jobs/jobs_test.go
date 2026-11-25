package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"vigie/internal/store"
)

func TestJobLifecycle(t *testing.T) {
	st, _ := store.New(t.TempDir())
	m := NewManager(st)
	release := make(chan struct{})
	j, err := m.Submit("standard", map[string]int{"cycles": 1}, "test", func(ctx context.Context, r *Reporter) (any, error) {
		r.Progress(10, "ligne de base")
		r.Data("second", map[string]int{"t": 1})
		<-release
		r.Progress(5, "recul ignoré")
		r.Progress(90, "analyse")
		return map[string]string{"verdict": "OK"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// verrou global
	if _, err := m.Submit("bench", nil, "test", func(context.Context, *Reporter) (any, error) { return nil, nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("second job accepté : %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	hist, ch, unsub, ok := m.Subscribe(j.ID, 0)
	if !ok || len(hist) < 3 {
		t.Fatalf("historique : %v %d", ok, len(hist))
	}
	if cur := m.Current(); cur == nil || cur.ID != j.ID || cur.Progress != 10 {
		t.Fatalf("courant : %+v", cur)
	}
	close(release)
	var types []string
	for e := range ch {
		types = append(types, e.Type)
	}
	unsub()
	got, _ := m.Get(j.ID)
	if got.State != Succeeded || got.Progress != 100 || !strings.Contains(string(got.Result), "OK") {
		t.Fatalf("job : %+v", got)
	}
	if !strings.Contains(strings.Join(types, ","), "result") || types[len(types)-1] != "state" {
		t.Fatalf("événements : %v", types)
	}
	// progression jamais en recul : la dernière progression avant 90 reste 10
	for _, l := range got.Log {
		if strings.Contains(l, "recul ignoré") && !strings.HasPrefix(l, "[ 10%]") {
			t.Fatalf("recul de progression : %s", l)
		}
	}
	// persistance : un nouveau gestionnaire relit le job
	m2 := NewManager(st)
	if j2, ok := m2.Get(j.ID); !ok || j2.State != Succeeded {
		t.Fatal("job non persisté")
	}
	if l := m2.List("standard", 10); len(l) != 1 || l[0].Log != nil {
		t.Fatalf("liste : %+v", l)
	}
	if m.Current() != nil {
		t.Fatal("aucun job ne doit être en cours")
	}
}

func TestJobCancelAndFailure(t *testing.T) {
	m := NewManager(nil)
	if l := m.List("", 10); l == nil {
		t.Fatal("liste vide : [] attendu, pas nil")
	}
	j, _ := m.Submit("bench", nil, "test", func(ctx context.Context, r *Reporter) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	time.Sleep(20 * time.Millisecond)
	if err := m.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	m.Wait(j.ID)
	if got, _ := m.Get(j.ID); got.State != Cancelled {
		t.Fatalf("état : %s", got.State)
	}
	if err := m.Cancel(j.ID); err == nil {
		t.Fatal("annulation d'un job terminé acceptée")
	}
	j, _ = m.Submit("bench", nil, "test", func(context.Context, *Reporter) (any, error) { return nil, errors.New("boum") })
	m.Wait(j.ID)
	if got, _ := m.Get(j.ID); got.State != Failed || got.Error != "boum" {
		t.Fatalf("échec : %+v", got)
	}
	j, _ = m.Submit("bench", nil, "test", func(context.Context, *Reporter) (any, error) { panic("aïe") })
	m.Wait(j.ID)
	if got, _ := m.Get(j.ID); got.State != Failed || !strings.Contains(got.Error, "panique") {
		t.Fatalf("panique : %+v", got)
	}
	// un abonné qui arrive après la fin reçoit l'historique et un canal fermé
	hist, ch, unsub, ok := m.Subscribe(j.ID, 0)
	defer unsub()
	if !ok || len(hist) == 0 {
		t.Fatal("historique après la fin")
	}
	if _, open := <-ch; open {
		t.Fatal("canal non fermé")
	}
	var last Event
	json.Unmarshal(mustJSON(hist[len(hist)-1]), &last)
	if last.Type != "state" || last.State != Failed {
		t.Fatalf("dernier événement : %+v", last)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
