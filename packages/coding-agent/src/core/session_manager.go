package ycode

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ycode/packages/coding-agent/src/core/compaction"
	"ycode/packages/llm/src"
)

// ============================================================================
// Constants & Types
// ============================================================================

const CurrentSessionVersion = 3

// SessionHeader is the first line of a session file.
type SessionHeader struct {
	Type          string `json:"type"`
	Version       int    `json:"version,omitempty"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
}

// NewSessionOptions for creating a new session.
type NewSessionOptions struct {
	ID             string
	ParentSession  string
}

// SessionContext holds the resolved messages and settings for the LLM.
type SessionContext struct {
	Messages      []llm.Message
	ThinkingLevel string
	Model         *SessionModel
}

// SessionModel holds the active model info.
type SessionModel struct {
	Provider string
	ModelID  string
}

// SessionTreeNode is a defensive copy of a node in the session tree.
type SessionTreeNode struct {
	Entry         *compaction.SessionEntry
	Children      []*SessionTreeNode
	Label         string
	LabelTimestamp string
}

// SessionInfo holds metadata about a session file.
type SessionInfo struct {
	Path             string
	ID               string
	Cwd              string
	Name             string
	ParentSessionPath string
	Created          time.Time
	Modified         time.Time
	MessageCount     int
	FirstMessage     string
	AllMessagesText  string
}

// SessionListProgress is a callback for listing progress.
type SessionListProgress func(loaded, total int)

// ============================================================================
// ID Generation
// ============================================================================

func createSessionId() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generateId(byID map[string]bool) string {
	for i := 0; i < 100; i++ {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		if !byID[id] {
			return id
		}
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ============================================================================
// Migration
// ============================================================================

func migrateV1ToV2(entries []json.RawMessage) {
	ids := make(map[string]bool)
	var prevID *string

	for i, raw := range entries {
		var partial struct{ Type string }
		if json.Unmarshal(raw, &partial) != nil || partial.Type == "session" {
			if partial.Type == "session" {
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil {
					m["version"] = 2
					entries[i], _ = json.Marshal(m)
				}
			}
			continue
		}
		id := generateId(ids)
		ids[id] = true

		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		m["id"] = id
		m["parentId"] = prevID

		// Convert firstKeptEntryIndex to firstKeptEntryId for compaction
		if m["type"] == "compaction" {
			if idx, ok := m["firstKeptEntryIndex"].(float64); ok {
				idxInt := int(idx)
				if idxInt < len(entries) {
					var target map[string]any
					if json.Unmarshal(entries[idxInt], &target) == nil && target["type"] != "session" {
						if tid, ok := target["id"]; ok {
							m["firstKeptEntryId"] = tid
						}
					}
				}
				delete(m, "firstKeptEntryIndex")
			}
		}

		entries[i], _ = json.Marshal(m)
		prevID = &id
	}
}

func migrateV2ToV3(entries []json.RawMessage) {
	for i, raw := range entries {
		var partial struct{ Type string }
		if json.Unmarshal(raw, &partial) != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if partial.Type == "session" {
			m["version"] = 3
			entries[i], _ = json.Marshal(m)
			continue
		}
		if partial.Type == "message" {
			if msg, ok := m["message"].(map[string]any); ok {
				if role, ok := msg["role"].(string); ok && role == "hookMessage" {
					msg["role"] = "custom"
					entries[i], _ = json.Marshal(m)
				}
			}
		}
	}
}

func migrateToCurrentVersion(entries []json.RawMessage) bool {
	var header struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
	}
	for _, raw := range entries {
		if json.Unmarshal(raw, &header) == nil && header.Type == "session" {
			break
		}
	}
	version := header.Version
	if version == 0 {
		version = 1
	}
	if version >= CurrentSessionVersion {
		return false
	}
	if version < 2 {
		migrateV1ToV2(entries)
	}
	if version < 3 {
		migrateV2ToV3(entries)
	}
	return true
}

// ============================================================================
// File Operations
// ============================================================================

// ParseSessionEntries parses JSONL content into raw entries.
func ParseSessionEntries(content string) []json.RawMessage {
	var entries []json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw json.RawMessage
		if json.Unmarshal([]byte(line), &raw) == nil {
			entries = append(entries, raw)
		}
	}
	return entries
}

// LoadEntriesFromFile loads and parses session entries from a JSONL file.
func LoadEntriesFromFile(filePath string) []json.RawMessage {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	entries := ParseSessionEntries(string(data))
	if len(entries) == 0 {
		return entries
	}
	// Validate session header
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(entries[0], &header) != nil || header.Type != "session" || header.ID == "" {
		return nil
	}
	return entries
}

func isValidSessionFile(filePath string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	firstLine := strings.SplitN(string(buf[:n]), "\n", 2)[0]
	if firstLine == "" {
		return false
	}
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	return json.Unmarshal([]byte(firstLine), &header) == nil && header.Type == "session" && header.ID != ""
}

// FindMostRecentSession finds the most recently modified session file in a directory.
func FindMostRecentSession(sessionDir string) string {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return ""
	}
	type fileStat struct {
		path  string
		mtime time.Time
	}
	var files []fileStat
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(sessionDir, e.Name())
		if !isValidSessionFile(p) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileStat{path: p, mtime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.After(files[j].mtime)
	})
	if len(files) == 0 {
		return ""
	}
	return files[0].path
}

func extractTextFromMessage(msg llm.Message) string {
	switch m := msg.(type) {
	case llm.UserMessage:
		var texts []string
		for _, b := range m.Content {
			if tc, ok := b.(llm.TextContent); ok {
				texts = append(texts, tc.Text)
			}
		}
		return strings.Join(texts, " ")
	case llm.AssistantMessage:
		var texts []string
		for _, b := range m.Content {
			if tc, ok := b.(llm.TextContent); ok {
				texts = append(texts, tc.Text)
			}
		}
		return strings.Join(texts, " ")
	}
	return ""
}

func getLastActivityTime(entries []json.RawMessage) time.Time {
	var last time.Time
	for _, raw := range entries {
		var partial struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &partial) != nil || partial.Type != "message" {
			continue
		}
		var msgEntry struct {
			Message struct {
				Role      string `json:"role"`
				Timestamp int64  `json:"timestamp"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &msgEntry) != nil {
			continue
		}
		role := msgEntry.Message.Role
		if role != string(llm.RoleUser) && role != string(llm.RoleAssistant) {
			continue
		}
		if msgEntry.Message.Timestamp > 0 {
			if t := time.UnixMilli(msgEntry.Message.Timestamp); t.After(last) {
				last = t
			}
		}
	}
	return last
}

