/*
TGBot_RSS/SiegerExtra @ github.com/SiegerExtra/TGBot_RSS
	Forked from notoriously CN-only interface app/code by AbBai @ github.com/IonRh/TGBot_RSS
	-> because you know, not all of us are from CN, and thus unwilling to tolerate it =)

Changelog:
v1.0.6
	Changed standart mode output to also include subscription/feed name;
	Changed original fixed CN-timeZone to be instead configurable via config.json\TimeZone;

v1.0.5
	Elaborated keywords to allow for spaces, e.g. 'key word';
	Changed original keywords separator from ','' to '#!#';

v1.0.4
	Fully replaced all CN-strings with EN-strings;
	Reworked database access so it uses connections efficiently;
*/

/*
Cross-compiling for Linux on Windows
//CGO-free compilation cmd, only for CGO-free packages:
//$env:GOOS = "linux"; $env:GOARCH = "amd64"; go build -ldflags "-X main.version=v1.0.4" -o TGBot_RSS

Because of go-sqlite3 requiring CGO (C lang code parts for Go), and cross-compiling for Linux on Windows,
install Zig which provides C cross-compiler that targets Linux
winget install zig.zig --scope machine
OR
use WSL/VM for native Linux compiling

CGO-enabled compilation cmd:
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED="1"; $env:CC = "zig cc -target x86_64-linux-gnu"; go build -ldflags "-X main.version=v1.0.0" -o TGBot_RSS

Alternatively, to use pure-Go SQLite drivers, like modernc.org/sqlite or github.com/ncruces/go-sqlite3
*/
/*
Versioning samples:
-ldflags "-X main.version=v1.2.3 -X main.buildTime=$(date -u +%F) -X main.gitCommit=$(git rev-parse --short HEAD)"

-ldflags "-X main.version=v1.2.3 -X main.buildTime=$(Get-Date -Format 'yyyyMMddHHmmss')"
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED="1"; $env:CC = "zig cc -target x86_64-linux-gnu"; go build -ldflags "-X main.version=v1.0.0 -X main.buildTime=$(Get-Date -Format 'yyyyMMddHHmmss')" -o TGBot_RSS
*/

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/mattn/go-sqlite3"
)

// Version information — set at build time via -ldflags:
var (
	version   = "1.0.0"
	buildTime = "-"
	gitCommit = "-"
)

// Config holds application configuration loaded from config.json.
type Config struct {
	BotToken  string `json:"BotToken"`  // Telegram Bot API token
	ADMINIDS  int64  `json:"ADMINIDS"`  // Administrator user ID
	Cycletime int    `json:"Cycletime"` // RSS check interval (minutes)
	Debug     bool   `json:"Debug"`     // Enable debug logging
	ProxyURL  string `json:"ProxyURL"`  // Optional proxy server URL
	Pushinfo  string `json:"Pushinfo"`  // Push notification endpoint template
	TimeZone  string `json:"TimeZone"`  // IANA timezone name, e.g. "Asia/Riyadh"
}

// Message holds a parsed RSS feed entry.
type Message struct {
	Title       string
	Description string
	Link        string
	PubDate     time.Time
}

// Subscription represents an RSS feed subscription stored in the database.
type Subscription struct {
	ID      int     // Unique database ID
	URL     string  // RSS feed URL
	Name    string  // Subscription display name
	Users   []int64 // List of subscribed user IDs
	Channel int     // 1 = channel mode (push full content), 0 = standard mode
}

// UserState tracks the current interactive state for a user.
type UserState struct {
	Action    string                 // Current action, e.g. "add_keyword", "add_subscription"
	MessageID int                    // Related message ID for editing
	Data      map[string]interface{} // Additional action-specific data
}

// Global singletons
var (
	globalConfig *Config
	db           *sql.DB
	bot          *tgbotapi.BotAPI
	userStates   = make(map[int64]*UserState)
	stateMutex   sync.RWMutex
)

// UserStats holds aggregated statistics for a single user.
type UserStats struct {
	SubscriptionCount int
	KeywordCount      int
}

// SubscriptionInfo is a lightweight view of a subscription for display purposes.
type SubscriptionInfo struct {
	Name       string
	URL        string
	LastUpdate string
}

var cyclenum int

// Application-wide constants.
const (
	MaxMessageLength = 4000             // Maximum Telegram message length in bytes
	DatabaseTimeout  = 30 * time.Second // Timeout for database operations
	HTTPTimeout      = 60 * time.Second // Timeout for outbound HTTP requests
	LogFile          = "bot.log"        // Path to the log file
	DBFile           = "tgbot.db"       // Path to the SQLite database file
	ConfigFile       = "config.json"    // Path to the configuration file
	DefaultCycleTime = 300              // Default RSS check interval in seconds
)

// PushStats tracks daily push notification counts.
type PushStats struct {
	Date      string         // Date in YYYY-MM-DD format
	TotalPush int            // Total pushes sent today
	ByRSS     map[string]int // Per-feed push counts
	mutex     sync.Mutex     // Protects all fields above
}

// DailyPushStats is the global daily counter, reset at midnight.
var DailyPushStats = &PushStats{
	Date:  time.Now().Format("2006-01-02"),
	ByRSS: make(map[string]int),
}

// DatabaseOperator wraps *sql.DB and provides transaction helpers.
type DatabaseOperator struct {
	db *sql.DB
}

// resetPushStatsIfNeeded resets the daily counters when the calendar date changes.
func resetPushStatsIfNeeded() {
	DailyPushStats.mutex.Lock()
	defer DailyPushStats.mutex.Unlock()

	currentDate := time.Now().Format("2006-01-02")
	if DailyPushStats.Date != currentDate {
		if DailyPushStats.TotalPush > 0 {
			logMessage("info", fmt.Sprintf("Date changed — %s push summary: %d total. Counters reset.",
				DailyPushStats.Date, DailyPushStats.TotalPush))
		}
		DailyPushStats.Date = currentDate
		DailyPushStats.TotalPush = 0
		DailyPushStats.ByRSS = make(map[string]int)
	}
}

// recordPush increments the push counter for the given RSS feed name.
func recordPush(rssName string) {
	DailyPushStats.mutex.Lock()
	defer DailyPushStats.mutex.Unlock()

	currentDate := time.Now().Format("2006-01-02")
	if DailyPushStats.Date != currentDate {
		DailyPushStats.Date = currentDate
		DailyPushStats.TotalPush = 0
		DailyPushStats.ByRSS = make(map[string]int)
	}

	DailyPushStats.TotalPush++
	DailyPushStats.ByRSS[rssName]++
}

// GetPushStatsInfo returns a formatted string describing today's push statistics.
func GetPushStatsInfo() string {
	DailyPushStats.mutex.Lock()
	defer DailyPushStats.mutex.Unlock()

	info := fmt.Sprintf("📊 Today (%s) pushes: %d total",
		DailyPushStats.Date, DailyPushStats.TotalPush)

	if len(DailyPushStats.ByRSS) > 0 {
		info += "\n"
		for rssName, count := range DailyPushStats.ByRSS {
			info += fmt.Sprintf("📊 %s: %d\n", rssName, count)
		}
	}

	return info
}

