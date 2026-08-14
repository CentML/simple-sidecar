# Build the sidecar-injector binary
FROM golang:1.25 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${BUILDPLATFORM} go build -a -o simple-sidecar ./cmd


# OSRB-approved base; CGO_ENABLED=0 binary needs no libc. Already runs as
# non-root (uid 1000), so the explicit USER is no longer required.
#
# curl dropped: no preStop hook or lifecycle block exists in this chart or in
# the platform config that consumes it.
FROM nvcr.io/nvidia/distroless/static:v4.0.0

WORKDIR /

# install binary
COPY --from=builder /workspace/simple-sidecar .

ENTRYPOINT ["/simple-sidecar"]
