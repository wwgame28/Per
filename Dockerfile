FROM golang:1.25.1-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN go test -timeout 60s ./... && CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/shtab .

FROM ubuntu:24.04 AS llama-build
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip && rm -rf /var/lib/apt/lists/*
RUN curl -fL --retry 3 https://github.com/ggml-org/llama.cpp/releases/download/b6650/llama-b6650-bin-ubuntu-x64.zip -o /tmp/llama.zip \
 && echo '24b65542d6015c688e117d555ac76646229ddb682c8ac2257d0c955e772b0046  /tmp/llama.zip' | sha256sum -c - \
 && unzip -q /tmp/llama.zip -d /opt/llama

FROM ubuntu:24.04
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl libcurl4t64 libgomp1 tzdata \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=go-build /out/shtab /app/shtab
COPY --from=llama-build /opt/llama/build/bin/ /opt/llama/
COPY start.sh /app/start.sh
RUN chmod 755 /app/start.sh && mkdir -p /data/assets
ENV PORT=8080 DB_PATH=/data/shtab.sqlite3 TZ=Asia/Yakutsk \
    LLAMA_URL=http://127.0.0.1:8081 MODEL_PATH=/data/model.gguf \
    GOMEMLIMIT=96MiB GOMAXPROCS=1 MALLOC_ARENA_MAX=2 PARALLEL=1
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD curl -fsS http://127.0.0.1:8080/healthz || exit 1
CMD ["/app/start.sh"]
