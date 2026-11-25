package testdisk

import (
	"context"
	"crypto/tls"
	"strings"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"vigie/internal/vsphere"
)

func TestVCSimCreateRemove(t *testing.T) {
	model := simulator.VPX()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	srv := model.Service.NewServer()
	defer srv.Close()
	defer model.Remove()
	ctx := context.Background()

	info, err := vsphere.Thumbprint(ctx, srv.URL.String())
	if err != nil {
		t.Fatal(err)
	}
	m := vsphere.NewManager()
	m.Logf = t.Logf
	pw, _ := srv.URL.User.Password()
	sess, err := m.Connect(ctx, vsphere.ConnectParams{URL: srv.URL.String(), Username: srv.URL.User.Username(), Password: pw, Thumbprint: info.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect(ctx)

	v, _ := view.NewManager(sess.Client).CreateContainerView(ctx, sess.Client.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"datastore", "runtime.host"}, &vms); err != nil {
		t.Fatal(err)
	}
	vm := vms[0]
	ds := vm.Datastore[0]
	before, _ := object.NewVirtualMachine(sess.Client, vm.Self).Device(ctx)
	nDisks := len(before.SelectByType(&types.VirtualDisk{}))

	var progressed int
	rec, err := Create(ctx, sess, vm.Self, ds, 1, func(p int, msg string) { progressed++ })
	if err != nil {
		t.Fatalf("Create : %v", err)
	}
	if rec.SizeBytes != 1<<30 || rec.DeviceKey == 0 || rec.FileName == "" || rec.VMRef != vm.Self.String() {
		t.Fatalf("record : %+v", rec)
	}
	if !strings.HasPrefix(rec.FileName, "["+rec.DatastoreName+"] vigie-") || !strings.HasSuffix(rec.FileName, "/testdisk.vmdk") || rec.Folder == "" {
		t.Fatalf("le VMDK doit être dans [ds] vigie-<vm>/testdisk.vmdk : %s", rec.FileName)
	}
	after, _ := object.NewVirtualMachine(sess.Client, vm.Self).Device(ctx)
	if len(after.SelectByType(&types.VirtualDisk{})) != nDisks+1 {
		t.Fatal("le disque n'a pas été ajouté")
	}
	disk, err := Lookup(ctx, sess, rec)
	if err != nil || disk == nil || disk.Key != rec.DeviceKey {
		t.Fatalf("Lookup : %v %+v", err, disk)
	}
	// Un enregistrement falsifié (mauvais UUID pour cette clé) ne doit jamais désigner le disque.
	bad := *rec
	bad.BackingUUID = "6000C29c-0000-0000-0000-000000000000"
	if _, err := Lookup(ctx, sess, &bad); err == nil {
		t.Fatal("Lookup doit refuser une clé dont l'UUID ne correspond pas")
	}
	if err := Remove(ctx, sess, &bad, nil); err == nil {
		t.Fatal("Remove doit refuser un enregistrement incohérent")
	}
	if err := Remove(ctx, sess, rec, nil); err != nil {
		t.Fatalf("Remove : %v", err)
	}
	final, _ := object.NewVirtualMachine(sess.Client, vm.Self).Device(ctx)
	if len(final.SelectByType(&types.VirtualDisk{})) != nDisks {
		t.Fatal("le disque n'a pas été supprimé")
	}
	if err := Remove(ctx, sess, rec, nil); err != nil {
		t.Fatalf("Remove idempotent : %v", err)
	}
	if sizeErr := func() error { _, err := Create(ctx, sess, vm.Self, ds, 65, nil); return err }(); sizeErr == nil {
		t.Fatal("65 Go doit être refusé")
	}
}
