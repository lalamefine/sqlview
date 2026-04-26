FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod .
COPY main.go .
RUN go mod download
RUN CGO_ENABLED=0 go build -o /app/sqlview .

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/sqlview /usr/local/bin/sqlview
WORKDIR /app
EXPOSE 8080
ENV ADDR=":8080"
ENV SQL_DIR="/queries"
CMD ["/usr/local/bin/sqlview"]
