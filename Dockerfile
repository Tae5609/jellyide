FROM golang:alpine AS backend
WORKDIR /app
COPY backend/go.mod .
RUN go mod download
COPY backend/ .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o server .

FROM node:20-alpine AS frontend
WORKDIR /app
COPY frontend/package.json .
RUN npm install
COPY frontend/ .
RUN npm run build

FROM alpine:latest
# The pip yt-dlp stays as the system fallback: with YTDLP_MANAGED on (the
# default) JellyIDE installs and auto-updates its own yt-dlp zipapp under
# <DOWNLOAD_DIR>/.jellyide/bin, running it with python3 from this line.
RUN apk add --no-cache ffmpeg python3 py3-pip nodejs npm     && pip3 install yt-dlp mutagen --break-system-packages
WORKDIR /app
COPY --from=backend /app/server .
COPY --from=backend /app/update_metadata.py .
COPY --from=frontend /app/build ./frontend/build
ENV PORT=8080
ENV DOWNLOAD_DIR=/mnt/media/music
ENV CONCURRENT_DOWNLOAD=2
EXPOSE 8080
CMD ["./server"]
