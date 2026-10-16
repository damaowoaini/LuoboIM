ARG GO_IMAGE=golang:1.27.2-alpine
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /route-service .
FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && addgroup -S route && adduser -S -G route route
WORKDIR /app
COPY --from=build /route-service ./route-service
USER route
EXPOSE 10010
ENTRYPOINT ["/app/route-service"]
