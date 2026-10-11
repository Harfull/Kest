# Docker

Images are published to `ghcr.io/harfull/kest` on every release tag (linux/amd64 and linux/arm64). The image is distroless, runs as a non-root user, and keeps state in `/data`.

## Compose

```bash
cp .env.example .env        # fill in the three required values
docker compose -f deploy/docker/compose.yml up -d
docker compose -f deploy/docker/compose.yml logs -f kest
```

Look for the `kest: ready` line.

## Plain `docker run`

```bash
docker run -d --name kest --restart unless-stopped \
  --env-file .env \
  -v kest-data:/data \
  ghcr.io/harfull/kest:latest
```

## Build locally

```bash
docker build -f deploy/docker/Dockerfile -t kest:dev .
```

## Notes

- No ports are exposed; Kest only makes outbound HTTPS connections.
- Stop with `docker stop`; Kest exits within 10 seconds on SIGTERM.
- The MCP plugin's stdio servers that need Node or Python are not available in the distroless image. Use remote (HTTP) MCP servers, or build a custom image on a base that has the runtime you need.
