# Khinsider Downloader

A vibe-coded Go-based command-line tool to download video game music soundtracks from KHInsider.  
Existing tools were clunky and unreliable, so I made this lightweight and efficient alternative.

## Installation

```bash
go install github.com/nalsai/khinsider_downloader@latest
```

## Usage

```bash
khinsider_downloader <album_url>
```

### Command Line Options

```
Usage: khinsider_downloader <album_url> [options]

Options:
  --format mp3|flac    Download format (default: flac)
  --no-images          Skip downloading album images
```
