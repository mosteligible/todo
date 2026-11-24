FROM node:22-bookworm-slim AS assets
WORKDIR /assets
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go tool templ generate \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/daylist .

FROM alpine:3.20
RUN addgroup -S daylist \
    && adduser -S -D -H -u 10001 -G daylist daylist \
    && mkdir -p /app/instance /app/node_modules/htmx.org/dist \
    && chown -R daylist:daylist /app
WORKDIR /app
COPY --from=build /out/daylist /app/daylist
COPY static /app/static
COPY --from=assets /assets/node_modules/htmx.org/dist/htmx.min.js \
    /app/node_modules/htmx.org/dist/htmx.min.js
USER daylist
ENV ADDR=0.0.0.0:5050 \
    DATABASE=/app/instance/todos.sqlite
VOLUME ["/app/instance"]
EXPOSE 5050
CMD ["./daylist"]
