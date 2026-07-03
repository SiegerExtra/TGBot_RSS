/*
TGBot_RSS/SiegerExtra @ github.com/SiegerExtra/TGBot_RSS
	Forked from notoriously CN-only-interface app/code by AbBai @ github.com/IonRh/TGBot_RSS
	-> because you know, not everyone of us are CN yet, so we've dealt with the original code in a better way =)
*/

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mmcdole/gofeed"

	"math/rand"
)

// getSubscriptions returns all RSS subscriptions from the database.
func getSubscriptions(db *sql.DB) ([]Subscription, error) {
	rows, err := db.Query("SELECT subscription_id, rss_url, rss_name, users, channel FROM subscriptions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subscriptions []Subscription
	for rows.Next() {
		var sub Subscription
		var usersStr string
		var channel int

		if err := rows.Scan(&sub.ID, &sub.URL, &sub.Name, &usersStr, &channel); err != nil {
			logMessage("error", fmt.Sprintf("Failed to scan subscription row: %v", err))
			continue
		}

		sub.Users = parseUserIDs(usersStr)
		sub.Channel = channel
		subscriptions = append(subscriptions, sub)
	}

	return subscriptions, nil
}

// parseUserIDs converts a stored user-ID string (JSON array or legacy CSV) to a slice.
func parseUserIDs(usersStr string) []int64 {
	usersStr = strings.Trim(usersStr, "[] ")
	if usersStr == "" {
		return nil
	}

	var userIDs []int64
	for _, idStr := range strings.Split(usersStr, ",") {
		var id int64
		if n, _ := fmt.Sscanf(strings.TrimSpace(idStr), "%d", &id); n == 1 && id > 0 {
			userIDs = append(userIDs, id)
		}
	}
	return userIDs
}

// getUserKeywords loads the keyword map (userID → keywords) from the database.
func getUserKeywords(db *sql.DB) (map[int64][]string, error) {
	rows, err := db.Query("SELECT user_id, keywords FROM user_keywords")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	userKeywords := make(map[int64][]string)
	for rows.Next() {
		var userID int64
		var keywordsStr string

		if err := rows.Scan(&userID, &keywordsStr); err != nil {
			continue
		}

		keywords := parseKeywords(keywordsStr)
		if len(keywords) > 0 {
			userKeywords[userID] = keywords
		}
	}

	return userKeywords, nil
}

// parseKeywords parses a stored keyword string (JSON array or comma-separated).
func parseKeywords(keywordsStr string) []string {
	keywordsStr = strings.TrimSpace(keywordsStr)
	if keywordsStr == "" {
		return nil
	}

	// JSON array format
	if strings.HasPrefix(keywordsStr, "[") && strings.HasSuffix(keywordsStr, "]") {
		var keywords []string
		if err := json.Unmarshal([]byte(keywordsStr), &keywords); err == nil {
			return keywords
		}
	}

	// Fallback: comma-separated
	var keywords []string
	for _, kw := range strings.Split(keywordsStr, ",") {
		kw = strings.TrimSpace(kw)
		if kw != "" {
			keywords = append(keywords, kw)
		}
	}
	return keywords
}

// fetchRSS retrieves new items from the feed and returns only those published
// after the last recorded update time.
func fetchRSS(db *sql.DB, sub Subscription, client *http.Client) ([]Message, error) {
	parser := gofeed.NewParser()
	parser.Client = client

	feed, err := parser.ParseURL(sub.URL)
	if err != nil {
		return nil, err
	}

	if len(feed.Items) == 0 {
		return nil, nil
	}

	lastUpdateTime, err := getLastUpdateTime(db, sub.Name)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to read last update time: %v", err))
		lastUpdateTime = time.Time{}
	}

	var messages []Message
	var latestTime time.Time

	for _, item := range feed.Items {
		pubTime := getItemTime(item)
		if pubTime.After(latestTime) {
			latestTime = pubTime
		}

		if pubTime.After(lastUpdateTime) {
			messages = append(messages, Message{
				Title:       item.Title,
				Description: item.Description,
				Link:        item.Link,
				PubDate:     pubTime,
			})
		}
	}

	if !latestTime.IsZero() {
		updateLastTime(db, sub.Name, latestTime, feed.Items[0].Title)
	}

	return messages, nil
}

// getItemTime returns the publication time of a feed item, falling back to
// UpdatedParsed and then time.Now() if neither field is set.
func getItemTime(item *gofeed.Item) time.Time {
	if item.PublishedParsed != nil {
		return item.PublishedParsed.UTC()
	}
	if item.UpdatedParsed != nil {
		return item.UpdatedParsed.UTC()
	}
	return time.Now().UTC()
}

