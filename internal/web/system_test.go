package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vigie/internal/auth"
	"vigie/internal/store"
	"vigie/internal/sysconf"
)

// Les routes système ne répondent qu'au socket de la console, jamais en HTTP(S), même authentifié.
func TestSystemConsoleOnly(t *testing.T) {
	st, _ := store.New(t.TempDir())
	pw := auth.New(st)
	_ = pw.Set("secret-123")
	ran := false
	sys := &sysconf.System{Root: t.TempDir(), Data: st.Dir, Run: func([]byte, string, ...string) ([]byte, error) { ran = true; return nil, nil }}
	h := New(Info{Started: time.Now()}, Deps{Auth: pw, Settings: NewSettings(st), SysConf: sys})
	for _, c := range []struct{ m, p, b string }{
		{"GET", "/api/system/network", ""},
		{"PUT", "/api/system/network", `{"mode":"dhcp"}`},
		{"PUT", "/api/system/password", `{"password":"autre-mdp-1"}`},
		{"POST", "/api/system/power", `{"action":"reboot"}`},
		{"GET", "/api/console", ""},
	} {
		req := httptest.NewRequest(c.m, c.p, strings.NewReader(c.b))
		req.Header.Set("Authorization", "Bearer secret-123")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s en HTTP : %d", c.m, c.p, rec.Code)
		}
	}
	if ran || !pw.Verify("secret-123") {
		t.Fatal("une opération système a été exécutée hors console")
	}
}
