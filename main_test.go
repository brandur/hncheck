package main

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
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
var domainHTML = `
        <span class="score" id="score_13877867">2 points</span> by <a href="user?id=mooreds" class="hnuser">mooreds</a> <span class="age"><a href="item?id=13877867">3 days ago</a></span> <span id="unv_13877867"></span> | <a href="flag?id=13877867&amp;auth=6872af1bbe300db8892d0032ac5a516312b40846&amp;goto=from%3Fsite%3Dbrandur.org">flag</a> | <a href="https://hn.algolia.com/?query=AWS%20Islands&sort=byDate&dateRange=all&type=story&storyText=false&prefix&page=0" class="hnpast">past</a> | <a href="https://www.google.com/search?q=AWS%20Islands">web</a> | <a href="item?id=13877867">discuss</a>              </td></tr>
      <tr class="spacer" style="height:5px"></tr>
                <tr class='athing' id='13845842'>
      <td align="right" valign="top" class="title"><span class="rank"></span></td>      <td valign="top" class="votelinks"><center><a id='up_13845842' onclick='return vote(event, this, "up")' href='vote?id=13845842&amp;how=up&amp;auth=4ef3e44542dd6a955debaae74af769d818f97f75&amp;goto=from%3Fsite%3Dbrandur.org' class='nosee'><div class='votearrow' title='upvote'></div></a></center></td><td class="title"><a href="https://brandur.org/canonical-log-lines" class="storylink" rel="nofollow">Using Canonical Log Lines for Online Visibility</a><span class="sitebit comhead"> (<a href="from?site=brandur.org"><span class="sitestr">brandur.org</span></a>)</span></td></tr><tr><td colspan="2"></td><td class="subtext">
        <span class="score" id="score_13845842">6 points</span> by <a href="user?id=aurelium" class="hnuser">aurelium</a> <span class="age"><a href="item?id=13845842">7 days ago</a></span> <span id="unv_13845842"></span> | <a href="flag?id=13845842&amp;auth=4ef3e44542dd6a955debaae74af769d818f97f75&amp;goto=from%3Fsite%3Dbrandur.org">flag</a> | <a href="https://hn.algolia.com/?query=Using%20Canonical%20Log%20Lines%20for%20Online%20Visibility&sort=byDate&dateRange=all&type=story&storyText=false&prefix&page=0" class="hnpast">past</a> | <a href="https://www.google.com/search?q=Using%20Canonical%20Log%20Lines%20for%20Online%20Visibility">web</a> | <a href="item?id=13845842">discuss</a>              </td></tr>
`
