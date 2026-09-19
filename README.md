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

Every option is passed as `-name=value`; this is the canonical form and
keeps values such as negative chat IDs or `ip=filename` mappings intact.
The older space-separated form (`-name value`) is still accepted and is
automatically converted before parsing, so existing command lines keep
working.

    ./camera -subnet=192.168.8.0/24 -output-dir=/path/to/images

Every discovered camera gets an image named after its IP address. To use
custom file names, map cameras with `-name` (repeatable):

    ./camera -subnet=192.168.8.0/24 -output-dir=/path/to/images \
        -tg-bot-token=123456:ABC-DEF -common-chat-list=111111 \
        -name=192.168.8.58=area.jpg \
        -name=192.168.8.204=entrance.jpg

To also get an alert in a second chat whenever GLM's vision model
detects a person on a captured image, pass an alert chat list plus a
GLM API key:

    ./camera -subnet=192.168.8.0/24 -output-dir=/path/to/images \
        -tg-bot-token=123456:ABC-DEF -common-chat-list=111111 \
        -alert-chat-list=-5221378345 -glm-api-key=<your-key>.<secret>

Because every flag uses `-name=value`, values that begin with `-`
(negative Telegram group IDs) and values that themselves contain `=`
(`-name=192.168.8.58=area.jpg`) are always parsed correctly — the flag
package splits only on the first `=`.

| Flag               | Default    | Meaning                                   |
|--------------------|------------|-------------------------------------------|
| `-subnet`          | (required) | IPv4 CIDR to scan, e.g. `192.168.8.0/24`  |
| `-output-dir`      | (required) | Directory where images are written        |
| `-name`            | none       | `ip=filename` mapping (repeatable)        |
| `-tg-bot-token` | (required) | Telegram bot token used to send images; the literal `bot` prefix is optional (`123456:ABC-DEF` or `bot123456:ABC-DEF`)   |
| `-common-chat-list` | (required) | Telegram chat IDs (repeatable; also accepts comma/space separated list) |
| `-alert-chat-list` | none | Comma/space separated Telegram chat IDs that receive images where a person was detected (repeatable); group IDs are negative, e.g. `-alert-chat-list=-5221378345` |
| `-glm-api-key` | none | GLM API key (required when `-alert-chat-list` is given) |
| `-glm-model` | `glm-4.6v-flash` | GLM vision model used for people detection |
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
- Each captured image is hashed (SHA-256). The digest is stored next to
  the image in a `.sha256` sidecar file and compared on the next run; an
  identical image is not sent again.
- When the image changed, it is uploaded to every chat given with
  `-common-chat-list` (repeatable flag or comma/space separated IDs)
  using the bot identified by `-tg-bot-token`.
- If `-alert-chat-list` is set, the new image is also sent to GLM's
  vision API (`-glm-model`, default `glm-4v-flash`) which answers whether
  a person is visible. When it does, the image is additionally sent to
  every alert chat, using the same bot token as for the regular chats.- GLM vision requests are serialized (one at a time across all cameras)
  because rate limits apply per account. A request rejected with the
  account rate limit (error 1302 or HTTP 429) is retried with short
  waits; if it stays rate limited, detection is skipped for that image
  and retried on the next run when the image changes.- People detection runs only for newly changed images that were sent
  successfully; alert delivery errors are logged only and do not affect
  the exit code.
