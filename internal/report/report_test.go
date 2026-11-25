package report

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	stdhtml "html"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"vigie/internal/advanced"
	"vigie/internal/analysis"
	"vigie/internal/bench"
	"vigie/internal/jobs"
	"vigie/internal/standard"
	"vigie/internal/tour"
	"vigie/internal/vsphere"
)

func TestFormats(t *testing.T) {
	cases := map[string]string{
		MBps(2310.4):    "2" + thin + "310" + nbsp + "Mo/s",
		IOPS(48213):     "48" + thin + "200" + nbsp + "IO/s",
		IOPS(912.6):     "913" + nbsp + "IO/s",
		Ms(0.4216):      "0,42" + nbsp + "ms",
		Ms(13.24):       "13,2" + nbsp + "ms",
		Ms(142.6):       "143" + nbsp + "ms",
		Pct(12.34):      "+12,3" + nbsp + "%",
		Pct(-3.01):      "−3,0" + nbsp + "%",
		Ratio(1.0374):   "1,037",
		Size(4 << 30):   "4,00" + nbsp + "Gio",
		BlockSize(1024): "1" + nbsp + "Mio",
		BlockSize(4):    "4" + nbsp + "Kio",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestDiff(t *testing.T) {
	if d := diff(100, 120, true); d.Class != "better" || d.Text != "+20,0"+nbsp+"%" {
		t.Errorf("hausse du débit : %+v", d)
	}
	if d := diff(1.0, 2.0, false); d.Class != "worse" {
		t.Errorf("hausse de latence : %+v", d)
	}
	if d := diff(100, 103, true); d.Class != "same" {
		t.Errorf("variation de 3 %% : %+v", d)
	}
}

// ---------- jeux de données synthétiques ----------

func seconds(n int, mbps float64, at time.Time) []bench.Second {
	var out []bench.Second
	for i := 1; i <= n; i++ {
		v := mbps * (1 + 0.05*float64(i%7-3)/3)
		out = append(out, bench.Second{T: i, At: at.Add(time.Duration(i) * time.Second), Bytes: int64(v * 1e6), Ops: int64(v), P99US: 12000})
	}
	return out
}

func buckets(centerUS float64) [][2]uint64 {
	var b [][2]uint64
	for i := -10; i <= 30; i++ {
		ns := uint64(centerUS * 1000 * (1 + float64(i)/20))
		n := uint64(1000 / (1 + absI(i)))
		b = append(b, [2]uint64{ns, n})
	}
	return b
}

func absI(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

func result(kind bench.Kind, bs, qd int, mbps float64, p99us float64, at time.Time) *bench.Result {
	prof := bench.Profile{Pattern: string(kind), BlockKiB: bs, QD: qd, Duration: 80, ZoneBytes: 4<<30 - 1<<20, Direct: true, Sync: true, Data: "aléatoires incompressibles",
		Text: string(kind) + " bloc " + BlockSize(bs) + " QD " + itoa(qd) + ", O_DIRECT+O_SYNC"}
	return &bench.Result{Step: bench.Step{Kind: kind, BlockKiB: bs, QD: qd, Duration: 80}, Started: at, MBps: mbps, IOPS: mbps * 1e6 / float64(bs*1024), Ops: int64(mbps * 80),
		Latency: bench.Latency{P50: p99us / 3, P95: p99us / 1.5, P99: p99us, P999: p99us * 2, Max: p99us * 5, Mean: p99us / 2.5}, CPUPct: 30,
		Profile: prof, Seconds: seconds(80, mbps, at), Buckets: buckets(p99us / 3), ZoneBytes: 4<<30 - 1<<20}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func stdReport(host string, w, r float64, at time.Time, level analysis.Level) *standard.Report {
	th := analysis.DefaultThresholds()
	return &standard.Report{Started: at, Ended: at.Add(5 * time.Minute), Options: standard.Options{BaselineS: 60, WriteS: 80, ReadS: 80, BlockKiB: 1024, QD: 4, Sync: true, Thresholds: th},
		Appliance: "test", Server: &vsphere.ServerInfo{FullName: "VMware vCenter Server 8.0.3", Build: "24322831"},
		Host:      vsphere.HostInfo{Name: host, Version: "8.0.3", Build: "24280767", Cluster: &vsphere.ObjectInfo{Name: "Cluster1"}},
		VM:        vsphere.ObjectInfo{Name: "vigie-dev"},
		Datastore: vsphere.DatastoreInfo{Name: "ssd-001557", Type: "NFS", NFSServer: "172.17.136.11", NFSExport: "/ssd-001557", Capacity: 2 << 40, Free: 1 << 40},
		Path: &vsphere.Path{NFSIP: "172.17.136.11", VMK: vsphere.VMKInfo{Device: "vmk1", IP: "172.17.136.50", MTU: 9000}, Switch: "vDS-test", SwitchType: "distributed",
			Uplinks: []vsphere.Uplink{{Device: "vmnic0", Role: "active", SpeedMb: 50000, Driver: "nmlx5_core"}}},
		Write: result(bench.SeqWrite, 1024, 4, w, 13200, at), Read: result(bench.SeqRead, 1024, 4, r, 6000, at.Add(90*time.Second)),
		TxRatio:  &analysis.Ratio{Value: 1.037, Samples: 3, Reliable: true},
		PerVmnic: []standard.VmnicStats{{Device: "vmnic0", Role: "active", SpeedMb: 50000, TxWriteMBps: w * 1.03, RxReadMBps: r}},
		Verdict: analysis.Verdict{Level: level, Preset: th.Preset, Indicators: []analysis.Indicator{
			{Key: "tx_ratio", Label: "Ratio TX/écrit corrigé", Value: 1.037, Level: analysis.OK, Reliable: true, Why: "1,037"},
			{Key: "nic_errors", Label: "Erreurs/pertes en réception (vmnic NFS)", Level: analysis.OK, Reliable: true, Why: "0"},
			{Key: "nfs_warnings", Label: "Avertissements NFS (vmkernel.log)", Level: analysis.Unknown, Why: "journal non lisible"},
		}},
	}
}

func job(t *testing.T, id, profile string, res any) *jobs.Job {
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return &jobs.Job{ID: id, Profile: profile, State: jobs.Succeeded, Result: b}
}

func advReport(at time.Time, scale float64) *advanced.Report {
	std := stdReport("172.17.136.50", 462*scale, 1215*scale, at, analysis.OK)
	a := &advanced.Report{Started: at, Options: advanced.Options{Standard: std.Options, CeilingMs: 5, WarmupS: 5, MeasureS: 40, QDs: []int{1, 4, 16, 32}}, Standard: std, WorkerCPU: 1}
	for _, pat := range []struct {
		k bench.Kind
		p string
	}{{bench.RandRead, "read"}, {bench.RandWrite, "write"}, {bench.RandMix, "mixed"}} {
		for _, qd := range a.Options.QDs {
			iops := scale * 3000 * float64(qd) / (1 + float64(qd)/12)
			p99 := 0.4 * float64(qd) / scale
			res := result(pat.k, 4, qd, iops*4096/1e6, p99*1000, at)
			res.IOPS = iops
			a.Steps = append(a.Steps, res)
			mm := 2.0
			a.Points = append(a.Points, analysis.Point{Pattern: pat.p, BlockKiB: 4, QD: qd, IOPS: iops, P50Ms: p99 / 3, P99Ms: p99, MismatchPct: &mm})
		}
	}
	a.Useful = analysis.UsefulIOPS(a.Points, 5)
	a.Verdict = std.Verdict
	return a
}

var external = regexp.MustCompile(`(?i)(src|href)\s*=\s*"(https?:|//)|@import|url\(\s*["']?https?:`)

func render(t *testing.T, p *Page) string {
	var buf bytes.Buffer
	if _, err := Render(&buf, p); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if external.MatchString(html) {
		t.Errorf("référence externe dans le rapport : %q", external.FindString(html))
	}
	if !strings.Contains(stdhtml.UnescapeString(html), "default-src 'none'") {
		t.Error("CSP absente du document")
	}
	if strings.Contains(html, " style=\"") {
		t.Error("attribut style en ligne (bloqué par la CSP)")
	}
	if dir := os.Getenv("VIGIE_REPORT_OUT"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "rapport-"+p.Kind+".html"), buf.Bytes(), 0o644)
	}
	return stdhtml.UnescapeString(html)
}

var brand = Branding{Client: "Client Exemple", Site: "Datacenter Nord", Author: "Équipe stockage", Comment: "Mesure demandée après le remplacement des optiques.\n\nSecond paragraphe."}

func TestStandardReport(t *testing.T) {
	at := time.Date(2026, 11, 18, 14, 5, 0, 0, time.UTC)
	j := job(t, "std1", "standard", map[string]any{"runs": []*standard.Report{stdReport("172.17.136.50", 462, 1215, at, analysis.OK)}})
	p, err := Build(j, brand, Meta{Version: "test", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range p.Sections {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "synthese,debit,distribution,sante,methodologie" {
		t.Errorf("sections : %v", ids)
	}
	h := render(t, p)
	for _, want := range []string{"Client Exemple", "Le datastore soutient 462" + nbsp + "Mo/s en écriture séquentielle et 1" + thin + "215" + nbsp + "Mo/s",
		"non mesurées : test avancé requis", "18 novembre 2026 à 14" + nbsp + "h" + nbsp + "05 UTC", "Second paragraphe", "NFS v3", "Non mesuré", "Profil d'I/O"} {
		if !strings.Contains(h, want) {
			t.Errorf("absent du rapport : %q", want)
		}
	}
	if s := Summarize("standard", j.Result); s == nil || s.Host != "172.17.136.50" || s.Level != "OK" {
		t.Errorf("résumé : %+v", s)
	}
}

func TestAdvancedReport(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	p, err := Build(job(t, "adv1", "advanced", advReport(at, 1)), brand, Meta{Version: "test", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sections) != 6 {
		t.Errorf("%d sections, 6 attendues", len(p.Sections))
	}
	h := render(t, p)
	for _, want := range []string{"IOPS utiles", "en 4" + nbsp + "Kio aléatoire mixte 70/30 avec une latence p99 inférieure à 5" + nbsp + "ms", "Une écriture 4 Kio isolée (QD1)", "<svg class=\"xy\"", "Écriture 4 Kio QD1 : la moitié", "plafond 5" + nbsp + "ms", "les IOPS mesurées incluent l'effet du cache"} {
		if !strings.Contains(h, want) {
			t.Errorf("absent du rapport avancé : %q", want)
		}
	}
}

func TestTourReport(t *testing.T) {
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	tr := &tour.Report{Started: at, Options: tour.Options{Hosts: []string{"HostSystem:host-1", "HostSystem:host-2"}, Cycles: 2, DRSOverride: true, ReturnHome: true}, HomeHost: "esx43"}
	var runs []analysis.HostRun
	for c := 1; c <= 2; c++ {
		for _, h := range []struct {
			n string
			w float64
		}{{"esx43", 40}, {"esx44", 476}} {
			r := stdReport(h.n, h.w, 1100, at.Add(time.Duration(c)*10*time.Minute), analysis.OK)
			tr.Legs = append(tr.Legs, tour.Leg{Cycle: c, Host: h.n, Moved: true, MoveS: 2, Report: r})
			runs = append(runs, analysis.HostRun{Host: h.n, Cycle: c, WriteMBps: h.w, ReadMBps: 1100, TxRatio: 1.03, WriteP99: 13, Level: analysis.OK})
		}
	}
	tr.Comparison = analysis.Compare(runs, analysis.DefaultThresholds())
	p, err := Build(job(t, "tour1", "tour", tr), Branding{}, Meta{Version: "test", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	h := render(t, p)
	if !strings.Contains(h, "Comparatif entre hôtes") || !strings.Contains(h, "ALERTE") || !strings.Contains(h, "<svg class=\"bars\"") {
		t.Error("comparatif de tournée incomplet")
	}
	if p.Sections[0].Verdict.Level != "ALERTE" {
		t.Errorf("verdict de la tournée : %s", p.Sections[0].Verdict.Level)
	}
}

func TestCompareReport(t *testing.T) {
	a := job(t, "adv-a", "advanced", advReport(time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), 0.5))
	b := job(t, "adv-b", "advanced", advReport(time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC), 1))
	// ordre inversé : l'avant reste le plus ancien
	p, err := BuildCompare(b, a, brand, Meta{Version: "test", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if p.JobIDs[0] != "adv-a" {
		t.Errorf("avant = %s, attendu adv-a", p.JobIDs[0])
	}
	h := render(t, p)
	for _, want := range []string{"+100,0" + nbsp + "%", "delta better", "amélioration", "IOPS et latence, avant / après", "pointillés : avant"} {
		if !strings.Contains(h, want) {
			t.Errorf("absent du rapport avant/après : %q", want)
		}
	}
	// standard contre avancé : signalé, pas bloqué
	s := job(t, "std", "standard", map[string]any{"runs": []*standard.Report{stdReport("172.17.136.51", 450, 1200, time.Now(), analysis.OK)}})
	p2, err := BuildCompare(a, s, Branding{}, Meta{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	h2 := render(t, p2)
	if !strings.Contains(h2, "Un seul des deux runs est un test avancé") || !strings.Contains(h2, "Hôtes différents") {
		t.Error("différences de conditions non signalées")
	}
}

func TestNoReport(t *testing.T) {
	if _, err := Build(&jobs.Job{ID: "x", Profile: "net.iperf", State: jobs.Succeeded, Result: []byte(`{}`)}, Branding{}, Meta{}); err == nil {
		t.Error("rapport accepté pour un job iperf")
	}
	if _, err := Build(&jobs.Job{ID: "x", Profile: "standard", State: jobs.Failed}, Branding{}, Meta{}); err == nil {
		t.Error("rapport accepté pour un job en échec")
	}
}

func svgURI(s string) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(s))
}

func TestLogo(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	pngURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	good := []string{pngURI, svgURI(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><defs><linearGradient id="g"/></defs><rect width="10" height="10" fill="url(#g)"/><use href="#g"/></svg>`)}
	for _, u := range good {
		if _, err := ValidateLogo(u); err != nil {
			t.Errorf("logo valide refusé : %v", err)
		}
	}
	bad := map[string]string{
		"script":    svgURI(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"onload":    svgURI(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`),
		"foreign":   svgURI(`<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><div/></foreignObject></svg>`),
		"href ext":  svgURI(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="https://evil.example/x.png"/></svg>`),
		"js href":   svgURI(`<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"><text>x</text></a></svg>`),
		"entity":    svgURI(`<!DOCTYPE svg [<!ENTITY a "aaaa">]><svg xmlns="http://www.w3.org/2000/svg">&a;</svg>`),
		"style":     svgURI(`<svg xmlns="http://www.w3.org/2000/svg"><style>@import url(https://evil.example/a.css);</style></svg>`),
		"pas svg":   svgURI(`<html><body/></html>`),
		"gif":       "data:image/gif;base64,R0lGODlhAQABAAAAACw=",
		"faux png":  "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("pas un png")),
		"trop gros": "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, MaxLogoBytes+10)),
		"url":       "https://example.com/logo.png",
	}
	for name, u := range bad {
		if _, err := ValidateLogo(u); err == nil {
			t.Errorf("logo %s accepté", name)
		}
	}
	b := Branding{Client: strings.Repeat("x", 121)}
	if b.Validate() == nil {
		t.Error("client trop long accepté")
	}
}

func TestEscaping(t *testing.T) {
	at := time.Date(2026, 11, 18, 14, 5, 0, 0, time.UTC)
	r := stdReport(`<img src=x onerror=alert(1)>`, 462, 1215, at, analysis.OK)
	j := job(t, "x\"><script>", "standard", map[string]any{"runs": []*standard.Report{r}})
	p, err := Build(j, Branding{Client: "</script><script>alert(1)</script>", Comment: "a & b <b>"}, Meta{Version: "t", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Render(&buf, p); err != nil {
		t.Fatal(err)
	}
	h := buf.String()
	if !strings.Contains(h, "a &amp; b &lt;b&gt;") {
		t.Error("commentaire mal échappé")
	}
	for _, bad := range []string{"<script>alert", "<img src=x", "x\"><script>"} {
		if strings.Contains(h, bad) {
			t.Errorf("texte non échappé dans le rapport : %q", bad)
		}
	}
	if strings.Count(h, "<script") != 3 {
		t.Errorf("%d balises <script>, 3 attendues (données, uPlot, rapport)", strings.Count(h, "<script"))
	}
}