func buildSessionInfo(filePath string) *SessionInfo {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	rawEntries := ParseSessionEntries(string(data))
	if len(rawEntries) == 0 {
		return nil
	}

	var header SessionHeader
	if json.Unmarshal(rawEntries[0], &header) != nil || header.Type != "session" {
		return nil
	}

	stat, err := os.Stat(filePath)
	if err != nil {
		return nil
	}

	var messageCount int
	var firstMessage string
	var allMessages []string
	var sessionName string

	for _, raw := range rawEntries {
		var partial struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &partial) != nil {
			continue
		}

		if partial.Type == "session_info" {
			var info struct{ Name string `json:"name"` }
			if json.Unmarshal(raw, &info) == nil {
				sessionName = strings.TrimSpace(info.Name)
			}
		}

		if partial.Type != "message" {
			continue
		}
		messageCount++

		var msgEntry struct {
			Message struct {
				Role      string `json:"role"`
				Timestamp int64  `json:"timestamp"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &msgEntry) != nil {
			continue
		}
		role := msgEntry.Message.Role
		if role != string(llm.RoleUser) && role != string(llm.RoleAssistant) {
			continue
		}
		// Extract text content (simplified — full parse would need full message decode)
		var full struct {
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &full) != nil {
			continue
		}
		var texts []string
		for _, c := range full.Message.Content {
			if c.Type == "text" {
				texts = append(texts, c.Text)
			}
		}
		textContent := strings.Join(texts, " ")
		if textContent == "" {
			continue
		}
		allMessages = append(allMessages, textContent)
		if firstMessage == "" && role == string(llm.RoleUser) {
			firstMessage = textContent
		}
	}

	created, _ := time.Parse(time.RFC3339, header.Timestamp)
	modified := getLastActivityTime(rawEntries)
	if modified.IsZero() {
		modified = stat.ModTime()
	}

	return &SessionInfo{
		Path:             filePath,
		ID:               header.ID,
		Cwd:              header.Cwd,
		Name:             sessionName,
		ParentSessionPath: header.ParentSession,
		Created:          created,
		Modified:         modified,
		MessageCount:     messageCount,
		FirstMessage:     firstMessage,
		AllMessagesText:   strings.Join(allMessages, " "),
	}
}

// ============================================================================
// buildSessionContext
// ============================================================================

// BuildSessionContext builds the session context from entries using tree traversal.
func BuildSessionContext(entries []*compaction.SessionEntry, leafID string, byID map[string]*compaction.SessionEntry) *SessionContext {
	if byID == nil {
		byID = make(map[string]*compaction.SessionEntry, len(entries))
		for _, e := range entries {
			byID[e.ID] = e
		}
	}

	if leafID == "" {
		// Explicitly empty — navigate to before first entry
		return &SessionContext{ThinkingLevel: "off"}
	}

	// Find leaf
	var leaf *compaction.SessionEntry
	if leafID != "" {
		leaf = byID[leafID]
	}
	if leaf == nil && len(entries) > 0 {
		leaf = entries[len(entries)-1]
	}
	if leaf == nil {
		return &SessionContext{ThinkingLevel: "off"}
	}

	// Walk from leaf to root
	var path []*compaction.SessionEntry
	current := leaf
	for current != nil {
		path = append([]*compaction.SessionEntry{current}, path...)
		if current.ParentID == "" {
			break
		}
		current = byID[current.ParentID]
	}

	// Extract settings and find compaction
	thinkingLevel := "off"
	var model *SessionModel
	var compactionEntry *compaction.SessionEntry

	for _, entry := range path {
		switch entry.Type {
		case compaction.EntryTypeThinkingLevelChange:
			thinkingLevel = entry.ThinkingLevel
		case compaction.EntryTypeModelChange:
			model = &SessionModel{Provider: entry.Provider, ModelID: entry.ModelID}
		case compaction.EntryTypeMessage:
			if am, ok := entry.Message.(llm.AssistantMessage); ok {
				model = &SessionModel{Provider: string(am.Provider), ModelID: am.Model}
			}
		case compaction.EntryTypeCompaction:
			compactionEntry = entry
		}
	}

	// Build messages
	var messages []llm.Message

	appendMessage := func(entry *compaction.SessionEntry) {
		switch entry.Type {
		case compaction.EntryTypeMessage:
			if entry.Message != nil {
				messages = append(messages, entry.Message)
			}
		case compaction.EntryTypeCustomMessage:
			msg := CreateCustomMessage(entry.CustomType, entry.Content, entry.Display, entry.Details, entry.Timestamp)
			messages = append(messages, msg)
		case compaction.EntryTypeBranchSummary:
			if entry.Summary != "" {
				msg := CreateBranchSummaryMessage(entry.Summary, entry.FromID, entry.Timestamp)
				messages = append(messages, msg)
			}
		}
	}

	if compactionEntry != nil {
		// Emit compaction summary first
		msg := CreateCompactionSummaryMessage(compactionEntry.Summary, compactionEntry.TokensBefore, compactionEntry.Timestamp)
		messages = append(messages, msg)

		// Find compaction index in path
		compactionIdx := -1
		for i, e := range path {
			if e.Type == compaction.EntryTypeCompaction && e.ID == compactionEntry.ID {
				compactionIdx = i
				break
			}
		}

		// Emit kept messages (before compaction, from firstKeptEntryId)
		foundFirstKept := false
		for i := 0; i < compactionIdx; i++ {
			if path[i].ID == compactionEntry.FirstKeptEntryID {
				foundFirstKept = true
			}
			if foundFirstKept {
				appendMessage(path[i])
			}
		}

		// Emit messages after compaction
		for i := compactionIdx + 1; i < len(path); i++ {
			appendMessage(path[i])
		}
	} else {
		for _, entry := range path {
			appendMessage(entry)
		}
	}

	return &SessionContext{Messages: messages, ThinkingLevel: thinkingLevel, Model: model}
}

// ============================================================================
// GetDefaultSessionDir
// ============================================================================

// GetDefaultSessionDir computes the session directory for a cwd.
func GetDefaultSessionDir(cwd, agentDir string) string {
	if agentDir == "" {
		agentDir = GetAgentDir()
	}
	safe := strings.TrimLeft(cwd, "/\\")
	safe = strings.TrimLeft(safe, "/\\")
	safe = strings.ReplaceAll(safe, "/", "-")
	safe = strings.ReplaceAll(safe, "\\", "-")
	safe = strings.ReplaceAll(safe, ":", "-")
	safe = fmt.Sprintf("--%s--", safe)
	dir := filepath.Join(agentDir, "sessions", safe)
	os.MkdirAll(dir, 0o755)
	return dir
}

// GetLatestCompactionEntry finds the most recent compaction entry.
func GetLatestCompactionEntry(entries []*compaction.SessionEntry) *compaction.SessionEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == compaction.EntryTypeCompaction {
			return entries[i]
		}
	}
	return nil
}

// ============================================================================
// SessionManager
// ============================================================================

// SessionManager manages conversation sessions as append-only trees in JSONL files.
type SessionManager struct {
	sessionID   string
	sessionFile string
	sessionDir  string
	cwd         string
	persist     bool
	flushed     bool

	fileEntries    []json.RawMessage
	byID           map[string]*compaction.SessionEntry
	labelsByID     map[string]string
	labelTimestamps map[string]string
	leafID         string
}

// NewSessionManager creates a new persisted session.
func NewSessionManager(cwd, sessionDir string) *SessionManager {
	if sessionDir == "" {
		sessionDir = GetDefaultSessionDir(cwd, "")
	}
	sm := &SessionManager{
		cwd:            cwd,
		sessionDir:     sessionDir,
		persist:        true,
		byID:           make(map[string]*compaction.SessionEntry),
		labelsByID:     make(map[string]string),
		labelTimestamps: make(map[string]string),
	}
	sm.newSession(nil)
	return sm
}

// OpenSessionManager opens a specific session file.
func OpenSessionManager(path, sessionDir, cwdOverride string) *SessionManager {
	rawEntries := LoadEntriesFromFile(path)
	var header SessionHeader
	for _, raw := range rawEntries {
		if json.Unmarshal(raw, &header) == nil && header.Type == "session" {
			break
		}
	}
	cwd := cwdOverride
	if cwd == "" {
		cwd = header.Cwd
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	dir := sessionDir
	if dir == "" {
		dir = filepath.Dir(path)
	}
	sm := &SessionManager{
		cwd:            cwd,
		sessionDir:     dir,
		persist:        true,
		byID:           make(map[string]*compaction.SessionEntry),
		labelsByID:     make(map[string]string),
		labelTimestamps: make(map[string]string),
	}
	sm.setSessionFile(path, rawEntries)
	return sm
}

// ContinueRecentSession continues the most recent session or creates new.
func ContinueRecentSession(cwd, sessionDir string) *SessionManager {
	dir := sessionDir
	if dir == "" {
		dir = GetDefaultSessionDir(cwd, "")
	}
	mostRecent := FindMostRecentSession(dir)
	if mostRecent != "" {
		return OpenSessionManager(mostRecent, dir, cwd)
	}
	return NewSessionManager(cwd, dir)
}

// NewInMemorySession creates a non-persisted session.
func NewInMemorySession(cwd string) *SessionManager {
	sm := &SessionManager{
		cwd:            cwd,
		sessionDir:     "",
		persist:        false,
		byID:           make(map[string]*compaction.SessionEntry),
		labelsByID:     make(map[string]string),
		labelTimestamps: make(map[string]string),
	}
	sm.newSession(nil)
	return sm
}

// ForkSession creates a new session with history from a source session.
func ForkSession(sourcePath, targetCwd, sessionDir string) (*SessionManager, error) {
	sourceEntries := LoadEntriesFromFile(sourcePath)
	if len(sourceEntries) == 0 {
		return nil, fmt.Errorf("cannot fork: source session file is empty or invalid: %s", sourcePath)
	}
	var sourceHeader SessionHeader
	for _, raw := range sourceEntries {
		if json.Unmarshal(raw, &sourceHeader) == nil && sourceHeader.Type == "session" {
			break
		}
	}
	if sourceHeader.ID == "" {
		return nil, fmt.Errorf("cannot fork: source session has no header: %s", sourcePath)
	}

	dir := sessionDir
	if dir == "" {
		dir = GetDefaultSessionDir(targetCwd, "")
	}
	os.MkdirAll(dir, 0o755)

	newID := createSessionId()
	timestamp := time.Now().Format(time.RFC3339)
	fileTimestamp := strings.ReplaceAll(strings.ReplaceAll(timestamp, ":", "-"), ".", "-")
	newFile := filepath.Join(dir, fmt.Sprintf("%s_%s.jsonl", fileTimestamp, newID))

	newHeader := SessionHeader{
		Type: "session", Version: CurrentSessionVersion,
		ID: newID, Timestamp: timestamp, Cwd: targetCwd, ParentSession: sourcePath,
	}
	headerJSON, _ := json.Marshal(newHeader)
	if err := os.WriteFile(newFile, []byte(string(headerJSON)+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("cannot fork: %w", err)
	}

	// Append non-header entries
	f, err := os.OpenFile(newFile, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	for _, raw := range sourceEntries {
		var partial struct{ Type string }
		if json.Unmarshal(raw, &partial) == nil && partial.Type != "session" {
			f.Write(raw)
			f.Write([]byte("\n"))
		}
	}

	return OpenSessionManager(newFile, dir, targetCwd), nil
}

// ============================================================================
// SessionManager: Internal Methods
// ============================================================================

func (sm *SessionManager) setSessionFile(path string, rawEntries []json.RawMessage) {
	sm.sessionFile, _ = filepath.Abs(path)
	if rawEntries == nil {
		rawEntries = LoadEntriesFromFile(sm.sessionFile)
	}

	if len(rawEntries) == 0 {
		explicitPath := sm.sessionFile
		sm.newSession(nil)
		sm.sessionFile = explicitPath
		sm.rewriteFile()
		sm.flushed = true
		return
	}

	var header SessionHeader
	for _, raw := range rawEntries {
		if json.Unmarshal(raw, &header) == nil && header.Type == "session" {
			break
		}
	}
	sm.sessionID = header.ID
	if sm.sessionID == "" {
		sm.sessionID = createSessionId()
	}

	if migrateToCurrentVersion(rawEntries) {
		sm.fileEntries = rawEntries
		sm.rewriteFile()
	} else {
		sm.fileEntries = rawEntries
	}

	sm.buildIndex()
	sm.flushed = true
}

func (sm *SessionManager) newSession(options *NewSessionOptions) {
	sm.sessionID = createSessionId()
	if options != nil && options.ID != "" {
		sm.sessionID = options.ID
	}
	timestamp := time.Now().Format(time.RFC3339)
	parentSession := ""
	if options != nil {
		parentSession = options.ParentSession
	}
	header := SessionHeader{
		Type: "session", Version: CurrentSessionVersion,
		ID: sm.sessionID, Timestamp: timestamp, Cwd: sm.cwd, ParentSession: parentSession,
	}
	headerJSON, _ := json.Marshal(header)
	sm.fileEntries = []json.RawMessage{headerJSON}
	sm.byID = make(map[string]*compaction.SessionEntry)
	sm.labelsByID = make(map[string]string)
	sm.labelTimestamps = make(map[string]string)
	sm.leafID = ""
	sm.flushed = false

	if sm.persist {
		fileTimestamp := strings.ReplaceAll(strings.ReplaceAll(timestamp, ":", "-"), ".", "-")
		sm.sessionFile = filepath.Join(sm.getSessionDir(), fmt.Sprintf("%s_%s.jsonl", fileTimestamp, sm.sessionID))
	}
}

// NewSessionEntry is the public wrapper for newSession.
// Used by AgentSessionRuntime.NewSession and AgentSessionRuntime.Fork.
func (sm *SessionManager) NewSessionEntry(parentSession string) {
	sm.newSession(&NewSessionOptions{ParentSession: parentSession})
}

func (sm *SessionManager) buildIndex() {
	sm.byID = make(map[string]*compaction.SessionEntry)
	sm.labelsByID = make(map[string]string)
	sm.labelTimestamps = make(map[string]string)
	sm.leafID = ""

	for _, raw := range sm.fileEntries {
		var partial struct{ Type string }
		if json.Unmarshal(raw, &partial) != nil || partial.Type == "session" {
			continue
		}
		entry, err := decodeSessionEntry(raw)
		if err != nil {
			continue
		}
		sm.byID[entry.ID] = entry
		sm.leafID = entry.ID

		if entry.Type == compaction.EntryTypeLabel {
			if entry.Label != "" {
				sm.labelsByID[entry.TargetID] = entry.Label
				sm.labelTimestamps[entry.TargetID] = fmt.Sprintf("%d", entry.Timestamp)
			} else {
				delete(sm.labelsByID, entry.TargetID)
				delete(sm.labelTimestamps, entry.TargetID)
			}
		}
	}
}

func (sm *SessionManager) rewriteFile() {
	if !sm.persist || sm.sessionFile == "" {
		return
	}
	var lines []string
	for _, e := range sm.fileEntries {
		lines = append(lines, string(e))
	}
	os.WriteFile(sm.sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func (sm *SessionManager) persistEntry(entry *compaction.SessionEntry) {
	if !sm.persist || sm.sessionFile == "" {
		return
	}
	// Don't persist until we have at least one assistant message
	hasAssistant := false
	for _, raw := range sm.fileEntries {
		var partial struct {
			Type    string `json:"type"`
			Message struct{ Role string `json:"role"` } `json:"message"`
		}
		if json.Unmarshal(raw, &partial) == nil && partial.Type == "message" && partial.Message.Role == string(llm.RoleAssistant) {
			hasAssistant = true
			break
		}
	}
	if !hasAssistant {
		sm.flushed = false
		return
	}

	f, err := os.OpenFile(sm.sessionFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	if !sm.flushed {
		for _, e := range sm.fileEntries {
			f.Write(e)
			f.Write([]byte("\n"))
		}
		sm.flushed = true
	} else {
		data, _ := json.Marshal(entry)
		f.Write(data)
		f.Write([]byte("\n"))
	}
}

func (sm *SessionManager) appendEntry(entry *compaction.SessionEntry) {
	data, _ := json.Marshal(entry)
	sm.fileEntries = append(sm.fileEntries, data)
	sm.byID[entry.ID] = entry
	sm.leafID = entry.ID
	sm.persistEntry(entry)
}

// ============================================================================
// SessionManager: Getters
// ============================================================================

func (sm *SessionManager) IsPersisted() bool     { return sm.persist }
func (sm *SessionManager) GetCwd() string         { return sm.cwd }
func (sm *SessionManager) GetSessionDir() string  { return sm.sessionDir }
func (sm *SessionManager) GetSessionID() string   { return sm.sessionID }
func (sm *SessionManager) GetSessionFile() string { return sm.sessionFile }
func (sm *SessionManager) GetLeafId() string      { return sm.leafID }

func (sm *SessionManager) GetLeafEntry() *compaction.SessionEntry {
	if sm.leafID == "" {
		return nil
	}
	return sm.byID[sm.leafID]
}

func (sm *SessionManager) GetEntry(id string) *compaction.SessionEntry {
	return sm.byID[id]
}

func (sm *SessionManager) GetLabel(id string) string {
	return sm.labelsByID[id]
}

func (sm *SessionManager) GetChildren(parentID string) []*compaction.SessionEntry {
	var children []*compaction.SessionEntry
	for _, entry := range sm.byID {
		if entry.ParentID == parentID {
			children = append(children, entry)
		}
	}
	return children
}

func (sm *SessionManager) GetHeader() *SessionHeader {
	for _, raw := range sm.fileEntries {
		var header SessionHeader
		if json.Unmarshal(raw, &header) == nil && header.Type == "session" {
			return &header
		}
	}
	return nil
}

func (sm *SessionManager) GetEntries() []*compaction.SessionEntry {
	var entries []*compaction.SessionEntry
	for _, raw := range sm.fileEntries {
		var partial struct{ Type string }
		if json.Unmarshal(raw, &partial) != nil || partial.Type == "session" {
			continue
		}
		entry, err := decodeSessionEntry(raw)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func (sm *SessionManager) GetSessionName() string {
	entries := sm.GetEntries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == compaction.EntryTypeSessionInfo {
			name := strings.TrimSpace(entries[i].Name)
			if name != "" {
				return name
			}
			return ""
		}
	}
	return ""
}

// ============================================================================
// SessionManager: Tree Traversal
// ============================================================================

// GetBranch walks from entry to root, returning entries in path order.
func (sm *SessionManager) GetBranch(fromID string) []*compaction.SessionEntry {
	startID := fromID
	if startID == "" {
		startID = sm.leafID
	}
	var path []*compaction.SessionEntry
	current := sm.byID[startID]
	for current != nil {
		path = append([]*compaction.SessionEntry{current}, path...)
		if current.ParentID == "" {
			break
		}
		current = sm.byID[current.ParentID]
	}
	return path
}

// BuildSessionContext builds the context (messages + settings) for the LLM.
func (sm *SessionManager) BuildSessionContext() *SessionContext {
	return BuildSessionContext(sm.GetEntries(), sm.leafID, sm.byID)
}

// GetTree returns the session as a tree structure.
func (sm *SessionManager) GetTree() []*SessionTreeNode {
	entries := sm.GetEntries()
	nodeMap := make(map[string]*SessionTreeNode, len(entries))
	var roots []*SessionTreeNode

	for _, entry := range entries {
		node := &SessionTreeNode{
			Entry:         entry,
			Label:         sm.labelsByID[entry.ID],
			LabelTimestamp: sm.labelTimestamps[entry.ID],
		}
		nodeMap[entry.ID] = node
	}

	for _, entry := range entries {
		node := nodeMap[entry.ID]
		if entry.ParentID == "" || entry.ParentID == entry.ID {
			roots = append(roots, node)
		} else {
			parent := nodeMap[entry.ParentID]
			if parent != nil {
				parent.Children = append(parent.Children, node)
			} else {
				roots = append(roots, node)
			}
		}
	}

	// Sort children by timestamp
	stack := make([]*SessionTreeNode, len(roots))
	copy(stack, roots)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		sort.Slice(n.Children, func(i, j int) bool {
			return n.Children[i].Entry.Timestamp < n.Children[j].Entry.Timestamp
		})
		stack = append(stack, n.Children...)
	}

	return roots
}

// ============================================================================
// SessionManager: Append Methods
// ============================================================================

// AppendMessage appends a message as child of current leaf.
func (sm *SessionManager) AppendMessage(message llm.Message) string {
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	entry := &compaction.SessionEntry{
		Type:      compaction.EntryTypeMessage,
		ID:        generateId(byIDSet),
		ParentID:  sm.leafID,
		Timestamp: time.Now().UnixMilli(),
		Message:   message,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendThinkingLevelChange appends a thinking level change.
func (sm *SessionManager) AppendThinkingLevelChange(thinkingLevel string) string {
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	entry := &compaction.SessionEntry{
		Type:          compaction.EntryTypeThinkingLevelChange,
		ID:            generateId(byIDSet),
		ParentID:      sm.leafID,
		Timestamp:     time.Now().UnixMilli(),
		ThinkingLevel: thinkingLevel,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendModelChange appends a model change.
func (sm *SessionManager) AppendModelChange(provider, modelID string) string {
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	entry := &compaction.SessionEntry{
		Type:      compaction.EntryTypeModelChange,
		ID:        generateId(byIDSet),
		ParentID:  sm.leafID,
		Timestamp: time.Now().UnixMilli(),
		Provider:  provider,
		ModelID:   modelID,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendCompaction appends a compaction summary.
func (sm *SessionManager) AppendCompaction(summary, firstKeptEntryID string, tokensBefore int, details any, fromHook bool) string {
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	entry := &compaction.SessionEntry{
		Type:             compaction.EntryTypeCompaction,
		ID:               generateId(byIDSet),
		ParentID:         sm.leafID,
		Timestamp:        time.Now().UnixMilli(),
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		Details:          details,
		FromHook:         fromHook,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendBranchSummary appends a branch summary.
func (sm *SessionManager) AppendBranchSummary(fromID, summary string, details any, fromHook bool) string {
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	from := fromID
	if from == "" {
		from = "root"
	}
	entry := &compaction.SessionEntry{
		Type:      compaction.EntryTypeBranchSummary,
		ID:        generateId(byIDSet),
		ParentID:  sm.leafID,
		Timestamp: time.Now().UnixMilli(),
		FromID:    from,
		Summary:   summary,
		Details:   details,
		FromHook:  fromHook,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendCustomEntry appends a custom entry for extensions.
func (sm *SessionManager) AppendCustomEntry(customType string, data any) string {
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	entry := &compaction.SessionEntry{
		Type:       compaction.EntryTypeCustom,
		ID:         generateId(byIDSet),
		ParentID:   sm.leafID,
		Timestamp:  time.Now().UnixMilli(),
		CustomType: customType,
		Data:       data,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendCustomMessageEntry appends a custom message that participates in LLM context.
func (sm *SessionManager) AppendCustomMessageEntry(customType string, content any, display bool, details any) string {
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	entry := &compaction.SessionEntry{
		Type:       compaction.EntryTypeCustomMessage,
		ID:         generateId(byIDSet),
		ParentID:   sm.leafID,
		Timestamp:  time.Now().UnixMilli(),
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendSessionInfo appends a session info entry.
func (sm *SessionManager) AppendSessionInfo(name string) string {
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	entry := &compaction.SessionEntry{
		Type:      compaction.EntryTypeSessionInfo,
		ID:        generateId(byIDSet),
		ParentID:  sm.leafID,
		Timestamp: time.Now().UnixMilli(),
		Name:      strings.TrimSpace(name),
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendLabelChange sets or clears a label on an entry.
func (sm *SessionManager) AppendLabelChange(targetID, label string) string {
	if _, ok := sm.byID[targetID]; !ok {
		panic(fmt.Sprintf("Entry %s not found", targetID))
	}
	byIDSet := make(map[string]bool, len(sm.byID))
	for id := range sm.byID {
		byIDSet[id] = true
	}
	entry := &compaction.SessionEntry{
		Type:      compaction.EntryTypeLabel,
		ID:        generateId(byIDSet),
		ParentID:  sm.leafID,
		Timestamp: time.Now().UnixMilli(),
		TargetID:  targetID,
		Label:     label,
	}
	sm.appendEntry(entry)
	if label != "" {
		sm.labelsByID[targetID] = label
		sm.labelTimestamps[targetID] = fmt.Sprintf("%d", entry.Timestamp)
	} else {
		delete(sm.labelsByID, targetID)
		delete(sm.labelTimestamps, targetID)
	}
	return entry.ID
}

// ============================================================================
// SessionManager: Branching
// ============================================================================

// Branch starts a new branch from an earlier entry.
func (sm *SessionManager) Branch(branchFromID string) {
	if _, ok := sm.byID[branchFromID]; !ok {
		panic(fmt.Sprintf("Entry %s not found", branchFromID))
	}
	sm.leafID = branchFromID
}

// ResetLeaf resets the leaf pointer to null (before any entries).
func (sm *SessionManager) ResetLeaf() {
	sm.leafID = ""
}

// BranchWithSummary branches and appends a branch summary.
func (sm *SessionManager) BranchWithSummary(branchFromID string, summary string, details any, fromHook bool) string {
	if branchFromID != "" {
		if _, ok := sm.byID[branchFromID]; !ok {
			panic(fmt.Sprintf("Entry %s not found", branchFromID))
		}
	}
	sm.leafID = branchFromID
	return sm.AppendBranchSummary(branchFromID, summary, details, fromHook)
}

// CreateBranchedSession creates a new session with only the path to the given leaf.
func (sm *SessionManager) CreateBranchedSession(leafID string) string {
	path := sm.GetBranch(leafID)
	if len(path) == 0 {
		panic(fmt.Sprintf("Entry %s not found", leafID))
	}

	// Filter out labels
	var pathNoLabels []*compaction.SessionEntry
	for _, e := range path {
		if e.Type != compaction.EntryTypeLabel {
			pathNoLabels = append(pathNoLabels, e)
		}
	}

	newID := createSessionId()
	timestamp := time.Now().Format(time.RFC3339)
	fileTimestamp := strings.ReplaceAll(strings.ReplaceAll(timestamp, ":", "-"), ".", "-")
	newFile := filepath.Join(sm.getSessionDir(), fmt.Sprintf("%s_%s.jsonl", fileTimestamp, newID))

	var parentSession string
	if sm.persist {
		parentSession = sm.sessionFile
	}
	header := SessionHeader{
		Type: "session", Version: CurrentSessionVersion,
		ID: newID, Timestamp: timestamp, Cwd: sm.cwd, ParentSession: parentSession,
	}

	// Collect labels for path entries
	pathIDs := make(map[string]bool, len(pathNoLabels))
	for _, e := range pathNoLabels {
		pathIDs[e.ID] = true
	}
	type labelInfo struct{ targetID, label, timestamp string }
	var labelsToWrite []labelInfo
	for targetID, label := range sm.labelsByID {
		if pathIDs[targetID] {
			labelsToWrite = append(labelsToWrite, labelInfo{targetID, label, sm.labelTimestamps[targetID]})
		}
	}

	// Build new entries
	var newFileEntries []json.RawMessage
	headerJSON, _ := json.Marshal(header)
	newFileEntries = append(newFileEntries, headerJSON)
	for _, e := range pathNoLabels {
		data, _ := json.Marshal(e)
		newFileEntries = append(newFileEntries, data)
	}

	// Build label entries
	allIDs := make(map[string]bool, len(pathIDs))
	for id := range pathIDs {
		allIDs[id] = true
	}
	var lastEntryID string
	if len(pathNoLabels) > 0 {
		lastEntryID = pathNoLabels[len(pathNoLabels)-1].ID
	}
	parentID := lastEntryID
	for _, lbl := range labelsToWrite {
		entry := &compaction.SessionEntry{
			Type: compaction.EntryTypeLabel,
			ID: generateId(allIDs),
			ParentID: parentID,
			Timestamp: 0, // Use original timestamp if available
			TargetID: lbl.targetID,
			Label: lbl.label,
		}
		allIDs[entry.ID] = true
		data, _ := json.Marshal(entry)
		newFileEntries = append(newFileEntries, data)
		parentID = entry.ID
	}

	// Set new session
	sm.fileEntries = newFileEntries
	sm.sessionID = newID
	sm.sessionFile = newFile
	sm.buildIndex()

	// Write file if has assistant message
	hasAssistant := false
	for _, e := range pathNoLabels {
		if e.Type == compaction.EntryTypeMessage {
			if _, ok := e.Message.(llm.AssistantMessage); ok {
				hasAssistant = true
				break
			}
		}
	}
	if hasAssistant {
		sm.rewriteFile()
		sm.flushed = true
	} else {
		sm.flushed = false
	}

	return newFile
}

// ============================================================================
// SessionManager: List Sessions
// ============================================================================

// ListSessions lists all sessions for a directory.
func ListSessions(cwd, sessionDir string) []*SessionInfo {
	dir := sessionDir
	if dir == "" {
		dir = GetDefaultSessionDir(cwd, "")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var sessions []*SessionInfo
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if !isValidSessionFile(p) {
			continue
		}
		info := buildSessionInfo(p)
		if info != nil {
			sessions = append(sessions, info)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified.After(sessions[j].Modified)
	})
	return sessions
}

// ListAllSessions lists all sessions across all project directories.
func ListAllSessions() []*SessionInfo {
	sessionsDir := GetSessionDir()
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}
	var sessions []*SessionInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(sessionsDir, e.Name())
		dirEntries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range dirEntries {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			p := filepath.Join(dir, f.Name())
			if !isValidSessionFile(p) {
				continue
			}
			info := buildSessionInfo(p)
			if info != nil {
				sessions = append(sessions, info)
			}
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified.After(sessions[j].Modified)
	})
	return sessions
}

func (sm *SessionManager) getSessionDir() string {
	return sm.sessionDir
}

/*
Key notes:

Uses compaction.SessionEntry — the flat struct from the compaction package. All entry types share this struct with different fields populated based on Type.
Implements compaction.ReadonlySessionManager — GetBranch and GetEntry methods satisfy the interface.
JSONL storage — entries stored as json.RawMessage to preserve exact JSON, only decoded to SessionEntry when needed for tree traversal.
Migration — migrateV1ToV2 and migrateV2ToV3 handle version upgrades by manipulating raw JSON.
NewBranchSummaryMessage/NewCompactionSummaryMessage/NewCustomMessage — these are from messages.go. Verify the function names match (could be Create... instead of New...).
GetSessionsDir — helper that likely needs to come from configuration.go. If it doesn't exist, add: func GetSessionsDir() string { return filepath.Join(GetAgentDir(), "sessions") }.
Timestamps — TS uses ISO strings (new Date().toISOString()), Go uses time.Now().UnixMilli() for entry timestamps and RFC3339 for headers. The SessionEntry.Timestamp is int64 (millis).
No uuid dependency — uses crypto/rand for ID generation instead.
*/