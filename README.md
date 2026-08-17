# bark2ntfy

`bark2ntfy` is a small HTTP bridge for applications that can send [Bark](https://github.com/Finb/Bark)-shaped notifications but cannot send to [ntfy](https://ntfy.sh/) directly.

It accepts a subset of the Bark API, converts it into a ntfy publish request, and sends that request to one configured ntfy server and topic.

It is intentionally **not** a general-purpose Bark server:

- It does not deliver notifications to Bark clients.
- It does not inspect or validate Bark device keys.
- It does not select a ntfy topic per request.
- Every accepted notification is published to the single `NTFY_URL` + `NTFY_TOPIC` configured when the process starts.

## What is forwarded

| Bark input | ntfy output | Notes |
| --- | --- | --- |
| `body` | request body | Required. |
| `title` | `Title` header | Optional. |
| `group` | `Tags` header | The value is passed as ntfy tags; it is not used for grouping by this bridge. |
| `url` | `Click` header | Optional click-through URL. |
| `level: critical` | `Priority: max` | |
| `level: timeSensitive` | `Priority: high` | Matching is case-insensitive. |
| `level: passive` | `Priority: low` | |

`subtitle`, `device_key`, `device_keys`, `sound`, and `icon` are accepted in JSON input but are currently ignored. Any other Bark fields are also ignored.

## Requirements

- A reachable ntfy server.
- A ntfy topic on that server.
- A ntfy access token that may publish to that topic.
- Go **1.26.5 or newer** to build from this repository (the module declares `go 1.26.5`).

The bridge requires a ntfy token even if the target topic is otherwise publicly writable.

## Build and run

Clone or copy the project to the machine that will run the bridge, then build it:

```sh
cd /path/to/bark2ntfy
go build -o bark2ntfy .
```

Set the required environment variables and start the binary:

```sh
export LISTEN=':8080'
export NTFY_URL='https://ntfy.example.com'
export NTFY_TOPIC='my-notification-topic'
export NTFY_TOKEN='tk_your_ntfy_access_token'

./bark2ntfy
```

On startup the process logs the listening address and ntfy target, for example:

```text
bark2ntfy listening on :8080
ntfy target: https://ntfy.example.com/my-notification-topic
```

`run_bark2ntfy.sh` is a convenience launcher. Replace every bracketed placeholder in that file before using it, then make it executable:

```sh
chmod 700 run_bark2ntfy.sh
./run_bark2ntfy.sh
```

Keep that script readable only by the account that runs it: it contains the ntfy token. Do not commit a real token. The repository's `.gitignore` already ignores `.env`, but it does not ignore the launcher script.

## Configuration

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `LISTEN` | No | `:8080` | Address passed to Go's HTTP server. Use `:8080` for all interfaces on port 8080, or `127.0.0.1:8080` for local access only. |
| `NTFY_URL` | No | `https://ntfy.example.com` | Base URL of the ntfy server. A trailing `/` is removed automatically. |
| `NTFY_TOPIC` | No | `1panel` | The ntfy topic for **every** forwarded notification. The topic is URL-escaped before use. |
| `NTFY_TOKEN` | **Yes** | none | ntfy bearer token. The process exits immediately if it is empty. |

The final ntfy publish URL is:

```text
NTFY_URL/NTFY_TOPIC
```

For example, `NTFY_URL=https://ntfy.example.com/` and `NTFY_TOPIC=server alerts` publish to:

```text
https://ntfy.example.com/server%20alerts
```

## HTTP API

The server has four useful endpoint forms.

### Health checks

`GET /ping` returns plain text:

```text
pong
```

`GET /healthz` confirms that the HTTP process is running and returns a Bark-style response:

```json
{"code":200,"message":"success","timestamp":1760000000}
```

Neither health endpoint tests whether ntfy is reachable or whether the configured token can publish.

`GET /` returns basic bridge metadata. It does not publish a notification.

### JSON push endpoint

Send a `POST` request to `/push` with a JSON body. `body` is the only required field.

```sh
curl --fail-with-body -X POST 'http://127.0.0.1:8080/push' \
  -H 'Content-Type: application/json' \
  --data '{
    "title": "Backup complete",
    "body": "nightly backup finished successfully",
    "group": "white_check_mark",
    "url": "https://status.example.com",
    "level": "timeSensitive"
  }'
```

On successful ntfy delivery, the bridge replies with HTTP `200` and:

```json
{"code":200,"message":"success","timestamp":1760000000}
```

The JSON request body is limited to 1 MiB. Invalid JSON, or a missing/whitespace-only `body`, returns HTTP `400`.

### Legacy Bark URL endpoint

The bridge also accepts the common Bark v1 URL forms with either `GET` or `POST`:

```text
/{DEVICE_KEY}/{BODY}
/{DEVICE_KEY}/{TITLE}/{BODY}
```

Examples:

```sh
# Body only
curl --fail-with-body \
  'http://127.0.0.1:8080/unused-device-key/Backup%20finished'

# Title and body
curl --fail-with-body \
  'http://127.0.0.1:8080/unused-device-key/Backup/Finished%20successfully'
```

The device-key path segment is parsed for Bark compatibility but is not used to route or authorize messages. Use URL encoding for path values containing spaces, slashes, `?`, `#`, or other reserved characters. In particular, a slash in a body becomes an additional path segment; the bridge joins all remaining segments with `/`.

These optional query parameters are recognized on a legacy URL:

```text
title=<title>
body=<body>
group=<ntfy tag or tags>
url=<click-through URL>
sound=<accepted but ignored>
icon=<accepted but ignored>
level=critical|timeSensitive|passive
```

For legacy URLs, a non-empty `title` or `body` query parameter overrides the value extracted from the URL path.

Example:

```sh
curl --get --fail-with-body \
  --data-urlencode 'title=Database alert' \
  --data-urlencode 'body=Replication is behind' \
  --data-urlencode 'group=warning' \
  --data-urlencode 'level=critical' \
  'http://127.0.0.1:8080/unused-device-key/ignored'
```

### Responses and errors

All normal API responses are JSON except `/ping`.

| Condition | HTTP status | `message` |
| --- | --- | --- |
| Accepted and published by ntfy | `200` | `success` |
| Invalid JSON, invalid legacy path, or missing body | `400` | explanatory error |
| Unsupported method | `405` | `method not allowed` |
| ntfy connection failure, timeout, or non-2xx reply | `502` | `notification delivery failed` |

The server waits up to 15 seconds for ntfy. A `200` from this bridge means ntfy returned a 2xx response; it does not guarantee that a subscriber device displayed the notification.

## Run with systemd

The supplied `bark2ntfy.service` is a template. Before installing it, replace all four bracketed values:

| Placeholder | Example | Must be |
| --- | --- | --- |
| `[USER]` | `bark2ntfy` | The Linux account that owns and runs the bridge files. |
| `[GROUP]` | `bark2ntfy` | That account's group. |
| `[BARK2NTFY_DIR]` | `/opt/bark2ntfy` | Absolute directory containing `bark2ntfy` and `run_bark2ntfy.sh`. |
| `[RUN_BARK2NTFY.SH Script]` | `/opt/bark2ntfy/run_bark2ntfy.sh` | Absolute path to the executable launcher script. |

The service starts the launcher, so set `LISTEN`, `NTFY_URL`, `NTFY_TOPIC`, and `NTFY_TOKEN` in `run_bark2ntfy.sh` first.

For an installation in `/opt/bark2ntfy` run by a dedicated `bark2ntfy` account:

```sh
# Run as root.
useradd --system --home-dir /opt/bark2ntfy --shell /usr/sbin/nologin bark2ntfy
install -d -o bark2ntfy -g bark2ntfy -m 750 /opt/bark2ntfy

# Copy the built binary, run_bark2ntfy.sh, and service template into place.
# Edit /opt/bark2ntfy/run_bark2ntfy.sh with real values.
chown bark2ntfy:bark2ntfy /opt/bark2ntfy/bark2ntfy /opt/bark2ntfy/run_bark2ntfy.sh
chmod 750 /opt/bark2ntfy/bark2ntfy
chmod 700 /opt/bark2ntfy/run_bark2ntfy.sh
```

Edit the unit so that its service section contains:

```ini
User=bark2ntfy
Group=bark2ntfy
WorkingDirectory=/opt/bark2ntfy
ExecStart=/opt/bark2ntfy/run_bark2ntfy.sh
```

Then install and start it:

```sh
install -o root -g root -m 644 bark2ntfy.service /etc/systemd/system/bark2ntfy.service
systemctl daemon-reload
systemctl enable --now bark2ntfy.service
systemctl status bark2ntfy.service
```

Read its logs with:

```sh
journalctl -u bark2ntfy.service -f
```

After changing the unit file, use `systemctl daemon-reload` before restarting. After changing only `run_bark2ntfy.sh`, restart the service:

```sh
systemctl restart bark2ntfy.service
```

## Run with Docker Compose

Docker Compose builds the bridge locally and runs it as a non-root user. It has
no persistent data volume.

Copy the example configuration, then replace the values with the ntfy server,
topic, and access token for this bridge:

```sh
cp .env.example .env
chmod 600 .env
```

By default, Compose publishes the bridge only on the host loopback address at
`127.0.0.1:8080`. Start it with:

```sh
docker compose up -d --build
docker compose ps
docker compose logs -f bark2ntfy
```

Verify the local health endpoint and then send a test notification:

```sh
curl --fail-with-body http://127.0.0.1:8080/healthz
curl --fail-with-body -X POST http://127.0.0.1:8080/push \
  -H 'Content-Type: application/json' \
  --data '{"title":"bark2ntfy test","body":"Docker Compose bridge works."}'
```

Stop the container without deleting the local image:

```sh
docker compose down
```

### Compose configuration

The included `compose.yaml` reads the following values from `.env`:

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `NTFY_URL` | **Yes** | none | Base URL of the ntfy server. |
| `NTFY_TOPIC` | **Yes** | none | Topic for every forwarded notification. |
| `NTFY_TOKEN` | **Yes** | none | ntfy bearer token. Keep it only in `.env`. |
| `BARK2NTFY_BIND_ADDRESS` | No | `127.0.0.1` | Host interface to which Docker publishes port 8080. |
| `BARK2NTFY_HOST_PORT` | No | `8080` | Host port forwarded to the container's port 8080. |

`NTFY_URL`, `NTFY_TOPIC`, and `NTFY_TOKEN` are intentionally required by
Compose, even though the binary provides development defaults for the first
two. This prevents accidentally publishing to a default ntfy target.

To expose the bridge to a reverse proxy running on the same host, leave the
default loopback bind in place and proxy to `http://127.0.0.1:8080`. To expose
it beyond the host, set `BARK2NTFY_BIND_ADDRESS` to a specific LAN address or
`0.0.0.0` only after adding an appropriate firewall, VPN, or authenticated
reverse proxy. The bridge itself does not authenticate incoming requests.

## Network and security notes

The bridge provides **no incoming authentication**. Anyone who can reach its listening address can ask it to publish to the configured ntfy topic.

- Prefer `LISTEN=127.0.0.1:8080` when a local reverse proxy will expose it.
- If it must listen on a LAN or public interface, restrict access with a firewall, reverse proxy authentication, VPN, or an equivalent network control.
- Treat `NTFY_TOKEN` as a secret. It is sent to ntfy in an `Authorization: Bearer ...` header.
- Use HTTPS for a remote ntfy server so the token and notification content are encrypted in transit.
- Request paths are logged, including legacy URL notification content and query parameters. Do not place sensitive notification content in legacy URLs if logs are accessible to others; use `POST /push` instead.

## Verification checklist

After starting the service, verify the bridge in this order:

```sh
curl --fail-with-body http://127.0.0.1:8080/ping
curl --fail-with-body http://127.0.0.1:8080/healthz
curl --fail-with-body -X POST http://127.0.0.1:8080/push \
  -H 'Content-Type: application/json' \
  --data '{"title":"bark2ntfy test","body":"If you receive this, the bridge works."}'
```

The first command should print `pong`; the next two should return `code: 200`. Finally, confirm that the configured ntfy topic received the test notification.

## License

See [LICENSE](LICENSE).
