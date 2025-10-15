FROM golang:1.25-alpine

RUN apk add git chromium terminus-font ttf-inconsolata ttf-dejavu font-noto font-noto-cjk ttf-font-awesome font-noto-extra
RUN fc-cache -fv

WORKDIR /go/src/app

ADD . .

RUN go get -v ./...
RUN go install -v ./...

ENTRYPOINT ["niete"]