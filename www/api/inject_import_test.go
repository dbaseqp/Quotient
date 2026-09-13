package api

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeInjectImportArchive(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, contents := range files {
		file, err := writer.Create(name)
		require.NoError(t, err)
		_, err = file.Write([]byte(contents))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	archive, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err)
	return archive
}

func TestPrepareInjectImport(t *testing.T) {
	anchor := time.Date(2027, time.March, 19, 15, 0, 0, 0, time.UTC)
	archive := makeInjectImportArchive(t, map[string]string{
		"injects.toml": `
[[Inject]]
Title = "Executive Briefing"
OpenAfter = "30m"
DueAfter = "1h30m"
Files = ["brief.pdf"]
`,
		"brief.pdf": "%PDF fixture",
	})

	prepared, err := prepareInjectImport(archive, anchor, nil)
	require.NoError(t, err)
	require.Len(t, prepared, 1)
	assert.Equal(t, "See attached files.", prepared[0].Schema.Description)
	assert.Equal(t, anchor.Add(30*time.Minute), prepared[0].Schema.OpenTime)
	assert.Equal(t, anchor.Add(90*time.Minute), prepared[0].Schema.DueTime)
	assert.Equal(t, prepared[0].Schema.DueTime, prepared[0].Schema.CloseTime)
	assert.Equal(t, int64(1800), *prepared[0].Schema.OpenOffset)
	assert.Equal(t, []byte("%PDF fixture"), prepared[0].Files["brief.pdf"])
}

func TestPrepareInjectImportRejectsInvalidBundle(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		files    map[string]string
		existing map[string]struct{}
		contains string
	}{
		{
			name: "missing attachment",
			manifest: `[[Inject]]
Title = "Missing"
OpenAfter = "0s"
DueAfter = "1h"
Files = ["missing.pdf"]`,
			contains: "missing from the ZIP",
		},
		{
			name: "invalid ordering",
			manifest: `[[Inject]]
Title = "Ordering"
OpenAfter = "2h"
DueAfter = "1h"`,
			contains: "OpenAfter must be before",
		},
		{
			name: "existing title",
			manifest: `[[Inject]]
Title = "Existing"
OpenAfter = "0s"
DueAfter = "1h"`,
			existing: map[string]struct{}{"Existing": {}},
			contains: "already exists",
		},
		{
			name: "unknown field",
			manifest: `[[Inject]]
Title = "Unknown"
OpenAfter = "0s"
DueAfter = "1h"
Surprise = true`,
			contains: "unknown field",
		},
		{
			name: "nested archive path",
			manifest: `[[Inject]]
Title = "Nested"
OpenAfter = "0s"
DueAfter = "1h"`,
			files:    map[string]string{"folder/file.pdf": "content"},
			contains: "ZIP root",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := map[string]string{"injects.toml": test.manifest}
			for name, contents := range test.files {
				files[name] = contents
			}
			archive := makeInjectImportArchive(t, files)
			_, err := prepareInjectImport(archive, time.Now(), test.existing)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.contains)
		})
	}
}