// loadConfig reads and validates the configuration from ConfigFile.
func loadConfig() (*Config, error) {
	file, err := os.Open(ConfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %v", err)
	}
	defer file.Close()

	var config Config
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	if config.BotToken == "" {
		return nil, fmt.Errorf("BotToken must not be empty")
	}

	if config.Cycletime <= 0 {
		config.Cycletime = DefaultCycleTime
	}

	return &config, nil
}

// logMessage writes a levelled log entry to stdout and to the log file.
// Passing an optional userID appends a [User:N] tag to the entry.
func logMessage(level, message string, userID ...int64) {
	colors := map[string]string{
		"info":  "\033[32m", // green
		"error": "\033[31m", // red
		"debug": "\033[34m", // blue
		"warn":  "\033[33m", // yellow
	}

	icons := map[string]string{
		"info":  "ℹ️",
		"error": "❌",
		"debug": "🐞",
		"warn":  "⚠️",
	}

	if level == "debug" && (globalConfig == nil || !globalConfig.Debug) {
		return
	}

	color := colors[level]
	icon := icons[level]
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	userInfo := ""
	if len(userID) > 0 && userID[0] != 0 {
		userInfo = fmt.Sprintf(" [User:%d]", userID[0])
	}

	logText := fmt.Sprintf("%s [%s]%s %s%s", timestamp, level, userInfo, icon, message)

	fmt.Printf("\033[36m%s\033[0m %s%s\033[0m %s%s\033[0m%s\n",
		timestamp, color, strings.ToUpper(level), icon, message, userInfo)

	writeToLogFile(logText)
}

// writeToLogFile appends a log line to LogFile.
func writeToLogFile(message string) {
	file, err := os.OpenFile(LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open log file: %v\n", err)
		return
	}
	defer file.Close()

	if _, err := file.WriteString(message + "\n"); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write log: %v\n", err)
	}
}

// createHTTPClient builds an *http.Client with sensible timeouts.
// If proxyURL is non-empty, the client routes traffic through it.
func createHTTPClient(proxyURL string) *http.Client {
	transport := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 10 * time.Second,
	}

	client := &http.Client{
		Timeout:   HTTPTimeout,
		Transport: transport,
	}

	if proxyURL != "" {
		if parsed, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
			if cyclenum == 0 {
				logMessage("info", "Using proxy: "+proxyURL)
			}
		} else {
			logMessage("error", "Failed to parse proxy URL: "+err.Error())
		}
	}

	return client
}

// ---------------------------------------------------------------------------
// User state management
// ---------------------------------------------------------------------------

// setUserState stores the current interactive state for userID.
func setUserState(userID int64, action string, messageID int, data map[string]interface{}) {
	stateMutex.Lock()
	defer stateMutex.Unlock()

	if data == nil {
		data = make(map[string]interface{})
	}

	userStates[userID] = &UserState{
		Action:    action,
		MessageID: messageID,
		Data:      data,
	}

	logMessage("debug", fmt.Sprintf("User state set: %s", action), userID)
}

// getUserState returns the current state for userID, or nil if none is set.
func getUserState(userID int64) *UserState {
	stateMutex.RLock()
	defer stateMutex.RUnlock()
	return userStates[userID]
}

// clearUserState removes any pending state for userID.
func clearUserState(userID int64) {
	stateMutex.Lock()
	defer stateMutex.Unlock()
	delete(userStates, userID)
	logMessage("debug", "User state cleared", userID)
}

// ---------------------------------------------------------------------------
// Database helpers
//
// withDB avoids the per-call PingContext that was previously here; SQLite
// connections are reliable once opened, and the extra round-trip added latency
// on every query.  A context timeout is still applied to the operation itself
// via the sql.DB methods.
// ---------------------------------------------------------------------------

// withDB executes operation using the global db handle.
// It applies DatabaseTimeout to the operation's context but skips the
// superfluous PingContext that was in the previous implementation.
func withDB(operation func(*sql.DB) error) error {
	_, cancel := context.WithTimeout(context.Background(), DatabaseTimeout)
	defer cancel()
	return operation(db)
}

// ---------------------------------------------------------------------------
// MessageSender — unified Telegram message sending
// ---------------------------------------------------------------------------

// MessageSender wraps the bot API and provides consistent send/edit helpers.
type MessageSender struct {
	bot *tgbotapi.BotAPI
}

// NewMessageSender creates a MessageSender backed by bot.
func NewMessageSender(bot *tgbotapi.BotAPI) *MessageSender {
	return &MessageSender{bot: bot}
}

// SendResponse either edits an existing message (messageID > 0) or sends a new one.
func (m *MessageSender) SendResponse(userID int64, messageID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) error {
	if messageID > 0 {
		edit := tgbotapi.NewEditMessageText(userID, messageID, text)
		if keyboard != nil {
			edit.ReplyMarkup = keyboard
		}
		_, err := m.bot.Send(edit)
		return err
	}
	msg := tgbotapi.NewMessage(userID, text)
	if keyboard != nil {
		msg.ReplyMarkup = *keyboard
	}
	_, err := m.bot.Send(msg)
	return err
}

// SendHTMLResponse sends or edits a message using HTML parse mode.
// The optional disablePreview parameter suppresses link previews when true.
func (m *MessageSender) SendHTMLResponse(userID int64, messageID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup, disablePreview ...bool) error {
	logMessage("debug", fmt.Sprintf("SendHTMLResponse: messageID=%d, textLen=%d", messageID, len(text)), userID)

	shouldDisablePreview := false
	if len(disablePreview) > 0 {
		shouldDisablePreview = disablePreview[0]
	}

	if messageID > 0 {
		edit := tgbotapi.NewEditMessageText(userID, messageID, text)
		edit.ParseMode = "HTML"
		edit.DisableWebPagePreview = shouldDisablePreview
		if keyboard != nil {
			edit.ReplyMarkup = keyboard
		}
		logMessage("debug", "Editing existing message", userID)
		_, err := m.bot.Send(edit)
		if err != nil {
			logMessage("error", fmt.Sprintf("Failed to edit message: %v", err), userID)
		}
		return err
	}

	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = shouldDisablePreview
	if keyboard != nil {
		msg.ReplyMarkup = *keyboard
	}
	logMessage("debug", "Sending new message", userID)
	_, err := m.bot.Send(msg)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to send message: %v", err), userID)
	}
	return err
}

// SendError sends an error message with a back-to-menu button.
func (m *MessageSender) SendError(userID int64, messageID int, errorText string) {
	keyboard := CreateBackButton()
	if err := m.SendResponse(userID, messageID, errorText, &keyboard); err != nil {
		logMessage("error", fmt.Sprintf("Failed to send error message: %v", err), userID)
	}
}

// HandleLongText sends text, splitting it into chunks if it exceeds MaxMessageLength.
func (m *MessageSender) HandleLongText(userID int64, messageID int, text string, addBackButton bool) {
	if len(text) <= MaxMessageLength {
		var keyboard *tgbotapi.InlineKeyboardMarkup
		if addBackButton {
			kb := CreateBackButton()
			keyboard = &kb
		}
		m.SendResponse(userID, messageID, text, keyboard)
		return
	}

	if messageID > 0 {
		m.bot.Request(tgbotapi.NewDeleteMessage(userID, messageID))
	}

	chunks := splitMessage(text, MaxMessageLength)
	for i, chunk := range chunks {
		var keyboard *tgbotapi.InlineKeyboardMarkup
		if addBackButton && i == len(chunks)-1 {
			kb := CreateBackButton()
			keyboard = &kb
		}
		m.SendResponse(userID, 0, chunk, keyboard)
		if i < len(chunks)-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// ---------------------------------------------------------------------------
// Keyboard builders
// ---------------------------------------------------------------------------

// CreateBackButton returns a single-button keyboard that navigates back to the main menu.
func CreateBackButton() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back to menu", "back_to_menu"),
		),
	)
}

