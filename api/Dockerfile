FROM golang:1.25.0-alpine

RUN apk update && apk add git

RUN mkdir /go/src/api

WORKDIR /go/src/api

ADD https://github.com/golang/go/raw/master/lib/time/zoneinfo.zip /usr/local/go/lib/time/zoneinfo.zip

ADD . /go/src/api

RUN go install github.com/air-verse/air@latest

RUN touch .air.toml

EXPOSE 8080

CMD [ "air","-c",".air.toml" ]