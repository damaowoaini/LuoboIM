package office

import "testing"

func TestMomentRejectsUnsafeOrInvalidMediaBeforePersistence(t *testing.T) {
	for _, content := range []*WorkMomentContent{nil, {}, {Text: "text", Type: 5}, {Type: 1}, {Metas: []*Meta{nil}}, {Metas: []*Meta{{Original: "file:///private/source.jpg"}}}, {Metas: []*Meta{{Original: "http://objects.example.com/image"}}}, {Metas: []*Meta{{Original: "https://u:p@objects.example.com/image"}}}, {Metas: []*Meta{{Original: "https://objects.example.com/image", Width: -1}}}} {
		if err := (&CreateOneWorkMomentReq{Content: content}).Check(); err == nil {
			t.Fatal("invalid content accepted")
		}
	}
	for _, content := range []*WorkMomentContent{{Text: "text only"}, {Metas: []*Meta{{Original: "https://objects.example.com/p.jpg", Width: 640, Height: 480}}}, {Type: 1, Metas: []*Meta{{Original: "https://objects.example.com/v.mp4", Thumb: "https://objects.example.com/t.jpg"}}}} {
		if err := (&CreateOneWorkMomentReq{Content: content}).Check(); err != nil {
			t.Fatal(err)
		}
	}
}
