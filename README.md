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
- `Keyword/Exclusion/multiWildcard` filters
- `Global` and `Per-feed` keyword filtering
- `Per-user subscriptions` management
- `Telegram.GUI`-based general setup
- `Telegram.HTML output format` supported
- Automatic `image extraction`
- `Proxy` access support for Feeds
- Adjustable `Throttling of Telegram messages pushing`
	to prevent Telegram service overuse potentially resulting in bot throttling/ban
- Adjustable `Throttling of RSS/Atom feeds polling`
	to prevent RSS services overuse potentially resulting in access bans;
- Telegram.ID `admin/user access` control
- Daily statistics

### Origin
Forked from *notoriously* `CN-only-interface` app/code by AbBai @ github.com/IonRh/TGBot_RSS
*-> because you know, not everyone of us are CN yet, so we've dealt with the original code in a better way =)*

## Quick Start
### `bash-script` for automated deployment/update on Linux (amd64/arm64/armv7)
- includes automated setup for a service, running as separate restricted-service-user,
	so *"your funds are SAFU"*, of course until they are not ;)
```bash
# Download and Run install script
curl -sL https://raw.githubusercontent.com/SiegerExtra/TGBot_RSS/refs/heads/main/TGBot_RSS.sh | bash

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
- `/start` or `/s` Open clickable/tappable graphical main menu

## Telegram chat commands `case-insensitive`
- `/help` or `/h` Show GUI help
- `/version` or `/v` Show bot version
- `/resetLastUpdateTime` or `/r` *$days*[1-99] Reset last update timeStamp of all feeds related to calling TG-user,
		allowing quickly re-match feeds, for setup/debug purposes

### Feed Format
`FeedURL FeedName Mode`
	Where `Mode` is `0` for standard feeds and `1` for channels. Just use `0`, if in doubts.

### Keyword Syntax
Matching is `case-insensitive`
`Spaces` are supported in keywords

- `keyword` or `key word` Exact match
- `key word*pattern*another pattern` (multi-)wildcard match
- `-keyword` Exclude match
- `#t` Matches just the post *Title*
- `#c` Matches just the post *Content*
- `#a` Matches both *Title* and *Content*
- `key word+FeedName` Matches posts in target feed only / per-feed match

## Database

`SQLite`
- If desired, feel free to look-up or alter data manually via `sqlite3`, like for testing/setup purposes

## ChangeLog

`v1.0.7`
- Added `throttling of Telegram message pushing` via `config.json\TGmsgDelay`,
	to prevent Telegram service overuse resulting in potential bot-throttling/ban
- Added `throttling of RSS/Atom feeds polling` via `config.json\RSSpollDelay`,
		to prevent RSS services overuse resulting in potential throttling/bans
- Added chat command `/resetLastUpdateTime` `/r` *$days*[1-99] that resets feeds last update timeStamp,
		allowing quickly re-match feeds for setup/debug. Affects all feeds for calling TG user only
- Changed `keyword separator` to a most natural one: *`new line`*.
		That is, for multiple keywords, just naturally place each keyword on a separate line.
		Empty lines are skipped automatically.
- Changed original minced `keyword list output` to be one-keyword-per-line, and *in monospace font*
- Added alises for `/start` -> `/s`, `/help` -> `/h` chat commands
- Added chat command `/version` `/v`
- Explicitly *restricted* `keyword length` to max 57 bytes (also consider Unicode),
		since original code passes keywords via Telegram callback (max 64 bytes) using 'del_kw_'+keyword string.
		Without such restriction, adding a keyword >57 bytes causes `Delete Keyword` menu to become inoperational,
		until all lengthy keywords get removed manually/scripted from the database;
---
`v1.0.6`
- Changed standart mode output to also include `subscription/feed name`
- Changed original fixed CN-timeZone to be instead configurable via `config.json\TimeZone`
---
`v1.0.5`
- Elaborated keywords to allow for `spaces`, e.g. `key word`
- Changed original keywords separator from excessively-encountered `,` to `#!#`
---
`v1.0.4`
- Fully replaced all `CN-strings` with `EN-strings`
- Reworked database access so it uses connections efficiently

## Credits
- `AbBai` @ github.com/IonRh/TGBot_RSS for a pretty good piece of software, despite being excessively CN-biased
- US-based GPT `Claude` for rapid initial `CN`->`EN` strings translation and DB-access restructure
- CN-based GPT `DeepSeek` for overall processings/assistance ;)

## Disclaimer
- Any textual artefacts that *may appear* to be of `trolling` context, were never intended to be so,
	and thus are purely a subject of imaginative personal interpretation `=)`
- All rights are reserved to their respective owners, *except when they are not*,
	according to included Boost Software License - Version 1.0 - August 17th, 2003