package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type glmRecord struct {
	auth     string
	model    string
	imageURL string
	prompt   string
}

// fakeGLM starts an httptest server mimicking the GLM chat completions API
// and points glmAPI at it. answer is returned as the assistant content;
// when errPayload is non-nil it is served instead. The returned channel
// receives one record per request.
func fakeGLM(t *testing.T, answer string, errPayload map[string]any) <-chan glmRecord {
	t.Helper()
	rec := make(chan glmRecord, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var req glmRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		g := glmRecord{
			auth:     r.Header.Get("Authorization"),
			model:    req.Model,
			prompt:   req.Messages[0].Content[1].Text,
			imageURL: req.Messages[0].Content[0].ImageURL.URL,
		}
		rec <- g
		if errPayload != nil {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(errPayload)
			return
		}
		var resp glmResponse
		resp.Choices = make([]struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}, 1)
		resp.Choices[0].Message.Content = answer
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(func() {
		srv.Close()
		close(rec)
	})
	glmAPI = srv.URL
	return rec
}

func writeTestImage(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "frame.jpg")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGLMSaysPeopleTable(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"yes", true},
		{"Yes.", true},
		{"  YEP ", true},
		{"yeah", true},
		{"有人", true},
		{"no", false},
		{"NO", false},
		{"none", false},
		{"nobody.", false},
		{"没有人", false},
		{"无人", false},
	}
	for _, tc := range cases {
		got, err := glmSaysPeople(tc.in)
		if err != nil {
			t.Fatalf("glmSaysPeople(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("glmSaysPeople(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestGLMSaysPeopleUnrecognized(t *testing.T) {
	if _, err := glmSaysPeople("maybe"); err == nil {
		t.Fatal("glmSaysPeople(\"maybe\") succeeded, want error")
	}
	if _, err := glmSaysPeople(""); err == nil {
		t.Fatal("glmSaysPeople(\"\") succeeded, want error")
	}
}

func TestDetectPeopleRequestShapeAndNoAnswer(t *testing.T) {
	path := writeTestImage(t, "jpeg-bytes")
	rec := fakeGLM(t, "no", nil)

	got, err := detectPeople("test-key", defaultGLMModel, path)
	if err != nil {
		t.Fatalf("detectPeople: %v", err)
	}
	if got {
		t.Fatal("detectPeople = true, want false for a \"no\" answer")
	}
	g := <-rec
	if g.auth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want %q", g.auth, "Bearer test-key")
	}
	if g.model != defaultGLMModel {
		t.Fatalf("model = %q, want %q", g.model, defaultGLMModel)
	}
	if g.prompt != glmPrompt {
		t.Fatalf("prompt = %q, want %q", g.prompt, glmPrompt)
	}
	const prefix = "data:image/jpeg;base64,"
	if !strings.HasPrefix(g.imageURL, prefix) {
		t.Fatalf("image_url = %q, want prefix %q", g.imageURL, prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(g.imageURL, prefix))
	if err != nil {
		t.Fatalf("image_url payload is not base64: %v", err)
	}
	if string(decoded) != "jpeg-bytes" {
		t.Fatalf("decoded image payload = %q, want %q", decoded, "jpeg-bytes")
	}
}

func TestDetectPeopleYesAnswer(t *testing.T) {
	path := writeTestImage(t, "frame")
	rec := fakeGLM(t, "yes", nil)
	drainGLM(rec)

	got, err := detectPeople("test-key", defaultGLMModel, path)
	if err != nil {
		t.Fatalf("detectPeople: %v", err)
	}
	if !got {
		t.Fatal("detectPeople = false, want true for a \"yes\" answer")
	}
}

func TestDetectPeopleErrorPayload(t *testing.T) {
	path := writeTestImage(t, "frame")
	rec := fakeGLM(t, "", map[string]any{
		"error": map[string]any{"code": "1002", "message": "invalid api key"},
	})
	drainGLM(rec)

	_, err := detectPeople("bad-key", defaultGLMModel, path)
	if err == nil {
		t.Fatal("detectPeople succeeded, want error for error payload")
	}
	if !strings.Contains(err.Error(), "1002") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("error = %v, want it to mention code and message", err)
	}
}

func TestDetectPeopleUnrecognizedAnswer(t *testing.T) {
	path := writeTestImage(t, "frame")
	rec := fakeGLM(t, "I am not sure", nil)
	drainGLM(rec)

	if _, err := detectPeople("test-key", defaultGLMModel, path); err == nil {
		t.Fatal("detectPeople succeeded, want error for unrecognized answer")
	}
}

func TestDetectPeopleNoChoices(t *testing.T) {
	path := writeTestImage(t, "frame")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(srv.Close)
	orig := glmAPI
	glmAPI = srv.URL
	t.Cleanup(func() { glmAPI = orig })

	if _, err := detectPeople("test-key", defaultGLMModel, path); err == nil {
		t.Fatal("detectPeople succeeded, want error for zero choices")
	}
}

func TestDetectPeopleMissingImage(t *testing.T) {
	if _, err := detectPeople("test-key", defaultGLMModel, filepath.Join(t.TempDir(), "gone.jpg")); err == nil {
		t.Fatal("detectPeople succeeded, want error for missing image")
	}
}

// withNoRetryWait shrinks the rate-limit backoff to zero for the duration of
// a test so retries stay instant.
func withNoRetryWait(t *testing.T) {
	t.Helper()
	orig := glmRetrySleep
	glmRetrySleep = func(time.Duration) {}
	t.Cleanup(func() { glmRetrySleep = orig })
}

func TestDetectPeopleRetriesRateLimit(t *testing.T) {
	withNoRetryWait(t)
	path := writeTestImage(t, "frame")

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.Write([]byte(`{"error":{"code":"1302","message":"您的账户已达到速率限制，请您控制请求频率"}}`))
			return
		}
		w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"no"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)
	orig := glmAPI
	glmAPI = srv.URL
	t.Cleanup(func() { glmAPI = orig })

	people, err := detectPeople("test-key", defaultGLMModel, path)
	if err != nil {
		t.Fatalf("detectPeople after retry: %v", err)
	}
	if people {
		t.Fatal("detectPeople = true, want false")
	}
	if n := serveCalls(&mu, &calls); n != 2 {
		t.Fatalf("served %d calls, want 2 (one rate limited, one success)", n)
	}
}

func TestDetectPeopleGivesUpAfterRetries(t *testing.T) {
	withNoRetryWait(t)
	path := writeTestImage(t, "frame")

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Write([]byte(`{"error":{"code":"1302","message":"rate limited"}}`))
	}))
	t.Cleanup(srv.Close)
	orig := glmAPI
	glmAPI = srv.URL
	t.Cleanup(func() { glmAPI = orig })

	_, err := detectPeople("test-key", defaultGLMModel, path)
	if err == nil {
		t.Fatal("detectPeople succeeded, want rate-limit error after retries exhausted")
	}
	var e *glmError
	if !errors.As(err, &e) || e.Code != "1302" {
		t.Fatalf("err = %v, want glm error code 1302", err)
	}
	if n := serveCalls(&mu, &calls); n != len(glmRetryWaits)+1 {
		t.Fatalf("served %d calls, want %d (initial + %d retries)", n, len(glmRetryWaits)+1, len(glmRetryWaits))
	}
}

func TestDetectPeopleDoesNotRetryOtherErrors(t *testing.T) {
	withNoRetryWait(t)
	path := writeTestImage(t, "frame")

	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Write([]byte(`{"error":{"code":"1002","message":"invalid api key"}}`))
	}))
	t.Cleanup(srv.Close)
	orig := glmAPI
	glmAPI = srv.URL
	t.Cleanup(func() { glmAPI = orig })

	if _, err := detectPeople("test-key", defaultGLMModel, path); err == nil {
		t.Fatal("detectPeople succeeded, want error for invalid key")
	}
	if n := serveCalls(&mu, &calls); n != 1 {
		t.Fatalf("served %d calls, want 1 (no retry for non-rate-limit errors)", n)
	}
}

func serveCalls(mu *sync.Mutex, calls *int) int {
	mu.Lock()
	defer mu.Unlock()
	return *calls
}

func drainGLM(rec <-chan glmRecord) {
	for {
		select {
		case <-rec:
		default:
			return
		}
	}
}
