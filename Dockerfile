# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download

COPY src/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /seek .

FROM alpine:3.21

# CA bundle for HTTPS calls to search/fetch/crawl providers.
RUN apk add --no-cache ca-certificates \
	&& adduser -D -u 65532 seek

COPY --from=build /seek /usr/local/bin/seek

USER seek
WORKDIR /home/seek

EXPOSE 8787

ENV SEEK_SERVE_MAX_CONCURRENT=50

# Bind all interfaces so the port is reachable from outside the container.
# Set SEEK_SERVE_TOKEN (or pass --token) before exposing this publicly.
# Mount config at /home/seek/.seek or pass provider keys via env.
ENTRYPOINT ["seek"]
CMD ["serve", "--addr", "0.0.0.0:8787"]
