// Package jobs exécute les opérations longues (tests, création de disque…) une à la fois, avec
// progression, journal, diffusion des événements (SSE), annulation propre et persistance.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vigie/internal/store"
)

// State est l'état d'un job.
type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

// Done indique un état terminal.
func (s State) Done() bool { return s == Succeeded || s == Failed || s == Cancelled }

// ErrBusy : un job est déjà en cours (verrou global, §5.3).
var ErrBusy = errors.New("un job est déjà en cours")

// Event est un événement diffusé aux abonnés (SSE).
type Event struct {
	Seq      int             `json:"seq"`
	Type     string          `json:"type"` // state, progress, log, second, result
	Time     time.Time       `json:"time"`
	State    State           `json:"state,omitempty"`
	Progress float64         `json:"progress,omitempty"` // 0-100
	Step     string          `json:"step,omitempty"`
	Msg      string          `json:"msg,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Job est l'état public d'un job.
type Job struct {
	ID       string          `json:"id"`
	Profile  string          `json:"profile"`
	Params   json.RawMessage `json:"params,omitempty"`
	State    State           `json:"state"`
	Progress float64         `json:"progress"`
	Step     string          `json:"step"`
	Created  time.Time       `json:"created"`
	Started  time.Time       `json:"started,omitempty"`
	Ended    time.Time       `json:"ended,omitempty"`
	Error    string          `json:"error,omitempty"`
	Log      []string        `json:"log"`
	Result   json.RawMessage `json:"result,omitempty"`
	By       string          `json:"by,omitempty"` // « ui » ou nom du jeton d'API
	Summary  *Summary        `json:"summary,omitempty"`
}

// Summary résume le résultat d'un job pour les listes (historique) : hôte, datastore, verdict.
type Summary struct {
	Host      string `json:"host,omitempty"`
	Datastore string `json:"datastore,omitempty"`
	Level     string `json:"level,omitempty"`
	Text      string `json:"text,omitempty"`
}

// Func est le corps d'un job : il rapporte via r et renvoie un résultat sérialisable.
type Func func(ctx context.Context, r *Reporter) (any, error)

// Reporter permet à un job de publier sa progression.
type Reporter struct {
	j *running
}

// Progress publie l'avancement global (0-100) et l'étape en cours.
func (r *Reporter) Progress(pct float64, step string) {
	r.j.mu.Lock()
	if step == "" {
		step = r.j.job.Step // une progression sans libellé garde l'étape courante
	}
	if pct < r.j.job.Progress {
		pct = r.j.job.Progress // jamais de recul
	}
	if pct > 100 {
		pct = 100
	}
	changed := step != r.j.job.Step
	r.j.job.Progress, r.j.job.Step = pct, step
	r.j.mu.Unlock()
	r.j.emit(Event{Type: "progress", Progress: pct, Step: step})
	if changed {
		r.Logf("%s", step)
	}
}

// Logf ajoute une ligne au journal du job (préfixée du pourcentage, §5.2).
func (r *Reporter) Logf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.j.mu.Lock()
	line := fmt.Sprintf("[%3.0f%%] %s", r.j.job.Progress, msg)
	r.j.job.Log = append(r.j.job.Log, line)
	if len(r.j.job.Log) > 2000 {
		r.j.job.Log = r.j.job.Log[len(r.j.job.Log)-2000:]
	}
	r.j.mu.Unlock()
	r.j.emit(Event{Type: "log", Msg: line})
}

// Data publie une donnée structurée (ex. une seconde de mesure pour le mini-graph).
func (r *Reporter) Data(typ string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	r.j.emit(Event{Type: typ, Data: b})
}

type running struct {
	mu     sync.Mutex
	job    Job
	events []Event // historique (rejoué aux nouveaux abonnés)
	subs   map[chan Event]struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

func (j *running) emit(e Event) {
	j.mu.Lock()
	e.Seq = len(j.events) + 1
	e.Time = time.Now()
	if e.Type != "second" || len(j.events) < 20000 {
		j.events = append(j.events, e)
	}
	for ch := range j.subs {
		select {
		case ch <- e:
		default: // abonné trop lent : il perd l'événement mais pas le résultat final
		}
	}
	j.mu.Unlock()
}

func (j *running) snapshot() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := j.job
	cp.Log = append([]string(nil), j.job.Log...)
	return cp
}

// Manager exécute les jobs (un à la fois) et garde l'historique.
type Manager struct {
	store   *store.Store
	mu      sync.Mutex
	current *running
	recent  map[string]*running // jobs de la session (en mémoire)
	keep    int
	// Summarize résume un résultat (nil : pas de résumé) ; appelé à la fin du job et, pour les
	// jobs persistés sans résumé, à la lecture de la liste.
	Summarize func(profile string, result json.RawMessage) *Summary
	// OnDone est appelé (hors verrou) après la persistance d'un job terminé (historique des mesures).
	OnDone func(j Job)
	// keepScheduled : jobs planifiés conservés (comptés à part des jobs manuels).
	keepScheduled int
}

// NewManager crée le gestionnaire ; les jobs terminés sont persistés dans le store (job-<id>.json).
func NewManager(s *store.Store) *Manager {
	return &Manager{store: s, recent: map[string]*running{}, keep: 200, keepScheduled: 96}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// Submit démarre un job s'il n'y en a pas déjà un en cours.
func (m *Manager) Submit(profile string, params any, by string, fn Func) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && !m.current.snapshot().State.Done() {
		return nil, fmt.Errorf("%w (%s, %s)", ErrBusy, m.current.job.ID, m.current.job.Profile)
	}
	p, _ := json.Marshal(params)
	ctx, cancel := context.WithCancel(context.Background())
	id := newID()
	if by == "schedule" {
		id += "s" // jobs planifiés : rétention à part (ils ne chassent pas les tests manuels)
	}
	r := &running{job: Job{ID: id, Profile: profile, Params: p, State: Queued, Created: time.Now(), Log: []string{}, By: by},
		subs: map[chan Event]struct{}{}, cancel: cancel, done: make(chan struct{})}
	m.current = r
	m.recent[r.job.ID] = r
	go m.run(ctx, r, fn)
	snap := r.snapshot()
	return &snap, nil
}

func (m *Manager) run(ctx context.Context, r *running, fn Func) {
	defer close(r.done)
	r.mu.Lock()
	r.job.State, r.job.Started = Running, time.Now()
	r.mu.Unlock()
	r.emit(Event{Type: "state", State: Running})
	log.Printf("jobs: %s (%s) démarré", r.job.ID, r.job.Profile)
	rep := &Reporter{j: r}
	var res any
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("panique : %v", p)
			}
		}()
		res, err = fn(ctx, rep)
	}()
	r.mu.Lock()
	r.job.Ended = time.Now()
	switch {
	case ctx.Err() != nil:
		r.job.State, r.job.Error = Cancelled, "annulé à la demande"
	case err != nil:
		r.job.State, r.job.Error = Failed, err.Error()
	default:
		r.job.State, r.job.Progress = Succeeded, 100
	}
	if res != nil {
		if b, e := json.Marshal(res); e == nil {
			r.job.Result = b
		}
	}
	if m.Summarize != nil && r.job.Result != nil && r.job.State == Succeeded {
		r.job.Summary = m.Summarize(r.job.Profile, r.job.Result)
	}
	st, msg := r.job.State, r.job.Error
	r.mu.Unlock()
	m.persist(r.snapshot()) // avant de notifier la fin : un abonné qui relit le job le trouve
	if m.OnDone != nil {
		go m.OnDone(r.snapshot())
	}
	if res != nil {
		r.emit(Event{Type: "result", Data: r.job.Result})
	}
	r.emit(Event{Type: "state", State: st, Msg: msg})
	log.Printf("jobs: %s (%s) terminé : %s %s", r.job.ID, r.job.Profile, st, msg)
	r.mu.Lock()
	for ch := range r.subs {
		close(ch)
	}
	r.subs = map[chan Event]struct{}{}
	r.mu.Unlock()
}

func (m *Manager) persist(j Job) {
	if m.store == nil {
		return
	}
	if err := m.store.Save("job-"+j.ID+".json", j); err != nil {
		log.Printf("jobs: persistance de %s : %v", j.ID, err)
	}
	// rétention : les m.keep jobs manuels et les m.keepScheduled jobs planifiés les plus récents
	files, _ := filepath.Glob(m.store.Path("job-*.json"))
	var manual, sched []string
	for _, f := range files {
		if strings.HasSuffix(f, "s.json") {
			sched = append(sched, f)
		} else {
			manual = append(manual, f)
		}
	}
	for _, l := range []struct {
		f    []string
		keep int
	}{{manual, m.keep}, {sched, m.keepScheduled}} {
		if len(l.f) > l.keep {
			sort.Strings(l.f)
			for _, f := range l.f[:len(l.f)-l.keep] {
				os.Remove(f)
			}
		}
	}
}

// Get renvoie un job (en mémoire ou persisté).
func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	r, ok := m.recent[id]
	m.mu.Unlock()
	if ok {
		j := r.snapshot()
		return &j, true
	}
	if m.store == nil || strings.ContainsAny(id, "/\\.") {
		return nil, false
	}
	var j Job
	if err := m.store.Load("job-"+id+".json", &j); err != nil {
		return nil, false
	}
	return &j, true
}

// Current renvoie le job en cours, s'il y en a un.
func (m *Manager) Current() *Job {
	m.mu.Lock()
	r := m.current
	m.mu.Unlock()
	if r == nil {
		return nil
	}
	j := r.snapshot()
	if j.State.Done() {
		return nil
	}
	return &j
}

// List renvoie les jobs (plus récents d'abord), filtrés par profil si non vide, sans journal ni résultat.
func (m *Manager) List(profile string, limit int) []Job {
	seen := map[string]bool{}
	var out []Job
	m.mu.Lock()
	for _, r := range m.recent {
		j := r.snapshot()
		seen[j.ID] = true
		out = append(out, j)
	}
	m.mu.Unlock()
	if m.store != nil {
		files, _ := filepath.Glob(m.store.Path("job-*.json"))
		for _, f := range files {
			id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "job-"), ".json")
			if seen[id] {
				continue
			}
			var j Job
			if err := m.store.Load(filepath.Base(f), &j); err == nil {
				out = append(out, j)
			}
		}
	}
	res := []Job{} // jamais null en JSON
	for _, j := range out {
		if profile != "" && j.Profile != profile {
			continue
		}
		if j.Summary == nil && j.Result != nil && j.State == Succeeded && m.Summarize != nil {
			j.Summary = m.Summarize(j.Profile, j.Result)
		}
		j.Log, j.Result = nil, nil
		res = append(res, j)
	}
	sort.Slice(res, func(i, k int) bool { return res[i].Created.After(res[k].Created) })
	if limit > 0 && len(res) > limit {
		res = res[:limit]
	}
	return res
}

// Cancel demande l'annulation d'un job en cours ; le job se termine proprement (worker arrêté,
// disque conservé, tâche vCenter en cours attendue).
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	r, ok := m.recent[id]
	m.mu.Unlock()
	if !ok {
		return errors.New("job inconnu ou terminé")
	}
	if r.snapshot().State.Done() {
		return errors.New("job déjà terminé")
	}
	rep := &Reporter{j: r}
	rep.Logf("annulation demandée")
	r.cancel()
	return nil
}

// Subscribe renvoie l'historique des événements depuis after (numéro de séquence) et un canal des
// suivants, fermé à la fin du job. unsubscribe doit être appelé.
func (m *Manager) Subscribe(id string, after int) (history []Event, ch <-chan Event, unsubscribe func(), ok bool) {
	m.mu.Lock()
	r, found := m.recent[id]
	m.mu.Unlock()
	if !found {
		return nil, nil, func() {}, false
	}
	c := make(chan Event, 256)
	r.mu.Lock()
	for _, e := range r.events {
		if e.Seq > after {
			history = append(history, e)
		}
	}
	if r.job.State.Done() {
		close(c)
	} else {
		r.subs[c] = struct{}{}
	}
	r.mu.Unlock()
	return history, c, func() {
		r.mu.Lock()
		if _, ok := r.subs[c]; ok {
			delete(r.subs, c)
			close(c)
		}
		r.mu.Unlock()
	}, true
}

// Wait attend la fin d'un job de la session (tests).
func (m *Manager) Wait(id string) {
	m.mu.Lock()
	r, ok := m.recent[id]
	m.mu.Unlock()
	if ok {
		<-r.done
	}
}
