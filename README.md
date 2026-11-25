# Vigie

Diagnostic appliance for vSphere: **datastore performance** measured from the ESXi host it runs on,
and **network diagnostics** (ping, smokeping-style monitoring, traceroute, ARP, iperf3). A single
small Alpine Linux VM (≈ 45 MB), driven by an embedded web interface, by its API, or from the VM
console with the keyboard.

- No SSH to the ESXi hosts: everything goes through the vSphere API (vCenter preferred, a standalone
  ESXi works too).
- No network dependency: interface, charts and fonts are built into the binary; works on an isolated
  network.
- Read-only on the infrastructure, except for explicit operations (see "What the appliance changes").
- Interface and console in English (default), French, Spanish, Italian, Polish and German.

## Formats

| File | Size | For |
|---|---|---|
| `vigie-<version>.ova` | ≈ 45 MB | vSphere (vCenter or ESXi) |
| `vigie-<version>.qcow2` | ≈ 45 MB | Proxmox, OpenStack, KVM |
| `vigie-<version>.iso` | ≈ 50 MB | Live ISO, BIOS/UEFI, nothing is kept |

Proxmox, OpenStack and the live ISO are covered in [docs/formats.md](docs/formats.md) (in French).
The rest of this document covers deployment on vSphere.

## Verifying a release

