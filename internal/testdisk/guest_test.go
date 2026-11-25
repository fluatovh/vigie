package testdisk

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	uuidA = "6000C292-9ca7-d820-2e78-5152b35439b5"
	uuidB = "6000C29c-aaaa-bbbb-cccc-000000000001"
)

// fakeGuest construit une arborescence sysfs/procfs minimale : sda = disque système (monté),
// sdb = candidat au disque de test.
func fakeGuest(t *testing.T, sdbUUID string, sdbSize int64) (Guest, string) {
	t.Helper()
	root := t.TempDir()
	g := Guest{Sys: filepath.Join(root, "sys"), Proc: filepath.Join(root, "proc"), Dev: filepath.Join(root, "dev")}
	mk := func(name, wwid string, sectors int64, majmin, hctl string) {
		d := filepath.Join(g.Sys, "block", name)
		os.MkdirAll(filepath.Join(d, "holders"), 0o755)
		os.MkdirAll(filepath.Join(g.Sys, "devices", hctl), 0o755)
		os.Symlink(filepath.Join(g.Sys, "devices", hctl), filepath.Join(d, "device"))
		os.WriteFile(filepath.Join(d, "device/wwid"), []byte(wwid+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "device/vendor"), []byte("VMware  \n"), 0o644)
		os.WriteFile(filepath.Join(d, "device/model"), []byte("Virtual disk    \n"), 0o644)
		os.WriteFile(filepath.Join(d, "size"), []byte(itoa(sectors)+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "dev"), []byte(majmin+"\n"), 0o644)
	}
	mk("sda", WWIDFromUUID(uuidA), 1048576, "8:0", "0:0:0:0")
	os.MkdirAll(filepath.Join(g.Sys, "block/sda/sda1"), 0o755)
	os.MkdirAll(filepath.Join(g.Sys, "block/sda/sda2"), 0o755)
	mk("sdb", WWIDFromUUID(sdbUUID), sdbSize/512, "8:16", "0:0:1:0")
	os.MkdirAll(filepath.Join(g.Sys, "class/scsi_host/host0"), 0o755)
	os.MkdirAll(g.Proc, 0o755)
	os.WriteFile(filepath.Join(g.Proc, "mounts"), []byte("/dev/sda1 /media/sda1 vfat ro 0 0\n/dev/sda2 /data ext4 rw 0 0\ntmpfs / tmpfs rw 0 0\n"), 0o644)
	os.MkdirAll(g.Dev, 0o755)
	// le « périphérique » sdb est un fichier ordinaire de la taille voulue
	f, _ := os.Create(filepath.Join(g.Dev, "sdb"))
	f.Truncate(sdbSize)
	f.Close()
	return g, root
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func record(uuid string, size int64) *Record {
	return &Record{VMRef: "VirtualMachine:vm-1", BackingUUID: uuid, SizeBytes: size, DeviceKey: 2001, UnitNumber: 1, FileName: "[ds] vm/vm_1.vmdk"}
}

func failed(v *Verified) []string {
	var out []string
	for _, c := range v.Checks {
		if !c.OK {
			out = append(out, c.Name)
		}
	}
	return out
}

func TestVerifyOK(t *testing.T) {
	g, _ := fakeGuest(t, uuidB, 1<<30)
	dev, err := g.FindByUUID(uuidB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Name != "sdb" || dev.UUID != strings.ToLower(uuidB) || dev.HCTL != "0:0:1:0" {
		t.Fatalf("device : %+v", dev)
	}
	v := g.Verify(dev, record(uuidB, 1<<30))
	if !v.OK() {
		t.Fatalf("contrôles en échec : %v", failed(v))
	}
}

func TestVerifyRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, g Guest, root string) (*Device, *Record)
		want  string
	}{
		{"aucun enregistrement", func(t *testing.T, g Guest, _ string) (*Device, *Record) {
			d, _ := g.FindByUUID(uuidB, 0)
			return d, nil
		}, "ajouté par l'appliance"},
		{"mauvais UUID", func(t *testing.T, g Guest, _ string) (*Device, *Record) {
			d, _ := g.FindByUUID(uuidB, 0)
			return d, record(uuidA, 1<<30)
		}, "numéro de série = UUID du VMDK"},
		{"mauvaise unité SCSI", func(t *testing.T, g Guest, _ string) (*Device, *Record) {
			d, _ := g.FindByUUID(uuidB, 0)
			r := record(uuidB, 1<<30)
			r.UnitNumber = 3
			return d, r
		}, "unité SCSI attendue"},
		{"monté", func(t *testing.T, g Guest, _ string) (*Device, *Record) {
			f, _ := os.OpenFile(filepath.Join(g.Proc, "mounts"), os.O_APPEND|os.O_WRONLY, 0)
			f.WriteString("/dev/sdb /mnt/x ext4 rw 0 0\n")
			f.Close()
			d, _ := g.FindByUUID(uuidB, 0)
			return d, record(uuidB, 1<<30)
		}, "non monté"},
		{"partitionné", func(t *testing.T, g Guest, _ string) (*Device, *Record) {
			os.MkdirAll(filepath.Join(g.Sys, "block/sdb/sdb1"), 0o755)
			d, _ := g.FindByUUID(uuidB, 0)
			return d, record(uuidB, 1<<30)
		}, "sans partition"},
		{"holder (LVM)", func(t *testing.T, g Guest, _ string) (*Device, *Record) {
			os.MkdirAll(filepath.Join(g.Sys, "block/sdb/holders/dm-0"), 0o755)
			d, _ := g.FindByUUID(uuidB, 0)
			return d, record(uuidB, 1<<30)
		}, "sans utilisateur (LVM, md…)"},
		{"disque système", func(t *testing.T, g Guest, _ string) (*Device, *Record) {
			d, _ := g.FindByUUID(uuidA, 0) // sda, qui porte /media et /data
			r := record(uuidA, 512<<20)
			r.UnitNumber = 0
			return d, r
		}, "différent du disque système"},
		{"taille", func(t *testing.T, g Guest, _ string) (*Device, *Record) {
			d, _ := g.FindByUUID(uuidB, 0)
			return d, record(uuidB, 2<<30)
		}, "taille attendue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, root := fakeGuest(t, uuidB, 1<<30)
			dev, rec := tc.setup(t, g, root)
			v := g.Verify(dev, rec)
			if v.OK() {
				t.Fatal("les contrôles auraient dû échouer")
			}
			f := failed(v)
			found := false
			for _, n := range f {
				if n == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("contrôle %q attendu en échec, échecs : %v", tc.want, f)
			}
			// et surtout : aucune écriture possible
			if err := WriteSignature(v, Signature{UUID: dev.UUID}); err == nil {
				t.Fatal("WriteSignature doit refuser un périphérique non vérifié")
			}
		})
	}
}

