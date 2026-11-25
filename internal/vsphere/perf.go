package vsphere

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/performance"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/types"

	"vigie/internal/analysis"
)

// RealtimeInterval est l'intervalle temps réel du PerformanceManager (secondes).
const RealtimeInterval = 20

// Compteurs voulus, avec leurs alternatives (§6.4 : résolution par nom, jamais par ID figé).
var CounterAlternatives = map[string][]string{
	"tx":          {"net.bytesTx.average", "net.transmitted.average"},
	"rx":          {"net.bytesRx.average", "net.received.average"},
	"errorsRx":    {"net.errorsRx.summation"},
	"errorsTx":    {"net.errorsTx.summation"},
	"droppedRx":   {"net.droppedRx.summation"},
	"droppedTx":   {"net.droppedTx.summation"},
	"dsWriteLat":  {"datastore.totalWriteLatency.average"},
	"dsReadLat":   {"datastore.totalReadLatency.average"},
	"dsWriteOps":  {"datastore.numberWriteAveraged.average"},
	"dsReadOps":   {"datastore.numberReadAveraged.average"},
	"dsWriteKBps": {"datastore.write.average"},
	"dsReadKBps":  {"datastore.read.average"},
	// côté VM (instance scsiB:U) : IOPS et latences du disque de test vus par l'hôte pour cette VM seule
	"vdReadOps":  {"virtualDisk.numberReadAveraged.average"},
	"vdWriteOps": {"virtualDisk.numberWriteAveraged.average"},
	"vdReadLat":  {"virtualDisk.totalReadLatency.average"},
	"vdWriteLat": {"virtualDisk.totalWriteLatency.average"},
}

// Counters associe une clé logique (tx, rx…) au compteur résolu sur ce serveur.
type Counters struct {
	ByKey map[string]types.PerfCounterInfo
	Names map[string]string // clé -> nom complet retenu
}

// ResolveCounters résout les compteurs par nom, en prenant la première alternative disponible.
func ResolveCounters(ctx context.Context, s *Session) (*Counters, error) {
	byName, err := performance.NewManager(s.Client).CounterInfoByName(ctx)
	if err != nil {
		return nil, fmt.Errorf("liste des compteurs : %w", err)
	}
	c := &Counters{ByKey: map[string]types.PerfCounterInfo{}, Names: map[string]string{}}
	for key, alts := range CounterAlternatives {
		for _, n := range alts {
			if info, ok := byName[n]; ok {
				c.ByKey[key] = *info
				c.Names[key] = n
				break
			}
		}
	}
	return c, nil
}

// Series : clé logique -> instance -> échantillons.
type Series map[string]map[string][]analysis.Sample

// QueryRealtime interroge les échantillons temps réel (20 s) d'une entité pour les clés données,
// toutes instances, sur [start, end] (heure de l'hôte).
func QueryRealtime(ctx context.Context, s *Session, c *Counters, entity types.ManagedObjectReference, keys []string, start, end time.Time) (Series, error) {
	var ids []types.PerfMetricId
	keyOf := map[int32]string{}
	for _, k := range keys {
		info, ok := c.ByKey[k]
		if !ok {
			continue
		}
		ids = append(ids, types.PerfMetricId{CounterId: info.Key, Instance: "*"})
		keyOf[info.Key] = k
	}
	out := Series{}
	if len(ids) == 0 {
		return out, nil
	}
	spec := types.PerfQuerySpec{Entity: entity, StartTime: &start, EndTime: &end, IntervalId: RealtimeInterval, MetricId: ids, Format: string(types.PerfFormatNormal)}
	res, err := performance.NewManager(s.Client).Query(ctx, []types.PerfQuerySpec{spec})
	if err != nil {
		return nil, fmt.Errorf("QueryPerf %s : %w", entity, err)
	}
	for _, base := range res {
		em, ok := base.(*types.PerfEntityMetric)
		if !ok {
			continue
		}
		for _, v := range em.Value {
			ser, ok := v.(*types.PerfMetricIntSeries)
			if !ok {
				continue
			}
			k := keyOf[ser.Id.CounterId]
			if k == "" {
				continue
			}
			if out[k] == nil {
				out[k] = map[string][]analysis.Sample{}
			}
			for i, val := range ser.Value {
				if i >= len(em.SampleInfo) || val < 0 { // -1 = pas de donnée
					continue
				}
				si := em.SampleInfo[i]
				iv := si.Interval
				if iv <= 0 {
					iv = RealtimeInterval
				}
				out[k][ser.Id.Instance] = append(out[k][ser.Id.Instance], analysis.Sample{
					Start: si.Timestamp.Add(-time.Duration(iv) * time.Second), End: si.Timestamp, Value: float64(val),
				})
			}
		}
	}
	for _, inst := range out {
		for name := range inst {
			sort.Slice(inst[name], func(i, j int) bool { return inst[name][i].Start.Before(inst[name][j].Start) })
		}
	}
	return out, nil
}

