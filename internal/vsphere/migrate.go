package vsphere

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/progress"
	"github.com/vmware/govmomi/vim25/types"
)

// MigrationCheck est le résultat de la prévalidation d'un vMotion de l'appliance vers un hôte.
type MigrationCheck struct {
	Host     string   `json:"host"`
	HostRef  string   `json:"host_ref"`
	OK       bool     `json:"ok"`
	Current  bool     `json:"current"` // l'appliance y est déjà
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// CheckMigration prévalide un vMotion (CheckRelocate) et vérifie l'état de l'hôte, le montage du
// datastore de test et la présence des réseaux de la VM.
func CheckMigration(ctx context.Context, s *Session, vmRef, hostRef types.ManagedObjectReference, dsRef *types.ManagedObjectReference) MigrationCheck {
	pc := property.DefaultCollector(s.Client)
	c := MigrationCheck{HostRef: hostRef.String()}
	var h mo.HostSystem
	if err := pc.RetrieveOne(ctx, hostRef, []string{"name", "parent", "runtime", "summary.config.vmotionEnabled", "datastore", "network"}, &h); err != nil {
		c.Errors = append(c.Errors, err.Error())
		return c
	}
	c.Host = h.Name
	var vm mo.VirtualMachine
	if err := pc.RetrieveOne(ctx, vmRef, []string{"runtime.host", "network", "datastore", "layoutEx.file"}, &vm); err != nil {
		c.Errors = append(c.Errors, err.Error())
		return c
	}
	if vm.Runtime.Host != nil && *vm.Runtime.Host == hostRef {
		c.Current, c.OK = true, true
		return c
	}
	if h.Runtime.ConnectionState != types.HostSystemConnectionStateConnected {
		c.Errors = append(c.Errors, fmt.Sprintf("hôte %s", h.Runtime.ConnectionState))
	}
	if h.Runtime.InMaintenanceMode {
		c.Errors = append(c.Errors, "hôte en maintenance")
	}
	if !h.Summary.Config.VmotionEnabled {
		c.Warnings = append(c.Warnings, "vMotion non activé sur un vmk de l'hôte")
	}
	has := func(list []types.ManagedObjectReference, r types.ManagedObjectReference) bool {
		for _, x := range list {
			if x == r {
				return true
			}
		}
		return false
	}
	// Datastores requis : ceux qui portent la configuration et les disques de la VM. Les datastores qui
	// ne contiennent que le fichier d'échange (.vswp, souvent local à l'hôte) ne le sont pas : vMotion
	// recrée le swap sur la destination.
	needed := requiredDatastores(ctx, s, vm)
	for _, d := range append(needed, derefs(dsRef)...) {
		if !has(h.Datastore, d) {
			var ds mo.Datastore
			name := d.Value
			if pc.RetrieveOne(ctx, d, []string{"name"}, &ds) == nil {
				name = ds.Name
			}
			c.Errors = append(c.Errors, fmt.Sprintf("datastore %s non monté sur l'hôte", name))
		}
	}
	for _, n := range vm.Network {
		if !has(h.Network, n) {
			c.Errors = append(c.Errors, fmt.Sprintf("réseau %s absent de l'hôte", n.Value))
		}
	}
	if len(c.Errors) == 0 {
		spec := types.VirtualMachineRelocateSpec{Host: &hostRef}
		if pool, err := hostPool(ctx, s, h); err == nil {
			spec.Pool = pool
		}
		task, err := object.NewVmProvisioningChecker(s.Client).CheckRelocate(ctx, vmRef, spec)
		if err != nil {
			c.Warnings = append(c.Warnings, fmt.Sprintf("CheckRelocate indisponible : %v", err))
		} else {
			for _, r := range task {
				for _, e := range r.Error {
					c.Errors = append(c.Errors, e.LocalizedMessage)
				}
				for _, w := range r.Warning {
					c.Warnings = append(c.Warnings, w.LocalizedMessage)
				}
			}
		}
	}
	c.OK = len(c.Errors) == 0
	return c
}

func requiredDatastores(ctx context.Context, s *Session, vm mo.VirtualMachine) []types.ManagedObjectReference {
	if vm.LayoutEx == nil || len(vm.LayoutEx.File) == 0 {
		return vm.Datastore
	}
	pc := property.DefaultCollector(s.Client)
	var out []types.ManagedObjectReference
	for _, d := range vm.Datastore {
		var ds mo.Datastore
		if err := pc.RetrieveOne(ctx, d, []string{"name"}, &ds); err != nil {
			out = append(out, d)
			continue
		}
		for _, f := range vm.LayoutEx.File {
			if strings.HasPrefix(f.Name, "["+ds.Name+"]") && f.Type != "swap" && f.Type != "uwswap" && !strings.HasSuffix(f.Name, ".vswp") {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

func derefs(r *types.ManagedObjectReference) []types.ManagedObjectReference {
	if r == nil {
		return nil
	}
	return []types.ManagedObjectReference{*r}
}

func hostPool(ctx context.Context, s *Session, h mo.HostSystem) (*types.ManagedObjectReference, error) {
	if h.Parent == nil {
		return nil, errors.New("hôte sans parent")
	}
	var cr mo.ComputeResource
	if err := property.DefaultCollector(s.Client).RetrieveOne(ctx, *h.Parent, []string{"resourcePool"}, &cr); err != nil {
		return nil, err
	}
	return cr.ResourcePool, nil
}

// Progress : avancement d'une tâche vCenter (0-100).
type Progress func(pct int, msg string)

type sink struct{ fn Progress }

func (p sink) Sink() chan<- progress.Report {
	ch := make(chan progress.Report, 8)
	go func() {
		for r := range ch {
			if p.fn != nil && r.Error() == nil {
				p.fn(int(r.Percentage()), r.Detail())
			}
		}
	}()
	return ch
}

// MigrateSelf est LA fonction qui déplace l'appliance (vMotion de sa propre VM uniquement, §0).
// La tâche vCenter est toujours menée à son terme : le contexte d'annulation n'est pas propagé.
func MigrateSelf(ctx context.Context, s *Session, self *Self, hostRef types.ManagedObjectReference, prog Progress) error {
	vmRef, ok := Ref(self.VM.Ref)
	if !ok {
		return errors.New("référence de l'appliance invalide")
	}
	var h mo.HostSystem
	if err := property.DefaultCollector(s.Client).RetrieveOne(ctx, hostRef, []string{"name", "parent"}, &h); err != nil {
		return err
	}
	pool, err := hostPool(ctx, s, h)
	if err != nil {
		return err
	}
	log.Printf("vsphere: vMotion de l'appliance %s (%s) de %s vers %s", self.VM.Name, vmRef, self.Host.Name, h.Name)
	vm := object.NewVirtualMachine(s.Client, vmRef)
	// RelocateVM sans changement de datastore = vMotion de calcul seul (API recommandée par VMware).
	task, err := vm.Relocate(ctx, types.VirtualMachineRelocateSpec{Host: &hostRef, Pool: pool}, types.VirtualMachineMovePriorityHighPriority)
	if err != nil {
		return fmt.Errorf("vMotion vers %s : %w", h.Name, err)
	}
	info, err := task.WaitForResultEx(context.WithoutCancel(ctx), sink{prog})
	if err != nil {
		if info != nil && info.Error != nil {
			return fmt.Errorf("vMotion vers %s : %s", h.Name, info.Error.LocalizedMessage)
		}
		return fmt.Errorf("vMotion vers %s : %w", h.Name, err)
	}
	log.Printf("vsphere: vMotion vers %s terminé (%s)", h.Name, task.Reference())
	return nil
}

// DRSState décrit le DRS du cluster et l'override éventuel de l'appliance.
type DRSState struct {
	Cluster     string `json:"cluster"`
	ClusterRef  string `json:"cluster_ref"`
	Enabled     bool   `json:"enabled"`
	Default     string `json:"default_behavior"`
	Override    bool   `json:"override"` // un override existe pour l'appliance
	Behavior    string `json:"override_behavior,omitempty"`
	OverrideOn  *bool  `json:"override_enabled,omitempty"`
	NeedsManual bool   `json:"needs_manual"` // DRS déplacerait l'appliance pendant la mesure
}

// DRSOf lit la configuration DRS du cluster de l'appliance.
func DRSOf(ctx context.Context, s *Session, self *Self) (*DRSState, error) {
	if self.Cluster == nil {
		return &DRSState{}, nil
	}
	cref, _ := Ref(self.Cluster.Ref)
	vmRef, _ := Ref(self.VM.Ref)
	cfg, err := object.NewClusterComputeResource(s.Client, cref).Configuration(ctx)
	if err != nil {
		return nil, err
	}
	st := &DRSState{Cluster: self.Cluster.Name, ClusterRef: cref.String()}
	if cfg.DrsConfig.Enabled != nil {
		st.Enabled = *cfg.DrsConfig.Enabled
	}
	st.Default = string(cfg.DrsConfig.DefaultVmBehavior)
	for _, v := range cfg.DrsVmConfig {
		if v.Key == vmRef {
			st.Override, st.Behavior, st.OverrideOn = true, string(v.Behavior), v.Enabled
		}
	}
	behavior := st.Default
	if st.Override && st.Behavior != "" {
		behavior = st.Behavior
	}
	st.NeedsManual = st.Enabled && behavior == string(types.DrsBehaviorFullyAutomated) && !(st.OverrideOn != nil && !*st.OverrideOn)
	return st, nil
}

// SetDRSOverride est LA fonction qui modifie le DRS : uniquement l'override de l'appliance elle-même
// (§0). manual=true pose (ou modifie) un override « manuel » ; restore remet l'état précédent.
func SetDRSOverride(ctx context.Context, s *Session, self *Self, prev *DRSState, manual bool) error {
	if self.Cluster == nil {
		return nil
	}
	cref, _ := Ref(self.Cluster.Ref)
	vmRef, _ := Ref(self.VM.Ref)
	var spec types.ClusterDrsVmConfigSpec
	switch {
	case manual:
		op := types.ArrayUpdateOperationAdd
		if prev != nil && prev.Override {
			op = types.ArrayUpdateOperationEdit
		}
		spec = types.ClusterDrsVmConfigSpec{ArrayUpdateSpec: types.ArrayUpdateSpec{Operation: op},
			Info: &types.ClusterDrsVmConfigInfo{Key: vmRef, Behavior: types.DrsBehaviorManual, Enabled: types.NewBool(true)}}
	case prev != nil && prev.Override:
		spec = types.ClusterDrsVmConfigSpec{ArrayUpdateSpec: types.ArrayUpdateSpec{Operation: types.ArrayUpdateOperationEdit},
			Info: &types.ClusterDrsVmConfigInfo{Key: vmRef, Behavior: types.DrsBehavior(prev.Behavior), Enabled: prev.OverrideOn}}
	default:
		spec = types.ClusterDrsVmConfigSpec{ArrayUpdateSpec: types.ArrayUpdateSpec{Operation: types.ArrayUpdateOperationRemove, RemoveKey: vmRef}}
	}
	what := map[bool]string{true: "pose d'un override DRS manuel", false: "restauration du DRS"}[manual]
	log.Printf("vsphere: %s pour %s dans %s", what, self.VM.Name, self.Cluster.Name)
	req := types.ReconfigureComputeResource_Task{This: cref, Spec: &types.ClusterConfigSpecEx{DrsVmConfigSpec: []types.ClusterDrsVmConfigSpec{spec}}, Modify: true}
	res, err := methods.ReconfigureComputeResource_Task(ctx, s.Client, &req)
	if err != nil {
		return fmt.Errorf("%s : %w", what, err)
	}
	if err := object.NewTask(s.Client, res.Returnval).WaitEx(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("%s : %w", what, err)
	}
	return nil
}

// HostByName retrouve un hôte par nom parmi les hôtes montant le datastore.
func HostByName(hosts []HostInfo, name string) (HostInfo, bool) {
	for _, h := range hosts {
		if strings.EqualFold(h.Name, name) || h.Ref == name {
			return h, true
		}
	}
	return HostInfo{}, false
}
