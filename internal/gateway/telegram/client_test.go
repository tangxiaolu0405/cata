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

// TestMessageAttachments 验证 Message 能解析 photo/document/voice 附件字段。
func TestMessageAttachments(t *testing.T) {
	raw := `{
		"message_id": 20,
		"chat": {"id": 1},
		"from": {"id": 2},
		"caption": "看这张图",
		"photo": [
			{"file_id": "small", "width": 100, "height": 100},
			{"file_id": "large", "width": 800, "height": 600}
		]
	}`
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Photo) != 2 {
		t.Fatalf("photo count=%d", len(m.Photo))
	}
	if m.Photo[1].FileID != "large" {
		t.Fatalf("largest photo file_id=%q", m.Photo[1].FileID)
	}
	if m.Caption != "看这张图" {
		t.Fatalf("caption=%q", m.Caption)
	}
	if m.Text != "" {
		t.Fatalf("caption should not map to Text: %q", m.Text)
	}

	// document。
	rawDoc := `{
		"message_id": 21,
		"chat": {"id": 1},
		"document": {"file_id": "doc1", "file_name": "report.pdf", "mime_type": "application/pdf"}
	}`
	var md Message
	if err := json.Unmarshal([]byte(rawDoc), &md); err != nil {
		t.Fatal(err)
	}
	if md.Document == nil || md.Document.FileID != "doc1" || md.Document.FileName != "report.pdf" {
		t.Fatalf("document=%+v", md.Document)
	}

	// voice。
	rawVoice := `{
		"message_id": 22,
		"chat": {"id": 1},
		"voice": {"file_id": "v1", "mime_type": "audio/ogg", "duration": 5}
	}`
	var mv Message
	if err := json.Unmarshal([]byte(rawVoice), &mv); err != nil {
		t.Fatal(err)
	}
	if mv.Voice == nil || mv.Voice.FileID != "v1" {
		t.Fatalf("voice=%+v", mv.Voice)
	}
}
