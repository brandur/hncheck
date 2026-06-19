package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	alertPeriod       = 20 * time.Minute
	errorBodyMaxBytes = 4 * 1024
	hnDomainURL       = "https://news.ycombinator.com/from?site=%s"
)

var (
	conf *Conf //nolint:gochecknoglobals

	// Matches something like "1 minute ago" or "3 hours ago". Note we include
	// some angle brackets to avoid false positives.
	timeRegexp = regexp.MustCompile(`>([1-9]\d*) (\w+) ago<`)
)

// Conf holds configuration information for the program.
type Conf struct {
	// Domain is specified as DOMAIN and may included multiple domains to check
	// separated by a comma.
	Domain []string

	// Loop determines whether the program runs continuous in a loop. It
	// defaults to true. If false, it runs once and exits.
	Loop bool

	// Recipient is the email address of the person to be alerted in case a new
	// submission on a configured domain is detected.
	Recipient string

	SMTPLogin    string
	SMTPPassword string
	SMTPPort     string
	SMTPServer   string
}

func main() {
	var err error
	conf, err = parseConf()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	if os.Getenv("TEST_EMAIL") == "true" {
		err := sendDomainMessage(ctx, conf.Domain[0])
		if err != nil {
			panic(err)
		}
		fmt.Printf("Test email sent: %s\n", conf.Recipient)
	} else {
		for {
			err = checkDomains(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", err)
				if exitStatus := exitStatusForCheckError(err); exitStatus != 0 {
					os.Exit(exitStatus)
				}
				goto wait
			}

		wait:
			if !conf.Loop {
				break
			}

			// Add some random jitter just so that we're not requesting on a
			// perfectly predictable schedule all the time.
			sleepDuration := alertPeriod - time.Duration(rand.IntN(60))*time.Second
			fmt.Printf("Sleeping for %v between runs\n", sleepDuration)
			time.Sleep(sleepDuration)
		}
	}
}

//
// Helpers
//

