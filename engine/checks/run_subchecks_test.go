package checks

import "testing"

func TestRunSubchecks_CheckAllFailsEarlyIncludesSuffix(t *testing.T) {
	items := []commandData{
		{Command: "ok-1"},
		{Command: "fail-2"},
		{Command: "ok-3"},
	}
	base := Result{}
	debugSuffix := "creds used were user:pass"

	result := RunSubchecks(items, true, base, debugSuffix, func(item commandData, res Result) Result {
		if item.Command == "fail-2" {
			res.Error = "command failed"
			res.Debug = "failed on " + item.Command
			res.Status = false
			return res
		}
		res.Status = true
		res.Debug = "ran " + item.Command
		return res
	})

	if result.Status {
		t.Fatalf("expected failure, got success")
	}
	if result.Error != "command failed" {
		t.Fatalf("unexpected error: %q", result.Error)
	}
	if result.Debug != "failed on fail-2 ("+debugSuffix+")" {
		t.Fatalf("unexpected debug: %q", result.Debug)
	}
}

func TestRunSubchecks_CheckAllSuccessAggregatesAndSuffix(t *testing.T) {
	items := []commandData{
		{Command: "ok-1"},
		{Command: "ok-2"},
	}
	base := Result{}
	debugSuffix := "creds used were user:pass"

	result := RunSubchecks(items, true, base, debugSuffix, func(item commandData, res Result) Result {
		res.Status = true
		res.Debug = "ran " + item.Command
		return res
	})

	if !result.Status {
		t.Fatalf("expected success, got failure: %q", result.Error)
	}
	expected := "all 2 checks passed: ran ok-1; ran ok-2 (" + debugSuffix + ")"
	if result.Debug != expected {
		t.Fatalf("unexpected debug: %q", result.Debug)
	}
}

func TestRunSubchecks_SingleCheckFailureIncludesSuffix(t *testing.T) {
	items := []commandData{
		{Command: "fail-1"},
	}
	base := Result{}
	debugSuffix := "creds used were user:pass"

	result := RunSubchecks(items, false, base, debugSuffix, func(item commandData, res Result) Result {
		res.Error = "command failed"
		res.Debug = "failed on " + item.Command
		res.Status = false
		return res
	})

	if result.Status {
		t.Fatalf("expected failure, got success")
	}
	if result.Debug != "failed on fail-1 ("+debugSuffix+")" {
		t.Fatalf("unexpected debug: %q", result.Debug)
	}
}
