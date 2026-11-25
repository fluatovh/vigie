package vsphere

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// anyVM renvoie une VM quelconque du simulateur avec son UUID et ses périphériques.
func anyVM(t *testing.T, s *Session) mo.VirtualMachine {
	t.Helper()
	ctx := context.Background()
	v, err := view.NewManager(s.Client).CreateContainerView(ctx, s.Client.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Destroy(ctx)
	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"config.uuid", "config.hardware.device", "runtime.host"}, &vms); err != nil {
		t.Fatal(err)
	}
	for _, vm := range vms {
		if vm.Runtime.Host != nil && vm.Config != nil {
			return vm
		}
	}
	t.Fatal("aucune VM dans le simulateur")
	return mo.VirtualMachine{}
}

// startVCSim démarre un vCenter simulé en TLS et renvoie le gestionnaire connecté avec empreinte.
func startVCSim(t *testing.T) (*Manager, *Session, func()) {
	t.Helper()
	model := simulator.VPX()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	srv := model.Service.NewServer()
	cert := srv.Certificate()
	ctx := context.Background()

	info, err := Thumbprint(ctx, srv.URL.String())
	if err != nil {
		t.Fatalf("thumbprint : %v", err)
	}
	if info.SHA256 != soap.ThumbprintSHA256(cert) || !info.SelfSigned || info.Trusted {
		t.Fatalf("empreinte inattendue : %+v", info)
	}

	m := NewManager()
	m.Logf = t.Logf
	pw, _ := srv.URL.User.Password()
	// mauvaise empreinte : refus
	if _, err := m.Connect(ctx, ConnectParams{URL: srv.URL.String(), Username: srv.URL.User.Username(), Password: pw, Thumbprint: "00:" + info.SHA256[3:]}); err == nil {
		t.Fatal("une empreinte fausse doit être refusée")
	}
	// sans empreinte : refus
	if _, err := m.Connect(ctx, ConnectParams{URL: srv.URL.String(), Username: "user", Password: "pass"}); err == nil {
		t.Fatal("une connexion sans empreinte doit être refusée")
	}
	s, err := m.Connect(ctx, ConnectParams{URL: srv.URL.String(), Username: srv.URL.User.Username(), Password: pw, Thumbprint: info.SHA256})
	if err != nil {
		t.Fatalf("connect : %v", err)
	}
	cleanup := func() {
		m.Disconnect(ctx)
		srv.Close()
		model.Remove()
	}
	return m, s, cleanup
}

func TestVCSimConnectIdentifyInventory(t *testing.T) {
	m, s, cleanup := startVCSim(t)
	defer cleanup()
	ctx := context.Background()

	st := m.Status()
	if !st.Connected || !st.Server.IsVCenter || st.Username == "" {
		t.Fatalf("statut : %+v", st)
	}

	// Une VM du simulateur sert d'appliance : on lit son UUID et on le présente en ordre inversé.
	vm := anyVM(t, s)
	ref := vm.Self
	ProductUUID = func() (string, error) { return swapUUID(vm.Config.Uuid), nil }
	self, err := Identify(ctx, s)
	if err != nil {
		t.Fatalf("identify : %v", err)
	}
	if self.VM.Ref != ref.String() || self.Method != "uuid-swapped" || self.Host.Name == "" {
		t.Fatalf("self : %+v", self)
	}

	// Repli par MAC.
	var mac string
	for _, d := range vm.Config.Hardware.Device {
		if nic, ok := d.(types.BaseVirtualEthernetCard); ok {
			mac = nic.GetVirtualEthernetCard().MacAddress
		}
	}
	ProductUUID = func() (string, error) { return "00000000-0000-0000-0000-000000000000", nil }
	PrimaryMAC = func() (string, error) { return strings.ToLower(mac), nil }
	self2, err := Identify(ctx, s)
	if err != nil || self2.Method != "mac" || self2.VM.Ref != ref.String() {
		t.Fatalf("identify par MAC : %v %+v", err, self2)
	}

	hostRef, _ := Ref(self.Host.Ref)
	// Ajout d'un datastore NFS sur l'hôte de l'appliance.
	host := object.NewHostSystem(s.Client, hostRef)
	dss, err := host.ConfigManager().DatastoreSystem(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ResolveIP = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.2")}, nil }
	// vcsim adosse chaque datastore à un répertoire réel : LocalPath doit exister.
	local := filepath.Join(t.TempDir(), "shared-nfs")
	if err := os.Mkdir(local, 0o755); err != nil {
		t.Fatal(err)
	}
	nfs, err := dss.CreateNasDatastore(ctx, types.HostNasVolumeSpec{RemoteHost: "nas.lab", RemotePath: "/export/bench", LocalPath: local, AccessMode: "readWrite", Type: "NFS"})
	if err != nil {
		t.Fatalf("CreateNasDatastore : %v", err)
	}

	list, err := Datastores(ctx, s, hostRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 || !list[0].IsNFS() || list[0].Name != "shared-nfs" || list[0].NFSServer != "nas.lab" || list[0].NFSExport != "/export/bench" {
		t.Fatalf("datastores : %+v", list)
	}
	for _, d := range list[1:] {
		if d.IsNFS() {
			t.Fatalf("NFS doit être en premier : %+v", list)
		}
	}

	hosts, err := Hosts(ctx, s, nfs.Reference())
	if err != nil || len(hosts) != 1 || hosts[0].Ref != hostRef.String() {
		t.Fatalf("hosts : %v %+v", err, hosts)
	}

	// Chemin : vcsim expose vmk0 127.0.0.1/255.0.0.0 sur vSwitch0 → vmnic0.
	p, err := HostPath(ctx, s, hostRef, nfs.Reference())
	if err != nil {
		t.Fatalf("path : %v", err)
	}
	if p.VMK.Device != "vmk0" || p.Switch != "vSwitch0" || len(p.Uplinks) == 0 || p.Uplinks[0].Device != "vmnic0" || p.NFSIP != "127.0.0.2" {
		t.Fatalf("path : %+v", p)
	}

	// Privilèges (vcsim : administrateur).
	rep, err := Privileges(ctx, s, map[string]types.ManagedObjectReference{"vm": ref, "datastore": nfs.Reference(), "host": hostRef})
	if err != nil {
		t.Fatalf("privileges : %v", err)
	}
	if len(rep.Entities) != 3 || len(rep.Missing) != 0 {
		t.Fatalf("privileges : %+v", rep)
	}

	m.Disconnect(ctx)
	if _, err := m.Session(); err == nil {
		t.Fatal("après déconnexion, Session doit échouer")
	}
}

// Une session que le serveur ne reconnaît plus (vCenter redémarré) est détectée et oubliée, pour que
// la reconnexion automatique prenne le relais ; une session valide est gardée.
func TestVCSimSessionExpired(t *testing.T) {
	m, s, cleanup := startVCSim(t)
	defer cleanup()
	ctx := context.Background()
	if !m.Alive(ctx) || !m.Status().Connected {
		t.Fatal("une session valide doit être gardée")
	}
	// le serveur oublie la session (comme après un redémarrage de vCenter)
	if err := s.mgr.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Alive(ctx) {
		t.Fatal("session expirée non détectée")
	}
	if m.Status().Connected {
		t.Fatal("une session expirée doit apparaître déconnectée")
	}
	if m.Alive(ctx) {
		t.Fatal("sans session, Alive doit renvoyer false")
	}
}