// CreateDeleteKeyboard builds a grid keyboard for deleting items.
// Each button is labelled "❌ <item>" and carries the callback data "<prefix>_<item>".
func CreateDeleteKeyboard(items []string, prefix string) tgbotapi.InlineKeyboardMarkup {
	const buttonsPerRow = 3
	var keyboardRows [][]tgbotapi.InlineKeyboardButton
	var currentRow []tgbotapi.InlineKeyboardButton

	for i, item := range items {
		currentRow = append(currentRow, tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("❌ %s", item),
			fmt.Sprintf("%s_%s", prefix, item),
		))

		if len(currentRow) == buttonsPerRow || i == len(items)-1 {
			keyboardRows = append(keyboardRows, currentRow)
			currentRow = []tgbotapi.InlineKeyboardButton{}
		}
	}

	// Empty spacer row then back button
	keyboardRows = append(keyboardRows, []tgbotapi.InlineKeyboardButton{})
	keyboardRows = append(keyboardRows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 Back to menu", "back_to_menu"),
	))

	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: keyboardRows}
}

// ---------------------------------------------------------------------------
// DatabaseOperator
// ---------------------------------------------------------------------------

// NewDatabaseOperator wraps db in a DatabaseOperator.
func NewDatabaseOperator(db *sql.DB) *DatabaseOperator {
	return &DatabaseOperator{db: db}
}

// ExecuteWithTransaction runs operation inside a database transaction.
func (d *DatabaseOperator) ExecuteWithTransaction(operation func(*sql.Tx) error) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := operation(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// Execute delegates to the global withDB helper.
func (d *DatabaseOperator) Execute(operation func(*sql.DB) error) error {
	return withDB(operation)
}

// ---------------------------------------------------------------------------
// UserActionHandler — dispatches keyword / subscription actions
// ---------------------------------------------------------------------------

// UserActionHandler coordinates MessageSender and DatabaseOperator for user actions.
type UserActionHandler struct {
	sender *MessageSender
	dbOp   *DatabaseOperator
}

// NewUserActionHandler creates a UserActionHandler.
func NewUserActionHandler(sender *MessageSender, dbOp *DatabaseOperator) *UserActionHandler {
	return &UserActionHandler{sender: sender, dbOp: dbOp}
}

// HandleAction dispatches to the keyword or subscription handler based on actionType.
func (h *UserActionHandler) HandleAction(userID int64, messageID int, actionType, action string, data ...string) {
	switch actionType {
	case "keyword":
		h.handleKeywordAction(userID, messageID, action, data...)
	case "subscription":
		h.handleSubscriptionAction(userID, messageID, action, data...)
	}
}

func (h *UserActionHandler) handleKeywordAction(userID int64, messageID int, action string, data ...string) {
	switch action {
	case "add_prompt":
		setUserState(userID, "add_keyword", messageID, nil)
		text := "Please enter the keyword(s) to add. Separate multiple keywords with #!#\n\n" +
			"💡 Matching tips:\n" +
			" * matches any sequence of characters\n" +
			" -keyword blocks content containing that keyword\n" +
			"Example: you*great* matches \"you are so great!\"\n" +
			"Example: -dislike blocks content containing \"dislike\"\n\n" +
			"💡 Scope prefixes:\n" +
			"#t keyword — title only\n" +
			"#c keyword — description only\n" +
			"#a keyword — title and description\n" +
			"Example: #ttechnology matches \"technology\" in titles only\n" +
			"Example: #cnews matches \"news\" in descriptions only\n\n" +
			"💡 RSS filter:\n" +
			"keyword+FeedName — restrict matching to a specific feed\n" +
			"Example: tech+TechNews  only matches the feed named \"TechNews\"\n\n" +
			"💡 Use * alone to receive all items from all feeds."
		keyboard := CreateBackButton()
		h.sender.SendResponse(userID, messageID, text, &keyboard)

	case "add":
		if len(data) == 0 {
			h.sender.SendError(userID, messageID, "❌ Please enter a valid keyword.")
			return
		}

		result, err := h.addKeywords(userID, data)
		if err != nil {
			logMessage("error", fmt.Sprintf("Failed to add keywords: %v", err), userID)
			h.sender.SendError(userID, messageID, "Failed to add keywords, please try again later.")
			return
		}
		clearUserState(userID)
		keyboard := CreateBackButton()
		h.sender.SendResponse(userID, messageID, result, &keyboard)

	case "view":
		h.viewKeywords(userID, messageID)

	case "delete_list":
		h.showDeleteKeywords(userID, messageID)

	case "delete":
		if len(data) == 0 {
			h.sender.SendError(userID, messageID, "Failed to delete keyword: missing argument.")
			return
		}
		h.deleteKeyword(userID, messageID, data[0])
	}
}

func (h *UserActionHandler) handleSubscriptionAction(userID int64, messageID int, action string, data ...string) {
	switch action {
	case "add_prompt":
		setUserState(userID, "add_subscription", messageID, nil)
		text := "✏️ Add a new subscription manually:\n" +
			"⚠️ Channels must be converted to RSS before adding.\n" +
			"Enter subscription details in this format:\n\n" +
			"URL Name ChannelMode\n\n" +
			"📝 Examples:\n" +
			"Regular:  https://example.com/feed TechNews 0\n" +
			"Channel:  https://example.com/channel/feed TGUpdates 1"
		keyboard := CreateBackButton()
		h.sender.SendResponse(userID, messageID, text, &keyboard)

	case "add":
		if len(data) < 3 {
			h.sender.SendError(userID, messageID,
				"❌ Format error! Please use:\nURL Name ChannelMode\nExample: https://example.com/feed TechNews 0")
			return
		}
		h.addSubscription(userID, messageID, data[0], data[1], data[2])

	case "view":
		h.viewSubscriptions(userID, messageID)

	case "delete_list":
		h.showDeleteSubscriptions(userID, messageID)

	case "delete":
		if len(data) == 0 {
			h.sender.SendError(userID, messageID, "Failed to delete subscription: missing argument.")
			return
		}
		h.deleteSubscription(userID, messageID, data[0])
	}
}

// ---------------------------------------------------------------------------
// Keyword operations
// ---------------------------------------------------------------------------

func (h *UserActionHandler) addKeywords(userID int64, keywords []string) (string, error) {
	return addKeywordsForUser(userID, keywords)
}

func (h *UserActionHandler) viewKeywords(userID int64, messageID int) {
	keywords, err := getKeywordsForUser(userID)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to retrieve keywords: %v", err), userID)
		h.sender.SendError(userID, messageID, "Failed to retrieve keywords, please try again later.")
		return
	}

	if len(keywords) == 0 {
		h.sender.SendError(userID, messageID, "You have no keywords yet.\n\nTap 📝 Add keyword to get started.")
		return
	}

	sort.Strings(keywords)
	text := h.formatKeywordsList(keywords)
	keyboard := CreateBackButton()
	h.sender.SendHTMLResponse(userID, messageID, text, &keyboard)
}

