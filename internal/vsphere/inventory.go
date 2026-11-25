package vsphere

import (
	"context"
	"fmt"
	"sort"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

type vimClient = vim25.Client

// HostInfo décrit un hôte ESXi.
type HostInfo struct {
	Name            string      `json:"name"`
	Ref             string      `json:"ref"`
	Version         string      `json:"version"`
	Build           string      `json:"build"`
	ConnectionState string      `json:"connection_state"`
	PowerState      string      `json:"power_state"`
	Maintenance     bool        `json:"maintenance"`
	Cluster         *ObjectInfo `json:"cluster,omitempty"`
	VMotionEnabled  bool        `json:"vmotion_enabled"`
}

// DatastoreInfo décrit un datastore vu par un hôte.
type DatastoreInfo struct {
	Name       string   `json:"name"`
	Ref        string   `json:"ref"`
	Type       string   `json:"type"`                  // NFS, NFS41, VMFS, vsan…
	NFSServer  string   `json:"nfs_server,omitempty"`  // info.nas.remoteHost
	NFSServers []string `json:"nfs_servers,omitempty"` // NFS 4.1 multi-serveurs
	NFSExport  string   `json:"nfs_export,omitempty"`
	Capacity   int64    `json:"capacity_bytes"`
	Free       int64    `json:"free_bytes"`
	Accessible bool     `json:"accessible"`
	Mounted    bool     `json:"mounted"` // sur l'hôte interrogé
	ReadOnly   bool     `json:"read_only"`
	HostCount  int      `json:"host_count"`
	URL        string   `json:"url"`
}

// IsNFS indique un datastore NFS (v3 ou 4.1).
func (d DatastoreInfo) IsNFS() bool { return d.Type == "NFS" || d.Type == "NFS41" }

func hostInfo(ctx context.Context, vc *vimClient, ref types.ManagedObjectReference) (*HostInfo, error) {
	pc := property.DefaultCollector(vc)
	var h mo.HostSystem
	if err := pc.RetrieveOne(ctx, ref, []string{"name", "parent", "summary.config.product", "summary.runtime", "summary.config.vmotionEnabled"}, &h); err != nil {
		return nil, fmt.Errorf("lecture de l'hôte : %w", err)
	}
	info := &HostInfo{Name: h.Name, Ref: ref.String(), VMotionEnabled: h.Summary.Config.VmotionEnabled}
	if p := h.Summary.Config.Product; p != nil {
		info.Version, info.Build = p.Version, p.Build
	}
	info.ConnectionState = string(h.Summary.Runtime.ConnectionState)
	info.PowerState = string(h.Summary.Runtime.PowerState)
	info.Maintenance = h.Summary.Runtime.InMaintenanceMode
	if h.Parent != nil && h.Parent.Type == "ClusterComputeResource" {
		var c mo.ClusterComputeResource
		if err := pc.RetrieveOne(ctx, *h.Parent, []string{"name"}, &c); err == nil {
			info.Cluster = &ObjectInfo{Name: c.Name, Ref: h.Parent.String()}
		}
	}
	return info, nil
}

// Datastores liste les datastores visibles par un hôte, NFS en premier puis par nom.
func Datastores(ctx context.Context, s *Session, hostRef types.ManagedObjectReference) ([]DatastoreInfo, error) {
	vc := s.Client
	pc := property.DefaultCollector(vc)
	var h mo.HostSystem
	if err := pc.RetrieveOne(ctx, hostRef, []string{"datastore"}, &h); err != nil {
		return nil, fmt.Errorf("datastores de l'hôte : %w", err)
	}
	if len(h.Datastore) == 0 {
		return []DatastoreInfo{}, nil
	}
	var dss []mo.Datastore
	if err := pc.Retrieve(ctx, h.Datastore, []string{"name", "summary", "info", "host"}, &dss); err != nil {
		return nil, fmt.Errorf("propriétés des datastores : %w", err)
	}
	out := make([]DatastoreInfo, 0, len(dss))
	for _, ds := range dss {
		d := datastoreInfo(ds, &hostRef)
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsNFS() != out[j].IsNFS() {
			return out[i].IsNFS()
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func datastoreInfo(ds mo.Datastore, host *types.ManagedObjectReference) DatastoreInfo {
	d := DatastoreInfo{
		Name: ds.Name, Ref: ds.Self.String(), Type: ds.Summary.Type,
		Capacity: ds.Summary.Capacity, Free: ds.Summary.FreeSpace, Accessible: ds.Summary.Accessible,
		HostCount: len(ds.Host), URL: ds.Summary.Url,
	}
	if nas, ok := ds.Info.(*types.NasDatastoreInfo); ok && nas.Nas != nil {
		d.NFSServer = nas.Nas.RemoteHost
		d.NFSServers = nas.Nas.RemoteHostNames
		d.NFSExport = nas.Nas.RemotePath
		if nas.Nas.Type != "" {
			d.Type = nas.Nas.Type
		}
	}
	for _, hm := range ds.Host {
		if host != nil && hm.Key == *host {
			if hm.MountInfo.Mounted != nil {
				d.Mounted = *hm.MountInfo.Mounted
			}
			if hm.MountInfo.Accessible != nil {
				d.Accessible = *hm.MountInfo.Accessible
			}
			d.ReadOnly = hm.MountInfo.AccessMode == "readOnly"
		}
	}
	return d
}

// Datastore lit un datastore donné (vu depuis un hôte optionnel).
func Datastore(ctx context.Context, s *Session, dsRef types.ManagedObjectReference, host *types.ManagedObjectReference) (*DatastoreInfo, error) {
	var ds mo.Datastore
	if err := property.DefaultCollector(s.Client).RetrieveOne(ctx, dsRef, []string{"name", "summary", "info", "host"}, &ds); err != nil {
		return nil, fmt.Errorf("datastore : %w", err)
	}
	d := datastoreInfo(ds, host)
	return &d, nil
}

// Hosts liste les hôtes qui montent un datastore.
func Hosts(ctx context.Context, s *Session, dsRef types.ManagedObjectReference) ([]HostInfo, error) {
	var ds mo.Datastore
	if err := property.DefaultCollector(s.Client).RetrieveOne(ctx, dsRef, []string{"host"}, &ds); err != nil {
		return nil, fmt.Errorf("hôtes du datastore : %w", err)
	}
	out := make([]HostInfo, 0, len(ds.Host))
	for _, hm := range ds.Host {
		h, err := hostInfo(ctx, s.Client, hm.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
