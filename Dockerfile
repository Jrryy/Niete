FROM golang:1.25-alpine

RUN apk add git chromium terminus-font ttf-inconsolata ttf-dejavu font-noto font-noto-cjk ttf-font-awesome font-noto-extra
RUN fc-cache -fv

WORKDIR /go/src/app

COPY cmd/ cmd/
COPY go.mod go.mod
COPY go.sum go.sum

RUN go build -o /app/niete cmd/niete/main.go

ENTRYPOINT ["/app/niete"]
