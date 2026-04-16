# ---- build stage ----
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /web-terminal .

# ---- runtime stage ----
FROM alpine:3.21

RUN apk add --no-cache bash curl git openssh-client

COPY --from=builder /web-terminal /usr/local/bin/web-terminal

# Container must listen on all interfaces
ENV WEB_TERMINAL_BIND=0.0.0.0
ENV WEB_TERMINAL_PORT=7681

EXPOSE 7681

ENTRYPOINT ["web-terminal"]
