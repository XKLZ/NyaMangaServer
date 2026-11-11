ARG NODE_IMAGE=node:24.19.0-bookworm-slim@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df
ARG GO_IMAGE=golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452
ARG PYTHON_IMAGE=python:3.12.0-slim-bookworm@sha256:19a6235339a74eca01227b03629f63b6f5020abc21142436eced6ec3a9839a76
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ENV GOTOOLCHAIN=local
ARG GOPROXY=https://proxy.golang.org,direct
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY migrations/ ./migrations/
COPY LICENSE THIRD_PARTY_NOTICES.md ./
COPY --from=web /src/internal/webassets/dist/ ./internal/webassets/dist/
ARG TARGETOS
ARG TARGETARCH
ARG COMMIT=unknown
RUN test "${#COMMIT}" -eq 40 && ! printf '%s' "$COMMIT" | grep -q '[^0-9a-f]'
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.commit=${COMMIT}" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.commit=${COMMIT}" -o /out/instance-restore ./cmd/instance-restore
RUN mkdir /out/data && chown 65532:65532 /out/data && chmod 700 /out/data

FROM --platform=$TARGETPLATFORM ${GO_IMAGE} AS runtime-libs
RUN mkdir /runtime-libs && for library in libstdc++.so.6 libgcc_s.so.1 libgomp.so.1 libz.so.1; do \
    found=$(find /usr/lib /lib -name "$library" -print -quit); test -n "$found"; \
    cp -L "$found" "/runtime-libs/$library"; done
RUN mkdir -p /runtime-notices && for package in libstdc++6 libgcc-s1 libgomp1 zlib1g ca-certificates; do \
    cp "/usr/share/doc/$package/copyright" "/runtime-notices/$package.copyright"; \
    dpkg-query -W -f='${Package} ${Version} source=${source:Package} ${source:Version}\n' "$package" >> /runtime-notices/packages.txt; done

# Immutable Python 3.12.0 digest verified from Docker official repo-info.
# Build helper also checks the wheel verification report before invoking Docker.
FROM ${PYTHON_IMAGE}
ARG TARGETARCH
ARG PYTHON_IMAGE
ENV OMP_NUM_THREADS=1 OPENBLAS_NUM_THREADS=1 MKL_NUM_THREADS=1 NUMEXPR_NUM_THREADS=1 \
    TOKENIZERS_PARALLELISM=false HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 \
    HF_HUB_DISABLE_TELEMETRY=1 HF_HUB_DISABLE_IMPLICIT_TOKEN=1 DO_NOT_TRACK=1 \
    PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
COPY --from=runtime-libs /runtime-libs/ /usr/local/lib/
COPY --from=runtime-libs /runtime-notices/ /usr/share/nyamangaserver/runtime-notices/
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/nyamangaserver/
COPY --from=build /usr/local/go/LICENSE /usr/share/nyamangaserver/GO-LICENSE
COPY third_party/ /usr/share/nyamangaserver/third_party/
COPY runtime/recommendations/ /opt/nyamanga/recommendations/
RUN case "$PYTHON_IMAGE" in python:3.12.0-slim-bookworm@sha256:*) test "${#PYTHON_IMAGE}" -eq 99 ;; *) exit 1 ;; esac && \
    python -c "import platform; assert platform.python_version() == '3.12.0'" && \
    python -m venv /opt/runtime && \
    /opt/runtime/bin/python -m pip install --no-cache-dir --no-deps --require-hashes --only-binary=:all: \
      -r "/opt/nyamanga/recommendations/requirements-lock-linux-${TARGETARCH}.txt" && \
    /opt/runtime/bin/python -m pip check && \
    ldconfig && /opt/runtime/bin/python -c "import numpy, PIL.Image, onnxruntime, tokenizers" && \
    mkdir -p /secrets /models && chown 65532:65532 /secrets /models && chmod 700 /secrets
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/server /server
COPY --from=build /out/instance-restore /instance-restore
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV NYAMANGA_CONFIG=/run/config/server.json
EXPOSE 9400
HEALTHCHECK --interval=5s --timeout=4s --start-period=5s --retries=5 CMD ["/server", "--healthcheck", "http://127.0.0.1:9400/api/v1/health/ready"]
ENTRYPOINT ["/server"]