// getLastUpdateTime retrieves the last recorded update time for rssName.
// On first call it inserts a sentinel record so future updates are relative to now.
func getLastUpdateTime(db *sql.DB, rssName string) (time.Time, error) {
	var timeStr string
	err := db.QueryRow("SELECT last_update_time FROM feed_data WHERE rss_name = ?", rssName).Scan(&timeStr)

	if err == sql.ErrNoRows {
		// First run — seed the record with the current time so we do not
		// immediately re-deliver all historical items.
		_, err = db.Exec(
			"INSERT INTO feed_data (rss_name, last_update_time, latest_title) VALUES (?, ?, ?)",
			rssName, time.Now().Format(TimeStampFormat), "",
		)
		return time.Time{}, err
	}

	if err != nil {
		return time.Time{}, err
	}

	return time.Parse("2006-01-02 15:04:05", timeStr)
}

// updateLastTime persists the latest publication time and article title for rssName.
func updateLastTime(db *sql.DB, rssName string, updateTime time.Time, title string) {
	_, err := db.Exec(
		"UPDATE feed_data SET last_update_time = ?, latest_title = ? WHERE rss_name = ?",
		updateTime.Format(TimeStampFormat), title, rssName,
	)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to update last time: %v", err))
	}
}

// matchesKeywords checks msg against keywords and returns the matched keyword
// names, or nil if no match (or a block keyword fires).
func matchesKeywords(msg Message, keywords []string, rssName string) []string {
	if len(keywords) == 0 {
		return nil
	}

	var matchedKeywords []string
	var blockedKeywords []string

	titleContent := strings.ToLower(msg.Title)
	descContent := strings.ToLower(msg.Description)
	allContent := strings.ToLower(msg.Title + " " + msg.Description)

	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}

		// Block keyword prefix
		isBlockKeyword := strings.HasPrefix(keyword, "-")
		if isBlockKeyword {
			keyword = strings.TrimPrefix(keyword, "-")
		}

		// Match scope prefix (#t = title, #c = description, #a = all)
		matchScope := "default" // default = title only (backwards-compatible)
		processedKeyword := keyword

		switch {
		case strings.HasPrefix(keyword, "#t"):
			matchScope = "title"
			processedKeyword = strings.TrimPrefix(keyword, "#t")
		case strings.HasPrefix(keyword, "#c"):
			matchScope = "description"
			processedKeyword = strings.TrimPrefix(keyword, "#c")
		case strings.HasPrefix(keyword, "#a"):
			matchScope = "all"
			processedKeyword = strings.TrimPrefix(keyword, "#a")
		}

		processedKeyword = strings.TrimSpace(processedKeyword)

		// RSS name filter (keyword+FeedName)
		actualKeyword := processedKeyword
		var targetRSSName string
		hasRSSFilter := false

		if strings.Contains(processedKeyword, "+") {
			parts := strings.SplitN(processedKeyword, "+", 2)
			if len(parts) == 2 {
				actualKeyword = strings.TrimSpace(parts[0])
				targetRSSName = strings.TrimSpace(parts[1])
				hasRSSFilter = true
			}
		}

		if hasRSSFilter && strings.ToLower(rssName) != strings.ToLower(targetRSSName) {
			continue // Feed name does not match
		}

		lowerKeyword := strings.ToLower(actualKeyword)

		var targetContent string
		switch matchScope {
		case "title":
			targetContent = titleContent
		case "description":
			targetContent = descContent
		case "all":
			targetContent = allContent
		default:
			targetContent = titleContent
		}

		// Wildcard matching via regular expression
		if strings.Contains(lowerKeyword, "*") {
			pattern := "^.*" + strings.ReplaceAll(lowerKeyword, "*", ".*") + ".*$"
			re, err := regexp.Compile(pattern)
			if err == nil && re.MatchString(targetContent) {
				if isBlockKeyword {
					blockedKeywords = append(blockedKeywords, actualKeyword)
				} else {
					matchedKeywords = append(matchedKeywords, actualKeyword)
				}
				continue
			}
		}

		// Plain substring matching
		if strings.Contains(targetContent, lowerKeyword) {
			if isBlockKeyword {
				blockedKeywords = append(blockedKeywords, actualKeyword)
			} else {
				matchedKeywords = append(matchedKeywords, actualKeyword)
			}
		}
	}

	if len(blockedKeywords) > 0 {
		logMessage("debug", fmt.Sprintf("Message blocked by [%s]: %s",
			strings.Join(blockedKeywords, ", "), msg.Title))
		return nil
	}

	return matchedKeywords
}

