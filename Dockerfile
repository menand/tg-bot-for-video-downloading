FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /telegram-bot .

FROM alpine:3.23

RUN apk add --no-cache ca-certificates ffmpeg python3 py3-pip

# yt-dlp через pip, а не PyInstaller-бинарём с GitHub: тот распаковывает себя
# при каждом запуске (десятки секунд на слабом VPS), pip-версия стартует ~1с.
# Формат версии у pip — 2026.6.9 (без ведущих нулей), а не тег релиза 2026.06.09.
ARG YT_DLP_VERSION=2026.6.9
RUN pip install --no-cache-dir --break-system-packages yt-dlp==${YT_DLP_VERSION} && \
    yt-dlp --version

RUN adduser -D -g '' botuser && \
    mkdir -p /data && \
    chown botuser /data

COPY --from=builder /telegram-bot /telegram-bot
USER botuser

ENTRYPOINT ["/telegram-bot"]
