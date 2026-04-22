# Этап 1: Сборка
FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Собираем статически слинкованный бинарник без CGO
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o api-gateway main.go

# Этап 2: Релизный образ
FROM alpine:latest

# Устанавливаем часовые пояса
RUN apk --no-cache add tzdata
ENV TZ=Europe/Moscow

WORKDIR /root/

COPY --from=builder /app/api-gateway .

# Понижаем привилегии для безопасности
USER nobody:nobody

# Порт
EXPOSE 8080

# Healthcheck через наш эндпоинт
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget -qO- http://localhost:8080/api/health || exit 1

# Запуск
CMD ["./api-gateway"]