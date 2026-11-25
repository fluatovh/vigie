package vsphere

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Self décrit l'appliance telle que vSphere la voit.
type Self struct {
	Method  string      `json:"method"` // uuid, uuid-swapped, mac
	VM      ObjectInfo  `json:"vm"`
	UUID    string      `json:"uuid"`
	Host    HostInfo    `json:"host"`
	Cluster *ObjectInfo `json:"cluster,omitempty"`
}

// ObjectInfo est un objet géré nommé.
type ObjectInfo struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

// Fonctions de lecture du matériel, remplaçables dans les tests.
var (
	ProductUUID = func() (string, error) { return readSys("/sys/class/dmi/id/product_uuid") }
	PrimaryMAC  = func() (string, error) { return readSys("/sys/class/net/eth0/address") }
)

func readSys(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(string(b))), nil
}

// Identify retrouve la VM de l'appliance : par UUID SMBIOS (les deux ordres d'octets), sinon par
// adresse MAC de la première interface.
func Identify(ctx context.Context, s *Session) (*Self, error) {
	vc := s.Client
	var vmRef *types.ManagedObjectReference
	method := ""
	uuid, err := ProductUUID()
	if err == nil && uuid != "" {
		si := object.NewSearchIndex(vc)
		for _, cand := range []struct{ m, u string }{{"uuid", uuid}, {"uuid-swapped", swapUUID(uuid)}} {
			if cand.u == "" {
				continue
			}
			r, err := si.FindByUuid(ctx, nil, cand.u, true, nil)
			if err != nil {
				return nil, fmt.Errorf("recherche par UUID : %w", err)
			}
			if r != nil {
				ref := r.Reference()
				vmRef, method = &ref, cand.m
				break
			}
		}
	}
	if vmRef == nil {
		mac, err := PrimaryMAC()
		if err != nil || mac == "" {
			return nil, errors.New("VM introuvable par UUID et adresse MAC illisible")
		}
		ref, err := findByMAC(ctx, vc, mac)
		if err != nil {
			return nil, err
		}
		vmRef, method = ref, "mac"
	}

	pc := property.DefaultCollector(vc)
	var vm mo.VirtualMachine
	if err := pc.RetrieveOne(ctx, *vmRef, []string{"name", "config.uuid", "runtime.host"}, &vm); err != nil {
		return nil, fmt.Errorf("lecture de la VM : %w", err)
	}
	if vm.Runtime.Host == nil {
		return nil, errors.New("la VM n'est rattachée à aucun hôte (éteinte ?)")
	}
	host, err := hostInfo(ctx, vc, *vm.Runtime.Host)
	if err != nil {
		return nil, err
	}
	self := &Self{Method: method, VM: ObjectInfo{Name: vm.Name, Ref: vmRef.String()}, Host: *host}
	if vm.Config != nil {
		self.UUID = vm.Config.Uuid
	}
	if host.Cluster != nil {
		self.Cluster = host.Cluster
	}
	return self, nil
}

// swapUUID inverse l'ordre des octets des trois premiers groupes (convention SMBIOS little-endian).
func swapUUID(u string) string {
	u = strings.ReplaceAll(u, "-", "")
	raw, err := hex.DecodeString(u)
	if err != nil || len(raw) != 16 {
		return ""
	}
	raw[0], raw[1], raw[2], raw[3] = raw[3], raw[2], raw[1], raw[0]
	raw[4], raw[5] = raw[5], raw[4]
	raw[6], raw[7] = raw[7], raw[6]
	h := hex.EncodeToString(raw)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func findByMAC(ctx context.Context, vc *vimClient, mac string) (*types.ManagedObjectReference, error) {
	m := view.NewManager(vc)
	v, err := m.CreateContainerView(ctx, vc.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	if err != nil {
		return nil, err
	}
	defer v.Destroy(ctx)
	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"config.hardware.device"}, &vms); err != nil {
		return nil, fmt.Errorf("recherche par MAC : %w", err)
	}
	for _, vm := range vms {
		if vm.Config == nil {
			continue
		}
		for _, d := range vm.Config.Hardware.Device {
			if nic, ok := d.(types.BaseVirtualEthernetCard); ok {
				if strings.EqualFold(nic.GetVirtualEthernetCard().MacAddress, mac) {
					ref := vm.Self
					return &ref, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("aucune VM avec l'adresse MAC %s", mac)
}
