package internal

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGetVersionDefault(t *testing.T) {
	got := GetVersion()
	if !strings.Contains(got, commitVersion) {
		t.Errorf("GetVersion() = %q, want it to contain the version %q", got, commitVersion)
	}
}

func TestGetVersionWithDate(t *testing.T) {
	orig := commitDate
	t.Cleanup(func() { commitDate = orig })

	commitDate = "1783209600"
	secs, _ := strconv.Atoi(commitDate)
	want := "date: " + time.Unix(int64(secs), 0).UTC().Format("2006-01-02")
	got := GetVersion()
	if !strings.Contains(got, want) {
		t.Errorf("GetVersion() = %q, want it to contain %q", got, want)
	}
	if !strings.Contains(got, commitVersion) {
		t.Errorf("GetVersion() = %q, want it to contain the version %q", got, commitVersion)
	}
}

func TestGetVersionWithoutDate(t *testing.T) {
	orig := commitDate
	t.Cleanup(func() { commitDate = orig })

	commitDate = ""
	if got := GetVersion(); got != commitVersion {
		t.Errorf("GetVersion() = %q, want just the version %q when no date is set", got, commitVersion)
	}
}

func TestGetVersionBadDate(t *testing.T) {
	orig := commitDate
	t.Cleanup(func() { commitDate = orig })

	commitDate = "not-a-number"
	if got := GetVersion(); got != commitVersion {
		t.Errorf("GetVersion() = %q, want just the version %q when the date is unparseable", got, commitVersion)
	}
}