func TestFindByUUIDAbsent(t *testing.T) {
	g, _ := fakeGuest(t, uuidB, 1<<30)
	if _, err := g.FindByUUID("6000C29c-0000-0000-0000-000000000099", 0); err == nil {
		t.Fatal("un UUID absent doit être une erreur")
	}
}

func TestSignatureRoundTrip(t *testing.T) {
	s := Signature{UUID: strings.ToLower(uuidB), CreatedAt: time.Unix(1700000000, 0).UTC(), SizeBytes: 1 << 30, PrefilledBytes: 1 << 29, PrefilledAt: time.Unix(1700000100, 0).UTC()}
	got, err := Decode(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if *got != s {
		t.Fatalf("roundtrip : %+v != %+v", *got, s)
	}
	buf := s.Encode()
	buf[20] ^= 0xff
	if _, err := Decode(buf); err == nil {
		t.Fatal("CRC invalide non détecté")
	}
	if _, err := Decode(make([]byte, SignatureSize)); err == nil {
		t.Fatal("bloc vide accepté")
	}
}

func TestWriteSignatureRefusesWrongUUID(t *testing.T) {
	g, _ := fakeGuest(t, uuidB, 1<<30)
	dev, _ := g.FindByUUID(uuidB, 0)
	v := g.Verify(dev, record(uuidB, 1<<30))
	if !v.OK() {
		t.Fatal(failed(v))
	}
	if err := WriteSignature(v, Signature{UUID: uuidA}); err == nil {
		t.Fatal("signature d'un autre UUID acceptée")
	}
	if err := WriteSignature(nil, Signature{UUID: uuidB}); err == nil {
		t.Fatal("Verified nil accepté")
	}
}

func TestUUIDWWID(t *testing.T) {
	if WWIDFromUUID(uuidA) != "naa.6000c2929ca7d8202e785152b35439b5" {
		t.Fatal(WWIDFromUUID(uuidA))
	}
	if UUIDFromWWID("naa.6000c2929ca7d8202e785152b35439b5") != strings.ToLower(uuidA) {
		t.Fatal(UUIDFromWWID("naa.6000c2929ca7d8202e785152b35439b5"))
	}
	// hors VMware (t10.…), l'UUID est dérivé du wwid (phase P) ; un NAA tronqué reste refusé
	if u := UUIDFromWWID("t10.ATA_QEMU"); len(u) != 36 || UUIDFromWWID("naa.123") != "" {
		t.Fatal("wwid non NAA : " + u)
	}
	if UUIDFromWWID("naa.6000c29c0000d15c") != "6000c29c0000d15c" || WWIDFromUUID("6000c29c0000d15c") != "naa.6000c29c0000d15c" {
		t.Fatal("NAA de 8 octets (QEMU)")
	}
}
