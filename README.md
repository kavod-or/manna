# Manna

Manna is a self-hosted, multilingual catering guide for conferences and events. Organizers manage menus in a browser; guests open an event URL or QR code to see the current schedule, food, prices, availability, and declarations.

It is a single Go application with embedded assets, server-rendered HTML, vanilla JavaScript and CSS, and no frontend build step.

## Start locally

Requires Go 1.27.2 or newer. Node.js is only needed for JavaScript tests.

```bash
make dev
```

Open [http://localhost:8080/admin/](http://localhost:8080/admin/) and sign in:

- Username: `admin`
- Password: `manna-local-development`

Choose **Add event**, enter a URL such as `/team-day`, and build the menu. The editor can save private drafts, quickly adjust availability and prices, preview and publish changes, archive events, download QR codes, duplicate events, and restore earlier published versions.

Runtime menus and uploaded branding are stored in the ignored `content` directory. Override the local password when needed:

```bash
MANNA_ADMIN_PASSWORD="a-local-password-with-at-least-16-characters" make dev
```

## Run without automatic reload

Enable the editor with a writable content directory and password:

```bash
MANNA_ADMIN_PASSWORD="a-password-with-at-least-16-characters" \
CONTENT_DIR=content \
go run .
```

For a public-only server, omit the password:

```bash
CONTENT_DIR=content go run .
```

Manna listens on port `8080` by default. Health checks are available at `/healthz`.

## Docker Compose

### Start

Create the local deployment files:

```bash
cp docker-compose.example.yaml docker-compose.yaml
cp .env.example .env
openssl rand -base64 32
```

Put the generated password in `.env`, then start Manna:

```bash
docker compose up --build -d
docker compose logs -f manna
```

Open the public service at [http://localhost:8080](http://localhost:8080). Stop it with:

```bash
docker compose down
```

### Shipped Compose settings

| Setting | Reason |
| --- | --- |
| `127.0.0.1:8080:8080` | Makes Manna reachable only from the host, not the network |
| `./content:/app/content` | Persists menus, drafts, revision history, and branding across container replacement |
| `read_only: true` | Keeps the container filesystem immutable outside mounted content and `/tmp` |
| UID `10001` | Runs Manna without root privileges |
| `cap_drop: [ALL]` | Removes Linux capabilities |
| `no-new-privileges` | Prevents privilege escalation |
| Restricted `/tmp` | Provides a small temporary filesystem with `noexec`, `nosuid`, and `nodev` |
| Memory/PID limits | Bounds resource use |

The host `content` directory must be writable by container UID `10001`. If startup reports a permissions error, grant that UID access without making the container root.

### HTTPS and trusted proxies

Remote admin access requires HTTPS. Keep port `8080` private and place a TLS-terminating reverse proxy in front of Manna. The proxy must replace—not append—these headers:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-For $remote_addr;
}
```

Then configure `.env`:

```dotenv
MANNA_ADMIN_TRUST_PROXY_HTTPS=true
MANNA_ADMIN_TRUSTED_PROXY_CIDRS=<exact proxy address or CIDR>
```

For a host proxy connecting through Docker’s published port, use the source address that the container sees, which may be the Docker bridge gateway rather than `127.0.0.1`. For a proxy in another container, use a dedicated Docker network and trust only that network or proxy address. Never use a broad range such as `0.0.0.0/0`, and never expose Manna’s backend port directly to untrusted networks.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | HTTP server port |
| `CONTENT_DIR` | embedded content | Writable runtime menus and branding |
| `MANNA_ADMIN_PASSWORD` | unset | Enables `/admin/`; minimum 16 characters |
| `MANNA_ADMIN_TRUST_PROXY_HTTPS` | `false` | Trust HTTPS forwarding information from configured proxies |
| `MANNA_ADMIN_TRUSTED_PROXY_CIDRS` | unset | Comma-separated trusted proxy addresses or CIDRs |

Without `CONTENT_DIR`, Manna uses bundled read-only content. Without `MANNA_ADMIN_PASSWORD`, the admin editor is disabled.

## Publishing and content

- Each event has its own public URL and validated YAML menu.
- Published files are replaced atomically and appear without a restart.
- **Save draft** keeps one private, server-side working copy per event. Drafts may be incomplete and are never shown to guests.
- Publishing validates the saved draft, archives the current menu, and removes the draft. If the published menu changed in the meantime, the draft is preserved and publication is blocked.
- Revision checks prevent overwriting newer changes.
- The ten previous published versions are retained for restore.
- Archiving hides an event and its QR code without deleting its menu, branding, or history. Use **Make public** to restore it.
- **Remove event** permanently deletes an event and its event-owned files.
- Invalid external edits do not replace the last valid menu.
- Event URLs are public but are not listed on the root page.

## Development

```bash
make test                         # Go and JavaScript tests
go test ./...                     # Go tests only
node --test tests/*.test.cjs      # JavaScript tests only
make build                        # Build bin/manna
```

Application dependencies are in `go.mod`. The automatic-reload tool is kept separately in `dev.mod`.

## License

Copyright © 2026 Kavod'or.

Licensed under [AGPL-3.0](LICENSE).
