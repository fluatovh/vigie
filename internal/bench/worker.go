package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"vigie/internal/testdisk"
)

// Request est ce que le serveur envoie au worker (JSON sur stdin). Le worker retrouve LUI-MÊME le
// périphérique à partir de l'UUID du VMDK et refait tous les contrôles : aucun chemin de
// périphérique n'est transmis.
type Request struct {
	Record  testdisk.Record `json:"record"`
	Prefill bool            `json:"prefill"` // préremplir si la signature ne l'indique pas déjà
	Sync    bool            `json:"sync"`    // O_SYNC en plus d'O_DIRECT
	Steps   []Step          `json:"steps"`
	Seed    uint64          `json:"seed,omitempty"` // graine du réservoir de données
	// Réservé aux tests : vue sysfs/procfs simulée et O_DIRECT optionnel.
	TestGuest     *testdisk.Guest `json:"test_guest,omitempty"`
	AllowBuffered bool            `json:"allow_buffered,omitempty"`
}

// Event est une ligne JSON émise par le worker.
type Event struct {
	Type    string             `json:"type"` // start, checks, prefill, second, step, done, error
	Time    time.Time          `json:"time"`
	Msg     string             `json:"msg,omitempty"`
	Step    int                `json:"step,omitempty"` // index du palier (1..n)
	Kind    Kind               `json:"kind,omitempty"`
	Second  *Second            `json:"second,omitempty"`
	Result  *Result            `json:"result,omitempty"`
	Checks  *testdisk.Verified `json:"checks,omitempty"`
	Percent float64            `json:"percent,omitempty"` // préremplissage
	CPUs    []int              `json:"cpus,omitempty"`
}

// RunWorker est le corps de `vigie worker` : lit la requête sur in, écrit les événements sur out.
func RunWorker(ctx context.Context, in io.Reader, out io.Writer) error {
	enc := json.NewEncoder(out)
	emit := func(e Event) {
		e.Time = time.Now()
		_ = enc.Encode(e)
	}
	fail := func(err error) error {
		emit(Event{Type: "error", Msg: err.Error()})
		return err
	}
	var req Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return fail(fmt.Errorf("requête invalide : %w", err))
	}
	cpus := pinToWorkerCPUs()
	emit(Event{Type: "start", Msg: "worker démarré", CPUs: cpus})

	guest := testdisk.DefaultGuest
	if req.TestGuest != nil {
		guest = *req.TestGuest
	}
	dev, err := guest.FindByUUID(req.Record.BackingUUID, 10*time.Second)
	if err != nil {
		return fail(err)
	}
	v := guest.Verify(dev, &req.Record)
	sig, sigErr := testdisk.ReadSignature(dev.Path)
	testdisk.VerifySignature(v, sig, sigErr)
	emit(Event{Type: "checks", Checks: v})
	if !v.OK() {
		return fail(errors.New("contrôles de sécurité en échec : le worker refuse de toucher au disque"))
	}

	tgt, err := OpenTarget(dev.Path, dev.SizeBytes, req.Sync, req.AllowBuffered)
	if err != nil {
		return fail(err)
	}
	defer tgt.File.Close()
	if req.Seed == 0 {
		req.Seed = uint64(time.Now().UnixNano())
	}
	pool := NewPool(req.Seed, PoolSize)

	zone := dev.SizeBytes - ZoneStart
	if req.Prefill && sig.PrefilledBytes < zone {
		emit(Event{Type: "prefill", Msg: "préremplissage en données incompressibles", Percent: 0})
		var written int64
		res, err := Run(ctx, tgt, Step{Kind: Prefill, BlockKiB: 1024, QD: 4, Seed: req.Seed}, pool, func(s Second) {
			written += s.Bytes
			emit(Event{Type: "prefill", Second: &s, Percent: float64(written) / float64(zone) * 100})
		})
		if err != nil {
			return fail(err)
		}
		if res.Aborted {
			return fail(errors.New("préremplissage interrompu"))
		}
		if res.Errors > 0 {
			return fail(fmt.Errorf("préremplissage : %d erreurs d'I/O", res.Errors))
		}
		newSig := *sig
		newSig.PrefilledBytes, newSig.PrefilledAt = res.Bytes, time.Now().UTC()
		if err := testdisk.WriteSignature(v, newSig); err != nil {
			return fail(err)
		}
		emit(Event{Type: "prefill", Result: res, Percent: 100, Msg: fmt.Sprintf("%d octets préremplis à %.0f Mo/s", res.Bytes, res.MBps)})
	}

	for i, st := range req.Steps {
		if st.Kind == Prefill {
			return fail(errors.New("prefill n'est pas un palier : utiliser le champ prefill"))
		}
		if st.Seed == 0 {
			st.Seed = req.Seed + uint64(i+1)
		}
		emit(Event{Type: "step", Step: i + 1, Kind: st.Kind, Msg: "début"})
		res, err := Run(ctx, tgt, st, pool, func(s Second) {
			emit(Event{Type: "second", Step: i + 1, Kind: st.Kind, Second: &s})
		})
		if err != nil {
			return fail(err)
		}
		emit(Event{Type: "step", Step: i + 1, Kind: st.Kind, Result: res})
		if res.Aborted {
			return fail(errors.New("mesure interrompue"))
		}
	}
	emit(Event{Type: "done"})
	return nil
}

// pinToWorkerCPUs épingle le worker sur les CPU 1..n-1 (le serveur et le collecteur restent sur le
// CPU 0, §1.1). Sur une VM à un seul vCPU, rien n'est fait. Renvoie les CPU retenus.
func pinToWorkerCPUs() []int {
	n := runtime.NumCPU()
	if n < 2 {
		return nil
	}
	var set [128]uint64 // cpu_set_t de 1024 bits
	var cpus []int
	for c := 1; c < n && c < 1024; c++ {
		set[c/64] |= 1 << (uint(c) % 64)
		cpus = append(cpus, c)
	}
	_, _, errno := syscall.RawSyscall(syscall.SYS_SCHED_SETAFFINITY, 0, uintptr(len(set)*8), uintptr(unsafe.Pointer(&set[0])))
	if errno != 0 {
		return nil
	}
	return cpus
}

// Allow the worker to be identified in process listings.
var _ = os.Getpid
