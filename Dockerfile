# syntax=docker/dockerfile:1

# --- Build stage -------------------------------------------------------------
# The minor tag floats to the newest 1.27.x, so a rebuild always picks up
# stdlib security fixes. It can never float DOWN past the floor: go.mod's
# `toolchain` directive pins the minimum patch release and the go command
# fetches it if the base image is older. Raise that directive, not this tag,
# when govulncheck reports a stdlib finding — and keep this tag's minor version
# in step with the `go` directive, which json/v2 requires to be 1.27.0+.
FROM golang:1.27 AS builder

WORKDIR /src

# Cache module downloads independently from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown


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
