# Project conventions

- Go standard-library `net/http` server in `main.go`, templ components in `views.templ`, local HTMX from npm, and SQLite via `modernc.org/sqlite`.
- Edit `views.templ`, not the generated `views_templ.go`. Generate components with the pinned `go tool templ` tool.
- Keep dependencies locked with Go modules and npm.
- This is a single-user local app without authentication. Keep the server bound to localhost by default.
- SQLite data and the session signing key live in the ignored `instance/` directory. Never commit them or modify real task data during checks.
- Preserve signed CSRF cookies, parameterized SQL, template escaping, and the self-only Content Security Policy.
- Prioritize implementation and quick smoke checks. Do not spend significant time adding tests unless requested.

# Commands

- Install dependencies: `go mod download && npm ci`
- Generate templates: `npm run generate`
- Start locally: `npm start` (http://127.0.0.1:5050)
- Build: `npm run build`
- Run the built server from the app directory: `./daylist`
- Quick checks: `npm run check`
- Override the listen address with `ADDR` or the SQLite path with `DATABASE`.
- Optional `SECRET_KEY` overrides the persisted signing key and must be at least 32 bytes.
- Serve assets from the app directory; only the three explicitly registered asset paths are exposed.
