# go-camera-ai

Integration with web camera and AI.

`camera` scans a subnet for RTSP cameras and saves one snapshot per
camera using `ffmpeg`. Go replacement for the old bash grab script,
intended to run from cron on a Raspberry Pi 4.

## Requirements

- Go 1.27 or newer to build
- `ffmpeg` on the Pi: `sudo apt install ffmpeg`
- Cameras answering on RTSP port 554 with the URL scheme
  `rtsp://<ip>:554/user=admin_password=_channel=0_stream=<0|1>.sdp`
  (the scheme used by the existing H.264 IP cameras)

## Build

On the Pi (or this machine, which is also linux/arm64):

    go build -o camera .

Cross-compiling from a PC:

    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o camera .   # Pi 4, 64-bit OS
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o camera .  # Pi 4, 32-bit OS

Copy the binary to the Pi:

    scp camera jurikolo@<pi-ip>:~/bin/

## Usage

    ./camera -subnet 192.168.8.0/24

Every discovered camera gets an image named after its IP address, e.g.
`/home/jurikolo/camera/192.168.8.58.jpg`. To keep the old file names,
map cameras with `-name` (repeatable):

    ./camera -subnet 192.168.8.0/24 \
        -name 192.168.8.58=parking_kolya.jpg \
        -name 192.168.8.204=entrance.jpg

All flags:

| Flag               | Default                 | Meaning                                  |
|--------------------|-------------------------|------------------------------------------|
| `-subnet`          | (required)              | IPv4 CIDR to scan, e.g. `192.168.8.0/24` |
| `-output-dir`      | `/home/jurikolo/camera` | Where images are written                 |
| `-name`            | none                    | `ip=filename` mapping (repeatable)       |
| `-port`            | `554`                   | RTSP port probed on every host           |
| `-min-size`        | `5000`                  | Minimum valid image size in bytes        |
| `-probe-timeout`   | `1s`                    | TCP connect timeout per host             |
| `-capture-timeout` | `10s`                   | Timeout for one ffmpeg run               |
| `-scan-workers`    | `64`                    | Hosts probed concurrently                |

Exit codes: `0` when at least one image was captured, `1` when the scan
found nothing or every capture failed, `2` for bad options.

## Cron

    */5 * * * * /home/jurikolo/bin/camera -subnet 192.168.8.0/24 -name 192.168.8.58=parking_kolya.jpg -name 192.168.8.204=entrance.jpg >> /home/jurikolo/camera/camera.log 2>&1

or, keeping the old journald logging:

    */5 * * * * /home/jurikolo/bin/camera -subnet 192.168.8.0/24 2>&1 | systemd-cat -p info -t camera

## Behavior notes

- The scan probes TCP port 554 on all usable addresses of the subnet
  concurrently; an open port is treated as a candidate camera and ffmpeg
  makes the final call, so non-camera hosts are simply skipped.
- Like the old script, stream 0 is tried first and stream 1 is the
  fallback when the frame is missing or smaller than `-min-size`.
- Images are written to a hidden temp file and renamed into place only
  after the size check passes, so a reader (e.g. a web page) never sees
  a half-written image, and a failed run never destroys the previous
  snapshot. The `yes |` hack from the script is replaced by ffmpeg's
  `-y` flag; writing to a fresh temp file means ffmpeg is never asked
  to overwrite anything.
