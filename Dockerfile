# Dockerfile — FEAT-2326609775 (Azure cloud engine, inc1) deliverable 2.
#
# Builds cmd/metroserve as a scratch/distroless-runtime Linux container for
# Azure Container Apps (docs/planning/azure-cloud-engine-design.md §3.2,
# §7 inc1 item 1). Go 1.25 multi-stage: a full golang:1.25 builder stage,
# a minimal distroless runtime stage carrying nothing but the static
# binary and CA certs.
#
# # Version stamping (GR#2: app version = git describe via ldflags, NEVER
# a hand-maintained file)
#
# The four VERSION/COMMIT/BRANCH/BUILD_TIME build-args mirror build.ps1's
# own ldflags incantation exactly (internal/foundation/buildinfo's doc
# comment names the five -X flags; HOST is set to "azure-container-apps"
# here rather than a build-args value, since "the machine that built this"
# is meaningless once the image is pushed to a registry and later run on
# a DIFFERENT Azure node than the one that built it -- see the ARG below).
# The CI workflow (.github/workflows/azure-deploy.yml) computes VERSION/
# COMMIT/BRANCH/BUILD_TIME on the RUNNER via `git describe`/`git rev-parse`
# BEFORE invoking `docker build`, exactly as build.ps1 does for the local
# TUI build -- this Dockerfile never runs `git` itself, so it never
# depends on the build context actually containing a `.git` directory
# (a shallow/tarball checkout, or a .dockerignore excluding .git, would
# otherwise silently degrade every image to buildinfo's "dev" defaults —
# GR#1: no confident-wrong version string).
#
# Defaults below ("dev"/"unknown") match buildinfo.go's own package-level
# defaults exactly, so a bare `docker build .` with no --build-arg (a
# local smoke build) still produces a working, honestly-labelled image
# rather than a required-arg failure.
#
# # Why distroless, not alpine (design doc §7 item 1: "distroless or alpine")
#
# gcr.io/distroless/static is chosen over alpine: cmd/metroserve is pure
# Go with CGO disabled below (no libc dependency at all), so the "static"
# distroless variant needs nothing alpine's musl libc would otherwise
# provide, and it ships no shell, no package manager, and a dramatically
# smaller CVE surface than even alpine's -- appropriate for a network-
# facing service on a public FQDN (design doc §5.5's "private, not yet
# authenticated" posture is a reason to shrink attack surface everywhere
# else that costs nothing to shrink).

# ---- Builder ----
FROM golang:1.25 AS builder

WORKDIR /src

# Go module cache layer: copy go.mod/go.sum first so `go mod download`
# is cached across builds that only change source, not dependencies.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=dev
ARG BRANCH=dev
ARG BUILD_TIME=dev

# CGO_DISABLED + GOOS=linux: a fully static Linux binary, buildable on any
# host CI runner (incl. windows-latest, where this project's own CI
# already runs `go build`/`go test` — see .github/workflows/ci.yml) and
# runnable unmodified on distroless/static's scratch-like base below.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.Version=${VERSION} \
              -X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.Commit=${COMMIT} \
              -X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.Branch=${BRANCH} \
              -X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.BuildTime=${BUILD_TIME} \
              -X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.Host=azure-container-apps" \
    -o /out/metroserve \
    ./cmd/metroserve

# Empty staging dir for the /data anonymous-volume ownership fix below
# (BUG-693, REGRESSION CLASS: nonroot base image vs Docker's anonymous-
# volume default ownership) — created here in the builder stage, which
# still has a shell, so the runtime COPY --chown below has a real,
# already-existing source directory to copy (COPY --chown works without
# a shell, but mkdir does not exist in the shell-less runtime image).
RUN mkdir -p /data-empty

