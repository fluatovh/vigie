package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vigie/internal/testdisk"
)

const wuuid = "6000C29c-aaaa-bbbb-cccc-000000000042"

// fakeGuest : sysfs simulé avec un seul disque sdb de 8 Mio (fichier ordinaire) portant le wwid.
func fakeGuest(t *testing.T) (testdisk.Guest, testdisk.Record) {
	root := t.TempDir()
	g := testdisk.Guest{Sys: filepath.Join(root, "sys"), Proc: filepath.Join(root, "proc"), Dev: filepath.Join(root, "dev")}
	d := filepath.Join(g.Sys, "block/sdb")
	os.MkdirAll(filepath.Join(d, "holders"), 0o755)
	os.MkdirAll(filepath.Join(g.Sys, "devices/0:0:1:0"), 0o755)
	os.Symlink(filepath.Join(g.Sys, "devices/0:0:1:0"), filepath.Join(d, "device"))
	os.WriteFile(filepath.Join(d, "device/wwid"), []byte(testdisk.WWIDFromUUID(wuuid)+"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "device/vendor"), []byte("VMware\n"), 0o644)
	os.WriteFile(filepath.Join(d, "device/model"), []byte("Virtual disk\n"), 0o644)
	os.WriteFile(filepath.Join(d, "size"), []byte("16384\n"), 0o644) // 8 Mio
	os.WriteFile(filepath.Join(d, "dev"), []byte("8:16\n"), 0o644)
	os.MkdirAll(filepath.Join(g.Sys, "class/scsi_host/host0"), 0o755)
	os.MkdirAll(g.Proc, 0o755)
	os.WriteFile(filepath.Join(g.Proc, "mounts"), []byte("tmpfs / tmpfs rw 0 0\n"), 0o644)
	os.MkdirAll(g.Dev, 0o755)
	f, _ := os.Create(filepath.Join(g.Dev, "sdb"))
	f.Truncate(8 << 20)
	f.Close()
	rec := testdisk.Record{BackingUUID: wuuid, SizeBytes: 8 << 20, UnitNumber: 1, FileName: "[ds] x/testdisk.vmdk"}
	// signature initiale (comme après POST /api/testdisk)
	dev, err := g.FindByUUID(wuuid, 0)
	if err != nil {
		t.Fatal(err)
	}
	v := g.Verify(dev, &rec)
	if !v.OK() {
		t.Fatalf("contrôles : %+v", v.Checks)
	}
	if err := testdisk.WriteSignature(v, testdisk.Signature{UUID: dev.UUID, SizeBytes: rec.SizeBytes}); err != nil {
		t.Fatal(err)
	}
	return g, rec
}

func TestWorkerEndToEnd(t *testing.T) {
	g, rec := fakeGuest(t)
	req := Request{Record: rec, Prefill: true, Seed: 5, TestGuest: &g, AllowBuffered: true,
		Steps: []Step{{Kind: SeqWrite, Duration: 1, BlockKiB: 256, QD: 2}, {Kind: RandRead, Duration: 1, BlockKiB: 4, QD: 2}}}
	in, _ := json.Marshal(req)
	var out bytes.Buffer
	if err := RunWorker(context.Background(), bytes.NewReader(in), &out); err != nil {
		t.Fatalf("worker : %v\n%s", err, out.String())
	}
	var types []string
	var steps, prefillDone int
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("ligne : %s", line)
		}
		types = append(types, e.Type)
		if e.Type == "step" && e.Result != nil {
			steps++
			if e.Result.Ops == 0 || e.Result.Errors != 0 {
				t.Fatalf("palier %d : %+v", e.Step, e.Result)
			}
		}
		if e.Type == "prefill" && e.Result != nil {
			prefillDone++
			if e.Result.Bytes != 7<<20 {
				t.Fatalf("prefill : %d octets", e.Result.Bytes)
			}
		}
	}
	if types[0] != "start" || types[1] != "checks" || types[len(types)-1] != "done" || steps != 2 || prefillDone != 1 {
		t.Fatalf("événements : %v", types)
	}
	// la signature porte maintenant le préremplissage, et un second run ne préremplit pas
	dev, _ := g.FindByUUID(wuuid, 0)
	sig, err := testdisk.ReadSignature(dev.Path)
	if err != nil || sig.PrefilledBytes != 7<<20 {
		t.Fatalf("signature : %v %+v", err, sig)
	}
	out.Reset()
	req.Steps = nil
	in, _ = json.Marshal(req)
	if err := RunWorker(context.Background(), bytes.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `"percent":100`) {
		t.Fatal("le préremplissage ne doit pas être refait")
	}
}

func TestWorkerRefusesBadRecord(t *testing.T) {
	g, rec := fakeGuest(t)
	rec.SizeBytes = 16 << 20 // taille fausse : contrôle en échec
	in, _ := json.Marshal(Request{Record: rec, TestGuest: &g, AllowBuffered: true, Steps: []Step{{Kind: SeqRead, Duration: 1}}})
	var out bytes.Buffer
	if err := RunWorker(context.Background(), bytes.NewReader(in), &out); err == nil {
		t.Fatal("le worker doit refuser")
	}
	if !strings.Contains(out.String(), `"type":"error"`) || strings.Contains(out.String(), `"type":"step"`) {
		t.Fatalf("événements : %s", out.String())
	}
	rec.SizeBytes = 8 << 20
	rec.BackingUUID = "6000C29c-0000-0000-0000-000000000000"
	in, _ = json.Marshal(Request{Record: rec, TestGuest: &g, AllowBuffered: true})
	out.Reset()
	if err := RunWorker(context.Background(), bytes.NewReader(in), &out); err == nil {
		t.Fatal("UUID inconnu accepté")
	}
}
