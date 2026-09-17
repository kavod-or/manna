# Manna

Manna is a small, responsive conference catering guide. It supports multiple events, each with its own menu URL, scheduled meals, food trucks, coffee, drinks, snacks, and configurable languages.

## Features

- responsive and accessible design without a frontend framework
- server-rendered HTML
- configurable language switch that matches the browser language and otherwise defaults to the first configured language
- separate event URLs mapped to menu files in an ignored runtime `content/events.yaml`, with `content/events.example.yaml` as the tracked template
- YAML as the single source of truth for menu content
- templates and assets embedded in one Go binary
- gzip compression and long-lived browser caching for slow or crowded Wi-Fi
- health endpoint and secure HTTP headers
- multi-stage Docker image running as a non-root user
- hardened Docker Compose configuration with a read-only container filesystem

## Quick start

### Local development, including the admin editor

Go 1.26.8 or newer is required. Start the development server with automatic reload:

```bash
make dev
```

Then open:

- event menu: [http://localhost:8080/example-conference](http://localhost:8080/example-conference)
- admin editor: [http://localhost:8080/admin/](http://localhost:8080/admin/)
- health check: [http://localhost:8080/healthz](http://localhost:8080/healthz)

Sign in to the editor with username `admin` and password `manna-local-development`. This predictable credential is only supplied by the local `make dev` command. Set `MANNA_ADMIN_PASSWORD` before running it to override the development password. `make dev` also sets `CONTENT_DIR=content`, so validated changes published in the editor update the files in this checkout.

To run without the editor, leave `MANNA_ADMIN_PASSWORD` unset:

```bash
CONTENT_DIR=content go run .
```

### Docker Compose

Copy the provided Compose and environment examples, generate a unique password of at least 16 characters, and paste it after `MANNA_ADMIN_PASSWORD=` in `.env`:

```bash
cp docker-compose.example.yaml docker-compose.yaml
cp .env.example .env
cp content/events.example.yaml content/events.yaml
openssl rand -base64 32
docker compose up --build -d
docker compose logs -f manna
```

Press `Ctrl+C` to leave the log view; the detached container keeps running. The event menu is now available at [http://localhost:8080/example-conference](http://localhost:8080/example-conference). Compose deliberately exposes Manna only on the host loopback interface. The admin editor also rejects plain HTTP received through Docker, so configure HTTPS before using `/admin/` in this setup; a complete reverse-proxy example is below.

Stop the deployment with `docker compose down`. Published menu files remain in the host's `content` directory.

## Edit the menu

Each event has its own menu file, selected by `content/events.yaml`. Copy the tracked `content/events.example.yaml` template to that ignored runtime path before customizing event routes. If the runtime file is absent, Manna falls back to the template. The example conference uses `content/menu.yaml`, and the community event uses `content/community-day.yaml`.

Configure lowercase language codes under `conference.languages`, in display and fallback order. If omitted, Manna uses `[de, en]`. Every translated name, description, title, subtitle, tag, and configured payment notice must contain all configured languages:

```yaml
conference:
  languages: [de, en, ru]
  name:
    de: Beispiel Konferenz
    en: Example Conference
    ru: Пример конференции

# The same language keys are used throughout the menu, for example in tags:
tags:
  vegetarian:
    de: Vegetarisch
    en: Vegetarian
    ru: Вегетарианское
```

Language codes may include subtags such as `pt-br`. Manna includes interface labels for German, English, and Russian; other languages fall back to English, while menu content is always required in every configured language. Prices use localized formatting for German, Spanish, French, Italian, Dutch, Portuguese, and Russian, and English-style formatting for other languages.

### Minimal complete event example

First copy the template when no runtime manifest exists, then map the public URL in `content/events.yaml`:

```bash
cp content/events.example.yaml content/events.yaml
```

The runtime file is ignored by Git, so server-specific routes do not dirty the checkout:

```yaml
events:
  - path: /team-day
    menu: team-day.yaml
```

Then create `content/team-day.yaml`:

```yaml
conference:
  currency: euro
  languages: [de, en]
  timezone: Europe/Berlin
  name: {de: Teamtag, en: Team Day}
  location: {de: Hauptfoyer, en: Main lobby}

tags:
  vegetarian: {de: Vegetarisch, en: Vegetarian}

permanent:
  coffee: []
  drinks:
    - id: water
      name: {de: Wasser, en: Water}
      price: 2.50
  snacks: []

days:
  - date: "2026-10-12"
    services:
      - id: lunch
        title: {de: Mittagessen, en: Lunch}
        subtitle: {de: Buffet im Foyer, en: Buffet in the lobby}
        from: "12:30"
        until: "14:00"
        items:
          - id: vegetable_curry
            name: {de: Gemüse-Curry, en: Vegetable curry}
            description: {de: Mit Basmatireis, en: With basmati rice}
            price: 8.50
            tags: [vegetarian]
```

Replace the date and text, then open `/team-day`. The online admin editor intentionally cannot make the first `events.yaml` change; an operator must create the event mapping and initial file. After that, the editor can update `team-day.yaml`. See the tracked [`content/events.example.yaml`](content/events.example.yaml) manifest template and bundled [`content/menu.yaml`](content/menu.yaml) and [`content/community-day.yaml`](content/community-day.yaml) menus for complete examples.

Docker Compose bind-mounts only the `content` directory as writable so the admin editor can publish atomic file replacements, then overlays the host file selected by `MANNA_EVENTS_FILE` as read-only `events.yaml`. The copied `.env.example` selects the ignored runtime manifest; without that setting, Compose uses the tracked template. Neither the editor nor the application process can change the mounted manifest. The container root filesystem remains read-only and the application still runs as non-root user `10001`, with all Linux capabilities dropped and `no-new-privileges`. Manna checks for updates at most twice per second, so no restart or rebuild is required after publishing.

Without `CONTENT_DIR`, the application uses the manifest template and menus embedded at build time.

## Admin editor

The optional editor can change the YAML of existing events. It cannot add, remove, rename, or remap events because `events.yaml` is not exposed to the editor and is mounted read-only by Compose.

### Publishing a menu

1. Open `/admin/` and sign in with username `admin`.
2. Select an existing event. Manna loads its current YAML and revision.
3. Edit the YAML and choose **Validate**. Validation does not change the live menu.
4. Choose **Publish**. Manna validates again and atomically replaces that event's menu file.
5. Reload the public event URL to verify the result; no application restart is needed.

If the file changed after the editor loaded it, publishing returns a conflict instead of overwriting the newer version. Reload the current YAML, review it, and reapply the intended change.

### HTTPS reverse-proxy example

Admin credentials use HTTP Basic Authentication, so remote admin access requires HTTPS. The following nginx example terminates TLS, replaces both forwarding headers, and sends traffic to the loopback-only Compose port:

```nginx
server {
    listen 443 ssl;
    server_name menu.example.com;

    ssl_certificate     /etc/letsencrypt/live/menu.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/menu.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

Manna accepts these headers only from explicitly trusted proxy addresses. Do not rely on Compose's automatically allocated default network for this: its subnet and gateway can change when Docker recreates it.

For nginx installed on the Docker host, give Manna a dedicated network with a fixed subnet and gateway. First inspect the subnets already assigned by Docker:

```bash
docker network inspect $(docker network ls -q) \
  --format '{{.Name}}: {{range .IPAM.Config}}{{.Subnet}} {{end}}'
```

Choose a private subnet that does not overlap with those results, the host network, or any VPN. The following examples use `172.30.0.0/24`; change it everywhere if that range is already in use.

Add the network to the `manna` service in `docker-compose.yaml`:

```yaml
services:
  manna:
    # Keep the existing build, ports, environment, volumes, and hardening.
    networks:
      - manna_private
```

Then add this top-level section at the end of the same file:

```yaml
networks:
  manna_private:
    name: manna_private
    driver: bridge
    ipam:
      config:
        - subnet: 172.30.0.0/24
          gateway: 172.30.0.1
```

The explicit network name avoids project-name-dependent network names. The explicit IPAM settings keep the gateway stable when the network is removed and recreated. Configure Manna to trust only that gateway:

```dotenv
MANNA_ADMIN_PASSWORD=replace-with-your-generated-password
MANNA_ADMIN_TRUST_PROXY_HTTPS=true
MANNA_ADMIN_TRUSTED_PROXY_CIDRS=172.30.0.1/32
```

Recreate the service and verify the resulting network before exposing nginx:

```bash
docker compose down
docker compose up --build -d
docker network inspect manna_private \
  --format 'subnet={{(index .IPAM.Config 0).Subnet}} gateway={{(index .IPAM.Config 0).Gateway}}'
```

The output should be `subnet=172.30.0.0/24 gateway=172.30.0.1`. The nginx configuration above can now forward to `127.0.0.1:8080`, while Manna consistently sees the host-side proxy through the trusted gateway address.

Now use `https://menu.example.com/admin/`. The backend port remains reachable only from the host. The proxy must overwrite, not append, `X-Forwarded-Proto` and `X-Forwarded-For`; the nginx directives above do that. Add a proxy-level request limit for `/admin/` as defense in depth. Trust the single gateway address with `/32`, not the entire Docker subnet: another container attached to the network must not be able to impersonate the proxy.

If nginx runs in a container instead, assign that proxy container a fixed address on the dedicated network, trust that address with `/32`, remove Manna's published `ports`, and have nginx forward to `http://manna:8080`. This avoids the host gateway entirely and is the preferred fully containerized layout.

### Content permissions

The container runs as the non-root UID `10001`. It must be able to read `events.yaml` and create, rename, and remove menu files in `content`. At startup, Manna verifies this by creating and removing a temporary `.events.yaml.manna-*` file in that directory; this does not modify the read-only event manifest. A `permission denied` error for that temporary file means UID `10001` cannot write to the host directory.

Do not map the container to host UID `0` when deploying from a root-owned checkout. Keep the container non-root and grant UID `10001` access with filesystem ACLs instead; the files remain owned by root:

```bash
setfacl -R -m u:10001:rwX content
find content -type d -exec setfacl -m d:u:10001:rwx {} +
```

The Compose example uses `Z` on its bind mounts so Docker assigns a private container label on SELinux systems. Do not disable SELinux globally. If `setfacl` is unavailable and the container may own the files, use this simpler alternative:

```bash
chown -R 10001:10001 content
chmod 700 content
chmod 600 content/*
```

Verify the mounted permissions without starting the web server:

```bash
docker compose run --rm --entrypoint sh manna -c \
  'test -r /app/content/events.yaml && test -w /app/content && test -w /app/content/menu.yaml'
```

Verify that the container still runs as UID `10001` with `docker compose run --rm --entrypoint id manna`. Do not run Manna as root and do not make the directory world-writable.

### Backup example

Stop writes and make a timestamped copy before first enabling online editing or before a large menu change:

```bash
docker compose stop manna
cp -a content "content-backup-$(date +%Y%m%d-%H%M%S)"
docker compose start manna
```

Published files live in the host-mounted `content` directory, so they survive container replacement and restart.

### Security behavior

- `/admin/` exists only when `MANNA_ADMIN_PASSWORD` is set; the password must contain at least 16 characters and requires a writable external `CONTENT_DIR`.
- Plain HTTP is accepted only from a direct loopback peer for local development. Remote access must use HTTPS.
- Failed authentication is limited to five attempts per source address per minute. With a trusted proxy, the source is the single sanitized client IP supplied by that proxy.
- Admin responses are not cached. Secure responses include HTTP Strict Transport Security.
- Access logs include the complete request endpoint, including admin event slugs, plus the method, status, and duration. They never include query strings, request bodies, or headers such as `Authorization`, so Basic Authentication passwords are not logged. Treat the local logs as sensitive operational data.
- Menu paths stay rooted inside `CONTENT_DIR`; path traversal and escaping symlinks are rejected.
- Publishing validates the complete menu, rejects stale revisions, and does not expose internal paths or raw storage errors to the browser.

Keep `.env` out of source control. This repository ignores `.env` and `.env.*` while retaining the secret-free `.env.example`. To disable the editor in a non-Compose deployment, unset `MANNA_ADMIN_PASSWORD`. Compose intentionally requires the password; to run that deployment without an admin route, remove its `MANNA_ADMIN_PASSWORD` environment mapping and recreate the service.

## Event branding

To replace the Manna header logo for an event, place a PNG, JPEG, WebP, GIF, or AVIF image in the `content` directory and set its relative path under `conference.logo` in that event's menu:

```yaml
conference:
  logo: example-conference-logo.png
  name: {de: Beispiel Konferenz, en: Example Conference}
  # Keep the existing location and timezone fields.
```

The image is served only through that event's branding URL. Paths must remain inside `content`; absolute paths, traversal, escaping symlinks, non-image data, and files larger than 5 MB are rejected. Omit `logo` to use the built-in Manna logo. With `CONTENT_DIR`, replacing the image or changing the setting takes effect on reload without rebuilding the application.

## Optional event effects

Choose the hidden experience for each event with `conference.easter_egg_mode`:

```yaml
conference:
  serious_mode: false
  easter_egg_mode: mazel_tov
```

The supported modes are `girly_vibes` and `mazel_tov`.

Set `serious_mode: true` to disable the selected easter egg entirely. When `easter_egg_mode` is omitted, Manna uses `girly_vibes` for backward compatibility. Changes made through `CONTENT_DIR` take effect when the event page is reloaded.

## Local development

Go 1.26.8 or newer is required. Run directly with embedded content:

```bash
go mod download
go run .
```

Use `make dev` for automatic reload as shown in the quick start. Run all Go and browser-side JavaScript tests, then create a local binary:

```bash
make test
go build -o bin/manna .
```

The JavaScript tests require Node.js with `node --test` support.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP port used by the web server |
| `CONTENT_DIR` | empty | Optional directory containing event menus and either runtime `events.yaml` or fallback `events.example.yaml` |
| `MANNA_ADMIN_PASSWORD` | empty | Enables `/admin/` with the fixed username `admin`; must contain at least 16 characters and requires writable external content |
| `MANNA_ADMIN_TRUST_PROXY_HTTPS` | `false` | Allow HTTPS forwarding headers only from explicitly trusted proxy CIDRs |
| `MANNA_ADMIN_TRUSTED_PROXY_CIDRS` | empty | Comma-separated proxy CIDRs required when proxy HTTPS trust is enabled, for example `127.0.0.1/32,::1/128` |

### Configuration examples

Public server with the menu files from `content`, but no admin editor:

```bash
CONTENT_DIR=content go run .
```

Local development server with a generated admin password:

```bash
export MANNA_ADMIN_PASSWORD="$(openssl rand -base64 32)"
CONTENT_DIR=content go run .
```

Custom port with external content:

```bash
PORT=9090 CONTENT_DIR=/srv/manna/content ./manna
```

Complete `.env` example for the fixed `manna_private` network shown above:

```dotenv
MANNA_ADMIN_PASSWORD=use-a-unique-random-password-of-at-least-16-characters
MANNA_ADMIN_TRUST_PROXY_HTTPS=true
MANNA_ADMIN_TRUSTED_PROXY_CIDRS=172.30.0.1/32
```

If you deliberately operate multiple trusted proxies, their individual addresses are comma-separated:

```dotenv
MANNA_ADMIN_TRUSTED_PROXY_CIDRS=10.20.0.5/32,10.20.0.6/32
```

Do not use a broad network such as `0.0.0.0/0`. Only a proxy that overwrites the forwarding headers should be trusted.

## Optional prices

Add `price` to an individual meal item or a permanent coffee/drink/snack in the event's menu file. Services such as breakfast, lunch, and dinner provide the title and short description; their items carry the prices:

```yaml
# Within days[].services[]:
- id: lunch
  title: {de: Mittagessen, en: Lunch}
  subtitle: {de: Frisch zubereitet, en: Freshly prepared}
  items:
    - id: vegetable_curry
      price: 8.50
      name: {de: Gemüse-Curry, en: Vegetable curry}
```

Prices are amounts in the event currency, with a decimal point and at most two decimal places. Negative amounts and invalid values are rejected. Omit `price` or set it to `null` to hide it; `price: 0` explicitly displays zero.

The language switch localizes prices in the featured meal, full schedule, drinks, and snacks. External menu prices reload just like other menu content.

Coffee is configured separately under `permanent.coffee`, other drinks under `permanent.drinks`, and snacks under `permanent.snacks`. Coffee items support the same optional prices and translations. The Coffee section is hidden when its list is empty.

For optional size prices, replace an item's `price` with either or both size fields:

```yaml
- id: cappuccino
  name: {de: Cappuccino, en: Cappuccino}
  price_normal: 3.20
  price_large: 4.20
```

Size labels are translated into German, English, and Russian, with English as the fallback for other interface languages. Missing or null sizes are hidden, and zero is displayed. Do not combine a single `price` with size prices on the same item.

## Food trucks

Add an optional `food_trucks` list to each entry in `days`, alongside `services`:

```yaml
- date: "2026-10-12"
  food_trucks:
    - id: pita_stop
      name: {de: Pita-Pause, en: The Pita Stop}
      description: {de: Frische Pita und Falafel., en: Fresh pita and falafel.}
      location: {de: Innenhof, en: Courtyard}
      times:
        - from: "11:30"
          until: "15:00"
        - from: "17:00"
          until: "20:30"
  services:
    # Existing conference meals go here.
```

Add as many trucks as needed per day, using unique IDs within that day. Names, descriptions, and locations require every configured language. A truck's `times` list may contain one or more serving windows. Times use `HH:MM`, with `until` later than `from` on the same day. The legacy single `from`/`until` pair remains supported, but must not be combined with `times`. Trucks appear in a separate section for the selected day. Omit `food_trucks` or use `food_trucks: []` to hide that day's section.

## Sold out

Add `sold_out: true` to any service (whole meal) or item, including coffee, drinks, and snacks:

```yaml
- id: cappuccino
  name: {de: Cappuccino, en: Cappuccino}
  price: 3.20
  sold_out: true
```

The item stays visible with a localized sold-out badge in place of its prices. Set `sold_out: false` or remove the field to restore normal display. A meal flag labels the whole service; individual item flags are independent. With `CONTENT_DIR` enabled, changes reload with the menu.

## Development with automatic reload

Run `make dev` to rebuild and restart when Go, HTML, CSS, JavaScript, or YAML changes. This uses Air from the separate `dev.mod` / `dev.sum` module files. Refresh the browser to see changes.

Air is development-only: Docker excludes its module files and local build outputs, builds only the application, and copies only the Manna binary into the final runtime image. The container starts Manna directly.

## Automatic day and featured meal

`conference.timezone` sets the conference clock (default: `Europe/Berlin`). The page selects today, the next configured day if today has no menu, or the final day after the conference. The featured meal is the currently running service, then the next upcoming service, or the final service once the day ends. Future days show their first meal; past days show their last. Overlapping services feature the one that started most recently. Sold-out services remain visible with their badge.

The clock updates every 15 seconds and when returning to the tab. Selecting a day or opening a topic link keeps that day selected until reload; its featured meal still updates. Without JavaScript, the first day and first meal remain the fallback.

## Multiple events

Copy `content/events.example.yaml` to the Git-ignored `content/events.yaml`, then map event paths to menu files there:

```yaml
events:
  - path: /example-conference
    menu: menu.yaml
  - path: /community-day
    menu: community-day.yaml
```

Each menu uses the same conference, days, food trucks, and refreshments format. Menu paths must be relative, end in `.yaml`, and cannot point to `events.yaml` or `events.example.yaml`. Set `conference.currency` in each menu to `euro` (€), `dollar` ($), or `schekel` (₪); it defaults to `euro` when omitted. Paths are unique lowercase slugs; `/`, `/healthz`, `/static`, and `/branding` are reserved. Generate each venue QR code for its full event URL. `/` displays only a centered German/English instruction to scan the venue QR code, and unknown paths return 404.

Event URLs are intentionally public and require no login or access token. Anyone who knows, guesses, or receives an event URL can open its menu directly. The QR code provides a convenient link; scanning it is not required for access. The root page does not list events.

`make dev` and Docker Compose use `CONTENT_DIR` to read external files. Manna prefers `events.yaml` and falls back to `events.example.yaml` when the runtime manifest is absent. Menu files and the selected manifest are checked for updates at most twice per second. Events can be added, removed, renamed, or pointed to a different menu without restarting the app. Invalid edits keep the last valid menus and route set online. `MENU_PATH` has been replaced by `CONTENT_DIR`.

## Payment information

Add an optional translated `payment` notice under `conference` in an event's menu:

```yaml
conference:
  payment:
    de: Nur Barzahlung
    en: Cash only
  # Keep the existing name, location, and timezone fields.
```

The notice appears below the event name, stays visible across days, and follows the language switch. Every configured language is required when the notice is present. Omit `payment` to hide it. With `CONTENT_DIR`, edit the menu and reload the page to see changes.

Each food truck can also have its own optional `payment` notice, shown inside its card:

```yaml
food_trucks:
  - id: pita_stop
    payment: {de: Nur Barzahlung, en: Cash only}
    # Keep the truck's name, description, location, and serving times.
```

Truck notices are independent of the conference notice; no payment method is inferred or inherited. Both example menus demonstrate cash-only, card payment, and cash-and-card trucks.

## Food truck menu items

Add optional `items` to a food truck to list dishes and prices inside its card:

```yaml
food_trucks:
  - id: pita_stop
    # Keep the truck's name, description, location, serving times, and payment.
    items:
      - id: falafel_pita
        name: {de: Falafel-Pita, en: Falafel pita}
        price: 7.50
        description: {de: Mit Hummus und Salat, en: With hummus and salad}
```

Each item requires an ID and a name in every configured language. Prices use the event's configured currency and the same localized formatting as other menu items: omit `price` to hide it, use `0` for free items, or use `price_normal` and `price_large` for sizes. `sold_out: true` shows the sold-out badge instead of prices. Descriptions are optional and require every configured translation when present. Omit `items` or use `items: []` to hide the list. Both example menus include priced food truck dishes.

## License

Copyright © 2026 Kavod'or.

Licensed under [AGPL-3.0](LICENSE).