# ---- Runtime ----
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# REGRESSION CLASS (round r1 REJECT, 2026-09-04 — recorded here because this
# is the FIRST Dockerfile this repo has ever had, so this integration was
# NEVER exercised before a real destructive round actually ran the binary
# the way the container would): every prior gate on this increment --
# `go build`, `go vet`, `go test ./...`, golangci-lint, the smoke test
# against a LOCAL `go build` binary -- passed while carrying a fatal defect,
# because none of them run the process from an empty directory the way a
# container does. cmd/metroserve's composition root resolves data/errors.json
# and the whole data/ catalogue via FILESYSTEM SEARCH at process start
# (internal/foundation/errs/registry.go's loadRegistry +
# internal/foundation/data/paths.go's ResolveDataDir): walk upward from the
# executable's directory, then from cwd, looking for a "data/" directory
# containing the expected marker files. A distroless image with ONLY the
# binary copied in has no such directory anywhere on its filesystem, and the
# root ("/") has no parent to keep walking to. Reproduced two ways:
#   - no data/ tree at all  -> compose.Wire fails outright at boot
#     (MET-G801: composition root registry failure) before the HTTP server
#     ever binds, so /health never serves a single request;
#   - errors.json copied in isolation without wiring the override env vars
#     below -> the process DOES boot, but ResolveDataDir (paths.go) still
#     fails for the REST of the data/ catalogue (MET-F600), and separately
#     every error this binary raises -- including this increment's OWN
#     MET-P040/MET-P041/MET-P042 -- silently degrades to errs' generic
#     unregistered-code fallback instead of its real registered message: a
#     live GR#1 (aggressive error trapping) + GR#7 (registry-sourced errors)
#     hole that would have shipped invisibly, since a degraded-but-non-nil
#     error still LOOKS like an error in every log line.
# FIX: copy the whole data/ tree into the image at a path OTHER than /data
# (that mount point is reserved for the Azure Files persistence volume,
# declared below -- colliding the two would let an operator's volume mount
# silently shadow the read-only error registry) and point BOTH resolvers at
# it explicitly via their documented override env vars, so neither one ever
# performs the upward filesystem walk inside the container at all.
COPY --from=builder /src/data /data-src
ENV METROPOLIS_DATA_DIR=/data-src
ENV METROPOLIS_ERRORS_PATH=/data-src/errors.json

# /data is the Azure Files mount point the design doc's inc1 run line uses
# (§7 item 4: `metroserve -addr 0.0.0.0:8080 -persist-dir /data ...`).
# Declaring it here documents the contract; Container Apps' volume mount
# config (not this Dockerfile) is what actually attaches the share. Kept
# entirely separate from /data-src above -- persistence and the read-only
# data catalogue must never share a mount point.
#
# REGRESSION CLASS (BUG-693, Azure Deploy run 33924081197 boot-proof
# failure — nonroot base image vs anonymous-volume default ownership):
# when nothing external is bind/volume-mounted at /data (exactly what the
# workflow's boot-proof step does), Docker auto-creates an ANONYMOUS
# volume, populated from whatever already exists in the image at that
# path -- both its CONTENTS and its OWNERSHIP. With no COPY into /data,
# that path did not exist in the image at all, so Docker's auto-create
# defaulted the new volume to root:root 0755. This runtime stage's base
# is gcr.io/distroless/static-debian12:nonroot (uid 65532, no shell, so
# no `chown`/`mkdir` available here to fix it after the fact). metroserve
# pre-creates the default city BEFORE binding HTTP (cmd/metroserve/
# main.go's runHosted) and its persist write path hits os.MkdirAll under
# /data (internal/persist/diskstore.go) -- which EACCES'd as non-root
# against a root-owned directory, so main exited 1 and /health never
# came up. In production this is masked entirely because Azure Files
# always supplies an external mount that shadows /data, which is exactly
# why the gate (nothing mounted) caught what production traffic never
# would. FIX: seed /data from an empty directory already owned by
# nonroot at COPY time -- COPY --chown works with no shell, unlike a
# RUN chown/mkdir in this stage -- so Docker's anonymous-volume
# auto-create inherits nonroot:nonroot ownership from the image path
# instead of defaulting to root:root.
COPY --from=builder --chown=nonroot:nonroot /data-empty /data
VOLUME ["/data"]

COPY --from=builder /out/metroserve /metroserve

EXPOSE 8080

# nonroot base image already runs as the "nonroot" user (uid 65532) —
# no USER directive needed, unlike a from-scratch or debian-slim base.
ENTRYPOINT ["/metroserve"]
# Matches the design doc's §7 item 4 run line exactly (persist-dir,
# tick-interval, snapshot-every, addr); -city is deliberately NOT
# defaulted here so the Container App's own command/args override picks
# the real city id per the deploy config, not a value buried in the image.
CMD ["-addr", "0.0.0.0:8080", "-persist-dir", "/data", "-city", "dogfood", "-tick-interval", "1s", "-snapshot-every", "360"]
