# syntax=docker/dockerfile:1
#
# rtl_433 receiver image for the WxAlerts PWS Gateway.
#
# This builds the wxalerts fork of rtl_433 (https://github.com/wxalerts/rtl_433,
# branch wxalerts), which is upstream release 25.12 (merbanan/rtl_433 tag
# 25.12) plus the WxLink decoder for the owner's Stevenson screen node. The
# commit is pinned below. See docs/spec/wxlink-decoder.md for the fork policy.
#
# rtl_433 is GPL-2.0-or-later and librtlsdr is GPL-2.0. They run in this
# separate container and are never linked into the MIT licensed gateway (see
# NOTICE). Distributing this image is distributing a GPL binary, so the
# release workflow attaches the exact source tarball for RTL433_COMMIT and
# this Dockerfile (the build recipe) to every GitHub Release. Keep the two
# ARGs below in sync with each other: the workflow reads them with sed and
# verifies the tarball checksum before attaching it.
#
# Build from the deploy/ directory so the udev rules can be copied in:
#   docker build -f deploy/rtl433/Dockerfile -t pws-rtl433 deploy

# Commit on branch wxalerts of the fork, based on upstream tag 25.12.
ARG RTL433_COMMIT=7803968c9992f141606203b615f1bb10aeb055c1
ARG RTL433_SHA256=103fbc7904a044c926d5d987bb25825fb55802c05b50765cc58a4cf81a149db4

# -------------------------------------------------------------- build -------
FROM debian:bookworm-slim AS build
ARG RTL433_COMMIT
ARG RTL433_SHA256
RUN set -eu; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      build-essential ca-certificates cmake curl pkg-config \
      libusb-1.0-0-dev librtlsdr-dev libssl-dev; \
    rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN set -eu; \
    curl -fsSL -o rtl_433.tar.gz \
      "https://github.com/wxalerts/rtl_433/archive/${RTL433_COMMIT}.tar.gz"; \
    echo "${RTL433_SHA256}  rtl_433.tar.gz" | sha256sum -c -; \
    tar xzf rtl_433.tar.gz --strip-components=1; \
    rm rtl_433.tar.gz
# SoapySDR is off: the gateway supports RTL-SDR dongles only in v1. OpenSSL
# stays on so the HTTP and MQTT outputs can use TLS if ever needed.
RUN set -eu; \
    cmake -B build -DCMAKE_BUILD_TYPE=Release -DENABLE_SOAPYSDR=OFF -DENABLE_OPENSSL=ON; \
    cmake --build build -j"$(nproc)"; \
    DESTDIR=/out cmake --install build --strip

# ------------------------------------------------------------ runtime -------
FROM debian:bookworm-slim AS final
ARG RTL433_COMMIT
LABEL org.opencontainers.image.title="rtl_433 for WxAlerts PWS Gateway" \
      org.opencontainers.image.source="https://github.com/wxalerts/pws-gateway" \
      org.opencontainers.image.licenses="GPL-2.0-or-later" \
      org.opencontainers.image.version="25.12-wxalerts.${RTL433_COMMIT}"
# Runtime libraries only. The plugdev group (gid 46 on Debian and Raspberry
# Pi OS) matches the group the host udev rules give the dongle, so the
# container can open /dev/bus/usb without running as root.
RUN set -eu; \
    apt-get update; \
    apt-get install -y --no-install-recommends librtlsdr0 libusb-1.0-0 libssl3; \
    rm -rf /var/lib/apt/lists/*; \
    useradd --system --uid 10001 --no-create-home --shell /usr/sbin/nologin --groups plugdev rtl433
COPY --from=build /out/usr/local/bin/rtl_433 /usr/local/bin/rtl_433
COPY --from=build /src/COPYING /usr/share/doc/rtl_433/COPYING
# The same udev rules the bare-metal install uses, so an operator can extract
# them from the image: docker cp <container>:/usr/share/pwsgw/udev/ .
COPY udev/99-rtl-sdr.rules /usr/share/pwsgw/udev/99-rtl-sdr.rules
USER rtl433
# JSON event stream for the gateway over HTTP on 8433 (rtl_433 -F http).
EXPOSE 8433
ENTRYPOINT ["/usr/local/bin/rtl_433"]
# Defaults are overridden by compose; SI units and UTC timestamps match the
# gateway's internal conventions.
CMD ["-F", "http://0.0.0.0:8433", "-M", "time:utc", "-M", "protocol", "-M", "level", "-C", "si"]
