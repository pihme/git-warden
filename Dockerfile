# syntax=docker/dockerfile:1
#
# Git Warden in a container: push-guard and backup-guard, plus the programs they
# call, provisioned here at pinned versions. The guards themselves still never
# download anything at runtime; the image is the host.
#
#   docker build -t git-warden --build-arg VERSION=$(git describe --tags --always) .
#
# How to run either guard: README.md, section "Docker".
#
# Pinned here:
#   base images     by digest, written out in each FROM so Dependabot can
#                   keep them current (keep the two debian lines equal)
#   git             Debian trixie package (2.47), checked to be 2.42 or newer
#   gitleaks        8.30.1, the same release CI uses, tarball SHA-256 below
#   git-everref     v1.0.0 via scripts/install-everref.sh (SHA-256 pinned there)

# --- build: both binaries, static ------------------------------------------
FROM golang:1.27-trixie@sha256:3b77fc618ec235a1ab412de7737f120dd507c57e8d87de4cbb7994fb94275ed5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN for bin in push-guard backup-guard; do \
      CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/$bin ./cmd/$bin || exit 1; \
    done

# --- tools: gitleaks and git-everref, checksum-verified --------------------
FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a AS tools
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
ARG GITLEAKS_VERSION=8.30.1
ARG GITLEAKS_SHA256_amd64=551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb
ARG GITLEAKS_SHA256_arm64=e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080
COPY scripts/install-everref.sh /tmp/install-everref.sh
RUN set -eu; \
    arch=$(dpkg --print-architecture); \
    case "$arch" in \
      amd64) gl=x64; sum=$GITLEAKS_SHA256_amd64 ;; \
      arm64) gl=arm64; sum=$GITLEAKS_SHA256_arm64 ;; \
      *) echo "unsupported architecture $arch" >&2; exit 1 ;; \
    esac; \
    tarball="gitleaks_${GITLEAKS_VERSION}_linux_${gl}.tar.gz"; \
    cd /tmp; \
    curl -fsSL --retry 3 -o "$tarball" "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/$tarball"; \
    echo "$sum  $tarball" | sha256sum -c -; \
    tar -xzf "$tarball" gitleaks; \
    test "$(./gitleaks version)" = "$GITLEAKS_VERSION"; \
    install -D -m 0755 gitleaks /out/gitleaks; \
    sh /tmp/install-everref.sh /out

# --- runtime ----------------------------------------------------------------
FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a
ARG DEBIAN_FRONTEND=noninteractive
# git (and git http-backend), ssh for SSH remotes, CA certificates for HTTPS.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git openssh-client \
 && rm -rf /var/lib/apt/lists/* \
 && git version | awk '{ split($3, v, "."); if (v[1] < 2 || (v[1] == 2 && v[2] < 42)) { print "git " $3 " is older than 2.42"; exit 1 } }'
COPY --from=tools /out/gitleaks /out/git-everref /usr/local/bin/
COPY --from=build /out/push-guard /out/backup-guard /usr/local/bin/
# Unprivileged guard user. Configuration is mounted read-only at /etc/warden
# (Push Guard) or /etc/warden-backup (Backup Guard); state goes to a volume at
# /var/lib/warden or /var/lib/warden-backup (state_dir in defaults.yaml).
RUN useradd --uid 10001 --user-group --no-log-init --create-home --home-dir /home/warden --shell /usr/sbin/nologin warden \
 && install -d -o warden -g warden -m 0750 /var/lib/warden /var/lib/warden-backup
USER warden
WORKDIR /home/warden
EXPOSE 8418
# The default command is the Push Guard over HTTP; run the Backup Guard (or any
# other subcommand) by passing it as the command, e.g.
#   docker run … git-warden backup-guard run --config /etc/warden-backup
CMD ["push-guard", "serve", "--config", "/etc/warden", "--listen", "0.0.0.0:8418"]
