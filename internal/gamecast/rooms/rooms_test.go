package rooms

import (
	"strings"
	"testing"
	"time"

	"PartyLAN/internal/gamecast"
)

func TestGenerateRoomID(t *testing.T) {
	got := GenerateRoomID("Ashen Player")
	if !strings.HasPrefix(got, "ashen-player-") {
		t.Fatalf("GenerateRoomID prefix=%q", got)
	}
	if len(got) <= len("ashen-player-") {
		t.Fatalf("GenerateRoomID missing suffix: %q", got)
	}
}

func TestGenerateRoomIDFallsBackForNonASCII(t *testing.T) {
	got := GenerateRoomID("玩家")
	if !strings.HasPrefix(got, "room-") {
		t.Fatalf("GenerateRoomID=%q want room-*", got)
	}
}

func TestPersonalRoomIDUsesUserID(t *testing.T) {
	got := PersonalRoomID(" Ashen_123 ")
	if got != "Ashen_123" {
		t.Fatalf("PersonalRoomID=%q", got)
	}
}

func TestPersonalRoomIDRejectsGuest(t *testing.T) {
	if got := PersonalRoomID("guest"); got != "" {
		t.Fatalf("PersonalRoomID guest=%q", got)
	}
}

func TestAddHistoryMovesRoomToFront(t *testing.T) {
	now := time.Unix(10, 0)
	got := AddHistory([]gamecast.HistoryEntry{
		{ID: "old", Time: 1},
		{ID: "room-1", Time: 2},
	}, "room-1", now)

	if len(got) != 2 {
		t.Fatalf("history len=%d", len(got))
	}
	if got[0].ID != "room-1" || got[0].Time != now.UnixMilli() {
		t.Fatalf("first history entry=%#v", got[0])
	}
	if got[1].ID != "old" {
		t.Fatalf("second history entry=%#v", got[1])
	}
}

func TestMergeHistoryDeduplicates(t *testing.T) {
	got := MergeHistory(
		[]gamecast.HistoryEntry{{ID: "room-1", Time: 3}},
		[]gamecast.HistoryEntry{{ID: "ROOM-1", Time: 2}, {ID: "room-2", Time: 1}},
	)
	if len(got) != 2 {
		t.Fatalf("history len=%d", len(got))
	}
	if got[0].ID != "room-1" || got[1].ID != "room-2" {
		t.Fatalf("unexpected merge: %#v", got)
	}
}
