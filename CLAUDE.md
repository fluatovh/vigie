# vigie — appliance de diagnostic réseau et de performance datastore pour vSphere

Nom du projet : **Vigie** (anciennement « dsprobe », renommé le 18/11/2026 à la demande de l'utilisateur :
VM, OVA, binaire, module Go, paquet, chemins, jetons `vg_`, interface).

Plan complet : `docs/PLAN.md` (source de vérité, 11 phases). On avance **une phase à la fois** ;
la suivante ne commence qu'après validation de la précédente. Phase en cours : voir `docs/STATUS.md`.

## Contraintes non négociables (§0 du plan)

Une **seule VM**, Alpine Linux minimale, livrée en **OVA d'environ 50 Mo** (plafond dur : 100 Mo,
qui n'est pas un objectif), qui mesure les performances d'un datastore depuis l'hôte ESXi sur lequel
elle tourne. Elle se pilote entièrement par une **interface web auto-hébergée**.

- **Aucun SSH vers les ESXi.** Tout passe par l'API vSphere (vCenter de préférence ; un ESXi seul
  fonctionne, mais sans vMotion). Le SSH n'existe que *vers* l'appliance, pour l'admin.
- **Aucune dépendance réseau au runtime** : pas de CDN, pas de Google Fonts. Tout (JS, CSS, graphs)
  est embarqué dans le binaire. L'appliance doit marcher sur un réseau isolé.
- **Lecture seule côté infra**, sauf ces opérations explicites, toutes déclenchées par l'utilisateur
  dans l'UI : créer et supprimer *le* disque de test de l'appliance, vMotion de *l'appliance
  elle-même*, et en option un override DRS sur *l'appliance elle-même*.
- **Jamais d'écriture brute sur un périphérique bloc non vérifié** (§6.3 du plan). C'est la seule
  règle dont la violation détruit des données ; elle prime sur tout le reste.

## Règles de travail (§10 du plan)

- Versions figées partout (Alpine, Go, govmomi, uPlot) : `image/versions.mk`, `go.mod`, `web/`.
  Aucune dépendance non vendorée au runtime.
- Toute fonction qui écrit sur un périphérique bloc ou qui modifie vSphere passe par une fonction
  unique, testée et journalisée.
- Tout nouveau paquet apk ou toute nouvelle dépendance Go indique son coût en Mo dans le commit ;
  `image/size-budget.json` et `image/size-baseline.json` sont contrôlés au build (`make size-check`).
- Les tests vcsim tournent en CI ; les validations sur le lab sont listées en fin de phase sous
  forme de checklist pour l'humain.

## Où ça tourne

- **Dépôt git** : ce dossier, sur le Mac. Rien ne se compile ni ne s'exécute en local.
- **Build, tests, image, déploiement** : sur une machine de build distante (Linux + Docker) décrite
  dans `remote.env` (non versionné) ; `make sync` puis `make remote T=<cible>`. Détails de la
  machine et du lab : `CLAUDE.local.md` (non versionné).
- **CI GitHub** (`.github/workflows/`) : tests, images et boot QEMU à chaque push ; release signée
  (SHA256SUMS + Sigstore + attestation de provenance) à chaque tag `vX.Y.Z`.
- Les identifiants du lab vSphere vivent dans `lab.env` sur la machine de build, jamais dans le
  dépôt. Les artefacts (`dist/`) y restent.

## Décisions prises en cours de route (phase 1)

- **open-vm-tools est obligatoire** (demande utilisateur du 16/11/2026), plus optionnel.
- **Lancement des tests par l'API** (standard et avancé) avec un jeton d'API, sans passer par l'UI,
  pour permettre une QA automatisée (demande utilisateur du 16/11/2026). À prévoir dès la phase 6.
- **DHCP par défaut**, et l'IP obtenue s'affiche sur la console VMware (`/etc/issue`) et dans le motd.
- `vmw_pvscsi`, `ahci` et `ata_piix` sont **compilés en dur** dans le noyau Alpine `linux-virt`
  (voir `modules.builtin`), ils n'apparaissent donc pas dans le modloop. Disque système et disque de
  test sur **PVSCSI** ; le CD-ROM (ovf-env) reste sur SATA.

## Décisions (phases suivantes)

- **Phase 8b avant la phase 8** (18/11/2026) : le test avancé ne dépend pas du vMotion.
- **Phase N « Network »** ajoutée le 18/11/2026 (demande utilisateur, §11 de docs/PLAN.md) : onglet
  « Datastore performance » (ex-« Mesure ») + onglet « Network » (ping, surveillance smokeping/RRD,
  traceroute, ARP, iperf3 entre appliances). Réalisée juste après la 8b, avant la phase 8.
- **Console** : le mot de passe est toujours affiché, qu'il soit généré ou fourni par OVF.
- **Chaque résultat de bench présenté porte son profil d'I/O complet** (chat, docs, UI).
- **Renommage dsprobe → Vigie** (18/11/2026) : le projet couvre désormais réseau et stockage.
- **Phase T « TUI console »** demandée le 20/11/2026 (§12 de docs/PLAN.md), réalisée après la 8c :
  tty1 lance `vigie tui` (clavier seul, 80 × 25, police VGA CP437) ; tty2 garde un login root.
- **Mode local du disque de test** (phase T) : sans vCenter, l'utilisateur ajoute le disque dans
  vSphere et l'appliance l'adopte (apparu après la demande, vierge, nom retapé) ; seule écriture :
  WriteSignature. Le disque n'est jamais retiré ni effacé par l'appliance.
- **Phase D « DCUI »** (23/11/2026, §13 de docs/PLAN.md) : accueil façon DCUI sur tty1, root sans mot
  de passe en console (shell, tty2), SSH avec mot de passe ; réseau et mot de passe modifiables
  depuis la console (persistés dans /data, priment sur l'OVF) ; `/api/system/*` réservé au socket
  console. nmap et tcpdump inclus dans l'image.
- **Langues** (23/11/2026) : anglais par défaut, français, espagnol, italien, polonais, allemand, pour
  la console et l'interface web. Catalogue unique `web/i18n.tsv` (texte source français) : tout texte
  affiché nouveau doit y être ajouté dans les 6 langues (les tests le vérifient pour la console).
- **Formats** (phase P, 24/11/2026) : OVA, ISO live (BIOS/UEFI, `vigie.live`, aucune persistance même
  avec un disque) et qcow2 (Proxmox/OpenStack, métadonnées cloud) — `make release`, `docs/formats.md`.
  Outils inclus : nmap, tcpdump, ethtool, smartmontools, ipmitool, iperf3.
- **Phase 10 sans module switch** (24/11/2026) : le module Arista eAPI est abandonné (demande
  utilisateur). La phase 10 se limite à esxcli via l'API (compteurs détaillés des vmnic) et à CDP/LLDP
  par `QueryNetworkHint`, tous deux lus depuis l'ESXi.
- **Phase 11 réduite** (24/11/2026, demande utilisateur) : pas de désactivation de SSH depuis l'UI, pas
  de rotation du certificat (il est déjà généré au premier démarrage, propre à chaque appliance), pas
  de page « rôle vSphere minimal ». On garde le README de déploiement, les notes de version et la
  taille finale.
