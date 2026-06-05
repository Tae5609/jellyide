# JellyIDE

*"Download, Clean, Organize and Manage your Music Library"*

JellyIDE is a comprehensive, professional-grade music management platform designed for local music collectors and users of media servers like Jellyfin, Navidrome, and Plex.

What started as a simple "YT Scraper for Jellyfin" has evolved into a full-fledged Integrated Development Environment (IDE) for your music library.

## Background & Goals

Managing a large, locally hosted music library often involves juggling multiple disjointed tools: a downloader, a tag editor (like Mp3tag or Picard), and a file manager.

**The Goal:** JellyIDE unifies these workflows into a single, cohesive web application. It is designed to be the only tool you need to get music onto your server and ensure it is perfectly tagged and organized.

### Core Modules

1.  **Downloads:** Download audio directly from YouTube URLs or Playlists. Supports concurrent downloads, FLAC conversion, and Proxy routing for region-locked content.
2.  **Library:** Automatically scans your `/mnt/media/music` directory and indexes your collection into a persistent SQLite database. Browse your collection hierarchically by Artist and Album.
3.  **Metadata IDE:** A powerful, real-time metadata editor.
    *   **Smart Analyzer:** Automatically detects and suggests common, noisy patterns (like `[Official Audio]`) across an album for 1-click removal.
    *   **Bulk Operations:** Replace, Prefix, Suffix, or Regex edit titles across an entire album.
    *   **Deep Synchronization:** Applying changes updates the internal SQLite database, modifies the embedded tags (FLAC, MP3, M4A, OGG) using Mutagen, and physically renames the files and directories on disk to stay perfectly in sync.
4.  **Jobs:** A unified dashboard to monitor background tasks like downloading, metadata applying, and library scanning.

## Technical Implementation

JellyIDE is built with a modern, containerized stack:

*   **Backend:** Go (Golang) with a modular architecture (`internal/api`, `internal/library`, etc.).
*   **Database:** SQLite (via `modernc.org/sqlite` and `sqlx`) for persistent, zero-configuration indexing.
*   **Frontend:** React with a custom, dependency-free Vanilla CSS dark theme inspired by VS Code, featuring full client-side routing via `react-router-dom`.
*   **Media Processing:** `yt-dlp` for downloading, `ffprobe` for rapid background library scanning, and a dedicated Python script utilizing `mutagen` for robust physical metadata tag writing.

## Setup & Deployment

JellyIDE is designed to be easily deployed via Docker.

### Requirements
*   Docker
*   Docker Compose

### Quick Start

1.  **Clone the repository** (or navigate to the project root).
2.  **Configure your mount point** in the `docker-compose.yml`. By default, it maps your host's `/mnt/media/music` to the container.
3.  **Deploy:**
    ```bash
    docker compose up -d --build
    ```
4.  **Access:** Open your web browser and navigate to `http://localhost:8080`.

*Note: On the first startup, JellyIDE will automatically scan the mapped directory and begin populating your library.*

## License
MIT License
