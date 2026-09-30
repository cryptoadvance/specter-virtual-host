FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates libgtk-3-0 libwebkit2gtk-4.1-0 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home --home-dir /data --shell /usr/sbin/nologin specter \
    && mkdir -p /data \
    && chown specter:specter /data

COPY build/bin/specter-virtual-host /usr/local/bin/specter-virtual-host
RUN chmod 0755 /usr/local/bin/specter-virtual-host

ENV SPECTER_VIRTUAL_HOST_CONFIG=/data/config.json \
    SPECTER_VIRTUAL_HOST_WEB_ADDRESS=0.0.0.0:8788 \
    SPECTER_VIRTUAL_HOST_HWI_ADDRESS=0.0.0.0:8789

USER specter
EXPOSE 8788 8789
ENTRYPOINT ["/usr/local/bin/specter-virtual-host"]
CMD ["serve", "--headless", "--web-address", "0.0.0.0:8788", "--hwi-address", "0.0.0.0:8789"]
