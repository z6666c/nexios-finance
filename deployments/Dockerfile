FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /orchestrator ./cmd/orchestrator

FROM alpine:3.19
COPY --from=build /orchestrator /orchestrator
EXPOSE 8080
ENTRYPOINT ["/orchestrator"]
