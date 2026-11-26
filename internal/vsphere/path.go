package vsphere

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Path décrit le chemin réseau d'un hôte vers un serveur NFS : vmk → portgroup → vSwitch → uplinks.
type Path struct {
	NFSServer  string   `json:"nfs_server"`
	NFSIP      string   `json:"nfs_ip"`
	VMK        VMKInfo  `json:"vmk"`
	Portgroup  string   `json:"portgroup"`
	Switch     string   `json:"switch"`
	SwitchType string   `json:"switch_type"` // standard ou distributed
	Uplinks    []Uplink `json:"uplinks"`
	Via        string   `json:"via"` // subnet (même sous-réseau) ou gateway (route par défaut)
	Notes      []string `json:"notes,omitempty"`
}

// VMKInfo décrit l'interface vmkernel retenue.
type VMKInfo struct {
	Device string `json:"device"`
	IP     string `json:"ip"`
	Mask   string `json:"mask"`
	MTU    int32  `json:"mtu"`
	MAC    string `json:"mac"`
}

// Uplink décrit une vmnic du chemin.
type Uplink struct {
	Device  string `json:"device"`
	Role    string `json:"role"` // active, standby, unused
	SpeedMb int32  `json:"speed_mb"`
	Duplex  bool   `json:"duplex"`
	LinkUp  bool   `json:"link_up"`
	MAC     string `json:"mac"`
	Driver  string `json:"driver"`
	Peer    *Peer  `json:"peer,omitempty"` // voisin CDP/LLDP vu par l'hôte (phase 10)
}

// HostPath calcule le chemin d'un hôte vers le serveur NFS d'un datastore.
func HostPath(ctx context.Context, s *Session, hostRef, dsRef types.ManagedObjectReference) (*Path, error) {
	ds, err := Datastore(ctx, s, dsRef, &hostRef)
	if err != nil {
		return nil, err
	}
	if !ds.IsNFS() {
		return nil, fmt.Errorf("%s n'est pas un datastore NFS (%s)", ds.Name, ds.Type)
	}
	var h mo.HostSystem
	if err := property.DefaultCollector(s.Client).RetrieveOne(ctx, hostRef, []string{"config.network"}, &h); err != nil {
		return nil, fmt.Errorf("configuration réseau de l'hôte : %w", err)
	}
	if h.Config == nil || h.Config.Network == nil {
		return nil, errors.New("configuration réseau de l'hôte indisponible")
	}
	servers := ds.NFSServers
	if len(servers) == 0 {
		servers = []string{ds.NFSServer}
	}
	var first error
	for _, srv := range servers {
		ip, err := resolveIP(srv)
		if err != nil {
			first = errors.Join(first, err)
			continue
		}
		p, err := ComputePath(h.Config.Network, ip)
		if err != nil {
			first = errors.Join(first, err)
			continue
		}
		p.NFSServer = srv
		attachPeers(ctx, s, hostRef, p)
		if len(servers) > 1 {
			p.Notes = append(p.Notes, fmt.Sprintf("datastore NFS 4.1 multi-serveurs (%d adresses), chemin calculé pour %s", len(servers), srv))
		}
		return p, nil
	}
	return nil, first
}

// attachPeers ajoute aux uplinks leur voisin CDP/LLDP (meilleur effort : une note sinon).
func attachPeers(ctx context.Context, s *Session, hostRef types.ManagedObjectReference, p *Path) {
	var devs []string
	for _, u := range p.Uplinks {
		devs = append(devs, u.Device)
	}
	if len(devs) == 0 {
		return
	}
	peers, err := Peers(ctx, s, hostRef, devs)
	if err != nil {
		p.Notes = append(p.Notes, fmt.Sprintf("voisins CDP/LLDP non lus : %v", err))
		return
	}
	ApplyPeers(p, peers)
}

