FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY loadgen ./loadgen
ENV CGO_ENABLED=0
RUN go build -o /out/loadgen ./loadgen

FROM debian:bookworm-slim
COPY --from=build /out/loadgen /usr/local/bin/loadgen
USER nobody
ENTRYPOINT ["loadgen"]
