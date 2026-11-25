package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vigie/internal/jobs"
	"vigie/internal/store"
)

func TestReportsAndBranding(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jm := jobs.NewManager(st)
	h := New(Info{Version: "t", Started: time.Now()}, Deps{Settings: NewSettings(st), Jobs: jm})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	if r := do("PUT", "/api/branding", `{"client":"ACME","logo":"data:image/svg+xml;base64,PHN2Zz48c2NyaXB0Lz48L3N2Zz4="}`); r.Code != http.StatusBadRequest {
		t.Errorf("SVG avec script : %d", r.Code)
	}
	if r := do("PUT", "/api/branding", `{"client":"ACME","site":"Nord"}`); r.Code != http.StatusOK {
		t.Fatalf("branding : %d %s", r.Code, r.Body)
	}
	var b map[string]string
	_ = json.Unmarshal(do("GET", "/api/branding", "").Body.Bytes(), &b)
	if b["client"] != "ACME" || b["site"] != "Nord" {
		t.Errorf("branding relu : %v", b)
	}
	if r := do("GET", "/api/reports/inconnu", ""); r.Code != http.StatusNotFound {
		t.Errorf("job inconnu : %d", r.Code)
	}
	j, _ := jm.Submit("net.iperf", nil, "ui", func(ctx context.Context, r *jobs.Reporter) (any, error) { return map[string]int{"x": 1}, nil })
	jm.Wait(j.ID)
	if r := do("GET", "/api/reports/"+j.ID, ""); r.Code != http.StatusUnprocessableEntity {
		t.Errorf("rapport d'un job iperf : %d %s", r.Code, r.Body)
	}
	if r := do("GET", "/api/reports/compare?a="+j.ID, ""); r.Code != http.StatusBadRequest {
		t.Errorf("comparaison sans b : %d", r.Code)
	}
}
