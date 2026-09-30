# kongctl as an Argo CD Config Management Plugin sidecar image.
#
# The final image is scratch plus one static binary, plugin.yaml in the place
# the repo-server expects it, and a passwd entry for uid 999 (the Argo CD
# convention; the sidecar runs runAsNonRoot). No shell, no package manager.
# Organisation CA bundles can be added at build time with --build-arg
# EXTRA_CA_CERTS=path/to/bundle.crt (PEM, concatenated).
FROM golang:1.26 AS build
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/m0un10/kongctl/internal/cli.Version=${VERSION} -X github.com/m0un10/kongctl/internal/cli.Commit=${COMMIT}" \
      -o /out/kongctl ./cmd/kongctl \
 && printf 'argocd:x:999:999:argocd:/home/argocd:/sbin/nologin\n' > /out/passwd \
 && printf 'argocd:x:999:\n' > /out/group \
 && mkdir -p /out/home/argocd/cmp-server/config /out/tmp \
 && chown -R 999:999 /out/home /out/tmp

ARG EXTRA_CA_CERTS=""
RUN if [ -n "$EXTRA_CA_CERTS" ]; then cat "$EXTRA_CA_CERTS" >> /etc/ssl/certs/ca-certificates.crt; fi

FROM scratch
COPY --from=build /out/passwd /out/group /etc/
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=999:999 /out/home /home
COPY --from=build --chown=999:999 /out/tmp /tmp
COPY --from=build /out/kongctl /kongctl
COPY --chown=999:999 plugin.yaml /home/argocd/cmp-server/config/plugin.yaml
ENV HOME=/home/argocd \
    TMPDIR=/tmp \
    PATH=/
WORKDIR /home/argocd
USER 999
ENTRYPOINT ["/kongctl"]