// processSubscription fetches new items for sub and delivers matching ones to subscribers.
func processSubscription(db *sql.DB, sub Subscription, userKeywords map[int64][]string, client *http.Client) {
	if cyclenum == 0 {
		logMessage("info", fmt.Sprintf("Processing subscription: %s @ %s", sub.Name, sub.URL))
	}

	messages, err := fetchRSS(db, sub, client)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to fetch RSS %s: %v", sub.Name, err))
		return
	}

	if len(messages) == 0 {
		logMessage("debug", fmt.Sprintf("No new items in %s", sub.Name))
		return
	}

	pushCount := 0
	for _, msg := range messages {
		for _, userID := range sub.Users {
			keywords := userKeywords[userID]
			if len(keywords) == 0 {
				continue
			}

			matchedKeywords := matchesKeywords(msg, keywords, sub.Name)
			if len(matchedKeywords) == 0 {
				continue
			}

			pushCount++
			logMessage("debug", fmt.Sprintf("Keyword(s) [%s] matched — pushing to user %d: %s",
				strings.Join(matchedKeywords, ", "), userID, msg.Title))

			recordPush(sub.Name)

			// Format matched keyword badges
			keywordCodes := make([]string, len(matchedKeywords))
			for i, kw := range matchedKeywords {
				keywordCodes[i] = fmt.Sprintf("<code>%s</code>", kw)
			}
			formattedKeywords := strings.Join(keywordCodes, " ")

			// Load the configured TimeZone
			loc, err := time.LoadLocation(globalConfig.TimeZone)
			if err != nil {
				// Fallback to UTC if timezone is invalid
				logMessage("warn", fmt.Sprintf("Invalid timezone '%s', falling back to UTC", globalConfig.TimeZone))
				loc = time.UTC
			}

			// Format the time with timezone offset
			tzTime := msg.PubDate.In(loc)
			formattedDate := tzTime.Format(TimeStampFormat)
			timezoneOffset := tzTime.Format(TimeZoneFormat)

			var htmlMessage, otherpush string

			// Modify message formatting to include the timezone offset
			if sub.Channel == 1 {
				// Channel mode👋📶: include description and optional image
				imageURL := extractImageURL(msg.Description)
				cleanDescription := cleanHTMLContent(msg.Description)
				
				htmlMessage = fmt.Sprintf("👋📶 %s: %s\n🕒 %s %s\n%s\n", sub.Name, formattedKeywords, formattedDate, timezoneOffset, cleanDescription)
				otherpush = fmt.Sprintf("👋📶 %s\n🕒 %s %s\n%s", sub.Name, formattedDate, timezoneOffset, cleanDescription)
				
				if imageURL != "" {
					go sendPhotoMessage(userID, imageURL, htmlMessage)
				} else {
					go sendHTMLMessage(userID, htmlMessage)
				}
			} else {
				// Standard mode📡📶: feed + post title + link
				htmlMessage = fmt.Sprintf("📡📶 %s\n\n📌 %s\n\n🔖 Keywords: %s\n\n🕒 %s %s\n\n🔗 %s",
					sub.Name, msg.Title, formattedKeywords, formattedDate, timezoneOffset, msg.Link)
				otherpush = fmt.Sprintf("📡📶 %s\n\n📌 %s\n\n🕒 %s %s\n\n🔗 %s", sub.Name, msg.Title, formattedDate, timezoneOffset, msg.Link)
				go sendHTMLMessage(userID, htmlMessage)
			}

			if userID == globalConfig.ADMINIDS {
				go sendother(otherpush)
			}
		}
	}

	logMessage("info", fmt.Sprintf("Subscription %s done — %d item(s) pushed", sub.Name, pushCount))
}

// checkAllRSS is called on each ticker tick.
// It reuses the global db connection passed in from startRSSMonitor
// instead of opening a fresh connection on every cycle.
func checkAllRSS() {
    startTime := time.Now()
    resetPushStatsIfNeeded()
    logMessage("info", fmt.Sprintf("RSS reader: polling subscriptions now. Entire subscription-pool is polled every %d min, while using max %d sec delay between each single feed polling", globalConfig.Cycletime, globalConfig.RSSpollDelay))

    subscriptions, err := getSubscriptions(db)
    if err != nil {
        logMessage("error", fmt.Sprintf("Failed to load subscriptions: %v", err))
        return
    }

    if len(subscriptions) == 0 {
        logMessage("info", "No RSS subscriptions found.")
        return
    }

    userKeywords, err := getUserKeywords(db)
    if err != nil {
        logMessage("error", fmt.Sprintf("Failed to load user keywords: %v", err))
        return
    }

    client := createHTTPClient(globalConfig.ProxyURL)

    if globalConfig.RSSpollDelay == 0 {
        // Concurrent processing via goroutines (original behavior)
        var wg sync.WaitGroup
        for _, sub := range subscriptions {
            wg.Add(1)
            go func(sub Subscription) {
                defer wg.Done()
                processSubscription(db, sub, userKeywords, client)
            }(sub)
        }
        wg.Wait()
    } else {
        // Sequential processing with delay, to prevent target services overuse/bans
        for _, sub := range subscriptions {
            processSubscription(db, sub, userKeywords, client)

            // Randomize delay if RSSpollDelay >= 5
            delay := globalConfig.RSSpollDelay
            strRandomDelay := "fixed"
            if delay >= 5 {
                // Random between delay/4 and delay
                minDelay := delay / 4
                rand.Seed(time.Now().UnixNano())
                delay = minDelay + rand.Intn(delay-minDelay+1)
                strRandomDelay = "random"
            }
            logMessage("debug", fmt.Sprintf("RSS single poll complete, adding a %s delay of %v sec", strRandomDelay, delay))
            time.Sleep(time.Duration(delay) * time.Second)
        }
    }

    logMessage("info", fmt.Sprintf("RSS check complete — elapsed: %v", time.Since(startTime)))
    cyclenum = 1
}