// ApplyPeers rattache les voisins aux uplinks et signale un MTU de switch inférieur à celui du vmk.
func ApplyPeers(p *Path, peers map[string]Peer) {
	n := 0
	for i := range p.Uplinks {
		pe, ok := peers[p.Uplinks[i].Device]
		if !ok {
			continue
		}
		n++
		p.Uplinks[i].Peer = &pe
		if pe.MTU > 0 && p.VMK.MTU > pe.MTU {
			p.Notes = append(p.Notes, fmt.Sprintf("%s : le switch annonce un MTU %d, inférieur au MTU %d du %s", pe.Device, pe.MTU, p.VMK.MTU, p.VMK.Device))
		}
	}
	if n == 0 {
		p.Notes = append(p.Notes, "aucune annonce CDP/LLDP reçue sur les uplinks (écoute désactivée sur le vSwitch ou switch silencieux)")
	}
}

// ResolveIP est remplaçable dans les tests.
var ResolveIP = func(host string) ([]net.IP, error) { return net.LookupIP(host) }

func resolveIP(host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	ips, err := ResolveIP(host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("résolution DNS de %q impossible : %v", host, err)
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, nil
		}
	}
	return ips[0], nil
}

// ComputePath est la partie pure : à partir de la configuration réseau d'un hôte et de l'IP du
// serveur NFS, trouve le vmk (même sous-réseau, sinon celui qui porte la passerelle par défaut),
// son portgroup, son commutateur et les uplinks selon la politique de teaming.
func ComputePath(n *types.HostNetworkInfo, nfsIP net.IP) (*Path, error) {
	p := &Path{NFSIP: nfsIP.String()}
	vnic, via, err := selectVMK(n, nfsIP)
	if err != nil {
		return nil, err
	}
	p.Via = via
	p.VMK = VMKInfo{Device: vnic.Device, MAC: vnic.Spec.Mac, MTU: vnic.Spec.Mtu}
	if vnic.Spec.Ip != nil {
		p.VMK.IP, p.VMK.Mask = vnic.Spec.Ip.IpAddress, vnic.Spec.Ip.SubnetMask
	}
	pnics := map[string]types.PhysicalNic{}
	for _, pn := range n.Pnic {
		pnics[pn.Key] = pn
		pnics[pn.Device] = pn
	}

	if dvp := vnic.Spec.DistributedVirtualPort; dvp != nil {
		p.SwitchType = "distributed"
		p.Portgroup = dvp.PortgroupKey
		for _, ps := range n.ProxySwitch {
			if ps.DvsUuid != dvp.SwitchUuid {
				continue
			}
			p.Switch = ps.DvsName
			for _, key := range ps.Pnic {
				p.Uplinks = append(p.Uplinks, uplink(pnics, key, "active"))
			}
		}
		if p.Switch == "" {
			return nil, fmt.Errorf("vmk %s est sur un vDS inconnu de l'hôte (%s)", vnic.Device, dvp.SwitchUuid)
		}
		p.Notes = append(p.Notes, "commutateur distribué : la politique de teaming du portgroup n'est pas lue, tous les uplinks du vDS sont listés")
		return p, nil
	}

	p.SwitchType = "standard"
	p.Portgroup = vnic.Portgroup
	var pg *types.HostPortGroup
	for i := range n.Portgroup {
		if n.Portgroup[i].Spec.Name == vnic.Portgroup {
			pg = &n.Portgroup[i]
			break
		}
	}
	if pg == nil {
		return nil, fmt.Errorf("portgroup %q du vmk %s introuvable", vnic.Portgroup, vnic.Device)
	}
	var vs *types.HostVirtualSwitch
	for i := range n.Vswitch {
		if n.Vswitch[i].Name == pg.Spec.VswitchName {
			vs = &n.Vswitch[i]
			break
		}
	}
	if vs == nil {
		return nil, fmt.Errorf("vSwitch %q du portgroup %q introuvable", pg.Spec.VswitchName, pg.Spec.Name)
	}
	p.Switch = vs.Name
	// Politique de teaming : celle du portgroup si définie, sinon celle du vSwitch.
	var order *types.HostNicOrderPolicy
	if pg.Spec.Policy.NicTeaming != nil && pg.Spec.Policy.NicTeaming.NicOrder != nil {
		order = pg.Spec.Policy.NicTeaming.NicOrder
	} else if vs.Spec.Policy != nil && vs.Spec.Policy.NicTeaming != nil {
		order = vs.Spec.Policy.NicTeaming.NicOrder
	}
	listed := map[string]bool{}
	if order != nil {
		for _, d := range order.ActiveNic {
			p.Uplinks = append(p.Uplinks, uplink(pnics, d, "active"))
			listed[d] = true
		}
		for _, d := range order.StandbyNic {
			p.Uplinks = append(p.Uplinks, uplink(pnics, d, "standby"))
			listed[d] = true
		}
	}
	for _, key := range vs.Pnic {
		d := pnics[key].Device
		if d == "" {
			d = strings.TrimPrefix(key, "key-vim.host.PhysicalNic-")
		}
		if !listed[d] {
			role := "unused"
			if order == nil {
				role = "active"
			}
			p.Uplinks = append(p.Uplinks, uplink(pnics, d, role))
		}
	}
	if len(p.Uplinks) == 0 {
		p.Notes = append(p.Notes, "aucun uplink physique sur ce vSwitch : trafic NFS impossible par ce chemin")
	}
	return p, nil
}