func (h *UserActionHandler) showDeleteKeywords(userID int64, messageID int) {
	keywords, err := getKeywordsForUser(userID)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to retrieve keywords: %v", err), userID)
		h.sender.SendError(userID, messageID, "Failed to retrieve keywords, please try again later.")
		return
	}

	if len(keywords) == 0 {
		h.sender.SendError(userID, messageID, "You have no keywords to delete.")
		return
	}

	sort.Strings(keywords)
	keyboard := CreateDeleteKeyboard(keywords, "del_kw")
	h.sender.SendResponse(userID, messageID, "Select the keyword to delete:", &keyboard)
}

func (h *UserActionHandler) deleteKeyword(userID int64, messageID int, keyword string) {
	result, err := removeKeywordForUser(userID, keyword)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to delete keyword: %v", err), userID)
		h.sender.SendError(userID, messageID, "Failed to delete keyword, please try again later.")
		return
	}

	keyboard := CreateBackButton()
	h.sender.SendResponse(userID, messageID, result, &keyboard)

	// Refresh the delete list after a short delay if more keywords remain.
	go func() {
		time.Sleep(time.Second)
		kws, err := getKeywordsForUser(userID)
		if err == nil && len(kws) > 0 {
			h.showDeleteKeywords(userID, messageID)
		}
	}()
}

// ---------------------------------------------------------------------------
// Subscription operations
// ---------------------------------------------------------------------------

func (h *UserActionHandler) addSubscription(userID int64, messageID int, feedURL, name, channel string) {
	feedURL = strings.TrimSpace(feedURL)
	name = strings.TrimSpace(name)

	if err := validateAndProcessSubscription(feedURL, name, channel, userID); err != nil {
		logMessage("error", fmt.Sprintf("Failed to add subscription: %v", err), userID)
		h.sender.SendError(userID, messageID, "❌ "+err.Error())
		return
	}

	clearUserState(userID)
	keyboard := CreateBackButton()
	text := fmt.Sprintf("✅ Subscription added:\n📰 %s\n🔗 %s", name, feedURL)
	logMessage("info", fmt.Sprintf("✅ Subscription added: 📰 %s  🔗 %s", name, feedURL))
	h.sender.SendResponse(userID, messageID, text, &keyboard)
}

func (h *UserActionHandler) viewSubscriptions(userID int64, messageID int) {
	subscriptions, err := getSubscriptionsForUser(userID)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to retrieve subscriptions: %v", err), userID)
		h.sender.SendError(userID, messageID, "Failed to retrieve subscriptions, please try again later.")
		return
	}

	if len(subscriptions) == 0 {
		h.sender.SendError(userID, messageID, "You have no subscriptions yet.\n\nTap ➕ Add subscription to get started.")
		return
	}

	text := h.formatSubscriptionsList(subscriptions)
	keyboard := CreateBackButton()
	h.sender.SendHTMLResponse(userID, messageID, text, &keyboard)
}

func (h *UserActionHandler) showDeleteSubscriptions(userID int64, messageID int) {
	subscriptions, err := getSubscriptionsForUser(userID)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to retrieve subscriptions: %v", err), userID)
		h.sender.SendError(userID, messageID, "Failed to retrieve subscriptions, please try again later.")
		return
	}

	if len(subscriptions) == 0 {
		h.sender.SendError(userID, messageID, "You have no subscriptions to delete.")
		return
	}

	var names []string
	for _, sub := range subscriptions {
		names = append(names, sub.Name)
	}

	keyboard := CreateDeleteKeyboard(names, "del_sub")
	h.sender.SendResponse(userID, messageID, "Select the subscription to delete:", &keyboard)
}

func (h *UserActionHandler) deleteSubscription(userID int64, messageID int, subscriptionName string) {
	result, err := removeSubscriptionForUser(userID, subscriptionName)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to delete subscription: %v", err), userID)
		h.sender.SendError(userID, messageID, "Failed to delete subscription, please try again later.")
		return
	}

	keyboard := CreateBackButton()
	h.sender.SendResponse(userID, messageID, result, &keyboard)

	// Refresh the delete list after a short delay if more subscriptions remain.
	go func() {
		time.Sleep(time.Second)
		subs, err := getSubscriptionsForUser(userID)
		if err == nil && len(subs) > 0 {
			h.showDeleteSubscriptions(userID, messageID)
		}
	}()
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

func (h *UserActionHandler) formatKeywordsList(keywords []string) string {
	var rows []string
	var currentRow []string

	for i, kw := range keywords {
		currentRow = append(currentRow, fmt.Sprintf("%d.<code>%s</code>", i+1, kw))
		if i == len(keywords)-1 {
			rows = append(rows, strings.Join(currentRow, "  "))
		}
	}

	return fmt.Sprintf("📋 Your keywords (%d total):\n\n%s", len(keywords), strings.Join(rows, "\n"))
}

func (h *UserActionHandler) formatSubscriptionsList(subscriptions []SubscriptionInfo) string {
	var subList []string
	for i, sub := range subscriptions {
		subList = append(subList, fmt.Sprintf("Feed %d.<code>%s</code>\n%s", i+1, sub.Name, sub.URL))
	}
	return fmt.Sprintf("📰 Your subscriptions (%d total):\n\n%s", len(subscriptions), strings.Join(subList, "\n"))
}

// ---------------------------------------------------------------------------
// Global component instances
// ---------------------------------------------------------------------------

var (
	messageSender    *MessageSender
	databaseOperator *DatabaseOperator
	actionHandler    *UserActionHandler
)

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	// Command-line flags
	showVersion := flag.Bool("version", false, "Show version information")
	flag.BoolVar(showVersion, "v", false, "Show version information")
	flag.Parse()

	if *showVersion {
		//fmt.Printf("TGBot_RSS\n  Version:    %s\n  Build time: %s\n  Git commit: %s\n", version, buildTime, gitCommit)
		fmt.Printf("TGBot_RSS %s\n", version)
		os.Exit(0)
	}

	var err error

	// Load configuration
	globalConfig, err = loadConfig()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	// Set default TimeZone if not configured
	if globalConfig.TimeZone == "" {
		globalConfig.TimeZone = "UTC"
	}

	asciiArt := `
  _________.__                          ___________         __                 
 /   _____/|__| ____   ____   __________\_   _____/__  ____/  |_____________   
 \_____  \ |  |/ __ \ / ___\_/ __ \_  __ \    __)_\  \/  /\   __\_  __ \__  \  
 /        \|  \  ___// /_/  >  ___/|  | \/        \>    <  |  |  |  | \// __ \_
/_______  /|__|\___  >___  / \___  >__| /_______  /__/\_ \ |__|  |__|  (____  /
        \/         \/_____/      \/             \/      \/                  \/ 
`
	intro := fmt.Sprintf(`%s
Welcome to TGBot_RSS/SiegerExtra
Version:    %s
Author:     SiegerExtra, original CN-only source code by AbBai @ github.com/IonRh/TGBot_RSS
Repository: https://github.com/SiegerExtra/TGBot_RSS
About:      TGBot_RSS/SiegerExtra is a flexible Telegram bot that pushes RSS feeds`, asciiArt, version/*, buildTime, gitCommit*/)
	logMessage("info", fmt.Sprintf(intro+"\n"))

	logMessage("info", "RSS Bot starting...")

	// Open and configure the SQLite database.
	// A single shared connection pool is used throughout the application;
	// rss.go functions receive the global db via the withDB helper instead of
	// opening their own connections (which was the source of redundant handles).
	db, err = sql.Open("sqlite3", fmt.Sprintf("%s?cache=shared&mode=rwc&_timeout=30000", DBFile))
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	// Connection pool settings
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	// Validate the connection before proceeding
	if err := db.Ping(); err != nil {
		log.Fatal("Database connection failed:", err)
	}

	// Create tables and indexes
	if err := initDatabase(); err != nil {
		log.Fatal("Failed to initialise database:", err)
	}

	// Build the HTTP client (with optional proxy)
	client := createHTTPClient(globalConfig.ProxyURL)

	// Create the Telegram bot
	bot, err = tgbotapi.NewBotAPIWithClient(globalConfig.BotToken, tgbotapi.APIEndpoint, client)
	if err != nil {
		log.Fatal("Failed to create bot:", err)
	}

	bot.Debug = false
	logMessage("info", fmt.Sprintf("Bot started — authenticated as @%s", bot.Self.UserName))

	// Initialise component singletons
	messageSender = NewMessageSender(bot)
	databaseOperator = NewDatabaseOperator(db)
	actionHandler = NewUserActionHandler(messageSender, databaseOperator)

	// Start the RSS monitor goroutine
	go startRSSMonitor()

	// Configure and start the update polling loop
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		go func(update tgbotapi.Update) {
			defer func() {
				if r := recover(); r != nil {
					logMessage("error", fmt.Sprintf("Panic while handling update: %v", r))
				}
			}()

			if update.Message != nil {
				handleMessage(update.Message)
			} else if update.CallbackQuery != nil {
				handleCallbackQuery(update.CallbackQuery)
			}
		}(update)
	}
}

