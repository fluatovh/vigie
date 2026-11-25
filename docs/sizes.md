# Tailles mesurées (phase 1)

Mesure du 16/11/2026, build `dev`, Alpine 3.24.4, noyau `linux-virt` 6.18.63-r0. Mo = 10⁶ octets.

| Composant | Mo | Budget (Mo) | Note |
|---|---|---|---|
| Noyau `vmlinuz-virt` | 12,7 | 14 | tel quel, non élagable |
| initramfs (mkinitfs : base, scsi, ata, cdrom, squashfs, fat) | 5,8 | 11 | généré par nous, l'initramfs netboot Alpine ne voit pas un disque SATA |
| modloop élagué (26 modules) | 1,4 | 6 | contre 23 Mo pour le modloop Alpine complet |
| apks du world (alpine-base, dropbear, open-vm-tools…) | 10,7 | 14 | dont open-vm-tools + glib + deps : 3,4 |
| binaire `vigie` (statique, Go 1.27) | 6,7 → **14,8** (phase 2, govmomi) | 25 | 2,8 → 4,6 Mo une fois dans l'apk |
| apkovl (`/etc`) | 0,002 | 0,1 | |
| **Média de boot (partition FAT)** | **30,5** | | |
| VMDK streamOptimized | 31,0 | | déjà compressé, l'OVA n'ajoute rien |
| **OVA** | **31,0** → **32,8** (phase 2) | avertissement 60, plafond 100 | objectif du plan : environ 50 |

Décision open-vm-tools : **inclus** (obligatoire, demande utilisateur), coût 3,4 Mo.

Phase 2 (17/11/2026) : govmomi v0.56.2 + x/crypto (bcrypt) coûtent 8,1 Mo sur le binaire et 1,8 Mo
sur l'OVA. Le front (uPlot 45 Ko + HTML/CSS/JS) restera négligeable : l'OVA finira autour de 35 Mo.

Phase 7 (18/11/2026) : front embarqué (index.html, app.js, style.css, uPlot 1.6.32 : 98 Ko en tout)
et TLS : binaire 16,1 Mo, **OVA 33,3 Mo** (+0,3 Mo).

Phase N (18/11/2026) : golang.org/x/net v0.62.0 (ICMP) et iperf3 3.20 (194 Kio) : binaire 16,5 Mo,
**OVA 33,5 Mo** (+0,2 Mo).

Phase 8c (20/11/2026) : rapport client (paquet `internal/report`, CSS et JS du rapport, uPlot réutilisé
depuis le front) : binaire 16,6 → 17,1 Mo, **OVA 33,7 Mo** (+0,15 Mo). Une première version rendue
avec `html/template` coûtait +5,7 Mo de binaire (+1,4 Mo compressés, budget apks dépassé) : la
réflexion de text/template oblige l'éditeur de liens à garder toutes les méthodes exportées, govmomi
compris. Le rendu est donc écrit en Go avec un échappement explicite (`html.EscapeString`).

Phase T (20/11/2026) : TUI console (`internal/tui`, sans dépendance), mode local du disque de test,
test local : binaire 17,1 → 17,5 Mo, **OVA 33,9 Mo** (+0,2 Mo).

Phase D (23/11/2026) : DCUI et configuration système (+0,1 Mo de binaire) ; **nmap 7.99 et tcpdump
4.99.6** (demande utilisateur) : apks 13,6 → 18,2 Mo, **OVA 38,5 Mo** (+4,6 Mo). Budget apks : 20 Mo.

Langues (23/11/2026) : catalogue partagé (web/i18n.tsv, 842 × 6), moteur JS, polices de console
(Lat2-Terminus16, default8x16 : 10 Ko) : binaire 17,5 → 17,8 Mo, **OVA 38,6 Mo** (+0,1 Mo).

Phase P (24/11/2026) : qemu-guest-agent et pilotes virtio (+1,8 Mo), ethtool, smartmontools,
ipmitool (+3,8 Mo, demande utilisateur), simpledrm/drm pour la console UEFI (+0,8 Mo) :
**OVA 45,0 Mo**, ISO 50,3 Mo, qcow2 45,2 Mo. Budget apks : 24 Mo.

Phases 9, 10 et 11 (24/11/2026) : surveillance continue, esxcli et CDP/LLDP (cli/esx de govmomi déjà
présent), aucune nouvelle dépendance : binaire 17,9 → 18,0 Mo. **Taille finale : OVA 45,1 Mo, ISO
50,3 Mo, qcow2 45,3 Mo** (plafond 100 Mo ; budget apks 24 Mo, binaire 25 Mo).
