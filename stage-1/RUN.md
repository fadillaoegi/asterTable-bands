# Tablekeeper Stage 1

Build and start the complete service (including its private PostgreSQL 18 server) with one command:

```sh
docker build -t tablekeeper-stage1 . && docker run --rm -e PORT=8080 -p 8080:8080 tablekeeper-stage1
```

The API listens on `0.0.0.0:$PORT` (`8080` by default). No volume, external database, Compose file, or runtime outbound access is required. Readiness is exposed by `GET /health`. The same image also starts successfully under `--network none` when exercised from inside its network namespace.
