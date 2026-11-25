package web

import (
	"crypto/tls"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"vigie/internal/store"
	webui "vigie/web"
)

func TestStaticAndCSP(t *testing.T) {
	h := New(Info{Started: time.Now()}, Deps{})
	for _, p := range []string{"/", "/app.js", "/style.css", "/vendor/uPlot.iife.min.js", "/vendor/uPlot.min.css", "/favicon.svg", "/une/route/front"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s : %d", p, rec.Code)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "unsafe") {
			t.Fatalf("%s : CSP %q", p, csp)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s : en-têtes de sécurité absents", p)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `<script src="i18n.js">`) {
		t.Fatal("index.html attendu sur /")
	}
}

// Aucune ressource externe (§0) : aucun chargement (src, href, url(), @import, fetch, EventSource,
// import) vers une URL absolue dans les fichiers embarqués. Les espaces de noms XML du W3C ne sont
// jamais chargés.
func TestNoExternalResources(t *testing.T) {
	re := regexp.MustCompile(`(?i)(src\s*=|href\s*=|url\(|@import|fetch\(|EventSource\(|import\s*\(?)\s*["']?\s*(https?:)?//[a-z0-9.-]+`)
	_ = fs.WalkDir(webui.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(webui.FS, p)
		for _, m := range re.FindAllString(string(b), -1) {
			if strings.Contains(m, "www.w3.org") {
				continue
			}
			t.Errorf("%s : référence externe %q", p, m)
		}
		if strings.HasSuffix(p, ".html") && (strings.Contains(string(b), "<script>") || strings.Contains(string(b), "style=") || strings.Contains(string(b), "onclick=")) {
			t.Errorf("%s : script, style ou gestionnaire en ligne (incompatible avec la CSP)", p)
		}
		return nil
	})
}

func TestSessionLoginCSRF(t *testing.T) {
	h := New(Info{Started: time.Now()}, Deps{Auth: fakeAuth{pw: "secret-1234"}})
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	c := srv.Client()
	post := func(path, body string, hdr map[string]string, cookie *http.Cookie) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		return r
	}
	if r := post("/api/login", `{"password":"faux"}`, nil, nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("mauvais mot de passe : %d", r.StatusCode)
	}
	r := post("/api/login", `{"password":"secret-1234"}`, nil, nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("login : %d", r.StatusCode)
	}
	var ck *http.Cookie
	for _, k := range r.Cookies() {
		if k.Name == sessionCookie {
			ck = k
		}
	}
	if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie : %+v", ck)
	}
	// GET avec le cookie : accepté
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/vsphere/status", nil)
	req.AddCookie(ck)
	if g, _ := c.Do(req); g.StatusCode != http.StatusOK {
		t.Fatalf("GET par cookie : %d", g.StatusCode)
	}
	// POST par cookie sans X-Vigie : refusé (CSRF)
	if r := post("/api/vsphere/disconnect", "", nil, ck); r.StatusCode != http.StatusForbidden {
		t.Fatalf("POST sans en-tête anti-CSRF : %d", r.StatusCode)
	}
	if r := post("/api/vsphere/disconnect", "", map[string]string{csrfHeader: "1"}, ck); r.StatusCode != http.StatusOK {
		t.Fatalf("POST avec en-tête : %d", r.StatusCode)
	}
	// déconnexion : le cookie ne vaut plus rien
	post("/api/logout", "", map[string]string{csrfHeader: "1"}, ck)
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/vsphere/status", nil)
	req.AddCookie(ck)
	if g, _ := c.Do(req); g.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie après déconnexion : %d", g.StatusCode)
	}
}

func TestLoginThrottle(t *testing.T) {
	if testing.Short() {
		t.Skip("lent (1 s par échec)")
	}
	h := New(Info{Started: time.Now()}, Deps{Auth: fakeAuth{pw: "secret-1234"}})
	code := 0
	for i := 0; i < 6; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"x"}`)))
		code = rec.Code
	}
	if code != http.StatusTooManyRequests {
		t.Fatalf("6e tentative : %d, attendu 429", code)
	}
}

func TestRedirectHandler(t *testing.T) {
	h := RedirectHandler(New(Info{Started: time.Now()}, Deps{}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://10.0.0.5:80/history?x=1", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "https://10.0.0.5/history?x=1" {
		t.Fatalf("redirection : %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://10.0.0.5/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health en HTTP : %d", rec.Code)
	}
}

func TestCertPersistence(t *testing.T) {
	st, _ := store.New(t.TempDir())
	c1, fp1, err := LoadOrCreateCert(st)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := LoadOrCreateCert(st)
	if err != nil || fp1 != fp2 || len(fp1) != 95 {
		t.Fatalf("empreinte instable : %s / %s", fp1, fp2)
	}
	if _, err := tls.X509KeyPair([]byte{}, []byte{}); err == nil || c1.Leaf == nil && len(c1.Certificate) == 0 {
		t.Fatal("certificat vide")
	}
}
