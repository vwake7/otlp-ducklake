FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=1
RUN go build -o /out/logs ./logs \
    && go build -o /out/metrics ./metrics \
    && go build -o /out/query ./query \
    && go build -o /out/maintenance ./maintenance

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libstdc++6 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
