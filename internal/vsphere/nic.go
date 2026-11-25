package vsphere

// Phase 10 : compteurs détaillés des vmnic (esxcli via l'API, sans SSH) et voisins CDP/LLDP
// (QueryNetworkHint). Lecture seule : esxcli ne passe que par Esxcli, qui refuse toute commande
// hors de la liste blanche.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/vmware/govmomi/cli/esx"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// esxcliAllowed : les seules commandes esxcli que l'appliance peut lancer (toutes en lecture).
var esxcliAllowed = [][]string{
	{"network", "nic", "stats", "get"},
	{"network", "nic", "list"},
	{"network", "nic", "get"},
}

// EsxcliAllowed vérifie qu'une commande est dans la liste blanche (préfixe exact, puis options).
func EsxcliAllowed(args []string) error {
	for _, a := range esxcliAllowed {
		if len(args) < len(a) {
			continue
		}
		ok := true
		for i := range a {
			if args[i] != a[i] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		rest := args[len(a):]
		// options autorisées : -n <vmnicX> seulement
		for i := 0; i < len(rest); i++ {
			if rest[i] == "-n" && i+1 < len(rest) && isVmnic(rest[i+1]) {
				i++
				continue
			}
			return fmt.Errorf("esxcli : option refusée %q", rest[i])
		}
		return nil
	}
	return fmt.Errorf("esxcli : commande refusée (lecture seule) : %s", strings.Join(args, " "))
}

func isVmnic(s string) bool {
	if !strings.HasPrefix(s, "vmnic") || len(s) == 5 || len(s) > 12 {
		return false
	}
	for _, c := range s[5:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Esxcli lance une commande esxcli en lecture sur un hôte, via l'API vSphere. Journalisé.
func Esxcli(ctx context.Context, s *Session, hostRef types.ManagedObjectReference, args []string) ([]esx.Values, error) {
	if err := EsxcliAllowed(args); err != nil {
		return nil, err
	}
	e, err := esx.NewExecutor(ctx, s.Client, object.NewHostSystem(s.Client, hostRef))
	if err != nil {
		return nil, fmt.Errorf("esxcli : %w", err)
	}
	res, err := e.Run(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("esxcli %s : %w", strings.Join(args, " "), err)
	}
	log.Printf("vsphere: esxcli %s sur %s", strings.Join(args, " "), hostRef.Value)
	return res.Values, nil
}

// NICCounters : compteurs cumulés d'une vmnic (esxcli network nic stats get).
type NICCounters map[string]int64

// Compteurs retenus (noms esxcli) et leur clé JSON.
var NICCounterKeys = []struct{ Esxcli, Key string }{
	{"ReceiveCRCerrors", "rx_crc"}, {"Receiveframeerrors", "rx_frame"}, {"Receivelengtherrors", "rx_length"},
	{"ReceiveFIFOerrors", "rx_fifo"}, {"Receivemissederrors", "rx_missed"}, {"Receiveovererrors", "rx_over"},
	{"Receivepacketsdropped", "rx_dropped"}, {"Totalreceiveerrors", "rx_errors"},
	{"Transmitabortederrors", "tx_aborted"}, {"Transmitcarriererrors", "tx_carrier"}, {"TransmitFIFOerrors", "tx_fifo"},
	{"Transmitpacketsdropped", "tx_dropped"}, {"Totaltransmiterrors", "tx_errors"},
	{"Packetsreceived", "rx_packets"}, {"Packetssent", "tx_packets"},
}

// ParseNICStats lit la réponse d'esxcli network nic stats get.
func ParseNICStats(v []esx.Values) (NICCounters, error) {
	if len(v) == 0 {
		return nil, fmt.Errorf("esxcli : réponse vide")
	}
	out := NICCounters{}
	for _, k := range NICCounterKeys {
		if s := v[0].Value(k.Esxcli); s != "" {
			n, err := strconv.ParseInt(s, 10, 64)
			if err == nil {
				out[k.Key] = n
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("esxcli : aucun compteur reconnu")
	}
	return out, nil
}

// NICStats relève les compteurs de plusieurs vmnic.
func NICStats(ctx context.Context, s *Session, hostRef types.ManagedObjectReference, devices []string) (map[string]NICCounters, error) {
	out := map[string]NICCounters{}
	for _, d := range devices {
		v, err := Esxcli(ctx, s, hostRef, []string{"network", "nic", "stats", "get", "-n", d})
		if err != nil {
			return nil, err
		}
		c, err := ParseNICStats(v)
		if err != nil {
			return nil, err
		}
		out[d] = c
	}
	return out, nil
}

// Delta : écarts entre deux relevés (compteurs absents ou revenus en arrière ignorés).
func Delta(before, after map[string]NICCounters) map[string]NICCounters {
	out := map[string]NICCounters{}
	for d, a := range after {
		b, ok := before[d]
		if !ok {
			continue
		}
		c := NICCounters{}
		for k, v := range a {
			if w, ok := b[k]; ok && v >= w {
				c[k] = v - w
			}
		}
		out[d] = c
	}
	return out
}

// Peer : ce que l'hôte entend du switch sur une vmnic (CDP ou LLDP).
type Peer struct {
	Device     string `json:"device"`
	Protocol   string `json:"protocol"` // cdp, lldp
	Switch     string `json:"switch,omitempty"`
	Port       string `json:"port,omitempty"`
	PortDesc   string `json:"port_description,omitempty"`
	MgmtAddr   string `json:"mgmt_addr,omitempty"`
	Platform   string `json:"platform,omitempty"`
	VLAN       int32  `json:"vlan,omitempty"`
	MTU        int32  `json:"mtu,omitempty"`
	FullDuplex *bool  `json:"full_duplex,omitempty"`
}

// PeersFromHints convertit les indications réseau de l'hôte.
func PeersFromHints(hints []types.PhysicalNicHintInfo) map[string]Peer {
	out := map[string]Peer{}
	for _, h := range hints {
		if c := h.ConnectedSwitchPort; c != nil && (c.DevId != "" || c.PortId != "") {
			out[h.Device] = Peer{Device: h.Device, Protocol: "cdp", Switch: firstNonEmpty(c.SystemName, c.DevId), Port: c.PortId,
				MgmtAddr: firstNonEmpty(c.MgmtAddr, c.Address), Platform: c.HardwarePlatform, VLAN: c.Vlan, MTU: c.Mtu, FullDuplex: c.FullDuplex}
			continue
		}
		if l := h.LldpInfo; l != nil && (l.ChassisId != "" || l.PortId != "") {
			p := Peer{Device: h.Device, Protocol: "lldp", Switch: l.ChassisId, Port: l.PortId}
			for _, kv := range l.Parameter {
				v := fmt.Sprint(kv.Value)
				switch strings.ToLower(strings.ReplaceAll(kv.Key, " ", "")) {
				case "systemname":
					p.Switch = v
				case "portdescription":
					p.PortDesc = v
				case "managementaddress":
					p.MgmtAddr = v
				case "systemdescription":
					p.Platform = v
				case "vlanid", "portvlanid":
					if n, err := strconv.Atoi(v); err == nil {
						p.VLAN = int32(n)
					}
				case "mtu", "maximumframesize":
					if n, err := strconv.Atoi(v); err == nil {
						p.MTU = int32(n)
					}
				}
			}
			out[h.Device] = p
		}
	}
	return out
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// Peers interroge l'hôte (QueryNetworkHint) pour les vmnic données (toutes si vide).
func Peers(ctx context.Context, s *Session, hostRef types.ManagedObjectReference, devices []string) (map[string]Peer, error) {
	ns, err := object.NewHostSystem(s.Client, hostRef).ConfigManager().NetworkSystem(ctx)
	if err != nil {
		return nil, fmt.Errorf("système réseau de l'hôte : %w", err)
	}
	sort.Strings(devices)
	hints, err := ns.QueryNetworkHint(ctx, devices)
	if err != nil {
		return nil, fmt.Errorf("CDP/LLDP (QueryNetworkHint) : %w", err)
	}
	return PeersFromHints(hints), nil
}
