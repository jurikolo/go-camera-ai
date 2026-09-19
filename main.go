// Command camera scans a subnet for RTSP cameras and grabs one snapshot
// per camera with ffmpeg. It is a Go replacement for the older bash grab
// script and is meant to run from cron on a Raspberry Pi.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config holds everything derived from the command line flags.
type Config struct {
	Subnet         string
	OutputDir      string
	MinSize        int64
	RTSPPort       int
	ProbeTimeout   time.Duration
	CaptureTimeout time.Duration
	ScanWorkers    int
	NameMap        map[string]string
	FFmpegPath     string
	// Telegram delivery: ChatToken is the bot token, ChatList/ChatFlags are
	// the chat IDs that receive every captured image. AlertChats/AlertFlags
	// are the chat IDs that additionally receive images when GLM detects a
	// person. GLMKey/GLMModel configure the vision analysis.
	ChatToken  string   // Telegram bot token
	ChatList   string   // comma/space separated chat IDs
	ChatFlags  []string // repeated -common-chat-list values
	AlertChats string   // comma/space separated alert chat IDs
	AlertFlags []string // repeated -alert-chat-list values
	GLMKey     string   // GLM API key for people detection
	GLMModel   string   // GLM vision model name
}

// chatFlags collects repeated -common-chat-list options.
type chatFlags []string

func (c *chatFlags) String() string { return strings.Join(*c, ", ") }

func (c *chatFlags) Set(v string) error {
	*c = append(*c, v)
	return nil
}

// nameFlags collects repeated -name ip=filename options.
type nameFlags []string

func (n *nameFlags) String() string { return strings.Join(*n, ", ") }

func (n *nameFlags) Set(v string) error {
	*n = append(*n, v)
	return nil
}

func main() {
	log.SetFlags(log.LstdFlags)

	var names nameFlags
	var chats chatFlags
	var alerts chatFlags
	cfg := Config{}
	flag.StringVar(&cfg.Subnet, "subnet", "", "IPv4 subnet to scan for cameras, e.g. 192.168.8.0/24 (required)")
	flag.StringVar(&cfg.OutputDir, "output-dir", "", "directory that receives the captured images (required)")
	flag.Int64Var(&cfg.MinSize, "min-size", 5000, "minimum image size in bytes for a capture to count as good")
	flag.IntVar(&cfg.RTSPPort, "port", 554, "RTSP port probed on every host")
	flag.DurationVar(&cfg.ProbeTimeout, "probe-timeout", time.Second, "TCP connect timeout per host while scanning")
	flag.DurationVar(&cfg.CaptureTimeout, "capture-timeout", 10*time.Second, "timeout for a single ffmpeg invocation")
	flag.IntVar(&cfg.ScanWorkers, "scan-workers", 64, "number of hosts probed concurrently")
	flag.Var(&names, "name", "output file for one camera as ip=filename, e.g. -name 192.168.8.58=area.jpg (repeatable)")
	flag.StringVar(&cfg.ChatToken, "tg-bot-token", "", "Telegram bot token used to send captured images (required)")
	flag.Var(&chats, "common-chat-list", "Telegram chat IDs receiving images, e.g. -common-chat-list 123456789 (repeatable)")
	flag.StringVar(&cfg.AlertChats, "alert-chat-list", "", "comma/space separated Telegram chat IDs receiving images where GLM detected a person (repeatable)")
	flag.StringVar(&cfg.GLMKey, "glm-api-key", "", "GLM API key used for people detection (required with -alert-chat-list)")
	flag.StringVar(&cfg.GLMModel, "glm-model", defaultGLMModel, "GLM vision model used for people detection")
	flag.Parse()

	cfg.ChatFlags = chats
	cfg.AlertFlags = alerts
	cfg.NameMap = make(map[string]string, len(names))
	for _, spec := range names {
		ip, name, err := parseNameMapping(spec)
		if err != nil {
			log.Fatalf("invalid -name option %q: %v", spec, err)
		}
		cfg.NameMap[ip] = name
	}

	os.Exit(run(cfg))
}

