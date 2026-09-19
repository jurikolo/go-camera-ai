package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
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

// detectPeople sends the JPEG at imagePath to the GLM vision model and
// reports whether the model thinks it shows at least one person.
func detectPeople(apiKey, model, imagePath string) (bool, error) {
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
		return false, fmt.Errorf("glm error %s: %s", parsed.Error.Code, parsed.Error.Message)
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