// extractImageURL searches htmlContent for the first image URL.
// It tries (in order): <img src="…">, bare image file URLs, Telegram CDN URLs.
func extractImageURL(htmlContent string) string {
	imgRegex := regexp.MustCompile(`<img[^>]+src=["']([^"']+)["']`)
	if matches := imgRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		return matches[1]
	}

	urlRegex := regexp.MustCompile(`https?://[^\s"']+\.(jpg|jpeg|png|gif|webp)`)
	if m := urlRegex.FindString(htmlContent); m != "" {
		return m
	}

	cdnRegex := regexp.MustCompile(`https?://cdn[0-9]*\.cdn-telegram\.org/[^\s"']+`)
	if m := cdnRegex.FindString(htmlContent); m != "" {
		return m
	}

	return ""
}

// cleanHTMLContent strips HTML tags that Telegram does not support while
// preserving the subset it does: <b>, <i>, <u>, <s>, <code>, <pre>, <a>.
func cleanHTMLContent(htmlContent string) string {
	// Remove <img> tags
	content := regexp.MustCompile(`<img[^>]*>`).ReplaceAllString(htmlContent, "")

	// Convert <br> to newline
	content = regexp.MustCompile(`<br\s*\/?>` ).ReplaceAllString(content, "\n")

	// Temporarily encode supported tags so they survive the strip step
	replacements := [][2]string{
		{"<b>", "§§§B§§§"}, {"</b>", "§§§/B§§§"},
		{"<i>", "§§§I§§§"}, {"</i>", "§§§/I§§§"},
		{"<u>", "§§§U§§§"}, {"</u>", "§§§/U§§§"},
		{"<s>", "§§§S§§§"}, {"</s>", "§§§/S§§§"},
		{"<code>", "§§§CODE§§§"}, {"</code>", "§§§/CODE§§§"},
		{"<pre>", "§§§PRE§§§"}, {"</pre>", "§§§/PRE§§§"},
	}
	for _, r := range replacements {
		content = regexp.MustCompile(regexp.QuoteMeta(r[0])).ReplaceAllString(content, r[1])
	}

	// Encode <a href="…"> specially
	content = regexp.MustCompile(`<a\s+href=["']([^"']+)["'][^>]*>`).
		ReplaceAllString(content, "§§§A§§§$1§§§")
	content = regexp.MustCompile(`</a>`).ReplaceAllString(content, "§§§/A§§§")

	// Strip all remaining HTML tags
	content = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(content, "")

	// Restore supported tags
	restores := [][2]string{
		{"§§§B§§§", "<b>"}, {"§§§/B§§§", "</b>"},
		{"§§§I§§§", "<i>"}, {"§§§/I§§§", "</i>"},
		{"§§§U§§§", "<u>"}, {"§§§/U§§§", "</u>"},
		{"§§§S§§§", "<s>"}, {"§§§/S§§§", "</s>"},
		{"§§§CODE§§§", "<code>"}, {"§§§/CODE§§§", "</code>"},
		{"§§§PRE§§§", "<pre>"}, {"§§§/PRE§§§", "</pre>"},
	}
	for _, r := range restores {
		content = regexp.MustCompile(regexp.QuoteMeta(r[0])).ReplaceAllString(content, r[1])
	}
	content = regexp.MustCompile(`§§§A§§§(.*?)§§§`).ReplaceAllString(content, `<a href="$1">`)
	content = regexp.MustCompile(`§§§/A§§§`).ReplaceAllString(content, "</a>")

	// Collapse runs of three or more newlines
	content = regexp.MustCompile(`\n{3,}`).ReplaceAllString(content, "\n\n")

	return content
}
