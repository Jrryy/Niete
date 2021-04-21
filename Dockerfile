FROM golang:1.13-alpine

RUN apk add git

WORKDIR /go/src/app

COPY . .

RUN go get -d -v ./...
RUN go get github.com/githubnemo/CompileDaemon
RUN go install -v ./...

ENTRYPOINT CompileDaemon --build="go install -v ./..." --command=niete