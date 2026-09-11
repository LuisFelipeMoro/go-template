# syntax=docker/dockerfile:1

# --- Build stage -------------------------------------------------------------
# The minor tag floats to the newest 1.26.x, so a rebuild always picks up
# stdlib security fixes. It can never float DOWN past the floor: go.mod's
# `toolchain` directive pins the minimum patch release and the go command
# fetches it if the base image is older. Raise that directive, not this tag,
# when govulncheck reports a stdlib finding.
FROM golang:1.26 AS builder

WORKDIR /src

# Cache module downloads independently from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown

# json/v2 (encoding/json/v2 + jsontext) is behind GOEXPERIMENT in Go 1.26.
ENV GOEXPERIMENT=jsonv2

# Static binary: distroless/static has no libc.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w \
    -X github.com/luisfelipecoelho/go-template/internal/cli.version=${VERSION} \
    -X github.com/luisfelipecoelho/go-template/internal/cli.commit=${COMMIT} \
    -X github.com/luisfelipecoelho/go-template/internal/cli.buildTime=${BUILD_TIME}" \
    -o /app ./cmd

# --- Runtime stage -----------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /app /app

USER nonroot:nonroot

EXPOSE 8080

# Distroless has no shell/curl: the binary probes itself.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app", "healthcheck"]

ENTRYPOINT ["/app"]
CMD ["server"]