func checkDomains(ctx context.Context) error {
	for _, domain := range conf.Domain {
		url := fmt.Sprintf(hnDomainURL, domain)
		respData, err := getHTTPData(ctx, url)
		if err != nil {
			return err
		}

		durations, err := parseDurations(string(respData))
		if err != nil {
			return err
		}

		for _, duration := range durations {
			fmt.Printf("Found an article with age: %v\n", duration)

			if duration <= alertPeriod {
				fmt.Printf("Article's age is below alert threshold; sending email")
				err := sendDomainMessage(ctx, domain)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func getHTTPData(ctx context.Context, url string) ([]byte, error) {
	fmt.Printf("Requesting: %v\n", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Gosec flags this with "G704: SSRF via taint analysis", but neither its
	// error or website provides even a whiff of what it means by this or a
	// suggested remediation.
	resp, err := http.DefaultClient.Do(req) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("error while requesting %q: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodySample, bodyTruncated, bodyReadErr := readBodySample(resp.Body, errorBodyMaxBytes)
		err := &BadStatusError{
			URL:                url,
			Status:             resp.Status,
			StatusCode:         resp.StatusCode,
			BodySample:         bodySample,
			BodyTruncated:      bodyTruncated,
			BodyReadErr:        bodyReadErr,
			RateLimitHeaders:   rateLimitHeaders(resp.Header, resp.StatusCode),
			BodySampleMaxBytes: errorBodyMaxBytes,
		}
		return nil, err
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error while reading response from %q: %w", url, err)
	}

	return respBytes, nil
}

type BadStatusError struct {
	URL                string
	Status             string
	StatusCode         int
	BodySample         string
	BodyTruncated      bool
	BodyReadErr        error
	RateLimitHeaders   []string
	BodySampleMaxBytes int
}

func (e *BadStatusError) Error() string {
	status := e.Status
	if status == "" {
		status = strconv.Itoa(e.StatusCode)
	}

	msg := fmt.Sprintf("bad status while requesting %q: %s", e.URL, status)

	if e.BodyReadErr != nil {
		msg += fmt.Sprintf("; error while reading response body: %v", e.BodyReadErr)
	} else if e.BodySample != "" {
		if e.BodyTruncated {
			msg += fmt.Sprintf("; response body sample (truncated to %d bytes): %q",
				e.BodySampleMaxBytes, e.BodySample)
		} else {
			msg += fmt.Sprintf("; response body: %q", e.BodySample)
		}
	}

	if len(e.RateLimitHeaders) > 0 {
		msg += "; rate limit headers: " + strings.Join(e.RateLimitHeaders, ", ")
	}

	return msg
}

func exitStatusForCheckError(err error) int {
	var badStatusErr *BadStatusError
	if errors.As(err, &badStatusErr) {
		return 1
	}

	return 0
}

func rateLimitHeaders(header http.Header, statusCode int) []string {
	if statusCode != http.StatusTooManyRequests {
		return nil
	}

	const rateLimitHeaderPrefix = "x-ratelimit-"

	headers := make([]string, 0)
	for name, values := range header {
		lowerName := strings.ToLower(name)
		if !strings.HasPrefix(lowerName, rateLimitHeaderPrefix) {
			continue
		}

		displayName := "X-RateLimit-" + name[len(rateLimitHeaderPrefix):]
		headers = append(headers, fmt.Sprintf("%s=%s", displayName, strings.Join(values, ", ")))
	}

	sort.Strings(headers)
	return headers
}

func readBodySample(body io.Reader, maxBytes int) (string, bool, error) {
	bodySample, err := io.ReadAll(io.LimitReader(body, int64(maxBytes)+1))
	if err != nil {
		return "", false, err
	}

	truncated := len(bodySample) > maxBytes
	if truncated {
		bodySample = bodySample[:maxBytes]
	}

	return strings.TrimSpace(string(bodySample)), truncated, nil
}

type MissingEnvError struct {
	EnvName string
}

func (e MissingEnvError) Error() string {
	return "missing environment value for: " + e.EnvName
}

func parseConf() (*Conf, error) {
	conf := &Conf{
		Loop: true,
	}

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return nil, &MissingEnvError{"DOMAIN"}
	}

	conf.Domain = strings.Split(domain, ",")
	if len(conf.Domain) < 1 {
		return nil, errors.New("need at least one value in: DOMAIN")
	}

	if os.Getenv("LOOP") == "false" {
		conf.Loop = false
	}

	conf.Recipient = os.Getenv("RECIPIENT")
	if conf.Recipient == "" {
		return nil, &MissingEnvError{"RECIPIENT"}
	}

	conf.SMTPLogin = os.Getenv("SMTP_LOGIN")
	if conf.SMTPLogin == "" {
		return nil, &MissingEnvError{"SMTP_LOGIN"}
	}

	conf.SMTPPassword = os.Getenv("SMTP_PASSWORD")
	if conf.SMTPPassword == "" {
		return nil, &MissingEnvError{"SMTP_PASSWORD"}
	}

	conf.SMTPPort = os.Getenv("SMTP_PORT")
	if conf.SMTPPort == "" {
		return nil, &MissingEnvError{"SMTP_PORT"}
	}

	conf.SMTPServer = os.Getenv("SMTP_SERVER")
	if conf.SMTPServer == "" {
		return nil, &MissingEnvError{"SMTP_SERVER"}
	}

	return conf, nil
}

func parseDuration(num int, unit string) (time.Duration, error) {
	// So I'm pretty sure HN only goes from minutes to days units, but just
	// handle everything in case that changes at some point.
	switch unit {
	case "second", "seconds":
		return time.Duration(num) * time.Second, nil

	case "minute", "minutes":
		return time.Duration(num) * time.Minute, nil

	case "hour", "hours":
		return time.Duration(num) * time.Hour, nil

	case "day", "days":
		return time.Duration(num) * time.Hour * 24, nil

	case "month", "months":
		return time.Duration(num) * time.Hour * 24 * 30, nil

	case "year", "years":
		return time.Duration(num) * time.Hour * 24 * 365, nil
	}

	return 0 * time.Second, fmt.Errorf("couldn't parse duration: %v %v", num, unit)
}

func parseDurations(content string) ([]time.Duration, error) {
	// We identify articles purely by looking at the ages under the
	// domain-specific list. This isn't very robust, and given consistently bad
	// results it'd be a good idea to revisit it, but so far in practice it
	// seems to have yielded pretty good results, so I'll stick with it for
	// now.
	matches := timeRegexp.FindAllStringSubmatch(content, -1)

	durations := make([]time.Duration, len(matches))

	for i, match := range matches {
		numStr := match[1]
		unit := match[2]

		num, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("error while parsing number %d: %w", num, err)
		}

		duration, err := parseDuration(num, unit)
		if err != nil {
			return nil, fmt.Errorf("error while parsing duration: %w", err)
		}

		durations[i] = duration
	}
	return durations, nil
}

func sendDomainMessage(ctx context.Context, domain string) error {
	return sendEmail(
		ctx,
		"New HN submission for \""+domain+"\"",
		"New HN submission for \""+domain+"\". Please see:\n\n"+
			"https://news.ycombinator.com/newest\n",
	)
}

func sendEmail(_ context.Context, subject, body string) error {
	auth := smtp.PlainAuth("", conf.SMTPLogin, conf.SMTPPassword, conf.SMTPServer)

	recipients := []string{conf.Recipient}
	payload := []byte("To: " + conf.Recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" + body + "\r\n")
	err := smtp.SendMail(
		conf.SMTPServer+":"+conf.SMTPPort,
		auth, "hncheck@mutelight.org", recipients, payload)
	if err != nil {
		return fmt.Errorf("error sending mail: %w", err)
	}

	return nil
}
