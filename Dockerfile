# Build stage: compile the server (static) and the browser worker (wasm).
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X github.com/e6qu/zzira/internal/build.Version=$VERSION" -o /out/zzira-server ./cmd/server
RUN CGO_ENABLED=0 GOOS=js GOARCH=wasm go build -trimpath -ldflags "-s -w -X github.com/e6qu/zzira/internal/build.Version=$VERSION" -o /out/zzira-worker.wasm ./cmd/client
RUN cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" /out/wasm_exec.js

# Runtime: scratch — the static binary is the entire dependency surface.
FROM scratch
COPY --from=build /out/zzira-server /zzira-server
COPY --from=build /out/zzira-worker.wasm /static/zzira-worker.wasm
COPY --from=build /out/wasm_exec.js /static/wasm/wasm_exec.js
COPY web/static /static
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV STATIC_DIR=/static \
    DATA_DIR=/data \
    SERVER_PORT=8080
USER 65532:65532
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/zzira-server"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD ["/zzira-server", "-healthcheck"]
