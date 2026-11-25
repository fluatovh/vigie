// Command vigie est le binaire unique de l'appliance.
//
// Sous-commandes :
//
//	serve    serveur web + collecteur (CPU 0)
//	worker   moteur de bench (sous-processus, CPU 1..n-1) — phase 4
//	passwd   définit le mot de passe de l'interface web et de l'API (lu sur stdin)
//	version  affiche la version
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vigie/internal/auth"
	"vigie/internal/bench"
	"vigie/internal/cloudinit"
	"vigie/internal/jobs"
	"vigie/internal/netdiag"
	"vigie/internal/report"
	"vigie/internal/store"
	"vigie/internal/sysconf"
	"vigie/internal/testdisk"
	"vigie/internal/tui"
	"vigie/internal/vsphere"
	"vigie/internal/web"
)

// Renseignés par -ldflags au build (voir Makefile).
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "worker":
		// Sous-processus de bench : requête JSON sur stdin, événements JSON sur stdout.
		err = bench.RunWorker(context.Background(), os.Stdin, os.Stdout)
	case "passwd":
		err = passwd(os.Args[2:])
	case "testdisk":
		err = testdiskCmd(os.Args[2:])
	case "tls-init":
		err = tlsInit(os.Args[2:])
	case "tui":
		// Console de l'appliance (tty1) : entièrement au clavier, par le socket de la console.
		sock := "/run/vigie/console.sock"
		if len(os.Args) > 2 {
			sock = os.Args[2]
		}
		err = tui.Main(sock)
	case "sysconf":
		// `vigie sysconf boot -data /data/vigie` : rejoue au boot la configuration réseau choisie en
		// console (appelé par init.sh après les propriétés OVF, avant le réseau).
		err = sysconfCmd(os.Args[2:])
	case "cloud":
		// `vigie cloud apply -data /data/vigie (-dir /mnt/cloud | -url http://169.254.169.254)` :
		// métadonnées d'instance (config-drive, NoCloud, service OpenStack), la console prime.
		err = cloudCmd(os.Args[2:])
	case "report":
		err = reportCmd(os.Args[2:])
	case "version":
		fmt.Printf("vigie %s (commit %s, build %s)\n", version, commit, buildDate)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("%s : %v", os.Args[1], err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: vigie serve [-listen addr] [-data dir] | passwd [-data dir] (mot de passe sur stdin) | testdisk [-data dir] [-uuid U -size N] | tui [socket] | report job.json [job2.json] > rapport.html | worker | version")
}

func dataFlag(fs *flag.FlagSet) *string {
	def := os.Getenv("VIGIE_DATA")
	if def == "" {
		def = "/data/vigie"
	}
	return fs.String("data", def, "répertoire de persistance")
}

func passwd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	data := dataFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		return err
	}
	pw := strings.TrimRight(string(raw), "\r\n")
	st, err := store.New(*data)
	if err != nil {
		return err
	}
	if err := auth.New(st).Set(pw); err != nil {
		return err
	}
	fmt.Println("mot de passe enregistré")
	return nil
}

// testdiskCmd affiche l'état du disque de test vu de l'invité (contrôles de sécurité, signature),
// sans vSphere. Avec -uuid et -size, il simule un enregistrement : utile pour la QA sous QEMU.
func testdiskCmd(args []string) error {
	fs := flag.NewFlagSet("testdisk", flag.ExitOnError)
	data := dataFlag(fs)
	uuid := fs.String("uuid", "", "UUID du VMDK (simule un enregistrement)")
	size := fs.Int64("size", 0, "taille attendue en octets (avec -uuid)")
	sign := fs.Bool("sign", false, "écrire la signature si les contrôles passent (avec -uuid)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.New(*data)
	if err != nil {
		return err
	}
	svc := testdisk.NewService(st)
	if *uuid != "" {
		rec := &testdisk.Record{BackingUUID: *uuid, SizeBytes: *size, UnitNumber: 1, FileName: "(simulé)"}
		dev, err := svc.Guest.FindByUUID(*uuid, 15*time.Second)
		if err != nil {
			return err
		}
		v := svc.Guest.Verify(dev, rec)
		if *sign && v.OK() {
			if err := testdisk.WriteSignature(v, testdisk.Signature{UUID: dev.UUID, CreatedAt: time.Now().UTC(), SizeBytes: *size}); err != nil {
				return err
			}
		}
		sig, err := testdisk.ReadSignature(dev.Path)
		testdisk.VerifySignature(v, sig, err)
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"device": dev, "checks": v.Checks, "ok": v.OK(), "signature": sig})
	}
	return json.NewEncoder(os.Stdout).Encode(svc.Status(context.Background(), nil))
}

