FROM golang:1.13 as builder
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOPATH=/go

WORKDIR /go/src/github.com/goodrain/rainbond-operator
COPY go.mod .
COPY go.sum .
ENV GOPROXY=https://goproxy.io
RUN go mod download

COPY pkg pkg
COPY cmd cmd

ARG GOARCH
ARG LDFLAGS
RUN go build -ldflags "$LDFLAGS" -o /openapi $PWD/cmd/openapi

FROM registry.cn-hangzhou.aliyuncs.com/goodrain/rbd-op-ui-base:v0.0.1
RUN apk add --update tzdata
ENV TZ=Asia/Shanghai
COPY --from=builder openapi /app
EXPOSE 8080

ENTRYPOINT ["/app/openapi"]