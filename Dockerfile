FROM golang:1.26-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends libmediainfo-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go run github.com/a-h/templ/cmd/templ@v0.3.1100 generate
RUN CGO_ENABLED=1 go build -o /brakarr ./cmd

# Ubuntu's ffmpeg is built without libvmaf, which the quality check needs. BtbN's GPL build
# includes libvmaf plus VAAPI/QSV (it loads libva from the system at runtime) and NVENC. The shared
# variant is used so ffmpeg and ffprobe share one copy of the libraries.
FROM alpine:3.20 AS ffmpeg
RUN mkdir /ffmpeg && wget -qO- https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-n8.1-latest-linux64-gpl-shared-8.1.tar.xz \
    | tar -xJ --strip-components=1 -C /ffmpeg \
    && rm -rf /ffmpeg/include /ffmpeg/doc /ffmpeg/man /ffmpeg/bin/ffplay /ffmpeg/lib/pkgconfig

FROM ubuntu:26.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    libmediainfo0v5 mesa-va-drivers intel-media-va-driver-non-free libva2 libva-drm2 ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=ffmpeg /ffmpeg /opt/ffmpeg
ENV PATH=/opt/ffmpeg/bin:$PATH LD_LIBRARY_PATH=/opt/ffmpeg/lib
# For NVIDIA GPUs: asks the NVIDIA Container Toolkit to mount the driver's encode and decode
# libraries, which it leaves out by default. It has no effect on other GPUs.
ENV NVIDIA_DRIVER_CAPABILITIES=compute,video,utility
COPY --from=build /brakarr /usr/local/bin/brakarr
WORKDIR /config
EXPOSE 1323
ENTRYPOINT ["brakarr"]
# Settings are edited on the Settings page and stored in /config/config.json.
CMD ["-config", "/config/config.json", "-listen", "0.0.0.0:1323"]
