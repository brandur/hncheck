package main

import (
	"context"
	"errors"
	"fmt"
	"html"
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
	hnNewestURL       = "https://news.ycombinator.com/newest"
)

type EmailMode string

const (
	EmailModeSMTP EmailMode = "smtp"
	EmailModeLog  EmailMode = "log"
)

var (
	conf *Conf //nolint:gochecknoglobals

	// Matches a story title row and its following metadata row on /newest.
	newestStoryRegexp = regexp.MustCompile(
		`(?is)<tr[^>]*class=['"][^'"]*\bathing\b[^'"]*['"][^>]*>.*?</tr>\s*<tr[^>]*>.*?</tr>`)

	siteRegexp = regexp.MustCompile(
		`(?is)<span[^>]*class=['"][^'"]*\bsitestr\b[^'"]*['"][^>]*>([^<]+)</span>`)

	// Matches something like "1 minute ago" or "3 hours ago". Note we include
	// some angle brackets to avoid false positives.
	timeRegexp = regexp.MustCompile(`(?is)>\s*([1-9]\d*)\s+(\w+)\s+ago\s*<`)
)

// Conf holds configuration information for the program.
type Conf struct {
	// Domain is specified as DOMAIN and may included multiple domains to check
	// separated by a comma.
	Domain []string

	// EmailMode configures whether email is sent through SMTP or logged.
	EmailMode EmailMode

	// Loop determines whether the program runs continuous in a loop. It
	// defaults to false. If true, it runs continuously.
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
		if conf.EmailMode == EmailModeSMTP {
			fmt.Printf("Test email sent: %s\n", conf.Recipient)
		}
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
	respData, err := getHTTPData(ctx, hnNewestURL)
	if err != nil {
		return err
	}

	durationsByDomain, err := parseNewestSubmissionDurations(string(respData), conf.Domain)
	if err != nil {
		return err
	}

	for _, domain := range conf.Domain {
		for _, duration := range durationsByDomain[domain] {
			fmt.Printf("Found an article for %s with age: %v\n", domain, duration)

			if duration <= alertPeriod {
				fmt.Printf("Article's age is below alert threshold; sending email\n")
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
		EmailMode: EmailModeSMTP,
	}

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return nil, &MissingEnvError{"DOMAIN"}
	}

	conf.Domain = parseConfiguredDomains(domain)
	if len(conf.Domain) < 1 {
		return nil, errors.New("need at least one value in: DOMAIN")
	}

	if os.Getenv("LOOP") == "true" {
		conf.Loop = true
	}

	switch emailMode := os.Getenv("EMAIL_MODE"); emailMode {
	case "":
	case string(EmailModeSMTP):
		conf.EmailMode = EmailModeSMTP
	case string(EmailModeLog):
		conf.EmailMode = EmailModeLog
	default:
		return nil, fmt.Errorf("invalid EMAIL_MODE %q; expected %q or %q",
			emailMode, EmailModeSMTP, EmailModeLog)
	}

	conf.Recipient = os.Getenv("RECIPIENT")
	if conf.EmailMode == EmailModeSMTP && conf.Recipient == "" {
		return nil, &MissingEnvError{"RECIPIENT"}
	}

	conf.SMTPLogin = os.Getenv("SMTP_LOGIN")
	if conf.EmailMode == EmailModeSMTP && conf.SMTPLogin == "" {
		return nil, &MissingEnvError{"SMTP_LOGIN"}
	}

	conf.SMTPPassword = os.Getenv("SMTP_PASSWORD")
	if conf.EmailMode == EmailModeSMTP && conf.SMTPPassword == "" {
		return nil, &MissingEnvError{"SMTP_PASSWORD"}
	}

	conf.SMTPPort = os.Getenv("SMTP_PORT")
	if conf.EmailMode == EmailModeSMTP && conf.SMTPPort == "" {
		return nil, &MissingEnvError{"SMTP_PORT"}
	}

	conf.SMTPServer = os.Getenv("SMTP_SERVER")
	if conf.EmailMode == EmailModeSMTP && conf.SMTPServer == "" {
		return nil, &MissingEnvError{"SMTP_SERVER"}
	}

	return conf, nil
}

func parseDuration(num int, unit string) (time.Duration, error) {
	// So I'm pretty sure HN only goes from minutes to days units, but just
	// handle everything in case that changes at some point.
	switch strings.ToLower(unit) {
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

func parseConfiguredDomains(domain string) []string {
	domains := strings.Split(domain, ",")
	parsedDomains := make([]string, 0, len(domains))

	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}

		parsedDomains = append(parsedDomains, domain)
	}

	return parsedDomains
}

func parseNewestSubmissionDurations(content string, domains []string) (map[string][]time.Duration, error) {
	domainByNormalized := make(map[string]string, len(domains))
	durationsByDomain := make(map[string][]time.Duration, len(domains))

	for _, domain := range domains {
		domainByNormalized[normalizeDomain(domain)] = domain
		durationsByDomain[domain] = nil
	}

	stories := newestStoryRegexp.FindAllString(content, -1)
	for _, story := range stories {
		siteMatch := siteRegexp.FindStringSubmatch(story)
		if siteMatch == nil {
			continue
		}

		domain, ok := domainByNormalized[normalizeDomain(siteMatch[1])]
		if !ok {
			continue
		}

		timeMatch := timeRegexp.FindStringSubmatch(story)
		if timeMatch == nil {
			continue
		}

		numStr := timeMatch[1]
		unit := timeMatch[2]

		num, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("error while parsing number %d: %w", num, err)
		}

		duration, err := parseDuration(num, unit)
		if err != nil {
			return nil, fmt.Errorf("error while parsing duration: %w", err)
		}

		durationsByDomain[domain] = append(durationsByDomain[domain], duration)
	}

	return durationsByDomain, nil
}

func normalizeDomain(domain string) string {
	return strings.ToLower(strings.TrimSpace(html.UnescapeString(domain)))
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
	if conf.EmailMode == EmailModeLog {
		recipient := conf.Recipient
		if recipient == "" {
			recipient = "<unset>"
		}

		fmt.Printf("Email would've been sent: to=%s subject=%q\n", recipient, subject)
		return nil
	}

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
