# Build the sidecar-injector binary
FROM golang:1.23 as builder

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


FROM alpine:latest

# install curl for prestop script
RUN apk --no-cache add curl

WORKDIR /

# install binary
COPY --from=builder /workspace/simple-sidecar .

USER 65532:65532

ENTRYPOINT ["/simple-sidecar"]
