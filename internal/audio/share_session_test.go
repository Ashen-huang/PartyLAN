package audio

import "testing"

func TestShareSessionSetSelectedKeepsSingleSource(t *testing.T) {
	session := NewShareSession()

	session.SetSelected([]string{"chrome.exe", "game.exe"})
	selected := session.Selected()

	if len(selected) != 1 || selected[0] != "chrome.exe" {
		t.Fatalf("selected sources = %#v, want [chrome.exe]", selected)
	}
}

func TestShareSessionSetSelectedClearsSources(t *testing.T) {
	session := NewShareSession()
	session.SetSelected([]string{"chrome.exe"})

	session.SetSelected(nil)

	if selected := session.Selected(); len(selected) != 0 {
		t.Fatalf("selected sources after clear = %#v, want empty", selected)
	}
}
