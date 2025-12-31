FROM golang:1.22-alpine AS build
WORKDIR /src
COPY . .
RUN go build -ldflags="-w -s" -o /out/agent ./main.go

FROM alpine:3.20
RUN apk add --no-cache docker-cli ca-certificates
COPY --from=build /out/agent /agent
ENTRYPOINT ["/agent"]
