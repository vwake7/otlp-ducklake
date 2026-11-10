FROM debian:bookworm-slim
ARG DUCKDB_VERSION=2.0.0
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && arch="$(dpkg --print-architecture)" \
    && case "$arch" in amd64) darch=amd64 ;; arm64) darch=arm64 ;; *) echo "unsupported $arch" >&2; exit 1 ;; esac \
    && curl -fsSL -o /tmp/duckdb.tar.gz "https://github.com/duckdb/duckdb/releases/download/v${DUCKDB_VERSION}/duckdb-cli-linux-${darch}.tar.gz" \
    && tar -tzf /tmp/duckdb.tar.gz \
    && tar -xzf /tmp/duckdb.tar.gz -C /usr/local/bin \
    && rm /tmp/duckdb.tar.gz \
    && apt-get purge -y curl \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* \
    && command -v duckdb
COPY quack/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 9494
ENTRYPOINT ["/entrypoint.sh"]
