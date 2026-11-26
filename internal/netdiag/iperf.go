package netdiag

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// IperfBinary est le chemin d'iperf3 (paquet Alpine iperf3, 3.20).
var IperfBinary = "iperf3"

// IperfPort est le port du serveur iperf3 de l'appliance.
const IperfPort = 5201

// Server pilote le serveur iperf3 local, démarré à la demande et arrêté automatiquement.
type Server struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	until   time.Time
	started time.Time
	by      string
}

// ServerStatus est l'état du serveur local.
type ServerStatus struct {
	Running bool      `json:"running"`
	Port    int       `json:"port"`
	Until   time.Time `json:"until,omitempty"`
	Started time.Time `json:"started,omitempty"`
	By      string    `json:"by,omitempty"`
}

// Status renvoie l'état du serveur.
func (s *Server) Status() ServerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return ServerStatus{Port: IperfPort}
	}
	return ServerStatus{Running: true, Port: IperfPort, Until: s.until, Started: s.started, By: s.by}
}

// Start démarre (ou prolonge) le serveur pour la durée donnée (10 s à 1 h).
func (s *Server) Start(d time.Duration, by string) (ServerStatus, error) {
	if d < 10*time.Second || d > time.Hour {
		return ServerStatus{}, errors.New("durée du serveur : 10 s à 1 h")
	}
	s.mu.Lock()
	if s.cmd != nil {
		s.until = time.Now().Add(d)
		s.mu.Unlock()
		return s.Status(), nil
	}
	cmd := exec.Command(IperfBinary, "-s", "-p", strconv.Itoa(IperfPort), "--idle-timeout", strconv.Itoa(int(d.Seconds())))
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return ServerStatus{}, fmt.Errorf("iperf3 -s : %w", err)
	}
	s.cmd, s.started, s.until, s.by = cmd, time.Now(), time.Now().Add(d), by
	log.Printf("netdiag: serveur iperf3 démarré sur le port %d jusqu'à %s (%s)", IperfPort, s.until.Format(time.TimeOnly), by)
	s.mu.Unlock()
	go func() {
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		for {
			s.mu.Lock()
			until := s.until
			s.mu.Unlock()
			select {
			case <-done:
				s.mu.Lock()
				if s.cmd == cmd {
					s.cmd = nil
				}
				s.mu.Unlock()
				log.Printf("netdiag: serveur iperf3 arrêté")
				return
			case <-time.After(time.Until(until)):
				s.mu.Lock()
				if time.Now().After(s.until) && s.cmd == cmd {
					_ = cmd.Process.Kill()
				}
				s.mu.Unlock()
			}
		}
	}()
	return s.Status(), nil
}

// Stop arrête le serveur.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		_ = s.cmd.Process.Kill()
		s.until = time.Now()
	}
}

// IperfOptions paramètrent un test client.
type IperfOptions struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // tcp, udp
	Duration int    `json:"duration_s"`
	Streams  int    `json:"streams"`
	Reverse  bool   `json:"reverse"`          // le serveur émet vers l'appliance (-R)
	Bitrate  string `json:"bitrate"`          // UDP : ex. 1G, 500M (défaut 1G)
	Length   int    `json:"length,omitempty"` // taille des écritures (octets), facultatif
}

// Validate vérifie et complète les options (aucune n'est passée à un shell).
func (o *IperfOptions) Validate() error {
	ip := net.ParseIP(o.Host)
	if ip == nil || ip.To4() == nil {
		return errors.New("adresse IPv4 du serveur iperf3 attendue")
	}
	if o.Port == 0 {
		o.Port = IperfPort
	}
	if o.Port < 1 || o.Port > 65535 {
		return errors.New("port invalide")
	}
	if o.Protocol == "" {
		o.Protocol = "tcp"
	}
	if o.Protocol != "tcp" && o.Protocol != "udp" {
		return errors.New("protocole : tcp ou udp")
	}
	if o.Duration == 0 {
		o.Duration = 10
	}
	if o.Duration < 1 || o.Duration > 600 {
		return errors.New("durée : 1 à 600 s")
	}
	if o.Streams == 0 {
		o.Streams = 1
	}
	if o.Streams < 1 || o.Streams > 32 {
		return errors.New("flux parallèles : 1 à 32")
	}
	if o.Protocol == "udp" && o.Bitrate == "" {
		o.Bitrate = "1G"
	}
	if o.Bitrate != "" {
		n := o.Bitrate
		last := n[len(n)-1]
		if last == 'K' || last == 'M' || last == 'G' || last == 'k' || last == 'm' || last == 'g' {
			n = n[:len(n)-1]
		}
		if v, err := strconv.ParseFloat(n, 64); err != nil || v <= 0 {
			return errors.New("débit cible invalide (ex. 500M, 1G)")
		}
	}
	if o.Length < 0 || o.Length > 1<<20 {
		return errors.New("taille des écritures invalide")
	}
	return nil
}

