# syntax=docker/dockerfile:1

FROM golang:1.27 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/Harfull/Kest/internal/version.Version=${VERSION} -X github.com/Harfull/Kest/internal/version.Commit=${COMMIT}" \
    -o /out/kest ./cmd/kest && mkdir -p /out/data

# Distroless static includes CA certificates; zone data is embedded in the binary.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/kest /kest
COPY --from=build --chown=65532:65532 /out/data /data

ENV KEST_DATA_DIR=/data
VOLUME ["/data"]

USER nonroot:nonroot
ENTRYPOINT ["/kest"]
