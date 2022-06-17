FROM golang:1.13-alpine

RUN apk add git chromium

WORKDIR /go/src/app

COPY . .

RUN go get -v ./...
RUN go install -v ./...

ENTRYPOINT ["niete"]