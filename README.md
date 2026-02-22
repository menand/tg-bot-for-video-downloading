# Telegram бот на Go

Бот умеет скачивать видео по ссылкам (YouTube, VK, Instagram, TikTok и др.) и присылать файл в чат. Написан на Go с [go-telegram-bot-api](https://github.com/go-telegram-bot-api/telegram-bot-api).

## Требования

- Go 1.21+
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — для скачивания видео (установи отдельно)
- Токен бота от [@BotFather](https://t.me/BotFather) в Telegram

### Установка yt-dlp

- **macOS:** `brew install yt-dlp`
- **Linux:** например `pip install yt-dlp` или скачай бинарник с [релиза](https://github.com/yt-dlp/yt-dlp/releases)

## Как получить токен

1. Открой Telegram и найди [@BotFather](https://t.me/BotFather)
2. Отправь команду `/newbot`
3. Введи имя и username бота
4. Скопируй выданный токен

## Запуск

```bash
# Установка зависимостей
go mod tidy

# Токен: положи в файл .env (он в .gitignore, в репозиторий не попадёт)
cp .env.example .env
# Отредактируй .env и вставь свой TELEGRAM_BOT_TOKEN

go run main.go
```

Либо задай токен переменной окружения:

```bash
TELEGRAM_BOT_TOKEN="твой_токен" go run main.go
```

## Команды бота

- `/start` — приветствие
- `/help` — справка по командам
- `/hello` — персональное приветствие
- **Ссылка на видео** — бот скачает ролик и пришлёт файлом (YouTube, VK, Instagram, TikTok и другие сайты, которые поддерживает yt-dlp)

Файлы больше 50 МБ Telegram не принимает — в таком случае бот напишет об этом.

## Сборка

```bash
go build -o telegram-bot
./telegram-bot
```

## Запуск в Docker на VPS

На сервере нужны только Docker (и при желании Docker Compose).

### Вариант 1: Docker Compose (удобнее)

```bash
# На VPS: клонируй репозиторий и перейди в каталог
git clone <url> telegram-bot && cd telegram-bot

# Создай .env с токеном (файл в .gitignore)
echo 'TELEGRAM_BOT_TOKEN=твой_токен' > .env

# Сборка и запуск в фоне, с автоперезапуском
docker-compose down && docker-compose up -d --build

# Логи
docker compose logs -f bot
```

### Вариант 2: Только Docker

```bash
docker build -t telegram-bot .
docker run -d --restart unless-stopped --env-file .env --name telegram-bot telegram-bot
```

Токен можно передать и так: `-e TELEGRAM_BOT_TOKEN=твой_токен` вместо `--env-file .env`.

В образ входят бот, yt-dlp и ffmpeg — ничего дополнительно на хост ставить не нужно.

## Структура проекта

```
telegram-bot/
├── main.go           # точка входа и логика бота
├── go.mod
├── go.sum
├── Dockerfile        # образ с ботом и yt-dlp
├── docker-compose.yml
├── .env.example
└── README.md
```

Добавляй новые команды в `switch update.Message.Text` в `main.go` или выноси обработчики в отдельные пакеты по мере роста проекта.
