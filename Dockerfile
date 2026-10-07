# The control plane image (spec §12.2): ghrm with the web UI embedded, plus ghrm-agent,
# which template builds install into job environments.
FROM node:22-bookworm-slim AS web
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@9 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-bookworm AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/cocardoso/gh-runners-manager/internal/version.Version=${VERSION} -X github.com/cocardoso/gh-runners-manager/internal/version.Commit=${COMMIT}" -o /out/ghrm ./cmd/ghrm \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/cocardoso/gh-runners-manager/internal/version.Version=${VERSION} -X github.com/cocardoso/gh-runners-manager/internal/version.Commit=${COMMIT}" -o /out/ghrm-agent ./cmd/ghrm-agent \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/ghrm /out/ghrm-agent /usr/local/bin/
# A named volume mounted here inherits the owner, so the non-root user can write it.
COPY --from=go --chown=65532:65532 /out/data /var/lib/ghrm
VOLUME /var/lib/ghrm
EXPOSE 8080 8443
ENTRYPOINT ["/usr/local/bin/ghrm"]
CMD ["serve", "--config", "/etc/ghrm/ghrm.yaml"]
