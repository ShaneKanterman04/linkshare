FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/linkshare .

FROM scratch AS artifact
COPY --from=build /out/linkshare /linkshare

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget && addgroup -S linkshare && adduser -S -G linkshare linkshare
WORKDIR /data
RUN chown linkshare:linkshare /data
COPY --from=build /out/linkshare /usr/local/bin/linkshare
USER linkshare
EXPOSE 8080
ENV LINKSHARE_ADDR=:8080 LINKSHARE_DB=/data/linkshare.db LINKSHARE_OWNER_NAME=Me
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/linkshare"]
