package netdiag

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"vigie/internal/store"
)

// MaxTargets borne la surveillance continue (§11 du plan).
const MaxTargets = 50

// Target est une cible surveillée en continu.
type Target struct {
	ID      string    `json:"id"`
	Host    string    `json:"host"` // adresse ou nom tel que saisi
	Label   string    `json:"label,omitempty"`
	Added   time.Time `json:"added"`
	LastIP  string    `json:"last_ip,omitempty"`
	LastErr string    `json:"last_error,omitempty"`
	Last    *Point    `json:"last,omitempty"`
}

// Monitor envoie une salve de 20 pings par minute à chaque cible et consolide les résultats.
type Monitor struct {
	store   *store.Store
	mu      sync.Mutex
	targets map[string]*Target
	rrd     map[string]*RRD
	cancel  map[string]context.CancelFunc
	dirty   map[string]bool
	Burst   int           // 20
	Spacing time.Duration // 500 ms
	Step    time.Duration // 1 min
	pinger  func(ctx context.Context, host string, n int, spacing time.Duration) (string, []float64, error)
}

const targetsFile = "net-targets.json"

// NewMonitor charge les cibles et leurs RRD et démarre la surveillance.
func NewMonitor(s *store.Store) *Monitor {
	m := &Monitor{store: s, targets: map[string]*Target{}, rrd: map[string]*RRD{}, cancel: map[string]context.CancelFunc{},
		dirty: map[string]bool{}, Burst: 20, Spacing: 500 * time.Millisecond, Step: time.Minute}
	m.pinger = func(ctx context.Context, host string, n int, spacing time.Duration) (string, []float64, error) {
		ip, err := Resolve(ctx, host)
		if err != nil {
			return "", nil, err
		}
		rtts, err := Ping(ctx, ip, PingOptions{Count: n, Interval: spacing, Timeout: 2 * time.Second})
		return ip.String(), rtts, err
	}
	var list []Target
	if s != nil {
		_ = s.Load(targetsFile, &list)
	}
	for i := range list {
		t := list[i]
		m.targets[t.ID] = &t
		r := NewRRD()
		if s != nil {
			_ = s.Load(rrdFile(t.ID), r)
		}
		m.rrd[t.ID] = r
	}
	return m
}

func rrdFile(id string) string { return "net-rrd-" + id + ".json" }

// Start lance les boucles de mesure et la persistance périodique (toutes les 5 minutes).
func (m *Monitor) Start(ctx context.Context) {
	m.mu.Lock()
	for id := range m.targets {
		m.startLocked(ctx, id)
	}
	m.mu.Unlock()
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				m.Flush()
				return
			case <-t.C:
				m.Flush()
			}
		}
	}()
}

func (m *Monitor) startLocked(ctx context.Context, id string) {
	if _, ok := m.cancel[id]; ok {
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	m.cancel[id] = cancel
	go m.loop(cctx, id)
}

func (m *Monitor) loop(ctx context.Context, id string) {
	// aligne la salve sur le début de la minute (comme un pas RRD)
	for {
		now := time.Now()
		next := now.Truncate(m.Step).Add(m.Step)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
		}
		m.mu.Lock()
		t, ok := m.targets[id]
		host := ""
		if ok {
			host = t.Host
		}
		m.mu.Unlock()
		if !ok {
			return
		}
		ip, rtts, err := m.pinger(ctx, host, m.Burst, m.Spacing)
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		if t, ok := m.targets[id]; ok {
			if err != nil {
				t.LastErr = err.Error()
				rtts = make([]float64, m.Burst)
				for i := range rtts {
					rtts[i] = nan
				}
			} else {
				t.LastErr = ""
				t.LastIP = ip
			}
			p := FromRTTs(next, rtts)
			t.Last = &p
			m.rrd[id].Add(p)
			m.dirty[id] = true
		}
		m.mu.Unlock()
	}
}

// Flush persiste les RRD modifiés et la liste des cibles.
func (m *Monitor) Flush() {
	if m.store == nil {
		return
	}
	m.mu.Lock()
	type job struct {
		id string
		r  RRD
	}
	var jobs []job
	for id := range m.dirty {
		if r, ok := m.rrd[id]; ok {
			cp := *r
			cp.Tiers = append([]Tier(nil), r.Tiers...)
			for i := range cp.Tiers {
				cp.Tiers[i].Points = append([]Point(nil), r.Tiers[i].Points...)
			}
			jobs = append(jobs, job{id, cp})
		}
	}
	m.dirty = map[string]bool{}
	list := m.listLocked()
	m.mu.Unlock()
	for _, j := range jobs {
		if err := m.store.Save(rrdFile(j.id), j.r); err != nil {
			log.Printf("netdiag: persistance du RRD %s : %v", j.id, err)
		}
	}
	if err := m.store.Save(targetsFile, list); err != nil {
		log.Printf("netdiag: persistance des cibles : %v", err)
	}
}

func (m *Monitor) listLocked() []Target {
	out := make([]Target, 0, len(m.targets))
	for _, t := range m.targets {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Added.Before(out[j].Added) })
	return out
}

// List renvoie les cibles surveillées avec leur dernière mesure.
func (m *Monitor) List() []Target {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked()
}

// Add ajoute une cible (validée) et démarre sa surveillance.
func (m *Monitor) Add(ctx context.Context, host, label string) (*Target, error) {
	if _, err := Resolve(ctx, host); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.targets {
		if strings.EqualFold(t.Host, host) {
			return nil, fmt.Errorf("%s est déjà surveillée", host)
		}
	}
	if len(m.targets) >= MaxTargets {
		return nil, fmt.Errorf("%d cibles au maximum", MaxTargets)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	t := &Target{ID: hex.EncodeToString(b), Host: host, Label: strings.TrimSpace(label), Added: time.Now().UTC()}
	m.targets[t.ID] = t
	m.rrd[t.ID] = NewRRD()
	m.dirty[t.ID] = true
	m.startLocked(context.Background(), t.ID)
	go m.Flush()
	return t, nil
}

// Remove arrête la surveillance d'une cible et supprime son historique.
func (m *Monitor) Remove(id string) error {
	m.mu.Lock()
	if _, ok := m.targets[id]; !ok {
		m.mu.Unlock()
		return errors.New("cible inconnue")
	}
	if c, ok := m.cancel[id]; ok {
		c()
		delete(m.cancel, id)
	}
	delete(m.targets, id)
	delete(m.rrd, id)
	delete(m.dirty, id)
	m.mu.Unlock()
	if m.store != nil {
		_ = m.store.Remove(rrdFile(id))
	}
	m.Flush()
	return nil
}

// Series renvoie les points d'une cible sur [from, to] (résolution choisie automatiquement).
func (m *Monitor) Series(id string, from, to time.Time) (*Target, int64, []Point, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.targets[id]
	if !ok {
		return nil, 0, nil, errors.New("cible inconnue")
	}
	step, pts := m.rrd[id].Range(from, to)
	cp := *t
	return &cp, step, pts, nil
}