// SumInstances additionne plusieurs instances échantillon par échantillon (mêmes horodatages).
func SumInstances(series map[string][]analysis.Sample, instances []string) []analysis.Sample {
	acc := map[time.Time]*analysis.Sample{}
	for _, inst := range instances {
		for _, smp := range series[inst] {
			if a, ok := acc[smp.End]; ok {
				a.Value += smp.Value
			} else {
				cp := smp
				acc[smp.End] = &cp
			}
		}
	}
	out := make([]analysis.Sample, 0, len(acc))
	for _, v := range acc {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// DatastoreInstance renvoie l'identifiant d'instance d'un datastore dans les compteurs datastore.*
// de l'hôte : l'UUID de son URL (ds:///vmfs/volumes/<uuid>/).
func DatastoreInstance(ds *DatastoreInfo) string {
	u := strings.TrimSuffix(ds.URL, "/")
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// Clock donne le décalage entre l'horloge de l'appliance et celles de vSphere (§3.1 : ne pas se fier
// au NTP invité). Les échantillons temps réel sont horodatés par l'hôte.
type Clock struct {
	HostOffset   time.Duration `json:"host_offset_ns"`   // heure hôte − heure appliance
	ServerOffset time.Duration `json:"server_offset_ns"` // heure vCenter − heure appliance
	RTT          time.Duration `json:"rtt_ns"`
	At           time.Time     `json:"at"`
}

// ToHost convertit une heure de l'appliance en heure de l'hôte.
func (c Clock) ToHost(t time.Time) time.Time { return t.Add(c.HostOffset) }

// MeasureClock interroge l'heure du vCenter (ServiceInstance.CurrentTime) et de l'hôte
// (HostDateTimeSystem), en corrigeant de la moitié de l'aller-retour.
func MeasureClock(ctx context.Context, s *Session, hostRef types.ManagedObjectReference) (Clock, error) {
	var c Clock
	t1 := time.Now()
	srv, err := methods.GetCurrentTime(ctx, s.Client)
	t2 := time.Now()
	if err != nil {
		return c, fmt.Errorf("heure du serveur : %w", err)
	}
	mid := t1.Add(t2.Sub(t1) / 2)
	c.ServerOffset, c.RTT, c.At = srv.Sub(mid), t2.Sub(t1), mid
	c.HostOffset = c.ServerOffset
	host := object.NewHostSystem(s.Client, hostRef)
	dts, err := host.ConfigManager().DateTimeSystem(ctx)
	if err == nil {
		t1 = time.Now()
		ht, err := dts.Query(ctx)
		t2 = time.Now()
		if err == nil && ht != nil {
			c.HostOffset = ht.Sub(t1.Add(t2.Sub(t1) / 2))
		}
	}
	return c, nil
}

// DiskInstance renvoie l'instance « scsiB:U » d'un disque de la VM (compteurs virtualDisk.*), à partir
// de la clé de son contrôleur et de son numéro d'unité.
func DiskInstance(ctx context.Context, s *Session, vmRef types.ManagedObjectReference, controllerKey, unit int32) (string, error) {
	devices, err := object.NewVirtualMachine(s.Client, vmRef).Device(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devices {
		if c, ok := d.(types.BaseVirtualSCSIController); ok && c.GetVirtualSCSIController().Key == controllerKey {
			return fmt.Sprintf("scsi%d:%d", c.GetVirtualSCSIController().BusNumber, unit), nil
		}
	}
	return "", fmt.Errorf("contrôleur %d introuvable", controllerKey)
}