// tlsInit génère le certificat s'il n'existe pas et affiche son empreinte (appelé au boot par
// vigie-init, pour que la console l'affiche).
func tlsInit(args []string) error {
	fs := flag.NewFlagSet("tls-init", flag.ExitOnError)
	data := dataFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.New(*data)
	if err != nil {
		return err
	}
	_, fp, err := web.LoadOrCreateCert(st)
	if err != nil {
		return err
	}
	fmt.Println(fp)
	return nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", ":80", "adresse d'écoute HTTP (redirection vers HTTPS et /api/health)")
	listenTLS := fs.String("listen-tls", ":443", "adresse d'écoute HTTPS (vide : HTTP seul, tests)")
	consoleSock := fs.String("console-socket", "/run/vigie/console.sock", "socket Unix de la console (TUI de tty1, root seul ; vide : désactivé)")
	data := dataFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.New(*data)
	if err != nil {
		return err
	}
	var cert tls.Certificate
	var fp string
	if *listenTLS != "" {
		if cert, fp, err = web.LoadOrCreateCert(st); err != nil {
			return fmt.Errorf("certificat TLS : %w", err)
		}
	}
	netMon := netdiag.NewMonitor(st)
	passwords := auth.New(st)
	if !passwords.IsSet() {
		log.Printf("ATTENTION : aucun mot de passe défini dans %s, l'API refusera toute requête", *data)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	handler := web.New(web.Info{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
		Started:   time.Now(),
	}, web.Deps{
		Auth:           passwords,
		VSphere:        vsphere.NewManager(),
		Settings:       web.NewSettings(st),
		TestDisk:       testdisk.NewService(st),
		Jobs:           jobs.NewManager(st),
		Tokens:         auth.NewTokens(st),
		TLSFingerprint: fp,
		NetMonitor:     netMon,
		Peers:          netdiag.NewPeers(st),
		SysConf:        sysconf.Default(*data),
		Background:     ctx,
	})
	plainHandler := handler
	if *listenTLS != "" {
		plainHandler = web.RedirectHandler(handler)
	}
	srv := &http.Server{Addr: *listen, Handler: plainHandler, ReadHeaderTimeout: 10 * time.Second}
	var tlsSrv *http.Server
	if *listenTLS != "" {
		tlsSrv = &http.Server{Addr: *listenTLS, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	}

	netMon.Start(ctx) // surveillance ping continue (persistée toutes les 5 min et à l'arrêt)

	errc := make(chan error, 3)
	go func() {
		log.Printf("vigie %s : écoute HTTP sur %s, données dans %s", version, *listen, *data)
		errc <- srv.ListenAndServe()
	}()
	if tlsSrv != nil {
		go func() {
			log.Printf("vigie %s : écoute HTTPS sur %s, empreinte %s", version, *listenTLS, fp)
			errc <- tlsSrv.ListenAndServeTLS("", "")
		}()
	}

	if *consoleSock != "" {
		if err := os.MkdirAll(filepath.Dir(*consoleSock), 0o700); err != nil {
			return err
		}
		_ = os.Remove(*consoleSock)
		old := syscall.Umask(0o077)
		ln, err := net.Listen("unix", *consoleSock)
		syscall.Umask(old)
		if err != nil {
			return fmt.Errorf("socket console : %w", err)
		}
		_ = os.Chmod(*consoleSock, 0o600)
		csrv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ConnContext: web.ConsoleConn}
		go func() { errc <- csrv.Serve(ln) }()
		defer csrv.Close()
		log.Printf("vigie : socket console %s", *consoleSock)
	}

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Print("arrêt demandé")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if tlsSrv != nil {
			_ = tlsSrv.Shutdown(shutdownCtx)
		}
		netMon.Flush()
		return srv.Shutdown(shutdownCtx)
	}
}

