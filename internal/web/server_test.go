package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vigie/internal/auth"
	"vigie/internal/jobs"
	"vigie/internal/store"
	"vigie/internal/testdisk"
	"vigie/internal/vsphere"
)

func TestHealth(t *testing.T) {
	started := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s := &Server{info: Info{Version: "1.2.3", Commit: "abc", BuildDate: "d", Started: started}, now: func() time.Time { return started.Add(90 * time.Second) }, vs: vsphere.NewManager()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, rec.Body.String())
	}
	if got.Status != "ok" || got.Version != "1.2.3" || got.Commit != "abc" {
		t.Errorf("unexpected body: %+v", got)
	}
	if got.UptimeS != 90 {
		t.Errorf("uptime_s = %v, want 90", got.UptimeS)
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	h := New(Info{Started: time.Now()}, Deps{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/health", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestIndex(t *testing.T) {
	h := New(Info{Version: "9.9.9", Started: time.Now()}, Deps{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>") {
		t.Fatalf("index : status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / : %d, attendu 405", rec.Code)
	}
}

type fakeAuth struct{ pw string }

func (f fakeAuth) Verify(s string) bool { return f.pw != "" && s == f.pw }
func (f fakeAuth) IsSet() bool          { return f.pw != "" }

func TestAuthRequired(t *testing.T) {
	h := New(Info{Started: time.Now()}, Deps{Auth: fakeAuth{pw: "secret-1234"}})
	get := func(auth string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/vsphere/status", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := get(""); c != http.StatusUnauthorized {
		t.Fatalf("sans jeton : %d", c)
	}
	if c := get("Bearer mauvais"); c != http.StatusUnauthorized {
		t.Fatalf("mauvais jeton : %d", c)
	}
	if c := get("Bearer secret-1234"); c != http.StatusOK {
		t.Fatalf("bon jeton : %d", c)
	}
	// health reste public
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health : %d", rec.Code)
	}
	// route API inconnue : 404 JSON (après auth)
	req := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
	req.Header.Set("Authorization", "Bearer secret-1234")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "route inconnue") {
		t.Fatalf("route inconnue : %d %s", rec.Code, rec.Body.String())
	}
	// sans connexion vSphere : 409
	req = httptest.NewRequest(http.MethodGet, "/api/self", nil)
	req.Header.Set("Authorization", "Bearer secret-1234")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("self sans connexion : %d", rec.Code)
	}
}

func TestJobsAndTokensAPI(t *testing.T) {
	st, _ := store.New(t.TempDir())
	toks := auth.NewTokens(st)
	h := New(Info{Started: time.Now()}, Deps{Auth: fakeAuth{pw: "secret-1234"}, Tokens: toks, Jobs: jobs.NewManager(st), TestDisk: testdisk.NewService(st)})
	do := func(method, path, auth, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+auth)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("POST", "/api/tokens", "secret-1234", `{"name":"qa"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("création de jeton : %d %s", rec.Code, rec.Body.String())
	}
	var created struct{ Token, ID string }
	json.Unmarshal(rec.Body.Bytes(), &created)
	if rec := do("GET", "/api/jobs", created.Token, ""); rec.Code != http.StatusOK {
		t.Fatalf("jeton refusé : %d", rec.Code)
	}
	if rec := do("POST", "/api/tokens", created.Token, `{"name":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("un jeton ne doit pas créer de jeton : %d", rec.Code)
	}
	// profils : validation immédiate, sans créer de job
	for body, want := range map[string]int{
		`{"profile":"nope"}`:     http.StatusBadRequest,
		`{"profile":"standard"}`: http.StatusBadRequest, // pas de disque de test
		`{"profile":"advanced"}`: http.StatusBadRequest,
		`{"profile":"testdisk.create","datastore":"Datastore:ds-1"}`: http.StatusBadRequest, // pas de session vSphere
	} {
		if rec := do("POST", "/api/jobs", created.Token, body); rec.Code != want {
			t.Fatalf("%s : %d, attendu %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
	if rec := do("GET", "/api/jobs/inconnu", created.Token, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("job inconnu : %d", rec.Code)
	}
	if rec := do("DELETE", "/api/tokens/"+created.ID, "secret-1234", ""); rec.Code != http.StatusOK {
		t.Fatalf("révocation : %d", rec.Code)
	}
	if rec := do("GET", "/api/jobs", created.Token, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("jeton révoqué accepté : %d", rec.Code)
	}
}

func TestJobsSSE(t *testing.T) {
	m := jobs.NewManager(nil)
	srv := httptest.NewServer(New(Info{Started: time.Now()}, Deps{Jobs: m}))
	defer srv.Close()
	release := make(chan struct{})
	j, _ := m.Submit("bench", nil, "test", func(ctx context.Context, r *jobs.Reporter) (any, error) {
		r.Progress(50, "moitié")
		<-release
		return map[string]int{"ok": 1}, nil
	})
	resp, err := http.Get(srv.URL + "/api/jobs/" + j.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %s", ct)
	}
	time.AfterFunc(100*time.Millisecond, func() { close(release) })
	body, _ := io.ReadAll(resp.Body)
	b := string(body)
	for _, want := range []string{"event: progress", "event: result", "event: state", `"state":"succeeded"`, "id: 1\n"} {
		if !strings.Contains(b, want) {
			t.Fatalf("flux SSE sans %q :\n%s", want, b)
		}
	}
}
