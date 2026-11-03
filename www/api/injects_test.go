package api

import "testing"

func TestGetInjectFilePresentation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fileName    string
		inline      bool
		disposition string
		contentType string
		frameable   bool
	}{
		{
			name:        "PDF inline",
			fileName:    "briefing.PDF",
			inline:      true,
			disposition: "inline",
			contentType: "application/pdf",
			frameable:   true,
		},
		{
			name:        "PDF download",
			fileName:    "briefing.pdf",
			disposition: "attachment",
			contentType: "application/pdf",
		},
		{
			name:        "DOCX inline",
			fileName:    "response.DOCX",
			inline:      true,
			disposition: "inline",
			contentType: docxContentType,
		},
		{
			name:        "DOCX download",
			fileName:    "response.docx",
			disposition: "attachment",
			contentType: docxContentType,
		},
		{
			name:        "macro document remains download only",
			fileName:    "unsafe.docm",
			inline:      true,
			disposition: "attachment",
			contentType: "application/octet-stream",
		},
		{
			name:        "legacy document remains download only",
			fileName:    "legacy.doc",
			inline:      true,
			disposition: "attachment",
			contentType: "application/octet-stream",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := getInjectFilePresentation(test.fileName, test.inline)
			if got.Disposition != test.disposition {
				t.Fatalf("disposition = %q, want %q", got.Disposition, test.disposition)
			}
			if got.ContentType != test.contentType {
				t.Fatalf("content type = %q, want %q", got.ContentType, test.contentType)
			}
			if got.Frameable != test.frameable {
				t.Fatalf("frameable = %t, want %t", got.Frameable, test.frameable)
			}
		})
	}
}
