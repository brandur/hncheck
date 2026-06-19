package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGetHTTPDataAccepts2xxStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))
	defer server.Close()

	respData, err := getHTTPData(context.Background(), server.URL)
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}
	if string(respData) != "created" {
		t.Errorf("Expected response data %q to equal %q.", string(respData), "created")
	}
}

func TestGetHTTPDataNon2xxIncludesBodySample(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a", errorBodyMaxBytes) + "tail"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, body, http.StatusInternalServerError)
	}))
	defer server.Close()

	respData, err := getHTTPData(context.Background(), server.URL)
	if err == nil {
		t.Fatal("Expected to return an error.")
	}
	if respData != nil {
		t.Errorf("Expected response data to be nil (was %q).", string(respData))
	}

	var badStatusErr *BadStatusError
	if !errors.As(err, &badStatusErr) {
		t.Fatalf("Expected error to be BadStatusError (was %T).", err)
	}
	if badStatusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected status code %v to equal %v.",
			badStatusErr.StatusCode, http.StatusInternalServerError)
	}
	if len(badStatusErr.BodySample) != errorBodyMaxBytes {
		t.Errorf("Expected body sample length %v to equal %v.",
			len(badStatusErr.BodySample), errorBodyMaxBytes)
	}
	if !badStatusErr.BodyTruncated {
		t.Error("Expected body sample to be marked as truncated.")
	}
	if strings.Contains(err.Error(), "tail") {
		t.Errorf("Expected error not to include body content beyond sample limit (was %q).", err.Error())
	}
	if !strings.Contains(err.Error(), "response body sample") {
		t.Errorf("Expected error to include response body sample (was %q).", err.Error())
	}
}

func TestGetHTTPData429IncludesRateLimitHeaders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1712345678")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := getHTTPData(context.Background(), server.URL)
	if err == nil {
		t.Fatal("Expected to return an error.")
	}

	for _, expected := range []string{
		"X-RateLimit-Limit=60",
		"X-RateLimit-Remaining=0",
		"X-RateLimit-Reset=1712345678",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Expected error to include %q (was %q).", expected, err.Error())
		}
	}
}

func TestExitStatusForCheckError(t *testing.T) {
	t.Parallel()

	err := &BadStatusError{URL: "https://example.com", StatusCode: http.StatusTooManyRequests}
	if exitStatus := exitStatusForCheckError(err); exitStatus != 1 {
		t.Errorf("Expected exit status %v to equal 1.", exitStatus)
	}

	wrappedErr := errors.New("some other error")
	if exitStatus := exitStatusForCheckError(wrappedErr); exitStatus != 0 {
		t.Errorf("Expected exit status %v to equal 0.", exitStatus)
	}
}

func TestParseConfEmailModeLogToleratesMissingEmailConfiguration(t *testing.T) {
	t.Setenv("DOMAIN", "brandur.org, example.com")
	t.Setenv("EMAIL_MODE", string(EmailModeLog))
	t.Setenv("RECIPIENT", "")
	t.Setenv("SMTP_LOGIN", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_PORT", "")
	t.Setenv("SMTP_SERVER", "")

	conf, err := parseConf()
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}
	if conf.EmailMode != EmailModeLog {
		t.Errorf("Expected email mode %q to equal %q.", conf.EmailMode, EmailModeLog)
	}
	if conf.Loop {
		t.Error("Expected loop to be disabled by default.")
	}
	expectedDomains := []string{"brandur.org", "example.com"}
	if len(conf.Domain) != len(expectedDomains) {
		t.Fatalf("Expected domain length %v to equal %v.", len(conf.Domain), len(expectedDomains))
	}
	for i, expectedDomain := range expectedDomains {
		if conf.Domain[i] != expectedDomain {
			t.Errorf("Expected domain (index %v) %q to equal %q.",
				i, conf.Domain[i], expectedDomain)
		}
	}
}

func TestParseConfLoopCanBeEnabled(t *testing.T) {
	t.Setenv("DOMAIN", "brandur.org")
	t.Setenv("EMAIL_MODE", string(EmailModeLog))
	t.Setenv("LOOP", "true")

	conf, err := parseConf()
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}
	if !conf.Loop {
		t.Error("Expected loop to be enabled.")
	}
}

func TestSendEmailLogModePrintsWouldSendLine(t *testing.T) {
	oldConf := conf
	t.Cleanup(func() {
		conf = oldConf
	})

	conf = &Conf{
		EmailMode: EmailModeLog,
	}

	subject := `New HN submission for "brandur.org"`
	output, err := captureStdout(t, func() error {
		return sendEmail(context.Background(), subject, "body")
	})
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}

	expected := fmt.Sprintf("Email would've been sent: to=<unset> subject=%q\n", subject)
	if output != expected {
		t.Errorf("Expected output %q to equal %q.", output, expected)
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()

	var duration, expected time.Duration
	var err error

	duration, err = parseDuration(1, "minute")
	expected = 1 * time.Minute
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}
	if duration != expected {
		t.Errorf("Expected duration %v to equal %v.", duration, expected)
	}

	duration, err = parseDuration(5, "hours")
	expected = 5 * time.Hour
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}
	if duration != expected {
		t.Errorf("Expected duration %v to equal %v.", duration, expected)
	}

	duration, err = parseDuration(1000, "days")
	expected = 1000 * time.Hour * 24
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}
	if duration != expected {
		t.Errorf("Expected duration %v to equal %v.", duration, expected)
	}
}

