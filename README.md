# Stockyard Gate

Auth proxy. Add login, API keys, and rate limiting to any internal tool with zero code changes.

## What it does

Gate sits in front of any web application or API and adds authentication. Your upstream app doesn't need to change. Gate handles login, API key validation, rate limiting, and access logging before forwarding requests.

Built for self-hosters who run tools that ship without auth (Grafana, Jupyter, internal dashboards, admin panels).

## Features

- **API key auth** — issue keys, validate on every request, revoke instantly
- **Basic login** — username/password with session cookies
- **Rate limiting** — per-IP and per-key request limits
- **Access logging** — every request logged with user, IP, path, timestamp
- **Upstream forwarding** — transparent reverse proxy to your app
- **Path rules** — public paths, auth-required paths, admin-only paths
- **Single binary** — Go + embedded SQLite, no external dependencies
- **Self-hosted** — auth data never leaves your infrastructure

## Quick start

```bash
curl -fsSL https://stockyard.dev/gate/install.sh | sh
gate serve --upstream http://localhost:3000 --port 8780
```

## Configuration

```yaml
upstream: http://localhost:3000
port: 8780
auth:
  require: true
  api_keys: true
  sessions: true
rules:
  - path: /health
    public: true
  - path: /admin/*
    role: admin
rate_limit:
  requests_per_minute: 60
```

## Pricing

- **Free:** 1 upstream, 5 users, basic auth
- **Pro ($9/mo):** Unlimited upstreams, unlimited users, rate limiting, access logs, OIDC/SSO

## Part of Stockyard

Gate reuses the auth and rate limiting engine from [Stockyard](https://stockyard.dev), the self-hosted LLM infrastructure platform.

## License

Apache 2.0
