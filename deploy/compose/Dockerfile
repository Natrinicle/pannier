FROM kopia/kopia:0.23.1 AS kopia
FROM docker:27.5.1-cli AS dockercli

FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pannierd ./cmd/pannierd \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pannier-maint ./cmd/pannier-maint

FROM debian:bookworm-slim
# kopia 0.23.1 matches the Debian apt package used for host-run cells.
# postgresql-client is for dump-first SQL when pannierd cannot docker exec.
# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates postgresql-client \
    && rm -rf /var/lib/apt/lists/*
RUN useradd --system --uid 10001 --home /data --shell /usr/sbin/nologin pannier
COPY --from=kopia /bin/kopia /usr/local/bin/kopia
COPY --from=dockercli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=build /out/pannierd /usr/local/bin/pannierd
COPY --from=build /out/pannier-maint /usr/local/bin/pannier-maint
USER 10001:10001
WORKDIR /data
EXPOSE 3477
ENTRYPOINT ["/usr/local/bin/pannierd"]
