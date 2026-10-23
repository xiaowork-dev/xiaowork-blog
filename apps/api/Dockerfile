FROM golang:1.27.3-alpine AS build
# Optional module proxy override (e.g. https://goproxy.cn,direct); go.sum still verifies every module.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/ ./cmd/blog-api ./cmd/blog-migrate

FROM alpine:3.22
# Fixed UID/GID so a host media directory can be chowned to 10001:10001 on Linux.
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 blog \
    && adduser -S -D -H -u 10001 -G blog blog \
    && mkdir -p /data/media \
    && chown blog:blog /data/media
COPY --from=build /out/blog-api /out/blog-migrate /usr/local/bin/
USER blog
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/blog-api"]
