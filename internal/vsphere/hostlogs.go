package vsphere

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vmware/govmomi/event"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// LogCursor mémorise la dernière ligne lue d'un journal d'hôte.
type LogCursor struct {
	Key  string `json:"key"`
	Line int32  `json:"line"`
}

// nfsWarning reconnaît les lignes d'avertissement NFS du vmkernel.log (§6.4).
var nfsWarning = regexp.MustCompile(`(?i)(NFS\d*:|nfs).*(warn|lost|not respond|timed? ?out|timeout|disconnect|apd|retry|restored|fail)`)

// OpenLog place un curseur à la fin du journal (BrowseDiagnosticLog, privilège Global.Diagnostics).
func OpenLog(ctx context.Context, s *Session, hostRef types.ManagedObjectReference, key string) (*LogCursor, error) {
	dm := object.NewDiagnosticManager(s.Client)
	h, err := dm.BrowseLog(ctx, object.NewHostSystem(s.Client, hostRef), key, 999999999, 0)
	if err != nil {
		return nil, fmt.Errorf("journal %s : %w", key, err)
	}
	return &LogCursor{Key: key, Line: h.LineEnd}, nil
}

// LogReport résume les nouvelles lignes d'un journal.
type LogReport struct {
	NewLines   int      `json:"new_lines"`
	NFSWarning int      `json:"nfs_warning_lines"`
	Samples    []string `json:"samples,omitempty"` // jusqu'à 20 lignes d'avertissement
	Truncated  bool     `json:"truncated,omitempty"`
}

// ReadNewLines lit les lignes ajoutées depuis le curseur (au plus maxLines) et compte les
// avertissements NFS. Le curseur avance.
func ReadNewLines(ctx context.Context, s *Session, hostRef types.ManagedObjectReference, cur *LogCursor, maxLines int32) (*LogReport, error) {
	dm := object.NewDiagnosticManager(s.Client)
	host := object.NewHostSystem(s.Client, hostRef)
	rep := &LogReport{}
	start := cur.Line + 1
	for read := int32(0); read < maxLines; {
		n := int32(1000)
		if maxLines-read < n {
			n = maxLines - read
		}
		h, err := dm.BrowseLog(ctx, host, cur.Key, start, n)
		if err != nil {
			return nil, fmt.Errorf("journal %s : %w", cur.Key, err)
		}
		if len(h.LineText) == 0 || h.LineEnd < start {
			break
		}
		for _, l := range h.LineText {
			rep.NewLines++
			if nfsWarning.MatchString(l) {
				rep.NFSWarning++
				if len(rep.Samples) < 20 {
					rep.Samples = append(rep.Samples, strings.TrimSpace(l))
				}
			}
		}
		read += int32(len(h.LineText))
		cur.Line = h.LineEnd
		start = h.LineEnd + 1
		if int32(len(h.LineText)) < n {
			break
		}
	}
	if rep.NewLines >= int(maxLines) {
		rep.Truncated = true
	}
	return rep, nil
}

// HostEvent est un événement vCenter pertinent pour le chemin NFS.
type HostEvent struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
	Flap    bool      `json:"flap"` // changement d'état de lien
}

// linkEvent : identifiants d'événements de lien et de connectivité (vmnic, uplinks, NFS, APD).
func classify(id string) (relevant, flap bool) {
	l := strings.ToLower(id)
	switch {
	case strings.Contains(l, "linkstate"), strings.Contains(l, "uplink"), strings.Contains(l, "redundancy.lost"), strings.Contains(l, "redundancy.degraded"):
		return true, strings.Contains(l, "down") || strings.Contains(l, "lost") || strings.Contains(l, "degraded")
	case strings.Contains(l, "connectivity.lost"), strings.Contains(l, "nfs"), strings.Contains(l, "apd"), strings.Contains(l, "storage.connectivity"):
		return true, false
	}
	return false, false
}

// HostEvents lit les événements de l'hôte sur [begin, end] et garde ceux du chemin réseau/stockage.
func HostEvents(ctx context.Context, s *Session, hostRef types.ManagedObjectReference, begin, end time.Time) ([]HostEvent, error) {
	m := event.NewManager(s.Client)
	filter := types.EventFilterSpec{
		Entity: &types.EventFilterSpecByEntity{Entity: hostRef, Recursion: types.EventFilterSpecRecursionOptionSelf},
		Time:   &types.EventFilterSpecByTime{BeginTime: &begin, EndTime: &end},
	}
	evs, err := m.QueryEvents(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("événements : %w", err)
	}
	var out []HostEvent
	for _, be := range evs {
		e := be.GetEvent()
		id := ""
		switch x := be.(type) {
		case *types.EventEx:
			id = x.EventTypeId
		case *types.ExtendedEvent:
			id = x.EventTypeId
		default:
			id = fmt.Sprintf("%T", be)
		}
		rel, flap := classify(id)
		if !rel {
			continue
		}
		out = append(out, HostEvent{Time: e.CreatedTime, Type: id, Message: e.FullFormattedMessage, Flap: flap})
	}
	return out, nil
}
