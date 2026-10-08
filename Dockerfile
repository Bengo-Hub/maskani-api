# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN GOTOOLCHAIN=auto go mod download
COPY . .
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maskani-api ./cmd/api
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maskani-migrate ./cmd/migrate
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maskani-seed ./cmd/seed
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maskani-seed-tenant ./cmd/seed-tenant

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /out/maskani-api /usr/local/bin/maskani-api
COPY --from=builder /out/maskani-migrate /usr/local/bin/maskani-migrate
COPY --from=builder /out/maskani-seed /usr/local/bin/maskani-seed
COPY --from=builder /out/maskani-seed-tenant /usr/local/bin/maskani-seed-tenant
COPY scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
# /app/seed-tenant is the documented path for: kubectl exec ... -- /app/seed-tenant --tenant <slug>
RUN chmod +x /usr/local/bin/entrypoint.sh && mkdir -p /app/media && chown app:app /app/media && \
    ln -s /usr/local/bin/maskani-seed-tenant /app/seed-tenant
USER app
ENV TZ=Africa/Nairobi HTTP_PORT=4000 MEDIA_ROOT=/app/media
EXPOSE 4000
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
