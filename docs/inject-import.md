# Importing Injects

Inject managers and administrators can import a complete inject schedule from the Injects page. Upload a ZIP file containing an `injects.toml` manifest and any attachments referenced by the manifest. Attachments are optional and must be in the root of the ZIP.

A ready-to-upload bundle is available at [`examples/inject-import-example.zip`](../examples/inject-import-example.zip). Its editable source files are in [`examples/inject-import`](../examples/inject-import/).

File structure
```
regional-injects.zip
├── injects.toml
├── inject-01.pdf
├── inject-02.pdf
└── templates/
    └── response-form.docx
```

Configure the planned competition start in `config/event.conf`:

```toml
[InjectSettings]
CompetitionStartTime = "2027-03-19 08:00"
CompetitionTimezone = "America/Los_Angeles"
```

The named timezone applies the correct daylight-saving rules for that date. The import preview uses this planned time until the competition begins. The first time an administrator starts the competition, Quotient records the actual start in PostgreSQL and recalculates every imported inject from its relative offsets. That timestamp survives engine restarts and later stop/start toggles.

An example `injects.toml`:

```toml
[[Inject]]
Title = "Executive Briefing"
Description = "Review the attached document."
OpenAfter = "30m"
DueAfter = "1h30m"
CloseAfter = "2h"
Files = ["inject-01.pdf"]

[[Inject]]
Title = "Incident Response"
OpenAfter = "2h"
DueAfter = "3h"
Files = ["inject-02.pdf", "response-form.docx"]
```

`Title`, `OpenAfter`, and `DueAfter` are required. `Description` defaults to `See attached files.` and `CloseAfter` defaults to `DueAfter`. Durations use Go duration notation, including `30m`, `2h`, and `2h15m`. Offsets must be non-negative and ordered as open, due, then close.

The importer validates the complete bundle and displays the calculated schedule before enabling Import. It rejects unknown manifest fields, duplicate titles, missing files, unsafe archive paths, and invalid time ordering.
