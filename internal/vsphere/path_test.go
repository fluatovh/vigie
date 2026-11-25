package vsphere

import (
	"net"
	"testing"

	"github.com/vmware/govmomi/vim25/types"
)

func sampleNet() *types.HostNetworkInfo {
	return &types.HostNetworkInfo{
		Vswitch: []types.HostVirtualSwitch{
			{Name: "vSwitch0", Pnic: []string{"key-vim.host.PhysicalNic-vmnic0", "key-vim.host.PhysicalNic-vmnic1"},
				Spec: types.HostVirtualSwitchSpec{Policy: &types.HostNetworkPolicy{NicTeaming: &types.HostNicTeamingPolicy{
					NicOrder: &types.HostNicOrderPolicy{ActiveNic: []string{"vmnic0"}, StandbyNic: []string{"vmnic1"}}}}}},
			{Name: "vSwitch1", Pnic: []string{"key-vim.host.PhysicalNic-vmnic2", "key-vim.host.PhysicalNic-vmnic3"}},
		},
		Portgroup: []types.HostPortGroup{
			{Spec: types.HostPortGroupSpec{Name: "Management Network", VswitchName: "vSwitch0"}},
			{Spec: types.HostPortGroupSpec{Name: "NFS", VswitchName: "vSwitch1", Policy: types.HostNetworkPolicy{
				NicTeaming: &types.HostNicTeamingPolicy{NicOrder: &types.HostNicOrderPolicy{ActiveNic: []string{"vmnic2"}}}}}},
		},
		Pnic: []types.PhysicalNic{
			{Key: "key-vim.host.PhysicalNic-vmnic0", Device: "vmnic0", LinkSpeed: &types.PhysicalNicLinkInfo{SpeedMb: 1000, Duplex: true}},
			{Key: "key-vim.host.PhysicalNic-vmnic1", Device: "vmnic1", LinkSpeed: &types.PhysicalNicLinkInfo{SpeedMb: 1000, Duplex: true}},
			{Key: "key-vim.host.PhysicalNic-vmnic2", Device: "vmnic2", LinkSpeed: &types.PhysicalNicLinkInfo{SpeedMb: 10000, Duplex: true}, Driver: "ixgben"},
			{Key: "key-vim.host.PhysicalNic-vmnic3", Device: "vmnic3"}, // lien down
		},
		Vnic: []types.HostVirtualNic{
			{Device: "vmk0", Portgroup: "Management Network", Spec: types.HostVirtualNicSpec{Ip: &types.HostIpConfig{IpAddress: "10.0.0.5", SubnetMask: "255.255.255.0"}, Mtu: 1500}},
			{Device: "vmk1", Portgroup: "NFS", Spec: types.HostVirtualNicSpec{Ip: &types.HostIpConfig{IpAddress: "192.168.148.10", SubnetMask: "255.255.255.0"}, Mtu: 9000}},
		},
		IpRouteConfig: &types.HostIpRouteConfig{DefaultGateway: "10.0.0.1"},
	}
}

func TestComputePathSameSubnet(t *testing.T) {
	p, err := ComputePath(sampleNet(), net.ParseIP("192.168.148.24"))
	if err != nil {
		t.Fatal(err)
	}
	if p.VMK.Device != "vmk1" || p.Via != "subnet" || p.Switch != "vSwitch1" || p.Portgroup != "NFS" || p.SwitchType != "standard" {
		t.Fatalf("chemin inattendu : %+v", p)
	}
	if len(p.Uplinks) != 2 || p.Uplinks[0].Device != "vmnic2" || p.Uplinks[0].Role != "active" || p.Uplinks[0].SpeedMb != 10000 ||
		p.Uplinks[1].Device != "vmnic3" || p.Uplinks[1].Role != "unused" || p.Uplinks[1].LinkUp {
		t.Fatalf("uplinks inattendus : %+v", p.Uplinks)
	}
	if p.VMK.MTU != 9000 {
		t.Fatalf("MTU : %d", p.VMK.MTU)
	}
}

func TestComputePathViaGateway(t *testing.T) {
	p, err := ComputePath(sampleNet(), net.ParseIP("172.16.5.5"))
	if err != nil {
		t.Fatal(err)
	}
	if p.VMK.Device != "vmk0" || p.Via != "gateway" || p.Switch != "vSwitch0" {
		t.Fatalf("chemin inattendu : %+v", p)
	}
	// politique héritée du vSwitch : vmnic0 actif, vmnic1 standby
	if len(p.Uplinks) != 2 || p.Uplinks[0].Role != "active" || p.Uplinks[1].Role != "standby" {
		t.Fatalf("uplinks inattendus : %+v", p.Uplinks)
	}
}

func TestComputePathNoRoute(t *testing.T) {
	n := sampleNet()
	n.IpRouteConfig = nil
	if _, err := ComputePath(n, net.ParseIP("172.16.5.5")); err == nil {
		t.Fatal("une absence de route doit être une erreur")
	}
}

func TestComputePathDistributed(t *testing.T) {
	n := sampleNet()
	n.Vnic = append(n.Vnic, types.HostVirtualNic{Device: "vmk2", Spec: types.HostVirtualNicSpec{
		Ip:                     &types.HostIpConfig{IpAddress: "10.10.10.2", SubnetMask: "255.255.255.0"},
		DistributedVirtualPort: &types.DistributedVirtualSwitchPortConnection{SwitchUuid: "50 2e", PortgroupKey: "dvportgroup-7"}}})
	n.ProxySwitch = []types.HostProxySwitch{{DvsUuid: "50 2e", DvsName: "dvs-nfs", Pnic: []string{"key-vim.host.PhysicalNic-vmnic2"}}}
	p, err := ComputePath(n, net.ParseIP("10.10.10.50"))
	if err != nil {
		t.Fatal(err)
	}
	if p.SwitchType != "distributed" || p.Switch != "dvs-nfs" || len(p.Uplinks) != 1 || p.Uplinks[0].Device != "vmnic2" {
		t.Fatalf("chemin vDS inattendu : %+v", p)
	}
}

func TestSwapUUID(t *testing.T) {
	got := swapUUID("4213a1b2-c3d4-e5f6-0708-090a0b0c0d0e")
	if got != "b2a11342-d4c3-f6e5-0708-090a0b0c0d0e" {
		t.Fatalf("swapUUID = %s", got)
	}
	if swapUUID("pas-un-uuid") != "" {
		t.Fatal("entrée invalide")
	}
}
