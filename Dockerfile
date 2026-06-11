FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /telegram-bot .

FROM alpine:3.23

RUN apk add --no-cache ca-certificates ffmpeg

ARG YT_DLP_VERSION=2026.06.09
RUN arch="$(uname -m)" && \
    case "$arch" in \
        x86_64)  bin="yt-dlp_musllinux" ;; \
        aarch64) bin="yt-dlp_musllinux_aarch64" ;; \
        *)       echo "Unsupported arch: $arch" && exit 1 ;; \
    esac && \
    wget -q "https://github.com/yt-dlp/yt-dlp/releases/download/${YT_DLP_VERSION}/${bin}" \
         -O /usr/local/bin/yt-dlp && \
    chmod +x /usr/local/bin/yt-dlp && \
    yt-dlp --version

RUN adduser -D -g '' botuser && \
    mkdir -p /data && \
    chown botuser /data

COPY --from=builder /telegram-bot /telegram-bot
USER botuser

ENTRYPOINT ["/telegram-bot"]
