package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	alertPeriod = 20 * time.Minute
	hnDomainURL = "https://news.ycombinator.com/from?site=%s"
)

var (
	conf *Conf

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
		fmt.Fprintf(os.Stderr, err.Error()+"\n")
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
				fmt.Fprintf(os.Stderr, err.Error()+"\n")
				goto wait
			}

		wait:
			if !conf.Loop {
				break
			}

			// Add some random jitter just so that we're not requesting on a
			// perfectly predictable schedule all the time.
			sleepDuration := alertPeriod - time.Duration(randIntn(60))*time.Second
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error while requesting %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status while requesting %q: %v", url, resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error while reading response from %q: %w", url, err)
	}

	return respBytes, nil
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
		return nil, fmt.Errorf("need at least one value in: DOMAIN")
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
			return nil, fmt.Errorf("error while parsing number %q: %w", num, err)
		}

		duration, err := parseDuration(num, unit)
		if err != nil {
			return nil, fmt.Errorf("error while parsing duration: %w", err)
		}

		durations[i] = duration
	}
	return durations, nil
}

func randIntn(max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		panic(err)
	}
	return int(n.Int64())
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

	to := []string{conf.Recipient}
	payload := []byte("To: " + conf.Recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" + body + "\r\n")
	err := smtp.SendMail(
		conf.SMTPServer+":"+conf.SMTPPort,
		auth, "hncheck@mutelight.org", to, payload)
	if err != nil {
		return fmt.Errorf("error sending mail: %w", err)
	}

	return nil
}
