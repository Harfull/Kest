package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImageInputUsesContentParts(t *testing.T) {
	data, err := json.Marshal(Message{Role: RoleUser, Content: "untrusted screenshot", Images: []Image{{MediaType: "image/png", Data: []byte("PNG")}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"text"`, `"type":"image_url"`, `data:image/png;base64,UE5H`, `untrusted screenshot`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s: %s", want, data)
		}
	}
}

func TestImageInputRefusesUnsafePayloads(t *testing.T) {
	for _, tc := range []struct {
		name, role, media string
		data              []byte
	}{
		{"tool role", RoleTool, "image/png", []byte("x")},
		{"wrong type", RoleUser, "text/html", []byte("x")},
		{"empty", RoleUser, "image/png", nil},
		{"oversize", RoleUser, "image/png", make([]byte, (10<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := json.Marshal(Message{Role: tc.role, Images: []Image{{MediaType: tc.media, Data: tc.data}}}); err == nil {
				t.Fatal("unsafe image input accepted")
			}
		})
	}
}
