package vsphere

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

func TestVCSimMigrateAndDRS(t *testing.T) {
	_, s, cleanup := startVCSim(t)
	defer cleanup()
	ctx := context.Background()
	// une VM du cluster (DC0_C0) et un autre hôte du même cluster
	v, _ := view.NewManager(s.Client).CreateContainerView(ctx, s.Client.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"name", "config.uuid", "runtime.host"}, &vms); err != nil {
		t.Fatal(err)
	}
	var vm mo.VirtualMachine
	var other types.ManagedObjectReference
	pc := property.DefaultCollector(s.Client)
	for _, cand := range vms {
		var h mo.HostSystem
		if err := pc.RetrieveOne(ctx, *cand.Runtime.Host, []string{"parent"}, &h); err != nil || h.Parent.Type != "ClusterComputeResource" {
			continue
		}
		var c mo.ClusterComputeResource
		_ = pc.RetrieveOne(ctx, *h.Parent, []string{"host"}, &c)
		for _, x := range c.Host {
			if x != *cand.Runtime.Host {
				vm, other = cand, x
			}
		}
		if other.Value != "" {
			break
		}
	}
	if other.Value == "" {
		t.Skip("pas de cluster à plusieurs hôtes dans le modèle")
	}
	ProductUUID = func() (string, error) { return vm.Config.Uuid, nil }
	self, err := Identify(ctx, s)
	if err != nil || self.Cluster == nil {
		t.Fatalf("identify : %v %+v", err, self)
	}

	c := CheckMigration(ctx, s, vm.Self, other, nil)
	if !c.OK || c.Current {
		t.Fatalf("prévalidation : %+v", c)
	}
	if cur := CheckMigration(ctx, s, vm.Self, *vm.Runtime.Host, nil); !cur.Current {
		t.Fatal("l'hôte courant doit être signalé")
	}

	before, err := DRSOf(ctx, s, self)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetDRSOverride(ctx, s, self, before, true); err != nil {
		t.Fatalf("override : %v", err)
	}
	during, _ := DRSOf(ctx, s, self)
	if !during.Override || during.Behavior != string(types.DrsBehaviorManual) || during.NeedsManual {
		t.Fatalf("pendant : %+v", during)
	}
	if err := MigrateSelf(ctx, s, self, other, nil); err != nil {
		t.Fatalf("vMotion : %v", err)
	}
	after, err := Identify(ctx, s)
	if err != nil || after.Host.Ref != other.String() {
		t.Fatalf("après vMotion : %v %+v", err, after.Host)
	}
	if err := SetDRSOverride(ctx, s, after, before, false); err != nil {
		t.Fatalf("restauration : %v", err)
	}
	restored, _ := DRSOf(ctx, s, after)
	if restored.Override != before.Override {
		t.Fatalf("état DRS non restauré : avant %+v, après %+v", before, restored)
	}
}
