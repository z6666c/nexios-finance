# This project ships with zero third-party Go dependencies on purpose (see
# internal/uuid and internal/pgwire's package docs): a fresh checkout must
# build with no access to proxy.golang.org, which a local/on-prem server's
# network commonly can't reach. That also means this build stage never
# touches the network - `go mod download` here is a genuine no-op - so the
# image builds even fully offline once the base images are cached locally.
FROM golang:1.22-alpine AS build
WORKDIR /app
ENV CGO_ENABLED=0
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /orchestrator ./cmd/orchestrator

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /orchestrator /orchestrator
COPY config/config.yaml /config/config.yaml
WORKDIR /
EXPOSE 8080
ENTRYPOINT ["/orchestrator"]
