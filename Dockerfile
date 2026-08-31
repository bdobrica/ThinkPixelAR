FROM golang:1.26.7-alpine3.23@sha256:b17af760035fc2f338eed92d448a6c67f2d45438844fc6c60678fa5f99e44b57 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/thinkpixelar ./cmd/thinkpixelar

FROM gcr.io/distroless/static-debian13:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7

LABEL org.opencontainers.image.source="https://github.com/bdobrica/ThinkPixelAR" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build --chown=65532:65532 /out/thinkpixelar /usr/local/bin/thinkpixelar

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/thinkpixelar"]
