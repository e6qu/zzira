# Build stage: compile the server (static) and the browser worker (wasm).
# The Go image comes from the Amazon ECR Public copy of Docker's official
# images, pinned to the same index digest Docker Hub serves: Docker Hub limits
# anonymous pulls per address, and the shared CI runners exhaust it.
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26.9-alpine@sha256:cdfd4fe2da6b225d8b40c6b7a105736e548e83ff56d5d8f9394446eeb5eb84e0 AS build
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
COPY web/static /static
COPY --from=build /out/zzira-worker.wasm /static/zzira-worker.wasm
COPY --from=build /out/wasm_exec.js /static/wasm/wasm_exec.js
# The demo scenario the -mode=demo flag defaults to. Without it in the image,
# the documented `docker compose exec zzira /zzira-server -mode=demo` answers
# "open demo/company.json: no such file or directory" -- the mode cannot build
# a company anywhere but a source checkout. The default path is relative and
# the working directory of a scratch image is /, so /demo/company.json is
# exactly what the flag already looks for.
COPY demo/company.json /demo/company.json
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV STATIC_DIR=/static \
    DATA_DIR=/data \
    SERVER_PORT=8080
USER 65532:65532
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/zzira-server"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD ["/zzira-server", "-healthcheck"]
