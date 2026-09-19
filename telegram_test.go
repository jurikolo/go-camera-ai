package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "img.jpg")
	if err := os.WriteFile(path, []byte("camera image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}
	want := sha256.Sum256([]byte("camera image bytes"))
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("hashFile = %q, want %q", got, hex.EncodeToString(want[:]))
	}
}

func TestReadHashMissingFile(t *testing.T) {
	if got := readHash(filepath.Join(t.TempDir(), "img.jpg")); got != "" {
		t.Fatalf("readHash on missing sidecar = %q, want empty", got)
	}
}

func TestWriteReadHashRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "img.jpg")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeHash(path, "abc123"); err != nil {
		t.Fatalf("writeHash: %v", err)
	}
	if got := readHash(path); got != "abc123" {
		t.Fatalf("readHash = %q, want %q", got, "abc123")
	}
}

func TestParseChatIDs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"123,456", []string{"123", "456"}},
		{"123 456", []string{"123", "456"}},
		{" 123, 456 ,789", []string{"123", "456", "789"}},
		{"", nil},
		{" , ,", nil},
	}
	for _, tc := range cases {
		got := parseChatIDs(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("parseChatIDs(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("parseChatIDs(%q) = %v, want %v", tc.in, got, tc.want)
			}
		}
	}
}

// fakeTelegram starts an httptest server mimicking the Telegram Bot API and
// points telegramAPI at it. The returned channel receives one record per
// sendPhoto request.
func fakeTelegram(t *testing.T, ok bool, failChats map[string]bool) <-chan sendRecord {
	t.Helper()
	rec := make(chan sendRecord, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		chatID := r.FormValue("chat_id")
		file, header, err := r.FormFile("photo")
		if err != nil {
			http.Error(w, "missing photo", http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "bad photo", http.StatusBadRequest)
			return
		}
		rec <- sendRecord{chatID: chatID, filename: header.Filename, body: body}
		if failChats[chatID] || !ok {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "forbidden"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	t.Cleanup(func() {
		srv.Close()
		close(rec)
	})
	telegramAPI = srv.URL
	return rec
}

type sendRecord struct {
	chatID   string
	filename string
	body     []byte
}

func TestSendImageToChatsDeliversAllAndWritesHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entrance.jpg")
	image := []byte("fresh frame")
	if err := os.WriteFile(path, image, 0o644); err != nil {
		t.Fatal(err)
	}

	rec := fakeTelegram(t, true, nil)

	if err := sendImageToChats("tok", []string{"111", "222"}, path); err != nil {
		t.Fatalf("sendImageToChats: %v", err)
	}
	seen := map[string]sendRecord{}
	for range 2 {
		r := <-rec
		seen[r.chatID] = r
	}
	for id := range map[string]bool{"111": true, "222": true} {
		r, ok := seen[id]
		if !ok {
			t.Fatalf("chat %s never received the image", id)
		}
		if r.filename != "entrance.jpg" {
			t.Fatalf("chat %s got filename %q, want %q", id, r.filename, "entrance.jpg")
		}
		if string(r.body) != string(image) {
			t.Fatalf("chat %s got body %q, want %q", id, r.body, image)
		}
	}
	if got := readHash(path); got == "" {
		t.Fatal("sidecar hash not written after successful delivery")
	}
}

func TestSendImageToChatsAllFailNoSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entrance.jpg")
	if err := os.WriteFile(path, []byte("frame"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := fakeTelegram(t, false, nil)

	if err := sendImageToChats("tok", []string{"111"}, path); err == nil {
		t.Fatal("sendImageToChats succeeded, want error when every chat fails")
	}
	drain(rec)
	if got := readHash(path); got != "" {
		t.Fatalf("sidecar written despite total failure: %q", got)
	}
}

func TestSendImageToChatsPartialFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entrance.jpg")
	if err := os.WriteFile(path, []byte("frame"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := fakeTelegram(t, true, map[string]bool{"222": true})

	err := sendImageToChats("tok", []string{"111", "222"}, path)
	if err != nil {
		t.Fatalf("sendImageToChats: %v, want nil when at least one chat succeeds", err)
	}
	drain(rec)
	if got := readHash(path); got == "" {
		t.Fatal("sidecar not written although one chat succeeded")
	}
}

func drain(rec <-chan sendRecord) {
	for {
		select {
		case <-rec:
		default:
			return
		}
	}
}
