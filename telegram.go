package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// telegramAPI is the base URL of the Telegram Bot API. Overridable in tests.
var telegramAPI = "https://api.telegram.org"

// httpClient is shared by all Telegram requests; Telegram calls are rare
// (one per changed image per run) so generous timeouts are fine.
var httpClient = &http.Client{Timeout: 90 * time.Second}

// hashFile returns the hex-encoded SHA-256 digest of the file's contents.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readHash returns the stored hash from the .sha256 sidecar of imagePath,
// or "" when the sidecar is missing or unreadable (treated as "never sent").
func readHash(imagePath string) string {
	data, err := os.ReadFile(imagePath + ".sha256")
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(data))
}

// writeHash atomically stores hash in the .sha256 sidecar of imagePath.
func writeHash(imagePath, hash string) error {
	tmp, err := os.CreateTemp(filepath.Dir(imagePath), ".camera-*.sha256")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(hash); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, imagePath+".sha256")
}

// parseChatIDs splits a comma/space separated list of chat IDs.
func parseChatIDs(list string) []string {
	var ids []string
	for _, part := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }) {
		if part = strings.TrimSpace(part); part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}

// sendImageToChats uploads imagePath to every chat via bot sendPhoto.
// The sidecar hash is written only after all sends have been attempted and
// at least one succeeded, so a fully failed delivery is retried next run.
func sendImageToChats(token string, chats []string, imagePath string) error {
	if len(chats) == 0 {
		return errors.New("no telegram chats configured")
	}

	image, err := os.ReadFile(imagePath)
	if err != nil {
		return err
	}

	var delivered bool
	var errs []error
	for _, chat := range chats {
		if err := sendPhoto(token, chat, filepath.Base(imagePath), image); err != nil {
			errs = append(errs, fmt.Errorf("chat %s: %w", chat, err))
			log.Printf("telegram: chat %s: %v", chat, err)
			continue
		}
		delivered = true
	}
	if !delivered {
		return errors.Join(errs...)
	}

	sum := sha256.Sum256(image)
	return writeHash(imagePath, hex.EncodeToString(sum[:]))
}

// sendPhoto performs a single multipart sendPhoto request for one chat.
func sendPhoto(token, chatID, filename string, image []byte) error {
	// The Telegram API URL is "…/bot<token>/sendPhoto". Users often paste the
	// token including its literal "bot" prefix; strip it so the URL does not
	// become "…/botbot<token>/sendPhoto", which Telegram answers with 404.
	token = strings.TrimPrefix(token, "bot")
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	fw, err := w.CreateFormFile("photo", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(image); err != nil {
		return err
	}
	if err := w.WriteField("chat_id", chatID); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/bot%s/sendPhoto", telegramAPI, token), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram sendPhoto: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(respBody))
	}

	var tr struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return fmt.Errorf("telegram sendPhoto: bad response: %w", err)
	}
	if !tr.OK {
		return fmt.Errorf("telegram sendPhoto: %s", tr.Description)
	}
	return nil
}
