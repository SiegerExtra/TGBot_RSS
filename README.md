# TGBot_RSS/SiegerExtra
```
  _________.__                          ___________         __                 
 /   _____/|__| ____   ____   __________\_   _____/__  ____/  |_____________   
 \_____  \ |  |/ __ \ / ___\_/ __ \_  __ \    __)_\  \/  /\   __\_  __ \__  \  
 /        \|  \  ___// /_/  >  ___/|  | \/        \>    <  |  |  |  | \// __ \_
/_______  /|__|\___  >___  / \___  >__| /_______  /__/\_ \ |__|  |__|  (____  /
        \/         \/_____/      \/             \/      \/                  \/ 
```
A lightweight Go-based **Telegram bot–based RSS/Atom multi-feed reader**
	
## Features

- keyword + multiWildcard filters
- exclusion filters
- per-feed filtering
- Telegram.GUI-based general setup
- Telegram.ID admin access control
- per-user subscriptions
- Telegram.HTML output when desired
- automatic image extraction
- proxy support
- daily statistics

### Origin

Forked from *notoriously* `CN-only interface/output/help/gosh-even-timeZone` app/code by AbBai @ github.com/IonRh/TGBot_RSS
-> because you know, not everyone of us are CN yet, so we've dealt with the original code in a better way =)

## Quick Start

### `bash-script` for automated deployment/update on Linux (amd64/arm64/armv7)
- includes automated setup for service running as separate restricted-service-user,
	so *"your funds are SAFU"*, of course until they are not ;)

```bash
curl -sL https://raw.githubusercontent.com/SiegerExtra/TGBot_RSS/refs/heads/main/TGBot_RSS.sh | bash # DL and Run install script

sudo -e /usr/local/tgbot-rss/config.json # Populate/adjust config.json with your desired settings
sudo systemctl status tgbot-rss # Check target service
sudo systemctl enable --now tgbot-rss # Enable and start target service
```

### Manual deployment, if desired

- Download and unpack latest release, e.g. to your `home folder`
- Populate/adjust `config.json` with your desired settings
- Test-run with `./TGBot_RSS`
- Background-run using `nohup`, if desired

## Usage via Telegram.GUI

- `/start` — Open Telegram.GUI main menu
- `/help` — Show GUI help

### Feed Format

`FeedURL FeedName Mode`

Where `Mode` is `0` for standard feeds and `1` for channels. Just use `0`, if in doubts.

### Keyword Syntax

`Spaces` are supported in keywords
Matching is `case-insensitive`

- `keyword` or `key word` Exact match
- `key word*pattern*another pattern` (multi-)wildcard match
- `-keyword` Exclude match
- `#t` Matches just the post *Title*
- `#c` Matches just the post *Content*
- `#a` Matches both *Title* and *Content*
- `key word+FeedName` Matches posts in target feed only / per-feed match

## Database

`SQLite`
- Feel free to alter data manually for testing/setup purposes, e.g.
```bash
# Reset the last_update_time to target date interval
# from current date (day/week/month/year) @ 00:00, for all subscriptions
function tgrss.rts {
	sudo sqlite3 /usr/local/tgbot-rss/tgbot.db \
		"UPDATE feed_data SET last_update_time = datetime('now', '-$2 $1', 'start of day');";
};

tgrss.rts day 3; # Reset last_update_time to -3 days, allowing to re-match/notify for recent posts
sudo systemctl restart tgbot-rss; # Restart service to immediately start feed fetching/matching
```

## ChangeLog

`v1.0.6`
	Changed standart mode output to also include `subscription/feed name`;
	Changed original fixed CN-timeZone to be instead configurable via `config.json\TimeZone`;

`v1.0.5`
	Elaborated keywords to allow for `spaces`, e.g. `key word`;
	Changed original keywords separator from `,` to `#!#`;

`v1.0.4`
	Fully replaced all `CN-strings` with `EN-strings`;
	Reworked database access so it uses connections efficiently;

## Credits
- `AbBai` @ github.com/IonRh/TGBot_RSS for a pretty good piece of software, despite being excessively CN-biased
- US-based GPT `Claude` for "all the hard work" of rapid initial `CN`->`EN` strings translation and DB-access restructure
- CN-based GPT `DeepSeek` for overall processings ;)

## Disclaimer
- Any textual artefacts that *may appear* to be of `trolling` context, were never intended to be so,
	and thus are purely a subject of imaginative personal interpretation;
- All rights are reserved to their respective owners, except when they are not,
	according to included Boost Software License - Version 1.0 - August 17th, 2003