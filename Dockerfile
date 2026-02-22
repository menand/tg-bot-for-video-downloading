# Сборка бота
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /telegram-bot .

# Финальный образ: бот + yt-dlp + ffmpeg (для скачивания видео)
FROM alpine:3.19
RUN apk add --no-cache ca-certificates ffmpeg wget && \
    wget -q https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -O /usr/local/bin/yt-dlp && \
    chmod +x /usr/local/bin/yt-dlp && \
    apk del wget

COPY --from=builder /telegram-bot /telegram-bot

# Токен задаётся при запуске: -e TELEGRAM_BOT_TOKEN=... или --env-file .env
ENTRYPOINT ["/telegram-bot"]
