#!/bin/sh
set -eu
if [ -z "${QUACK_TOKEN:-}" ]; then
    echo "QUACK_TOKEN is empty" >&2
    exit 1
fi
token=$(printf '%s' "$QUACK_TOKEN" | sed "s/'/''/g")
port="${QUACK_PORT:-9494}"
sql="INSTALL quack; LOAD quack; CALL quack_serve('quack:0.0.0.0:${port}', token => '${token}', allow_other_hostname => true, disable_ssl => true);"
fifo=/tmp/quack-hold
log=/tmp/quack.log
rm -f "$fifo" /tmp/quack-ready
mkfifo "$fifo"
sleep infinity > "$fifo" &
hold=$!
duckdb -cmd "$sql" /data/catalog.duckdb < "$fifo" > "$log" 2>&1 &
pid=$!
trap 'kill "$pid" "$hold" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; exit 0' TERM INT
i=0
while [ "$i" -lt 90 ]; do
    if grep -q listen_url "$log" 2>/dev/null; then
        touch /tmp/quack-ready
        cat "$log"
        wait "$pid"
        exit $?
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
        cat "$log" >&2
        exit 1
    fi
    i=$((i + 1))
    sleep 1
done
echo "quack did not start" >&2
cat "$log" >&2
exit 1
