# vigie — Makefile. Les cibles de build s'exécutent sur une machine Linux avec Docker (la CI GitHub
# ou une machine de build distante : `make sync` puis `make remote T=<cible>`, voir remote.env).
include image/versions.mk
-include version.env

VERSION           ?= dev
COMMIT            ?= none
SOURCE_DATE_EPOCH ?= $(shell date +%s)
export SOURCE_DATE_EPOCH VERSION COMMIT
BUILD_DATE := $(shell date -u -d @$(SOURCE_DATE_EPOCH) +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)

DIST   := dist
CACHE  := $(HOME)/.cache/vigie
UID    := $(shell id -u)
GID    := $(shell id -g)
GO_SRC := $(shell find cmd internal web -type f 2>/dev/null) go.mod

LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

# Conteneur Go : build statique, caches persistants dans $(CACHE)/go, fichiers créés avec l'uid courant.
DOCKER_GO := docker run --rm -u $(UID):$(GID) -v $(CURDIR):/src -w /src \
  -v $(CACHE)/go:/cache -e HOME=/cache -e GOCACHE=/cache/build -e GOMODCACHE=/cache/mod \
  -e CGO_ENABLED=0 -e GOFLAGS=-trimpath $(GO_IMAGE)

# Conteneur d'assemblage de l'image (Alpine + abuild + syslinux + mtools + e2fsprogs + qemu-img).
BUILDER     := vigie-builder:$(ALPINE_VERSION)
DOCKER_IMG  := docker run --rm -u $(UID):$(GID) -v $(CURDIR):/src -w /src \
  -v $(CACHE)/alpine:/cache -e HOME=/tmp -e SOURCE_DATE_EPOCH -e BUILD_DATE=$(BUILD_DATE) -e VERSION -e COMMIT -e VIGIE_APK_KEY \
  -e ALPINE_VERSION=$(ALPINE_VERSION) -e ALPINE_BRANCH=$(ALPINE_BRANCH) -e ALPINE_MIRROR=$(ALPINE_MIRROR) \
  -e KERNEL_FLAVOR=$(KERNEL_FLAVOR) -e LINUX_PKG_VERSION=$(LINUX_PKG_VERSION) $(BUILDER)

.PHONY: all build test vet builder apk image ova iso qcow2 release size-check fmt-check ci-qemu ci-ui checksums deploy undeploy clean sync remote version

all: ova

version:
	@echo "$(VERSION) ($(COMMIT), $(BUILD_DATE))"

$(DIST) $(CACHE)/go $(CACHE)/alpine:
	mkdir -p $@

build: $(DIST)/vigie
$(DIST)/vigie: $(GO_SRC) $(wildcard version.env) | $(DIST) $(CACHE)/go
	$(DOCKER_GO) go build -ldflags "$(LDFLAGS)" -o $@ ./cmd/vigie
	@ls -l $@

test: | $(CACHE)/go
	$(DOCKER_GO) go test ./...

vet: | $(CACHE)/go
	$(DOCKER_GO) go vet ./...

builder:
	docker build -q -f image/Dockerfile.build --build-arg ALPINE_VERSION=$(ALPINE_VERSION) -t $(BUILDER) image/

apk: build builder | $(CACHE)/alpine
	$(DOCKER_IMG) image/build-apk.sh

# L'assemblage de l'image installe le paquet noyau dans le conteneur : il tourne en root (dans le conteneur
# seulement, sans --privileged) et rend dist/ à l'utilisateur courant à la fin.
DOCKER_IMG_ROOT := $(subst -u $(UID):$(GID),-e OWNER=$(UID):$(GID),$(DOCKER_IMG))
image: apk
	$(DOCKER_IMG_ROOT) image/build-image.sh

ova: image
	$(DOCKER_IMG) image/make-ova.sh
	image/size-check.sh

iso: image ## ISO live hybride BIOS/UEFI (rien n'est conservé)
	$(DOCKER_IMG) image/make-iso.sh

qcow2: image ## disque pour Proxmox / OpenStack / KVM
	$(DOCKER_IMG) image/make-qcow2.sh

release: image ## les trois formats : OVA, ISO, qcow2
	$(DOCKER_IMG) image/make-ova.sh
	$(DOCKER_IMG) image/make-iso.sh
	$(DOCKER_IMG) image/make-qcow2.sh
	image/size-check.sh

size-check:
	image/size-check.sh

fmt-check: | $(CACHE)/go ## code Go formaté (gofmt)
	$(DOCKER_GO) sh -c 'f=$$(gofmt -l cmd internal web); [ -z "$$f" ] || { echo "non formaté : $$f"; exit 1; }'

ci-qemu: ## boot des formats sous QEMU (ISO BIOS/UEFI, qcow2 NoCloud/config-drive) ; QEMU_ACCEL=kvm si dispo
	image/qemu-formats.sh

ci-ui: ## interface dans Chromium headless contre le qcow2 démarré sous QEMU
	image/ci-ui.sh

checksums: ## dist/SHA256SUMS des trois formats de la version courante
	cd $(DIST) && sha256sum vigie-$(VERSION).ova vigie-$(VERSION).iso vigie-$(VERSION).qcow2 > SHA256SUMS && cat SHA256SUMS

deploy: ## déploie dist/vigie-$(VERSION).ova sur le lab (lab.env + govc)
	image/deploy.sh

undeploy:
	image/deploy.sh --destroy

clean:
	rm -rf $(DIST)

# --- cibles locales : machine de build distante, décrite dans remote.env (non versionné) ---
-include remote.env
REMOTE ?= $(VIGIE_REMOTE)
PORT   ?= $(or $(VIGIE_PORT),22)
T      ?= ova
sync:
	scripts/sync.sh
remote: sync
	ssh -p $(PORT) -o BatchMode=yes $(REMOTE) 'cd vigie && make $(T)'

qemu-test: ## boot local de dist/disk.raw sous QEMU (DHCP), puis --static via ARGS
	image/qemu-test.sh $(ARGS)
