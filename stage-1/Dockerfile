FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/tablekeeper ./cmd/tablekeeper

FROM postgres:18
ENV PORT=8080 \
    PGDATA=/var/lib/postgresql/data/tablekeeper \
    PGHOST=/var/run/postgresql \
    PGDATABASE=tablekeeper \
    PGUSER=postgres
COPY --from=build /out/tablekeeper /app/tablekeeper
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod 0755 /app/docker-entrypoint.sh && mkdir -p /app /var/run/postgresql && chown -R postgres:postgres /var/run/postgresql /var/lib/postgresql
EXPOSE 8080
ENTRYPOINT ["/app/docker-entrypoint.sh"]
