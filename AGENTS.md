# Repository guidance

## Project overview

Manna is a multilingual conference catering guide with multiple event URLs and an optional admin editor. It uses Go's HTTP server and HTML templates, vanilla JavaScript and CSS, and YAML menu content. Templates, static assets, and bundled content are embedded in the Go binary; there is no frontend package installation or bundling step.

## Layout

- `main.go`: startup, embedded assets, environment configuration, and shutdown.
- `events.go`: event manifest loading, routing, and external content access.
- `admin_content.go`: admin content reads, validation, revision checks, and atomic publishing.
- `internal/menu/`: menu types, strict YAML decoding, validation, prices, and reloadable stores.
- `internal/admin/`: shared admin content interfaces and errors.
- `internal/web/`: HTTP handlers, rendering, authentication, admin API, and asset delivery.
- `web/templates/` and `web/static/`: public and admin UI templates, scripts, styles, and images.
- `content/`: empty event manifest template plus ignored runtime menus and branding assets.
- `tests/*.test.cjs`: JavaScript tests using Node's built-in test runner and VM/DOM stubs.
- `README.md`: content schema, configuration, local development, and deployment documentation.

## Development commands

Run commands from the repository root. The module currently requires Go 1.27.1; JavaScript tests need Node.js with `node --test` support.

- `make test`: run all Go and JavaScript tests.
- `go test ./...`: run Go tests only.
- `node --test tests/*.test.cjs`: run JavaScript tests only.
- `make build`: build `bin/manna`.
- `make run`: run with embedded content by default.
- `CONTENT_DIR=content go run .`: use external content; leave `MANNA_ADMIN_PASSWORD` unset to disable the editor.
- `make dev`: run with automatic reload, external content, and the local admin editor at `/admin/` on port 8080 by default. This sets a development-only password unless overridden. Publishing through this editor changes files in `content/`.

The reload tool is managed separately through `dev.mod` and `dev.sum`. Keep development tool dependencies separate from the application module.

## Change conventions

- Follow surrounding code and run `gofmt` on changed Go files.
- Keep the frontend framework-free and preserve server-rendered, responsive, accessible behavior.
- Keep menu schema changes consistent across Go decoding/validation, the admin API and editor, public templates, examples, and README documentation where applicable.
- Respect configured language order and fallback behavior. Required translated content must cover every configured language; update interface translations when introducing UI labels.
- Add focused regression coverage for behavior changes. Go tests live beside their packages; JavaScript tests live in `tests/` and run without npm dependencies.
- Run relevant tests while iterating and `make test` for changes spanning backend and frontend. Report checks that could not run. Documentation-only changes do not require the application test suite.
- Templates and static assets are embedded at build time: rebuild or use `make dev` to see changes. External menu content reloads without a rebuild and retains the last valid menu when a reload fails.

## Content and security boundaries

- `content/events.yaml` is the tracked empty event manifest. Runtime event menus and branding are ignored. Do not commit deployment event data, `.env` files, or credentials.
- Event creation and removal may update the runtime manifest only through the dedicated admin boundary. Derive filenames from validated route slugs, require exact destructive confirmation and manifest revisions, and never expose arbitrary manifest paths to clients.
- Preserve strict menu validation, revision conflict checks, atomic file replacement, and restrictions on filesystem paths and uploaded assets.
- Preserve admin authentication, HTTPS requirements for remote access, and explicit trusted-proxy configuration. Never use the development password in deployment examples as a production credential.
- Use temporary directories and test fixtures for tests that publish content; avoid changing real event menus as a testing side effect.
- Inspect the working tree before editing and preserve unrelated user changes, including event-specific content and branding assets.