// Args construit la ligne de commande iperf3.
func (o IperfOptions) Args() []string {
	a := []string{"-c", o.Host, "-p", strconv.Itoa(o.Port), "-t", strconv.Itoa(o.Duration), "-P", strconv.Itoa(o.Streams), "--json-stream", "-f", "m"}
	if o.Reverse {
		a = append(a, "-R")
	}
	if o.Protocol == "udp" {
		a = append(a, "-u")
	}
	if o.Bitrate != "" {
		a = append(a, "-b", o.Bitrate)
	}
	if o.Length > 0 {
		a = append(a, "-l", strconv.Itoa(o.Length))
	}
	return a
}

// Interval est une seconde du test.
type Interval struct {
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Mbps        float64 `json:"mbps"`
	Retransmits int     `json:"retransmits"`
	JitterMs    float64 `json:"jitter_ms"`
	LostPct     float64 `json:"lost_pct"`
}

// IperfResult résume un test.
type IperfResult struct {
	Options     IperfOptions `json:"options"`
	Intervals   []Interval   `json:"intervals"`
	SentMbps    float64      `json:"sent_mbps"`
	RecvMbps    float64      `json:"received_mbps"`
	Retransmits int          `json:"retransmits"`
	JitterMs    float64      `json:"jitter_ms"`
	LostPct     float64      `json:"lost_pct"`
	SenderCPU   float64      `json:"sender_cpu_pct"`
	ReceiverCPU float64      `json:"receiver_cpu_pct"`
	MSS         int          `json:"tcp_mss,omitempty"`
	Version     string       `json:"version,omitempty"`
	Error       string       `json:"error,omitempty"`
	Started     time.Time    `json:"started"`
	Ended       time.Time    `json:"ended"`
}

type streamEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type intervalData struct {
	Sum struct {
		Start       float64 `json:"start"`
		End         float64 `json:"end"`
		BitsPerSec  float64 `json:"bits_per_second"`
		Retransmits int     `json:"retransmits"`
		JitterMs    float64 `json:"jitter_ms"`
		LostPercent float64 `json:"lost_percent"`
	} `json:"sum"`
}

type endData struct {
	SumSent struct {
		BitsPerSec  float64 `json:"bits_per_second"`
		Retransmits int     `json:"retransmits"`
	} `json:"sum_sent"`
	SumReceived struct {
		BitsPerSec float64 `json:"bits_per_second"`
	} `json:"sum_received"`
	Sum struct {
		BitsPerSec  float64 `json:"bits_per_second"`
		JitterMs    float64 `json:"jitter_ms"`
		LostPercent float64 `json:"lost_percent"`
	} `json:"sum"`
	CPU struct {
		Host   float64 `json:"host_total"`
		Remote float64 `json:"remote_total"`
	} `json:"cpu_utilization_percent"`
}

type startData struct {
	Version string `json:"version"`
	TCPMSS  int    `json:"tcp_mss_default"`
}

// RunIperf lance le client iperf3 et rapporte chaque seconde à onInterval.
func RunIperf(ctx context.Context, o IperfOptions, onInterval func(Interval)) (*IperfResult, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	res := &IperfResult{Options: o, Started: time.Now(), Intervals: []Interval{}}
	cmd := exec.CommandContext(ctx, IperfBinary, o.Args()...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("iperf3 : %w", err)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev streamEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "start":
			var sd startData
			if json.Unmarshal(ev.Data, &sd) == nil {
				res.Version, res.MSS = sd.Version, sd.TCPMSS
			}
		case "interval":
			var d intervalData
			if json.Unmarshal(ev.Data, &d) == nil {
				iv := Interval{Start: d.Sum.Start, End: d.Sum.End, Mbps: d.Sum.BitsPerSec / 1e6, Retransmits: d.Sum.Retransmits, JitterMs: d.Sum.JitterMs, LostPct: d.Sum.LostPercent}
				res.Intervals = append(res.Intervals, iv)
				if onInterval != nil {
					onInterval(iv)
				}
			}
		case "end":
			var d endData
			if json.Unmarshal(ev.Data, &d) == nil {
				res.SentMbps, res.RecvMbps, res.Retransmits = d.SumSent.BitsPerSec/1e6, d.SumReceived.BitsPerSec/1e6, d.SumSent.Retransmits
				if o.Protocol == "udp" {
					res.SentMbps, res.RecvMbps, res.JitterMs, res.LostPct = d.Sum.BitsPerSec/1e6, d.Sum.BitsPerSec/1e6*(1-d.Sum.LostPercent/100), d.Sum.JitterMs, d.Sum.LostPercent
				}
				res.SenderCPU, res.ReceiverCPU = d.CPU.Host, d.CPU.Remote
			}
		case "error":
			var msg string
			_ = json.Unmarshal(ev.Data, &msg)
			res.Error = msg
		}
	}
	werr := cmd.Wait()
	res.Ended = time.Now()
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if res.Error != "" {
		return res, fmt.Errorf("iperf3 : %s", res.Error)
	}
	if werr != nil {
		return res, fmt.Errorf("iperf3 : %w", werr)
	}
	return res, nil
}