// reportCmd génère le rapport client (ou l'avant/après) d'un ou deux jobs exportés en JSON
// (GET /api/jobs/{id}), hors appliance : outil de QA et de reprise d'un résultat archivé.
func reportCmd(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage : vigie report job.json [job2.json] > rapport.html")
	}
	var js []*jobs.Job
	for _, f := range args {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var j jobs.Job
		if err := json.Unmarshal(b, &j); err != nil {
			return fmt.Errorf("%s : %w", f, err)
		}
		js = append(js, &j)
	}
	var brand report.Branding
	if b := os.Getenv("VIGIE_BRANDING"); b != "" {
		if err := json.Unmarshal([]byte(b), &brand); err != nil {
			return fmt.Errorf("VIGIE_BRANDING : %w", err)
		}
		if err := brand.Validate(); err != nil {
			return err
		}
	}
	meta := report.Meta{Version: version, Now: time.Now()}
	var p *report.Page
	var err error
	if len(js) == 1 {
		p, err = report.Build(js[0], brand, meta)
	} else {
		p, err = report.BuildCompare(js[0], js[1], brand, meta)
	}
	if err != nil {
		return err
	}
	_, err = report.Render(os.Stdout, p)
	return err
}

func sysconfCmd(args []string) error {
	if len(args) < 1 || args[0] != "boot" {
		return fmt.Errorf("usage : vigie sysconf boot [-data dir]")
	}
	fs := flag.NewFlagSet("sysconf", flag.ExitOnError)
	data := dataFlag(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	applied, err := sysconf.Default(*data).Boot()
	if applied {
		fmt.Println("console")
	}
	return err
}

func cloudCmd(args []string) error {
	if len(args) < 1 || args[0] != "apply" {
		return fmt.Errorf("usage : vigie cloud apply [-data dir] (-dir point_de_montage | -url base)")
	}
	fs := flag.NewFlagSet("cloud", flag.ExitOnError)
	data := dataFlag(fs)
	dir := fs.String("dir", "", "source montée (config-drive ou NoCloud)")
	base := fs.String("url", "", "service de métadonnées (http://169.254.169.254)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	var c *cloudinit.Config
	var err error
	switch {
	case *dir != "":
		c, err = cloudinit.FromDir(*dir)
	case *base != "":
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c, err = cloudinit.FromMetadata(ctx, *base)
	default:
		return fmt.Errorf("-dir ou -url requis")
	}
	if err != nil {
		return err
	}
	st, err := store.New(*data)
	if err != nil {
		return err
	}
	sys := sysconf.Default(*data)
	saved := sys.Saved()
	_, passLocal := os.Stat(filepath.Join(*data, sysconf.PasswordFlag))
	t := cloudinit.Target{Root: "/", ConsoleHost: saved != nil && saved.Hostname != "", ConsolePass: passLocal == nil,
		SetPassword: func(pw string) error { return sys.SetPasswordCloud(pw, auth.New(st).Set) },
		SetHostname: func(h string) error { return exec.Command("hostname", h).Run() }}
	for _, d := range c.Apply(t) {
		fmt.Println(c.Source + " : " + d)
	}
	return nil
}
