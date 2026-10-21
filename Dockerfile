# Builds on the host platform and cross-compiles for the target one, so
# multi-arch builds (buildx --platform linux/amd64,linux/arm64) need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w \
        -X github.com/prometheus/common/version.Version=${VERSION} \
        -X github.com/prometheus/common/version.Revision=${REVISION} \
        -X github.com/prometheus/common/version.BuildUser=docker" \
      -o /out/raisecom_exporter ./cmd/raisecom_exporter

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED
LABEL org.opencontainers.image.title="raisecom-snmp-exporter" \
      org.opencontainers.image.description="Prometheus SNMP exporter for Raisecom ROS switches (ISCOM2600G): PoE, interfaces, system, SFP DDM" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/raisecom_exporter /bin/raisecom_exporter
COPY config.example.yml /etc/raisecom_exporter/config.yml
USER nonroot:nonroot
EXPOSE 9840
ENTRYPOINT ["/bin/raisecom_exporter"]
CMD ["--config.file=/etc/raisecom_exporter/config.yml"]
