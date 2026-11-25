# Versions figées (§10 du plan). Toute mise à jour est un commit dédié avec le coût en Mo.
ALPINE_VERSION    := 3.24.4
ALPINE_BRANCH     := v3.24
ALPINE_MIRROR     := https://dl-cdn.alpinelinux.org/alpine
KERNEL_FLAVOR     := virt
# Paquet noyau Alpine (fournit vmlinuz, modules du modloop et base de l'initramfs mkinitfs)
LINUX_PKG_VERSION := 6.18.63-r0
GO_IMAGE          := golang:1.27.4-alpine3.24
GOVC_VERSION      := 0.56.2