Images are downloaded from the [GitHub releases](https://github.com/fluatovh/vigie/releases), along
with `SHA256SUMS` and its signature `SHA256SUMS.sigstore.json`. Before deploying:

```sh
# 1. integrity: the downloaded files match the published checksums
sha256sum -c SHA256SUMS --ignore-missing          # macOS: shasum -a 256 -c SHA256SUMS --ignore-missing

# 2. authenticity: SHA256SUMS was signed by this repository's release workflow, for this tag
cosign verify-blob --bundle SHA256SUMS.sigstore.json \
  --certificate-identity https://github.com/fluatovh/vigie/.github/workflows/release.yml@refs/tags/vX.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS

# 3. provenance (optional): the image was built by GitHub Actions from this repository
gh attestation verify vigie-vX.Y.Z.ova --repo fluatovh/vigie
```

[cosign](https://docs.sigstore.dev/cosign/system_config/installation/) and the
[GitHub CLI](https://cli.github.com/) are single binaries. The checksum alone (step 1) detects a
corrupted download; the signature (step 2) also proves the checksums come from this repository. No
MD5 is provided: it no longer protects against deliberate tampering.

## Deploying on vSphere

### Importing the OVA

vSphere Client: *Deploy OVF Template* → `vigie-<version>.ova` → choose the host and the network.
**Place the appliance on the host you want to measure**: it tests the path of the host it runs on
(it can then move itself with vMotion, see below).

VM hardware: 2 vCPU, 1 GB RAM, PVSCSI system disk, vmxnet3 NIC, SATA CD-ROM (carries the OVF
configuration), virtual hardware `vmx-14` (ESXi 6.7 and later).

### OVF properties

All optional. Without an IP address, the appliance uses DHCP.

| Property | Purpose |
|---|---|
| `hostname` | Appliance host name |
| `ip`, `netmask`, `gateway` | Static IPv4 address; empty: DHCP |
| `dns` | DNS servers, comma-separated (static IP) |
| `ntp` | NTP servers, comma-separated; empty: pool.ntp.org |
| `ui_password` | Root (SSH) and web interface password; empty: generated at first boot |

From the command line with govc:

```sh
govc import.spec vigie-<version>.ova > vigie.json      # then fill in PropertyMapping and NetworkMapping
govc import.ova -name vigie -options vigie.json vigie-<version>.ova
govc vm.power -on vigie
```

Network settings and the password chosen later in the appliance console take precedence over the OVF
properties.

## First access

Open the VM console. The home screen (DCUI-style) always shows:

- the IP address and the web interface URL;
- the **password** (from OVF or generated at first boot);
- the SHA-256 fingerprint of the **HTTPS certificate**. This self-signed certificate is generated at
  first boot and unique to each appliance: compare it with the fingerprint your browser shows.

Then:

- **Web interface**: `https://<ip>/` (port 80 redirects to HTTPS), with the password shown.
- **Console**: Enter opens the keyboard text interface (network tests, storage tests without
  vCenter, network settings, password, language). F12: shutdown or restart. Alt+F2: root shell
  without password (access to the VM console is equivalent to physical access).
- **SSH**: `ssh root@<ip>`, with the password.

## Measuring a datastore

**Datastore performance** tab of the web interface, step by step:

1. **vSphere connection**: URL of the vCenter (or ESXi), account, password. The server certificate
   fingerprint is shown and must be confirmed. Tick "Remember the password" for automatic
   reconnection after a restart (encrypted on the appliance disk).
2. **Identification**: the appliance finds itself in the inventory (VM, host, cluster). The page
   lists the vSphere privileges the account is missing.
3. **Datastore**: datastore selection and NFS path (vmk, vSwitch, uplinks, CDP/LLDP neighbour when
   the switch announces itself).
4. **Test disk**: the appliance creates its own VMDK (1 to 64 GB) on the datastore, verifies it
   before any write (it never writes to an unverified disk), then prefills it with incompressible
   data on the first test.
5. **Hosts**: move the appliance with vMotion, or run a comparison tour across hosts.
6. **Test type**:
   - *standard*: baseline, sequential write then read (1 MiB, QD 4), TX/written ratio, vmnic errors
     (including CRC, frame and length via esxcli), latency seen by the host, verdict;
   - *advanced*: the standard test, then IOPS and latencies in 4 KiB random I/O at QD 1, 4, 16
     and 32;
   - *continuous monitoring*: a short test every N minutes.
7. **Execution**, live; 8. **Results** with verdict and full I/O profile; 9. **Cleanup** (test
   disk removal).

Each standard, advanced or tour test produces a self-contained HTML **customer report**, printable
on A4, with before/after comparison of two tests (History tab). Verdict thresholds are set per preset
(all-flash, hybrid, disks) in Settings.

### Without vCenter (local mode)

From the appliance console, Storage tab: the appliance explains how to add a disk to its VM in
vSphere (size, controller), then adopts that disk after strict checks (appeared after the request,
blank, name typed back). Local tests measure throughput and latency without the host counters. The
appliance never removes or wipes this disk.

## Network diagnostics

**Network** tab: appliance interfaces, live ping and traceroute, continuous target monitoring
(20 pings per minute, one year of history), ARP table, iperf3 TCP or UDP throughput to another Vigie
appliance. Tools available over SSH: nmap, tcpdump, ethtool, smartctl, ipmitool, iperf3.

## Continuous monitoring and trends

Step 6, "Continuous monitoring": a standard (or local) test every 15 minutes to 24 hours on the
current host. A test is skipped, with the reason shown, if another job is running or vCenter is
unreachable. Measurements feed the **Trends** card of the History tab (throughput, p99 latency,
TX/written ratio per host and datastore), exportable as CSV or JSON. Adjustable retention (30 days
by default).

## API

The whole interface runs on a JSON API. Create a token in **Settings → API tokens**, then:

```sh
V=https://<ip>/api; T="Authorization: Bearer vg_..."
curl -k -H "$T" $V/vsphere/status
curl -k -H "$T" -H 'Content-Type: application/json' -X POST $V/jobs -d '{"profile":"standard"}'
curl -k -H "$T" $V/jobs/<id>                  # state and result
curl -k -H "$T" -N $V/jobs/<id>/events        # live progress (Server-Sent Events)
curl -k -H "$T" $V/reports/<id> > report.html
curl -k -H "$T" "$V/results?period=7d&format=csv" > results.csv
```

Job profiles: `standard`, `advanced`, `local`, `tour`, `move`, `net.iperf`, `testdisk.create`,
`testdisk.delete`. One job at a time (HTTP 409 otherwise).

## What the appliance changes

Everything else is read-only. These operations are always triggered by the user:

- creating and deleting **its own** test disk (on its own VM);
- vMotion of **the appliance itself**;
- optionally, a DRS override on **the appliance itself** during a tour.

esxcli commands go through the vSphere API and are limited to a read-only allowlist
(`network nic stats get`, `network nic list`, `network nic get`).

## Limitations

- No vmkernel TCP retransmission counter: the TX/written ratio is an indirect indicator, sensitive
  to traffic from other VMs on the same vmnic.
- 20 s granularity of vSphere counters: shorter micro-outages are smoothed out; a test that is too
  short makes the TX/written ratio inconclusive.
- Advanced test IOPS include the effect of the NFS server cache: this is what a VM sees, not a
  measurement of the server's disks.
- Host transmit errors are only visible on the switch side, which the appliance does not query.
- The appliance measures the host it runs on: move it (or deploy another one) to test another host.

## Building and testing

On a Linux machine with Docker: `make release` builds the OVA, ISO and qcow2 in `dist/`.

| Target | Checks |
|---|---|
| `make fmt-check vet test` | formatting, static analysis, Go tests (including the vcsim simulator and the language catalog) |
| `make release` | builds the three formats and checks the size budgets |
| `make ci-qemu` | boots the ISO (BIOS, UEFI, with a disk present) and the qcow2 (NoCloud, config-drive) under QEMU |
| `make ci-ui` | interface in headless Chromium against the booted qcow2: login, tabs, ping, API token, thresholds, languages, CSP, no external request |

GitHub CI runs them on every push and pull request; a `vX.Y.Z` tag also triggers the signed release.
Tests against a real vSphere (`image/lab-qa-*.sh`) are run by hand with a local `lab.env`. Pinned
versions: `image/versions.mk`, `go.mod`, `web/vendor/`. Release notes:
[docs/RELEASE-NOTES.md](docs/RELEASE-NOTES.md).

## License

[Apache 2.0](LICENSE).
