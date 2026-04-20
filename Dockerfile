FROM golang:1.24 AS builder

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/infohub ./cmd/infohub

FROM debian:bookworm-slim

WORKDIR /app

COPY --from=builder /out/infohub /app/infohub
COPY config.yaml /app/config.yaml

EXPOSE 8080

ENTRYPOINT ["/app/infohub"]
CMD ["-config", "/app/config.yaml"]