func TestParseNewestSubmissionDurations(t *testing.T) {
	t.Parallel()

	durationsByDomain, err := parseNewestSubmissionDurations(newestHTML, []string{"brandur.org", "example.com"})
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}

	expectedBrandurDurations := []time.Duration{3 * time.Minute, 2 * time.Hour}
	brandurDurations := durationsByDomain["brandur.org"]
	if len(brandurDurations) != len(expectedBrandurDurations) {
		t.Fatalf("Expected brandur.org durations length %v to equal %v.",
			len(brandurDurations), len(expectedBrandurDurations))
	}
	for i, expectedDuration := range expectedBrandurDurations {
		if brandurDurations[i] != expectedDuration {
			t.Errorf("Expected brandur.org durations element (index %v) %v to equal %v.",
				i, brandurDurations[i], expectedDuration)
		}
	}

	expectedExampleDurations := []time.Duration{19 * time.Minute}
	exampleDurations := durationsByDomain["example.com"]
	if len(exampleDurations) != len(expectedExampleDurations) {
		t.Fatalf("Expected example.com durations length %v to equal %v.",
			len(exampleDurations), len(expectedExampleDurations))
	}
	for i, expectedDuration := range expectedExampleDurations {
		if exampleDurations[i] != expectedDuration {
			t.Errorf("Expected example.com durations element (index %v) %v to equal %v.",
				i, exampleDurations[i], expectedDuration)
		}
	}
}

//
// Data
//

// This is a minimal sampling of current and older HN /newest row structures.
const newestHTML = `
<tr class='athing submission' id='1'>
  <td class='title'><span class='titleline'><a href='https://brandur.org/new-post'>New Post</a><span class='sitebit comhead'> (<a href='from?site=brandur.org'><span class='sitestr'>brandur.org</span></a>)</span></span></td>
</tr>
<tr>
  <td colspan='2'></td><td class='subtext'><span class='subline'><span class='age'><a href='item?id=1'>3 minutes ago</a></span></span></td>
</tr>
<tr class='spacer' style='height:5px'></tr>
<tr class='athing submission' id='2'>
  <td class='title'><span class='titleline'><a href='https://other.example/new-post'>Other Post</a><span class='sitebit comhead'> (<a href='from?site=other.example'><span class='sitestr'>other.example</span></a>)</span></span></td>
</tr>
<tr>
  <td colspan='2'></td><td class='subtext'><span class='subline'><span class='age'><a href='item?id=2'>4 minutes ago</a></span></span></td>
</tr>
<tr class='spacer' style='height:5px'></tr>
<tr class="athing" id="3">
  <td class="title"><a href="https://example.com/new-post" class="storylink" rel="nofollow">Example Post</a><span class="sitebit comhead"> (<a href="from?site=example.com"><span class="sitestr">example.com</span></a>)</span></td>
</tr>
<tr>
  <td colspan="2"></td><td class="subtext"><span class="age"><a href="item?id=3">19 minutes ago</a></span></td>
</tr>
<tr class='spacer' style='height:5px'></tr>
<tr class='athing submission' id='4'>
  <td class='title'><span class='titleline'><a href='item?id=4'>Ask HN: No site</a></span></td>
</tr>
<tr>
  <td colspan='2'></td><td class='subtext'><span class='subline'><span class='age'><a href='item?id=4'>1 minute ago</a></span></span></td>
</tr>
<tr class='spacer' style='height:5px'></tr>
<tr class='athing submission' id='5'>
  <td class='title'><span class='titleline'><a href='https://brandur.org/old-post'>Old Post</a><span class='sitebit comhead'> (<a href='from?site=Brandur.Org'><span class='sitestr'>Brandur.Org</span></a>)</span></span></td>
</tr>
<tr>
  <td colspan='2'></td><td class='subtext'><span class='subline'><span class='age'><a href='item?id=5'>2 hours ago</a></span></span></td>
</tr>
`

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Expected to create stdout pipe (was \"%v\").", err)
	}
	os.Stdout = w

	fnErr := fn()
	closeErr := w.Close()
	os.Stdout = oldStdout

	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("Expected to read stdout pipe (was \"%v\").", readErr)
	}
	if closeErr != nil {
		t.Fatalf("Expected to close stdout pipe (was \"%v\").", closeErr)
	}

	return string(out), fnErr
}