func uplink(pnics map[string]types.PhysicalNic, key, role string) Uplink {
	pn, ok := pnics[key]
	if !ok {
		pn = pnics[strings.TrimPrefix(key, "key-vim.host.PhysicalNic-")]
	}
	u := Uplink{Device: pn.Device, Role: role, MAC: pn.Mac, Driver: pn.Driver}
	if u.Device == "" {
		u.Device = strings.TrimPrefix(key, "key-vim.host.PhysicalNic-")
	}
	if pn.LinkSpeed != nil {
		u.SpeedMb, u.Duplex, u.LinkUp = pn.LinkSpeed.SpeedMb, pn.LinkSpeed.Duplex, true
	}
	return u
}

func selectVMK(n *types.HostNetworkInfo, nfsIP net.IP) (*types.HostVirtualNic, string, error) {
	for i := range n.Vnic {
		v := &n.Vnic[i]
		if v.Spec.Ip == nil {
			continue
		}
		if sameSubnet(v.Spec.Ip.IpAddress, v.Spec.Ip.SubnetMask, nfsIP) {
			return v, "subnet", nil
		}
	}
	// Pas de vmk dans le sous-réseau : le trafic passe par la passerelle par défaut.
	if n.IpRouteConfig != nil {
		if rc, ok := n.IpRouteConfig.(*types.HostIpRouteConfig); ok && rc.DefaultGateway != "" {
			gw := net.ParseIP(rc.DefaultGateway)
			for i := range n.Vnic {
				v := &n.Vnic[i]
				if v.Spec.Ip == nil {
					continue
				}
				if rc.GatewayDevice != "" && v.Device == rc.GatewayDevice {
					return v, "gateway", nil
				}
				if gw != nil && sameSubnet(v.Spec.Ip.IpAddress, v.Spec.Ip.SubnetMask, gw) {
					return v, "gateway", nil
				}
			}
		}
	}
	return nil, "", fmt.Errorf("aucune interface vmkernel ne mène à %s (ni même sous-réseau, ni passerelle)", nfsIP)
}

func sameSubnet(ipStr, maskStr string, target net.IP) bool {
	ip := net.ParseIP(ipStr).To4()
	mask := net.ParseIP(maskStr).To4()
	t := target.To4()
	if ip == nil || mask == nil || t == nil {
		return false
	}
	m := net.IPMask(mask)
	return ip.Mask(m).Equal(t.Mask(m))
}
