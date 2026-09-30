# Manna

Manna is a self-hosted catering guide for conferences and events. Guests open an event-specific URL—typically from a QR code—to see the current schedule, meals, food trucks, drinks, snacks, prices, availability, and food declarations in their preferred language.

Organizers manage events and menus in a browser-based admin interface with a live guest preview. Published changes are validated and become available without restarting the server.

## Highlights

- Multiple events with separate public URLs
- Multilingual menus and automatic browser-language selection
- Scheduled meals, food trucks, permanent refreshments, prices, and sold-out states
- Allergen, additive, and other food information
- Event branding and responsive, accessible guest pages
- Admin editor with validation, live preview, revision checks, and atomic publishing
- Server-rendered Go application with no frontend build step
- Embedded templates and static assets in a single binary

## Quick start

You need Go 1.27.1 or newer. Node.js is only required to run the JavaScript tests.

Start the development server with automatic reload:

```bash
make dev
```

Open [http://localhost:8080/admin/](http://localhost:8080/admin/) and sign in with:

- Username: `admin`
- Password: `manna-local-development`

Choose **Add event**, enter its URL slug, and configure the menu in the editor. The public event link is available from the editor after the event is created.

The development password is for local use only. Override it when needed:

```bash
MANNA_ADMIN_PASSWORD="your-local-password-of-at-least-16-characters" make dev
```

Menu data and uploaded branding are written to the ignored `content` directory. They are not committed to the repository.

## Run without automatic reload

Enable the admin interface by providing a password and writable content directory:

```bash
MANNA_ADMIN_PASSWORD="your-password-of-at-least-16-characters" \
CONTENT_DIR=content \
go run .
```

To run the public server without the admin interface, omit the password:

```bash
CONTENT_DIR=content go run .
```

The server listens on port `8080` by default. Its health endpoint is [http://localhost:8080/healthz](http://localhost:8080/healthz).

## Docker Compose

Create the local deployment files and generate a strong admin password:

```bash
cp docker-compose.example.yaml docker-compose.yaml
cp .env.example .env
openssl rand -base64 32
```

Place the generated value in `.env`, then start Manna:

```bash
docker compose up --build -d
docker compose logs -f manna
```

The service is exposed on `127.0.0.1:8080`. Published content remains in the host `content` directory when the container is replaced or restarted. Stop the service with `docker compose down`.

Before using the admin interface in the Compose deployment, place Manna behind an HTTPS reverse proxy. Docker traffic is not treated as local, and the admin interface rejects plain HTTP. Configure the proxy to replace `X-Forwarded-Proto` and `X-Forwarded-For`, then set the proxy trust variables below to the proxy's exact address or CIDR. Do not expose the backend port directly to untrusted networks.

The container runs as UID `10001` and must be able to write to `content`. Keep the container non-root and grant that UID access to the directory if startup reports a permissions error.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | HTTP server port |
| `CONTENT_DIR` | embedded content | Directory containing runtime event data and branding |
| `MANNA_ADMIN_PASSWORD` | unset | Enables `/admin/`; requires at least 16 characters and a writable `CONTENT_DIR` |
| `MANNA_ADMIN_TRUST_PROXY_HTTPS` | `false` | Accept HTTPS forwarding information from trusted proxies |
| `MANNA_ADMIN_TRUSTED_PROXY_CIDRS` | unset | Comma-separated proxy CIDRs; required when proxy HTTPS trust is enabled |

When proxy trust is enabled, list only proxies that overwrite forwarding headers. Avoid broad ranges such as `0.0.0.0/0`.

## How content works

Each event has a public URL and its own menu. Use the admin interface to:

- create and remove events;
- set languages, event details, dates, and branding;
- manage meals, food trucks, drinks, coffee, and snacks;
- publish prices, sold-out states, payment notes, and food declarations;
- preview, validate, and publish changes.

Manna checks external content for updates while it is running. Invalid changes do not replace the last valid menu. Concurrent edits are protected by revision checks, and publishing replaces files atomically.

Event URLs are public. The root page does not list them, but anyone with an event URL can open it.

## Development

Run the full test suite:

```bash
make test
```

Run individual test suites or build the binary:

```bash
go test ./...
node --test tests/*.test.cjs
make build
```

The application uses Go's HTTP server and HTML templates plus framework-free JavaScript and CSS. Application dependencies are defined in `go.mod`; the automatic-reload tool is kept separately in `dev.mod`.

## License

Copyright © 2026 Kavod'or.

Licensed under [AGPL-3.0](LICENSE).