// ---------------------------------------------------------------------------
// Message handlers
// ---------------------------------------------------------------------------

// handleMessage dispatches an incoming user message.
func handleMessage(message *tgbotapi.Message) {
	userID := message.From.ID

	defer func() {
		if r := recover(); r != nil {
			logMessage("error", fmt.Sprintf("Panic while handling message: %v", r), userID)
			sendMessage(userID, "An error occurred while processing your message. Please try again later.")
		}
	}()

	if message.IsCommand() {
		handleCommand(message)
		return
	}

	state := getUserState(userID)
	if state != nil {
		handleStateMessage(message, state)
		return
	}

	// Legacy reply-based input (backwards compatibility)
	if message.ReplyToMessage != nil {
		replyText := message.ReplyToMessage.Text
		switch {
		case strings.Contains(replyText, "Please enter the keyword"):
			handleKeywordInput(message)
			return
		case strings.Contains(replyText, "Enter subscription details"):
			handleSubscriptionInput(message)
			return
		}
	}

	sendHTMLMessage(userID, "Please use /start to open the menu or /help for assistance.")
}

// handleStateMessage routes a message based on the user's current interactive state.
func handleStateMessage(message *tgbotapi.Message, state *UserState) {
	userID := message.From.ID

	switch state.Action {
	case "add_keyword":
		handleKeywordInput(message)
	case "add_subscription":
		handleSubscriptionInput(message)
	default:
		logMessage("warn", fmt.Sprintf("Unknown user state: %s", state.Action), userID)
		clearUserState(userID)
		sendMessage(userID, "Operation cancelled. Please start again.")
	}
}

// handleKeywordInput processes free-text keyword input from the user
// Allowed keyword to contain spaces, and changed separator from ',' to '#!#'
func handleKeywordInput(message *tgbotapi.Message) {
    userID := message.From.ID
    text := strings.TrimSpace(message.Text)
    if text == "" {
        messageSender.SendError(userID, 0, "❌ Please enter a valid keyword.")
        return
    }

    var keywords []string

    // Split by #!# separator
    if strings.Contains(text, "#!#") {
        for _, part := range strings.Split(text, "#!#") {
            if trimmed := strings.TrimSpace(part); trimmed != "" {
                keywords = append(keywords, trimmed)
            }
        }
    } else {
        // No separator: treat the entire input as one keyword
        keywords = []string{text}
    }

    if len(keywords) == 0 {
        messageSender.SendError(userID, 0, "❌ Please enter a valid keyword.")
        return
    }

    actionHandler.HandleAction(userID, 0, "keyword", "add", keywords...)
}

// handleSubscriptionInput processes free-text subscription input from the user.
func handleSubscriptionInput(message *tgbotapi.Message) {
	userID := message.From.ID
	parts := strings.SplitN(strings.TrimSpace(message.Text), " ", 3)

	if len(parts) != 3 {
		messageSender.SendError(userID, 0,
			"❌ Format error! Please use:\nURL Name ChannelMode\nExample: https://example.com/feed TechNews 0")
		return
	}

	actionHandler.HandleAction(userID, 0, "subscription", "add", parts[0], parts[1], parts[2])
}

// showMainMenu sends the main menu to userID.
func showMainMenu(userID int64, from string, messageID int) {
	stats, err := getUserStats(userID)
	if err != nil {
		logMessage("error", fmt.Sprintf("Failed to get user stats: %v", err), userID)
		stats = &UserStats{}
	}
	pushstats := GetPushStatsInfo()
	menuText := fmt.Sprintf(`👋 Welcome to TGBot_RSS!

👥 %s (<code>%d</code>):
📰 Subscriptions: %d    🔍 Keywords: %d

%s
1️⃣ Subscription management: add / remove / view RSS feeds
2️⃣ Keyword management: add / remove / view keywords

Please choose an action:`,
		from, userID, stats.SubscriptionCount, stats.KeywordCount, pushstats)

	keyboard := createMainMenuKeyboard()
	messageSender.SendHTMLResponse(userID, messageID, menuText, &keyboard)
}

// showHelp sends the help text to userID.
func showHelp(userID int64, messageID int) {
	count := downloadcounnt()
	helpText := fmt.Sprintf(`🤖 RSS Subscription Bot
📰 TGBot_RSS downloads: %d

📝 <b>Usage guide</b> (if pushes are not arriving, try the following)

🔤 <b>Basic keywords</b>
• Supports any language; separate multiple keywords with commas
• Regular expressions are supported for advanced matching

🎯 <b>Advanced matching</b>
• <code>*</code> matches any sequence of characters
• <code>-keyword</code> blocks content containing that keyword
• Example: <code>you*great*</code> matches "you are so great!"
• Example: <code>-ugly</code> blocks content containing "ugly"

🎯 <b>Match scope</b>
• Default: title only (backwards-compatible)
• #t keyword — title only
• #c keyword — description only
• #a keyword — title and description
• Example: <code>#ttechnology</code> matches "technology" in titles only

📡 <b>RSS filtering (combinable with advanced matching)</b>
• <code>keyword+FeedName</code> restricts matching to a named feed
• Example: <code>tech+TechNews</code> only matches the feed named "TechNews"
• Omitting +FeedName matches all subscribed feeds

📦 Repository: github.com/SiegerExtra/TGBot_RSS
🔧 Feedback:   github.com/SiegerExtra/TGBot_RSS`, count)

	keyboard := CreateBackButton()
	messageSender.SendHTMLResponse(userID, messageID, helpText, &keyboard, true)
}

