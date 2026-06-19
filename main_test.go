package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestParseDurations(t *testing.T) {
	t.Parallel()

	durations, err := parseDurations(domainHTML)
	if err != nil {
		t.Errorf("Expected not to return an error (was \"%v\").", err)
	}
	expected := []time.Duration{3 * 24 * time.Hour, 7 * 24 * time.Hour}
	if len(durations) != len(expected) {
		t.Errorf("Expected durations length %v to equal %v.",
			len(durations), len(expected))
	}
	for i := range expected {
		if durations[i] != expected[i] {
			t.Errorf("Expected durations element (index %v) %v to equal %v.",
				i, durations[i], expected[i])
		}
	}
}

//
// Data
//

// This is just a random sampling pulled from a domain-specific HN page.
const domainHTML = `
        <span class="score" id="score_13877867">2 points</span> by <a href="user?id=mooreds" class="hnuser">mooreds</a> <span class="age"><a href="item?id=13877867">3 days ago</a></span> <span id="unv_13877867"></span> | <a href="flag?id=13877867&amp;auth=6872af1bbe300db8892d0032ac5a516312b40846&amp;goto=from%3Fsite%3Dbrandur.org">flag</a> | <a href="https://hn.algolia.com/?query=AWS%20Islands&sort=byDate&dateRange=all&type=story&storyText=false&prefix&page=0" class="hnpast">past</a> | <a href="https://www.google.com/search?q=AWS%20Islands">web</a> | <a href="item?id=13877867">discuss</a>              </td></tr>
      <tr class="spacer" style="height:5px"></tr>
                <tr class='athing' id='13845842'>
      <td align="right" valign="top" class="title"><span class="rank"></span></td>      <td valign="top" class="votelinks"><center><a id='up_13845842' onclick='return vote(event, this, "up")' href='vote?id=13845842&amp;how=up&amp;auth=4ef3e44542dd6a955debaae74af769d818f97f75&amp;goto=from%3Fsite%3Dbrandur.org' class='nosee'><div class='votearrow' title='upvote'></div></a></center></td><td class="title"><a href="https://brandur.org/canonical-log-lines" class="storylink" rel="nofollow">Using Canonical Log Lines for Online Visibility</a><span class="sitebit comhead"> (<a href="from?site=brandur.org"><span class="sitestr">brandur.org</span></a>)</span></td></tr><tr><td colspan="2"></td><td class="subtext">
        <span class="score" id="score_13845842">6 points</span> by <a href="user?id=aurelium" class="hnuser">aurelium</a> <span class="age"><a href="item?id=13845842">7 days ago</a></span> <span id="unv_13845842"></span> | <a href="flag?id=13845842&amp;auth=4ef3e44542dd6a955debaae74af769d818f97f75&amp;goto=from%3Fsite%3Dbrandur.org">flag</a> | <a href="https://hn.algolia.com/?query=Using%20Canonical%20Log%20Lines%20for%20Online%20Visibility&sort=byDate&dateRange=all&type=story&storyText=false&prefix&page=0" class="hnpast">past</a> | <a href="https://www.google.com/search?q=Using%20Canonical%20Log%20Lines%20for%20Online%20Visibility">web</a> | <a href="item?id=13845842">discuss</a>              </td></tr>
`
