// Package results garde l'historique compact des mesures (phase 9) : une ligne JSON par mesure
// (test standard, avancé, tournée, local, manuel ou planifié) dans /data/vigie/results.jsonl, pour les
// graphs de tendance par hôte et datastore et l'export CSV/JSON. Rétention en jours.
package results

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// File est le nom du fichier dans le répertoire de données.
const File = "results.jsonl"

// Point est une mesure (une répétition, un passage de tournée…).
type Point struct {
	T            time.Time `json:"t"`
	Job          string    `json:"job"`
	Profile      string    `json:"profile"` // standard, advanced, tour, local
	By           string    `json:"by,omitempty"`
	Scheduled    bool      `json:"scheduled,omitempty"`
	Host         string    `json:"host"`
	Datastore    string    `json:"datastore"`
	WriteMBps    float64   `json:"write_mbps"`
	ReadMBps     float64   `json:"read_mbps"`
	WriteP99Ms   float64   `json:"write_p99_ms"`
	ReadP99Ms    float64   `json:"read_p99_ms"`
	TxRatio      *float64  `json:"tx_ratio,omitempty"`
	DSWriteLatMs *float64  `json:"ds_write_latency_ms,omitempty"`
	NICErrors    *float64  `json:"nic_errors,omitempty"`
	UsefulIOPS   *float64  `json:"useful_iops,omitempty"`
	QD1WriteP99  *float64  `json:"qd1_write_p99_ms,omitempty"`
	QD1ReadP99   *float64  `json:"qd1_read_p99_ms,omitempty"`
	Level        string    `json:"level"`
	Triggered    []string  `json:"triggered,omitempty"`
	IOProfile    string    `json:"io_profile,omitempty"` // profil d'I/O de l'écriture (texte)
}

// Store est l'historique sur disque (écritures sérialisées).
type Store struct {
	path string
	mu   sync.Mutex
}

// Open ouvre (sans le créer) l'historique dans dir.
func Open(dir string) *Store { return &Store{path: filepath.Join(dir, File)} }

// Exists indique si le fichier existe (sinon : reconstruction depuis les jobs).
func (s *Store) Exists() bool {
	_, err := os.Stat(s.path)
	return err == nil
}

// Append ajoute des points (une ligne chacun).
func (s *Store) Append(pts []Point) error {
	if len(pts) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	var b bytes.Buffer
	for _, p := range pts {
		line, err := json.Marshal(p)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	_, err = f.Write(b.Bytes())
	return err
}

// Reset remplace tout l'historique (reconstruction depuis les jobs) ; crée le fichier même vide.
func (s *Store) Reset(pts []Point) error {
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].T.Before(pts[j].T) })
	var b bytes.Buffer
	for _, p := range pts {
		line, err := json.Marshal(p)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Filter sélectionne des points.
type Filter struct {
	From, To  time.Time
	Host      string
	Datastore string
}

func (f Filter) match(p Point) bool {
	return (f.From.IsZero() || !p.T.Before(f.From)) && (f.To.IsZero() || !p.T.After(f.To)) &&
		(f.Host == "" || p.Host == f.Host) && (f.Datastore == "" || p.Datastore == f.Datastore)
}

func (s *Store) read() ([]Point, error) {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Point
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var p Point
		if json.Unmarshal(sc.Bytes(), &p) == nil {
			out = append(out, p)
		}
	}
	return out, sc.Err()
}

// Query renvoie les points du filtre, triés par date.
func (s *Store) Query(f Filter) ([]Point, error) {
	s.mu.Lock()
	all, err := s.read()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := []Point{}
	for _, p := range all {
		if f.match(p) {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out, nil
}

// Series : couples hôte / datastore présents dans l'historique.
func (s *Store) Series() ([][2]string, error) {
	pts, err := s.Query(Filter{})
	if err != nil {
		return nil, err
	}
	seen := map[[2]string]bool{}
	out := [][2]string{}
	for _, p := range pts {
		k := [2]string{p.Host, p.Datastore}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out, nil
}

// Prune retire les points plus vieux que days jours ; renvoie le nombre retiré.
func (s *Store) Prune(days int, now time.Time) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.read()
	if err != nil {
		return 0, err
	}
	cut := now.Add(-time.Duration(days) * 24 * time.Hour)
	var keep bytes.Buffer
	n := 0
	for _, p := range all {
		if p.T.Before(cut) {
			n++
			continue
		}
		line, _ := json.Marshal(p)
		keep.Write(line)
		keep.WriteByte('\n')
	}
	if n == 0 {
		return 0, nil
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, keep.Bytes(), 0o600); err != nil {
		return 0, err
	}
	return n, os.Rename(tmp, s.path)
}

func opt(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 3, 64)
}

// WriteCSV exporte des points (séparateur virgule, décimales avec un point, dates RFC 3339 UTC).
func WriteCSV(w io.Writer, pts []Point) error {
	c := csv.NewWriter(w)
	_ = c.Write([]string{"time_utc", "job", "profile", "scheduled", "host", "datastore", "write_mbps", "read_mbps", "write_p99_ms", "read_p99_ms",
		"tx_ratio", "ds_write_latency_ms", "nic_errors", "useful_iops", "qd1_write_p99_ms", "qd1_read_p99_ms", "level", "triggered", "io_profile"})
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	for _, p := range pts {
		trig := ""
		for i, t := range p.Triggered {
			if i > 0 {
				trig += " | "
			}
			trig += t
		}
		_ = c.Write([]string{p.T.UTC().Format(time.RFC3339), p.Job, p.Profile, strconv.FormatBool(p.Scheduled), p.Host, p.Datastore,
			f(p.WriteMBps), f(p.ReadMBps), f(p.WriteP99Ms), f(p.ReadP99Ms), opt(p.TxRatio), opt(p.DSWriteLatMs), opt(p.NICErrors),
			opt(p.UsefulIOPS), opt(p.QD1WriteP99), opt(p.QD1ReadP99), p.Level, trig, p.IOProfile})
	}
	c.Flush()
	return c.Error()
}