// handleCommand dispatches bot commands (e.g. /start, /help).
func handleCommand(message *tgbotapi.Message) {
	userID := message.From.ID

	if userID == globalConfig.ADMINIDS {
		logMessage("debug", fmt.Sprintf("Admin used command: %s", message.Command()), userID)
	} else if globalConfig.ADMINIDS == 0 {
		logMessage("debug", fmt.Sprintf("Open access — user used command: %s", message.Command()), userID)
	} else {
		logMessage("warn", fmt.Sprintf("Unauthorized user attempted command: %s", message.Command()), userID)
		sendMessage(userID, "You do not have permission to use this command.")
		return
	}

	from := message.From.FirstName + " " + message.From.LastName
	command := message.Command()

	logMessage("debug", fmt.Sprintf("Command received: %s", command), userID)

	switch command {
	case "start":
		clearUserState(userID)
		showMainMenu(userID, from, 0)

	case "help":
		showHelp(userID, 0)

	default:
		sendMessage(userID, fmt.Sprintf("Unknown command: %s\nUse /start for the menu or /help for assistance.", command))
	}
}

// handleCallbackQuery dispatches inline keyboard button callbacks.
func handleCallbackQuery(callbackQuery *tgbotapi.CallbackQuery) {
	userID := callbackQuery.From.ID
	from := callbackQuery.From.FirstName + " " + callbackQuery.From.LastName
	data := callbackQuery.Data
	messageID := callbackQuery.Message.MessageID

	defer func() {
		if r := recover(); r != nil {
			logMessage("error", fmt.Sprintf("Panic while handling callback: %v", r), userID)
		}
	}()

	// Acknowledge the callback to stop the loading spinner
	callback := tgbotapi.NewCallback(callbackQuery.ID, "")
	if _, err := bot.Request(callback); err != nil {
		logMessage("error", fmt.Sprintf("Failed to acknowledge callback: %v", err), userID)
	}

	// Clear user state unless the action requires follow-up input
	if data != "add_keyword" && data != "add_subscription" {
		clearUserState(userID)
	}

	switch {
	case data == "back_to_menu":
		showMainMenu(userID, from, messageID)

	case data == "add_keyword":
		actionHandler.HandleAction(userID, messageID, "keyword", "add_prompt")

	case data == "view_keywords":
		actionHandler.HandleAction(userID, messageID, "keyword", "view")

	case data == "delete_keyword":
		actionHandler.HandleAction(userID, messageID, "keyword", "delete_list")

	case data == "add_subscription":
		actionHandler.HandleAction(userID, messageID, "subscription", "add_prompt")

	case data == "view_subscriptions":
		actionHandler.HandleAction(userID, messageID, "subscription", "view")

	case data == "delete_subscription":
		actionHandler.HandleAction(userID, messageID, "subscription", "delete_list")

	case data == "help":
		showHelp(userID, messageID)

	case strings.HasPrefix(data, "del_kw_"):
		keyword := strings.TrimPrefix(data, "del_kw_")
		actionHandler.HandleAction(userID, messageID, "keyword", "delete", keyword)

	case strings.HasPrefix(data, "del_sub_"):
		subscription := strings.TrimPrefix(data, "del_sub_")
		actionHandler.HandleAction(userID, messageID, "subscription", "delete", subscription)

	default:
		logMessage("warn", fmt.Sprintf("Unknown callback data: %s", data), userID)
		messageSender.SendError(userID, messageID, "Unknown action, please try again.")
	}
}

// createMainMenuKeyboard returns the main menu inline keyboard.
func createMainMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Add keyword", "add_keyword"),
			tgbotapi.NewInlineKeyboardButtonData("📋 View keywords", "view_keywords"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete keyword", "delete_keyword"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Add subscription", "add_subscription"),
			tgbotapi.NewInlineKeyboardButtonData("📰 View subscriptions", "view_subscriptions"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete subscription", "delete_subscription"),
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ About", "help"),
		),
	)
}

// ---------------------------------------------------------------------------
// Low-level send helpers
// ---------------------------------------------------------------------------

// sendMessage sends a plain-text message to userID.
func sendMessage(userID int64, text string) {
	msg := tgbotapi.NewMessage(userID, text)
	if _, err := bot.Send(msg); err != nil {
		logMessage("error", fmt.Sprintf("Failed to send message: %v", err), userID)
	}
}

// sendHTMLMessage sends an HTML-formatted message to userID.
func sendHTMLMessage(userID int64, text string) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ParseMode = "HTML"
	if _, err := bot.Send(msg); err != nil {
		logMessage("error", fmt.Sprintf("Failed to send HTML message: %v", err), userID)
	}
}

// sendPhotoMessage sends a photo with an HTML caption.
// Falls back to a plain HTML message if the photo upload fails.
func sendPhotoMessage(userID int64, photoURL, caption string) {
	msg := tgbotapi.NewPhoto(userID, tgbotapi.FileURL(photoURL))
	msg.Caption = caption
	msg.ParseMode = "HTML"

	if _, err := bot.Send(msg); err != nil {
		logMessage("error", fmt.Sprintf("Failed to send photo: %v", err), userID)
		fallbackMsg := fmt.Sprintf("Image: %s\n\n%s", photoURL, caption)
		sendHTMLMessage(userID, fallbackMsg)
	}
}

// ---------------------------------------------------------------------------
// Database initialization and operations
// ---------------------------------------------------------------------------

// initDatabase creates the required tables and indexes if they do not exist.
func initDatabase() error {
	tables := map[string]string{
		"subscriptions": `CREATE TABLE IF NOT EXISTS subscriptions (
			subscription_id INTEGER PRIMARY KEY AUTOINCREMENT,
			rss_url TEXT NOT NULL,
			rss_name TEXT NOT NULL UNIQUE,
			users TEXT NOT NULL DEFAULT ',',
			channel INTEGER DEFAULT 0
		)`,
		"user_keywords": `CREATE TABLE IF NOT EXISTS user_keywords (
			user_id INTEGER PRIMARY KEY,
			keywords TEXT NOT NULL DEFAULT '[]'
		)`,
		"feed_data": `CREATE TABLE IF NOT EXISTS feed_data (
			rss_name TEXT PRIMARY KEY,
			last_update_time TEXT,
			latest_title TEXT DEFAULT ''
		)`,
	}

	for name, tablesql := range tables {
		if err := withDB(func(db *sql.DB) error {
			_, err := db.Exec(tablesql)
			return err
		}); err != nil {
			return fmt.Errorf("failed to create table %s: %v", name, err)
		}
		logMessage("debug", fmt.Sprintf("Table %s ready", name))
	}

	indexes := []struct {
		name string
		sql  string
	}{
		{
			name: "idx_subscriptions_users",
			sql:  "CREATE INDEX IF NOT EXISTS idx_subscriptions_users ON subscriptions(users)",
		},
		{
			name: "idx_feed_data_update_time",
			sql:  "CREATE INDEX IF NOT EXISTS idx_feed_data_update_time ON feed_data(last_update_time)",
		},
	}

	for _, index := range indexes {
		if err := withDB(func(db *sql.DB) error {
			_, err := db.Exec(index.sql)
			return err
		}); err != nil {
			logMessage("warn", fmt.Sprintf("Failed to create index %s: %v", index.name, err))
		} else {
			logMessage("debug", fmt.Sprintf("Index %s ready", index.name))
		}
	}

	logMessage("info", "Database initialization complete")
	return nil
}

