FROM golang:1.26-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends libmediainfo-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go run github.com/a-h/templ/cmd/templ@v0.3.1100 generate
RUN CGO_ENABLED=1 go build -o /brakarr ./cmd

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg libmediainfo0v5 mesa-va-drivers intel-media-va-driver ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /brakarr /usr/local/bin/brakarr
ENV WORK_DIR=/config/work DB_PATH=/config/app.db LISTEN_ADDR=0.0.0.0:1323
WORKDIR /config
EXPOSE 1323
ENTRYPOINT ["brakarr"]
