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
	cfg := Config{}
	flag.StringVar(&cfg.Subnet, "subnet", "", "IPv4 subnet to scan for cameras, e.g. 192.168.8.0/24 (required)")
	flag.StringVar(&cfg.OutputDir, "output-dir", "", "directory that receives the captured images (required)")
	flag.Int64Var(&cfg.MinSize, "min-size", 5000, "minimum image size in bytes for a capture to count as good")
	flag.IntVar(&cfg.RTSPPort, "port", 554, "RTSP port probed on every host")
	flag.DurationVar(&cfg.ProbeTimeout, "probe-timeout", time.Second, "TCP connect timeout per host while scanning")
	flag.DurationVar(&cfg.CaptureTimeout, "capture-timeout", 10*time.Second, "timeout for a single ffmpeg invocation")
	flag.IntVar(&cfg.ScanWorkers, "scan-workers", 64, "number of hosts probed concurrently")
	flag.Var(&names, "name", "output file for one camera as ip=filename, e.g. -name 192.168.8.58=parking.jpg (repeatable)")
	flag.Parse()

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
			if captureFromCamera(ip, cfg) {
				mu.Lock()
				captured++
				mu.Unlock()
			}
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
