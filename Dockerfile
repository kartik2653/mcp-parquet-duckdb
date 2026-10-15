FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=1
RUN go build -o /out/analytics-mcp ./cmd/server

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 65532 --create-home --shell /usr/sbin/nologin app

WORKDIR /app
COPY --from=build /out/analytics-mcp /app/analytics-mcp
USER app
ENV HOME=/tmp
EXPOSE 8080
ENTRYPOINT ["/app/analytics-mcp"]
