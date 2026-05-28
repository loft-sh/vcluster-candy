# Build the vcluster-candy binary
FROM golang:1.26.0 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

COPY go.mod go.mod
COPY go.sum go.sum
COPY vendor/ vendor/

COPY pkg/ pkg/
COPY cmd/ cmd/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a ./cmd/vcluster-candy/

# Use distroless as minimal base image to package the vcluster-candy binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/vcluster-candy .
USER 65532:65532

ENTRYPOINT ["/vcluster-candy"]
