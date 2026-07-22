package rooms

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"time"
	"unicode"

	"PartyLAN/internal/gamecast"
)

const MaxHistoryEntries = 20
const MaxPersonalRoomIDRunes = 120

func GenerateRoomID(user string) string {
	prefix := slug(user)
	if prefix == "" || prefix == "guest" {
		prefix = "room"
	}
	return prefix + "-" + randomSuffix()
}

func PersonalRoomID(user string) string {
	roomID := strings.TrimSpace(user)
	if roomID == "" || strings.EqualFold(roomID, "guest") {
		return ""
	}
	roomID = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t':
			return '-'
		default:
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}
	}, roomID)
	runes := []rune(roomID)
	if len(runes) > MaxPersonalRoomIDRunes {
		roomID = string(runes[:MaxPersonalRoomIDRunes])
	}
	return strings.TrimSpace(roomID)
}

func AddHistory(history []gamecast.HistoryEntry, roomID string, now time.Time) []gamecast.HistoryEntry {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return NormalizeHistory(history)
	}
	next := make([]gamecast.HistoryEntry, 0, len(history)+1)
	next = append(next, gamecast.HistoryEntry{
		ID:   roomID,
		Time: now.UnixMilli(),
	})
	for _, entry := range history {
		if strings.EqualFold(strings.TrimSpace(entry.ID), roomID) {
			continue
		}
		if strings.TrimSpace(entry.ID) == "" {
			continue
		}
		next = append(next, entry)
	}
	return NormalizeHistory(next)
}

func MergeHistory(primary, secondary []gamecast.HistoryEntry) []gamecast.HistoryEntry {
	seen := map[string]bool{}
	merged := make([]gamecast.HistoryEntry, 0, len(primary)+len(secondary))
	for _, source := range [][]gamecast.HistoryEntry{primary, secondary} {
		for _, entry := range source {
			id := strings.TrimSpace(entry.ID)
			if id == "" {
				continue
			}
			key := strings.ToLower(id)
			if seen[key] {
				continue
			}
			seen[key] = true
			entry.ID = id
			merged = append(merged, entry)
		}
	}
	return NormalizeHistory(merged)
}

func NormalizeHistory(history []gamecast.HistoryEntry) []gamecast.HistoryEntry {
	seen := map[string]bool{}
	out := make([]gamecast.HistoryEntry, 0, len(history))
	for _, entry := range history {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if seen[key] {
			continue
		}
		seen[key] = true
		entry.ID = id
		out = append(out, entry)
		if len(out) >= MaxHistoryEntries {
			break
		}
	}
	return out
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func randomSuffix() string {
	var raw [5]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(time.Now().Format("150405")))[:6]
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))[:6]
}
