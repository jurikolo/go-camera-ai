package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// glmAPI is the base URL of the GLM open platform. It is a package level
// variable so tests can point it at a local httptest server.
var glmAPI = "https://open.bigmodel.cn/api/paas/v4"

// defaultGLMModel is the vision model used for people detection. It can be
// overridden with the -glm-model flag.
const defaultGLMModel = "glm-4.6v-flash"

// glmClient has its own timeout, independent of the Telegram client, so a
// slow vision model cannot delay image delivery.
var glmClient = &http.Client{Timeout: 60 * time.Second}

// GLM rate limits are account-wide, so concurrent detections from several
// cameras would immediately trip the free tier's request frequency cap.
// The mutex serializes vision requests within the process.
var glmMu sync.Mutex

// Rate-limit retries: Zhipu answers error code "1302" (account rate limit)
// or HTTP 429 when requests arrive too fast. The wait between attempts is a
// variable so tests can shrink it to zero.
var (
	glmRetryWaits = []time.Duration{3 * time.Second, 8 * time.Second}
	glmRetrySleep = time.Sleep
)

// glmPrompt asks the vision model for an unambiguous one word answer so the
// result can be parsed without guessing.
const glmPrompt = "You are a security camera analyst. Does this image show at " +
	"least one person? Answer with exactly one word: yes or no."

// The Chinese alternatives are anchored to the full answer (^...$) so the
// "有人" inside the negation "没有人" cannot be mistaken for an affirmative.
var (
	glmYesRe = regexp.MustCompile(`^(?:yes|yep|yeah)\b|^有人$`)
	glmNoRe  = regexp.MustCompile(`^(?:no|none|nobody)\b|^没有人$|^无人$`)
)

type glmRequest struct {
	Model    string       `json:"model"`
	Messages []glmMessage `json:"messages"`
}

type glmMessage struct {
	Role    string       `json:"role"`
	Content []glmContent `json:"content"`
}

type glmContent struct {
	Type     string    `json:"type"` // "image_url" or "text"
	ImageURL *glmImage `json:"image_url,omitempty"`
	Text     string    `json:"text,omitempty"`
}

type glmImage struct {
	URL string `json:"url"`
}

type glmResponse struct {
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *glmError `json:"error"`
}

type glmError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *glmError) Error() string {
	return fmt.Sprintf("glm error %s: %s", e.Code, e.Message)
}

// glmRateLimited reports whether a detection failure was caused by GLM's
// account rate limit, which is worth retrying after a pause.
func glmRateLimited(err error) bool {
	var e *glmError
	return errors.As(err, &e) && e.Code == "1302"
}

// detectPeople sends the JPEG at imagePath to the GLM vision model and
// reports whether the model thinks it shows at least one person. A request
// rejected with the account rate limit (error 1302 or HTTP 429) is retried
// with a growing pause.
func detectPeople(apiKey, model, imagePath string) (bool, error) {
	glmMu.Lock()
	defer glmMu.Unlock()

	for attempt := 0; ; attempt++ {
		people, err := detectPeopleOnce(apiKey, model, imagePath)
		if err == nil {
			return people, nil
		}
		if attempt >= len(glmRetryWaits) || !glmRateLimited(err) {
			return false, err
		}
		wait := glmRetryWaits[attempt]
		log.Printf("%s: glm rate limited, retrying in %s", imagePath, wait)
		glmRetrySleep(wait)
	}
}

// detectPeopleOnce performs a single GLM chat completion request.
func detectPeopleOnce(apiKey, model, imagePath string) (bool, error) {
	image, err := os.ReadFile(imagePath)
	if err != nil {
		return false, fmt.Errorf("read image: %w", err)
	}

	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(image)
	payload, err := json.Marshal(glmRequest{
		Model: model,
		Messages: []glmMessage{{
			Role: "user",
			Content: []glmContent{
				{Type: "image_url", ImageURL: &glmImage{URL: dataURL}},
				{Type: "text", Text: glmPrompt},
			},
		}},
	})
	if err != nil {
		return false, fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, glmAPI+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := glmClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("glm request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, fmt.Errorf("read glm response: %w", err)
	}

	var parsed glmResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, fmt.Errorf("decode glm response (status %d): %w", resp.StatusCode, err)
	}
	if parsed.Error != nil {
		return false, &glmError{Code: parsed.Error.Code, Message: parsed.Error.Message}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return false, &glmError{Code: "1302", Message: "HTTP 429 (too many requests)"}
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("glm returned status %d", resp.StatusCode)
	}
	if len(parsed.Choices) == 0 {
		return false, errors.New("glm returned no choices")
	}
	return glmSaysPeople(parsed.Choices[0].Message.Content)
}

// glmSaysPeople interprets the model's textual answer.
func glmSaysPeople(content string) (bool, error) {
	answer := strings.ToLower(strings.TrimSpace(content))
	switch {
	case glmYesRe.MatchString(answer):
		return true, nil
	case glmNoRe.MatchString(answer):
		return false, nil
	}
	return false, fmt.Errorf("unrecognized glm answer %q", answer)
}
