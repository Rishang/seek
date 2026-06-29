# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download

COPY src/ ./
RUN apk add --no-cache ca-certificates \
	&& CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /seek .

FROM busybox:1.36-glibc

COPY --from=build /etc/ssl/certs /etc/ssl/certs
COPY --from=build /seek /usr/local/bin/seek

WORKDIR /root

EXPOSE 8787

ENV SEEK_SERVE_MAX_CONCURRENT=50

# Bind all interfaces so the port is reachable from outside the container.
# Set SEEK_SERVE_TOKEN (or pass --token) before exposing this publicly.
# Mount config at /home/seek/.seek or pass provider keys via env.
ENTRYPOINT ["seek"]
CMD ["serve", "--addr", "0.0.0.0:8787"]
