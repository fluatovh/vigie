package vsphere

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// RequiredPrivileges est le rôle minimal du §6.5 du plan, par entité.
var RequiredPrivileges = map[string][]string{
	"vm": {
		"System.View", "System.Read",
		"VirtualMachine.Config.AddNewDisk", "VirtualMachine.Config.RemoveDisk",
		"Resource.HotMigrate",
	},
	"datastore": {"System.View", "System.Read", "Datastore.AllocateSpace", "Datastore.Browse", "Datastore.FileManagement"},
	"host":      {"System.View", "System.Read", "Global.Diagnostics"},
	"cluster":   {"System.View", "Host.Inventory.EditCluster"},
}

// PrivilegeReport donne, par entité, l'état de chaque privilège requis.
type PrivilegeReport struct {
	User     string                     `json:"user"`
	Entities map[string]EntityPrivilege `json:"entities"`
	Missing  []string                   `json:"missing"` // « entité:privilège »
}

// EntityPrivilege est le résultat pour une entité.
type EntityPrivilege struct {
	Ref        string          `json:"ref"`
	Privileges map[string]bool `json:"privileges"`
}

// Privileges vérifie les privilèges de la session courante sur les entités données
// (clés : vm, datastore, host, cluster ; les absentes sont ignorées).
func Privileges(ctx context.Context, s *Session, entities map[string]types.ManagedObjectReference) (*PrivilegeReport, error) {
	am := object.NewAuthorizationManager(s.Client)
	rep := &PrivilegeReport{User: s.Username, Entities: map[string]EntityPrivilege{}}
	for kind, ref := range entities {
		privs := RequiredPrivileges[kind]
		if len(privs) == 0 {
			continue
		}
		has, err := am.HasPrivilegeOnEntity(ctx, ref, s.SessionKey, privs)
		if err != nil {
			return nil, fmt.Errorf("privilèges sur %s : %w", kind, err)
		}
		ep := EntityPrivilege{Ref: ref.String(), Privileges: map[string]bool{}}
		for i, p := range privs {
			ok := i < len(has) && has[i]
			ep.Privileges[p] = ok
			if !ok {
				rep.Missing = append(rep.Missing, kind+":"+p)
			}
		}
		rep.Entities[kind] = ep
	}
	return rep, nil
}
