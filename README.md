# go-camera-ai

Integration with web cameras and AI.

`camera` scans a subnet for RTSP cameras and saves one snapshot per camera
using `ffmpeg`.

## Requirements

- Go 1.27 or newer to build
- `ffmpeg` available in `PATH`
- Cameras answering on RTSP port 554 with the URL scheme
  `rtsp://<ip>:554/user=admin_password=_channel=0_stream=<0|1>.sdp`

## Build

    go build -o camera .

Cross-compiling:

    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o camera .   # 64-bit ARM
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o camera .  # 32-bit ARM

## Usage

    ./camera -subnet 192.168.8.0/24 -output-dir /path/to/images

Every discovered camera gets an image named after its IP address. To use
custom file names, map cameras with `-name` (repeatable):

    ./camera -subnet 192.168.8.0/24 -output-dir /path/to/images \
        -name 192.168.8.58=parking.jpg \
        -name 192.168.8.204=entrance.jpg

| Flag               | Default    | Meaning                                   |
|--------------------|------------|-------------------------------------------|
| `-subnet`          | (required) | IPv4 CIDR to scan, e.g. `192.168.8.0/24`  |
| `-output-dir`      | (required) | Directory where images are written        |
| `-name`            | none       | `ip=filename` mapping (repeatable)        |
| `-port`            | `554`      | RTSP port probed on every host            |
| `-min-size`        | `5000`     | Minimum valid image size in bytes         |
| `-probe-timeout`   | `1s`       | TCP connect timeout per host              |
| `-capture-timeout` | `10s`      | Timeout for one ffmpeg run                |
| `-scan-workers`    | `64`       | Hosts probed concurrently                 |

Exit codes: `0` when at least one image was captured, `1` when the scan
found nothing or every capture failed, `2` for bad options.

## Behavior notes

- An open RTSP port marks a host as a candidate camera; ffmpeg makes the
  final call, so non-camera hosts are simply skipped.
- Stream 0 is tried first, stream 1 is the fallback when the frame is
  missing or smaller than `-min-size`.
- Images are written to a hidden temp file and renamed into place only
  after the size check passes, so a failed run never destroys the
  previous snapshot.
