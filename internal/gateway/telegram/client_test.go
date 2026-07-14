package telegram

import (
	"encoding/json"
	"testing"
)

func TestUpdateMessage_chatID(t *testing.T) {
	raw := `{
		"update_id": 1,
		"message": {
			"message_id": 10,
			"from": {"id": 999, "is_bot": false, "first_name": "U"},
			"chat": {"id": 123456789, "type": "private", "first_name": "U"},
			"text": "/start"
		}
	}`
	var u Update
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.Message == nil || u.Message.Chat.ID != 123456789 {
		t.Fatalf("chat id=%d", u.Message.Chat.ID)
	}
	if u.Message.From == nil || u.Message.From.ID != 999 {
		t.Fatalf("from id")
	}
}