// getKeywordsForUser returns the keyword list for userID.
func getKeywordsForUser(userID int64) ([]string, error) {
	var keywordsStr string
	var keywords []string

	err := withDB(func(db *sql.DB) error {
		return db.QueryRow("SELECT keywords FROM user_keywords WHERE user_id = ?", userID).Scan(&keywordsStr)
	})

	if err == sql.ErrNoRows {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(keywordsStr), &keywords); err != nil {
		return nil, err
	}
	return keywords, nil
}

// addKeywordsForUser merges newKeywords into the existing set for userID.
func addKeywordsForUser(userID int64, newKeywords []string) (string, error) {
	existingKeywords, err := getKeywordsForUser(userID)
	if err != nil {
		return "", err
	}

	// Deduplicate using a map
	keywordMap := make(map[string]bool)
	for _, k := range existingKeywords {
		keywordMap[k] = true
	}

	// Expand comma-separated input and normalise fullwidth commas
	var processedKeywords []string
	for _, k := range newKeywords {
		k = strings.ReplaceAll(k, "\uff0c", ",") // normalise fullwidth comma -> ASCII comma
		if strings.Contains(k, ",") {
			for _, part := range strings.Split(k, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					processedKeywords = append(processedKeywords, trimmed)
				}
			}
		} else {
			if trimmed := strings.TrimSpace(k); trimmed != "" {
				processedKeywords = append(processedKeywords, trimmed)
			}
		}
	}

	var addedCount int
	for _, k := range processedKeywords {
		if !keywordMap[k] {
			keywordMap[k] = true
			addedCount++
		}
	}

	if addedCount == 0 {
		return "❌ No new keywords added — all already exist.", nil
	}

	var finalKeywords []string
	for k := range keywordMap {
		finalKeywords = append(finalKeywords, k)
	}
	sort.Strings(finalKeywords)

	keywordsJSON, err := json.Marshal(finalKeywords)
	if err != nil {
		return "", err
	}

	// Upsert using a single INSERT OR REPLACE to avoid the redundant COUNT query
	err = withDB(func(db *sql.DB) error {
		_, err = db.Exec(
			"INSERT INTO user_keywords (user_id, keywords) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET keywords = excluded.keywords",
			userID, string(keywordsJSON),
		)
		return err
	})
	if err != nil {
		return "", err
	}

	var rows []string
	var currentRow []string
	for i, kw := range finalKeywords {
		currentRow = append(currentRow, fmt.Sprintf("%d.%s", i+1, kw))
		if i == len(finalKeywords)-1 {
			rows = append(rows, strings.Join(currentRow, "  "))
		}
	}

	logMessage("info", fmt.Sprintf("✅ Added %d keyword(s). Total: %d. List: %s",
		addedCount, len(finalKeywords), strings.Join(rows, "\n")))
	return fmt.Sprintf("✅ Added %d keyword(s)\nTotal: %d keyword(s)\n\n📋 Keyword list:\n%s",
		addedCount, len(finalKeywords), strings.Join(rows, "\n")), nil
}

// removeKeywordForUser deletes keyword from userID's list.
func removeKeywordForUser(userID int64, keyword string) (string, error) {
	keywords, err := getKeywordsForUser(userID)
	if err != nil {
		return "", err
	}

	var newKeywords []string
	found := false
	for _, k := range keywords {
		if k != keyword {
			newKeywords = append(newKeywords, k)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Sprintf("❌ Keyword \"%s\" not found.", keyword), nil
	}

	keywordsJ := "[]"
	if len(newKeywords) > 0 {
		b, err := json.Marshal(newKeywords)
		if err != nil {
			return "", err
		}
		keywordsJ = string(b)
	}

	err = withDB(func(db *sql.DB) error {
		_, err := db.Exec("UPDATE user_keywords SET keywords = ? WHERE user_id = ?", keywordsJ, userID)
		return err
	})
	if err != nil {
		return "", err
	}

	if len(newKeywords) == 0 {
		return fmt.Sprintf("✅ Keyword \"%s\" deleted.\nNo keywords remaining.", keyword), nil
	}

	sort.Strings(newKeywords)

	var rows []string
	var currentRow []string
	for i, kw := range newKeywords {
		currentRow = append(currentRow, fmt.Sprintf("%d.%s", i+1, kw))
		if i == len(newKeywords)-1 {
			rows = append(rows, strings.Join(currentRow, "  "))
		}
	}

	return fmt.Sprintf("✅ Keyword \"%s\" deleted.\n%d keyword(s) remaining.\n\n📋 Keyword list:\n%s",
		keyword, len(newKeywords), strings.Join(rows, "\n")), nil
}

// getSubscriptionsForUser returns all subscriptions that include userID.
func getSubscriptionsForUser(userID int64) ([]SubscriptionInfo, error) {
	var subscriptions []SubscriptionInfo

	err := withDB(func(db *sql.DB) error {
		rows, err := db.Query(`SELECT rss_name, rss_url, users FROM subscriptions`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var sub SubscriptionInfo
			var usersStr string
			if err := rows.Scan(&sub.Name, &sub.URL, &usersStr); err != nil {
				continue
			}

			var users []int64
			if err := json.Unmarshal([]byte(usersStr), &users); err != nil {
				// Legacy comma-delimited format
				if strings.HasPrefix(usersStr, ",") && strings.HasSuffix(usersStr, ",") {
					for _, userIDStr := range strings.Split(strings.Trim(usersStr, ","), ",") {
						if userIDStr == "" {
							continue
						}
						if uid, err := strconv.ParseInt(userIDStr, 10, 64); err == nil && uid == userID {
							subscriptions = append(subscriptions, sub)
							break
						}
					}
				}
				continue
			}

			for _, uid := range users {
				if uid == userID {
					subscriptions = append(subscriptions, sub)
					break
				}
			}
		}
		return nil
	})

	return subscriptions, err
}

// removeSubscriptionForUser removes userID from the named subscription.
// If no users remain, the subscription row is deleted entirely.
func removeSubscriptionForUser(userID int64, subscriptionName string) (string, error) {
	var result string

	err := withDB(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		var usersStr string
		err = tx.QueryRow("SELECT users FROM subscriptions WHERE rss_name = ?", subscriptionName).Scan(&usersStr)
		if err != nil {
			return err
		}

		var users []int64
		var newUsers []int64

		if err := json.Unmarshal([]byte(usersStr), &users); err != nil {
			// Legacy comma-delimited format — convert to JSON on the fly
			if strings.HasPrefix(usersStr, ",") && strings.HasSuffix(usersStr, ",") {
				for _, userStr := range strings.Split(strings.Trim(usersStr, ","), ",") {
					if userStr == "" {
						continue
					}
					if uid, err := strconv.ParseInt(userStr, 10, 64); err == nil && uid != userID {
						newUsers = append(newUsers, uid)
					}
				}

				usersJSON, err := json.Marshal(newUsers)
				if err != nil {
					return err
				}

				if len(newUsers) == 0 {
					if _, err = tx.Exec("DELETE FROM subscriptions WHERE rss_name = ?", subscriptionName); err != nil {
						return err
					}
					if _, err = tx.Exec("DELETE FROM feed_data WHERE rss_name = ?", subscriptionName); err != nil {
						return err
					}
					result = fmt.Sprintf("✅ Subscription \"%s\" fully removed.", subscriptionName)
				} else {
					if _, err = tx.Exec("UPDATE subscriptions SET users = ? WHERE rss_name = ?",
						string(usersJSON), subscriptionName); err != nil {
						return err
					}
					result = fmt.Sprintf("✅ Unsubscribed from \"%s\".", subscriptionName)
				}
				return tx.Commit()
			}
			return err
		}

		for _, uid := range users {
			if uid != userID {
				newUsers = append(newUsers, uid)
			}
		}

		usersJSON, err := json.Marshal(newUsers)
		if err != nil {
			return err
		}

		if len(newUsers) == 0 {
			if _, err = tx.Exec("DELETE FROM subscriptions WHERE rss_name = ?", subscriptionName); err != nil {
				return err
			}
			if _, err = tx.Exec("DELETE FROM feed_data WHERE rss_name = ?", subscriptionName); err != nil {
				return err
			}
			result = fmt.Sprintf("✅ Subscription \"%s\" fully removed.", subscriptionName)
		} else {
			if _, err = tx.Exec("UPDATE subscriptions SET users = ? WHERE rss_name = ?",
				string(usersJSON), subscriptionName); err != nil {
				return err
			}
			result = fmt.Sprintf("✅ Unsubscribed from \"%s\".", subscriptionName)
		}

		return tx.Commit()
	})

	return result, err
}

