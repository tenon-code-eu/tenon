package event

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewMarshalsPayload(t *testing.T) {
	ts := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	e, err := New("s_1", 1, ts, UserMessage, UserMessagePayload{Text: "salut"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(e)
	want := `{"id":"s_1-1","session":"s_1","seq":1,"ts":"2026-10-14T10:00:00Z","type":"UserMessage","payload":{"text":"salut"}}`
	if string(got) != want {
		t.Fatalf("got %s", got)
	}
}