func run(cfg Config) int {
	if cfg.Subnet == "" {
		fmt.Fprintln(os.Stderr, "camera: the -subnet flag is required, e.g. camera -subnet 192.168.8.0/24")
		flag.PrintDefaults()
		return 2
	}
	if cfg.OutputDir == "" {
		fmt.Fprintln(os.Stderr, "camera: the -output-dir flag is required, e.g. camera -output-dir /var/camera")
		flag.PrintDefaults()
		return 2
	}

	if cfg.ChatToken == "" {
		fmt.Fprintln(os.Stderr, "camera: the -tg-bot-token flag is required, e.g. camera -tg-bot-token 123456:ABC-DEF...")
		flag.PrintDefaults()
		return 2
	}
	chatIDs := parseChatIDs(cfg.ChatList)
	for _, v := range cfg.ChatFlags {
		chatIDs = append(chatIDs, parseChatIDs(v)...)
	}
	if len(chatIDs) == 0 {
		fmt.Fprintln(os.Stderr, "camera: at least one chat id must be given with -common-chat-list")
		flag.PrintDefaults()
		return 2
	}
	alertChatIDs := parseChatIDs(cfg.AlertChats)
	for _, v := range cfg.AlertFlags {
		alertChatIDs = append(alertChatIDs, parseChatIDs(v)...)
	}
	if len(alertChatIDs) > 0 && cfg.GLMKey == "" {
		fmt.Fprintln(os.Stderr, "camera: -glm-api-key is required when -alert-chat-list is given")
		flag.PrintDefaults()
		return 2
	}
	if cfg.GLMModel == "" {
		cfg.GLMModel = defaultGLMModel
	}

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		log.Printf("ffmpeg not found in PATH: %v", err)
		return 1
	}
	cfg.FFmpegPath = ffmpeg

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		log.Printf("cannot create output directory %s: %v", cfg.OutputDir, err)
		return 1
	}

	log.Printf("scanning %s for cameras on port %d", cfg.Subnet, cfg.RTSPPort)
	cameras, err := scanSubnet(cfg)
	if err != nil {
		log.Printf("scan failed: %v", err)
		return 1
	}
	if len(cameras) == 0 {
		log.Printf("no RTSP cameras found in %s", cfg.Subnet)
		return 1
	}
	log.Printf("found %d camera(s): %s", len(cameras), strings.Join(cameras, ", "))

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentCaptures)
	var mu sync.Mutex
	captured := 0
	for _, ip := range cameras {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			path, ok := captureFromCamera(ip, cfg)
			if !ok {
				return
			}
			mu.Lock()
			captured++
			mu.Unlock()
			sendIfChanged(cfg, path, chatIDs, alertChatIDs)
		})
	}
	wg.Wait()

	if captured == 0 {
		log.Printf("failed to capture an image from any camera")
		return 1
	}
	log.Printf("captured images from %d of %d camera(s)", captured, len(cameras))
	return 0
}

// sendIfChanged sends the image to the configured Telegram chats, but only
// if it differs from the last version that was delivered (tracked with a
// .sha256 sidecar file next to the image). When alert chats and a GLM API
// key are configured, newly delivered images are additionally analysed for
// people and, on a positive result, sent to the alert chats with the same
// bot token. Errors are logged, not fatal.
func sendIfChanged(cfg Config, path string, chatIDs, alertChatIDs []string) {
	sum, err := hashFile(path)
	if err != nil {
		log.Printf("%s: cannot hash image: %v", path, err)
		return
	}
	if old := readHash(path); old == sum {
		return // image unchanged since the last delivery
	}
	if err := sendImageToChats(cfg.ChatToken, chatIDs, path); err != nil {
		log.Printf("%s: telegram delivery failed: %v", path, err)
		return
	}
	log.Printf("%s: sent image to %d chat(s)", path, len(chatIDs))

	if len(alertChatIDs) == 0 || cfg.GLMKey == "" {
		return // people detection not configured
	}
	people, err := detectPeople(cfg.GLMKey, cfg.GLMModel, path)
	if err != nil {
		log.Printf("%s: glm detection failed: %v", path, err)
		return
	}
	if !people {
		return
	}
	if err := sendImageToChats(cfg.ChatToken, alertChatIDs, path); err != nil {
		log.Printf("%s: alert delivery failed: %v", path, err)
		return
	}
	log.Printf("%s: person detected, sent alert to %d chat(s)", path, len(alertChatIDs))
}

// parseNameMapping splits an ip=filename option. The filename is reduced
// to its base so a stray slash cannot write outside the output directory.
func parseNameMapping(spec string) (ip, name string, err error) {
	ip, name, ok := strings.Cut(spec, "=")
	if !ok || ip == "" || name == "" {
		return "", "", fmt.Errorf("expected ip=filename")
	}
	if net.ParseIP(ip) == nil {
		return "", "", fmt.Errorf("%q is not an IP address", ip)
	}
	name = filepath.Base(name)
	if name == "." || name == ".." {
		return "", "", fmt.Errorf("%q is not a file name", name)
	}
	return ip, name, nil
}
