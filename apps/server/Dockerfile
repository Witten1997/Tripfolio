# 多阶段构建：编译 api、worker、migrate 三个入口，运行镜像只含二进制、证书与时区数据。
# 大陆构建时传入 --build-arg GOPROXY=https://goproxy.cn,direct。
FROM golang:1.27-alpine AS build
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0 GOFLAGS=-trimpath
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o /out/api ./cmd/api \
 && go build -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && go build -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S app && adduser -S -G app -u 10001 app
COPY --from=build /out/api /out/worker /out/migrate /app/
USER app
WORKDIR /app
ENV TRIPFOLIO_HTTP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/app/api"]
