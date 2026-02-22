package main

import (
	"bufio"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	telegram "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const maxFileSizeMB = 50 // лимит Telegram для отправки файлов

var urlRegex = regexp.MustCompile(`https?://[^\s]+`)

// loadTokenFromEnvFile читает TELEGRAM_BOT_TOKEN из файла .env в текущей директории.
func loadTokenFromEnvFile() string {
	f, err := os.Open(".env")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		if key != "TELEGRAM_BOT_TOKEN" {
			continue
		}
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)
		return val
	}
	return ""
}

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		token = loadTokenFromEnvFile()
	}
	if token == "" {
		log.Fatal("Установите TELEGRAM_BOT_TOKEN в файле .env или в переменной окружения")
	}

	bot, err := telegram.NewBotAPI(token)
	if err != nil {
		log.Fatalf("Ошибка создания бота: %v", err)
	}

	bot.Debug = true
	log.Printf("Бот запущен: @%s", bot.Self.UserName)

	u := telegram.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatID := update.Message.Chat.ID
		text := strings.TrimSpace(update.Message.Text)
		if text == "" {
			continue
		}

		// Проверяем, есть ли в сообщении ссылка на видео
		if url := extractFirstURL(text); url != "" {
			go handleVideoURL(bot, chatID, url)
			continue
		}

		// Обычные команды
		msg := telegram.NewMessage(chatID, "")
		switch text {
		case "/start":
			msg.Text = "Привет! Я умею скачивать видео. Отправь ссылку на YouTube, VK, Instagram, TikTok или другой сайт — пришлю видео.\n\n/help — справка по командам."
		case "/help":
			msg.Text = "Доступные команды:\n/start — приветствие\n/help — эта справка\n/hello — поздороваться\n\nИли просто отправь ссылку на видео — скачаю и пришлю файл."
		case "/hello":
			msg.Text = "Привет, " + update.Message.From.FirstName + "!"
		default:
			msg.Text = "Отправь ссылку на видео (YouTube, VK, Instagram, TikTok и др.) — я скачаю и пришлю файл. Или используй /help."
		}

		if _, err := bot.Send(msg); err != nil {
			log.Printf("Ошибка отправки: %v", err)
		}
	}
}

func extractFirstURL(text string) string {
	// Убираем возможные завершающие знаки препинания из URL
	raw := urlRegex.FindString(text)
	raw = strings.TrimRight(raw, ".,;:!?)")
	return raw
}

func handleVideoURL(bot *telegram.BotAPI, chatID int64, url string) {
	statusMsg, _ := bot.Send(telegram.NewMessage(chatID, "Скачиваю видео…"))

	filePath, err := downloadVideo(url)
	if err != nil {
		log.Printf("Ошибка скачивания %s: %v", url, err)
		bot.Send(telegram.NewMessage(chatID, "Не удалось скачать видео: "+err.Error()+"\n\nПроверь ссылку и доступность ролика (приватное, регион и т.д.)."))
		bot.Request(telegram.NewDeleteMessage(chatID, statusMsg.MessageID))
		return
	}
	defer os.RemoveAll(filepath.Dir(filePath))

	// Удаляем сообщение "Скачиваю..."
	bot.Request(telegram.NewDeleteMessage(chatID, statusMsg.MessageID))

	info, _ := os.Stat(filePath)
	if info != nil && info.Size() > maxFileSizeMB*1024*1024 {
		bot.Send(telegram.NewMessage(chatID, "Файл получился больше 50 МБ — Telegram не примет такой размер. Попробуй другую ссылку или качество."))
		return
	}

	video := telegram.NewVideo(chatID, telegram.FilePath(filePath))
	if _, err := bot.Send(video); err != nil {
		// Если не отправилось как видео (например, формат), пробуем как документ
		doc := telegram.NewDocument(chatID, telegram.FilePath(filePath))
		if _, err2 := bot.Send(doc); err2 != nil {
			log.Printf("Ошибка отправки файла: %v", err)
			bot.Send(telegram.NewMessage(chatID, "Скачал файл, но не получилось отправить: "+err.Error()))
		}
	}
}

func downloadVideo(url string) (string, error) {
	dir, err := os.MkdirTemp("", "tg-video-*")
	if err != nil {
		return "", err
	}
	outPath := filepath.Join(dir, "video.%(ext)s")

	// yt-dlp: одно видео (без плейлиста), лучшее качество, макс 50 МБ под лимит Telegram
	cmd := exec.Command("yt-dlp",
		"--no-playlist",
		"-f", "best[ext=mp4][filesize<50M]/best[ext=mp4]/best[filesize<50M]/best",
		"--max-filesize", "50M",
		"-o", outPath,
		"--no-warnings",
		url,
	)
	cmd.Dir = dir
	if _, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", err
	}

	// yt-dlp создаёт файл с подставленным расширением
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	os.RemoveAll(dir)
	return "", os.ErrNotExist
}
