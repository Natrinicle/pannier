FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pannierd ./cmd/pannierd \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pannier-maint ./cmd/pannier-maint

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 --home /data --shell /usr/sbin/nologin pannier
COPY --from=build /out/pannierd /usr/local/bin/pannierd
COPY --from=build /out/pannier-maint /usr/local/bin/pannier-maint
USER 10001:10001
WORKDIR /data
EXPOSE 3477
ENTRYPOINT ["/usr/local/bin/pannierd"]
