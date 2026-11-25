package testdisk

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vigie/internal/store"
)

const uuidC = "6000C29d-1111-2222-3333-444455556666"

// hotAdd simule l'ajout à chaud d'un disque sdc (unité SCSI 2) de la taille donnée.
func hotAdd(t *testing.T, g Guest, size int64, content []byte) string {
	t.Helper()
	d := filepath.Join(g.Sys, "block", "sdc")
	os.MkdirAll(filepath.Join(d, "holders"), 0o755)
	os.MkdirAll(filepath.Join(g.Sys, "devices", "0:0:2:0"), 0o755)
	os.Symlink(filepath.Join(g.Sys, "devices", "0:0:2:0"), filepath.Join(d, "device"))
	os.WriteFile(filepath.Join(d, "device/wwid"), []byte(WWIDFromUUID(uuidC)+"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "device/vendor"), []byte("VMware  \n"), 0o644)
	os.WriteFile(filepath.Join(d, "device/model"), []byte("Virtual disk    \n"), 0o644)
	os.WriteFile(filepath.Join(d, "size"), []byte(itoa(size/512)+"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "dev"), []byte("8:32\n"), 0o644)
	os.WriteFile(filepath.Join(g.Sys, "class/scsi_host/host0/proc_name"), []byte("vmw_pvscsi\n"), 0o644)
	p := filepath.Join(g.Dev, "sdc")
	f, _ := os.Create(p)
	f.Truncate(size)
	if content != nil {
		f.WriteAt(content, 0)
	}
	f.Close()
	return p
}

func localService(t *testing.T) (*Service, Guest) {
	g, _ := fakeGuest(t, uuidB, 1<<30)
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Store: st, Guest: g}, g
}

func TestLocalAdopt(t *testing.T) {
	s, g := localService(t)
	if _, err := s.LocalScan(0); err == nil {
		t.Fatal("scan accepté sans préparation")
	}
	if _, err := s.LocalPrepare(); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.LocalScan(0); len(c) != 0 {
		t.Fatalf("aucun disque ajouté, %d candidat(s) (sdb existait avant)", len(c))
	}
	path := hotAdd(t, g, 4<<30, nil)
	c, err := s.LocalScan(0)
	if err != nil || len(c) != 1 || c[0].Device.Name != "sdc" {
		t.Fatalf("candidats : %+v %v", c, err)
	}
	if !c[0].OK || c[0].Unit != 2 || c[0].Controller != "vmw_pvscsi" {
		t.Fatalf("candidat refusé ou mal décrit : %+v", c[0])
	}
	if _, err := s.LocalAdopt("sdc", "sdb", ""); err == nil {
		t.Fatal("confirmation erronée acceptée")
	}
	if _, err := s.LocalAdopt("sdb", "sdb", ""); err == nil {
		t.Fatal("adoption de sdb (présent avant la demande) acceptée")
	}
	if b := head(path, 8); !bytes.Equal(b, make([]byte, 8)) {
		t.Fatal("écriture malgré un refus")
	}
	st, err := s.LocalAdopt("sdc", "sdc", "ssd-001557")
	if err != nil || !st.Ready {
		t.Fatalf("adoption : %v %+v", err, st)
	}
	if string(head(path, 5)) != "VIGIE" {
		t.Fatal("signature absente après adoption")
	}
	rec := s.Record()
	if rec == nil || rec.Mode != ModeLocal || rec.UnitNumber != 2 || rec.DatastoreName != "ssd-001557" {
		t.Fatalf("enregistrement : %+v", rec)
	}
	// le chemin du worker (FindByUUID + Verify + signature) accepte le disque adopté
	dev, err := g.FindByUUID(rec.BackingUUID, 0)
	if err != nil {
		t.Fatal(err)
	}
	v := g.Verify(dev, rec)
	sig, serr := ReadSignature(dev.Path)
	VerifySignature(v, sig, serr)
	if !v.OK() {
		t.Fatalf("revérification du worker : %v", failed(v))
	}
	if s.LocalExpect() != nil {
		t.Error("instantané conservé après adoption")
	}
	if err := s.Delete(nil, nil, nil); err == nil {
		t.Error("suppression vCenter acceptée pour un disque local")
	}
	if err := s.LocalRelease(); err != nil || s.Record() != nil {
		t.Fatalf("libération : %v", err)
	}
	if string(head(path, 5)) != "VIGIE" {
		t.Error("la libération ne doit rien écrire")
	}
	// réadoption du même disque : sa propre signature est acceptée
	s.LocalPrepare()
	os.RemoveAll(filepath.Join(g.Sys, "block", "sdc"))
	s.LocalPrepare()
	hotAddKeep(t, g)
	if st, err := s.LocalAdopt("sdc", "sdc", ""); err != nil || !st.Ready {
		t.Fatalf("réadoption : %v", err)
	}
}

// head lit les n premiers octets d'un fichier (les « disques » font plusieurs Gio, creux).
func head(path string, n int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b := make([]byte, n)
	f.ReadAt(b, 0)
	return b
}

// hotAddKeep remet sdc dans sysfs sans toucher au contenu du « périphérique ».
func hotAddKeep(t *testing.T, g Guest) {
	fi, _ := os.Stat(filepath.Join(g.Dev, "sdc"))
	hotAdd(t, g, fi.Size(), head(filepath.Join(g.Dev, "sdc"), 4096))
}

func TestLocalRefusals(t *testing.T) {
	cases := map[string]struct {
		size    int64
		content []byte
		want    string
	}{
		"MBR":         {4 << 30, append(make([]byte, 510), 0x55, 0xAA), "table de partitions MBR"},
		"ext4":        {4 << 30, append(make([]byte, 1080), 0x53, 0xEF), "ext2/3/4"},
		"trop petit":  {1 << 30, nil, "taille suffisante"},
		"autre vigie": {4 << 30, Signature{UUID: uuidA}.Encode(), "signature vigie d'un autre disque"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, g := localService(t)
			s.LocalPrepare()
			path := hotAdd(t, g, tc.size, tc.content)
			before := head(path, 1<<20)
			c, _ := s.LocalScan(0)
			if len(c) != 1 || c[0].OK {
				t.Fatalf("candidat accepté : %+v", c)
			}
			var all []string
			for _, ch := range c[0].Checks {
				if !ch.OK {
					all = append(all, ch.Name+" : "+ch.Detail)
				}
			}
			if !strings.Contains(strings.Join(all, " | "), tc.want) {
				t.Errorf("refus attendu %q, obtenu %v", tc.want, all)
			}
			if _, err := s.LocalAdopt("sdc", "sdc", ""); err == nil {
				t.Fatal("adoption acceptée")
			}
			if !bytes.Equal(before, head(path, 1<<20)) {
				t.Fatal("le disque refusé a été modifié")
			}
		})
	}
}

func TestDerivedUUID(t *testing.T) {
	a := UUIDFromWWID("t10.QEMU    QEMU HARDDISK    drive-scsi1")
	b := UUIDFromWWID("t10.QEMU    QEMU HARDDISK    drive-scsi2")
	if a == "" || a == b || len(a) != 36 || a != UUIDFromWWID("t10.QEMU    QEMU HARDDISK    drive-scsi1") {
		t.Fatalf("uuid dérivé : %q %q", a, b)
	}
	if UUIDFromWWID("naa.6000c2929ca7d8202e785152b35439b5") != "6000c292-9ca7-d820-2e78-5152b35439b5" {
		t.Fatal("NAA VMware")
	}
}
