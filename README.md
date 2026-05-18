# Private Tracker Vault

A self-hosted dashboard for managing private tracker credentials, with optional sync to Prowlarr and Autobrr.

## Features

- Manage multiple UNIT3D-based private tracker accounts in one place
- Per-tracker stats: ratio, buffer, seed bonus, upload/download totals
- IP allowlist (CIDR-based) with optional reverse-proxy support
- Prowlarr integration: import existing indexers, push credentials, sync state
- Autobrr integration: push tracker credentials, view per-tracker IRC connection status
- Argon2id + ChaCha20-Poly1305 encrypted config at rest
- Single-binary, minimal dependencies

## Quick Start

```bash
docker run -d \
  -p 8008:8008 \
  -v ptv-config:/config \
  ghcr.io/nerney/ptv:latest
```

Or with Docker Compose:

```bash
docker compose up -d
```

Then open `http://localhost:8008` and follow the setup prompts.

## Configuration

All config is stored under the `/config` volume mount:

| File | Purpose |
|---|---|
| `config.enc` | Encrypted dashboard config (trackers, Prowlarr, Autobrr credentials) |
| `netacl.json` | Plaintext IP allowlist — loaded before login to gate access |

The network config (IP allowlist + proxy host) is stored in plaintext so the app can enforce it on the login page itself, before any password is entered.

## Building

```bash
go build -o ptv .
./ptv
```

Requires Go 1.25+.
