package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	alertPeriod = 12 * time.Minute
	hnDomainURL = "https://news.ycombinator.com/from?site=%s"
)

var (
	conf *Conf

	// Matches something like "1 minute ago" or "3 hours ago". Note we include
	// some angle brackets to avoid false positives.
	timeRegexp = regexp.MustCompile(`>([1-9]\d*) (\w+) ago<`)
)

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

	if os.Getenv("TEST_EMAIL") == "true" {
		err := sendDomainMessage(conf.Domain[0])
		if err != nil {
			panic(err)
		}
		fmt.Printf("Test email sent: %s\n", conf.Recipient)
	} else {
		for {
			err = checkDomains()
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
			sleepDuration := alertPeriod - time.Duration(rand.Intn(60))*time.Second
			fmt.Printf("Sleeping for %v between runs\n", sleepDuration)
			time.Sleep(sleepDuration)
		}
	}
}

//
// Helpers
//

func checkDomains() error {
	for _, domain := range conf.Domain {
		url := fmt.Sprintf(hnDomainURL, domain)
		respData, err := getHTTPData(url)
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
				err := sendDomainMessage(domain)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func getHTTPData(url string) ([]byte, error) {
	fmt.Printf("Requesting: %v\n", url)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("Error while requesting \"%s\": %s\n", url, err.Error())
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Bad status while requesting \"%s\": %v\n", url, resp.StatusCode)
	}

	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Error while reading response from \"%s\": %v\n", url, err.Error())
	}

	return respBytes, nil
}

func parseConf() (*Conf, error) {
	conf := &Conf{
		Loop: true,
	}

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return nil, fmt.Errorf("Need value for: DOMAIN")
	}

	conf.Domain = strings.Split(domain, ",")
	if len(conf.Domain) < 1 {
		return nil, fmt.Errorf("Need at least one value in: DOMAIN")
	}

	if os.Getenv("LOOP") == "false" {
		conf.Loop = false
	}

	conf.Recipient = os.Getenv("RECIPIENT")
	if conf.Recipient == "" {
		return nil, fmt.Errorf("Need value for: RECIPIENT")
	}

	conf.SMTPLogin = os.Getenv("SMTP_LOGIN")
	if conf.SMTPLogin == "" {
		return nil, fmt.Errorf("Need value for: SMTP_LOGIN")
	}

	conf.SMTPPassword = os.Getenv("SMTP_PASSWORD")
	if conf.SMTPPassword == "" {
		return nil, fmt.Errorf("Need value for: SMTP_PASSWORD")
	}

	conf.SMTPPort = os.Getenv("SMTP_PORT")
	if conf.SMTPPort == "" {
		return nil, fmt.Errorf("Need value for: SMTP_PORT")
	}

	conf.SMTPServer = os.Getenv("SMTP_SERVER")
	if conf.SMTPServer == "" {
		return nil, fmt.Errorf("Need value for: SMTP_SERVER")
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

	return 0 * time.Second, fmt.Errorf("Couldn't parse duration: %v %v", num, unit)
}

func parseDurations(content string) ([]time.Duration, error) {
	var durations []time.Duration

	// We identify articles purely by looking at the ages under the
	// domain-specific list. This isn't very robust, and given consistently bad
	// results it'd be a good idea to revisit it, but so far in practice it
	// seems to have yielded pretty good results, so I'll stick with it for
	// now.
	matches := timeRegexp.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		numStr := match[1]
		unit := match[2]

		num, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("Error while parsing number \"%s\": %v\n", num, err.Error())
			continue
		}

		duration, err := parseDuration(num, unit)
		if err != nil {
			return nil, fmt.Errorf("Error while parsing duration: %v\n", err.Error())
		}

		durations = append(durations, duration)
	}
	return durations, nil
}

func sendDomainMessage(domain string) error {
	return sendEmail(
		"New HN submission for \""+domain+"\"",
		"New HN submission for \""+domain+"\". Please see:\n\n"+
			"https://news.ycombinator.com/newest\n",
	)
}

func sendEmail(subject, body string) error {
	auth := smtp.PlainAuth("", conf.SMTPLogin, conf.SMTPPassword, conf.SMTPServer)

	to := []string{conf.Recipient}
	payload := []byte("To: " + conf.Recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" + body + "\r\n")
	return smtp.SendMail(
		conf.SMTPServer+":"+conf.SMTPPort,
		auth, "hncheck@mutelight.org", to, payload)
}
