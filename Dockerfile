# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26.4

FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mca .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata gettext \
 && adduser -D -H -u 10001 -h /app app \
 && mkdir -p /app/data /app/storage /app/cache /app/config \
 && chown -R app:app /app
WORKDIR /app
COPY --from=builder /out/mca /app/mca
COPY docker-entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
EXPOSE 9178
USER app
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["/app/mca"]
