FROM golang:alpine

WORKDIR /build

RUN apk add upx bash

COPY go.mod go.sum /build/

RUN go mod download

RUN go get github.com/githubnemo/CompileDaemon

COPY . .

CMD [ "sh", "-c", "CompileDaemon -command='/build/cloudprov-controller'" ]


