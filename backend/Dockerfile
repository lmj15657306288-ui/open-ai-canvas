# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS payment-package-build

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY

RUN apk add --no-cache nodejs unzip zip

WORKDIR /src
# 插件只下载实际依赖，不为四个小程序预取整个业务后端的模块图。
COPY backend ./backend
COPY plugin-packages ./plugin-packages
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build set -eu; \
    test -n "$TARGETOS"; \
    test -n "$TARGETARCH"; \
    PAYMENT_PLUGIN_GOOS="$TARGETOS" \
    PAYMENT_PLUGIN_GOARCH="$TARGETARCH" \
    PAYMENT_PLUGIN_CGO_ENABLED=0 \
      ./plugin-packages/build-packages.sh --payments-only; \
    sh ./plugin-packages/verify-payment-packages.sh "$TARGETOS" "$TARGETARCH"

FROM alpine:3.22 AS payment-package-smoke

ARG TARGETOS
ARG TARGETARCH
RUN apk add --no-cache unzip
COPY --from=payment-package-build /src/plugin-packages /app/plugin-packages
RUN PAYMENT_PLUGIN_SMOKE_TEST=1 \
    sh /app/plugin-packages/verify-payment-packages.sh "$TARGETOS" "$TARGETARCH"

FROM golang:1.25-alpine AS backend-build

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY

RUN apk add --no-cache build-base

WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
# go.mod 含本地 replace（github.com/casdoor/go-sms-sender => ./third_party/go-sms-sender）。
# go mod download 解析依赖时就需要读到该目录的 go.mod，而这行执行时完整源码尚未复制，
# 缺了它会报 "reading third_party/go-sms-sender/go.mod: no such file or directory"。
# 后面 COPY backend ./ 会覆盖为完整源码，这里只为让依赖解析通过。
COPY backend/third_party ./third_party
# 模块保留在普通镜像层中，供后续编译及跨 Runner 的 GHA 层缓存复用。
RUN go mod download
COPY backend ./
COPY VERSION /src/VERSION
ARG BUILD_VERSION
ARG BUILD_COMMIT=unknown
ARG BUILD_TIME=unknown
RUN --mount=type=cache,target=/root/.cache/go-build set -eu; \
    version="${BUILD_VERSION:-$(tr -d '\r\n' </src/VERSION)}"; \
    ldflags="-s -w -X yingce/backend/internal/buildinfo.Version=${version} -X yingce/backend/internal/buildinfo.Commit=${BUILD_COMMIT} -X yingce/backend/internal/buildinfo.BuildTime=${BUILD_TIME}"; \
    CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="$ldflags" -o /out/yingce-backend ./cmd/server; \
    CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="$ldflags" -o /out/migrate-schema ./cmd/migrate-schema; \
    CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="$ldflags" -o /out/migrate-sqlite-postgres ./cmd/migrate-sqlite-postgres

FROM node:22.19-alpine AS pi-runtime-deps

WORKDIR /app/backend/agent-runtime/pi
COPY backend/agent-runtime/pi/package.json backend/agent-runtime/pi/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts

FROM node:22.19-alpine

# ffmpeg/ffprobe：短剧抽取剧情段视频最后一帧、时间线合成成片与视频播放转码。
RUN apk add --no-cache ca-certificates tzdata wget ffmpeg \
    && addgroup -S app \
    && adduser -S app -G app

# yt-dlp：分享链接解析（B 站/YouTube/X 等，见 internal/videoparser）。
# 用官方 musl 静态二进制而不是 apk 安装——alpine 的 yt-dlp 会连带拉进 python3/glib
# 约 170MB 依赖，构建多花 3 分钟；静态二进制只有 37MB。
# 版本固定并校验官方 SHA2-256SUMS，避免 latest 漂移导致构建不可复现。
ARG TARGETARCH
ARG YTDLP_VERSION=2025.11.12
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
      amd64) ytdlp_file=yt-dlp_musllinux ;; \
      arm64) ytdlp_file=yt-dlp_musllinux_aarch64 ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    base="https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}"; \
    cd /tmp; \
    wget -q -O yt-dlp "${base}/${ytdlp_file}"; \
    wget -q -O SHA2-256SUMS "${base}/SHA2-256SUMS"; \
    grep " ${ytdlp_file}\$" SHA2-256SUMS > yt-dlp.sums; \
    sed "s|  ${ytdlp_file}\$|  yt-dlp|" yt-dlp.sums | sha256sum -c -; \
    install -m 0755 yt-dlp /usr/local/bin/yt-dlp; \
    rm -f yt-dlp SHA2-256SUMS yt-dlp.sums; \
    yt-dlp --version

WORKDIR /app
ENV GIN_MODE=release
ENV CANVAS_BACKEND_ADDR=:8080
ENV CANVAS_BACKEND_DATA_DIR=/data

RUN mkdir -p /data && chown -R app:app /data
COPY --from=backend-build /out/yingce-backend /usr/local/bin/yingce-backend
COPY --from=backend-build /out/migrate-schema /usr/local/bin/migrate-schema
COPY --from=backend-build /out/migrate-sqlite-postgres /usr/local/bin/migrate-sqlite-postgres
COPY --from=payment-package-smoke /app/plugin-packages /app/plugin-packages
COPY --from=pi-runtime-deps /app/backend/agent-runtime/pi/node_modules /app/backend/agent-runtime/pi/node_modules
COPY backend/agent-runtime/pi/package.json backend/agent-runtime/pi/package-lock.json /app/backend/agent-runtime/pi/
COPY backend/agent-runtime/pi/agent-runtime.mjs /app/backend/agent-runtime/pi/agent-runtime.mjs

USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:8080/api/health/ready >/dev/null || exit 1
CMD ["yingce-backend"]