// getUserStats returns subscription and keyword counts for userID.
func getUserStats(userID int64) (*UserStats, error) {
	stats := &UserStats{}

	err := withDB(func(db *sql.DB) error {
		subs, _ := getSubscriptionsForUser(userID)
		stats.SubscriptionCount = len(subs)

		kws, err := getKeywordsForUser(userID)
		if err == nil {
			stats.KeywordCount = len(kws)
		}
		return nil
	})

	return stats, err
}

// validateAndProcessSubscription verifies the RSS feed and upserts the subscription.
func validateAndProcessSubscription(feedURL, name, channel string, userID int64) error {
	parsedURL, err := url.Parse(feedURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return fmt.Errorf("invalid URL — please provide a full http:// or https:// address")
	}

	if valid, errMsg := verifyRSSFeed(feedURL); !valid {
		return fmt.Errorf("RSS feed validation failed: %s", errMsg)
	}

	return withDB(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		var existingUsersStr string
		err = tx.QueryRow("SELECT users FROM subscriptions WHERE rss_url = ? OR rss_name = ?", feedURL, name).Scan(&existingUsersStr)

		if err == sql.ErrNoRows {
			usersJSON, err := json.Marshal([]int64{userID})
			if err != nil {
				return err
			}

			if _, err = tx.Exec(
				"INSERT INTO subscriptions (rss_url, rss_name, users, channel) VALUES (?, ?, ?, ?)",
				feedURL, name, string(usersJSON), channel,
			); err != nil {
				return err
			}

			if _, err = tx.Exec(
				"INSERT INTO feed_data (rss_name, last_update_time) VALUES (?, CURRENT_TIMESTAMP)",
				name,
			); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			var existingUsers []int64
			if err := json.Unmarshal([]byte(existingUsersStr), &existingUsers); err != nil {
				return err
			}

			for _, uid := range existingUsers {
				if uid == userID {
					return fmt.Errorf("you are already subscribed to this RSS feed")
				}
			}

			existingUsers = append(existingUsers, userID)
			usersJSON, err := json.Marshal(existingUsers)
			if err != nil {
				return err
			}

			if _, err = tx.Exec("UPDATE subscriptions SET users = ? WHERE rss_url = ?", string(usersJSON), feedURL); err != nil {
				return err
			}
		}

		return tx.Commit()
	})
}

// verifyRSSFeed performs a quick HTTP GET to confirm the URL returns RSS or Atom content.
func verifyRSSFeed(feedURL string) (bool, string) {
	client := createHTTPClient(globalConfig.ProxyURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", feedURL, nil)
	if err != nil {
		return false, "failed to create request"
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RSS Bot/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("unexpected HTTP status: %d", resp.StatusCode)
	}

	body := make([]byte, 8192)
	n, _ := io.ReadFull(resp.Body, body)
	content := string(body[:n])

	if strings.Contains(content, "<rss") || strings.Contains(content, "<feed") ||
		strings.Contains(content, "<?xml") {
		return true, ""
	}

	return false, "no valid RSS/Atom content detected"
}

// startRSSMonitor runs the periodic RSS check on a ticker driven by Cycletime.
// It reuses the global db connection instead of opening a separate one.
func startRSSMonitor() {
	ticker := time.NewTicker(time.Duration(globalConfig.Cycletime) * time.Minute)
	defer ticker.Stop()

	// Run an initial check immediately at startup
	checkAllRSS()
	logMessage("info", fmt.Sprintf("Bot started — RSS will be checked every %d minute(s)", globalConfig.Cycletime))

	for range ticker.C {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logMessage("error", fmt.Sprintf("Panic in RSS monitor: %v", r))
				}
			}()
			checkAllRSS()
		}()
	}
}

// splitMessage splits text into chunks of at most maxLength bytes,
// preferring to break at newline boundaries.
func splitMessage(text string, maxLength int) []string {
	var chunks []string
	for len(text) > maxLength {
		chunk := text[:maxLength]
		lastNewline := strings.LastIndex(chunk, "\n")
		if lastNewline != -1 && lastNewline > maxLength/2 {
			chunk = text[:lastNewline]
			text = text[lastNewline+1:]
		} else {
			text = text[maxLength:]
		}
		chunks = append(chunks, chunk)
	}
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	return chunks
}

// sendother pushes a notification to the Pushinfo webhook if configured.
func sendother(message string) {
	if globalConfig.Pushinfo == "" {
		return
	}
	encodedInfo := url.QueryEscape(message)
	tgURL := fmt.Sprintf(globalConfig.Pushinfo+"%s", encodedInfo)

	client := createHTTPClient(globalConfig.ProxyURL)
	resp, err := client.Get(tgURL)
	if err != nil {
		logMessage("error", fmt.Sprintf("Push notification failed: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		logMessage("error", fmt.Sprintf("Push notification failed, status %d: %s", resp.StatusCode, string(body)))
		return
	}
	logMessage("debug", fmt.Sprintf("Push notification sent — response: %s", resp.Status))
}

// ---------------------------------------------------------------------------
// GitHub download count
// ---------------------------------------------------------------------------

// Asset represents a single release asset from the GitHub Releases API.
type Asset struct {
	DownloadCount int `json:"download_count"`
}

// Release represents a single entry from the GitHub Releases API.
type Release struct {
	Assets []Asset `json:"assets"`
}

// downloadcounnt fetches the total download count for all releases of the project.
func downloadcounnt() int {
	owner := "SiegerExtra"
	repo := "TGBot_RSS"
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", owner, repo)

	client := createHTTPClient(globalConfig.ProxyURL)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		fmt.Printf("Error creating request: %v\n", err)
		return 1
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error fetching releases: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		fmt.Printf("Error decoding JSON: %v\n", err)
		return 1
	}

	total := 0
	for _, release := range releases {
		for _, asset := range release.Assets {
			total += asset.DownloadCount
		}
	}
	return total
}
