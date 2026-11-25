package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Kind est le motif d'un palier.
type Kind string

const (
	Prefill   Kind = "prefill"   // écriture séquentielle unique de toute la zone (données incompressibles)
	SeqWrite  Kind = "seqwrite"  // écriture séquentielle en boucle, au temps
	SeqRead   Kind = "seqread"   // lecture séquentielle en boucle, au temps
	RandWrite Kind = "randwrite" // écriture aléatoire alignée
	RandRead  Kind = "randread"  // lecture aléatoire alignée
	RandMix   Kind = "randmix"   // mixte aléatoire, ReadPct % de lectures
)

// ZoneStart : le bloc 0 (signature) n'est jamais touché, la zone de bench commence à 1 Mio.
const ZoneStart = 1 << 20

// Step décrit un palier.
type Step struct {
	Kind     Kind   `json:"kind"`
	Duration int    `json:"duration_s"`         // durée de mesure (ignorée pour prefill)
	Warmup   int    `json:"warmup_s,omitempty"` // chauffe non comptée
	BlockKiB int    `json:"block_kib"`          // 4, 8, 64, 256, 1024
	QD       int    `json:"qd"`                 // profondeur de file = goroutines
	ReadPct  int    `json:"read_pct,omitempty"` // randmix : part des lectures
	Seed     uint64 `json:"seed,omitempty"`     // graine (journalisée) ; 0 = dérivée de l'heure
	Name     string `json:"name,omitempty"`     // libellé libre (ex. « 4k QD16 lecture »)
}

// Second est l'agrégat d'une seconde de mesure.
type Second struct {
	T      int       `json:"t"`  // seconde depuis le début du palier (T<=0 pendant la chauffe)
	At     time.Time `json:"at"` // fin de la seconde, horloge de l'appliance
	Bytes  int64     `json:"bytes"`
	Ops    int64     `json:"ops"`
	Errors int64     `json:"errors"`
	P99US  float64   `json:"p99_us"`
	MaxUS  float64   `json:"max_us"`
}

// Result est le résultat d'un palier.
type Result struct {
	Step      Step        `json:"step"`
	Started   time.Time   `json:"started"`
	Ended     time.Time   `json:"ended"`
	Measured  float64     `json:"measured_s"` // durée effectivement mesurée (hors chauffe)
	Bytes     int64       `json:"bytes"`
	Ops       int64       `json:"ops"`
	Errors    int64       `json:"errors"`
	IOPS      float64     `json:"iops"`
	MBps      float64     `json:"mbps"` // 10^6 octets/s
	Latency   Latency     `json:"latency"`
	CPUPct    float64     `json:"cpu_pct"` // usage CPU du worker pendant la mesure (100 = un cœur)
	Profile   Profile     `json:"profile"` // profil d'I/O effectif (à afficher avec chaque résultat)
	Seconds   []Second    `json:"seconds"`
	Buckets   [][2]uint64 `json:"buckets,omitempty"` // histogramme (borne ns, effectif)
	ZoneBytes int64       `json:"zone_bytes"`
	Seed      uint64      `json:"seed"`
	Aborted   bool        `json:"aborted,omitempty"`
	latency   *Hist
}

// Target est le périphérique ouvert sur lequel on mesure.
type Target struct {
	File      *os.File
	SizeBytes int64
	Direct    bool // O_DIRECT effectif
	Sync      bool // O_SYNC effectif
}

// Profile décrit le profil d'I/O effectif d'un palier, tel qu'il doit être présenté avec les chiffres.
type Profile struct {
	Pattern   string `json:"pattern"` // séquentiel, aléatoire uniforme, mixte
	BlockKiB  int    `json:"block_kib"`
	QD        int    `json:"qd"`
	ReadPct   int    `json:"read_pct"` // 0 = écriture pure, 100 = lecture pure
	Duration  int    `json:"duration_s"`
	Warmup    int    `json:"warmup_s"`
	ZoneBytes int64  `json:"zone_bytes"`
	Direct    bool   `json:"o_direct"`
	Sync      bool   `json:"o_sync"`
	Data      string `json:"data"` // nature des données écrites
	Text      string `json:"text"` // résumé lisible
}

