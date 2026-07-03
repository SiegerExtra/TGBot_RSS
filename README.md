# TGBot RSS

A lightweight **Telegram Bot–based RSS reader** with powerful keyword filtering, multi-user subscriptions, and flexible notification formatting.

## Features

- Keyword filtering
- Exclusion filters (`-keyword`)
- Automatic feed polling
- Multi-user subscriptions
- Daily statistics
- Automatic image extraction
- Telegram HTML formatting
- Optional proxy support

## Quick Start

### Docker

Configure `BotToken`, `ADMINIDS`, `Cycletime`, `Debug`, `ProxyURL`, and `Pushinfo`, then run the provided Docker image.

### Native Installation

```bash
curl -sL https://raw.githubusercontent.com/IonRh/TGBot_RSS/main/TGBot_RSS.sh | bash
```

Edit `config.json`, test with `./TGBot_RSS`, then run using `nohup` if desired.

## Usage

- `/start` — Open the main menu
- `/help` — Show help

### Feed Format

`URL NAME MODE`

Where `MODE` is `0` for Telegram channels and `1` for standard subscriptions.

### Keyword Syntax

- `keyword`
- `keyword*pattern`
- `-keyword`
- `#t` title only
- `#c` content only
- `#a` title and content
- `keyword+FeedName` for feed-specific matching

## Database

SQLite tables:

- `subscriptions`
- `user_keywords`
- `feed_data`
