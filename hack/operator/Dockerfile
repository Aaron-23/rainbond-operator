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
COPY version version

ARG GOARCH
ARG LDFLAGS
RUN go build -ldflags "$LDFLAGS" -o /opeator $PWD/cmd/manager

FROM alpine:3.11.2
RUN apk add --update tzdata && mkdir /app
ENV TZ=Asia/Shanghai
COPY --from=builder opeator /app

ENTRYPOINT ["/app/opeator"]