// OpenTarget ouvre le périphérique en O_DIRECT (et O_SYNC si demandé). allowBuffered n'est là que
// pour les tests sur un système de fichiers sans O_DIRECT : jamais en production.
func OpenTarget(path string, size int64, sync, allowBuffered bool) (*Target, error) {
	flags := os.O_RDWR | syscall.O_DIRECT
	if sync {
		flags |= syscall.O_SYNC
	}
	f, err := os.OpenFile(path, flags, 0)
	if err != nil && allowBuffered && errors.Is(err, syscall.EINVAL) {
		f, err = os.OpenFile(path, os.O_RDWR, 0)
		if err == nil {
			return &Target{File: f, SizeBytes: size, Direct: false, Sync: false}, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("ouverture de %s : %w", path, err)
	}
	return &Target{File: f, SizeBytes: size, Direct: true, Sync: sync}, nil
}

// Reporter reçoit les secondes au fil de l'eau.
type Reporter func(s Second)

// Run exécute un palier sur la cible. Les goroutines sont verrouillées sur leur thread et disposent
// chacune d'un tampon aligné ; les I/O sont synchrones (pread/pwrite), la profondeur de file est le
// nombre de goroutines (§6.2).
func Run(ctx context.Context, tgt *Target, st Step, pool *Pool, report Reporter) (*Result, error) {
	if st.BlockKiB <= 0 {
		st.BlockKiB = 1024
	}
	if st.QD <= 0 {
		st.QD = 1
	}
	if st.Seed == 0 {
		st.Seed = uint64(time.Now().UnixNano())
	}
	block := int64(st.BlockKiB) << 10
	zone := (tgt.SizeBytes - ZoneStart) / block * block
	if zone <= 0 {
		return nil, fmt.Errorf("zone de bench vide (disque de %d octets, bloc %d)", tgt.SizeBytes, block)
	}
	nblocks := zone / block
	res := &Result{Step: st, Started: time.Now(), ZoneBytes: zone, Seed: st.Seed, latency: &Hist{}}
	res.Profile = profileOf(st, zone, tgt)
	fd := int(tgt.File.Fd())

	var (
		stop     atomic.Bool
		measure  atomic.Bool
		seq      atomic.Int64 // compteur de blocs séquentiels partagé
		secBytes atomic.Int64
		secOps   atomic.Int64
		secErr   atomic.Int64
		totBytes atomic.Int64
		totOps   atomic.Int64
		totErr   atomic.Int64
		hmu      sync.Mutex
		secHist  Hist
		wg       sync.WaitGroup
	)
	cpu0 := cpuTime()
	t0 := time.Now()

	worker := func(id int) {
		defer wg.Done()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		buf := alignedBuffer(int(block))
		r := newRNG(st.Seed ^ (uint64(id)+1)*0x9E3779B97F4A7C15)
		var local Hist
		flush := func() {
			hmu.Lock()
			secHist.Merge(&local)
			hmu.Unlock()
			local.Reset()
		}
		lastFlush := time.Now()
		for !stop.Load() {
			var off int64
			var salt uint64
			write := false
			switch st.Kind {
			case Prefill, SeqWrite, SeqRead:
				n := seq.Add(1) - 1
				if st.Kind == Prefill && n >= nblocks {
					stop.Store(true)
					break
				}
				off = ZoneStart + (n%nblocks)*block
				write = st.Kind != SeqRead
				// une passe complète sur la zone = un nouveau contenu
				salt = st.Seed ^ uint64(n/nblocks+1)*0xBF58476D1CE4E5B9
			case RandWrite, RandRead, RandMix:
				off = ZoneStart + int64(r.next()%uint64(nblocks))*block
				switch st.Kind {
				case RandWrite:
					write = true
				case RandMix:
					write = int(r.next()%100) >= st.ReadPct
				}
				salt = r.next() // chaque écriture aléatoire a son propre contenu
			}
			if stop.Load() {
				break
			}
			if write {
				pool.Fill(buf, off, salt)
			}
			start := time.Now()
			var n int
			var err error
			if write {
				n, err = syscall.Pwrite(fd, buf, off)
			} else {
				n, err = syscall.Pread(fd, buf, off)
			}
			lat := uint64(time.Since(start).Nanoseconds())
			if err != nil || n != len(buf) {
				secErr.Add(1)
				if measure.Load() {
					totErr.Add(1)
				}
				continue
			}
			local.Add(lat)
			secBytes.Add(int64(n))
			secOps.Add(1)
			if measure.Load() {
				totBytes.Add(int64(n))
				totOps.Add(1)
			}
			if time.Since(lastFlush) > 200*time.Millisecond {
				flush()
				lastFlush = time.Now()
			}
		}
		flush()
	}
	for i := 0; i < st.QD; i++ {
		wg.Add(1)
		go worker(i)
	}

	// Horloge : une seconde = un échantillon. Chauffe (T<0) puis mesure.
	total := st.Warmup + st.Duration
	if st.Kind == Prefill {
		total = 1 << 30 // jusqu'à la fin de la zone
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	sec := 0
	var cpuStart float64
	var measureStart time.Time
	if st.Warmup == 0 || st.Kind == Prefill {
		measure.Store(true)
		cpuStart = cpu0
		measureStart = t0
	}
	finish := func() {
		stop.Store(true)
		<-done
	}
loop:
	for {
		select {
		case <-ctx.Done():
			res.Aborted = true
			finish()
			break loop
		case <-done:
			break loop
		case <-ticker.C:
			sec++
			hmu.Lock()
			h := secHist
			secHist.Reset()
			hmu.Unlock()
			s := Second{T: sec - st.Warmup, At: time.Now(), Bytes: secBytes.Swap(0), Ops: secOps.Swap(0), Errors: secErr.Swap(0),
				P99US: float64(h.Percentile(99)) / 1000, MaxUS: float64(h.Max) / 1000}
			if st.Kind == Prefill {
				s.T = sec
			}
			if measure.Load() {
				res.latency.Merge(&h)
				res.Seconds = append(res.Seconds, s)
			}
			if report != nil {
				report(s)
			}
			if !measure.Load() && sec >= st.Warmup {
				measure.Store(true)
				cpuStart = cpuTime()
				measureStart = time.Now()
			}
			if sec >= total {
				finish()
				break loop
			}
		}
	}
	res.Ended = time.Now()
	if measureStart.IsZero() {
		measureStart = t0
		cpuStart = cpu0
	}
	res.Measured = res.Ended.Sub(measureStart).Seconds()
	res.Bytes, res.Ops, res.Errors = totBytes.Load(), totOps.Load(), totErr.Load()
	if res.Measured > 0 {
		res.IOPS = float64(res.Ops) / res.Measured
		res.MBps = float64(res.Bytes) / res.Measured / 1e6
		res.CPUPct = (cpuTime() - cpuStart) / res.Measured * 100
	}
	res.Latency = res.latency.Summary()
	res.Buckets = res.latency.Buckets()
	return res, nil
}

func profileOf(st Step, zone int64, tgt *Target) Profile {
	p := Profile{BlockKiB: st.BlockKiB, QD: st.QD, Duration: st.Duration, Warmup: st.Warmup, ZoneBytes: zone, Direct: tgt.Direct, Sync: tgt.Sync}
	data := "aléatoires incompressibles, contenu nouveau à chaque passe, secteurs de 4 Kio tous distincts"
	switch st.Kind {
	case Prefill:
		p.Pattern, p.ReadPct, p.Data, p.Duration = "séquentielle, une passe sur toute la zone", 0, data, 0
	case SeqWrite:
		p.Pattern, p.ReadPct, p.Data = "séquentielle en boucle sur la zone", 0, data
	case SeqRead:
		p.Pattern, p.ReadPct = "séquentielle en boucle sur la zone", 100
	case RandWrite:
		p.Pattern, p.ReadPct, p.Data = "aléatoire uniforme, aligné sur le bloc", 0, "aléatoires incompressibles, contenu nouveau à chaque écriture"
	case RandRead:
		p.Pattern, p.ReadPct = "aléatoire uniforme, aligné sur le bloc", 100
	case RandMix:
		p.Pattern, p.ReadPct, p.Data = "aléatoire uniforme, aligné sur le bloc", st.ReadPct, "aléatoires incompressibles, contenu nouveau à chaque écriture"
	}
	sens := "écriture"
	switch {
	case p.ReadPct == 100:
		sens = "lecture"
	case p.ReadPct > 0:
		sens = fmt.Sprintf("mixte %d/%d lecture/écriture", p.ReadPct, 100-p.ReadPct)
	}
	flags := "O_DIRECT"
	if !p.Direct {
		flags = "SANS O_DIRECT (tests uniquement)"
	}
	if p.Sync {
		flags += "+O_SYNC"
	} else {
		flags += " sans O_SYNC"
	}
	dur := "jusqu'à la fin de la zone"
	if st.Kind != Prefill {
		dur = fmt.Sprintf("%d s", st.Duration)
		if st.Warmup > 0 {
			dur += fmt.Sprintf(" après %d s de chauffe", st.Warmup)
		}
	}
	p.Text = fmt.Sprintf("%s %s, bloc %d Kio, QD %d, %s, zone %.2f Gio, %s", sens, p.Pattern, p.BlockKiB, p.QD, dur, float64(zone)/(1<<30), flags)
	if p.Data != "" {
		p.Text += ", données " + p.Data
	}
	return p
}

// cpuTime renvoie le temps CPU (utilisateur + système) du processus, en secondes.
func cpuTime() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6 + float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
}

// alignedBuffer renvoie un tampon aligné sur 4 Kio (exigence d'O_DIRECT).
func alignedBuffer(size int) []byte {
	raw := make([]byte, size+4096)
	off := 0
	if rem := int(uintptrOf(raw) % 4096); rem != 0 {
		off = 4096 - rem
	}
	return raw[off : off+size]
}
