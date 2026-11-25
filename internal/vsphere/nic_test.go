package vsphere

import (
	"strings"
	"testing"

	"github.com/vmware/govmomi/cli/esx"
	"github.com/vmware/govmomi/vim25/types"
)

func TestEsxcliWhitelist(t *testing.T) {
	ok := [][]string{{"network", "nic", "stats", "get", "-n", "vmnic0"}, {"network", "nic", "list"}, {"network", "nic", "get", "-n", "vmnic12"}}
	for _, a := range ok {
		if err := EsxcliAllowed(a); err != nil {
			t.Errorf("%v refusée : %v", a, err)
		}
	}
	bad := [][]string{{"network", "nic", "down", "-n", "vmnic0"}, {"system", "shutdown", "reboot"}, {"network", "nic", "stats", "get", "-n", "vmk0"},
		{"network", "nic", "stats", "get", "-n", "vmnic0", "--reset"}, {"network", "nic", "set", "-n", "vmnic0", "-m", "1500"}, {"network"}, {"software", "vib", "install"},
		{"network", "nic", "stats", "get", "-n", "vmnic0;reboot"}}
	for _, a := range bad {
		if EsxcliAllowed(a) == nil {
			t.Errorf("%v acceptée", a)
		}
	}
}

func TestParseNICStatsDelta(t *testing.T) {
	v := []esx.Values{{"NICName": {"vmnic0"}, "ReceiveCRCerrors": {"3"}, "Receivemissederrors": {"756"}, "Receivelengtherrors": {"1"}, "Packetsreceived": {"100"}}}
	a, err := ParseNICStats(v)
	if err != nil || a["rx_crc"] != 3 || a["rx_missed"] != 756 {
		t.Fatalf("%v %v", a, err)
	}
	b := NICCounters{"rx_crc": 5, "rx_missed": 700, "rx_length": 1, "rx_packets": 200}
	d := Delta(map[string]NICCounters{"vmnic0": a}, map[string]NICCounters{"vmnic0": b, "vmnic1": b})
	if d["vmnic0"]["rx_crc"] != 2 || d["vmnic0"]["rx_length"] != 0 || d["vmnic0"]["rx_packets"] != 100 {
		t.Errorf("delta : %v", d)
	}
	if _, ok := d["vmnic0"]["rx_missed"]; ok {
		t.Error("un compteur revenu en arrière (remise à zéro) doit être ignoré")
	}
	if _, ok := d["vmnic1"]; ok {
		t.Error("vmnic sans relevé initial")
	}
	if _, err := ParseNICStats(nil); err == nil {
		t.Error("réponse vide")
	}
}

func TestPeers(t *testing.T) {
	fd := true
	hints := []types.PhysicalNicHintInfo{
		{Device: "vmnic0", ConnectedSwitchPort: &types.PhysicalNicCdpInfo{DevId: "leaf-03.lab", PortId: "Ethernet12/1", Mtu: 1500, Vlan: 20, FullDuplex: &fd, HardwarePlatform: "cisco N9K"}},
		{Device: "vmnic1", LldpInfo: &types.LinkLayerDiscoveryProtocolInfo{ChassisId: "00:1c:73:aa:bb:cc", PortId: "Ethernet7",
			Parameter: []types.KeyAnyValue{{Key: "System Name", Value: "arista-02"}, {Key: "Port Description", Value: "esx52 vmnic1"}, {Key: "Management Address", Value: "10.0.0.2"}}}},
		{Device: "vmnic2"},
	}
	p := PeersFromHints(hints)
	if len(p) != 2 || p["vmnic0"].Protocol != "cdp" || p["vmnic0"].Switch != "leaf-03.lab" || p["vmnic0"].Port != "Ethernet12/1" || p["vmnic0"].VLAN != 20 {
		t.Errorf("cdp : %+v", p)
	}
	if l := p["vmnic1"]; l.Protocol != "lldp" || l.Switch != "arista-02" || l.PortDesc != "esx52 vmnic1" || l.MgmtAddr != "10.0.0.2" {
		t.Errorf("lldp : %+v", l)
	}
	path := &Path{VMK: VMKInfo{Device: "vmk1", MTU: 9000}, Uplinks: []Uplink{{Device: "vmnic0"}, {Device: "vmnic1"}, {Device: "vmnic2"}}}
	ApplyPeers(path, p)
	if path.Uplinks[0].Peer == nil || path.Uplinks[2].Peer != nil {
		t.Error("rattachement des voisins")
	}
	if len(path.Notes) != 1 || !strings.Contains(path.Notes[0], "MTU 1500") {
		t.Errorf("note MTU : %v", path.Notes)
	}
	empty := &Path{Uplinks: []Uplink{{Device: "vmnic0"}}}
	ApplyPeers(empty, PeersFromHints([]types.PhysicalNicHintInfo{{Device: "vmnic0"}}))
	if len(empty.Notes) != 1 || !strings.Contains(empty.Notes[0], "aucune annonce") {
		t.Errorf("sans annonce : %v", empty.Notes)
	}
}
