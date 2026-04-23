FROM golang:1.21-alpine AS builder
WORKDIR /app

ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/mc-proxy .

FROM scratch
WORKDIR /app
COPY --from=builder /out/mc-proxy /app/mc-proxy
COPY config.yaml /app/config.yaml
ENTRYPOINT ["/app/mc-proxy", "-config", "/app/config.yaml"]
